package kb

// Repairing a random id collision (S-W5AJ): copies gave new statements the same id, and a merge brought
// them into one record. The merge driver never sees it, since the statements are usually different files,
// so kb resolve looks across the record for ids two or more statement files carry. The one on the shared
// history keeps its id; each other gets a new one, and every reference to it is rewritten, the text
// included, where only the id changes. It is a repair by kb, entered in the override log so that it can be
// traced, and not an override: it needs no confirmation or human turn.
//
// The shared history is the first of these refs that holds any of the statements: origin's default
// branch, the upstream branch, the branch a rebase is onto, the side being merged in (MERGE_HEAD), and the
// second parent of a merge just made. If it holds one, that one keeps the id; if it holds several, the one
// that reached its first-parent line first keeps it (S-H2ZW). A reference belongs to whichever of the
// statements existed in the commit it was written in, since a statement's references are fixed when it is
// made; one written where several or none of them existed, or not yet committed, cannot be attributed and
// is listed for the human. The marks the moving statements' relations set on others are rebuilt from
// those relations.
//
// The log is only appended to. An entry made before the repair and naming a moving statement is not
// rewritten, since other copies may hold that line; the repair lists it in moved_entries, and kb reads it
// through the repair. The repair's entry is written before the files, so that a repair cut short is
// finished by running kb resolve again.

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"kenttheclerk/internal/dyn"
)

// ovRepairID is the log entry for a repaired id collision: not an override, so kb check reads it apart
// from the list.
const ovRepairID = "repair-id"

// notRewritten are the frontmatter fields an id is never rewritten in: the statement's own id, which moves
// on its own; the marks others' relations set on it (superseded_by and the like), which are rebuilt from
// those relations; and the attestations, the seen list and the citations, which hold turns, not ids. Every
// other field, its relations (rests_on, supersedes, amends, clarifies, context_for), title and answers
// among them, is rewritten where it names the id.
var notRewritten = map[string]bool{"id": true, "superseded_by": true, "amended_by": true, "clarified_by": true,
	extraContextKey: true, "attestations": true, "seen": true, "cites": true}

// statementOrigin tells statements with one id apart: when it was made and the turn it was made in.
func statementOrigin(meta *dyn.Dict) string {
	turn := dyn.Str(dyn.Or(meta.Get("made_in"), ""))
	if atts := listOf(meta.Get("attestations")); len(atts) > 0 {
		if d, ok := atts[0].(*dyn.Dict); ok {
			turn = dyn.Str(dyn.Or(d.Get("turn"), ""))
		}
	}
	return madeAt(meta) + "|" + turn
}

// safeStatement reads a statement file's text, or says why it cannot: one holding conflict markers is
// refused.
func safeStatement(path, text string) (*Statement, error) {
	meta, body, err := parseStatementText(text)
	if err != nil {
		return nil, err
	}
	return &Statement{path, meta, body}, nil
}

func statementFileText(st *Statement) string { return dumpFrontmatter(st.Meta) + "\n" + st.Body + "\n" }

// idCollision is statements with one id: keep is on the shared history and keeps it; each of others gets a
// new one. For two files of one name, all have the same Path.
type idCollision struct {
	id     string
	keep   *Statement
	others []*Statement
	ref    string // the commit that showed which is shared
	label  string // that ref as the log names it: its name and commit
	both   bool   // several were on the shared history, and keep reached it first (S-H2ZW)
}

func (c idCollision) all() []*Statement { return append([]*Statement{c.keep}, c.others...) }

