// Package argparse parses kb's command lines and prints its usage, help and error messages, each in an
// exact format: long options abbreviated to any unambiguous prefix and given as "--name value" or
// "--name=value", flags, positionals taking one value, an optional one, any number or at least one,
// a required sub-command, integer options, arguments that look like negative numbers taken as values,
// and help laid out in columns and wrapped to the terminal's width.
package argparse

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"strings"

	"kenttheclerk/internal/dyn"
)

// Nargs values: how many command-line arguments an action takes.
const (
	Single   = ""
	Optional = "?"
	ZeroMore = "*"
	OneMore  = "+"
	Zero     = "0"
	parser   = "A..."
)

// Action is one argument.
type Action struct {
	Dest          string
	OptionStrings []string
	Nargs         string
	Required      bool
	Help          string
	Metavar       string
	Int           bool // the value is an integer
	Default       any
	isHelp        bool
	storeTrue     bool
	sub           *Parser // the parent of a sub-command action
	choices       []string
	choiceActions []*Action // help pseudo-actions of the sub-commands
}

// Parser is the parser of a command: its options, positionals and sub-commands.
type Parser struct {
	Prog        string
	Description string
	actions     []*Action
	subs        map[string]*Parser
	subAction   *Action
	Run         func(ns Namespace) int
}

// Namespace holds parsed values: string or nil for single values, bool for flags, []string for
// lists, *big.Int for integer options.
type Namespace map[string]any

// Str returns a single string value and whether it was given (not nil).
func (ns Namespace) Str(k string) (string, bool) {
	s, ok := ns[k].(string)
	return s, ok
}

// Bool returns a flag.
func (ns Namespace) Bool(k string) bool { b, _ := ns[k].(bool); return b }

// List returns a list value.
func (ns Namespace) List(k string) []string { l, _ := ns[k].([]string); return l }

// New makes a parser with the automatic -h/--help.
func New(prog, description string) *Parser {
	p := &Parser{Prog: prog, Description: description, subs: map[string]*Parser{}}
	p.actions = append(p.actions, &Action{Dest: "help", OptionStrings: []string{"-h", "--help"}, Nargs: Zero,
		Help: "show this help message and exit", isHelp: true})
	return p
}

// Positional adds a positional argument.
func (p *Parser) Positional(dest, nargs, help string) {
	a := &Action{Dest: dest, Nargs: nargs, Help: help, Required: nargs == Single || nargs == OneMore}
	if nargs == ZeroMore {
		a.Default = nil
	}
	p.actions = append(p.actions, a)
}

// Option adds "--name VALUE"; dest is the name with dashes turned into underscores.
func (p *Parser) Option(name, help string, def any, required bool) *Action {
	a := &Action{Dest: strings.ReplaceAll(strings.TrimLeft(name, "-"), "-", "_"), OptionStrings: []string{name},
		Help: help, Default: def, Required: required}
	p.actions = append(p.actions, a)
	return a
}

// IntOption adds "--name N", taking an integer.
func (p *Parser) IntOption(name, help string, def int) {
	a := p.Option(name, help, big.NewInt(int64(def)), false)
	a.Int = true
}

// Flag adds "--name", which sets its dest to true.
func (p *Parser) Flag(name, help string) {
	p.actions = append(p.actions, &Action{Dest: strings.ReplaceAll(strings.TrimLeft(name, "-"), "-", "_"),
		OptionStrings: []string{name}, Nargs: Zero, Help: help, Default: false, storeTrue: true})
}

// Subcommands adds the required sub-command positional (dest "cmd").
func (p *Parser) Subcommands(dest string) {
	p.subAction = &Action{Dest: dest, Nargs: parser, Required: true, sub: p}
	p.actions = append(p.actions, p.subAction)
}

// AddParser adds a sub-command.
func (p *Parser) AddParser(name, help string) *Parser {
	sp := New(p.Prog+" "+name, "")
	p.subs[name] = sp
	p.subAction.choices = append(p.subAction.choices, name)
	p.subAction.choiceActions = append(p.subAction.choiceActions, &Action{Dest: name, Metavar: name, Help: help})
	return sp
}

// Exit carries the status a parse ended with when it did not produce a namespace.
type Exit struct{ Code int }

