// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package httpserv

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"tidelab/internal/common"
)

var loopback0 = netip.MustParseAddrPort("127.0.0.1:0")

func TestParsesTheRequestLine(t *testing.T) {
	for line, want := range map[string]Request{
		"GET /api/login?a=1&b=2 HTTP/1.1": {"GET", "/api/login", "a=1&b=2", true},
		"GET /x HTTP/1.0":                 {"GET", "/x", "", false},
		"POST /x? HTTP/1.1":               {"POST", "/x", "", true},
		"GET /x?a?b HTTP/1.1":             {"GET", "/x", "a?b", true},
	} {
		if got, ok := ParseLine(line); !ok || got != want {
			t.Fatalf("%q: %+v %v", line, got, ok)
		}
	}
	for _, line := range []string{"", "GET /x", "GET  /x HTTP/1.1", "GET /x HTTP/1.1 extra", "GET /x FTP/1.0"} {
		if _, ok := ParseLine(line); ok {
			t.Fatalf("%q parsed", line)
		}
	}
}

func TestResponseHasOnlyStatusLengthAndType(t *testing.T) {
	for want, r := range map[string]*Response{
		"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Type: application/json\r\n\r\n{}": JSON("{}"),
		"HTTP/1.1 404 Not Found\r\n\r\n":          Empty(404),
		"HTTP/1.1 405 Method Not Allowed\r\n\r\n": Empty(405),
	} {
		if got := string(r.Encode()); got != want {
			t.Fatalf("%q", got)
		}
	}
}

// Exchange sends request and reads until the server closes.
func Exchange(t *testing.T, addr netip.AddrPort, request string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(c)
	return string(b)
}

func TestAnswersOneRequestAndCloses(t *testing.T) {
	s, err := Start(loopback0, func(r Request) *Response {
		if r.Path == "/drop" {
			return nil
		}
		return JSON(`"` + r.Path + `"`)
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := Exchange(t, s.Addr(), "GET /a?x=1 HTTP/1.1\r\nHost: x\r\nAccept: */*\r\n\r\n"); got != "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nContent-Type: application/json\r\n\r\n\"/a\"" {
		t.Fatalf("%q", got)
	}
	if got := Exchange(t, s.Addr(), "GET /drop HTTP/1.1\r\n\r\n"); got != "" {
		t.Fatalf("%q", got)
	}
	if got := Exchange(t, s.Addr(), "nonsense\r\n\r\n"); got != "HTTP/1.1 400 Bad Request\r\n\r\n" {
		t.Fatalf("%q", got)
	}
	// A bare LF ends the line too.
	if got := Exchange(t, s.Addr(), "GET /b HTTP/1.1\n\n"); !strings.HasSuffix(got, `"/b"`) {
		t.Fatalf("%q", got)
	}
}

func TestAFailingHandlerAnswers500(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	s, err := Start(loopback0, func(Request) *Response { panic(errors.New("boom")) }, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := Exchange(t, s.Addr(), "GET /x HTTP/1.1\r\n\r\n"); got != "HTTP/1.1 500 Internal Server Error\r\n\r\n" {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(log.String(), "error(test): request /x failed: *errors.errorString: boom") {
		t.Fatal(log.String())
	}
}

func TestWorksWithAnOrdinaryHttpClient(t *testing.T) {
	s, err := Start(loopback0, func(Request) *Response { return JSON(`{"ok":true}`) }, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 3; i++ {
		resp, err := http.Get("http://" + s.Addr().String() + "/x")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" || string(body) != `{"ok":true}` {
			t.Fatalf("%d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
	}
}

func TestJSONObjectEscapesAsTheRelaxedEncoder(t *testing.T) {
	got := JSONObject("a", "x\"y\\z\n<&>中", "n", 1, "b", true, "raw", RawJSON("[1]"))
	if got != `{"a":"x\"y\\z\n<&>中","n":1,"b":true,"raw":[1]}` {
		t.Fatal(got)
	}
}
