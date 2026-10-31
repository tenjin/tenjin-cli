// Package update tells a person at the terminal when a newer release of the
// CLI is out. It asks GitHub at most once a day, and remembers the answer in
// a small state file.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Releases is where the CLI's releases are published.
const Releases = "https://github.com/tenjin/tenjin-cli/releases"

// Cask is the Homebrew cask in Tenjin's tap, as brew upgrade reads it. A
// release's cask reaches the tap when its pull request there is merged,
// some time after the release is published.
const Cask = "https://raw.githubusercontent.com/tenjin/homebrew-tap/main/Casks/tenjin.rb"

// maxCask bounds how much of the cask is read. The real one is well under
// a kilobyte.
const maxCask = 64 << 10

// checkEvery is how long an answer is kept, and how long a failed check
// waits before the next try, so being offline costs one wait a day.
const checkEvery = 24 * time.Hour

// Checker finds the latest release.
type Checker struct {
	// Current is the running version, such as 1.0.0. Only a release
	// version is checked; a development build never is.
	Current string
	// Releases is the releases page; its /latest redirects to the newest tag.
	Releases string
	// Cask, when set, is the Homebrew cask this copy was installed from
	// (see CaskFor). The newest version is then the one the cask names, not
	// the newest release: until the cask is updated, brew upgrade has
	// nothing newer to install.
	Cask string
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
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	// Source is where Latest was read: the releases page or the cask. An
	// answer from the other one is checked again.
	Source     string    `json:"source,omitempty"`
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
	source := c.source()
	if now().Sub(s.CheckedAt) >= checkEvery || s.CheckedAt.After(now()) || s.Source != source {
		// A failed or cut-short check counts too, so a slow or missing
		// network costs one short wait a day, not one per command.
		latest, err := c.latest(ctx)
		if s.Source != source {
			s.Latest = ""
		}
		s.CheckedAt, s.Source = now(), source
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

func (c *Checker) source() string {
	if c.Cask != "" {
		return c.Cask
	}
	return c.releases()
}

func (c *Checker) latest(ctx context.Context) (string, error) {
	if c.Cask != "" {
		return c.latestInCask(ctx)
	}
	return c.latestRelease(ctx)
}

// caskVersion is the cask's version stanza: version "1.2.3".
var caskVersion = regexp.MustCompile(`(?m)^\s*version\s+"([^"]*)"`)

// latestInCask reads the version the cask installs.
func (c *Checker) latestInCask(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Cask, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Transport: c.Transport}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the cask answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCask))
	if err != nil {
		return "", err
	}
	m := caskVersion.FindSubmatch(raw)
	if m == nil {
		return "", errors.New("the cask has no version")
	}
	version := string(m[1])
	if _, ok := parse(version); !ok {
		return "", errors.New("the cask's version is not a release: " + version)
	}
	return version, nil
}

// latestRelease reads the version of the newest release from where /latest
// redirects: .../releases/tag/v1.2.3.
func (c *Checker) latestRelease(ctx context.Context) (string, error) {
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
	path := resolve(executable)
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

// CaskFor returns the cask a copy of the CLI installed at executable is
// updated from, for Checker.Cask: Cask for a copy in Homebrew's Caskroom,
// otherwise "".
func CaskFor(executable string) string {
	if strings.Contains(filepath.ToSlash(resolve(executable)), "/Caskroom/") {
		return Cask
	}
	return ""
}

// resolve follows the links to executable, as Homebrew's bin/tenjin is one.
func resolve(executable string) string {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		return resolved
	}
	return executable
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
