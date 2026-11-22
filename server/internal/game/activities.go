// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// ----- sign-ins (R-37) -----

// ActivitySigns are R-37's sign-in activities (ActivitySign, 7 days each). Day N unlocks N-1
// days after the player first saw the activity; a day is claimed once, the free gift once. Daily
// counters (double rewards) share the file. Saved in activity_sign:
// "<activity> <first day> <claimed mask> <free 0|1>". Ported from our C# Game/Activities.cs.
type ActivitySigns struct {
	path    string
	entries omap.Map[int32, SignEntry]
}

type SignEntry struct {
	FirstDay  int64
	Claimed   uint32
	FreeTaken bool
}

func NewActivitySigns(files *save.PlayerFiles) *ActivitySigns {
	a := &ActivitySigns{path: files.PathOf("activity_sign")}
	for _, f := range readLineFile(a.path) {
		id, ok1 := lineInt(f, 0)
		first, ok2 := lineLong(f, 1)
		if !ok1 || !ok2 {
			continue
		}
		var claimed uint32
		if len(f) > 2 {
			if c, ok := parseUnsigned(f[2], 32); ok {
				claimed = uint32(c)
			}
		}
		a.entries.Set(id, SignEntry{FirstDay: first, Claimed: claimed, FreeTaken: len(f) > 3 && f[3] == "1"})
	}
	return a
}

func (a *ActivitySigns) save() {
	var sb strings.Builder
	for id, e := range a.entries.All() {
		fmt.Fprintf(&sb, "%d %d %d %s\n", id, e.FirstDay, e.Claimed, b01(e.FreeTaken))
	}
	save.WriteAtomic(a.path, sb.String())
}

// SignStates are the state of each of days days on day.
func SignStates(e SignEntry, day int64, days int) []protocol.SignState {
	var list []protocol.SignState
	for i := 0; i < min(days, 32); i++ {
		switch {
		case e.Claimed&(1<<uint(i)) != 0:
			list = append(list, protocol.SignState_SignState_IsReceive)
		case day-e.FirstDay >= int64(i):
			list = append(list, protocol.SignState_SignState_Unlock)
		default:
			list = append(list, protocol.SignState_SignState_Lock)
		}
	}
	return list
}

func SignCanClaim(e SignEntry, day int64, index int32, days int) bool {
	return index >= 0 && int(index) < days && index < 32 && SignStates(e, day, days)[index] == protocol.SignState_SignState_Unlock
}

// EntryOf is the activity's entry; the first call for an activity starts its days today.
func (a *ActivitySigns) EntryOf(activityID int32, day int64) SignEntry {
	if e, ok := a.entries.Get(activityID); ok {
		return e
	}
	e := SignEntry{FirstDay: day}
	a.entries.Set(activityID, e)
	a.save()
	return e
}

// Reset forgets an activity's sign-in days (R-160: a new window).
func (a *ActivitySigns) Reset(activityID int32) {
	if a.entries.Delete(activityID) {
		a.save()
	}
}

func (a *ActivitySigns) MarkClaimed(activityID, index int32) {
	e, ok := a.entries.Get(activityID)
	if !ok {
		return
	}
	e.Claimed |= 1 << uint(index)
	a.entries.Set(activityID, e)
	a.save()
}

func (a *ActivitySigns) MarkFreeTaken(activityID int32) {
	e, ok := a.entries.Get(activityID)
	if !ok {
		return
	}
	e.FreeTaken = true
	a.entries.Set(activityID, e)
	a.save()
}

// UsedToday is a daily counter: its day is FirstDay, its count Claimed; a new day starts at 0.
func (a *ActivitySigns) UsedToday(activityID int32, day int64) uint32 {
	if e, ok := a.entries.Get(activityID); ok && e.FirstDay == day {
		return e.Claimed
	}
	return 0
}

