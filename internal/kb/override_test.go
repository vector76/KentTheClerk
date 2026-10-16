package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kenttheclerk/internal/dyn"
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
	mustRefuse(t, "Kent Rule 52", "override", "merge", "alice", "bob", "--confirm", "--turn", turnPtr(5), "--reason", "x", "--user", "alice")

	// a confirmation made before kb showed the check does not count
	old := overrideNow
	overrideNow = func() time.Time { return time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC) }
	defer func() { overrideNow = old }()
	out := mustRun(t, "override", "merge", "alice", "bob", "--user", "alice")
	if !strings.Contains(out, "this is the check") || !strings.Contains(out, "last resort") {
		t.Fatalf("the check did not show the warning:\n%s", out)
	}
	mustRefuse(t, "before kb showed the check", "override", "merge", "alice", "bob", "--confirm", "--turn", turnPtr(5), "--reason", "x", "--user", "alice")
}

// TestOverrideMergePutsRightAWrongKey checks that a key wrong from the start is put right by adding the
// person under the right key and merging into it, renaming a key being no override of its own (S-0148).
func TestOverrideMergePutsRightAWrongKey(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	mustRefuse(t, "kb override merge <wrong> <right>", "override", "rename", "alice", "carol", "--user", "alice")
	mustRefuse(t, "add the right person first", "override", "merge", "alice", "carol", "--user", "alice")
	mustRun(t, "user", "add", "Alice Adams", "--email", "adams@example.com", "--key", "carol", "--user", "alice")
	out := mustRun(t, "override", "merge", "alice", "carol", "--user", "alice")
	if !strings.Contains(out, "wrong from the start") {
		t.Fatalf("the check did not say what the merge puts right:\n%s", out)
	}
	// the authorizing turn is the human's own, and a reason is required
	mustRefuse(t, "Kent Rule 21", "override", "merge", "alice", "carol", "--confirm", "--turn", turnPtr(3), "--reason", "x", "--user", "alice")
	mustRefuse(t, "--reason is required", "override", "merge", "alice", "carol", "--confirm", "--turn", turnPtr(5), "--user", "alice")
	mustRun(t, "override", "merge", "alice", "carol", "--confirm", "--turn", turnPtr(5), "--reason", "wrong key", "--user", "alice")

	if by := dyn.AsDict(listOf(statementMeta(t, root, "S-0001").Get("attestations"))[0]).Get("by"); !dyn.Equal(by, "carol") {
		t.Fatalf("S-0001 attested by %v after the merge", by)
	}
	if a := turnAuthor(t, root, testTurns[1].uuid); !dyn.Equal(a, "carol") {
		t.Fatalf("turn author %v after the merge", a)
	}
	if a := turnAuthor(t, root, testTurns[3].uuid); !dyn.Equal(a, "bob") {
		t.Fatalf("bob's turn was relabelled: %v", a)
	}
	if tbl := mustLoadUsers(root); tbl.has("alice") || !tbl.has("carol") {
		t.Fatalf("table after the merge: %+v", tbl.rows)
	}
	checkClean(t)

	// the wrong key acts no more, and nothing merges into it
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two"), "Statement two text.")
	mustRefuse(t, "moved to", "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "alice")
	mustRefuse(t, "retired", "override", "merge", "bob", "alice", "--user", "bob")
}

func TestOverrideMerge(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	override(t, "alice", 5, "merge", "alice", "bob")
	if by := dyn.AsDict(listOf(statementMeta(t, root, "S-0001").Get("attestations"))[0]).Get("by"); !dyn.Equal(by, "bob") {
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
	if a := turnAuthor(t, root, testTurns[1].uuid); !dyn.Equal(a, "dave") {
		t.Fatalf("turn author %v after the reassignment", a)
	}
	if a := turnAuthor(t, root, testTurns[5].uuid); !dyn.Equal(a, "alice") {
		t.Fatalf("another of alice's turns was reassigned: %v", a)
	}
	if by := dyn.AsDict(listOf(statementMeta(t, root, "S-0001").Get("attestations"))[0]).Get("by"); !dyn.Equal(by, "dave") {
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
	mustRun(t, "override", "merge", "alice", "bob", "--user", "alice")
	mustRefuse(t, "Kent Rule 51", "override", "merge", "alice", "bob", "--confirm",
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
	if !dyn.Equal(tt.Get("author"), "agent:test") {
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
	s.Turn(testTurns[0].uuid).Set("content", []any{dyn.D("type", "text", "text", "Statement one text.")})
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
	if !strings.HasSuffix(statementPath(root, "S-0001"), "S-0001-scrubbed.md") || !strings.HasPrefix(dyn.Str(m.Get("title")), "Scrubbed") {
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
	entries, _ := loadOverrideLog(root)
	if st.Body != "Statement three text." || len(entries) != 1 || dyn.Str(st.Meta.Get("revised")) != overrideRef(entries[0]) || !strings.Contains(p, "one-revised") {
		t.Fatalf("S-0001 not revised: %s %v %q", p, st.Meta.Get("revised"), st.Body)
	}
	if !strings.Contains(entryStr(entries[0], "original"), "Statement one text.") {
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

// overrideAt makes an override as override does, with the log's clock at the given minute past 9:00, before
// every test turn.
func overrideAt(t *testing.T, minute int, user string, turn int, args ...string) string {
	t.Helper()
	old := overrideNow
	overrideNow = func() time.Time { return time.Date(2026, 10, 14, 9, minute, 0, 0, time.UTC) }
	defer func() { overrideNow = old }()
	return override(t, user, turn, args...)
}

func readLines(t *testing.T, p string) []string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func writeLines(t *testing.T, p string, lines []string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
}

// TestOverrideIDs checks that an override gets a random O- id, cited by its tombstone and its output, and
// that its entry carries no number and no hash of the entry before it (S-QYEK, S-8MW2).
func TestOverrideIDs(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	out := override(t, "alice", 5, "scrub", turnPtr(2))
	entries := mustLoadOverrideLog(root)
	id := entryStr(entries[0], "id")
	if !overrideIDRE.MatchString(id) || entries[0].Has("seq") || entries[0].Has("prev") {
		t.Fatalf("the entry is not identified by a new id alone: %s", dyn.Dumps(entries[0]))
	}
	if !strings.Contains(out, "override "+id+": ") {
		t.Fatalf("kb override does not name the override by its id:\n%s", out)
	}
	tt := findSession(root, testSession).Turn(testTurns[2].uuid)
	if !strings.Contains(turnText(tt), "by override "+id+": ") || markRef(tt.Get("scrubbed")) != id {
		t.Fatalf("the tombstone does not cite %s: %q %v", id, turnText(tt), tt.Get("scrubbed"))
	}
	checkClean(t)
	// a statement's marks cite it too
	override(t, "alice", 5, "scrub", "S-0001")
	m := statementMeta(t, root, "S-0001")
	if sc := dyn.Str(m.Get("scrubbed")); !overrideIDRE.MatchString(sc) || dyn.Str(m.Get("title")) != "Scrubbed by override "+sc {
		t.Fatalf("S-0001 scrubbed = %v, title %v", m.Get("scrubbed"), m.Get("title"))
	}
	checkClean(t)
	// two entries under one id is an error
	p := overrideLogPath(root)
	lines := readLines(t, p)
	writeLines(t, p, append(lines, lines[0]))
	checkFails(t, "two entries are override "+id)
}

// TestOverrideLogMerged checks that a log as a merge leaves it, one copy's new entries after the other's,
// is read in the order the entries were made, and that the chain no longer guards it (S-8MW2).
func TestOverrideLogMerged(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	mustRun(t, "user", "add", "Carol Cole", "--email", "carol@example.com", "--key", "carol", "--user", "alice")
	overrideAt(t, 10, "alice", 5, "merge", "alice", "carol") // made first
	overrideAt(t, 20, "bob", 3, "scrub", turnPtr(0))         // made later
	p := overrideLogPath(root)
	lines := readLines(t, p)
	writeLines(t, p, []string{lines[1], lines[0]})
	entries := mustLoadOverrideLog(root)
	if entryStr(entries[0], "kind") != ovMerge || entryStr(entries[1], "kind") != ovScrubTurn {
		t.Fatalf("the log is not read in the order made: %s, %s", entryStr(entries[0], "kind"), entryStr(entries[1], "kind"))
	}
	if to, ref := retiredID(root, "alice"); to != "carol" || ref != overrideRef(entries[0]) {
		t.Fatalf("retiredID(alice) = %s, %s", to, ref)
	}
	checkClean(t)
	// a change to an entry is no longer caught by a hash; git history is what shows it
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "a test", "a tset", 1)), 0o666); err != nil {
		t.Fatal(err)
	}
	checkClean(t)
	// entries made in the same second keep the order of their lines
	same := `{"id": "O-AAAA", "at": "2026-10-14T09:00:00+00:00"}`
	if got := byAtOrder(t, same, strings.Replace(same, "O-AAAA", "O-BBBB", 1)); got != "O-AAAA O-BBBB" {
		t.Fatalf("a tie was not kept in line order: %s", got)
	}
}

func byAtOrder(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log.jsonl")
	writeLines(t, p, lines)
	entries, problem := loadLogFile(p)
	if problem != "" {
		t.Fatal(problem)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, overrideRef(e))
	}
	return strings.Join(ids, " ")
}

// TestOverrideLogBeforeIDs checks that overrides made before S-QYEK, numbered and chained, stay valid: their
// tombstones and marks cite their numbers, their hashes go unchecked, and a new override joins them.
func TestOverrideLogBeforeIDs(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	overrideAt(t, 10, "alice", 5, "scrub", turnPtr(2))
	overrideAt(t, 20, "alice", 5, "scrub", "S-0001")
	// rewrite both as kb wrote them before: numbered 1 and 2, the second carrying a hash, and marks citing
	// the numbers
	entries := mustLoadOverrideLog(root)
	ids := []string{overrideRef(entries[0]), overrideRef(entries[1])}
	old := func(s string) string {
		s = strings.Replace(s, `"id": "`+ids[0]+`"`, `"seq": 1`, 1)
		s = strings.Replace(s, `"id": "`+ids[1]+`"`, `"seq": 2`, 1)
		s = strings.ReplaceAll(s, `"override": "`+ids[0]+`"`, `"override": 1`)
		s = strings.ReplaceAll(s, "by override "+ids[0], "by override 1")
		s = strings.ReplaceAll(s, ids[1], "2")
		return s
	}
	lines := readLines(t, overrideLogPath(root))
	writeLines(t, overrideLogPath(root), []string{old(lines[0]) + "", strings.Replace(old(lines[1]), "}", `, "prev": "sha256:00"}`, 1)})
	writeLines(t, findSession(root, testSession).Path, func() []string {
		var out []string
		for _, l := range readLines(t, findSession(root, testSession).Path) {
			out = append(out, old(l))
		}
		return out
	}())
	sp := statementPath(root, "S-0001")
	b, _ := os.ReadFile(sp)
	if err := os.WriteFile(sp, []byte(old(string(b))), 0o666); err != nil {
		t.Fatal(err)
	}
	if m := statementMeta(t, root, "S-0001"); markRef(m.Get("scrubbed")) != "2" || dyn.Str(m.Get("title")) != "Scrubbed by override 2" {
		t.Fatalf("S-0001 was not rewritten as before: %v %v", m.Get("scrubbed"), m.Get("title"))
	}
	checkClean(t)
	overrideAt(t, 30, "alice", 5, "scrub", turnPtr(4))
	checkClean(t)
}

// TestUsersLogMerged checks that an add carries no number or hash, and that the log is read in the order
// made after a merge leaves its lines out of order (S-QYEK, S-8MW2).
func TestUsersLogMerged(t *testing.T) {
	root := newTestKB(t)
	lines := readLines(t, usersLogPath(root))
	for _, l := range lines {
		if strings.Contains(l, `"seq"`) || strings.Contains(l, `"prev"`) {
			t.Fatalf("an add carries a number or a hash: %s", l)
		}
	}
	// alice was added before bob; a merge could leave bob's line first
	writeLines(t, usersLogPath(root), []string{lines[1], lines[0]})
	adds, _ := loadLogFile(usersLogPath(root))
	if entryStr(adds[0], "key") != "alice" && entryStr(adds[0], "at") != entryStr(adds[1], "at") {
		t.Fatalf("the user log is not read in the order made: %s first", entryStr(adds[0], "key"))
	}
	checkClean(t)
}

func TestOverrideUnknownKind(t *testing.T) {
	newTestKB(t)
	mustRefuse(t, "the list is closed", "override", "rewrite", "S-0001", "--user", "alice")
}

// TestSnapshotKeepsOverrides checks that a re-snapshot neither restores a scrubbed turn nor relabels a
// merged one.
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

	mustRun(t, "user", "add", "Alice Adams", "--email", "adams@example.com", "--key", "carol", "--user", "alice")
	mustRun(t, "override", "merge", "alice", "carol", "--user", "alice")
	mustRun(t, "override", "merge", "alice", "carol", "--confirm", "--turn", ptr(3), "--reason", "wrong key", "--user", "alice")
	mustRefuse(t, "moved to", "snapshot", src, "--user", "alice")
	add(5, "user", "Thanks.")
	mustRun(t, "snapshot", src, "--user", "carol")
	s = findSession(root, sid)
	for _, n := range []int{1, 3, 5} {
		if a := s.Turn(uuid(n)).Get("author"); !dyn.Equal(a, "carol") {
			t.Fatalf("turn %d is by %v after the merge and a re-snapshot", n, a)
		}
	}
	if strings.Contains(turnText(s.Turn(uuid(1))), "secret that") {
		t.Fatal("the scrubbed turn came back")
	}
	checkClean(t)
}
