package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// fakeAgent is a stand-in for Claude Code: a script that records how it was
// started, repairs the project, and reports in the real tool's stream format.
type fakeAgent struct {
	dir string
}

const fakeAgentScript = `#!/bin/sh
if [ "$1" = "--help" ]; then
  echo "  --restricted    Restricted mode"
  exit 0
fi
printf '%s\n' "$@" > "$FAKE_AGENT_DIR/args"
cat > "$FAKE_AGENT_DIR/prompt"
echo "$PATH" > "$FAKE_AGENT_DIR/path"
env | grep '^TENJIN_SDK_KEY' > "$FAKE_AGENT_DIR/keyvars"
n=$(cat "$FAKE_AGENT_DIR/runs" 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > "$FAKE_AGENT_DIR/runs"
if [ -n "$FAKE_AGENT_FAIL" ]; then
  echo "agent crashed" >&2
  exit 1
fi
if [ "$n" -ge "${FAKE_AGENT_FIX_ON_RUN:-1}" ]; then
  cp "$FAKE_AGENT_DIR/fixed-manifest.xml" app/src/main/AndroidManifest.xml
  if [ -n "$FAKE_AGENT_PLACEHOLDER" ]; then
    sed -i.bak 's/YOUR_SDK_KEY/__TENJIN_ANDROID_SDK_KEY__/' app/src/main/java/com/example/sample/MainActivity.kt
    rm -f app/src/main/java/com/example/sample/MainActivity.kt.bak
  fi
  echo "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Edit\",\"input\":{\"file_path\":\"$PWD/app/src/main/AndroidManifest.xml\"}}]}}"
fi
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"tenjin doctor --json ."}}]}}'
echo "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"Added the INTERNET permission. Key used: $FAKE_AGENT_SAYS_KEY\",\"permission_denials\":[]}"
`

// installAgent puts a fake `claude` where the CLI will find it.
func (h *harness) installAgent() *fakeAgent {
	h.t.Helper()
	if runtime.GOOS == "windows" {
		h.t.Skip("the fake agent is a shell script")
	}
	dir := h.t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte(fakeAgentScript), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixed-manifest.xml"), []byte(testproject.AndroidManifest), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.t.Setenv("FAKE_AGENT_DIR", dir)
	h.onPath("claude", script)
	return &fakeAgent{dir: dir}
}

// onPath makes a program findable, beside any installed before it.
func (h *harness) onPath(name, path string) {
	previous := h.env.lookPath
	h.env.lookPath = func(wanted string) (string, error) {
		if wanted == name {
			return path, nil
		}
		return previous(wanted)
	}
}

// fakeCodexScript stands in for `codex exec --json`, with the events the
// real one was seen to emit: commands wrapped in a login shell, file changes
// with absolute paths, and the report as the last message.
const fakeCodexScript = `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_CODEX_DIR/args"
cat > "$FAKE_CODEX_DIR/prompt"
echo '{"type":"thread.started","thread_id":"01a0fdba-284c-7ed2-88aa-48efee485cf3"}'
echo '{"type":"turn.started"}'
if [ -n "$FAKE_CODEX_FAIL" ]; then
  echo '{"type":"error","message":"{\"type\":\"error\",\"status\":400,\"error\":{\"message\":\"The model is not supported with this account.\"}}"}'
  exit 1
fi
echo '{"type":"item.started","item":{"type":"command_execution","command":"/bin/zsh -lc '"'"'cat app/src/main/AndroidManifest.xml'"'"'","status":"in_progress"}}'
cp "$FAKE_CODEX_DIR/fixed-manifest.xml" app/src/main/AndroidManifest.xml
echo "{\"type\":\"item.started\",\"item\":{\"type\":\"file_change\",\"changes\":[{\"path\":\"$PWD/app/src/main/AndroidManifest.xml\",\"kind\":\"update\"}]}}"
echo '{"type":"item.started","item":{"type":"command_execution","command":"/bin/zsh -lc '"'"'tenjin doctor --offline --json .'"'"'","status":"in_progress"}}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"Added the INTERNET permission."}}'
echo '{"type":"turn.completed","usage":{}}'
`

// installCodex puts a fake `codex` where the CLI will find it.
func (h *harness) installCodex() *fakeAgent {
	h.t.Helper()
	if runtime.GOOS == "windows" {
		h.t.Skip("the fake agent is a shell script")
	}
	dir := h.t.TempDir()
	script := filepath.Join(dir, "codex")
	if err := os.WriteFile(script, []byte(fakeCodexScript), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixed-manifest.xml"), []byte(testproject.AndroidManifest), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.t.Setenv("FAKE_CODEX_DIR", dir)
	h.onPath("codex", script)
	return &fakeAgent{dir: dir}
}

func (f *fakeAgent) read(name string) string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, name))
	return strings.TrimSpace(string(raw))
}

