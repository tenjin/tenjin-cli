package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/proc"
)

// readLine reads one line of input, without its line ending. Ctrl-C ends
// the wait.
func (e *environment) readLine() (string, error) {
	line, err := e.readLineUntil(e.context())
	if err != nil && e.context().Err() != nil {
		return "", errInterrupted()
	}
	return line, err
}

// lineRead is one line of input, or why there is none.
type lineRead struct {
	line string
	err  error
}

// readLineUntil reads one line of input, without its line ending, until ctx
// ends. A read of a terminal cannot be called off, so one that ctx ends is
// left under way, and the next read takes its line: what is typed after a
// wait ended, such as the one for a pasted sign-in address, answers the next
// question rather than being lost to it.
func (e *environment) readLineUntil(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e.lineMu.Lock()
	read := e.lineRead
	if read == nil {
		read = make(chan lineRead, 1)
		go func() {
			line, err := e.stdin.ReadString('\n')
			read <- lineRead{line, err}
		}()
		e.lineRead = read
	}
	e.lineMu.Unlock()
	var r lineRead
	select {
	case r = <-read:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if ctx.Err() != nil {
		// The wait ended as the line came: it is the next read's.
		read <- r
		return "", ctx.Err()
	}
	e.lineMu.Lock()
	e.lineRead = nil
	e.lineMu.Unlock()
	if r.err != nil && r.line == "" {
		return "", r.err
	}
	return strings.TrimRight(r.line, "\r\n"), nil
}

// lineUnderWay reports whether a read of a line is still under way, left by
// a wait that ended first. Keys cannot be read meanwhile: they would go to
// that read.
func (e *environment) lineUnderWay() bool {
	e.lineMu.Lock()
	defer e.lineMu.Unlock()
	return e.lineRead != nil
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

// openBrowser opens a URL in the user's default browser. The opener hands
// the URL to the desktop and is not waited for, and the browser it may
// start is the user's, not the CLI's: it is detached, so Ctrl-C does not
// close the browser, and the opener is reaped once it ends.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return proc.Detach("open", []string{url}, nil)
	case "windows":
		return proc.Detach("rundll32", []string{"url.dll,FileProtocolHandler", url}, nil)
	}
	return proc.Detach("xdg-open", []string{url}, nil)
}
