package kb

// kb session-start and kb setup: how Kent reaches a project in Claude Code. Onboarding is two steps,
// install kb and have the agent run kb setup (S-0126); setup leaves nothing in what the project commits
// (S-0128); Kent's context loads from a session-start hook in the project's local settings (S-0129).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/pin"
)

const (
	sessionStartDoc     = "front-ends/claude-code/session-start.md"
	setupDoc            = "front-ends/claude-code/setup.md"
	sessionStartCommand = "kb session-start"
	settingsRel         = ".claude/settings.local.json"
	// the project's settings for Kent, read from the environment the settings file sets
	askModeEnv     = "KENT_ASK_SESSION_MODE"
	perTurnEnv     = "KENT_STATEMENTS_PER_TURN"
	kentVersionEnv = "KENT_VERSION"
	// the hook's output is cut off beyond this many characters (Claude Code's limit)
	sessionStartLimit = 10000
)

// ---------------------------------------------------------------- the project's settings

// askModeSetting describes KENT_ASK_SESSION_MODE: when to ask about the session mode (Kent Rule 12).
func askModeSetting(v string) (text string, ok bool) {
	switch strings.TrimSpace(v) {
	case "", "when-needed":
		return "when statements worth recording come up", true
	case "up-front":
		return "up front. If the human has not said which mode this conversation is in, ask before doing anything else", true
	}
	return "", false
}

// perTurnSetting describes KENT_STATEMENTS_PER_TURN: how many statements to present per turn (Kent Rule 24).
func perTurnSetting(v string) (text string, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "1" {
		return "one at a time", true
	}
	if n, err := strconv.Atoi(v); err == nil && n > 1 {
		return fmt.Sprintf("up to %d", n), true
	}
	return "", false
}

// settingLines are the project's settings as the agent reads them, and any value kb does not understand.
func settingLines(env func(string) string) (lines, problems []string) {
	ask, ok := askModeSetting(env(askModeEnv))
	if !ok {
		problems = append(problems, fmt.Sprintf("%s is %q, which kb does not understand (up-front or when-needed); "+
			"asking when statements worth recording come up", askModeEnv, env(askModeEnv)))
		ask, _ = askModeSetting("")
	}
	per, ok := perTurnSetting(env(perTurnEnv))
	if !ok {
		problems = append(problems, fmt.Sprintf("%s is %q, which kb does not understand (a whole number from 1); "+
			"presenting one at a time", perTurnEnv, env(perTurnEnv)))
		per, _ = perTurnSetting("")
	}
	lines = []string{
		"Ask about the session mode: " + ask + ".",
		"Statements to present per turn: " + per + ".",
	}
	return lines, problems
}

// ---------------------------------------------------------------- kb session-start

// runSessionStart prints what the agent needs before anything else, for the session-start hook. It
// never fails: an error here would leave the agent without its instructions and without knowing why.
func runSessionStart(ns argparse.Namespace) (code int) {
	defer func() {
		if r := recover(); r != nil {
			why := "see the message above"
			if _, ok := r.(exitCode); !ok {
				why = fmt.Sprint(r)
			}
			println("")
			println("kb session-start could not finish (" + why + "). Until that is fixed, write nothing to the " +
				"knowledge base. Tell the human when the conversation starts, and offer to run `kb setup`, which " +
				"finds out what is wrong.")
			code = 0
		}
	}()
	root, how := findKBRoot()
	if root == "" {
		println("# Knowledge base")
		println("")
		println("Kent the Clerk's session-start hook ran in this project, but kb found no knowledge base: " + how +
			". Nothing in this conversation can be recorded until that is fixed. Tell the human when the " +
			"conversation starts, and offer to run `kb setup`, which finds out what is wrong and walks through " +
			"fixing it.")
		return 0
	}
	fmt.Fprint(Stdout, readDoc(sessionStartDoc))
	println("")
	println("## This project")
	println("")
	println(fmt.Sprintf("- Knowledge base: %s (%s).", root, how))
	launched := os.Getenv("KENT_LAUNCHED")
	if launched == "" {
		launched = kent.Version() + ", run directly rather than by the launcher"
	}
	println("- Kent: " + launched + ".")
	var problems []string
	if p, ok, err := pin.Read(root); err != nil {
		problems = append(problems, fmt.Sprintf("the knowledge base's pin cannot be read: %v", err))
	} else if ok {
		println("- Pinned to " + p.String() + ".")
	} else {
		println("- Not pinned: any version of Kent writes here.")
	}
	if u, source := userSource("", root); u != "" {
		println(fmt.Sprintf("- The human is recorded as %s (%s).", u, source))
	} else {
		problems = append(problems, "no user id is set, so kb cannot record anything the human does")
	}
	lines, bad := settingLines(os.Getenv)
	for _, l := range lines {
		println("- " + l)
	}
	problems = append(problems, bad...)
	if len(problems) > 0 {
		println("")
		println("Not set up right, so tell the human when the conversation starts, and offer to run `kb setup`:")
		println("")
		for _, p := range problems {
			println("- " + p + ".")
		}
	}
	return 0
}

