package kb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kenttheclerk/internal/py"
)

const testSession = "aaaaaaaa-0000-4000-8000-000000000001"

// testTurns are the turns of the on-record session the tests attest and record from: an agent's proposal
// followed by a human's agreement, several times over, and an agent turn stating a choice.
var testTurns = []struct{ uuid, role, author, text string }{
	{"11111111-0000-4000-8000-000000000001", "assistant", "agent:test", "Statement one text."},
	{"11111111-0000-4000-8000-000000000002", "user", "alice", "Agreed."},
	{"11111111-0000-4000-8000-000000000003", "assistant", "agent:test", "Statement two text."},
	{"11111111-0000-4000-8000-000000000004", "user", "bob", "Agreed."},
	{"11111111-0000-4000-8000-000000000005", "assistant", "agent:test", "Statement three text."},
	{"11111111-0000-4000-8000-000000000006", "user", "alice", "Agreed."},
	{"11111111-0000-4000-8000-000000000007", "assistant", "agent:test", "Agent choice text."},
	{"11111111-0000-4000-8000-000000000008", "assistant", "agent:test", "Statement four text."},
	{"11111111-0000-4000-8000-000000000009", "user", "bob", "Agreed."},
}

func turnPtr(i int) string { return testSession + "#" + testTurns[i].uuid }

// newTestKB makes an empty knowledge base holding testSession, and points kb at it.
func newTestKB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"attested", "agent", "evidence", "off-record/drafts"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	hdr, _ := json.Marshal(map[string]any{"type": "kb-snapshot", "session_id": testSession, "mode": "on-record"})
	b.Write(hdr)
	b.WriteString("\n")
	for i, tt := range testTurns {
		rec, _ := json.Marshal(map[string]any{"uuid": tt.uuid, "role": tt.role, "author": tt.author,
			"timestamp": fmt.Sprintf("2026-10-14T10:00:%02d.000Z", i),
			"content":   []any{map[string]any{"type": "text", "text": tt.text}}})
		b.Write(rec)
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(root, "evidence", "2026-10-14-aaaaaaaa.jsonl"), []byte(b.String()), 0o666); err != nil {
		t.Fatal(err)
	}
	t.Setenv(kbEnv, root)
	// the people who act in testSession add themselves first, as everyone does (S-0140, S-0142)
	mustRun(t, "user", "add", "Alice Adams", "--email", "alice@example.com", "--key", "alice", "--user", "alice")
	mustRun(t, "user", "add", "Bob Brown", "--email", "bob@example.com", "--key", "bob", "--user", "bob")
	return root
}

// kbRun runs one kb command and returns what it printed and its exit status.
func kbRun(t *testing.T, args ...string) (out string, code int) {
	t.Helper()
	var buf bytes.Buffer
	oldOut, oldErr := Stdout, Stderr
	Stdout, Stderr = &buf, &buf
	defer func() {
		Stdout, Stderr = oldOut, oldErr
		if r := recover(); r != nil {
			switch e := r.(type) {
			case exitCode:
				code = int(e)
			case *py.Exception:
				t.Fatalf("kb %s: unexpected %s: %s\n%s", strings.Join(args, " "), e.Type, e.Msg, buf.String())
			default:
				panic(r)
			}
		}
		out = buf.String()
	}()
	ns, sub, exit := newParser().Parse(args, &buf, &buf)
	if exit != nil {
		return buf.String(), exit.Code
	}
	return buf.String(), sub.Run(ns)
}

