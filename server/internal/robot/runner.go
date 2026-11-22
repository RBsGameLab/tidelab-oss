// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/quests"
	"tidelab/internal/wire"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// Runner drives the game in memory like a client (MovePackagePush, EntityInteractRequest,
// EntityDynamicInteractRequest, SubmitNodeRequest, FlowEndRequest, EnterInstRequest,
// LeaveInstRequest, ...) and reports progress. Ported from our C# RobotRunner.
type Runner struct {
	Host         *Host
	StateSetters *StateSetters
	Ranges       *RangeTable
	Logs         *LogCapture
	Out          io.Writer
	Verbose      bool
	TryOptions   bool

	Phase PhaseStats

	// Observed in server replies and pushes since the last reset.
	DungeonResult *bool
	Flows         []int64
	Lifts         []liftMove
	LastError     *protocol.ErrorCode
	UnhandledList []string
	unhandledSet  map[string]bool

	// Per dungeon / round tracking
	Killed     map[int64]bool
	Interacted map[int64]bool
	AwayDone   map[uint64]bool
	StepsSig   uint64
	DriveTries map[int64]int
	Submitted  map[int64]bool
	Drives     int

	// Targeted check observations
	SignActivity        *int32
	SignStates          []protocol.SignState
	TimePoints          []timePointEntry
	DoubleID            *int32
	DoubleUsed          int32
	SettleRate          int32
	UpdateUsed          *int32
	LordGymUnlocked     int
	LordGymPassed       int
	LordGymResult       *protocol.LordGymLevelPlayResultNotify
	BossRushLevels      int
	BossRushFirst       int32
	BossRushScore       *int32
	TowerFloors         int
	TowerFirst          int32
	TowerFirstStars     int32
	TowerEnd            *bool
	DreamInsts          int
	DreamRuns           int
	DreamBossRewards    int
	DreamLimited        int
	DreamButtons        int
	DreamBossFirst      protocol.SignState
	WaveTemplates       []int32
	WaveTowerPrefabs    int
	WaveBareTower       int
	LevelPlayStates     int
	MinimapPos          *world.Vec3
	RangeReply          *rangeReplyInfo
	LpEnter, LpLeave    int
	LastEnteredLp       *int32
	RidesOn, RidesOff   int
	RideVehicle         int64
	LevelEvents         int
	EntityPosOK         bool
	EntityPosResult     *world.Vec3
	SitOK               *bool
	ShopOK              *bool
	LandingOK           *bool
	PatrolOK, PatrolBad int
	BagList             *[2]int
	PushedItemsTotal    int
	PushedItemsUnknown  int
	WeaponsAdded        [][4]int32
	AchEntries          int
	AchFinished         int32
	AchStars            int32
	AchTargetTime       uint32
	AchTargetReceived   bool
	AchNotifies         int
	MarksShown          int
	TowerMarkShown      bool
	TerminalGroups      int
	MusicAlbums         int
	MusicSongs          int
	PayShopItems        int
	RolesNotified       []int32
	TurntableID         *int32
	TurntableRound      int32
	TurntableClaimed    int
	TurntableAllDone    bool
	TurntableTasks      []*protocol.ActivityTask
	TurntableReward     int32
	TurntableItems      int
	TurntableRoundEnd   bool
	ScratchRounds       int
	ScratchFirstRound   int32
	ScratchFirstCells   int
	ScratchFirstLeft    int
	ScratchTimes        []*protocol.ScratchCardTimeInfo
	ScratchOpenType     int32
	ScratchOpened       []int32
	ScratchGot          int

	// Offers and R-124's tree tracking
	Offered           []int32
	OptionNets        []int64
	InProgress        []int32
	ClientTrees       map[int64]bool
	ClientNodes       map[treeNode]bool
	TreeInfosSeen     int
	TreeDupNodes      int
	TreeOrphanUpdates int

	// Cross-map travel (R-34)
	elsewhere   map[int64]*Loc
	elsewhereOK map[int64]bool
	teleporters []teleporterAt
	teleLoaded  bool
	Travels     int
}

const (
	MaxDungeonIterations = 300
	IdleLimit            = 3
	StallLimit           = 60
	StallLimitKnown      = 15
	DungeonStallLimit    = 4 * StallLimit
	RoundsPerTarget      = 4
	MaxSkips             = 400
	MaxStoryIterations   = 40_000
	AchievementChecked   = 100101
)

type Loc struct {
	Map int32
	Pos world.Vec3
}

type teleporterAt struct {
	ID, Map int32
	Pos     world.Vec3
}

type treeNode struct {
	Tree int64
	Node int32
}

type timePointEntry struct {
	ID        int32
	Can, Done bool
}

type rangeReplyInfo struct {
	ID       int64
	Entities int
	Player   bool
}

type liftMove struct {
	EntityID int64
	Floor    int32
}

// PhaseStats are where a story run spends its time.
type PhaseStats struct {
	Rounds, StepsMs, ActMs, FlowsMs, MoveMs, DispatchMs, Sends int64
}

func NewRunner(h *Host, setters *StateSetters, ranges *RangeTable, logs *LogCapture, out io.Writer, verbose, tryOptions bool) *Runner {
	if setters == nil {
		setters = NewStateSetters(nil)
	}
	if ranges == nil {
		ranges = NewRangeTable(nil)
	}
	return &Runner{
		Host: h, StateSetters: setters, Ranges: ranges, Logs: logs, Out: out, Verbose: verbose, TryOptions: tryOptions,
		unhandledSet: map[string]bool{}, Killed: map[int64]bool{}, Interacted: map[int64]bool{}, AwayDone: map[uint64]bool{},
		DriveTries: map[int64]int{}, Submitted: map[int64]bool{}, DreamBossFirst: protocol.SignState_SignState_Lock,
		ScratchOpenType: -1, ClientTrees: map[int64]bool{}, ClientNodes: map[treeNode]bool{},
		elsewhere: map[int64]*Loc{}, elsewhereOK: map[int64]bool{},
	}
}

func (r *Runner) Session() *game.PlayerSession { return r.Host.Session }
func (r *Runner) Tables() *data.DataTables     { return r.Host.Tables }
func (r *Runner) WorldData() *world.WorldData  { return r.Host.WorldData }
func (r *Runner) Quests() *quests.Engine       { return r.Session().Quests() }
func (r *Runner) PlayerID() int32              { return r.Host.PlayerID }

