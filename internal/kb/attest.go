package kb

// kb attest — the only write path into attested/.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

func resolveOnRecord(root string, ptr Pointer, what string) (*Session, *py.Dict) {
	s := findSession(root, ptr.Session)
	if s == nil {
		die(fmt.Sprintf("%s: no evidence session %s", what, ptr.Session))
	}
	if !py.Equal(s.ModeValue(), "on-record") {
		die(fmt.Sprintf("%s: session %s is %s; attestation evidence must be on-record (%s)", what, py.Head(s.ID(), 8), s.Mode(), rule("on-record-only")))
	}
	if s.Kind() == "document" {
		die(fmt.Sprintf("%s: %s is a document; documents cannot attest. A document chunk may be cited as "+
			"background; the attesting turn must be a human's turn in an on-record conversation "+
			"(%s)", what, py.Head(s.ID(), 8), rule("documents-cannot-attest")))
	}
	t := s.Turn(ptr.UUID)
	if t == nil {
		die(fmt.Sprintf("%s: no turn %s in session %s", what, ptr.UUID, py.Head(s.ID(), 8)))
	}
	return s, t
}

// checkCites warns about background citations that do not resolve here.
func checkCites(root string, cites any) {
	if !py.Truthy(cites) {
		return
	}
	sessions := loadAllSessions(root)
	for _, c := range py.Iter(cites) {
		_, t := resolvePointer(sessions, parsePointerValue(c))
		if t == nil {
			warn(fmt.Sprintf("background citation %s does not resolve here (off-record or missing); kept as-is", py.Str(c)))
		}
	}
}

// restsOn is the string form of each entry of meta's rests_on (none when it is missing or empty), each
// checked to be an existing statement.
func restsOn(root string, meta *py.Dict) []any {
	var out []any
	for _, x := range py.Iter(py.Or(meta.Get("rests_on"), []any{})) {
		out = append(out, py.Str(x))
	}
	for _, x := range out {
		if !statementIDRE.MatchString(x.(string)) || statementPath(root, x) == "" {
			die(fmt.Sprintf("rests_on: %s is not an existing statement id", x))
		}
	}
	return out
}

func deleteDraft(path string) { _ = os.Remove(path) }

