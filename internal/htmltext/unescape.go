package htmltext

import "fmt"

// maxStrDigits is the 4300-digit limit on decimal character references: converting one
// with more digits fails with a ValueError (hexadecimal references are not limited).
const maxStrDigits = 4300

// ValueError is the error extraction stops with when unescape meets a decimal character
// reference of more than 4300 digits (in text or in an attribute value).
type ValueError struct{ Msg string }

func (e *ValueError) Error() string { return "ValueError: " + e.Msg }

// unescape replaces character references: every match of
//
//	&(#[0-9]+;?|#[xX][0-9a-fA-F]+;?|[^\t\n\f <&#;]{1,32};?)
//
// is replaced by its numeric or named expansion, scanning left to right without overlap.
func unescape(s []rune) []rune {
	if indexRune(s, '&', 0) < 0 {
		return s
	}
	out := make([]rune, 0, len(s))
	n := len(s)
	i := 0
	for i < n {
		if s[i] != '&' {
			out = append(out, s[i])
			i++
			continue
		}
		p := i + 1
		switch {
		case p < n && s[p] == '#' && p+1 < n && isASCIIDigit(s[p+1]):
			q := p + 1
			for q < n && isASCIIDigit(s[q]) {
				q++
			}
			if nd := q - (p + 1); nd > maxStrDigits {
				panic(&ValueError{Msg: fmt.Sprintf("Exceeds the limit (%d digits) for integer string conversion: "+
					"value has %d digits", maxStrDigits, nd)})
			}
			num := parseNum(s[p+1:q], 10)
			if q < n && s[q] == ';' {
				q++
			}
			out = append(out, numericCharref(num)...)
			i = q
		case p < n && s[p] == '#' && p+2 < n && (s[p+1] == 'x' || s[p+1] == 'X') && isHexDigit(s[p+2]):
			q := p + 2
			for q < n && isHexDigit(s[q]) {
				q++
			}
			num := parseNum(s[p+2:q], 16)
			if q < n && s[q] == ';' {
				q++
			}
			out = append(out, numericCharref(num)...)
			i = q
		default:
			q := p
			for q < n && q-p < 32 && !isRefStop(s[q]) {
				q++
			}
			if q == p {
				// no match at this '&'; the scan moves on by one character
				out = append(out, '&')
				i++
				continue
			}
			if q < n && s[q] == ';' {
				q++
			}
			out = append(out, namedCharref(s[p:q])...)
			i = q
		}
	}
	return out
}

func isRefStop(r rune) bool {
	return r == '\t' || r == '\n' || r == '\f' || r == ' ' || r == '<' || r == '&' || r == '#' || r == ';'
}

// parseNum is the value of digits in base, saturated just above 0x10FFFF: every larger
// value expands the same way in numericCharref.
func parseNum(digits []rune, base int) int {
	const limit = 0x110000
	v := 0
	for _, r := range digits {
		var d int
		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= 'a' && r <= 'f':
			d = int(r-'a') + 10
		default:
			d = int(r-'A') + 10
		}
		v = v*base + d
		if v > limit {
			v = limit
		}
	}
	return v
}

var invalidCharrefs = map[int]rune{
	0x00: 0xFFFD, 0x0D: '\r', 0x80: 0x20AC, 0x81: 0x81, 0x82: 0x201A, 0x83: 0x0192,
	0x84: 0x201E, 0x85: 0x2026, 0x86: 0x2020, 0x87: 0x2021, 0x88: 0x02C6, 0x89: 0x2030,
	0x8A: 0x0160, 0x8B: 0x2039, 0x8C: 0x0152, 0x8D: 0x8D, 0x8E: 0x017D, 0x8F: 0x8F,
	0x90: 0x90, 0x91: 0x2018, 0x92: 0x2019, 0x93: 0x201C, 0x94: 0x201D, 0x95: 0x2022,
	0x96: 0x2013, 0x97: 0x2014, 0x98: 0x02DC, 0x99: 0x2122, 0x9A: 0x0161, 0x9B: 0x203A,
	0x9C: 0x0153, 0x9D: 0x9D, 0x9E: 0x017E, 0x9F: 0x0178,
}

func isInvalidCodepoint(n int) bool {
	switch {
	case n >= 0x1 && n <= 0x8, n >= 0xE && n <= 0x1F, n >= 0x7F && n <= 0x9F,
		n >= 0xFDD0 && n <= 0xFDEF, n == 0xB:
		return true
	case n&0xFFFE == 0xFFFE && n <= 0x10FFFF:
		// 0xFFFE, 0xFFFF, 0x1FFFE, 0x1FFFF, ... 0x10FFFE, 0x10FFFF
		return true
	}
	return false
}

func numericCharref(num int) []rune {
	if r, ok := invalidCharrefs[num]; ok {
		return []rune{r}
	}
	if num >= 0xD800 && num <= 0xDFFF || num > 0x10FFFF {
		return []rune{0xFFFD}
	}
	if isInvalidCodepoint(num) {
		return nil
	}
	return []rune{rune(num)}
}

// namedCharref expands a named reference; s is the matched text after '&'. An exact name
// wins; otherwise the longest name that is a prefix of s (at least two characters) is
// expanded and the rest kept; otherwise s is kept with its '&'.
func namedCharref(s []rune) []rune {
	if v, ok := html5[string(s)]; ok {
		return []rune(v)
	}
	for x := len(s) - 1; x > 1; x-- {
		if v, ok := html5[string(s[:x])]; ok {
			return append([]rune(v), s[x:]...)
		}
	}
	return append([]rune{'&'}, s...)
}
