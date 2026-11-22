// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"os"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/protocol"
)

// Pay shop, NPC shops, manufacture, material replacement and gacha (specs/shops.md §1–§5).
// Ported from our C# ShopAndGachaTests.

func enterShopRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	tb.ItemInfo = []*data.ItemInfo{
		{Id: 2, MainTypeId: 0, QualityId: 1}, {Id: 3, MainTypeId: 0, QualityId: 5}, {Id: 4, MainTypeId: 0, QualityId: 5},
		{Id: 50001, MainTypeId: 7, QualityId: 5}, {Id: 50002, MainTypeId: 7, QualityId: 5}, {Id: 50003, MainTypeId: 7, QualityId: 5},
		{Id: 50004, MainTypeId: 7, QualityId: 4}, {Id: 50005, MainTypeId: 7, QualityId: 5},
		{Id: 10001102, MainTypeId: 7, ItemType: 1, QualityId: 5},
		{Id: 41400144, MainTypeId: 4, ItemType: 1, QualityId: 2}, {Id: 41400145, MainTypeId: 4, ItemType: 1, QualityId: 3},
		{Id: 42300010, MainTypeId: 1, ItemType: 3, QualityId: 2}, {Id: 42600010, MainTypeId: 5, ItemType: 32, QualityId: 3},
		{Id: 70010001, MainTypeId: 6, ItemType: 8, QualityId: 1}, // quest item: not sold
	}
	tb.WeaponConf = []*data.WeaponConf{
		{ItemId: 21010013, QualityId: 3, ResonLevelLimit: 5}, {ItemId: 21010024, QualityId: 4, ResonLevelLimit: 5},
		{ItemId: 21010015, QualityId: 5, ResonLevelLimit: 5}, {ItemId: 210011, QualityId: 5, ResonLevelLimit: 5},
		{ItemId: 210012, QualityId: 5, ResonLevelLimit: 5}, {ItemId: 210013, QualityId: 5, ResonLevelLimit: 5},
	}
	tb.RoleInfo = []*data.RoleInfo{
		{Id: 1304, RoleType: 1, QualityId: 5, SpilloverItem: pairs(50003, 15)},
		{Id: 1202, RoleType: 1, QualityId: 4, SpilloverItem: pairs(50003, 3)},
	}
	tb.ShopFixed = []*data.ShopFixed{
		{Id: 1001, ShopId: 10, SortIndex: 1, ItemId: 41400144, ItemNum: 2, LimitNum: 3, Price: pairs(3, 10), OriginalPrice: pairs(3, 10)},
		{Id: 1002, ShopId: 10, SortIndex: 2, ItemId: 21010024, ItemNum: 1, LimitNum: 0, Price: pairs(3, 5)},
	}
	tb.SynthesisFormula = []*data.SynthesisFormula{{Id: 1, ItemId: 41400145, ConsumeItems: []*data.OneItemConfig{{ItemId: 41400144, Count: 3}}}}
	tb.CookFormula = []*data.CookFormula{{Id: 11, FoodItemId: 42300010, ConsumeItems: []*data.OneItemConfig{{ItemId: 41400144, Count: 1}}}}
	tb.CookProcessed = []*data.CookProcessed{{Id: 21, FinalItemId: 42600010, ConsumeItemsId: []*data.OneItemConfig{{ItemId: 41400144, Count: 2}}, InterationId: []int32{101}}}
	tb.ForgeFormula = []*data.ForgeFormula{{Id: 31, ItemId: 21010024, ConsumeItems: []*data.OneItemConfig{{ItemId: 41400144, Count: 2}, {ItemId: 2, Count: 1000}}}}
	tb.MaterialReplace = []*data.MaterialReplace{{ItemId: 41400144, GroupId: 7, ExchangeParam: 1}, {ItemId: 41400145, GroupId: 7, ExchangeParam: 1}}
	tb.ExchangeParam = []*data.ExchangeParam{{Id: 1, Target: &data.IntPair{Item1: 0, Item2: 3}}}
	tb.ItemExchangeContent = []*data.ItemExchangeContent{{ItemId: 50002, Times: 1, GainCount: 1, Consume: pairs(3, 160)}}
	tb.Gacha = []*data.Gacha{{Id: 1, RuleGroupId: 1, Sort: 10}, {Id: 2, RuleGroupId: 3, Sort: 50}, {Id: 3, RuleGroupId: 4, Sort: 60}}
	tb.GachaPool = []*data.GachaPool{{Id: 1, GachaId: 1, Sort: 10}, {Id: 2, GachaId: 2, Sort: 50}, {Id: 31, GachaId: 3, Sort: 65}, {Id: 32, GachaId: 3, Sort: 66}}
	tb.GachaViewInfo = []*data.GachaViewInfo{
		{Id: 1, Type: 1, SummaryTitle: "Beginner"},
		{Id: 2, Type: 4, SummaryTitle: "PermanentRole", UpList: []int32{1304, 1202}},
		{Id: 31, Type: 5, SummaryTitle: "PermanentWeapon1", UpList: []int32{21010015, 21010024}},
		{Id: 32, Type: 5, SummaryTitle: "PermanentWeapon2", UpList: []int32{21010015, 21010024}},
	}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enter(false)
	s.Actions().GrantItem(3, 500, false)
	s.Actions().GrantItem(50001, 20, false)
	s.Actions().GrantItem(41400144, 30, false)
	c.msgs = nil
	return r, s, c
}

