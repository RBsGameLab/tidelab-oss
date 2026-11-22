// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// BuildDungeonList resolves the dungeon ids of all, quests and explicit ids (deduplicated, in order).
func BuildDungeonList(t *data.DataTables, cat *quests.Catalog, o *Options) []int32 {
	var ids []int32
	seen := map[int32]bool{}
	add := func(id int32) {
		if _, ok := t.InstanceDungeonById[id]; ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if o.DungeonsQuests && cat != nil {
		for _, id := range cat.QuestDungeonIDs() {
			add(id)
		}
	}
	for _, id := range o.DungeonIDs {
		add(id)
	}
	if o.DungeonsAll {
		for _, e := range t.InstanceDungeonEntrance {
			for _, id := range e.InstanceDungeonList {
				add(id)
			}
		}
	}
	return ids
}

// RunDungeon enters a dungeon, plays its level play to the end and leaves.
func (r *Runner) RunDungeon(dungeonID int32) *DungeonReport {
	rep := NewDungeonReport(dungeonID)
	if d, ok := r.Tables().InstanceDungeonById[dungeonID]; ok {
		rep.LevelExpected = d.EntityLevel
	}
	r.DungeonResult = nil
	r.EndFlows()
	r.Flows = nil
	r.Killed, r.Interacted, r.Submitted = map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	r.UnhandledList, r.unhandledSet = nil, map[string]bool{}
	e := r.Quests()
	if e != nil {
		e.SkippedSteps = nil
		e.ClockOffsetMs = 0
	}
	r.LastEnteredLp = nil
	lpEnterBefore := r.LpEnter
	if r.Logs != nil {
		r.Logs.Reset()
	}
	r.SendRequest(&protocol.EnterInstRequest{InstanceId: dungeonID})
	entered := r.InstanceID() == dungeonID
	if (r.LastError != nil && *r.LastError != success) || !entered {
		rep.Outcome = DungeonEnterFailed
		return r.finishDungeonReport(rep)
	}
	if e != nil {
		rep.LevelPlays = append(rep.LevelPlays, e.RunningLevelPlays()...)
	}
	if len(rep.LevelPlays) == 0 {
		if r.LpEnter > lpEnterBefore && r.LastEnteredLp != nil {
			rep.LevelPlays = append(rep.LevelPlays, *r.LastEnteredLp)
			rep.Outcome = DungeonEndedAtStart
			if r.DungeonResult != nil {
				rep.Outcome = DungeonFailed
				if *r.DungeonResult {
					rep.Outcome = DungeonSuccess
				}
			}
		} else {
			rep.Outcome = DungeonNoLevelPlay
		}
		if rep.Outcome != DungeonSuccess {
			r.leaveDungeon(rep)
			return r.finishDungeonReport(rep)
		}
	}
	idle := 0
	var lastSig uint64
	i := 0
	for ; i < MaxDungeonIterations; i++ {
		var steps []quests.Step
		if e != nil {
			steps = e.ActiveSteps(true)
		}
		if r.DungeonResult != nil {
			onlyLeave := true
			for _, st := range steps {
				if st.Def.Cond != "LeaveDungeon" {
					onlyLeave = false
				}
			}
			if onlyLeave {
				break
			}
		}
		if len(steps) == 0 && r.DungeonResult == nil && idle >= IdleLimit {
			break
		}
		var target *world.Vec3
		setTarget := func(id int64) {
			if target == nil {
				if p, ok := r.RowPos(id); ok {
					target = &p
				}
			}
		}
		for _, st := range steps {
			def := st.Def
			key := st.Run*31 + int64(def.ID)
			if rangeID, ok := RangeTarget(def); ok {
				if rp, ok := r.RowPos(rangeID); ok {
					r.MoveTo(rp)
				}
			}
			switch {
			case quests.IsClientCondition(def.Cond):
				if r.Submitted[key] {
					continue
				}
				r.Submitted[key] = true
				r.SendRequest(&protocol.SubmitNodeRequest{TreeOwnerId: r.PlayerID(), TreeIncId: st.Run, NodeId: def.ID})
			case def.Cond == "DoInteract":
				for _, o := range def.Options {
					if net, ok := r.NetIDOf(o.EntityID); ok {
						r.SendRequest(&protocol.EntityDynamicInteractRequest{EntityId: net, OptionGuid: o.Guid})
					} else {
						setTarget(o.EntityID)
					}
				}
			case def.Cond == "CheckEntityState":
				for _, sc := range def.States {
					if net, ok := r.NetIDOf(sc.EntityID); ok {
						if r.Interacted[sc.EntityID] {
							continue
						}
						r.Interacted[sc.EntityID] = true
						if p, ok := r.RowPos(sc.EntityID); ok {
							r.MoveTo(p)
						}
						r.SendRequest(&protocol.EntityInteractRequest{EntityId: net, OptionIndex: 0})
					} else {
						setTarget(sc.EntityID)
					}
				}
			case def.Cond == "Kill":
				for _, t := range def.Targets {
					if _, ok := r.NetIDOf(t); !ok {
						setTarget(t)
					}
				}
			}
		}
		r.EndFlows()
		r.KillMonsters(rep)
		here := r.PlayerPos()
		jitter := float32(3000)
		if i%2 != 0 {
			jitter = -3000
		}
		if target != nil {
			r.MoveTo(*target)
			r.MoveTo(*target)
		} else {
			r.MoveTo(world.Vec3{X: here.X + jitter, Y: here.Y, Z: here.Z})
		}
		h := fnv64{0}
		for _, st := range steps {
			h.addInt(st.Def.ID)
		}
		h.addInt(int32(len(r.Killed)))
		h.addInt(int32(r.ProgressEntities()))
		h.addOptBool(r.DungeonResult)
		sig := h.value()
		if sig == lastSig {
			idle++
		} else {
			idle = 0
		}
		lastSig = sig
		if idle >= IdleLimit {
			if e != nil {
				if due, ok := e.NextDue(); ok {
					if now := e.NowMs(); due > now {
						e.ClockOffsetMs += due - now + 1
					}
					rep.ClockSkips++
					idle = 0
					continue
				}
			}
			rep.StuckAt = r.describeStuckLevelPlays()
			break
		}
	}
	rep.Iterations = i
	rep.Outcome = DungeonStuck
	if r.DungeonResult != nil {
		rep.Outcome = DungeonFailed
		if *r.DungeonResult {
			rep.Outcome = DungeonSuccess
		}
	}
	d, ok := r.Tables().InstanceDungeonById[dungeonID]
	if rep.Outcome == DungeonSuccess && ok && d.RewardId > 0 {
		r.SendRequest(&protocol.GetInstExchangeRewardRequest{ExchangeCount: 1})
		rep.RewardOK = r.LastError == nil || *r.LastError == success
	} else {
		rep.RewardOK = true
	}
	r.leaveDungeon(rep)
	return r.finishDungeonReport(rep)
}

func (r *Runner) describeStuckLevelPlays() string {
	e := r.Quests()
	if e == nil {
		return "(no active node)"
	}
	var nodes []quests.Step
	for _, run := range e.Runs() {
		if !run.LevelPlay || run.Done {
			continue
		}
		for idx := range run.Nodes {
			if run.Nodes[idx].Status == protocol.NodeStatus_NodeStatus_Activated {
				nodes = append(nodes, quests.Step{Run: run.Key, Tree: run.ID(), Def: run.Tree.Node(idx), LevelPlay: true})
			}
		}
	}
	leaves := 0
	for _, n := range nodes {
		if n.Def.Is("ChildQuest") {
			leaves++
		}
	}
	var sb strings.Builder
	for _, st := range nodes {
		leaf := st.Def.Is("ChildQuest")
		if leaves > 0 && !leaf {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		kind := st.Def.Type
		if leaf {
			kind = st.Def.Cond
		}
		fmt.Fprintf(&sb, "%d:%d:%s", st.Tree, st.Def.ID, kind)
		if leaf && st.Def.Cond == "CheckEntityState" {
			for _, sc := range st.Def.States {
				where := "absent"
				if _, ok := r.NetIDOf(sc.EntityID); ok {
					where = "spawned"
				}
				cur := "null"
				if v, ok := e.EntityState(sc.EntityID); ok {
					cur = strconv.Itoa(int(v))
				}
				fmt.Fprintf(&sb, "[%d=%s,state %s,want %s]", sc.EntityID, where, cur, sc.State)
			}
		}
	}
	if sb.Len() == 0 {
		return "(no active node)"
	}
	return sb.String()
}

func (r *Runner) leaveDungeon(rep *DungeonReport) {
	r.SendRequest(&protocol.LeaveInstRequest{})
	rep.LeftOK = r.Session().Scene != nil && r.InstanceID() != rep.Dungeon
}

func (r *Runner) finishDungeonReport(rep *DungeonReport) *DungeonReport {
	if e := r.Quests(); e != nil {
		seen := map[string]bool{}
		var parts []string
		for _, sk := range e.SkippedSteps {
			if !seen[sk.Cond] {
				seen[sk.Cond] = true
				parts = append(parts, sk.Cond)
			}
		}
		rep.Skipped = strings.Join(parts, ",")
	}
	rep.Unhandled = r.UnhandledSummary()
	if r.Logs != nil {
		rep.Warnings, rep.FirstWarning = r.Logs.Warnings()
	}
	return rep
}

// RunDungeonsMode runs each dungeon and writes the dungeon report.
func (r *Runner) RunDungeonsMode(ids []int32, reportPath string) int {
	reports := make([]*DungeonReport, 0, len(ids))
	start := time.Now()
	for n, id := range ids {
		rep := r.runDungeonSafely(id)
		reports = append(reports, rep)
		badLevel := rep.LevelExpected > 0 && rep.Monsters > 0 && (rep.LevelMin != rep.LevelExpected || rep.LevelMax != rep.LevelExpected)
		at, lvl, skipped := "", "", ""
		if rep.StuckAt != "" {
			at = " at " + rep.StuckAt
		}
		if badLevel {
			lvl = " LEVEL!"
		}
		if rep.Skipped != "" {
			skipped = " skipped " + rep.Skipped
		}
		r.printf("[%d/%d] %d %s%s monsters %d lv %d-%d/%d%s%s\n", n+1, len(ids), id, rep.Outcome, at, rep.Monsters, rep.LevelMin, rep.LevelMax, rep.LevelExpected, lvl, skipped)
	}
	battle := 0
	if e := r.Quests(); e != nil {
		battle = e.BattleStepsDone
	}
	if err := WriteDungeonReport(reports, reportPath, battle, int64(time.Since(start).Seconds()), r.Out); err != nil {
		r.printf("robot: writing %s failed: %v\n", reportPath, err)
		return 1
	}
	return 0
}

func (r *Runner) runDungeonSafely(id int32) (rep *DungeonReport) {
	defer func() {
		if x := recover(); x != nil {
			msg := fmt.Sprint(x)
			r.printf("robot: dungeon %d crashed the robot: %s\n", id, msg)
			rep = NewDungeonReport(id)
			rep.FirstWarning = SanitizeField(msg)
		}
	}()
	return r.RunDungeon(id)
}
