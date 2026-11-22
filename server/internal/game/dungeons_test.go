// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// R-4a, R-32, R-50c, R-127: dungeons (specs/dungeons.md §3, §5); R-41..R-127: 深塔. Ported from
// our C# DungeonTests and TowerTests.

const (
	testDungeon    = 301010
	testDungeonMap = 301000
	testPlay       = 9001
)

func boolp(b bool) *bool { return &b }

func finishDungeonAction(ok bool) []quests.QuestAction {
	return []quests.QuestAction{{Name: "FinishDungeon", Params: &quests.ActionParams{IsSuccess: boolp(ok)}}}
}

// enterDungeonRig: dungeon 301010 (map 301000, a repeatable run at level 60, reward 77) with a
// monster 7001 and a start device 7002 its level play 9001 waits on; entrance 5 lists it.
func enterDungeonRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	var dungeons []*data.InstanceDungeon
	for _, d := range tb.InstanceDungeon {
		if d.Id != testDungeon {
			dungeons = append(dungeons, d)
		}
	}
	tb.InstanceDungeon = append(dungeons, &data.InstanceDungeon{Id: testDungeon, MapConfigId: testDungeonMap, InstType: RepeatableRun, BornPosition: []int32{100, 200, 300}, RewardId: 77, EntityLevel: 60})
	tb.InstanceDungeonEntrance = []*data.InstanceDungeonEntrance{{Id: 5, InstanceDungeonList: []int32{testDungeon, 301011}}}
	tb.BlueprintConfig = append(tb.BlueprintConfig,
		&data.BlueprintConfig{BlueprintType: "Monster001", EntityType: "Monster", EntityLogic: "Monster", HalfHeight: 100},
		&data.BlueprintConfig{BlueprintType: "Device01", EntityType: "Gameplay", EntityLogic: "Item"})
	tb.TemplateConfig = append(tb.TemplateConfig,
		&data.TemplateConfig{Id: 7, BlueprintType: "Monster001", ComponentsData: parseJSON(t, `{"BaseInfoComponent":{"Camp":1},"AttributeComponent":{"PropertyId":90000015,"Level":10}}`)},
		&data.TemplateConfig{Id: 8, BlueprintType: "Device01", ComponentsData: parseJSON(t, `{"BaseInfoComponent":{"Camp":0}}`)})
	tb.ItemInfo = append(tb.ItemInfo, &data.ItemInfo{Id: 5001})
	tb.DropPackage = []*data.DropPackage{{Id: 600, DropPreview: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 5001, Value: 3}}}}}
	v := func(x, y, z int32) *data.IntVector { return &data.IntVector{X: x, Y: y, Z: z} }
	tb.LevelEntityConfig = []*data.LevelEntityConfig{
		{MapId: testDungeonMap, EntityId: 7001, BlueprintType: "Monster001", Name: "怪", Transform: []*data.IntVector{v(11000, 20000, 30000), v(0, 0, 0)}, ComponentsData: parseJSON(t, `{}`)},
		{MapId: testDungeonMap, EntityId: 7002, BlueprintType: "Device01", Name: "副本_挑战交互", Transform: []*data.IntVector{v(12000, 20000, 30000), v(0, 0, 0)}, ComponentsData: parseJSON(t, `{}`)},
		// An entrance on the world map: its option enters the dungeon (R-66), landing on 7002.
		{MapId: 8, EntityId: 8100, BlueprintType: "Device01", Name: "入口", Transform: []*data.IntVector{v(0, 0, 0), v(0, 0, 0)},
			ComponentsData: parseJSON(t, `{ "InteractComponent": { "Options": [ { "Type": { "Type": "Actions", "Actions": [ { "Name": "TeleportDungeon", "Params": { "DungeonId": 301010 } } ] } } ] } }`)},
	}
	dir := filepath.Join(r.state, "bindata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ExchangeReward.json"), []byte(`[{ "Id": 77, "RewardId": { "1": 600 } }]`), 0o644); err != nil {
		t.Fatal(err)
	}
	r.rebuild()
	r.data.JSONDirs = []string{dir}
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	ds := quests.NewQuestDataSet()
	n2 := qNode(2, 1, "ChildQuest", "CheckEntityState")
	n2.States = []quests.StateCond{{EntityID: 7002, State: "关卡.Common.状态.激活"}}
	ds.LevelPlays = append(ds.LevelPlays, quests.LevelPlayDef{ID: testPlay, Name: "挑战", LevelID: testDungeonMap, Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2), n2}})
	ds.DungeonPlays[testDungeon] = testPlay
	s, c := r.enterWithQuests(quests.NewCatalog(ds), false)
	return r, s, c
}

