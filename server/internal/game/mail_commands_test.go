// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/combat"
	"tidelab/internal/data"
	"tidelab/protocol"
)

// The mailbox (R-55, R-102), the command bot and commands (specs/systems.md §8, §12), and the
// motorcycle (§10). Ported from our C# MailboxTests, CommandsTests and MotorcycleAndCommandsTests.

func (r *rig) mailDir() string { return filepath.Join(r.state, "mailbox", "777") }

func (r *rig) dropMail(id, text string) {
	if err := os.MkdirAll(r.mailDir(), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.mailDir(), id+".mail"), []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func TestMailLinesKeepTheirLastCharacterAndLoseALeadingBom(t *testing.T) {
	if CleanMailLine("title: 兿\r") != "title: 兿" || CleanMailLine("  content: 佻 ") != "content: 佻" || CleanMailLine(utf8BOM+"title: x\r") != "title: x" {
		t.Fatal("clean")
	}
}

func TestAMailFileParses(t *testing.T) {
	m := ParseMail("gift", utf8BOM+"title: 守岸人的武器\r\nsender: 我\n# a note\ncontent: 第一行\ncontent: 第二行\nattach: 21050036 5\nattach: 2\ncmd: weapon 21050036 5\nbad line\n")
	if m.Title != "守岸人的武器" || m.Sender != "我" || m.Content != "第一行\n第二行" {
		t.Fatalf("%+v", m)
	}
	if len(m.Attachments) != 2 || m.Attachments[0].Id != 21050036 || m.Attachments[0].Count != 5 || m.Attachments[1].Id != 2 || m.Attachments[1].Count != 1 {
		t.Fatal(m.Attachments)
	}
	eq(t, m.Cmds, []string{"weapon 21050036 5"})
	if ValidMailID("../x") || ValidMailID("a..b") || !ValidMailID("gift_2026-11.20") {
		t.Fatal("ids")
	}
}

func TestLoginShowsEveryMailAndANewOneComesAtTheNextHeartbeat(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.dropMail("b", "title: 二\n")
	r.dropMail("a", "title: 一\ncontent: 中文内容\n")
	s, c := r.enter(false)
	infos := last[*protocol.MailInfosNotify](t, c).MailInfos
	if len(infos) != 2 || infos[0].Id != "a" || infos[1].Id != "b" || infos[0].Content != "中文内容" {
		t.Fatal(infos)
	}
	now := r.clock.Seconds()
	if b, _ := os.ReadFile(filepath.Join(r.mailDir(), "a.state")); string(b) != fmt.Sprintf("0 %d\n", now) {
		t.Fatal(string(b))
	}
	if infos[0].ReceivedTime != now || infos[0].ExpiryTime != now+MailValidSeconds || infos[0].Level != protocol.MailLevel_MailLevel_General {
		t.Fatal(infos[0])
	}
	c.msgs = nil
	s.Heartbeat()
	if len(ofType[*protocol.MailAddNotify](c)) != 0 {
		t.Fatal("nothing new")
	}
	r.dropMail("c", "title: 三\n")
	s.Heartbeat()
	adds := ofType[*protocol.MailAddNotify](c)
	if len(adds) != 1 || adds[0].NewMail.Id != "c" {
		t.Fatal(adds)
	}
	s.Heartbeat()
	if len(ofType[*protocol.MailAddNotify](c)) != 1 {
		t.Fatal("twice")
	}
}

func TestReadClaimAndDelete(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.dropMail("gift", "title: 礼物\ncmd: item 2 100\ncmd: broken\n")
	s, _ := r.enter(false)
	read := s.MailRead(&protocol.MailReadRequest{Id: "gift"})
	if read.ErrorCode != protocol.ErrorCode_ErrorCode_Success || read.State != MailStateRead {
		t.Fatal(read)
	}
	if s.MailRead(&protocol.MailReadRequest{Id: "nope"}).ErrorCode != protocol.ErrorCode_ErrorCode_RequestParamError {
		t.Fatal("nope")
	}
	var ran []string
	s.RunCommand = func(cmd string) bool {
		ran = append(ran, cmd)
		if b, _ := os.ReadFile(filepath.Join(r.mailDir(), "gift.state")); !strings.HasPrefix(string(b), "3 ") {
			t.Fatal("not claimed first")
		}
		if cmd == "broken" {
			panic(errors.New("boom"))
		}
		return true
	}
	claim := s.MailGetAttachment(&protocol.MailGetAttachmentRequest{MailIds: []string{"gift", "nope"}})
	if len(claim.SuccessIdMap) != 1 || claim.SuccessIdMap["gift"] != 3 {
		t.Fatal(claim)
	}
	eq(t, ran, []string{"item 2 100", "broken"})
	if len(s.MailGetAttachment(&protocol.MailGetAttachmentRequest{MailIds: []string{"gift"}}).SuccessIdMap) != 0 || len(ran) != 2 {
		t.Fatal("claimed twice")
	}
	del := s.MailDelete(&protocol.MailDeleteRequest{MailIds: []string{"gift", "../evil"}})
	eq(t, del.SuccessIds, []string{"gift"})
	for _, f := range []string{"gift.mail", "gift.state"} {
		if _, err := os.Stat(filepath.Join(r.mailDir(), f)); err == nil {
			t.Fatal(f + " kept")
		}
	}
}

// ----- commands -----

const (
	cmdWeapon5  = 21050116
	cmdWeapon4  = 21050036
	cmdWaveband = 10130401
)

func enterCommandRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.ItemInfo = []*data.ItemInfo{{Id: 2}, {Id: 3}, {Id: cmdWaveband}}
	tb.RoleInfo = []*data.RoleInfo{{Id: 1304, ResonantChainGroupId: 13, SpilloverItem: pairs(3, 5)}, {Id: 1505, ResonantChainGroupId: 99}}
	tb.ResonantChain = []*data.ResonantChain{
		{Id: 2, GroupId: 13, GroupIndex: 2, ActivateConsume: pairs(cmdWaveband, 1)},
		{Id: 1, GroupId: 13, GroupIndex: 1, ActivateConsume: pairs(cmdWaveband, 1)},
	}
	tb.WeaponConf = []*data.WeaponConf{
		{ItemId: cmdWeapon5, QualityId: 5, ResonLevelLimit: 5, BreachId: 50},
		{ItemId: cmdWeapon4, QualityId: 4, ResonLevelLimit: 5, BreachId: 40},
		{ItemId: 210013, QualityId: 5, ResonLevelLimit: 5, BreachId: 50},
	}
	tb.WeaponBreach = []*data.WeaponBreach{{BreachId: 50, Level: 0, LevelLimit: 20}, {BreachId: 50, Level: 6, LevelLimit: 90}, {BreachId: 50, Level: 5, LevelLimit: 80}}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enter(false)
	c.msgs = nil
	return r, s, c
}

