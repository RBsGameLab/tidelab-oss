// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
	"tidelab/protocol"
)

const (
	// LevelPlayKey separates level play TreeIncIds (LevelPlayKey | id) from quest ids.
	LevelPlayKey int64 = LevelPlayBit
	// MaxTimerMs caps Timer child quests at 60 seconds.
	MaxTimerMs int64 = 60 * 1000
	// LevelPlayOpen is ELevelPlayState.Open, sent in EnterLevelPlayNotify (R-92).
	LevelPlayOpen int32 = 2
	// RideReplayDelayMs waits after the vehicle and player are both present for client load (R-99).
	RideReplayDelayMs int64 = 2000
)

// NodeState holds one node's mutable state inside a Run.
type NodeState struct {
	Status protocol.NodeStatus
	Cq     protocol.ChildQuestNodeStatus
	// Count: ParallelSelect children done; Kill targets killed; FinishDungeon finishes.
	Count int32
	// Cursor: Sequence index of running child; FinishDungeon 1 once the player was in the dungeon.
	Cursor int32
}

func (s NodeState) IsDefault() bool {
	return s.Status == protocol.NodeStatus_NodeStatus_NotActive &&
		s.Cq == protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_NotActive &&
		s.Count == 0 && s.Cursor == 0
}

// Reached reports whether the node has been reached and should be sent to the client (R-117).
func (s NodeState) Reached() bool {
	return s.Status != protocol.NodeStatus_NodeStatus_NotActive ||
		s.Cq != protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_NotActive
}

// Run is an active quest or level play tree execution.
type Run struct {
	Tree          *QuestTree
	Nodes         []NodeState
	Key           int64
	LevelPlay     bool
	Done          bool
	ClientHasTree bool
}

func newRun(tree *QuestTree, key int64, levelPlay bool) *Run {
	return &Run{
		Tree:      tree,
		Nodes:     make([]NodeState, len(tree.Def.Nodes)),
		Key:       key,
		LevelPlay: levelPlay,
	}
}

func (r *Run) ID() int32 { return r.Tree.Def.ID }

func (r *Run) BtType() protocol.BtType {
	if r.LevelPlay {
		return protocol.BtType_BtType_BtTypeLevelPlay
	}
	return protocol.BtType_BtType_BtTypeQuest
}

func (r *Run) Ref() RunRef {
	return RunRef{Key: r.Key, TreeID: r.ID(), LevelPlay: r.LevelPlay}
}

// TakeTreeSend returns true the first time in a session (send the tree now), false after (R-124).
func (r *Run) TakeTreeSend() bool {
	send := !r.ClientHasTree
	r.ClientHasTree = true
	return send
}

// Skipped records a level-play child quest completed without checking its condition.
type Skipped struct {
	Tree int32
	Node int32
	Cond string
}

// Step describes an active node of a running tree (for tooling/robots).
type Step struct {
	Run       int64
	Tree      int32
	Def       NodeDef
	LevelPlay bool
}

// FixStats counts fix processor activity (R-82a).
type FixStats struct {
	Run           int
	Finished      int
	Moved         int
	Transfers     int
	ClientSkipped int
}

// LevelPlayVar is an initial Self variable passed to StartLevelPlay.
type LevelPlayVar struct {
	Name  string
	Value float64
}

type flowWait struct {
	run  int64
	node int
}

type timerWait struct {
	run   int64
	node  int
	dueMs int64
}

type fixKey struct {
	run  int64
	node int
	fix  int
}

