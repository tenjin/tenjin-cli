//go:build !windows

package proc

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
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
