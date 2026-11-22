// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"slices"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/protocol"
)

// Client storage (R-113), cosmetic defaults (§2.1), explore tools and echo projector summons
// (§5), guides and the codex (§6), short messages, the battle pass and the profile (§7.2–7.4),
// cosmetics and the camera (§9) and tutorials (§11). Ported from our C# ProfileAndCosmeticsTests.

func enterProfileRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.ItemInfo = []*data.ItemInfo{{Id: 2}, {Id: 3}}
	tb.FlySkinConfig = []*data.FlySkinConfig{{Id: 7001, SkinType: 0}, {Id: 7002, SkinType: 1}}
	tb.RoleInfo = []*data.RoleInfo{{Id: 1304, SkinId: 130400, WeaponType: 2}}
	tb.RoleSkin = []*data.RoleSkin{{Id: 130400, RoleId: 1304, SuitWeaponSkinId: 8001}, {Id: 130401, RoleId: 1304, SuitWeaponSkinId: 8001}}
	tb.WeaponSkin = []*data.WeaponSkin{{Id: 8001, IsShow: true, WeaponSkinType: 2}, {Id: 8002, IsShow: true, HideInSkinView: true, WeaponSkinType: 2}}
	tb.Ornament = []*data.Ornament{
		{Id: 9001, RoleSkinIds: []int32{130400, 130401}, OrGroupId: 1, ItemAccess: []int32{9901}, OrnamentBuff: []float64{3001}},
		{Id: 9002, RoleSkinIds: []int32{130400, 130401}, OrGroupId: 1},
		{Id: 9003, RoleSkinIds: []int32{130400}, OrGroupId: 2, HideInUi: true},
	}
	tb.CalabashSkin = []*data.CalabashSkin{{Id: 6001}}
	tb.CalabashDevelopReward = []*data.CalabashDevelopReward{{MonsterId: 31001, IsShow: true, AllowVision: true, DevelopCondition: []int32{1, 2}}}
	tb.PhantomItem = []*data.PhantomItem{
		{ItemId: 600001, MonsterId: 31001, ParentMonsterId: 0, SkillId: 240010},
		{ItemId: 600002, MonsterId: 31002, ParentMonsterId: 31001, SkillId: 240010},
	}
	tb.SummonCfg = append(tb.SummonCfg, &data.SummonCfg{Id: 240010, BlueprintType: "ProjectorVisionBp", BornBuffId: []float64{3001}})
	tb.BlueprintConfig = append(tb.BlueprintConfig, &data.BlueprintConfig{BlueprintType: "ProjectorVisionBp", EntityType: "Custom", EntityLogic: "Vision"})
	tb.TemplateConfig = append(tb.TemplateConfig, &data.TemplateConfig{Id: 39000001, BlueprintType: "ProjectorVisionBp"})
	tb.Buff = append(tb.Buff, &data.Buff{Id: 3001, DurationPolicy: 1})
	tb.PlayerHeadRe = []*data.PlayerHeadRe{{Id: 82001209}, {Id: 82001412}}
	tb.PlayerTitle = []*data.PlayerTitle{{Id: 501}}
	tb.BackgroundCard = []*data.BackgroundCard{{Id: 401}}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enter(false)
	return r, s, c
}

func storageOf(n *protocol.StorageInfoNotify, sys protocol.EClientStorageSystemIdType) *protocol.ClientStorageInfo {
	for _, i := range n.Infos {
		if i.SystemId == int32(sys) {
			return i
		}
	}
	return nil
}

