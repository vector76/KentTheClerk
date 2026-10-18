// Command launcher is Kent the Clerk's launcher, installed as kb on the PATH with the installed versions
// beside it (S-0112):
//
//	<install folder>/kb[.exe]                      this launcher
//	<install folder>/versions/<version>/kb[.exe]   each installed version of Kent
//
// It runs the version the knowledge base is pinned to (S-0115), the one --kb names when the project refers to
// several and their pins differ; with no pin, the version named by
// KENT_VERSION in the user's settings for the project; with neither, the installed version with the
// highest version number, a development build such as 0.9.0-dev counting as just below 0.9.0. A pin and a
// different KENT_VERSION are refused. A missing version is never installed (S-0108): the launcher says how
// the human would install it.
//
// The launcher is infrastructure rather than part of the system it dispatches to: it is outside the system
// hash and never pinned. Every argument goes to the version it runs, except a first argument of
// --launcher, which asks the launcher itself (kb --launcher, kb --launcher version). kb session-start, run by
// the session-start hook, never fails (session_start.go).
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"kenttheclerk/internal/kbset"
	"kenttheclerk/internal/pin"
)

// launcherVersion is the launcher's own version, which the install scripts compare to replace the
// launcher only with a newer one. It is not Kent's version.
var launcherVersion = "0.2.0"

const (
	// repo is the GitHub repository Kent's releases are published from.
	repo       = "vector76/KentTheClerk"
	versionEnv = "KENT_VERSION"
	kbEnv      = "KENT_KB"
	// launchedEnv tells the version that it was started by the launcher, and how its version was chosen.
	launchedEnv = "KENT_LAUNCHED"
)

func exeName() string {
	if runtime.GOOS == "windows" {
		return "kb.exe"
	}
	return "kb"
}

// installDir is the folder the launcher is installed in.
func installDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe), nil
}

// installed lists the versions in <dir>/versions that hold an executable, highest first.
func installed(dir string) []string {
	entries, _ := os.ReadDir(filepath.Join(dir, "versions"))
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !validVersion(e.Name()) {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, "versions", e.Name(), exeName())); err == nil && !st.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i], out[j]) > 0 })
	return out
}