// Parse parses args: on an error it prints usage and the message to stderr and returns Exit{2}; for
// -h it prints help to stdout and returns Exit{0}.
func (p *Parser) Parse(args []string, stdout, stderr io.Writer) (ns Namespace, sub *Parser, exit *Exit) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *parseError:
				fmt.Fprint(stderr, e.parser.FormatUsage())
				fmt.Fprintf(stderr, "%s: error: %s\n", e.parser.Prog, e.msg)
				ns, sub, exit = nil, nil, &Exit{2}
			case *helpExit:
				fmt.Fprint(stdout, e.parser.FormatHelp())
				ns, sub, exit = nil, nil, &Exit{0}
			default:
				panic(r)
			}
		}
	}()
	ns = Namespace{}
	var chosen *Parser
	extras := p.parseKnown(args, ns, &chosen)
	if len(extras) > 0 {
		p.error("unrecognized arguments: " + strings.Join(extras, " "))
	}
	return ns, chosen, nil
}

type parseError struct {
	parser *Parser
	msg    string
}

type helpExit struct{ parser *Parser }

func (p *Parser) error(msg string) { panic(&parseError{p, msg}) }

// argError is an error message about an argument, prefixed with the argument's name.
func argError(a *Action, msg string) string {
	if a == nil {
		return msg
	}
	name := actionName(a)
	if name == "" {
		return msg
	}
	return "argument " + name + ": " + msg
}

func actionName(a *Action) string {
	if len(a.OptionStrings) > 0 {
		return strings.Join(a.OptionStrings, "/")
	}
	if a.Metavar != "" {
		return a.Metavar
	}
	return a.Dest
}

type optTuple struct {
	action       *Action
	optionString string
	sep          string
	hasSep       bool
	explicitArg  string
	hasExplicit  bool
}

// negativeNumber matches an argument that looks like a negative number, which is taken as a value
// rather than an option: "-" and decimal digits (any Unicode decimal digits), with or without a
// fraction, optionally followed by a final newline.
var negativeNumber = regexp.MustCompile(`^-\p{Nd}+\n?$|^-\p{Nd}*\.\p{Nd}+\n?$`)

func (p *Parser) optionMap() map[string]*Action {
	m := map[string]*Action{}
	for _, a := range p.actions {
		for _, s := range a.OptionStrings {
			m[s] = a
		}
	}
	return m
}

func (p *Parser) optionOrder() []string {
	var out []string
	for _, a := range p.actions {
		out = append(out, a.OptionStrings...)
	}
	return out
}

func (p *Parser) parseOptional(arg string) []optTuple {
	if arg == "" || arg[0] != '-' {
		return nil
	}
	m := p.optionMap()
	if a, ok := m[arg]; ok {
		return []optTuple{{action: a, optionString: arg}}
	}
	if dyn.Len(arg) == 1 {
		return nil
	}
	if opt, sep, explicit := dyn.Partition(arg, "="); sep != "" {
		if a, ok := m[opt]; ok {
			return []optTuple{{action: a, optionString: opt, sep: sep, hasSep: true, explicitArg: explicit, hasExplicit: true}}
		}
	}
	if t := p.optionTuples(arg); len(t) > 0 {
		return t
	}
	if negativeNumber.MatchString(arg) {
		return nil
	}
	if strings.Contains(arg, " ") {
		return nil
	}
	return []optTuple{{optionString: arg}}
}

func (p *Parser) optionTuples(arg string) []optTuple {
	var out []optTuple
	m := p.optionMap()
	runes := []rune(arg)
	if len(runes) > 1 && runes[0] == '-' && runes[1] == '-' {
		prefix, sep, explicit := dyn.Partition(arg, "=")
		for _, s := range p.optionOrder() {
			if strings.HasPrefix(s, prefix) {
				out = append(out, optTuple{action: m[s], optionString: s, sep: sep, hasSep: sep != "",
					explicitArg: explicit, hasExplicit: sep != ""})
			}
		}
		return out
	}
	prefix, sep, explicit := dyn.Partition(arg, "=")
	short := string(runes[:2])
	shortExplicit := string(runes[2:])
	for _, s := range p.optionOrder() {
		if s == short {
			out = append(out, optTuple{action: m[s], optionString: s, hasSep: true, explicitArg: shortExplicit, hasExplicit: true})
		} else if strings.HasPrefix(s, prefix) {
			out = append(out, optTuple{action: m[s], optionString: s, sep: sep, hasSep: sep != "",
				explicitArg: explicit, hasExplicit: sep != ""})
		}
	}
	return out
}

