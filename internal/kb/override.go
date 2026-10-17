package kb

// kb override — the closed list of overrides (S-0132 to S-0137): changes to the record against its own
// rules, discouraged but permitted, so that nobody has to go outside kb to make them. Each is made from an
// on-record session on the authority of the human's own turn there, and entered in the override log, which
// kb only appends to: the record is as it was written except where the log says otherwise.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
)

// overrideLogName is the override log, at the top of the knowledge base: one JSON entry per line. An entry
// carries no hash of the one before it (S-8MW2): entries added in separate copies are independent lines,
// which git keeps both of when it merges the log, and git history is what shows a change to one.
const overrideLogName = "overrides.jsonl"

// overrideIDRE is a new override's id (S-QYEK): O- and four characters in the form of a statement's
// (S-0149). The overrides made before it are cited by their numbers, which stay valid.
var overrideIDRE = regexp.MustCompile("^O-[0-9A-HJKMNP-TV-Z]{4}$")

func overrideLogPath(root string) string { return filepath.Join(root, overrideLogName) }

// The kinds of entry in the log. Removing a whole session (S-0135) is a scrub at session scale.
const (
	ovRename         = "rename" // no longer made (S-0148), but read in a log that holds one
	ovMerge          = "merge"
	ovReassign       = "reassign"
	ovScrubTurn      = "scrub-turn"
	ovScrubSession   = "scrub-session"
	ovScrubStatement = "scrub-statement"
	ovRevise         = "revise"
	ovUser           = "user"
)

var overrideKinds = []string{ovRename, ovMerge, ovReassign, ovScrubTurn, ovScrubSession, ovScrubStatement, ovRevise, ovUser}

// overrideNow is the clock overrides read, so tests can place the double-check before or after a turn.
var overrideNow = now

// ---------------------------------------------------------------- the log

// loadOverrideLog reads the log's entries in the order they were made. problem is "" unless a line is not
// a JSON object.
func loadOverrideLog(root string) (entries []*dyn.Dict, problem string) {
	return loadLogFile(overrideLogPath(root))
}

// loadLogFile reads a log kb only appends to, the override log or the user log, in the order its entries
// were made (S-QYEK): by their at, and where two share a time, in the order of their lines. A merge leaves
// one copy's new entries after the other's, so the order of the lines is not the order they were made in.
func loadLogFile(p string) (entries []*dyn.Dict, problem string) {
	if !dyn.IsFile(p) {
		return nil, ""
	}
	for i, line := range dyn.TextLines(p) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		v, err := dyn.Loads(line)
		d, ok := v.(*dyn.Dict)
		if err != nil || !ok {
			problem = fmt.Sprintf("line %d is not a JSON object", i+1)
			break
		}
		entries = append(entries, d)
	}
	// the times are ISO timestamps in UTC, so they compare as strings
	sort.SliceStable(entries, func(i, j int) bool { return entryStr(entries[i], "at") < entryStr(entries[j], "at") })
	return entries, problem
}

func mustLoadOverrideLog(root string) []*dyn.Dict {
	entries, problem := loadOverrideLog(root)
	if problem != "" {
		die(fmt.Sprintf("the override log %s cannot be read: %s (%s)", overrideLogName, problem, rule("write-through-kb")))
	}
	return entries
}

