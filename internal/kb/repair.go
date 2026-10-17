package kb

// Repairing a random id collision (S-W5AJ): two copies gave new statements the same id, and a merge brought
// both into one record. The merge driver never sees it, since the two are usually different files, so kb
// resolve looks across the record for duplicate ids. The statement already on the shared history keeps its
// id; the other gets a new one, and every reference to it is rewritten, the text included, where only the
// id changes. It is a repair by kb, entered in the override log so that it can be traced, and not an
// override: it needs no confirmation or human turn.
//
// The shared history is the first of these refs that holds exactly one of the two statements: the
// upstream branch, origin's default branch, the side being merged in (MERGE_HEAD), and the second parent of
// a merge just made. A reference belongs to the statement that keeps its id when its file is on that ref,
// since a statement's references are fixed when it is made; any other reference was made in the unshared
// work, where only the other statement existed. The marks the two statements' own relations set on others
// are rebuilt from those relations.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"kenttheclerk/internal/dyn"
)

// ovRepairID is the log entry for a repaired id collision: not an override, so kb check reads it apart
// from the list.
const ovRepairID = "repair-id"

// sharedRefs are the refs that can show which of two colliding statements is on the shared history, in the
// order kb tries them.
var sharedRefs = []string{"@{u}", "refs/remotes/origin/HEAD", "MERGE_HEAD", "HEAD^2"}

// notRewritten are the frontmatter fields an id is never rewritten in: the statement's own id, which moves
// on its own; the marks others' relations set on it (superseded_by and the like), which are rebuilt from
// those relations; and the attestations, the seen list and the citations, which hold turns, not ids. Every
// other field, its relations (rests_on, supersedes, amends, clarifies, context_for), title and answers
// among them, is rewritten where it names the id.
var notRewritten = map[string]bool{"id": true, "superseded_by": true, "amended_by": true, "clarified_by": true,
	extraContextKey: true, "attestations": true, "seen": true, "cites": true}

// statementOrigin tells two statements with one id apart: when it was made and the turn it was made in.
func statementOrigin(meta *dyn.Dict) string {
	turn := dyn.Str(dyn.Or(meta.Get("made_in"), ""))
	if atts := listOf(meta.Get("attestations")); len(atts) > 0 {
		if d, ok := atts[0].(*dyn.Dict); ok {
			turn = dyn.Str(dyn.Or(d.Get("turn"), ""))
		}
	}
	return madeAt(meta) + "|" + turn
}

// safeStatement reads a statement file's text, or says why it cannot.
func safeStatement(path, text string) (*Statement, error) {
	meta, body, err := parseStatementText(text)
	if err != nil {
		return nil, err
	}
	return &Statement{path, meta, body}, nil
}

func statementFileText(st *Statement) string { return dumpFrontmatter(st.Meta) + "\n" + st.Body + "\n" }

// idCollision is two statements with one id: keep is on the shared history and keeps it; other gets a new
// one. For two files of one name, both have the same Path.
type idCollision struct {
	id          string
	keep, other *Statement
	ref         string // the commit that showed which is shared
	label       string // that ref as the log names it: its name and commit
}

// atRef is the file at rel inside the knowledge base on ref, if there is one.
func atRef(root, ref, rel string) (string, bool) {
	out, err := exec.Command("git", "-C", root, "show", ref+":./"+filepath.ToSlash(rel)).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// onRef is whether ref holds the statement st, at its path and with its origin.
func onRef(root, ref string, st *Statement) bool {
	rel, err := filepath.Rel(root, st.Path)
	if err != nil {
		return false
	}
	text, ok := atRef(root, ref, rel)
	if !ok {
		return false
	}
	there, err := safeStatement(st.Path, text)
	return err == nil && dyn.Equal(there.Meta.Get("id"), st.Meta.Get("id")) && statementOrigin(there.Meta) == statementOrigin(st.Meta)
}

func refExists(root, ref string) bool {
	return exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", ref+"^{commit}").Run() == nil
}

// sharedOf decides which of a and b is on the shared history: the first ref that holds exactly one of
// them. When a ref holds both, both have been shared and others may cite either, so kb does not choose.
func sharedOf(root string, a, b *Statement) (keep, other *Statement, ref, label, problem string) {
	if gitTop(root) == "" {
		return nil, nil, "", "", "the knowledge base is not in git, so kb cannot tell which of them was shared"
	}
	for _, r := range sharedRefs {
		if !refExists(root, r) {
			continue
		}
		ina, inb := onRef(root, r, a), onRef(root, r, b)
		if ina && inb {
			return nil, nil, "", "", "both are on " + r + ", so both have been shared and others may cite either"
		}
		if ina || inb {
			sha, _ := git(root, "rev-parse", "--short", r+"^{commit}")
			name := r
			if n, st := git(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", r); st == 0 && n != "" && r == "@{u}" {
				name = n
			}
			label = name + " (" + sha + ")"
			if ina {
				return a, b, sha, label, ""
			}
			return b, a, sha, label, ""
		}
	}
	return nil, nil, "", "", "no shared history holds either of them (" + strings.Join(sharedRefs, ", ") + ")"
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

// rewriteForward rewrites old to new in a statement's references and its text, and says whether anything
// changed. Only the id changes.
func rewriteForward(st *Statement, old, new string) bool {
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
				x[i] = walk(x[i])
			}
		case *dyn.Dict:
			for _, k := range x.Keys() {
				x.Set(k, walk(x.Get(k)))
			}
		}
		return v
	}
	for _, k := range st.Meta.Keys() {
		if !notRewritten[k] {
			st.Meta.Set(k, walk(st.Meta.Get(k)))
		}
	}
	if b := replaceID(st.Body, old, new); b != st.Body {
		st.Body, changed = b, true
	}
	return changed
}

