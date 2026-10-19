// Command fakeversion stands in for an installed version of Kent in the launcher's process tests
// (process_test.go), which build it into versions/<version>/kb[.exe] beside a built launcher. It writes
// its process id to the file KENT_FAKE_VERSION_PIDFILE names, once it is ready, and then does one of:
//
//	fakeversion exit <status>   exits with that status at once
//	fakeversion serve           runs until interrupted, as kb serve does: on Ctrl-C or Ctrl-Break it stops
//	                            and exits 0
//
// A serve nobody stops ends by itself after a few minutes, so that one a test left running does not stay.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"
)

const (
	pidFileEnv = "KENT_FAKE_VERSION_PIDFILE"
	serveLimit = 3 * time.Minute
)

// ready writes the process id where the test looks for it, renaming it into place so that the test never
// reads it half written.
func ready() {
	f := os.Getenv(pidFileEnv)
	if f == "" {
		return
	}
	if err := os.WriteFile(f+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o666); err != nil {
		fail(err)
	}
	if err := os.Rename(f+".tmp", f); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fake version: "+err.Error())
	os.Exit(2)
}

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 2 && args[0] == "exit":
		status, err := strconv.Atoi(args[1])
		if err != nil {
			fail(err)
		}
		ready()
		os.Exit(status)
	case len(args) == 1 && args[0] == "serve":
		// the handler is in place before the test is told the version is ready
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt)
		ready()
		fmt.Println("serving (Ctrl+C to stop)")
		select {
		case <-stop:
			os.Exit(0)
		case <-time.After(serveLimit):
			fmt.Fprintf(os.Stderr, "fake version: not stopped within %s\n", serveLimit)
			os.Exit(3)
		}
	}
	fail(fmt.Errorf("unknown arguments %q", args))
}
