// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/protocol"
)

// DynamicInteract classifies a picked dynamic option: not a quest's, done at once, or a flow now plays.
type DynamicInteract int

const (
	DynamicInteractUnhandled DynamicInteract = iota
	DynamicInteractDone
	DynamicInteractFlow
)

// -----------------------------------------------------------------------------
// Accepting and offering (R-10, R-61)

// Accept starts a quest now: the client learns about it, gets its tree, then the root activates.
func (e *Engine) Accept(quest int32) bool {
	run := e.startRun(quest)
	if run == nil {
		return false
	}
	common.Info("quest", fmt.Sprintf("quest %d %s accepted", quest, run.Tree.Def.Name))
	e.host.Push(&protocol.QuestStateUpdateNotify{QuestId: quest, State: protocol.QuestState_QuestState_Progress})
	run.ClientHasTree = true
	e.host.Push(&protocol.BehaviorTreeInfoNotify{TreeInfos: []*protocol.TreeInfo{e.TreeInfoOf(run)}})
	e.activate(run, run.Tree.Root)
	return true
}

// AcceptUnlocked starts every main story quest whose unlock conditions now hold (R-10) and System
// side quests (R-61), then offers Interact quests.
func (e *Engine) AcceptUnlocked() {
	live := liveProgress{e: e}
	var ready []int32
	for _, raw := range e.cat.Data.Quests {
		tree, ok := e.cat.Quests[raw.ID]
		if !ok {
			continue
		}
		def := tree.Def
		if e.IsFinished(def.ID) || e.IsRunning(def.ID) {
			continue
		}
		var unlocked bool
		if AutoAccepted(def) {
			unlocked = slices.Contains(ActivityGuideQuests, def.ID) || Unlocked(def, live)
		} else if o, offered := e.cat.Offers()[def.ID]; offered {
			unlocked = e.OffersEnabled && !o.Interact && OfferUnlocked(def, o, live)
		}
		if unlocked {
			ready = append(ready, def.ID)
		}
	}
	for _, id := range ready {
		if !e.IsFinished(id) && !e.IsRunning(id) {
			e.Accept(id)
		}
	}
	e.offerReady()
}

func (e *Engine) offerReady() {
	if !e.OffersEnabled {
		return
	}
	live := liveProgress{e: e}
	var fresh []int32
	for _, oid := range e.cat.Data.OfferOrder {
		o := e.cat.Data.Offers[oid]
		if !o.Interact || o.Option == nil || slices.Contains(e.ready, o.ID) || e.IsFinished(o.ID) || e.IsRunning(o.ID) {
			continue
		}
		tree, ok := e.cat.Quests[o.ID]
		if !ok || AutoAccepted(tree.Def) || !OfferUnlocked(tree.Def, o, live) {
			continue
		}
		fresh = append(fresh, o.ID)
	}
	if len(fresh) == 0 {
		return
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i] < fresh[j] })
	common.Info("quest", fmt.Sprintf("offering %d quests: %s", len(fresh), describeInts(fresh)))
	e.host.Push(&protocol.QuestReadyListNotify{QuestId: fresh})
	w := e.host.World()
	for _, id := range fresh {
		e.ready = append(e.ready, id)
		opt := *e.cat.Data.Offers[id].Option
		// The NPC may be one a quest put to sleep; it has to be there to give the quest.
		w.Wake(opt.EntityID)
		if net, ok := e.host.NetIDOf(opt.EntityID); ok {
			e.pushAcceptOption(id, opt, net)
		}
	}
	w.Save()
	e.host.RefreshSpawns()
}

func (e *Engine) acceptAfterFlow(quest int32) {
	if at := slices.Index(e.ready, quest); at >= 0 {
		e.ready = append(e.ready[:at], e.ready[at+1:]...)
	}
	o, ok := e.cat.Data.Offers[quest]
	if !ok || o.Option == nil {
		return
	}
	opt := *o.Option
	if net, spawned := e.host.NetIDOf(opt.EntityID); spawned {
		e.host.Push(&protocol.EntityInteractingNotify{EntityId: net, IsInteracting: false})
		e.host.Push(&protocol.EntityRemoveDynamicInteractNotify{EntityId: net, OptionGuid: opt.Guid})
	}
	if e.IsFinished(quest) || e.IsRunning(quest) {
		return
	}
	e.Accept(quest)
	e.Save()
}

