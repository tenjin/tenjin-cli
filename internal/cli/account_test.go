package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/session"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func TestLoginStatusLogout(t *testing.T) {
	h := newHarness(t)

	code, stdout, _ := h.run("auth", "status")
	if code != 3 || !strings.Contains(stdout, "Not signed in") {
		t.Fatalf("before login: exit = %d, stdout = %q", code, stdout)
	}

	code, stdout, stderr := h.run("login")
	if code != 0 || !strings.Contains(stdout, "Signed in to Acme Games") {
		t.Fatalf("login: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// The sign-in URL is narration, so it goes to stderr.
	if !strings.Contains(stderr, "/oauth/authorize?") || strings.Contains(stdout, "/oauth/authorize") {
		t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
	}

	code, stdout, _ = h.run("auth", "status", "--json")
	var status session.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil || code != 0 {
		t.Fatalf("status: exit = %d, err = %v, stdout = %q", code, err, stdout)
	}
	if !status.SignedIn || status.OrganizationName != "Acme Games" || status.Source != session.SourceLogin || status.TokenExpiry == nil {
		t.Errorf("status = %+v", status)
	}

	code, stdout, _ = h.run("logout")
	if code != 0 || !strings.Contains(stdout, "expires within two hours") || !strings.Contains(stdout, "/dashboard/access_tokens") {
		t.Errorf("logout: exit = %d, stdout = %q", code, stdout)
	}
	if code, _, _ = h.run("auth", "status"); code != 3 {
		t.Errorf("after logout: exit = %d, want 3", code)
	}
	if code, stdout, _ = h.run("logout"); code != 0 || !strings.Contains(stdout, "Not signed in") {
		t.Errorf("second logout: exit = %d, stdout = %q", code, stdout)
	}
}

func TestLoginNeverPrintsACredential(t *testing.T) {
	h := newHarness(t)
	_, stdout, stderr := h.run("login", "--json")
	_, statusOut, _ := h.run("auth", "status", "--json")
	creds, _ := h.store.Load("test")
	for _, secret := range []string{creds.JWT, creds.RefreshToken} {
		if secret == "" {
			t.Fatal("login stored no credential")
		}
		if strings.Contains(stdout+stderr+statusOut, secret) {
			t.Error("a credential was printed")
		}
	}
}

// The client registration survives sign-out, so signing in again does not
// register a second client with the server.
func TestLoginReusesItsClientRegistration(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.run("logout")
	h.signIn()
	if h.fake.registrations != 1 {
		t.Errorf("registrations = %d, want 1", h.fake.registrations)
	}
}

func TestLoginNoBrowserPrintsTheURL(t *testing.T) {
	h := newHarness(t)
	h.env.loginTimeout = 200 * time.Millisecond
	opened := false
	h.env.openBrowser = func(string) error { opened = true; return nil }

	code, _, stderr := h.run("login", "--no-browser")
	if opened {
		t.Error("the browser was opened despite --no-browser")
	}
	// Nobody visits the URL, so the wait times out.
	if code != 3 || !strings.Contains(stderr, "Open this URL") || !strings.Contains(stderr, "timed out") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}

	// An abandoned sign-in keeps its client registration for the retry.
	h.run("login", "--no-browser")
	if h.fake.registrations != 1 {
		t.Errorf("registrations = %d after two abandoned sign-ins, want 1", h.fake.registrations)
	}
	if code, _, _ := h.run("auth", "status"); code != 3 {
		t.Error("an abandoned sign-in left the user signed in")
	}
}

func TestExpiredTokenIsRefreshedSilently(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()

	creds, _ := h.store.Load("test")
	before := *creds
	creds.JWTExpiry = time.Now().Add(-time.Minute)
	_, _ = h.store.Save(creds)

	code, stdout, stderr := h.run("apps", "list")
	if code != 0 || !strings.Contains(stdout, "com.example.sample") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	after, _ := h.store.Load("test")
	if after.JWT == before.JWT || after.RefreshToken == before.RefreshToken {
		t.Error("the token was not refreshed, or the rotated refresh token was not saved")
	}
}

