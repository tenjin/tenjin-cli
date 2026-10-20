//go:build windows

package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"
)

// stopSignals are Ctrl-C and, as Go reports them, a console window that is
// closed, a log-off and a shutdown (SIGTERM).
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// tree puts the program in a job object, which every process it starts
// joins too, and on cancel ends the job: an npm shim's cmd.exe and the
// agent it started, or adb and its children, stop together. The program is
// started suspended and resumed once it is in the job, so that nothing it
// starts can escape before. Where a job cannot be made, cancel kills the
// program alone, as exec does.
type tree struct {
	// job is read by cancel, which exec calls from a goroutine of its own.
	job atomic.Uintptr
}

func (t *tree) prepare(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if job := windows.Handle(t.job.Load()); job != 0 && windows.TerminateJobObject(job, 1) == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

func (t *tree) start(cmd *exec.Cmd) error {
	// Made before the program starts, so that cancel, which can run as soon
	// as it has, always sees it.
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		t.job.Store(uintptr(job))
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		t.release()
		return err
	}
	pid := uint32(cmd.Process.Pid) //nolint:gosec // G115: a process ID fits
	if job := windows.Handle(t.job.Load()); job != 0 && assign(job, pid) != nil {
		t.release()
	}
	if err := resume(pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.release()
		return fmt.Errorf("could not start %s: %w", cmd.Path, err)
	}
	return nil
}

func (t *tree) release() {
	if job := windows.Handle(t.job.Swap(0)); job != 0 {
		_ = windows.CloseHandle(job)
	}
}

func assign(job windows.Handle, pid uint32) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	return windows.AssignProcessToJobObject(job, process)
}

// resume starts the threads of a process created suspended. exec does not
// keep the handle of its first thread, so it is found by listing threads.
func resume(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	var entry windows.ThreadEntry32
	entry.Size = uint32(binary.Size(entry)) //nolint:gosec // G115: the size of a small struct
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return err
		}
		resumed = true
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	if !resumed {
		return errors.New("its first thread was not found")
	}
	return nil
}
