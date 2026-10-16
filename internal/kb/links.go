package kb

// Links between statements beyond supersession, amendment and clarification: context_for, the link that
// changes how another statement is read, with the extra_context mark it sets on its target; who may change
// a statement; and kb links, the one-call map of a statement's neighbourhood.

import (
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
)

// contextNoteLimit is the most characters a context_for note may hold. The note is the link's type, read
// when the link is followed, so it is a line, not an essay.
const contextNoteLimit = 160

// extraContextKey is the mark context_for sets on its target: a yes/no fact telling a reader that the file
// is not the whole picture. The linking statement and its note stay out of the target.
const extraContextKey = "extra_context"

// contextLink is one entry of a statement's context_for: the statement it changes the reading of, and how.
type contextLink struct{ target, note string }

// contextLinks reads a statement's context_for entries as they are written, without checking them.
func contextLinks(meta *dyn.Dict) []contextLink {
	var out []contextLink
	for _, e := range listOf(meta.Get("context_for")) {
		d, ok := e.(*dyn.Dict)
		if !ok {
			out = append(out, contextLink{target: dyn.Str(e)})
			continue
		}
		out = append(out, contextLink{dyn.Strip(dyn.Str(dyn.Or(d.Get("statement"), ""))), dyn.Strip(dyn.Str(dyn.Or(d.Get("note"), "")))})
	}
	return out
}

// contextProblem says what is wrong with one context_for entry of statement self, or "".
func contextProblem(root, self string, l contextLink) string {
	switch {
	case !statementIDRE.MatchString(l.target):
		return fmt.Sprintf("%s is not a statement id; each entry names a statement and carries a note "+
			"(statement: S-xxxx, note: how this changes its reading)", dyn.StrRepr(l.target))
	case l.target == self:
		return fmt.Sprintf("%s cannot be context for itself", l.target)
	case statementPath(root, l.target) == "":
		return fmt.Sprintf("%s does not exist", l.target)
	case l.note == "":
		return fmt.Sprintf("the link to %s has no note; the note says how this statement changes its reading", l.target)
	case strings.ContainsAny(l.note, "\r\n"):
		return fmt.Sprintf("the note on the link to %s runs over more than one line", l.target)
	case dyn.Len(l.note) > contextNoteLimit:
		return fmt.Sprintf("the note on the link to %s is %d characters; a note holds at most %d", l.target, dyn.Len(l.note), contextNoteLimit)
	}
	return ""
}

// contextFor checks a draft's context_for and returns it as it is written into the new statement. kind is
// "S" for kb attest, "A" for kb record; rels are the draft's supersedes/amends/clarifies.
func contextFor(root, kind string, meta, rels *dyn.Dict) []any {
	raw := meta.Get("context_for")
	if !dyn.Truthy(raw) {
		return nil
	}
	if _, ok := raw.([]any); !ok {
		die(fmt.Sprintf("context_for: a list of entries, each naming a statement and carrying a note (%s)", rule("context-for")))
	}
	var out []any
	seen := map[string]bool{}
	for _, l := range contextLinks(meta) {
		if p := contextProblem(root, "", l); p != "" {
			die(fmt.Sprintf("context_for: %s (%s)", p, rule("context-for")))
		}
		if seen[l.target] {
			die(fmt.Sprintf("context_for: %s is named twice; one link carries one note (%s)", l.target, rule("context-for")))
		}
		seen[l.target] = true
		for _, r := range rels.Keys() {
			if dyn.Str(rels.Get(r)) == l.target {
				die(fmt.Sprintf("context_for: this draft already %s %s, which marks it; no further link is needed (%s)", r, l.target, rule("context-for")))
			}
		}
		if kind == "S" && strings.HasPrefix(l.target, "A-") {
			die(fmt.Sprintf("context_for: %s is an agent statement; a human statement relates to an agent statement only "+
				"by superseding it (overriding or adopting it; %s)", l.target, rule("agent-no-attestations")))
		}
		out = append(out, dyn.D("statement", l.target, "note", l.note))
	}
	return out
}

// markContext sets extra_context on each target of a new statement's context_for. The mark is a yes/no
// fact, set once: the new statement and its note stay out of the target.
func markContext(root string, links []any, newID string) {
	for _, e := range links {
		target := dyn.Str(e.(*dyn.Dict).Get("statement"))
		p := statementPath(root, target)
		if p == "" {
			warn(fmt.Sprintf("context_for %s: statement not found; not marking", target))
			continue
		}
		st := loadStatement(p)
		if st.Meta.Get(extraContextKey) == true {
			println(fmt.Sprintf("%s already marked %s (context from %s)", target, extraContextKey, newID))
			continue
		}
		st.Meta.Set(extraContextKey, true)
		writeStatement(p, st)
		println(fmt.Sprintf("%s marked %s (context from %s)", target, extraContextKey, newID))
	}
}

// ---------------------------------------------------------------- who may change a statement

