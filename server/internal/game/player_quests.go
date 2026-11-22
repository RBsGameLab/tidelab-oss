// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/gateway"
	"tidelab/internal/quests"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// PlayerQuests holds a player's quest state for the life of the server: the quest engine, the
// flow queue, flow actions, options, hide groups, 机关组, presentation, vehicles, prefabs, and
// spawner jobs. It outlives gateway sessions (a reconnect keeps queued flows and timers;
// specs/quests.md §5.1) and points at the player's current PlayerSession on each Enter.
// Ported from our C# Game/PlayerQuests.cs.
type PlayerQuests struct {
	// Data is the quest data set (R-160c: the 深塔 floor prefabs the panel's swaps read).
	Data         *quests.QuestDataSet
	Engine       *quests.Engine
	Flows        *FlowQueue
	FlowActions  *FlowActions
	Options      *quests.FlowOptions
	HideGroups   *quests.HideGroups
	Groups       *EntityGroups
	Presentation *quests.Presentation
	Vehicles     *quests.Vehicles
	Prefabs      *quests.PrefabGroups
	Spawners     *quests.SpawnerJobs
	Dungeons     *Dungeons
	Tower        *Tower

	dungeonPlays map[int32]int32
	host         *questHost
}

func NewPlayerQuests(catalog *quests.Catalog, files *save.PlayerFiles) *PlayerQuests {
	q := &PlayerQuests{
		Data:         catalog.Data,
		Flows:        NewFlowQueue(),
		FlowActions:  NewFlowActions(catalog.Data.FlowActions),
		Options:      quests.NewFlowOptions(),
		HideGroups:   quests.NewHideGroups(catalog.Data.HideGroups, files),
		Groups:       NewEntityGroups(),
		Presentation: quests.NewPresentation(),
		Vehicles:     quests.NewVehicles(),
		Prefabs:      quests.NewPrefabGroups(catalog.Data.Prefabs, catalog.Data.PrefabOrder, catalog.Data.TowerFloors),
		Spawners:     quests.NewSpawnerJobs(),
		Dungeons:     NewDungeons(files),
		Tower:        NewTower(files),
		dungeonPlays: catalog.Data.DungeonPlays,
		host:         &questHost{files: files},
	}
	q.Engine = quests.NewEngine(catalog, q.host, &questBridge{q: q})
	q.Engine.Load()
	return q
}

func (q *PlayerQuests) Session() *PlayerSession {
	if q.host.session == nil {
		panic("quests: PlayerQuests has no session")
	}
	return q.host.session
}

func (q *PlayerQuests) SetSession(s *PlayerSession) {
	q.host.session = s
	q.Tower.Pick = func() int32 {
		season, _ := s.Data.Panel.TowerSeason()
		return season
	}
}

func (q *PlayerQuests) IsDungeonEntered(instanceID int32) bool {
	return q.Dungeons.IsEntered(instanceID)
}

// LeaveVehicle ends the player's quest vehicle ride (R-99).
func (q *PlayerQuests) LeaveVehicle(exit protocol.ExitVehicleType) {
	s := q.Session()
	if n := q.Vehicles.Leave(s.State.PlayerID(), exit); n != nil {
		s.Sink().Push(n)
	}
}

// StartFlow sends a flow through the queue, waking its actors first (every FlowStartNotify the server sends).
func (q *PlayerQuests) StartFlow(n *protocol.FlowStartNotify) {
	s := q.Session()
	q.FlowActions.Started(s, n)
	q.Flows.Send(n, s.Sink())
}

// ----- quests.Host implementation -----

type questHost struct {
	files   *save.PlayerFiles
	session *PlayerSession
}

func (h *questHost) s() *PlayerSession {
	if h.session == nil {
		panic("quests: host has no session")
	}
	return h.session
}

func (h *questHost) Push(msg proto.Message) { h.s().Sink().Push(msg) }
func (h *questHost) PlayerID() int32        { return h.files.PlayerID }

func (h *questHost) InstanceID() int32 {
	s := h.s()
	if s.Scene != nil {
		return s.Scene.InstanceID
	}
	return s.State.LastScene
}

func (h *questHost) MapID() int32 {
	s := h.s()
	if s.Scene != nil {
		return s.Scene.MapID
	}
	return s.Data.MapOf(s.State.LastScene)
}

func (h *questHost) NowMs() int64                        { return h.s().Clock.Ms() }
func (h *questHost) NowSeconds() int64                   { return h.s().Clock.Seconds() }
func (h *questHost) Tables() *data.DataTables            { return h.s().Data.Tables }
func (h *questHost) World() *save.WorldState             { return h.s().World.State }
func (h *questHost) Files() *save.PlayerFiles            { return h.files }
func (h *questHost) NetIDOf(id int64) (int64, bool)      { return h.s().Actions().NetIDOf(id) }
func (h *questHost) SpawnedState(id int64) (int32, bool) { return h.s().Actions().SpawnedState(id) }

func (h *questHost) HealthPercent(configID int64) (int32, bool) {
	s := h.s()
	net, ok := s.Actions().NetIDOf(configID)
	if !ok {
		return 0, false
	}
	e := s.Combat.Scene.GetEntity(net)
	if e == nil || e.LifeMax() <= 0 {
		return 0, false
	}
	return int32(int64(e.Life()) * 100 / int64(e.LifeMax())), true
}

func (h *questHost) RefreshSpawns() { h.s().MarkSpawns() }
func (h *questHost) ItemCount(itemID int32) int32 {
	return h.s().ItemCount(itemID)
}
func (h *questHost) GrantItem(itemID, count int32) {
	h.s().Actions().GrantItem(itemID, count, false)
}
func (h *questHost) TakeItem(itemID, count int32) {
	c := count
	h.s().Actions().TakeItem(itemID, &c)
}
func (h *questHost) GrantDrop(dropID, questID int32) {
	h.s().Actions().GrantDrop(dropID, int64(questID), 0, nil)
}
func (h *questHost) RunActions(actions []quests.QuestAction) error {
	h.s().Actions().RunDetached(actions)
	return nil
}
func (h *questHost) PushClientRange(actions []quests.QuestAction, start, end int, ctx *protocol.GameCtxPb) {
	h.s().Actions().PushClientRange(len(actions), start, end, ctx)
}
func (h *questHost) EntityPos(configID int64) ([3]float32, bool) {
	return h.s().Actions().EntityPos(configID)
}
func (h *questHost) HideEntity(configID int64) { h.s().Actions().HideEntity(configID) }
func (h *questHost) QueueTransfer(instanceID int32, pos [3]float32, force bool) {
	h.s().QueueTransfer(instanceID, pos, force)
}

