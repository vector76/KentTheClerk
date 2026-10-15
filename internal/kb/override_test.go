package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kenttheclerk/internal/py"
)

// checkedBefore places the double-check before every test turn, so any of them may confirm an override.
func checkedBefore(t *testing.T) {
	t.Helper()
	old := overrideNow
	overrideNow = func() time.Time { return time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { overrideNow = old })
}

// override runs an override's check and then confirms it with the given turn.
func override(t *testing.T, user string, turn int, args ...string) string {
	t.Helper()
	mustRun(t, append(append([]string{"override"}, args...), "--user", user)...)
	return mustRun(t, append(append([]string{"override"}, args...), "--confirm", "--turn", turnPtr(turn), "--reason", "a test", "--user", user)...)
}

func turnAuthor(t *testing.T, root, uuid string) any {
	t.Helper()
	s := findSession(root, testSession)
	tt := s.Turn(uuid)
	if tt == nil {
		t.Fatalf("no turn %s", uuid)
	}
	return tt.Get("author")
}

func checkClean(t *testing.T) string {
	t.Helper()
	out := mustRun(t, "check")
	if !strings.Contains(out, " 0 error(s)") {
		t.Fatalf("kb check found errors:\n%s", out)
	}
	return out
}

func checkFails(t *testing.T, want string) {
	t.Helper()
	mustRefuse(t, want, "check")
}

func TestOverrideNeedsTheCheckFirst(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	mustRefuse(t, "Kent Rule 52", "override", "rename", "alice", "carol", "--confirm", "--turn", turnPtr(5), "--reason", "x", "--user", "alice")

	// a confirmation made before kb showed the check does not count
	old := overrideNow
	overrideNow = func() time.Time { return time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC) }
	defer func() { overrideNow = old }()
	out := mustRun(t, "override", "rename", "alice", "carol", "--user", "alice")
	if !strings.Contains(out, "this is the check") || !strings.Contains(out, "last resort") {
		t.Fatalf("the check did not show the warning:\n%s", out)
	}
	mustRefuse(t, "before kb showed the check", "override", "rename", "alice", "carol", "--confirm", "--turn", turnPtr(5), "--reason", "x", "--user", "alice")
}

func TestOverrideRename(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	mustRun(t, "override", "rename", "alice", "carol", "--user", "alice")
	// the authorizing turn is the human's own, and a reason is required
	mustRefuse(t, "Kent Rule 21", "override", "rename", "alice", "carol", "--confirm", "--turn", turnPtr(3), "--reason", "x", "--user", "alice")
	mustRefuse(t, "--reason is required", "override", "rename", "alice", "carol", "--confirm", "--turn", turnPtr(5), "--user", "alice")
	mustRun(t, "override", "rename", "alice", "carol", "--confirm", "--turn", turnPtr(5), "--reason", "wrong id", "--user", "alice")

	if by := py.AsDict(listOf(statementMeta(t, root, "S-0001").Get("attestations"))[0]).Get("by"); !py.Equal(by, "carol") {
		t.Fatalf("S-0001 attested by %v after the rename", by)
	}
	if a := turnAuthor(t, root, testTurns[1].uuid); !py.Equal(a, "carol") {
		t.Fatalf("turn author %v after the rename", a)
	}
	if a := turnAuthor(t, root, testTurns[3].uuid); !py.Equal(a, "bob") {
		t.Fatalf("bob's turn was relabelled: %v", a)
	}
	checkClean(t)

	// the old id acts no more, and a rename onto someone with acts is a merge
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two"), "Statement two text.")
	mustRefuse(t, "moved to", "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "alice")
	mustRefuse(t, "that is a merge", "override", "rename", "carol", "bob", "--user", "carol")
	mustRefuse(t, "retired", "override", "rename", "bob", "alice", "--user", "bob")
}

func TestOverrideMerge(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	mustRefuse(t, "this is a rename", "override", "merge", "alice", "carol", "--user", "alice")
	override(t, "alice", 5, "merge", "alice", "bob")
	if by := py.AsDict(listOf(statementMeta(t, root, "S-0001").Get("attestations"))[0]).Get("by"); !py.Equal(by, "bob") {
		t.Fatalf("S-0001 attested by %v after the merge", by)
	}
	checkClean(t)
}

func TestOverrideMergeRefusesDoubleAttestation(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	mustRun(t, "attest", "--form", "endorsement", "--statement", "S-0001", "--turn", turnPtr(3), "--user", "bob")
	mustRefuse(t, "Kent Rule 26", "override", "merge", "alice", "bob", "--user", "alice")
}

func TestOverrideReassign(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	mustRefuse(t, "Kent Rule 56", "override", "reassign", turnPtr(1), "dave", "--user", "alice")
	mustRun(t, "user", "add", "Dave Diaz", "--email", "dave@example.com", "--key", "dave", "--user", "alice")
	override(t, "alice", 5, "reassign", turnPtr(1), "dave")
	if a := turnAuthor(t, root, testTurns[1].uuid); !py.Equal(a, "dave") {
		t.Fatalf("turn author %v after the reassignment", a)
	}
	if a := turnAuthor(t, root, testTurns[5].uuid); !py.Equal(a, "alice") {
		t.Fatalf("another of alice's turns was reassigned: %v", a)
	}
	if by := py.AsDict(listOf(statementMeta(t, root, "S-0001").Get("attestations"))[0]).Get("by"); !py.Equal(by, "dave") {
		t.Fatalf("S-0001 attested by %v after the reassignment", by)
	}
	checkClean(t)
}

func TestOverrideOnlyFromOnRecord(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	off := filepath.Join(root, "off-record", "sessions")
	if err := os.MkdirAll(off, 0o777); err != nil {
		t.Fatal(err)
	}
	hdr, _ := json.Marshal(map[string]any{"type": "kb-snapshot", "session_id": "cccccccc-0000-4000-8000-000000000001", "mode": "off-record"})
	turn, _ := json.Marshal(map[string]any{"uuid": "33333333-0000-4000-8000-000000000001", "role": "user", "author": "alice",
		"timestamp": "2026-10-14T10:00:00.000Z", "content": []any{map[string]any{"type": "text", "text": "Yes."}}})
	if err := os.WriteFile(filepath.Join(off, "2026-10-14-cccccccc.jsonl"), append(append(append(hdr, '\n'), turn...), '\n'), 0o666); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "override", "rename", "alice", "carol", "--user", "alice")
	mustRefuse(t, "Kent Rule 51", "override", "rename", "alice", "carol", "--confirm",
		"--turn", "cccccccc-0000-4000-8000-000000000001#33333333-0000-4000-8000-000000000001", "--reason", "x", "--user", "alice")
}

