package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// server answers with the given statuses in turn, then 200, and counts what
// it received.
func server(t *testing.T, statuses ...int) (*httptest.Server, *int32, *[]string) {
	t.Helper()
	var calls int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if int(n) <= len(statuses) {
			w.WriteHeader(statuses[n-1])
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &bodies
}

func client(log io.Writer) (*http.Client, *[]time.Duration) {
	var waits []time.Duration
	tr := New(log)
	tr.wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return &http.Client{Transport: tr}, &waits
}

func TestAGetIsRetriedAfterAGatewayError(t *testing.T) {
	srv, calls, _ := server(t, http.StatusBadGateway, http.StatusServiceUnavailable)
	c, waits := client(nil)
	resp, err := c.Get(srv.URL)
	if err != nil || resp.StatusCode != http.StatusOK || *calls != 3 {
		t.Fatalf("status = %v, err = %v, calls = %d", resp, err, *calls)
	}
	_ = resp.Body.Close()
	if len(*waits) != 2 || (*waits)[0] != 500*time.Millisecond || (*waits)[1] != 2*time.Second {
		t.Errorf("waits = %v", *waits)
	}
}

func TestRetriesStopAtThree(t *testing.T) {
	srv, calls, _ := server(t, 503, 503, 503, 503)
	c, _ := client(nil)
	resp, err := c.Get(srv.URL)
	if err != nil || resp.StatusCode != 503 || *calls != 3 {
		t.Fatalf("status = %v, err = %v, calls = %d", resp, err, *calls)
	}
	_ = resp.Body.Close()
}

// An answer from the service itself, even a failure, is final: retrying a
// 500 or a 429 would only repeat it.
func TestServiceAnswersAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500} {
		srv, calls, _ := server(t, status)
		c, _ := client(nil)
		resp, err := c.Get(srv.URL)
		if err != nil || resp.StatusCode != status || *calls != 1 {
			t.Fatalf("%d: calls = %d, err = %v", status, *calls, err)
		}
		_ = resp.Body.Close()
	}
}

// A POST may have done its work before the failure; it is sent once unless
// the caller says sending it again is harmless.
func TestAPostIsRetriedOnlyWhenMarked(t *testing.T) {
	srv, calls, _ := server(t, 503)
	c, _ := client(nil)
	resp, err := c.Post(srv.URL, "text/plain", strings.NewReader("x"))
	if err != nil || resp.StatusCode != 503 || *calls != 1 {
		t.Fatalf("unmarked: status = %v, err = %v, calls = %d", resp, err, *calls)
	}
	_ = resp.Body.Close()

	srv, calls, bodies := server(t, 503)
	req, _ := http.NewRequestWithContext(Retryable(context.Background()), http.MethodPost, srv.URL, strings.NewReader("form=1"))
	resp, err = c.Do(req)
	if err != nil || resp.StatusCode != 200 || *calls != 2 {
		t.Fatalf("marked: status = %v, err = %v, calls = %d", resp, err, *calls)
	}
	_ = resp.Body.Close()
	// The body is sent whole every time.
	if strings.Join(*bodies, "|") != "form=1|form=1" {
		t.Errorf("bodies = %q", *bodies)
	}
}

func TestAConnectionFailureIsRetried(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	var log bytes.Buffer
	c, waits := client(&log)
	resp, err := c.Get(url)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected an error")
	}
	if len(*waits) != 2 || strings.Count(log.String(), "failed:") != 3 || !strings.Contains(log.String(), "(attempt 3 of 3)") {
		t.Errorf("waits = %v, log:\n%s", *waits, log.String())
	}
}

func TestACancelledRequestIsNotRetried(t *testing.T) {
	srv, calls, _ := server(t, 503)
	ctx, cancel := context.WithCancel(context.Background())
	tr := New(nil)
	tr.wait = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	if err == nil || *calls != 1 {
		t.Errorf("err = %v, calls = %d", err, *calls)
	}
}

// The log is for showing to support. It has what was asked and what came
// back, and none of the credentials that went with it.
func TestTheLogHoldsNoCredential(t *testing.T) {
	srv, _, _ := server(t)
	var log bytes.Buffer
	c, _ := client(&log)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v0/event?platform=android", strings.NewReader("advertising_id=SECRET-BODY"))
	req.SetBasicAuth("SDKKEYSECRET", "")
	req.Header.Set("Authorization", "Bearer TOKENSECRET")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	line := log.String()
	if !strings.HasPrefix(line, "tenjin: POST "+srv.URL+"/v0/event?platform=android → 200 in ") {
		t.Errorf("log = %q", line)
	}
	for _, secret := range []string{"SECRET-BODY", "SDKKEYSECRET", "TOKENSECRET"} {
		if strings.Contains(line, secret) {
			t.Errorf("the log holds %s: %q", secret, line)
		}
	}

	// A user name in the address is left out too.
	log.Reset()
	withUser := strings.Replace(srv.URL, "http://", "http://someone:password@", 1)
	resp, err = c.Get(withUser)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if strings.Contains(log.String(), "password") || strings.Contains(log.String(), "someone") {
		t.Errorf("log = %q", log.String())
	}
}

// failing is a transport whose every request fails with err.
type failing struct {
	err   error
	calls int
}

func (f *failing) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return nil, f.err
}

// Where the network is known to be blocked, a connection that fails says
// why, once: trying again cannot help.
func TestABlockedConnectionSaysWhy(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: operation not permitted")}
	for _, tc := range []struct {
		name    string
		blocked string
		err     error
		calls   int
		says    bool
	}{
		{"a failed connection, blocked", "the sandbox has no network", refused, 1, true},
		{"a failed lookup, blocked", "the sandbox has no network", &net.DNSError{Err: "no such host", Name: "api.tenjin.com"}, 1, true},
		{"a failed connection, not known to be blocked", "", refused, 3, false},
		{"another failure, blocked", "the sandbox has no network", errors.New("tls: handshake failure"), 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &failing{err: tc.err}
			tr := &Transport{Base: base, Blocked: tc.blocked, wait: func(context.Context, time.Duration) error { return nil }}
			resp, err := (&http.Client{Transport: tr}).Get("https://api.tenjin.com/v2/apps")
			if err == nil {
				_ = resp.Body.Close()
			}
			var blocked *BlockedError
			if base.calls != tc.calls || errors.As(err, &blocked) != tc.says || !errors.Is(err, tc.err) {
				t.Fatalf("calls = %d, err = %v", base.calls, err)
			}
			if tc.says && !strings.Contains(err.Error(), "the sandbox has no network (") {
				t.Errorf("err = %v", err)
			}
		})
	}
}
