// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"testing"

	"tidelab/internal/data"
	"tidelab/protocol"
)

func rawAct(t *testing.T, name, raw string) QuestAction {
	t.Helper()
	v, err := data.ParseJSON([]byte(raw))
	if err != nil {
		t.Fatalf("ParseJSON(%s): %v", name, err)
	}
	return QuestAction{Name: name, Raw: v}
}

func TestVehicleEnterShapes(t *testing.T) {
	named := rawAct(t, "VehicleEnter", `{"EnteringTarget":{"Type":"Player","TargetVehicle":915900222},"Seat":0}`)
	if r, ok := ParseVehicleEnter(named); !ok || r != (Ride{Vehicle: 915900222, Seat: 0}) {
		t.Fatalf("named = (%+v, %v)", r, ok)
	}
	appointed := rawAct(t, "VehicleEnter", `{"EnteringTarget":{"Type":"Player"},"Seat":1,"VehicleTarget":{"Type":"Appointed","VehicleId":120000037},"ForceExitLastVehicle":true}`)
	if r, ok := ParseVehicleEnter(appointed); !ok || r != (Ride{Vehicle: 120000037, Seat: 1}) {
		t.Fatalf("appointed = (%+v, %v)", r, ok)
	}
	npc := rawAct(t, "VehicleEnter", `{"EnteringTarget":{"Type":"Npc","TargetNpc":311900250,"TargetVehicle":311000479},"Seat":1}`)
	if _, ok := ParseVehicleEnter(npc); ok {
		t.Fatal("npc VehicleEnter should be ignored")
	}
	motor := rawAct(t, "VehicleEnter", `{"EnteringTarget":{"Type":"Player"},"Seat":0,"VehicleTarget":{"Type":"PlayerMotor"}}`)
	if _, ok := ParseVehicleEnter(motor); ok {
		t.Fatal("PlayerMotor should be ignored")
	}
	wait := QuestAction{Name: "Wait"}
	if r, ok := VehicleEnterIn([]QuestAction{wait, npc, named}); !ok || r != (Ride{Vehicle: 915900222, Seat: 0}) {
		t.Fatalf("VehicleEnterIn = (%+v, %v)", r, ok)
	}
	if _, ok := VehicleEnterIn([]QuestAction{wait, npc, motor}); ok {
		t.Fatal("VehicleEnterIn should return false when no player vehicle")
	}
}

func TestVehicleExitTypes(t *testing.T) {
	if got := VehicleExitType(rawAct(t, "VehicleExitPlayer", `{"ExitType":{"Type":"StandUp"}}`)); got != protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal {
		t.Fatalf("StandUp = %v", got)
	}
	if got := VehicleExitType(rawAct(t, "VehicleExitPlayer", `{"DestroyVehicle":true,"ExitType":{"Type":"Launch"}}`)); got != protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeLaunch {
		t.Fatalf("Launch = %v", got)
	}
	if got := VehicleExitType(rawAct(t, "VehicleExitPlayer", `{"ExitType":{"Type":"SeatStandUp"}}`)); got != protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeSeatStandUp {
		t.Fatalf("SeatStandUp = %v", got)
	}
}

func TestVehicleBoardingWaitsForVehicleAndSentOncePerSpawn(t *testing.T) {
	v := NewVehicles()
	if n := v.Board(Ride{Vehicle: 915900222, Seat: 0}, 777, nil); n != nil {
		t.Fatal("unspawned vehicle should send nothing yet")
	}
	if !v.RidesConfig(915900222) || v.IsRidden(5001) {
		t.Fatal("ride state mismatch")
	}
	if n := v.OnSpawned(915900223, 5000, 777); n != nil {
		t.Fatal("other entity should not trigger boarding")
	}
	n := v.OnSpawned(915900222, 5001, 777)
	if n == nil || n.EntityId != 5001 || n.SeatId != 0 || n.PlayerId != 777 {
		t.Fatalf("boarding notify = %+v", n)
	}
	if v.OnSpawned(915900222, 5001, 777) != nil {
		t.Fatal("duplicate spawn should not re-send")
	}
	if !v.IsRidden(5001) {
		t.Fatal("expected IsRidden(5001)")
	}
	o := v.Leave(777, protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal)
	if o == nil || o.EntityId != 5001 || o.SeatId != -1 {
		t.Fatalf("leave notify = %+v", o)
	}
	if v.Current != nil || v.Leave(777, protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal) != nil {
		t.Fatal("second leave should be nil")
	}
	if v.OnSpawned(915900222, 7001, 777) != nil {
		t.Fatal("spawn after leave should do nothing")
	}
	if v.Stats.Boarded != 1 || v.Stats.Left != 1 {
		t.Fatalf("stats = %+v", v.Stats)
	}
}

func TestVehicleNewSceneEndsRideWithoutNotify(t *testing.T) {
	v := NewVehicles()
	id := int64(5001)
	v.Board(Ride{Vehicle: 915900222, Seat: 0}, 777, &id)
	if !v.IsRidden(5001) {
		t.Fatal("expected IsRidden")
	}
	v.Reset()
	if v.IsRidden(5001) || v.OnSpawned(915900222, 6001, 777) != nil || v.Leave(777, protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal) != nil {
		t.Fatal("Reset should clear ride silently")
	}
}

