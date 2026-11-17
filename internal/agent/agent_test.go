package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These lines have the shape Claude Code 2.1 emits with
// --output-format stream-json.
const stream = `{"type":"system","subtype":"init","cwd":"/work/app"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the build file."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/work/app/app/build.gradle.kts"}}]}}
{"type":"rate_limit_event","rate_limit_info":{}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"plugins { }"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"TenjinSDK"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/work/app/app/build.gradle.kts"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"tenjin doctor --json .\necho done"}}]}}
not json at all
{"type":"result","subtype":"success","is_error":false,"result":" Added two dependencies. \n","permission_denials":[{"tool_name":"Bash"}]}
`

func TestReadEvents(t *testing.T) {
	var actions []string
	result, finished, err := readEvents(strings.NewReader(stream), "/work/app", func(a Action) {
		actions = append(actions, a.Verb+" "+a.Detail)
	})
	if err != nil || !finished {
		t.Fatalf("finished = %v, err = %v", finished, err)
	}
	// Paths are shown relative to the project; searches are left out; a
	// multi-line command shows its first line.
	want := "read app/build.gradle.kts|edit app/build.gradle.kts|run tenjin doctor --json ."
	if got := strings.Join(actions, "|"); got != want {
		t.Errorf("actions = %q, want %q", got, want)
	}
	if result.Summary != "Added two dependencies." || result.Denied != 1 {
		t.Errorf("result = %+v", result)
	}
}

