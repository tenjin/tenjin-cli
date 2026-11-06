// Package proc starts the programs the CLI runs (adb, xcrun, a coding
// agent) so that stopping one stops everything it started: the program and
// its children are killed together when the context ends (a process group on
// Unix, a job object on Windows), and output a leftover child holds open is
// not waited for forever.
package proc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"time"
)

// waitDelay is how long Wait gives a killed program's output to close.
const waitDelay = 2 * time.Second

// Cmd is an exec.Cmd whose Start, Wait, Run and Output keep the program's
// children together with it. Use those, not CombinedOutput, which would
// start it without them.
type Cmd struct {
	*exec.Cmd
	tree tree
}

// Command is exec.CommandContext, with the handling described above.
func Command(ctx context.Context, name string, args ...string) *Cmd {
	c := &Cmd{Cmd: exec.CommandContext(ctx, name, args...)}
	c.WaitDelay = waitDelay
	c.tree.prepare(c.Cmd)
	return c
}

// Start starts the program.
func (c *Cmd) Start() error { return c.tree.start(c.Cmd) }

// Wait waits for the program to exit, as exec.Cmd.Wait does.
func (c *Cmd) Wait() error {
	err := c.Cmd.Wait()
	c.tree.release()
	return err
}

// Run starts the program and waits for it.
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// Output runs the program and returns its standard output. As with
// exec.Cmd.Output, a failure's standard error is in its *exec.ExitError
// when Stderr was not set.
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout
	var stderr *bytes.Buffer
	if c.Stderr == nil {
		stderr = &bytes.Buffer{}
		c.Stderr = stderr
	}
	err := c.Run()
	var exit *exec.ExitError
	if stderr != nil && errors.As(err, &exit) {
		exit.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

// Detach starts a program that outlives the CLI and is not waited for: in a
// session of its own on Unix, a new process group without a console on
// Windows, with no input or output. Ctrl-C does not reach it.
func Detach(name string, args, env []string) error {
	cmd, err := startDetached(func() *exec.Cmd {
		cmd := exec.Command(name, args...)
		cmd.Env = env
		return cmd
	})
	if err != nil {
		return err
	}
	return cmd.Process.Release()
}

// StopContext returns a context that ends when the person or the system
// stops the CLI: Ctrl-C, and the other signals in stopSignals. One the CLI
// was started with ignored, as under nohup, stays ignored.
func StopContext(parent context.Context) (context.Context, context.CancelFunc) {
	var signals []os.Signal
	for _, s := range stopSignals {
		if !signal.Ignored(s) {
			signals = append(signals, s)
		}
	}
	// NotifyContext with no signal at all would relay every one.
	if len(signals) == 0 {
		return context.WithCancel(parent)
	}
	return signal.NotifyContext(parent, signals...)
}
