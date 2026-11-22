// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"slices"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

const (
	DefaultMaxEchoCost = 12
	MaxProjectorSlots  = 8
)

var seedGachaCurrencies = []int32{50001, 50002, 50005, 50006, 50007, 50008}

// InventoryService is the normal items (R-80e, R-108 snapshot and log; R-109, R-116 ItemInfo
// filter and stray weapons), weapons, echoes, presets, calabash and the echo projector
// (specs/inventory.md).
type InventoryService struct {
	tables       *data.DataTables
	player       *save.PlayerData
	scene        *CombatSceneState
	buffBuilder  *RoleBuffBuilder
	concomitants *ConcomitantResolver
	formation    *FormationService
	roles        *RoleService
	buffs        *BuffManager
	rng          Rng
}

func NewInventoryService(t *data.DataTables, p *save.PlayerData, s *CombatSceneState, bb *RoleBuffBuilder, c *ConcomitantResolver,
	f *FormationService, r *RoleService, b *BuffManager, rng Rng) *InventoryService {
	if rng == nil {
		rng = SystemRng{}
	}
	return &InventoryService{t, p, s, bb, c, f, r, b, rng}
}

func (v *InventoryService) MaxEchoCost() int32 {
	if len(v.tables.CalabashLevel) == 0 {
		return DefaultMaxEchoCost
	}
	m := v.tables.CalabashLevel[0].Cost
	for _, c := range v.tables.CalabashLevel {
		m = max(m, c.Cost)
	}
	return max(DefaultMaxEchoCost, m)
}

func funcValueOf(quality int32) int32 {
	if quality >= 5 {
		return 0
	}
	return 1
}

// SeedNewAccount gives a new account its weapons, echoes, calabash levels, normal items and
// cosmetics (specs/inventory.md §1).
func (v *InventoryService) SeedNewAccount(seedDefaultEchoes bool) {
	t, p := v.tables, v.player
	// 1. Weapons (player/<id>/weapon/ missing).
	if !p.WeaponDirExistedOnLoad && p.Weapons.Len() == 0 {
		for _, wc := range t.WeaponConf {
			uid := p.SpecialItemIDs.Next()
			w := save.NewWeapon(uid)
			w.ID, w.FuncValue = wc.ItemId, funcValueOf(wc.QualityId)
			p.Weapons.Set(uid, w)
			save.SaveWeapon(p, w)
		}
	}
	// 2. Echoes (player/<id>/echo/ empty) and the calabash's rewarded levels.
	if !p.EchoDirHadEntriesOnLoad && p.Echoes.Len() == 0 && seedDefaultEchoes {
		for _, it := range t.PhantomItem {
			if !IsSeedableEcho(t, it) {
				continue
			}
			for _, fg := range it.FetterGroup {
				uid := p.SpecialItemIDs.Next()
				e := CreateLevel0Echo(t, uid, it, fg, false, v.rng)
				if e == nil {
					continue
				}
				p.Echoes.Set(uid, e)
				save.SaveEcho(p, e)
			}
		}
	}
	if len(p.Calabash.RewardedLevels) == 0 && len(t.CalabashLevel) > 0 {
		for _, r := range t.CalabashLevel {
			if !slices.Contains(p.Calabash.RewardedLevels, r.Level) {
				p.Calabash.RewardedLevels = append(p.Calabash.RewardedLevels, r.Level)
			}
		}
		save.SaveCalabash(p)
	}
	// 3. Normal items (inventory_info empty).
	if p.NormalInventory.Items().Len() == 0 {
		ids := map[int32]bool{}
		collect := func(id int32) {
			if id > 6 {
				ids[id] = true
			}
		}
		keys := func(m *data.Map[int32, int32]) {
			if m == nil {
				return
			}
			for _, pr := range m.Pairs {
				collect(pr.Key)
			}
		}
		for _, id := range seedGachaCurrencies {
			collect(id)
		}
		for _, r := range t.SkillTree {
			keys(r.Consume)
		}
		for _, r := range t.WeaponBreach {
			keys(r.Consume)
		}
		for _, r := range t.RoleBreach {
			keys(r.BreachConsume)
		}
		for _, r := range t.SkillLevel {
			keys(r.Consume)
		}
		for _, r := range t.PhantomQuality {
			keys(r.IdentifyCost)
		}
		for _, r := range t.PhantomRarity {
			keys(r.PolishCost)
		}
		for _, r := range t.PhantomVicePolishConfig {
			keys(r.Cost)
		}
		for _, r := range t.WeaponExpItem {
			collect(r.Id)
		}
		for _, r := range t.PhantomExpItem {
			collect(r.ItemId)
		}
		for _, r := range t.MotorTechTree {
			collect(r.TpItemId)
		}
		if len(ids) > 0 {
			sorted := make([]int32, 0, len(ids))
			for id := range ids {
				sorted = append(sorted, id)
			}
			slices.Sort(sorted)
			var batch []save.ItemCount
			for _, id := range sorted {
				batch = append(batch, save.ItemCount{ID: id, Count: 666})
			}
			p.NormalInventory.SetBatch(batch)
			p.NormalInventory.CompactSnapshot()
		}
	}
	// 4. Cosmetics (specs/systems.md §2.1).
	v.SeedCosmeticDefaults()
}

// SeedCosmeticDefaults fills an empty cosmetic_info, and keeps every PhantomItem skin
// (ParentMonsterId != 0) in phantom_skins (specs/systems.md §2.1).
func (v *InventoryService) SeedCosmeticDefaults() {
	cos := &v.player.CosmeticInfo
	changed := false
	addSkins := func() {
		for _, r := range v.tables.PhantomItem {
			if r.ParentMonsterId != 0 && !slices.Contains(cos.PhantomSkins, r.ItemId) {
				cos.PhantomSkins = append(cos.PhantomSkins, r.ItemId)
				changed = true
			}
		}
	}
	if len(cos.RoleSkins) == 0 && len(cos.PhantomSkins) == 0 && len(cos.FlySkins) == 0 &&
		len(cos.WeaponSkins) == 0 && len(cos.Ornaments) == 0 && len(cos.ViewedOrnaments) == 0 {
		for _, r := range v.tables.FlySkinConfig {
			cos.FlySkins = append(cos.FlySkins, r.Id)
			cos.PhantomSkins = append(cos.PhantomSkins, r.Id)
		}
		for _, r := range v.tables.RoleSkin {
			cos.RoleSkins = append(cos.RoleSkins, r.Id)
			cos.PhantomSkins = append(cos.PhantomSkins, r.Id)
		}
		for _, r := range v.tables.WeaponSkin {
			if !r.HideInSkinView {
				cos.WeaponSkins = append(cos.WeaponSkins, r.Id)
				cos.PhantomSkins = append(cos.PhantomSkins, r.Id)
			}
		}
		for _, r := range v.tables.Ornament {
			if r.HideInUi {
				continue
			}
			cos.Ornaments = append(cos.Ornaments, r.Id)
			cos.ViewedOrnaments = append(cos.ViewedOrnaments, r.Id)
			cos.PhantomSkins = append(cos.PhantomSkins, r.Id)
			for _, a := range r.ItemAccess {
				cos.ViewedOrnaments = append(cos.ViewedOrnaments, a)
				cos.PhantomSkins = append(cos.PhantomSkins, a)
			}
		}
		addSkins()
		changed = len(cos.FlySkins) > 0 || len(cos.RoleSkins) > 0 || len(cos.WeaponSkins) > 0 ||
			len(cos.Ornaments) > 0 || len(cos.ViewedOrnaments) > 0 || len(cos.PhantomSkins) > 0
	} else {
		addSkins()
	}
	if changed {
		save.SaveCosmeticInfo(v.player)
	}
}

// MigrateStrayWeaponsOnLogin (R-116) moves weapons that landed in inventory_info (not in
// ItemInfo, in WeaponConf) into weapon/<uid> before any item push (specs/inventory.md §2.2).
func (v *InventoryService) MigrateStrayWeaponsOnLogin() int {
	type stray struct{ id, n int32 }
	var list []stray
	for id, n := range v.player.NormalInventory.Items().All() {
		_, inItems := v.tables.ItemInfoById[id]
		_, isWeapon := v.tables.WeaponConfByItemId[id]
		if n > 0 && !inItems && isWeapon {
			list = append(list, stray{id, n})
		}
	}
	if len(list) == 0 {
		return 0
	}
	copies := 0
	var removals []save.ItemCount
	for _, s := range list {
		q := v.tables.WeaponConfByItemId[s.id].QualityId
		for range s.n {
			uid := v.player.SpecialItemIDs.Next()
			w := save.NewWeapon(uid)
			w.ID, w.FuncValue = s.id, funcValueOf(q)
			v.player.Weapons.Set(uid, w)
			save.SaveWeapon(v.player, w)
			copies++
		}
		removals = append(removals, save.ItemCount{ID: s.id, Count: 0})
	}
	v.player.NormalInventory.SetBatch(removals)
	return copies
}

// PushFilteredNormalItemUpdate (R-109, R-116) sends only ItemInfo ids, nothing when none is left.
func (v *InventoryService) PushFilteredNormalItemUpdate(items []save.ItemCount, noTips bool, sink PushSink) bool {
	n := &protocol.NormalItemUpdateNotify{NoTips: noTips}
	for _, it := range items {
		if _, ok := v.tables.ItemInfoById[it.ID]; ok {
			n.NormalItemList = append(n.NormalItemList, &protocol.NormalItem{Id: it.ID, Count: it.Count})
		}
	}
	if len(n.NormalItemList) == 0 {
		return false
	}
	sink.Push(n)
	return true
}

