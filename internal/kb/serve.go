package kb

// kb serve — local HTML reading surface over index.db. The HTTP framing is Go's (HTTP/1.1 and its own
// headers).

import (
	"database/sql"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/dyn"
	"kenttheclerk/internal/mimetypes"
)

const serveCSS = `
:root{--bg:#fff;--fg:#1b1b1b;--muted:#6b6b6b;--line:#e3e3e3;--accent:#2a5db0;--human:#f4f7fb;--agent:#fafafa;--client:#fff8e6;--doc:#f3f6f3;--mono:ui-monospace,Consolas,monospace}
@media (prefers-color-scheme:dark){:root{--bg:#161618;--fg:#e6e6e6;--muted:#9a9a9a;--line:#333;--accent:#8ab4ff;--human:#1d2330;--agent:#1c1c1e;--client:#2b2614;--doc:#1b211b}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.55 system-ui,Segoe UI,sans-serif}
header{border-bottom:1px solid var(--line);padding:.6rem 1.2rem;display:flex;gap:1.2rem;align-items:center}
header a{color:var(--fg);text-decoration:none;font-weight:600}
header form{margin-left:auto}header input{padding:.3rem .5rem;border:1px solid var(--line);border-radius:4px;background:var(--bg);color:var(--fg)}
main{max-width:60rem;margin:0 auto;padding:1.2rem}
a{color:var(--accent)}
h1{font-size:1.5rem;margin:.2rem 0 .8rem}h2{font-size:1.15rem;margin:1.6rem 0 .5rem;border-bottom:1px solid var(--line)}
.meta{color:var(--muted);font-size:.9rem}.pill{display:inline-block;border:1px solid var(--line);border-radius:999px;padding:0 .55rem;font-size:.8rem;color:var(--muted)}
.stmt{border:1px solid var(--line);border-radius:6px;padding:1rem 1.2rem;margin:.8rem 0}
.stmt .body{font-size:1.05rem}
.turn{border-left:3px solid var(--line);padding:.5rem 1rem;margin:.6rem 0;border-radius:0 6px 6px 0}
.turn.user{background:var(--human);border-color:var(--accent)}.turn.assistant{background:var(--agent)}.turn.client{background:var(--client)}.turn.document{background:var(--doc);border-color:var(--muted)}
.turn:target{outline:2px solid var(--accent)}
.turn .hd{font-size:.8rem;color:var(--muted);display:flex;gap:.8rem}.turn .hd a{color:var(--muted)}
.tool{font:.82rem var(--mono);color:var(--muted)}
table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:.3rem .5rem;border-bottom:1px solid var(--line);vertical-align:top}
code{font:.9em var(--mono)}pre{overflow-x:auto;background:var(--agent);padding:.6rem;border-radius:4px}
.dead{color:var(--muted);text-decoration:line-through dotted}
table.dense{font-size:.88rem;line-height:1.3}table.dense td,table.dense th{padding:.15rem .5rem;white-space:nowrap}
table.dense td:nth-child(3){white-space:normal}table.dense td:first-child a{font:.85em var(--mono)}tr.old td{color:var(--muted)}
`

// esc is v as HTML-escaped text, quotes included ("" for nil).
func esc(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return htmlEscape(s, true)
	}
	return htmlEscape(sqlStr(v), true)
}

func page(title, body string) []byte {
	return []byte(`<!doctype html><html><head><meta charset="utf-8"><title>` + esc(title) + ` · Kent the Clerk</title>
<style>` + serveCSS + `</style></head><body><header><a href="/">Kent the Clerk</a>
<a href="/cards" style="font-weight:400">cards</a><a href="/e/" style="font-weight:400">evidence</a>
<form action="/search"><input name="q" placeholder="search…"></form></header><main>` + body + `</main></body></html>`)
}

// sqlS is a TEXT column read as a string ("" for NULL).
func sqlS(v any) string {
	if v == nil {
		return ""
	}
	return sqlStr(v)
}

func truthy(v any) bool { return dyn.Truthy(sqlTruthy(v)) }

