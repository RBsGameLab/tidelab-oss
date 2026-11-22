// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/protocol"
)

// Achievements, the area terminal, map marks, the world map, ranges, monster patrols, the
// adventure manual and phonograph music (specs/systems.md §1, §3, §4, §7.5). Ported from our C#
// WorldSystemsTests.

func enterWorldSystemsRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.ItemInfo = []*data.ItemInfo{{Id: 2}, {Id: 3}}
	tb.DropPackage = []*data.DropPackage{{Id: 501, DropPreview: pairs(3, 5)}, {Id: 502, DropPreview: pairs(3, 20)}}
	tb.Achievement = []*data.Achievement{{Id: 100101, GroupId: 1001, Level: 1}, {Id: 100102, GroupId: 1001, Level: 3, OverrideDropId: 502}}
	tb.AchievementGroup = []*data.AchievementGroup{{Id: 1001, Category: 1, DropId: 502, Enable: true}, {Id: 1002, Category: 1, DropId: 501}}
	tb.AchievementStarLevel = []*data.AchievementStarLevel{{Level: 1, DropId: 501}, {Level: 3, DropId: 501}}
	tb.AreaMapGroup = []*data.AreaMapGroup{{Id: 1, MapId: 8}, {Id: 2, MapId: 906}}
	tb.ConditionGroup = []*data.ConditionGroup{{Id: 10, GroupId: []int32{101}}, {Id: 20, GroupId: []int32{102}}}
	level := func(v string) *data.Map[string, string] {
		return &data.Map[string, string]{Pairs: []data.Pair[string, string]{{Key: "Level", Value: v}}}
	}
	tb.Condition = []*data.Condition{{Id: 101, Type: "CheckLevel", LimitParams: level("50")}, {Id: 102, Type: "CheckLevel", LimitParams: level("99")}}
	tb.MapMark = []*data.MapMark{{MarkId: 301203, MapId: 8, ShowCondition: 10}, {MarkId: 301204, MapId: 8, ShowCondition: 20}, {MarkId: 301205, MapId: 8}}
	tb.MapFog = []*data.MapFog{{Fog: 1}, {Fog: 2}}
	tb.MultiMap = []*data.MultiMap{{Id: 10}}
	tb.MapBlockInfo = []*data.MapBlockInfo{{BlockId: 20}}
	tb.PhonographMusic = []*data.PhonographMusic{{Id: 101, Album: []int32{1, 2}}, {Id: 102, Album: []int32{1}}}
	tb.LevelEntityConfig = []*data.LevelEntityConfig{
		{Id: 1, EntityId: 5001, MapId: 8, BlueprintType: "Monster001"},
		{Id: 2, EntityId: 5002, MapId: 8, BlueprintType: "Monster001", InSleep: true},
		{Id: 3, EntityId: 5003, MapId: 906, BlueprintType: "Boss999", InSleep: true},
	}
	dir := filepath.Join(r.state, "json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"DungeonDetection.json":    `[{ "Id": 11 }, { "Id": 12 }]`,
		"MonsterDetection.json":    `[{ "Id": 310000010, "BlueprintType": "Monster001" }, { "Id": 340000330, "EntityConfigId": 5003 }]`,
		"SilentAreaDetection.json": `[{ "Id": 21 }]`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.rebuild()
	r.data.JSONDirs = []string{dir}
	r.saveLivePlayer()
	s, c := r.enter(false)
	return r, s, c
}

