package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kenttheclerk/internal/pin"
)

func testKB(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"attested", "evidence"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	if version != "" {
		if err := pin.Write(root, pin.Pin{Version: version, Hash: "sha256:" + strings.Repeat("0", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestPinForSeveral: with several knowledge bases, the pin comes from the one --kb names; pins that agree
// need no --kb; pins that differ refuse a command without one, except session-start, which takes the
// highest (S-ZBVAHS).
func TestPinForSeveral(t *testing.T) {
	a, b, c := testKB(t, "0.10.0"), testKB(t, "0.11.0"), testKB(t, "0.10.0")
	sep := string(os.PathListSeparator)

	t.Setenv(kbEnv, a+sep+c)
	if p, _, err := pinFor([]string{"check"}); err != nil || p == nil || p.Version != "0.10.0" {
		t.Fatalf("agreeing pins: %v %v", p, err)
	}
	t.Setenv(kbEnv, a+sep+b)
	if _, _, err := pinFor([]string{"check"}); err == nil || !strings.Contains(err.Error(), "--kb") {
		t.Fatalf("differing pins without --kb: %v", err)
	}
	if p, _, err := pinFor([]string{"links", "--kb", b, "S-0001"}); err != nil || p.Version != "0.11.0" {
		t.Fatalf("--kb b: %v %v", p, err)
	}
	if p, _, err := pinFor([]string{"check", "--kb=" + a}); err != nil || p.Version != "0.10.0" {
		t.Fatalf("--kb=a: %v %v", p, err)
	}
	if p, _, err := pinFor([]string{sessionStartArg}); err != nil || p.Version != "0.11.0" {
		t.Fatalf("session-start: %v %v", p, err)
	}
	// one pinned, one not, also differ
	t.Setenv(kbEnv, a+sep+testKB(t, ""))
	if _, _, err := pinFor([]string{"check"}); err == nil {
		t.Fatal("a pinned and an unpinned knowledge base were taken to agree")
	}
}
