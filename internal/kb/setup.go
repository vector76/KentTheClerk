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
	"unicode/utf8"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
	"kenttheclerk/internal/kbset"
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
	offerCommitEnv = "KENT_OFFER_COMMIT"
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
	roots, how, problem := findKBRoots()
	if problem != "" {
		println("# Knowledge base")
		println("")
		println("Kent the Clerk's session-start hook ran in this project, but kb found no knowledge base: " + problem +
			". Nothing in this conversation can be recorded until that is fixed. Tell the human when the " +
			"conversation starts, and offer to run `kb setup`, which finds out what is wrong and walks through " +
			"fixing it.")
		return 0
	}
	fmt.Fprint(Stdout, readDoc(sessionStartDoc))
	println("")
	println("## This project")
	println("")
	var problems []string
	if len(roots) > 1 {
		println(fmt.Sprintf("This project refers to %d knowledge bases (%s), each below. A session is on the record "+
			"in at most one of them (%s). When this one goes on the record, ask the human which, and pass "+
			"`--kb <folder or id>` to the first `kb snapshot`; after that kb finds it from the session. Any other "+
			"command that is not about a session's turns takes `--kb` too. The others may be read and cited: a "+
			"statement in another knowledge base is written `<its id>/<statement id>` (%s).",
			len(roots), how, rule("one-knowledge-base"), rule("other-knowledge-base")))
		if pinsDiffer(roots) {
			println("")
			println("Their pins differ, so pass `--kb` on every command, even one about a session's turns: the " +
				"launcher runs the version of Kent the knowledge base it names is pinned to.")
		}
		println("")
	}
	launched := os.Getenv("KENT_LAUNCHED")
	if launched == "" {
		launched = kent.Version() + ", run directly rather than by the launcher"
	}
	lines, bad := settingLines(os.Getenv)
	problems = append(problems, bad...)
	if len(roots) == 1 {
		// one knowledge base reads as it did before there could be several
		println(fmt.Sprintf("- Knowledge base: %s (%s).", roots[0], how))
		println("- Kent: " + launched + ".")
		problems = append(problems, describeKB(roots[0])...)
		for _, l := range lines {
			println("- " + l)
		}
	} else {
		println("- Kent: " + launched + ".")
		for _, l := range lines {
			println("- " + l)
		}
		for _, root := range roots {
			println("")
			println("### " + root)
			println("")
			for _, p := range describeKB(root) {
				problems = append(problems, "in "+root+" (pass --kb "+root+"): "+p)
			}
		}
	}
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

// pinsDiffer is whether the knowledge bases are pinned to different versions, or some pinned and some not.
func pinsDiffer(roots []string) bool {
	first := ""
	for i, r := range roots {
		p, ok, _ := pin.Read(r)
		v := ""
		if ok {
			v = p.String()
		}
		if i == 0 {
			first = v
		} else if v != first {
			return true
		}
	}
	return false
}

