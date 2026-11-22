// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// R-159: the pay shop (specs/shops.md §3).

const (
	tGift     = 51018 // 金库的波纹收藏: 50002, 50005, 120 星声
	tSkin     = 53009 // 蜜桃冰 (goods 106100007): role skin 81011508 + its head 82011508
	tOwnSkin  = 53003 // 长离's skin (goods 106100003), owned
	tMat4     = 43010004
	tMat2     = 41100061
	tPhase    = 44010029 // 异相: not sold
	tSkinGood = 106100007
)

func payRig(t *testing.T) (*rig, *PlayerSession, *recording) {
	r := newRig(t)
	tb := r.tables
	gift := func(id, pkg int32) *data.ItemInfo {
		return &data.ItemInfo{Id: id, ItemType: 11, MainTypeId: 7, QualityId: 5, Parameters: pairs(4, pkg)}
	}
	tb.ItemInfo = []*data.ItemInfo{
		{Id: 2, QualityId: 3}, {Id: 3, QualityId: 5}, {Id: 4, QualityId: 5},
		{Id: 50001, MainTypeId: 7, ItemType: 1, QualityId: 5}, {Id: 50002, MainTypeId: 7, ItemType: 1, QualityId: 5},
		{Id: 50005, MainTypeId: 7, ItemType: 1, QualityId: 5}, {Id: 50003, MainTypeId: 7, ItemType: 1, QualityId: 4},
		{Id: 50004, MainTypeId: 7, ItemType: 1, QualityId: 5}, {Id: 50009, MainTypeId: 7, ItemType: 1, QualityId: 5},
		{Id: tMat4, MainTypeId: 4, ItemType: 1, QualityId: 4}, {Id: tMat2, MainTypeId: 4, ItemType: 1, QualityId: 2},
		{Id: tPhase, MainTypeId: 4, ItemType: 1, QualityId: 5, ShowTypes: []int32{47}},
		{Id: MonthCardItemID, MainTypeId: 7, QualityId: 5},
		gift(tGift, 3020), gift(tSkin, 5009), gift(tOwnSkin, 5004),
	}
	tb.GiftPackage = []*data.GiftPackage{
		{Id: 3020, Content: pairs(50002, 1, 50005, 1, 3, 120)},
		{Id: 5009, Content: pairs(81011508, 1, 82011508, 1)},
		{Id: 5004, Content: pairs(81011205, 1, 82011205, 1)},
	}
	tb.RoleSkin = []*data.RoleSkin{{Id: 81011508, RoleId: 1508}, {Id: 81011205, RoleId: 1205}}
	tb.PlayerHeadRe = []*data.PlayerHeadRe{{Id: 82011508}, {Id: 82011205}}
	tb.PayShop = []*data.PayShop{
		{Id: 1, ShopTabViewType: 1, DynamicTabId: 34, Sort: 1, Money: []int32{4}, Enable: true},
		{Id: 3, ShopTabViewType: 3, DynamicTabId: 35, Sort: 3, Money: []int32{4}, Enable: true},
		{Id: 4, ShopTabViewType: 4, DynamicTabId: 36, Sort: 4, Money: []int32{50009, 50004, 50003, 3}, Enable: true},
		{Id: 6, ShopTabViewType: 9, DynamicTabId: 82, Sort: 3, Money: []int32{4}, Enable: true},
		{Id: 100, ShopTabViewType: 5, DynamicTabId: 37, Sort: 100, Money: []int32{4}, Enable: true},
		{Id: 5, ShopTabViewType: 6, DynamicTabId: 53, Sort: 101, Money: []int32{12}, Enable: true},
	}
	tb.PayShopTab = []*data.PayShopTab{
		{ShopId: 4, TabId: 1, Sort: 10, Name: "PayShopTab_4_1_Name", Logic: 2, Enable: true},
		{ShopId: 4, TabId: 3, Sort: 30, Name: "PayShopTab_4_3_Name", Logic: 2, Enable: true},
		{ShopId: 3, TabId: 1, Sort: 1, Name: "PayShopTab_3_1_Name", Logic: 3, Enable: true},
		{ShopId: 5, TabId: 1, Sort: 1, Name: "PayShopTab_5_1_Name", Logic: 1, Enable: true},
	}
	tb.PayShopRecommend = []*data.PayShopRecommend{
		{Id: 1, RecommendType: 1, TabName: "PrefabTextItem_3160938884_Text", Sort: 99, Show: true},
		{Id: 5, RecommendType: 3, RecommendId: 1, TabName: "PrefabTextItem_WeekCard_Text", Sort: 10, Show: true},
		{Id: 9, RecommendType: 2, RecommendId: tSkinGood, TabName: "PrefabTextItem_QianxiaoSkinRecommend_Text", Sort: 30, Show: true},
	}
	tb.PayItem = []*data.PayItem{{Id: 1, ItemId: 4, ItemCount: 60, PayId: 1}, {Id: 2, ItemId: 4, ItemCount: 300, PayId: 2, BonusItemCount: 30},
		{Id: 10000, ItemId: 4, ItemCount: 52, PayId: 10000}}
	tb.MonthCardContent = []*data.MonthCardContent{{Id: 1, Days: 30, ItemId: 4, Count: 300}}
	r.rebuild()
	r.saveLivePlayer()
	s, c := r.enter(false)
	cos := &s.Combat.Player.CosmeticInfo
	cos.RoleSkins = []int32{81011205}
	s.Actions().GrantItem(PayAstrite, 5000, false)
	c.msgs = nil
	return r, s, c
}

