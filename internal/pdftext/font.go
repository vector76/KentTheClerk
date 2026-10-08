package pdftext

import (
	"math"
	"strings"
)

// Port of the parts of pypdf/_font.py that text extraction uses.

const fixedPitch = 1 // FontFlags.FIXED_PITCH

type font struct {
	name       string
	enc        *encoding
	charMap    *ordMap
	widths     map[string]float64 // pstr key (single chars) or "default"
	spaceWidth float64
	spaceChar  pstr
	flags      int64
}

func (f *font) width(c pstr) float64 {
	w, ok := f.widths[c.key()]
	if !ok {
		w = f.widths["default"]
	}
	if isBad(w) {
		raise(pyErr("TypeError", "unsupported operand type(s) for +"))
	}
	return w
}

// spaceW is font.space_width used in arithmetic.
func (f *font) spaceW() float64 {
	if isBad(f.spaceWidth) {
		raise(pyErr("TypeError", "unsupported operand type(s)"))
	}
	return f.spaceWidth
}

// getTextWidth is Font.get_text_width for a single character string.
func (f *font) getTextWidth(c pstr) float64 {
	sum := 0.0
	for _, r := range c {
		sum += f.width(pstr{r})
	}
	return sum
}

func notInitializedFont() *font {
	return &font{
		name:       "NotInitialized",
		enc:        &encoding{isStr: true, name: "charmap"},
		charMap:    newOrdMap(),
		widths:     map[string]float64{"default": 500},
		spaceWidth: 250,
		spaceChar:  pstr{' '},
		flags:      32,
	}
}

func unknownFont() *font {
	e := &encoding{m: map[int]pstr{}}
	for i := 0; i < 256; i++ {
		e.set(i, pstr{0xFFFD})
	}
	return &font{
		name:       "Unknown",
		enc:        e,
		charMap:    newOrdMap(),
		widths:     map[string]float64{"default": 500},
		spaceWidth: 250,
		spaceChar:  pstr{' '},
		flags:      32,
	}
}

// pyNum converts a numeric object as int()/float() would see it; ok=false
// where Python would raise TypeError.
func (d *Document) num(o Object) (float64, bool, bool) {
	switch v := o.(type) {
	case Int:
		return float64(v), true, true
	case Real:
		return float64(v), false, true
	case Ref:
		return d.num(d.resolve(v))
	}
	return 0, false, false
}

