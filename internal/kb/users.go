package kb

// The user table (S-0138 to S-0143): who each key in the record is. Each act names its author by a key
// that never changes once used, readable so the files stay easy to search and interpret; the table,
// users.jsonl at the top of the knowledge base, says who each key is now, by name and email, and holds
// nothing else (S-0139). It is treated as fact, not testimony. Only kb writes it: kb user add appends a
// row and logs the act in users-log.jsonl, and changing a row's name or email is an override (S-0141),
// logged in overrides.jsonl. kb check replays the two logs and compares the table with what they say.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

const (
	usersName    = "users.jsonl"
	usersLogName = "users-log.jsonl"
)

func usersPath(root string) string    { return filepath.Join(root, usersName) }
func usersLogPath(root string) string { return filepath.Join(root, usersLogName) }

// usersNow is the clock kb user add reads, so tests can begin the table before or after a turn.
var usersNow = now

// person is one row of the user table.
type person struct{ key, name, email string }

// userTable is the table as read.
type userTable struct{ rows []person }

func (u *userTable) get(key string) (person, bool) {
	for _, p := range u.rows {
		if p.key == key {
			return p, true
		}
	}
	return person{}, false
}

func (u *userTable) has(key string) bool { _, ok := u.get(key); return ok }

// byEmail is the row with this email, compared as email addresses are, ignoring case.
func (u *userTable) byEmail(email string) (person, bool) {
	if email == "" {
		return person{}, false
	}
	for _, p := range u.rows {
		if strings.EqualFold(p.email, email) {
			return p, true
		}
	}
	return person{}, false
}

// who is how kb shows an author: the key, with the name beside it when the table has one (S-0138).
func (u *userTable) who(key string) string {
	if u != nil {
		if p, ok := u.get(key); ok && p.name != "" {
			return key + " (" + p.name + ")"
		}
	}
	return key
}

// loadUsers reads the user table. problem is "" unless the file is not what kb writes.
func loadUsers(root string) (*userTable, string) {
	t := &userTable{}
	p := usersPath(root)
	if !py.IsFile(p) {
		return t, ""
	}
	for i, line := range py.TextLines(p) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		v, err := py.Loads(line)
		d, ok := v.(*py.Dict)
		if err != nil || !ok {
			return t, fmt.Sprintf("line %d is not a JSON object", i+1)
		}
		keys := d.Keys()
		if len(keys) != 3 || !d.Has("key") || !d.Has("name") || !d.Has("email") {
			return t, fmt.Sprintf("line %d does not hold exactly a key, a name and an email", i+1)
		}
		var vals [3]string
		for j, k := range []string{"key", "name", "email"} {
			s, ok := d.Get(k).(string)
			if !ok {
				return t, fmt.Sprintf("line %d: %s is not a string", i+1, k)
			}
			vals[j] = s
		}
		t.rows = append(t.rows, person{vals[0], vals[1], vals[2]})
	}
	return t, ""
}

func mustLoadUsers(root string) *userTable {
	t, problem := loadUsers(root)
	if problem != "" {
		die(fmt.Sprintf("the user table %s cannot be read: %s (%s)", usersName, problem, rule("write-through-kb")))
	}
	return t
}

// usersForDisplay is the table for showing names beside keys; a table that cannot be read shows none.
func usersForDisplay(root string) *userTable {
	t, problem := loadUsers(root)
	if problem != "" {
		return &userTable{}
	}
	return t
}

// writeUsers writes the table whole: one row per line, in the order people were added.
func writeUsers(root string, rows []person) {
	var b strings.Builder
	for _, p := range rows {
		line := py.Dumps(py.D("key", p.key, "name", p.name, "email", p.email))
		py.CheckEncodable(line)
		b.WriteString(line + "\n")
	}
	tmp := usersPath(root) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o666); err != nil {
		py.RaiseOS(err)
	}
	if err := os.Rename(tmp, usersPath(root)); err != nil {
		py.RaiseOS(err)
	}
}

// ---------------------------------------------------------------- who is acting

