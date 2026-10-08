package pdftext

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Port of pypdf's plain-mode text extraction: PageObject._extract_text,
// pypdf/_text_extraction/__init__.py and _text_extractor.py.
//
// Arithmetic is written so Go evaluates exactly what CPython evaluates
// (float64, same operation order, explicit float64() conversions so no
// fused multiply-add is emitted).

type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

func mult(m, n matrix) matrix {
	return matrix{
		float64(m[0]*n[0]) + float64(m[1]*n[2]),
		float64(m[0]*n[1]) + float64(m[1]*n[3]),
		float64(m[2]*n[0]) + float64(m[3]*n[2]),
		float64(m[2]*n[1]) + float64(m[3]*n[3]),
		float64(float64(m[4]*n[0])+float64(m[5]*n[2])) + n[4],
		float64(float64(m[4]*n[1])+float64(m[5]*n[3])) + n[5],
	}
}

func orient(m matrix) int {
	if m[3] > 1e-6 {
		return 0
	}
	if m[3] < -1e-6 {
		return 180
	}
	if m[1] > 0 {
		return 90
	}
	return 270
}

func sq(x float64) float64 { return float64(x * x) }

type gstate struct {
	cm         matrix
	fontRes    *Dict
	font       *font
	fontSize   float64
	charScale  float64
	spaceScale float64
	tl         float64
}

type extractor struct {
	d              *Document
	cm, tm         matrix
	stack          []gstate
	cmPrev, tmPrev matrix
	memoCm, memoTm matrix
	charScale      float64
	spaceScale     float64
	spaceWidth     float64 // _space_width (half a space)
	strWidths      float64
	strHeight      float64
	tl             float64
	fontSize       float64
	text, output   pstr
	rtl            bool
	fontRes        *Dict
	font           *font
	fonts          map[string]*font // keyed by hashKey of the resource name
	fontResources  map[string]*Dict
}

func newExtractor(d *Document) *extractor {
	return &extractor{
		d: d, cm: identity, tm: identity, cmPrev: identity, tmPrev: identity,
		memoCm: identity, memoTm: identity, charScale: 1, spaceScale: 1,
		spaceWidth: 500, fontSize: 12, font: notInitializedFont(),
	}
}

// tjOperand is what a Tj receives: Python str (isText) or bytes.
type tjOperand struct {
	isText bool
	text   pstr
	raw    []byte
	other  Object // neither str nor bytes (decided by the font's encoding)
}

// pyFloat is float(obj) for a pypdf object.
func pyFloat(o Object) (float64, error) {
	switch v := o.(type) {
	case Int:
		return float64(v), nil
	case Real:
		return float64(v), nil
	case *String:
		s := strings.TrimSpace(string(v.Raw))
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if err != nil {
			return 0, pyErr("ValueError", "could not convert string to float")
		}
		return f, nil
	case Name:
		return 0, pyErr("ValueError", "could not convert string to float")
	}
	return 0, pyErr("TypeError", "float() argument must be a string or a real number")
}

func lastRune(a, b pstr) (rune, bool) {
	if len(b) > 0 {
		return b[len(b)-1], true
	}
	if len(a) > 0 {
		return a[len(a)-1], true
	}
	return 0, false
}

