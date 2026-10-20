package tenjinapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

func failed(status int, body string) *clierr.Error {
	return clierr.From(apiError(&http.Response{StatusCode: status, Header: http.Header{"X-Retry-After": {"30"}}}, []byte(body)))
}

// Each answer the API gives for a failure is reported as what it is.
func TestTheAPIsRefusalsAreToldApart(t *testing.T) {
	for _, c := range []struct {
		status        int
		body          string
		kind          clierr.Kind
		message, hint string
	}{
		{401, `{"error":"Unauthorized. Invalid access token provided."}`, clierr.NotSignedIn, "saved sign-in", "tenjin login"},
		{498, `{"error":"Unauthorized. Your access token is no longer valid."}`, clierr.NotSignedIn, "saved sign-in", "tenjin login"},
		{403, `{"error":"AuthHandler::DecodeError"}`, clierr.NotSignedIn, "saved sign-in", "tenjin login"},
		// A role: the API's message says nothing more, so the hint does.
		{403, `{"error":"Unauthorized. You do not have proper access. Contact support@tenjin.com if you are receiving this in error."}`, clierr.Permission, "your Tenjin role does not allow this", "your role"},
		// Anything else the API names is passed on, with no word of roles.
		{403, `{"error":"This organization does not have access to S2S callbacks"}`, clierr.Permission, "does not have access to S2S callbacks", ""},
		{404, `{"error":"Not Found"}`, clierr.NotFound, "Not Found", ""},
		// The API says "not found" with a 400.
		{400, `{"error":"App not found"}`, clierr.NotFound, "App not found", ""},
		{400, `{"error":"S2S callback with id: 12 not found"}`, clierr.NotFound, "with id: 12 not found", ""},
		{400, `{"error":"Event purchase not found for this app"}`, clierr.NotFound, "Event purchase", ""},
		{400, `{"error":"Channel does not exist"}`, clierr.NotFound, "Channel does not exist", ""},
		// A refusal that mentions something missing is still a refusal.
		{400, `{"error":"Could not create app from store_url: com.example not found on the App Store"}`, clierr.Refused, "Could not create app from store_url", ""},
		{400, `{"error":"Could not create app from store_url: App not found"}`, clierr.Refused, "Could not create app", ""},
		{400, `{"error":"App not updated, invalid record, Store not found"}`, clierr.Refused, "App not updated", ""},
		{400, `{"error":"Could not create app: {:name=>[\"has already been taken\"]}"}`, clierr.Refused, "has already been taken", ""},
		{400, `{"error":"channel_id is invalid"}`, clierr.Refused, "channel_id is invalid", ""},
		{400, `{"error":"Memory limit exceeded. Ask for less."}`, clierr.Refused, "Memory limit exceeded", ""},
		{422, ``, clierr.Refused, "HTTP 422", ""},
		{413, `<html><body>Request Entity Too Large</body></html>`, clierr.Refused, "HTTP 413", ""},
		{429, `{"error":"Throttle limit reached"}`, clierr.RateLimited, "rate limiting", "Retry in 30 seconds"},
		// Maintenance only when the API says so.
		{503, `{"error":"This action is currently unavailable due to maintenance. Please try again later."}`, clierr.Upstream, "read-only maintenance: This action", ""},
		{503, `<html><body><h1>503 Service Temporarily Unavailable</h1></body></html>`, clierr.Upstream, "unavailable (HTTP 503)", "Try again"},
		{502, `<html>Bad gateway</html>`, clierr.Upstream, "unavailable (HTTP 502)", "Try again"},
		{302, ``, clierr.Upstream, "HTTP 302", ""},
	} {
		e := failed(c.status, c.body)
		if e.Kind != c.kind || !strings.Contains(e.Message, c.message) || !strings.Contains(e.Hint, c.hint) || (c.hint == "" && e.Hint != "") {
			t.Errorf("%d %s: %s %q, hint %q", c.status, c.body, e.Kind, e.Message, e.Hint)
		}
		if strings.Contains(e.Message, "<") || strings.Contains(e.Message+e.Hint, "manager") {
			t.Errorf("%d %s: message %q, hint %q", c.status, c.body, e.Message, e.Hint)
		}
	}
}

// renewing is a token source whose token the API stops accepting, as a
// sign-in this machine's clock misjudged.
type renewing struct {
	token    string
	renewals int
	err      error
}

