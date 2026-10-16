package dyn

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// A value is one of: nil (null), bool, Int (an integer, kept exactly), float64, string, []any (a
// list), *Dict (a dict, with its keys in order).

// Int is an integer of any size, held as its canonical decimal text so it survives a round trip.
type Int string

// IntOf makes an Int from a Go int.
func IntOf(n int) Int { return Int(strconv.Itoa(n)) }

// Int64 returns the value if it fits.
func (i Int) Int64() (int64, bool) {
	n, err := strconv.ParseInt(string(i), 10, 64)
	return n, err == nil
}

// Dict maps string keys to values and keeps the keys in insertion order, which for a dict decoded from
// JSON is the order in the text.
type Dict struct {
	keys []string
	m    map[string]any
}

// NewDict makes an empty Dict.
func NewDict() *Dict { return &Dict{m: map[string]any{}} }

// D builds a Dict from alternating keys and values.
func D(kv ...any) *Dict {
	d := NewDict()
	for i := 0; i+1 < len(kv); i += 2 {
		d.Set(kv[i].(string), kv[i+1])
	}
	return d
}

// Get is the value for k, or nil if absent.
func (d *Dict) Get(k string) any {
	if d == nil {
		return nil
	}
	return d.m[k]
}

// Lookup returns the value and whether the key is present.
func (d *Dict) Lookup(k string) (any, bool) {
	if d == nil {
		return nil, false
	}
	v, ok := d.m[k]
	return v, ok
}

// Has reports whether k is present.
func (d *Dict) Has(k string) bool { _, ok := d.Lookup(k); return ok }

// Set sets k to v: a new key goes last, an existing key keeps its position.
func (d *Dict) Set(k string, v any) {
	if _, ok := d.m[k]; !ok {
		d.keys = append(d.keys, k)
	}
	d.m[k] = v
}

// SetDefault is the value for k, after setting it to v if k is absent.
func (d *Dict) SetDefault(k string, v any) any {
	if old, ok := d.m[k]; ok {
		return old
	}
	d.Set(k, v)
	return v
}

// Keys returns the keys in insertion order.
func (d *Dict) Keys() []string {
	if d == nil {
		return nil
	}
	return d.keys
}

// Len is the number of keys.
func (d *Dict) Len() int {
	if d == nil {
		return 0
	}
	return len(d.keys)
}

// Copy is a shallow copy.
func (d *Dict) Copy() *Dict {
	c := NewDict()
	for _, k := range d.Keys() {
		c.Set(k, d.m[k])
	}
	return c
}

// Truthy is false for nil, false, zero, the empty string and an empty list or dict, and true otherwise.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case Int:
		return x != "0"
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case *Dict:
		return x.Len() > 0
	}
	return true
}

// Or is a if it is truthy, else b.
func Or(a, b any) any {
	if Truthy(a) {
		return a
	}
	return b
}

// Exception is an error the recorder does not recover from: the command stops with it (exit status 1)
// instead of carrying on. Type names its kind, such as TypeError or ValueError.
type Exception struct {
	Type, Msg string
}

func (e *Exception) Error() string { return e.Type + ": " + e.Msg }

// Raise panics with an Exception; the command runner turns it into exit status 1.
func Raise(typ, format string, a ...any) {
	panic(&Exception{typ, fmt.Sprintf(format, a...)})
}

// TypeName is the name messages give v's JSON type: null, boolean, integer, number, string, array or object.
func TypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case Int:
		return "integer"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case *Dict:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

// AsDict is v as an object, raising TypeError if it is not one.
func AsDict(v any) *Dict {
	if d, ok := v.(*Dict); ok {
		return d
	}
	Raise("TypeError", "expected an object, got %s", TypeName(v))
	return nil
}

// Iter is the items of v: a list's items, a string's code points, a dict's keys. Anything else raises
// TypeError.
func Iter(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case string:
		out := []any{}
		for i := 0; i < len(x); {
			_, size := decodeWTF8(x[i:])
			out = append(out, x[i:i+size])
			i += size
		}
		return out
	case *Dict:
		out := []any{}
		for _, k := range x.Keys() {
			out = append(out, k)
		}
		return out
	}
	Raise("TypeError", "expected an array, string or object, got %s", TypeName(v))
	return nil
}

// AsStr is v as a string, raising TypeError if it is not one.
func AsStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	Raise("TypeError", "expected a string, got %s", TypeName(v))
	return ""
}

