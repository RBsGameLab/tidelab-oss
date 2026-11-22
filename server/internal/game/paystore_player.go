// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"slices"
	"strings"

	"tidelab/internal/save"
	"tidelab/protocol"
)

// ----- R-159: the player's side of the pay shop (specs/shops.md §3.5–§3.9) -----

// PlayerPayShop is player/<id>/pay_shop: "b <goods> <count> <periodBegin>" per bought good and
// "m <lastDay> <claimedDay>" for the month card (game days, PayDay; no line = no card).
type PlayerPayShop struct {
	path      string
	bought    map[int32]payBought
	order     []int32
	cardLast  int64 // last day with a daily reward; valid when hasCard
	cardClaim int64 // last day the daily reward was given
	hasCard   bool
}

type payBought struct {
	count int32
	begin int64
}

func NewPlayerPayShop(files *save.PlayerFiles) *PlayerPayShop {
	s := &PlayerPayShop{path: files.PathOf("pay_shop"), bought: map[int32]payBought{}}
	for _, f := range readLineFile(s.path) {
		switch f[0] {
		case "b":
			id, ok1 := lineInt(f, 1)
			n, ok2 := lineInt(f, 2)
			begin, ok3 := lineLong(f, 3)
			if ok1 && ok2 && ok3 && n > 0 {
				s.setBought(id, payBought{n, begin})
			}
		case "m":
			last, ok1 := lineLong(f, 1)
			claim, ok2 := lineLong(f, 2)
			if ok1 && ok2 {
				s.cardLast, s.cardClaim, s.hasCard = last, claim, true
			}
		}
	}
	return s
}

func (s *PlayerPayShop) setBought(id int32, b payBought) {
	if _, ok := s.bought[id]; !ok {
		s.order = append(s.order, id)
	}
	s.bought[id] = b
}

func (s *PlayerPayShop) save() {
	var sb strings.Builder
	for _, id := range s.order {
		b := s.bought[id]
		fmt.Fprintf(&sb, "b %d %d %d\n", id, b.count, b.begin)
	}
	if s.hasCard {
		fmt.Fprintf(&sb, "m %d %d\n", s.cardLast, s.cardClaim)
	}
	save.WriteAtomic(s.path, sb.String())
}

// Bought is how many of g were bought in the current period.
func (s *PlayerPayShop) Bought(g *PayGood, now int64) int32 {
	b, ok := s.bought[g.ID]
	if !ok {
		return 0
	}
	if begin, _ := PayPeriod(g.Update, now); begin != b.begin {
		return 0
	}
	return b.count
}

func (s *PlayerPayShop) addBought(g *PayGood, n int32, now int64) {
	begin, _ := PayPeriod(g.Update, now)
	s.setBought(g.ID, payBought{s.Bought(g, now) + n, begin})
	s.save()
}

// CardDays is the month card's remaining days after today (the client's RemainDays: -1 none,
// 0 today is the last).
func (s *PlayerPayShop) CardDays(now int64) int32 {
	if !s.hasCard || s.cardLast < PayDay(now) {
		return -1
	}
	return int32(s.cardLast - PayDay(now))
}

func (p *PlayerSession) PayShop() *PlayerPayShop {
	if p.payShop == nil {
		p.payShop = NewPlayerPayShop(p.Files)
	}
	return p.payShop
}

// ----- §3.5 PayShopInfo / PayShopUpdate -----

