package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitLines checks that the case is read from the knowledge base's situation (S-MBME) and that the
// session-start text says what drives its commits and its pulls and pushes (S-7BWA, S-8J77, S-DQES).
func TestGitLines(t *testing.T) {
	none := func(string) string { return "" }
	root := newTestKB(t)
	lines, problem := gitLines(root, none)
	if problem != "" || len(lines) != 1 || !strings.Contains(lines[0], "in no git repository") {
		t.Errorf("not in git: %q %q", lines, problem)
	}
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644)
	lines, _ = gitLines(root, none)
	if p := readGitPlace(root); p.kind != localRepo || !strings.Contains(lines[0], "no remote") ||
		!strings.Contains(lines[0], gitPrefix(root)) {
		t.Errorf("local repository: %+v %q", p, lines)
	}
	os.WriteFile(filepath.Join(root, ".git", "config"),
		[]byte("[remote \"backup\"]\n\turl = x\n[remote \"origin\"]\n\turl = y\n"), 0o644)
	lines, _ = gitLines(root, none)
	if p := readGitPlace(root); p.kind != remoteRepo || p.remote != "origin" || !strings.Contains(lines[0], "on every act") ||
		!strings.Contains(lines[0], "cannot be reached") || len(lines) != 2 || !strings.Contains(lines[1], "Expect any pull") {
		t.Errorf("repository with a remote: %+v %q", p, lines)
	}

	code := t.TempDir()
	os.Mkdir(filepath.Join(code, ".git"), 0o755)
	inner := filepath.Join(code, "kent")
	for _, d := range []string{"attested", "evidence"} {
		os.MkdirAll(filepath.Join(inner, d), 0o755)
	}
	lines, _ = gitLines(inner, none)
	if !strings.Contains(lines[0], "inside the code repository at "+code) || !strings.Contains(lines[0], "Leave committing") {
		t.Errorf("inside a code repository: %q", lines)
	}
	lines, _ = gitLines(inner, func(k string) string {
		if k == offerCommitEnv {
			return "after-attestation"
		}
		return ""
	})
	if !strings.Contains(lines[0], "offer to commit") {
		t.Errorf("inside a code repository, offering commits: %q", lines)
	}
	if readGitPlace(inner).ownRepo() {
		t.Errorf("a knowledge base inside a code repository counts as its own repository")
	}
}

// TestGitPlaceFromGitFile checks a .git that is a file: a worktree reads its config from the common folder,
// and a submodule is flagged (S-SG81).
func TestGitPlaceFromGitFile(t *testing.T) {
	parent := t.TempDir()
	main := filepath.Join(parent, "main", ".git")
	os.MkdirAll(filepath.Join(main, "worktrees", "wt"), 0o755)
	os.WriteFile(filepath.Join(main, "config"), []byte("[remote \"origin\"]\n"), 0o644)
	os.WriteFile(filepath.Join(main, "worktrees", "wt", "commondir"), []byte("../..\n"), 0o644)
	wt := filepath.Join(parent, "wt")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(main, "worktrees", "wt")+"\n"), 0o644)
	if p := readGitPlace(wt); p.kind != remoteRepo || p.submodule {
		t.Errorf("worktree: %+v", p)
	}

	sub := filepath.Join(parent, "code", "kent")
	os.MkdirAll(filepath.Join(parent, "code", ".git", "modules", "kent"), 0o755)
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/kent\n"), 0o644)
	p := readGitPlace(sub)
	if !p.submodule || !p.ownRepo() {
		t.Errorf("submodule: %+v", p)
	}
	if _, problem := gitLines(sub, func(string) string { return "" }); !strings.Contains(problem, "submodule") {
		t.Errorf("submodule not reported: %q", problem)
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
	os.Mkdir(filepath.Join(kb, ".git"), 0o755)
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
	kbReport(&r, kb)
	todo := strings.Join(r.todos(), "\n")
	if !strings.Contains(todo, "README.md") || !strings.Contains(todo, "design.md") {
		t.Fatalf("setup did not ask for the hint file and about design.md:\n%s", todo)
	}
	os.Remove(filepath.Join(kb, "design.md"))
	os.WriteFile(filepath.Join(kb, "README.md"), nil, 0o644)
	r = report{}
	kbReport(&r, kb)
	if todo := strings.Join(r.todos(), "\n"); strings.Contains(todo, "README.md") || strings.Contains(todo, "besides") {
		t.Fatalf("setup still asks:\n%s", todo)
	}
}
