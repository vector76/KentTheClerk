package kb

import (
	"testing"

	kent "kenttheclerk"
)

// TestRulesNumbering checks that the rules built into kb are numbered 1, 2, 3, ... with no gap or
// repeat, so kb rules <number> finds each one, and that every rule the code cites exists.
func TestRulesNumbering(t *testing.T) {
	seen := map[int]bool{}
	want := 1
	for _, rf := range ruleFiles() {
		if rf.title == "" {
			t.Errorf("%s has no title", rf.path)
		}
		for _, r := range rf.rules {
			if r.n != want {
				t.Errorf("%s: Kent Rule %d where Rule %d was expected", rf.path, r.n, want)
			}
			if r.text == "" {
				t.Errorf("Kent Rule %d is empty", r.n)
			}
			seen[r.n] = true
			want = r.n + 1
		}
	}
	for key, n := range productRules {
		if !seen[n] {
			t.Errorf("the code cites Kent Rule %d (%s), which no rule file holds", n, key)
		}
	}
}

// TestDocumentsBuiltIn checks that every document kb rules names is built into the binary.
func TestDocumentsBuiltIn(t *testing.T) {
	for _, d := range append(append([]document{}, leadingDocs...), trailingDocs...) {
		if s, err := kent.ReadFile(d.path); err != nil || s == "" {
			t.Errorf("%s (%s) is not built in: %v", d.name, d.path, err)
		}
	}
}