func TestClientStorageSurvivesReloadAndReplacesLoginDefaults(t *testing.T) {
	_, s, _ := enterProfileRig(t)
	activity := protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_Activity
	outer := &protocol.ClientStorageMapMapData{Data: map[int32]*protocol.ClientStorageMapData{100300001: {Data: map[int32]int32{1: 1}}}}
	s.ClientStorage().HandleUpdate(&protocol.StorageInfoUpdateRequest{Infos: []*protocol.ClientStorageInfo{
		{SystemId: int32(activity), Data: &protocol.ClientStorageInfo_MapMapData{MapMapData: outer}},
		{SystemId: int32(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_RoleSkinRedDot), Data: &protocol.ClientStorageInfo_SetData{SetData: &protocol.ClientStorageSetData{Data: []int32{121101}}}},
	}})
	n := NewPlayerClientStorage(s.Files).BuildNotify(s.Combat.Player.CosmeticInfo.ViewedOrnaments)
	eq(t, storageOf(n, protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_RoleSkinRedDot).GetSetData().Data, []int32{121101})
	orn := storageOf(n, protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_Ornament).GetSetData().Data
	if !slices.Contains(orn, 9001) || !slices.Contains(orn, 9901) {
		t.Fatal(orn)
	}
	if storageOf(n, activity).GetMapMapData().Data[100300001].Data[1] != 1 {
		t.Fatal("activity storage")
	}
}

