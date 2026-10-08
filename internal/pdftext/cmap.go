package pdftext

import (
	"bytes"
	"math/big"
	"strings"
)

// Port of pypdf/_cmap.py.

var predefinedCMap = map[string]string{
	"/Identity-H":     "utf-16-be",
	"/Identity-V":     "utf-16-be",
	"/GB-EUC-H":       "gbk",
	"/GB-EUC-V":       "gbk",
	"/GBpc-EUC-H":     "gb2312",
	"/GBpc-EUC-V":     "gb2312",
	"/GBK-EUC-H":      "gbk",
	"/GBK-EUC-V":      "gbk",
	"/GBK2K-H":        "gb18030",
	"/GBK2K-V":        "gb18030",
	"/ETen-B5-H":      "cp950",
	"/ETen-B5-V":      "cp950",
	"/ETenms-B5-H":    "cp950",
	"/ETenms-B5-V":    "cp950",
	"/UniCNS-UTF16-H": "utf-16-be",
	"/UniCNS-UTF16-V": "utf-16-be",
	"/UniGB-UTF16-H":  "gb18030",
	"/UniGB-UTF16-V":  "gb18030",
	"/90ms-RKSJ-H":    "cp932",
	"/90ms-RKSJ-V":    "cp932",
	"/UniJIS-UTF16-H": "utf-16-be",
	"/UniJIS-UTF16-V": "utf-16-be",
}

// encoding is either a codec name (isStr) or an ordered code->text map.
type encoding struct {
	isStr bool
	name  string
	codes []int // insertion order
	m     map[int]pstr
}

func (e *encoding) set(code int, v pstr) {
	if _, ok := e.m[code]; !ok {
		e.codes = append(e.codes, code)
	}
	e.m[code] = v
}

func encodingFromList(lst []pstr) *encoding {
	e := &encoding{m: map[int]pstr{}}
	for i, v := range lst {
		e.set(i, v)
	}
	return e
}

// ordMap is a Python dict from str to str, in insertion order.
type ordMap struct {
	keys []string
	m    map[string]pstr
}

func newOrdMap() *ordMap { return &ordMap{m: map[string]pstr{}} }

func (o *ordMap) set(k pstr, v pstr) {
	kk := k.key()
	if _, ok := o.m[kk]; !ok {
		o.keys = append(o.keys, kk)
	}
	o.m[kk] = v
}

func (o *ordMap) get(k pstr) (pstr, bool) {
	v, ok := o.m[k.key()]
	return v, ok
}

// getEncoding is get_encoding(ft).
func (d *Document) getEncoding(ft *Dict) (*encoding, *ordMap, error) {
	enc, err := d.parseEncoding(ft)
	if err != nil {
		return nil, nil, err
	}
	cmap, intEntry, err := d.parseToUnicode(ft)
	if err != nil {
		return nil, nil, err
	}
	if !enc.isStr {
		for _, x := range intEntry {
			if x <= 255 {
				if x < 0 {
					return nil, nil, pyErr("ValueError", "chr() arg not in range")
				}
				enc.set(x, pstr{rune(x)})
			}
		}
	}
	return enc, cmap, nil
}

