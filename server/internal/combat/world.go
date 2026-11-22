// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"os"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/protocol"
)

// PushSink is where server pushes go: the gateway session, or a recorder in tests.
type PushSink interface{ Push(m proto.Message) }

// GameHooks are the calls combat makes into the quests, dungeons and level plays (R-143h).
type GameHooks interface {
	OnUseSkill(entityNetID, skillID int64)
	OnMonsterKilled(monster *CombatEntity)
	OnGenderChanged(isMale bool)
	OnSceneSaved()
}

// NullHooks does nothing; embed it to implement only some hooks.
type NullHooks struct{}

func (NullHooks) OnUseSkill(int64, int64)       {}
func (NullHooks) OnMonsterKilled(*CombatEntity) {}
func (NullHooks) OnGenderChanged(bool)          {}
func (NullHooks) OnSceneSaved()                 {}

// ActiveBuffEntry is one live buff on an entity's FightBuffComponent.
type ActiveBuffEntry struct {
	HandleID     int32
	BuffID       int64
	Level        int32
	StackCount   int32
	InstigatorID int64
	EntityID     int64
	ApplyType    protocol.ApplyGEType
	Duration     float32
	LeftDuration float32
	IsActive     bool
	ServerID     int32
	MessageID    int64
	ConfBuffID   int64

	// ExpireAtMs is when the buff ends (monotonic ms), nil: never.
	ExpireAtMs *int64
	// NextPeriodAtMs is the next periodic tick, nil: not periodic.
	NextPeriodAtMs *int64
	// TimerVersion goes up when the timers are rescheduled, so stale heap entries are skipped.
	TimerVersion int32
}

// NewBuffEntry has the defaults: level 1, one stack, infinite, active, message -1.
func NewBuffEntry() *ActiveBuffEntry {
	return &ActiveBuffEntry{Level: 1, StackCount: 1, Duration: -1, LeftDuration: -1, IsActive: true, MessageID: -1}
}

func i64p(v int64) *int64 { return &v }

func (b *ActiveBuffEntry) ToProto(nowMs int64) *protocol.FightBuffInformation {
	left := b.Duration
	if b.ExpireAtMs != nil {
		left = max(0, float32(*b.ExpireAtMs-nowMs)/1000)
	}
	b.LeftDuration = left
	return &protocol.FightBuffInformation{
		HandleId: b.HandleID, BuffId: b.BuffID, Level: b.Level, StackCount: b.StackCount,
		InstigatorId: b.InstigatorID, EntityId: b.EntityID, ApplyType: b.ApplyType,
		Duration: b.Duration, LeftDuration: left, IsActive: b.IsActive, ServerId: b.ServerID,
		MessageId: b.MessageID, ConfBuffId: b.ConfBuffID,
	}
}

// CombatEntity is an entity as roles, formation and combat see it.
type CombatEntity struct {
	NetID            int64
	ConfigID         int32
	ConfigType       protocol.EntityConfigType
	EntityType       protocol.EEntityType
	PlayerID         int32
	ModelID          int32
	AreaID           int32
	MapID            int32
	IsVisible        bool
	IsOpenWorldSpawn bool
	VisionCaptureID  int32
	RewardID         int32

	PosX, PosY, PosZ     float32
	Pitch, Yaw, Roll     float32
	Attributes           *AttributeSet
	Buffs                []*ActiveBuffEntry
	NeedClientApplyBuffs []int64
	TagCounts            omap.Map[int32, int32]

	// Role-owned fields.
	VisionEntityIDs      []int64
	CustomEntityIDs      []int64
	WeaponID             int32
	WeaponBreachLevel    int32
	MainEchoSummonID     int32
	ComboEchoSummonID    int32
	VisionSkillComponent *protocol.VisionSkillComponentPb
	SkillComponent       *protocol.SkillComponentPb

	// Monster and combat components.
	PartComponent *protocol.PartComponentPb
	LogicState    *protocol.LogicStateComponentPb
	FsmComponent  *protocol.EntityFsmComponentPb
	Fsm           *FsmRuntime // the server's side of its AI state machines (R-152)
	FsmDirty      bool
	HasHate       bool
}

// NewCombatEntity is an entity with empty attributes, visible, with an empty logic state.
func NewCombatEntity(netID int64) *CombatEntity {
	return &CombatEntity{NetID: netID, IsVisible: true, Attributes: NewAttributeSet(), LogicState: &protocol.LogicStateComponentPb{}}
}

