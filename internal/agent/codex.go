package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/proc"
)

// Codex has no list of allowed commands. It is kept to the task by its own
// sandbox instead: files outside the project cannot be written, and there is
// no network. So it checks its work with the doctor's offline form; the key
// check against Tenjin is made afterwards, outside the sandbox. The sandbox
// still reads the whole disk, which is why Codex is not started where the
// Tenjin sign-in is kept in a file (see the CLI's runAgent).
var codex = driver{
	id: "codex", name: "Codex", binary: "codex",
	limits:       "it runs in Codex's sandbox: it can change files in this project only and has no network access, but it can read files elsewhere on this machine; it is told not to commit",
	readsOutside: true,
	doctor:       offlineDoctor,
	args: func(task Task, _ string, confinement []string) []string {
		args := []string{
			"exec", "--json",
			"--sandbox", "workspace-write",
			// Pinned here, so that the developer's own Codex configuration
			// cannot open the sandbox to the network or to other folders.
			// Without the last two, /tmp and $TMPDIR stay writable.
			"-c", "sandbox_workspace_write.network_access=false",
			"-c", "sandbox_workspace_write.writable_roots=[]",
			"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
			"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
			// A project need not be a Git repository to be integrated.
			"--skip-git-repo-check",
		}
		// The developer's MCP servers turned off; the rest of their Codex
		// configuration, such as the model, is kept.
		args = append(args, confinement...)
		// The task is read from standard input.
		return append(args, "-C", task.Dir, "-")
	},
	confine: confineCodex,
	read:    readCodexEvents,
	resume:  func(sessionID string) string { return "codex resume " + sessionID },
	inside:  []string{"CODEX_THREAD_ID", "CODEX_SANDBOX"},
}

// confineCodex turns off what would give Codex tools outside its sandbox:
// the developer's MCP servers, and the connectors and plugins that bring
// servers of their own. An override of mcp_servers is merged with the
// configuration, not put in its place, so each server Codex lists, here
// where it will run, is turned off by name; then Codex is asked again, and
// is not started if any is still on.
func confineCodex(ctx context.Context, path, dir string, env []string) ([]string, error) {
	args := []string{"-c", "features.apps=false", "-c", "features.plugins=false"}
	servers, err := codexServers(ctx, path, dir, env, args)
	if err != nil {
		return nil, unconfined(err)
	}
	if len(servers) == 0 {
		return args, nil
	}
	off := make([]string, len(servers))
	for i, s := range servers {
		off[i] = tomlKey(s.Name) + "={enabled=false}"
	}
	args = append(args, "-c", "mcp_servers={"+strings.Join(off, ",")+"}")
	if servers, err = codexServers(ctx, path, dir, env, args); err != nil {
		return nil, unconfined(err)
	}
	for _, s := range servers {
		if s.Enabled == nil || *s.Enabled {
			return nil, unconfined(fmt.Errorf("%q is still on", s.Name))
		}
	}
	return args, nil
}

func unconfined(reason error) error {
	return fmt.Errorf("this version of Codex cannot be held to the task: its MCP servers could not be turned off (%w). Update Codex, or turn them off in its configuration, then run this again", reason)
}

type codexServer struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
}

// codexServers returns the MCP servers Codex would start with the given
// overrides. What went wrong is not passed on: Codex's errors can quote the
// configuration, which may hold tokens.
func codexServers(ctx context.Context, path, dir string, env, overrides []string) ([]codexServer, error) {
	cmd := proc.Command(ctx, path, slices.Concat(overrides, []string{"mcp", "list", "--json"})...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("'codex mcp list --json' failed")
	}
	var servers []codexServer
	if json.Unmarshal(out, &servers) != nil {
		return nil, errors.New("'codex mcp list --json' answered in a form this CLI does not know")
	}
	return servers, nil
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlKey writes a server's name as a TOML key, quoted unless it is bare.
func tomlKey(name string) string {
	if bareKey.MatchString(name) {
		return name
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range name {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// codexEvent is the part of `codex exec --json` output this package reads.
type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Error    struct {
		Message string `json:"message"`
	} `json:"error"`
	Item struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Command string `json:"command"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	} `json:"item"`
}

// Codex runs every command through a login shell; the wrapper says nothing.
var shellWrapper = regexp.MustCompile(`^\S*/(?:zsh|bash|sh) -lc (.*)$`)

// lookingAround matches commands that only read: Codex has no read tool, so
// it inspects files with these, many times over. They are left out of the
// progress, as searches are for Claude Code.
var lookingAround = regexp.MustCompile(`^(?:cat|sed -n|nl|head|tail|ls|find|rg|grep|wc|pwd|tree|file|stat)\b`)

// readCodexEvents follows Codex's output, one JSON object per line, to its
// end: Codex blocks once a pipe nobody reads is full. finished is true once
// the turn has ended, well or badly. An error event alone is not the end:
// Codex sends one when it reconnects, and carries on.
func readCodexEvents(r io.Reader, dir string, progress func(Action)) (result Result, finished bool, err error) {
	// lastError is the last error event, and reason the last one that is
	// not Codex saying it reconnects: that one says why a turn failed.
	var lastError, reason, failure string
	failed := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var e codexEvent
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "thread.started":
			result.SessionID = e.ThreadID
		case "item.started":
			if progress == nil {
				continue
			}
			switch e.Item.Type {
			case "file_change":
				for _, change := range e.Item.Changes {
					progress(Action{Verb: "edit", Detail: relative(change.Path, dir)})
				}
			case "command_execution":
				if command := unwrapShell(e.Item.Command); command != "" && !lookingAround.MatchString(command) {
					progress(Action{Verb: "run", Detail: firstLine(command)})
				}
			}
		case "item.completed":
			// The last thing the agent says is its report.
			if e.Item.Type == "agent_message" {
				result.Summary = strings.TrimSpace(e.Item.Text)
			}
		case "turn.completed":
			finished, failed = true, false
		case "turn.failed":
			finished, failed = true, true
			// The error event before it usually says more.
			failure = firstNonEmpty(reason, e.Error.Message, e.Message, lastError)
		case "error":
			lastError = firstNonEmpty(e.Error.Message, e.Message)
			if !strings.HasPrefix(lastError, "Reconnecting") {
				reason = lastError
			}
		}
	}
	switch {
	case scanner.Err() != nil:
		return result, finished, scanner.Err()
	case failed:
		return result, true, errors.New(codexFailure(failure))
	case !finished && lastError != "":
		return result, true, errors.New(codexFailure(firstNonEmpty(reason, lastError)))
	}
	return result, finished, nil
}

// unwrapShell returns the command Codex was asked to run, without the shell
// it runs it through.
func unwrapShell(command string) string {
	m := shellWrapper.FindStringSubmatch(command)
	if m == nil {
		return command
	}
	inner := m[1]
	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
		inner = inner[1 : len(inner)-1]
	}
	return inner
}

// codexFailure pulls the sentence out of a failure, which often arrives as
// the service's JSON error inside a string.
func codexFailure(message string) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(message), &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return firstNonEmpty(message, "Codex reported an error")
}
