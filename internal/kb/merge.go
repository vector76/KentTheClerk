package kb

// Resolving git conflicts in the record's files on the agent's behalf (S-HD8F): the agent never edits
// those files, so kb merges them. git calls kb as a merge driver while it merges (S-NNKA), and kb resolve
// applies the same merge to files git has already left conflicted. Neither takes text the agent merged by
// hand, and what kb cannot merge goes to the human.
//
// A statement file merges field by field. What only kb writes after the fact, the attestations, the seen
// list and the amended_by and clarified_by marks, keeps what either side added and drops what either side
// removed. Every other field, and the text, merges three ways: a change on one side is taken, and two
// different changes are a conflict kb does not guess at. The result does not depend on which side is
// ours. The logs and the user table are merged by git's own union merge.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
)

const (
	// mergeDriverName names the driver in .gitattributes and git's config (merge.kent.driver).
	mergeDriverName = "kent"
	// mergeDriverCommand is the driver as git's config holds it: git fills in the base, ours, theirs, the
	// path, and the labels it would put on conflict markers.
	mergeDriverCommand = "kb merge-driver %O %A %B %P %S %X %Y"
)

// mergeAttributes are the .gitattributes lines that send statement files to the driver.
var mergeAttributes = []string{"attested/*.md merge=" + mergeDriverName, "agent/*.md merge=" + mergeDriverName}

// accumulated are the fields that keep what either side added: lists only kb appends to after the fact.
var accumulated = map[string]bool{"attestations": true, "seen": true, "amended_by": true, "clarified_by": true}

// conflictMarkerRE is a line git writes around a conflict. A side holding one was merged by hand or by a
// text merge, and kb never takes it.
var conflictMarkerRE = regexp.MustCompile(`(?m)^(<{7}|={7}|>{7}|\|{7})( |$)`)

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
	return slices.ContainsFunc(xs, func(y any) bool { return dyn.Equal(x, y) })
}

// itemKey orders the items either side added: by when they were made, where they say (an attestation's
// at), then by their text, so that the order does not depend on which side is ours.
func itemKey(x any) (string, string) {
	if d, ok := x.(*dyn.Dict); ok {
		return dyn.Str(dyn.Or(d.Get("at"), "")), dyn.Dumps(d)
	}
	return "", dyn.Str(x)
}

// mergeAccumulated keeps the base's items that neither side removed, in the base's order, then what either
// side added, ordered by itemKey. A field one side deleted stays only if something is left in it.
func mergeAccumulated(o, a, b side) side {
	ol, al, bl := listOf(o.v), listOf(a.v), listOf(b.v)
	var out, added []any
	for _, x := range ol {
		if containsEqual(al, x) && containsEqual(bl, x) {
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
		ai, ti := itemKey(added[i])
		aj, tj := itemKey(added[j])
		return ai < aj || ai == aj && ti < tj
	})
	out = append(out, added...)
	if out == nil {
		out = []any{}
	}
	return side{out, len(out) > 0 || a.has && b.has}
}

// parseStatementText reads a statement file's text as kb does, reporting what kb cannot read instead of
// stopping. Empty text, as git gives for a base the two sides added separately, is an empty statement.
func parseStatementText(text string) (meta *dyn.Dict, body string, err error) {
	if strings.TrimSpace(text) == "" {
		return dyn.NewDict(), "", nil
	}
	if conflictMarkerRE.MatchString(dyn.Universal(text)) {
		return nil, "", fmt.Errorf("it holds conflict markers")
	}
	if !frontmatterRE.MatchString(dyn.Universal(text)) {
		return nil, "", fmt.Errorf("it has no frontmatter")
	}
	st, err := readStatement("", text)
	if err != nil {
		return nil, "", err
	}
	return st.Meta, st.Body, nil
}

// mergeStatementText merges a statement file's base, ours and theirs, or says why it cannot. The two sides
// are put in a fixed order first, so that ours and theirs swapped give the same bytes.
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
	if dyn.Universal(theirs) < dyn.Universal(ours) {
		am, ab, bm, bb = bm, bb, am, ab
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
	// a statement is either superseded or withdrawn, never both, which kb itself refuses
	if dyn.Truthy(merged.Get("superseded_by")) && dyn.Truthy(merged.Get("withdrawn_in")) {
		problems = append(problems, "one side superseded it and the other withdrew it")
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

// unionMerge is git's union merge of a log or the user table, whose entries and rows are independent
// lines (S-8MW2): git merge-file --union, which prints the result and changes nothing in git.
func unionMerge(base, ours, theirs string) (string, error) {
	dir, err := os.MkdirTemp("", "kb-union-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	var paths []string
	for i, text := range []string{ours, base, theirs} {
		p := filepath.Join(dir, fmt.Sprint(i))
		if err := os.WriteFile(p, []byte(dyn.Universal(text)), 0o644); err != nil {
			return "", err
		}
		paths = append(paths, p)
	}
	out, err := exec.Command("git", "merge-file", "--union", "-p", paths[0], paths[1], paths[2]).Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "", fmt.Errorf("git could not be run: %v", err)
		}
	}
	return string(out), nil
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
	case dir == "." && slices.Contains(unionFiles, name):
		return "lines"
	}
	return ""
}