// atRef is the file at rel inside the knowledge base on ref, if there is one.
func atRef(root, ref, rel string) (string, bool) {
	out, err := exec.Command("git", "-C", root, "show", ref+":./"+filepath.ToSlash(rel)).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func relPath(root, p string) string {
	if r, ok := relInside(root, p); ok {
		return r
	}
	return filepath.ToSlash(p)
}

// onRef is whether ref holds the statement st, at its path and with its origin.
func onRef(root, ref string, st *Statement) bool {
	text, ok := atRef(root, ref, relPath(root, st.Path))
	if !ok {
		return false
	}
	there, err := safeStatement(st.Path, text)
	return err == nil && dyn.Equal(there.Meta.Get("id"), st.Meta.Get("id")) && statementOrigin(there.Meta) == statementOrigin(st.Meta)
}

func refExists(root, ref string) bool {
	return exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", ref+"^{commit}").Run() == nil
}

// sharedRefs are the refs that can show which statement is on the shared history, in the order kb tries
// them: origin's default branch first, so that a feature branch's upstream does not keep the feature's
// copy over the one on the main line; then the upstream branch; during a rebase, the commit it is onto,
// since HEAD is detached; then the side being merged in and the second parent of a merge just made.
func sharedRefs(root string) []string {
	refs := []string{"refs/remotes/origin/HEAD", "@{u}"}
	for _, f := range []string{"rebase-merge/onto", "rebase-apply/onto"} {
		if p, st := git(root, "rev-parse", "--git-path", f); st == 0 {
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
				refs = append(refs, strings.TrimSpace(string(b)))
			}
		}
	}
	return append(refs, "MERGE_HEAD", "HEAD^2")
}

// chooseKeeper decides which of the statements keeps the id: the one on the first shared ref that holds
// any of them, or, where it holds several, the one that reached its first-parent line first.
func chooseKeeper(root, id string, sts []*Statement) (c idCollision, problem string) {
	if gitTop(root) == "" {
		return c, "the knowledge base is not in git, so kb cannot tell which of them was shared"
	}
	for _, r := range sharedRefs(root) {
		if !refExists(root, r) {
			continue
		}
		var on []*Statement
		for _, st := range sts {
			if onRef(root, r, st) {
				on = append(on, st)
			}
		}
		if len(on) == 0 {
			continue
		}
		sha, _ := git(root, "rev-parse", "--short", r+"^{commit}")
		name := r
		if n, st := git(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", r); st == 0 && n != "" && r == "@{u}" {
			name = n
		} else if r == "refs/remotes/origin/HEAD" {
			if n, st := git(root, "symbolic-ref", "--short", r); st == 0 && n != "" {
				name = n
			}
		}
		c = idCollision{id: id, ref: sha, label: name + " (" + sha + ")", keep: on[0], both: len(on) > 1}
		if c.both {
			c.keep = firstLanded(root, sha, on)
		}
		for _, st := range sts {
			if st != c.keep {
				c.others = append(c.others, st)
			}
		}
		return c, ""
	}
	return c, "no shared history holds any of them (origin's default branch, the upstream branch, a rebase's " +
		"onto, MERGE_HEAD, HEAD^2)"
}

// firstLanded is the statement that reached the shared line ref first, read from its first-parent history:
// the first commit on that line that added each one's file, compared with the first parent. A statement
// that landed in the same commit as another yields to the one made earlier, then to the file name.
func firstLanded(root, ref string, on []*Statement) *Statement {
	out, _ := git(root, "rev-list", "--first-parent", ref)
	revs := strings.Fields(out)
	pos := map[string]int{}
	for i, r := range revs {
		pos[r] = len(revs) - i // older commits on the line come later in rev-list's output
	}
	landed := func(st *Statement) int {
		out, _ := git(root, "log", "--first-parent", "--diff-merges=first-parent", "--diff-filter=A", "--format=%H",
			ref, "--", relPath(root, st.Path))
		best := len(revs) + 1
		for _, h := range strings.Fields(out) {
			if p, ok := pos[h]; ok && p < best && onRef(root, h, st) {
				best = p
			}
		}
		return best
	}
	keep, at := on[0], landed(on[0])
	for _, st := range on[1:] {
		l := landed(st)
		if l < at || l == at && (madeAt(st.Meta) < madeAt(keep.Meta) || madeAt(st.Meta) == madeAt(keep.Meta) && st.Path < keep.Path) {
			keep, at = st, l
		}
	}
	return keep
}

