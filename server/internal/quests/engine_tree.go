// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"slices"

	"tidelab/internal/common"
	"tidelab/protocol"
)

type actionKind int

const (
	actionEnter actionKind = iota
	actionFinish
	actionGroup
	actionSuccess
	actionFix
)

func actionCtx(run *Run, idx int, kind actionKind) *protocol.GameCtxPb {
	bt := treeCtx(run, idx)
	switch kind {
	case actionEnter:
		return &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_ChildQuestNodeEnterAction,
			CtxInfo: &protocol.GameCtxPb_ChildQuestNodeEnterAction{
				ChildQuestNodeEnterAction: &protocol.ChildQuestNodeEnterActionCtxPb{BehaviorTreeCtx: bt},
			},
		}
	case actionFinish:
		return &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_ChildQuestNodeFinishAction,
			CtxInfo: &protocol.GameCtxPb_ChildQuestNodeFinishAction{
				ChildQuestNodeFinishAction: &protocol.ChildQuestNodeFinishActionCtxPb{BehaviorTreeCtx: bt},
			},
		}
	case actionGroup:
		return &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_ActionGroupNodeAction,
			CtxInfo: &protocol.GameCtxPb_ActionGroupNodeAction{
				ActionGroupNodeAction: &protocol.ActionGroupNodeActionCtxPb{BehaviorTreeCtx: bt},
			},
		}
	case actionSuccess:
		return &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_SuccessNodeAction,
			CtxInfo: &protocol.GameCtxPb_SuccessNodeAction{
				SuccessNodeAction: &protocol.SuccessNodeActionCtxPb{BehaviorTreeCtx: bt},
			},
		}
	default:
		return &protocol.GameCtxPb{
			CtxType: protocol.GameCtxType_GameCtxType_ChildQuestNodeFixAction,
			CtxInfo: &protocol.GameCtxPb_BehaviorTree{BehaviorTree: bt},
		}
	}
}

// runNodeActions runs the server half of a node's actions, then the client's presentation half.
func (e *Engine) runNodeActions(run *Run, idx int, actions []QuestAction, kind actionKind) {
	if len(actions) == 0 {
		return
	}
	// SetVar / CalculateVar (R-4g level plays, R-83 quests too: their variables are saved).
	varsChanged := false
	for _, a := range actions {
		if a.VarOp != nil && e.Vars.Apply(run.Key, *a.VarOp) {
			varsChanged = true
		}
	}
	defer func() {
		if varsChanged {
			e.RecheckConditions()
			if !run.LevelPlay {
				e.Save()
			}
		}
	}()

	// Prefabs belong to the run (variable names are per level play).
	for _, a := range actions {
		if a.Params == nil {
			continue
		}
		if a.Name == "CreatePrefab" && a.Params.PrefabID != nil {
			varName := ""
			if a.Params.Var != nil {
				varName = *a.Params.Var
			}
			var posID int64
			if a.Params.PosEntityID != nil {
				posID = *a.Params.PosEntityID
			}
			e.sys.CreatePrefab(run.Key, varName, *a.Params.PrefabID, posID, run.LevelPlay)
			e.host.RefreshSpawns()
		} else if a.Name == "DestroyPrefab" {
			varName := ""
			if a.Params.Var != nil {
				varName = *a.Params.Var
			}
			common.Info("quest", fmt.Sprintf("quest %d: DestroyPrefab %s (left to the run's cleanup)", run.ID(), varName))
		}
	}

	if err := e.host.RunActions(actions); err != nil {
		common.Warn("quest", fmt.Sprintf("quest %d node %d: actions failed: %v", run.ID(), run.Tree.Node(idx).ID, err))
	}
	ctx := actionCtx(run, idx, kind)
	e.sys.StepActions(run.Ref(), actions, ctx, kind == actionFix)
	if kind == actionFix {
		for _, a := range actions {
			if IsClientAction(a.Name) {
				e.FixStats.ClientSkipped++
			}
		}
		return
	}
	e.runOrdered(run, actions, ctx)
	// The client refuses PlayFlow actions of server flows; the server starts them (R-89).
	for _, a := range actions {
		if a.Name != "PlayFlow" || a.Params == nil || a.Params.Flow == nil || a.Params.Flow.IsEmpty() {
			continue
		}
		flow := *a.Params.Flow
		n := e.newFlow(flow, ctx)
		common.Info("quest", fmt.Sprintf("quest %d node %d: action flow %s %d state %d (inc %d)",
			run.ID(), run.Tree.Node(idx).ID, flow.FlowListName, flow.FlowID, flow.StateID, n.FlowIncId))
		e.sys.StartFlow(n)
	}
}

