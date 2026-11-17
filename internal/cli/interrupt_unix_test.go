//go:build !windows

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// Ctrl-C at a prompt ends setup at once, with the interrupted status.
func TestCtrlCAtAPromptStopsSetup(t *testing.T) {
	h := newHarness(t)
	h.env.interactive = true
	// A terminal nobody types at.
	stdin, typing := io.Pipe()
	defer func() { _ = typing.Close() }()
	h.env.stdin = bufio.NewReader(stdin)
	dir := testproject.Write(t, testproject.AndroidNative())

	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- execute([]string{"setup", dir}, &stdout, &stderr, h.env)
	}()
	// The signal goes once setup is at its first question, waiting for an
	// answer. setup, a flow for people, asks on stdout.
	for deadline := time.Now().Add(10 * time.Second); !atPrompt(stdout.String()); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("setup did not reach a prompt in 10 seconds:\n%s%s", stdout.String(), stderr.String())
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 130 {
			t.Errorf("exit = %d, want 130", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("setup was still waiting at its prompt 3 seconds after Ctrl-C")
	}
}

// Ctrl-C while the device is read ends doctor with the interrupted status.
// The read it cut short is not a device that cannot be read, and the key
// check it cut short is not a warning: nothing after it stands.
func TestCtrlCWhileReadingTheDeviceStopsDoctor(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	h.env.lookPath = func(name string) (string, error) {
		if name == "adb" {
			return "/opt/android/platform-tools/adb", nil
		}
		return "", fmt.Errorf("%s: not found", name)
	}
	h.env.run = func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "devices" {
			return []byte("List of devices attached\nemulator-5554\tdevice\n\n"), nil
		}
		// Ctrl-C lands while adb dumps the log, and ends adb with it.
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("the command's context was not cancelled by Ctrl-C")
		}
		return nil, errors.New("signal: interrupt")
	}
	code, stdout, stderr := h.run("doctor", dir, "--device")
	if code != 130 || stdout != "" || !strings.Contains(stderr, "stopped") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if len(h.fake.keysAsked) != 0 {
		t.Errorf("the key was checked after Ctrl-C: %q", h.fake.keysAsked)
	}
}

// atPrompt reports whether output ends at setup's first question, the
// choice of features asked a line at a time, waiting for its answer. The
// tick may be coloured.
func atPrompt(output string) bool {
	return strings.HasSuffix(output, " list is right: ")
}

// A line that only says what setup is doing is not a question.
func TestAtPromptWaitsForTheQuestion(t *testing.T) {
	if atPrompt("Step 1 of 7 · Your project\nReading: ") || !atPrompt("all or none. Press Enter when the ✓ list is right: ") {
		t.Error("atPrompt")
	}
}

// slowAgentScript is a Claude Code that starts a child and works forever,
// saying which processes are its own.
const slowAgentScript = `#!/bin/sh
if [ "$1" = "--help" ]; then echo "  --restricted"; exit 0; fi
sleep 60 &
echo "$$ $!" > "$SLOW_AGENT_DIR/pids.tmp"
mv "$SLOW_AGENT_DIR/pids.tmp" "$SLOW_AGENT_DIR/pids"
wait
`

// A terminal that closes, or a kill, stops the agent with the CLI, as
// Ctrl-C does: it runs in a process group of its own, which nothing else
// would stop.
func TestTermAndHangupStopTheAgent(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			h, dir := brokenAndroid(t)
			agentDir := t.TempDir()
			script := filepath.Join(agentDir, "claude")
			if err := os.WriteFile(script, []byte(slowAgentScript), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SLOW_AGENT_DIR", agentDir)
			h.onPath("claude", script)

			done := make(chan int, 1)
			go func() {
				code, _, _ := h.run("setup", dir, "--agent", "claude")
				done <- code
			}()
			var pids []int
			for deadline := time.Now().Add(10 * time.Second); len(pids) == 0; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the agent did not start")
				}
				raw, _ := os.ReadFile(filepath.Join(agentDir, "pids"))
				for _, field := range strings.Fields(string(raw)) {
					pid, _ := strconv.Atoi(field)
					pids = append(pids, pid)
				}
			}
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-done:
				if code != 130 {
					t.Errorf("exit = %d, want 130", code)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("setup was still running 5 seconds after %s", sig)
			}
			// The agent and its child are gone; a child may take a moment to be
			// reaped once orphaned.
			for _, pid := range pids {
				for deadline := time.Now().Add(3 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Errorf("process %d is still running", pid)
						_ = syscall.Kill(pid, syscall.SIGKILL)
						break
					}
				}
			}
		})
	}
}