func (r *Runner) printf(format string, a ...any) { fmt.Fprintf(r.Out, format, a...) }

func (r *Runner) UnhandledSummary() string { return strings.Join(r.UnhandledList, ",") }

func (r *Runner) InstanceID() int32 {
	if sc := r.Session().Scene; sc != nil {
		return sc.InstanceID
	}
	return 0
}

func (r *Runner) MapID() int32 {
	if sc := r.Session().Scene; sc != nil {
		return sc.MapID
	}
	return 0
}

func (r *Runner) InDungeon() bool {
	d, ok := r.Tables().InstanceDungeonById[r.InstanceID()]
	return ok && d.InstType == 2
}

func (r *Runner) PlayerPos() world.Vec3 {
	if sc := r.Session().Scene; sc != nil {
		return sc.PlayerPos
	}
	return world.Vec3{}
}

// RoleNetID is the current role's entity (else the first role's).
func (r *Runner) RoleNetID() (int64, bool) {
	sc := r.Session().Scene
	if sc == nil {
		return 0, false
	}
	if id, ok := sc.RoleEntities.Get(r.Session().Formation().CurRole); ok {
		return id, true
	}
	if vals := sc.RoleEntities.Values(); len(vals) > 0 {
		return vals[0], true
	}
	return 0, false
}

func (r *Runner) NetIDOf(configID int64) (int64, bool) {
	return r.Session().Actions().NetIDOf(configID)
}

// RowPos is where a level row of this map stands.
func (r *Runner) RowPos(configID int64) (world.Vec3, bool) {
	sc := r.Session().Scene
	if sc == nil || configID < math.MinInt32 || configID > math.MaxInt32 {
		return world.Vec3{}, false
	}
	row := r.Tables().LevelEntity(sc.MapID, int32(configID))
	if row == nil || len(row.Transform) == 0 || row.Transform[0] == nil {
		return world.Vec3{}, false
	}
	p := row.Transform[0]
	return world.Vec3{X: float32(p.X) / 100, Y: float32(p.Y) / 100, Z: float32(p.Z) / 100}, true
}

// RowElsewhere is R-34: the first row of configID on another map (cached).
func (r *Runner) RowElsewhere(configID int64) *Loc {
	here := int32(-1)
	if sc := r.Session().Scene; sc != nil {
		here = sc.MapID
	}
	if r.elsewhereOK[configID] {
		cached := r.elsewhere[configID]
		if cached == nil {
			return nil
		}
		if cached.Map != here {
			return cached
		}
	}
	var found *Loc
	for _, row := range r.Tables().LevelEntityConfig {
		if int64(row.EntityId) != configID || row.MapId == here || len(row.Transform) == 0 || row.Transform[0] == nil {
			continue
		}
		p := row.Transform[0]
		found = &Loc{row.MapId, world.Vec3{X: float32(p.X) / 100, Y: float32(p.Y) / 100, Z: float32(p.Z) / 100}}
		break
	}
	r.elsewhere[configID] = found
	r.elsewhereOK[configID] = true
	return found
}

func (r *Runner) TeleporterNear(mapID int32, pos world.Vec3) (int32, bool) {
	if !r.teleLoaded {
		r.teleLoaded = true
		for _, tp := range r.Tables().Teleporter {
			row := r.Tables().LevelEntity(tp.MapId, tp.TeleportEntityConfigId)
			if row == nil || len(row.Transform) < 3 || row.Transform[0] == nil || row.Transform[2] == nil {
				continue
			}
			p, s := row.Transform[0], row.Transform[2]
			if s.X == 0 || s.Y == 0 || s.Z == 0 {
				continue
			}
			r.teleporters = append(r.teleporters, teleporterAt{tp.Id, row.MapId, world.Vec3{X: float32(p.X) / float32(s.X), Y: float32(p.Y) / float32(s.Y), Z: float32(p.Z) / float32(s.Z)}})
		}
	}
	var best int32
	found := false
	bestD := float32(math.MaxFloat32)
	for _, tp := range r.teleporters {
		if tp.Map != mapID {
			continue
		}
		if d := DistSq(tp.Pos, pos); d < bestD {
			bestD, best, found = d, tp.ID, true
		}
	}
	return best, found
}

// StoryInstance is R-80b: a story instance whose Id equals its MapConfigId and is not an open-world map.
func (r *Runner) StoryInstance(mapID int32) (int32, bool) {
	if mapID == r.InstanceID() {
		return 0, false
	}
	d, ok := r.Tables().InstanceDungeonById[mapID]
	if !ok || d.MapConfigId != mapID || world.HasWorldSpawns(mapID) {
		return 0, false
	}
	return mapID, true
}

// StepDungeon is R-80g: the dungeon of an EnterDungeon/FinishDungeon step (by id, else the lowest
// id of its InstSubType).
func (r *Runner) StepDungeon(def quests.NodeDef) (int32, bool) {
	if def.Dungeon != 0 {
		return def.Dungeon, true
	}
	if def.SubType == 0 {
		return 0, false
	}
	var lowest int32
	found := false
	for _, d := range r.Tables().InstanceDungeon {
		if d.InstSubType == def.SubType && (!found || d.Id < lowest) {
			lowest, found = d.Id, true
		}
	}
	return lowest, found
}

func DistSq(a, b world.Vec3) float32 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return dx*dx + dy*dy + dz*dz
}

// AwayKey is R-83: the key of (steps signature, map) for AwayDone.
func AwayKey(stepsSig uint64, mapID int32) uint64 { return stepsSig*1_000_003 + uint64(int64(mapID)) }

// RangeTarget is R-76h: the range entity a CheckConditionGroup step asks the player to stand in.
func RangeTarget(def quests.NodeDef) (int64, bool) {
	if def.Check == nil {
		return 0, false
	}
	for _, c := range def.Check.Conds {
		if c.T == "CheckInRange" && c.InRange && len(c.Entities) > 0 {
			return c.Entities[0], true
		}
	}
	return 0, false
}

