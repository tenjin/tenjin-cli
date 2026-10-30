package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	h.env.telemetry = &telemetry.Client{Key: "k", Endpoint: srv.URL, State: h.env.telemetryState}
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
	code, _, _ := h.run("doctor", dir, "--sdk-key", "android="+key)
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
	if !slices.Equal(flags, []any{"sdk-key"}) {
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
	if c := telemetryClient(h.env.getenv, h.env.endpoints, h.env.telemetryState); c != nil {
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

// --offline asks for no network the command can do without: no event.
func TestOfflineSendsNoEvent(t *testing.T) {
	h, dir := brokenAndroid(t)
	bodies := h.telemetry()
	h.run("doctor", dir, "--offline")
	if len(*bodies) != 0 {
		t.Errorf("events: %v", *bodies)
	}
}

// The variable that hands an event over does nothing without the argument
// only the CLI passes: a command run with it set still runs.
func TestTheHandOverVariableAloneChangesNothing(t *testing.T) {
	t.Setenv(telemetryEventVar, "{}")
	var out, errOut bytes.Buffer
	if code := Execute([]string{"version"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Tenjin CLI") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Execute([]string{telemetrySendArg}, &out, &errOut); code != 0 || out.Len() != 0 {
		t.Errorf("the copy: exit = %d, stdout = %q", code, out.String())
	}
}

// A run asked to stay offline keeps the coding agent's own runs offline too.
func TestAnOfflineRunsAgentSendsNothing(t *testing.T) {
	h, dir := brokenAndroid(t)
	h.telemetry()
	agent := h.installAgent()
	h.run("doctor", dir, "--offline", "--agent", "claude")
	if !strings.Contains(agent.read("telemetryvars"), "TENJIN_NO_TELEMETRY=1") {
		t.Errorf("offline: agent's environment had %q", agent.read("telemetryvars"))
	}
	h, dir = brokenAndroid(t)
	h.telemetry()
	agent = h.installAgent()
	h.run("doctor", dir, "--agent", "claude")
	if agent.runs() != "1" || agent.read("telemetryvars") != "" {
		t.Errorf("online: runs = %q, agent's environment had %q", agent.runs(), agent.read("telemetryvars"))
	}
}

// A run asked to stay offline tells Claude Code to check its work offline
// too, in setup, doctor and sdk upgrade alike; the allow-list still lets it.
func TestAnOfflineRunsAgentChecksOffline(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(t *testing.T, dir string) []string
	}{
		{"setup", func(_ *testing.T, dir string) []string { return []string{"setup", dir} }},
		{"doctor", func(_ *testing.T, dir string) []string { return []string{"doctor", dir} }},
		{"sdk upgrade", func(t *testing.T, _ string) []string { return []string{"sdk", "upgrade", oldAndroid(t)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := brokenAndroid(t)
			agent := h.installAgent()
			h.run(append(tc.args(t, dir), "--offline", "--agent", "claude")...)
			if prompt := agent.read("prompt"); !strings.Contains(prompt, "run `tenjin doctor --offline --json .`") {
				t.Errorf("offline: the task:\n%s", prompt)
			}
			if !strings.Contains(agent.read("args"), "Bash(tenjin doctor *)") {
				t.Errorf("args = %q", agent.read("args"))
			}

			h, dir = brokenAndroid(t)
			agent = h.installAgent()
			h.run(append(tc.args(t, dir), "--agent", "claude")...)
			if prompt := agent.read("prompt"); !strings.Contains(prompt, "run `tenjin doctor --json .`") {
				t.Errorf("online: the task:\n%s", prompt)
			}
		})
	}
}

// A no at a question is a run like any other, said as such.
func TestADeclinedRunIsSaid(t *testing.T) {
	h, dir := newHarness(t), t.TempDir()
	bodies := h.telemetry()
	h.typing("n")
	if code, _, _ := h.run("agent", "install", dir, "--agent", "claude"); code != 0 || len(*bodies) != 1 {
		t.Fatalf("exit = %d, events %v", code, *bodies)
	}
	if props := eventProperties(t, (*bodies)[0]); props["declined"] != true || props["status"] != "ok" {
		t.Errorf("properties = %v", props)
	}
}

func telemetryStatusOf(t *testing.T, h *harness) telemetryStatus {
	t.Helper()
	code, stdout, stderr := h.run("telemetry", "status", "--json")
	var s telemetryStatus
	if err := json.Unmarshal([]byte(stdout), &s); code != 0 || err != nil {
		t.Fatalf("exit = %d, %v\n%s%s", code, err, stdout, stderr)
	}
	return s
}

// Turned off, nothing more is sent, from this command or any other, until
// it is turned on again.
func TestTelemetryOffAndOn(t *testing.T) {
	h := newHarness(t)
	bodies := h.telemetry()
	h.run("version")
	if s := telemetryStatusOf(t, h); !s.Enabled || s.Setting != "on" || s.Reason != "" {
		t.Errorf("status = %+v", s)
	}
	sent := len(*bodies)

	code, stdout, stderr := h.run("telemetry", "off")
	if code != 0 || stdout != "Usage statistics are off. Nothing more is sent from this machine, and its random ID was deleted.\n" || stderr != "" {
		t.Fatalf("off: exit = %d\n%s%s", code, stdout, stderr)
	}
	// A client made before the choice sends nothing either.
	bodies = h.telemetry()
	h.run("version")
	if want := (telemetryStatus{Setting: "off", Reason: "turned off with 'tenjin telemetry off'"}); telemetryStatusOf(t, h) != want {
		t.Errorf("status = %+v", telemetryStatusOf(t, h))
	}
	if len(*bodies) != 0 || sent != 2 {
		t.Errorf("sent %v after it was turned off, and %d before", *bodies, sent)
	}
	_, stdout, _ = h.run("telemetry", "status")
	lacks(t, stdout, "Usage statistics are off: turned off with 'tenjin telemetry off'.")

	code, stdout, _ = h.run("telemetry", "on")
	if code != 0 || !strings.HasPrefix(stdout, "Usage statistics are on. What they hold: "+privacyURL) {
		t.Fatalf("on: exit = %d\n%s", code, stdout)
	}
	h.run("version")
	if s := telemetryStatusOf(t, h); !s.Enabled || s.Setting != "on" || len(*bodies) == 0 {
		t.Errorf("status = %+v, sent %v", s, *bodies)
	}
}

// Off in the environment, it stays off whatever the setting, and says why.
func TestTelemetryOffInTheEnvironmentSaysSo(t *testing.T) {
	h := newHarness(t)
	h.vars[telemetry.OffVar] = "1"
	if want := (telemetryStatus{Setting: "on", Reason: telemetry.OffVar + " is set"}); telemetryStatusOf(t, h) != want {
		t.Errorf("status = %+v", telemetryStatusOf(t, h))
	}
	code, stdout, _ := h.run("telemetry", "on")
	if code != 0 || stdout != "Usage statistics are on, but none are sent: TENJIN_NO_TELEMETRY is set.\n" {
		t.Errorf("on: exit = %d\n%s", code, stdout)
	}
	code, stdout, _ = h.run("telemetry", "off", "--json")
	var s telemetryStatus
	if err := json.Unmarshal([]byte(stdout), &s); code != 0 || err != nil || s != (telemetryStatus{Setting: "off", Reason: "turned off with 'tenjin telemetry off'"}) {
		t.Errorf("off --json: exit = %d, %+v, %v", code, s, err)
	}
}

// Without a configuration folder there is nowhere to keep the choice.
func TestTelemetryOffWithoutAConfigurationFolder(t *testing.T) {
	h := newHarness(t)
	h.env.telemetryState = ""
	code, _, stderr := h.run("telemetry", "off")
	if code != 1 || !strings.Contains(stderr, "no configuration folder") || !strings.Contains(stderr, "TENJIN_NO_TELEMETRY=1") {
		t.Errorf("exit = %d\n%s", code, stderr)
	}
	if s := telemetryStatusOf(t, h); s.Enabled || s.Reason == "" {
		t.Errorf("status = %+v", s)
	}
}
