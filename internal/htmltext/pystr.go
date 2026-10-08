package htmltext

// Python str semantics the extractor relies on. Strings are handled as []rune so that
// indexes and lengths are code points, as in Python.

// isSpace is str.isspace() for one character, which is also what `\s` matches in a
// Python str pattern (CPython 3.13, Unicode 15.1).
func isSpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0D, r >= 0x1C && r <= 0x20:
		return true
	case r < 0x85:
		return false
	case r == 0x85, r == 0xA0, r == 0x1680, r >= 0x2000 && r <= 0x200A,
		r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000:
		return true
	}
	return false
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func isHexDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// pyStrip is str.strip() with no arguments.
func pyStrip(s []rune) []rune {
	i, j := 0, len(s)
	for i < j && isSpace(s[i]) {
		i++
	}
	for j > i && isSpace(s[j-1]) {
		j--
	}
	return s[i:j]
}

// tagLower is str.lower() as far as it matters here: the result is only ever compared
// with ASCII tag names. The only non-ASCII characters whose Python lowercase contains
// ASCII are U+212A KELVIN SIGN ("k") and U+0130 ("i" + U+0307). Every other non-ASCII
// character is kept as is, which can never compare equal to an ASCII name, exactly as
// its true lowercase could not.
func tagLower(s []rune) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r == 0x212A:
			out = append(out, 'k')
		case r == 0x130:
			out = append(out, 'i', 0x307)
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func indexRune(s []rune, r rune, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(s); i++ {
		if s[i] == r {
			return i
		}
	}
	return -1
}

func hasPrefixAt(s []rune, i int, prefix string) bool {
	for _, r := range prefix {
		if i >= len(s) || s[i] != r {
			return false
		}
		i++
	}
	return true
}

// slice is Python's s[i:j] for 0 <= i, j: out-of-range bounds clamp, i > j gives empty.
func slice(s []rune, i, j int) []rune {
	if j > len(s) {
		j = len(s)
	}
	if i > j {
		return nil
	}
	return s[i:j]
}
