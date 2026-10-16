package kb

// kb check — mechanical invariants over evidence/, attested/ and agent/.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
	"kenttheclerk/internal/pin"
)

var (
	humanIDRE = regexp.MustCompile(`^S-` + idBody + `\n?$`)
	agentIDRE = regexp.MustCompile(`^A-` + idBody + `\n?$`)
	// digitsRE is an id given before S-0149, when ids were sequential numbers
	digitsRE = regexp.MustCompile(`^[SA]-\p{Nd}{4,}\n?$`)
)

// pathKey identifies a resolved path for comparison, as Path equality does (case-insensitive on Windows).
func pathKey(p string) string {
	p = dyn.Resolve(p)
	if dyn.Windows {
		return dyn.Lower(p)
	}
	return p
}

func listOf(v any) []any {
	if !dyn.Truthy(v) {
		return nil
	}
	return dyn.Iter(v)
}

func runCheck(ns argparse.Namespace) int {
	root := repoRoot()
	var errors, warnings []string
	errorf := func(f string, a ...any) { errors = append(errors, fmt.Sprintf(f, a...)) }
	warnf := func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) }
	sessions := loadAllSessions(root)

	// the pin (S-0115): unreadable is an error; another version's is a warning, since this kb would
	// refuse to write here but the record itself is not at fault
	if p, ok, err := pin.Read(root); err != nil {
		errorf("the knowledge base's pin cannot be read: %v (%s)", err, rule("pinned-version"))
	} else if ok && !p.Matches(kent.Version(), kent.Hash()) {
		warnf("this knowledge base is pinned to %s; this kb is %s and would refuse to write here (%s)",
			p, thisBuild(), rule("pinned-version"))
	}

	// the override log, and the tombstones overrides leave in evidence (S-0132)
	overrides := overrideChecks(root, sessions, errorf, warnf)
	usersChecks(root, sessions, errorf, warnf)

	// resolve is same-session resolution by default; background citations may resolve through the
	// fork's ancestry chain (S-0034); attestation and proposal turns may not (S-0032).
	resolve := func(ptrStr any, throughAncestry bool) (*Session, *dyn.Dict) {
		p, ok := tryParsePointer(strArg(ptrStr))
		if !ok {
			return nil, nil
		}
		if throughAncestry {
			return resolvePointer(sessions, p)
		}
		s := sessionByPrefix(sessions, p.Session)
		if s == nil {
			return nil, nil
		}
		return s, s.Turn(p.UUID)
	}

	// fork ancestry
	for _, s := range sessions.Values() {
		bp := s.BranchPoint()
		if !dyn.Truthy(bp) {
			continue
		}
		chain := ancestry(sessions, s)
		if len(chain) == 0 {
			warnf("session %s: forked from turn %s, which is in no snapshot "+
				"(parent not snapshotted, or its snapshot predates the branch point)", dyn.Head(s.ID(), 8), dyn.Head(dyn.AsStr(bp), 8))
		} else if dyn.Equal(s.ModeValue(), "on-record") {
			for _, a := range chain {
				if !dyn.Equal(a.s.ModeValue(), "on-record") {
					errorf("session %s is on-record but its ancestor %s is %s (%s)", dyn.Head(s.ID(), 8), dyn.Head(a.s.ID(), 8), a.s.Mode(), rule("fork-ancestry"))
				}
			}
		}
	}

	// documents (S-0038, S-0039)
	referenced := map[string]bool{}
	for _, s := range sessions.Values() {
		if s.Kind() != "document" || dyn.Truthy(s.Header.Get("scrubbed")) {
			continue
		}
		o := dyn.AsDict(dyn.Or(s.Header.Get("original"), dyn.NewDict()))
		h := dyn.Str(dyn.Or(o.Get("hash"), ""))
		if !originalHashRE.MatchString(h) {
			errorf("document %s: header has a missing or malformed original hash", dyn.Head(s.ID(), 8))
			continue
		}
		hexdigest := strings.SplitN(h, ":", 2)[1]
		relp := dyn.Or(o.Get("path"), "")
		relS := dyn.AsStr(relp)
		var opath string
		if dyn.Equal(s.ModeValue(), "on-record") {
			opath = filepath.Join(filepath.Dir(s.Path), relS)
		} else {
			opath = filepath.Join(root, "off-record", relS)
		}
		m := originalNameRE.FindStringSubmatch(dyn.Name(opath))
		if m == nil || m[1] != hexdigest {
			errorf("document %s: original path %s is not named by its hash (%s)", dyn.Head(s.ID(), 8), dyn.StrRepr(relS), rule("frozen-projection"))
		}
		if !dyn.IsFile(opath) {
			errorf("document %s: original %s is missing", dyn.Head(s.ID(), 8), relS)
		} else {
			referenced[pathKey(opath)] = true
			actual := sha256File(opath)
			if actual != hexdigest {
				errorf("document %s: original %s hashes to %s…, not its name; "+
					"the bytes were damaged or modified (%s)", dyn.Head(s.ID(), 8), relS, actual[:12], rule("frozen-projection"))
			}
		}
		for _, t := range s.Turns {
			if !dyn.Equal(t.Get("role"), "document") || !dyn.Equal(t.Get("author"), "document") {
				u := getOr(t, "uuid", "?")
				errorf("document %s: chunk %s is not role/author 'document'", dyn.Head(s.ID(), 8), dyn.Head(dyn.AsStr(u), 8))
			}
		}
		if !dyn.Truthy(s.Header.Get("extractor")) {
			errorf("document %s: header has no extractor", dyn.Head(s.ID(), 8))
		}
	}
	for _, mode := range []string{"on-record", "off-record"} {
		d := originalsDir(root, mode)
		if dyn.IsDir(d) {
			for _, e := range dyn.Scandir(d) {
				p := filepath.Join(d, e.Name())
				if dyn.IsFile(p) && !referenced[pathKey(p)] {
					warnf("original %s is referenced by no projection", rel(p, root))
				}
			}
		}
	}

	stmts := loadAllStatements(root)
	ids := map[string]string{}
	var nums []int
	var unstamped, noAnswers []string
	for _, st := range stmts {
		name := filepath.Base(st.Path)
		sidV := st.Meta.Get("id")
		sid, isStr := sidV.(string)
		if !dyn.Truthy(sidV) || !isStr || !humanIDRE.MatchString(sid) {
			if dyn.Truthy(sidV) && !isStr {
				dyn.Raise("TypeError", "expected a string, got %s", dyn.TypeName(sidV))
			}
			errorf("%s: missing or malformed id", name)
			continue
		}
		if !strings.HasPrefix(name, sid+"-") {
			errorf("%s: filename does not start with its id %s", name, sid)
		}
		if prev, ok := ids[sid]; ok {
			errorf("duplicate id %s: %s and %s", sid, name, prev)
		}
		ids[sid] = name
		// only the sequential ids given before S-0149 can leave a gap; a random id has a letter
		if digitsRE.MatchString(sid) {
			n, _ := dyn.ParseInt(sid[2:])
			nums = append(nums, int(n.Int64()))
		}
		statementOverrideChecks(st, overrides, errorf)
		scrubbed := dyn.Truthy(st.Meta.Get("scrubbed"))
		if !dyn.Truthy(st.Meta.Get("title")) {
			errorf("%s: missing title (%s)", sid, rule("title-and-answers"))
		}
		if dyn.Strip(st.Body) == "" {
			errorf("%s: empty body", sid)
		}
		if dyn.Strip(dyn.Str(dyn.Or(st.Meta.Get("answers"), ""))) == "" {
			var versions []any
			for _, a := range listOf(st.Meta.Get("attestations")) {
				if v := dyn.AsDict(a).Get("system_version"); dyn.Truthy(v) {
					versions = append(versions, v)
				}
			}
			if len(versions) > 0 && tupleGE(versionTuple(versions[0]), []int64{0, 3, 0}) {
				errorf("%s: no answers line (what question it answers and in what role) (%s)", sid, rule("title-and-answers"))
			} else {
				noAnswers = append(noAnswers, sid)
			}
		}

		// proposal / verified copy
		prop := st.Meta.Get("proposed_in")
		if !dyn.Truthy(prop) {
			errorf("%s: missing proposed_in", sid)
		} else {
			s, t := resolve(prop, false)
			switch {
			case t == nil:
				errorf("%s: proposed_in %s does not resolve", sid, dyn.Str(prop))
			case s.Kind() == "document":
				errorf("%s: proposed_in %s is a document chunk; documents cannot attest (%s)", sid, dyn.Str(prop), rule("documents-cannot-attest"))
			default:
				if !dyn.Equal(s.ModeValue(), "on-record") {
					errorf("%s: proposed_in %s is in a %s session (%s)", sid, dyn.Str(prop), s.Mode(), rule("on-record-only"))
				}
				modified := false
				for _, a := range listOf(st.Meta.Get("attestations")) {
					if dyn.Equal(dyn.AsDict(a).Get("form"), "agreed-with-modification") {
						modified = true
					}
				}
				if modified {
					if !dyn.Truthy(st.Meta.Get("modified_in")) {
						errorf("%s: agreed-with-modification but no modified_in turn", sid)
					} else {
						_, mt := resolve(st.Meta.Get("modified_in"), false)
						if mt == nil || !dyn.Equal(item(mt, "role"), "user") {
							errorf("%s: modified_in %s does not resolve to a human turn", sid, dyn.Str(st.Meta.Get("modified_in")))
						}
					}
				} else if scrubbedTurn(t) {
					if !scrubbed {
						warnf("%s: the turn it was proposed in, %s, was scrubbed; review whether it should stand (%s)", sid, dyn.Str(prop), rule("override-scrub"))
					}
				} else if !scrubbed && !textContained(st.Body, turnText(t)) {
					errorf("%s: body text is not contained verbatim in proposal turn %s (%s)", sid, dyn.Str(prop), rule("exact-text"))
				}
				if !dyn.Equal(st.Meta.Get("originator"), item(t, "author")) {
					errorf("%s: originator %s != proposal turn author %s", sid, dyn.Repr(st.Meta.Get("originator")), dyn.Repr(item(t, "author")))
				}
			}
		}

		atts := listOf(st.Meta.Get("attestations"))
		if len(atts) == 0 {
			warnf("%s: no attestations", sid)
		}
		var seenBy []any
		for _, av := range atts {
			a := dyn.AsDict(av)
			by, turn, form := a.Get("by"), a.Get("turn"), a.Get("form")
			if fs, ok := form.(string); !ok || !in(fs, attestationForms) {
				errorf("%s: attestation form %s invalid", sid, dyn.Repr(form))
			}
			for _, x := range seenBy {
				if dyn.Equal(x, by) {
					errorf("%s: duplicate attestation by %s (%s)", sid, dyn.Str(by), rule("attest-once"))
					break
				}
			}
			seenBy = append(seenBy, by)
			if !dyn.Truthy(turn) {
				errorf("%s: attestation by %s has no turn", sid, dyn.Str(by))
				continue
			}
			s, t := resolve(turn, false)
			if t == nil {
				errorf("%s: attestation turn %s does not resolve", sid, dyn.Str(turn))
				continue
			}
			if s.Kind() == "document" {
				errorf("%s: attestation turn %s is a document chunk; documents cannot attest (%s)", sid, dyn.Str(turn), rule("documents-cannot-attest"))
				continue
			}
			if !dyn.Equal(s.ModeValue(), "on-record") {
				errorf("%s: attestation turn %s is in a %s session (must be on-record) (%s)", sid, dyn.Str(turn), s.Mode(), rule("on-record-only"))
			}
			if !dyn.Equal(item(t, "role"), "user") || !dyn.Equal(item(t, "author"), by) {
				errorf("%s: attestation turn %s is authored by %s, not %s (%s)", sid, dyn.Str(turn), dyn.Repr(item(t, "author")), dyn.Repr(by), rule("own-turn"))
			}
			if dyn.Equal(form, "self") && !scrubbed && !scrubbedTurn(t) && !textContained(st.Body, turnText(t)) {
				errorf("%s: self-attestation but body not contained in %s (%s)", sid, dyn.Str(turn), rule("exact-text"))
			}
			h, v := a.Get("system_hash"), a.Get("system_version")
			if !dyn.Truthy(h) && !dyn.Truthy(v) {
				unstamped = append(unstamped, sid+"/"+dyn.Str(by))
			} else {
				if !dyn.Truthy(v) {
					errorf("%s: attestation by %s has system_hash but no system_version (%s)", sid, dyn.Str(by), rule("system-stamp"))
				}
				if !dyn.Truthy(h) || !systemHashRE.MatchString(dyn.Str(h)) {
					errorf("%s: attestation by %s has a missing or malformed system_hash (%s)", sid, dyn.Str(by), rule("system-stamp"))
				}
			}
		}

		for _, c := range listOf(st.Meta.Get("cites")) {
			_, t := resolve(c, true)
			if t == nil {
				warnf("%s: background citation %s does not resolve (off-record or missing)", sid, dyn.Str(c))
			}
		}
	}

	// agent statements (S-0065..S-0067)
	agents := loadAllAgentStatements(root)
	for _, st := range agents {
		name := filepath.Base(st.Path)
		aidV := st.Meta.Get("id")
		aid, isStr := aidV.(string)
		if !dyn.Truthy(aidV) || !isStr || !agentIDRE.MatchString(aid) {
			if dyn.Truthy(aidV) && !isStr {
				dyn.Raise("TypeError", "expected a string, got %s", dyn.TypeName(aidV))
			}
			errorf("agent/%s: missing or malformed id", name)
			continue
		}
		if !strings.HasPrefix(name, aid+"-") {
			errorf("agent/%s: filename does not start with its id %s", name, aid)
		}
		if prev, ok := ids[aid]; ok {
			errorf("duplicate id %s: %s and %s", aid, name, prev)
		}
		ids[aid] = name
		statementOverrideChecks(st, overrides, errorf)
		for _, k := range []string{"title", "answers", "author"} {
			if dyn.Strip(dyn.Str(dyn.Or(st.Meta.Get(k), ""))) == "" {
				errorf("%s: missing %s", aid, k)
			}
		}
		if dyn.Strip(st.Body) == "" {
			errorf("%s: empty body", aid)
		}
		if dyn.Truthy(st.Meta.Get("attestations")) {
			errorf("%s: an agent statement carries no attestations (%s)", aid, rule("agent-no-attestations"))
		}
		if !systemHashRE.MatchString(dyn.Str(dyn.Or(st.Meta.Get("system_hash"), ""))) || !dyn.Truthy(st.Meta.Get("system_version")) {
			errorf("%s: missing or malformed system stamp (%s)", aid, rule("system-stamp"))
		}
		madeIn := dyn.Or(st.Meta.Get("made_in"), "")
		s, t := resolve(madeIn, false)
		switch {
		case t == nil:
			errorf("%s: made_in %s does not resolve", aid, dyn.Str(st.Meta.Get("made_in")))
		case s.Kind() == "document" || !dyn.Equal(s.ModeValue(), "on-record"):
			errorf("%s: made_in %s is not in an on-record conversation (%s)", aid, dyn.Str(st.Meta.Get("made_in")), rule("on-record-only"))
		case !dyn.Equal(item(t, "author"), st.Meta.Get("author")) || !dyn.Equal(item(t, "role"), "assistant"):
			errorf("%s: author %s != made_in turn author %s (%s)", aid, dyn.Repr(st.Meta.Get("author")), dyn.Repr(item(t, "author")), rule("agent-own-turn"))
		}
		for _, xv := range listOf(st.Meta.Get("seen")) {
			x := dyn.AsDict(xv)
			s, t := resolve(dyn.Or(x.Get("turn"), ""), false)
			if t == nil || s.Kind() == "document" || !dyn.Equal(s.ModeValue(), "on-record") {
				errorf("%s: seen turn %s does not resolve to an on-record turn (%s)", aid, dyn.Str(x.Get("turn")), rule("on-record-only"))
			} else if !dyn.Truthy(st.Meta.Get("scrubbed")) && !scrubbedTurn(t) && !textContained(st.Body, turnText(t)) {
				errorf("%s: seen turn %s does not show the full text (%s)", aid, dyn.Str(x.Get("turn")), rule("seen-in-full"))
			}
		}
		if wi := st.Meta.Get("withdrawn_in"); dyn.Truthy(wi) {
			s, t := resolve(wi, false)
			if t == nil || !dyn.Equal(s.ModeValue(), "on-record") {
				errorf("%s: withdrawn_in %s does not resolve to an on-record turn (%s)", aid, dyn.Str(wi), rule("on-record-only"))
			}
		}
		for _, r := range []string{"supersedes", "amends", "clarifies"} {
			if v := st.Meta.Get(r); dyn.Truthy(v) && !strings.HasPrefix(dyn.Str(v), "A-") {
				errorf("%s: %s %s, a human statement; agents revise only agent statements (%s)", aid, r, dyn.Str(v), rule("agent-revision"))
			}
		}
		for _, c := range listOf(st.Meta.Get("cites")) {
			_, t := resolve(c, true)
			if t == nil {
				warnf("%s: background citation %s does not resolve (off-record or missing)", aid, dyn.Str(c))
			}
		}
	}

	// rests_on (S-0066)
	all := append(append([]*Statement{}, stmts...), agents...)
	byID := map[string]*Statement{}
	for _, x := range all {
		if k, ok := hashKey(x.Meta.Get("id")); ok {
			byID[k] = x
		}
	}
	lookup := func(v any) *Statement {
		k, ok := hashKey(v)
		if !ok {
			dyn.Raise("TypeError", "%s cannot be used as a key", dyn.TypeName(v))
		}
		return byID[k]
	}
	for _, st := range all {
		sid := st.Meta.Get("id")
		inForce := !dyn.Truthy(st.Meta.Get("superseded_by")) && !dyn.Truthy(st.Meta.Get("withdrawn_in"))
		for _, x := range listOf(st.Meta.Get("rests_on")) {
			dep := lookup(x)
			if dep == nil {
				errorf("%s: rests_on %s, which does not exist", dyn.Str(sid), dyn.Str(x))
			} else if inForce && dyn.Truthy(dep.Meta.Get("scrubbed")) {
				warnf("%s: rests on %s, which was scrubbed; review whether it should stand (%s)", dyn.Str(sid), dyn.Str(x), rule("override-scrub"))
			} else if inForce && strings.HasPrefix(dyn.Str(sid), "A-") && (dyn.Truthy(dep.Meta.Get("superseded_by")) || dyn.Truthy(dep.Meta.Get("withdrawn_in"))) {
				gone := "withdrawn"
				if sb := dep.Meta.Get("superseded_by"); dyn.Truthy(sb) {
					gone = "superseded by " + dyn.Str(sb)
				}
				warnf("%s: rests on %s, which is %s; review in cleanup (%s)", dyn.Str(sid), dyn.Str(x), gone, rule("agent-revision"))
			}
		}
	}

	// relation reciprocity
	marks := [][2]string{{"supersedes", "superseded_by"}, {"amends", "amended_by"}, {"clarifies", "clarified_by"}}
	for _, st := range all {
		sid := st.Meta.Get("id")
		for _, rm := range marks {
			relName, mark := rm[0], rm[1]
			if old := st.Meta.Get(relName); dyn.Truthy(old) {
				if o := lookup(old); o == nil {
					errorf("%s: %s %s, which does not exist", dyn.Str(sid), relName, dyn.Str(old))
				} else {
					m := o.Meta.Get(mark)
					var ok bool
					if relName == "supersedes" {
						ok = dyn.Equal(m, sid)
					} else {
						ok = containsValue(dyn.Or(m, []any{}), sid)
					}
					if !ok {
						errorf("%s: not marked %s %s", dyn.Str(old), mark, dyn.Str(sid))
					}
				}
			}
			m := st.Meta.Get(mark)
			var targets []any
			if relName == "supersedes" {
				if dyn.Truthy(m) {
					targets = []any{m}
				}
			} else {
				targets = listOf(m)
			}
			for _, nw := range targets {
				n := lookup(nw)
				if n == nil || !dyn.Equal(n.Meta.Get(relName), sid) {
					errorf("%s: %s %s is not reciprocal", dyn.Str(sid), mark, dyn.Str(nw))
				}
			}
		}
		// only the author changes a human statement (from 0.10.0; earlier supersessions were claims anyone
		// could record)
		if strings.HasPrefix(dyn.Str(sid), "S-") && madeFrom(st, []int64{0, 10, 0}) {
			for _, r := range relations {
				old := st.Meta.Get(r)
				if o := lookup(dyn.Or(old, "")); dyn.Truthy(old) && o != nil && strings.HasPrefix(dyn.Str(old), "S-") && !mayChange(authorOf(st), o) {
					errorf("%s: %s %s, whose author is %s, not %s (%s)", dyn.Str(sid), r, dyn.Str(old), dyn.Repr(authorOf(o)), dyn.Repr(authorOf(st)), rule("author-only"))
				}
			}
		}
		nMarks := len(listOf(st.Meta.Get("amended_by"))) + len(listOf(st.Meta.Get("clarified_by")))
		if nMarks > 2 && !dyn.Truthy(st.Meta.Get("superseded_by")) {
			warnf("%s: %d amendment/clarification marks; consider consolidating by supersession (%s)", dyn.Str(sid), nMarks, rule("change-by-supersession"))
		}
	}

	// context_for and the extra_context mark it sets, both ways
	incoming := map[string]bool{}
	for _, st := range all {
		sid := dyn.Str(st.Meta.Get("id"))
		if v := st.Meta.Get("context_for"); dyn.Truthy(v) {
			if _, ok := v.([]any); !ok {
				errorf("%s: context_for is not a list of entries (%s)", sid, rule("context-for"))
				continue
			}
		}
		seen := map[string]bool{}
		for _, l := range contextLinks(st.Meta) {
			if p := contextProblem(root, sid, l); p != "" {
				errorf("%s: context_for: %s (%s)", sid, p, rule("context-for"))
				continue
			}
			if seen[l.target] {
				errorf("%s: context_for names %s twice (%s)", sid, l.target, rule("context-for"))
			}
			seen[l.target] = true
			incoming[l.target] = true
			if strings.HasPrefix(sid, "S-") && strings.HasPrefix(l.target, "A-") {
				errorf("%s: context_for %s, an agent statement; a human statement relates to one only by superseding it (%s)", sid, l.target, rule("agent-no-attestations"))
			}
			if t := lookup(l.target); t != nil && t.Meta.Get(extraContextKey) != true {
				errorf("%s: not marked %s, though %s is context for it", l.target, extraContextKey, sid)
			}
		}
	}
	for _, st := range all {
		sid := dyn.Str(st.Meta.Get("id"))
		v, present := st.Meta.Lookup(extraContextKey)
		if !present {
			continue
		}
		if v != true {
			errorf("%s: %s is %s; the mark is only ever true", sid, extraContextKey, dyn.Repr(v))
		} else if !incoming[sid] {
			errorf("%s: marked %s, but no statement is context for it", sid, extraContextKey)
		}
	}

	if len(unstamped) > 0 {
		warnf("%d attestation(s) predate system stamping (no system_version/hash): %s .. %s", len(unstamped), unstamped[0], unstamped[len(unstamped)-1])
	}
	if len(noAnswers) > 0 {
		warnf("%d statement(s) predate the answers line (%s): %s .. %s", len(noAnswers), rule("title-and-answers"), noAnswers[0], noAnswers[len(noAnswers)-1])
	}
	if len(nums) > 0 {
		have := map[int]bool{}
		maxN := 0
		for _, n := range nums {
			have[n] = true
			maxN = max(maxN, n)
		}
		var missing []string
		for n := 1; n <= maxN; n++ {
			if !have[n] {
				missing = append(missing, fmt.Sprintf("S-%04d", n))
			}
		}
		if len(missing) > 0 {
			warnf("id gaps: %s", strings.Join(missing, ", "))
		}
	}

	for _, w := range warnings {
		println("warning: " + w)
	}
	for _, e := range errors {
		println("ERROR: " + e)
	}
	if len(errors) > 0 {
		println(fmt.Sprintf("errors in files kb writes mean they were changed outside kb (%s)", rule("write-through-kb")))
	}
	nDocs := 0
	for _, s := range sessions.Values() {
		if s.Kind() == "document" {
			nDocs++
		}
	}
	unseen := 0
	for _, a := range agents {
		if !dyn.Truthy(a.Meta.Get("seen")) {
			unseen++
		}
	}
	println(fmt.Sprintf("%d session(s), %d document(s), %d statement(s), %d agent statement(s) (%d unseen): %d error(s), %d warning(s)",
		sessions.Len()-nDocs, nDocs, len(stmts), len(agents), unseen, len(errors), len(warnings)))
	if len(errors) > 0 {
		return 1
	}
	return 0
}

// madeFrom says whether a statement's first attestation was made under at least version v.
func madeFrom(st *Statement, v []int64) bool {
	atts := listOf(st.Meta.Get("attestations"))
	if len(atts) == 0 {
		return false
	}
	d, ok := atts[0].(*dyn.Dict)
	if !ok || !dyn.Truthy(d.Get("system_version")) {
		return false
	}
	return tupleGE(versionTuple(d.Get("system_version")), v)
}

// containsValue says whether the list v has an element equal to x (or, for a string v, whether x is a
// substring of it).
func containsValue(v any, x any) bool {
	if s, ok := v.(string); ok {
		return strings.Contains(s, dyn.AsStr(x))
	}
	for _, it := range dyn.Iter(v) {
		if dyn.Equal(it, x) {
			return true
		}
	}
	return false
}

func addCheckParser(sub *argparse.Parser) {
	p := sub.AddParser("check", "verify invariants over evidence/ and attested/")
	p.Run = runCheck
}