func said(c *recording) []string {
	var out []string
	for _, n := range ofType[*protocol.PrivateMessageNotify](c) {
		out = append(out, n.ChatContent.Content)
	}
	return out
}

func TestCommandsSplitOnSemicolonsAndAnswerErrorsWithTheUsage(t *testing.T) {
	_, s, c := enterCommandRig(t)
	if s.RunCommands(" item 2 5 ;; nope; item 2 x ; shop 1; item") {
		t.Fatal("ok")
	}
	got := said(c)
	if len(got) != 5 || got[0] != "gave 5 of item 2 (now 155)." || !strings.HasPrefix(got[1], "unknown command \"nope\"") ||
		got[2] != "give_item: count \"x\" is not a whole number\nusage: item <item_id> [count=1]" ||
		got[3] != "open_shop: takes 0 argument(s)\nusage: shop" || !strings.HasPrefix(got[4], "give_item: takes 1 to 2 arguments") {
		t.Fatalf("%q", got)
	}
	if !s.RunCommands("give_item 2") || !strings.Contains(FindCommand("help").Run(s, nil), "give_item (item)") || !ArgBool("1") || ArgBool("false") {
		t.Fatal("help")
	}
}

func TestItemNeedsAnItemInfoRowAndPushesAddOrUpdate(t *testing.T) {
	_, s, c := enterCommandRig(t)
	if s.RunCommands("item 4 1") || said(c)[0] != "give_item: no item 4 in ItemInfo\nusage: item <item_id> [count=1]" {
		t.Fatal(said(c))
	}
	if s.RunCommands("item 2 0") || !s.RunCommands("item 3 2") {
		t.Fatal("counts")
	}
	// 3 (星声) is money: a player attribute, not a bag item (specs/inventory.md §2.3).
	money := last[*protocol.PlayerAttrNotify](t, c)
	if money.Attributes[0].Key != protocol.PlayerAttrKey_PlayerAttrKey_RareCoin || money.Attributes[0].GetInt32Value() != 2 || s.ItemCount(3) != 2 {
		t.Fatal(money)
	}
	if !s.RunCommands("item 2") || s.ItemCount(2) != 151 {
		t.Fatal("money update")
	}
	const bagItem = cmdWaveband
	if !s.RunCommands(fmt.Sprintf("item %d 2", bagItem)) || last[*protocol.NormalItemAddNotify](t, c).NormalItemList[0].Id != bagItem {
		t.Fatal("add")
	}
	if !s.RunCommands(fmt.Sprintf("item %d", bagItem)) || last[*protocol.NormalItemUpdateNotify](t, c).NormalItemList[0].Count != 3 || s.ItemCount(bagItem) != 3 {
		t.Fatal("update")
	}
}

