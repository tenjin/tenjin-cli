package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// Where Tenjin's API serves SDK keys, nobody copies one: setup fetches the
// key, checks it, and writes it over the placeholder, with no terminal.
func TestSetupFetchesTheKeyFromTenjin(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.serveKeys(app.ID, goodKey)
	h.signIn()

	code, stdout, stderr := h.run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		`✓ Android, key …GOOD ("SDK Key 1") from Tenjin: Tenjin accepts this key for com.example.sample`,
		"✓ Wrote the Android SDK key at "+androidActivity,
		// Once written, the key is checked where the app reads it.
		"Tenjin accepts the SDK key …GOOD for com.example.sample  "+androidActivity,
		"Nothing here would stop events reaching Tenjin",
	)
	if strings.Contains(stdout, `"SDK Keys" card`) || strings.Contains(stdout, "--sdk-key") {
		t.Errorf("setup sent the developer to copy a key it had:\n%s", stdout)
	}
	if strings.Contains(stdout+stderr, goodKey) {
		t.Error("the whole key was printed")
	}
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if !strings.Contains(string(source), goodKey) {
		t.Errorf("source:\n%s", source)
	}
}

// The keys come oldest first; one Tenjin refuses is passed over.
func TestSetupUsesTheFirstFetchedKeyTenjinAccepts(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.serveKeys(app.ID, wrongKey, goodKey)
	h.fake.keyAnswers[wrongKey] = "not logged"
	h.signIn()

	code, stdout, _ := h.run("setup", dir)
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if code != 0 || !strings.Contains(string(source), goodKey) {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
}

// When the API has nothing to give, setup says why and falls back to the
// dashboard steps. Where the endpoint is not deployed, it says nothing.
func TestSetupFallsBackToTheDashboard(t *testing.T) {
	for _, c := range []struct {
		name  string
		serve func(h *harness, appID string)
		note  string
	}{
		{"not served", func(*harness, string) {}, ""},
		{"no key yet", func(h *harness, id string) { h.fake.serveKeys(id) }, "! Android: the app has no SDK key yet."},
		{"role", func(h *harness, id string) { h.fake.serveKeys(id, goodKey); h.fake.denyKeys = true }, "! Android: your Tenjin role cannot read this app's SDK keys."},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, dir := agentPlaceholderAndroid(t)
			app := h.fake.addApp("Sample", "com.example.sample", "android")
			c.serve(h, app.ID)
			h.signIn()

			code, stdout, _ := h.run("setup", dir)
			lacks(t, stdout, `"SDK Keys" card`, "Then pass it with --sdk-key android=<key>.")
			if c.note != "" {
				lacks(t, stdout, c.note)
			} else if strings.Contains(stdout, "from Tenjin") || strings.Contains(stdout, "! Android:") {
				t.Errorf("a missing endpoint was reported:\n%s", stdout)
			}
			if code != 9 {
				t.Errorf("exit = %d, want 9 with the placeholder left", code)
			}
		})
	}
}

// A coding agent can finish the job itself: set-keys fetches the key, and
// the agent sees only its last four characters.
func TestSetKeysFetchesTheKeyForAnAgent(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.serveKeys(app.ID, goodKey)
	h.signIn()

	code, stdout, stderr := h.run("project", "set-keys", dir, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var result struct{ Filled []struct{ Platform string } }
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Filled) != 1 {
		t.Errorf("result = %s (%v)", stdout, err)
	}
	if strings.Contains(stdout+stderr, goodKey) {
		t.Error("the whole key was printed")
	}
	if !strings.Contains(stderr, "…GOOD") {
		t.Errorf("stderr = %q", stderr)
	}
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if strings.Contains(string(source), keyfill.Placeholder(project.Android)) {
		t.Errorf("the placeholder is still there:\n%s", source)
	}
}

func TestAppsKeysListShowsTheKeysMasked(t *testing.T) {
	h := newHarness(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.serveKeys(app.ID, goodKey)
	h.signIn()

	code, stdout, _ := h.run("apps", "keys", "list", "--app", app.ID, "--json")
	var got struct {
		Keys []struct {
			ID, Name, Masked string
		}
		Available bool
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || code != 0 {
		t.Fatalf("exit = %d, err = %v", code, err)
	}
	if !got.Available || len(got.Keys) != 1 || got.Keys[0].Masked != "…GOOD" || got.Keys[0].Name != "SDK Key 1" {
		t.Errorf("result = %+v", got)
	}
	_, text, _ := h.run("apps", "keys", "list", "--app", app.ID)
	lacks(t, text, "SDK keys of Sample (android, com.example.sample):", "  …GOOD  SDK Key 1")
	if strings.Contains(stdout+text, goodKey) {
		t.Error("the whole key was printed")
	}
}