const gitEmailSource = "from git's user.email"

// actingKey is the key of the person kb takes to be acting (S-0143), from the id userSource found: the key
// the user set, or their git email looked up in the user table. msg says why they cannot act, or is "".
func actingKey(root, id, source string) (key, msg string) {
	return actingKeyIn(root, mustLoadUsers(root), id, source)
}

func actingKeyIn(root string, t *userTable, id, source string) (key, msg string) {
	key = id
	if source == gitEmailSource {
		p, ok := t.byEmail(id)
		if !ok {
			return "", fmt.Sprintf("nobody in this knowledge base's user table has the email %s (git's user.email), so kb "+
				"records nothing you do here. Add yourself first: kb user add <your name> --email %s, and choose your key "+
				"(%s)", id, id, rule("add-first"))
		}
		key = p.key
	}
	if to, seq := retiredID(root, key); to != "" {
		return "", fmt.Sprintf("the user id %s was moved to %s by override %d in this knowledge base; act as %s: change "+
			"the id in ~/.kenttheclerk.json, KB_USER or git's user.email, or pass --user (%s)", py.StrRepr(key), py.StrRepr(to), seq, to, rule("override-list"))
	}
	if !t.has(key) {
		return "", fmt.Sprintf("%s (%s) is not in this knowledge base's user table, so kb records nothing they do here. "+
			"Add them first: kb user add <their name> --email <their email> --key %s (%s)", py.StrRepr(key), strings.TrimPrefix(source, "from "), key, rule("add-first"))
	}
	return key, ""
}

// smallTable is how many people the session-start hook lists, so the agent has everyone in context; a
// larger table is left to kb user.
const smallTable = 20

// describeUsers is what kb session-start and kb setup say about the user table and the human's place in
// it: lines to print, and a problem when the human cannot act. It never dies.
func describeUsers(root string) (lines []string, problem string) {
	t, bad := loadUsers(root)
	if bad != "" {
		return nil, fmt.Sprintf("the user table %s cannot be read (%s), so kb records nothing anyone does", usersName, bad)
	}
	u, source := userSource("", root)
	if u == "" {
		problem = "no user id is set, so kb cannot record anything the human does"
	} else if key, msg := actingKeyIn(root, t, u, source); msg == "" {
		name := key
		if p, ok := t.get(key); ok && p.name != "" {
			name = key + ", " + p.name
		}
		lines = append(lines, fmt.Sprintf("The human is recorded as %s (%s).", name, source))
	} else {
		lines = append(lines, fmt.Sprintf("The human's id is %s (%s).", u, source))
		problem = fmt.Sprintf("%s is not in the knowledge base's user table, so kb cannot record anything the human does "+
			"until they add themselves: kb user add <their name> --email <their email> (%s)", u, rule("add-first"))
		if to, _ := retiredID(root, u); to != "" {
			problem = fmt.Sprintf("the id %s was moved to %s by an override here, so the human sets their id to %s", u, to, to)
		}
	}
	switch n := len(t.rows); {
	case n == 0:
		lines = append(lines, "The user table is empty: nobody can act until they add themselves (`kb rules people`).")
	case n <= smallTable:
		lines = append(lines, "The user table, which says who each key in the record is (`kb rules people`):")
		for _, p := range t.rows {
			lines = append(lines, fmt.Sprintf("  - %s: %s <%s>", p.key, p.name, p.email))
		}
	default:
		lines = append(lines, fmt.Sprintf("The user table has %d people; `kb user` lists them (`kb rules people`).", n))
	}
	return lines, problem
}

// ---------------------------------------------------------------- keys

var badKeyRE = regexp.MustCompile(`[\s#"<>]`)

// badKey says what is wrong with a key someone chose, or "".
func badKey(key string) string {
	switch {
	case key == "":
		return "a key cannot be empty"
	case badKeyRE.MatchString(key):
		return fmt.Sprintf("%s is not a key: a key holds no spaces, #, quotes or angle brackets", py.StrRepr(key))
	case strings.HasPrefix(key, "agent:") || in(key, []string{"client", "tool", "document"}):
		return fmt.Sprintf("%s is not a person's key", py.StrRepr(key))
	}
	return ""
}

