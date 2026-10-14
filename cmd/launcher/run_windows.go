package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

// run starts the chosen version with the launcher's standard streams and returns its exit status.
// Windows cannot replace a running process, so the launcher waits for it. Ctrl-C reaches every process
// on the console; the launcher ignores it and lets the version decide what to do.
func run(exe string, args, env []string) int {
	signal.Ignore(os.Interrupt)
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	return fail(err)
}