func (f *fakeAgent) runs() string { return f.read("runs") }

// brokenAndroid is a signed-in harness and a project that lacks the INTERNET
// permission, which the fake agent knows how to add.
func brokenAndroid(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	dir := testproject.Write(t, base.With(testproject.Files{
		manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1),
	}))
	return h, dir
}

func TestSetupIntegratesUsingTheAgent(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	// The real agent repeated a key in its report; make the fake do so too.
	// This one is not a key setup was given, as when the agent reads it out
	// of the project.
	t.Setenv("FAKE_AGENT_SAYS_KEY", "KEYFORTHEAGENT1234567890ABCDEFGH")

	code, stdout, stderr := h.typing("", "y").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"not fully set up in this project yet. 1 thing would stop events reaching Tenjin.",
		"Claude Code is installed on this machine and can do the integration for you",
		"under your own Claude Code account",
		"Integrate the Tenjin SDK using Claude Code? [Y/n]",
		"· edit app/src/main/AndroidManifest.xml",
		"· run tenjin doctor --json .",
		"1 file changed:",
		"Claude Code reports:",
		"Added the INTERNET permission. Key used: …EFGH",
		"Nothing here would stop events reaching Tenjin",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(stdout, "KEYFORTHEAGENT1234567890ABCDEFGH") {
		t.Error("the whole SDK key was printed")
	}
	if strings.Contains(stdout, "INTERNET permission is missing") {
		t.Errorf("the check after the agent still reports the fault:\n%s", stdout)
	}

	// The task goes in on standard input, so it is not in the process list.
	prompt, args := agent.read("prompt"), agent.read("args")
	for _, want := range []string{
		"INTERNET permission is missing", "app/src/main/AndroidManifest.xml", "Fix: Add <uses-permission",
		"bundle ID com.example.sample", "tenjin doctor --json .", "Do not commit",
		// The project's key is one Tenjin accepts, so the agent is to leave it.
		"SDK key: already in the project: leave it as it is",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(args, "INTERNET") || strings.Contains(args, "KEYFORTHEAGENT") {
		t.Errorf("the task or the key was passed as an argument: %q", args)
	}
	// The agent is confined to file edits and the doctor.
	if !strings.Contains(args, "Bash(tenjin doctor *)") || !strings.Contains(args, "acceptEdits") {
		t.Errorf("args = %q", args)
	}
	if !strings.HasPrefix(agent.read("path"), "/usr/local/bin:") {
		t.Errorf("tenjin's directory is not first on the agent's PATH: %q", agent.read("path"))
	}

	// The conversation is kept, and the developer is told how to pick it up:
	// the ID on screen is the one the agent was started with.
	resume := regexp.MustCompile(`claude --resume ([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})`).FindStringSubmatch(stdout)
	if resume == nil {
		t.Fatalf("no resume command with a session ID in the output:\n%s", stdout)
	}
	if !strings.Contains(args, "--session-id\n"+resume[1]+"\n") || strings.Contains(args, "--no-session-persistence") {
		t.Errorf("args = %q, session on screen = %s", args, resume[1])
	}
}

func TestSetupDoesNotStartTheAgentUnasked(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *harness)
		args  []string
	}{
		{"the developer declines", func(h *harness) { h.typing("", "n", "q") }, nil},
		{"--agent none", func(h *harness) { h.typing("", "q") }, []string{"--agent", "none"}},
		{"no terminal", func(h *harness) {}, nil},
		{"already running inside an agent", func(h *harness) { h.typing("", "q"); h.vars["CLAUDECODE"] = "1" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := brokenAndroid(t)
			agent := h.installAgent()
			tc.setup(h)
			code, stdout, _ := h.run(append([]string{"setup", dir}, tc.args...)...)
			if agent.runs() != "" {
				t.Fatalf("the agent was started:\n%s", stdout)
			}
			if code != 9 || !strings.Contains(stdout, "The full guide:") {
				t.Errorf("exit = %d\n%s", code, stdout)
			}
		})
	}
}

