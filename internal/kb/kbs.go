package kb

// Which knowledge base a command acts on, when a code project refers to several (S-ZBVAHS): --kb names
// one, by folder or id; otherwise a command that acts for a session finds the knowledge base that session
// is recorded in, since a session is recorded in one knowledge base only (S-DHSE97). Also each knowledge
// base's id (S-Y17H90), kb id, and following a reference into another knowledge base.

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
	"kenttheclerk/internal/kbset"
)

// kbFlag is --kb, which Main takes out of the arguments wherever it stands: the folder or id of the
// knowledge base the command acts on.
var kbFlag string

// takeKBFlag removes --kb <value> (or --kb=<value>) from args and returns the value. A second --kb is
// refused, so that two never quietly disagree.
func takeKBFlag(args []string) ([]string, string, error) {
	var out []string
	val, seen := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		var v string
		switch {
		case a == "--":
			out = append(out, args[i:]...)
			return out, val, nil
		case a == "--kb":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--kb: expected one argument, the knowledge base's folder or id")
			}
			v = args[i+1]
			i++
		case strings.HasPrefix(a, "--kb="):
			v = strings.TrimPrefix(a, "--kb=")
		default:
			out = append(out, a)
			continue
		}
		if seen {
			return nil, "", fmt.Errorf("--kb is given twice; a command acts on one knowledge base")
		}
		val, seen = v, true
	}
	return out, val, nil
}

// findKBRoots are the project's knowledge bases and how they were found: KENT_KB's folders, else the one
// in the current folder or above. problem says why there are none, or which named folder is no knowledge
// base.
func findKBRoots() (roots []string, how, problem string) {
	if env := os.Getenv(kbEnv); env != "" {
		var bad []string
		for _, f := range kbset.SplitList(env) {
			p := dyn.Resolve(f)
			if isKBFolder(p) {
				roots = append(roots, p)
			} else {
				bad = append(bad, p)
			}
		}
		switch {
		case len(bad) > 0:
			return nil, "", fmt.Sprintf("%s names %s, which has no attested/ and evidence/ folders", kbEnv, strings.Join(bad, " and "))
		case len(roots) == 0:
			return nil, "", kbEnv + " is set but names no folder"
		}
		return roots, "from " + kbEnv, ""
	}
	cwd, err := os.Getwd()
	if err != nil {
		dyn.RaiseOS(err)
	}
	p := dyn.Resolve(cwd)
	for cand := p; ; {
		if isKBFolder(cand) {
			if cand == p {
				return []string{cand}, "from the current folder", ""
			}
			return []string{cand}, "from a folder above the current one", ""
		}
		parent := filepath.Dir(cand)
		if parent == cand {
			break
		}
		cand = parent
	}
	return nil, "", kbEnv + " is not set, and no attested/ and evidence/ folders were found in the current " +
		"folder or above"
}

// projectRoots are the project's knowledge bases, or none when they cannot be found.
func projectRoots() []string {
	roots, _, _ := findKBRoots()
	return roots
}

// kbLabel is a knowledge base as a message names it: its folder, and its id when it has one.
func kbLabel(root string) string {
	if id, ok, _ := kbset.ReadID(root); ok {
		return root + " (" + id + ")"
	}
	return root
}

// labels are kbLabel of each root, joined.
func labels(roots []string) string {
	var out []string
	for _, r := range roots {
		out = append(out, kbLabel(r))
	}
	return strings.Join(out, ", ")
}

// flagRoot is the knowledge base --kb names, or "" without --kb.
func flagRoot() string {
	if kbFlag == "" {
		return ""
	}
	f, err := kbset.Arg(kbFlag, projectRoots())
	if err != nil {
		die(err.Error())
	}
	return dyn.Resolve(f)
}

