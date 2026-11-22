// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// specs/inventory.md §8 (InventoryTests; the snapshot-and-log test is in package save).

const ok = protocol.ErrorCode_ErrorCode_Success

func TestSharedUniqueIDCounterIncreasesAcrossWeaponsEchoesAndPresetsAndReload(t *testing.T) {
	dir := t.TempDir()
	ctx := createContext(t, dir, ctxOpts{seedDefaultEchoes: true})
	id1, id2 := ctx.Player.SpecialItemIDs.Next(), ctx.Player.SpecialItemIDs.Next()
	must(t, id2 == id1+1, id1, id2)
	e := ctx.Player.Echoes.Values()[0]
	e.RoleID, e.EquippedPos = i32p(1211), 0
	save.SaveEcho(ctx.Player, e)
	must(t, ctx.Inventory.HandleAddPhantomEquipGroup(&protocol.AddPhantomEquipGroupRequest{RoleId: 1211, Name: "PresetA"}).ErrorCode == ok, "preset")
	preset := ctx.Player.EchoPresets.Groups[0].IncrID
	must(t, preset == id2+1, preset)
	must(t, save.LoadPlayer(dir, 777).SpecialItemIDs.Next() > preset, "counter after reload")
}

func TestStrayWeaponMigrationAndItemInfoFilterAndCoinOverrideWork(t *testing.T) {
	dir := t.TempDir()
	tables := syntheticTables()
	pd := filepath.Join(dir, "player", "777")
	os.MkdirAll(pd, 0o755)
	save.WriteAtomic(filepath.Join(pd, "inventory_info"), ".{ .normal_items = .{ .{ .id = 21020053, .count = 2 }, .{ .id = 999999, .count = 5 }, .{ .id = 41100011, .count = 42 }, .{ .id = 2, .count = 123 } } }\n")
	ctx := createContext(t, dir, ctxOpts{tables: tables})
	ctx.Player.BasicInfo.Attributes.Coin = 987654
	n := 0
	for _, w := range ctx.Player.Weapons.All() {
		if w.ID == 21020053 {
			n++
		}
	}
	must(t, ctx.Player.NormalInventory.CountOf(21020053) == 0 && n >= 2, n)
	resp := ctx.Inventory.HandleNormalItem(&protocol.NormalItemRequest{})
	count := map[int32]int32{}
	for _, it := range resp.NormalItemList {
		_, known := tables.ItemInfoById[it.Id]
		must(t, known, it.Id)
		count[it.Id] = it.Count
	}
	must(t, count[2] == 987654 && count[41100011] == 42, count)
	sink := &recordingSink{}
	must(t, !ctx.Inventory.PushFilteredNormalItemUpdate([]save.ItemCount{{ID: 999999, Count: 5}}, true, sink) &&
		!ctx.Inventory.PushFilteredNormalItemAdd([]save.ItemCount{{ID: 21020053, Count: 1}}, 0, false, sink) && len(sink.msgs) == 0, sink.msgs)
}