// runOrdered interleaves client action ranges and server player-movement actions in list order (R-99).
func (e *Engine) runOrdered(run *Run, actions []QuestAction, ctx *protocol.GameCtxPb) {
	for i := 0; i < len(actions); {
		if !IsClientAction(actions[i].Name) {
			e.movePlayerAction(run, actions[i])
			i++
			continue
		}
		end := ClientRunEnd(actions, i)
		e.host.PushClientRange(actions, i, end, ctx)
		i = end + 1
	}
}

// ClientRunEnd returns the last index of the contiguous run of client actions starting at start.
func ClientRunEnd(actions []QuestAction, start int) int {
	end := start
	for end+1 < len(actions) && IsClientAction(actions[end+1].Name) {
		end++
	}
	return end
}

// replayRide replays a running step's VehicleEnter EnterActions after a login once the role moved
// and the vehicle is spawned, 2 s later (R-99).
func (e *Engine) replayRide() {
	for _, run := range e.runList {
		if run.Done {
			continue
		}
		for i, st := range run.Nodes {
			if st.Status != protocol.NodeStatus_NodeStatus_Activated ||
				st.Cq != protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Progress {
				continue
			}
			def := run.Tree.Node(i)
			ride, ok := VehicleEnterIn(def.Enter)
			if !ok {
				continue
			}
			if e.playerPos == nil {
				return
			}
			if _, spawned := e.host.NetIDOf(ride.Vehicle); !spawned {
				return
			}
			now := e.NowMs()
			if e.rideReplayAtMs == nil {
				at := now + RideReplayDelayMs
				e.rideReplayAtMs = &at
				return
			}
			if now < *e.rideReplayAtMs {
				return
			}
			e.rideReplayPending = false
			common.Info("quest", fmt.Sprintf("quest %d node %d: after the login the player boards %d again",
				run.ID(), def.ID, ride.Vehicle))
			e.runOrdered(run, def.Enter, actionCtx(run, i, actionEnter))
			return
		}
	}
	e.rideReplayPending = false
}

func (e *Engine) movePlayerAction(run *Run, a QuestAction) {
	if e.sys.MovePlayerAction(run.Ref(), a) || a.Name != "SetPlayerPos" {
		return
	}
	pos, yaw, ok := e.setPlayerPosTarget(a)
	if !ok {
		common.Warn("quest", fmt.Sprintf("quest %d: SetPlayerPos without a usable target", run.ID()))
		return
	}
	// Only once the client has moved in this scene: a step that starts as the scene is joined
	// would teleport a client still loading.
	if e.playerPos == nil {
		e.TeleportsHeld++
		common.Info("quest", fmt.Sprintf("quest %d: SetPlayerPos not sent (the client has not moved in this scene yet)", run.ID()))
		return
	}
	e.sys.LeaveVehicle()
	e.teleportHere(pos, yaw)
	e.Teleports++
	common.Info("quest", fmt.Sprintf("quest %d: SetPlayerPos -> %.0f,%.0f,%.0f", run.ID(), pos[0], pos[1], pos[2]))
}

// setPlayerPosTarget reads SetPlayerPos {TelePortConfig}: Type 0 TargetPos, 1 first EntityIds, 2 GravityAndPosEntityId.
func (e *Engine) setPlayerPosTarget(a QuestAction) ([3]float32, *float64, bool) {
	kind, ok := ActionInt(a, "TelePortConfig", "Type")
	if !ok {
		kind = 0
	}
	if kind == 0 {
		x, okX := ActionFloat(a, "TelePortConfig", "TargetPos", "X")
		y, okY := ActionFloat(a, "TelePortConfig", "TargetPos", "Y")
		if !okX || !okY {
			return [3]float32{}, nil, false
		}
		z, _ := ActionFloat(a, "TelePortConfig", "TargetPos", "Z")
		var yaw *float64
		if v, ok := ActionFloat(a, "TelePortConfig", "TargetPos", "A"); ok {
			yaw = &v
		}
		return [3]float32{float32(x), float32(y), float32(z)}, yaw, true
	}
	var id int64
	hasID := false
	switch kind {
	case 1:
		if ids := ActionInts(a, "TelePortConfig", "EntityIds"); len(ids) > 0 {
			id = ids[0]
			hasID = true
		}
	case 2:
		id, hasID = ActionInt(a, "TelePortConfig", "GravityAndPosEntityId")
	}
	if !hasID {
		return [3]float32{}, nil, false
	}
	pos, ok := e.host.EntityPos(id)
	if !ok {
		return [3]float32{}, nil, false
	}
	return pos, nil, true
}

