// Package htmltext is the html-v1 text extraction: it turns an HTML document into lines of
// text and a title, with a tolerant tokenizer. Its output for every input, malformed markup
// included, is fixed byte for byte within a system version, since ingested projections are
// frozen; a version that fixes a defect in it may change it, and each ingest records the
// system version it was made under, which tells the outputs apart.
package htmltext

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Extract turns raw bytes into lines (before chunking):
// raw bytes -> decode (UTF-8 with optional BOM, invalid bytes as windows-1252; UTF-16 after its
// BOM) -> tokenize -> post-processing
// -> lines. title is the text inside the first title element that has any, each piece stripped
// of surrounding whitespace and whitespace between two pieces made one space, and hasTitle is
// false when it is empty. A title in the head counts, and one inside svg, or inside a skipped
// element other than the head, does not.
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

// decode reads raw as UTF-8 after dropping one leading BOM. Each byte that does not begin a
// valid UTF-8 sequence is read alone as windows-1252, as browsers read a page labelled
// Latin-1, so a document in Latin-1 or windows-1252, or a UTF-8 one with a stray legacy byte
// or a sequence cut short, keeps every character that is valid UTF-8, and 0x80-0x9F give the
// euro sign, curly quotes and dashes rather than C1 control characters.
//
// A document that begins with a UTF-16 BOM, little-endian (FF FE) or big-endian (FE FF), is
// read as UTF-16 instead, as browsers read it whatever else it declares; a surrogate without
// its pair, and a last odd byte, become U+FFFD.
func decode(raw []byte) []rune {
	b := raw
	if len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF) {
		return decodeUTF16(b[2:], b[0] == 0xFE)
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	if utf8.Valid(b) {
		return []rune(string(b))
	}
	out := make([]rune, 0, len(b))
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n == 1 {
			r = rune(b[0])
			if b[0] >= 0x80 && b[0] <= 0x9F {
				// The windows-1252 table numeric references use; its five undefined bytes
				// keep their own code point.
				r = invalidCharrefs[int(b[0])]
			}
		}
		out = append(out, r)
		b = b[n:]
	}
	return out
}

// decodeUTF16 reads b, the bytes after the BOM, as UTF-16 code units in the byte order given.
func decodeUTF16(b []byte, bigEndian bool) []rune {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
		}
	}
	out := utf16.Decode(units)
	if len(b)%2 == 1 {
		out = append(out, utf8.RuneError)
	}
	return out
}

// htmlText receives the tokenizer's tags and text and builds the output text and title.
type htmlText struct {
	out     []rune
	title   []rune // empty means no title
	inTitle bool
	// titleSpace is set while in a title when whitespace has followed the last title text
	// taken; it becomes one space before the next title text.
	titleSpace bool
	// titleDone is set when a title element that gave the title text ends; later title
	// elements are not the document's title.
	titleDone bool
	skip      int
	// head counts the open head elements among the skip elements: a title in the head is
	// the document's title, although the rest of the head is not text.
	head int
	// headDone is set once a head start tag no longer opens a head: a head has been opened, or
	// a start tag other than html, a head end tag or text other than whitespace came first. A
	// later head start tag is ignored, as browsers ignore it, so head is never more than 1.
	headDone bool
	// svg counts the open svg elements; a title inside one labels an image and is neither
	// the document's title nor text.
	svg     int
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

// headTags are the elements a head may contain. Any other start tag, or text other than
// whitespace, directly in a head whose end tag was left out ends the head there, as it does in
// a browser, and what follows is the body's.
var headTags = map[string]bool{
	"base": true, "basefont": true, "bgsound": true, "link": true, "meta": true, "title": true,
	"noscript": true, "noframes": true, "style": true, "script": true, "template": true,
}

// isHTMLSpace is HTML's whitespace: tab, line feed, form feed, carriage return and space.
func isHTMLSpace(r rune) bool {
	return r == '\t' || r == '\n' || r == '\f' || r == '\r' || r == ' '
}

func allHTMLSpace(s []rune) bool {
	for _, r := range s {
		if !isHTMLSpace(r) {
			return false
		}
	}
	return true
}

// endOpenHead ends an open head when nothing else skipped is open inside it.
func (h *htmlText) endOpenHead() {
	if h.head > 0 && h.skip == h.head {
		h.head--
		h.skip--
	}
}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

func (h *htmlText) handleStarttag(tag string) {
	switch {
	case tag == "head":
		if h.headDone {
			return
		}
	case tag != "html" && !headTags[tag]:
		h.endOpenHead()
	}
	if tag != "html" {
		h.headDone = true
	}
	switch tag {
	case "head":
		h.head++
	case "svg":
		h.svg++
	}
	switch {
	case skipTags[tag]:
		h.skip++
	case tag == "title":
		// A title start tag inside an open title is one more tag between two pieces, so the
		// whitespace before it still counts.
		if !h.inTitle {
			h.titleSpace = false
		}
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
	switch tag {
	case "head":
		// A head end tag ends only an open head; any other is ignored.
		h.headDone = true
		if h.head == 0 {
			return
		}
		h.head--
	case "svg":
		h.svg = max(0, h.svg-1)
	}
	switch {
	case skipTags[tag]:
		h.skip = max(0, h.skip-1)
	case tag == "title":
		h.inTitle = false
		h.titleDone = h.titleDone || len(h.title) > 0
	case headingLevel(tag) > 0:
		h.out = append(h.out, '\n', '\n')
		h.heading = false
		h.headingSpace = false
	case blockTags[tag]:
		h.out = append(h.out, '\n', '\n')
	}
}

func (h *htmlText) handleData(data []rune) {
	// Text other than whitespace outside every skipped element but a head begins the body.
	if !h.inTitle && h.skip == h.head && !allHTMLSpace(data) {
		h.endOpenHead()
		h.headDone = true
	}
	// A title is the document's outside svg, when every open skip element is a head.
	if h.inTitle && h.svg == 0 && h.skip <= h.head {
		if !h.titleDone {
			// As in a heading, each piece is stripped of surrounding whitespace, and
			// whitespace between two pieces becomes one space.
			text := stripSpace(data)
			if len(text) == 0 {
				h.titleSpace = h.titleSpace || len(data) > 0
				return
			}
			if (h.titleSpace || isSpace(data[0])) && len(h.title) > 0 {
				h.title = append(h.title, ' ')
			}
			h.title = append(h.title, text...)
			h.titleSpace = isSpace(data[len(data)-1])
		}
		return
	}
	if h.skip > 0 || h.inTitle {
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