// addingCommit is the commit that added the file at rel, the oldest if it was added more than once, or ""
// when no commit did, as for a file not yet committed. Merges are not counted, so it is the commit the
// file was written in.
func addingCommit(root, rel string) string {
	out, st := git(root, "log", "--format=%H", "--diff-filter=A", "--", filepath.ToSlash(rel))
	if st != 0 || out == "" {
		return ""
	}
	l := strings.Fields(out)
	return l[len(l)-1]
}

// entryCommit is the commit that added a line to the override log: the oldest that changed how often it
// occurs, searched for by the whole line, which an entry's time and id make unique.
func entryCommit(root, line string) string {
	out, st := git(root, "log", "--format=%H", "--reverse", "-S"+line, "--", overrideLogName)
	if st != 0 || out == "" {
		return ""
	}
	return strings.Fields(out)[0]
}

// citedOne is which of the statements a reference made in commit refers to: the one that existed there.
// -1 when several or none did, or commit is "", as for a citation not yet committed: kb cannot attribute it.
func citedOne(root, commit string, sts []*Statement) int {
	if commit == "" {
		return -1
	}
	found := -1
	for i, st := range sts {
		if onRef(root, commit, st) {
			if found >= 0 {
				return -1
			}
			found = i
		}
	}
	return found
}

// statementFiles are the record's statement files.
func statementFiles(root string) []string {
	var out []string
	for kind, dir := range statementDirs {
		out = append(out, dyn.Glob(filepath.Join(root, dir), kind+"-*.md")...)
	}
	sort.Strings(out)
	return out
}

// duplicateIDs are the ids two or more statement files carry, with those files, read safely; files kb
// cannot read are left to kb check.
func duplicateIDs(root string) map[string][]*Statement {
	byID := map[string][]*Statement{}
	for _, p := range statementFiles(root) {
		st, err := safeStatement(p, dyn.ReadText(p))
		if err != nil || !dyn.Truthy(st.Meta.Get("id")) {
			continue
		}
		id := strings.ToUpper(dyn.Str(st.Meta.Get("id")))
		byID[id] = append(byID[id], st)
	}
	for id, sts := range byID {
		if len(sts) < 2 {
			delete(byID, id)
		}
	}
	return byID
}

// replaceID rewrites the id old as a whole word in s.
func replaceID(s, old, new string) string {
	return regexp.MustCompile(`\b`+regexp.QuoteMeta(old)+`\b`).ReplaceAllString(s, new)
}

// rewriteRefs rewrites old to new in a statement's references and its text, and says whether anything
// changed. Only the id changes. With dry set it only says whether anything would.
func rewriteRefs(st *Statement, old, new string, dry ...bool) bool {
	write := len(dry) == 0 || !dry[0]
	changed := false
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if r := replaceID(x, old, new); r != x {
				changed = true
				return r
			}
		case []any:
			for i := range x {
				if r := walk(x[i]); write {
					x[i] = r
				}
			}
		case *dyn.Dict:
			for _, k := range x.Keys() {
				if r := walk(x.Get(k)); write {
					x.Set(k, r)
				}
			}
		}
		return v
	}
	for _, k := range st.Meta.Keys() {
		if !notRewritten[k] {
			if r := walk(st.Meta.Get(k)); write {
				st.Meta.Set(k, r)
			}
		}
	}
	if b := replaceID(st.Body, old, new); b != st.Body {
		changed = true
		if write {
			st.Body = b
		}
	}
	return changed
}

// pendingRepair is the logged repair of the statement at path, still to be finished: its entry was written
// but the files were not, so the statement is still where it was. nil when there is none.
func pendingRepair(root, path string) *dyn.Dict {
	entries, _ := loadOverrideLog(root)
	for _, e := range entries {
		if entryStr(e, "kind") == ovRepairID && entryStr(e, "renamed_from") == relPath(root, path) &&
			!dyn.IsFile(filepath.Join(root, filepath.FromSlash(entryStr(e, "renamed_to")))) {
			return e
		}
	}
	return nil
}

