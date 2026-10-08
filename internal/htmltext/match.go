package htmltext

// A tiny backtracking matcher with Python `re` (sre) semantics, used for the two
// html.parser patterns that need lookbehind/lookahead and whose match end depends on
// backtracking order: locatestarttagend_tolerant and attrfind_tolerant. Nodes are written
// in continuation-passing style, so alternatives and greedy repeats are tried in the same
// order sre tries them, and the first complete match wins, as in sre.

type cont func(int) bool

type node func(s []rune, p int, k cont) bool

func charIf(f func(rune) bool) node {
	return func(s []rune, p int, k cont) bool {
		return p < len(s) && f(s[p]) && k(p+1)
	}
}

func lit(r rune) node { return charIf(func(c rune) bool { return c == r }) }

// repeatChar is a greedy single-character repeat (X* when min == 0, X+ when min == 1).
func repeatChar(f func(rune) bool, min int) node {
	return func(s []rune, p int, k cont) bool {
		q := p
		for q < len(s) && f(s[q]) {
			q++
		}
		for ; q >= p+min; q-- {
			if k(q) {
				return true
			}
		}
		return false
	}
}

func seq(ns ...node) node {
	if len(ns) == 0 {
		return func(s []rune, p int, k cont) bool { return k(p) }
	}
	if len(ns) == 1 {
		return ns[0]
	}
	head, tail := ns[0], seq(ns[1:]...)
	return func(s []rune, p int, k cont) bool {
		return head(s, p, func(q int) bool { return tail(s, q, k) })
	}
}

func alt(ns ...node) node {
	return func(s []rune, p int, k cont) bool {
		for _, n := range ns {
			if n(s, p, k) {
				return true
			}
		}
		return false
	}
}

// opt is a greedy (?:X)?.
func opt(n node) node {
	return func(s []rune, p int, k cont) bool { return n(s, p, k) || k(p) }
}

// star is a greedy (?:X)* over a group. Like sre's MAX_UNTIL, an iteration that matched
// the empty string is not followed by another iteration.
func star(n node) node {
	var self node
	self = func(s []rune, p int, k cont) bool {
		if n(s, p, func(q int) bool {
			if q == p {
				return k(q)
			}
			return self(s, q, k)
		}) {
			return true
		}
		return k(p)
	}
	return self
}

// behind is the lookbehind (?<=[...]) for a one-character class.
func behind(f func(rune) bool) node {
	return func(s []rune, p int, k cont) bool { return p > 0 && f(s[p-1]) && k(p) }
}

// notAhead is the negative lookahead (?![...]) for a one-character class.
func notAhead(f func(rune) bool) node {
	return func(s []rune, p int, k cont) bool { return !(p < len(s) && f(s[p])) && k(p) }
}

// slashNotGT is /(?!>).
func slashNotGT(s []rune, p int, k cont) bool {
	return p < len(s) && s[p] == '/' && !(p+1 < len(s) && s[p+1] == '>') && k(p+1)
}

// matchEnd runs n anchored at p and returns the end of the first match, or -1.
func matchEnd(n node, s []rune, p int) int {
	end := -1
	n(s, p, func(q int) bool { end = q; return true })
	return end
}

var (
	isQuote        = func(r rune) bool { return r == '\'' || r == '"' }
	isQuoteSpaceSl = func(r rune) bool { return r == '\'' || r == '"' || r == '/' || isSpace(r) }
	isSpaceOrSlash = func(r rune) bool { return r == '/' || isSpace(r) }
	notSpaceSlGT   = func(r rune) bool { return !(r == '/' || r == '>' || isSpace(r)) }
	notSpaceSlEqGT = func(r rune) bool { return !(r == '/' || r == '=' || r == '>' || isSpace(r)) }
	notGTSpace     = func(r rune) bool { return !(r == '>' || isSpace(r)) }
	notSQuote      = func(r rune) bool { return r != '\'' }
	notDQuote      = func(r rune) bool { return r != '"' }
	isEq           = func(r rune) bool { return r == '=' }
	tagNameChar    = func(r rune) bool {
		return !(r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == ' ' || r == '/' || r == '>' || r == 0)
	}
	spaces           = repeatChar(isSpace, 0)
	spaceOrSlashNoGT = star(alt(charIf(isSpace), slashNotGT))

	// ('[^']*'|"[^"]*"|(?!['"])[^>\s]*)
	attrValue = alt(
		seq(lit('\''), repeatChar(notSQuote, 0), lit('\'')),
		seq(lit('"'), repeatChar(notDQuote, 0), lit('"')),
		seq(notAhead(isQuote), repeatChar(notGTSpace, 0)),
	)

	// (?<=['"\s/])[^\s/>][^\s/=>]*
	attrName = seq(behind(isQuoteSpaceSl), charIf(notSpaceSlGT), repeatChar(notSpaceSlEqGT, 0))

	// attrfind_tolerant:
	// ((?<=['"\s/])[^\s/>][^\s/=>]*)(\s*=+\s*('[^']*'|"[^"]*"|(?![\'"])[^>\s]*))?(?:\s|/(?!>))*
	attrfindTolerant = seq(
		attrName,
		opt(seq(spaces, repeatChar(isEq, 1), spaces, attrValue)),
		spaceOrSlashNoGT,
	)

	// locatestarttagend_tolerant (re.VERBOSE):
	//   <[a-zA-Z][^\t\n\r\f />\x00]*
	//   (?:[\s/]*
	//     (?:(?<=['"\s/])[^\s/>][^\s/=>]*
	//       (?:\s*=+\s*(?:'[^']*'|"[^"]*"|(?!['"])[^>\s]*)\s*)?(?:\s|/(?!>))*
	//     )*
	//   )?
	//   \s*
	locateStarttagEndTolerant = seq(
		lit('<'), charIf(isASCIILetter), repeatChar(tagNameChar, 0),
		opt(seq(
			repeatChar(isSpaceOrSlash, 0),
			star(seq(
				attrName,
				opt(seq(spaces, repeatChar(isEq, 1), spaces, attrValue, spaces)),
				spaceOrSlashNoGT,
			)),
		)),
		spaces,
	)
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

// searchCloseSeq is a leftmost search from `from` for the literal runes of parts joined by
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
