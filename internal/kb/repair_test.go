package kb

import (
	"os"
	"path/filepath"
	"slices"
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

// TestRepairCollision: a colleague's copy shared S-7K2M, and this copy gave the same id to a statement of its
// own, cited it in another statement's rests_on and text, and amended an older statement with it. After the
// merge, kb resolve gives this copy's statement a new id and rewrites only this copy's references.
func TestRepairCollision(t *testing.T) {
	k := newGitKB(t)
	// the shared side: S-7K2M, and a statement that rests on it
	k.write(k.other, "attested/S-7K2M-shared.md", stmt("S-7K2M", "Shared", "2026-10-02T10:00:00+00:00", "The shared one."))
	k.write(k.other, "attested/S-ZZZ1-cites-shared.md", stmt("S-ZZZ1", "Cites shared", "2026-10-02T11:00:00+00:00",
		"Builds on S-7K2M.", "rests_on", []any{"S-7K2M"}))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "shared")
	k.git(k.other, "push", "-q")
	// this copy: its own S-7K2M amending S-0001, and a statement resting on it and naming it
	k.write(k.root, "attested/S-7K2M-unshared.md", stmt("S-7K2M", "Unshared", "2026-10-03T10:00:00+00:00",
		"The unshared one, amending S-0001.", "amends", "S-0001"))
	k.write(k.root, "attested/S-0001-the-first.md", stmt("S-0001", "The first", "2026-10-01T10:00:00+00:00", "First.",
		"amended_by", []any{"S-7K2M"}))
	k.write(k.root, "attested/S-YYY1-cites-unshared.md", stmt("S-YYY1", "Cites unshared", "2026-10-03T11:00:00+00:00",
		"As S-7K2M says.", "rests_on", []any{"S-7K2M"}))
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "unshared")
	k.git(k.root, "fetch", "-q")
	k.git(k.root, "merge", "-q", "--no-edit", "origin/main")

	t.Setenv(kbEnv, k.root)
	if code := Main([]string{"resolve"}); code != 0 {
		t.Fatalf("resolve exit %d", code)
	}
	if dups := duplicateIDs(k.root); len(dups) != 0 {
		t.Fatalf("still duplicated: %v", dups)
	}
	entries := mustLoadOverrideLog(k.root)
	if len(entries) != 1 || entryStr(entries[0], "kind") != ovRepairID || entryStr(entries[0], "old_id") != "S-7K2M" {
		t.Fatalf("log: %v", entries)
	}
	newID := entryStr(entries[0], "new_id")
	if k.read("attested/S-7K2M-shared.md") == "" || k.read("attested/S-7K2M-unshared.md") != "" {
		t.Errorf("the shared statement did not keep its id, or the unshared file is still there")
	}
	moved := k.read("attested/" + newID + "-unshared.md")
	if !strings.Contains(moved, "id: "+newID) || !strings.Contains(moved, "amends: S-0001") {
		t.Errorf("renamed statement:\n%s", moved)
	}
	if y := k.read("attested/S-YYY1-cites-unshared.md"); !strings.Contains(y, "  - "+newID) || !strings.Contains(y, "As "+newID+" says.") {
		t.Errorf("this copy's reference was not rewritten:\n%s", y)
	}
	if z := k.read("attested/S-ZZZ1-cites-shared.md"); !strings.Contains(z, "  - S-7K2M") || !strings.Contains(z, "Builds on S-7K2M.") {
		t.Errorf("the shared side's reference was rewritten:\n%s", z)
	}
	if f := k.read("attested/S-0001-the-first.md"); !strings.Contains(f, "  - "+newID) || strings.Contains(f, "S-7K2M") {
		t.Errorf("the amendment mark was not rebuilt:\n%s", f)
	}
	var errs []string
	overrideChecks(k.root, &Sessions{}, func(f string, a ...any) { errs = append(errs, f) }, func(string, ...any) {})
	if len(errs) > 0 {
		t.Errorf("kb check on the log: %v", errs)
	}
}

