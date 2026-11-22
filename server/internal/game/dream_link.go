// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// DreamLink is R-53: 失序梦途 (activity type 26; the client calls it DreamLink, the protocol
// RogueWhiteCat). Its parts: 深梦历程 (role dungeons, RogueWhiteCatInst), 异梦封锁 (world runs,
// DreamLinkWorldRun), 永夜重现 (boss levels, RogueBossInstance), energy rewards and
// limited-time rewards; the guide quest opens them step by step (the condition groups). The
// server keeps what was cleared and claimed. Not modelled: trial roles, boss scores, the 联梦融觉
// counters and how a world run reports its end (NoteRunDone has no caller).
// Saved in dream_link: "P <inst>" cleared, "E|B|R|L <id>" claimed energy / boss / run / limited
// rewards, "D <run>" run done. Ported from our C# Game/DreamLink.cs.
type DreamLink struct {
	path string
	sets map[byte]*omap.Map[int32, struct{}]
}

const (
	DreamLinkActivityType = 26
	dreamLinkKinds        = "PEBRLD"
)

type DreamLinkKind int

const (
	DreamEnergy DreamLinkKind = iota
	DreamBoss
	DreamRun
	DreamLimited
)

func (k DreamLinkKind) String() string { return [...]string{"Energy", "Boss", "Run", "Limited"}[k] }

func NewDreamLink(files *save.PlayerFiles) *DreamLink {
	d := &DreamLink{path: files.PathOf("dream_link"), sets: map[byte]*omap.Map[int32, struct{}]{}}
	for i := 0; i < len(dreamLinkKinds); i++ {
		d.sets[dreamLinkKinds[i]] = omap.New[int32, struct{}]()
	}
	for _, f := range readLineFile(d.path) {
		if len(f[0]) != 1 {
			continue
		}
		set, ok := d.sets[f[0][0]]
		if !ok {
			continue
		}
		if id, ok := lineInt(f, 1); ok {
			set.Set(id, struct{}{})
		}
	}
	return d
}

func (d *DreamLink) passed() *omap.Map[int32, struct{}] { return d.sets['P'] }

func (d *DreamLink) IsPassed(inst int32) bool { return d.passed().Has(inst) }

// Reset forgets every claim and pass (R-160: a new window).
func (d *DreamLink) Reset() {
	for _, set := range d.sets {
		set.Clear()
	}
	d.save()
}

func (d *DreamLink) save() {
	var sb strings.Builder
	for i := 0; i < len(dreamLinkKinds); i++ {
		k := dreamLinkKinds[i]
		for id := range d.sets[k].All() {
			fmt.Fprintf(&sb, "%c %d\n", k, id)
		}
	}
	save.WriteAtomic(d.path, sb.String())
}

// add puts id in a set; false when it was there.
func addTo(set *omap.Map[int32, struct{}], id int32) bool {
	if set.Has(id) {
		return false
	}
	set.Set(id, struct{}{})
	return true
}

// dreamLinkConfig is the listed activity's config (nil: none listed; R-160, automatic: the
// newest RogueWhiteCat row), with its plan slot.
func dreamLinkConfig(s *PlayerSession) (*data.RogueWhiteCat, *ActivitySlot) {
	p := s.Plan()
	id, ok := p.Listed(SlotDreamLink)
	if !ok {
		return nil, nil
	}
	return s.Data.Tables.RogueWhiteCatById[id], p.Slot(SlotDreamLink)
}

func conditionParam(c *data.Condition, key string) (int64, bool) {
	if c.LimitParams == nil {
		return 0, false
	}
	v, ok := c.LimitParams.Get(key)
	if !ok {
		return 0, false
	}
	return parseSigned(v, 64)
}

// met is one condition: quest progress and cleared dungeons are judged; the rest count as met offline.
func (d *DreamLink) met(s *PlayerSession, c *data.Condition) bool {
	switch c.Type {
	case "NewQuestStepState", "NewQuestState":
		quest, ok := conditionParam(c, "NewQuestId")
		if !ok {
			return true
		}
		e := s.Quests()
		if e != nil && e.IsFinished(int32(quest)) {
			return true
		}
		node, ok := conditionParam(c, "QuestNodeId")
		return ok && e != nil && e.IsNodeDone(int32(quest), int32(node))
	case "PassInst", "CheckDungeonFinished":
		inst, ok := conditionParam(c, "InstanceId")
		return !ok || d.passed().Has(int32(inst))
	}
	return true
}

func (d *DreamLink) groupOK(s *PlayerSession, groupID int32) bool {
	if groupID == 0 {
		return true
	}
	g, ok := s.Data.Tables.ConditionGroupById[groupID]
	if !ok {
		return true
	}
	n := 0
	for _, cid := range g.GroupId {
		c, ok := s.Data.Tables.ConditionById[cid]
		if !ok || d.met(s, c) {
			n++
		}
	}
	if g.Relation == 1 {
		return n > 0
	}
	return n == len(g.GroupId)
}

