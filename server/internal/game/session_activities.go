// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"math/rand/v2"
	"sort"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/gateway"
	"tidelab/protocol"
)

// The activity page and its requests (specs/activities.md §2–§3). Ported from our C#
// Game/PlayerSession.Activities.cs.

// ExcludedActivities are retired or unsupported activity ids omitted from the listing.
var ExcludedActivities = buildExcludedActivities()

func buildExcludedActivities() map[int32]bool {
	set := map[int32]bool{}
	for _, id := range []int32{100, 100300001, 100400001, 100770001, 100770002, 100800001, 100800002, 100805001, 100805002, 100805003,
		101100001, 101700001, 101700002, 102300013, 102300020, 102400001, 102400002, 102400003, 102900001, 102900002,
		103000001, 103000002, 103200001, 103300001, 103300002, 103300003, 103500001, 103500002, 103500003, 104000001, 104000002,
		104400001, 105100001, 105300002, 105800001, 107100001, 107200001, 107500002, 108600001} {
		set[id] = true
	}
	for i := int32(1); i <= 20; i++ {
		set[100100000+i] = true
	}
	for i := int32(1); i <= 36; i++ {
		set[100700000+i] = true
	}
	return set
}

const (
	SignType            = 2
	TimePointRewardType = 20
	NewLordGymType      = 34
)

func (s *PlayerSession) today() int64 { return Today(s.Clock.Seconds()) }

// GrantReward is R-38: an activity reward: a role the player does not own is granted (with its
// weapon, PbGetRoleListNotify); an owned one turns into its SpilloverItem, count times; other
// items go through the normal-item path.
func (s *PlayerSession) GrantReward(itemID, count int32) {
	if itemID <= 0 || count <= 0 {
		return
	}
	if _, ok := s.Data.Tables.RoleInfoById[itemID]; ok {
		times := int32(1)
		if s.Combat.Player.Roles.Has(itemID) {
			times = count
		}
		for i := int32(0); i < times; i++ {
			s.Combat.Roles.GrantRole(itemID, uint32(s.Clock.Seconds()), s.Sink())
		}
		return
	}
	s.Actions().GrantItem(itemID, count, false)
}

// RefreshActivityTasks is §5.2 step 4c: the daily activity tasks (R-79).
func (s *PlayerSession) RefreshActivityTasks() {
	defer func() {
		if r := recover(); r != nil {
			common.Warn("activity", fmt.Sprintf("activity tasks failed: %v", r))
		}
	}()
	s.Activities().Tasks.Refresh(s)
}

// ActivityPage is ActivityRequest (§3.1): every open slot of the activity plan (R-160: the
// automatic rules with the panel's overrides), newest id first.
func (s *PlayerSession) ActivityPage() *protocol.ActivityResponse {
	if s.Scene != nil {
		s.RefreshActivityTasks()
	}
	t := s.Data.Tables
	plan := s.Plan()
	s.SyncActivityWindows(plan)
	resp := &protocol.ActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, slot := range plan.Slots {
		if !slot.Open {
			continue
		}
		cfg, ok := t.ActivityById[slot.Edition]
		if !ok || cfg.Type != slot.Type {
			continue
		}
		entry := SlotEntry(t, slot)
		switch cfg.Type {
		case DoubleActivityType:
			row, ok := t.DoubleRewardActivityById[cfg.Id]
			if !ok {
				continue
			}
			entry.Data = &protocol.ActivityData_DoubleInstActivityReward{DoubleInstActivityReward: DoubleData(row, s.Activities().Signs.UsedToday(cfg.Id, s.today()))}
		case NewLordGymType:
		default:
			if !s.fillActivity(entry, cfg) {
				continue
			}
		}
		resp.Activities = append(resp.Activities, entry)
	}
	sort.SliceStable(resp.Activities, func(i, j int) bool { return resp.Activities[i].Id > resp.Activities[j].Id })
	return resp
}