func (e *CombatEntity) IsPlayerRole() bool {
	return e.EntityType == protocol.EEntityType_EEntityType_Player
}
func (e *CombatEntity) IsMonster() bool {
	return e.EntityType == protocol.EEntityType_EEntityType_Monster
}
func (e *CombatEntity) Life() int32    { return e.Attributes.Slots[AttrLife].Current }
func (e *CombatEntity) LifeMax() int32 { return e.Attributes.Slots[AttrLifeMax].Current }

func (e *CombatEntity) SetPosition(x, y, z float32) { e.PosX, e.PosY, e.PosZ = x, y, z }

// AdjustTag counts a tag up or down; it returns the new count.
func (e *CombatEntity) AdjustTag(tagID int32, isAdd bool) int32 {
	cur := e.TagCounts.At(tagID)
	next := max(0, cur-1)
	if isAdd {
		next = cur + 1
	}
	if next == 0 {
		e.TagCounts.Delete(tagID)
	} else {
		e.TagCounts.Set(tagID, next)
	}
	e.FsmDirty = true
	return next
}

type CombineRelation struct {
	TargetEntityID   int64
	CombinerEntityID int64
	PartInfo         *protocol.CombinePartInfo
}

type DeadCorpseEntry struct {
	EntityNetID int64
	RemoveAtMs  int64
}

type RespawnKey struct{ MapID, ConfigID int32 }

type PendingRespawnEntry struct {
	MapID, EntityConfigID int32
	RespawnAtMs           int64
}

// CombatSceneState is the scene as combat sees it, shared by its services and kept in step
// with the world's scene.
type CombatSceneState struct {
	nextNetID      int64
	nextBuffHandle int32

	InstanceID  int32
	MapConfigID int32
	PlayerID    int32

	PlayerX, PlayerY, PlayerZ          float32
	PlayerPitch, PlayerYaw, PlayerRoll float32

	Entities         omap.Map[int64, *CombatEntity]
	ActiveCombatants omap.Map[int64, struct{}]
	CombineRelations []*CombineRelation
	DeadCorpses      omap.Map[int64, DeadCorpseEntry]
	PendingRespawns  omap.Map[RespawnKey, PendingRespawnEntry]

	// Time dilation and the flow clock (specs/enter-world.md §4.2, combat.md §9.5).
	TimeDilation      float32
	RTimeDilation     int32
	FlowAnchorRealMs  int64
	FlowAnchorClockMs int64

	// NetIDSource is where net ids come from once the world's scene owns them (entities.md §2).
	NetIDSource func() int64
}

func NewCombatSceneState() *CombatSceneState {
	return &CombatSceneState{nextNetID: 1, nextBuffHandle: 1, InstanceID: 8, MapConfigID: 8, TimeDilation: 1, RTimeDilation: 1}
}

func (s *CombatSceneState) AllocateNetID() int64 {
	if s.NetIDSource != nil {
		return s.NetIDSource()
	}
	id := s.nextNetID
	s.nextNetID++
	return id
}

// Reset: a scene join starts the scene over with no entities (specs/spawning.md §7).
func (s *CombatSceneState) Reset(instanceID, mapConfigID int32, netIDs func() int64) {
	s.InstanceID, s.MapConfigID, s.NetIDSource = instanceID, mapConfigID, netIDs
	s.Entities.Clear()
	s.ActiveCombatants.Clear()
	s.CombineRelations = nil
	s.DeadCorpses.Clear()
	s.PendingRespawns.Clear()
}

func (s *CombatSceneState) SetNextNetID(next int64) {
	if next > s.nextNetID {
		s.nextNetID = next
	}
}

func (s *CombatSceneState) AllocateBuffHandle() int32 {
	h := s.nextBuffHandle
	s.nextBuffHandle++
	return h
}

func (s *CombatSceneState) SetNextBuffHandle(next int32) {
	if next > s.nextBuffHandle {
		s.nextBuffHandle = next
	}
}

func (s *CombatSceneState) GetEntity(netID int64) *CombatEntity { return s.Entities.At(netID) }

func (s *CombatSceneState) PlayerRoleEntities() []*CombatEntity {
	var out []*CombatEntity
	for _, e := range s.Entities.All() {
		if e.IsPlayerRole() {
			out = append(out, e)
		}
	}
	return out
}

func (s *CombatSceneState) InitFlowClock(nowMs int64) {
	s.FlowAnchorRealMs, s.FlowAnchorClockMs, s.TimeDilation = nowMs, nowMs, 1
}