// crlfSpaceCheck is crlf_space_check.
func (e *extractor) crlfSpaceCheck(strWidths, spacewidth float64) {
	mPrev := mult(e.tmPrev, e.cmPrev)
	m := mult(e.tm, e.cm)
	orientation := orient(m)
	deltaX := m[4] - mPrev[4]
	deltaY := m[5] - mPrev[5]
	scalePrevX := math.Sqrt(sq(e.tmPrev[0]) + sq(e.tmPrev[1]))
	scalePrevY := math.Sqrt(sq(e.tmPrev[2]) + sq(e.tmPrev[3]))
	scaleY := math.Sqrt(sq(e.tm[2]) + sq(e.tm[3]))
	var movedHeight, movedWidth float64
	if orientation == 0 || orientation == 180 {
		movedHeight, movedWidth = deltaY, deltaX
	} else {
		movedHeight, movedWidth = deltaX, deltaY
	}
	a := float64(e.strHeight * scalePrevY)
	b := float64(e.fontSize * scaleY)
	if math.Abs(movedHeight) > float64(0.8*pyMin(a, b)) {
		if last, ok := lastRune(e.output, e.text); ok && last != '\n' {
			e.output = append(e.output, e.text...)
			e.output = append(e.output, '\n')
			e.text = nil
		}
	} else if movedWidth >= float64((spacewidth+strWidths)*scalePrevX) {
		if last, ok := lastRune(e.output, e.text); ok && last != ' ' {
			e.text = append(e.text, ' ')
		}
	}
	e.tmPrev = e.tm
	e.cmPrev = e.cm
}

func pyMin(a, b float64) float64 {
	// min(a, b) returns a unless b < a
	if b < a {
		return b
	}
	return a
}

func (e *extractor) postProcess(strWidths float64) {
	e.crlfSpaceCheck(strWidths, float64(e.fontSize*e.spaceWidth)/1000)
	if len(e.text) == 0 {
		e.memoCm = e.cm
		e.memoTm = e.tm
	}
}

func (e *extractor) flushText() {
	e.output = append(e.output, e.text...)
	e.text = nil
	e.memoCm = e.cm
	e.memoTm = e.tm
}

// processOperation is TextExtraction.process_operation.
func (e *extractor) processOperation(op string, operands []Object, tj []tjOperand) {
	switch op {
	case "BT":
		e.tm = identity
		e.flushText()
	case "ET":
		e.flushText()
	case "q":
		e.stack = append(e.stack, gstate{e.cm, e.fontRes, e.font, e.fontSize, e.charScale, e.spaceScale, e.tl})
	case "Q":
		if n := len(e.stack); n > 0 {
			g := e.stack[n-1]
			e.stack = e.stack[:n-1]
			e.cm, e.fontRes, e.font, e.fontSize, e.charScale, e.spaceScale, e.tl =
				g.cm, g.fontRes, g.font, g.fontSize, g.charScale, g.spaceScale, g.tl
		} else {
			e.cm = identity
		}
	case "cm":
		e.output = append(e.output, e.text...)
		e.text = nil
		ok := len(operands) >= 6
		var v matrix
		if ok {
			for i := 0; i < 6; i++ {
				f, err := pyFloat(operands[i])
				if err != nil {
					ok = false
					break
				}
				v[i] = f
			}
		}
		if ok {
			e.cm = mult(v, e.cm)
		} else {
			e.cm = identity
		}
		e.memoCm = e.cm
		e.memoTm = e.tm
	case "Tz":
		if len(operands) > 0 {
			f, err := pyFloat(operands[0])
			if err != nil {
				raise(err)
			}
			e.charScale = f / 100
		} else {
			e.charScale = 1
		}
	case "Tw":
		f := 0.0
		if len(operands) > 0 {
			var err error
			if f, err = pyFloat(operands[0]); err != nil {
				raise(err)
			}
		}
		e.spaceScale = 1 + f
	case "TL":
		scaleX := math.Sqrt(sq(e.tm[0]) + sq(e.tm[2]))
		f := 0.0
		if len(operands) > 0 {
			var err error
			if f, err = pyFloat(operands[0]); err != nil {
				raise(err)
			}
		}
		e.tl = float64(float64(f*e.fontSize) * scaleX)
	case "Tf":
		if len(e.text) != 0 {
			e.output = append(e.output, e.text...)
		}
		e.text = nil
		e.memoCm = e.cm
		e.memoTm = e.tm
		found := false
		if len(operands) > 0 {
			k, ok := hashKey(operands[0])
			if !ok {
				raise(pyErr("TypeError", "unhashable type"))
			}
			fr, ok1 := e.fontResources[k]
			fnt, ok2 := e.fonts[k]
			if ok1 && ok2 {
				e.fontRes = fr
				e.font = fnt
				found = true
			}
		}
		if !found {
			e.fontRes = nil
			e.font = unknownFont()
		}
		e.spaceWidth = e.font.spaceW() / 2
		if len(operands) > 1 {
			if f, err := pyFloat(operands[1]); err == nil {
				e.fontSize = f
			}
		}
	case "Td":
		tx, ty := 0.0, 0.0
		var err error
		if len(operands) > 0 {
			if tx, err = pyFloat(operands[0]); err != nil {
				raise(err)
			}
		}
		if len(operands) > 1 {
			if ty, err = pyFloat(operands[1]); err != nil {
				raise(err)
			}
		}
		e.tm[4] += float64(tx*e.tm[0]) + float64(ty*e.tm[2])
		e.tm[5] += float64(tx*e.tm[1]) + float64(ty*e.tm[3])
		sw := e.strWidths / 1000
		e.strWidths = 0
		e.postProcess(sw)
	case "Tm":
		ok := len(operands) >= 6
		var v matrix
		if ok {
			for i := 0; i < 6; i++ {
				f, err := pyFloat(operands[i])
				if err != nil {
					if errClass(err) == "TypeError" || errClass(err) == "ValueError" {
						ok = false
						break
					}
					raise(err)
				}
				v[i] = f
			}
		}
		if ok {
			e.tm = v
		} else {
			e.tm = identity
		}
		sw := e.strWidths / 1000
		e.strWidths = 0
		e.postProcess(sw)
	case "T*":
		e.tm[4] -= float64(e.tl * e.tm[2])
		e.tm[5] -= float64(e.tl * e.tm[3])
		sw := e.strWidths / 1000
		e.strWidths = 0
		e.postProcess(sw)
	case "Tj":
		e.handleTj(tj)
		e.postProcess(0)
	}
}