// fillActivity is the type's payload (false: none, the next older activity of the type is tried).
func (s *PlayerSession) fillActivity(entry *protocol.ActivityData, cfg *data.Activity) bool {
	t := s.Data.Tables
	a := s.Activities()
	today := s.today()
	switch cfg.Type {
	case SignType:
		sign, ok := t.ActivitySignById[cfg.Id]
		if !ok {
			return false
		}
		e := a.Signs.EntryOf(cfg.Id, today)
		entry.Data = &protocol.ActivityData_SignActivity{SignActivity: &protocol.SignActivity{SignStateList: SignStates(e, today, len(sign.SignRewards)), RewardFree: e.FreeTaken}}
		return true
	case TurntableActivityType:
		info, ok := t.TurntableInfoById[cfg.Id]
		if !ok {
			return false
		}
		st := a.Turntable.StateOf(t, cfg.Id)
		awards := TurntableRoundAwards(t, cfg.Id, st.Round)
		if len(awards) == 0 {
			return false
		}
		d := &protocol.TurntableActivityData{IsAllFinish: st.AllDone, CurRound: st.Round, RewardedItem: TurntableClaimedIDs(st, awards)}
		if info.TurntableType == TurntableDailyCoins {
			spent := a.Tasks.WaveplateToday(today)
			for _, def := range TurntableTaskDefs(t, cfg.Id) {
				d.TurntableTasks = append(d.TurntableTasks, TaskPb(def, spent, a.Tasks.TakenToday(def, today)))
			}
		}
		entry.Data = &protocol.ActivityData_TurntableActivityData{TurntableActivityData: d}
		return true
	case DreamLinkActivityType:
		dream := a.DreamLink.Data(s)
		if dream == nil {
			return false
		}
		entry.Data = &protocol.ActivityData_RogueWhiteCatData{RogueWhiteCatData: dream}
		return true
	case BossRushActivityType:
		entry.Data = &protocol.ActivityData_BossRushActivityData{BossRushActivityData: a.BossRush.Data(t, cfg.Id)}
		return true
	case ScratchActivityType:
		scratch := s.scratchInfo(cfg.Id)
		if scratch == nil {
			return false
		}
		entry.Data = &protocol.ActivityData_ScratchCardActivityInfo{ScratchCardActivityInfo: scratch}
		return true
	case TimePointRewardType:
		var rows []*data.TimePointRewardActivity
		for _, r := range t.TimePointRewardActivity {
			if r.ActivityId == cfg.Id {
				rows = append(rows, r)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Id < rows[j].Id })
		if len(rows) > 32 {
			rows = rows[:32]
		}
		if len(rows) == 0 {
			return false
		}
		se := a.Signs.EntryOf(cfg.Id, today)
		states := SignStates(se, today, len(rows))
		tp := &protocol.TimePointRewardActivityData{}
		for k, row := range rows {
			cur := int32(1)
			if states[k] == protocol.SignState_SignState_Lock {
				cur = 0
			}
			tp.Rewards = append(tp.Rewards, &protocol.OneTimePointRewardActivityData{
				Id:           row.Id,
				RewardTime:   (se.FirstDay + int64(k)) * ScheduleDay,
				Rewarded:     states[k] == protocol.SignState_SignState_IsReceive,
				CanGetReward: states[k] == protocol.SignState_SignState_Unlock,
				// R-37g: the client throws "活动数据异常" without it.
				Task: &protocol.ConditionTask{Id: row.Id, Current: cur, Target: 1},
			})
		}
		entry.Data = &protocol.ActivityData_TimePointRewardActivityData{TimePointRewardActivityData: tp}
		return true
	}
	return false
}

// ----- time-point rewards and sign-ins (§3.2, §3.3) -----

