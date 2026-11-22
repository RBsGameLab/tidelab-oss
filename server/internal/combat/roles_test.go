// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tidelab/internal/save"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

// specs/roles.md §8 (RolesTests).

func TestNewAccountSeedingCreatesAllRolesGuaranteesWeaponsAndFiltersLoginList(t *testing.T) {
	tables := syntheticTables()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables})
	must(t, ctx.Player.Roles.Len() == len(tables.RoleInfo), ctx.Player.Roles.Len())
	for _, r := range ctx.Player.Roles.All() {
		must(t, r.Weapon > 0 && ctx.Player.Weapons.Has(r.Weapon) && len(r.BaseProp) == int(AttrCount), r.RoleID)
	}
	var ids []int32
	for _, r := range ctx.Roles.BuildLoginRoleListNotify().RoleList {
		ids = append(ids, r.RoleId)
	}
	must(t, !slices.Contains(ids, 9001) && slices.Contains(ids, 1211) && slices.Contains(ids, 1108) && slices.Contains(ids, 1506), ids)
	var rovers []int32
	for _, id := range ids {
		if id == 1501 || id == 1502 || id == 1604 || id == 1605 {
			rovers = append(rovers, id)
		}
	}
	must(t, slices.Equal(rovers, []int32{1502}), rovers)
}

func TestTwoPassRoleAttributeFormulasMatchExactIntegers(t *testing.T) {
	tables := syntheticTables()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables})
	role := ctx.Player.Roles.At(1211)
	baseLifeMax := int32(10333) // (int)(1000f * (103333f / 10000f))
	baseAtk := int32(412)       // (int)(100f * (41250f / 10000f))
	baseDef := int32(1258)      // (int)(100f * (125800f / 10000f))
	must(t, role.BaseProp[AttrLifeMax] == baseLifeMax && role.BaseProp[AttrLife] == baseLifeMax &&
		role.BaseProp[AttrAtk] == baseAtk && role.BaseProp[AttrDef] == baseDef, role.BaseProp[:12])

	w := ctx.Player.Weapons.At(role.Weapon)
	w.ID, w.Level, w.Breach, w.ResonLevel = 21010011, 90, 6, 1
	for _, n := range role.SkillNodeState {
		if n.NodeID == 102 {
			n.Active = true
		}
	}
	role.ResonantChainGroupIndex = 2
	e0 := save.NewEcho(9001)
	e0.ID, e0.FetterGroupID, e0.RoleID, e0.EquippedPos = 600001, 1, i32p(1211), 0
	e0.MainProp = []save.EchoPropSave{{ID: 5001, Value: 3000}, {ID: 50001, Value: 150}}
	e0.SubProp = []save.EchoPropSave{{ID: 6, Value: 750}}
	e1 := save.NewEcho(9002)
	e1.ID, e1.FetterGroupID, e1.RoleID, e1.EquippedPos = 600001, 1, i32p(1211), 1
	ctx.Player.Echoes.Set(9001, e0)
	ctx.Player.Echoes.Set(9002, e1)

	dup := ComputeLive(tables, ctx.Player, role, nil)
	must(t, dup.AddProps[attr("DamageChangeElement1")] == 0 && dup.AddProps[AttrDef] == 0, "a duplicate monster made a fetter")

	e2 := save.NewEcho(9003)
	e2.ID, e2.FetterGroupID, e2.RoleID, e2.EquippedPos = 600002, 1, i32p(1211), 2
	ctx.Player.Echoes.Set(9003, e2)
	snap := ComputeLive(tables, ctx.Player, role, nil)
	pass1Atk, pass1LifeMax := baseAtk+588, baseLifeMax+500
	must(t, snap.BaseProps[AttrAtk] == pass1Atk && snap.BaseProps[AttrLifeMax] == pass1LifeMax && snap.BaseProps[AttrLife] == pass1LifeMax, snap.BaseProps[AttrAtk])
	must(t, snap.AddProps[AttrAtk] == 365+40+100+300+150+150, snap.AddProps[AttrAtk])
	must(t, snap.AddProps[AttrCrit] == 500+750 && snap.AddProps[attr("DamageChangeElement1")] == 1000 && snap.AddProps[AttrDef] == 200, snap.AddProps[AttrCrit])
	info := snap.ToRolePhantomPropInfo(1211)
	val := func(list []*protocol.ArrayIntInt, k Attr) int32 {
		for _, p := range list {
			if p.Key == k {
				return p.Value
			}
		}
		return -1
	}
	must(t, val(info.BaseProp, AttrAtk) == 150 && val(info.BaseProp, AttrCrit) == 750 && val(info.BaseProp, attr("DamageChangeElement1")) == 1000, info.BaseProp)
	must(t, val(info.AddProp, AttrAtk) == 450 && val(info.AddProp, AttrDef) == 200, info.AddProp)
}

