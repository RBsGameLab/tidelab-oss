// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/protocol"
)

// specs/combat.md §10 (CombatTests).

func obj(t *testing.T, s string) *data.Object {
	t.Helper()
	return mustJSON(s).(*data.Object)
}

func TestMonsterAttributesFallbackEffectiveLevelR126SaturationAndPartsWork(t *testing.T) {
	tb := syntheticTables()
	must(t, ResolvePropertyID(obj(t, `{"AttributeComponent":{"PropertyId":999}}`), "Monster") == 999, "999")
	must(t, ResolvePropertyID(obj(t, `{"AttributeComponent":{"PropertyId":999,"Disabled":true},"BaseInfoComponent":{"EntityPropertyId":100}}`), "Monster") == 100, "100")
	must(t, ResolvePropertyID(nil, "Monster") == 2 && ResolvePropertyID(nil, "Item") == 0, "fallback")
	lv := int32(77)
	must(t, ResolveEffectiveLevel(tb, nil, 2, 4, &lv) == 77, "dungeon")
	must(t, ResolveEffectiveLevel(tb, obj(t, `{"AttributeComponent":{"Level":65}}`), 2, 4, nil) == 65, "explicit")
	must(t, ResolveEffectiveLevel(tb, nil, 2, 4, nil) == 48, "area walk")
	must(t, ResolveEffectiveLevel(tb, nil, 3, 4, nil) == 43, "wl fallback")
	sat := BuildMonsterAttributes(tb, obj(t, `{"AttributeComponent":{"PropertyId":999,"Level":100,"MonsterPropGrowthId":9}}`), "Monster", 1, 8, nil)
	must(t, sat.Slots[AttrLifeMax].Current == math.MaxInt32 && sat.Slots[AttrLife].Current == math.MaxInt32, sat.Slots[AttrLifeMax])
	var tags omap.Map[int32, int32]
	p := BuildPartComponent(tb, 8001, 10000, &tags)
	must(t, len(p.PartLifeInfos) == 2 && p.PartLifeInfos[0].LifeMax == 2500 && p.PartLifeInfos[1].LifeMax == -1, p)
	must(t, tags.At(222) == 1 && !tags.Has(223), tags.Keys())
	must(t, PartAllowsCombine(tb, 8001, 1) && !PartAllowsCombine(tb, 8001, 2), "combine")
}

func TestDamageAndAllSixHealFormulasMatchHandComputedValues(t *testing.T) {
	tb := syntheticTables()
	a, tg := NewAttributeSet(), NewAttributeSet()
	set := func(s *AttributeSet, name string, v int32) { s.Slots[attr(name)] = AttributeSlot{v, 0, v} }
	for n, v := range map[string]int32{"Lv": 90, "Atk": 1000, "Def": 500, "LifeMax": 10000, "Crit": 0, "CritDamage": 20000, "DamageChange": 1000,
		"DamageChangePhys": 2000, "DamageChangeAuto": 1000, "IgnoreDefRate": 5000, "IgnoreDamageResistancePhys": 500, "HealChange": 1000} {
		set(a, n, v)
	}
	for n, v := range map[string]int32{"LifeMax": 10000, "Life": 4000, "Def": 1520, "DamageResistancePhys": 1500, "DamageReduce": 1000, "HealedChange": 2000} {
		set(tg, n, v)
	}
	amount, crit := ComputeDamage(tb.DamageById[1001], a, tg, 2, true, NewDotNetRandom(1))
	must(t, crit && amount == 2268, amount)
	for id, want := range map[float64]int32{2000: 1625, 2002: 4100, 2003: 550, 2005: 2600, 2006: 1200} {
		must(t, ComputeHeal(tb.DamageById[id], a, tg, 1) == want, id, ComputeHeal(tb.DamageById[id], a, tg, 1))
	}
}

func damagePack(role, target int64, damageID int64, level int32) *protocol.CombatSendPackRequest {
	return &protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{{Message: &protocol.CombatSendData_Request{Request: &protocol.CombatRequestData{
		RequestId: 1, CombatCommon: &protocol.CombatCommon{EntityId: role},
		Message: &protocol.CombatRequestData_DamageExecuteRequest{DamageExecuteRequest: &protocol.DamageExecuteRequest{
			AttackerEntityId: role, TargetEntityId: target, DamageId: damageID, SkillLevel: level, ForceCritical: true}}}}}}}
}

