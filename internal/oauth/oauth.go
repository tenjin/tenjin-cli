// Package oauth signs a user in to Tenjin through the browser.
//
// The flow is authorization code with PKCE and a loopback redirect, followed
// by a token exchange: Tenjin's API does not accept the OAuth access token
// itself, only the short-lived JWT it is exchanged for.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	grantTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeAccess    = "urn:ietf:params:oauth:token-type:access_token"
	scope              = "mcp"
)

// ClientID is this CLI's own client at Tenjin's authorization server, a
// public client: the browser sign-in proves who the user is, PKCE that the
// code is this process's. Its redirect is http://127.0.0.1/callback on any
// port: the server ignores the port for a loopback IP address, but not for
// the name "localhost".
const ClientID = "tenjin-cli"

// ErrGrantInvalid means the refresh token or access token is no longer good
// and the user has to sign in again.
var ErrGrantInvalid = errors.New("the saved sign-in is no longer valid")

// Client talks to one authorization server.
type Client struct {
	Issuer   string
	Resource string
	HTTP     *http.Client
}

// New returns a client for the issuer, requesting tokens for resource.
func New(issuer, resource string) *Client {
	return &Client{
		Issuer:   strings.TrimSuffix(issuer, "/"),
		Resource: resource,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Tokens are what the authorization server issues after sign-in or refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
}

// Identity is the API credential and who it belongs to.
type Identity struct {
	JWT              string
	Expiry           time.Time
	UserID           string
	OrganizationID   string
	OrganizationName string
}

type metadata struct {
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	Revocation    string `json:"revocation_endpoint"`
}

func (c *Client) discover(ctx context.Context) (metadata, error) {
	var m metadata
	status, body, err := c.do(ctx, http.MethodGet, c.Issuer+"/.well-known/oauth-authorization-server", "", "")
	if err != nil {
		return m, err
	}
	if status != http.StatusOK || json.Unmarshal(body, &m) != nil || m.Authorization == "" || m.Token == "" {
		return m, fmt.Errorf("%s does not describe an authorization server (HTTP %d)", c.Issuer, status)
	}
	return m, nil
}

// LoginOptions controls an interactive sign-in.
type LoginOptions struct {
	ClientID string
	// Open is called with the URL the user must visit. It should open a
	// browser, or show the URL when it cannot.
	Open func(authorizeURL string) error
	// Timeout bounds the wait for the user to finish in the browser.
	Timeout time.Duration
	// Pasted, when set, reads the address the browser ended on, pasted by
	// the user. It is for a browser on another machine, as over SSH, whose
	// redirect to this machine's loopback address cannot arrive. Whichever
	// comes first, the redirect or the paste, is used.
	Pasted func() (string, error)
}

// Login runs the browser sign-in and returns the tokens it produced.
func (c *Client) Login(ctx context.Context, opts LoginOptions) (Tokens, error) {
	m, err := c.discover(ctx)
	if err != nil {
		return Tokens{}, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Tokens{}, fmt.Errorf("could not listen for the sign-in callback: %w", err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Addr().(*net.TCPAddr).Port)
	verifier, state := randomString(48), randomString(24)
	challenge := sha256.Sum256([]byte(verifier))

	type callback struct {
		code string
		err  error
	}
	done := make(chan callback, 1)
	// check reads the authorization server's answer, however it arrived.
	check := func(q url.Values) callback {
		result := callback{code: q.Get("code")}
		switch {
		case q.Get("state") != state:
			result.err = errors.New("the sign-in response did not match this request")
		case q.Get("iss") != "" && q.Get("iss") != c.Issuer:
			result.err = fmt.Errorf("the sign-in response came from %s, not %s", q.Get("iss"), c.Issuer)
		case q.Get("error") != "":
			result.err = fmt.Errorf("sign-in was not completed: %s", firstNonEmpty(q.Get("error_description"), q.Get("error")))
		case result.code == "":
			result.err = errors.New("the sign-in response carried no authorization code")
		}
		return result
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		// A request without this sign-in's state is not its answer: anything
		// on this machine can reach the port. It is refused and the wait
		// goes on.
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Tenjin CLI: this is not the answer to the sign-in in progress.", http.StatusBadRequest)
			return
		}
		result := check(r.URL.Query())
		if result.err != nil {
			http.Error(w, "Tenjin CLI: sign-in failed. You can close this tab and return to the terminal.", http.StatusBadRequest)
		} else {
			_, _ = io.WriteString(w, "Tenjin CLI: signed in. You can close this tab and return to the terminal.")
		}
		select {
		case done <- result:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	authorizeURL := m.Authorization + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {opts.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {scope},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"resource":              {c.Resource},
	}.Encode()
	if err := opts.Open(authorizeURL); err != nil {
		return Tokens{}, err
	}
	if opts.Pasted != nil {
		go func() {
			for {
				line, err := opts.Pasted()
				if err != nil {
					return
				}
				u, perr := url.Parse(strings.TrimSpace(line))
				if perr != nil || !strings.HasSuffix(u.Path, "/callback") {
					continue // not the address; wait for the next line
				}
				select {
				case done <- check(u.Query()):
				default:
				}
				return
			}
		}()
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	var result callback
	select {
	case result = <-done:
	case <-time.After(timeout):
		return Tokens{}, fmt.Errorf("timed out after %s waiting for sign-in in the browser", timeout)
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	}
	if result.err != nil {
		return Tokens{}, result.err
	}

	return c.token(ctx, m.Token, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {result.code},
		"redirect_uri":  {redirect},
		"client_id":     {opts.ClientID},
		"code_verifier": {verifier},
	})
}

// Refresh trades a refresh token for new tokens. The refresh token rotates:
// the one passed in stops working.
func (c *Client) Refresh(ctx context.Context, clientID, refreshToken string) (Tokens, error) {
	m, err := c.discover(ctx)
	if err != nil {
		return Tokens{}, err
	}
	return c.token(ctx, m.Token, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	})
}

func (c *Client) token(ctx context.Context, endpoint string, form url.Values) (Tokens, error) {
	status, body, err := c.do(ctx, http.MethodPost, endpoint, "application/x-www-form-urlencoded", form.Encode())
	if err != nil {
		return Tokens{}, err
	}
	var t struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if status == http.StatusOK && json.Unmarshal(body, &t) == nil && t.AccessToken != "" {
		return Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}, nil
	}
	if success(status) {
		// A success's body can hold tokens; it is never shown.
		return Tokens{}, fmt.Errorf("the token request answered HTTP %d without a token", status)
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		return Tokens{}, fmt.Errorf("%w: %s", ErrGrantInvalid, describe(body))
	}
	return Tokens{}, fmt.Errorf("token request failed (HTTP %d): %s", status, describe(body))
}

// Exchange trades an OAuth access token for the JWT the API accepts.
func (c *Client) Exchange(ctx context.Context, accessToken string) (Identity, error) {
	m, err := c.discover(ctx)
	if err != nil {
		return Identity{}, err
	}
	status, body, err := c.do(ctx, http.MethodPost, m.Token, "application/x-www-form-urlencoded", url.Values{
		"grant_type":         {grantTokenExchange},
		"subject_token":      {accessToken},
		"subject_token_type": {tokenTypeAccess},
		"resource":           {c.Resource},
	}.Encode())
	if err != nil {
		return Identity{}, err
	}
	var ex struct {
		AccessToken      string          `json:"access_token"`
		UserID           string          `json:"user_id"`
		OrganizationID   string          `json:"organization_id"`
		OrganizationName string          `json:"organization_name"`
		ExpiresIn        json.RawMessage `json:"expires_in"`
	}
	if success(status) && (status != http.StatusOK || json.Unmarshal(body, &ex) != nil || ex.AccessToken == "") {
		// A success's body can hold the token; it is never shown.
		return Identity{}, fmt.Errorf("the token exchange answered HTTP %d without a token", status)
	}
	if status != http.StatusOK {
		if status == http.StatusBadRequest || status == http.StatusUnauthorized {
			return Identity{}, fmt.Errorf("%w: %s", ErrGrantInvalid, describe(body))
		}
		return Identity{}, fmt.Errorf("token exchange failed (HTTP %d): %s", status, describe(body))
	}
	expiry, err := tokenExpiry(ex.ExpiresIn, ex.AccessToken, time.Now())
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		JWT:              ex.AccessToken,
		Expiry:           expiry,
		UserID:           ex.UserID,
		OrganizationID:   ex.OrganizationID,
		OrganizationName: ex.OrganizationName,
	}, nil
}

