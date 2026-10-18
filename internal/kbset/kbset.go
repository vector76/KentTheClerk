// Package kbset finds the knowledge bases a project refers to and the one a command acts on, for both the
// recorder and the launcher. A code project can refer to several knowledge bases (S-ZBVAHS); KENT_KB names
// them, separated as PATH separates folders. Each knowledge base can carry a collision-free id in a
// committed file at its top, kent.id, and each person's ~/.kenttheclerk.json maps ids to the folders the
// knowledge bases are kept in on their machine (S-Y17H90). A reference into another knowledge base combines
// its id with a statement id: K-XXXXXXXXXXXX/S-XXXXXX.
package kbset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	// Env names the project's knowledge bases.
	Env = "KENT_KB"
	// IDFile is the id's file in the knowledge base's top folder.
	IDFile = "kent.id"
	// IDLen is the number of characters after "K-".
	IDLen = 12
	// MapKey is the key of the id-to-folder map in ~/.kenttheclerk.json.
	MapKey = "knowledge_bases"
)

// IDRE is a knowledge base's id: "K-" and twelve characters of Crockford's base32 alphabet, the alphabet of
// statement ids.
var IDRE = regexp.MustCompile(`^K-[0-9A-HJKMNP-TV-Z]{12}$`)

// RefRE is a reference into another knowledge base: its id, a slash, and a statement id.
var RefRE = regexp.MustCompile(`^(K-[0-9A-HJKMNP-TV-Z]{12})/([SA]-[0-9A-Z]+)$`)

// SplitRef splits "K-…/S-…" into the knowledge base's id and the statement id; ok is false for anything else.
func SplitRef(s string) (kbID, statement string, ok bool) {
	m := RefRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// IsKB is whether p is shaped like a knowledge base: attested/ and evidence/ folders.
func IsKB(p string) bool {
	a, err1 := os.Stat(filepath.Join(p, "attested"))
	e, err2 := os.Stat(filepath.Join(p, "evidence"))
	return err1 == nil && err2 == nil && a.IsDir() && e.IsDir()
}

// IDPath is where the id of the knowledge base at root lives.
func IDPath(root string) string { return filepath.Join(root, IDFile) }

// ReadID is the id of the knowledge base at root. With no id file it returns ok false and no error; a file
// that does not hold exactly one valid id is an error, a merge conflict among them.
func ReadID(root string) (id string, ok bool, err error) {
	b, err := os.ReadFile(IDPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	switch {
	case len(lines) == 0:
		return "", false, fmt.Errorf("%s is empty", IDPath(root))
	case len(lines) > 1:
		return "", false, fmt.Errorf("%s holds more than one line; a knowledge base has one id", IDPath(root))
	case !IDRE.MatchString(lines[0]):
		return "", false, fmt.Errorf("%s holds %q, which is not a knowledge base id (K- and twelve characters)", IDPath(root), lines[0])
	}
	return lines[0], true, nil
}

// SplitList is KENT_KB's value as a list of folders, separated as PATH separates them.
func SplitList(v string) []string {
	var out []string
	for _, p := range filepath.SplitList(v) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// homeFiles are the settings files read for the map, the older name only when the new one is absent.
var homeFiles = []string{".kenttheclerk.json", ".convkb.json"}

// HomeMap is the id-to-folder map in ~/.kenttheclerk.json, with "~/" expanded, and the file it came from.
// A missing file or map is an empty map; a file that is not JSON is skipped, as the user id's reading
// skips it.
func HomeMap() (map[string]string, string) {
	home, _ := os.UserHomeDir()
	for _, name := range homeFiles {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			continue
		}
		var cfg map[string]json.RawMessage
		if json.Unmarshal(b, &cfg) != nil {
			continue
		}
		out := map[string]string{}
		var m map[string]string
		if raw, ok := cfg[MapKey]; ok && json.Unmarshal(raw, &m) == nil {
			for id, folder := range m {
				if strings.HasPrefix(folder, "~/") || strings.HasPrefix(folder, `~\`) {
					folder = filepath.Join(home, folder[2:])
				}
				out[strings.ToUpper(strings.TrimSpace(id))] = folder
			}
		}
		return out, "~/" + name
	}
	return map[string]string{}, ""
}

// SamePath compares two folders as the file system would.
func SamePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Abs is p made absolute, with "~/" expanded; symbolic links are not followed.
func Abs(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if h, err := os.UserHomeDir(); err == nil {
			p = h + p[1:]
		}
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// Locate is the folder an id names: one of the given folders (the project's knowledge bases) whose
// kent.id holds it, else the folder ~/.kenttheclerk.json maps it to. "" when neither has it.
func Locate(id string, folders []string) (folder, how string) {
	id = strings.ToUpper(strings.TrimSpace(id))
	for _, f := range folders {
		if got, ok, _ := ReadID(f); ok && got == id {
			return f, "one of this project's knowledge bases"
		}
	}
	m, from := HomeMap()
	if f, ok := m[id]; ok {
		return Abs(f), "from " + from
	}
	return "", ""
}

// Arg is the folder a --kb argument names: an id, located as Locate does, or a folder. The error says why
// it names no knowledge base.
func Arg(arg string, folders []string) (string, error) {
	arg = strings.TrimSpace(arg)
	if IDRE.MatchString(strings.ToUpper(arg)) {
		f, _ := Locate(arg, folders)
		if f == "" {
			return "", fmt.Errorf("--kb %s: no knowledge base with that id is among this project's (%s) or "+
				"mapped to a folder under %q in ~/.kenttheclerk.json", arg, Env, MapKey)
		}
		if !IsKB(f) {
			return "", fmt.Errorf("--kb %s names %s, which has no attested/ and evidence/ folders", arg, f)
		}
		return f, nil
	}
	f := Abs(arg)
	if !IsKB(f) {
		return "", fmt.Errorf("--kb %s has no attested/ and evidence/ folders", f)
	}
	return f, nil
}