// ---------------------------------------------------------------- kb setup: what it finds

// permPath is a folder in the form Claude Code's permission rules take: //c/work/kb for C:\work\kb,
// //home/you/kb for /home/you/kb. The rules expand no variables, so the path is written out in full.
func permPath(p string) string {
	s := strings.TrimRight(filepath.ToSlash(p), "/")
	if len(s) >= 2 && s[1] == ':' {
		return "//" + strings.ToLower(s[:1]) + s[2:]
	}
	return "/" + s
}

// settingPath is a folder as the settings file's env and additionalDirectories hold it.
func settingPath(p string) string { return filepath.ToSlash(p) }

// samePath compares two folders as the file system would.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// kentAllow, kentDeny and kentHook are what Kent needs in a project's settings for the knowledge base at kb.
func kentAllow(kb string) []string {
	return []string{"Bash(kb)", "Bash(kb *)", "PowerShell(kb)", "PowerShell(kb *)",
		"Edit(" + permPath(kb) + "/off-record/drafts/**)"}
}

func kentDeny(kb string) []string {
	pp := permPath(kb)
	return []string{"Edit(" + pp + "/attested/**)", "Edit(" + pp + "/evidence/**)", "Edit(" + pp + "/agent/**)"}
}

// kentSettingsJSON is the part of .claude/settings.local.json that Kent needs, to be merged into the file.
func kentSettingsJSON(kb string, extraEnv [][2]string) string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	list := func(xs []string, indent string) string {
		var parts []string
		for _, x := range xs {
			parts = append(parts, indent+q(x))
		}
		return strings.Join(parts, ",\n")
	}
	var env []string
	env = append(env, "    "+q(kbEnv)+": "+q(settingPath(kb)))
	for _, kv := range extraEnv {
		env = append(env, "    "+q(kv[0])+": "+q(kv[1]))
	}
	return "{\n" +
		"  \"env\": {\n" + strings.Join(env, ",\n") + "\n  },\n" +
		"  \"permissions\": {\n" +
		"    \"additionalDirectories\": [\n" + list([]string{settingPath(kb)}, "      ") + "\n    ],\n" +
		"    \"allow\": [\n" + list(kentAllow(kb), "      ") + "\n    ],\n" +
		"    \"deny\": [\n" + list(kentDeny(kb), "      ") + "\n    ]\n" +
		"  },\n" +
		"  \"hooks\": {\n" +
		"    \"SessionStart\": [\n" +
		"      { \"hooks\": [ { \"type\": \"command\", \"command\": " + q(sessionStartCommand) + " } ] }\n" +
		"    ]\n" +
		"  }\n" +
		"}"
}

// git runs git in dir and returns its trimmed output and exit status (-1 if git could not be run).
func git(dir string, args ...string) (string, int) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return strings.TrimSpace(string(out)), ee.ExitCode()
		}
		return "", -1
	}
	return strings.TrimSpace(string(out)), 0
}

