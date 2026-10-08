package htmltext

import (
	"math/rand"
	"testing"
)

// The continuation-passing reference matcher the production scanner (match.go) replaced.
// It mirrors sre backtracking literally and recurses per repetition, so it is kept only as a
// test oracle: TestScannerMatchesReference compares the iterative scanner against it.

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

// capture records the span n matched on the successful path into *g (group 3 of
// attrfind_tolerant); a failed attempt restores the previous value.
func capture(n node, g *[2]int) node {
	return func(s []rune, p int, k cont) bool {
		return n(s, p, func(q int) bool {
			old := *g
			*g = [2]int{p, q}
			if k(q) {
				return true
			}
			*g = old
			return false
		})
	}
}

var refG3 [2]int

var (
	isQuote             = func(r rune) bool { return r == '\'' || r == '"' }
	notSQuote           = func(r rune) bool { return r != '\'' }
	notDQuote           = func(r rune) bool { return r != '"' }
	isEq                = func(r rune) bool { return r == '=' }
	refSpaces           = repeatChar(isSpace, 0)
	refSpaceOrSlashNoGT = star(alt(charIf(isSpace), slashNotGT))

	// ('[^']*'|"[^"]*"|(?!['"])[^>\s]*)
	refAttrValue = alt(
		seq(lit('\''), repeatChar(notSQuote, 0), lit('\'')),
		seq(lit('"'), repeatChar(notDQuote, 0), lit('"')),
		seq(notAhead(isQuote), repeatChar(notGTSpace, 0)),
	)

	// (?<=['"\s/])[^\s/>][^\s/=>]*
	refAttrName = seq(behind(isQuoteSpaceSl), charIf(notSpaceSlGT), repeatChar(notSpaceSlEqGT, 0))

	// attrfind_tolerant:
	// ((?<=['"\s/])[^\s/>][^\s/=>]*)(\s*=+\s*('[^']*'|"[^"]*"|(?![\'"])[^>\s]*))?(?:\s|/(?!>))*
	refAttrfind = seq(
		refAttrName,
		opt(seq(refSpaces, repeatChar(isEq, 1), refSpaces, capture(refAttrValue, &refG3))),
		refSpaceOrSlashNoGT,
	)

	// locatestarttagend_tolerant (re.VERBOSE):
	//   <[a-zA-Z][^\t\n\r\f />\x00]*
	//   (?:[\s/]*
	//     (?:(?<=['"\s/])[^\s/>][^\s/=>]*
	//       (?:\s*=+\s*(?:'[^']*'|"[^"]*"|(?!['"])[^>\s]*)\s*)?(?:\s|/(?!>))*
	//     )*
	//   )?
	//   \s*
	refLocateStarttagEnd = seq(
		lit('<'), charIf(isASCIILetter), repeatChar(tagNameChar, 0),
		opt(seq(
			repeatChar(isSpaceOrSlash, 0),
			star(seq(
				refAttrName,
				opt(seq(refSpaces, repeatChar(isEq, 1), refSpaces, refAttrValue, refSpaces)),
				refSpaceOrSlashNoGT,
			)),
		)),
		refSpaces,
	)
)

// TestScannerMatchesReference checks the iterative scanner against the literal sre-order
// reference on random short strings over the characters that matter to the two patterns.
func TestScannerMatchesReference(t *testing.T) {
	alphabet := []rune("ab'\"=/> \t\n >x=='/")
	rng := rand.New(rand.NewSource(7))
	const n = 300_000
	for iter := 0; iter < n; iter++ {
		l := rng.Intn(14)
		s := []rune("<a")
		if rng.Intn(4) == 0 {
			s = []rune("<")
		}
		for i := 0; i < l; i++ {
			s = append(s, alphabet[rng.Intn(len(alphabet))])
		}
		sc := &scanner{s: s}
		if len(s) >= 2 && isASCIILetter(s[1]) {
			if got, want := sc.locateStarttagEnd(0), matchEnd(refLocateStarttagEnd, s, 0); got != want {
				t.Fatalf("locatestarttagend_tolerant on %q: got %d, want %d", string(s), got, want)
			}
		}
		for k := 0; k <= len(s); k++ {
			refG3 = [2]int{-1, -1}
			wantEnd := matchEnd(refAttrfind, s, k)
			wantHas := wantEnd >= 0 && refG3[0] >= 0
			gotEnd, vs, ve, gotHas := sc.attrfind(k)
			if gotEnd != wantEnd || gotHas != wantHas || gotHas && (vs != refG3[0] || ve != refG3[1]) {
				t.Fatalf("attrfind_tolerant on %q at %d: got end %d value %v [%d:%d], want end %d value %v %v",
					string(s), k, gotEnd, gotHas, vs, ve, wantEnd, wantHas, refG3)
			}
		}
	}
}
