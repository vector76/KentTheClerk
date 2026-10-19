package kb

// A stand-in for a terminal on stdout: run commands as if what they print went to one, and find in what
// they printed the bytes a terminal would act on rather than show.

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Unless a test asks for a terminal, kb's tests run as if stdout were a pipe, which is what kbRun's
// buffer is. Without this a test would get the answer for the test binary's own stdout, which go test
// connects to the terminal it was run from when no package is named on its command line.
func init() { stdoutIsTerminal = func() bool { return false } }

// asIfStdoutIsTerminal tells kb, until the test ends, that its stdout is a terminal. kbRun still
// captures what a command prints; terminalControls says what a terminal would make of it.
func asIfStdoutIsTerminal(t *testing.T) {
	t.Helper()
	old := stdoutIsTerminal
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = old })
}

// terminalControl is one thing in printed output that a terminal would act on rather than show.
type terminalControl struct {
	Offset       int    // byte offset in the output
	Line, Column int    // where it starts, counted from 1; the column in code points
	Kind         string // what it is, such as "CSI escape sequence" or "C0 control BEL"
	Bytes        string // its bytes: the whole sequence, for an escape sequence
}

func (c terminalControl) String() string {
	return fmt.Sprintf("line %d, column %d (byte %d): %s %q", c.Line, c.Column, c.Offset, c.Kind, c.Bytes)
}

var c0Names = [...]string{"NUL", "SOH", "STX", "ETX", "EOT", "ENQ", "ACK", "BEL", "BS", "HT", "LF", "VT", "FF",
	"CR", "SO", "SI", "DLE", "DC1", "DC2", "DC3", "DC4", "NAK", "SYN", "ETB", "CAN", "EM", "SUB", "ESC", "FS", "GS",
	"RS", "US"}

// terminalControls finds what a terminal would act on rather than show in s: C0 controls other than
// newline and tab, DEL, C1 controls (U+0080 to U+009F), and bytes that are not UTF-8. An ESC is reported
// with the sequence it starts, read as ECMA-48 does: a CSI sequence (ESC [) to its final byte, a control
// string (ESC ] for OSC, ESC P, ESC X, ESC ^, ESC _) to BEL or the string terminator, or ESC with its
// intermediate and final bytes. A C1 control is reported alone, even when it introduces a sequence.
func terminalControls(s string) []terminalControl {
	var found []terminalControl
	line, col := 1, 1
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		var c *terminalControl
		switch {
		case r == utf8.RuneError && size == 1:
			c = &terminalControl{Kind: "byte that is not UTF-8", Bytes: s[i : i+1]}
		case r == '\x1b':
			kind, n := escapeSequence(s[i:])
			c = &terminalControl{Kind: kind, Bytes: s[i : i+n]}
			size = n
		case r < 0x20 && r != '\n' && r != '\t':
			c = &terminalControl{Kind: "C0 control " + c0Names[r], Bytes: s[i : i+size]}
		case r == 0x7f:
			c = &terminalControl{Kind: "control DEL", Bytes: s[i : i+size]}
		case r >= 0x80 && r <= 0x9f:
			c = &terminalControl{Kind: fmt.Sprintf("C1 control U+%04X", r), Bytes: s[i : i+size]}
		}
		if c != nil {
			c.Offset, c.Line, c.Column = i, line, col
			found = append(found, *c)
		}
		for _, r := range s[i : i+size] {
			if r == '\n' {
				line, col = line+1, 1
			} else {
				col++
			}
		}
		i += size
	}
	return found
}