func TestSetupAgentFlagRunsWithoutATerminal(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	code, stdout, stderr := h.run("setup", dir, "--agent", "claude")
	if code != 0 || agent.runs() != "1" {
		t.Fatalf("exit = %d, runs = %q, stderr = %q\n%s", code, agent.runs(), stderr, stdout)
	}
	if strings.Contains(agent.read("prompt"), "TENJIN_SDK_KEY_PLACEHOLDER") {
		t.Errorf("the project has a working key, yet the agent was told to write a placeholder:\n%s", agent.read("prompt"))
	}
}

// placeholderAndroid is a signed-in harness and a project whose SDK key is
// still the placeholder from a guide.
func placeholderAndroid(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	return h, testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, "YOUR_SDK_KEY", 1)}))
}

// The agent never sees an SDK key. It writes a placeholder, and setup puts
// the key there itself once the agent is done.
func TestSetupNeverGivesTheAgentAKey(t *testing.T) {
	h, dir := placeholderAndroid(t)
	agent := h.installAgent()
	t.Setenv("FAKE_AGENT_PLACEHOLDER", "1")
	// A key exported in the developer's shell stays out of the agent's.
	t.Setenv("TENJIN_SDK_KEY_ANDROID", "KEYFORTHEAGENT1234567890ABCDEFGH")
	_, stdout, _ := h.run("setup", dir, "--agent", "claude", "--sdk-key", "KEYFORTHEAGENT1234567890ABCDEFGH")
	if vars := agent.read("keyvars"); vars != "" {
		t.Errorf("the agent inherited %q", vars)
	}

	prompt, args := agent.read("prompt"), agent.read("args")
	if strings.Contains(prompt, "KEYFORTHEAGENT") || strings.Contains(args, "KEYFORTHEAGENT") {
		t.Fatalf("the agent was given the key:\n%s", prompt)
	}
	for _, want := range []string{
		"SDK key: write the string __TENJIN_ANDROID_SDK_KEY__ exactly",
		"You are not given the SDK keys, on purpose.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}
	// The placeholder the agent wrote now holds the key, and the checks pass.
	source, _ := os.ReadFile(filepath.Join(dir, "app/src/main/java/com/example/sample/MainActivity.kt"))
	if !strings.Contains(string(source), `TenjinSDK.getInstance(this, "KEYFORTHEAGENT1234567890ABCDEFGH")`) {
		t.Errorf("source:\n%s", source)
	}
	if !strings.Contains(stdout, "✓ Wrote the Android SDK key at app/src/main/java/com/example/sample/MainActivity.kt:9") ||
		!strings.Contains(stdout, "Nothing here would stop events reaching Tenjin") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "KEYFORTHEAGENT1234567890ABCDEFGH") {
		t.Error("the whole key was printed")
	}
}

func TestSetupLeavesThePlaceholderWithoutAKey(t *testing.T) {
	h, dir := placeholderAndroid(t)
	h.installAgent()
	t.Setenv("FAKE_AGENT_PLACEHOLDER", "1")
	code, stdout, _ := h.run("setup", dir, "--agent", "claude")
	if code != 9 || !strings.Contains(stdout, "The Android SDK key is still the placeholder __TENJIN_ANDROID_SDK_KEY__") || !strings.Contains(stdout, "Run tenjin setup, which asks for the key") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
}