// -----------------------------------------------------------------------------
// Scene join (enter-world.md §4)

// SyncOnJoin sends finished quests, quests in progress and their trees. Called after every scene join.
func (e *Engine) SyncOnJoin() {
	// A new scene: the last position was on the old map (R-82a fixes judge positions).
	e.playerPos = nil
	// R-99: the vehicle stayed in the old scene; after a login the step replays it.
	e.sys.ResetVehicles()
	e.rideReplayPending = e.RestorePending
	e.rideReplayAtMs = nil
	// R-67: NPCs and monsters left awake by quests finished before.
	e.hideFinishedActors()
	e.sys.OnSceneJoin()
	// R-69: clearings of quests and plays no longer running.
	e.sys.RetainClearings(func(kind byte, id int32) bool {
		if kind == 'q' {
			return e.IsRunning(id)
		}
		return e.IsLevelPlayRunning(id)
	})
	e.host.Push(&protocol.QuestFinishListNotify{QuestId: e.finished.Keys()})
	list := &protocol.QuestListNotify{}
	infos := &protocol.BehaviorTreeInfoNotify{}
	snapshot := slices.Clone(e.runList)
	for _, run := range snapshot {
		if !run.LevelPlay {
			list.Quests = append(list.Quests, &protocol.QuestInfo{
				QuestId: run.ID(),
				Status:  protocol.QuestState_QuestState_Progress,
			})
		}
		e.enterLevelPlay(run) // R-92: before its tree
		if run.TakeTreeSend() {
			infos.TreeInfos = append(infos.TreeInfos, e.TreeInfoOf(run))
		}
	}
	e.host.Push(list)
	if len(infos.TreeInfos) > 0 {
		e.host.Push(infos)
	}
	common.Info("quest", fmt.Sprintf("synced %d finished and %d running quests, %d trees sent",
		e.finished.Len(), len(list.Quests), len(infos.TreeInfos)))
	// A fresh quest starts now; a restored PlayFlow replays its flow.
	for _, run := range snapshot {
		if run.Done {
			continue
		}
		if run.Nodes[run.Tree.Root].Status == protocol.NodeStatus_NodeStatus_NotActive {
			e.activate(run, run.Tree.Root)
			continue
		}
		for i, st := range run.Nodes {
			if st.Status != protocol.NodeStatus_NodeStatus_Activated ||
				st.Cq != protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Progress {
				continue
			}
			def := run.Tree.Node(i)
			switch def.Cond {
			case "PlayFlow":
				if def.Flow != nil {
					e.startFlow(run, i, *def.Flow, protocol.GameCtxType_GameCtxType_PlayFlowChildQuestNode)
				}
			case "DoInteract":
				for _, o := range def.Options {
					e.host.World().Wake(o.EntityID)
					e.host.RefreshSpawns()
					if net, ok := e.host.NetIDOf(o.EntityID); ok {
						e.pushOption(run, i, o, net)
					}
				}
			case "Timer":
				exists := false
				for _, t := range e.timers {
					if t.run == run.Key && t.node == i {
						exists = true
						break
					}
				}
				if !exists {
					e.timers = append(e.timers, timerWait{run: run.Key, node: i, dueMs: e.NowMs() + 1000})
				}
			}
		}
	}
	e.Save()
	// EnterDungeon / LeaveDungeon steps (R-4e) are met by arriving in this scene.
	e.RecheckConditions()
	// R-10: progress saved before the full unlock check may already unlock quests.
	e.AcceptUnlocked()
	e.Save()
}

