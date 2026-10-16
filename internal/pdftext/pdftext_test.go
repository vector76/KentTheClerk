package pdftext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/corpus holds fixed test PDFs: hand-built files for single
// features (filters, xref layouts, encodings, titles) and randomly
// generated ones (rand_*.pdf). testdata/corpus/expected.json holds the
// pages, title or error expected for each; TestGolden checks Extract
// against it on any machine.
//
// TestCorpusExtra runs every PDF in the directory named by PDFTEXT_EXTRA,
// e.g. real documents, through Extract.

type extraction struct {
	File  string   `json:"file"`
	Pages []string `json:"pages"`
	Title *string  `json:"title"`
	Error *string  `json:"error"`
}

func goResult(file string) extraction {
	pages, title, hasTitle, err := Extract(file)
	r := extraction{File: file}
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
		return "nil"
	}
	return fmt.Sprintf("%q", *s)
}

// diff lists the differences between the expected result and ours.
func diff(want, got extraction) []string {
	name := filepath.Base(want.File)
	if (want.Error != nil) != (got.Error != nil) {
		return []string{fmt.Sprintf("%s: expected error %s, got error %s", name, show(want.Error), show(got.Error))}
	}
	if want.Error != nil {
		return nil
	}
	var out []string
	if show(want.Title) != show(got.Title) {
		out = append(out, fmt.Sprintf("%s: title\n  want: %s\n  got:  %s", name, show(want.Title), show(got.Title)))
	}
	if len(want.Pages) != len(got.Pages) {
		return append(out, fmt.Sprintf("%s: expected %d pages, got %d", name, len(want.Pages), len(got.Pages)))
	}
	for i := range want.Pages {
		if want.Pages[i] != got.Pages[i] {
			out = append(out, fmt.Sprintf("%s page %d differs\n  want: %q\n  got:  %q", name, i+1, want.Pages[i], got.Pages[i]))
		}
	}
	return out
}

func compare(t *testing.T, want, got extraction) {
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
	var want []extraction
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		w.File = filepath.Join("testdata", "corpus", w.File)
		compare(t, w, goResult(w.File))
	}
}

// TestCorpusExtra runs any PDFs in PDFTEXT_EXTRA (a directory) through
// Extract and fails on an internal error; it is a tool for checking
// real-world files locally.
func TestCorpusExtra(t *testing.T) {
	dir := os.Getenv("PDFTEXT_EXTRA")
	if dir == "" {
		t.Skip("PDFTEXT_EXTRA not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if len(files) == 0 {
		t.Skip("no PDFs in PDFTEXT_EXTRA")
	}
	failed := 0
	for _, f := range files {
		r := goResult(f)
		if r.Error != nil {
			failed++
		}
		switch {
		case r.Error != nil && strings.HasPrefix(*r.Error, "pdftext: internal error"):
			t.Errorf("%s: %s", filepath.Base(f), *r.Error)
		case r.Error != nil:
			t.Logf("%s: %s", filepath.Base(f), *r.Error)
		default:
			t.Logf("%s: %d pages, title %s", filepath.Base(f), len(r.Pages), show(r.Title))
		}
	}
	t.Logf("%d/%d files extracted", len(files)-failed, len(files))
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
