// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"tidelab/internal/quests"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// SideTypes are R-42's side quest types: 支线 2, 角色传 3, 系列 4, 世界/地点 9, 偶遇 14.
var SideTypes = []int32{2, 3, 4, 9, 14}

// PrepareFreshStory is R-3b: a fresh story save starting at startQuest.
func PrepareFreshStory(playerDir string, cat *quests.Catalog, startQuest int32, out io.Writer) {
	deleteIfPresent(playerDir, "world_state", "dungeon", "world_net_ids")
	chain := cat.PreQuestChain(startQuest)
	var sb strings.Builder
	for _, id := range chain {
		fmt.Fprintf(&sb, "F %d\n", id)
	}
	fmt.Fprintf(&sb, "P %d\n", startQuest)
	os.WriteFile(filepath.Join(playerDir, "quests"), []byte(sb.String()), 0o644)
	fmt.Fprintf(out, "robot: story starts at %d %s (%d quests before it counted as done)\n", startQuest, cat.QuestName(startQuest), len(chain))
}

// PrepareFreshSide is R-42 / R-130: a fresh save with the main quests (and the earlier shards'
// side quests) finished.
func PrepareFreshSide(playerDir string, cat *quests.Catalog, o *Options, out io.Writer) {
	deleteIfPresent(playerDir, "world_state", "dungeon", "world_net_ids")
	mains := cat.QuestIDsOfTypes(1)
	var sb strings.Builder
	for _, id := range mains {
		fmt.Fprintf(&sb, "F %d\n", id)
	}
	if o.ShardCount > 1 {
		sides := cat.QuestIDsOfTypes(SideTypes...)
		start, end := o.ShardRange(len(sides))
		for i := 0; i < start; i++ {
			fmt.Fprintf(&sb, "F %d\n", sides[i])
		}
		fmt.Fprintf(out, "robot: side: shard %d/%d: quests %d..%d of %d, the %d before it counted as done\n", o.ShardIndex, o.ShardCount, start, end, len(sides), start)
	}
	os.WriteFile(filepath.Join(playerDir, "quests"), []byte(sb.String()), 0o644)
	fmt.Fprintf(out, "robot: side: %d main quests counted as done\n", len(mains))
}

// PrepareFreshRide is R-99: the 114000026 node 138 (乘船入城) save snapshot on map 8.
func PrepareFreshRide(playerDir string) {
	deleteIfPresent(playerDir, "world_state", "dungeon", "world_net_ids")
	const q = "F 145000004\n" +
		"P 114000026 0:2:0:0:3 1:4:4:0:0 3:4:0:0:2 4:4:4:0:0 5:4:4:0:0 6:4:4:0:0 7:4:0:0:1 8:4:0:0:1 10:4:0:0:0 11:4:0:0:0 12:4:4:0:0 13:2:0:0:0 14:4:0:1:0 15:4:4:0:0 19:4:4:0:0 20:4:0:0:1 21:4:0:0:0 22:4:0:0:3 23:4:4:0:0 24:4:4:0:0 25:4:0:1:0 26:4:4:0:0 27:5:0:0:0 28:4:4:0:0 47:2:3:0:0 50:4:4:0:0 51:2:0:0:2 52:4:4:0:0 53:2:0:0:0 54:2:0:0:0 55:4:4:0:0 56:4:0:3:0 57:4:4:0:0 58:4:4:0:0 59:4:0:1:0\n"
	os.WriteFile(filepath.Join(playerDir, "quests"), []byte(q), 0o644)
	os.WriteFile(filepath.Join(playerDir, "last_scene"), []byte(".{ .instance_id = 8 }"), 0o644)
}

// PrepareFreshActivityTasks is R-79: clears the daily activity task, turntable and scratch card saves.
func PrepareFreshActivityTasks(playerDir string) {
	deleteIfPresent(playerDir, "activity_task", "turntable", "scratch_card")
}

func deleteIfPresent(dir string, names ...string) {
	for _, n := range names {
		os.Remove(filepath.Join(dir, n))
	}
}