// repairCollision gives c.other a new id and rewrites what refers to it. It returns the files it wrote,
// what it could not repair, and the log entry's fields.
func repairCollision(root string, c idCollision) (written []string, problems []string) {
	kind := c.id[:1]
	newID := makeStatementID(root, kind)
	oldPath := c.other.Path
	newPath := filepath.Join(filepath.Dir(oldPath), newID+strings.TrimPrefix(filepath.Base(oldPath), c.id))
	rel := func(p string) string { r, _ := filepath.Rel(root, p); return filepath.ToSlash(r) }

	// the statement itself: its id, its name, and its text where it names itself
	x := c.other
	x.Meta.Set("id", newID)
	rewriteForward(x, c.id, newID)
	x.Path = newPath
	bodies := map[string]*Statement{newPath: x}

	// the others' references, and the marks the two statements' relations set on them
	loaded := map[string]*Statement{}
	load := func(p string) *Statement {
		if st := loaded[p]; st != nil {
			return st
		}
		st, err := safeStatement(p, dyn.ReadText(p))
		if err != nil {
			return nil
		}
		loaded[p] = st
		return st
	}
	changed := map[string]bool{}
	for _, p := range statementFiles(root) {
		if p == c.keep.Path || p == oldPath {
			continue
		}
		if !strings.Contains(dyn.ReadText(p), c.id) {
			continue
		}
		st := load(p)
		if st == nil || onRef(root, c.ref, st) {
			continue
		}
		if rewriteForward(st, c.id, newID) {
			changed[p] = true
		}
	}
	for relation, mark := range markKey {
		target := dyn.Str(dyn.Or(x.Meta.Get(relation), ""))
		if target == "" {
			continue
		}
		tp := statementPath(root, target)
		if tp == "" {
			continue
		}
		t := load(tp)
		if t == nil {
			continue
		}
		keepToo := dyn.Equal(c.keep.Meta.Get(relation), target)
		if relation == "supersedes" {
			if keepToo {
				problems = append(problems, fmt.Sprintf("%s and %s both supersede %s, which can be superseded only once", c.id, newID, target))
				continue
			}
			t.Meta.Set(mark, newID)
		} else {
			var l []any
			for _, v := range listOf(t.Meta.Get(mark)) {
				if !dyn.Equal(v, c.id) || keepToo {
					l = append(l, v)
				}
			}
			if !containsEqual(l, newID) {
				l = append(l, newID)
			}
			t.Meta.Set(mark, l)
		}
		changed[tp] = true
	}
	if len(problems) > 0 {
		return nil, problems
	}

	// the override log: entries made in the unshared work that name the statement
	logPath := overrideLogPath(root)
	logChanged := false
	var lines []string
	if dyn.IsFile(logPath) {
		shared := map[string]bool{}
		if text, ok := atRef(root, c.ref, overrideLogName); ok {
			for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
				shared[l] = true
			}
		}
		for _, l := range dyn.TextLines(logPath) {
			if !shared[l] && strings.Contains(l, c.id) {
				if v, err := dyn.Loads(l); err == nil {
					if e, ok := v.(*dyn.Dict); ok {
						for _, k := range []string{"statement", "target"} {
							if entryStr(e, k) == c.id {
								e.Set(k, newID)
								logChanged = true
							}
						}
						l = dyn.Dumps(e)
					}
				}
			}
			lines = append(lines, l)
		}
	}

	// write: the renamed statement, the others, then the log with any revised text's hash brought along
	all := map[string]*Statement{}
	for p := range changed {
		all[p] = loaded[p]
	}
	for p, st := range bodies {
		all[p] = st
	}
	revisedHash := map[string]string{}
	for _, st := range all {
		if rv := st.Meta.Get("revised"); dyn.Truthy(rv) {
			revisedHash[markRef(rv)] = sha256Tag(dyn.Strip(st.Body))
		}
	}
	if len(revisedHash) > 0 && len(lines) > 0 {
		for i, l := range lines {
			if v, err := dyn.Loads(l); err == nil {
				if e, ok := v.(*dyn.Dict); ok && entryStr(e, "kind") == ovRevise {
					if h, ok := revisedHash[overrideRef(e)]; ok && entryStr(e, "body_sha256") != h {
						e.Set("body_sha256", h)
						lines[i] = dyn.Dumps(e)
						logChanged = true
					}
				}
			}
		}
	}
	var paths []string
	for p := range all {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		dyn.WriteText(p, statementFileText(all[p]))
		written = append(written, rel(p))
	}
	if oldPath != newPath && oldPath != c.keep.Path {
		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			dyn.RaiseOS(err)
		}
		written = append(written, rel(oldPath))
	}
	if oldPath == c.keep.Path {
		dyn.WriteText(c.keep.Path, statementFileText(c.keep))
		written = append(written, rel(c.keep.Path))
	}
	if logChanged {
		dyn.WriteText(logPath, strings.Join(lines, "\n")+"\n")
		written = append(written, overrideLogName)
	}

	user, _ := userSource("", root)
	e := dyn.D("id", newOverrideID(root), "kind", ovRepairID, "at", isoSeconds(overrideNow()), "by", "kb",
		"run_by", user, "reason", "two copies gave new statements the same random id; the one on "+c.label+
			" keeps it", "old_id", c.id, "new_id", newID, "kept", rel(c.keep.Path), "renamed_from", rel(oldPath),
		"renamed_to", rel(newPath), "shared", c.label, "rewritten", toAny(written))
	appendLogEntry(logPath, e)
	if !containsStr(written, overrideLogName) {
		written = append(written, overrideLogName)
	}
	return written, nil
}

