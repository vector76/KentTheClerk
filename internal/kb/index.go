package kb

// kb index / kb search — rebuildable SQLite index over evidence/, attested/ and agent/ (gitignored).
// The schema and queries are those of kb/index.py, so an index built by either version serves both.

import (
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

const schema = `
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
CREATE TABLE context_links (statement TEXT, target TEXT, note TEXT);
CREATE INDEX context_links_target ON context_links(target);
CREATE VIRTUAL TABLE turns_fts USING fts5(uuid UNINDEXED, text);
CREATE VIRTUAL TABLE statements_fts USING fts5(id UNINDEXED, title, body);
`

func dbPath(root string) string { return filepath.Join(root, "index.db") }

// sqlArg converts a value the way Python's sqlite3 binds it; a list or dict cannot be bound.
func sqlArg(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case py.Int:
		if n, ok := x.Int64(); ok {
			return n
		}
		py.Raise("OverflowError", "Python int too large to convert to SQLite INTEGER")
	case int:
		return int64(x)
	case float64:
		return x
	case string:
		py.CheckEncodable(x) // sqlite3 encodes str as UTF-8: a lone surrogate raises
		return x
	}
	py.Raise("sqlite3.ProgrammingError", "Error binding parameter - probably unsupported type.")
	return nil
}

func sqlArgs(vs ...any) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = sqlArg(v)
	}
	return out
}

// joinStrs is ",".join(v) for a list of str.
func joinStrs(v any) string {
	var parts []string
	for _, x := range py.Iter(v) {
		parts = append(parts, py.AsStr(x))
	}
	return strings.Join(parts, ",")
}