func HasVarPart(c *quests.Check) bool {
	if c == nil {
		return false
	}
	for _, s := range c.Conds {
		if s.T == "CompareVar" {
			return true
		}
	}
	return false
}

func GroupStates(def quests.NodeDef) []quests.StateCond {
	if def.Check == nil {
		return nil
	}
	var list []quests.StateCond
	for _, c := range def.Check.Conds {
		if c.T == "CompareEntityState" && c.Op == "Eq" {
			list = append(list, quests.StateCond{EntityID: c.Entity, State: c.State})
		}
	}
	return list
}

// ProgressEntities is R-39 / R-101: the scene's entities without town NPCs and chairs (they come
// and go as the robot walks).
func (r *Runner) ProgressEntities() int {
	sc := r.Session().Scene
	if sc == nil {
		return 0
	}
	ignore := 0
	if sp := r.Session().Spawner; sp != nil {
		for _, w := range sp.Spawns() {
			if w.Class == world.ClassNpc || w.Class == world.ClassSeat {
				ignore++
			}
		}
	}
	return max(0, len(sc.Entities())-ignore)
}

func (r *Runner) LocalCanComeOut(configID int64) bool {
	return WaitingHelps(InspectSpawn(r.Session(), configID))
}

func (r *Runner) NoteTargetAt(targets *[]world.Vec3, away **Loc, configID int64) {
	local, hasLocal := r.RowPos(configID)
	other := r.RowElsewhere(configID)
	if hasLocal && (other == nil || r.LocalCanComeOut(configID)) {
		for _, t := range *targets {
			if DistSq(t, local) < 1 {
				return
			}
		}
		*targets = append(*targets, local)
		return
	}
	if *away == nil && other != nil && !r.AwayDone[AwayKey(r.StepsSig, other.Map)] {
		*away = other
	}
}

func (r *Runner) NoteTarget(target **world.Vec3, away **Loc, configID int64) {
	if *target != nil {
		return
	}
	local, hasLocal := r.RowPos(configID)
	other := r.RowElsewhere(configID)
	if hasLocal && (other == nil || r.LocalCanComeOut(configID)) {
		*target = &local
		return
	}
	if *away == nil {
		*away = other
	}
	if *away == nil && hasLocal {
		*target = &local
	}
}

// NothingCanChange is R-80c: no waiting target on this map can spawn by walking or waiting.
func (r *Runner) NothingCanChange(steps []quests.Step) bool {
	if r.Session().Scene == nil {
		return false
	}
	waited := 0
	for _, st := range steps {
		def := st.Def
		if !st.LevelPlay && quests.IsVarConditionStep(def) {
			return false
		}
		var ids []int64
		ids = append(ids, def.Targets...)
		for _, o := range def.Options {
			ids = append(ids, o.EntityID)
		}
		for _, sc := range def.States {
			ids = append(ids, sc.EntityID)
		}
		added := 0
		for _, id := range ids {
			added++
			if added > 16 {
				break
			}
			if _, ok := r.NetIDOf(id); ok {
				continue
			}
			waited++
			if r.LocalCanComeOut(id) {
				return false
			}
		}
	}
	return waited > 0
}

// ----- sending requests and pushes -----

func (r *Runner) NoteUnhandled(name string) {
	if !r.unhandledSet[name] {
		r.unhandledSet[name] = true
		r.UnhandledList = append(r.UnhandledList, name)
	}
}

func msgIDName(m proto.Message) string {
	id, _ := protocol.IDOf(m)
	return id.Name()
}

func (r *Runner) SendRequest(m proto.Message) bool {
	r.LastError = nil
	start := time.Now()
	handled := r.Host.SendRequest(m, r.ProcessFrame)
	r.Phase.DispatchMs += time.Since(start).Milliseconds()
	r.Phase.Sends++
	if !handled {
		r.NoteUnhandled(msgIDName(m))
	}
	return handled
}

func (r *Runner) SendPush(m proto.Message) bool {
	r.LastError = nil
	start := time.Now()
	handled := r.Host.SendPush(m, r.ProcessFrame)
	r.Phase.DispatchMs += time.Since(start).Milliseconds()
	r.Phase.Sends++
	if !handled {
		r.NoteUnhandled(msgIDName(m))
	}
	return handled
}

// SendQuestCommand runs a chat command (quest next, quest start <id>) through PrivateChatRequest.
func (r *Runner) SendQuestCommand(content string) {
	if r.Host.Game.Handlers().HasRequest(protocol.IDPrivateChatRequest) {
		r.SendRequest(&protocol.PrivateChatRequest{Content: content})
		return
	}
	r.Host.Clock.Advance(MsPerRequest)
	r.Phase.Sends++
	e := r.Quests()
	if e == nil {
		return
	}
	if content == "quest next" {
		e.SkipActive()
	} else if strings.HasPrefix(content, "quest start ") {
		if q, ok := dotnetInt(content[len("quest start "):], 32); ok && e.Accept(int32(q)) {
			e.Save()
		}
	}
	r.Session().ApplyPendingTransfer()
	r.Session().ImmediateSpawnCheck()
	r.Host.DrainServerPushes(r.ProcessFrame)
}

func (r *Runner) MoveTo(pos world.Vec3) {
	net, ok := r.RoleNetID()
	if !ok {
		return
	}
	r.SendPush(&protocol.MovePackagePush{MovingEntities: []*protocol.MovingEntityData{{
		EntityId: net, Originator: net,
		MoveInfos: []*protocol.MoveReplaySample{{Location: pos.ToPb(), Rotation: &protocol.Rotator{}}},
	}}})
}

// EndFlows is R-89: ends the flows sent to the client, then the queued flows those ends released.
func (r *Runner) EndFlows() int {
	wasWaiting := map[int64]bool{}
	for _, f := range r.Session().Flows().Waiting() {
		wasWaiting[f.FlowIncId] = true
	}
	ending := append([]int64(nil), r.Flows...)
	r.Flows = nil
	count := 0
	for len(ending) > 0 {
		for _, inc := range ending {
			r.SendRequest(&protocol.FlowEndRequest{FlowIncId: inc})
		}
		count += len(ending)
		var next, keep []int64
		for _, inc := range r.Flows {
			if wasWaiting[inc] {
				next = append(next, inc)
			} else {
				keep = append(keep, inc)
			}
		}
		r.Flows = keep
		ending = next
	}
	return count
}

