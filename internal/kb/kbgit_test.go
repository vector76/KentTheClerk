package kb

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"kenttheclerk/internal/dyn"
)

// fakeRepo makes dir a git repository as kb reads one from files: a .git folder with HEAD and the config.
func fakeRepo(t *testing.T, dir, config string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(config), 0o644)
}

func joined(xs []string) string { return strings.Join(xs, "\n") }

// TestGitLines checks that the case is read from the knowledge base's situation (S-MBME) and that the
// session-start text says what drives its commits and its pulls and pushes (S-7BWA, S-8J77, S-DQES).
func TestGitLines(t *testing.T) {
	none := func(string) string { return "" }
	root := newTestKB(t)
	lines, problems := gitLines(root, none)
	if len(problems) != 0 || len(lines) != 1 || !strings.Contains(lines[0], "in no git repository") {
		t.Errorf("not in git: %q %q", lines, problems)
	}
	fakeRepo(t, root, "[core]\n\tbare = false\n")
	lines, _ = gitLines(root, none)
	if p := readGitPlace(root); p.kind != localRepo || !strings.Contains(lines[0], "no remote") ||
		!strings.Contains(lines[0], gitPrefix(root)) {
		t.Errorf("local repository: %+v %q", p, lines)
	}
	fakeRepo(t, root, "[remote \"backup\"]\n\turl = x\n[remote \"origin\"]\n\turl = y\n")
	lines, _ = gitLines(root, none)
	if p := readGitPlace(root); p.kind != remoteRepo || p.remote != "origin" || !strings.Contains(lines[0], "On every act") ||
		!strings.Contains(lines[0], gitPrefix(root)+" pull --no-rebase") ||
		!strings.Contains(lines[0], "cannot be reached") || len(lines) != 2 || !strings.Contains(lines[1], "kb resolve") ||
		!strings.Contains(lines[1], "push it where you push") {
		t.Errorf("repository with a remote: %+v %q", p, lines)
	}

	code := t.TempDir()
	fakeRepo(t, code, "")
	inner := filepath.Join(code, "kent")
	for _, d := range []string{"attested", "evidence"} {
		os.MkdirAll(filepath.Join(inner, d), 0o755)
	}
	lines, _ = gitLines(inner, none)
	if !strings.Contains(lines[0], "inside the code repository at "+code) || !strings.Contains(lines[0], "Leave committing") ||
		!strings.Contains(lines[1], "Leave what it writes to the person") || strings.Count(joined(lines), "own initiative") != 1 {
		t.Errorf("inside a code repository: %q", lines)
	}
	offer := func(v string) func(string) string {
		return func(k string) string {
			if k == offerCommitEnv {
				return v
			}
			return ""
		}
	}
	if lines, _ = gitLines(inner, offer("after-attestation")); !strings.Contains(lines[0], "offer to commit") {
		t.Errorf("inside a code repository, offering commits: %q", lines)
	}
	// a value kb does not understand: the default's sentence, and the problem
	lines, problems = gitLines(inner, offer("sometimes"))
	if !strings.Contains(lines[0], "Leave committing") || !strings.Contains(joined(problems), offerCommitEnv) {
		t.Errorf("a bad commit setting: %q %q", lines, problems)
	}
	// it is not checked for a knowledge base of its own
	if _, problems = gitLines(root, offer("sometimes")); strings.Contains(joined(problems), offerCommitEnv) {
		t.Errorf("the commit setting was checked outside a code repository: %q", problems)
	}
	if readGitPlace(inner).ownRepo() {
		t.Errorf("a knowledge base inside a code repository counts as its own repository")
	}
}

// TestGitRemoteForms: a remote is seen in either section form, in any case.
func TestGitRemoteForms(t *testing.T) {
	for _, config := range []string{"[Remote \"origin\"]\n", "[remote.origin]\n", "[REMOTE \"up\"]\n"} {
		root := t.TempDir()
		fakeRepo(t, root, config)
		if p := readGitPlace(root); p.kind != remoteRepo {
			t.Errorf("%q: %+v", config, p)
		}
	}
}