// hashKey gives the dict key Python would use for an object (equal objects
// get equal keys: a NameObject equals the str with the same text, a float
// equals the int with the same value); ok is false for unhashable objects.
func hashKey(o Object) (string, bool) {
	switch v := o.(type) {
	case Name:
		return "s" + string(v), true
	case *String:
		if t, ok := textString(v.Raw); ok {
			return "s" + t.goString(), true
		}
		return "b" + string(v.Raw), true
	case Int:
		return "n" + strconv.FormatInt(int64(v), 10), true
	case Real:
		if float64(v) == math.Trunc(float64(v)) && math.Abs(float64(v)) < 1e18 {
			return "n" + strconv.FormatInt(int64(v), 10), true
		}
		return "f" + strconv.FormatFloat(float64(v), 'g', -1, 64), true
	case Bool:
		if v { // BooleanObject equals only booleans
			return "t1", true
		}
		return "t0", true
	case Null:
		return "null", true
	case Ref:
		return fmt.Sprintf("r%d %d", v.Num, v.Gen), true
	}
	return "", false
}

// handleTj is _handle_tj with get_text_operands / get_display_str.
func (e *extractor) handleTj(ops []tjOperand) {
	f := e.font
	var textOperands pstr
	isStr := false
	if len(ops) > 0 {
		o := ops[0]
		if o.isText {
			textOperands = o.text
			isStr = true
		} else if o.other != nil {
			textOperands = codesFromObject(o.other, f.enc)
		} else if f.enc.isStr {
			textOperands = decodeWith(o.raw, f.enc.name)
		} else {
			textOperands = make(pstr, 0, len(o.raw))
			for _, x := range o.raw {
				v, ok := f.enc.m[int(x)]
				if ok && len(v) == 1 && v[0] == -1 {
					raise(pyErr("TypeError", "sequence item: expected str instance"))
				}
				if !ok {
					if x >= 0x80 {
						raise(pyErr("UnicodeDecodeError", "utf-8 codec can't decode byte"))
					}
					v = pstr{rune(x)}
				}
				textOperands = append(textOperands, v...)
			}
		}
	}
	var fontWidths float64
	if isStr {
		e.text = append(e.text, textOperands...)
		sum := 0.0
		for _, r := range textOperands {
			c := pstr{r}
			if c.eq(f.spaceChar) {
				sum += f.spaceW()
			} else {
				sum += f.getTextWidth(c)
			}
		}
		fontWidths = sum
	} else {
		fontWidths = e.displayStr(textOperands)
	}
	e.strWidths += float64(fontWidths * e.fontSize)
	e.strHeight = e.fontSize
}

