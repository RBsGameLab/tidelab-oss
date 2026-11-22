// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/world"
	"tidelab/protocol"
)

func fmtInt(format string, a ...any) string { return fmt.Sprintf(format, a...) }

// Systems is where the quests and the systems of R-143h (stage R-144f) join a session. NoSystems
// is the session without quest data: what the C# server did with no Quests.json.
type Systems interface {
	// Hooks are the player's combat hooks (quest hooks on kills, skills, gender).
	Hooks(p *PlayerSession) combat.GameHooks
	// Entered runs after the state is loaded, before the login data (the quest engine starts).
	Entered(p *PlayerSession, reconnect bool)
	// OwnLoginPush is a login slot's message when combat did not fill it (nil: the session's own).
	OwnLoginPush(p *PlayerSession, slot string) proto.Message
	// LeavingScene runs before a scene join (R-89: the flows of the old scene end).
	LeavingScene(p *PlayerSession)
	// BeforeFormation runs before the formation's roles spawn (R-86: the tower's team goes back).
	BeforeFormation(p *PlayerSession, instanceID int32)
	// HasSpawns: the instance gets a world spawner (the open world; quest dungeons with quests).
	HasSpawns(p *PlayerSession, instanceID int32) bool
	// Candidates are the scene's spawn candidates (nil: the map's own).
	Candidates(p *PlayerSession, instanceID int32) []*world.Candidate
	// ConfigureSpawner adds the quest rules to a new spawner (hidden rows, prefabs, woken rows).
	ConfigureSpawner(p *PlayerSession, s *world.Spawner)
	// SyncOnJoin is the quest sync right after AfterJoinSceneNotify (specs/quests.md §5.2 step 4).
	SyncOnJoin(p *PlayerSession)
	// BeforeHeartbeat runs first at a heartbeat (the dev command file, specs/systems.md §7.1).
	BeforeHeartbeat(p *PlayerSession)
	// Tick is the quests' timers and a transfer they asked for; true when the scene changed.
	Tick(p *PlayerSession) bool
	// SpawnsQueued: prefab spawns wait for the next refresh.
	SpawnsQueued(p *PlayerSession) bool
	// AfterHeartbeat runs last at a heartbeat (new mail, quests.md §5.3 step 4).
	AfterHeartbeat(p *PlayerSession)
	// Moved reacts to the player's move; true when the scene changed.
	Moved(p *PlayerSession, pos world.Vec3) bool
	// EnterInst handles EnterInstRequest when the systems own dungeons; handled false: the
	// session's open-world path.
	EnterInst(p *PlayerSession, req *protocol.EnterInstRequest) (resp *protocol.EnterInstResponse, handled bool)
	// VehicleLeftByClient (R-99): the client got off a quest vehicle by itself.
	VehicleLeftByClient(p *PlayerSession, entityID int64) bool
	// VehicleRidden: a quest vehicle the player rides.
	VehicleRidden(p *PlayerSession, entityID int64) bool
}

// NoSystems is a session without quest data.
type NoSystems struct{}

func (NoSystems) Hooks(p *PlayerSession) combat.GameHooks           { return sessionHooks{p} }
func (NoSystems) Entered(*PlayerSession, bool)                      {}
func (NoSystems) OwnLoginPush(*PlayerSession, string) proto.Message { return nil }
func (NoSystems) LeavingScene(*PlayerSession)                       {}
func (NoSystems) BeforeFormation(*PlayerSession, int32)             {}
func (NoSystems) HasSpawns(_ *PlayerSession, instanceID int32) bool {
	return world.HasWorldSpawns(instanceID)
}
func (NoSystems) Candidates(*PlayerSession, int32) []*world.Candidate { return nil }
func (NoSystems) ConfigureSpawner(*PlayerSession, *world.Spawner)     {}
func (NoSystems) BeforeHeartbeat(*PlayerSession)                      {}
func (NoSystems) Tick(*PlayerSession) bool                            { return false }
func (NoSystems) SpawnsQueued(*PlayerSession) bool                    { return false }
func (NoSystems) AfterHeartbeat(*PlayerSession)                       {}
func (NoSystems) Moved(*PlayerSession, world.Vec3) bool               { return false }
func (NoSystems) VehicleLeftByClient(*PlayerSession, int64) bool      { return false }
func (NoSystems) VehicleRidden(*PlayerSession, int64) bool            { return false }

func (NoSystems) EnterInst(*PlayerSession, *protocol.EnterInstRequest) (*protocol.EnterInstResponse, bool) {
	return nil, false
}

// SyncOnJoin without quest data: the empty sync of R-143f.
func (NoSystems) SyncOnJoin(p *PlayerSession) {
	p.Client.Push(&protocol.QuestFinishListNotify{})
	p.Client.Push(&protocol.QuestListNotify{})
	p.Client.Push(&protocol.BehaviorTreeInfoNotify{})
	p.Client.Push(&protocol.QuestReadyListNotify{})
}

// sessionHooks are the session's combat hooks without quest data: only the formation sync
// (C#'s PlayerSession is the hooks whether or not quests run).
type sessionHooks struct{ p *PlayerSession }

func (sessionHooks) OnMonsterKilled(*combat.CombatEntity) {}
func (sessionHooks) OnUseSkill(int64, int64)              {}
func (sessionHooks) OnGenderChanged(bool)                 {}
func (h sessionHooks) OnSceneSaved()                      { h.p.SyncFormationEntities() }
