// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/protocol"
)

// R-37, R-38, R-52, R-53, R-79, R-115: activities (specs/activities.md). Ported from our C#
// ActivityTests, ChallengeTests and DreamLinkTests.

func pairs(kv ...int32) *data.Map[int32, int32] {
	m := &data.Map[int32, int32]{}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Pairs = append(m.Pairs, data.Pair[int32, int32]{Key: kv[i], Value: kv[i+1]})
	}
	return m
}

func rigFiles(r *rig) *PlayerSession { return r.Game().NewSession(&recording{id: rigPlayer}) }

func TestSignDaysUnlockOneADayAndAreClaimedOnce(t *testing.T) {
	U, L, R := protocol.SignState_SignState_Unlock, protocol.SignState_SignState_Lock, protocol.SignState_SignState_IsReceive
	fresh := SignEntry{FirstDay: 100}
	eq(t, SignStates(fresh, 100, 7), []protocol.SignState{U, L, L, L, L, L, L})
	eq(t, SignStates(fresh, 102, 7), []protocol.SignState{U, U, U, L, L, L, L})
	if !SignCanClaim(fresh, 100, 0, 7) || SignCanClaim(fresh, 100, 1, 7) || SignCanClaim(fresh, 100, 7, 7) {
		t.Fatal("claims")
	}
	some := SignEntry{FirstDay: 100, Claimed: 0b101}
	eq(t, SignStates(some, 103, 7), []protocol.SignState{R, U, R, U, L, L, L})
	if SignCanClaim(some, 103, 0, 7) || !SignCanClaim(some, 103, 1, 7) || SignStates(fresh, 999, 7)[6] != U {
		t.Fatal("claims 2")
	}
}

func TestSavedLinesOfEveryActivityModuleLoad(t *testing.T) {
	r := newRig(t)
	r.save("activity_sign", "100200001 20000 5 1\r\n100100021 20001 0 0\n\nbad line\n")
	r.save("activity_task", "t13 20418 1\r\ns10014 20418 3\nw 20418 125\n\nbad\n")
	r.save("turntable", "101500009 1 5 0\r\n101500008 2 4294967295 1\n\nbad\n")
	r.save("scratch_card", "701 7\r\n702 0\n\nbad\n")
	a := NewPlayerActivities(rigFiles(r).Files)
	if e := a.Signs.EntryOf(100200001, 30000); e != (SignEntry{20000, 5, true}) || a.Signs.EntryOf(100100021, 30000).FreeTaken {
		t.Fatal(e)
	}
	if a.Tasks.WaveplateToday(20418) != 125 || a.Tasks.WaveplateToday(20419) != 0 {
		t.Fatal("waveplate")
	}
	card := TaskDef{TaskOwnerScratchCard, 10014, TaskWaveplateSpent, 40, 10, 2}
	if a.Tasks.TakenToday(card, 20418) != 3 || a.Tasks.TakenToday(card, 20419) != 0 {
		t.Fatal("taken")
	}
	tb := &data.DataTables{}
	if s := a.Turntable.StateOf(tb, 101500009); s != (TurntableState{1, 5, false}) || !a.Turntable.StateOf(tb, 101500008).AllDone {
		t.Fatal(s)
	}
	if a.Scratch.Opened(701) != 7 || a.Scratch.Opened(702) != 0 {
		t.Fatal("scratch")
	}
}

func TestDoubleRewardsCoverTheClientsDungeonTypes(t *testing.T) {
	eq(t, DoubleUpList(2), []int32{1, 2})
	eq(t, DoubleUpList(3), []int32{6})
	if len(DoubleUpList(9)) != 0 {
		t.Fatal("9")
	}
}