func writeDraft(t *testing.T, root, name, front, body string) string {
	t.Helper()
	p := filepath.Join("off-record", "drafts", name+".md")
	if err := os.WriteFile(filepath.Join(root, p), []byte("---\n"+front+"\n---\n\n"+body+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, code := kbRun(t, args...)
	if code != 0 {
		t.Fatalf("kb %s exited %d:\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

func mustRefuse(t *testing.T, want string, args ...string) {
	t.Helper()
	out, code := kbRun(t, args...)
	if code == 0 {
		t.Fatalf("kb %s succeeded; expected a refusal mentioning %q:\n%s", strings.Join(args, " "), want, out)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("kb %s refused without mentioning %q:\n%s", strings.Join(args, " "), want, out)
	}
}

func statementMeta(t *testing.T, root, id string) *py.Dict {
	t.Helper()
	p := statementPath(root, id)
	if p == "" {
		t.Fatalf("%s not found", id)
	}
	return loadStatement(p).Meta
}

const front = "title: %s\nanswers: a test question; a decision"

// attestOne makes S-0001 by alice from the first proposal.
func attestOne(t *testing.T, root string) {
	t.Helper()
	d := writeDraft(t, root, "one", fmt.Sprintf(front, "One"), "Statement one text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(0), "--turn", turnPtr(1), "--user", "alice")
}

func TestOnlyTheAuthorSupersedes(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	for _, rel := range relations {
		d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\n"+rel+": S-0001", "Statement two text.")
		mustRefuse(t, "Kent Rule 46", "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
		out, _ := kbRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
		if !strings.Contains(out, "context_for") {
			t.Errorf("the refusal of %s does not point to context_for:\n%s", rel, out)
		}
	}
	if statementPath(root, "S-0002") != "" {
		t.Fatal("a refused supersession wrote a statement")
	}
	// the author may
	d := writeDraft(t, root, "three", fmt.Sprintf(front, "Three")+"\nsupersedes: S-0001", "Statement three text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--user", "alice")
	if got := statementMeta(t, root, "S-0001").Get("superseded_by"); got != "S-0002" {
		t.Fatalf("S-0001 superseded_by = %v", got)
	}
	// and any human may supersede an agent statement
	a := writeDraft(t, root, "choice", "title: Choice\nanswers: a test choice", "Agent choice text.")
	mustRun(t, "record", a, "--turn", turnPtr(6))
	d = writeDraft(t, root, "four", fmt.Sprintf(front, "Four")+"\nsupersedes: A-0001", "Statement four text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(7), "--turn", turnPtr(8), "--user", "bob")
	if out := mustRun(t, "check"); !strings.Contains(out, ": 0 error(s)") {
		t.Fatalf("check:\n%s", out)
	}
}

func TestContextForMarksTheTarget(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	note := `says "one" holds only before the migration: a qualification`
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\ncontext_for:\n  - statement: S-0001\n    note: "+py.JSONStr(note), "Statement two text.")
	out := mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	if !strings.Contains(out, "S-0001 marked extra_context (context from S-0002)") {
		t.Errorf("attest did not report the mark:\n%s", out)
	}
	one := statementMeta(t, root, "S-0001")
	if one.Get("extra_context") != true {
		t.Fatalf("S-0001 extra_context = %v", one.Get("extra_context"))
	}
	if b, _ := os.ReadFile(statementPath(root, "S-0001")); strings.Contains(string(b), "S-0002") || strings.Contains(string(b), "migration") {
		t.Fatalf("the linking statement or its note leaked into the target:\n%s", b)
	}
	links := contextLinks(statementMeta(t, root, "S-0002"))
	if len(links) != 1 || links[0].target != "S-0001" || links[0].note != note {
		t.Fatalf("S-0002 context_for = %+v", links)
	}

	// an agent statement may be context for a human statement; the mark is set once, not counted
	a := writeDraft(t, root, "choice", "title: Choice\nanswers: a test choice\ncontext_for:\n  - statement: S-0001\n    note: reads it for the recorder", "Agent choice text.")
	out = mustRun(t, "record", a, "--turn", turnPtr(6))
	if !strings.Contains(out, "S-0001 already marked extra_context") {
		t.Errorf("record did not report the existing mark:\n%s", out)
	}
	// rewriting the file keeps the note intact (quotes and colons survive a round trip)
	mustRun(t, "attest", "--form", "endorsement", "--statement", "S-0002", "--turn", turnPtr(1), "--user", "alice")
	if got := contextLinks(statementMeta(t, root, "S-0002"))[0].note; got != note {
		t.Fatalf("note after a rewrite = %q, want %q", got, note)
	}
	if out := mustRun(t, "check"); !strings.Contains(out, ": 0 error(s)") {
		t.Fatalf("check:\n%s", out)
	}
}

func TestContextForRefusals(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	a := writeDraft(t, root, "choice", "title: Choice\nanswers: a test choice", "Agent choice text.")
	mustRun(t, "record", a, "--turn", turnPtr(6))
	cases := []struct{ entries, want string }{
		{"  - statement: S-0009\n    note: x", "S-0009 does not exist"},
		{"  - statement: S-0001", "has no note"},
		{"  - S-0001", "has no note"},
		{"  - statement: S-0001\n    note: " + strings.Repeat("n", contextNoteLimit+1), fmt.Sprintf("at most %d", contextNoteLimit)},
		{"  - statement: S-0001\n    note: a\n  - statement: S-0001\n    note: b", "named twice"},
		{"  - statement: A-0001\n    note: x", "Kent Rule 37"},
	}
	for _, c := range cases {
		d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\ncontext_for:\n"+c.entries, "Statement two text.")
		mustRefuse(t, c.want, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	}
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\nclarifies: S-0001\ncontext_for:\n  - statement: S-0001\n    note: x", "Statement three text.")
	mustRefuse(t, "already clarifies S-0001", "attest", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--user", "alice")
	if statementPath(root, "S-0002") != "" || statementMeta(t, root, "S-0001").Get("extra_context") != nil {
		t.Fatal("a refused draft changed the record")
	}
}

func TestCheckContextMarks(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\ncontext_for:\n  - statement: S-0001\n    note: x", "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")

	edit := func(id string, f func(*py.Dict)) {
		p := statementPath(root, id)
		st := loadStatement(p)
		f(st.Meta)
		writeStatement(p, st)
	}
	edit("S-0001", func(m *py.Dict) { delKey(m, "extra_context") })
	out, code := kbRun(t, "check")
	if code == 0 || !strings.Contains(out, "S-0001: not marked extra_context, though S-0002 is context for it") {
		t.Fatalf("check missed a missing mark:\n%s", out)
	}
	edit("S-0001", func(m *py.Dict) { m.Set("extra_context", true) })
	edit("S-0002", func(m *py.Dict) { m.Set("extra_context", true) })
	out, code = kbRun(t, "check")
	if code == 0 || !strings.Contains(out, "S-0002: marked extra_context, but no statement is context for it") {
		t.Fatalf("check missed a stray mark:\n%s", out)
	}
	edit("S-0002", func(m *py.Dict) { delKey(m, "extra_context"); m.Set("supersedes", "S-0001") })
	edit("S-0001", func(m *py.Dict) { m.Set("superseded_by", "S-0002") })
	out, code = kbRun(t, "check")
	if code == 0 || !strings.Contains(out, "S-0002: supersedes S-0001, whose author is 'alice', not 'bob' (Kent Rule 46") {
		t.Fatalf("check missed a supersession by someone other than the author:\n%s", out)
	}
}

// delKey removes a key from a frontmatter dict, as a hand edit would.
func delKey(m *py.Dict, k string) {
	n := py.NewDict()
	for _, x := range m.Keys() {
		if x != k {
			n.Set(x, m.Get(x))
		}
	}
	*m = *n
}

func TestLinksMap(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\nrests_on: [S-0001]\ncontext_for:\n  - statement: S-0001\n    note: qualifies it", "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	d = writeDraft(t, root, "three", fmt.Sprintf(front, "Three")+"\nsupersedes: S-0001", "Statement three text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--user", "alice")
	a := writeDraft(t, root, "choice", "title: Choice\nanswers: a test choice\nrests_on: [S-0002]", "Agent choice text.")
	mustRun(t, "record", a, "--turn", turnPtr(6))

	out := mustRun(t, "links", "S-0001")
	for _, want := range []string{
		"S-0001  superseded by S-0003  attested/S-0001-one.md  One\n",
		"  <- context_for S-0002: qualifies it\n",
		"  <- supersedes S-0003\n",
		"  <- rests_on S-0002\n",
		"also reached:\nS-0002  in force  attested/S-0002-two.md  Two\nS-0003  in force  attested/S-0003-three.md  Three\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kb links S-0001 lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "A-0001") {
		t.Errorf("depth 1 reached two links out:\n%s", out)
	}

	// from S-0002, links with S-0001 (no longer in force) fold into one line; depth 2 shows A-0001 in
	// full, and supersession is followed to the version in force
	out = mustRun(t, "links", "S-0002", "--depth", "2")
	for _, want := range []string{
		"S-0002  in force  attested/S-0002-two.md  Two\n  <- rests_on A-0001\n  not in force: -> context_for S-0001, -> rests_on S-0001\n",
		"\nA-0001  in force  agent/A-0001-choice.md  Choice\n  -> rests_on S-0002\n",
		"S-0003  in force  attested/S-0003-three.md  Three\n",
		"S-0001  superseded by S-0003",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kb links S-0002 --depth 2 lacks %q:\n%s", want, out)
		}
	}

	mustRefuse(t, "not found", "links", "S-0042")
	mustRefuse(t, "not a statement id", "links", "nope")
}

func TestIndexAndServeShowContext(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\ncontext_for:\n  - statement: S-0001\n    note: qualifies it",
		"Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	db := openDB(buildIndex(root))
	rows := query(db, "SELECT statement, target, note FROM context_links")
	db.Close()
	if len(rows) != 1 || rows[0]["statement"] != "S-0002" || rows[0]["target"] != "S-0001" || rows[0]["note"] != "qualifies it" {
		t.Fatalf("context_links = %v", rows)
	}
	get := func(path string) string {
		rec := httptest.NewRecorder()
		(&kbServer{root}).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Body.String()
	}
	if page := get("/s/S-0001"); !strings.Contains(page, `id="context"`) || !strings.Contains(page, "qualifies it") || !strings.Contains(page, "extra context</a> (1)") {
		t.Errorf("the page of S-0001 does not show its extra context:\n%s", page)
	}
	if page := get("/s/S-0002"); !strings.Contains(page, "<th>context for</th>") {
		t.Errorf("the page of S-0002 does not show what it is context for")
	}
}