func (r *Runner) storyQuestOf(list *[]*StoryQuestReport, id int32) *StoryQuestReport {
	for _, q := range *list {
		if q.ID == id {
			return q
		}
	}
	q := &StoryQuestReport{ID: id}
	if e := r.Quests(); e != nil {
		q.Name, q.Kind = e.Catalog().QuestName(id), e.Catalog().QuestType(id)
	}
	*list = append(*list, q)
	return q
}

func (r *Runner) noteDungeon(list *[]*StoryQuestReport, dungeon int32, what string) {
	e := r.Quests()
	if e == nil {
		return
	}
	for _, id := range e.RunningQuestIDs() {
		q := r.storyQuestOf(list, id)
		if q.Dungeons.Len() > 0 {
			q.Dungeons.WriteByte(' ')
		}
		fmt.Fprintf(&q.Dungeons, "%d:%s", dungeon, what)
	}
}

// goForDungeonCond is R-83: satisfies a quest condition group's CompareDungeonId (Eq/Ne).
func (r *Runner) goForDungeonCond(list *[]*StoryQuestReport, op string, id int32, entering **int32, key int64) {
	here := r.InstanceID()
	wantIn := op == "Eq"
	if wantIn == (here == id) {
		return
	}
	tkey := key*977 + int64(id)
	if r.Submitted[tkey] {
		return
	}
	which := here
	if wantIn {
		which = id
	}
	d, ok := r.Tables().InstanceDungeonById[which]
	if !ok {
		return
	}
	worldMap := d.InstType == 1 && d.InstSubType == 13
	if wantIn {
		if worldMap {
			tp, ok := r.TeleporterNear(id, world.Vec3{})
			if !ok {
				return
			}
			r.Submitted[tkey] = true
			r.Travels++
			r.printf("[cond] travel to map %d (teleporter %d)\n", id, tp)
			r.SendRequest(&protocol.TeleportTransferRequest{Id: tp})
			return
		}
		if *entering != nil && **entering == id {
			return
		}
		r.Submitted[tkey] = true
		e := id
		*entering = &e
		r.noteDungeon(list, id, "enter")
		r.SendRequest(&protocol.EnterInstRequest{InstanceId: id})
		return
	}
	if worldMap && here == 8 {
		return
	}
	r.Submitted[tkey] = true
	if worldMap {
		tp, ok := r.TeleporterNear(8, world.Vec3{})
		if !ok {
			return
		}
		r.printf("[cond] leave map %d for map 8 (teleporter %d)\n", here, tp)
		r.SendRequest(&protocol.TeleportTransferRequest{Id: tp})
		return
	}
	r.noteDungeon(list, here, "left")
	r.SendRequest(&protocol.LeaveInstRequest{})
}

// RunSide is R-42: runs the side quests one at a time, respecting --shard=k/n.
func (r *Runner) RunSide(types []int32, perQuest int, o *Options) []*StoryQuestReport {
	var all []*StoryQuestReport
	e := r.Quests()
	if e == nil {
		return all
	}
	ids := e.Catalog().QuestIDsOfTypes(types...)
	r.printf("robot: side: %d quests\n", len(ids))
	start, end := o.ShardRange(len(ids))
	for n, id := range ids {
		if n < start || n >= end || e.IsFinished(id) {
			continue
		}
		if !e.IsRunning(id) {
			r.SendQuestCommand(fmt.Sprintf("quest start %d", id))
		}
		target := id
		for _, q := range r.RunStoryUntil(1, true, perQuest, &target) {
			if q.ID != id {
				continue
			}
			all = append(all, q)
			r.printf("robot: side [%d/%d] %d %s: %s, %d skips\n", n+1, len(ids), id, q.Name, q.Outcome, q.Skips)
		}
	}
	return all
}

func (r *Runner) RunStory(maxQuests int, skipStuck bool) []*StoryQuestReport {
	return r.RunStoryUntil(maxQuests, skipStuck, MaxStoryIterations, nil)
}

func intp(v int) *int { return &v }

