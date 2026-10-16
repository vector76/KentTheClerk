package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKBMode(t *testing.T) {
	good := map[string]string{
		"mode: online\n":                   modeOnline,
		"# a comment\r\n\r\nmode: offline": modeOffline,
		"mode:online\nmode: online\n":      modeOnline,
	}
	for text, want := range good {
		if got, err := parseKBMode(text); err != nil || got != want {
			t.Errorf("parseKBMode(%q) = %q, %v; want %q", text, got, err, want)
		}
	}
	for _, text := range []string{"", "online\n", "mode: Online\n", "sync: online\n", "mode: online\nmode: offline\n"} {
		if got, err := parseKBMode(text); err == nil {
			t.Errorf("parseKBMode(%q) = %q, want an error", text, got)
		}
	}
}

// TestModeLines checks what the session-start text says for each mode and place in git (S-DR9W, S-HT4V).
func TestModeLines(t *testing.T) {
	root := newTestKB(t)
	lines, problem := modeLines(root)
	if problem != "" || len(lines) != 1 || !strings.Contains(lines[0], "mode: offline") || !strings.Contains(lines[0], "no git") {
		t.Errorf("plain folder: %q %q", lines, problem)
	}
	os.WriteFile(modePath(root), []byte("mode: online\n"), 0o644)
	if _, problem = modeLines(root); !strings.Contains(problem, "no git repository") {
		t.Errorf("online outside git: %q", problem)
	}
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	lines, problem = modeLines(root)
	if problem != "" || !strings.Contains(lines[0], "mode: online") || !strings.Contains(lines[0], "push it at once") ||
		!strings.Contains(lines[0], gitPrefix(root)) {
		t.Errorf("online: %q %q", lines, problem)
	}
	os.WriteFile(modePath(root), []byte("mode: sometimes\n"), 0o644)
	if lines, problem = modeLines(root); !strings.Contains(lines[0], "mode: offline") || problem == "" {
		t.Errorf("unreadable mode is not offline with a problem: %q %q", lines, problem)
	}
	os.Remove(modePath(root))
	if lines, _ = modeLines(root); !strings.Contains(lines[0], "its own git repository") {
		t.Errorf("offline, own repository: %q", lines)
	}

	code := t.TempDir()
	os.Mkdir(filepath.Join(code, ".git"), 0o755)
	inner := filepath.Join(code, "kent")
	for _, d := range []string{"attested", "evidence"} {
		os.MkdirAll(filepath.Join(inner, d), 0o755)
	}
	if lines, _ = modeLines(inner); !strings.Contains(lines[0], "inside the git repository at "+code) {
		t.Errorf("offline, inside a code repository: %q", lines)
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

func TestOnlineAllowsGit(t *testing.T) {
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
		t.Errorf("an offline knowledge base gets git permissions")
	}
	os.WriteFile(modePath(kb), []byte("mode: online\n"), 0o644)
	if !has() {
		t.Errorf("an online knowledge base gets no git permissions: %q", kentAllow(kb))
	}
}