// OnEntitySpawned gives a newly spawned world entity options of active steps and offered quests.
func (e *Engine) OnEntitySpawned(configID, netID int64) {
	e.sys.OnEntitySpawned(configID, netID)
	for _, run := range e.runList {
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated || !HasOptions(def) {
				continue
			}
			for _, o := range def.Options {
				if o.EntityID == configID {
					e.pushOption(run, i, o, netID)
				}
			}
		}
	}
	// R-61: the NPC of an offered quest.
	for _, quest := range e.ready {
		if o, ok := e.cat.Data.Offers[quest]; ok && o.Option != nil && o.Option.EntityID == configID {
			e.pushAcceptOption(quest, *o.Option, netID)
		}
	}
	e.sys.AnnounceOptions(configID, netID)
}

// -----------------------------------------------------------------------------
// Client events

// SubmitNode handles SubmitNodeRequest: the client finished a child quest it watches itself.
func (e *Engine) SubmitNode(treeIncID int64, nodeID int32) protocol.ErrorCode {
	run := e.runOf(treeIncID)
	if run == nil {
		return protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive
	}
	idx, ok := run.Tree.IndexOf(nodeID)
	if !ok || run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
		return protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive
	}
	common.Info("quest", fmt.Sprintf("quest %d node %d: submitted", treeIncID, nodeID))
	e.complete(run, idx, true)
	e.Save()
	return protocol.ErrorCode_ErrorCode_Success
}

// OnFlowEnd handles FlowEndRequest: a PlayFlow node's flow, quest option dialogue, or accept dialogue finished.
func (e *Engine) OnFlowEnd(flowIncID int64) {
	if quest, ok := e.acceptFlows[flowIncID]; ok {
		delete(e.acceptFlows, flowIncID)
		common.Info("quest", fmt.Sprintf("quest %d: accept dialogue %d ended", quest, flowIncID))
		e.acceptAfterFlow(quest)
		return
	}
	// R-74: a quest option's dialogue ended: entity is free again (the option stays).
	if entity, ok := e.optionFlows[flowIncID]; ok {
		delete(e.optionFlows, flowIncID)
		if net, spawned := e.host.NetIDOf(entity); spawned {
			e.host.Push(&protocol.EntityInteractingNotify{EntityId: net, IsInteracting: false})
		}
		return
	}
	wait, ok := e.flows[flowIncID]
	if !ok {
		return
	}
	delete(e.flows, flowIncID)
	run := e.runOf(wait.run)
	if run == nil {
		return
	}
	def := run.Tree.Node(wait.node)
	common.Info("quest", fmt.Sprintf("quest %d node %d: flow %d ended", wait.run, def.ID, flowIncID))
	if def.Cond == "DoInteract" {
		for _, o := range def.Options {
			if net, spawned := e.host.NetIDOf(o.EntityID); spawned {
				e.host.Push(&protocol.EntityInteractingNotify{EntityId: net, IsInteracting: false})
			}
		}
	}
	e.complete(run, wait.node, true)
	e.Save()
}

func playFlowOf(actions []QuestAction) *FlowRef {
	for _, a := range actions {
		if a.Name == "PlayFlow" && a.Params != nil && a.Params.Flow != nil && !a.Params.Flow.IsEmpty() {
			return a.Params.Flow
		}
	}
	return nil
}

