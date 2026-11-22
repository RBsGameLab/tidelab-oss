// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"slices"

	"tidelab/internal/common"
	"tidelab/protocol"
)

// condEnv answers ConditionEnv queries from Engine's current state.
type condEnv struct {
	e      *Engine
	quest  bool
	fix    bool
	fixPos *[3]float32
}

func (c condEnv) EntityHasState(id int64, state string) (bool, bool) {
	want := GameplayTagID(c.e.host.Tables(), state)
	// No recorded change: still in its configured initial state, which is not tracked here.
	have, ok := c.e.host.World().StateOverride(id)
	if !ok {
		return false, false
	}
	return have == want, true
}

// EntityExists is unknown to quests (not spawned may only mean out of range, R-83).
func (c condEnv) EntityExists(id int64) (bool, bool) {
	if c.quest {
		return false, false
	}
	_, ok := c.e.host.NetIDOf(id)
	return ok, true
}

func (c condEnv) QuestState(q int32) (int32, bool) {
	if c.e.IsFinished(q) {
		return 3, true
	}
	if c.e.IsRunning(q) {
		return 2, true
	}
	return 0, true
}

func (c condEnv) ChildDone(q, node int32) (bool, bool) {
	return c.e.IsChildDone(q, node), true
}

// LevelPlayState: finished level plays are not saved, so for a quest "not running" is unknown (R-83).
func (c condEnv) LevelPlayState(lp int32) (int32, bool) {
	s := c.e.LevelPlayStateOf(lp)
	if c.quest && s == 0 {
		return 0, false
	}
	return s, true
}

func (c condEnv) DungeonID() int32 { return c.e.host.InstanceID() }

func (c condEnv) TreasureClaimed(id int64) (bool, bool) {
	return c.e.host.World().IsGone(id, c.e.host.NowSeconds()), true
}

func (c condEnv) Male() (bool, bool) {
	if c.e.PlayerMale == nil {
		return false, false
	}
	return *c.e.PlayerMale, true
}

// InRange implements CheckInRange (R-76h). Asking marks that movement must recheck.
func (c condEnv) InRange(ids []int64) (bool, bool) {
	if c.fix {
		return false, false
	}
	c.e.rangeWaiting = true
	if c.e.playerPos == nil || c.e.InAnyRange == nil {
		return false, false
	}
	return c.e.InAnyRange(c.e.host.MapID(), ids, *c.e.playerPos)
}

func (c condEnv) PlayerPos() ([3]float32, bool) {
	if c.fix && c.fixPos != nil {
		return *c.fixPos, true
	}
	return [3]float32{}, false
}

func (c condEnv) ItemCount(id int32) (int32, bool) {
	if !c.fix {
		return 0, false
	}
	return c.e.host.ItemCount(id), true
}

func (c condEnv) DungeonCleared(id int32) (bool, bool) {
	if !c.fix {
		return false, false
	}
	return c.e.DungeonCleared(id), true
}

func (c condEnv) NodeStatus(run int64, node int32) (int32, bool) {
	if !c.fix {
		return 0, false
	}
	r := c.e.runOf(run)
	if r == nil {
		return 0, false
	}
	i, ok := r.Tree.IndexOf(node)
	if !ok {
		return 0, false
	}
	return int32(r.Nodes[i].Status), true
}

func (c condEnv) ChildStatus(run int64, node int32) (int32, bool) {
	if !c.fix {
		return 0, false
	}
	r := c.e.runOf(run)
	if r == nil {
		return 0, false
	}
	i, ok := r.Tree.IndexOf(node)
	if !ok {
		return 0, false
	}
	return int32(r.Nodes[i].Cq), true
}

func (e *Engine) checkMet(run *Run, check Check) bool {
	return e.Vars.Evaluate(run.Key, check, condEnv{e: e, quest: !run.LevelPlay})
}

func (e *Engine) conditionMet(run *Run, def NodeDef) bool {
	if def.Cond == "CheckLevelPlay" {
		// Sub level plays without a tree cannot run here: counted as done.
		done := 0
		for _, lp := range def.LevelPlays {
			if e.lpFinished.Has(lp) {
				done++
				continue
			}
			if _, hasTree := e.cat.LevelPlays[lp]; !hasTree {
				done++
			}
		}
		return done >= min(int(def.Need), len(def.LevelPlays))
	}
	return def.Check == nil || e.checkMet(run, *def.Check)
}

func (e *Engine) subTypeOf(instance int32) (int32, bool) {
	t := e.host.Tables()
	if t == nil {
		return 0, false
	}
	if d, ok := t.InstanceDungeonById[instance]; ok && d != nil {
		return d.InstSubType, true
	}
	return 0, false
}

