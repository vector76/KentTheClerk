// Command launcher is Kent the Clerk's launcher, installed as kb on the PATH with the installed versions
// beside it (S-0112):
//
//	<install folder>/kb[.exe]                      this launcher
//	<install folder>/versions/<version>/kb[.exe]   each installed version of Kent
//
// It runs the version the knowledge base is pinned to (S-0115); with no pin, the version named by
// KENT_VERSION in the user's settings for the project; with neither, the installed version with the
// highest version number, a development build such as 0.9.0-dev counting as just below 0.9.0. A pin and a
// different KENT_VERSION are refused. A missing version is never installed (S-0108): the launcher says how
// the human would install it.
//
// The launcher is infrastructure rather than part of the system it dispatches to: it is outside the system
// hash and never pinned. Every argument goes to the version it runs, except a first argument of
// --launcher, which asks the launcher itself (kb --launcher, kb --launcher version).
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"kenttheclerk/internal/pin"
)

// launcherVersion is the launcher's own version, which the install scripts compare to replace the
// launcher only with a newer one. It is not Kent's version.
const launcherVersion = "0.1.0"

const (
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

// findKB is where the recorder will look for the knowledge base: KENT_KB, else the current folder or the
// nearest folder above it holding attested/ and evidence/. It mirrors the recorder's own lookup only to find
// the pin; when it finds nothing, the version chosen without a pin reports the problem itself.
func findKB() string {
	isKB := func(p string) bool {
		a, err1 := os.Stat(filepath.Join(p, "attested"))
		e, err2 := os.Stat(filepath.Join(p, "evidence"))
		return err1 == nil && err2 == nil && a.IsDir() && e.IsDir()
	}
	if env := os.Getenv(kbEnv); env != "" {
		if p, err := filepath.Abs(env); err == nil && isKB(p) {
			return p
		}
		return ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for p := cwd; ; {
		if isKB(p) {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
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
	want := version
	if want == "" {
		want = "<version>"
	}
	fmt.Fprintf(&b, "\n\nHow the human would install it: download kent-%s-%s-%s.zip from Kent the Clerk's "+
		"releases on GitHub and unpack it into %s.", want, runtime.GOOS, runtime.GOARCH, dir)
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
	dir, err := installDir()
	if err != nil {
		return fail(err)
	}
	if os.Getenv(launchedEnv) != "" {
		return fail(fmt.Errorf("started from a version of Kent run by the launcher; %s in %s should be a "+
			"version of Kent, not the launcher", exeName(), filepath.Join(dir, "versions")))
	}
	versions := installed(dir)

	var p *pin.Pin
	pinPath := ""
	if root := findKB(); root != "" {
		pinPath = pin.Path(root)
		got, ok, err := pin.Read(root)
		if err != nil {
			return fail(fmt.Errorf("the knowledge base's pin cannot be read: %v", err))
		}
		if ok {
			p = &got
		}
	}
	c, chooseErr := choose(versions, p, pinPath, strings.TrimSpace(os.Getenv(versionEnv)), dir)

	if len(args) > 0 && args[0] == "--launcher" {
		return launcherInfo(args[1:], dir, versions, c, chooseErr)
	}
	if chooseErr != nil {
		return fail(chooseErr)
	}
	exe := filepath.Join(dir, "versions", c.version, exeName())
	env := append(os.Environ(), launchedEnv+"="+c.version+" ("+c.reason+")")
	return run(exe, args, env)
}

func launcherInfo(args []string, dir string, versions []string, c choice, chooseErr error) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(launcherVersion)
		return 0
	}
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