func TestTheWayBackFormatsAndParses(t *testing.T) {
	i := DungeonInside{Dungeon: 301010, BackInstance: 8, BackPos: world.Vec3{X: -111141.4, Y: 74229.6, Z: 6980.7}}
	if got := FormatDungeonInside(i); got != "301010 8 -111141 74230 6981\n" {
		t.Fatalf("format %q", got)
	}
	p := ParseDungeonInside(FormatDungeonInside(i))
	if p == nil || *p != (DungeonInside{Dungeon: 301010, BackInstance: 8, BackPos: world.Vec3{X: -111141, Y: 74230, Z: 6981}}) {
		t.Fatalf("parse %+v", p)
	}
	if ParseDungeonInside("") != nil {
		t.Fatal("empty parses")
	}
	if f0(2.5) != "3" || f0(-2.5) != "-3" || f0(0.4) != "0" {
		t.Fatal("F0 rounds half away from zero")
	}
}

func TestEnteringADungeonRepliesThenChangesSceneAndStartsItsLevelPlay(t *testing.T) {
	r, s, c := enterDungeonRig(t)
	c.msgs = nil
	if resp := s.EnterInst(&protocol.EnterInstRequest{InstanceId: testDungeon}); resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal(resp.ErrorCode)
	}
	if len(c.msgs) != 0 {
		t.Fatal("the reply goes first")
	}
	s.RunAfterResponse()
	if c.names()[0] != "LeaveSceneNotify" || s.Scene.InstanceID != testDungeon {
		t.Fatalf("%v %d", c.names()[:1], s.Scene.InstanceID)
	}
	if s.Scene.PlayerPos != (world.Vec3{X: 100, Y: 200, Z: 300}) {
		t.Fatal(s.Scene.PlayerPos)
	}
	if !strings.HasPrefix(r.read(DungeonFileName), "301010 8 -111141 74229 6981") {
		t.Fatal(r.read(DungeonFileName))
	}
	if !s.Quests().IsLevelPlayRunning(testPlay) || last[*protocol.EnterLevelPlayNotify](t, c).Id != testPlay {
		t.Fatal("level play")
	}
	s.Heartbeat()
	has := func(id int64) bool {
		for _, sp := range s.Spawner.Spawns() {
			if int64(sp.Candidate.EntityID()) == id {
				return true
			}
		}
		return false
	}
	if !has(7002) || has(7001) {
		t.Fatal("the start device spawns, the monster sleeps")
	}
	s.World.State.Wake(7001)
	s.MarkSpawns()
	s.Heartbeat()
	if !has(7001) {
		t.Fatal("woken monster")
	}
	for _, sp := range s.Spawner.Spawns() {
		if sp.Candidate.EntityID() == 7001 {
			row := s.RowForWoken(sp.Candidate)
			attr := row.ComponentsData.(*data.Object).Get("AttributeComponent").(*data.Object)
			if attr.Get("Level").(data.Number) != 60 {
				t.Fatal("dungeon level")
			}
		}
	}
}

