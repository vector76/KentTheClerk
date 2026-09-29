"""kb serve — local HTML reading surface over index.db.

Routes:
  /                    overview: statements, sessions
  /s/<id>              statement page: text, originator, attestations, citations, supersession, inbound links
  /e/<session>         conversation page; turns anchored by uuid (#<uuid>)
  /e/<session>#<uuid>  deep link to a turn (or a document chunk)
  /o/<hash><ext>       the original bytes of an ingested document, served by content-hash name
  /u/<user>            everything a user authored or attested
  /search?q=           full-text search
"""
from __future__ import annotations

import html
import mimetypes
import re
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .common import ORIGINAL_NAME_RE, originals_dir, repo_root
from .index import build, connect
from .md import render_markdown

CSS = """
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
"""


def esc(s) -> str:
    return html.escape("" if s is None else str(s))


def page(title: str, body: str) -> bytes:
    doc = f"""<!doctype html><html><head><meta charset="utf-8"><title>{esc(title)} · Kent the Clerk</title>
<style>{CSS}</style></head><body><header><a href="/">Kent the Clerk</a>
<a href="/cards" style="font-weight:400">cards</a><a href="/e/" style="font-weight:400">evidence</a>
<form action="/search"><input name="q" placeholder="search…"></form></header><main>{body}</main></body></html>"""
    return doc.encode("utf-8")


def link_pointer(con, ptr: str) -> str:
    """Render an evidence pointer as a link if it resolves; inactive text otherwise."""
    if not ptr or "#" not in ptr:
        return esc(ptr)
    sess, uuid = ptr.split("#", 1)
    r = con.execute("SELECT t.session, t.label, t.author FROM turns t WHERE t.session LIKE ? AND t.uuid = ?",
                    (sess + "%", uuid)).fetchone()
    if not r:
        return f'<span class="dead" title="does not resolve here (off-record or missing)">{esc(ptr)}</span>'
    return f'<a href="/e/{esc(r["session"])}#{esc(uuid)}">{esc(r["label"])} · {esc(sess[:8])}#{esc(uuid[:8])}</a>'


def render_turn(con, t) -> str:
    cls = "client" if t["author"] in ("client", "tool") else t["role"]
    body = render_markdown(t["text"]) if t["text"] else ""
    when = (t["timestamp"] or "")[:19].replace("T", " ")
    if t["locator"]:
        when = t["locator"]
    tools = "".join(f'<div class="tool">⟨tool: {esc(x)}⟩</div>' for x in (t["tool_calls"] or "").split("; ") if x)
    cites = con.execute("SELECT DISTINCT statement, kind FROM citations WHERE turn = ?", (f"{t['session']}#{t['uuid']}",)).fetchall()
    cite_html = ""
    if cites:
        cite_html = '<div class="meta">cited by ' + ", ".join(
            f'<a href="/s/{esc(c["statement"])}">{esc(c["statement"])}</a> ({esc(c["kind"])})' for c in cites) + "</div>"
    return (f'<div class="turn {cls}" id="{esc(t["uuid"])}"><div class="hd"><b>{esc(t["label"])}</b>'
            f'<span>{esc(t["author"])}</span><span>{esc(when)}</span>'
            f'<a href="#{esc(t["uuid"])}">#</a></div>{body}{tools}{cite_html}</div>')


def statement_card(con, s, full: bool = False) -> str:
    atts = con.execute("SELECT * FROM attestations WHERE statement = ? ORDER BY at", (s["id"],)).fetchall()
    who = ", ".join(f'<a href="/u/{esc(a["by_user"])}">{esc(a["by_user"])}</a>' for a in atts) or "<i>unattested</i>"
    byline = f"attested by {who}"
    if s["kind"] == "agent":
        seen = f'seen {s["seen_count"]}×' if s["seen_count"] else "<i>not yet seen</i>"
        byline = (f'agent statement by <a href="/u/{esc(s["originator"])}">{esc(s["originator"])}</a> · {seen}'
                  + (' · <span class="pill">withdrawn</span>' if s["withdrawn_in"] else ""))
    sup = ""
    if s["superseded_by"]:
        sup = f' <span class="pill">superseded by <a href="/s/{esc(s["superseded_by"])}">{esc(s["superseded_by"])}</a></span>'
    for key, label in (("amended_by", "amended by"), ("clarified_by", "clarified by")):
        for x in (s[key] or "").split(","):
            if x:
                sup += f' <span class="pill">{label} <a href="/s/{esc(x)}">{esc(x)}</a></span>'
    for key in ("supersedes", "amends", "clarifies"):
        if s[key]:
            sup += f' <span class="pill">{key} <a href="/s/{esc(s[key])}">{esc(s[key])}</a></span>'
    head = (f'<div class="meta"><a href="/s/{esc(s["id"])}"><b>{esc(s["id"])}</b></a>'
            f'{sup} · {byline}</div><h3 style="margin:.2rem 0 .5rem">{esc(s["title"])}</h3>')
    body = render_markdown(s["body"]) if full else render_markdown(s["body"].split("\n\n")[0])
    answers = f'<div class="meta">answers: {esc(s["answers"])}</div>' if s["answers"] else ""
    return f'<div class="stmt">{head}{answers}<div class="body">{body}</div></div>'


