package main

// Process tests run the launcher the way it is installed: the launcher and a stand-in version
// (testdata/fakeversion) are built once per test run into a temporary install folder,
//
//	<install>/kb[.exe]                       the launcher
//	<install>/versions/0.0.0-fake/kb[.exe]   the fake version, the only one installed
//
// and startLauncherProcess runs the launcher there as a process, so that what happens to it and to the
// version it starts, under signals and termination, can be observed. The platform parts (watching the
// version's process, process groups, console control events) are in process_windows_test.go and
// process_other_test.go.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fakeVersion = "0.0.0-fake"
	// fakeVersionPIDFileEnv names the file the fake version writes its process id to once it is ready.
	fakeVersionPIDFileEnv = "KENT_FAKE_VERSION_PIDFILE"
	processTimeout        = 30 * time.Second
)

var launcherBuild struct {
	once sync.Once
	dir  string // the install folder
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	removeLauncherBuild()
	os.Exit(code)
}

// builtLauncher builds the launcher and the fake version into the install folder, the first time a test
// asks, and returns that folder. Without a go command the test is skipped.
func builtLauncher(t *testing.T) string {
	t.Helper()
	gotool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("process tests build the launcher, and there is no go command: " + err.Error())
	}
	launcherBuild.once.Do(func() {
		launcherBuild.dir, launcherBuild.err = buildLauncher(gotool)
	})
	if launcherBuild.err != nil {
		t.Fatal(launcherBuild.err)
	}
	return launcherBuild.dir
}

func buildLauncher(gotool string) (string, error) {
	dir, err := os.MkdirTemp("", "kb-launcher-test-")
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "build")
	cmd := exec.Command(gotool, "build", "-o", out+string(filepath.Separator), ".", "./testdata/fakeversion")
	if b, err := cmd.CombinedOutput(); err != nil {
		return dir, fmt.Errorf("building the launcher and the fake version: %v\n%s", err, b)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	version := filepath.Join(dir, "versions", fakeVersion)
	if err := os.MkdirAll(version, 0o755); err != nil {
		return dir, err
	}
	if err := os.Rename(filepath.Join(out, "launcher"+ext), filepath.Join(dir, exeName())); err != nil {
		return dir, err
	}
	if err := os.Rename(filepath.Join(out, "fakeversion"+ext), filepath.Join(version, exeName())); err != nil {
		return dir, err
	}
	return dir, os.Remove(out)
}

// removeLauncherBuild removes the install folder. On Windows a program's file cannot be removed for a
// moment after the program ends, so it tries for a while.
func removeLauncherBuild() {
	if launcherBuild.dir == "" {
		return
	}
	var err error
	for i := 0; i < 50; i++ {
		if err = os.RemoveAll(launcherBuild.dir); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "removing the launcher's test install folder:", err)
}

type launchOptions struct {
	// ownProcessGroup starts the launcher in a new process group, as sendCtrlBreak needs.
	ownProcessGroup bool
}

// launcherProcess is a launcher started by startLauncherProcess.
type launcherProcess struct {
	t               *testing.T
	cmd             *exec.Cmd
	ownProcessGroup bool
	done            chan struct{} // closed once the launcher has ended
	dir             string
	pidFile         string
	stdout, stderr  string
	versionExe      string
	version         *versionProcess
}