func TestAnEntranceOptionEntersTheDungeonAfterTheResponse(t *testing.T) {
	_, s, _ := enterDungeonRig(t)
	e := s.Scene.Add(&world.Entity{ConfigID: 8100, EntityType: world.TypeSceneEntity, ConfigType: world.ConfigLevel})
	if s.EntityInteract(&protocol.EntityInteractRequest{EntityId: e.ID, OptionIndex: 0}).Interacting {
		t.Fatal("interacting")
	}
	if s.Scene.InstanceID != 8 {
		t.Fatal("moved before the reply")
	}
	s.RunAfterResponse()
	if s.Scene.InstanceID != testDungeon || !s.QuestState.Dungeons.IsEntered(testDungeon) {
		t.Fatal("not entered")
	}
}

func TestAnUnknownInstanceIsRefusedWithoutASceneChange(t *testing.T) {
	_, s, c := enterDungeonRig(t)
	c.msgs = nil
	if s.EnterInst(&protocol.EnterInstRequest{InstanceId: 4242}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound {
		t.Fatal("accepted")
	}
	s.RunAfterResponse()
	if len(c.msgs) != 0 || s.Scene.InstanceID != 8 {
		t.Fatal("moved")
	}
}

func TestLeavingGoesBackWhereThePlayerCameFromAndStopsTheLevelPlay(t *testing.T) {
	r, s, c := enterDungeonRig(t)
	s.EnterInst(&protocol.EnterInstRequest{InstanceId: testDungeon})
	s.RunAfterResponse()
	c.msgs = nil
	if s.LeaveInst(&protocol.LeaveInstRequest{}).ErrorCode != protocol.ErrorCode_ErrorCode_Success || s.Scene.InstanceID != testDungeon {
		t.Fatal("the reply first")
	}
	s.RunAfterResponse()
	if s.Scene.InstanceID != 8 || s.Scene.PlayerPos != (world.Vec3{X: -111141.48, Y: 74229.27, Z: 6980.71}) {
		t.Fatal(s.Scene.InstanceID, s.Scene.PlayerPos)
	}
	if s.Quests().IsLevelPlayRunning(testPlay) {
		t.Fatal("level play runs")
	}
	left := false
	for _, n := range ofType[*protocol.LeaveLevelPlayNotify](c) {
		left = left || n.Id == testPlay
	}
	if !left || r.read(DungeonFileName) != "" {
		t.Fatal("not left")
	}
}

func TestRechallengeReloadsTheDungeonOnlyWhenInside(t *testing.T) {
	_, s, c := enterDungeonRig(t)
	if s.SingleInstRechallenge(&protocol.SingleInstRechallengeRequest{}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound {
		t.Fatal("outside")
	}
	s.EnterInst(&protocol.EnterInstRequest{InstanceId: testDungeon})
	s.RunAfterResponse()
	c.msgs = nil
	if s.SingleInstRechallenge(&protocol.SingleInstRechallengeRequest{}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("inside")
	}
	s.RunAfterResponse()
	first := ""
	for _, n := range c.names() {
		if n == "LeaveSceneNotify" || n == "JoinSceneNotify" {
			first = n
			break
		}
	}
	if first != "LeaveSceneNotify" || s.Scene.InstanceID != testDungeon || !s.Quests().IsLevelPlayRunning(testPlay) {
		t.Fatal("not reloaded")
	}
}

func TestAnOpenWorldInstanceIsAReturnNotADungeon(t *testing.T) {
	_, s, _ := enterDungeonRig(t)
	s.EnterInst(&protocol.EnterInstRequest{InstanceId: 906})
	s.RunAfterResponse()
	if s.Scene.InstanceID != 906 || s.QuestState.Dungeons.Current != nil {
		t.Fatal("not a return")
	}
}

func TestTheRewardIsClaimedOnlyInADungeonWithOne(t *testing.T) {
	_, s, c := enterDungeonRig(t)
	if s.GetInstExchangeReward(&protocol.GetInstExchangeRewardRequest{ExchangeCount: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound {
		t.Fatal("claimed outside")
	}
	s.EnterInst(&protocol.EnterInstRequest{InstanceId: testDungeon})
	s.RunAfterResponse()
	if s.GetInstExchangeReward(&protocol.GetInstExchangeRewardRequest{ExchangeCount: 2}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("not claimed")
	}
	settle := last[*protocol.InstSettleNotify](t, c)
	items := settle.RewardItems[0].ItemList
	if !settle.IsSuccess || len(items) != 2 || items[0].ItemId != 5001 || items[0].Count != 3 || items[1].Count != 3 {
		t.Fatal(settle)
	}
	if s.ItemCount(5001) != 6 {
		t.Fatal("count")
	}
}

func TestAnEntranceListsItsDungeons(t *testing.T) {
	_, s, _ := enterDungeonRig(t)
	resp := s.InstEntranceDetail(&protocol.InstEntranceDetailRequest{EntranceId: 5})
	var ids []int32
	for _, e := range resp.InstDetailEntryList {
		ids = append(ids, e.InstId)
	}
	eq(t, ids, []int32{testDungeon, 301011})
	if resp.EntranceId != 5 || len(s.InstEntranceDetail(&protocol.InstEntranceDetailRequest{EntranceId: 6}).InstDetailEntryList) != 0 {
		t.Fatal(resp)
	}
}

func TestAQuestMovingThePlayerOutDropsTheWayBack(t *testing.T) {
	_, s, _ := enterDungeonRig(t)
	s.EnterInst(&protocol.EnterInstRequest{InstanceId: testDungeon})
	s.RunAfterResponse()
	d := s.QuestState.Dungeons
	d.OnQuestTransfer(testDungeon)
	if d.Current == nil {
		t.Fatal("dropped inside")
	}
	d.OnQuestTransfer(8)
	if d.Current != nil {
		t.Fatal("kept")
	}
}

func TestTheTeamScreenEntersTheOfferedDungeonWhenConfirmed(t *testing.T) {
	_, s, c := enterDungeonRig(t)
	d := s.QuestState.Dungeons
	d.OfferTeleport(s, testDungeon)
	offer := last[*protocol.TeleportDungeonActionNotify](t, c)
	if offer.HostPlayerId != rigPlayer || offer.DungeonId != testDungeon {
		t.Fatal(offer)
	}
	s.TeleportDungeonAction(&protocol.TeleportDungeonActionRequest{IncId: offer.IncId, IsConfirm: false})
	s.RunAfterResponse()
	if s.Scene.InstanceID != 8 {
		t.Fatal("declined but moved")
	}
	d.OfferTeleport(s, testDungeon)
	s.TeleportDungeonAction(&protocol.TeleportDungeonActionRequest{IncId: last[*protocol.TeleportDungeonActionNotify](t, c).IncId, IsConfirm: true})
	s.RunAfterResponse()
	if s.Scene.InstanceID != testDungeon {
		t.Fatal("not entered")
	}
}

// ----- 深塔 -----

func TestCommonsAndElitesInOneWaveThenEachBossAlone(t *testing.T) {
	w := GroupTowerWaves([]int32{1, 2, 3}, []int32{10, 11})
	if len(w) != 3 || !slices.Equal(w[0], []int32{1, 2, 3}) || w[1][0] != 10 || w[2][0] != 11 {
		t.Fatal(w)
	}
	if w := GroupTowerWaves(nil, []int32{10}); len(w) != 1 || w[0][0] != 10 {
		t.Fatal(w)
	}
}

func TestAStoryOnlyPreviewSpawnsTheTowerVersionOfTheSameModel(t *testing.T) {
	tpl := func(id int32, bp, name string, model int) *data.TemplateConfig {
		return &data.TemplateConfig{Id: id, BlueprintType: bp, Name: name,
			ComponentsData: parseJSON(t, `{ "ModelComponent": { "ModelType": { "Type": "ModelId", "ModelId": `+itoa(int64(model))+` } } }`)}
	}
	ts := []*data.TemplateConfig{
		tpl(1000080, "Monster046", "TsEntity_BOSS_无冠者_任务专用", 390008),
		tpl(650000014, "Monster071", "BOSS-暗-无冠者_复刷专用", 390008),
		tpl(650000041, "Monster104", "BOSS_无冠者_爬塔专用", 390008),
		tpl(7, "MonsterX", "别的_任务专用", 390009),
	}
	none := func(string) bool { return false }
	towers := func(b string) bool { return b == "Monster104" }
	for _, c := range []struct {
		got, want int32
	}{
		{TowerVariant(ts, ts[0], none), 650000041},
		{TowerVariant(ts, ts[1], none), 650000014},
		{TowerVariant(ts, ts[3], none), 7},
		{TowerVariant(ts, ts[1], towers), 650000041},
		{TowerVariant(ts, ts[0], towers), 650000041},
		{TowerVariant(ts, ts[1], func(b string) bool { return b == "Monster071" || b == "Monster104" }), 650000014},
	} {
		if c.got != c.want {
			t.Fatalf("got %d want %d", c.got, c.want)
		}
	}
}

func TestAFloorDungeonRunsTheFloorBeingClimbedElseTheNewestSeasons(t *testing.T) {
	floors := []*data.TowerConfig{
		{Id: 400, Season: 41, InstanceId: 20018, ShowMonsters: []int32{1}},
		{Id: 466, Season: 42, InstanceId: 20018, ShowMonsters: []int32{330000070, 330000060}},
		{Id: 300, Season: 40, InstanceId: 20018, ShowMonsters: []int32{2}},
		{Id: 459, Season: 42, InstanceId: 20013},
	}
	i400, i459 := int32(400), int32(459)
	if TowerFloorOf(floors, nil, 20018, 0).Id != 466 || TowerFloorOf(floors, &i400, 20018, 0).Id != 400 ||
		TowerFloorOf(floors, &i459, 20018, 0).Id != 466 || TowerFloorOf(floors, nil, 2071, 0) != nil {
		t.Fatal("floor of")
	}
}

func TestAStarForEachConditionTheClearMeets(t *testing.T) {
	finish := &data.TowerTarget{Id: 1, TargetType: 1, Params: []int32{0}}
	left120 := &data.TowerTarget{Id: 6, TargetType: 1, Params: []int32{120}}
	left180 := &data.TowerTarget{Id: 7, TargetType: 1, Params: []int32{180}}
	n := func(v int64) *int64 { return &v }
	if !(TowerTargetMet(finish, n(200)) && TowerTargetMet(left120, n(200)) && TowerTargetMet(left180, n(200))) {
		t.Fatal("200 s left")
	}
	if !(TowerTargetMet(left120, n(150)) && !TowerTargetMet(left180, n(150))) {
		t.Fatal("150 s left")
	}
	if !(TowerTargetMet(finish, n(50)) && !TowerTargetMet(left120, n(50))) {
		t.Fatal("50 s left")
	}
	if !TowerTargetMet(left180, nil) {
		t.Fatal("no countdown")
	}
}

func TestTheSaveKeepsStarsRewardsThePlayersOwnTeamAndEveryFloors(t *testing.T) {
	r := newRig(t)
	files := r.Game().NewSession(&recording{id: rigPlayer}).Files
	tw := NewTower(files)
	tw.Parse("F 459 3\r\nR 3 2\nbad\nF 463 3\nB 1 1404 1404 1308 1204\nP 463 1205 1304 1506\nP 462 1205 1304\n")
	if tw.StarsOf(459) != 3 || !tw.IsClaimed(3, 2) || tw.OwnTeam.Slot != 1 || tw.OwnTeam.CurRole != 1404 || tw.OwnTeam.Roles != [3]int32{1404, 1308, 1204} {
		t.Fatal(tw.Format())
	}
	if team, _ := tw.TeamOf(463); team != [3]int32{1205, 1304, 1506} {
		t.Fatal(team)
	}
	if team, _ := tw.TeamOf(462); team != [3]int32{1205, 1304, 0} {
		t.Fatal(team)
	}
	again := NewTower(files)
	again.Parse(tw.Format())
	if again.Format() != tw.Format() {
		t.Fatal("round trip")
	}
}

func TestCyclesAre28DaysFromMonday20240520(t *testing.T) {
	b, e := TowerCycle(1716148800+5, 28)
	if b != 1716148800 || e != 1716148800+28*86400 {
		t.Fatal(b, e)
	}
	if b, _ := TowerCycle(1716148800-1, 28); b != 1716148800-28*86400 {
		t.Fatal(b)
	}
}

// enterTowerRig: floor 463 (season 42, 深境区, dungeon 20013 on map 90) with stars [finish,
// 120 s, 180 s] and a 3-star reward of drop 600.
func setupTowerRig(t *testing.T) *rig {
	r := newRig(t)
	tb := r.tables
	tb.InstanceDungeon = append(tb.InstanceDungeon, &data.InstanceDungeon{Id: 20013, MapConfigId: 90, InstSubType: TowerDungeonSubType, InstType: 2, BornPosition: []int32{1, 2, 3}})
	tb.TowerConfig = []*data.TowerConfig{
		{Id: 463, Season: 42, Difficulty: 3, AreaNum: 1, Floor: 1, InstanceId: 20013, TargetConfig: []int32{1, 6, 7}},
		{Id: 300, Season: 40, Difficulty: 3, AreaNum: 1, Floor: 1, InstanceId: 20013},
	}
	tb.TowerTarget = []*data.TowerTarget{{Id: 1, TargetType: 1, Params: []int32{0}}, {Id: 6, TargetType: 1, Params: []int32{120}}, {Id: 7, TargetType: 1, Params: []int32{180}}}
	tb.TowerSeason = []*data.TowerSeason{{Id: 42, RewardGroup: 5}}
	tb.TowerSeasonReward = []*data.TowerSeasonReward{{Id: 1, Difficulty: 3, RewardGroup: 5, Reward: []*data.IntPair{{Item1: 3, Item2: 600}, {Item1: 30, Item2: 600}}}}
	tb.ItemInfo = append(tb.ItemInfo, &data.ItemInfo{Id: 5001})
	tb.DropPackage = []*data.DropPackage{{Id: 600, DropPreview: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 5001, Value: 3}}}}}
	r.rebuild()
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	return r
}

func enterTowerRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := setupTowerRig(t)
	s, c := r.enterWithQuests(quests.NewCatalog(quests.NewQuestDataSet()), false)
	return r, s, c
}

