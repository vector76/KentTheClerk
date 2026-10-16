package kb

// kb record / seen / withdraw — the only write path into agent/.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

func onRecordTurn(root, ptrStr, what string) (*Session, *py.Dict) {
	ptr := parsePointer(ptrStr)
	s := findSession(root, ptr.Session)
	if s == nil {
		die(fmt.Sprintf("%s: no evidence session %s", what, ptr.Session))
	}
	if !py.Equal(s.ModeValue(), "on-record") {
		die(fmt.Sprintf("%s: session %s is %s; an agent statement must come from an on-record "+
			"session. Propose going on the record first (%s)", what, py.Head(s.ID(), 8), s.Mode(), rule("on-record-only")))
	}
	if s.Kind() == "document" {
		die(fmt.Sprintf("%s: %s is a document; agent statements come from conversations "+
			"(%s)", what, py.Head(s.ID(), 8), rule("agent-from-conversation")))
	}
	t := s.Turn(ptr.UUID)
	if t == nil {
		die(fmt.Sprintf("%s: no turn %s in session %s", what, ptr.UUID, py.Head(s.ID(), 8)))
	}
	return s, t
}

func loadAgent(root, aid string) (string, *Statement) {
	aid = strings.ToUpper(strings.TrimSpace(aid))
	if !strings.HasPrefix(aid, "A-") {
		die(aid + " is not an agent statement id (A-xxxx)")
	}
	p := statementPath(root, aid)
	if p == "" {
		die(fmt.Sprintf("agent statement %s not found", aid))
	}
	return p, loadStatement(p)
}

func runRecord(ns argparse.Namespace) int {
	root := repoRoot()
	requirePin(root)
	turnArg, _ := ns.Str("turn")
	sess, turn := onRecordTurn(root, turnArg, "--turn")
	if !py.Equal(item(turn, "role"), "assistant") || !strings.HasPrefix(py.Str(item(turn, "author")), "agent:") {
		die(fmt.Sprintf("--turn %s is authored by %s; an agent statement is made in an agent's "+
			"turn (a human's own words go through kb attest) (%s)", turnArg, py.Repr(item(turn, "author")), rule("agent-own-turn")))
	}
	draftArg, _ := ns.Str("draft")
	draft := loadStatement(draftPath(root, draftArg))
	meta, body := draft.Meta.Copy(), py.Strip(draft.Body)
	if body == "" {
		die("draft has an empty body")
	}
	if py.Truthy(meta.Get("id")) {
		die(fmt.Sprintf("draft already carries an id; drafts are unnumbered until recorded (%s)", rule("no-pending-statements")))
	}
	if !py.Truthy(meta.Get("title")) {
		die(fmt.Sprintf("draft needs a title (%s)", rule("title-and-answers")))
	}
	if py.Strip(py.Str(py.Or(meta.Get("answers"), ""))) == "" {
		die(fmt.Sprintf("draft needs an answers line: the question this choice answers and the role it plays "+
			"(%s)", rule("title-and-answers")))
	}

	rels := py.NewDict()
	for _, r := range relations {
		if v := meta.Get(r); py.Truthy(v) {
			rels.Set(r, v)
		}
	}
	for _, r := range rels.Keys() {
		target := rels.Get(r)
		if !strings.HasPrefix(py.Str(target), "A-") {
			die(fmt.Sprintf("%s: %s is a human statement; an agent statement may revise only agent statements. "+
				"Changing what a human said is the human's to do (%s)", r, py.Str(target), rule("agent-revision")))
		}
		tp := statementPath(root, target)
		if tp == "" {
			die(fmt.Sprintf("%s: %s does not exist", r, py.Str(target)))
		}
		old := loadStatement(tp)
		if py.Truthy(old.Meta.Get("superseded_by")) || py.Truthy(old.Meta.Get("withdrawn_in")) {
			die(fmt.Sprintf("%s: %s is no longer in force (%s)", r, py.Str(target), rule("agent-revision")))
		}
	}
	context := contextFor(root, "A", meta, rels)
	rests := restsOn(root, meta)

	cites := py.Or(getOr(meta, "cites", []any{}), []any{})
	checkCites(root, cites)

	ts := isoSeconds(now())
	madeIn := Pointer{sess.ID(), py.Str(item(turn, "uuid"))}.String()
	aid := makeStatementID(root, "A")
	ordered := py.D("id", aid, "title", meta.Get("title"), "answers", py.Strip(py.Str(meta.Get("answers"))),
		"author", item(turn, "author"), "made_in", madeIn, "recorded_at", ts)
	addStamp(ordered)
	if len(rests) > 0 {
		ordered.Set("rests_on", rests)
	}
	ordered.Set("cites", cites)
	for _, r := range rels.Keys() {
		ordered.Set(r, rels.Get(r))
	}
	if len(context) > 0 {
		ordered.Set("context_for", context)
	}
	ordered.Set("topics", py.Or(getOr(meta, "topics", []any{}), []any{}))
	shown := textContained(body, turnText(turn))
	if shown {
		ordered.Set("seen", []any{py.D("turn", madeIn, "at", ts)})
	} else {
		ordered.Set("seen", []any{})
	}
	if err := os.Mkdir(filepath.Join(root, "agent"), 0o777); err != nil && !os.IsExist(err) {
		py.RaiseOS(err)
	}
	dest := filepath.Join(root, "agent", aid+"-"+slugify(py.AsStr(meta.Get("title")), 60)+".md")
	py.WriteText(dest, dumpFrontmatter(ordered)+"\n"+body+"\n")
	if !ns.Bool("keep_draft") {
		deleteDraft(draft.Path)
	}
	how := " (not yet seen)"
	if shown {
		how = " (seen in the turn it was made)"
	}
	println(fmt.Sprintf("recorded %s by %s%s: %s", aid, py.Str(item(turn, "author")), how, rel(dest, root)))
	for _, r := range rels.Keys() {
		markRelation(root, rels.Get(r), r, aid)
	}
	markContext(root, context, aid)
	return 0
}

