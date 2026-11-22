// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/protocol"
)

// R-160b: the content panel over the pay shop and the gacha banners (specs/panel.md §4, §5).

func TestThePanelChangesAGoodsPriceLimitAndPeriodAndCanTakeItOffSale(t *testing.T) {
	r, s, _ := payRig(t)
	o := panel.Open(r.state)
	s.Data.Panel = o
	id := TicketGoodsID(50001)
	before := s.ItemCount(PayAstrite)
	if err := o.SetGood(id, panel.Good{Price: 100, Limit: 2, Update: int32(protocol.PayUpdateType_PayUpdateType_Weekly)}, "t"); err != nil {
		t.Fatal(err)
	}
	it := payItemOf(s.PayShopInfo(&protocol.PayShopInfoRequest{}), id)
	if it == nil || it.Price.Count != 100 || it.BuyLimit != 2 || it.UpdateType != protocol.PayUpdateType_PayUpdateType_Weekly || it.UpdateTime == 0 {
		t.Fatal(it)
	}
	if c := s.PayShopBuy(&protocol.PayShopBuyRequest{Id: id, Count: 2}).ErrorCode; c != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal(c)
	}
	if got := before - s.ItemCount(PayAstrite); got != 200 {
		t.Fatal("paid", got)
	}
	if c := s.PayShopBuy(&protocol.PayShopBuyRequest{Id: id, Count: 1}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsBuyLimit {
		t.Fatal(c)
	}
	// Only the price left as the catalogue's: Auto keeps it.
	o.SetGood(id, panel.Good{Price: panel.Auto, Limit: 0, Update: panel.Auto}, "t")
	if it := payItemOf(s.PayShopInfo(&protocol.PayShopInfoRequest{}), id); it.Price.Count != 160 || it.BuyLimit != 0 {
		t.Fatal(it)
	}
	o.SetGood(id, panel.Good{Off: true, Price: panel.Auto, Limit: panel.Auto, Update: panel.Auto}, "t")
	if payItemOf(s.PayShopInfo(&protocol.PayShopInfoRequest{}), id) != nil {
		t.Fatal("off sale is not listed")
	}
	if c := s.PayShopBuy(&protocol.PayShopBuyRequest{Id: id, Count: 1}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists {
		t.Fatal(c)
	}
	if u := s.PayShopItemUpdate(&protocol.PayShopItemUpdateRequest{ShopItemIds: []int32{id}}); len(u.Items) != 0 {
		t.Fatal(u.Items)
	}
	o.ResetGood(id, "t")
	if it := payItemOf(s.PayShopInfo(&protocol.PayShopInfoRequest{}), id); it == nil || it.Price.Count != 160 {
		t.Fatal(it)
	}
}

func limitedGachaRig(t *testing.T) (*PlayerSession, *panel.Overrides) {
	r, s, _ := enterShopRig(t)
	tb := r.tables
	tb.Gacha = append(tb.Gacha, &data.Gacha{Id: 100001, RuleGroupId: 1, Sort: 20}, &data.Gacha{Id: 100002, RuleGroupId: 1, Sort: 20},
		&data.Gacha{Id: 200001, RuleGroupId: 2, Sort: 30})
	tb.GachaPool = append(tb.GachaPool, &data.GachaPool{Id: 1000011, GachaId: 100001, Sort: 1}, &data.GachaPool{Id: 1000021, GachaId: 100002, Sort: 1},
		&data.GachaPool{Id: 2000011, GachaId: 200001, Sort: 1})
	tb.GachaViewInfo = append(tb.GachaViewInfo,
		&data.GachaViewInfo{Id: 1000011, Type: 2, ShowIdList: []int32{1404}, UpList: []int32{1602}},
		&data.GachaViewInfo{Id: 1000021, Type: 2, ShowIdList: []int32{1302}, UpList: []int32{1601}},
		&data.GachaViewInfo{Id: 2000011, Type: 3, ShowIdList: []int32{21010016}, UpList: []int32{21010044}})
	tb.Reindex()
	o := panel.Open(r.state)
	s.Data.Panel = o
	return s, o
}

func gachaIDs(s *PlayerSession) []int32 {
	var ids []int32
	for _, g := range s.Gacha().Info(s).GachaInfos {
		ids = append(ids, g.Id)
	}
	return ids
}

func TestLimitedBannersAreClosedUntilThePanelOpensOne(t *testing.T) {
	s, o := limitedGachaRig(t)
	eq(t, GachaSlotBanners(s.Data.Tables, panel.GachaRole), []int32{100002, 100001})
	eq(t, GachaSlotBanners(s.Data.Tables, panel.GachaWeapon), []int32{200001})
	eq(t, gachaIDs(s), []int32{2, 3})
	// A banner not listed cannot be pulled (the novice banner 1 included).
	for _, id := range []int32{1, 100001} {
		if c := s.Gacha().Pull(s, &protocol.GachaRequest{GachaId: id, GachaTimes: 1}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrGachaIsNotOpen {
			t.Fatal(id, c)
		}
	}
	o.SetGacha(panel.GachaRole, 100001, "t")
	o.SetGacha(panel.GachaWeapon, 200001, "t")
	info := s.Gacha().Info(s).GachaInfos
	eq(t, gachaIDs(s), []int32{100001, 200001, 2, 3})
	if info[0].ItemId != 50002 || info[1].ItemId != 50005 || info[2].ItemId != 50001 || info[0].Pools[0].UIType != 2 {
		t.Fatal(info[0].ItemId, info[1].ItemId, info[2].ItemId)
	}
	// An id that is not a banner of the slot is ignored.
	o.SetGacha(panel.GachaWeapon, 100002, "t")
	eq(t, gachaIDs(s), []int32{100001, 2, 3})
	o.ResetGacha(panel.GachaRole, "t")
	eq(t, gachaIDs(s), []int32{2, 3})
}

func TestALimitedBannerPullsWithItsTicketAndSharesPityWithItsKind(t *testing.T) {
	s, o := limitedGachaRig(t)
	o.SetGacha(panel.GachaRole, 100001, "t")
	if c := s.Gacha().Pull(s, &protocol.GachaRequest{GachaId: 100001, GachaTimes: 1}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrItemNotEnough {
		t.Fatal("pulls with 50002, not 50001:", c)
	}
	s.Actions().GrantItem(50002, 10, false)
	before := s.ItemCount(50001)
	if c := s.Gacha().Pull(s, &protocol.GachaRequest{GachaId: 100001, GachaTimes: 10}).ErrorCode; c != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal(c)
	}
	if s.ItemCount(50002) != 0 || s.ItemCount(50001) != before {
		t.Fatal(s.ItemCount(50002), s.ItemCount(50001))
	}
	bucket, _ := s.Gacha().GetOrCreateBucket(1)
	bucket.PullCount, bucket.GuaranteeFive = 42, true
	// Another resonator banner of the same rule group keeps the pity.
	o.SetGacha(panel.GachaRole, 100002, "t")
	s.Gacha().Info(s)
	if b, _ := s.Gacha().GetOrCreateBucket(1); b.PullCount != 42 || !b.GuaranteeFive {
		t.Fatal(b)
	}
}

func TestAPullOnlyGivesWhatTheConveneAnimationCanShow(t *testing.T) {
	tb := (&data.DataTables{
		WeaponConf:       []*data.WeaponConf{{ItemId: 21020044, QualityId: 4}, {ItemId: 21020053, QualityId: 4}, {ItemId: 21030076, QualityId: 5}, {ItemId: 21010013, QualityId: 3}},
		GachaTextureInfo: []*data.GachaTextureInfo{{Id: 21020044}, {Id: 21030076}, {Id: 21010013}},
	}).Reindex()
	up5, _, std5, std4, std3 := gachaCandidates(tb, &data.GachaViewInfo{Type: 3, ShowIdList: []int32{21030076}, UpList: []int32{21020053}})
	eq(t, up5, []int32{21030076})
	eq(t, std5, []int32{21030076})
	eq(t, std4, []int32{21020044})
	eq(t, std3, []int32{21010013})
}
