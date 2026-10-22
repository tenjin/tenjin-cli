package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