// repoRoot is the knowledge base the command acts on: the one --kb names, else the project's only one. A
// project with several needs --kb for a command that cannot tell from what it is given.
func repoRoot() string {
	if r := flagRoot(); r != "" {
		return r
	}
	roots, _, problem := findKBRoots()
	switch {
	case problem != "":
		die("no knowledge base found: " + problem)
	case len(roots) > 1:
		die(fmt.Sprintf("this project refers to several knowledge bases: %s. Name the one this command acts "+
			"on with --kb <folder or id> (%s)", labels(roots), rule("one-knowledge-base")))
	}
	return roots[0]
}

// repoRootOrFirst is the knowledge base --kb names, else the project's first: for reading, where any of
// them will do.
func repoRootOrFirst() string {
	if r := flagRoot(); r != "" {
		return r
	}
	roots, _, problem := findKBRoots()
	if problem != "" {
		die("no knowledge base found: " + problem)
	}
	return roots[0]
}

// sessionHolders are the knowledge bases among roots that hold a snapshot of the session, on or off the
// record. A snapshot's file is named after the first eight characters of the session's id.
func sessionHolders(roots []string, session string) []string {
	key := dyn.Head(session, 8)
	if key == "" {
		return nil
	}
	var out []string
	for _, r := range roots {
		found := false
		for _, md := range evidenceDirs(r) {
			for _, p := range dyn.Glob(md.dir, "*.jsonl") {
				if strings.Contains(filepath.Base(p), key) {
					found = true
				}
			}
		}
		if found {
			out = append(out, r)
		}
	}
	return out
}

// sessionRoot is the knowledge base a command acting for session works in: the one --kb names, the
// project's only one, or the one the session is recorded in. snapshotting is true for kb snapshot, which
// may record a session for the first time and so needs --kb when the project has several.
func sessionRoot(session string, snapshotting bool) string {
	roots, _, problem := findKBRoots()
	holders := sessionHolders(roots, session)
	if r := flagRoot(); r != "" {
		for _, h := range holders {
			if !samePath(h, r) {
				die(fmt.Sprintf("session %s is already recorded in %s, so it is not recorded in %s: a session is "+
					"recorded in one knowledge base (%s)", dyn.Head(session, 8), kbLabel(h), kbLabel(r), rule("one-knowledge-base")))
			}
		}
		return r
	}
	switch {
	case problem != "":
		die("no knowledge base found: " + problem)
	case len(roots) == 1:
		return roots[0]
	case len(holders) == 1:
		return holders[0]
	case len(holders) > 1:
		die(fmt.Sprintf("session %s is recorded in more than one knowledge base: %s. A session is recorded in "+
			"one knowledge base; tell the human (%s)", dyn.Head(session, 8), labels(holders), rule("one-knowledge-base")))
	case snapshotting:
		die(fmt.Sprintf("this project refers to several knowledge bases, and session %s is recorded in none of "+
			"them yet: %s. Ask the human which one this session goes on the record in, and pass --kb <folder or "+
			"id> (%s)", dyn.Head(session, 8), labels(roots), rule("one-knowledge-base")))
	}
	die(fmt.Sprintf("session %s is recorded in none of this project's knowledge bases: %s (%s)",
		dyn.Head(session, 8), labels(roots), rule("one-knowledge-base")))
	return ""
}

// turnRoot is sessionRoot for the session of an evidence pointer such as --turn <session>#<turn>.
func turnRoot(ptr string) string {
	session, _, _ := strings.Cut(strings.TrimSpace(ptr), "#")
	if session == "" {
		return repoRoot()
	}
	return sessionRoot(session, false)
}

// ---------------------------------------------------------------- references into another knowledge base

// refStatus is how far a reference into another knowledge base can be followed here.
type refStatus int

const (
	refFound   refStatus = iota // the statement, in a knowledge base on this machine
	refNoKB                     // no knowledge base with that id on this machine
	refMissing                  // the knowledge base is here, the statement is not
	refOwn                      // the id is this knowledge base's own
)

// foreignRef is a followed reference into another knowledge base.
type foreignRef struct {
	kbID, statement string
	folder          string // where the knowledge base is, when it is on this machine
	path            string // the statement's file, when found
	status          refStatus
}