// fontFromResource is Font.from_font_resource.
func (d *Document) fontFromResource(fd *Dict) (*font, error) {
	loadTables()
	nameObj := d.resolveOpt(fd, "/BaseFont")
	name := "Unknown"
	if nameObj != nil {
		n, err := removePrefixable(nameObj)
		if err != nil {
			return nil, err
		}
		name = strings.TrimPrefix(n, "/")
	}
	if st := d.resolveOpt(fd, "/Subtype"); st != nil {
		if _, err := removePrefixable(st); err != nil {
			return nil, err
		}
	}
	enc, cmap, err := d.getEncoding(fd)
	if err != nil {
		return nil, err
	}
	widths := map[string]float64{}
	var flags int64 = 32 // FontDescriptor() default
	var flagsIsFloat bool

	subRaw, _ := fd.Get("/Subtype")
	sub, _ := subRaw.(Name) // raw get: an indirect /Subtype compares unequal
	if sub == "/Type1" || sub == "/MMType1" || sub == "/TrueType" || sub == "/Type3" {
		interpretable := true
		if sub == "/Type3" && !fd.Has("/ToUnicode") {
			// pdf_font_dict.get("/CharProcs") or []  (raw value; iterating
			// a reference resolves it)
			if cpRaw, ok := fd.Get("/CharProcs"); ok && truthy(cpRaw) {
				items, err := pyIter(d.resolve(cpRaw))
				if err != nil {
					return nil, err
				}
				for _, k := range items {
					n, isName := k.(Name)
					if !isName {
						if t, isStr := k.(*String); isStr {
							if txt, ok := textString(t.Raw); ok {
								n, isName = Name(txt.goString()), true
							}
						}
					}
					if _, ok := adobeGlyphs[string(n)]; !isName || !ok {
						interpretable = false
						break
					}
				}
			}
		}
		if interpretable {
			if fd.Has("/Widths") {
				arr, err := pyIter(d.resolve(fd.m["/Widths"]))
				if err != nil {
					return nil, err
				}
				first := 0
				firstOK := true
				if fcRaw, ok := fd.Get("/FirstChar"); ok {
					fc, isInt := fcRaw.(Int)
					first, firstOK = int(fc), isInt
				}
				for idx, w := range arr {
					v, err := d.pyIntObj(w)
					if err != nil {
						return nil, err
					}
					if !firstOK {
						return nil, pyErr("TypeError", "FirstChar")
					}
					c := idx + first
					if c < 0 || c > 0x10FFFF {
						return nil, pyErr("ValueError", "chr() arg not in range")
					}
					widths[pstr{rune(c)}.key()] = v
				}
			} else if cf, ok := coreFonts[name]; ok {
				flags = cf.flags
				if !enc.isStr {
					for _, code := range enc.codes {
						ch := enc.m[code]
						if w, ok := cf.widths[ch.key()]; ok {
							widths[pstr{rune(code)}.key()] = w
						}
					}
				} else {
					for code := 0; code < 256; code++ {
						k := pstr{rune(code)}.key()
						if w, ok := cf.widths[k]; ok {
							widths[k] = w
						}
					}
				}
			}
			if fd.Has("/FontDescriptor") {
				descObj := d.resolve(fd.m["/FontDescriptor"])
				desc, ok := asDict(descObj)
				if !ok {
					// `"/MissingWidth" in obj`, then the descriptor keys
					if err := nonDictDescriptor(descObj, true); err != nil {
						return nil, err
					}
					desc = newDict()
				}
				if desc.Has("/MissingWidth") {
					v, err := d.anyWidth(desc.m["/MissingWidth"])
					if err != nil {
						return nil, err
					}
					widths["default"] = v
				}
				f, isF, err := d.parseFontDescriptor(desc)
				if err != nil {
					return nil, err
				}
				flags, flagsIsFloat = f, isF
			} else if fd.Has("/FontBBox") {
				// Type 3 without a descriptor: tuple(map(float, bbox))
				if err := d.checkBBox(fd.m["/FontBBox"]); err != nil {
					return nil, err
				}
			}
		}
	} else {
		dfs, ok := fd.Get("/DescendantFonts")
		if !ok {
			return nil, pyErr("KeyError", "/DescendantFonts")
		}
		var arr Array
		switch v := d.resolve(dfs).(type) {
		case Array:
			arr = v
		case *Dict, *Stream:
			// enumerate() yields the keys; assigning dict[0] = key raises
			if dd, _ := asDict(v); dd.Len() > 0 {
				return nil, pyErr("ValueError", "Key must be a PdfObject")
			}
		case Name, *String:
			if items, _ := pyIter(v); len(items) > 0 {
				return nil, pyErr("AttributeError", "'str' object has no attribute 'get_object'")
			}
		default:
			return nil, pyErr("TypeError", "object is not iterable")
		}
		for _, dfo := range arr {
			df, ok := asDict(d.resolve(dfo))
			if !ok {
				return nil, pyErr("AttributeError", "descendant font")
			}
			if err := d.collectCIDWidths(df, widths); err != nil {
				return nil, err
			}
			if df.Has("/DW") {
				v, err := d.anyWidth(df.m["/DW"])
				if err != nil {
					return nil, err
				}
				widths["default"] = v
			}
			desc := newDict()
			if dd, ok := df.Get("/FontDescriptor"); ok {
				r := d.resolve(dd)
				dsc, ok := asDict(r)
				if !ok {
					if err := nonDictDescriptor(r, false); err != nil {
						return nil, err
					}
					dsc = newDict()
				}
				desc = dsc
			}
			f, isF, err := d.parseFontDescriptor(desc)
			if err != nil {
				return nil, err
			}
			flags, flagsIsFloat = f, isF
		}
	}

	spaceChar := getSpaceChar(enc, cmap)
	if widths["default"] == 0 {
		if flagsIsFloat && len(widths) > 0 {
			if w, ok := widths[spaceChar.key()]; ok && w != 0 {
				return nil, pyErr("TypeError", "flags & int")
			}
		}
		addDefaultWidth(widths, flags, spaceChar)
	}
	if flagsIsFloat {
		if widths[spaceChar.key()] == 0 {
			return nil, pyErr("TypeError", "flags & int")
		}
	}
	spaceWidth := addSpaceWidth(widths, flags, spaceChar)
	return &font{
		name:       name,
		enc:        enc,
		charMap:    cmap,
		widths:     widths,
		spaceWidth: spaceWidth,
		spaceChar:  spaceChar,
		flags:      flags,
	}, nil
}

