// Package proc starts the programs the CLI runs (adb, xcrun, a coding
// agent) so that stopping one stops everything it started: the program runs
// in a process group of its own, the whole group is killed when the context
// ends, and output a leftover child holds open is not waited for forever.
package proc

import (
	"context"
	"os/exec"
	"time"
)

// waitDelay is how long Wait gives a killed program's output to close.
const waitDelay = 2 * time.Second

// Command is exec.CommandContext, with the group handling described above.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = waitDelay
	ownGroup(cmd)
	return cmd
}
