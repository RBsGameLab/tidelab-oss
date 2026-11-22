// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"google.golang.org/protobuf/proto"

	"math"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// The requests the client sends while loading and standing in the world (specs/enter-world.md
// §5, entities.md §7–§8, movement.md §4.1, §6), and the moves, teleports, scene changes and
// vehicles of specs/movement.md.

func (p *PlayerSession) EntityActive(req *protocol.EntityActiveRequest) *protocol.EntityActiveResponse {
	var e *world.Entity
	if p.Scene != nil {
		e = p.Scene.Get(req.EntityId)
	}
	if e == nil {
		return &protocol.EntityActiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrEntityNotFound}
	}
	pb := e.ToPb()
	return &protocol.EntityActiveResponse{IsVisible: pb.IsVisible, Pos: pb.Pos, Rot: pb.Rot, ComponentPbs: pb.ComponentPbs}
}

// ApplyClientEntityIDs gives net ids to the entities the client makes itself (specs/entities.md §7).
func (p *PlayerSession) ApplyClientEntityIDs(req *protocol.ApplySceneClientEntityIncIdRequest) *protocol.ApplySceneClientEntityIncIdResponse {
	s := p.Scene
	resp := &protocol.ApplySceneClientEntityIncIdResponse{}
	if s == nil {
		return resp
	}
	resp.InstId = s.InstanceID
	for _, cfg := range req.EntityConfigId {
		var e *world.Entity
		for _, x := range s.Entities() {
			if x.ConfigID == cfg && x.PrefabID == 0 && (x.ConfigType == world.ConfigLevel || x.ConfigType == world.ConfigTemplate) {
				e = x
				break
			}
		}
		if e == nil {
			e = s.Add(&world.Entity{ConfigID: cfg, ConfigType: world.ConfigTemplate, EntityType: world.TypeSceneEntity,
				Pos: world.FarAway, PlayerID: p.State.PlayerID()})
		}
		resp.ClientEntityInfo = append(resp.ClientEntityInfo, &protocol.ClientEntityInfoPb{EntityConfigId: cfg, IncId: proto.Int64(e.ID), ServerRealExist: proto.Bool(true), IsClientEntityConfig: true})
	}
	return resp
}

// TraceEntityPos is where a not-loaded entity is (R-100): the spawned entity's place, else its
// row on the asked dungeon's map (the scene's when the dungeon is 0 or unknown).
func (p *PlayerSession) TraceEntityPos(req *protocol.TraceEntityPosRequest) *protocol.TraceEntityPosResponse {
	notFound := &protocol.TraceEntityPosResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrEntityNotFound}
	var m int32
	if d, ok := p.Data.Tables.InstanceDungeonById[req.DungeonId]; ok {
		m = d.MapConfigId
	} else if p.Scene != nil {
		m = p.Scene.MapID
	} else {
		return notFound
	}
	var pos *world.Vec3
	if p.Scene != nil && p.Scene.MapID == m {
		for _, e := range p.Scene.Entities() {
			if e.ConfigID == req.ConfigId && e.EntityType != protocol.EEntityType_EEntityType_Player {
				at := e.Pos
				pos = &at
				break
			}
		}
	}
	if pos == nil {
		if row := p.Data.Tables.LevelEntity(m, req.ConfigId); row != nil && len(row.Transform) > 0 && row.Transform[0] != nil {
			t := row.Transform[0]
			pos = &world.Vec3{X: float32(t.X) / 100, Y: float32(t.Y) / 100, Z: float32(t.Z) / 100}
		}
	}
	if pos == nil {
		return notFound
	}
	return &protocol.TraceEntityPosResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Pos: pos.ToPb()}
}

func (p *PlayerSession) TeleportData() *protocol.TeleportDataResponse {
	r := &protocol.TeleportDataResponse{ErrCode: protocol.ErrorCode_ErrorCode_Success}
	for _, t := range p.Data.Tables.Teleporter {
		r.Ids = append(r.Ids, t.Id)
	}
	return r
}

