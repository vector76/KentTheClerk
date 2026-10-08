// Package htmltext is a byte-exact Go port of the html-v1 text extraction in kb/ingest.py
// (class _HTMLText and extract_html up to chunk_units), including the CPython 3.13.1
// html.parser behaviour it depends on.
package htmltext

import (
	"strings"
	"unicode/utf8"
)

// Extract reproduces kb/ingest.py extract_html up to (not including) chunk_units:
// raw bytes -> decode (utf-8-sig, else latin-1) -> _HTMLText feed -> post-processing -> lines.
// title is p.title, and hasTitle is false when Python's `(p.title or None)` would be None.
//
// For the inputs on which Python's feed() raises (see ExtractErr), Extract panics with the
// *AssertionError or *ValueError, the counterpart of the uncaught Python exception.
func Extract(raw []byte) (lines []string, title string, hasTitle bool) {
	lines, title, hasTitle, err := ExtractErr(raw)
	if err != nil {
		panic(err)
	}
	return lines, title, hasTitle
}

// ExtractErr is Extract, returning an error instead of panicking in exactly the cases where
// CPython's feed() raises:
//   - *AssertionError: a "<![" that is not followed by a name, or whose name is not one of
//     temp/cdata/ignore/include/rcdata/if/else/endif, before the input ends;
//   - *ValueError: a decimal character reference of more than 4300 digits that html.unescape
//     converts (in text, or in an attribute value of a complete start tag).
//
// Msg carries Python's exception message.
func ExtractErr(raw []byte) (lines []string, title string, hasTitle bool, err error) {
	h := &htmlText{}
	src := decode(raw)
	p := &parser{raw: src, h: h, sc: scanner{s: src}}
	if err := run(p); err != nil {
		return nil, "", false, err
	}
	lines = postprocess(h.out)
	return lines, string(h.title), len(h.title) > 0, nil
}

func run(p *parser) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *AssertionError:
				err = e
				return
			case *ValueError:
				err = e
				return
			}
			panic(r)
		}
	}()
	p.goahead()
	return nil
}

// decode is raw.decode("utf-8-sig"), falling back to raw.decode("latin-1").
func decode(raw []byte) []rune {
	b := raw
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	if utf8.Valid(b) {
		return []rune(string(b))
	}
	out := make([]rune, len(raw))
	for i, c := range raw {
		out[i] = rune(c)
	}
	return out
}

// htmlText is the _HTMLText subclass.
type htmlText struct {
	out     []rune
	title   []rune // None and "" both end as hasTitle == false
	inTitle bool
	skip    int
	heading bool
}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "ul": true, "ol": true, "tr": true,
	"table": true, "section": true, "article": true, "header": true, "footer": true,
	"pre": true, "blockquote": true, "hr": true, "main": true, "nav": true, "aside": true,
	"dd": true, "dt": true, "figure": true, "figcaption": true,
}

var skipTags = map[string]bool{"script": true, "style": true, "noscript": true, "head": true, "template": true}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

func (h *htmlText) handleStarttag(tag string) {
	switch {
	case skipTags[tag]:
		h.skip++
	case tag == "title":
		h.inTitle = true
	case headingLevel(tag) > 0:
		h.out = append(h.out, []rune("\n\n"+strings.Repeat("#", headingLevel(tag))+" ")...)
		h.heading = true
	case tag == "td" || tag == "th":
		h.out = append(h.out, []rune(" | ")...)
	case blockTags[tag]:
		if tag == "br" {
			h.out = append(h.out, '\n')
		} else {
			h.out = append(h.out, '\n', '\n')
		}
	}
}

func (h *htmlText) handleEndtag(tag string) {
	switch {
	case skipTags[tag]:
		h.skip = max(0, h.skip-1)
	case tag == "title":
		h.inTitle = false
	case headingLevel(tag) > 0:
		h.out = append(h.out, '\n', '\n')
		h.heading = false
	case blockTags[tag]:
		h.out = append(h.out, '\n', '\n')
	}
}

func (h *htmlText) handleData(data []rune) {
	if h.skip > 0 {
		return
	}
	if h.inTitle {
		h.title = append(h.title, pyStrip(data)...)
		return
	}
	if h.heading {
		h.out = append(h.out, pyStrip(data)...)
		return
	}
	// re.sub(r"\s+", " ", data)
	for i := 0; i < len(data); {
		if isSpace(data[i]) {
			for i < len(data) && isSpace(data[i]) {
				i++
			}
			h.out = append(h.out, ' ')
			continue
		}
		h.out = append(h.out, data[i])
		i++
	}
}

func isSpTab(r rune) bool { return r == ' ' || r == '\t' }

// postprocess is the tail of extract_html:
//
//	text = re.sub(r"[ \t]+\n", "\n", text)
//	text = re.sub(r"\n[ \t]+", "\n", text)
//	text = re.sub(r"\n{3,}", "\n\n", text).strip("\n")
//	lines = [l.strip() for l in text.split("\n")]
func postprocess(text []rune) []string {
	// [ \t]+\n -> \n: drop every run of spaces/tabs that ends right before a newline.
	a := make([]rune, 0, len(text))
	for _, r := range text {
		if r == '\n' {
			for len(a) > 0 && isSpTab(a[len(a)-1]) {
				a = a[:len(a)-1]
			}
		}
		a = append(a, r)
	}
	// \n[ \t]+ -> \n: drop every run of spaces/tabs that starts right after a newline.
	b := make([]rune, 0, len(a))
	for i := 0; i < len(a); i++ {
		b = append(b, a[i])
		if a[i] == '\n' {
			for i+1 < len(a) && isSpTab(a[i+1]) {
				i++
			}
		}
	}
	// \n{3,} -> \n\n
	c := make([]rune, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] == '\n' {
			j := i
			for j < len(b) && b[j] == '\n' {
				j++
			}
			if j-i >= 3 {
				c = append(c, '\n', '\n')
			} else {
				c = append(c, b[i:j]...)
			}
			i = j
			continue
		}
		c = append(c, b[i])
		i++
	}
	// .strip("\n")
	lo, hi := 0, len(c)
	for lo < hi && c[lo] == '\n' {
		lo++
	}
	for hi > lo && c[hi-1] == '\n' {
		hi--
	}
	c = c[lo:hi]
	// split("\n") and strip each line
	var lines []string
	start := 0
	for i := 0; i <= len(c); i++ {
		if i == len(c) || c[i] == '\n' {
			lines = append(lines, string(pyStrip(c[start:i])))
			start = i + 1
		}
	}
	return lines
}