// ----- quests.Systems implementation -----

type questBridge struct{ q *PlayerQuests }

func (b *questBridge) s() *PlayerSession                     { return b.q.Session() }
func (b *questBridge) p() *quests.Presentation               { return b.q.Presentation }
func (b *questBridge) StartFlow(f *protocol.FlowStartNotify) { b.q.StartFlow(f) }

func (b *questBridge) MovePlayerAction(run quests.RunRef, a quests.QuestAction) bool {
	v := b.q.Vehicles
	if a.Name == "VehicleEnter" {
		e, ok := quests.ParseVehicleEnter(a)
		if !ok {
			v.Stats.NotDone++
			return true
		}
		if v.Current != nil && v.Current.Vehicle != e.Vehicle {
			b.q.LeaveVehicle(protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal)
		}
		var netPtr *int64
		if net, ok := b.s().Actions().NetIDOf(e.Vehicle); ok {
			netPtr = &net
		}
		if n := v.Board(e, b.s().State.PlayerID(), netPtr); n != nil {
			b.s().Sink().Push(n)
		} else {
			b.s().MarkSpawns()
		}
		note := ""
		if v.Announced == nil {
			note = ", once it spawns"
		}
		common.Info("quest", fmt.Sprintf("quest %d: the player boards %d (seat %d)%s", run.TreeID, e.Vehicle, e.Seat, note))
		return true
	}
	if a.Name == "VehicleExitPlayer" {
		if v.Current != nil {
			common.Info("quest", fmt.Sprintf("quest %d: the player leaves %d", run.TreeID, v.Current.Vehicle))
		}
		b.q.LeaveVehicle(quests.VehicleExitType(a))
		return true
	}
	return false
}

func (b *questBridge) LeaveVehicle() {
	b.q.LeaveVehicle(protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal)
}

func (b *questBridge) CreatePrefab(runKey int64, varName string, prefabID int32, posEntityID int64, levelPlay bool) {
	s := b.s()
	var inst int32
	if s.Scene != nil {
		inst = s.Scene.InstanceID
	}
	t := s.Data.Tables
	inTower := false
	if d, ok := t.InstanceDungeonById[inst]; ok && levelPlay {
		inTower = d.InstSubType == TowerDungeonSubType
	}
	if inTower {
		var floor int32
		if f := b.q.Tower.FloorOfDungeon(t, inst); f != nil {
			floor = f.Id
		}
		if own, ok := b.q.Prefabs.FloorPrefabOf(floor); ok {
			if b.q.Prefabs.CreateFloor(runKey, varName, own, posEntityID) != nil {
				return
			}
		}
		waves := b.q.Tower.Waves(s.Data, func(bp string) bool {
			_, ok := b.q.Prefabs.TowerEntityOf(bp)
			return ok
		}, inst)
		if len(waves) > 0 {
			b.q.Prefabs.CreateWaves(runKey, varName, prefabID, posEntityID, waves)
			return
		}
	}
	b.q.Prefabs.Create(runKey, varName, prefabID, posEntityID)
}

func (b *questBridge) HasPrefab(runKey int64, varName string) bool {
	return b.q.Prefabs.Has(runKey, varName)
}

func (b *questBridge) PrefabGroupCleared(runKey int64, varName string) (bool, bool) {
	return b.q.Prefabs.GroupCleared(runKey, varName)
}

func (b *questBridge) StartSpawners(runKey int64, node int, creators []int64, nowMs int64, wake *[]int64) bool {
	s := b.s()
	var mapID int32
	if s.Scene != nil {
		mapID = s.Scene.MapID
	}
	var lists [][]quests.SpawnWave
	for _, id := range creators {
		if id > 0 && id <= math.MaxInt32 {
			if row := s.Data.Tables.LevelEntity(mapID, int32(id)); row != nil {
				if c, ok := row.ComponentsData.(*data.Object); ok && c != nil {
					if comp, ok := c.Get("SpawnMonsterComponent").(*data.Object); ok && comp != nil {
						lists = append(lists, quests.ParseSpawnWaves(comp))
						continue
					}
				}
			}
		}
		common.Warn("quest", fmt.Sprintf("run %x node %d: spawner %d has no SpawnMonsterComponent on map %d", runKey, node, id, mapID))
	}
	if len(lists) == 0 {
		return true
	}
	return b.q.Spawners.Start(quests.SpawnerKey{Run: runKey, Node: node}, lists, nowMs, wake)
}

func (b *questBridge) DropSpawner(runKey int64, node int) {
	b.q.Spawners.Drop(quests.SpawnerKey{Run: runKey, Node: node})
}

func (b *questBridge) SpawnerMentions(configID int64) bool {
	return b.q.Spawners.Mentions(configID)
}

func (b *questBridge) SpawnerKilled(configID, nowMs int64, wake *[]int64) (quests.SpawnerKey, bool) {
	return b.q.Spawners.OnKilled(configID, nowMs, wake)
}

func (b *questBridge) SpawnerTick(nowMs int64, wake *[]int64, cleared *[]quests.SpawnerKey) {
	b.q.Spawners.Tick(nowMs, wake, cleared)
}

func (b *questBridge) SpawnerNextDue() (int64, bool) { return b.q.Spawners.NextDue() }

func (b *questBridge) OnQuestTransfer(dungeonID int32) {
	b.q.Dungeons.OnQuestTransfer(dungeonID)
}

func (b *questBridge) LeaveDungeon() bool {
	return b.q.Dungeons.Leave(b.s())
}

func (b *questBridge) IsDungeonEntered(instanceID int32) bool {
	return b.q.IsDungeonEntered(instanceID)
}

func (b *questBridge) DungeonLevelPlay(dungeonID int32) (int32, bool) {
	if lp, ok := b.q.Tower.FloorPlay(b.s().Data.Tables, b.q.Prefabs, dungeonID); ok {
		return lp, true
	}
	lp, ok := b.q.dungeonPlays[dungeonID]
	return lp, ok
}

func (b *questBridge) CountdownRuns(levelPlayID int32, seconds float64) bool {
	s := b.s()
	towerFloor := s.Scene != nil && IsTowerFloorDungeon(s.Data.Tables, s.Scene.InstanceID)
	if towerFloor {
		b.q.Tower.StartClock(b.q.Engine.NowMs(), seconds)
	}
	return towerFloor || s.Activities().LordGyms.IsCurrentPlay(levelPlayID)
}

func (b *questBridge) OnLevelPlayFailed(levelPlayID int32) {
	s := b.s()
	s.Activities().LordGyms.OnLevelPlayFailed(s, levelPlayID)
	b.q.Tower.OnTimeUp(s, b.q.Engine.NowMs())
}