func s2cNotifies(sink *recordingSink) []*protocol.CombatNotifyData {
	var out []*protocol.CombatNotifyData
	for _, p := range ofType[*protocol.CombatReceivePackNotify](sink) {
		for _, d := range p.Data {
			if n := d.GetCombatNotifyData(); n != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func TestWeaponLevelUpBreachRefinementEquipSwapAndLockDisuseWork(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{nowMs: 1000})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	w1211, w1108 := ctx.Player.Roles.At(1211).Weapon, ctx.Player.Roles.At(1108).Weapon
	w := ctx.Player.Weapons.At(w1211)
	must(t, w.ID == 21010011, w.ID)

	must(t, ctx.Inventory.HandleItemLock(&protocol.ItemLockRequest{IncrId: w.UniqueID, Oper: 1}, 1100, sink).ErrCode == ok && w.FuncValue == 1, "lock")
	fv := ofType[*protocol.ItemFuncValueUpdateNotify](sink)
	must(t, fv[len(fv)-1].FuncValue == 1, fv)
	must(t, ctx.Inventory.HandleItemDisuse(&protocol.ItemDisuseRequest{IncrId: w.UniqueID, Oper: 1}, 1200, sink).ErrCode == ok && w.FuncValue == 2, "disuse")

	ctx.Player.NormalInventory.SetBatch([]save.ItemCount{{ID: 43020001, Count: 5}, {ID: 41100011, Count: 5}})
	lv := ctx.Inventory.HandleWeaponLevelUp(&protocol.WeaponLevelUpRequest{IncId: w.UniqueID, ConsumeList: []*protocol.WeaponConsumeItem{{ItemId: 43020001, Count: 2}}}, 1300, sink)
	must(t, lv.ErrorCode == ok && lv.WeaponLevel == 3 && lv.WeaponExp == 0 && ctx.Player.NormalInventory.CountOf(43020001) == 3, lv)

	sink.clear()
	w.Level = 20
	br := ctx.Inventory.HandleWeaponBreach(&protocol.WeaponBreachRequest{IncId: w.UniqueID}, 1400, sink)
	must(t, ctx.Player.NormalInventory.CountOf(41100011) == 0, "breach cost")
	must(t, br.ErrorCode == ok && br.WeaponBreach == 1 && len(ofType[*protocol.EntityEquipChangeNotify](sink)) == 1 && len(ofType[*protocol.UpdateFormationNotify](sink)) == 1, br)

	dupID := ctx.Player.SpecialItemIDs.Next()
	dup := save.NewWeapon(dupID)
	dup.ID = w.ID
	ctx.Player.Weapons.Set(dupID, dup)
	save.SaveWeapon(ctx.Player, dup)
	sink.clear()
	re := ctx.Inventory.HandleWeaponResonUp(&protocol.WeaponResonUpRequest{IncId: w.UniqueID, ConsumeList: []int32{dupID}}, 1500, sink)
	must(t, re.ErrorCode == ok && re.ReaonLevel == 2 && !ctx.Player.Weapons.Has(dupID), re)
	must(t, slices.ContainsFunc(ofType[*protocol.WeaponItemRemoveNotify](sink), func(n *protocol.WeaponItemRemoveNotify) bool {
		return slices.Contains(n.WeaponItemIncrIdList, dupID)
	}), "no remove notify")
	ns := s2cNotifies(sink)
	must(t, slices.ContainsFunc(ns, func(n *protocol.CombatNotifyData) bool {
		return n.GetRemoveBuffS2CRequestNotify().GetReason() == WeaponRemoveReason
	}) &&
		slices.ContainsFunc(ns, func(n *protocol.CombatNotifyData) bool {
			return n.GetApplyBuffS2CRequestNotify().GetReason() == WeaponApplyReason
		}), ns)

	sink.clear()
	sw := ctx.Inventory.HandleEquipTakeOn(&protocol.EquipTakeOnRequest{Data: &protocol.RoleLoadEquipData{RoleID: 1211, Pos: protocol.EquipPos_EquipPos_Weapon, EquipIncID: w1108}}, sink)
	must(t, sw.ErrorCode == ok && ctx.Player.Roles.At(1211).Weapon == w1108 && ctx.Player.Roles.At(1108).Weapon == w1211 &&
		len(sw.DataList) == 2 && len(ofType[*protocol.EquipTakeOnNotify](sink)) == 1, sw)
}

func echoWithItem(ctx *PlayerContext, id int32) *save.EchoSave {
	for _, e := range ctx.Player.Echoes.All() {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// The 3.8 client looks every UnlockIllustratedPhantoms monster up in CalabashDevelopReward and
// throws on the first one it lacks (CalabashConfig.GetCalabashDevelopRewardByMonsterId), which
// drops the whole notify; 71 PhantomItem monsters have no row (R-144h, real client, 6020021).
func TestPhantomInteractionUnlockListsOnlyMonstersTheClientCanLookUp(t *testing.T) {
	tables := syntheticTables()
	tables.CalabashDevelopReward = []*data.CalabashDevelopReward{{MonsterId: 390001}, {MonsterId: 390003}}
	tables.CalabashDevelopRewardByMonsterId = map[int32]*data.CalabashDevelopReward{390001: tables.CalabashDevelopReward[0], 390003: tables.CalabashDevelopReward[1]}
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables, nowMs: 1000, rng: NewDotNetRandom(42)})
	var got []int32
	for _, u := range ctx.Inventory.BuildPhantomInteractionUnlockNotify().UnlockIllustratedPhantoms {
		got = append(got, u.MonsterId)
	}
	must(t, slices.Equal(got, []int32{390001, 390003}), got)
}

func TestEchoFormulasEquipTopologyRebuildTunePolishPresetsCalabashAndProjectorWork(t *testing.T) {
	tables := syntheticTables()
	rng := NewDotNetRandom(42)
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables, nowMs: 1000, rng: rng, seedDefaultEchoes: true})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	must(t, ComputeMainPropValue(tables, tables.PhantomMainPropItemById[5001], 25) == 3000, "main prop growth")
	flatAtk := tables.PhantomSubPropertyById[3]
	allowed := []int32{int32((37*813+5000)/10000) * 10, int32((37*1083+5000)/10000) * 10}
	for range 30 {
		v := RollSubStatValue(flatAtk, 3, rng)
		must(t, slices.Contains(allowed, v), v)
	}
	a, b, c, d := echoWithItem(ctx, 600001), echoWithItem(ctx, 600002), echoWithItem(ctx, 600003), echoWithItem(ctx, 600004)

	sink.clear()
	must(t, ctx.Inventory.HandlePhantomPutOn(&protocol.PhantomPutOnRequest{RoleId: 1211, Pos: 0, IncId: a.UniqueID}, 1100, sink).ErrorCode == ok, "put 0")
	must(t, len(ofType[*protocol.EntityRemoveNotify](sink)) == 1 && len(ofType[*protocol.EntityAddNotify](sink)) >= 1 && len(ofType[*protocol.UpdateSceneGroupDataNotify](sink)) == 1, "rebuild")
	sink.clear()
	must(t, ctx.Inventory.HandlePhantomPutOn(&protocol.PhantomPutOnRequest{RoleId: 1211, Pos: 1, IncId: b.UniqueID}, 1200, sink).ErrorCode == ok, "put 1")
	must(t, len(ofType[*protocol.EntityRemoveNotify](sink)) == 0 && len(ofType[*protocol.VisionSkillChangeNotify](sink)) == 1, "vision change")
	must(t, ctx.Inventory.HandlePhantomPutOn(&protocol.PhantomPutOnRequest{RoleId: 1211, Pos: 2, IncId: c.UniqueID}, 1300, sink).ErrorCode == ok, "put 2")
	must(t, ctx.Inventory.HandlePhantomPutOn(&protocol.PhantomPutOnRequest{RoleId: 1211, Pos: 3, IncId: d.UniqueID}, 1400, sink).ErrorCode == protocol.ErrorCode_ErrorCode_ErrPhantomEquipTargetCost, "over cost")

	must(t, ctx.Inventory.HandlePhantomPolish(&protocol.PhantomPolishRequest{IncrId: a.UniqueID, PhantomMainPropItemId: 5002}, 1500, sink).ErrorCode == ok && a.MainProp[0].ID == 5002, "polish")
	bp := ctx.Inventory.HandlePhantomBatchPolish(&protocol.PhantomBatchPolishRequest{PhantomMainPropItemId: 5002,
		StackEntries: []*protocol.PhantomStackConsumeEntry{{IncrId: b.UniqueID}}}, 1550, sink)
	must(t, bp.ErrorCode == ok && len(bp.UpdateInfos) == 1 && b.MainProp[0].ID == 5002, bp)

	lv := ctx.Inventory.HandlePhantomLevelUp(&protocol.PhantomLevelUpRequest{IncId: a.UniqueID, IdentifyCount: 1,
		ConsumeList: []*protocol.PhantomConsumeItem{{ItemId: 43010001, Count: 1}}}, 1600, sink)
	must(t, lv.ErrorCode == ok && a.Level == 5 && len(a.SubProp) == 1 && a.MainProp[0].Value == 880, lv, a.Level, a.MainProp)
	must(t, ctx.Inventory.HandlePhantomLevelUp(&protocol.PhantomLevelUpRequest{IncId: a.UniqueID,
		ConsumeList: []*protocol.PhantomConsumeItem{{ItemId: 43010001, Count: 1}}}, 1620, sink).ErrorCode == ok && a.Level == 10, a.Level)
	must(t, ctx.Inventory.HandlePhantomIdentify(&protocol.PhantomIdentifyRequest{IncrId: a.UniqueID, Count: 1}, 1640, sink).ErrorCode == ok && len(a.SubProp) == 2, a.SubProp)

	locked := a.SubProp[0]
	vp := ctx.Inventory.HandlePhantomVicePolish(&protocol.PhantomVicePolishRequest{IncrId: a.UniqueID, LockPropIndex: []int32{0}}, sink)
	must(t, vp.ErrorCode == ok && len(a.UnackSubProp) == 2 && a.UnackSubProp[0] == locked, a.UnackSubProp)
	cand := a.UnackSubProp[1]
	ack := ctx.Inventory.HandlePhantomVicePolishAck(&protocol.PhantomVicePolishAckRequest{IncrId: a.UniqueID, Ack: true}, 1700, sink)
	must(t, ack.ErrorCode == ok && a.SubProp[0] == locked && a.SubProp[1] == cand && len(a.UnackSubProp) == 0, a.SubProp)

	must(t, ctx.Inventory.HandlePhantomAutoPut(&protocol.PhantomAutoPutRequest{RoleId: 1108, PhantomItemIncrId: []int32{d.UniqueID, 0, 0, 0, 0}}, 1750, sink).ErrorCode == ok &&
		*d.RoleID == 1108, "auto put")
	sink.clear()
	must(t, ctx.Inventory.HandlePhantomSkinChange(&protocol.PhantomSkinChangeRequest{IncrId: a.UniqueID, SkinId: 600091}, 1800, sink).ErrorCode == ok &&
		len(ofType[*protocol.PhantomItemUpdateNotify](sink)) == 1, "skin")

	for i := range 20 {
		must(t, ctx.Inventory.HandleAddPhantomEquipGroup(&protocol.AddPhantomEquipGroupRequest{RoleId: 1211, Name: fmt.Sprintf("G%d", i)}).ErrorCode == ok, i)
	}
	must(t, ctx.Inventory.HandleAddPhantomEquipGroup(&protocol.AddPhantomEquipGroupRequest{RoleId: 1211, Name: "G21"}).ErrorCode == protocol.ErrorCode_ErrorCode_PhantomEquipGroupCountLimit, "cap")
	must(t, ctx.Inventory.HandleTopPhantomEquipGroup(&protocol.TopPhantomEquipGroupRequest{RoleId: 1211, Index: 5}).ErrorCode == ok && ctx.Player.EchoPresets.Groups[0].Name == "G5", "top")
	must(t, ctx.Inventory.HandleChangePhantomEquipGroupName(&protocol.ChangePhantomEquipGroupNameRequest{RoleId: 1211, Index: 0, Name: "Renamed"}).ErrorCode == ok &&
		ctx.Player.EchoPresets.Groups[0].Name == "Renamed", "rename")
	must(t, ctx.Inventory.HandleDeletePhantomEquipGroup(&protocol.DeletePhantomEquipGroupRequest{RoleId: 1211, Index: 0}).ErrorCode == ok &&
		len(ctx.Player.EchoPresets.Groups) == 19 && len(ctx.Inventory.HandlePhantomEquipGroupInfos(&protocol.PhantomEquipGroupInfosRequest{}).PhantomEquipGroupInfos) == 19, "delete")
	must(t, ctx.Inventory.HandleUsePhantomEquipGroup(&protocol.UsePhantomEquipGroupRequest{RoleId: 1108, Index: 0}, 1850, sink).ErrorCode == ok && *a.RoleID == 1108, "use preset")

	sink.clear()
	must(t, ctx.Inventory.HandleCalabashLevelReward(&protocol.CalabashLevelRewardRequest{Level: 99}, sink).ErrorCode == ok &&
		slices.Contains(ctx.Player.Calabash.RewardedLevels, 99), "calabash")
	must(t, ctx.Inventory.HandlePhantomInteractionEquip(&protocol.PhantomInteractionEquipRequest{EquipedMonsterIds: []int32{390001, 390002}}, sink).Code == ok &&
		slices.Equal(ctx.Player.Calabash.ProjectorMonsterIDs, []int32{390001, 390002}), "projector")
	must(t, ctx.Inventory.HandlePhantomInteractionSkinChange(&protocol.PhantomInteractionSkinChangeRequest{SkinChangeInfos: []*protocol.SkinChangeInfo{{MonsterId: 390001, SkinId: 600091}}}, sink).Code == ok &&
		slices.Contains(ctx.Player.Calabash.ProjectorSkins, save.ProjectorSkinSave{MonsterID: 390001, SkinID: 600091}), "projector skin")
}