// moving is one statement that gets a new id, and what moves with it.
type moving struct {
	st              *Statement
	was, newID      string // its file before, inside the knowledge base, and its new id
	rewritten       []string
	movedEntries    []any
	resumed         *dyn.Dict // the entry of a repair cut short that this finishes
	newPath, reason string
}

// repairCollision gives each of c.others a new id and rewrites what refers to it. It returns the files it
// wrote, what it could not repair (then nothing is written), and the references it could not attribute.
func repairCollision(root string, c idCollision) (written, problems, unattributed []string) {
	all := c.all()
	var moves []*moving
	taken := takenIDs(root, c.id[:1])
	for _, st := range c.others {
		m := &moving{st: st, was: relPath(root, st.Path)}
		if e := pendingRepair(root, st.Path); e != nil {
			m.resumed, m.newID = e, entryStr(e, "new_id")
		} else {
			// drawn as a new statement's id is, and not one another statement moving here takes
			m.newID = drawID(c.id[:1], func(id string) bool { return taken[id] }, idSource)
		}
		taken[m.newID] = true
		m.newPath = filepath.Join(filepath.Dir(st.Path), m.newID+strings.TrimPrefix(filepath.Base(st.Path), c.id))
		moves = append(moves, m)
	}
	index := func(i int) *moving {
		if i < 0 {
			return nil
		}
		for _, m := range moves {
			if m.st == all[i] {
				return m
			}
		}
		return nil
	}
	out := map[string]*Statement{} // what to write, by path

	// the references in other statements, each to the one that existed where it was written
	loaded := map[string]*Statement{}
	load := func(p string) *Statement {
		if st, ok := loaded[p]; ok {
			return st
		}
		st, _ := safeStatement(p, dyn.ReadText(p))
		loaded[p] = st
		return st
	}
	for _, p := range statementFiles(root) {
		if slices.ContainsFunc(all, func(s *Statement) bool { return s.Path == p }) || !strings.Contains(dyn.ReadText(p), c.id) {
			continue
		}
		st := load(p)
		if st == nil || !rewriteRefs(st, c.id, c.id+"-", true) {
			continue // the id is only in a mark another relation set, which is rebuilt below
		}
		i := citedOne(root, addingCommit(root, relPath(root, p)), all)
		switch m := index(i); {
		case i < 0:
			unattributed = append(unattributed, relPath(root, p))
		case m != nil && rewriteRefs(st, c.id, m.newID):
			out[p] = st
			m.rewritten = append(m.rewritten, relPath(root, p))
		}
	}
	// the override log's entries naming the id, each moved, by the repair's entry, with its statement
	if dyn.IsFile(overrideLogPath(root)) {
		for _, l := range dyn.TextLines(overrideLogPath(root)) {
			v, err := dyn.Loads(l)
			e, ok := v.(*dyn.Dict)
			if err != nil || !ok || entryStr(e, "kind") == ovRepairID || (entryStr(e, "statement") != c.id && entryStr(e, "target") != c.id) {
				continue
			}
			i := citedOne(root, entryCommit(root, l), all)
			if i < 0 {
				unattributed = append(unattributed, overrideLogName+" "+overrideRef(e))
			} else if m := index(i); m != nil {
				m.movedEntries = append(m.movedEntries, overrideRef(e))
			}
		}
	}
	// each moving statement: its id, its name, its text where it names itself, and the marks its relations
	// set on others
	for _, m := range moves {
		m.st.Meta.Set("id", m.newID)
		rewriteRefs(m.st, c.id, m.newID)
		for relation, mark := range markKey {
			target := dyn.Str(dyn.Or(m.st.Meta.Get(relation), ""))
			tp := statementPath(root, target)
			if target == "" || tp == "" {
				continue
			}
			t := out[tp]
			if t == nil {
				if t = load(tp); t == nil {
					continue
				}
			}
			stays := slices.ContainsFunc(all, func(s *Statement) bool {
				return s != m.st && !slices.ContainsFunc(moves, func(o *moving) bool { return o.st == s }) &&
					dyn.Equal(s.Meta.Get(relation), target)
			})
			if relation == "supersedes" {
				for _, o := range all {
					if o != m.st && dyn.Equal(o.Meta.Get(relation), target) {
						problems = append(problems, fmt.Sprintf("two statements given %s both supersede %s, which can "+
							"be superseded only once", c.id, target))
					}
				}
				t.Meta.Set(mark, m.newID)
			} else {
				var l []any
				for _, v := range listOf(t.Meta.Get(mark)) {
					if !dyn.Equal(v, c.id) || stays {
						l = append(l, v)
					}
				}
				if !containsEqual(l, m.newID) {
					l = append(l, m.newID)
				}
				t.Meta.Set(mark, l)
			}
			out[tp] = t
			if !slices.Contains(m.rewritten, relPath(root, tp)) {
				m.rewritten = append(m.rewritten, relPath(root, tp))
			}
		}
		m.st.Path = m.newPath
		out[m.newPath] = m.st
		m.rewritten = append([]string{relPath(root, m.newPath)}, m.rewritten...)
	}
	if len(problems) > 0 {
		return nil, dedupe(problems), nil
	}
	if c.keep.Path == moves[0].was || filepath.Clean(c.keep.Path) == filepath.Join(root, filepath.FromSlash(moves[0].was)) {
		out[c.keep.Path] = c.keep // one name for all: the kept one takes it
	}

	// the entries first, as a record of what is about to be written, then the files
	user, _ := userSource("", root)
	for _, m := range moves {
		if m.resumed != nil {
			continue
		}
		reason := "copies gave new statements the same random id; the one on " + c.label + " keeps it"
		if c.both {
			reason = "copies gave new statements the same random id, and several reached " + c.label +
				"; the one that reached it first keeps it"
		}
		e := dyn.D("id", newOverrideID(root), "kind", ovRepairID, "at", isoSeconds(overrideNow()), "by", "kb",
			"run_by", user, "reason", reason, "old_id", c.id, "new_id", m.newID, "kept", relPath(root, c.keep.Path),
			"renamed_from", m.was, "renamed_to", relPath(root, m.newPath), "shared", c.label,
			"rewritten", toAny(m.rewritten), "moved_entries", dyn.Or(m.movedEntries, []any{}))
		if len(unattributed) > 0 {
			e.Set("unattributed", toAny(unattributed))
		}
		appendLogEntry(overrideLogPath(root), e)
	}
	var paths []string
	for p := range out {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		tmp := p + ".kb-repair"
		dyn.WriteText(tmp, statementFileText(out[p]))
		if err := os.Rename(tmp, p); err != nil {
			dyn.RaiseOS(err)
		}
		written = append(written, relPath(root, p))
	}
	for _, m := range moves {
		old := filepath.Join(root, filepath.FromSlash(m.was))
		if out[old] == nil {
			if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
				dyn.RaiseOS(err)
			}
			written = append(written, m.was)
		}
	}
	return append(written, overrideLogName), nil, unattributed
}