// Equal compares two values: bools and numbers by numeric value (a NaN or infinity equals nothing),
// strings exactly, lists item by item, dicts by their keys and values in any order.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool, Int, float64:
		fa, oka := number(a)
		fb, okb := number(b)
		if !oka || !okb {
			return false
		}
		return fa.Cmp(fb) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *Dict:
		y, ok := b.(*Dict)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for _, k := range x.Keys() {
			v, ok := y.Lookup(k)
			if !ok || !Equal(x.Get(k), v) {
				return false
			}
		}
		return true
	}
	return false
}

func number(v any) (*big.Float, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewFloat(1), true
		}
		return big.NewFloat(0), true
	case Int:
		f, _, err := big.ParseFloat(string(x), 10, 2000, big.ToNearestEven)
		return f, err == nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return big.NewFloat(x), true
	}
	return nil, false
}

// Str is v as text: a string itself, anything else its Repr.
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Repr(v)
}

// Repr is v written as a literal: None, True, False, numbers, quoted strings (StrRepr), [a, b] for a
// list and {'k': v} for a dict.
func Repr(v any) string {
	var b strings.Builder
	writeRepr(&b, v)
	return b.String()
}

// Tuple is a tuple of strs, for messages that print one, e.g. ('on-record', 'off-record').
func Tuple(items ...string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = Repr(s)
	}
	if len(parts) == 1 {
		return "(" + parts[0] + ",)"
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func writeRepr(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("None")
	case bool:
		if x {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case Int:
		b.WriteString(string(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case float64:
		b.WriteString(FloatRepr(x))
	case string:
		b.WriteString(StrRepr(x))
	case []any:
		b.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeRepr(b, it)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(StrRepr(it))
		}
		b.WriteByte(']')
	case *Dict:
		b.WriteByte('{')
		for i, k := range x.Keys() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(StrRepr(k))
			b.WriteString(": ")
			writeRepr(b, x.Get(k))
		}
		b.WriteByte('}')
	default:
		fmt.Fprint(b, x)
	}
}

// StrRepr is s as a quoted literal: in single quotes, or in double quotes when s contains ' and no ".
// The quote and backslash are escaped, as are \t, \n and \r; other control characters, lone surrogates
// and code points that are not printable (IsPrintable) are written as \x, \u or \U escapes.
func StrRepr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for i := 0; i < len(s); {
		r, size := decodeWTF8(s[i:])
		i += size
		switch {
		case r == quote || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case r >= 0xd800 && r <= 0xdfff:
			fmt.Fprintf(&b, `\u%04x`, r)
		case IsPrintable(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

// FloatRepr is f as the shortest decimal text that reads back as f: in exponent form when the decimal
// exponent is below -4 or at least 16, otherwise with at least one digit after the point; "nan",
// "inf" or "-inf" if not finite.
func FloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	mant, exps, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(exps)
	digits := strings.Replace(mant, ".", "", 1)
	if exp < -4 || exp >= 16 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		es := "+"
		if exp < 0 {
			es, exp = "-", -exp
		}
		return fmt.Sprintf("%s%se%s%02d", sign, m, es, exp)
	}
	if exp < 0 {
		return sign + "0." + strings.Repeat("0", -exp-1) + digits
	}
	if len(digits) <= exp+1 {
		return sign + digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
	}
	return sign + digits[:exp+1] + "." + digits[exp+1:]
}

// ParseInt reads an integer from s: surrounding whitespace, a sign, Unicode decimal digits and single
// underscores between digits are accepted, up to 4300 digits.
func ParseInt(s string) (*big.Int, bool) {
	// surrounding whitespace is what IsSpace reports, except \x1c-\x1f
	s = strings.TrimFunc(s, func(r rune) bool { return IsSpace(r) && !(r >= 0x1c && r <= 0x1f) })
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return nil, false
	}
	var digits strings.Builder
	prevUnderscore := true // no leading underscore
	for _, r := range s {
		if r == '_' {
			if prevUnderscore {
				return nil, false
			}
			prevUnderscore = true
			continue
		}
		if !IsDecimal(r) {
			return nil, false
		}
		digits.WriteByte(byte('0' + DecimalValue(r)))
		prevUnderscore = false
	}
	if prevUnderscore {
		return nil, false
	}
	if digits.Len() > 4300 { // the limit on decimal digits
		return nil, false
	}
	n, ok := new(big.Int).SetString(digits.String(), 10)
	if ok && neg {
		n.Neg(n)
	}
	return n, ok
}