// PushFilteredNormalItemAdd (R-109, R-116) as PushFilteredNormalItemUpdate.
func (v *InventoryService) PushFilteredNormalItemAdd(items []save.ItemCount, reason int32, noTips bool, sink PushSink) bool {
	n := &protocol.NormalItemAddNotify{Reason: reason, NoTips: noTips}
	for _, it := range items {
		if _, ok := v.tables.ItemInfoById[it.ID]; ok {
			n.NormalItemList = append(n.NormalItemList, &protocol.NormalItem{Id: it.ID, Count: it.Count})
		}
	}
	if len(n.NormalItemList) == 0 {
		return false
	}
	sink.Push(n)
	return true
}

func (v *InventoryService) sortedItems() []save.ItemCount {
	var out []save.ItemCount
	for id, n := range v.player.NormalInventory.Items().All() {
		out = append(out, save.ItemCount{ID: id, Count: n})
	}
	slices.SortFunc(out, func(a, b save.ItemCount) int { return int(a.ID) - int(b.ID) })
	return out
}

// PushLoginNormalItems (specs/inventory.md §3.1).
func (v *InventoryService) PushLoginNormalItems(sink PushSink) {
	v.PushFilteredNormalItemUpdate(v.sortedItems(), true, sink)
}

// PushLoginCalabashAndBagNotifies: calabash, projector, bag tabs, buff items and energy (inventory.md §3.2–§3.7).
func (v *InventoryService) PushLoginCalabashAndBagNotifies(sink PushSink) {
	sink.Push(v.BuildCalabashMsgNotify())
	sink.Push(v.BuildPhantomInteractionUnlockNotify())
	sink.Push(&protocol.CalabashLevelsRewardNotify{RewardedLevels: slices.Clone(v.player.Calabash.RewardedLevels)})
	sink.Push(&protocol.ItemPkgOpenNotify{OpenPkg: []int32{0, 1, 2, 3, 4, 5, 6, 7}})
	sink.Push(&protocol.BuffItemNotify{})
	sink.Push(&protocol.EnergyUpdateNotify{UpdateInfoList: energyInfos()})
}

func energyInfos() []*protocol.EnergyInfo {
	return []*protocol.EnergyInfo{{Id: 5, EnergyCount: 240}, {Id: 6, EnergyCount: 480}}
}

func (v *InventoryService) BuildCalabashMsgNotify() *protocol.CalabashMsgNotify {
	var top *data.CalabashLevel
	for _, c := range v.tables.CalabashLevel {
		if top == nil || c.Level > top.Level {
			top = c
		}
	}
	cfg := &protocol.CalabashCfg{}
	msg := &protocol.CalabashMsg{Level: 1, Exp: 0}
	if top != nil {
		cfg.LevelUpExp, cfg.LevelUpCondition = top.LevelUpExp, top.LevelUpCondition
		cfg.CatchGain = map[int32]int32{1: top.TempCatchGain, 2: top.LowCostTempCatchGain}
		msg.Level = top.Level
	}
	for _, l := range v.tables.CalabashLevel {
		msg.UnlockedLevels = append(msg.UnlockedLevels, l.Level)
	}
	for _, d := range v.tables.CalabashDevelopReward {
		if !d.IsShow {
			continue
		}
		info := &protocol.CalabashDevelopInfo{MonsterId: d.MonsterId}
		for _, c := range d.DevelopCondition {
			info.UnlockConditions = append(info.UnlockConditions, &protocol.CalabashDevelopConditionState{ConditionId: c, Rewarded: true})
		}
		msg.UnlockedDevelopRewards = append(msg.UnlockedDevelopRewards, info)
	}
	return &protocol.CalabashMsgNotify{CalabashCfg: cfg, CalabashMsg: msg}
}

func (v *InventoryService) BuildPhantomInteractionUnlockNotify() *protocol.PhantomInteractionUnlockNotify {
	ids := v.player.Calabash.ProjectorMonsterIDs
	n := &protocol.PhantomInteractionUnlockNotify{EquipedMonsterIds: slices.Clone(ids[:min(len(ids), MaxProjectorSlots)])}
	unlocked := map[int32]bool{}
	for _, s := range v.player.CosmeticInfo.PhantomSkins {
		unlocked[s] = true
	}
	equipped := map[int32]int32{}
	for _, s := range v.player.Calabash.ProjectorSkins {
		equipped[s.MonsterID] = s.SkinID
	}
	skins := map[int32][]int32{}
	monsters := map[int32]bool{}
	for _, it := range v.tables.PhantomItem {
		if it.ParentMonsterId == 0 {
			// The client throws on a monster without a CalabashDevelopReward row and drops
			// the rest of the notify (R-144h).
			if _, ok := v.tables.CalabashDevelopRewardByMonsterId[it.MonsterId]; ok {
				monsters[it.MonsterId] = true
			}
		} else if unlocked[it.ItemId] && !slices.Contains(skins[it.ParentMonsterId], it.ItemId) {
			skins[it.ParentMonsterId] = append(skins[it.ParentMonsterId], it.ItemId)
		}
	}
	sorted := make([]int32, 0, len(monsters))
	for m := range monsters {
		sorted = append(sorted, m)
	}
	slices.Sort(sorted)
	for _, m := range sorted {
		n.UnlockIllustratedPhantoms = append(n.UnlockIllustratedPhantoms, &protocol.UnlockIllustratedPhantom{
			MonsterId: m, EqupiedSkin: equipped[m], IsSpecial: false, SkinIds: skins[m],
		})
	}
	return n
}

// ----- §4 normal items, lock and disuse -----

func (v *InventoryService) HandleNormalItem(*protocol.NormalItemRequest) *protocol.NormalItemResponse {
	resp := &protocol.NormalItemResponse{}
	attr := &v.player.BasicInfo.Attributes
	saw := map[int32]bool{}
	for _, it := range v.sortedItems() {
		if _, ok := v.tables.ItemInfoById[it.ID]; !ok {
			continue
		}
		count := it.Count
		if m, _ := MoneyAttr(attr, it.ID); m != nil {
			saw[it.ID], count = true, *m
		}
		resp.NormalItemList = append(resp.NormalItemList, &protocol.NormalItem{Id: it.ID, Count: count})
	}
	for id := MoneyShellCredit; id <= MoneyLunite; id++ {
		if _, ok := v.tables.ItemInfoById[id]; ok && !saw[id] {
			m, _ := MoneyAttr(attr, id)
			resp.NormalItemList = append(resp.NormalItemList, &protocol.NormalItem{Id: id, Count: *m})
		}
	}
	return resp
}

func (v *InventoryService) HandleItemLock(req *protocol.ItemLockRequest, nowMs int64, sink PushSink) *protocol.ItemLockResponse {
	fv := int32(0)
	if req.Oper == 1 {
		fv = 1
	}
	if !v.setFuncValue(req.IncrId, fv, nowMs, sink) {
		return &protocol.ItemLockResponse{ErrCode: codeParamError}
	}
	return &protocol.ItemLockResponse{ErrCode: codeSuccess}
}

func (v *InventoryService) HandleItemDisuse(req *protocol.ItemDisuseRequest, nowMs int64, sink PushSink) *protocol.ItemDisuseResponse {
	fv := int32(0)
	if req.Oper == 1 {
		fv = 2
	}
	if !v.setFuncValue(req.IncrId, fv, nowMs, sink) {
		return &protocol.ItemDisuseResponse{ErrCode: codeParamError}
	}
	return &protocol.ItemDisuseResponse{ErrCode: codeSuccess}
}

func (v *InventoryService) setFuncValue(incrID, fv int32, nowMs int64, sink PushSink) bool {
	if w, ok := v.player.Weapons.Get(incrID); ok {
		w.FuncValue = fv
		v.player.DirtySaves.MarkWeapon(w.UniqueID, nowMs)
		sink.Push(&protocol.ItemFuncValueUpdateNotify{IncrId: incrID, FuncValue: fv})
		return true
	}
	if e, ok := v.player.Echoes.Get(incrID); ok {
		e.FuncValue = fv
		save.SaveEcho(v.player, e)
		sink.Push(&protocol.ItemFuncValueUpdateNotify{IncrId: incrID, FuncValue: fv})
		return true
	}
	return false
}

// ----- §5 weapons -----

