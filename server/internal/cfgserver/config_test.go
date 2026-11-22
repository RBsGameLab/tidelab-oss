// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package cfgserver

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/httpserv"
)

const id = "zXGBurT1FHY5eBpEhgbCw7n85imvi3XR"

var key, iv = func() ([]byte, []byte) {
	k, v := make([]byte, 32), make([]byte, 16)
	for i := range k {
		k[i] = byte(i*7 + 1)
	}
	for i := range v {
		v[i] = byte(200 - i)
	}
	return k, v
}()

type rig struct {
	state   string
	service *Service
}

func newRig(t *testing.T) *rig {
	r := &rig{state: t.TempDir()}
	r.write(t, "cfg/key", []byte(base64.StdEncoding.EncodeToString(key)))
	r.write(t, "cfg/iv", []byte(base64.StdEncoding.EncodeToString(iv)+"\n"))
	r.service = NewService(r.state)
	return r
}

func (r *rig) write(t *testing.T, rel string, b []byte) {
	p := filepath.Join(r.state, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) get(path string, method ...string) *httpserv.Response {
	m := "GET"
	if len(method) > 0 {
		m = method[0]
	}
	return r.service.Handle(httpserv.Request{Method: m, Path: path})
}

// referenceCBC is CBC by hand over single-block AES, with PKCS#7 padding by hand.
func referenceCBC(plain, key, iv []byte) []byte {
	pad := 16 - len(plain)%16
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(padded))
	prev := append([]byte(nil), iv...)
	for at := 0; at < len(padded); at += 16 {
		x := make([]byte, 16)
		for i := range x {
			x[i] = padded[at+i] ^ prev[i]
		}
		block.Encrypt(out[at:at+16], x)
		prev = out[at : at+16]
	}
	return out
}