// MoveLifts is the client half of ElevatorMoveNotify: Forward (上升/下降), then End (停靠).
func (r *Runner) MoveLifts() {
	if len(r.Lifts) == 0 {
		return
	}
	moves := r.Lifts
	r.Lifts = nil
	stop := world.TagID(r.Tables(), "关卡.电梯.停靠")
	for _, m := range moves {
		name := "关卡.电梯.下降"
		if m.Floor >= 2 {
			name = "关卡.电梯.上升"
		}
		run := world.TagID(r.Tables(), name)
		r.SendRequest(&protocol.ElevatorStateChangeRequest{EntityId: m.EntityID, State: protocol.ElevatorState_ElevatorState_Forward, TagState: run})
		r.SendRequest(&protocol.ElevatorStateChangeRequest{EntityId: m.EntityID, State: protocol.ElevatorState_ElevatorState_End, TagState: stop})
	}
}

// KillMonsterNetID kills one monster through the server's real kill path.
func (r *Runner) KillMonsterNetID(net int64) {
	r.Host.Clock.Advance(MsPerRequest)
	r.Phase.Sends++
	s := r.Session()
	if c := s.Combat.Scene.GetEntity(net); c != nil {
		if c.Life() > 0 {
			c.Attributes.ApplyDelta(combat.AttrLife, -c.Life(), false)
		}
		s.Combat.Scene.UpdateCombatant(net, false, s.Sink())
		s.Combat.Kills.OnMonsterKilled(c, r.Host.Clock.RealMs(), s.Sink())
	} else if s.Scene != nil {
		if e := s.Scene.Get(net); e != nil {
			if h := s.Systems.Hooks(s); h != nil {
				h.OnMonsterKilled(&combat.CombatEntity{NetID: net, ConfigID: e.ConfigID, MapID: s.Scene.MapID, EntityType: e.EntityType})
			}
		}
	}
	s.ApplyPendingTransfer()
	s.ImmediateSpawnCheck()
	r.Host.DrainServerPushes(r.ProcessFrame)
}

// KillMonsters kills every live monster once in a dungeon (Level and Prefab rows).
func (r *Runner) KillMonsters(rep *DungeonReport) int {
	s := r.Session()
	sc := s.Scene
	if sc == nil {
		return 0
	}
	var victims []int64
	for _, e := range sc.Entities() {
		if e.EntityType != world.TypeMonster {
			continue
		}
		groupMonster := false
		if q := s.QuestState; q != nil {
			for _, g := range q.Prefabs.Groups() {
				for _, a := range g.Alive {
					if a == e.ID {
						groupMonster = true
					}
				}
			}
		}
		if e.ConfigType != world.ConfigLevel && e.ConfigType != world.ConfigPrefab && !groupMonster {
			continue
		}
		if r.Killed[e.ID] {
			continue
		}
		victims = append(victims, e.ID)
		var lv int32
		if ce := s.Combat.Scene.GetEntity(e.ID); ce != nil {
			lv = ce.Attributes.Slots[combat.AttrLv].Current
		}
		if r.Verbose {
			r.printf("robot: kill net %d config %d type %s prefab %d lv %d\n", e.ID, e.ConfigID, configTypeName(e.ConfigType), e.PrefabID, lv)
		}
		if rep.Monsters == 0 || lv < rep.LevelMin {
			rep.LevelMin = lv
		}
		if rep.Monsters == 0 || lv > rep.LevelMax {
			rep.LevelMax = lv
		}
		rep.Monsters++
	}
	for _, net := range victims {
		r.Killed[net] = true
		r.KillMonsterNetID(net)
	}
	return len(victims)
}

func configTypeName(t protocol.EntityConfigType) string {
	return strings.TrimPrefix(t.String(), "EntityConfigType_")
}

// KillConfigs is R-80e: kills the spawned monsters with these config ids (open-world Kill steps).
func (r *Runner) KillConfigs(configIDs []int64) {
	for _, id := range configIDs {
		net, ok := r.NetIDOf(id)
		if !ok {
			continue
		}
		r.Killed[net] = true
		r.KillMonsterNetID(net)
	}
}

// SendSelfEvents sends EntitySendEventRequest for every self event in the entity's ConditionListenerComponent.
func (r *Runner) SendSelfEvents(net, configID int64) {
	sc := r.Session().Scene
	if sc == nil || configID < math.MinInt32 || configID > math.MaxInt32 {
		return
	}
	row := r.Tables().LevelEntity(sc.MapID, int32(configID))
	if row == nil {
		return
	}
	comps, ok := row.ComponentsData.(*data.Object)
	if !ok {
		return
	}
	cl, ok := comps.Get("ConditionListenerComponent").(*data.Object)
	if !ok {
		return
	}
	listeners, ok := cl.Get("Listeners").(*data.Array)
	if !ok {
		return
	}
	for _, lv := range listeners.Items {
		l, ok := lv.(*data.Object)
		if !ok {
			continue
		}
		cond, ok := l.Get("Condition").(*data.Object)
		if !ok {
			continue
		}
		conds, ok := cond.Get("Conditions").(*data.Array)
		if !ok {
			continue
		}
		for _, cv := range conds.Items {
			if c, ok := cv.(*data.Object); ok {
				if key, ok := c.Get("EventKey").(data.String); ok && len(key) > 0 {
					r.SendRequest(&protocol.EntitySendEventRequest{EntityId: net, EventKey: string(key)})
				}
			}
		}
	}
}

