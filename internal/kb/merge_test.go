package kb

import (
	"os"
	"path/filepath"
	"runtime"
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
	if m := mustMerge(t, base, theirs, amendBoth); !dyn.Equal(m.Get("amended_by"), []any{"S-JNZA", "S-Z5P4"}) {
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

// TestUnionMerge: the logs merge as git's union merge does, theirs' changes and deletions included.
func TestUnionMerge(t *testing.T) {
	got, err := unionMerge("a\nb\nc\n", "a\nb\nc\nours\n", "a\nc\ntheirs\n")
	if err != nil {
		t.Skip("git is not available")
	}
	if got != "a\nc\nours\ntheirs\n" {
		t.Errorf("union merge = %q", got)
	}
}

// TestMergeRefusesMarkers: a side with conflict markers anywhere, the text included, is never merged.
func TestMergeRefusesMarkers(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	base := statementText("The text.", []any{author})
	inBody := statementText("<<<<<<< ours\nThe text.\n=======\nOther.\n>>>>>>> theirs", []any{author})
	if _, problems := mergeStatementText(base, inBody, base); len(problems) == 0 {
		t.Errorf("markers in the text were taken as a change")
	}
	inMeta := strings.Replace(base, "topics: []", "<<<<<<< ours\ntopics: []\n=======\ntopics: [x]\n>>>>>>> theirs", 1)
	if _, problems := mergeStatementText(base, base, inMeta); len(problems) == 0 {
		t.Errorf("markers in the frontmatter were parsed as keys")
	}
}

// TestMergeLineEndings: a side with CRLF line endings merges with an LF side, without doubled blank lines.
func TestMergeLineEndings(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	base := statementText("One.\n\nTwo.", []any{author})
	crlf := strings.ReplaceAll(statementText("One.\n\nTwo.", []any{author, bob}), "\n", "\r\n")
	out, problems := mergeStatementText(base, crlf, base)
	if len(problems) > 0 || !strings.Contains(out, "One.\n\nTwo.\n") || strings.Contains(out, "\r") {
		t.Errorf("CRLF side: %q\n%q", problems, out)
	}
}

// TestMergeEitherWay: ours and theirs swapped give the same bytes, string lists, ties and new keys included.
func TestMergeEitherWay(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	carol := attestation("carol", "2026-10-16T12:00:00+00:00", "endorsement")
	base := statementText("The text.", []any{author})
	a := statementText("The text.", []any{author, bob}, "amended_by", []any{"S-ZZZZ"}, "extra_context", true)
	b := statementText("The text.", []any{author, carol}, "amended_by", []any{"S-AAAA"}, "superseded_by", "S-P3KB")
	ab, p1 := mergeStatementText(base, a, b)
	ba, p2 := mergeStatementText(base, b, a)
	if len(p1)+len(p2) > 0 || ab != ba {
		t.Errorf("depends on the side: %q %q\n%s\n---\n%s", p1, p2, ab, ba)
	}
}

// TestMergeDeletedList: a list field one side deleted does not come back empty.
func TestMergeDeletedList(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	base := statementText("The text.", []any{author}, "seen", []any{})
	gone := statementText("The text.", []any{author})
	if out, _ := mergeStatementText(base, gone, base); strings.Contains(out, "seen:") {
		t.Errorf("a deleted list came back:\n%s", out)
	}
}

// TestMergeWithdrawnAndSuperseded: one side withdrew an agent statement, the other superseded it.
func TestMergeWithdrawnAndSuperseded(t *testing.T) {
	agent := func(extra ...any) string {
		meta := dyn.D("id", "A-7K2M", "title", "A choice", "answers", "a question", "author", "agent:x",
			"made_in", "aaaaaaaa#bbbbbbbb", "recorded_at", "2026-10-16T10:00:00+00:00", "seen", []any{})
		for i := 0; i+1 < len(extra); i += 2 {
			meta.Set(extra[i].(string), extra[i+1])
		}
		return dumpFrontmatter(meta) + "\nThe choice.\n"
	}
	_, problems := mergeStatementText(agent(), agent("withdrawn_in", "aaaaaaaa#cccccccc", "withdrawn_reason", "x"),
		agent("superseded_by", "A-P3KB"))
	if len(problems) == 0 {
		t.Errorf("a withdrawal and a supersession were both taken")
	}
}

// TestQuoteBoolLike: a title that reads like a bool or null reads back as text.
func TestQuoteBoolLike(t *testing.T) {
	for _, s := range []string{"true", "false", "null", "~"} {
		meta, _, err := parseStatementText(dumpFrontmatter(dyn.D("id", "S-7K2M", "title", s)) + "\nText.\n")
		if err != nil || meta.Get("title") != s {
			t.Errorf("title %q read back as %#v (%v)", s, meta.Get("title"), err)
		}
	}
}

// TestResolvePathForms: kb resolve takes a file relative to the current folder, or with a drive letter in
// another case.
func TestResolvePathForms(t *testing.T) {
	root := t.TempDir()
	conflicted := map[string]bool{"attested/S-7K2M-a.md": true}
	if r, ok := askedFile(root, filepath.Join(root, "attested", "S-7K2M-a.md"), conflicted); !ok || r != "attested/S-7K2M-a.md" {
		t.Errorf("absolute: %q %v", r, ok)
	}
	if r, ok := askedFile(root, "attested/S-7K2M-a.md", conflicted); !ok || r != "attested/S-7K2M-a.md" {
		t.Errorf("relative to the knowledge base: %q %v", r, ok)
	}
	old, _ := os.Getwd()
	os.Chdir(filepath.Join(root))
	os.MkdirAll(filepath.Join(root, "attested"), 0o755)
	os.Chdir(filepath.Join(root, "attested"))
	defer os.Chdir(old)
	if r, ok := askedFile(root, "S-7K2M-a.md", conflicted); !ok || r != "attested/S-7K2M-a.md" {
		t.Errorf("relative to the current folder: %q %v", r, ok)
	}
	if runtime.GOOS == "windows" && len(root) > 1 && root[1] == ':' {
		other := strings.ToLower(root[:1]) + root[1:]
		if root[:1] == strings.ToLower(root[:1]) {
			other = strings.ToUpper(root[:1]) + root[1:]
		}
		if _, ok := relInside(root, filepath.Join(other, "attested", "S-7K2M-a.md")); !ok {
			t.Errorf("a drive letter in another case is not inside")
		}
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
	if code := Main([]string{"merge-driver", base, ours, theirs, "attested/S-7K2M-a-decision.md", "merged common ancestors",
		"HEAD", "feature"}); code == 0 {
		t.Errorf("driver merged two different texts")
	}
	if b, _ := os.ReadFile(ours); !strings.Contains(string(b), "<<<<<<< HEAD") || !strings.Contains(string(b), ">>>>>>> feature") {
		t.Errorf("the conflict does not carry git's labels:\n%s", b)
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
