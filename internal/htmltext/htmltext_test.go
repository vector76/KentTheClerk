package htmltext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type result struct {
	Lines []string `json:"lines"`
	Title *string  `json:"title"`
	Error string   `json:"error,omitempty"`
	Msg   string   `json:"message,omitempty"`
}

type namedInput struct {
	Name string `json:"name"`
	Raw  []byte `json:"raw"`
}

func goResult(raw []byte) result {
	lines, title, has, err := ExtractErr(raw)
	switch e := err.(type) {
	case nil:
	case *AssertionError:
		return result{Error: "AssertionError", Msg: e.Msg}
	case *ValueError:
		return result{Error: "ValueError", Msg: e.Msg}
	default:
		return result{Error: fmt.Sprintf("%T", err), Msg: err.Error()}
	}
	r := result{Lines: lines}
	if has {
		r.Title = &title
	}
	return r
}

func corpus(t *testing.T) []namedInput {
	var in []namedInput
	for _, c := range handCases {
		in = append(in, namedInput{c.name, []byte(c.src)})
	}
	for _, c := range byteCases {
		in = append(in, namedInput{c.name, c.raw})
	}
	files, err := filepath.Glob(filepath.Join("testdata", "cases", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		in = append(in, namedInput{"file:" + filepath.Base(f), b})
	}
	return in
}

// deepInputs once overflowed the stack of the recursive (continuation-passing) matcher.
func deepInputs() []namedInput {
	return []namedInput{
		{"deep:400k-attrs", []byte("<p>x</p><a" + strings.Repeat(" b", 400_000) + ">hi</a>")},
		{"deep:2.3M-spaces", []byte("<a b" + strings.Repeat(" ", 2_300_000) + ">")},
		{"deep:2.3M-slashes", []byte("<a b" + strings.Repeat("/", 2_300_000) + ">")},
		{"deep:unclosed-quotes", []byte("<a" + strings.Repeat(" b='x", 100_000) + ">y")},
		{"deep:eq-runs", []byte("<a" + strings.Repeat(" b=='x", 100_000) + ">y")},
	}
}

// digitInputs exercise the 4300-digit limit on decimal character references.
func digitInputs() []namedInput {
	d := func(n int) string { return strings.Repeat("7", n) }
	return []namedInput{
		{"digits:text-4300", []byte("<p>&#" + d(4300) + ";</p>")},
		{"digits:text-4301", []byte("<p>&#" + d(4301) + ";</p>")},
		{"digits:text-4301-nosemi", []byte("<p>&#" + d(4301) + " x</p>")},
		{"digits:zeros-4301", []byte("<p>&#" + strings.Repeat("0", 4300) + "65;</p>")},
		{"digits:text-at-end", []byte("x &#" + d(5000))},
		{"digits:hex-5000", []byte("<p>&#x" + strings.Repeat("f", 5000) + ";</p>")},
		{"digits:title", []byte("<title>&#" + d(4400) + ";</title>")},
		{"digits:first-wins", []byte("&#" + d(4500) + "; &#" + d(4400) + ";<p>")},
		{"digits:attr-dq", []byte("<a href=\"&#" + d(4301) + ";\">x</a>")},
		{"digits:attr-sq", []byte("<a href='x&#" + d(4301) + "'>x</a>")},
		{"digits:attr-bare", []byte("<a href=&#" + d(4301) + ">x</a>")},
		{"digits:attr-4300", []byte("<a href=\"&#" + d(4300) + ";\">x</a>")},
		{"digits:attr-junk-tag", []byte("<a href=\"&#" + d(4301) + "\" \"q>x</a>")},
		{"digits:attr-incomplete", []byte("x<a href=\"&#" + d(4301) + "\"")},
		{"digits:attr-eqeq", []byte("<a href=='&#" + d(4301) + ">x</a>")},
		{"digits:script", []byte("<script>&#" + d(4301) + ";</script>ok")},
		{"digits:comment", []byte("<!-- &#" + d(4301) + "; -->ok")},
		{"digits:endtag", []byte("</a x='&#" + d(4301) + "'>ok")},
	}
}

func show(r result) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func compare(t *testing.T, inputs []namedInput, want []result) int {
	t.Helper()
	bad := 0
	for i, in := range inputs {
		got := goResult(in.Raw)
		if !reflect.DeepEqual(normalize(got), normalize(want[i])) {
			bad++
			if bad <= 20 {
				t.Errorf("%s: input %q\n  want: %s\n  got:  %s", in.Name, in.Raw, show(want[i]), show(got))
			}
		}
	}
	return bad
}

// normalize treats a nil and an empty line list alike (JSON round trip).
func normalize(r result) result {
	if r.Lines == nil {
		r.Lines = []string{}
	}
	return r
}

type goldenCase struct {
	Name string `json:"name"`
	Raw  []byte `json:"raw"`
	Want result `json:"want"`
}

const goldenPath = "testdata/golden.json"

// TestGolden checks the extractor against the expected results recorded in
// testdata/golden.json.
func TestGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.FromSlash(goldenPath))
	if err != nil {
		t.Fatalf("no golden file: %v", err)
	}
	var g []goldenCase
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	inputs := make([]namedInput, len(g))
	want := make([]result, len(g))
	for i, c := range g {
		inputs[i] = namedInput{c.Name, c.Raw}
		want[i] = c.Want
	}
	// The golden inputs are exactly the hand-written cases and the files in testdata/cases.
	if cases := corpus(t); !reflect.DeepEqual(inputs, cases) {
		t.Errorf("golden inputs (%d) differ from cases_test.go and testdata/cases (%d)", len(inputs), len(cases))
	}
	if bad := compare(t, inputs, want); bad > 0 {
		t.Fatalf("%d of %d golden cases differ", bad, len(inputs))
	}
}

