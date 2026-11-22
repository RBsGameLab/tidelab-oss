// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"math"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
)

// StateSetters is R-80a: who can put an entity into the state a quest step waits for, from
// assets/quest/StateSetters.json (only the robot reads this table).
type StateSetters struct {
	entries  []SetterEntry
	byEntity map[setterKey][]int
	Counters SetterStats
}

type setterKey struct {
	mapID int32
	id    int64
}

type SetterNeed struct {
	ID    int64
	State string
}

type SetterWay struct {
	Kind   string
	ID     int64
	Option int32
	State  string
	Need   []SetterNeed
}

type SetterEntry struct {
	Map   int32
	ID    int64
	State string
	Ways  []SetterWay
}

// SetterStats count how the robot drove each state.
type SetterStats struct{ Interact, Chain, Group, Client, None int }

func NewStateSetters(entries []SetterEntry) *StateSetters {
	s := &StateSetters{entries: entries, byEntity: map[setterKey][]int{}}
	for i, e := range entries {
		k := setterKey{e.Map, e.ID}
		s.byEntity[k] = append(s.byEntity[k], i)
	}
	return s
}

func (s *StateSetters) Count() int { return len(s.entries) }

func LoadStateSetters(path string) *StateSetters {
	if !common.FileExists(path) {
		return NewStateSetters(nil)
	}
	v, err := quests.ParseJSONFile(path)
	if err != nil {
		common.Error("state_setters", fmt.Sprintf("loading %s failed: %v", path, err))
		return NewStateSetters(nil)
	}
	return ParseStateSetters(v)
}

func ParseStateSetters(root data.Value) *StateSetters {
	var entries []SetterEntry
	for _, item := range quests.JSONArr(root) {
		o, ok := item.(*data.Object)
		if !ok {
			continue
		}
		var ways []SetterWay
		for _, wv := range quests.JSONArr(o.Get("Ways")) {
			w, ok := wv.(*data.Object)
			if !ok {
				continue
			}
			var needs []SetterNeed
			for _, nv := range quests.JSONArr(w.Get("Need")) {
				if n, ok := nv.(*data.Object); ok {
					needs = append(needs, SetterNeed{quests.JSONLong(n.Get("Id"), 0), quests.JSONStr(n.Get("State"), "")})
				}
			}
			ways = append(ways, SetterWay{quests.JSONStr(w.Get("Kind"), ""), quests.JSONLong(w.Get("Id"), 0), quests.JSONInt(w.Get("Option"), 0),
				quests.JSONStr(w.Get("State"), ""), needs})
		}
		entries = append(entries, SetterEntry{quests.JSONInt(o.Get("Map"), 0), quests.JSONLong(o.Get("Id"), 0), quests.JSONStr(o.Get("State"), ""), ways})
	}
	return NewStateSetters(entries)
}

// WaysFor are the ways to put entity id of map into state.
func (s *StateSetters) WaysFor(mapID int32, id int64, state string) []SetterWay {
	for _, i := range s.byEntity[setterKey{mapID, id}] {
		if s.entries[i].State == state {
			return s.entries[i].Ways
		}
	}
	return nil
}

// RangeTable is R-76h / R-83: range entities from assets/quest/Ranges.json (where to walk for
// CheckInRange).
type RangeTable struct{ byID map[int64]*RangeEntry }

type RangeEntry struct {
	ID     int64
	Map    int32
	Kind   byte
	Center world.Vec3
	Size   world.Vec3
}

func absf(x float32) float32 { return float32(math.Abs(float64(x))) }

func (r *RangeEntry) Contains(p world.Vec3) bool {
	dx, dy, dz := p.X-r.Center.X, p.Y-r.Center.Y, p.Z-r.Center.Z
	switch r.Kind {
	case 0:
		return dx*dx+dy*dy+dz*dz <= r.Size.X*r.Size.X
	case 2:
		return dx*dx+dy*dy <= r.Size.X*r.Size.X && absf(dz) <= r.Size.Y
	}
	return absf(dx) <= r.Size.X && absf(dy) <= r.Size.Y && (r.Size.Z == 0 || absf(dz) <= r.Size.Z)
}

func NewRangeTable(entries []*RangeEntry) *RangeTable {
	t := &RangeTable{byID: map[int64]*RangeEntry{}}
	for _, e := range entries {
		t.byID[e.ID] = e
	}
	return t
}

func (t *RangeTable) Count() int { return len(t.byID) }

func LoadRangeTable(path string) *RangeTable {
	if !common.FileExists(path) {
		return NewRangeTable(nil)
	}
	v, err := quests.ParseJSONFile(path)
	if err != nil {
		common.Error("ranges", fmt.Sprintf("loading %s failed: %v", path, err))
		return NewRangeTable(nil)
	}
	return ParseRangeTable(v)
}

func vec3Of(f []float32) world.Vec3 {
	var v world.Vec3
	if len(f) > 0 {
		v.X = f[0]
	}
	if len(f) > 1 {
		v.Y = f[1]
	}
	if len(f) > 2 {
		v.Z = f[2]
	}
	return v
}

func ParseRangeTable(root data.Value) *RangeTable {
	var entries []*RangeEntry
	for _, item := range quests.JSONArr(root) {
		o, ok := item.(*data.Object)
		if !ok {
			continue
		}
		entries = append(entries, &RangeEntry{
			ID: quests.JSONLong(o.Get("Id"), 0), Map: quests.JSONInt(o.Get("Map"), 0), Kind: byte(quests.JSONInt(o.Get("Kind"), 0)),
			Center: vec3Of(quests.JSONFloats(o.Get("C"))), Size: vec3Of(quests.JSONFloats(o.Get("P"))),
		})
	}
	return NewRangeTable(entries)
}

func (t *RangeTable) Find(id int64) *RangeEntry { return t.byID[id] }

// InAny is whether pos is in one of the ranges ids on mapID; known false when none of them is known.
func (t *RangeTable) InAny(mapID int32, ids []int64, pos world.Vec3) (in, known bool) {
	for _, id := range ids {
		r, ok := t.byID[id]
		if !ok {
			continue
		}
		known = true
		if r.Map == mapID && r.Contains(pos) {
			return true, true
		}
	}
	return false, known
}