// authorOf is the author of a human statement: the person whose attestation created it, "" if it has none.
func authorOf(st *Statement) string {
	atts := listOf(st.Meta.Get("attestations"))
	if len(atts) == 0 {
		return ""
	}
	d, ok := atts[0].(*dyn.Dict)
	if !ok {
		return ""
	}
	return dyn.Str(dyn.Or(d.Get("by"), ""))
}

// mayChange says whether user may supersede, amend or clarify the human statement st, which only its author
// may do: supersession means "I changed my mind", and only the author can say that. When there are ways for
// one person to act for another (power of attorney, succession), they belong here, and nowhere else.
func mayChange(user string, st *Statement) bool {
	return user != "" && user == authorOf(st)
}

// requireAuthor refuses a supersession, amendment or clarification of a human statement by anyone but its
// author, pointing to context_for as the way to record a divergence.
func requireAuthor(root, user string, rels *dyn.Dict) {
	for _, r := range rels.Keys() {
		target := dyn.Str(rels.Get(r))
		if !strings.HasPrefix(target, "S-") {
			continue
		}
		st := loadStatement(statementPath(root, target))
		if mayChange(user, st) {
			continue
		}
		author := usersForDisplay(root).who(authorOf(st))
		if author == "" {
			author = "nobody (it has no attestation)"
		}
		die(fmt.Sprintf("%s: %s was made by %s, and only its author %s it. To record that yours "+
			"diverges from it, updates it or reads it differently, link to it with context_for, with a note "+
			"saying how; its author may later supersede it themselves (%s)",
			r, target, author, r, rule("author-only")))
	}
}

// ---------------------------------------------------------------- kb links

// linkNode is one statement as kb links sees it.
type linkNode struct {
	id, path, title string
	madeAt          string // when it was made, the order statements are listed in (S-0149)
	inForce         bool
	status          string
	supersededBy    string
	out             []linkEdge // links this statement's own file names
	in              []linkEdge // links other statements' files name to this one
}

// linkEdge is one link seen from one end: the kind, the other statement, and the note for context_for.
type linkEdge struct {
	kind, other, note string
}

// linkKinds are the kinds kb links shows, in the order it shows them.
var linkKinds = []string{"context_for", "supersedes", "amends", "clarifies", "rests_on"}

// loadLinkGraph reads every statement in attested/ and agent/ and the links between them. It is read from
// the files, not index.db, so it is never stale.
func loadLinkGraph(root string) map[string]*linkNode {
	nodes := map[string]*linkNode{}
	var all []*Statement
	all = append(all, loadAllStatements(root)...)
	all = append(all, loadAllAgentStatements(root)...)
	for _, st := range all {
		id := dyn.Str(dyn.Or(st.Meta.Get("id"), ""))
		if id == "" {
			continue
		}
		n := &linkNode{id: id, path: filepath.ToSlash(rel(st.Path, root)), title: dyn.Str(dyn.Or(st.Meta.Get("title"), "")), madeAt: madeAt(st.Meta), inForce: true, status: "in force"}
		if sb := st.Meta.Get("superseded_by"); dyn.Truthy(sb) {
			n.inForce, n.status, n.supersededBy = false, "superseded by "+dyn.Str(sb), dyn.Str(sb)
		} else if dyn.Truthy(st.Meta.Get("withdrawn_in")) {
			n.inForce, n.status = false, "withdrawn"
		}
		for _, l := range contextLinks(st.Meta) {
			n.out = append(n.out, linkEdge{"context_for", l.target, l.note})
		}
		for _, r := range relations {
			if v := st.Meta.Get(r); dyn.Truthy(v) {
				n.out = append(n.out, linkEdge{r, dyn.Str(v), ""})
			}
		}
		for _, x := range listOf(st.Meta.Get("rests_on")) {
			n.out = append(n.out, linkEdge{"rests_on", dyn.Str(x), ""})
		}
		nodes[id] = n
	}
	for _, n := range nodes {
		for _, e := range n.out {
			if o := nodes[e.other]; o != nil {
				o.in = append(o.in, linkEdge{e.kind, n.id, e.note})
			}
		}
	}
	return nodes
}

// linkMap is the neighbourhood kb links prints: every statement reached, and the ones shown in full.
type linkMap struct {
	nodes map[string]*linkNode
	order []string
	full  map[string]bool
}

// mapLinks walks out from roots. A root is always shown in full; any other statement fewer than depth
// links from a root, and in force, is shown in full with its links; every other statement reached is shown
// on one line. Supersession is followed to its end whatever the depth, so the version in force is always
// reachable.
func mapLinks(nodes map[string]*linkNode, roots []string, depth int) linkMap {
	m := linkMap{nodes: nodes, full: map[string]bool{}}
	dist := map[string]int{}
	add := func(id string, d int) {
		if _, ok := dist[id]; ok || nodes[id] == nil {
			return
		}
		dist[id] = d
		m.order = append(m.order, id)
	}
	follow := func(id string) {
		for cur := id; nodes[cur] != nil && nodes[cur].supersededBy != ""; cur = nodes[cur].supersededBy {
			next := nodes[cur].supersededBy
			if _, seen := dist[next]; seen {
				return
			}
			add(next, dist[cur]+1)
		}
	}
	var frontier []string
	for _, r := range roots {
		add(r, 0)
		if !m.full[r] {
			m.full[r] = true
			frontier = append(frontier, r)
		}
	}
	for _, r := range roots {
		follow(r)
	}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var reached []string
		for _, id := range frontier {
			n := nodes[id]
			for _, e := range append(append([]linkEdge{}, n.out...), n.in...) {
				add(e.other, d+1)
				reached = append(reached, e.other)
			}
		}
		var next []string
		for _, x := range reached {
			if nodes[x] == nil {
				continue
			}
			follow(x)
			if dist[x] == d+1 && d+1 < depth && nodes[x].inForce && !m.full[x] {
				m.full[x] = true
				next = append(next, x)
			}
		}
		sortInForceFirst(nodes, next)
		frontier = next
	}
	return m
}

