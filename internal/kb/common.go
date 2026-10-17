// Package kb is the recorder of Kent the Clerk, the kb command. Each command lives in a file named
// after it; this file holds what they share: output and exits, finding the knowledge base, the system's
// and the user's identity, evidence pointers, and reading evidence and statements.
package kb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	kent "kenttheclerk"
	"kenttheclerk/internal/dyn"
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
	"pinned-version": 44, "author-only": 46, "context-for": 47, "override-list": 50, "override-on-record": 51,
	"override-confirm": 52, "override-revise": 53, "override-scrub": 54, "user-table": 55, "add-first": 56,
	"acting-key": 57, "user-change": 58,
}

// rule is how a message cites a rule, saying how to read it (S-0106), e.g. "Kent Rule 7: `kb rules 7` to
// view it".
func rule(key string) string {
	n, ok := productRules[key]
	if !ok {
		dyn.Raise("KeyError", "%s", dyn.StrRepr(key))
	}
	return fmt.Sprintf("Kent Rule %d: `kb rules %d` to view it", n, n)
}

var (
	systemHashRE   = regexp.MustCompile(`^sha256:[0-9a-f]{64}\n?$`)
	originalHashRE = systemHashRE
	originalNameRE = regexp.MustCompile(`^([0-9a-f]{64})(\.[A-Za-z0-9]{1,12})?\n?$`)
	statementIDRE  = regexp.MustCompile(`^([SA])-` + idBody + `\n?$`)
)

