// Package py holds the few pieces of Python semantics the recorder depends on, so that the Go port
// produces what the Python version produced: str methods (Unicode whitespace, code-point lengths and
// slices, full case mapping), repr(), json.loads/json.dumps, int(), and pathlib-style paths and globs.
package py

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// IsSpace is str.isspace() for one code point: bidirectional class WS, B or S, or category Zs.
func IsSpace(r rune) bool {
	switch {
	case r >= 9 && r <= 13, r >= 0x1c && r <= 0x20, r == 0x85, r == 0xa0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// SpaceClass is a regexp character-class body matching what Python's \s matches in str patterns.
const SpaceClass = `\t\n\x0b\x0c\r\x1c-\x20\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// Strip is str.strip() with no argument.
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// LStrip is str.lstrip() with no argument.
func LStrip(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// RStrip is str.rstrip() with no argument.
func RStrip(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// StripChars is str.strip(chars).
func StripChars(s, chars string) string { return strings.Trim(s, chars) }

// Fields is str.split() with no argument.
func Fields(s string) []string { return strings.FieldsFunc(s, IsSpace) }

// Len is len(s) for a str: the number of code points.
func Len(s string) int { return utf8.RuneCountInString(s) }

// Head is s[:n] in code points (n >= 0).
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// Tail is s[n:] in code points (n >= 0).
func Tail(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[j:]
		}
		i++
	}
	return ""
}

var (
	lower = cases.Lower(language.Und)
	upper = cases.Upper(language.Und)
)

// Lower is str.lower(): full Unicode case mapping, language-independent.
func Lower(s string) string { return lower.String(s) }

// Upper is str.upper().
func Upper(s string) string { return upper.String(s) }

// Splitlines is str.splitlines(): it breaks at \n, \r, \r\n, \v, \f, \x1c-\x1e, \x85, U+2028, U+2029.
func Splitlines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			i += size
			start = i
			continue
		case '\r':
			out = append(out, s[start:i])
			i += size
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// Partition is str.partition(sep).
func Partition(s, sep string) (string, string, string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], sep, s[i+len(sep):]
	}
	return s, "", ""
}

// IsWord is what \w matches in a Python str pattern: alphanumeric (str.isalnum) or underscore.
func IsWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// IsDecimal is what \d matches in a Python str pattern (category Nd).
func IsDecimal(r rune) bool { return unicode.Is(unicode.Nd, r) }

// DecimalValue is the digit value of a code point for which IsDecimal is true.
func DecimalValue(r rune) int {
	// Decimal digits come in runs of ten starting at zero, and the table's ranges begin at a zero.
	for _, rg := range unicode.Nd.R16 {
		if uint16(r) >= rg.Lo && r <= rune(rg.Hi) && r <= 0xffff {
			return int(uint16(r)-rg.Lo) % 10
		}
	}
	for _, rg := range unicode.Nd.R32 {
		if uint32(r) >= rg.Lo && uint32(r) <= rg.Hi {
			return int(uint32(r)-rg.Lo) % 10
		}
	}
	return 0
}

// IsPrintable is str.isprintable() for one code point.
func IsPrintable(r rune) bool {
	if r == ' ' {
		return true
	}
	if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Zl, unicode.Zp, unicode.Zs) {
		return false
	}
	// unassigned (Cn): in no general category at all
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S)
}

// Ljust is format(s, "N"): pad a str on the right with spaces to N code points.
func Ljust(s string, n int) string {
	if l := Len(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}