func TestWireEncodingsMatchWhiteGreenPropAndGameplayAttributeRules(t *testing.T) {
	a := NewAttributeSet()
	a.Slots[AttrLifeMax] = AttributeSlot{10000, 2000, 12000}
	a.Slots[AttrLife] = AttributeSlot{8000, 0, 8000}
	a.Slots[AttrAtk] = AttributeSlot{500, 300, 800}
	a.Slots[AttrDef] = AttributeSlot{400, 100, 500}
	a.Slots[AttrCrit] = AttributeSlot{500, 2000, 2500}
	a.Slots[AttrEnergy] = AttributeSlot{0, 0, 5000}
	c := a.ToAttributeComponentPb(0, 0)
	wg := func(x Attr) *protocol.WhiteGreenPropData {
		for _, p := range c.WhiteGreenProps {
			if p.AttributeType == x {
				return p
			}
		}
		return nil
	}
	must(t, wg(AttrLife).WhiteValue == 8000 && wg(AttrLife).CurrentValue == 8000 && wg(AttrEnergy).WhiteValue == 5000, wg(AttrLife))
	atk := wg(AttrAtk)
	must(t, atk.WhiteValue == 500 && atk.GreenValue == 300 && atk.CurrentValue == 800, atk)
	ga := a.ToGameplayAttributeData(AttrAtk)
	must(t, ga.BaseValue == 500 && ga.CurrentValue == 800, ga)
	cr := a.ToGameplayAttributeData(AttrCrit)
	must(t, cr.BaseValue == 2500 && cr.CurrentValue == 2500, cr)
}

func hasBuff(bs []InitialBuffSpec, id int64) bool {
	return slices.ContainsFunc(bs, func(b InitialBuffSpec) bool { return b.BuffID == id })
}

func TestRoleInitialBuffsAndConcomitantsAndPassiveSkillsWork(t *testing.T) {
	tables := syntheticTables()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables})
	con := NewConcomitantResolver(tables)
	bb := NewRoleBuffBuilder(tables, con)
	c1105, c1301 := con.CustomConcomitants(1105), con.CustomConcomitants(1301)
	must(t, len(c1105) == 1 && c1105[0].SpawnCount == 3 && len(c1301) == 1 && c1301[0].SpawnCount == 4, c1105, c1301)
	own := bb.RoleOwnBuffs(1108)
	must(t, hasBuff(own, 1108001) && !hasBuff(own, 11086661) && !hasBuff(own, 1108002), own)

	b1211, _ := bb.BuildForRole(ctx.Player, ctx.Player.Roles.At(1211), ComputeLive(tables, ctx.Player, ctx.Player.Roles.At(1211), nil))
	must(t, !hasBuff(b1211, 1101002011) && hasBuff(b1211, 12110001) && hasBuff(b1211, 70001), b1211)
	b1105, _ := bb.BuildForRole(ctx.Player, ctx.Player.Roles.At(1105), ComputeLive(tables, ctx.Player, ctx.Player.Roles.At(1105), nil))
	must(t, hasBuff(b1105, 1101002011) && !hasBuff(b1105, 91105), b1105)

	var ids []int64
	for _, b := range b1211 {
		ids = append(ids, b.BuffID)
	}
	pb := NewPassiveSkillsIndex(tables).BuildForRole(1211, ids, nil, nil)
	var sids []int64
	for _, s := range pb.GaPassiveSkills {
		sids = append(sids, s.SkillId)
		must(t, s.ContextId != 0, s)
	}
	must(t, slices.Contains(sids, 901) && slices.Contains(sids, 902) && slices.Contains(sids, 100005), sids)
}

