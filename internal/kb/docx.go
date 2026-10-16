package kb

// docx-v1: word/document.xml is read from the zip with archive/zip and parsed by a small namespace-aware
// tree builder over encoding/xml's raw tokens. The parser follows expat: as strict where expat fails
// (unbound prefixes, duplicate attributes, mismatched tags, anything after the root element) and as
// lenient where it does not (declared single-byte encodings, UTF-16, XML 1.1, internal entities).

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"

	"kenttheclerk/internal/py"
)

// xmlElem is an element of the parsed tree, with what extractDocx uses.
type xmlElem struct {
	tag      string // "{namespace}local"
	attr     map[string]string
	text     string
	children []*xmlElem
}

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"

var (
	xmlDeclRE    = regexp.MustCompile(`^<\?xml[ \t\r\n]`)
	xmlVersion11 = regexp.MustCompile(`^(<\?xml[ \t\r\n]+version[ \t\r\n]*=[ \t\r\n]*["'])1\.1(["'])`)
	entityDeclRE = regexp.MustCompile(`<!ENTITY[ \t\r\n]+([A-Za-z_:][-A-Za-z0-9_:.]*)[ \t\r\n]+(?:"([^"%&<]*)"|'([^'%&<]*)')[ \t\r\n]*>`)
)

// parseError raises an XML error as expat reports it: at the line and 0-based column where the offending
// token starts.
func parseError(data []byte, offset int64, msg string) {
	before := data[:min(int(offset), len(data))]
	line := bytes.Count(before, []byte{'\n'}) + 1
	col := len(before) - (bytes.LastIndexByte(before, '\n') + 1)
	py.Raise("XML error", "%s: line %d, column %d", msg, line, col)
}

// attrOffset is where the name of attribute k of the start tag at offset start begins.
func attrOffset(data []byte, start int64, k int) int64 {
	i := int(start) + 1
	ws := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
	for i < len(data) && !ws(data[i]) && data[i] != '/' && data[i] != '>' {
		i++
	}
	for n := 0; i < len(data); n++ {
		for i < len(data) && ws(data[i]) {
			i++
		}
		if n == k {
			return int64(i)
		}
		for i < len(data) && data[i] != '=' && !ws(data[i]) {
			i++
		}
		for i < len(data) && (ws(data[i]) || data[i] == '=') {
			i++
		}
		if i < len(data) && (data[i] == '"' || data[i] == '\'') {
			q := data[i]
			i++
			for i < len(data) && data[i] != q {
				i++
			}
			i++
		}
	}
	return start
}

