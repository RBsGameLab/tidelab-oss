// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"cmp"
	"math"
	"slices"

	"tidelab/internal/data"
	"tidelab/internal/save"
)

// WorldSpawn is a world spawn alive in the scene: its entity, its candidate and the class it counts as.
type WorldSpawn struct {
	NetID     int64
	Candidate *Candidate
	Class     SpawnClass
	Woken     bool
}

// WorldInstances are the open-world instances that have world spawns (specs/spawning.md §1).
var WorldInstances = map[int32]bool{8: true, 100: true, 900: true, 902: true, 903: true, 905: true, 906: true, 909: true, 910: true, 912: true}

const (
	RefreshStep = 800
	RespawnMs   = 180_000
	RetryMs     = 3_600_000
	NearestNpcs = 60
)

func HasWorldSpawns(instanceID int32) bool { return WorldInstances[instanceID] }

// PlayerWorld is the player's saved world facts the spawner reads.
type PlayerWorld struct {
	State  *save.WorldState
	Moves  *save.EntityMoves
	Quests save.QuestView
	NetIds *WorldNetIds
	// Areas are the area states quests set (sent at every scene join, R-82c).
	Areas      *save.AreaStates
	WorldLevel int32
}

func LoadPlayerWorld(files *save.PlayerFiles, worldLevel int32) *PlayerWorld {
	return &PlayerWorld{
		State:      save.NewWorldState(files),
		Moves:      save.NewEntityMoves(files),
		Quests:     save.NewQuestProgress(files),
		NetIds:     NewWorldNetIds(files),
		Areas:      save.NewAreaStates(files),
		WorldLevel: worldLevel,
	}
}

// Spawner is the world spawner of one scene (specs/spawning.md §4): it decides which
// candidates of the map the client is shown, adds them as the player comes near and removes
// them as the player leaves.
type Spawner struct {
	data       *WorldData
	scene      *Scene
	world      *PlayerWorld
	context    *LevelContext
	added      func(*Entity)
	removed    func([]int64)
	candidates []*Candidate
	spawns     []*WorldSpawn
	spawnedIDs map[int32]bool
	notBefore  map[int32]int64
	start      *SessionStart

	// Beacons are the awake beacon locations, for the beacon heal (specs/movement.md §2).
	Beacons []Vec3
	// LastRefreshAt is where the last refresh ran, nil before the first.
	LastRefreshAt *Vec3
	// Marked: something woke entities since the last refresh.
	Marked bool

	// Spawned is called for each spawned entity before its add goes out (combat registers AI entities).
	Spawned func(*Entity, *Candidate)
	// AfterSpawn is called for each spawned entity after its add went out (the quests' options).
	AfterSpawn func(*Entity)
	// Despawned is called for each world spawn the spawner removes.
	Despawned func(netID int64)
	// RowFor is the row a woken candidate spawns from (R-127); nil: the candidate's.
	RowFor func(*Candidate) *data.LevelEntityConfig
	// BeforeCandidates runs near the start of every refresh: the queued prefab spawns.
	BeforeCandidates func()
	// MoreNetIDs are more net ids for world_net_ids: the live prefab-group monsters.
	MoreNetIDs func() []int64
	// Hidden (R-69): whether a quest clearing hides a row (config id, awake); quests.md §5.5.
	Hidden func(configID int64, awake bool) bool
	// InFight: an entity in a fight with the formation (combat.md §9.2) does not leave.
	InFight func(netID int64) bool
}