func TestRoleRequestsSkillChainBranchVoiceAndDirtySaveQueueWork(t *testing.T) {
	dir := t.TempDir()
	tables := syntheticTables()
	ctx := createContext(t, dir, ctxOpts{tables: tables, nowMs: 1000})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	ok := protocol.ErrorCode_ErrorCode_Success

	up := ctx.Roles.HandleUpLevelSkill(&protocol.PbUpLevelSkillRequest{RoleId: 1211, SkillId: 1001}, 1000, sink)
	must(t, up.Code == ok && up.SkillInfo.Value == 2, up)
	quick := ctx.Roles.HandleSkillQuickLevelUp(&protocol.RoleSkillQuickLevelUpRequest{RoleId: 1211, SkillId: 1001, TargetLevel: 99}, 1500, sink)
	must(t, quick.ErrorCode == ok, quick)
	for _, s := range quick.RoleInfo.Skills {
		if s.Key == 1001 {
			must(t, s.Value == 10, s)
		}
	}
	sink.clear()
	act := ctx.Roles.HandleActivateSkill(&protocol.RoleActivateSkillRequest{RoleId: 1211, SkillNodeId: 102}, 2000, sink)
	must(t, act.Code == ok && len(ofType[*protocol.RoleSkillNodeNotify](sink)) == 1 &&
		len(ofType[*protocol.CombatReceivePackNotify](sink)) == 1 && len(ofType[*protocol.PbGetRoleListNotify](sink)) == 1, act)

	ctx.Player.NormalInventory.SetBatch([]save.ItemCount{{ID: 41100011, Count: 1}})
	fail := ctx.Roles.HandleResonantChainUnlock(&protocol.ResonantChainUnlockRequest{RoleId: 1211}, 2500, sink)
	must(t, fail.ErrCode == protocol.ErrorCode_ErrorCode_ErrRoleItemListNoEnough, fail)
	ctx.Player.NormalInventory.SetBatch([]save.ItemCount{{ID: 41100011, Count: 3}})
	sink.clear()
	chain := ctx.Roles.HandleResonantChainUnlock(&protocol.ResonantChainUnlockRequest{RoleId: 1211}, 3000, sink)
	must(t, chain.ErrCode == ok && chain.ResonantChainGroupIndex == 1 && ctx.Player.NormalInventory.CountOf(41100011) == 1, chain)
	found := false
	for _, n := range ofType[*protocol.NormalItemUpdateNotify](sink) {
		for _, i := range n.NormalItemList {
			found = found || (n.NoTips && i.Id == 41100011 && i.Count == 1)
		}
	}
	must(t, found, "no NormalItemUpdateNotify")

	sink.clear()
	br := ctx.Roles.HandleSkillBranchModify(&protocol.RoleSkillBranchModifyRequest{RoleId: 1211, SkillBranch: 121102}, 3500, sink)
	must(t, br.ErrorCode == ok && len(ofType[*protocol.UpdateFormationNotify](sink)) == 1 && ctx.Player.Roles.At(1211).SkillBranch == 121102, br)

	set := &protocol.PlayerRoleVoiceSetRequest{RoleVoices: []*protocol.RoleVoiceSetting{{RoleId: 1211, VoiceLanguage: 2}}}
	must(t, ctx.Roles.HandlePlayerRoleVoiceSet(set, 4000).ErrorCode == ok, "voice set")
	voices := ctx.Roles.HandlePlayerVoiceLanguage(&protocol.PlayerVoiceLanguageRequest{}).RoleVoices
	must(t, len(voices) == 1 && voices[0].RoleId == 1211 && voices[0].VoiceId == 2, voices)
	ctx.Roles.HandlePlayerRoleVoiceSet(&protocol.PlayerRoleVoiceSetRequest{}, 4500)
	must(t, len(ctx.Roles.HandlePlayerVoiceLanguage(&protocol.PlayerVoiceLanguageRequest{}).RoleVoices) == 0, "voices not cleared")

	favor := ctx.Roles.HandleRoleFavorList(&protocol.RoleFavorListRequest{})
	must(t, favor.ErrCode == ok && slices.ContainsFunc(favor.FavorList, func(f *protocol.RoleFavor) bool {
		return f.RoleId == 1211 && f.Level == 5 && f.Exp == 16800
	}), favor)
	prop := ctx.Roles.HandleRoleProp(&protocol.RolePropRequest{RoleIds: []int32{1211, 999999}})
	must(t, prop.Error == ok && len(prop.RoleProps) == 1 && prop.RoleProps[0].RoleId == 1211 && len(prop.RoleProps[0].BaseProp) == 143, prop)

	must(t, ctx.Player.DirtySaves.HasPending(), "nothing pending")
	save.FlushDirtyIfDue(ctx.Player, 30_999)
	must(t, ctx.Player.DirtySaves.HasPending(), "flushed early")
	save.FlushDirtyIfDue(ctx.Player, 31_000)
	must(t, !ctx.Player.DirtySaves.HasPending(), "not flushed")
	r := save.LoadPlayer(dir, 777).Roles.At(1211)
	lv := int32(0)
	for _, s := range r.Skills {
		if s.Key == 1001 {
			lv = s.Value
		}
	}
	must(t, lv == 10 && r.SkillBranch == 121102 && r.ResonantChainGroupIndex == 1, r)
}