func linkPointer(db *sql.DB, ptr any) string {
	p, ok := ptr.(string)
	if !truthy(ptr) || !ok || !strings.Contains(p, "#") {
		return esc(ptr)
	}
	sess, uuid, _ := strings.Cut(p, "#")
	rows := query(db, "SELECT t.session, t.label, t.author FROM turns t WHERE t.session LIKE ? AND t.uuid = ?", sess+"%", uuid)
	if len(rows) == 0 {
		return `<span class="dead" title="does not resolve here (off-record or missing)">` + esc(p) + `</span>`
	}
	r := rows[0]
	return `<a href="/e/` + esc(r["session"]) + `#` + esc(uuid) + `">` + esc(r["label"]) + ` · ` + esc(dyn.Head(sess, 8)) + `#` + esc(dyn.Head(uuid, 8)) + `</a>`
}

func renderTurn(db *sql.DB, t row) string {
	cls := sqlStr(t["role"])
	if a := t["author"]; a == "client" || a == "tool" {
		cls = "client"
	}
	body := ""
	if truthy(t["text"]) {
		body = renderMarkdown(sqlStr(t["text"]))
	}
	when := strings.ReplaceAll(dyn.Head(sqlS(t["timestamp"]), 19), "T", " ")
	if truthy(t["locator"]) {
		when = sqlStr(t["locator"])
	}
	var tools strings.Builder
	for _, x := range strings.Split(sqlS(t["tool_calls"]), "; ") {
		if x != "" {
			tools.WriteString(`<div class="tool">⟨tool: ` + esc(x) + `⟩</div>`)
		}
	}
	cites := query(db, "SELECT DISTINCT statement, kind FROM citations WHERE turn = ?", sqlStr(t["session"])+"#"+sqlStr(t["uuid"]))
	citeHTML := ""
	if len(cites) > 0 {
		var parts []string
		for _, c := range cites {
			parts = append(parts, `<a href="/s/`+esc(c["statement"])+`">`+esc(c["statement"])+`</a> (`+esc(c["kind"])+`)`)
		}
		citeHTML = `<div class="meta">cited by ` + strings.Join(parts, ", ") + `</div>`
	}
	return `<div class="turn ` + cls + `" id="` + esc(t["uuid"]) + `"><div class="hd"><b>` + esc(t["label"]) + `</b>` +
		`<span>` + who(db, t["author"]) + `</span><span>` + esc(when) + `</span>` +
		`<a href="#` + esc(t["uuid"]) + `">#</a></div>` + body + tools.String() + citeHTML + `</div>`
}

func splitComma(v any) []string { return strings.Split(sqlS(v), ",") }

// nameOf is the name the user table gives a key, or "".
func nameOf(db *sql.DB, key any) string {
	if rows := query(db, "SELECT name FROM users WHERE key = ?", key); len(rows) > 0 {
		return sqlS(rows[0]["name"])
	}
	return ""
}

// who is an author as the pages show one: the key, with the name beside it (S-0138).
func who(db *sql.DB, key any) string {
	if n := nameOf(db, key); n != "" {
		return esc(key) + ` <span class="meta">(` + esc(n) + `)</span>`
	}
	return esc(key)
}

// userLink is an author linked to their page.
func userLink(db *sql.DB, key any) string {
	return `<a href="/u/` + esc(key) + `">` + who(db, key) + `</a>`
}

