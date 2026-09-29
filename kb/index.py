"""kb index / kb search — rebuildable SQLite index over evidence/, attested/ and agent/ (gitignored)."""
from __future__ import annotations

import sqlite3
from pathlib import Path

from .common import load_all_agent_statements, load_all_sessions, load_all_statements, repo_root, turn_text
from .show import label_map

SCHEMA = """
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, kind TEXT, title TEXT, mode TEXT, filter TEXT, snapshot_at TEXT,
  participants TEXT, agents TEXT, turn_count INTEGER, human_turns INTEGER, path TEXT,
  original_name TEXT, original_path TEXT, original_hash TEXT, original_media_type TEXT, original_size INTEGER
);
CREATE TABLE turns (
  uuid TEXT PRIMARY KEY, session TEXT, parent TEXT, seq INTEGER, label TEXT, role TEXT,
  author TEXT, timestamp TEXT, text TEXT, tool_calls TEXT, locator TEXT
);
CREATE INDEX turns_session ON turns(session, seq);
CREATE TABLE statements (
  id TEXT PRIMARY KEY, title TEXT, answers TEXT, originator TEXT, proposed_in TEXT, modified_in TEXT,
  supersedes TEXT, superseded_by TEXT, amends TEXT, amended_by TEXT, clarifies TEXT, clarified_by TEXT,
  topics TEXT, body TEXT, path TEXT,
  kind TEXT, rests_on TEXT, seen_count INTEGER, withdrawn_in TEXT
);
CREATE TABLE attestations (
  statement TEXT, by_user TEXT, at TEXT, turn TEXT, form TEXT
);
CREATE TABLE citations (statement TEXT, turn TEXT, kind TEXT);
CREATE VIRTUAL TABLE turns_fts USING fts5(uuid UNINDEXED, text);
CREATE VIRTUAL TABLE statements_fts USING fts5(id UNINDEXED, title, body);
"""


def db_path(root: Path) -> Path:
    return root / "index.db"


def build(root: Path) -> Path:
    """Build into a temp file, then swap it in, so an open reader never blocks the rebuild (Windows locks)."""
    p = db_path(root)
    tmp = p.with_suffix(".db.tmp")
    if tmp.exists():
        tmp.unlink()
    con = sqlite3.connect(tmp)
    con.executescript(SCHEMA)
    for s in load_all_sessions(root).values():
        h = s.header
        o = h.get("original") or {}
        if s.kind == "document":
            row = (s.id, "document", h.get("title"), s.mode, h.get("extractor"), h.get("ingested_at"),
                   h.get("ingested_by", ""), "", h.get("chunk_count"), 0, str(s.path.relative_to(root)),
                   o.get("name"), o.get("path"), o.get("hash"), o.get("media_type"), o.get("size"))
        else:
            row = (s.id, "session", h.get("title"), s.mode, h.get("filter"), h.get("snapshot_at"),
                   ",".join(h.get("participants", [])), ",".join(h.get("agents", [])),
                   h.get("turn_count"), h.get("human_turns"), str(s.path.relative_to(root)),
                   None, None, None, None, None)
        con.execute("INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", row)
        labels = label_map(s)
        for i, t in enumerate(s.turns):
            tools = "; ".join(f"{b['name']}" + (f" — {b['description']}" if b.get("description") else "")
                              for b in t["content"] if b["type"] == "tool_use")
            text = turn_text(t)
            loc = t.get("locator")
            if loc:
                loc = ", ".join(f"{k} {v[0]}-{v[1]}" if isinstance(v, list) else f"{k} {v}" for k, v in loc.items())
            con.execute("INSERT INTO turns VALUES (?,?,?,?,?,?,?,?,?,?,?)",
                        (t["uuid"], s.id, t.get("parent"), i, labels[t["uuid"]], t["role"], t["author"],
                         t.get("timestamp"), text, tools, loc))
            con.execute("INSERT INTO turns_fts(uuid, text) VALUES (?,?)", (t["uuid"], text))
    for kind, st in ([("human", x) for x in load_all_statements(root)] +
                     [("agent", x) for x in load_all_agent_statements(root)]):
        m = st.meta
        agent = kind == "agent"
        con.execute("INSERT INTO statements VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
                    (m.get("id"), m.get("title"), m.get("answers"), m.get("author" if agent else "originator"),
                     m.get("made_in" if agent else "proposed_in"),
                     m.get("modified_in"), m.get("supersedes"), m.get("superseded_by"), m.get("amends"),
                     ",".join(m.get("amended_by") or []), m.get("clarifies"), ",".join(m.get("clarified_by") or []),
                     ",".join(m.get("topics") or []), st.body, str(st.path.relative_to(root)),
                     kind, ",".join(m.get("rests_on") or []), len(m.get("seen") or []), m.get("withdrawn_in")))
        for x in m.get("seen") or []:
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), x.get("turn"), "seen"))
        if m.get("withdrawn_in"):
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), m["withdrawn_in"], "withdrawal"))
        con.execute("INSERT INTO statements_fts(id, title, body) VALUES (?,?,?)",
                    (m.get("id"), m.get("title"), st.body))
        for a in m.get("attestations") or []:
            con.execute("INSERT INTO attestations VALUES (?,?,?,?,?)",
                        (m.get("id"), a.get("by"), a.get("at"), a.get("turn"), a.get("form")))
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), a.get("turn"), "attestation"))
        if m.get("proposed_in"):
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), m["proposed_in"], "proposal"))
        if m.get("made_in"):
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), m["made_in"], "made"))
        if m.get("modified_in"):
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), m["modified_in"], "modification"))
        for c in m.get("cites") or []:
            con.execute("INSERT INTO citations VALUES (?,?,?)", (m.get("id"), c, "background"))
    con.commit()
    con.close()
    import os
    os.replace(tmp, p)
    return p