func (p *PlayerSession) TimeCheck(req *protocol.TimeCheckRequest) *protocol.TimeCheckResponse {
	real := p.Clock.RealMs()
	flow := real
	if p.Scene != nil {
		flow = p.Scene.Flow.Now(p.Clock.Ms())
	}
	return &protocol.TimeCheckResponse{ClientTime: req.ClientTime, ServerTime: real, ServerStopTime: real, SceneStopTime: real, FlowTime: flow}
}

// SetFlowDilation: a time-stop push re-anchors the flow time (specs/enter-world.md §4.2).
func (p *PlayerSession) SetFlowDilation(d float64) {
	if p.Scene != nil {
		p.Scene.Flow.SetDilation(d, p.Clock.Ms())
	}
	if p.Combat != nil {
		p.Combat.Scene.SetTimeDilation(float32(d), p.Clock.RealMs())
	}
}

// Move is MovePackagePush (specs/movement.md §1): the last sample of each moving entity.
func (p *PlayerSession) Move(push *protocol.MovePackagePush) {
	s := p.Scene
	if s == nil {
		return
	}
	for _, m := range push.MovingEntities {
		e := s.Get(m.EntityId)
		if e == nil {
			common.Warn("scene", fmtInt("entity with id %d is not movable (originator: %d, sender: %d)", m.EntityId, m.Originator, p.State.PlayerID()))
			continue
		}
		if len(m.MoveInfos) == 0 {
			common.Warn("scene", fmtInt("move of entity %d has no samples", m.EntityId))
			continue
		}
		sample := m.MoveInfos[len(m.MoveInfos)-1]
		if sample.Location == nil {
			continue
		}
		carries := p.roleEntityHolds(m.EntityId) || (e.IsVehicle && (e.Occupied || p.roleEntityHolds(m.Originator)))
		loc := world.Vec3From(sample.Location)
		e.Pos = loc
		if c := p.Combat.Scene.GetEntity(m.EntityId); c != nil {
			c.SetPosition(loc.X, loc.Y, loc.Z)
		}
		if carries {
			s.PlayerPos = loc
		}
		if sample.Rotation != nil {
			e.Rot = world.RotFrom(sample.Rotation)
			if carries {
				s.PlayerRot = e.Rot
			}
		}
		p.AfterMove()
	}
}

// AfterMove reacts to an applied move with the player's location (specs/movement.md §2).
func (p *PlayerSession) AfterMove() {
	s := p.Scene
	if s == nil {
		return
	}
	s.MarkMoved(p.Clock.Ms())
	p.syncPlayerPlace(s)
	p.Tick()
	if p.Systems.Tick(p) || p.Spawner == nil {
		return
	}
	p.Combat.Revive.TryBeaconHealOnMove(s.PlayerPos.X, s.PlayerPos.Y, s.PlayerPos.Z, p.Clock.RealMs(), p.Sink())
	if p.Systems.Moved(p, s.PlayerPos) {
		return
	}
	if p.Spawner != nil && p.Spawner.DueAfterMove(s.PlayerPos) {
		p.Spawner.Refresh(p.Clock.Ms(), p.Clock.Seconds())
	}
}

// LandingPoint is where a beacon puts the player (R-48, specs/movement.md §4.2): the row's
// location moved by TeleportComponent.TeleportPos turned by the row's yaw; the facing is the yaw
// plus its A. Scale is ignored.
func LandingPoint(row *data.LevelEntityConfig) (world.Vec3, float32) {
	pos, rot, ok := world.Place(row)
	if !ok {
		t := row.Transform[0]
		pos, rot = world.Vec3{X: float32(t.X) / 100, Y: float32(t.Y) / 100, Z: float32(t.Z) / 100}, world.Rot{}
	}
	yaw := rot.Yaw
	tp := world.Obj(world.Field(world.Field(row.ComponentsData, "TeleportComponent"), "TeleportPos"))
	if tp == nil {
		return pos, yaw
	}
	x := func(k string) float64 {
		if n, ok := tp.Get(k).(data.Number); ok {
			return float64(n)
		}
		return 0
	}
	rad := float64(yaw) * math.Pi / 180
	dx, dy := x("X"), x("Y")
	pos = world.Vec3{
		X: float32(float64(pos.X) + dx*math.Cos(rad) - dy*math.Sin(rad)),
		Y: float32(float64(pos.Y) + dx*math.Sin(rad) + dy*math.Cos(rad)),
		Z: float32(float64(pos.Z) + x("Z")),
	}
	return pos, float32(float64(yaw) + x("A"))
}

