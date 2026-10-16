// Package dyn holds the recorder's dynamic values and the text and file handling around them: JSON
// values with insertion-ordered dicts and integers of any size, string helpers with code-point
// semantics (Unicode whitespace, lengths and slices in code points, full case mapping, quoted
// literals, integer parsing), the recorder's exact JSON decoding and encoding, path normalisation and
// globbing, reading and writing UTF-8 text files, and the exceptions that stop a command.
package dyn

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// IsSpace reports whether r is whitespace: bidirectional class WS, B or S, or category Zs.
func IsSpace(r rune) bool {
	switch {
	case r >= 9 && r <= 13, r >= 0x1c && r <= 0x20, r == 0x85, r == 0xa0, r == 0x1680,
		r >= 0x2000 && r <= 0x200a, r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// SpaceClass is a regexp character-class body matching the whitespace IsSpace reports.
const SpaceClass = `\t\n\x0b\x0c\r\x1c-\x20\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// Strip removes leading and trailing whitespace (IsSpace).
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// LStrip removes leading whitespace (IsSpace).
func LStrip(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// RStrip removes trailing whitespace (IsSpace).
func RStrip(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// StripChars removes leading and trailing characters that are in chars.
func StripChars(s, chars string) string { return strings.Trim(s, chars) }

// Fields splits s at runs of whitespace (IsSpace), with no empty fields.
func Fields(s string) []string { return strings.FieldsFunc(s, IsSpace) }

// Len is the number of code points in s.
func Len(s string) int {
	n := 0
	for i := 0; i < len(s); n++ {
		_, size := decodeWTF8(s[i:])
		i += size
	}
	return n
}

// Head is the first n code points of s (n >= 0).
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return s[:byteOffset(s, n)]
}

// byteOffset is the byte index of code point n (a lone surrogate, as Loads keeps it, counts as one).
func byteOffset(s string, n int) int {
	i := 0
	for k := 0; k < n && i < len(s); k++ {
		_, size := decodeWTF8(s[i:])
		i += size
	}
	return i
}

// Tail is s after its first n code points (n >= 0).
func Tail(s string, n int) string { return s[byteOffset(s, n):] }

var (
	lower = cases.Lower(language.Und)
	upper = cases.Upper(language.Und)
)

// Lower is s lower-cased: full Unicode case mapping, language-independent.
func Lower(s string) string { return lower.String(s) }

// Upper is s upper-cased: full Unicode case mapping, language-independent.
func Upper(s string) string { return upper.String(s) }

// Splitlines splits s into lines at \n, \r, \r\n, \v, \f, \x1c-\x1e, \x85, U+2028 and U+2029. The
// line breaks are dropped, and a break at the end does not start another line.
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

// Partition splits s at the first sep into what comes before, sep and what comes after; without sep
// it is s, "", "".
func Partition(s, sep string) (string, string, string) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], sep, s[i+len(sep):]
	}
	return s, "", ""
}

// IsWord reports whether r is a word character: a letter or number (categories L and N, Unicode 15.1
// included) or an underscore.
func IsWord(r rune) bool {
	if _, letter := unicode151(r); letter {
		return true
	}
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// IsDecimal reports whether r is a decimal digit (category Nd).
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

// unicode151 is true for the code points Unicode 15.1 assigned beyond Go's Unicode 15.0 tables: CJK
// Extension I (letters) and a few ideographic description characters.
func unicode151(r rune) (assigned, letter bool) {
	switch {
	case r >= 0x2EBF0 && r <= 0x2EE5D:
		return true, true
	case r >= 0x2FFC && r <= 0x2FFF, r == 0x31EF:
		return true, false
	}
	return false, false
}

// IsPrintable reports whether r is shown as itself in a quoted literal (StrRepr): the space, and any
// assigned code point outside the control, format, surrogate, private-use and separator categories.
func IsPrintable(r rune) bool {
	if r == ' ' {
		return true
	}
	if ok, _ := unicode151(r); ok {
		return true
	}
	if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Zl, unicode.Zp, unicode.Zs) {
		return false
	}
	// unassigned (Cn): in no general category at all
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S)
}

// Ljust pads s on the right with spaces to n code points.
func Ljust(s string, n int) string {
	if l := Len(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}
