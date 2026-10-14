package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kenttheclerk/internal/pin"
)

func TestCompareVersions(t *testing.T) {
	ordered := []string{"0.8.0", "0.9.0-dev", "0.9.0-rc.1", "0.9.0-rc.2", "0.9.0", "0.10.0-dev", "0.10.0", "1.0.0"}
	for i := range ordered {
		for j := range ordered {
			got := compareVersions(ordered[i], ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("compareVersions(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
	for _, bad := range []string{"0.9", "v0.9.0", "0.9.0-", "latest", "0.9.0 "} {
		if validVersion(bad) {
			t.Errorf("%q accepted as a version", bad)
		}
	}
}

func TestInstalled(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.8.0", "0.9.0-dev", "0.10.0", "notaversion"} {
		os.MkdirAll(filepath.Join(dir, "versions", v), 0o755)
		os.WriteFile(filepath.Join(dir, "versions", v, exeName()), nil, 0o755)
	}
	os.MkdirAll(filepath.Join(dir, "versions", "0.11.0"), 0o755) // no executable
	got := strings.Join(installed(dir), " ")
	if got != "0.10.0 0.9.0-dev 0.8.0" {
		t.Errorf("installed = %s", got)
	}
}

func TestChoose(t *testing.T) {
	versions := []string{"0.9.0-dev", "0.8.0"}
	pinned := &pin.Pin{Version: "0.8.0"}
	cases := []struct {
		name     string
		p        *pin.Pin
		env      string
		want     string
		errMatch string
	}{
		{"highest", nil, "", "0.9.0-dev", ""},
		{"setting", nil, "0.8.0", "0.8.0", ""},
		{"pin", pinned, "", "0.8.0", ""},
		{"pin and matching setting", pinned, "0.8.0", "0.8.0", ""},
		{"pin and other setting", pinned, "0.9.0-dev", "", "does not override"},
		{"pinned version missing", &pin.Pin{Version: "0.7.0"}, "", "", "not installed"},
		{"setting missing", nil, "0.7.0", "", "not installed"},
	}
	for _, c := range cases {
		got, err := choose(versions, c.p, "kent.pin", c.env, "/kent")
		if c.errMatch != "" {
			if err == nil || !strings.Contains(err.Error(), c.errMatch) {
				t.Errorf("%s: err = %v, want one mentioning %q", c.name, err, c.errMatch)
			}
			continue
		}
		if err != nil || got.version != c.want {
			t.Errorf("%s: got %v, %v; want %s", c.name, got, err, c.want)
		}
	}
	if _, err := choose(nil, nil, "", "", "/kent"); err == nil || !strings.Contains(err.Error(), "No version") {
		t.Errorf("nothing installed: err = %v", err)
	}
}

func TestMissingWarnsAgents(t *testing.T) {
	err := missing("This knowledge base is pinned to Kent 0.7.0", "0.7.0", []string{"0.8.0"}, "/kent")
	for _, want := range []string{"0.7.0", "Installed: 0.8.0", "Note for agents", "without their agreement"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err)
		}
	}
}