func filledRoleIDs(s *PlayerSession) []int32 {
	var ids []int32
	for _, slot := range s.Formation().FilledRoles() {
		ids = append(ids, slot.RoleID)
	}
	return ids
}

func TestTheSeasonUpdateNeverUnlocksADifficultyAndListsTheNewestSeasonsFloors(t *testing.T) {
	_, s, _ := enterTowerRig(t)
	resp := s.TowerSeasonUpdate()
	if resp.MaxUnlockDifficulty != 0 || resp.TowerInfo.MaxUnlockDifficulty != 4 || resp.TowerInfo.CurrentSeason != 42 {
		t.Fatal(resp)
	}
	for _, d := range resp.TowerInfo.TowerDifficulties {
		if d.Difficulty != 3 {
			continue
		}
		var ids []int32
		for _, a := range d.TowerAreas {
			for _, f := range a.TowerFloors {
				ids = append(ids, f.TowerConfigId)
			}
		}
		eq(t, ids, []int32{463})
		if d.MaxStar != 3 {
			t.Fatal(d.MaxStar)
		}
	}
}

func TestStartingAFloorSwapsTheTeamFirstAndLeavingTheTowerBringsTheOwnTeamBack(t *testing.T) {
	r, s, _ := enterTowerRig(t)
	resp := s.TowerStart(&protocol.TowerStartRequest{TowerConfigId: 463, Formation: []*protocol.TowerRolePb{{RoleId: 1505}, {RoleId: 9999}, {RoleId: 1304}}})
	if resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal(resp)
	}
	eq(t, filledRoleIDs(s), []int32{1505, 1304})
	if !strings.Contains(r.read("formation_info"), "1505") {
		t.Fatal("not saved")
	}
	s.RunAfterResponse()
	if s.Scene.InstanceID != 20013 {
		t.Fatal(s.Scene.InstanceID)
	}
	got := filledRoleIDs(s)
	slices.Sort(got)
	eq(t, got, []int32{1304, 1505})
	s.LeaveInst(&protocol.LeaveInstRequest{})
	s.RunAfterResponse()
	got = filledRoleIDs(s)
	slices.Sort(got)
	if s.Scene.InstanceID != 8 || s.Formation().CurRole != 1304 || s.QuestState.Tower.OwnTeam != nil {
		t.Fatal("not back")
	}
	eq(t, got, []int32{1304, 1311, 1505})
	var floorTeam []int32
	for _, d := range s.TowerInfoRequest().TowerInfo.TowerDifficulties {
		if d.Difficulty == 3 {
			for _, f := range d.TowerAreas[0].TowerFloors[0].Formation {
				floorTeam = append(floorTeam, f.RoleId)
			}
		}
	}
	eq(t, floorTeam, []int32{1505, 1304})
}