func (s *PlayerSession) claimTimePoint(id int32) protocol.ErrorCode {
	t := s.Data.Tables
	row, ok := t.TimePointRewardActivityById[id]
	if !ok {
		return protocol.ErrorCode_ErrorCode_ErrSignRewardCfg
	}
	var rows []*data.TimePointRewardActivity
	for _, r := range t.TimePointRewardActivity {
		if r.ActivityId == row.ActivityId {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Id < rows[j].Id })
	k := -1
	for i, r := range rows {
		if r.Id == id {
			k = i
			break
		}
	}
	if k < 0 {
		return protocol.ErrorCode_ErrorCode_ErrSignRewardCfg
	}
	e := s.Activities().Signs.EntryOf(row.ActivityId, s.today())
	if !s.activityListed(row.ActivityId) || !SignCanClaim(e, s.today(), int32(k), len(rows)) {
		return protocol.ErrorCode_ErrorCode_ErrActivityLock
	}
	for _, r := range row.Rewards {
		if r != nil {
			s.GrantReward(r.Item1, r.Item2)
		}
	}
	s.Activities().Signs.MarkClaimed(row.ActivityId, int32(k))
	return protocol.ErrorCode_ErrorCode_Success
}

func (s *PlayerSession) TimePointReward(r *protocol.TimePointActivityRewardRequest) *protocol.TimePointActivityRewardResponse {
	return &protocol.TimePointActivityRewardResponse{ErrorCode: s.claimTimePoint(r.Id)}
}

func (s *PlayerSession) TimePointBatchReward(r *protocol.TimePointActivityBatchRewardRequest) *protocol.TimePointActivityBatchRewardResponse {
	var first *protocol.ErrorCode
	anyOK := false
	for _, id := range r.Ids {
		code := s.claimTimePoint(id)
		if code == protocol.ErrorCode_ErrorCode_Success {
			anyOK = true
		} else if first == nil {
			c := code
			first = &c
		}
	}
	code := protocol.ErrorCode_ErrorCode_Success
	if !anyOK && first != nil {
		code = *first
	}
	return &protocol.TimePointActivityBatchRewardResponse{ErrorCode: code}
}

func (s *PlayerSession) Sign(r *protocol.SignActivityRequest) *protocol.SignActivityResponse {
	sign, ok := s.Data.Tables.ActivitySignById[r.ActivityId]
	if !ok || r.Index < 0 {
		return &protocol.SignActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSignRewardCfg}
	}
	e := s.Activities().Signs.EntryOf(r.ActivityId, s.today())
	if !s.activityListed(r.ActivityId) || !SignCanClaim(e, s.today(), r.Index, len(sign.SignRewards)) {
		return &protocol.SignActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrActivityLock}
	}
	resp := &protocol.SignActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ItemMap: map[int32]int32{}}
	if reward := sign.SignRewards[r.Index]; reward != nil && reward.ItemId > 0 && reward.Count > 0 {
		s.GrantReward(reward.ItemId, reward.Count)
		resp.ItemMap[reward.ItemId] = reward.Count
	}
	s.Activities().Signs.MarkClaimed(r.ActivityId, r.Index)
	return resp
}

func (s *PlayerSession) SignFreeDrop(r *protocol.SignActivityFreeDropRequest) *protocol.SignActivityFreeDropResponse {
	sign, ok := s.Data.Tables.ActivitySignById[r.ActivityId]
	if !ok {
		return &protocol.SignActivityFreeDropResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSignRewardCfg}
	}
	e := s.Activities().Signs.EntryOf(r.ActivityId, s.today())
	if !s.activityListed(r.ActivityId) || e.FreeTaken || sign.FreeDropId <= 0 {
		return &protocol.SignActivityFreeDropResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrActivityLock}
	}
	s.Actions().GrantDrop(sign.FreeDropId, 0, 1, nil)
	s.Activities().Signs.MarkFreeTaken(r.ActivityId)
	return &protocol.SignActivityFreeDropResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- scratch card and turntable (§3.4, §3.5) -----