func TestDamageExecuteKillsMonsterTriggersDropsCorpseAndRespawnAndTeamWipe(t *testing.T) {
	hooks := &recordingHooks{}
	ctx := createContext(t, t.TempDir(), ctxOpts{nowMs: 1000, hooks: hooks, rng: NewDotNetRandom(7)})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	role := ctx.Formation.FindLiveRoleEntity(1211)
	role.Attributes.Slots[AttrAtk] = AttributeSlot{50000, 0, 50000}
	role.Attributes.Slots[AttrEnergy] = AttributeSlot{}
	role.Attributes.Slots[AttrElementEnergy] = AttributeSlot{}
	id := ctx.Scene.AllocateNetID()
	m := NewCombatEntity(id)
	m.ConfigID, m.EntityType, m.MapID, m.IsOpenWorldSpawn, m.VisionCaptureID, m.RewardID = 55501, protocol.EEntityType_EEntityType_Monster, 8, true, 390001, 500
	m.Attributes.Slots[AttrLifeMax] = AttributeSlot{500, 0, 500}
	m.Attributes.Slots[AttrLife] = AttributeSlot{500, 0, 500}
	ctx.Scene.Entities.Set(id, m)

	resp := ctx.Combat.HandleCombatSendPack(damagePack(role.NetID, id, 1001, 1), 1000, sink)
	var dr *protocol.DamageExecuteResponse
	dead := false
	for _, d := range resp.ReceivePackNotify.Data {
		if x := d.GetCombatResponseData().GetDamageExecuteResponse(); x != nil {
			dr = x
		}
		if n := d.GetCombatNotifyData().GetEntityLivingStatusNotify(); n != nil && n.Id == id && n.LivingStatus == livingDead {
			dead = true
		}
	}
	must(t, dr.ErrorCode == ok && dr.KilledTarget && m.Life() == 0 && dead, dr)
	must(t, role.Attributes.Slots[AttrElementEnergy].Current == 500 && role.Attributes.Slots[AttrEnergy].Current == role.Attributes.Slots[AttrEnergyMax].Current, "energy")
	must(t, len(hooks.killed) == 1 && ctx.Scene.DeadCorpses.At(id).RemoveAtMs == 1000+CorpseRemoveDelayMs &&
		ctx.Scene.PendingRespawns.At(RespawnKey{8, 55501}).RespawnAtMs == 1000+OpenWorldRespawnDelayMs, hooks.killed)
	must(t, len(ofType[*protocol.PhantomItemAddNotify](sink)) == 1, "echo drop")
	drops := ofType[*protocol.NormalItemAddNotify](sink)
	must(t, len(drops) == 1, drops)
	for _, i := range drops[0].NormalItemList {
		must(t, i.Id == 41100011, i)
	}
	sink.clear()
	ctx.Kills.ProcessExpiredCorpses(8999, sink)
	must(t, ctx.Scene.Entities.Has(id), "removed early")
	ctx.Kills.ProcessExpiredCorpses(9000, sink)
	must(t, !ctx.Scene.Entities.Has(id) && len(ofType[*protocol.EntityRemoveNotify](sink)) == 1, "corpse")
}

func TestLandingDamageMatchesClientLogNumbersAndHandlesUnknownEntityAndTeamWipe(t *testing.T) {
	d := LandingRoleDamage(DefaultLandingRoleArgs, -4000.000244, 34069, 10300)
	must(t, d >= 103300 && d <= 103320, d)
	for _, c := range []struct {
		speed      float32
		ms, max, w int32
	}{{-2500, 0, 10000, 0}, {4000, 0, 10000, 0}, {-4500, 0, 10000, 5000}, {-3000, 1000, 10000, 2000}, {-4500, 1000, 10000, 7000}, {-9000, 5000, 0, 0}} {
		must(t, LandingRoleDamage(DefaultLandingRoleArgs, c.speed, c.ms, c.max) == c.w, c)
	}
	must(t, slices.Equal(ParseLandingRoleArgs([]byte(`{"landing_damage_args_role":[3000,4000,11000,2000]}`)), DefaultLandingRoleArgs) &&
		ParseLandingRoleArgs([]byte(`{"bag_size":200}`)) == nil, "parse")

	ctx := createContext(t, t.TempDir(), ctxOpts{})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	must(t, ctx.Combat.HandleLandingDamage(&protocol.LandingDamageRequest{EntityId: 6, SpeedZ: -4500}, 1000, sink).ErrorCode == ok && len(sink.msgs) == 0, "unknown")
	r1 := ctx.Formation.FindLiveRoleEntity(1211)
	mx := r1.LifeMax()
	ctx.Combat.HandleLandingDamage(&protocol.LandingDamageRequest{EntityId: r1.NetID, SpeedZ: -4500}, 1100, sink)
	must(t, r1.Life() == mx-mx/2, r1.Life())
	sink.clear()
	long := func(e *CombatEntity, at int64) {
		ctx.Combat.HandleLandingDamage(&protocol.LandingDamageRequest{EntityId: e.NetID, SpeedZ: -4000, TimeExceeding: 34069}, at, sink)
	}
	long(r1, 1200)
	must(t, r1.Life() == 0 && len(ofType[*protocol.PlayerDeadNotify](sink)) == 0, "first death")
	long(ctx.Formation.FindLiveRoleEntity(1108), 1300)
	must(t, len(ofType[*protocol.PlayerDeadNotify](sink)) == 0, "second death")
	long(ctx.Formation.FindLiveRoleEntity(1506), 1400)
	dn := ofType[*protocol.PlayerDeadNotify](sink)
	must(t, len(dn) == 1 && dn[0].IsAutoRevive && dn[0].ReviveId == 2, dn)
}

