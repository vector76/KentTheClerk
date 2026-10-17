package kb

// Resolving git conflicts in the record's files on the agent's behalf (S-HD8F): the agent never edits
// those files, so kb merges them. git calls kb as a merge driver while it merges (S-NNKA), and kb resolve
// applies the same merge to files git has already left conflicted. Neither takes text the agent merged by
// hand, and what kb cannot merge goes to the human.
//
// A statement file merges field by field. What only kb writes after the fact, the attestations, the seen
// list and the amended_by and clarified_by marks, keeps what either side added and drops what either side
// removed. Every other field, and the text, merges three ways: a change on one side is taken, and two
// different changes are a conflict kb does not guess at. A change to the text, the title or the answers
// line on one side, met by anything added to the statement on the other, is left to the human too: the
// addition was made to the text as it was. The result does not depend on which side is ours, line endings
// included. The logs and the user table are merged by git's own union merge.

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
	// path, the labels it would put on conflict markers, and the markers' length.
	mergeDriverCommand = "kb merge-driver %O %A %B %P %S %X %Y %L"
)

// mergeAttributes are the .gitattributes lines that send statement files to the driver.
var mergeAttributes = []string{"attested/*.md merge=" + mergeDriverName, "agent/*.md merge=" + mergeDriverName}

// accumulated are the fields that keep what either side added: lists only kb appends to after the fact.
var accumulated = map[string]bool{"attestations": true, "seen": true, "amended_by": true, "clarified_by": true}

// conflictStartRE and conflictEndRE are the first and last lines git writes around a conflict, seven or
// more characters long (git writes more for a nested conflict, or where conflict-marker-size says). Text
// holding both was merged by hand or by a text merge, and kb never takes it; a lone line of = signs, as a
// Markdown heading underline, is not a conflict.
var (
	conflictStartRE = regexp.MustCompile(`(?m)^<{7,}( |$)`)
	conflictEndRE   = regexp.MustCompile(`(?m)^>{7,}( |$)`)
)

// hasConflictMarkers is whether text holds a conflict git wrote.
func hasConflictMarkers(text string) bool {
	return conflictStartRE.MatchString(text) && conflictEndRE.MatchString(text)
}

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

// itemKey orders items: by when they were made, where they say (an attestation's at), then by their text.
func itemKey(x any) (string, string) {
	if d, ok := x.(*dyn.Dict); ok {
		return dyn.Str(dyn.Or(d.Get("at"), "")), dyn.Dumps(d)
	}
	return "", dyn.Str(x)
}

func byItemKey(xs []any) {
	sort.SliceStable(xs, func(i, j int) bool {
		ai, ti := itemKey(xs[i])
		aj, tj := itemKey(xs[j])
		return ai < aj || ai == aj && ti < tj
	})
}

// mergeAccumulated keeps the base's items that neither side removed and what either side added. A list of
// items that say when they were made (attestations, seen) is ordered by that, the whole of it, so that an
// item one side edited in place keeps its place, the author's attestation first; such items are one per
// turn, and attestations one per person (Kent Rule 26), the earliest kept. A list of ids keeps the base's
// order, then the additions in order. A field one side deleted from the base stays deleted unless
// something is left in it; one a side added, even empty, is kept.
func mergeAccumulated(k string, o, a, b side) side {
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
	byItemKey(added)
	out = append(out, added...)
	if slices.ContainsFunc(out, func(x any) bool { _, ok := x.(*dyn.Dict); return ok }) {
		byItemKey(out)
		turns, people := map[string]bool{}, map[string]bool{}
		var kept []any
		for _, x := range out {
			d, ok := x.(*dyn.Dict)
			if ok {
				turn, by := dyn.Str(dyn.Or(d.Get("turn"), "")), dyn.Str(dyn.Or(d.Get("by"), ""))
				if turn != "" && turns[turn] || k == "attestations" && by != "" && people[by] {
					continue
				}
				turns[turn], people[by] = true, true
			}
			kept = append(kept, x)
		}
		out = kept
	}
	if out == nil {
		out = []any{}
	}
	return side{out, len(out) > 0 || a.has && b.has || !o.has && (a.has || b.has)}
}

