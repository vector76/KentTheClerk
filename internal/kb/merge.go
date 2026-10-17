package kb

// Resolving git conflicts in the record's files on the agent's behalf (S-HD8F): the agent never edits
// those files, so kb merges them. git calls kb as a merge driver while it merges (S-NNKA), and kb resolve
// applies the same merge to files git has already left conflicted. Neither takes text the agent merged by
// hand, and what kb cannot merge goes to the human.
//
// A statement file merges field by field. What only kb writes after the fact, the attestations, the seen
// list and the amended_by and clarified_by marks, keeps what either side added and drops what either side
// removed. Every other field, and the text, merges three ways: a change on one side is taken, and two
// different changes are a conflict kb does not guess at. The logs and the user table keep both sides'
// lines, as git's union merge does.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
)

const (
	// mergeDriverName names the driver in .gitattributes and git's config (merge.kent.driver).
	mergeDriverName = "kent"
	// mergeDriverCommand is the driver as git's config holds it: git fills in the base, ours, theirs and the path.
	mergeDriverCommand = "kb merge-driver %O %A %B %P"
)

// mergeAttributes are the .gitattributes lines that send statement files to the driver.
var mergeAttributes = []string{"attested/*.md merge=" + mergeDriverName, "agent/*.md merge=" + mergeDriverName}

// accumulated are the fields that keep what either side added: lists only kb appends to after the fact.
var accumulated = map[string]bool{"attestations": true, "seen": true, "amended_by": true, "clarified_by": true}

// side is one version of a field: its value and whether the field is there at all.
type side struct {
	v   any
	has bool
}

func fieldOf(d *dyn.Dict, k string) side { v, ok := d.Lookup(k); return side{v, ok} }

func (a side) same(b side) bool { return a.has == b.has && (!a.has || dyn.Equal(a.v, b.v)) }

// merge3 is a three-way merge of one value: one side's change is taken; two different changes conflict.
func merge3(o, a, b side) (side, bool) {
	switch {
	case a.same(b), o.same(b):
		return a, true
	case o.same(a):
		return b, true
	}
	return side{}, false
}

func containsEqual(xs []any, x any) bool {
	for _, y := range xs {
		if dyn.Equal(x, y) {
			return true
		}
	}
	return false
}

// mergeAccumulated keeps the base's items that neither side removed, then what either side added, the
// additions ordered by when they were made where they say (an attestation's at), so that the result does
// not depend on which side is ours.
func mergeAccumulated(o, a, b side) side {
	ol, al, bl := listOf(o.v), listOf(a.v), listOf(b.v)
	var out, added []any
	for _, x := range al {
		if containsEqual(ol, x) && containsEqual(bl, x) {
			out = append(out, x)
		}
	}
	for _, l := range [][]any{al, bl} {
		for _, x := range l {
			if !containsEqual(ol, x) && !containsEqual(added, x) {
				added = append(added, x)
			}
		}
	}
	sort.SliceStable(added, func(i, j int) bool {
		di, oki := added[i].(*dyn.Dict)
		dj, okj := added[j].(*dyn.Dict)
		return oki && okj && dyn.Str(dyn.Or(di.Get("at"), "")) < dyn.Str(dyn.Or(dj.Get("at"), ""))
	})
	out = append(out, added...)
	if out == nil {
		out = []any{}
	}
	return side{out, a.has || b.has || len(out) > 0}
}

// parseStatementText reads a statement file's text as kb does, reporting what kb cannot read instead of
// stopping. Empty text, as git gives for a base the two sides added separately, is an empty statement.
func parseStatementText(text string) (meta *dyn.Dict, body string, err error) {
	if strings.TrimSpace(text) == "" {
		return dyn.NewDict(), "", nil
	}
	if !frontmatterRE.MatchString(text) {
		return nil, "", fmt.Errorf("it has no frontmatter, or holds conflict markers")
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("its frontmatter cannot be read")
		}
	}()
	quiet := Stderr
	Stderr = &bytes.Buffer{}
	defer func() { Stderr = quiet }()
	meta, body = parseFrontmatter(text)
	return meta, strings.Trim(body, "\n"), nil
}

