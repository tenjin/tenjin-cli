package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// server is an authorization server whose token endpoint answers with the
// given status and body.
func server(t *testing.T, status int, body string) *Client {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"revocation_endpoint":    srv.URL + "/revoke",
		})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL, "https://resource.test")
}

// approving opens the sign-in as a browser would, and approves it: the
// authorization server sends it back to the CLI's loopback address with a
// code and the request's state.
func approving(t *testing.T) func(string) error {
	return func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"code": {"code-1"}, "state": {q.Get("state")}}.Encode())
			if err != nil {
				t.Errorf("callback: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
		return nil
	}
}

// redeeming is an authorization server that counts the codes traded at its
// token endpoint.
func redeeming(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	var redeemed atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		redeemed.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh"}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL, "https://resource.test"), &redeemed
}

// BeforeRedeem runs once the browser has answered and before the code is
// spent; when it refuses, the code is never traded for tokens.
func TestBeforeRedeemComesBeforeTheCodeIsSpent(t *testing.T) {
	c, redeemed := redeeming(t)

	refused := errors.New("not now")
	_, err := c.Login(context.Background(), LoginOptions{ClientID: ClientID, Open: approving(t), Timeout: 5 * time.Second,
		BeforeRedeem: func(context.Context) error { return refused }})
	if !errors.Is(err, refused) || redeemed.Load() != 0 {
		t.Errorf("Login = %v, with the code redeemed %d times", err, redeemed.Load())
	}

	var before int32 = -1
	tokens, err := c.Login(context.Background(), LoginOptions{ClientID: ClientID, Open: approving(t), Timeout: 5 * time.Second,
		BeforeRedeem: func(context.Context) error { before = redeemed.Load(); return nil }})
	if err != nil || tokens.RefreshToken != "refresh" || before != 0 || redeemed.Load() != 1 {
		t.Errorf("Login = %+v, %v; redeemed %d times, %d before BeforeRedeem", tokens, err, redeemed.Load(), before)
	}
}

// answer is the address the sign-in ends on, with a code and the request's
// state, as edit changes it.
func answer(authorizeURL string, edit func(url.Values)) (string, error) {
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", err
	}
	q := url.Values{"code": {"code-1"}, "state": {u.Query().Get("state")}}
	edit(q)
	return u.Query().Get("redirect_uri") + "?" + q.Encode(), nil
}

// An answer that is not to this sign-in, with another request's state or
// from another authorization server, is refused, however it arrives, and its
// code is never traded for tokens. A redirect with another state is not even
// taken as the answer: anything on this machine can reach the port, so the
// wait goes on.
func TestAnAnswerToAnotherSignInIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(url.Values)
		// want is the error when the answer is pasted, and redirected
		// when it arrives at the loopback address.
		want, redirected string
	}{
		{"another state", func(q url.Values) { q.Set("state", "another-request") }, "did not match this request", "timed out"},
		{"another issuer", func(q url.Values) { q.Set("iss", "https://elsewhere.test") }, "came from https://elsewhere.test", "came from https://elsewhere.test"},
	} {
		t.Run(c.name+", pasted", func(t *testing.T) {
			client, redeemed := redeeming(t)
			opened := make(chan string, 1)
			pasted := false
			_, err := client.Login(context.Background(), LoginOptions{ClientID: ClientID, Timeout: 5 * time.Second,
				Open: func(authorizeURL string) error { opened <- authorizeURL; return nil },
				Pasted: func() (string, error) {
					if pasted {
						return "", io.EOF
					}
					pasted = true
					return answer(<-opened, c.edit)
				}})
			if err == nil || !strings.Contains(err.Error(), c.want) || redeemed.Load() != 0 {
				t.Errorf("Login = %v, with the code redeemed %d times", err, redeemed.Load())
			}
		})
		t.Run(c.name+", redirected", func(t *testing.T) {
			client, redeemed := redeeming(t)
			status := make(chan int, 1)
			_, err := client.Login(context.Background(), LoginOptions{ClientID: ClientID, Timeout: 500 * time.Millisecond,
				Open: func(authorizeURL string) error {
					address, err := answer(authorizeURL, c.edit)
					if err != nil {
						return err
					}
					go func() {
						resp, err := http.Get(address)
						if err != nil {
							t.Errorf("callback: %v", err)
							status <- 0
							return
						}
						_ = resp.Body.Close()
						status <- resp.StatusCode
					}()
					return nil
				}})
			if err == nil || !strings.Contains(err.Error(), c.redirected) || redeemed.Load() != 0 {
				t.Errorf("Login = %v, with the code redeemed %d times", err, redeemed.Load())
			}
			if got := <-status; got != http.StatusBadRequest {
				t.Errorf("the callback answered HTTP %d", got)
			}
		})
	}
}