func (d *Document) parseEncoding(ft *Dict) (*encoding, error) {
	loadTables()
	if !ft.Has("/Encoding") {
		if bf, ok := ft.Get("/BaseFont"); ok {
			if n, ok := d.resolve(bf).(Name); ok {
				// ft["/BaseFont"] in charset_encoding (resolved by __getitem__)
				if lst, ok := charsets[string(n)]; ok {
					return encodingFromList(lst), nil
				}
			}
		}
		return &encoding{isStr: true, name: "charmap"}, nil
	}
	encObj := d.resolve(ft.m["/Encoding"])
	var list []pstr // the list form while Differences apply
	var result *encoding
	switch e := encObj.(type) {
	case Name:
		if lst, ok := charsets[string(e)]; ok {
			list = append([]pstr(nil), lst...)
		} else if c, ok := predefinedCMap[string(e)]; ok {
			return &encoding{isStr: true, name: c}, nil
		} else if strings.Contains(string(e), "-UCS2-") {
			return &encoding{isStr: true, name: "utf-16-be"}, nil
		} else {
			return &encoding{isStr: true, name: string(e)}, nil
		}
	case *String:
		// TextStringObject is a str too; it is compared the same way.
		s := pyStr(e).goString()
		if lst, ok := charsets[s]; ok {
			list = append([]pstr(nil), lst...)
		} else if c, ok := predefinedCMap[s]; ok {
			return &encoding{isStr: true, name: c}, nil
		} else if strings.Contains(s, "-UCS2-") {
			return &encoding{isStr: true, name: "utf-16-be"}, nil
		} else {
			return &encoding{isStr: true, name: s}, nil
		}
	case *Dict:
		if be, ok := e.Get("/BaseEncoding"); ok {
			if n, ok := d.resolve(be).(Name); ok && charsets[string(n)] != nil {
				list = append([]pstr(nil), charsets[string(n)]...)
			} else {
				list = append([]pstr(nil), charsets["/StandardEncoding"]...)
			}
		} else {
			list = append([]pstr(nil), charsets["/StandardEncoding"]...)
		}
		if diffs, ok := e.Get("/Differences"); ok {
			arr, err := pyIter(d.resolve(diffs))
			if err != nil {
				return nil, err
			}
			x := 0
			for _, o := range arr {
				if v, ok := o.(Int); ok {
					x = int(v)
					continue
				}
				if x < len(list) {
					idx := x
					if idx < 0 {
						idx += len(list)
					}
					val := badEncoding // a non-str object: "".join() fails later
					var str string
					isStr := false
					switch v := o.(type) {
					case Name:
						str, isStr = string(v), true
					case *String:
						if t, ok := textString(v.Raw); ok {
							str, isStr = t.goString(), true
						}
					}
					if isStr {
						if gl, found := adobeGlyphs[str]; found {
							val = gl
						} else {
							val = pstr([]rune(str))
						}
					}
					if idx < 0 {
						return nil, pyErr("IndexError", "list assignment index out of range")
					}
					list[idx] = val
				}
				x++
			}
		}
	default:
		list = append([]pstr(nil), charsets["/StandardEncoding"]...)
	}
	result = encodingFromList(list)
	return result, nil
}

// badEncoding stands for a non-str object stored in an encoding table.
var badEncoding = pstr{-1}

// parseToUnicode is _parse_to_unicode(ft).
func (d *Document) parseToUnicode(ft *Dict) (*ordMap, []int, error) {
	m := newOrdMap()
	var intEntry []int
	if !ft.Has("/ToUnicode") {
		if st, ok := ft.Get("/Subtype"); ok && st == Name("/Type1") {
			return d.type1Alternative(ft, m, intEntry)
		}
		return newOrdMap(), nil, nil
	}
	cm, err := d.prepareCM(ft)
	if err != nil {
		return nil, nil, err
	}
	st := &cmState{m: m}
	for _, line := range bytes.Split(cm, []byte("\n")) {
		if err := st.processLine(bytes.Trim(line, " \t")); err != nil {
			return nil, nil, err
		}
	}
	return m, st.intEntry, nil
}

func (d *Document) prepareCM(ft *Dict) ([]byte, error) {
	tu := d.resolve(ft.m["/ToUnicode"])
	var cm []byte
	if s, ok := tu.(*Stream); ok {
		data, err := d.streamData(s)
		if err != nil {
			return nil, err
		}
		cm = data
	} else {
		cm = []byte("beginbfrange\n<0000> <0001> <0000>\nendbfrange")
	}
	cm = bytes.Trim(cm, " \t\n\r\x0b\x0c")
	cm = bytes.ReplaceAll(cm, []byte("beginbfchar"), []byte("\nbeginbfchar\n"))
	cm = bytes.ReplaceAll(cm, []byte("endbfchar"), []byte("\nendbfchar\n"))
	cm = bytes.ReplaceAll(cm, []byte("beginbfrange"), []byte("\nbeginbfrange\n"))
	cm = bytes.ReplaceAll(cm, []byte("endbfrange"), []byte("\nendbfrange\n"))
	cm = bytes.ReplaceAll(cm, []byte("<<"), []byte("\n{\n"))
	cm = bytes.ReplaceAll(cm, []byte(">>"), []byte("\n}\n"))
	ll := bytes.Split(cm, []byte("<"))
	for i := range ll {
		j := bytes.IndexByte(ll[i], '>')
		if j >= 0 {
			var content []byte
			if j == 0 {
				content = []byte(".")
			} else {
				content = bytes.ReplaceAll(ll[i][:j], []byte(" "), nil)
			}
			n := append(append(append([]byte{}, content...), ' '), ll[i][j+1:]...)
			ll[i] = n
		}
	}
	cm = bytes.Join(ll, []byte(" "))
	cm = bytes.ReplaceAll(cm, []byte("["), []byte(" [ "))
	cm = bytes.ReplaceAll(cm, []byte("]"), []byte(" ]\n "))
	cm = bytes.ReplaceAll(cm, []byte("\r"), []byte("\n"))
	return cm, nil
}