// mergeStatementText merges a statement file's base, ours and theirs, or says why it cannot.
func mergeStatementText(base, ours, theirs string) (string, []string) {
	var problems []string
	om, ob, err := parseStatementText(base)
	if err != nil {
		problems = append(problems, "the base cannot be read: "+err.Error())
	}
	am, ab, err := parseStatementText(ours)
	if err != nil {
		problems = append(problems, "our side cannot be read: "+err.Error())
	}
	bm, bb, err := parseStatementText(theirs)
	if err != nil {
		problems = append(problems, "their side cannot be read: "+err.Error())
	}
	if len(problems) > 0 {
		return "", problems
	}
	if !dyn.Truthy(am.Get("id")) || !dyn.Equal(am.Get("id"), bm.Get("id")) {
		return "", []string{fmt.Sprintf("the two sides are different statements (%s and %s)",
			dyn.Str(dyn.Or(am.Get("id"), "no id")), dyn.Str(dyn.Or(bm.Get("id"), "no id")))}
	}
	if strings.TrimSpace(base) == "" && statementOrigin(am) != statementOrigin(bm) {
		return "", []string{fmt.Sprintf("the two sides are different statements that were given the same id, %s, "+
			"and the same file name; kb resolve gives one of them a new id", dyn.Str(am.Get("id")))}
	}
	var keys []string
	seen := map[string]bool{}
	for _, d := range []*dyn.Dict{am, bm, om} {
		for _, k := range d.Keys() {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	merged := dyn.NewDict()
	for _, k := range keys {
		o, a, b := fieldOf(om, k), fieldOf(am, k), fieldOf(bm, k)
		var v side
		if accumulated[k] {
			v = mergeAccumulated(o, a, b)
		} else {
			var ok bool
			if v, ok = merge3(o, a, b); !ok {
				problems = append(problems, "the two sides changed "+k+" differently")
				continue
			}
		}
		if v.has {
			merged.Set(k, v.v)
		}
	}
	body, ok := merge3(side{ob, true}, side{ab, true}, side{bb, true})
	if !ok {
		problems = append(problems, "the two sides changed the statement's text differently")
	}
	if len(problems) > 0 {
		return "", problems
	}
	return dumpFrontmatter(merged) + "\n" + dyn.Str(body.v) + "\n", nil
}

// mergeLinesText keeps ours and adds what theirs added, as git's union merge does: the logs' entries and
// the user table's rows are independent lines (S-8MW2).
func mergeLinesText(base, ours, theirs string) string {
	lines := func(s string) []string {
		var out []string
		for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	have := map[string]bool{}
	for _, l := range lines(base) {
		have[l] = true
	}
	var out []string
	for _, l := range lines(ours) {
		out = append(out, l)
		have[l] = true
	}
	for _, l := range lines(theirs) {
		if !have[l] {
			out = append(out, l)
			have[l] = true
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// recordFileKind says how kb merges the file at rel, a path inside the knowledge base: "statement",
// "lines", or "" for a file kb does not merge.
func recordFileKind(rel string) string {
	rel = filepath.ToSlash(rel)
	dir, name := filepath.ToSlash(filepath.Dir(rel)), filepath.Base(rel)
	switch {
	case dir == "attested" && strings.HasPrefix(name, "S-") && strings.HasSuffix(name, ".md"),
		dir == "agent" && strings.HasPrefix(name, "A-") && strings.HasSuffix(name, ".md"):
		return "statement"
	case dir == "." && containsStr(unionFiles, name):
		return "lines"
	}
	return ""
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// mergeRecordFile merges one of the record's files by its kind.
func mergeRecordFile(kind, base, ours, theirs string) (string, []string) {
	switch kind {
	case "statement":
		return mergeStatementText(base, ours, theirs)
	case "lines":
		return mergeLinesText(base, ours, theirs), nil
	}
	return "", []string{"kb merges only statement files, the logs and the user table"}
}

// ---------------------------------------------------------------- kb merge-driver

// runMergeDriver is what git runs to merge a file .gitattributes sends to the driver: it writes the merged
// file over ours and exits 0, or leaves git's conflict markers there and exits 1.
func runMergeDriver(ns argparse.Namespace) int {
	base, _ := ns.Str("base")
	ours, _ := ns.Str("ours")
	theirs, _ := ns.Str("theirs")
	path, _ := ns.Str("path")
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	o, a, b := read(base), read(ours), read(theirs)
	kind := recordFileKind(path)
	if kind == "" {
		// the path git gives is from the repository's top; inside a code repository, look below it
		parts := strings.Split(filepath.ToSlash(path), "/")
		for i := 1; i < len(parts) && kind == ""; i++ {
			kind = recordFileKind(strings.Join(parts[i:], "/"))
		}
	}
	merged, problems := mergeRecordFile(kind, o, a, b)
	if len(problems) == 0 {
		if err := os.WriteFile(ours, []byte(merged), 0o644); err != nil {
			fmt.Fprintf(Stderr, "kb merge-driver: cannot write %s: %v\n", path, err)
			return 1
		}
		return 0
	}
	// leave the conflict as git would have, for the human
	out, _ := exec.Command("git", "merge-file", "-p", "-L", "ours", "-L", "base", "-L", "theirs", ours, base, theirs).Output()
	if len(out) > 0 {
		os.WriteFile(ours, out, 0o644)
	}
	fmt.Fprintf(Stderr, "kb could not merge %s: %s. It is left for the human.\n", path, strings.Join(problems, "; "))
	return 1
}

// ---------------------------------------------------------------- kb resolve

// gitBlob is a file's content at a stage of git's index (1 base, 2 ours, 3 theirs), and whether it is there.
func gitBlob(root, rel string, stage int) (string, bool) {
	cmd := exec.Command("git", "-C", root, "show", fmt.Sprintf(":%d:./%s", stage, filepath.ToSlash(rel)))
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// conflictedFiles are the knowledge base's files git reports as conflicted, as paths inside it.
func conflictedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-u", "-z", "--", ".")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cannot list the conflicted files in %s", root)
	}
	set := map[string]bool{}
	for _, e := range strings.Split(string(out), "\x00") {
		if _, p, ok := strings.Cut(e, "\t"); ok {
			set[filepath.ToSlash(p)] = true
		}
	}
	var files []string
	for p := range set {
		files = append(files, p)
	}
	sort.Strings(files)
	return files, nil
}

func runResolve(ns argparse.Namespace) int {
	root := repoRoot()
	conflicted, err := conflictedFiles(root)
	if err != nil {
		die(err.Error())
	}
	isConflicted := map[string]bool{}
	for _, f := range conflicted {
		isConflicted[f] = true
	}
	files := conflicted
	if asked := ns.List("files"); len(asked) > 0 {
		files = nil
		for _, f := range asked {
			p := f
			if filepath.IsAbs(p) {
				if r, err := filepath.Rel(root, p); err == nil {
					p = r
				}
			}
			p = filepath.ToSlash(filepath.Clean(p))
			if !isConflicted[p] {
				die(fmt.Sprintf("git does not report %s as conflicted in %s; kb resolve merges only files git left "+
					"conflicted", f, root))
			}
			files = append(files, p)
		}
	}
	var resolved, left []string
	var collisions []idCollision
	for _, f := range files {
		kind := recordFileKind(f)
		base, okO := gitBlob(root, f, 1)
		ours, okA := gitBlob(root, f, 2)
		theirs, okB := gitBlob(root, f, 3)
		if kind == "statement" && !okO && okA && okB {
			// both sides added a file of this name: perhaps two statements given one id and one title
			if c, problem, ok := sameNameCollision(root, f, ours, theirs); ok {
				if problem != "" {
					left = append(left, fmt.Sprintf("%s: two statements were given one id and name, and %s", f, problem))
				} else {
					collisions = append(collisions, c)
				}
				continue
			}
		}
		var merged string
		var problems []string
		switch {
		case kind == "":
			problems = []string{"kb merges only statement files, the logs and the user table"}
		case !okA || !okB:
			problems = []string{"one side deleted it"}
		default:
			merged, problems = mergeRecordFile(kind, base, ours, theirs)
		}
		if len(problems) > 0 {
			left = append(left, fmt.Sprintf("%s: %s", f, strings.Join(problems, "; ")))
			continue
		}
		dyn.WriteText(filepath.Join(root, filepath.FromSlash(f)), merged)
		resolved = append(resolved, f)
	}
	// then make the record whole: no two statements with one id (S-W5AJ)
	repaired, written, unrepaired := repairIDs(root, collisions)
	left = append(left, unrepaired...)
	for _, f := range written {
		if !containsStr(resolved, f) {
			resolved = append(resolved, f)
		}
	}
	if len(repaired) > 0 {
		println("Repaired id collisions, each entered in " + overrideLogName + ":")
		for _, r := range repaired {
			println("  " + r)
		}
	}
	if len(resolved) > 0 {
		println("Written by kb:")
		for _, f := range resolved {
			println("  " + f)
		}
		println("Stage them with `" + gitPrefix(root) + " add -A -- " + strings.Join(resolved, " ") + "`, then " +
			"finish any merge or rebase, and run kb check.")
		refreshIndex(root)
	} else if len(left) == 0 {
		println("Nothing to resolve: git reports no conflicted files in " + root + ", and no two statements share an id.")
	}
	if len(left) > 0 {
		println("Not resolved, so left for the human; do not merge them by hand:")
		for _, l := range left {
			println("  " + l)
		}
		return 1
	}
	return 0
}

func addMergeParsers(sub *argparse.Parser) {
	d := sub.AddParser("merge-driver", "merge a statement file or log during a git merge (git runs this; see kb setup)")
	d.Positional("base", argparse.Single, "the common ancestor's version (%O)")
	d.Positional("ours", argparse.Single, "our version, overwritten with the result (%A)")
	d.Positional("theirs", argparse.Single, "their version (%B)")
	d.Positional("path", argparse.Optional, "the file's path in the repository (%P)")
	d.Run = runMergeDriver

	r := sub.AddParser("resolve", "merge the record's files git left conflicted, and repair ids two statements share")
	r.Positional("files", argparse.ZeroMore, "conflicted files to resolve (default: every conflicted file)")
	r.Run = runResolve
}