func TestRevokedSessionAsksForLogin(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	creds, _ := h.store.Load("test")
	creds.JWTExpiry = time.Now().Add(-time.Minute)
	_, _ = h.store.Save(creds)
	h.fake.refreshToken = "" // revoked from the dashboard

	code, _, stderr := h.run("apps", "list")
	if code != 3 || !strings.Contains(stderr, "expired or was revoked") || !strings.Contains(stderr, "tenjin login") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	if code, _, _ = h.run("auth", "status"); code != 3 {
		t.Error("status still reports a session after the grant was refused")
	}
}

func TestAPITokenFromTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.vars["TENJIN_API_TOKEN"] = "dashboard-token"

	code, stdout, stderr := h.run("apps", "list")
	if code != 0 || !strings.Contains(stdout, "com.example.sample") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if _, stdout, _ = h.run("auth", "status"); !strings.Contains(stdout, "TENJIN_API_TOKEN") {
		t.Errorf("status = %q", stdout)
	}
	if creds, _ := h.store.Load("test"); creds != nil {
		t.Error("an API token from the environment was written to the store")
	}
}

func TestBadAPITokenIsASignInProblem(t *testing.T) {
	h := newHarness(t)
	for _, token := range []string{"not-a-jwt", "looks.like.jwt"} {
		h.vars["TENJIN_API_TOKEN"] = token
		code, _, stderr := h.run("apps", "list", "--json")
		if code != 3 || errorBody(t, stderr)["code"] != "not_signed_in" {
			t.Errorf("token %q: exit = %d, stderr = %q", token, code, stderr)
		}
	}
}

func TestAppsListFollowsEveryPage(t *testing.T) {
	h := newHarness(t)
	h.fake.pageSize = 2
	for i := 1; i <= 5; i++ {
		h.fake.addApp(fmt.Sprintf("App %d", i), fmt.Sprintf("com.example.app%d", i), "android")
	}
	h.signIn()

	code, stdout, _ := h.run("apps", "list", "--json")
	var apps []map[string]any
	if err := json.Unmarshal([]byte(stdout), &apps); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v", code, err)
	}
	if len(apps) != 5 {
		t.Errorf("got %d apps, want 5", len(apps))
	}
	if strings.Contains(stdout, "TOP-SECRET") {
		t.Error("the app's shared secret was printed")
	}
}

func TestAppsListWhenSignedOut(t *testing.T) {
	code, stdout, stderr := newHarness(t).run("apps", "list")
	if code != 3 || stdout != "" || !strings.Contains(stderr, "tenjin login") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestAppsEnsure(t *testing.T) {
	type result struct {
		ID      string
		Created bool
	}
	ensure := func(h *harness, args ...string) (int, result, string) {
		code, stdout, stderr := h.run(append([]string{"apps", "ensure", "--bundle-id", "com.example.sample", "--platform", "android", "--json"}, args...)...)
		var r result
		_ = json.Unmarshal([]byte(stdout), &r)
		return code, r, stderr
	}

	t.Run("finds an existing app", func(t *testing.T) {
		h := newHarness(t)
		existing := h.fake.addApp("Sample", "com.example.sample", "android")
		h.fake.addApp("Sample iOS", "com.example.sample", "ios")
		h.signIn()
		code, r, _ := ensure(h)
		if code != 0 || r.ID != existing.ID || r.Created {
			t.Errorf("exit = %d, result = %+v", code, r)
		}
	})

	t.Run("refuses to create without a yes", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		code, _, stderr := ensure(h)
		if code != 5 || !strings.Contains(stderr, "--yes") || len(h.fake.apps) != 0 {
			t.Errorf("exit = %d, apps = %d, stderr = %q", code, len(h.fake.apps), stderr)
		}
	})

	t.Run("creates with --yes and is idempotent", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		code, first, _ := ensure(h, "--yes", "--name", "Sample")
		if code != 0 || !first.Created || first.ID == "" {
			t.Fatalf("exit = %d, result = %+v", code, first)
		}
		code, second, _ := ensure(h, "--yes")
		if code != 0 || second.Created || second.ID != first.ID || len(h.fake.apps) != 1 {
			t.Errorf("exit = %d, result = %+v, apps = %d", code, second, len(h.fake.apps))
		}
	})

	t.Run("asks at a terminal", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		h.typing("n")
		if code, _, _ := ensure(h); code != 5 || len(h.fake.apps) != 0 {
			t.Errorf("answered no: exit = %d, apps = %d", code, len(h.fake.apps))
		}
		h.typing("y")
		if code, r, _ := ensure(h); code != 0 || !r.Created {
			t.Errorf("answered yes: exit = %d, result = %+v", code, r)
		}
	})

	t.Run("explains a role that cannot create apps", func(t *testing.T) {
		h := newHarness(t)
		h.fake.denyCreate = true
		h.signIn()
		code, _, stderr := ensure(h, "--yes")
		if code != 4 || errorBody(t, stderr)["code"] != "permission_denied" || !strings.Contains(stderr, "manager") {
			t.Errorf("exit = %d, stderr = %q", code, stderr)
		}
	})

	t.Run("rejects an unknown platform", func(t *testing.T) {
		h := newHarness(t)
		code, _, stderr := h.run("apps", "ensure", "--bundle-id", "x", "--platform", "symbian")
		if code != 2 || !strings.Contains(stderr, "unknown platform") {
			t.Errorf("exit = %d, stderr = %q", code, stderr)
		}
	})
}