func (e *Engine) teleportHere(pos [3]float32, yaw *float64) {
	var rot *protocol.Vector
	if yaw != nil {
		// The client reads Rotator(Pitch = Y, Yaw = Z, Roll = X).
		rot = &protocol.Vector{X: 0, Y: 0, Z: float32(*yaw)}
	}
	e.host.Push(&protocol.TeleportNotify{
		MapId:    e.host.InstanceID(),
		Position: &protocol.Vector{X: pos[0], Y: pos[1], Z: pos[2]},
		Rotation: rot,
	})
	p := pos
	e.playerPos = &p
}

// -----------------------------------------------------------------------------
// Tree execution

func (e *Engine) activate(run *Run, idx int) {
	def := run.Tree.Node(idx)
	kids := run.Tree.Children[idx]
	if run.Nodes[idx].Status == protocol.NodeStatus_NodeStatus_Activated {
		return
	}
	run.Nodes[idx].Count = 0
	run.Nodes[idx].Cursor = 0
	e.setStatus(run, idx, protocol.NodeStatus_NodeStatus_Activated)
	if def.DungeonID != 0 && def.DungeonID != e.host.InstanceID() {
		e.planTransfer(def.DungeonID)
	}
	if len(def.Fix) > 0 {
		e.runFixes(run, idx, fixActivated)
		if run.Done || run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
			return
		}
	}
	switch def.Type {
	case "ChildQuest":
		e.startChildQuest(run, idx)
	case "Sequence", "ConditionSelector", "Repeater", "AlwaysTrue", "AlwaysFalse":
		if len(kids) == 0 {
			e.complete(run, idx, !def.Is("AlwaysFalse"))
			return
		}
		// ConditionSelector picks its first slot; R-37: in level plays, the first slot whose check holds.
		if run.LevelPlay && def.Is("ConditionSelector") {
			for _, slot := range def.Slots {
				if !e.checkMet(run, slot.Check) {
					continue
				}
				if kid, ok := run.Tree.IndexOf(slot.Node); ok {
					e.activate(run, kid)
					return
				}
			}
		}
		e.activate(run, kids[0])
	case "ParallelSelect":
		if len(kids) == 0 {
			e.complete(run, idx, true)
			return
		}
		for _, kid := range kids {
			if run.Done || run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
				break
			}
			e.activate(run, kid)
		}
	case "Action":
		e.runNodeActions(run, idx, def.Actions, actionGroup)
		e.complete(run, idx, true)
	case "QuestSucceed":
		e.runNodeActions(run, idx, def.Finish, actionSuccess)
		e.setStatus(run, idx, protocol.NodeStatus_NodeStatus_CompletedSuccess)
		e.finishQuest(run)
	case "QuestFailed":
		// Stays active: failure branch never wins except a countdown 领主挑战 / 深塔 runs (R-37f, R-127).
		if run.LevelPlay && def.FailTime > 0 && e.sys.CountdownRuns(run.ID(), def.FailTime) {
			e.timers = append(e.timers, timerWait{run: run.Key, node: idx, dueMs: e.NowMs() + int64(def.FailTime*1000)})
		}
	default:
		e.complete(run, idx, true)
	}
}