// startLauncherProcess runs the built launcher with args, in a folder of its own outside any knowledge
// base, with KENT_KB, KENT_VERSION and KENT_LAUNCHED left out of its environment, so that it chooses the
// fake version. When the test ends, the launcher and the version it started are ended if they are still
// running.
func startLauncherProcess(t *testing.T, opts launchOptions, args ...string) *launcherProcess {
	t.Helper()
	install := builtLauncher(t)
	dir := t.TempDir()
	r := &launcherProcess{
		t:               t,
		ownProcessGroup: opts.ownProcessGroup,
		done:            make(chan struct{}),
		dir:             dir,
		pidFile:         filepath.Join(dir, "version.pid"),
		stdout:          filepath.Join(dir, "stdout"),
		stderr:          filepath.Join(dir, "stderr"),
		versionExe:      filepath.Join(install, "versions", fakeVersion, exeName()),
	}
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// Files rather than pipes: a version left running keeps the launcher's output handles open, and
	// waiting on a pipe would wait for it too.
	stdout, err := os.Create(r.stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(r.stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	r.cmd = exec.Command(filepath.Join(install, exeName()), args...)
	r.cmd.Dir = work
	r.cmd.Env = append(launcherTestEnv(), fakeVersionPIDFileEnv+"="+r.pidFile)
	r.cmd.Stdout, r.cmd.Stderr = stdout, stderr
	if opts.ownProcessGroup {
		r.cmd.SysProcAttr = newProcessGroupAttr()
	}
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		r.cmd.Wait()
		close(r.done)
	}()
	// registered after t.TempDir, so it runs before the folder is removed
	t.Cleanup(r.cleanup)
	return r
}

// launcherTestEnv is the test's environment without the variables that would make the launcher choose
// something other than the fake version, or refuse to run.
func launcherTestEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, n := range []string{kbEnv, versionEnv, launchedEnv} {
			if name == n || (runtime.GOOS == "windows" && strings.EqualFold(name, n)) {
				drop = true
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	return env
}

func (r *launcherProcess) cleanup() {
	if !r.ended() {
		r.cmd.Process.Kill()
		select {
		case <-r.done:
		case <-time.After(processTimeout):
			r.t.Errorf("the launcher (pid %d) did not end when killed", r.cmd.Process.Pid)
		}
	}
	if r.version == nil {
		if pid, ok := r.readPID(); ok {
			if v, err := openVersionProcess(r, pid); err == nil {
				r.version = v
			}
		}
	}
	if r.version != nil {
		r.version.kill()
	}
}

func (r *launcherProcess) ended() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *launcherProcess) readPID() (int, bool) {
	b, err := os.ReadFile(r.pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil
}

// waitVersion waits until the version the launcher started is ready, and returns its process. It fails
// the test if the launcher ends first or the version is not ready in time.
func (r *launcherProcess) waitVersion() *versionProcess {
	r.t.Helper()
	if r.version != nil {
		return r.version
	}
	deadline := time.Now().Add(processTimeout)
	for {
		if pid, ok := r.readPID(); ok {
			v, err := openVersionProcess(r, pid)
			if err != nil {
				r.t.Fatal(err)
			}
			r.version = v
			return v
		}
		if r.ended() {
			r.t.Fatalf("the launcher ended (%s) without starting the version\n%s", r.cmd.ProcessState, r.output())
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("the version did not start within %s\n%s", processTimeout, r.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// terminate ends the launcher process at once, without a signal it could handle: TerminateProcess on
// Windows, SIGKILL elsewhere. Nothing it started is ended by the test.
func (r *launcherProcess) terminate() {
	r.t.Helper()
	if err := r.cmd.Process.Kill(); err != nil && !r.ended() {
		r.t.Fatal(err)
	}
}

// wait waits for the launcher to end and returns its exit status (-1 if a signal ended it), failing the
// test if it does not end within timeout.
func (r *launcherProcess) wait(timeout time.Duration) int {
	r.t.Helper()
	select {
	case <-r.done:
		return r.cmd.ProcessState.ExitCode()
	case <-time.After(timeout):
		r.t.Fatalf("the launcher did not end within %s\n%s", timeout, r.output())
		return 0
	}
}

// output is what the launcher and the version wrote so far, for failure messages.
func (r *launcherProcess) output() string {
	out, _ := os.ReadFile(r.stdout)
	errOut, _ := os.ReadFile(r.stderr)
	return "stdout:\n" + string(out) + "\nstderr:\n" + string(errOut)
}

// TestLauncherProcessRunsVersion: the built launcher runs the installed fake version, which reports its
// process, and ends with the version's exit status.
func TestLauncherProcessRunsVersion(t *testing.T) {
	r := startLauncherProcess(t, launchOptions{}, "exit", "7")
	v := r.waitVersion()
	if got := r.wait(processTimeout); got != 7 {
		t.Fatalf("launcher exit status = %d, want the version's 7\n%s", got, r.output())
	}
	if !v.waitExit(processTimeout) {
		t.Fatal("the version is still running after it exited")
	}
}