func decode(t *testing.T, b []byte) []byte {
	out, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRawFilesGoOutUnchangedAndWin(t *testing.T) {
	r := newRig(t)
	raw := []byte{0, 1, 2, 0xFF, '\n'}
	r.write(t, "cfg/index/"+id+"/Windows/3.8.0/ManifestAggregated.txt.raw", raw)
	resp := r.get("/" + id + "/Windows/3.8.0/ManifestAggregated.txt")
	if resp.Status != 200 || resp.ContentType != "application/octet-stream" || !bytes.Equal(resp.Body, raw) {
		t.Fatal(resp)
	}
	r.write(t, "cfg/index/"+id+"/index.json", []byte("plain"))
	r.write(t, "cfg/index/"+id+"/index.json.raw", []byte("raw"))
	if b := r.get("/" + id + "/index.json").Body; string(b) != "raw" {
		t.Fatal(string(b))
	}
}

func TestAPlainFileGoesOutEncryptedAndChanged(t *testing.T) {
	r := newRig(t)
	content := `{"default":{"CdnUrl":"http://127.0.0.1:5500/"}}`
	r.write(t, "cfg/index/"+id+"/index.json", []byte(content))
	resp := r.get("/" + id + "/index.json")
	if resp.Status != 200 || resp.ContentType != "application/json" || !bytes.Equal(decode(t, resp.Body), referenceCBC([]byte(content), key, iv)) {
		t.Fatal(resp)
	}
	r.write(t, "cfg/index/"+id+"/Windows/config.json", []byte("one"))
	first := r.get("/" + id + "/Windows/config.json").Body
	r.write(t, "cfg/index/"+id+"/Windows/config.json", []byte("two"))
	second := r.get("/" + id + "/Windows/config.json").Body
	if bytes.Equal(first, second) || !bytes.Equal(decode(t, second), referenceCBC([]byte("two"), key, iv)) {
		t.Fatal("not re-read")
	}
}

func TestWithoutKeyAndIvAPlainFileGoesOutInTheClear(t *testing.T) {
	r := &rig{state: t.TempDir()}
	r.service = NewService(r.state)
	content := `{"default":{"CdnUrl":"http://127.0.0.1:5500/"}}`
	r.write(t, "cfg/index/"+id+"/index.json", []byte(content))
	resp := r.get("/" + id + "/index.json")
	if resp == nil || resp.Status != 200 || resp.ContentType != "application/json" || string(resp.Body) != content {
		t.Fatal(resp)
	}
	// Only one of the two is still an error: no answer.
	r.write(t, "cfg/key", []byte(base64.StdEncoding.EncodeToString(key)))
	if r.get("/"+id+"/index.json") != nil {
		t.Fatal("answered with a key but no iv")
	}
}

func TestMissingFilesDirectoriesAndMethods(t *testing.T) {
	r := newRig(t)
	log, restore := common.CaptureLog()
	resp := r.get("/" + id + "/Windows/nothing.json")
	restore()
	if resp.Status != 404 || resp.ContentType != "" ||
		!strings.Contains(log.String(), "info(serve): request: /"+id+"/Windows/nothing.json\n") ||
		!strings.Contains(log.String(), "warning(serve): requested file not found: /"+id+"/Windows/nothing.json\n") {
		t.Fatal(resp, log.String())
	}
	r.write(t, "cfg/index/"+id+"/Windows/config.json", []byte("x"))
	if s := r.get("/" + id + "/Windows").Status; s != 404 {
		t.Fatal(s)
	}
	r.write(t, "cfg/index/"+id+"/index.json", []byte("x"))
	if s := r.get("/"+id+"/index.json", "POST").Status; s != 405 {
		t.Fatal(s)
	}
}

func TestRefusedPathsCloseWithoutAnAnswer(t *testing.T) {
	r := newRig(t)
	r.write(t, "secret", []byte("do not serve"))
	r.write(t, "cfg/secret", []byte("do not serve"))
	for _, p := range []string{"/", "", "//etc/passwd", "/a/../../secret", "/../secret", "/a/..", `/a\..\..\secret`, "/C:/Windows/win.ini", "no-slash"} {
		if resp := r.get(p); resp != nil {
			t.Fatalf("%q answered %d", p, resp.Status)
		}
	}
}

func TestAKeyOfTheWrongLengthGivesNoAnswer(t *testing.T) {
	r := newRig(t)
	r.write(t, "cfg/index/"+id+"/index.json", []byte("x"))
	r.write(t, "cfg/key", []byte(base64.StdEncoding.EncodeToString(make([]byte, 16))))
	log, restore := common.CaptureLog()
	resp := r.get("/" + id + "/index.json")
	restore()
	if resp != nil || !strings.Contains(log.String(), "cfg/key must hold 32 bytes") {
		t.Fatal(resp, log.String())
	}
}

func TestEncryptionIsAes256CbcWithPkcs7(t *testing.T) {
	for _, n := range []int{0, 15, 16, 17, 100} {
		plain := make([]byte, n)
		for i := range plain {
			plain[i] = byte(i*31 + 5)
		}
		c := Encrypt(plain, key, iv)
		if len(c) != (n/16+1)*16 || !bytes.Equal(c, referenceCBC(plain, key, iv)) {
			t.Fatal(n)
		}
	}
	// NIST SP 800-38A, F.2.5 CBC-AES256.Encrypt, first two blocks.
	k, _ := hex.DecodeString("603deb1015ca71be2b73aef0857d77811f352c073b6108d72d9810a30914dff4")
	v, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	p, _ := hex.DecodeString("6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e51")
	c := Encrypt(p, k, v)
	if len(c) != 48 || hex.EncodeToString(c[:32]) != "f58c4c04d6e5f1ba779eabfb5f7bfbd69cfc4e967edb808d679f777bc6702c7d" {
		t.Fatal(hex.EncodeToString(c))
	}
}

func TestServesOverTcp(t *testing.T) {
	r := newRig(t)
	r.write(t, "cfg/index/"+id+"/Windows/KeyList.json", []byte("[]"))
	s, err := httpserv.Start(netip.MustParseAddrPort("127.0.0.1:0"), r.service.Handle, "serve")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exchange := func(req string) string {
		c, err := net.Dial("tcp", s.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		c.Write([]byte(req))
		b, _ := io.ReadAll(c)
		return string(b)
	}
	body := base64.StdEncoding.EncodeToString(referenceCBC([]byte("[]"), key, iv))
	if got := exchange("GET /" + id + "/Windows/KeyList.json HTTP/1.1\r\nHost: x\r\n\r\n"); got != "HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\nContent-Type: application/json\r\n\r\n"+body {
		t.Fatalf("%q", got)
	}
	if got := exchange("GET //x HTTP/1.1\r\n\r\n"); got != "" {
		t.Fatalf("%q", got)
	}
}