// OnDynamicInteract handles EntityDynamicInteractRequest: the player picked an option a quest added.
func (e *Engine) OnDynamicInteract(entityConfigID int64, guid string) DynamicInteract {
	for _, run := range e.runList {
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated || def.Cond != "DoInteract" {
				continue
			}
			for _, option := range def.Options {
				if option.EntityID != entityConfigID || (guid != "" && option.Guid != guid) {
					continue
				}
				common.Info("quest", fmt.Sprintf("quest %d node %d: interact with %d", run.ID(), def.ID, entityConfigID))
				result := DynamicInteractFlow
				if option.Flow != nil {
					e.startFlow(run, i, *option.Flow, protocol.GameCtxType_GameCtxType_DoInteractChildQuestNode)
				} else if af := playFlowOf(option.Actions); af != nil {
					_ = e.host.RunActions(option.Actions)
					e.startFlow(run, i, *af, protocol.GameCtxType_GameCtxType_DoInteractChildQuestNode)
				} else {
					_ = e.host.RunActions(option.Actions)
					e.complete(run, i, true)
					result = DynamicInteractDone
				}
				e.Save()
				return result
			}
		}
	}
	// R-76g: a hand-in step's option opens the hand-in view.
	for _, run := range e.runList {
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated || def.Cond != "HandInItems" {
				continue
			}
			for _, o := range def.Options {
				if o.EntityID == entityConfigID && (guid == "" || o.Guid == guid) {
					common.Info("quest", fmt.Sprintf("quest %d node %d: hand-in opened at %d", run.ID(), def.ID, entityConfigID))
					return DynamicInteractDone
				}
			}
		}
	}
	// R-74: an option a quest step added (AddFlowInteractOption).
	if picked, handled := e.sys.PickFlowOption(entityConfigID, guid); handled {
		if picked == nil {
			return DynamicInteractDone
		}
		e.nextFlowInc++
		picked.FlowIncId = e.nextFlowInc
		e.optionFlows[picked.FlowIncId] = entityConfigID
		common.Info("quest", fmt.Sprintf("quest option on %d: flow %s %d state %d (inc %d)",
			entityConfigID, picked.FlowListName, picked.FlowId, picked.StateId, picked.FlowIncId))
		e.sys.StartFlow(picked)
		return DynamicInteractFlow
	}
	// R-61: the accept option of an offered quest.
	for _, quest := range e.ready {
		o, ok := e.cat.Data.Offers[quest]
		if !ok || o.Option == nil {
			continue
		}
		opt := *o.Option
		if opt.EntityID != entityConfigID || (guid != "" && opt.Guid != guid) {
			continue
		}
		n := e.newFlow(opt.Flow, acceptCtx(quest))
		e.acceptFlows[n.FlowIncId] = quest
		common.Info("quest", fmt.Sprintf("quest %d: accept dialogue %s %d state %d (inc %d)",
			quest, opt.Flow.FlowListName, opt.Flow.FlowID, opt.Flow.StateID, n.FlowIncId))
		e.sys.StartFlow(n)
		return DynamicInteractFlow
	}
	return DynamicInteractUnhandled
}

// HandIn handles HandInItemRequest (R-76g): takes the handed-in items and completes the step.
func (e *Engine) HandIn(treeIncID int64, nodeID int32, items []*protocol.PbHandInItem) protocol.ErrorCode {
	run := e.runOf(treeIncID)
	if run == nil {
		return protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive
	}
	idx, ok := run.Tree.IndexOf(nodeID)
	if !ok {
		return protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive
	}
	def := run.Tree.Node(idx)
	if run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated || def.Cond != "HandInItems" {
		return protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive
	}
	for _, group := range items {
		if group == nil {
			continue
		}
		for _, it := range group.HandItemInfo {
			if it != nil && it.ItemId > 0 && it.Num > 0 {
				e.host.TakeItem(it.ItemId, it.Num)
			}
		}
	}
	common.Info("quest", fmt.Sprintf("quest %d node %d: items handed in", run.ID(), def.ID))
	e.HandIns++
	e.complete(run, idx, true)
	e.Save()
	return protocol.ErrorCode_ErrorCode_Success
}

// OnUseSkill handles server-judged UseSkill steps (R-14).
func (e *Engine) OnUseSkill(skillID int64, pos [3]float32) {
	changed := false
	for _, run := range slices.Clone(e.runList) {
		for i := range run.Nodes {
			if run.Done {
				break
			}
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated ||
				def.Cond != "UseSkill" || def.Skill == nil || def.Skill.ID != skillID {
				continue
			}
			if !InSphere(*def.Skill, pos) {
				common.Info("quest", fmt.Sprintf("quest %d node %d: skill %d used outside the area", run.ID(), def.ID, skillID))
				continue
			}
			common.Info("quest", fmt.Sprintf("quest %d node %d: skill %d used", run.ID(), def.ID, skillID))
			e.complete(run, i, true)
			changed = true
		}
	}
	if changed {
		e.Save()
	}
}