// TeleportTransfer is beacon travel (specs/movement.md §4.2).
func (p *PlayerSession) TeleportTransfer(req *protocol.TeleportTransferRequest) *protocol.TeleportTransferResponse {
	var tp *data.Teleporter
	for _, t := range p.Data.Tables.Teleporter {
		if t.Id == req.Id {
			tp = t
			break
		}
	}
	if tp == nil {
		return &protocol.TeleportTransferResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrTeleportIdNotExist}
	}
	row := p.Data.Tables.LevelEntity(tp.MapId, tp.TeleportEntityConfigId)
	if row == nil || len(row.Transform) == 0 || row.Transform[0] == nil {
		return &protocol.TeleportTransferResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrTeleportCreatureIdNotExist}
	}
	pos, facing := LandingPoint(row)
	if p.Scene != nil && tp.MapId == p.Scene.InstanceID {
		p.Teleport(pos, &facing)
	} else {
		p.ChangeScene(tp.MapId, &pos)
	}
	return &protocol.TeleportTransferResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MapId: tp.MapId, PosX: pos.X, PosY: pos.Y, PosZ: pos.Z}
}

// Teleport is a move within the scene (TeleportNotify); false when not in a scene.
func (p *PlayerSession) Teleport(pos world.Vec3, facing *float32) bool {
	s := p.Scene
	if s == nil {
		return false
	}
	yaw := s.PlayerRot.Yaw
	if facing != nil {
		yaw = *facing
	}
	s.PlayerPos = pos
	s.PlayerRot = world.Rot{Yaw: yaw}
	// The client reads Rotator(Pitch = Y, Yaw = Z, Roll = X).
	p.Client.Push(&protocol.TeleportNotify{MapId: s.InstanceID, Position: pos.ToPb(), Rotation: &protocol.Vector{X: 0, Y: 0, Z: yaw}})
	return true
}

// ChangeSceneAfterReply changes the scene once the current request's reply went out.
func (p *PlayerSession) ChangeSceneAfterReply(instanceID int32) {
	p.After(func() { p.ChangeScene(instanceID, nil) })
}

// EnterInst (specs/dungeons.md §3.2, movement.md §4.3): the systems' dungeons, else only the
// open-world return; the reply goes out, then the transfer.
func (p *PlayerSession) EnterInst(req *protocol.EnterInstRequest) *protocol.EnterInstResponse {
	if r, handled := p.Systems.EnterInst(p, req); handled {
		return r
	}
	notFound := &protocol.EnterInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound}
	d, ok := p.Data.Tables.InstanceDungeonById[req.InstanceId]
	if !ok {
		return notFound
	}
	if !world.HasWorldSpawns(req.InstanceId) {
		common.Warn("scene", fmtInt("EnterInstRequest to instance %d: not an open-world instance, refused until R-143h", req.InstanceId))
		return notFound
	}
	var target world.Vec3
	if len(d.BornPosition) >= 3 {
		target = world.Vec3{X: float32(d.BornPosition[0]), Y: float32(d.BornPosition[1]), Z: float32(d.BornPosition[2])}
	}
	if req.PosEntityId != 0 {
		if row := p.Data.Tables.LevelEntity(d.MapConfigId, req.PosEntityId); row != nil && len(row.Transform) > 0 && row.Transform[0] != nil {
			t := row.Transform[0]
			target = world.Vec3{X: float32(t.X) / 100, Y: float32(t.Y) / 100, Z: float32(t.Z) / 100}
		}
	}
	if p.Scene != nil && p.Scene.InstanceID == req.InstanceId {
		return &protocol.EnterInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	}
	p.After(func() { p.ChangeScene(req.InstanceId, &target) })
	return &protocol.EnterInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ChangeScene (specs/movement.md §4.4): leave, record the new instance, join it.