func TestReviveAtNearestBeaconAndPassiveBeaconHealWithin1500CmWork(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	ctx.Scene.PlayerX, ctx.Scene.PlayerY, ctx.Scene.PlayerZ = 100, 200, 50
	for _, r := range ctx.Scene.PlayerRoleEntities() {
		r.Attributes.Slots[AttrLife] = AttributeSlot{}
	}
	must(t, ctx.Combat.HandleRevive(&protocol.ReviveRequest{}, 2000, sink).ErrorCode == ok, "revive")
	n := ofType[*protocol.PlayerReviveNotify](sink)[0]
	must(t, n.Location.X == 1180 && n.Location.Y == 2180 && n.Location.Z == 550 && len(n.ReviveRoleInfos) == 3, n.Location)
	for _, r := range ctx.Scene.PlayerRoleEntities() {
		must(t, r.Life() == r.LifeMax(), r.NetID)
	}
	roles := ctx.Scene.PlayerRoleEntities()
	roles[0].Attributes.Slots[AttrLife] = AttributeSlot{100, 0, 100}
	roles[1].Attributes.Slots[AttrLife] = AttributeSlot{}
	sink.clear()
	must(t, !ctx.Revive.TryBeaconHealOnMove(4000, 2000, 500, 3000, sink) && roles[0].Life() == 100 && roles[1].Life() == 0, "far")
	must(t, ctx.Revive.TryBeaconHealOnMove(2000, 2000, 500, 3100, sink) && roles[0].Life() == roles[0].LifeMax() && roles[1].Life() == roles[1].LifeMax(), "near")
	alive := false
	for _, x := range s2cNotifies(sink) {
		if l := x.GetEntityLivingStatusNotify(); l != nil && l.Id == roles[1].NetID && l.LivingStatus == livingAlive {
			alive = true
		}
	}
	must(t, alive, "no alive notify")
}

func TestBuffManagerInstantDurationStackingPeriodicAndExpirationEffectsWork(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	role := ctx.Formation.FindLiveRoleEntity(1211)
	role.Attributes.Slots[AttrLife] = AttributeSlot{5000, 0, 5000}
	role.Attributes.Slots[AttrAtk] = AttributeSlot{1000, 0, 1000}
	var out []*protocol.CombatNotifyData
	apply := func(id int64, stacks int32, at int64) *protocol.OrderApplyBuffResponse {
		return ctx.Buffs.HandleOrderApplyBuff(role.NetID, &protocol.OrderApplyBuffRequest{Id: id, InstigatorId: role.NetID, Level: 1, StackCount: stacks}, at, &out)
	}
	must(t, apply(500001, 0, 1000).ErrorCode == ok && role.Life() == 5300, role.Life())
	must(t, slices.ContainsFunc(out, func(n *protocol.CombatNotifyData) bool {
		return n.GetApplyGameplayEffectNotify().GetHandle() == InstantBuffHandle
	}), "instant handle")
	apply(500005, 0, 1000)
	must(t, role.Attributes.Slots[AttrAtk].Current == 1200, role.Attributes.Slots[AttrAtk])
	apply(500004, 0, 1000)
	must(t, role.Attributes.Slots[AttrAtk].Current == 777, role.Attributes.Slots[AttrAtk])

	out = nil
	apply(500002, 1, 1000)
	apply(500002, 1, 1500)
	b := findBuff(role, func(b *ActiveBuffEntry) bool { return b.BuffID == 500002 })
	must(t, b.StackCount == 2 && *b.ExpireAtMs == 3500, b.StackCount)
	t1 := ctx.Buffs.TickNotifies(3500)
	must(t, b.StackCount == 1 && *b.ExpireAtMs == 5500 && slices.ContainsFunc(t1, func(n *protocol.CombatNotifyData) bool {
		return n.GetBuffStackCountNotify().GetNewStackCount() == 1
	}), b.StackCount)
	t2 := ctx.Buffs.TickNotifies(5500)
	must(t, findBuff(role, func(b *ActiveBuffEntry) bool { return b.BuffID == 500002 }) == nil &&
		slices.ContainsFunc(t2, func(n *protocol.CombatNotifyData) bool { return n.GetRemoveGameplayEffectNotify() != nil }) && role.Life() == 5600, role.Life())

	out = nil
	apply(500003, 0, 6000)
	must(t, role.Life() == 5500, role.Life())
	ctx.Buffs.Tick(7000, sink)
	must(t, role.Life() == 5400, role.Life())
	ctx.Buffs.Tick(8000, sink)
	must(t, role.Life() == 5300, role.Life())
}