// Revoke invalidates a token at the authorization server. For ClientID it
// also ends the API access the token was exchanged for, at once; for a
// client an older version registered, a JWT already exchanged keeps working
// until it expires.
func (c *Client) Revoke(ctx context.Context, clientID, token string) error {
	m, err := c.discover(ctx)
	if err != nil {
		return err
	}
	status, body, err := c.do(ctx, http.MethodPost, m.Revocation, "application/x-www-form-urlencoded", url.Values{
		"token":     {token},
		"client_id": {clientID},
	}.Encode())
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("revocation failed (HTTP %d): %s", status, describe(body))
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, endpoint, contentType, body string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("could not reach %s: %w", c.Issuer, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, err
}

// jwtExpiry reads the exp claim without verifying the signature. The server
// verifies it on every call; the client only needs to know when to refresh.
func jwtExpiry(jwt string) (time.Time, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return time.Time{}, errors.New("the exchanged token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, errors.New("the exchanged token has an unreadable payload")
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, errors.New("the exchanged token has no expiry")
	}
	return time.Unix(claims.Exp, 0), nil
}

// maxExpiresIn is the longest expires_in believed. An API token lasts hours;
// a value past this is an error on the server's side, not a lifetime.
const maxExpiresIn = 30 * 24 * time.Hour

// tokenExpiry is when the exchanged token ends: as the answer's expires_in
// says, but never after the token's own expiry. An older server only says it
// inside the token.
func tokenExpiry(raw json.RawMessage, jwt string, now time.Time) (time.Time, error) {
	own, ownErr := jwtExpiry(jwt)
	expiry, ok := expiresIn(raw, now)
	switch {
	case ok && ownErr == nil && own.Before(expiry):
		return own, nil
	case ok:
		return expiry, nil
	default:
		return own, ownErr
	}
}

// expiresIn reads an expires_in, a number of seconds or a string holding
// one, as the time it ends. ok is false when there is none to read, or it is
// not a number of seconds a token could last.
func expiresIn(raw json.RawMessage, now time.Time) (time.Time, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	seconds, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > maxExpiresIn.Seconds() {
		return time.Time{}, false
	}
	return now.Add(time.Duration(seconds * float64(time.Second))), true
}

func success(status int) bool {
	return status >= 200 && status < 300
}

// describe turns an OAuth error body into one line.
func describe(body []byte) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return firstNonEmpty(e.Description, e.Error)
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no entropy; nothing can be done safely
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