// Engine runs each quest's behaviour tree (QuestNodeData) and each level play's on the server,
// for one player. Ported from QuestEngine*.cs.
type Engine struct {
	cat  *Catalog
	host Host
	sys  Systems
	Vars *BtVariables

	// Ordered with swap-remove (runs) and omap (.NET HashSet order) to match C#.
	runList          []*Run
	runs             map[int64]*Run
	finished         omap.Map[int32, struct{}]
	worldDone        omap.Map[int32, struct{}]
	dungeonsDone     omap.Map[int32, struct{}]
	lpFinished       omap.Map[int32, struct{}]
	ready            []int32
	acceptFlows      map[int64]int32
	optionFlows      map[int64]int64
	flows            map[int64]flowWait
	timers           []timerWait
	log              *QuestLog
	fixLastMs        map[fixKey]int64
	dungeonKills     map[int64]struct{}
	dungeonKillsInst int32
	nextFlowInc      int64

	playerPos              *[3]float32
	rangeWaiting           bool
	rangeCheckedAt         *[3]float32
	battleWaiting          bool
	questConditionsWaiting bool
	condScanMs             int64
	fixScanMs              int64
	rechecking             bool
	recheckAgain           bool
	actorsCleaned          bool
	rideReplayPending      bool
	rideReplayAtMs         *int64
	inventoryVersion       int
	actors                 map[int64]struct{}
	ownedActors            map[int64]struct{}

	// Public hooks & stats
	ClockOffsetMs    int64
	OffersEnabled    bool
	PlayerMale       *bool
	RestorePending   bool
	InventoryVersion func() int
	InAnyRange       func(mapID int32, ids []int64, pos [3]float32) (bool, bool)

	SkippedSteps         []Skipped
	LastStartedLevelPlay *int32
	BattleStepsDone      int
	Teleports            int
	TeleportsHeld        int
	HandIns              int
	FixStats             FixStats
}

func NewEngine(catalog *Catalog, host Host, systems Systems) *Engine {
	if systems == nil {
		systems = NoSystems{}
	}
	vars := NewBtVariables()
	catalog.Prepare(vars)
	return &Engine{
		cat:              catalog,
		host:             host,
		sys:              systems,
		Vars:             vars,
		runs:             map[int64]*Run{},
		acceptFlows:      map[int64]int32{},
		optionFlows:      map[int64]int64{},
		flows:            map[int64]flowWait{},
		log:              NewQuestLog(),
		fixLastMs:        map[fixKey]int64{},
		dungeonKills:     map[int64]struct{}{},
		nextFlowInc:      500000,
		inventoryVersion: -1,
		OffersEnabled:    true,
	}
}

func (e *Engine) Catalog() *Catalog       { return e.cat }
func (e *Engine) NowMs() int64            { return e.host.NowMs() + e.ClockOffsetMs }
func (e *Engine) Runs() []*Run            { return e.runList }
func (e *Engine) Finished() []int32       { return e.finished.Keys() }
func (e *Engine) OptionFlowsPending() int { return len(e.optionFlows) }

// -----------------------------------------------------------------------------
// Queries

func (e *Engine) IsFinished(quest int32) bool { return e.finished.Has(quest) }

func (e *Engine) IsRunning(quest int32) bool {
	_, ok := e.runs[int64(quest)]
	return ok
}

func (e *Engine) IsLevelPlayRunning(lp int32) bool {
	_, ok := e.runs[LevelPlayRun(lp)]
	return ok
}

func (e *Engine) IsLevelPlayFinished(lp int32) bool { return e.lpFinished.Has(lp) }

func (e *Engine) WorldPlayDone(id int32) bool { return e.worldDone.Has(id) }

func (e *Engine) DungeonCleared(id int32) bool { return e.dungeonsDone.Has(id) }

// LevelPlayStateOf returns ELevelPlayState (0 Close, 2 Open, 3 Finish) (R-87).
func (e *Engine) LevelPlayStateOf(lp int32) int32 {
	if e.lpFinished.Has(lp) {
		return 3
	}
	if e.IsLevelPlayRunning(lp) {
		return 2
	}
	return 0
}

// IsNodeDone reports whether node of running quest completed (R-51).
func (e *Engine) IsNodeDone(quest, node int32) bool {
	run, ok := e.runs[int64(quest)]
	if !ok {
		return false
	}
	i, ok := run.Tree.IndexOf(node)
	if !ok {
		return false
	}
	st := run.Nodes[i].Status
	return st == protocol.NodeStatus_NodeStatus_CompletedSuccess || st == protocol.NodeStatus_NodeStatus_CompletedFailed
}

// IsChildDone reports whether child quest node of quest is done (R-80h: a running tree is the
// authority on its own nodes; "quest finished" answers only when not running).
func (e *Engine) IsChildDone(quest, node int32) bool {
	if run, ok := e.runs[int64(quest)]; ok {
		if i, found := run.Tree.IndexOf(node); found {
			st := run.Nodes[i].Status
			return st == protocol.NodeStatus_NodeStatus_CompletedSuccess || st == protocol.NodeStatus_NodeStatus_CompletedFailed
		}
		return e.IsFinished(quest)
	}
	return e.IsFinished(quest)
}