func (p *PlayerSession) payShopItem(g *PayGood, now int64) *protocol.PayShopItem {
	bought := p.PayShop().Bought(g, now)
	if g.Skin && bought == 0 && p.ownsCosmetics(GiftContents(p.Data.Tables, g.ItemID)) {
		bought = 1 // owned: shown as sold out (已拥有)
	}
	item := &protocol.PayShopItem{
		Id: g.ID, ItemId: g.ItemID, ItemCount: g.Count, BuyLimit: g.Limit, BoughtCount: bought,
		Price: &protocol.PayShopPrice{Id: g.Money, Count: g.Price}, UpdateType: g.Update,
		ShopItemType: protocol.PayShopItemType_PayShopItemType_Normal, CanBuyGoods: true, TabId: g.Tab, ShopId: g.Shop,
		Sort: g.Sort, Show: true, Quality: g.Quality, OnceBuyLimit: PayMaxBuy, SoldoutShowInShop: true,
		IsShowHaveNum: g.Shop == PayShopExchange, IsBuyMaxButton: g.Shop == PayShopExchange, StageImage: g.Image, ShowStageImage: g.Image,
	}
	if g.Limit > 0 {
		item.OnceBuyLimit = g.Limit
	}
	if _, end := PayPeriod(g.Update, now); end > 0 {
		item.UpdateTime = end
	}
	return item
}

func (p *PlayerSession) payShopInfo(id int32, now int64) *protocol.PayShopInfo {
	t := p.Data.Tables
	row, ok := t.PayShopById[id]
	if !ok {
		return &protocol.PayShopInfo{Id: id}
	}
	info := &protocol.PayShopInfo{Id: id, ShopTabViewType: row.ShopTabViewType, DynamicTabId: row.DynamicTabId, Sort: row.Sort,
		Money: slices.Clone(row.Money), SortRule: row.SortRule}
	for _, g := range p.payGoods().Goods {
		if g.Shop == id {
			info.Items = append(info.Items, p.payShopItem(g, now))
		}
	}
	return info
}

// payTabMoney is a tab's currency bar: the coral tabs show their coral, the rest the shop's.
func payTabMoney(shop, tab int32, shopMoney []int32) []int32 {
	if shop == PayShopExchange {
		switch tab {
		case 1:
			return []int32{PayCoralA}
		case 2:
			return []int32{PayCoralO}
		case 3:
			return []int32{PayAstrite}
		case 4:
			return []int32{PayCoralD}
		}
	}
	return slices.Clone(shopMoney)
}

func (p *PlayerSession) payShopTabs() []*protocol.PayShopTabConfigInfo {
	t := p.Data.Tables
	var out []*protocol.PayShopTabConfigInfo
	for _, row := range t.PayShopTab {
		shop, ok := t.PayShopById[row.ShopId]
		if !ok || !slices.Contains(payShops, row.ShopId) {
			continue
		}
		out = append(out, &protocol.PayShopTabConfigInfo{ShopId: row.ShopId, TabId: row.TabId, Sort: row.Sort,
			Name: payText(row.Name), Logic: row.Logic, Enable: row.Enable, Money: payTabMoney(row.ShopId, row.TabId, shop.Money)})
	}
	return out
}

// payRecommends are PayShopRecommend as configured; the week card (type 3) is hidden (no week card).
func (p *PlayerSession) payRecommends() []*protocol.PayShopRecommendConfigInfo {
	var out []*protocol.PayShopRecommendConfigInfo
	for _, r := range p.Data.Tables.PayShopRecommend {
		out = append(out, &protocol.PayShopRecommendConfigInfo{Id: r.Id, RecommendType: r.RecommendType, RecommendId: r.RecommendId,
			TabName: payText(r.TabName), PrefabPath: r.PrefabPath, Sort: r.Sort, Show: r.Show && r.RecommendType != 3, TabImage: r.RecommendTexture})
	}
	return out
}

// monthCardGift is the month card as the client's PayGift (MonthCardView buys it with SdkPay).
func (p *PlayerSession) monthCardGift() *protocol.PayGiftInfo {
	return &protocol.PayGiftInfo{Id: MonthCardGiftID, PayId: MonthCardPayID, ItemId: MonthCardItemID, ItemCount: 1, Sort: 1,
		Amount: itoa(MonthCardYuan), TabId: 1, Type: 1, IsCanBuy: true, ShopId: PayShopGifts, Quality: 5,
		UpdateType: protocol.PayUpdateType_PayUpdateType_None, StageImage: MonthCardImage, ShowStageImage: MonthCardImage}
}

