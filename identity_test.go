package kent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHashCoversSource fails when a .go file of the executable is outside the embed patterns, e.g. in a
// new nested package, so the hash cannot silently stop covering code.
func TestHashCoversSource(t *testing.T) {
	embedded := map[string]bool{}
	for _, f := range Files() {
		embedded[f] = true
	}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(p)
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", ".github", "testdata", "parity":
				return fs.SkipDir
			}
			// the launcher is infrastructure outside the system it dispatches to, never hashed or pinned
			if rel == "cmd/launcher" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".go") && !embedded[rel] {
			t.Errorf("%s is not covered by system_hash; extend the go:embed patterns in identity.go", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go.mod", "go.sum", "procedure.md", "README.md", "front-ends/claude-code/CLAUDE.md"} {
		if !embedded[want] {
			t.Errorf("%s is not covered by system_hash", want)
		}
	}
	rules, _ := filepath.Glob("rules/*.md")
	for _, r := range rules {
		if !embedded[filepath.ToSlash(r)] {
			t.Errorf("%s is not covered by system_hash", r)
		}
	}
	if _, err := os.Stat("go.mod"); err != nil {
		t.Fatal(err)
	}
}
