package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/session"
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
env | grep '^TENJIN_NO_TELEMETRY' > "$FAKE_AGENT_DIR/telemetryvars"
n=$(cat "$FAKE_AGENT_DIR/runs" 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > "$FAKE_AGENT_DIR/runs"
if [ -n "$FAKE_AGENT_FAIL" ]; then
  echo "agent crashed" >&2
  exit 1
fi
if [ -n "$FAKE_AGENT_DO" ]; then
  sh -c "$FAKE_AGENT_DO"
elif [ "$n" -ge "${FAKE_AGENT_FIX_ON_RUN:-1}" ]; then
  cp "$FAKE_AGENT_DIR/fixed-manifest.xml" app/src/main/AndroidManifest.xml
  if [ -n "$FAKE_AGENT_PLACEHOLDER" ]; then
    sed -i.bak 's/YOUR_SDK_KEY/__TENJIN_ANDROID_SDK_KEY__/' app/src/main/java/com/example/sample/MainActivity.kt
    rm -f app/src/main/java/com/example/sample/MainActivity.kt.bak
  fi
  echo "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Edit\",\"input\":{\"file_path\":\"$PWD/app/src/main/AndroidManifest.xml\"}}]}}"
  if [ -n "$FAKE_AGENT_NEW_FILE" ]; then
    : > "$FAKE_AGENT_NEW_FILE"
  fi
fi
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"tenjin doctor --json ."}}]}}'
if [ -n "$FAKE_AGENT_SAYS_KEY" ]; then
  echo "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"name\":\"Bash\",\"input\":{\"command\":\"grep -r $FAKE_AGENT_SAYS_KEY .\"}}]}}"
