package kb

// kb snapshot — copy a Claude Code session JSONL into evidence/ (or off-record/sessions/) with a filter.
// See kb/snapshot.py for the filter profiles; this is a line-for-line port.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

var (
	systemReminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	clientNoticeRE   = regexp.MustCompile(`^\[(Request interrupted by user[^\]]*)\][` + py.SpaceClass + `]*\z`)
)

func claudeProjectsDir() string {
	if v, ok := os.LookupEnv("CLAUDE_PROJECTS_DIR"); ok {
		return py.PathStr(v)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Claude Code sets CLAUDE_CODE_SESSION_ID for the commands it runs. It is trusted only when its file
// looks like the live conversation: the most recently written session file in its project, or written
// within the last minute.
const (
	sessionEnv           = "CLAUDE_CODE_SESSION_ID"
	currentWithinSeconds = 60
)

func mtime(p string) float64 {
	st, err := os.Stat(p)
	if err != nil {
		py.RaiseOS(err)
	}
	return float64(st.ModTime().UnixNano()) / 1e9
}

func currentSessionSource() string {
	sid := os.Getenv(sessionEnv)
	if sid == "" {
		die(fmt.Sprintf("no session given, and %s is not set; pass the session id", sessionEnv))
	}
	src := locateSource(sid)
	mt := mtime(src)
	age := float64(time.Now().UnixNano())/1e9 - mt
	newest := true
	for _, p := range py.Glob(filepath.Dir(src), "*.jsonl") {
		if mtime(p) > mt {
			newest = false
		}
	}
	if age > currentWithinSeconds && !newest {
		die(fmt.Sprintf("%s names session %s, but its file was last written %ds ago and a newer "+
			"session file exists beside it, so it may not be this conversation (for example after /clear); "+
			"pass the session id", sessionEnv, py.Head(sid, 8), int(age)))
	}
	return src
}

func locateSource(sessionID string) string {
	if py.Suffix(sessionID) == ".jsonl" && py.Exists(sessionID) {
		return py.PathStr(sessionID)
	}
	var hits []string
	for _, h := range py.Glob(claudeProjectsDir(), "*/"+sessionID+"*.jsonl") {
		if filepath.Base(filepath.Dir(h)) != "memory" {
			hits = append(hits, h)
		}
	}
	if len(hits) == 0 {
		die(fmt.Sprintf("no session JSONL matching %s under %s", py.StrRepr(sessionID), claudeProjectsDir()))
	}
	if len(hits) > 1 {
		die("ambiguous session id; matches:\n  " + strings.Join(hits, "\n  "))
	}
	return hits[0]
}

func toolCallSummary(block *py.Dict, profile string) *py.Dict {
	inp := py.Or(block.Get("input"), py.NewDict())
	out := py.D("type", "tool_use", "id", block.Get("id"), "name", block.Get("name"))
	if profile == "full-v1" {
		out.Set("input", inp)
		return out
	}
	in := py.AsDict(inp)
	desc := in.Get("description")
	if !py.Truthy(desc) {
		for _, k := range []string{"file_path", "pattern", "query", "prompt", "command", "url"} {
			if s, ok := in.Get(k).(string); ok {
				desc = py.Head(s, 160)
				break
			}
		}
	}
	if py.Truthy(desc) {
		out.Set("description", desc)
	}
	return out
}

func blockText(b *py.Dict) any {
	if v, ok := b.Lookup("text"); ok {
		return v
	}
	return ""
}

func filterUser(rec *py.Dict, profile string) *py.Dict {
	msg := py.AsDict(py.Or(rec.Get("message"), py.NewDict()))
	content := msg.Get("content")
	origin := py.AsDict(py.Or(rec.Get("origin"), py.NewDict())).Get("kind")
	var blocks []any
	isHuman := py.Equal(origin, "human")
	allToolResults := true
	add := func(b *py.Dict) {
		if !py.Equal(b.Get("type"), "tool_result") {
			allToolResults = false
		}
		blocks = append(blocks, b)
	}
	switch c := content.(type) {
	case string:
		text := c
		if profile != "full-v1" {
			text = py.Strip(systemReminderRE.ReplaceAllString(text, ""))
		}
		if text == "" {
			return nil
		}
		if !isHuman && !clientNoticeRE.MatchString(text) && profile != "full-v1" {
			return nil
		}
		add(py.D("type", "text", "text", text))
	case []any:
		for _, bv := range c {
			b := py.AsDict(bv)
			t := b.Get("type")
			switch {
			case py.Equal(t, "text"):
				tv := blockText(b)
				if profile != "full-v1" {
					text := py.Strip(systemReminderRE.ReplaceAllString(py.AsStr(tv), ""))
					if text == "" {
						continue
					}
					if !isHuman && !clientNoticeRE.MatchString(text) {
						continue
					}
					tv = text
				}
				add(py.D("type", "text", "text", tv))
			case py.Equal(t, "tool_result"):
				if profile == "full-v1" {
					add(b)
				} else {
					cv, ok := b.Lookup("content")
					if !ok {
						cv = ""
					}
					raw := py.Dumps(cv)
					add(py.D("type", "tool_result", "tool_use_id", b.Get("tool_use_id"), "omitted", true, "size", py.IntOf(py.Len(raw))))
				}
			case py.Equal(t, "image") && profile == "full-v1":
				add(b)
			}
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	author := rec.Get("_author")
	if !isHuman && allToolResults {
		author = "tool"
	} else if !isHuman {
		author = "client"
	}
	return py.D("type", "turn", "uuid", item(rec, "uuid"), "parent", rec.Get("parentUuid"),
		"timestamp", rec.Get("timestamp"), "role", "user", "author", author, "content", blocks)
}

func filterAssistant(rec *py.Dict, profile string) *py.Dict {
	msg := py.AsDict(py.Or(rec.Get("message"), py.NewDict()))
	var blocks []any
	content, ok := msg.Lookup("content")
	if !ok {
		content = []any{}
	}
	for _, bv := range py.Iter(content) {
		b := py.AsDict(bv)
		t := b.Get("type")
		switch {
		case py.Equal(t, "text"):
			if py.Strip(py.AsStr(blockText(b))) != "" {
				blocks = append(blocks, py.D("type", "text", "text", item(b, "text")))
			}
		case py.Equal(t, "tool_use"):
			blocks = append(blocks, toolCallSummary(b, profile))
		case py.Equal(t, "thinking"):
			if profile == "full-v1" {
				th, ok := b.Lookup("thinking")
				if !ok {
					th = ""
				}
				blocks = append(blocks, py.D("type", "thinking", "thinking", th))
			} else {
				blocks = append(blocks, py.D("type", "thinking", "omitted", true))
			}
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	model, ok := msg.Lookup("model")
	if !ok {
		model = "unknown"
	}
	return py.D("type", "turn", "uuid", item(rec, "uuid"), "parent", rec.Get("parentUuid"),
		"timestamp", rec.Get("timestamp"), "role", "assistant", "author", "agent:"+py.Str(model), "content", blocks)
}

// droppedCounts is info["dropped"]: a dict whose keys may be any hashable JSON value; json.dumps turns
// them into strings.
type droppedCounts struct {
	order  []string
	jsonKy map[string]string
	n      map[string]int
}

func (d *droppedCounts) add(kind any) {
	var id, key string
	switch k := kind.(type) {
	case string:
		id, key = "s"+k, k
	case nil:
		id, key = "n", "null"
	case bool:
		id, key = "b"+py.Repr(k), map[bool]string{true: "true", false: "false"}[k]
		if k {
			id = "i1"
		} else {
			id = "i0"
		}
	case py.Int:
		id, key = "i"+string(k), string(k)
	case float64:
		id, key = "f"+py.FloatRepr(k), py.Dumps(k)
		if k == float64(int64(k)) {
			id = "i" + fmt.Sprint(int64(k))
		}
	default:
		py.Raise("TypeError", "unhashable type: '%s'", py.TypeName(kind))
	}
	if _, ok := d.n[id]; !ok {
		d.order = append(d.order, id)
		d.jsonKy[id] = key
	}
	d.n[id]++
}

func (d *droppedCounts) dict() *py.Dict {
	out := py.NewDict()
	for _, id := range d.order {
		out.Set(d.jsonKy[id], py.IntOf(d.n[id]))
	}
	return out
}

type projectInfo struct {
	title                               any
	dropped                             *droppedCounts
	humanTurns, agentTurns, clientTurns int
}

func project(records []*py.Dict, profile, humanUser string) ([]*py.Dict, *projectInfo) {
	var turns []*py.Dict
	info := &projectInfo{dropped: &droppedCounts{jsonKy: map[string]string{}, n: map[string]int{}}}
	for _, rec := range records {
		t := rec.Get("type")
		if py.Equal(t, "custom-title") {
			info.title = rec.Get("customTitle")
			continue
		}
		if py.Equal(t, "ai-title") && !py.Truthy(info.title) {
			info.title = rec.Get("aiTitle")
			continue
		}
		if !py.Equal(t, "user") && !py.Equal(t, "assistant") {
			info.dropped.add(t)
			continue
		}
		if py.Truthy(rec.Get("isSidechain")) {
			info.dropped.add("sidechain")
			continue
		}
		rec.Set("_author", humanUser)
		var out *py.Dict
		if py.Equal(t, "user") {
			out = filterUser(rec, profile)
		} else {
			out = filterAssistant(rec, profile)
		}
		if out == nil {
			info.dropped.add(t.(string) + ":empty-after-filter")
			continue
		}
		turns = append(turns, out)
		if py.Equal(out.Get("role"), "assistant") {
			info.agentTurns++
		} else if a := out.Get("author"); py.Equal(a, "client") || py.Equal(a, "tool") {
			info.clientTurns++
		} else {
			info.humanTurns++
		}
	}
	return turns, info
}

// hashKey makes a dict key for a JSON scalar the way Python hashes them (1 == 1.0 == True).
func hashKey(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return "s" + x, true
	case nil:
		return "n", true
	case bool:
		if x {
			return "i1", true
		}
		return "i0", true
	case py.Int:
		return "i" + string(x), true
	case float64:
		if x == float64(int64(x)) {
			return "i" + fmt.Sprint(int64(x)), true
		}
		return "f" + py.FloatRepr(x), true
	}
	return "", false
}

// resolveBranchPoint is the parent-session turn this session forked from, or nil, following the
// first kept turn's parent through records of this same session that the filter dropped.
func resolveBranchPoint(records []*py.Dict, turns []*py.Dict) any {
	if len(turns) == 0 {
		return nil
	}
	own := map[string]*py.Dict{}
	for _, r := range records {
		if u := r.Get("uuid"); py.Truthy(u) {
			k, ok := hashKey(u)
			if !ok {
				py.Raise("TypeError", "unhashable type: '%s'", py.TypeName(u))
			}
			own[k] = r
		}
	}
	p := turns[0].Get("parent")
	seen := map[string]bool{}
	for py.Truthy(p) {
		k, ok := hashKey(p)
		if !ok {
			py.Raise("TypeError", "unhashable type: '%s'", py.TypeName(p))
		}
		r, in := own[k]
		if !in || seen[k] {
			break
		}
		seen[k] = true
		p = r.Get("parentUuid")
	}
	return py.Or(p, nil)
}

func readSource(path string) []*py.Dict {
	var recs []*py.Dict
	for n, line := range py.TextLines(path) {
		line = py.Strip(line)
		if line == "" {
			continue
		}
		v, err := py.Loads(line)
		if err != nil {
			die(fmt.Sprintf("%s:%d: bad JSON (%s)", path, n+1, err.Error()))
		}
		recs = append(recs, py.AsDict(v))
	}
	return recs
}

func writeJSONL(dest string, header *py.Dict, records []*py.Dict) {
	tmp := strings.TrimSuffix(dest, ".jsonl") + ".jsonl.tmp"
	var b strings.Builder
	for _, rec := range append([]*py.Dict{header}, records...) {
		line := py.Dumps(rec) + "\n"
		if py.SurrogateIndex(line) >= 0 {
			// Python writes the lines before this one to the .tmp file, then fails to encode it;
			// the record itself is left untouched.
			if err := os.WriteFile(tmp, []byte(b.String()), 0o666); err != nil {
				py.RaiseOS(err)
			}
			py.CheckEncodable(line)
		}
		b.WriteString(line)
	}
	if err := os.WriteFile(tmp, []byte(b.String()), 0o666); err != nil {
		py.RaiseOS(err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		py.RaiseOS(err)
	}
}

func runSnapshot(ns argparse.Namespace) int {
	root := repoRoot()
	userArg, _ := ns.Str("user")
	user := currentUser(userArg)
	mode, _ := ns.Str("mode")
	filter, _ := ns.Str("filter")
	session, hasSession := ns.Str("session")
	title, _ := ns.Str("title")
	force := ns.Bool("force")
	if !in(mode, modes) {
		die("mode must be one of " + py.Tuple(modes...))
	}
	if !in(filter, filterProfiles) {
		die("filter must be one of " + py.Tuple(filterProfiles...))
	}
	var src string
	if hasSession && session != "" {
		src = locateSource(session)
	} else {
		src = currentSessionSource()
	}
	records := readSource(src)
	ids := map[string]bool{}
	var idList []string
	for _, r := range records {
		if sid := r.Get("sessionId"); py.Truthy(sid) {
			s := py.Str(sid)
			if !ids[s] {
				ids[s] = true
				idList = append(idList, s)
			}
		}
	}
	var sessionID string
	if len(idList) == 1 {
		sessionID = idList[0]
	} else {
		prefix := session
		if prefix == "" {
			prefix = py.Stem(src)
		}
		var wanted []string
		for _, s := range idList {
			if strings.HasPrefix(s, prefix) {
				wanted = append(wanted, s)
			}
		}
		if len(wanted) != 1 {
			sorted := append([]string{}, idList...)
			sort.Strings(sorted)
			if sorted == nil {
				sorted = []string{}
			}
			die(fmt.Sprintf("multiple sessionIds in %s: %s; pass the id of the one to snapshot", src, py.Repr(sorted)))
		}
		sessionID = wanted[0]
		var kept []*py.Dict
		for _, r := range records {
			if v := r.Get("sessionId"); v == nil || py.Equal(v, sessionID) {
				kept = append(kept, r)
			}
		}
		records = kept
	}

	turns, info := project(records, filter, user)
	branchPoint := resolveBranchPoint(records, turns)
	if info.humanTurns == 0 {
		die("no human turns survived the filter; refusing to write an empty snapshot")
	}

	destDir := filepath.Join(root, "evidence")
	if mode != "on-record" {
		destDir = filepath.Join(root, "off-record", "sessions")
	}
	if err := os.MkdirAll(destDir, 0o777); err != nil {
		py.RaiseOS(err)
	}
	var firstTS any
	for _, t := range turns {
		if ts := t.Get("timestamp"); py.Truthy(ts) {
			firstTS = ts
			break
		}
	}
	date := ""
	if py.Truthy(firstTS) {
		date = py.Head(py.AsStr(firstTS), 10)
	}
	if date == "" {
		date = now().Format("2006-01-02")
	}
	dest := filepath.Join(destDir, date+"-"+py.Head(sessionID, 8)+".jsonl")

	// Append-only guard: a re-snapshot must be a superset of what was there.
	if py.Exists(dest) {
		var prev []any
		for _, line := range py.TextLines(dest) {
			v, err := py.Loads(line)
			if err != nil {
				py.Raise("json.decoder.JSONDecodeError", "%s", err.Error())
			}
			rec := py.AsDict(v)
			switch t := rec.Get("type"); {
			case py.Equal(t, "turn"):
				prev = append(prev, item(rec, "uuid"))
			case py.Equal(t, "kb-snapshot"):
				if !py.Equal(rec.Get("filter"), filter) && !force {
					die(fmt.Sprintf("%s was taken with filter %s; pass --force to change profile (%s)", dest, py.Repr(rec.Get("filter")), rule("mode-fixed")))
				}
				if !py.Equal(rec.Get("mode"), mode) {
					die(fmt.Sprintf("%s has mode %s; a session's mode does not change (%s)", dest, py.Repr(rec.Get("mode")), rule("mode-fixed")))
				}
			}
		}
		newIDs := map[string]bool{}
		for _, t := range turns {
			k, _ := hashKey(t.Get("uuid"))
			newIDs[k] = true
		}
		var missing []any
		for _, u := range prev {
			k, ok := hashKey(u)
			if !ok || !newIDs[k] {
				missing = append(missing, u)
			}
		}
		if len(missing) > 0 && !force {
			die(fmt.Sprintf("re-snapshot would drop %d previously recorded turn(s) (e.g. %s); refusing (%s)", len(missing), py.Str(missing[0]), rule("append-only")))
		}
	}
	// Same session must not exist in the other mode either.
	other := filepath.Join(root, "off-record", "sessions")
	if mode != "on-record" {
		other = filepath.Join(root, "evidence")
	}
	for _, p := range py.Glob(other, "*.jsonl") {
		if strings.Contains(filepath.Base(p), py.Head(sessionID, 8)) {
			die(fmt.Sprintf("session %s already exists under %s; a session has one mode (%s)", py.Head(sessionID, 8), other, rule("mode-fixed")))
		}
	}

	agentSet := map[string]bool{}
	agents := []string{}
	for _, t := range turns {
		if py.Equal(t.Get("role"), "assistant") {
			a := py.AsStr(t.Get("author"))
			if !agentSet[a] {
				agentSet[a] = true
				agents = append(agents, a)
			}
		}
	}
	sort.Strings(agents)
	var titleV any = title
	if title == "" {
		titleV = info.title
	}
	header := py.D("type", "kb-snapshot", "version", py.IntOf(snapshotVersion), "session_id", sessionID,
		"title", titleV, "mode", mode, "filter", filter, "participants", []any{user}, "agents", agents,
		"source", src, "snapshot_at", isoSeconds(now()))
	addStamp(header)
	header.Set("branch_point", branchPoint)
	header.Set("turn_count", py.IntOf(len(turns)))
	header.Set("human_turns", py.IntOf(info.humanTurns))
	header.Set("dropped", info.dropped.dict())
	writeJSONL(dest, header, turns)
	println(fmt.Sprintf("wrote %s: %d turns (%d human, %d agent, %d client), mode=%s, filter=%s",
		rel(dest, root), len(turns), info.humanTurns, info.agentTurns, info.clientTurns, mode, filter))

	// The two pointers kb attest needs: the human's latest turn, and the agent's last text before it.
	excerpt := func(t *py.Dict) string {
		var parts []string
		for _, b := range py.Iter(item(t, "content")) {
			bd := py.AsDict(b)
			if py.Equal(bd.Get("type"), "text") {
				parts = append(parts, py.AsStr(blockText(bd)))
			}
		}
		text := strings.Join(py.Fields(strings.Join(parts, " ")), " ")
		if py.Len(text) > 100 {
			return py.Head(text, 100) + "..."
		}
		return text
	}
	hi := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if a := turns[i].Get("author"); py.Equal(turns[i].Get("role"), "user") && !py.Equal(a, "client") && !py.Equal(a, "tool") {
			hi = i
			break
		}
	}
	if hi >= 0 {
		println(fmt.Sprintf("last human turn: %s#%s: %s", py.Head(sessionID, 8), py.Str(turns[hi].Get("uuid")), excerpt(turns[hi])))
		for i := hi - 1; i >= 0; i-- {
			if !py.Equal(turns[i].Get("role"), "assistant") {
				continue
			}
			hasText := false
			for _, b := range py.Iter(turns[i].Get("content")) {
				if py.Equal(py.AsDict(b).Get("type"), "text") {
					hasText = true
				}
			}
			if hasText {
				println(fmt.Sprintf("agent text before it: %s#%s: %s", py.Head(sessionID, 8), py.Str(turns[i].Get("uuid")), excerpt(turns[i])))
				break
			}
		}
	}
	if mode == "off-record" {
		io.WriteString(Stderr, "note: off-record/ is gitignored; this snapshot is at risk of loss by design\n")
	}
	return 0
}

func addSnapshotParser(sub *argparse.Parser) {
	p := sub.AddParser("snapshot", "copy a session JSONL into evidence with a filter")
	p.Positional("session", argparse.Optional,
		"session id (or prefix), or path to a .jsonl; default: this conversation, from "+sessionEnv)
	p.Option("--mode", "on-record (evidence/) or off-record (off-record/sessions/)", "on-record", false)
	p.Option("--filter", strings.Join(filterProfiles, "|"), "human-visible-v1", false)
	p.Option("--user", "human participant id (else KB_USER / ~/.kenttheclerk.json)", nil, false)
	p.Option("--title", "override the session title", nil, false)
	p.Flag("--force", "allow changing filter profile / dropping turns")
	p.Run = runSnapshot
}