def connect(root: Path) -> sqlite3.Connection:
    p = db_path(root)
    if not p.exists():
        build(root)
    con = sqlite3.connect(p)
    con.row_factory = sqlite3.Row
    return con


def run_index(args) -> None:
    root = repo_root()
    p = build(root)
    con = sqlite3.connect(p)
    ns = con.execute("select count(*) from sessions where kind = 'session'").fetchone()[0]
    nd = con.execute("select count(*) from sessions where kind = 'document'").fetchone()[0]
    nt = con.execute("select count(*) from turns").fetchone()[0]
    nst = con.execute("select count(*) from statements where kind = 'human'").fetchone()[0]
    nag = con.execute("select count(*) from statements where kind = 'agent'").fetchone()[0]
    print(f"indexed {ns} session(s), {nd} document(s), {nt} turn(s)/chunk(s), {nst} statement(s), "
          f"{nag} agent statement(s) -> {p.name}")


def run_search(args) -> None:
    root = repo_root()
    build(root)
    con = connect(root)
    q = " ".join(args.query)
    print("== statements")
    for r in con.execute("SELECT s.id, s.title, snippet(statements_fts, 2, '[', ']', '…', 12) AS snip "
                         "FROM statements_fts JOIN statements s ON s.id = statements_fts.id "
                         "WHERE statements_fts MATCH ? ORDER BY rank LIMIT ?", (q, args.limit)):
        print(f"{r['id']}  {r['title']}\n    {r['snip']}")
    print("== evidence turns")
    for r in con.execute("SELECT t.session, t.uuid, t.label, t.author, t.locator, snippet(turns_fts, 1, '[', ']', '…', 12) AS snip "
                         "FROM turns_fts JOIN turns t ON t.uuid = turns_fts.uuid "
                         "WHERE turns_fts MATCH ? ORDER BY rank LIMIT ?", (q, args.limit)):
        where = f" ({r['locator']})" if r['locator'] else ""
        print(f"{r['session'][:8]}#{r['uuid']}  [{r['label']}] {r['author']}{where}\n    {r['snip']}")


def add_parser(sub) -> None:
    p = sub.add_parser("index", help="rebuild index.db from evidence/ and attested/")
    p.set_defaults(func=run_index)
    s = sub.add_parser("search", help="full-text search over statements and evidence")
    s.add_argument("query", nargs="+")
    s.add_argument("--limit", type=int, default=10)
    s.set_defaults(func=run_search)