// inDungeon reports whether instance is a dungeon (InstType 2), not a world map or plane (InstType 1).
func (e *Engine) inDungeon(instance int32) bool {
	t := e.host.Tables()
	if t == nil {
		return false
	}
	d, ok := t.InstanceDungeonById[instance]
	return ok && d != nil && d.InstType == 2
}

// dungeonStepMet evaluates FinishDungeon, EnterDungeon, and LeaveDungeon (R-4e).
func (e *Engine) dungeonStepMet(run *Run, idx int) bool {
	def := run.Tree.Node(idx)
	here := e.host.InstanceID()
	switch def.Cond {
	case "FinishDungeon":
		st := &run.Nodes[idx]
		if st.Count >= max(def.Need, 1) {
			return true
		}
		if DungeonMatches(def, here, e.subTypeOf) {
			if st.Cursor != 1 {
				st.Cursor = 1
				e.Save()
			}
			return false
		}
		if st.Cursor == 1 {
			common.Info("quest", fmt.Sprintf("quest %d node %d: left dungeon %d/%d without a finish, completing (fallback)",
				run.ID(), def.ID, def.Dungeon, def.SubType))
			e.noteSkipped(run, def, "FinishDungeon(left)")
			return true
		}
		return false
	case "EnterDungeon":
		return DungeonMatches(def, here, e.subTypeOf)
	default:
		return !e.inDungeon(here) // LeaveDungeon
	}
}

// RecheckConditions completes condition steps that are met now: level plays' condition steps,
// quests' var condition steps (R-83), and dungeon steps (R-4e). Re-entrant calls request another round.
func (e *Engine) RecheckConditions() {
	if e.rechecking {
		e.recheckAgain = true
		return
	}
	e.rechecking = true
	defer func() { e.rechecking = false }()

	e.rangeWaiting = false // set again by any check that still asks about a range
	questProgressed := false
	stillWaiting := false
	for round := 0; round < 32; round++ {
		e.recheckAgain = false
		stillWaiting = false
		progressed := false
		snapshot := slices.Clone(e.runList)
		for _, run := range snapshot {
			for i := range run.Nodes {
				if run.Done {
					break
				}
				def := run.Tree.Node(i)
				if run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated || !def.Is("ChildQuest") {
					continue
				}
				if run.LevelPlay {
					if !IsConditionStep(def) || !e.conditionMet(run, def) {
						continue
					}
				} else if IsVarConditionStep(def) {
					if !e.conditionMet(run, def) {
						stillWaiting = true
						continue
					}
				} else if !IsDungeonStep(def) || !e.dungeonStepMet(run, i) {
					continue
				}
				common.Info("quest", fmt.Sprintf("quest %d node %d: %s met", run.ID(), def.ID, def.Cond))
				e.complete(run, i, true)
				progressed = true
				if !run.LevelPlay {
					questProgressed = true
				}
			}
		}
		if !progressed && !e.recheckAgain {
			break
		}
	}
	e.questConditionsWaiting = stillWaiting
	if questProgressed {
		e.Save()
	}
}

// -----------------------------------------------------------------------------
// R-82a: fix processors (NodeDef.Fix). Conditions evaluate with EvaluateKnown.

type fixWhen int

const (
	fixActivated fixWhen = iota
	fixStart
	fixFinish
	fixRestore
	fixPeriodic
)

func (w fixWhen) String() string {
	switch w {
	case fixActivated:
		return "Activated"
	case fixStart:
		return "Start"
	case fixFinish:
		return "Finish"
	case fixRestore:
		return "Restore"
	default:
		return "Periodic"
	}
}

func fixWhenMatches(fix Fix, when fixWhen) bool {
	switch when {
	case fixActivated:
		return fix.When == "OnActivated"
	case fixStart:
		return fix.When == "OnStartAction"
	case fixFinish:
		return fix.When == "OnFinishAction"
	case fixRestore:
		return fix.When == "OnProgress" || fix.When == "Default"
	default:
		return fix.Period > 0 && (fix.When == "OnProgress" || fix.When == "Default")
	}
}

// treeMap returns the instance a tree's positions are on: the nearest DungeonId up the tree, else the quest's.
func (e *Engine) treeMap(run *Run, idx int) (int32, bool) {
	if run.LevelPlay {
		return e.host.InstanceID(), true
	}
	for n, ok := idx, true; ok; n, ok = run.Tree.Parent(n) {
		if d := run.Tree.Node(n).DungeonID; d != 0 {
			return d, true
		}
	}
	if run.Tree.Def.DungeonID != 0 {
		return run.Tree.Def.DungeonID, true
	}
	return 0, false
}

