package htmltext

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.json from Python")

const fuzzCount = 20000

type result struct {
	Lines []string `json:"lines"`
	Title *string  `json:"title"`
	Error string   `json:"error,omitempty"`
}

type namedInput struct {
	Name string `json:"name"`
	Raw  []byte `json:"raw"`
}

func goResult(raw []byte) result {
	lines, title, has, err := ExtractErr(raw)
	if err != nil {
		return result{Error: "AssertionError"}
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

// largeInputs are big documents for the differential test (not stored in golden.json).
func largeInputs(files []namedInput) []namedInput {
	var all []byte
	for _, f := range files {
		if strings.HasPrefix(f.Name, "file:") && utf8.Valid(f.Raw) {
			all = append(all, f.Raw...)
		}
	}
	return []namedInput{
		{"large:files-x300", bytes.Repeat(all, 300)},
		{"large:many-attrs", []byte("<div " + strings.Repeat("a=1 b='2' c ", 30000) + ">x</div>y")},
		{"large:unclosed-quote", []byte("<p a='" + strings.Repeat("word ", 50000) + "<p>z")},
		{"large:long-text", []byte(strings.Repeat("lorem ipsum &amp; dolor sit<br>", 40000) + "&amp")},
	}
}

// fuzzInputs returns deterministic random tag soup.
func fuzzInputs(n int) []namedInput {
	rng := rand.New(rand.NewSource(20261007))
	tags := []string{"p", "div", "br", "li", "ul", "ol", "tr", "table", "section", "article",
		"header", "footer", "pre", "blockquote", "hr", "main", "nav", "aside", "dd", "dt",
		"figure", "figcaption", "script", "style", "noscript", "head", "template", "title",
		"h1", "h2", "h3", "h4", "h5", "h6", "td", "th", "a", "b", "span", "P", "SCRIPT",
		"Style", "TITLE", "H2", "Td", "x"}
	toks := []string{"<", ">", "/", "!", "-", "--", "?", "&", "#", ";", "=", "\"", "'", " ",
		"  ", "\n", "\t", "\r", "\f", "\x0b", "\x00", "a", "b", "x", "X", "1", "9", "0", "[", "]",
		"CDATA", "doctype", "DOCTYPE", "if", "endif", "amp", "lt", "nbsp", "copy", "notin",
		"x41", "#65", "#x", " ", "　", " ", "\u0085", "K", "ſ",
		"İ", "ı", "é", "\U0001F600", "text", "word ", "</", "<!--", "-->",
		"<!", "<?", "<![", "]]>", "/>", "&amp;", "&#", "&#x", "&lt", "&nbsp;"}
	attrPieces := []string{" ", "a", "href", "=", "==", " = ", "'v'", "\"v\"", "'", "\"", "x>y",
		"/", "&amp;", "&", "\t", "\n", " ", "v", "'a>b'", "\"<\""}
	texts := []string{"Hello", " world ", "&amp;", "&nbsp;", "&#169;", "&copy", "&notit;",
		"a < b", "5 > 3", "x&y", " \t\n ", "　 ", "&#x1F600;", "&#0;", "&lt;p&gt;",
		"R&D;", "&", "&#", "tail"}
	var out []namedInput
	for k := 0; k < n; k++ {
		if k%2 == 1 {
			// structured: elements with attributes, text, comments, script bodies
			var sb strings.Builder
			parts := 1 + rng.Intn(25)
			for i := 0; i < parts; i++ {
				switch rng.Intn(9) {
				case 0, 1, 2:
					tag := tags[rng.Intn(len(tags))]
					sb.WriteString("<" + tag)
					for a := rng.Intn(4); a > 0; a-- {
						sb.WriteString(" ")
						for m := 1 + rng.Intn(4); m > 0; m-- {
							sb.WriteString(attrPieces[rng.Intn(len(attrPieces))])
						}
					}
					sb.WriteString([]string{">", ">", "/>", " />", " >", ""}[rng.Intn(6)])
				case 3:
					sb.WriteString("</" + tags[rng.Intn(len(tags))] + []string{">", " >", "", " x>", "\n>"}[rng.Intn(5)])
				case 4:
					sb.WriteString([]string{"<!-- c -->", "<!--x--!>", "<!-- a -- >", "<!DOCTYPE html>",
						"<![CDATA[q]]>", "<![if x]>", "<![endif]>", "<?pi?>", "<!x>", "</>", "<",
						"<!-", "<!--", "<![", "<![x", "<![CDATA["}[rng.Intn(16)])
				default:
					sb.WriteString(texts[rng.Intn(len(texts))])
				}
			}
			out = append(out, namedInput{fmt.Sprintf("fuzz#%d", k), []byte(sb.String())})
			continue
		}
		var sb strings.Builder
		length := 1 + rng.Intn(40)
		if rng.Intn(10) == 0 {
			length += rng.Intn(200)
		}
		for i := 0; i < length; i++ {
			switch r := rng.Intn(100); {
			case r < 22:
				// a tag-shaped token
				tag := tags[rng.Intn(len(tags))]
				switch rng.Intn(6) {
				case 0, 1:
					sb.WriteString("<" + tag + ">")
				case 2:
					sb.WriteString("</" + tag + ">")
				case 3:
					sb.WriteString("<" + tag)
				case 4:
					sb.WriteString("</" + tag)
				default:
					sb.WriteString(tag)
				}
			default:
				sb.WriteString(toks[rng.Intn(len(toks))])
			}
		}
		raw := []byte(sb.String())
		switch rng.Intn(25) {
		case 0:
			raw = append([]byte("\xef\xbb\xbf"), raw...)
		case 1:
			raw = append(raw, 0xE9) // invalid UTF-8: latin-1 path
		case 2:
			pos := rng.Intn(len(raw) + 1)
			raw = append(raw[:pos:pos], append([]byte{0xA0}, raw[pos:]...)...)
		}
		out = append(out, namedInput{fmt.Sprintf("fuzz#%d", k), raw})
	}
	return out
}

// findPython returns a CPython 3.13.1 interpreter, or "" (the port targets that exact
// html.parser; other versions parse some malformed markup differently).
func findPython(t *testing.T) string {
	cands := []string{os.Getenv("HTMLTEXT_PYTHON"), "python", "python3", "py"}
	for _, c := range cands {
		if c == "" {
			continue
		}
		path, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "-I", "-c", "import sys; print('%d.%d.%d' % sys.version_info[:3])").Output()
		if err != nil {
			continue
		}
		v := strings.TrimSpace(string(out))
		if v == "3.13.1" {
			return path
		}
		t.Logf("%s is Python %s, not 3.13.1", path, v)
	}
	return ""
}

func runPython(t *testing.T, py string, inputs []namedInput) []result {
	t.Helper()
	enc := make([]string, len(inputs))
	for i, in := range inputs {
		enc[i] = base64.StdEncoding.EncodeToString(in.Raw)
	}
	data, _ := json.Marshal(enc)
	f := filepath.Join(t.TempDir(), "inputs.json")
	if err := os.WriteFile(f, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(py, "-I", filepath.Join("testdata", "pyextract.py"), f)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, stderr.String())
	}
	var res []result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decoding python output: %v", err)
	}
	if len(res) != len(inputs) {
		t.Fatalf("python returned %d results for %d inputs", len(res), len(inputs))
	}
	return res
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
				t.Errorf("%s: input %q\n  python: %s\n  go:     %s", in.Name, in.Raw, show(want[i]), show(got))
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

