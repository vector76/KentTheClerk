package htmltext

import (
	"fmt"

	"kenttheclerk/internal/py"
)

// A port of CPython 3.13.1 html.parser.HTMLParser (with the parts of _markupbase.ParserBase
// it reaches) for convert_charrefs=True, fed once and never closed: everything goahead(0)
// holds back at the end of the input is dropped, as it is in kb/ingest.py extract_html.
// Position bookkeeping (updatepos, lineno, offset) has no visible effect and is omitted, as
// are handlers the _HTMLText subclass leaves as no-ops (comments, declarations, PIs).

// AssertionError is what Python raises out of feed() for markup _markupbase cannot scan,
// e.g. "<![" not followed by a name, or an unknown marked-section keyword like "<![foo[".
type AssertionError struct{ Msg string }

func (e *AssertionError) Error() string { return "AssertionError: " + e.Msg }

type sink interface {
	handleStarttag(tag string)
	handleEndtag(tag string)
	handleData(data []rune)
}

type parser struct {
	raw   []rune
	cdata string // cdata_elem; "" is None
	h     sink
	sc    scanner // over raw
}

func (p *parser) fail(format string, a ...any) {
	panic(&AssertionError{Msg: fmt.Sprintf(format, a...)})
}

// goahead is HTMLParser.goahead(0).
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
					break // wait till we get all the text (which never comes)
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
		// raw[i] is always '<' here: with convert_charrefs=True the '&' branches of
		// goahead are unreachable.
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

// isDoctype is rawdata[i:i+9].lower() == '<!doctype'. No non-ASCII character lowercases to
// any of these letters, so an ASCII case fold is exact.
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

// parseMarkedSection is _markupbase.ParserBase.parse_marked_section.
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
		p.fail("unknown status keyword %s in marked section", py.StrRepr(string(p.raw[i+3:j])))
	}
	return end
}

// scanName is _markupbase.ParserBase._scan_name.
func (p *parser) scanName(i, declstartpos int) (string, int) {
	raw := p.raw
	n := len(raw)
	if i == n {
		return "", -1
	}
	if !isASCIILetter(raw[i]) {
		p.fail("expected name token at %s", py.StrRepr(string(slice(raw, declstartpos, declstartpos+20))))
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

// parseStarttag is HTMLParser.parse_starttag.
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
			// The attribute values are unused, but Python unescapes them, and unescape can
			// raise ValueError (a decimal charref over 4300 digits).
			v := raw[vs:ve]
			if len(v) > 0 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
				v = slice(v, 1, len(v)-1) // Python's attrvalue[1:-1]
			}
			if len(v) > 0 {
				unescape(v)
			}
		}
		k = e
	}
	end := string(pyStrip(slice(raw, k, endpos)))
	if end != ">" && end != "/>" {
		p.h.handleData(raw[i:endpos])
		return endpos
	}
	if end == "/>" {
		// handle_startendtag, which _HTMLText does not override
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

// checkForWholeStartTag is HTMLParser.check_for_whole_start_tag.
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

// parseEndtag is HTMLParser.parse_endtag.
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