func TestExtractPanicsOnAssertionError(t *testing.T) {
	defer func() {
		if _, ok := recover().(*AssertionError); !ok {
			t.Fatal("want *AssertionError panic")
		}
	}()
	Extract([]byte("a<![foo[x]]>b"))
}

func TestExtractSimple(t *testing.T) {
	lines, title, has := Extract([]byte("<title> T </title><h1>Head</h1><p>a &amp; b</p>"))
	want := []string{"# Head", "", "a & b"}
	if !reflect.DeepEqual(lines, want) || title != "T" || !has {
		t.Fatalf("got %q %q %v", lines, title, has)
	}
}

// TestTitleIsTheHeadTitleNotAnSVGLabel: the title inside <head> is the document's title,
// although the rest of the head is not text; a <title> inside inline SVG labels an image and
// is neither the title nor text; and a title in a template or noscript inside the head is not
// the document's either.
func TestTitleIsTheHeadTitleNotAnSVGLabel(t *testing.T) {
	cases := []struct {
		src       string
		wantTitle string
		wantHas   bool
		wantLines []string
	}{
		{"<head><title>T</title></head><p>x</p>", "T", true, []string{"x"}},
		{"<html><head><meta charset=\"utf-8\"><title> My Page </title><style>p{}</style></head><body><p>x</p></body></html>",
			"My Page", true, []string{"x"}},
		{"<head><title>Docs</title></head><body><button><svg><title>Search</title><path d=\"M0\"/></svg></button><p>x</p></body>",
			"Docs", true, []string{"x"}},
		{"<body><svg viewBox=\"0 0 1 1\"><title>Menu</title></svg><p>x</p></body>", "", false, []string{"x"}},
		{"<head><template><title>X</title></template><noscript><title>N</title></noscript><title>T</title></head><p>x</p>",
			"T", true, []string{"x"}},
	}
	for _, c := range cases {
		lines, title, has, err := ExtractErr([]byte(c.src))
		if err != nil || title != c.wantTitle || has != c.wantHas || !reflect.DeepEqual(lines, c.wantLines) {
			t.Errorf("%q: got title %q %v, lines %q, %v; want title %q %v, lines %q",
				c.src, title, has, lines, err, c.wantTitle, c.wantHas, c.wantLines)
		}
	}
}

