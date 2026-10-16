package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kenttheclerk/internal/dyn"
)

func attestation(by, at, form string) *dyn.Dict {
	return dyn.D("by", by, "at", at, "turn", "aaaaaaaa#bbbbbbbb-"+by, "form", form)
}

// statementText is a statement file as kb writes it, with extra fields set after the attestations.
func statementText(body string, atts []any, extra ...any) string {
	meta := dyn.D("id", "S-7K2M", "title", "A decision", "answers", "what was decided",
		"originator", "jamie", "cites", []any{}, "topics", []any{}, "attestations", atts)
	for i := 0; i+1 < len(extra); i += 2 {
		meta.Set(extra[i].(string), extra[i+1])
	}
	return dumpFrontmatter(meta) + "\n" + body + "\n"
}

func mustMerge(t *testing.T, base, ours, theirs string) *dyn.Dict {
	t.Helper()
	out, problems := mergeStatementText(base, ours, theirs)
	if len(problems) > 0 {
		t.Fatalf("not merged: %q", problems)
	}
	meta, _, err := parseStatementText(out)
	if err != nil {
		t.Fatalf("merged file does not read: %v\n%s", err, out)
	}
	return meta
}

// TestMergeTwoEndorsements: each copy endorsed the statement; the merge keeps both attestations, in the
// order they were made, whichever side is ours.
func TestMergeTwoEndorsements(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	carol := attestation("carol", "2026-10-16T11:00:00+00:00", "endorsement")
	base := statementText("The text.", []any{author})
	ours := statementText("The text.", []any{author, bob})
	theirs := statementText("The text.", []any{author, carol})
	for _, pair := range [][2]string{{ours, theirs}, {theirs, ours}} {
		atts := listOf(mustMerge(t, base, pair[0], pair[1]).Get("attestations"))
		var by []string
		for _, a := range atts {
			by = append(by, dyn.Str(a.(*dyn.Dict).Get("by")))
		}
		if strings.Join(by, ",") != "jamie,carol,bob" {
			t.Errorf("attestations by %v, want jamie, carol, bob", by)
		}
	}
}

// TestMergeSupersessionMark: one copy superseded the statement while the other amended and endorsed it, so
// both sides added marks at the end of the frontmatter; the merge keeps every mark. Two different
// supersessions are a conflict kb does not guess at.
func TestMergeSupersessionMark(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	base := statementText("The text.", []any{author})
	ours := statementText("The text.", []any{author}, "superseded_by", "S-P3KB")
	theirs := statementText("The text.", []any{author, bob}, "amended_by", []any{"S-Z5P4"}, "extra_context", true)
	m := mustMerge(t, base, ours, theirs)
	if m.Get("superseded_by") != "S-P3KB" || !dyn.Equal(m.Get("amended_by"), []any{"S-Z5P4"}) ||
		m.Get("extra_context") != true || len(listOf(m.Get("attestations"))) != 2 {
		t.Errorf("marks not all kept: %s", dumpFrontmatter(m))
	}
	both := statementText("The text.", []any{author}, "superseded_by", "S-P3KB")
	if m := mustMerge(t, base, ours, both); m.Get("superseded_by") != "S-P3KB" {
		t.Errorf("one supersession made in both copies: %s", dumpFrontmatter(m))
	}
	other := statementText("The text.", []any{author}, "superseded_by", "S-MBME")
	if _, problems := mergeStatementText(base, ours, other); len(problems) != 1 || !strings.Contains(problems[0], "superseded_by") {
		t.Errorf("two different supersessions merged: %q", problems)
	}
	amendBoth := statementText("The text.", []any{author}, "amended_by", []any{"S-JNZA"})
	if m := mustMerge(t, base, theirs, amendBoth); !dyn.Equal(m.Get("amended_by"), []any{"S-Z5P4", "S-JNZA"}) {
		t.Errorf("amendments from both copies: %v", m.Get("amended_by"))
	}
}