// settingsFile is a project's .claude/settings.local.json as read for setup.
type settingsFile struct {
	path    string
	exists  bool
	err     error
	data    map[string]any
	allow   []string
	deny    []string
	dirs    []string
	env     map[string]string
	hasHook bool
}

func readSettings(project string) settingsFile {
	sf := settingsFile{path: filepath.Join(project, filepath.FromSlash(settingsRel)), env: map[string]string{}}
	b, err := os.ReadFile(sf.path)
	if err != nil {
		if !os.IsNotExist(err) {
			sf.exists, sf.err = true, err
		}
		return sf
	}
	sf.exists = true
	if err := json.Unmarshal(b, &sf.data); err != nil {
		sf.err = err
		return sf
	}
	strs := func(v any) []string {
		var out []string
		if xs, ok := v.([]any); ok {
			for _, x := range xs {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	}
	if env, ok := sf.data["env"].(map[string]any); ok {
		for k, v := range env {
			if s, ok := v.(string); ok {
				sf.env[k] = s
			}
		}
	}
	if perms, ok := sf.data["permissions"].(map[string]any); ok {
		sf.allow, sf.deny, sf.dirs = strs(perms["allow"]), strs(perms["deny"]), strs(perms["additionalDirectories"])
	}
	if hooks, ok := sf.data["hooks"].(map[string]any); ok {
		groups, _ := hooks["SessionStart"].([]any)
		for _, g := range groups {
			gm, _ := g.(map[string]any)
			hs, _ := gm["hooks"].([]any)
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				if c, _ := hm["command"].(string); strings.TrimSpace(c) == sessionStartCommand {
					sf.hasHook = true
				}
			}
		}
	}
	return sf
}

func missingFrom(have, want []string) []string {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	var out []string
	for _, w := range want {
		if !set[w] {
			out = append(out, w)
		}
	}
	return out
}

// globalIgnoreLine is the line Claude Code adds to the global git ignore file when it creates a project's
// settings.local.json, so that no project commits one.
const globalIgnoreLine = "**/.claude/settings.local.json"

// globalIgnorePath is the user's global git ignore file: core.excludesFile, else git's default.
func globalIgnorePath(dir string) string {
	if p, st := git(dir, "config", "--global", "--get", "core.excludesFile"); st == 0 && p != "" {
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			p = filepath.Join(home, p[2:])
		}
		return filepath.Clean(p)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git", "ignore")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "git", "ignore")
}

// oldBlockFiles are the project's CLAUDE.md files that still hold the block earlier versions of Kent had
// pasted in, which the session-start hook replaces (S-0128, S-0129).
func oldBlockFiles(project string) []string {
	var out []string
	for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md", "CLAUDE.local.md"} {
		b, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		s := string(b)
		if strings.Contains(s, "kb rules introduction") || strings.Contains(s, "(Kent Rule 1)") {
			out = append(out, name)
		}
	}
	return out
}

