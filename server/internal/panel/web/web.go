// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package web is R-160's content panel page: a local web page served by gamesv that shows the
// activity plan and writes the panel's overrides (specs/panel.md §3).
package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/game"
	"tidelab/internal/panel"
	"tidelab/internal/quests"
	"tidelab/internal/world"
)

//go:embed index.html
var indexHTML []byte

// Names are the optional zh-Hans display names for activities, shop items and limited banners
// (tools/export_panel_names.py). Loaded at run time so the server builds without game-derived files.
type Names struct {
	Activity map[int32]string
	Item     map[int32]string
	Gacha    map[int32]string
}

// LoadNames reads activity_names.tsv, item_names.tsv and gacha_names.tsv from the first directory
// in dirs that holds each file. Missing files leave that map empty.
func LoadNames(dirs ...string) Names {
	return Names{
		Activity: loadNamesFile(dirs, "activity_names.tsv"),
		Item:     loadNamesFile(dirs, "item_names.tsv"),
		Gacha:    loadNamesFile(dirs, "gacha_names.tsv"),
	}
}

func loadNamesFile(dirs []string, name string) map[int32]string {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d, name))
		if err == nil {
			return parseNames(string(raw))
		}
	}
	return map[int32]string{}
}

func parseNames(tsv string) map[int32]string {
	m := map[int32]string{}
	for _, line := range strings.Split(tsv, "\n") {
		id, name, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if n, err := strconv.ParseInt(id, 10, 32); ok && err == nil {
			m[int32(n)] = name
		}
	}
	return m
}

// Panel serves the page for one world.
type Panel struct {
	Data *world.WorldData
	// Quests is the quest data set (the 深塔 floor prefabs; nil: the 深塔 page has no floors).
	Quests *quests.QuestDataSet
	// Names holds the optional display names (zero value is valid: ids are shown without names).
	Names Names
	// Now is the game clock in seconds.
	Now func() int64
}

// Handler is the page and its API.
func (p *Panel) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/activities", p.list)
	mux.HandleFunc("POST /api/activities/{key}", p.set)
	mux.HandleFunc("POST /api/activities/{key}/reset", p.reset)
	mux.HandleFunc("GET /api/shop", p.shopList)
	mux.HandleFunc("POST /api/shop/{id}", p.shopSet)
	mux.HandleFunc("POST /api/shop/{id}/reset", p.shopReset)
	mux.HandleFunc("GET /api/gacha", p.gachaList)
	mux.HandleFunc("POST /api/gacha/{slot}", p.gachaSet)
	mux.HandleFunc("POST /api/gacha/{slot}/reset", p.gachaReset)
	mux.HandleFunc("GET /api/tower", p.towerList)
	mux.HandleFunc("POST /api/tower/season", p.towerSeason)
	mux.HandleFunc("POST /api/tower/season/reset", p.towerSeasonReset)
	mux.HandleFunc("POST /api/tower/swap", p.towerSwap)
	return localOnly(mux)
}

// localOnly refuses requests that are not from this machine, or that come from a page of
// another site (a browser sends its Origin on a POST).
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			http.Error(w, "local only", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "other origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve listens on addr and serves the page until the listener fails; the URL is logged.
func (p *Panel) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	common.Info("panel", "content panel at http://"+ln.Addr().String()+"/")
	srv := &http.Server{Handler: p.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			common.Warn("panel", "content panel stopped: "+err.Error())
		}
	}()
	return nil
}

type editionJSON struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

type slotJSON struct {
	Key        string        `json:"key"`
	Type       int32         `json:"type"`
	SubType    int32         `json:"subType"`
	Supported  bool          `json:"supported"`
	Editions   []editionJSON `json:"editions"`
	Auto       int32         `json:"auto"`
	Edition    int32         `json:"edition"`
	Overridden bool          `json:"overridden"`
	Override   *overrideJSON `json:"override,omitempty"`
	Begin      int64         `json:"begin"`
	End        int64         `json:"end"`
	AutoBegin  int64         `json:"autoBegin"`
	AutoEnd    int64         `json:"autoEnd"`
	Explicit   bool          `json:"explicit"`
	Open       bool          `json:"open"`
}

