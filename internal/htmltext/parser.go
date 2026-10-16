package htmltext

import (
	"fmt"

	"kenttheclerk/internal/dyn"
)

// The tokenizer: a tolerant HTML tokenizer that unescapes character references in text and
// runs over the whole input once. Whatever it holds back at the end of the input is dropped:
// an incomplete tag, comment or declaration, or the final run of text when a '&' among its
// last 34 characters is followed by no whitespace or ';'. Comments, declarations and
// processing instructions are skipped without output.

// AssertionError is the error extraction stops with for markup the declaration scanner
// cannot scan, e.g. "<![" not followed by a name, or an unknown marked-section keyword like
// "<![foo[".
type AssertionError struct{ Msg string }

func (e *AssertionError) Error() string { return "AssertionError: " + e.Msg }

type sink interface {
	handleStarttag(tag string)
	handleEndtag(tag string)
	handleData(data []rune)
}

type parser struct {
	raw   []rune
	cdata string // the open script or style element whose content is raw text; "" if none
	h     sink
	sc    scanner // over raw
}

func (p *parser) fail(format string, a ...any) {
	panic(&AssertionError{Msg: fmt.Sprintf(format, a...)})
}

// goahead tokenizes the whole input, passing tags and text to the sink.
func (p *parser) goahead() {
	raw := p.raw
	n := len(raw)
	i := 0
	for i < n {
		var j int
		if p.cdata == "" {
			j = indexRune(raw, '<', i)
			if j < 0 {
				start := max(i, n-34)
				amppos := -1
				for q := n - 1; q >= start; q-- {
					if raw[q] == '&' {
						amppos = q
						break
					}
				}
				if amppos >= 0 && !hasSpaceOrSemi(raw[amppos:]) {
					break // the reference may be incomplete: hold the text back (dropped)
				}
				j = n
			}
		} else {
			j = searchCdataEnd(raw, i, p.cdata)
			if j < 0 {
				break
			}
		}
		if i < j {
			if p.cdata == "" {
				p.h.handleData(unescape(raw[i:j]))
			} else {
				p.h.handleData(raw[i:j])
			}
		}
		i = j
		if i == n {
			break
		}
		// raw[i] is always '<' here: character references are unescaped with the text, never
		// handled on their own.
		var k int
		switch {
		case i+1 < n && isASCIILetter(raw[i+1]):
			k = p.parseStarttag(i)
		case hasPrefixAt(raw, i, "</"):
			k = p.parseEndtag(i)
		case hasPrefixAt(raw, i, "<!--"):
			k = p.parseComment(i)
		case hasPrefixAt(raw, i, "<?"):
			k = p.parsePI(i)
		case hasPrefixAt(raw, i, "<!"):
			k = p.parseHTMLDeclaration(i)
		case i+1 < n:
			p.h.handleData([]rune{'<'})
			k = i + 1
		default:
			k = -1
		}
		if k < 0 {
			break
		}
		i = k
	}
}

func hasSpaceOrSemi(s []rune) bool {
	for _, r := range s {
		if r == ';' || isSpace(r) {
			return true
		}
	}
	return false
}

func (p *parser) parseHTMLDeclaration(i int) int {
	raw := p.raw
	switch {
	case hasPrefixAt(raw, i, "<!--"):
		return p.parseComment(i)
	case hasPrefixAt(raw, i, "<!["):
		return p.parseMarkedSection(i)
	case isDoctype(raw, i):
		gtpos := indexRune(raw, '>', i+9)
		if gtpos == -1 {
			return -1
		}
		return gtpos + 1
	default:
		return p.parseBogusComment(i)
	}
}

// isDoctype reports whether the 9 characters at i lowercase to "<!doctype". No non-ASCII
// character lowercases to any of these letters, so an ASCII case fold is exact.
func isDoctype(raw []rune, i int) bool {
	const want = "<!doctype"
	if i+len(want) > len(raw) {
		return false
	}
	for k, w := range want {
		r := raw[i+k]
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r != w {
			return false
		}
	}
	return true
}

func (p *parser) parseBogusComment(i int) int {
	pos := indexRune(p.raw, '>', i+2)
	if pos == -1 {
		return -1
	}
	return pos + 1
}

func (p *parser) parsePI(i int) int {
	j := indexRune(p.raw, '>', i+2)
	if j < 0 {
		return -1
	}
	return j + 1
}

func (p *parser) parseComment(i int) int {
	_, end := searchClose(p.raw, i+4, "--")
	return end // -1 when not terminated
}