func (b *questBridge) OnLevelPlayFinished(levelPlayID int32) {
	s := b.s()
	s.Activities().LordGyms.OnLevelPlayFinished(s, levelPlayID)
}

func (b *questBridge) DropRunData(runKey int64) {
	b.q.Prefabs.DropRun(runKey)
	b.q.Spawners.DropRun(runKey)
}

func (b *questBridge) ResetVehicles() { b.q.Vehicles.Reset() }

func (b *questBridge) OnEntitySpawned(configID, netID int64) {
	if n := b.q.Vehicles.OnSpawned(configID, netID, b.s().State.PlayerID()); n != nil {
		b.s().Sink().Push(n)
	}
}

func (b *questBridge) StepActions(run quests.RunRef, actions []quests.QuestAction, ctx *protocol.GameCtxPb, _ bool) {
	for _, a := range actions {
		if a.Raw == nil {
			continue
		}
		if a.Name == "AddFlowInteractOption" {
			e, ok := quests.ParseFlowOption(run.Key, ctx, a.Raw)
			if !ok {
				common.Warn("quest", fmt.Sprintf("quest %d: AddFlowInteractOption not understood", run.TreeID))
				continue
			}
			b.q.Options.Add(e)
			common.Info("quest", fmt.Sprintf("quest %d: option %s on %d", run.TreeID, e.Guid, e.Entity))
			if net, ok := b.s().Actions().NetIDOf(e.Entity); ok {
				b.pushOption(e, net)
			}
		} else if a.Name == "RemoveFlowInteractOption" {
			if entity, flow, token, ok := quests.RemoveFlowOptionArgs(a.Raw); ok {
				b.unpush(b.q.Options.Remove(entity, flow, token))
			}
		}
	}
	for _, a := range actions {
		b.presentationAction(run, a)
	}
}

func (b *questBridge) presentationAction(run quests.RunRef, a quests.QuestAction) {
	s := b.s()
	p := b.p()
	switch a.Name {
	case "SetAudioState":
		group, ok1 := quests.ActionString(a, "AudioConfig", "Group")
		state, ok2 := quests.ActionString(a, "AudioConfig", "State")
		if !ok1 || !ok2 {
			return
		}
		s.Sink().Push(&protocol.AudioStateUpdateNotify{
			AudioStates: []*protocol.AudioStatePb{{
				TreeOwnerId: s.State.PlayerID(),
				TreeIncId:   run.Key,
				Group:       group,
				State:       state,
			}},
		})
		p.Stats.Audio++
	case "AddGuestCharacter":
		if guest, ok := quests.ActionInt(a, "GuestCharacterId"); ok && p.AddGuest(run.Key, int32(guest)) {
			s.Sink().Push(&protocol.AddGuestNotify{GuestId: int32(guest)})
		}
	case "RemoveGuestCharacter":
		gone, ok := quests.ActionInt(a, "GuestCharacterId")
		if !ok {
			return
		}
		p.RemoveGuest(int32(gone))
		s.Sink().Push(&protocol.RemoveGuestNotify{GuestId: int32(gone)})
	case "AddPlayBubble":
		guid, ok := quests.ActionString(a, "ActionGuid")
		if !ok {
			return
		}
		for _, entity := range quests.ActionInts(a, "EntityIds") {
			if p.AddBubble(run.Key, entity, guid) {
				b.pushBubble(quests.Bubble{Owner: run.Key, Entity: entity, Guid: guid}, true)
			}
		}
	case "ClearPlayBubble":
		for _, bub := range p.ClearBubbles(quests.ActionInts(a, "EntityIds")) {
			b.pushBubble(bub, false)
		}
	case "SetAreaTimeState":
		kind, ok := quests.ActionInt(a, "SetAreaTimeConfig", "Type")
		if !ok {
			return
		}
		if kind == 0 {
			hour, _ := quests.ActionInt(a, "SetAreaTimeConfig", "LockTime", "Hour")
			minute, _ := quests.ActionInt(a, "SetAreaTimeConfig", "LockTime", "Minutes")
			p.LockTime(run.Key, hour, minute)
			s.Sink().Push(&protocol.TimeLockInfoNotify{IsLock: true, Hour: hour, Minute: minute})
		} else {
			p.Time = nil
			s.Sink().Push(&protocol.TimeLockInfoNotify{IsLock: false})
		}
	case "HideSpecificEntities", "ShowSpecificEntities":
		b.specificEntities(run, a)
	case "SetEntityPos", "ResetEntityPos":
		b.entityPos(run, a)
	case "SetAreaState":
		area, ok1 := quests.ActionInt(a, "AreaId")
		st, ok2 := quests.ActionInt(a, "State")
		if !ok1 || !ok2 {
			return
		}
		on := st != 0
		if s.World.Areas.Set(int32(area), on) {
			s.World.Areas.Save()
			stateStr := "off"
			if on {
				stateStr = "on"
			}
			common.Info("quest", fmt.Sprintf("quest %d: area %d %s", run.TreeID, area, stateStr))
		}
		s.Sink().Push(&protocol.SceneAreaStateNotify{AreaState: &protocol.SceneAreaState{AreaId: int32(area), State: on}})
	case "SetWeather":
		weather, ok := quests.ActionInt(a, "WeatherId")
		if !ok {
			return
		}
		p.SetWeather(run.Key, int32(weather))
		s.Sink().Push(&protocol.WeatherNotify{WeatherId: int32(weather)})
	case "EnableSystem":
		system, ok := quests.ActionInt(a, "SystemType")
		if !ok {
			return
		}
		enable := true
		if v, ok := quests.ActionBool(a, "IsEnable"); ok {
			enable = v
		}
		p.SetEnabled(run.Key, quests.DisableSystem, int32(system), enable)
		b.pushSwitch(quests.DisableSystem, int32(system), enable, nil)
	case "EnableFunction":
		fnName, _ := quests.ActionString(a, "Type")
		typ := functionType(fnName)
		if typ == protocol.EnableFunctionType_EnableFunctionType_Invalid {
			return
		}
		mode, hasMode := quests.ActionString(a, "DisplayMode", "Type")
		fnEnable := !hasMode || mode != "DisableAll"
		if v, ok := quests.ActionBool(a, "Enable"); ok {
			fnEnable = v
		}
		p.SetEnabled(run.Key, quests.DisableFunction, int32(typ), fnEnable)
		b.pushSwitch(quests.DisableFunction, int32(typ), fnEnable, quests.ActionInts(a, "DungeonList"))
	}
}