// scratchInfo is the shared ScratchCardActivityInfo builder; nil without rounds.
func (s *PlayerSession) scratchInfo(activityID int32) *protocol.ScratchCardActivityInfo {
	t := s.Data.Tables
	rounds := ScratchRounds(t, activityID)
	if len(rounds) == 0 {
		return nil
	}
	a := s.Activities()
	info := &protocol.ScratchCardActivityInfo{}
	for _, round := range rounds {
		rewards := ScratchRoundRewards(t, round.RoundId)
		cells := ScratchCellCount(round, len(rewards))
		layout := ScratchLayout(s.State.PlayerID(), round.RoundId, cells)
		opened := a.Scratch.Opened(round.RoundId)
		ri := &protocol.ScratchCardRoundInfo{RoundId: round.RoundId, UnlockTime: 0, RewardIds: map[int32]*protocol.ScratchCardRewardData{}, LeftRewardItem: map[int32]int32{}}
		for cell := 0; cell < cells; cell++ {
			if ScratchIsOpen(opened, cell) {
				if item := rewards[layout[cell]].RewardItem; item != nil {
					ri.RewardIds[int32(cell)] = &protocol.ScratchCardRewardData{ItemId: item.Item1, Num: item.Item2}
				}
			}
		}
		for k, v := range ScratchLeftRewards(rewards, layout, opened) {
			ri.LeftRewardItem[k] = v
		}
		info.RoundInfos = append(info.RoundInfos, ri)
	}
	spent := a.Tasks.WaveplateToday(s.today())
	for _, def := range ScratchTaskDefs(t, activityID) {
		info.ScratchTimeInfos = append(info.ScratchTimeInfos, TaskTimeInfoPb(def, spent, a.Tasks.TakenToday(def, s.today())))
	}
	return info
}

func (s *PlayerSession) ScratchCardInfo(r *protocol.ScratchCardActivityInfoRequest) *protocol.ScratchCardActivityInfoResponse {
	id := r.ActivityId
	if id == 0 {
		id, _ = s.Plan().Listed(SlotScratch)
	}
	s.RefreshActivityTasks()
	if info := s.scratchInfo(id); info != nil {
		return &protocol.ScratchCardActivityInfoResponse{Error: protocol.ErrorCode_ErrorCode_Success, Info: info}
	}
	return &protocol.ScratchCardActivityInfoResponse{Error: protocol.ErrorCode_ErrorCode_ScratchCardNoActivityConfig}
}

func (s *PlayerSession) ScratchCardReward(r *protocol.ScratchCardRewardRequest) *protocol.ScratchCardRewardResponse {
	t := s.Data.Tables
	fail := func(c protocol.ErrorCode) *protocol.ScratchCardRewardResponse {
		return &protocol.ScratchCardRewardResponse{Error: c}
	}
	round, ok := t.ScratchCardRoundReByRoundId[r.RoundId]
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ScratchCardNoRoundConfig)
	}
	act, ok := t.ScratchCardActivityReById[round.ActivityId]
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ScratchCardNoActivityConfig)
	}
	if !s.activityListed(round.ActivityId) {
		return fail(errActivityLock)
	}
	rewards := ScratchRoundRewards(t, round.RoundId)
	cells := ScratchCellCount(round, len(rewards))
	if r.Index < 0 || int(r.Index) >= cells {
		return fail(protocol.ErrorCode_ErrorCode_ScratchCardIllegalIndex)
	}
	a := s.Activities()
	if round.PreRoundId != 0 {
		if pre, ok := t.ScratchCardRoundReByRoundId[round.PreRoundId]; ok {
			preCells := ScratchCellCount(pre, len(ScratchRoundRewards(t, pre.RoundId)))
			if !ScratchIsFinished(a.Scratch.Opened(pre.RoundId), preCells) {
				return fail(protocol.ErrorCode_ErrorCode_ScratchCardRoundNoUnlock)
			}
		}
	}
	opened := a.Scratch.Opened(round.RoundId)
	if ScratchIsOpen(opened, int(r.Index)) {
		return fail(protocol.ErrorCode_ErrorCode_ScratchCardIndexRewarded)
	}
	if s.ItemCount(act.ItemId) < 1 {
		return fail(protocol.ErrorCode_ErrorCode_ScratchCardNoTime)
	}
	one := int32(1)
	s.Actions().TakeItem(act.ItemId, &one)
	layout := ScratchLayout(s.State.PlayerID(), round.RoundId, cells)
	item := rewards[layout[r.Index]].RewardItem
	resp := &protocol.ScratchCardRewardResponse{
		Error: protocol.ErrorCode_ErrorCode_Success, OpenType: ScratchOpenCenter,
		ItemMap: map[int32]int32{}, OpenIndex: map[int32]*protocol.ScratchCardRewardData{}, LeftRewardItem: map[int32]int32{},
	}
	if item != nil && item.Item1 > 0 && item.Item2 > 0 {
		s.Actions().GrantItem(item.Item1, item.Item2, false)
		resp.ItemMap[item.Item1] = item.Item2
	}
	open := &protocol.ScratchCardRewardData{}
	if item != nil {
		open.ItemId, open.Num = item.Item1, item.Item2
	}
	resp.OpenIndex[r.Index] = open
	opened = ScratchWithOpen(opened, int(r.Index))
	a.Scratch.SetOpened(round.RoundId, opened)
	for k, v := range ScratchLeftRewards(rewards, layout, opened) {
		resp.LeftRewardItem[k] = v
	}
	return resp
}