// decodeWith is tt.decode(encoding, "surrogatepass") with pypdf's fallback.
func decodeWith(b []byte, enc string) pstr {
	switch enc {
	case "charmap", "latin1", "latin-1":
		return decodeLatin1(b)
	case "utf-16-be":
		if s, ok := decodeUTF16BEPass(b); ok {
			return s
		}
		return decodeLatin1(b)
	}
	// CJK codecs; anything else is an unknown codec name (LookupError),
	// and pypdf falls back to "charmap".
	if s, ok := decodeCJK(b, enc); ok {
		return s
	}
	return decodeLatin1(b)
}

func (e *extractor) displayStr(textOperands pstr) float64 {
	f := e.font
	widths := 0.0
	for _, r := range textOperands {
		raw := pstr{r}
		if raw.eq(f.spaceChar) {
			widths += f.spaceW()
		} else {
			widths += f.getTextWidth(raw)
		}
		x, ok := f.charMap.get(raw)
		if !ok {
			x = raw
		}
		xx := rune(1)
		if len(x) == 1 {
			xx = x[0]
		}
		switch {
		case xx <= 0x2F || (xx >= 0x3A && xx <= 0x40) || (xx >= 0x2000 && xx <= 0x206F) || (xx >= 0x20A0 && xx <= 0x21FF):
			if e.rtl {
				e.text = append(append(pstr{}, x...), e.text...)
			} else {
				e.text = append(e.text, x...)
			}
		case (xx >= 0x0590 && xx <= 0x08FF) || (xx >= 0xFB1D && xx <= 0xFDFF) || (xx >= 0xFE70 && xx <= 0xFEFF):
			if !e.rtl {
				e.rtl = true
				e.text = nil
			}
			e.text = append(append(pstr{}, x...), e.text...)
		default:
			if e.rtl {
				e.rtl = false
				e.text = nil
			}
			e.text = append(e.text, x...)
		}
	}
	return widths
}

func toTjOperand(o Object) (tjOperand, bool) {
	switch v := o.(type) {
	case *String:
		return tjOperand{raw: v.Raw}, true
	case Name:
		return tjOperand{isText: true, text: pstr([]rune(string(v)))}, true
	}
	return tjOperand{}, false
}

// tjOperands converts Tj operands; non-string operands make pypdf raise.
func tjOperands(operands []Object) []tjOperand {
	if len(operands) == 0 {
		return nil
	}
	t, ok := toTjOperand(operands[0])
	if !ok {
		return []tjOperand{{other: operands[0]}}
	}
	return []tjOperand{t}
}