func TestVehicleRespawnDoesNotSeatPlayerAgain(t *testing.T) {
	v := NewVehicles()
	id := int64(5001)
	v.Board(Ride{Vehicle: 915900161, Seat: 0}, 777, &id)
	if v.OnSpawned(915900161, 5001, 777) != nil || !v.IsRidden(5001) {
		t.Fatal("same netId should keep ride")
	}
	if v.OnSpawned(915900161, 5002, 777) != nil || v.Current != nil {
		t.Fatal("new netId respawn should end ride without boarding")
	}
	if v.OnSpawned(915900161, 5003, 777) != nil {
		t.Fatal("further spawns should do nothing")
	}
}

func TestVehicleClientReportsPlayerGotOff(t *testing.T) {
	v := NewVehicles()
	id := int64(5001)
	v.Board(Ride{Vehicle: 915900161, Seat: 0}, 777, &id)
	if v.LeftByClient(4000) || !v.IsRidden(5001) {
		t.Fatal("wrong netId should not end ride")
	}
	if !v.LeftByClient(5001) || v.Current != nil || v.LeftByClient(5001) || v.Stats.Left != 1 {
		t.Fatal("LeftByClient mismatch")
	}
}

func TestVehicleRideLeftBeforeClientKnewSendsNothing(t *testing.T) {
	v := NewVehicles()
	v.Board(Ride{Vehicle: 915900161, Seat: 0}, 777, nil)
	if v.Leave(777, protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeLaunch) != nil || v.Current != nil {
		t.Fatal("Leave before announce should return nil and clear Current")
	}
}

type vehicleSystems struct {
	NoSystems
	v    *Vehicles
	host *fakeQuestHost
}

func (s *vehicleSystems) ResetVehicles() { s.v.Reset() }

func (s *vehicleSystems) OnEntitySpawned(configID, netID int64) {
	if n := s.v.OnSpawned(configID, netID, s.host.PlayerID()); n != nil {
		s.host.Push(n)
	}
}

func (s *vehicleSystems) MovePlayerAction(_ RunRef, a QuestAction) bool {
	switch a.Name {
	case "VehicleEnter":
		if r, ok := ParseVehicleEnter(a); ok {
			var net *int64
			if id, spawned := s.host.NetIDOf(r.Vehicle); spawned {
				net = &id
			}
			if n := s.v.Board(r, s.host.PlayerID(), net); n != nil {
				s.host.Push(n)
			}
			return true
		}
	case "VehicleExitPlayer":
		if n := s.v.Leave(s.host.PlayerID(), VehicleExitType(a)); n != nil {
			s.host.Push(n)
		}
		return true
	}
	return false
}

func TestVehicleEngineRideReplayAfterLogin(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	v := NewVehicles()
	sys := &vehicleSystems{v: v, host: host}
	ds := NewQuestDataSet()
	ds.Quests = append(ds.Quests, QuestDef{
		ID: 12, Type: 1, Name: "十二", PreQuest: 1,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", "", 2, 3, 4),
			func() NodeDef {
				n := node(2, 1, "ChildQuest", "ReachArea")
				n.Enter = []QuestAction{rawAct(t, "VehicleEnter", `{"EnteringTarget":{"Type":"Player","TargetVehicle":600},"Seat":1}`)}
				return n
			}(),
			func() NodeDef {
				n := node(3, 1, "ChildQuest", "ReachArea")
				n.Enter = []QuestAction{rawAct(t, "VehicleExitPlayer", `{"ExitType":{"Type":"Launch"}}`)}
				return n
			}(),
			node(4, 1, "QuestSucceed", ""),
		},
	})
	engine := NewEngine(NewCatalog(ds), host, sys)
	engine.ParseState("F 1\nP 12 0:2:0:0:0 1:2:3:0:0\n")
	engine.RestorePending = true
	engine.SyncOnJoin()

	host.spawned[600] = 5600
	engine.OnPlayerMoved([3]float32{1, 2, 3})
	engine.Tick() // schedules replay at NowMs + 2000
	if len(pushesOf[*protocol.VehicleUpdateNotify](host)) != 0 {
		t.Fatal("replay should wait RideReplayDelayMs")
	}
	host.nowMs += RideReplayDelayMs + 500
	engine.Tick()
	notifies := pushesOf[*protocol.VehicleUpdateNotify](host)
	if len(notifies) != 1 || notifies[0].EntityId != 5600 || notifies[0].SeatId != 1 {
		t.Fatalf("expected one boarding notify, got %+v", notifies)
	}
	// Replay fires once only.
	host.nowMs += RideReplayDelayMs + 500
	engine.Tick()
	if len(pushesOf[*protocol.VehicleUpdateNotify](host)) != 1 {
		t.Fatal("ride replay should not fire twice")
	}
	// Advancing to step 3 takes the player off with Launch.
	engine.SubmitNode(12, 2)
	notifies = pushesOf[*protocol.VehicleUpdateNotify](host)
	if len(notifies) != 2 || notifies[1].SeatId != -1 || notifies[1].ExitType != protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeLaunch {
		t.Fatalf("expected Launch exit notify, got %+v", notifies)
	}
}