func (b *questBridge) entityPos(run quests.RunRef, a quests.QuestAction) {
	id, ok := quests.ActionInt(a, "EntityId")
	if !ok {
		return
	}
	s := b.s()
	moves := s.World.Moves
	if a.Name == "SetEntityPos" {
		x, ok1 := quests.ActionFloat(a, "Pos", "X")
		y, ok2 := quests.ActionFloat(a, "Pos", "Y")
		z, ok3 := quests.ActionFloat(a, "Pos", "Z")
		if !ok1 || !ok2 || !ok3 {
			return
		}
		var yaw *float32
		if v, ok := quests.ActionFloat(a, "Rot", "Z"); ok {
			fv := float32(v)
			yaw = &fv
		} else if v, ok := quests.ActionFloat(a, "Pos", "A"); ok {
			fv := float32(v)
			yaw = &fv
		}
		moves.Move(id, float32(x), float32(y), float32(z), yaw)
		common.Info("quest", fmt.Sprintf("quest %d: entity %d moved to %.0f,%.0f,%.0f", run.TreeID, id, x, y, z))
	} else if !moves.Reset(id) {
		return
	}
	moves.Save()
	s.Actions().Respawn(id)
}

func functionType(name string) protocol.EnableFunctionType {
	switch name {
	case "TeleportDungeon":
		return protocol.EnableFunctionType_EnableFunctionType_TeleportDungeon
	case "MotorRadioUI":
		return protocol.EnableFunctionType_EnableFunctionType_MotorRadioUi
	case "InteractiveItems":
		return protocol.EnableFunctionType_EnableFunctionType_IInteractiveItems
	default:
		return protocol.EnableFunctionType_EnableFunctionType_Invalid
	}
}

func (b *questBridge) pushSwitch(kind quests.DisableKind, id int32, enable bool, param []int64) {
	if kind == quests.DisableSystem {
		b.s().Sink().Push(&protocol.UpdateEnableSystemNotify{
			FuncInfo: []*protocol.EnableSystemFuncInfo{{FuncId: id, IsOpen: enable}},
		})
		return
	}
	info := &protocol.EnableFunctionInfo{
		Type:     protocol.EnableFunctionType(id),
		IsEnable: enable,
	}
	if len(param) > 0 {
		ints := make([]int32, len(param))
		for i, v := range param {
			ints[i] = int32(v)
		}
		info.Param = &protocol.EnableFunctionInfo_FBParamList{
			FBParamList: &protocol.EnableFBParam{ParamIntList: ints},
		}
	}
	b.s().Sink().Push(&protocol.UpdateEnableFunctionNotify{FuncInfo: []*protocol.EnableFunctionInfo{info}})
}

func (b *questBridge) pushBubble(bub quests.Bubble, add bool) {
	net, ok := b.s().Actions().NetIDOf(bub.Entity)
	if !ok {
		return
	}
	if add {
		b.s().Sink().Push(&protocol.EntityAddBubbleNotify{EntityId: net, ActionGuid: bub.Guid})
	} else {
		b.s().Sink().Push(&protocol.EntityRemoveBubbleNotify{EntityId: net, ActionGuid: bub.Guid})
	}
}

func (b *questBridge) OnSceneJoin() {
	s := b.s()
	p := b.p()
	for _, g := range p.Guests() {
		s.Sink().Push(&protocol.AddGuestNotify{GuestId: g.ID})
	}
	for _, d := range p.DisabledList() {
		b.pushSwitch(d.Kind, d.ID, false, nil)
	}
	if p.Time != nil {
		s.Sink().Push(&protocol.TimeLockInfoNotify{IsLock: true, Hour: p.Time.Hour, Minute: p.Time.Minute})
	}
	if p.CurrentWeather != nil {
		s.Sink().Push(&protocol.WeatherNotify{WeatherId: p.CurrentWeather.ID, IsClient: true})
	}
}

func (b *questBridge) specificEntities(run quests.RunRef, a quests.QuestAction) {
	hide := a.Name == "HideSpecificEntities"
	var mapID int32
	if b.s().Scene != nil {
		mapID = b.s().Scene.MapID
	}
	kind := byte('q')
	if run.LevelPlay {
		kind = 'l'
	}
	n := 0
	for _, id := range quests.ActionInts(a, "EntityIds") {
		if hide {
			if b.q.HideGroups.HideSpecific(kind, run.TreeID, mapID, id) {
				n++
			}
		} else {
			n += b.q.HideGroups.ShowSpecific(mapID, id)
		}
	}
	what := "shown again"
	if hide {
		what = "hidden"
	}
	common.Info("quest", fmt.Sprintf("quest %d: %d entities %s", run.TreeID, n, what))
	if hide {
		b.s().RemoveHidden()
	} else {
		b.s().MarkSpawns()
	}
}

func (b *questBridge) RetainClearings(ownerRunning func(kind byte, id int32) bool) {
	released := b.q.HideGroups.Retain(ownerRunning)
	if released == 0 {
		return
	}
	b.q.HideGroups.Save()
	common.Info("quest", fmt.Sprintf("%d hidden groups of ended quests and plays released", released))
}

func (b *questBridge) PickFlowOption(entityConfigID int64, guid string) (*protocol.FlowStartNotify, bool) {
	e, ok := b.q.Options.Find(entityConfigID, guid)
	if !ok {
		return nil, false
	}
	b.q.Options.Stats.Picked++
	net, _ := b.s().Actions().NetIDOf(e.Entity)
	picked := quests.PickedFlowOptionCtx(e, net)
	common.Info("quest", fmt.Sprintf("quest option %s on %d picked", e.Guid, e.Entity))
	if len(e.Actions) > 0 {
		b.s().Actions().RunAsEntity(e.Entity, e.Actions)
		b.s().Actions().PushClientActions(e.Actions, picked)
	}
	f := e.Flow
	if f == nil {
		f = playFlowOfActions(e.Actions)
	}
	if f == nil {
		return nil, true
	}
	return &protocol.FlowStartNotify{
		FlowListName: f.FlowListName,
		FlowId:       f.FlowID,
		StateId:      f.StateID,
		GameCtx:      picked,
	}, true
}

func playFlowOfActions(actions []quests.QuestAction) *quests.FlowRef {
	for _, a := range actions {
		if a.Name == "PlayFlow" && a.Params != nil && a.Params.Flow != nil && !a.Params.Flow.IsEmpty() {
			return a.Params.Flow
		}
	}
	return nil
}

