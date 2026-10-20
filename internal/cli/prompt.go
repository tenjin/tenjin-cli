package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

// readLine reads one line of input, without its line ending. Ctrl-C ends
// the wait.
func (e *environment) readLine() (string, error) {
	type read struct {
		line string
		err  error
	}
	done := make(chan read, 1)
	go func() {
		line, err := e.stdin.ReadString('\n')
		done <- read{line, err}
	}()
	var r read
	select {
	case r = <-done:
	case <-e.context().Done():
		return "", errInterrupted()
	}
	if r.err != nil && r.line == "" {
		return "", r.err
	}
	return strings.TrimRight(r.line, "\r\n"), nil
}

// context is the run's context, which Ctrl-C cancels.
func (e *environment) context() context.Context {
	if e.ctx == nil {
		return context.Background()
	}
	return e.ctx
}

// pause waits, and returns early with an error when the user presses Ctrl-C.
func (e *environment) pause(d time.Duration) error {
	if e.wait != nil {
		e.wait(d)
	} else {
		select {
		case <-time.After(d):
		case <-e.context().Done():
		}
	}
	return e.interrupted()
}

// interrupted is the error to stop with once the user pressed Ctrl-C, or nil.
func (e *environment) interrupted() error {
	if e.context().Err() != nil {
		return errInterrupted()
	}
	return nil
}

func errInterrupted() error { return clierr.New(clierr.Interrupted, "stopped") }

// confirm asks a yes/no question. An empty answer takes the default; an
// answer that is neither is asked again. Input that ends before an answer is
// io.EOF, which the command reports as a usage error: nothing is created or
// started on an answer nobody gave, and nobody said no either.
func (e *environment) confirm(w io.Writer, question string, def bool) (bool, error) {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	_, _ = fmt.Fprint(w, question+suffix)
	for {
		answer, err := e.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				_, _ = fmt.Fprintln(w)
			}
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		_, _ = fmt.Fprint(w, "Answer y or n:"+suffix)
	}
}

// ask prompts for a line of text. Input that ends before an answer is
// io.EOF, for the command to report as a usage error.
func (e *environment) ask(w io.Writer, question string) (string, error) {
	_, _ = fmt.Fprint(w, question+" ")
	answer, err := e.readLine()
	if errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintln(w)
	}
	return strings.TrimSpace(answer), err
}

// openBrowser opens a URL in the user's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