// DriveState is R-80a / R-80e: drives target into want via StateSetters.json; "" when it found no way.
func (r *Runner) DriveState(target int64, want string, depth, skip int) string {
	sc := r.Session().Scene
	if depth > 2 || sc == nil {
		return ""
	}
	ways := r.StateSetters.WaysFor(sc.MapID, target, want)
	for idx := skip; idx < len(ways); idx++ {
		w := ways[idx]
		switch w.Kind {
		case "interact":
			net, ok := r.NetIDOf(w.ID)
			if !ok {
				if p, ok := r.RowPos(w.ID); ok {
					r.MoveTo(p)
				}
				continue
			}
			if p, ok := r.RowPos(w.ID); ok {
				r.MoveTo(p)
			}
			if r.Verbose {
				r.printf("robot: option %d of %d sets %d to %s\n", w.Option, w.ID, target, want)
			}
			r.SendRequest(&protocol.EntityInteractRequest{EntityId: net, OptionIndex: w.Option})
			r.StateSetters.Counters.Interact++
			return "interact"
		case "chain":
			if r.DriveState(w.ID, w.State, depth+1, 0) != "" {
				if r.Verbose {
					r.printf("robot: %d -> %s sets %d to %s\n", w.ID, w.State, target, want)
				}
				r.StateSetters.Counters.Chain++
				return "chain"
			}
		case "group":
			anyDriven := false
			for _, need := range w.Need {
				if need.State == "" || need.ID == target {
					continue
				}
				if r.DriveState(need.ID, need.State, depth+1, 0) != "" {
					anyDriven = true
				}
			}
			if anyDriven {
				if r.Verbose {
					r.printf("robot: 机关组 %d members driven for %d %s\n", w.ID, target, want)
				}
				r.StateSetters.Counters.Group++
				return "group"
			}
		case "client":
			tag := world.TagID(r.Tables(), want)
			if p, ok := r.RowPos(w.ID); ok {
				r.MoveTo(p)
			}
			if r.Verbose {
				r.printf("robot: client mechanism reports %d %s\n", w.ID, want)
			}
			r.SendRequest(&protocol.ChangeEntityStateRequest{EntityId: int32(w.ID), StateTagId: tag})
			r.StateSetters.Counters.Client++
			return "client"
		}
	}
	if depth == 0 {
		r.StateSetters.Counters.None++
	}
	return ""
}

func (r *Runner) WhereIs(configID int64) string {
	if _, ok := r.NetIDOf(configID); ok {
		return "spawned"
	}
	if _, ok := r.RowPos(configID); ok {
		return "this map: " + WhyNotSpawned(r.Session(), configID)
	}
	for _, row := range r.Tables().LevelEntityConfig {
		if int64(row.EntityId) == configID {
			return "map " + strconv.Itoa(int(row.MapId))
		}
	}
	return "nowhere"
}