func (e *Engine) planTransfer(dungeonID int32) {
	t := e.host.Tables()
	if t == nil {
		return
	}
	target, ok := t.InstanceDungeonById[dungeonID]
	if !ok || target == nil {
		common.Warn("quest", fmt.Sprintf("quest wants dungeon %d: not in InstanceDungeon", dungeonID))
		return
	}
	var pos [3]float32
	if len(target.BornPosition) >= 3 {
		pos = [3]float32{float32(target.BornPosition[0]), float32(target.BornPosition[1]), float32(target.BornPosition[2])}
	}
	// Leaving a story instance: its RecoverWorldLocation says where in the world to come back.
	if current, ok := t.InstanceDungeonById[e.host.InstanceID()]; ok && current != nil &&
		len(current.RecoverWorldLocation) >= 4 && current.RecoverWorldLocation[0] == target.MapConfigId {
		r := current.RecoverWorldLocation
		pos = [3]float32{float32(r[1]), float32(r[2]), float32(r[3])}
	}
	common.Info("quest", fmt.Sprintf("quest moves the player to dungeon %d (map %d) at %.0f,%.0f,%.0f",
		dungeonID, target.MapConfigId, pos[0], pos[1], pos[2]))
	e.sys.OnQuestTransfer(dungeonID)
	e.host.QueueTransfer(dungeonID, pos, false)
}

// PlanExit handles ExitDungeon: back to where the instance's RecoverWorldLocation says (or world 8).
func (e *Engine) PlanExit() {
	if e.sys.LeaveDungeon() {
		return
	}
	t := e.host.Tables()
	if t == nil {
		return
	}
	current, ok := t.InstanceDungeonById[e.host.InstanceID()]
	if !ok || current == nil {
		return
	}
	if r := current.RecoverWorldLocation; len(r) >= 4 && r[0] != 0 {
		e.host.QueueTransfer(r[0], [3]float32{float32(r[1]), float32(r[2]), float32(r[3])}, false)
	} else {
		e.planTransfer(8)
	}
	common.Info("quest", fmt.Sprintf("leaving dungeon %d", e.host.InstanceID()))
}

func (e *Engine) startChildQuest(run *Run, idx int) {
	def := run.Tree.Node(idx)
	common.Info("quest", fmt.Sprintf("quest %d node %d: %s %s", run.ID(), def.ID, def.Cond, run.Tree.Def.Name))
	e.setChildStatus(run, idx, protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Progress)
	e.runNodeActions(run, idx, def.Enter, actionEnter)
	if run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
		return
	}
	if len(def.Fix) > 0 {
		e.runFixes(run, idx, fixStart)
		if run.Done || run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
			return
		}
	}
	cond := def.Cond
	switch cond {
	case "PlayFlow":
		if def.Flow == nil {
			e.complete(run, idx, true)
		} else {
			e.startFlow(run, idx, *def.Flow, protocol.GameCtxType_GameCtxType_PlayFlowChildQuestNode)
		}
		return
	case "CheckTargetBattleAttribute":
		// R-76i: boss phase, judged as the fight goes; without a judgeable option it completes.
		if len(def.Battle) == 0 || e.battleMet(def) {
			e.complete(run, idx, true)
		} else {
			e.battleWaiting = true
		}
		return
	case "HandInItems":
		// R-76g: the NPC's option opens the hand-in view; HandInItemRequest completes the step.
		if len(def.Options) == 0 || len(def.HandIn) == 0 {
			e.complete(run, idx, true)
			return
		}
		// A prop the player never got would block the story.
		for _, need := range def.HandIn {
			var have int32
			for _, id := range need.IDs {
				have += e.host.ItemCount(id)
			}
			if have >= need.Count || len(need.IDs) == 0 {
				continue
			}
			common.Info("quest", fmt.Sprintf("quest %d node %d: hand-in item %d missing, given", run.ID(), def.ID, need.IDs[0]))
			e.host.GrantItem(need.IDs[0], need.Count-have)
		}
		e.offerOptions(run, idx, def)
		return
	case "DoInteract":
		if len(def.Options) == 0 {
			e.complete(run, idx, true)
			return
		}
		e.offerOptions(run, idx, def)
		return
	case "MonsterCreator":
		e.startMonsterCreator(run, idx, def)
		return
	}
	// R-4g level plays, R-83 quests: what the server cannot judge counts as met.
	if IsVarConditionStep(def) {
		if def.Check == nil || e.conditionMet(run, def) {
			e.complete(run, idx, true)
			return
		}
		if !run.LevelPlay {
			e.questConditionsWaiting = true
			common.Info("quest", fmt.Sprintf("quest %d node %d: waits for %s", run.ID(), def.ID, cond))
		}
		return
	}
	if run.LevelPlay && cond == "CheckLevelPlay" {
		// A main tree waiting on its sub level plays: start them.
		for _, lp := range def.LevelPlays {
			if e.IsLevelPlayFinished(lp) || e.IsLevelPlayRunning(lp) {
				continue
			}
			if !e.StartLevelPlay(lp) {
				common.Warn("quest", fmt.Sprintf("quest %d node %d: sub level play %d has no tree", run.ID(), def.ID, lp))
			}
			if run.Done || run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
				return
			}
		}
		if e.conditionMet(run, def) {
			e.complete(run, idx, true)
		}
		return
	}
	if !run.LevelPlay && IsDungeonStep(def) {
		// R-4e. A dungeon without a level play cannot report a finish: complete those.
		if cond == "FinishDungeon" && def.Dungeon != 0 {
			if _, hasLP := e.LevelPlayForDungeon(def.Dungeon); !hasLP {
				common.Info("quest", fmt.Sprintf("quest %d node %d: dungeon %d has no level play, completing", run.ID(), def.ID, def.Dungeon))
				e.noteSkipped(run, def, cond)
				e.complete(run, idx, true)
				return
			}
		}
		if e.dungeonStepMet(run, idx) {
			e.complete(run, idx, true)
		} else {
			common.Info("quest", fmt.Sprintf("quest %d node %d: waits for %s %d/%d", run.ID(), def.ID, cond, def.Dungeon, def.SubType))
		}
		return
	}
	switch cond {
	case "LeaveDungeon":
		return // ends with the dungeon (StopLevelPlays)
	case "Kill":
		e.startKill(run, idx, def)
		return
	case "Timer":
		dur := min(MaxTimerMs, int64(def.Time*1000))
		e.timers = append(e.timers, timerWait{run: run.Key, node: idx, dueMs: e.NowMs() + dur})
		return
	case "CheckEntityState":
		if e.entityStatesMet(def) {
			e.complete(run, idx, true)
			return
		}
		// R-33b: the entities whose state it waits for must be there to be used.
		ids := make([]int64, len(def.States))
		for i, s := range def.States {
			ids[i] = s.EntityID
		}
		e.wake(ids)
		return
	}
	if IsClientCondition(cond) {
		return
	}
	common.Info("quest", fmt.Sprintf("quest %d node %d: %s is server-checked, completing it", run.ID(), def.ID, cond))
	e.noteSkipped(run, def, cond)
	e.complete(run, idx, true)
}