func TestSetupNeedsASessionWhenNotInteractive(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	code, stdout, stderr := h.run("setup", dir)
	if code != 3 || !strings.Contains(stdout, "Step 1 of 7") || !strings.Contains(stderr, "tenjin login") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestSetupHasNoJSONOutput(t *testing.T) {
	code, _, stderr := newHarness(t).run("setup", "--json")
	if code != 2 || !strings.Contains(errorBody(t, stderr)["hint"], "tenjin doctor") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestSetupOnACorrectProject(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, testproject.AndroidNative())

	code, stdout, stderr := h.run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"Step 1 of 7", "Step 7 of 7 · Check a real launch", "Signed in to Acme Games",
		`found "Sample"`, "the project already holds a key Tenjin accepts", "already set up in this project",
		"Tenjin accepts the SDK key", "Nothing here would stop events",
		// Without adb the launch cannot be read here; it says how to do it later.
		"adb was not found", "then run tenjin doctor --device",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
}

// Without a terminal, setup does not create apps unless told to, and still
// carries on to the checks.
// With only recommendations left, such as a newer SDK, step 5 says the SDK is
// set up rather than that it is not.
func TestSetupWithOnlyRecommendationsLeft(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	const build = "app/build.gradle.kts"
	base := testproject.AndroidNative()
	dir := testproject.Write(t, base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:1.24.0", 1)}))

	code, stdout, stderr := h.run("setup", dir, "--agent", "none")
	if code != 0 || !strings.Contains(stdout, "The Tenjin SDK is set up in this project. 1 change is recommended.") || strings.Contains(stdout, "not fully set up") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
}

func TestSetupDoesNotCreateAppsUnasked(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	dir := testproject.Write(t, testproject.Flutter())

	code, stdout, _ := h.run("setup", dir)
	if len(h.fake.apps) != 0 {
		t.Fatalf("created %d apps without --yes", len(h.fake.apps))
	}
	if code != 0 || !strings.Contains(stdout, "this project needs 2 apps") || !strings.Contains(stdout, "Pass --yes to create it") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}

	code, stdout, _ = h.run("setup", dir, "--yes")
	if code != 0 || len(h.fake.apps) != 2 || !strings.Contains(stdout, "created") {
		t.Errorf("with --yes: exit = %d, apps = %d\n%s", code, len(h.fake.apps), stdout)
	}
}

// fixOnRead repairs the project the moment setup waits for the user, which
// is what a person does between checks.
// fixOnRead answers every question with Enter, and makes the fix when the
// developer is asked to check again: the second question, after the one
// about features.
type fixOnRead struct {
	fix   func()
	reads int
}

func (f *fixOnRead) Read(p []byte) (int, error) {
	if f.reads++; f.reads == 2 {
		f.fix()
	}
	return copy(p, "\n"), nil
}

func TestSetupChecksAgainAfterAFix(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()

	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	dir := testproject.Write(t, base.With(testproject.Files{
		manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1),
	}))

	h.env.interactive = true
	h.env.stdin = bufio.NewReader(&fixOnRead{fix: func() {
		path := filepath.Join(dir, filepath.FromSlash(manifest))
		if err := os.WriteFile(path, []byte(base[manifest]), 0o644); err != nil {
			t.Error(err)
		}
	}})

	code, stdout, stderr := h.run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	// The whole report the first time; after the fix, only the outcome.
	if strings.Count(stdout, "Checking ") != 1 || strings.Index(stdout, "INTERNET permission is missing") > strings.Index(stdout, "Nothing here would stop events") {
		t.Errorf("expected one full report, then the outcome:\n%s", stdout)
	}
	if !strings.Contains(stdout, "INTERNET permission is missing") || !strings.Contains(stdout, "Nothing here would stop events") {
		t.Errorf("output:\n%s", stdout)
	}
}

