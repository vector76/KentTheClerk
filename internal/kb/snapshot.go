package kb

// kb snapshot — copy a Claude Code session JSONL into evidence/ (or off-record/sessions/) with a filter.

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
	"kenttheclerk/internal/dyn"
)

var (
	systemReminderRE = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	clientNoticeRE   = regexp.MustCompile(`^\[(Request interrupted by user[^\]]*)\][` + dyn.SpaceClass + `]*\z`)
)

func claudeProjectsDir() string {
	if v, ok := os.LookupEnv("CLAUDE_PROJECTS_DIR"); ok {
		return dyn.PathStr(v)
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
		dyn.RaiseOS(err)
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
	for _, p := range dyn.Glob(filepath.Dir(src), "*.jsonl") {
		if mtime(p) > mt {
			newest = false
		}
	}
	if age > currentWithinSeconds && !newest {
		die(fmt.Sprintf("%s names session %s, but its file was last written %ds ago and a newer "+
			"session file exists beside it, so it may not be this conversation (for example after /clear); "+
			"pass the session id", sessionEnv, dyn.Head(sid, 8), int(age)))
	}
	return src
}

func locateSource(sessionID string) string {
	if dyn.Suffix(sessionID) == ".jsonl" && dyn.Exists(sessionID) {
		return dyn.PathStr(sessionID)
	}
	var hits []string
	for _, h := range dyn.Glob(claudeProjectsDir(), "*/"+sessionID+"*.jsonl") {
		if filepath.Base(filepath.Dir(h)) != "memory" {
			hits = append(hits, h)
		}
	}
	if len(hits) == 0 {
		die(fmt.Sprintf("no session JSONL matching %s under %s", dyn.StrRepr(sessionID), claudeProjectsDir()))
	}
	if len(hits) > 1 {
		die("ambiguous session id; matches:\n  " + strings.Join(hits, "\n  "))
	}
	return hits[0]
}

func toolCallSummary(block *dyn.Dict, profile string) *dyn.Dict {
	inp := dyn.Or(block.Get("input"), dyn.NewDict())
	out := dyn.D("type", "tool_use", "id", block.Get("id"), "name", block.Get("name"))
	if profile == "full-v1" {
		out.Set("input", inp)
		return out
	}
	in := dyn.AsDict(inp)
	desc := in.Get("description")
	if !dyn.Truthy(desc) {
		// Never a command or a prompt: the human usually sees only the description, and either can
		// carry what the human never saw, such as a token on a command line.
		for _, k := range []string{"file_path", "pattern", "query", "url"} {
			if s, ok := in.Get(k).(string); ok {
				desc = dyn.Head(s, 160)
				break
			}
		}
	}
	if dyn.Truthy(desc) {
		out.Set("description", desc)
	}
	return out
}

func blockText(b *dyn.Dict) any {
	if v, ok := b.Lookup("text"); ok {
		return v
	}
	return ""
}

func filterUser(rec *dyn.Dict, profile string) *dyn.Dict {
	msg := dyn.AsDict(dyn.Or(rec.Get("message"), dyn.NewDict()))
	content := msg.Get("content")
	origin := dyn.AsDict(dyn.Or(rec.Get("origin"), dyn.NewDict())).Get("kind")
	var blocks []any
	isHuman := dyn.Equal(origin, "human")
	allToolResults := true
	add := func(b *dyn.Dict) {
		if !dyn.Equal(b.Get("type"), "tool_result") {
			allToolResults = false
		}
		blocks = append(blocks, b)
	}
	switch c := content.(type) {
	case string:
		text := c
		if profile != "full-v1" {
			text = dyn.Strip(systemReminderRE.ReplaceAllString(text, ""))
		}
		if text == "" {
			return nil
		}
		if !isHuman && !clientNoticeRE.MatchString(text) && profile != "full-v1" {
			return nil
		}
		add(dyn.D("type", "text", "text", text))
	case []any:
		for _, bv := range c {
			b := dyn.AsDict(bv)
			t := b.Get("type")
			switch {
			case dyn.Equal(t, "text"):
				tv := blockText(b)
				if profile != "full-v1" {
					text := dyn.Strip(systemReminderRE.ReplaceAllString(dyn.AsStr(tv), ""))
					if text == "" {
						continue
					}
					if !isHuman && !clientNoticeRE.MatchString(text) {
						continue
					}
					tv = text
				}
				add(dyn.D("type", "text", "text", tv))
			case dyn.Equal(t, "tool_result"):
				if profile == "full-v1" {
					add(b)
				} else {
					cv, ok := b.Lookup("content")
					if !ok {
						cv = ""
					}
					raw := dyn.Dumps(cv)
					add(dyn.D("type", "tool_result", "tool_use_id", b.Get("tool_use_id"), "omitted", true, "size", dyn.IntOf(dyn.Len(raw))))
				}
			case dyn.Equal(t, "image") && profile == "full-v1":
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
	return dyn.D("type", "turn", "uuid", item(rec, "uuid"), "parent", rec.Get("parentUuid"),
		"timestamp", rec.Get("timestamp"), "role", "user", "author", author, "content", blocks)
}

func filterAssistant(rec *dyn.Dict, profile string) *dyn.Dict {
	msg := dyn.AsDict(dyn.Or(rec.Get("message"), dyn.NewDict()))
	var blocks []any
	content, ok := msg.Lookup("content")
	if !ok {
		content = []any{}
	}
	for _, bv := range dyn.Iter(content) {
		b := dyn.AsDict(bv)
		t := b.Get("type")
		switch {
		case dyn.Equal(t, "text"):
			if dyn.Strip(dyn.AsStr(blockText(b))) != "" {
				blocks = append(blocks, dyn.D("type", "text", "text", item(b, "text")))
			}
		case dyn.Equal(t, "tool_use"):
			blocks = append(blocks, toolCallSummary(b, profile))
		case dyn.Equal(t, "thinking"):
			if profile == "full-v1" {
				th, ok := b.Lookup("thinking")
				if !ok {
					th = ""
				}
				blocks = append(blocks, dyn.D("type", "thinking", "thinking", th))
			} else {
				blocks = append(blocks, dyn.D("type", "thinking", "omitted", true))
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
	return dyn.D("type", "turn", "uuid", item(rec, "uuid"), "parent", rec.Get("parentUuid"),
		"timestamp", rec.Get("timestamp"), "role", "assistant", "author", "agent:"+dyn.Str(model), "content", blocks)
}

// droppedCounts is info["dropped"]: counts whose keys may be any scalar JSON value; written as JSON,
// they become strings.
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
		id, key = "b"+dyn.Repr(k), map[bool]string{true: "true", false: "false"}[k]
		if k {
			id = "i1"
		} else {
			id = "i0"
		}
	case dyn.Int:
		id, key = "i"+string(k), string(k)
	case float64:
		id, key = "f"+dyn.FloatRepr(k), dyn.Dumps(k)
		if k == float64(int64(k)) {
			id = "i" + fmt.Sprint(int64(k))
		}
	default:
		dyn.Raise("TypeError", "%s cannot be used as a key", dyn.TypeName(kind))
	}
	if _, ok := d.n[id]; !ok {
		d.order = append(d.order, id)
		d.jsonKy[id] = key
	}
	d.n[id]++
}

func (d *droppedCounts) dict() *dyn.Dict {
	out := dyn.NewDict()
	for _, id := range d.order {
		out.Set(d.jsonKy[id], dyn.IntOf(d.n[id]))
	}
	return out
}

type projectInfo struct {
	title                               any
	dropped                             *droppedCounts
	humanTurns, agentTurns, clientTurns int
}

func project(records []*dyn.Dict, profile, humanUser string) ([]*dyn.Dict, *projectInfo) {
	var turns []*dyn.Dict
	info := &projectInfo{dropped: &droppedCounts{jsonKy: map[string]string{}, n: map[string]int{}}}
	for _, rec := range records {
		t := rec.Get("type")
		if dyn.Equal(t, "custom-title") {
			info.title = rec.Get("customTitle")
			continue
		}
		if dyn.Equal(t, "ai-title") && !dyn.Truthy(info.title) {
			info.title = rec.Get("aiTitle")
			continue
		}
		if !dyn.Equal(t, "user") && !dyn.Equal(t, "assistant") {
			info.dropped.add(t)
			continue
		}
		if dyn.Truthy(rec.Get("isSidechain")) {
			info.dropped.add("sidechain")
			continue
		}
		rec.Set("_author", humanUser)
		var out *dyn.Dict
		if dyn.Equal(t, "user") {
			out = filterUser(rec, profile)
		} else {
			out = filterAssistant(rec, profile)
		}
		if out == nil {
			info.dropped.add(t.(string) + ":empty-after-filter")
			continue
		}
		turns = append(turns, out)
		if dyn.Equal(out.Get("role"), "assistant") {
			info.agentTurns++
		} else if a := out.Get("author"); dyn.Equal(a, "client") || dyn.Equal(a, "tool") {
			info.clientTurns++
		} else {
			info.humanTurns++
		}
	}
	return turns, info
}

// hashKey makes a map key for a JSON scalar under which equal numbers and booleans meet (1, 1.0 and
// true are one key).
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
	case dyn.Int:
		return "i" + string(x), true
	case float64:
		if x == float64(int64(x)) {
			return "i" + fmt.Sprint(int64(x)), true
		}
		return "f" + dyn.FloatRepr(x), true
	}
	return "", false
}

// resolveBranchPoint is the parent-session turn this session forked from, or nil, following the
// first kept turn's parent through records of this same session that the filter dropped.
func resolveBranchPoint(records []*dyn.Dict, turns []*dyn.Dict) any {
	if len(turns) == 0 {
		return nil
	}
	own := map[string]*dyn.Dict{}
	for _, r := range records {
		if u := r.Get("uuid"); dyn.Truthy(u) {
			k, ok := hashKey(u)
			if !ok {
				dyn.Raise("TypeError", "%s cannot be used as a key", dyn.TypeName(u))
			}
			own[k] = r
		}
	}
	p := turns[0].Get("parent")
	seen := map[string]bool{}
	for dyn.Truthy(p) {
		k, ok := hashKey(p)
		if !ok {
			dyn.Raise("TypeError", "%s cannot be used as a key", dyn.TypeName(p))
		}
		r, in := own[k]
		if !in || seen[k] {
			break
		}
		seen[k] = true
		p = r.Get("parentUuid")
	}
	return dyn.Or(p, nil)
}

func readSource(path string) []*dyn.Dict {
	var recs []*dyn.Dict
	for n, line := range dyn.TextLines(path) {
		line = dyn.Strip(line)
		if line == "" {
			continue
		}
		v, err := dyn.Loads(line)
		if err != nil {
			die(fmt.Sprintf("%s:%d: bad JSON (%s)", path, n+1, err.Error()))
		}
		recs = append(recs, dyn.AsDict(v))
	}
	return recs
}

func writeJSONL(dest string, header *dyn.Dict, records []*dyn.Dict) {
	tmp := strings.TrimSuffix(dest, ".jsonl") + ".jsonl.tmp"
	var b strings.Builder
	for _, rec := range append([]*dyn.Dict{header}, records...) {
		line := dyn.Dumps(rec) + "\n"
		if dyn.SurrogateIndex(line) >= 0 {
			// The lines before this one go to the .tmp file, then encoding this one fails;
			// the record itself is left untouched.
			if err := os.WriteFile(tmp, []byte(b.String()), 0o666); err != nil {
				dyn.RaiseOS(err)
			}
			dyn.CheckEncodable(line)
		}
		b.WriteString(line)
	}
	if err := os.WriteFile(tmp, []byte(b.String()), 0o666); err != nil {
		dyn.RaiseOS(err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		dyn.RaiseOS(err)
	}
}

func runSnapshot(ns argparse.Namespace) int {
	root := repoRoot()
	requirePin(root)
	userArg, _ := ns.Str("user")
	user := currentUser(userArg, root)
	mode, _ := ns.Str("mode")
	filter, _ := ns.Str("filter")
	session, hasSession := ns.Str("session")
	title, _ := ns.Str("title")
	force := ns.Bool("force")
	if !in(mode, modes) {
		die("mode must be one of " + dyn.Tuple(modes...))
	}
	if !in(filter, filterProfiles) {
		die("filter must be one of " + dyn.Tuple(filterProfiles...))
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
		if sid := r.Get("sessionId"); dyn.Truthy(sid) {
			s := dyn.Str(sid)
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
			prefix = dyn.Stem(src)
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
			die(fmt.Sprintf("multiple sessionIds in %s: %s; pass the id of the one to snapshot", src, dyn.Repr(sorted)))
		}
		sessionID = wanted[0]
		var kept []*dyn.Dict
		for _, r := range records {
			if v := r.Get("sessionId"); v == nil || dyn.Equal(v, sessionID) {
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
		dyn.RaiseOS(err)
	}
	var firstTS any
	for _, t := range turns {
		if ts := t.Get("timestamp"); dyn.Truthy(ts) {
			firstTS = ts
			break
		}
	}
	date := ""
	if dyn.Truthy(firstTS) {
		date = dyn.Head(dyn.AsStr(firstTS), 10)
	}
	if date == "" {
		date = now().Format("2006-01-02")
	}
	dest := filepath.Join(destDir, date+"-"+dyn.Head(sessionID, 8)+".jsonl")

	// Append-only guard: a re-snapshot must be a superset of what was there.
	if dyn.Exists(dest) {
		var prev []any
		recorded := map[string]*dyn.Dict{}
		for _, line := range dyn.TextLines(dest) {
			v, err := dyn.Loads(line)
			if err != nil {
				dyn.Raise("JSON error", "%s", err.Error())
			}
			rec := dyn.AsDict(v)
			switch t := rec.Get("type"); {
			case dyn.Equal(t, "turn"):
				prev = append(prev, item(rec, "uuid"))
				if k, ok := hashKey(rec.Get("uuid")); ok {
					recorded[k] = rec
				}
			case dyn.Equal(t, "kb-snapshot"):
				if sc := rec.Get("scrubbed"); dyn.Truthy(sc) {
					die(fmt.Sprintf("%s was scrubbed by override %s; it is not snapshotted again (%s)", dest, dyn.Str(dyn.AsDict(sc).Get("override")), rule("override-scrub")))
				}
				if !dyn.Equal(rec.Get("filter"), filter) && !force {
					die(fmt.Sprintf("%s was taken with filter %s; pass --force to change profile (%s)", dest, dyn.Repr(rec.Get("filter")), rule("mode-fixed")))
				}
				if !dyn.Equal(rec.Get("mode"), mode) {
					die(fmt.Sprintf("%s has mode %s; a session's mode does not change (%s)", dest, dyn.Repr(rec.Get("mode")), rule("mode-fixed")))
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
			die(fmt.Sprintf("re-snapshot would drop %d previously recorded turn(s) (e.g. %s); refusing (%s)", len(missing), dyn.Str(missing[0]), rule("append-only")))
		}
		// A turn already recorded stays as it was recorded: a re-snapshot only adds turns, and it must not
		// undo an override, restoring a scrubbed turn or relabelling a renamed or reassigned one. A new
		// filter profile (--force) re-filters a turn's content, but never a scrubbed turn's, and keeps
		// its recorded author.
		for i, t := range turns {
			k, _ := hashKey(t.Get("uuid"))
			r := recorded[k]
			switch {
			case r == nil:
			case !force || dyn.Truthy(r.Get("scrubbed")):
				turns[i] = r
			default:
				t.Set("author", r.Get("author"))
			}
		}
	}
	// Same session must not exist in the other mode either.
	other := filepath.Join(root, "off-record", "sessions")
	if mode != "on-record" {
		other = filepath.Join(root, "evidence")
	}
	for _, p := range dyn.Glob(other, "*.jsonl") {
		if strings.Contains(filepath.Base(p), dyn.Head(sessionID, 8)) {
			die(fmt.Sprintf("session %s already exists under %s; a session has one mode (%s)", dyn.Head(sessionID, 8), other, rule("mode-fixed")))
		}
	}

	agentSet := map[string]bool{}
	agents := []string{}
	for _, t := range turns {
		if dyn.Equal(t.Get("role"), "assistant") {
			a := dyn.AsStr(t.Get("author"))
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
	header := dyn.D("type", "kb-snapshot", "version", dyn.IntOf(snapshotVersion), "session_id", sessionID,
		"title", titleV, "mode", mode, "filter", filter, "participants", []any{user}, "authentication", authentication, "agents", agents,
		"source", src, "snapshot_at", isoSeconds(now()))
	addStamp(header)
	header.Set("branch_point", branchPoint)
	header.Set("turn_count", dyn.IntOf(len(turns)))
	header.Set("human_turns", dyn.IntOf(info.humanTurns))
	header.Set("dropped", info.dropped.dict())
	writeJSONL(dest, header, turns)
	println(fmt.Sprintf("wrote %s: %d turns (%d human, %d agent, %d client), mode=%s, filter=%s",
		rel(dest, root), len(turns), info.humanTurns, info.agentTurns, info.clientTurns, mode, filter))

	// The two pointers kb attest needs: the human's latest turn, and the agent's last text before it.
	excerpt := func(t *dyn.Dict) string {
		var parts []string
		for _, b := range dyn.Iter(item(t, "content")) {
			bd := dyn.AsDict(b)
			if dyn.Equal(bd.Get("type"), "text") {
				parts = append(parts, dyn.AsStr(blockText(bd)))
			}
		}
		text := strings.Join(dyn.Fields(strings.Join(parts, " ")), " ")
		if dyn.Len(text) > 100 {
			return dyn.Head(text, 100) + "..."
		}
		return text
	}
	hi := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if a := turns[i].Get("author"); dyn.Equal(turns[i].Get("role"), "user") && !dyn.Equal(a, "client") && !dyn.Equal(a, "tool") {
			hi = i
			break
		}
	}
	if hi >= 0 {
		println(fmt.Sprintf("last human turn: %s#%s: %s", dyn.Head(sessionID, 8), dyn.Str(turns[hi].Get("uuid")), excerpt(turns[hi])))
		for i := hi - 1; i >= 0; i-- {
			if !dyn.Equal(turns[i].Get("role"), "assistant") {
				continue
			}
			hasText := false
			for _, b := range dyn.Iter(turns[i].Get("content")) {
				if dyn.Equal(dyn.AsDict(b).Get("type"), "text") {
					hasText = true
				}
			}
			if hasText {
				println(fmt.Sprintf("agent text before it: %s#%s: %s", dyn.Head(sessionID, 8), dyn.Str(turns[i].Get("uuid")), excerpt(turns[i])))
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