func TestALoginTaskIsDoneOnceADayAWaveplateTaskUpToItsLimit(t *testing.T) {
	login := TaskDef{TaskOwnerScratchCard, 10013, TaskDailyLogin, 1, 1, 1}
	spend := TaskDef{TaskOwnerScratchCard, 10014, TaskWaveplateSpent, 40, 10, 2}
	live := TaskDef{TaskOwnerTurntable, 15, TaskGuideLiveness, 100, 1, 3}
	got := []int32{TaskCompletions(login, 0), TaskCompletions(spend, 39), TaskCompletions(spend, 40), TaskCompletions(spend, 125), TaskCompletions(spend, 10000), TaskCompletions(live, 0)}
	eq(t, got, []int32{1, 0, 1, 3, 10, 1})
}

func TestWhatTheClientReadsOfATask(t *testing.T) {
	spend := TaskDef{TaskOwnerTurntable, 14, TaskWaveplateSpent, 120, 1, 2}
	running := TaskPb(spend, 60, 0)
	if running.Current != 60 || running.Target != 120 || running.Status != protocol.ActivityTaskState_ActivityTaskState_ActivityTaskRunning {
		t.Fatal(running)
	}
	if TaskPb(spend, 120, 0).Status != protocol.ActivityTaskState_ActivityTaskState_ActivityTaskFinish ||
		TaskPb(spend, 120, 1).Status != protocol.ActivityTaskState_ActivityTaskState_ActivityTaskTaken || TaskPb(spend, 500, 1).Current != 120 {
		t.Fatal("states")
	}
	card := TaskDef{TaskOwnerScratchCard, 10014, TaskWaveplateSpent, 40, 10, 2}
	if info := TaskTimeInfoPb(card, 100, 2); info.Progress != 20 || info.FinishCount != 2 || TaskTimeInfoPb(card, 400, 10).Progress != 40 {
		t.Fatal(info)
	}
}

func TestASpecialPrizeEndsTheRoundTheLastOneFinishesTheTurntable(t *testing.T) {
	awards := []*data.TurntableAwards{{Id: 1, ActivityId: 7, IsSpecial: true}, {Id: 2, ActivityId: 7}, {Id: 3, ActivityId: 7}}
	fresh := TurntableState{}
	eq(t, TurntableOpenPicks(fresh, awards), []int{0, 1, 2})
	if len(TurntableClaimedIDs(fresh, awards)) != 0 {
		t.Fatal("claimed")
	}
	one := TurntableState{0, 0b010, false}
	eq(t, TurntableOpenPicks(one, awards), []int{0, 2})
	eq(t, TurntableClaimedIDs(one, awards), []int32{2})
	next := int32(1)
	if TurntableAfterSpin(fresh, awards, 1, &next) != (TurntableState{0, 0b010, false}) || TurntableAfterSpin(one, awards, 0, &next) != (TurntableState{Round: 1}) {
		t.Fatal("spin")
	}
	done := TurntableAfterSpin(TurntableState{Round: 2}, awards, 0, nil)
	if !done.AllDone || len(TurntableOpenPicks(done, awards)) != 0 {
		t.Fatal(done)
	}
	eq(t, TurntableClaimedIDs(done, awards), []int32{1, 2, 3})
}

func TestACardsLayoutIsAShuffleThatStaysPut(t *testing.T) {
	first := ScratchLayout(777, 701, 49)
	if !slices.Equal(first, ScratchLayout(777, 701, 49)) || slices.Equal(first, ScratchLayout(777, 702, 49)) || slices.Equal(first, ScratchLayout(778, 701, 49)) {
		t.Fatal("layout")
	}
	sorted := slices.Clone(first)
	slices.Sort(sorted)
	for i, v := range sorted {
		if v != i {
			t.Fatal("not a permutation")
		}
	}
	if slices.Equal(first, sorted) {
		t.Fatal("not shuffled")
	}
}

