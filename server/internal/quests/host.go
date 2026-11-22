// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// Host is what Engine asks of the player's world: the scene, the connection, the inventory,
// and the entity actions. Engine holds no scene of its own; the session provides this.
// Ported from IQuestHost in QuestHost.cs.
type Host interface {
	Push(msg proto.Message)

	PlayerID() int32
	// InstanceID is the instance the player is in (InstanceDungeon Id).
	InstanceID() int32
	// MapID is its map (InstanceDungeon MapConfigId).
	MapID() int32
	// NowMs is the awake clock in milliseconds.
	NowMs() int64
	// NowSeconds is the game clock in seconds (treasure and drop timers).
	NowSeconds() int64
	Tables() *data.DataTables
	World() *save.WorldState
	Files() *save.PlayerFiles

	// NetIDOf returns the net id of the spawned entity for configID, or ok=false.
	NetIDOf(configID int64) (int64, bool)
	// SpawnedState returns the state tag the spawned entity has now (ok=false: not spawned or stateless).
	SpawnedState(configID int64) (int32, bool)
	// HealthPercent returns a spawned monster's health in percent (ok=false when not spawned).
	HealthPercent(configID int64) (int32, bool)
	// RefreshSpawns marks that something woke or hid entities so the spawner looks again.
	RefreshSpawns()

	ItemCount(itemID int32) int32
	GrantItem(itemID, count int32)
	TakeItem(itemID, count int32)
	// GrantDrop awards a DropPackage reward for a quest or node.
	GrantDrop(dropID, questID int32)

	// RunActions executes the server half of an action list (entity states, wakes, teleports...).
	RunActions(actions []QuestAction) error
	// PushClientRange sends the client half of actions[start..end] (inclusive) as one LevelEventNotify range.
	PushClientRange(actions []QuestAction, start, end int, ctx *protocol.GameCtxPb)
	// EntityPos returns where entity configID is (spawned, moved, or its row on this map).
	EntityPos(configID int64) ([3]float32, bool)
	// HideEntity puts an awake entity back to sleep and removes it from the scene.
	HideEntity(configID int64)
	// QueueTransfer queues moving the player to another instance at pos (done after the current request).
	QueueTransfer(instanceID int32, pos [3]float32, force bool)
}

// RunRef describes a quest or level play run as other systems see it.
type RunRef struct {
	Key       int64
	TreeID    int32
	LevelPlay bool
}

// Systems is the quest engine's hook interface into sibling systems (flow queue, flow options,
// prefabs, spawner waves, clearings, presentation, vehicles, dungeons, tower, boss rush).
// Embed NoSystems to override only the hooks you implement. Ported from IQuestSystems in QuestHost.cs.
type Systems interface {
	StartFlow(flow *protocol.FlowStartNotify)
	StepActions(run RunRef, actions []QuestAction, ctx *protocol.GameCtxPb, fix bool)
	PickFlowOption(entityConfigID int64, guid string) (flow *protocol.FlowStartNotify, handled bool)
	ReleaseRun(runKey int64, levelPlay bool, treeID int32)
	OnSceneJoin()
	OnEntitySpawned(configID, netID int64)
	AnnounceOptions(configID, netID int64)
	RetainClearings(ownerRunning func(kind byte, id int32) bool)

	CreatePrefab(runKey int64, varName string, prefabID int32, posEntityID int64, levelPlay bool)
	HasPrefab(runKey int64, varName string) bool
	PrefabGroupCleared(runKey int64, varName string) (cleared bool, ok bool)

	StartSpawners(runKey int64, node int, creators []int64, nowMs int64, wake *[]int64) bool
	DropSpawner(runKey int64, node int)
	SpawnerMentions(configID int64) bool
	SpawnerKilled(configID int64, nowMs int64, wake *[]int64) (SpawnerKey, bool)
	SpawnerTick(nowMs int64, wake *[]int64, cleared *[]SpawnerKey)
	SpawnerNextDue() (int64, bool)
	DropRunData(runKey int64)

	MovePlayerAction(run RunRef, action QuestAction) bool
	LeaveVehicle()
	ResetVehicles()

	OnQuestTransfer(dungeonID int32)
	LeaveDungeon() bool
	IsDungeonEntered(instanceID int32) bool
	DungeonLevelPlay(dungeonID int32) (int32, bool)
	CountdownRuns(levelPlayID int32, seconds float64) bool
	OnLevelPlayFailed(levelPlayID int32)
	OnLevelPlayFinished(levelPlayID int32)
}

// NoSystems provides default no-op implementations for every Systems hook.
type NoSystems struct{}

func (NoSystems) StartFlow(*protocol.FlowStartNotify)                            {}
func (NoSystems) StepActions(RunRef, []QuestAction, *protocol.GameCtxPb, bool)   {}
func (NoSystems) PickFlowOption(int64, string) (*protocol.FlowStartNotify, bool) { return nil, false }
func (NoSystems) ReleaseRun(int64, bool, int32)                                  {}
func (NoSystems) OnSceneJoin()                                                   {}
func (NoSystems) OnEntitySpawned(int64, int64)                                   {}
func (NoSystems) AnnounceOptions(int64, int64)                                   {}
func (NoSystems) RetainClearings(func(byte, int32) bool)                         {}
func (NoSystems) CreatePrefab(int64, string, int32, int64, bool)                 {}
func (NoSystems) HasPrefab(int64, string) bool                                   { return false }
func (NoSystems) PrefabGroupCleared(int64, string) (bool, bool)                  { return false, false }
func (NoSystems) StartSpawners(int64, int, []int64, int64, *[]int64) bool        { return true }
func (NoSystems) DropSpawner(int64, int)                                         {}
func (NoSystems) SpawnerMentions(int64) bool                                     { return false }
func (NoSystems) SpawnerKilled(int64, int64, *[]int64) (SpawnerKey, bool)        { return SpawnerKey{}, false }
func (NoSystems) SpawnerTick(int64, *[]int64, *[]SpawnerKey)                     {}
func (NoSystems) SpawnerNextDue() (int64, bool)                                  { return 0, false }
func (NoSystems) DropRunData(int64)                                              {}
func (NoSystems) MovePlayerAction(RunRef, QuestAction) bool                      { return false }
func (NoSystems) LeaveVehicle()                                                  {}
func (NoSystems) ResetVehicles()                                                 {}
func (NoSystems) OnQuestTransfer(int32)                                          {}
func (NoSystems) LeaveDungeon() bool                                             { return false }
func (NoSystems) IsDungeonEntered(int32) bool                                    { return false }
func (NoSystems) DungeonLevelPlay(int32) (int32, bool)                           { return 0, false }
func (NoSystems) CountdownRuns(int32, float64) bool                              { return false }
func (NoSystems) OnLevelPlayFailed(int32)                                        {}
func (NoSystems) OnLevelPlayFinished(int32)                                      {}