func (r *renewing) Token(context.Context) (string, error) { return r.token, nil }
func (r *renewing) Renew(_ context.Context, refused string) (string, error) {
	r.renewals++
	if r.err != nil {
		return "", r.err
	}
	if refused == r.token {
		r.token = "fresh"
	}
	return r.token, nil
}

// api is a Tenjin API that accepts only the token "fresh", answering any
// other with status.
func api(t *testing.T, status int, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*seen = append(*seen, r.Method+" "+r.Header.Get("Authorization")+" "+string(body))
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"Unauthorized. Your access token is no longer valid."}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"id":"00000000-0000-4000-8000-000000000001","attributes":{"name":"Sample","bundle_id":"com.example","platform":"android"}}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A token the API refuses is renewed and the request sent again, once: a
// write too, since a refused token means the request was not acted on.
func TestARefusedTokenIsRenewedAndTheRequestSentOnceMore(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, 498} {
		var seen []string
		tokens := &renewing{token: "lapsed"}
		c := New(api(t, status, &seen).URL, "test", tokens)
		app, created, err := c.CreateApp(context.Background(), NewApp{Name: "Sample", BundleID: "com.example", Platform: "android"})
		if err != nil || !created || app.Name != "Sample" {
			t.Fatalf("%d: %+v, %v, %v", status, app, created, err)
		}
		if len(seen) != 2 || !strings.HasPrefix(seen[1], "POST Bearer fresh {") || !strings.Contains(seen[1], `"bundle_id":"com.example"`) || tokens.renewals != 1 {
			t.Errorf("%d: requests %q, renewals %d", status, seen, tokens.renewals)
		}
	}
}

// A token that stays refused after renewing is reported, without a loop.
func TestATokenRefusedAgainIsNotRenewedTwice(t *testing.T) {
	var seen []string
	tokens := &renewing{token: "lapsed", err: clierr.New(clierr.NotSignedIn, "Your sign-in has expired or was revoked.")}
	c := New(api(t, http.StatusUnauthorized, &seen).URL, "test", tokens)
	_, err := c.ListApps(context.Background(), "")
	if e := clierr.From(err); e.Kind != clierr.NotSignedIn || !strings.Contains(e.Message, "expired or was revoked") {
		t.Errorf("err = %v", err)
	}
	if len(seen) != 1 || tokens.renewals != 1 {
		t.Errorf("requests %q, renewals %d", seen, tokens.renewals)
	}
}

// fixed is a token the user gave: it cannot be renewed, and says so.
type fixed struct{}

func (fixed) Token(context.Context) (string, error) { return "given", nil }
func (fixed) Refused() *clierr.Error {
	return clierr.New(clierr.NotSignedIn, "Tenjin did not accept the token in TENJIN_API_TOKEN")
}

func TestARefusedTokenIsDescribedByItsSource(t *testing.T) {
	var seen []string
	c := New(api(t, http.StatusUnauthorized, &seen).URL, "test", fixed{})
	_, err := c.ListApps(context.Background(), "")
	if e := clierr.From(err); e.Kind != clierr.NotSignedIn || !strings.Contains(e.Message, "TENJIN_API_TOKEN") {
		t.Errorf("err = %v", err)
	}
	if len(seen) != 1 {
		t.Errorf("requests %q", seen)
	}
}

// An answer larger than the CLI reads is reported as that, not as an answer
// cut short that is then not JSON.
func TestAnAnswerTooLargeIsSaidToBe(t *testing.T) {
	defer func(was int64) { maxResponse = was }(maxResponse)
	maxResponse = 64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[`+strings.Repeat(`{"id":"x"},`, 20)+`{"id":"x"}]}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "test", fixed{}).ListApps(context.Background(), "")
	if !errors.Is(err, errTooLarge) || !strings.Contains(err.Error(), "larger than") || clierr.From(err).Kind != clierr.Upstream {
		t.Errorf("err = %v", err)
	}
	// Exactly the cap is not too large.
	body := `{"data":[]}`
	maxResponse = int64(len(body))
	exact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
	defer exact.Close()
	if apps, err := New(exact.URL, "test", fixed{}).ListApps(context.Background(), ""); err != nil || len(apps) != 0 {
		t.Errorf("exactly the cap: %v, %v", apps, err)
	}
}