func TestOpenedCellsAndWhatIsLeft(t *testing.T) {
	if ScratchIsOpen(0, 3) {
		t.Fatal("open")
	}
	one := ScratchWithOpen(0, 3)
	if !ScratchIsOpen(one, 3) || ScratchIsFinished(one, 4) || !ScratchIsFinished(0b1111, 4) || !ScratchIsFinished(^uint64(0), 49) {
		t.Fatal("masks")
	}
	rewards := []*data.ScratchCardRewardRe{
		{Id: 1, RoundId: 701, RewardItem: &data.IntPair{Item1: 3, Item2: 40}},
		{Id: 2, RoundId: 701, RewardItem: &data.IntPair{Item1: 3, Item2: 40}},
		{Id: 3, RoundId: 701, RewardItem: &data.IntPair{Item1: 2, Item2: 10000}},
	}
	left := ScratchLeftRewards(rewards, []int{0, 1, 2}, ScratchWithOpen(0, 1))
	if len(left) != 2 || left[3] != 40 || left[2] != 10000 {
		t.Fatal(left)
	}
	if ScratchCellCount(&data.ScratchCardRoundRe{RoundId: 701, Size: 7}, 49) != 49 || ScratchCellCount(&data.ScratchCardRoundRe{RoundId: 701, Size: 7}, 10) != 10 ||
		ScratchCellCount(&data.ScratchCardRoundRe{RoundId: 701, Size: 0}, 49) != 0 {
		t.Fatal("cells")
	}
}

func TestCyclesAreBackToBackAndHoldNow(t *testing.T) {
	const now = 1794571200
	for _, d := range []int64{7, 21, 28} {
		b, e := Cycle(now, d)
		nb, _ := Cycle(e, d)
		if !(b <= now && now < e) || e-b != d*ScheduleDay || nb != e || (b-ScheduleEpoch)%(7*ScheduleDay) != 0 {
			t.Fatal(d, b, e)
		}
	}
}

// ----- in the session -----

func enterActivityRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.Activity = []*data.Activity{
		{Id: 100100001, Type: SignType}, // excluded
		{Id: 100200001, Type: SignType},
		{Id: 100200002, Type: SignType}, // newest sign-in
		{Id: 100900001, Type: TimePointRewardType},
		{Id: 103400001, Type: NewLordGymType},
		{Id: 101500009, Type: TurntableActivityType},
		{Id: 100700100, Type: DoubleActivityType},
	}
	tb.ActivitySign = []*data.ActivitySign{
		{Id: 100200001, SignRewards: []*data.OneItemConfig{{ItemId: 2, Count: 5}}},
		{Id: 100200002, SignRewards: []*data.OneItemConfig{{ItemId: 2, Count: 10}, {ItemId: 1505, Count: 1}}, FreeDropId: 600},
	}
	tb.TimePointRewardActivity = []*data.TimePointRewardActivity{
		{Id: 2, ActivityId: 100900001, Rewards: []*data.IntPair{{Item1: 1311, Item2: 1}}},
		{Id: 1, ActivityId: 100900001, Rewards: []*data.IntPair{{Item1: 2, Item2: 7}}},
	}
	tb.DoubleRewardActivity = []*data.DoubleRewardActivity{{Id: 100700100, Type: 2, Count: 3, UpRate: 2}}
	tb.TurntableInfo = []*data.TurntableInfo{{Id: 101500009, CostItemId: 3, CostItemCount: 1, TurntableType: TurntableQuestCoins}}
	tb.TurntableAwards = []*data.TurntableAwards{
		{Id: 11, ActivityId: 101500009, GroupId: 1, IsSpecial: true, RewardItem: pairs(2, 50)},
		{Id: 21, ActivityId: 101500009, GroupId: 2, IsSpecial: true, RewardItem: pairs(2, 60)},
	}
	tb.RoleInfo = append(tb.RoleInfo, &data.RoleInfo{Id: 1505, SpilloverItem: pairs(3, 1)}, &data.RoleInfo{Id: 1311, SpilloverItem: pairs(3, 2)})
	tb.DropPackage = []*data.DropPackage{{Id: 600, DropPreview: pairs(2, 1)}}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enter(false)
	return r, s, c
}

