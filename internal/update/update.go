// Package update tells a person at the terminal when a newer release of the
// CLI is out. It asks GitHub at most once a day, and remembers the answer in
// a small state file.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Releases is where the CLI's releases are published.
const Releases = "https://github.com/tenjin/tenjin-cli/releases"

// checkEvery is how long an answer is kept, and how long a failed check
// waits before the next try, so being offline costs one wait a day.
const checkEvery = 24 * time.Hour

// Checker finds the latest release.
type Checker struct {
	// Current is the running version, such as 0.1.0. Only a release
	// version is checked; a development build never is.
	Current string
	// Releases is the releases page; its /latest redirects to the newest tag.
	Releases string
	// Transport sends the request; nil is http.DefaultTransport.
	Transport http.RoundTripper
	// State is the file the last answer is kept in.
	State string
	Now   func() time.Time
}

// Notice is a newer release to tell the user about.
type Notice struct {
	Current, Latest string
	// Notes is the release's page.
	Notes string
}

type state struct {
	CheckedAt  time.Time `json:"checked_at"`
	Latest     string    `json:"latest,omitempty"`
	NotifiedAt time.Time `json:"notified_at,omitempty"`
}

// Check returns a newer release, or nil when there is none, it was already
// told today, or nothing could be found out. It never fails: an update
// check must not get in the way of the command.
func (c *Checker) Check(ctx context.Context) *Notice {
	current, ok := parse(c.Current)
	if !ok {
		return nil
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	s := c.load()
	if now().Sub(s.CheckedAt) >= checkEvery || s.CheckedAt.After(now()) {
		// A failed or cut-short check counts too, so a slow or missing
		// network costs one short wait a day, not one per command.
		latest, err := c.latest(ctx)
		s.CheckedAt = now()
		if err == nil {
			s.Latest = latest
		}
		c.save(s)
	}
	latest, ok := parse(s.Latest)
	if !ok || !newer(latest, current) || now().Sub(s.NotifiedAt) < checkEvery {
		return nil
	}
	s.NotifiedAt = now()
	c.save(s)
	return &Notice{Current: c.Current, Latest: s.Latest, Notes: c.releases() + "/tag/v" + s.Latest}
}

func (c *Checker) releases() string {
	if c.Releases != "" {
		return strings.TrimSuffix(c.Releases, "/")
	}
	return Releases
}

// latest reads the version of the newest release from where /latest
// redirects: .../releases/tag/v1.2.3.
func (c *Checker) latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.releases()+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Transport:     c.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	location := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || location == "" {
		return "", errors.New("no latest release")
	}
	tag := location[strings.LastIndex(location, "/")+1:]
	version := strings.TrimPrefix(tag, "v")
	if _, ok := parse(version); !ok {
		return "", errors.New("the latest release has no version: " + tag)
	}
	return version, nil
}

func (c *Checker) load() state {
	var s state
	if raw, err := os.ReadFile(c.State); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// save writes the state through a temporary file, so two commands run at
// once never leave it half written. A state that cannot be saved only
// means the next command checks again.
func (c *Checker) save(s state) {
	raw, err := json.Marshal(s)
	if err != nil || c.State == "" {
		return
	}
	dir := filepath.Dir(c.State)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".update-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), c.State)
}

// parse reads a release version, MAJOR.MINOR.PATCH with an optional leading
// v. Pre-releases and development builds are not releases.
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Command is how to update a copy of the CLI installed at executable: the
// way it was installed.
func Command(executable, goos string, getenv func(string) string) string {
	path := executable
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		path = resolved
	}
	slashed := filepath.ToSlash(path)
	switch {
	case strings.Contains(slashed, "/Caskroom/") || strings.Contains(slashed, "/Cellar/"):
		return "brew upgrade tenjin"
	case inGoBin(path, getenv):
		return "go install github.com/tenjin/tenjin-cli/cmd/tenjin@latest"
	case goos == "windows":
		// Through powershell, so it works typed into cmd.exe as well.
		return `powershell -c "irm https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.ps1 | iex"`
	default:
		return "curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh"
	}
}

// inGoBin reports whether path is in the folder go install writes to.
func inGoBin(path string, getenv func(string) string) bool {
	dirs := []string{getenv("GOBIN")}
	for _, gopath := range filepath.SplitList(getenv("GOPATH")) {
		dirs = append(dirs, filepath.Join(gopath, "bin"))
	}
	if getenv("GOPATH") == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, "go", "bin"))
		}
	}
	for _, dir := range dirs {
		if dir != "" && filepath.Dir(path) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}