func (a *ActivitySigns) AddUse(activityID int32, day int64) {
	e, ok := a.entries.Get(activityID)
	if !ok || e.FirstDay != day {
		e = SignEntry{FirstDay: day}
	}
	e.Claimed++
	a.entries.Set(activityID, e)
	a.save()
}

// ----- double rewards (R-37) -----

// DoubleActivityType is Activity.Type of double rewards (DoubleInstanceRewardActivity).
const DoubleActivityType = 7

// DoubleUpList are the dungeon CustomTypes a double activity covers (the client's GetDungeonUpList default).
func DoubleUpList(subType int32) []int32 {
	switch subType {
	case 1:
		return []int32{1}
	case 2:
		return []int32{1, 2}
	case 3:
		return []int32{6}
	}
	return nil
}

// ListedDoubles are the listed double activities: the plan's sub types 2 and 3 (R-160; the
// automatic edition is the newest of each).
func ListedDoubles(t *data.DataTables, p *ActivityPlan) [2]*data.DoubleRewardActivity {
	var res [2]*data.DoubleRewardActivity
	for i, sub := range []int32{2, 3} {
		if id, ok := p.Listed(DoubleSlot(sub)); ok {
			res[i] = t.DoubleRewardActivityById[id]
		}
	}
	return res
}

func IsDoubleListed(t *data.DataTables, p *ActivityPlan, id int32) bool {
	for _, r := range ListedDoubles(t, p) {
		if r != nil && r.Id == id {
			return true
		}
	}
	return false
}

func DoubleData(row *data.DoubleRewardActivity, used uint32) *protocol.DoubleInstActivityReward {
	return &protocol.DoubleInstActivityReward{GetDoubleInstRwdCount: int32(min(used, uint32(max(row.Count, 0))))}
}

// ActivityEntry is an activity entry in its automatic window (seconds, autoWindow).
func ActivityEntry(id, typ, openType int32, now int64) *protocol.ActivityData {
	begin, end := autoWindow(typ, openType, now)
	return EntryIn(id, typ, openType, begin, end)
}

// EntryIn is an activity entry in the window [begin, end) (0/0: always open).
func EntryIn(id, typ, openType int32, begin, end int64) *protocol.ActivityData {
	return &protocol.ActivityData{
		Id: id, Type: protocol.ActivityType(typ),
		BeginShowTime: begin, EndShowTime: end, BeginOpenTime: begin, EndOpenTime: end,
		IsUnlock: true, ActivityOpenType: protocol.ActivityOpenType(openType),
		LimitBeginTime: begin, LimitEndTime: end, LimitRewardBeginTime: begin, LimitRewardEndTime: end,
	}
}

// SlotEntry is the entry of a plan slot's edition (nil: no Activity row).
func SlotEntry(t *data.DataTables, slot *ActivitySlot) *protocol.ActivityData {
	cfg, ok := t.ActivityById[slot.Edition]
	if !ok {
		return nil
	}
	return EntryIn(cfg.Id, cfg.Type, cfg.OpenType, slot.Begin, slot.End)
}

// DoubleForDungeon is the listed activity covering a dungeon with these CustomTypes.
func DoubleForDungeon(t *data.DataTables, p *ActivityPlan, customTypes []int32) *data.DoubleRewardActivity {
	for _, r := range ListedDoubles(t, p) {
		if r == nil {
			continue
		}
		for _, c := range DoubleUpList(r.Type) {
			if slices.Contains(customTypes, c) {
				return r
			}
		}
	}
	return nil
}