fi
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
// with absolute paths, and the report as the last message. It also answers
// `codex mcp list --json` with the servers in FAKE_CODEX_MCP, turned off
// when an override names them, unless FAKE_CODEX_MCP_STAYS is set; and
// `codex features list` in the real one's columns, with each feature an
// override turns off listed as off, but the one FAKE_CODEX_FEATURE_STAYS
// names, which stays on, and the one FAKE_CODEX_FEATURE_UNKNOWN names,
// which this Codex does not have.
const fakeCodexScript = `#!/bin/sh
case " $* " in *" features list "*)
  printf '%s\n' "$@" >> "$FAKE_CODEX_DIR/featureargs"
  [ -n "$FAKE_CODEX_FEATURES_FAIL" ] && { echo "error: unrecognized subcommand 'features'" >&2; exit 2; }
  echo "shell_tool                         stable             true"
  echo "code_mode                          under development  false"
  for arg in "$@"; do
    case "$arg" in features.*=false)
      name=${arg#features.}; name=${name%=false}
      [ "$name" = "$FAKE_CODEX_FEATURE_UNKNOWN" ] && continue
      state=false; [ "$name" = "$FAKE_CODEX_FEATURE_STAYS" ] && state=true
      echo "$name                    stable             $state" ;;
    esac
  done
  exit 0 ;;
esac
case " $* " in *" mcp list --json "*)
  printf '%s\n' "$@" >> "$FAKE_CODEX_DIR/mcpargs"
  pwd -P > "$FAKE_CODEX_DIR/mcpdir"
  [ -n "$FAKE_CODEX_MCP_FAIL" ] && { echo "error: unexpected argument '--json'" >&2; exit 2; }
  case " $* " in *" mcp_servers="*)
    [ -z "$FAKE_CODEX_MCP_STAYS" ] && { echo "${FAKE_CODEX_MCP:-[]}" | sed 's/"enabled":true/"enabled":false/g'; exit 0; } ;;
  esac
  echo "${FAKE_CODEX_MCP:-[]}"
  exit 0 ;;
esac
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
		"not fully set up in this project yet. 1 thing would stop events reaching Tenjin:\n  ✗ ",
		"Claude Code is installed on this machine and can do the integration for you",
		"under your own Claude Code account",
		"it uses tokens from that account and edits files in this project: commit or stash your work first, so you can review its changes and undo them;",
		"Integrate the Tenjin SDK using Claude Code? [Y/n]",
		"· edit app/src/main/AndroidManifest.xml",
		"· run tenjin doctor --json .",
		"· run grep -r …EFGH .",
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
	if after := stdout[strings.Index(stdout, "Claude Code reports:"):]; strings.Contains(after, "INTERNET permission is missing") {
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
	for _, want := range []string{"No files were changed.", "a to have Claude Code fix them", "1 file changed:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
}

// A developer who said no to the agent in step 5 can still call it in from
// the check.
func TestSetupOffersTheAgentAgainAtTheCheck(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	code, stdout, stderr := h.typing("", "n", "a").run("setup", dir)
	if agent.runs() != "1" || !strings.Contains(stdout, "a to have Claude Code fix them") {
		t.Fatalf("exit = %d, runs = %q, stderr = %q\n%s", code, agent.runs(), stderr, stdout)
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

// Codex's sandbox can read the arguments and environment of the run, so a
// key given there is refused. A key setup fetches from Tenjin is in neither.
func TestAKeyTenjinServesDoesNotStopCodex(t *testing.T) {
	h, dir := brokenAndroid(t)
	h.fake.serveKeys(h.fake.apps[0].ID, testproject.Key)
	h.installCodex()
	code, stdout, stderr := h.run("setup", dir, "--agent", "codex")
	if !strings.Contains(stdout, "Codex is working on the integration") {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
}

func TestSetupIntegratesUsingCodex(t *testing.T) {
	h, dir := brokenAndroid(t)
	codex := h.installCodex()

	// With one agent installed it is a yes-or-no question, as for Claude Code.
	code, stdout, stderr := h.typing("", "y").run("setup", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"Codex is installed on this machine and can do the integration for you",
		"it runs in Codex's sandbox: it can change files in this project only and has no network access, but it can read files elsewhere on this machine",
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
	for _, want := range []string{
		"exec\n--json\n", "--sandbox\nworkspace-write\n", "-c\nfeatures.apps=false\n", "-C\n" + dir,
		// Nothing but the project is writable: not /tmp, not $TMPDIR.
		"sandbox_workspace_write.writable_roots=[]",
		"sandbox_workspace_write.exclude_slash_tmp=true",
		"sandbox_workspace_write.exclude_tmpdir_env_var=true",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %q", want, args)
		}
	}
	if strings.Contains(args, "dangerously") {
		t.Errorf("args = %q", args)
	}
}

// An override of mcp_servers merges with the developer's configuration
// rather than replacing it, so each server Codex lists is turned off by name,
// and Codex is asked again to be sure none is left on.
func TestCodexRunsWithEveryMCPServerTurnedOff(t *testing.T) {
	h, dir := brokenAndroid(t)
	codex := h.installCodex()
	t.Setenv("FAKE_CODEX_MCP", `[{"name":"github","enabled":true},{"name":"my.docs","enabled":true},{"name":"off","enabled":false}]`)
	if code, stdout, stderr := h.run("setup", dir, "--agent", "codex"); code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	const off = `mcp_servers={github={enabled=false},"my.docs"={enabled=false},off={enabled=false}}`
	if args := codex.read("args"); !strings.Contains(args, "-c\n"+off+"\n") || strings.Contains(args, "mcp_servers={}") {
		t.Errorf("args = %q", args)
	}
	// Listed where Codex runs, since a project can bring servers of its own,
	// and listed again with the overrides.
	if real, _ := filepath.EvalSymlinks(dir); codex.read("mcpdir") != real {
		t.Errorf("listed in %q, not %q", codex.read("mcpdir"), real)
	}
	if mcp := codex.read("mcpargs"); strings.Count(mcp, "list\n--json") != 2 || !strings.Contains(mcp, off) {
		t.Errorf("mcp list args = %q", mcp)
	}
}

func TestCodexIsNotStartedWhenItsMCPServersStayOn(t *testing.T) {
	for _, tc := range []struct{ name, variable string }{
		{"the override does not take", "FAKE_CODEX_MCP_STAYS"},
		{"this version cannot list them", "FAKE_CODEX_MCP_FAIL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := brokenAndroid(t)
			codex := h.installCodex()
			t.Setenv("FAKE_CODEX_MCP", `[{"name":"github","enabled":true}]`)
			t.Setenv(tc.variable, "1")
			code, stdout, _ := h.run("setup", dir, "--agent", "codex")
			if codex.read("args") != "" {
				t.Fatal("Codex was started")
			}
			if code != 9 || !strings.Contains(stdout, "its MCP servers could not be turned off") || !strings.Contains(stdout, "No files were changed.") {
				t.Errorf("exit = %d\n%s", code, stdout)
			}
		})
	}
}

// Codex's features that act outside its sandbox are turned off, and Codex
// is asked, with the run's overrides, whether they are.
func TestCodexRunsWithItsFeaturesCheckedOff(t *testing.T) {
	h, dir := brokenAndroid(t)
	codex := h.installCodex()
	t.Setenv("FAKE_CODEX_MCP", `[{"name":"github","enabled":true}]`)
	if code, stdout, stderr := h.run("setup", dir, "--agent", "codex"); code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	args, listed := codex.read("args"), codex.read("featureargs")
	for _, feature := range []string{"apps", "plugins", "hooks", "computer_use", "multi_agent", "guardian_approval"} {
		if want := "-c\nfeatures." + feature + "=false\n"; !strings.Contains(args, want) || !strings.Contains(listed, want) {
			t.Errorf("%s is not turned off in the run and in the list:\nrun: %q\nlist: %q", feature, args, listed)
		}
	}
	// The list is asked for with every override the run gets, the servers'
	// included.
	if !strings.Contains(listed, "mcp_servers={github={enabled=false}}") || !strings.HasSuffix(listed, "\nfeatures\nlist") {
		t.Errorf("features list args = %q", listed)
	}
	// A command that asks to leave the sandbox is refused, whoever the
	// developer's configuration has review it.
	for _, want := range []string{"-c\napproval_policy=\"never\"\n", "-c\napprovals_reviewer=\"user\"\n"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q", want)
		}
	}
}

func TestCodexIsNotStartedUnlessItsFeaturesAreOff(t *testing.T) {
	for _, tc := range []struct{ name, variable, value, says string }{
		{"one stays on", "FAKE_CODEX_FEATURE_STAYS", "computer_use", `its feature "computer_use", which acts outside its sandbox, stays on`},
		{"one is unknown, as after a rename", "FAKE_CODEX_FEATURE_UNKNOWN", "browser_use", `it does not have the feature "browser_use"`},
		{"this version cannot list them", "FAKE_CODEX_FEATURES_FAIL", "1", "'codex features list'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := brokenAndroid(t)
			codex := h.installCodex()
			t.Setenv(tc.variable, tc.value)
			code, stdout, _ := h.run("setup", dir, "--agent", "codex")
			if codex.read("args") != "" {
				t.Fatal("Codex was started")
			}
			if code != 9 || !strings.Contains(stdout, tc.says) || !strings.Contains(stdout, "No files were changed.") {
				t.Errorf("exit = %d\n%s", code, stdout)
			}
		})
	}
}

// filedStore keeps the sign-in in memory but says it is in a file, as on a
// machine without a keychain.
type filedStore struct {
	*session.MemoryStore
	path string
}

func (f filedStore) CredentialsFile(string) string { return f.path }

// Codex's sandbox reads the whole disk, so it is not started where the
// sign-in is in a file it could read.
func TestCodexIsNotStartedWhereTheSignInIsInAFile(t *testing.T) {
	const file = "/home/me/.config/tenjin/credentials-production.json"
	h, dir := brokenAndroid(t)
	codex := h.installCodex()
	h.env.store = filedStore{h.store, file}
	code, stdout, _ := h.run("setup", dir, "--agent", "codex")
	if codex.read("args") != "" {
		t.Fatal("Codex was started")
	}
	if code != 9 || !strings.Contains(stdout, "Codex was not started: its sandbox can read files outside this project, and this machine keeps your Tenjin sign-in in a file ("+file+")") ||
		!strings.Contains(stdout, "Make the changes yourself; 'tenjin sdk guide' prints the integration guide.") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}

	// With Claude Code installed too, it is named as the way to go.
	h.installAgent()
	code, stdout, _ = h.run("doctor", dir, "--offline", "--agent", "codex")
	if codex.read("args") != "" || code != 9 || !strings.Contains(stdout, "Use Claude Code instead, with 'tenjin doctor --agent claude'") || strings.Contains(stdout, "Checking again") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
	// Claude Code keeps to the project, so it starts.
	if code, stdout, _ := h.run("setup", dir, "--agent", "claude"); code != 0 {
		t.Errorf("--agent claude: exit = %d\n%s", code, stdout)
	}
}

func TestCodexIsNotStartedWithAKeyOrTokenGiven(t *testing.T) {
	cases := []struct {
		name string
		args []string
		vars map[string]string
	}{
		{"--sdk-key", []string{"--sdk-key", goodKey}, nil},
		{"TENJIN_SDK_KEY_ANDROID", nil, map[string]string{"TENJIN_SDK_KEY_ANDROID": goodKey}},
		{"TENJIN_API_TOKEN", nil, map[string]string{"TENJIN_API_TOKEN": "token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := brokenAndroid(t)
			codex := h.installCodex()
			for k, v := range tc.vars {
				h.vars[k] = v
			}
			_, stdout, stderr := h.run(append([]string{"doctor", dir, "--offline", "--agent", "codex"}, tc.args...)...)
			if codex.read("args") != "" {
				t.Fatal("Codex was started")
			}
			if !strings.Contains(stdout, "Codex was not started: its sandbox can read outside this project, including what this run of tenjin was given") {
				t.Errorf("stdout = %s\nstderr = %s", stdout, stderr)
			}
			if strings.Contains(stdout+stderr, goodKey) {
				t.Error("the whole key was printed")
			}
		})
	}
	// Claude Code keeps to the project, so a given key does not stop it.
	h, dir := brokenAndroid(t)
	h.installAgent()
	if _, stdout, _ := h.run("doctor", dir, "--offline", "--agent", "claude", "--sdk-key", goodKey); strings.Contains(stdout, "was not started") {
		t.Errorf("Claude Code was refused:\n%s", stdout)
	}
}

// An agent that would be refused is not offered, in setup's steps 5 and 6
// or by doctor; why is said once, and another agent is offered alone.
func TestAnAgentThatWouldBeRefusedIsNotOffered(t *testing.T) {
	const file = "/home/me/.config/tenjin/credentials-production.json"
	const notOffered = "Codex is installed, but is not offered: its sandbox can read files outside this project, and this machine keeps your Tenjin sign-in in a file (" + file + ")"
	h, dir := brokenAndroid(t)
	codex := h.installCodex()
	h.env.store = filedStore{h.store, file}

	code, stdout, _ := h.typing("", "", "q").run("setup", dir)
	if codex.read("args") != "" || code != 9 || strings.Count(stdout, notOffered) != 1 || !strings.Contains(stdout, "The full guide:") ||
		strings.Contains(stdout, "using Codex?") || strings.Contains(stdout, "a to have Codex fix them") || strings.Contains(stdout, "uses tokens") {
		t.Errorf("setup: exit = %d\n%s", code, stdout)
	}

	code, stdout, stderr := h.typing("").run("doctor", dir, "--offline")
	if codex.read("args") != "" || code != 9 || strings.Count(stdout, notOffered) != 1 || strings.Contains(stdout, "Fix them with Codex") || strings.Contains(stderr, "(or codex)") {
		t.Errorf("doctor: exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}

	agent := h.installAgent()
	code, stdout, _ = h.typing("n").run("doctor", dir, "--offline")
	if agent.runs() != "" || code != 9 || !strings.Contains(stdout, notOffered) || !strings.Contains(stdout, "Fix them with Claude Code? [y/N]") || strings.Contains(stdout, "Codex (2)") {
		t.Errorf("doctor with Claude Code too: exit = %d\n%s", code, stdout)
	}
	_, stdout, _ = h.typing("", "n", "q").run("setup", dir)
	if !strings.Contains(stdout, notOffered) || !strings.Contains(stdout, "Integrate the Tenjin SDK using Claude Code? [Y/n]") || strings.Contains(stdout, "Codex (2)") {
		t.Errorf("setup with Claude Code too:\n%s", stdout)
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
				"Either uses tokens from your own account and edits files in this project: commit or stash your work first",
				"Integrate the Tenjin SDK using Claude Code (1), Codex (2), or neither (n)? [1]",
				tc.want,
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("output lacks %q:\n%s", want, stdout)
				}
			}
		})
	}

	// The flag skips the question, and its note, and works without a terminal.
	h, dir := brokenAndroid(t)
	h.installAgent()
	h.installCodex()
	code, stdout, _ := h.run("setup", dir, "--agent", "codex")
	if code != 0 || !strings.Contains(stdout, "Codex is working") || strings.Contains(stdout, "installed on this machine") || strings.Contains(stdout, "uses tokens") {
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

// When only a key is left to settle, the agent has nothing to do and is not
// started.
func TestTheAgentIsNotStartedForKeysAlone(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir := testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1)}))
	agent := h.installAgent()

	_, stdout, _ := h.typing("", "", "").run("setup", dir)
	if _, err := os.Stat(filepath.Join(agent.dir, "runs")); err == nil {
		t.Errorf("the agent was started:\n%s", stdout)
	}
	if !strings.Contains(stdout, "What is left is the SDK keys") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// A key passed in that Tenjin refuses is said so, and step 4 asks for the
// right one at once.
func TestARefusedGivenKeyIsAskedForAgain(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir := testproject.Write(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1)}))
	h.fake.keyAnswers["WRONGKEYWRONGKEYWRONGKEY1234"] = "unauthorized"

	_, stdout, _ := h.typing("", "", "q").run("setup", dir, "--sdk-key", "android=WRONGKEYWRONGKEYWRONGKEY1234")
	step4 := stdout[strings.Index(stdout, "Step 4 of 7"):]
	if i := strings.Index(step4, "Step 5 of 7"); i > 0 {
		step4 = step4[:i]
	}
	if !strings.Contains(step4, "Paste the") {
		t.Errorf("step 4 did not ask for the key:\n%s", step4)
	}
}

// doctor offers to fix what it found with the developer's agent, defaults
// to no, and checks again after a yes.
func TestDoctorOffersToFix(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()

	code, stdout, _ := h.typing("").run("doctor", dir, "--offline")
	if code != 9 || agent.runs() != "" || !strings.Contains(stdout, "Fix them with Claude Code? [y/N]") ||
		!strings.Contains(stdout, "it uses tokens from that account and edits files in this project: commit or stash your work first") {
		t.Fatalf("Enter: exit = %d, runs = %q\n%s", code, agent.runs(), stdout)
	}

	code, stdout, stderr := h.typing("y").run("doctor", dir, "--offline")
	if agent.runs() != "1" || !strings.Contains(stdout, "Checking again") || !strings.Contains(stdout, "INTERNET permission is declared") {
		t.Fatalf("yes: exit = %d, runs = %q, stderr = %q\n%s", code, agent.runs(), stderr, stdout)
	}
	if again := stdout[strings.Index(stdout, "Checking again"):]; strings.Contains(again, "INTERNET permission is missing") {
		t.Errorf("the second check still lacks the permission:\n%s", again)
	}
}

// Without a terminal doctor never asks, and says how to get the fixes made.
func TestDoctorWithoutATerminalSaysHowToFix(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	code, stdout, stderr := h.run("doctor", dir, "--offline")
	if code != 9 || agent.runs() != "" || strings.Contains(stdout, "Fix them") || !strings.Contains(stderr, "tenjin setup") {
		t.Errorf("exit = %d, runs = %q, stderr = %q", code, agent.runs(), stderr)
	}
	// --agent claude fixes without asking; --agent none never offers.
	if code, _, stderr := h.run("doctor", dir, "--offline", "--agent", "claude"); agent.runs() != "1" {
		t.Errorf("--agent claude: exit = %d, runs = %q, stderr = %q", code, agent.runs(), stderr)
	}
}

// An agent's run is for a person: with --json, --agent is refused rather
// than ignored, and an agent that does not exist is named as such.
func TestDoctorAgentFlagErrors(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--agent", "claude", "--json"}, "has no JSON output"},
		{[]string{"--agent", "cursor"}, `unknown agent "cursor"`},
	} {
		code, stdout, stderr := h.run(append([]string{"doctor", dir, "--offline"}, tc.args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || agent.runs() != "" {
			t.Errorf("%v: exit = %d, runs = %q, stdout = %q, stderr = %q", tc.args, code, agent.runs(), stdout, stderr)
		}
	}
	// --agent none offers nothing, so it goes with JSON.
	if code, stdout, _ := h.run("doctor", dir, "--offline", "--agent", "none", "--json"); code != 9 || !strings.HasPrefix(stdout, "{") {
		t.Errorf("--agent none --json: exit = %d, stdout = %q", code, stdout)
	}
}

// The names of the files an agent changed reach the terminal cleaned.
func TestTheChangedFilesAreCleanedForPeople(t *testing.T) {
	h, dir := brokenAndroid(t)
	h.installAgent()
	t.Setenv("FAKE_AGENT_NEW_FILE", "notes\x1b]52;c;cHduZWQ=\x07.txt")
	_, stdout, stderr := h.run("setup", dir, "--agent", "claude")
	if !strings.Contains(stdout, "notes]52;c;cHduZWQ=.txt") || strings.ContainsAny(stdout+stderr, "\x1b\x07") {
		t.Errorf("stdout:\n%q", stdout)
	}
}

// A key Tenjin accepts is already in the project: doctor's agent is told to
// leave it, and the integration that worked still works afterwards.
func TestDoctorsAgentLeavesTheKeyInTheProject(t *testing.T) {
	h, dir := brokenAndroid(t)
	agent := h.installAgent()
	code, stdout, stderr := h.run("doctor", dir, "--agent", "claude")
	prompt := agent.read("prompt")
	if !strings.Contains(prompt, "SDK key: already in the project: leave it as it is") || strings.Contains(prompt, "__TENJIN_ANDROID_SDK_KEY__") {
		t.Errorf("the task:\n%s", prompt)
	}
	again := stdout[strings.Index(stdout, "Checking again"):]
	if code != 0 || !strings.Contains(again, "Tenjin accepts the SDK key …MNBV") {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
}

// Over the placeholder its agent left, doctor writes only a given key that
// Tenjin accepts for that build, and none offline.
func TestDoctorWritesOnlyACheckedKeyOverThePlaceholder(t *testing.T) {
	source := func(dir string) string {
		raw, _ := os.ReadFile(filepath.Join(dir, androidActivity))
		return string(raw)
	}
	cases := []struct {
		name    string
		args    []string
		written bool
		want    string
	}{
		{"accepted", []string{"--sdk-key", goodKey}, true, "✓ Wrote the Android SDK key at " + androidActivity},
		{"refused", []string{"--sdk-key", wrongKey}, false, "✗ Android, key …WRNG from --sdk-key: Tenjin does not accept this key"},
		{"offline", []string{"--sdk-key", goodKey, "--offline"}, false, "with --offline it cannot be checked with Tenjin first"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := placeholderAndroid(t)
			h.fake.keyAnswers[wrongKey] = "unauthorized"
			h.installAgent()
			t.Setenv("FAKE_AGENT_PLACEHOLDER", "1")
			_, stdout, _ := h.run(append([]string{"doctor", dir, "--agent", "claude"}, tc.args...)...)
			lacks(t, stdout, tc.want)
			if written := strings.Contains(source(dir), goodKey); written != tc.written {
				t.Errorf("written = %v:\n%s", written, source(dir))
			}
			if strings.Contains(source(dir), wrongKey) || (!tc.written && !strings.Contains(source(dir), "__TENJIN_ANDROID_SDK_KEY__")) {
				t.Errorf("the placeholder is gone:\n%s", source(dir))
			}
		})
	}
}

// A key given without a build is not written into a platform with several:
// each is its own app, with its own key.
func TestDoctorWritesAKeyPerStoreOnlyWhenGivenPerStore(t *testing.T) {
	base := placeholderStores()
	const manifest = "app/src/main/AndroidManifest.xml"
	files := base.With(testproject.Files{manifest: strings.Replace(base[manifest], `    <uses-permission android:name="android.permission.INTERNET" />`+"\n", "", 1)})

	h, dir := multiStore(t, files)
	h.installAgent()
	_, stdout, _ := h.run("doctor", dir, "--agent", "claude", "--sdk-key", testproject.PlayKey)
	lacks(t, stdout, "Android, amazon (Amazon Appstore): the key from --sdk-key was not written")
	if raw, _ := os.ReadFile(filepath.Join(dir, flavorBuild)); strings.Contains(string(raw), testproject.PlayKey) {
		t.Errorf("a key given for no build was written:\n%s", raw)
	}

	h, dir = multiStore(t, files)
	h.installAgent()
	h.run("doctor", dir, "--agent", "claude", "--sdk-key", "android="+testproject.PlayKey, "--sdk-key", "android:amazon="+testproject.AmazonKey)
	if raw, _ := os.ReadFile(filepath.Join(dir, flavorBuild)); !strings.Contains(string(raw), testproject.PlayKey) || !strings.Contains(string(raw), testproject.AmazonKey) {
		t.Errorf("each store's key was not written:\n%s", raw)
	}
}