func TestThePageListsTheNewestActivityWithDataOfEachTypeAndEveryListedDoubleReward(t *testing.T) {
	_, s, _ := enterActivityRig(t)
	page := s.ActivityPage()
	var ids []int32
	byID := map[int32]*protocol.ActivityData{}
	for _, a := range page.Activities {
		ids = append(ids, a.Id)
		byID[a.Id] = a
	}
	eq(t, ids, []int32{103400001, 101500009, 100900001, 100700100, 100200002})
	if byID[103400001].Data != nil {
		t.Fatal("新领主挑战 carries no payload")
	}
	tp := byID[100900001].GetTimePointRewardActivityData()
	if len(tp.Rewards) != 2 || tp.Rewards[0].Id != 1 || tp.Rewards[1].Id != 2 || !tp.Rewards[0].CanGetReward || tp.Rewards[1].Task.Current != 0 || tp.Rewards[1].Task.Target != 1 {
		t.Fatal(tp)
	}
	sign := byID[100200002]
	eq(t, sign.GetSignActivity().SignStateList, []protocol.SignState{protocol.SignState_SignState_Unlock, protocol.SignState_SignState_Lock})
	if !sign.IsUnlock || sign.EndOpenTime-sign.BeginOpenTime != 21*ScheduleDay || byID[100700100].GetDoubleInstActivityReward().GetDoubleInstRwdCount != 0 {
		t.Fatal(sign)
	}
}

func TestASignInDayIsClaimedOnceAndTheFreeGiftOnce(t *testing.T) {
	r, s, _ := enterActivityRig(t)
	lock, ok := protocol.ErrorCode_ErrorCode_ErrActivityLock, protocol.ErrorCode_ErrorCode_Success
	if s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 1}).ErrorCode != lock {
		t.Fatal("day 2 open")
	}
	resp := s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 0})
	if resp.ErrorCode != ok || resp.ItemMap[2] != 10 || s.ItemCount(2) != 160 {
		t.Fatal(resp, s.ItemCount(2))
	}
	if s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 0}).ErrorCode != lock ||
		s.Sign(&protocol.SignActivityRequest{ActivityId: 5, Index: 0}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrSignRewardCfg {
		t.Fatal("twice")
	}
	if s.SignFreeDrop(&protocol.SignActivityFreeDropRequest{ActivityId: 100200002}).ErrorCode != ok ||
		s.SignFreeDrop(&protocol.SignActivityFreeDropRequest{ActivityId: 100200002}).ErrorCode != lock {
		t.Fatal("free")
	}
	r.clock.Advance(ScheduleDay * 1000)
	if s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 1}).ErrorCode != ok || s.ItemCount(3) != 1 {
		t.Fatal("spillover")
	}
}