// An id's body is characters from Crockford's base32 alphabet (S-0149): six for a new statement's (S-N5TZ),
// and four or five for those given before it, which stay valid. The ids given before S-0149 were
// sequential numbers, which the alphabet's digits already cover; the second branch keeps the digits-only
// form the earlier pattern allowed.
const (
	idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	idBody     = `(?:[0-9A-HJKMNP-TV-Z]{4,6}|\p{Nd}{4,})`
	// statementIDLen is the length of a new statement's id after its prefix (S-N5TZ); overrideIDLen is a new
	// override's (S-QYEK), which S-N5TZ does not change
	statementIDLen = 6
	overrideIDLen  = 4
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

// println prints s and a newline to stdout.
func println(s string) { io.WriteString(Stdout, s+"\n") }

// exitCode is an exit with that status, carried by panic to Main.
type exitCode int

// die prints "error: msg" to stderr and exits with status 1.
func die(msg string) {
	io.WriteString(Stderr, "error: "+msg+"\n")
	panic(exitCode(1))
}

func warn(msg string) { io.WriteString(Stderr, "warning: "+msg+"\n") }

// now is the current time in UTC.
var now = func() time.Time { return time.Now().UTC() }

// isoSeconds is a UTC time in ISO 8601 to the second, with "+00:00" for the zone.
func isoSeconds(t time.Time) string { return t.Format("2006-01-02T15:04:05") + "+00:00" }

// ---------------------------------------------------------------- knowledge base location

// Kent is installed outside the customer's repositories, and the knowledge folder usually sits beside
// the code repository rather than in it (S-0084). Its location comes per user and per project from
// KENT_KB (S-0085); without it, kb looks for the record in the current folder and above.
const kbEnv = "KENT_KB"

func isKBFolder(p string) bool {
	return dyn.IsDir(filepath.Join(p, "attested")) && dyn.IsDir(filepath.Join(p, "evidence"))
}

// findKBRoot says where the knowledge base is and how it was found: (path or "", explanation).
func findKBRoot() (string, string) {
	if env := os.Getenv(kbEnv); env != "" {
		p := dyn.Resolve(env)
		if isKBFolder(p) {
			return p, "from " + kbEnv
		}
		return "", fmt.Sprintf("%s names %s, which has no attested/ and evidence/ folders", kbEnv, p)
	}
	cwd, err := os.Getwd()
	if err != nil {
		dyn.RaiseOS(err)
	}
	p := dyn.Resolve(cwd)
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

// rel is p relative to root (p itself when it cannot be made relative).
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
func addStamp(d *dyn.Dict, prefix ...string) {
	pre := strings.Join(prefix, "")
	for _, f := range systemStamp() {
		d.Set(pre+f.key, f.value)
	}
}

// ---------------------------------------------------------------- identity

// authentication is how the author of an act is known (S-0055). The user id is asserted, not verified,
// and every act records it as asserted (S-0113).
const authentication = "asserted"

// currentUser is the user id: --user flag, else KB_USER env, else ~/.kenttheclerk.json (an entry under
// "folders" for the folder holding the knowledge base, else "user"; the older ~/.convkb.json is still read
// if the new file is absent), else git's user.email as git reports it in the knowledge base's folder, so a
// setting in the knowledge base's own repository wins over the global one (S-0113, S-0123).
//
//	{"user": "jamie", "folders": {"C:/work/client": "jamie@client.example"}}
//
// A folder entry applies to a knowledge base in that folder or anywhere below it; the longest match wins,
// so one person can be known by different ids to different knowledge bases.
//
// The id is a key in the user table, or, from git, an email the table maps to a key (S-0143). Someone not in
// the table cannot act until they are added (S-0140), and an id an override renamed or merged away acts here
// no more.
func currentUser(explicit, root string) string {
	if u, source := userSource(explicit, root); u != "" {
		key, msg := actingKey(root, u, source)
		if msg != "" {
			die(msg)
		}
		return key
	}
	die(`no user id: pass --user, set KB_USER, write ~/.kenttheclerk.json with {"user": "<id>"}, or set ` +
		`git's user.email`)
	return ""
}

// userSource is the user id currentUser would use and where it comes from, or ("", "") when there is none.
// With root "" (no knowledge base chosen yet) folder entries and git's user.email are not consulted.
func userSource(explicit, root string) (string, string) {
	if explicit != "" {
		return explicit, "from --user"
	}
	if u := os.Getenv("KB_USER"); u != "" {
		return u, "from KB_USER"
	}
	home, _ := os.UserHomeDir()
	for _, name := range []string{".kenttheclerk.json", ".convkb.json"} {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			continue
		}
		var cfg struct {
			User    string            `json:"user"`
			Folders map[string]string `json:"folders"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			continue
		}
		if root != "" {
			best, id := "", ""
			for folder, u := range cfg.Folders {
				f := folder
				if strings.HasPrefix(f, "~/") || strings.HasPrefix(f, `~\`) {
					f = filepath.Join(home, f[2:])
				}
				if u != "" && underFolder(root, f) && len(filepath.Clean(f)) > len(best) {
					best, id = filepath.Clean(f), u
				}
			}
			if id != "" {
				return id, "from ~/" + name + ", for " + best
			}
		}
		if cfg.User != "" {
			return cfg.User, "from ~/" + name
		}
	}
	if root != "" {
		if u := gitUserEmail(root); u != "" {
			return u, gitEmailSource
		}
	}
	return "", ""
}

// underFolder reports whether path is folder or inside it, comparing as the file system would.
func underFolder(path, folder string) bool {
	p, f := filepath.Clean(path), filepath.Clean(folder)
	if a, err := filepath.Abs(f); err == nil {
		f = a
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p, f = strings.ToLower(p), strings.ToLower(f)
	}
	return p == f || strings.HasPrefix(p, strings.TrimSuffix(f, string(filepath.Separator))+string(filepath.Separator))
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

// tryParsePointer parses "<session-id>#<turn-uuid>", ignoring surrounding whitespace; ok is false (after
// printing the error, as die does) where parsePointer exits.
func tryParsePointer(s string) (Pointer, bool) {
	m := pointerRE.FindStringSubmatch(dyn.Strip(s))
	if m == nil {
		io.WriteString(Stderr, "error: "+fmt.Sprintf("bad evidence pointer %s; expected <session-id>#<turn-uuid>", dyn.StrRepr(s))+"\n")
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

// parsePointerValue is parsePointer for a value read from a file, which must be a string.
func parsePointerValue(v any) Pointer {
	return parsePointer(strArg(v))
}

// strArg is v where a string is needed to strip it: anything else raises TypeError.
func strArg(v any) string {
	s, ok := v.(string)
	if !ok {
		dyn.Raise("TypeError", "expected a string, got %s", dyn.TypeName(v))
	}
	return s
}

// ---------------------------------------------------------------- evidence

// Session is one evidence item: a conversation snapshot (turns) or a document projection (chunks;
// S-0038). Both are a header record followed by uuid-keyed records.
type Session struct {
	Path   string
	Header *dyn.Dict
	Turns  []*dyn.Dict
}

func item(d *dyn.Dict, k string) any {
	v, ok := d.Lookup(k)
	if !ok {
		dyn.Raise("KeyError", "%s", dyn.StrRepr(k))
	}
	return v
}

func itemStr(d *dyn.Dict, k string) string { return dyn.AsStr(item(d, k)) }

func (s *Session) IDValue() any {
	if v := s.Header.Get("session_id"); dyn.Truthy(v) {
		return v
	}
	return item(s.Header, "document_id")
}

// ID is the session or document id.
func (s *Session) ID() string { return dyn.AsStr(s.IDValue()) }

// Kind is "document" for a projection, else "session".
func (s *Session) Kind() string {
	if dyn.Equal(s.Header.Get("type"), "kb-document") {
		return "document"
	}
	return "session"
}

// ModeValue is the header's mode, "on-record" when it has none.
func (s *Session) ModeValue() any {
	if v, ok := s.Header.Lookup("mode"); ok {
		return v
	}
	return "on-record"
}

// Mode is the mode as a string (a mode that is not a string compares unequal to every mode name).
func (s *Session) Mode() string {
	if m, ok := s.ModeValue().(string); ok {
		return m
	}
	return dyn.Str(s.ModeValue())
}

func uuidMatches(t *dyn.Dict, uuid string) bool {
	u := item(t, "uuid")
	if dyn.Equal(u, uuid) {
		return true
	}
	return strings.HasPrefix(dyn.AsStr(u), uuid)
}

// Turn finds a turn by uuid or uuid prefix.
func (s *Session) Turn(uuid string) *dyn.Dict {
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
		return dyn.Or(v, nil)
	}
	if len(s.Turns) == 0 {
		return nil
	}
	return dyn.Or(s.Turns[0].Get("parent"), nil)
}

func loadSession(path string) *Session {
	var header *dyn.Dict
	var turns []*dyn.Dict
	for _, line := range dyn.TextLines(path) {
		line = dyn.Strip(line)
		if line == "" {
			continue
		}
		v, err := dyn.Loads(line)
		if err != nil {
			dyn.Raise("JSON error", "%s", err.Error())
		}
		rec := dyn.AsDict(v)
		if t := rec.Get("type"); dyn.Equal(t, "kb-snapshot") || dyn.Equal(t, "kb-document") {
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
		if dyn.IsDir(off) {
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
		dyn.RaiseOS(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		dyn.RaiseOS(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Sessions is what loadAllSessions returns: the sessions by id, in the order they were added.
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
	g := dyn.Glob(dir, pat)
	dyn.SortPaths(g)
	return g
}

func loadAllSessions(root string) *Sessions {
	out := &Sessions{m: map[string]*Session{}}
	for _, md := range evidenceDirs(root) {
		for _, p := range sortedGlob(md.dir, "*.jsonl") {
			s := loadSession(p)
			if !dyn.Equal(s.ModeValue(), md.mode) {
				warn(fmt.Sprintf("%s: header mode %s disagrees with directory (%s)", p, dyn.Repr(s.ModeValue()), md.mode))
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
		if !dyn.Truthy(bp) {
			break
		}
		var hit *ancestor
		for _, cand := range ss.Values() {
			if seen[cand.ID()] {
				continue
			}
			if i := cand.TurnIndex(dyn.AsStr(bp)); i >= 0 {
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
func resolvePointer(ss *Sessions, p Pointer) (*Session, *dyn.Dict) {
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
		for _, p := range dyn.Glob(md.dir, "*.jsonl") {
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
func turnText(t *dyn.Dict) string {
	var parts []string
	for _, b := range dyn.Iter(contentOrEmpty(t)) {
		bd := dyn.AsDict(b)
		if dyn.Equal(bd.Get("type"), "text") {
			v, ok := bd.Lookup("text")
			if !ok {
				v = ""
			}
			parts = append(parts, dyn.AsStr(v))
		}
	}
	return strings.Join(parts, "\n")
}

func contentOrEmpty(t *dyn.Dict) any {
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
	Meta *dyn.Dict
	Body string
}

// ID is the frontmatter's id (nil when it has none).
func (st *Statement) ID() any { return st.Meta.Get("id") }

func parseScalar(v string) any {
	v = dyn.Strip(v)
	// a double-quoted value is written by fmQuote as a JSON string, so it is read back as one; otherwise
	// each rewrite of the file would escape its quotes and backslashes once more
	if strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) && dyn.Len(v) >= 2 {
		if x, err := dyn.Loads(v); err == nil {
			if s, ok := x.(string); ok {
				return s
			}
		}
	}
	if (strings.HasPrefix(v, "'") || strings.HasPrefix(v, `"`)) && strings.HasSuffix(v, v[:1]) && dyn.Len(v) >= 2 {
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
func parseFrontmatter(text string) (*dyn.Dict, string) {
	meta, body, err := readFrontmatter(text)
	if err != nil {
		die(err.Error())
	}
	return meta, body
}

// readFrontmatter is parseFrontmatter that says what it cannot read instead of stopping.
func readFrontmatter(text string) (*dyn.Dict, string, error) {
	m := frontmatterRE.FindStringSubmatch(text)
	if m == nil {
		return dyn.NewDict(), text, nil
	}
	raw, body := m[1], m[2]
	meta := dyn.NewDict()
	lines := dyn.Splitlines(raw)
	i := 0
	for i < len(lines) {
		line := lines[i]
		if dyn.Strip(line) == "" || strings.HasPrefix(dyn.LStrip(line), "#") {
			i++
			continue
		}
		if startsIndented(line) {
			return nil, "", fmt.Errorf("unexpected indentation in frontmatter: %s", dyn.StrRepr(line))
		}
		key, _, rest := dyn.Partition(line, ":")
		key = dyn.Strip(key)
		rest = dyn.Strip(rest)
		if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
			inner := dyn.Strip(rest[1 : len(rest)-1])
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
		for i < len(lines) && (startsIndented(lines[i]) || dyn.Strip(lines[i]) == "") {
			l := lines[i]
			if dyn.Strip(l) == "" {
				i++
				continue
			}
			s := dyn.Strip(l)
			if strings.HasPrefix(s, "- ") {
				s = s[2:]
				if strings.Contains(s, ":") && !strings.HasPrefix(s, "'") && !strings.HasPrefix(s, `"`) {
					obj := dyn.NewDict()
					k, _, v := dyn.Partition(s, ":")
					obj.Set(dyn.Strip(k), parseScalar(v))
					i++
					for i < len(lines) && startsIndented(lines[i]) && !strings.HasPrefix(dyn.Strip(lines[i]), "- ") && dyn.Strip(lines[i]) != "" {
						k, _, v := dyn.Partition(dyn.Strip(lines[i]), ":")
						obj.Set(dyn.Strip(k), parseScalar(v))
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
	return meta, body, nil
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
	s := dyn.Str(v)
	// a string that would read back as something else, a bool or null, is quoted so it reads back as itself
	if _, isStr := v.(string); isStr && (s == "true" || s == "false" || s == "null" || s == "~") {
		return dyn.JSONStr(s)
	}
	if s == "" || strings.ContainsAny(s, ":#[]{},\"'") || s != dyn.Strip(s) {
		return dyn.JSONStr(s)
	}
	return s
}

func dumpFrontmatter(meta *dyn.Dict) string {
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
				if d, ok := it.(*dyn.Dict); ok {
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
	meta, body := parseFrontmatter(dyn.ReadText(path))
	return &Statement{path, meta, strings.Trim(body, "\n")}
}

// readStatement reads a statement file's text as loadStatement does, with its line endings made
// universal, and says what it cannot read instead of stopping.
func readStatement(path, text string) (*Statement, error) {
	meta, body, err := readFrontmatter(dyn.Universal(text))
	if err != nil {
		return nil, err
	}
	return &Statement{path, meta, strings.Trim(body, "\n")}, nil
}

func writeStatement(path string, st *Statement) {
	dyn.WriteText(path, dumpFrontmatter(st.Meta)+"\n"+st.Body+"\n")
}

// loadAllStatements reads attested/ in the order the statements were made (byMade).
func loadAllStatements(root string) []*Statement {
	var out []*Statement
	for _, p := range sortedGlob(filepath.Join(root, "attested"), "S-*.md") {
		out = append(out, loadStatement(p))
	}
	return byMade(out)
}

// loadAllAgentStatements reads agent/ in the order the statements were made (byMade).
func loadAllAgentStatements(root string) []*Statement {
	var out []*Statement
	for _, p := range sortedGlob(filepath.Join(root, "agent"), "A-*.md") {
		out = append(out, loadStatement(p))
	}
	return byMade(out)
}

// madeAt is when a statement was made: its first attestation for a human statement, recorded_at for an
// agent statement, or "" when it carries neither. Ids carry no order (S-0149), so this is the order.
func madeAt(meta *dyn.Dict) string {
	if v := meta.Get("recorded_at"); dyn.Truthy(v) {
		return dyn.Str(v)
	}
	if atts := listOf(meta.Get("attestations")); len(atts) > 0 {
		if d, ok := atts[0].(*dyn.Dict); ok {
			if v := d.Get("at"); dyn.Truthy(v) {
				return dyn.Str(v)
			}
		}
	}
	return ""
}

// byMade sorts statements by when they were made, then by file name; the times are ISO timestamps in UTC,
// so they compare as strings.
func byMade(sts []*Statement) []*Statement {
	sort.SliceStable(sts, func(i, j int) bool {
		a, b := madeAt(sts[i].Meta), madeAt(sts[j].Meta)
		if a != b {
			return a < b
		}
		return filepath.Base(sts[i].Path) < filepath.Base(sts[j].Path)
	})
	return sts
}

// makeStatementID gives a new statement its id. Tests replace it to get ids they can predict.
var makeStatementID = newStatementID

// idSource is where new ids get their randomness.
var idSource io.Reader = rand.Reader

// newStatementID is a new id for a statement of the kind, S or A (S-0149): random rather than the next
// number, so that people attesting at the same time in separate copies of the knowledge base do not give
// out the same id. It is drawn again until it holds a letter, which sets it apart from the sequential
// ids, and is not taken in this copy, by a file name or by an id inside a file.
func newStatementID(root, kind string) string {
	taken := takenIDs(root, kind)
	return drawID(kind, statementIDLen, func(id string) bool { return taken[id] }, idSource)
}

// takenIDs are the ids of the kind, S or A, taken in this copy, by a file name or by an id inside a file.
func takenIDs(root, kind string) map[string]bool {
	taken := map[string]bool{}
	d := filepath.Join(root, statementDirs[kind])
	for _, p := range dyn.Glob(d, kind+"-*.md") {
		name := filepath.Base(p)
		id, _, _ := strings.Cut(name[len(kind)+1:], "-")
		taken[kind+"-"+strings.ToUpper(strings.TrimSuffix(id, ".md"))] = true
		if v := loadStatement(p).Meta.Get("id"); dyn.Truthy(v) {
			taken[strings.ToUpper(dyn.Str(v))] = true
		}
	}
	return taken
}

// drawID draws ids with the prefix kind and n characters from src until one holds a letter and is not
// taken. Each character comes from one byte's low five bits; 256 is a multiple of 32, so every character is
// equally likely.
func drawID(kind string, n int, taken func(string) bool, src io.Reader) string {
	buf := make([]byte, n)
	for range 10000 {
		if _, err := io.ReadFull(src, buf); err != nil {
			dyn.RaiseOS(err)
		}
		body := make([]byte, n)
		letter := false
		for i, b := range buf {
			body[i] = idAlphabet[b&31]
			letter = letter || body[i] > '9'
		}
		if id := kind + "-" + string(body); letter && !taken(id) {
			return id
		}
	}
	die(fmt.Sprintf("could not find a free %s- id", kind))
	return ""
}

// statementPath is the file of a human (S-) or agent (A-) statement by id, or "".
func statementPath(root string, sid any) string {
	s := ""
	if dyn.Truthy(sid) {
		s = dyn.Str(sid)
	}
	m := statementIDRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	hits := dyn.Glob(filepath.Join(root, statementDirs[m[1]]), s+"-*.md")
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
		warn(fmt.Sprintf("%s %s: statement not found; not marking", relation, dyn.Str(oldID)))
		return
	}
	st := loadStatement(p)
	key := markKey[relation]
	if relation == "supersedes" {
		st.Meta.Set(key, newID)
	} else {
		lst := dyn.Or(st.Meta.Get(key), []any{})
		l, ok := lst.([]any)
		if !ok {
			dyn.Raise("TypeError", "expected an array, got %s", dyn.TypeName(lst))
		}
		found := false
		for _, x := range l {
			if dyn.Equal(x, newID) {
				found = true
			}
		}
		if !found {
			l = append(l, newID)
		}
		st.Meta.Set(key, l)
	}
	writeStatement(p, st)
	println(fmt.Sprintf("%s marked %s %s", dyn.Str(oldID), key, newID))
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string, limit int) string {
	s = strings.Trim(slugRE.ReplaceAllString(dyn.Lower(s), "-"), "-")
	return strings.TrimRight(dyn.Head(s, limit), "-")
}

// versionTuple is "0.3.0" -> (0, 3, 0); junk gives (0,). A pre-release or build suffix is dropped, so
// "0.9.0-dev" is (0, 9, 0): a development build follows the rules of the version it is becoming.
func versionTuple(v any) []int64 {
	var out []int64
	core, _, _ := strings.Cut(dyn.Str(v), "-")
	core, _, _ = strings.Cut(core, "+")
	for _, x := range strings.Split(core, ".") {
		n, ok := dyn.ParseInt(x)
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

// normalizeWS collapses each run of whitespace in s to one space and trims both ends.
func normalizeWS(s string) string { return strings.Join(dyn.Fields(s), " ") }

// textContained is the whitespace-insensitive substring test used for verified copies.
func textContained(needle, haystack string) bool {
	return strings.Contains(normalizeWS(haystack), normalizeWS(needle))
}

// draftPath is the draft argument as attest and record resolve it: relative to the knowledge base.
func draftPath(root, draft string) string {
	if strings.HasPrefix(draft, "/") || strings.HasPrefix(draft, `\`) || dyn.Head(dyn.Tail(draft, 1), 1) == ":" {
		return draft
	}
	return filepath.Join(root, draft)
}

func itoa(n int) string { return strconv.Itoa(n) }