func TestRoverElementAndSexChangeTransferGearFormationAndEmitLogoutNotify(t *testing.T) {
	dir := t.TempDir()
	tables := syntheticTables()
	hooks := &recordingHooks{}
	save.WriteAtomic(filepath.Join(dir, "account", "link", "testacc"), zon.Write(zon.NewStruct(zon.F("player_id", zon.Int(777)))))
	save.WriteAtomic(filepath.Join(dir, "account", "testacc"), zon.Write(zon.NewStruct(
		zon.F("user_id", zon.Str("testacc")), zon.F("user_name", zon.Str("testacc")), zon.F("token", zon.Str("tok")), zon.F("sex", zon.Int(0)))))
	ctx := createContext(t, dir, ctxOpts{tables: tables, hooks: hooks})
	sink := &recordingSink{}
	ctx.Player.BasicInfo.SelectedMainRoleID = 1502
	ctx.Player.FormationInfo.Formations[0].Roles[0] = save.NewFormationRole(1502)
	ctx.Player.FormationInfo.Formations[0].CurRole = 1502
	echo := save.NewEcho(888)
	echo.ID, echo.RoleID, echo.EquippedPos = 600001, i32p(1502), 0
	ctx.Player.Echoes.Set(888, echo)
	save.SaveEcho(ctx.Player, echo)
	weapon := ctx.Player.Roles.At(1502).Weapon
	ctx.Formation.SpawnActiveFormation(1000)
	ok := protocol.ErrorCode_ErrorCode_Success

	el := ctx.Roles.HandleRoleElementChange(&protocol.RoleElementChangeRequest{ElementType: 6}, 2000, sink)
	must(t, el.ErrorCode == ok && ctx.Player.BasicInfo.SelectedMainRoleID == 1605 && ctx.Player.Roles.At(1605).Weapon == weapon &&
		*ctx.Player.Echoes.At(888).RoleID == 1605 && ctx.Player.FormationInfo.Formations[0].CurRole == 1605, el)
	must(t, len(ofType[*protocol.RoleChangeNotify](sink)) == 1 && len(ofType[*protocol.EquipTakeOnNotify](sink)) == 1 &&
		len(ofType[*protocol.RolePhantomPropUpdateNotify](sink)) == 1 && len(ofType[*protocol.PhantomEquipInfoNotify](sink)) == 1, sink.msgs)

	sink.clear()
	sex := ctx.Roles.HandleRoleSexChange(&protocol.RoleSexChangeRequest{Sex: 1}, 3000, sink)
	must(t, sex.ErrorCode == ok && sex.Sex == 1 && ctx.Player.BasicInfo.SelectedMainRoleID == 1604 && *ctx.Player.Echoes.At(888).RoleID == 1604, sex)
	must(t, slices.Equal(hooks.genderChanges, []bool{true}), hooks.genderChanges)
	must(t, slices.ContainsFunc(ofType[*protocol.LogoutNotify](sink), func(l *protocol.LogoutNotify) bool {
		return l.Code == protocol.ErrorCode_ErrorCode_ErrSexChangeLogout
	}), "no LogoutNotify")
	acc, err := os.ReadFile(filepath.Join(dir, "account", "testacc"))
	must(t, err == nil && string(acc) != "" && contains(string(acc), ".sex = 1,"), string(acc))
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestRoleDevelopConfigAndTeamRecommendAndGrantRoleWork(t *testing.T) {
	tables := syntheticTables()
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tables})
	sink := &recordingSink{}
	v := "old"
	dev := HandleRoleDevelopConfig(tables, &protocol.RoleDevelopConfigRequest{Version: &v})
	must(t, dev.ErrorCode == protocol.ErrorCode_ErrorCode_Success && dev.Configs.Version == RoleDevelopVersion &&
		len(dev.Configs.DevPropsList) == 1 && dev.Configs.DevPropsList[0].ProspectBeginTime == 0 && len(dev.Configs.DevPropsProjectList) == 1, dev)
	must(t, len(HandleTeamRecommend(&protocol.TeamRecommendRequest{}).TeamRecommendList) == 0, "team recommend")
	before := ctx.Player.NormalInventory.CountOf(41100011)
	ctx.Roles.GrantRole(1211, 1000, sink)
	must(t, ctx.Player.NormalInventory.CountOf(41100011) == before+10 && len(ofType[*protocol.NormalItemAddNotify](sink)) == 1, before)
}