func (e *Engine) offerOptions(run *Run, idx int, def NodeDef) {
	w := e.host.World()
	for _, o := range def.Options {
		w.Wake(o.EntityID)
		if net, ok := e.host.NetIDOf(o.EntityID); ok {
			e.pushOption(run, idx, o, net)
		}
	}
	w.Save()
	e.host.RefreshSpawns()
}

func (e *Engine) startMonsterCreator(run *Run, idx int, def NodeDef) {
	if def.Var == "" {
		if !run.LevelPlay {
			common.Info("quest", fmt.Sprintf("quest %d node %d: MonsterCreator %s outside a dungeon, completing",
				run.ID(), def.ID, describeLongs(def.Creators)))
			e.complete(run, idx, true)
			return
		}
		var wake []int64
		clearNow := e.sys.StartSpawners(run.Key, idx, def.Creators, e.NowMs(), &wake)
		common.Info("quest", fmt.Sprintf("quest %d node %d: spawners %s started, waking %s",
			run.ID(), def.ID, describeLongs(def.Creators), describeLongs(wake)))
		if len(wake) > 0 {
			e.wake(wake)
		}
		if clearNow {
			e.noteSkipped(run, def, "MonsterCreator(no spawner data)")
			e.complete(run, idx, true)
		}
		return
	}
	// R-60: a step that spawns its own bundle, once per run.
	if def.Bundle > 0 && !e.sys.HasPrefab(run.Key, def.Var) {
		e.sys.CreatePrefab(run.Key, def.Var, def.Bundle, def.BundlePos, run.LevelPlay)
		e.host.RefreshSpawns()
		return
	}
	if cleared, ok := e.sys.PrefabGroupCleared(run.Key, def.Var); ok && cleared {
		e.complete(run, idx, true)
	}
}

