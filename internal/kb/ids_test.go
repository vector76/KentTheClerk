package kb

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sequentialIDs makes new statements numbered as they were before S-0149, S-0001, S-0002 and so on, so
// that tests about other things can name the statements they make.
func sequentialIDs(t *testing.T) {
	t.Helper()
	old := makeStatementID
	makeStatementID = func(root, kind string) string {
		n := len(loadIDs(root, kind)) + 1
		return fmt.Sprintf("%s-%04d", kind, n)
	}
	t.Cleanup(func() { makeStatementID = old })
}

func loadIDs(root, kind string) []string {
	var ids []string
	sts := loadAllStatements(root)
	if kind == "A" {
		sts = loadAllAgentStatements(root)
	}
	for _, st := range sts {
		ids = append(ids, st.Meta.Get("id").(string))
	}
	return ids
}

func isLetter(c byte) bool { return c > '9' }

func TestDrawID(t *testing.T) {
	// the bytes' low five bits pick from the alphabet: 0..9 are digits, 10 is A, 31 is Z
	never := func(string) bool { return false }
	if got := drawID("S", 4, never, bytes.NewReader([]byte{10, 1, 2, 31})); got != "S-A12Z" {
		t.Fatalf("got %s, want S-A12Z", got)
	}
	// the high bits are ignored: 32+10 is A again, 255 is Z
	if got := drawID("A", 4, never, bytes.NewReader([]byte{42, 255, 0, 0})); got != "A-AZ00" {
		t.Fatalf("got %s, want A-AZ00", got)
	}
	// digits only is drawn again, as is an id already taken
	src := bytes.NewReader([]byte{1, 2, 3, 4, 10, 0, 0, 0, 11, 0, 0, 0})
	taken := func(id string) bool { return id == "S-A000" }
	if got := drawID("S", 4, taken, src); got != "S-B000" {
		t.Fatalf("got %s, want S-B000 after skipping S-1234 and the taken S-A000", got)
	}
	// every character of the alphabet comes out, and I, L, O and U never do
	all := make([]byte, 32*4)
	for i := range 32 {
		all[i*4], all[i*4+1], all[i*4+2], all[i*4+3] = byte(i), byte(i), byte(i), 10
	}
	seen := map[byte]bool{}
	src = bytes.NewReader(all)
	for range 32 {
		id := drawID("S", 4, never, src)
		seen[id[2]] = true
	}
	if len(seen) != 32 {
		t.Fatalf("drew %d distinct characters, want 32", len(seen))
	}
	for _, c := range []byte("ILOU") {
		if seen[c] {
			t.Fatalf("drew %c, which is not in the alphabet", c)
		}
	}
}

func TestNewStatementIDFormat(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "attested"), 0o777); err != nil {
		t.Fatal(err)
	}
	for range 2000 {
		id := newStatementID(root, "S")
		if !statementIDRE.MatchString(id) || !humanIDRE.MatchString(id) || len(id) != 2+statementIDLen {
			t.Fatalf("%s is not a valid new id", id)
		}
		if digitsRE.MatchString(id) {
			t.Fatalf("%s has no letter", id)
		}
		if !strings.ContainsFunc(id[2:], func(r rune) bool { return isLetter(byte(r)) }) {
			t.Fatalf("%s has no letter", id)
		}
	}
}