func (b *questBridge) ReleaseRun(runKey int64, levelPlay bool, treeID int32) {
	kind := byte('q')
	if levelPlay {
		kind = 'l'
	}
	b.q.HideGroups.Release(kind, treeID)
	b.unpush(b.q.Options.RemoveOwner(runKey))
	left := b.p().Release(runKey)
	if !left.Any() {
		return
	}
	for _, bub := range left.Bubbles {
		b.pushBubble(bub, false)
	}
	for _, id := range left.Guests {
		b.s().Sink().Push(&protocol.RemoveGuestNotify{GuestId: id})
	}
	for _, d := range left.Disabled {
		b.pushSwitch(d.Kind, d.ID, true, nil)
	}
	if left.UnlockTime {
		b.s().Sink().Push(&protocol.TimeLockInfoNotify{IsLock: false})
	}
	if left.ClearWeather {
		b.s().Sink().Push(&protocol.WeatherNotify{WeatherId: quests.WeatherSunny})
	}
	extra := ""
	if left.UnlockTime {
		extra += ", time unlocked"
	}
	if left.ClearWeather {
		extra += ", weather back to sunny"
	}
	common.Info("quest", fmt.Sprintf("run %d ended: %d bubbles, %d guests, %d switches undone%s",
		runKey, len(left.Bubbles), len(left.Guests), len(left.Disabled), extra))
}

func (b *questBridge) AnnounceOptions(configID, netID int64) {
	for _, e := range b.q.Options.All() {
		if e.Entity == configID {
			b.pushOption(e, netID)
		}
	}
	for _, bub := range b.p().Bubbles() {
		if bub.Entity == configID {
			b.s().Sink().Push(&protocol.EntityAddBubbleNotify{EntityId: netID, ActionGuid: bub.Guid})
		}
	}
}

func (b *questBridge) pushOption(e quests.FlowOptionEntry, net int64) {
	b.s().Sink().Push(&protocol.EntityAddDynamicInteractNotify{
		EntityId:   net,
		OptionGuid: e.Guid,
		GameCtx:    e.Source,
		Text:       e.Text,
	})
}

func (b *questBridge) unpush(gone []quests.FlowOptionEntry) {
	for _, e := range gone {
		if net, ok := b.s().Actions().NetIDOf(e.Entity); ok {
			b.s().Sink().Push(&protocol.EntityRemoveDynamicInteractNotify{EntityId: net, OptionGuid: e.Guid})
		}
	}
	if len(gone) > 0 {
		common.Info("quest", fmt.Sprintf("%d quest options removed", len(gone)))
	}
}

// ----- game.Systems & combat.GameHooks -----

// QuestSystems wires PlayerQuests into PlayerSession through game.Systems.
type QuestSystems struct{ NoSystems }

type questHooks struct{ p *PlayerSession }

func (h questHooks) OnMonsterKilled(monster *combat.CombatEntity) {
	p := h.p
	e := p.Quests()
	if e == nil {
		return
	}
	e.OnKill(int64(monster.ConfigID))
	p.Groups().OnEntityState(p, int64(monster.ConfigID))
	if p.QuestState != nil {
		if runKey, varName, ok := p.QuestState.Prefabs.OnKilled(monster.NetID); ok {
			e.OnGroupCleared(runKey, varName)
		}
	}
	if p.Scene != nil && e.Catalog().IsQuestOwned(p.Scene.MapID, int64(monster.ConfigID)) {
		p.World.State.Sleep(int64(monster.ConfigID))
		p.World.State.Save()
	}
}

func (h questHooks) OnUseSkill(entityNetID, skillID int64) {
	p := h.p
	e := p.Quests()
	if e == nil || entityNetID == 0 || skillID <= 0 || p.Scene == nil {
		return
	}
	e.OnUseSkill(skillID, [3]float32{p.Scene.PlayerPos.X, p.Scene.PlayerPos.Y, p.Scene.PlayerPos.Z})
}

func (h questHooks) OnGenderChanged(isMale bool) {
	if e := h.p.Quests(); e != nil {
		male := isMale
		e.PlayerMale = &male
	}
}

func (h questHooks) OnSceneSaved() { h.p.SyncFormationEntities() }

func (QuestSystems) Hooks(p *PlayerSession) combat.GameHooks { return questHooks{p: p} }

func (QuestSystems) Entered(p *PlayerSession, reconnect bool) {
	q := p.QuestState
	if q == nil {
		return
	}
	p.World.Quests = q.Engine
	q.SetSession(p)
	e := q.Engine
	e.InventoryVersion = func() int { return int(p.Combat.Player.NormalInventory.ChangeVersion()) }
	e.NewSession()
	if reconnect {
		return
	}
	e.RestorePending = true
	q.Flows.LoginReset()
}

// BeforeFormation is R-86: out of the tower, however the player left: their own team again
// (dungeons.md §5 step 1).
func (QuestSystems) BeforeFormation(p *PlayerSession, instanceID int32) {
	if q := p.QuestState; q != nil && q.Tower.RestoreFormation(p.Combat.Player, p.Data.Tables, instanceID) {
		save.SaveFormationInfo(p.Combat.Player)
	}
}

// EnterInst with quest data: the dungeon module enters the dungeon or returns to the open world.
func (QuestSystems) EnterInst(p *PlayerSession, r *protocol.EnterInstRequest) (*protocol.EnterInstResponse, bool) {
	if q := p.QuestState; q != nil {
		return p.enterInstWithQuests(q, r), true
	}
	return nil, false
}

func (QuestSystems) LeavingScene(p *PlayerSession) {
	if p.QuestState != nil {
		p.Flows().SceneChanged(p.Sink())
	}
}

func (QuestSystems) HasSpawns(p *PlayerSession, instanceID int32) bool {
	if world.HasWorldSpawns(instanceID) {
		return true
	}
	if q := p.QuestState; q != nil {
		if _, ok := q.Engine.Catalog().QuestDungeons[instanceID]; ok {
			return true
		}
		if q.IsDungeonEntered(instanceID) {
			return true
		}
	}
	return false
}

func (QuestSystems) Candidates(p *PlayerSession, instanceID int32) []*world.Candidate {
	q := p.QuestState
	if q == nil {
		return nil
	}
	e := q.Engine
	entered := q.IsDungeonEntered(instanceID)
	offers := e.OffersEnabled
	return p.Data.QuestCandidatesOf(instanceID, entered, offers, func() *world.CandidateRules {
		lpEntities := map[int64]bool{}
		if entered {
			if lp, ok := e.LevelPlayForDungeon(instanceID); ok {
				lpEntities = e.Catalog().LevelPlayEntities(lp)
			}
		}
		return &world.CandidateRules{
			Entered:           entered,
			OffersEnabled:     offers,
			LevelPlayEntities: lpEntities,
			Owned:             e.Catalog().IsQuestOwned,
			KillTarget:        e.Catalog().IsKillTarget,
			Referenced:        e.Catalog().ReferencedEntities(offers),
		}
	})
}