func TestWeaponMaxIsTheTopBreachAndCopiesComeInOneNotify(t *testing.T) {
	r, s, c := enterCommandRig(t)
	if !s.RunCommands(fmt.Sprintf("weapon %d max", cmdWeapon5)) {
		t.Fatal(said(c))
	}
	list := last[*protocol.WeaponItemAddNotify](t, c).WeaponItemList
	if len(list) != 1 {
		t.Fatal(list)
	}
	w := list[0]
	if w.Id != cmdWeapon5 || w.WeaponLevel != 90 || w.WeaponBreach != 6 || w.WeaponResonLevel != 5 || w.FuncValue != 0 {
		t.Fatal(w)
	}
	if _, err := os.Stat(r.playerFile(fmt.Sprintf("weapon/%d", w.IncrId))); err != nil {
		t.Fatal(err)
	}
	if !s.RunCommands(fmt.Sprintf("weapon %d 2", cmdWeapon4)) {
		t.Fatal("two")
	}
	two := last[*protocol.WeaponItemAddNotify](t, c).WeaponItemList
	if len(two) != 2 || two[0].IncrId == two[1].IncrId {
		t.Fatal(two)
	}
	for _, x := range two {
		if x.Id != cmdWeapon4 || x.WeaponLevel != 1 || x.WeaponBreach != 0 || x.WeaponResonLevel != 1 || x.FuncValue != 1 {
			t.Fatal(x)
		}
	}
	if !s.RunCommands("weapon current") || len(last[*protocol.WeaponItemAddNotify](t, c).WeaponItemList) != 4 {
		t.Fatal("current")
	}
	if !s.RunCommands("weapon current clear") || len(last[*protocol.WeaponItemRemoveNotify](t, c).WeaponItemIncrIdList) != 4 {
		t.Fatal("clear")
	}
	n := 0
	for _, x := range s.Combat.Player.Weapons.Values() {
		if x.ID == 210013 {
			n++
		}
	}
	if n != 1 || s.RunCommands("weapon 999") || s.RunCommands(fmt.Sprintf("weapon %d 0", cmdWeapon4)) {
		t.Fatal(n)
	}
}

func TestWavebandIsTheFirstChainRowsItemWithoutTips(t *testing.T) {
	_, s, c := enterCommandRig(t)
	if item, ok := WavebandOf(s, 1304); !ok || item != cmdWaveband {
		t.Fatal(item)
	}
	if !s.RunCommands("waveband current") {
		t.Fatal(said(c))
	}
	add := last[*protocol.NormalItemAddNotify](t, c)
	if !add.NoTips || add.NormalItemList[0].Count != 6 {
		t.Fatal(add)
	}
	if !s.RunCommands("waveband 1304 clear") || s.ItemCount(cmdWaveband) != 0 || s.RunCommands("waveband 1505") {
		t.Fatal("clear")
	}
}

func TestAnOwnedRoleGivesItsSpillover(t *testing.T) {
	_, s, c := enterCommandRig(t)
	if !s.RunCommands("role 1304") || said(c)[0] != "role 1304 is owned already: gave its spillover items." || s.ItemCount(3) != 5 || s.RunCommands("role 4242") {
		t.Fatal(said(c))
	}
}