func TestReadEventsReportsAnAgentError(t *testing.T) {
	_, finished, err := readEvents(strings.NewReader(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"usage limit reached"}`), "/work", nil)
	if !finished || err == nil || !strings.Contains(err.Error(), "usage limit reached") {
		t.Errorf("finished = %v, err = %v", finished, err)
	}
}

func TestReadEventsWithoutAResultIsUnfinished(t *testing.T) {
	_, finished, err := readEvents(strings.NewReader(`{"type":"system","subtype":"init"}`+"\n"), "/work", nil)
	if finished || err != nil {
		t.Errorf("finished = %v, err = %v", finished, err)
	}
}

func TestFind(t *testing.T) {
	found := Find(func(name string) (string, error) {
		if name == "claude" {
			return "/opt/bin/claude", nil
		}
		return "", errors.New("not found")
	})
	if len(found) != 1 || found[0].Name != "Claude Code" || found[0].Path != "/opt/bin/claude" {
		t.Errorf("found = %+v", found)
	}
	if none := Find(func(string) (string, error) { return "", errors.New("not found") }); len(none) != 0 {
		t.Errorf("found = %+v", none)
	}
}

func TestInsideAgent(t *testing.T) {
	if !InsideAgent(func(name string) string { return map[string]string{"CLAUDECODE": "1"}[name] }) {
		t.Error("CLAUDECODE was not recognised")
	}
	if InsideAgent(func(string) string { return "" }) {
		t.Error("reported an agent in a plain shell")
	}
}

func TestWithPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := withPath([]string{"HOME=/h", "PATH=/usr/bin"}, "/opt/tenjin", false)
	if got[1] != "PATH=/opt/tenjin"+sep+"/usr/bin" || got[0] != "HOME=/h" {
		t.Errorf("env = %q", got)
	}
	if got := withPath([]string{"HOME=/h"}, "/opt/tenjin", false); got[1] != "PATH=/opt/tenjin" {
		t.Errorf("env = %q", got)
	}
	// Where names are case-sensitive, Path is another variable.
	if got := withPath([]string{"Path=/usr/bin"}, "/opt/tenjin", false); strings.Join(got, " ") != "Path=/usr/bin PATH=/opt/tenjin" {
		t.Errorf("env = %q", got)
	}
}

// Windows spells it Path, and a second entry named PATH would replace it, so
// the agent would find nothing but the Tenjin CLI.
func TestWithPathKeepsTheWindowsPath(t *testing.T) {
	env := []string{`=C:=C:\work`, `Path=C:\Windows;C:\Program Files\nodejs`, `USERPROFILE=C:\Users\me`}
	got := withPath(env, `C:\tenjin`, true)
	want := []string{`=C:=C:\work`, `Path=C:\tenjin` + string(os.PathListSeparator) + `C:\Windows;C:\Program Files\nodejs`, `USERPROFILE=C:\Users\me`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("env = %q, want %q", got, want)
	}
}

// These lines have the shape `codex exec --json` 0.160 emits.
const codexStream = `{"type":"thread.started","thread_id":"01a0fdba-284c-7ed2-88aa-48efee485cf3"}
{"type":"item.completed","item":{"id":"item_0","type":"error","message":"Model metadata not found."}}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"I will look at the build file."}}
{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"/bin/zsh -lc 'sed -n 1,40p app/build.gradle.kts'","status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_2","type":"command_execution","command":"/bin/zsh -lc 'sed -n 1,40p app/build.gradle.kts'","exit_code":0,"status":"completed"}}
{"type":"item.started","item":{"id":"item_3","type":"file_change","changes":[{"path":"/work/app/app/build.gradle.kts","kind":"update"},{"path":"/work/app/app/proguard-rules.pro","kind":"add"}],"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_3","type":"file_change","changes":[{"path":"/work/app/app/build.gradle.kts","kind":"update"}],"status":"completed"}}
{"type":"item.started","item":{"id":"item_4","type":"command_execution","command":"/bin/zsh -lc \"tenjin doctor --offline --json .\"","status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_5","type":"agent_message","text":" Added two dependencies. \n"}}
{"type":"turn.completed","usage":{"input_tokens":1}}
`

func TestReadCodexEvents(t *testing.T) {
	var actions []string
	result, finished, err := readCodexEvents(strings.NewReader(codexStream), "/work/app", func(a Action) {
		actions = append(actions, a.Verb+" "+a.Detail)
	})
	if err != nil || !finished {
		t.Fatalf("finished = %v, err = %v", finished, err)
	}
	// Each change is shown once, when it starts; reading a file is left out;
	// the shell Codex wraps every command in is not shown.
	want := "edit app/build.gradle.kts|edit app/proguard-rules.pro|run tenjin doctor --offline --json ."
	if got := strings.Join(actions, "|"); got != want {
		t.Errorf("actions = %q, want %q", got, want)
	}
	// The report is the last message, and the session is the one Codex named.
	if result.Summary != "Added two dependencies." || result.SessionID != "01a0fdba-284c-7ed2-88aa-48efee485cf3" {
		t.Errorf("result = %+v", result)
	}
}

func TestReadCodexEventsReportsAFailure(t *testing.T) {
	stream := `{"type":"thread.started","thread_id":"t1"}
{"type":"error","message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The model is not supported.\"}}"}
{"type":"turn.failed","error":{"message":"same again"}}
`
	_, finished, err := readCodexEvents(strings.NewReader(stream), "/work", nil)
	if !finished || err == nil || err.Error() != "The model is not supported." {
		t.Errorf("finished = %v, err = %v", finished, err)
	}
	// A failure that is plain text is passed on as it is.
	_, _, err = readCodexEvents(strings.NewReader(`{"type":"turn.failed","error":{"message":"usage limit reached"}}`), "/work", nil)
	if err == nil || err.Error() != "usage limit reached" {
		t.Errorf("err = %v", err)
	}
}

// Codex says it reconnects before it fails: the reason is what comes after,
// whether the turn then fails or the output just ends.
func TestReadCodexEventsReportsWhyNotThatItReconnected(t *testing.T) {
	errs := `{"type":"error","message":"Reconnecting... 1/5"}
{"type":"error","message":"Reconnecting... 5/5"}
{"type":"error","message":"You've hit your usage limit."}
`
	for _, stream := range []string{errs + `{"type":"turn.failed","error":{"message":"You've hit your usage limit."}}` + "\n", errs} {
		if _, _, err := readCodexEvents(strings.NewReader(stream), "/work", nil); err == nil || err.Error() != "You've hit your usage limit." {
			t.Errorf("err = %v", err)
		}
	}
	// With only reconnects before it, the turn's own message is the reason.
	stream := `{"type":"error","message":"Reconnecting... 5/5"}
{"type":"turn.failed","error":{"message":"stream disconnected"}}
`
	if _, _, err := readCodexEvents(strings.NewReader(stream), "/work", nil); err == nil || err.Error() != "stream disconnected" {
		t.Errorf("err = %v", err)
	}
}

func TestFindBothAgentsInTheOrderTheyAreOffered(t *testing.T) {
	found := Find(func(name string) (string, error) { return "/opt/bin/" + name, nil })
	if len(found) != 2 || found[0].ID != "claude" || found[1].ID != "codex" {
		t.Fatalf("found = %+v", found)
	}
	if found[0].ResumeCommand("abc") != "claude --resume abc" || found[1].ResumeCommand("abc") != "codex resume abc" {
		t.Error("resume commands")
	}
	// Codex has no network in its sandbox, so it is given the offline check;
	// Claude Code is given it when the run is offline.
	if found[0].DoctorCommand(false) != "tenjin doctor --json ." || found[1].DoctorCommand(false) != "tenjin doctor --offline --json ." {
		t.Error("doctor commands")
	}
	if found[0].DoctorCommand(true) != "tenjin doctor --offline --json ." || found[1].DoctorCommand(true) != "tenjin doctor --offline --json ." {
		t.Error("offline doctor commands")
	}
	if got := strings.Join(IDs(), ","); got != "claude,codex" {
		t.Errorf("IDs = %q", got)
	}
}

func TestInsideCodex(t *testing.T) {
	if !InsideAgent(func(name string) string { return map[string]string{"CODEX_THREAD_ID": "t1"}[name] }) {
		t.Error("a Codex session was not recognised")
	}
}

func TestTheAgentInheritsNoSDKKey(t *testing.T) {
	env := []string{"HOME=/h", "TENJIN_SDK_KEY=abc", "TENJIN_SDK_KEY_ANDROID=def", "TENJIN_SDK_KEY_IOS=ghi", "TENJIN_SDK_KEY_ANDROID_AMAZON=jkl", "TENJIN_API_TOKEN=tok", "TENJIN_API_TOKENS=x", "TENJIN_API_URL=http://x"}
	for _, fold := range []bool{false, true} {
		if got := withoutKeys(env, fold); strings.Join(got, " ") != "HOME=/h TENJIN_API_TOKENS=x TENJIN_API_URL=http://x" {
			t.Errorf("fold %v: env = %q", fold, got)
		}
	}
}

// On Windows the CLI reads tenjin_api_token as TENJIN_API_TOKEN, so the
// agent must not inherit it under that spelling either.
func TestTheAgentInheritsNoKeyInAnotherCaseOnWindows(t *testing.T) {
	env := []string{"Path=C:\\bin", "tenjin_api_token=tok", "Tenjin_Sdk_Key_Android=def"}
	if got := withoutKeys(env, true); strings.Join(got, " ") != "Path=C:\\bin" {
		t.Errorf("env = %q", got)
	}
	// Elsewhere those are other variables, which the CLI does not read.
	if got := withoutKeys(env, false); len(got) != 3 {
		t.Errorf("env = %q", got)
	}
}

// A Claude Code too old to be confined is not started at all.
func TestAClaudeCodeWithoutRestrictedModeIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\nif [ \"$1\" = --help ]; then echo '  --print'; exit 0; fi\ntouch " + ran + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Find(func(string) (string, error) { return script, nil })[0]
	_, err := a.Run(context.Background(), Task{Dir: dir, Prompt: "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "claude update") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(ran); statErr == nil {
		t.Error("the agent was started")
	}
}

func TestTheHelpCheckReadsTheErrorOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\nif [ \"$1\" = --help ]; then echo '  --restricted' >&2; exit 0; fi\ncat > /dev/null\necho '{\"type\":\"result\",\"result\":\"Done.\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Find(func(string) (string, error) { return script, nil })[0]
	if result, err := a.Run(context.Background(), Task{Dir: dir, Prompt: "x"}, nil); err != nil || result.Summary != "Done." {
		t.Errorf("result = %+v, err = %v", result, err)
	}
}

// Claude Code's help is read as the run itself is started: in the project,
// without the Tenjin secrets in its environment.
func TestTheHelpCheckGetsNoKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	t.Setenv("TENJIN_API_TOKEN", "tok")
	t.Setenv("TENJIN_SDK_KEY", "KEY")
	dir, project := t.TempDir(), t.TempDir()
	script := filepath.Join(dir, "claude")
	help := filepath.Join(dir, "help-env")
	body := "#!/bin/sh\nif [ \"$1\" = --help ]; then { pwd -P; env; } > " + help + "; echo '  --restricted'; exit 0; fi\ncat > /dev/null\necho '{\"type\":\"result\",\"result\":\"Done.\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Find(func(string) (string, error) { return script, nil })[0]
	if _, err := a.Run(context.Background(), Task{Dir: project, Prompt: "x", ToolDir: "/opt/tenjin"}, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(help)
	if err != nil {
		t.Fatal(err)
	}
	// The environment is not printed: it is the test's own.
	got := string(raw)
	if strings.Contains(got, "TENJIN_API_TOKEN") || strings.Contains(got, "TENJIN_SDK_KEY") {
		t.Error("the help check got a Tenjin secret")
	}
	if !strings.Contains(got, "\nPATH=/opt/tenjin"+string(os.PathListSeparator)) {
		t.Error("the help check did not get the agent's PATH")
	}
	if real, _ := filepath.EvalSymlinks(project); !strings.HasPrefix(got, real+"\n") {
		t.Errorf("the help check ran in %q, not in the project", strings.SplitN(got, "\n", 2)[0])
	}
}

// tail is output that follows an agent's error, long enough to fill a pipe
// that nobody reads.
var tail = strings.Repeat(`{"type":"item.completed","item":{"type":"reasoning","text":"`+strings.Repeat("x", 1000)+`"}}`+"\n", 3000)

func TestReadCodexEventsReadsOnAfterAnError(t *testing.T) {
	stream := strings.NewReader(`{"type":"thread.started","thread_id":"t1"}
{"type":"error","message":"Reconnecting... 1/5"}
` + tail + `{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}
{"type":"turn.completed","usage":{"input_tokens":1}}
`)
	result, finished, err := readCodexEvents(stream, "/work", nil)
	if err != nil || !finished || result.Summary != "Done." || stream.Len() != 0 {
		t.Errorf("finished = %v, err = %v, result = %+v, %d bytes unread", finished, err, result, stream.Len())
	}
	// An error the turn never recovers from is the failure.
	stream = strings.NewReader(`{"type":"error","message":"stream disconnected"}` + "\n" + tail)
	_, finished, err = readCodexEvents(stream, "/work", nil)
	if !finished || err == nil || err.Error() != "stream disconnected" || stream.Len() != 0 {
		t.Errorf("finished = %v, err = %v, %d bytes unread", finished, err, stream.Len())
	}
}

// Codex reads a dot in a -c key as a step into a table, quoted or not, so a
// server's name goes in the value, as a TOML key.
func TestTomlKey(t *testing.T) {
	for name, want := range map[string]string{
		"github":    `github`,
		"my-docs_2": `my-docs_2`,
		"my.docs":   `"my.docs"`,
		`a "b" \ c`: `"a \"b\" \\ c"`,
		"tab\there": `"tab\u0009here"`,
		"café":      `"café"`,
	} {
		if got := tomlKey(name); got != want {
			t.Errorf("tomlKey(%q) = %s, want %s", name, got, want)
		}
	}
}

// `codex features list` prints a feature per line, in columns padded to
// the longest name and stage (Codex 0.166.0's main.rs), with warnings, if
// any, on standard error.
func TestParseCodexFeatures(t *testing.T) {
	out := "apps                         stable             false\n" +
		"code_mode                    under development  true\n" +
		"worktrees                    removed            true\n" +
		"\n"
	features, ok := parseCodexFeatures([]byte(out))
	want := map[string]codexFeature{
		"apps":      {stage: "stable"},
		"code_mode": {stage: "under development", enabled: true},
		"worktrees": {stage: "removed", enabled: true},
	}
	if !ok || len(features) != len(want) {
		t.Fatalf("features = %+v, ok = %v", features, ok)
	}
	for name, f := range want {
		if features[name] != f {
			t.Errorf("%s = %+v, want %+v", name, features[name], f)
		}
	}
	if _, ok := parseCodexFeatures([]byte("error: no such command\n")); ok {
		t.Error("an answer with no feature was read as a list")
	}
}

// Every feature turned off is named once, in Codex's spelling.
func TestCodexFeaturesOffAreKeys(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range codexFeaturesOff {
		if seen[f] || !bareKey.MatchString(f) {
			t.Errorf("%q is repeated or not a bare TOML key", f)
		}
		seen[f] = true
	}
}

func TestReadEventsReadsOnAfterAnError(t *testing.T) {
	stream := strings.NewReader(`{"type":"result","is_error":true,"result":"usage limit reached"}` + "\n" + tail)
	_, finished, err := readEvents(stream, "/work", nil)
	if !finished || err == nil || !strings.Contains(err.Error(), "usage limit reached") || stream.Len() != 0 {
		t.Errorf("finished = %v, err = %v, %d bytes unread", finished, err, stream.Len())
	}
}
