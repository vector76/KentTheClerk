package pdftext

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Plain-mode text extraction of a page.
//
// Arithmetic is written so the result does not depend on the compiler
// (float64, a fixed operation order, explicit float64() conversions so no
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
	spaceWidth     float64 // half the width of a space
	strWidths      float64
	strHeight      float64
	tl             float64
	fontSize       float64
	text, output   ustr
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

// tjOperand is what a Tj receives: text (isText) or bytes.
type tjOperand struct {
	isText bool
	text   ustr
	raw    []byte
	other  Object // neither text nor bytes (decided by the font's encoding)
}

// toFloat converts an object to a float: numbers as they are, strings by
// parsing (surrounding whitespace and underscores allowed).
func toFloat(o Object) (float64, error) {
	switch v := o.(type) {
	case Int:
		return float64(v), nil
	case Real:
		return float64(v), nil
	case *String:
		s := strings.TrimSpace(string(v.Raw))
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if err != nil {
			return 0, pdfErr(errValue, "string is not a number")
		}
		return f, nil
	case Name:
		return 0, pdfErr(errValue, "string is not a number")
	}
	return 0, pdfErr(errType, "not a number")
}

func lastRune(a, b ustr) (rune, bool) {
	if len(b) > 0 {
		return b[len(b)-1], true
	}
	if len(a) > 0 {
		return a[len(a)-1], true
	}
	return 0, false
}

