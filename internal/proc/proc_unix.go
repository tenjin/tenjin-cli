//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// stopSignals are Ctrl-C, a request to stop (SIGTERM, from kill or a
// supervisor) and a terminal that closes (SIGHUP). Without handling the
// last two, the CLI would die and leave a coding agent running in its own
// process group, unwatched.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// tree starts the program in a new process group and, on cancel, kills the
// group, children included.
type tree struct{}

func (tree) prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative PID names the group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func (tree) start(cmd *exec.Cmd) error { return cmd.Start() }

func (tree) release() {}