func TestTimePointRewardsAreClaimedByIndexAndInABatch(t *testing.T) {
	r, s, _ := enterActivityRig(t)
	lock, ok := protocol.ErrorCode_ErrorCode_ErrActivityLock, protocol.ErrorCode_ErrorCode_Success
	if s.TimePointReward(&protocol.TimePointActivityRewardRequest{Id: 99}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrSignRewardCfg ||
		s.TimePointReward(&protocol.TimePointActivityRewardRequest{Id: 2}).ErrorCode != lock {
		t.Fatal("single")
	}
	if s.TimePointBatchReward(&protocol.TimePointActivityBatchRewardRequest{Ids: []int32{1, 2}}).ErrorCode != ok || s.ItemCount(2) != 157 {
		t.Fatal("batch")
	}
	if s.TimePointBatchReward(&protocol.TimePointActivityBatchRewardRequest{Ids: []int32{1, 2}}).ErrorCode != lock ||
		s.TimePointBatchReward(&protocol.TimePointActivityBatchRewardRequest{}).ErrorCode != ok {
		t.Fatal("batch 2")
	}
	r.clock.Advance(ScheduleDay * 1000)
	if s.TimePointReward(&protocol.TimePointActivityRewardRequest{Id: 2}).ErrorCode != ok || s.ItemCount(3) != 2 {
		t.Fatal("spillover")
	}
}

func TestATurntableSpinCostsACoinAndASpecialPrizeOpensTheNextRound(t *testing.T) {
	r, s, _ := enterActivityRig(t)
	if s.RunTurntable(&protocol.RunTurntableActivityRequest{ActivityId: 5}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrTurntableConfigNotFound ||
		s.RunTurntable(&protocol.RunTurntableActivityRequest{}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrItemNotEnough {
		t.Fatal("refusals")
	}
	s.Actions().GrantItem(3, 2, false)
	spin := s.RunTurntable(&protocol.RunTurntableActivityRequest{})
	if spin.ErrorCode != protocol.ErrorCode_ErrorCode_Success || spin.RewardId != 11 || !spin.IsFinishCurRound || spin.ItemMap[2] != 50 {
		t.Fatal(spin)
	}
	if s.Activities().Turntable.StateOf(r.tables, 101500009) != (TurntableState{Round: 2}) {
		t.Fatal("round 2")
	}
	if l := s.RunTurntable(&protocol.RunTurntableActivityRequest{ActivityId: 101500009}); l.RewardId != 21 || !s.Activities().Turntable.StateOf(r.tables, 101500009).AllDone {
		t.Fatal(l)
	}
	if s.RunTurntable(&protocol.RunTurntableActivityRequest{}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrTurntableActivityIsFinish || s.ItemCount(3) != 0 {
		t.Fatal("finished")
	}
}

func TestSpentWaveplateIsCountedForTheDailyTasks(t *testing.T) {
	r, s, _ := enterActivityRig(t)
	s.Actions().GrantItem(ActivityWaveplateItem, 100, false)
	n := int32(60)
	s.Actions().TakeItem(ActivityWaveplateItem, &n)
	if s.Activities().Tasks.WaveplateToday(Today(r.clock.Seconds())) != 60 {
		t.Fatal("waveplate")
	}
}

// ----- Boss 连战, 领主挑战, fishing -----

func TestBossRushScoreAndSavedLines(t *testing.T) {
	if k, ts, l := BossRushScore(3, 100); k != 3000 || ts != 2500 || l != 500 {
		t.Fatal(k, ts, l)
	}
	if _, ts, _ := BossRushScore(3, 9999); ts != 0 {
		t.Fatal(ts)
	}
	r := newRig(t)
	r.save("boss_rush", "L 8666 4200 1\r\nS 2\nbad\n")
	b := NewBossRush(rigFiles(r).Files)
	if b.Best(8666) != 4200 {
		t.Fatal(b.Best(8666))
	}
	tb := (&data.DataTables{
		BossRushActivity: []*data.BossRushActivity{{Id: 1, ActivityId: 100805004, InstId: 8666, RewardId: 600}},
		BossRushScore:    []*data.BossRushScore{{Id: 1, Score: 1500}, {Id: 2, Score: 1500}},
	}).Reindex()
	d := b.Data(tb, 100805004)
	if d.BossRushLevelInfo[0].LevelRewardClaimStatus != protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimed {
		t.Fatal(d)
	}
	eq(t, []protocol.BossRushRewardClaimStatus{d.BossRushScoreScoreRewardInfo[0].RewardClaimStatus, d.BossRushScoreScoreRewardInfo[1].RewardClaimStatus},
		[]protocol.BossRushRewardClaimStatus{protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimable, protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimed})
	eq(t, d.UnlockedBuffIndices, []int32{0, 1, 2, 3})
}

func TestSavedLordGymLines(t *testing.T) {
	r := newRig(t)
	r.save("lord_gym", "1001 95 1\r\n1002 0 1\n\nbad\n")
	g := NewLordGyms(rigFiles(r).Files)
	tb := (&data.DataTables{
		LordGym:         []*data.LordGym{{Id: 1001, Difficulty: 2, PlayId: 7}, {Id: 1002, Difficulty: 3, PlayId: 8}, {Id: 9, IsDebug: true}},
		LordGymEntrance: []*data.LordGymEntrance{{Id: 5, LordGymList: []int32{1001, 1002}}},
	}).Reindex()
	info := g.Info(tb, 1794571200)
	eq(t, info.UnlockLoadGymIds, []int32{1001, 1002})
	eq(t, info.ReadLoadGymIds, []int32{1001, 1002})
	if len(info.LordGymPassRecords) != 1 || info.LordGymPassRecords[0].LoadGymId != 1001 || info.LordGymPassRecords[0].PassTime != 95 {
		t.Fatal(info.LordGymPassRecords)
	}
	if gi := info.LordGymGroupInfos[0]; gi.GroupId != 5 || gi.PassDiff != 2 {
		t.Fatal(gi)
	}
	if e := info.EntranceInfos[0]; e.EffectEndTime-e.EffectBeginTime != 28*ScheduleDay {
		t.Fatal(e)
	}
}

func TestFishingInfoSurvivesTheWire(t *testing.T) {
	b, err := proto.Marshal(&protocol.FishingInfoResponse{FishingInfo: FishingInfo()})
	if err != nil {
		t.Fatal(err)
	}
	var got protocol.FishingInfoResponse
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	f := got.FishingInfo
	if f.IllustratedInfo == nil || f.ShipInfo.SkinId != FishingStartSkinID || f.ShipInfo.CabinInfo.CabinShape != FishingStartCabinShape {
		t.Fatal(f)
	}
}

func TestABossRushLevelIsScoredAtItsFinishAndItsRewardClaimedOnce(t *testing.T) {
	r := newRig(t)
	tb := r.tables
	tb.InstanceDungeon = append(tb.InstanceDungeon, &data.InstanceDungeon{Id: 8666, MapConfigId: 8666, InstType: 2})
	tb.BossRushActivity = []*data.BossRushActivity{{Id: 1, ActivityId: 100805004, InstId: 8666, RewardId: 600, BossCount: 2}}
	tb.BossRushScore = []*data.BossRushScore{{Id: 1, Score: 1000, RewardId: 600}}
	tb.ItemInfo = append(tb.ItemInfo, &data.ItemInfo{Id: 5001})
	tb.DropPackage = []*data.DropPackage{{Id: 600, DropPreview: pairs(5001, 1)}}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enterWithQuests(quests.NewCatalog(quests.NewQuestDataSet()), false)
	s.EnterInst(&protocol.EnterInstRequest{InstanceId: 8666})
	s.RunAfterResponse()
	r.clock.Advance(100_000)
	s.Actions().RunDetached(finishDungeonAction(true))
	res := last[*protocol.BossRushResultNotify](t, c)
	if res.KilMonsterScore != 2000 || res.TimerScore != 2500 || res.HighScore != 4500 || res.LeftTime != 500 {
		t.Fatal(res)
	}
	if last[*protocol.BossRushLevelInfoNotify](t, c).BossRushLevelInfo[0].LevelRewardClaimStatus != protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimable {
		t.Fatal("claimable")
	}
	b := s.Activities().BossRush
	level, score := protocol.BossRushRewardType_BossRushRewardType_Level, protocol.BossRushRewardType_BossRushRewardType_Score
	if b.Claim(s, 8666, level) != protocol.ErrorCode_ErrorCode_Success || b.Claim(s, 8666, level) != protocol.ErrorCode_ErrorCode_ErrActivityLock ||
		b.Claim(s, 1, score) != protocol.ErrorCode_ErrorCode_Success || s.ItemCount(5001) != 2 {
		t.Fatal("claims")
	}
}

func TestALordGymRunsItsLevelPlayAtItsDifficultyAndAPassIsRecorded(t *testing.T) {
	r := newRig(t)
	tb := r.tables
	tb.LordGym = []*data.LordGym{{Id: 1001, Difficulty: 3, PlayId: 9100, RewardId: 600}}
	tb.ItemInfo = append(tb.ItemInfo, &data.ItemInfo{Id: 5001})
	tb.DropPackage = []*data.DropPackage{{Id: 600, DropPreview: pairs(5001, 2)}}
	r.rebuild()
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	ds := quests.NewQuestDataSet()
	ds.LevelPlays = append(ds.LevelPlays, quests.LevelPlayDef{ID: 9100, Name: "领主", LevelID: 8,
		Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2, 3), qNode(2, 1, "ChildQuest", "ReachArea"), qNode(3, 1, "QuestSucceed", "")}})
	s, c := r.enterWithQuests(quests.NewCatalog(ds), false)
	gyms := s.Activities().LordGyms
	if gyms.Begin(s, 5) || !gyms.Begin(s, 1001) || !s.Quests().IsLevelPlayRunning(9100) || !gyms.IsCurrentPlay(9100) {
		t.Fatal("begin")
	}
	r.clock.Advance(42_000)
	s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: quests.LevelPlayRun(9100), NodeId: 2})
	res := last[*protocol.LordGymLevelPlayResultNotify](t, c)
	if !res.IsSuccess || !res.IsNewRecord || res.LordGymPassRecord.LoadGymId != 1001 || res.LordGymPassRecord.PassTime != 42 {
		t.Fatal(res)
	}
	if len(res.ItemDatas) != 1 || res.ItemDatas[0].ItemId != 5001 || res.ItemDatas[0].Count != 2 {
		t.Fatal(res.ItemDatas)
	}
	gyms.MarkRead(1001)
	if !strings.Contains(r.read("lord_gym"), "1001 42 1") {
		t.Fatal(r.read("lord_gym"))
	}
}