type cmState struct {
	processRg, processChar bool
	multiline              bool
	mlA                    int64
	m                      *ordMap
	intEntry               []int
	width                  int  // map_dict[-1]
	hasWidth               bool // -1 in map_dict
}

const mappingLimit = 100000

func (s *cmState) processLine(line []byte) error {
	if len(line) == 0 || line[0] == '%' {
		return nil
	}
	line = bytes.ReplaceAll(line, []byte("\t"), []byte(" "))
	switch {
	case bytes.Contains(line, []byte("beginbfrange")):
		s.processRg = true
	case bytes.Contains(line, []byte("endbfrange")):
		s.processRg = false
	case bytes.Contains(line, []byte("beginbfchar")):
		s.processChar = true
	case bytes.Contains(line, []byte("endbfchar")):
		s.processChar = false
	case s.processRg:
		err := s.parseBfrange(line)
		if err != nil && errClass(err) != "ValueError" && errClass(err) != "IndexError" {
			return err
		}
	case s.processChar:
		err := s.parseBfchar(line)
		if err != nil && errClass(err) != "ValueError" && errClass(err) != "IndexError" {
			return err
		}
	}
	return nil
}

func fields(line []byte) [][]byte {
	var out [][]byte
	for _, f := range bytes.Split(line, []byte(" ")) {
		if len(f) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// pyBigHex is int(b, 16) with Python's unbounded integers.
func pyBigHex(b []byte) (*big.Int, error) {
	s := strings.TrimSpace(string(b))
	neg := false
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	s = strings.ReplaceAll(s, "_", "")
	v, ok := new(big.Int).SetString(s, 16)
	if s == "" || !ok {
		return nil, pyErr("ValueError", "invalid literal for int() with base 16")
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

// pyIntHex is int(b, 16).
func pyIntHex(b []byte) (int64, error) {
	s := strings.TrimSpace(string(b))
	neg := false
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		neg = s[0] == '-'
		s = s[1:]
	}
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	s = strings.ReplaceAll(s, "_", "")
	if s == "" {
		return 0, pyErr("ValueError", "invalid literal for int() with base 16")
	}
	v, ok := new(big.Int).SetString(s, 16)
	if !ok || !v.IsInt64() {
		return 0, pyErr("ValueError", "invalid literal for int() with base 16")
	}
	r := v.Int64()
	if neg {
		r = -r
	}
	return r, nil
}

// unhexlify is binascii.unhexlify.
func unhexlify(b []byte) ([]byte, error) {
	if len(b)%2 != 0 {
		return nil, pyErr("ValueError", "Odd-length string")
	}
	out := make([]byte, len(b)/2)
	for i := 0; i < len(out); i++ {
		h, l := hexVal(b[2*i]), hexVal(b[2*i+1])
		if h < 0 || l < 0 {
			return nil, pyErr("ValueError", "Non-hexadecimal digit found")
		}
		out[i] = byte(h<<4 | l)
	}
	return out, nil
}

// fmtHex is b"%0*X" % v.
func fmtHex(v int64, width int) []byte {
	neg := v < 0
	if neg {
		v = -v
	}
	s := strings.ToUpper(big.NewInt(v).Text(16))
	pad := width
	if neg {
		pad--
	}
	for len(s) < pad {
		s = "0" + s
	}
	if neg {
		s = "-" + s
	}
	return []byte(s)
}

func (s *cmState) decodeKey(b []byte) (pstr, error) {
	if s.width == 1 {
		return decodeLatin1(b), nil
	}
	k, ok := decodeUTF16BEPass(b)
	if !ok {
		return nil, pyErr("ValueError", "truncated data")
	}
	return k, nil
}

func decodeValue(hex []byte) (pstr, error) {
	raw, err := unhexlify(hex)
	if err != nil {
		return nil, err
	}
	v, ok := decodeUTF16BEPass(raw)
	if !ok {
		return nil, pyErr("ValueError", "truncated data")
	}
	return v, nil
}

func (s *cmState) addEntry(a int64, value pstr) error {
	raw, err := unhexlify(fmtHex(a, s.width*2))
	if err != nil {
		return err
	}
	k, err := s.decodeKey(raw)
	if err != nil {
		return err
	}
	s.m.set(k, value)
	s.intEntry = append(s.intEntry, int(a))
	return nil
}

func (s *cmState) parseBfrange(line []byte) error {
	lst := fields(line)
	closure := false
	entryCount := len(s.intEntry)
	if entryCount > mappingLimit {
		return pyErr("LimitReachedError", "Maximum /ToUnicode size limit reached")
	}
	if s.multiline {
		if !s.hasWidth {
			return pyErr("KeyError", "-1")
		}
		a := s.mlA
		for _, sq := range lst {
			if string(sq) == "]" {
				closure = true
				break
			}
			entryCount++
			if entryCount > mappingLimit {
				return pyErr("LimitReachedError", "Maximum /ToUnicode size limit reached")
			}
			if _, err := decodeValueAfterKey(s, a, sq); err != nil {
				return err // multiline_rg keeps its previous value
			}
			a++
		}
		if closure {
			s.multiline = false
		} else {
			s.mlA = a
		}
		return nil
	}
	if len(lst) < 2 {
		return pyErr("IndexError", "list index out of range")
	}
	bigA, err := pyBigHex(lst[0])
	if err != nil {
		return err
	}
	bigB, err := pyBigHex(lst[1])
	if err != nil {
		return err
	}
	// Python ints are unbounded; codes beyond int64 are only accepted here
	// as far as the range check (see the package doc).
	a, b := bigA.Int64(), bigB.Int64()
	fits := bigA.IsInt64() && bigB.IsInt64()
	nbi := len(lst[0])
	if len(lst[1]) > nbi {
		nbi = len(lst[1])
	}
	s.width = (nbi + 1) / 2
	s.hasWidth = true
	if len(lst) < 3 {
		return pyErr("IndexError", "list index out of range")
	}
	if string(lst[2]) == "[" {
		if !fits {
			return pyErr("ValueError", "code out of the supported range")
		}
		for _, sq := range lst[3:] {
			if string(sq) == "]" {
				closure = true
				break
			}
			entryCount++
			if entryCount > mappingLimit {
				return pyErr("LimitReachedError", "Maximum /ToUnicode size limit reached")
			}
			if _, err := decodeValueAfterKey(s, a, sq); err != nil {
				return err
			}
			a++
		}
	} else {
		c, err := pyIntHex(lst[2])
		if err != nil {
			return err
		}
		w2 := len(lst[2])
		if w2 < 4 {
			w2 = 4
		}
		closure = true
		// range_size = max(0, b - a + 1), computed without overflow
		rangeSize := new(big.Int).Sub(bigB, bigA)
		rangeSize.Add(rangeSize, big.NewInt(1))
		if rangeSize.Sign() < 0 {
			rangeSize.SetInt64(0)
		}
		rangeSize.Add(rangeSize, big.NewInt(int64(entryCount)))
		if rangeSize.Cmp(big.NewInt(mappingLimit)) > 0 {
			return pyErr("LimitReachedError", "Maximum /ToUnicode size limit reached")
		}
		if !fits {
			return pyErr("ValueError", "code out of the supported range")
		}
		for a <= b {
			v, err := decodeValue(fmtHex(c, w2))
			if err != nil {
				return err
			}
			if err := s.addEntry(a, v); err != nil {
				return err
			}
			a++
			c++
		}
	}
	if !closure {
		s.multiline = true
		s.mlA = a
	}
	return nil
}

// decodeValueAfterKey adds one list-form bfrange entry: key from a, value
// from the hex token sq (the key expression is evaluated after the value in
// pypdf's dict assignment, so value errors win).
func decodeValueAfterKey(s *cmState, a int64, sq []byte) (pstr, error) {
	v, err := decodeValue(sq)
	if err != nil {
		return nil, err
	}
	if err := s.addEntry(a, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *cmState) parseBfchar(line []byte) error {
	lst := fields(line)
	if len(s.intEntry)+len(lst)/2 > mappingLimit {
		return pyErr("LimitReachedError", "Maximum /ToUnicode size limit reached")
	}
	if len(lst) == 0 {
		return pyErr("IndexError", "list index out of range")
	}
	s.width = len(lst[0]) / 2
	s.hasWidth = true
	for len(lst) > 1 {
		mapTo := pstr{}
		if string(lst[1]) != "." {
			raw, err := unhexlify(lst[1])
			if err == nil {
				if len(lst[1]) < 4 {
					mapTo = decodeLatin1(raw)
				} else {
					v, ok := decodeUTF16BEPass(raw)
					if !ok {
						return pyErr("ValueError", "truncated data")
					}
					mapTo = v
				}
			}
			// binascii errors are logged and leave map_to empty
		}
		rawKey, err := unhexlify(lst[0])
		if err != nil {
			return err
		}
		k, err := s.decodeKey(rawKey)
		if err != nil {
			return err
		}
		s.m.set(k, mapTo)
		code, err := pyIntHex(lst[0])
		if err != nil {
			return err
		}
		s.intEntry = append(s.intEntry, int(code))
		lst = lst[2:]
	}
	return nil
}

// type1Alternative reads the /Encoding of an embedded Type 1 font program.
func (d *Document) type1Alternative(ft *Dict, m *ordMap, intEntry []int) (*ordMap, []int, error) {
	if !ft.Has("/FontDescriptor") {
		return m, intEntry, nil
	}
	fd, ok := asDict(d.resolve(ft.m["/FontDescriptor"]))
	if !ok {
		return nil, nil, pyErr("AttributeError", "no get")
	}
	ff, ok := fd.Get("/FontFile")
	if !ok || isNullOrNone(d, ff) {
		return m, intEntry, nil
	}
	st, ok := d.resolve(ff).(*Stream)
	if !ok {
		return nil, nil, pyErr("AttributeError", "no get_data")
	}
	txt, err := d.streamData(st)
	if err != nil {
		return nil, nil, err
	}
	if i := bytes.Index(txt, []byte("eexec\n")); i >= 0 {
		txt = txt[:i]
	}
	parts := bytes.Split(txt, []byte("/Encoding"))
	if len(parts) < 2 {
		return m, intEntry, nil
	}
	txt = parts[1]
	txt = bytes.ReplaceAll(txt, []byte("\r"), []byte("\n"))
	for _, li := range bytes.Split(txt, []byte("\n")) {
		if !bytes.HasPrefix(li, []byte("dup")) {
			continue
		}
		var words [][]byte
		for _, w := range bytes.Split(li, []byte(" ")) {
			if len(w) > 0 {
				words = append(words, w)
			}
		}
		if len(words) < 3 || (len(words) > 3 && string(words[3]) != "put") {
			continue
		}
		iv, err := pyIntLoose(words[1])
		if err != nil {
			continue
		}
		var v pstr
		if !utf8Valid(words[2]) {
			return nil, nil, pyErr("UnicodeDecodeError", "utf-8")
		}
		if g, ok := adobeGlyphs[string(words[2])]; ok {
			v = g
		} else if bytes.HasPrefix(words[2], []byte("/uni")) {
			cp, err := pyIntHex(words[2][4:])
			if err != nil || cp < 0 || cp > 0x10FFFF {
				continue
			}
			v = pstr{rune(cp)}
		} else {
			continue
		}
		if iv < 0 || iv > 0x10FFFF {
			return nil, nil, pyErr("ValueError", "chr() arg not in range")
		}
		m.set(pstr{rune(iv)}, v)
		intEntry = append(intEntry, int(iv))
	}
	return m, intEntry, nil
}