// parseFontDescriptor returns the /Flags value the FontDescriptor would get.
func (d *Document) parseFontDescriptor(desc *Dict) (int64, bool, error) {
	var flags int64 = 32
	isFloat := false
	if desc.Has("/Flags") {
		switch v := d.resolve(desc.m["/Flags"]).(type) {
		case Int:
			flags = int64(v)
		case Real:
			flags = int64(v)
			isFloat = true
		default:
			isFloat = true // & on a non-int raises TypeError later
		}
	}
	if desc.Has("/FontBBox") {
		if err := d.checkBBox(desc.m["/FontBBox"]); err != nil {
			return 0, false, err
		}
	}
	n := 0
	for _, k := range []Name{"/FontFile", "/FontFile2", "/FontFile3"} {
		if !desc.Has(k) {
			continue
		}
		if n > 0 {
			return 0, false, pyErr("PdfReadError", "More than one /FontFile found")
		}
		// font_descriptor_obj[key].get_object(): a reference to a missing
		// object gives None and None.get_object() raises AttributeError;
		// PdfReadError while resolving is logged and ignored.
		var ff Object
		err := catch(func() error { ff = d.resolve(desc.m[k]); return nil })
		if err != nil {
			if isReadError(err) {
				continue
			}
			return 0, false, err
		}
		if ff == nil {
			return 0, false, pyErr("AttributeError", "'NoneType' object has no attribute 'get_object'")
		}
		n++
	}
	return flags, isFloat, nil
}

// badWidth marks a width that is not a number (pypdf stores the object and
// fails with TypeError only when it does arithmetic or comparisons on it).
var badWidth = math.NaN()

func isBad(v float64) bool { return math.IsNaN(v) }

// rawWidth is a /W list element as stored by pypdf (unresolved).
func rawWidth(o Object) float64 {
	switch v := o.(type) {
	case Int:
		return float64(v)
	case Real:
		return float64(v)
	}
	return badWidth
}

// sequenceWidths returns the elements of a /W entry that pypdf treats as a
// width list (any Sequence), or ok=false.
func sequenceWidths(o Object) ([]float64, bool) {
	switch v := o.(type) {
	case Array:
		out := make([]float64, len(v))
		for i, x := range v {
			out[i] = rawWidth(x)
		}
		return out, true
	case Name:
		out := make([]float64, len([]rune(string(v))))
		for i := range out {
			out[i] = badWidth
		}
		return out, true
	case *String:
		if t, ok := textString(v.Raw); ok {
			out := make([]float64, len(t))
			for i := range out {
				out[i] = badWidth
			}
			return out, true
		}
		out := make([]float64, len(v.Raw))
		for i, b := range v.Raw {
			out[i] = float64(b)
		}
		return out, true
	}
	return nil, false
}

