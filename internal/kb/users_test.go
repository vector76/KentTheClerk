package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateHome points the home folder at an empty one, so ~/.kenttheclerk.json does not name a user.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KB_USER", "")
}

// TestUserAddGeneratesKey checks that kb user add without --key only proposes a key, made from the name
// and free in this knowledge base, and that the adder may choose another.
func TestUserAddGeneratesKey(t *testing.T) {
	root := newTestKB(t)
	out := mustRun(t, "user", "add", "Alice", "Cooper", "--email", "ac@example.com", "--user", "alice")
	if !strings.Contains(out, "not added yet") || !strings.Contains(out, " alice-c.") {
		t.Fatalf("expected the key alice-c, alice being taken:\n%s", out)
	}
	if mustLoadUsers(root).has("alice-c") {
		t.Fatal("kb user add without --key added the person")
	}
	mustRun(t, "user", "add", "Alice Cooper", "--email", "ac@example.com", "--key", "cooper", "--user", "alice")
	p, ok := mustLoadUsers(root).get("cooper")
	if !ok || p.name != "Alice Cooper" || p.email != "ac@example.com" {
		t.Fatalf("row for cooper: %+v %v", p, ok)
	}
	mustRefuse(t, "already in the user table", "user", "add", "Someone", "--email", "x@example.com", "--key", "cooper", "--user", "alice")
	mustRefuse(t, "already has the email", "user", "add", "Someone", "--email", "AC@example.com", "--key", "someone", "--user", "alice")
	mustRefuse(t, "is not a key", "user", "add", "Someone", "--email", "y@example.com", "--key", "some one", "--user", "alice")
	mustRefuse(t, "--email is required", "user", "add", "Someone", "--key", "someone", "--user", "alice")
	if out := mustRun(t, "user"); !strings.Contains(out, "cooper") || !strings.Contains(out, "Alice Cooper  <ac@example.com>") {
		t.Fatalf("kb user:\n%s", out)
	}
	checkClean(t)
}

