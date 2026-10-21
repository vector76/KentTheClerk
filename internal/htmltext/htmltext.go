// Package htmltext is the html-v1 text extraction: it turns an HTML document into lines of
// text and a title, with a tolerant tokenizer. Its output for every input, malformed markup
// included, is fixed byte for byte within a system version, since ingested projections are
// frozen; a version that fixes a defect in it may change it, and each ingest records the
// system version it was made under, which tells the outputs apart.
package htmltext

import (
	"strings"
	"unicode/utf8"
)

// Extract turns raw bytes into lines (before chunking):
// raw bytes -> decode (UTF-8 with optional BOM, else Latin-1) -> tokenize -> post-processing
// -> lines. title is the text inside title elements, each piece stripped of surrounding
// whitespace and the pieces joined, and hasTitle is false when it is empty.
//
// For the inputs on which extraction fails (see ExtractErr), Extract panics with the
// *AssertionError or *ValueError.
func Extract(raw []byte) (lines []string, title string, hasTitle bool) {
	lines, title, hasTitle, err := ExtractErr(raw)
	if err != nil {
		panic(err)
	}
	return lines, title, hasTitle
}

// ExtractErr is Extract, returning an error instead of panicking in exactly these cases:
//   - *AssertionError: a "<![" that is not followed by a name, or whose name is not one of
//     temp/cdata/ignore/include/rcdata/if/else/endif, before the input ends;
//   - *ValueError: a decimal character reference of more than 4300 digits that unescape
//     converts (in text, or in an attribute value of a complete start tag).
//
// Msg carries the error message.
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

// decode reads raw as UTF-8 after dropping one leading BOM, or, when that is not valid
// UTF-8, reads all of raw (BOM bytes included) as Latin-1.
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

// htmlText receives the tokenizer's tags and text and builds the output text and title.
type htmlText struct {
	out     []rune
	title   []rune // empty means no title
	inTitle bool
	skip    int
	heading bool
	// headingSpace is set while in a heading when whitespace has followed the last heading
	// text written; it becomes one space before the next heading text.
	headingSpace bool
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
		h.headingSpace = false
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
		h.headingSpace = false
	case blockTags[tag]:
		h.out = append(h.out, '\n', '\n')
	}
}

func (h *htmlText) handleData(data []rune) {
	if h.skip > 0 {
		return
	}
	if h.inTitle {
		h.title = append(h.title, stripSpace(data)...)
		return
	}
	if h.heading {
		// Each piece is stripped of surrounding whitespace, and whitespace between two
		// pieces becomes one space, unless what was written last already ends in whitespace
		// (the heading marker, a cell separator, a break).
		text := stripSpace(data)
		if len(text) == 0 {
			h.headingSpace = h.headingSpace || len(data) > 0
			return
		}
		if (h.headingSpace || isSpace(data[0])) && len(h.out) > 0 && !isSpace(h.out[len(h.out)-1]) {
			h.out = append(h.out, ' ')
		}
		h.out = append(h.out, text...)
		h.headingSpace = isSpace(data[len(data)-1])
		return
	}
	// every run of whitespace becomes one space
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

// postprocess turns the collected text into lines: spaces and tabs next to a newline are
// dropped, runs of three or more newlines become two, leading and trailing newlines are
// removed, and the text is split at newlines with each line stripped of whitespace.
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
	// drop leading and trailing newlines
	lo, hi := 0, len(c)
	for lo < hi && c[lo] == '\n' {
		lo++
	}
	for hi > lo && c[hi-1] == '\n' {
		hi--
	}
	c = c[lo:hi]
	// split at newlines and strip each line
	var lines []string
	start := 0
	for i := 0; i <= len(c); i++ {
		if i == len(c) || c[i] == '\n' {
			lines = append(lines, string(stripSpace(c[start:i])))
			start = i + 1
		}
	}
	return lines
}
