package pdftext

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// The object model mirrors pypdf's generic objects closely enough to reproduce
// the decisions pypdf's text extraction takes on them.

// Object is one PDF object: Name, *String, Int, Real, Bool, Null, Array,
// *Dict, Ref or *Stream.
type Object interface{}

// Name is a PDF name including its leading "/", decoded like NameObject
// (#xx escapes resolved, then UTF-8 or Latin-1).
type Name string

// String is a PDF string; the raw bytes are kept, decoding happens on use.
type String struct{ Raw []byte }

// Int is a PDF integer (NumberObject).
type Int int64

// Real is a PDF real (FloatObject).
type Real float64

// Bool is a PDF boolean.
type Bool bool

// Null is the PDF null object.
type Null struct{}

// Array is a PDF array.
type Array []Object

// Ref is an indirect reference.
type Ref struct{ Num, Gen int }

// Dict is a PDF dictionary preserving key order (like a Python dict).
type Dict struct {
	keys []Name
	m    map[Name]Object
}

// Stream is a stream object: its dictionary (without /Length, as pypdf drops
// it) and the raw, still-encoded bytes.
type Stream struct {
	Dict    *Dict
	Raw     []byte
	decoded []byte
	decErr  error
	done    bool
}

func newDict() *Dict { return &Dict{m: map[Name]Object{}} }

// Get returns the raw (unresolved) value, like dict.get.
func (d *Dict) Get(k Name) (Object, bool) {
	if d == nil {
		return nil, false
	}
	v, ok := d.m[k]
	return v, ok
}

// Has reports whether k is a key.
func (d *Dict) Has(k Name) bool {
	if d == nil {
		return false
	}
	_, ok := d.m[k]
	return ok
}

// Set sets k, keeping the position of an existing key.
func (d *Dict) Set(k Name, v Object) {
	if _, ok := d.m[k]; !ok {
		d.keys = append(d.keys, k)
	}
	d.m[k] = v
}

// Delete removes k.
func (d *Dict) Delete(k Name) {
	if _, ok := d.m[k]; !ok {
		return
	}
	delete(d.m, k)
	for i, kk := range d.keys {
		if kk == k {
			d.keys = append(d.keys[:i:i], d.keys[i+1:]...)
			break
		}
	}
}

// Keys returns the keys in insertion order.
func (d *Dict) Keys() []Name {
	if d == nil {
		return nil
	}
	return d.keys
}

// Len is the number of entries.
func (d *Dict) Len() int {
	if d == nil {
		return 0
	}
	return len(d.keys)
}

func (d *Dict) copyDict() *Dict {
	n := newDict()
	for _, k := range d.keys {
		n.Set(k, d.m[k])
	}
	return n
}

// truthy is Python's bool() of the pypdf object.
func truthy(o Object) bool {
	switch v := o.(type) {
	case nil:
		return false
	case Int:
		return v != 0
	case Real:
		return v != 0
	case *String:
		return len(v.Raw) > 0
	case Name:
		return v != ""
	case Array:
		return len(v) > 0
	case *Dict:
		return v.Len() > 0
	case *Stream:
		return v.Dict.Len() > 0
	}
	return true // Null, Bool, Ref: no __bool__/__len__ in pypdf
}

// errors -------------------------------------------------------------------

// pyError is an exception pypdf would raise, tagged with its Python class so
// callers can mimic which except clauses catch it.
type pyError struct {
	Class string // "PdfReadError", "TypeError", "AttributeError", "KeyError", ...
	Msg   string
}

func (e *pyError) Error() string { return e.Class + ": " + e.Msg }

func pyErr(class, format string, a ...interface{}) error {
	return &pyError{Class: class, Msg: fmt.Sprintf(format, a...)}
}

// isReadError reports whether err is a PdfReadError (or subclass).
func isReadError(err error) bool {
	switch errClass(err) {
	case "PdfReadError", "PdfStreamError", "FileNotDecryptedError", "EmptyFileError":
		return true
	}
	return false
}

func errClass(err error) string {
	var pe *pyError
	if errors.As(err, &pe) {
		return pe.Class
	}
	return ""
}

// lexer --------------------------------------------------------------------

const (
	whitespaceChars = "\x00\t\n\f\r " // pypdf WHITESPACES
)