func payItemOf(info *protocol.PayShopInfoResponse, id int32) *protocol.PayShopItem {
	for _, shop := range info.Infos {
		for _, it := range shop.Items {
			if it.Id == id {
				return it
			}
		}
	}
	return nil
}

func TestPayShopInfoSendsFiveShopsChineseTabsAndRecommends(t *testing.T) {
	_, s, _ := payRig(t)
	info := s.PayShopInfo(&protocol.PayShopInfoRequest{Version: "1"})
	var ids []int32
	for _, shop := range info.Infos {
		ids = append(ids, shop.Id)
	}
	if !slices.Equal(ids, []int32{1, 3, 4, 6, 100}) || info.Infos[2].DynamicTabId != 36 {
		t.Fatal(ids)
	}
	names := map[int32]string{}
	for _, tab := range info.PayShopTabConfigs {
		if tab.ShopId == 4 {
			names[tab.TabId] = tab.Name
		}
		if tab.ShopId == 5 {
			t.Fatal("shop 5 is not sent")
		}
	}
	if names[3] != "聆听星声" || names[1] != "余波珊瑚" {
		t.Fatal(names)
	}
	shown := map[int32]bool{}
	for _, r := range info.PayShopRecommends {
		shown[r.Id] = r.Show
		if r.Id == 9 && r.TabName != "蜜桃冰" {
			t.Fatal(r.TabName)
		}
	}
	if !shown[1] || shown[5] || !shown[9] {
		t.Fatal("recommends", shown)
	}
	if g := info.PayGiftShopInfo.Gifts; len(g) != 1 || g[0].Id != MonthCardGiftID || g[0].Amount != "30" {
		t.Fatal(g)
	}
	// The skin banner's RecommendId is a good.
	if it := payItemOf(info, tSkinGood); it == nil || it.ShopId != PayShopSkins || it.TabId != 1 || it.Price.Count != 2480 || it.Price.Id != PayLunite {
		t.Fatal(it)
	}
}