func (p *PlayerSession) PayShopInfo(r *protocol.PayShopInfoRequest) *protocol.PayShopInfoResponse {
	now := p.Clock.Seconds()
	resp := &protocol.PayShopInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Version: r.Version,
		PayShopTabConfigs: p.payShopTabs(), PayShopRecommends: p.payRecommends(),
		PayGiftShopInfo: &protocol.PayGiftShopInfo{Version: "1", Gifts: []*protocol.PayGiftInfo{p.monthCardGift()}}}
	if resp.Version == "" {
		resp.Version = "1"
	}
	for _, id := range payShops {
		resp.Infos = append(resp.Infos, p.payShopInfo(id, now))
	}
	return resp
}

func (p *PlayerSession) PayShopUpdate(r *protocol.PayShopUpdateRequest) *protocol.PayShopUpdateResponse {
	if !slices.Contains(payShops, r.Id) {
		return &protocol.PayShopUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPayShopNotExists}
	}
	return &protocol.PayShopUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Info: p.payShopInfo(r.Id, p.Clock.Seconds())}
}

func (p *PlayerSession) PayShopItemUpdate(r *protocol.PayShopItemUpdateRequest) *protocol.PayShopItemUpdateResponse {
	resp := &protocol.PayShopItemUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	cat := p.payGoods()
	for _, id := range r.ShopItemIds {
		if g, ok := cat.ByID[id]; ok {
			resp.Items = append(resp.Items, p.payShopItem(g, p.Clock.Seconds()))
		}
	}
	return resp
}

// ----- §3.6 PayShopBuy -----

func (p *PlayerSession) PayShopBuy(r *protocol.PayShopBuyRequest) *protocol.PayShopBuyResponse {
	fail := func(code protocol.ErrorCode) *protocol.PayShopBuyResponse {
		return &protocol.PayShopBuyResponse{ErrorCode: code, Id: r.Id, Count: r.Count}
	}
	t := p.Data.Tables
	g, ok := p.payGoods().ByID[r.Id]
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists)
	}
	if r.Count < 1 || r.Count > PayMaxBuy {
		return fail(protocol.ErrorCode_ErrorCode_ErrPayShopIllegalBuyCount)
	}
	now := p.Clock.Seconds()
	contents := GiftContents(t, g.ItemID)
	if g.Limit > 0 && p.PayShop().Bought(g, now)+r.Count > g.Limit || g.Skin && p.ownsCosmetics(contents) {
		return fail(protocol.ErrorCode_ErrorCode_ErrPayShopGoodsBuyLimit)
	}
	cost := int64(g.Price) * int64(r.Count)
	if cost > 1<<31-1 || int64(p.ItemCount(g.Money)) < cost {
		return fail(protocol.ErrorCode_ErrorCode_ErrItemNotEnough)
	}
	c := int32(cost)
	p.Actions().TakeItem(g.Money, &c)
	if contents != nil {
		for i := int32(0); i < r.Count; i++ {
			p.grantContents(contents)
		}
	} else {
		GrantWeaponOrItem(p, g.ItemID, g.Count*r.Count)
	}
	p.PayShop().addBought(g, r.Count, now)
	return &protocol.PayShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Id: r.Id, Count: r.Count}
}