func runAttest(ns argparse.Namespace) int {
	root := repoRoot()
	requirePin(root)
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)
	form, _ := ns.Str("form")
	if !in(form, attestationForms) {
		die("form must be one of " + py.Tuple(attestationForms...))
	}
	turnArg, _ := ns.Str("turn")
	agreePtr := parsePointer(turnArg)
	sess, agree := resolveOnRecord(root, agreePtr, "--turn")
	if !py.Equal(item(agree, "role"), "user") || !py.Equal(item(agree, "author"), user) {
		die(fmt.Sprintf("--turn %s is authored by %s, not the attesting user %s (%s)", agreePtr, py.Repr(item(agree, "author")), py.StrRepr(user), rule("own-turn")))
	}

	ts := isoSeconds(now())
	record := py.D("by", user, "authentication", authentication, "at", ts, "turn", Pointer{sess.ID(), py.Str(item(agree, "uuid"))}.String(), "form", form)
	addStamp(record)

	if form == "endorsement" {
		statement, ok := ns.Str("statement")
		statement = strings.ToUpper(strings.TrimSpace(statement))
		if !ok || statement == "" {
			die("endorsement requires --statement S-xxxx")
		}
		if strings.HasPrefix(statement, "A-") {
			die(fmt.Sprintf("%s is an agent statement, which carries no attestations; to adopt it, attest a "+
				"human statement that supersedes it (%s)", statement, rule("agent-no-attestations")))
		}
		var hits []string
		if humanIDRE.MatchString(statement) {
			hits = py.Glob(filepath.Join(root, "attested"), statement+"-*.md")
		}
		if len(hits) != 1 {
			die(fmt.Sprintf("statement %s not found (or ambiguous)", statement))
		}
		st := loadStatement(hits[0])
		atts, present := st.Meta.Lookup("attestations")
		if !present {
			atts = []any{}
		}
		for _, a := range py.Iter(atts) {
			if py.Equal(py.AsDict(a).Get("by"), user) {
				die(fmt.Sprintf("%s has already attested %s (%s)", user, py.Str(st.ID()), rule("attest-once")))
			}
		}
		l, ok := st.Meta.SetDefault("attestations", []any{}).([]any)
		if !ok {
			py.Raise("AttributeError", "object has no attribute 'append'")
		}
		st.Meta.Set("attestations", append(l, record))
		writeStatement(hits[0], st)
		println(fmt.Sprintf("%s: endorsement by %s recorded", py.Str(st.ID()), user))
		return 0
	}

	draftArg, ok := ns.Str("draft")
	if !ok || draftArg == "" {
		die("proposed-and-agreed / self require a draft file")
	}
	draft := loadStatement(draftPath(root, draftArg))
	meta, body := draft.Meta.Copy(), py.Strip(draft.Body)
	if body == "" {
		die("draft has an empty body")
	}
	if py.Truthy(meta.Get("id")) {
		die(fmt.Sprintf("draft already carries an id; drafts are unnumbered until attested (%s)", rule("no-pending-statements")))
	}
	if !py.Truthy(meta.Get("title")) {
		die(fmt.Sprintf("draft needs a title (%s)", rule("title-and-answers")))
	}
	if py.Strip(py.Str(py.Or(meta.Get("answers"), ""))) == "" {
		die(fmt.Sprintf("draft needs an answers line: the question this statement answers and the role it plays in "+
			"answering (an example, a working position, a decision, a belief stated directly) "+
			"(%s)", rule("title-and-answers")))
	}

	proposed, _ := ns.Str("proposed")
	originator, proposedIn, modifiedIn := agreedProposal(root, form, proposed, sess, agree, body, user)
	meta.Set("originator", originator)
	meta.Set("proposed_in", proposedIn)
	if modifiedIn != "" {
		meta.Set("modified_in", modifiedIn)
	}

	// background citations: allowed to point anywhere, incl. through fork ancestry (S-0034)
	cites := py.Or(getOr(meta, "cites", []any{}), []any{})
	checkCites(root, cites)

	// relations to existing statements
	rels := py.NewDict()
	for _, r := range relations {
		if v := meta.Get(r); py.Truthy(v) {
			rels.Set(r, v)
		}
	}
	for _, r := range rels.Keys() {
		target := rels.Get(r)
		if statementPath(root, target) == "" {
			die(fmt.Sprintf("%s: %s does not exist", r, py.Str(target)))
		}
		if strings.HasPrefix(py.Str(target), "A-") && r != "supersedes" {
			die(fmt.Sprintf("%s: a human statement relates to an agent statement only by superseding it "+
				"(overriding or adopting it; %s)", r, rule("agent-no-attestations")))
		}
	}
	// only a statement's author changes it; anyone else links to it with context_for
	requireAuthor(root, user, rels)
	context := contextFor(root, "S", meta, rels)
	rests := restsOn(root, meta)

	sid := makeStatementID(root, "S")
	ordered := py.D("id", sid, "title", meta.Get("title"), "answers", py.Strip(py.Str(meta.Get("answers"))),
		"originator", meta.Get("originator"), "proposed_in", meta.Get("proposed_in"))
	if v := meta.Get("modified_in"); py.Truthy(v) {
		ordered.Set("modified_in", v)
	}
	ordered.Set("cites", py.Or(getOr(meta, "cites", []any{}), []any{}))
	if len(rests) > 0 {
		ordered.Set("rests_on", rests)
	}
	for _, r := range rels.Keys() {
		ordered.Set(r, rels.Get(r))
	}
	if len(context) > 0 {
		ordered.Set("context_for", context)
	}
	ordered.Set("topics", py.Or(getOr(meta, "topics", []any{}), []any{}))
	ordered.Set("attestations", []any{record})
	dest := filepath.Join(root, "attested", sid+"-"+slugify(py.AsStr(meta.Get("title")), 60)+".md")
	py.WriteText(dest, dumpFrontmatter(ordered)+"\n"+body+"\n")
	if !ns.Bool("keep_draft") {
		deleteDraft(draft.Path)
	}
	println(fmt.Sprintf("attested %s (%s) by %s: %s", sid, form, user, rel(dest, root)))
	for _, r := range rels.Keys() {
		markRelation(root, rels.Get(r), r, sid)
	}
	markContext(root, context, sid)
	return 0
}