type overrideJSON struct {
	Off     bool  `json:"off"`
	Edition int32 `json:"edition"`
	Begin   int64 `json:"begin"`
	End     int64 `json:"end"`
}

func (p *Panel) list(w http.ResponseWriter, _ *http.Request) {
	now := p.Now()
	plan := game.PlanActivities(p.Data, now)
	out := struct {
		Now     int64      `json:"now"`
		Slots   []slotJSON `json:"slots"`
		Changes []string   `json:"changes"`
	}{Now: now, Slots: []slotJSON{}, Changes: p.Data.Panel.Changes(30)}
	for _, s := range plan.Slots {
		j := slotJSON{Key: s.Key, Type: s.Type, SubType: s.SubType, Supported: s.Supported, Auto: s.Auto, Edition: s.Edition,
			Overridden: s.Overridden, Begin: s.Begin, End: s.End, AutoBegin: s.AutoBegin, AutoEnd: s.AutoEnd, Explicit: s.ExplicitWindow, Open: s.Open}
		for _, id := range s.Editions {
			j.Editions = append(j.Editions, editionJSON{id, p.Names.Activity[id]})
		}
		if s.Overridden {
			j.Override = &overrideJSON{s.Override.Off, s.Override.Edition, s.Override.Begin, s.Override.End}
		}
		out.Slots = append(out.Slots, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Panel) slot(w http.ResponseWriter, r *http.Request) *game.ActivitySlot {
	s := game.PlanActivities(p.Data, p.Now()).Slot(r.PathValue("key"))
	if s == nil || !s.Supported {
		writeError(w, http.StatusNotFound, "这个活动类型服务端不支持")
		return nil
	}
	return s
}

func (p *Panel) set(w http.ResponseWriter, r *http.Request) {
	s := p.slot(w, r)
	if s == nil {
		return
	}
	var in overrideJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	a := panel.Activity{Off: in.Off, Edition: in.Edition, Begin: in.Begin, End: in.End}
	if a.Edition != 0 && !slices.Contains(s.Editions, a.Edition) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%d 不是这个类型的活动", a.Edition))
		return
	}
	if !panel.ValidWindow(a) {
		writeError(w, http.StatusBadRequest, "开始时间要早于结束时间")
		return
	}
	if err := p.Data.Panel.SetActivity(s.Key, a, p.describe(s, a)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", p.describe(s, a))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) reset(w http.ResponseWriter, r *http.Request) {
	s := p.slot(w, r)
	if s == nil {
		return
	}
	note := slotName(s) + "：恢复默认"
	if err := p.Data.Panel.ResetActivity(s.Key, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func slotName(s *game.ActivitySlot) string {
	if s.SubType != 0 {
		return fmt.Sprintf("类型 %d.%d", s.Type, s.SubType)
	}
	return fmt.Sprintf("类型 %d", s.Type)
}

// describe is the change log line of an override.
func (p *Panel) describe(s *game.ActivitySlot, a panel.Activity) string {
	if a.Off {
		return slotName(s) + "：关闭"
	}
	ed := "自动选期"
	if a.Edition != 0 {
		ed = fmt.Sprintf("%d「%s」", a.Edition, p.Names.Activity[a.Edition])
	}
	win := "自动时间"
	if a.Window() {
		win = FormatTime(a.Begin) + " 至 " + FormatTime(a.End)
	}
	return slotName(s) + "：开启，" + ed + "，" + win
}

// FormatTime is a time in UTC+8 as the panel shows it.
func FormatTime(sec int64) string {
	return time.Unix(sec, 0).In(panel.UTC8).Format("2006-01-02 15:04")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
