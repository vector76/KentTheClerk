package mimetypes

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAgainstPython compares GuessType with mimetypes.guess_type on this machine (registry included).
func TestAgainstPython(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not available")
	}
	names := []string{"a.md", "a.txt", "a.html", "a.pdf", "a.docx", "a.png", "a.json", "a.jsonl", "a.csv",
		"a.PY", "x.tar.gz", "x.tgz", "x.svgz", ".bashrc", "a.", "..md", "a.b.c.xml", "noext", "a.ts", "a.zip",
		"a.WMD", "a.Z", "a.gz", "photo.JPG", "a.mp3", "a.xlsx", "a.pptx", "a.7z", "a.cpp", "a.webp", "a.svg"}
	cmd := exec.Command(py, "-I", "-c", "import mimetypes,sys\nfor n in sys.argv[1:]: print(mimetypes.guess_type(n)[0] or '')")
	cmd.Args = append(cmd.Args, names...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.ReplaceAll(strings.TrimRight(string(out), "\r\n"), "\r\n", "\n"), "\n")
	for i, n := range names {
		if got := GuessType(n); got != want[i] {
			t.Errorf("%s: got %q, Python %q", n, got, want[i])
		}
	}
}
