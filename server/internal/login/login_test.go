// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package login

import (
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/httpserv"
	"tidelab/internal/zon"
)

const clientLogin = "loginType=0&userId=3838&userName=3838&token=1&userData=7&loginTraceId=abc"

type rig struct {
	state   string
	service *Service
}

func newRig(t *testing.T) *rig {
	state := t.TempDir()
	return &rig{state, NewService(NewAccounts(state), netip.MustParseAddrPort("127.0.0.1:13100"))}
}

func (r *rig) file(id string) string { return filepath.Join(r.state, "account", id) }

func (r *rig) write(t *testing.T, id, text string) {
	if err := os.MkdirAll(filepath.Join(r.state, "account"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.file(id), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) get(target string, method ...string) (int, string, string) {
	m := "GET"
	if len(method) > 0 {
		m = method[0]
	}
	req, _ := httpserv.ParseLine(m + " " + target + " HTTP/1.1")
	resp := r.service.Handle(req)
	return resp.Status, resp.ContentType, string(resp.Body)
}

func read(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFirstLoginCreatesTheAccount(t *testing.T) {
	r := newRig(t)
	status, typ, body := r.get("/api/login?" + clientLogin)
	if status != 200 || typ != "application/json" || body != `{"code":0,"hasRpc":true,"token":"1","udpHosts":[{"host":"127.0.0.1","port":13100}],"userData":7,"sex":0}` {
		t.Fatal(status, typ, body)
	}
	if got := read(t, r.file("3838")); got != ".{\n    .user_id = \"3838\",\n    .user_name = \"3838\",\n    .token = \"1\",\n    .sex = 0,\n}\n" {
		t.Fatalf("%q", got)
	}
}

func TestAnExistingAccountAnswersItsOwnTokenAndSex(t *testing.T) {
	r := newRig(t)
	r.write(t, "3838", ".{\n    .user_id = \"3838\",\n    .user_name = \"someone\",\n    .token = \"1\",\n    .sex = 1,\n    .written_later = .{ 1, 2 },\n}")
	before := read(t, r.file("3838"))
	status, _, body := r.get("/api/login?" + clientLogin)
	if status != 200 || body != `{"code":0,"hasRpc":true,"token":"1","udpHosts":[{"host":"127.0.0.1","port":13100}],"userData":7,"sex":1}` {
		t.Fatal(status, body)
	}
	if read(t, r.file("3838")) != before {
		t.Fatal("account rewritten")
	}
}

func TestAnotherTokenFails(t *testing.T) {
	r := newRig(t)
	r.write(t, "3838", `.{ .user_id = "3838", .user_name = "3838", .token = "secret", .sex = 0 }`)
	status, typ, body := r.get("/api/login?" + clientLogin)
	if status != 200 || typ != "application/json" || body != `{"code":1,"hasRpc":true,"errMessage":"Login Failed","userData":7}` {
		t.Fatal(status, typ, body)
	}
}

func TestAnUnreadableAccountIsAnInternalError(t *testing.T) {
	for _, content := range []string{"not zon at all", `.{ .user_id = "3838", .user_name = "3838" }`, `.{ .user_id = "3838", .user_name = "3838", .token = 1, .sex = 0 }`} {
		r := newRig(t)
		r.write(t, "3838", content)
		log, restore := common.CaptureLog()
		status, _, body := r.get("/api/login?" + clientLogin)
		restore()
		if status != 200 || body != `{"code":-1,"hasRpc":true,"errMessage":"Internal Server Error","userData":7}` {
			t.Fatal(status, body)
		}
		if !strings.Contains(log.String(), "error(login): account 3838 unreadable") {
			t.Fatal(log.String())
		}
		if read(t, r.file("3838")) != content {
			t.Fatal("account changed")
		}
	}
}

func TestCopyUserLoginIsTheSameLogin(t *testing.T) {
	r := newRig(t)
	status, _, body := r.get("/api/CopyUserLogin?loginType=0&userId=new&userName=new&token=1&userData=-3&loginTraceId=t&sourceAccount=3838&copyUserLoginMode=1")
	if status != 200 || !strings.Contains(body, `"code":0`) || !strings.HasSuffix(body, `"userData":-3,"sex":0}`) {
		t.Fatal(status, body)
	}
	if !common.FileExists(r.file("new")) {
		t.Fatal("no account file")
	}
}

func TestValuesAreNotPercentDecoded(t *testing.T) {
	r := newRig(t)
	r.get("/api/login?loginType=1&userId=a%40b&userName=x%20y&token=t%2B&userData=0&deviceId=0&loginTraceId=z")
	v, err := zon.ParseFile(r.file("a%40b"))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := AccountFromZon(v); err != nil || a != (Account{"a%40b", "x%20y", "t%2B", 0}) {
		t.Fatal(a, err)
	}
}

func TestBadQueriesAre400(t *testing.T) {
	for _, q := range []string{
		"",
		"loginType=0&userId=a&userName=a&token=1",
		"loginType=0&userId=a&userName=a&token=1&userData=x",
		"loginType=2&userId=a&userName=a&token=1&userData=0",
		"loginType=0&userId=a&userName=a&token=1&userData=0&extra=1",
		"loginType=0&userId=a&userName=a&token=1&userData=0&deviceId",
		"loginType=0&userId=a&userName=a&token=1=2&userData=0",
		"loginType=0&userId=a&userName=a&token=1&userData=0&copyUserLoginMode=q",
		"loginType=0&userId=a&userName=a&token=1&userData=0&userData=1",
		"loginType=0&userId=a&userName=a&token=1&userData=0&",
		"loginType=0&userId=a&userName=a&token=1&userData=99999999999",
		"loginType=0&userId=..&userName=a&token=1&userData=0",
		"loginType=0&userId=a/b&userName=a&token=1&userData=0",
		"loginType=0&userId=&userName=a&token=1&userData=0",
	} {
		r := newRig(t)
		status, typ, body := r.get("/api/login?" + q)
		if status != 400 || typ != "" || body != "" {
			t.Fatalf("%q: %d %q %q", q, status, typ, body)
		}
		if _, err := os.Stat(filepath.Join(r.state, "account")); !os.IsNotExist(err) {
			t.Fatalf("%q: account directory made", q)
		}
	}
	if status, _, _ := newRig(t).get("/api/login"); status != 400 {
		t.Fatal(status)
	}
}

func TestPingHealthUnknownAndMethods(t *testing.T) {
	r := newRig(t)
	if s, typ, b := r.get("/api/ping"); s != 200 || typ != "application/json" || b != `{"clientIp":"127.0.0.1"}` {
		t.Fatal(s, typ, b)
	}
	if s, typ, b := r.get("/health"); s != 200 || typ != "" || b != "" {
		t.Fatal(s, typ, b)
	}
	log, restore := common.CaptureLog()
	s, _, _ := r.get("/TDCfg/config")
	restore()
	if s != 404 || !strings.Contains(log.String(), "warning(serve): unhandled request path: /TDCfg/config\n") {
		t.Fatal(s, log.String())
	}
	if s, _, _ := r.get("/api/login?"+clientLogin, "POST"); s != 405 || common.FileExists(r.file("3838")) {
		t.Fatal(s)
	}
}

func TestServesOverTcp(t *testing.T) {
	r := newRig(t)
	s, err := httpserv.Start(netip.MustParseAddrPort("127.0.0.1:0"), r.service.Handle, "serve")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	c.Write([]byte("GET /api/login?" + clientLogin + " HTTP/1.1\r\nHost: 127.0.0.1:10001\r\nContent-Type: application/json\r\n\r\n"))
	answer, _ := io.ReadAll(c)
	if !strings.HasPrefix(string(answer), "HTTP/1.1 200 OK\r\nContent-Length: 104\r\nContent-Type: application/json\r\n\r\n{\"code\":0,") {
		t.Fatalf("%q", answer)
	}
}