func roleIDOf(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func (v *InventoryService) HandleWeaponItem(*protocol.WeaponItemRequest) *protocol.WeaponItemResponse {
	ws := v.player.Weapons.Values()
	slices.SortFunc(ws, func(a, b *save.WeaponSave) int { return int(a.UniqueID) - int(b.UniqueID) })
	resp := &protocol.WeaponItemResponse{}
	for _, w := range ws {
		resp.WeaponItemList = append(resp.WeaponItemList, &protocol.WeaponItem{Id: w.ID, IncrId: w.UniqueID, FuncValue: w.FuncValue,
			WeaponLevel: w.Level, WeaponExp: w.Exp, WeaponBreach: w.Breach, WeaponResonLevel: w.ResonLevel, RoleId: roleIDOf(w.RoleID)})
	}
	return resp
}

// weaponFodderExpPermille is CommonParam WeaponStrengthenExpCost (3.8: 900): a weapon used as
// level-up material gives back this share of the exp put into it (client
// WeaponConfig.GetWeaponExpCoefficient, WeaponInstance.GetMaterialExp).
const weaponFodderExpPermille = 900

// weaponLockedFuncValue: FuncValue of a locked item (§4.2).
const weaponLockedFuncValue = 1

// weaponBreachRow is WeaponBreach (breachID, level), or nil.
func (v *InventoryService) weaponBreachRow(breachID, level int32) *data.WeaponBreach {
	for _, b := range v.tables.WeaponBreach {
		if b.BreachId == breachID && b.Level == level {
			return b
		}
	}
	return nil
}

// weaponExpTo is the exp from level `from` (exp 0) to level `to`: WeaponLevel(levelID, L).Exp is
// what level L needs to reach L+1 (client WeaponDataBase.GetMaxExp).
func (v *InventoryService) weaponExpTo(levelID, from, to int32) int64 {
	var n int64
	for l := from; l < to; l++ {
		n += int64(v.weaponLevelExp(levelID, l))
	}
	return n
}

// weaponFodderExp is what weapon o gives as level-up material (client
// WeaponInstance.GetMaterialExp): WeaponQualityInfo.BasicExp at level 1 with no exp, else
// floor(BasicExp + (exp to its level + exp) × 0.9).
func (v *InventoryService) weaponFodderExp(o *save.WeaponSave) int64 {
	oc, ok := v.tables.WeaponConfByItemId[o.ID]
	if !ok {
		return 0
	}
	q, ok := v.tables.WeaponQualityInfoById[oc.QualityId]
	if !ok {
		return 0
	}
	if o.Exp <= 0 && o.Level <= 1 {
		return int64(q.BasicExp)
	}
	put := v.weaponExpTo(oc.LevelId, 1, o.Level) + int64(o.Exp)
	return int64(q.BasicExp) + put*weaponFodderExpPermille/1000
}

// weaponExpRefund turns overflow exp back into weapon exp items, biggest first (client
// WeaponModel.GetCanChangeMaterialList; the client sorts the ItemType 4 items by quality, which
// is the BasicExp order).
func (v *InventoryService) weaponExpRefund(over int64) []Cost {
	items := slices.Clone(v.tables.WeaponExpItem)
	slices.SortFunc(items, func(a, b *data.WeaponExpItem) int { return int(b.BasicExp - a.BasicExp) })
	var out []Cost
	for _, it := range items {
		if it.BasicExp <= 0 || over < int64(it.BasicExp) {
			continue
		}
		n := over / int64(it.BasicExp)
		out = append(out, Cost{Key: it.Id, Value: int32(min(n, math.MaxInt32))})
		over %= int64(it.BasicExp)
	}
	return out
}

func (v *InventoryService) HandleWeaponLevelUp(req *protocol.WeaponLevelUpRequest, nowMs int64, sink PushSink) *protocol.WeaponLevelUpResponse {
	fail := func(code protocol.ErrorCode) *protocol.WeaponLevelUpResponse {
		return &protocol.WeaponLevelUpResponse{ErrorCode: code}
	}
	w, ok := v.player.Weapons.Get(req.IncId)
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeItemNotFound)
	}
	wc, ok := v.tables.WeaponConfByItemId[w.ID]
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ErrWeaponResonConfigNotFound)
	}
	limit := int32(90)
	if b := v.weaponBreachRow(wc.BreachId, w.Breach); b != nil {
		limit = b.LevelLimit
	}
	if w.Level >= limit {
		return &protocol.WeaponLevelUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponLevelLimit, IncId: w.UniqueID, WeaponLevel: w.Level, WeaponExp: w.Exp}
	}
	// What the request uses up: weapons (IncId > 0) and exp items, each priced in 贝币
	// (WeaponQualityInfo.Cost / WeaponExpItem.Cost; client WeaponModel.GetWeaponExpItemListCost).
	var gained, gold int64
	var costs []Cost
	var fodder []int32
	seenItem := map[int32]bool{}
	for _, c := range req.ConsumeList {
		if c.ItemId == 0 || c.Count <= 0 {
			continue
		}
		if c.IncId > 0 {
			o, ok := v.player.Weapons.Get(c.IncId)
			switch {
			case !ok:
				return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeItemNotFound)
			case o.UniqueID == w.UniqueID:
				return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeSelf)
			case slices.Contains(fodder, o.UniqueID):
				return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeDuplicate)
			case o.RoleID != nil:
				return fail(protocol.ErrorCode_ErrorCode_ErrWeaponEquiped)
			case o.FuncValue == weaponLockedFuncValue:
				return fail(protocol.ErrorCode_ErrorCode_ErrWeaponLocked)
			}
			oc := v.tables.WeaponConfByItemId[o.ID]
			if oc == nil {
				return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeItemIdNotFound)
			}
			fodder = append(fodder, o.UniqueID)
			gained += v.weaponFodderExp(o)
			if q, ok := v.tables.WeaponQualityInfoById[oc.QualityId]; ok {
				gold += int64(q.Cost)
			}
			continue
		}
		e, ok := v.tables.WeaponExpItemById[c.ItemId]
		if !ok {
			return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeItemIdNotFound)
		}
		if seenItem[c.ItemId] {
			return fail(protocol.ErrorCode_ErrorCode_ErrWeaponLevelUpItemDuplicate)
		}
		seenItem[c.ItemId] = true
		gained += int64(e.BasicExp) * int64(c.Count)
		gold += int64(e.Cost) * int64(c.Count)
		costs = append(costs, Cost{Key: c.ItemId, Value: c.Count})
	}
	if gained <= 0 {
		return fail(protocol.ErrorCode_ErrorCode_ErrWeaponLevelUpNoExp)
	}
	if gold > 0 {
		costs = append(costs, Cost{Key: MoneyShellCredit, Value: int32(min(gold, math.MaxInt32))})
	}
	if !CanPay(v.player, costs) {
		return fail(protocol.ErrorCode_ErrorCode_ErrWeaponConsumeInvalid)
	}
	Pay(v.tables, v.player, costs, sink)
	if len(fodder) > 0 {
		for _, id := range fodder {
			v.player.Weapons.Delete(id)
			save.DeleteWeapon(v.player, id)
		}
		sink.Push(&protocol.WeaponItemRemoveNotify{WeaponItemIncrIdList: fodder})
	}
	// Exp past the level limit comes back as exp items (ItemMap, shown as returned).
	over := gained - (v.weaponExpTo(wc.LevelId, w.Level, limit) - int64(w.Exp))
	exp := int64(w.Exp) + gained
	for w.Level < limit {
		need := v.weaponLevelExp(wc.LevelId, w.Level)
		if need <= 0 || exp < int64(need) {
			break
		}
		exp -= int64(need)
		w.Level++
	}
	if w.Level >= limit {
		w.Exp = 0
	} else {
		w.Exp = int32(min(exp, math.MaxInt32))
	}
	itemMap := map[int32]int32{}
	if over > 0 {
		refund := v.weaponExpRefund(over)
		for _, r := range refund {
			itemMap[r.Key] = r.Value
		}
		Give(v.tables, v.player, refund, sink)
	}
	v.player.DirtySaves.MarkWeapon(w.UniqueID, nowMs)
	if w.RoleID != nil {
		v.roles.RefreshLiveRoleAttributes(*w.RoleID, sink)
	}
	return &protocol.WeaponLevelUpResponse{ErrorCode: codeSuccess, IncId: w.UniqueID, WeaponLevel: w.Level, WeaponExp: w.Exp, ItemMap: itemMap}
}

// HandleWeaponBreach: breach b → b+1 needs level = WeaponBreach(b).LevelLimit and a row b+1,
// and pays WeaponBreach(b).Consume + GoldConsume (client WeaponBreachView).
func (v *InventoryService) HandleWeaponBreach(req *protocol.WeaponBreachRequest, nowMs int64, sink PushSink) *protocol.WeaponBreachResponse {
	w, ok := v.player.Weapons.Get(req.IncId)
	if !ok {
		return &protocol.WeaponBreachResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponConsumeItemNotFound}
	}
	wc, ok := v.tables.WeaponConfByItemId[w.ID]
	if !ok {
		return &protocol.WeaponBreachResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponConfigNotFound}
	}
	cur := v.weaponBreachRow(wc.BreachId, w.Breach)
	if cur == nil || v.weaponBreachRow(wc.BreachId, w.Breach+1) == nil {
		return &protocol.WeaponBreachResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponBreachLimit}
	}
	if w.Level < cur.LevelLimit {
		return &protocol.WeaponBreachResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponLevelUpLevel}
	}
	costs := AddCosts(nil, cur.Consume, 1)
	if cur.GoldConsume > 0 {
		costs = append(costs, Cost{Key: MoneyShellCredit, Value: cur.GoldConsume})
	}
	if !CanPay(v.player, costs) {
		return &protocol.WeaponBreachResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponConsumeInvalid}
	}
	Pay(v.tables, v.player, costs, sink)
	w.Breach++
	v.player.DirtySaves.MarkWeapon(w.UniqueID, nowMs)
	if w.RoleID != nil {
		rid := *w.RoleID
		if live := v.formation.FindLiveRoleEntity(rid); live != nil {
			live.WeaponBreachLevel = w.Breach
			sink.Push(&protocol.EntityEquipChangeNotify{EntityId: live.NetID, EquipComponent: &protocol.EquipComponentPb{WeaponId: w.ID, WeaponBreachLevel: w.Breach}})
		}
		v.roles.RefreshLiveRoleAttributes(rid, sink)
	}
	sink.Push(v.formation.BuildUpdateFormationNotify())
	return &protocol.WeaponBreachResponse{ErrorCode: codeSuccess, IncId: w.UniqueID, WeaponBreach: w.Breach}
}

