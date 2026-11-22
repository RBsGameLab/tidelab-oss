// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/internal/quests"
)

// R-160c: the content panel over the 深塔 (specs/panel.md §6): the season listed and monster
// swaps per floor, wave and kind.

// TowerMonster is a monster a swap can bring: a 深塔 floor prefab's wave entity, the first one
// of its blueprint in floor order.
type TowerMonster struct {
	Blueprint string
	Name      string
	quests.TowerEntity
}

// TowerWaveGroup is one kind of monster in a floor's wave: how many, and the first of them.
type TowerWaveGroup struct {
	Blueprint string
	Name      string
	Count     int
}

var towerMonsterCache sync.Map // *quests.QuestDataSet -> []TowerMonster

var trailingDigits = regexp.MustCompile(`\d+$`)

// TowerMonsterName is a prefab entity's designer name made readable: "TsEntity_精英_磐石护卫2" →
// "精英·磐石护卫".
func TowerMonsterName(name string) string {
	n := strings.TrimPrefix(name, "TsEntity_")
	n = strings.ReplaceAll(n, "_爬塔专用", "")
	n = trailingDigits.ReplaceAllString(n, "")
	return strings.ReplaceAll(n, "_", "·")
}

func floorPrefabDef(qd *quests.QuestDataSet, floor int32) (quests.PrefabDef, bool) {
	for _, f := range qd.TowerFloors {
		if f.Floor == floor {
			def, ok := qd.Prefabs[f.Prefab]
			return def, ok && len(def.Waves) > 0
		}
	}
	return quests.PrefabDef{}, false
}

// TowerMonsters are every monster of every 深塔 floor's waves, one per blueprint, by name.
func TowerMonsters(qd *quests.QuestDataSet) []TowerMonster {
	if qd == nil {
		return nil
	}
	if c, ok := towerMonsterCache.Load(qd); ok {
		return c.([]TowerMonster)
	}
	seen := map[string]bool{}
	var out []TowerMonster
	for _, f := range qd.TowerFloors {
		def, ok := qd.Prefabs[f.Prefab]
		if !ok {
			continue
		}
		for _, wave := range def.Waves {
			for _, id := range wave {
				e, ok := quests.PrefabEntityIn(def, id)
				if !ok || seen[e.BlueprintType] {
					continue
				}
				seen[e.BlueprintType] = true
				out = append(out, TowerMonster{Blueprint: e.BlueprintType, Name: TowerMonsterName(e.Name),
					TowerEntity: quests.TowerEntity{PrefabID: def.ID, EntityID: e.ID, PropertyID: e.PropertyID}})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	towerMonsterCache.Store(qd, out)
	return out
}

// TowerMonsterOf is the swap candidate that is entity (prefab, entity) (false: not one).
func TowerMonsterOf(qd *quests.QuestDataSet, s panel.TowerSwap) (TowerMonster, bool) {
	for _, m := range TowerMonsters(qd) {
		if m.PrefabID == s.Prefab && m.EntityID == s.Entity {
			return m, true
		}
	}
	return TowerMonster{}, false
}

// TowerFloorWaves are a floor's waves as kinds of monsters, in the order they first appear
// (nil: the floor has no prefab of its own).
func TowerFloorWaves(qd *quests.QuestDataSet, floor int32) [][]TowerWaveGroup {
	def, ok := floorPrefabDef(qd, floor)
	if !ok {
		return nil
	}
	out := make([][]TowerWaveGroup, len(def.Waves))
	for w, wave := range def.Waves {
		at := map[string]int{}
		for _, id := range wave {
			e, ok := quests.PrefabEntityIn(def, id)
			if !ok {
				continue
			}
			if k, ok := at[e.BlueprintType]; ok {
				out[w][k].Count++
				continue
			}
			at[e.BlueprintType] = len(out[w])
			out[w] = append(out[w], TowerWaveGroup{Blueprint: e.BlueprintType, Name: TowerMonsterName(e.Name), Count: 1})
		}
	}
	return out
}

// towerSwapOf is the monster that replaces blueprint in floor's wave (false: none, or the saved
// swap is not a candidate any more).
func towerSwapOf(o *panel.Overrides, qd *quests.QuestDataSet, floor int32, wave int, blueprint string) (TowerMonster, bool) {
	s, ok := o.TowerSwap(floor, wave, blueprint)
	if !ok {
		return TowerMonster{}, false
	}
	return TowerMonsterOf(qd, s)
}

// TowerSeasons are the 深境区 seasons with floors, newest first, with their floor counts.
func TowerSeasons(tb *data.DataTables) (seasons []int32, floors map[int32]int) {
	floors = map[int32]int{}
	for _, f := range tb.TowerConfig {
		if f.Difficulty == 3 {
			if floors[f.Season] == 0 {
				seasons = append(seasons, f.Season)
			}
			floors[f.Season]++
		}
	}
	sort.Slice(seasons, func(i, j int) bool { return seasons[i] > seasons[j] })
	return seasons, floors
}
