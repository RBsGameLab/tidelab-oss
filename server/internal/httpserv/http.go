// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package httpserv is the plain HTTP/1.1 server the login and config servers share: one
// request per connection, only the request line is read (specs/login.md and specs/config.md,
// Listening). Ported from Tidelab.Common.Http and ServerHost.
package httpserv

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"tidelab/internal/common"
)

// Request is the request line: Method, Path up to the first '?', Query after it (HasQuery
// false when there is no '?').
type Request struct {
	Method   string
	Path     string
	Query    string
	HasQuery bool
}

// ParseLine parses METHOD SP TARGET SP VERSION (line ending removed); false for anything else:
// a missing part, an extra space, or a version that is not HTTP/….
func ParseLine(line string) (Request, bool) {
	parts := strings.Split(line, " ")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || !strings.HasPrefix(parts[2], "HTTP/") {
		return Request{}, false
	}
	path, query, has := strings.Cut(parts[1], "?")
	return Request{parts[0], path, query, has}, true
}

// Response is a status and a body with its content type, or no body (ContentType "").
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

func Empty(status int) *Response { return &Response{Status: status} }

func Bytes(status int, contentType string, body []byte) *Response {
	return &Response{status, contentType, body}
}

func JSON(json string) *Response { return &Response{200, "application/json", []byte(json)} }

// Encode is the status line, Content-Length and Content-Type when there is a body, and nothing else.
func (r *Response) Encode() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", r.Status, reason(r.Status))
	if r.ContentType != "" {
		fmt.Fprintf(&b, "Content-Length: %d\r\nContent-Type: %s\r\n", len(r.Body), r.ContentType)
	}
	b.WriteString("\r\n")
	b.Write(r.Body)
	return b.Bytes()
}

func reason(status int) string {
	switch status {
	case 200:
		return "OK"
	case 400:
		return "Bad Request"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 500:
		return "Internal Server Error"
	}
	return "Unknown"
}

// Handler answers one request; nil closes the connection without an answer (the config server
// does this for refused paths).
type Handler func(Request) *Response

const (
	// MaxLineBytes: a longer request line is dropped without an answer.
	MaxLineBytes = 16 * 1024
	readTimeout  = 10 * time.Second
	drainTimeout = time.Second
)

// Server answers requests until Close.
type Server struct {
	ln      net.Listener
	handler Handler
	scope   string
	done    chan struct{}
	wg      sync.WaitGroup
}

// Start binds addr and starts answering; scope is the log scope for the server's own lines.
func Start(addr netip.AddrPort, h Handler, scope string) (*Server, error) {
	ln, err := net.Listen("tcp", addr.String())
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, handler: h, scope: scope, done: make(chan struct{})}
	go s.accept()
	return s, nil
}

// Addr is where the server listens (the real port when started on port 0).
func (s *Server) Addr() netip.AddrPort { return s.ln.Addr().(*net.TCPAddr).AddrPort() }

// Done is closed when the server stops.
func (s *Server) Done() <-chan struct{} { return s.done }

// Close stops accepting and waits for the connections being served.
func (s *Server) Close() {
	s.ln.Close()
	<-s.done
	s.wg.Wait()
}

func (s *Server) accept() {
	defer close(s.done)
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			common.Warn(s.scope, "accept failed: "+err.Error())
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(c.(*net.TCPConn))
		}()
	}
}

func (s *Server) serve(c *net.TCPConn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(readTimeout))
	line, ok := readLine(c)
	if !ok {
		return
	}
	var resp *Response
	if req, ok := ParseLine(line); ok {
		resp = s.answer(req)
	} else {
		resp = Empty(400)
	}
	if resp == nil {
		return
	}
	if _, err := c.Write(resp.Encode()); err != nil {
		return
	}
	// End our side, then read and drop what the client still sends (its headers) until it
	// closes: closing with unread bytes resets the connection, and a reset can make the client
	// lose an answer it has not read yet.
	c.CloseWrite()
	c.SetReadDeadline(time.Now().Add(drainTimeout))
	sink := make([]byte, 4096)
	for {
		if n, err := c.Read(sink); n == 0 || err != nil {
			return
		}
	}
}

func (s *Server) answer(req Request) (resp *Response) {
	defer func() {
		if r := recover(); r != nil {
			common.Error(s.scope, fmt.Sprintf("request %s failed: %T: %v", req.Path, r, r))
			resp = Empty(500)
		}
	}()
	return s.handler(req)
}

// readLine reads up to the first line feed and returns the line without its CR LF; false when
// the peer closes first or the line is too long.
func readLine(c net.Conn) (string, bool) {
	buf := make([]byte, MaxLineBytes)
	n := 0
	for n < len(buf) {
		r, err := c.Read(buf[n:])
		if r == 0 && err != nil {
			return "", false
		}
		if i := bytes.IndexByte(buf[n:n+r], '\n'); i >= 0 {
			end := n + i
			if end > 0 && buf[end-1] == '\r' {
				end--
			}
			return string(buf[:end]), true
		}
		n += r
	}
	return "", false
}

// Main runs an HTTP server program until Ctrl+C: options, the listening line, exit codes
// (0 stopped, 1 could not start, 2 bad options).
func Main(args []string, name string, defaults map[string]string, build func(*common.Options) (Handler, error)) int {
	opts, err := common.ParseOptions(args, defaults)
	var listen netip.AddrPort
	if err == nil {
		listen, err = opts.Endpoint("listen_address")
	}
	var h Handler
	if err == nil {
		h, err = build(opts)
	}
	if err != nil {
		common.Error("init", err.Error())
		return 2
	}
	s, err := Start(listen, h, "serve")
	if err != nil {
		common.Error("init", name+" could not start: "+err.Error())
		return 1
	}
	common.Info("init", name+" is listening at "+s.Addr().String())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	select {
	case <-stop:
	case <-s.Done():
	}
	s.Close()
	return 0
}

// JSONObject writes {"k":v,...} as the C# servers do (System.Text.Json with the relaxed
// encoder): values are strings, bools, integers, or raw JSON (RawJSON).
func JSONObject(kv ...any) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(&b, kv[i].(string))
		b.WriteByte(':')
		switch v := kv[i+1].(type) {
		case string:
			writeJSONString(&b, v)
		case bool:
			b.WriteString(strconv.FormatBool(v))
		case int:
			b.WriteString(strconv.Itoa(v))
		case int32:
			b.WriteString(strconv.FormatInt(int64(v), 10))
		case int64:
			b.WriteString(strconv.FormatInt(v, 10))
		case uint16:
			b.WriteString(strconv.FormatUint(uint64(v), 10))
		case RawJSON:
			b.WriteString(string(v))
		default:
			panic(fmt.Sprintf("JSONObject: %T", v))
		}
	}
	b.WriteByte('}')
	return b.String()
}

// RawJSON is inserted as it is.
type RawJSON string

func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
