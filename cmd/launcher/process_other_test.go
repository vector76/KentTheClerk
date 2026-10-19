//go:build !windows

package main

import (
	"syscall"
	"testing"
	"time"
)

// versionProcess is the version's process. Here the launcher replaces itself with the version, so it is
// the launcher's own process, which is watched through the launcher's exec.Cmd: once the launcher has been
// waited for, its process id may be reused, and kill(pid, 0) or a SIGKILL would reach whatever holds it.
type versionProcess struct {
	pid      int
	launcher *launcherProcess // nil if the version is a process of its own
}

func openVersionProcess(r *launcherProcess, pid int) (*versionProcess, error) {
	v := &versionProcess{pid: pid}
	if pid == r.cmd.Process.Pid {
		v.launcher = r
	}
	return v, nil
}

func (v *versionProcess) running() bool {
	if v.launcher != nil {
		return !v.launcher.ended()
	}
	return syscall.Kill(v.pid, 0) == nil
}

// waitExit waits up to timeout for the version to end, and says whether it did.
func (v *versionProcess) waitExit(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for v.running() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// kill ends the version if it is still running.
func (v *versionProcess) kill() {
	if !v.running() {
		return
	}
	if v.launcher != nil {
		v.launcher.cmd.Process.Kill()
	} else {
		syscall.Kill(v.pid, syscall.SIGKILL)
	}
	v.waitExit(processTimeout)
}

func newProcessGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// sendCtrlBreak is Windows' console control event; elsewhere the test is skipped.
func sendCtrlBreak(t *testing.T, r *launcherProcess) {
	t.Helper()
	t.Skip("console control events exist only on Windows")
}
