package kb

// kb show — render one or more evidence turns (or a whole session) as text.

import (
	"fmt"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

// labelMap gives friendly labels (J1, C1, X1) derived at render time from author; never stored.
func labelMap(s *Session) map[string]string {
	counts := map[string]int{}
	labels := map[string]string{}
	for _, t := range s.Turns {
		role := item(t, "role")
		var prefix string
		switch {
		case py.Equal(role, "assistant"):
			prefix = "C"
		case py.Equal(role, "document"):
			prefix = "D"
		case py.Equal(item(t, "author"), "client") || py.Equal(item(t, "author"), "tool"):
			prefix = "X"
		default:
			prefix = py.Upper(py.Head(py.AsStr(item(t, "author")), 1))
			if prefix == "" {
				prefix = "U"
			}
		}
		counts[prefix]++
		k, _ := hashKey(item(t, "uuid"))
		labels[k] = fmt.Sprintf("%s%d", prefix, counts[prefix])
	}
	return labels
}

func label(labels map[string]string, uuid any) (string, bool) {
	k, _ := hashKey(uuid)
	l, ok := labels[k]
	return l, ok
}

// locatorText renders a locator dict: "lines 1-5, heading Intro".
func locatorText(loc any) string {
	d := py.AsDict(loc)
	var parts []string
	for _, k := range d.Keys() {
		v := d.Get(k)
		if l, ok := v.([]any); ok {
			if len(l) < 2 {
				py.Raise("IndexError", "list index out of range")
			}
			parts = append(parts, fmt.Sprintf("%s %s-%s", k, py.Str(l[0]), py.Str(l[1])))
		} else {
			parts = append(parts, fmt.Sprintf("%s %s", k, py.Str(v)))
		}
	}
	return strings.Join(parts, ", ")
}

func formatTurn(s *Session, t *py.Dict, labels map[string]string, users *userTable) string {
	const width = 100
	where := py.Str(func() any {
		if v, ok := t.Lookup("timestamp"); ok {
			return v
		}
		return ""
	}())
	if loc := t.Get("locator"); py.Truthy(loc) {
		where = locatorText(loc)
	}
	l, ok := label(labels, item(t, "uuid"))
	if !ok {
		l = "?"
	}
	head := fmt.Sprintf("[%s] %s  %s  %s#%s", l, users.who(py.Str(item(t, "author"))), where, py.Head(s.ID(), 8), py.Str(item(t, "uuid")))
	lines := []string{head, strings.Repeat("-", min(py.Len(head), width))}
	for _, bv := range py.Iter(item(t, "content")) {
		b := py.AsDict(bv)
		switch typ := item(b, "type"); {
		case py.Equal(typ, "text"):
			lines = append(lines, py.AsStr(item(b, "text")))
		case py.Equal(typ, "file"):
			lines = append(lines, fmt.Sprintf("  ⟨file: %s, %s bytes; see the original⟩", py.Str(b.Get("media_type")), py.Str(b.Get("size"))))
		case py.Equal(typ, "tool_use"):
			d := ""
			if desc := b.Get("description"); py.Truthy(desc) {
				d = " — " + py.Str(desc)
			}
			lines = append(lines, fmt.Sprintf("  ⟨tool call: %s%s⟩", py.Str(item(b, "name")), d))
		case py.Equal(typ, "tool_result"):
			om := ""
			if py.Truthy(b.Get("omitted")) {
				om = " omitted"
			}
			lines = append(lines, "  ⟨tool result"+om+"⟩")
		case py.Equal(typ, "thinking"):
			if py.Truthy(b.Get("omitted")) {
				lines = append(lines, "  ⟨thinking omitted⟩")
			} else {
				th, ok := b.Lookup("thinking")
				if !ok {
					th = ""
				}
				lines = append(lines, "  ⟨thinking⟩ "+py.AsStr(th))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func runShow(ns argparse.Namespace) int {
	root := repoRoot()
	refs := ns.List("refs")
	if len(refs) == 0 {
		for _, s := range loadAllSessions(root).Values() {
			h := s.Header
			mode := py.Ljust(py.AsStr(h.Get("mode")), 9)
			title := py.Str(py.Or(h.Get("title"), ""))
			if s.Kind() == "document" {
				o := py.AsDict(func() any {
					if v, ok := h.Lookup("original"); ok {
						return v
					}
					return py.NewDict()
				}())
				println(fmt.Sprintf("%s  %s  %s  (document: %s, %s chunks, %s)", s.ID(), mode, title,
					py.Str(o.Get("name")), py.Str(h.Get("chunk_count")), py.Str(h.Get("extractor"))))
			} else {
				println(fmt.Sprintf("%s  %s  %s  (%s human turns)", s.ID(), mode, title, py.Str(h.Get("human_turns"))))
			}
		}
		return 0
	}
	for _, ref := range refs {
		if strings.Contains(ref, "#") {
			p := parsePointer(ref)
			s, t := resolvePointer(loadAllSessions(root), p)
			if s == nil {
				die("no evidence session " + p.Session)
			}
			if t == nil {
				die(fmt.Sprintf("no turn %s in session %s or its fork ancestry", p.UUID, s.ID()))
			}
			if !strings.HasPrefix(s.ID(), p.Session) {
				println(fmt.Sprintf("(resolved through fork ancestry: turn is in %s)", py.Head(s.ID(), 8)))
			}
			println(formatTurn(s, t, labelMap(s), usersForDisplay(root)))
		} else {
			s := findSession(root, ref)
			if s == nil {
				die("no evidence session " + ref)
			}
			labels := labelMap(s)
			users := usersForDisplay(root)
			titleOrID := py.Str(py.Or(s.Header.Get("title"), s.IDValue()))
			if s.Kind() == "document" {
				o := py.AsDict(func() any {
					if v, ok := s.Header.Lookup("original"); ok {
						return v
					}
					return py.NewDict()
				}())
				println(fmt.Sprintf("# %s  mode=%s extractor=%s  original=%s (%s) at %s", titleOrID, s.Mode(),
					py.Str(s.Header.Get("extractor")), py.Str(o.Get("name")), py.Str(o.Get("hash")), py.Str(o.Get("path"))))
			} else {
				println(fmt.Sprintf("# %s  mode=%s filter=%s", titleOrID, s.Mode(), py.Str(s.Header.Get("filter"))))
			}
			for _, t := range s.Turns {
				if ns.Bool("human_only") && (!py.Equal(item(t, "role"), "user") ||
					py.Equal(item(t, "author"), "client") || py.Equal(item(t, "author"), "tool")) {
					continue
				}
				println("")
				println(formatTurn(s, t, labels, users))
			}
		}
		println("")
	}
	return 0
}

func addShowParser(sub *argparse.Parser) {
	p := sub.AddParser("show", "print evidence turns; no args lists sessions")
	p.Positional("refs", argparse.ZeroMore, "<session>#<uuid> pointers or session ids")
	p.Flag("--human-only", "only human/client turns")
	p.Run = runShow
}