func nargsPattern(a *Action) string {
	opt := len(a.OptionStrings) > 0
	switch a.Nargs {
	case Single:
		if opt {
			return "([A])"
		}
		return "(-*A-*)"
	case Optional:
		if opt {
			return "(A?)"
		}
		return "(-*A?-*)"
	case ZeroMore:
		if opt {
			return "(A*)"
		}
		return "(-*[A-]*)"
	case OneMore:
		if opt {
			return "(A+)"
		}
		return "(-*A[A-]*)"
	case parser:
		if opt {
			return "(A[AO]*)"
		}
		return "(-*A[-AO]*)"
	case Zero:
		if opt {
			return "([AO]{0})"
		}
		return "((?:-*A){0}-*)"
	}
	panic("nargs")
}

func matchArgument(a *Action, pattern string) int {
	m := regexp.MustCompile("^" + nargsPattern(a)).FindStringSubmatchIndex(pattern)
	if m == nil {
		msg := map[string]string{Single: "expected one argument", Optional: "expected at most one argument",
			OneMore: "expected at least one argument"}[a.Nargs]
		if msg == "" {
			msg = "expected " + a.Nargs + " argument"
			if a.Nargs != "1" {
				msg += "s"
			}
		}
		panic(argErr{a, msg})
	}
	return m[3] - m[2]
}

type argErr struct {
	a   *Action
	msg string
}

func matchPartial(actions []*Action, pattern string) []int {
	for i := len(actions); i > 0; i-- {
		var pat strings.Builder
		for _, a := range actions[:i] {
			pat.WriteString(nargsPattern(a))
		}
		m := regexp.MustCompile("^" + pat.String()).FindStringSubmatchIndex(pattern)
		if m == nil {
			continue
		}
		var res []int
		for g := 1; g*2 < len(m); g++ {
			res = append(res, m[g*2+1]-m[g*2])
		}
		if m[1] < len(pattern) && pattern[m[1]] == 'O' {
			for len(res) > 0 && res[len(res)-1] == 0 {
				res = res[:len(res)-1]
			}
		}
		return res
	}
	return nil
}