// escapeSequence names the escape sequence s starts with (s[0] is ESC) and says how many bytes it takes.
func escapeSequence(s string) (string, int) {
	if len(s) < 2 {
		return "ESC at the end of the output", 1
	}
	switch s[1] {
	case '[':
		i := 2
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			return "CSI escape sequence", i + 1
		}
		return "unfinished CSI escape sequence", i
	case ']', 'P', 'X', '^', '_':
		kind := map[byte]string{']': "OSC", 'P': "DCS", 'X': "SOS", '^': "PM", '_': "APC"}[s[1]]
		for i := 2; i < len(s); i++ {
			switch {
			case s[i] == '\a' && s[1] == ']':
				return kind + " escape sequence", i + 1
			case s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\':
				return kind + " escape sequence", i + 2
			case strings.HasPrefix(s[i:], "\u009c"):
				return kind + " escape sequence", i + len("\u009c")
			}
		}
		return "unfinished " + kind + " escape sequence", len(s)
	}
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		return "escape sequence", i + 1
	}
	return "ESC", 1
}

// requireNoTerminalControls fails the test when out holds anything a terminal would act on rather than
// show, saying what and where, and quoting each line it is on (the raw bytes would act on the
// terminal the test log is read in).
func requireNoTerminalControls(t *testing.T, what, out string) {
	t.Helper()
	found := terminalControls(out)
	if len(found) == 0 {
		return
	}
	lines := strings.Split(out, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "%s printed %d thing(s) a terminal would act on rather than show:", what, len(found))
	for _, c := range found {
		fmt.Fprintf(&b, "\n  %s\n    in line %q", c, lines[c.Line-1])
	}
	t.Fatal(b.String())
}

func TestTerminalControls(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"plain text,\ttabs\nand ⟨newlines⟩ — and é\n", nil},
		{"\x1b[2Jhidden", []string{`line 1, column 1 (byte 0): CSI escape sequence "\x1b[2J"`}},
		{"ok\nab\x1b[8mhid\x1b[0m", []string{
			`line 2, column 3 (byte 5): CSI escape sequence "\x1b[8m"`,
			`line 2, column 10 (byte 12): CSI escape sequence "\x1b[0m"`}},
		{"\x1b]8;;https://example.com\alink\x1b]8;;\x1b\\", []string{
			`line 1, column 1 (byte 0): OSC escape sequence "\x1b]8;;https://example.com\a"`,
			`line 1, column 30 (byte 29): OSC escape sequence "\x1b]8;;\x1b\\"`}},
		{"\x1b]52;c;aGk=", []string{`line 1, column 1 (byte 0): unfinished OSC escape sequence "\x1b]52;c;aGk="`}},
		{"é\x1b7x", []string{`line 1, column 2 (byte 2): escape sequence "\x1b7"`}},
		{"end\x1b", []string{`line 1, column 4 (byte 3): ESC at the end of the output "\x1b"`}},
		{"a\rb\a\x7f", []string{
			`line 1, column 2 (byte 1): C0 control CR "\r"`,
			`line 1, column 4 (byte 3): C0 control BEL "\a"`,
			`line 1, column 5 (byte 4): control DEL "\x7f"`}},
		{"x\u009b2J\xff", []string{
			`line 1, column 2 (byte 1): C1 control U+009B "\u009b"`,
			`line 1, column 5 (byte 5): byte that is not UTF-8 "\xff"`}},
	}
	for _, c := range cases {
		var got []string
		for _, f := range terminalControls(c.in) {
			got = append(got, f.String())
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("terminalControls(%q):\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestAsIfStdoutIsTerminal(t *testing.T) {
	if stdoutIsTerminal() {
		t.Fatal("stdout is taken for a terminal in a test that did not ask for one")
	}
	old := stdoutIsTerminal
	defer func() { stdoutIsTerminal = old }()
	stdoutIsTerminal = func() bool { return false }
	t.Run("on a terminal", func(t *testing.T) {
		asIfStdoutIsTerminal(t)
		if !stdoutIsTerminal() {
			t.Fatal("stdout is not taken for a terminal")
		}
	})
	if stdoutIsTerminal() {
		t.Fatal("stdout is still taken for a terminal after the test that asked for one")
	}
}