class Handler(BaseHTTPRequestHandler):
    root = None

    def log_message(self, *a):
        pass

    def send(self, content: bytes, status: int = 200, ctype: str = "text/html; charset=utf-8"):
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(content)))
        self.end_headers()
        self.wfile.write(content)

    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        path = urllib.parse.unquote(url.path)
        qs = urllib.parse.parse_qs(url.query)
        con = connect(self.root)
        try:
            if path == "/":
                return self.send(page("overview", self.overview(con)))
            if path == "/cards":
                return self.send(page("cards", self.cards(con)))
            if path.startswith("/s/"):
                return self.statement(con, path[3:])
            if path.startswith("/e/"):
                return self.evidence(con, path[3:])
            if path.startswith("/u/"):
                return self.user(con, path[3:])
            if path.startswith("/o/"):
                return self.original(path[3:])
            if path == "/search":
                return self.search(con, (qs.get("q") or [""])[0])
            if path == "/reindex":
                con.close()
                build(self.root)
                self.send_response(302); self.send_header("Location", "/"); self.end_headers(); return
            self.send(page("not found", "<h1>not found</h1>"), 404)
        finally:
            con.close()

    def original(self, name: str):
        """Serve an ingested original by its content-hash name (S-0039). Only hash-shaped names resolve."""
        if not ORIGINAL_NAME_RE.match(name):
            return self.send(page("not found", "<h1>not found</h1>"), 404)
        for mode in ("on-record", "off-record"):
            p = originals_dir(self.root, mode) / name
            if p.is_file():
                ctype = mimetypes.guess_type(name)[0] or "application/octet-stream"
                return self.send(p.read_bytes(), 200, ctype)
        return self.send(page("not found", "<h1>original not found</h1>"), 404)

    def overview(self, con) -> str:
        """Dense table: one row per statement, id + one-line synopsis (title) + relations + attesters."""
        out = ['<h1>Kent the Clerk</h1><table class="dense"><tr><th>id</th><th>statement</th><th>relations</th><th>attested by</th></tr>']
        for s in con.execute("SELECT * FROM statements ORDER BY id"):
            who = ", ".join(r["by_user"] for r in con.execute(
                "SELECT by_user FROM attestations WHERE statement = ? ORDER BY at", (s["id"],)))
            if s["kind"] == "agent":
                who = f'agent: {s["originator"]}' + ("" if s["seen_count"] else " (unseen)")
            marks = []
            for key, label in (("superseded_by", "→"), ("supersedes", "⇐"), ("amends", "amends"),
                               ("amended_by", "amended by"), ("clarifies", "clarifies"), ("clarified_by", "clarified by")):
                for x in (s[key] or "").split(","):
                    if x:
                        marks.append(f'{label} <a href="/s/{esc(x)}">{esc(x)}</a>')
            cls = ' class="old"' if s["superseded_by"] or s["withdrawn_in"] else ""
            out.append(f'<tr{cls}><td><a href="/s/{esc(s["id"])}">{esc(s["id"])}</a></td>'
                       f'<td><a href="/s/{esc(s["id"])}">{esc(s["title"])}</a></td><td>{" ".join(marks)}</td><td>{esc(who)}</td></tr>')
        out.append('</table>')
        out.append('<h2><a href="/e/">evidence</a></h2><table class="dense"><tr><th>item</th><th>title</th><th>mode</th><th>turns</th></tr>')
        for s in con.execute("SELECT * FROM sessions ORDER BY snapshot_at"):
            size = (f'{esc(s["turn_count"])} chunks of {esc(s["original_name"])}' if s["kind"] == "document"
                    else f'{esc(s["human_turns"])} human / {esc(s["turn_count"])}')
            out.append(f'<tr><td><a href="/e/{esc(s["id"])}">{esc(s["id"][:8])}</a> <span class="pill">{esc(s["kind"])}</span></td>'
                       f'<td>{esc(s["title"])}</td><td>{esc(s["mode"])}</td><td>{size}</td></tr>')
        out.append('</table><p class="meta"><a href="/cards">card view</a> · <a href="/reindex">rebuild index</a></p>')
        return "".join(out)

    def cards(self, con) -> str:
        out = ["<h1>Kent the Clerk</h1>"]
        for s in con.execute("SELECT * FROM statements ORDER BY id"):
            out.append(statement_card(con, s))
        return "".join(out)

    def statement(self, con, sid: str):
        s = con.execute("SELECT * FROM statements WHERE id = ?", (sid,)).fetchone()
        if not s:
            return self.send(page(sid, f"<h1>{esc(sid)} not found</h1>"), 404)
        out = [statement_card(con, s, full=True)]
        out.append("<h2>provenance</h2><table>")
        agent = s["kind"] == "agent"
        out.append(f"<tr><th>{'author' if agent else 'originator'}</th><td><a href='/u/{esc(s['originator'])}'>{esc(s['originator'])}</a></td></tr>")
        out.append(f"<tr><th>{'made in' if agent else 'proposed in'}</th><td>{link_pointer(con, s['proposed_in'])}</td></tr>")
        for c in con.execute("SELECT turn, kind FROM citations WHERE statement = ? AND kind IN ('seen', 'withdrawal')", (sid,)):
            out.append(f"<tr><th>{esc(c['kind'])}</th><td>{link_pointer(con, c['turn'])}</td></tr>")
        if s["rests_on"]:
            links = ", ".join(f"<a href='/s/{esc(x)}'>{esc(x)}</a>" for x in s["rests_on"].split(",") if x)
            out.append(f"<tr><th>rests on</th><td>{links}</td></tr>")
        if s["modified_in"]:
            out.append(f"<tr><th>modified in</th><td>{link_pointer(con, s['modified_in'])}</td></tr>")
        for a in con.execute("SELECT * FROM attestations WHERE statement = ? ORDER BY at", (sid,)):
            out.append(f"<tr><th>attested ({esc(a['form'])})</th><td><a href='/u/{esc(a['by_user'])}'>{esc(a['by_user'])}</a> "
                       f"at {esc((a['at'] or '')[:19])} · {link_pointer(con, a['turn'])}</td></tr>")
        for c in con.execute("SELECT turn FROM citations WHERE statement = ? AND kind = 'background'", (sid,)):
            out.append(f"<tr><th>background</th><td>{link_pointer(con, c['turn'])}</td></tr>")
        for key in ("supersedes", "superseded_by", "amends", "amended_by", "clarifies", "clarified_by"):
            if s[key]:
                links = ", ".join(f"<a href='/s/{esc(x)}'>{esc(x)}</a>" for x in s[key].split(",") if x)
                out.append(f"<tr><th>{esc(key.replace('_', ' '))}</th><td>{links}</td></tr>")
        if s["topics"]:
            out.append(f"<tr><th>topics</th><td>{esc(s['topics'])}</td></tr>")
        out.append(f"<tr><th>file</th><td><code>{esc(s['path'])}</code></td></tr></table>")
        if s["modified_in"]:
            out.append("<h2>agreed with modification — check by reading</h2>")
            for label, ptr in (("proposal", s["proposed_in"]), ("modification", s["modified_in"])):
                sess_, uuid_ = ptr.split("#", 1)
                t = con.execute("SELECT * FROM turns WHERE session LIKE ? AND uuid = ?", (sess_ + "%", uuid_)).fetchone()
                if t:
                    out.append(f"<h3 style='margin:.8rem 0 .2rem'>{label}</h3>" + render_turn(con, t))
        inbound = con.execute("SELECT id, title FROM statements WHERE body LIKE ? AND id != ? ORDER BY id", (f"%{sid}%", sid)).fetchall()
        if inbound:
            out.append("<h2>inbound links</h2><ul>" + "".join(f'<li><a href="/s/{esc(r["id"])}">{esc(r["id"])}</a> {esc(r["title"])}</li>' for r in inbound) + "</ul>")
        self.send(page(f"{sid} {s['title']}", "".join(out)))

    def evidence(self, con, sid: str):
        if not sid:
            out = ["<h1>evidence</h1><table><tr><th>item</th><th>title</th><th>mode</th><th>filter / extractor</th><th>recorded</th></tr>"]
            for s in con.execute("SELECT * FROM sessions ORDER BY snapshot_at"):
                out.append(f'<tr><td><a href="/e/{esc(s["id"])}">{esc(s["id"][:8])}</a> <span class="pill">{esc(s["kind"])}</span></td>'
                           f'<td>{esc(s["title"])}</td><td>{esc(s["mode"])}</td>'
                           f'<td>{esc(s["filter"])}</td><td>{esc((s["snapshot_at"] or "")[:19])}</td></tr>')
            return self.send(page("evidence", "".join(out) + "</table>"))
        s = con.execute("SELECT * FROM sessions WHERE id LIKE ?", (sid + "%",)).fetchone()
        if not s:
            return self.send(page(sid, f"<h1>session {esc(sid)} not found</h1>"), 404)
        if s["kind"] == "document":
            oname = (s["original_path"] or "").rsplit("/", 1)[-1]
            out = [f'<h1>{esc(s["title"] or s["id"])}</h1><div class="meta">document {esc(s["id"])} · mode {esc(s["mode"])} · '
                   f'extractor {esc(s["filter"])} · ingested by {esc(s["participants"])} {esc((s["snapshot_at"] or "")[:19])}<br>'
                   f'original <a href="/o/{esc(oname)}">{esc(s["original_name"])}</a> · {esc(s["original_media_type"])} · '
                   f'{esc(s["original_size"])} bytes · <code>{esc(s["original_hash"])}</code></div>']
        else:
            out = [f'<h1>{esc(s["title"] or s["id"])}</h1><div class="meta">{esc(s["id"])} · mode {esc(s["mode"])} · filter {esc(s["filter"])} · '
                   f'participants {esc(s["participants"])} · agents {esc(s["agents"])}</div>']
        for t in con.execute("SELECT * FROM turns WHERE session = ? ORDER BY seq", (s["id"],)):
            out.append(render_turn(con, t))
        self.send(page(s["title"] or s["id"], "".join(out)))

    def user(self, con, uid: str):
        out = [f"<h1>{esc(uid)}</h1><h2>attested</h2>"]
        for s in con.execute("SELECT s.* FROM statements s JOIN attestations a ON a.statement = s.id WHERE a.by_user = ? ORDER BY s.id", (uid,)):
            out.append(statement_card(con, s))
        out.append("<h2>originated</h2>")
        for s in con.execute("SELECT * FROM statements WHERE originator = ? ORDER BY id", (uid,)):
            out.append(statement_card(con, s))
        out.append("<h2>sessions</h2><ul>")
        for s in con.execute("SELECT * FROM sessions WHERE participants LIKE ? OR agents LIKE ?", (f"%{uid}%", f"%{uid}%")):
            out.append(f'<li><a href="/e/{esc(s["id"])}">{esc(s["title"] or s["id"])}</a></li>')
        self.send(page(uid, "".join(out) + "</ul>"))

    def search(self, con, q: str):
        out = [f'<h1>search</h1><form><input name="q" value="{esc(q)}" size="50"></form>']
        if q:
            out.append("<h2>statements</h2>")
            try:
                for r in con.execute("SELECT s.*, snippet(statements_fts, 2, '<mark>', '</mark>', '…', 16) AS snip FROM statements_fts "
                                     "JOIN statements s ON s.id = statements_fts.id WHERE statements_fts MATCH ? ORDER BY rank LIMIT 25", (q,)):
                    out.append(f'<div class="stmt"><a href="/s/{esc(r["id"])}"><b>{esc(r["id"])}</b></a> {esc(r["title"])}<div class="meta">{r["snip"]}</div></div>')
                out.append("<h2>evidence</h2>")
                for r in con.execute("SELECT t.*, snippet(turns_fts, 1, '<mark>', '</mark>', '…', 16) AS snip FROM turns_fts "
                                     "JOIN turns t ON t.uuid = turns_fts.uuid WHERE turns_fts MATCH ? ORDER BY rank LIMIT 25", (q,)):
                    out.append(f'<div class="turn {r["role"]}"><div class="hd"><a href="/e/{esc(r["session"])}#{esc(r["uuid"])}">{esc(r["label"])}</a>'
                               f'<span>{esc(r["author"])}</span></div>{r["snip"]}</div>')
            except Exception as e:  # bad FTS syntax
                out.append(f'<p class="meta">query error: {esc(e)}</p>')
        self.send(page("search", "".join(out)))


def run(args) -> None:
    root = repo_root()
    build(root)
    Handler.root = root
    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"serving http://{args.host}:{args.port}/  (Ctrl+C to stop)")
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


def add_parser(sub) -> None:
    p = sub.add_parser("serve", help="HTML reading surface over the index")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=8765)
    p.set_defaults(func=run)
