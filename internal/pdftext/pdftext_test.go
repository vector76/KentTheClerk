package pdftext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The corpus in testdata/corpus is generated (Python stdlib only) by
//
//	python -I testdata/gen_corpus.py               (hand-built feature files)
//	python -I testdata/gen_random.py testdata/corpus 25 2026   (rand_*.pdf)
//
// testdata/corpus/expected.json holds what pypdf (version recorded in the
// file) returned for each PDF; TestGolden checks Extract against it on any
// machine, TestDifferentialPypdf re-runs pypdf live when python and pypdf
// are available (and skips otherwise). Regenerate the golden file with:
//
//	PDFTEXT_UPDATE_GOLDEN=1 go test ./internal/pdftext/ -run TestDifferentialPypdf
//
// TestCorpusExtra compares every PDF in the directory named by PDFTEXT_EXTRA,
// e.g. the output of gen_random.py with a large count, or real documents.

// pinnedPypdf is the pypdf release the port reproduces.
const pinnedPypdf = "6.14.2"

type pyResult struct {
	File  string   `json:"file"`
	Pages []string `json:"pages"`
	Title *string  `json:"title"`
	Error *string  `json:"error"`
	Pypdf string   `json:"pypdf,omitempty"`
}

func corpusFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.pdf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}
	sort.Strings(files)
	return files
}

func findPython(t *testing.T) string {
	t.Helper()
	for _, c := range []string{os.Getenv("PDFTEXT_PYTHON"), "python", "python3", "py"} {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func runPypdf(t *testing.T, files []string) []pyResult {
	t.Helper()
	py := findPython(t)
	if py == "" {
		t.Skip("python not found (set PDFTEXT_PYTHON); skipping pypdf differential test")
	}
	var all []pyResult
	for len(files) > 0 {
		n := len(files)
		if n > 40 {
			n = 40
		}
		all = append(all, runPypdfBatch(t, py, files[:n])...)
		files = files[n:]
	}
	return all
}

func runPypdfBatch(t *testing.T, py string, files []string) []pyResult {
	t.Helper()
	args := append([]string{"-I", filepath.Join("testdata", "pypdf_dump.py")}, files...)
	cmd := exec.Command(py, args...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 {
			t.Skip("pypdf is not installed; skipping pypdf differential test")
		}
		t.Skipf("running python failed (%v): %s", err, stderr.String())
	}
	var res []pyResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("decoding pypdf output: %v\n%s", err, stdout.String())
	}
	if len(res) != len(files) {
		t.Fatalf("pypdf returned %d results for %d files", len(res), len(files))
	}
	return res
}

func goResult(file string) pyResult {
	pages, title, hasTitle, err := Extract(file)
	r := pyResult{File: file}
	if err != nil {
		s := err.Error()
		r.Error = &s
		return r
	}
	r.Pages = pages
	if hasTitle {
		r.Title = &title
	}
	return r
}

func show(s *string) string {
	if s == nil {
		return "None"
	}
	return fmt.Sprintf("%q", *s)
}

// diff lists the differences between pypdf's result and ours.
func diff(want, got pyResult) []string {
	name := filepath.Base(want.File)
	if (want.Error != nil) != (got.Error != nil) {
		return []string{fmt.Sprintf("%s: pypdf error %s, Go error %s", name, show(want.Error), show(got.Error))}
	}
	if want.Error != nil {
		return nil
	}
	var out []string
	if show(want.Title) != show(got.Title) {
		out = append(out, fmt.Sprintf("%s: title\n  pypdf: %s\n  go:    %s", name, show(want.Title), show(got.Title)))
	}
	if len(want.Pages) != len(got.Pages) {
		return append(out, fmt.Sprintf("%s: pypdf has %d pages, Go %d", name, len(want.Pages), len(got.Pages)))
	}
	for i := range want.Pages {
		if want.Pages[i] != got.Pages[i] {
			out = append(out, fmt.Sprintf("%s page %d differs\n  pypdf: %q\n  go:    %q", name, i+1, want.Pages[i], got.Pages[i]))
		}
	}
	return out
}

func compare(t *testing.T, want, got pyResult) {
	t.Helper()
	for _, d := range diff(want, got) {
		t.Error(d)
	}
}

func goldenPath() string { return filepath.Join("testdata", "corpus", "expected.json") }

func TestGolden(t *testing.T) {
	data, err := os.ReadFile(goldenPath())
	if err != nil {
		t.Skipf("no golden file: %v", err)
	}
	var want []pyResult
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		w.File = filepath.Join("testdata", "corpus", w.File)
		compare(t, w, goResult(w.File))
	}
}

func TestDifferentialPypdf(t *testing.T) {
	files := corpusFiles(t)
	res := runPypdf(t, files)
	if v := res[0].Pypdf; v != pinnedPypdf {
		// Parity is pinned to one pypdf release; other releases change the
		// extraction now and then, so only report what differs.
		n := 0
		for i, f := range files {
			n += len(diff(res[i], goResult(f)))
		}
		t.Skipf("pypdf %s installed (port follows %s): %d differences", v, pinnedPypdf, n)
	}
	for i, f := range files {
		compare(t, res[i], goResult(f))
	}
	if os.Getenv("PDFTEXT_UPDATE_GOLDEN") != "" {
		for i := range res {
			res[i].File = filepath.Base(res[i].File)
		}
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath(), append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCorpusExtra runs any PDFs listed in PDFTEXT_EXTRA (a directory) through
// both implementations; it is a tool for checking real-world files locally.
func TestCorpusExtra(t *testing.T) {
	dir := os.Getenv("PDFTEXT_EXTRA")
	if dir == "" {
		t.Skip("PDFTEXT_EXTRA not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if len(files) == 0 {
		t.Skip("no PDFs in PDFTEXT_EXTRA")
	}
	res := runPypdf(t, files)
	same, total := 0, 0
	for i, f := range files {
		total++
		if d := diff(res[i], goResult(f)); len(d) == 0 {
			same++
		} else {
			for _, m := range d {
				t.Error(m)
			}
		}
	}
	t.Logf("%d/%d files identical", same, total)
}

func TestNoPanicOnGarbage(t *testing.T) {
	inputs := [][]byte{
		nil,
		[]byte("%PDF-1.4\n"),
		[]byte("%PDF-1.4\n%%EOF\n"),
		[]byte("not a pdf at all"),
		[]byte(strings.Repeat("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n", 3) + "startxref\n9\n%%EOF"),
	}
	for _, in := range inputs {
		_, _, _, _ = ExtractBytes(in)
	}
}