// sha256Tag is "sha256:" and the hex digest of s: the hash of a revised statement's text.
func sha256Tag(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

// entrySeq is the number an override made before S-QYEK carries, or 0 when it has none.
func entrySeq(e *dyn.Dict) int {
	n, err := strconv.Atoi(dyn.Str(dyn.Or(e.Get("seq"), "")))
	if err != nil {
		return 0
	}
	return n
}

func entryStr(e *dyn.Dict, k string) string { return dyn.Str(dyn.Or(e.Get(k), "")) }

// overrideRef is how an override is cited: its id, or the number of one made before S-QYEK, written as
// text. "" when it has neither.
func overrideRef(e *dyn.Dict) string {
	if id := entryStr(e, "id"); id != "" {
		return id
	}
	if n := entrySeq(e); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// newOverrideID is a new override's id (S-QYEK), drawn as a statement's is, and not one the log holds.
func newOverrideID(root string) string {
	taken := map[string]bool{}
	for _, e := range mustLoadOverrideLog(root) {
		taken[overrideRef(e)] = true
	}
	return drawID("O", func(id string) bool { return taken[id] }, idSource)
}

// overrideAuth is the authority an override is made on: who, the turn of theirs that authorizes it, and why.
type overrideAuth struct{ user, turn, reason string }

// appendOverride enters an override in the log under its id.
func appendOverride(root string, id string, kind string, auth overrideAuth, fields *dyn.Dict) {
	for _, e := range mustLoadOverrideLog(root) {
		if overrideRef(e) == id {
			die(fmt.Sprintf("the override log gained an entry with the id %s while this override was being made", id))
		}
	}
	e := dyn.D("id", id, "kind", kind, "at", isoSeconds(overrideNow()), "by", auth.user,
		"authentication", authentication, "turn", auth.turn, "reason", auth.reason)
	for _, k := range fields.Keys() {
		e.Set(k, fields.Get(k))
	}
	appendLogEntry(overrideLogPath(root), e)
}

// appendLogEntry stamps an entry and appends it to the log at p.
func appendLogEntry(p string, e *dyn.Dict) {
	addStamp(e)
	line := dyn.Dumps(e)
	dyn.CheckEncodable(line)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		dyn.RaiseOS(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		dyn.RaiseOS(err)
	}
}

// retiredID follows an id through the renames and merges in the log: the id that took its place and the
// last override that moved it, or ("", "") when no override moved it.
func retiredID(root, id string) (string, string) {
	entries, _ := loadOverrideLog(root)
	to, ref := "", ""
	for _, e := range entries {
		k := entryStr(e, "kind")
		if (k == ovRename || k == ovMerge) && entryStr(e, "from") == id {
			id, to, ref = entryStr(e, "to"), entryStr(e, "to"), overrideRef(e)
		}
	}
	return to, ref
}

// idAfter is the id an act recorded as by id in entries[from] is recorded under now: the renames and
// merges from that entry on, in the order made, move it, and so does a reassignment of the turn itself.
func idAfter(entries []*dyn.Dict, from int, id, turn string) string {
	for _, e := range entries[min(from, len(entries)):] {
		switch entryStr(e, "kind") {
		case ovRename, ovMerge:
			if entryStr(e, "from") == id {
				id = entryStr(e, "to")
			}
		case ovReassign:
			if entryStr(e, "target") == turn && entryStr(e, "from") == id {
				id = entryStr(e, "to")
			}
		}
	}
	return id
}

// ---------------------------------------------------------------- authority and the double-check

// mayOverride says whether user may make overrides in this knowledge base. A knowledge base in git is
// cooperative, where everyone has full authority (S-0121); in PvP mode only the administrator would
// (S-0134). PvP mode is not built, so this is where it would answer otherwise.
func mayOverride(root, user string) bool { return true }

// overrideAuthority checks the turn an override is made on: a turn of the user's own, in an on-record
// conversation (S-0133). It returns the authority and the turn.
func overrideAuthority(root, user, turnArg, reason string) (overrideAuth, *dyn.Dict) {
	if !mayOverride(root, user) {
		die(fmt.Sprintf("%s may not make overrides in this knowledge base (%s)", user, rule("override-on-record")))
	}
	if turnArg == "" {
		die(fmt.Sprintf("--turn is required: the human's own turn, in this on-record conversation, that "+
			"authorizes the override (%s)", rule("override-on-record")))
	}
	if dyn.Strip(reason) == "" {
		die(fmt.Sprintf("--reason is required: why the override is made, for the log (%s)", rule("override-list")))
	}
	ptr := parsePointer(turnArg)
	s := findSession(root, ptr.Session)
	if s == nil {
		die(fmt.Sprintf("--turn: no evidence session %s", ptr.Session))
	}
	if !dyn.Equal(s.ModeValue(), "on-record") {
		die(fmt.Sprintf("--turn: session %s is %s; an override is made only from an on-record session, so "+
			"the conversation around it is on the record too (%s)", dyn.Head(s.ID(), 8), s.Mode(), rule("override-on-record")))
	}
	if s.Kind() == "document" {
		die(fmt.Sprintf("--turn: %s is a document; an override is authorized in a conversation (%s)", dyn.Head(s.ID(), 8), rule("override-on-record")))
	}
	t := s.Turn(ptr.UUID)
	if t == nil {
		die(fmt.Sprintf("--turn: no turn %s in session %s", ptr.UUID, dyn.Head(s.ID(), 8)))
	}
	if !dyn.Equal(item(t, "role"), "user") || !dyn.Equal(item(t, "author"), user) {
		die(fmt.Sprintf("--turn %s is authored by %s, not %s; an override is authorized by the human's own turn (%s)",
			ptr, dyn.Repr(item(t, "author")), dyn.StrRepr(user), rule("own-turn")))
	}
	return overrideAuth{user, Pointer{s.ID(), dyn.Str(item(t, "uuid"))}.String(), dyn.Strip(reason)}, t
}

// pendingPath is where the double-check of one override is noted between showing it and confirming it.
// It is off the record: it only orders the human's confirmation after what kb showed.
func pendingPath(root string, args []string) string {
	h := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return filepath.Join(root, "off-record", "overrides", hex.EncodeToString(h[:])[:16]+".json")
}

// showCheck is the first run of an override (S-0137): it prints what would change and the warning, notes
// when it showed them, and changes nothing in the record.
func showCheck(root string, args []string, plan []string) int {
	println("override not made: this is the check. It would:")
	for _, l := range plan {
		println("  " + l)
	}
	println("")
	println("An override is a last resort, not a normal mechanism: it changes the record against its own rules, " +
		"and the override log keeps the fact for good. It is not a way to settle a disagreement, to act as " +
		"someone else, or to change a statement something relies on.")
	println(fmt.Sprintf("Show the human all of the above and ask them to confirm that it is really what they want. "+
		"After they confirm in a turn of their own, snapshot the session and run the same command with --confirm, "+
		"--turn <that turn> and --reason <why> (%s).", rule("override-confirm")))
	p := pendingPath(root, args)
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		dyn.RaiseOS(err)
	}
	b, _ := json.Marshal(map[string]any{"override": args, "shown_at": overrideNow().Format(time.RFC3339Nano)})
	if err := os.WriteFile(p, b, 0o666); err != nil {
		dyn.RaiseOS(err)
	}
	return 0
}

// requireCheck refuses a confirmation that did not follow the check: the check must have been shown, and
// the confirming turn made after it.
func requireCheck(root string, args []string, turn *dyn.Dict) {
	p := pendingPath(root, args)
	b, err := os.ReadFile(p)
	var pending struct {
		ShownAt string `json:"shown_at"`
	}
	if err != nil || json.Unmarshal(b, &pending) != nil {
		die(fmt.Sprintf("this override has not been checked: run it first without --confirm, show the human what "+
			"it would change, and confirm with a turn of theirs made after that (%s)", rule("override-confirm")))
	}
	shown, err1 := time.Parse(time.RFC3339Nano, pending.ShownAt)
	at, err2 := time.Parse(time.RFC3339Nano, dyn.Str(dyn.Or(turn.Get("timestamp"), "")))
	if err1 != nil || err2 != nil || !at.After(shown) {
		die(fmt.Sprintf("the confirming turn was made before kb showed the check (at %s); the human confirms in a "+
			"turn of theirs after seeing it (%s)", pending.ShownAt, rule("override-confirm")))
	}
}

func clearCheck(root string, args []string) { _ = os.Remove(pendingPath(root, args)) }

// ---------------------------------------------------------------- acts by an id

// actsBy counts what is recorded as by id: human turns in conversations, on the record or off it, and
// attestations.
func actsBy(root, id string) (turns, atts int) {
	for _, s := range loadAllSessions(root).Values() {
		for _, t := range s.Turns {
			if dyn.Equal(t.Get("role"), "user") && dyn.Equal(t.Get("author"), id) {
				turns++
			}
		}
	}
	for _, st := range loadAllStatements(root) {
		for _, a := range listOf(st.Meta.Get("attestations")) {
			if d, ok := a.(*dyn.Dict); ok && dyn.Equal(d.Get("by"), id) {
				atts++
			}
		}
	}
	return turns, atts
}

// relabel records the acts by from as by to: all of them, or, with only, those made through that one turn.
// The override log is never relabelled; it is read through its own renames.
func relabel(root, from, to string, only *Pointer) (nTurns, nAtts, nFiles int) {
	through := func(v any) bool { return only == nil || dyn.Equal(v, only.String()) }
	for _, md := range evidenceDirs(root) {
		for _, p := range sortedGlob(md.dir, "*.jsonl") {
			s := loadSession(p)
			changed := false
			for _, t := range s.Turns {
				if !dyn.Equal(t.Get("role"), "user") || !dyn.Equal(t.Get("author"), from) {
					continue
				}
				if only != nil && (s.ID() != only.Session || !dyn.Equal(t.Get("uuid"), only.UUID)) {
					continue
				}
				t.Set("author", to)
				nTurns++
				changed = true
			}
			if parts, ok := s.Header.Get("participants").([]any); ok {
				var out []any
				for _, x := range parts {
					if only == nil && dyn.Equal(x, from) {
						x = to
					}
					if !containsValue(out, x) {
						out = append(out, x)
					}
				}
				if only != nil && changed && !containsValue(out, to) {
					out = append(out, to)
				}
				if dyn.Dumps(out) != dyn.Dumps(parts) {
					s.Header.Set("participants", out)
					changed = true
				}
			}
			if changed {
				writeJSONL(p, s.Header, s.Turns)
				nFiles++
			}
		}
	}
	for _, st := range loadAllStatements(root) {
		changed := false
		for _, a := range listOf(st.Meta.Get("attestations")) {
			d, ok := a.(*dyn.Dict)
			if ok && dyn.Equal(d.Get("by"), from) && through(d.Get("turn")) {
				d.Set("by", to)
				nAtts++
				changed = true
			}
		}
		if dyn.Equal(st.Meta.Get("originator"), from) && through(st.Meta.Get("proposed_in")) {
			st.Meta.Set("originator", to)
			changed = true
		}
		if changed {
			writeStatement(st.Path, st)
			nFiles++
		}
	}
	for _, st := range loadAllAgentStatements(root) {
		if dyn.Equal(st.Meta.Get("withdrawn_by"), from) && through(st.Meta.Get("withdrawn_in")) {
			st.Meta.Set("withdrawn_by", to)
			writeStatement(st.Path, st)
			nFiles++
		}
	}
	return nTurns, nAtts, nFiles
}

// refreshIndex rebuilds the index when there is one, so it does not go on showing what an override changed.
func refreshIndex(root string) {
	if dyn.IsFile(dbPath(root)) {
		buildIndex(root)
		println("index rebuilt")
	}
}

// ---------------------------------------------------------------- merge, reassign

// runOverrideMerge records every act by one key as by another that belongs to the same person. It is also
// how a key wrong from the start is put right, renaming a key being no override of its own (S-0148): the
// right key needs a row in the user table, not acts of its own.
func runOverrideMerge(ns argparse.Namespace, args []string) int {
	kind := ovMerge
	root := repoRoot()
	requirePin(root)
	if len(args) != 2 {
		die("kb override merge takes two keys: kb override merge <from> <into>")
	}
	from, to := dyn.Strip(args[0]), dyn.Strip(args[1])
	if from == "" || to == "" || from == to {
		die("the two ids must be different and not empty")
	}
	if msg := badKey(to); msg != "" {
		die(msg)
	}
	if moved, ref := retiredID(root, to); moved != "" {
		die(fmt.Sprintf("%s was moved to %s by override %s and is retired here", dyn.StrRepr(to), moved, ref))
	}
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)
	fromTurns, fromAtts := actsBy(root, from)
	toTurns, toAtts := actsBy(root, to)
	if fromTurns+fromAtts == 0 {
		die(fmt.Sprintf("nothing is recorded as by %s", dyn.StrRepr(from)))
	}
	users := mustLoadUsers(root)
	if !users.has(to) && toTurns+toAtts == 0 {
		die(fmt.Sprintf("%s is not in the user table and has no acts; add the right person first with kb user add, "+
			"choosing that key, then merge into it (%s)", dyn.StrRepr(to), rule("override-list")))
	}
	{
		var both []string
		for _, st := range loadAllStatements(root) {
			var a, b bool
			for _, x := range listOf(st.Meta.Get("attestations")) {
				if d, ok := x.(*dyn.Dict); ok {
					a = a || dyn.Equal(d.Get("by"), from)
					b = b || dyn.Equal(d.Get("by"), to)
				}
			}
			if a && b {
				both = append(both, dyn.Str(st.ID()))
			}
		}
		if len(both) > 0 {
			die(fmt.Sprintf("%s and %s have both attested %s; merged, one person would have attested each twice "+
				"(%s)", from, to, strings.Join(both, ", "), rule("attest-once")))
		}
	}
	verb := "merge"
	check := []string{"override", kind, from, to}
	if !ns.Bool("confirm") {
		plan := []string{
			fmt.Sprintf("%s %s to %s: %d human turn(s) and %d attestation(s) recorded as by %s would be recorded as by %s, "+
				"in every file that names them", verb, dyn.StrRepr(from), dyn.StrRepr(to), fromTurns, fromAtts, from, to),
			fmt.Sprintf("refuse any later act under %s, which would then be retired here", dyn.StrRepr(from)),
		}
		if toTurns+toAtts > 0 {
			plan = append(plan, fmt.Sprintf("say that %s and %s are one person: %s already has %d turn(s) and %d attestation(s)",
				from, to, to, toTurns, toAtts))
		} else {
			plan = append(plan, fmt.Sprintf("put right a key wrong from the start: %s has a row but no acts yet", to))
		}
		if users.has(from) {
			if users.has(to) {
				plan = append(plan, fmt.Sprintf("remove the user table's row for %s, since %s has one: %s", users.who(from), to, users.who(to)))
			} else {
				plan = append(plan, fmt.Sprintf("make the user table's row for %s the row of %s", users.who(from), to))
			}
		}
		return showCheck(root, check, plan)
	}
	turnArg, _ := ns.Str("turn")
	reason, _ := ns.Str("reason")
	auth, turn := overrideAuthority(root, user, turnArg, reason)
	requireCheck(root, check, turn)
	id := newOverrideID(root)
	nTurns, nAtts, nFiles := relabel(root, from, to, nil)
	appendOverride(root, id, kind, auth, dyn.D("from", from, "to", to, "turns", dyn.IntOf(nTurns), "attestations", dyn.IntOf(nAtts)))
	row := moveRow(root, from, to)
	clearCheck(root, check)
	println(fmt.Sprintf("override %s: %s %s to %s: %d turn(s) and %d attestation(s) in %d file(s)", id, verb, from, to, nTurns, nAtts, nFiles))
	if row != "" {
		println(row)
	}
	if from == user {
		println(fmt.Sprintf("act as %s from now on: change the id in ~/.kenttheclerk.json (or KB_USER, or git's user.email); "+
			"kb refuses acts under %s here", to, from))
	}
	refreshIndex(root)
	return 0
}

