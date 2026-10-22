package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
