package pdftext

import (
	"unicode/utf8"

	xenc "golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// Python codec equivalents used by pypdf's text extraction.

// decodeLatin1 is bytes.decode("charmap") / "latin1".
func decodeLatin1(b []byte) pstr {
	r := make(pstr, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return r
}

// decodeUTF16BEPass is bytes.decode("utf-16-be", "surrogatepass"): valid
// surrogate pairs combine, lone surrogates are kept; odd lengths fail.
func decodeUTF16BEPass(b []byte) (pstr, bool) {
	if len(b)%2 != 0 {
		return nil, false
	}
	return utf16Units(b, true), true
}

func utf16Units(b []byte, bigEndian bool) pstr {
	n := len(b) / 2
	u := make([]rune, n)
	for i := 0; i < n; i++ {
		if bigEndian {
			u[i] = rune(b[2*i])<<8 | rune(b[2*i+1])
		} else {
			u[i] = rune(b[2*i+1])<<8 | rune(b[2*i])
		}
	}
	out := make(pstr, 0, n)
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

func hasSurrogate(s pstr) bool {
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			return true
		}
	}
	return false
}

// decodeUTF16Strict decodes with an explicit byte order, strict errors.
func decodeUTF16Strict(b []byte, bigEndian bool) (pstr, bool) {
	if len(b)%2 != 0 {
		return nil, false
	}
	s := utf16Units(b, bigEndian)
	if hasSurrogate(s) {
		return nil, false
	}
	return s, true
}

// decodeUTF16Auto is bytes.decode("utf-16"): a BOM selects the byte order
// and is dropped; without one, little endian (native order on the machines
// pypdf runs on here).
func decodeUTF16Auto(b []byte) (pstr, bool) {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16Strict(b[2:], true)
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16Strict(b[2:], false)
	}
	return decodeUTF16Strict(b, false)
}

// decodeUTF8Strict is bytes.decode("utf-8").
func decodeUTF8Strict(b []byte) (pstr, bool) {
	if !utf8.Valid(b) {
		return nil, false
	}
	return pstr([]rune(string(b))), true
}

// decodePDFDoc is pypdf's decode_pdfdocencoding.
func decodePDFDoc(b []byte) (pstr, bool) {
	loadTables()
	tab := charsets["/PDFDocEncoding"]
	out := make(pstr, 0, len(b))
	for _, c := range b {
		ch := tab[c]
		if len(ch) == 1 && ch[0] == 0 {
			return nil, false
		}
		out = append(out, ch...)
	}
	return out, true
}

// textString is pypdf's create_string_object(bytes) without a forced
// encoding. ok is false where pypdf makes a ByteStringObject.
func textString(b []byte) (pstr, bool) {
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

// byteStringStr is str(ByteStringObject): utf-16, utf-8, gbk, latin1.
func byteStringStr(b []byte) pstr {
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

// pyStr is str() of a pypdf string object read from the file.
func pyStr(s *String) pstr {
	if t, ok := textString(s.Raw); ok {
		return t
	}
	return byteStringStr(s.Raw)
}

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// decodeCJK is bytes.decode(codec) (strict) for the multi-byte codecs pypdf
// uses: gbk, gb2312, gb18030, cp950 and cp932. golang.org/x/text decoders
// replace invalid input with U+FFFD; since none of these charsets map
// anything to U+FFFD, a U+FFFD in the result means Python would raise.
func decodeCJK(b []byte, codec string) (pstr, bool) {
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
		// Python's gbk has no single-byte 0x80 (x/text maps it to the euro
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
	return pstr(r), true
}
