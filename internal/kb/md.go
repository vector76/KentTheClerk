package kb

// Tiny Markdown-to-HTML for the renderer: paragraphs, headings, lists, fenced code, blockquotes,
// tables, inline code/bold/italic/links, and statement ids as links. Patterns that need look-around,
// which Go's regexp lacks, are matched by hand.

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"kenttheclerk/internal/py"
)

// htmlEscape escapes &, < and > in s, and with quote also " and '.
func htmlEscape(s string, quote bool) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	if quote {
		s = strings.ReplaceAll(s, `"`, "&quot;")
		s = strings.ReplaceAll(s, "'", "&#x27;")
	}
	return s
}

var (
	mdCode    = regexp.MustCompile("`([^`]+)`")
	mdBold    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdAbsLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)` + py.SpaceClass + `]+)\)`)
	mdRelLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)` + py.SpaceClass + `]+)\)`)
	mdHeading = regexp.MustCompile(`^(#{1,6})[` + py.SpaceClass + `]+(.*)$`)
	mdBullet  = regexp.MustCompile(`^[` + py.SpaceClass + `]*[-*][` + py.SpaceClass + `]+`)
	mdNumber  = regexp.MustCompile(`^[` + py.SpaceClass + `]*\p{Nd}+\.[` + py.SpaceClass + `]+`)
	mdOrdered = regexp.MustCompile(`^[` + py.SpaceClass + `]*\p{Nd}+\.`)
	mdItem    = regexp.MustCompile(`^[` + py.SpaceClass + `]*(?:[-*]|\p{Nd}+\.)[` + py.SpaceClass + `]+`)
	mdTable   = regexp.MustCompile(`^\|[` + py.SpaceClass + `:|-]+\|\n?$`)
)