func runSeen(ns argparse.Namespace) int {
	root := repoRoot()
	requirePin(root)
	statement, _ := ns.Str("statement")
	p, st := loadAgent(root, statement)
	turnArg, _ := ns.Str("turn")
	sess, turn := onRecordTurn(root, turnArg, "--turn")
	if !textContained(st.Body, turnText(turn)) {
		die(fmt.Sprintf("the full text of %s does not appear in %s; seen means shown in full (%s)", py.Str(st.ID()), turnArg, rule("seen-in-full")))
	}
	ptr := Pointer{sess.ID(), py.Str(item(turn, "uuid"))}.String()
	seenV := py.Or(st.Meta.Get("seen"), []any{})
	seen, ok := seenV.([]any)
	if !ok {
		py.Raise("AttributeError", "'%s' object has no attribute 'append'", py.TypeName(seenV))
	}
	for _, x := range seen {
		if py.Equal(py.AsDict(x).Get("turn"), ptr) {
			die(fmt.Sprintf("%s is already recorded as seen in %s", py.Str(st.ID()), ptr))
		}
	}
	seen = append(seen, py.D("turn", ptr, "at", isoSeconds(now())))
	st.Meta.Set("seen", seen)
	writeStatement(p, st)
	println(fmt.Sprintf("%s: seen in %s", py.Str(st.ID()), ptr))
	return 0
}

func runWithdraw(ns argparse.Namespace) int {
	root := repoRoot()
	requirePin(root)
	statement, _ := ns.Str("statement")
	p, st := loadAgent(root, statement)
	if py.Truthy(st.Meta.Get("withdrawn_in")) || py.Truthy(st.Meta.Get("superseded_by")) {
		die(fmt.Sprintf("%s is no longer in force (%s)", py.Str(st.ID()), rule("agent-revision")))
	}
	turnArg, _ := ns.Str("turn")
	sess, turn := onRecordTurn(root, turnArg, "--turn")
	reason, _ := ns.Str("reason")
	if py.Strip(reason) == "" {
		die(fmt.Sprintf("--reason is required (%s)", rule("agent-revision")))
	}
	st.Meta.Set("withdrawn_in", Pointer{sess.ID(), py.Str(item(turn, "uuid"))}.String())
	st.Meta.Set("withdrawn_by", item(turn, "author"))
	st.Meta.Set("withdrawn_at", isoSeconds(now()))
	st.Meta.Set("withdrawn_reason", py.Strip(reason))
	addStamp(st.Meta, "withdrawn_")
	writeStatement(p, st)
	println(fmt.Sprintf("%s: withdrawn by %s", py.Str(st.ID()), py.Str(item(turn, "author"))))
	return 0
}

func addRecordParsers(sub *argparse.Parser) {
	p := sub.AddParser("record", "record an agent statement (the only write path into agent/)")
	p.Positional("draft", argparse.Single, "draft statement file")
	p.Option("--turn", "<session>#<uuid> of the agent's turn where the choice was made", nil, true)
	p.Flag("--keep-draft", "do not delete the draft after recording")
	p.Run = runRecord
	s := sub.AddParser("seen", "record that an agent statement was shown in full in an on-record turn")
	s.Positional("statement", argparse.Single, "A-xxxx")
	s.Option("--turn", "<session>#<uuid> of the turn that showed it", nil, true)
	s.Run = runSeen
	w := sub.AddParser("withdraw", "retire an agent statement with nothing in its place")
	w.Positional("statement", argparse.Single, "A-xxxx")
	w.Option("--turn", "<session>#<uuid> of the on-record turn that withdraws it", nil, true)
	w.Option("--reason", "", nil, true)
	w.Run = runWithdraw
}
