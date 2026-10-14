package kb

// kb check — mechanical invariants over evidence/, attested/ and agent/. See kb/check.py.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/pin"
	"kenttheclerk/internal/py"
)

var (
	humanIDRE = regexp.MustCompile(`^S-\p{Nd}{4,}\n?$`)
	agentIDRE = regexp.MustCompile(`^A-\p{Nd}{4,}\n?$`)
)

// pathKey identifies a resolved path for comparison, as Path equality does (case-insensitive on Windows).
func pathKey(p string) string {
	p = py.Resolve(p)
	if py.Windows {
		return py.Lower(p)
	}
	return p
}

func listOf(v any) []any {
	if !py.Truthy(v) {
		return nil
	}
	return py.Iter(v)
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

	// resolve is same-session resolution by default; background citations may resolve through the
	// fork's ancestry chain (S-0034); attestation and proposal turns may not (S-0032).
	resolve := func(ptrStr any, throughAncestry bool) (*Session, *py.Dict) {
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
		if !py.Truthy(bp) {
			continue
		}
		chain := ancestry(sessions, s)
		if len(chain) == 0 {
			warnf("session %s: forked from turn %s, which is in no snapshot "+
				"(parent not snapshotted, or its snapshot predates the branch point)", py.Head(s.ID(), 8), py.Head(py.AsStr(bp), 8))
		} else if py.Equal(s.ModeValue(), "on-record") {
			for _, a := range chain {
				if !py.Equal(a.s.ModeValue(), "on-record") {
					errorf("session %s is on-record but its ancestor %s is %s (%s)", py.Head(s.ID(), 8), py.Head(a.s.ID(), 8), a.s.Mode(), rule("fork-ancestry"))
				}
			}
		}
	}

	// documents (S-0038, S-0039)
	referenced := map[string]bool{}
	for _, s := range sessions.Values() {
		if s.Kind() != "document" {
			continue
		}
		o := py.AsDict(py.Or(s.Header.Get("original"), py.NewDict()))
		h := py.Str(py.Or(o.Get("hash"), ""))
		if !originalHashRE.MatchString(h) {
			errorf("document %s: header has a missing or malformed original hash", py.Head(s.ID(), 8))
			continue
		}
		hexdigest := strings.SplitN(h, ":", 2)[1]
		relp := py.Or(o.Get("path"), "")
		relS := py.AsStr(relp)
		var opath string
		if py.Equal(s.ModeValue(), "on-record") {
			opath = filepath.Join(filepath.Dir(s.Path), relS)
		} else {
			opath = filepath.Join(root, "off-record", relS)
		}
		m := originalNameRE.FindStringSubmatch(py.Name(opath))
		if m == nil || m[1] != hexdigest {
			errorf("document %s: original path %s is not named by its hash (%s)", py.Head(s.ID(), 8), py.StrRepr(relS), rule("frozen-projection"))
		}
		if !py.IsFile(opath) {
			errorf("document %s: original %s is missing", py.Head(s.ID(), 8), relS)
		} else {
			referenced[pathKey(opath)] = true
			actual := sha256File(opath)
			if actual != hexdigest {
				errorf("document %s: original %s hashes to %s…, not its name; "+
					"the bytes were damaged or modified (%s)", py.Head(s.ID(), 8), relS, actual[:12], rule("frozen-projection"))
			}
		}
		for _, t := range s.Turns {
			if !py.Equal(t.Get("role"), "document") || !py.Equal(t.Get("author"), "document") {
				u := getOr(t, "uuid", "?")
				errorf("document %s: chunk %s is not role/author 'document'", py.Head(s.ID(), 8), py.Head(py.AsStr(u), 8))
			}
		}
		if !py.Truthy(s.Header.Get("extractor")) {
			errorf("document %s: header has no extractor", py.Head(s.ID(), 8))
		}
	}
	for _, mode := range []string{"on-record", "off-record"} {
		d := originalsDir(root, mode)
		if py.IsDir(d) {
			for _, e := range py.Scandir(d) {
				p := filepath.Join(d, e.Name())
				if py.IsFile(p) && !referenced[pathKey(p)] {
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
		if !py.Truthy(sidV) || !isStr || !humanIDRE.MatchString(sid) {
			if py.Truthy(sidV) && !isStr {
				py.Raise("TypeError", "expected string or bytes-like object, got '%s'", py.TypeName(sidV))
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
		n, _ := py.ParseInt(sid[2:])
		nums = append(nums, int(n.Int64()))
		if !py.Truthy(st.Meta.Get("title")) {
			errorf("%s: missing title (%s)", sid, rule("title-and-answers"))
		}
		if py.Strip(st.Body) == "" {
			errorf("%s: empty body", sid)
		}
		if py.Strip(py.Str(py.Or(st.Meta.Get("answers"), ""))) == "" {
			var versions []any
			for _, a := range listOf(st.Meta.Get("attestations")) {
				if v := py.AsDict(a).Get("system_version"); py.Truthy(v) {
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
		if !py.Truthy(prop) {
			errorf("%s: missing proposed_in", sid)
		} else {
			s, t := resolve(prop, false)
			switch {
			case t == nil:
				errorf("%s: proposed_in %s does not resolve", sid, py.Str(prop))
			case s.Kind() == "document":
				errorf("%s: proposed_in %s is a document chunk; documents cannot attest (%s)", sid, py.Str(prop), rule("documents-cannot-attest"))
			default:
				if !py.Equal(s.ModeValue(), "on-record") {
					errorf("%s: proposed_in %s is in a %s session (%s)", sid, py.Str(prop), s.Mode(), rule("on-record-only"))
				}
				modified := false
				for _, a := range listOf(st.Meta.Get("attestations")) {
					if py.Equal(py.AsDict(a).Get("form"), "agreed-with-modification") {
						modified = true
					}
				}
				if modified {
					if !py.Truthy(st.Meta.Get("modified_in")) {
						errorf("%s: agreed-with-modification but no modified_in turn", sid)
					} else {
						_, mt := resolve(st.Meta.Get("modified_in"), false)
						if mt == nil || !py.Equal(item(mt, "role"), "user") {
							errorf("%s: modified_in %s does not resolve to a human turn", sid, py.Str(st.Meta.Get("modified_in")))
						}
					}
				} else if !textContained(st.Body, turnText(t)) {
					errorf("%s: body text is not contained verbatim in proposal turn %s (%s)", sid, py.Str(prop), rule("exact-text"))
				}
				if !py.Equal(st.Meta.Get("originator"), item(t, "author")) {
					errorf("%s: originator %s != proposal turn author %s", sid, py.Repr(st.Meta.Get("originator")), py.Repr(item(t, "author")))
				}
			}
		}

		atts := listOf(st.Meta.Get("attestations"))
		if len(atts) == 0 {
			warnf("%s: no attestations", sid)
		}
		var seenBy []any
		for _, av := range atts {
			a := py.AsDict(av)
			by, turn, form := a.Get("by"), a.Get("turn"), a.Get("form")
			if fs, ok := form.(string); !ok || !in(fs, attestationForms) {
				errorf("%s: attestation form %s invalid", sid, py.Repr(form))
			}
			for _, x := range seenBy {
				if py.Equal(x, by) {
					errorf("%s: duplicate attestation by %s (%s)", sid, py.Str(by), rule("attest-once"))
					break
				}
			}
			seenBy = append(seenBy, by)
			if !py.Truthy(turn) {
				errorf("%s: attestation by %s has no turn", sid, py.Str(by))
				continue
			}
			s, t := resolve(turn, false)
			if t == nil {
				errorf("%s: attestation turn %s does not resolve", sid, py.Str(turn))
				continue
			}
			if s.Kind() == "document" {
				errorf("%s: attestation turn %s is a document chunk; documents cannot attest (%s)", sid, py.Str(turn), rule("documents-cannot-attest"))
				continue
			}
			if !py.Equal(s.ModeValue(), "on-record") {
				errorf("%s: attestation turn %s is in a %s session (must be on-record) (%s)", sid, py.Str(turn), s.Mode(), rule("on-record-only"))
			}
			if !py.Equal(item(t, "role"), "user") || !py.Equal(item(t, "author"), by) {
				errorf("%s: attestation turn %s is authored by %s, not %s (%s)", sid, py.Str(turn), py.Repr(item(t, "author")), py.Repr(by), rule("own-turn"))
			}
			if py.Equal(form, "self") && !textContained(st.Body, turnText(t)) {
				errorf("%s: self-attestation but body not contained in %s (%s)", sid, py.Str(turn), rule("exact-text"))
			}
			h, v := a.Get("system_hash"), a.Get("system_version")
			if !py.Truthy(h) && !py.Truthy(v) {
				unstamped = append(unstamped, sid+"/"+py.Str(by))
			} else {
				if !py.Truthy(v) {
					errorf("%s: attestation by %s has system_hash but no system_version (%s)", sid, py.Str(by), rule("system-stamp"))
				}
				if !py.Truthy(h) || !systemHashRE.MatchString(py.Str(h)) {
					errorf("%s: attestation by %s has a missing or malformed system_hash (%s)", sid, py.Str(by), rule("system-stamp"))
				}
			}
		}

		for _, c := range listOf(st.Meta.Get("cites")) {
			_, t := resolve(c, true)
			if t == nil {
				warnf("%s: background citation %s does not resolve (off-record or missing)", sid, py.Str(c))
			}
		}
	}

	// agent statements (S-0065..S-0067)
	agents := loadAllAgentStatements(root)
	for _, st := range agents {
		name := filepath.Base(st.Path)
		aidV := st.Meta.Get("id")
		aid, isStr := aidV.(string)
		if !py.Truthy(aidV) || !isStr || !agentIDRE.MatchString(aid) {
			if py.Truthy(aidV) && !isStr {
				py.Raise("TypeError", "expected string or bytes-like object, got '%s'", py.TypeName(aidV))
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
		for _, k := range []string{"title", "answers", "author"} {
			if py.Strip(py.Str(py.Or(st.Meta.Get(k), ""))) == "" {
				errorf("%s: missing %s", aid, k)
			}
		}
		if py.Strip(st.Body) == "" {
			errorf("%s: empty body", aid)
		}
		if py.Truthy(st.Meta.Get("attestations")) {
			errorf("%s: an agent statement carries no attestations (%s)", aid, rule("agent-no-attestations"))
		}
		if !systemHashRE.MatchString(py.Str(py.Or(st.Meta.Get("system_hash"), ""))) || !py.Truthy(st.Meta.Get("system_version")) {
			errorf("%s: missing or malformed system stamp (%s)", aid, rule("system-stamp"))
		}
		madeIn := py.Or(st.Meta.Get("made_in"), "")
		s, t := resolve(madeIn, false)
		switch {
		case t == nil:
			errorf("%s: made_in %s does not resolve", aid, py.Str(st.Meta.Get("made_in")))
		case s.Kind() == "document" || !py.Equal(s.ModeValue(), "on-record"):
			errorf("%s: made_in %s is not in an on-record conversation (%s)", aid, py.Str(st.Meta.Get("made_in")), rule("on-record-only"))
		case !py.Equal(item(t, "author"), st.Meta.Get("author")) || !py.Equal(item(t, "role"), "assistant"):
			errorf("%s: author %s != made_in turn author %s (%s)", aid, py.Repr(st.Meta.Get("author")), py.Repr(item(t, "author")), rule("agent-own-turn"))
		}
		for _, xv := range listOf(st.Meta.Get("seen")) {
			x := py.AsDict(xv)
			s, t := resolve(py.Or(x.Get("turn"), ""), false)
			if t == nil || s.Kind() == "document" || !py.Equal(s.ModeValue(), "on-record") {
				errorf("%s: seen turn %s does not resolve to an on-record turn (%s)", aid, py.Str(x.Get("turn")), rule("on-record-only"))
			} else if !textContained(st.Body, turnText(t)) {
				errorf("%s: seen turn %s does not show the full text (%s)", aid, py.Str(x.Get("turn")), rule("seen-in-full"))
			}
		}
		if wi := st.Meta.Get("withdrawn_in"); py.Truthy(wi) {
			s, t := resolve(wi, false)
			if t == nil || !py.Equal(s.ModeValue(), "on-record") {
				errorf("%s: withdrawn_in %s does not resolve to an on-record turn (%s)", aid, py.Str(wi), rule("on-record-only"))
			}
		}
		for _, r := range []string{"supersedes", "amends", "clarifies"} {
			if v := st.Meta.Get(r); py.Truthy(v) && !strings.HasPrefix(py.Str(v), "A-") {
				errorf("%s: %s %s, a human statement; agents revise only agent statements (%s)", aid, r, py.Str(v), rule("agent-revision"))
			}
		}
		for _, c := range listOf(st.Meta.Get("cites")) {
			_, t := resolve(c, true)
			if t == nil {
				warnf("%s: background citation %s does not resolve (off-record or missing)", aid, py.Str(c))
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
			py.Raise("TypeError", "unhashable type: '%s'", py.TypeName(v))
		}
		return byID[k]
	}
	for _, st := range all {
		sid := st.Meta.Get("id")
		inForce := !py.Truthy(st.Meta.Get("superseded_by")) && !py.Truthy(st.Meta.Get("withdrawn_in"))
		for _, x := range listOf(st.Meta.Get("rests_on")) {
			dep := lookup(x)
			if dep == nil {
				errorf("%s: rests_on %s, which does not exist", py.Str(sid), py.Str(x))
			} else if inForce && strings.HasPrefix(py.Str(sid), "A-") && (py.Truthy(dep.Meta.Get("superseded_by")) || py.Truthy(dep.Meta.Get("withdrawn_in"))) {
				gone := "withdrawn"
				if sb := dep.Meta.Get("superseded_by"); py.Truthy(sb) {
					gone = "superseded by " + py.Str(sb)
				}
				warnf("%s: rests on %s, which is %s; review in cleanup (%s)", py.Str(sid), py.Str(x), gone, rule("agent-revision"))
			}
		}
	}

	// relation reciprocity
	marks := [][2]string{{"supersedes", "superseded_by"}, {"amends", "amended_by"}, {"clarifies", "clarified_by"}}
	for _, st := range all {
		sid := st.Meta.Get("id")
		for _, rm := range marks {
			relName, mark := rm[0], rm[1]
			if old := st.Meta.Get(relName); py.Truthy(old) {
				if o := lookup(old); o == nil {
					errorf("%s: %s %s, which does not exist", py.Str(sid), relName, py.Str(old))
				} else {
					m := o.Meta.Get(mark)
					var ok bool
					if relName == "supersedes" {
						ok = py.Equal(m, sid)
					} else {
						ok = containsValue(py.Or(m, []any{}), sid)
					}
					if !ok {
						errorf("%s: not marked %s %s", py.Str(old), mark, py.Str(sid))
					}
				}
			}
			m := st.Meta.Get(mark)
			var targets []any
			if relName == "supersedes" {
				if py.Truthy(m) {
					targets = []any{m}
				}
			} else {
				targets = listOf(m)
			}
			for _, nw := range targets {
				n := lookup(nw)
				if n == nil || !py.Equal(n.Meta.Get(relName), sid) {
					errorf("%s: %s %s is not reciprocal", py.Str(sid), mark, py.Str(nw))
				}
			}
		}
		nMarks := len(listOf(st.Meta.Get("amended_by"))) + len(listOf(st.Meta.Get("clarified_by")))
		if nMarks > 2 && !py.Truthy(st.Meta.Get("superseded_by")) {
			warnf("%s: %d amendment/clarification marks; consider consolidating by supersession (%s)", py.Str(sid), nMarks, rule("change-by-supersession"))
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
		if !py.Truthy(a.Meta.Get("seen")) {
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

// containsValue is `x in v` for a list (or a str, as a substring).
func containsValue(v any, x any) bool {
	if s, ok := v.(string); ok {
		return strings.Contains(s, py.AsStr(x))
	}
	for _, it := range py.Iter(v) {
		if py.Equal(it, x) {
			return true
		}
	}
	return false
}

func addCheckParser(sub *argparse.Parser) {
	p := sub.AddParser("check", "verify invariants over evidence/ and attested/")
	p.Run = runCheck
}
