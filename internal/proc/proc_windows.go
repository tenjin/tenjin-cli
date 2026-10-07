//go:build windows

package proc

import "os/exec"

// ownGroup leaves Windows to exec's default, which kills the program
// itself; WaitDelay stops a child that keeps its output open from holding
// Wait.
func ownGroup(*exec.Cmd) {}
