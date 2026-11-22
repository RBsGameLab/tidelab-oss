// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
)

// WaveSpotShift is the shift between copies of a template wave at one spot, scene units (R-127).
const WaveSpotShift float32 = 200

// SpawnPrefabs drains the prefab spawn queue and saves the world spawn ids with the live prefab
// monsters (specs/quests.md §5.5). Ported from our C# Game/PlayerSession.Prefabs.cs.
func (p *PlayerSession) SpawnPrefabs() {
	q := p.QuestState
	sc := p.Scene
	if q == nil || sc == nil {
		return
	}
	jobs := q.Prefabs.TakeQueue()
	if len(jobs) == 0 {
		return
	}
	for _, job := range jobs {
		p.spawnPrefab(q, sc, job)
	}
	if p.Spawner != nil {
		p.Spawner.SaveNetIDs()
	}
}

func (p *PlayerSession) spawnPrefab(q *PlayerQuests, sc *world.Scene, job quests.PendingPrefab) {
	g := job.Group
	g.Spawned = true
	prefab, ok := q.Prefabs.Get(g.PrefabID)
	if !ok {
		common.Warn("prefab", fmt.Sprintf("prefab %d of %q is not in Prefabs.json", g.PrefabID, g.VarName))
		return
	}
	// 2. Where: the position entity's row, else the player.
	origin := sc.PlayerPos
	if job.PosEntity > 0 && job.PosEntity <= math.MaxInt32 {
		if row := p.Data.Tables.LevelEntity(sc.MapID, int32(job.PosEntity)); row != nil && len(row.Transform) > 0 && row.Transform[0] != nil {
			t := row.Transform[0]
			origin = world.Vec3{X: float32(t.X) / 100, Y: float32(t.Y) / 100, Z: float32(t.Z) / 100}
		}
	}
	// 3. The monster spots: BlueprintConfig.EntityType Monster (R-60: never the blueprint's name).
	var spots []quests.PrefabEntity
	for _, e := range prefab.Entities {
		if bp := p.Data.Blueprint(e.BlueprintType); bp != nil && bp.EntityType == "Monster" {
			spots = append(spots, e)
		}
	}
	// 4. Waves (R-56, R-126).
	isWave := len(g.Waves) > 0
	count := len(spots)
	if isWave {
		if g.Wave < len(g.Waves) {
			count = len(g.Waves[g.Wave])
		} else {
			count = 0
		}
	}
	// R-127: a 深塔 floor's buffs: RoleBuff on the roles at wave 0, MonsterBuff on each monster.
	var roleBuffs, monsterBuffs []int64
	var floorID int32
	if isWave {
		if floor := q.Tower.FloorOfDungeon(p.Data.Tables, sc.InstanceID); floor != nil {
			roleBuffs, monsterBuffs = buffIDs(floor.RoleBuff), buffIDs(floor.MonsterBuff)
			floorID = floor.Id
		}
	}
	if g.Wave == 0 && len(roleBuffs) > 0 {
		for _, roleNet := range sc.RoleEntities.Values() {
			if role := p.Combat.Scene.GetEntity(roleNet); role != nil {
				p.Combat.Buffs.ApplyDirectPassiveBuffs(role, roleBuffs, p.Sink())
			}
		}
	}
	var level int32
	if d, ok := p.Data.Tables.InstanceDungeonById[sc.InstanceID]; ok {
		level = d.EntityLevel
	}
	spawned := 0
	for k := 0; k < count && len(spots) > 0; k++ {
		e := spots[k%len(spots)]
		var propertyID int32
		var tower *quests.TowerEntity
		templateWave := false
		pos := e.Pos
		if isWave && g.EntityWaves {
			own, ok := quests.PrefabEntityIn(prefab, g.Waves[g.Wave][k])
			if !ok {
				continue
			}
			e = own
			pos = own.Pos
			propertyID = own.PropertyID
			// R-160c: the panel's swap of this kind in this wave: the other monster's own tower
			// entity at this one's spot, at the floor's level and buffs.
			if m, ok := towerSwapOf(p.Data.Panel, q.Data, floorID, g.Wave, own.BlueprintType); ok {
				swap := m.TowerEntity
				tower = &swap
				e.ID, e.BlueprintType, e.Name = m.EntityID, m.Blueprint, m.Blueprint
				propertyID = m.PropertyID
			}
		} else if isWave {
			tpl := p.Data.TemplateByID(g.Waves[g.Wave][k])
			if tpl == nil {
				continue
			}
			templateWave = true
			if tw, ok := q.Prefabs.TowerEntityOf(tpl.BlueprintType); ok {
				twCopy := tw
				tower = &twCopy
			}
			pos = [3]float32{e.Pos[0] + WaveSpotShift*float32(k/len(spots)), e.Pos[1], e.Pos[2]}
			id := tpl.Id
			if tower != nil {
				id = tower.EntityID
				propertyID = tower.PropertyID
			}
			e.ID = id
			e.BlueprintType = tpl.BlueprintType
			e.Name = tpl.BlueprintType
		}
		location := world.Vec3{X: origin.X + pos[0], Y: origin.Y + pos[1], Z: origin.Z + pos[2]}
		rot := world.Rot{Pitch: e.Rot[1], Yaw: e.Rot[2], Roll: e.Rot[0]}
		var identity world.Identity
		switch {
		case tower != nil:
			identity = world.Identity{Type: world.ConfigPrefab, PrefabID: tower.PrefabID, PrefabIncID: g.Inc}
		case templateWave:
			identity = world.Identity{Type: world.ConfigTemplate}
		default:
			identity = world.Identity{Type: world.ConfigPrefab, PrefabID: g.PrefabID, PrefabIncID: g.Inc}
		}
		row := prefabRow(sc.MapID, e, location, rot, propertyID, level)
		entity, refused := world.BuildLevelEntity(p.Data, row, &world.LevelContext{WorldLevel: p.World.WorldLevel}, &identity)
		if entity == nil {
			common.Warn("prefab", fmt.Sprintf("prefab %d entity %d (%s): not spawned (%s)", g.PrefabID, e.ID, e.BlueprintType, refused))
			continue
		}
		sc.Add(entity)
		p.RegisterLevelEntity(entity, row, false)
		p.added(entity)
		g.Alive = append(g.Alive, entity.ID)
		if len(monsterBuffs) > 0 {
			if monster := p.Combat.Scene.GetEntity(entity.ID); monster != nil {
				p.Combat.Buffs.ApplyDirectPassiveBuffs(monster, monsterBuffs, p.Sink())
			}
		}
		spawned++
	}
	waveNote := ""
	if isWave {
		waveNote = fmt.Sprintf(" wave %d/%d", g.Wave+1, len(g.Waves))
	}
	common.Info("prefab", fmt.Sprintf("level play %x: %q prefab %d%s: %d monsters at %.0f,%.0f,%.0f",
		g.Run, g.VarName, g.PrefabID, waveNote, spawned, origin.X, origin.Y, origin.Z))
}