func (p *PlayerSession) ChangeScene(instanceID int32, target *world.Vec3) {
	p.Client.Push(&protocol.LeaveSceneNotify{PlayerId: p.State.PlayerID(), SceneId: "", TransitionOption: &protocol.TransitionOptionPb{}})
	p.State.LastScene = instanceID
	p.State.SaveLastScene()
	p.JoinScene(instanceID, target, true)
}

// Vehicle is VehicleRequest (specs/movement.md §5, quests.md §4.7).
func (p *PlayerSession) Vehicle(req *protocol.VehicleRequest) *protocol.VehicleResponse {
	var v *world.Entity
	if p.Scene != nil {
		v = p.Scene.Get(req.EntityId)
	}
	if v == nil || !v.IsVehicle {
		// R-99: the client got off a quest vehicle by itself.
		if !req.IsGetOn && p.Systems.VehicleLeftByClient(p, req.EntityId) {
			return &protocol.VehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
		}
		return &protocol.VehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrVehicleEntityNotExist}
	}
	v.Occupied = req.IsGetOn
	if req.IsGetOn {
		if r := p.currentRole(); r != nil {
			v.Pos, v.Rot = r.Pos, r.Rot
		}
	}
	return &protocol.VehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (p *PlayerSession) EnterVehicle(req *protocol.EntityEnterVehicleRequest) *protocol.EntityEnterVehicleResponse {
	var v *world.Entity
	if p.Scene != nil {
		v = p.Scene.Get(req.VehicleId)
	}
	if v == nil || !v.IsVehicle {
		return &protocol.EntityEnterVehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrVehicleEntityNotExist}
	}
	v.Occupied = req.IsGetOn
	return &protocol.EntityEnterVehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// SplineMove (specs/quests.md §4.7, R-99): a quest vehicle the player rides moves along its
