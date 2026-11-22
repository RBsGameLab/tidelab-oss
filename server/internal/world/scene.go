// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"fmt"
	"slices"
	"strconv"

	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/internal/zon"
)

// PositionSaveDelayMs: a move's position is written this long after the first unsaved move
// (specs/movement.md §3).
const PositionSaveDelayMs = 30_000

// Scene is the scene a player is in: one instance (InstanceDungeon.Id) on one map, its
// entities, its net id counter, its flow clock and the player's place in it
// (specs/entities.md, movement.md §3). Everything here is rebuilt at each scene join.
type Scene struct {
	InstanceID int32
	MapID      int32
	Files      *save.PlayerFiles
	NetIds     *NetIds
	Flow       *FlowClock
	Instance   *save.SceneInstanceFile

	PlayerPos Vec3
	PlayerRot Rot
	GroupType int32
	// PositionSaveDue is when the moved position is due to be written (game clock ms), or nil.
	PositionSaveDue *int64

	VehicleID      int64
	PlayerEntityID int64
	BattleEntityID int64
	// RoleEntities is the role entity per role id, for the formation's roles.
	RoleEntities omap.Map[int32, int64]
	// NextBuffHandle: buff handles of this scene (specs/entities.md §4.1).
	NextBuffHandle int32

	entities         map[int64]*Entity
	order            []*Entity
	instanceOriginal *zon.Value
}

func NewScene(instanceID, mapID int32, files *save.PlayerFiles, flow *FlowClock) *Scene {
	s := &Scene{
		InstanceID:     instanceID,
		MapID:          mapID,
		Files:          files,
		Flow:           flow,
		NetIds:         NewNetIds(files.ScenePath(instanceID, "entity/next")),
		GroupType:      1,
		NextBuffHandle: 1,
		entities:       map[int64]*Entity{},
	}
	s.instanceOriginal = files.Read(s.InstancePath())
	s.Instance = save.SceneInstanceFrom(s.instanceOriginal)
	return s
}

// HadPlayer: scene/<i>/instance named this player before the join.
func (s *Scene) HadPlayer() bool {
	for _, p := range s.Instance.Players {
		if p.ID == s.Files.PlayerID {
			return true
		}
	}
	return false
}

func (s *Scene) Entities() []*Entity { return s.order }

func (s *Scene) InstancePath() string {
	return "scene/" + strconv.Itoa(int(s.InstanceID)) + "/instance"
}

func (s *Scene) Get(id int64) *Entity { return s.entities[id] }

// Add adds an entity under the counter's next id (specs/entities.md §2).
func (s *Scene) Add(e *Entity) *Entity {
	e.ID = s.NetIds.Take()
	s.put(e)
	return e
}

// AddAs adds an entity under an id another system chose; an id in use or not positive panics.
func (s *Scene) AddAs(e *Entity, id int64) *Entity {
	if id <= 0 || s.entities[id] != nil {
		panic(fmt.Errorf("net id %d is in use or not positive", id))
	}
	s.NetIds.RaiseTo(id + 1)
	e.ID = id
	s.put(e)
	return e
}

func (s *Scene) put(e *Entity) {
	s.entities[e.ID] = e
	s.order = append(s.order, e)
}

func (s *Scene) Remove(id int64) bool {
	e, ok := s.entities[id]
	if !ok {
		return false
	}
	delete(s.entities, id)
	s.order = slices.DeleteFunc(s.order, func(x *Entity) bool { return x == e })
	return true
}

// MaxID is the largest net id the scene holds (0 when empty).
func (s *Scene) MaxID() int64 {
	var m int64
	for i, e := range s.order {
		if i == 0 || e.ID > m {
			m = e.ID
		}
	}
	return m
}

// MarkMoved: the position is written PositionSaveDelayMs after the first such move.
func (s *Scene) MarkMoved(nowMs int64) {
	if s.PositionSaveDue == nil {
		due := nowMs + PositionSaveDelayMs
		s.PositionSaveDue = &due
	}
}

// SaveIfDue writes the position if it is due (or force).
func (s *Scene) SaveIfDue(nowMs int64, force bool) {
	if s.PositionSaveDue == nil || (!force && nowMs < *s.PositionSaveDue) {
		return
	}
	s.SaveInstance()
}

// SaveInstance writes scene/<i>/instance with the player's current place.
func (s *Scene) SaveInstance() {
	s.PositionSaveDue = nil
	var me *save.ScenePlayer
	for _, p := range s.Instance.Players {
		if p.ID == s.Files.PlayerID {
			me = p
			break
		}
	}
	if me == nil {
		me = &save.ScenePlayer{ID: s.Files.PlayerID, GroupType: 1}
		s.Instance.Players = append(s.Instance.Players, me)
	}
	me.GroupType = s.GroupType
	me.Location = [3]float32{s.PlayerPos.X, s.PlayerPos.Y, s.PlayerPos.Z}
	me.Rotation = [3]float32{s.PlayerRot.Roll, s.PlayerRot.Pitch, s.PlayerRot.Yaw}
	s.Files.Write(s.InstancePath(), s.Instance.ToZon(s.instanceOriginal))
}