func (v *InventoryService) HandleWeaponResonUp(req *protocol.WeaponResonUpRequest, nowMs int64, sink PushSink) *protocol.WeaponResonUpResponse {
	w, ok := v.player.Weapons.Get(req.IncId)
	if !ok {
		return &protocol.WeaponResonUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponConsumeItemNotFound}
	}
	wc, ok := v.tables.WeaponConfByItemId[w.ID]
	if !ok {
		return &protocol.WeaponResonUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponResonConfigNotFound}
	}
	if wc.ResonLevelLimit > 0 && w.ResonLevel >= wc.ResonLevelLimit {
		return &protocol.WeaponResonUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponResonLevelLimit}
	}
	var consumed int32
	for _, id := range req.ConsumeList {
		if v.isConsumableDuplicate(w, id) {
			consumed = id
			break
		}
	}
	if consumed == 0 {
		for _, it := range req.ConsumeItemList {
			if v.isConsumableDuplicate(w, it.IncId) {
				consumed = it.IncId
				break
			}
		}
	}
	if consumed == 0 {
		return &protocol.WeaponResonUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponResonConsumeItem}
	}
	old := w.ResonLevel
	// Level L → L+1 pays WeaponReson(ResonId, L).GoldConsume (client WeaponModel.GetResonanceNeedMoney).
	var costs []Cost
	for _, r := range v.tables.WeaponReson {
		if r.ResonId == wc.ResonId && r.Level == old && r.GoldConsume > 0 {
			costs = append(costs, Cost{Key: MoneyShellCredit, Value: r.GoldConsume})
			break
		}
	}
	if !CanPay(v.player, costs) {
		return &protocol.WeaponResonUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrWeaponResonConsumeGold}
	}
	Pay(v.tables, v.player, costs, sink)
	v.player.Weapons.Delete(consumed)
	save.DeleteWeapon(v.player, consumed)
	w.ResonLevel = old + 1
	v.player.DirtySaves.MarkWeapon(w.UniqueID, nowMs)
	sink.Push(&protocol.WeaponItemRemoveNotify{WeaponItemIncrIdList: []int32{consumed}})
	if w.RoleID != nil {
		rid := *w.RoleID
		if live := v.formation.FindLiveRoleEntity(rid); live != nil {
			v.buffs.SwapEquipmentBuffsS2C(live, v.weaponResonBuffs(wc.ResonId, old), v.weaponResonBuffs(wc.ResonId, w.ResonLevel), WeaponRemoveReason, WeaponApplyReason, sink)
		}
		v.roles.RefreshLiveRoleAttributes(rid, sink)
	}
	return &protocol.WeaponResonUpResponse{ErrorCode: codeSuccess, IncId: w.UniqueID, ReaonLevel: w.ResonLevel}
}

func (v *InventoryService) HandleEquipTakeOn(req *protocol.EquipTakeOnRequest, sink PushSink) *protocol.EquipTakeOnResponse {
	if req.Data == nil || req.Data.Pos != protocol.EquipPos_EquipPos_Weapon {
		return &protocol.EquipTakeOnResponse{ErrorCode: codeParamError}
	}
	targetID := req.Data.RoleID
	target, ok1 := v.player.Roles.Get(targetID)
	nw, ok2 := v.player.Weapons.Get(req.Data.EquipIncID)
	if !ok1 || !ok2 {
		return &protocol.EquipTakeOnResponse{ErrorCode: codeParamError}
	}
	old := v.player.EquippedWeapon(target)
	oldBuffsTarget := v.weaponBuffs(old)
	var other *save.RoleSave
	if nw.RoleID != nil && *nw.RoleID != targetID {
		other = v.player.Roles.At(*nw.RoleID)
	}
	var oldBuffsOther []int64
	if other != nil {
		oldBuffsOther = v.weaponBuffs(nw)
	}
	// Swap, so every role keeps a weapon.
	if other != nil {
		if old != nil {
			oid := other.RoleID
			old.RoleID = &oid
			other.Weapon = old.UniqueID
			save.SaveWeapon(v.player, old)
		} else {
			other.Weapon = 0
		}
		save.SaveRole(v.player, other)
	} else if old != nil && old.UniqueID != nw.UniqueID {
		old.RoleID = nil
		save.SaveWeapon(v.player, old)
	}
	tid := targetID
	nw.RoleID = &tid
	target.Weapon = nw.UniqueID
	save.SaveWeapon(v.player, nw)
	save.SaveRole(v.player, target)
	changed := []*protocol.RoleLoadEquipData{{RoleID: targetID, Pos: protocol.EquipPos_EquipPos_Weapon, EquipIncID: nw.UniqueID}}
	v.syncRoleWeaponOnScene(targetID, nw, oldBuffsTarget, sink)
	if other != nil {
		changed = append(changed, &protocol.RoleLoadEquipData{RoleID: other.RoleID, Pos: protocol.EquipPos_EquipPos_Weapon, EquipIncID: other.Weapon})
		v.syncRoleWeaponOnScene(other.RoleID, v.player.EquippedWeapon(other), oldBuffsOther, sink)
	}
	sink.Push(v.formation.BuildUpdateFormationNotify())
	sink.Push(&protocol.EquipTakeOnNotify{DataList: changed})
	return &protocol.EquipTakeOnResponse{ErrorCode: codeSuccess, DataList: changed}
}

// ----- §6 echoes -----

func (v *InventoryService) HandlePhantomItem(*protocol.PhantomItemRequest) *protocol.PhantomItemResponse {
	resp := &protocol.PhantomItemResponse{TotalCost: v.MaxEchoCost(), DirectRefineWeekTimes: 0, UnlockSkins: slices.Clone(v.player.CosmeticInfo.PhantomSkins)}
	es := v.player.Echoes.Values()
	slices.SortFunc(es, func(a, b *save.EchoSave) int { return int(a.UniqueID) - int(b.UniqueID) })
	var roles []int32
	for _, e := range es {
		resp.PhantomItemList = append(resp.PhantomItemList, ToProtoEcho(e))
		if e.RoleID != nil && e.EquippedPos >= 0 && e.EquippedPos < 5 && !slices.Contains(roles, *e.RoleID) {
			roles = append(roles, *e.RoleID)
		}
	}
	slices.Sort(roles)
	for _, rid := range roles {
		resp.EquipInfo = append(resp.EquipInfo, v.rolePhantomEquipInfo(rid))
		if role, ok := v.player.Roles.Get(rid); ok {
			resp.PropInfo = append(resp.PropInfo, ComputeLive(v.tables, v.player, role, v.buffBuilder.RoleAutoBuffTags(rid)).ToRolePhantomPropInfo(rid))
		}
	}
	return resp
}

func unequip(p *save.PlayerData, e *save.EchoSave) {
	e.RoleID = nil
	e.EquippedPos = -1
	save.SaveEcho(p, e)
}

func equipTo(p *save.PlayerData, e *save.EchoSave, roleID, pos int32) {
	e.RoleID = &roleID
	e.EquippedPos = pos
	save.SaveEcho(p, e)
}

func (v *InventoryService) HandlePhantomPutOn(req *protocol.PhantomPutOnRequest, nowMs int64, sink PushSink) *protocol.PhantomPutOnResponse {
	if req.Pos < 0 || req.Pos >= 5 || !v.player.Roles.Has(req.RoleId) {
		return &protocol.PhantomPutOnResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomInvalidPos}
	}
	affected := []int32{req.RoleId}
	before := v.captureEchoRoleState(req.RoleId)
	cur := v.player.EquippedEchoes(req.RoleId)
	prev := cur[req.Pos]
	if req.IncId == 0 {
		if prev != nil {
			unequip(v.player, prev)
		}
	} else {
		target, ok := v.player.Echoes.Get(req.IncId)
		if !ok {
			return &protocol.PhantomPutOnResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomNotExist}
		}
		prevOwner, prevPos := target.RoleID, target.EquippedPos
		next := cur
		if prevOwner != nil && *prevOwner == req.RoleId && prevPos >= 0 && prevPos < 5 {
			next[prevPos] = prev
		}
		next[req.Pos] = target
		if err := v.validateEchoSlots(next); err != codeSuccess {
			return &protocol.PhantomPutOnResponse{ErrorCode: err}
		}
		switch {
		case prevOwner != nil && *prevOwner != req.RoleId && prevPos >= 0 && prevPos < 5:
			other := *prevOwner
			affected = append(affected, other)
			if _, ok := before[other]; !ok {
				before[other] = v.captureSingle(other)
			}
			os := v.player.EquippedEchoes(other)
			os[prevPos] = prev
			if prev != nil && v.validateEchoSlots(os) != codeSuccess {
				// The swap would break the other role's cost or repeats: take prev off instead.
				unequip(v.player, prev)
			} else if prev != nil {
				equipTo(v.player, prev, other, prevPos)
			}
		case prevOwner != nil && *prevOwner == req.RoleId && prevPos >= 0 && prevPos < 5 && prevPos != req.Pos:
			if prev != nil {
				equipTo(v.player, prev, req.RoleId, prevPos)
			}
		case prev != nil && prev.UniqueID != target.UniqueID:
			unequip(v.player, prev)
		}
		equipTo(v.player, target, req.RoleId, req.Pos)
	}
	v.syncAffectedRoles(affected, before, nowMs, sink)
	resp := &protocol.PhantomPutOnResponse{ErrorCode: codeSuccess}
	for _, rid := range sortedSet(affected) {
		resp.EquipInfoList = append(resp.EquipInfoList, v.rolePhantomEquipInfo(rid))
	}
	return resp
}

