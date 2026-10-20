// Package agent hands a task to a coding agent already installed on the
// developer's machine. The agent runs under the developer's own account and
// settings; this package only starts it, watches what it does, and reports.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/proc"
)

// Agent is an installed coding agent the CLI knows how to drive.
type Agent struct {
	ID   string
	Name string
	Path string
	driver
}

// driver is what differs from one agent to the next: how it is started, how
// it is kept to the task, and how it reports what it does.
type driver struct {
	id, name, binary string
	// limits says, to the person deciding, what the agent can and cannot do.
	limits string
	// readsOutside is true when the agent can read files outside the
	// project, such as the CLI's own session file.
	readsOutside bool
	// doctor is the check the agent is told to run on its own work.
	doctor string
	// ownSession is true when this package picks the conversation's ID and
	// passes it in; otherwise the agent reports the ID it chose.
	ownSession bool
	args       func(task Task, sessionID string) []string
	read       func(r io.Reader, dir string, progress func(Action)) (Result, bool, error)
	resume     func(sessionID string) string
	// inside names environment variables the agent sets for what it runs.
	inside []string
	// confinable checks, before a run, that this installed version can be
	// held to the limits above. Nil when nothing needs checking.
	confinable func(ctx context.Context, path string) error
}

// drivers lists the agents this package can run headless, in the order they
// are offered.
var drivers = []driver{claude, codex}

// IDs returns the names accepted for choosing an agent.
func IDs() []string {
	ids := make([]string, 0, len(drivers))
	for _, d := range drivers {
		ids = append(ids, d.id)
	}
	return ids
}

// Find returns the supported agents installed on this machine.
func Find(lookPath func(string) (string, error)) []Agent {
	var found []Agent
	for _, d := range drivers {
		if path, err := lookPath(d.binary); err == nil {
			found = append(found, Agent{ID: d.id, Name: d.name, Path: path, driver: d})
		}
	}
	return found
}

// InsideAgent reports whether this process was itself started by a coding
// agent. Starting a second one from inside the first helps nobody: the agent
// already at work should make the changes.
func InsideAgent(getenv func(string) string) bool {
	for _, d := range drivers {
		for _, name := range d.inside {
			if getenv(name) != "" {
				return true
			}
		}
	}
	return false
}

// Limits says what the agent is and is not able to do while it works.
func (a Agent) Limits() string { return a.limits }

// ReadsOutsideProject reports whether the agent can read files outside the
// project while it works.
func (a Agent) ReadsOutsideProject() bool { return a.readsOutside }

// DoctorCommand is the check the agent runs on its own work.
func (a Agent) DoctorCommand() string { return a.doctor }

// Task is a piece of work for an agent.
type Task struct {
	// Dir is the project directory. The agent may read and edit files in it.
	Dir string
	// Prompt says what to do. It is sent on standard input, never as an
	// argument, so it does not show up in the process list.
	Prompt string
	// ToolDir is put on the agent's PATH so it can run `tenjin doctor` and
	// `tenjin sdk guide`, the commands it is allowed to run.
	ToolDir string
}

// Action is one thing the agent did, for showing progress.
type Action struct {
	// Verb is "read", "edit" or "run".
	Verb   string
	Detail string
}

// Result is what the agent reported when it finished.
type Result struct {
	Summary string
	// Denied counts the actions the agent attempted and was not allowed.
	Denied int
	// SessionID names the conversation, which the agent keeps so that the
	// developer can pick it up where it ended.
	SessionID string
}

// ResumeCommand is what the developer runs, in the project directory, to
// continue the conversation themselves.
func (a Agent) ResumeCommand(sessionID string) string {
	return a.resume(sessionID)
}

// sessionName is how the conversation is listed in the agent's own history.
const sessionName = "Tenjin SDK integration"

// allowedTools lets Claude Code read and edit files, run the doctor and read
// the SDK guide, and nothing else: no other shell command, no network.
const (
	tools        = "Read,Edit,Write,Glob,Grep,Bash"
	allowedTools = "Read,Edit,Write,Glob,Grep,Bash(tenjin doctor *),Bash(tenjin sdk guide *)"
)

var claude = driver{
	id: "claude", name: "Claude Code", binary: "claude",
	limits:     "it may edit files and run 'tenjin doctor' and 'tenjin sdk guide', and nothing else; it commits nothing",
	doctor:     "tenjin doctor --json .",
	ownSession: true,
	args: func(_ Task, sessionID string) []string {
		return []string{
			"--print",
			"--output-format", "stream-json", "--verbose",
			// Restricted mode is what makes the limits above hold: it ignores
			// the user's and the project's settings files, so a project
			// cannot bring hooks or permissions of its own, and it keeps the
			// file tools inside the project.
			"--restricted",
			"--permission-mode", "acceptEdits",
			"--tools", tools,
			"--allowedTools", allowedTools,
			// The developer's connectors have no part in this task; leaving
			// them out keeps the run quick and its report on topic.
			"--strict-mcp-config",
			// The conversation is kept, under an ID chosen here, so the
			// developer can continue it. It is stored by the agent, on their
			// machine.
			"--session-id", sessionID,
			"--name", sessionName,
		}
	},
	read:   readEvents,
	resume: func(sessionID string) string { return "claude --resume " + sessionID },
	inside: []string{"CLAUDECODE"},
	confinable: func(ctx context.Context, path string) error {
		help, _ := exec.CommandContext(ctx, path, "--help").CombinedOutput()
		if !strings.Contains(string(help), "--restricted") {
			return errors.New("this version of Claude Code cannot be held to the task (it has no --restricted mode). Update it with 'claude update', then run this again")
		}
		return nil
	},
}

