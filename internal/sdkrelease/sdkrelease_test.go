package sdkrelease

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Each registry's real answer, cut down to what is read.
func registry(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/maven-metadata.xml":
			_, _ = w.Write([]byte(`<metadata><groupId>com.tenjin</groupId><artifactId>android-sdk</artifactId><versioning><latest>2.0.1</latest><release>2.0.1</release></versioning></metadata>`))
		case "/pub":
			_, _ = w.Write([]byte(`{"name":"tenjin_plugin","latest":{"version":"1.5.1","pubspec":{}}}`))
		case "/npm/latest":
			_, _ = w.Write([]byte(`{"name":"react-native-tenjin","version":"1.7.2"}`))
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/1.20.1", http.StatusFound)
		case "/tagged/releases/latest":
			http.Redirect(w, r, "/tagged/releases/tag/v1.22.4", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEachRegistryIsRead(t *testing.T) {
	srv := registry(t)
	l := &Lookup{State: filepath.Join(t.TempDir(), "sdk-releases.json"), Sources: map[string]Source{
		"com.tenjin:android-sdk": MavenCentral(srv.URL + "/maven-metadata.xml"),
		"tenjin_plugin":          PubDev(srv.URL + "/pub"),
		"react-native-tenjin":    NPM(srv.URL + "/npm/latest"),
		"TenjinSDK":              GitHubLatest(srv.URL + "/releases"),
		"com.tenjin.sdk":         GitHubLatest(srv.URL + "/tagged/releases/"),
		"missing":                NPM(srv.URL + "/nothing"),
	}}
	got := l.Latest(context.Background())
	want := map[string]string{
		"com.tenjin:android-sdk": "2.0.1",
		"tenjin_plugin":          "1.5.1",
		"react-native-tenjin":    "1.7.2",
		"TenjinSDK":              "1.20.1",
		"com.tenjin.sdk":         "1.22.4",
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// The answers are kept for a day; a source that fails keeps its last one.
func TestAnswersAreKeptForADay(t *testing.T) {
	var asked atomic.Int32
	fail := false
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	l := &Lookup{
		State: filepath.Join(t.TempDir(), "tenjin", "sdk-releases.json"),
		Now:   func() time.Time { return now },
		Sources: map[string]Source{"com.tenjin:android-sdk": func(context.Context, *http.Client) (string, error) {
			asked.Add(1)
			if fail {
				return "", errors.New("offline")
			}
			return "2.0.1", nil
		}},
	}
	l.Latest(context.Background())
	now = now.Add(time.Hour)
	if got := l.Latest(context.Background())["com.tenjin:android-sdk"]; got != "2.0.1" || asked.Load() != 1 {
		t.Errorf("got %q after %d requests", got, asked.Load())
	}
	fail = true
	now = now.Add(24 * time.Hour)
	if got := l.Latest(context.Background())["com.tenjin:android-sdk"]; got != "2.0.1" || asked.Load() != 2 {
		t.Errorf("after a failed check: %q, %d requests", got, asked.Load())
	}
	// A check nothing answered is not an answer, and is not asked again for
	// an hour, so runs behind a firewall do not each wait for it.
	now = now.Add(time.Minute)
	if l.Latest(context.Background()); asked.Load() != 2 {
		t.Errorf("a failed check was asked again at once: %d requests", asked.Load())
	}
	now = now.Add(time.Hour)
	if l.Latest(context.Background()); asked.Load() != 3 {
		t.Errorf("a failed check was kept for over an hour: %d requests", asked.Load())
	}
	fail = false
	now = now.Add(time.Hour)
	if got := l.Latest(context.Background())["com.tenjin:android-sdk"]; got != "2.0.1" || asked.Load() != 4 {
		t.Errorf("back online: %q, %d requests", got, asked.Load())
	}
	// An answer is kept for a day again.
	now = now.Add(2 * time.Hour)
	if l.Latest(context.Background()); asked.Load() != 4 {
		t.Errorf("an answer after a failure was not kept: %d requests", asked.Load())
	}
}