func sortedSet(ids []int32) []int32 {
	var out []int32
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func (v *InventoryService) HandlePhantomAutoPut(req *protocol.PhantomAutoPutRequest, nowMs int64, sink PushSink) *protocol.PhantomAutoPutResponse {
	err, affected := v.applyFiveSlotEchoEquip(req.RoleId, req.PhantomItemIncrId, nowMs, sink)
	resp := &protocol.PhantomAutoPutResponse{ErrorCode: err}
	if err == codeSuccess {
		for _, rid := range sortedSet(affected) {
			resp.EquipInfoList = append(resp.EquipInfoList, v.rolePhantomEquipInfo(rid))
		}
	}
	return resp
}

func (v *InventoryService) HandlePhantomSkinChange(req *protocol.PhantomSkinChangeRequest, nowMs int64, sink PushSink) *protocol.PhantomSkinChangeResponse {
	e, ok := v.player.Echoes.Get(req.IncrId)
	if !ok {
		return &protocol.PhantomSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomNotExist}
	}
	base, ok := v.tables.PhantomItemByItemId[e.ID]
	if !ok {
		return &protocol.PhantomSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomConfigNotFound}
	}
	if req.SkinId != 0 {
		if skin, ok := v.tables.PhantomItemByItemId[req.SkinId]; !ok || skin.ParentMonsterId != base.MonsterId {
			return &protocol.PhantomSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomSkinMatch}
		}
	}
	var affected []int32
	before := map[int32]echoRoleBefore{}
	if e.RoleID != nil {
		affected = append(affected, *e.RoleID)
		before = v.captureEchoRoleState(*e.RoleID)
	}
	e.SkinID = req.SkinId
	save.SaveEcho(v.player, e)
	sink.Push(&protocol.PhantomItemUpdateNotify{UpdateInfo: []*protocol.PhantomItem{ToProtoEcho(e)}})
	if len(affected) > 0 {
		v.syncAffectedRoles(affected, before, nowMs, sink)
	}
	return &protocol.PhantomSkinChangeResponse{ErrorCode: codeSuccess}
}

func (v *InventoryService) beforeOf(e *save.EchoSave) map[int32]echoRoleBefore {
	if e.RoleID != nil {
		return v.captureEchoRoleState(*e.RoleID)
	}
	return map[int32]echoRoleBefore{}
}

func (v *InventoryService) afterEchoChange(e *save.EchoSave, before map[int32]echoRoleBefore, nowMs int64, sink PushSink) {
	save.SaveEcho(v.player, e)
	if e.RoleID != nil {
		v.syncAffectedRoles([]int32{*e.RoleID}, before, nowMs, sink)
	}
}

func (v *InventoryService) rollSubProps(e *save.EchoSave, quality, n int32) {
	for range n {
		if sp, ok := RollNewSubProp(v.tables, quality, e.SubProp, v.rng); ok {
			e.SubProp = append(e.SubProp, sp)
		}
	}
}

// Echo cost parameters, CommonParam of 3.8 (client PhantomBattleConfig, PhantomDataBase).
const (
	echoLevelUpCoinPermille        = 100 // PhantomLevelUpCoinCost: 贝币 per exp used
	echoFodderExpPermille          = 750 // PhantomExpReturnRatio: an echo used as material gives this share of its exp
	echoIdentifyBackPermille       = 300 // PhantomIdentifyReturnRatio: share of its tuning items it gives back
	echoLockedFuncValue      int32 = 1   // FuncValue of a locked item (§4.2)
)

// echoFullExp is the exp put into e (client PhantomDataBase.GetFullExp): PhantomLevel(group, L).Exp
// is what level L-1 needs to reach L.
func (v *InventoryService) echoFullExp(e *save.EchoSave, group int32) int64 {
	n := int64(e.Exp)
	for l := int32(1); l <= e.Level; l++ {
		n += int64(v.phantomLevelExp(group, l))
	}
	return n
}

// echoIdentifyCosts is what n tunings of a quality-q echo take: PhantomQuality.IdentifyCost
// items and IdentifyCoin 贝币 each (client PhantomDataBase.GetCurrentIdentifyCost,
// GetIdentifyCostItemValue).
func echoIdentifyCosts(q *data.PhantomQuality, n int32) []Cost {
	if n <= 0 {
		return nil
	}
	costs := AddCosts(nil, q.IdentifyCost, n)
	if q.IdentifyCoin > 0 {
		costs = append(costs, Cost{Key: MoneyShellCredit, Value: q.IdentifyCoin * n})
	}
	return costs
}

// echoExpRefund turns overflow exp into echo exp items, biggest first (client
// PhantomBattleModel.CalculateExpBackItem).
func (v *InventoryService) echoExpRefund(over int64) []Cost {
	items := slices.Clone(v.tables.PhantomExpItem)
	slices.SortFunc(items, func(a, b *data.PhantomExpItem) int { return int(b.Exp - a.Exp) })
	var out []Cost
	for _, it := range items {
		if it.Exp <= 0 || over < int64(it.Exp) {
			continue
		}
		out = append(out, Cost{Key: it.ItemId, Value: int32(min(over/int64(it.Exp), math.MaxInt32))})
		over %= int64(it.Exp)
	}
	return out
}

// netCosts takes refunds of an item the request also pays from that cost (client
// VisionLevelUpView.ProcessAutoIdentity: returned tuning items pay the tuning first).
func netCosts(costs, refunds []Cost) ([]Cost, []Cost) {
	for i := range refunds {
		for j := range costs {
			if costs[j].Key == refunds[i].Key {
				n := min(costs[j].Value, refunds[i].Value)
				costs[j].Value -= n
				refunds[i].Value -= n
			}
		}
	}
	return costs, refunds
}

func (v *InventoryService) HandlePhantomLevelUp(req *protocol.PhantomLevelUpRequest, nowMs int64, sink PushSink) *protocol.PhantomLevelUpResponse {
	fail := func(code protocol.ErrorCode) *protocol.PhantomLevelUpResponse {
		return &protocol.PhantomLevelUpResponse{ErrorCode: code}
	}
	e, ok := v.player.Echoes.Get(req.IncId)
	if !ok {
		return fail(protocol.ErrorCode_ErrorCode_ErrPhantomNotExist)
	}
	it, ok1 := v.tables.PhantomItemByItemId[e.ID]
	var q *data.PhantomQuality
	ok2 := false
	if ok1 {
		q, ok2 = v.tables.PhantomQualityByQuality[it.QualityId]
	}
	if !ok1 || !ok2 {
		return fail(protocol.ErrorCode_ErrorCode_ErrPhantomConfigNotFound)
	}
	if e.Level >= q.LevelLimit {
		return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLvupMax)
	}
	// Material: echoes (IncId > 0; exp floor(full exp × 0.75), tuning items back) and exp items.
	var gained int64
	var costs, refunds []Cost
	var fodder []int32
	seenItem := map[int32]bool{}
	for _, c := range req.ConsumeList {
		if c.IncId > 0 {
			f, ok := v.player.Echoes.Get(c.IncId)
			switch {
			case !ok || f.UniqueID == e.UniqueID:
				return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpConsumeItemErr)
			case slices.Contains(fodder, f.UniqueID):
				return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpRepeatItem)
			case f.RoleID != nil:
				return fail(protocol.ErrorCode_ErrorCode_ErrPhantomEquiped)
			case f.FuncValue == echoLockedFuncValue:
				return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpMaterialLock)
			}
			fi, ok := v.tables.PhantomItemByItemId[f.ID]
			if !ok {
				return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpConsumeItemErr)
			}
			fodder = append(fodder, f.UniqueID)
			gained += v.echoFullExp(f, fi.LevelUpGroupId) * echoFodderExpPermille / 1000
			if fq, ok := v.tables.PhantomQualityByQuality[fi.QualityId]; ok && fq.IdentifyCost != nil && len(f.SubProp) > 0 {
				for _, p := range fq.IdentifyCost.Pairs {
					if n := int64(p.Value) * int64(len(f.SubProp)) * echoIdentifyBackPermille / 1000; n > 0 {
						refunds = AddCosts(refunds, &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: p.Key, Value: int32(n)}}}, 1)
					}
				}
			}
			continue
		}
		if c.ItemId <= 0 || c.Count <= 0 {
			continue
		}
		x, ok := v.tables.PhantomExpItemByItemId[c.ItemId]
		if !ok {
			return fail(protocol.ErrorCode_ErrorCode_ErrPhantomExpItemNotExist)
		}
		if seenItem[c.ItemId] {
			return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpRepeatItem)
		}
		seenItem[c.ItemId] = true
		gained += int64(x.Exp) * int64(c.Count)
		costs = append(costs, Cost{Key: c.ItemId, Value: c.Count})
	}
	if gained <= 0 {
		return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLvupNoItem)
	}
	// The result, before anything changes: 贝币 on the exp used (up to the limit), the rest back
	// as exp items, tunings on the slots the new level opens.
	level, exp := e.Level, int64(e.Exp)+gained
	for level < q.LevelLimit {
		need := v.phantomLevelExp(it.LevelUpGroupId, level+1)
		if need <= 0 || exp < int64(need) {
			break
		}
		exp -= int64(need)
		level++
	}
	toLimit := -int64(e.Exp)
	for l := e.Level + 1; l <= q.LevelLimit; l++ {
		toLimit += int64(v.phantomLevelExp(it.LevelUpGroupId, l))
	}
	if gold := min(gained, toLimit) * echoLevelUpCoinPermille / 1000; gold > 0 {
		costs = append(costs, Cost{Key: MoneyShellCredit, Value: int32(min(gold, math.MaxInt32))})
	}
	if over := gained - toLimit; over > 0 {
		refunds = append(refunds, v.echoExpRefund(over)...)
	}
	var identify int32
	if req.IdentifyCount > 0 {
		identify = min(req.IdentifyCount, max(0, UnlockedSubSlots(v.tables, it.QualityId, level)-int32(len(e.SubProp))))
		costs = append(costs, echoIdentifyCosts(q, identify)...)
	}
	costs, refunds = netCosts(costs, refunds)
	if !CanPay(v.player, costs) {
		return fail(protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpConsumeItemNotEnough)
	}
	Pay(v.tables, v.player, costs, sink)
	if len(fodder) > 0 {
		for _, id := range fodder {
			v.player.Echoes.Delete(id)
			save.DeleteEcho(v.player, id)
		}
		sink.Push(&protocol.PhantomItemRemoveNotify{PhantomItemIncrIdList: fodder})
	}
	itemMap := map[int32]int32{}
	for _, r := range refunds {
		if r.Value > 0 {
			itemMap[r.Key] += r.Value
		}
	}
	Give(v.tables, v.player, refunds, sink)
	before := v.beforeOf(e)
	e.Level = level
	if level >= q.LevelLimit {
		e.Exp = 0
	} else {
		e.Exp = int32(min(exp, math.MaxInt32))
	}
	RecomputeMainProps(v.tables, e)
	v.rollSubProps(e, it.QualityId, identify)
	v.afterEchoChange(e, before, nowMs, sink)
	return &protocol.PhantomLevelUpResponse{ErrorCode: codeSuccess, UpdateInfo: ToProtoEcho(e), ItemMap: itemMap}
}