// parseStatementText reads a statement file's text as kb does, reporting what kb cannot read instead of
// stopping. Empty text, as git gives for a base the two sides added separately, is an empty statement.
func parseStatementText(text string) (meta *dyn.Dict, body string, err error) {
	if strings.TrimSpace(text) == "" {
		return dyn.NewDict(), "", nil
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

// changedText is whether a side changed the text, the title or the answers line from the base's.
func changedText(om *dyn.Dict, ob string, m *dyn.Dict, b string) bool {
	return b != ob || !fieldOf(m, "title").same(fieldOf(om, "title")) || !fieldOf(m, "answers").same(fieldOf(om, "answers"))
}

// addedTo is whether a side added anything to the statement after the fact: an attestation, a seen turn,
// an amendment or clarification mark, or a supersession.
func addedTo(om, m *dyn.Dict) bool {
	for k := range accumulated {
		for _, x := range listOf(m.Get(k)) {
			if !containsEqual(listOf(om.Get(k)), x) {
				return true
			}
		}
	}
	return dyn.Truthy(m.Get("superseded_by")) && !dyn.Equal(m.Get("superseded_by"), om.Get("superseded_by"))
}

// mergeStatementText merges a statement file's base, ours and theirs, or says why it cannot. The two sides
// are put in a fixed order first, so that ours and theirs swapped give the same bytes.
func mergeStatementText(base, ours, theirs string) (string, []string) {
	switch {
	case strings.TrimSpace(ours) == "" && strings.TrimSpace(theirs) == "":
		return "", nil
	case strings.TrimSpace(ours) == "" || strings.TrimSpace(theirs) == "":
		return "", []string{"one side is empty, as when it deleted the file"}
	}
	var problems []string
	for _, v := range []struct{ name, text string }{{"the base", base}, {"our side", ours}, {"their side", theirs}} {
		if hasConflictMarkers(dyn.Universal(v.text)) {
			problems = append(problems, v.name+" holds conflict markers")
		}
	}
	om, ob, err := parseStatementText(base)
	if err != nil && len(problems) == 0 {
		problems = append(problems, "the base cannot be read: "+err.Error())
	}
	am, ab, err := parseStatementText(ours)
	if err != nil && len(problems) == 0 {
		problems = append(problems, "our side cannot be read: "+err.Error())
	}
	bm, bb, err := parseStatementText(theirs)
	if err != nil && len(problems) == 0 {
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
		return "", []string{"both sides added it as two different statements: " +
			collisionText(dyn.Str(am.Get("id")), []string{"ours", "theirs"})}
	}
	if strings.TrimSpace(base) != "" && (changedText(om, ob, am, ab) && addedTo(om, bm) || changedText(om, ob, bm, bb) && addedTo(om, am)) {
		return "", []string{"one side changed the statement's text, title or answers line, and the other added to " +
			"the statement as it was, an attestation, a seen turn or a mark"}
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
			v = mergeAccumulated(k, o, a, b)
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
// lines (S-8MW2): git merge-file --union, which prints the result and changes nothing in git. git exits
// 255, or not at all, when it cannot merge, as for binary input, and that is an error.
func unionMerge(base, ours, theirs string) (string, error) {
	out, code, err := mergeFile([]string{"--union"}, ours, base, theirs)
	if err != nil {
		return "", err
	}
	if code < 0 || code >= 255 {
		return "", fmt.Errorf("git merge-file could not merge it")
	}
	return out, nil
}

// mergeFile runs git merge-file -p with extra flags on the three texts, and returns its output and exit
// status: the number of conflicts, or 255 when it cannot merge.
func mergeFile(flags []string, ours, base, theirs string) (string, int, error) {
	dir, err := os.MkdirTemp("", "kb-merge-")
	if err != nil {
		return "", -1, err
	}
	defer os.RemoveAll(dir)
	var paths []string
	for i, text := range []string{ours, base, theirs} {
		p := filepath.Join(dir, fmt.Sprint(i))
		if err := os.WriteFile(p, []byte(dyn.Universal(text)), 0o644); err != nil {
			return "", -1, err
		}
		paths = append(paths, p)
	}
	args := append(append([]string{"merge-file", "-p"}, flags...), paths...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode(), nil
		}
		return "", -1, fmt.Errorf("git could not be run: %v", err)
	}
	return string(out), 0, nil
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
// file over ours and exits 0, or leaves git's text merge there, with its conflict markers, and exits 1. A
// file kb does not merge, as a README among the statements, gets git's own text merge and its result.
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
	// git's own text merge, with git's labels and marker length, for a file kb does not merge and for
	// the human when kb cannot
	label := func(k, dflt string) string {
		if v, _ := ns.Str(k); v != "" {
			return v
		}
		return dflt
	}
	flags := []string{"-L", label("ours_label", "ours"), "-L", label("base_label", "base"), "-L", label("theirs_label", "theirs")}
	if size, _ := ns.Str("marker_size"); size != "" {
		flags = append(flags, "--marker-size="+size)
	}
	textMerge := func() (string, int) {
		out, code, err := mergeFile(flags, a, o, b)
		if err != nil {
			fmt.Fprintf(Stderr, "kb merge-driver: %v\n", err)
			return "", -1
		}
		return out, code
	}
	write := func(text string) bool {
		// CRLF only where both sides have it, so that the bytes do not depend on which side is ours
		if strings.Contains(a, "\r\n") && strings.Contains(b, "\r\n") {
			text = strings.ReplaceAll(dyn.Universal(text), "\n", "\r\n")
		}
		if err := os.WriteFile(ours, []byte(text), 0o644); err != nil {
			fmt.Fprintf(Stderr, "kb merge-driver: cannot write %s: %v\n", path, err)
			return false
		}
		return true
	}
	if kind == "" {
		out, code := textMerge()
		if code < 0 || code >= 255 || !write(out) {
			return 1
		}
		return min(code, 1)
	}
	merged, problems := mergeRecordFile(kind, o, a, b)
	if len(problems) == 0 {
		if !write(merged) {
			return 1
		}
		return 0
	}
	out, code := textMerge()
	msg := fmt.Sprintf("kb could not merge %s: %s. It is left for the human.", path, strings.Join(problems, "; "))
	switch {
	case code < 0 || code >= 255:
		msg += " git's text merge of it failed too, so the file holds our side."
	case !write(out):
		msg += " Writing git's text merge of it failed, so the file holds our side."
	case code == 0:
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
	for _, f := range files {
		kind := recordFileKind(f)
		base, okO := gitBlob(root, f, 1)
		ours, okA := gitBlob(root, f, 2)
		theirs, okB := gitBlob(root, f, 3)
		if kind == "statement" && !okO && okA && okB {
			// both sides added a file of this name: perhaps two statements given one id and one title (S-74B5)
			a, errA := readStatement(f, ours)
			b, errB := readStatement(f, theirs)
			if errA == nil && errB == nil && dyn.Truthy(a.Meta.Get("id")) && dyn.Equal(a.Meta.Get("id"), b.Meta.Get("id")) &&
				statementOrigin(a.Meta) != statementOrigin(b.Meta) {
				left = append(left, fmt.Sprintf("%s: both sides added it, as two different statements: %s",
					f, collisionText(dyn.Str(a.Meta.Get("id")), []string{"ours", "theirs"})))
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
	// then report any id two statements carry, which kb leaves to the human (S-74B5), and any person the
	// user table now has two rows for, as when two copies added one key with different emails
	left = append(left, collisions(root)...)
	left = append(left, userConflicts(root)...)
	if len(resolved) > 0 {
		println("Written by kb:")
		for _, f := range resolved {
			println("  " + f)
		}
		println(finishHint(root, resolved))
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

// finishHint says what follows once kb resolve has written files, as the knowledge base's case asks:
// git reports them conflicted until they are staged. In a repository of its own the agent stages them and
// finishes the merge or rebase, in forms that open no editor; inside a code repository that is the person's.
func finishHint(root string, files []string) string {
	p := readGitPlace(root)
	if !p.ownRepo() {
		return "They are written, and git reports them conflicted until they are staged. Staging them and " +
			"finishing the merge or rebase is the person's, with the code. Run kb check."
	}
	g := gitPrefix(root)
	return "git reports them conflicted until they are staged. Stage them with `" + g + " add -A -- " +
		strings.Join(files, " ") + "`, then finish a merge with `" + g + " commit --no-edit` or a rebase with `" + g +
		" -c core.editor=true rebase --continue`, push where you push, and run kb check."
}

// userConflicts are the people the user table has more than one row for, by key or by email, which the
// union merge keeps both of and kb check refuses; kb leaves them to the human.
func userConflicts(root string) []string {
	t, problem := loadUsers(root)
	if problem != "" {
		return []string{usersName + ": " + problem}
	}
	byKey, byEmail := map[string][]person{}, map[string][]person{}
	for _, r := range t.rows {
		byKey[r.key] = append(byKey[r.key], r)
		byEmail[strings.ToLower(r.email)] = append(byEmail[strings.ToLower(r.email)], r)
	}
	var out []string
	describe := func(rows []person) string {
		var parts []string
		for _, r := range rows {
			parts = append(parts, fmt.Sprintf("%s (%s <%s>)", r.key, r.name, r.email))
		}
		return strings.Join(parts, " and ")
	}
	for _, k := range sortedKeys(byKey) {
		if rows := byKey[k]; len(rows) > 1 {
			out = append(out, fmt.Sprintf("%s: the key %s has %d rows, %s, as when two copies added it differently",
				usersName, k, len(rows), describe(rows)))
		}
	}
	for _, e := range sortedKeys(byEmail) {
		if rows := byEmail[e]; len(rows) > 1 && rows[0].key != rows[1].key {
			out = append(out, fmt.Sprintf("%s: the email %s is on %d rows, %s", usersName, e, len(rows), describe(rows)))
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	d.Positional("marker_size", argparse.Optional, "the length of conflict markers (%L)")
	d.Run = runMergeDriver

	r := sub.AddParser("resolve", "merge the record's files git left conflicted, and report ids two statements share")
	r.Positional("files", argparse.ZeroMore, "conflicted files to resolve: absolute, or relative to the current "+
		"folder or the knowledge base (default: every conflicted file)")
	r.Run = runResolve
}
