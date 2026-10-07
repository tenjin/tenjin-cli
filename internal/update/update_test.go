package update

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		{`C:\Users\dev\AppData\Local\Programs\tenjin\tenjin.exe`, "windows", "irm https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.ps1 | iex"},
		{"/home/dev/.local/bin/tenjin", "linux", "curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh"},
	} {
		if got := Command(c.exe, c.goos, env); got != c.want {
			t.Errorf("%s: %q", c.exe, got)
		}
	}
}
