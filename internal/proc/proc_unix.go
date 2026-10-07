//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the program in a new process group and, on cancel, kills
// the group, children included.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative PID names the group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