// RunStoryUntil plays the running quests round by round until maxQuests finished (or untilQuest
// finished), skipping stuck steps with "quest next" when skipStuck.
func (r *Runner) RunStoryUntil(maxQuests int, skipStuck bool, iterationCap int, untilQuest *int32) []*StoryQuestReport {
	r.AwayDone = map[uint64]bool{}
	var list []*StoryQuestReport
	e := r.Quests()
	if e == nil {
		return list
	}
	totalSkips, idle := 0, 0
	var lastSig, lastStepsSig, stallSig, dungeonStallSig uint64
	var entering *int32
	stall, dungeonStall, finishedCount := 0, 0, 0
	optionsTried := map[string]bool{}
	i := 0
	for ; i < iterationCap; i++ {
		r.Phase.Rounds++
		t0 := time.Now()
		running := e.RunningQuestIDs()
		for _, id := range running {
			if q := r.storyQuestOf(&list, id); q.AcceptedAt == nil {
				q.AcceptedAt = intp(i)
			}
		}
		for _, q := range list {
			if q.Outcome == StoryRunning && !slices.Contains(running, q.ID) && e.IsFinished(q.ID) {
				q.Outcome = StoryFinished
				q.FinishedAt = intp(i)
				finishedCount++
				r.printf("[%d] quest %d %s finished\n", i, q.ID, q.Name)
			}
		}
		if len(running) == 0 {
			break
		}
		if untilQuest != nil {
			if e.IsFinished(*untilQuest) {
				break
			}
		} else if finishedCount >= maxQuests {
			break
		}
		if r.DungeonResult != nil && r.InDungeon() {
			ok := *r.DungeonResult
			if di, has := r.Tables().InstanceDungeonById[r.InstanceID()]; has && di.RewardId > 0 {
				r.SendRequest(&protocol.GetInstExchangeRewardRequest{ExchangeCount: 1})
			}
			what := "failed"
			if ok {
				what = "success"
			}
			r.noteDungeon(&list, r.InstanceID(), what)
			r.DungeonResult = nil
			r.SendRequest(&protocol.LeaveInstRequest{})
			continue
		}

		steps := e.ActiveSteps(false)
		sh := fnv64{1}
		for _, st := range steps {
			sh.addLong(st.Run)
			sh.addInt(st.Def.ID)
		}
		stepsSig := sh.value()
		r.Phase.StepsMs += time.Since(t0).Milliseconds()
		r.StepsSig = stepsSig
		if stepsSig != lastStepsSig {
			r.Submitted = map[int64]bool{}
			r.Interacted = map[int64]bool{}
			lastStepsSig = stepsSig
		}
		if q := r.Session().QuestState; r.TryOptions && q != nil {
			for _, op := range slices.Clone(q.Options.All()) {
				if op.Flow == nil || optionsTried[op.Guid] {
					continue
				}
				net, ok := r.NetIDOf(op.Entity)
				if !ok {
					continue
				}
				optionsTried[op.Guid] = true
				r.SendRequest(&protocol.EntityDynamicInteractRequest{EntityId: net, OptionGuid: op.Guid})
			}
		}

		var targets []world.Vec3
		var away *Loc
		var killTargets []int64
		t0 = time.Now()
		for _, st := range steps {
			def := st.Def
			key := st.Run*31 + int64(def.ID)
			if rangeID, ok := RangeTarget(def); ok {
				if rp, ok := r.RowPos(rangeID); ok {
					r.MoveTo(rp)
				}
			}
			if !st.LevelPlay && quests.IsVarConditionStep(def) && def.Check != nil && def.Check.Conds != nil {
				check := def.Check
				for _, c := range check.Conds {
					switch {
					case c.T == "CheckInRange" && c.InRange:
						for _, id := range c.Entities {
							rg := r.Ranges.Find(id)
							if rg == nil {
								if _, ok := r.RowPos(id); !ok {
									r.NoteTargetAt(&targets, &away, id)
								}
								continue
							}
							if rg.Map == r.MapID() {
								targets = append(targets, rg.Center)
							} else if away == nil && !r.AwayDone[AwayKey(r.StepsSig, rg.Map)] {
								away = &Loc{rg.Map, rg.Center}
							}
						}
					case c.T == "CompareDungeonId" && !HasVarPart(check):
						r.goForDungeonCond(&list, c.Op, c.Dungeon, &entering, key)
					case (c.T == "CheckEntitesExist" && !c.Exist) || c.T == "CheckTreasureBeenClaimed":
						ids := c.Entities
						if c.T == "CheckTreasureBeenClaimed" {
							ids = []int64{c.Entity}
						}
						for _, id := range ids {
							if net, ok := r.NetIDOf(id); ok {
								killTargets = append(killTargets, id)
								if r.Interacted[id] {
									continue
								}
								r.Interacted[id] = true
								if p, ok := r.RowPos(id); ok {
									r.MoveTo(p)
								}
								r.SendRequest(&protocol.EntityInteractRequest{EntityId: net, OptionIndex: 0})
							} else if c.Exist || c.T == "CheckTreasureBeenClaimed" {
								r.NoteTargetAt(&targets, &away, id)
							}
						}
					case c.T == "CompareQuestState" && c.Op == "Eq" && c.Num >= 2 && !e.IsFinished(c.Quest) && !e.IsRunning(c.Quest):
						qkey := key*613 + int64(c.Quest)
						if !r.Submitted[qkey] {
							r.Submitted[qkey] = true
							r.printf("[cond] quest %d waits for quest %d: starting it\n", st.Tree, c.Quest)
							r.SendQuestCommand(fmt.Sprintf("quest start %d", c.Quest))
						}
					}
				}
			}
			var condStates []quests.StateCond
			if !st.LevelPlay && quests.IsVarConditionStep(def) {
				condStates = GroupStates(def)
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
						okey := key*131 + o.EntityID
						if r.Submitted[okey] {
							continue
						}
						r.Submitted[okey] = true
						if p, ok := r.RowPos(o.EntityID); ok {
							r.MoveTo(p)
						}
						r.SendRequest(&protocol.EntityDynamicInteractRequest{EntityId: net, OptionGuid: o.Guid})
					} else {
						r.NoteTargetAt(&targets, &away, o.EntityID)
					}
				}
			case def.Cond == "HandInItems":
				if r.Submitted[key] {
					continue
				}
				r.Submitted[key] = true
				req := &protocol.HandInItemRequest{TreeOwnerId: r.PlayerID(), TreeIncId: st.Run, NodeId: def.ID}
				for _, need := range def.HandIn {
					if len(need.IDs) == 0 {
						continue
					}
					req.HandInItem = append(req.HandInItem, &protocol.PbHandInItem{Count: need.Count,
						HandItemInfo: []*protocol.PbHandInItemInfo{{ItemId: need.IDs[0], Num: need.Count}}})
				}
				r.SendRequest(req)
			case def.Cond == "CheckEntityState" || len(condStates) > 0:
				states := def.States
				if len(condStates) > 0 {
					states = condStates
				}
				for _, sc := range states {
					if net, ok := r.NetIDOf(sc.EntityID); ok {
						if !r.Interacted[sc.EntityID] {
							r.Interacted[sc.EntityID] = true
							if p, ok := r.RowPos(sc.EntityID); ok {
								r.MoveTo(p)
							}
							r.SendRequest(&protocol.EntityInteractRequest{EntityId: net, OptionIndex: 0})
							r.SendSelfEvents(net, sc.EntityID)
							if _, known := e.EntityState(sc.EntityID); !known {
								r.SendRequest(&protocol.TargetGearHitRequest{EntityId: net})
							}
						}
					} else {
						r.NoteTargetAt(&targets, &away, sc.EntityID)
					}
					dkey := sc.EntityID*7 + 3 + int64(hashString(sc.State)&0xffff)*1_000_003
					tries := r.DriveTries[dkey]
					if how := r.DriveState(sc.EntityID, sc.State, 0, tries); how != "" {
						r.DriveTries[dkey] = tries + 1
						r.Drives++
						if !st.LevelPlay {
							q := r.storyQuestOf(&list, st.Tree)
							if q.Drove.Len() > 0 {
								q.Drove.WriteByte(' ')
							}
							fmt.Fprintf(&q.Drove, "%d:%d=%s", def.ID, sc.EntityID, how)
						}
					}
				}
			case def.Cond == "Kill":
				for _, t := range def.Targets {
					if _, ok := r.NetIDOf(t); ok {
						killTargets = append(killTargets, t)
					} else {
						r.NoteTargetAt(&targets, &away, t)
					}
				}
			case !st.LevelPlay && (def.Cond == "FinishDungeon" || def.Cond == "EnterDungeon"):
				if did, ok := r.StepDungeon(def); ok && r.InstanceID() != did && (entering == nil || *entering != did) {
					d := did
					entering = &d
					r.noteDungeon(&list, did, "enter")
					r.SendRequest(&protocol.EnterInstRequest{InstanceId: did})
				}
			case !st.LevelPlay && def.Cond == "LeaveDungeon" && r.InDungeon():
				r.SendRequest(&protocol.LeaveInstRequest{})
			}
		}
		if entering != nil && r.InstanceID() == *entering {
			entering = nil
		}
		r.Phase.ActMs += time.Since(t0).Milliseconds()
		t0 = time.Now()
		r.EndFlows()
		r.MoveLifts()
		if r.InDungeon() {
			r.KillMonsters(NewDungeonReport(r.InstanceID()))
		} else {
			r.KillConfigs(killTargets)
		}
		r.Phase.FlowsMs += time.Since(t0).Milliseconds()

		if (len(targets) == 0 || stall >= StallLimitKnown) && !r.InDungeon() && away != nil {
			loc := *away
			moved := false
			if !world.HasWorldSpawns(r.InstanceID()) && r.InstanceID() != 0 && loc.Map != r.InstanceID() {
				before := r.InstanceID()
				r.printf("[%d] leaving story instance %d for map %d\n", i, before, loc.Map)
				r.noteDungeon(&list, before, "left")
				if r.SendRequest(&protocol.LeaveInstRequest{}) && r.InstanceID() != before {
					moved = true
				}
			}
			if moved {
				continue
			}
			r.AwayDone[AwayKey(r.StepsSig, loc.Map)] = true
			if sc := r.Session().Scene; sc != nil {
				r.AwayDone[AwayKey(r.StepsSig, sc.MapID)] = true
			}
			if tp, ok := r.TeleporterNear(loc.Map, loc.Pos); ok {
				r.Travels++
				r.printf("[%d] travel to map %d (teleporter %d)\n", i, loc.Map, tp)
				r.SendRequest(&protocol.TeleportTransferRequest{Id: tp})
				continue
			}
			if inst, ok := r.StoryInstance(loc.Map); ok {
				r.Travels++
				r.printf("[%d] enter story instance %d\n", i, inst)
				r.noteDungeon(&list, inst, "enter")
				r.SendRequest(&protocol.EnterInstRequest{InstanceId: inst})
				continue
			}
			r.printf("[%d] no teleporter on map %d\n", i, loc.Map)
		}

		t0 = time.Now()
		pos := r.PlayerPos()
		jitter := float32(3000)
		if i%2 != 0 {
			jitter = -3000
		}
		if len(targets) > 0 {
			pick := targets[(i/RoundsPerTarget)%len(targets)]
			r.MoveTo(pick)
			r.MoveTo(pick)
		} else {
			r.MoveTo(world.Vec3{X: pos.X + jitter, Y: pos.Y, Z: pos.Z})
		}
		r.Phase.MoveMs += time.Since(t0).Milliseconds()

		h := fnv64{0}
		h.addU(stepsSig)
		h.addInt(int32(len(r.Killed)))
		h.addInt(int32(r.ProgressEntities()))
		if sc := r.Session().Scene; sc != nil {
			h.addInt(sc.InstanceID)
		}
		h.addOptBool(r.DungeonResult)
		h.addInt(int32(finishedCount))
		sig := h.value()
		if sig == lastSig {
			idle++
		} else {
			idle = 0
		}
		lastSig = sig
		if stepsSig == stallSig && !r.InDungeon() {
			stall++
		} else {
			stall, stallSig = 0, stepsSig
		}
		if stepsSig == dungeonStallSig && r.InDungeon() {
			dungeonStall++
		} else {
			dungeonStall, dungeonStallSig = 0, stepsSig
		}
		if dungeonStall >= DungeonStallLimit {
			idle, dungeonStall = IdleLimit, 0
		}
		if stall >= StallLimit || (stall >= StallLimitKnown && !r.InDungeon() && r.NothingCanChange(steps)) {
			idle, stall = IdleLimit, 0
		}
		if idle < IdleLimit {
			continue
		}
		if due, ok := e.NextDue(); ok {
			if now := e.NowMs(); due > now {
				e.ClockOffsetMs += due - now + 1
			}
			idle = 0
			continue
		}
		if r.InDungeon() {
			prev := r.InstanceID()
			r.printf("[%d] dungeon %d stalled, leaving\n", i, prev)
			r.noteDungeon(&list, prev, "left")
			if r.SendRequest(&protocol.LeaveInstRequest{}) && r.InstanceID() != prev {
				idle = 0
				continue
			}
		}
		if skipStuck && totalSkips < MaxSkips {
			stuckIDs := e.RunningQuestIDs()
			allWithoutStep := len(stuckIDs) > 0
			for _, id := range stuckIDs {
				q := r.storyQuestOf(&list, id)
				if q.Skipped.Len() > 0 {
					q.Skipped.WriteString(" | ")
				}
				from := q.Skipped.Len()
				r.DescribeQuestInto(id, &q.Skipped)
				if q.Skipped.String()[from:] != "(no active step)" {
					allWithoutStep = false
				}
				q.Skips++
			}
			totalSkips++
			if allWithoutStep {
				r.printf("[%d] stuck with no active step: not skipping\n", i)
				break
			}
			r.printf("[%d] stuck, quest next (%d)\n", i, totalSkips)
			r.SendQuestCommand("quest next")
			idle = 0
			continue
		}
		break
	}
	for _, id := range e.RunningQuestIDs() {
		q := r.storyQuestOf(&list, id)
		q.Outcome = StoryStuck
		if i >= iterationCap {
			q.Outcome = StoryOutOfIterations
		}
		r.DescribeQuestInto(id, &q.StuckAt)
	}
	r.printf("robot: story ran %d iterations\n", i)
	return list
}