// OnGroupCleared completes MonsterCreator and R-128 Kill-over-variable steps waiting on varName.
func (e *Engine) OnGroupCleared(runKey int64, varName string) {
	run := e.runOf(runKey)
	if run == nil {
		return
	}
	for i := range run.Nodes {
		def := run.Tree.Node(i)
		if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated {
			continue
		}
		kill := def.Cond == "Kill"
		if def.Cond != "MonsterCreator" && !(kill && def.Var != "") {
			continue
		}
		if def.Var != varName {
			continue
		}
		common.Info("quest", fmt.Sprintf("quest %d node %d: %q cleared", run.ID(), def.ID, varName))
		e.complete(run, i, true)
		if run.Done {
			return
		}
	}
}

func (e *Engine) noteDungeonKill(configID int64) {
	inst := e.host.InstanceID()
	if !e.sys.IsDungeonEntered(inst) {
		return
	}
	if e.dungeonKillsInst != inst {
		clear(e.dungeonKills)
		e.dungeonKillsInst = inst
	}
	e.dungeonKills[configID] = struct{}{}
}

func (e *Engine) killedBefore(targets []int64) int {
	inst := e.host.InstanceID()
	if !e.sys.IsDungeonEntered(inst) || e.dungeonKillsInst != inst {
		return 0
	}
	n := 0
	for _, t := range targets {
		if _, ok := e.dungeonKills[t]; ok {
			n++
		}
	}
	return n
}

// OnKill counts a level monster death for spawner waves and Kill child quests.
func (e *Engine) OnKill(configID int64) {
	defer func() {
		e.RecheckConditions()
		if e.battleWaiting {
			e.checkBattles()
		}
	}()

	e.noteDungeonKill(configID)
	// A spawner wave's monster (R-4f): back to sleep so it does not respawn.
	if e.sys.SpawnerMentions(configID) {
		e.host.World().Sleep(configID)
		e.host.World().Save()
	}
	var wake []int64
	cleared, hasCleared := e.sys.SpawnerKilled(configID, e.NowMs(), &wake)
	if len(wake) > 0 {
		common.Info("quest", fmt.Sprintf("spawner: kill %d wakes %s", configID, describeLongs(wake)))
		e.wake(wake)
	}
	if hasCleared {
		e.completeSpawnerStep(cleared.Run, cleared.Node)
	}
	for _, run := range e.runList {
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated ||
				def.Cond != "Kill" || !slices.Contains(def.Targets, configID) {
				continue
			}
			run.Nodes[i].Count++
			common.Info("quest", fmt.Sprintf("quest %d node %d: killed %d/%d", run.ID(), def.ID, run.Nodes[i].Count, len(def.Targets)))
			if int(run.Nodes[i].Count) >= len(def.Targets) {
				e.complete(run, i, true)
			}
			e.Save()
			return
		}
	}
}

func (e *Engine) completeSpawnerStep(runKey int64, node int) {
	run := e.runOf(runKey)
	if run == nil || run.Done || node >= len(run.Nodes) || run.Nodes[node].Status != protocol.NodeStatus_NodeStatus_Activated {
		return
	}
	common.Info("quest", fmt.Sprintf("quest %d node %d: spawner waves cleared", run.ID(), run.Tree.Node(node).ID))
	e.complete(run, node, true)
}

// OnEntityState rechecks CheckEntityState steps when configID changes state.
func (e *Engine) OnEntityState(configID int64) {
	defer e.RecheckConditions()

	for _, run := range e.runList {
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated || def.Cond != "CheckEntityState" {
				continue
			}
			mentions := false
			for _, s := range def.States {
				if s.EntityID == configID {
					mentions = true
					break
				}
			}
			if mentions && e.entityStatesMet(def) {
				e.complete(run, i, true)
				e.Save()
				return
			}
		}
	}
}

