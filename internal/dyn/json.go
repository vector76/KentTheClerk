package dyn

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// JSONError is a JSON decoding error; Error() gives the message with its line, column and position.
type JSONError struct {
	Msg       string
	Pos       int // in code points
	Line, Col int
}

func (e *JSONError) Error() string {
	return fmt.Sprintf("%s: line %d column %d (char %d)", e.Msg, e.Line, e.Col, e.Pos)
}

// Loads decodes the JSON document s: strict, except that NaN, Infinity and -Infinity are accepted.
// Objects become *Dict (a repeated key keeps the last value), integers Int and other numbers float64.
// Lone surrogates from \u escapes are kept, encoded as WTF-8.
func Loads(s string) (v any, err error) {
	d := &decoder{s: s}
	defer func() {
		if r := recover(); r != nil {
			if je, ok := r.(*JSONError); ok {
				v, err = nil, je
				return
			}
			panic(r)
		}
	}()
	if strings.HasPrefix(s, "\uFEFF") {
		d.fail("Unexpected UTF-8 BOM (decode using utf-8-sig)", 0)
	}
	i := d.ws(0)
	v, i = d.value(i)
	i = d.ws(i)
	if i != len(s) {
		d.fail("Extra data", i)
	}
	return v, nil
}

type decoder struct{ s string }

func (d *decoder) fail(msg string, bytePos int) {
	doc := d.s[:bytePos]
	pos := utf8.RuneCountInString(doc)
	line := strings.Count(doc, "\n") + 1
	col := pos + 1
	if k := strings.LastIndexByte(doc, '\n'); k >= 0 {
		col = pos - utf8.RuneCountInString(doc[:k])
	}
	panic(&JSONError{msg, pos, line, col})
}

func (d *decoder) ws(i int) int {
	for i < len(d.s) && (d.s[i] == ' ' || d.s[i] == '\t' || d.s[i] == '\n' || d.s[i] == '\r') {
		i++
	}
	return i
}

func (d *decoder) value(i int) (any, int) {
	s := d.s
	if i >= len(s) {
		d.fail("Expecting value", i)
	}
	switch c := s[i]; {
	case c == '"':
		return d.str(i + 1)
	case c == '{':
		return d.object(i + 1)
	case c == '[':
		return d.array(i + 1)
	case c == 'n' && strings.HasPrefix(s[i:], "null"):
		return nil, i + 4
	case c == 't' && strings.HasPrefix(s[i:], "true"):
		return true, i + 4
	case c == 'f' && strings.HasPrefix(s[i:], "false"):
		return false, i + 5
	case c == 'N' && strings.HasPrefix(s[i:], "NaN"):
		return math.NaN(), i + 3
	case c == 'I' && strings.HasPrefix(s[i:], "Infinity"):
		return math.Inf(1), i + 8
	case c == '-' && strings.HasPrefix(s[i:], "-Infinity"):
		return math.Inf(-1), i + 9
	}
	// (-?(?:0|[1-9][0-9]*))(\.[0-9]+)?([eE][-+]?[0-9]+)?
	j := i
	if j < len(s) && s[j] == '-' {
		j++
	}
	digit := func(k int) bool { return k < len(s) && s[k] >= '0' && s[k] <= '9' }
	if !digit(j) {
		d.fail("Expecting value", i)
	}
	if s[j] == '0' {
		j++
	} else {
		for digit(j) {
			j++
		}
	}
	intEnd := j
	isFloat := false
	if j < len(s) && s[j] == '.' && digit(j+1) {
		j += 2
		for digit(j) {
			j++
		}
		isFloat = true
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if digit(k) {
			for digit(k) {
				k++
			}
			j = k
			isFloat = true
		}
	}
	if !isFloat {
		n, _ := new(big.Int).SetString(s[i:intEnd], 10)
		return Int(n.String()), intEnd
	}
	f, _ := strconv.ParseFloat(s[i:j], 64) // out of range gives ±Inf
	return f, j
}

func (d *decoder) object(i int) (any, int) {
	s := d.s
	obj := NewDict()
	start := i - 1
	i = d.ws(i)
	if i < len(s) && s[i] == '}' {
		return obj, i + 1
	}
	if i >= len(s) || s[i] != '"' {
		d.fail("Expecting property name enclosed in double quotes", i)
	}
	_ = start
	for {
		kv, j := d.str(i + 1)
		key := kv.(string)
		j = d.ws(j)
		if j >= len(s) || s[j] != ':' {
			d.fail("Expecting ':' delimiter", j)
		}
		j = d.ws(j + 1)
		var val any
		val, j = d.value(j)
		obj.Set(key, val)
		j = d.ws(j)
		if j < len(s) && s[j] == '}' {
			return obj, j + 1
		}
		if j >= len(s) || s[j] != ',' {
			d.fail("Expecting ',' delimiter", j)
		}
		comma := j
		j = d.ws(j + 1)
		if j < len(s) && s[j] == '}' {
			d.fail("Illegal trailing comma before end of object", comma)
		}
		if j >= len(s) || s[j] != '"' {
			d.fail("Expecting property name enclosed in double quotes", j)
		}
		i = j
	}
}

