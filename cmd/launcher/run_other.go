//go:build !windows

package main

import "syscall"

// run replaces the launcher with the chosen version, so signals and the exit status are the version's own.
func run(exe string, args, env []string) int {
	err := syscall.Exec(exe, append([]string{exe}, args...), env)
	return fail(err)
}
