package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/telemetry"
)

// telemetry points the harness's events at a server of its own, and returns
// the bodies it received.
func (h *harness) telemetry() *[]string {
	h.t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		bodies = append(bodies, string(raw))
	}))
	h.t.Cleanup(srv.Close)
	h.env.telemetry = &telemetry.Client{Key: "k", Endpoint: srv.URL, State: filepath.Join(h.t.TempDir(), "telemetry.json")}
	return &bodies
}

func eventProperties(t *testing.T, body string) map[string]any {
	t.Helper()
	var sent struct {
		Events []struct {
			Properties map[string]any `json:"event_properties"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(body), &sent); err != nil || len(sent.Events) != 1 {
		t.Fatalf("body %s: %v", body, err)
	}
	return sent.Events[0].Properties
}

// A command's event names the command, its flags and the project's shape,
// and never a flag's value, a path or a bundle ID.
func TestACommandsEventSaysItsShapeAndNothingOfTheProject(t *testing.T) {
	h, dir := brokenAndroid(t)
	bodies := h.telemetry()
	const key = "0123456789abcdef0123456789abcdef"
	code, _, _ := h.run("doctor", dir, "--offline", "--sdk-key", "android="+key)
	if code != 9 || len(*bodies) != 1 {
		t.Fatalf("exit = %d, %d events", code, len(*bodies))
	}
	body := (*bodies)[0]
	for _, secret := range []string{key, dir, "com.example.sample", "Sample"} {
		if strings.Contains(body, secret) {
			t.Errorf("the event carries %q: %s", secret, body)
		}
	}
	props := eventProperties(t, body)
	if props["command"] != "tenjin doctor" || props["error_class"] != "checks_failed" || props["framework"] != "native" {
		t.Errorf("properties = %v", props)
	}
	flags, _ := props["set_flags"].([]any)
	if !slices.Equal(flags, []any{"offline", "sdk-key"}) {
		t.Errorf("flags = %v", props["set_flags"])
	}
	failed, _ := props["failed_checks"].([]any)
	if len(failed) == 0 {
		t.Errorf("failed checks = %v", props["failed_checks"])
	}
}

// Shell completion runs as the person types, and sends nothing.
func TestCompletionSendsNoEvent(t *testing.T) {
	h := newHarness(t)
	bodies := h.telemetry()
	h.run("__complete", "doc")
	h.run("completion", "zsh")
	if len(*bodies) != 0 {
		t.Errorf("events: %v", *bodies)
	}
	h.run("version")
	if len(*bodies) != 1 || eventProperties(t, (*bodies)[0])["command"] != "tenjin version" {
		t.Errorf("events: %v", *bodies)
	}
}

// Only a release build with its key sends, only to production, and never
// when it was turned off.
func TestTelemetryIsOffOutsideARelease(t *testing.T) {
	h := newHarness(t)
	if c := telemetryClient(h.env.getenv, h.env.endpoints); c != nil {
		t.Errorf("a build without a key, against a fake: %+v", c)
	}
}

// A command hands its event to a copy of the CLI and ends; with --verbose
// it sends the event itself, so the request is shown.
func TestTheEventIsHandedOnUnlessTheRequestIsShown(t *testing.T) {
	h := newHarness(t)
	bodies := h.telemetry()
	var handed []telemetry.Event
	h.env.detach = func(ev telemetry.Event) error {
		handed = append(handed, ev)
		return nil
	}
	h.run("version")
	if len(handed) != 1 || handed[0].Command != "tenjin version" || len(*bodies) != 0 {
		t.Errorf("handed %v, sent %v", handed, *bodies)
	}
	_, _, stderr := h.run("version", "--verbose")
	if len(handed) != 1 || len(*bodies) != 1 || !strings.Contains(stderr, "POST") {
		t.Errorf("handed %v, sent %v, stderr %q", handed, *bodies, stderr)
	}
}

// What a copy receives is the event as it was made.
func TestAHandedEventSurvivesTheHandOver(t *testing.T) {
	ev := telemetry.Event{Command: "tenjin setup", Flags: []string{"yes"}, ExitCode: 130, Error: "interrupted", Duration: 3 * time.Second,
		Facts: map[string]any{"setup_step": 3, "platforms": []string{"android"}}}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var back telemetry.Event
	if err := json.Unmarshal(raw, &back); err != nil || back.Command != ev.Command || back.Duration != ev.Duration || back.Facts["setup_step"] != float64(3) {
		t.Errorf("%+v, %v", back, err)
	}
}