func runOverrideReassign(ns argparse.Namespace, args []string) int {
	root := repoRoot()
	requirePin(root)
	if len(args) != 2 {
		die("kb override reassign takes a turn and an id: kb override reassign <session>#<uuid> <id>")
	}
	ptr := parsePointer(args[0])
	to := dyn.Strip(args[1])
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)
	s := findSession(root, ptr.Session)
	if s == nil {
		die(fmt.Sprintf("no evidence session %s", ptr.Session))
	}
	t := s.Turn(ptr.UUID)
	if t == nil {
		die(fmt.Sprintf("no turn %s in session %s", ptr.UUID, dyn.Head(s.ID(), 8)))
	}
	from := dyn.Str(item(t, "author"))
	if !dyn.Equal(item(t, "role"), "user") || in(from, []string{"client", "tool", "document"}) {
		die(fmt.Sprintf("%s is not a human's turn; only a human's act is reassigned", ptr))
	}
	if to == "" || to == from || strings.HasPrefix(to, "agent:") || in(to, []string{"client", "tool", "document"}) {
		die(fmt.Sprintf("%s is not another person's id", dyn.StrRepr(to)))
	}
	if moved, ref := retiredID(root, to); moved != "" {
		die(fmt.Sprintf("%s was moved to %s by override %s and is retired here", dyn.StrRepr(to), moved, ref))
	}
	if !mustLoadUsers(root).has(to) {
		die(fmt.Sprintf("%s is not in the user table; an act is reassigned to someone in it, so add them first with kb "+
			"user add (%s)", dyn.StrRepr(to), rule("add-first")))
	}
	full := Pointer{s.ID(), dyn.Str(item(t, "uuid"))}
	var atts []string
	for _, st := range loadAllStatements(root) {
		for _, a := range listOf(st.Meta.Get("attestations")) {
			if d, ok := a.(*dyn.Dict); ok && dyn.Equal(d.Get("turn"), full.String()) && dyn.Equal(d.Get("by"), from) {
				for _, b := range listOf(st.Meta.Get("attestations")) {
					if bd, ok := b.(*dyn.Dict); ok && dyn.Equal(bd.Get("by"), to) {
						die(fmt.Sprintf("%s has already attested %s, which this turn attests (%s)", to, dyn.Str(st.ID()), rule("attest-once")))
					}
				}
				atts = append(atts, dyn.Str(st.ID()))
			}
		}
	}
	check := []string{"override", ovReassign, full.String(), to}
	if !ns.Bool("confirm") {
		plan := []string{fmt.Sprintf("record turn %s, now by %s, as by %s", full, dyn.StrRepr(from), dyn.StrRepr(to))}
		if len(atts) > 0 {
			plan = append(plan, fmt.Sprintf("and with it the attestation(s) made through that turn: %s", strings.Join(atts, ", ")))
		}
		return showCheck(root, check, plan)
	}
	turnArg, _ := ns.Str("turn")
	reason, _ := ns.Str("reason")
	auth, turn := overrideAuthority(root, user, turnArg, reason)
	requireCheck(root, check, turn)
	id := newOverrideID(root)
	_, nAtts, _ := relabel(root, from, to, &full)
	appendOverride(root, id, ovReassign, auth, dyn.D("target", full.String(), "from", from, "to", to, "attestations", dyn.IntOf(nAtts)))
	clearCheck(root, check)
	println(fmt.Sprintf("override %s: turn %s reassigned from %s to %s, with %d attestation(s)", id, full, from, to, nAtts))
	refreshIndex(root)
	return 0
}

