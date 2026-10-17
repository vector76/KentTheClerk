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
	submodule bool   // the repository is a git submodule checkout (S-SG81)
	broken    string // a .git that is not a repository, as an empty folder or a stale gitdir: where it is
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

// remoteRE is a remote's section in git's config, in either form git reads, [remote "x"] and the older
// [remote.x], in any case. kb reads the config file itself (S-MBME), so a remote defined in a file the
// config includes is not seen.
var remoteRE = regexp.MustCompile(`(?mi)^\s*\[remote(?:\s+"([^"]+)"|\.([^\]\s]+))\s*\]`)

// gitDirOf is the repository folder of the .git in top, and whether it is a submodule's. A .git that is a
// file names the folder ("gitdir: ..."): a worktree's, which holds a commondir file, or a submodule's,
// inside another repository's modules folder.
func gitDirOf(top string) (gitDir string, submodule bool) {
	dotgit := filepath.Join(top, ".git")
	gitDir = dotgit
	if st, err := os.Stat(dotgit); err == nil && !st.IsDir() {
		b, _ := os.ReadFile(dotgit)
		if d, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:"); ok {
			gitDir = strings.TrimSpace(d)
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(top, gitDir)
			}
			if _, err := os.Stat(filepath.Join(gitDir, "commondir")); err != nil {
				segs := strings.Split(filepath.ToSlash(filepath.Clean(gitDir)), "/")
				lastIndex := func(name string) int {
					for i := len(segs) - 1; i >= 0; i-- {
						if segs[i] == name {
							return i
						}
					}
					return -1
				}
				submodule = lastIndex("modules") > lastIndex("worktrees")
			}
		}
	}
	return gitDir, submodule
}

// gitConfigText is the config of the repository in gitDir, a worktree's from its common folder.
func gitConfigText(gitDir string) string {
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	}
	b, _ := os.ReadFile(filepath.Join(common, "config"))
	return string(b)
}

// readGitPlace reads the knowledge base at root's situation in git from files, running no git. A .git
// whose folder has no HEAD is not a repository, and the knowledge base is read as in no git repository,
// with broken saying where that .git is.
func readGitPlace(root string) gitPlace {
	root = filepath.Clean(root)
	top := gitTop(root)
	if top == "" {
		return gitPlace{kind: notInGit}
	}
	gitDir, submodule := gitDirOf(top)
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil {
		return gitPlace{kind: notInGit, broken: filepath.Join(top, ".git")}
	}
	p := gitPlace{kind: localRepo, top: top, submodule: submodule}
	if top != root {
		p.kind = inCodeRepo
		return p
	}
	for _, m := range remoteRE.FindAllStringSubmatch(gitConfigText(gitDir), -1) {
		name := m[1] + m[2]
		if p.remote == "" || name == "origin" {
			p.remote = name
		}
	}
	if p.remote != "" {
		p.kind = remoteRepo
	}
	return p
}

// ownRepo is whether the knowledge base is a repository of its own, which the agent commits.
func (p gitPlace) ownRepo() bool { return p.kind == localRepo || p.kind == remoteRepo }

// driverRegistered is whether git, in the repository the knowledge base at root is in, runs kb's merge
// driver for the record's files: git's own reading of its config, every scope included (S-JNZA).
func driverRegistered(root string) bool {
	d, st := git(root, "config", "--get", "merge."+mergeDriverName+".driver")
	return st == 0 && d == mergeDriverCommand
}

// submoduleWarning is what kb says about a knowledge base in a git submodule checkout (S-SG81).
const submoduleWarning = "the knowledge base is in a git submodule, which Kent does not support: whoever keeps it " +
	"that way is on their own with it"

// gitCaseText is what kb says about each case: the setup report's line, and the session-start text's.
var gitCaseText = map[gitCase]struct{ summary, line string }{
	notInGit: {"In no git repository: nothing commits it or syncs it.",
		"Git: the knowledge base is in no git repository, so nothing commits it and nothing syncs it."},
	localRepo: {"Its own git repository, with no remote: the agent commits it on every act, and nothing syncs it.",
		"Git: the knowledge base is its own git repository, with no remote. You commit it: when an act is done, " +
			"commit what it wrote. There is nothing to pull or push."},
	remoteRepo: {"Its own git repository, with the remote %[1]s: the agent commits, pulls and pushes it on every act.",
		"Git: the knowledge base is its own git repository, with the remote %[1]s. You commit, pull and push it on " +
			"every act: pull before the act, and commit what it wrote and push once it is done. When the remote " +
			"cannot be reached, act and commit anyway, and pull and push once it can be."},
	inCodeRepo: {"Inside the code repository at %[2]s: a person commits it, and it is pulled and pushed with the code.",
		"Git: the knowledge base sits inside the code repository at %[2]s, so a person commits it, and it is pulled " +
			"and pushed with the code. Never commit it on your own initiative."},
}

