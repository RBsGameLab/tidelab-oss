// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

func costMap(kv ...int32) *data.Map[int32, int32] {
	m := &data.Map[int32, int32]{}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Pairs = append(m.Pairs, data.Pair[int32, int32]{Key: kv[i], Value: kv[i+1]})
	}
	return m
}

func TestAddCostsMergesAndSkipsEmpty(t *testing.T) {
	c := AddCosts(nil, costMap(5, 2, 2, 100, 7, 0), 1)
	c = AddCosts(c, costMap(2, 50, 6, 1), 2)
	c = AddCosts(c, nil, 1)
	want := []Cost{{Key: 5, Value: 2}, {Key: 2, Value: 200}, {Key: 6, Value: 2}}
	must(t, len(c) == len(want), c)
	for i := range want {
		must(t, c[i] == want[i], c)
	}
}

// R-158: skill level-up, quick level-up and skill-tree nodes pay SkillLevel / SkillTree costs;
// short → ErrItemNotEnough with nothing taken; 贝币 from the attribute.
func TestRoleSkillCostsArePaid(t *testing.T) {
	const mat = 43020011
	tables := syntheticTables()
	for _, s := range tables.Skill {
		if s.Id == 1002 {
			s.SkillLevelGroupId = 77
		}
	}
	tables.SkillLevel = []*data.SkillLevel{
		{Id: 1, SkillLevelGroupId: 77, SkillId: 1, Consume: costMap()},
		{Id: 2, SkillLevelGroupId: 77, SkillId: 2, Consume: costMap(mat, 2, 2, 300)},
		{Id: 3, SkillLevelGroupId: 77, SkillId: 3, Consume: costMap(mat, 2, 2, 300)},
		{Id: 4, SkillLevelGroupId: 77, SkillId: 4, Consume: costMap(mat, 3, 2, 1000)},
		{Id: 5, SkillLevelGroupId: 88, SkillId: 2, Consume: costMap(mat, 99)},
	}
	for _, n := range tables.SkillTree {
		if n.Id == 102 {
			n.Consume = costMap(2, 500)
		}
	}
	tables.ItemInfo = append(tables.ItemInfo, &data.ItemInfo{Id: mat})
	tables.Reindex()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables, nowMs: 1000})
	sink := &recordingSink{}
	p := ctx.Player
	role := p.Roles.At(1211)
	setSkill(role, skillIndex(role, 1002), 1002, 1)
	p.BasicInfo.Attributes.Coin = 1000
	p.NormalInventory.SetBatch([]save.ItemCount{{ID: mat, Count: 5}})
	level := func() int32 { return role.Skills[skillIndex(role, 1002)].Value }
	coin := func() int32 { return p.BasicInfo.Attributes.Coin }
	notEnough := protocol.ErrorCode_ErrorCode_ErrItemNotEnough

	up := ctx.Roles.HandleUpLevelSkill(&protocol.PbUpLevelSkillRequest{RoleId: 1211, SkillId: 1002}, 1000, sink)
	must(t, up.Code == codeSuccess && level() == 2 && coin() == 700 && p.NormalInventory.CountOf(mat) == 3, up)
	must(t, len(ofType[*protocol.PlayerAttrNotify](sink)) == 1 && len(ofType[*protocol.NormalItemUpdateNotify](sink)) == 1, sink)

	sink.clear()
	quick := ctx.Roles.HandleSkillQuickLevelUp(&protocol.RoleSkillQuickLevelUpRequest{RoleId: 1211, SkillId: 1002, TargetLevel: 4}, 1100, sink)
	must(t, quick.ErrorCode == notEnough && level() == 2 && coin() == 700 && p.NormalInventory.CountOf(mat) == 3, quick)
	must(t, len(sink.msgs) == 0, sink.msgs)

	p.BasicInfo.Attributes.Coin = 2000
	p.NormalInventory.SetBatch([]save.ItemCount{{ID: mat, Count: 5}})
	quick = ctx.Roles.HandleSkillQuickLevelUp(&protocol.RoleSkillQuickLevelUpRequest{RoleId: 1211, SkillId: 1002, TargetLevel: 4}, 1200, sink)
	must(t, quick.ErrorCode == codeSuccess && level() == 4 && coin() == 700 && p.NormalInventory.CountOf(mat) == 0, quick)
	removed := ofType[*protocol.NormalItemRemoveNotify](sink)
	must(t, len(removed) == 1 && removed[0].NormalItemIdList[0] == mat, removed)

	p.BasicInfo.Attributes.Coin = 400
	act := ctx.Roles.HandleActivateSkill(&protocol.RoleActivateSkillRequest{RoleId: 1211, SkillNodeId: 102}, 1300, sink)
	must(t, act.Code == notEnough && coin() == 400, act)
	p.BasicInfo.Attributes.Coin = 700
	act = ctx.Roles.HandleActivateSkill(&protocol.RoleActivateSkillRequest{RoleId: 1211, SkillNodeId: 102}, 1400, sink)
	must(t, act.Code == codeSuccess && coin() == 200, act)
	act = ctx.Roles.HandleActivateSkill(&protocol.RoleActivateSkillRequest{RoleId: 1211, SkillNodeId: 102}, 1500, sink)
	must(t, act.Code == codeSuccess && coin() == 200, "an active node is not paid again")
}

