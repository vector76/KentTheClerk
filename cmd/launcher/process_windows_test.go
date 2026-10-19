package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// versionProcess is the version's process, watched through a handle opened as soon as it reports itself,
// which also keeps its process id from being reused while the test looks at it.
type versionProcess struct {
	pid int
	h   windows.Handle // 0 when it had already ended
}

// openVersionProcess opens the process pid that the version r started has reported, checking that it runs
// the version's program so that a process id reused since is not taken for the version.
func openVersionProcess(r *launcherProcess, pid int) (*versionProcess, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return &versionProcess{pid: pid}, nil // no such process: it has ended
	}
	if err != nil {
		return nil, fmt.Errorf("opening the version's process %d: %v", pid, err)
	}
	same, err := runsProgram(h, r.versionExe)
	if err != nil {
		// a process that has ended may no longer say what it ran; one still running must, or it could be
		// the version and would be taken for one that has ended
		ev, _ := windows.WaitForSingleObject(h, 0)
		windows.CloseHandle(h)
		if ev == windows.WAIT_OBJECT_0 {
			return &versionProcess{pid: pid}, nil
		}
		return nil, fmt.Errorf("checking which program process %d runs: %v", pid, err)
	}
	if !same {
		windows.CloseHandle(h)
		return &versionProcess{pid: pid}, nil // the id now belongs to another program: the version has ended
	}
	return &versionProcess{pid: pid, h: h}, nil
}

// runsProgram says whether the process h runs the program exe.
func runsProgram(h windows.Handle, exe string) (bool, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return false, err
	}
	got, err := os.Stat(windows.UTF16ToString(buf[:n]))
	if err != nil {
		return false, err
	}
	want, err := os.Stat(exe)
	if err != nil {
		return false, err
	}
	return os.SameFile(got, want), nil
}

func (v *versionProcess) running() bool {
	if v.h == 0 {
		return false
	}
	ev, _ := windows.WaitForSingleObject(v.h, 0)
	return ev == uint32(windows.WAIT_TIMEOUT)
}

// waitExit waits up to timeout for the version to end, and says whether it did.
func (v *versionProcess) waitExit(timeout time.Duration) bool {
	if v.h == 0 {
		return true
	}
	ev, _ := windows.WaitForSingleObject(v.h, uint32(timeout/time.Millisecond))
	return ev == windows.WAIT_OBJECT_0
}

// kill ends the version if it is still running and lets go of it.
func (v *versionProcess) kill() {
	if v.h == 0 {
		return
	}
	if v.running() {
		windows.TerminateProcess(v.h, 1)
		windows.WaitForSingleObject(v.h, uint32(processTimeout/time.Millisecond))
	}
	windows.CloseHandle(v.h)
	v.h = 0
}

func newProcessGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// sendCtrlBreak sends CTRL_BREAK_EVENT to the launcher's process group, which holds the launcher and the
// version it started, as pressing Ctrl-Break in their console would. The launcher must have been started
// with ownProcessGroup, so that the event does not reach the test.
func sendCtrlBreak(t *testing.T, r *launcherProcess) {
	t.Helper()
	if !r.ownProcessGroup {
		t.Fatal("sendCtrlBreak needs a launcher started in its own process group")
	}
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(r.cmd.Process.Pid)); err != nil {
		t.Fatalf("sending Ctrl-Break to the launcher's process group: %v", err)
	}
}