func statementCard(db *sql.DB, s row, full bool) string {
	atts := query(db, "SELECT * FROM attestations WHERE statement = ? ORDER BY at", s["id"])
	var whoParts []string
	for _, a := range atts {
		whoParts = append(whoParts, userLink(db, a["by_user"]))
	}
	who := strings.Join(whoParts, ", ")
	if who == "" {
		who = "<i>unattested</i>"
	}
	byline := "attested by " + who
	if s["kind"] == "agent" {
		seen := "<i>not yet seen</i>"
		if truthy(s["seen_count"]) {
			seen = "seen " + sqlStr(s["seen_count"]) + "×"
		}
		byline = `agent statement by ` + userLink(db, s["originator"]) + ` · ` + seen
		if truthy(s["withdrawn_in"]) {
			byline += ` · <span class="pill">withdrawn</span>`
		}
	}
	sup := ""
	if truthy(s["superseded_by"]) {
		sup = ` <span class="pill">superseded by <a href="/s/` + esc(s["superseded_by"]) + `">` + esc(s["superseded_by"]) + `</a></span>`
	}
	for _, kl := range [][2]string{{"amended_by", "amended by"}, {"clarified_by", "clarified by"}} {
		for _, x := range splitComma(s[kl[0]]) {
			if x != "" {
				sup += ` <span class="pill">` + kl[1] + ` <a href="/s/` + esc(x) + `">` + esc(x) + `</a></span>`
			}
		}
	}
	for _, key := range []string{"supersedes", "amends", "clarifies"} {
		if truthy(s[key]) {
			sup += ` <span class="pill">` + key + ` <a href="/s/` + esc(s[key]) + `">` + esc(s[key]) + `</a></span>`
		}
	}
	if n := len(query(db, "SELECT 1 FROM context_links WHERE target = ?", s["id"])); n > 0 {
		sup += ` <span class="pill"><a href="/s/` + esc(s["id"]) + `#context">extra context</a> (` + fmt.Sprint(n) + `)</span>`
	}
	head := `<div class="meta"><a href="/s/` + esc(s["id"]) + `"><b>` + esc(s["id"]) + `</b></a>` +
		sup + ` · ` + byline + `</div><h3 style="margin:.2rem 0 .5rem">` + esc(s["title"]) + `</h3>`
	bodyText := sqlStr(s["body"])
	if !full {
		bodyText = strings.SplitN(bodyText, "\n\n", 2)[0]
	}
	body := renderMarkdown(bodyText)
	answers := ""
	if truthy(s["answers"]) {
		answers = `<div class="meta">answers: ` + esc(s["answers"]) + `</div>`
	}
	return `<div class="stmt">` + head + answers + `<div class="body">` + body + `</div></div>`
}

type kbServer struct{ root string }

func (h *kbServer) send(w http.ResponseWriter, content []byte, status int, ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", fmt.Sprint(len(content)))
	w.WriteHeader(status)
	w.Write(content)
}

func (h *kbServer) html(w http.ResponseWriter, content []byte, status int) {
	h.send(w, content, status, "text/html; charset=utf-8")
}

// ServeHTTP routes a request. URLs are parsed and unescaped by net/http in the usual Go way.
func (h *kbServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path
	db := connectIndex(h.root)
	defer db.Close()
	switch {
	case path == "/":
		h.html(w, page("overview", h.overview(db)), 200)
	case path == "/cards":
		h.html(w, page("cards", h.cards(db)), 200)
	case strings.HasPrefix(path, "/s/"):
		h.statement(w, db, path[3:])
	case strings.HasPrefix(path, "/e/"):
		h.evidence(w, db, path[3:])
	case strings.HasPrefix(path, "/u/"):
		h.user(w, db, path[3:])
	case strings.HasPrefix(path, "/o/"):
		h.original(w, path[3:])
	case path == "/search":
		h.search(w, db, r.URL.Query().Get("q"))
	case path == "/reindex":
		db.Close()
		buildIndex(h.root)
		http.Redirect(w, r, "/", http.StatusFound)
	default:
		h.html(w, page("not found", "<h1>not found</h1>"), 404)
	}
}

func (h *kbServer) original(w http.ResponseWriter, name string) {
	if !originalNameRE.MatchString(name) {
		h.html(w, page("not found", "<h1>not found</h1>"), 404)
		return
	}
	for _, mode := range []string{"on-record", "off-record"} {
		p := filepath.Join(originalsDir(h.root, mode), name)
		if dyn.IsFile(p) {
			ctype := mimetypes.GuessType(name)
			if ctype == "" {
				ctype = "application/octet-stream"
			}
			b, err := os.ReadFile(p)
			if err != nil {
				dyn.RaiseOS(err)
			}
			h.send(w, b, 200, ctype)
			return
		}
	}
	h.html(w, page("not found", "<h1>original not found</h1>"), 404)
}