func (v *InventoryService) HandlePhantomIdentify(req *protocol.PhantomIdentifyRequest, nowMs int64, sink PushSink) *protocol.PhantomIdentifyResponse {
	e, ok := v.player.Echoes.Get(req.IncrId)
	if !ok {
		return &protocol.PhantomIdentifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomNotExist}
	}
	it, ok := v.tables.PhantomItemByItemId[e.ID]
	if !ok {
		return &protocol.PhantomIdentifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomConfigNotFound}
	}
	q, ok := v.tables.PhantomQualityByQuality[it.QualityId]
	if !ok {
		return &protocol.PhantomIdentifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomConfigNotFound}
	}
	unlocked := UnlockedSubSlots(v.tables, it.QualityId, e.Level)
	n := min(max(req.Count, 1), max(0, unlocked-int32(len(e.SubProp))))
	if n <= 0 {
		return &protocol.PhantomIdentifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomSubPropNotEnough}
	}
	costs := echoIdentifyCosts(q, n)
	if !CanPay(v.player, costs) {
		return &protocol.PhantomIdentifyResponse{ErrorCode: codeItemNotEnough}
	}
	Pay(v.tables, v.player, costs, sink)
	before := v.beforeOf(e)
	v.rollSubProps(e, it.QualityId, n)
	v.afterEchoChange(e, before, nowMs, sink)
	return &protocol.PhantomIdentifyResponse{ErrorCode: codeSuccess, UpdateInfo: ToProtoEcho(e)}
}

func (v *InventoryService) HandlePhantomPolish(req *protocol.PhantomPolishRequest, nowMs int64, sink PushSink) *protocol.PhantomPolishResponse {
	code, e, group := v.polishCheck(req.IncrId, req.PhantomMainPropItemId)
	if code != codeSuccess {
		return &protocol.PhantomPolishResponse{ErrorCode: code}
	}
	costs := v.polishCosts(e)
	if !CanPay(v.player, costs) {
		return &protocol.PhantomPolishResponse{ErrorCode: codeItemNotEnough}
	}
	Pay(v.tables, v.player, costs, sink)
	v.polishApply(e, group, nowMs, sink)
	return &protocol.PhantomPolishResponse{ErrorCode: codeSuccess, UpdateInfo: ToProtoEcho(e)}
}

// HandlePhantomBatchPolish polishes every valid entry, paying for all of them together (all
// or none).
func (v *InventoryService) HandlePhantomBatchPolish(req *protocol.PhantomBatchPolishRequest, nowMs int64, sink PushSink) *protocol.PhantomBatchPolishResponse {
	type job struct {
		e     *save.EchoSave
		group *data.PhantomMainProperty
	}
	var jobs []job
	var costs []Cost
	for _, s := range req.StackEntries {
		if code, e, group := v.polishCheck(s.IncrId, req.PhantomMainPropItemId); code == codeSuccess &&
			!slices.ContainsFunc(jobs, func(j job) bool { return j.e == e }) {
			jobs = append(jobs, job{e, group})
			for _, c := range v.polishCosts(e) {
				costs = AddCosts(costs, &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{c}}, 1)
			}
		}
	}
	if !CanPay(v.player, costs) {
		return &protocol.PhantomBatchPolishResponse{ErrorCode: codeItemNotEnough}
	}
	Pay(v.tables, v.player, costs, sink)
	resp := &protocol.PhantomBatchPolishResponse{ErrorCode: codeSuccess}
	for _, j := range jobs {
		v.polishApply(j.e, j.group, nowMs, sink)
		resp.UpdateInfos = append(resp.UpdateInfos, ToProtoEcho(j.e))
	}
	return resp
}

// HandlePhantomVicePolish pays PhantomVicePolishConfig(locked count).Cost for the reroll
// (client PhantomBattleModel, GetPhantomVicePolishCostByLockCount); the ack is free.
func (v *InventoryService) HandlePhantomVicePolish(req *protocol.PhantomVicePolishRequest, sink PushSink) *protocol.PhantomVicePolishResponse {
	e, ok := v.player.Echoes.Get(req.IncrId)
	if !ok {
		return &protocol.PhantomVicePolishResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomNotExist}
	}
	it, ok := v.tables.PhantomItemByItemId[e.ID]
	if !ok {
		return &protocol.PhantomVicePolishResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomConfigNotFound}
	}
	locked := map[int]bool{}
	for _, i := range req.LockPropIndex {
		if i >= 0 && int(i) < len(e.SubProp) {
			locked[int(i)] = true
		}
	}
	var costs []Cost
	if c, ok := v.tables.PhantomVicePolishConfigByPropCount[int32(len(locked))]; ok {
		costs = AddCosts(nil, c.Cost, 1)
	}
	if !CanPay(v.player, costs) {
		return &protocol.PhantomVicePolishResponse{ErrorCode: codeItemNotEnough}
	}
	Pay(v.tables, v.player, costs, sink)
	var working []save.EchoPropSave
	for i, sp := range e.SubProp {
		if locked[i] {
			working = append(working, sp)
		}
	}
	candidate := []save.EchoPropSave{}
	for i, sp := range e.SubProp {
		if locked[i] {
			candidate = append(candidate, sp)
		} else if r, ok := RollNewSubProp(v.tables, it.QualityId, working, v.rng); ok {
			candidate = append(candidate, r)
			working = append(working, r)
		} else {
			candidate = append(candidate, sp)
		}
	}
	e.UnackSubProp = candidate
	e.LockPropIndex = append([]int32{}, req.LockPropIndex...)
	save.SaveEcho(v.player, e)
	resp := &protocol.PhantomVicePolishResponse{ErrorCode: codeSuccess}
	for _, sp := range e.UnackSubProp {
		resp.PhantomSubProp = append(resp.PhantomSubProp, &protocol.PhantomPropInfo{PhantomPropId: sp.ID, Value: sp.Value})
	}
	return resp
}

func (v *InventoryService) HandlePhantomVicePolishAck(req *protocol.PhantomVicePolishAckRequest, nowMs int64, sink PushSink) *protocol.PhantomVicePolishAckResponse {
	e, ok := v.player.Echoes.Get(req.IncrId)
	if !ok {
		return &protocol.PhantomVicePolishAckResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomNotExist}
	}
	before := v.beforeOf(e)
	if req.Ack && len(e.UnackSubProp) > 0 {
		e.SubProp = slices.Clone(e.UnackSubProp)
	}
	e.UnackSubProp = []save.EchoPropSave{}
	e.LockPropIndex = []int32{}
	v.afterEchoChange(e, before, nowMs, sink)
	return &protocol.PhantomVicePolishAckResponse{ErrorCode: codeSuccess, UpdateInfo: ToProtoEcho(e)}
}

// ----- §6.6 echo presets (20 at most) -----

func (v *InventoryService) presetList() []*protocol.PhantomEquipGroupInfo {
	var out []*protocol.PhantomEquipGroupInfo
	for _, g := range v.player.EchoPresets.Groups {
		out = append(out, &protocol.PhantomEquipGroupInfo{Name: g.Name, IncId: slices.Clone(g.Slots[:])})
	}
	return out
}

func (v *InventoryService) HandlePhantomEquipGroupInfos(*protocol.PhantomEquipGroupInfosRequest) *protocol.PhantomEquipGroupInfosResponse {
	return &protocol.PhantomEquipGroupInfosResponse{ErrorCode: codeSuccess, IsOldData: false, PhantomEquipGroupInfos: v.presetList()}
}

func (v *InventoryService) HandleAddPhantomEquipGroup(req *protocol.AddPhantomEquipGroupRequest) *protocol.AddPhantomEquipGroupResponse {
	if len(v.player.EchoPresets.Groups) >= save.EchoPresetMaxGroups {
		return &protocol.AddPhantomEquipGroupResponse{ErrorCode: protocol.ErrorCode_ErrorCode_PhantomEquipGroupCountLimit}
	}
	if req.Name == "" {
		return &protocol.AddPhantomEquipGroupResponse{ErrorCode: protocol.ErrorCode_ErrorCode_PhantomEquipGroupNameEmpty}
	}
	g := &save.VisionGroupSave{Name: req.Name}
	any := false
	for i, e := range v.player.EquippedEchoes(req.RoleId) {
		if e != nil {
			g.Slots[i] = e.UniqueID
			any = any || e.UniqueID > 0
		}
	}
	if !any {
		return &protocol.AddPhantomEquipGroupResponse{ErrorCode: protocol.ErrorCode_ErrorCode_PhantomEquipGroupNoEquipPhantom}
	}
	g.IncrID = v.player.SpecialItemIDs.Next()
	v.player.EchoPresets.Groups = append(v.player.EchoPresets.Groups, g)
	save.SaveEchoPresets(v.player)
	return &protocol.AddPhantomEquipGroupResponse{ErrorCode: codeSuccess, IsOldData: false, PhantomEquipGroupInfos: v.presetList()}
}