// TestTitlePiecesKeepTheSpaceBetweenThemAndTheFirstTitleWins: whitespace between pieces of
// title text that a tag or a lone "<" separates becomes one space, as in a heading, instead of
// the pieces running together; and the first title is the document's, so a later title
// element is not appended to it.
func TestTitlePiecesKeepTheSpaceBetweenThemAndTheFirstTitleWins(t *testing.T) {
	cases := []struct {
		src       string
		wantTitle string
		wantLines []string
	}{
		{"<title>a <b>b</b></title>", "a b", []string{""}},
		{"<title>a<b> b</b></title>", "a b", []string{""}},
		{"<title>a <!-- c --> b</title>", "a b", []string{""}},
		{"<title>a<b>b</b>c</title>", "abc", []string{""}},
		{"<title>a < b</title>", "a < b", []string{""}},
		{"<title> My  Page </title>", "My  Page", []string{""}},
		{"<body><title>A</title>text<title> B </title></body>", "A", []string{"text"}},
		{"<head><title>Docs</title></head><body><title>Other</title><p>x</p></body>", "Docs", []string{"x"}},
		{"<title> </title><title>T</title>", "T", []string{""}},
		{"<title>A <title>B</title>", "A B", []string{""}},
	}
	for _, c := range cases {
		lines, title, has, err := ExtractErr([]byte(c.src))
		if err != nil || title != c.wantTitle || !has || !reflect.DeepEqual(lines, c.wantLines) {
			t.Errorf("%q: got title %q %v, lines %q, %v; want title %q, lines %q",
				c.src, title, has, lines, err, c.wantTitle, c.wantLines)
		}
	}
}

// TestHeadEndsWhereTheBodyBegins: a head whose end tag is left out ends at the first tag or
// text that belongs in the body, as it does in a browser, and a <head> start tag after the
// body has begun is ignored, so the text after either is kept; a complete head is still not
// text.
func TestHeadEndsWhereTheBodyBegins(t *testing.T) {
	cases := []struct {
		src       string
		wantTitle string
		wantHas   bool
		wantLines []string
	}{
		{"<p>a</p><head><p>b</p>", "", false, []string{"a", "", "b"}},
		{"visible<head>hidden", "", false, []string{"visiblehidden"}},
		{"<html><head><title>T</title><meta charset=utf-8><body><p>text</p></body></html>",
			"T", true, []string{"text"}},
		{"<head><title>T</title><link rel=x><div>x</div></head><p>y</p>", "T", true, []string{"x", "", "y"}},
		{"<head><title>T</title>Stray text<style>p{}</style></head><p>y</p>", "T", true, []string{"Stray text", "", "y"}},
		{"<p>a</p><head><style>p{}</style></head><p>b</p>", "", false, []string{"a", "", "b"}},
		{"<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n<title>T</title>\n<script>x</script>\n</head>\n<body><p>x</p></body></html>",
			"T", true, []string{"x"}},
	}
	for _, c := range cases {
		lines, title, has, err := ExtractErr([]byte(c.src))
		if err != nil || title != c.wantTitle || has != c.wantHas || !reflect.DeepEqual(lines, c.wantLines) {
			t.Errorf("%q: got title %q %v, lines %q, %v; want title %q %v, lines %q",
				c.src, title, has, lines, err, c.wantTitle, c.wantHas, c.wantLines)
		}
	}
}