func (QuestSystems) ConfigureSpawner(p *PlayerSession, s *world.Spawner) {
	s.RowFor = p.RowForWoken
	s.AfterSpawn = func(e *world.Entity) {
		if eng := p.Quests(); eng != nil {
			eng.OnEntitySpawned(int64(e.ConfigID), e.ID)
		}
	}
	s.Hidden = p.HiddenByClearing
	s.BeforeCandidates = p.SpawnPrefabs
	s.MoreNetIDs = func() []int64 {
		if p.QuestState != nil {
			return p.QuestState.Prefabs.AliveNetIDs()
		}
		return nil
	}
}

func (QuestSystems) SyncOnJoin(p *PlayerSession) {
	e := p.Quests()
	if e == nil {
		(NoSystems{}).SyncOnJoin(p)
		return
	}
	male := p.Combat.Player.BasicInfo.Attributes.Sex == 1
	e.PlayerMale = &male
	e.SyncOnJoin()
	// §5.2 step 4c: the daily activity tasks (R-79).
	p.RefreshActivityTasks()
	// §5.2 step 4d: an entered dungeon starts its level play.
	p.QuestState.Dungeons.OnJoined(p)
}

func (QuestSystems) Tick(p *PlayerSession) bool {
	e := p.Quests()
	if e == nil {
		return false
	}
	e.Tick()
	return p.ApplyPendingTransfer()
}

func (QuestSystems) SpawnsQueued(p *PlayerSession) bool {
	return p.QuestState != nil && len(p.QuestState.Prefabs.Queue()) > 0
}

func (QuestSystems) Moved(p *PlayerSession, pos world.Vec3) bool {
	e := p.Quests()
	if e == nil {
		return false
	}
	e.OnPlayerMoved([3]float32{pos.X, pos.Y, pos.Z})
	return p.ApplyPendingTransfer()
}

func (QuestSystems) VehicleLeftByClient(p *PlayerSession, entityID int64) bool {
	return p.QuestState != nil && p.QuestState.Vehicles.LeftByClient(entityID)
}

func (QuestSystems) VehicleRidden(p *PlayerSession, entityID int64) bool {
	return p.QuestState != nil && p.QuestState.Vehicles.IsRidden(entityID)
}

// ----- PlayerSession quest & interaction methods (PlayerSession.Quests.cs) -----

type pendingTransfer struct {
	instance int32
	pos      world.Vec3
	force    bool
}

func (p *PlayerSession) Quests() *quests.Engine {
	if p.QuestState == nil {
		return nil
	}
	return p.QuestState.Engine
}

func (p *PlayerSession) Flows() *FlowQueue {
	if p.QuestState != nil {
		return p.QuestState.Flows
	}
	if p.flows == nil {
		p.flows = NewFlowQueue()
	}
	return p.flows
}

func (p *PlayerSession) Groups() *EntityGroups {
	if p.QuestState != nil {
		return p.QuestState.Groups
	}
	if p.groups == nil {
		p.groups = NewEntityGroups()
	}
	return p.groups
}

func (p *PlayerSession) Actions() *EntityActions {
	if p.actions == nil {
		p.actions = NewEntityActions(p)
	}
	return p.actions
}

func (p *PlayerSession) MarkSpawns() {
	if p.Spawner != nil {
		p.Spawner.Marked = true
	}
}

func (p *PlayerSession) QueueTransfer(instanceID int32, pos [3]float32, force bool) {
	p.pendingTransfer = &pendingTransfer{
		instance: instanceID,
		pos:      world.Vec3{X: pos[0], Y: pos[1], Z: pos[2]},
		force:    force,
	}
}

func (p *PlayerSession) ApplyPendingTransfer() bool {
	if p.pendingTransfer == nil {
		return false
	}
	t := *p.pendingTransfer
	p.pendingTransfer = nil
	if p.Scene != nil && p.Scene.InstanceID == t.instance && !t.force {
		return false
	}
	common.Info("quest", fmt.Sprintf("transfer to instance %d at %.0f,%.0f,%.0f", t.instance, t.pos.X, t.pos.Y, t.pos.Z))
	p.ChangeScene(t.instance, &t.pos)
	return true
}

func (p *PlayerSession) RemoveHidden() {
	if p.Spawner == nil {
		return
	}
	removed := p.Spawner.RemoveHidden()
	if p.QuestState != nil {
		p.QuestState.HideGroups.Stats.Removed += removed
	}
	if removed > 0 {
		common.Info("hide_groups", fmt.Sprintf("hide groups: %d spawned entities removed", removed))
	}
}

func (p *PlayerSession) HiddenByClearing(configID int64, awake bool) bool {
	return p.QuestState != nil && p.Scene != nil && p.QuestState.HideGroups.IsHidden(p.Scene.MapID, configID, awake)
}

// IsDead reports whether configID was killed (waiting to respawn or corpse still around) or gone for good (R-80a).
func (p *PlayerSession) IsDead(configID int64) bool {
	if p.Scene != nil {
		if configID >= math.MinInt32 && configID <= math.MaxInt32 {
			if p.Combat.Scene.PendingRespawns.Has(combat.RespawnKey{MapID: p.Scene.MapID, ConfigID: int32(configID)}) {
				return true
			}
		}
		if p.Spawner != nil && p.Spawner.WaitsToRespawn(configID) {
			return true
		}
		for _, corpse := range p.Combat.Scene.DeadCorpses.Values() {
			if e := p.Combat.Scene.GetEntity(corpse.EntityNetID); e != nil && int64(e.ConfigID) == configID {
				return true
			}
		}
	}
	return p.World.State.IsGone(configID, p.Clock.Seconds())
}

func (p *PlayerSession) SubmitNode(r *protocol.SubmitNodeRequest) *protocol.SubmitNodeResponse {
	code := protocol.ErrorCode_ErrorCode_Success
	if e := p.Quests(); e != nil {
		code = e.SubmitNode(r.TreeIncId, r.NodeId)
	}
	p.After(func() { p.ApplyPendingTransfer() })
	p.After(p.ImmediateSpawnCheck)
	return &protocol.SubmitNodeResponse{TreeIncId: r.TreeIncId, ErrorId: code, NodeId: r.NodeId, TreeOwnerId: r.TreeOwnerId}
}

