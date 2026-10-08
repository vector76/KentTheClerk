// Package mimetypes reproduces mimetypes.guess_type(name)[0] from CPython 3.13: the built-in table,
// then on Windows the "Content Type" values of the registry's file-extension keys, then any of the
// mime.types files Python knows about that exist. So the Go kb reports the media type the Python kb
// reported on the same machine.
package mimetypes

import (
	"bufio"
	"os"
	"strings"
	"sync"

	"kenttheclerk/internal/py"
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
		if py.IsFile(f) {
			readFile(f)
		}
	}
}

// readFile is MimeTypes.read(filename): a mime.types file.
func readFile(name string) {
	f, err := os.Open(name)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		words := py.Fields(sc.Text())
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

// GuessType is mimetypes.guess_type(name)[0] for a file name; "" stands for None.
func GuessType(name string) string {
	once.Do(load)
	path := name
	splitext := py.Splitext
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
	} else if py.Windows && len(path) >= 2 && path[1] == ':' {
		path = path[2:]
	}
	base, ext := splitext(path)
	for {
		s, ok := defaultSuffixMap[py.Lower(ext)]
		if !ok {
			break
		}
		base, ext = splitext(base + s)
	}
	if _, ok := defaultEncodings[ext]; ok {
		base, ext = splitext(base)
	}
	return types[py.Lower(ext)]
}

// urlScheme is the scheme urllib.parse.urlparse finds, and the path part after it.
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