func isPdfWhite(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// isPySpace is bytes.isspace() (and the regex \s): no NUL.
func isPySpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// isNameDelim matches NameObject.delimiter_pattern: \s|[()<>\[\]{}/%].
func isNameDelim(c byte) bool {
	if isPySpace(c) {
		return true
	}
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// isNumberChar is the complement of NumberPattern [^+-.0-9]; the class
// "+-." is the range 0x2B-0x2E, so ',' counts as a number character.
func isNumberChar(c byte) bool {
	return (c >= 0x2B && c <= 0x2E) || (c >= '0' && c <= '9')
}

type parser struct {
	data  []byte
	pos   int
	doc   *Document // nil inside content streams
	frame int       // Python frame number of a top-level read_object call
}

// slice returns data[s:e] clamped to the data (positions past the end are
// allowed, as with a BytesIO).
func (p *parser) slice(s, e int) []byte {
	if s > len(p.data) {
		s = len(p.data)
	}
	if e > len(p.data) {
		e = len(p.data)
	}
	if e < s {
		e = s
	}
	return p.data[s:e]
}

// read reads up to n bytes, like stream.read(n).
func (p *parser) read(n int) []byte {
	if p.pos >= len(p.data) {
		return nil
	}
	e := p.pos + n
	if e > len(p.data) {
		e = len(p.data)
	}
	b := p.data[p.pos:e]
	p.pos = e
	return b
}

// read1 returns the next byte, or -1 at EOF.
func (p *parser) read1() int {
	if p.pos >= len(p.data) {
		return -1
	}
	c := p.data[p.pos]
	p.pos++
	return int(c)
}

func (p *parser) back(n int) {
	p.pos -= n
	if p.pos < 0 {
		p.pos = 0
	}
}

// readNonWhitespace is pypdf's read_non_whitespace: returns the first
// non-whitespace byte (consumed) or -1.
func (p *parser) readNonWhitespace() int {
	for {
		c := p.read1()
		if c < 0 || !isPdfWhite(byte(c)) {
			return c
		}
	}
}

// skipOverWhitespace is skip_over_whitespace (consumes one non-white byte too).
func (p *parser) skipOverWhitespace() bool {
	cnt := 0
	for {
		c := p.read1()
		if c < 0 || !isPdfWhite(byte(c)) {
			break
		}
		cnt++
	}
	return cnt > 0
}

func (p *parser) skipOverComment() error {
	if p.pos >= len(p.data) {
		// read(1) gives b"" and the seek(-1, 1) still moves back
		p.back(1)
		return nil
	}
	if p.data[p.pos] == '%' {
		for {
			c := p.read1()
			if c == '\n' || c == '\r' {
				return nil
			}
			if c < 0 {
				return pyErr("PdfStreamError", "File ended unexpectedly.")
			}
		}
	}
	return nil
}

func (p *parser) readUntilWhitespace() []byte {
	s := p.pos
	for p.pos < len(p.data) && !isPySpace(p.data[p.pos]) {
		p.pos++
	}
	b := p.slice(s, p.pos)
	if p.pos < len(p.data) {
		p.pos++ // the whitespace is consumed
	}
	return b
}

func (p *parser) readUntilDelim() []byte {
	s := p.pos
	for p.pos < len(p.data) && !isNameDelim(p.data[p.pos]) {
		p.pos++
	}
	return p.slice(s, p.pos)
}

// readObject is pypdf's read_object.
func (p *parser) readObject() (Object, error) {
	return p.readObjectDepth(p.frame)
}

// pyRecursionLimit is Python's default sys.getrecursionlimit(). Parsers
// track the Python stack depth pypdf would be at (see frames.go) so that
// deeply nested input fails where pypdf's RecursionError would hit; this
// also bounds the Go recursion.
const pyRecursionLimit = 1000

func recursionError() error {
	return pyErr("RecursionError", "maximum recursion depth exceeded")
}

// readObjectDepth is read_object running in Python frame number depth;
// the terminal readers need two more frames (X.read_from_stream and a
// helper such as read_until_regex).
func (p *parser) readObjectDepth(depth int) (Object, error) {
	if depth > pyRecursionLimit {
		return nil, recursionError()
	}
	tok := -1
	if p.pos < len(p.data) {
		tok = int(p.data[p.pos])
	}
	if tok != '<' && tok != '[' && tok != '%' && depth+2 > pyRecursionLimit {
		return nil, recursionError()
	}
	switch {
	case tok == '/':
		return p.readName()
	case tok == '<':
		if p.pos+1 < len(p.data) && p.data[p.pos+1] == '<' {
			return p.readDict(depth + 1)
		}
		if depth+2 > pyRecursionLimit {
			return nil, recursionError()
		}
		return p.readHexString()
	case tok == '[':
		return p.readArray(depth + 1)
	case tok == 't' || tok == 'f':
		w := p.read(4)
		if string(w) == "true" {
			return Bool(true), nil
		}
		if string(w) == "fals" {
			p.read(1)
			return Bool(false), nil
		}
		return nil, pyErr("PdfReadError", "Could not read Boolean object")
	case tok == '(':
		return p.readLiteralString()
	case tok == 'e' && string(p.slice(p.pos, p.pos+6)) == "endobj":
		p.pos += 6
		return Null{}, nil
	case tok == 'n':
		if string(p.read(4)) != "null" {
			return nil, pyErr("PdfReadError", "Could not read Null object")
		}
		return Null{}, nil
	case tok == '%':
		// skip_over_comment, then read_object recurses: one frame each
		if depth+1 > pyRecursionLimit {
			return nil, recursionError()
		}
		if err := p.skipOverComment(); err != nil {
			return nil, err
		}
		p.readNonWhitespace()
		p.back(1)
		return p.readObjectDepth(depth + 1)
	case tok < 0 || (tok >= '0' && tok <= '9') || tok == '+' || tok == '-' || tok == '.':
		if p.matchIndirect() {
			if p.doc == nil {
				return nil, pyErr("AssertionError", "mypy")
			}
			return p.readIndirect()
		}
		return p.readNumber()
	}
	if tok == 'e' {
		p.read(6)
	}
	p.readUntilWhitespace()
	return nil, pyErr("PdfReadError", "Invalid Elementary Object starting with %q", rune(tok))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// matchIndirect applies IndirectPattern [+-]?(\d+)\s+(\d+)\s+R[^a-zA-Z] to
// the next 20 bytes.
func (p *parser) matchIndirect() bool {
	end := minInt(p.pos+20, len(p.data))
	b := p.slice(p.pos, end)
	i := 0
	if i < len(b) && (b[i] == '+' || b[i] == '-') {
		i++
	}
	digits := func() bool {
		s := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		return i > s
	}
	spaces := func() bool {
		s := i
		for i < len(b) && isPySpace(b[i]) {
			i++
		}
		return i > s
	}
	if !digits() || !spaces() || !digits() || !spaces() {
		return false
	}
	if i >= len(b) || b[i] != 'R' {
		return false
	}
	i++
	if i >= len(b) {
		return false
	}
	c := b[i]
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return false
	}
	return true
}

// readIndirect is IndirectObject.read_from_stream.
func (p *parser) readIndirect() (Object, error) {
	var idnum []byte
	for {
		c := p.read1()
		if c < 0 {
			return nil, pyErr("PdfStreamError", "Stream has ended unexpectedly")
		}
		if isPySpace(byte(c)) {
			break
		}
		idnum = append(idnum, byte(c))
	}
	var gen []byte
	for {
		c := p.read1()
		if c < 0 {
			return nil, pyErr("PdfStreamError", "Stream has ended unexpectedly")
		}
		if isPySpace(byte(c)) {
			if len(gen) == 0 {
				continue
			}
			break
		}
		gen = append(gen, byte(c))
	}
	if r := p.readNonWhitespace(); r != 'R' {
		return nil, pyErr("PdfReadError", "Error reading indirect object reference")
	}
	n, err1 := pyInt(idnum)
	g, err2 := pyInt(gen)
	if err1 != nil || err2 != nil {
		return nil, pyErr("ValueError", "invalid literal for int()")
	}
	return Ref{Num: int(n), Gen: int(g)}, nil
}

// pyInt is Python's int(bytes) for the digit strings we see.
func pyInt(b []byte) (int64, error) {
	s := string(b)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// tokenLimit mimics read_until_regex's length limit: the token starting at
// p.pos is read in chunks of 16, 32, 64... bytes, and LimitReachedError is
// raised once the bytes read reach the limit before the delimiter is seen.
func (p *parser) tokenLimit(isDelim func(byte) bool, limit int) error {
	start := p.pos
	if start >= len(p.data) {
		return nil
	}
	n := len(p.data) - start
	m := -1
	for i := start; i < len(p.data); i++ {
		if isDelim(p.data[i]) {
			m = i - start
			break
		}
	}
	total, chunk := 0, 16
	for {
		tok := n - total
		if tok > chunk {
			tok = chunk
		}
		if tok <= 0 {
			return nil
		}
		cur := total + tok
		if cur >= limit {
			return pyErr("LimitReachedError", "Read stream length of %d exceeds maximum allowed length of %d.", cur, limit)
		}
		if m >= 0 && m < cur {
			return nil
		}
		total = cur
		if chunk < 8192 {
			chunk <<= 1
		}
	}
}

func isNotNumberChar(c byte) bool { return !isNumberChar(c) }

// readNumber is NumberObject.read_from_stream.
func (p *parser) readNumber() (Object, error) {
	if err := p.tokenLimit(isNotNumberChar, 64); err != nil {
		return nil, err
	}
	s := p.pos
	for p.pos < len(p.data) && isNumberChar(p.data[p.pos]) {
		p.pos++
	}
	num := p.slice(s, p.pos)
	for _, c := range num {
		if c == '.' {
			f, ok := pyFloatBytes(num)
			if !ok {
				return Real(0), nil
			}
			return Real(f), nil
		}
	}
	v, err := pyInt(num)
	if err != nil {
		return Int(0), nil
	}
	return Int(v), nil
}

// pyFloatBytes is Python float() on a token made of number characters.
func pyFloatBytes(b []byte) (float64, bool) {
	s := string(b)
	for _, c := range s {
		if c == ',' {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// readName is NameObject.read_from_stream.
func (p *parser) readName() (Object, error) {
	p.pos++ // "/"
	if err := p.tokenLimit(isNameDelim, 4096); err != nil {
		return nil, err
	}
	raw := append([]byte{'/'}, p.readUntilDelim()...)
	return Name(decodeName(unnumber(raw))), nil
}

func unnumber(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] == '#' {
			e := i + 3
			if e > len(b) {
				e = len(b)
			}
			if v, ok := hexByte(b[i+1 : e]); ok {
				out = append(out, v)
				i += 3
				continue
			}
		}
		out = append(out, b[i])
		i++
	}
	return out
}

// hexByte is bytearray.append(int(x, 16)) for the (up to) two bytes after a
// "#": Python's int() also takes one digit or a "+" sign; negative values
// fail on append.
func hexByte(x []byte) (byte, bool) {
	if len(x) > 0 && x[0] == '+' {
		x = x[1:]
	}
	if len(x) == 0 {
		return 0, false
	}
	v := 0
	for _, c := range x {
		h := hexVal(c)
		if h < 0 {
			return 0, false
		}
		v = v*16 + h
	}
	return byte(v), true
}

// decodeName decodes name bytes like NameObject: utf-8, gbk, latin-1.
func decodeName(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	if s, ok := decodeCJK(b, "gbk"); ok {
		return s.goString()
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

var escapeDict = map[byte]byte{
	'n': '\n', 'r': '\r', 't': '\t', 'b': '\b', 'f': '\f', '(': '(', ')': ')',
	'/': '/', '\\': '\\', ' ': ' ', '%': '%', '<': '<', '>': '>', '[': '[',
	']': ']', '#': '#', '_': '_', '&': '&', '$': '$',
}

// readLiteralString is read_string_from_stream.
func (p *parser) readLiteralString() (Object, error) {
	p.pos++
	parens := 1
	var txt []byte
	for {
		c := p.read1()
		if c < 0 {
			return nil, pyErr("PdfStreamError", "Stream has ended unexpectedly")
		}
		tok := byte(c)
		if tok == '(' {
			parens++
		} else if tok == ')' {
			parens--
			if parens == 0 {
				break
			}
		} else if tok == '\\' {
			c = p.read1()
			if c < 0 {
				// stream.read(1) == b"": KeyError, not octal, b"" in b"\n\r" is True
				p.read1()
				continue
			}
			tok = byte(c)
			if e, ok := escapeDict[tok]; ok {
				txt = append(txt, e)
				continue
			}
			if tok >= '0' && tok <= '7' {
				sav := p.pos - 1
				v := int(tok - '0')
				for k := 0; k < 2; k++ {
					n := p.read1()
					if n >= '0' && n <= '7' {
						v = v*8 + (n - '0')
					} else {
						if n >= 0 {
							p.back(1)
						}
						break
					}
				}
				if v > 255 {
					txt = append(txt, '\\')
					p.pos = sav
				} else {
					txt = append(txt, byte(v))
				}
				continue
			}
			if tok == '\n' || tok == '\r' {
				n := p.read1()
				if n >= 0 && n != '\n' && n != '\r' {
					p.back(1)
				}
				continue
			}
			txt = append(txt, '\\')
		}
		txt = append(txt, tok)
	}
	return &String{Raw: txt}, nil
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// readHexString is read_hex_string_from_stream.
func (p *parser) readHexString() (Object, error) {
	p.pos++
	var arr []byte
	var x []byte
	for {
		c := p.readNonWhitespace()
		if c < 0 {
			return nil, pyErr("PdfStreamError", "Stream has ended unexpectedly")
		}
		if c == '>' {
			break
		}
		if hexVal(byte(c)) < 0 {
			return nil, pyErr("PdfStreamError", "Invalid hexadecimal character in hex string")
		}
		x = append(x, byte(c))
		if len(x) == 2 {
			arr = append(arr, byte(hexVal(x[0])<<4|hexVal(x[1])))
			x = x[:0]
		}
	}
	if len(x) == 1 {
		arr = append(arr, byte(hexVal(x[0])<<4))
	}
	return &String{Raw: arr}, nil
}

// readArray is ArrayObject.read_from_stream.
// readArray is ArrayObject.read_from_stream running in Python frame depth.
func (p *parser) readArray(depth int) (Object, error) {
	if depth > pyRecursionLimit {
		return nil, recursionError()
	}
	p.pos++
	arr := Array{}
	for {
		c := p.read1()
		for c >= 0 && isPySpace(byte(c)) {
			c = p.read1()
		}
		if c < 0 {
			break
		}
		if c == '%' {
			p.back(1)
			if err := p.skipOverComment(); err != nil {
				return nil, err
			}
			continue
		}
		if c == ']' {
			break
		}
		p.back(1)
		o, err := p.readObjectDepth(depth + 1)
		if err != nil {
			return nil, err
		}
		arr = append(arr, o)
	}
	return arr, nil
}

// readDict is DictionaryObject.read_from_stream, including streams.
// readDict is DictionaryObject.read_from_stream running in Python frame depth.
func (p *parser) readDict(depth int) (Object, error) {
	if depth > pyRecursionLimit {
		return nil, recursionError()
	}
	p.pos += 2
	data := newDict()
	for {
		c := p.readNonWhitespace()
		if c == 0 {
			continue
		}
		if c == '%' {
			p.back(1)
			if err := p.skipOverComment(); err != nil {
				return nil, err
			}
			continue
		}
		if c < 0 {
			return nil, pyErr("PdfStreamError", "Stream has ended unexpectedly")
		}
		if c == '>' {
			p.read1()
			break
		}
		p.back(1)
		key, err := p.readObjectDepth(depth + 1)
		if err != nil {
			if isReadError(err) {
				continue
			}
			return p.dictError(data, err)
		}
		if _, isNull := key.(Null); isNull {
			break
		}
		kn, ok := key.(Name)
		if !ok {
			continue // PdfReadError("Expecting a NameObject...") is logged and skipped
		}
		p.readNonWhitespace()
		p.back(1)
		val, err := p.readObjectDepth(depth + 1)
		if err != nil {
			return p.dictError(data, err)
		}
		if old, ok := data.Get(kn); !ok || !truthy(old) {
			data.Set(kn, val)
		}
	}
	pos := p.pos
	s := p.readNonWhitespace()
	if s == 's' && string(p.read(5)) == "tream" {
		eol := p.read1()
		for eol == ' ' {
			eol = p.read1()
		}
		if eol != '\n' && eol != '\r' {
			return nil, pyErr("PdfStreamError", "Stream data must be followed by a newline")
		}
		if eol == '\r' {
			if p.read1() != '\n' {
				p.back(1)
			}
		}
		lengthObj, ok := data.Get("/Length")
		if !ok {
			lengthObj = Int(-1)
		}
		if r, isRef := lengthObj.(Ref); isRef {
			if p.doc == nil {
				return nil, pyErr("AssertionError", "mypy")
			}
			t := p.pos
			// pdf.get_object(length) is called from this frame
			saved := p.doc.frame
			p.doc.frame = depth - 1
			o, err := p.doc.getObject(r)
			p.doc.frame = saved
			if err != nil {
				return nil, err
			}
			lengthObj = o
			p.pos = t
		}
		length := int64(-1)
		switch l := lengthObj.(type) {
		case Int:
			length = int64(l)
		case Real:
			// stream.read(float) raises TypeError in Python
			return nil, pyErr("TypeError", "length must be int")
		case nil:
			length = -1
		default:
			return nil, pyErr("TypeError", "length must be int")
		}
		pstart := p.pos
		var sdata []byte
		if length > maxDeclaredStreamLength {
			return nil, pyErr("LimitReachedError", "Declared stream length of %d exceeds maximum allowed length.", length)
		}
		if length >= 0 {
			sdata = p.read(int(minInt64(length, int64(len(p.data)))))
		} else {
			idx := indexOf(p.data[p.pos:], []byte("endstream"))
			if idx >= maxDeclaredStreamLength-16 || (idx < 0 && len(p.data)-p.pos >= maxDeclaredStreamLength-16) {
				// read_until_regex(..., length=MAX_DECLARED_STREAM_LENGTH)
				return nil, pyErr("LimitReachedError", "Read stream length exceeds maximum allowed length.")
			}
			if idx < 0 {
				sdata = p.data[p.pos:]
				p.pos = len(p.data)
			} else {
				sdata = p.data[p.pos : p.pos+idx]
				p.pos += idx
			}
		}
		e := p.readNonWhitespace()
		nd := p.read(8)
		var tail []byte
		if e >= 0 {
			tail = append([]byte{byte(e)}, nd...)
		}
		if string(tail) != "endstream" {
			p.back(10)
			end := p.read(9)
			if string(end) == "endstream" {
				if len(sdata) > 0 {
					sdata = sdata[:len(sdata)-1]
				}
			} else if p.doc != nil {
				p.pos = pstart
				end := len(p.data)
				for _, entries := range p.doc.xref {
					for _, x := range entries {
						if pstart < x && x <= end {
							end = x
						}
					}
				}
				if end-pstart >= maxDeclaredStreamLength {
					return nil, pyErr("LimitReachedError", "Requested length of %d exceeds maximum allowed length.", end-pstart)
				}
				rest := p.data[pstart:end]
				idx := indexOf(rest, []byte("endstream"))
				if idx < 0 {
					p.pos = end // the bytes up to the next object were read
					return nil, pyErr("PdfReadError", "Unable to find 'endstream' marker")
				}
				if idx >= 1 {
					sdata = rest[:idx-1]
				} else {
					sdata = rest[:0]
				}
				p.pos = pstart + idx + 9
			} else {
				return nil, pyErr("PdfReadError", "Unable to find 'endstream' marker after stream")
			}
		}
		data.Delete("/Length")
		return &Stream{Dict: data, Raw: sdata}, nil
	}
	p.pos = pos
	return data, nil
}

// dictError is the except clauses around reading a key or value:
// RecursionError and LimitReachedError become a PdfReadError that
// propagates; anything else is logged and the partial dict is returned.
func (p *parser) dictError(data *Dict, err error) (Object, error) {
	switch errClass(err) {
	case "RecursionError", "LimitReachedError":
		return nil, pyErr("PdfReadError", "%s('%s')", errClass(err), err.(*pyError).Msg)
	}
	return data, nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func indexOf(h, n []byte) int { return bytes.Index(h, n) }

// newParser starts a parser over the document's bytes at pos (clamped).
func newParser(d *Document, pos int) *parser {
	if pos < 0 {
		pos = 0
	}
	return &parser{data: d.data, doc: d, pos: pos, frame: d.frame + 3}
}
