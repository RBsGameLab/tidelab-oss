// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package cfgserver is the HTTP config and CDN server (specs/config.md), ported from
// src/Tidelab.ConfigServer. It serves state/cfg/index/: <path>.raw as it is, otherwise <path>
// encrypted with the key and IV in state/cfg/ (in the clear when there are none, R-173c). Files are read at each request, so a file
// changed on disk is served changed.
package cfgserver

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/httpserv"
)

type Service struct {
	cfg string
}

func NewService(stateDir string) *Service {
	abs, err := filepath.Abs(filepath.Join(stateDir, "cfg"))
	if err != nil {
		abs = filepath.Join(stateDir, "cfg")
	}
	return &Service{cfg: abs}
}

func (s *Service) indexRoot() string { return filepath.Join(s.cfg, "index") }

// Handle is the answer, or nil to close the connection without one.
func (s *Service) Handle(r httpserv.Request) *httpserv.Response {
	if r.Method != "GET" {
		return httpserv.Empty(405)
	}
	common.Info("serve", "request: "+r.Path)
	file, ok := s.Resolve(r.Path)
	if !ok {
		common.Warn("serve", "refused path: "+r.Path)
		return nil
	}
	if common.FileExists(file + ".raw") {
		b, err := os.ReadFile(file + ".raw")
		if err != nil {
			panic(err)
		}
		return httpserv.Bytes(200, "application/octet-stream", b)
	}
	if !common.FileExists(file) {
		common.Warn("serve", "requested file not found: "+r.Path)
		return httpserv.Empty(404)
	}
	plain, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	if !s.hasKey() {
		// R-173c: no cfg/key and cfg/iv: the JSON goes out in the clear; the patched launcher
		// (RB.RBCfg) passes plain JSON past its native decrypt.
		return httpserv.Bytes(200, "application/json", plain)
	}
	key, iv, ok := s.readKey()
	if !ok {
		return nil
	}
	body := base64.StdEncoding.EncodeToString(Encrypt(plain, key, iv))
	return httpserv.Bytes(200, "application/json", []byte(body))
}

// Resolve is the file a request path names under the index; false for a path to refuse:
// empty, not starting with one '/', containing "../", and also (tidelab) any ".." segment, a
// backslash or a colon, or anything that would resolve outside the index.
func (s *Service) Resolve(path string) (string, bool) {
	if len(path) <= 1 || path[0] != '/' || strings.HasPrefix(path, "//") {
		return "", false
	}
	if strings.Contains(path, "../") || strings.ContainsAny(path, "\\:\x00") {
		return "", false
	}
	rel := path[1:]
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", false
		}
	}
	root := s.indexRoot()
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

// Encrypt is AES-256-CBC with PKCS#7 padding: a whole block of 0x10 when the length is a
// multiple of 16.
func Encrypt(plain, key, iv []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	pad := 16 - len(plain)%16
	data := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(data, data)
	return data
}

// hasKey: cfg/key or cfg/iv exists. With neither, files are served in the clear (R-173c); with
// only one, or a bad one, readKey refuses as before.
func (s *Service) hasKey() bool {
	return common.FileExists(filepath.Join(s.cfg, "key")) || common.FileExists(filepath.Join(s.cfg, "iv"))
}

func (s *Service) readKey() (key, iv []byte, ok bool) {
	read := func(name string) ([]byte, error) {
		text, err := os.ReadFile(filepath.Join(s.cfg, name))
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(strings.TrimSpace(string(text)))
	}
	key, err := read("key")
	if err == nil {
		iv, err = read("iv")
	}
	if err != nil {
		common.Error("serve", "cfg/key or cfg/iv unreadable: "+err.Error())
		return nil, nil, false
	}
	if len(key) != 32 || len(iv) != 16 {
		common.Error("serve", fmt.Sprintf("cfg/key must hold 32 bytes and cfg/iv 16, found %d and %d", len(key), len(iv)))
		return nil, nil, false
	}
	return key, iv, true
}
