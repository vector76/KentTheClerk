// Command indexquery runs kb search's two queries against an existing index.db with the Go SQLite
// driver, without rebuilding it, so the parity harness can show that the Go reader searches an index
// the Python version built (and the reverse, with Python's sqlite3) with the same results.
//
//	indexquery <index.db> <limit> <query...>
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		panic(err)
	}
	limit, _ := strconv.Atoi(os.Args[2])
	q := strings.Join(os.Args[3:], " ")
	out := map[string][][]any{}
	for name, sq := range map[string]string{
		"statements": "SELECT s.id, s.title, snippet(statements_fts, 2, '[', ']', '…', 12) AS snip " +
			"FROM statements_fts JOIN statements s ON s.id = statements_fts.id " +
			"WHERE statements_fts MATCH ? ORDER BY rank LIMIT ?",
		"turns": "SELECT t.session, t.uuid, t.label, t.author, t.locator, snippet(turns_fts, 1, '[', ']', '…', 12) AS snip " +
			"FROM turns_fts JOIN turns t ON t.uuid = turns_fts.uuid " +
			"WHERE turns_fts MATCH ? ORDER BY rank LIMIT ?",
	} {
		rows, err := db.Query(sq, q, limit)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				panic(err)
			}
			out[name] = append(out[name], vals)
		}
		rows.Close()
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