// TestGitBroken: a .git that is not a repository, an empty folder or one naming a folder that is gone, is
// read as in no git repository, and reported.
func TestGitBroken(t *testing.T) {
	root := newTestKB(t)
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	p := readGitPlace(root)
	_, problems := gitLines(root, func(string) string { return "" })
	if p.kind != notInGit || p.broken == "" || !strings.Contains(joined(problems), "not a git repository") {
		t.Errorf("empty .git: %+v %q", p, problems)
	}
	var r report
	kbReport(&r, root, false)
	if todo := joined(r.todos()); !strings.Contains(todo, "not a git repository") || strings.Contains(todo, "merge driver") {
		t.Errorf("setup contradicts itself on a broken .git:\n%s", todo)
	}
	os.Remove(filepath.Join(root, ".git"))
	os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+filepath.Join(root, "gone")+"\n"), 0o644)
	if p := readGitPlace(root); p.kind != notInGit || p.broken == "" {
		t.Errorf("stale gitdir: %+v", p)
	}
}

// TestGitPlaceFromGitFile checks a .git that is a file: a worktree reads its config from the common folder,
// even under a folder named modules, and a submodule is flagged, also for a knowledge base in a folder of
// its checkout (S-SG81).
func TestGitPlaceFromGitFile(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "modules", "work")
	main := filepath.Join(parent, "main", ".git")
	os.MkdirAll(filepath.Join(main, "worktrees", "wt"), 0o755)
	os.WriteFile(filepath.Join(main, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	os.WriteFile(filepath.Join(main, "config"), []byte("[remote \"origin\"]\n"), 0o644)
	os.WriteFile(filepath.Join(main, "worktrees", "wt", "HEAD"), []byte("ref: refs/heads/wt\n"), 0o644)
	os.WriteFile(filepath.Join(main, "worktrees", "wt", "commondir"), []byte("../..\n"), 0o644)
	wt := filepath.Join(parent, "wt")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(main, "worktrees", "wt")+"\n"), 0o644)
	if p := readGitPlace(wt); p.kind != remoteRepo || p.submodule {
		t.Errorf("worktree under a folder named modules: %+v", p)
	}

	sub := filepath.Join(parent, "code", "kent")
	os.MkdirAll(filepath.Join(parent, "code", ".git", "modules", "kent"), 0o755)
	os.WriteFile(filepath.Join(parent, "code", ".git", "modules", "kent", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/kent\n"), 0o644)
	p := readGitPlace(sub)
	if !p.submodule || !p.ownRepo() {
		t.Errorf("submodule: %+v", p)
	}
	if _, problems := gitLines(sub, func(string) string { return "" }); !strings.Contains(joined(problems), "submodule") {
		t.Errorf("submodule not reported: %q", problems)
	}
	inSub := filepath.Join(sub, "docs", "kb")
	os.MkdirAll(inSub, 0o755)
	if p := readGitPlace(inSub); p.kind != inCodeRepo || !p.submodule {
		t.Errorf("a knowledge base in a folder of a submodule checkout: %+v", p)
	}
}

// TestGitPrefixQuoting: a folder a shell would split, or read anything in, is quoted.
func TestGitPrefixQuoting(t *testing.T) {
	cases := map[string]string{
		"/work/kb":             "git -C /work/kb",
		"/my work/kb":          "git -C '/my work/kb'",
		"/work/$HOME/kb":       "git -C '/work/$HOME/kb'",
		"/work/a`b/kb":         "git -C '/work/a`b/kb'",
		"/work/o'brien/kb":     `git -C "/work/o'brien/kb"`,
		"/work/a&b;c/kb":       "git -C '/work/a&b;c/kb'",
		"C:/Users/x/kb-1.2_3+": "git -C C:/Users/x/kb-1.2_3+",
	}
	for in, want := range cases {
		if got := gitPrefix(in); got != want {
			t.Errorf("gitPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGitPrefixAgrees: setup and session-start find the knowledge base the same way, so the allow rules
// setup writes and the git prefix the agent is told to use are the same.
func TestGitPrefixAgrees(t *testing.T) {
	kb := newTestKB(t)
	link := filepath.Join(t.TempDir(), "link")
	target := kb
	if err := os.Symlink(kb, link); err == nil {
		target = link
	}
	t.Setenv(kbEnv, target)
	fromSetup := chooseKBs("", settingsFile{env: map[string]string{}})[0].path
	roots, _, _ := findKBRoots()
	fromSession := roots[0]
	if gitPrefix(fromSetup) != gitPrefix(fromSession) {
		t.Errorf("setup %q, session-start %q", gitPrefix(fromSetup), gitPrefix(fromSession))
	}
}

func TestOwnRepoAllowsGit(t *testing.T) {
	kb := newTestKB(t)
	has := func() bool {
		for _, a := range kentAllow(kb) {
			if strings.HasPrefix(a, "Bash(git -C ") {
				return true
			}
		}
		return false
	}
	if has() {
		t.Errorf("a knowledge base in no git repository gets git permissions")
	}
	fakeRepo(t, kb, "")
	if !has() {
		t.Errorf("a knowledge base in its own repository gets no git permissions: %q", kentAllow(kb))
	}
}

// TestFindKBsByShape checks that setup finds a knowledge base inside a code repository by its shape, not
// its name, and does not take any pair of attested/ and evidence/ folders for one (S-1RA3).
func TestFindKBsByShape(t *testing.T) {
	code := t.TempDir()
	mk := func(rel string, files ...string) {
		os.MkdirAll(filepath.Join(code, filepath.FromSlash(rel)), 0o755)
		for _, f := range files {
			os.WriteFile(filepath.Join(code, filepath.FromSlash(rel), f), nil, 0o644)
		}
	}
	mk("docs/decisions/attested", "S-7K2M-a-decision.md")
	mk("docs/decisions/evidence", "3781af31.jsonl", ".gitkeep")
	mk("docs/decisions/evidence/originals")
	mk("empty/attested", ".gitkeep")
	mk("empty/evidence", ".gitkeep")
	mk("other/attested", "notes.txt")
	mk("other/evidence", "x.jsonl")
	mk(".hidden/kb/attested", "S-0001-x.md")
	mk(".hidden/kb/evidence")
	got := findKBsIn(code, 4)
	want := filepath.Join(code, "docs", "decisions")
	if len(got) != 1 || got[0] != want {
		t.Errorf("found %q, want only %q", got, want)
	}
	mk("empty", usersName)
	if got = findKBsIn(code, 4); len(got) != 2 {
		t.Errorf("a knowledge base with only its user table was not found: %q", got)
	}
}

// TestSetupInCodeRepository checks that setup asks for the hint file and points out what does not belong in
// a knowledge base kept inside a code repository (S-NXEB, S-M1JC).
func TestSetupInCodeRepository(t *testing.T) {
	code := t.TempDir()
	if _, st := git(code, "init", "-q"); st != 0 {
		t.Skip("git is not available")
	}
	kb := filepath.Join(code, "kent")
	for _, d := range []string{"attested", "evidence"} {
		os.MkdirAll(filepath.Join(kb, d), 0o755)
	}
	os.WriteFile(filepath.Join(kb, "design.md"), nil, 0o644)
	var r report
	kbReport(&r, kb, false)
	todo := strings.Join(r.todos(), "\n")
	if !strings.Contains(todo, "README.md") || !strings.Contains(todo, "design.md") {
		t.Fatalf("setup did not ask for the hint file and about design.md:\n%s", todo)
	}
	os.Remove(filepath.Join(kb, "design.md"))
	os.WriteFile(filepath.Join(kb, "README.md"), nil, 0o644)
	r = report{}
	kbReport(&r, kb, false)
	if todo := strings.Join(r.todos(), "\n"); strings.Contains(todo, "README.md") || strings.Contains(todo, "besides") {
		t.Fatalf("setup still asks:\n%s", todo)
	}
}

// TestGitAllowNarrow: the allow rules for a knowledge base of its own are exactly the git forms the text
// asks for, the same for Bash and PowerShell; no rule lets an option through that writes a file or runs
// a program (log, diff, show, a wildcard after fetch, pull or push).
func TestGitAllowNarrow(t *testing.T) {
	kb := newTestKB(t)
	fakeRepo(t, kb, "")
	g := gitPrefix(kb) + " "
	var want []string
	for _, c := range []string{"status", "fetch", "pull --no-rebase", "pull --no-rebase --no-edit", "add *",
		"commit -m *", "commit --no-edit", "push", "push -u origin HEAD", "-c core.editor=true rebase --continue"} {
		want = append(want, "Bash("+g+c+")", "PowerShell("+g+c+")")
	}
	var got []string
	for _, a := range kentAllow(kb) {
		if strings.Contains(a, "(git ") {
			got = append(got, a)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("git allow rules:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, a := range got {
		for _, bad := range []string{" log", " diff", " show", "fetch *", "pull *", "push *", "--output", "-pack", "--exec"} {
			if strings.Contains(a, bad) {
				t.Errorf("%s lets %q through", a, bad)
			}
		}
	}
}

// TestDriverRegisteredForms: the driver may name kb by a path, kb or kb.exe; other arguments are not it.
func TestDriverRegisteredForms(t *testing.T) {
	root := newTestKB(t)
	if _, st := git(root, "init", "-q"); st != 0 {
		t.Skip("git is not available")
	}
	args := strings.TrimPrefix(mergeDriverCommand, "kb")
	for cmd, want := range map[string]bool{
		mergeDriverCommand:                           true,
		"C:/Programs/Kent/kb.exe" + args:             true,
		"/usr/local/bin/kb" + args:                   true,
		"kb merge-driver %O %A %B %P":                false,
		"other merge-driver %O %A %B %P %S %X %Y %L": false,
	} {
		git(root, "config", "merge.kent.driver", cmd)
		if got, ran := driverRegistered(root); !ran || got != want {
			t.Errorf("%q: registered %v, want %v", cmd, got, want)
		}
	}
}

// TestGitNotRunnable: when git cannot be run, session-start says so instead of calling the driver missing.
func TestGitNotRunnable(t *testing.T) {
	root := newTestKB(t)
	fakeRepo(t, root, "")
	t.Setenv("PATH", "")
	_, problems := gitLines(root, func(string) string { return "" })
	if s := joined(problems); !strings.Contains(s, "git could not be run") || strings.Contains(s, "not registered") {
		t.Errorf("problems: %q", problems)
	}
}

// TestGitkeep: a new knowledge base gets .gitkeep files, and an existing empty one gets them from
// kb setup --gitkeep.
func TestGitkeep(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kb")
	kbRun(t, "setup", "--create", dir, "--project", t.TempDir())
	for _, sub := range []string{"attested", "evidence"} {
		if !dyn.IsFile(filepath.Join(dir, sub, ".gitkeep")) {
			t.Errorf("--create wrote no %s/.gitkeep", sub)
		}
	}
	other := newTestKB(t)
	kbRun(t, "setup", "--gitkeep", "--kb", other, "--project", t.TempDir())
	if !dyn.IsFile(filepath.Join(other, "attested", ".gitkeep")) {
		t.Errorf("--gitkeep wrote no attested/.gitkeep")
	}
}

// TestFoundKBTravels: setup recommends a knowledge base it finds in the code repository only when it
// travels with the code, not when it is a repository of its own inside the folder.
func TestFoundKBTravels(t *testing.T) {
	code := t.TempDir()
	if _, st := git(code, "init", "-q"); st != 0 {
		t.Skip("git is not available")
	}
	inside := filepath.Join(code, "kent")
	for _, d := range []string{"attested", "evidence"} {
		os.MkdirAll(filepath.Join(inside, d), 0o755)
	}
	os.WriteFile(filepath.Join(inside, "attested", "S-7K2MQX-a.md"), nil, 0o644)
	t.Setenv(kbEnv, "")
	r := buildReport(code, "")
	if todo := joined(r.todos()); !strings.Contains(todo, "travels with the code") {
		t.Errorf("an in-repo knowledge base was not recommended:\n%s", todo)
	}
	fakeRepo(t, inside, "")
	r = buildReport(code, "")
	if todo := joined(r.todos()); strings.Contains(todo, "travels with the code") {
		t.Errorf("a knowledge base with its own repository was said to travel with the code:\n%s", todo)
	}
}
