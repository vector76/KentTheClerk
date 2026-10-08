// Package kent holds the identity of the system (S-0030): the version and the content hash every
// recorded act is stamped with. This file is the one place that decides how they are computed.
//
// PENDING THE HUMAN'S DECISION (to be recorded in the design record): how the hash is computed for a
// compiled binary. The design implemented here: the hash is computed over the files the binary is built
// from, which are embedded in it at build time — the Go source of the executable (go.mod, go.sum, and
// every .go file, tests included, of the root package, cmd/kb and the internal/ packages; not the
// parity harness, the generators or test data) and the shipped rule files (rules/*.md, procedure.md,
// README.md, front-ends/*/*). identity_test.go fails if Go source is added where the patterns below do
// not reach it. The hash is fixed when the binary is built, the same on every platform, and
// computed the way the former Python recorder computed it (sha256 over "path\0content\0" for each file in
// path order, CRLF normalised to LF). The version comes from the release tag, set at build time with
//
//	go build -ldflags "-X kenttheclerk.version=0.8.0" ./cmd/kb
//
// and falls back to defaultVersion for a build without a tag.
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
// name the same version (the release workflow checks), and it changes whenever the rules do.
const defaultVersion = "0.8.0"

// version is set from the release tag by the build ("-X kenttheclerk.version=..."); a leading "v" is dropped.
var version string

// Version is system_version.
func Version() string {
	v := strings.TrimPrefix(version, "v")
	if v == "" {
		return defaultVersion
	}
	return v
}

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

// FilesHeading introduces the list printed by "kb version --files".
const FilesHeading = "files, as built into this binary:"
