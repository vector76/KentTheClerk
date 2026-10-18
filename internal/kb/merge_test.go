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
		"originator", "jamie", "cites", []any{}, "attestations", atts)
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
	// a revision meeting an endorsement goes to the human (TestMergeReviseMeetsEndorse)
	if _, problems := mergeStatementText(base, revised, endorsed); len(problems) == 0 {
		t.Errorf("a revision meeting an endorsement was merged")
	}
	// a change to the text with nothing added on the other side is taken
	if out, problems := mergeStatementText(base, revised, base); len(problems) > 0 || !strings.Contains(out, "The revised text.") {
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
	inMeta := strings.Replace(base, "cites: []", "<<<<<<< ours\ncites: []\n=======\ncites: [x]\n>>>>>>> theirs", 1)
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
	// 1 is a merge that stopped on a conflict; anything else, such as 128 for a missing identity, is not
	if out, st := git(root, "-c", "user.name=t", "-c", "user.email=t@t", "merge", "-q", "other"); st != 1 {
		t.Fatalf("git merge: %d, want 1 for a conflict: %s", st, out)
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

// expectMeta is the statement file kb writes for the meta and text, the bytes a merge must give.
func expectMeta(t *testing.T, got string, meta *dyn.Dict, body string) {
	t.Helper()
	if want := dumpFrontmatter(meta) + "\n" + body + "\n"; got != want {
		t.Errorf("merged file:\n%s\nwant:\n%s", got, want)
	}
}

func baseMeta(atts []any, extra ...any) *dyn.Dict {
	meta := dyn.D("id", "S-7K2M", "title", "A decision", "answers", "what was decided",
		"originator", "jamie", "cites", []any{}, "attestations", atts)
	for i := 0; i+1 < len(extra); i += 2 {
		meta.Set(extra[i].(string), extra[i+1])
	}
	return meta
}

// TestMergeBytes: two endorsements and a mark merge to exactly the file kb would write.
func TestMergeBytes(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	carol := attestation("carol", "2026-10-16T11:00:00+00:00", "endorsement")
	out, problems := mergeStatementText(statementText("The text.", []any{author}),
		statementText("The text.", []any{author, bob}, "amended_by", []any{"S-Z5P4"}),
		statementText("The text.", []any{author, carol}))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	expectMeta(t, out, baseMeta([]any{author, carol, bob}, "amended_by", []any{"S-Z5P4"}), "The text.")
}

// TestMergeReviseMeetsEndorse: one side changed the text, title or answers, and the other added to the
// statement as it was; that goes to the human.
func TestMergeReviseMeetsEndorse(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	base := statementText("The text.", []any{author})
	for name, changed := range map[string]string{
		"text":    statementText("The revised text.", []any{author}),
		"title":   strings.Replace(base, "title: A decision", "title: Another", 1),
		"answers": strings.Replace(base, "answers: what was decided", "answers: another question", 1),
	} {
		for what, added := range map[string]string{
			"an endorsement": statementText("The text.", []any{author, bob}),
			"a mark":         statementText("The text.", []any{author}, "clarified_by", []any{"S-Z5P4"}),
			"a supersession": statementText("The text.", []any{author}, "superseded_by", "S-P3KB"),
		} {
			if _, problems := mergeStatementText(base, changed, added); len(problems) == 0 {
				t.Errorf("a change to the %s met by %s was merged", name, what)
			}
		}
	}
}

// TestMergeInPlaceEdit: an attestation edited in place on one side, as a reassignment does, keeps its
// place, so the author stays first.
func TestMergeInPlaceEdit(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	moved := attestation("jamie2", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	moved.Set("turn", author.Get("turn"))
	bob := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	out, problems := mergeStatementText(statementText("The text.", []any{author}),
		statementText("The text.", []any{moved}), statementText("The text.", []any{author, bob}))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	expectMeta(t, out, baseMeta([]any{moved, bob}), "The text.")
}

// TestMergeDedupe: the same person endorsing in two copies, or one seen turn recorded at different
// seconds, is kept once, the earliest.
func TestMergeDedupe(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	bob1 := attestation("bob", "2026-10-16T12:00:00+00:00", "endorsement")
	bob2 := attestation("bob", "2026-10-16T13:00:00+00:00", "endorsement")
	bob2.Set("turn", "aaaaaaaa#cccccccc")
	out, _ := mergeStatementText(statementText("The text.", []any{author}),
		statementText("The text.", []any{author, bob2}), statementText("The text.", []any{author, bob1}))
	expectMeta(t, out, baseMeta([]any{author, bob1}), "The text.")

	seen1 := dyn.D("turn", "aaaaaaaa#dddddddd", "at", "2026-10-16T12:00:00+00:00")
	seen2 := dyn.D("turn", "aaaaaaaa#dddddddd", "at", "2026-10-16T12:00:05+00:00")
	out, _ = mergeStatementText(statementText("The text.", []any{author}, "seen", []any{}),
		statementText("The text.", []any{author}, "seen", []any{seen2}),
		statementText("The text.", []any{author}, "seen", []any{seen1}))
	expectMeta(t, out, baseMeta([]any{author}, "seen", []any{seen1}), "The text.")
}

// TestMergeListAddedEmpty: a list one side added, even empty, is kept.
func TestMergeListAddedEmpty(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	base := statementText("The text.", []any{author})
	out, _ := mergeStatementText(base, statementText("The text.", []any{author}, "seen", []any{}), base)
	expectMeta(t, out, baseMeta([]any{author}, "seen", []any{}), "The text.")
}

// TestMergeEmptySide: an empty side is reported as empty, not as a different statement.
func TestMergeEmptySide(t *testing.T) {
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	base := statementText("The text.", []any{author})
	if _, problems := mergeStatementText(base, "", base); len(problems) != 1 || !strings.Contains(problems[0], "empty") {
		t.Errorf("empty side: %q", problems)
	}
}

// TestConflictMarkers: runs of seven or more, start and end both; a lone line of = signs is not one.
func TestConflictMarkers(t *testing.T) {
	if !hasConflictMarkers("<<<<<<<< HEAD\na\n========\nb\n>>>>>>>> theirs\n") {
		t.Errorf("eight-character markers missed")
	}
	if hasConflictMarkers("A heading\n=======\n\nText.\n") {
		t.Errorf("a Markdown heading underline taken for a conflict")
	}
}

// TestReaderRefuses: the reader refuses a file holding conflict markers and a line that is not key: value,
// and kb check says which.
func TestReaderRefuses(t *testing.T) {
	if _, err := readStatement("x", "---\nid: S-7K2M\n<<<<<<< ours\ntitle: a\n=======\ntitle: b\n>>>>>>> theirs\n---\n\nText.\n"); err == nil ||
		!strings.Contains(err.Error(), "conflict markers") {
		t.Errorf("markers: %v", err)
	}
	if _, err := readStatement("x", "---\nid: S-7K2M\njust words\n---\n\nText.\n"); err == nil {
		t.Errorf("a line with no colon was read")
	}
	root := newTestKB(t)
	os.WriteFile(filepath.Join(root, "attested", "S-7K2M-x.md"),
		[]byte("---\n<<<<<<< ours\nid: S-7K2M\n=======\nid: S-7K2N\n>>>>>>> theirs\n---\n\nText.\n"), 0o644)
	t.Setenv(kbEnv, root)
	if out, code := kbRun(t, "check"); !strings.Contains(out, "S-7K2M-x.md: it holds conflict markers") || code != 1 {
		t.Errorf("kb check exit %d:\n%s", code, out)
	}
}

// TestWriterRoundTrip: values with line breaks or control characters, a bool-like string and an empty
// dict in a list read back as written.
func TestWriterRoundTrip(t *testing.T) {
	meta := dyn.D("id", "S-7K2M", "reason", "one\ntwo\rthree\vfour\ffive\x1csix\x85seven eight nine",
		"title", "true", "seen", []any{dyn.NewDict(), dyn.D("turn", "a#b", "at", "x")})
	text := dumpFrontmatter(meta) + "\nText.\n"
	got, err := readStatement("x", text)
	if err != nil {
		t.Fatal(err)
	}
	if !dyn.Equal(got.Meta, meta) {
		t.Errorf("read back %s\nfrom\n%s", dyn.Dumps(got.Meta), text)
	}
}

// TestUnionMergeEdges: no final newline, an empty base, and binary input, which is an error.
func TestUnionMergeEdges(t *testing.T) {
	got, err := unionMerge("", "a", "b\n")
	if err != nil {
		t.Skip("git is not available")
	}
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("empty base, no final newline: %q", got)
	}
	if got, err := unionMerge("x\n", "x\na", "x\nb"); err != nil || !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("no final newline: %q %v", got, err)
	}
	if _, err := unionMerge("a\x00b\n", "a\x00c\n", "a\x00d\n"); err == nil {
		t.Errorf("binary input was merged")
	}
}

// TestDriverLineEndings: CRLF only where both sides have it, so the bytes do not depend on the side.
func TestDriverLineEndings(t *testing.T) {
	dir := t.TempDir()
	author := attestation("jamie", "2026-10-16T10:00:00+00:00", "proposed-and-agreed")
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	run := func(a, b string) string {
		base := write("O", statementText("The text.", []any{author}))
		ours := write("A", a)
		theirs := write("B", b)
		if code := Main([]string{"merge-driver", base, ours, theirs, "attested/S-7K2M-a.md"}); code != 0 {
			t.Fatalf("driver exit %d", code)
		}
		out, _ := os.ReadFile(ours)
		return string(out)
	}
	a := statementText("The text.", []any{author}, "superseded_by", "S-P3KB")
	b := statementText("The text.", []any{author}, "amended_by", []any{"S-Z5P4"})
	if got := run(crlf(a), crlf(b)); !strings.Contains(got, "\r\n") {
		t.Errorf("both CRLF gave LF")
	}
	one, other := run(crlf(a), b), run(b, crlf(a))
	if one != other || strings.Contains(one, "\r") {
		t.Errorf("one side CRLF depends on the side, or kept CRLF:\n%q\n%q", one, other)
	}
}

// TestDriverOtherFile: a file kb does not merge, as a README among the statements, gets git's own text
// merge: merged when it can be, conflicted when not.
func TestDriverOtherFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	base := write("O", "one\ntwo\nthree\n")
	ours := write("A", "ONE\ntwo\nthree\n")
	theirs := write("B", "one\ntwo\nTHREE\n")
	if code := Main([]string{"merge-driver", base, ours, theirs, "attested/README.md"}); code != 0 {
		t.Fatalf("a clean text merge exited %d", code)
	}
	if b, _ := os.ReadFile(ours); string(b) != "ONE\ntwo\nTHREE\n" {
		t.Errorf("text merge: %q", b)
	}
}

// TestUserConflicts: two rows for one key, as two copies adding it with different emails leave, are
// reported for the human.
func TestUserConflicts(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, usersName), []byte(
		`{"key": "sam", "name": "Sam", "email": "sam@a.example"}`+"\n"+
			`{"key": "sam", "name": "Sam", "email": "sam@b.example"}`+"\n"), 0o644)
	if got := userConflicts(root); len(got) != 1 || !strings.Contains(got[0], "the key sam has 2 rows") {
		t.Errorf("user conflicts: %q", got)
	}
}