func (r *Runner) DescribeQuestInto(questID int32, sb *strings.Builder) {
	e := r.Quests()
	if e == nil {
		sb.WriteString("(no active step)")
		return
	}
	start := sb.Len()
	for _, st := range e.ActiveQuestNodes(questID) {
		def := st.Def
		if !def.Is("ChildQuest") {
			continue
		}
		if sb.Len() > start {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(sb, "%d:%s", def.ID, def.Cond)
		if def.Cond == "Kill" {
			for _, t := range def.Targets {
				fmt.Fprintf(sb, "[%d=%s]", t, r.WhereIs(t))
			}
		}
		for _, o := range def.Options {
			fmt.Fprintf(sb, "[%d=%s]", o.EntityID, r.WhereIs(o.EntityID))
		}
		for _, sc := range def.States {
			fmt.Fprintf(sb, "[%d=%s,want %s]", sc.EntityID, r.WhereIs(sc.EntityID), sc.State)
		}
		if def.Dungeon != 0 {
			fmt.Fprintf(sb, "[dungeon %d, here %d]", def.Dungeon, r.InstanceID())
		}
	}
	if sb.Len() == start {
		sb.WriteString("(no active step)")
	}
}

// ----- decoding the server's frames -----

func decode[T any, PT interface {
	*T
	proto.Message
}](payload []byte) PT {
	m := PT(new(T))
	if err := proto.Unmarshal(payload, m); err != nil {
		panic(err)
	}
	return m
}

func errp(c protocol.ErrorCode) *protocol.ErrorCode { return &c }
func boolp(b bool) *bool                            { return &b }

// ProcessFrame decodes a frame the server pushed or answered into the robot's observations.
func (r *Runner) ProcessFrame(f *wire.Frame) {
	p := f.Payload
	switch f.MessageID {
	case protocol.IDFlowStartNotify:
		r.Flows = append(r.Flows, decode[protocol.FlowStartNotify](p).FlowIncId)
	case protocol.IDEnterLevelPlayNotify:
		n := decode[protocol.EnterLevelPlayNotify](p)
		r.LpEnter++
		id := n.Id
		r.LastEnteredLp = &id
	case protocol.IDLeaveLevelPlayNotify:
		r.LpLeave++
	case protocol.IDVehicleUpdateNotify:
		n := decode[protocol.VehicleUpdateNotify](p)
		r.RideVehicle = n.EntityId
		if n.SeatId < 0 {
			r.RidesOff++
		} else {
			r.RidesOn++
		}
	case protocol.IDActionNotify:
		r.LevelEvents++
	case protocol.IDTraceEntityPosResponse:
		resp := decode[protocol.TraceEntityPosResponse](p)
		r.EntityPosOK = resp.ErrorCode == protocol.ErrorCode_ErrorCode_Success
		r.EntityPosResult = nil
		if resp.Pos != nil {
			v := world.Vec3{X: resp.Pos.X, Y: resp.Pos.Y, Z: resp.Pos.Z}
			r.EntityPosResult = &v
		}
	case protocol.IDWeaponItemAddNotify:
		r.WeaponsAdded = nil
		for _, w := range decode[protocol.WeaponItemAddNotify](p).WeaponItemList {
			r.WeaponsAdded = append(r.WeaponsAdded, [4]int32{w.Id, w.WeaponLevel, w.WeaponBreach, w.WeaponResonLevel})
		}
	case protocol.IDNormalItemResponse:
		resp := decode[protocol.NormalItemResponse](p)
		unk := 0
		for _, it := range resp.NormalItemList {
			if _, ok := r.Tables().ItemInfoById[it.Id]; !ok {
				unk++
			}
		}
		r.BagList = &[2]int{len(resp.NormalItemList), unk}
	case protocol.IDNormalItemAddNotify:
		r.countPushedItems(decode[protocol.NormalItemAddNotify](p).NormalItemList)
	case protocol.IDNormalItemUpdateNotify:
		r.countPushedItems(decode[protocol.NormalItemUpdateNotify](p).NormalItemList)
	case protocol.IDPayShopBuyResponse:
		resp := decode[protocol.PayShopBuyResponse](p)
		r.LastError = errp(resp.ErrorCode)
		r.ShopOK = boolp(resp.ErrorCode == protocol.ErrorCode_ErrorCode_Success)
	case protocol.IDLandingDamageResponse:
		r.LandingOK = boolp(decode[protocol.LandingDamageResponse](p).ErrorCode == protocol.ErrorCode_ErrorCode_Success)
	case protocol.IDEntityPatrolStartResponse:
		r.countPatrol(decode[protocol.EntityPatrolStartResponse](p).Code)
	case protocol.IDEntityPatrolStopResponse:
		r.countPatrol(decode[protocol.EntityPatrolStopResponse](p).Code)
	case protocol.IDEntityPatrolChangeDirResponse:
		r.countPatrol(decode[protocol.EntityPatrolChangeDirResponse](p).Code)
	case protocol.IDMonsterPatrolPointReachedResponse:
		r.countPatrol(decode[protocol.MonsterPatrolPointReachedResponse](p).ErrorCode)
	case protocol.IDSitChairResponse:
		r.SitOK = boolp(decode[protocol.SitChairResponse](p).ErrorCode == protocol.ErrorCode_ErrorCode_Success)
	case protocol.IDAdventureManualResponse:
		u := decode[protocol.AdventureManualResponse](p).DetectionUnlocks
		r.printf("robot: sola guide unlocks %d dungeon, %d monster, %d silent area entries\n",
			len(u.GetDungeonDetectionIds()), len(u.GetMonsterDetectionIds()), len(u.GetSilentAreaDetectionIds()))
	case protocol.IDActivityResponse:
		r.reportActivities(decode[protocol.ActivityResponse](p))
	case protocol.IDInitRangeResponse:
		resp := decode[protocol.InitRangeResponse](p)
		r.LastError = errp(resp.ErrorCode)
		r.RangeReply = &rangeReplyInfo{resp.RangeEntityId, len(resp.EntitiesAccessRangeResult), resp.PlayerAccessRangeResult != nil}
	case protocol.IDEntityAccessRangeResponse:
		resp := decode[protocol.EntityAccessRangeResponse](p)
		r.LastError = errp(resp.ErrorCode)
		r.RangeReply = &rangeReplyInfo{resp.RangeEntityId, len(resp.EntitiesAccessRangeResult), false}
	case protocol.IDPlayerAccessRangeResponse:
		resp := decode[protocol.PlayerAccessRangeResponse](p)
		r.LastError = errp(resp.ErrorCode)
		r.RangeReply = &rangeReplyInfo{resp.RangeEntityId, 0, resp.AccessRangeResult != nil}
	case protocol.IDTimePointActivityRewardResponse:
		r.LastError = errp(decode[protocol.TimePointActivityRewardResponse](p).ErrorCode)
	case protocol.IDRoguelikeWriteCatBossRewardResponse:
		r.LastError = errp(decode[protocol.RoguelikeWriteCatBossRewardResponse](p).ErrorCode)
	case protocol.IDRoguelikeRoleInstStartResponse:
		r.LastError = errp(decode[protocol.RoguelikeRoleInstStartResponse](p).ErrorCode)
	case protocol.IDSignActivityResponse:
		resp := decode[protocol.SignActivityResponse](p)
		r.LastError = errp(resp.ErrorCode)
		var items []string
		for k, v := range resp.ItemMap {
			items = append(items, fmt.Sprintf("%dx%d", k, v))
		}
		r.printf("robot: sign claim: %s, items: %s\n", errName(resp.ErrorCode), strings.Join(items, " "))
	case protocol.IDRunTurntableActivityResponse:
		resp := decode[protocol.RunTurntableActivityResponse](p)
		r.LastError = errp(resp.ErrorCode)
		r.TurntableReward, r.TurntableItems, r.TurntableRoundEnd = resp.RewardId, len(resp.ItemMap), resp.IsFinishCurRound
	case protocol.IDScratchCardActivityInfoResponse:
		resp := decode[protocol.ScratchCardActivityInfoResponse](p)
		r.LastError = errp(resp.Error)
		if resp.Info != nil {
			r.reportScratch(0, resp.Info)
		}
	case protocol.IDScratchCardRewardResponse:
		resp := decode[protocol.ScratchCardRewardResponse](p)
		r.LastError = errp(resp.Error)
		r.ScratchOpenType = resp.OpenType
		r.ScratchOpened = nil
		for k := range resp.OpenIndex {
			r.ScratchOpened = append(r.ScratchOpened, k)
		}
		r.ScratchGot = len(resp.ItemMap)
	case protocol.IDGachaInfoResponse:
		resp := decode[protocol.GachaInfoResponse](p)
		var ids []string
		for _, g := range resp.GachaInfos {
			ids = append(ids, strconv.Itoa(int(g.Id)))
		}
		r.printf("robot: gacha page shows %d banners: %s\n", len(resp.GachaInfos), strings.Join(ids, " "))
	case protocol.IDGetDetectionTargetResponse:
		resp := decode[protocol.GetDetectionTargetResponse](p)
		targets := 0
		for _, d := range resp.DetectionTargetDatas {
			targets += len(d.TargetIds)
		}
		r.printf("robot: detection %d: %d targets on %d maps\n", resp.DetectionId, targets, len(resp.DetectionTargetDatas))
	case protocol.IDElevatorMoveNotify:
		n := decode[protocol.ElevatorMoveNotify](p)
		r.Lifts = append(r.Lifts, liftMove{n.EntityId, n.Location})
	case protocol.IDMapMarkInfoNotify:
		n := decode[protocol.MapMarkInfoNotify](p)
		r.MarksShown = len(n.ShowMarkIds)
		for _, m := range n.ShowMarkIds {
			if m.MarkId == 301203 {
				r.TowerMarkShown = true
			}
		}
	case protocol.IDAreaTerminalInfoNotify:
		r.TerminalGroups = len(decode[protocol.AreaTerminalInfoNotify](p).AreaMapGroup)
	case protocol.IDPhantomMusicInfoResponse:
		resp := decode[protocol.PhantomMusicInfoResponse](p)
		r.MusicAlbums = len(resp.AlbumTimeInfos)
		r.MusicSongs = 0
		for _, a := range resp.AlbumTimeInfos {
			r.MusicSongs += len(a.MusicIds)
		}
	case protocol.IDPayShopInfoResponse:
		for _, s := range decode[protocol.PayShopInfoResponse](p).Infos {
			r.PayShopItems += len(s.Items)
		}
	case protocol.IDAchievementInfoResponse:
		resp := decode[protocol.AchievementInfoResponse](p)
		r.AchEntries = 0
		for _, g := range resp.AchievementGroupInfoList {
			for _, e := range g.AchievementEntryList {
				r.AchEntries++
				if e.Id == AchievementChecked {
					r.AchTargetTime, r.AchTargetReceived = e.FinishTime, e.IsReceive
				}
			}
		}
		r.AchFinished, r.AchStars = resp.FinishCount, resp.StarCount
	case protocol.IDAchievementProgressNotify:
		r.AchNotifies++
	case protocol.IDAchievementFinishResponse:
		r.LastError = errp(decode[protocol.AchievementFinishResponse](p).ErrorCode)
	case protocol.IDAchievementReceiveResponse:
		r.LastError = errp(decode[protocol.AchievementReceiveResponse](p).ErrorCode)
	case protocol.IDTowerResponse:
		t := decode[protocol.TowerResponse](p).TowerInfo
		if t == nil {
			break
		}
		r.TowerFloors = 0
		var first *protocol.TowerFloorPb
		for _, d := range t.TowerDifficulties {
			for _, a := range d.TowerAreas {
				for _, fl := range a.TowerFloors {
					r.TowerFloors++
					if d.Difficulty == 1 && (first == nil || fl.TowerConfigId < first.TowerConfigId) {
						first = fl
					}
				}
			}
		}
		if first != nil {
			r.TowerFirst, r.TowerFirstStars = first.TowerConfigId, first.Star
		}
	case protocol.IDTowerStartResponse:
		r.LastError = errp(decode[protocol.TowerStartResponse](p).ErrorCode)
	case protocol.IDTowerRewardResponse:
		r.LastError = errp(decode[protocol.TowerRewardResponse](p).ErrorCode)
	case protocol.IDLevelPlayStateListResponse:
		resp := decode[protocol.LevelPlayStateListResponse](p)
		r.LastError = errp(resp.ErrorCode)
		r.LevelPlayStates = 0
		for _, x := range resp.InstLevelPlayStateRsps {
			r.LevelPlayStates += len(x.LevelPlayStateDict)
		}
	case protocol.IDLastBigSceneMiniMapInfoResponse:
		resp := decode[protocol.LastBigSceneMiniMapInfoResponse](p)
		r.LastError = errp(resp.Code)
		r.MinimapPos = &world.Vec3{X: resp.PosX, Y: resp.PosY, Z: resp.PosZ}
	case protocol.IDQuestRedDotResponse:
		r.LastError = errp(decode[protocol.QuestRedDotResponse](p).ErrorId)
	case protocol.IDTraceQuestResponse:
		r.LastError = errp(decode[protocol.TraceQuestResponse](p).ErrorId)
	case protocol.IDTowerEndNotify:
		r.TowerEnd = boolp(decode[protocol.TowerEndNotify](p).Success)
	case protocol.IDPbGetRoleListNotify:
		for _, ri := range decode[protocol.PbGetRoleListNotify](p).RoleList {
			r.RolesNotified = append(r.RolesNotified, ri.RoleId)
		}
	case protocol.IDBossRushResultNotify:
		hs := decode[protocol.BossRushResultNotify](p).HighScore
		r.BossRushScore = &hs
	case protocol.IDBossRushClaimRewardResponse:
		r.LastError = errp(decode[protocol.BossRushClaimRewardResponse](p).ErrorCode)
	case protocol.IDLordGymInfoResponse:
		resp := decode[protocol.LordGymInfoResponse](p)
		r.LordGymUnlocked, r.LordGymPassed = len(resp.UnlockLoadGymIds), len(resp.LordGymPassRecords)
	case protocol.IDLordGymBeginResponse:
		r.LastError = errp(decode[protocol.LordGymBeginResponse](p).ErrorCode)
	case protocol.IDLordGymLevelPlayResultNotify:
		r.LordGymResult = decode[protocol.LordGymLevelPlayResultNotify](p)
	case protocol.IDInstSettleNotify:
		r.SettleRate = decode[protocol.InstSettleNotify](p).Magnification
	case protocol.IDActivityUpdateNotify:
		for _, act := range decode[protocol.ActivityUpdateNotify](p).Activities {
			if dbl := act.GetDoubleInstActivityReward(); dbl != nil {
				u := dbl.GetDoubleInstRwdCount
				r.UpdateUsed = &u
			}
		}
	case protocol.IDQuestReadyListNotify:
		r.Offered = append(r.Offered, decode[protocol.QuestReadyListNotify](p).QuestId...)
	case protocol.IDBehaviorTreeInfoNotify:
		for _, t := range decode[protocol.BehaviorTreeInfoNotify](p).TreeInfos {
			r.TreeInfosSeen++
			r.ClientTrees[t.TreeIncId] = true
			for k := range t.Nodes {
				key := treeNode{t.TreeIncId, k}
				if r.ClientNodes[key] {
					r.TreeDupNodes++
				}
				r.ClientNodes[key] = true
			}
		}
	case protocol.IDBehaviorTreeDeleteNotify:
		for _, tree := range decode[protocol.BehaviorTreeDeleteNotify](p).TreeIncIds {
			r.dropClientTree(tree)
		}
	case protocol.IDUpdateNodeStatusNotify:
		n := decode[protocol.UpdateNodeStatusNotify](p)
		r.clientNodeStatus(n.TreeIncId, n.NodeId)
	case protocol.IDUpdateChildQuestNodeStatusNotify:
		n := decode[protocol.UpdateChildQuestNodeStatusNotify](p)
		r.clientNodeStatus(n.TreeIncId, n.NodeId)
	case protocol.IDEntityAddDynamicInteractNotify:
		r.OptionNets = append(r.OptionNets, decode[protocol.EntityAddDynamicInteractNotify](p).EntityId)
	case protocol.IDQuestStateUpdateNotify:
		n := decode[protocol.QuestStateUpdateNotify](p)
		if n.State == protocol.QuestState_QuestState_Progress {
			r.InProgress = append(r.InProgress, n.QuestId)
		}
	case protocol.IDInstResultNotify:
		r.DungeonResult = boolp(decode[protocol.InstResultNotify](p).Succ)
	case protocol.IDEnterInstResponse:
		r.LastError = errp(decode[protocol.EnterInstResponse](p).ErrorCode)
	case protocol.IDGetInstExchangeRewardResponse:
		r.LastError = errp(decode[protocol.GetInstExchangeRewardResponse](p).ErrorCode)
	}
}

// errName is C#'s ErrorCode.ToString(): the name without protoc's prefix.
func errName(c protocol.ErrorCode) string { return strings.TrimPrefix(c.String(), "ErrorCode_") }

func (r *Runner) countPushedItems(items []*protocol.NormalItem) {
	for _, it := range items {
		r.PushedItemsTotal++
		if _, ok := r.Tables().ItemInfoById[it.Id]; !ok {
			r.PushedItemsUnknown++
		}
	}
}

func (r *Runner) countPatrol(c protocol.ErrorCode) {
	if c == protocol.ErrorCode_ErrorCode_Success {
		r.PatrolOK++
	} else {
		r.PatrolBad++
	}
}

func (r *Runner) clientNodeStatus(tree int64, node int32) {
	if !r.ClientTrees[tree] {
		r.TreeOrphanUpdates++
		return
	}
	r.ClientNodes[treeNode{tree, node}] = true
}

func (r *Runner) dropClientTree(tree int64) {
	delete(r.ClientTrees, tree)
	for k := range r.ClientNodes {
		if k.Tree == tree {
			delete(r.ClientNodes, k)
		}
	}
}

func (r *Runner) reportActivities(res *protocol.ActivityResponse) {
	withData := 0
	for _, act := range res.Activities {
		if act.Data == nil {
			continue
		}
		withData++
		switch d := act.Data.(type) {
		case *protocol.ActivityData_SignActivity:
			id := act.Id
			r.SignActivity = &id
			r.SignStates = append([]protocol.SignState(nil), d.SignActivity.GetSignStateList()...)
		case *protocol.ActivityData_TimePointRewardActivityData:
			r.TimePoints = nil
			for _, rw := range d.TimePointRewardActivityData.GetRewards() {
				r.TimePoints = append(r.TimePoints, timePointEntry{rw.Id, rw.CanGetReward, rw.Rewarded})
			}
		case *protocol.ActivityData_BossRushActivityData:
			br := d.BossRushActivityData
			r.BossRushLevels = len(br.GetBossRushLevelInfo())
			if r.BossRushLevels > 0 {
				r.BossRushFirst = br.BossRushLevelInfo[0].InstId
			}
		case *protocol.ActivityData_RogueWhiteCatData:
			dl := d.RogueWhiteCatData
			r.DreamInsts, r.DreamRuns = len(dl.GetRogueRoleInstData()), len(dl.GetRogueLevelPlayData())
			r.DreamBossRewards, r.DreamLimited, r.DreamButtons = len(dl.GetBossRewardStateList()), len(dl.GetRogueLimitedAwards()), len(dl.GetUnlockButtons())
			if len(dl.GetBossRewardStateList()) > 0 {
				r.DreamBossFirst = dl.BossRewardStateList[0]
			}
		case *protocol.ActivityData_TurntableActivityData:
			tt := d.TurntableActivityData
			id := act.Id
			r.TurntableID = &id
			r.TurntableRound, r.TurntableClaimed, r.TurntableAllDone = tt.GetCurRound(), len(tt.GetRewardedItem()), tt.GetIsAllFinish()
			r.TurntableTasks = append([]*protocol.ActivityTask(nil), tt.GetTurntableTasks()...)
		case *protocol.ActivityData_ScratchCardActivityInfo:
			r.reportScratch(act.Id, d.ScratchCardActivityInfo)
		case *protocol.ActivityData_DoubleInstActivityReward:
			if row, ok := r.Tables().DoubleRewardActivityById[act.Id]; ok && row.Type == 2 {
				id := act.Id
				r.DoubleID = &id
				r.DoubleUsed = d.DoubleInstActivityReward.GetGetDoubleInstRwdCount()
			}
		}
	}
	r.printf("robot: activity page: %d activities, %d with data\n", len(res.Activities), withData)
}

func (r *Runner) reportScratch(id int32, sc *protocol.ScratchCardActivityInfo) {
	r.ScratchRounds = len(sc.RoundInfos)
	if len(sc.RoundInfos) > 0 {
		first := sc.RoundInfos[0]
		r.ScratchFirstRound, r.ScratchFirstCells, r.ScratchFirstLeft = first.RoundId, len(first.RewardIds), len(first.LeftRewardItem)
	}
	r.ScratchTimes = append([]*protocol.ScratchCardTimeInfo(nil), sc.ScratchTimeInfos...)
	r.printf("robot: scratch card %d: %d rounds, round %d: %d cells open, %d items left\n", id, r.ScratchRounds, r.ScratchFirstRound, r.ScratchFirstCells, r.ScratchFirstLeft)
}

// fnv64 is a deterministic 64-bit FNV-1a hash.
type fnv64 struct{ h uint64 }

func newFnv64() fnv64 { return fnv64{14695981039346656037} }

func (f *fnv64) addU(v uint64) {
	for i := 0; i < 8; i++ {
		f.h ^= uint64(byte(v >> (i * 8)))
		f.h *= 1099511628211
	}
}

func (f *fnv64) addLong(v int64) { f.addU(uint64(v)) }
func (f *fnv64) addInt(v int32)  { f.addU(uint64(uint32(v))) }
func (f *fnv64) addOptBool(v *bool) {
	switch {
	case v == nil:
		f.addU(2)
	case *v:
		f.addU(1)
	default:
		f.addU(0)
	}
}
func (f *fnv64) value() uint64 { return f.h }

func hashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range []byte(s) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

// f0 is .NET's ToString("F0") of a float: rounded half away from zero.
func f0(x float32) string { return strconv.FormatFloat(math.Round(float64(x)), 'f', 0, 64) }