func (e *Engine) fixMet(run *Run, idx int, check Check) bool {
	tm, ok := e.treeMap(run, idx)
	var fp *[3]float32
	if ok && tm == e.host.InstanceID() {
		fp = e.playerPos
	}
	return e.Vars.EvaluateKnown(run.Key, check, condEnv{e: e, quest: false, fix: true, fixPos: fp})
}

func (e *Engine) runFixes(run *Run, idx int, when fixWhen) {
	def := run.Tree.Node(idx)
	now := e.NowMs()
	for fi, fix := range def.Fix {
		if run.Done || run.Nodes[idx].Status != protocol.NodeStatus_NodeStatus_Activated {
			return
		}
		if !fixWhenMatches(fix, when) {
			continue
		}
		if when == fixPeriodic {
			// The first look waits one period: the step's own Enter actions move the player first.
			key := fixKey{run: run.Key, node: idx, fix: fi}
			last, seen := e.fixLastMs[key]
			if !seen {
				e.fixLastMs[key] = now
				continue
			}
			if now-last < int64(fix.Period*1000) {
				continue
			}
			e.fixLastMs[key] = now
		}
		if e.fixMet(run, idx, fix.Check) {
			e.applyFix(run, idx, fix, when)
		}
	}
}

func fixPos(a QuestAction, key string) ([3]float32, bool) {
	x, okX := ActionFloat(a, key, "X")
	y, okY := ActionFloat(a, key, "Y")
	z, okZ := ActionFloat(a, key, "Z")
	if !okX || !okY || !okZ {
		return [3]float32{}, false
	}
	return [3]float32{float32(x), float32(y), float32(z)}, true
}

func (e *Engine) applyFix(run *Run, idx int, fix Fix, when fixWhen) {
	def := run.Tree.Node(idx)
	e.FixStats.Run++
	common.Info("quest", fmt.Sprintf("quest %d node %d: fix %s (%s) runs %d actions",
		run.ID(), def.ID, fix.When, when, len(fix.Then)))
	e.runNodeActions(run, idx, fix.Then, actionFix)
	finish := false
	for _, a := range fix.Then {
		switch a.Name {
		case "FinishCondition":
			finish = true
		case "ServerSetPlayerPos":
			pos, ok := fixPos(a, "Pos")
			if !ok {
				break
			}
			common.Info("quest", fmt.Sprintf("quest %d node %d: fix puts the player at %.0f,%.0f,%.0f",
				run.ID(), def.ID, pos[0], pos[1], pos[2]))
			var yaw *float64
			if y, ok := ActionFloat(a, "Pos", "A"); ok {
				yaw = &y
			}
			e.teleportHere(pos, yaw)
			e.FixStats.Moved++
		case "TeleportDungeonPos":
			target, okT := ActionInt(a, "DungeonId")
			tp, okP := fixPos(a, "TeleportPos")
			if !okT || !okP {
				break
			}
			if int32(target) == e.host.InstanceID() {
				e.teleportHere(tp, nil)
				e.FixStats.Moved++
				break
			}
			// Pulling the player into another instance every few seconds would trap them.
			if when == fixPeriodic {
				break
			}
			common.Info("quest", fmt.Sprintf("quest %d node %d: fix moves the player to instance %d", run.ID(), def.ID, target))
			e.sys.OnQuestTransfer(int32(target))
			e.host.QueueTransfer(int32(target), tp, false)
			e.FixStats.Transfers++
		}
	}
	if finish && def.Is("ChildQuest") &&
		run.Nodes[idx].Status == protocol.NodeStatus_NodeStatus_Activated &&
		run.Nodes[idx].Cq == protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Progress {
		common.Info("quest", fmt.Sprintf("quest %d node %d: fix finishes the step", run.ID(), def.ID))
		e.FixStats.Finished++
		e.complete(run, idx, true)
	}
}

func (e *Engine) runRunningFixes(when fixWhen) {
	before := e.FixStats.Run
	for _, run := range slices.Clone(e.runList) {
		if run.Done {
			continue
		}
		for i := range run.Nodes {
			def := run.Tree.Node(i)
			if len(def.Fix) == 0 || run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated {
				continue
			}
			if def.Is("ChildQuest") && run.Nodes[i].Cq != protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Progress {
				continue
			}
			e.runFixes(run, i, when)
			if run.Done {
				break
			}
		}
	}
	if e.FixStats.Run != before {
		e.Save()
	}
}