// TestMergeText: a change to the text on one side is taken; two different changes, to the text or to a
// field such as the title, are a conflict.
func TestMergeText(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	base := statementText("The text.", []any{author})
	revised := statementText("The revised text.", []any{author})
	endorsed := statementText("The text.", []any{author, bob})
	out, problems := mergeStatementText(base, revised, endorsed)
	if len(problems) > 0 || !strings.Contains(out, "The revised text.") || !strings.Contains(out, "by: bob") {
		t.Errorf("one-sided text change: %q\n%s", problems, out)
	}
	other := statementText("Another text.", []any{author})
	if _, problems := mergeStatementText(base, revised, other); len(problems) != 1 || !strings.Contains(problems[0], "text") {
		t.Errorf("two text changes: %q", problems)
	}
	retitled := strings.Replace(base, "title: A decision", "title: Another", 1)
	retitled2 := strings.Replace(base, "title: A decision", "title: A third", 1)
	if _, problems := mergeStatementText(base, retitled, retitled2); len(problems) != 1 || !strings.Contains(problems[0], "title") {
		t.Errorf("two title changes: %q", problems)
	}
	conflicted := "<<<<<<< ours\n" + base + "=======\n" + endorsed + ">>>>>>> theirs\n"
	if _, problems := mergeStatementText(base, conflicted, endorsed); len(problems) == 0 {
		t.Errorf("a side holding conflict markers was merged")
	}
}

func TestMergeLines(t *testing.T) {
	got := mergeLinesText("a\n", "a\nb\n", "a\nc\nb\n")
	if got != "a\nb\nc\n" {
		t.Errorf("union merge = %q", got)
	}
}

func TestRecordFileKind(t *testing.T) {
	cases := map[string]string{
		"attested/S-7K2M-a-decision.md": "statement", "agent/A-KF7W-x.md": "statement",
		"users.jsonl": "lines", "overrides.jsonl": "lines", "evidence/abc.jsonl": "", "README.md": "",
	}
	for p, want := range cases {
		if got := recordFileKind(p); got != want {
			t.Errorf("recordFileKind(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestMergeDriver runs the driver as git would, on a file it can merge and on one it cannot.
func TestMergeDriver(t *testing.T) {
	dir := t.TempDir()
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	base := write("O", statementText("The text.", []any{author}))
	ours := write("A", statementText("The text.", []any{author}, "superseded_by", "S-P3KB"))
	theirs := write("B", statementText("The text.", []any{author}, "amended_by", []any{"S-Z5P4"}))
	if code := Main([]string{"merge-driver", base, ours, theirs, "kent/attested/S-7K2M-a-decision.md"}); code != 0 {
		t.Fatalf("driver exit %d", code)
	}
	b, _ := os.ReadFile(ours)
	if !strings.Contains(string(b), "superseded_by: S-P3KB") || !strings.Contains(string(b), "S-Z5P4") {
		t.Errorf("driver result:\n%s", b)
	}
	write("A", statementText("Ours.", []any{author}))
	write("B", statementText("Theirs.", []any{author}))
	if code := Main([]string{"merge-driver", base, ours, theirs, "attested/S-7K2M-a-decision.md"}); code == 0 {
		t.Errorf("driver merged two different texts")
	}
}

// TestResolve merges, with kb resolve, a statement git left conflicted because no driver was registered.
func TestResolve(t *testing.T) {
	root := newTestKB(t)
	g := func(args ...string) {
		t.Helper()
		if out, st := git(root, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); st != 0 {
			t.Fatalf("git %v: %d %s", args, st, out)
		}
	}
	if _, st := git(root, "init", "-q", "-b", "main"); st != 0 {
		t.Skip("git is not available")
	}
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	file := filepath.Join(root, "attested", "S-7K2M-a-decision.md")
	set := func(text string) { os.WriteFile(file, []byte(text), 0o644) }
	set(statementText("The text.", []any{author}))
	g("add", "-A")
	g("commit", "-q", "-m", "base")
	g("checkout", "-q", "-b", "other")
	set(statementText("The text.", []any{author}, "amended_by", []any{"S-Z5P4"}))
	g("commit", "-q", "-am", "amend")
	g("checkout", "-q", "main")
	set(statementText("The text.", []any{author}, "superseded_by", "S-P3KB"))
	g("commit", "-q", "-am", "supersede")
	if _, st := git(root, "merge", "-q", "other"); st == 0 {
		t.Fatalf("git merged without a conflict")
	}
	t.Setenv(kbEnv, root)
	if code := Main([]string{"resolve", "attested/S-0001-not-conflicted.md"}); code == 0 {
		t.Errorf("resolve accepted a file git does not report as conflicted")
	}
	if code := Main([]string{"resolve"}); code != 0 {
		t.Fatalf("resolve exit %d", code)
	}
	b, _ := os.ReadFile(file)
	if strings.Contains(string(b), "<<<<<<<") || !strings.Contains(string(b), "superseded_by: S-P3KB") ||
		!strings.Contains(string(b), "S-Z5P4") {
		t.Errorf("resolved file:\n%s", b)
	}
}
