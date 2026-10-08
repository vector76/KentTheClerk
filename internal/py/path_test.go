package py

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPathStrAgainstPython compares PathStr with str(Path(s)) on this platform. Skipped without python.
func TestPathStrAgainstPython(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not available")
	}
	cases := []string{"a", "a/b", "a//b/", "./a", ".", "", "a/./b", "a/../b", "/a", "//a", "//a/", "//srv/share/x",
		"//srv/share", "C:", "C:/", "C:a", "./c:x", "a/./c:x", "c:/x/../y", `a\b`, "x.jsonl"}
	cmd := exec.Command(py, "-I", "-c", "import sys,pathlib\nfor a in sys.argv[1:]: print(str(pathlib.Path(a)) if a else '.')")
	cmd.Args = append(cmd.Args, cases...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.ReplaceAll(strings.TrimRight(string(out), "\r\n"), "\r\n", "\n"), "\n")
	for i, c := range cases {
		if got := PathStr(c); got != want[i] {
			t.Errorf("%q: got %q, Python %q", c, got, want[i])
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