// actCounts is how many acts carry each person's key: human turns, attestations, and entries in the
// override log and the user log, which are read through their renames. first is the earliest act's time.
type actCount struct {
	turns, atts, logged int
	first, last         time.Time
}

func (c *actCount) total() int { return c.turns + c.atts + c.logged }

func (c *actCount) saw(at string) {
	t, ok := parseAt(at)
	if !ok {
		return
	}
	if c.first.IsZero() || t.Before(c.first) {
		c.first = t
	}
	if t.After(c.last) {
		c.last = t
	}
}

// parseAt reads the times acts carry: "2026-10-15T22:08:59+00:00" or "2026-10-14T10:00:00.000Z".
func parseAt(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	return t, err == nil
}

func personKey(k string) bool {
	return k != "" && !strings.HasPrefix(k, "agent:") && !in(k, []string{"client", "tool", "document"})
}

func actKeys(root string, sessions *Sessions) map[string]*actCount {
	out := map[string]*actCount{}
	at := func(k string) *actCount {
		if out[k] == nil {
			out[k] = &actCount{}
		}
		return out[k]
	}
	for _, s := range sessions.Values() {
		for _, t := range s.Turns {
			if a := py.Str(py.Or(t.Get("author"), "")); py.Equal(t.Get("role"), "user") && personKey(a) {
				c := at(a)
				c.turns++
				c.saw(py.Str(py.Or(t.Get("timestamp"), "")))
			}
		}
	}
	for _, st := range append(loadAllStatements(root), loadAllAgentStatements(root)...) {
		for _, a := range listOf(st.Meta.Get("attestations")) {
			if d, ok := a.(*py.Dict); ok {
				if by := py.Str(py.Or(d.Get("by"), "")); personKey(by) {
					c := at(by)
					c.atts++
					c.saw(py.Str(py.Or(d.Get("at"), "")))
				}
			}
		}
	}
	entries, _, _ := loadOverrideLog(root)
	for _, e := range entries {
		if by := idAfter(entries, entrySeq(e), entryStr(e, "by"), entryStr(e, "turn")); personKey(by) {
			c := at(by)
			c.logged++
			c.saw(entryStr(e, "at"))
		}
	}
	adds, _, _ := loadLogFile(usersLogPath(root))
	for _, e := range adds {
		if by := idAfter(entries, 1, entryStr(e, "by"), ""); personKey(by) {
			c := at(by)
			c.logged++
			c.saw(entryStr(e, "at"))
		}
	}
	return out
}

// keyTaken says whether a key is in use: in the table, carried by acts, or retired by an override.
func keyTaken(root string, t *userTable, acts map[string]*actCount, key string) bool {
	if t.has(key) || acts[key] != nil {
		return true
	}
	to, _ := retiredID(root, key)
	return to != ""
}

// generateKey makes a readable key from a name (S-0138): the first name, then the first name with more and
// more of the last name until one is free (jeff, jeff-b, jeff-br, ...), then a number.
func generateKey(root string, t *userTable, acts map[string]*actCount, name string) string {
	var words []string
	for _, w := range strings.Split(slugify(name, 200), "-") {
		if w != "" {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		words = []string{"person"}
	}
	first := words[0]
	cands := []string{first}
	if len(words) > 1 {
		last := words[len(words)-1]
		for i := 1; i <= len(last); i++ {
			cands = append(cands, first+"-"+last[:i])
		}
	}
	for _, c := range cands {
		if !keyTaken(root, t, acts, c) {
			return c
		}
	}
	base := cands[len(cands)-1]
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s-%d", base, n); !keyTaken(root, t, acts, c) {
			return c
		}
	}
}

// ---------------------------------------------------------------- the user log and replaying it