// ----- 失序梦途 -----

// enterDreamLinkRig: activity 102600001: role dungeon 8406 (row 1, open after quest 1), boss
// level 8408 (row 1), energy reward 1 (400 energy, drop 600), a boss reward 101.
func enterDreamLinkRig(t *testing.T, questSave string) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.Activity = []*data.Activity{{Id: 102600001, Type: DreamLinkActivityType}}
	tb.RogueWhiteCat = []*data.RogueWhiteCat{{Id: 102600001, EnergyId: 80100004, MaxEnergy: 1200, Insts: []int32{1}, Rewards: []int32{1}, BossRewards: []int32{101},
		BossInstanceList: []int32{1}, FirstWhiteCatDungeonId: 8407, OpenCondition: pairs(3, 50)}}
	tb.RogueWhiteCatInst = []*data.RogueWhiteCatInst{{Id: 1, InstId: 8406, ConditionGroupId: 50}}
	tb.RogueBossInstance = []*data.RogueBossInstance{{Id: 1, InstanceId: 8408}}
	tb.RogueWhiteCatReward = []*data.RogueWhiteCatReward{{Id: 1, NeedEnergy: 400, DropId: 600}}
	tb.RogueWhiteCatBossReward = []*data.RogueWhiteCatBossReward{{Id: 101, DropId: 600}}
	tb.ConditionGroup = []*data.ConditionGroup{{Id: 50, GroupId: []int32{70}}}
	tb.Condition = []*data.Condition{{Id: 70, Type: "NewQuestState", LimitParams: &data.Map[string, string]{Pairs: []data.Pair[string, string]{{Key: "NewQuestId", Value: "1"}}}}}
	tb.InstanceDungeon = append(tb.InstanceDungeon, &data.InstanceDungeon{Id: 8406, MapConfigId: 8406}, &data.InstanceDungeon{Id: 8408, MapConfigId: 8408})
	tb.ItemInfo = append(tb.ItemInfo, &data.ItemInfo{Id: 5001}, &data.ItemInfo{Id: 80100004})
	tb.DropPackage = []*data.DropPackage{{Id: 600, DropPreview: pairs(5001, 1)}}
	r.rebuild()
	r.saveLivePlayer()
	r.save("quests", questSave)
	s, c := r.enterWithQuests(quests.NewCatalog(quests.NewQuestDataSet()), false)
	return r, s, c
}

