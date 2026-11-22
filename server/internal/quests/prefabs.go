// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"slices"
	"sort"

	"tidelab/internal/common"
)

// PrefabEntity is one entity of a prefab (Prefabs.json): its id inside the prefab, blueprint,
// offset from the position entity, and rotation.
type PrefabEntity struct {
	ID            int32
	BlueprintType string
	Name          string
	Pos           [3]float32
	Rot           [3]float32
	// Tower marks the game's 深塔 version of this blueprint (R-88).
	Tower bool
	// PropertyID is a Tower entity's AttributeComponent.PropertyId (R-126); 0 = template's.
	PropertyID int32
}

// PrefabDef is a PrefabConfig group (Prefabs.json); Waves are a 深塔 floor prefab's spawner waves (R-126).
type PrefabDef struct {
	ID       int32
	Entities []PrefabEntity
	Waves    [][]int32
}

// FloorPrefab is a TowerFloors.json entry: the game's own prefab of a 深塔 floor and its level play (R-126).
type FloorPrefab struct {
	Floor  int32
	Prefab int32
	Play   int32
}

// TowerEntity locates the 深塔 version of a monster (R-88): a prefab and entity id inside it.
type TowerEntity struct {
	PrefabID   int32
	EntityID   int32
	PropertyID int32
}

// PrefabGroup is a group a level play refers to by variable name (CreatePrefab / MonsterCreator).
type PrefabGroup struct {
	Run       int64
	VarName   string
	PrefabID  int32
	Inc       int64
	PosEntity int64
	// Alive holds net ids of spawned monsters still alive.
	Alive       []int64
	Spawned     bool
	Waves       [][]int32
	EntityWaves bool
	Wave        int
}

// PendingPrefab is a prefab group waiting to be spawned at a position entity.
type PendingPrefab struct {
	Group     *PrefabGroup
	PosEntity int64
}

// PrefabGroups tracks prefabs created with CreatePrefab and MonsterCreator steps waiting on them
// (R-4c, R-56, R-88, R-126). Ported from PrefabGroups.cs.
type PrefabGroups struct {
	prefabs     map[int32]PrefabDef
	prefabOrder []int32
	floors      []FloorPrefab
	groups      []*PrefabGroup
	pending     []PendingPrefab
	nextInc     int64
}

func NewPrefabGroups(prefabs map[int32]PrefabDef, prefabOrder []int32, floors []FloorPrefab) *PrefabGroups {
	if prefabs == nil {
		prefabs = map[int32]PrefabDef{}
	}
	order := prefabOrder
	if len(order) == 0 && len(prefabs) > 0 {
		order = make([]int32, 0, len(prefabs))
		for id := range prefabs {
			order = append(order, id)
		}
		sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	}
	return &PrefabGroups{
		prefabs:     prefabs,
		prefabOrder: order,
		floors:      floors,
	}
}

func (p *PrefabGroups) Groups() []*PrefabGroup { return p.groups }
func (p *PrefabGroups) Queue() []PendingPrefab { return p.pending }

// TakeQueue drains and returns the queued spawns (for the world refresh).
func (p *PrefabGroups) TakeQueue() []PendingPrefab {
	q := make([]PendingPrefab, len(p.pending))
	copy(q, p.pending)
	p.pending = p.pending[:0]
	return q
}

func (p *PrefabGroups) Get(prefabID int32) (PrefabDef, bool) {
	def, ok := p.prefabs[prefabID]
	return def, ok
}

// FloorPlay returns the dungeon level play of 深塔 floor when present (R-127).
func (p *PrefabGroups) FloorPlay(floor int32) (int32, bool) {
	for _, f := range p.floors {
		if f.Floor == floor {
			if f.Play != 0 {
				return f.Play, true
			}
			return 0, false
		}
	}
	return 0, false
}

// FloorPrefabOf returns the game's own prefab of 深塔 floor if it has spawner waves (R-126).
func (p *PrefabGroups) FloorPrefabOf(floor int32) (int32, bool) {
	for _, f := range p.floors {
		if f.Floor == floor {
			if def, ok := p.prefabs[f.Prefab]; ok && len(def.Waves) > 0 {
				return f.Prefab, true
			}
			return 0, false
		}
	}
	return 0, false
}

// PrefabEntityIn finds an entity in a prefab by its local id (R-126).
func PrefabEntityIn(def PrefabDef, id int32) (PrefabEntity, bool) {
	for _, e := range def.Entities {
		if e.ID == id {
			return e, true
		}
	}
	return PrefabEntity{}, false
}

// TowerEntityOf finds the tower prefab entity for blueprint (R-88).
func (p *PrefabGroups) TowerEntityOf(blueprint string) (TowerEntity, bool) {
	for _, id := range p.prefabOrder {
		if def, ok := p.prefabs[id]; ok {
			if t, found := TowerEntityIn(def, blueprint); found {
				return t, true
			}
		}
	}
	return TowerEntity{}, false
}

func TowerEntityIn(def PrefabDef, blueprint string) (TowerEntity, bool) {
	for _, e := range def.Entities {
		if e.Tower && e.BlueprintType == blueprint {
			return TowerEntity{PrefabID: def.ID, EntityID: e.ID, PropertyID: e.PropertyID}, true
		}
	}
	return TowerEntity{}, false
}