func openDB(p string) *sql.DB {
	db, err := sql.Open("sqlite", p)
	if err != nil {
		py.Raise("sqlite3.OperationalError", "%v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func must(err error) {
	if err != nil {
		py.Raise("sqlite3.OperationalError", "%s", sqliteMsg(err))
	}
}

// sqliteMsg strips the driver's decoration, leaving SQLite's own message.
func sqliteMsg(err error) string {
	s := err.Error()
	if i := strings.Index(s, ": "); i >= 0 && strings.HasPrefix(s, "SQL logic error") {
		s = s[i+2:]
	}
	if i := strings.LastIndex(s, " ("); i >= 0 && strings.HasSuffix(s, ")") {
		s = s[:i]
	}
	return s
}

// buildIndex builds into a temp file, then swaps it in, so an open reader never blocks the rebuild.
func buildIndex(root string) string {
	p := dbPath(root)
	tmp := strings.TrimSuffix(p, ".db") + ".db.tmp"
	if py.Exists(tmp) {
		if err := os.Remove(tmp); err != nil {
			py.RaiseOS(err)
		}
	}
	db := openDB(tmp)
	tx, err := db.Begin()
	must(err)
	_, err = tx.Exec(schema)
	must(err)
	exec := func(q string, args ...any) {
		_, err := tx.Exec(q, sqlArgs(args...)...)
		must(err)
	}
	for _, s := range loadAllSessions(root).Values() {
		h := s.Header
		o := py.AsDict(py.Or(h.Get("original"), py.NewDict()))
		relPath := rel(s.Path, root)
		if s.Kind() == "document" {
			exec("INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
				s.IDValue(), "document", h.Get("title"), s.ModeValue(), h.Get("extractor"), h.Get("ingested_at"),
				getOr(h, "ingested_by", ""), "", h.Get("chunk_count"), py.IntOf(0), relPath,
				o.Get("name"), o.Get("path"), o.Get("hash"), o.Get("media_type"), o.Get("size"))
		} else {
			exec("INSERT INTO sessions VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
				s.IDValue(), "session", h.Get("title"), s.ModeValue(), h.Get("filter"), h.Get("snapshot_at"),
				joinStrs(getOr(h, "participants", []any{})), joinStrs(getOr(h, "agents", []any{})),
				h.Get("turn_count"), h.Get("human_turns"), relPath, nil, nil, nil, nil, nil)
		}
		labels := labelMap(s)
		for i, t := range s.Turns {
			var tools []string
			for _, bv := range py.Iter(item(t, "content")) {
				b := py.AsDict(bv)
				if py.Equal(item(b, "type"), "tool_use") {
					x := py.Str(item(b, "name"))
					if d := b.Get("description"); py.Truthy(d) {
						x += " — " + py.Str(d)
					}
					tools = append(tools, x)
				}
			}
			text := turnText(t)
			loc := t.Get("locator")
			if py.Truthy(loc) {
				loc = locatorText(loc)
			}
			l, _ := label(labels, item(t, "uuid"))
			exec("INSERT INTO turns VALUES (?,?,?,?,?,?,?,?,?,?,?)",
				item(t, "uuid"), s.IDValue(), t.Get("parent"), py.IntOf(i), l, item(t, "role"), item(t, "author"),
				t.Get("timestamp"), text, strings.Join(tools, "; "), loc)
			exec("INSERT INTO turns_fts(uuid, text) VALUES (?,?)", item(t, "uuid"), text)
		}
	}
	type kst struct {
		kind string
		st   *Statement
	}
	var all []kst
	for _, x := range loadAllStatements(root) {
		all = append(all, kst{"human", x})
	}
	for _, x := range loadAllAgentStatements(root) {
		all = append(all, kst{"agent", x})
	}
	for _, ks := range all {
		m := ks.st.Meta
		agent := ks.kind == "agent"
		originator, proposed := m.Get("originator"), m.Get("proposed_in")
		if agent {
			originator, proposed = m.Get("author"), m.Get("made_in")
		}
		id := m.Get("id")
		exec("INSERT INTO statements VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			id, m.Get("title"), m.Get("answers"), originator, proposed,
			m.Get("modified_in"), m.Get("supersedes"), m.Get("superseded_by"), m.Get("amends"),
			joinStrs(py.Or(m.Get("amended_by"), []any{})), m.Get("clarifies"), joinStrs(py.Or(m.Get("clarified_by"), []any{})),
			joinStrs(py.Or(m.Get("topics"), []any{})), ks.st.Body, rel(ks.st.Path, root),
			ks.kind, joinStrs(py.Or(m.Get("rests_on"), []any{})), py.IntOf(len(listOf(m.Get("seen")))), m.Get("withdrawn_in"))
		for _, xv := range listOf(m.Get("seen")) {
			exec("INSERT INTO citations VALUES (?,?,?)", id, py.AsDict(xv).Get("turn"), "seen")
		}
		if wi := m.Get("withdrawn_in"); py.Truthy(wi) {
			exec("INSERT INTO citations VALUES (?,?,?)", id, wi, "withdrawal")
		}
		exec("INSERT INTO statements_fts(id, title, body) VALUES (?,?,?)", id, m.Get("title"), ks.st.Body)
		for _, av := range listOf(m.Get("attestations")) {
			a := py.AsDict(av)
			exec("INSERT INTO attestations VALUES (?,?,?,?,?)", id, a.Get("by"), a.Get("at"), a.Get("turn"), a.Get("form"))
			exec("INSERT INTO citations VALUES (?,?,?)", id, a.Get("turn"), "attestation")
		}
		for _, k := range [][2]string{{"proposed_in", "proposal"}, {"made_in", "made"}, {"modified_in", "modification"}} {
			if v := m.Get(k[0]); py.Truthy(v) {
				exec("INSERT INTO citations VALUES (?,?,?)", id, v, k[1])
			}
		}
		for _, c := range listOf(m.Get("cites")) {
			exec("INSERT INTO citations VALUES (?,?,?)", id, c, "background")
		}
		for _, l := range contextLinks(m) {
			exec("INSERT INTO context_links VALUES (?,?,?)", id, l.target, l.note)
		}
	}
	must(tx.Commit())
	must(db.Close())
	if err := os.Rename(tmp, p); err != nil {
		py.RaiseOS(err)
	}
	return p
}

func connectIndex(root string) *sql.DB {
	p := dbPath(root)
	if !py.Exists(p) {
		buildIndex(root)
	}
	return openDB(p)
}

func count(db *sql.DB, q string) int64 {
	var n int64
	must(db.QueryRow(q).Scan(&n))
	return n
}

func runIndex(ns argparse.Namespace) int {
	root := repoRoot()
	p := buildIndex(root)
	db := openDB(p)
	defer db.Close()
	println(fmt.Sprintf("indexed %d session(s), %d document(s), %d turn(s)/chunk(s), %d statement(s), %d agent statement(s) -> %s",
		count(db, "select count(*) from sessions where kind = 'session'"),
		count(db, "select count(*) from sessions where kind = 'document'"),
		count(db, "select count(*) from turns"),
		count(db, "select count(*) from statements where kind = 'human'"),
		count(db, "select count(*) from statements where kind = 'agent'"), filepath.Base(p)))
	return 0
}

// row is a result row keyed by column name, holding int64, float64, string, []byte or nil.
type row map[string]any

func query(db *sql.DB, q string, args ...any) []row {
	rows, err := db.Query(q, args...)
	must(err)
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		must(rows.Scan(ptrs...))
		r := row{}
		for i, c := range cols {
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	must(rows.Err())
	return out
}

// sqlStr is str(value) for a value read from SQLite.
func sqlStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case int64:
		return fmt.Sprint(x)
	case float64:
		return py.FloatRepr(x)
	case string:
		return x
	case []byte:
		return py.Repr(string(x))
	}
	return fmt.Sprint(v)
}

func runSearch(ns argparse.Namespace) int {
	root := repoRoot()
	buildIndex(root)
	db := connectIndex(root)
	defer db.Close()
	q := strings.Join(ns.List("query"), " ")
	limit := ns["limit"].(*big.Int)
	if !limit.IsInt64() {
		py.Raise("OverflowError", "Python int too large to convert to SQLite INTEGER")
	}
	println("== statements")
	flushStdout()
	for _, r := range query(db, "SELECT s.id, s.title, snippet(statements_fts, 2, '[', ']', '…', 12) AS snip "+
		"FROM statements_fts JOIN statements s ON s.id = statements_fts.id "+
		"WHERE statements_fts MATCH ? ORDER BY rank LIMIT ?", q, limit.Int64()) {
		println(fmt.Sprintf("%s  %s\n    %s", sqlStr(r["id"]), sqlStr(r["title"]), sqlStr(r["snip"])))
	}
	println("== evidence turns")
	for _, r := range query(db, "SELECT t.session, t.uuid, t.label, t.author, t.locator, snippet(turns_fts, 1, '[', ']', '…', 12) AS snip "+
		"FROM turns_fts JOIN turns t ON t.uuid = turns_fts.uuid "+
		"WHERE turns_fts MATCH ? ORDER BY rank LIMIT ?", q, limit.Int64()) {
		where := ""
		if py.Truthy(sqlTruthy(r["locator"])) {
			where = " (" + sqlStr(r["locator"]) + ")"
		}
		println(fmt.Sprintf("%s#%s  [%s] %s%s\n    %s", py.Head(sqlStr(r["session"]), 8), sqlStr(r["uuid"]), sqlStr(r["label"]),
			sqlStr(r["author"]), where, sqlStr(r["snip"])))
	}
	return 0
}

// sqlTruthy maps a SQLite value to the Python value whose truth it has.
func sqlTruthy(v any) any {
	switch x := v.(type) {
	case int64:
		return x != 0
	case []byte:
		return len(x) > 0
	}
	return v
}

func addIndexParsers(sub *argparse.Parser) {
	p := sub.AddParser("index", "rebuild index.db from evidence/ and attested/")
	p.Run = runIndex
	s := sub.AddParser("search", "full-text search over statements and evidence")
	s.Positional("query", argparse.OneMore, "")
	s.IntOption("--limit", "", 10)
	s.Run = runSearch
}