// codesFromObject is get_text_operands on an operand that is neither str nor
// bytes: a str encoding calls .decode() on it (AttributeError); a dict
// encoding iterates it and looks each item up as a character code.
func codesFromObject(o Object, enc *encoding) pstr {
	if enc.isStr {
		raise(pyErr("AttributeError", "object has no attribute 'decode'"))
	}
	var items []Object
	switch v := o.(type) {
	case Array:
		items = v
	case *Dict:
		for _, k := range v.Keys() {
			items = append(items, k)
		}
	default:
		raise(pyErr("TypeError", "object is not iterable"))
	}
	var out pstr
	for _, x := range items {
		code, ok := 0, false
		switch n := x.(type) {
		case Int:
			code, ok = int(n), true
		case Real:
			if float64(n) == float64(int64(n)) {
				code, ok = int(n), true
			}
		}
		if !ok {
			raise(pyErr("TypeError", "'%T' object cannot be interpreted as an integer", x))
		}
		v, found := enc.m[code]
		if found && len(v) == 1 && v[0] == -1 {
			raise(pyErr("TypeError", "sequence item: expected str instance"))
		}
		if !found {
			switch {
			case code < 0 || code > 255:
				raise(pyErr("ValueError", "bytes must be in range(0, 256)"))
			case code >= 0x80:
				raise(pyErr("UnicodeDecodeError", "utf-8 codec can't decode byte"))
			}
			v = pstr{rune(code)}
		}
		out = append(out, v...)
	}
	return out
}