// summary says the case and what drives the commits and the pulls and pushes (S-7BWA), for setup's report.
func (p gitPlace) summary() string { return sprintfCase(gitCaseText[p.kind].summary, p) }

func sprintfCase(format string, p gitPlace) string {
	return strings.NewReplacer("%[1]s", p.remote, "%[2]s", p.top).Replace(format)
}

// shellSafeRE is a path a shell takes as one word as it stands.
var shellSafeRE = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,~-]+$`)

// gitPrefix is how the agent runs git on the knowledge base at root, and how the permissions for one in its
// own repository name it: git -C and the folder with forward slashes, quoted when a shell would split it
// or read anything in it: in single quotes, which neither bash nor PowerShell reads inside, or in double
// quotes when it holds a single quote.
func gitPrefix(root string) string {
	p := filepath.ToSlash(root)
	switch {
	case shellSafeRE.MatchString(p):
	case !strings.Contains(p, "'"):
		p = "'" + p + "'"
	default:
		p = `"` + p + `"`
	}
	return "git -C " + p
}

// offerCommitSetting describes KENT_OFFER_COMMIT: for a knowledge base inside a code repository, whether the
// agent offers to commit after each attestation or leaves commits to the human (S-DQES). A value kb does
// not understand is not ok, and the default's text is given.
func offerCommitSetting(v string) (text string, ok bool) {
	switch strings.TrimSpace(v) {
	case "", "never":
		return "Leave committing it to the human, who commits it with the code that reflects the change: do " +
			"not offer or try to commit for each statement.", true
	case "after-attestation":
		return "After each attestation, offer to commit what it wrote.", true
	}
	text, _ = offerCommitSetting("")
	return text, false
}

// offerCommitProblem is what is wrong with KENT_OFFER_COMMIT's value v, or "".
func offerCommitProblem(v string) string {
	if _, ok := offerCommitSetting(v); ok {
		return ""
	}
	return offerCommitEnv + " is \"" + v + "\", which kb does not understand (never or after-attestation); " +
		"leaving commits to the human"
}

// gitLines tell the agent which case applies to the knowledge base at root and what it asks of the agent
// (S-7BWA, S-8J77), and any problems with it.
func gitLines(root string, env func(string) string) (lines, problems []string) {
	p := readGitPlace(root)
	line := sprintfCase(gitCaseText[p.kind].line, p)
	if p.ownRepo() {
		line += " Run git as `" + gitPrefix(root) + " …`."
	}
	if p.kind == inCodeRepo {
		offer, _ := offerCommitSetting(env(offerCommitEnv))
		line += " " + offer
		if pr := offerCommitProblem(env(offerCommitEnv)); pr != "" {
			problems = append(problems, pr)
		}
	}
	lines = []string{line}
	if p.kind != notInGit {
		lines = append(lines, "Expect any pull to bring changes, even where nobody else writes, since the same person "+
			"may write from another machine, and be ready for a pull that merges or stops on a conflict. kb merges "+
			"the record's files as git merges them. After every merge, rebase or pull that brought changes, run "+
			"`kb resolve`: it merges what git left conflicted in the record and reports ids copies gave to different "+
			"statements. Never merge the record's files by hand. If it cannot resolve something, stop and tell the "+
			"human. Then run `kb check`. Files kb resolve writes are commits like an act's: "+
			map[bool]string{true: "commit them, and push them where you push.",
				false: "leave them to the person, as the rest of the knowledge base."}[p.ownRepo()])
		if !driverRegistered(root) {
			problems = append(problems, "kb's merge driver is not registered in this clone, so git merges the record's "+
				"files as plain text and leaves more conflicts for `kb resolve`")
		}
	}
	switch {
	case p.broken != "":
		problems = append(problems, p.broken+" is not a git repository (it has no HEAD); it is read as in no git "+
			"repository until it is repaired or removed")
	case p.submodule:
		problems = append(problems, submoduleWarning)
	}
	return lines, problems
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
	statementFileRE = regexp.MustCompile(`^S-(?:[0-9A-Z]{4,6}|[0-9]{4,})-[a-z0-9-]*\.md$`)
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