// ClaimDouble is a claim of a dungeon with these CustomTypes: the multiplier (1 when none
// applies). A doubled claim is counted and the activity's new count is pushed.
func ClaimDouble(s *PlayerSession, customTypes []int32) int32 {
	t := s.Data.Tables
	plan := s.Plan()
	row := DoubleForDungeon(t, plan, customTypes)
	if row == nil {
		return 1
	}
	day := Today(s.Clock.Seconds())
	used := s.Activities().Signs.UsedToday(row.Id, day)
	if int64(used) >= int64(row.Count) || row.UpRate <= 1 {
		return 1
	}
	s.Activities().Signs.AddUse(row.Id, day)
	common.Info("activity_double", fmt.Sprintf("double reward %d: claim %d/%d x%d", row.Id, used+1, row.Count, row.UpRate))
	entry := SlotEntry(t, plan.ListedSlot(row.Id))
	if entry == nil {
		return row.UpRate
	}
	entry.Data = &protocol.ActivityData_DoubleInstActivityReward{DoubleInstActivityReward: DoubleData(row, used+1)}
	s.Sink().Push(&protocol.ActivityUpdateNotify{Activities: []*protocol.ActivityData{entry}})
	return row.UpRate
}

// ----- turntable (R-79) -----

// Turntable is R-79's turntable activity (呜呜企划, Activity.Type 15). A spin costs one coin and
// pays one prize of the current round (TurntableAwards, GroupId = the round); a round's IsSpecial
// prize ends it and opens the next. Saved in turntable: "<activity> <round> <claimed mask> <all done 0|1>".
type Turntable struct {
	path   string
	states omap.Map[int32, TurntableState]
}

const (
	TurntableActivityType = 15
	TurntableQuestCoins   = 1
	TurntableDailyCoins   = 2
)

type TurntableState struct {
	Round   int32
	Claimed uint32
	AllDone bool
}

func NewTurntable(files *save.PlayerFiles) *Turntable {
	t := &Turntable{path: files.PathOf("turntable")}
	for _, f := range readLineFile(t.path) {
		id, ok1 := lineInt(f, 0)
		round, ok2 := lineInt(f, 1)
		if !ok1 || !ok2 {
			continue
		}
		var claimed uint32
		if len(f) > 2 {
			if c, ok := parseUnsigned(f[2], 32); ok {
				claimed = uint32(c)
			}
		}
		t.states.Set(id, TurntableState{Round: round, Claimed: claimed, AllDone: len(f) > 3 && f[3] == "1"})
	}
	return t
}

