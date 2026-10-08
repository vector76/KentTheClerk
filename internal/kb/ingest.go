package kb

// kb ingest — bring an external document into evidence/ (or off-record/documents/) as a frozen
// projection beside its original (S-0004, S-0038, S-0039). See kb/ingest.py.

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/htmltext"
	"kenttheclerk/internal/mimetypes"
	"kenttheclerk/internal/py"
)

// docNamespace is uuid5(NAMESPACE_URL, "convkb:document"). The namespace string is part of every
// document id; it keeps its original value so ids stay stable.
var docNamespace = uuid5(mustUUIDBytes("6ba7b811-9dad-11d1-80b4-00c04fd430c8"), "convkb:document")

func mustUUIDBytes(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil {
		panic(err)
	}
	return b
}

// uuid5 returns the 16 bytes of uuid.uuid5(namespace, name).
func uuid5(ns []byte, name string) []byte {
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(name))
	b := h.Sum(nil)[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return b
}

func uuidString(b []byte) string {
	x := hex.EncodeToString(b)
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
}

var (
	headingRE = regexp.MustCompile(`^#{1,6}[` + py.SpaceClass + `]+[^` + py.SpaceClass + `]`)
	textExts  = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".text": true, ".csv": true, ".tsv": true,
		".json": true, ".jsonl": true, ".yaml": true, ".yml": true, ".rst": true, ".log": true, ".py": true, ".js": true,
		".ts": true, ".toml": true, ".ini": true, ".cfg": true, ".xml": true, ".sql": true, ".sh": true, ".bat": true}
	extRE = regexp.MustCompile(`^\.[A-Za-z0-9]{1,12}\n?$`)
)

const maxChunkChars = 3000

func pickExtractor(path string) string {
	ext := py.Lower(py.Suffix(path))
	switch {
	case textExts[ext]:
		return "text-v1"
	case ext == ".html" || ext == ".htm":
		return "html-v1"
	case ext == ".pdf":
		return "pdf-v1"
	case ext == ".docx":
		return "docx-v1"
	}
	return "opaque-v1"
}

type chunk struct {
	locator *py.Dict
	text    string
	content []any
}

// chunkUnits splits text units (lines or paragraphs) into chunks. Sections start at markdown headings;
// a run with no headings is grouped by blank-line paragraphs up to maxChunkChars. 1-based, inclusive.
func chunkUnits(units []string, unitName string) []chunk {
	hasHeadings := false
	for _, u := range units {
		if headingRE.MatchString(u) {
			hasHeadings = true
			break
		}
	}
	var chunks []chunk
	if hasHeadings {
		start := 0
		for i := 1; i <= len(units); i++ {
			if i == len(units) || headingRE.MatchString(units[i]) {
				block := units[start:i]
				text := strings.Trim(strings.Join(block, "\n"), "\n")
				if py.Strip(text) != "" {
					loc := py.D(unitName, []any{py.IntOf(start + 1), py.IntOf(i)})
					if headingRE.MatchString(block[0]) {
						loc.Set("heading", py.Head(py.Strip(strings.TrimLeft(block[0], "#")), 120))
					}
					chunks = append(chunks, chunk{locator: loc, text: text})
				}
				start = i
			}
		}
		return chunks
	}
	blank := func(u string) bool { return py.Strip(u) == "" }
	i, n := 0, len(units)
	for i < n {
		for i < n && blank(units[i]) {
			i++
		}
		if i >= n {
			break
		}
		start := i
		size := 0
		for i < n {
			j := i
			for j < n && !blank(units[j]) {
				j++
			}
			paraLen := 0
			for _, u := range units[i:j] {
				paraLen += py.Len(u) + 1
			}
			if size > 0 && size+paraLen > maxChunkChars {
				break
			}
			size += paraLen
			i = j
			for i < n && blank(units[i]) {
				i++
			}
		}
		end := i
		for end > start && blank(units[end-1]) {
			end--
		}
		chunks = append(chunks, chunk{locator: py.D(unitName, []any{py.IntOf(start + 1), py.IntOf(end)}),
			text: strings.Join(units[start:end], "\n")})
	}
	return chunks
}

func readBytes(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		py.RaiseOS(err)
	}
	return b
}