// grantContents gives a gift package's contents: cosmetics are unlocked, the rest goes to the
// bag (decision 5: buying a gift gives what is in it, not the gift item).
func (p *PlayerSession) grantContents(contents []itemCount) {
	t := p.Data.Tables
	cos := &p.Combat.Player.CosmeticInfo
	changed := false
	unlock := func(list *[]int32, id int32) bool {
		if slices.Contains(*list, id) {
			return false
		}
		*list = append(*list, id)
		changed = true
		return true
	}
	for _, c := range contents {
		switch cosmeticKind(t, c.id) {
		case cosRole:
			if unlock(&cos.RoleSkins, c.id) {
				p.Sink().Push(&protocol.RoleSkinAddNotify{SkinIds: []int32{c.id}})
			}
		case cosWeapon:
			if unlock(&cos.WeaponSkins, c.id) {
				p.Sink().Push(&protocol.WeaponSkinAddNotify{SkinIds: []int32{c.id}})
			}
		case cosFly:
			if unlock(&cos.FlySkins, c.id) {
				p.Sink().Push(&protocol.FlyEquipAddNotify{FlySkins: []int32{c.id}})
			}
		case cosOrnament:
			if unlock(&cos.Ornaments, c.id) {
				p.Sink().Push(&protocol.OrnamentUnlockNotify{UnlockOrnamentIds: []int32{c.id}})
			}
		case cosMotor, cosHead, cosCard:
			// Motor looks, heads and cards are all owned from the start (PlayerMotorcycle.seedDefaults,
			// PlayerHead, BuildBasicInfo).
		default:
			GrantWeaponOrItem(p, c.id, c.count)
		}
	}
	if changed {
		save.SaveCosmeticInfo(p.Combat.Player)
	}
}

// ownsCosmetics: the contents hold at least one cosmetic and the player owns all of them.
func (p *PlayerSession) ownsCosmetics(contents []itemCount) bool {
	t := p.Data.Tables
	cos := &p.Combat.Player.CosmeticInfo
	any := false
	for _, c := range contents {
		var owned bool
		switch cosmeticKind(t, c.id) {
		case cosNone, cosHead, cosCard:
			continue
		case cosRole:
			owned = slices.Contains(cos.RoleSkins, c.id)
		case cosWeapon:
			owned = slices.Contains(cos.WeaponSkins, c.id)
		case cosFly:
			owned = slices.Contains(cos.FlySkins, c.id)
		case cosOrnament:
			owned = slices.Contains(cos.Ornaments, c.id)
		case cosMotor:
			m := p.Motorcycle()
			m.ensureLoaded(t)
			owned = slices.Contains(m.OwnedSkins, c.id) || slices.Contains(m.OwnedStickers, c.id) ||
				slices.Contains(m.OwnedDecorations, c.id) || slices.Contains(m.OwnedFrames, c.id)
		}
		if !owned {
			return false
		}
		any = true
	}
	return any
}

// ----- §3.7 凝刻月相: PayInfo / PayItem (real money paid in 星声) -----

// payTiers are the PayItem rows shown: the six mainland tiers 60…6480.
var payItemIDs = []int32{1, 2, 3, 4, 5, 6}

func (p *PlayerSession) PayInfo(r *protocol.PayInfoRequest) *protocol.PayInfoResponse {
	resp := &protocol.PayInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Version: "1"}
	for _, id := range payItemIDs {
		row, ok := p.Data.Tables.PayItemById[id]
		if !ok {
			continue
		}
		resp.Infos = append(resp.Infos, &protocol.PayItemInfo{Id: row.Id, PayId: row.PayId, ItemId: row.ItemId, ItemCount: row.ItemCount,
			BonusItemCount: row.BonusItemCount, StageImage: row.StageImage, Amount: itoa(row.ItemCount / AstritePerYuan), Quality: 5})
	}
	return resp
}

func (p *PlayerSession) PayItem(r *protocol.PayItemRequest) *protocol.PayItemResponse {
	row, ok := p.Data.Tables.PayItemById[r.Id]
	if !ok || !slices.Contains(payItemIDs, r.Id) {
		return &protocol.PayItemResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists}
	}
	cost := row.ItemCount // ¥ x 10 星声 = the Lunite count
	if p.ItemCount(PayAstrite) < cost {
		return &protocol.PayItemResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	p.Actions().TakeItem(PayAstrite, &cost)
	total := row.ItemCount + row.BonusItemCount
	p.Actions().GrantItem(row.ItemId, total, true)
	receipt := p.payReceipt()
	p.Sink().Push(&protocol.PayItemSuccessNotify{Id: row.Id, ReceiptId: receipt, ItemId: row.ItemId, ItemCount: total})
	return &protocol.PayItemResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ReceiptId: receipt}
}