// find returns the newest group of that name (a level play may re-create it per wave).
func (p *PrefabGroups) find(run int64, varName string) *PrefabGroup {
	for i := len(p.groups) - 1; i >= 0; i-- {
		if p.groups[i].Run == run && p.groups[i].VarName == varName {
			return p.groups[i]
		}
	}
	return nil
}

// Create queues a prefab for spawning at posEntity (CreatePrefab).
func (p *PrefabGroups) Create(run int64, varName string, prefabID int32, posEntity int64) *PrefabGroup {
	p.nextInc++
	g := &PrefabGroup{
		Run:       run,
		VarName:   varName,
		PrefabID:  prefabID,
		Inc:       p.nextInc,
		PosEntity: posEntity,
	}
	p.groups = append(p.groups, g)
	p.pending = append(p.pending, PendingPrefab{Group: g, PosEntity: posEntity})
	common.Info("prefab", fmt.Sprintf("level play %x: prefab %d as %q queued at entity %d", run, prefabID, varName, posEntity))
	return g
}

// CreateWaves creates a group that spawns waves (TemplateConfig ids) wave by wave at the prefab's spots (R-56).
func (p *PrefabGroups) CreateWaves(run int64, varName string, prefabID int32, posEntity int64, waves [][]int32) *PrefabGroup {
	g := p.Create(run, varName, prefabID, posEntity)
	copied := make([][]int32, len(waves))
	for i, w := range waves {
		c := make([]int32, len(w))
		copy(c, w)
		copied[i] = c
	}
	g.Waves = copied
	common.Info("prefab", fmt.Sprintf("level play %x: %q as %d waves", run, varName, len(waves)))
	return g
}

// CreateFloor creates a 深塔 floor's own prefab group spawning wave by wave by entity ids (R-126).
func (p *PrefabGroups) CreateFloor(run int64, varName string, prefabID int32, posEntity int64) *PrefabGroup {
	def, ok := p.prefabs[prefabID]
	if !ok {
		return nil
	}
	g := p.Create(run, varName, prefabID, posEntity)
	g.Waves = def.Waves
	g.EntityWaves = true
	common.Info("prefab", fmt.Sprintf("level play %x: %q is the floor's own prefab %d: %d waves", run, varName, prefabID, len(def.Waves)))
	return g
}

// Has reports whether run created a group named varName (R-60).
func (p *PrefabGroups) Has(run int64, varName string) bool {
	return p.find(run, varName) != nil
}

// GroupCleared returns (cleared, known): ok=false while never created or not spawned yet.
func (p *PrefabGroups) GroupCleared(run int64, varName string) (bool, bool) {
	g := p.find(run, varName)
	if g == nil || !g.Spawned {
		return false, false
	}
	return len(g.Alive) == 0, true
}

// OnKilled records a monster death. Returns (run, varName, true) if that death emptied a group's
// final wave. Searches newest group first (R-127).
func (p *PrefabGroups) OnKilled(netID int64) (int64, string, bool) {
	for gi := len(p.groups) - 1; gi >= 0; gi-- {
		g := p.groups[gi]
		k := slices.Index(g.Alive, netID)
		if k < 0 {
			continue
		}
		last := len(g.Alive) - 1
		g.Alive[k] = g.Alive[last]
		g.Alive = g.Alive[:last]
		common.Info("prefab", fmt.Sprintf("level play %x: %q %d left", g.Run, g.VarName, len(g.Alive)))
		if len(g.Alive) > 0 {
			return 0, "", false
		}
		if g.Wave+1 < len(g.Waves) {
			// Not spawned until the refresh runs, so GroupCleared says "not yet" meanwhile.
			g.Wave++
			g.Spawned = false
			p.pending = append(p.pending, PendingPrefab{Group: g, PosEntity: g.PosEntity})
			common.Info("prefab", fmt.Sprintf("level play %x: %q wave %d/%d queued", g.Run, g.VarName, g.Wave+1, len(g.Waves)))
			return 0, "", false
		}
		return g.Run, g.VarName, true
	}
	return 0, "", false
}

func (p *PrefabGroups) IsGroupMonster(netID int64) bool {
	for _, g := range p.groups {
		if slices.Contains(g.Alive, netID) {
			return true
		}
	}
	return false
}

func (p *PrefabGroups) AliveNetIDs() []int64 {
	var out []int64
	for _, g := range p.groups {
		out = append(out, g.Alive...)
	}
	return out
}

// DropRun forgets every group of run and returns the net ids still alive.
func (p *PrefabGroups) DropRun(run int64) []int64 {
	var left []int64
	for i := 0; i < len(p.groups); {
		if p.groups[i].Run != run {
			i++
			continue
		}
		g := p.groups[i]
		left = append(left, g.Alive...)
		p.groups = append(p.groups[:i], p.groups[i+1:]...)
		// Remove all pending entries for g while preserving order.
		w := 0
		for _, pend := range p.pending {
			if pend.Group != g {
				p.pending[w] = pend
				w++
			}
		}
		p.pending = p.pending[:w]
	}
	return left
}