func (h *kbServer) overview(db *sql.DB) string {
	out := []string{`<h1>Kent the Clerk</h1><table class="dense"><tr><th>id</th><th>statement</th><th>relations</th><th>attested by</th></tr>`}
	for _, s := range query(db, "SELECT * FROM statements ORDER BY made_at, id") {
		var whos []string
		for _, r := range query(db, "SELECT by_user FROM attestations WHERE statement = ? ORDER BY at", s["id"]) {
			if r["by_user"] == nil {
				dyn.Raise("TypeError", "an attestation has no author")
			}
			whos = append(whos, who(db, r["by_user"]))
		}
		by := strings.Join(whos, ", ")
		if s["kind"] == "agent" {
			by = "agent: " + esc(s["originator"])
			if !truthy(s["seen_count"]) {
				by += " (unseen)"
			}
		}
		var marks []string
		for _, kl := range [][2]string{{"superseded_by", "→"}, {"supersedes", "⇐"}, {"amends", "amends"},
			{"amended_by", "amended by"}, {"clarifies", "clarifies"}, {"clarified_by", "clarified by"}} {
			for _, x := range splitComma(s[kl[0]]) {
				if x != "" {
					marks = append(marks, kl[1]+` <a href="/s/`+esc(x)+`">`+esc(x)+`</a>`)
				}
			}
		}
		for _, r := range query(db, "SELECT c.target FROM context_links c LEFT JOIN statements s ON s.id = c.target WHERE c.statement = ? ORDER BY s.made_at, c.target", s["id"]) {
			marks = append(marks, `context for `+stmtLink(sqlS(r["target"])))
		}
		for _, r := range query(db, "SELECT c.statement FROM context_links c LEFT JOIN statements s ON s.id = c.statement WHERE c.target = ? ORDER BY s.made_at, c.statement", s["id"]) {
			marks = append(marks, `context from <a href="/s/`+esc(r["statement"])+`">`+esc(r["statement"])+`</a>`)
		}
		cls := ""
		if truthy(s["superseded_by"]) || truthy(s["withdrawn_in"]) {
			cls = ` class="old"`
		}
		out = append(out, `<tr`+cls+`><td><a href="/s/`+esc(s["id"])+`">`+esc(s["id"])+`</a></td>`+
			`<td><a href="/s/`+esc(s["id"])+`">`+esc(s["title"])+`</a></td><td>`+strings.Join(marks, " ")+`</td><td>`+by+`</td></tr>`)
	}
	out = append(out, `</table>`)
	out = append(out, `<h2><a href="/e/">evidence</a></h2><table class="dense"><tr><th>item</th><th>title</th><th>mode</th><th>turns</th></tr>`)
	for _, s := range query(db, "SELECT * FROM sessions ORDER BY snapshot_at") {
		var size string
		if s["kind"] == "document" {
			size = esc(s["turn_count"]) + ` chunks of ` + esc(s["original_name"])
		} else {
			size = esc(s["human_turns"]) + ` human / ` + esc(s["turn_count"])
		}
		out = append(out, `<tr><td><a href="/e/`+esc(s["id"])+`">`+esc(dyn.Head(sqlStr(s["id"]), 8))+`</a> <span class="pill">`+esc(s["kind"])+`</span></td>`+
			`<td>`+esc(s["title"])+`</td><td>`+esc(s["mode"])+`</td><td>`+size+`</td></tr>`)
	}
	out = append(out, `</table><p class="meta"><a href="/cards">card view</a> · <a href="/reindex">rebuild index</a></p>`)
	return strings.Join(out, "")
}

func (h *kbServer) cards(db *sql.DB) string {
	out := []string{"<h1>Kent the Clerk</h1>"}
	for _, s := range query(db, "SELECT * FROM statements ORDER BY made_at, id") {
		out = append(out, statementCard(db, s, false))
	}
	return strings.Join(out, "")
}