func TestSetupAgentFlagErrors(t *testing.T) {
	h, dir := brokenAndroid(t)
	if code, _, stderr := h.run("setup", dir, "--agent", "claude"); code != 5 || !strings.Contains(stderr, "not installed") {
		t.Errorf("agent missing: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("setup", dir, "--agent", "hal9000"); code != 2 || !strings.Contains(stderr, "unknown agent") {
		t.Errorf("unknown agent: exit = %d, stderr = %q", code, stderr)
	}
}

// When the first pass leaves work undone, the developer can send the agent
// back in from the check step.
func TestSetupContinuesWithTheAgentFromTheCheck(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	t.Setenv("FAKE_AGENT_FIX_ON_RUN", "2")
	h.vars["TENJIN_SDK_KEY"] = "KEYFORTHEAGENT1234567890ABCDEFGH"

	code, stdout, stderr := h.typing("", "y", "a").run("setup", dir)
	if code != 0 || agent.runs() != "2" {
		t.Fatalf("exit = %d, runs = %q, stderr = %q\n%s", code, agent.runs(), stderr, stdout)
	}
	for _, want := range []string{"No files were changed.", "a to continue with Claude Code", "1 file changed:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
}

// An agent that fails is reported and does not end the walkthrough: the
// checks still run and still decide the outcome.
func TestSetupSurvivesAFailingAgent(t *testing.T) {
	h, dir := brokenAndroid(t)
	h.installAgent()
	t.Setenv("FAKE_AGENT_FAIL", "1")

	code, stdout, _ := h.run("setup", dir, "--agent", "claude")
	if code != 9 {
		t.Errorf("exit = %d, want 9", code)
	}
	for _, want := range []string{"Claude Code did not finish", "agent crashed", "No files were changed.", "INTERNET permission is missing"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	// A run that never finished left no conversation worth pointing at.
	if strings.Contains(stdout, "claude --resume") {
		t.Errorf("a resume command was offered for a failed run:\n%s", stdout)
	}
}

func TestSetupIntegratesUsingCodex(t *testing.T) {
	h, dir := brokenAndroid(t)
	codex := h.installCodex()
	h.vars["TENJIN_SDK_KEY"] = "KEYFORTHEAGENT1234567890ABCDEFGH"

	// With one agent installed it is a yes-or-no question, as for Claude Code.
	code, stdout, stderr := h.typing("", "y").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"Codex is installed on this machine and can do the integration for you",
		"it runs in Codex's sandbox: it can change files in this project only, and has no network access",
		"Integrate the Tenjin SDK using Codex? [Y/n]",
		"· edit app/src/main/AndroidManifest.xml",
		"· run tenjin doctor --offline --json .",
		"Codex reports:",
		"Added the INTERNET permission.",
		// The ID is the one Codex reported, in the command Codex resumes with.
		"codex resume 01a0fdba-284c-7ed2-88aa-48efee485cf3",
		"Nothing here would stop events reaching Tenjin",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	// Reading a file is not progress worth a line.
	if strings.Contains(stdout, "run cat ") {
		t.Errorf("a read-only command was shown as progress:\n%s", stdout)
	}

	prompt, args := codex.read("prompt"), codex.read("args")
	// Its sandbox has no network, so it is told to run the offline check.
	if !strings.Contains(prompt, "run `tenjin doctor --offline --json .`") || !strings.Contains(prompt, "INTERNET permission is missing") {
		t.Errorf("the task:\n%s", prompt)
	}
	for _, want := range []string{"exec\n--json\n", "--sandbox\nworkspace-write\n", "mcp_servers={}", "-C\n" + dir} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %q", want, args)
		}
	}
	if strings.Contains(args, "KEYFORTHEAGENT") || strings.Contains(args, "dangerously") {
		t.Errorf("args = %q", args)
	}
}

func TestSetupAsksWhichAgentWhenThereAreTwo(t *testing.T) {
	answers := []struct{ typed, want string }{
		{"", "Claude Code is working"}, // Enter takes the first
		{"1", "Claude Code is working"},
		{"2", "Codex is working"},
		{"codex", "Codex is working"},
		{"n", "The full guide:"},
		{"what?", "The full guide:"}, // not a choice, so nothing is started
	}
	for _, tc := range answers {
		t.Run("answer "+tc.typed, func(t *testing.T) {
			h, dir := brokenAndroid(t)
			h.installAgent()
			h.installCodex()
			_, stdout, _ := h.typing("", tc.typed, "q").run("setup", dir)
			for _, want := range []string{
				"Claude Code and Codex are installed on this machine",
				"1) Claude Code: it may edit files and run 'tenjin doctor'",
				"2) Codex: it runs in Codex's sandbox",
				"Integrate the Tenjin SDK using Claude Code (1), Codex (2), or neither (n)? [1]",
				tc.want,
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("output lacks %q:\n%s", want, stdout)
				}
			}
		})
	}

	// The flag skips the question, and works without a terminal.
	h, dir := brokenAndroid(t)
	h.installAgent()
	h.installCodex()
	code, stdout, _ := h.run("setup", dir, "--agent", "codex")
	if code != 0 || !strings.Contains(stdout, "Codex is working") || strings.Contains(stdout, "installed on this machine") {
		t.Errorf("--agent codex: exit = %d\n%s", code, stdout)
	}
}

func TestSetupReportsWhyCodexFailed(t *testing.T) {
	h, dir := brokenAndroid(t)
	h.installCodex()
	t.Setenv("FAKE_CODEX_FAIL", "1")
	code, stdout, _ := h.run("setup", dir, "--agent", "codex")
	// Codex's own sentence, out of the JSON it arrives in; and no session to
	// resume, since nothing was done in it.
	if code != 9 || !strings.Contains(stdout, "Codex did not finish: The model is not supported with this account.") || strings.Contains(stdout, "codex resume") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
}
