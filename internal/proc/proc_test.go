package proc

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A cancelled program returns at once, even when a child of its own keeps
// the output open: the child is killed with it, so Wait does not sit out
// waitDelay for the output to close.
func TestCancelStopsTheProgramAndItsChildren(t *testing.T) {
	name, args := "sh", []string{"-c", "sleep 30 & sleep 30"}
	if runtime.GOOS == "windows" {
		// cmd.exe starts ping, which holds the output, as an npm shim's
		// cmd.exe starts the agent.
		name, args = "cmd", []string{"/c", "ping -n 30 127.0.0.1"}
	}
	const after = 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), after)
	defer cancel()
	start := time.Now()
	_, _ = Command(ctx, name, args...).Output()
	if took := time.Since(start); took > after+waitDelay*3/4 {
		t.Errorf("took %s: a child outlived its program", took)
	}
}

// Output reports the program's standard error in a failure, as exec's
// does, which the device tools' messages are read from.
func TestOutputKeepsTheErrorOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	out, err := Command(context.Background(), "sh", "-c", "echo out; echo oops >&2; exit 3").Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || strings.TrimSpace(string(exit.Stderr)) != "oops" || string(out) != "out\n" {
		t.Errorf("out = %q, err = %v", out, err)
	}
	if err := Command(context.Background(), "sh", "-c", "exit 0").Run(); err != nil {
		t.Errorf("run: %v", err)
	}
	c := Command(context.Background(), "sh", "-c", "exit 0")
	c.Stdout = &strings.Builder{}
	if _, err := c.Output(); err == nil {
		t.Error("Output with Stdout set did not fail")
	}
}