func TestDifferential(t *testing.T) {
	py := findPython(t)
	if py == "" {
		t.Skip("CPython 3.13.1 not found (set HTMLTEXT_PYTHON); skipping differential test")
	}
	inputs := corpus(t)
	all := append(append([]namedInput(nil), inputs...), largeInputs(inputs)...)
	want := runPython(t, py, all)
	if bad := compare(t, all, want); bad > 0 {
		t.Fatalf("%d of %d corpus cases differ", bad, len(all))
	}
	t.Logf("%d corpus cases (%d large) identical to Python", len(all), len(all)-len(inputs))
	if *update {
		writeGolden(t, inputs, want[:len(inputs)])
	}
}

func TestDifferentialFuzz(t *testing.T) {
	py := findPython(t)
	if py == "" {
		t.Skip("CPython 3.13.1 not found (set HTMLTEXT_PYTHON); skipping differential fuzz test")
	}
	n := fuzzCount
	if testing.Short() {
		n = 500
	}
	inputs := fuzzInputs(n)
	want := runPython(t, py, inputs)
	if bad := compare(t, inputs, want); bad > 0 {
		t.Fatalf("%d of %d fuzz inputs differ", bad, len(inputs))
	}
	errs, titled, nonempty := 0, 0, 0
	for _, w := range want {
		switch {
		case w.Error != "":
			errs++
		case w.Title != nil:
			titled++
		}
		if len(w.Lines) > 1 || len(w.Lines) == 1 && w.Lines[0] != "" {
			nonempty++
		}
	}
	t.Logf("%d fuzz inputs identical to Python (%d raise AssertionError, %d with a title, %d with text)",
		len(inputs), errs, titled, nonempty)
}

type goldenCase struct {
	Name string `json:"name"`
	Raw  []byte `json:"raw"`
	Want result `json:"want"`
}

const goldenPath = "testdata/golden.json"

func writeGolden(t *testing.T, inputs []namedInput, want []result) {
	g := make([]goldenCase, len(inputs))
	for i, in := range inputs {
		g[i] = goldenCase{in.Name, in.Raw, want[i]}
	}
	data, err := json.MarshalIndent(g, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.FromSlash(goldenPath), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", goldenPath)
}

// TestGolden checks the corpus against Python results recorded in testdata/golden.json
// (go test -run TestDifferential -update), so it runs without Python.
func TestGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.FromSlash(goldenPath))
	if err != nil {
		t.Skipf("no golden file: %v", err)
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
	if bad := compare(t, inputs, want); bad > 0 {
		t.Fatalf("%d of %d golden cases differ", bad, len(inputs))
	}
}

func TestExtractPanicsLikePython(t *testing.T) {
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
