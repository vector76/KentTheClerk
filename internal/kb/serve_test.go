package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An ingested original reaches the browser without running in kb serve's origin: HTML, SVG, XML and
// script are not served under a type a browser renders as a page, every original carries a sandbox
// policy and nosniff, and a PDF or a text file is still shown inline as it is.
func TestServeOriginalsNeverRunAsActiveContent(t *testing.T) {
	root := newTestKB(t)
	dir := t.TempDir()
	get := func(name string, content []byte, extractor ...string) *httptest.ResponseRecorder {
		t.Helper()
		src := filepath.Join(dir, name)
		if err := os.WriteFile(src, content, 0o666); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"ingest", src, "--user", "alice"}, extractor...)
		mustRun(t, args...)
		sum := sha256.Sum256(content)
		rec := httptest.NewRecorder()
		(&kbServer{root}).ServeHTTP(rec, httptest.NewRequest("GET", "/o/"+hex.EncodeToString(sum[:])+filepath.Ext(name), nil))
		if rec.Code != 200 {
			t.Fatalf("/o/ for %s: status %d", name, rec.Code)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("/o/ for %s: X-Content-Type-Options = %q, want nosniff", name, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
			t.Errorf("/o/ for %s: Content-Security-Policy = %q, want a sandbox", name, got)
		}
		return rec
	}

	active := []struct{ name, content string }{
		{"page.html", "<html><head><title>Page</title></head><body><h1>Page</h1><p>text</p><script>alert(document.domain)</script></body></html>\n"},
		{"drawing.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.domain)</script><circle r="4"/></svg>` + "\n"},
		{"feed.xml", `<html xmlns="http://www.w3.org/1999/xhtml"><script>alert(document.domain)</script></html>` + "\n"},
		{"code.js", "alert(document.domain)\n"},
	}
	for _, a := range active {
		rec := get(a.name, []byte(a.content))
		ctype := strings.ToLower(rec.Header().Get("Content-Type"))
		download := strings.HasPrefix(strings.ToLower(rec.Header().Get("Content-Disposition")), "attachment")
		if !strings.HasPrefix(ctype, "text/plain") && !download {
			t.Errorf("/o/ for %s is served inline as %q, which a browser renders as active content", a.name, ctype)
		}
		if rec.Body.String() != a.content {
			t.Errorf("/o/ for %s does not carry the original's bytes:\n%s", a.name, rec.Body.String())
		}
	}

	pdf := []byte("%PDF-1.4\n% not a real document, stored as it is\n%%EOF\n")
	text := []byte("Plain notes, to read in the browser.\n")
	for _, o := range []struct {
		name, ctype string
		content     []byte
		extractor   []string
	}{
		{"report.pdf", "application/pdf", pdf, []string{"--extractor", "opaque-v1"}},
		{"notes.txt", "text/plain", text, nil},
	} {
		rec := get(o.name, o.content, o.extractor...)
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, o.ctype) {
			t.Errorf("/o/ for %s: Content-Type = %q, want %s", o.name, got, o.ctype)
		}
		if got := rec.Header().Get("Content-Disposition"); strings.HasPrefix(strings.ToLower(got), "attachment") {
			t.Errorf("/o/ for %s is sent as a download (%q), not shown inline", o.name, got)
		}
		if rec.Body.String() != string(o.content) {
			t.Errorf("/o/ for %s does not carry the original's bytes:\n%s", o.name, rec.Body.String())
		}
	}
}
