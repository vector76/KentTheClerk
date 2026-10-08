package kb

// The HTTP side of kb serve, as Python's http.server.BaseHTTPRequestHandler does it under
// ThreadingHTTPServer: HTTP/1.0, one request per connection, the request line parsed leniently (any
// path is passed to the handler as sent, malformed percent-escapes included), "//" collapsed, error
// pages from send_error. Go's net/http would reject or rewrite some of those requests itself.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	kent "kenttheclerk"
	"kenttheclerk/internal/py"
)

// resp collects one response: status, headers in the order they are set, body.
type resp struct {
	status  int
	reason  string // send_response(code, message): the message replaces the standard phrase
	headers [][2]string
	body    []byte
}

func (w *resp) set(k, v string) { w.headers = append(w.headers, [2]string{k, v}) }

var statusText = map[int][2]string{ // phrase, description (http.HTTPStatus)
	200: {"OK", "Request fulfilled, document follows"},
	302: {"Found", "Object moved temporarily -- see URI list"},
	400: {"Bad Request", "Bad request syntax or unsupported method"},
	404: {"Not Found", "Nothing matches the given URI"},
	414: {"URI Too Long", "URI is too long"},
	431: {"Request Header Fields Too Large", "The server is unwilling to process the request because its header fields are too large"},
	501: {"Not Implemented", "Server does not support this operation"},
	505: {"HTTP Version Not Supported", "Cannot fulfill request"},
}

const errorPage = `<!DOCTYPE HTML>
<html lang="en">
    <head>
        <meta charset="utf-8">
        <title>Error response</title>
    </head>
    <body>
        <h1>Error response</h1>
        <p>Error code: %d</p>
        <p>Message: %s.</p>
        <p>Error code explanation: %d - %s.</p>
    </body>
</html>
`

// errorResp is send_error(code, message, explain).
func errorResp(code int, message, explain string) *resp {
	st := statusText[code]
	if message == "" {
		message = st[0]
	}
	if explain == "" {
		explain = st[1]
	}
	w := &resp{status: code, reason: message}
	w.set("Connection", "close")
	w.body = []byte(fmt.Sprintf(errorPage, code, htmlEscape(message, false), code, htmlEscape(explain, false)))
	w.set("Content-Type", "text/html;charset=utf-8")
	w.set("Content-Length", strconv.Itoa(len(w.body)))
	return w
}

const maxLine = 65536

// readLine is rfile.readline(maxLine + 1).
func readLine(br *bufio.Reader) []byte {
	var line []byte
	for len(line) <= maxLine {
		c, err := br.ReadByte()
		if err != nil {
			break
		}
		line = append(line, c)
		if c == '\n' {
			break
		}
	}
	return line
}

// latin1 is str(b, "iso-8859-1").
func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !py.IsDecimal(r) {
			return false
		}
	}
	return true
}

// handleConn is handle_one_request for one connection; the connection is then closed (HTTP/1.0).
func (h *kbServer) handleConn(conn net.Conn) {
	defer func() {
		// socketserver's shutdown_request: shut the write side, then close. Reading what the client
		// still sends (briefly) keeps the close from turning into a reset that loses the response.
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
			conn.SetReadDeadline(time.Now().Add(time.Second))
			io.Copy(io.Discard, io.LimitReader(conn, 1<<20))
		}
		conn.Close()
	}()
	br := bufio.NewReader(conn)
	raw := readLine(br)
	if len(raw) == 0 {
		return
	}
	version := "HTTP/0.9"
	send := func(w *resp, command string) {
		var b bytes.Buffer
		if version != "HTTP/0.9" {
			reason := w.reason
			if reason == "" {
				reason = statusText[w.status][0]
			}
			fmt.Fprintf(&b, "HTTP/1.0 %d %s\r\n", w.status, reason)
			fmt.Fprintf(&b, "Server: BaseHTTP/0.6 kb/%s\r\n", kent.Version())
			fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"))
			for _, hv := range w.headers {
				fmt.Fprintf(&b, "%s: %s\r\n", hv[0], hv[1])
			}
			b.WriteString("\r\n")
		}
		if command != "HEAD" {
			b.Write(w.body)
		}
		conn.Write(b.Bytes())
	}
	if len(raw) > maxLine {
		version = ""
		send(errorResp(414, "", ""), "")
		return
	}
	requestline := strings.TrimRight(latin1(raw), "\r\n")
	words := py.Fields(requestline)
	if len(words) == 0 {
		return
	}
	if len(words) >= 3 {
		v := words[len(words)-1]
		bad := !strings.HasPrefix(v, "HTTP/")
		var nums []string
		if !bad {
			nums = strings.Split(strings.SplitN(v, "/", 2)[1], ".")
			bad = len(nums) != 2 || !isDigits(nums[0]) || !isDigits(nums[1]) || py.Len(nums[0]) > 10 || py.Len(nums[1]) > 10
		}
		if bad {
			send(errorResp(400, "Bad request version ("+py.StrRepr(v)+")", ""), "")
			return
		}
		if major, _ := py.ParseInt(nums[0]); major.Int64() >= 2 {
			send(errorResp(505, "Invalid HTTP version ("+strings.SplitN(v, "/", 2)[1]+")", ""), "")
			return
		}
		version = v
	}
	if len(words) < 2 || len(words) > 3 {
		send(errorResp(400, "Bad request syntax ("+py.StrRepr(requestline)+")", ""), "")
		return
	}
	command, path := words[0], words[1]
	if len(words) == 2 && command != "GET" {
		send(errorResp(400, "Bad HTTP/0.9 request type ("+py.StrRepr(command)+")", ""), command)
		return
	}
	if strings.HasPrefix(path, "//") {
		path = "/" + strings.TrimLeft(path, "/")
	}
	// http.client.parse_headers: lines up to the blank one, each at most 65536 bytes, at most 100
	for n := 0; ; {
		line := readLine(br)
		if len(line) > maxLine {
			send(errorResp(431, "Line too long", fmt.Sprintf("got more than %d bytes when reading header line", maxLine)), command)
			return
		}
		n++
		if n > 100 {
			send(errorResp(431, "Too many headers", "got more than 100 headers"), command)
			return
		}
		if s := string(line); s == "\r\n" || s == "\n" || s == "" {
			break
		}
	}
	if command != "GET" {
		send(errorResp(501, "Unsupported method ("+py.StrRepr(command)+")", ""), command)
		return
	}
	w := &resp{status: 200}
	ok := func() (ok bool) {
		defer func() {
			if rec := recover(); rec != nil {
				// the Python handler dies with a traceback and the connection closes with no response
				fmt.Fprintf(os.Stderr, "kb serve: %v\n", rec)
				ok = false
			}
		}()
		h.get(w, path)
		return true
	}()
	if ok {
		send(w, command)
	}
}

func (h *kbServer) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}
