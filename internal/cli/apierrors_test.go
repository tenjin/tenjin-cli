package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A sign-in the API stopped accepting while this machine's clock still
// thinks it current is renewed, and the request sent again, once.
func TestASignInTheAPIRefusesIsRenewed(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	forget := func() {
		h.fake.mu.Lock()
		h.fake.jwts = map[string]bool{}
		h.fake.mu.Unlock()
	}

	forget()
	before, _ := h.store.Load("test")
	code, stdout, stderr := h.run("apps", "list")
	if code != 0 || !strings.Contains(stdout, "No apps.") {
		t.Fatalf("list: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if after, _ := h.store.Load("test"); after.JWT == before.JWT || after.RefreshToken == before.RefreshToken {
		t.Error("the sign-in was not renewed, or the new one was not saved")
	}

	// A write is sent again too: the refusal came before anything was done.
	forget()
	code, _, stderr = h.run("apps", "create", "--name", "Sample", "--bundle-id", "com.example.sample", "--platform", "android", "--yes")
	if code != 0 || len(h.fake.apps) != 1 {
		t.Errorf("create: exit = %d, apps = %d, stderr = %q", code, len(h.fake.apps), stderr)
	}

	// Refused again after renewing: reported, not tried a third time.
	asked := 0
	h.fake.data = map[string]func(*http.Request) (int, any){
		"/v2/campaigns": func(*http.Request) (int, any) {
			asked++
			return 498, map[string]string{"error": "Unauthorized. Your access token is no longer valid."}
		},
	}
	issued := h.fake.issued
	code, _, stderr = h.run("campaigns", "list", "--json")
	if code != 3 || errorBody(t, stderr)["code"] != "not_signed_in" || h.fake.issued != issued+1 || asked != 2 {
		t.Errorf("refused again: exit = %d, renewals = %d, requests = %d, stderr = %q", code, h.fake.issued-issued, asked, stderr)
	}
}

// A connection Tenjin closes early is Tenjin's failure, not input that ended
// at a prompt.
func TestAConnectionClosedEarlyIsUpstream(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()
	h.env.endpoints.API = srv.URL + "/v2"
	h.vars["TENJIN_API_TOKEN"] = "dashboard-token"
	code, _, stderr := h.run("apps", "list", "--json")
	if code != 7 || errorBody(t, stderr)["code"] != "upstream_error" {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// A token from TENJIN_API_TOKEN that Tenjin refuses is named as such, and
// not refreshed.
func TestARefusedAPITokenIsNamed(t *testing.T) {
	for _, token := range []string{"looks.like.jwt", "not-a-jwt"} {
		h := newHarness(t)
		h.vars["TENJIN_API_TOKEN"] = token
		code, _, stderr := h.run("apps", "list", "--json")
		body := errorBody(t, stderr)
		if code != 3 || body["code"] != "not_signed_in" || !strings.Contains(body["message"], "TENJIN_API_TOKEN") ||
			strings.Contains(body["message"], "saved sign-in") || strings.Contains(stderr, token) || !strings.Contains(body["hint"], "TENJIN_API_TOKEN") {
			t.Errorf("%s: exit = %d, stderr = %q", token, code, stderr)
		}
		if h.fake.issued != 0 {
			t.Errorf("%s: a refresh was tried", token)
		}
	}
}

// A token copied with its line break works; one with a space inside is
// refused before any request, without being shown.
func TestTheAPITokenIsReadAsTheDashboardShowsIt(t *testing.T) {
	h := newHarness(t)
	h.vars["TENJIN_API_TOKEN"] = "dashboard-token\n"
	if code, _, stderr := h.run("apps", "list"); code != 0 {
		t.Errorf("trailing line break: exit = %d, stderr = %q", code, stderr)
	}
	h.vars["TENJIN_API_TOKEN"] = "dashboard token"
	for _, args := range [][]string{{"apps", "list"}, {"auth", "status"}} {
		code, _, stderr := h.run(args...)
		if code != 2 || !strings.Contains(stderr, "TENJIN_API_TOKEN holds a space") || strings.Contains(stderr, "dashboard token") {
			t.Errorf("%v: exit = %d, stderr = %q", args, code, stderr)
		}
	}
}

// Anyone in an organization can name an app, and an answer from the API can
// say anything: what reaches a terminal carries no escape sequence, and no
// line break that passes for another field. JSON keeps the text as it is.
func TestTextFromTheAPIIsCleanedForPeople(t *testing.T) {
	const evil = "Evil\x1b]52;c;aGk=\x07\nBundle ID  com.fake"
	h := newHarness(t)
	app := h.fake.addApp(evil, "com.example.evil", "android")
	h.fake.serveKeys(app.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	h.signIn()
	h.fake.data = map[string]func(*http.Request) (int, any){
		"/v2/campaigns/" + unknownUUID: func(*http.Request) (int, any) {
			return http.StatusBadRequest, map[string]string{"error": "Could not \x1b]52;c;aGk=\x07read it"}
		},
	}
	clean := func(what, out string) {
		t.Helper()
		if strings.ContainsAny(out, "\x1b\x07") || strings.Contains(out, "\nBundle ID  com.fake") {
			t.Errorf("%s: %q", what, out)
		}
	}
	for _, args := range [][]string{
		{"apps", "list"},
		{"apps", "get", app.ID},
		{"apps", "keys", "list", "--app", app.ID},
		{"apps", "ensure", "--bundle-id", "com.example.evil", "--platform", "android"},
	} {
		code, stdout, stderr := h.run(args...)
		if code != 0 || !strings.Contains(stdout, "Evil]52;c;aGk=") {
			t.Errorf("%v: exit = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
		clean(strings.Join(args, " "), stdout)
	}
	code, _, stderr := h.run("apps", "create", "--name", "Other", "--bundle-id", "com.example.evil", "--platform", "android", "--yes")
	if code != 10 {
		t.Errorf("create: exit = %d, stderr = %q", code, stderr)
	}
	clean("already exists", stderr)

	code, _, stderr = h.run("campaigns", "get", unknownUUID)
	if code != 11 || !strings.Contains(stderr, "Could not ]52;c;aGk=read it") {
		t.Errorf("error: exit = %d, stderr = %q", code, stderr)
	}
	clean("error", stderr)
	_, _, stderr = h.run("campaigns", "get", unknownUUID, "--json")
	if !strings.Contains(stderr, `\u001b]52`) {
		t.Errorf("JSON error: %q", stderr)
	}
	if _, stdout, _ := h.run("apps", "get", app.ID, "--json"); !strings.Contains(stdout, `\u001b]52`) {
		t.Errorf("JSON app: %q", stdout)
	}
}