func TestGiftCostsHalfItsWorthAtALuniteTierGivesContentsAndRefreshesWeekly(t *testing.T) {
	_, s, c := payRig(t)
	g := payItemOf(s.PayShopInfo(&protocol.PayShopInfoRequest{}), 103103001)
	// 160 + 160 + 120 = 440, half 220, nearest tier 300.
	if g == nil || g.ItemId != tGift || g.Price.Count != 300 || g.Price.Id != PayLunite || g.BuyLimit != 1 || g.UpdateType != payWeekly {
		t.Fatal(g)
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: 103103001, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrItemNotEnough {
		t.Fatal("no 月相 yet")
	}
	l0 := s.Actions().GrantItem(PayLunite, 1000, false)
	a0 := s.ItemCount(PayAstrite)
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: 103103001, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("buy")
	}
	if s.ItemCount(PayLunite) != l0-300 || s.ItemCount(50002) != 1 || s.ItemCount(50005) != 1 || s.ItemCount(PayAstrite) != a0+120 || s.ItemCount(tGift) != 0 {
		t.Fatal("contents", s.ItemCount(PayLunite), s.ItemCount(50002), s.ItemCount(PayAstrite)-a0, s.ItemCount(tGift))
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: 103103001, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsBuyLimit {
		t.Fatal("weekly limit")
	}
	if len(ofType[*protocol.NormalItemUpdateNotify](c))+len(ofType[*protocol.NormalItemAddNotify](c)) == 0 {
		t.Fatal("no item notifies")
	}
	s.Clock.Advance(7 * 24 * 3600 * 1000)
	if payItemOf(s.PayShopInfo(&protocol.PayShopInfoRequest{}), 103103001).BoughtCount != 0 ||
		s.PayShopBuy(&protocol.PayShopBuyRequest{Id: 103103001, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("next week")
	}
}

func TestExchangeSellsTicketsForAstriteAndMaterialsForCoral(t *testing.T) {
	_, s, _ := payRig(t)
	info := s.PayShopInfo(&protocol.PayShopInfoRequest{})
	ticket := payItemOf(info, TicketGoodsID(50002))
	coralTicket := payItemOf(info, ExchangeGoodsID(1, 50002))
	mat4 := payItemOf(info, ExchangeGoodsID(1, tMat4))
	mat2 := payItemOf(info, ExchangeGoodsID(2, tMat2))
	if ticket == nil || ticket.Price.Id != PayAstrite || ticket.Price.Count != 160 || ticket.BuyLimit != 0 {
		t.Fatal(ticket)
	}
	if coralTicket == nil || coralTicket.Price.Id != PayCoralA || coralTicket.Price.Count != 8 || coralTicket.BuyLimit != 5 || coralTicket.UpdateType != payMonthly {
		t.Fatal(coralTicket)
	}
	if mat4 == nil || mat4.Price.Count != 6 || mat4.BuyLimit != 20 || mat2 == nil || mat2.Price.Id != PayCoralO || mat2.Price.Count != 8 || mat2.BuyLimit != 100 {
		t.Fatal(mat4, mat2)
	}
	if payItemOf(info, ExchangeGoodsID(2, tMat4)) == nil || payItemOf(info, ExchangeGoodsID(1, tMat2)) != nil || payItemOf(info, ExchangeGoodsID(1, tPhase)) != nil {
		t.Fatal("quality ranges / 异相")
	}
	a0 := s.ItemCount(PayAstrite)
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: ticket.Id, Count: 3}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		s.ItemCount(PayAstrite) != a0-480 || s.ItemCount(50002) != 3 {
		t.Fatal("ticket")
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: mat4.Id, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrItemNotEnough || s.ItemCount(tMat4) != 0 {
		t.Fatal("no coral")
	}
	s.Actions().GrantItem(PayCoralA, 1000, false)
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: mat4.Id, Count: 21}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsBuyLimit ||
		s.PayShopBuy(&protocol.PayShopBuyRequest{Id: mat4.Id, Count: 20}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		s.ItemCount(PayCoralA) != 880 || s.ItemCount(tMat4) != 20 {
		t.Fatal("material limit")
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: 3, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists ||
		s.PayShopBuy(&protocol.PayShopBuyRequest{Id: ticket.Id, Count: 0}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrPayShopIllegalBuyCount {
		t.Fatal("bad requests")
	}
}

func TestSkinsAreOwnedOrBoughtOnceAndUnlocked(t *testing.T) {
	r, s, c := payRig(t)
	info := s.PayShopInfo(&protocol.PayShopInfoRequest{})
	if own := payItemOf(info, 106100003); own == nil || own.BoughtCount != 1 || own.BuyLimit != 1 {
		t.Fatal("owned skin shows sold out", own)
	}
	l0 := s.Actions().GrantItem(PayLunite, 3000, false)
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: 106100003, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsBuyLimit {
		t.Fatal("owned skin bought")
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: tSkinGood, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("buy skin")
	}
	cos := s.Combat.Player.CosmeticInfo
	if !slices.Contains(cos.RoleSkins, 81011508) || slices.Contains(cos.WeaponSkins, 82011508) || s.ItemCount(PayLunite) != l0-2480 {
		t.Fatal("unlock")
	}
	if last[*protocol.RoleSkinAddNotify](t, c).SkinIds[0] != 81011508 || len(ofType[*protocol.WeaponSkinAddNotify](c)) != 0 {
		t.Fatal("notifies")
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: tSkinGood, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsBuyLimit {
		t.Fatal("twice")
	}
	if !strings.Contains(r.read("pay_shop"), "b 106100007 1 0") {
		t.Fatal(r.read("pay_shop"))
	}
}

func TestLuniteTiersCostAstriteAndGiveTheirBonus(t *testing.T) {
	_, s, c := payRig(t)
	pi := s.PayInfo(&protocol.PayInfoRequest{})
	if len(pi.Infos) != 2 || pi.Infos[0].Amount != "6" || pi.Infos[1].Amount != "30" {
		t.Fatal(pi.Infos)
	}
	a0, l0 := s.ItemCount(PayAstrite), s.ItemCount(PayLunite)
	if s.PayItem(&protocol.PayItemRequest{Id: 2}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		s.ItemCount(PayAstrite) != a0-300 || s.ItemCount(PayLunite) != l0+330 {
		t.Fatal("tier 2")
	}
	if n := last[*protocol.PayItemSuccessNotify](t, c); n.Id != 2 || n.ItemCount != 330 {
		t.Fatal(n)
	}
	if s.PayItem(&protocol.PayItemRequest{Id: 10000}).ErrorCode == protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("other region's tier")
	}
}

func TestMonthCardGivesLuniteNowAndAstriteDaily(t *testing.T) {
	r, s, c := payRig(t)
	if mc := s.MonthCard(); mc.Days != -1 || mc.IsDailyGot {
		t.Fatal(mc)
	}
	a0, l0 := s.ItemCount(PayAstrite), s.ItemCount(PayLunite)
	if s.PayGift(&protocol.PayGiftRequest{Id: MonthCardGiftID}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("buy")
	}
	if s.ItemCount(PayAstrite) != a0-300+90 || s.ItemCount(PayLunite) != l0+300 {
		t.Fatal("rewards", s.ItemCount(PayAstrite)-a0, s.ItemCount(PayLunite)-l0)
	}
	names := c.names()
	daily, use := slices.Index(names, "MonthCardDailyRewardNotify"), slices.Index(names, "MonthCardUseNotify")
	if daily < 0 || use < daily || slices.Index(names, "PayGiftSuccessNotify") < use {
		t.Fatal(names)
	}
	if mc := s.MonthCard(); mc.Days != 29 || !mc.IsDailyGot {
		t.Fatal(mc)
	}
	if s.ClaimMonthCard() {
		t.Fatal("twice a day")
	}
	s.Clock.Advance(24 * 3600 * 1000)
	if !s.ClaimMonthCard() || s.MonthCard().Days != 28 || last[*protocol.MonthCardDailyRewardNotify](t, c).Days != 28 {
		t.Fatal("next day")
	}
	for range 4 {
		if s.PayGift(&protocol.PayGiftRequest{Id: MonthCardGiftID}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
			t.Fatal("stack")
		}
	}
	if s.MonthCard().Days != 148 || s.PayGift(&protocol.PayGiftRequest{Id: MonthCardGiftID}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		s.PayGift(&protocol.PayGiftRequest{Id: MonthCardGiftID}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrMonthCardDaysMax {
		t.Fatal("180 days", s.MonthCard().Days)
	}
	// Saved; the next login gives the day's reward.
	s.Clock.Advance(24 * 3600 * 1000)
	s2, c2 := r.enter(false)
	if s2.MonthCard().Days != 177 || !s2.MonthCard().IsDailyGot || len(ofType[*protocol.MonthCardDailyRewardNotify](c2)) != 1 {
		t.Fatal("login", s2.MonthCard().Days)
	}
}

func TestPayPeriodsResetAt4InUTC8(t *testing.T) {
	at := func(y int, m time.Month, d, h, min int) int64 {
		return time.Date(y, m, d, h, min, 0, 0, payZone).Unix()
	}
	b, e := PayPeriod(payMonthly, at(2026, 11, 1, 3, 59))
	if b != at(2026, 10, 1, 4, 0) || e != at(2026, 11, 1, 4, 0) {
		t.Fatal("before 04:00 on the 1st is last month")
	}
	if b, _ := PayPeriod(payMonthly, at(2026, 12, 31, 23, 0)); b != at(2026, 12, 1, 4, 0) {
		t.Fatal("december")
	}
	// 2026-11-23 is a Monday.
	if b, e := PayPeriod(payWeekly, at(2026, 11, 25, 12, 0)); b != at(2026, 11, 23, 4, 0) || e != at(2026, 11, 30, 4, 0) {
		t.Fatal("week")
	}
	if PayDay(at(2026, 11, 21, 3, 59)) != PayDay(at(2026, 11, 20, 4, 0)) || PayDay(at(2026, 11, 21, 4, 0)) != PayDay(at(2026, 11, 20, 4, 0))+1 {
		t.Fatal("day")
	}
	if b, e := PayPeriod(payForever, 1); b != 0 || e != 0 {
		t.Fatal("forever")
	}
}

// On the 3.8 data (skipped without it): every listed gift and skin resolves to a package, the
// shown skin banners find their goods and every tab of a sent shop has its Chinese name.
func TestPayCatalogOnLabData(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "ItemInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cat := buildPayCatalog(tb)
	per := map[[2]int32]int{}
	for _, g := range cat.Goods {
		per[[2]int32{g.Shop, g.Tab}]++
	}
	if per[[2]int32{3, 1}]+per[[2]int32{3, 2}]+per[[2]int32{3, 3}]+per[[2]int32{3, 4}]+per[[2]int32{3, 5}] != len(payGiftGoods) {
		t.Fatal("gifts", per)
	}
	if per[[2]int32{6, 1}]+per[[2]int32{6, 2}]+per[[2]int32{6, 3}]+per[[2]int32{6, 4}] != len(paySkinGoods) {
		t.Fatal("skins", per)
	}
	for _, r := range tb.PayShopRecommend {
		if r.Show && r.RecommendType == 2 && cat.ByID[r.RecommendId] == nil {
			t.Fatal("banner without a good", r.Id)
		}
		if r.Show && r.RecommendType != 3 && payText(r.TabName) == r.TabName {
			t.Fatal("untranslated banner", r.TabName)
		}
	}
	for _, row := range tb.PayShopTab {
		if slices.Contains(payShops, row.ShopId) && payText(row.Name) == row.Name {
			t.Fatal("untranslated tab", row.Name)
		}
	}
	for _, g := range cat.Goods {
		for _, c := range GiftContents(tb, g.ItemID) {
			_, item := tb.ItemInfoById[c.id]
			if !item && cosmeticKind(tb, c.id) == cosNone {
				t.Error("cannot give", c.id, "of", g.ItemID)
			}
		}
		if g.Shop == PayShopGifts || g.Shop == PayShopSkins {
			t.Logf("%d %d tab %d price %d %v", g.ID, g.ItemID, g.Tab, g.Price, g.Update)
		}
	}
	t.Logf("goods per (shop, tab): %v", per)
}

// R-159 fix: 月相 and 星声 are player attributes (CashCoin, RareCoin); the client shows those, so
// the shop checks and takes them there, and the bag's item rows 3/4 play no part.
func TestPayShopSpendsTheMoneyAttributesTheClientShows(t *testing.T) {
	_, s, c := payRig(t)
	attr := &s.Combat.Player.BasicInfo.Attributes
	s.Combat.Player.NormalInventory.SetBatch([]save.ItemCount{{ID: PayLunite, Count: 0}, {ID: PayAstrite, Count: 0}})
	attr.CashCoin, attr.RareCoin = 2480, 160
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: tSkinGood, Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_Success || attr.CashCoin != 0 {
		t.Fatal("skin from CashCoin", attr.CashCoin)
	}
	cash := last[*protocol.PlayerAttrNotify](t, c).Attributes[0]
	if cash.Key != protocol.PlayerAttrKey_PlayerAttrKey_CashCoin || cash.GetInt32Value() != 0 {
		t.Fatal(cash)
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: TicketGoodsID(50002), Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_Success ||
		attr.RareCoin != 0 || last[*protocol.PlayerAttrNotify](t, c).Attributes[0].Key != protocol.PlayerAttrKey_PlayerAttrKey_RareCoin {
		t.Fatal("ticket from RareCoin", attr.RareCoin)
	}
	if s.PayShopBuy(&protocol.PayShopBuyRequest{Id: TicketGoodsID(50002), Count: 1}).ErrorCode != protocol.ErrorCode_ErrorCode_ErrItemNotEnough {
		t.Fatal("no 星声 left")
	}
}

// Gift cards need a 728-wide picture: without ShowStageImage the client stretches the gift's
// square icon, and most gifts share one icon (T_IconGiftPack_dj_19).
func TestEveryGiftHasACardPicture(t *testing.T) {
	for _, g := range payGiftGoods {
		if payGiftImages[g.item] == "" {
			t.Error("no picture for gift", g.item)
		}
	}
	if GiftImage("060") != "/Game/Aki/UI/UIResources/Common/Image/IconGiftPack728/T_IconGiftPack728_060_UI.T_IconGiftPack728_060_UI" || GiftImage("") != "" {
		t.Fatal(GiftImage("060"))
	}
	_, s, _ := payRig(t)
	g := &PayGood{ID: 1, Shop: PayShopGifts, Tab: 1, ItemID: tGift, Count: 1, Money: PayLunite, Price: 60, Image: GiftImage("060")}
	it, mc := s.payShopItem(g, 0), s.monthCardGift()
	if it.StageImage != g.Image || it.ShowStageImage != g.Image || mc.StageImage != MonthCardImage || mc.ShowStageImage != MonthCardImage {
		t.Fatal("not sent")
	}
}

// The gacha view's exchange button opens the good named by CommonParam
// GachaItemIdToShopId; a missing good hangs the client's ExchangePopView (R-168).
func TestTicketGoodsUseTheGachaExchangeIDs(t *testing.T) {
	_, s, _ := payRig(t)
	info := s.PayShopInfo(&protocol.PayShopInfoRequest{})
	for item, id := range map[int32]int32{50002: 104300001, 50001: 104300003} {
		g := payItemOf(info, id)
		if g == nil || g.ItemId != item || g.Price.Id != PayAstrite || g.Price.Count != 160 || TicketGoodsID(item) != id {
			t.Fatal(item, id, g)
		}
	}
}
