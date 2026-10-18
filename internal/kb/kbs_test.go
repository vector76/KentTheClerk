package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kenttheclerk/internal/dyn"
	"kenttheclerk/internal/kbset"
)

// homeMap points the home folder at a new one whose ~/.kenttheclerk.json maps ids to folders.
func homeMap(t *testing.T, m map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	b, _ := json.Marshal(map[string]any{kbset.MapKey: m})
	if err := os.WriteFile(filepath.Join(home, ".kenttheclerk.json"), b, 0o666); err != nil {
		t.Fatal(err)
	}
}

// emptyKB makes a knowledge base with no evidence, with alice in its user table.
func emptyKB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"attested", "agent", "evidence", "off-record/drafts"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "user", "add", "Alice Adams", "--email", "alice@example.com", "--key", "alice", "--user", "alice", "--kb", root)
	return root
}

func kbList(roots ...string) string { return strings.Join(roots, string(os.PathListSeparator)) }

// TestKBID: kb id --create gives a knowledge base an id once; kb setup --create gives a new one its id.
func TestKBID(t *testing.T) {
	root := newTestKB(t)
	mustRefuse(t, "id: none", "id")
	out := mustRun(t, "id", "--create")
	id, ok, err := kbset.ReadID(root)
	if !ok || err != nil || !strings.Contains(out, id) {
		t.Fatalf("kb id --create: %q, id %q %v %v", out, id, ok, err)
	}
	mustRefuse(t, "already has the id", "id", "--create")
	if out := mustRun(t, "id"); !strings.Contains(out, id) {
		t.Fatalf("kb id: %q", out)
	}
	dir := filepath.Join(t.TempDir(), "new")
	mustRun(t, "setup", "--create", dir, "--project", t.TempDir())
	if other, ok, _ := kbset.ReadID(dir); !ok || other == id {
		t.Fatalf("kb setup --create gave the id %q (first %q)", other, id)
	}
	// a merge of two ids is an error kb check reports
	if err := os.WriteFile(kbset.IDPath(root), []byte(id+"\nK-0000000000AB\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, "id cannot be read", "check")
}

// TestSeveralKBs: with several knowledge bases, a command acting for a session finds the one the session
// is recorded in, any other needs --kb, and a session is never recorded in two (S-DHSE97, S-ZBVAHS).
func TestSeveralKBs(t *testing.T) {
	a := newTestKB(t)
	attestOne(t, a)
	b := emptyKB(t)
	t.Setenv(kbEnv, kbList(a, b))

	mustRefuse(t, "Kent Rule 59", "links", "S-0001")
	if out := mustRun(t, "links", "--kb", a, "S-0001"); !strings.Contains(out, "S-0001") {
		t.Fatalf("links --kb:\n%s", out)
	}
	// attest goes where its session is
	d := writeDraft(t, a, "two", fmt.Sprintf(front, "Two"), "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	if statementPath(a, "S-0002") == "" || statementPath(b, "S-0001") != "" {
		t.Fatal("attest did not write into the knowledge base holding its session")
	}
	out := mustRun(t, "check")
	if !strings.Contains(out, "# Across them") || !strings.Contains(out, "no session is on the record in more than one") {
		t.Fatalf("check over two:\n%s", out)
	}

	// a new session needs --kb on its first snapshot, and stays where it went
	const sid = "cccccccc-0000-4000-8000-000000000001"
	src := filepath.Join(t.TempDir(), sid+".jsonl")
	var lines []string
	for n, typ := range []string{"user", "assistant"} {
		rec := map[string]any{"type": typ, "uuid": fmt.Sprintf("33333333-0000-4000-8000-%012d", n+1), "sessionId": sid,
			"timestamp": fmt.Sprintf("2026-10-17T12:00:%02d.000Z", n)}
		if typ == "user" {
			rec["origin"] = map[string]any{"kind": "human"}
			rec["message"] = map[string]any{"role": "user", "content": "On the record in the second one."}
		} else {
			rec["parentUuid"] = "33333333-0000-4000-8000-000000000001"
			rec["message"] = map[string]any{"model": "m", "content": []any{map[string]any{"type": "text", "text": "Noted."}}}
		}
		j, _ := json.Marshal(rec)
		lines = append(lines, string(j))
	}
	if err := os.WriteFile(src, []byte(strings.Join(lines, "\n")+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, "Ask the human which one", "snapshot", src, "--user", "alice")
	mustRun(t, "snapshot", src, "--user", "alice", "--kb", b)
	mustRun(t, "snapshot", src, "--user", "alice")
	if h := sessionHolders([]string{a, b}, sid); len(h) != 1 || !samePath(h[0], b) {
		t.Fatalf("the session is held by %v", h)
	}
	mustRefuse(t, "already recorded in", "snapshot", src, "--user", "alice", "--kb", a)

	// a session found on the record in both is an error, and acts for it are refused
	ev, _ := filepath.Glob(filepath.Join(b, "evidence", "*-cccccccc.jsonl"))
	data, _ := os.ReadFile(ev[0])
	if err := os.WriteFile(filepath.Join(a, "evidence", filepath.Base(ev[0])), data, 0o666); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, "is on the record in", "check")
	mustRefuse(t, "more than one knowledge base", "snapshot", src, "--user", "alice")
}

// TestReferenceIntoAnotherKB: a statement may rest on and be context for a statement in another knowledge
// base, written <id>/<statement id>; the target is never marked, kb check notes a reference it cannot
// follow, and supersession does not cross (S-Y17H90, S-BJK3H8).
func TestReferenceIntoAnotherKB(t *testing.T) {
	a := newTestKB(t)
	attestOne(t, a)
	mustRun(t, "id", "--create")
	idA, _, _ := kbset.ReadID(a)
	ref := idA + "/S-0001"

	b := newTestKB(t) // KENT_KB now names b only
	homeMap(t, map[string]string{idA: a})
	d := writeDraft(t, b, "x", fmt.Sprintf(front, "Builds on the other")+"\nrests_on:\n  - "+ref+
		"\ncontext_for:\n  - statement: "+ref+"\n    note: reads it for the second audience", "Statement one text.")
	out := mustRun(t, "attest", d, "--proposed", turnPtr(0), "--turn", turnPtr(1), "--user", "alice")
	if !strings.Contains(out, "not marked") {
		t.Errorf("attest did not say the target stays unmarked:\n%s", out)
	}
	if statementMeta(t, a, "S-0001").Get("extra_context") != nil {
		t.Fatal("a link from another knowledge base marked its target")
	}
	if out := mustRun(t, "check"); !strings.Contains(out, ": 0 error(s), 0 warning(s)") {
		t.Fatalf("check:\n%s", out)
	}
	if out := mustRun(t, "links", "S-0001"); !strings.Contains(out, ref) || !strings.Contains(out, "One") {
		t.Fatalf("links does not follow the reference:\n%s", out)
	}
	if out := mustRun(t, "links", ref); !strings.Contains(out, "S-0001  in force") {
		t.Fatalf("links on a reference does not map the other knowledge base:\n%s", out)
	}

	// supersession does not cross
	d = writeDraft(t, b, "y", fmt.Sprintf(front, "Replaces the other")+"\nsupersedes: "+ref, "Statement two text.")
	mustRefuse(t, "Kent Rule 60", "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")

	// without the knowledge base on this machine, a new reference is refused and an old one only noted
	homeMap(t, map[string]string{})
	d = writeDraft(t, b, "z", fmt.Sprintf(front, "Rests on the other")+"\nrests_on:\n  - "+ref, "Statement two text.")
	mustRefuse(t, "not on this machine", "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	out = mustRun(t, "check")
	if !strings.Contains(out, "note: 2 reference(s) into "+idA) || !strings.Contains(out, ": 0 error(s), 0 warning(s)") {
		t.Fatalf("check without the other knowledge base:\n%s", out)
	}
}

// TestTakeKBFlag: --kb is taken from anywhere in the arguments, once.
func TestTakeKBFlag(t *testing.T) {
	args, kb, err := takeKBFlag([]string{"links", "--kb", "x", "S-0001"})
	if err != nil || kb != "x" || strings.Join(args, " ") != "links S-0001" {
		t.Fatalf("%v %q %v", args, kb, err)
	}
	if _, kb, _ := takeKBFlag([]string{"check", "--kb=K-0000000000AB"}); kb != "K-0000000000AB" {
		t.Fatalf("--kb= gave %q", kb)
	}
	if _, _, err := takeKBFlag([]string{"check", "--kb", "x", "--kb", "y"}); err == nil {
		t.Fatal("two --kb were taken")
	}
}

// TestStmtRefsSkipsOtherKB: in text, the statement of a reference into another knowledge base is not
// linked to a statement here.
func TestStmtRefsSkipsOtherKB(t *testing.T) {
	got := stmtRefs("see K-4QX7M2PT9WNB/S-Y17H90 and S-Y17H90")
	if strings.Count(got, "<a ") != 1 || !strings.Contains(got, "K-4QX7M2PT9WNB/S-Y17H90 and") {
		t.Fatalf("%s", got)
	}
}

// TestSetupSeveral: setup's settings name every knowledge base, and ask for an id on each.
func TestSetupSeveral(t *testing.T) {
	// kb names a folder as dyn.Resolve gives it, the long form of a short 8.3 temporary path on Windows
	a := dyn.Resolve(newTestKB(t))
	b := dyn.Resolve(emptyKB(t))
	t.Setenv(kbEnv, a)
	out := mustRun(t, "setup", "--report", "--kb", b, "--project", t.TempDir())
	if !strings.Contains(out, kbListValue([]string{a, b})) || !strings.Contains(out, "kb id --create --kb") {
		t.Fatalf("setup with a second knowledge base:\n%s", out)
	}
	t.Setenv(kbEnv, kbList(a, b))
	out = mustRun(t, "session-start")
	if !strings.Contains(out, "refers to 2 knowledge bases") || !strings.Contains(out, "### "+a) || !strings.Contains(out, "### "+b) {
		t.Fatalf("session-start with two:\n%s", out)
	}
}
