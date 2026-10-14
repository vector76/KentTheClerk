// Package pin reads and writes a knowledge base's pin (S-0093): a small committed file at the top of the
// knowledge base naming the version of Kent everyone writes under and that version's system hash. The
// recorder refuses to act under any other version; the launcher reads the pin to choose which installed
// version to run (S-0112).
package pin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileName is the pin's name in the knowledge base's top folder.
const FileName = "kent.pin"

// Pin is the version a knowledge base has adopted.
type Pin struct {
	Version string // system_version, e.g. "0.9.0"
	Hash    string // system_hash, "sha256:" + hex
}

var hashRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Path is where the pin of the knowledge base at root lives.
func Path(root string) string { return filepath.Join(root, FileName) }

// Read returns the pin of the knowledge base at root. With no pin it returns ok false and no error; a
// pin that cannot be read or parsed is an error.
func Read(root string) (p Pin, ok bool, err error) {
	b, err := os.ReadFile(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		return Pin{}, false, nil
	}
	if err != nil {
		return Pin{}, false, err
	}
	p, err = Parse(string(b))
	if err != nil {
		return Pin{}, false, fmt.Errorf("%s: %w", Path(root), err)
	}
	return p, true, nil
}

// Parse reads a pin's text: "key: value" lines, with blank lines and lines starting with "#" ignored.
// Both system_version and system_hash are required; other keys are refused, so a typo is never ignored.
func Parse(text string) (Pin, error) {
	var p Pin
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			return Pin{}, fmt.Errorf("not a \"key: value\" line: %q", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "system_version":
			p.Version = v
		case "system_hash":
			p.Hash = v
		default:
			return Pin{}, fmt.Errorf("unknown key %q", k)
		}
	}
	if p.Version == "" {
		return Pin{}, errors.New("no system_version")
	}
	if !hashRE.MatchString(p.Hash) {
		return Pin{}, errors.New("missing or malformed system_hash")
	}
	return p, nil
}

// Format is the pin's text as Write writes it.
func Format(p Pin) string {
	return "# The version of Kent the Clerk this knowledge base has adopted. kb refuses to write under any\n" +
		"# other. Change it with `kb pin --set` under the new version, and commit it.\n" +
		"system_version: " + p.Version + "\n" +
		"system_hash: " + p.Hash + "\n"
}

// Write sets the pin of the knowledge base at root.
func Write(root string, p Pin) error {
	return os.WriteFile(Path(root), []byte(Format(p)), 0o644)
}

// Remove deletes the pin of the knowledge base at root; removing a pin that is not there is not an error.
func Remove(root string) error {
	err := os.Remove(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Matches reports whether a build with the given version and hash is the pinned one.
func (p Pin) Matches(version, hash string) bool { return p.Version == version && p.Hash == hash }

func (p Pin) String() string { return "Kent " + p.Version + " (" + p.Hash + ")" }