func (d *decoder) array(i int) (any, int) {
	s := d.s
	arr := []any{}
	i = d.ws(i)
	if i < len(s) && s[i] == ']' {
		return arr, i + 1
	}
	for {
		var val any
		val, i = d.value(i)
		arr = append(arr, val)
		i = d.ws(i)
		if i < len(s) && s[i] == ']' {
			return arr, i + 1
		}
		if i >= len(s) || s[i] != ',' {
			d.fail("Expecting ',' delimiter", i)
		}
		comma := i
		i = d.ws(i + 1)
		if i < len(s) && s[i] == ']' {
			d.fail("Illegal trailing comma before end of array", comma)
		}
	}
}

func hex4(s string) (rune, bool) {
	if len(s) < 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(s[:4], 16, 32)
	if err != nil || strings.ContainsAny(s[:4], "+-xX_") {
		return 0, false
	}
	return rune(n), true
}

// str scans a string body starting just after the opening quote.
func (d *decoder) str(i int) (any, int) {
	s := d.s
	begin := i - 1
	var b strings.Builder
	for {
		j := i
		for j < len(s) && s[j] != '"' && s[j] != '\\' && s[j] >= 0x20 {
			j++
		}
		b.WriteString(s[i:j])
		if j >= len(s) {
			d.fail("Unterminated string starting at", begin)
		}
		c := s[j]
		if c == '"' {
			return b.String(), j + 1
		}
		if c != '\\' {
			d.fail("Invalid control character at", j)
		}
		j++
		if j >= len(s) {
			d.fail("Unterminated string starting at", begin)
		}
		esc := s[j]
		if esc != 'u' {
			m := map[byte]string{'"': "\"", '\\': "\\", '/': "/", 'b': "\b", 'f': "\f", 'n': "\n", 'r': "\r", 't': "\t"}
			r, ok := m[esc]
			if !ok {
				d.fail("Invalid \\escape", j-1)
			}
			b.WriteString(r)
			i = j + 1
			continue
		}
		u, ok := hex4(s[j+1:])
		if !ok {
			d.fail("Invalid \\uXXXX escape", j)
		}
		j += 5
		if u >= 0xd800 && u <= 0xdbff && strings.HasPrefix(s[j:], "\\u") {
			if u2, ok2 := hex4(s[j+2:]); ok2 && u2 >= 0xdc00 && u2 <= 0xdfff {
				u = 0x10000 + (u-0xd800)<<10 + (u2 - 0xdc00)
				j += 6
			}
		}
		b.WriteString(encodeWTF8(u))
		i = j
	}
}

// CheckEncodable raises UnicodeEncodeError if s cannot be written as UTF-8: at a lone surrogate (kept
// by Loads from an escape in the JSON).
func CheckEncodable(s string) {
	if i := SurrogateIndex(s); i >= 0 {
		r, _ := decodeWTF8(s[i:])
		Raise("UnicodeEncodeError", "'utf-8' codec can't encode character '%s' in position %d: surrogates not allowed",
			fmt.Sprintf("%cu%04x", '\\', r), Len(s[:i]))
	}
}

// SurrogateIndex is the byte index of the first lone surrogate in s, or -1.
func SurrogateIndex(s string) int {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == 0xed && s[i+1] >= 0xa0 && s[i+1] <= 0xbf {
			return i
		}
	}
	return -1
}

func encodeWTF8(r rune) string {
	if r >= 0xd800 && r <= 0xdfff {
		return string([]byte{0xed, byte(0xa0 | (r>>6)&0x1f), byte(0x80 | r&0x3f)})
	}
	return string(r)
}

// decodeWTF8 decodes one code point, accepting the 3-byte encodings of surrogates that Loads produces.
func decodeWTF8(s string) (rune, int) {
	if len(s) >= 3 && s[0] == 0xed && s[1] >= 0xa0 && s[1] <= 0xbf && s[2] >= 0x80 && s[2] <= 0xbf {
		return 0xd000 | rune(s[1]&0x3f)<<6 | rune(s[2]&0x3f), 3
	}
	return utf8.DecodeRuneInString(s)
}

// Dumps writes v as JSON with ", " and ": " separators and non-ASCII characters unescaped. Floats are
// written as FloatRepr writes them, and NaN and infinities as NaN, Infinity and -Infinity.
func Dumps(v any) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

// JSONStr is s as a JSON string, non-ASCII characters unescaped.
func JSONStr(s string) string {
	var b strings.Builder
	writeJSONStr(&b, s)
	return b.String()
}

func writeJSONStr(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

func writeJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Int:
		b.WriteString(string(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case float64:
		switch {
		case math.IsNaN(x):
			b.WriteString("NaN")
		case math.IsInf(x, 1):
			b.WriteString("Infinity")
		case math.IsInf(x, -1):
			b.WriteString("-Infinity")
		default:
			b.WriteString(FloatRepr(x))
		}
	case string:
		writeJSONStr(b, x)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeJSON(b, it)
		}
		b.WriteByte(']')
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		writeJSON(b, items)
	case *Dict:
		if x.Len() == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range x.Keys() {
			if i > 0 {
				b.WriteString(", ")
			}
			writeJSONStr(b, k)
			b.WriteString(": ")
			writeJSON(b, x.Get(k))
		}
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("dyn.Dumps: unsupported %T", v))
	}
}
