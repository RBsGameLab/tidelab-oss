// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/panel"
)

// R-160c: the 深塔 page (specs/panel.md §6).

var difficultyNames = map[int32]string{1: "稳定区", 2: "实验区", 3: "深境区", 4: "超载区"}

type towerGroupJSON struct {
	Blueprint string `json:"blueprint"`
	Name      string `json:"name"`
	Count     int    `json:"count"`
	Swap      string `json:"swap,omitempty"` // the replacing monster's blueprint
	SwapName  string `json:"swapName,omitempty"`
}

type towerFloorJSON struct {
	ID         int32              `json:"id"`
	Difficulty string             `json:"difficulty"`
	Area       int32              `json:"area"`
	Floor      int32              `json:"floor"`
	Waves      [][]towerGroupJSON `json:"waves"`
}

type towerMonsterJSON struct {
	Blueprint string `json:"blueprint"`
	Name      string `json:"name"`
}

type seasonJSON struct {
	ID     int32 `json:"id"`
	Floors int   `json:"floors"`
}

// difficultyOrder puts 深境区 (the seasonal one) first.
func difficultyOrder(d int32) int32 {
	if d == 3 {
		return 0
	}
	return d
}

// towerFloors are the floors the tower lists now: 深境区 of the season listed, then the fixed
// difficulties.
func (p *Panel) towerFloors(pick int32) []*data.TowerConfig {
	t := p.Data.Tables
	var out []*data.TowerConfig
	for _, f := range t.TowerConfig {
		if game.TowerListed(t, f, pick) {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Difficulty != b.Difficulty {
			return difficultyOrder(a.Difficulty) < difficultyOrder(b.Difficulty)
		}
		if a.AreaNum != b.AreaNum {
			return a.AreaNum < b.AreaNum
		}
		return a.Floor < b.Floor
	})
	return out
}

func (p *Panel) towerList(w http.ResponseWriter, _ *http.Request) {
	t := p.Data.Tables
	pick, picked := p.Data.Panel.TowerSeason()
	seasons, counts := game.TowerSeasons(t)
	out := struct {
		HasData  bool               `json:"hasData"`
		Season   int32              `json:"season"`
		Picked   bool               `json:"picked"`
		Newest   int32              `json:"newest"`
		Seasons  []seasonJSON       `json:"seasons"`
		Floors   []towerFloorJSON   `json:"floors"`
		Monsters []towerMonsterJSON `json:"monsters"`
		Changes  []string           `json:"changes"`
	}{HasData: p.Quests != nil, Season: game.TowerCurrentSeason(t, pick), Picked: picked, Newest: game.TowerNewestSeason(t),
		Seasons: []seasonJSON{}, Floors: []towerFloorJSON{}, Monsters: []towerMonsterJSON{}, Changes: p.Data.Panel.Changes(30)}
	for _, s := range seasons {
		out.Seasons = append(out.Seasons, seasonJSON{s, counts[s]})
	}
	for _, m := range game.TowerMonsters(p.Quests) {
		out.Monsters = append(out.Monsters, towerMonsterJSON{m.Blueprint, m.Name})
	}
	if p.Quests != nil {
		for _, f := range p.towerFloors(pick) {
			j := towerFloorJSON{ID: f.Id, Difficulty: difficultyNames[f.Difficulty], Area: f.AreaNum, Floor: f.Floor, Waves: [][]towerGroupJSON{}}
			for wi, wave := range game.TowerFloorWaves(p.Quests, f.Id) {
				groups := []towerGroupJSON{}
				for _, g := range wave {
					gj := towerGroupJSON{Blueprint: g.Blueprint, Name: g.Name, Count: g.Count}
					if s, ok := p.Data.Panel.TowerSwap(f.Id, wi, g.Blueprint); ok {
						if m, ok := game.TowerMonsterOf(p.Quests, s); ok {
							gj.Swap, gj.SwapName = m.Blueprint, m.Name
						}
					}
					groups = append(groups, gj)
				}
				j.Waves = append(j.Waves, groups)
			}
			out.Floors = append(out.Floors, j)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Panel) towerSeason(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Season int32 `json:"season"`
	}
	seasons, _ := game.TowerSeasons(p.Data.Tables)
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || !slices.Contains(seasons, in.Season) {
		writeError(w, http.StatusBadRequest, "没有这个赛季")
		return
	}
	note := fmt.Sprintf("深塔：深境区换成第 %d 期", in.Season)
	if err := p.Data.Panel.SetTowerSeason(in.Season, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) towerSeasonReset(w http.ResponseWriter, _ *http.Request) {
	note := "深塔：深境区恢复默认（最新一期）"
	if err := p.Data.Panel.ResetTowerSeason(note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// towerSwap sets (To = a monster's blueprint) or drops (To = "") the swap of a kind in a wave.
func (p *Panel) towerSwap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Floor     int32  `json:"floor"`
		Wave      int    `json:"wave"`
		Blueprint string `json:"blueprint"`
		To        string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil || p.Quests == nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	waves := game.TowerFloorWaves(p.Quests, in.Floor)
	if in.Wave < 0 || in.Wave >= len(waves) || !slices.ContainsFunc(waves[in.Wave], func(g game.TowerWaveGroup) bool { return g.Blueprint == in.Blueprint }) {
		writeError(w, http.StatusNotFound, "这一层的这一波没有这种怪")
		return
	}
	where := fmt.Sprintf("深塔 %d 层第 %d 波「%s」", in.Floor, in.Wave+1, nameIn(waves[in.Wave], in.Blueprint))
	if in.To == "" || in.To == in.Blueprint {
		note := where + "：恢复默认"
		if err := p.Data.Panel.ResetTowerSwap(in.Floor, in.Wave, in.Blueprint, note); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		common.Info("panel", note)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	i := slices.IndexFunc(game.TowerMonsters(p.Quests), func(m game.TowerMonster) bool { return m.Blueprint == in.To })
	if i < 0 {
		writeError(w, http.StatusBadRequest, "不是深塔里出现过的怪")
		return
	}
	m := game.TowerMonsters(p.Quests)[i]
	note := where + "：换成「" + m.Name + "」"
	if err := p.Data.Panel.SetTowerSwap(in.Floor, in.Wave, in.Blueprint, panel.TowerSwap{Prefab: m.PrefabID, Entity: m.EntityID}, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func nameIn(groups []game.TowerWaveGroup, blueprint string) string {
	for _, g := range groups {
		if g.Blueprint == blueprint {
			return g.Name
		}
	}
	return blueprint
}