// EntityState returns the saved state override of configID, else its spawned state (R-43).
func (e *Engine) EntityState(configID int64) (int32, bool) {
	if s, ok := e.host.World().StateOverride(configID); ok {
		return s, true
	}
	return e.host.SpawnedState(configID)
}

// UsedByRunning reports whether an active quest/level play talks to, kills, or checks state of id (R-80c).
func (e *Engine) UsedByRunning(id int64) bool {
	for _, run := range e.runList {
		if run.Done {
			continue
		}
		for _, n := range run.Tree.Def.Nodes {
			for _, o := range n.Options {
				if o.EntityID == id {
					return true
				}
			}
			if slices.Contains(n.Targets, id) {
				return true
			}
			for _, s := range n.States {
				if s.EntityID == id {
					return true
				}
			}
		}
	}
	return false
}

// ActiveSteps returns the active ChildQuest steps of every running tree (R-3b).
func (e *Engine) ActiveSteps(levelPlaysOnly bool) []Step {
	var out []Step
	for _, run := range e.runList {
		if run.Done || (levelPlaysOnly && !run.LevelPlay) {
			continue
		}
		for i := range run.Nodes {
			if run.Tree.Node(i).Is("ChildQuest") && run.Nodes[i].Status == protocol.NodeStatus_NodeStatus_Activated {
				out = append(out, Step{Run: run.Key, Tree: run.ID(), Def: run.Tree.Node(i), LevelPlay: run.LevelPlay})
			}
		}
	}
	return out
}

// ActiveQuestNodes returns every active node (composites too) of one running quest.
func (e *Engine) ActiveQuestNodes(quest int32) []Step {
	run, ok := e.runs[int64(quest)]
	if !ok || run.LevelPlay || run.Done {
		return nil
	}
	var out []Step
	for i := range run.Nodes {
		if run.Nodes[i].Status == protocol.NodeStatus_NodeStatus_Activated {
			out = append(out, Step{Run: run.Key, Tree: run.ID(), Def: run.Tree.Node(i), LevelPlay: false})
		}
	}
	return out
}

func (e *Engine) RunningQuestIDs() []int32 {
	var out []int32
	for _, r := range e.runList {
		if !r.LevelPlay && !r.Done {
			out = append(out, r.ID())
		}
	}
	return out
}

func (e *Engine) RunningLevelPlays() []int32 {
	var out []int32
	for _, r := range e.runList {
		if r.LevelPlay && !r.Done {
			out = append(out, r.ID())
		}
	}
	return out
}

// NextDue returns when the next Timer step or spawner wave is due.
func (e *Engine) NextDue() (int64, bool) {
	var best int64
	found := false
	for _, t := range e.timers {
		if !found || t.dueMs < best {
			best = t.dueMs
			found = true
		}
	}
	if d, ok := e.sys.SpawnerNextDue(); ok && (!found || d < best) {
		best = d
		found = true
	}
	return best, found
}

// WorldPlaysOn returns the world plays that would start on mapID (R-62).
func (e *Engine) WorldPlaysOn(mapID int32) []WorldPlay {
	live := liveProgress{e: e}
	var out []WorldPlay
	for _, wp := range e.cat.WorldPlays() {
		if !wp.Sub && wp.LevelID == mapID && wp.Radius > 0 && !e.WorldPlayDone(wp.ID) && WorldPlayOpen(wp, live) {
			out = append(out, wp)
		}
	}
	return out
}

// LevelPlayForDungeon picks the level play of InstanceDungeon dungeonID.
func (e *Engine) LevelPlayForDungeon(dungeonID int32) (int32, bool) {
	if lp, ok := e.sys.DungeonLevelPlay(dungeonID); ok {
		return lp, true
	}
	t := e.host.Tables()
	if t == nil {
		return 0, false
	}
	d, ok := t.InstanceDungeonById[dungeonID]
	if !ok || d == nil {
		return 0, false
	}
	return e.cat.LevelPlayFor(d.MapConfigId, d.MonsterPreview)
}