func (e *Engine) startKill(run *Run, idx int, def NodeDef) {
	// R-128: no targets but a prefab variable: the group a CreatePrefab made must die.
	if len(def.Targets) == 0 && def.Var != "" {
		if cleared, ok := e.sys.PrefabGroupCleared(run.Key, def.Var); ok && cleared {
			e.complete(run, idx, true)
		}
		return
	}
	if len(def.Targets) == 0 {
		e.complete(run, idx, true)
		return
	}
	// All targets, not only TargetsToAwake: quest-owned monsters spawn only when woken (R-13).
	e.wake(def.Targets)
	// R-44: targets already killed in this dungeon run count.
	before := e.killedBefore(def.Targets)
	if before > 0 {
		run.Nodes[idx].Count = int32(before)
		common.Info("quest", fmt.Sprintf("quest %d node %d: %d/%d targets already killed in this run",
			run.ID(), def.ID, before, len(def.Targets)))
		if before >= len(def.Targets) {
			e.complete(run, idx, true)
		}
	}
}

func (e *Engine) entityStatesMet(def NodeDef) bool {
	for _, s := range def.States {
		want := GameplayTagID(e.host.Tables(), s.State)
		have, ok := e.EntityState(s.EntityID)
		if !ok || have != want {
			return false
		}
	}
	return true
}

// battleMet evaluates CheckTargetBattleAttribute; a monster not on scene counts as 0% (R-76i).
func (e *Engine) battleMet(def NodeDef) bool {
	met := 0
	for _, need := range def.Battle {
		ok := len(need.IDs) > 0
		for _, id := range need.IDs {
			pct, present := e.host.HealthPercent(id)
			if !present {
				pct = 0
			}
			if pct < need.Min || pct > need.Max {
				ok = false
			}
		}
		if ok {
			met++
		}
	}
	if def.BattleAny {
		return met > 0
	}
	return met == len(def.Battle)
}

func (e *Engine) checkBattles() {
	e.battleWaiting = false
	for _, run := range slices.Clone(e.runList) {
		if run.Done {
			continue
		}
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated ||
				def.Cond != "CheckTargetBattleAttribute" || len(def.Battle) == 0 {
				continue
			}
			if !e.battleMet(def) {
				e.battleWaiting = true
				continue
			}
			common.Info("quest", fmt.Sprintf("quest %d node %d: battle condition met", run.ID(), def.ID))
			e.BattleStepsDone++
			e.complete(run, i, true)
			e.Save()
			if run.Done {
				break
			}
		}
	}
}

// complete ends node idx and notifies its parent.
func (e *Engine) complete(run *Run, idx int, success bool) {
	st := run.Nodes[idx]
	if st.Status != protocol.NodeStatus_NodeStatus_Activated {
		return
	}
	def := run.Tree.Node(idx)
	defer func() {
		// R-83: a waiting condition step may ask about this node or its quest.
		if !run.LevelPlay && e.questConditionsWaiting {
			e.RecheckConditions()
		}
	}()

	// R-42: already completing (its Finish actions run while it is still active).
	if def.Is("ChildQuest") && st.Cq == protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Finished {
		return
	}
	if def.Is("ChildQuest") {
		if HasOptions(def) {
			e.removeOptions(def)
		}
		if def.Cond == "MonsterCreator" {
			e.sys.DropSpawner(run.Key, idx)
		}
		e.setChildStatus(run, idx, protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Finished)
		e.runNodeActions(run, idx, def.Finish, actionFinish)
		if len(def.Fix) > 0 && !run.Done {
			e.runFixes(run, idx, fixFinish)
		}
		if def.RewardID > 0 {
			e.host.GrantDrop(def.RewardID, run.ID())
		}
	}
	// Finish actions can end the whole quest (another node's QuestSucceed); stop if so.
	if run.Done {
		return
	}
	endStatus := protocol.NodeStatus_NodeStatus_CompletedFailed
	if success {
		endStatus = protocol.NodeStatus_NodeStatus_CompletedSuccess
	}
	e.setStatus(run, idx, endStatus)
	// R-10: a later quest may start once this child quest is done.
	if !run.LevelPlay && e.cat.UnlocksSomething(run.ID(), def.ID) {
		e.AcceptUnlocked()
	}
	parent, hasParent := run.Tree.Parent(idx)
	if !hasParent {
		e.finishQuest(run)
		return
	}
	e.childDone(run, parent, success)
}