// extractText is PageObject._extract_text for a page (contentKey true) or a
// form XObject stream (contentKey false).
func (d *Document) extractText(obj *Dict, self *Stream, contentKey bool, known map[*Stream]bool) pstr {
	if known == nil {
		known = map[*Stream]bool{}
	}
	e := newExtractor(d)
	e.fonts = map[string]*font{}
	e.fontResources = map[string]*Dict{}

	resObj := getInherited(d, obj, "/Resources")
	if resObj == nil || isNullOrNone(d, resObj) || !truthy(resObj) {
		return pstr{}
	}
	resources, ok := asDict(resObj)
	if !ok {
		// `"/Font" in resources_dict` on whatever the entry holds
		hasFont := false
		switch r := resObj.(type) {
		case Array:
			for _, x := range r {
				if x == Name("/Font") {
					hasFont = true
				}
			}
		case Name:
			hasFont = strings.Contains(string(r), "/Font")
		case *String:
			t, isText := textString(r.Raw)
			if !isText {
				raise(pyErr("TypeError", "a bytes-like object is required"))
			}
			hasFont = strings.Contains(t.goString(), "/Font")
		default:
			raise(pyErr("TypeError", "argument is not iterable"))
		}
		if hasFont {
			raise(pyErr("TypeError", "indices must be integers"))
		}
		resources = newDict() // no /Font; /XObject lookups fail (and are caught)
		resources.Set("/__not_a_dict__", Null{})
	}
	if resources.Has("/Font") {
		fontsObj := d.resolve(resources.m["/Font"])
		if truthy(fontsObj) {
			// for font_resource in font_resources_dict:
			//     font_resources_dict[font_resource].get_object() ...
			type entry struct {
				key   Object
				value Object // nil: the lookup raises lookupErr
				err   error
			}
			var entries []entry
			switch fv := fontsObj.(type) {
			case *Dict, *Stream:
				fd, _ := asDict(fv)
				for _, k := range fd.Keys() {
					entries = append(entries, entry{key: k, value: fd.m[k]})
				}
			case Array:
				for _, x := range fv {
					idx, isInt := x.(Int)
					if !isInt {
						entries = append(entries, entry{key: x, err: pyErr("TypeError", "list indices must be integers")})
						continue
					}
					i := int(idx)
					if i < 0 {
						i += len(fv)
					}
					if i < 0 || i >= len(fv) {
						raise(pyErr("IndexError", "list index out of range"))
					}
					entries = append(entries, entry{key: x, value: fv[i]})
				}
			case Name:
				// str[char]: TypeError for every character
			case *String:
				if _, isText := textString(fv.Raw); !isText {
					for _, b := range fv.Raw {
						if int(b) >= len(fv.Raw) {
							raise(pyErr("IndexError", "index out of range"))
						}
						// bytes[i] is an int: no get_object()
						entries = append(entries, entry{key: Int(b), err: pyErr("AttributeError", "'int' object has no attribute 'get_object'")})
					}
				}
			default:
				raise(pyErr("TypeError", "object is not iterable"))
			}
			for _, en := range entries {
				en := en
				err := catch(func() error {
					if en.err != nil {
						return en.err
					}
					k, ok := hashKey(en.key)
					if !ok {
						return pyErr("TypeError", "unhashable type")
					}
					fo := d.resolve(en.value)
					fdict, ok := asDict(fo)
					if !ok {
						return pyErr("AttributeError", "font is not a dictionary")
					}
					e.fontResources[k] = fdict
					fnt, err := d.fontFromResource(fdict)
					if err != nil {
						return err
					}
					e.fonts[k] = fnt
					if fnt.widths[fnt.spaceChar.key()] == 0 {
						fnt.spaceWidth = 200.0
					}
					return nil
				})
				if err != nil {
					c := errClass(err)
					if c != "AttributeError" && c != "TypeError" {
						raise(err)
					}
				}
			}
		}
	}

	// content
	var data []byte
	if contentKey {
		co, ok := obj.Get("/Contents")
		if !ok {
			return pstr{}
		}
		content := d.resolve(co)
		switch c := content.(type) {
		case Array:
			var buf []byte
			for _, s := range c {
				sr := d.resolve(s)
				if _, isNull := sr.(Null); isNull {
					continue
				}
				if st, ok := sr.(*Stream); ok {
					b, err := d.streamData(st)
					if err != nil {
						raise(err)
					}
					buf = append(buf, b...)
				}
				if len(buf) == 0 || buf[len(buf)-1] != '\n' {
					buf = append(buf, '\n')
				}
			}
			data = buf
		case *Stream:
			b, err := d.streamData(c)
			if err != nil {
				raise(err)
			}
			data = b
		default:
			return pstr{} // AttributeError: no content can be extracted
		}
	} else {
		b, err := d.streamData(self)
		if err != nil {
			raise(err)
		}
		data = b
	}
	ops, err := parseContent(data)
	if err != nil {
		raise(err)
	}

	for _, o := range ops {
		operands := o.operands
		switch {
		case o.operator == "'":
			e.processOperation("T*", nil, nil)
			e.processOperation("Tj", operands, tjOperands(operands))
		case o.operator == "\"" && len(operands) >= 3:
			e.processOperation("Tw", operands[:1], nil)
			e.processOperation("Tc", operands[1:2], nil)
			e.processOperation("T*", nil, nil)
			e.processOperation("Tj", operands[2:], tjOperands(operands[2:]))
		case o.operator == "TJ":
			confirm := e.spaceWidth * 0.95
			if len(operands) > 0 {
				for _, el := range tjElements(operands[0]) {
					if el.isStr {
						e.processOperation("Tj", nil, []tjOperand{el.op})
					}
					if el.isNum && math.Abs(el.num) >= confirm && len(e.text) > 0 && e.text[len(e.text)-1] != ' ' {
						e.processOperation("Tj", nil, []tjOperand{{isText: true, text: pstr{' '}}})
					}
				}
			}
		case o.operator == "TD" && len(operands) >= 2:
			neg, err := pyNeg(operands[1])
			if err != nil {
				raise(err)
			}
			e.processOperation("TL", []Object{neg}, nil)
			e.processOperation("Td", operands, nil)
		case o.operator == "Do":
			e.output = append(e.output, e.text...)
			if len(e.output) > 0 && e.output[len(e.output)-1] != '\n' {
				e.output = append(e.output, '\n')
			}
			var text pstr
			err := catch(func() error {
				xo, ok := resources.Get("/XObject")
				if !ok || resources.Has("/__not_a_dict__") {
					return pyErr("KeyError", "/XObject")
				}
				xobj, ok := asDict(d.resolve(xo))
				if !ok {
					return pyErr("TypeError", "/XObject")
				}
				if len(operands) == 0 {
					return pyErr("IndexError", "Do without operand")
				}
				n, ok := operands[0].(Name)
				if !ok {
					return pyErr("KeyError", "Do operand")
				}
				xr, ok := xobj.Get(n)
				if !ok {
					return pyErr("KeyError", "%s", string(n))
				}
				xform := d.resolve(xr)
				xd, ok := asDict(xform)
				if !ok {
					return pyErr("TypeError", "XObject is not a dictionary")
				}
				st, ok := xd.Get("/Subtype")
				if !ok {
					return pyErr("KeyError", "/Subtype")
				}
				if d.resolve(st) == Name("/Image") {
					return nil
				}
				xs, ok := xform.(*Stream)
				if !ok {
					return pyErr("AttributeError", "form is not a stream")
				}
				if known[xs] {
					text = pstr{}
				} else {
					known[xs] = true
					func() {
						defer delete(known, xs)
						text = d.extractText(xs.Dict, xs, false, known)
					}()
				}
				e.output = append(e.output, text...)
				return nil
			})
			if err != nil && len(operands) == 0 {
				// the warning's operand=operands[0] raises IndexError
				raise(pyErr("IndexError", "list index out of range"))
			}
			e.text = nil
			e.memoCm = e.cm
			e.memoTm = e.tm
		default:
			if o.operator == "Tj" {
				e.processOperation("Tj", operands, tjOperands(operands))
			} else {
				e.processOperation(o.operator, operands, nil)
			}
		}
	}
	e.output = append(e.output, e.text...)
	return e.output
}

