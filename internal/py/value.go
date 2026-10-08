package py

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Values mirror what json.loads produces and what the Python code builds:
//   nil (None), bool, Int (an int, kept exactly), float64, string, []any (list), *Dict (dict, ordered).

// Int is a Python int, held as its canonical decimal text so any size survives a round trip.
type Int string

// IntOf makes an Int from a Go int.
func IntOf(n int) Int { return Int(strconv.Itoa(n)) }

// Int64 returns the value if it fits.
func (i Int) Int64() (int64, bool) {
	n, err := strconv.ParseInt(string(i), 10, 64)
	return n, err == nil
}

// Dict is an insertion-ordered dict with str keys, as Python dicts loaded from JSON are.
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

// Get is d.get(k): the value, or nil (None) if absent.
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

// Has is `k in d`.
func (d *Dict) Has(k string) bool { _, ok := d.Lookup(k); return ok }

// Set is d[k] = v: an existing key keeps its position.
func (d *Dict) Set(k string, v any) {
	if _, ok := d.m[k]; !ok {
		d.keys = append(d.keys, k)
	}
	d.m[k] = v
}

// SetDefault is d.setdefault(k, v).
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

// Len is len(d).
func (d *Dict) Len() int {
	if d == nil {
		return 0
	}
	return len(d.keys)
}

// Copy is dict(d), a shallow copy.
func (d *Dict) Copy() *Dict {
	c := NewDict()
	for _, k := range d.Keys() {
		c.Set(k, d.m[k])
	}
	return c
}

// Truthy is bool(v).
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

// Or is `a or b`.
func Or(a, b any) any {
	if Truthy(a) {
		return a
	}
	return b
}

// Exception is a Python exception the Python version would have raised and not caught. The Go port
// stops with it (exit status 1, as an uncaught exception does) instead of carrying on.
type Exception struct {
	Type, Msg string
}

func (e *Exception) Error() string { return e.Type + ": " + e.Msg }

// Raise panics with an Exception; the command runner turns it into exit status 1.
func Raise(typ, format string, a ...any) {
	panic(&Exception{typ, fmt.Sprintf(format, a...)})
}

// TypeName is type(v).__name__.
func TypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case Int:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case []any:
		return "list"
	case *Dict:
		return "dict"
	}
	return fmt.Sprintf("%T", v)
}

// AsDict is v as a dict, raising AttributeError (as calling .get on it would) if it is not one.
func AsDict(v any) *Dict {
	if d, ok := v.(*Dict); ok {
		return d
	}
	Raise("AttributeError", "'%s' object has no attribute 'get'", TypeName(v))
	return nil
}

// Iter is iterating v in a for loop: a list's items, a str's characters, a dict's keys.
func Iter(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case string:
		out := []any{}
		for _, r := range x {
			out = append(out, string(r))
		}
		return out
	case *Dict:
		out := []any{}
		for _, k := range x.Keys() {
			out = append(out, k)
		}
		return out
	}
	Raise("TypeError", "'%s' object is not iterable", TypeName(v))
	return nil
}

// AsStr is v as a str, raising TypeError where Python would fail on a str operation.
func AsStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	Raise("TypeError", "expected str, got %s", TypeName(v))
	return ""
}

// Equal is a == b for these values.
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

// Str is str(v).
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Repr(v)
}

// Repr is repr(v).
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

// StrRepr is repr() of a str.
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

// FloatRepr is repr(float): the shortest round-tripping digits, in exponent form when the decimal
// exponent is below -4 or at least 16.
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

// ParseInt is int(s) for a str: surrounding whitespace, a sign, Unicode decimal digits and single
// underscores between digits are accepted.
func ParseInt(s string) (*big.Int, bool) {
	s = Strip(s)
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
	n, ok := new(big.Int).SetString(digits.String(), 10)
	if ok && neg {
		n.Neg(n)
	}
	return n, ok
}