func (h *kbServer) statement(w http.ResponseWriter, db *sql.DB, sid string) {
	rows := query(db, "SELECT * FROM statements WHERE id = ?", sid)
	if len(rows) == 0 {
		h.html(w, page(sid, "<h1>"+esc(sid)+" not found</h1>"), 404)
		return
	}
	s := rows[0]
	out := []string{statementCard(db, s, true)}
	out = append(out, "<h2>provenance</h2><table>")
	agent := s["kind"] == "agent"
	l1, l2 := "originator", "proposed in"
	if agent {
		l1, l2 = "author", "made in"
	}
	out = append(out, "<tr><th>"+l1+"</th><td>"+userLink(db, s["originator"])+"</td></tr>")
	out = append(out, "<tr><th>"+l2+"</th><td>"+linkPointer(db, s["proposed_in"])+"</td></tr>")
	for _, c := range query(db, "SELECT turn, kind FROM citations WHERE statement = ? AND kind IN ('seen', 'withdrawal')", sid) {
		out = append(out, "<tr><th>"+esc(c["kind"])+"</th><td>"+linkPointer(db, c["turn"])+"</td></tr>")
	}
	if truthy(s["rests_on"]) {
		var links []string
		for _, x := range splitComma(s["rests_on"]) {
			if x != "" {
				links = append(links, stmtLink(x))
			}
		}
		out = append(out, "<tr><th>rests on</th><td>"+strings.Join(links, ", ")+"</td></tr>")
	}
	if truthy(s["modified_in"]) {
		out = append(out, "<tr><th>modified in</th><td>"+linkPointer(db, s["modified_in"])+"</td></tr>")
	}
	for _, a := range query(db, "SELECT * FROM attestations WHERE statement = ? ORDER BY at", sid) {
		out = append(out, "<tr><th>attested ("+esc(a["form"])+")</th><td>"+userLink(db, a["by_user"])+" "+
			"at "+esc(dyn.Head(sqlS(a["at"]), 19))+" · "+linkPointer(db, a["turn"])+"</td></tr>")
	}
	for _, c := range query(db, "SELECT turn FROM citations WHERE statement = ? AND kind = 'background'", sid) {
		out = append(out, "<tr><th>background</th><td>"+linkPointer(db, c["turn"])+"</td></tr>")
	}
	for _, key := range []string{"supersedes", "superseded_by", "amends", "amended_by", "clarifies", "clarified_by"} {
		if truthy(s[key]) {
			var links []string
			for _, x := range splitComma(s[key]) {
				if x != "" {
					links = append(links, stmtLink(x))
				}
			}
			out = append(out, "<tr><th>"+esc(strings.ReplaceAll(key, "_", " "))+"</th><td>"+strings.Join(links, ", ")+"</td></tr>")
		}
	}
	for _, c := range query(db, "SELECT c.target, c.note FROM context_links c LEFT JOIN statements s ON s.id = c.target WHERE c.statement = ? ORDER BY s.made_at, c.target", sid) {
		out = append(out, "<tr><th>context for</th><td>"+stmtLink(sqlS(c["target"]))+": "+esc(c["note"])+"</td></tr>")
	}
	out = append(out, "<tr><th>file</th><td><code>"+esc(s["path"])+"</code></td></tr></table>")
	if truthy(s["modified_in"]) {
		out = append(out, "<h2>agreed with modification — check by reading</h2>")
		for _, lp := range [][2]any{{"proposal", s["proposed_in"]}, {"modification", s["modified_in"]}} {
			sess, uuid, ok := strings.Cut(sqlStr(lp[1]), "#")
			if !ok {
				dyn.Raise("ValueError", "not enough values to unpack (expected 2, got 1)")
			}
			ts := query(db, "SELECT * FROM turns WHERE session LIKE ? AND uuid = ?", sess+"%", uuid)
			if len(ts) > 0 {
				out = append(out, "<h3 style='margin:.8rem 0 .2rem'>"+lp[0].(string)+"</h3>"+renderTurn(db, ts[0]))
			}
		}
	}
	// statements that change how this one is read, with their notes; those in force first
	context := query(db, "SELECT c.statement, c.note, s.title, s.superseded_by, s.withdrawn_in FROM context_links c "+
		"LEFT JOIN statements s ON s.id = c.statement WHERE c.target = ? "+
		"ORDER BY (s.superseded_by IS NOT NULL AND s.superseded_by != '') OR (s.withdrawn_in IS NOT NULL AND s.withdrawn_in != ''), s.made_at, c.statement", sid)
	if len(context) > 0 {
		var b strings.Builder
		b.WriteString(`<h2 id="context">extra context: statements that change how this one is read</h2><table class="dense">`)
		for _, r := range context {
			cls := ""
			if truthy(r["superseded_by"]) || truthy(r["withdrawn_in"]) {
				cls = ` class="old"`
			}
			b.WriteString(`<tr` + cls + `><td><a href="/s/` + esc(r["statement"]) + `">` + esc(r["statement"]) + `</a></td><td>` + esc(r["title"]) +
				`</td><td>` + esc(r["note"]) + `</td></tr>`)
		}
		b.WriteString("</table>")
		out = append(out, b.String())
	}
	inbound := query(db, "SELECT id, title FROM statements WHERE body LIKE ? AND id != ? ORDER BY made_at, id", "%"+sid+"%", sid)
	if len(inbound) > 0 {
		var b strings.Builder
		b.WriteString("<h2>inbound links</h2><ul>")
		for _, r := range inbound {
			b.WriteString(`<li><a href="/s/` + esc(r["id"]) + `">` + esc(r["id"]) + `</a> ` + esc(r["title"]) + `</li>`)
		}
		b.WriteString("</ul>")
		out = append(out, b.String())
	}
	h.html(w, page(sid+" "+sqlStr(s["title"]), strings.Join(out, "")), 200)
}