// TestRepairBothShared: two branches each gave a statement S-7K2M and were merged into main without the
// driver, then pushed (S-H2ZW). The one that reached main first keeps the id; the other moves with the
// citation made on its own branch; a citation made on main after both had landed is listed for the human
// and left as it is.
func TestRepairBothShared(t *testing.T) {
	k := newGitKB(t)
	r := k.root
	k.git(r, "checkout", "-q", "-b", "one")
	k.write(r, "attested/S-7K2M-first.md", stmt("S-7K2M", "First", "2026-10-03T10:00:00+00:00", "The first."))
	k.write(r, "attested/S-ONE1-cites-first.md", stmt("S-ONE1", "Cites first", "2026-10-03T11:00:00+00:00",
		"Per S-7K2M.", "rests_on", []any{"S-7K2M"}))
	k.git(r, "add", "-A")
	k.git(r, "commit", "-q", "-m", "one")
	k.git(r, "checkout", "-q", "-b", "two", "main")
	// made earlier than the first, but it reaches main second
	k.write(r, "attested/S-7K2M-second.md", stmt("S-7K2M", "Second", "2026-10-02T10:00:00+00:00", "The second."))
	k.write(r, "attested/S-TWO1-cites-second.md", stmt("S-TWO1", "Cites second", "2026-10-02T11:00:00+00:00",
		"As S-7K2M says.", "rests_on", []any{"S-7K2M"}))
	k.git(r, "add", "-A")
	k.git(r, "commit", "-q", "-m", "two")
	k.git(r, "checkout", "-q", "main")
	k.git(r, "merge", "-q", "--no-ff", "--no-edit", "one")
	k.git(r, "merge", "-q", "--no-ff", "--no-edit", "two")
	k.write(r, "attested/S-LATE-cites-after.md", stmt("S-LATE", "Cites after", "2026-10-04T10:00:00+00:00",
		"After both, S-7K2M.", "rests_on", []any{"S-7K2M"}))
	k.git(r, "add", "-A")
	k.git(r, "commit", "-q", "-m", "late")
	k.git(r, "push", "-q")

	t.Setenv(kbEnv, r)
	if code := Main([]string{"resolve"}); code != 1 {
		t.Fatalf("resolve exit %d, want 1 for the citation left to the human", code)
	}
	if dups := duplicateIDs(r); len(dups) != 0 {
		t.Fatalf("still duplicated: %v", dups)
	}
	e := mustLoadOverrideLog(r)[0]
	newID := entryStr(e, "new_id")
	if entryStr(e, "kept") != "attested/S-7K2M-first.md" || !dyn.Equal(e.Get("unattributed"), []any{"attested/S-LATE-cites-after.md"}) {
		t.Errorf("log entry: %s", dyn.Dumps(e))
	}
	if k.read("attested/"+newID+"-second.md") == "" {
		t.Errorf("the statement that reached main second did not move")
	}
	if two := k.read("attested/S-TWO1-cites-second.md"); !strings.Contains(two, "  - "+newID) || !strings.Contains(two, "As "+newID+" says.") {
		t.Errorf("its own branch's citation was not rewritten:\n%s", two)
	}
	if one := k.read("attested/S-ONE1-cites-first.md"); !strings.Contains(one, "Per S-7K2M.") {
		t.Errorf("the first one's citation was rewritten:\n%s", one)
	}
	if late := k.read("attested/S-LATE-cites-after.md"); !strings.Contains(late, "After both, S-7K2M.") {
		t.Errorf("the late citation was changed:\n%s", late)
	}
}

// TestRepairSameName:both copies gave a statement the same id and title, so git sees one file added on both
// sides; kb resolve keeps the shared one at that name and moves this copy's to a new id.
func TestRepairSameName(t *testing.T) {
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
	if code := Main([]string{"resolve"}); code != 0 {
		t.Fatalf("resolve exit %d", code)
	}
	if kept := k.read("attested/S-7K2M-one-title.md"); !strings.Contains(kept, "Theirs.") || strings.Contains(kept, "<<<<") {
		t.Errorf("kept file:\n%s", kept)
	}
	newID := entryStr(mustLoadOverrideLog(k.root)[0], "new_id")
	if moved := k.read("attested/" + newID + "-one-title.md"); !strings.Contains(moved, "Ours.") {
		t.Errorf("moved file:\n%s", moved)
	}
	// run again before the files are staged: nothing more happens
	Main([]string{"resolve"})
	files, _ := filepath.Glob(filepath.Join(k.root, "attested", "*-one-title.md"))
	if n := len(mustLoadOverrideLog(k.root)); n != 1 || len(files) != 2 {
		t.Errorf("a second run repaired again: %d entries, files %v", n, files)
	}
}

