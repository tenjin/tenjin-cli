//go:build !windows

package cli

import (
	"bufio"
	"io"
	"os"
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

	done := make(chan int, 1)
	go func() {
		code, _, _ := h.run("setup", dir)
		done <- code
	}()
	time.Sleep(300 * time.Millisecond)
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