// On the real 3.8 data (skipped without it): every skill a role levels up (node types 1, 2) has
// a SkillLevel row with a 贝币 cost for each level 2 … MaxSkillLevel, and every cost item is an
// ItemInfo row, so no level is free by a missing row.
func TestRealDataSkillCostsAreComplete(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "RoleInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := &RoleService{tables: tb}
	checked := 0
	for _, n := range tb.SkillTree {
		if n.NodeType != 1 && n.NodeType != 2 {
			continue
		}
		cfg, ok := tb.SkillById[n.SkillId]
		must(t, ok && cfg.SkillLevelGroupId != 0, n.Id, n.SkillId)
		for lv := int32(2); lv <= cfg.MaxSkillLevel; lv++ {
			costs := r.skillLevelCosts(cfg, lv-1, lv)
			must(t, slices.ContainsFunc(costs, func(c Cost) bool { return c.Key == MoneyShellCredit }), n.SkillId, lv, costs)
			for _, c := range costs {
				_, ok := tb.ItemInfoById[c.Key]
				must(t, ok, c.Key)
			}
			checked++
		}
	}
	must(t, checked > 1000, checked)
}

// R-158: weapon level-up pays exp items and 贝币 (WeaponExpItem.Cost, WeaponQualityInfo.Cost for
// a weapon used as material, which is deleted), refunds exp past the limit as exp items
// (ItemMap); breach pays WeaponBreach(b) Consume + GoldConsume at the level limit; resonance pays
// WeaponReson(L).GoldConsume. Short → refused with nothing taken.
func TestWeaponCostsArePaid(t *testing.T) {
	const expBig, expSmall, breachMat = 43020001, 43020002, 41100011
	tables := syntheticTables()
	tables.WeaponExpItem = []*data.WeaponExpItem{{Id: expBig, BasicExp: 150, Cost: 60}, {Id: expSmall, BasicExp: 50, Cost: 20}}
	tables.WeaponQualityInfo = []*data.WeaponQualityInfo{{Id: 4, BasicExp: 120, Cost: 40}, {Id: 5, BasicExp: 200, Cost: 100}}
	tables.WeaponBreach = []*data.WeaponBreach{
		{Id: 1, BreachId: 1, Level: 0, LevelLimit: 4, Consume: costMap(breachMat, 5), GoldConsume: 500},
		{Id: 2, BreachId: 1, Level: 1, LevelLimit: 40, Consume: costMap(breachMat, 10), GoldConsume: 900},
		{Id: 3, BreachId: 1, Level: 2, LevelLimit: 50, Consume: costMap()},
	}
	for _, r := range tables.WeaponReson {
		if r.ResonId == 101 && r.Level == 1 {
			r.GoldConsume = 300
		}
	}
	tables.ItemInfo = append(tables.ItemInfo, &data.ItemInfo{Id: expSmall})
	tables.Reindex()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables, nowMs: 1000})
	sink := &recordingSink{}
	p := ctx.Player
	w := p.Weapons.At(p.Roles.At(1211).Weapon)
	must(t, w.Level == 1 && w.Exp == 0 && w.Breach == 0, w)
	newWeapon := func(id, funcValue int32) *save.WeaponSave {
		uid := p.SpecialItemIDs.Next()
		nw := save.NewWeapon(uid)
		nw.ID, nw.FuncValue = id, funcValue
		p.Weapons.Set(uid, nw)
		save.SaveWeapon(p, nw)
		return nw
	}
	fodder, locked := newWeapon(21020074, 0), newWeapon(21020074, weaponLockedFuncValue)
	equipped := p.Roles.At(1108).Weapon
	coin := func() int32 { return p.BasicInfo.Attributes.Coin }
	count := p.NormalInventory.CountOf
	levelUp := func(items ...*protocol.WeaponConsumeItem) *protocol.WeaponLevelUpResponse {
		return ctx.Inventory.HandleWeaponLevelUp(&protocol.WeaponLevelUpRequest{IncId: w.UniqueID, ConsumeList: items}, 1100, sink)
	}
	item := func(id, n int32) *protocol.WeaponConsumeItem {
		return &protocol.WeaponConsumeItem{ItemId: id, Count: n}
	}
	weapon := func(uid int32) *protocol.WeaponConsumeItem {
		return &protocol.WeaponConsumeItem{ItemId: p.Weapons.At(uid).ID, IncId: uid, Count: 1}
	}

	must(t, levelUp(weapon(locked.UniqueID)).ErrorCode == protocol.ErrorCode_ErrorCode_ErrWeaponLocked, "locked material")
	must(t, levelUp(weapon(equipped)).ErrorCode == protocol.ErrorCode_ErrorCode_ErrWeaponEquiped, "equipped material")
	must(t, levelUp(weapon(w.UniqueID)).ErrorCode == protocol.ErrorCode_ErrorCode_ErrWeaponConsumeSelf, "itself")

	p.BasicInfo.Attributes.Coin = 100
	p.NormalInventory.SetBatch([]save.ItemCount{{ID: expBig, Count: 4}})
	short := levelUp(item(expBig, 2), weapon(fodder.UniqueID)) // 120 + 40 贝币 > 100
	must(t, short.ErrorCode == protocol.ErrorCode_ErrorCode_ErrWeaponConsumeInvalid && coin() == 100 && count(expBig) == 4 &&
		p.Weapons.Has(fodder.UniqueID) && w.Level == 1, short)

	p.BasicInfo.Attributes.Coin = 1000
	sink.clear()
	lv := levelUp(item(expBig, 2), weapon(fodder.UniqueID)) // 300 + 120 exp: 1 → 3, exp 120
	must(t, lv.ErrorCode == codeSuccess && w.Level == 3 && w.Exp == 120 && coin() == 840 && count(expBig) == 2 &&
		!p.Weapons.Has(fodder.UniqueID) && len(lv.ItemMap) == 0, lv)
	must(t, len(ofType[*protocol.WeaponItemRemoveNotify](sink)) == 1, "no WeaponItemRemoveNotify")

	sink.clear()
	small := count(expSmall)
	lv = levelUp(item(expBig, 2)) // 300 exp, 180 to the limit 4: 120 over → 2 × 50 back
	must(t, lv.ErrorCode == codeSuccess && w.Level == 4 && w.Exp == 0 && coin() == 720 && count(expBig) == 0 &&
		lv.ItemMap[expSmall] == 2 && len(lv.ItemMap) == 1 && count(expSmall) == small+2, lv)
	must(t, len(ofType[*protocol.NormalItemAddNotify](sink)) == 1, "no NormalItemAddNotify")
	must(t, levelUp(item(expSmall, 1)).ErrorCode == protocol.ErrorCode_ErrorCode_ErrWeaponLevelLimit, "at the limit")

	breach := func() protocol.ErrorCode {
		return ctx.Inventory.HandleWeaponBreach(&protocol.WeaponBreachRequest{IncId: w.UniqueID}, 1200, sink).ErrorCode
	}
	p.NormalInventory.SetBatch([]save.ItemCount{{ID: breachMat, Count: 4}})
	must(t, breach() == protocol.ErrorCode_ErrorCode_ErrWeaponConsumeInvalid && w.Breach == 0 && coin() == 720 && count(breachMat) == 4, "breach short")
	p.NormalInventory.SetBatch([]save.ItemCount{{ID: breachMat, Count: 5}})
	must(t, breach() == codeSuccess && w.Breach == 1 && coin() == 220 && count(breachMat) == 0, "breach paid")
	must(t, breach() == protocol.ErrorCode_ErrorCode_ErrWeaponLevelUpLevel && w.Breach == 1, "breach below the limit")
	w.Breach = 2
	must(t, breach() == protocol.ErrorCode_ErrorCode_ErrWeaponBreachLimit, "no next breach")

	dup := newWeapon(w.ID, 0)
	reson := func() protocol.ErrorCode {
		return ctx.Inventory.HandleWeaponResonUp(&protocol.WeaponResonUpRequest{IncId: w.UniqueID, ConsumeList: []int32{dup.UniqueID}}, 1300, sink).ErrorCode
	}
	must(t, reson() == protocol.ErrorCode_ErrorCode_ErrWeaponResonConsumeGold && w.ResonLevel == 1 && p.Weapons.Has(dup.UniqueID), "reson short")
	p.BasicInfo.Attributes.Coin = 300
	must(t, reson() == codeSuccess && w.ResonLevel == 2 && coin() == 0 && !p.Weapons.Has(dup.UniqueID), "reson paid")
}

