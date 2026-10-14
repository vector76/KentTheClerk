// Package kb is the recorder of Kent the Clerk. It began as a port of the Python recorder (kb.py and
// kb/*.py, retired after commit d1333a5, where they can still be read); each command lives in the file
// named after the Python module it was ported from, and comments that cite kb/<module>.py refer to that
// version. This file was kb/common.py.
package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	kent "kenttheclerk"
	"kenttheclerk/internal/py"
)

const (
	snapshotVersion = 1
	documentVersion = 1
)

// productRules are Kent's rules (S-0057, S-0058, S-0077): every refusal cites the rule it enforces, by
// number. The numbers are those in rules/; this table is the one place the code holds them.
var productRules = map[string]int{
	"write-through-kb": 1, "append-only": 2, "no-pending-statements": 3, "change-by-supersession": 4,
	"documents-cannot-attest": 8, "frozen-projection": 9, "mode-fixed": 11, "on-record-only": 14,
	"fork-ancestry": 17, "own-turn": 21, "after-proposal": 22, "same-session": 23, "exact-text": 25,
	"attest-once": 26, "title-and-answers": 27, "agent-from-conversation": 31, "agent-own-turn": 32,
	"seen-in-full": 34, "agent-revision": 35, "agent-no-attestations": 37, "system-stamp": 44,
}

// rule is how a message cites a rule, e.g. "Kent Rule 7".
func rule(key string) string {
	n, ok := productRules[key]
	if !ok {
		py.Raise("KeyError", "%s", py.StrRepr(key))
	}
	return fmt.Sprintf("Kent Rule %d", n)
}

var (
	systemHashRE   = regexp.MustCompile(`^sha256:[0-9a-f]{64}\n?$`)
	originalHashRE = systemHashRE
	originalNameRE = regexp.MustCompile(`^([0-9a-f]{64})(\.[A-Za-z0-9]{1,12})?\n?$`)
	statementIDRE  = regexp.MustCompile(`^([SA])-\p{Nd}{4,}\n?$`)
)

var (
	filterProfiles   = []string{"human-visible-v1", "full-v1"}
	modes            = []string{"on-record", "off-record"}
	extractors       = []string{"text-v1", "html-v1", "pdf-v1", "docx-v1", "opaque-v1"}
	attestationForms = []string{"proposed-and-agreed", "agreed-with-modification", "self", "endorsement"}
	relations        = []string{"supersedes", "amends", "clarifies"}
	markKey          = map[string]string{"supersedes": "superseded_by", "amends": "amended_by", "clarifies": "clarified_by"}
	statementDirs    = map[string]string{"S": "attested", "A": "agent"}
)