func req(id int32, entity int64, m protocol.CombatRequestMessage) *protocol.CombatSendData {
	return &protocol.CombatSendData{Message: &protocol.CombatSendData_Request{Request: &protocol.CombatRequestData{
		RequestId: id, CombatCommon: &protocol.CombatCommon{EntityId: entity}, Message: m}}}
}

func TestCombatRelaysAiHateTagsCombineTimeCheckFsmAndUnhandledSkipWork(t *testing.T) {
	hooks := &recordingHooks{}
	ctx := createContext(t, t.TempDir(), ctxOpts{hooks: hooks, nowMs: 1000})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	role := ctx.Formation.FindLiveRoleEntity(1211)
	mid := ctx.Scene.AllocateNetID()
	m := NewCombatEntity(mid)
	m.EntityType, m.ModelID = protocol.EEntityType_EEntityType_Monster, 8001
	ctx.Scene.Entities.Set(mid, m)
	pack := &protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{
		req(1, mid, &protocol.CombatRequestData_AiHateRequest{AiHateRequest: &protocol.AiHateRequest{HateList: []*protocol.AiHateEntity{{EntityId: role.NetID, HatredValue: 10}}}}),
		req(2, role.NetID, &protocol.CombatRequestData_HitRequest{HitRequest: &protocol.HitRequest{HitInfo: &protocol.HitInformation{Originator: role.NetID, TargetId: mid}}}),
		req(3, role.NetID, &protocol.CombatRequestData_UseSkillRequest{UseSkillRequest: &protocol.UseSkillRequest{SkillSingleId: 99, UseSkillInfo: &protocol.UseSkillInformation{SkillId: 121101}}}),
		req(4, role.NetID, &protocol.CombatRequestData_AnimationGameplayTagRequest{AnimationGameplayTagRequest: &protocol.AnimationGameplayTagRequest{ChangedTagId: 4242, IsAdd: true}}),
		req(5, mid, &protocol.CombatRequestData_CombineEntitiesRequest{CombineEntitiesRequest: &protocol.CombineEntitiesRequest{TargetEntity: mid, CombinePartInfo: &protocol.CombinePartInfo{CombinerEntityId: role.NetID, PartId: 1}}}),
		req(6, role.NetID, &protocol.CombatRequestData_CreateBulletRequest{CreateBulletRequest: &protocol.CreateBulletRequest{}}),
		req(7, role.NetID, &protocol.CombatRequestData_ChangeStateRequest{ChangeStateRequest: &protocol.ChangeStateRequest{FsmId: 1, FromState: 11, ToState: 12}}),
	}}
	resp := ctx.Combat.HandleCombatSendPack(pack, 2000, sink)
	n := 0
	for _, d := range resp.ReceivePackNotify.Data {
		if d.GetCombatResponseData() != nil {
			n++
		}
	}
	must(t, n == 6, n)
	must(t, slices.ContainsFunc(ofType[*protocol.PlayerBattleStateChangeNotify](sink), func(b *protocol.PlayerBattleStateChangeNotify) bool { return b.InBattle }), "in battle")
	must(t, len(hooks.usedSkills) == 1 && hooks.usedSkills[0] == [2]int64{role.NetID, 121101}, hooks.usedSkills)
	must(t, role.TagCounts.At(4242) == 1 && len(ctx.Scene.CombineRelations) == 1 && role.FsmComponent.Fsms[0].CurrentState == 12, "relays")

	dis := ctx.Combat.HandleCombatSendPack(&protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{
		req(8, mid, &protocol.CombatRequestData_DissolveCombineRelationRequest{DissolveCombineRelationRequest: &protocol.DissolveCombineRelationRequest{EntityA: mid, EntityB: role.NetID}})}}, 2400, sink)
	must(t, len(ctx.Scene.CombineRelations) == 0 && slices.ContainsFunc(dis.ReceivePackNotify.Data, func(d *protocol.CombatReceiveData) bool {
		return d.GetCombatNotifyData().GetRemoveCombineRelationNotify() != nil
	}), "dissolve")

	sink.clear()
	ctx.Combat.HandleCombatSendPack(&protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{{Message: &protocol.CombatSendData_Push{Push: &protocol.CombatPushData{
		CombatCommon: &protocol.CombatCommon{EntityId: mid}, Message: &protocol.CombatPushData_AiHatePush{AiHatePush: &protocol.AiHatePush{}}}}}}}, 2500, sink)
	must(t, slices.ContainsFunc(ofType[*protocol.PlayerBattleStateChangeNotify](sink), func(b *protocol.PlayerBattleStateChangeNotify) bool { return !b.InBattle }), "left battle")

	ctx.Combat.HandleTimeStopPush(&protocol.TimeStopPush{TimeDilation: 0.5}, 3000)
	tc := ctx.Combat.HandleTimeCheck(&protocol.TimeCheckRequest{ClientTime: 1234}, 4000)
	must(t, tc.Code == ok && tc.ClientTime == 1234 && tc.ServerTime == 4000 && tc.FlowTime == 3500, tc)
}

