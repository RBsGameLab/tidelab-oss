// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"math"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/world"
	"tidelab/protocol"
)

func movementRig(t *testing.T) *rig {
	r := newRig(t)
	tp, err := data.ParseJSON([]byte(`{"TeleportComponent":{"TeleportPos":{"X":100,"A":-90}}}`))
	if err != nil {
		t.Fatal(err)
	}
	r.tables.LevelEntityConfig = []*data.LevelEntityConfig{
		{MapId: 8, EntityId: 500, BlueprintType: "Teleport001",
			Transform: []*data.IntVector{{X: 10000, Y: 20000, Z: 300}, {Z: 9000}, {X: 300, Y: 300, Z: 300}}, ComponentsData: tp},
		{MapId: 906, EntityId: 600, BlueprintType: "Teleport001", Transform: []*data.IntVector{{X: 500000, Y: 600000, Z: 70000}, {}}},
	}
	r.tables.Teleporter = []*data.Teleporter{{Id: 1, MapId: 8, TeleportEntityConfigId: 500}, {Id: 2, MapId: 906, TeleportEntityConfigId: 600}, {Id: 3, MapId: 8, TeleportEntityConfigId: 999}}
	r.tables.InstanceDungeon = append(r.tables.InstanceDungeon, &data.InstanceDungeon{Id: 8501, MapConfigId: 8})
	r.tables.Reindex()
	r.data = world.NewWorldData(r.tables, map[int32][]int32{906: {4, 5}})
	r.saveLivePlayer()
	return r
}

type sample struct{ x, y, z float32 }

func moving(id, originator int64, samples ...*sample) *protocol.MovingEntityData {
	m := &protocol.MovingEntityData{EntityId: id, Originator: originator}
	for _, s := range samples {
		if s == nil {
			m.MoveInfos = append(m.MoveInfos, &protocol.MoveReplaySample{})
		} else {
			m.MoveInfos = append(m.MoveInfos, &protocol.MoveReplaySample{Location: &protocol.Vector{X: s.x, Y: s.y, Z: s.z}, Rotation: &protocol.Rotator{Yaw: 10}})
		}
	}
	return m
}

func move(p *PlayerSession, ms ...*protocol.MovingEntityData) {
	p.Move(&protocol.MovePackagePush{MovingEntities: ms})
}

func roleOf(p *PlayerSession, id int32) int64 {
	n, _ := p.Scene.RoleEntities.Get(id)
	return n
}

func TestARoleMoveCarriesThePlayerAndTheLastSampleWins(t *testing.T) {
	p, _ := movementRig(t).enter(false)
	role := roleOf(p, 1304)
	move(p, moving(role, 777, &sample{1, 1, 1}, &sample{5, 6, 7}))
	if p.Scene.PlayerPos != (world.Vec3{X: 5, Y: 6, Z: 7}) || p.Scene.PlayerRot.Yaw != 10 || p.Scene.Get(role).Pos != (world.Vec3{X: 5, Y: 6, Z: 7}) {
		t.Fatal(p.Scene.PlayerPos)
	}
}

func TestOtherEntitiesMoveAloneUnknownAndEmptyMovesAreSkipped(t *testing.T) {
	p, _ := movementRig(t).enter(false)
	s := p.Scene
	before := s.PlayerPos
	move(p, moving(s.BattleEntityID, 777, &sample{9, 9, 9}), moving(12345, 777, &sample{1, 2, 3}), moving(s.PlayerEntityID, 777))
	if s.PlayerPos != before || s.Get(s.BattleEntityID).Pos != (world.Vec3{X: 9, Y: 9, Z: 9}) {
		t.Fatal(s.PlayerPos)
	}
	move(p, moving(s.BattleEntityID, 777, nil))
	if s.Get(s.BattleEntityID).Pos != (world.Vec3{X: 9, Y: 9, Z: 9}) {
		t.Fatal("moved by an empty sample")
	}
}