// Any successful answer can carry tokens, whatever its status: an error
// about it never shows its body.
func TestASuccessfulAnswersBodyIsNeverShown(t *testing.T) {
	const secret = "SECRET-TOKEN-VALUE"
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNonAuthoritativeInfo} {
		for _, body := range []string{
			`{"access_token":"` + secret + `","refresh_token":"` + secret + `","token_type":"Bearer"`,
			`{"refresh_token":"` + secret + `"}`,
			`{"access_token":"` + secret + `","refresh_token":"` + secret + `"}`,
		} {
			if status == http.StatusOK && strings.HasPrefix(body, `{"access_token"`) && strings.HasSuffix(body, "}") {
				continue // a good answer
			}
			c := server(t, status, body)
			_, err := c.Refresh(context.Background(), ClientID, "refresh")
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Errorf("token request, HTTP %d, %s: err = %v", status, body, err)
			}
			_, err = c.Exchange(context.Background(), "access")
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Errorf("exchange, HTTP %d, %s: err = %v", status, body, err)
			}
		}
	}
}

// An error answer is described, so the user can tell what went wrong.
func TestAnErrorAnswerIsDescribed(t *testing.T) {
	c := server(t, http.StatusServiceUnavailable, `{"error":"temporarily_unavailable","error_description":"try later"}`)
	if _, err := c.Refresh(context.Background(), ClientID, "refresh"); err == nil || !strings.Contains(err.Error(), "try later") {
		t.Errorf("token request: err = %v", err)
	}
	if _, err := c.Exchange(context.Background(), "access"); err == nil || !strings.Contains(err.Error(), "try later") {
		t.Errorf("exchange: err = %v", err)
	}
}

func jwtWith(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "eyJhbGciOiJFUzUxMiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// The exchange's expires_in is believed only as a number of seconds a token
// could last, and never past the token's own expiry.
func TestTheExchangedTokensExpiry(t *testing.T) {
	inTwoHours := time.Now().Add(2 * time.Hour)
	withExp, withoutExp := jwtWith(map[string]any{"exp": inTwoHours.Unix()}), jwtWith(map[string]any{"sub": "user"})
	for _, tc := range []struct {
		jwt, expiresIn string
		want           time.Duration // from now; 0 means an error
	}{
		{withExp, `600`, 10 * time.Minute},
		{withExp, `"600"`, 10 * time.Minute},
		{withoutExp, `600`, 10 * time.Minute},
		{"not-a-jwt", `600`, 10 * time.Minute},
		// Longer than the token itself lasts: the token's expiry stands.
		{withExp, `86400`, 2 * time.Hour},
		// Not a lifetime: the token's expiry stands, or there is none.
		{withExp, `"NaN"`, 2 * time.Hour},
		{withExp, `"Inf"`, 2 * time.Hour},
		{withExp, `"-Inf"`, 2 * time.Hour},
		{withExp, `1e300`, 2 * time.Hour},
		{withExp, `9.3e9`, 2 * time.Hour},
		{withExp, `0`, 2 * time.Hour},
		{withExp, `-5`, 2 * time.Hour},
		{withExp, `null`, 2 * time.Hour},
		{withoutExp, `"NaN"`, 0},
		{withoutExp, `1e300`, 0},
		{withoutExp, `"Inf"`, 0},
	} {
		body := `{"access_token":"` + tc.jwt + `","expires_in":` + tc.expiresIn + `}`
		identity, err := server(t, http.StatusOK, body).Exchange(context.Background(), "access")
		if tc.want == 0 {
			if err == nil {
				t.Errorf("%s, expires_in %s: expiry %v, want an error", tc.jwt, tc.expiresIn, identity.Expiry)
			}
			continue
		}
		if got := time.Until(identity.Expiry); err != nil || got < tc.want-time.Minute || got > tc.want+time.Minute {
			t.Errorf("%s, expires_in %s: lasts %v (%v), want about %v", tc.jwt, tc.expiresIn, got.Round(time.Second), err, tc.want)
		}
	}
}