// TestNewStatementIDAvoidsTaken fills attested/ with every id but one and checks that the one left is
// the one drawn, whether an id is taken by a file's name or by the id inside it.
func TestNewStatementIDAvoidsTaken(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "attested")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	// ids S-A00000 .. S-A0000Z less S-A0000Q are taken; the draws are limited to that range by the source
	write := func(name, id string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("---\nid: "+id+"\ntitle: x\n---\n\nx\n"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	for i, c := range []byte(idAlphabet) {
		id := "S-A0000" + string(c)
		switch {
		case c == 'Q':
		case i%2 == 0:
			write(id+"-by-name.md", id)
		default:
			// a file whose name is not its id, as only a hand edit leaves it; its id is still taken
			write(fmt.Sprintf("S-Z%03d-by-id.md", i), id)
		}
	}
	old := idSource
	defer func() { idSource = old }()
	var buf []byte
	for i := range 32 {
		buf = append(buf, 10, 0, 0, 0, 0, byte(i))
	}
	idSource = bytes.NewReader(buf)
	if got := newStatementID(root, "S"); got != "S-A0000Q" {
		t.Fatalf("got %s, want S-A0000Q, the one id left", got)
	}
}

func TestIDValidation(t *testing.T) {
	// six characters for a new id (S-N5TZ); four and five stay valid, as do the digits-only ids
	for _, id := range []string{"S-0001", "S-0149", "A-0001", "S-00001", "S-7K2M", "A-ZZZZ", "S-0A00", "S-1234\n",
		"S-7K2MX", "S-7K2MXQ", "A-ZZ9Z00"} {
		if !statementIDRE.MatchString(id) {
			t.Errorf("%q should be a valid id", id)
		}
	}
	for _, id := range []string{"S-001", "S-7K2", "S-7K2MXQZ", "S-7k2m", "S-IIII", "S-LOOP", "S-OUCH", "S-7U2M", "B-7K2M", "S7K2M", "S-7K2M-"} {
		if statementIDRE.MatchString(id) {
			t.Errorf("%q should not be a valid id", id)
		}
	}
	if !humanIDRE.MatchString("S-7K2M") || humanIDRE.MatchString("A-7K2M") || !agentIDRE.MatchString("A-7K2M") {
		t.Error("the human and agent patterns do not tell the prefixes apart")
	}
}

func TestStmtRefsLinksNewIDs(t *testing.T) {
	cases := map[string]string{
		"see S-0012.":                           `see <a href="/s/S-0012">S-0012</a>.`,
		"see S-7K2M and A-0003":                 `see <a href="/s/S-7K2M">S-7K2M</a> and <a href="/s/A-0003">A-0003</a>`,
		"(A-ZZ9Z)":                              `(<a href="/s/A-ZZ9Z">A-ZZ9Z</a>)`,
		"S-00012":                               `<a href="/s/S-00012">S-00012</a>`,
		"S-7K2MXQ and A-7K2MX":                  `<a href="/s/S-7K2MXQ">S-7K2MXQ</a> and <a href="/s/A-7K2MX">A-7K2MX</a>`,
		"S-7K2MXQZ S-7K2 xS-7K2M S-7k2m S-LOOP": "S-7K2MXQZ S-7K2 xS-7K2M S-7k2m S-LOOP",
	}
	for in, want := range cases {
		if got := stmtRefs(in); got != want {
			t.Errorf("stmtRefs(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRandomIDsEndToEnd attests and records with the real generator: new ids carry a letter, sit beside
// the sequential ones, are listed in the order they were made, and leave the gap check alone.
func TestRandomIDsEndToEnd(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root) // S-0001, made sequentially
	makeStatementID = newStatementID
	later := now
	defer func() { now = later }()
	tick := 0
	now = func() time.Time { tick++; return later().Add(time.Duration(tick) * time.Hour) }

	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two")+"\nrests_on: [S-0001]", "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob")
	a := writeDraft(t, root, "choice", "title: Choice\nanswers: a test choice", "Agent choice text.")
	mustRun(t, "record", a, "--turn", turnPtr(6))
	d = writeDraft(t, root, "four", fmt.Sprintf(front, "Four")+"\nrests_on: [S-0001]", "Statement four text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(7), "--turn", turnPtr(8), "--user", "bob")

	ids := loadIDs(root, "S")
	if len(ids) != 3 || ids[0] != "S-0001" {
		t.Fatalf("statements in the order made = %v", ids)
	}
	for _, id := range append(ids[1:], loadIDs(root, "A")...) {
		if digitsRE.MatchString(id) || !statementIDRE.MatchString(id) {
			t.Fatalf("%s is not a new id", id)
		}
	}
	out := mustRun(t, "check")
	if !strings.Contains(out, ": 0 error(s)") || strings.Contains(out, "id gaps") {
		t.Fatalf("check:\n%s", out)
	}
	// kb links lists what it reached in the order made, whatever the ids
	out = mustRun(t, "links", "S-0001")
	if i, j := strings.Index(out, ids[1]+"  "), strings.Index(out, ids[2]+"  "); i < 0 || j < 0 || i > j {
		t.Fatalf("kb links S-0001 does not list %s before %s:\n%s", ids[1], ids[2], out)
	}
	// ids typed in lower case are read as upper case
	mustRun(t, "links", strings.ToLower(ids[1]))
}

// TestIDGapsCountOnlyNumbers: a missing number among the sequential ids is still reported, and the
// random ids beside them are not read as numbers.
func TestIDGapsCountOnlyNumbers(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root) // S-0001
	d := writeDraft(t, root, "two", fmt.Sprintf(front, "Two"), "Statement two text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(2), "--turn", turnPtr(3), "--user", "bob") // S-0002
	d = writeDraft(t, root, "three", fmt.Sprintf(front, "Three"), "Statement three text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(4), "--turn", turnPtr(5), "--user", "alice") // S-0003
	makeStatementID = newStatementID
	d = writeDraft(t, root, "four", fmt.Sprintf(front, "Four"), "Statement four text.")
	mustRun(t, "attest", d, "--proposed", turnPtr(7), "--turn", turnPtr(8), "--user", "bob")
	if err := os.Remove(statementPath(root, "S-0002")); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "check")
	if !strings.Contains(out, "warning: id gaps: S-0002\n") {
		t.Fatalf("check does not report the gap at S-0002 alone:\n%s", out)
	}
}