// On the real 3.8 data (skipped without it): new account roles and weapons.
func TestRealDataNewAccount(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "RoleInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx := createContext(t, t.TempDir(), ctxOpts{tables: tb, seedDefaultEchoes: true})
	must(t, ctx.Player.Roles.Len() == 161 && len(tb.WeaponConf) == 125 && ctx.Player.Weapons.Len() >= 125, ctx.Player.Roles.Len(), len(tb.WeaponConf))
	owned := map[int32]bool{}
	for _, w := range ctx.Player.Weapons.All() {
		owned[w.ID] = true
	}
	for _, wc := range tb.WeaponConf {
		must(t, owned[wc.ItemId], wc.ItemId)
	}
	for _, r := range ctx.Player.Roles.All() {
		must(t, r.Weapon > 0 && ctx.Player.Weapons.Has(r.Weapon), r.RoleID)
	}
	must(t, ctx.Player.Echoes.Len() == 1488, ctx.Player.Echoes.Len())
	list := ctx.Roles.BuildLoginRoleListNotify().RoleList
	must(t, len(list) >= 50 && len(list) <= 65, len(list))
	for _, r := range list {
		must(t, r.RoleId >= 1000 && r.RoleId <= 1999, r.RoleId)
	}
	dev := HandleRoleDevelopConfig(tb, &protocol.RoleDevelopConfigRequest{})
	must(t, len(dev.Configs.DevPropsList) >= 10 && len(dev.Configs.DevPropsProjectList) >= 1, "develop")
	fsm := NewFsmInitializer(tb).BuildForRole(1304)
	must(t, fsm != nil && len(fsm.Fsms) > 0, "fsm")
}

