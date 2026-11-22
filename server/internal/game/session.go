// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package game is the game behind the gateway: one PlayerSession per player in the world, from
// Entered to Released (specs/enter-world.md), with its scene, world spawner and the player's
// combat context (roles, inventory, formation, combat). The quests and the systems of R-143h
// (stage R-144f) plug in through Systems. Ported from our C# Game/PlayerSession and WorldGame.
package game

import (
	"slices"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// FirstWorldInstance is the open world's instance.
const FirstWorldInstance = 8

// Client is where a player's pushes go: the gateway session, or a recorder in tests.
type Client interface {
	PlayerID() int32
	Push(m proto.Message)
	PushID(id protocol.MessageID, m proto.Message)
}

// PlayerSession is one player in the game. Single-threaded, like the gateway.
type PlayerSession struct {
	Client  Client
	Files   *save.PlayerFiles
	Data    *world.WorldData
	Clock   *common.GameClock
	Fsm     *combat.FsmInitializer
	Systems Systems

	State  *save.PlayerState
	Combat *combat.PlayerContext
	Scene  *world.Scene
	// Reconnected: the session came in by ReconnectRequest (specs/enter-world.md §8).
	Reconnected bool
	// World is the saved world facts the spawner reads, loaded once per session.
	World *world.PlayerWorld
	// Spawner is the current scene's world spawner, in scenes that have world spawns.
	Spawner *world.Spawner
	// QuestState is the player's persistent quest state (nil: game runs without quest data).
	QuestState *PlayerQuests

	attach          func(combat.GameHooks) *combat.PlayerContext
	start           *world.SessionStart
	afterResponse   []func()
	flows           *FlowQueue
	groups          *EntityGroups
	actions         *EntityActions
	interactFlowInc int64
	pendingTransfer *pendingTransfer
	activities      *PlayerActivities
	shops           *PlayerShops
	payShop         *PlayerPayShop
	gacha           *PlayerGacha
	motorcycle      *PlayerMotorcycle
	achievements    *PlayerAchievements
	worldMap        *PlayerWorldMap
	music           *PlayerMusic
	clientStorage   *PlayerClientStorage
	guides          *PlayerGuides

	// Patrols are the scene's monster patrols (specs/systems.md §1.5).
	Patrols Patrols

	// CommandFile is the dev command file (assets/scripts/dbg/cmd.txt) polled at the heartbeat;
	// "" polls none (specs/systems.md §7.1).
	CommandFile string
	// RunCommand runs one chat command of a mail's cmd: line (specs/systems.md §12); nil: the
	// command dispatcher (RunCommands). False when it failed.
	RunCommand func(string) bool
}

func NewPlayerSession(c Client, files *save.PlayerFiles, d *world.WorldData, clock *common.GameClock,
	attach func(combat.GameHooks) *combat.PlayerContext) *PlayerSession {
	return &PlayerSession{Client: c, Files: files, Data: d, Clock: clock, attach: attach, Systems: NoSystems{}}
}

// Sink is the client as combat pushes to it.
func (p *PlayerSession) Sink() combat.PushSink { return p.Client }

// Formation is the formation in use.
func (p *PlayerSession) Formation() *save.FormationSlotSave {
	return p.Combat.Formation.ActiveFormation()
}

// Enter: load the state, push the login data, join the last scene.
func (p *PlayerSession) Enter(reconnect bool) {
	p.Reconnected = reconnect
	p.Combat = p.attach(p.Systems.Hooks(p))
	p.State = save.LoadPlayerState(p.Files)
	p.Motorcycle().EnsureInitialized(p)
	p.World = world.LoadPlayerWorld(p.Files, p.Combat.Player.BasicInfo.Attributes.CurWorldLevel)
	start := world.StartLogin
	if reconnect {
		start = world.StartReconnect
	}
	p.start = &start
	p.Systems.Entered(p, reconnect)
	p.pushLoginData()
	p.JoinScene(p.State.LastScene, nil, false)
}

// Release: write what is pending (specs/movement.md §3).
func (p *PlayerSession) Release() {
	if p.Scene != nil {
		p.Scene.SaveIfDue(p.Clock.Ms(), true)
	}
	if p.Combat != nil {
		p.Combat.FlushSaves()
	}
}

// Tick runs what falls due on the game clock: the position save, buff timers, corpses (8 s;
// the spawner then counts them killed) and the role and weapon saves.
func (p *PlayerSession) Tick() {
	if p.Scene != nil {
		p.Scene.SaveIfDue(p.Clock.Ms(), false)
	}
	if p.Combat == nil {
		return
	}
	for _, id := range p.Combat.Tick(p.Clock.RealMs(), p.Sink()) {
		if p.Scene != nil {
			p.Scene.Remove(id)
		}
	}
}

// JoinScene is the scene join (specs/enter-world.md §4, movement.md §4.4 steps 3–5): close the
// old scene, load the new one, place the player, make the join entities and push the join.
func (p *PlayerSession) JoinScene(instanceID int32, target *world.Vec3, sceneChange bool) {
	if old := p.Scene; old != nil {
		old.SaveIfDue(p.Clock.Ms(), true)
		p.Combat.FlushSaves()
	}
	p.Systems.LeavingScene(p)
	scene := world.NewScene(instanceID, p.Data.MapOf(instanceID), p.Files, world.NewFlowClock(p.Clock.RealMs(), p.Clock.Ms()))
	p.place(scene, target)
	if p.State.FirstScene {
		p.InitExploreTools(p.State.ExploreTools)
	}
	p.Systems.BeforeFormation(p, instanceID)
	p.Combat.Formation.EnsureDefaults()
	g := p.Combat.Scene
	g.Reset(instanceID, scene.MapID, scene.NetIds.Take)
	g.InitFlowClock(p.Clock.RealMs())
	p.syncPlayerPlace(scene)

	world.MakeJoinEntities(scene, p.State, p.Data)
	for _, pb := range p.Combat.Formation.SpawnActiveFormation(p.Clock.RealMs()) {
		scene.AddAs(p.fromCombat(pb), pb.Id)
	}
	for _, slot := range p.Formation().FilledRoles() {
		scene.RoleEntities.Set(slot.RoleID, slot.EntityID)
	}
	p.Scene = scene
	p.Spawner = nil
	if p.Systems.HasSpawns(p, instanceID) {
		start := world.StartLogin
		if p.start != nil {
			start = *p.start
		}
		sp := world.NewSpawner(p.Data, scene, p.World, start, p.added, p.removed, p.Systems.Candidates(p, instanceID))
		// The first refresh comes with the next heartbeat or the first move, whichever is first.
		sp.Marked = true
		sp.Spawned = func(e *world.Entity, c *world.Candidate) { p.RegisterLevelEntity(e, c.Row, true) }
		sp.Despawned = p.ForgetLevelEntity
		sp.InFight = func(id int64) bool { return p.Combat.Scene.ActiveCombatants.Has(id) }
		p.Systems.ConfigureSpawner(p, sp)
		if p.start == nil {
			sp.SkipCleanUp()
		}
		p.start = nil
		p.Spawner = sp
	}
	scene.SaveInstance()
	p.State.SceneSaved()
	p.State.SaveExploreTools()
	if sceneChange {
		common.Info("scene", fmtInt("player %d entered instance %d (map %d)", p.State.PlayerID(), instanceID, scene.MapID))
	}
	p.pushSceneJoin(scene)
}

// fromCombat is a world entity for an entity combat made (a role, its visions and
// concomitants): a role's values are rebuilt from combat's state whenever it is sent; the
// others keep what combat made.
func (p *PlayerSession) fromCombat(pb *protocol.EntityPb) *world.Entity {
	e := &world.Entity{
		ConfigID: pb.ConfigId, ConfigType: pb.ConfigType, EntityType: protocol.EEntityType(pb.EntityType),
		Pos: world.Vec3From(pb.Pos), Rot: world.RotFrom(pb.Rot), IsVisible: pb.IsVisible, PlayerID: pb.PlayerId,
	}
	c := p.Combat.Scene.GetEntity(pb.Id)
	if role, ok := p.Combat.Player.Roles.Get(pb.ConfigId); c != nil && c.IsPlayerRole() && ok {
		e.Live = func() *protocol.EntityPb { return p.Combat.Formation.BuildRoleEntityPb(c, role, p.Clock.RealMs()) }
	} else {
		e.Live = func() *protocol.EntityPb { return proto.Clone(pb).(*protocol.EntityPb) }
	}
	return e
}

// syncPlayerPlace: combat's scene keeps its own copy of the player's place (beacons, revive).
func (p *PlayerSession) syncPlayerPlace(s *world.Scene) {
	g := p.Combat.Scene
	g.PlayerX, g.PlayerY, g.PlayerZ = s.PlayerPos.X, s.PlayerPos.Y, s.PlayerPos.Z
	g.PlayerPitch, g.PlayerYaw, g.PlayerRoll = s.PlayerRot.Pitch, s.PlayerRot.Yaw, s.PlayerRot.Roll
}

// place puts the player in a scene just loaded (specs/movement.md §4.4 step 4).
func (p *PlayerSession) place(s *world.Scene, target *world.Vec3) {
	var born world.Vec3
	var bornRot world.Rot
	if d, ok := p.Data.Tables.InstanceDungeonById[s.InstanceID]; ok {
		if len(d.BornPosition) >= 3 {
			born = world.Vec3{X: float32(d.BornPosition[0]), Y: float32(d.BornPosition[1]), Z: float32(d.BornPosition[2])}
		}
		if len(d.BornRotation) >= 3 {
			bornRot = world.Rot{Pitch: float32(d.BornRotation[1]), Yaw: float32(d.BornRotation[2]), Roll: float32(d.BornRotation[0])}
		}
	}
	var saved *save.ScenePlayer
	for _, sp := range s.Instance.Players {
		if sp.ID == p.State.PlayerID() {
			saved = sp
			break
		}
	}
	switch {
	case saved == nil:
		s.PlayerPos, s.PlayerRot = born, bornRot
		if target != nil {
			s.PlayerPos = *target
		}
	case target != nil:
		s.GroupType = saved.GroupType
		s.PlayerPos, s.PlayerRot = *target, bornRot
	default:
		s.GroupType = saved.GroupType
		s.PlayerPos = world.Vec3{X: saved.Location[0], Y: saved.Location[1], Z: saved.Location[2]}
		s.PlayerRot = world.Rot{Pitch: saved.Rotation[1], Yaw: saved.Rotation[2], Roll: saved.Rotation[0]}
	}
}

// Heartbeat: the dev command file, what falls due, then the systems' timers and a refresh when
// something marked the scene (specs/spawning.md §4, quests.md §5.3).
func (p *PlayerSession) Heartbeat() {
	// specs/systems.md §7.1 step 1: the dev command file.
	p.pollCommandFile()
	p.Systems.BeforeHeartbeat(p)
	p.Tick()
	if p.Scene == nil {
		return
	}
	p.Systems.Tick(p)
	p.ImmediateSpawnCheck()
	p.Systems.AfterHeartbeat(p)
	// quests.md §5.3 step 4: mails dropped in since the last look.
	p.AnnounceNewMail()
}

// ImmediateSpawnCheck refreshes now when something marked the scene (specs/quests.md §5.5).
func (p *PlayerSession) ImmediateSpawnCheck() {
	if p.Scene == nil || p.Spawner == nil {
		return
	}
	if p.Spawner.Marked || p.Systems.SpawnsQueued(p) {
		p.Spawner.Refresh(p.Clock.Ms(), p.Clock.Seconds())
	}
}

func (p *PlayerSession) added(e *world.Entity) {
	p.Client.Push(&protocol.EntityAddNotify{EntityPbs: []*protocol.EntityPb{e.ToPb()}})
}

func (p *PlayerSession) removed(ids []int64) {
	n := &protocol.EntityRemoveNotify{IsRemove: true}
	for _, id := range ids {
		n.RemoveInfos = append(n.RemoveInfos, &protocol.EntityRemoveInfo{EntityId: id})
	}
	p.Client.Push(n)
}

// RemoveFromScene takes an entity off the scene and tells the client.
func (p *PlayerSession) RemoveFromScene(id int64) {
	if p.Scene == nil || !p.Scene.Remove(id) {
		return
	}
	p.ForgetLevelEntity(id)
	p.removed([]int64{id})
}

// InitExploreTools are a new player's explore tools in the first scene (specs/enter-world.md §7).
func (p *PlayerSession) InitExploreTools(t *save.ExploreToolsInfo) {
	rows := p.Data.Tables.ExploreTools
	t.UnlockedExploreSkills = []int32{}
	roulette := []int32{6002, 1001, 1007}
	for _, r := range rows {
		if r.Authorization.Count() == 0 {
			t.UnlockedExploreSkills = append(t.UnlockedExploreSkills, r.PhantomSkillId)
		}
	}
	for _, r := range rows {
		if id := r.PhantomSkillId; id == 1015 || id == 1029 || id == 1009 {
			roulette = append(roulette, id)
		}
	}
	t.Roulette = Pad(roulette)
	t.ActiveExploreSkill = 1001
	t.ActiveFunctionSkill = 0
	t.MotorcycleRoulette = []int32{6001, 6003, 6007, 6011, 6012, 6020, 0, 0}
	t.ActiveMotorcycleSkill = 6001
}

// Pad is a roulette padded with 0 or cut to 8 slots.
func Pad(ids []int32) []int32 {
	out := make([]int32, 8)
	copy(out, ids)
	return out
}

// After queues work for after the current request's response.
func (p *PlayerSession) After(fn func()) { p.afterResponse = append(p.afterResponse, fn) }

// RunAfterResponse runs what After queued.
func (p *PlayerSession) RunAfterResponse() {
	run := p.afterResponse
	p.afterResponse = nil
	for _, fn := range run {
		fn()
	}
}

// roleEntityHolds: the entity is one of the formation's role entities.
func (p *PlayerSession) roleEntityHolds(id int64) bool {
	return slices.Contains(p.Scene.RoleEntities.Values(), id)
}

// SyncFormationEntities keeps Scene.RoleEntities and Scene entities in sync after formation
// or role entity rebuilds (PlayerSession.ProfileAndCosmetics.cs).
func (p *PlayerSession) SyncFormationEntities() {
	s := p.Scene
	if s == nil {
		return
	}
	for _, oldID := range s.RoleEntities.Values() {
		if !p.Combat.Scene.Entities.Has(oldID) {
			s.Remove(oldID)
		}
	}
	s.RoleEntities.Clear()
	for _, slot := range p.Formation().FilledRoles() {
		if slot.EntityID <= 0 {
			continue
		}
		s.RoleEntities.Set(slot.RoleID, slot.EntityID)
		if s.Get(slot.EntityID) == nil {
			if c := p.Combat.Scene.GetEntity(slot.EntityID); c != nil {
				if role, ok := p.Combat.Player.Roles.Get(slot.RoleID); ok {
					s.AddAs(p.fromCombat(p.Combat.Formation.BuildRoleEntityPb(c, role, p.Clock.RealMs())), slot.EntityID)
				}
			}
		}
	}
}