// replayUsers is the table the logs say there should be: every add in the user log, then each override
// that touches a row, in order. Adds can all come first because an add refuses a key that is in the table
// or retired, so no override in the log can depend on an add made after it.
func replayUsers(adds, overrides []*py.Dict) []person {
	var rows []person
	find := func(k string) int {
		for i, p := range rows {
			if p.key == k {
				return i
			}
		}
		return -1
	}
	for _, e := range adds {
		rows = append(rows, person{entryStr(e, "key"), entryStr(e, "name"), entryStr(e, "email")})
	}
	for _, e := range overrides {
		switch entryStr(e, "kind") {
		case ovRename, ovMerge:
			i, j := find(entryStr(e, "from")), find(entryStr(e, "to"))
			switch {
			case i >= 0 && j >= 0:
				rows = append(rows[:i], rows[i+1:]...)
			case i >= 0:
				rows[i].key = entryStr(e, "to")
			}
		case ovUser:
			if i := find(entryStr(e, "key")); i >= 0 {
				rows[i].name, rows[i].email = entryStr(e, "name"), entryStr(e, "email")
			}
		}
	}
	return rows
}

// moveRow is what a merge (or a rename, as kb made them before S-0148) does to the table: the row of from becomes to's, or, when to has a row
// already, goes, since the two are one person.
func moveRow(root, from, to string) string {
	t := mustLoadUsers(root)
	var out []person
	note := ""
	_, toHas := t.get(to)
	for _, p := range t.rows {
		if p.key == from {
			if toHas {
				note = fmt.Sprintf("the user table's row for %s is removed, since %s has one", from, to)
				continue
			}
			p.key = to
			note = fmt.Sprintf("the user table's row for %s becomes %s's", from, to)
		}
		out = append(out, p)
	}
	if note != "" {
		writeUsers(root, out)
	}
	return note
}

// mayAddUser says whether by may add a person to the user table. In single-player and cooperative mode
// anyone may, including themselves (S-0140, S-0142); in PvP mode only the administrator would, except the
// first person in an empty table. PvP mode is not built, so this is where it would answer otherwise.
func mayAddUser(root, by, key string) bool { return true }

// adder is who is adding a person: someone in the table, or the person themselves (S-0142).
func adder(root string, t *userTable, explicit, key, email string) string {
	id, source := userSource(explicit, root)
	switch {
	case id == "":
		return key
	case source == gitEmailSource:
		if p, ok := t.byEmail(id); ok {
			return p.key
		}
		if strings.EqualFold(id, email) {
			return key
		}
	default:
		if to, seq := retiredID(root, id); to != "" {
			die(fmt.Sprintf("the user id %s was moved to %s by override %d in this knowledge base; act as %s (%s)", py.StrRepr(id), py.StrRepr(to), seq, to, rule("override-list")))
		}
		if t.has(id) || id == key {
			return id
		}
	}
	die(fmt.Sprintf("you are known here as %s (%s), who is not in the user table: add yourself first, then add "+
		"others (%s)", py.StrRepr(id), strings.TrimPrefix(source, "from "), rule("add-first")))
	return ""
}

var emailRE = regexp.MustCompile(`^[^\s@<>"]+@[^\s@<>"]+$`)

