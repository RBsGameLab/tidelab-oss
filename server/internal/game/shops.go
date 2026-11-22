// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"slices"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// ----- NPC and fixed shops (R-80h, R-108, R-109, R-116; specs/shops.md §2–§3) -----

// PlayerShops is the ShopFixed shops and the purchase history (player/<id>/shop_bought,
// "<goodsId> <count>" lines). Ported from our C# Game/Shops.cs.
type PlayerShops struct {
	path   string
	order  []int32
	bought map[int32]int32
}

const MaxWeaponCopiesPerPurchase = 5

func NewPlayerShops(files *save.PlayerFiles) *PlayerShops {
	s := &PlayerShops{path: files.PathOf("shop_bought"), bought: map[int32]int32{}}
	for _, f := range readLineFile(s.path) {
		id, ok1 := lineInt(f, 0)
		count, ok2 := lineInt(f, 1)
		if !ok1 || !ok2 || count <= 0 {
			continue
		}
		s.set(id, count)
	}
	return s
}

func (s *PlayerShops) Bought(goodsID int32) int32 { return s.bought[goodsID] }

func (s *PlayerShops) AddBought(goodsID, count int32) int32 {
	if count <= 0 {
		return s.Bought(goodsID)
	}
	next := s.Bought(goodsID) + count
	s.set(goodsID, next)
	var sb strings.Builder
	for _, id := range s.order {
		fmt.Fprintf(&sb, "%d %d\n", id, s.bought[id])
	}
	save.WriteAtomic(s.path, sb.String())
	return next
}

func (s *PlayerShops) set(goodsID, count int32) {
	if _, ok := s.bought[goodsID]; !ok {
		s.order = append(s.order, goodsID)
	}
	s.bought[goodsID] = count
}

func moneyList(m *data.Map[int32, int32]) []*protocol.ShoppMoneyInfo {
	var out []*protocol.ShoppMoneyInfo
	if m == nil {
		return out
	}
	for _, p := range m.Pairs {
		out = append(out, &protocol.ShoppMoneyInfo{MoneyId: p.Key, MoneyNum: p.Value})
	}
	return out
}

func (s *PlayerShops) BuildItem(row *data.ShopFixed) *protocol.ShopItemInfoNew {
	return &protocol.ShopItemInfoNew{
		Id: row.Id, BoughtCount: s.Bought(row.Id), Lock: false, ItemId: row.ItemId, ItemNum: row.ItemNum,
		CondText: "", BeginTime: 0, EndTime: 0, LimitNum: row.LimitNum, Label: row.Label,
		SwitchText: row.SwitchText, PurchaseText: row.PurchaseText, UnlockConditionId: 0, SortIndex: row.SortIndex,
		MoneyList: moneyList(row.Price), OriginalMoneyList: moneyList(row.OriginalPrice),
	}
}

func (s *PlayerShops) BuildShop(t *data.DataTables, shopID int32) *protocol.ShopInfo {
	info := &protocol.ShopInfo{ShopId: shopID, UpdateTime: 0}
	for _, row := range t.ShopFixed {
		if row.ShopId == shopID {
			info.ItemInfoList = append(info.ItemInfoList, s.BuildItem(row))
		}
	}
	if len(info.ItemInfoList) == 0 {
		return nil
	}
	return info
}

func (s *PlayerShops) BuildAllShops(t *data.DataTables) []*protocol.ShopInfo {
	byShop := map[int32]*protocol.ShopInfo{}
	var list []*protocol.ShopInfo
	for _, row := range t.ShopFixed {
		shop, ok := byShop[row.ShopId]
		if !ok {
			shop = &protocol.ShopInfo{ShopId: row.ShopId, UpdateTime: 0}
			byShop[row.ShopId] = shop
			list = append(list, shop)
		}
		shop.ItemInfoList = append(shop.ItemInfoList, s.BuildItem(row))
	}
	return list
}