func (d *Document) collectCIDWidths(df *Dict, widths map[string]float64) error {
	var w Array
	if wo, ok := df.Get("/W"); ok {
		switch v := d.resolve(wo).(type) {
		case Array:
			w = v
		case *Dict:
			return nil // iterates the keys: names, each ignored with a warning
		case *Stream:
			return nil
		case Name, *String:
			return pyErr("AttributeError", "str has no get_object")
		default:
			return pyErr("TypeError", "W has no len()")
		}
	}
	skip := 0
	for idx := 0; idx < len(w); idx++ {
		entry := d.resolve(w[idx])
		if skip > 0 {
			skip--
			continue
		}
		start, startIsInt, ok := d.num(entry)
		if !ok {
			continue // logged and ignored
		}
		var next Object
		if idx+1 < len(w) {
			next = d.resolve(w[idx+1])
		}
		if list, isSeq := sequenceWidths(next); isSeq {
			if !startIsInt {
				return pyErr("TypeError", "range() float")
			}
			for i, v := range list {
				c := int64(start) + int64(i)
				if c < 0 || c > 0x10FFFF {
					return pyErr("ValueError", "chr() arg not in range")
				}
				widths[pstr{rune(c)}.key()] = v
			}
			skip = 1
			continue
		}
		if _, _, nextNum := d.num(next); nextNum && idx+2 < len(w) {
			if cw, _, ok := d.num(d.resolve(w[idx+2])); ok {
				stop, stopIsInt, _ := d.num(next)
				if !startIsInt || !stopIsInt {
					return pyErr("TypeError", "range() float")
				}
				for c := int64(start); c < int64(stop)+1; c++ {
					if c < 0 || c > 0x10FFFF {
						return pyErr("ValueError", "chr() arg not in range")
					}
					widths[pstr{rune(c)}.key()] = cw
				}
				skip = 2
				continue
			}
		}
		// "Invalid font width definition" warning
	}
	return nil
}

func getSpaceChar(enc *encoding, cmap *ordMap) pstr {
	space := pstr{' '}
	if !enc.isStr {
		for _, code := range enc.codes {
			if enc.m[code].eq(space) {
				return pstr{rune(code)}
			}
		}
	}
	for _, k := range cmap.keys {
		if cmap.m[k].eq(space) {
			return keyToPstr(k)
		}
	}
	return space
}

func addDefaultWidth(widths map[string]float64, flags int64, spaceChar pstr) {
	if len(widths) == 0 {
		widths["default"] = 500
		return
	}
	if w, ok := widths[spaceChar.key()]; ok && w != 0 {
		if flags&fixedPitch == fixedPitch {
			widths["default"] = w
			return
		}
		if isBad(w) {
			raise(pyErr("TypeError", "int() of a non-number width"))
		}
		widths["default"] = math.Trunc(2 * w)
		return
	}
	sum := 0.0
	n := 0
	for _, w := range widths {
		if isBad(w) {
			raise(pyErr("TypeError", "comparison with a non-number width"))
		}
		if w > 0 {
			sum += w
			n++
		}
	}
	if n == 0 {
		widths["default"] = 500
		return
	}
	widths["default"] = math.Floor(sum / float64(n))
}

func addSpaceWidth(widths map[string]float64, flags int64, spaceChar pstr) float64 {
	if w := widths[spaceChar.key()]; w != 0 {
		return w
	}
	if flags&fixedPitch == fixedPitch {
		return widths["default"]
	}
	if isBad(widths["default"]) {
		raise(pyErr("TypeError", "floor division of a non-number width"))
	}
	return math.Floor(widths["default"] / 2)
}

// anyWidth is cast(int, x.get_object()): any object is stored; None fails.
func (d *Document) anyWidth(o Object) (float64, error) {
	r := d.resolve(o)
	if r == nil {
		return 0, pyErr("AttributeError", "'NoneType' object has no attribute 'get_object'")
	}
	switch v := r.(type) {
	case Int:
		return float64(v), nil
	case Real:
		return float64(v), nil
	}
	return badWidth, nil
}

// removePrefixable checks that x.removeprefix("/") works (str objects).
func removePrefixable(o Object) (string, error) {
	switch v := o.(type) {
	case Name:
		return string(v), nil
	case *String:
		if t, ok := textString(v.Raw); ok {
			return t.goString(), nil
		}
		return "", pyErr("TypeError", "a bytes-like object is required")
	}
	return "", pyErr("AttributeError", "object has no attribute 'removeprefix'")
}