func dreamClaimState(claimed, ready bool) protocol.SignState {
	switch {
	case claimed:
		return protocol.SignState_SignState_IsReceive
	case ready:
		return protocol.SignState_SignState_Unlock
	}
	return protocol.SignState_SignState_Lock
}

func doneProgress(b bool) (int32, int32) {
	if b {
		return 1, 1
	}
	return 0, 1
}

// limitedProgress is a limited-time reward's progress by its id: 1xx story / energy, 2xx
// 深梦历程, 3xx 异梦封锁, others not tracked.
func (d *DreamLink) limitedProgress(s *PlayerSession, cfg *data.RogueWhiteCat, id, energy int32) (int32, int32) {
	k := id % 100
	switch id / 100 {
	case 1:
		switch k {
		case 1:
			return doneProgress(d.passed().Has(cfg.FirstWhiteCatDungeonId))
		case 2:
			e := s.Quests()
			return doneProgress(e != nil && e.IsFinished(cfg.FirstWhiteCatQuestId))
		case 3, 4, 5:
			target := 400 * (k - 2)
			return min(energy, target), target
		}
	case 2:
		if k >= 1 && int(k) <= len(cfg.Insts) {
			row, ok := s.Data.Tables.RogueWhiteCatInstById[cfg.Insts[k-1]]
			return doneProgress(ok && d.passed().Has(row.InstId))
		}
	case 3:
		return doneProgress(d.sets['D'].Has(k))
	}
	return doneProgress(false)
}

// bossRewardReady: 永夜重现 rewards: 1xx = clearing level x, 2xx = its score reward (a clear counts).
func (d *DreamLink) bossRewardReady(t *data.DataTables, cfg *data.RogueWhiteCat, rewardID int32) bool {
	k := rewardID % 100
	if k < 1 || int(k) > len(cfg.BossInstanceList) {
		return false
	}
	r, ok := t.RogueBossInstanceById[cfg.BossInstanceList[k-1]]
	return ok && d.passed().Has(r.InstanceId)
}

func dreamEnergyOf(s *PlayerSession, cfg *data.RogueWhiteCat) int32 {
	return s.ItemCount(cfg.EnergyId)
}

// Data is the activity page's data, nil without a config.
func (d *DreamLink) Data(s *PlayerSession) *protocol.RogueWhiteCatData {
	t := s.Data.Tables
	cfg, slot := dreamLinkConfig(s)
	if cfg == nil {
		return nil
	}
	energy := dreamEnergyOf(s, cfg)
	out := &protocol.RogueWhiteCatData{MaxEnergy: cfg.MaxEnergy}
	for _, id := range cfg.Rewards {
		if r, ok := t.RogueWhiteCatRewardById[id]; ok {
			out.SignStateList = append(out.SignStateList, dreamClaimState(d.sets['E'].Has(id), energy >= r.NeedEnergy))
		}
	}
	for _, id := range cfg.Insts {
		if r, ok := t.RogueWhiteCatInstById[id]; ok {
			out.RogueRoleInstData = append(out.RogueRoleInstData, &protocol.RogueRoleInstData{InstId: r.InstId, IsUnlock: d.groupOK(s, r.ConditionGroupId), IsFinish: d.passed().Has(r.InstId)})
		}
	}
	for _, r := range t.DreamLinkWorldRun {
		if r.ActivityId == cfg.Id {
			out.RogueLevelPlayData = append(out.RogueLevelPlayData, &protocol.RogueLevelPlayData{Index: r.Id, Reward: dreamClaimState(d.sets['R'].Has(r.Id), d.sets['D'].Has(r.Id)), IsUnlock: d.groupOK(s, r.ConditionGroupId)})
		}
	}
	for _, id := range cfg.BossRewards {
		out.BossRewardStateList = append(out.BossRewardStateList, dreamClaimState(d.sets['B'].Has(id), d.bossRewardReady(t, cfg, id)))
	}
	for _, r := range t.RogueLimitTimeReward {
		if r.ActivityId != cfg.Id {
			continue
		}
		cur, target := d.limitedProgress(s, cfg, r.Id, energy)
		out.RogueLimitedAwards = append(out.RogueLimitedAwards, &protocol.RogueLimitedAward{SignState: dreamClaimState(d.sets['L'].Has(r.Id), cur >= target), CurProgress: cur, MaxProgress: target, ConfigId: r.Id})
	}
	out.LimitedStartTime, out.LimitedEndTime = slot.Begin, slot.End
	for _, id := range cfg.BossInstanceList {
		if r, ok := t.RogueBossInstanceById[id]; ok {
			out.RogueBossInstDatas = append(out.RogueBossInstDatas, &protocol.RogueBossInstData{InstId: r.InstanceId, IsUnlock: d.groupOK(s, r.ConditionGroupId), IsFinish: d.passed().Has(r.InstanceId)})
		}
	}
	if cfg.OpenCondition != nil {
		for _, p := range cfg.OpenCondition.Pairs {
			if d.groupOK(s, p.Value) {
				out.UnlockButtons = append(out.UnlockButtons, p.Key)
			}
		}
	}
	return out
}

