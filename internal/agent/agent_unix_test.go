//go:build !windows

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The help Claude Code is asked for runs as any program the CLI starts:
// stopping the run stops it and what it started, and its error output
// counts.
func TestTheHelpCheckIsStoppedWithTheRun(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\nif [ \"$1\" = --help ]; then sleep 60 & echo $! > " + filepath.Join(dir, "child.tmp") + "; mv " + filepath.Join(dir, "child.tmp") + " " + filepath.Join(dir, "child") + "; wait; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Find(func(string) (string, error) { return script, nil })[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.Run(ctx, Task{Dir: dir, Prompt: "x"}, nil)
		done <- err
	}()
	var child int
	for deadline := time.Now().Add(10 * time.Second); child == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the help check did not start")
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "child"))
		child, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a stopped check let the agent start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run was still waiting for the help check 5 seconds after it was stopped")
	}
	for deadline := time.Now().Add(3 * time.Second); syscall.Kill(child, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatal("the help check's child is still running")
		}
	}
}