func (s *PlayerShops) Buy(p *PlayerSession, r *protocol.ShopBuyRequest) *protocol.ShopBuyResponse {
	t := p.Data.Tables
	row, ok := t.ShopFixedById[r.Id]
	if !ok || row.ShopId != r.ShopId {
		return &protocol.ShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrShopFixeNotExist, ShopId: r.ShopId, Id: r.Id}
	}
	if r.Num <= 0 {
		return &protocol.ShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrShopIllegalBuyCount, ShopId: r.ShopId, Id: r.Id}
	}
	bought := s.Bought(r.Id)
	if row.LimitNum > 0 && bought+r.Num > row.LimitNum {
		return &protocol.ShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrShopNumLimit, ShopId: r.ShopId, Id: r.Id, BoughtCount: bought}
	}
	notEnough := &protocol.ShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemNotEnough, ShopId: r.ShopId, Id: r.Id, BoughtCount: bought}
	var price []data.Pair[int32, int32]
	if row.Price != nil {
		price = row.Price.Pairs
	}
	// 1. Validate currencies.
	for _, pr := range price {
		total := int64(pr.Value) * int64(r.Num)
		if total <= 0 {
			continue
		}
		if total > 1<<31-1 {
			return notEnough
		}
		if p.ItemCount(pr.Key) < int32(total) {
			return notEnough
		}
	}
	// 2. Deduct currencies.
	for _, pr := range price {
		tp := pr.Value * r.Num
		if tp <= 0 {
			continue
		}
		p.Actions().TakeItem(pr.Key, &tp)
	}
	// 3. Deliver the purchased item (R-108, R-109, R-116).
	GrantWeaponOrItem(p, row.ItemId, max(row.ItemNum, 1)*r.Num)
	// 4. Record the purchase in shop_bought.
	return &protocol.ShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ShopId: r.ShopId, Id: r.Id, BoughtCount: s.AddBought(r.Id, r.Num)}
}

// GrantWeaponOrItem grants either weapon copies (capped at 5, never written to inventory_info,
// R-116) or a normal item in ItemInfo (R-108, R-109).
func GrantWeaponOrItem(p *PlayerSession, itemID, count int32) {
	if count <= 0 {
		return
	}
	t := p.Data.Tables
	player := p.Combat.Player
	if wconf, ok := t.WeaponConfByItemId[itemID]; ok {
		copies := min(max(count, 1), MaxWeaponCopiesPerPurchase)
		notify := &protocol.WeaponItemAddNotify{}
		for i := int32(0); i < copies; i++ {
			fv := int32(1)
			if wconf.QualityId >= 5 {
				fv = 0
			}
			w := &save.WeaponSave{UniqueID: player.SpecialItemIDs.Next(), ID: itemID, Level: 1, Exp: 0, Breach: 0, ResonLevel: 1, FuncValue: fv}
			player.Weapons.Set(w.UniqueID, w)
			save.SaveWeapon(player, w)
			notify.WeaponItemList = append(notify.WeaponItemList, &protocol.WeaponItem{
				Id: w.ID, IncrId: w.UniqueID, FuncValue: w.FuncValue, WeaponLevel: w.Level, WeaponExp: w.Exp,
				WeaponBreach: w.Breach, WeaponResonLevel: w.ResonLevel,
			})
		}
		p.Sink().Push(notify)
		return
	}
	if _, ok := t.ItemInfoById[itemID]; !ok {
		common.Warn("shop", fmt.Sprintf("item %d is neither in WeaponConf nor in ItemInfo; not granted", itemID))
		return
	}
	p.Actions().GrantItem(itemID, count, false)
}

// ----- manufacture (specs/shops.md §4) -----

const (
	DefaultSynthesisLevel = 6
	DefaultCookingLevel   = 6
)

func SynthesisInfo(p *PlayerSession) *protocol.SynthesisInfoResponse {
	resp := &protocol.SynthesisInfoResponse{
		Code: protocol.ErrorCode_ErrorCode_Success, LimitRefreshTime: 0,
		LevelInfo: &protocol.SynthesisLevelInfo{Level: maxLevelFromJSON(p, "SynthesisLevel.json", DefaultSynthesisLevel), TotalProficiency: 0},
	}
	for _, f := range p.Data.Tables.SynthesisFormula {
		resp.SynthesisInfoList = append(resp.SynthesisInfoList, &protocol.OneSynthesisInfo{Id: f.Id})
		resp.SynthesisConfigs = append(resp.SynthesisConfigs, &protocol.OneSynthesisConfig{Id: f.Id})
	}
	return resp
}

