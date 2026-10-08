package htmltext

// Hand-written matchers for the html.parser patterns Go's RE2 cannot express. Everything
// here is iterative: no recursion in proportion to the input.
//
// locatestarttagend_tolerant and attrfind_tolerant use lookbehind/lookahead, and their match
// end depends on sre's backtracking order. Every part after an attribute name is optional, so
// once a name matches the overall match never fails; backtracking only happens locally,
// inside the value part \s*=+\s*VALUE, when VALUE starts with an unclosed quote:
//
//   - sre first gives back one character of the \s* after the '=' run: VALUE then matches
//     empty (bare alternative) at that whitespace;
//   - with no such whitespace it gives back one '=' of a run of two or more: VALUE is then the
//     bare run starting at that last '=';
//   - otherwise the optional value part matches nothing.
//
// The CPS reference matcher in match_ref_test.go mirrors sre literally and the test
// TestScannerMatchesReference checks this scanner against it.

// scanner holds the input and caches the search for the next quote, so that many
// attributes with unclosed quotes cost linear rather than quadratic time.
type scanner struct {
	s      []rune
	qcache [2]struct {
		valid    bool
		from, at int
	}
}

// nextQuote is s.find(q, from), q being the single quote (idx 0) or the double quote (idx 1).
func (sc *scanner) nextQuote(idx int, q rune, from int) int {
	c := &sc.qcache[idx]
	if c.valid && c.from <= from && (c.at < 0 || from <= c.at) {
		return c.at
	}
	at := indexRune(sc.s, q, from)
	c.valid, c.from, c.at = true, from, at
	return at
}

// attrNameEnd matches (?<=['"\s/])[^\s/>][^\s/=>]* at p and returns its end, or -1.
func (sc *scanner) attrNameEnd(p int) int {
	s := sc.s
	if p <= 0 || p >= len(s) || !isQuoteSpaceSl(s[p-1]) || !notSpaceSlGT(s[p]) {
		return -1
	}
	p++
	for p < len(s) && notSpaceSlEqGT(s[p]) {
		p++
	}
	return p
}

// valueAt matches ('[^']*'|"[^"]*"|(?!['"])[^>\s]*) at q and returns its end, or -1.
func (sc *scanner) valueAt(q int) int {
	s := sc.s
	if q < len(s) {
		switch s[q] {
		case '\'':
			if c := sc.nextQuote(0, '\'', q+1); c >= 0 {
				return c + 1
			}
			return -1
		case '"':
			if c := sc.nextQuote(1, '"', q+1); c >= 0 {
				return c + 1
			}
			return -1
		}
	}
	for q < len(s) && notGTSpace(s[q]) {
		q++
	}
	return q
}

// valuePart matches \s*=+\s*VALUE at a with sre's backtracking. It returns the span of
// VALUE (group 3 of attrfind_tolerant), or ok == false when the optional part is absent.
func (sc *scanner) valuePart(a int) (vstart, vend int, ok bool) {
	s := sc.s
	n := len(s)
	p := a
	for p < n && isSpace(s[p]) {
		p++
	}
	if p >= n || s[p] != '=' {
		return 0, 0, false
	}
	eq := p
	for p < n && s[p] == '=' {
		p++
	}
	afterEq := p
	for p < n && isSpace(s[p]) {
		p++
	}
	if e := sc.valueAt(p); e >= 0 {
		return p, e, true
	}
	if p > afterEq {
		return p - 1, p - 1, true // empty bare value at the last whitespace
	}
	if afterEq-eq > 1 {
		return p - 1, sc.valueAt(p - 1), true // bare value from the last '='
	}
	return 0, 0, false
}

// skipSpaceOrSlashNoGT matches (?:\s|/(?!>))* at p and returns its end.
func (sc *scanner) skipSpaceOrSlashNoGT(p int) int {
	s := sc.s
	for p < len(s) && (isSpace(s[p]) || s[p] == '/' && !(p+1 < len(s) && s[p+1] == '>')) {
		p++
	}
	return p
}

func (sc *scanner) skipSpace(p int) int {
	for p < len(sc.s) && isSpace(sc.s[p]) {
		p++
	}
	return p
}

// attrfind is attrfind_tolerant.match(s, k):
//
//	((?<=['"\s/])[^\s/>][^\s/=>]*)(\s*=+\s*('[^']*'|"[^"]*"|(?![\'"])[^>\s]*))?(?:\s|/(?!>))*
//
// It returns the match end (-1 when there is no match) and group 3 (hasValue false when
// group 2 did not participate).
func (sc *scanner) attrfind(k int) (end, vstart, vend int, hasValue bool) {
	p := sc.attrNameEnd(k)
	if p < 0 {
		return -1, 0, 0, false
	}
	if vs, ve, ok := sc.valuePart(p); ok {
		vstart, vend, hasValue = vs, ve, true
		p = ve
	}
	return sc.skipSpaceOrSlashNoGT(p), vstart, vend, hasValue
}

