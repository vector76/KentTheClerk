package kbset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitRef(t *testing.T) {
	if k, s, ok := SplitRef(" K-4QX7M2PT9WNB/S-Y17H90 "); !ok || k != "K-4QX7M2PT9WNB" || s != "S-Y17H90" {
		t.Fatalf("%q %q %v", k, s, ok)
	}
	for _, bad := range []string{"S-Y17H90", "K-4QX7M2PT9WN/S-Y17H90", "K-4QX7M2PT9WNI/S-Y17H90", "K-4QX7M2PT9WNB:S-Y17H90"} {
		if _, _, ok := SplitRef(bad); ok {
			t.Errorf("%q taken as a reference", bad)
		}
	}
}

func TestReadIDAndLocate(t *testing.T) {
	root := t.TempDir()
	if _, ok, err := ReadID(root); ok || err != nil {
		t.Fatalf("no file: %v %v", ok, err)
	}
	write := func(s string) {
		if err := os.WriteFile(IDPath(root), []byte(s), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write("K-4QX7M2PT9WNB\r\n")
	if id, ok, err := ReadID(root); !ok || err != nil || id != "K-4QX7M2PT9WNB" {
		t.Fatalf("%q %v %v", id, ok, err)
	}
	if f, _ := Locate("k-4qx7m2pt9wnb", []string{t.TempDir(), root}); f != root {
		t.Fatalf("Locate among the project's: %q", f)
	}
	for _, bad := range []string{"", "K-4QX7M2PT9WNB\nK-0000000000AB\n", "K-SHORT\n"} {
		write(bad)
		if _, _, err := ReadID(root); err == nil {
			t.Errorf("%q read without error", bad)
		}
	}
}

func TestSplitList(t *testing.T) {
	sep := string(os.PathListSeparator)
	a, b := filepath.Join("x", "a"), filepath.Join("y", "b")
	got := SplitList(a + sep + " " + sep + b + sep)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("%q", got)
	}
}