func TestGotoMovesInTheSceneAndMapChangesItAfterTheReply(t *testing.T) {
	_, s, c := enterCommandRig(t)
	if !s.RunCommands("goto 100 -200.5 300") {
		t.Fatal(said(c))
	}
	tp := last[*protocol.TeleportNotify](t, c)
	if tp.MapId != 8 || tp.Position.X != 100 || tp.Position.Y != -200.5 || tp.Position.Z != 300 || s.Scene.PlayerPos.Y != -200.5 {
		t.Fatal(tp)
	}
	if !s.RunCommands("m 906") || s.Scene.InstanceID != 8 {
		t.Fatal("map before reply")
	}
	s.RunAfterResponse()
	if s.Scene.InstanceID != 906 || s.RunCommands("map 12345") || s.RunCommands("quest") {
		t.Fatal("map")
	}
}

func TestTheBotAnswersChatAndFriendRequests(t *testing.T) {
	r, s, c := enterCommandRig(t)
	if !s.PrivateChatData().LoadSucc {
		t.Fatal("load")
	}
	h := last[*protocol.PrivateChatHistoryNotify](t, c).AllChats[0]
	if h.TargetUID != BotID || h.Chats[0].Content != BotGreeting {
		t.Fatal(h)
	}
	resp := s.PrivateChat(&protocol.PrivateChatRequest{TargetUID: BotID, Content: "item 2 1"})
	if resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success || resp.TargetUID != BotID || resp.FilterMsg != "item 2 1" {
		t.Fatal(resp)
	}
	reply := last[*protocol.PrivateMessageNotify](t, c).ChatContent
	if reply.SenderUID != BotID || reply.ChatContentType != protocol.ChatContentType_ChatContentType_Text || reply.Content != "gave 1 of item 2 (now 151)." {
		t.Fatal(reply)
	}
	if BotDetails().PlayerId != 1337 || !r.Game().Handlers().HasRequest(protocol.IDFriendAllRequest) || !r.Game().Handlers().HasRequest(protocol.IDPlayerBasicInfoGetRequest) {
		t.Fatal("handlers")
	}
}