type tjElement struct {
	isStr bool
	op    tjOperand
	isNum bool
	num   float64
}

// tjElements iterates a TJ operand the way Python iterates it.
func tjElements(o Object) []tjElement {
	var out []tjElement
	switch v := o.(type) {
	case Array:
		for _, el := range v {
			switch x := el.(type) {
			case *String:
				out = append(out, tjElement{isStr: true, op: tjOperand{raw: x.Raw}})
			case Name:
				out = append(out, tjElement{isStr: true, op: tjOperand{isText: true, text: pstr([]rune(string(x)))}})
			case Int:
				out = append(out, tjElement{isNum: true, num: float64(x)})
			case Real:
				out = append(out, tjElement{isNum: true, num: float64(x)})
			}
		}
	case *String:
		for _, b := range v.Raw {
			out = append(out, tjElement{isNum: true, num: float64(b)})
		}
	case Name:
		for _, r := range string(v) {
			out = append(out, tjElement{isStr: true, op: tjOperand{isText: true, text: pstr{r}}})
		}
	case *Dict:
		for _, k := range v.Keys() {
			out = append(out, tjElement{isStr: true, op: tjOperand{isText: true, text: pstr([]rune(string(k)))}})
		}
	default:
		raise(pyErr("TypeError", "object is not iterable"))
	}
	return out
}

func pyNeg(o Object) (Object, error) {
	switch v := o.(type) {
	case Int:
		return -v, nil
	case Real:
		return -v, nil
	}
	return nil, pyErr("TypeError", "bad operand type for unary -")
}

// getInherited is DictionaryObject.get_inherited (resolved value or nil).
func getInherited(d *Document, obj *Dict, key Name) Object {
	cur := obj
	seen := map[*Dict]bool{}
	for {
		if seen[cur] {
			raise(pyErr("LimitReachedError", "Detected cycle in /Parent hierarchy"))
		}
		seen[cur] = true
		if cur.Has(key) {
			return d.resolve(cur.m[key])
		}
		if !cur.Has("/Parent") {
			return newDict()
		}
		p := d.resolve(cur.m["/Parent"])
		pd, ok := asDict(p)
		if !ok {
			// `key in current` and `"/Parent" in current` on a list or a
			// string work; subscripting one raises TypeError.
			for _, k := range []Name{key, "/Parent"} {
				in, err := pyIn(p, k)
				if err != nil {
					raise(err)
				}
				if in {
					raise(pyErr("TypeError", "indices must be integers"))
				}
			}
			return newDict()
		}
		cur = pd
	}
}