// installedVersions are the versions of Kent installed beside the launcher, when this one was installed there.
func installedVersions() []string {
	dir := productDir()
	if filepath.Base(filepath.Dir(dir)) != "versions" {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func countFiles(dir, pattern string) int {
	m, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(m)
}

func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

// report collects what setup finds, in sections, and what is left to do.
type report struct {
	lines []string
	todo  []todoItem
}

// todoItem is something left to do, with its place in the runbook's order: 0 before anything else, then the
// knowledge base (1 choosing, 2 creating, 3 git), the user id (4), and the code project (5 settings, 6 git
// ignore, 7 a committed settings file, 8 an old CLAUDE.md block, 9 the project's settings).
type todoItem struct {
	rank int
	text string
}

func (r *report) section(title string) { r.lines = append(r.lines, "", "## "+title, "") }
func (r *report) item(format string, a ...any) {
	r.lines = append(r.lines, "- "+fmt.Sprintf(format, a...))
}
func (r *report) need(rank int, format string, a ...any) {
	r.todo = append(r.todo, todoItem{rank, fmt.Sprintf(format, a...)})
}

// todos are the things left to do in the runbook's order.
func (r *report) todos() []string {
	sort.SliceStable(r.todo, func(i, j int) bool { return r.todo[i].rank < r.todo[j].rank })
	var out []string
	for _, t := range r.todo {
		out = append(out, t.text)
	}
	return out
}

// chooseKB is the knowledge base setup is about: --kb, else KENT_KB, else the one the project's settings
// name (they apply to a running conversation's commands, but setup may be checking a file just written).
func chooseKB(arg string, sf settingsFile) (path, how string) {
	switch {
	case arg != "":
		return absPath(arg), "from --kb"
	case os.Getenv(kbEnv) != "":
		return absPath(os.Getenv(kbEnv)), "from " + kbEnv
	case sf.env[kbEnv] != "":
		return absPath(sf.env[kbEnv]), "from " + kbEnv + " in " + settingsRel
	}
	return "", ""
}

func buildReport(project, kbArg string) report {
	var r report
	sf := readSettings(project)
	kb, kbHow := chooseKB(kbArg, sf)

	r.section("Kent")
	launched := os.Getenv("KENT_LAUNCHED")
	if launched == "" {
		launched = kent.Version() + ", run directly rather than by the launcher"
	}
	r.item("Running: %s.", launched)
	if vs := installedVersions(); len(vs) > 0 {
		r.item("Installed: %s.", strings.Join(vs, ", "))
	}

	r.section("Code project: " + project)
	top, status := git(project, "rev-parse", "--show-toplevel")
	inGit := status == 0
	switch {
	case status == -1:
		r.item("git could not be run, so nothing below that depends on git was checked.")
	case !inGit:
		r.item("Not in a git repository.")
	case samePath(top, project):
		r.item("A git repository.")
	default:
		r.item("Inside the git repository at %s. Settings belong in the folder Claude Code is opened in; "+
			"if that is %s, run kb setup there.", top, top)
	}
	switch {
	case !sf.exists:
		r.item("%s does not exist yet.", settingsRel)
	case sf.err != nil:
		r.item("%s cannot be read as JSON: %v.", settingsRel, sf.err)
		r.need(0, "Fix %s, which is not valid JSON, before anything else: Claude Code ignores a settings file it "+
			"cannot parse.", settingsRel)
	default:
		r.item("%s exists.", settingsRel)
	}
	if sf.exists && sf.err == nil {
		if v := sf.env[kbEnv]; v != "" {
			r.item("It sets %s to %s.", kbEnv, v)
		} else {
			r.item("It does not set %s.", kbEnv)
		}
		if sf.hasHook {
			r.item("It has the session-start hook (%s).", sessionStartCommand)
		} else {
			r.item("It has no session-start hook running %s.", sessionStartCommand)
		}
		for _, k := range []string{askModeEnv, perTurnEnv, kentVersionEnv} {
			if v, ok := sf.env[k]; ok {
				r.item("It sets %s to %q.", k, v)
			}
		}
		if _, bad := settingLines(func(k string) string { return sf.env[k] }); len(bad) > 0 {
			for _, b := range bad {
				r.need(9, "In %s, %s.", settingsRel, b)
			}
		}
	}
	if inGit && sf.exists {
		if _, st := git(project, "ls-files", "--error-unmatch", settingsRel); st == 0 {
			r.item("%s is committed to git, so it is shared and Claude Code holds back its hooks until the "+
				"folder is trusted.", settingsRel)
			r.need(7, "%s is committed to git. It holds per-user paths and must not be: ask the human before "+
				"untracking it (git rm --cached keeps the file).", settingsRel)
		}
	}
	if inGit {
		by, st := git(project, "check-ignore", "-v", settingsRel)
		if st == 0 {
			if i := strings.Index(by, "\t"); i > 0 {
				by = by[:i]
			}
			r.item("git ignores %s (%s).", settingsRel, by)
		} else {
			r.item("git does not ignore %s.", settingsRel)
			r.need(6, "Keep %s out of git without a committed change. Offer to add the line %s to the global "+
				"git ignore file, %s, as Claude Code itself does; it keeps the file out of every project. If the "+
				"human declines, give them the line /%s for .git/info/exclude to add themselves: Claude Code does "+
				"not let the agent write in .git/.", settingsRel, globalIgnoreLine, globalIgnorePath(project), settingsRel)
		}
	}
	if files := oldBlockFiles(project); len(files) > 0 {
		r.item("%s still holds the knowledge base block from an earlier version of Kent.", strings.Join(files, " and "))
		r.need(8, "Offer to remove the knowledge base block from %s: the session-start hook now gives the agent "+
			"those lines, and the project's settings move into %s.", strings.Join(files, " and "), settingsRel)
	}

	r.section("Knowledge base")
	if kb == "" {
		r.item("None chosen: %s is not set, and no --kb was given.", kbEnv)
		r.need(1, "Ask the human whether this project uses a new knowledge base or an existing one, and where it " +
			"is or should go; then run kb setup --report --kb <folder>.")
	} else {
		r.item("%s (%s).", kb, kbHow)
		kbReport(&r, kb)
		if sf.exists && sf.err == nil {
			if v := sf.env[kbEnv]; v != "" && !samePath(absPath(v), kb) {
				r.need(1, "%s names %s, not %s. A project refers to at most one knowledge base; "+
					"ask the human which one this project uses.", settingsRel, v, kb)
			}
		}
	}

	if kb != "" {
		r.section("Settings for this project")
		var extra [][2]string
		for _, k := range []string{askModeEnv, perTurnEnv} {
			if v, ok := sf.env[k]; ok {
				extra = append(extra, [2]string{k, v})
			}
		}
		var missing []string
		if sf.err == nil {
			if !samePath(absPath(sf.env[kbEnv]), kb) || sf.env[kbEnv] == "" {
				missing = append(missing, kbEnv)
			}
			found := false
			for _, d := range sf.dirs {
				if samePath(absPath(d), kb) {
					found = true
				}
			}
			if !found {
				missing = append(missing, "the knowledge base in additionalDirectories")
			}
			for _, a := range missingFrom(sf.allow, kentAllow(kb)) {
				missing = append(missing, "allow "+a)
			}
			for _, d := range missingFrom(sf.deny, kentDeny(kb)) {
				missing = append(missing, "deny "+d)
			}
			if !sf.hasHook {
				missing = append(missing, "the session-start hook")
			}
		}
		if sf.err == nil && len(missing) == 0 {
			r.item("%s has everything Kent needs.", settingsRel)
		} else {
			r.lines = append(r.lines, "What Kent needs in "+settingsRel+", to merge into what is there:", "",
				"```json", kentSettingsJSON(kb, extra), "```")
			if len(missing) > 0 {
				r.lines = append(r.lines, "", "Missing from the file now:")
				for _, m := range missing {
					r.lines = append(r.lines, "- "+m)
				}
				r.need(5, "Merge what Kent needs into %s, as the runbook says; what is missing is listed above.", settingsRel)
			}
		}
	}
	return r
}

// kbReport describes the knowledge base folder: whether it is one, its git repository, its pin and who
// would be recorded.
func kbReport(r *report, kb string) {
	st, err := os.Stat(kb)
	switch {
	case err != nil:
		r.item("The folder does not exist yet.")
		r.need(2, "If the human wants a new knowledge base at %s, create it with kb setup --create %s; if they "+
			"meant an existing one, find where it is (or clone it, with their agreement).", kb, kb)
		return
	case !st.IsDir():
		r.item("It is a file, not a folder.")
		r.need(1, "%s is a file; ask the human where the knowledge base is.", kb)
		return
	case !isKBFolder(kb):
		if isEmptyDir(kb) {
			r.item("An empty folder, not a knowledge base yet.")
			r.need(2, "If the human wants a new knowledge base in %s, create it with kb setup --create %s.", kb, kb)
		} else {
			r.item("A folder with files in it, but no attested/ and evidence/ folders, so not a knowledge base.")
			r.need(1, "%s is not a knowledge base and not empty: ask the human whether it is the right folder.", kb)
		}
		return
	}
	r.item("A knowledge base: %d attested statement(s), %d agent statement(s), %d evidence file(s).",
		countFiles(filepath.Join(kb, "attested"), "S-*.md"), countFiles(filepath.Join(kb, "agent"), "A-*.md"),
		countFiles(filepath.Join(kb, "evidence"), "*.jsonl"))

	top, status := git(kb, "rev-parse", "--show-toplevel")
	switch {
	case status == -1:
		r.item("git could not be run, so its repository was not checked.")
	case status != 0:
		r.item("Not in a git repository.")
		r.need(3, "Ask the human whether to keep the knowledge base in git; if so, kb setup --git --kb %s.", kb)
	default:
		if samePath(top, kb) {
			r.item("Its own git repository.")
		} else {
			r.item("Inside the git repository at %s.", top)
		}
		if url, st := git(kb, "remote", "get-url", "origin"); st == 0 && url != "" {
			r.item("Its remote origin is %s.", url)
		} else {
			r.item("No remote named origin.")
		}
		var unignored []string
		for _, p := range []string{"off-record/x", "index.db", "render/x"} {
			if _, st := git(kb, "check-ignore", "-q", p); st != 0 {
				unignored = append(unignored, strings.TrimSuffix(p, "x"))
			}
		}
		if len(unignored) > 0 {
			r.item("git does not ignore %s.", strings.Join(unignored, ", "))
			r.need(3, "git must ignore %s in the knowledge base: off-record material is outside the record, and "+
				"index.db and render/ are rebuilt from it. kb setup --git --kb %s adds them to its .gitignore.",
				strings.Join(unignored, ", "), kb)
		} else {
			r.item("git ignores off-record/, index.db and render/.")
		}
		for _, d := range []string{"attested", "evidence"} {
			if isEmptyDir(filepath.Join(kb, d)) {
				r.need(3, "%s/ is empty, and git keeps no empty folder, so a clone would not be a knowledge base: "+
					"kb setup --git --kb %s adds %s/.gitkeep.", d, kb, d)
			}
		}
	}

	if p, ok, err := pin.Read(kb); err != nil {
		r.item("Its pin cannot be read: %v.", err)
		r.need(1, "The knowledge base's pin cannot be read; show the human %s.", pin.Path(kb))
	} else if ok {
		r.item("Pinned to %s.", p)
		installed := false
		for _, v := range installedVersions() {
			if v == p.Version {
				installed = true
			}
		}
		if !installed && p.Version != kent.Version() {
			r.need(1, "The knowledge base is pinned to Kent %s, which is not installed. The human installs it from "+
				"that version's release; do not install it yourself.", p.Version)
		}
	} else {
		r.item("Not pinned.")
	}

	if u, source := userSource("", kb); u != "" {
		r.item("The human would be recorded as %s (%s).", u, source)
	} else {
		r.item("No user id: kb could not record anything the human does.")
		r.need(4, `Set a user id: git's user.email, or ~/.kenttheclerk.json with {"user": "<id>"}; ask the human which.`)
	}
}

// ---------------------------------------------------------------- kb setup

// createKB makes a new knowledge base in dir: the folder and its attested/ and evidence/ folders, which is
// how kb finds a knowledge base. Whether it goes in git is a separate question (kb setup --git).
func createKB(dir string) {
	if st, err := os.Stat(dir); err == nil {
		switch {
		case !st.IsDir():
			die(dir + " is a file")
		case isKBFolder(dir):
			die(dir + " is already a knowledge base")
		case !isEmptyDir(dir):
			die(dir + " is not empty; a new knowledge base goes in a new or empty folder")
		}
	}
	for _, sub := range []string{"attested", "evidence"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o777); err != nil {
			die(fmt.Sprintf("cannot create %s: %v", filepath.Join(dir, sub), err))
		}
	}
	println("created the knowledge base " + dir + ", with attested/ and evidence/")
}

// kbIgnores are what git must ignore in a knowledge base, each with a path that shows whether it does.
var kbIgnores = [][2]string{{"off-record/", "off-record/x"}, {"index.db", "index.db"},
	{"index.db-journal", "index.db-journal"}, {"render/", "render/x"}}

// gitKB keeps a knowledge base in git: a repository if none holds the folder, the .gitignore lines it needs,
// and a .gitkeep in an empty attested/ or evidence/ so that a clone is a knowledge base too. It never commits.
func gitKB(kb string) {
	if !isKBFolder(kb) {
		die(kb + " is not a knowledge base (no attested/ and evidence/ folders)")
	}
	top, st := git(kb, "rev-parse", "--show-toplevel")
	switch {
	case st == -1:
		die("git could not be run; is it installed and on the PATH?")
	case st != 0:
		if out, st := git(kb, "init", "-q"); st != 0 {
			die("git init failed: " + out)
		}
		println("made " + kb + " a git repository")
	default:
		println("already in the git repository at " + top)
	}
	var add []string
	for _, ig := range kbIgnores {
		if _, st := git(kb, "check-ignore", "-q", ig[1]); st != 0 {
			add = append(add, ig[0])
		}
	}
	if len(add) > 0 {
		path := filepath.Join(kb, ".gitignore")
		old, _ := os.ReadFile(path)
		text := string(old)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "# Kent: off-record material is outside the record; index.db and render/ are rebuilt from it\n" +
			strings.Join(add, "\n") + "\n"
		if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
			die(fmt.Sprintf("cannot write %s: %v", path, err))
		}
		println("added " + strings.Join(add, ", ") + " to " + path)
	}
	for _, d := range []string{"attested", "evidence"} {
		if isEmptyDir(filepath.Join(kb, d)) {
			keep := filepath.Join(kb, d, ".gitkeep")
			if err := os.WriteFile(keep, nil, 0o666); err != nil {
				die(fmt.Sprintf("cannot write %s: %v", keep, err))
			}
			println("added " + keep)
		}
	}
	println("nothing was committed; kb never commits")
}

