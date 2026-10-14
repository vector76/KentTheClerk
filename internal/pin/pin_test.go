package pin

import (
	"strings"
	"testing"
)

const hash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, ok, err := Read(root); ok || err != nil {
		t.Fatalf("no pin: got ok=%v err=%v", ok, err)
	}
	want := Pin{Version: "0.9.0", Hash: hash}
	if err := Write(root, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(root)
	if !ok || err != nil || got != want {
		t.Fatalf("got %v ok=%v err=%v, want %v", got, ok, err, want)
	}
	if err := Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("removing a missing pin: %v", err)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, text := range []string{
		"",
		"system_version: 0.9.0\n",
		"system_hash: " + hash + "\n",
		"system_version: 0.9.0\nsystem_hash: sha256:short\n",
		"system_version: 0.9.0\nsystem_hash: " + hash + "\nsystem_verison: 0.8.0\n",
		"system_version 0.9.0\nsystem_hash: " + hash + "\n",
	} {
		if _, err := Parse(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestParseCRLF(t *testing.T) {
	p, err := Parse(strings.ReplaceAll(Format(Pin{"0.9.0", hash}), "\n", "\r\n"))
	if err != nil || p.Version != "0.9.0" || p.Hash != hash {
		t.Fatalf("got %v, %v", p, err)
	}
}
