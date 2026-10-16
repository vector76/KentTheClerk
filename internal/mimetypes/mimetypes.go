// Package mimetypes guesses a file's media type from its name. The types come from a built-in table,
// then on Windows the "Content Type" values of the registry's file-extension keys, then whichever of
// the usual mime.types files exist; a later source overrides an earlier one for the same extension.
package mimetypes

import (
	"bufio"
	"os"
	"strings"
	"sync"

	"kenttheclerk/internal/dyn"
)

var (
	once  sync.Once
	types map[string]string
)

func addType(typ, ext string) { types[ext] = typ }

func load() {
	types = map[string]string{}
	for _, kv := range defaultTypes {
		addType(kv[1], kv[0])
	}
	readRegistry(addType)
	for _, f := range knownFiles {
		if dyn.IsFile(f) {
			readFile(f)
		}
	}
}

// readFile adds the types in a mime.types file: on each line a type and its extensions, with "#"
// starting a comment.
func readFile(name string) {
	f, err := os.Open(name)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		words := dyn.Fields(sc.Text())
		for i, w := range words {
			if strings.HasPrefix(w, "#") {
				words = words[:i]
				break
			}
		}
		if len(words) == 0 {
			continue
		}
		for _, suff := range words[1:] {
			addType(words[0], "."+suff)
		}
	}
}

// GuessType is the media type for a file name or URL, or "" when there is none. Suffixes like .tgz
// stand for two (.tar.gz), a compression suffix (.gz, .Z, .bz2, .xz, .br) is skipped, the extension
// is looked up case-insensitively, and a data: URL gives its own type.
func GuessType(name string) string {
	once.Do(load)
	path := name
	splitext := dyn.Splitext
	if scheme, rest, ok := urlScheme(name); ok && len(scheme) > 1 {
		if scheme == "data" {
			comma := strings.Index(rest, ",")
			if comma < 0 {
				return ""
			}
			typ := rest[:comma]
			if semi := strings.Index(rest[:comma], ";"); semi >= 0 {
				typ = rest[:semi]
			}
			if strings.Contains(typ, "=") || !strings.Contains(typ, "/") {
				typ = "text/plain"
			}
			return typ
		}
		path = rest
		splitext = posixSplitext
	} else if dyn.Windows && len(path) >= 2 && path[1] == ':' {
		path = path[2:]
	}
	base, ext := splitext(path)
	for {
		s, ok := defaultSuffixMap[dyn.Lower(ext)]
		if !ok {
			break
		}
		base, ext = splitext(base + s)
	}
	if _, ok := defaultEncodings[ext]; ok {
		base, ext = splitext(base)
	}
	return types[dyn.Lower(ext)]
}

// urlScheme splits a URL into its scheme, lower-cased, and its path, without the authority, query and
// fragment.
func urlScheme(url string) (string, string, bool) {
	i := strings.Index(url, ":")
	if i <= 0 {
		return "", "", false
	}
	c0 := url[0]
	if !(c0 >= 'a' && c0 <= 'z' || c0 >= 'A' && c0 <= 'Z') {
		return "", "", false
	}
	for _, c := range url[:i] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return "", "", false
		}
	}
	rest := url[i+1:]
	if strings.HasPrefix(rest, "//") {
		j := strings.IndexAny(rest[2:], "/?#")
		if j < 0 {
			rest = ""
		} else {
			rest = rest[2+j:]
		}
	}
	if j := strings.IndexAny(rest, "?#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.ToLower(url[:i]), rest, true
}

func posixSplitext(p string) (string, string) {
	sep := strings.LastIndex(p, "/")
	dot := strings.LastIndex(p, ".")
	if dot > sep {
		for k := sep + 1; k < dot; k++ {
			if p[k] != '.' {
				return p[:dot], p[dot:]
			}
		}
	}
	return p, ""
}