func (e *Engine) runOf(key int64) *Run { return e.runs[key] }

// -----------------------------------------------------------------------------
// Pushes

func treeCtx(run *Run, idx int) *protocol.BehaviorTreeCtxPb {
	return &protocol.BehaviorTreeCtxPb{
		IncId:  run.Key,
		BtType: int32(run.BtType()),
		BtId:   run.ID(),
		NodeId: run.Tree.Node(idx).ID,
	}
}

func (e *Engine) setStatus(run *Run, idx int, status protocol.NodeStatus) {
	run.Nodes[idx].Status = status
	e.host.Push(&protocol.UpdateNodeStatusNotify{
		TreeOwnerId: e.host.PlayerID(),
		TreeIncId:   run.Key,
		NodeId:      run.Tree.Node(idx).ID,
		Status:      status,
	})
}

func (e *Engine) setChildStatus(run *Run, idx int, status protocol.ChildQuestNodeStatus) {
	run.Nodes[idx].Cq = status
	e.host.Push(&protocol.UpdateChildQuestNodeStatusNotify{
		TreeOwnerId: e.host.PlayerID(),
		TreeIncId:   run.Key,
		NodeId:      run.Tree.Node(idx).ID,
		Status:      status,
	})
}

// TreeInfoOf builds the BehaviorTreeInfo TreeInfo containing only reached nodes (R-117).
func (e *Engine) TreeInfoOf(run *Run) *protocol.TreeInfo {
	info := &protocol.TreeInfo{
		TreeOwnerId:  e.host.PlayerID(),
		TreeIncId:    run.Key,
		BtType:       run.BtType(),
		BlackboardId: run.ID(),
		Nodes:        map[int32]*protocol.NodeInfo{},
	}
	for i := range run.Nodes {
		st := run.Nodes[i]
		if !st.Reached() {
			continue
		}
		node := &protocol.NodeInfo{Status: st.Status}
		if run.Tree.Node(i).Is("ChildQuest") {
			node.ExtraInfo = &protocol.NodeInfo_ChildQuestNodeInfo{
				ChildQuestNodeInfo: &protocol.ChildQuestNodeInfo{Status: st.Cq},
			}
		}
		info.Nodes[run.Tree.Node(i).ID] = node
	}
	return info
}

func (e *Engine) enterLevelPlay(run *Run) {
	if run.LevelPlay {
		e.host.Push(&protocol.EnterLevelPlayNotify{Id: run.ID(), State: LevelPlayOpen, CanGetReward: true})
	}
}

func (e *Engine) leaveLevelPlay(run *Run) {
	if run.LevelPlay {
		e.host.Push(&protocol.LeaveLevelPlayNotify{Id: run.ID()})
	}
}

func (e *Engine) pushOption(run *Run, idx int, option QuestOption, netID int64) {
	common.Info("quest", fmt.Sprintf("quest %d node %d: option on %d (net %d)", run.ID(), run.Tree.Node(idx).ID, option.EntityID, netID))
	// A new quest step on this entity: drop any interaction lock an earlier step left behind.
	e.host.Push(&protocol.EntityInteractingNotify{EntityId: netID, IsInteracting: false})
	bt := treeCtx(run, idx)
	var ctx *protocol.GameCtxPb
	if run.Tree.Node(idx).Cond == "HandInItems" {
		ctx = &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_HandInItemChildQuestNode,
			CtxInfo: &protocol.GameCtxPb_HandInItemChildQuestNode{
				HandInItemChildQuestNode: &protocol.HandInItemChildQuestNodeCtxPb{BehaviorTreeCtx: bt},
			},
		}
	} else {
		ctx = &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_DoInteractChildQuestNode,
			CtxInfo: &protocol.GameCtxPb_DoInteractChildQuestNode{
				DoInteractChildQuestNode: &protocol.DoInteractChildQuestNodeCtxPb{BehaviorTreeCtx: bt},
			},
		}
	}
	e.host.Push(&protocol.EntityAddDynamicInteractNotify{
		EntityId:   netID,
		OptionGuid: option.Guid,
		GameCtx:    ctx,
		Text:       option.TidContent,
	})
}