func TestTheVehicleCarriesThePlayerWhenRiddenOrMovedByARole(t *testing.T) {
	p, _ := movementRig(t).enter(false)
	s := p.Scene
	move(p, moving(s.VehicleID, 777, &sample{1, 0, 0}))
	if s.PlayerPos == (world.Vec3{X: 1}) {
		t.Fatal("carried")
	}
	move(p, moving(s.VehicleID, roleOf(p, 1304), &sample{2, 0, 0}))
	if s.PlayerPos != (world.Vec3{X: 2}) {
		t.Fatal(s.PlayerPos)
	}
	if p.Vehicle(&protocol.VehicleRequest{EntityId: s.VehicleID, IsGetOn: true}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		s.Get(s.VehicleID).Pos != s.Get(roleOf(p, 1304)).Pos {
		t.Fatal("get on")
	}
	move(p, moving(s.VehicleID, 777, &sample{3, 0, 0}))
	if s.PlayerPos != (world.Vec3{X: 3}) {
		t.Fatal(s.PlayerPos)
	}
	if p.Vehicle(&protocol.VehicleRequest{EntityId: s.BattleEntityID}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrVehicleEntityNotExist {
		t.Fatal("not a vehicle")
	}
	if p.EnterVehicle(&protocol.EntityEnterVehicleRequest{VehicleId: s.VehicleID, IsGetOn: false}).ErrorCode != protocol.ErrorCode_ErrorCode_Success || s.Get(s.VehicleID).Occupied {
		t.Fatal("get off")
	}
}

func TestMovesRefreshOnTheFirstAndPast8m(t *testing.T) {
	p, _ := movementRig(t).enter(false)
	role := roleOf(p, 1304)
	if p.Spawner.LastRefreshAt != nil {
		t.Fatal("refreshed")
	}
	move(p, moving(role, 777, &sample{0, 0, 0}))
	if *p.Spawner.LastRefreshAt != world.Zero3 {
		t.Fatal(*p.Spawner.LastRefreshAt)
	}
	move(p, moving(role, 777, &sample{700, 0, 0}))
	if *p.Spawner.LastRefreshAt != world.Zero3 {
		t.Fatal(*p.Spawner.LastRefreshAt)
	}
	move(p, moving(role, 777, &sample{800, 0, 0}))
	if *p.Spawner.LastRefreshAt != (world.Vec3{X: 800}) {
		t.Fatal(*p.Spawner.LastRefreshAt)
	}
}

func TestTheFirstHeartbeatAfterAJoinRefreshes(t *testing.T) {
	p, _ := movementRig(t).enter(false)
	if !p.Spawner.Marked {
		t.Fatal("not marked")
	}
	p.Heartbeat()
	if p.Spawner.LastRefreshAt == nil || p.Spawner.Marked {
		t.Fatal("no refresh")
	}
}

func TestThePositionIsSaved30sAfterTheFirstMove(t *testing.T) {
	r := movementRig(t)
	p, _ := r.enter(false)
	role := roleOf(p, 1304)
	move(p, moving(role, 777, &sample{11, 0, 0}))
	r.clock.Advance(20_000)
	move(p, moving(role, 777, &sample{22, 0, 0}))
	if strings.Contains(r.read("scene/8/instance"), ".{ 22, 0, 0 }") {
		t.Fatal("saved early")
	}
	r.clock.Advance(10_000)
	move(p, moving(role, 777, &sample{33, 0, 0}))
	if !strings.Contains(r.read("scene/8/instance"), ".{ 33, 0, 0 }") || !strings.Contains(r.read("last_scene"), "8") {
		t.Fatal(r.read("scene/8/instance"))
	}
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.001 }

func TestTheLandingPointTurnsTheOffsetByTheYawAndIgnoresScale(t *testing.T) {
	r := movementRig(t)
	pos, facing := LandingPoint(r.tables.LevelEntity(8, 500))
	if !near(pos.X, 100) || !near(pos.Y, 300) || !near(pos.Z, 3) || !near(facing, 0) {
		t.Fatal(pos, facing)
	}
	plain, yaw := LandingPoint(r.tables.LevelEntity(906, 600))
	if plain != (world.Vec3{X: 5000, Y: 6000, Z: 700}) || yaw != 0 {
		t.Fatal(plain, yaw)
	}
}

func TestASameMapTeleportPushesTeleportNotifyWithTheFacingInZ(t *testing.T) {
	p, c := movementRig(t).enter(false)
	c.msgs = nil
	r := p.TeleportTransfer(&protocol.TeleportTransferRequest{Id: 1})
	if r.ErrorCode != protocol.ErrorCode_ErrorCode_Success || r.MapId != 8 || !near(r.PosX, 100) || len(c.msgs) != 1 {
		t.Fatal(r, c.names())
	}
	n := c.msgs[0].(*protocol.TeleportNotify)
	if n.Rotation.X != 0 || n.Rotation.Y != 0 || !near(n.Rotation.Z, 0) || !near(n.Position.Y, 300) {
		t.Fatal(n)
	}
	if p.TeleportTransfer(&protocol.TeleportTransferRequest{Id: 99}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrTeleportIdNotExist ||
		p.TeleportTransfer(&protocol.TeleportTransferRequest{Id: 3}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrTeleportCreatureIdNotExist {
		t.Fatal("errors")
	}
}

func TestAnotherMapLeavesTheSceneAndJoinsTheNewOneAtTheLandingPoint(t *testing.T) {
	r := movementRig(t)
	p, c := r.enter(false)
	c.msgs = nil
	resp := p.TeleportTransfer(&protocol.TeleportTransferRequest{Id: 2})
	if resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success || resp.MapId != 906 {
		t.Fatal(resp)
	}
	names := c.names()
	if names[0] != "LeaveSceneNotify" || names[1] != "ExploreToolAllNotify" || strings.Contains(strings.Join(names, ","), "BasicInfoNotify") {
		t.Fatal(names)
	}
	j := last[*protocol.JoinSceneNotify](t, c).SceneInfo
	if j.InstanceId != 906 || j.PlayerInfos[0].Location.X != 5000 || j.PlayerInfos[0].Rotation.Yaw != 186 {
		t.Fatal(j.PlayerInfos[0])
	}
	if !strings.Contains(r.read("last_scene"), ".instance_id = 906,") || p.Scene.InstanceID != 906 || p.Spawner == nil {
		t.Fatal(r.read("last_scene"))
	}
}

func TestEnterInstToTheOpenWorldLandsOnItsEntityElseItsBornPoint(t *testing.T) {
	p, c := movementRig(t).enter(false)
	c.msgs = nil
	ok := protocol.ErrorCode_ErrorCode_Success
	if p.EnterInst(&protocol.EnterInstRequest{InstanceId: 8}).ErrorCode != ok {
		t.Fatal("8")
	}
	p.RunAfterResponse()
	if len(c.msgs) != 0 {
		t.Fatal(c.names())
	}
	if p.EnterInst(&protocol.EnterInstRequest{InstanceId: 906, PosEntityId: 600}).ErrorCode != ok || len(c.msgs) != 0 {
		t.Fatal(c.names())
	}
	p.RunAfterResponse()
	if last[*protocol.JoinSceneNotify](t, c).SceneInfo.PlayerInfos[0].Location.X != 5000 {
		t.Fatal("906")
	}
	c.msgs = nil
	p.EnterInst(&protocol.EnterInstRequest{InstanceId: 8})
	p.RunAfterResponse()
	if last[*protocol.JoinSceneNotify](t, c).SceneInfo.PlayerInfos[0].Location.X != -143200 {
		t.Fatal("8 born")
	}
	notFound := protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound
	if p.EnterInst(&protocol.EnterInstRequest{InstanceId: 8501}).ErrorCode != notFound || p.EnterInst(&protocol.EnterInstRequest{InstanceId: 4}).ErrorCode != notFound {
		t.Fatal("refusals")
	}
}