// NewSpawner: candidates nil means the map's without quest rules.
func NewSpawner(d *WorldData, scene *Scene, world *PlayerWorld, start SessionStart, added func(*Entity), removed func([]int64), candidates []*Candidate) *Spawner {
	s := &Spawner{
		data: d, scene: scene, world: world, added: added, removed: removed,
		spawnedIDs: map[int32]bool{}, notBefore: map[int32]int64{}, start: &start,
	}
	s.context = &LevelContext{
		WorldLevel: world.WorldLevel,
		SavedState: func(id int32) (int32, bool) { return world.State.StateOverride(int64(id)) },
		Moved: func(id int32) (MovedPlace, bool) {
			p, ok := world.Moves.Get(int64(id))
			if !ok {
				return MovedPlace{}, false
			}
			return MovedPlace{Vec3{p.X, p.Y, p.Z}, p.Yaw}, true
		},
	}
	if candidates == nil {
		candidates = d.CandidatesOf(scene.InstanceID)
	}
	s.candidates = candidates
	for _, c := range candidates {
		if c.Class == ClassBeacon && !c.Sleeping {
			s.Beacons = append(s.Beacons, c.Location)
		}
	}
	return s
}

func (s *Spawner) hidden(id int64, awake bool) bool { return s.Hidden != nil && s.Hidden(id, awake) }
func (s *Spawner) inFight(id int64) bool            { return s.InFight != nil && s.InFight(id) }

// SaveNetIDs saves the world spawn ids with the live prefab monsters.
func (s *Spawner) SaveNetIDs() {
	ids := make([]int64, 0, len(s.spawns))
	for _, w := range s.spawns {
		ids = append(ids, w.NetID)
	}
	if s.MoreNetIDs != nil {
		ids = append(ids, s.MoreNetIDs()...)
	}
	s.world.NetIds.Save(ids)
}

// RemoveHidden (R-69, R-82d) removes the world spawns a clearing now covers and saves the world
// spawn ids; it returns how many went.
func (s *Spawner) RemoveHidden() int {
	removed := 0
	for i := 0; i < len(s.spawns); {
		w := s.spawns[i]
		id := int64(w.Candidate.EntityID())
		if !s.hidden(id, s.world.State.IsAwake(id)) {
			i++
			continue
		}
		s.forget(i)
		if s.scene.Remove(w.NetID) {
			if s.Despawned != nil {
				s.Despawned(w.NetID)
			}
			s.removed([]int64{w.NetID})
		}
		removed++
	}
	if removed > 0 {
		s.SaveNetIDs()
	}
	return removed
}

// SkipCleanUp: a later scene of the session has no clean-up at its first refresh.
func (s *Spawner) SkipCleanUp() { s.start = nil }

func (s *Spawner) Spawns() []*WorldSpawn       { return s.spawns }
func (s *Spawner) CandidateList() []*Candidate { return s.candidates }

// DueAfterMove: a move refreshes when something marked the scene, none ran yet or the player is
// 8 m from the last one.
func (s *Spawner) DueAfterMove(player Vec3) bool {
	return s.Marked || s.LastRefreshAt == nil || s.LastRefreshAt.DistanceTo(player) >= RefreshStep
}

// WaitsToRespawn: a row whose spawn left the scene (killed, picked) waits to come back.
func (s *Spawner) WaitsToRespawn(configID int64) bool {
	if configID < math.MinInt32 || configID > math.MaxInt32 {
		return false
	}
	_, ok := s.notBefore[int32(configID)]
	return ok
}

// NetIDOf is the net id of the world spawn of a row, if it is spawned.
func (s *Spawner) NetIDOf(configID int64) (int64, bool) {
	for _, w := range s.spawns {
		if int64(w.Candidate.EntityID()) == configID {
			return w.NetID, true
		}
	}
	return 0, false
}

// Drop forgets the world spawn of a row (a quest or an interaction took it away; the world
// state keeps it away) and saves the world spawn ids; its net id, if it was spawned.
func (s *Spawner) Drop(configID int64) (int64, bool) {
	at := slices.IndexFunc(s.spawns, func(w *WorldSpawn) bool { return int64(w.Candidate.EntityID()) == configID })
	if at < 0 {
		return 0, false
	}
	net := s.spawns[at].NetID
	s.forget(at)
	s.SaveNetIDs()
	return net, true
}