func SynthesisItem(p *PlayerSession, r *protocol.SynthesisItemRequest) *protocol.SynthesisItemResponse {
	f, ok := p.Data.Tables.SynthesisFormulaById[r.Id]
	if r.Count <= 0 || !ok {
		return &protocol.SynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrSynthesisConfigNotFound, Id: r.Id, RoleId: r.RoleId}
	}
	if !hasEnoughConsumes(p, f.ConsumeItems, r.Count) {
		return &protocol.SynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemNotEnough, Id: r.Id, RoleId: r.RoleId}
	}
	takeConsumes(p, f.ConsumeItems, r.Count)
	GrantWeaponOrItem(p, f.ItemId, r.Count)
	return &protocol.SynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_Success, Id: r.Id, RoleId: r.RoleId, ActiveSkillType: 0,
		ItemInfos: []*protocol.SingleItemInfo{{ItemId: f.ItemId, ItemNum: r.Count}}}
}

// itemTotals sums counts per item in first-seen order (C#'s Dictionary).
type itemTotals struct {
	order []int32
	n     map[int32]int32
}

func (it *itemTotals) add(id, count int32) {
	if it.n == nil {
		it.n = map[int32]int32{}
	}
	if _, ok := it.n[id]; !ok {
		it.order = append(it.order, id)
	}
	it.n[id] += count
}

func (it *itemTotals) enough(p *PlayerSession) bool {
	for _, id := range it.order {
		if p.ItemCount(id) < it.n[id] {
			return false
		}
	}
	return true
}

func (it *itemTotals) take(p *PlayerSession) {
	for _, id := range it.order {
		c := it.n[id]
		p.Actions().TakeItem(id, &c)
	}
}

type itemCount struct{ id, count int32 }

func BatchSynthesisItem(p *PlayerSession, r *protocol.BatchSynthesisItemRequest) *protocol.BatchSynthesisItemResponse {
	var needed itemTotals
	var outputs []itemCount
	for _, entry := range r.BatchItems {
		f, ok := p.Data.Tables.SynthesisFormulaById[entry.Id]
		if entry.Count <= 0 || !ok {
			return &protocol.BatchSynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrSynthesisConfigNotFound}
		}
		for _, c := range f.ConsumeItems {
			if c == nil || c.ItemId <= 0 || c.Count <= 0 {
				continue
			}
			needed.add(c.ItemId, c.Count*entry.Count)
		}
		outputs = append(outputs, itemCount{f.ItemId, entry.Count})
	}
	if !needed.enough(p) {
		return &protocol.BatchSynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	needed.take(p)
	resp := &protocol.BatchSynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_Success, Items: map[int32]int32{}}
	for _, o := range outputs {
		GrantWeaponOrItem(p, o.id, o.count)
		resp.Items[o.id] += o.count
	}
	return resp
}

func SynthesisLevelReward(p *PlayerSession, _ *protocol.SynthesisLevelRewardRequest) *protocol.SynthesisLevelRewardResponse {
	maxLevel := maxLevelFromJSON(p, "SynthesisLevel.json", DefaultSynthesisLevel)
	if arr, ok := p.Data.ReadJSON("SynthesisLevel.json").(*data.Array); ok {
		for _, item := range arr.Items {
			if o, ok := item.(*data.Object); ok {
				if drop, ok := jsonIntOf(o.Get("DropIds")); ok && drop > 0 {
					p.Actions().GrantDrop(drop, 0, 1, nil)
				}
			}
		}
	}
	return &protocol.SynthesisLevelRewardResponse{Code: protocol.ErrorCode_ErrorCode_Success, LevelInfo: &protocol.SynthesisLevelInfo{Level: maxLevel, TotalProficiency: 0}}
}