func (s *CombatSceneState) GetFlowTimeMs(nowMs int64) int64 {
	if s.FlowAnchorClockMs == 0 {
		return nowMs
	}
	elapsed := max(0, nowMs-s.FlowAnchorClockMs)
	return s.FlowAnchorRealMs + int64(float64(elapsed)*float64(s.TimeDilation))
}

func (s *CombatSceneState) SetTimeDilation(d float32, nowMs int64) {
	if math.IsInf(float64(d), 0) || math.IsNaN(float64(d)) {
		return
	}
	s.FlowAnchorRealMs = s.GetFlowTimeMs(nowMs)
	s.FlowAnchorClockMs = nowMs
	s.TimeDilation = d
}

// UpdateCombatant tracks the entities in a fight and pushes PlayerBattleStateChangeNotify when
// the set turns empty or non-empty (specs/combat.md §9.2).
func (s *CombatSceneState) UpdateCombatant(netID int64, inCombat bool, sink PushSink) bool {
	was := s.ActiveCombatants.Len() > 0
	if inCombat {
		s.ActiveCombatants.Set(netID, struct{}{})
	} else {
		s.ActiveCombatants.Delete(netID)
	}
	now := s.ActiveCombatants.Len() > 0
	if was != now {
		sink.Push(&protocol.PlayerBattleStateChangeNotify{PlayerId: s.PlayerID, InBattle: now})
		return true
	}
	return false
}

// ----- state machines (specs/combat.md §9.6) -----

const (
	CommonStateMachineID           = "SM_Common"
	DefaultRoleBeHitStateMachineID = "SM_RoleBeHit"
)

// FsmInitializer builds the first EntityFsmComponentPb of a role or monster from RoleBeHitMap
// or AiBase and AiStateMachineConfig.
type FsmInitializer struct {
	tables      *data.DataTables
	commonHash  int32
	commonNodes map[int32]*data.Object
	graphs      fsmGraphCache
}

func NewFsmInitializer(t *data.DataTables) *FsmInitializer {
	f := &FsmInitializer{tables: t, commonNodes: map[int32]*data.Object{}}
	if cfg, ok := t.AiStateMachineConfigById[CommonStateMachineID]; ok {
		if root := objOf(cfg.StateMachineJson); root != nil {
			f.commonHash = readInt(root.Get("Version"))
			indexNodes(root, f.commonNodes)
		}
	}
	return f
}

func (f *FsmInitializer) BuildForRole(roleID int32) *protocol.EntityFsmComponentPb {
	name := DefaultRoleBeHitStateMachineID
	if m, ok := f.tables.RoleBeHitMapById[roleID]; ok && m.StateMachineName != "" {
		name = m.StateMachineName
	}
	return f.BuildForStateMachine(name)
}

func (f *FsmInitializer) BuildForMonsterAI(aiID int32) *protocol.EntityFsmComponentPb {
	ai, ok := f.tables.AiBaseById[aiID]
	if aiID <= 0 || !ok || ai.StateMachine == "" {
		return nil
	}
	return f.BuildForStateMachine(ai.StateMachine)
}

// GraphForMonsterAI is the state machine graph of an AI (AiBase.StateMachine), nil without one.
func (f *FsmInitializer) GraphForMonsterAI(aiID int32) *FsmGraph {
	ai, ok := f.tables.AiBaseById[aiID]
	if aiID <= 0 || !ok || ai.StateMachine == "" {
		return nil
	}
	return f.graphs.get(f.tables, ai.StateMachine)
}

func (f *FsmInitializer) BuildForStateMachine(id string) *protocol.EntityFsmComponentPb {
	cfg, ok := f.tables.AiStateMachineConfigById[id]
	if !ok {
		return nil
	}
	root := objOf(cfg.StateMachineJson)
	if root == nil {
		return nil
	}
	local := map[int32]*data.Object{}
	indexNodes(root, local)
	pb := &protocol.EntityFsmComponentPb{
		HashCode: readInt(root.Get("Version")), CommonHashCode: f.commonHash,
		FsmCustomBlackboardDatas: &protocol.FsmCustomBlackboardDatas{},
	}
	if sms, ok := root.Get("StateMachines").(*data.Array); ok {
		seen := map[int32]bool{}
		for _, it := range sms.Items {
			fsmID := readInt(it)
			if fsmID == 0 || seen[fsmID] {
				continue
			}
			seen[fsmID] = true
			isAnim := false
			if b, ok := member(local[fsmID], "IsAnimStateMachine").(data.Bool); ok {
				isAnim = bool(b)
			}
			flag := protocol.EFsmStateFlag_EFsmStateFlag_Changed
			if isAnim {
				flag = protocol.EFsmStateFlag_EFsmStateFlag_Confirmed
			}
			pb.Fsms = append(pb.Fsms, &protocol.DFsm{FsmId: fsmID, CurrentState: f.initialLeaf(fsmID, local), Flag: flag})
		}
	}
	return pb
}