func TestGenerateKey(t *testing.T) {
	root := newTestKB(t)
	tbl := mustLoadUsers(root)
	acts := actKeys(root, loadAllSessions(root))
	for name, want := range map[string]string{"Jeff Brown": "jeff", "Bob Brown": "bob-b", "bob": "bob-2", "": "person", "Ö": "person"} {
		if got := generateKey(root, tbl, acts, name); got != want {
			t.Errorf("generateKey(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestActRefusedUntilAdded checks that someone not in the user table cannot act, and can add themselves.
func TestActRefusedUntilAdded(t *testing.T) {
	root := newTestKB(t)
	d := writeDraft(t, root, "one", strings.Replace(front, "%s", "Statement one", 1), "Statement one text.")
	mustRefuse(t, "Kent Rule 56", "attest", d, "--proposed", turnPtr(0), "--turn", turnPtr(1), "--user", "carol")
	// carol, not in the table, adds only herself
	mustRefuse(t, "add yourself first", "user", "add", "Dave Diaz", "--email", "dave@example.com", "--key", "dave", "--user", "carol")
	mustRun(t, "user", "add", "Carol Chen", "--email", "carol@example.com", "--key", "carol", "--user", "carol")
	if !mustLoadUsers(root).has("carol") {
		t.Fatal("carol did not add herself")
	}
	checkClean(t)
}

// TestGitEmailLooksUpTheTable checks that git's user.email finds the key whose row holds it.
func TestGitEmailLooksUpTheTable(t *testing.T) {
	root := newTestKB(t)
	if key, msg := actingKey(root, "Alice@Example.com", gitEmailSource); key != "alice" || msg != "" {
		t.Fatalf("actingKey by email = %q, %q", key, msg)
	}
	if _, msg := actingKey(root, "nobody@example.com", gitEmailSource); !strings.Contains(msg, "Kent Rule 56") {
		t.Fatalf("an email nobody has: %q", msg)
	}
	if key, msg := actingKey(root, "bob", "from --user"); key != "bob" || msg != "" {
		t.Fatalf("actingKey by key = %q, %q", key, msg)
	}
}

// TestOverrideUser checks that changing a row is an override: checked twice, logged, and the key kept.
func TestOverrideUser(t *testing.T) {
	root := newTestKB(t)
	checkedBefore(t)
	mustRefuse(t, "must change the row", "override", "user", "alice", "--name", "Alice Adams", "--user", "alice")
	mustRefuse(t, "Kent Rule 52", "override", "user", "alice", "--name", "Alice Smith", "--confirm", "--turn", turnPtr(5), "--reason", "x", "--user", "alice")
	out := override(t, "alice", 5, "user", "alice", "--name", "Alice Smith", "--email", "alice@smith.example")
	if !strings.Contains(out, "alice is now Alice Smith <alice@smith.example>, was Alice Adams <alice@example.com>") {
		t.Fatalf("override user said:\n%s", out)
	}
	p, _ := mustLoadUsers(root).get("alice")
	if p.name != "Alice Smith" || p.email != "alice@smith.example" {
		t.Fatalf("row after the override: %+v", p)
	}
	entries, _ := mustLoadOverrideLog(root)
	if len(entries) != 1 || entryStr(entries[0], "kind") != ovUser || entryStr(entries[0], "was_name") != "Alice Adams" {
		t.Fatalf("override log: %v", entries)
	}
	checkClean(t)
	if out := mustRun(t, "show", turnPtr(1)); !strings.Contains(out, "alice (Alice Smith)") {
		t.Fatalf("kb show does not name the author:\n%s", out)
	}
}

// TestRenameCarriesTheRow checks that renaming a key carries its row to the new key.
func TestRenameCarriesTheRow(t *testing.T) {
	root := newTestKB(t)
	attestOne(t, root)
	checkedBefore(t)
	out := override(t, "alice", 5, "rename", "alice", "carol")
	if !strings.Contains(out, "row for alice becomes carol's") {
		t.Fatalf("rename said:\n%s", out)
	}
	tbl := mustLoadUsers(root)
	if p, ok := tbl.get("carol"); !ok || p.name != "Alice Adams" || tbl.has("alice") {
		t.Fatalf("table after the rename: %+v", tbl.rows)
	}
	checkClean(t)
}

// TestCheckVerifiesTheTable checks that kb check finds a table changed by hand.
func TestCheckVerifiesTheTable(t *testing.T) {
	root := newTestKB(t)
	p := filepath.Join(root, usersName)
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "Bob Brown", "Robert Brown", 1)), 0o666); err != nil {
		t.Fatal(err)
	}
	checkFails(t, "the row for bob is not what kb wrote")
	if err := os.WriteFile(p, []byte(string(b)+`{"key": "eve", "name": "Eve", "email": "eve@example.com"}`+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	checkFails(t, "eve has a row that no add")
	if err := os.WriteFile(p, []byte(`{"key": "eve", "name": "Eve", "email": "e@x", "role": "admin"}`+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	checkFails(t, "exactly a key, a name and an email")
}

// TestActsBeforeTheTable checks that keys on acts from before the table are warned about, and keys on
// acts since it began are errors.
func TestActsBeforeTheTable(t *testing.T) {
	root := newTestKB(t)
	for _, f := range []string{usersName, usersLogName} {
		if err := os.Remove(filepath.Join(root, f)); err != nil {
			t.Fatal(err)
		}
	}
	out := checkClean(t)
	if !strings.Contains(out, "warning: alice:") || !strings.Contains(out, "--key alice") {
		t.Fatalf("no warning about alice's acts:\n%s", out)
	}
	// the table began before bob's turns, which no row covers
	old := usersNow
	usersNow = func() time.Time { return time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { usersNow = old })
	mustRun(t, "user", "add", "Alice Adams", "--email", "alice@example.com", "--key", "alice", "--user", "alice")
	checkFails(t, "bob: 2 act(s) carry this key, some made since the user table began")
}

// TestSessionStartListsTheTable checks that the session-start hook names the human and lists the table.
func TestSessionStartListsTheTable(t *testing.T) {
	newTestKB(t)
	isolateHome(t)
	t.Setenv("KB_USER", "alice")
	out := mustRun(t, "session-start")
	for _, want := range []string{"The human is recorded as alice (Alice Adams) (from KB_USER).", "  - bob: Bob Brown <bob@example.com>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session-start lacks %q:\n%s", want, out)
		}
	}
	t.Setenv("KB_USER", "zed")
	if out := mustRun(t, "session-start"); !strings.Contains(out, "zed is not in the knowledge base's user table") {
		t.Fatalf("session-start does not say zed cannot act:\n%s", out)
	}
}
