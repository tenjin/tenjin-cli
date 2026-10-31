// Package transport is how the CLI's HTTP clients reach Tenjin: a request
// that is safe to send twice is retried when it fails in transit, and with
// --verbose every request is written to a log. The log never holds a
// credential or a request body.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// attempts is how many times a retryable request is sent, at most.
const attempts = 3

// Transport wraps another http.RoundTripper.
type Transport struct {
	Base http.RoundTripper
	// Log receives a line per request sent. Nil logs nothing.
	Log io.Writer
	// Blocked says why connections cannot be made, when that is known, as
	// in a sandbox without network: a connection that fails then fails
	// with a *BlockedError that says so, and is not tried again.
	Blocked string
	// wait pauses between attempts; tests replace it.
	wait func(ctx context.Context, d time.Duration) error
	now  func() time.Time
}

// New returns a transport over Go's default one, logging to log if it is
// not nil.
func New(log io.Writer) *Transport {
	return &Transport{Base: http.DefaultTransport, Log: log}
}

type retryableKey struct{}

// Retryable marks the requests made with ctx as safe to send again although
// their method is not GET, because sending one twice has no further effect.
func Retryable(ctx context.Context) context.Context {
	return context.WithValue(ctx, retryableKey{}, true)
}

func retryable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	marked, _ := req.Context().Value(retryableKey{}).(bool)
	// A body that cannot be read again cannot be sent again.
	return marked && (req.Body == nil || req.GetBody != nil)
}

// RoundTrip sends the request, again after a failure in transit or a
// gateway error when that is safe, and logs each attempt.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	tries := 1
	if retryable(req) {
		tries = attempts
	}
	for attempt := 1; ; attempt++ {
		send := req
		if attempt > 1 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			send = req.Clone(req.Context())
			send.Body = body
		}
		start := t.clock()
		resp, err := t.base().RoundTrip(send)
		if err != nil && t.Blocked != "" && connectionFailed(err) {
			err = &BlockedError{Reason: t.Blocked, Err: err}
		}
		var blocked *BlockedError
		again := attempt < tries && !errors.As(err, &blocked) && worthRetrying(req.Context(), resp, err)
		t.log(send, resp, err, t.clock().Sub(start), attempt, tries, again)
		if !again {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
		}
		// 0.5s, then 1.5s: long enough for a restarting server, short
		// enough that a person at the terminal does not give up.
		if err := t.pause(req.Context(), time.Duration(attempt*attempt)*500*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

// BlockedError is a connection that failed where the network is known to
// be blocked. Its message gives the reason first, since that is what to act
// on, then the failure itself.
type BlockedError struct {
	Reason string
	Err    error
}

func (e *BlockedError) Error() string { return e.Reason + " (" + e.Err.Error() + ")" }
func (e *BlockedError) Unwrap() error { return e.Err }

// connectionFailed reports whether err is a failure to reach the server at
// all: a name that could not be looked up, or a connection not made.
func connectionFailed(err error) bool {
	var op *net.OpError
	var dns *net.DNSError
	return errors.As(err, &op) || errors.As(err, &dns)
}

// worthRetrying reports whether another attempt could succeed: the request
// failed before an answer came, or a gateway in front of the service failed.
func worthRetrying(ctx context.Context, resp *http.Response, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// log writes one line for an attempt. The address is written without any
// user information, and nothing from the headers or the body is.
func (t *Transport) log(req *http.Request, resp *http.Response, err error, took time.Duration, attempt, tries int, again bool) {
	if t.Log == nil {
		return
	}
	u := *req.URL
	u.User = nil
	outcome := ""
	switch {
	case err != nil:
		outcome = "failed: " + err.Error()
	default:
		outcome = fmt.Sprintf("%d", resp.StatusCode)
	}
	line := fmt.Sprintf("tenjin: %s %s → %s in %s", req.Method, u.String(), outcome, took.Round(time.Millisecond))
	if tries > 1 && (again || attempt > 1) {
		line += fmt.Sprintf(" (attempt %d of %d)", attempt, tries)
	}
	_, _ = fmt.Fprintln(t.Log, line)
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *Transport) pause(ctx context.Context, d time.Duration) error {
	if t.wait != nil {
		return t.wait(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