func runeBefore(s string, i int) (rune, bool) {
	if i == 0 {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r, true
}

func runeAt(s string, i int) (rune, bool) {
	if i >= len(s) {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r, true
}

// italic replaces each match of (?<![\w*])\*([^*\n]+)\*(?![\w*]) in s with <i>\1</i>.
func italic(s string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); {
		if s[i] != '*' {
			i++
			continue
		}
		if r, ok := runeBefore(s, i); ok && (py.IsWord(r) || r == '*') {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && s[j] != '*' && s[j] != '\n' {
			j++
		}
		if j == i+1 || j >= len(s) || s[j] != '*' {
			i++
			continue
		}
		if r, ok := runeAt(s, j+1); ok && (py.IsWord(r) || r == '*') {
			i++
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString("<i>" + s[i+1:j] + "</i>")
		i = j + 1
		last = i
	}
	b.WriteString(s[last:])
	return b.String()
}

// stmtRefs links statement ids, human and agent: STMT_REF.sub(r'<a href="/s/\1">\1</a>', s) with
// STMT_REF = r"\b([SA]-(?:[0-9A-HJKMNP-TV-Z]{4}|\d{4,}))\b", the ids of S-0149 and the numbers before them.
func stmtRefs(s string) string {
	var b strings.Builder
	last := 0
	for i := 0; i+1 < len(s); i++ {
		if (s[i] != 'S' && s[i] != 'A') || s[i+1] != '-' {
			continue
		}
		if r, ok := runeBefore(s, i); ok && py.IsWord(r) {
			continue
		}
		// both branches match word chars only and must end at a boundary, so the id is the whole run of
		// word chars after the dash
		j := i + 2
		n, digits, alphabet := 0, true, true
		for {
			r, ok := runeAt(s, j)
			if !ok || !py.IsWord(r) {
				break
			}
			digits = digits && py.IsDecimal(r)
			alphabet = alphabet && r < utf8.RuneSelf && strings.IndexByte(idAlphabet, byte(r)) >= 0
			j += utf8.RuneLen(r)
			n++
		}
		if !(digits && n >= 4 || alphabet && n == 4) {
			continue
		}
		b.WriteString(s[last:i])
		ref := s[i:j]
		b.WriteString(`<a href="/s/` + ref + `">` + ref + `</a>`)
		last = j
		i = j - 1
	}
	b.WriteString(s[last:])
	return b.String()
}

func inline(s string) string {
	s = htmlEscape(s, false)
	s = mdCode.ReplaceAllString(s, "<code>$1</code>")
	s = mdBold.ReplaceAllString(s, "<b>$1</b>")
	s = italic(s)
	s = mdAbsLink.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = mdRelLink.ReplaceAllString(s, "$1")
	return stmtRefs(s)
}

func renderMarkdown(text string) string {
	var out []string
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, "<p>"+inline(strings.Join(para, " "))+"</p>")
			para = nil
		}
	}
	isItem := func(l string) bool { return mdBullet.MatchString(l) || mdNumber.MatchString(l) }
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.HasPrefix(line, "```") {
			flush()
			j := i + 1
			var buf []string
			for j < len(lines) && !strings.HasPrefix(lines[j], "```") {
				buf = append(buf, lines[j])
				j++
			}
			out = append(out, "<pre><code>"+htmlEscape(strings.Join(buf, "\n"), true)+"</code></pre>")
			i = j + 1
			continue
		}
		if m := mdHeading.FindStringSubmatch(line); m != nil {
			flush()
			lvl := min(len(m[1])+2, 6)
			h := string(rune('0' + lvl))
			out = append(out, "<h"+h+">"+inline(m[2])+"</h"+h+">")
			i++
			continue
		}
		if isItem(line) {
			flush()
			tag := "ul"
			if mdOrdered.MatchString(line) {
				tag = "ol"
			}
			var items []string
			for i < len(lines) && (isItem(lines[i]) || (strings.HasPrefix(lines[i], "  ") && len(items) > 0)) {
				l := lines[i]
				if mdItem.MatchString(l) {
					items = append(items, mdItem.ReplaceAllString(l, ""))
				} else {
					items[len(items)-1] += " " + py.Strip(l)
				}
				i++
			}
			var b strings.Builder
			b.WriteString("<" + tag + ">")
			for _, x := range items {
				b.WriteString("<li>" + inline(x) + "</li>")
			}
			b.WriteString("</" + tag + ">")
			out = append(out, b.String())
			continue
		}
		if strings.HasPrefix(line, ">") {
			flush()
			var buf []string
			for i < len(lines) && strings.HasPrefix(lines[i], ">") {
				buf = append(buf, py.Strip(lines[i][1:]))
				i++
			}
			out = append(out, "<blockquote>"+renderMarkdown(strings.Join(buf, "\n"))+"</blockquote>")
			continue
		}
		if strings.HasPrefix(line, "|") && i+1 < len(lines) && mdTable.MatchString(lines[i+1]) {
			flush()
			cells := func(l string) []string {
				var c []string
				for _, x := range strings.Split(strings.Trim(l, "|"), "|") {
					c = append(c, py.Strip(x))
				}
				return c
			}
			hdr := cells(line)
			i += 2
			var rows [][]string
			for i < len(lines) && strings.HasPrefix(lines[i], "|") {
				rows = append(rows, cells(lines[i]))
				i++
			}
			var b strings.Builder
			b.WriteString("<table><tr>")
			for _, h := range hdr {
				b.WriteString("<th>" + inline(h) + "</th>")
			}
			b.WriteString("</tr>")
			for _, r := range rows {
				b.WriteString("<tr>")
				for _, c := range r {
					b.WriteString("<td>" + inline(c) + "</td>")
				}
				b.WriteString("</tr>")
			}
			b.WriteString("</table>")
			out = append(out, b.String())
			continue
		}
		if py.Strip(line) == "" {
			flush()
			i++
			continue
		}
		para = append(para, py.Strip(line))
		i++
	}
	flush()
	return strings.Join(out, "")
}