func (p *PlayerSession) payReceipt() string {
	return fmt.Sprintf("tidelab-%d", p.Clock.Seconds())
}

// ----- §3.8 the month card -----

func (p *PlayerSession) PayGiftInfo(*protocol.PayGiftInfoRequest) *protocol.PayGiftInfoResponse {
	return &protocol.PayGiftInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Version: "1", Infos: []*protocol.PayGiftInfo{p.monthCardGift()}}
}

func (p *PlayerSession) PayGift(r *protocol.PayGiftRequest) *protocol.PayGiftResponse {
	if r.Id != MonthCardGiftID {
		return &protocol.PayGiftResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists}
	}
	now := p.Clock.Seconds()
	ps := p.PayShop()
	days := ps.CardDays(now)
	if days+1+MonthCardDays > MonthCardMaxDays {
		return &protocol.PayGiftResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrMonthCardDaysMax}
	}
	cost := int32(MonthCardYuan * AstritePerYuan)
	if p.ItemCount(PayAstrite) < cost {
		return &protocol.PayGiftResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	p.Actions().TakeItem(PayAstrite, &cost)
	today := PayDay(now)
	if days < 0 {
		ps.cardLast, ps.cardClaim, ps.hasCard = today+MonthCardDays-1, today-1, true
	} else {
		ps.cardLast += MonthCardDays
	}
	ps.save()
	p.ClaimMonthCard() // today's 90 星声 when not given yet; before the use notify (its reward view reads it)
	once, onceCount := int32(PayLunite), int32(300)
	if row, ok := p.Data.Tables.MonthCardContentById[1]; ok {
		once, onceCount = row.ItemId, row.Count
	}
	p.Actions().GrantItem(once, onceCount, true)
	p.Sink().Push(&protocol.MonthCardUseNotify{ItemId: once, Count: onceCount, Days: ps.CardDays(now),
		DailyRewardItemId: PayAstrite, DailyRewardItemCount: MonthCardDaily, ExtendedDays: MonthCardDays})
	receipt := p.payReceipt()
	p.Sink().Push(&protocol.PayGiftSuccessNotify{Id: MonthCardGiftID, ReceiptId: receipt, ItemId: MonthCardItemID, ItemCount: 1, PayGiftInfo: p.monthCardGift()})
	return &protocol.PayGiftResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ReceiptId: receipt}
}

func (p *PlayerSession) MonthCard() *protocol.MonthCardResponse {
	now := p.Clock.Seconds()
	ps := p.PayShop()
	return &protocol.MonthCardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Days: ps.CardDays(now), IsDailyGot: ps.hasCard && ps.cardClaim == PayDay(now)}
}

// ClaimMonthCard gives today's 90 星声 once a day while the card runs (at login and on purchase)
// and pushes MonthCardDailyRewardNotify, which opens the client's claim popup. Missed days are lost.
func (p *PlayerSession) ClaimMonthCard() bool {
	now := p.Clock.Seconds()
	ps := p.PayShop()
	today := PayDay(now)
	if ps.CardDays(now) < 0 || ps.cardClaim >= today {
		return false
	}
	ps.cardClaim = today
	ps.save()
	p.Actions().GrantItem(PayAstrite, MonthCardDaily, true)
	p.Sink().Push(&protocol.MonthCardDailyRewardNotify{ItemId: PayAstrite, Count: MonthCardDaily, Days: ps.CardDays(now)})
	return true
}
