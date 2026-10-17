package kb

// Detecting a random id collision (S-74B5): copies gave different statements the same id, and a merge
// brought both into one record. With six-character ids (S-N5TZ) that is rare enough that kb does not repair
// it: kb check and kb resolve report it, and it is left to the human.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"kenttheclerk/internal/dyn"
)

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

// statementFiles are the record's statement files.
func statementFiles(root string) []string {
	var out []string
	for kind, dir := range statementDirs {
		out = append(out, dyn.Glob(filepath.Join(root, dir), kind+"-*.md")...)
	}
	sort.Strings(out)
	return out
}

// duplicateIDs are the ids two or more statement files carry, with those files' names; files kb cannot
// read are left to kb check.
func duplicateIDs(root string) map[string][]string {
	byID := map[string][]string{}
	for _, p := range statementFiles(root) {
		st, err := readStatement(p, dyn.ReadText(p))
		if err != nil || !dyn.Truthy(st.Meta.Get("id")) {
			continue
		}
		id := strings.ToUpper(dyn.Str(st.Meta.Get("id")))
		byID[id] = append(byID[id], filepath.Base(p))
	}
	for id, names := range byID {
		if len(names) < 2 {
			delete(byID, id)
		}
	}
	return byID
}

// collisionText is how kb reports statements that carry one id.
func collisionText(id string, names []string) string {
	return fmt.Sprintf("%s is carried by %s: copies gave different statements the same id. kb does not repair "+
		"this; it is left to the human", id, strings.Join(names, " and "))
}

// collisions are the reports of every id statement files share, in the order of the ids.
func collisions(root string) []string {
	dups := duplicateIDs(root)
	var ids []string
	for id := range dups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		out = append(out, collisionText(id, dups[id]))
	}
	return out
}
