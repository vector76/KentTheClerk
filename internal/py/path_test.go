package py

import "testing"

// TestPathStr checks PathStr under Windows path rules: drives, UNC anchors, separators and "." parts.
func TestPathStr(t *testing.T) {
	if !Windows {
		t.Skip("the expected results are those of Windows path rules")
	}
	for _, c := range [][2]string{
		{"a", "a"},
		{"a/b", `a\b`},
		{"a//b/", `a\b`},
		{"./a", "a"},
		{".", "."},
		{"", "."},
		{"a/./b", `a\b`},
		{"a/../b", `a\..\b`},
		{"/a", `\a`},
		{"//a", `\\a`},
		{"//a/", `\\a\`},
		{"//srv/share/x", `\\srv\share\x`},
		{"//srv/share", `\\srv\share\`},
		{"C:", "C:"},
		{"C:/", `C:\`},
		{"C:a", "C:a"},
		{"./c:x", `.\c:x`},
		{"a/./c:x", `a\c:x`},
		{"c:/x/../y", `c:\x\..\y`},
		{`a\b`, `a\b`},
		{"x.jsonl", "x.jsonl"},
	} {
		if got := PathStr(c[0]); got != c[1] {
			t.Errorf("%q: got %q, want %q", c[0], got, c[1])
		}
	}
}

func TestFnMatchFolds(t *testing.T) {
	if !Windows {
		t.Skip("case-insensitive matching is Windows-only")
	}
	for _, c := range [][2]string{{"ſ", "S"}, {"K", "k"}, {"Σ", "ς"}, {"µ", "Μ"}} {
		if !FnMatch(c[0], c[1]) || !FnMatch(c[0], "["+c[1]+"]") {
			t.Errorf("%q should match %q", c[0], c[1])
		}
	}
}
