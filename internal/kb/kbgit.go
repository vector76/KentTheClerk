package kb

// A knowledge base's place in git, and what it means for the git work. kb may run git to read, but never
// to change anything: the agent commits, pulls and pushes (S-0T5Y, S-JNZA). What drives a knowledge
// base's commits, and separately its pulls and pushes, follows from its situation (S-7BWA), which kb
// reads from files without running git (S-MBME), and the session-start text tells the agent which case
// applies. A knowledge base may also sit inside a code repository, in a folder of its own (S-NXEB), which
// setup finds by its shape rather than its name (S-1RA3).

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// gitCase is a knowledge base's situation in git (S-MBME).
type gitCase int

const (
	notInGit   gitCase = iota // no .git in its folder or above: never committed or synced
	localRepo                 // a .git of its own and no remote: the agent commits, nothing syncs
	remoteRepo                // a .git of its own and a remote: the agent commits, pulls and pushes on every act
	inCodeRepo                // a .git above its folder: a person commits, and it syncs on the code's cadence
)

// gitPlace is what kb reads of a knowledge base's git situation.
type gitPlace struct {
	kind      gitCase
	top       string // the folder holding the .git: the knowledge base's own, or the code repository's
	remote    string // for remoteRepo, the remote's name: origin if there is one, else the first
	submodule bool   // its .git is a file pointing into another repository's modules (S-SG81)
}

// gitTop is the folder holding the .git of the repository the folder dir is in, or "" when it is in none.
func gitTop(dir string) string {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

var remoteRE = regexp.MustCompile(`(?m)^\s*\[remote\s+"([^"]+)"\s*\]`)

// readGitPlace reads the knowledge base at root's situation in git from files, running no git. A .git
// that is a file names the repository's folder ("gitdir: ..."): a worktree's, whose config is in the
// common folder, or a submodule's, inside another repository's modules.
func readGitPlace(root string) gitPlace {
	top := gitTop(root)
	switch {
	case top == "":
		return gitPlace{kind: notInGit}
	case !samePath(top, root):
		return gitPlace{kind: inCodeRepo, top: top}
	}
	p := gitPlace{kind: localRepo, top: top}
	dotgit := filepath.Join(root, ".git")
	gitDir := dotgit
	if st, err := os.Stat(dotgit); err == nil && !st.IsDir() {
		b, _ := os.ReadFile(dotgit)
		if d, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:"); ok {
			gitDir = strings.TrimSpace(d)
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(root, gitDir)
			}
			slashed := "/" + filepath.ToSlash(filepath.Clean(gitDir)) + "/"
			p.submodule = strings.Contains(slashed, "/modules/") && !strings.Contains(slashed, "/worktrees/")
		}
	}
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	}
	b, _ := os.ReadFile(filepath.Join(common, "config"))
	for _, m := range remoteRE.FindAllStringSubmatch(string(b), -1) {
		if p.remote == "" || m[1] == "origin" {
			p.remote = m[1]
		}
	}
	if p.remote != "" {
		p.kind = remoteRepo
	}
	return p
}

// ownRepo is whether the knowledge base is a repository of its own, which the agent commits.
func (p gitPlace) ownRepo() bool { return p.kind == localRepo || p.kind == remoteRepo }

// summary says the case and what drives the commits and the pulls and pushes (S-7BWA), for setup's report.
func (p gitPlace) summary() string {
	switch p.kind {
	case localRepo:
		return "Its own git repository, with no remote: the agent commits it on every act, and nothing syncs it."
	case remoteRepo:
		return "Its own git repository, with the remote " + p.remote + ": the agent commits, pulls and pushes it on every act."
	case inCodeRepo:
		return "Inside the code repository at " + p.top + ": a person commits it, and it is pulled and pushed with the code."
	}
	return "In no git repository: nothing commits it or syncs it."
}

// gitPrefix is how the agent runs git on the knowledge base at root, and how the permissions for one in its
// own repository name it: git -C and the folder with forward slashes, quoted if it holds a space.
func gitPrefix(root string) string {
	p := filepath.ToSlash(root)
	if strings.ContainsAny(p, " \t") {
		p = `"` + p + `"`
	}
	return "git -C " + p
}

// offerCommitSetting describes KENT_OFFER_COMMIT: for a knowledge base inside a code repository, whether the
// agent offers to commit after each attestation or leaves commits to the human (S-DQES).
func offerCommitSetting(v string) (text string, ok bool) {
	switch strings.TrimSpace(v) {
	case "", "never":
		return "Leave committing it to the human, who commits it with the code that reflects the change: do " +
			"not offer or try to commit for each statement.", true
	case "after-attestation":
		return "After each attestation, offer to commit what it wrote.", true
	}
	return "", false
}