// OnPlayerMoved runs restore/periodic fixes, battle/condition/range checks, inventory-triggered
// offers, and proximity world plays (R-62, R-82a, R-76h).
func (e *Engine) OnPlayerMoved(pos [3]float32) {
	p := pos
	e.playerPos = &p
	if e.RestorePending {
		e.RestorePending = false
		e.runRunningFixes(fixRestore)
	}
	now := e.NowMs()
	if now-e.fixScanMs >= 1000 {
		e.fixScanMs = now
		e.runRunningFixes(fixPeriodic)
	}
	if e.battleWaiting {
		e.checkBattles()
	}
	// R-83: condition steps wait on things not every change rechecks: look again once a second.
	if e.questConditionsWaiting && now-e.condScanMs >= 1000 {
		e.condScanMs = now
		e.RecheckConditions()
	}
	// R-76h: steps waiting for the player to enter or leave a range: look again every 3 m.
	if e.rangeWaiting {
		var moved float32 = 1e12
		if e.rangeCheckedAt != nil {
			dx := pos[0] - e.rangeCheckedAt[0]
			dy := pos[1] - e.rangeCheckedAt[1]
			moved = dx*dx + dy*dy
		}
		if moved > 300*300 {
			cp := pos
			e.rangeCheckedAt = &cp
			e.RecheckConditions()
		}
	}
	if !e.OffersEnabled {
		return
	}
	if e.InventoryVersion != nil {
		if version := e.InventoryVersion(); version != e.inventoryVersion {
			e.inventoryVersion = version
			e.AcceptUnlocked()
			e.Save()
		}
	}
	mapID := e.host.MapID()
	live := liveProgress{e: e}
	for _, wp := range e.cat.WorldPlays() {
		if wp.Sub || wp.LevelID != mapID || wp.Radius <= 0 || e.WorldPlayDone(wp.ID) || e.IsLevelPlayRunning(wp.ID) {
			continue
		}
		dx := pos[0] - wp.Pos[0]
		dy := pos[1] - wp.Pos[1]
		dz := pos[2] - wp.Pos[2]
		if dx*dx+dy*dy+dz*dz > wp.Radius*wp.Radius || !WorldPlayOpen(wp, live) {
			continue
		}
		common.Info("quest", fmt.Sprintf("world play %d %s: player within %g m", wp.ID, wp.Name, wp.Radius/100))
		e.StartLevelPlay(wp.ID)
	}
}

// Tick completes due Timer steps, countdowns, spawner waves, and pending ride replays.
func (e *Engine) Tick() {
	now := e.NowMs()
	changed := false
	if e.rideReplayPending {
		e.replayRide()
	}
	// Entities came and went or a timer ran: condition steps may be met.
	e.RecheckConditions()
	var wake []int64
	var cleared []SpawnerKey
	e.sys.SpawnerTick(now, &wake, &cleared)
	if len(wake) > 0 {
		common.Info("quest", fmt.Sprintf("spawner: timed waves wake %s", describeLongs(wake)))
		e.wake(wake)
	}
	for _, k := range cleared {
		e.completeSpawnerStep(k.Run, k.Node)
	}
	if len(cleared) > 0 {
		changed = true
	}
	for i := 0; i < len(e.timers); {
		t := e.timers[i]
		if t.dueMs > now {
			i++
			continue
		}
		last := len(e.timers) - 1
		e.timers[i] = e.timers[last]
		e.timers = e.timers[:last]
		run := e.runOf(t.run)
		if run == nil {
			continue
		}
		common.Info("quest", fmt.Sprintf("quest %d node %d: timer done", t.run, run.Tree.Node(t.node).ID))
		if run.Tree.Node(t.node).Is("QuestFailed") {
			// R-37f: a failure countdown, still active: the level play fails.
			if run.Nodes[t.node].Status == protocol.NodeStatus_NodeStatus_Activated && !run.Done {
				e.failLevelPlay(run, t.node)
			}
		} else {
			e.complete(run, t.node, true)
		}
		changed = true
	}
	if changed {
		e.Save()
	}
}

// SkipActive force-completes every active child quest (except AlwaysFalse failure branches).
func (e *Engine) SkipActive() int {
	skipped := 0
	for _, run := range slices.Clone(e.runList) {
		for i := range run.Nodes {
			if run.Done {
				break
			}
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated || !def.Is("ChildQuest") {
				continue
			}
			if p, ok := run.Tree.Parent(i); ok && run.Tree.Node(p).Is("AlwaysFalse") {
				continue
			}
			common.Info("quest", fmt.Sprintf("quest %d node %d: skipped by command", run.ID(), def.ID))
			e.complete(run, i, true)
			skipped++
		}
	}
	e.Save()
	return skipped
}