// TestRepairOursShared: the shared ref holds our copy, and our own unpushed citation of it stays; the
// colleague's copy, merged in from their branch, moves with the citation made beside it.
func TestRepairOursShared(t *testing.T) {
	k := newGitKB(t)
	k.write(k.root, "attested/S-7K2M-ours.md", stmt("S-7K2M", "Ours", "2026-10-02T10:00:00+00:00", "Ours."))
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "ours")
	k.git(k.root, "push", "-q")
	k.write(k.root, "attested/S-OUR1-cites-ours.md", stmt("S-OUR1", "Cites ours", "2026-10-03T10:00:00+00:00",
		"Per S-7K2M.", "rests_on", []any{"S-7K2M"}))
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "our citation, not pushed")
	k.git(k.other, "checkout", "-q", "-b", "feat")
	k.write(k.other, "attested/S-7K2M-theirs.md", stmt("S-7K2M", "Theirs", "2026-10-02T11:00:00+00:00", "Theirs."))
	k.write(k.other, "attested/S-THR1-cites-theirs.md", stmt("S-THR1", "Cites theirs", "2026-10-02T12:00:00+00:00",
		"Per S-7K2M.", "rests_on", []any{"S-7K2M"}))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "theirs")
	k.git(k.other, "push", "-q", "origin", "feat")
	k.git(k.root, "fetch", "-q")
	k.git(k.root, "merge", "-q", "--no-edit", "origin/feat")
	t.Setenv(kbEnv, k.root)
	if code := Main([]string{"resolve"}); code != 0 {
		t.Fatalf("resolve exit %d", code)
	}
	newID := entryStr(mustLoadOverrideLog(k.root)[0], "new_id")
	if ours := k.read("attested/S-OUR1-cites-ours.md"); !strings.Contains(ours, "Per S-7K2M.") {
		t.Errorf("our own citation was moved:\n%s", ours)
	}
	if theirs := k.read("attested/S-THR1-cites-theirs.md"); !strings.Contains(theirs, "Per "+newID+".") {
		t.Errorf("their citation did not move:\n%s", theirs)
	}
}

// TestRepairResumes: a repair whose entry was written but whose files were not is finished by running
// kb resolve again, under the id the entry gave, without a second entry.
func TestRepairResumes(t *testing.T) {
	k := newGitKB(t)
	k.write(k.other, "attested/S-7K2M-shared.md", stmt("S-7K2M", "Shared", "2026-10-02T10:00:00+00:00", "Shared."))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "shared")
	k.git(k.other, "push", "-q")
	k.write(k.root, "attested/S-7K2M-unshared.md", stmt("S-7K2M", "Unshared", "2026-10-03T10:00:00+00:00", "Unshared."))
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "unshared")
	k.git(k.root, "fetch", "-q")
	k.git(k.root, "merge", "-q", "--no-edit", "origin/main")
	appendLogEntry(overrideLogPath(k.root), dyn.D("id", "O-RSM1", "kind", ovRepairID, "at", "2026-10-04T10:00:00+00:00",
		"by", "kb", "reason", "cut short", "old_id", "S-7K2M", "new_id", "S-RSMD", "kept", "attested/S-7K2M-shared.md",
		"renamed_from", "attested/S-7K2M-unshared.md", "renamed_to", "attested/S-RSMD-unshared.md", "rewritten", []any{}))
	t.Setenv(kbEnv, k.root)
	if code := Main([]string{"resolve"}); code != 0 {
		t.Fatalf("resolve exit %d", code)
	}
	if k.read("attested/S-RSMD-unshared.md") == "" || len(mustLoadOverrideLog(k.root)) != 1 {
		t.Errorf("the repair was not finished under its logged id, or was entered twice")
	}
}

// TestRepairThreeWay: three statements given one id; the shared one keeps it and the other two move, each
// with the citation made beside it.
func TestRepairThreeWay(t *testing.T) {
	k := newGitKB(t)
	r := k.root
	k.write(k.other, "attested/S-7K2M-shared.md", stmt("S-7K2M", "Shared", "2026-10-02T10:00:00+00:00", "Shared."))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "shared")
	k.git(k.other, "push", "-q")
	k.git(r, "checkout", "-q", "-b", "b1")
	k.write(r, "attested/S-7K2M-one.md", stmt("S-7K2M", "One", "2026-10-03T10:00:00+00:00", "One."))
	k.write(r, "attested/S-CIT1-cites-one.md", stmt("S-CIT1", "Cites one", "2026-10-03T11:00:00+00:00", "Per S-7K2M."))
	k.git(r, "add", "-A")
	k.git(r, "commit", "-q", "-m", "one")
	k.git(r, "checkout", "-q", "-b", "b2", "main")
	k.write(r, "attested/S-7K2M-two.md", stmt("S-7K2M", "Two", "2026-10-03T12:00:00+00:00", "Two."))
	k.write(r, "attested/S-CIT2-cites-two.md", stmt("S-CIT2", "Cites two", "2026-10-03T13:00:00+00:00", "Per S-7K2M."))
	k.git(r, "add", "-A")
	k.git(r, "commit", "-q", "-m", "two")
	k.git(r, "checkout", "-q", "main")
	k.git(r, "merge", "-q", "--no-edit", "b1", "b2")
	k.git(r, "fetch", "-q")
	k.git(r, "merge", "-q", "--no-edit", "origin/main")
	t.Setenv(kbEnv, r)
	if code := Main([]string{"resolve"}); code != 0 {
		t.Fatalf("resolve exit %d", code)
	}
	ids := map[string]string{}
	for _, e := range mustLoadOverrideLog(r) {
		ids[entryStr(e, "renamed_from")] = entryStr(e, "new_id")
	}
	one, two := ids["attested/S-7K2M-one.md"], ids["attested/S-7K2M-two.md"]
	if len(duplicateIDs(r)) != 0 || one == "" || two == "" || one == two {
		t.Fatalf("not repaired three ways: %v", ids)
	}
	if !strings.Contains(k.read("attested/S-CIT1-cites-one.md"), "Per "+one+".") ||
		!strings.Contains(k.read("attested/S-CIT2-cites-two.md"), "Per "+two+".") {
		t.Errorf("citations did not follow their statements")
	}
}