func (v *InventoryService) presetIndexOK(i int32) bool {
	return i >= 0 && int(i) < len(v.player.EchoPresets.Groups)
}

func (v *InventoryService) HandleUsePhantomEquipGroup(req *protocol.UsePhantomEquipGroupRequest, nowMs int64, sink PushSink) *protocol.UsePhantomEquipGroupResponse {
	if !v.presetIndexOK(req.Index) {
		return &protocol.UsePhantomEquipGroupResponse{ErrorCode: codeParamError}
	}
	g := v.player.EchoPresets.Groups[req.Index]
	err, affected := v.applyFiveSlotEchoEquip(req.RoleId, g.Slots[:], nowMs, sink)
	resp := &protocol.UsePhantomEquipGroupResponse{ErrorCode: err}
	if err == codeSuccess {
		for _, rid := range sortedSet(affected) {
			resp.EquipInfoList = append(resp.EquipInfoList, v.rolePhantomEquipInfo(rid))
		}
	}
	return resp
}

func (v *InventoryService) HandleTopPhantomEquipGroup(req *protocol.TopPhantomEquipGroupRequest) *protocol.TopPhantomEquipGroupResponse {
	if !v.presetIndexOK(req.Index) {
		return &protocol.TopPhantomEquipGroupResponse{ErrorCode: codeParamError}
	}
	if req.Index > 0 {
		gs := v.player.EchoPresets.Groups
		g := gs[req.Index]
		gs = slices.Delete(gs, int(req.Index), int(req.Index)+1)
		v.player.EchoPresets.Groups = slices.Insert(gs, 0, g)
		save.SaveEchoPresets(v.player)
	}
	return &protocol.TopPhantomEquipGroupResponse{ErrorCode: codeSuccess, PhantomEquipGroupInfos: v.presetList()}
}

func (v *InventoryService) HandleChangePhantomEquipGroupName(req *protocol.ChangePhantomEquipGroupNameRequest) *protocol.ChangePhantomEquipGroupNameResponse {
	if !v.presetIndexOK(req.Index) {
		return &protocol.ChangePhantomEquipGroupNameResponse{ErrorCode: codeParamError}
	}
	if req.Name == "" {
		return &protocol.ChangePhantomEquipGroupNameResponse{ErrorCode: protocol.ErrorCode_ErrorCode_PhantomEquipGroupNameEmpty}
	}
	v.player.EchoPresets.Groups[req.Index].Name = req.Name
	save.SaveEchoPresets(v.player)
	return &protocol.ChangePhantomEquipGroupNameResponse{ErrorCode: codeSuccess, PhantomEquipGroupInfos: v.presetList()}
}

func (v *InventoryService) HandleDeletePhantomEquipGroup(req *protocol.DeletePhantomEquipGroupRequest) *protocol.DeletePhantomEquipGroupResponse {
	if !v.presetIndexOK(req.Index) {
		return &protocol.DeletePhantomEquipGroupResponse{ErrorCode: codeParamError}
	}
	v.player.EchoPresets.Groups = slices.Delete(v.player.EchoPresets.Groups, int(req.Index), int(req.Index)+1)
	save.SaveEchoPresets(v.player)
	return &protocol.DeletePhantomEquipGroupResponse{ErrorCode: codeSuccess, PhantomEquipGroupInfos: v.presetList()}
}

// ----- §6.7 calabash and the echo projector -----

func (v *InventoryService) HandleCalabashLevelReward(req *protocol.CalabashLevelRewardRequest, sink PushSink) *protocol.CalabashLevelRewardResponse {
	c := v.player.Calabash
	if !slices.Contains(c.RewardedLevels, req.Level) {
		c.RewardedLevels = append(c.RewardedLevels, req.Level)
		save.SaveCalabash(v.player)
	}
	sink.Push(&protocol.CalabashLevelsRewardNotify{RewardedLevels: slices.Clone(c.RewardedLevels)})
	return &protocol.CalabashLevelRewardResponse{ErrorCode: codeSuccess}
}

func (v *InventoryService) HandlePhantomInteractionEquip(req *protocol.PhantomInteractionEquipRequest, sink PushSink) *protocol.PhantomInteractionEquipResponse {
	ids := req.EquipedMonsterIds
	v.player.Calabash.ProjectorMonsterIDs = append([]int32{}, ids[:min(len(ids), MaxProjectorSlots)]...)
	save.SaveCalabash(v.player)
	sink.Push(v.BuildPhantomInteractionUnlockNotify())
	return &protocol.PhantomInteractionEquipResponse{Code: codeSuccess}
}

func (v *InventoryService) HandlePhantomInteractionSkinChange(req *protocol.PhantomInteractionSkinChangeRequest, sink PushSink) *protocol.PhantomInteractionSkinChangeResponse {
	c := v.player.Calabash
	for _, ch := range req.SkinChangeInfos {
		e := save.ProjectorSkinSave{MonsterID: ch.MonsterId, SkinID: ch.SkinId}
		if i := slices.IndexFunc(c.ProjectorSkins, func(s save.ProjectorSkinSave) bool { return s.MonsterID == ch.MonsterId }); i >= 0 {
			c.ProjectorSkins[i] = e
		} else {
			c.ProjectorSkins = append(c.ProjectorSkins, e)
		}
	}
	save.SaveCalabash(v.player)
	sink.Push(v.BuildPhantomInteractionUnlockNotify())
	return &protocol.PhantomInteractionSkinChangeResponse{Code: codeSuccess}
}

// ----- loading requests (specs/enter-world.md §5) -----

func HandleEnergySync(*protocol.EnergySyncRequest) *protocol.EnergySyncResponse {
	return &protocol.EnergySyncResponse{ErrCode: codeSuccess, SyncInfoList: energyInfos()}
}

func (v *InventoryService) HandleLoadEquipInfo(*protocol.LoadEquipInfoRequest) *protocol.LoadEquipInfoResponse {
	resp := &protocol.LoadEquipInfoResponse{ErrorCode: codeSuccess}
	for _, r := range v.player.Roles.Values() {
		if r.RoleSkinID > 0 {
			resp.EquipList = append(resp.EquipList, &protocol.RoleSkinEquipData{RoleID: r.RoleID, SkinItemId: r.RoleSkinID})
		}
	}
	return resp
}

func (v *InventoryService) HandleRoleSkin(*protocol.RoleSkinRequest) *protocol.RoleSkinResponse {
	return &protocol.RoleSkinResponse{SkinIds: slices.Clone(v.player.CosmeticInfo.RoleSkins)}
}

func HandleLoadCalabashSkinInfo(*protocol.LoadCalabashSkinInfoRequest) *protocol.LoadCalabashSkinInfoResponse {
	return &protocol.LoadCalabashSkinInfoResponse{ErrorCode: codeSuccess}
}

func HandlePhantomFetterRecommend(*protocol.PhantomFetterRecommendRequest) *protocol.PhantomFetterRecommendResponse {
	return &protocol.PhantomFetterRecommendResponse{ErrorCode: codeSuccess}
}

func HandlePhantomAttrRecommend(*protocol.PhantomAttrRecommendRequest) *protocol.PhantomAttrRecommendResponse {
	return &protocol.PhantomAttrRecommendResponse{ErrorCode: codeSuccess}
}

// ----- helpers -----

func props(ps []save.EchoPropSave) []*protocol.PhantomPropInfo {
	var out []*protocol.PhantomPropInfo
	for _, p := range ps {
		out = append(out, &protocol.PhantomPropInfo{PhantomPropId: p.ID, Value: p.Value})
	}
	return out
}

// ToProtoEcho is an echo as the client is sent it.
func ToProtoEcho(e *save.EchoSave) *protocol.PhantomItem {
	return &protocol.PhantomItem{
		Id: e.ID, IncrId: e.UniqueID, FuncValue: e.FuncValue, PhantomLevel: e.Level, PhantomExp: e.Exp,
		FetterGroupId: e.FetterGroupID, SkinId: e.SkinID, StackCount: 1, AddTime: 0,
		PhantomMainProp: props(e.MainProp), PhantomSubProp: props(e.SubProp), UnAckSubProp: props(e.UnackSubProp),
		LockPropIndex: slices.Clone(e.LockPropIndex),
	}
}

func (v *InventoryService) rolePhantomEquipInfo(roleID int32) *protocol.RolePhantomEquipInfo {
	info := &protocol.RolePhantomEquipInfo{RoleId: roleID}
	for _, e := range v.player.EquippedEchoes(roleID) {
		var id int32
		if e != nil {
			id = e.UniqueID
		}
		info.PhantomItemIncrId = append(info.PhantomItemIncrId, id)
	}
	return info
}

func (v *InventoryService) validateEchoSlots(slots [5]*save.EchoSave) protocol.ErrorCode {
	var cost int32
	seen := map[int32]bool{}
	for _, e := range slots {
		if e == nil {
			continue
		}
		cfg, ok := v.tables.PhantomItemByItemId[e.ID]
		if !ok {
			continue
		}
		if seen[cfg.MonsterId] {
			return protocol.ErrorCode_ErrorCode_ErrPhantomEquipDuplicate
		}
		seen[cfg.MonsterId] = true
		if r, ok := v.tables.PhantomRarityByRare[cfg.Rarity]; ok {
			cost += r.Cost
		}
	}
	if cost > v.MaxEchoCost() {
		return protocol.ErrorCode_ErrorCode_ErrPhantomEquipTargetCost
	}
	return codeSuccess
}