// findKBs are where the recorder will look for the knowledge bases: the folders KENT_KB names, which may
// be several (S-ZBVAHS), else the current folder or the nearest folder above it holding attested/ and
// evidence/. It mirrors the recorder's own lookup only to find the pins; when it finds nothing, the version
// chosen without a pin reports the problem itself.
func findKBs() []string {
	if env := os.Getenv(kbEnv); env != "" {
		var out []string
		for _, f := range kbset.SplitList(env) {
			if p := kbset.Abs(f); kbset.IsKB(p) {
				out = append(out, p)
			}
		}
		return out
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	for p := cwd; ; {
		if kbset.IsKB(p) {
			return []string{p}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
}

// kbArg is the value of --kb among the arguments, wherever it stands, as the recorder takes it.
func kbArg(args []string) string {
	for i, a := range args {
		switch {
		case a == "--":
			return ""
		case a == "--kb" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "--kb="):
			return strings.TrimPrefix(a, "--kb=")
		}
	}
	return ""
}

// pinned is a knowledge base and its pin.
type pinned struct {
	root string
	pin  *pin.Pin
}

// pinFor is the pin that chooses the version: that of the knowledge base --kb names, else the one pin the
// project's knowledge bases agree on. When their pins differ, a command must name its knowledge base,
// except kb session-start, which runs under the highest version they are pinned to and tells the agent to
// pass --kb. A --kb the launcher cannot follow is left to the recorder to report.
func pinFor(args []string) (*pin.Pin, string, error) {
	roots := findKBs()
	if a := kbArg(args); a != "" {
		if f, err := kbset.Arg(a, roots); err == nil {
			roots = []string{f}
		}
	}
	var pins []pinned
	for _, r := range roots {
		got, ok, err := pin.Read(r)
		if err != nil {
			return nil, "", fmt.Errorf("the knowledge base's pin cannot be read: %v", err)
		}
		if ok {
			pins = append(pins, pinned{r, &got})
		} else {
			pins = append(pins, pinned{r, nil})
		}
	}
	if len(pins) == 0 {
		return nil, "", nil
	}
	same := true
	for _, x := range pins[1:] {
		if (x.pin == nil) != (pins[0].pin == nil) || (x.pin != nil && *x.pin != *pins[0].pin) {
			same = false
		}
	}
	if same {
		return pins[0].pin, pin.Path(pins[0].root), nil
	}
	if len(args) == 1 && args[0] == sessionStartArg {
		var best *pinned
		for i, x := range pins {
			if x.pin != nil && (best == nil || compareVersions(x.pin.Version, best.pin.Version) > 0) {
				best = &pins[i]
			}
		}
		return best.pin, pin.Path(best.root), nil
	}
	var names []string
	for _, x := range pins {
		v := "not pinned"
		if x.pin != nil {
			v = "pinned to " + x.pin.Version
		}
		names = append(names, x.root+" ("+v+")")
	}
	return nil, "", fmt.Errorf("this project's knowledge bases are pinned differently: %s. Name the one this "+
		"command acts on with --kb <folder or id>, and the launcher runs the version it is pinned to.",
		strings.Join(names, ", "))
}

// choice is the version to run and why.
type choice struct {
	version, reason string
}

// choose applies the order of S-0112 to what is installed, the pin (if any) and KENT_VERSION (if set).
func choose(versions []string, p *pin.Pin, pinPath, envVersion, dir string) (choice, error) {
	has := func(v string) bool {
		for _, x := range versions {
			if x == v {
				return true
			}
		}
		return false
	}
	switch {
	case p != nil && envVersion != "" && envVersion != p.Version:
		return choice{}, fmt.Errorf("this knowledge base is pinned to Kent %s (%s), and %s names %s. The pin is "+
			"the version everyone writing here uses, so one person's setting does not override it. Remove "+
			"%s from this project's settings, or set it to %s.", p.Version, pinPath, versionEnv, envVersion,
			versionEnv, p.Version)
	case p != nil:
		if !has(p.Version) {
			return choice{}, missing(fmt.Sprintf("This knowledge base is pinned to Kent %s (%s)", p.Version, pinPath), p.Version, versions, dir)
		}
		return choice{p.Version, "the knowledge base's pin"}, nil
	case envVersion != "":
		if !has(envVersion) {
			return choice{}, missing(fmt.Sprintf("%s names Kent %s", versionEnv, envVersion), envVersion, versions, dir)
		}
		return choice{envVersion, versionEnv}, nil
	case len(versions) == 0:
		return choice{}, missing("No version of Kent is installed", "", nil, dir)
	}
	return choice{versions[0], "the highest version installed"}, nil
}

// installScriptURL is where the install script for a version is published: each release carries its own,
// which installs that release (S-0116). With no version, it is the latest release's.
func installScriptURL(version, script string) string {
	if version == "" {
		return "https://github.com/" + repo + "/releases/latest/download/" + script
	}
	return "https://github.com/" + repo + "/releases/download/v" + version + "/" + script
}

// missing says that a version is not installed and how the human would install it, without inviting an
// agent to do it (S-0108).
func missing(what, version string, versions []string, dir string) error {
	var b strings.Builder
	if version != "" {
		fmt.Fprintf(&b, "%s, which is not installed.", what)
	} else {
		fmt.Fprintf(&b, "%s in %s.", what, filepath.Join(dir, "versions"))
	}
	if len(versions) > 0 {
		fmt.Fprintf(&b, " Installed: %s.", strings.Join(versions, ", "))
	}
	which := "the latest release"
	if version != "" {
		which = "Kent " + version
	}
	script, run := "install.sh", "sh install.sh"
	if runtime.GOOS == "windows" {
		script, run = "install.ps1", `powershell -ExecutionPolicy Bypass -File .\install.ps1`
	}
	fmt.Fprintf(&b, "\n\nHow the human would install it: download the install script for %s,\n\n    %s\n\n"+
		"read it, and run it:\n\n    %s", which, installScriptURL(version, script), run)
	b.WriteString("\n\nNote for agents: tell the human which version is missing. Do not download or install " +
		"it yourself without their agreement.")
	return errors.New(b.String())
}

func main() {
	os.Exit(launch(os.Args[1:]))
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "kb (launcher): "+err.Error())
	return 1
}

func launch(args []string) int {
	// answered before anything else is looked at, so the install scripts can always ask it
	if len(args) == 2 && args[0] == "--launcher" && args[1] == "version" {
		fmt.Println(launcherVersion)
		return 0
	}
	dir, err := installDir()
	if err != nil {
		return fail(err)
	}
	if os.Getenv(launchedEnv) != "" {
		return fail(fmt.Errorf("started from a version of Kent run by the launcher; %s in %s should be a "+
			"version of Kent, not the launcher", exeName(), filepath.Join(dir, "versions")))
	}
	versions := installed(dir)

	p, pinPath, pinErr := pinFor(args)
	c, chooseErr := choose(versions, p, pinPath, strings.TrimSpace(os.Getenv(versionEnv)), dir)
	if pinErr != nil {
		chooseErr = pinErr
	}

	if len(args) > 0 && args[0] == "--launcher" {
		return launcherInfo(args[1:], dir, versions, c, chooseErr)
	}
	exe := filepath.Join(dir, "versions", c.version, exeName())
	env := append(os.Environ(), launchedEnv+"="+c.version+" ("+c.reason+")")
	if len(args) == 1 && args[0] == sessionStartArg {
		return sessionStart(exe, args, env, chooseErr)
	}
	if chooseErr != nil {
		return fail(chooseErr)
	}
	return run(exe, args, env)
}

func launcherInfo(args []string, dir string, versions []string, c choice, chooseErr error) int {
	if len(args) != 0 {
		return fail(errors.New("kb --launcher takes nothing, or \"version\""))
	}
	fmt.Println("launcher: " + filepath.Join(dir, exeName()) + " (launcher version " + launcherVersion + ")")
	if len(versions) == 0 {
		fmt.Println("installed: none")
	} else {
		fmt.Println("installed: " + strings.Join(versions, ", "))
	}
	if chooseErr != nil {
		fmt.Println("would run: nothing")
		fmt.Println()
		fmt.Println(chooseErr.Error())
		return 1
	}
	fmt.Println("would run: " + c.version + ", chosen by " + c.reason)
	return 0
}
