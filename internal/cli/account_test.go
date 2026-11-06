package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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

	creds, _ := h.store.Load("test")
	if creds.ClientID != "tenjin-cli" {
		t.Errorf("signed in as client %q, want the CLI's own", creds.ClientID)
	}
	code, stdout, _ = h.run("logout")
	if code != 0 || !strings.Contains(stdout, "The session's API access has ended") || strings.Contains(stdout, "two hours") {
		t.Errorf("logout: exit = %d, stdout = %q", code, stdout)
	}
	// The API token the session held stops working at once.
	h.vars["TENJIN_API_TOKEN"] = creds.JWT
	if code, _, _ := h.run("apps", "list"); code != 3 {
		t.Errorf("the session's API token still works after logout: exit = %d", code)
	}
	delete(h.vars, "TENJIN_API_TOKEN")
	if code, _, _ = h.run("auth", "status"); code != 3 {
		t.Errorf("after logout: exit = %d, want 3", code)
	}
	if code, stdout, _ = h.run("logout"); code != 0 || !strings.Contains(stdout, "Not signed in") {
		t.Errorf("second logout: exit = %d, stdout = %q", code, stdout)
	}
}

// The sign-in lasts as long as the exchange says; a server that does not say
// leaves it to the token's own expiry.
func TestTheSignInLastsAsTheExchangeSays(t *testing.T) {
	for _, tc := range []struct {
		expiresIn int
		want      time.Duration
	}{{600, 10 * time.Minute}, {0, 2 * time.Hour}} {
		h := newHarness(t)
		h.fake.exchangeExpiresIn = tc.expiresIn
		h.signIn()
		creds, _ := h.store.Load("test")
		if got := time.Until(creds.JWTExpiry); got < tc.want-time.Minute || got > tc.want+time.Minute {
			t.Errorf("expires_in %d: the sign-in lasts %v, want about %v", tc.expiresIn, got.Round(time.Minute), tc.want)
		}
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

// With TENJIN_API_TOKEN set, login still reports the session it saved, and
// says the token is what other commands use.
func TestLoginWithAnAPITokenSet(t *testing.T) {
	for _, token := range []string{"dashboard-token", "has a space"} {
		h := newHarness(t)
		h.vars["TENJIN_API_TOKEN"] = token
		code, stdout, stderr := h.run("login")
		if code != 0 || !strings.Contains(stdout, "Signed in to Acme Games") || !strings.Contains(stderr, "TENJIN_API_TOKEN is set") {
			t.Errorf("%q: exit = %d, stdout = %q, stderr = %q", token, code, stdout, stderr)
		}
		code, stdout, _ = h.run("login", "--json")
		var status session.Status
		if err := json.Unmarshal([]byte(stdout), &status); err != nil || code != 0 {
			t.Fatalf("%q --json: exit = %d, err = %v", token, code, err)
		}
		if status.Source != session.SourceLogin || status.OrganizationName != "Acme Games" {
			t.Errorf("%q --json: %+v", token, status)
		}
	}
}

// A session an earlier build signed in with a client it registered for
// itself keeps working with that client, and its logout says what is true of
// it: the API token lasts until it expires.
func TestASessionFromAnEarlierClient(t *testing.T) {
	h := newHarness(t)
	h.signInAsAnEarlierClient()
	if code, _, stderr := h.run("apps", "list"); code != 0 {
		t.Fatalf("an earlier client's session: exit = %d, stderr = %q", code, stderr)
	}
	code, stdout, _ := h.run("logout")
	if code != 0 || !strings.Contains(stdout, "expires within two hours") || !strings.Contains(stdout, "/dashboard/access_tokens") {
		t.Errorf("logout: exit = %d, stdout = %q", code, stdout)
	}
	if got := h.fake.revocations; len(got) != 1 || !strings.HasPrefix(got[0], "client-1 ") {
		t.Errorf("revocations = %q, want one with the earlier client", got)
	}
}

// A new sign-in over an earlier client's session is the CLI's own client,
// and revokes the earlier client's grant, which nothing would end otherwise.
func TestSigningInOverAnEarlierClientsSession(t *testing.T) {
	h := newHarness(t)
	h.signInAsAnEarlierClient()
	old, _ := h.store.Load("test")
	h.signIn()
	creds, _ := h.store.Load("test")
	if creds.ClientID != "tenjin-cli" {
		t.Errorf("signed in as client %q, want the CLI's own", creds.ClientID)
	}
	if got := h.fake.revocations; len(got) != 1 || got[0] != "client-1 "+old.RefreshToken {
		t.Errorf("revocations = %q, want the earlier client's grant", got)
	}
	// Signing in again over the CLI's own client revokes nothing: that would
	// end the new session too.
	h.signIn()
	if got := h.fake.revocations; len(got) != 1 {
		t.Errorf("revocations = %q after a second sign-in", got)
	}
	if code, _, _ := h.run("apps", "list"); code != 0 {
		t.Errorf("the new session does not work: exit = %d", code)
	}
}

// When Tenjin does not confirm the revocation, logout says the session may
// still be open, and how to end it.
func TestLogoutThatTenjinDoesNotConfirm(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.revokeFails = true
	code, stdout, stderr := h.run("logout")
	if code != 7 || strings.Contains(stdout, "has ended") || !strings.Contains(stderr, "may still be open") || !strings.Contains(stderr, "/dashboard/access_tokens") {
		t.Errorf("exit = %d\nstdout = %q\nstderr = %q", code, stdout, stderr)
	}
}

// With TENJIN_API_TOKEN set, logout ends the saved session and says that
// commands still use the token.
func TestLogoutWithAnAPIToken(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.vars["TENJIN_API_TOKEN"] = "dashboard-token"
	code, stdout, stderr := h.run("logout")
	if code != 0 || !strings.Contains(stdout, "has ended") || !strings.Contains(stderr, "TENJIN_API_TOKEN is still set") {
		t.Errorf("exit = %d\nstdout = %q\nstderr = %q", code, stdout, stderr)
	}
}

// With only TENJIN_API_TOKEN, there is no session to end, and logout says
// that commands still use the token rather than that nobody is signed in.
func TestLogoutWithOnlyAnAPIToken(t *testing.T) {
	h := newHarness(t)
	h.vars["TENJIN_API_TOKEN"] = "dashboard-token"
	code, stdout, stderr := h.run("logout")
	if code != 0 || stdout != "No saved session to sign out of.\n" || stderr != "TENJIN_API_TOKEN is still set, and commands keep using it.\n" {
		t.Errorf("exit = %d\nstdout = %q\nstderr = %q", code, stdout, stderr)
	}
	code, stdout, stderr = h.run("logout", "--json")
	var out map[string]bool
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || code != 0 || out["signed_out"] || out["access_ended"] || !out["api_token_set"] || stderr != "" {
		t.Errorf("--json: exit = %d\nstdout = %q\nstderr = %q", code, stdout, stderr)
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

	t.Run("a dry run finds, and creates nothing", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		code, stdout, stderr := h.run("apps", "ensure", "--bundle-id", "com.example.sample", "--platform", "android", "--dry-run")
		if code != 0 || len(h.fake.apps) != 0 || !strings.Contains(stdout, `"name": "com.example.sample"`) ||
			!strings.Contains(stderr, `Would create "com.example.sample" with the bundle ID com.example.sample on android. Nothing was sent`) {
			t.Errorf("missing: exit = %d, apps = %d, stdout = %q, stderr = %q", code, len(h.fake.apps), stdout, stderr)
		}
		// With --json, both answers have the result's shape: one that would
		// be created says so, with the body that would create it.
		type dryRun struct {
			ID          *string           `json:"id"`
			BundleID    string            `json:"bundle_id"`
			Created     *bool             `json:"created"`
			WouldCreate map[string]string `json:"would_create"`
		}
		code, stdout, stderr = h.run("apps", "ensure", "--bundle-id", "com.example.sample", "--platform", "android", "--dry-run", "--json")
		var missing dryRun
		if err := json.Unmarshal([]byte(stdout), &missing); err != nil || code != 0 || missing.ID == nil || *missing.ID != "" || missing.Created == nil || *missing.Created ||
			missing.BundleID != "com.example.sample" || missing.WouldCreate["bundle_id"] != "com.example.sample" || missing.WouldCreate["platform"] != "android" ||
			!strings.Contains(stderr, "Nothing was sent") || len(h.fake.apps) != 0 {
			t.Errorf("missing, --json: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
		}
		existing := h.fake.addApp("Sample", "com.example.sample", "android")
		if code, r, stderr := ensure(h, "--dry-run"); code != 0 || r.ID != existing.ID || r.Created || len(h.fake.apps) != 1 {
			t.Errorf("there: exit = %d, result = %+v, stderr = %q", code, r, stderr)
		}
		_, stdout, _ = h.run("apps", "ensure", "--bundle-id", "com.example.sample", "--platform", "android", "--dry-run", "--json")
		if strings.Contains(stdout, "would_create") {
			t.Errorf("there, --json: stdout = %q", stdout)
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
	if code != 0 || !strings.Contains(stdout, "The Tenjin SDK is set up in this project. 1 change is recommended:\n  ! The Tenjin SDK is at 1.24.0; the current release is 2.0.0") || strings.Contains(stdout, "not fully set up") {
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

// A setup that has created an app and then waits too long for the sign-in
// says what it did: exit status 12 would say that nothing was.
func TestSetupThatFindsTheSignInBusyAfterCreatingSaysSo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.fake.afterCreate = func() {
		// The sign-in runs out, and another tenjin is renewing it.
		h.fake.expireTokens()
		h.store.Hold()
	}
	code, _, stderr := h.run("setup", testproject.Write(t, testproject.Flutter()), "--yes", "--agent", "none")
	if code != 7 || len(h.fake.apps) != 1 || !strings.Contains(stderr, "another tenjin command") || !strings.Contains(stderr, "setup had created 1 Tenjin app when it stopped: another tenjin command") ||
		!strings.Contains(stderr, "Run tenjin setup again") || strings.Contains(stderr, "Nothing was changed") {
		t.Errorf("exit = %d, %d apps, stderr = %q", code, len(h.fake.apps), stderr)
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

	t.Run("a dry run sends nothing", func(t *testing.T) {
		h := newHarness(t)
		code, stdout, stderr := h.run(append(create, "--dry-run")...)
		var body map[string]string
		if err := json.Unmarshal([]byte(stdout), &body); err != nil || code != 0 || len(h.fake.apps) != 0 ||
			!reflect.DeepEqual(body, map[string]string{"name": "Sample", "bundle_id": "com.example.sample", "platform": "android"}) ||
			!strings.Contains(stderr, `Would create "Sample" with the bundle ID com.example.sample on android. Nothing was sent: it would send POST `+h.fake.srv.URL+"/v2/apps with this body") {
			t.Errorf("exit = %d, apps = %d, stdout = %q, stderr = %q", code, len(h.fake.apps), stdout, stderr)
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
	// ends makes the input end after the address; otherwise the user types
	// nothing more.
	ends bool
}

var printedURL = regexp.MustCompile(`https?://\S+/oauth/authorize\S*`)

func (r *remoteBrowser) Read(p []byte) (int, error) {
	if r.sent && r.ends {
		return 0, io.EOF
	}
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

// Over SSH the browser that opens, if one does, is not the developer's, and
// the one they use cannot reach this machine: the address it ends on is
// asked for without --no-browser.
func TestLoginOverSSHTakesThePastedAddress(t *testing.T) {
	h := newHarness(t)
	h.vars["SSH_CONNECTION"] = "203.0.113.7 52114 198.51.100.2 22"
	stderr := &lockedBuffer{}
	h.env.interactive = true
	h.env.stdin = bufio.NewReader(&remoteBrowser{t: t, stderr: stderr})
	h.env.openBrowser = func(string) error { return nil } // on a screen nobody sees

	var stdout bytes.Buffer
	if code := execute([]string{"login"}, &stdout, stderr, h.env); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Opening the browser") || !strings.Contains(stderr.String(), "paste it here") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// A browser that cannot be opened, as in a container, leaves the address to
// be opened elsewhere and pasted back.
func TestLoginTakesThePastedAddressWhenNoBrowserOpens(t *testing.T) {
	h := newHarness(t)
	stderr := &lockedBuffer{}
	h.env.interactive = true
	h.env.stdin = bufio.NewReader(&remoteBrowser{t: t, stderr: stderr})
	h.env.openBrowser = func(string) error { return errors.New(`exec: "xdg-open": executable file not found in $PATH`) }

	var stdout bytes.Buffer
	if code := execute([]string{"login"}, &stdout, stderr, h.env); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Could not open a browser") || !strings.Contains(stderr.String(), "paste it here") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// A sign-in nobody finishes says what else works.
func TestASignInThatTimesOutSaysWhatElseWorks(t *testing.T) {
	for _, c := range []struct {
		name        string
		interactive bool
		args        []string
		want        []string
		unwanted    string
	}{
		{"at a terminal", true, []string{"login"}, []string{"run 'tenjin login --no-browser' and paste", "TENJIN_API_TOKEN"}, ""},
		{"pasting", true, []string{"login", "--no-browser"}, []string{"paste the address it ends on", "TENJIN_API_TOKEN"}, "run 'tenjin login --no-browser'"},
		{"without a terminal", false, []string{"login", "--no-browser"}, []string{"on this machine, to sign in", "only a browser on this machine can finish", "TENJIN_API_TOKEN"}, "any other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.env.loginTimeout = 200 * time.Millisecond
			h.env.interactive = c.interactive
			h.env.stdin = bufio.NewReader(&remoteBrowser{t: t, sent: true}) // types nothing
			h.env.openBrowser = func(string) error { return nil }
			code, _, stderr := h.run(c.args...)
			if code != 3 || !strings.Contains(stderr, "timed out") {
				t.Errorf("exit = %d, stderr = %q", code, stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q: %q", want, stderr)
				}
			}
			if c.unwanted != "" && strings.Contains(stderr, c.unwanted) {
				t.Errorf("stderr has %q: %q", c.unwanted, stderr)
			}
		})
	}
}

// setup signs in as login does, --no-browser included.
func TestSetupNoBrowserTakesThePastedAddress(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	dir := testproject.Write(t, testproject.AndroidNative())
	stderr := &lockedBuffer{}
	h.env.interactive = true
	// Enter to sign in, then the address the far browser ended on.
	h.env.stdin = bufio.NewReader(io.MultiReader(strings.NewReader("\n"), &remoteBrowser{t: t, stderr: stderr, ends: true}))
	h.env.openBrowser = func(string) error { t.Error("a browser was opened"); return nil }

	var stdout bytes.Buffer
	execute([]string{"setup", dir, "--no-browser", "--features", "none", "--agent", "none"}, &stdout, stderr, h.env)
	if !strings.Contains(stdout.String(), "Signed in to Acme Games") || !strings.Contains(stderr.String(), "paste it here") {
		t.Errorf("stdout:\n%s\nstderr = %q", stdout.String(), stderr.String())
	}
}

// A paste still being read when the browser's redirect arrived leaves the
// line typed next to the next question, rather than losing it.
func TestALineTypedAfterTheSignInAnswersTheNextQuestion(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	stdout, stderr := &lockedBuffer{}, &lockedBuffer{}
	h.env.interactive = true
	// Enter to sign in, and once signed in, a build untied, then Enter.
	h.env.stdin = bufio.NewReader(io.MultiReader(strings.NewReader("\n"), &afterSignIn{stdout: stdout, lines: "1\n\n"}))
	// The developer opens the address in a browser on this machine.
	go func() {
		for i := 0; i < 500; i++ {
			if authorize := printedURL.FindString(stderr.String()); authorize != "" {
				if resp, err := http.Get(authorize); err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	execute([]string{"setup", dir, "--no-browser", "--features", "none", "--agent", "none"}, stdout, stderr, h.env)
	if !strings.Contains(stdout.String(), "Setting up 1 build.") {
		t.Errorf("stdout:\n%s\nstderr = %q", stdout.String(), stderr.String())
	}
}

// afterSignIn is a developer who types only once the terminal says they are
// signed in.
type afterSignIn struct {
	stdout *lockedBuffer
	lines  string
	sent   bool
}

func (a *afterSignIn) Read(p []byte) (int, error) {
	if a.sent {
		return 0, io.EOF
	}
	a.sent = true
	for i := 0; !strings.Contains(a.stdout.String(), "Signed in to"); i++ {
		if i == 1000 {
			return 0, io.EOF // never signed in: the test fails on its own
		}
		time.Sleep(10 * time.Millisecond)
	}
	return copy(p, a.lines), nil
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
