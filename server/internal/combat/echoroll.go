// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"tidelab/internal/data"
	"tidelab/internal/save"
)

// Echo main-stat scaling, the sub-stat pool without repeats and the quality-tiered weighted
// sub-stat rolls (specs/inventory.md §1, §6.1, §6.5).

var (
	flatAtkMults   = []int32{813, 1083, 1353, 1623}
	flatAtkWeights = []int32{70, 540, 390, 30}
	flatDefMults   = []int32{813, 1083, 1353, 1623}
	flatDefWeights = []int32{150, 460, 330, 90}
	critMults      = []int32{1263, 1383, 1503, 1623, 1743, 1863, 1983, 2103}
	critWeights    = []int32{70, 70, 70, 24, 24, 24, 9, 9}
	defaultMults   = []int32{853, 953, 1053, 1153, 1253, 1353, 1453, 1553}
	defaultWeights = []int32{70, 80, 210, 250, 180, 150, 60, 30}
)

func IsSeedableEcho(t *data.DataTables, it *data.PhantomItem) bool {
	if it.PhantomType != 1 || it.ParentMonsterId != 0 || !it.ShowInBag || !it.Destructible {
		return false
	}
	if _, ok := t.PhantomRarityByRare[it.Rarity]; !ok {
		return false
	}
	if _, ok := t.PhantomQualityByQuality[it.QualityId]; !ok {
		return false
	}
	return it.MainProp != nil && len(FindValidMainPropGroups(t, it.MainProp.RandGroupId)) > 0
}

func FindValidMainPropGroups(t *data.DataTables, randGroupID int32) []*data.PhantomMainProperty {
	var out []*data.PhantomMainProperty
	for _, r := range t.PhantomMainProperty {
		if r.RandGroupId != randGroupID {
			continue
		}
		for _, id := range r.PropGroup {
			if _, ok := t.PhantomMainPropItemById[id]; ok {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// ComputeMainPropValue is trunc(StandardProperty × growth / 10000) in 64 bits (inventory.md §6.1).
func ComputeMainPropValue(t *data.DataTables, it *data.PhantomMainPropItem, level int32) int32 {
	growth := int32(10000)
	for _, r := range t.PhantomGrowth {
		if r.GrowthId == it.GrowthId && r.Level == level {
			growth = r.Value
			break
		}
	}
	return int32(int64(it.StandardProperty) * int64(growth) / 10000)
}

func BuildMainProps(t *data.DataTables, g *data.PhantomMainProperty, level int32) []save.EchoPropSave {
	out := []save.EchoPropSave{}
	for _, id := range g.PropGroup {
		if cfg, ok := t.PhantomMainPropItemById[id]; ok {
			out = append(out, save.EchoPropSave{ID: cfg.Id, Value: ComputeMainPropValue(t, cfg, level)})
		}
	}
	return out
}

func RecomputeMainProps(t *data.DataTables, e *save.EchoSave) {
	for i, cur := range e.MainProp {
		if cfg, ok := t.PhantomMainPropItemById[cur.ID]; ok {
			e.MainProp[i] = save.EchoPropSave{ID: cur.ID, Value: ComputeMainPropValue(t, cfg, e.Level)}
		}
	}
}

func CreateLevel0Echo(t *data.DataTables, uniqueID int32, it *data.PhantomItem, fetterGroupID int32, randomMainGroup bool, rng Rng) *save.EchoSave {
	if it.MainProp == nil {
		return nil
	}
	groups := FindValidMainPropGroups(t, it.MainProp.RandGroupId)
	if len(groups) == 0 {
		return nil
	}
	chosen := groups[0]
	if randomMainGroup {
		chosen = groups[rng.Next(int32(len(groups)))]
	}
	e := save.NewEcho(uniqueID)
	e.ID = it.ItemId
	e.MainProp = BuildMainProps(t, chosen, 0)
	e.FetterGroupID = fetterGroupID
	return e
}

type subKey struct{ propID, addType int32 }

// SubStatTypeKey is the no-repeat key of a sub-stat: (PropId, AddType) for HP, Atk and Def,
// else (PropId, 0).
func SubStatTypeKey(propID, addType int32) subKey {
	if propID == 10002 || propID == 10007 || propID == 10010 {
		return subKey{propID, addType}
	}
	return subKey{propID, 0}
}

func tierCap(quality int32, low, mid, high int) int {
	switch {
	case quality <= 3:
		return low
	case quality == 4:
		return mid
	}
	return high
}

func GetTierTable(propID, addType, quality int32) (mults, weights []int32, maxIndex int) {
	switch {
	case propID == 10007 && addType == 1:
		return flatAtkMults, flatAtkWeights, min(tierCap(quality, 1, 2, 3), len(flatAtkMults)-1)
	case propID == 10010 && addType == 1:
		return flatDefMults, flatDefWeights, min(tierCap(quality, 1, 2, 3), len(flatDefMults)-1)
	case propID == 8 || propID == 9:
		return critMults, critWeights, min(tierCap(quality, 3, 5, 7), len(critMults)-1)
	}
	return defaultMults, defaultWeights, min(tierCap(quality, 3, 5, 7), len(defaultMults)-1)
}

func RollSubStatValue(cfg *data.PhantomSubProperty, quality int32, rng Rng) int32 {
	mults, weights, maxIdx := GetTierTable(cfg.PropId, cfg.AddType, quality)
	var total int32
	for i := 0; i <= maxIdx; i++ {
		total += weights[i]
	}
	roll := rng.Next(total)
	chosen := maxIdx
	var acc int32
	for i := 0; i <= maxIdx; i++ {
		acc += weights[i]
		if roll < acc {
			chosen = i
			break
		}
	}
	return int32((int64(cfg.SubStandardProperty)*int64(mults[chosen])+5000)/10000) * 10
}

func RollNewSubProp(t *data.DataTables, quality int32, existing []save.EchoPropSave, rng Rng) (save.EchoPropSave, bool) {
	used := map[subKey]bool{}
	for _, sp := range existing {
		if cfg, ok := t.PhantomSubPropertyById[sp.ID]; ok {
			used[SubStatTypeKey(cfg.PropId, cfg.AddType)] = true
		}
	}
	var pool []*data.PhantomSubProperty
	seen := map[subKey]bool{}
	for _, cfg := range t.PhantomSubProperty {
		k := SubStatTypeKey(cfg.PropId, cfg.AddType)
		if !used[k] && !seen[k] {
			seen[k] = true
			pool = append(pool, cfg)
		}
	}
	if len(pool) == 0 {
		return save.EchoPropSave{}, false
	}
	picked := pool[rng.Next(int32(len(pool)))]
	return save.EchoPropSave{ID: picked.Id, Value: RollSubStatValue(picked, quality, rng)}, true
}

func UnlockedSubSlots(t *data.DataTables, quality, level int32) int32 {
	q, ok := t.PhantomQualityByQuality[quality]
	if !ok {
		return 0
	}
	var n int32
	for _, lv := range q.SlotUnlockLevel {
		if level >= lv {
			n++
		}
	}
	return n
}