func prefabRow(mapID int32, e quests.PrefabEntity, loc world.Vec3, rot world.Rot, propertyID, level int32) *data.LevelEntityConfig {
	iv := func(x, y, z float32) *data.IntVector {
		return &data.IntVector{
			X: int32(math.RoundToEven(float64(x * 100))),
			Y: int32(math.RoundToEven(float64(y * 100))),
			Z: int32(math.RoundToEven(float64(z * 100))),
		}
	}
	var ak []string
	var av []data.Value
	if propertyID > 0 {
		ak = append(ak, "PropertyId")
		av = append(av, data.Number(propertyID))
	}
	if level > 0 {
		ak = append(ak, "Level")
		av = append(av, data.Number(level))
	}
	var components data.Value = &data.Object{}
	if len(ak) > 0 {
		components = &data.Object{
			Keys:   []string{"AttributeComponent"},
			Values: []data.Value{&data.Object{Keys: ak, Values: av}},
		}
	}
	return &data.LevelEntityConfig{
		MapId:          mapID,
		EntityId:       e.ID,
		BlueprintType:  e.BlueprintType,
		Name:           e.Name,
		Transform:      []*data.IntVector{iv(loc.X, loc.Y, loc.Z), iv(rot.Roll, rot.Pitch, rot.Yaw), iv(1, 1, 1)},
		ComponentsData: components,
	}
}

// RowForWoken (R-127): a woken monster row in an entered dungeon spawns at InstanceDungeon.EntityLevel.
// Ported from PlayerSession.Dungeons.cs.
func (p *PlayerSession) RowForWoken(c *world.Candidate) *data.LevelEntityConfig {
	row := c.Row
	q := p.QuestState
	sc := p.Scene
	if q == nil || sc == nil || !q.IsDungeonEntered(sc.InstanceID) {
		return row
	}
	d, ok := p.Data.Tables.InstanceDungeonById[sc.InstanceID]
	if !ok || d.EntityLevel <= 0 {
		return row
	}
	bp := p.Data.Blueprint(row.BlueprintType)
	monster := q.Spawners.Mentions(int64(row.EntityId)) ||
		strings.Contains(row.BlueprintType, "Monster") ||
		(bp != nil && bp.EntityType == "Monster")
	if !monster {
		return row
	}
	cp := *row
	cp.ComponentsData = withAttrLevel(row.ComponentsData, d.EntityLevel)
	return &cp
}

func withAttrLevel(components data.Value, level int32) data.Value {
	var keys []string
	var values []data.Value
	if o, ok := components.(*data.Object); ok && o != nil {
		keys = slices.Clone(o.Keys)
		values = slices.Clone(o.Values)
	}
	at := slices.Index(keys, "AttributeComponent")
	var ak []string
	var av []data.Value
	if at >= 0 {
		if attr, ok := values[at].(*data.Object); ok && attr != nil {
			ak = slices.Clone(attr.Keys)
			av = slices.Clone(attr.Values)
		}
	}
	if li := slices.Index(ak, "Level"); li >= 0 {
		av[li] = data.Number(level)
	} else {
		ak = append(ak, "Level")
		av = append(av, data.Number(level))
	}
	newAttr := &data.Object{Keys: ak, Values: av}
	if at >= 0 {
		values[at] = newAttr
	} else {
		keys = append(keys, "AttributeComponent")
		values = append(values, newAttr)
	}
	return &data.Object{Keys: keys, Values: values}
}

// buffIDs are a TowerConfig buff list (numbers in the table) as buff ids.
func buffIDs(v []float64) []int64 {
	out := make([]int64, len(v))
	for i, b := range v {
		out[i] = int64(b)
	}
	return out
}