// describeKB prints the session-start lines about one knowledge base: its id, pin, git and people. It
// returns what is not set up right.
func describeKB(root string) (problems []string) {
	if id, ok, err := kbset.ReadID(root); err != nil {
		problems = append(problems, fmt.Sprintf("the knowledge base's id cannot be read: %v", err))
	} else if ok {
		println("- Id: " + id + "; a reference from another knowledge base to a statement here is written " + id + "/<statement id>.")
	}
	if p, ok, err := pin.Read(root); err != nil {
		problems = append(problems, fmt.Sprintf("the knowledge base's pin cannot be read: %v", err))
	} else if ok {
		println("- Pinned to " + p.String() + ".")
	} else {
		println("- Not pinned: any version of Kent writes here.")
	}
	gl, gitProblems := gitLines(root, os.Getenv)
	for _, l := range gl {
		println("- " + l)
	}
	problems = append(problems, gitProblems...)
	people, problem := describeUsers(root)
	for _, l := range people {
		if strings.HasPrefix(l, "  ") {
			println(l)
		} else {
			println("- " + l)
		}
	}
	if problem != "" {
		problems = append(problems, problem)
	}
	return problems
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

func settingPaths(ps []string) []string {
	var out []string
	for _, p := range ps {
		out = append(out, settingPath(p))
	}
	return out
}

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
// For a knowledge base in a repository of its own that includes git in its folder, since the agent commits
// it on every act (S-7BWA), in the one form the session-start text tells the agent to use.
func kentAllow(kb string) []string {
	allow := []string{"Bash(kb)", "Bash(kb *)", "PowerShell(kb)", "PowerShell(kb *)",
		"Edit(" + permPath(kb) + "/off-record/drafts/**)"}
	if readGitPlace(kb).ownRepo() {
		for _, c := range gitAllowed(kb) {
			allow = append(allow, "Bash("+c+")", "PowerShell("+c+")")
		}
	}
	return allow
}

// kentDeny keeps the agent's edit tools off every file Kent Rule 1 says only kb writes: the record's
// folders, the override log, and the user table and its log. It also keeps the agent from running
// kb merge-driver, which git runs itself during a merge.
func kentDeny(kb string) []string {
	pp := permPath(kb)
	return []string{"Edit(" + pp + "/attested/**)", "Edit(" + pp + "/evidence/**)", "Edit(" + pp + "/agent/**)",
		"Edit(" + pp + "/" + overrideLogName + ")", "Edit(" + pp + "/" + usersName + ")", "Edit(" + pp + "/" + usersLogName + ")",
		"Bash(kb merge-driver)", "Bash(kb merge-driver *)", "PowerShell(kb merge-driver)", "PowerShell(kb merge-driver *)"}
}

// kbListValue is KENT_KB's value for the knowledge bases kbs, separated as PATH separates folders.
func kbListValue(kbs []string) string {
	var parts []string
	for _, kb := range kbs {
		parts = append(parts, settingPath(kb))
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// allAllow and allDeny are kentAllow and kentDeny over several knowledge bases, each rule once.
func allAllow(kbs []string) []string {
	var out []string
	for _, kb := range kbs {
		out = append(out, missingFrom(out, kentAllow(kb))...)
	}
	return out
}

func allDeny(kbs []string) []string {
	var out []string
	for _, kb := range kbs {
		out = append(out, missingFrom(out, kentDeny(kb))...)
	}
	return out
}

// kentSettingsJSON is the part of .claude/settings.local.json that Kent needs for the knowledge bases kbs,
// to be merged into the file.
func kentSettingsJSON(kbs []string, extraEnv [][2]string) string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	list := func(xs []string, indent string) string {
		var parts []string
		for _, x := range xs {
			parts = append(parts, indent+q(x))
		}
		return strings.Join(parts, ",\n")
	}
	var env []string
	env = append(env, "    "+q(kbEnv)+": "+q(kbListValue(kbs)))
	for _, kv := range extraEnv {
		env = append(env, "    "+q(kv[0])+": "+q(kv[1]))
	}
	return "{\n" +
		"  \"env\": {\n" + strings.Join(env, ",\n") + "\n  },\n" +
		"  \"permissions\": {\n" +
		"    \"additionalDirectories\": [\n" + list(settingPaths(kbs), "      ") + "\n    ],\n" +
		"    \"allow\": [\n" + list(allAllow(kbs), "      ") + "\n    ],\n" +
		"    \"deny\": [\n" + list(allDeny(kbs), "      ") + "\n    ]\n" +
		"  },\n" +
		"  \"hooks\": {\n" +
		"    \"SessionStart\": [\n" +
		"      { \"hooks\": [ { \"type\": \"command\", \"command\": " + q(sessionStartCommand) + " } ] }\n" +
		"    ]\n" +
		"  }\n" +
		"}"
}

// unionFiles are the files git merges by keeping both copies' lines (S-8MW2): the two logs, whose entries
// are independent lines read in the order made, and the user table, whose rows are order-free. Where both
// copies changed the same row, both lines stay, and kb check reports the key or email twice.
var unionFiles = []string{overrideLogName, usersLogName, usersName}

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

// kbChoice is one knowledge base setup is about, and where it was named.
type kbChoice struct{ path, how string }

// chooseKBs are the knowledge bases setup is about: those KENT_KB names, else those the project's settings
// name (they apply to a running conversation's commands, but setup may be checking a file just written),
// and the one --kb names when it is not among them, which the project is to refer to as well (S-ZBVAHS).
func chooseKBs(arg string, sf settingsFile) []kbChoice {
	// resolved as kb finds the knowledge base when it runs, so that the paths setup writes into the
	// permissions are the ones the session-start text gives
	var out []kbChoice
	switch {
	case os.Getenv(kbEnv) != "":
		for _, f := range kbset.SplitList(os.Getenv(kbEnv)) {
			out = append(out, kbChoice{dyn.Resolve(f), "from " + kbEnv})
		}
	case sf.env[kbEnv] != "":
		for _, f := range kbset.SplitList(sf.env[kbEnv]) {
			out = append(out, kbChoice{dyn.Resolve(f), "from " + kbEnv + " in " + settingsRel})
		}
	}
	if arg != "" {
		p := dyn.Resolve(arg)
		if kbset.IDRE.MatchString(strings.ToUpper(strings.TrimSpace(arg))) {
			var have []string
			for _, c := range out {
				have = append(have, c.path)
			}
			if f, _ := kbset.Locate(arg, have); f != "" {
				p = dyn.Resolve(f)
			}
		}
		for _, c := range out {
			if samePath(c.path, p) {
				return out
			}
		}
		how := "from --kb"
		if len(out) > 0 {
			how = "from --kb, beside the knowledge base(s) " + kbEnv + " names"
		}
		out = append(out, kbChoice{p, how})
	}
	return out
}

// choicePaths are the folders of choices.
func choicePaths(cs []kbChoice) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.path)
	}
	return out
}

