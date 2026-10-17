package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kenttheclerk/internal/dyn"
)

// stmt is a statement file's text with the given id, title, time of its attestation and text.
func stmt(id, title, at, body string, extra ...any) string {
	meta := dyn.D("id", id, "title", title, "answers", "a question", "originator", "jamie", "cites", []any{},
		"topics", []any{}, "attestations", []any{attestation("jamie", at, "proposed-and-agreed")})
	for i := 0; i+1 < len(extra); i += 2 {
		meta.Set(extra[i].(string), extra[i+1])
	}
	return dumpFrontmatter(meta) + "\n" + body + "\n"
}

// gitKB is a knowledge base in a git repository with a shared remote, and a second clone of it.
type gitKB struct {
	t           *testing.T
	root, other string
}

func newGitKB(t *testing.T) *gitKB {
	root := newTestKB(t)
	if _, st := git(root, "init", "-q", "-b", "main"); st != 0 {
		t.Skip("git is not available")
	}
	k := &gitKB{t: t, root: root}
	remote := filepath.Join(t.TempDir(), "remote.git")
	k.git(root, "init", "-q", "--bare", "-b", "main", remote)
	k.write(root, "attested/S-0001-the-first.md", stmt("S-0001", "The first", "2026-10-01T10:00:00+00:00", "First."))
	k.git(root, "add", "-A")
	k.git(root, "commit", "-q", "-m", "base")
	k.git(root, "remote", "add", "origin", remote)
	k.git(root, "push", "-q", "-u", "origin", "main")
	k.other = filepath.Join(t.TempDir(), "other")
	k.git(root, "clone", "-q", remote, k.other)
	return k
}

func (k *gitKB) git(dir string, args ...string) {
	k.t.Helper()
	if out, st := git(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); st != 0 {
		k.t.Fatalf("git %v: %d %s", args, st, out)
	}
}

func (k *gitKB) write(dir, rel, text string) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		k.t.Fatal(err)
	}
}

func (k *gitKB) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(k.root, filepath.FromSlash(rel)))
	return string(b)
}

// TestCollisionReported: a colleague's copy shared S-7K2M, and this copy gave the same id to a statement
// of its own. After the merge, kb resolve and kb check report it and change nothing (S-74B5).
func TestCollisionReported(t *testing.T) {
	k := newGitKB(t)
	k.write(k.other, "attested/S-7K2M-shared.md", stmt("S-7K2M", "Shared", "2026-10-02T10:00:00+00:00", "The shared one."))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "shared")
	k.git(k.other, "push", "-q")
	k.write(k.root, "attested/S-7K2M-unshared.md", stmt("S-7K2M", "Unshared", "2026-10-03T10:00:00+00:00", "Ours."))
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "unshared")
	k.git(k.root, "fetch", "-q")
	k.git(k.root, "merge", "-q", "--no-edit", "origin/main")
	before, _ := git(k.root, "status", "--porcelain")
	t.Setenv(kbEnv, k.root)
	var out struct {
		text string
		code int
	}
	out.text, out.code = kbRun(t, "resolve")
	if out.code != 1 || !strings.Contains(out.text, "S-7K2M is carried by S-7K2M-shared.md and S-7K2M-unshared.md") ||
		!strings.Contains(out.text, "left to the human") {
		t.Errorf("resolve: %d\n%s", out.code, out.text)
	}
	if after, _ := git(k.root, "status", "--porcelain"); after != before {
		t.Errorf("resolve changed files: %q", after)
	}
	if dyn.IsFile(overrideLogPath(k.root)) {
		t.Errorf("resolve wrote to the override log")
	}
}

// TestSameNameCollisionReported: two statements given one id and one title are one file added on both
// sides; kb resolve reports a collision, not a deletion, and leaves the file as git left it.
func TestSameNameCollisionReported(t *testing.T) {
	k := newGitKB(t)
	k.write(k.other, "attested/S-7K2M-one-title.md", stmt("S-7K2M", "One title", "2026-10-02T10:00:00+00:00", "Theirs."))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "shared")
	k.git(k.other, "push", "-q")
	k.write(k.root, "attested/S-7K2M-one-title.md", stmt("S-7K2M", "One title", "2026-10-03T10:00:00+00:00", "Ours."))
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "unshared")
	k.git(k.root, "fetch", "-q")
	if _, st := git(k.root, "-c", "user.name=t", "-c", "user.email=t@t", "merge", "-q", "--no-edit", "origin/main"); st == 0 {
		t.Fatalf("git merged an add/add without a conflict")
	}
	t.Setenv(kbEnv, k.root)
	var out struct {
		text string
		code int
	}
	out.text, out.code = kbRun(t, "resolve")
	if out.code != 1 || !strings.Contains(out.text, "two different statements") || strings.Contains(out.text, "deleted") {
		t.Errorf("resolve: %d\n%s", out.code, out.text)
	}
	if !strings.Contains(k.read("attested/S-7K2M-one-title.md"), "<<<<<<<") {
		t.Errorf("the conflicted file was changed")
	}
}

// TestDriverRefusesSameName: the driver does not merge two different statements that share an id and a
// name, as git hands it with no base.
func TestDriverRefusesSameName(t *testing.T) {
	a := stmt("S-7K2M", "One title", "2026-10-02T10:00:00+00:00", "Same text.")
	b := stmt("S-7K2M", "One title", "2026-10-03T10:00:00+00:00", "Same text.")
	if _, problems := mergeStatementText("", a, b); len(problems) == 0 {
		t.Errorf("merged two statements that share an id")
	}
	if _, problems := mergeStatementText("", a, a); len(problems) != 0 {
		t.Errorf("one statement added on both sides: %q", problems)
	}
}
