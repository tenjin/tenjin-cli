package agent

import (
	"errors"
	"os"
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
	got := withPath([]string{"HOME=/h", "PATH=/usr/bin"}, "/opt/tenjin")
	if got[1] != "PATH=/opt/tenjin"+sep+"/usr/bin" || got[0] != "HOME=/h" {
		t.Errorf("env = %q", got)
	}
	if got := withPath([]string{"HOME=/h"}, "/opt/tenjin"); got[1] != "PATH=/opt/tenjin" {
		t.Errorf("env = %q", got)
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

func TestFindBothAgentsInTheOrderTheyAreOffered(t *testing.T) {
	found := Find(func(name string) (string, error) { return "/opt/bin/" + name, nil })
	if len(found) != 2 || found[0].ID != "claude" || found[1].ID != "codex" {
		t.Fatalf("found = %+v", found)
	}
	if found[0].ResumeCommand("abc") != "claude --resume abc" || found[1].ResumeCommand("abc") != "codex resume abc" {
		t.Error("resume commands")
	}
	// Codex has no network in its sandbox, so it is given the offline check.
	if found[0].DoctorCommand() != "tenjin doctor --json ." || found[1].DoctorCommand() != "tenjin doctor --offline --json ." {
		t.Error("doctor commands")
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
	got := withoutKeys([]string{"HOME=/h", "TENJIN_SDK_KEY=abc", "TENJIN_SDK_KEY_ANDROID=def", "TENJIN_SDK_KEY_IOS=ghi", "TENJIN_SDK_KEY_ANDROID_AMAZON=jkl", "TENJIN_API_TOKEN=tok", "TENJIN_API_URL=http://x"})
	if strings.Join(got, " ") != "HOME=/h TENJIN_API_URL=http://x" {
		t.Errorf("env = %q", got)
	}
}