func dreamPage(s *PlayerSession) *protocol.RogueWhiteCatData {
	for _, a := range s.ActivityPage().Activities {
		if a.Id == 102600001 {
			return a.GetRogueWhiteCatData()
		}
	}
	return nil
}

func TestDreamLinkSavedLines(t *testing.T) {
	r := newRig(t)
	r.save("dream_link", "P 8406\r\nE 1\nB 101\nR 2\nL 103\nD 2\nX 9\nbad\n")
	d := NewDreamLink(rigFiles(r).Files)
	if !d.IsPassed(8406) || d.IsPassed(9) {
		t.Fatal("passed")
	}
}

func TestThePageFollowsTheQuestAndTheClearedDungeons(t *testing.T) {
	_, s, _ := enterDreamLinkRig(t, "")
	page := dreamPage(s)
	if page.MaxEnergy != 1200 || page.RogueRoleInstData[0].IsUnlock || len(page.UnlockButtons) != 0 || page.LimitedEndTime-page.LimitedStartTime != 21*ScheduleDay {
		t.Fatal(page)
	}
	eq(t, page.SignStateList, []protocol.SignState{protocol.SignState_SignState_Lock})
	_, again, c := enterDreamLinkRig(t, "F 1\n")
	open := dreamPage(again)
	if !open.RogueRoleInstData[0].IsUnlock {
		t.Fatal("locked")
	}
	eq(t, open.UnlockButtons, []int32{3})
	again.Activities().DreamLink.OnFinished(again, 8408, true)
	pushed := last[*protocol.ActivityUpdateNotify](t, c).Activities[0].GetRogueWhiteCatData()
	if !pushed.RogueBossInstDatas[0].IsFinish {
		t.Fatal("finish")
	}
	eq(t, pushed.BossRewardStateList, []protocol.SignState{protocol.SignState_SignState_Unlock})
}

