package pdftext

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Regression tests for adversarial input.

type pdfObj struct {
	num  int
	body []byte
}

func buildPDF(objs []pdfObj, root int, trailerExtra string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offs := map[int]int{}
	maxNum := 0
	for _, o := range objs {
		offs[o.num] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", o.num)
		out.Write(o.body)
		out.WriteString("\nendobj\n")
		if o.num > maxNum {
			maxNum = o.num
		}
	}
	size := maxNum + 1
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", size)
	for i := 1; i < size; i++ {
		if off, ok := offs[i]; ok {
			fmt.Fprintf(&out, "%010d 00000 n \n", off)
		} else {
			out.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", size, root, trailerExtra, xref)
	return out.Bytes()
}

func streamObj(data []byte, extra string) []byte {
	return append(append([]byte(fmt.Sprintf("<< /Length %d %s >>\nstream\n", len(data), extra)), data...), "\nendstream"...)
}

var (
	objCatalog = pdfObj{1, []byte("<< /Type /Catalog /Pages 2 0 R >>")}
	objPages   = pdfObj{2, []byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")}
	objFont    = pdfObj{5, []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")}
	textA      = []byte("BT /F1 12 Tf (A) Tj ET")
)

func pageObj(contents, res string) pdfObj {
	if contents == "" {
		contents = "4 0 R"
	}
	if res == "" {
		res = "<< /Font << /F1 5 0 R >> >>"
	}
	return pdfObj{3, []byte("<< /Type /Page /Parent 2 0 R /Contents " + contents + " /Resources " + res + " >>")}
}

func simplePDF(extra ...pdfObj) []byte {
	objs := []pdfObj{objCatalog, objPages, pageObj("", ""), {4, streamObj(textA, "")}, objFont}
	return buildPDF(append(objs, extra...), 1, "")
}

func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// extractOK runs ExtractBytes with a time limit.
func extractTimed(t *testing.T, data []byte, limit time.Duration) ([]string, error) {
	t.Helper()
	start := time.Now()
	pages, _, _, err := ExtractBytes(data)
	if el := time.Since(start); el > limit {
		t.Errorf("took %v (limit %v)", el, limit)
	}
	return pages, err
}

func wantErrClass(t *testing.T, err error, class string) {
	t.Helper()
	if errClass(err) != class {
		t.Fatalf("got error %v, want a %s", err, class)
	}
}

func TestToUnicodeRangeOverflow(t *testing.T) {
	cmap := []byte("begincmap\n1 beginbfrange\n<0000000000000000> <7FFFFFFFFFFFFFFF> <00000041>\nendbfrange\nendcmap\n")
	objs := []pdfObj{objCatalog, objPages, pageObj("", ""), {4, streamObj(textA, "")},
		{5, []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>")},
		{6, streamObj(cmap, "")}}
	_, err := extractTimed(t, buildPDF(objs, 1, ""), 2*time.Second)
	wantErrClass(t, err, "LimitReachedError")
}

func TestDecompressionLimits(t *testing.T) {
	big := append(append([]byte(nil), textA...), bytes.Repeat([]byte(" "), maxDecodedOutput)...)
	cases := map[string][]byte{}
	// FlateDecode inside FlateDecode: a few KB that would inflate past 75 MB
	cases["flate chain"] = buildPDF([]pdfObj{objCatalog, objPages, pageObj("", ""),
		{4, streamObj(zlibBytes(zlibBytes(big)), "/Filter [/FlateDecode /FlateDecode]")}, objFont}, 1, "")
	rl := bytes.Repeat([]byte{129, ' '}, maxDecodedOutput/128+2)
	cases["run length"] = buildPDF([]pdfObj{objCatalog, objPages, pageObj("[4 0 R 6 0 R]", ""),
		{4, streamObj(textA, "")}, objFont, {6, streamObj(rl, "/Filter /RunLengthDecode")}}, 1, "")
	cases["predictor"] = buildPDF([]pdfObj{objCatalog, objPages, pageObj("", ""),
		{4, streamObj(zlibBytes(textA), "/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 250000 /Colors 16 /BitsPerComponent 16 >>")},
		objFont}, 1, "")
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := extractTimed(t, data, 10*time.Second)
			wantErrClass(t, err, "LimitReachedError")
		})
	}
}

func TestContentsArrayLimit(t *testing.T) {
	for _, n := range []int{10000, 10001} {
		contents := "[" + strings.Repeat("4 0 R ", n) + "]"
		data := buildPDF([]pdfObj{objCatalog, objPages, pageObj(contents, ""), {4, streamObj(textA, "")}, objFont}, 1, "")
		pages, err := extractTimed(t, data, 5*time.Second)
		if n == 10000 {
			if err != nil || len(pages) != 1 || pages[0] != strings.Repeat("A", n) {
				t.Errorf("10000 entries: %v %d", err, len(pages))
			}
			continue
		}
		wantErrClass(t, err, "LimitReachedError")
	}
}

// A file whose xref realignment makes no progress must still terminate.
func TestXrefRealignmentNoProgress(t *testing.T) {
	data := []byte("%PDF-1.4\nxref\n0 999999\n0000000000 65535 f \nstartxref\n9\n%%EOF" + strings.Repeat("\n", 40))
	_, err := extractTimed(t, data, 2*time.Second)
	wantErrClass(t, err, "PdfReadError")
}

func formChain(depth int, fanout bool) []byte {
	objs := []pdfObj{objCatalog, objPages,
		pageObj("", "<< /Font << /F1 5 0 R >> /XObject << /X 10 0 R >> >>"),
		{4, streamObj([]byte("/X Do"), "")}, objFont}
	for i := 0; i < depth; i++ {
		n := 10 + i
		body, res := []byte("/X Do"), fmt.Sprintf("<< /XObject << /X %d 0 R >> >>", n+1)
		if fanout {
			body = []byte("/X Do /X Do")
		}
		if i == depth-1 {
			body, res = []byte("BT /F1 12 Tf (x) Tj ET"), "<< /Font << /F1 5 0 R >> >>"
		}
		objs = append(objs, pdfObj{n, streamObj(body, "/Type /XObject /Subtype /Form /BBox [0 0 1 1] /Resources "+res)})
	}
	return buildPDF(objs, 1, "")
}

func TestFormXObjectDepth(t *testing.T) {
	for depth, want := range map[int]string{489: "x", 490: "", 300000: ""} {
		pages, err := extractTimed(t, formChain(depth, false), 20*time.Second)
		if err != nil || len(pages) != 1 || pages[0] != want {
			t.Errorf("chain of %d forms: %q %v, want %q", depth, pages, err, want)
		}
	}
}

// A form fan-out of 2^30 runs never finishes; the work budget stops it.
func TestFormXObjectFanoutBudget(t *testing.T) {
	_, err := extractTimed(t, formChain(30, true), 30*time.Second)
	wantErrClass(t, err, "LimitReachedError")
}

func TestPageTreeDepth(t *testing.T) {
	build := func(depth int) []byte {
		objs := []pdfObj{{1, []byte("<< /Type /Catalog /Pages 10 0 R >>")}, {4, streamObj([]byte("BT /F1 12 Tf (p) Tj ET"), "")}, objFont}
		for i := 0; i < depth; i++ {
			objs = append(objs, pdfObj{10 + i, []byte(fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", 11+i))})
		}
		objs = append(objs, pdfObj{10 + depth, []byte("<< /Type /Page /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")})
		return buildPDF(objs, 1, "")
	}
	pages, err := extractTimed(t, build(982), 5*time.Second)
	if err != nil || len(pages) != 1 || pages[0] != "p" {
		t.Errorf("982 levels: %q %v", pages, err)
	}
	pages, err = extractTimed(t, build(983), 5*time.Second)
	if err != nil || len(pages) != 1 || pages[0] != "" {
		t.Errorf("983 levels: %q %v", pages, err) // /Resources fails to parse
	}
	_, err = extractTimed(t, build(300000), 20*time.Second)
	wantErrClass(t, err, "PdfReadError")
}

func TestNestedArrays(t *testing.T) {
	content := func(n int) []byte {
		return []byte("BT /F1 12 Tf " + strings.Repeat("[", n) + strings.Repeat("]", n) + " (A) Tj ET")
	}
	for n, class := range map[int]string{496: "AttributeError", 497: "RecursionError", 1000000: "RecursionError"} {
		data := buildPDF([]pdfObj{objCatalog, objPages, pageObj("", ""), {4, streamObj(content(n), "")}, objFont}, 1, "")
		_, err := extractTimed(t, data, 5*time.Second)
		if errClass(err) != class {
			t.Errorf("%d nested arrays: %v, want %s", n, err, class)
		}
	}
	// 500 comment lines in front of a dictionary value are fine
	res := "<< /Font " + strings.Repeat("% c\n", 500) + "<< /F1 5 0 R >> >>"
	data := buildPDF([]pdfObj{objCatalog, objPages, pageObj("", res), {4, streamObj(textA, "")}, objFont}, 1, "")
	pages, err := extractTimed(t, data, 5*time.Second)
	if err != nil || len(pages) != 1 || pages[0] != "A" {
		t.Errorf("comment lines: %q %v", pages, err)
	}
}

func TestReferenceBodies(t *testing.T) {
	// An object whose body is "N 0 R": the first lookup of it raises
	// AttributeError (caught around /Contents and fonts).
	contentsHop := buildPDF([]pdfObj{objCatalog, objPages, pageObj("7 0 R", ""), {7, []byte("4 0 R")},
		{4, streamObj(textA, "")}, objFont}, 1, "")
	fontHop := buildPDF([]pdfObj{objCatalog, objPages, pageObj("", "<< /Font << /F1 7 0 R >> >>"),
		{7, []byte("5 0 R")}, {4, streamObj(textA, "")}, objFont}, 1, "")
	for name, tc := range map[string]struct {
		data []byte
		want string
	}{"contents": {contentsHop, ""}, "font": {fontHop, "�"}} {
		pages, err := extractTimed(t, tc.data, 2*time.Second)
		if err != nil || len(pages) != 1 || pages[0] != tc.want {
			t.Errorf("%s: %q %v, want %q", name, pages, err, tc.want)
		}
	}
}

func TestManyObjectsAndMissingReferences(t *testing.T) {
	var objs []pdfObj
	for i := 0; i < 200000; i++ {
		objs = append(objs, pdfObj{10 + i, []byte(fmt.Sprintf("<< /K %d >>", i))})
	}
	pages, err := extractTimed(t, simplePDF(objs...), 15*time.Second)
	if err != nil || len(pages) != 1 || pages[0] != "A" {
		t.Errorf("many objects: %q %v", pages, err)
	}
	// thousands of references to one missing object: one file scan, not one per reference
	kids := "[3 0 R" + strings.Repeat(" 999999 0 R", 20000) + "]"
	data := buildPDF([]pdfObj{objCatalog, {2, []byte("<< /Type /Pages /Kids " + kids + " /Count 1 >>")}, pageObj("", ""),
		{4, streamObj(textA, "")}, objFont, {6, streamObj(bytes.Repeat([]byte("x"), 2_000_000), "")}}, 1, "")
	pages, err = extractTimed(t, data, 10*time.Second)
	if err != nil || len(pages) != 1 || pages[0] != "A" {
		t.Errorf("missing references: %q %v", pages, err)
	}
}

func TestRootRecoveryLimit(t *testing.T) {
	// no /Catalog anywhere and a /Size above the /Root recovery limit
	objs := []pdfObj{{1, []byte("<< /Type /NotACatalog >>")}, {2, []byte("<< /K 1 >>")}}
	data := buildPDF(objs, 1, "")
	data = bytes.Replace(data, []byte("/Size 3"), []byte("/Size 20000"), 1)
	_, err := extractTimed(t, data, 10*time.Second)
	wantErrClass(t, err, "LimitReachedError")
}

func TestDeterministicAverageWidth(t *testing.T) {
	// CID widths with floats and no space width: the default width is an
	// average that must be summed in insertion order.
	desc := "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /X /CIDSystemInfo << /Registry (A) /Ordering (I) /Supplement 0 >> " +
		"/W [1 [0.1 0.2 0.30000000000000004 1e-17 700.7 3.3333333 5.5 9.25 11.125 0.7]] >>"
	objs := []pdfObj{objCatalog, objPages, pageObj("", ""),
		{4, streamObj([]byte("BT /F1 12 Tf <0001000200030004> Tj 5 0 Td <0005> Tj ET"), "")},
		{5, []byte("<< /Type /Font /Subtype /Type0 /BaseFont /X /Encoding /Identity-H /DescendantFonts [6 0 R] >>")},
		{6, []byte(desc)}}
	data := buildPDF(objs, 1, "")
	want, _, _, wantErr := ExtractBytes(data)
	for i := 0; i < 20; i++ {
		got, _, _, err := ExtractBytes(data)
		if fmt.Sprint(got, err) != fmt.Sprint(want, wantErr) {
			t.Fatalf("nondeterministic: %q %v vs %q %v", got, err, want, wantErr)
		}
	}
}