func runUserAdd(ns argparse.Namespace, args []string) int {
	root := repoRoot()
	requirePin(root)
	name := normalizeWS(strings.Join(args, " "))
	if name == "" {
		die("kb user add takes the person's name: kb user add <name> --email <email> [--key <key>]")
	}
	email, _ := ns.Str("email")
	email = strings.TrimSpace(email)
	if email == "" {
		die(fmt.Sprintf("--email is required: the user table holds each person's key, name and email (%s)", rule("user-table")))
	}
	if !emailRE.MatchString(email) {
		die(fmt.Sprintf("%s is not an email address", py.StrRepr(email)))
	}
	t := mustLoadUsers(root)
	if p, ok := t.byEmail(email); ok {
		die(fmt.Sprintf("%s already has the email %s in the user table", t.who(p.key), email))
	}
	acts := actKeys(root, loadAllSessions(root))
	keyArg, _ := ns.Str("key")
	if keyArg == "" {
		k := generateKey(root, t, acts, name)
		println(fmt.Sprintf("not added yet: the key kb generates for %s is %s.", name, k))
		var legacy []string
		for key, c := range acts {
			if !t.has(key) {
				legacy = append(legacy, fmt.Sprintf("%s (%d turn(s), %d attestation(s))", key, c.turns, c.atts))
			}
		}
		if len(legacy) > 0 {
			sort.Strings(legacy)
			println("Acts already in the record carry keys nobody in the table has: " + strings.Join(legacy, ", ") +
				". A person whose acts carry one chooses it as their key.")
		}
		println(fmt.Sprintf("Ask the human whether to use %s or another key; a key never changes once used. Then run "+
			"the same command with --key <key> (%s).", k, rule("add-first")))
		return 0
	}
	key := strings.TrimSpace(keyArg)
	if msg := badKey(key); msg != "" {
		die(msg)
	}
	if t.has(key) {
		die(fmt.Sprintf("%s is already in the user table", t.who(key)))
	}
	if to, seq := retiredID(root, key); to != "" {
		die(fmt.Sprintf("%s was moved to %s by override %d and is retired here", py.StrRepr(key), to, seq))
	}
	userArg, _ := ns.Str("user")
	by := adder(root, t, userArg, key, email)
	if !mayAddUser(root, by, key) {
		die(fmt.Sprintf("%s may not add people to this knowledge base's user table (%s)", by, rule("add-first")))
	}
	entries, lines, problem := loadLogFile(usersLogPath(root))
	if problem != "" {
		die(fmt.Sprintf("the user log %s cannot be read: %s (%s)", usersLogName, problem, rule("write-through-kb")))
	}
	e := py.D("seq", py.IntOf(len(entries)+1), "act", "add", "key", key, "name", name, "email", email,
		"at", isoSeconds(usersNow()), "by", by, "authentication", authentication)
	appendLogEntry(usersLogPath(root), e, lines)
	writeUsers(root, append(t.rows, person{key, name, email}))
	msg := fmt.Sprintf("added %s to the user table: %s, %s", key, name, email)
	if by != key {
		msg += ", by " + t.who(by)
	}
	println(msg)
	if c := acts[key]; c != nil {
		println(fmt.Sprintf("%d turn(s) and %d attestation(s) already recorded under %s are now shown as %s's", c.turns, c.atts, key, name))
	}
	return 0
}

func runUserList(root string) int {
	t := mustLoadUsers(root)
	if len(t.rows) == 0 {
		println(fmt.Sprintf("the user table is empty: nobody can act here until they are added, with kb user add (%s)", rule("add-first")))
		return 0
	}
	width := 0
	for _, p := range t.rows {
		width = max(width, len(p.key))
	}
	for _, p := range t.rows {
		println(fmt.Sprintf("%-*s  %s  <%s>", width, p.key, p.name, p.email))
	}
	return 0
}

func runUser(ns argparse.Namespace) int {
	what, _ := ns.Str("what")
	args := ns.List("args")
	switch what {
	case "", "list":
		if len(args) > 0 {
			die("kb user list takes nothing more")
		}
		return runUserList(repoRoot())
	case "add":
		return runUserAdd(ns, args)
	}
	die(fmt.Sprintf("kb user %s: there is kb user list and kb user add; a change to someone's name or email is an "+
		"override, kb override user (%s)", what, rule("user-change")))
	return 1
}

func addUserParser(sub *argparse.Parser) {
	p := sub.AddParser("user", "list the user table, or add a person to it (kb rules people)")
	p.Positional("what", argparse.Optional, "list (the default) | add")
	p.Positional("args", argparse.ZeroMore, "add: the person's name")
	p.Option("--email", "add: the person's email", nil, false)
	p.Option("--key", "add: the key they are known by; omit it to see the key kb generates", nil, false)
	p.Option("--user", "who is adding them (else KB_USER / ~/.kenttheclerk.json / git's user.email)", nil, false)
	p.Run = runUser
}

// ---------------------------------------------------------------- kb check

