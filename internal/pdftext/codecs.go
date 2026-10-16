package pdftext

import (
	"unicode/utf8"

	xenc "golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// Text decoders used by the extraction.

// decodeLatin1 maps each byte to the code point of the same value.
func decodeLatin1(b []byte) ustr {
	r := make(ustr, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return r
}

// decodeUTF16BEPass decodes big-endian UTF-16: valid surrogate pairs
// combine, lone surrogates are kept; odd lengths fail.
func decodeUTF16BEPass(b []byte) (ustr, bool) {
	if len(b)%2 != 0 {
		return nil, false
	}
	return utf16Units(b, true), true
}

func utf16Units(b []byte, bigEndian bool) ustr {
	n := len(b) / 2
	u := make([]rune, n)
	for i := 0; i < n; i++ {
		if bigEndian {
			u[i] = rune(b[2*i])<<8 | rune(b[2*i+1])
		} else {
			u[i] = rune(b[2*i+1])<<8 | rune(b[2*i])
		}
	}
	out := make(ustr, 0, n)
	for i := 0; i < n; i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF && i+1 < n && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			out = append(out, 0x10000+((c-0xD800)<<10)+(u[i+1]-0xDC00))
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasSurrogate(s ustr) bool {
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			return true
		}
	}
	return false
}

// decodeUTF16Strict decodes with an explicit byte order, strict errors.
func decodeUTF16Strict(b []byte, bigEndian bool) (ustr, bool) {
	if len(b)%2 != 0 {
		return nil, false
	}
	s := utf16Units(b, bigEndian)
	if hasSurrogate(s) {
		return nil, false
	}
	return s, true
}

// decodeUTF16Auto decodes UTF-16 strictly: a BOM selects the byte order
// and is dropped; without one, little endian.
func decodeUTF16Auto(b []byte) (ustr, bool) {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16Strict(b[2:], true)
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16Strict(b[2:], false)
	}
	return decodeUTF16Strict(b, false)
}

// decodeUTF8Strict decodes UTF-8; ok is false for invalid input.
func decodeUTF8Strict(b []byte) (ustr, bool) {
	if !utf8.Valid(b) {
		return nil, false
	}
	return ustr([]rune(string(b))), true
}

// decodePDFDoc decodes PDFDocEncoding; ok is false for a byte that has no
// character in it.
func decodePDFDoc(b []byte) (ustr, bool) {
	loadTables()
	tab := charsets["/PDFDocEncoding"]
	out := make(ustr, 0, len(b))
	for _, c := range b {
		ch := tab[c]
		if len(ch) == 1 && ch[0] == 0 {
			return nil, false
		}
		out = append(out, ch...)
	}
	return out, true
}

// textString decodes a string from the file as a text string: UTF-16 when
// it starts with a byte order mark or has a zero in its first two bytes,
// PDFDocEncoding otherwise. ok is false where it stays a byte string.
func textString(b []byte) (ustr, bool) {
	if len(b) >= 2 && ((b[0] == 0xFE && b[1] == 0xFF) || (b[0] == 0xFF && b[1] == 0xFE)) {
		return decodeUTF16Auto(b)
	}
	if len(b) >= 1 && b[0] == 0 {
		return decodeUTF16Strict(b, true)
	}
	if len(b) >= 2 && b[1] == 0 {
		return decodeUTF16Strict(b, false)
	}
	return decodePDFDoc(b)
}

// byteStringStr is the text of a byte string: the first of utf-16, utf-8,
// gbk and latin-1 that decodes it.
func byteStringStr(b []byte) ustr {
	if s, ok := decodeUTF16Auto(b); ok {
		return s
	}
	if s, ok := decodeUTF8Strict(b); ok {
		return s
	}
	if s, ok := decodeCJK(b, "gbk"); ok {
		return s
	}
	return decodeLatin1(b)
}

// stringText is the text of a string object read from the file: a text string
// where it decodes as one, otherwise a byte string (byteStringStr).
func stringText(s *String) ustr {
	if t, ok := textString(s.Raw); ok {
		return t
	}
	return byteStringStr(s.Raw)
}

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// decodeCJK strictly decodes the multi-byte codecs gbk, gb2312, gb18030,
// cp950 and cp932; ok is false for invalid input. golang.org/x/text decoders
// replace invalid input with U+FFFD; since none of these charsets map
// anything to U+FFFD, a U+FFFD in the result means the input is invalid.
func decodeCJK(b []byte, codec string) (ustr, bool) {
	var dec *xenc.Decoder
	switch codec {
	case "gbk", "gb2312":
		dec = simplifiedchinese.GBK.NewDecoder()
	case "gb18030":
		dec = simplifiedchinese.GB18030.NewDecoder()
	case "cp950":
		dec = traditionalchinese.Big5.NewDecoder()
	case "cp932":
		dec = japanese.ShiftJIS.NewDecoder()
	default:
		return nil, false
	}
	if codec == "gbk" || codec == "gb2312" {
		// gbk has no single-byte 0x80 here (x/text maps it to the euro
		// sign) and gb2312 is limited to EUC-CN byte pairs.
		for i := 0; i < len(b); i++ {
			c := b[i]
			if c < 0x80 {
				continue
			}
			if c == 0x80 || c == 0xFF || i+1 >= len(b) {
				return nil, false
			}
			if codec == "gb2312" && (c < 0xA1 || b[i+1] < 0xA1 || b[i+1] == 0xFF) {
				return nil, false
			}
			i++
		}
	}
	out, err := dec.Bytes(b)
	if err != nil {
		return nil, false
	}
	r := []rune(string(out))
	for _, c := range r {
		if c == 0xFFFD {
			return nil, false
		}
	}
	return ustr(r), true
}
