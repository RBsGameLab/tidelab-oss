// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package login is the HTTP login server (specs/login.md), ported from src/Tidelab.LoginServer.
package login

import (
	"errors"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"tidelab/internal/common"
	"tidelab/internal/httpserv"
	"tidelab/internal/zon"
)

// Account is a file state/account/<userId> (specs/save-format.md §3).
type Account struct {
	UserID, UserName, Token string
	Sex                     int32
}

func AccountFromZon(v *zon.Value) (Account, error) {
	text := func(name string) (string, error) {
		f := v.Field(name)
		if f == nil {
			return "", &zon.Error{Msg: "field ." + name + " is missing"}
		}
		return f.AsString()
	}
	var a Account
	var err error
	if a.UserID, err = text("user_id"); err != nil {
		return a, err
	}
	if a.UserName, err = text("user_name"); err != nil {
		return a, err
	}
	if a.Token, err = text("token"); err != nil {
		return a, err
	}
	if s := v.Field("sex"); s != nil {
		if a.Sex, err = s.AsInt32(); err != nil {
			return a, err
		}
	}
	return a, nil
}

func (a Account) Zon() *zon.Value {
	return zon.NewStruct(
		zon.F("user_id", zon.Str(a.UserID)),
		zon.F("user_name", zon.Str(a.UserName)),
		zon.F("token", zon.Str(a.Token)),
		zon.F("sex", zon.Int(int64(a.Sex))))
}

type Outcome int

const (
	// OK: the account exists with the token given, or was created with it.
	OK Outcome = iota
	// WrongToken: the account exists with another token.
	WrongToken
	// Unreadable: the account file could not be read or parsed.
	Unreadable
)

// Accounts are the files under state/account/.
type Accounts struct {
	mu  sync.Mutex
	Dir string
}

func NewAccounts(stateDir string) *Accounts {
	return &Accounts{Dir: filepath.Join(stateDir, "account")}
}

// IsFileName: userID can name a file in the account directory: not empty, not . or .., and
// none of < > : " / \ | ? * or bytes 0–31. The client escapes the id (Uri.EscapeDataString), so
// it never sends one of these.
func IsFileName(userID string) bool {
	if userID == "" || userID == "." || userID == ".." {
		return false
	}
	for _, r := range userID {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	return true
}

// LogIn is the account the query logs in to: an existing one if its token matches, otherwise a
// new one created from the query (sex 0).
func (a *Accounts) LogIn(q Query) (Outcome, Account) {
	if !IsFileName(q.UserID) {
		panic("login: the user id is not a file name")
	}
	path := filepath.Join(a.Dir, q.UserID)
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		created := Account{q.UserID, q.UserName, q.Token, 0}
		if _, err := common.WriteAtomic(path, zon.Write(created.Zon())); err != nil {
			panic(err) // as C#: the server answers 500
		}
		common.Info("login", "account "+q.UserID+" created")
		return OK, created
	}
	v, err := zon.ParseFile(path)
	var acc Account
	if err == nil {
		acc, err = AccountFromZon(v)
	}
	if err != nil {
		common.Error("login", "account "+q.UserID+" unreadable: "+err.Error())
		return Unreadable, Account{}
	}
	if acc.Token == q.Token {
		return OK, acc
	}
	return WrongToken, acc
}

// Query is the query string of /api/login and /api/CopyUserLogin (specs/login.md, Login).
// Values are used as they come, without percent-decoding.
type Query struct {
	LoginType                             int32
	UserID, UserName, Token               string
	UserData                              int32
	DeviceID, LoginTraceID, SourceAccount *string
	CopyUserLoginMode                     *int32
}

var known = map[string]bool{"loginType": true, "userId": true, "userName": true, "token": true, "userData": true, "deviceId": true, "loginTraceId": true, "sourceAccount": true, "copyUserLoginMode": true}

var required = []string{"loginType", "userId", "userName", "token", "userData"}

// int32Text parses as C#'s int.TryParse with AllowLeadingSign.
func int32Text(s string) (int32, bool) {
	if s == "" || strings.HasPrefix(s, "+-") || strings.HasPrefix(s, "-+") {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 32)
	return int32(n), err == nil
}

// ParseQuery is false for a bad request: a pair without '=' or with two, an unknown or
// repeated name, a number that does not parse, a loginType other than 0 or 1, or a required
// name missing.
func ParseQuery(query string, has bool) (Query, bool) {
	if !has {
		return Query{}, false
	}
	values := map[string]string{}
	for _, pair := range strings.Split(query, "&") {
		parts := strings.Split(pair, "=")
		if len(parts) != 2 || !known[parts[0]] {
			return Query{}, false
		}
		if _, dup := values[parts[0]]; dup {
			return Query{}, false
		}
		values[parts[0]] = parts[1]
	}
	for _, r := range required {
		if _, ok := values[r]; !ok {
			return Query{}, false
		}
	}
	var q Query
	var ok bool
	if q.LoginType, ok = int32Text(values["loginType"]); !ok || (q.LoginType != 0 && q.LoginType != 1) {
		return Query{}, false
	}
	if q.UserData, ok = int32Text(values["userData"]); !ok {
		return Query{}, false
	}
	if m, has := values["copyUserLoginMode"]; has {
		n, ok := int32Text(m)
		if !ok {
			return Query{}, false
		}
		q.CopyUserLoginMode = &n
	}
	opt := func(name string) *string {
		if v, ok := values[name]; ok {
			return &v
		}
		return nil
	}
	q.UserID, q.UserName, q.Token = values["userId"], values["userName"], values["token"]
	q.DeviceID, q.LoginTraceID, q.SourceAccount = opt("deviceId"), opt("loginTraceId"), opt("sourceAccount")
	return q, true
}

// Service is the login server's routes.
type Service struct {
	accounts *Accounts
	gateway  netip.AddrPort
}

func NewService(accounts *Accounts, gateway netip.AddrPort) *Service {
	return &Service{accounts, gateway}
}

func (s *Service) Handle(r httpserv.Request) *httpserv.Response {
	if r.Method != "GET" {
		return httpserv.Empty(405)
	}
	host := s.gateway.Addr().String()
	switch r.Path {
	case "/health":
		return httpserv.Empty(200)
	case "/api/ping":
		return httpserv.JSON(httpserv.JSONObject("clientIp", host))
	case "/api/login", "/api/CopyUserLogin":
		return s.logIn(r)
	}
	common.Warn("serve", "unhandled request path: "+r.Path)
	return httpserv.Empty(404)
}

func (s *Service) logIn(r httpserv.Request) *httpserv.Response {
	q, ok := ParseQuery(r.Query, r.HasQuery)
	if !ok || !IsFileName(q.UserID) {
		return httpserv.Empty(400)
	}
	outcome, acc := s.accounts.LogIn(q)
	switch outcome {
	case OK:
		udp := httpserv.RawJSON("[" + httpserv.JSONObject("host", s.gateway.Addr().String(), "port", s.gateway.Port()) + "]")
		return httpserv.JSON(httpserv.JSONObject("code", 0, "hasRpc", true, "token", acc.Token, "udpHosts", udp, "userData", q.UserData, "sex", acc.Sex))
	case WrongToken:
		return failure(1, "Login Failed", q.UserData)
	}
	return failure(-1, "Internal Server Error", q.UserData)
}

func failure(code int, message string, userData int32) *httpserv.Response {
	return httpserv.JSON(httpserv.JSONObject("code", code, "hasRpc", true, "errMessage", message, "userData", userData))
}
