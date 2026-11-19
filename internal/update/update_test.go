package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// releases stands in for GitHub: /latest redirects to the newest tag.
func releases(t *testing.T, latest string) (string, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path != "/releases/latest" || latest == "" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/releases/tag/"+latest, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/releases", &asked
}

func checker(t *testing.T, current, url string, now *time.Time) *Checker {
	return &Checker{
		Current:  current,
		Releases: url,
		State:    filepath.Join(t.TempDir(), "tenjin", "update-check.json"),
		Now:      func() time.Time { return *now },
	}
}

func TestANewerReleaseIsToldOnceADay(t *testing.T) {
	url, asked := releases(t, "v0.2.0")
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	c := checker(t, "0.1.0", url, &now)

	n := c.Check(context.Background())
	if n == nil || n.Current != "0.1.0" || n.Latest != "0.2.0" || n.Notes != url+"/tag/v0.2.0" {
		t.Fatalf("notice = %+v", n)
	}
	now = now.Add(time.Hour)
	if n := c.Check(context.Background()); n != nil {
		t.Errorf("told again within the day: %+v", n)
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("GitHub was asked %d times; the answer is kept for a day", got)
	}
	now = now.Add(24 * time.Hour)
	if n := c.Check(context.Background()); n == nil {
		t.Error("not told again the next day")
	}
}

func TestNothingIsSaidWhenUpToDate(t *testing.T) {
	for _, latest := range []string{"v0.1.0", "v0.0.9"} {
		url, _ := releases(t, latest)
		now := time.Now()
		if n := checker(t, "0.1.0", url, &now).Check(context.Background()); n != nil {
			t.Errorf("latest %s: %+v", latest, n)
		}
	}
}

// Offline, or with no release yet, the check costs one try a day and says
// nothing.
func TestAFailedCheckWaitsADay(t *testing.T) {
	url, asked := releases(t, "")
	now := time.Now()
	c := checker(t, "0.1.0", url, &now)
	for range 3 {
		if n := c.Check(context.Background()); n != nil {
			t.Errorf("notice = %+v", n)
		}
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("asked %d times", got)
	}
}

func TestADevelopmentBuildIsNotChecked(t *testing.T) {
	url, asked := releases(t, "v9.0.0")
	now := time.Now()
	for _, current := range []string{"dev", "v0.0.0-20261006211235-7742175b057a+dirty", "0.0.1-snapshot", "0.2.0-rc.1"} {
		if n := checker(t, current, url, &now).Check(context.Background()); n != nil {
			t.Errorf("%s: %+v", current, n)
		}
	}
	if got := asked.Load(); got != 0 {
		t.Errorf("asked %d times", got)
	}
}