// DescribeProgress formats running quests and their active child quests for the `quest` command.
func (e *Engine) DescribeProgress() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d finished, %d running", e.finished.Len(), len(e.runList))
	for _, run := range e.runList {
		sb.WriteByte('\n')
		sb.WriteString(strconv.FormatInt(int64(run.ID()), 10))
		sb.WriteByte(' ')
		sb.WriteString(run.Tree.Def.Name)
		sb.WriteByte(':')
		for i := range run.Nodes {
			if run.Nodes[i].Status == protocol.NodeStatus_NodeStatus_Activated && run.Tree.Node(i).Is("ChildQuest") {
				n := run.Tree.Node(i)
				sb.WriteString(" [")
				sb.WriteString(strconv.FormatInt(int64(n.ID), 10))
				sb.WriteByte(' ')
				sb.WriteString(n.Cond)
				sb.WriteByte(']')
			}
		}
	}
	return sb.String()
}

// -----------------------------------------------------------------------------
// Level plays (R-4b)

// StartLevelPlay starts level play lp and sends its tree; returns false if there is no such tree.
func (e *Engine) StartLevelPlay(lp int32, vars ...LevelPlayVar) bool {
	tree, ok := e.cat.LevelPlays[lp]
	if !ok {
		return false
	}
	key := LevelPlayRun(lp)
	if _, exists := e.runs[key]; exists {
		return true
	}
	// A Self variable it starts with (领主挑战: Level = chosen difficulty).
	for _, v := range vars {
		e.Vars.Write(key, VarRef{Src: "Self", T: "Int", Name: v.Name}, BtNum(v.Value))
	}
	lpCopy := lp
	e.LastStartedLevelPlay = &lpCopy
	run := newRun(tree, key, true)
	e.addRun(run)
	common.Info("quest", fmt.Sprintf("level play %d %s started", lp, tree.Def.Name))
	e.enterLevelPlay(run)
	run.ClientHasTree = true
	e.host.Push(&protocol.BehaviorTreeInfoNotify{TreeInfos: []*protocol.TreeInfo{e.TreeInfoOf(run)}})
	e.activate(run, tree.Root)
	return true
}

func (e *Engine) failLevelPlay(run *Run, idx int) {
	common.Info("quest", fmt.Sprintf("level play %d %s failed (node %d)", run.ID(), run.Tree.Def.Name, run.Tree.Node(idx).ID))
	e.runNodeActions(run, idx, run.Tree.Node(idx).Finish, actionFinish)
	e.setStatus(run, idx, protocol.NodeStatus_NodeStatus_CompletedSuccess)
	e.StopLevelPlay(run.ID())
	e.sys.OnLevelPlayFailed(run.ID())
}

// StopLevelPlay drops one running level play and forgets it finished.
func (e *Engine) StopLevelPlay(lp int32) {
	e.lpFinished.Delete(lp)
	key := LevelPlayRun(lp)
	run := e.runOf(key)
	if run == nil {
		return
	}
	run.Done = true
	e.sys.DropRunData(key)
	e.Vars.DropRun(key)
	e.removeRun(key)
	e.host.Push(&protocol.BehaviorTreeDeleteNotify{TreeIncIds: []int64{key}})
	e.leaveLevelPlay(run)
}

// StopLevelPlays drops every running level play (leaving the dungeon).
func (e *Engine) StopLevelPlays() {
	// R-44: a new run starts with no kills.
	clear(e.dungeonKills)
	e.dungeonKillsInst = 0
	var ids []int64
	for i := 0; i < len(e.runList); {
		run := e.runList[i]
		if !run.LevelPlay {
			i++
			continue
		}
		run.Done = true
		ids = append(ids, run.Key)
		e.sys.DropRunData(run.Key)
		e.Vars.DropRun(run.Key)
		e.sys.ReleaseRun(run.Key, true, run.ID())
		e.leaveLevelPlay(run)
		e.removeRun(run.Key)
	}
	if len(ids) > 0 {
		e.host.Push(&protocol.BehaviorTreeDeleteNotify{TreeIncIds: ids})
	}
}