// crlfSpaceCheck compares the text position with the previous one: a
// vertical move of more than 0.8 line heights starts a new line, a
// horizontal move of at least a space width adds a space.
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
	if math.Abs(movedHeight) > float64(0.8*minFloat(a, b)) {
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

func minFloat(a, b float64) float64 {
	// a unless b < a (so a NaN in b is ignored)
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

// processOperation applies one content stream operator to the text state.
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
				f, err := toFloat(operands[i])
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
			f, err := toFloat(operands[0])
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
			if f, err = toFloat(operands[0]); err != nil {
				raise(err)
			}
		}
		e.spaceScale = 1 + f
	case "TL":
		scaleX := math.Sqrt(sq(e.tm[0]) + sq(e.tm[2]))
		f := 0.0
		if len(operands) > 0 {
			var err error
			if f, err = toFloat(operands[0]); err != nil {
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
				raise(pdfErr(errType, "Tf font name is an array or dictionary"))
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
			if f, err := toFloat(operands[1]); err == nil {
				e.fontSize = f
			}
		}
	case "Td":
		tx, ty := 0.0, 0.0
		var err error
		if len(operands) > 0 {
			if tx, err = toFloat(operands[0]); err != nil {
				raise(err)
			}
		}
		if len(operands) > 1 {
			if ty, err = toFloat(operands[1]); err != nil {
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
				f, err := toFloat(operands[i])
				if err != nil {
					if errKindOf(err) == errType || errKindOf(err) == errValue {
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

// hashKey gives the dictionary key for an object (equal objects
// get equal keys: a name equals the text string with the same text, a real
// equals the integer with the same value); ok is false for objects that
// cannot be keys (arrays, dictionaries).
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
		if v { // a boolean equals only booleans
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

// handleTj decodes a Tj operand through the current font and appends the
// text.
func (e *extractor) handleTj(ops []tjOperand) {
	f := e.font
	var textOperands ustr
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
			textOperands = make(ustr, 0, len(o.raw))
			for _, x := range o.raw {
				v, ok := f.enc.m[int(x)]
				if ok && len(v) == 1 && v[0] == -1 {
					raise(pdfErr(errType, "encoding maps the code to an object that is not text"))
				}
				if !ok {
					if x >= 0x80 {
						raise(pdfErr(errDecode, "character code above 0x7F has no encoding entry"))
					}
					v = ustr{rune(x)}
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
			c := ustr{r}
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

// decodeWith decodes b with the named encoding, keeping lone surrogates,
// and falls back to latin-1 where that fails.
func decodeWith(b []byte, enc string) ustr {
	switch enc {
	case "charmap", "latin1", "latin-1":
		return decodeLatin1(b)
	case "utf-16-be":
		if s, ok := decodeUTF16BEPass(b); ok {
			return s
		}
		return decodeLatin1(b)
	}
	// CJK codecs; anything else is an unknown codec name and falls back
	// to latin-1.
	if s, ok := decodeCJK(b, enc); ok {
		return s
	}
	return decodeLatin1(b)
}

func (e *extractor) displayStr(textOperands ustr) float64 {
	f := e.font
	widths := 0.0
	for _, r := range textOperands {
		raw := ustr{r}
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
				e.text = append(append(ustr{}, x...), e.text...)
			} else {
				e.text = append(e.text, x...)
			}
		case (xx >= 0x0590 && xx <= 0x08FF) || (xx >= 0xFB1D && xx <= 0xFDFF) || (xx >= 0xFE70 && xx <= 0xFEFF):
			if !e.rtl {
				e.rtl = true
				e.text = nil
			}
			e.text = append(append(ustr{}, x...), e.text...)
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
		return tjOperand{isText: true, text: ustr([]rune(string(v)))}, true
	}
	return tjOperand{}, false
}

// tjOperands converts Tj operands; a non-string operand is kept in other
// for the font's encoding to decide on (see codesFromObject).
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

// codesFromObject decodes an operand that is neither text nor bytes: a
// codec-name encoding fails with errStructure; a table encoding iterates
// it and looks each item up as a character code.
func codesFromObject(o Object, enc *encoding) ustr {
	if enc.isStr {
		raise(pdfErr(errStructure, "text operand is not a string"))
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
		raise(pdfErr(errType, "text operand is neither an array nor a dictionary"))
	}
	var out ustr
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
			raise(pdfErr(errType, "character code is not an integer"))
		}
		v, found := enc.m[code]
		if found && len(v) == 1 && v[0] == -1 {
			raise(pdfErr(errType, "encoding maps the code to an object that is not text"))
		}
		if !found {
			switch {
			case code < 0 || code > 255:
				raise(pdfErr(errValue, "character code out of range"))
			case code >= 0x80:
				raise(pdfErr(errDecode, "character code above 0x7F has no encoding entry"))
			}
			v = ustr{rune(code)}
		}
		out = append(out, v...)
	}
	return out
}

// extractText extracts the text of a page (contentKey true) or a form
// XObject stream (contentKey false).
// level is the form XObject nesting level (0 for the page itself).
func (d *Document) extractText(obj *Dict, self *Stream, contentKey bool, known map[*Stream]bool, level int) ustr {
	if known == nil {
		known = map[*Stream]bool{}
	}
	savedFrame := d.frame
	defer func() { d.frame = savedFrame }()
	frame := extractFrame(level)
	d.frame = frame + 3 // resources and fonts are resolved a few calls down
	e := newExtractor(d)
	e.fonts = map[string]*font{}
	e.fontResources = map[string]*Dict{}

	resObj := getInherited(d, obj, "/Resources")
	if resObj == nil || isNullOrNone(d, resObj) || !truthy(resObj) {
		return ustr{}
	}
	resources, ok := asDict(resObj)
	if !ok {
		// /Resources is not a dictionary: test whether it contains "/Font"
		// as an array, name or string would (a match fails below)
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
				raise(pdfErr(errType, "/Resources is a string that is not text"))
			}
			hasFont = strings.Contains(t.goString(), "/Font")
		default:
			raise(pdfErr(errType, "/Resources is not a dictionary"))
		}
		if hasFont {
			raise(pdfErr(errType, "/Resources is not a dictionary"))
		}
		resources = newDict() // no /Font; /XObject lookups fail (and are caught)
		resources.Set("/__not_a_dict__", Null{})
	}
	if resources.Has("/Font") {
		fontsObj := d.resolve(resources.m["/Font"])
		if truthy(fontsObj) {
			// each entry of /Font (keys of a dictionary, items of an array
			// used as indices, characters or bytes of a string) is loaded
			// as a font; errStructure and errType skip the entry
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
						entries = append(entries, entry{key: x, err: pdfErr(errType, "/Font array entry is not an index")})
						continue
					}
					i := int(idx)
					if i < 0 {
						i += len(fv)
					}
					if i < 0 || i >= len(fv) {
						raise(pdfErr(errRange, "/Font array index out of range"))
					}
					entries = append(entries, entry{key: x, value: fv[i]})
				}
			case Name:
				// indexing by a character: errType for every character
			case *String:
				if _, isText := textString(fv.Raw); !isText {
					for _, b := range fv.Raw {
						if int(b) >= len(fv.Raw) {
							raise(pdfErr(errRange, "/Font string index out of range"))
						}
						// each byte is an integer, not a font
						entries = append(entries, entry{key: Int(b), err: pdfErr(errStructure, "/Font entry is a number, not a font")})
					}
				}
			default:
				raise(pdfErr(errType, "/Font is not a dictionary or array"))
			}
			for _, en := range entries {
				en := en
				err := catch(func() error {
					if en.err != nil {
						return en.err
					}
					k, ok := hashKey(en.key)
					if !ok {
						return pdfErr(errType, "font resource name is an array or dictionary")
					}
					fo := d.resolve(en.value)
					fdict, ok := asDict(fo)
					if !ok {
						return pdfErr(errStructure, "font is not a dictionary")
					}
					e.fontResources[k] = fdict
					fnt, err := d.fontFromResource(fdict)
					if err != nil {
						return err
					}
					e.fonts[k] = fnt
					if fnt.widths.get(fnt.spaceChar.key()) == 0 {
						fnt.spaceWidth = 200.0
					}
					return nil
				})
				if err != nil {
					c := errKindOf(err)
					if c != errStructure && c != errType {
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
			return ustr{}
		}
		// errStructure or errMissingKey while building the content: no text
		noContent := false
		err := catch(func() error {
			data, noContent = d.pageContent(co)
			return nil
		})
		if err != nil {
			if c := errKindOf(err); c == errStructure || c == errMissingKey {
				return ustr{}
			}
			raise(err)
		}
		if noContent {
			return ustr{}
		}
	} else {
		b, err := d.streamData(self)
		if err != nil {
			raise(err)
		}
		data = b
	}
	ops, err := parseContent(data, frame+3)
	d.spend(0, len(ops), 0)
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
						e.processOperation("Tj", nil, []tjOperand{{isText: true, text: ustr{' '}}})
					}
				}
			}
		case o.operator == "TD" && len(operands) >= 2:
			neg, err := negate(operands[1])
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
			var text ustr
			err := catch(func() error {
				xo, ok := resources.Get("/XObject")
				if !ok || resources.Has("/__not_a_dict__") {
					return pdfErr(errMissingKey, "no /XObject resources")
				}
				xobj, ok := asDict(d.resolve(xo))
				if !ok {
					return pdfErr(errType, "/XObject resources are not a dictionary")
				}
				if len(operands) == 0 {
					return pdfErr(errRange, "Do without operand")
				}
				n, ok := operands[0].(Name)
				if !ok {
					return pdfErr(errMissingKey, "Do operand is not a name")
				}
				xr, ok := xobj.Get(n)
				if !ok {
					return pdfErr(errMissingKey, "%s", string(n))
				}
				xform := d.resolve(xr)
				xd, ok := asDict(xform)
				if !ok {
					return pdfErr(errType, "XObject is not a dictionary")
				}
				st, ok := xd.Get("/Subtype")
				if !ok {
					return pdfErr(errMissingKey, "/Subtype")
				}
				if d.resolve(st) == Name("/Image") {
					return nil
				}
				xs, ok := xform.(*Stream)
				if !ok {
					return pdfErr(errStructure, "form is not a stream")
				}
				if known[xs] {
					text = ustr{}
				} else {
					// an errDepth deep down a chain of forms is caught
					// by this handler (see frames.go)
					if extractFrame(level+1)+frameFormWork > recursionLimit {
						return recursionError()
					}
					d.spend(1, 0, 0)
					known[xs] = true
					func() {
						defer delete(known, xs)
						text = d.extractText(xs.Dict, xs, false, known, level+1)
					}()
					d.frame = frame + 3
				}
				d.spend(0, 0, len(text))
				e.output = append(e.output, text...)
				return nil
			})
			if err != nil && len(operands) == 0 {
				// a failed Do without operands raises errRange
				raise(pdfErr(errRange, "Do without operand"))
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

// tjElements iterates a TJ operand: arrays give their strings and numbers,
// byte strings their byte values, names their characters, dictionaries
// their keys; anything else is an errType.
func tjElements(o Object) []tjElement {
	var out []tjElement
	switch v := o.(type) {
	case Array:
		for _, el := range v {
			switch x := el.(type) {
			case *String:
				out = append(out, tjElement{isStr: true, op: tjOperand{raw: x.Raw}})
			case Name:
				out = append(out, tjElement{isStr: true, op: tjOperand{isText: true, text: ustr([]rune(string(x)))}})
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
			out = append(out, tjElement{isStr: true, op: tjOperand{isText: true, text: ustr{r}}})
		}
	case *Dict:
		for _, k := range v.Keys() {
			out = append(out, tjElement{isStr: true, op: tjOperand{isText: true, text: ustr([]rune(string(k)))}})
		}
	default:
		raise(pdfErr(errType, "TJ operand is not an array, string or name"))
	}
	return out
}

func negate(o Object) (Object, error) {
	switch v := o.(type) {
	case Int:
		return -v, nil
	case Real:
		return -v, nil
	}
	return nil, pdfErr(errType, "operand is not a number")
}

// getInherited looks key up in obj and then up its /Parent chain: the
// resolved value, or an empty dictionary where no node has it.
func getInherited(d *Document, obj *Dict, key Name) Object {
	cur := obj
	seen := map[*Dict]bool{}
	for {
		if seen[cur] {
			raise(pdfErr(errLimit, "Detected cycle in /Parent hierarchy"))
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
			// a /Parent that is an array or string can be tested for the
			// key and /Parent (containsKey); where it has one, reading it
			// raises errType.
			for _, k := range []Name{key, "/Parent"} {
				in, err := containsKey(p, k)
				if err != nil {
					raise(err)
				}
				if in {
					raise(pdfErr(errType, "/Parent is not a dictionary"))
				}
			}
			return newDict()
		}
		cur = pd
	}
}

// pageContent builds a page's content bytes: a stream's data, or the data
// of an array of streams joined by newlines; noContent stands for a page
// without extractable content.
func (d *Document) pageContent(co Object) (data []byte, noContent bool) {
	content := d.resolve(co)
	switch c := content.(type) {
	case Array:
		if len(c) > maxContentArrayLength {
			raise(pdfErr(errLimit, "Array-based stream has %d > %d elements.", len(c), maxContentArrayLength))
		}
		var buf []byte
		length := 0
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
				length += len(b)
				if length > maxArrayStreamOutput {
					raise(pdfErr(errLimit, "Array-based stream has at least %d > %d output bytes.", length, maxArrayStreamOutput))
				}
				buf = append(buf, b...)
			}
			if len(buf) == 0 || buf[len(buf)-1] != '\n' {
				length++
				buf = append(buf, '\n')
			}
		}
		return buf, false
	case *Stream:
		b, err := d.streamData(c)
		if err != nil {
			raise(err)
		}
		return b, false
	}
	return nil, true // errStructure: no content can be extracted
}