// R-164: a weak-time end refills Tough and resends ToughRecover; the start only answers Success.
func TestFragileChangeEndRefillsToughAndResendsItsRecoveryRate(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{nowMs: 1000})
	sink := &recordingSink{}
	mid := ctx.Scene.AllocateNetID()
	m := NewCombatEntity(mid)
	m.EntityType = protocol.EEntityType_EEntityType_Monster
	m.Attributes.Slots[AttrToughMax] = AttributeSlot{10000, 0, 10000}
	m.Attributes.Slots[AttrTough] = AttributeSlot{0, 0, 0}
	m.Attributes.Slots[AttrToughRecover] = AttributeSlot{2500, 0, 2500}
	ctx.Scene.Entities.Set(mid, m)
	send := func(flag bool, entity int64) []*protocol.CombatReceiveData {
		return ctx.Combat.HandleCombatSendPack(&protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{
			req(1, mid, &protocol.CombatRequestData_FragileChangeRequest{FragileChangeRequest: &protocol.FragileChangeRequest{EntityId: entity, Flag: flag}})}}, 2000, sink).ReceivePackNotify.Data
	}
	recover := func(data []*protocol.CombatReceiveData) *protocol.RecoverAttr {
		for _, d := range data {
			for _, a := range d.GetCombatNotifyData().GetRecoverPropChangedNotify().GetAttributes() {
				if a.AttrId == AttrTough {
					return a
				}
			}
		}
		return nil
	}
	answered := func(data []*protocol.CombatReceiveData) bool {
		for _, d := range data {
			if r := d.GetCombatResponseData().GetFragileChangeResponse(); r != nil && r.ErrorCode == ok {
				return true
			}
		}
		return false
	}

	start := send(true, mid)
	must(t, answered(start) && recover(start) == nil && m.Attributes.Slots[AttrTough].Current == 0, "start")
	end := send(false, 0) // entity 0: the sender
	r := recover(end)
	must(t, answered(end) && r != nil && r.CurrentValue == 10000 && r.MaxValue == 10000 && r.Ratio == 2500, r)
	must(t, m.Attributes.Slots[AttrTough].Current == 10000, "server tough")
	must(t, answered(send(false, 999999)), "unknown entity still answered")
}

// R-165: remove-by-tags removes the buffs granting the tag or a child tag, keeps the others,
// filters by instigator, and a missing entity answers ErrSceneEntityNotExist; the summon state
// request is answered.
func TestOrderRemoveBuffByTagsAndSummonStateAreAnswered(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{nowMs: 1000})
	sink := &recordingSink{}
	mid := ctx.Scene.AllocateNetID()
	m := NewCombatEntity(mid)
	m.EntityType = protocol.EEntityType_EEntityType_Monster
	ctx.Scene.Entities.Set(mid, m)
	m.Buffs = []*ActiveBuffEntry{
		{HandleID: 1, BuffID: 200001001, InstigatorID: 7}, // Echo.Active.Main
		{HandleID: 2, BuffID: 1108002, InstigatorID: 7},   // Rogue.Test
		{HandleID: 3, BuffID: 200001001, InstigatorID: 8},
	}
	send := func(entity int64, m protocol.CombatRequestMessage) []*protocol.CombatReceiveData {
		return ctx.Combat.HandleCombatSendPack(&protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{req(1, entity, m)}}, 2000, sink).ReceivePackNotify.Data
	}
	byTags := func(entity int64, instigator int64, tags ...string) (protocol.ErrorCode, []int32) {
		var ids []int32
		for _, s := range tags {
			ids = append(ids, HashTag(s, ctx.Buffs.tables))
		}
		code, removed := protocol.ErrorCode(-1), []int32{}
		for _, d := range send(entity, &protocol.CombatRequestData_OrderRemoveBuffByTagsRequest{OrderRemoveBuffByTagsRequest: &protocol.OrderRemoveBuffByTagsRequest{TagIds: ids, InstigatorId: instigator}}) {
			if r := d.GetCombatResponseData().GetOrderRemoveBuffByTagsResponse(); r != nil {
				code = r.ErrorCode
			}
			if n := d.GetCombatNotifyData().GetRemoveGameplayEffectNotify(); n != nil {
				removed = append(removed, n.Handle)
			}
		}
		return code, removed
	}

	code, removed := byTags(mid, 7, "Echo.Active") // parent tag, instigator 7 only
	must(t, code == ok && slices.Equal(removed, []int32{1}) && len(m.Buffs) == 2, removed)
	code, removed = byTags(mid, 0, "Rogue.Test", "Echo")
	must(t, code == ok && len(removed) == 2 && len(m.Buffs) == 0, removed)
	log, restore := common.CaptureLog()
	code, _ = byTags(999999, 0, "Rogue")
	restore()
	must(t, code == codeEntityMissing, code)
	// R-166: a reply that is not Success leaves a line tools/tl_audit.py counts.
	must(t, strings.Contains(log.String(), "warning(reply): OrderRemoveBuffByTagsRequest answered ErrorCode_ErrSceneEntityNotExist (700056)"), log.String())

	answered := false
	for _, d := range send(mid, &protocol.CombatRequestData_ChangeSummonEntityStateRequest{ChangeSummonEntityStateRequest: &protocol.ChangeSummonEntityStateRequest{SummonEntityId: mid, TagId: 5}}) {
		if r := d.GetCombatResponseData().GetChangeSummonEntityStateResponse(); r != nil && r.ErrorCode == ok {
			answered = true
		}
	}
	must(t, answered, "summon state")
}