func (v *InventoryService) applyFiveSlotEchoEquip(roleID int32, incrIDs []int32, nowMs int64, sink PushSink) (protocol.ErrorCode, []int32) {
	affected := []int32{roleID}
	if !v.player.Roles.Has(roleID) {
		return codeParamError, affected
	}
	var desired [5]*save.EchoSave
	before := v.captureEchoRoleState(roleID)
	for i := 0; i < min(5, len(incrIDs)); i++ {
		uid := incrIDs[i]
		if uid <= 0 {
			continue
		}
		e, ok := v.player.Echoes.Get(uid)
		if !ok {
			return protocol.ErrorCode_ErrorCode_ErrPhantomNotExist, affected
		}
		desired[i] = e
		if e.RoleID != nil && *e.RoleID != roleID {
			o := *e.RoleID
			affected = append(affected, o)
			if _, ok := before[o]; !ok {
				before[o] = v.captureSingle(o)
			}
		}
	}
	if err := v.validateEchoSlots(desired); err != codeSuccess {
		return err, affected
	}
	for _, p := range v.player.EquippedEchoes(roleID) {
		if p != nil {
			unequip(v.player, p)
		}
	}
	for i, e := range desired {
		if e != nil {
			equipTo(v.player, e, roleID, int32(i))
		}
	}
	v.syncAffectedRoles(affected, before, nowMs, sink)
	return codeSuccess, affected
}

type echoRoleBefore struct {
	main, combo int32
	echoBuffs   []int64
	fetterBuffs []int64
}

func (v *InventoryService) captureEchoRoleState(roleID int32) map[int32]echoRoleBefore {
	return map[int32]echoRoleBefore{roleID: v.captureSingle(roleID)}
}

func (v *InventoryService) captureSingle(roleID int32) echoRoleBefore {
	m, c := v.concomitants.VisionTopology(v.player, roleID)
	role, ok := v.player.Roles.Get(roleID)
	if !ok {
		return echoRoleBefore{m, c, nil, nil}
	}
	s := ComputeLive(v.tables, v.player, role, v.buffBuilder.RoleAutoBuffTags(roleID))
	return echoRoleBefore{m, c, s.EchoBuffIDs, s.FetterBuffIDs}
}

func (v *InventoryService) syncAffectedRoles(affected []int32, before map[int32]echoRoleBefore, nowMs int64, sink PushSink) {
	roles := sortedSet(affected)
	pn := &protocol.RolePhantomPropUpdateNotify{}
	for _, rid := range roles {
		if role, ok := v.player.Roles.Get(rid); ok {
			pn.PropInfo = append(pn.PropInfo, ComputeLive(v.tables, v.player, role, v.buffBuilder.RoleAutoBuffTags(rid)).ToRolePhantomPropInfo(rid))
		}
	}
	if len(pn.PropInfo) > 0 {
		sink.Push(pn)
	}
	for _, rid := range roles {
		live := v.formation.FindLiveRoleEntity(rid)
		if live == nil {
			v.roles.RefreshLiveRoleAttributes(rid, sink)
			continue
		}
		nm, nc := v.concomitants.VisionTopology(v.player, rid)
		b, had := before[rid]
		om, oc := live.MainEchoSummonID, live.ComboEchoSummonID
		if had {
			om, oc = b.main, b.combo
		}
		if nm != om || nc != oc {
			v.formation.RebuildLiveRoleEntity(rid, nowMs, sink)
			v.roles.RefreshLiveRoleAttributes(rid, sink)
			continue
		}
		main, combo := v.concomitants.ResolveVisionSummons(v.player, rid)
		var mainVid, comboVid int64
		if len(live.VisionEntityIDs) > 0 {
			mainVid = live.VisionEntityIDs[0]
		}
		if len(live.VisionEntityIDs) > 1 {
			comboVid = live.VisionEntityIDs[1]
		}
		live.VisionSkillComponent = v.concomitants.BuildVisionSkillComponent(main, mainVid, combo, comboVid)
		vn := &protocol.VisionSkillChangeNotify{EntityId: live.NetID}
		if live.VisionSkillComponent.PhantomSkillInfo != nil {
			vn.PhantomSkillInfo = proto.Clone(live.VisionSkillComponent.PhantomSkillInfo).(*protocol.VisionSkillInformation)
		}
		for _, x := range live.VisionSkillComponent.VisionSkillInfos {
			vn.VisionSkillInfos = append(vn.VisionSkillInfos, proto.Clone(x).(*protocol.VisionSkillInformation))
		}
		sink.Push(vn)
		if role, ok := v.player.Roles.Get(rid); ok {
			after := ComputeLive(v.tables, v.player, role, v.buffBuilder.RoleAutoBuffTags(rid))
			v.buffs.SwapEquipmentBuffsS2C(live, b.echoBuffs, after.EchoBuffIDs, EchoRemoveReason, EchoApplyReason, sink)
			v.buffs.SwapEquipmentBuffsS2C(live, b.fetterBuffs, after.FetterBuffIDs, FetterRemoveReason, EchoApplyReason, sink)
		}
		v.roles.RefreshLiveRoleAttributes(rid, sink)
	}
}

// polishCheck finds the echo and the main-stat group a polish asks for.
func (v *InventoryService) polishCheck(incrID, mainPropItemID int32) (protocol.ErrorCode, *save.EchoSave, *data.PhantomMainProperty) {
	e, ok := v.player.Echoes.Get(incrID)
	if !ok {
		return protocol.ErrorCode_ErrorCode_ErrPhantomNotExist, nil, nil
	}
	if e.Level > 0 {
		return protocol.ErrorCode_ErrorCode_PhantomPolishHadLevelUp, nil, nil
	}
	it, ok := v.tables.PhantomItemByItemId[e.ID]
	if !ok || it.MainProp == nil {
		return protocol.ErrorCode_ErrorCode_ErrPhantomConfigNotFound, nil, nil
	}
	for _, g := range FindValidMainPropGroups(v.tables, it.MainProp.RandGroupId) {
		if slices.Contains(g.PropGroup, mainPropItemID) {
			return codeSuccess, e, g
		}
	}
	return protocol.ErrorCode_ErrorCode_ErrPhantomMainPropNotMatch, nil, nil
}

// polishCosts is PhantomRarity(PhantomItem.Rarity).PolishCost (client PhantomDataBase.GetRareConfig).
func (v *InventoryService) polishCosts(e *save.EchoSave) []Cost {
	if it, ok := v.tables.PhantomItemByItemId[e.ID]; ok {
		if r, ok := v.tables.PhantomRarityByRare[it.Rarity]; ok {
			return AddCosts(nil, r.PolishCost, 1)
		}
	}
	return nil
}

func (v *InventoryService) polishApply(e *save.EchoSave, group *data.PhantomMainProperty, nowMs int64, sink PushSink) {
	before := v.beforeOf(e)
	e.MainProp = BuildMainProps(v.tables, group, 0)
	v.afterEchoChange(e, before, nowMs, sink)
}

func (v *InventoryService) syncRoleWeaponOnScene(roleID int32, w *save.WeaponSave, oldBuffs []int64, sink PushSink) {
	if live := v.formation.FindLiveRoleEntity(roleID); live != nil {
		live.WeaponID, live.WeaponBreachLevel = 0, 0
		if w != nil {
			live.WeaponID, live.WeaponBreachLevel = w.ID, w.Breach
		}
		sink.Push(&protocol.EntityEquipChangeNotify{EntityId: live.NetID, EquipComponent: &protocol.EquipComponentPb{WeaponId: live.WeaponID, WeaponBreachLevel: live.WeaponBreachLevel}})
		v.buffs.SwapEquipmentBuffsS2C(live, oldBuffs, v.weaponBuffs(w), WeaponRemoveReason, WeaponApplyReason, sink)
	}
	v.roles.RefreshLiveRoleAttributes(roleID, sink)
}

func (v *InventoryService) weaponBuffs(w *save.WeaponSave) []int64 {
	if w == nil {
		return []int64{}
	}
	wc, ok := v.tables.WeaponConfByItemId[w.ID]
	if !ok {
		return []int64{}
	}
	return v.weaponResonBuffs(wc.ResonId, w.ResonLevel)
}

func (v *InventoryService) weaponResonBuffs(resonID, level int32) []int64 {
	out := []int64{}
	for _, r := range v.tables.WeaponReson {
		if r.ResonId == resonID && r.Level == level {
			for _, raw := range r.Effect {
				if id := int64(raw); id != 0 {
					out = append(out, id)
				}
			}
			break
		}
	}
	return out
}

func (v *InventoryService) isConsumableDuplicate(target *save.WeaponSave, uid int32) bool {
	if uid <= 0 || uid == target.UniqueID {
		return false
	}
	o, ok := v.player.Weapons.Get(uid)
	return ok && o.ID == target.ID && o.RoleID == nil && o.FuncValue != weaponLockedFuncValue
}

func (v *InventoryService) weaponLevelExp(levelID, level int32) int32 {
	for _, r := range v.tables.WeaponLevel {
		if r.LevelId == levelID && r.Level == level {
			return r.Exp
		}
	}
	return 0
}

func (v *InventoryService) phantomLevelExp(group, level int32) int32 {
	for _, r := range v.tables.PhantomLevel {
		if r.GroupId == group && r.Level == level {
			return r.Exp
		}
	}
	return 0
}