func extractText(path string) ([]chunk, any) {
	raw := readBytes(path)
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(raw) {
		die(py.Name(path) + " is not UTF-8 text; pass --extractor opaque-v1 to store it without a text projection")
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	var title any
	for _, l := range lines {
		if headingRE.MatchString(l) {
			title = py.Strip(strings.TrimLeft(l, "#"))
			break
		}
	}
	return chunkUnits(lines, "lines"), title
}

func extractHTML(path string) ([]chunk, any) {
	lines, title, hasTitle, err := htmltext.ExtractErr(readBytes(path))
	if err != nil {
		py.Raise("AssertionError", "%s", err.Error())
	}
	var t any
	if hasTitle {
		t = title
	}
	return chunkUnits(lines, "lines"), t
}

// xmlElem is the part of an ElementTree element extract_docx uses.
type xmlElem struct {
	tag      string // "{namespace}local"
	attr     map[string]string
	text     string
	children []*xmlElem
}

func parseXML(data []byte) *xmlElem {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(data))
	var root *xmlElem
	var stack []*xmlElem
	name := func(n xml.Name) string {
		if n.Space != "" {
			return "{" + n.Space + "}" + n.Local
		}
		return n.Local
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			py.Raise("xml.etree.ElementTree.ParseError", "%v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			e := &xmlElem{tag: name(t.Name), attr: map[string]string{}}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
					continue
				}
				e.attr[name(a.Name)] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, e)
			} else {
				root = e
			}
			stack = append(stack, e)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				e := stack[len(stack)-1]
				if len(e.children) == 0 {
					e.text += string(t)
				}
			}
		}
	}
	if root == nil {
		py.Raise("xml.etree.ElementTree.ParseError", "no element found")
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

var headingStyleRE = regexp.MustCompile(`^(?i:heading)(\p{Nd})`)

func extractDocx(path string) ([]chunk, any) {
	const W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
	zr, err := zip.OpenReader(path)
	if err != nil {
		die(py.Name(path) + " is not a Word document: File is not a zip file")
	}
	defer zr.Close()
	var member *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			member = f
		}
	}
	if member == nil {
		die(py.Name(path) + ` is not a Word document: "There is no item named 'word/document.xml' in the archive"`)
	}
	rc, err := member.Open()
	if err != nil {
		py.Raise("zipfile.BadZipFile", "%v", err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		py.Raise("zipfile.BadZipFile", "%v", err)
	}
	root := parseXML(data)
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
				if ppr := el.find(W + "pPr"); ppr != nil {
					if st := ppr.find(W + "pStyle"); st != nil {
						sval = st.attr[W+"val"]
					}
				}
				m := headingStyleRE.FindStringSubmatch(sval)
				if m != nil && py.Strip(text) != "" {
					d, _ := utf8.DecodeRuneInString(m[1])
					text = strings.Repeat("#", py.DecimalValue(d)) + " " + py.Strip(text)
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

func extractOpaque(path, caption, mediaType string) ([]chunk, any) {
	st, err := os.Stat(path)
	if err != nil {
		py.RaiseOS(err)
	}
	content := []any{py.D("type", "file", "media_type", mediaType, "size", py.IntOf(int(st.Size())))}
	if caption != "" {
		content = append(content, py.D("type", "text", "text", caption))
	}
	return []chunk{{locator: py.D("whole", true), content: content}}, nil
}

func runIngest(ns argparse.Namespace) int {
	root := repoRoot()
	userArg, _ := ns.Str("user")
	user := currentUser(userArg)
	mode, _ := ns.Str("mode")
	if !in(mode, modes) {
		die("mode must be one of " + py.Tuple(modes...))
	}
	pathArg, _ := ns.Str("path")
	src := py.PathStr(pathArg)
	if !py.IsFile(src) {
		die("no such file: " + src)
	}
	extractor, _ := ns.Str("extractor")
	if extractor == "" {
		extractor = pickExtractor(src)
	}
	if !in(extractor, extractors) {
		die("extractor must be one of " + py.Tuple(extractors...))
	}
	caption, _ := ns.Str("caption")
	if caption != "" && extractor != "opaque-v1" {
		die("--caption applies to opaque-v1 only; other extractors take their text from the document")
	}

	hexdigest := sha256File(src)
	ext := ""
	if suf := py.Suffix(src); extRE.MatchString(suf) {
		ext = py.Lower(suf)
	}
	srcName := py.Name(src)
	mediaType := mimetypes.GuessType(srcName)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	docID := uuidString(uuid5(docNamespace, hexdigest+":"+extractor))

	// one mode per original, as for sessions
	otherMode := "off-record"
	if mode != "on-record" {
		otherMode = "on-record"
	}
	for _, e := range py.Scandir(originalsDir(root, otherMode)) {
		if strings.HasPrefix(e.Name(), hexdigest) {
			die(fmt.Sprintf("these bytes are already in evidence as %s; a document has one mode (%s)", otherMode, rule("mode-fixed")))
		}
	}
	// frozen projection (S-0038): same bytes + same extractor is the same evidence item
	for _, md := range evidenceDirs(root) {
		for _, p := range py.Glob(md.dir, "*-doc-"+docID[:8]+".jsonl") {
			die(fmt.Sprintf("already ingested under extractor %s: %s "+
				"(projections are frozen; a different --extractor makes a new item) (%s)", extractor, rel(p, root), rule("frozen-projection")))
		}
	}

	var chunks []chunk
	var title any
	switch extractor {
	case "text-v1":
		chunks, title = extractText(src)
	case "html-v1":
		chunks, title = extractHTML(src)
	case "pdf-v1":
		chunks, title = extractPDF(src)
	case "docx-v1":
		chunks, title = extractDocx(src)
	default:
		chunks, title = extractOpaque(src, caption, mediaType)
	}
	if len(chunks) == 0 {
		die("extractor produced no chunks; pass --extractor opaque-v1 to store the file without a text projection")
	}

	var records []*py.Dict
	for i, c := range chunks {
		seq := i + 1
		content := c.content
		if len(content) == 0 {
			content = []any{py.D("type", "text", "text", c.text)}
		}
		records = append(records, py.D("type", "chunk",
			"uuid", uuidString(uuid5(docNamespace, fmt.Sprintf("%s:%s:%d", hexdigest, extractor, seq))),
			"seq", py.IntOf(seq), "role", "document", "author", "document", "locator", c.locator, "content", content))
	}

	odir := originalsDir(root, mode)
	if err := os.MkdirAll(odir, 0o777); err != nil {
		py.RaiseOS(err)
	}
	original := filepath.Join(odir, hexdigest+ext)
	var sameHash []string
	for _, e := range py.Scandir(odir) {
		if strings.HasPrefix(e.Name(), hexdigest) {
			sameHash = append(sameHash, filepath.Join(odir, e.Name()))
		}
	}
	if len(sameHash) > 0 {
		found := false
		for _, p := range sameHash {
			if p == original || (py.Windows && py.Lower(p) == py.Lower(original)) {
				found = true
			}
		}
		if !found {
			original = sameHash[0] // same bytes under another extension: reuse, do not duplicate
		}
	}
	duplicate := py.Exists(original)
	if !duplicate {
		if err := copyFile(src, original); err != nil {
			py.RaiseOS(err)
		}
		if sha256File(original) != hexdigest {
			os.Remove(original)
			die("copy did not verify; nothing written")
		}
	}

	destDir := filepath.Join(root, "evidence")
	if mode != "on-record" {
		destDir = filepath.Join(root, "off-record", "documents")
	}
	if err := os.MkdirAll(destDir, 0o777); err != nil {
		py.RaiseOS(err)
	}
	t := now()
	dest := filepath.Join(destDir, t.Format("2006-01-02")+"-doc-"+docID[:8]+".jsonl")
	st, err := os.Stat(src)
	if err != nil {
		py.RaiseOS(err)
	}
	titleArg, _ := ns.Str("title")
	var hTitle any = srcName
	if titleArg != "" {
		hTitle = titleArg
	} else if py.Truthy(title) {
		hTitle = title
	}
	header := py.D("type", "kb-document", "version", py.IntOf(documentVersion), "document_id", docID,
		"title", hTitle, "mode", mode, "extractor", extractor,
		"original", py.D("hash", "sha256:"+hexdigest, "path", "originals/"+py.Name(original), "name", srcName,
			"size", py.IntOf(int(st.Size())), "media_type", mediaType),
		"ingested_by", user, "ingested_at", isoSeconds(t))
	addStamp(header)
	header.Set("chunk_count", py.IntOf(len(records)))
	writeJSONL(dest, header, records)
	println(fmt.Sprintf("wrote %s: %d chunk(s), extractor=%s, mode=%s", rel(dest, root), len(records), extractor, mode))
	how := "stored"
	if duplicate {
		how = "already present"
	}
	println(fmt.Sprintf("original %s as %s (%d bytes, %s)", how, rel(original, root), st.Size(), mediaType))
	println(fmt.Sprintf("document id %s; cite chunks as %s#<chunk-uuid> (kb show %s)", docID, docID[:8], docID[:8]))
	if mode == "off-record" {
		io.WriteString(Stderr, "note: off-record/ is gitignored; this document is at risk of loss by design\n")
	}
	return 0
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func addIngestParser(sub *argparse.Parser) {
	p := sub.AddParser("ingest", "bring an external document into evidence as a frozen projection + original")
	p.Positional("path", argparse.Single, "file to ingest (any format; the text projection depends on the extractor)")
	p.Option("--mode", "on-record (evidence/) or off-record (off-record/documents/)", "on-record", false)
	p.Option("--extractor", strings.Join(extractors, "|")+" (default: by extension)", nil, false)
	p.Option("--title", "title for the projection header (default: first heading, else file name)", nil, false)
	p.Option("--caption", "opaque-v1 only: a line of text to carry with the file", nil, false)
	p.Option("--user", "who brings the document in (else KB_USER / ~/.kenttheclerk.json)", nil, false)
	p.Run = runIngest
}