// isForeignRef is whether s is written as a reference into another knowledge base.
func isForeignRef(s string) bool {
	_, _, ok := kbset.SplitRef(s)
	return ok
}

// followRef follows ref, written <kb id>/<statement id>, from the knowledge base at root.
func followRef(root, ref string) foreignRef {
	kid, sid, _ := kbset.SplitRef(ref)
	r := foreignRef{kbID: kid, statement: sid}
	if own, ok, _ := kbset.ReadID(root); ok && own == kid {
		r.status = refOwn
		return r
	}
	r.folder, _ = kbset.Locate(kid, projectRoots())
	if r.folder == "" || !isKBFolder(r.folder) {
		r.folder, r.status = "", refNoKB
		return r
	}
	if r.path = statementPath(r.folder, sid); r.path == "" {
		r.status = refMissing
	}
	return r
}

// requireForeignRef refuses a reference a new statement makes into another knowledge base unless it can be
// followed here: the agent writing it has that knowledge base, so a reference it cannot follow is a mistake.
func requireForeignRef(root, field, ref string) {
	r := followRef(root, ref)
	switch r.status {
	case refOwn:
		die(fmt.Sprintf("%s: %s names this knowledge base's own id; write the statement id alone, %s (%s)", field, ref, r.statement, rule("other-knowledge-base")))
	case refNoKB:
		die(fmt.Sprintf("%s: %s is in a knowledge base that is not on this machine: no knowledge base with id %s is "+
			"in %s or mapped under %q in ~/.kenttheclerk.json (%s)", field, ref, r.kbID, kbEnv, kbset.MapKey, rule("other-knowledge-base")))
	case refMissing:
		die(fmt.Sprintf("%s: %s does not exist in %s", field, r.statement, kbLabel(r.folder)))
	}
}

// refusedForeign refuses a supersession, amendment or clarification across knowledge bases: each marks its
// target, which a session on the record in one knowledge base cannot write (S-DHSE97).
func refusedForeign(field, ref string) {
	if isForeignRef(ref) {
		die(fmt.Sprintf("%s: %s is in another knowledge base, and %s would mark it there, which a session on "+
			"the record here cannot write. Link to it with context_for or rests_on (%s)", field, ref, field, rule("other-knowledge-base")))
	}
}

// ---------------------------------------------------------------- kb id

// newKBID draws a knowledge base's id: twelve characters, so that two drawn anywhere do not collide.
func newKBID() string {
	return drawID("K", kbset.IDLen, func(string) bool { return false }, rand.Reader)
}

func runID(ns argparse.Namespace) int {
	root := repoRoot()
	id, ok, err := kbset.ReadID(root)
	if err != nil {
		die(fmt.Sprintf("the knowledge base's id cannot be read: %v", err))
	}
	if !ns.Bool("create") {
		if !ok {
			println("id: none. kb id --create gives this knowledge base one; it is needed before any other " +
				"knowledge base refers to it")
			return 1
		}
		println("id: " + id)
		return 0
	}
	if ok {
		die(fmt.Sprintf("this knowledge base already has the id %s; an id never changes, since references "+
			"into the knowledge base carry it", id))
	}
	requirePin(root)
	id = newKBID()
	if err := os.WriteFile(kbset.IDPath(root), []byte(id+"\n"), 0o666); err != nil {
		die(fmt.Sprintf("cannot write %s: %v", kbset.IDPath(root), err))
	}
	println("gave " + root + " the id " + id + " in " + kbset.IDFile)
	println("commit " + kbset.IDFile + " now, so that every copy of this knowledge base has the same id")
	return 0
}

func addIDParser(sub *argparse.Parser) {
	p := sub.AddParser("id", "show this knowledge base's id, or give it one")
	p.Flag("--create", "give the knowledge base an id, once; commit kent.id after")
	p.Run = runID
}