func TurntableRoundAwards(t *data.DataTables, activityID, round int32) []*data.TurntableAwards {
	var out []*data.TurntableAwards
	for _, r := range t.TurntableAwards {
		if r.ActivityId == activityID && r.GroupId == round {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

func TurntableRounds(t *data.DataTables, activityID int32) []int32 {
	var out []int32
	for _, r := range t.TurntableAwards {
		if r.ActivityId == activityID && !slices.Contains(out, r.GroupId) {
			out = append(out, r.GroupId)
		}
	}
	slices.Sort(out)
	return out
}

func TurntableNextRound(t *data.DataTables, activityID, round int32) *int32 {
	for _, r := range TurntableRounds(t, activityID) {
		if r > round {
			return &r
		}
	}
	return nil
}

func (tt *Turntable) StateOf(t *data.DataTables, activityID int32) TurntableState {
	if s, ok := tt.states.Get(activityID); ok {
		return s
	}
	var first int32
	if r := TurntableRounds(t, activityID); len(r) > 0 {
		first = r[0]
	}
	return TurntableState{Round: first}
}

// TurntableAfterSpin is what one spin does: a special prize ends the round; with no round left
// the activity is finished.
func TurntableAfterSpin(s TurntableState, awards []*data.TurntableAwards, pick int, next *int32) TurntableState {
	if pick >= len(awards) {
		return s
	}
	if !awards[pick].IsSpecial {
		s.Claimed |= 1 << uint(pick)
		return s
	}
	if next != nil {
		return TurntableState{Round: *next}
	}
	return TurntableState{Round: s.Round, Claimed: ^uint32(0), AllDone: true}
}

func TurntableOpenPicks(s TurntableState, awards []*data.TurntableAwards) []int {
	out := []int{}
	for i := 0; i < min(len(awards), 32); i++ {
		if s.Claimed&(1<<uint(i)) == 0 {
			out = append(out, i)
		}
	}
	return out
}

func TurntableClaimedIDs(s TurntableState, awards []*data.TurntableAwards) []int32 {
	out := []int32{}
	for i := 0; i < min(len(awards), 32); i++ {
		if s.Claimed&(1<<uint(i)) != 0 {
			out = append(out, awards[i].Id)
		}
	}
	return out
}

func (tt *Turntable) SetState(activityID int32, s TurntableState) {
	tt.states.Set(activityID, s)
	tt.save()
}

// Reset forgets an activity's turntable (R-160: a new window).
func (tt *Turntable) Reset(activityID int32) {
	if tt.states.Delete(activityID) {
		tt.save()
	}
}

func (tt *Turntable) save() {
	var sb strings.Builder
	for id, st := range tt.states.All() {
		fmt.Fprintf(&sb, "%d %d %d %s\n", id, st.Round, st.Claimed, b01(st.AllDone))
	}
	save.WriteAtomic(tt.path, sb.String())
}

// ----- scratch card (R-79) -----

// ScratchCard is R-79's scratch card activity (踏潮探历, Activity.Type 27). A card is a Size x
// Size grid whose cells hold the round's rewards; one scratch costs one ticket. Which reward
// sits in which cell is dealt the same way every time for one player and round (Layout).
// Saved in scratch_card: "<round> <opened mask>".
type ScratchCard struct {
	path  string
	masks omap.Map[int32, uint64]
}

const (
	ScratchActivityType = 27
	ScratchMaxCells     = 64
	// ScratchOpenCenter is EScratchTicketRewardType Center: one cell is opened.
	ScratchOpenCenter = 0
)

func NewScratchCard(files *save.PlayerFiles) *ScratchCard {
	c := &ScratchCard{path: files.PathOf("scratch_card")}
	for _, f := range readLineFile(c.path) {
		round, ok := lineInt(f, 0)
		if !ok || len(f) <= 1 {
			continue
		}
		if mask, ok := parseUnsigned(f[1], 64); ok {
			c.masks.Set(round, mask)
		}
	}
	return c
}

func ScratchRounds(t *data.DataTables, activityID int32) []*data.ScratchCardRoundRe {
	var out []*data.ScratchCardRoundRe
	for _, r := range t.ScratchCardRoundRe {
		if r.ActivityId == activityID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RoundId < out[j].RoundId })
	return out
}

func ScratchRoundRewards(t *data.DataTables, roundID int32) []*data.ScratchCardRewardRe {
	var out []*data.ScratchCardRewardRe
	for _, r := range t.ScratchCardRewardRe {
		if r.RoundId == roundID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

// ScratchCellCount is how many cells a round has (Size x Size), never more than its rewards.
func ScratchCellCount(row *data.ScratchCardRoundRe, rewards int) int {
	size := int(min(max(row.Size, 0), 8))
	return min(size*size, rewards)
}

func scratchMix(x uint64) uint64 {
	z := x + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// ScratchLayout is which reward (an index) sits in each cell: a shuffle that depends only on
// the player and the round.
func ScratchLayout(playerID, roundID int32, cells int) []int {
	n := min(cells, ScratchMaxCells)
	res := make([]int, max(n, 0))
	for i := range res {
		res[i] = i
	}
	if n < 2 {
		return res
	}
	seed := scratchMix(uint64(int64(playerID)) ^ (scratchMix(uint64(int64(roundID))) << 1))
	for i := n - 1; i > 0; i-- {
		seed = scratchMix(seed)
		j := int(seed % uint64(i+1))
		res[i], res[j] = res[j], res[i]
	}
	return res
}

func (c *ScratchCard) Opened(roundID int32) uint64 { return c.masks.At(roundID) }

func (c *ScratchCard) SetOpened(roundID int32, mask uint64) {
	c.masks.Set(roundID, mask)
	c.save()
}

// Reset forgets the cards of rounds (R-160: a new window).
func (c *ScratchCard) Reset(rounds []*data.ScratchCardRoundRe) {
	changed := false
	for _, r := range rounds {
		changed = c.masks.Delete(r.RoundId) || changed
	}
	if changed {
		c.save()
	}
}

func (c *ScratchCard) save() {
	var sb strings.Builder
	for k, v := range c.masks.All() {
		fmt.Fprintf(&sb, "%d %d\n", k, v)
	}
	save.WriteAtomic(c.path, sb.String())
}

func ScratchIsOpen(mask uint64, cell int) bool {
	return cell >= 0 && cell < 64 && mask&(1<<uint(cell)) != 0
}

func ScratchWithOpen(mask uint64, cell int) uint64 {
	if cell >= 0 && cell < 64 {
		return mask | 1<<uint(cell)
	}
	return mask
}

func ScratchIsFinished(mask uint64, cells int) bool {
	for i := 0; i < cells; i++ {
		if !ScratchIsOpen(mask, i) {
			return false
		}
	}
	return true
}

// ScratchLeftRewards is what is still under the unopened cells, summed per item.
func ScratchLeftRewards(rewards []*data.ScratchCardRewardRe, cellReward []int, mask uint64) map[int32]int32 {
	totals := map[int32]int32{}
	for cell, ri := range cellReward {
		if ScratchIsOpen(mask, cell) || ri >= len(rewards) {
			continue
		}
		item := rewards[ri].RewardItem
		if item == nil || item.Item1 == 0 {
			continue
		}
		totals[item.Item1] += item.Item2
	}
	return totals
}

// ----- activity tasks (R-79) -----

// ActivityTasks is R-79's activity task progress (活动任务). The client only shows what the
// server sends; three kinds: daily login, waveplate (item 5) spent today, Sola guide liveness
// 100 (always met). A finished task pays its TaskReward drop at once. Saved in activity_task:
// "t<turntable task>|s<scratch task> <day> <taken>" and "w <day> <waveplate spent>"; another day
// reads as 0.
type ActivityTasks struct {
	path    string
	order   []string
	entries map[string]taskCount
}

type taskCount struct {
	day   int64
	count int32
}

const (
	ActivityWaveplateItem = 5
	ActivityLiveness      = 100
)

type TaskKind int

const (
	TaskDailyLogin TaskKind = iota
	TaskWaveplateSpent
	TaskGuideLiveness
)

func (k TaskKind) String() string {
	return [...]string{"DailyLogin", "WaveplateSpent", "GuideLiveness"}[k]
}

type TaskOwner int

const (
	TaskOwnerTurntable TaskOwner = iota
	TaskOwnerScratchCard
)

func (o TaskOwner) String() string { return [...]string{"Turntable", "ScratchCard"}[o] }

type TaskDef struct {
	Owner      TaskOwner
	ID         int32
	Kind       TaskKind
	Target     int32
	Limit      int32
	RewardDrop int32
}

func NewActivityTasks(files *save.PlayerFiles) *ActivityTasks {
	a := &ActivityTasks{path: files.PathOf("activity_task"), entries: map[string]taskCount{}}
	for _, f := range readLineFile(a.path) {
		day, ok := lineLong(f, 1)
		if !ok {
			continue
		}
		c, _ := lineInt(f, 2)
		a.put(f[0], taskCount{day, c})
	}
	return a
}

func (a *ActivityTasks) put(key string, e taskCount) {
	if _, ok := a.entries[key]; !ok {
		a.order = append(a.order, key)
	}
	a.entries[key] = e
}

func (a *ActivityTasks) save() {
	var sb strings.Builder
	for _, k := range a.order {
		e := a.entries[k]
		fmt.Fprintf(&sb, "%s %d %d\n", k, e.day, e.count)
	}
	save.WriteAtomic(a.path, sb.String())
}

func (a *ActivityTasks) countToday(key string, day int64) int32 {
	if e, ok := a.entries[key]; ok && e.day == day {
		return e.count
	}
	return 0
}

func taskKey(d TaskDef) string {
	if d.Owner == TaskOwnerTurntable {
		return "t" + itoa(d.ID)
	}
	return "s" + itoa(d.ID)
}

func (a *ActivityTasks) WaveplateToday(day int64) int32 { return a.countToday("w", day) }

// AddWaveplate counts waveplate the player just spent; the tasks are judged when next read.
func (a *ActivityTasks) AddWaveplate(day int64, amount int32) {
	if amount <= 0 {
		return
	}
	a.put("w", taskCount{day, a.countToday("w", day) + amount})
	a.save()
}

func (a *ActivityTasks) TakenToday(d TaskDef, day int64) int32 { return a.countToday(taskKey(d), day) }

func (a *ActivityTasks) markTaken(d TaskDef, day int64, count int32) {
	a.put(taskKey(d), taskCount{day, count})
	a.save()
}

func TaskProgress(d TaskDef, waveplate int32) int32 {
	switch d.Kind {
	case TaskDailyLogin:
		return 1
	case TaskWaveplateSpent:
		return waveplate
	}
	return ActivityLiveness
}

func TaskCompletions(d TaskDef, waveplate int32) int32 {
	if d.Target <= 0 {
		return 0
	}
	return min(TaskProgress(d, waveplate)/d.Target, max(d.Limit, 0))
}

// TurntableTaskDefs are the daily tasks of a Daily-type turntable, by TaskSort.
func TurntableTaskDefs(t *data.DataTables, activityID int32) []TaskDef {
	var rows []*data.TurntableTask
	for _, r := range t.TurntableTask {
		if r.ActivityId == activityID {
			rows = append(rows, r)
			if len(rows) == 16 {
				break
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].TaskSort != rows[j].TaskSort {
			return rows[i].TaskSort < rows[j].TaskSort
		}
		return rows[i].TaskId < rows[j].TaskId
	})
	var defs []TaskDef
	for _, row := range rows {
		var kind TaskKind
		var target int32
		switch {
		case strings.HasSuffix(row.TaskDescription, "_1_Desc"):
			kind, target = TaskDailyLogin, 1
		case strings.HasSuffix(row.TaskDescription, "_2_Desc"):
			kind, target = TaskWaveplateSpent, 120
		case strings.HasSuffix(row.TaskDescription, "_3_Desc"):
			kind, target = TaskGuideLiveness, 100
		default:
			continue
		}
		defs = append(defs, TaskDef{TaskOwnerTurntable, row.TaskId, kind, target, 1, row.TaskReward})
	}
	return defs
}

// ScratchTaskDefs are the daily tasks of a scratch card activity (its TaskIdList, in that order).
func ScratchTaskDefs(t *data.DataTables, activityID int32) []TaskDef {
	var defs []TaskDef
	act, ok := t.ScratchCardActivityReById[activityID]
	if !ok {
		return defs
	}
	for _, id := range act.TaskIdList {
		row, ok := t.ScratchCardTimesReByTaskId[id]
		if len(defs) == 16 || !ok {
			continue
		}
		var kind TaskKind
		switch row.TaskType {
		case 1:
			kind = TaskDailyLogin
		case 2:
			kind = TaskWaveplateSpent
		default:
			continue
		}
		target := int32(1)
		if row.TaskType == 2 {
			target = 40
			if len(row.TaskParams) > 0 && row.TaskParams[0] > 0 {
				target = row.TaskParams[0]
			}
		}
		defs = append(defs, TaskDef{TaskOwnerScratchCard, row.TaskId, kind, target, max(row.RefreshTimesLimit, 1), row.TaskReward})
	}
	return defs
}

func TaskPb(d TaskDef, waveplate, taken int32) *protocol.ActivityTask {
	cur := TaskProgress(d, waveplate)
	due := TaskCompletions(d, waveplate)
	status := protocol.ActivityTaskState_ActivityTaskState_ActivityTaskRunning
	if taken >= d.Limit {
		status = protocol.ActivityTaskState_ActivityTaskState_ActivityTaskTaken
	} else if due > taken {
		status = protocol.ActivityTaskState_ActivityTaskState_ActivityTaskFinish
	}
	return &protocol.ActivityTask{Id: d.ID, Current: min(cur, d.Target*max(d.Limit, 1)), Target: d.Target, Status: status}
}

func TaskTimeInfoPb(d TaskDef, waveplate, taken int32) *protocol.ScratchCardTimeInfo {
	cur := TaskProgress(d, waveplate)
	used := min(taken, max(d.Limit, 0)) * d.Target
	left := cur - used
	if taken >= d.Limit {
		left = d.Target
	}
	return &protocol.ScratchCardTimeInfo{Id: d.ID, Progress: min(max(left, 0), d.Target), FinishCount: taken}
}

// GrantDue pays what d earned today and is not paid yet. Returns how many completions it paid.
func (a *ActivityTasks) GrantDue(s *PlayerSession, d TaskDef, day int64) int32 {
	spent := a.WaveplateToday(day)
	due := TaskCompletions(d, spent)
	taken := a.TakenToday(d, day)
	if due <= taken {
		return 0
	}
	for paid := taken; paid < due; paid++ {
		if d.RewardDrop > 0 {
			s.Actions().GrantDrop(d.RewardDrop, 0, 0, nil)
		}
	}
	a.markTaken(d, day, due)
	common.Info("activity_task", fmt.Sprintf("task %s %d (%s): %d -> %d of %d today, drop %d", d.Owner, d.ID, d.Kind, taken, due, d.Limit, d.RewardDrop))
	if d.Owner == TaskOwnerTurntable {
		s.Sink().Push(&protocol.TurntableTaskUpdateNotify{TurntableTask: TaskPb(d, spent, due)})
	}
	return due - taken
}

// Refresh pays every activity task of the two activities the page shows (at scene join and when
// a page that lists them opens).
func (a *ActivityTasks) Refresh(s *PlayerSession) {
	t := s.Data.Tables
	day := Today(s.Clock.Seconds())
	plan := s.Plan()
	if tid, ok := plan.Listed(SlotTurntable); ok {
		if info, ok := t.TurntableInfoById[tid]; ok && info.TurntableType == TurntableDailyCoins {
			for _, d := range TurntableTaskDefs(t, tid) {
				a.GrantDue(s, d, day)
			}
		}
	}
	if sid, ok := plan.Listed(SlotScratch); ok {
		for _, d := range ScratchTaskDefs(t, sid) {
			a.GrantDue(s, d, day)
		}
	}
}

// PlayerActivities are a player's activity states, loaded per session from their files.
type PlayerActivities struct {
	Signs     *ActivitySigns
	Tasks     *ActivityTasks
	Turntable *Turntable
	Scratch   *ScratchCard
	BossRush  *BossRush
	LordGyms  *LordGyms
	DreamLink *DreamLink
	Windows   *ActivityWindows
}

func NewPlayerActivities(files *save.PlayerFiles) *PlayerActivities {
	return &PlayerActivities{
		Signs:     NewActivitySigns(files),
		Tasks:     NewActivityTasks(files),
		Turntable: NewTurntable(files),
		Scratch:   NewScratchCard(files),
		BossRush:  NewBossRush(files),
		LordGyms:  NewLordGyms(files),
		DreamLink: NewDreamLink(files),
		Windows:   NewActivityWindows(files),
	}
}

// Activities are the player's activity states (loaded at first use in the session).
func (s *PlayerSession) Activities() *PlayerActivities {
	if s.activities == nil {
		s.activities = NewPlayerActivities(s.Files)
	}
	return s.activities
}