// TestRepairLeavesSharedLogLines: an entry naming the moving statement is not rewritten in the log, which
// other copies hold; the repair lists it, and the log reads it under the new id.
func TestRepairLeavesSharedLogLines(t *testing.T) {
	k := newGitKB(t)
	k.write(k.other, "attested/S-7K2M-shared.md", stmt("S-7K2M", "Shared", "2026-10-02T10:00:00+00:00", "Shared."))
	k.git(k.other, "add", "-A")
	k.git(k.other, "commit", "-q", "-m", "shared")
	k.git(k.other, "push", "-q")
	k.write(k.root, "attested/S-7K2M-unshared.md", stmt("S-7K2M", "Unshared", "2026-10-03T10:00:00+00:00", "Unshared."))
	line := `{"id": "O-RVS1", "kind": "revise", "at": "2026-10-03T11:00:00+00:00", "statement": "S-7K2M"}`
	k.write(k.root, overrideLogName, line+"\n")
	k.git(k.root, "add", "-A")
	k.git(k.root, "commit", "-q", "-m", "unshared, and a revision of it")
	k.git(k.root, "fetch", "-q")
	k.git(k.root, "merge", "-q", "--no-edit", "origin/main")
	t.Setenv(kbEnv, k.root)
	Main([]string{"resolve"})
	if first := strings.SplitN(k.read(overrideLogName), "\n", 2)[0]; first != line {
		t.Errorf("the earlier line was rewritten: %s", first)
	}
	var revise, repair *dyn.Dict
	for _, e := range mustLoadOverrideLog(k.root) {
		switch entryStr(e, "kind") {
		case ovRevise:
			revise = e
		case ovRepairID:
			repair = e
		}
	}
	if repair == nil || revise == nil || entryStr(revise, "statement") != entryStr(repair, "new_id") ||
		!dyn.Equal(repair.Get("moved_entries"), []any{"O-RVS1"}) {
		t.Errorf("the revision is not read under the new id: %v %v", revise, repair)
	}
	if strings.Contains(k.read(overrideLogName), "\r") {
		t.Errorf("the log has CRLF line endings")
	}
}

// TestAsMadeChain: a statement whose ids two repairs rewrote, the second after renaming it, reads back as
// it was made.
func TestAsMadeChain(t *testing.T) {
	entries := []*dyn.Dict{
		dyn.D("kind", ovRepairID, "at", "2026-10-04T10:00:00+00:00", "old_id", "S-AAAA", "new_id", "S-BBBB",
			"renamed_from", "attested/S-CCCC-x.md", "renamed_to", "attested/S-DDDD-x.md", "rewritten", []any{"attested/S-CCCC-x.md"}),
		dyn.D("kind", ovRepairID, "at", "2026-10-04T10:00:00+00:00", "old_id", "S-CCCC", "new_id", "S-DDDD",
			"renamed_from", "attested/S-CCCC-x.md", "renamed_to", "attested/S-DDDD-x.md", "rewritten", []any{"attested/S-DDDD-x.md"}),
	}
	st := &Statement{Path: "attested/S-DDDD-x.md", Body: "I, S-DDDD, rest on S-BBBB."}
	if got := asMade(st, entries); got != "I, S-CCCC, rest on S-AAAA." {
		t.Errorf("asMade = %q", got)
	}
}

// TestSharedRefsRebase: during a rebase, the commit it is onto is a shared ref, since HEAD is detached.
func TestSharedRefsRebase(t *testing.T) {
	k := newGitKB(t)
	head, _ := git(k.root, "rev-parse", "HEAD")
	p, _ := git(k.root, "rev-parse", "--git-path", "rebase-merge")
	if !filepath.IsAbs(p) {
		p = filepath.Join(k.root, p)
	}
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "onto"), []byte(head+"\n"), 0o644)
	refs := sharedRefs(k.root)
	if refs[0] != "refs/remotes/origin/HEAD" || !slices.Contains(refs, head) {
		t.Errorf("shared refs %v", refs)
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