func SynthesisFormulaUnlock(p *PlayerSession, r *protocol.SynthesisFormulaUnlockRequest) *protocol.SynthesisFormulaUnlockResponse {
	p.Sink().Push(&protocol.SynthesisInfoUpdateNotify{SynthesisInfoList: []*protocol.OneSynthesisInfo{{Id: r.Id}}})
	return &protocol.SynthesisFormulaUnlockResponse{Code: protocol.ErrorCode_ErrorCode_Success, Id: r.Id}
}

func CookingData(p *PlayerSession) *protocol.CookingDataResponse {
	resp := &protocol.CookingDataResponse{
		Code: protocol.ErrorCode_ErrorCode_Success, LimitRefreshTime: 0,
		CookingInfo: &protocol.CookingInfo{CookingLevel: maxLevelFromJSON(p, "CookLevel.json", DefaultCookingLevel), TotalProficiencys: 0},
	}
	for _, f := range p.Data.Tables.CookFormula {
		resp.FoodFormulaInfos = append(resp.FoodFormulaInfos, &protocol.SingleFoodFormulaInfo{Id: f.Id})
		resp.FormulaConfigs = append(resp.FormulaConfigs, &protocol.OneFormulaConfig{Id: f.Id})
	}
	for _, pr := range p.Data.Tables.CookProcessed {
		resp.ProcessedFoodFormulaInfos = append(resp.ProcessedFoodFormulaInfos, &protocol.SingleProcessedFoodFormulaInfo{
			Id: pr.Id, LockState: false, Interations: append([]int32(nil), pr.InterationId...),
		})
	}
	return resp
}

func CookFood(p *PlayerSession, r *protocol.CookFoodRequest) *protocol.CookFoodResponse {
	if r.CookCount <= 0 {
		return &protocol.CookFoodResponse{Code: protocol.ErrorCode_ErrorCode_ErrCookingCount, Id: r.Id, RoleId: r.RoleId}
	}
	var consumes []*data.OneItemConfig
	var out int32
	if f, ok := p.Data.Tables.CookFormulaById[r.Id]; ok {
		consumes, out = f.ConsumeItems, f.FoodItemId
	} else if pr, ok := p.Data.Tables.CookProcessedById[r.Id]; ok {
		consumes, out = pr.ConsumeItemsId, pr.FinalItemId
	} else {
		return &protocol.CookFoodResponse{Code: protocol.ErrorCode_ErrorCode_ErrCookingFormulaNotFound, Id: r.Id, RoleId: r.RoleId}
	}
	if !hasEnoughConsumes(p, consumes, r.CookCount) {
		return &protocol.CookFoodResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemNotEnough, Id: r.Id, RoleId: r.RoleId}
	}
	takeConsumes(p, consumes, r.CookCount)
	GrantWeaponOrItem(p, out, r.CookCount)
	return &protocol.CookFoodResponse{Code: protocol.ErrorCode_ErrorCode_Success, Id: r.Id, RoleId: r.RoleId, ActiveSkillType: 0,
		ItemInfos: []*protocol.SingleItemInfo{{ItemId: out, ItemNum: r.CookCount}}}
}

func CookFormulaUnlock(p *PlayerSession, r *protocol.CookFormulaRequest) *protocol.CookFormulaResponse {
	p.Sink().Push(&protocol.CookingFormulaUpdateNotify{FoodFormulaInfos: []*protocol.SingleFoodFormulaInfo{{Id: r.CookFormulaId}}})
	return &protocol.CookFormulaResponse{Code: protocol.ErrorCode_ErrorCode_Success, CookFormulaId: r.CookFormulaId}
}

func ForgeInfo(p *PlayerSession) *protocol.ForgeInfoResponse {
	resp := &protocol.ForgeInfoResponse{Code: protocol.ErrorCode_ErrorCode_Success, LimitRefreshTime: 0}
	for _, f := range p.Data.Tables.ForgeFormula {
		resp.ForgeInfoList = append(resp.ForgeInfoList, &protocol.OneForgeInfo{Id: f.Id})
		resp.ForgeConfigs = append(resp.ForgeConfigs, &protocol.OneForgeConfig{Id: f.Id})
	}
	return resp
}

