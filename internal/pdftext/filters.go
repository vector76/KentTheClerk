package pdftext

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
)

// Port of pypdf/filters.py decode_stream_data for the filters that matter
// for text (images are never decoded by text extraction).

// streamData is StreamObject.get_data(): the decoded bytes, cached.
func (d *Document) streamData(s *Stream) ([]byte, error) {
	if s.done {
		return s.decoded, s.decErr
	}
	s.decoded, s.decErr = d.decodeStream(s)
	s.done = true
	return s.decoded, s.decErr
}

func (d *Document) decodeStream(s *Stream) ([]byte, error) {
	fo, ok := s.Dict.Get("/Filter")
	if !ok {
		return s.Raw, nil
	}
	var filters []Object
	if r, isRef := fo.(Ref); isRef {
		fo = d.resolve(r)
	}
	if arr, isArr := fo.(Array); isArr {
		filters = arr
	} else {
		filters = []Object{fo}
	}
	var parms []Object
	if po, ok := s.Dict.Get("/DecodeParms"); ok {
		if arr, isArr := po.(Array); isArr {
			parms = arr
		} else {
			parms = []Object{po}
		}
	} else {
		parms = make([]Object, len(filters))
		for i := range parms {
			parms[i] = newDict()
		}
	}
	data := s.Raw
	if len(data) == 0 {
		return data, nil
	}
	n := len(filters)
	if len(parms) < n {
		n = len(parms)
	}
	for i := 0; i < n; i++ {
		f := filters[i]
		params := parms[i]
		if _, isNull := params.(Null); isNull {
			params = newDict()
		}
		name, _ := f.(Name)
		var err error
		switch name {
		case "/ASCIIHexDecode", "/AHx":
			data, err = asciiHexDecode(data)
		case "/ASCII85Decode", "/A85":
			data, err = ascii85Decode(data)
		case "/LZWDecode", "/LZW":
			data, err = lzwDecode(data)
		case "/FlateDecode", "/Fl":
			data, err = d.flateDecode(data, params)
		case "/RunLengthDecode", "/RL":
			data, err = runLengthDecode(data)
		case "/Crypt":
			if pd, ok := asDict(params); ok && (pd.Has("/Name") || pd.Has("/Type")) {
				err = pyErr("NotImplementedError", "/Crypt filter with /Name or /Type not supported yet")
			}
		case "/CCITTFaxDecode", "/CCF", "/DCTDecode", "/DCT", "/JPXDecode", "/JBIG2Decode":
			// Image codecs: pypdf decodes them (or needs Pillow); text
			// extraction never needs the result.
			err = pyErr("NotImplementedError", "image filter %s not supported here", name)
		default:
			err = pyErr("NotImplementedError", "Unsupported filter %v", f)
		}
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

// pypdf's output limits (pypdf/filters.py, generic/_data_structures.py).
const (
	maxDecodedOutput        = 75_000_000 // ZLIB/LZW/RUN_LENGTH_MAX_OUTPUT_LENGTH
	maxZlibRecoveryInput    = 5_000_000  // ZLIB_MAX_RECOVERY_INPUT_LENGTH
	maxFlateColumns         = 250_000    // FLATE_MAX_COLUMNS
	maxFlateRowLength       = 4_000_000  // FLATE_MAX_ROW_LENGTH
	maxContentArrayLength   = 10_000     // CONTENT_STREAM_ARRAY_MAX_LENGTH
	maxArrayStreamOutput    = 75_000_000 // MAX_ARRAY_BASED_STREAM_OUTPUT_LENGTH
	maxDeclaredStreamLength = 75_000_000 // MAX_DECLARED_STREAM_LENGTH
)

// readLimited reads r, failing like pypdf once more than maxDecodedOutput
// bytes come out. corrupt reports a decoding error that is not a plain
// truncation or a checksum mismatch.
func readLimited(r io.Reader) (out []byte, corrupt bool, err error) {
	out, rerr := io.ReadAll(io.LimitReader(r, maxDecodedOutput+1))
	if len(out) > maxDecodedOutput {
		return nil, false, pyErr("LimitReachedError", "Limit reached while decompressing.")
	}
	if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != zlib.ErrChecksum && rerr != gzip.ErrChecksum {
		corrupt = true
	}
	return out, corrupt, nil
}

// zlibDecompress mimics pypdf's decompress(): a zlib stream, tolerant of
// truncation and trailing garbage; on corrupt data it keeps what decoded.
func zlibDecompress(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err == nil {
		out, corrupt, err := readLimited(r)
		if err != nil {
			return nil, err
		}
		// pypdf's byte-by-byte recovery keeps failing after the first
		// error and gives up once the input index passes 5,000,000.
		if corrupt && len(data) > maxZlibRecoveryInput+1 {
			return nil, pyErr("LimitReachedError", "Recovery limit reached while decompressing.")
		}
		return out, nil
	}
	// No zlib header: pypdf's last resort feeds the bytes to a decompressor
	// that also accepts a gzip header (wbits | 32); after the first error
	// zlib produces nothing more, so a bad header yields no data.
	if len(data) >= 10 && data[0] == 0x1f && data[1] == 0x8b {
		if gr, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
			out, corrupt, err := readLimited(gr)
			if err != nil {
				return nil, err
			}
			if corrupt && len(data) > maxZlibRecoveryInput+1 {
				return nil, pyErr("LimitReachedError", "Recovery limit reached while decompressing.")
			}
			return out, nil
		}
	}
	if len(data) > maxZlibRecoveryInput+1 {
		return nil, pyErr("LimitReachedError", "Recovery limit reached while decompressing.")
	}
	return []byte{}, nil
}

func (d *Document) flateDecode(data []byte, params Object) ([]byte, error) {
	out, err := zlibDecompress(data)
	if err != nil {
		return nil, err
	}
	pd, ok := asDict(params)
	if !ok {
		return out, nil
	}
	pred := Object(Int(1))
	if p, ok := pd.Get("/Predictor"); ok {
		pred = p
	}
	if pi, ok := pred.(Int); ok && pi == 1 {
		return out, nil
	}
	if pr, ok := pred.(Real); ok && pr == 1 {
		return out, nil
	}
	get := func(key Name, def int64) (int64, error) {
		v := Object(Int(def))
		if o, ok := pd.Get(key); ok {
			v = d.resolve(o)
		}
		iv, ok := v.(Int)
		if !ok || iv < 1 {
			return 0, pyErr("PdfReadError", "Expected positive number for %s", key)
		}
		return int64(iv), nil
	}
	columns, err := get("/Columns", 1)
	if err != nil {
		return nil, err
	}
	if columns > maxFlateColumns {
		return nil, pyErr("LimitReachedError", "Number of columns %d exceeds defined limit of %d.", columns, maxFlateColumns)
	}
	colors, err := get("/Colors", 1)
	if err != nil {
		return nil, err
	}
	if colors > 16 {
		return nil, pyErr("LimitReachedError", "Color value %d exceeds limit of 16.", colors)
	}
	bpc, err := get("/BitsPerComponent", 8)
	if err != nil {
		return nil, err
	}
	if bpc > 16 {
		return nil, pyErr("PdfReadError", "More than 16 bits per component are not allowed: %d", bpc)
	}
	rowLength := int((columns*colors*bpc+7)/8) + 1
	if rowLength > maxFlateRowLength {
		return nil, pyErr("LimitReachedError", "Row length of %d exceeds defined limit of %d.", rowLength, maxFlateRowLength)
	}
	predictor, _ := pred.(Int)
	switch {
	case predictor == 2:
		rowLength--
		bpp := rowLength / int(columns)
		buf := append([]byte(nil), out...)
		for i := range buf {
			if i%rowLength >= bpp {
				buf[i] = buf[i] + buf[i-bpp]
			}
		}
		return buf, nil
	case predictor >= 10 && predictor <= 15:
		return pngPredict(out, int(columns), rowLength)
	}
	return nil, pyErr("PdfReadError", "Unsupported flatedecode predictor %v", pred)
}

func pngPredict(data []byte, columns, rowLength int) ([]byte, error) {
	if rem := len(data) % rowLength; rem != 0 {
		data = append(append([]byte(nil), data...), make([]byte, rowLength-rem)...)
	}
	var out []byte
	prev := make([]byte, rowLength)
	bpp := (rowLength - 1) / columns
	for row := 0; row < len(data); row += rowLength {
		r := append([]byte(nil), data[row:row+rowLength]...)
		switch r[0] {
		case 0:
		case 1:
			for i := bpp + 1; i < rowLength; i++ {
				r[i] += r[i-bpp]
			}
		case 2:
			for i := 1; i < rowLength; i++ {
				r[i] += prev[i]
			}
		case 3:
			for i := 1; i < bpp+1 && i < rowLength; i++ {
				r[i] += prev[i] / 2
			}
			for i := bpp + 1; i < rowLength; i++ {
				r[i] += byte((int(r[i-bpp]) + int(prev[i])) / 2)
			}
		case 4:
			for i := 1; i < bpp+1 && i < rowLength; i++ {
				r[i] += prev[i]
			}
			for i := bpp + 1; i < rowLength; i++ {
				left, up, ul := int(r[i-bpp]), int(prev[i]), int(prev[i-bpp])
				p := left + up - ul
				dl, du, dul := abs(p-left), abs(p-up), abs(p-ul)
				var pa int
				if dl <= du && dl <= dul {
					pa = left
				} else if du <= dul {
					pa = up
				} else {
					pa = ul
				}
				r[i] += byte(pa)
			}
		default:
			return nil, pyErr("PdfReadError", "Unsupported PNG filter %d", r[0])
		}
		prev = r
		out = append(out, r[1:]...)
	}
	return out, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func asciiHexDecode(data []byte) ([]byte, error) {
	if i := bytes.IndexByte(data, '>'); i >= 0 {
		data = data[:i]
	}
	var h []byte
	for _, c := range data {
		if !isPySpace(c) {
			h = append(h, c)
		}
	}
	if len(h)%2 == 1 {
		h = append(h, '0')
	}
	out, err := unhexlify(h)
	if err != nil {
		return nil, pyErr("PdfStreamError", "Invalid hexadecimal character in ASCIIHexDecode stream")
	}
	return out, nil
}

func ascii85Decode(data []byte) ([]byte, error) {
	data = bytes.Trim(data, whitespaceChars)
	if len(data) > 2 && data[len(data)-1] == '>' {
		data = append(bytes.TrimRight(data[:len(data)-1], whitespaceChars), '>')
	}
	adobe := true
	if !(bytes.HasSuffix(data, []byte("~>"))) {
		adobe = false // pypdf retries with adobe=False after a warning
	}
	if adobe {
		data = data[:len(data)-2]
		if bytes.HasPrefix(data, []byte("<~")) {
			data = data[2:]
		}
	}
	var out []byte
	var group [5]byte
	n := 0
	for _, c := range data {
		if bytes.IndexByte([]byte(whitespaceChars), c) >= 0 {
			continue
		}
		if c == 'z' && n == 0 {
			out = append(out, 0, 0, 0, 0)
			continue
		}
		if c < '!' || c > 'u' {
			return nil, pyErr("ValueError", "Non-Ascii85 digit found: %c", c)
		}
		group[n] = c - '!'
		n++
		if n == 5 {
			var v uint64
			for _, g := range group {
				v = v*85 + uint64(g)
			}
			if v > 0xFFFFFFFF {
				return nil, pyErr("ValueError", "Ascii85 overflow")
			}
			out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
			n = 0
		}
	}
	if n > 0 {
		for i := n; i < 5; i++ {
			group[i] = 'u' - '!'
		}
		var v uint64
		for _, g := range group {
			v = v*85 + uint64(g)
		}
		b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		out = append(out, b[:n-1]...)
	}
	return out, nil
}

func runLengthDecode(data []byte) ([]byte, error) {
	var out []byte
	i := 0
	total := 0
	for i < len(data) {
		l := int(data[i])
		i++
		if l == 128 {
			break
		}
		if l < 128 {
			e := i + l + 1
			if e > len(data) {
				e = len(data)
			}
			out = append(out, data[i:e]...)
			i += l + 1
			total += l + 1
		} else {
			if i >= len(data) {
				break
			}
			for k := 0; k < 257-l; k++ {
				out = append(out, data[i])
			}
			i++
			total += 257 - l
		}
		if total > maxDecodedOutput {
			return nil, pyErr("LimitReachedError", "Limit reached while decompressing.")
		}
	}
	return out, nil
}

// lzwDecode ports pypdf's _LzwCodec.decode (early change, 9-12 bits).
func lzwDecode(data []byte) ([]byte, error) {
	const clear, eod = 256, 257
	const maxCode = 4095
	table := make([][]byte, maxCode+1)
	var tableIndex, bits int
	init := func() {
		for i := 0; i < 256; i++ {
			table[i] = []byte{byte(i)}
		}
		for i := 256; i <= maxCode; i++ {
			table[i] = nil
		}
		tableIndex = eod + 1
		bits = 9
	}
	andTable := []int{511, 1023, 2047, 4095}
	bytePtr, nextData, nextBits := 0, 0, 0
	next := func() int {
		for nextBits < bits {
			if bytePtr >= len(data) {
				return eod
			}
			nextData = nextData<<8 | int(data[bytePtr])
			bytePtr++
			nextBits += 8
		}
		code := (nextData >> (nextBits - bits)) & andTable[bits-9]
		nextBits -= bits
		nextData &= 0xFFFFF
		return code
	}
	add := func(old []byte, c byte) {
		if tableIndex > maxCode {
			return
		}
		table[tableIndex] = append(append([]byte(nil), old...), c)
		tableIndex++
		switch tableIndex {
		case 511:
			bits = 10
		case 1023:
			bits = 11
		case 2047:
			bits = 12
		}
	}
	init()
	var out []byte
	old := clear
	for {
		code := next()
		if code == eod {
			break
		}
		if code == clear {
			init()
			code = next()
			if code == eod {
				break
			}
			out = append(out, table[code]...)
			old = code
			if len(out) > maxDecodedOutput {
				return nil, pyErr("LimitReachedError", "Limit reached while decompressing.")
			}
			continue
		} else if code < tableIndex {
			dec := table[code]
			out = append(out, dec...)
			if old != clear && len(dec) > 0 {
				add(table[old], dec[0])
			} else if old != clear {
				return nil, pyErr("IndexError", "index out of range")
			}
			old = code
		} else {
			base := table[old]
			if len(base) == 0 {
				return nil, pyErr("PdfStreamError", "LZW code out of range")
			}
			dec := append(append([]byte(nil), base...), base[0])
			out = append(out, dec...)
			add(base, dec[0])
			old = code
		}
		if len(out) > maxDecodedOutput {
			return nil, pyErr("LimitReachedError", "Limit reached while decompressing.")
		}
	}
	return out, nil
}
