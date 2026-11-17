// Package sdkrelease finds the newest release of each Tenjin SDK in its
// public registry: Maven Central, pub.dev, npm, and GitHub for the iOS and
// Unity SDKs. It asks at most once a day and keeps the answers in a small
// state file, so the checks stay current between releases of the CLI.
package sdkrelease

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// checkEvery is how long the answers are kept, and retryEvery how long a
// check nothing answered is: behind a firewall that drops the requests,
// every run would otherwise wait for them again.
const (
	checkEvery = 24 * time.Hour
	retryEvery = time.Hour
)

// Source finds the newest release of one SDK.
type Source func(ctx context.Context, client *http.Client) (string, error)

// Lookup finds the newest releases.
type Lookup struct {
	// Transport sends the requests; nil is http.DefaultTransport.
	Transport http.RoundTripper
	// State is the file the answers are kept in.
	State string
	// Sources are keyed by the package prefix the checks use. Nil is
	// Registries.
	Sources map[string]Source
	Now     func() time.Time
}

type state struct {
	CheckedAt time.Time         `json:"checked_at"`
	FailedAt  time.Time         `json:"failed_at,omitzero"`
	Versions  map[string]string `json:"versions"`
}

// Latest returns the newest version of each SDK it could find, keyed by
// package prefix. What it cannot find is left out; it never fails, because
// the CLI knows a version of each SDK without it.
func (l *Lookup) Latest(ctx context.Context) map[string]string {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	s := l.load()
	if s.Versions == nil {
		s.Versions = map[string]string{}
	}
	if fresh(s.CheckedAt, checkEvery, now()) || fresh(s.FailedAt, retryEvery, now()) {
		return s.Versions
	}
	sources := l.Sources
	if sources == nil {
		sources = Registries
	}
	client := &http.Client{Transport: l.Transport}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		answered bool
	)
	for prefix, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version, err := source(ctx, client)
			if err != nil || version == "" {
				// The last answer stands until a new one comes.
				return
			}
			mu.Lock()
			s.Versions[prefix] = version
			answered = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	switch {
	case answered:
		s.CheckedAt, s.FailedAt = now(), time.Time{}
	case ctx.Err() != nil:
		// Stopped, not failed.
		return s.Versions
	default:
		// Offline, or every registry failed: the last answers stand, and
		// the registries are asked again in an hour.
		s.FailedAt = now()
	}
	l.save(s)
	return s.Versions
}

// fresh reports whether a time is less than d before now, and not after it.
func fresh(at time.Time, d time.Duration, now time.Time) bool {
	return now.Sub(at) < d && !at.After(now)
}

func (l *Lookup) load() state {
	var s state
	if raw, err := os.ReadFile(l.State); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// save writes the state through a temporary file. A state that cannot be
// saved only means the next run asks again.
func (l *Lookup) save(s state) {
	raw, err := json.Marshal(s)
	if err != nil || l.State == "" {
		return
	}
	dir := filepath.Dir(l.State)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".sdk-releases-*")
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
	_ = os.Rename(tmp.Name(), l.State)
}

// Registries are where each SDK is published.
var Registries = map[string]Source{
	"com.tenjin:android-sdk": MavenCentral("https://repo1.maven.org/maven2/com/tenjin/android-sdk/maven-metadata.xml"),
	// The pod and the Swift package are released from the same tags.
	"TenjinSDK":              GitHubLatest("https://github.com/tenjin/tenjin-ios-sdk/releases"),
	"tenjin_plugin":          PubDev("https://pub.dev/api/packages/tenjin_plugin"),
	"react-native-tenjin":    NPM("https://registry.npmjs.org/react-native-tenjin/latest"),
	"ionic-capacitor-tenjin": NPM("https://registry.npmjs.org/ionic-capacitor-tenjin/latest"),
	"com.tenjin.sdk":         GitHubLatest("https://github.com/tenjin/tenjin-unity-sdk/releases"),
}

// MavenCentral reads the release a Maven repository's metadata names.
func MavenCentral(metadata string) Source {
	return func(ctx context.Context, client *http.Client) (string, error) {
		body, err := get(ctx, client, metadata)
		if err != nil {
			return "", err
		}
		var m struct {
			Versioning struct {
				Release string `xml:"release"`
			} `xml:"versioning"`
		}
		if err := xml.Unmarshal(body, &m); err != nil {
			return "", err
		}
		return m.Versioning.Release, nil
	}
}

// PubDev reads a package's latest version from pub.dev's API.
func PubDev(api string) Source {
	return func(ctx context.Context, client *http.Client) (string, error) {
		body, err := get(ctx, client, api)
		if err != nil {
			return "", err
		}
		var p struct {
			Latest struct {
				Version string `json:"version"`
			} `json:"latest"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return "", err
		}
		return p.Latest.Version, nil
	}
}

// NPM reads the version of a package's latest tag.
func NPM(latest string) Source {
	return func(ctx context.Context, client *http.Client) (string, error) {
		body, err := get(ctx, client, latest)
		if err != nil {
			return "", err
		}
		var p struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return "", err
		}
		return p.Version, nil
	}
}

// GitHubLatest reads the tag a repository's releases/latest redirects to.
// It needs no API token and counts against no API limit.
func GitHubLatest(releases string) Source {
	return func(ctx context.Context, client *http.Client) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimSuffix(releases, "/")+"/latest", nil)
		if err != nil {
			return "", err
		}
		noRedirect := *client
		noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := noRedirect.Do(req)
		if err != nil {
			return "", err
		}
		_ = resp.Body.Close()
		location := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode > 399 || !strings.Contains(location, "/releases/tag/") {
			return "", errors.New("no latest release")
		}
		return strings.TrimPrefix(location[strings.LastIndex(location, "/")+1:], "v"), nil
	}
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