func toAny(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

// repairIDs repairs every id statement files share, and returns what it repaired, the files it wrote and
// what it left.
func repairIDs(root string, extra []idCollision) (repaired, written, left []string) {
	cols := extra
	dups := duplicateIDs(root)
	var ids []string
	for id := range dups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var names []string
		for _, st := range dups[id] {
			names = append(names, filepath.Base(st.Path))
		}
		c, problem := chooseKeeper(root, id, dups[id])
		if problem != "" {
			left = append(left, fmt.Sprintf("%s: carried by %s, and %s", id, strings.Join(names, ", "), problem))
			continue
		}
		cols = append(cols, c)
	}
	for _, c := range cols {
		var was []string
		for _, o := range c.others {
			was = append(was, filepath.Base(o.Path))
		}
		w, problems, unattributed := repairCollision(root, c)
		if len(problems) > 0 {
			left = append(left, c.id+": "+strings.Join(problems, "; "))
			continue
		}
		for i, o := range c.others {
			repaired = append(repaired, fmt.Sprintf("%s: %s keeps it (on %s); %s is now %s", c.id,
				filepath.Base(c.keep.Path), c.label, was[i], dyn.Str(o.Meta.Get("id"))))
		}
		if len(unattributed) > 0 {
			left = append(left, fmt.Sprintf("%s: %s name it, but kb cannot tell which of the statements given it "+
				"they mean: they were made where several or none of them existed, or are not committed. They still "+
				"say %s, which %s keeps", c.id, strings.Join(unattributed, ", "), c.id, filepath.Base(c.keep.Path)))
		}
		for _, f := range w {
			if !slices.Contains(written, f) {
				written = append(written, f)
			}
		}
	}
	return repaired, written, left
}