func (h *kbServer) evidence(w http.ResponseWriter, db *sql.DB, sid string) {
	if sid == "" {
		out := []string{"<h1>evidence</h1><table><tr><th>item</th><th>title</th><th>mode</th><th>filter / extractor</th><th>recorded</th></tr>"}
		for _, s := range query(db, "SELECT * FROM sessions ORDER BY snapshot_at") {
			out = append(out, `<tr><td><a href="/e/`+esc(s["id"])+`">`+esc(dyn.Head(sqlStr(s["id"]), 8))+`</a> <span class="pill">`+esc(s["kind"])+`</span></td>`+
				`<td>`+esc(s["title"])+`</td><td>`+esc(s["mode"])+`</td>`+
				`<td>`+esc(s["filter"])+`</td><td>`+esc(dyn.Head(sqlS(s["snapshot_at"]), 19))+`</td></tr>`)
		}
		h.html(w, page("evidence", strings.Join(out, "")+"</table>"), 200)
		return
	}
	rows := query(db, "SELECT * FROM sessions WHERE id LIKE ?", sid+"%")
	if len(rows) == 0 {
		h.html(w, page(sid, "<h1>session "+esc(sid)+" not found</h1>"), 404)
		return
	}
	s := rows[0]
	var out []string
	titleOrID := s["title"]
	if !truthy(titleOrID) {
		titleOrID = s["id"]
	}
	if s["kind"] == "document" {
		op := sqlS(s["original_path"])
		oname := op[strings.LastIndex(op, "/")+1:]
		out = []string{`<h1>` + esc(titleOrID) + `</h1><div class="meta">document ` + esc(s["id"]) + ` · mode ` + esc(s["mode"]) + ` · ` +
			`extractor ` + esc(s["filter"]) + ` · ingested by ` + esc(s["participants"]) + ` ` + esc(dyn.Head(sqlS(s["snapshot_at"]), 19)) + `<br>` +
			`original <a href="/o/` + esc(oname) + `">` + esc(s["original_name"]) + `</a> · ` + esc(s["original_media_type"]) + ` · ` +
			esc(s["original_size"]) + ` bytes · <code>` + esc(s["original_hash"]) + `</code></div>`}
	} else {
		out = []string{`<h1>` + esc(titleOrID) + `</h1><div class="meta">` + esc(s["id"]) + ` · mode ` + esc(s["mode"]) + ` · filter ` + esc(s["filter"]) + ` · ` +
			`participants ` + esc(s["participants"]) + ` · agents ` + esc(s["agents"]) + `</div>`}
	}
	for _, t := range query(db, "SELECT * FROM turns WHERE session = ? ORDER BY seq", s["id"]) {
		out = append(out, renderTurn(db, t))
	}
	h.html(w, page(sqlStr(titleOrID), strings.Join(out, "")), 200)
}

func (h *kbServer) user(w http.ResponseWriter, db *sql.DB, uid string) {
	head := "<h1>" + esc(uid) + "</h1>"
	if rows := query(db, "SELECT name, email FROM users WHERE key = ?", uid); len(rows) > 0 {
		head += `<p class="meta">` + esc(rows[0]["name"]) + ` &lt;` + esc(rows[0]["email"]) + `&gt;, from the user table</p>`
	}
	out := []string{head + "<h2>attested</h2>"}
	for _, s := range query(db, "SELECT s.* FROM statements s JOIN attestations a ON a.statement = s.id WHERE a.by_user = ? ORDER BY s.made_at, s.id", uid) {
		out = append(out, statementCard(db, s, false))
	}
	out = append(out, "<h2>originated</h2>")
	for _, s := range query(db, "SELECT * FROM statements WHERE originator = ? ORDER BY made_at, id", uid) {
		out = append(out, statementCard(db, s, false))
	}
	out = append(out, "<h2>sessions</h2><ul>")
	for _, s := range query(db, "SELECT * FROM sessions WHERE participants LIKE ? OR agents LIKE ?", "%"+uid+"%", "%"+uid+"%") {
		t := s["title"]
		if !truthy(t) {
			t = s["id"]
		}
		out = append(out, `<li><a href="/e/`+esc(s["id"])+`">`+esc(t)+`</a></li>`)
	}
	h.html(w, page(uid, strings.Join(out, "")+"</ul>"), 200)
}

