// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"

	"tidelab/internal/game"
	"tidelab/internal/save"
	"tidelab/internal/world"
)

// SpawnFacts are why a row on the current map has not spawned yet (R-80c, R-80g), from the
// spawner's public state.
type SpawnFacts struct {
	OnMap, Spawned, HasSpawner, InCandidates, Spawnable bool
	RowHidden, HideGrouped, Sleeping, Awake             bool
	Gone, PermanentlyGone, Visible                      bool
	Distance, SpawnRadius                               float32
}

func InspectSpawn(s *game.PlayerSession, configID int64) SpawnFacts {
	sc := s.Scene
	if sc == nil || configID < -1<<31 || configID > 1<<31-1 {
		return SpawnFacts{}
	}
	row := s.Data.Tables.LevelEntity(sc.MapID, int32(configID))
	if row == nil {
		return SpawnFacts{}
	}
	_, spawned := s.Actions().NetIDOf(configID)
	ws := s.World.State
	awake := ws.IsAwake(configID)
	hideGrouped := s.QuestState != nil && s.QuestState.HideGroups.IsHidden(sc.MapID, configID, awake)
	bp := s.Data.Blueprint(row.BlueprintType)
	spawnable := false
	if bp != nil {
		_, kindOK := world.LevelKindOf(bp.EntityLogic)
		spawnable = kindOK && s.Data.Template(row.BlueprintType) != nil
	}
	gone := ws.IsGone(configID, s.Clock.Seconds())
	e, has := ws.Get(configID)
	permGone := has && e.GoneUntil == save.Forever
	f := SpawnFacts{OnMap: true, Spawned: spawned, Spawnable: spawnable, RowHidden: row.IsHidden, HideGrouped: hideGrouped,
		Sleeping: row.InSleep, Awake: awake, Gone: gone, PermanentlyGone: permGone, Visible: true}
	sp := s.Spawner
	if sp == nil {
		return f
	}
	f.HasSpawner = true
	var cand *world.Candidate
	for _, c := range sp.CandidateList() {
		if int64(c.EntityID()) == configID {
			cand = c
			break
		}
	}
	if cand == nil {
		return f
	}
	woken := cand.Sleeping && cand.Class != world.ClassItem && awake
	radius := float32(8000)
	switch {
	case cand.Class == world.ClassBeacon || woken:
		radius = 30000
	case cand.Class == world.ClassSeat:
		radius = 2500
	}
	loc := cand.Location
	if m, ok := s.World.Moves.Get(configID); ok {
		loc = world.Vec3{X: m.X, Y: m.Y, Z: m.Z}
	}
	f.InCandidates = true
	f.Sleeping = cand.Sleeping
	f.Distance = loc.DistanceTo(sc.PlayerPos)
	f.SpawnRadius = radius
	f.Visible = !cand.HasVisibility || woken || world.Visible(cand.Row.ComponentsData, s.World.Quests)
	return f
}

// WaitingHelps: walking closer or another refresh can still bring this row out.
func WaitingHelps(f SpawnFacts) bool {
	if f.Spawned {
		return true
	}
	if !f.OnMap || !f.HasSpawner || !f.InCandidates || !f.Spawnable || f.HideGrouped || f.Gone {
		return false
	}
	if f.Sleeping && !f.Awake {
		return false
	}
	return f.Visible
}

func WhyNotSpawned(s *game.PlayerSession, configID int64) string {
	f := InspectSpawn(s, configID)
	switch {
	case !f.OnMap:
		return "not on map"
	case f.Spawned:
		return "spawned"
	case !f.HasSpawner:
		return "no world spawner"
	case !f.InCandidates:
		if f.RowHidden {
			return "hidden row"
		}
		return "not a candidate"
	case !f.Spawnable:
		return "not spawnable"
	case f.HideGrouped:
		return "hidden by hide group"
	case f.PermanentlyGone:
		return "destroyed/gone"
	case f.Gone:
		return "waiting to respawn"
	case f.Sleeping && !f.Awake:
		return "asleep, nobody woke it"
	case !f.Visible:
		return "visibility conditions not met"
	case f.Distance > f.SpawnRadius:
		return fmt.Sprintf("%s m away", f0(f.Distance/100))
	}
	return "nothing blocks it (next refresh)"
}