// pyIntObj is int(obj) for a pypdf object (resolving references).
func (d *Document) pyIntObj(o Object) (float64, error) {
	switch v := d.resolve(o).(type) {
	case Int:
		return float64(v), nil
	case Real:
		return math.Trunc(float64(v)), nil
	case Name:
		return 0, pyErr("ValueError", "invalid literal for int() with base 10: %q", string(v))
	case *String:
		raw := v.Raw
		if t, ok := textString(raw); ok {
			raw = []byte(t.goString())
		}
		n, err := pyIntLoose(raw)
		if err != nil {
			return 0, err
		}
		return float64(n), nil
	}
	return 0, pyErr("TypeError", "int() argument must be a string or a real number")
}

// pyIter is iter(obj) for a resolved pypdf object: arrays give their items,
// dictionaries their keys, text strings and names their characters (as
// one-character text strings), byte strings their byte values.
func pyIter(o Object) ([]Object, error) {
	switch v := o.(type) {
	case Array:
		return v, nil
	case *Dict:
		out := make([]Object, 0, v.Len())
		for _, k := range v.Keys() {
			out = append(out, k)
		}
		return out, nil
	case *Stream:
		return pyIter(v.Dict)
	case Name:
		var out []Object
		for _, r := range string(v) {
			out = append(out, &String{Raw: []byte(string(r))})
		}
		return out, nil
	case *String:
		if t, ok := textString(v.Raw); ok {
			var out []Object
			for _, r := range t {
				out = append(out, &String{Raw: []byte(string(r))})
			}
			return out, nil
		}
		out := make([]Object, len(v.Raw))
		for i, b := range v.Raw {
			out[i] = Int(b)
		}
		return out, nil
	case nil:
		return nil, pyErr("AttributeError", "'NoneType' object has no attribute '__iter__'")
	}
	return nil, pyErr("TypeError", "object is not iterable")
}

// pyIn is `key in obj` for a resolved object that is not a dictionary.
func pyIn(o Object, key Name) (bool, error) {
	switch v := o.(type) {
	case Array:
		for _, x := range v {
			switch e := x.(type) {
			case Name:
				if e == key {
					return true, nil
				}
			case *String:
				if t, ok := textString(e.Raw); ok && t.goString() == string(key) {
					return true, nil
				}
			}
		}
		return false, nil
	case Name:
		return strings.Contains(string(v), string(key)), nil
	case *String:
		if t, ok := textString(v.Raw); ok {
			return strings.Contains(t.goString(), string(key)), nil
		}
	}
	return false, pyErr("TypeError", "argument is not iterable")
}

// nonDictDescriptor mimics _parse_font_descriptor (and the /MissingWidth
// test) on an object that is not a dictionary: `in` works on lists and
// strings, but any hit is then subscripted, which raises TypeError.
func nonDictDescriptor(o Object, missingWidth bool) error {
	keys := []Name{"/FontName", "/FontFamily", "/FontWeight", "/Ascent", "/Descent", "/CapHeight",
		"/XHeight", "/ItalicAngle", "/Flags", "/FontBBox", "/FontFile", "/FontFile2", "/FontFile3"}
	if missingWidth {
		keys = append([]Name{"/MissingWidth"}, keys...)
	}
	for _, k := range keys {
		in, err := pyIn(o, k)
		if err != nil {
			return err
		}
		if in {
			return pyErr("TypeError", "indices must be integers")
		}
	}
	return nil
}

// checkBBox is tuple(map(float, bbox)) followed by the length assertion.
func (d *Document) checkBBox(o Object) error {
	items, err := pyIter(d.resolve(o))
	if err != nil {
		if errClass(err) == "AttributeError" {
			err = pyErr("TypeError", "'NoneType' object is not iterable")
		}
		return err
	}
	for _, x := range items {
		if _, err := pyFloat(d.resolve(x)); err != nil {
			return err
		}
	}
	if len(items) != 4 {
		return pyErr("AssertionError", "bbox")
	}
	return nil
}