func TestOverrideScrubTurn(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	override(t, "alice", 5, "scrub", turnPtr(0))
	tt := findSession(root, testSession).Turn(testTurns[0].uuid)
	if strings.Contains(turnText(tt), "Statement one text.") || !strings.HasPrefix(turnText(tt), "[scrubbed on ") {
		t.Fatalf("the turn was not scrubbed: %q", turnText(tt))
	}
	if !py.Equal(tt.Get("author"), "agent:test") {
		t.Fatalf("the scrubbed turn lost its author: %v", tt.Get("author"))
	}
	out := checkClean(t)
	if !strings.Contains(out, "S-0001: the turn it was proposed in") {
		t.Fatalf("kb check did not flag the statement resting on scrubbed evidence:\n%s", out)
	}
	mustRefuse(t, "already scrubbed", "override", "scrub", turnPtr(0), "--user", "alice")

	// restoring the content by hand shows
	p := findSession(root, testSession).Path
	s := loadSession(p)
	s.Turn(testTurns[0].uuid).Set("content", []any{py.D("type", "text", "text", "Statement one text.")})
	writeJSONL(p, s.Header, s.Turns)
	checkFails(t, "not the tombstone")
}

func TestOverrideScrubStatementAndSession(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	out := override(t, "alice", 5, "scrub", "S-0001")
	if !strings.Contains(out, "the turn it was proposed in") {
		t.Fatalf("the scrub did not say where the text remains:\n%s", out)
	}
	m := statementMeta(t, root, "S-0001")
	if !strings.HasSuffix(statementPath(root, "S-0001"), "S-0001-scrubbed.md") || !strings.HasPrefix(py.Str(m.Get("title")), "Scrubbed") {
		t.Fatalf("S-0001 was not scrubbed: %s %v", statementPath(root, "S-0001"), m.Get("title"))
	}
	checkClean(t)

	override(t, "bob", 3, "scrub", testSession[:8])
	s := findSession(root, testSession)
	for _, tt := range s.Turns {
		if !scrubbedTurn(tt) {
			t.Fatalf("turn %v was not scrubbed with its session", tt.Get("uuid"))
		}
	}
	checkClean(t)
}