// parseXML parses an XML document into its root element.
func parseXML(data []byte) *xmlElem {
	transcoded := false
	switch {
	case bytes.HasPrefix(data, []byte("\xef\xbb\xbf")):
		data = data[3:]
	case bytes.HasPrefix(data, []byte("\xff\xfe")), bytes.HasPrefix(data, []byte("\xfe\xff")),
		bytes.HasPrefix(data, []byte("<\x00?\x00")), bytes.HasPrefix(data, []byte("\x00<\x00?")):
		order := unicode.LittleEndian
		if data[0] == 0xfe || data[0] == 0 {
			order = unicode.BigEndian
		}
		out, err := unicode.UTF16(order, unicode.UseBOM).NewDecoder().Bytes(data)
		if err != nil {
			py.Raise("XML error", "not well-formed (invalid token): line 1, column 0")
		}
		data, transcoded = out, true
	}
	if t := bytes.TrimLeft(data, " \t\r\n"); len(t) < len(data) && xmlDeclRE.Match(t) {
		parseError(data, int64(len(data)-len(t)), "XML or text declaration not at start of entity")
	}
	data = xmlVersion11.ReplaceAll(data, []byte("${1}1.0${2}")) // expat reads XML 1.1 documents
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Entity = map[string]string{}
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		cs := strings.ToLower(charset)
		switch {
		case transcoded || cs == "us-ascii" || cs == "ascii":
			return input, nil
		case cs == "utf-16" || strings.HasPrefix(cs, "utf-16"):
			return nil, errors.New("encoding specified in XML declaration is incorrect")
		}
		enc, err := ianaindex.IANA.Encoding(charset)
		if err != nil || enc == nil {
			return nil, fmt.Errorf("unknown encoding")
		}
		if _, single := enc.(*charmap.Charmap); !single {
			return nil, fmt.Errorf("unknown encoding") // expat decodes only single-byte encodings itself
		}
		return enc.NewDecoder().Reader(input), nil
	}

	type open struct {
		el    *xmlElem
		raw   xml.Name
		scope map[string]string
	}
	var root *xmlElem
	var stack []open
	rootDone := false
	scope := map[string]string{"xml": xmlNamespace}
	name := func(uri, local string) string {
		if uri != "" {
			return "{" + uri + "}" + local
		}
		return local
	}
	var start int64
	fail := func(msg string) { parseError(data, start, msg) }
	for {
		start = dec.InputOffset()
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			parseError(data, dec.InputOffset(), "not well-formed (invalid token)")
		}
		switch t := tok.(type) {
		case xml.Directive:
			for _, m := range entityDeclRE.FindAllStringSubmatch(string(t), -1) {
				if _, ok := dec.Entity[m[1]]; !ok {
					dec.Entity[m[1]] = m[2] + m[3]
				}
			}
		case xml.StartElement:
			if rootDone {
				fail("junk after document element")
			}
			sc := scope
			if len(stack) > 0 {
				sc = stack[len(stack)-1].scope
			}
			copied := false
			for _, a := range t.Attr {
				prefix := ""
				switch {
				case a.Name.Space == "xmlns":
					prefix = a.Name.Local
				case a.Name.Space == "" && a.Name.Local == "xmlns":
				default:
					continue
				}
				if !copied {
					n := map[string]string{}
					for k, v := range sc {
						n[k] = v
					}
					sc, copied = n, true
				}
				sc[prefix] = a.Value
			}
			uri, ok := sc[t.Name.Space]
			if t.Name.Space != "" && !ok {
				fail("unbound prefix")
			}
			e := &xmlElem{tag: name(uri, t.Name.Local), attr: map[string]string{}}
			seenRaw := map[xml.Name]bool{}
			for ai, a := range t.Attr {
				if seenRaw[a.Name] {
					parseError(data, attrOffset(data, start, ai), "duplicate attribute")
				}
				seenRaw[a.Name] = true
				if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
					continue
				}
				auri := ""
				if a.Name.Space != "" {
					if auri, ok = sc[a.Name.Space]; !ok {
						fail("unbound prefix")
					}
				}
				k := name(auri, a.Name.Local)
				if _, dup := e.attr[k]; dup {
					parseError(data, attrOffset(data, start, ai), "duplicate attribute")
				}
				e.attr[k] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1].el
				p.children = append(p.children, e)
			} else {
				root = e
			}
			stack = append(stack, open{e, t.Name, sc})
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].raw != t.Name {
				parseError(data, start+2, "mismatched tag") // expat points at the name after "</"
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				rootDone = true
			}
		case xml.CharData:
			if len(stack) == 0 {
				if len(bytes.TrimLeft(t, " \t\r\n")) > 0 {
					if rootDone {
						fail("junk after document element")
					}
					fail("syntax error")
				}
				continue
			}
			if e := stack[len(stack)-1].el; len(e.children) == 0 {
				e.text += string(t)
			}
		}
	}
	if root == nil || len(stack) > 0 {
		if root == nil {
			parseError(data, int64(len(data)), "no element found")
		}
		parseError(data, int64(len(data)), "unclosed token")
	}
	return root
}

// iter is Element.iter(tag): the element and its descendants in document order, filtered by tag.
func (e *xmlElem) iter(tag string, fn func(*xmlElem)) {
	if tag == "" || e.tag == tag {
		fn(e)
	}
	for _, c := range e.children {
		c.iter(tag, fn)
	}
}

func (e *xmlElem) find(tag string) *xmlElem {
	for _, c := range e.children {
		if c.tag == tag {
			return c
		}
	}
	return nil
}

// findPath is Element.find("a/b"): the first b child of any a child.
func (e *xmlElem) findPath(a, b string) *xmlElem {
	for _, c := range e.children {
		if c.tag == a {
			if f := c.find(b); f != nil {
				return f
			}
		}
	}
	return nil
}