// spline; the vehicle and the player are where the client says.
func (p *PlayerSession) SplineMove(req *protocol.SplineMoveRequest) *protocol.SplineMoveResponse {
	if s := p.Scene; req.Pos != nil && s != nil && p.Systems.VehicleRidden(p, req.EntityId) {
		at := world.Vec3From(req.Pos)
		id := req.EntityId
		p.After(func() {
			if v := s.Get(id); v != nil {
				v.Pos = at
			}
			s.PlayerPos = at
			p.AfterMove()
		})
	}
	return &protocol.SplineMoveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (p *PlayerSession) currentRole() *world.Entity {
	if p.Scene == nil {
		return nil
	}
	if id, ok := p.Scene.RoleEntities.Get(p.Formation().CurRole); ok {
		return p.Scene.Get(id)
	}
	return nil
}

// ----- level entities in combat -----

// RegisterLevelEntity puts an AI world spawn into combat: its attributes, parts and state
// machine come from combat (combat.md §3, §9.6), and damage, kills and drops find it. Its
// AttributeComponent is rebuilt from combat's state whenever the entity is sent. openWorld: a
// world spawn (it respawns after 3 minutes); prefab monsters do not.
func (p *PlayerSession) RegisterLevelEntity(e *world.Entity, row *data.LevelEntityConfig, openWorld bool) {
	kind := e.EntityType
	if kind != world.TypeMonster && kind != world.TypeAnimal && kind != world.TypeNpc && kind != world.TypeVision {
		return
	}
	bp := p.Data.Blueprint(row.BlueprintType)
	var tpl data.Value
	if t := p.Data.Template(row.BlueprintType); t != nil {
		tpl = t.ComponentsData
	}
	merged := world.Merge(row.ComponentsData, tpl)
	obj := &data.Object{}
	for k, v := range merged {
		obj.Keys = append(obj.Keys, k)
		obj.Values = append(obj.Values, v)
	}
	logic := ""
	var bpModel int32
	if bp != nil {
		logic, bpModel = bp.EntityLogic, bp.ModelId
	}
	attrs := combat.BuildMonsterAttributes(p.Data.Tables, obj, logic, row.AreaId, p.World.WorldLevel, nil)
	model := world.JSONIntOr(world.Field(world.Field(merged["ModelComponent"], "ModelType"), "ModelId"), bpModel)
	c := combat.NewCombatEntity(e.ID)
	c.ConfigID, c.ConfigType, c.EntityType = row.EntityId, e.ConfigType, kind
	c.ModelID, c.AreaID, c.MapID, c.IsOpenWorldSpawn = model, row.AreaId, row.MapId, openWorld
	c.RewardID = world.JSONIntOr(world.Field(merged["RewardComponent"], "RewardId"), 0)
	c.VisionCaptureID = world.JSONIntOr(world.Field(merged["VisionCaptureComponent"], "VisionCaptureId"), 0)
	c.Attributes = attrs
	c.SetPosition(e.Pos.X, e.Pos.Y, e.Pos.Z)
	attribute := merged["AttributeComponent"]
	hardness := world.JSONIntOr(world.Field(attribute, "HardnessModeId"), 0)
	rage := world.JSONIntOr(world.Field(attribute, "RageModeId"), 0)

	// The components entities.md §5.2 leaves to combat.
	index := func(match func(*protocol.EntityComponentPb) bool) int {
		for i, x := range e.Components {
			if match(x) {
				return i
			}
		}
		return -1
	}
	at := index(func(x *protocol.EntityComponentPb) bool { return x.GetAttributeComponent() != nil })
	tagAt := index(func(x *protocol.EntityComponentPb) bool { return x.GetTagComponent() != nil })
	if kind == world.TypeMonster {
		if part := combat.BuildPartComponent(p.Data.Tables, model, c.LifeMax(), nil); part != nil {
			c.PartComponent = part
			e.Components = insertAt(e.Components, tagAt+1, world.C(&protocol.EntityComponentPb_PartComponent{PartComponent: part}))
		}
	}
	if p.Fsm != nil {
		aiID := world.JSONIntOr(world.Field(merged["AiComponent"], "AiId"), 0)
		if fsm := p.Fsm.BuildForMonsterAI(aiID); fsm != nil {
			c.FsmComponent = fsm
			p.Combat.Combat.StartFsm(c, p.Fsm.GraphForMonsterAI(aiID), p.Clock.RealMs())
			stateAt := index(func(x *protocol.EntityComponentPb) bool { return x.GetStateTagComponentPb() != nil })
			if stateAt < 0 {
				stateAt = len(e.Components)
			}
			e.Components = insertAt(e.Components, stateAt, world.C(&protocol.EntityComponentPb_EntityFsmComponentPb{EntityFsmComponentPb: fsm}))
		}
	}
	// The tags it starts with, which the server's state machine conditions read (R-152).
	if tagAt >= 0 {
		for _, t := range e.Components[tagAt].GetTagComponent().GetGameplayTags() {
			for i := int32(0); i < t.TagCount; i++ {
				c.AdjustTag(t.Id, true)
			}
		}
	}
	p.Combat.Scene.Entities.Set(e.ID, c)
	// Built from the entity each time: a quest or an interaction changes its state tag.
	e.Live = func() *protocol.EntityPb {
		pb := e.BasePb()
		if at >= 0 {
			pb.ComponentPbs[at] = world.C(&protocol.EntityComponentPb_AttributeComponent{AttributeComponent: c.Attributes.ToAttributeComponentPb(hardness, rage)})
		}
		return pb
	}
}

func insertAt(cs []*protocol.EntityComponentPb, i int, c *protocol.EntityComponentPb) []*protocol.EntityComponentPb {
	cs = append(cs, nil)
	copy(cs[i+1:], cs[i:])
	cs[i] = c
	return cs
}

// ForgetLevelEntity: a world spawn left the scene; combat forgets it too.
func (p *PlayerSession) ForgetLevelEntity(id int64) {
	p.Combat.Scene.Entities.Delete(id)
	p.Combat.Scene.ActiveCombatants.Delete(id)
}