func firstChild(node *data.Object) (int32, bool) {
	if ch, ok := node.Get("Children").(*data.Array); ok && len(ch.Items) > 0 {
		return readInt(ch.Items[0]), true
	}
	return 0, false
}

func (f *FsmInitializer) initialLeaf(uuid int32, local map[int32]*data.Object) int32 {
	cur := uuid
	for depth := 0; depth < 32; depth++ {
		node := local[cur]
		if node == nil {
			node = f.commonNodes[cur]
		}
		if node == nil {
			return cur
		}
		// Follow ReferenceUuid into SM_Common when the node has no children of its own.
		if next, ok := firstChild(node); ok {
			if next == 0 || next == cur {
				return cur
			}
			cur = next
			continue
		}
		ref := readInt(node.Get("ReferenceUuid"))
		if refNode, ok := f.commonNodes[ref]; ref != 0 && ref != cur && ok {
			if next, ok := firstChild(refNode); ok && next != 0 && next != cur {
				cur = next
				continue
			}
			return ref
		}
		return cur
	}
	return cur
}

func indexNodes(root *data.Object, m map[int32]*data.Object) {
	nodes, ok := root.Get("Nodes").(*data.Array)
	if !ok {
		return
	}
	for _, it := range nodes.Items {
		if n := objOf(it); n != nil {
			if u := readInt(n.Get("Uuid")); u != 0 {
				if _, dup := m[u]; !dup {
					m[u] = n
				}
			}
		}
	}
}

// ----- landing damage (R-118, specs/combat.md §5.6) -----

// DefaultLandingRoleArgs is CommonParam landing_damage_args_role of 3.8, used when the file
// cannot be read.
var DefaultLandingRoleArgs = []int32{3000, 4000, 11000, 2000}

// ParseLandingRoleArgs reads landing_damage_args_role from CommonParam.json text.
func ParseLandingRoleArgs(text []byte) []int32 {
	v, err := data.ParseJSON(text)
	if err != nil {
		return nil
	}
	root := objOf(v)
	if root == nil {
		return nil
	}
	arr, ok := root.Get("landing_damage_args_role").(*data.Array)
	if !ok || len(arr.Items) < 4 {
		return nil
	}
	out := make([]int32, 4)
	for i := range out {
		n, ok := arr.Items[i].(data.Number)
		if !ok || float64(n) != float64(int32(n)) {
			return nil
		}
		out[i] = int32(n)
	}
	return out
}

func LoadLandingRoleArgs(path string) []int32 {
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if a := ParseLandingRoleArgs(b); a != nil {
				return a
			}
		}
	}
	return DefaultLandingRoleArgs
}

// LandingRoleDamage is the client's LandingDamageCalculationRole:
// floor((max(-speedZ / a0 - 1, 0) + time_s ^ (a2 / 10000) * a3 / 10000) * LifeMax).
func LandingRoleDamage(args []int32, speedZ float32, timeExceedingMs, lifeMax int32) int32 {
	if lifeMax <= 0 || len(args) < 4 || args[0] == 0 {
		return 0
	}
	fall := -float64(speedZ)
	timeS := float64(max(timeExceedingMs, 0)) / 1000.0
	rateBase := math.Max(fall/float64(args[0])-1.0, 0.0)
	exp := float64(args[2]) / 10000.0
	rateTime := math.Pow(timeS, exp) * float64(args[3]) / 10000.0
	damage := math.Floor((rateBase + rateTime) * float64(lifeMax))
	if !(damage > 0) {
		return 0
	}
	return int32(math.Min(damage, 2_000_000_000.0))
}

// ApplyLanding takes damage off Life (current and base), never below 0: what was lost and
// whether that killed.
func ApplyLanding(a *AttributeSet, damage int32) (lost int32, killed bool) {
	life := &a.Slots[AttrLife]
	before := life.Current
	if damage <= 0 || before <= 0 {
		return 0, false
	}
	after := max(before-damage, 0)
	life.Current, life.Base = after, after
	return before - after, after == 0
}

func f32p(v float32) *float32 { return &v }