// OnDungeonFinished counts a dungeon completion for PreDungeon unlocks and FinishDungeon steps.
func (e *Engine) OnDungeonFinished(instanceID int32, success bool) {
	if !success {
		return
	}
	// R-62: PreDungeon unlock conditions.
	if !e.dungeonsDone.Has(instanceID) {
		e.dungeonsDone.Set(instanceID, struct{}{})
		e.Save()
		e.AcceptUnlocked()
	}
	counted := false
	for _, run := range e.runList {
		if run.LevelPlay {
			continue
		}
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated ||
				def.Cond != "FinishDungeon" || !DungeonMatches(def, instanceID, e.subTypeOf) {
				continue
			}
			run.Nodes[i].Count++
			counted = true
			common.Info("quest", fmt.Sprintf("quest %d node %d: dungeon %d finished %d/%d",
				run.ID(), def.ID, instanceID, run.Nodes[i].Count, max(def.Need, 1)))
		}
	}
	if !counted {
		return
	}
	e.Save()
	e.RecheckConditions()
}

// -----------------------------------------------------------------------------
// R-67: put finished quests' sleeping NPCs and monsters back to sleep.

func isActorType(t string) bool {
	switch t {
	case "Monster", "NpcMonster", "Npc", "Npc2", "SimpleNPc", "PasserbyNpc", "ElementalNpc":
		return true
	default:
		return false
	}
}

func (e *Engine) buildActors() {
	if e.actors != nil {
		return
	}
	e.actors = map[int64]struct{}{}
	e.ownedActors = map[int64]struct{}{}
	t := e.host.Tables()
	if t == nil {
		return
	}
	kinds := map[string]struct{}{}
	for _, b := range t.BlueprintConfig {
		if isActorType(b.EntityType) {
			kinds[b.BlueprintType] = struct{}{}
		}
	}
	for _, row := range t.LevelEntityConfig {
		if !row.InSleep {
			continue
		}
		if _, ok := kinds[row.BlueprintType]; !ok {
			continue
		}
		eid := int64(row.EntityId)
		e.actors[eid] = struct{}{}
		if e.cat.IsQuestOwned(row.MapId, eid) {
			e.ownedActors[eid] = struct{}{}
		}
	}
	common.Info("quest", fmt.Sprintf("%d sleeping NPC and monster rows quests may wake (%d quest-owned)",
		len(e.actors), len(e.ownedActors)))
}

func (e *Engine) actorsOf(n NodeDef) []int64 {
	var out []int64
	if HasOptions(n) {
		for _, o := range n.Options {
			out = append(out, o.EntityID)
		}
	} else if n.Cond == "Kill" {
		out = append(out, n.Targets...)
	}
	scan := func(actions []QuestAction) {
		for _, a := range actions {
			if a.Name == "AwakeEntity" && a.Params != nil && a.Params.HasEntityIDs {
				for _, id := range a.Params.EntityIDs {
					if _, ok := e.ownedActors[id]; ok {
						out = append(out, id)
					}
				}
			}
		}
	}
	scan(n.Enter)
	scan(n.Finish)
	scan(n.Actions)
	return out
}

func (e *Engine) hideActorsOf(def QuestDef) int {
	e.buildActors()
	hidden := 0
	var ids []int64
	for _, n := range def.Nodes {
		ids = append(ids, e.actorsOf(n)...)
	}
	w := e.host.World()
	for _, id := range ids {
		if _, ok := e.actors[id]; !ok || !w.IsAwake(id) || e.UsedByRunning(id) {
			continue
		}
		e.host.HideEntity(id)
		hidden++
	}
	return hidden
}

func (e *Engine) hideFinishedActors() {
	if e.actorsCleaned {
		return
	}
	e.actorsCleaned = true
	hidden := 0
	for _, id := range e.finished.Keys() {
		if tree, ok := e.cat.Quests[id]; ok {
			hidden += e.hideActorsOf(tree.Def)
		}
	}
	if hidden > 0 {
		e.host.World().Save()
		common.Info("quest", fmt.Sprintf("%d NPCs and monsters of finished quests back to sleep", hidden))
	}
}