// ---------------------------------------------------------------- scrub

// tombstone is the text that takes the place of scrubbed content, citing the override by its id, or by its
// number for one made before S-QYEK.
func tombstone(ref string, at, reason string) string {
	return fmt.Sprintf("[scrubbed on %s by override %s: %s]", dyn.Head(at, 10), ref, reason)
}

// scrubMark is the mark a scrubbed turn, session or statement carries, pointing to its log entry.
func scrubMark(ref string, at, reason string) *dyn.Dict {
	return dyn.D("override", ref, "at", at, "reason", reason)
}

// markRef is the override a mark points to, as overrideRef cites it: the "override" of a mark in
// evidence, or what a statement's "scrubbed" or "revised" holds (frontmatter keeps a statement's marks
// flat). A number from before S-QYEK comes back as text. "" when there is none.
func markRef(v any) string {
	if d, ok := v.(*dyn.Dict); ok {
		v = d.Get("override")
	}
	if !dyn.Truthy(v) {
		return ""
	}
	return dyn.Str(v)
}

func scrubbedTurn(t *dyn.Dict) bool { return dyn.Truthy(t.Get("scrubbed")) }

func scrubTurnIn(t *dyn.Dict, ref string, at, reason string) {
	t.Set("content", []any{dyn.D("type", "text", "text", tombstone(ref, at, reason))})
	t.Set("scrubbed", scrubMark(ref, at, reason))
}

func excerptOf(s string) string {
	s = normalizeWS(s)
	if dyn.Len(s) > 80 {
		return dyn.Head(s, 80) + "..."
	}
	return s
}

// inGit says whether the knowledge base's folder is in a git working tree.
func inGit(root string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func runOverrideScrub(ns argparse.Namespace, args []string) int {
	root := repoRoot()
	requirePin(root)
	if len(args) != 1 {
		die("kb override scrub takes one target: <session>#<uuid> for a turn, <session> for a whole session or " +
			"document, or S-xxxx / A-xxxx for a statement")
	}
	target := dyn.Strip(args[0])
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)

	var plan, remains []string
	var apply func(ref string, at, reason string) (kind, key string)

	switch {
	case statementIDRE.MatchString(strings.ToUpper(target)):
		sid := strings.ToUpper(target)
		p := statementPath(root, sid)
		if p == "" {
			die(fmt.Sprintf("statement %s not found", sid))
		}
		st := loadStatement(p)
		if dyn.Truthy(st.Meta.Get("scrubbed")) {
			die(fmt.Sprintf("%s is already scrubbed", sid))
		}
		plan = []string{fmt.Sprintf("replace the title, answers line and text of %s (%s) with a tombstone, keeping its id, "+
			"links and attestations", sid, excerptOf(dyn.Str(st.Meta.Get("title"))))}
		for _, k := range []string{"proposed_in", "modified_in", "made_in"} {
			if v := st.Meta.Get(k); dyn.Truthy(v) {
				remains = append(remains, fmt.Sprintf("the turn it was %s, %s: scrub it with kb override scrub %s",
					map[string]string{"proposed_in": "proposed in", "modified_in": "modified in", "made_in": "made in"}[k], dyn.Str(v), dyn.Str(v)))
			}
		}
		for _, a := range listOf(st.Meta.Get("attestations")) {
			if d, ok := a.(*dyn.Dict); ok && dyn.Equal(d.Get("form"), "self") {
				remains = append(remains, fmt.Sprintf("the turn it was attested in, %s", dyn.Str(d.Get("turn"))))
			}
		}
		apply = func(ref string, at, reason string) (string, string) {
			st.Meta.Set("title", fmt.Sprintf("Scrubbed by override %s", ref))
			if st.Meta.Has("answers") || strings.HasPrefix(sid, "S-") {
				st.Meta.Set("answers", fmt.Sprintf("scrubbed by override %s", ref))
			}
			st.Meta.Set("scrubbed", ref)
			st.Meta.Set("scrubbed_at", at)
			st.Meta.Set("scrubbed_reason", reason)
			st.Body = tombstone(ref, at, reason)
			dest := filepath.Join(filepath.Dir(p), sid+"-scrubbed.md")
			writeStatement(dest, st)
			if pathKey(dest) != pathKey(p) {
				if err := os.Remove(p); err != nil {
					dyn.RaiseOS(err)
				}
			}
			return ovScrubStatement, sid
		}

	case strings.Contains(target, "#"):
		ptr := parsePointer(target)
		s := findSession(root, ptr.Session)
		if s == nil || !dyn.Equal(s.ModeValue(), "on-record") {
			die(fmt.Sprintf("no on-record evidence session %s; off-record material is outside the record, and its "+
				"files may simply be deleted", ptr.Session))
		}
		t := s.Turn(ptr.UUID)
		if t == nil {
			die(fmt.Sprintf("no turn %s in session %s", ptr.UUID, dyn.Head(s.ID(), 8)))
		}
		if scrubbedTurn(t) {
			die(fmt.Sprintf("%s is already scrubbed", target))
		}
		full := Pointer{s.ID(), dyn.Str(item(t, "uuid"))}
		plan = []string{fmt.Sprintf("replace the content of %s %s by %s at %s (%s) with a tombstone, keeping the turn's "+
			"id, author and time", s.Kind(), full, dyn.Str(t.Get("author")), dyn.Str(t.Get("timestamp")), excerptOf(turnText(t)))}
		if s.Kind() == "document" {
			remains = append(remains, "the document's original bytes, which hold the whole document: to remove them, scrub "+
				"the whole document with kb override scrub "+dyn.Head(s.ID(), 8))
		}
		apply = func(ref string, at, reason string) (string, string) {
			scrubTurnIn(t, ref, at, reason)
			writeJSONL(s.Path, s.Header, s.Turns)
			return ovScrubTurn, full.String()
		}
		remains = append(remains, sourceRemains(s)...)

	default:
		s := sessionByPrefix(loadAllSessions(root), target)
		if s == nil || !dyn.Equal(s.ModeValue(), "on-record") {
			die(fmt.Sprintf("no on-record evidence session %s; off-record material is outside the record, and its "+
				"files may simply be deleted", dyn.StrRepr(target)))
		}
		if dyn.Truthy(s.Header.Get("scrubbed")) {
			die(fmt.Sprintf("%s %s is already scrubbed", s.Kind(), dyn.Head(s.ID(), 8)))
		}
		n := 0
		for _, t := range s.Turns {
			if !scrubbedTurn(t) {
				n++
			}
		}
		plan = []string{fmt.Sprintf("replace the content of all %d turn(s) of %s %s (%s) with tombstones, and its title, "+
			"keeping each turn's id, author and time", n, s.Kind(), s.ID(), excerptOf(dyn.Str(dyn.Or(s.Header.Get("title"), ""))))}
		var original string
		if s.Kind() == "document" {
			o := dyn.AsDict(dyn.Or(s.Header.Get("original"), dyn.NewDict()))
			original = filepath.Join(filepath.Dir(s.Path), dyn.Str(dyn.Or(o.Get("path"), "")))
			if sharedOriginal(root, s, original) {
				remains = append(remains, "the original bytes, which another projection still uses: scrub that one too")
				original = ""
			} else if dyn.IsFile(original) {
				plan = append(plan, "delete the document's original bytes, "+rel(original, root))
			}
		}
		remains = append(remains, sourceRemains(s)...)
		apply = func(ref string, at, reason string) (string, string) {
			for _, t := range s.Turns {
				if !scrubbedTurn(t) {
					scrubTurnIn(t, ref, at, reason)
				}
			}
			for _, k := range []string{"title", "caption"} {
				if s.Header.Has(k) {
					s.Header.Set(k, nil)
				}
			}
			s.Header.Set("scrubbed", scrubMark(ref, at, reason))
			writeJSONL(s.Path, s.Header, s.Turns)
			if original != "" && dyn.IsFile(original) {
				if err := os.Remove(original); err != nil {
					dyn.RaiseOS(err)
				}
			}
			return ovScrubSession, s.ID()
		}
	}
	if inGit(root) {
		remains = append(remains, "git history, and every clone of the knowledge base: removing it there means rewriting "+
			"history (for instance with git filter-repo) and having every clone replaced")
	}

	check := []string{"override", "scrub", target}
	if !ns.Bool("confirm") {
		for _, r := range remains {
			plan = append(plan, "and not reach: "+r)
		}
		return showCheck(root, check, plan)
	}
	turnArg, _ := ns.Str("turn")
	reason, _ := ns.Str("reason")
	auth, turn := overrideAuthority(root, user, turnArg, reason)
	requireCheck(root, check, turn)
	id := newOverrideID(root)
	at := isoSeconds(overrideNow())
	kind, key := apply(id, at, auth.reason)
	appendOverride(root, id, kind, auth, dyn.D("target", key))
	clearCheck(root, check)
	println(fmt.Sprintf("override %s: %s scrubbed", id, key))
	refreshIndex(root)
	if len(remains) > 0 {
		println(fmt.Sprintf("the content may remain outside what kb scrubs; tell the human, whose decision it is (%s):", rule("override-scrub")))
		for _, r := range remains {
			println("  " + r)
		}
	}
	return 0
}