func TestSetupStopsWhenAsked(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	dir := testproject.Write(t, base.With(testproject.Files{
		manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1),
	}))

	code, stdout, _ := h.typing("", "q").run("setup", dir)
	if code != 9 || strings.Count(stdout, "Checking ") != 1 {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
}

func TestAppsListFilters(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.fake.addApp("Other", "com.example.other", "android")
	h.signIn()

	ids := func(args ...string) string {
		t.Helper()
		code, stdout, stderr := h.run(append([]string{"apps", "list", "--json"}, args...)...)
		if code != 0 {
			t.Fatalf("exit = %d: %s", code, stderr)
		}
		var apps []struct {
			BundleID string `json:"bundle_id"`
			Platform string
		}
		if err := json.Unmarshal([]byte(stdout), &apps); err != nil {
			t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
		}
		var out []string
		for _, a := range apps {
			out = append(out, a.Platform+":"+a.BundleID)
		}
		return strings.Join(out, " ")
	}
	if got := ids("--platform", "android"); got != "android:com.example.sample android:com.example.other" {
		t.Errorf("--platform: %q", got)
	}
	if got := ids("--bundle-id", "com.example.sample", "--platform", "ios"); got != "ios:com.example.sample" {
		t.Errorf("--bundle-id and --platform: %q", got)
	}
	// No match is an empty list, not null and not an error.
	if got := ids("--bundle-id", "com.example"); got != "" {
		t.Errorf("a bundle ID prefix matched: %q", got)
	}
}