func (e *Engine) childDone(run *Run, p int, success bool) {
	ps := &run.Nodes[p]
	if ps.Status != protocol.NodeStatus_NodeStatus_Activated {
		return
	}
	def := run.Tree.Node(p)
	kids := run.Tree.Children[p]
	switch def.Type {
	case "Sequence":
		if !success {
			e.complete(run, p, false)
			return
		}
		next := int(ps.Cursor) + 1
		if next < len(kids) {
			ps.Cursor = int32(next)
			e.activate(run, kids[next])
			return
		}
		e.complete(run, p, true)
	case "ParallelSelect":
		if success {
			ps.Count++
		}
		need := int32(len(kids))
		if def.Count > 0 {
			need = def.Count
		}
		if ps.Count < need {
			return
		}
		for _, kid := range kids {
			e.deactivate(run, kid)
		}
		e.complete(run, p, true)
	case "AlwaysTrue":
		e.complete(run, p, true)
	case "AlwaysFalse":
		e.complete(run, p, false)
	default:
		e.complete(run, p, success)
	}
}

// deactivate stops a branch that lost a ParallelSelect.
func (e *Engine) deactivate(run *Run, idx int) {
	if run.Nodes[idx].Status == protocol.NodeStatus_NodeStatus_Activated {
		def := run.Tree.Node(idx)
		if def.Is("ChildQuest") {
			if def.Cond == "DoInteract" {
				e.removeOptions(def)
			}
			e.setChildStatus(run, idx, protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_NotActive)
		}
		e.setStatus(run, idx, protocol.NodeStatus_NodeStatus_NotActive)
		run.Nodes[idx].Cq = protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_NotActive
	}
	for _, kid := range run.Tree.Children[idx] {
		e.deactivate(run, kid)
	}
}

func (e *Engine) finishQuest(run *Run) {
	def := run.Tree.Def
	id := def.ID
	if run.Done {
		return
	}
	run.Done = true
	if run.LevelPlay {
		common.Info("quest", fmt.Sprintf("level play %d %s finished", id, def.Name))
		e.host.Push(&protocol.BehaviorTreeDeleteNotify{TreeIncIds: []int64{run.Key}})
		e.leaveLevelPlay(run)
		e.removeRun(run.Key)
		e.lpFinished.Set(id, struct{}{})
		e.Vars.DropRun(run.Key)
		// R-127: its prefab groups and spawner waves go with it; R-69: its clearings too.
		e.sys.DropRunData(run.Key)
		e.sys.ReleaseRun(run.Key, true, id)
		// R-37: a 领主挑战 passed.
		e.sys.OnLevelPlayFinished(id)
		// R-62: an open-world play quests wait for (PreLevelPlay).
		if _, isWP := e.cat.WorldPlayIDs[id]; isWP {
			if !e.worldDone.Has(id) {
				e.worldDone.Set(id, struct{}{})
				e.Save()
				e.AcceptUnlocked()
				e.Save()
			}
		}
		// A main tree may wait for this one (CheckLevelPlay).
		e.RecheckConditions()
		return
	}
	common.Info("quest", fmt.Sprintf("quest %d %s finished", id, def.Name))
	e.host.Push(&protocol.QuestStateUpdateNotify{QuestId: id, State: protocol.QuestState_QuestState_Finish})
	e.host.Push(&protocol.BehaviorTreeDeleteNotify{TreeIncIds: []int64{int64(id)}})
	if def.RewardID > 0 {
		e.host.GrantDrop(def.RewardID, id)
	}
	e.removeRun(int64(id))
	e.finished.Set(id, struct{}{})
	// R-83: its own variables go with it (saved as "W" lines).
	e.Vars.DropRun(run.Key)
	// R-67: its NPCs and monsters go back to sleep.
	if hidden := e.hideActorsOf(def); hidden > 0 {
		e.host.World().Save()
		common.Info("quest", fmt.Sprintf("quest %d: %d of its NPCs and monsters back to sleep", id, hidden))
	}
	// R-69: clearings come back; R-74, R-76: options and presentation go.
	e.sys.ReleaseRun(run.Key, false, id)
	// Next quests (R-10).
	e.AcceptUnlocked()
	e.Save()
}
