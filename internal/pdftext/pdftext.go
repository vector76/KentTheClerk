// Package pdftext extracts the text of PDF pages the way pypdf 6.x does.
//
// It is a port of pypdf's PdfReader (non-strict) plus PageObject.extract_text
// in its default "plain" mode, written to give character-for-character the
// same output. pypdf is Copyright (c) 2006-2008 Mathieu Fenniak, 2007 Ashish
// Kulkarni and contributors, and is distributed under the BSD-3-Clause
// license; the port follows its algorithms and tables (see tables_gen.go).
//
// The port mirrors pypdf down to its quirks (e.g. text dropped when a line
// switches to right-to-left script, Python dict ordering, which exceptions
// are swallowed and which abort the extraction), because the goal is
// identical output, not better output. Python exceptions are modelled as
// *pyError values carrying the Python class name, raised with panic/recover
// where pypdf relies on try/except.
//
// Known differences from pypdf 6.14.2:
//   - Encrypted files (an /Encrypt entry) always fail. pypdf decrypts RC4
//     files that have an empty user password (and AES ones when the
//     "cryptography" package is installed).
//   - Inline images: settings keys that are not names are ignored and an
//     indirect reference inside the settings fails (pypdf would accept it).
//   - The CJK codecs (gbk, gb2312, gb18030, cp950, cp932) come from
//     golang.org/x/text and disagree with Python's codecs on some rare or
//     invalid byte sequences. They matter only for non-UTF-8 names, byte
//     string titles and fonts using predefined CJK CMaps.
//   - Recovery from corrupt Flate data: both keep the bytes decoded before
//     the corruption, but the amount can differ.
//   - Damaged-file corner cases: str() of non-string titles (arrays,
//     dictionaries) is approximated; integers beyond int64; the recursion
//     limit for nested object loads; pypdf's size limits other than the
//     declared stream length and token lengths.
package pdftext

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Extract reproduces pypdf's PdfReader(path): the text of every page as page.extract_text() returns it
// (default "plain" mode), and the document title as (reader.metadata or {}).get("/Title"), converted the
// way Python's str() would convert it. hasTitle is false where Python's value would be None or falsy.
// err is non-nil where pypdf would raise (the Python caller then crashes).
func Extract(path string) (pages []string, title string, hasTitle bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", false, err
	}
	return ExtractBytes(data)
}

// ExtractBytes is Extract for PDF bytes already in memory.
func ExtractBytes(data []byte) (pages []string, title string, hasTitle bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			if rr, ok := r.(raised); ok {
				err = rr.err
			} else {
				err = fmt.Errorf("pdftext: internal error: %v", r)
			}
			pages, title, hasTitle = nil, "", false
		}
	}()
	d, err := openDocument(data)
	if err != nil {
		return nil, "", false, err
	}
	if d.trailer.Has("/Encrypt") {
		return nil, "", false, pyErr("NotImplemented", "encrypted PDF files are not supported")
	}
	// title = (reader.metadata or {}).get("/Title") or None  (errors -> None)
	var titleObj Object
	_ = catch(func() error {
		titleObj = d.titleObject()
		return nil
	})
	err = catch(func() error {
		for _, pg := range d.pages() {
			text := d.extractText(pg, nil, true, nil)
			pages = append(pages, text.goString())
		}
		// str(title) runs after the pages, outside any try
		if titleObj != nil {
			title, hasTitle = d.pyStrObj(titleObj).goString(), true
		}
		return nil
	})
	if err != nil {
		return nil, "", false, err
	}
	if pages == nil {
		pages = []string{}
	}
	return pages, title, hasTitle, nil
}

// titleObject evaluates (reader.metadata or {}).get("/Title") or None; nil
// stands for None.
func (d *Document) titleObject() Object {
	info, ok := d.trailer.Get("/Info")
	if !ok || isNullOrNone(d, info) {
		return nil
	}
	idict, ok := asDict(d.resolve(info))
	if !ok {
		raise(pyErr("PdfReadError", "Trailer not found or does not point to a document information dictionary"))
	}
	if idict.Len() == 0 {
		return nil
	}
	t, ok := idict.Get("/Title")
	if !ok || !truthy(t) {
		return nil
	}
	return t
}

// pyStrObj is str() of a pypdf object.
func (d *Document) pyStrObj(o Object) pstr {
	return pstr([]rune(d.strOf(o, 0)))
}

func (d *Document) strOf(o Object, depth int) string {
	if depth > 20 {
		return "..."
	}
	switch v := o.(type) {
	case nil:
		return "None"
	case *String:
		return pyStr(v).goString()
	case Name:
		return string(v)
	case Int:
		return strconv.FormatInt(int64(v), 10)
	case Real:
		return pyFloatRepr(float64(v))
	case Bool:
		if v {
			return "True"
		}
		return "False"
	case Null:
		return "NullObject"
	case Ref:
		r, err := d.getObject(v)
		if err != nil {
			raise(err)
		}
		return d.strOf(r, depth+1)
	case Array:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = d.reprOf(x, depth+1)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Dict:
		return d.dictRepr(v, depth)
	case *Stream:
		return d.dictRepr(v.Dict, depth)
	}
	return ""
}

func (d *Document) dictRepr(v *Dict, depth int) string {
	parts := make([]string, 0, v.Len())
	for _, k := range v.Keys() {
		parts = append(parts, pyQuote(string(k))+": "+d.reprOf(v.m[k], depth+1))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// reprOf approximates repr() inside container reprs.
func (d *Document) reprOf(o Object, depth int) string {
	switch v := o.(type) {
	case Name:
		return pyQuote(string(v))
	case *String:
		return pyQuote(pyStr(v).goString())
	case Ref:
		return fmt.Sprintf("IndirectObject(%d, %d, ...)", v.Num, v.Gen)
	}
	return d.strOf(o, depth)
}

func pyQuote(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		return "\"" + s + "\""
	}
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// pyFloatRepr is FloatObject.myrepr.
func pyFloatRepr(f float64) string {
	if f == 0 {
		return "0.0"
	}
	nb := 8 - int(math.Log10(math.Abs(f)))
	if nb < 1 {
		nb = 1
	}
	s := strconv.FormatFloat(f, 'f', nb, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}
