package kb

// kb/__main__.py: the argument parser and the command dispatch.

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"kenttheclerk/internal/argparse"
	"kenttheclerk/internal/py"
)

// textWriter writes as a Python text-mode stream does: on Windows "\n" goes out as "\r\n".
type textWriter struct{ w io.Writer }

func (t textWriter) Write(p []byte) (int, error) {
	if !py.Windows {
		return t.w.Write(p)
	}
	out := make([]byte, 0, len(p)+16)
	for _, c := range p {
		if c == '\n' {
			out = append(out, '\r')
		}
		out = append(out, c)
	}
	if _, err := t.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// flushingWriter flushes the buffered stdout before anything goes to stderr, so the two streams
// keep their relative order on a shared terminal.
type flushingWriter struct {
	before *bufio.Writer
	w      io.Writer
}

func (f flushingWriter) Write(p []byte) (int, error) {
	f.before.Flush()
	return f.w.Write(p)
}

var stdoutBuf *bufio.Writer

// flushStdout pushes out what has been printed so far (for kb serve, which runs until stopped).
func flushStdout() {
	if stdoutBuf != nil {
		stdoutBuf.Flush()
	}
}

func newParser() *argparse.Parser {
	ap := argparse.New("kb", "conversation-grounded knowledge base tools")
	ap.Subcommands("cmd")
	addSnapshotParser(ap)
	addIngestParser(ap)
	addShowParser(ap)
	addAttestParser(ap)
	addRecordParsers(ap)
	addLinksParser(ap)
	addCheckParser(ap)
	addIndexParsers(ap)
	addServeParser(ap)
	addVersionParser(ap)
	addWhereParser(ap)
	addRulesParser(ap)
	addPinParser(ap)
	addSetupParsers(ap)
	return ap
}

// Main runs kb with the given arguments (without the program name) and returns the exit status.
func Main(args []string) (code int) {
	stdoutBuf = bufio.NewWriterSize(textWriter{os.Stdout}, 64*1024)
	Stdout = stdoutBuf
	Stderr = flushingWriter{stdoutBuf, textWriter{os.Stderr}}
	defer stdoutBuf.Flush()
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case exitCode:
				code = int(e)
			case *py.Exception:
				// An exception the Python version would not have caught: it would print a traceback
				// and exit with status 1.
				fmt.Fprintf(Stderr, "kb: unexpected %s: %s\n", e.Type, e.Msg)
				code = 1
			default:
				// a fault in the port itself; exit as an uncaught exception would
				fmt.Fprintf(Stderr, "kb: internal error: %v\n", r)
				code = 1
			}
		}
	}()
	ap := newParser()
	ns, sub, exit := ap.Parse(args, Stdout, Stderr)
	if exit != nil {
		return exit.Code
	}
	return sub.Run(ns)
}