// BuildStoryCounters are the end-of-run counters.
func (r *Runner) BuildStoryCounters() *StorySummaryCounters {
	s := r.Session()
	c := &StorySummaryCounters{Unhandled: r.UnhandledSummary(), StatesDriven: r.StateSetters.Counters,
		ItemsGiven: s.Actions().QuestItemsGiven, ItemsTaken: s.Actions().QuestItemsTaken,
		AreaStatesChanged: s.World.Areas.Changes, EntitiesMoved: s.World.Moves.MoveCount}
	if r.Logs != nil {
		c.Warnings, _ = r.Logs.Warnings()
	}
	if q := s.QuestState; q != nil {
		hg := q.HideGroups.Stats
		c.HideHidden, c.HideEntities, c.HideShown, c.HideReleased, c.HideRemoved, c.HideActive = hg.Hidden, hg.Entities, hg.Shown, hg.Released, hg.Removed, q.HideGroups.Count()
		ost := q.Options.Stats
		c.OptionsAdded, c.OptionsRemoved, c.OptionsReleased, c.OptionsPicked, c.OptionsStillOffered = ost.Added, ost.Removed, ost.Released, ost.Picked, q.Options.Count()
		ps := q.Presentation.Stats
		c.PresAudio, c.PresGuests, c.PresBubbles, c.PresBubblesCleared, c.PresTimeLocks = ps.Audio, ps.Guests, ps.Bubbles, ps.BubblesCleared, ps.TimeLocks
		c.PresWeather, c.PresSwitchedOff, c.PresSwitchedOn, c.PresUndoneAtEnd, c.PresStillOn = ps.Weather, ps.SwitchedOff, ps.SwitchedOn, ps.UndoneAtEnd, q.Presentation.Count()
		vs := q.Vehicles.Stats
		c.VehiclesBoarded, c.VehiclesLeft, c.VehiclesNotDone = vs.Boarded, vs.Left, vs.NotDone
	}
	if e := r.Quests(); e != nil {
		c.OptionFlowsPending, c.HandIns, c.BattleStepsDone = e.OptionFlowsPending(), e.HandIns, e.BattleStepsDone
		c.TeleportsSent, c.TeleportsHeld = e.Teleports, e.TeleportsHeld
		c.Fixes = FixCounts(e.FixStats)
	}
	return c
}