func TestAFloorsLevelPlaySpawnsTheFloorsMonstersAsTemplateWavesWithTheFloorsBuffs(t *testing.T) {
	r := setupTowerRig(t)
	tb := r.tables
	tb.BlueprintConfig = append(tb.BlueprintConfig, &data.BlueprintConfig{BlueprintType: "Monster001", EntityType: "Monster", EntityLogic: "Monster", HalfHeight: 100})
	tb.TemplateConfig = append(tb.TemplateConfig, &data.TemplateConfig{Id: 7, BlueprintType: "Monster001", Name: "怪", ComponentsData: parseJSON(t, `{"BaseInfoComponent":{"Camp":1},"AttributeComponent":{"PropertyId":90000015,"Level":10}}`)})
	tb.MonsterInfo = []*data.MonsterInfo{{Id: 330, RarityId: 1, MonsterEntityID: "Monster001"}}
	for _, f := range tb.TowerConfig {
		if f.Id == 463 {
			f.ShowMonsters = []int32{330}
			f.RoleBuff = []float64{9100}
			f.MonsterBuff = []float64{9200}
		}
	}
	r.rebuild()
	ds := quests.NewQuestDataSet()
	pid, varName := int32(3000), "M"
	n2 := qNode(2, 1, "ChildQuest", "MonsterCreator")
	n2.Var = "M"
	n2.Enter = []quests.QuestAction{{Name: "CreatePrefab", Params: &quests.ActionParams{PrefabID: &pid, Var: &varName}}}
	ds.LevelPlays = append(ds.LevelPlays, quests.LevelPlayDef{ID: 9002, Name: "低危区-塔1-第1层", LevelID: 90, Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2), n2}})
	ds.DungeonPlays[20013] = 9002
	ds.Prefabs[3000] = quests.PrefabDef{ID: 3000, Entities: []quests.PrefabEntity{{ID: 1, BlueprintType: "Monster001", Pos: [3]float32{10, 0, 0}}}}
	s, c := r.enterWithQuests(quests.NewCatalog(ds), false)
	s.TowerStart(&protocol.TowerStartRequest{TowerConfigId: 463, Formation: []*protocol.TowerRolePb{{RoleId: 1304}}})
	s.RunAfterResponse()
	if !s.Quests().IsLevelPlayRunning(9002) {
		t.Fatal("play")
	}
	groups := s.QuestState.Prefabs.Groups()
	if len(groups) != 1 || len(groups[0].Waves) != 1 || !slices.Equal(groups[0].Waves[0], []int32{7}) {
		t.Fatalf("%+v", groups)
	}
	s.Heartbeat()
	g := groups[0]
	if len(g.Alive) != 1 {
		t.Fatal(g.Alive)
	}
	net := g.Alive[0]
	found := false
	for _, n := range ofType[*protocol.EntityAddNotify](c) {
		for _, e := range n.EntityPbs {
			if e.Id == net {
				found = e.ConfigType == protocol.EntityConfigType_EntityConfigType_Template && e.ConfigId == 7
			}
		}
	}
	if !found {
		t.Fatal("template spawn")
	}
	hasBuff := func(id, buff int64) bool {
		for _, b := range s.Combat.Scene.GetEntity(id).Buffs {
			if b.BuffID == buff {
				return true
			}
		}
		return false
	}
	role, _ := s.Scene.RoleEntities.Get(1304)
	if !hasBuff(net, 9200) || !hasBuff(role, 9100) {
		t.Fatal("floor buffs")
	}
}

