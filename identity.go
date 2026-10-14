// Package kent holds the identity of the system (S-0030): the version and the content hash every
// recorded act is stamped with. This file is the one place that decides how they are computed.
//
// PENDING THE HUMAN'S DECISION (to be recorded in the design record): how the hash is computed for a
// compiled binary. The design implemented here: the hash is computed over the files the binary is built
// from, which are embedded in it at build time — the Go source of the executable (go.mod, go.sum, and
// every .go file, tests included, of the root package, cmd/kb and the internal/ packages; not the
// launcher in cmd/launcher, which is outside the system it dispatches to, nor the parity harness, the
// generators or test data) and the shipped rule files (rules/*.md, procedure.md,
// README.md, front-ends/*/*). identity_test.go fails if Go source is added where the patterns below do
// not reach it. The hash is fixed when the binary is built, the same on every platform, and
// computed the way the former Python recorder computed it (sha256 over "path\0content\0" for each file in
// path order, CRLF normalised to LF). The version comes from the release tag, set at build time with
//
//	go build -ldflags "-X kenttheclerk.version=0.9.0" ./cmd/kb
//
// and falls back to defaultVersion for a build without a tag.
//
// A development build (S-0110) is made with a flag at build time, never a switch at run time, so a release
// build has no development mode at all:
//
//	go build -ldflags "-X kenttheclerk.dev=true -X kenttheclerk.commit=<sha> -X kenttheclerk.dirty=true" ./cmd/kb
//
// (build-dev.ps1 and build-dev.sh fill these in from git). Its version is the one it is becoming with a
// pre-release suffix, "0.9.0-dev" (S-0112), and its acts are stamped with the commit it was built from and
// whether the working tree was dirty (S-0109).
package kent

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

//go:embed go.mod go.sum *.go cmd/kb/*.go internal/*/*.go rules/*.md procedure.md README.md front-ends/*/*
var systemFS embed.FS

// defaultVersion is what an untagged build reports: the version of this source. A release tag must
// name the same version (the release workflow checks). It is bumped when work on the next version
// starts (S-0112), and it changes whenever the rules do.
const defaultVersion = "0.10.0"

// devSuffix is the pre-release suffix a development build adds to its version.
const devSuffix = "-dev"

// Set by the build with -X: version from the release tag (a leading "v" is dropped); dev, commit and
// dirty for a development build only.
var (
	version string
	dev     string
	commit  string
	dirty   string
)

// Dev reports whether this is a development build.
func Dev() bool { return dev == "true" }

// Version is system_version.
func Version() string {
	v := strings.TrimPrefix(version, "v")
	if v == "" {
		v = defaultVersion
	}
	if Dev() {
		v += devSuffix
	}
	return v
}

// Commit is the git commit a development build was built from ("" if unknown or not a development build).
func Commit() string { return commit }

// Dirty reports whether a development build's working tree had uncommitted changes.
func Dirty() bool { return dirty == "true" }

// Files are the paths the hash covers, relative to the repository root, in hash order.
func Files() []string {
	var out []string
	_ = fs.WalkDir(systemFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

var (
	hashOnce sync.Once
	hash     string
)

// Hash is system_hash: "sha256:" + hex.
func Hash() string {
	hashOnce.Do(func() {
		h := sha256.New()
		for _, p := range Files() {
			b, _ := systemFS.ReadFile(p)
			h.Write([]byte(p))
			h.Write([]byte{0})
			h.Write([]byte(strings.ReplaceAll(string(b), "\r\n", "\n")))
			h.Write([]byte{0})
		}
		hash = "sha256:" + hex.EncodeToString(h.Sum(nil))
	})
	return hash
}

// ReadFile reads one of the files built into this binary, by its path in the repository (e.g.
// "rules/record.md"). The documents the agent and the human read are served from here, so an installed
// version of Kent is the executable alone (S-0106, S-0107). Line endings come back as LF.
func ReadFile(p string) (string, error) {
	b, err := systemFS.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

// FilesHeading introduces the list printed by "kb version --files".
const FilesHeading = "files, as built into this binary:"