func (s *PlayerSession) RunTurntable(r *protocol.RunTurntableActivityRequest) *protocol.RunTurntableActivityResponse {
	t := s.Data.Tables
	fail := func(c protocol.ErrorCode) *protocol.RunTurntableActivityResponse {
		return &protocol.RunTurntableActivityResponse{ErrorCode: c}
	}
	listed, hasListed := s.Plan().Listed(SlotTurntable)
	id := r.ActivityId
	if id == 0 {
		id = listed
	}
	info, ok := t.TurntableInfoById[id]
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ErrTurntableConfigNotFound)
	}
	if !hasListed || id != listed {
		return fail(protocol.ErrorCode_ErrorCode_ErrTurntableActivityNotOpen)
	}
	s.RefreshActivityTasks()
	a := s.Activities()
	st := a.Turntable.StateOf(t, id)
	if st.AllDone {
		return fail(protocol.ErrorCode_ErrorCode_ErrTurntableActivityIsFinish)
	}
	awards := TurntableRoundAwards(t, id, st.Round)
	if len(awards) == 0 {
		return fail(protocol.ErrorCode_ErrorCode_ErrTurntableActivityRoundConfigNotFound)
	}
	picks := TurntableOpenPicks(st, awards)
	if len(picks) == 0 {
		return fail(protocol.ErrorCode_ErrorCode_ErrTurntableActivityIsFinish)
	}
	cost := max(info.CostItemCount, 1)
	if s.ItemCount(info.CostItemId) < cost {
		return fail(protocol.ErrorCode_ErrorCode_ErrItemNotEnough)
	}
	s.Actions().TakeItem(info.CostItemId, &cost)
	pick := picks[rand.IntN(len(picks))]
	award := awards[pick]
	resp := &protocol.RunTurntableActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, RewardId: award.Id, IsFinishCurRound: award.IsSpecial, ItemMap: map[int32]int32{}}
	if award.RewardItem != nil {
		for _, p := range award.RewardItem.Pairs {
			s.GrantReward(p.Key, p.Value)
			resp.ItemMap[p.Key] = p.Value
		}
	}
	a.Turntable.SetState(id, TurntableAfterSpin(st, awards, pick, TurntableNextRound(t, id, st.Round)))
	special := ""
	if award.IsSpecial {
		special = " (special)"
	}
	common.Info("turntable", fmt.Sprintf("turntable %d round %d: prize %d%s", id, st.Round, award.Id, special))
	return resp
}

