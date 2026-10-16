package pdftext

import (
	"strconv"
	"strings"
	"sync"
)

// pstr is text: a sequence of code points, where surrogate halves are
// allowed (a UTF-16 decode keeps lone surrogates).
type pstr []rune

// key turns a pstr into a map key that keeps surrogates distinct.
func (s pstr) key() string {
	b := make([]byte, 0, 3*len(s))
	for _, r := range s {
		b = append(b, byte(r>>16), byte(r>>8), byte(r))
	}
	return string(b)
}

func keyToPstr(k string) pstr {
	r := make(pstr, 0, len(k)/3)
	for i := 0; i+3 <= len(k); i += 3 {
		r = append(r, rune(k[i])<<16|rune(k[i+1])<<8|rune(k[i+2]))
	}
	return r
}

func (s pstr) eq(t pstr) bool {
	if len(s) != len(t) {
		return false
	}
	for i := range s {
		if s[i] != t[i] {
			return false
		}
	}
	return true
}

// goString converts to UTF-8; surrogate halves become U+FFFD.
func (s pstr) goString() string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			r = 0xFFFD
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parseHexSeq(s string) pstr {
	if s == "" {
		return pstr{}
	}
	parts := strings.Split(s, ".")
	out := make(pstr, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 16, 32)
		if err != nil {
			panic("pdftext: bad table entry " + s)
		}
		out[i] = rune(v)
	}
	return out
}

type coreFont struct {
	flags  int64
	widths map[string]float64 // pstr key, or "default"
}

var (
	tablesOnce  sync.Once
	charsets    map[string][]pstr // "/WinAnsiEncoding" -> 256 entries
	adobeGlyphs map[string]pstr   // "/A" -> "A"
	coreFonts   map[string]*coreFont
)

func loadTables() {
	tablesOnce.Do(func() {
		charsets = map[string][]pstr{}
		for _, line := range strings.Split(charsetTablesData, "\n") {
			if line == "" {
				continue
			}
			f := strings.Split(line, " ")
			if len(f) != 257 {
				panic("pdftext: bad charset line")
			}
			lst := make([]pstr, 256)
			for i := 0; i < 256; i++ {
				lst[i] = parseHexSeq(f[i+1])
			}
			charsets[f[0]] = lst
		}
		adobeGlyphs = map[string]pstr{}
		for _, line := range strings.Split(adobeGlyphsData, "\n") {
			if line == "" {
				continue
			}
			i := strings.IndexByte(line, ' ')
			adobeGlyphs[line[:i]] = parseHexSeq(line[i+1:])
		}
		coreFonts = map[string]*coreFont{}
		var cur *coreFont
		for _, line := range strings.Split(coreFontsData, "\n") {
			if line == "" {
				cur = nil
				continue
			}
			i := strings.LastIndexByte(line, ' ')
			a, b := line[:i], line[i+1:]
			n, err := strconv.ParseInt(b, 10, 64)
			if err != nil {
				panic("pdftext: bad core font line")
			}
			if cur == nil {
				cur = &coreFont{flags: n, widths: map[string]float64{}}
				coreFonts[a] = cur
				continue
			}
			if a == "default" {
				cur.widths["default"] = float64(n)
			} else {
				cur.widths[parseHexSeq(a).key()] = float64(n)
			}
		}
	})
}