func (p *Parser) parseKnown(args []string, ns Namespace, chosen **Parser) (extras []string) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(argErr); ok {
				p.error(argError(e.a, e.msg))
			}
			panic(r)
		}
	}()
	*chosen = nil
	for _, a := range p.actions {
		if a.isHelp {
			continue
		}
		if _, ok := ns[a.Dest]; !ok {
			ns[a.Dest] = a.Default
		}
	}
	optIdx := map[int][]optTuple{}
	var pat strings.Builder
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			pat.WriteByte('-')
			for i++; i < len(args); i++ {
				pat.WriteByte('A')
			}
			break
		}
		if t := p.parseOptional(args[i]); t != nil {
			optIdx[i] = t
			pat.WriteByte('O')
		} else {
			pat.WriteByte('A')
		}
	}
	pattern := pat.String()
	seen := map[*Action]bool{}

	takeAction := func(a *Action, strs []string, optionString string) {
		seen[a] = true
		val := p.getValues(a, strs)
		switch {
		case a.isHelp:
			panic(&helpExit{p})
		case a.storeTrue:
			ns[a.Dest] = true
		case a.sub != nil:
			name := val.([]string)[0]
			ns[a.Dest] = name
			sp := p.subs[name]
			*chosen = sp
			subNs := Namespace{}
			var subChosen *Parser
			more := sp.parseKnown(val.([]string)[1:], subNs, &subChosen)
			for k, v := range subNs {
				ns[k] = v
			}
			extras = append(extras, more...)
		default:
			ns[a.Dest] = val
		}
	}

	consumeOptional := func(start int) int {
		tuples := optIdx[start]
		if len(tuples) > 1 {
			var names []string
			for _, t := range tuples {
				names = append(names, t.optionString)
			}
			panic(argErr{nil, fmt.Sprintf("ambiguous option: %s could match %s", args[start], strings.Join(names, ", "))})
		}
		t := tuples[0]
		type step struct {
			a    *Action
			args []string
			opt  string
		}
		var steps []step
		stop := 0
		for {
			if t.action == nil {
				extras = append(extras, args[start])
				return start + 1
			}
			if t.hasExplicit {
				count := matchArgument(t.action, "A")
				optRunes := []rune(t.optionString)
				if count == 0 && optRunes[1] != '-' && t.explicitArg != "" {
					if t.sep != "" || t.explicitArg[0] == '-' {
						panic(argErr{t.action, "ignored explicit argument " + dyn.StrRepr(t.explicitArg)})
					}
					steps = append(steps, step{t.action, nil, t.optionString})
					er := []rune(t.explicitArg)
					optStr := string(optRunes[0]) + string(er[0])
					if a, ok := p.optionMap()[optStr]; ok {
						rest := string(er[1:])
						t = optTuple{action: a, optionString: optStr}
						if rest == "" {
						} else if rest[0] == '=' {
							t.sep, t.hasSep, t.explicitArg, t.hasExplicit = "=", true, rest[1:], true
						} else {
							t.hasSep, t.explicitArg, t.hasExplicit = true, rest, true
						}
						continue
					}
					extras = append(extras, string(optRunes[0])+t.explicitArg)
					stop = start + 1
					break
				} else if count == 1 {
					stop = start + 1
					steps = append(steps, step{t.action, []string{t.explicitArg}, t.optionString})
					break
				}
				panic(argErr{t.action, "ignored explicit argument " + dyn.StrRepr(t.explicitArg)})
			}
			s := start + 1
			count := matchArgument(t.action, pattern[s:])
			stop = s + count
			steps = append(steps, step{t.action, args[s:stop], t.optionString})
			break
		}
		for _, st := range steps {
			takeAction(st.a, st.args, st.opt)
		}
		return stop
	}

	var positionals []*Action
	for _, a := range p.actions {
		if len(a.OptionStrings) == 0 {
			positionals = append(positionals, a)
		}
	}
	consumePositionals := func(start int) int {
		counts := matchPartial(positionals, pattern[start:])
		for i, c := range counts {
			a := positionals[i]
			strs := append([]string(nil), args[start:start+c]...)
			if a.Nargs == parser {
				if pattern[start] == '-' {
					strs = strs[1:]
				}
			} else if strings.Contains(pattern[start:start+c], "-") {
				for k, s := range strs {
					if s == "--" {
						strs = append(strs[:k], strs[k+1:]...)
						break
					}
				}
			}
			start += c
			takeAction(a, strs, "")
		}
		positionals = positionals[len(counts):]
		return start
	}

	maxOpt := -1
	for i := range optIdx {
		if i > maxOpt {
			maxOpt = i
		}
	}
	start := 0
	for start <= maxOpt {
		next := start
		for next <= maxOpt {
			if _, ok := optIdx[next]; ok {
				break
			}
			next++
		}
		if start != next {
			end := consumePositionals(start)
			if end > start {
				start = end
				continue
			}
			start = end
		}
		if _, ok := optIdx[start]; !ok {
			extras = append(extras, args[start:next]...)
			start = next
		}
		start = consumeOptional(start)
	}
	stop := consumePositionals(start)
	extras = append(extras, args[stop:]...)

	var required []string
	for _, a := range p.actions {
		if !seen[a] && a.Required {
			required = append(required, actionName(a))
		}
	}
	if len(required) > 0 {
		panic(argErr{nil, "the following arguments are required: " + strings.Join(required, ", ")})
	}
	return extras
}

func (p *Parser) getValues(a *Action, strs []string) any {
	switch {
	case len(strs) == 0 && a.Nargs == Optional:
		return a.Default
	case len(strs) == 0 && a.Nargs == ZeroMore && len(a.OptionStrings) == 0:
		if a.Default != nil {
			return a.Default
		}
		return []string{}
	case len(strs) == 1 && (a.Nargs == Single || a.Nargs == Optional):
		v := p.getValue(a, strs[0])
		return v
	case a.Nargs == parser:
		if !contains(a.choices, strs[0]) {
			panic(argErr{a, fmt.Sprintf("invalid choice: %s (choose from %s)", dyn.StrRepr(strs[0]), strings.Join(a.choices, ", "))})
		}
		return strs
	case a.Nargs == Zero:
		return nil
	}
	out := make([]string, len(strs))
	copy(out, strs)
	return out
}