func ForgeItem(p *PlayerSession, r *protocol.ForgeItemRequest) *protocol.ForgeItemResponse {
	f, ok := p.Data.Tables.ForgeFormulaById[r.Id]
	if r.Count <= 0 || !ok {
		return &protocol.ForgeItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrForgeConfigNotFound, Id: r.Id, RoleId: r.RoleId}
	}
	if !hasEnoughConsumes(p, f.ConsumeItems, r.Count) {
		return &protocol.ForgeItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemNotEnough, Id: r.Id, RoleId: r.RoleId}
	}
	takeConsumes(p, f.ConsumeItems, r.Count)
	GrantWeaponOrItem(p, f.ItemId, r.Count)
	return &protocol.ForgeItemResponse{Code: protocol.ErrorCode_ErrorCode_Success, Id: r.Id, RoleId: r.RoleId, ActiveSkillType: 0,
		ItemInfos: []*protocol.SingleItemInfo{{ItemId: f.ItemId, ItemNum: r.Count}}}
}

func ForgeFormulaUnlock(p *PlayerSession, r *protocol.ForgeFormulaUnlockRequest) *protocol.ForgeFormulaUnlockResponse {
	p.Sink().Push(&protocol.ForgeItemInfoUpdateNotify{ForgeInfoList: []*protocol.OneForgeInfo{{Id: r.Id}}})
	return &protocol.ForgeFormulaUnlockResponse{Code: protocol.ErrorCode_ErrorCode_Success, Id: r.Id}
}

// replaceRatio is how many of consumeID make one targetID (R-158): both MaterialReplace rows of
// one GroupId, ratio ExchangeParam(target.ExchangeParam).Target.Item2, else 2 (client
// ComposeModel.GetExchangeCountByData).
func replaceRatio(t *data.DataTables, consumeID, targetID int32) (int32, bool) {
	c, okC := t.MaterialReplaceByItemId[consumeID]
	tg, okT := t.MaterialReplaceByItemId[targetID]
	if !okC || !okT || consumeID == targetID || c.GroupId != tg.GroupId {
		return 0, false
	}
	if e, ok := t.ExchangeParamById[tg.ExchangeParam]; ok && e.Target != nil && e.Target.Item2 > 0 {
		return e.Target.Item2, true
	}
	return 2, true
}