func buildReport(project, kbArg string) report {
	var r report
	sf := readSettings(project)
	choices := chooseKBs(kbArg, sf)
	kbs := choicePaths(choices)

	r.section("Kent")
	launched := os.Getenv("KENT_LAUNCHED")
	if launched == "" {
		launched = kent.Version() + ", run directly rather than by the launcher"
	}
	r.item("Running: %s.", launched)
	if vs := installedVersions(); len(vs) > 0 {
		r.item("Installed: %s.", strings.Join(vs, ", "))
	}
	if len(kbs) == 0 {
		// the id that does not depend on the knowledge base, so setup does not ask for one already set
		if u, source := userSource("", ""); u != "" {
			r.item("The human would be recorded as %s (%s), unless ~/.kenttheclerk.json names another id "+
				"for the knowledge base's folder.", u, source)
		} else {
			r.item("No user id in KB_USER or ~/.kenttheclerk.json; git's user.email in the knowledge base's " +
				"folder may give one once it is chosen.")
		}
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
		for _, k := range []string{askModeEnv, perTurnEnv, offerCommitEnv, kentVersionEnv} {
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
			// .git/info/exclude anchors a leading / at the repository's top, so the line names the file from
			// there, through the project's folder when it is a subfolder of the repository
			prefix, _ := git(project, "rev-parse", "--show-prefix")
			prefix = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(prefix)
			r.need(6, "Keep %s out of git without a committed change. Offer to add the line %s to the global "+
				"git ignore file, %s, as Claude Code itself does; it keeps the file out of every project. If the "+
				"human declines, give them the line /%s%s for .git/info/exclude to add themselves: Claude Code does "+
				"not let the agent write in .git/.", settingsRel, globalIgnoreLine, globalIgnorePath(project), prefix,
				settingsRel)
		}
	}
	if files := oldBlockFiles(project); len(files) > 0 {
		r.item("%s still holds the knowledge base block from an earlier version of Kent.", strings.Join(files, " and "))
		r.need(8, "Offer to remove the knowledge base block from %s: the session-start hook now gives the agent "+
			"those lines, and the project's settings move into %s.", strings.Join(files, " and "), settingsRel)
	}

	if len(kbs) == 0 {
		r.section("Knowledge base")
		r.item("None chosen: %s is not set, and no --kb was given.", kbEnv)
		// a knowledge base already kept inside the code repository, found by its shape, not its name
		where := project
		if inGit {
			where = top
		}
		found := findKBsIn(where, 4)
		holder := "code project's folder"
		if inGit {
			holder = "code repository"
		}
		travels := false
		for _, f := range found {
			how := "a repository of its own, which does not travel with the code"
			switch readGitPlace(f).kind {
			case inCodeRepo:
				how, travels = "it travels with the code", true
			case notInGit:
				how = "not in git"
			}
			r.item("Inside the %s, %s is shaped like a knowledge base: %d attested statement(s), "+
				"%d evidence file(s); %s.", holder, f, countFiles(filepath.Join(f, "attested"), "S-*.md"),
				countFiles(filepath.Join(f, "evidence"), "*.jsonl"), how)
		}
		if len(found) > 0 {
			advice := "ask the human whether this project uses it"
			if travels {
				advice += ", which the runbook recommends, since it travels with the code"
			}
			r.need(1, "The %s already holds a knowledge base (%s): %s; then run kb setup --report --kb <folder>.",
				holder, strings.Join(found, ", "), advice)
		} else {
			r.need(1, "Ask the human whether this project uses a new knowledge base or an existing one, and where it "+
				"is or should go; then run kb setup --report --kb <folder>.")
		}
	}
	ids := map[string]string{}
	for _, c := range choices {
		kb := c.path
		if len(kbs) == 1 {
			r.section("Knowledge base")
		} else {
			r.section("Knowledge base: " + kb)
		}
		r.item("%s (%s).", kb, c.how)
		kbReport(&r, kb, len(kbs) > 1)
		// the commit setting matters only for a knowledge base inside a code repository (S-DQES)
		if pr := offerCommitProblem(sf.env[offerCommitEnv]); pr != "" && readGitPlace(kb).kind == inCodeRepo {
			r.need(9, "In %s, %s.", settingsRel, pr)
		}
		if id, ok, _ := kbset.ReadID(kb); ok {
			if other, dup := ids[id]; dup {
				r.need(1, "%s and %s have the same id, %s, so they are copies of one knowledge base: ask the human "+
					"which one this project uses.", other, kb, id)
			}
			ids[id] = kb
		}
	}
	if len(kbs) > 1 {
		r.item("This project refers to %d knowledge bases; each session is on the record in at most one of them, "+
			"chosen when it goes on the record.", len(kbs))
		if pinsDiffer(kbs) {
			r.item("Their pins differ, so the agent passes --kb on every command; the session-start text says so.")
		}
	}

	if len(kbs) > 0 {
		r.section("Settings for this project")
		var extra [][2]string
		for _, k := range []string{askModeEnv, perTurnEnv, offerCommitEnv} {
			if v, ok := sf.env[k]; ok {
				extra = append(extra, [2]string{k, v})
			}
		}
		var missing []string
		if sf.err == nil {
			if !sameList(kbset.SplitList(sf.env[kbEnv]), kbs) {
				missing = append(missing, kbEnv+", naming "+map[bool]string{true: "the knowledge base", false: "every knowledge base"}[len(kbs) == 1])
			}
			for _, kb := range kbs {
				found := false
				for _, d := range sf.dirs {
					if samePath(dyn.Resolve(d), kb) {
						found = true
					}
				}
				if !found {
					missing = append(missing, kb+" in additionalDirectories")
				}
			}
			for _, a := range missingFrom(sf.allow, allAllow(kbs)) {
				missing = append(missing, "allow "+a)
			}
			for _, d := range missingFrom(sf.deny, allDeny(kbs)) {
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
				"```json", kentSettingsJSON(kbs, extra), "```")
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

// sameList is whether the folders named in have are the folders want, in any order.
func sameList(have, want []string) bool {
	if len(have) != len(want) {
		return false
	}
	for _, w := range want {
		found := false
		for _, h := range have {
			if samePath(dyn.Resolve(h), w) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// kbReport describes the knowledge base folder: whether it is one, its id, its git repository, its pin and
// who would be recorded. several is whether the project refers to more than one, when each needs an id.
func kbReport(r *report, kb string, several bool) {
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

	switch id, ok, err := kbset.ReadID(kb); {
	case err != nil:
		r.item("Its id cannot be read: %v.", err)
		r.need(1, "The knowledge base's id cannot be read (%s); show the human %s. An id never changes, so "+
			"the one to keep is the one other knowledge bases refer to.", err, kbset.IDPath(kb))
	case ok:
		r.item("Its id is %s.", id)
	case several:
		r.item("It has no id yet.")
		r.need(3, "Give %s an id with kb id --create --kb %s, and commit %s: this project refers to several "+
			"knowledge bases, and a reference from one into another carries the id.", kb, kb, kbset.IDFile)
	default:
		r.item("It has no id yet; kb id --create gives it one, needed once another knowledge base is to refer to it.")
	}

	place := readGitPlace(kb)
	r.item("%s", place.summary())
	if place.submodule {
		r.item("Tell the human: %s.", submoduleWarning)
	}
	_, gitRuns := git(kb, "--version")
	switch {
	case place.broken != "":
		r.item("%s is not a git repository: it has no HEAD.", place.broken)
		r.need(1, "%s is not a git repository, though it is named like one; ask the human whether to repair it "+
			"or remove it.", place.broken)
	case place.kind == notInGit:
		r.item("Git is recommended, not required; if the human wants it, the runbook says how.")
	case gitRuns == -1:
		r.item("git could not be run, so its repository was not checked.")
	default:
		if place.kind == inCodeRepo {
			if !hasHintFile(kb) {
				r.item("No README or other hint file for people who check out the code without Kent.")
				r.need(3, "Add the hint file README.md to the knowledge base's folder, as the runbook says, for people "+
					"who check out the code without Kent.")
			}
			if s := strangers(kb); len(s) > 0 {
				r.item("Its folder also holds %s, which is not part of a knowledge base.", strings.Join(s, ", "))
				r.need(1, "The knowledge base's folder holds %s besides the knowledge base. A knowledge base inside a "+
					"code repository has a folder of its own: ask the human where those belong.", strings.Join(s, ", "))
			}
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
				"index.db and render/ are rebuilt from it. Add them to its .gitignore, as the runbook says.",
				strings.Join(unignored, ", "))
		} else {
			r.item("git ignores off-record/, index.db and render/.")
		}
		var plain []string
		for _, f := range unionFiles {
			if out, st := git(kb, "check-attr", "merge", "--", f); st != 0 || !strings.HasSuffix(out, ": merge: union") {
				plain = append(plain, f)
			}
		}
		if len(plain) > 0 {
			r.item("git does not merge %s by keeping both copies' lines.", strings.Join(plain, ", "))
			r.need(3, "git must keep both copies' new lines when it merges %s, rather than report a conflict: "+
				"add them to the knowledge base's .gitattributes with merge=union, as the runbook says.", strings.Join(plain, ", "))
		} else {
			r.item("git merges %s by keeping both copies' lines.", strings.Join(unionFiles, ", "))
		}
		var unrouted []string
		for _, f := range []string{"attested/S-0000-x.md", "agent/A-0000-x.md"} {
			if out, st := git(kb, "check-attr", "merge", "--", f); st != 0 || !strings.HasSuffix(out, ": merge: "+mergeDriverName) {
				unrouted = append(unrouted, filepath.Dir(filepath.FromSlash(f))+"/")
			}
		}
		if len(unrouted) > 0 {
			r.item("git does not send the statement files in %s to kb's merge driver.", strings.Join(unrouted, " and "))
			r.need(3, "Send the statement files to kb's merge driver: add %s to the knowledge base's .gitattributes, "+
				"as the runbook says.", strings.Join(mergeAttributes, " and "))
		} else {
			r.item("git sends the statement files to kb's merge driver.")
		}
		if ok, _ := driverRegistered(kb); ok {
			r.item("kb's merge driver is registered in this clone.")
		} else {
			r.item("kb's merge driver is not registered in this clone.")
			r.need(3, "Register kb's merge driver in this clone; kb never changes git's config itself. Run "+
				"`%s config merge.%s.driver \"%s\"`, and nothing else under merge.%s: git refuses to merge a file "+
				"whose driver has a name and no command.", gitPrefix(kb), mergeDriverName, mergeDriverCommand, mergeDriverName)
		}
		var empty []string
		for _, d := range []string{"attested", "evidence"} {
			if isEmptyDir(filepath.Join(kb, d)) {
				empty = append(empty, d+"/")
			}
		}
		if len(empty) > 0 {
			r.need(3, "%s empty, and git keeps no empty folder, so a clone would not be a knowledge base: run "+
				"kb setup --gitkeep --kb %s, which writes an empty .gitkeep in each.", strings.Join(empty, " and ")+
				map[bool]string{true: " are", false: " is"}[len(empty) > 1], kb)
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

	people, problem := reportUsers(kb)
	for _, l := range people {
		if strings.HasPrefix(l, "  ") {
			r.lines = append(r.lines, l)
		} else {
			r.item("%s", l)
		}
	}
	switch u, _ := userSource("", kb); {
	case u == "":
		r.item("No user id: kb could not record anything the human does.")
		r.need(4, `Set a user id: git's user.email in the knowledge base's folder, or ~/.kenttheclerk.json `+
			`with {"user": "<key>"} for every knowledge base, or an entry under "folders" for this one; ask the `+
			`human which. Then add them to the user table, as the runbook says.`)
	case problem != "":
		r.need(4, "Add the human to the knowledge base's user table, as the runbook says: %s.", problem)
	}
}

// reportUsers is describeUsers for kb setup's report, which is printed only once it is whole: a user table
// or override log that cannot even be decoded as text is reported as unreadable, naming the file, rather
// than ending kb setup before it says anything.
func reportUsers(kb string) (lines []string, problem string) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		e, ok := r.(*dyn.Exception)
		if !ok {
			panic(r)
		}
		what := "the user table " + usersName + " or the override log " + overrideLogName
		for _, f := range []struct{ path, what string }{
			{usersPath(kb), "the user table " + usersName},
			{overrideLogPath(kb), "the override log " + overrideLogName},
		} {
			if b, err := os.ReadFile(f.path); err == nil && !utf8.Valid(b) {
				what = f.what
				break
			}
		}
		lines, problem = nil, fmt.Sprintf("%s cannot be read (%s: %s), so kb records nothing anyone does", what, e.Type, e.Msg)
	}()
	return describeUsers(kb)
}

// ---------------------------------------------------------------- kb setup

// createKB makes a new knowledge base in dir: the folder and its attested/ and evidence/ folders, which is
// how kb finds a knowledge base, and its id (S-Y17H90). Whether it goes in git is a separate question (S-0130), and the agent runs
// git itself rather than through kb.
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
	writeGitkeeps(dir)
	id := newKBID()
	if err := os.WriteFile(kbset.IDPath(dir), []byte(id+"\n"), 0o666); err != nil {
		die(fmt.Sprintf("cannot write %s: %v", kbset.IDPath(dir), err))
	}
	println("created the knowledge base " + dir + ", with attested/ and evidence/, each with an empty .gitkeep, " +
		"and its id, " + id + ", in " + kbset.IDFile)
}

// writeGitkeeps writes an empty .gitkeep in attested/ and evidence/ where they are empty, since git keeps
// no empty folder and a clone must still be a knowledge base; the folders are the record's, so kb writes
// the files rather than the agent (Kent Rule 1).
func writeGitkeeps(dir string) {
	for _, sub := range []string{"attested", "evidence"} {
		p := filepath.Join(dir, sub, ".gitkeep")
		if dyn.IsFile(p) {
			continue
		}
		if err := os.WriteFile(p, nil, 0o666); err != nil {
			die(fmt.Sprintf("cannot write %s: %v", p, err))
		}
	}
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
	kbArg := kbFlag
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
	if ns.Bool("gitkeep") {
		kb := ""
		if kbArg != "" {
			kb = dyn.Resolve(kbArg)
		} else if cs := chooseKBs("", readSettings(project)); len(cs) == 1 {
			kb = cs[0].path
		}
		if kb == "" || !isKBFolder(kb) {
			die("--gitkeep needs one knowledge base: name it with --kb")
		}
		writeGitkeeps(kb)
		println("wrote an empty .gitkeep in " + kb + "'s attested/ and evidence/ where there was none")
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
	p.Option("--project", "the code project's folder (default: CLAUDE_PROJECT_DIR, else the current folder)", nil, false)
	p.Flag("--report", "print only what setup finds, without the runbook")
	p.Option("--create", "create a new knowledge base in this folder (new or empty), then report", nil, false)
	p.Flag("--gitkeep", "write an empty .gitkeep in the knowledge base's attested/ and evidence/, then report")
	p.Run = runSetup

	s := sub.AddParser("session-start", "print what the agent reads first, for the project's session-start hook")
	s.Run = runSessionStart
}