func TestOverrideRevise(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	d := writeDraft(t, root, "rev", fmt.Sprintf(front, "One revised"), "Statement three text.")
	mustRefuse(t, "only the person who attested it", "override", "revise", "S-0001", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--reason", "x", "--user", "bob")
	mustRefuse(t, "--reason is required", "override", "revise", "S-0001", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--user", "alice")
	// no double-check: it is attested anew instead
	out := mustRun(t, "override", "revise", "S-0001", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--reason", "got it backwards", "--user", "alice")
	if !strings.Contains(out, "revised in place") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	p := statementPath(root, "S-0001")
	st := loadStatement(p)
	if st.Body != "Statement three text." || py.Str(st.Meta.Get("revised")) != "1" || !strings.Contains(p, "one-revised") {
		t.Fatalf("S-0001 not revised: %s %v %q", p, st.Meta.Get("revised"), st.Body)
	}
	entries, _, _ := loadOverrideLog(root)
	if len(entries) != 1 || !strings.Contains(entryStr(entries[0], "original"), "Statement one text.") {
		t.Fatalf("the log does not keep the original text: %v", entries)
	}
	checkClean(t)

	// a change by hand shows
	st.Body = "Something else."
	writeStatement(p, st)
	checkFails(t, "changed outside kb")
}

func TestOverrideReviseRefusedOnceReliedOn(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\nrests_on:\n  - S-0001", "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	r := writeDraft(t, root, "rev", fmt.Sprintf(front, "One revised"), "Statement three text.")
	mustRefuse(t, "S-0002 rests on it", "override", "revise", "S-0001", r, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--reason", "x", "--user", "alice")
}

func TestOverrideReviseRefusedOnceMentioned(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two"), "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	// S-0002's text does not mention S-0001, so it may still be revised; an endorsement relies on it
	mustRun(t, "attest", "--form", "endorsement", "--statement", "S-0001", "--turn", turnPtr(3), "--user", "bob")
	r := writeDraft(t, root, "rev", fmt.Sprintf(front, "One revised"), "Statement three text.")
	mustRefuse(t, "endorsed by bob", "override", "revise", "S-0001", r, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--reason", "x", "--user", "alice")
}

func TestOverrideLogIsChained(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	override(t, "alice", 5, "scrub", turnPtr(2))
	override(t, "alice", 5, "scrub", turnPtr(4))
	checkClean(t)
	p := overrideLogPath(root)
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "a test", "a tset", 1)), 0o666); err != nil {
		t.Fatal(err)
	}
	checkFails(t, "hash of the entry before it")
}

func TestOverrideUnknownKind(t *testing.T) {
	newTestKB(t)
	mustRefuse(t, "the list is closed", "override", "rewrite", "S-0001", "--user", "alice")
}

// TestSnapshotKeepsOverrides checks that a re-snapshot neither restores a scrubbed turn nor relabels a
// renamed one.
func TestSnapshotKeepsOverrides(t *testing.T) {
	root := newTestKB(t)
	checkedBefore(t)
	const sid = "bbbbbbbb-0000-4000-8000-000000000001"
	uuid := func(n int) string { return fmt.Sprintf("22222222-0000-4000-8000-%012d", n) }
	src := filepath.Join(t.TempDir(), sid+".jsonl")
	var lines []string
	add := func(n int, typ, text string) {
		rec := map[string]any{"type": typ, "uuid": uuid(n), "sessionId": sid, "timestamp": fmt.Sprintf("2026-10-14T12:00:%02d.000Z", n)}
		if n > 1 {
			rec["parentUuid"] = uuid(n - 1)
		}
		if typ == "user" {
			rec["origin"] = map[string]any{"kind": "human"}
			rec["message"] = map[string]any{"role": "user", "content": text}
		} else {
			rec["message"] = map[string]any{"model": "m", "content": []any{map[string]any{"type": "text", "text": text}}}
		}
		b, _ := json.Marshal(rec)
		lines = append(lines, string(b))
		if err := os.WriteFile(src, []byte(strings.Join(lines, "\n")+"\n"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "user", "A secret that must go.")
	add(2, "assistant", "Noted.")
	add(3, "user", "Scrub my first turn.")
	mustRun(t, "snapshot", src, "--user", "alice")
	ptr := func(n int) string { return sid + "#" + uuid(n) }
	mustRun(t, "override", "scrub", ptr(1), "--user", "alice")
	mustRun(t, "override", "scrub", ptr(1), "--confirm", "--turn", ptr(3), "--reason", "a secret", "--user", "alice")

	add(4, "assistant", "Done.")
	mustRun(t, "snapshot", src, "--user", "alice")
	s := findSession(root, sid)
	if len(s.Turns) != 4 || strings.Contains(turnText(s.Turn(uuid(1))), "secret that") {
		t.Fatalf("the re-snapshot restored the scrubbed turn: %q", turnText(s.Turn(uuid(1))))
	}

	mustRun(t, "override", "rename", "alice", "carol", "--user", "alice")
	mustRun(t, "override", "rename", "alice", "carol", "--confirm", "--turn", ptr(3), "--reason", "new id", "--user", "alice")
	mustRefuse(t, "moved to", "snapshot", src, "--user", "alice")
	add(5, "user", "Thanks.")
	mustRun(t, "snapshot", src, "--user", "carol")
	s = findSession(root, sid)
	for _, n := range []int{1, 3, 5} {
		if a := s.Turn(uuid(n)).Get("author"); !py.Equal(a, "carol") {
			t.Fatalf("turn %d is by %v after the rename and a re-snapshot", n, a)
		}
	}
	if strings.Contains(turnText(s.Turn(uuid(1))), "secret that") {
		t.Fatal("the scrubbed turn came back")
	}
	checkClean(t)
}