// usersChecks verifies the user table and its log, and that every key on an act has a row, for kb check.
// Acts from before the table began, when nobody could be in it, are only warned about (S-0142).
func usersChecks(root string, sessions *Sessions, errorf, warnf func(string, ...any)) {
	overrides, _, _ := loadOverrideLog(root)
	t, problem := loadUsers(root)
	if problem != "" {
		errorf("user table: %s (%s)", problem, rule("write-through-kb"))
	}
	adds, lines, problem := loadLogFile(usersLogPath(root))
	if problem != "" {
		errorf("user log: %s (%s)", problem, rule("write-through-kb"))
	}
	for i, e := range adds {
		n := i + 1
		if entrySeq(e) != n {
			errorf("user log: entry %d is numbered %s; entries were removed or reordered outside kb (%s)", n, py.Repr(e.Get("seq")), rule("write-through-kb"))
		}
		var want any
		if i > 0 {
			want = sha256Tag(lines[i-1])
		}
		if !py.Equal(e.Get("prev"), want) {
			errorf("user log: entry %d does not carry the hash of the entry before it; the log was changed outside kb (%s)", n, rule("write-through-kb"))
		}
		if entryStr(e, "act") != "add" {
			errorf("user log: entry %d is %s, not an add (%s)", n, py.Repr(e.Get("act")), rule("add-first"))
		}
		for _, k := range []string{"key", "name", "email", "at", "by"} {
			if entryStr(e, k) == "" {
				errorf("user log: entry %d has no %s (%s)", n, k, rule("user-table"))
			}
		}
		if !systemHashRE.MatchString(entryStr(e, "system_hash")) || entryStr(e, "system_version") == "" {
			errorf("user log: entry %d has a missing or malformed system stamp (%s)", n, rule("system-stamp"))
		}
	}
	if problem == "" {
		want := replayUsers(adds, overrides)
		got := map[string]person{}
		for _, p := range t.rows {
			got[p.key] = p
		}
		wantBy := map[string]person{}
		for _, p := range want {
			wantBy[p.key] = p
			if g, ok := got[p.key]; !ok {
				errorf("user table: %s was added, but has no row; the table was changed outside kb (%s)", p.key, rule("write-through-kb"))
			} else if g != p {
				errorf("user table: the row for %s is not what kb wrote: it holds %s <%s>, and the logs say %s <%s> (%s)",
					p.key, g.name, g.email, p.name, p.email, rule("write-through-kb"))
			}
		}
		for _, p := range t.rows {
			if _, ok := wantBy[p.key]; !ok {
				errorf("user table: %s has a row that no add in %s made; the table was changed outside kb (%s)", p.key, usersLogName, rule("write-through-kb"))
			}
		}
	}
	seen := map[string]bool{}
	emails := map[string]string{}
	for _, p := range t.rows {
		if seen[p.key] {
			errorf("user table: %s has two rows (%s)", p.key, rule("user-table"))
		}
		seen[p.key] = true
		if o, ok := emails[strings.ToLower(p.email)]; ok && p.email != "" {
			errorf("user table: %s and %s have the same email, %s (%s)", o, p.key, p.email, rule("user-table"))
		}
		emails[strings.ToLower(p.email)] = p.key
	}

	var began time.Time
	if len(adds) > 0 {
		began, _ = parseAt(entryStr(adds[0], "at"))
	}
	acts := actKeys(root, sessions)
	keys := make([]string, 0, len(acts))
	for k := range acts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := acts[k]
		if t.has(k) {
			continue
		}
		if !began.IsZero() && !c.last.Before(began) {
			errorf("%s: %d act(s) carry this key, some made since the user table began, and nobody in the table has it; "+
				"kb refuses acts by someone not in the table (%s)", k, c.total(), rule("add-first"))
			continue
		}
		warnf("%s: %d act(s) from before the user table carry this key, and nobody in the table has it yet; the person "+
			"adds themselves with kb user add <their name> --email <their email> --key %s (%s)", k, c.total(), k, rule("add-first"))
	}
}