// locateStarttagEnd is locatestarttagend_tolerant.match(s, i).end(), for s[i] == '<' and
// s[i+1] an ASCII letter (re.VERBOSE):
//
//	<[a-zA-Z][^\t\n\r\f />\x00]*
//	(?:[\s/]*
//	  (?:(?<=['"\s/])[^\s/>][^\s/=>]*
//	    (?:\s*=+\s*(?:'[^']*'|"[^"]*"|(?!['"])[^>\s]*)\s*)?(?:\s|/(?!>))*
//	  )*
//	)?
//	\s*
func (sc *scanner) locateStarttagEnd(i int) int {
	s := sc.s
	p := i + 2
	for p < len(s) && tagNameChar(s[p]) {
		p++
	}
	for p < len(s) && isSpaceOrSlash(s[p]) {
		p++
	}
	for {
		e := sc.attrNameEnd(p)
		if e < 0 {
			break
		}
		p = e
		if _, ve, ok := sc.valuePart(p); ok {
			p = sc.skipSpace(ve)
		}
		p = sc.skipSpaceOrSlashNoGT(p)
	}
	return sc.skipSpace(p)
}

var (
	isQuoteSpaceSl = func(r rune) bool { return r == '\'' || r == '"' || r == '/' || isSpace(r) }
	isSpaceOrSlash = func(r rune) bool { return r == '/' || isSpace(r) }
	notSpaceSlGT   = func(r rune) bool { return !(r == '/' || r == '>' || isSpace(r)) }
	notSpaceSlEqGT = func(r rune) bool { return !(r == '/' || r == '=' || r == '>' || isSpace(r)) }
	notGTSpace     = func(r rune) bool { return !(r == '>' || isSpace(r)) }
	tagNameChar    = func(r rune) bool {
		return !(r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == ' ' || r == '/' || r == '>' || r == 0)
	}
)

// tagfindTolerant matches ([a-zA-Z][^\t\n\r\f />\x00]*)(?:\s|/(?!>))* at p. It returns the
// end of group 1 and the end of the match, or (-1, -1). Both parts are greedy with nothing
// after them that could fail, so no backtracking is involved.
func tagfindTolerant(s []rune, p int) (nameEnd, end int) {
	if p >= len(s) || !isASCIILetter(s[p]) {
		return -1, -1
	}
	q := p + 1
	for q < len(s) && tagNameChar(s[q]) {
		q++
	}
	nameEnd = q
	for q < len(s) {
		if isSpace(s[q]) || s[q] == '/' && !(q+1 < len(s) && s[q+1] == '>') {
			q++
			continue
		}
		break
	}
	return nameEnd, q
}

// endtagfind matches </\s*([a-zA-Z][-.a-zA-Z0-9:_]*)\s*> at p and returns group 1 and the
// match end, or ok == false.
func endtagfind(s []rune, p int) (name []rune, end int, ok bool) {
	if !hasPrefixAt(s, p, "</") {
		return nil, 0, false
	}
	q := p + 2
	for q < len(s) && isSpace(s[q]) {
		q++
	}
	if q >= len(s) || !isASCIILetter(s[q]) {
		return nil, 0, false
	}
	start := q
	q++
	for q < len(s) {
		r := s[q]
		if isASCIILetter(r) || isASCIIDigit(r) || r == '-' || r == '.' || r == ':' || r == '_' {
			q++
			continue
		}
		break
	}
	name = s[start:q]
	for q < len(s) && isSpace(s[q]) {
		q++
	}
	if q >= len(s) || s[q] != '>' {
		return nil, 0, false
	}
	return name, q + 1, true
}

// searchClose is a leftmost search from `from` for the literal runes of parts joined by
// \s* and ending in '>': parts "--" gives --\s*>, parts "]","]" gives ]\s*]\s*>, parts "]"
// gives ]\s*>. It returns the match start and end, or (-1, -1).
func searchClose(s []rune, from int, parts ...string) (start, end int) {
	for p := from; p < len(s); p++ {
		q := p
		ok := true
		for idx, part := range parts {
			if idx > 0 {
				for q < len(s) && isSpace(s[q]) {
					q++
				}
			}
			if !hasPrefixAt(s, q, part) {
				ok = false
				break
			}
			q += len([]rune(part))
		}
		if !ok {
			continue
		}
		for q < len(s) && isSpace(s[q]) {
			q++
		}
		if q < len(s) && s[q] == '>' {
			return p, q + 1
		}
	}
	return -1, -1
}

// foldClass lists, for each letter of "script" and "style", every character that the
// letter matches under Python re.IGNORECASE in a str pattern (CPython 3.13).
var foldClass = map[rune][]rune{
	's': {'S', 's', 0x17F},
	'c': {'C', 'c'},
	'r': {'R', 'r'},
	'i': {'I', 'i', 0x130, 0x131},
	'p': {'P', 'p'},
	't': {'T', 't'},
	'y': {'Y', 'y'},
	'l': {'L', 'l'},
	'e': {'E', 'e'},
}

func foldEq(lower rune, r rune) bool {
	for _, c := range foldClass[lower] {
		if c == r {
			return true
		}
	}
	return false
}

// searchCdataEnd is re.compile(r'</\s*%s\s*>' % elem, re.I).search(s, from): the start of
// the leftmost match, or -1.
func searchCdataEnd(s []rune, from int, elem string) int {
	name := []rune(elem)
	for p := from; p+1 < len(s); p++ {
		if s[p] != '<' || s[p+1] != '/' {
			continue
		}
		q := p + 2
		for q < len(s) && isSpace(s[q]) {
			q++
		}
		ok := true
		for _, c := range name {
			if q >= len(s) || !foldEq(c, s[q]) {
				ok = false
				break
			}
			q++
		}
		if !ok {
			continue
		}
		for q < len(s) && isSpace(s[q]) {
			q++
		}
		if q < len(s) && s[q] == '>' {
			return p
		}
	}
	return -1
}