func TestAchievementsFinishCompleteGroupClaimRewardsAndSurviveSave(t *testing.T) {
	_, s, c := enterWorldSystemsRig(t)
	c.msgs = nil
	info := s.Achievements().Info(s.Data.Tables)
	if len(info.AchievementGroupInfoList) != 1 || len(info.AchievementGroupInfoList[0].AchievementEntryList) != 2 {
		t.Fatal(info)
	}
	if s.AchievementFinish(&protocol.AchievementFinishRequest{Id: 100101}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		len(ofType[*protocol.AchievementProgressNotify](c)) != 1 || len(ofType[*protocol.AchievementGroupProgressNotify](c)) != 0 {
		t.Fatal(c.names())
	}
	before := s.ItemCount(3)
	if s.AchievementReceive(&protocol.AchievementReceiveRequest{Id: 100101}).ErrorCode != protocol.ErrorCode_ErrorCode_Success || s.ItemCount(3) != before+5 {
		t.Fatal("claim")
	}
	if s.AchievementReceive(&protocol.AchievementReceiveRequest{Id: 100101}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrTriggerConditionNotMet {
		t.Fatal("twice")
	}
	s.Actions().RunDetached([]quests.QuestAction{{Name: "UnlockSystemItem", Raw: parseJSON(t, `{ "SystemOption": { "Type": "AchievementSystem", "Id": 100102 } }`)}})
	if len(ofType[*protocol.AchievementGroupProgressNotify](c)) != 1 {
		t.Fatal("group")
	}
	if n := last[*protocol.AchievementCountChangeNotify](t, c); n.StarCount != 4 || n.FinishCount != 2 {
		t.Fatal(n)
	}
	mul := s.MulAchievementReceive(&protocol.MulAchievementReceiveRequest{AchievementIds: []int32{100101, 100102}, AchievementGroupIds: []int32{1001}})
	eq(t, mul.AchievementIds, []int32{100102})
	eq(t, mul.AchievementGroupIds, []int32{1001})
	if s.ItemCount(3) != before+45 {
		t.Fatal(s.ItemCount(3))
	}
	reloaded := NewPlayerAchievements(s.Files)
	if stars, n := reloaded.Counts(s.Data.Tables); stars != 4 || n != 2 || !reloaded.Entry(100101).IsReceive {
		t.Fatal(stars, n)
	}
	if !s.RunCommands("achieve 100101") {
		t.Fatal("command")
	}
}

func TestAreaTerminalPushesAtLoginAndSavesPins(t *testing.T) {
	_, s, c := enterWorldSystemsRig(t)
	eq(t, ofType[*protocol.AreaTerminalInfoNotify](c)[0].AreaMapGroup, []int32{1, 2})
	SetAreaPin(s.Files, 7, true)
	SetAreaPin(s.Files, 3, true)
	SetAreaPin(s.Files, 7, false)
	eq(t, LoadAreaPins(s.Files), []int32{3})
	eq(t, ParseAreaPins("3 7\r\n3 x 12"), []int32{3, 7, 12})
}

func TestWorldMapCustomMarksAndServerJudgedMapMarksWork(t *testing.T) {
	_, s, c := enterWorldSystemsRig(t)
	c.msgs = nil
	resp := s.WorldMap().AddMark(&protocol.MapMarkRequest{MarkPointRequestInfo: &protocol.MarkPointRequestInfo{
		PosX: 10, PosY: 20, PosZ: 30, ConfigId: 2, MarkType: 11, IsTrace: true, MapId: 8, EntityConfigId: 99,
	}})
	if resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success || resp.Info.MarkId != 1_000_000 || resp.Info.MapId != 8 || resp.Info.EntityConfigId != 99 ||
		!slices.Contains(s.WorldMap().TrackedMarkIDs, 1_000_000) {
		t.Fatal(resp)
	}
	field := UnlockFieldInfo(s)
	eq(t, field.FieldId, []int32{1, 2})
	if len(ofType[*protocol.MultiMapInfoNotify](c)) != 1 {
		t.Fatal("multi map")
	}
	mn := last[*protocol.MapMarkInfoNotify](t, c)
	if len(mn.InfoList) != 1 || len(mn.ShowMarkIds) != 1 || mn.ShowMarkIds[0].MarkId != 301203 {
		t.Fatal(mn)
	}
	reloaded := NewPlayerWorldMap(s.Files)
	if len(reloaded.CustomMarks) != 1 {
		t.Fatal("reload")
	}
	if pb := reloaded.CustomMarks[0].ToPb(); pb.MarkId != 1_000_000 || pb.MapId != 8 || pb.EntityConfigId != 99 {
		t.Fatal(pb)
	}
	s.WorldMap().CancelTrace(s, &protocol.MapCancelTraceRequest{MarkId: 1_000_000})
	if len(s.WorldMap().TraceInfo().MarkIdList) != 0 {
		t.Fatal("trace")
	}
	s.WorldMap().RemoveMarks(&protocol.MapRemoveMarkRequest{MarkIds: []int32{1_000_000}})
	if len(s.WorldMap().CustomMarks) != 0 {
		t.Fatal("remove")
	}
	if mini := LastBigSceneMiniMapInfo(s); mini.Code != protocol.ErrorCode_ErrorCode_Success || mini.PosX != s.Scene.PlayerPos.X {
		t.Fatal(mini)
	}
}

func TestRangesAndPatrolsAnswerAsExpected(t *testing.T) {
	init := InitRange(&protocol.InitRangeRequest{RangeEntityId: 94748818081, IsPlayerInRange: true, InRangeEntityIds: []int64{94748818082, 94748818083}})
	if init.RangeEntityId != 94748818081 || len(init.EntitiesAccessRangeResult) != 2 || init.PlayerAccessRangeResult.AccessRangeType != protocol.AccessRangeType_AccessRangeType_RangeInit {
		t.Fatal(init)
	}
	pl := PlayerAccessRange(&protocol.PlayerAccessRangeRequest{RangeEntityId: 94748818081, AccessRangeType: protocol.AccessRangeType_AccessRangeType_RangeEnter})
	if pl.RangeEntityId != 94748818081 || pl.AccessRangeResult == nil {
		t.Fatal(pl)
	}
	ent := EntityAccessRange(&protocol.EntityAccessRangeRequest{RangeEntityId: 94748818081, AccessRangeType: protocol.AccessRangeType_AccessRangeType_RangeLeave, AccessEntityIds: []int64{94748818082, 94748818083}})
	if len(ent.EntitiesAccessRangeResult) != 2 {
		t.Fatal(ent)
	}
	var p Patrols
	p.Start(10, true)
	p.PointReached(10, 133002058, 1)
	p.PointReached(10, 133002058, 2)
	p.ChangeDir(10, false)
	p.Stop(10)
	if st, _ := p.Get(10); st != (PatrolState{Active: false, Forward: false, Spline: 133002058, Point: 2, PointsReached: 2}) {
		t.Fatal(st)
	}
	for i := 0; i < PatrolPruneAt; i++ {
		p.Start(int64(100+i), true)
	}
	p.Prune(func(id int64) bool { return id%2 == 0 })
	if p.Count() >= PatrolPruneAt {
		t.Fatal(p.Count())
	}
}

// The world map waits for both replies when it opens; a DarkCoastInfoResponse without
// DragonPoolConf threw in the 3.8 client and the map never opened (R-144h, real client).
func TestWorldMapOpenRepliesFollowTheSpec(t *testing.T) {
	dc := DarkCoastInfo(&protocol.DarkCoastInfoRequest{DragonPoolId: 3})
	if dc.ErrorCode != protocol.ErrorCode_ErrorCode_Success || dc.DragonPoolConf == nil || dc.DragonPoolConf.DragonPoolId != 3 {
		t.Fatal(dc)
	}
	_, s, _ := enterWorldSystemsRig(t)
	s.Data.Tables.InfrV2TreeBuild = []*data.InfrV2TreeBuild{{Id: 7}}
	in := InfrV2Info(s)
	fire := in.InfrInfo.GetFireInfo()
	if fire.GetFireLevel() != 1 || fire.GetFireStatus() != protocol.InfrV2StatusPb_InfrV2StatusPb_InfrV2StatusComplete || len(in.InfrInfo.GetTreeInfo().GetTrees()) != 1 {
		t.Fatal(in)
	}
}

func TestAdventureManualDetectionAndMusicWork(t *testing.T) {
	_, s, _ := enterWorldSystemsRig(t)
	m := AdventureManualInfo(s)
	eq(t, m.DetectionUnlocks.DungeonDetectionIds, []int32{11, 12})
	eq(t, m.DetectionUnlocks.MonsterDetectionIds, []int32{310000010, 340000330})
	eq(t, m.DetectionUnlocks.SilentAreaDetectionIds, []int32{21})
	d1 := GetDetectionTarget(s, &protocol.GetDetectionTargetRequest{DetectionId: 310000010})
	if len(d1.DetectionTargetDatas) != 1 || d1.DetectionTargetDatas[0].MapId != 8 {
		t.Fatal(d1)
	}
	eq(t, d1.DetectionTargetDatas[0].TargetIds, []int32{5001})
	d2 := GetDetectionTarget(s, &protocol.GetDetectionTargetRequest{DetectionId: 340000330})
	if len(d2.DetectionTargetDatas) != 1 || d2.DetectionTargetDatas[0].MapId != 906 {
		t.Fatal(d2)
	}
	eq(t, d2.DetectionTargetDatas[0].TargetIds, []int32{5003})
	music := s.Music().Info(s.Data.Tables)
	if len(music.AlbumTimeInfos) != 2 || !slices.Equal(music.AlbumTimeInfos[0].MusicIds, []int32{101, 102}) {
		t.Fatal(music)
	}
	s.Music().UpdateCollect(&protocol.PhantomMusicCollectUpdateRequest{CollectMusicIds: []int32{102}})
	eq(t, NewPlayerMusic(s.Files).FavoriteMusicIDs, []int32{102})
	// R-155: the bag's phonograph items are not songs; no song is new, so no "获得新曲目".
	if u := MusicUnlock(&protocol.PhantomMusicUnlockRequest{Items: []int32{60100001, 60100002}}); u.ErrorCode != protocol.ErrorCode_ErrorCode_Success || len(u.AddMusicIds) != 0 {
		t.Fatal(u)
	}
}

// R-165: the explore items of an area are its ExploreProgress rows, not ExploreScore's percent
// steps (the client looked up 60/80/100 as ids and logged an error each).
func TestExploreProgressListsTheAreasExploreProgressRows(t *testing.T) {
	_, s, _ := enterWorldSystemsRig(t)
	tb := s.Data.Tables
	tb.ExploreProgress = []*data.ExploreProgress{{Id: 1, Area: 2}, {Id: 7, Area: 2}, {Id: 9, Area: 3}}
	resp := ExploreProgress(s, &protocol.ExploreProgressRequest{AreaIds: []int32{2}})
	var ids []int32
	for _, p := range resp.AreaProgress[0].ExploreProgress {
		ids = append(ids, p.ExploreProgressId)
	}
	eq(t, ids, []int32{1, 7})
}