// agreedProposal checks what the human agreed to, for every form but endorsement: the proposal turn and the
// text in it, or the human's own turn for a self-attestation. It returns the statement's originator,
// proposed_in, and modified_in ("" unless agreed with modification).
func agreedProposal(root, form, proposed string, sess *Session, agree *py.Dict, body, user string) (any, string, string) {
	if form == "self" {
		if !textContained(body, turnText(agree)) {
			die(fmt.Sprintf("self-attesting statement text must be contained verbatim in the human's turn (%s)", rule("exact-text")))
		}
		return user, Pointer{sess.ID(), py.Str(item(agree, "uuid"))}.String(), ""
	}
	if proposed == "" {
		die(form + " requires --proposed <session>#<uuid> (the agent turn containing the proposal)")
	}
	propPtr := parsePointer(proposed)
	psess, prop := resolveOnRecord(root, propPtr, "--proposed")
	if psess.ID() != sess.ID() {
		die(fmt.Sprintf("proposal and agreement must be in the same session (%s)", rule("same-session")))
	}
	contained := textContained(body, turnText(prop))
	if form == "proposed-and-agreed" && !contained {
		die(fmt.Sprintf("statement text is not contained verbatim in the proposal turn (whitespace-insensitive); "+
			"use --form agreed-with-modification if the human agreed subject to a change "+
			"(%s)", rule("exact-text")))
	}
	if form == "agreed-with-modification" && contained {
		warn("text is contained verbatim in the proposal; proposed-and-agreed would be the stronger form")
	}
	pts, ats := timestampOr(prop), timestampOr(agree)
	if pts >= ats {
		die(fmt.Sprintf("agreement turn must come after the proposal turn (%s)", rule("after-proposal")))
	}
	modified := ""
	if form == "agreed-with-modification" {
		modified = Pointer{sess.ID(), py.Str(item(agree, "uuid"))}.String()
	}
	return item(prop, "author"), Pointer{psess.ID(), py.Str(item(prop, "uuid"))}.String(), modified
}

// getOr is d's value for k, or def when d has no k.
func getOr(d *py.Dict, k string, def any) any {
	if v, ok := d.Lookup(k); ok {
		return v
	}
	return def
}

// timestampOr is the turn's timestamp ("" when it has none), which must be a string to be compared.
func timestampOr(t *py.Dict) string {
	v, ok := t.Lookup("timestamp")
	if !ok {
		return ""
	}
	s, isStr := v.(string)
	if !isStr {
		py.Raise("TypeError", "'>=' not supported between instances of '%s' and 'str'", py.TypeName(v))
	}
	return s
}

func addAttestParser(sub *argparse.Parser) {
	p := sub.AddParser("attest", "record an attestation (the only write path into attested/)")
	p.Positional("draft", argparse.Optional, "draft statement file (not for endorsement)")
	p.Option("--form", strings.Join(attestationForms, "|"), "proposed-and-agreed", false)
	p.Option("--turn", "<session>#<uuid> of the human's agreeing turn", nil, true)
	p.Option("--proposed", "<session>#<uuid> of the agent turn containing the exact text", nil, false)
	p.Option("--statement", "S-xxxx to endorse (endorsement form)", nil, false)
	p.Option("--user", "attesting user id", nil, false)
	p.Flag("--keep-draft", "do not delete the draft after attesting")
	p.Run = runAttest
}
