package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The session-start hook runs "kb session-start" when a conversation starts (S-0129). Whatever goes wrong,
// the agent must still learn that the project has a knowledge base and what holds before anything else, so
// the launcher never lets that command fail: it runs the version as a child, and if the version cannot be
// chosen or does not succeed, it prints what it can instead and exits 0.

const sessionStartArg = "session-start"

// fallbackRules are the two rules that hold before any of Kent's documents is read.
const fallbackRules = `Two rules hold before any of Kent's documents is read:

- **Write to the record only through ` + "`kb`" + `.** Never create or edit anything in the knowledge base's
  evidence/, attested/ or agent/ folders by hand, not even to fix a typo (Kent Rule 1).
- **A session nobody declared is off-record.** When statements worth recording come up, ask the human
  whether this session goes on the record before recording anything (Kent Rule 12).`

// sessionStart runs "kb session-start" in the chosen version, or says why it cannot.
func sessionStart(exe string, args, env []string, chooseErr error) int {
	if chooseErr == nil {
		out, errOut, err := capture(exe, args, env)
		if err == nil {
			os.Stdout.Write(out)
			return 0
		}
		// a version older than kb session-start printed its instructions with kb rules claude-code
		if old, _, err2 := capture(exe, []string{"rules", "claude-code"}, env); err2 == nil {
			fmt.Println("# Knowledge base")
			fmt.Println()
			fmt.Println("This project's session-start hook ran kb session-start, which the version of Kent that runs " +
				"here does not have, so these are its instructions from kb rules claude-code, written for a " +
				"project's CLAUDE.md. Where they mention CLAUDE.md, the settings are in this project's " +
				".claude/settings.local.json instead.")
			fmt.Println()
			os.Stdout.Write(old)
			return 0
		}
		chooseErr = fmt.Errorf("kb session-start failed: %s", strings.TrimSpace(string(errOut)+" "+err.Error()))
	}
	fmt.Println("# Knowledge base")
	fmt.Println()
	fmt.Println("This project keeps a knowledge base with Kent the Clerk, and its session-start hook ran, but kb " +
		"cannot run here, so nothing in this conversation can be recorded until that is fixed. Tell the human " +
		"when the conversation starts; what the launcher said is below. Do not install or download a version " +
		"of Kent yourself.")
	fmt.Println()
	fmt.Println(fallbackRules)
	fmt.Println()
	fmt.Println("What the launcher said:")
	fmt.Println()
	for _, l := range strings.Split(chooseErr.Error(), "\n") {
		fmt.Println("    " + l)
	}
	return 0
}

// capture runs a version with its output captured.
func capture(exe string, args, env []string) (stdout, stderr []byte, err error) {
	var out, errOut bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}