// Run starts the agent on the task and blocks until it finishes. progress is
// called for each action the agent takes.
func (a Agent) Run(ctx context.Context, task Task, progress func(Action)) (Result, error) {
	sessionID := ""
	if a.ownSession {
		id, err := newSessionID()
		if err != nil {
			return Result{}, err
		}
		sessionID = id
	}
	if a.confinable != nil {
		if err := a.confinable(ctx, a.Path); err != nil {
			return Result{}, err
		}
	}
	// Its own process group, so stopping it stops the commands it started.
	cmd := proc.Command(ctx, a.Path, a.args(task, sessionID)...)
	cmd.Dir = task.Dir
	cmd.Stdin = strings.NewReader(task.Prompt)
	cmd.Env = withPath(withoutKeys(os.Environ(), foldEnv), task.ToolDir, foldEnv)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("could not start %s: %w", a.Name, err)
	}

	result, finished, parseErr := a.read(stdout, task.Dir, progress)
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return result, fmt.Errorf("%s was interrupted", a.Name)
	case parseErr != nil:
		return result, parseErr
	case !finished:
		detail := lastLine(stderr.String())
		if detail == "" && waitErr != nil {
			detail = waitErr.Error()
		}
		return result, fmt.Errorf("%s stopped without finishing: %s", a.Name, detail)
	}
	if a.ownSession {
		result.SessionID = sessionID
	}
	return result, nil
}

// newSessionID returns a random version 4 UUID, the form the agent expects.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// event is the part of Claude Code's streamed output this package reads.
type event struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Denials []any  `json:"permission_denials"`
	Message struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				FilePath string `json:"file_path"`
				Command  string `json:"command"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// readEvents follows Claude Code's output, one JSON object per line. finished
// is true once the agent has sent its closing result.
func readEvents(r io.Reader, dir string, progress func(Action)) (result Result, finished bool, err error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20) // a line can carry a whole file
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue // not an event; the agent's own diagnostics
		}
		switch e.Type {
		case "assistant":
			for _, block := range e.Message.Content {
				if block.Type != "tool_use" {
					continue
				}
				if action, ok := describe(block.Name, block.Input.FilePath, block.Input.Command, dir); ok && progress != nil {
					progress(action)
				}
			}
		case "result":
			result = Result{Summary: strings.TrimSpace(e.Result), Denied: len(e.Denials)}
			if e.IsError {
				return result, true, errors.New(firstNonEmpty(result.Summary, "the agent reported an error"))
			}
			finished = true
		}
	}
	return result, finished, scanner.Err()
}

// describe turns a tool call into a line of progress. Searches are left out:
// they are frequent and say little.
func describe(tool, file, command, dir string) (Action, bool) {
	switch tool {
	case "Read":
		return Action{Verb: "read", Detail: relative(file, dir)}, file != ""
	case "Edit", "Write":
		return Action{Verb: "edit", Detail: relative(file, dir)}, file != ""
	case "Bash":
		return Action{Verb: "run", Detail: firstLine(command)}, command != ""
	}
	return Action{}, false
}

func relative(file, dir string) string {
	// The agent reports absolute paths, and may resolve symlinks in them.
	for _, base := range []string{dir, resolved(dir)} {
		if rel, err := filepath.Rel(base, file); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return file
}

func resolved(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// foldEnv is true where the names of environment variables ignore case, as
// on Windows: there PATH is usually spelled Path, and tenjin_api_token is
// TENJIN_API_TOKEN.
var foldEnv = runtime.GOOS == "windows"

// envName returns the name of a NAME=value entry. Windows has entries such
// as "=C:=C:\work", whose name starts with the sign.
func envName(kv string) string {
	if kv == "" {
		return ""
	}
	if i := strings.IndexByte(kv[1:], '='); i >= 0 {
		return kv[:i+1]
	}
	return kv
}

// withoutKeys drops the variables that carry Tenjin secrets: the SDK keys
// (TENJIN_SDK_KEY and its per-build forms) and TENJIN_API_TOKEN. The agent
// writes a placeholder where a key belongs and is never given the key, and
// the checks it runs need no sign-in, so it must find neither in its
// environment.
func withoutKeys(env []string, fold bool) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := envName(kv)
		if fold {
			name = strings.ToUpper(name)
		}
		if !strings.HasPrefix(name, "TENJIN_SDK_KEY") && name != "TENJIN_API_TOKEN" {
			out = append(out, kv)
		}
	}
	return out
}

// withPath returns env with dir placed first on PATH, keeping the name the
// entry already has.
func withPath(env []string, dir string, fold bool) []string {
	if dir == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		name := envName(kv)
		if name == "PATH" || fold && strings.EqualFold(name, "PATH") {
			kv = name + "=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(kv[len(name):], "=")
			found = true
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 100 {
		line = line[:100] + "…"
	}
	return line
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
