// Package pdftext extracts the text of PDF pages and the document title.
//
// It reads the file structure leniently (non-strict: a damaged xref table
// is realigned or rebuilt, a lost /Root is searched for) and extracts each
// page's text in a plain layout mode, character for character the same
// for the same file. Its algorithms and tables derive from work Copyright
// (c) 2006-2008 Mathieu Fenniak, 2007 Ashish Kulkarni and contributors,
// distributed under the BSD-3-Clause license (see tables_gen.go).
//
// The extraction keeps its quirks (e.g. text dropped when a line switches
// to right-to-left script, dictionaries kept in insertion order, which
// errors are swallowed and which abort the extraction), because the goal
// is stable output, not better output. Errors are *pdfError values carrying
// an errKind, raised with panic/recover where a failure has to unwind to an
// enclosing handler, which decides by kind what it catches.
//
// Resource limits: 75 MB per decoded filter output, 4 MB predictor rows,
// 250,000 predictor columns, 10,000 /Contents entries and 75 MB of joined
// content, 75 MB declared stream lengths, 100,000 /ToUnicode entries, the
// 10,000-object /Root recovery limit, token length limits; past one,
// Extract returns an errLimit error.
//
// Hostile input cannot hang the extraction or exhaust memory:
//   - An xref table whose entry realignment stops making progress near the
//     end of the file is an error rather than an endless loop.
//   - Work budget: more than 250,000 form XObject executions, 50 million
//     content operations or 50 million characters of text per document is
//     an error (frames.go).
//
// Known limitations:
//   - Encrypted files (an /Encrypt entry) always fail, even RC4 or AES
//     files with an empty user password.
//   - Recursion depth is bounded by a model of a 1000-frame call stack
//     (frames.go): about 490 form XObjects, 496 nested arrays and 983
//     page-tree levels. Near those limits the model is approximate.
//   - Inline images: settings keys that are not names are ignored and an
//     indirect reference inside the settings fails.
//   - The CJK codecs (gbk, gb2312, gb18030, cp950, cp932) come from
//     golang.org/x/text, and their handling of some rare or invalid byte
//     sequences is its own. They matter only for non-UTF-8 names, byte
//     string titles and fonts using predefined CJK CMaps.
//   - Recovery from corrupt Flate data keeps the bytes decoded before the
//     corruption, though not necessarily all of them.
//   - A title that is an array or dictionary is written as its contents,
//     with "..." where an object's identity would appear.
//   - Integers beyond int64 are rejected, e.g. huge /ToUnicode codes.
//   - The average glyph width (used for spacing when a font has no space
//     width) is a plain left-to-right sum in float64, without compensated
//     summation.
package pdftext

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Extract reads the PDF at path: the text of every page (plain mode), and the /Title of the document
// information dictionary converted to text (see strOf). hasTitle is false where there is no title, the
// information dictionary cannot be read, or the title is empty, zero, false or null.
// err is non-nil where the file cannot be extracted at all.
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
			} else if be, ok := r.(budgetError); ok {
				err = pdfErr(errLimit, "pdftext work limit: %s", be.msg)
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
		return nil, "", false, pdfErr(errUnsupported, "encrypted PDF files are not supported")
	}
	// The title object; an error reading it means no title.
	var titleObj Object
	_ = catch(func() error {
		d.frame = frameMetadata
		titleObj = d.titleObject()
		return nil
	})
	err = catch(func() error {
		for _, pg := range d.pages() {
			text := d.extractText(pg, nil, true, nil, 0)
			d.spend(0, 0, len(text))
			pages = append(pages, text.goString())
		}
		// The title is converted after the pages, and its errors abort.
		if titleObj != nil {
			d.frame = frameTitleStr
			title, hasTitle = d.objText(titleObj).goString(), true
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

// titleObject returns the /Title of the document information dictionary,
// or nil where there is none or it is not truthy.
func (d *Document) titleObject() Object {
	info, ok := d.trailer.Get("/Info")
	if !ok || isNullOrNone(d, info) {
		return nil
	}
	idict, ok := asDict(d.resolve(info))
	if !ok {
		raise(pdfErr(errMalformed, "Trailer not found or does not point to a document information dictionary"))
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

// objText converts an object to text the way a title is written.
func (d *Document) objText(o Object) ustr {
	return ustr([]rune(d.strOf(o, 0)))
}

func (d *Document) strOf(o Object, depth int) string {
	if depth > 20 {
		return "..."
	}
	switch v := o.(type) {
	case nil:
		return "None"
	case *String:
		return stringText(v).goString()
	case Name:
		return string(v)
	case Int:
		return strconv.FormatInt(int64(v), 10)
	case Real:
		return floatRepr(float64(v))
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
		parts = append(parts, quoteRepr(string(k))+": "+d.reprOf(v.m[k], depth+1))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// reprOf writes an element of an array or dictionary: names and strings
// quoted, references as IndirectObject(num, gen, ...), the rest as strOf.
func (d *Document) reprOf(o Object, depth int) string {
	switch v := o.(type) {
	case Name:
		return quoteRepr(string(v))
	case *String:
		return quoteRepr(stringText(v).goString())
	case Ref:
		return fmt.Sprintf("IndirectObject(%d, %d, ...)", v.Num, v.Gen)
	}
	return d.strOf(o, depth)
}

func quoteRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		return "\"" + s + "\""
	}
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// floatRepr writes a real with at most 8 significant digits before trailing zeros are dropped.
func floatRepr(f float64) string {
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