// pushUpdate pushes the page again (ActivityUpdateNotify) after something changed.
func (d *DreamLink) pushUpdate(s *PlayerSession) {
	cfg, slot := dreamLinkConfig(s)
	if cfg == nil {
		return
	}
	page := d.Data(s)
	entry := SlotEntry(s.Data.Tables, slot)
	if page == nil || entry == nil {
		return
	}
	entry.Data = &protocol.ActivityData_RogueWhiteCatData{RogueWhiteCatData: page}
	s.Sink().Push(&protocol.ActivityUpdateNotify{Activities: []*protocol.ActivityData{entry}})
}

func (d *DreamLink) config(s *PlayerSession) *data.RogueWhiteCat {
	cfg, _ := dreamLinkConfig(s)
	return cfg
}

func isDreamLinkDungeon(t *data.DataTables, cfg *data.RogueWhiteCat, inst int32) bool {
	if cfg == nil {
		return false
	}
	if inst == cfg.FirstWhiteCatDungeonId {
		return true
	}
	for _, r := range t.RogueWhiteCatInst {
		if r.InstId == inst {
			return true
		}
	}
	for _, r := range t.RogueBossInstance {
		if r.InstanceId == inst {
			return true
		}
	}
	return false
}

// OnFinished: a dungeon finished: a successful clear of one of the activity's dungeons is kept.
func (d *DreamLink) OnFinished(s *PlayerSession, inst int32, ok bool) {
	if !ok || !isDreamLinkDungeon(s.Data.Tables, d.config(s), inst) || !addTo(d.passed(), inst) {
		return
	}
	d.save()
	common.Info("dream_link", fmt.Sprintf("失序梦途 dungeon %d cleared", inst))
	d.pushUpdate(s)
}

// NoteRunDone: a world run (异梦封锁) was completed.
func (d *DreamLink) NoteRunDone(s *PlayerSession, runID int32) {
	if !addTo(d.sets['D'], runID) {
		return
	}
	d.save()
	d.pushUpdate(s)
}

// RoleDungeon is RoguelikeRoleInstStartRequest: the role dungeon of row index, or false when it is not open.
func (d *DreamLink) RoleDungeon(s *PlayerSession, index int32) (int32, bool) {
	r, ok := s.Data.Tables.RogueWhiteCatInstById[index]
	if !ok || !d.groupOK(s, r.ConditionGroupId) {
		return 0, false
	}
	return r.InstId, true
}

// Claim is a claim; returns the answer.
func (d *DreamLink) Claim(s *PlayerSession, kind DreamLinkKind, id int32) protocol.ErrorCode {
	t := s.Data.Tables
	lock := protocol.ErrorCode_ErrorCode_ErrActivityLock
	cfg, _ := dreamLinkConfig(s)
	if cfg == nil {
		return lock
	}
	energy := dreamEnergyOf(s, cfg)
	var set *omap.Map[int32, struct{}]
	var ready bool
	var drop int32
	switch kind {
	case DreamEnergy:
		er, ok := t.RogueWhiteCatRewardById[id]
		if !ok {
			return lock
		}
		set, ready, drop = d.sets['E'], energy >= er.NeedEnergy, er.DropId
	case DreamBoss:
		br, ok := t.RogueWhiteCatBossRewardById[id]
		if !ok {
			return lock
		}
		set, ready, drop = d.sets['B'], d.bossRewardReady(t, cfg, id), br.DropId
	case DreamRun:
		rr, ok := t.DreamLinkWorldRunById[id]
		if !ok {
			return lock
		}
		set, ready, drop = d.sets['R'], d.sets['D'].Has(id), rr.RewardId
	default:
		lr, ok := t.RogueLimitTimeRewardById[id]
		if !ok {
			return lock
		}
		cur, target := d.limitedProgress(s, cfg, id, energy)
		set, ready, drop = d.sets['L'], cur >= target, lr.TargetReward
	}
	if set.Has(id) || !ready {
		return lock
	}
	if drop > 0 {
		s.Actions().GrantDrop(drop, 0, 0, nil)
	}
	set.Set(id, struct{}{})
	d.save()
	common.Info("dream_link", fmt.Sprintf("失序梦途 %s reward %d claimed", kind, id))
	return protocol.ErrorCode_ErrorCode_Success
}