// sourceRemains is where a scrubbed conversation's content still lives outside the knowledge base: the
// session file it was snapshotted from.
func sourceRemains(s *Session) []string {
	if src := dyn.Str(dyn.Or(s.Header.Get("source"), "")); src != "" && s.Kind() == "session" {
		return []string{"the session file it was snapshotted from, " + src}
	}
	return nil
}

// sharedOriginal says whether a projection other than s, not itself scrubbed, uses the same original bytes.
func sharedOriginal(root string, s *Session, original string) bool {
	for _, o := range loadAllSessions(root).Values() {
		if o.ID() == s.ID() || o.Kind() != "document" || dyn.Truthy(o.Header.Get("scrubbed")) {
			continue
		}
		od := dyn.AsDict(dyn.Or(o.Header.Get("original"), dyn.NewDict()))
		if pathKey(filepath.Join(filepath.Dir(o.Path), dyn.Str(dyn.Or(od.Get("path"), "")))) == pathKey(original) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- revise

// reliance is what relies on a statement, in the record or by being pushed (S-0136): each reason it can no
// longer be revised in place.
func reliance(root string, st *Statement) []string {
	id := dyn.Str(st.ID())
	var out []string
	if sb := st.Meta.Get("superseded_by"); dyn.Truthy(sb) {
		out = append(out, dyn.Str(sb)+" supersedes it")
	}
	for _, k := range []string{"amended_by", "clarified_by"} {
		for _, x := range listOf(st.Meta.Get(k)) {
			out = append(out, fmt.Sprintf("%s %s it", dyn.Str(x), markVerb[k]))
		}
	}
	mention := regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`)
	others := append(loadAllStatements(root), loadAllAgentStatements(root)...)
	for _, o := range others {
		oid := dyn.Str(o.ID())
		if oid == id {
			continue
		}
		for _, x := range listOf(o.Meta.Get("rests_on")) {
			if dyn.Str(x) == id {
				out = append(out, oid+" rests on it")
			}
		}
		for _, l := range contextLinks(o.Meta) {
			if l.target == id {
				out = append(out, oid+" links to it with context_for")
			}
		}
		if mention.MatchString(o.Body) || mention.MatchString(dyn.Str(dyn.Or(o.Meta.Get("title"), ""))) ||
			mention.MatchString(dyn.Str(dyn.Or(o.Meta.Get("answers"), ""))) {
			out = append(out, oid+" mentions it")
		}
	}
	atts := listOf(st.Meta.Get("attestations"))
	for _, a := range atts[min(1, len(atts)):] {
		if d, ok := a.(*dyn.Dict); ok {
			out = append(out, "it is endorsed by "+dyn.Str(d.Get("by")))
		}
	}
	if up := pushedTo(root, id); up != "" {
		out = append(out, "it has been pushed to "+up)
	}
	return dedupe(out)
}

var markVerb = map[string]string{"amended_by": "amends", "clarified_by": "clarifies"}

func dedupe(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range l {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// pushedTo is the upstream branch a statement has been pushed to, or "" when the knowledge base is not in
// git, has no upstream, or the upstream does not hold the statement.
func pushedTo(root, id string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	up := strings.TrimSpace(string(out))
	cmd = exec.Command("git", "ls-tree", "--name-only", up, "attested/")
	cmd.Dir = root
	out, err = cmd.Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(path.Base(strings.TrimSpace(l)), id+"-") {
			return up
		}
	}
	return ""
}

func runOverrideRevise(ns argparse.Namespace, args []string) int {
	root := repoRoot()
	requirePin(root)
	if len(args) != 2 {
		die("kb override revise takes the statement and the draft of its new text: kb override revise S-xxxx <draft> " +
			"--proposed <ptr> --turn <ptr> --reason <why>")
	}
	sid := strings.ToUpper(dyn.Strip(args[0]))
	if !humanIDRE.MatchString(sid) {
		die(fmt.Sprintf("%s is not a human statement id (S-xxxx); an agent revises its own choices with kb record (%s)", dyn.StrRepr(sid), rule("agent-revision")))
	}
	p := statementPath(root, sid)
	if p == "" {
		die(fmt.Sprintf("statement %s not found", sid))
	}
	st := loadStatement(p)
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)
	if dyn.Truthy(st.Meta.Get("scrubbed")) {
		die(fmt.Sprintf("%s is scrubbed", sid))
	}
	if authorOf(st) != user {
		die(fmt.Sprintf("%s was attested by %s; only the person who attested it revises it (%s)", sid, usersForDisplay(root).who(authorOf(st)), rule("override-revise")))
	}
	if reasons := reliance(root, st); len(reasons) > 0 {
		die(fmt.Sprintf("%s cannot be revised in place, because something relies on it: %s. Its author changes it by "+
			"superseding it instead (%s; %s)", sid, strings.Join(reasons, "; "), rule("override-revise"), rule("change-by-supersession")))
	}
	reason, _ := ns.Str("reason")
	if dyn.Strip(reason) == "" {
		die(fmt.Sprintf("--reason is required: why the statement is revised, for the log (%s)", rule("override-revise")))
	}
	form, _ := ns.Str("form")
	if !in(form, attestationForms) || form == "endorsement" {
		die("form must be one of " + dyn.Tuple("proposed-and-agreed", "agreed-with-modification", "self"))
	}
	turnArg, _ := ns.Str("turn")
	if turnArg == "" {
		die(fmt.Sprintf("--turn is required: the human's own turn agreeing to the revised text (%s)", rule("override-revise")))
	}
	agreePtr := parsePointer(turnArg)
	sess, agree := resolveOnRecord(root, agreePtr, "--turn")
	if !dyn.Equal(item(agree, "role"), "user") || !dyn.Equal(item(agree, "author"), user) {
		die(fmt.Sprintf("--turn %s is authored by %s, not the attesting user %s (%s)", agreePtr, dyn.Repr(item(agree, "author")), dyn.StrRepr(user), rule("own-turn")))
	}
	first := dyn.AsDict(listOf(st.Meta.Get("attestations"))[0])
	if _, old := resolvePointer(loadAllSessions(root), parsePointerValue(first.Get("turn"))); old != nil && timestampOr(agree) <= timestampOr(old) {
		die(fmt.Sprintf("the agreement to a revision comes after the attestation it revises (%s)", rule("after-proposal")))
	}

	draftArg := args[1]
	draft := loadStatement(draftPath(root, draftArg))
	meta, body := draft.Meta.Copy(), dyn.Strip(draft.Body)
	if body == "" {
		die("draft has an empty body")
	}
	if dyn.Truthy(meta.Get("id")) && !dyn.Equal(meta.Get("id"), sid) {
		die(fmt.Sprintf("draft carries the id %s; a revision keeps the id of the statement it revises", dyn.Str(meta.Get("id"))))
	}
	if !dyn.Truthy(meta.Get("title")) {
		die(fmt.Sprintf("draft needs a title (%s)", rule("title-and-answers")))
	}
	if dyn.Strip(dyn.Str(dyn.Or(meta.Get("answers"), ""))) == "" {
		die(fmt.Sprintf("draft needs an answers line (%s)", rule("title-and-answers")))
	}
	// what the statement changes and how it reads others stay as they were: changing them would change
	// marks on other statements, which is supersession's work
	for _, r := range relations {
		if v := meta.Get(r); dyn.Truthy(v) && !dyn.Equal(dyn.Str(v), dyn.Str(dyn.Or(st.Meta.Get(r), ""))) {
			die(fmt.Sprintf("%s: a revision keeps what the statement %s; to change that, supersede it (%s)", r, r, rule("override-revise")))
		}
	}
	if v := meta.Get("context_for"); dyn.Truthy(v) && contextKey(meta) != contextKey(st.Meta) {
		die(fmt.Sprintf("context_for: a revision keeps the statement's context_for links; to change them, supersede it (%s)", rule("override-revise")))
	}
	proposed, _ := ns.Str("proposed")
	originator, proposedIn, modifiedIn := agreedProposal(root, form, proposed, sess, agree, body, user)
	cites := dyn.Or(getOr(meta, "cites", []any{}), []any{})
	checkCites(root, cites)
	rests := restsOn(root, meta)
	for _, x := range rests {
		if dyn.Str(x) == sid {
			die("rests_on: a statement does not rest on itself")
		}
	}

	id := newOverrideID(root)
	ts := isoSeconds(now())
	record := dyn.D("by", user, "authentication", authentication, "at", ts, "turn", Pointer{sess.ID(), dyn.Str(item(agree, "uuid"))}.String(), "form", form)
	addStamp(record)
	ordered := dyn.D("id", sid, "title", meta.Get("title"), "answers", dyn.Strip(dyn.Str(meta.Get("answers"))),
		"originator", originator, "proposed_in", proposedIn)
	if modifiedIn != "" {
		ordered.Set("modified_in", modifiedIn)
	}
	ordered.Set("cites", cites)
	if len(rests) > 0 {
		ordered.Set("rests_on", rests)
	}
	for _, r := range relations {
		if v := st.Meta.Get(r); dyn.Truthy(v) {
			ordered.Set(r, v)
		}
	}
	if v := st.Meta.Get("context_for"); dyn.Truthy(v) {
		ordered.Set("context_for", v)
	}
	ordered.Set("topics", dyn.Or(getOr(meta, "topics", st.Meta.Get("topics")), []any{}))
	ordered.Set("attestations", []any{record})
	ordered.Set("revised", id)

	original := dyn.ReadText(p)
	dest := filepath.Join(root, "attested", sid+"-"+slugify(dyn.AsStr(meta.Get("title")), 60)+".md")
	dyn.WriteText(dest, dumpFrontmatter(ordered)+"\n"+body+"\n")
	if pathKey(dest) != pathKey(p) {
		if err := os.Remove(p); err != nil {
			dyn.RaiseOS(err)
		}
	}
	auth := overrideAuth{user, Pointer{sess.ID(), dyn.Str(item(agree, "uuid"))}.String(), dyn.Strip(reason)}
	appendOverride(root, id, ovRevise, auth, dyn.D("statement", sid, "proposed", proposedIn, "original", original,
		"body_sha256", sha256Tag(body)))
	if !ns.Bool("keep_draft") {
		deleteDraft(draft.Path)
	}
	println(fmt.Sprintf("override %s: %s revised in place (%s) by %s: %s; the log keeps its original text", id, sid, form, user, rel(dest, root)))
	refreshIndex(root)
	return 0
}

// contextKey is a statement's context_for links as one comparable string.
func contextKey(meta *dyn.Dict) string {
	var parts []string
	for _, l := range contextLinks(meta) {
		parts = append(parts, l.target+"\x00"+l.note)
	}
	return strings.Join(parts, "\x01")
}

// ---------------------------------------------------------------- a row of the user table

// runOverrideUser changes the name or email in a person's row of the user table (S-0141). Their key stays
// as it is: a key wrong from the start is merged into the right one (S-0148).
func runOverrideUser(ns argparse.Namespace, args []string) int {
	root := repoRoot()
	requirePin(root)
	if len(args) != 1 {
		die("kb override user takes one key: kb override user <key> --name <name> --email <email>")
	}
	key := dyn.Strip(args[0])
	users := mustLoadUsers(root)
	old, ok := users.get(key)
	if !ok {
		die(fmt.Sprintf("%s is not in the user table; a person is added with kb user add (%s)", dyn.StrRepr(key), rule("add-first")))
	}
	name, email := old.name, old.email
	if v, _ := ns.Str("name"); v != "" {
		name = normalizeWS(v)
	}
	if v, _ := ns.Str("email"); v != "" {
		email = strings.TrimSpace(v)
		if !emailRE.MatchString(email) {
			die(fmt.Sprintf("%s is not an email address", dyn.StrRepr(email)))
		}
	}
	if name == "" {
		die("a name cannot be empty")
	}
	if name == old.name && email == old.email {
		die(fmt.Sprintf("--name or --email must change the row: %s is %s <%s> now", key, old.name, old.email))
	}
	if p, ok := users.byEmail(email); ok && p.key != key {
		die(fmt.Sprintf("%s already has the email %s in the user table", users.who(p.key), email))
	}
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)
	check := []string{"override", ovUser, key, name, email}
	if !ns.Bool("confirm") {
		plan := []string{fmt.Sprintf("change the user table's row for %s from %s <%s> to %s <%s>, everywhere kb shows "+
			"them; the record keeps the key %s, and the override log keeps the old row", key, old.name, old.email, name, email, key)}
		return showCheck(root, check, plan)
	}
	turnArg, _ := ns.Str("turn")
	reason, _ := ns.Str("reason")
	auth, turn := overrideAuthority(root, user, turnArg, reason)
	requireCheck(root, check, turn)
	id := newOverrideID(root)
	appendOverride(root, id, ovUser, auth, dyn.D("key", key, "name", name, "email", email, "was_name", old.name, "was_email", old.email))
	var rows []person
	for _, p := range users.rows {
		if p.key == key {
			p.name, p.email = name, email
		}
		rows = append(rows, p)
	}
	writeUsers(root, rows)
	clearCheck(root, check)
	println(fmt.Sprintf("override %s: %s is now %s <%s>, was %s <%s>", id, key, name, email, old.name, old.email))
	refreshIndex(root)
	return 0
}

// ---------------------------------------------------------------- kb check

// overrideChecks verifies the override log and the marks overrides leave, for kb check. It returns the
// log's entries by how they are cited, their id or their number.
func overrideChecks(root string, sessions *Sessions, errorf, warnf func(string, ...any)) map[string]*dyn.Dict {
	entries, problem := loadOverrideLog(root)
	if problem != "" {
		errorf("override log: %s (%s)", problem, rule("write-through-kb"))
	}
	// the log is read in the order its entries were made; it has no chain or run of numbers to check, and
	// the hashes the earlier entries carry are left as they are (S-8MW2)
	byRef := map[string]*dyn.Dict{}
	lastRevise := map[string]string{}
	for i, e := range entries {
		n := overrideRef(e)
		switch {
		case e.Has("id") && !overrideIDRE.MatchString(entryStr(e, "id")):
			errorf("override log: the entry made at %s has the id %s, which is not an override's id (%s)", entryStr(e, "at"), dyn.Repr(e.Get("id")), rule("override-list"))
			continue
		case n == "":
			errorf("override log: the entry made at %s has no id (%s)", entryStr(e, "at"), rule("override-list"))
			continue
		case byRef[n] != nil:
			errorf("override log: two entries are override %s (%s)", n, rule("override-list"))
		}
		byRef[n] = e
		kind := entryStr(e, "kind")
		if kind == ovRepairID {
			// a repair by kb, not an override (S-W5AJ): no human turn, but traceable to what it did
			if !systemHashRE.MatchString(entryStr(e, "system_hash")) || entryStr(e, "system_version") == "" {
				errorf("override log: repair %s has a missing or malformed system stamp (%s)", n, rule("system-stamp"))
			}
			old, nw := entryStr(e, "old_id"), entryStr(e, "new_id")
			if !statementIDRE.MatchString(old) || !statementIDRE.MatchString(nw) || old[:1] != nw[:1] || entryStr(e, "at") == "" {
				errorf("override log: repair %s does not say which id it moved to which, or when", n)
			} else if p := statementPath(root, nw); p == "" || !dyn.Equal(loadStatement(p).Meta.Get("id"), nw) {
				errorf("override log: repair %s gave a statement the id %s, which no statement carries now", n, nw)
			}
			continue
		}
		if !in(kind, overrideKinds) {
			errorf("override log: override %s has kind %s, which is not on the list (%s)", n, dyn.Repr(e.Get("kind")), rule("override-list"))
			continue
		}
		for _, k := range []string{"at", "by", "turn", "reason"} {
			if entryStr(e, k) == "" {
				errorf("override log: override %s has no %s (%s)", n, k, rule("override-on-record"))
			}
		}
		if !systemHashRE.MatchString(entryStr(e, "system_hash")) || entryStr(e, "system_version") == "" {
			errorf("override log: override %s has a missing or malformed system stamp (%s)", n, rule("system-stamp"))
		}
		// the authorizing turn: a human's own, on the record, by whoever made the override as they are
		// recorded now
		if p, ok := tryParsePointer(entryStr(e, "turn")); ok {
			s := sessionByPrefix(sessions, p.Session)
			var t *dyn.Dict
			if s != nil {
				t = s.Turn(p.UUID)
			}
			switch {
			case t == nil:
				errorf("override log: override %s: turn %s does not resolve", n, entryStr(e, "turn"))
			case s.Kind() == "document" || !dyn.Equal(s.ModeValue(), "on-record"):
				errorf("override log: override %s: turn %s is not in an on-record conversation (%s)", n, entryStr(e, "turn"), rule("override-on-record"))
			case !dyn.Equal(t.Get("role"), "user") || !dyn.Equal(t.Get("author"), idAfter(entries, i, entryStr(e, "by"), entryStr(e, "turn"))):
				errorf("override log: override %s: turn %s is authored by %s, not %s (%s)", n, entryStr(e, "turn"), dyn.Repr(t.Get("author")), entryStr(e, "by"), rule("own-turn"))
			}
		}
		target := entryStr(e, "target")
		switch kind {
		case ovRename, ovMerge:
			if from := entryStr(e, "from"); from != "" {
				if nt, na := actsBy(root, from); nt+na > 0 {
					warnf("override log: override %s moved %s to %s, but %d turn(s) and %d attestation(s) are recorded as by %s again", n, from, entryStr(e, "to"), nt, na, from)
				}
			}
		case ovScrubTurn:
			p, ok := tryParsePointer(target)
			var t *dyn.Dict
			if ok {
				_, t = resolvePointer(sessions, p)
			}
			if t == nil || markRef(t.Get("scrubbed")) != n {
				errorf("override log: override %s scrubbed turn %s, which no longer carries its tombstone (%s)", n, target, rule("override-scrub"))
			}
		case ovScrubSession:
			s := sessionByPrefix(sessions, target)
			if s == nil || markRef(s.Header.Get("scrubbed")) != n {
				errorf("override log: override %s scrubbed session %s, which no longer carries its tombstone (%s)", n, dyn.Head(target, 8), rule("override-scrub"))
			}
		case ovScrubStatement:
			p := statementPath(root, target)
			if p == "" || markRef(loadStatement(p).Meta.Get("scrubbed")) != n {
				errorf("override log: override %s scrubbed %s, which no longer carries its tombstone (%s)", n, target, rule("override-scrub"))
			}
		case ovRevise:
			lastRevise[entryStr(e, "statement")] = n
		case ovUser:
			if entryStr(e, "key") == "" || entryStr(e, "name") == "" || entryStr(e, "email") == "" {
				errorf("override log: override %s changes a row of the user table, but does not say whose, or to what (%s)", n, rule("user-change"))
			}
		}
	}
	for sid, n := range lastRevise {
		p := statementPath(root, sid)
		if p == "" {
			errorf("override log: override %s revised %s, which does not exist", n, sid)
			continue
		}
		if st := loadStatement(p); markRef(st.Meta.Get("revised")) != n && !dyn.Truthy(st.Meta.Get("scrubbed")) {
			errorf("%s: last revised by override %s, but it does not say so (%s)", sid, n, rule("override-revise"))
		}
	}

	// tombstones in evidence, each pointing to its entry
	for _, s := range sessions.Values() {
		if sc := s.Header.Get("scrubbed"); dyn.Truthy(sc) {
			n := markRef(sc)
			if e := byRef[n]; e == nil || entryStr(e, "kind") != ovScrubSession || entryStr(e, "target") != s.ID() {
				errorf("%s %s: marked scrubbed by override %s, which the log does not hold (%s)", s.Kind(), dyn.Head(s.ID(), 8), n, rule("override-scrub"))
			}
		}
		for _, t := range s.Turns {
			sc := t.Get("scrubbed")
			if !dyn.Truthy(sc) {
				continue
			}
			m := dyn.AsDict(sc)
			n := markRef(m)
			ptr := Pointer{s.ID(), dyn.Str(t.Get("uuid"))}.String()
			e := byRef[n]
			if e == nil || !(entryStr(e, "kind") == ovScrubTurn && entryStr(e, "target") == ptr || entryStr(e, "kind") == ovScrubSession && entryStr(e, "target") == s.ID()) {
				errorf("turn %s: marked scrubbed by override %s, which the log does not hold (%s)", ptr, n, rule("override-scrub"))
			} else if turnText(t) != tombstone(n, dyn.Str(m.Get("at")), dyn.Str(m.Get("reason"))) {
				errorf("turn %s: scrubbed, but its content is not the tombstone (%s)", ptr, rule("override-scrub"))
			}
		}
	}
	return byRef
}

// statementOverrideChecks verifies a statement's marks from overrides, for kb check.
func statementOverrideChecks(st *Statement, byRef map[string]*dyn.Dict, errorf func(string, ...any)) {
	sid := dyn.Str(st.Meta.Get("id"))
	if sc := st.Meta.Get("scrubbed"); dyn.Truthy(sc) {
		n := markRef(sc)
		if e := byRef[n]; e == nil || entryStr(e, "kind") != ovScrubStatement || entryStr(e, "target") != sid {
			errorf("%s: marked scrubbed by override %s, which the log does not hold (%s)", sid, n, rule("override-scrub"))
		} else if st.Body != tombstone(n, dyn.Str(st.Meta.Get("scrubbed_at")), dyn.Str(st.Meta.Get("scrubbed_reason"))) {
			errorf("%s: scrubbed, but its text is not the tombstone (%s)", sid, rule("override-scrub"))
		}
	}
	if rv := st.Meta.Get("revised"); dyn.Truthy(rv) && !dyn.Truthy(st.Meta.Get("scrubbed")) {
		n := markRef(rv)
		if e := byRef[n]; e == nil || entryStr(e, "kind") != ovRevise || entryStr(e, "statement") != sid {
			errorf("%s: marked revised by override %s, which the log does not hold (%s)", sid, n, rule("override-revise"))
		} else if entryStr(e, "body_sha256") != sha256Tag(dyn.Strip(st.Body)) {
			errorf("%s: its text is not the text override %s revised it to; it was changed outside kb (%s)", sid, n, rule("write-through-kb"))
		}
	}
}

// ---------------------------------------------------------------- the command

func runOverride(ns argparse.Namespace) int {
	kind, _ := ns.Str("kind")
	args := ns.List("args")
	switch kind {
	case ovMerge:
		return runOverrideMerge(ns, args)
	case ovRename:
		die(fmt.Sprintf("renaming a key is no longer an override: a change of name or email is kb override user, and a key "+
			"wrong from the start is put right by adding the right person with kb user add and then kb override merge "+
			"<wrong> <right> (%s)", rule("override-list")))
	case ovReassign:
		return runOverrideReassign(ns, args)
	case "scrub":
		return runOverrideScrub(ns, args)
	case ovRevise:
		return runOverrideRevise(ns, args)
	case ovUser:
		return runOverrideUser(ns, args)
	}
	die(fmt.Sprintf("there is no override %s; the list is closed: merge, reassign, scrub, revise, user (%s)", dyn.StrRepr(kind), rule("override-list")))
	return 1
}

func addOverrideParser(sub *argparse.Parser) {
	p := sub.AddParser("override", "make one of the closed list of overrides, a last resort (kb rules overrides)")
	p.Positional("kind", argparse.Single, "merge | reassign | scrub | revise | user")
	p.Positional("args", argparse.ZeroMore, "merge <from> <into> | reassign <session>#<uuid> <id> | "+
		"scrub <session>#<uuid>|<session>|S-xxxx | revise S-xxxx <draft> | user <key> --name <name> --email <email>")
	p.Flag("--confirm", "make the override, after the human confirmed the check in a turn of their own")
	p.Option("--turn", "<session>#<uuid> of the human's on-record turn confirming it (for revise: agreeing to the new text)", nil, false)
	p.Option("--reason", "why the override is made, for the log", nil, false)
	p.Option("--proposed", "revise: <session>#<uuid> of the agent turn containing the new text", nil, false)
	p.Option("--form", "revise: proposed-and-agreed|agreed-with-modification|self", "proposed-and-agreed", false)
	p.Option("--name", "user: the person's name as it is to be", nil, false)
	p.Option("--email", "user: the person's email as it is to be", nil, false)
	p.Option("--user", "user id", nil, false)
	p.Flag("--keep-draft", "revise: do not delete the draft")
	p.Run = runOverride
}