func TestAppsGet(t *testing.T) {
	h := newHarness(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()

	code, stdout, _ := h.run("apps", "get", app.ID)
	if code != 0 || !strings.Contains(stdout, "Bundle ID  com.example.sample") || !strings.Contains(stdout, "none received yet") {
		t.Errorf("exit = %d, stdout:\n%s", code, stdout)
	}
	// The API returns shared secrets with an app; none of that is printed.
	code, stdout, _ = h.run("apps", "get", app.ID, "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v", code, err)
	}
	if url, _ := got["dashboard_url"].(string); got["id"] != app.ID || !strings.HasSuffix(url, "/dashboard/apps/"+app.ID+"/sdk") {
		t.Errorf("fields = %v", got)
	}
	if strings.Contains(stdout, "SECRET") {
		t.Errorf("a shared secret reached the output:\n%s", stdout)
	}
	if got["created_at"] != "2026-10-06T12:34:56.123Z" {
		t.Errorf("created_at = %v, want the API's own text", got["created_at"])
	}

	code, _, stderr := h.run("apps", "get", "00000000-0000-4000-8000-999999999999", "--json")
	if code != 5 || errorBody(t, stderr)["code"] != "not_found" {
		t.Errorf("unknown app: exit = %d, stderr = %q", code, stderr)
	}
	// App IDs are UUIDs; anything else cannot name an app.
	if code, _, stderr := h.run("apps", "get", "com.example.sample", "--json"); code != 5 || errorBody(t, stderr)["code"] != "not_found" || !strings.Contains(stderr, "app IDs look like") {
		t.Errorf("a bundle ID for an app ID: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("apps", "get"); code != 2 || !strings.Contains(stderr, "the app's ID") {
		t.Errorf("no argument: exit = %d, stderr = %q", code, stderr)
	}
}

func TestAppsCreate(t *testing.T) {
	create := []string{"apps", "create", "--name", "Sample", "--bundle-id", "com.example.sample", "--platform", "android"}

	t.Run("creates with --yes, then refuses the duplicate", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		code, stdout, stderr := h.run(append(create, "--yes", "--json")...)
		var app struct{ ID, Name string }
		if err := json.Unmarshal([]byte(stdout), &app); err != nil || code != 0 || app.ID == "" || app.Name != "Sample" {
			t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
		}

		code, stdout, stderr = h.run(append(create, "--yes", "--json")...)
		body := errorBody(t, stderr)
		if code != 10 || stdout != "" || body["code"] != "already_exists" || !strings.Contains(body["message"], app.ID) || !strings.Contains(body["hint"], "apps ensure") {
			t.Errorf("exit = %d, stdout = %q, error = %v", code, stdout, body)
		}
		if len(h.fake.apps) != 1 {
			t.Errorf("apps = %d", len(h.fake.apps))
		}
	})

	t.Run("never creates unasked", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		code, _, stderr := h.run(create...)
		if code != 2 || !strings.Contains(stderr, "--yes") || len(h.fake.apps) != 0 {
			t.Errorf("no terminal: exit = %d, apps = %d, stderr = %q", code, len(h.fake.apps), stderr)
		}
		h.typing("n")
		if code, _, stderr := h.run(create...); code != 0 || len(h.fake.apps) != 0 || !strings.Contains(stderr, "Nothing was created.") {
			t.Errorf("answered no: exit = %d, apps = %d, stderr = %q", code, len(h.fake.apps), stderr)
		}
		h.typing("y")
		code, stdout, stderr := h.run(create...)
		if code != 0 || len(h.fake.apps) != 1 || !strings.Contains(stderr, "cannot be changed later") || !strings.Contains(stdout, "App ID") {
			t.Errorf("answered yes: exit = %d, apps = %d, stderr = %q", code, len(h.fake.apps), stderr)
		}
	})

	t.Run("from a store URL", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		code, stdout, stderr := h.run("apps", "create", "--store-url", "https://play.google.com/store/apps/details?id=com.example.store", "--yes")
		if code != 0 || !strings.Contains(stdout, "Bundle ID  com.example.store") {
			t.Errorf("exit = %d, stderr = %q, stdout:\n%s", code, stderr, stdout)
		}
		// Tenjin's own explanation of a URL it cannot use is passed on.
		code, _, stderr = h.run("apps", "create", "--store-url", "https://example.com/app", "--yes")
		if code != 11 || !strings.Contains(stderr, "store_url could not be resolved") {
			t.Errorf("bad URL: exit = %d, stderr = %q", code, stderr)
		}
	})

	t.Run("explains a role that cannot create apps", func(t *testing.T) {
		h := newHarness(t)
		h.fake.denyCreate = true
		h.signIn()
		code, _, stderr := h.run(append(create, "--yes")...)
		if code != 4 || !strings.Contains(stderr, "manager") {
			t.Errorf("exit = %d, stderr = %q", code, stderr)
		}
	})

	t.Run("usage errors", func(t *testing.T) {
		h := newHarness(t)
		for _, args := range [][]string{
			{"--name", "Sample"},
			{"--name", "Sample", "--bundle-id", "x", "--platform", "symbian", "--yes"},
			{"--store-url", "https://play.google.com/store/apps/details?id=x", "--name", "Sample"},
		} {
			if code, _, stderr := h.run(append([]string{"apps", "create"}, args...)...); code != 2 {
				t.Errorf("%q: exit = %d, stderr = %q", args, code, stderr)
			}
		}
	})
}

