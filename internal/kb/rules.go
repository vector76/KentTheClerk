package kb

// kb rules: Kent's rules and the documents the agent reads, served from the executable (S-0106, S-0107).

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	kent "kenttheclerk"
	"kenttheclerk/internal/argparse"
)

// document is one thing kb rules serves: a name to ask for it by, where it is built from, and what it
// is for when that is not its title.
type document struct {
	name, path, about string
}

// documents are listed in this order. The rule files sit between the introduction and procedure and the
// front-end block, in the order of the rules they hold.
var (
	leadingDocs = []document{
		{"introduction", "README.md", "what the knowledge base is for and how it works, for the human and for you"},
		{"procedure", "procedure.md", "the steps and commands for attesting a statement and recording an agent statement"},
	}
	trailingDocs = []document{
		{"claude-code", "front-ends/claude-code/CLAUDE.md", "the block to paste into a project's CLAUDE.md"},
	}
)

var (
	ruleStartRE = regexp.MustCompile(`^\*\*Kent Rule (\d+)\.\*\*`)
	docHeadingRE   = regexp.MustCompile(`^(#+)\s+(.*)$`)
)

// ruleText is one rule: its number, the file and section it sits in, and its text.
type ruleText struct {
	n            int
	doc, section string
	text         string
}

// ruleFile is one file of rules/ with its title and the rules it holds.
type ruleFile struct {
	document
	title string
	rules []ruleText
}

func readDoc(p string) string {
	s, err := kent.ReadFile(p)
	if err != nil {
		die(fmt.Sprintf("%s is not built into this kb: %v", p, err))
	}
	return s
}

// parseRules splits a rule file into its rules. A rule runs from its "**Kent Rule N.**" to the next
// rule or heading.
func parseRules(d document) ruleFile {
	rf := ruleFile{document: d}
	var cur *ruleText
	var section string
	flush := func() {
		if cur != nil {
			cur.text = strings.TrimSpace(cur.text)
			rf.rules = append(rf.rules, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(readDoc(d.path), "\n") {
		if m := docHeadingRE.FindStringSubmatch(line); m != nil {
			flush()
			if m[1] == "#" && rf.title == "" {
				rf.title = m[2]
			} else {
				section = m[2]
			}
			continue
		}
		if m := ruleStartRE.FindStringSubmatch(line); m != nil {
			flush()
			n, _ := strconv.Atoi(m[1])
			cur = &ruleText{n: n, doc: rf.title, section: section}
		}
		if cur != nil {
			cur.text += line + "\n"
		}
	}
	flush()
	return rf
}

// ruleFiles are the files of rules/, ordered by the first rule each holds.
func ruleFiles() []ruleFile {
	var out []ruleFile
	for _, p := range kent.Files() {
		if strings.HasPrefix(p, "rules/") && strings.HasSuffix(p, ".md") {
			name := strings.TrimSuffix(strings.TrimPrefix(p, "rules/"), ".md")
			out = append(out, parseRules(document{name: name, path: p}))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return firstRule(out[i]) < firstRule(out[j]) })
	return out
}

func firstRule(rf ruleFile) int {
	if len(rf.rules) == 0 {
		return 1 << 30
	}
	return rf.rules[0].n
}

func ruleRange(rf ruleFile) string {
	switch len(rf.rules) {
	case 0:
		return "no numbered rules"
	case 1:
		return fmt.Sprintf("Rule %d", rf.rules[0].n)
	}
	return fmt.Sprintf("Rules %d to %d", rf.rules[0].n, rf.rules[len(rf.rules)-1].n)
}

func runRules(ns argparse.Namespace) int {
	what, _ := ns.Str("what")
	what = strings.TrimSpace(what)
	files := ruleFiles()
	docs := append(append(append([]document{}, leadingDocs...), docsOf(files)...), trailingDocs...)

	if what == "" {
		println(fmt.Sprintf("Kent's rules and documents, as built into kb %s:", kent.Version()))
		width := 0
		for _, d := range docs {
			width = max(width, len(d.name))
		}
		about := map[string]string{}
		for _, rf := range files {
			about[rf.name] = fmt.Sprintf("%s: %s", rf.title, ruleRange(rf))
		}
		for _, d := range docs {
			a := d.about
			if a == "" {
				a = about[d.name]
			}
			println(fmt.Sprintf("  %-*s  %s", width, d.name, a))
		}
		println("")
		println("kb rules <name> prints a document; kb rules <number> prints one rule.")
		return 0
	}

	if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(what), "rule")); err == nil {
		for _, rf := range files {
			for _, r := range rf.rules {
				if r.n == n {
					where := r.doc
					if r.section != "" {
						where += " > " + r.section
					}
					println(fmt.Sprintf("(%s; kb rules %s for the whole file)", where, rf.name))
					println("")
					println(r.text)
					return 0
				}
			}
		}
		die(fmt.Sprintf("there is no Kent Rule %d in this version; kb rules lists them", n))
	}

	for _, d := range docs {
		if strings.EqualFold(d.name, what) {
			out := readDoc(d.path)
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			fmt.Fprint(Stdout, out)
			return 0
		}
	}
	var names []string
	for _, d := range docs {
		names = append(names, d.name)
	}
	die(fmt.Sprintf("no document named %q; ask for a rule by number or one of: %s", what, strings.Join(names, ", ")))
	return 1
}

func docsOf(files []ruleFile) []document {
	out := make([]document, len(files))
	for i, rf := range files {
		out[i] = rf.document
	}
	return out
}

func addRulesParser(sub *argparse.Parser) {
	p := sub.AddParser("rules", "print Kent's rules and the documents the agent reads")
	p.Positional("what", argparse.Optional, "a rule number, or a document's name; omit to list them")
	p.Run = runRules
}