// On the real 3.8 data: every weapon exp item, weapon quality and breach step below the top has
// a 贝币 price, so no weapon upgrade is free by a missing field.
func TestRealDataWeaponCostsAreComplete(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "RoleInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	must(t, len(tb.WeaponExpItem) == 4 && len(tb.WeaponQualityInfo) == 5, len(tb.WeaponExpItem), len(tb.WeaponQualityInfo))
	for _, e := range tb.WeaponExpItem {
		must(t, e.Cost > 0 && e.BasicExp > 0, e.Id)
	}
	for _, wc := range tb.WeaponConf {
		q, ok := tb.WeaponQualityInfoById[wc.QualityId]
		must(t, ok && q.Cost > 0 && q.BasicExp > 0, wc.ItemId, wc.QualityId)
	}
	top := map[int32]int32{}
	for _, b := range tb.WeaponBreach {
		top[b.BreachId] = max(top[b.BreachId], b.Level)
	}
	for _, b := range tb.WeaponBreach {
		if b.Level < top[b.BreachId] {
			must(t, b.GoldConsume > 0 && len(b.Consume.Pairs) > 0, b.Id)
		}
	}
}

// R-158: echo level-up pays exp items, 0.1 贝币 per exp used and the auto tunings; an echo used
// as material gives 0.75 of its exp and 30 % of its tuning items back (netted against the
// tunings), and is deleted; exp past the limit comes back as exp items. Tuning pays
// PhantomQuality IdentifyCost + IdentifyCoin, polish PhantomRarity.PolishCost, the sub-stat
// reroll PhantomVicePolishConfig(locked count).Cost. Short → refused, nothing taken.
func TestEchoCostsArePaid(t *testing.T) {
	const expBig, expSmall, tuner, polishItem, viceItem = 43010001, 43010002, 43000001, 43000002, 43000003
	tables := syntheticTables()
	tables.PhantomLevel = []*data.PhantomLevel{{Id: 1, GroupId: 1, Level: 0, Exp: 0}}
	for l := int32(1); l <= 25; l++ {
		tables.PhantomLevel = append(tables.PhantomLevel, &data.PhantomLevel{Id: 1 + l, GroupId: 1, Level: l, Exp: 100})
	}
	tables.PhantomExpItem = []*data.PhantomExpItem{{ItemId: expBig, Exp: 500}, {ItemId: expSmall, Exp: 100}}
	for _, q := range tables.PhantomQuality {
		if q.Quality == 5 {
			q.IdentifyCost, q.IdentifyCoin = costMap(tuner, 10), 500
		}
	}
	for _, r := range tables.PhantomRarity {
		if r.Rare == 3 {
			r.PolishCost = costMap(polishItem, 1)
		}
	}
	tables.PhantomVicePolishConfig = []*data.PhantomVicePolishConfig{{PropCount: 1, Cost: costMap(viceItem, 2)}}
	tables.Reindex()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables, nowMs: 1000, rng: NewDotNetRandom(42), seedDefaultEchoes: true})
	sink := &recordingSink{}
	p := ctx.Player
	a, b := echoWithItem(ctx, 600001), echoWithItem(ctx, 600002)
	must(t, a.Level == 0 && a.RoleID == nil && b.RoleID == nil, a, b)
	b.Level, b.Exp = 2, 50
	a.SubProp = nil
	b.SubProp = []save.EchoPropSave{{ID: 3, Value: 10}}
	coin := func() int32 { return p.BasicInfo.Attributes.Coin }
	count := p.NormalInventory.CountOf
	set := func(kv ...int32) {
		var batch []save.ItemCount
		for i := 0; i+1 < len(kv); i += 2 {
			batch = append(batch, save.ItemCount{ID: kv[i], Count: kv[i+1]})
		}
		p.NormalInventory.SetBatch(batch)
	}
	notEnough := protocol.ErrorCode_ErrorCode_ErrItemNotEnough

	set(polishItem, 0)
	must(t, ctx.Inventory.HandlePhantomPolish(&protocol.PhantomPolishRequest{IncrId: a.UniqueID, PhantomMainPropItemId: 5002}, 1100, sink).ErrorCode == notEnough, "polish short")
	set(polishItem, 1)
	must(t, ctx.Inventory.HandlePhantomPolish(&protocol.PhantomPolishRequest{IncrId: a.UniqueID, PhantomMainPropItemId: 5002}, 1100, sink).ErrorCode == codeSuccess &&
		count(polishItem) == 0, "polish paid")

	// b: (100 + 100 + 50) × 0.75 = 187 exp, 3 tuners back; + 500 → 687: level 6, exp 87, 68 贝币;
	// one tuning: 10 tuners − 3 back, 500 贝币.
	levelUp := func(identify int32, items ...*protocol.PhantomConsumeItem) *protocol.PhantomLevelUpResponse {
		return ctx.Inventory.HandlePhantomLevelUp(&protocol.PhantomLevelUpRequest{IncId: a.UniqueID, IdentifyCount: identify, ConsumeList: items}, 1200, sink)
	}
	withB := []*protocol.PhantomConsumeItem{{IncId: b.UniqueID, ItemId: b.ID, Count: 1}, {ItemId: expBig, Count: 1}}
	p.BasicInfo.Attributes.Coin = 567
	set(expBig, 1, tuner, 7)
	short := levelUp(1, withB...)
	must(t, short.ErrorCode == protocol.ErrorCode_ErrorCode_ErrPhantomLevelUpConsumeItemNotEnough && coin() == 567 && count(expBig) == 1 &&
		count(tuner) == 7 && p.Echoes.Has(b.UniqueID) && a.Level == 0, short)
	p.BasicInfo.Attributes.Coin = 568
	sink.clear()
	lv := levelUp(1, withB...)
	must(t, lv.ErrorCode == codeSuccess && a.Level == 6 && a.Exp == 87 && len(a.SubProp) == 1 && coin() == 0 && count(expBig) == 0 &&
		count(tuner) == 0 && !p.Echoes.Has(b.UniqueID) && len(lv.ItemMap) == 0, lv, a.Level, a.Exp, coin())
	must(t, len(ofType[*protocol.PhantomItemRemoveNotify](sink)) == 1, "no PhantomItemRemoveNotify")

	// 2000 exp, 1813 to level 25: 181 贝币, 187 over → one 100-exp item back.
	p.BasicInfo.Attributes.Coin = 181
	set(expBig, 4, expSmall, 0)
	lv = levelUp(0, &protocol.PhantomConsumeItem{ItemId: expBig, Count: 4})
	must(t, lv.ErrorCode == codeSuccess && a.Level == 25 && a.Exp == 0 && coin() == 0 && count(expBig) == 0 && count(expSmall) == 1 &&
		lv.ItemMap[expSmall] == 1 && len(lv.ItemMap) == 1, lv, coin())
	must(t, levelUp(0, &protocol.PhantomConsumeItem{ItemId: expSmall, Count: 1}).ErrorCode == protocol.ErrorCode_ErrorCode_ErrPhantomLvupMax, "at the limit")

	identify := func() protocol.ErrorCode {
		return ctx.Inventory.HandlePhantomIdentify(&protocol.PhantomIdentifyRequest{IncrId: a.UniqueID, Count: 1}, 1300, sink).ErrorCode
	}
	p.BasicInfo.Attributes.Coin = 499
	set(tuner, 10)
	must(t, identify() == notEnough && len(a.SubProp) == 1 && count(tuner) == 10, "tune short")
	p.BasicInfo.Attributes.Coin = 500
	must(t, identify() == codeSuccess && len(a.SubProp) == 2 && count(tuner) == 0 && coin() == 0, "tune paid")

	vice := func() protocol.ErrorCode {
		return ctx.Inventory.HandlePhantomVicePolish(&protocol.PhantomVicePolishRequest{IncrId: a.UniqueID, LockPropIndex: []int32{0}}, sink).ErrorCode
	}
	set(viceItem, 1)
	must(t, vice() == notEnough && len(a.UnackSubProp) == 0, "reroll short")
	set(viceItem, 2)
	must(t, vice() == codeSuccess && len(a.UnackSubProp) == 2 && count(viceItem) == 0, "reroll paid")
}

