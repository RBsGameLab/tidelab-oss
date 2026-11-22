// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/panel"
	"tidelab/internal/world"
)

// R-160: the content panel page's API (specs/panel.md §3).

const testNow = 1_800_000_000

func testPanel(t *testing.T) (*Panel, *panel.Overrides) {
	tb := (&data.DataTables{
		Activity: []*data.Activity{
			{Id: 100200001, Type: game.SignType}, {Id: 100200002, Type: game.SignType},
			{Id: 106800001, Type: 68},
		},
		ActivitySign: []*data.ActivitySign{{Id: 100200001}, {Id: 100200002}},
	}).Reindex()
	wd := world.NewWorldData(tb, nil)
	wd.Panel = panel.Open(t.TempDir())
	return &Panel{Data: wd, Now: func() int64 { return testNow }}, wd.Panel
}

func do(t *testing.T, p *Panel, method, path, body string, edit func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	if edit != nil {
		edit(r)
	}
	w := httptest.NewRecorder()
	p.Handler().ServeHTTP(w, r)
	return w
}

func TestThePageAndTheListAreServed(t *testing.T) {
	p, _ := testPanel(t)
	if w := do(t, p, "GET", "/", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "内容面板") {
		t.Fatal(w.Code)
	}
	w := do(t, p, "GET", "/api/activities", "", nil)
	var out struct {
		Now   int64
		Slots []struct {
			Key       string
			Supported bool
			Edition   int32
			Open      bool
			Editions  []struct{ ID int32 }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Now != testNow {
		t.Fatal(err, w.Body.String())
	}
	byKey := map[string]int{}
	for i, s := range out.Slots {
		byKey[s.Key] = i
	}
	sign := out.Slots[byKey["2"]]
	if !sign.Supported || sign.Edition != 100200002 || !sign.Open || len(sign.Editions) != 2 {
		t.Fatal(sign)
	}
	if other := out.Slots[byKey["68"]]; other.Supported || other.Open {
		t.Fatal(other)
	}
}

func TestSavingAnOverrideAndResettingIt(t *testing.T) {
	p, o := testPanel(t)
	w := do(t, p, "POST", "/api/activities/2", `{"off":false,"edition":100200001,"begin":1799990000,"end":1800090000}`, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if a, ok := o.Activity("2"); !ok || a != (panel.Activity{Edition: 100200001, Begin: 1799990000, End: 1800090000}) {
		t.Fatal(a)
	}
	if c := o.Changes(1); len(c) != 1 || !strings.Contains(c[0], "类型 2：开启，100200001") {
		t.Fatal(c)
	}
	if w := do(t, p, "POST", "/api/activities/2/reset", "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, ok := o.Activity("2"); ok {
		t.Fatal("reset")
	}
}

func TestBadRequestsAreRefused(t *testing.T) {
	p, o := testPanel(t)
	cases := []struct {
		path, body string
		code       int
	}{
		{"/api/activities/2", `{"edition":106800001}`, 400},   // not a sign-in
		{"/api/activities/2", `{"begin":200,"end":100}`, 400}, // backwards
		{"/api/activities/2", `{"begin":200}`, 400},           // half a window
		{"/api/activities/2", `not json`, 400},                // garbage
		{"/api/activities/68", `{"edition":106800001}`, 404},  // not supported
		{"/api/activities/99", `{}`, 404},                     // no such type
	}
	for _, c := range cases {
		if w := do(t, p, "POST", c.path, c.body, nil); w.Code != c.code {
			t.Fatal(c, w.Code)
		}
	}
	if len(o.Activities()) != 0 {
		t.Fatal(o.Activities())
	}
}

func TestOnlyThisMachineAndThisPageMayAsk(t *testing.T) {
	p, o := testPanel(t)
	if w := do(t, p, "GET", "/api/activities", "", func(r *http.Request) { r.RemoteAddr = "192.168.1.5:4000" }); w.Code != 403 {
		t.Fatal(w.Code)
	}
	evil := func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }
	if w := do(t, p, "POST", "/api/activities/2", `{"off":true}`, evil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	same := func(r *http.Request) { r.Host = "127.0.0.1:13180"; r.Header.Set("Origin", "http://127.0.0.1:13180") }
	if w := do(t, p, "POST", "/api/activities/2", `{"off":true}`, same); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if a, _ := o.Activity("2"); !a.Off {
		t.Fatal(a)
	}
}

func TestLoadNamesReadsTSVsAndToleratesMissingFiles(t *testing.T) {
	empty := LoadNames(t.TempDir())
	if len(empty.Activity) != 0 || len(empty.Item) != 0 || len(empty.Gacha) != 0 {
		t.Fatal(empty)
	}
	primary := t.TempDir()
	fallback := t.TempDir()
	if err := os.WriteFile(filepath.Join(primary, "activity_names.tsv"), []byte("100200001\t签到活动\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallback, "activity_names.tsv"), []byte("100200001\t旧名\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallback, "item_names.tsv"), []byte("3\t星声\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallback, "gacha_names.tsv"), []byte("100001\t限定角色\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names := LoadNames("", primary, fallback)
	if names.Activity[100200001] != "签到活动" || names.Item[3] != "星声" || names.Gacha[100001] != "限定角色" {
		t.Fatalf("%+v", names)
	}
	p, _ := testPanel(t)
	p.Names = names
	w := do(t, p, "GET", "/api/activities", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "签到活动") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestTheShopAndGachaPagesAreServed(t *testing.T) {
	p, o := testPanel(t)
	if w := do(t, p, "GET", "/api/shop", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"goods"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do(t, p, "GET", "/api/gacha", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"slot":"role"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	cases := []struct {
		path, body string
		code       int
	}{
		{"/api/shop/12345", `{"price":1,"limit":0,"update":0}`, 404}, // no such good
		{"/api/gacha/role", `{"id":42}`, 404},                        // the test data has no limited banners
		{"/api/gacha/other", `{"id":1}`, 404},
	}
	for _, c := range cases {
		if w := do(t, p, "POST", c.path, c.body, nil); w.Code != c.code {
			t.Fatal(c, w.Code)
		}
	}
	if len(o.Goods()) != 0 {
		t.Fatal(o.Goods())
	}
}

func TestTheTowerPageIsServedAndRefusesBadInput(t *testing.T) {
	p, o := testPanel(t)
	w := do(t, p, "GET", "/api/tower", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"hasData":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range []struct{ path, body string }{
		{"/api/tower/season", `{"season":99}`}, // the test data has no seasons
		{"/api/tower/swap", `{"floor":1,"wave":0,"blueprint":"M","to":"N"}`},
	} {
		if w := do(t, p, "POST", c.path, c.body, nil); w.Code != 400 && w.Code != 404 {
			t.Fatal(c, w.Code)
		}
	}
	if _, ok := o.TowerSeason(); ok {
		t.Fatal("refused season saved")
	}
}