func TestAppsKeysListPointsAtTheDashboard(t *testing.T) {
	h := newHarness(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()

	code, stdout, _ := h.run("apps", "keys", "list", "--app", app.ID, "--json")
	var got struct {
		Keys         []string
		Available    bool
		DashboardURL string `json:"dashboard_url"`
		Instructions []string
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v", code, err)
	}
	if got.Keys == nil || len(got.Keys) != 0 || got.Available || !strings.HasSuffix(got.DashboardURL, app.ID+"/sdk") {
		t.Errorf("result = %+v", got)
	}
	// An agent gets the steps to pass on to the developer, with the page.
	if len(got.Instructions) != 3 || !strings.Contains(got.Instructions[0], got.DashboardURL) || !strings.Contains(got.Instructions[1], `"SDK Keys" card`) {
		t.Errorf("instructions = %q", got.Instructions)
	}
	// A person gets them numbered, and how to check the key they copied.
	_, stdout, _ = h.run("apps", "keys", "list", "--app", app.ID)
	for _, want := range []string{"To get the SDK key of Sample (android, com.example.sample):", "  1. Open http", `  3. If the card lists no key, choose "Generate SDK Key"`, "tenjin doctor --sdk-key android=<key>"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if code, _, _ := h.run("apps", "keys", "list", "--app", "00000000-0000-4000-8000-999999999999"); code != 5 {
		t.Errorf("unknown app: exit = %d", code)
	}
	if code, _, _ := h.run("apps", "keys", "list"); code != 2 {
		t.Errorf("no --app: exit = %d", code)
	}
}

// lockedBuffer is a buffer two goroutines can share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// remoteBrowser is the user on another machine: it reads the sign-in URL
// off the terminal, goes through the sign-in without following the
// redirect, which could not reach this machine, and types the address the
// browser ended on.
type remoteBrowser struct {
	t      *testing.T
	stderr *lockedBuffer
	sent   bool
}

var printedURL = regexp.MustCompile(`https?://\S+/oauth/authorize\S*`)

func (r *remoteBrowser) Read(p []byte) (int, error) {
	if r.sent {
		select {} // the user types nothing more
	}
	r.sent = true
	var authorize string
	for i := 0; i < 200 && authorize == ""; i++ {
		authorize = printedURL.FindString(r.stderr.String())
		time.Sleep(10 * time.Millisecond)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authorize)
	if err != nil {
		r.t.Error(err)
		return 0, io.EOF
	}
	_ = resp.Body.Close()
	return copy(p, "  "+resp.Header.Get("Location")+"\n"), nil
}

func TestLoginNoBrowserTakesThePastedAddress(t *testing.T) {
	h := newHarness(t)
	h.env.loginTimeout = 5 * time.Second
	stderr := &lockedBuffer{}
	h.env.interactive = true
	h.env.stdin = bufio.NewReader(&remoteBrowser{t: t, stderr: stderr})
	h.env.openBrowser = func(string) error { t.Error("a browser was opened"); return nil }

	var stdout bytes.Buffer
	code := execute([]string{"login", "--no-browser"}, &stdout, stderr, h.env)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "paste it here") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if code, _, _ := h.run("auth", "status"); code != 0 {
		t.Error("not signed in after pasting the address")
	}
}

// Input that ends where an answer was due says so, as a usage error.
func TestInputEndingAtAPrompt(t *testing.T) {
	h := newHarness(t)
	h.env.interactive = true
	dir := testproject.Write(t, testproject.AndroidNative())
	code, _, stderr := h.run("setup", dir, "--features", "none")
	if code != 2 || !strings.Contains(stderr, "input ended before an answer") || strings.Contains(stderr, "EOF") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}