// On the real 3.8 data: every echo quality has a tuning price (items and 贝币), every rarity a
// polish price, every locked count 0 … 4 a reroll price, and exp from level 0 starts at 0.
func TestRealDataEchoCostsAreComplete(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "RoleInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range tb.PhantomQuality {
		must(t, q.IdentifyCoin > 0 && len(q.IdentifyCost.Pairs) == 1, q.Quality)
	}
	for _, r := range tb.PhantomRarity {
		must(t, len(r.PolishCost.Pairs) > 0, r.Rare)
	}
	for n := int32(0); n <= 4; n++ {
		c, ok := tb.PhantomVicePolishConfigByPropCount[n]
		must(t, ok && len(c.Cost.Pairs) > 0, n)
	}
	for _, l := range tb.PhantomLevel {
		must(t, (l.Level == 0) == (l.Exp == 0), l.GroupId, l.Level, l.Exp)
	}
}

// On the real 3.8 data: every MaterialReplace row has an ExchangeParam ratio and a group of at
// least two items; every ItemExchangeContent row has a price.
func TestRealDataExchangeCostsAreComplete(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "RoleInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[int32]int{}
	for _, m := range tb.MaterialReplace {
		groups[m.GroupId]++
		e, ok := tb.ExchangeParamById[m.ExchangeParam]
		must(t, ok && e.Target != nil && e.Target.Item2 > 0, m.ItemId)
	}
	for g, n := range groups {
		must(t, n >= 2, g)
	}
	must(t, len(tb.ItemExchangeContent) > 0, "no ItemExchangeContent")
	for _, c := range tb.ItemExchangeContent {
		must(t, c.GainCount > 0 && len(c.Consume.Pairs) > 0, c.ItemId)
	}
}
