package kb

// A knowledge base's mode and its place in git. kb never runs git to change anything: the agent commits,
// pulls and pushes, as the knowledge base's mode warrants (S-0T5Y), and the session-start text tells it
// the mode (S-DR9W). The mode is a small file committed in the knowledge base, and without one the
// knowledge base is offline (S-HT4V). A knowledge base may also sit inside a code repository, in a folder
// of its own (S-NXEB), which setup finds by its shape rather than its name (S-1RA3).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// modeFileName is the mode's file in the knowledge base's top folder, beside kent.pin.
const modeFileName = "kent.mode"

const (
	modeOffline = "offline"
	modeOnline  = "online"
)

func modePath(root string) string { return filepath.Join(root, modeFileName) }

// readKBMode returns the mode of the knowledge base at root, and whether it has a mode file. Without one
// it is offline. A file that cannot be read or parsed is an error, and the caller treats the knowledge
// base as offline, since pushing what nobody asked to share is worse than not pulling.
func readKBMode(root string) (mode string, found bool, err error) {
	b, err := os.ReadFile(modePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return modeOffline, false, nil
	}
	if err != nil {
		return modeOffline, true, err
	}
	mode, err = parseKBMode(string(b))
	if err != nil {
		return modeOffline, true, fmt.Errorf("%s: %w", modePath(root), err)
	}
	return mode, true, nil
}

// parseKBMode reads a mode file's text: "key: value" lines, as in kent.pin, with blank lines and lines
// starting with "#" ignored. The one key is mode, offline or online; anything else is refused, so a typo
// never passes for offline.
func parseKBMode(text string) (string, error) {
	mode := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			return "", fmt.Errorf("not a \"key: value\" line: %q", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "mode" {
			return "", fmt.Errorf("unknown key %q", k)
		}
		if v != modeOffline && v != modeOnline {
			return "", fmt.Errorf("mode is %q, not offline or online", v)
		}
		if mode != "" && mode != v {
			return "", errors.New("mode is given twice")
		}
		mode = v
	}
	if mode == "" {
		return "", errors.New("no mode")
	}
	return mode, nil
}

// gitTop is the folder holding the .git of the repository the folder dir is in, or "" when it is in none.
// It looks for .git itself rather than asking git, so that session-start runs no git.
func gitTop(dir string) string {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// gitPrefix is how the agent runs git on the knowledge base at root, and how the permissions for an
// online knowledge base name it: git -C and the folder with forward slashes, quoted if it holds a space.
func gitPrefix(root string) string {
	p := filepath.ToSlash(root)
	if strings.ContainsAny(p, " \t") {
		p = `"` + p + `"`
	}
	return "git -C " + p
}

// modeLines tell the agent the knowledge base's mode and what it means for the git work (S-DR9W), and
// any problem with it.
func modeLines(root string) (lines []string, problem string) {
	mode, found, err := readKBMode(root)
	if err != nil {
		problem = fmt.Sprintf("the knowledge base's %s cannot be read (%v), so it is treated as offline", modeFileName, err)
	}
	top := gitTop(root)
	prefix := "`" + gitPrefix(root) + " …`"
	if mode == modeOnline {
		lines = append(lines, "Knowledge base mode: online (from "+modeFileName+"). The knowledge base is a git repository synced "+
			"through a shared remote, and the git work is yours: fetch and pull it when the conversation starts, "+
			"before every act and every answer from the record, and every so often between; commit each act and "+
			"push it at once. When the push is refused, pull and push again. When a pull stops on a conflict, or "+
			"the remote cannot be reached, stop and tell the human. Run git as "+prefix+".")
		switch {
		case top == "":
			problem = "the knowledge base's " + modeFileName + " says online, but it is in no git repository"
		case !samePath(top, root):
			lines = append(lines, "It sits inside the git repository at "+top+", so pulling and pushing it pulls "+
				"and pushes that whole repository.")
		}
		return lines, problem
	}
	how := "it has no " + modeFileName
	if found && err == nil {
		how = "from " + modeFileName
	}
	line := "Knowledge base mode: offline (" + how + "). Never fetch, pull or push the knowledge base, even though it is in git"
	switch {
	case top == "":
		line = "Knowledge base mode: offline (" + how + "). The knowledge base is in no git repository, so there is no git work."
	case samePath(top, root):
		line += ". It is its own git repository: commit what acts wrote when a stretch of work in the record is " +
			"done, and before the conversation ends. Run git as " + prefix + "."
	default:
		line += ". It sits inside the git repository at " + top + ", so its files travel with that repository's " +
			"commits: include them when you or the human commit there, and before the conversation ends, say " +
			"if any are left uncommitted."
	}
	return []string{line}, problem
}

// ---------------------------------------------------------------- a knowledge base inside a code repository

// kbOwnNames are the names a knowledge base's top folder holds; anything else in a knowledge base inside
// a code repository is something that does not belong in it (S-NXEB).
var kbOwnNames = map[string]bool{
	"attested": true, "evidence": true, "agent": true, "off-record": true, "render": true,
	"index.db": true, "index.db-journal": true, overrideLogName: true, usersName: true, usersLogName: true,
	"kent.pin": true, modeFileName: true, ".gitignore": true, ".gitattributes": true, ".git": true,
}

// strangers are the entries of the knowledge base's top folder that are not the knowledge base's own,
// apart from a hint file (S-M1JC).
func strangers(root string) []string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if !kbOwnNames[e.Name()] && !isHintFile(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// isHintFile is whether name is a file telling people what the folder is (S-M1JC): a README of any kind.
func isHintFile(name string) bool { return strings.HasPrefix(strings.ToUpper(name), "README") }

func hasHintFile(root string) bool {
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() && isHintFile(e.Name()) {
			return true
		}
	}
	return false
}

var (
	statementFileRE = regexp.MustCompile(`^S-[0-9A-Z]{4}-[a-z0-9-]*\.md$`)
	evidenceFileRE  = regexp.MustCompile(`^[^.].*\.jsonl$`)
)

// hasKBShape is whether dir is shaped like a knowledge base (S-1RA3): attested/ and evidence/ folders
// holding only what a knowledge base puts there, and at least one statement, evidence file or user table,
// so that an unrelated pair of folders with those names is not taken for one.
func hasKBShape(dir string) bool {
	if !isKBFolder(dir) {
		return false
	}
	records := 0
	for sub, re := range map[string]*regexp.Regexp{"attested": statementFileRE, "evidence": evidenceFileRE} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			return false
		}
		for _, e := range entries {
			switch {
			case e.Name() == ".gitkeep":
			case e.IsDir() && sub == "evidence" && e.Name() == "originals":
			case !e.IsDir() && re.MatchString(e.Name()):
				records++
			default:
				return false
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, usersName)); err == nil {
		records++
	}
	return records > 0
}

// skipWhenSearching are folders that hold no knowledge base worth finding and can be large.
var skipWhenSearching = map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true}

// findKBsIn are the folders under dir, dir included, shaped like a knowledge base, looking at most depth
// levels down and not inside hidden folders or another knowledge base.
func findKBsIn(dir string, depth int) []string {
	var out []string
	var walk func(d string, left int)
	walk = func(d string, left int) {
		if hasKBShape(d) {
			out = append(out, d)
			return
		}
		if left == 0 {
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() && !strings.HasPrefix(n, ".") && !skipWhenSearching[n] {
				walk(filepath.Join(d, n), left-1)
			}
		}
	}
	walk(dir, depth)
	sort.Strings(out)
	return out
}