func TestTheCommandFileIsEmptiedAndRunAtTheHeartbeat(t *testing.T) {
	r := newRig(t)
	file := filepath.Join(r.state, "cmd.txt")
	r.tables.ItemInfo = []*data.ItemInfo{{Id: 2}}
	r.rebuild()
	r.saveLivePlayer()
	r.Game().CommandFile = file
	s, c := r.enter(false)
	s.Heartbeat()
	if err := os.WriteFile(file, []byte(utf8BOM+" item 2 3\t\r\n\r\n   \nitem 2 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Heartbeat()
	if b, _ := os.ReadFile(file); len(b) != 0 {
		t.Fatalf("%q", b)
	}
	if s.ItemCount(2) != 157 || len(ofType[*protocol.PrivateMessageNotify](c)) != 2 {
		t.Fatal(s.ItemCount(2))
	}
	s.Heartbeat()
	if s.ItemCount(2) != 157 {
		t.Fatal("ran twice")
	}
}

func TestAGiftMailRunsItsCommands(t *testing.T) {
	r, s, _ := enterCommandRig(t)
	r.dropMail("gift", fmt.Sprintf("title: 礼物\ncmd: weapon %d 1\ncmd: item 2 10\n", cmdWeapon4))
	s.MailGetAttachment(&protocol.MailGetAttachmentRequest{MailIds: []string{"gift"}})
	n := 0
	for _, w := range s.Combat.Player.Weapons.Values() {
		if w.ID == cmdWeapon4 {
			n++
		}
	}
	if s.ItemCount(2) != 160 || n != 1 {
		t.Fatal(n)
	}
}

// ----- motorcycle -----

func enterMotorRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.MotorLvl = []*data.MotorLvl{{Level: 1, Exp: 0}, {Level: 10, Exp: 500}}
	tb.MotorTechTree = []*data.MotorTechTree{{Id: 1, Order: 1}}
	tb.MotorTech = []*data.MotorTech{{Id: 101, TreeType: 1, InitLevel: 1, TechLv: []int32{1001, 1002}}}
	tb.MotorTask = []*data.MotorTask{{Id: 201, TreeType: 1, RewardTpCount: 50}}
	tb.MotorSkin = []*data.MotorSkin{
		{Id: 301, DefaultFlag: true, BindFrame: 401, BindSticker: []int32{501}, BindDecorations: []int32{601}},
		{Id: 302, BindFrame: 402},
	}
	tb.MotorFrame = []*data.MotorFrame{{Id: 401, DefaultFlag: true}, {Id: 402}}
	tb.MotorStickerPart = []*data.MotorStickerPart{{Id: 1}, {Id: 2}}
	tb.MotorSticker = []*data.MotorSticker{{Id: 501, PartId: 1}, {Id: 502, PartId: 2}}
	tb.MotorLoadProject = []*data.MotorLoadProject{{Id: 1, Sticker: []int32{501, 502}, Frame: 401}}
	tb.MotorDecorationsPart = []*data.MotorDecorationsPart{{Id: 1}}
	tb.MotorDecorations = []*data.MotorDecorations{{Id: 601, PartId: 1}}
	tb.RoleInfo = []*data.RoleInfo{{Id: 1304, UiScenePerformanceABP: "/Game/Aki/Character/Role/FemaleM/Jinhsi/R2T1JinhsiMd10011/ABP_Performance"}}
	tb.BlueprintConfig = append(tb.BlueprintConfig, &data.BlueprintConfig{BlueprintType: "Npc_Passenger_1304", EntityLogic: "Npc", EntityType: "Npc"})
	tb.TemplateConfig = append(tb.TemplateConfig, &data.TemplateConfig{Id: 77001304, BlueprintType: "Npc_Passenger_1304"})
	tb.PhantomItem = []*data.PhantomItem{{ItemId: 600010, MonsterId: 31010, QualityId: 5, Rarity: 1}}
	tb.PhantomQuality = []*data.PhantomQuality{{Quality: 5, LevelLimit: 25, SlotUnlockLevel: []int32{5, 10, 15, 20, 25}}}
	tb.PhantomMainPropItem = []*data.PhantomMainPropItem{{Id: 1, PropId: 8, AddType: 1, StandardProperty: 2200, GrowthId: 1}}
	tb.PhantomGrowth = []*data.PhantomGrowth{{Id: 1, GrowthId: 1, Level: 25, Value: 10000}}
	tb.PhantomSubProperty = []*data.PhantomSubProperty{{Id: 11, PropId: 8, AddType: 1, SubStandardProperty: 500}, {Id: 12, PropId: 9, AddType: 1, SubStandardProperty: 1000}}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enter(false)
	return r, s, c
}

func TestMotorcycleOutlookPushesEntityChangeOrRespawnsOnFrameChangeAndPersistsPresets(t *testing.T) {
	_, s, c := enterMotorRig(t)
	m := s.Motorcycle()
	info := m.HandleInfo(s, &protocol.MotorInfoRequest{})
	if info.ErrorCode != protocol.ErrorCode_ErrorCode_Success || info.Motor.MotorLevel != 10 || len(info.Motor.UnlockedTree) != 1 {
		t.Fatal(info)
	}
	c.msgs = nil
	up := m.HandleTechLevelUp(s, &protocol.MotorTechLevelUpRequest{TechId: 101})
	if up.ErrorCode != protocol.ErrorCode_ErrorCode_Success || up.Tree.Tech[0].Level != 2 || len(ofType[*protocol.MotorLockedTechUpdateNotify](c)) != 1 {
		t.Fatal(up)
	}
	c.msgs = nil
	if m.HandleChangeOutlook(s, &protocol.MotorChangeOutlookRequest{FrameEquipped: 401, StickerEquipped: []int32{502}}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		len(ofType[*protocol.MotorOutlookEquippedChangeNotify](c)) != 1 || len(ofType[*protocol.EntityMotorOutlookChangeNotify](c)) != 1 || len(ofType[*protocol.EntityRemoveNotify](c)) != 0 {
		t.Fatal(c.names())
	}
	c.msgs = nil
	if m.HandleUseSkin(s, &protocol.MotorUseSkinRequest{SkinId: 302}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		len(ofType[*protocol.MotorOutlookEquippedChangeNotify](c)) != 1 || len(ofType[*protocol.EntityRemoveNotify](c)) != 1 || len(ofType[*protocol.EntityAddNotify](c)) != 1 {
		t.Fatal(c.names())
	}
	created := m.HandleCreatePreset(s, &protocol.MotorOutlookCreatePresetRequest{Name: "PlanA", Preset: &protocol.MotorOutlookEquippedPb{SkinEquipped: 301, FrameEquipped: 401, StickerEquipped: []int32{501, 502}}})
	if created.ErrorCode != protocol.ErrorCode_ErrorCode_Success || len(created.MotorOutlookPreset.Plan) != 1 {
		t.Fatal(created)
	}
	edited := m.HandleEditPreset(s, &protocol.MotorOutlookEditPresetRequest{PresetPlan: &protocol.MotorOutlookPresetPlanPb{Id: 1, Name: "PlanA-Edited", Preset: &protocol.MotorOutlookEquippedPb{SkinEquipped: 0, FrameEquipped: 402}}})
	if p := edited.MotorOutlookPreset.Plan[0]; edited.ErrorCode != protocol.ErrorCode_ErrorCode_Success || p.Name != "PlanA-Edited" || p.Preset.SkinEquipped != 302 {
		t.Fatal(edited)
	}
	reloaded := NewPlayerMotorcycle(s.Files)
	reloaded.EnsureInitialized(s)
	if len(reloaded.Presets) != 1 {
		t.Fatal(reloaded.Presets)
	}
	del := m.HandleDeletePreset(s, &protocol.MotorOutlookDeletePresetRequest{Id: 1})
	if del.ErrorCode != protocol.ErrorCode_ErrorCode_Success || len(del.MotorOutlookPreset.Plan) != 0 {
		t.Fatal(del)
	}
}

func TestMotorcycleRideSharingSpawnsAndRemovesPassengerOnSeat1(t *testing.T) {
	_, s, c := enterMotorRig(t)
	c.msgs = nil
	if HandleVehicleEnterSharing(s, &protocol.VehicleEnterSharingRequest{IsEnter: true}).ErrorCode != protocol.ErrorCode_ErrorCode_Success || len(ofType[*protocol.VehicleShareNotify](c)) != 1 {
		t.Fatal("share")
	}
	c.msgs = nil
	if s.Motorcycle().HandleVehicleChangePassenger(s, &protocol.VehicleChangePassengerRequest{RoleId: 1304, SeatId: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("change")
	}
	ups := ofType[*protocol.VehiclePassengerUpdateNotify](c)
	veh := ofType[*protocol.VehicleUpdateEntityNotify](c)
	if len(ups) != 1 || ups[0].RoleId != 1304 || ups[0].SeatId != 1 || len(veh) != 1 || !veh[0].IsGetOn || s.Scene.Get(ups[0].EntityId) == nil {
		t.Fatal(c.names())
	}
	c.msgs = nil
	if s.Motorcycle().HandleVehicleRemovePassenger(s, &protocol.VehicleRemovePassengerRequest{RoleId: 1304}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("remove")
	}
	veh = ofType[*protocol.VehicleUpdateEntityNotify](c)
	if s.Scene.Get(ups[0].EntityId) != nil || len(veh) != 1 || veh[0].IsGetOn {
		t.Fatal("still seated")
	}
}

func TestCustomEchoAndRemainingDebugCommandsWork(t *testing.T) {
	_, s, c := enterMotorRig(t)
	c.msgs = nil
	if len(PlannedCommands) != 0 {
		t.Fatal("planned")
	}
	if !s.RunCommands("echo 600010 5 a a 8 b 8") {
		t.Fatal(said(c))
	}
	adds := ofType[*protocol.PhantomItemAddNotify](c)
	if len(adds) != 1 || len(adds[0].PhantomItemList) != 1 {
		t.Fatal(adds)
	}
	e := adds[0].PhantomItemList[0]
	if e.Id != 600010 || e.PhantomLevel != 25 || e.FetterGroupId != 5 || len(e.PhantomMainProp) != 1 || len(e.PhantomSubProp) != 2 {
		t.Fatal(e)
	}
	if !s.RunCommands("set 1304 Life 1234; max 1304 Life; empty 1304 Energy; battle true; rf; photo true; photo false") {
		t.Fatal(said(c))
	}
	battle := slices.ContainsFunc(ofType[*protocol.PlayerBattleStateChangeNotify](c), func(n *protocol.PlayerBattleStateChangeNotify) bool { return n.InBattle })
	if !battle || len(ofType[*protocol.PhotoFightLevelInfoNotify](c)) == 0 || len(ofType[*protocol.BehaviorTreeDeleteNotify](c)) == 0 {
		t.Fatal(c.names())
	}
	if a, _, ok := attributeByWord("life"); !ok || a != combat.AttrLife {
		t.Fatal("case-insensitive attribute names")
	}
}