// parseMarkedSection skips a marked section "<![name ...", failing on an unknown name.
func (p *parser) parseMarkedSection(i int) int {
	name, j := p.scanName(i+3, i)
	if j < 0 {
		return j
	}
	var end int
	switch name {
	case "temp", "cdata", "ignore", "include", "rcdata":
		_, end = searchClose(p.raw, i+3, "]", "]")
	case "if", "else", "endif":
		_, end = searchClose(p.raw, i+3, "]")
	default:
		p.fail("unknown status keyword %s in marked section", dyn.StrRepr(string(p.raw[i+3:j])))
	}
	return end
}

// scanName reads a declaration name at i and the whitespace after it, returning the name
// lowercased and the position after the whitespace, or -1 at the end of the input. A
// character other than an ASCII letter at i fails.
func (p *parser) scanName(i, declstartpos int) (string, int) {
	raw := p.raw
	n := len(raw)
	if i == n {
		return "", -1
	}
	if !isASCIILetter(raw[i]) {
		p.fail("expected name token at %s", dyn.StrRepr(string(slice(raw, declstartpos, declstartpos+20))))
	}
	q := i + 1
	for q < n {
		r := raw[q]
		if isASCIILetter(r) || isASCIIDigit(r) || r == '-' || r == '_' || r == '.' {
			q++
			continue
		}
		break
	}
	nameEnd := q
	for q < n && isSpace(raw[q]) {
		q++
	}
	if q == n {
		return "", -1 // end of buffer
	}
	return tagLower(raw[i:nameEnd]), q
}

// parseStarttag handles a start tag at i and returns the position after it, or -1 when it
// is incomplete.
func (p *parser) parseStarttag(i int) int {
	endpos := p.checkForWholeStartTag(i)
	if endpos < 0 {
		return endpos
	}
	raw := p.raw
	nameEnd, k := tagfindTolerant(raw, i+1)
	tag := tagLower(raw[i+1 : nameEnd])
	for k < endpos {
		e, vs, ve, hasValue := p.sc.attrfind(k)
		if e < 0 {
			break
		}
		if hasValue {
			// The attribute values are unused, but they are still unescaped, since unescape
			// can fail with a ValueError (a decimal charref over 4300 digits).
			v := raw[vs:ve]
			if len(v) > 0 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
				v = slice(v, 1, len(v)-1) // drop the quotes; a lone quote gives empty
			}
			if len(v) > 0 {
				unescape(v)
			}
		}
		k = e
	}
	end := string(stripSpace(slice(raw, k, endpos)))
	if end != ">" && end != "/>" {
		p.h.handleData(raw[i:endpos])
		return endpos
	}
	if end == "/>" {
		// a self-closing tag is a start tag followed by its end tag
		p.h.handleStarttag(tag)
		p.h.handleEndtag(tag)
	} else {
		p.h.handleStarttag(tag)
		if tag == "script" || tag == "style" {
			p.cdata = tag
		}
	}
	return endpos
}

// checkForWholeStartTag returns the end of the start tag at i, or -1 when the tag cannot be
// completed (it is then held back).
func (p *parser) checkForWholeStartTag(i int) int {
	raw := p.raw
	j := p.sc.locateStarttagEnd(i) // always matches after "<" + letter
	if j >= len(raw) {
		return -1 // end of input
	}
	next := raw[j]
	switch {
	case next == '>':
		return j + 1
	case next == '/':
		if hasPrefixAt(raw, j, "/>") {
			return j + 2
		}
		return -1 // buffer boundary
	case isASCIILetter(next) || next == '=' || next == '/':
		return -1
	}
	if j > i {
		return j
	}
	return i + 1
}

// parseEndtag handles an end tag at i and returns the position after it, or -1 when it is
// incomplete.
func (p *parser) parseEndtag(i int) int {
	raw := p.raw
	gt := indexRune(raw, '>', i+1)
	if gt < 0 {
		return -1
	}
	gtpos := gt + 1
	name, _, ok := endtagfind(raw, i)
	if !ok {
		if p.cdata != "" {
			p.h.handleData(raw[i:gtpos])
			return gtpos
		}
		nameEnd, end := tagfindTolerant(raw, i+2)
		if nameEnd < 0 {
			if hasPrefixAt(raw, i, "</>") {
				return i + 3
			}
			return p.parseBogusComment(i)
		}
		tagname := tagLower(raw[i+2 : nameEnd])
		gtpos = indexRune(raw, '>', end)
		p.h.handleEndtag(tagname)
		return gtpos + 1
	}
	elem := tagLower(name)
	if p.cdata != "" && elem != p.cdata {
		p.h.handleData(raw[i:gtpos])
		return gtpos
	}
	p.h.handleEndtag(elem)
	p.cdata = ""
	return gtpos
}