func (p *PlayerSession) HandIn(r *protocol.HandInItemRequest) *protocol.HandInItemResponse {
	code := protocol.ErrorCode_ErrorCode_Success
	if e := p.Quests(); e != nil {
		code = e.HandIn(r.TreeIncId, r.NodeId, r.HandInItem)
	}
	p.After(func() { p.ApplyPendingTransfer() })
	p.After(p.ImmediateSpawnCheck)
	return &protocol.HandInItemResponse{ErrorId: code}
}

func (p *PlayerSession) SubmitAwakeAndLoad(r *protocol.SubmitAwakeAndLoadEntityNodeRequest) *protocol.SubmitAwakeAndLoadEntityNodeResponse {
	code := protocol.ErrorCode_ErrorCode_Success
	if e := p.Quests(); e != nil {
		code = e.SubmitNode(r.TreeIncId, r.NodeId)
	}
	return &protocol.SubmitAwakeAndLoadEntityNodeResponse{ErrorCode: code}
}

func (p *PlayerSession) LevelPlayStateList(r *protocol.LevelPlayStateListRequest) *protocol.LevelPlayStateListResponse {
	resp := &protocol.LevelPlayStateListResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	e := p.Quests()
	for _, req := range r.InstLevelPlayStateReqs {
		rsp := &protocol.InstLevelPlayStateRsp{
			InstId:             req.InstId,
			LevelPlayStateDict: map[int32]int32{},
			CompleteNumberDict: map[int32]int32{},
			HideDict:           map[int32]bool{},
			IsUnlockDict:       map[int32]bool{},
		}
		for _, lp := range req.LevelPlayIds {
			var st int32
			if e != nil {
				st = e.LevelPlayStateOf(lp)
			}
			rsp.LevelPlayStateDict[lp] = st
			rsp.CompleteNumberDict[lp] = 0
			rsp.HideDict[lp] = false
			rsp.IsUnlockDict[lp] = true
		}
		resp.InstLevelPlayStateRsps = append(resp.InstLevelPlayStateRsps, rsp)
	}
	return resp
}

func (p *PlayerSession) configIDOf(netID int64, clamp bool) int64 {
	if p.Scene != nil {
		if e := p.Scene.Get(netID); e != nil {
			return int64(e.ConfigID)
		}
	}
	if clamp {
		if netID < 0 {
			return 0
		}
		if netID > math.MaxInt32 {
			return math.MaxInt32
		}
	}
	return netID
}

func (p *PlayerSession) interactOption(configID int64, index int32) *data.Object {
	if index < 0 || p.Scene == nil || configID > math.MaxInt32 || configID < math.MinInt32 {
		return nil
	}
	row := p.Data.Tables.LevelEntity(p.Scene.MapID, int32(configID))
	if row == nil {
		return nil
	}
	var tpl data.Value
	if t := p.Data.Template(row.BlueprintType); t != nil {
		tpl = t.ComponentsData
	}
	merged := world.Merge(row.ComponentsData, tpl)
	ic := merged["InteractComponent"]
	if ic == nil {
		return nil
	}
	options, ok := ic.Get("Options").(*data.Array)
	if !ok || options == nil || int(index) >= len(options.Items) {
		return nil
	}
	o, _ := options.Items[index].(*data.Object)
	return o
}

func (p *PlayerSession) EntityInteract(r *protocol.EntityInteractRequest) *protocol.EntityInteractResponse {
	configID := p.configIDOf(r.EntityId, true)
	option := p.interactOption(configID, r.OptionIndex)
	if len(p.Data.Tables.ShopFixed) > 0 {
		p.PushShopInfo()
	}
	p.After(func() { p.runInteractOption(r, configID, option) })
	return &protocol.EntityInteractResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Interacting: false}
}

func (p *PlayerSession) runInteractOption(r *protocol.EntityInteractRequest, configID int64, option *data.Object) {
	var kind *data.Object
	if option != nil {
		kind, _ = option.Get("Type").(*data.Object)
	}
	if kind != nil {
		p.Actions().RunOption(r.EntityId, configID, EntityActionsFromList(kind.Get("Actions")))
	}
	if kind != nil {
		if typ, ok := kind.Get("Type").(data.String); ok && string(typ) == "Flow" {
			if f, ok := kind.Get("Flow").(*data.Object); ok && f != nil {
				flow := quests.ParseFlowRef(f)
				if flow == nil {
					flow = &quests.FlowRef{}
				}
				if p.interactFlowInc == 0 {
					p.interactFlowInc = 100
				}
				p.interactFlowInc++
				n := &protocol.FlowStartNotify{
					FlowIncId:    p.interactFlowInc,
					FlowListName: flow.FlowListName,
					FlowId:       flow.FlowID,
					StateId:      flow.StateID,
				}
				common.Info("interact", fmt.Sprintf("entity %d: flow %s %d state %d (inc %d)",
					configID, flow.FlowListName, flow.FlowID, flow.StateID, n.FlowIncId))
				if p.QuestState != nil {
					p.QuestState.StartFlow(n)
				} else {
					p.Flows().Send(n, p.Sink())
				}
				p.ImmediateSpawnCheck()
				p.ApplyPendingTransfer()
				return
			}
		}
	}
	ctx := &protocol.GameCtxPb{
		CtxType: protocol.GameCtxType_GameCtxType_NormalInteract,
		CtxInfo: &protocol.GameCtxPb_NormalInteract{
			NormalInteract: &protocol.NormalInteractCtxPb{
				EntityCtx:   &protocol.EntityCtxPb{ConfigId: int32(configID), IncId: r.EntityId},
				OptionIndex: r.OptionIndex,
			},
		},
	}
	if kind != nil {
		if _, ok := kind.Get("Actions").(*data.Array); ok {
			p.Actions().PushClientActions(EntityActionsFromList(kind.Get("Actions")), ctx)
			p.ApplyPendingTransfer()
			return
		}
	}
	p.Sink().Push(&protocol.ActionNotify{
		PlayerId:      p.State.PlayerID(),
		IncId:         1,
		GameCtx:       ctx,
		TotalCount:    1,
		StartIndex:    0,
		EndIndex:      0,
		NeedFinishReq: false,
	})
	p.ApplyPendingTransfer()
}

func (p *PlayerSession) EntityDynamicInteract(r *protocol.EntityDynamicInteractRequest) *protocol.EntityDynamicInteractResponse {
	configID := p.configIDOf(r.EntityId, false)
	result := quests.DynamicInteractUnhandled
	if e := p.Quests(); e != nil {
		result = e.OnDynamicInteract(configID, r.OptionGuid)
	}
	p.After(p.ImmediateSpawnCheck)
	p.After(func() { p.ApplyPendingTransfer() })
	return &protocol.EntityDynamicInteractResponse{
		ErrorCode:   protocol.ErrorCode_ErrorCode_Success,
		Interacting: result == quests.DynamicInteractFlow,
	}
}