// DreamLinkRoleInst is 深梦历程: the role dungeon of row Index with the player's team; the
// reply, then the transfer.
func (s *PlayerSession) DreamLinkRoleInst(r *protocol.RoguelikeRoleInstStartRequest) *protocol.RoguelikeRoleInstStartResponse {
	inst, ok := s.Activities().DreamLink.RoleDungeon(s, r.Index)
	if !ok {
		return &protocol.RoguelikeRoleInstStartResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrActivityLock}
	}
	if q := s.QuestState; q == nil || !q.Dungeons.Enter(s, inst, 0) {
		common.Warn("dream_link", fmt.Sprintf("entering role dungeon %d failed", inst))
		return &protocol.RoguelikeRoleInstStartResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound}
	}
	s.After(func() { s.ApplyPendingTransfer() })
	return &protocol.RoguelikeRoleInstStartResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// DreamLinkClaimAll is the "claim all" button of the energy rewards: answers the ones it gave.
func (s *PlayerSession) DreamLinkClaimAll(r *protocol.MulRoguelikeWriteCatRewardRequest) *protocol.MulRoguelikeWriteCatRewardResponse {
	var given []int32
	for _, id := range r.Index {
		if s.Activities().DreamLink.Claim(s, DreamEnergy, id) == protocol.ErrorCode_ErrorCode_Success {
			given = append(given, id)
		}
	}
	code := protocol.ErrorCode_ErrorCode_ErrActivityLock
	if len(given) > 0 {
		code = protocol.ErrorCode_ErrorCode_Success
	}
	return &protocol.MulRoguelikeWriteCatRewardResponse{ErrorCode: code, Index: given}
}