func (p *Parser) getValue(a *Action, s string) any {
	if a.Int {
		n, ok := dyn.ParseInt(s)
		if !ok {
			panic(argErr{a, "invalid int value: " + dyn.StrRepr(s)})
		}
		return n
	}
	return s
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- help formatting

func termWidth() int {
	// COLUMNS when it parses as a positive integer, else the terminal's width, else 80
	if n, ok := dyn.ParseInt(os.Getenv("COLUMNS")); ok && n.Sign() > 0 {
		if !n.IsInt64() || n.Int64() > 1<<30 {
			return 1 << 30
		}
		return int(n.Int64())
	}
	if c := terminalColumns(); c > 0 {
		return c
	}
	return 80
}

type formatter struct {
	prog         string
	width        int
	maxHelpPos   int
	indent       int
	actionMaxLen int
	whitespace   *regexp.Regexp
	longBreak    *regexp.Regexp
}

func newFormatter(prog string) *formatter {
	w := termWidth() - 2
	return &formatter{prog: prog, width: w, maxHelpPos: min(24, max(w-20, 4)),
		whitespace: regexp.MustCompile(`[ \t\n\r\f\v]+`), longBreak: regexp.MustCompile(`\n\n\n+`)}
}

func formatArgs(a *Action, def string) string {
	mv := def
	if a.Metavar != "" {
		mv = a.Metavar
	} else if a.choices != nil {
		mv = "{" + strings.Join(a.choices, ",") + "}"
	}
	switch a.Nargs {
	case Single:
		return mv
	case Optional:
		return "[" + mv + "]"
	case ZeroMore:
		return "[" + mv + " ...]"
	case OneMore:
		return mv + " [" + mv + " ...]"
	case parser:
		return mv + " ..."
	}
	return ""
}

func (a *Action) metavarDefault() string {
	if len(a.OptionStrings) > 0 {
		return strings.ToUpper(a.Dest)
	}
	return a.Dest
}

func invocation(a *Action) string {
	if len(a.OptionStrings) == 0 {
		mv := a.Dest
		if a.Metavar != "" {
			mv = a.Metavar
		} else if a.choices != nil {
			mv = "{" + strings.Join(a.choices, ",") + "}"
		}
		return mv
	}
	if a.Nargs == Zero {
		return strings.Join(a.OptionStrings, ", ")
	}
	return strings.Join(a.OptionStrings, ", ") + " " + formatArgs(a, a.metavarDefault())
}

func usageParts(actions []*Action) []string {
	var parts []string
	for _, a := range actions {
		var part string
		if len(a.OptionStrings) == 0 {
			part = formatArgs(a, a.metavarDefault())
		} else {
			if a.Nargs == Zero {
				part = a.OptionStrings[0]
			} else {
				part = a.OptionStrings[0] + " " + formatArgs(a, a.metavarDefault())
			}
			if !a.Required {
				part = "[" + part + "]"
			}
		}
		parts = append(parts, part)
	}
	return parts
}

func (f *formatter) usage(actions []*Action) string {
	prefix := "usage: "
	var opts, pos []*Action
	for _, a := range actions {
		if len(a.OptionStrings) > 0 {
			opts = append(opts, a)
		} else {
			pos = append(pos, a)
		}
	}
	all := append(append([]*Action{}, opts...), pos...)
	usage := strings.Join(nonEmpty(f.prog, strings.Join(usageParts(all), " ")), " ")
	textWidth := f.width - f.indent
	if len(prefix)+dyn.Len(usage) > textWidth {
		optParts := usageParts(opts)
		posParts := usageParts(pos)
		getLines := func(parts []string, indent string, prefix *string) []string {
			var lines, line []string
			lineLen := len(indent) - 1
			if prefix != nil {
				lineLen = len(*prefix) - 1
			}
			for _, part := range parts {
				if lineLen+1+dyn.Len(part) > textWidth && len(line) > 0 {
					lines = append(lines, indent+strings.Join(line, " "))
					line = nil
					lineLen = len(indent) - 1
				}
				line = append(line, part)
				lineLen += dyn.Len(part) + 1
			}
			if len(line) > 0 {
				lines = append(lines, indent+strings.Join(line, " "))
			}
			if prefix != nil {
				lines[0] = lines[0][len(indent):]
			}
			return lines
		}
		var lines []string
		if float64(len(prefix)+dyn.Len(f.prog)) <= 0.75*float64(textWidth) {
			indent := strings.Repeat(" ", len(prefix)+dyn.Len(f.prog)+1)
			if len(optParts) > 0 {
				lines = getLines(append([]string{f.prog}, optParts...), indent, &prefix)
				lines = append(lines, getLines(posParts, indent, nil)...)
			} else if len(posParts) > 0 {
				lines = getLines(append([]string{f.prog}, posParts...), indent, &prefix)
			} else {
				lines = []string{f.prog}
			}
		} else {
			indent := strings.Repeat(" ", len(prefix))
			parts := append(append([]string{}, optParts...), posParts...)
			lines = getLines(parts, indent, nil)
			if len(lines) > 1 {
				lines = nil
				lines = append(lines, getLines(optParts, indent, nil)...)
				lines = append(lines, getLines(posParts, indent, nil)...)
			}
			lines = append([]string{f.prog}, lines...)
		}
		usage = strings.Join(lines, "\n")
	}
	return prefix + usage + "\n\n"
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (f *formatter) action(a *Action) string {
	helpPos := min(f.actionMaxLen+2, f.maxHelpPos)
	helpWidth := max(f.width-helpPos, 11)
	actionWidth := helpPos - f.indent - 2
	header := invocation(a)
	indentFirst := 0
	pad := strings.Repeat(" ", f.indent)
	switch {
	case a.Help == "":
		header = pad + header + "\n"
	case dyn.Len(header) <= actionWidth:
		header = pad + header + strings.Repeat(" ", actionWidth-dyn.Len(header)) + "  "
	default:
		header = pad + header + "\n"
		indentFirst = helpPos
	}
	parts := []string{header}
	if a.Help != "" && strings.TrimSpace(a.Help) != "" {
		lines := wrap(strings.TrimSpace(f.whitespace.ReplaceAllString(a.Help, " ")), helpWidth)
		parts = append(parts, strings.Repeat(" ", indentFirst)+lines[0]+"\n")
		for _, l := range lines[1:] {
			parts = append(parts, strings.Repeat(" ", helpPos)+l+"\n")
		}
	} else if !strings.HasSuffix(header, "\n") {
		parts = append(parts, "\n")
	}
	if a.choiceActions != nil {
		f.indent += 2
		for _, sa := range a.choiceActions {
			parts = append(parts, f.action(sa))
		}
		f.indent -= 2
	}
	return strings.Join(parts, "")
}

// FormatUsage is the usage text, as an error prints it.
func (p *Parser) FormatUsage() string {
	return strings.Trim(newFormatter(p.Prog).usage(p.actions), "\n") + "\n"
}

// FormatHelp is the help text -h prints: usage, description, positional arguments and options.
func (p *Parser) FormatHelp() string {
	f := newFormatter(p.Prog)
	var pos, opts []*Action
	for _, a := range p.actions {
		if len(a.OptionStrings) == 0 {
			pos = append(pos, a)
		} else {
			opts = append(opts, a)
		}
	}
	// the widest invocation, at the section's indent (2) or a sub-action's (4), sets where help starts
	for _, a := range p.actions {
		l := dyn.Len(invocation(a)) + 2
		for _, sa := range a.choiceActions {
			l = max(l, dyn.Len(invocation(sa))+4)
		}
		f.actionMaxLen = max(f.actionMaxLen, l)
	}
	out := []string{f.usage(p.actions)}
	if p.Description != "" {
		text := strings.TrimSpace(f.whitespace.ReplaceAllString(p.Description, " "))
		out = append(out, strings.Join(wrap(text, max(f.width-f.indent, 11)), "\n")+"\n\n")
	}
	for _, sec := range []struct {
		heading string
		acts    []*Action
	}{{"positional arguments", pos}, {"options", opts}} {
		if len(sec.acts) == 0 {
			continue
		}
		f.indent += 2
		var items []string
		for _, a := range sec.acts {
			items = append(items, f.action(a))
		}
		f.indent -= 2
		out = append(out, "\n"+sec.heading+":\n"+strings.Join(items, "")+"\n")
	}
	help := strings.Join(out, "")
	help = f.longBreak.ReplaceAllString(help, "\n\n")
	return strings.Trim(help, "\n") + "\n"
}