func TestCosmeticDefaultsSeedAndSkinsOrnamentsAndCameraWork(t *testing.T) {
	_, s, c := enterProfileRig(t)
	cos := s.Combat.Player.CosmeticInfo
	eq(t, cos.FlySkins, []int32{7001, 7002})
	eq(t, cos.RoleSkins, []int32{130400, 130401})
	eq(t, cos.WeaponSkins, []int32{8001})
	eq(t, cos.Ornaments, []int32{9001, 9002})
	if !slices.Contains(cos.ViewedOrnaments, 9901) || !slices.Contains(cos.PhantomSkins, 600002) {
		t.Fatal(cos)
	}
	c.msgs = nil
	if ChangeOrnament(s, &protocol.ChangeOrnamentRequest{RoleSkinId: 130400, OrnamentId: 9001, IsDress: true}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		len(ofType[*protocol.EntityDressOrnamentChangeNotify](c)) != 1 || len(ofType[*protocol.OrnamentDressInfoUpdateNotify](c)) != 1 {
		t.Fatal(c.names())
	}
	if ChangeOrnament(s, &protocol.ChangeOrnamentRequest{RoleSkinId: 130400, OrnamentId: 9002, IsDress: true}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("9002")
	}
	role, _ := s.Combat.Player.Roles.Get(1304)
	if len(role.Ornaments) != 1 || role.Ornaments[0].OrnamentID != 9002 {
		t.Fatal(role.Ornaments)
	}
	c.msgs = nil
	if RoleSkinChange(s, &protocol.RoleSkinChangeRequest{RoleId: 1304, SkinId: 130401, SignatureWeapon: true}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		role.RoleSkinID != 130401 || role.WeaponSkinID != 8001 || len(ofType[*protocol.EquipTakeOnNotify](c)) != 1 {
		t.Fatal(role.RoleSkinID, role.WeaponSkinID, c.names())
	}
	CameraSettingModify(s, &protocol.CameraSettingModifyRequest{CameraSetting: &protocol.CameraSettingInfo{CameraParam: &protocol.CameraParamInfo{CameraMode: 2, Fov: 75, PitchValue: 12.5}}})
	cam := CameraSettingQuery(s, &protocol.CameraSettingQueryRequest{}).CameraSetting.CameraParam
	if cam.CameraMode != 2 || cam.Fov != 75 || cam.PitchValue != 12.5 {
		t.Fatal(cam)
	}
}

func TestExploreToolsRouletteAndEchoProjectorSummonWork(t *testing.T) {
	_, s, c := enterProfileRig(t)
	c.msgs = nil
	resp := ExploreSkillRouletteSet(s, &protocol.ExploreSkillRouletteSetRequest{RouletteType: protocol.RouletteType_RouletteType_Explore,
		SkillRoulette: &protocol.ExploreSkillRoulette{SkillIds: []int32{1001, 1007, 6002}, ExploreSkill: 1007, ExtraItemId: 55}})
	if resp.ErrCode != protocol.ErrorCode_ErrorCode_Success || len(ofType[*protocol.ExploreSkillRouletteUpdateNotify](c)) != 1 {
		t.Fatal(resp)
	}
	eq(t, s.State.ExploreTools.Roulette, []int32{1001, 1007, 6002, 0, 0, 0, 0, 0})
	c.msgs = nil
	sum := Summon3(s, &protocol.Summon3Request{SummonerEntityId: 10, SummonInfo: &protocol.SummonRequestInfo{
		SummonEntityId: 77777, SummonConfigId: 31001, Pos: &protocol.Vector{X: 1, Y: 2, Z: 3}, IsVisible: true}})
	if sum.ErrorCode != protocol.ErrorCode_ErrorCode_Success || s.Scene.Get(77777) == nil || len(ofType[*protocol.EntityAddNotify](c)) != 1 {
		t.Fatal(sum)
	}
	if Summon3(s, &protocol.Summon3Request{SummonerEntityId: 10, SummonInfo: &protocol.SummonRequestInfo{SummonEntityId: 77777, SummonConfigId: 31001}}).ErrorCode !=
		protocol.ErrorCode_ErrorCode_ErrSummonEntityIdAlreadyExist {
		t.Fatal("duplicate")
	}
	RemoveSummonEntity(s, &protocol.RemoveSummonEntityRequest{SummonerId: 999, RemoveEntityIds: []int64{77777}})
	if s.Scene.Get(77777) == nil {
		t.Fatal("removed by another summoner")
	}
	RemoveSummonEntity(s, &protocol.RemoveSummonEntityRequest{SummonerId: 10, RemoveEntityIds: []int64{77777}})
	if s.Scene.Get(77777) != nil {
		t.Fatal("kept")
	}
}

func TestProfileGuidesShortMessageAndTutorialsWork(t *testing.T) {
	r, s, c := enterProfileRig(t)
	c.msgs = nil
	if sm := ShortMessageInfo(); sm.ChatBgId == 0 || sm.BubbleId == 0 {
		t.Fatal(sm)
	}
	name := ModifyName(s, &protocol.ModifyNameRequest{Name: "Rover38"})
	if name.ErrorCode != protocol.ErrorCode_ErrorCode_Success || s.Combat.Player.BasicInfo.Attributes.Name != "Rover38" ||
		name.LastModifyNameTime != max(0, s.Clock.Seconds()-72*3600+5) || len(ofType[*protocol.NameModifyNotify](c)) != 1 {
		t.Fatal(name)
	}
	// R-153: a signature is saved and given back (it was always refused as a dirty word).
	sig := ModifySignature(s, &protocol.ModifySignatureRequest{Signature: "离线的潮汐 ☆"})
	if sig.ErrorCode != protocol.ErrorCode_ErrorCode_Success || sig.Signature != "离线的潮汐 ☆" ||
		s.Combat.Player.BasicInfo.Attributes.Sign != "离线的潮汐 ☆" || !strings.Contains(r.read("basic_info"), "离线的潮汐 ☆") {
		t.Fatal(sig)
	}
	if u := TutorialUnlock(s, &protocol.TutorialUnlockRequest{Id: 42}); u.UnLockInfo == nil || u.UnLockInfo.Id != 42 {
		t.Fatal(u)
	}
	before := s.ItemCount(3)
	if recv := TutorialReceive(s); recv.ItemMap[3] != 20 || s.ItemCount(3) != before+20 {
		t.Fatal(recv)
	}
	s.Guides().HandleFinish(&protocol.GuideFinishRequest{GroupId: 88888})
	list := NewPlayerGuides(s.Files).HandleInfo(&protocol.GuideInfoRequest{}).GuideGroupFinishList
	if !slices.Contains(list, 88888) || !slices.Contains(list, 10116) {
		t.Fatal(list)
	}
}