// sameNameCollision is, for a file git left conflicted because both sides added it, the two statements it
// holds when they are different statements with one id and one name; ok is false otherwise. A side already
// moved to another id by an earlier run is not moved again: c is then empty.
func sameNameCollision(root, rel, ours, theirs string) (c idCollision, problem string, ok bool) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	a, errA := safeStatement(p, ours)
	b, errB := safeStatement(p, theirs)
	if errA != nil || errB != nil || !dyn.Equal(a.Meta.Get("id"), b.Meta.Get("id")) ||
		statementOrigin(a.Meta) == statementOrigin(b.Meta) {
		return idCollision{}, "", false
	}
	id := strings.ToUpper(dyn.Str(a.Meta.Get("id")))
	for _, q := range statementFiles(root) {
		if filepath.Clean(q) == filepath.Clean(p) {
			continue
		}
		if st, err := safeStatement(q, dyn.ReadText(q)); err == nil && !dyn.Equal(st.Meta.Get("id"), id) &&
			(statementOrigin(st.Meta) == statementOrigin(a.Meta) || statementOrigin(st.Meta) == statementOrigin(b.Meta)) {
			return idCollision{}, "", true
		}
	}
	c, problem = chooseKeeper(root, id, []*Statement{a, b})
	if problem != "" {
		return idCollision{}, problem, true
	}
	return c, "", true
}

// ---------------------------------------------------------------- reading the record through its repairs

// applyRepairs moves, in memory, the log entries a repair listed in moved_entries to the statement's new
// id, in the order the repairs were made, so that everything reading the log sees the statement where it
// is now.
func applyRepairs(entries []*dyn.Dict) {
	byRef := map[string]*dyn.Dict{}
	for _, e := range entries {
		byRef[overrideRef(e)] = e
	}
	for _, r := range entries {
		if entryStr(r, "kind") != ovRepairID {
			continue
		}
		old, nw := entryStr(r, "old_id"), entryStr(r, "new_id")
		for _, ref := range listOf(r.Get("moved_entries")) {
			if e := byRef[dyn.Str(ref)]; e != nil {
				for _, k := range []string{"statement", "target"} {
					if entryStr(e, k) == old {
						e.Set(k, nw)
					}
				}
			}
		}
	}
}

// asMade is a statement's text as it was made, before the logged repairs that rewrote ids in it (S-RFFZ):
// the repairs are undone newest first, in the order of the log, following the statement back through each
// name a repair moved it from.
func asMade(st *Statement, entries []*dyn.Dict) string {
	body, name := st.Body, filepath.Base(st.Path)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if entryStr(e, "kind") != ovRepairID {
			continue
		}
		if slices.ContainsFunc(listOf(e.Get("rewritten")), func(w any) bool { return path.Base(dyn.Str(w)) == name }) {
			body = replaceID(body, entryStr(e, "new_id"), entryStr(e, "old_id"))
		}
		if path.Base(entryStr(e, "renamed_to")) == name {
			name = path.Base(entryStr(e, "renamed_from"))
		}
	}
	return body
}