func runSetup(ns argparse.Namespace) int {
	project, _ := ns.Str("project")
	if project == "" {
		project = os.Getenv("CLAUDE_PROJECT_DIR")
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	project = absPath(project)
	kbArg, _ := ns.Str("kb")
	create, _ := ns.Str("create")
	acted := false
	if create != "" {
		if kbArg != "" && !samePath(absPath(kbArg), absPath(create)) {
			die("--create and --kb name different folders")
		}
		kbArg = absPath(create)
		createKB(kbArg)
		acted = true
	}
	if ns.Bool("git") {
		kb, _ := chooseKB(kbArg, readSettings(project))
		if kb == "" {
			die("which knowledge base? give --kb <folder>")
		}
		gitKB(kb)
		acted = true
	}
	if acted {
		println("")
	} else if !ns.Bool("report") {
		fmt.Fprint(Stdout, readDoc(setupDoc))
		println("")
	}
	println("# What kb setup found")
	r := buildReport(project, kbArg)
	for _, l := range r.lines {
		println(l)
	}
	println("")
	println("## Left to do")
	println("")
	todo := r.todos()
	if len(todo) == 0 {
		println("Nothing: this project is set up. Run kb session-start to see what the agent is given.")
	} else {
		for i, t := range todo {
			println(fmt.Sprintf("%d. %s", i+1, t))
		}
	}
	return 0
}

func addSetupParsers(sub *argparse.Parser) {
	p := sub.AddParser("setup", "set Kent up in this project: a runbook for the agent, and what is there now")
	p.Option("--kb", "the knowledge base's folder, when it is not yet in the project's settings", nil, false)
	p.Option("--project", "the code project's folder (default: CLAUDE_PROJECT_DIR, else the current folder)", nil, false)
	p.Flag("--report", "print only what setup finds, without the runbook")
	p.Option("--create", "create a new knowledge base in this folder (new or empty), then report", nil, false)
	p.Flag("--git", "keep the knowledge base in git: git init if needed, its .gitignore and .gitkeep files; never commits")
	p.Run = runSetup

	s := sub.AddParser("session-start", "print what the agent reads first, for the project's session-start hook")
	s.Run = runSessionStart
}