func toAny(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

// repairIDs repairs every id two statement files carry, and returns what it wrote and what it left.
func repairIDs(root string, extra []idCollision) (repaired, written, left []string) {
	cols := extra
	dups := duplicateIDs(root)
	var ids []string
	for id := range dups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sts := dups[id]
		if len(sts) > 2 {
			left = append(left, fmt.Sprintf("%s: %d statements carry it; kb repairs two at a time", id, len(sts)))
			continue
		}
		keep, other, ref, label, problem := sharedOf(root, sts[0], sts[1])
		if problem != "" {
			left = append(left, fmt.Sprintf("%s: carried by %s and %s, and %s", id, filepath.Base(sts[0].Path),
				filepath.Base(sts[1].Path), problem))
			continue
		}
		cols = append(cols, idCollision{id, keep, other, ref, label})
	}
	for _, c := range cols {
		was := filepath.Base(c.other.Path)
		w, problems := repairCollision(root, c)
		if len(problems) > 0 {
			left = append(left, c.id+": "+strings.Join(problems, "; "))
			continue
		}
		repaired = append(repaired, fmt.Sprintf("%s: %s keeps it (on %s); %s is now %s", c.id, filepath.Base(c.keep.Path),
			c.label, was, dyn.Str(c.other.Meta.Get("id"))))
		for _, f := range w {
			if !containsStr(written, f) {
				written = append(written, f)
			}
		}
	}
	return repaired, written, left
}

// sameNameCollision is, for a file git left conflicted because both sides added it, the two statements it
// holds when they are different statements with one id and one name; ok is false otherwise.
func sameNameCollision(root, rel, ours, theirs string) (c idCollision, problem string, ok bool) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	a, errA := safeStatement(p, ours)
	b, errB := safeStatement(p, theirs)
	if errA != nil || errB != nil || !dyn.Equal(a.Meta.Get("id"), b.Meta.Get("id")) ||
		statementOrigin(a.Meta) == statementOrigin(b.Meta) {
		return idCollision{}, "", false
	}
	keep, other, ref, label, problem := sharedOf(root, a, b)
	if problem != "" {
		return idCollision{}, problem, true
	}
	return idCollision{strings.ToUpper(dyn.Str(a.Meta.Get("id"))), keep, other, ref, label}, "", true
}