// sortedKeys: a request's proto map in key order (C# read it in wire order, which Go's decoder
// does not keep).
func sortedKeys(m map[int32]int32) []int32 {
	keys := make([]int32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// BatchMaterialReplace: each entry turns its ConsumeItems into sum(count / ratio) of the target
// (client ComposeController.SendBatchMaterialReplaceRequest); all entries or none.
func BatchMaterialReplace(p *PlayerSession, r *protocol.BatchMaterialReplaceRequest) *protocol.BatchMaterialReplaceResponse {
	t := p.Data.Tables
	var needed itemTotals
	var outputs []itemCount
	for _, item := range r.ReplaceItems {
		var out int32
		for _, id := range sortedKeys(item.ConsumeItems) {
			count := item.ConsumeItems[id]
			ratio, ok := replaceRatio(t, id, item.TargetReplaceItemId)
			if count <= 0 || !ok {
				return &protocol.BatchMaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemInvalidParams}
			}
			needed.add(id, count)
			out += count / ratio
		}
		if out <= 0 {
			return &protocol.BatchMaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemInvalidParams}
		}
		outputs = append(outputs, itemCount{item.TargetReplaceItemId, out})
	}
	if !needed.enough(p) {
		return &protocol.BatchMaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	needed.take(p)
	for _, o := range outputs {
		GrantWeaponOrItem(p, o.id, o.count)
	}
	return &protocol.BatchMaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

// MaterialReplace takes ConsumeItemNum and gives ConsumeItemNum / ratio of the target (client
// ComposeController.SendExchangeItemRequest).
func MaterialReplace(p *PlayerSession, r *protocol.MaterialReplaceRequest) *protocol.MaterialReplaceResponse {
	ratio, ok := replaceRatio(p.Data.Tables, r.ConsumeItemId, r.TargetReplaceItemId)
	if r.ConsumeItemNum <= 0 || !ok || r.ConsumeItemNum/ratio <= 0 {
		return &protocol.MaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemInvalidParams}
	}
	if p.ItemCount(r.ConsumeItemId) < r.ConsumeItemNum {
		return &protocol.MaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	n := r.ConsumeItemNum
	p.Actions().TakeItem(r.ConsumeItemId, &n)
	GrantWeaponOrItem(p, r.TargetReplaceItemId, r.ConsumeItemNum/ratio)
	return &protocol.MaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

// ItemExchange (月相 → 星声, 星声 → passes): ExchangeTimes × the item's ItemExchangeContent row
// (Consume taken, GainCount given). 3.8 has one row (Times 1) per item, so the price does not
// rise with the times exchanged; the daily/total limits are not kept.
func ItemExchange(p *PlayerSession, r *protocol.ItemExchangeRequest) *protocol.ItemExchangeResponse {
	var row *data.ItemExchangeContent
	for _, c := range p.Data.Tables.ItemExchangeContent {
		if c.ItemId == r.ItemId && c.Times <= 1 && (row == nil || c.Times > row.Times) {
			row = c
		}
	}
	if r.ExchangeTimes <= 0 {
		return &protocol.ItemExchangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemExchageParamError}
	}
	if row == nil || row.GainCount <= 0 {
		return &protocol.ItemExchangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemExchageConfigNotFound}
	}
	var needed itemTotals
	if row.Consume != nil {
		for _, c := range row.Consume.Pairs {
			if c.Value > 0 {
				needed.add(c.Key, c.Value*r.ExchangeTimes)
			}
		}
	}
	if !needed.enough(p) {
		return &protocol.ItemExchangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	needed.take(p)
	gain := row.GainCount * r.ExchangeTimes
	GrantWeaponOrItem(p, r.ItemId, gain)
	return &protocol.ItemExchangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ItemId: r.ItemId, ItemCount: gain}
}

// hasEnoughConsumes: money (贝币 in forge formulas) is counted from the attributes (R-158), and
// an item listed twice adds up.
func hasEnoughConsumes(p *PlayerSession, consumes []*data.OneItemConfig, times int32) bool {
	var needed itemTotals
	for _, c := range consumes {
		if c == nil || c.ItemId <= 0 || c.Count <= 0 {
			continue
		}
		needed.add(c.ItemId, c.Count*times)
	}
	return needed.enough(p)
}

func takeConsumes(p *PlayerSession, consumes []*data.OneItemConfig, times int32) {
	for _, c := range consumes {
		if c == nil || c.ItemId <= 0 || c.Count <= 0 {
			continue
		}
		n := c.Count * times
		p.Actions().TakeItem(c.ItemId, &n)
	}
}

// jsonIntOf is C#'s LevelEntities.Int: a whole number in int range, or an int string.
func jsonIntOf(v data.Value) (int32, bool) {
	switch x := v.(type) {
	case data.Number:
		f := float64(x)
		if f == float64(int64(f)) && f >= -2147483648 && f <= 2147483647 {
			return int32(f), true
		}
	case data.String:
		if n, ok := parseSigned(trimNet(string(x)), 32); ok {
			return int32(n), true
		}
	}
	return 0, false
}

func maxLevelFromJSON(p *PlayerSession, file string, fallback int32) int32 {
	arr, ok := p.Data.ReadJSON(file).(*data.Array)
	if !ok {
		return fallback
	}
	var best int32
	for _, item := range arr.Items {
		if o, ok := item.(*data.Object); ok {
			if id, ok := jsonIntOf(o.Get("Id")); ok && id > best {
				best = id
			}
		}
	}
	return max(best, fallback)
}
