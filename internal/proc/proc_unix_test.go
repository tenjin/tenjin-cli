//go:build !windows

package proc

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A signal the CLI was started with ignored, as SIGHUP under nohup, is not
// taken back from whoever ignored it.
func TestStopContextLeavesAnIgnoredSignalIgnored(t *testing.T) {
	signal.Ignore(syscall.SIGHUP)
	defer signal.Reset(syscall.SIGHUP)

	ctx, stop := StopContext(context.Background())
	defer stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("an ignored SIGHUP stopped the run")
	case <-time.After(200 * time.Millisecond):
	}
	if !signal.Ignored(syscall.SIGHUP) {
		t.Error("SIGHUP is no longer ignored")
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("SIGTERM did not stop the run")
	}
}

// A detached program runs on its own, after Detach has returned.
func TestDetach(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ran")
	if err := Detach("/bin/sh", []string{"-c", "echo ok > " + out}, nil); err != nil {
		t.Fatal(err)
	}
	for range 250 {
		if raw, err := os.ReadFile(out); err == nil && string(raw) == "ok\n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the detached program did not run")
}

// A detached program that ends while the CLI still runs is reaped, not
// left a zombie until the CLI exits.
func TestDetachReaps(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pid")
	if err := Detach("/bin/sh", []string{"-c", "echo $$ > " + out + ".tmp; mv " + out + ".tmp " + out}, nil); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the detached program did not run")
		}
		raw, _ := os.ReadFile(out)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	// Signal 0 reaches a zombie too: it fails only once the process is gone.
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still there after it ended", pid)
		}
	}
}