func in(s string, l []string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- output and exits

// Stdout and Stderr are the streams commands print to. They are set up by Main.
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

func outf(format string, a ...any) { fmt.Fprintf(Stdout, format, a...) }

// println is print(...) to stdout.
func println(s string) { io.WriteString(Stdout, s+"\n") }

// exitCode is sys.exit(code), carried by panic to Main.
type exitCode int

// die prints "error: msg" to stderr and exits with status 1.
func die(msg string) {
	io.WriteString(Stderr, "error: "+msg+"\n")
	panic(exitCode(1))
}

func warn(msg string) { io.WriteString(Stderr, "warning: "+msg+"\n") }

// now is datetime.now(timezone.utc).
func now() time.Time { return time.Now().UTC() }

// isoSeconds is datetime.isoformat(timespec="seconds") for a UTC time.
func isoSeconds(t time.Time) string { return t.Format("2006-01-02T15:04:05") + "+00:00" }

// ---------------------------------------------------------------- knowledge base location

// Kent is installed outside the customer's repositories, and the knowledge folder usually sits beside
// the code repository rather than in it (S-0084). Its location comes per user and per project from
// KENT_KB (S-0085); without it, kb looks for the record in the current folder and above.
const kbEnv = "KENT_KB"

func isKBFolder(p string) bool {
	return py.IsDir(filepath.Join(p, "attested")) && py.IsDir(filepath.Join(p, "evidence"))
}

// findKBRoot says where the knowledge base is and how it was found: (path or "", explanation).
func findKBRoot() (string, string) {
	if env := os.Getenv(kbEnv); env != "" {
		p := py.Resolve(env)
		if isKBFolder(p) {
			return p, "from " + kbEnv
		}
		return "", fmt.Sprintf("%s names %s, which has no attested/ and evidence/ folders", kbEnv, p)
	}
	cwd, err := os.Getwd()
	if err != nil {
		py.RaiseOS(err)
	}
	p := py.Resolve(cwd)
	for cand := p; ; {
		if isKBFolder(cand) {
			if cand == p {
				return cand, "from the current folder"
			}
			return cand, "from a folder above the current one"
		}
		parent := filepath.Dir(cand)
		if parent == cand {
			break
		}
		cand = parent
	}
	return "", kbEnv + " is not set, and no attested/ and evidence/ folders were found in the current " +
		"folder or above"
}

func repoRoot() string {
	root, how := findKBRoot()
	if root == "" {
		die("no knowledge base found: " + how)
	}
	return root
}

// rel is str(p.relative_to(root)).
func rel(p, root string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}

// ---------------------------------------------------------------- system identity

// stampField is one field of the system stamp.
type stampField struct {
	key   string
	value any
}

// systemStamp is the fields every recorded act carries (S-0030), in order. An act made under a
// development build also carries the commit the build came from and whether its tree was dirty (S-0109).
func systemStamp() []stampField {
	out := []stampField{{"system_version", kent.Version()}, {"system_hash", kent.Hash()}}
	if kent.Dev() {
		out = append(out, stampField{"system_build", "dev"}, stampField{"system_commit", kent.Commit()},
			stampField{"system_dirty", kent.Dirty()})
	}
	return out
}

// addStamp adds the system stamp to d, each key after the given prefix ("" or e.g. "withdrawn_").
func addStamp(d *py.Dict, prefix ...string) {
	pre := strings.Join(prefix, "")
	for _, f := range systemStamp() {
		d.Set(pre+f.key, f.value)
	}
}

// ---------------------------------------------------------------- identity

// authentication is how the author of an act is known (S-0055). The user id is asserted, not verified,
// and every act records it as asserted (S-0113).
const authentication = "asserted"

// currentUser is the user id: --user flag, else KB_USER env, else ~/.kenttheclerk.json {"user": ...}
// (the older ~/.convkb.json is still read if the new file is absent), else git's user.email as git
// reports it in the knowledge base's folder, so a setting in the knowledge base's own repository wins
// (S-0113).
func currentUser(explicit, root string) string {
	if explicit != "" {
		return explicit
	}
	if u := os.Getenv("KB_USER"); u != "" {
		return u
	}
	home, _ := os.UserHomeDir()
	for _, name := range []string{".kenttheclerk.json", ".convkb.json"} {
		cfg := filepath.Join(home, name)
		if py.Exists(cfg) {
			v, err := py.Loads(py.ReadText(cfg))
			if err != nil {
				continue
			}
			if u := py.AsDict(v).Get("user"); py.Truthy(u) {
				return py.Str(u)
			}
		}
	}
	if u := gitUserEmail(root); u != "" {
		return u
	}
	die(`no user id: pass --user, set KB_USER, write ~/.kenttheclerk.json with {"user": "<id>"}, or set ` +
		`git's user.email`)
	return ""
}

// gitUserEmail is `git config user.email` run in dir, or "" when git is missing or the setting is unset.
func gitUserEmail(dir string) string {
	cmd := exec.Command("git", "config", "user.email")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------- pointers

var pointerRE = regexp.MustCompile(`^([0-9a-f-]{8,})#([0-9a-f-]{8,})\n?$`)

// Pointer is <session>#<uuid>.
type Pointer struct{ Session, UUID string }

func (p Pointer) String() string { return p.Session + "#" + p.UUID }

// parsePointer is Pointer.parse; ok is false (after printing the error, as die does) where Python dies.
func tryParsePointer(s string) (Pointer, bool) {
	m := pointerRE.FindStringSubmatch(py.Strip(s))
	if m == nil {
		io.WriteString(Stderr, "error: "+fmt.Sprintf("bad evidence pointer %s; expected <session-id>#<turn-uuid>", py.StrRepr(s))+"\n")
		return Pointer{}, false
	}
	return Pointer{m[1], m[2]}, true
}

func parsePointer(s string) Pointer {
	p, ok := tryParsePointer(s)
	if !ok {
		panic(exitCode(1))
	}
	return p
}

// parsePointerValue is Pointer.parse(v) for a value read from a file, which Python would call .strip() on.
func parsePointerValue(v any) Pointer {
	return parsePointer(strArg(v))
}

// strArg is v where Python calls a str method on it: anything else raises AttributeError.
func strArg(v any) string {
	s, ok := v.(string)
	if !ok {
		py.Raise("AttributeError", "'%s' object has no attribute 'strip'", py.TypeName(v))
	}
	return s
}

// ---------------------------------------------------------------- evidence

// Session is one evidence item: a conversation snapshot (turns) or a document projection (chunks;
// S-0038). Both are a header record followed by uuid-keyed records.
type Session struct {
	Path   string
	Header *py.Dict
	Turns  []*py.Dict
}

func item(d *py.Dict, k string) any {
	v, ok := d.Lookup(k)
	if !ok {
		py.Raise("KeyError", "%s", py.StrRepr(k))
	}
	return v
}

func itemStr(d *py.Dict, k string) string { return py.AsStr(item(d, k)) }

func (s *Session) IDValue() any {
	if v := s.Header.Get("session_id"); py.Truthy(v) {
		return v
	}
	return item(s.Header, "document_id")
}

// ID is the session or document id.
func (s *Session) ID() string { return py.AsStr(s.IDValue()) }

// Kind is "document" for a projection, else "session".
func (s *Session) Kind() string {
	if py.Equal(s.Header.Get("type"), "kb-document") {
		return "document"
	}
	return "session"
}

// ModeValue is header.get("mode", "on-record").
func (s *Session) ModeValue() any {
	if v, ok := s.Header.Lookup("mode"); ok {
		return v
	}
	return "on-record"
}

// Mode is the mode as a str (non-str modes compare unequal to every mode name, as in Python).
func (s *Session) Mode() string {
	if m, ok := s.ModeValue().(string); ok {
		return m
	}
	return py.Str(s.ModeValue())
}

func uuidMatches(t *py.Dict, uuid string) bool {
	u := item(t, "uuid")
	if py.Equal(u, uuid) {
		return true
	}
	return strings.HasPrefix(py.AsStr(u), uuid)
}

// Turn finds a turn by uuid or uuid prefix.
func (s *Session) Turn(uuid string) *py.Dict {
	for _, t := range s.Turns {
		if uuidMatches(t, uuid) {
			return t
		}
	}
	return nil
}

// TurnIndex is the index of that turn, or -1.
func (s *Session) TurnIndex(uuid string) int {
	for i, t := range s.Turns {
		if uuidMatches(t, uuid) {
			return i
		}
	}
	return -1
}

// BranchPoint is the uuid of the parent-session turn this session forked from, else nil. Snapshots
// record it in the header; older ones fall back to the first turn's parent.
func (s *Session) BranchPoint() any {
	if v, ok := s.Header.Lookup("branch_point"); ok {
		return py.Or(v, nil)
	}
	if len(s.Turns) == 0 {
		return nil
	}
	return py.Or(s.Turns[0].Get("parent"), nil)
}

func loadSession(path string) *Session {
	var header *py.Dict
	var turns []*py.Dict
	for _, line := range py.TextLines(path) {
		line = py.Strip(line)
		if line == "" {
			continue
		}
		v, err := py.Loads(line)
		if err != nil {
			py.Raise("json.decoder.JSONDecodeError", "%s", err.Error())
		}
		rec := py.AsDict(v)
		if t := rec.Get("type"); py.Equal(t, "kb-snapshot") || py.Equal(t, "kb-document") {
			header = rec
		} else {
			turns = append(turns, rec)
		}
	}
	if header == nil {
		die(fmt.Sprintf("%s has no kb-snapshot / kb-document header", path))
	}
	return &Session{path, header, turns}
}

type modeDir struct{ dir, mode string }

// evidenceDirs are the (directory, mode) pairs to scan. off-record/sessions and off-record/documents
// are scanned when present (client-side; never part of the record).
func evidenceDirs(root string) []modeDir {
	out := []modeDir{{filepath.Join(root, "evidence"), "on-record"}}
	for _, sub := range []string{"sessions", "documents"} {
		off := filepath.Join(root, "off-record", sub)
		if py.IsDir(off) {
			out = append(out, modeDir{off, "off-record"})
		}
	}
	return out
}

// originalsDir is where original document bytes live, named by content hash (S-0039).
func originalsDir(root, mode string) string {
	if mode == "on-record" {
		return filepath.Join(root, "evidence", "originals")
	}
	return filepath.Join(root, "off-record", "originals")
}

func sha256File(path string) string {
	f, err := os.Open(path)
	if err != nil {
		py.RaiseOS(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		py.RaiseOS(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Sessions is the dict load_all_sessions returns: insertion-ordered by id.
type Sessions struct {
	order []string
	m     map[string]*Session
}

func (ss *Sessions) Values() []*Session {
	out := make([]*Session, 0, len(ss.order))
	for _, k := range ss.order {
		out = append(out, ss.m[k])
	}
	return out
}

func (ss *Sessions) Len() int { return len(ss.order) }

func sortedGlob(dir, pat string) []string {
	g := py.Glob(dir, pat)
	py.SortPaths(g)
	return g
}

func loadAllSessions(root string) *Sessions {
	out := &Sessions{m: map[string]*Session{}}
	for _, md := range evidenceDirs(root) {
		for _, p := range sortedGlob(md.dir, "*.jsonl") {
			s := loadSession(p)
			if !py.Equal(s.ModeValue(), md.mode) {
				warn(fmt.Sprintf("%s: header mode %s disagrees with directory (%s)", p, py.Repr(s.ModeValue()), md.mode))
			}
			id := s.ID()
			if prev, ok := out.m[id]; ok {
				warn(fmt.Sprintf("duplicate session %s: %s and %s", id, p, prev.Path))
			} else {
				out.order = append(out.order, id)
			}
			out.m[id] = s
		}
	}
	return out
}

func sessionByPrefix(ss *Sessions, prefix string) *Session {
	for _, s := range ss.Values() {
		if strings.HasPrefix(s.ID(), prefix) {
			return s
		}
	}
	return nil
}

type ancestor struct {
	s      *Session
	cutoff int
}

// ancestry is a fork's history beyond its own turns (S-0034), nearest first; cutoff is the index of the
// branch-point turn in that ancestor. Stops where a branch point resolves in no loaded session.
func ancestry(ss *Sessions, s *Session) []ancestor {
	var out []ancestor
	seen := map[string]bool{s.ID(): true}
	cur := s
	for {
		bp := cur.BranchPoint()
		if !py.Truthy(bp) {
			break
		}
		var hit *ancestor
		for _, cand := range ss.Values() {
			if seen[cand.ID()] {
				continue
			}
			if i := cand.TurnIndex(py.AsStr(bp)); i >= 0 {
				hit = &ancestor{cand, i}
				break
			}
		}
		if hit == nil {
			break
		}
		out = append(out, *hit)
		seen[hit.s.ID()] = true
		cur = hit.s
	}
	return out
}

// resolvePointer resolves <session>#<uuid> to (session holding the turn, turn): the named session
// first, then its ancestry up to each branch point. (named-or-nil, nil) when not found.
func resolvePointer(ss *Sessions, p Pointer) (*Session, *py.Dict) {
	s := sessionByPrefix(ss, p.Session)
	if s == nil {
		return nil, nil
	}
	if t := s.Turn(p.UUID); t != nil {
		return s, t
	}
	for _, a := range ancestry(ss, s) {
		if i := a.s.TurnIndex(p.UUID); i >= 0 && i <= a.cutoff {
			return a.s, a.s.Turns[i]
		}
	}
	return s, nil
}

func findSession(root, sessionID string) *Session {
	for _, md := range evidenceDirs(root) {
		for _, p := range py.Glob(md.dir, "*.jsonl") {
			if strings.Contains(filepath.Base(p), sessionID) {
				return loadSession(p)
			}
		}
	}
	for _, s := range loadAllSessions(root).Values() {
		if strings.HasPrefix(s.ID(), sessionID) {
			return s
		}
	}
	return nil
}

// turnText is the visible text of a turn (text blocks joined).
func turnText(t *py.Dict) string {
	var parts []string
	for _, b := range py.Iter(contentOrEmpty(t)) {
		bd := py.AsDict(b)
		if py.Equal(bd.Get("type"), "text") {
			v, ok := bd.Lookup("text")
			if !ok {
				v = ""
			}
			parts = append(parts, py.AsStr(v))
		}
	}
	return strings.Join(parts, "\n")
}

func contentOrEmpty(t *py.Dict) any {
	if v, ok := t.Lookup("content"); ok {
		return v
	}
	return []any{}
}

// ---------------------------------------------------------------- statements

var frontmatterRE = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?(.*)$`)

// Statement is a statement file: frontmatter and body.
type Statement struct {
	Path string
	Meta *py.Dict
	Body string
}

// ID is meta.get("id").
func (st *Statement) ID() any { return st.Meta.Get("id") }

func parseScalar(v string) any {
	v = py.Strip(v)
	if (strings.HasPrefix(v, "'") || strings.HasPrefix(v, `"`)) && strings.HasSuffix(v, v[:1]) && py.Len(v) >= 2 {
		return v[1 : len(v)-1]
	}
	switch v {
	case "null", "~", "":
		return nil
	case "true":
		return true
	case "false":
		return false
	}
	return v
}

func startsIndented(l string) bool { return strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") }

// parseFrontmatter is the minimal YAML subset: key: scalar | key: [a, b] | key:\n  - item |
// key:\n  - k: v\n    k2: v2
func parseFrontmatter(text string) (*py.Dict, string) {
	m := frontmatterRE.FindStringSubmatch(text)
	if m == nil {
		return py.NewDict(), text
	}
	raw, body := m[1], m[2]
	meta := py.NewDict()
	lines := py.Splitlines(raw)
	i := 0
	for i < len(lines) {
		line := lines[i]
		if py.Strip(line) == "" || strings.HasPrefix(py.LStrip(line), "#") {
			i++
			continue
		}
		if startsIndented(line) {
			die(fmt.Sprintf("unexpected indentation in frontmatter: %s", py.StrRepr(line)))
		}
		key, _, rest := py.Partition(line, ":")
		key = py.Strip(key)
		rest = py.Strip(rest)
		if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
			inner := py.Strip(rest[1 : len(rest)-1])
			items := []any{}
			if inner != "" {
				for _, x := range strings.Split(inner, ",") {
					items = append(items, parseScalar(x))
				}
			}
			meta.Set(key, items)
			i++
			continue
		}
		if rest != "" {
			meta.Set(key, parseScalar(rest))
			i++
			continue
		}
		items := []any{}
		i++
		for i < len(lines) && (startsIndented(lines[i]) || py.Strip(lines[i]) == "") {
			l := lines[i]
			if py.Strip(l) == "" {
				i++
				continue
			}
			s := py.Strip(l)
			if strings.HasPrefix(s, "- ") {
				s = s[2:]
				if strings.Contains(s, ":") && !strings.HasPrefix(s, "'") && !strings.HasPrefix(s, `"`) {
					obj := py.NewDict()
					k, _, v := py.Partition(s, ":")
					obj.Set(py.Strip(k), parseScalar(v))
					i++
					for i < len(lines) && startsIndented(lines[i]) && !strings.HasPrefix(py.Strip(lines[i]), "- ") && py.Strip(lines[i]) != "" {
						k, _, v := py.Partition(py.Strip(lines[i]), ":")
						obj.Set(py.Strip(k), parseScalar(v))
						i++
					}
					items = append(items, obj)
					continue
				}
				items = append(items, parseScalar(s))
			}
			i++
		}
		meta.Set(key, items)
	}
	return meta, body
}

func fmQuote(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	s := py.Str(v)
	if s == "" || strings.ContainsAny(s, ":#[]{},\"'") || s != py.Strip(s) {
		return py.JSONStr(s)
	}
	return s
}

func dumpFrontmatter(meta *py.Dict) string {
	out := []string{"---"}
	for _, k := range meta.Keys() {
		v := meta.Get(k)
		if l, ok := v.([]any); ok {
			if len(l) == 0 {
				out = append(out, k+": []")
				continue
			}
			out = append(out, k+":")
			for _, it := range l {
				if d, ok := it.(*py.Dict); ok {
					first := true
					for _, ik := range d.Keys() {
						prefix := "    "
						if first {
							prefix = "  - "
						}
						out = append(out, prefix+ik+": "+fmQuote(d.Get(ik)))
						first = false
					}
				} else {
					out = append(out, "  - "+fmQuote(it))
				}
			}
		} else {
			out = append(out, k+": "+fmQuote(v))
		}
	}
	out = append(out, "---")
	return strings.Join(out, "\n") + "\n"
}

func loadStatement(path string) *Statement {
	meta, body := parseFrontmatter(py.ReadText(path))
	return &Statement{path, meta, strings.Trim(body, "\n")}
}

func writeStatement(path string, st *Statement) {
	py.WriteText(path, dumpFrontmatter(st.Meta)+"\n"+st.Body+"\n")
}

func loadAllStatements(root string) []*Statement {
	var out []*Statement
	for _, p := range sortedGlob(filepath.Join(root, "attested"), "S-*.md") {
		out = append(out, loadStatement(p))
	}
	return out
}

func loadAllAgentStatements(root string) []*Statement {
	var out []*Statement
	for _, p := range sortedGlob(filepath.Join(root, "agent"), "A-*.md") {
		out = append(out, loadStatement(p))
	}
	return out
}

// leadingDigits is re.match(rf"{kind}-(\d+)", name) -> int(group 1).
func leadingNumber(name, kind string) (int, bool) {
	if !strings.HasPrefix(name, kind+"-") {
		return 0, false
	}
	var digits strings.Builder
	for _, r := range name[len(kind)+1:] {
		if !py.IsDecimal(r) {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return 0, false
	}
	n, ok := py.ParseInt(digits.String())
	if !ok {
		return 0, false
	}
	return int(n.Int64()), true
}

func nextStatementID(root, kind string) string {
	d := filepath.Join(root, statementDirs[kind])
	maxN, any := 0, false
	for _, p := range py.Glob(d, kind+"-*.md") {
		if n, ok := leadingNumber(filepath.Base(p), kind); ok {
			if !any || n > maxN {
				maxN = n
			}
			any = true
		}
	}
	next := 1
	if any {
		next = maxN + 1
	}
	return fmt.Sprintf("%s-%04d", kind, next)
}

// statementPath is the file of a human (S-) or agent (A-) statement by id, or "".
func statementPath(root string, sid any) string {
	s := ""
	if py.Truthy(sid) {
		s = py.Str(sid)
	}
	m := statementIDRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	hits := py.Glob(filepath.Join(root, statementDirs[m[1]]), s+"-*.md")
	if len(hits) == 1 {
		return hits[0]
	}
	return ""
}

// markRelation puts the non-modification marks on the original (S-0016): superseded_by is single;
// amended_by/clarified_by accumulate.
func markRelation(root string, oldID any, relation, newID string) {
	p := statementPath(root, oldID)
	if p == "" {
		warn(fmt.Sprintf("%s %s: statement not found; not marking", relation, py.Str(oldID)))
		return
	}
	st := loadStatement(p)
	key := markKey[relation]
	if relation == "supersedes" {
		st.Meta.Set(key, newID)
	} else {
		lst := py.Or(st.Meta.Get(key), []any{})
		l, ok := lst.([]any)
		if !ok {
			py.Raise("AttributeError", "'%s' object has no attribute 'append'", py.TypeName(lst))
		}
		found := false
		for _, x := range l {
			if py.Equal(x, newID) {
				found = true
			}
		}
		if !found {
			l = append(l, newID)
		}
		st.Meta.Set(key, l)
	}
	writeStatement(p, st)
	println(fmt.Sprintf("%s marked %s %s", py.Str(oldID), key, newID))
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string, limit int) string {
	s = strings.Trim(slugRE.ReplaceAllString(py.Lower(s), "-"), "-")
	return strings.TrimRight(py.Head(s, limit), "-")
}

// versionTuple is "0.3.0" -> (0, 3, 0); junk gives (0,). A pre-release or build suffix is dropped, so
// "0.9.0-dev" is (0, 9, 0): a development build follows the rules of the version it is becoming.
func versionTuple(v any) []int64 {
	var out []int64
	core, _, _ := strings.Cut(py.Str(v), "-")
	core, _, _ = strings.Cut(core, "+")
	for _, x := range strings.Split(core, ".") {
		n, ok := py.ParseInt(x)
		if !ok {
			return []int64{0}
		}
		out = append(out, n.Int64())
	}
	return out
}

func tupleGE(a, b []int64) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) >= len(b)
}

// normalizeWS is re.sub(r"\s+", " ", s).strip().
func normalizeWS(s string) string { return strings.Join(py.Fields(s), " ") }

// textContained is the whitespace-insensitive substring test used for verified copies.
func textContained(needle, haystack string) bool {
	return strings.Contains(normalizeWS(haystack), normalizeWS(needle))
}

// draftPath is the draft argument as attest and record resolve it: relative to the knowledge base.
func draftPath(root, draft string) string {
	if strings.HasPrefix(draft, "/") || strings.HasPrefix(draft, `\`) || py.Head(py.Tail(draft, 1), 1) == ":" {
		return draft
	}
	return filepath.Join(root, draft)
}

func itoa(n int) string { return strconv.Itoa(n) }
