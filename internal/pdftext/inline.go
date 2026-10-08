package pdftext

import (
	"bytes"
	"math"
	"strings"
)

// Port of ContentStream._read_inline_image and pypdf/generic/_image_inline.py.
// Only the stream position matters for text extraction (the image data is
// discarded), so the readers below track positions, not the data.

const inlineBufferSize = 8192

func isWhiteByte(c int) bool { return c >= 0 && isPdfWhite(byte(c)) }

// seekRel is stream.seek(n, 1); BytesIO refuses negative positions.
func (p *parser) seekRel(n int) error {
	if p.pos+n < 0 {
		return pyErr("ValueError", "negative seek value")
	}
	p.pos += n
	return nil
}

// readInlineImage consumes "BI <settings> ID <data> EI" (positioned after BI).
func (p *parser) readInlineImage() error {
	settings := newDict()
	for {
		c := p.readNonWhitespace()
		if c < 0 {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		p.back(1)
		if c == 'I' {
			break
		}
		key, err := p.readObject()
		if err != nil {
			return err
		}
		p.readNonWhitespace()
		p.back(1)
		value, err := p.readObject()
		if err != nil {
			return err
		}
		if k, ok := key.(Name); ok {
			settings.Set(k, value)
		}
	}
	if tmp := p.read(3); len(tmp) < 2 || string(tmp[:2]) != "ID" {
		return pyErr("AssertionError", "")
	}
	get := func(k Name, def Object) Object {
		if v, ok := settings.Get(k); ok {
			return v
		}
		return def
	}
	filtr := get("/F", get("/Filter", Name("not set")))
	savpos := p.pos
	if arr, ok := filtr.(Array); ok {
		if len(arr) > 0 {
			filtr = arr[0]
		} else {
			filtr = Name("not set")
		}
	}
	f, isStr := strObject(filtr)
	if !isStr {
		f = "not set"
	}
	var err error
	switch {
	case strings.Contains(f, "AHx") || strings.Contains(f, "ASCIIHexDecode"):
		err = p.inlineASCIIHex()
	case strings.Contains(f, "A85") || strings.Contains(f, "ASCII85Decode"):
		err = p.inlineASCII85()
	case strings.Contains(f, "RL") || strings.Contains(f, "RunLengthDecode"):
		err = p.inlineRunLength()
	case strings.Contains(f, "DCT") || strings.Contains(f, "DCTDecode"):
		err = p.inlineDCT()
	case f == "not set":
		cs := get("/CS", &String{})
		if arr, ok := cs.(Array); ok {
			if len(arr) > 0 {
				cs = arr[0]
			} else {
				cs = &String{}
			}
		}
		css, ok := strObject(cs)
		if !ok {
			css = ""
		}
		lcs := 0.0
		switch {
		case strings.Contains(css, "RGB"):
			lcs = 3
		case strings.Contains(css, "CMYK"):
			lcs = 4
		default:
			def := Object(Int(-1))
			switch css {
			case "/I", "/G", "/Indexed", "/DeviceGray":
				def = Int(8)
			}
			bits := get("/BPC", def)
			bv, isNum := 0.0, false
			switch b := bits.(type) {
			case Int:
				bv, isNum = float64(b), true
			case Real:
				bv, isNum = float64(b), true
			}
			if isNum && bv > 0 {
				lcs = bv / 8.0
			} else {
				if err := p.inlineDefault(); err != nil {
					return err
				}
				lcs = -1
			}
		}
		w, wInt := get("/W", nil).(Int)
		h, hInt := get("/H", nil).(Int)
		if lcs > 0 && wInt && hInt {
			n := int64(math.Ceil(float64(w)*lcs)) * int64(h)
			if n < 0 {
				p.pos = maxInt(p.pos, len(p.data)) // read(-n) reads everything
			} else {
				p.read(int(minInt64(n, int64(len(p.data)+1))))
			}
		} else if lcs > 0 {
			if err := p.inlineDefault(); err != nil {
				return err
			}
		}
		p.readNonWhitespace()
		p.back(1)
	default:
		err = p.inlineDefault()
	}
	if err != nil {
		return err
	}
	if p.endImageAt() {
		return nil
	}
	p.pos = savpos
	if err := p.inlineDefault(); err != nil {
		return err
	}
	if p.endImageAt() {
		return nil
	}
	return pyErr("PdfStreamError", "Could not extract inline image, even using fallback.")
}

// endImageAt reads "EI" plus one byte (stepping back over that byte) and
// reports whether it is a proper end-of-image marker.
func (p *parser) endImageAt() bool {
	ei := p.read(3)
	if len(ei) == 3 {
		p.back(1)
	}
	if len(ei) < 2 || string(ei[:2]) != "EI" {
		return false
	}
	return len(ei) == 2 || isPdfWhite(ei[2])
}

func strObject(o Object) (string, bool) {
	switch v := o.(type) {
	case Name:
		return string(v), true
	case *String:
		if t, ok := textString(v.Raw); ok {
			return t.goString(), true
		}
	}
	return "", false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// checkEndImageMarker is _check_end_image_marker.
func (p *parser) checkEndImageMarker() bool {
	tok := p.readNonWhitespace()
	var ei []byte
	if tok >= 0 {
		ei = append(ei, byte(tok))
	}
	ei = append(ei, p.read(2)...)
	p.back(3)
	if len(ei) < 2 || string(ei[:2]) != "EI" {
		return false
	}
	return len(ei) == 2 || isPdfWhite(ei[2])
}

// nonWhiteAndBuffer is read_non_whitespace(stream) + stream.read(BUFFER_SIZE);
// it returns the buffer, the position it starts at and len(read_bytes).
func (p *parser) nonWhiteAndBuffer() ([]byte, int, int) {
	tok := p.readNonWhitespace()
	var buf []byte
	start := p.pos
	if tok >= 0 {
		buf = append(buf, byte(tok))
		start = p.pos - 1
	}
	rb := p.read(inlineBufferSize)
	buf = append(buf, rb...)
	return buf, start, len(rb)
}

func (p *parser) inlineASCIIHex() error {
	for {
		buf, start, nread := p.nonWhiteAndBuffer()
		if len(buf) == 0 || nread == 0 {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		if i := bytes.IndexByte(buf, '>'); i >= 0 {
			p.pos = start + i + 1
			break
		}
		if i := bytes.Index(buf, []byte("EI")); i >= 0 {
			if err := p.seekRel(-(p.pos - start) + i - 1); err != nil {
				return err
			}
			c := p.read1()
			for isWhiteByte(c) {
				if err := p.seekRel(-2); err != nil {
					return err
				}
				c = p.read1()
			}
			break
		}
		if len(buf) == 2 {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		p.back(2)
	}
	if !p.checkEndImageMarker() {
		return pyErr("PdfReadError", "EI stream not found.")
	}
	return nil
}

func (p *parser) inlineASCII85() error {
	for {
		buf, start, nread := p.nonWhiteAndBuffer()
		if len(buf) == 0 || nread == 0 {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		if i := bytes.Index(buf, []byte("~>")); i >= 0 {
			p.pos = start + i + 2
			break
		}
		if len(buf) == 2 {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		p.back(2)
	}
	if !p.checkEndImageMarker() {
		return pyErr("PdfReadError", "EI stream not found.")
	}
	return nil
}

func (p *parser) inlineRunLength() error {
	for {
		start := p.pos
		buf := p.read(inlineBufferSize)
		if len(buf) == 0 {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		i := bytes.IndexByte(buf, 0x80)
		if i < 0 {
			continue
		}
		e := i + 4
		if e > len(buf) {
			e = len(buf)
		}
		after := buf[i+1 : e]
		if bytes.HasPrefix(after, []byte("EI")) || bytes.HasSuffix(after, []byte("EI")) {
			p.pos = start + i + 1
		} else if ei := bytes.Index(buf, []byte("EI")); ei > 0 {
			p.pos = start + ei - 1
		}
		break
	}
	if !p.checkEndImageMarker() {
		return pyErr("PdfReadError", "EI stream not found.")
	}
	return nil
}

func (p *parser) inlineDCT() error {
	read := func(n int) ([]byte, error) {
		b := p.read(n)
		if len(b) != n {
			return nil, pyErr("PdfReadError", "Unexpected end of stream.")
		}
		return b, nil
	}
	for {
		c, err := read(1)
		if err != nil {
			return err
		}
		if c[0] != 0xFF {
			continue
		}
		c, err = read(1)
		if err != nil {
			return err
		}
		switch m := c[0]; {
		case m == 0xFF:
			p.back(1)
		case m == 0x00:
		case m == 0xD9:
			if !p.checkEndImageMarker() {
				return pyErr("PdfReadError", "EI stream not found.")
			}
			return nil
		case (m >= 0xC0 && m <= 0xC7) || (m >= 0xC9 && m <= 0xCF) || (m >= 0xDA && m <= 0xDF) ||
			(m >= 0xE0 && m <= 0xEF) || m == 0xFE:
			sz, err := read(2)
			if err != nil {
				return err
			}
			n := int(sz[0])*256 + int(sz[1]) - 2
			if n < 0 {
				// read(negative) returns everything left, never n bytes
				return pyErr("PdfReadError", "Unexpected end of stream.")
			}
			if _, err := read(n); err != nil {
				return err
			}
		}
	}
}

// inlineDefault is extract_inline_default: it leaves the stream on the "E"
// of the "EI" that ends the image.
func (p *parser) inlineDefault() error {
	bufStart := p.pos
	i := p.pos
	for {
		if bufStart >= len(p.data) {
			return pyErr("PdfReadError", "Unexpected end of stream.")
		}
		bufEnd := bufStart + inlineBufferSize
		if bufEnd > len(p.data) {
			bufEnd = len(p.data)
		}
		e := -1
		for j := i; j < bufEnd; j++ {
			if p.data[j] == 'E' {
				e = j
				break
			}
		}
		if e < 0 {
			bufStart, i = bufEnd, bufEnd
			continue
		}
		next := e + 1 // saved_pos: a rejected candidate restarts the buffer here
		if e+1 >= len(p.data) || p.data[e+1] != 'I' {
			bufStart, i = next, next
			continue
		}
		if e+2 >= len(p.data) {
			p.pos = e
			return nil
		}
		if !isPdfWhite(p.data[e+2]) {
			bufStart, i = next, next
			continue
		}
		k := e + 2
		for k < len(p.data) && isPdfWhite(p.data[k]) {
			k++
		}
		tok3 := -1
		if k < len(p.data) {
			tok3 = int(p.data[k])
			k++
		}
		prevWhite := e > bufStart && isPdfWhite(p.data[e-1])
		if !prevWhite && tok3 != 'Q' && tok3 != 'E' {
			bufStart, i = next, next
			continue
		}
		if followedByBinaryData(p.data, k) {
			bufStart, i = next, next
			continue
		}
		p.pos = e
		return nil
	}
}

// followedByBinaryData is is_followed_by_binary_data.
func followedByBinaryData(data []byte, pos int) bool {
	if pos >= len(data) {
		return false
	}
	end := pos + 10
	if end > len(data) {
		end = len(data)
	}
	d := data[pos:end]
	opStart, opEnd := -1, -1
	for i, b := range d {
		white := isPdfWhite(b)
		if b < 32 && !white {
			return true
		}
		if opStart < 0 && !white {
			opStart = i
		}
		if opStart >= 0 && white {
			opEnd = i
			break
		}
	}
	if opStart < 0 {
		return false
	}
	if opEnd < 0 {
		opEnd = 10
	}
	opLen := opEnd - opStart
	if opEnd > len(d) {
		opEnd = len(d)
	}
	op := d[opStart:opEnd]
	if len(op) > 0 && op[0] == '/' && opLen > 1 {
		return false
	}
	if digits := bytes.ReplaceAll(op, []byte("."), nil); len(digits) > 0 && isDigits(digits) {
		return false
	}
	return opLen > 3
}