func (p *PlayerSession) ChangeEntityState(r *protocol.ChangeEntityStateRequest) *protocol.ChangeEntityStateResponse {
	if r.EntityId != 0 && r.StateTagId != 0 {
		id, tag := int64(r.EntityId), r.StateTagId
		p.After(func() {
			var netPtr *int64
			if net, ok := p.Actions().NetIDOf(id); ok {
				netPtr = &net
			}
			p.Actions().OnClientState(netPtr, id, tag)
		})
	}
	return &protocol.ChangeEntityStateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (p *PlayerSession) EntitySendEvent(r *protocol.EntitySendEventRequest) *protocol.EntitySendEventResponse {
	if p.Scene != nil {
		if e := p.Scene.Get(r.EntityId); e != nil {
			netID, cfgID, key := r.EntityId, int64(e.ConfigID), r.EventKey
			p.After(func() { p.Actions().OnEntityEvent(netID, cfgID, key) })
		}
	}
	return &protocol.EntitySendEventResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (p *PlayerSession) TargetGearHit(r *protocol.TargetGearHitRequest) *protocol.TargetGearHitResponse {
	if p.Scene != nil {
		if e := p.Scene.Get(r.EntityId); e != nil {
			netID, cfgID := r.EntityId, int64(e.ConfigID)
			p.After(func() { p.Actions().OnTargetGearHit(netID, cfgID) })
		}
	}
	return &protocol.TargetGearHitResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// elevatorStates are the elevator entity states of the motions the client reports: its
// GamePlayElevatorComponent.SyncElevatorStateToServer sends the target floor in tagState and
// Forward (up) / Reverse (down) / End (stopped) in state. Quests wait on these states
// (139000027 node 154: 109002461 "关卡.电梯.上升"), and the elevator's StateChangeBehaviors
// run on them (R-169).
var elevatorStates = map[protocol.ElevatorState]string{
	protocol.ElevatorState_ElevatorState_Forward: "关卡.电梯.上升",
	protocol.ElevatorState_ElevatorState_Reverse: "关卡.电梯.下降",
}

func (p *PlayerSession) ElevatorStateChange(r *protocol.ElevatorStateChangeRequest) *protocol.ElevatorStateChangeResponse {
	if name, ok := elevatorStates[r.State]; ok && p.Scene != nil {
		if e := p.Scene.Get(r.EntityId); e != nil {
			netID, cfgID, tag := r.EntityId, int64(e.ConfigID), world.TagID(p.Data.Tables, name)
			p.After(func() { p.Actions().OnClientState(&netID, cfgID, tag) })
		}
	}
	return &protocol.ElevatorStateChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (p *PlayerSession) FlowEnd(r *protocol.FlowEndRequest) *protocol.FlowEndResponse {
	p.Flows().SendEnded(r.FlowIncId, p.Sink())
	if p.QuestState != nil {
		p.QuestState.FlowActions.Ended(p, r.FlowIncId)
	}
	if e := p.Quests(); e != nil {
		e.OnFlowEnd(r.FlowIncId)
	}
	p.After(p.ImmediateSpawnCheck)
	p.After(func() { p.ApplyPendingTransfer() })
	return &protocol.FlowEndResponse{}
}

func (p *PlayerSession) FlowAction(r *protocol.FlowActionRequest) *protocol.FlowActionResponse {
	if p.QuestState != nil {
		p.QuestState.FlowActions.Run(p, r.FlowIncId, r.ActionId)
	}
	p.After(p.ImmediateSpawnCheck)
	return &protocol.FlowActionResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

// registerQuests registers the quest, world-interaction and flow requests on the gateway handler table.
func (g *WorldGame) registerQuests(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.SubmitNodeRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.SubmitNodeResponse { return p.SubmitNode(r) },
			&protocol.SubmitNodeResponse{ErrorId: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.HandInItemRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.HandInItemResponse { return p.HandIn(r) },
			&protocol.HandInItemResponse{ErrorId: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.SubmitAwakeAndLoadEntityNodeRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.SubmitAwakeAndLoadEntityNodeResponse { return p.SubmitAwakeAndLoad(r) },
			&protocol.SubmitAwakeAndLoadEntityNodeResponse{})
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.QuestReviewInfoRequest) proto.Message {
		return &protocol.QuestReviewInfoResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.SetFocusModeDeterCondRequest) proto.Message {
		return &protocol.SetFocusModeDeterCondResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.QuestRedDotRequest) proto.Message {
		return &protocol.QuestRedDotResponse{ErrorId: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.TraceQuestRequest) proto.Message {
		return &protocol.TraceQuestResponse{ErrorId: ok}
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.LevelPlayStateListRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.LevelPlayStateListResponse { return p.LevelPlayStateList(r) },
			&protocol.LevelPlayStateListResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.QuestBranchRequest) proto.Message {
		return &protocol.QuestBranchResponse{ErrorCode: ok, QuestBranchInfo: &protocol.QuestBranchInfo{}}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.LevelPlayVarRequest) proto.Message {
		return &protocol.LevelPlayVarResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.LevelPlayStateRequest) proto.Message {
		return &protocol.LevelPlayStateResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.ActionFinishRequest) proto.Message {
		return &protocol.ActionFinishResponse{Code: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.OpenSystemBoardResultRequest) proto.Message {
		return &protocol.OpenSystemBoardResultResponse{Code: ok}
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.EntityInteractRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.EntityInteractResponse { return p.EntityInteract(r) },
			&protocol.EntityInteractResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.EntityDynamicInteractRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.EntityDynamicInteractResponse { return p.EntityDynamicInteract(r) },
			&protocol.EntityDynamicInteractResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.ChangeEntityStateRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.ChangeEntityStateResponse { return p.ChangeEntityState(r) },
			&protocol.ChangeEntityStateResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.EntitySendEventRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.EntitySendEventResponse { return p.EntitySendEvent(r) },
			&protocol.EntitySendEventResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.TargetGearHitRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.TargetGearHitResponse { return p.TargetGearHit(r) },
			&protocol.TargetGearHitResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.ElevatorStateChangeRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.ElevatorStateChangeResponse { return p.ElevatorStateChange(r) },
			&protocol.ElevatorStateChangeResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.FlowEndRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.FlowEndResponse { return p.FlowEnd(r) },
			&protocol.FlowEndResponse{})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.FlowActionRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.FlowActionResponse { return p.FlowAction(r) },
			&protocol.FlowActionResponse{Code: ok})
	})
}