func TestFixedShopPersistsBoughtCountsEnforcesLimitAndRoutesWeaponsToWeaponFiles(t *testing.T) {
	r, s, c := enterShopRig(t)
	s.PushShopInfo()
	if len(last[*protocol.ShopInfoNotify](t, c).ShopList) != 1 {
		t.Fatal("shops")
	}
	buy := s.ShopBuy(&protocol.ShopBuyRequest{ShopId: 10, Id: 1001, Num: 2})
	if buy.ErrorCode != protocol.ErrorCode_ErrorCode_Success || buy.BoughtCount != 2 || s.ItemCount(41400144) != 34 {
		t.Fatal(buy)
	}
	if !strings.Contains(r.read("shop_bought"), "1001 2") {
		t.Fatal(r.read("shop_bought"))
	}
	if s.ShopBuy(&protocol.ShopBuyRequest{ShopId: 10, Id: 1001, Num: 2}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrShopNumLimit {
		t.Fatal("limit")
	}
	before := s.Combat.Player.Weapons.Len()
	if s.ShopBuy(&protocol.ShopBuyRequest{ShopId: 10, Id: 1002, Num: 7}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("weapon")
	}
	if s.Combat.Player.Weapons.Len() != before+5 || s.ItemCount(21010024) != 0 || len(last[*protocol.WeaponItemAddNotify](t, c).WeaponItemList) != 5 {
		t.Fatal("weapon copies")
	}
}

func TestManufactureAndMaterialReplacementConsumeAndGrantItems(t *testing.T) {
	_, s, _ := enterShopRig(t)
	inv := s.Combat.Player.NormalInventory
	ok := protocol.ErrorCode_ErrorCode_Success
	if len(SynthesisInfo(s).SynthesisInfoList) != 1 {
		t.Fatal("synthesis info")
	}
	if SynthesisItem(s, &protocol.SynthesisItemRequest{Id: 1, Count: 2, RoleId: 1304}).Code != ok || inv.CountOf(41400145) != 2 {
		t.Fatal("synthesis")
	}
	if BatchSynthesisItem(s, &protocol.BatchSynthesisItemRequest{BatchItems: []*protocol.BatchItem{{Id: 1, Count: 1}}}).Code != ok || inv.CountOf(41400145) != 3 {
		t.Fatal("batch")
	}
	if CookFood(s, &protocol.CookFoodRequest{Id: 11, CookCount: 2, RoleId: 1304}).Code != ok || inv.CountOf(42300010) != 2 {
		t.Fatal("cook")
	}
	if CookFood(s, &protocol.CookFoodRequest{Id: 21, CookCount: 1, RoleId: 1304}).Code != ok || inv.CountOf(42600010) != 1 {
		t.Fatal("processed")
	}
	before := s.Combat.Player.Weapons.Len()
	attr := &s.Combat.Player.BasicInfo.Attributes
	attr.Coin = 999
	if ForgeItem(s, &protocol.ForgeItemRequest{Id: 31, Count: 1, RoleId: 1304}).Code != protocol.ErrorCode_ErrorCode_ErrItemNotEnough || s.Combat.Player.Weapons.Len() != before {
		t.Fatal("forge short of 贝币 (from the attribute)")
	}
	attr.Coin = 1000
	if ForgeItem(s, &protocol.ForgeItemRequest{Id: 31, Count: 1, RoleId: 1304}).Code != ok || s.Combat.Player.Weapons.Len() != before+1 {
		t.Fatal("forge")
	}
	rep := BatchMaterialReplace(s, &protocol.BatchMaterialReplaceRequest{ReplaceItems: []*protocol.BatchReplaceItem{{
		TargetReplaceItemId: 41400145, TargetNum: 1, ConsumeItems: map[int32]int32{41400144: 3},
	}}})
	if rep.Code != ok || inv.CountOf(41400145) != 4 || attr.Coin != 0 {
		t.Fatal("replace 3 : 1")
	}
	have := inv.CountOf(41400144)
	if MaterialReplace(s, &protocol.MaterialReplaceRequest{ConsumeItemId: 41400145, TargetReplaceItemId: 41400144, ConsumeItemNum: 3}).Code != ok ||
		inv.CountOf(41400145) != 1 || inv.CountOf(41400144) != have+1 {
		t.Fatal("single replace 3 : 1")
	}
	if MaterialReplace(s, &protocol.MaterialReplaceRequest{ConsumeItemId: 41400145, TargetReplaceItemId: 41400144, ConsumeItemNum: 2}).Code != protocol.ErrorCode_ErrorCode_ErrItemInvalidParams {
		t.Fatal("fewer than the ratio")
	}
	attr.RareCoin = 639
	if ItemExchange(s, &protocol.ItemExchangeRequest{ItemId: 50002, ExchangeTimes: 4}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrItemNotEnough || inv.CountOf(50002) != 0 {
		t.Fatal("exchange short")
	}
	attr.RareCoin = 640
	if ItemExchange(s, &protocol.ItemExchangeRequest{ItemId: 50002, ExchangeTimes: 4}).ErrorCode != ok || inv.CountOf(50002) != 4 || attr.RareCoin != 0 {
		t.Fatal("exchange")
	}
}

func TestGachaFiltersToPermanentBannersSavesPoolChoiceAndPullsTen(t *testing.T) {
	r, s, _ := enterShopRig(t)
	info := s.Gacha().Info(s)
	var ids []int32
	for _, g := range info.GachaInfos {
		ids = append(ids, g.Id)
	}
	eq(t, ids, []int32{2, 3})
	if s.Gacha().UsePool(s, &protocol.GachaUsePoolRequest{GachaId: 3, PoolId: 32}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("use pool")
	}
	used := slices.IndexFunc(s.Gacha().Info(s).GachaInfos, func(g *protocol.GachaInfo) bool { return g.Id == 3 && g.UsePoolId == 32 })
	if used < 0 {
		t.Fatal("pool choice")
	}
	if d := GachaPoolDetail(s, &protocol.GachaPoolDetailRequest{PoolId: 32}); d.ErrorCode != protocol.ErrorCode_ErrorCode_Success || len(d.Detail.FiveStarWeaponList) == 0 {
		t.Fatal(d)
	}
	pull := s.Gacha().Pull(s, &protocol.GachaRequest{GachaId: 3, GachaTimes: 10})
	if pull.ErrorCode != protocol.ErrorCode_ErrorCode_Success || len(pull.GachaResults) != 10 || s.ItemCount(50001) != 10 {
		t.Fatal(pull)
	}
	if _, err := os.Stat(r.playerFile("gacha_info")); err != nil {
		t.Fatal(err)
	}
}