func TestVersionsCompareByNumber(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{
		{"0.10.0", "0.9.0", true},
		{"1.0.0", "0.99.99", true},
		{"0.1.1", "0.1.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
	} {
		a, _ := parse(c.a)
		b, _ := parse(c.b)
		if got := newer(a, b); got != c.newer {
			t.Errorf("newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// cask stands in for the tap's raw cask file, naming version; "" answers 404.
func cask(t *testing.T, version *string) (string, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.Method != http.MethodGet || *version == "" {
			http.NotFound(w, r)
			return
		}
		// As the tap's 1.0.0 cask is: a block before version, then the
		// stanzas. Later casks have version first; the reader takes both.
		_, _ = fmt.Fprintf(w, `cask "tenjin" do
  postflight_steps do
    on_macos do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/tenjin"]
    end
  end

  name "tenjin"
  desc "Integrate and verify the Tenjin SDK from your terminal"
  homepage "https://github.com/tenjin/tenjin-cli"
  version "%s"
end
`, *version)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/Casks/tenjin.rb", &asked
}

// A copy from the Homebrew cask is told of a version once the cask names
// it, when brew upgrade can install it, not when the release is published.
func TestAHomebrewCopyFollowsTheCask(t *testing.T) {
	releasesURL, releasesAsked := releases(t, "v0.3.0")
	version := "0.1.0"
	caskURL, caskAsked := cask(t, &version)
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	c := checker(t, "0.1.0", releasesURL, &now)
	c.Cask = caskURL

	if n := c.Check(context.Background()); n != nil {
		t.Errorf("told of a release the cask does not have yet: %+v", n)
	}
	// The cask is updated within the day: the answer is still kept a day.
	version = "0.3.0"
	now = now.Add(time.Hour)
	if n := c.Check(context.Background()); n != nil {
		t.Errorf("asked again within the day: %+v", n)
	}
	now = now.Add(24 * time.Hour)
	n := c.Check(context.Background())
	if n == nil || n.Latest != "0.3.0" || n.Notes != releasesURL+"/tag/v0.3.0" {
		t.Errorf("notice = %+v", n)
	}
	if caskAsked.Load() != 2 || releasesAsked.Load() != 0 {
		t.Errorf("the cask was asked %d times, the releases %d", caskAsked.Load(), releasesAsked.Load())
	}
}

// An answer read from the releases page is not taken for the cask's, and
// a cask without a release version in it says nothing.
func TestTheCaskIsAskedAgainOverAnAnswerFromTheReleases(t *testing.T) {
	releasesURL, _ := releases(t, "v0.3.0")
	version := "0.1.0"
	caskURL, caskAsked := cask(t, &version)
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	c := checker(t, "0.1.0", releasesURL, &now)
	if n := c.Check(context.Background()); n == nil {
		t.Fatal("the releases page's answer was not told")
	}
	now = now.Add(25 * time.Hour)
	c.Cask = caskURL
	if n := c.Check(context.Background()); n != nil || caskAsked.Load() != 1 {
		t.Errorf("notice = %+v, the cask asked %d times", n, caskAsked.Load())
	}
	for _, bad := range []string{"", "latest", "0.4.0-rc.1"} {
		version = bad
		state := checker(t, "0.1.0", releasesURL, &now)
		state.Cask = caskURL
		if n := state.Check(context.Background()); n != nil {
			t.Errorf("cask version %q: %+v", bad, n)
		}
	}
}

func TestOnlyACopyInTheCaskroomFollowsTheCask(t *testing.T) {
	for exe, want := range map[string]string{
		"/opt/homebrew/Caskroom/tenjin/0.1.0/tenjin":              Cask,
		"/home/linuxbrew/.linuxbrew/Caskroom/tenjin/0.1.0/tenjin": Cask,
		"/opt/homebrew/Cellar/tenjin/0.1.0/bin/tenjin":            "",
		"/home/dev/.local/bin/tenjin":                             "",
		`C:\Users\dev\AppData\Local\Programs\tenjin\tenjin.exe`:   "",
	} {
		if got := CaskFor(exe); got != want {
			t.Errorf("%s: %q", exe, got)
		}
	}
	// Homebrew's bin/tenjin is a link into the Caskroom.
	root := t.TempDir()
	target := filepath.Join(root, "Caskroom", "tenjin", "0.1.0", "tenjin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "tenjin")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if got := CaskFor(link); got != Cask {
		t.Errorf("through a link: %q", got)
	}
}

func TestTheUpdateCommandFollowsTheInstall(t *testing.T) {
	gopath := t.TempDir()
	env := func(name string) string {
		if name == "GOPATH" {
			return gopath
		}
		return ""
	}
	for _, c := range []struct{ exe, goos, want string }{
		{"/opt/homebrew/Caskroom/tenjin/0.1.0/tenjin", "darwin", "brew upgrade tenjin"},
		{"/home/linuxbrew/.linuxbrew/Caskroom/tenjin/0.1.0/tenjin", "linux", "brew upgrade tenjin"},
		{filepath.Join(gopath, "bin", "tenjin"), "linux", "go install github.com/tenjin/tenjin-cli/cmd/tenjin@latest"},
		{`C:\Users\dev\AppData\Local\Programs\tenjin\tenjin.exe`, "windows", `powershell -c "irm https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.ps1 | iex"`},
		{"/home/dev/.local/bin/tenjin", "linux", "curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh"},
	} {
		if got := Command(c.exe, c.goos, env); got != c.want {
			t.Errorf("%s: %q", c.exe, got)
		}
	}
}

// An update is said once: on the first run of a release after another ran.
func TestUpgradedIsToldOnceAfterTheVersionChanged(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	first := checker(t, "0.1.0", "", &now)
	if first.Upgraded() {
		t.Error("the first release to run was taken for an update")
	}
	if first.Upgraded() {
		t.Error("the same release, again, was taken for an update")
	}
	// The daily check's answer is kept beside it.
	first.save(state{CheckedAt: now, Latest: "0.2.0", Ran: first.load().Ran})

	next := *first
	next.Current = "0.2.0"
	if !next.Upgraded() {
		t.Error("the run after an update was not told")
	}
	if next.Upgraded() {
		t.Error("told twice")
	}
	if s := next.load(); s.Latest != "0.2.0" || !s.CheckedAt.Equal(now) {
		t.Errorf("the check's answer was lost: %+v", s)
	}

	dev := next
	dev.Current = "dev"
	if dev.Upgraded() {
		t.Error("a development build was taken for an update")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.0", "0.2.0", false},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "dev", false},
		{"dev", "0.1.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.newer {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