// tryQuery runs a query, returning a raised exception (a bad search, say) as an error.
func tryQuery(db *sql.DB, q string, args ...any) (rows []row, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*dyn.Exception); ok {
				rows, err = nil, fmt.Errorf("%s", e.Msg)
				return
			}
			panic(r)
		}
	}()
	return query(db, q, args...), nil
}

// snipOpen and snipClose are what snippet() puts around a match: Unicode noncharacters, which text does
// not carry (private-use characters can come out of a PDF's symbol fonts), so the snippet can be
// HTML-escaped as text and the markers then turned into <mark> and </mark>.
const snipOpen, snipClose = "\uFDD0", "\uFDD1"

// snipHTML is an FTS5 snippet made with snipOpen and snipClose, as HTML: the indexed text escaped, the
// matches marked.
func snipHTML(v any) string {
	return strings.NewReplacer(snipOpen, "<mark>", snipClose, "</mark>").Replace(esc(v))
}

func (h *kbServer) search(w http.ResponseWriter, db *sql.DB, q string) {
	out := []string{`<h1>search</h1><form><input name="q" value="` + esc(q) + `" size="50"></form>`}
	if q != "" {
		out = append(out, "<h2>statements</h2>")
		func() {
			rows, err := tryQuery(db, "SELECT s.*, snippet(statements_fts, 2, '"+snipOpen+"', '"+snipClose+"', '…', 16) AS snip FROM statements_fts "+
				"JOIN statements s ON s.id = statements_fts.id WHERE statements_fts MATCH ? ORDER BY rank LIMIT 25", q)
			if err != nil {
				out = append(out, `<p class="meta">query error: `+esc(err.Error())+`</p>`)
				return
			}
			for _, r := range rows {
				out = append(out, `<div class="stmt"><a href="/s/`+esc(r["id"])+`"><b>`+esc(r["id"])+`</b></a> `+esc(r["title"])+`<div class="meta">`+snipHTML(r["snip"])+`</div></div>`)
			}
			out = append(out, "<h2>evidence</h2>")
			rows, err = tryQuery(db, "SELECT t.*, snippet(turns_fts, 1, '"+snipOpen+"', '"+snipClose+"', '…', 16) AS snip FROM turns_fts "+
				"JOIN turns t ON t.uuid = turns_fts.uuid WHERE turns_fts MATCH ? ORDER BY rank LIMIT 25", q)
			if err != nil {
				out = append(out, `<p class="meta">query error: `+esc(err.Error())+`</p>`)
				return
			}
			for _, r := range rows {
				out = append(out, `<div class="turn `+sqlStr(r["role"])+`"><div class="hd"><a href="/e/`+esc(r["session"])+`#`+esc(r["uuid"])+`">`+esc(r["label"])+`</a>`+
					`<span>`+who(db, r["author"])+`</span></div>`+snipHTML(r["snip"])+`</div>`)
			}
		}()
	}
	h.html(w, page("search", strings.Join(out, "")), 200)
}

// stmtLink links a statement id to its page. A reference into another knowledge base, which this server
// does not hold, is shown without a link (S-Y17H90).
func stmtLink(x string) string {
	if isForeignRef(x) {
		return `<span title="in another knowledge base">` + esc(x) + `</span>`
	}
	return "<a href='/s/" + esc(x) + "'>" + esc(x) + "</a>"
}

func runServe(ns argparse.Namespace) int {
	root := repoRoot()
	buildIndex(root)
	host, _ := ns.Str("host")
	port := ns["port"].(*big.Int)
	if !port.IsInt64() || port.Int64() < 0 || port.Int64() > 65535 {
		dyn.Raise("OverflowError", "bind(): port must be 0-65535.")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port.String()))
	if err != nil {
		dyn.RaiseOS(err)
	}
	println(fmt.Sprintf("serving http://%s:%s/  (Ctrl+C to stop)", host, port.String()))
	flushStdout()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		<-stop
		ln.Close()
	}()
	_ = (&http.Server{Handler: &kbServer{root}}).Serve(ln)
	return 0
}

func addServeParser(sub *argparse.Parser) {
	p := sub.AddParser("serve", "HTML reading surface over the index")
	p.Option("--host", "", "127.0.0.1", false)
	p.IntOption("--port", "", 8765)
	p.Run = runServe
}