// mergeRecordFile merges one of the record's files by its kind.
func mergeRecordFile(kind, base, ours, theirs string) (string, []string) {
	switch kind {
	case "statement":
		return mergeStatementText(base, ours, theirs)
	case "lines":
		out, err := unionMerge(base, ours, theirs)
		if err != nil {
			return "", []string{err.Error()}
		}
		return out, nil
	}
	return "", []string{"kb merges only statement files, the logs and the user table"}
}

// writeLF writes text with LF line endings, as kb writes its logs on every system.
func writeLF(path, text string) {
	dyn.CheckEncodable(text)
	if err := os.WriteFile(path, []byte(dyn.Universal(text)), 0o666); err != nil {
		dyn.RaiseOS(err)
	}
}

// writeRecordFile writes a file kb merged: a log or the user table with LF line endings, as kb appends to
// them, and a statement as kb writes statements.
func writeRecordFile(path, kind, text string) {
	if kind == "lines" {
		writeLF(path, text)
		return
	}
	dyn.WriteText(path, text)
}

// ---------------------------------------------------------------- kb merge-driver

// runMergeDriver is what git runs to merge a file .gitattributes sends to the driver: it writes the merged
// file over ours and exits 0, or leaves git's text merge there, with its conflict markers, and exits 1.
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
		if strings.Contains(a, "\r\n") {
			merged = strings.ReplaceAll(merged, "\n", "\r\n")
		}
		if err := os.WriteFile(ours, []byte(merged), 0o644); err != nil {
			fmt.Fprintf(Stderr, "kb merge-driver: cannot write %s: %v\n", path, err)
			return 1
		}
		return 0
	}
	// leave the conflict as git would have, with git's own labels, for the human
	label := func(k, dflt string) string {
		if v, _ := ns.Str(k); v != "" {
			return v
		}
		return dflt
	}
	cmd := exec.Command("git", "merge-file", "-p", "-L", label("ours_label", "ours"), "-L", label("base_label", "base"),
		"-L", label("theirs_label", "theirs"), ours, base, theirs)
	out, err := cmd.Output()
	clean := err == nil
	if len(out) > 0 {
		os.WriteFile(ours, out, 0o644)
	}
	msg := fmt.Sprintf("kb could not merge %s: %s. It is left for the human.", path, strings.Join(problems, "; "))
	if clean {
		msg += " git's text merge of it had no conflict, but kb refused it; the file holds that text merge, unchecked."
	}
	fmt.Fprintln(Stderr, msg)
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

// relInside is p as a path inside root, with forward slashes, and whether it is inside. Drive letters
// compare in any case on Windows.
func relInside(root, p string) (string, bool) {
	norm := func(s string) string {
		s = filepath.Clean(s)
		if runtime.GOOS == "windows" && len(s) >= 2 && s[1] == ':' {
			s = strings.ToUpper(s[:1]) + s[1:]
		}
		return s
	}
	r, err := filepath.Rel(norm(root), norm(p))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// askedFile is a file named on kb resolve's command line as a path inside the knowledge base: absolute,
// relative to the current folder, or relative to the knowledge base, the first that git reports conflicted.
func askedFile(root, f string, conflicted map[string]bool) (string, bool) {
	var cands []string
	if filepath.IsAbs(f) {
		cands = append(cands, f)
	} else {
		if cwd, err := os.Getwd(); err == nil {
			cands = append(cands, filepath.Join(cwd, f))
		}
		cands = append(cands, filepath.Join(root, f))
	}
	for _, c := range cands {
		if r, ok := relInside(root, c); ok && conflicted[r] {
			return r, true
		}
	}
	return "", false
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
			p, ok := askedFile(root, f, isConflicted)
			if !ok {
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
				switch {
				case problem != "":
					left = append(left, fmt.Sprintf("%s: two statements were given one id and name, and %s", f, problem))
				case c.id != "":
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
		writeRecordFile(filepath.Join(root, filepath.FromSlash(f)), kind, merged)
		resolved = append(resolved, f)
	}
	// then make the record whole: no two statements with one id (S-W5AJ)
	repaired, written, unrepaired := repairIDs(root, collisions)
	left = append(left, unrepaired...)
	for _, f := range written {
		if !slices.Contains(resolved, f) {
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
			"finish any merge or rebase, commit them as the session-start text says for this knowledge base, " +
			"and run kb check.")
		refreshIndex(root)
	} else if len(left) == 0 {
		println("Nothing to resolve: git reports no conflicted files in " + root + ", and no two statements share an id.")
	}
	if len(left) > 0 {
		println("Left for the human; do not merge or edit these by hand:")
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
	d.Positional("base_label", argparse.Optional, "the label git gives the base's conflict marker (%S)")
	d.Positional("ours_label", argparse.Optional, "the label of ours (%X)")
	d.Positional("theirs_label", argparse.Optional, "the label of theirs (%Y)")
	d.Run = runMergeDriver

	r := sub.AddParser("resolve", "merge the record's files git left conflicted, and repair ids two statements share")
	r.Positional("files", argparse.ZeroMore, "conflicted files to resolve: absolute, or relative to the current "+
		"folder or the knowledge base (default: every conflicted file)")
	r.Run = runResolve
}