// TestHeadingKeepsSpaceBetweenInlinePieces: whitespace between two pieces of heading text
// that a tag or comment separates stays one space, and so does whitespace between pieces of
// text after a heading that is never closed.
func TestHeadingKeepsSpaceBetweenInlinePieces(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{"<h1>Quarterly <em>Report</em></h1>", []string{"# Quarterly Report"}},
		{"<h2>Do <code>not</code> run</h2>", []string{"## Do not run"}},
		{"<h2>Using <code>kb ingest</code></h2>", []string{"## Using kb ingest"}},
		{"<h2>Heading with <!-- c --> inside &amp; <span>span</span></h2>", []string{"## Heading with inside & span"}},
		{"<h1>  Title <b>bold</b>  </h1>", []string{"# Title bold"}},
		{"<h3>a<i> b </i>c</h3>", []string{"### a b c"}},
		{"<h3><b>x</b>y</h3>", []string{"### xy"}},
		{"<h2>a <td>b</td></h2>", []string{"## a | b"}},
		{"<h2>x <p>Hello <b>world</b></p>", []string{"## x", "", "Hello world"}},
	}
	for _, c := range cases {
		lines, _, _, err := ExtractErr([]byte(c.src))
		if err != nil || !reflect.DeepEqual(lines, c.want) {
			t.Errorf("%q: got %q, %v; want %q", c.src, lines, err, c.want)
		}
	}
}

// TestStrayByteLeavesTheRestOfTheUTF8Text: a byte that is not valid UTF-8, such as a stray
// legacy byte pasted into a UTF-8 page or a sequence cut short at the end, is read alone, and
// the characters around it that are valid UTF-8 keep their meaning, a leading BOM included.
func TestStrayByteLeavesTheRestOfTheUTF8Text(t *testing.T) {
	cases := []struct {
		src  string
		want string // the start of the first line
	}{
		{"<p>caf\xc3\xa9 \xe9t\xe9</p>", "café été"},
		{"\xef\xbb\xbf<p>caf\xc3\xa9 \xe9</p>", "café é"},
		{"\xef\xbb\xbf<p>caf\xe9</p>", "café"},
		{"<p>caf\xc3\xa9 \xe2\x80\x94 it\x92s</p>", "café — it"},
		{"<p>na\xc3\xafve \xe2\x80", "naïve "},
	}
	for _, c := range cases {
		lines, _, _, err := ExtractErr([]byte(c.src))
		if err != nil || len(lines) == 0 || !strings.HasPrefix(lines[0], c.want) {
			t.Errorf("%q: got %q, %v; want a first line starting %q", c.src, lines, err, c.want)
		}
	}
}

// TestDeepInputs: these inputs used to overflow the goroutine stack (fatal, unrecoverable)
// in the recursive matcher.
func TestDeepInputs(t *testing.T) {
	want := map[string][]string{
		"deep:400k-attrs":      {"x", "", "hi"},
		"deep:2.3M-spaces":     {""},
		"deep:2.3M-slashes":    {""},
		"deep:unclosed-quotes": {"y"},
		"deep:eq-runs":         {"y"},
	}
	for _, in := range deepInputs() {
		lines, _, _, err := ExtractErr(in.Raw)
		if err != nil || !reflect.DeepEqual(lines, want[in.Name]) {
			t.Errorf("%s: got %q, %v; want %q", in.Name, lines, err, want[in.Name])
		}
	}
}

func TestDigitLimitValueError(t *testing.T) {
	for _, in := range digitInputs() {
		_, _, _, err := ExtractErr(in.Raw)
		ve, isVE := err.(*ValueError)
		wantErr := strings.Contains(in.Name, "4301") || strings.Contains(in.Name, "at-end") ||
			strings.Contains(in.Name, "title") || strings.Contains(in.Name, "first-wins") ||
			strings.Contains(in.Name, "attr-") && !strings.Contains(in.Name, "4300") && !strings.Contains(in.Name, "incomplete")
		if wantErr != isVE {
			t.Errorf("%s: err = %v, want ValueError: %v", in.Name, err, wantErr)
		}
		if in.Name == "digits:first-wins" && isVE {
			want := "Exceeds the limit (4300 digits) for integer string conversion: value has 4500 digits"
			if ve.Msg != want {
				t.Errorf("message %q, want %q", ve.Msg, want)
			}
		}
	}
	defer func() {
		if _, ok := recover().(*ValueError); !ok {
			t.Fatal("want *ValueError panic from Extract")
		}
	}()
	Extract([]byte("&#" + strings.Repeat("1", 4301) + ";<p>"))
}