// registerActivities registers the activity requests.
func (g *WorldGame) registerActivities(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	lock := protocol.ErrorCode_ErrorCode_ErrActivityLock
	gateway.OnRequest(h, func(ss *gateway.Session, _ *protocol.ActivityRequest) proto.Message {
		return on(g, ss, (*PlayerSession).ActivityPage, &protocol.ActivityResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.TimePointActivityRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.TimePointActivityRewardResponse { return p.TimePointReward(r) },
			&protocol.TimePointActivityRewardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSignRewardCfg})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.TimePointActivityBatchRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.TimePointActivityBatchRewardResponse {
			return p.TimePointBatchReward(r)
		},
			&protocol.TimePointActivityBatchRewardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSignRewardCfg})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.SignActivityRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.SignActivityResponse { return p.Sign(r) },
			&protocol.SignActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSignRewardCfg})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.SignActivityFreeDropRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.SignActivityFreeDropResponse { return p.SignFreeDrop(r) },
			&protocol.SignActivityFreeDropResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSignRewardCfg})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.ScratchCardActivityInfoRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.ScratchCardActivityInfoResponse { return p.ScratchCardInfo(r) },
			&protocol.ScratchCardActivityInfoResponse{Error: protocol.ErrorCode_ErrorCode_ScratchCardNoActivityConfig})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.ScratchCardRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.ScratchCardRewardResponse { return p.ScratchCardReward(r) },
			&protocol.ScratchCardRewardResponse{Error: protocol.ErrorCode_ErrorCode_ScratchCardNoActivityConfig})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.RunTurntableActivityRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.RunTurntableActivityResponse { return p.RunTurntable(r) },
			&protocol.RunTurntableActivityResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrTurntableConfigNotFound})
	})
	// Boss 连战 (§4.1): buffs, settlement, batch and level-score claims are placeholders.
	gateway.OnRequest(h, func(*gateway.Session, *protocol.BossRushChooseBuffRequest) proto.Message {
		return &protocol.BossRushChooseBuffResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.BossRushBuffChooseResultRequest) proto.Message {
		return &protocol.BossRushBuffChooseResultResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.BossRushSettlementRequest) proto.Message {
		return &protocol.BossRushSettlementResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.BossRushClaimRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.BossRushClaimRewardResponse {
			return &protocol.BossRushClaimRewardResponse{ErrorCode: p.Activities().BossRush.Claim(p, r.RewardId, r.RewardType)}
		}, &protocol.BossRushClaimRewardResponse{ErrorCode: lock})
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.BossRushBatchClaimRewardRequest) proto.Message {
		return &protocol.BossRushBatchClaimRewardResponse{ErrorCode: lock}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.BossRushLevelScoreRewardRequest) proto.Message {
		return &protocol.BossRushLevelScoreRewardResponse{Error: lock}
	})
	// 领主挑战 (§4.2).
	gateway.OnRequest(h, func(ss *gateway.Session, _ *protocol.LordGymInfoRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.LordGymInfoResponse {
			begin, end := p.lordGymWindow()
			return p.Activities().LordGyms.InfoIn(p.Data.Tables, begin, end)
		}, &protocol.LordGymInfoResponse{})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.LordGymBeginRequest) proto.Message {
		notFound := protocol.ErrorCode_ErrorCode_ErrLordGymConfigNotFound
		return on(g, ss, func(p *PlayerSession) *protocol.LordGymBeginResponse {
			if p.Activities().LordGyms.Begin(p, r.LoadGymId) {
				return &protocol.LordGymBeginResponse{ErrorCode: ok}
			}
			return &protocol.LordGymBeginResponse{ErrorCode: notFound}
		}, &protocol.LordGymBeginResponse{ErrorCode: notFound})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.LordGymReadRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.LordGymReadResponse {
			p.Activities().LordGyms.MarkRead(r.LoadGymId)
			return &protocol.LordGymReadResponse{ErrorCode: ok}
		}, &protocol.LordGymReadResponse{ErrorCode: ok})
	})
	// 失序梦途 (R-53).
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.RoguelikeRoleInstStartRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.RoguelikeRoleInstStartResponse { return p.DreamLinkRoleInst(r) },
			&protocol.RoguelikeRoleInstStartResponse{ErrorCode: lock})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.RoguelikeWriteCatRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.RoguelikeWriteCatRewardResponse {
			return &protocol.RoguelikeWriteCatRewardResponse{ErrorCode: p.Activities().DreamLink.Claim(p, DreamEnergy, r.Index)}
		}, &protocol.RoguelikeWriteCatRewardResponse{ErrorCode: lock})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.MulRoguelikeWriteCatRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.MulRoguelikeWriteCatRewardResponse { return p.DreamLinkClaimAll(r) },
			&protocol.MulRoguelikeWriteCatRewardResponse{ErrorCode: lock})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.RoguelikeWriteCatBossRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.RoguelikeWriteCatBossRewardResponse {
			return &protocol.RoguelikeWriteCatBossRewardResponse{ErrorCode: p.Activities().DreamLink.Claim(p, DreamBoss, r.Index)}
		}, &protocol.RoguelikeWriteCatBossRewardResponse{ErrorCode: lock})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.RoguelikeWriteCatLevelPlayRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.RoguelikeWriteCatLevelPlayRewardResponse {
			return &protocol.RoguelikeWriteCatLevelPlayRewardResponse{ErrorCode: p.Activities().DreamLink.Claim(p, DreamRun, r.Index)}
		}, &protocol.RoguelikeWriteCatLevelPlayRewardResponse{ErrorCode: lock})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.RogueWhiteCatLimitedRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.RogueWhiteCatLimitedRewardResponse {
			return &protocol.RogueWhiteCatLimitedRewardResponse{ErrorCode: p.Activities().DreamLink.Claim(p, DreamLimited, r.Index)}
		}, &protocol.RogueWhiteCatLimitedRewardResponse{ErrorCode: lock})
	})
	// The role dungeon's progress bar: accepted, not kept.
	gateway.OnRequest(h, func(*gateway.Session, *protocol.RogueWhiteCatRecordRequest) proto.Message {
		return &protocol.RogueWhiteCatRecordResponse{ErrorCode: ok}
	})
	// Fishing (§4.3, R-115).
	gateway.OnRequest(h, func(*gateway.Session, *protocol.FishingInfoRequest) proto.Message {
		return &protocol.FishingInfoResponse{FishingInfo: FishingInfo()}
	})
}