// Refresh runs one refresh at the player's location (specs/spawning.md §4 steps 1–8).
func (s *Spawner) Refresh(nowMs, nowSeconds int64) {
	player := s.scene.PlayerPos
	s.LastRefreshAt = &player
	s.Marked = false
	changed := false
	state := s.world.State

	// 1. The floor for world spawns.
	s.scene.NetIds.RaiseTo(max(WorldFloor(s.data.FloorSeconds(s.scene.InstanceID, nowSeconds)), s.scene.MaxID()+1))

	// 2. The first refresh of a session: ids an earlier run left alive (R-121).
	if s.start != nil {
		start := *s.start
		s.start = nil
		alive := s.world.NetIds.Alive()
		var stale []int64
		for _, id := range alive {
			if OnClient(start, id, func(x int64) bool { return s.scene.Get(x) != nil }) {
				stale = append(stale, id)
			}
		}
		if len(stale) > 0 {
			s.removed(stale)
		}
		changed = changed || len(alive) > 0
	}

	// R-4c: the queued prefab groups (specs/quests.md §5.5), before the open-world candidates.
	if s.BeforeCandidates != nil {
		s.BeforeCandidates()
	}

	// 4. Forget and remove.
	for i := len(s.spawns) - 1; i >= 0; i-- {
		w := s.spawns[i]
		id := int64(w.Candidate.EntityID())
		if s.scene.Get(w.NetID) == nil {
			s.forget(i)
			s.notBefore[w.Candidate.EntityID()] = nowMs + RespawnMs
			changed = true
			continue
		}
		gone := w.Candidate.HasVisibility && !Visible(w.Candidate.Row.ComponentsData, s.world.Quests)
		gone = gone || s.hidden(id, state.IsAwake(id))
		gone = gone || (s.locationOf(w.Candidate).DistanceTo(player) > LeaveDistance(w.Class) && !s.inFight(w.NetID))
		if gone {
			s.scene.Remove(w.NetID)
			if s.Despawned != nil {
				s.Despawned(w.NetID)
			}
			s.forget(i)
			s.removed([]int64{w.NetID})
			changed = true
		}
	}

	// 6. The nearest NPCs, one per spot key.
	type near struct {
		c *Candidate
		d float32
	}
	var nearList []near
	for _, c := range s.candidates {
		if c.Class != ClassNpc || c.Sleeping {
			continue
		}
		d := s.locationOf(c).DistanceTo(player)
		if d <= 8000 && (!c.HasVisibility || Visible(c.Row.ComponentsData, s.world.Quests)) {
			nearList = append(nearList, near{c, d})
		}
	}
	vis := func(c *Candidate) int {
		if c.HasVisibility {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(nearList, func(a, b near) int {
		return cmp.Or(cmp.Compare(a.d, b.d), cmp.Compare(vis(a.c), vis(b.c)), cmp.Compare(a.c.Order, b.c.Order))
	})
	nearest := map[int32]bool{}
	spots := map[string]bool{}
	for _, n := range nearList {
		if len(nearest) >= NearestNpcs {
			break
		}
		if n.c.SpotKey != "" {
			if spots[n.c.SpotKey] {
				continue
			}
			spots[n.c.SpotKey] = true
		}
		nearest[n.c.EntityID()] = true
	}

	// 7. Spawn.
	alive := map[SpawnClass]int{}
	monsters := 0
	for _, w := range s.spawns {
		alive[w.Class]++
		if isMonsterClass(w.Class) {
			monsters++
		}
	}
	thisRefresh := map[SpawnClass]int{}
	for _, c := range s.candidates {
		id := int64(c.EntityID())
		woken := c.Sleeping && c.Class != ClassItem && state.IsAwake(id)
		spawnAt := float32(8000)
		switch {
		case c.Class == ClassBeacon || woken:
			spawnAt = 30000
		case c.Class == ClassSeat:
			spawnAt = 2500
		}
		if s.locationOf(c).DistanceTo(player) > spawnAt {
			continue
		}
		if s.spawnedIDs[c.EntityID()] {
			continue
		}
		// A quest NPC (an awake town NPC the quest engine woke) has neither the nearest-60 rule nor caps (§3).
		questNpc := c.Class == ClassNpc && !c.Sleeping && state.IsAwake(id)
		if c.Class == ClassNpc && !c.Sleeping && !questNpc && !nearest[c.EntityID()] {
			continue
		}
		if !woken && !questNpc && c.Class != ClassBeacon {
			capAlive, perRefresh := Caps(c.Class)
			count := monsters
			if c.Class == ClassItem || c.Class == ClassNpc || c.Class == ClassSeat {
				count = alive[c.Class]
			}
			if count >= capAlive || thisRefresh[c.Class] >= perRefresh {
				continue
			}
		}
		if state.IsGone(id, nowSeconds) {
			continue
		}
		if s.hidden(id, state.IsAwake(id)) {
			continue
		}
		if c.Sleeping && !state.IsAwake(id) {
			continue
		}
		if !woken && c.HasVisibility && !Visible(c.Row.ComponentsData, s.world.Quests) {
			continue
		}
		if due, ok := s.notBefore[c.EntityID()]; ok {
			if nowMs < due {
				continue
			}
			delete(s.notBefore, c.EntityID())
		}
		row := c.Row
		if woken && s.RowFor != nil {
			row = s.RowFor(c)
		}
		entity, _ := BuildLevelEntity(s.data, row, s.context, nil)
		if entity == nil {
			s.notBefore[c.EntityID()] = nowMs + RetryMs
			continue
		}
		s.scene.Add(entity)
		cls := c.Class
		if woken {
			cls = ClassBeacon
		}
		s.spawns = append(s.spawns, &WorldSpawn{entity.ID, c, cls, woken})
		s.spawnedIDs[c.EntityID()] = true
		if s.Spawned != nil {
			cand := c
			if row != c.Row {
				copy := *c
				copy.Row = row
				cand = &copy
			}
			s.Spawned(entity, cand)
		}
		alive[cls]++
		if isMonsterClass(cls) {
			monsters++
		}
		thisRefresh[c.Class]++
		s.added(entity)
		if s.AfterSpawn != nil {
			s.AfterSpawn(entity)
		}
		changed = true
	}

	// 8. Save the world spawn ids.
	if changed {
		s.SaveNetIDs()
	}
}

func isMonsterClass(c SpawnClass) bool { return c != ClassBeacon && c != ClassItem && c != ClassNpc }

func (s *Spawner) forget(i int) {
	delete(s.spawnedIDs, s.spawns[i].Candidate.EntityID())
	s.spawns = slices.Delete(s.spawns, i, i+1)
}

// locationOf is the candidate's place: where a quest moved it, else its row's.
func (s *Spawner) locationOf(c *Candidate) Vec3 {
	if p, ok := s.world.Moves.Get(int64(c.EntityID())); ok {
		return Vec3{p.X, p.Y, p.Z}
	}
	return c.Location
}

func LeaveDistance(c SpawnClass) float32 {
	switch c {
	case ClassBeacon:
		return 40000
	case ClassItem:
		return 15000
	case ClassSeat:
		return 3000
	case ClassNpc:
		return 10000
	}
	return 15000
}

// Caps are the alive cap and per-refresh cap (specs/spawning.md §3); beacons and woken rows have none.
func Caps(c SpawnClass) (alive, perRefresh int) {
	switch c {
	case ClassItem:
		return 260, 40
	case ClassSeat:
		return 120, 60
	case ClassNpc:
		return 60, 20
	case ClassBeacon:
		return math.MaxInt32, math.MaxInt32
	}
	return 80, 20
}