func TestAnUnknownFloorIsRefused(t *testing.T) {
	_, s, _ := enterTowerRig(t)
	if s.TowerStart(&protocol.TowerStartRequest{TowerConfigId: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound {
		t.Fatal("accepted")
	}
}

func TestAClearedFloorEarnsTheStarsOfItsTimeLeftAndTheRewardIsClaimedOnce(t *testing.T) {
	r, s, c := enterTowerRig(t)
	s.TowerStart(&protocol.TowerStartRequest{TowerConfigId: 463, Formation: []*protocol.TowerRolePb{{RoleId: 1304}}})
	s.RunAfterResponse()
	tw := s.QuestState.Tower
	tw.StartClock(s.Quests().NowMs(), 300)
	r.clock.Advance(150_000)
	s.Actions().RunDetached(finishDungeonAction(true))
	end := last[*protocol.TowerEndNotify](t, c)
	if !end.Success || !slices.Equal(end.CurrentFloorData.StarIndex, []int32{0, 1}) || tw.StarsOf(463) != 2 {
		t.Fatal(end)
	}
	if last[*protocol.TowerFloorUpdateNotify](t, c).TowerFloors[0].TowerConfigId != 463 {
		t.Fatal("update")
	}
	if s.TowerReward(&protocol.TowerRewardRequest{Difficulty: 3, RewardIndex: 0}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrActivityLock {
		t.Fatal("2 stars claim")
	}
	s.Actions().RunDetached(finishDungeonAction(true))
	if tw.StarsOf(463) != 3 {
		t.Fatal(tw.StarsOf(463))
	}
	claim := s.TowerReward(&protocol.TowerRewardRequest{Difficulty: 3, RewardIndex: 0})
	if claim.ErrorCode != protocol.ErrorCode_ErrorCode_Success || !slices.Equal(claim.RewardIndexList, []int32{0}) || s.ItemCount(5001) != 3 {
		t.Fatal(claim)
	}
	again := s.TowerReward(&protocol.TowerRewardRequest{Difficulty: 3, RewardIndex: 0, RewardIndexList: []int32{0, 1}})
	if again.ErrorCode != protocol.ErrorCode_ErrorCode_ErrActivityLock || len(again.RewardIndexList) != 0 {
		t.Fatal(again)
	}
	if !strings.Contains(r.read(TowerFileName), "R 3 0") {
		t.Fatal(r.read(TowerFileName))
	}
}
