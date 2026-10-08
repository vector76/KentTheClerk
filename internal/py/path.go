package py

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Windows is true where pathlib uses Windows path rules (case-insensitive globbing and sorting,
// backslash separators) and where text written in text mode gets CRLF line endings.
var Windows = runtime.GOOS == "windows"

// PathStr is str(Path(s)): separators normalised, repeated separators and "." parts dropped, a
// trailing separator removed. ".." is kept, as pathlib keeps it.
func PathStr(s string) string {
	if s == "" {
		return "."
	}
	sep := "/"
	if Windows {
		sep = `\`
		s = strings.ReplaceAll(s, "/", `\`)
	}
	prefix := ""
	if Windows {
		switch {
		case strings.HasPrefix(s, `\\`):
			// UNC: \\server\share is the anchor
			rest := s[2:]
			parts := strings.SplitN(rest, `\`, 3)
			if len(parts) == 1 { // a server with no share
				return `\\` + parts[0]
			}
			if len(parts) == 2 && parts[1] == "" { // \\server\ with an empty share
				return `\\` + parts[0] + `\`
			}
			if len(parts) >= 2 {
				prefix = `\\` + parts[0] + `\` + parts[1] + `\`
				if len(parts) == 3 {
					s = parts[2]
				} else {
					s = ""
				}
			}
		case len(s) >= 2 && s[1] == ':':
			prefix = s[:2]
			s = s[2:]
			if strings.HasPrefix(s, `\`) {
				prefix += `\`
				s = strings.TrimLeft(s, `\`)
			}
		case strings.HasPrefix(s, `\`):
			prefix = `\`
			s = strings.TrimLeft(s, `\`)
		}
	} else if strings.HasPrefix(s, "/") {
		if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
			prefix = "//"
		} else {
			prefix = "/"
		}
		s = strings.TrimLeft(s, "/")
	}
	var parts []string
	for _, p := range strings.Split(s, sep) {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	if Windows && prefix == "" && len(parts) > 0 && len(parts[0]) >= 2 && parts[0][1] == ':' {
		// a first part that looks like a drive keeps a leading "." part, as pathlib does
		parts = append([]string{"."}, parts...)
	}
	out := prefix + strings.Join(parts, sep)
	if out == "" {
		return "."
	}
	return out
}

// Resolve is Path(s).expanduser().resolve(): absolute, with symlinks resolved and, on Windows, the
// case of each existing component as stored on disk. A missing tail is appended as given.
func Resolve(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") || (Windows && strings.HasPrefix(s, `~\`)) {
		if h, err := os.UserHomeDir(); err == nil {
			s = h + s[1:]
		}
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		return PathStr(s)
	}
	return resolveAbs(abs)
}

func resolveAbs(abs string) string {
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	dir, base := filepath.Split(abs)
	dir = strings.TrimRight(dir, `\/`)
	if dir == "" || dir == abs || base == "" || filepath.Dir(abs) == abs {
		return abs
	}
	if Windows && strings.HasSuffix(dir, ":") {
		dir += `\`
	}
	return filepath.Join(resolveAbs(dir), base)
}

// Name is Path(p).name.
func Name(p string) string {
	p = strings.TrimRight(p, `/`+sepIf())
	if i := strings.LastIndexAny(p, `/`+sepIf()); i >= 0 {
		return p[i+1:]
	}
	if Windows && len(p) >= 2 && p[1] == ':' {
		return p[2:]
	}
	return p
}

func sepIf() string {
	if Windows {
		return `\`
	}
	return "/"
}

// Suffix is Path(p).suffix.
func Suffix(p string) string {
	name := Name(p)
	i := strings.LastIndexByte(name, '.')
	if 0 < i && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// Stem is Path(p).stem.
func Stem(p string) string {
	name := Name(p)
	if suf := Suffix(p); suf != "" {
		return name[:len(name)-len(suf)]
	}
	return name
}

// Splitext is os.path.splitext(p): leading dots of the last component do not start an extension.
func Splitext(p string) (string, string) {
	seps := "/"
	if Windows {
		seps = `/\`
	}
	sepIndex := strings.LastIndexAny(p, seps)
	dot := strings.LastIndexByte(p, '.')
	if dot > sepIndex {
		for k := sepIndex + 1; k < dot; k++ {
			if p[k] != '.' {
				return p[:dot], p[dot:]
			}
		}
	}
	return p, ""
}

// ReadText is Path.read_text(encoding="utf-8"): strict UTF-8, with universal newlines (\r\n and \r
// read as \n). Invalid UTF-8 raises UnicodeDecodeError, as Python does.
func ReadText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		RaiseOS(err)
	}
	return Universal(DecodeUTF8(b))
}

// DecodeUTF8 is bytes.decode("utf-8") (strict).
func DecodeUTF8(b []byte) string {
	if !utf8.Valid(b) {
		pos := 0
		for pos < len(b) {
			r, size := utf8.DecodeRune(b[pos:])
			if r == utf8.RuneError && size <= 1 {
				break
			}
			pos += size
		}
		Raise("UnicodeDecodeError", "'utf-8' codec can't decode byte 0x%02x in position %d", b[pos], pos)
	}
	return string(b)
}

// Universal translates \r\n and lone \r to \n, as reading in text mode does.
func Universal(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// TextLines is iterating a file opened in text mode: lines split at \n, \r\n or \r, each without its
// line ending.
func TextLines(path string) []string {
	s := ReadText(path)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// WriteText is Path.write_text(s, encoding="utf-8"): in text mode, so on Windows "\n" is written as
// "\r\n".
func WriteText(path, s string) {
	// Python would truncate the file and then fail to encode; refusing before opening keeps the file.
	CheckEncodable(s)
	if Windows {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	if err := os.WriteFile(path, []byte(s), 0o666); err != nil {
		RaiseOS(err)
	}
}

// RaiseOS raises the OSError Python would raise for err.
func RaiseOS(err error) {
	// str(OSError) for a file: "[Errno 2] No such file or directory: 'path'"
	typ, errno, text := "OSError", 0, ""
	switch {
	case os.IsNotExist(err):
		typ, errno, text = "FileNotFoundError", 2, "No such file or directory"
	case os.IsPermission(err):
		typ, errno, text = "PermissionError", 13, "Permission denied"
	}
	var pe *fs.PathError
	if errno != 0 && errors.As(err, &pe) {
		Raise(typ, "[Errno %d] %s: %s", errno, text, StrRepr(pe.Path))
	}
	Raise(typ, "%v", err)
}

// IsDir is Path.is_dir().
func IsDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// IsFile is Path.is_file().
func IsFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Exists is Path.exists().
func Exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Scandir lists a directory in the order os.scandir yields it. On Windows (NTFS) that is the order of
// the names compared by their upper-cased UTF-16 code units; elsewhere the order is up to the file
// system, and sorted names are used. A missing directory gives nothing.
func Scandir(dir string) []os.DirEntry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if Windows {
		sort.SliceStable(ents, func(i, j int) bool {
			return ntfsLess(ents[i].Name(), ents[j].Name())
		})
	}
	return ents
}

func ntfsLess(a, b string) bool {
	up := func(r rune) rune {
		if r > 0xffff {
			return r
		}
		return unicode.ToUpper(r)
	}
	ua := utf16.Encode([]rune(strings.Map(up, a)))
	ub := utf16.Encode([]rune(strings.Map(up, b)))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// Glob is Path(dir).glob(pattern) in pathlib's order (scandir order per directory). Matching is
// fnmatch-style per path component, case-insensitive on Windows, and "*" matches hidden names.
func Glob(dir, pattern string) []string {
	seps := "/"
	if Windows {
		seps = `/\`
	}
	parts := strings.FieldsFunc(pattern, func(r rune) bool { return strings.ContainsRune(seps, r) })
	paths := []string{dir}
	for i, part := range parts {
		last := i == len(parts)-1
		var next []string
		if part == ".." { // followed literally, as pathlib's glob does
			for _, d := range paths {
				next = append(next, d+string(filepath.Separator)+"..")
			}
			paths = next
			continue
		}
		for _, d := range paths {
			for _, e := range Scandir(d) {
				if !FnMatch(e.Name(), part) {
					continue
				}
				full := filepath.Join(d, e.Name())
				if !last && !IsDir(full) {
					continue
				}
				next = append(next, full)
			}
		}
		paths = next
	}
	if len(parts) == 0 {
		return nil
	}
	return paths
}

// SortPaths is sorted(paths) for Path objects: on Windows by their lower-cased parts.
func SortPaths(paths []string) {
	key := func(p string) []string {
		if Windows {
			p = Lower(p)
		}
		return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || (Windows && r == '\\') })
	}
	sort.SliceStable(paths, func(i, j int) bool {
		a, b := key(paths[i]), key(paths[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}

// FnMatch matches a name against one fnmatch pattern component (*, ?, [seq], [!seq]), as pathlib's
// glob does: case-insensitively on Windows.
func FnMatch(name, pat string) bool {
	toks := compileFn(pat)
	n := []rune(name)
	return matchFn(toks, n)
}

type fnTok struct {
	kind   byte // '*', '?', '[', 'c'
	c      rune
	ranges [][2]rune
	neg    bool
	never  bool
}

func compileFn(pat string) []fnTok {
	p := []rune(pat)
	var toks []fnTok
	for i := 0; i < len(p); {
		c := p[i]
		i++
		switch c {
		case '*':
			if len(toks) == 0 || toks[len(toks)-1].kind != '*' {
				toks = append(toks, fnTok{kind: '*'})
			}
		case '?':
			toks = append(toks, fnTok{kind: '?'})
		case '[':
			j := i
			if j < len(p) && p[j] == '!' {
				j++
			}
			if j < len(p) && p[j] == ']' {
				j++
			}
			for j < len(p) && p[j] != ']' {
				j++
			}
			if j >= len(p) {
				toks = append(toks, fnTok{kind: 'c', c: '['})
				continue
			}
			stuff := p[i:j]
			i = j + 1
			t := fnTok{kind: '['}
			if len(stuff) > 0 && stuff[0] == '!' {
				t.neg = true
				stuff = stuff[1:]
			}
			if len(stuff) == 0 {
				if t.neg { // "[!]": any character
					toks = append(toks, fnTok{kind: '?'})
				} else {
					toks = append(toks, fnTok{kind: '[', never: true})
				}
				continue
			}
			for k := 0; k < len(stuff); {
				if k+2 < len(stuff) && stuff[k+1] == '-' {
					if stuff[k] <= stuff[k+2] {
						t.ranges = append(t.ranges, [2]rune{stuff[k], stuff[k+2]})
					}
					k += 3
					continue
				}
				t.ranges = append(t.ranges, [2]rune{stuff[k], stuff[k]})
				k++
			}
			toks = append(toks, t)
		default:
			toks = append(toks, fnTok{kind: 'c', c: c})
		}
	}
	return toks
}

func foldEq(a, b rune) bool {
	if a == b {
		return true
	}
	if !Windows {
		return false
	}
	// re.IGNORECASE: equal under case folding, including the irregular folds (ſ, ı, µ, ς, K)
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return unicode.ToLower(a) == unicode.ToLower(b) || unicode.ToUpper(a) == unicode.ToUpper(b)
}

func inClass(t fnTok, r rune) bool {
	if t.never {
		return false
	}
	cands := []rune{r}
	if Windows {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			cands = append(cands, f)
		}
		cands = append(cands, unicode.ToLower(r), unicode.ToUpper(r))
	}
	hit := false
	for _, c := range cands {
		for _, rg := range t.ranges {
			if c >= rg[0] && c <= rg[1] {
				hit = true
			}
		}
	}
	return hit != t.neg
}

func matchFn(toks []fnTok, n []rune) bool {
	if len(toks) == 0 {
		return len(n) == 0
	}
	t := toks[0]
	switch t.kind {
	case '*':
		for k := 0; k <= len(n); k++ {
			if matchFn(toks[1:], n[k:]) {
				return true
			}
		}
		return false
	case '?':
		return len(n) > 0 && matchFn(toks[1:], n[1:])
	case '[':
		return len(n) > 0 && inClass(t, n[0]) && matchFn(toks[1:], n[1:])
	default:
		return len(n) > 0 && foldEq(t.c, n[0]) && matchFn(toks[1:], n[1:])
	}
}
