package cli

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/update"
)

// withRelease makes the harness a person at a terminal running 0.1.0, with
// latest out on the releases page.
func withRelease(t *testing.T, h *harness, latest string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+latest, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	h.env.updates = &update.Checker{
		Current:  "0.1.0",
		Releases: srv.URL + "/releases",
		State:    filepath.Join(t.TempDir(), "update-check.json"),
	}
}

func TestANewerReleaseIsMentionedAfterTheCommand(t *testing.T) {
	h := newHarness(t)
	withRelease(t, h, "v0.2.0")
	dir := testproject.Write(t, testproject.AndroidNative())

	code, stdout, stderr := h.run("project", "detect", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "new version") {
		t.Errorf("the notice is in stdout:\n%s", stdout)
	}
	for _, want := range []string{
		"A new version of the Tenjin CLI is available: 0.1.0 → 0.2.0",
		// The harness's binary is /usr/local/bin/tenjin: the install script's
		// place, so its command, for this OS.
		"Update with: " + update.Command("/usr/local/bin/tenjin", runtime.GOOS, h.env.getenv),
		"/releases/tag/v0.2.0",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}

	// Once a day is enough.
	_, _, stderr = h.run("project", "detect", dir)
	if strings.Contains(stderr, "new version") {
		t.Errorf("told twice:\n%s", stderr)
	}
}

// JSON is read by programs, which have no use for the notice.
func TestJSONOutputHasNoUpdateNotice(t *testing.T) {
	h := newHarness(t)
	withRelease(t, h, "v0.2.0")
	dir := testproject.Write(t, testproject.AndroidNative())

	_, _, stderr := h.run("project", "detect", dir, "--json")
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

// Failing commands still mention it, after their error.
func TestAnUpdateNoticeFollowsAnError(t *testing.T) {
	h := newHarness(t)
	withRelease(t, h, "v0.2.0")

	code, _, stderr := h.run("project", "detect", t.TempDir())
	if code == 0 {
		t.Fatal("detect passed on an empty folder")
	}
	errAt, noticeAt := strings.Index(stderr, "no mobile project"), strings.Index(stderr, "A new version")
	if errAt < 0 || noticeAt < errAt {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// A stray request to the sign-in's local port, without the sign-in's state,
// does not end the sign-in.
func TestAStrayCallbackDoesNotEndTheSignIn(t *testing.T) {
	h := newHarness(t)
	follow := h.env.openBrowser
	h.env.openBrowser = func(authorize string) error {
		u, err := url.Parse(authorize)
		if err != nil {
			return err
		}
		if resp, err := http.Get(u.Query().Get("redirect_uri") + "?state=guess&code=x"); err == nil {
			_ = resp.Body.Close()
		}
		return follow(authorize)
	}
	if code, _, stderr := h.run("login"); code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
}
