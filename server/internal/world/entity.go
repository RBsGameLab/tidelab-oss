// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package world is a scene's entities and the world spawner (specs/entities.md,
// specs/spawning.md, specs/movement.md §3): net ids, the level entities made from
// LevelEntityConfig rows, the entities of a scene join, the candidates of a map and the
// spawner that shows them as the player comes near. Ported from our C# World/.
package world

import (
	"math"

	"google.golang.org/protobuf/proto"

	"tidelab/protocol"
)

// Vec3 is a location in game units (centimetres).
type Vec3 struct{ X, Y, Z float32 }

var (
	Zero3 = Vec3{}
	// FarAway is where the far-away entities of the scene join stand (specs/entities.md §4).
	FarAway = Vec3{-10000, -10000, -10000}
)

func (v Vec3) DistanceTo(o Vec3) float32 {
	dx, dy, dz := float64(v.X)-float64(o.X), float64(v.Y)-float64(o.Y), float64(v.Z)-float64(o.Z)
	return float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
}

func (v Vec3) ToPb() *protocol.Vector { return &protocol.Vector{X: v.X, Y: v.Y, Z: v.Z} }

func Vec3From(v *protocol.Vector) Vec3 {
	if v == nil {
		return Zero3
	}
	return Vec3{v.X, v.Y, v.Z}
}

// Rot is a rotation in degrees.
type Rot struct{ Pitch, Yaw, Roll float32 }

func (r Rot) ToPb() *protocol.Rotator {
	return &protocol.Rotator{Pitch: r.Pitch, Yaw: r.Yaw, Roll: r.Roll}
}

func RotFrom(r *protocol.Rotator) Rot {
	if r == nil {
		return Rot{}
	}
	return Rot{r.Pitch, r.Yaw, r.Roll}
}

// Entity is one entity the client is told about (specs/entities.md §1). The kind-specific
// part is the component list, built when the entity is made.
type Entity struct {
	ID                int64
	ConfigID          int32
	ConfigType        protocol.EntityConfigType
	EntityType        protocol.EEntityType
	Pos               Vec3
	Rot               Rot
	IsVisible         bool
	PlayerID          int32
	State             protocol.EntityState
	Camp              *int32
	RoleSkinID        int32
	GlideWingSkinID   int32
	GlideBrollySkinID int32
	Components        []*protocol.EntityComponentPb

	// IsVehicle: the player sits in it (the motorcycle; specs/movement.md §5).
	IsVehicle bool
	Occupied  bool

	// PrefabID, PrefabIncID: prefab entities of level plays (R-143h); else 0.
	PrefabID    int32
	PrefabIncID int64

	// LevelMap is a level row's map, for entities spawned from one (else 0).
	LevelMap int32

	// Live builds the entity's current EntityPb when another system owns its values (roles,
	// summons and monsters); the place stays this entity's.
	Live func() *protocol.EntityPb
}

// ToPb is the entity as the client is sent it.
func (e *Entity) ToPb() *protocol.EntityPb {
	if e.Live != nil {
		cur := e.Live()
		cur.Id = e.ID
		cur.Pos = e.Pos.ToPb()
		cur.Rot = e.Rot.ToPb()
		return cur
	}
	return e.BasePb()
}

// BasePb is the EntityPb from the entity's own fields and components (Live not asked).
func (e *Entity) BasePb() *protocol.EntityPb {
	pb := &protocol.EntityPb{
		Id:                 e.ID,
		ConfigId:           e.ConfigID,
		ConfigType:         e.ConfigType,
		EntityType:         int32(e.EntityType),
		Pos:                e.Pos.ToPb(),
		InitPos:            e.Pos.ToPb(),
		Rot:                e.Rot.ToPb(),
		IsVisible:          e.IsVisible,
		IsActorVisible:     true,
		PlayerId:           e.PlayerID,
		EntityState:        e.State,
		GravityDirection:   &protocol.Vector{X: 0, Y: 0, Z: -1},
		InitLinearVelocity: &protocol.Vector{},
		RoleSkinId:         e.RoleSkinID,
		GlideWingSkinId:    e.GlideWingSkinID,
		GlideBrollySkinId:  e.GlideBrollySkinID,
		PrefabId:           e.PrefabID,
		PrefabIncId:        e.PrefabIncID,
	}
	if e.Camp != nil {
		camp := *e.Camp
		pb.Camp = &camp
	}
	for _, c := range e.Components {
		pb.ComponentPbs = append(pb.ComponentPbs, proto.Clone(c).(*protocol.EntityComponentPb))
	}
	return pb
}

// ComponentOf is the entity's first component of case T (e.g. *protocol.EntityComponentPb_TagComponent).
func ComponentOf[T protocol.EntityComponent](e *Entity) (T, bool) {
	for _, c := range e.Components {
		if v, ok := c.Component.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// C wraps a component case (protocol.Component).
func C(c protocol.EntityComponent) *protocol.EntityComponentPb { return protocol.Component(c) }

// Short names of the enum values the world uses.
const (
	TypeNpc          = protocol.EEntityType_EEntityType_Npc
	TypeMonster      = protocol.EEntityType_EEntityType_Monster
	TypeSceneItem    = protocol.EEntityType_EEntityType_SceneItem
	TypeCustom       = protocol.EEntityType_EEntityType_Custom
	TypeVision       = protocol.EEntityType_EEntityType_Vision
	TypeAnimal       = protocol.EEntityType_EEntityType_Animal
	TypeClientOnly   = protocol.EEntityType_EEntityType_ClientOnly
	TypeVehicle      = protocol.EEntityType_EEntityType_Vehicle
	TypePlayerEntity = protocol.EEntityType_EEntityType_PlayerEntity
	TypeSceneEntity  = protocol.EEntityType_EEntityType_SceneEntity

	StateDefault = protocol.EntityState_EntityState_Default
	StateBorn    = protocol.EntityState_EntityState_Born

	ConfigLevel    = protocol.EntityConfigType_EntityConfigType_Level
	ConfigTemplate = protocol.EntityConfigType_EntityConfigType_Template
	ConfigPrefab   = protocol.EntityConfigType_EntityConfigType_Prefab
)