func acceptCtx(quest int32) *protocol.GameCtxPb {
	return &protocol.GameCtxPb{
		CtxType: protocol.GameCtxType_GameCtxType_QuestAcceptAction,
		CtxInfo: &protocol.GameCtxPb_QuestAcceptAction{
			QuestAcceptAction: &protocol.QuestAcceptActionCtxPb{QuestId: quest},
		},
	}
}

func (e *Engine) pushAcceptOption(quest int32, opt AcceptOption, netID int64) {
	common.Info("quest", fmt.Sprintf("quest %d: accept option on %d (net %d)", quest, opt.EntityID, netID))
	e.host.Push(&protocol.EntityAddDynamicInteractNotify{
		EntityId:   netID,
		OptionGuid: opt.Guid,
		GameCtx:    acceptCtx(quest),
		Text:       opt.TidContent,
	})
}

func (e *Engine) removeOptions(def NodeDef) {
	for _, o := range def.Options {
		if net, ok := e.host.NetIDOf(o.EntityID); ok {
			e.host.Push(&protocol.EntityRemoveDynamicInteractNotify{EntityId: net, OptionGuid: o.Guid})
		}
	}
}

func (e *Engine) wake(ids []int64) {
	w := e.host.World()
	for _, id := range ids {
		w.Wake(id)
	}
	w.Save()
	e.host.RefreshSpawns()
}

func (e *Engine) newFlow(flow FlowRef, ctx *protocol.GameCtxPb) *protocol.FlowStartNotify {
	e.nextFlowInc++
	return &protocol.FlowStartNotify{
		FlowIncId:    e.nextFlowInc,
		FlowListName: flow.FlowListName,
		FlowId:       flow.FlowID,
		StateId:      flow.StateID,
		GameCtx:      ctx,
	}
}

func (e *Engine) startFlow(run *Run, idx int, flow FlowRef, typ protocol.GameCtxType) {
	bt := treeCtx(run, idx)
	var ctx *protocol.GameCtxPb
	if typ == protocol.GameCtxType_GameCtxType_DoInteractChildQuestNode {
		ctx = &protocol.GameCtxPb{
			CtxType: typ,
			CtxInfo: &protocol.GameCtxPb_DoInteractChildQuestNode{
				DoInteractChildQuestNode: &protocol.DoInteractChildQuestNodeCtxPb{BehaviorTreeCtx: bt},
			},
		}
	} else {
		ctx = &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_PlayFlowChildQuestNode,
			CtxInfo: &protocol.GameCtxPb_PlayFlowChildQuestNode{
				PlayFlowChildQuestNode: &protocol.PlayFlowChildQuestNodeCtxPb{BehaviorTreeCtx: bt},
			},
		}
	}
	n := e.newFlow(flow, ctx)
	e.flows[n.FlowIncId] = flowWait{run: run.Key, node: idx}
	common.Info("quest", fmt.Sprintf("quest %d node %d: flow %s %d state %d (inc %d)",
		run.ID(), run.Tree.Node(idx).ID, flow.FlowListName, flow.FlowID, flow.StateID, n.FlowIncId))
	e.sys.StartFlow(n)
}

func (e *Engine) noteSkipped(run *Run, def NodeDef, cond string) {
	if run.LevelPlay {
		e.SkippedSteps = append(e.SkippedSteps, Skipped{Tree: run.ID(), Node: def.ID, Cond: cond})
	}
}

type liveProgress struct{ e *Engine }

func (l liveProgress) QuestDone(id int32) bool         { return l.e.IsFinished(id) }
func (l liveProgress) NodeDone(quest, node int32) bool { return l.e.IsNodeDone(quest, node) }
func (l liveProgress) PlayDone(id int32) bool          { return l.e.WorldPlayDone(id) }
func (l liveProgress) DungeonDone(id int32) bool       { return l.e.DungeonCleared(id) }
func (l liveProgress) HasItems(want [][]int32) bool {
	for _, w := range want {
		if len(w) < 2 || l.e.host.ItemCount(w[0]) < w[1] {
			return false
		}
	}
	return true
}

func describeLongs(ids []int64) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strconv.FormatInt(id, 10))
	}
	sb.WriteByte(']')
	return sb.String()
}

func describeInts(ids []int32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strconv.FormatInt(int64(id), 10))
	}
	sb.WriteByte(']')
	return sb.String()
}