func sortInForceFirst(nodes map[string]*linkNode, ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := nodes[ids[i]], nodes[ids[j]]
		if a.inForce != b.inForce {
			return a.inForce
		}
		return madeBefore(nodes, a.id, b.id)
	})
}

// madeBefore orders two statements by when they were made, since ids carry no order (S-0149); a statement
// not found comes last, and the id breaks a tie.
func madeBefore(nodes map[string]*linkNode, a, b string) bool {
	na, nb := nodes[a], nodes[b]
	if (na == nil) != (nb == nil) {
		return nb == nil
	}
	if na != nil && na.madeAt != nb.madeAt {
		return na.madeAt < nb.madeAt
	}
	return a < b
}

// oneLine is a statement as one line: id, standing, file and title.
func (n *linkNode) oneLine() string {
	return fmt.Sprintf("%s  %s  %s  %s", n.id, n.status, n.path, n.title)
}

// render prints the map: the statements shown in full, each with its links under it, then every other
// statement reached, one per line, those in force first. Under a full entry, "->" marks a link its own file
// names and "<-" one another statement's file names to it. Links with statements not in force, apart from
// supersession, are folded into one line at the end of the entry.
func (m linkMap) render() string {
	var b strings.Builder
	var rest []string
	first := true
	for _, id := range m.order {
		if !m.full[id] {
			rest = append(rest, id)
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false
		n := m.nodes[id]
		b.WriteString(n.oneLine() + "\n")
		var folded []string
		for _, kind := range linkKinds {
			for _, dir := range []string{"<-", "->"} {
				edges := n.out
				if dir == "<-" {
					edges = n.in
				}
				var shown []linkEdge
				for _, e := range edges {
					if e.kind != kind {
						continue
					}
					if o := m.nodes[e.other]; o != nil && !o.inForce && kind != "supersedes" {
						folded = append(folded, fmt.Sprintf("%s %s %s", dir, kind, e.other))
						continue
					}
					shown = append(shown, e)
				}
				sort.SliceStable(shown, func(i, j int) bool { return madeBefore(m.nodes, shown[i].other, shown[j].other) })
				if kind == "context_for" {
					for _, e := range shown {
						b.WriteString(fmt.Sprintf("  %s %s %s: %s\n", dir, kind, e.other, e.note))
					}
					continue
				}
				if len(shown) > 0 {
					var ids []string
					for _, e := range shown {
						x := e.other
						if m.nodes[x] == nil {
							x += " (missing)"
						}
						ids = append(ids, x)
					}
					b.WriteString(fmt.Sprintf("  %s %s %s\n", dir, kind, strings.Join(ids, " ")))
				}
			}
		}
		if len(folded) > 0 {
			b.WriteString("  not in force: " + strings.Join(folded, ", ") + "\n")
		}
	}
	if len(rest) > 0 {
		sortInForceFirst(m.nodes, rest)
		b.WriteString("\nalso reached:\n")
		for _, id := range rest {
			b.WriteString(m.nodes[id].oneLine() + "\n")
		}
	}
	return b.String()
}

func runLinks(ns argparse.Namespace) int {
	root := repoRoot()
	depthV := ns["depth"].(*big.Int)
	if !depthV.IsInt64() || depthV.Int64() < 1 || depthV.Int64() > 100 {
		die("--depth must be from 1 to 100")
	}
	depth := int(depthV.Int64())
	nodes := loadLinkGraph(root)
	var roots []string
	for _, id := range ns.List("statements") {
		id = strings.ToUpper(strings.TrimSpace(id))
		if !statementIDRE.MatchString(id) {
			die(fmt.Sprintf("%s is not a statement id (S-xxxx or A-xxxx)", dyn.StrRepr(id)))
		}
		if nodes[id] == nil {
			die(fmt.Sprintf("statement %s not found", id))
		}
		roots = append(roots, id)
	}
	fmt.Fprint(Stdout, mapLinks(nodes, roots, depth).render())
	return 0
}

func addLinksParser(sub *argparse.Parser) {
	p := sub.AddParser("links", "map the links around statements in one call: ids, files, titles, notes, standing")
	p.Positional("statements", argparse.OneMore, "S-xxxx or A-xxxx")
	p.IntOption("--depth", "how many links out to go (default 1); statements in force nearer than that are shown with their links", 1)
	p.Run = runLinks
}