// gitLines tell the agent which case applies to the knowledge base at root and what it asks of the agent
// (S-7BWA, S-8J77), and any problem with it.
func gitLines(root string, env func(string) string) (lines []string, problem string) {
	p := readGitPlace(root)
	run := " Run git as `" + gitPrefix(root) + " …`."
	expect := "Expect any pull to bring changes, even where nobody else writes, since the same person may write " +
		"from another machine, and be ready for a pull that merges or stops on a conflict. The logs and the user " +
		"table merge by keeping both sides' lines. A conflict in the record's files is not yours to resolve by " +
		"hand: stop and tell the human. After a pull that brought changes, run `kb check`."
	switch p.kind {
	case notInGit:
		return []string{"Git: the knowledge base is in no git repository, so nothing commits it and nothing syncs it."}, ""
	case localRepo:
		lines = []string{"Git: the knowledge base is its own git repository, with no remote. You commit it: when an act " +
			"is done, commit what it wrote. There is nothing to pull or push." + run}
	case remoteRepo:
		lines = []string{"Git: the knowledge base is its own git repository, with the remote " + p.remote + ". You commit, " +
			"pull and push it on every act: pull before the act, and commit what it wrote and push once it is done. " +
			"When the remote cannot be reached, act and commit anyway, and pull and push once it can be." + run, expect}
	case inCodeRepo:
		offer, _ := offerCommitSetting(env(offerCommitEnv))
		lines = []string{"Git: the knowledge base sits inside the code repository at " + p.top + ", so a person commits " +
			"it, and it is pulled and pushed with the code. Never commit it on your own initiative. " + offer, expect}
		return lines, ""
	}
	if p.submodule {
		problem = "the knowledge base is a git submodule, which Kent does not support: whoever keeps it that way is " +
			"on their own with it"
	}
	return lines, problem
}

// ---------------------------------------------------------------- a knowledge base inside a code repository

// kbOwnNames are the names a knowledge base's top folder holds; anything else in a knowledge base inside
// a code repository is something that does not belong in it (S-NXEB).
var kbOwnNames = map[string]bool{
	"attested": true, "evidence": true, "agent": true, "off-record": true, "render": true,
	"index.db": true, "index.db-journal": true, overrideLogName: true, usersName: true, usersLogName: true,
	"kent.pin": true, ".gitignore": true, ".gitattributes": true, ".git": true,
}

// strangers are the entries of the knowledge base's top folder that are not the knowledge base's own,
// apart from a hint file (S-M1JC).
func strangers(root string) []string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if !kbOwnNames[e.Name()] && !isHintFile(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// isHintFile is whether name is a file telling people what the folder is (S-M1JC): a README of any kind.
func isHintFile(name string) bool { return strings.HasPrefix(strings.ToUpper(name), "README") }

func hasHintFile(root string) bool {
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() && isHintFile(e.Name()) {
			return true
		}
	}
	return false
}

var (
	statementFileRE = regexp.MustCompile(`^S-[0-9A-Z]{4}-[a-z0-9-]*\.md$`)
	evidenceFileRE  = regexp.MustCompile(`^[^.].*\.jsonl$`)
)

// hasKBShape is whether dir is shaped like a knowledge base (S-1RA3): attested/ and evidence/ folders
// holding only what a knowledge base puts there, and at least one statement, evidence file or user table,
// so that an unrelated pair of folders with those names is not taken for one.
func hasKBShape(dir string) bool {
	if !isKBFolder(dir) {
		return false
	}
	records := 0
	for sub, re := range map[string]*regexp.Regexp{"attested": statementFileRE, "evidence": evidenceFileRE} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			return false
		}
		for _, e := range entries {
			switch {
			case e.Name() == ".gitkeep":
			case e.IsDir() && sub == "evidence" && e.Name() == "originals":
			case !e.IsDir() && re.MatchString(e.Name()):
				records++
			default:
				return false
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, usersName)); err == nil {
		records++
	}
	return records > 0
}

// skipWhenSearching are folders that hold no knowledge base worth finding and can be large.
var skipWhenSearching = map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true}

// findKBsIn are the folders under dir, dir included, shaped like a knowledge base, looking at most depth
// levels down and not inside hidden folders or another knowledge base.
func findKBsIn(dir string, depth int) []string {
	var out []string
	var walk func(d string, left int)
	walk = func(d string, left int) {
		if hasKBShape(d) {
			out = append(out, d)
			return
		}
		if left == 0 {
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() && !strings.HasPrefix(n, ".") && !skipWhenSearching[n] {
				walk(filepath.Join(d, n), left-1)
			}
		}
	}
	walk(dir, depth)
	sort.Strings(out)
	return out
}