// headingLevel is the digit after a leading "heading" in any case, where 'i' also matches the dotted and
// dotless I.
func headingLevel(style string) (int, bool) {
	rs := []rune(style)
	word := []rune("heading")
	if len(rs) < len(word)+1 {
		return 0, false
	}
	for k, w := range word {
		r := rs[k]
		ok := r == w || r == w-'a'+'A'
		if w == 'i' && (r == 0x130 || r == 0x131) {
			ok = true
		}
		if !ok {
			return 0, false
		}
	}
	if d := rs[len(word)]; py.IsDecimal(d) {
		return py.DecimalValue(d), true
	}
	return 0, false
}

// readDocumentXML reads word/document.xml from the zip at path. A file that is not a usable zip (not a
// zip, member missing, bad CRC) is reported as not a Word document; other errors propagate.
func readDocumentXML(path string) []byte {
	notWord := func(msg string) { die(py.Name(path) + " is not a Word document: " + msg) }
	zr, err := zip.OpenReader(path)
	if err != nil {
		notWord("File is not a zip file")
	}
	defer zr.Close()
	zr.RegisterDecompressor(12, func(r io.Reader) io.ReadCloser { return io.NopCloser(bzip2.NewReader(r)) })
	var member *zip.File
	for _, f := range zr.File {
		n := f.Name
		if i := strings.IndexByte(n, 0); i >= 0 {
			n = n[:i]
		}
		if py.Windows {
			n = strings.ReplaceAll(n, `\`, "/")
		}
		if n == "word/document.xml" {
			member = f // the last entry of a name wins
		}
	}
	if member == nil {
		notWord(`"There is no item named 'word/document.xml' in the archive"`)
	}
	switch {
	case member.Flags&0x20 != 0:
		py.Raise("NotImplementedError", "compressed patched data (flag bit 5)")
	case member.Flags&0x40 != 0:
		py.Raise("NotImplementedError", "strong encryption (flag bit 6)")
	case member.Flags&0x1 != 0:
		py.Raise("RuntimeError", "File %s is encrypted, password required for extraction", py.StrRepr("word/document.xml"))
	}
	rc, err := member.Open()
	if err != nil {
		if errors.Is(err, zip.ErrAlgorithm) {
			py.Raise("NotImplementedError", "That compression method is not supported")
		}
		notWord(err.Error())
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if errors.Is(err, zip.ErrChecksum) {
		notWord("Bad CRC-32 for file " + py.StrRepr("word/document.xml"))
	}
	if err != nil {
		py.Raise("zlib error", "Error -3 while decompressing data: %v", err)
	}
	return data
}

func extractDocx(path string) ([]chunk, any) {
	const W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
	root := parseXML(readDocumentXML(path))
	body := root.find(W + "body")
	var paras []string
	var title any

	paraText := func(p *xmlElem) string {
		var b strings.Builder
		p.iter("", func(el *xmlElem) {
			switch el.tag {
			case W + "t":
				b.WriteString(el.text)
			case W + "tab":
				b.WriteString("\t")
			case W + "br":
				b.WriteString("\n")
			}
		})
		return b.String()
	}

	if body != nil {
		for _, el := range body.children {
			switch el.tag {
			case W + "p":
				text := paraText(el)
				sval := ""
				if st := el.findPath(W+"pPr", W+"pStyle"); st != nil {
					sval = st.attr[W+"val"]
				}
				if level, ok := headingLevel(sval); ok && py.Strip(text) != "" {
					text = strings.Repeat("#", level) + " " + py.Strip(text)
					if !py.Truthy(title) {
						title = py.Strip(strings.TrimLeft(text, "#"))
					}
				} else if py.Lower(sval) == "title" && py.Strip(text) != "" {
					if !py.Truthy(title) {
						title = py.Strip(text)
					}
					text = "# " + py.Strip(text)
				}
				paras = append(paras, text)
			case W + "tbl":
				el.iter(W+"tr", func(tr *xmlElem) {
					if tr == el {
						return
					}
					var cells []string
					tr.iter(W+"tc", func(tc *xmlElem) {
						cells = append(cells, py.Strip(strings.ReplaceAll(paraText(tc), "\n", " ")))
					})
					paras = append(paras, "| "+strings.Join(cells, " | ")+" |")
				})
				paras = append(paras, "")
			}
		}
	}
	return chunkUnits(paras, "paragraphs"), title
}