func TestARoleDungeonStartsOnlyWhenOpenAndRewardsAreClaimedOnce(t *testing.T) {
	r, s, _ := enterDreamLinkRig(t, "F 1\n")
	lock, ok := protocol.ErrorCode_ErrorCode_ErrActivityLock, protocol.ErrorCode_ErrorCode_Success
	if s.DreamLinkRoleInst(&protocol.RoguelikeRoleInstStartRequest{Index: 9}).ErrorCode != lock ||
		s.DreamLinkRoleInst(&protocol.RoguelikeRoleInstStartRequest{Index: 1}).ErrorCode != ok {
		t.Fatal("role inst")
	}
	s.RunAfterResponse()
	if s.Scene.InstanceID != 8406 {
		t.Fatal(s.Scene.InstanceID)
	}
	d := s.Activities().DreamLink
	if d.Claim(s, DreamEnergy, 1) != lock {
		t.Fatal("no energy")
	}
	s.Actions().GrantItem(80100004, 400, false)
	all := s.DreamLinkClaimAll(&protocol.MulRoguelikeWriteCatRewardRequest{Index: []int32{1, 2}})
	if all.ErrorCode != ok {
		t.Fatal(all)
	}
	eq(t, all.Index, []int32{1})
	if d.Claim(s, DreamEnergy, 1) != lock || d.Claim(s, DreamBoss, 101) != lock {
		t.Fatal("twice / not ready")
	}
	d.OnFinished(s, 8408, true)
	if d.Claim(s, DreamBoss, 101) != ok || s.ItemCount(5001) != 2 || !strings.Contains(r.read("dream_link"), "P 8408") {
		t.Fatal("boss")
	}
}
