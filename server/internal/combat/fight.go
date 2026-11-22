// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// ----- damage and healing (specs/combat.md §5) -----

const (
	MaxDamageOrHeal            = 2_000_000_000
	LiberationEnergyGainPerHit = 10_000_000
)

// DamageLevelValue is the skill level's entry of a Damage array (specs/combat.md §5.1).
func DamageLevelValue(values []int32, skillLevel int32) int32 { return levelValue(values, skillLevel) }

// ComputeConcertoGain is the attacker's Concerto energy for a hit or heal (§5 step 3).
func ComputeConcertoGain(d *data.Damage, attacker *AttributeSet) int32 {
	if len(d.ElementPower) == 0 {
		return 0
	}
	raw := int64(d.ElementPower[0]) * int64(attacker.Slots[attr("ElementEfficiency")].Current) / 10000
	return int32(max(math.MinInt32, min(raw, math.MaxInt32)))
}

// ComputeHeal is a heal of Damage.CalculateType 1 (specs/combat.md §5.2).
func ComputeHeal(d *data.Damage, attacker, target *AttributeSet, skillLevel int32) int32 {
	healBonus := math.Max(0, 1.0+float64(attacker.Slots[attr("HealChange")].Current+target.Slots[attr("HealedChange")].Current)/10000.0)
	lv := func(v []int32) int32 { return levelValue(v, skillLevel) }
	p1, p2, p3, p4, p5, p6 := lv(d.FormulaParam1), lv(d.FormulaParam2), lv(d.FormulaParam3), lv(d.FormulaParam4), lv(d.FormulaParam5), lv(d.FormulaParam6)
	holder := func(flag int32) *AttributeSet {
		if flag == 0 {
			return attacker
		}
		return target
	}
	stat := func(s *AttributeSet, id int32) float64 {
		if id > 0 && id < AttrCount {
			return float64(s.Slots[id].Current)
		}
		return 0
	}
	var total float64
	switch d.FormulaType {
	case 2:
		total = math.Max(0, stat(holder(p2), p1)*(float64(p3)/10000.0)+float64(p4)-float64(target.Slots[AttrLife].Current))
	case 3:
		total = stat(holder(p2), p1)*(float64(p3)/10000.0) + float64(p4)
	case 5:
		total = float64(target.Slots[AttrLifeMax].Current)*(float64(p1)/10000.0) + float64(p2)
	case 6:
		total = stat(holder(p2), p1)*(float64(p3)/10000.0) + stat(holder(p5), p4)*(float64(p6)/10000.0)
	default:
		rel := d.RelatedProperty
		if rel <= 0 {
			rel = AttrLifeMax
		}
		total = (stat(attacker, rel)*(float64(lv(d.RateLv))/10000.0) + float64(lv(d.CureBaseValue))) * healBonus
	}
	c := math.Ceil(total)
	if math.IsInf(c, 0) || math.IsNaN(c) || c < 1 {
		return 1
	}
	return int32(math.Min(c, MaxDamageOrHeal))
}

func elementAttr(prefix string, element int32, other string) Attr {
	if element >= 1 && element <= 6 {
		return attr(prefix + string(rune('0'+element)))
	}
	return attr(other)
}

// ComputeDamage is a hit of Damage.CalculateType 0 and whether it was critical (§5.4).
func ComputeDamage(d *data.Damage, attacker, target *AttributeSet, skillLevel int32, forceCritical bool, rng Rng) (int32, bool) {
	rel := d.RelatedProperty
	if rel <= 0 || rel >= AttrCount {
		rel = AttrAtk
	}
	scaling := float64(attacker.Slots[rel].Current)
	rate := float64(levelValue(d.RateLv, skillLevel)) / 10000.0
	var ability int32
	if a := AbilityBonusAttr(d.Type); a != AttrNone {
		ability = attacker.Slots[a].Current
	}
	bonus := 1.0 + float64(attacker.Slots[attr("DamageChange")].Current+attacker.Slots[elementAttr("DamageChangeElement", d.Element, "DamageChangePhys")].Current+ability)/10000.0
	isCrit := forceCritical || rng.Next(10000) < attacker.Slots[AttrCrit].Current
	critMult := 1.0
	if isCrit {
		critMult = math.Max(float64(attacker.Slots[AttrCritDamage].Current)/10000.0, 1.0)
	}
	lv := float64(max(attacker.Slots[AttrLv].Current, 1))
	ignoreDef := math.Max(0, math.Min(float64(attacker.Slots[attr("IgnoreDefRate")].Current)/10000.0, 1))
	effDef := float64(max(target.Slots[AttrDef].Current, 0)) * (1.0 - ignoreDef)
	defBase := 800.0 + 8.0*lv
	defMult := defBase / (defBase + effDef)
	resRaw := target.Slots[elementAttr("DamageResistanceElement", d.Element, "DamageResistancePhys")].Current -
		attacker.Slots[elementAttr("IgnoreDamageResistanceElement", d.Element, "IgnoreDamageResistancePhys")].Current
	resMult := math.Max(0.1, math.Min(1.0-float64(resRaw)/10000.0, 2.0))
	reduceRaw := target.Slots[AttrDamageReduce].Current + target.Slots[elementAttr("DamageReduceElement", d.Element, "DamageReducePhys")].Current
	reduceMult := math.Max(0.0, math.Min(1.0-float64(reduceRaw)/10000.0, 2.0))
	total := scaling * rate * bonus * critMult * defMult * resMult * reduceMult
	r := math.Round(total)
	if math.IsInf(r, 0) || math.IsNaN(r) || r < 1 {
		return 1, isCrit
	}
	return int32(math.Min(r, MaxDamageOrHeal)), isCrit
}

// AbilityBonusAttr is the damage-type bonus attribute (none for unknown types).
func AbilityBonusAttr(damageType int32) Attr {
	switch damageType {
	case 0:
		return attr("DamageChangeAuto")
	case 1:
		return attr("DamageChangeCast")
	case 2:
		return attr("DamageChangeUltra")
	case 3:
		return attr("DamageChangeQte")
	case 4:
		return attr("DamageChangeNormalSkill")
	case 5:
		return attr("DamageChangePhantom")
	}
	return AttrNone
}

// ----- kills and drops (specs/combat.md §6) -----

const (
	CorpseRemoveDelayMs     = 8_000
	OpenWorldRespawnDelayMs = 180_000
)

var dropSlotProbabilities = []int32{100, 45, 18, 6}

// KillAndDropService is the shared monster-killed path: the 8 s corpse, the 3 min open-world
// respawn, the quest hooks, echo drops and the filtered item drops (R-109, R-116).
type KillAndDropService struct {
	tables *data.DataTables
	player *save.PlayerData
	scene  *CombatSceneState
	hooks  GameHooks
	rng    Rng
}

func NewKillAndDropService(t *data.DataTables, p *save.PlayerData, s *CombatSceneState, hooks GameHooks, rng Rng) *KillAndDropService {
	if hooks == nil {
		hooks = NullHooks{}
	}
	if rng == nil {
		rng = SystemRng{}
	}
	return &KillAndDropService{t, p, s, hooks, rng}
}

func (k *KillAndDropService) OnMonsterKilled(m *CombatEntity, nowMs int64, sink PushSink) {
	k.scene.DeadCorpses.Set(m.NetID, DeadCorpseEntry{m.NetID, nowMs + CorpseRemoveDelayMs})
	if m.IsOpenWorldSpawn {
		k.scene.PendingRespawns.Set(RespawnKey{m.MapID, m.ConfigID}, PendingRespawnEntry{m.MapID, m.ConfigID, nowMs + OpenWorldRespawnDelayMs})
	}
	k.hooks.OnMonsterKilled(m)
	k.tryDropEcho(m.VisionCaptureID, sink)
	k.tryDropItems(m.RewardID, sink)
}

// ProcessExpiredCorpses removes the corpses whose 8 s are over, with an EntityRemoveNotify.
func (k *KillAndDropService) ProcessExpiredCorpses(nowMs int64, sink PushSink) []int64 {
	var expired []int64
	for id, e := range k.scene.DeadCorpses.All() {
		if nowMs >= e.RemoveAtMs {
			expired = append(expired, id)
		}
	}
	if len(expired) == 0 {
		return expired
	}
	for _, id := range expired {
		k.scene.DeadCorpses.Delete(id)
		k.scene.Entities.Delete(id)
		k.scene.ActiveCombatants.Delete(id)
	}
	sink.Push(removeNotify(expired))
	return expired
}

func EchoDropProbabilityPercent(rarity int32) int32 {
	switch rarity {
	case 1:
		return 40
	case 2:
		return 60
	}
	return 100
}

func (k *KillAndDropService) tryDropEcho(captureID int32, sink PushSink) {
	if captureID <= 0 {
		return
	}
	var best *data.PhantomItem
	for _, it := range k.tables.PhantomItem {
		if it.MonsterId != captureID || !IsSeedableEcho(k.tables, it) {
			continue
		}
		if best == nil || it.QualityId > best.QualityId {
			best = it
		}
	}
	if best == nil || len(best.FetterGroup) == 0 {
		return
	}
	if p := EchoDropProbabilityPercent(best.Rarity); p < 100 && k.rng.Next(100) >= p {
		return
	}
	fetter := best.FetterGroup[k.rng.Next(int32(len(best.FetterGroup)))]
	uid := k.player.SpecialItemIDs.Next()
	e := CreateLevel0Echo(k.tables, uid, best, fetter, true, k.rng)
	if e == nil {
		return
	}
	k.player.Echoes.Set(uid, e)
	save.SaveEcho(k.player, e)
	sink.Push(&protocol.PhantomItemAddNotify{PhantomItemList: []*protocol.PhantomItem{ToProtoEcho(e)}})
}

func (k *KillAndDropService) tryDropItems(rewardID int32, sink PushSink) {
	pkg, ok := k.tables.DropPackageById[rewardID]
	if rewardID <= 0 || !ok {
		return
	}
	type drop struct{ id, n int32 }
	var added []drop
	slot := 0
	for _, p := range pkg.DropPreview.Pairs {
		// R-109, R-116: only ids ItemInfo has.
		if _, ok := k.tables.ItemInfoById[p.Key]; !ok {
			continue
		}
		prob := dropSlotProbabilities[min(slot, len(dropSlotProbabilities)-1)]
		slot++
		if prob < 100 && k.rng.Next(100) >= prob {
			continue
		}
		n := p.Value
		if n <= 0 {
			n = k.rng.NextRange(1, 3)
		}
		added = append(added, drop{p.Key, n})
	}
	if len(added) == 0 {
		return
	}
	var updates []save.ItemCount
	notify := &protocol.NormalItemAddNotify{}
	for _, d := range added {
		if v, _ := MoneyAttr(&k.player.BasicInfo.Attributes, d.id); v != nil {
			SetMoney(k.player, d.id, int64(*v)+int64(d.n), sink)
			continue
		}
		updates = append(updates, save.ItemCount{ID: d.id, Count: k.player.NormalInventory.CountOf(d.id) + d.n})
		notify.NormalItemList = append(notify.NormalItemList, &protocol.NormalItem{Id: d.id, Count: d.n})
	}
	if len(updates) == 0 {
		return
	}
	k.player.NormalInventory.SetBatch(updates)
	sink.Push(notify)
}

// ----- revive (specs/combat.md §7) -----

const (
	BeaconHealRadiusCm = 1500.0
	BeaconHealRadiusSq = BeaconHealRadiusCm * BeaconHealRadiusCm
	ReviveOffsetX      = 180.0
	ReviveOffsetY      = 180.0
	ReviveOffsetZ      = 50.0
)

// ReviveService is the revive at the nearest beacon and the beacon heal within 15 m.
type ReviveService struct {
	tables *data.DataTables
	scene  *CombatSceneState
}

func NewReviveService(t *data.DataTables, s *CombatSceneState) *ReviveService {
	return &ReviveService{t, s}
}

func IsBeacon(row *data.LevelEntityConfig) bool {
	return !row.IsHidden && len(row.Transform) > 0 && row.Transform[0] != nil &&
		(strings.HasPrefix(row.BlueprintType, "Teleport") || row.BlueprintType == "branch2.0_Teleport6" || row.BlueprintType == "branch2.2_14_Teleport4")
}

// FindNearestBeacon is the nearest beacon of the map and its squared distance.
func FindNearestBeacon(t *data.DataTables, mapID int32, x, y, z float32) (bx, by, bz float32, distSq float64, found bool) {
	distSq = math.Inf(1)
	consider := func(row *data.LevelEntityConfig) {
		if !IsBeacon(row) {
			return
		}
		l := row.Transform[0]
		cx, cy, cz := float32(l.X)/100, float32(l.Y)/100, float32(l.Z)/100
		dx, dy, dz := float64(cx-x), float64(cy-y), float64(cz-z)
		if d := dx*dx + dy*dy + dz*dz; d < distSq {
			distSq, bx, by, bz, found = d, cx, cy, cz, true
		}
	}
	if idx := t.LevelEntityRowsOfMap(mapID); len(idx) > 0 {
		for _, i := range idx {
			consider(t.LevelEntityConfig[i])
		}
	} else {
		// Small test tables without the map index.
		for _, r := range t.LevelEntityConfig {
			if r.MapId == mapID {
				consider(r)
			}
		}
	}
	return
}

func livingNotify(id int64, status protocol.LivingStatus) *protocol.CombatNotifyData {
	return protocol.Notify(id, &protocol.CombatNotifyData_EntityLivingStatusNotify{EntityLivingStatusNotify: &protocol.EntityLivingStatusNotify{Id: id, LivingStatus: status}})
}

const (
	livingAlive = protocol.LivingStatus_LivingStatus_Alive
	livingDead  = protocol.LivingStatus_LivingStatus_Dead
)

// HandleRevive (specs/combat.md §7.1).
func (r *ReviveService) HandleRevive(_ *protocol.ReviveRequest, nowMs int64, sink PushSink) *protocol.ReviveResponse {
	s := r.scene
	tx, ty, tz := s.PlayerX, s.PlayerY, s.PlayerZ
	if bx, by, bz, _, ok := FindNearestBeacon(r.tables, s.MapConfigID, s.PlayerX, s.PlayerY, s.PlayerZ); ok {
		tx, ty, tz = bx+ReviveOffsetX, by+ReviveOffsetY, bz+ReviveOffsetZ
	}
	s.PlayerX, s.PlayerY, s.PlayerZ = tx, ty, tz
	n := &protocol.PlayerReviveNotify{PlayerId: s.PlayerID, Location: &protocol.Vector{X: tx, Y: ty, Z: tz},
		Rotator: &protocol.Rotator{Pitch: s.PlayerPitch, Yaw: s.PlayerYaw, Roll: s.PlayerRoll}}
	var notifies []*protocol.CombatNotifyData
	for _, role := range s.PlayerRoleEntities() {
		m := role.LifeMax()
		life := &role.Attributes.Slots[AttrLife]
		life.Base, life.Current = m, m
		role.SetPosition(tx, ty, tz)
		role.FsmDirty = true
		n.ReviveRoleInfos = append(n.ReviveRoleInfos, &protocol.ReviveRoleInformation{EntityId: role.NetID, CurHpAttribute: role.Attributes.ToGameplayAttributeData(AttrLife)})
		notifies = append(notifies, livingNotify(role.NetID, livingAlive))
		notifies = append(notifies, role.Attributes.BuildChangeNotifies(role.NetID, []Attr{AttrLife}, nowMs)...)
	}
	sink.Push(n)
	PushCombatNotifies(sink, notifies)
	return &protocol.ReviveResponse{ErrorCode: codeSuccess}
}

// TryBeaconHealOnMove heals (and revives) the roles within 15 m of a beacon (specs/combat.md §7.2).
func (r *ReviveService) TryBeaconHealOnMove(x, y, z float32, nowMs int64, sink PushSink) bool {
	_, _, _, d, ok := FindNearestBeacon(r.tables, r.scene.MapConfigID, x, y, z)
	if !ok || d > BeaconHealRadiusSq {
		return false
	}
	var notifies []*protocol.CombatNotifyData
	healed := false
	for _, role := range r.scene.PlayerRoleEntities() {
		m := role.LifeMax()
		life := &role.Attributes.Slots[AttrLife]
		if life.Current >= m {
			continue
		}
		wasDead := life.Current <= 0
		life.Current, life.Base = m, m
		role.FsmDirty = true
		healed = true
		if wasDead {
			notifies = append(notifies, livingNotify(role.NetID, livingAlive))
		}
		notifies = append(notifies, role.Attributes.BuildChangeNotifies(role.NetID, []Attr{AttrLife}, nowMs)...)
	}
	if len(notifies) > 0 {
		PushCombatNotifies(sink, notifies)
	}
	return healed
}

// ----- the combat pack (specs/combat.md §2, §5, §9) -----

// CombatService dispatches CombatSendPackRequest: damage and healing, fall damage (R-118), relays,
// battle state, combining entities, time sync and the FSM relays.
type CombatService struct {
	tables      *data.DataTables
	scene       *CombatSceneState
	formation   *FormationService
	buffs       *BuffManager
	kills       *KillAndDropService
	revive      *ReviveService
	hooks       GameHooks
	landingArgs []int32
	rng         Rng
}

func NewCombatService(t *data.DataTables, s *CombatSceneState, f *FormationService, b *BuffManager, k *KillAndDropService, r *ReviveService,
	hooks GameHooks, landingArgs []int32, rng Rng) *CombatService {
	if hooks == nil {
		hooks = NullHooks{}
	}
	if len(landingArgs) < 4 {
		landingArgs = DefaultLandingRoleArgs
	}
	if rng == nil {
		rng = SystemRng{}
	}
	return &CombatService{t, s, f, b, k, r, hooks, landingArgs, rng}
}

// HandleCombatSendPack dispatches a pack in arrival order (specs/combat.md §2).
func (c *CombatService) HandleCombatSendPack(pack *protocol.CombatSendPackRequest, nowMs int64, sink PushSink) *protocol.CombatSendPackResponse {
	recv := &protocol.CombatReceivePackNotify{}
	for _, d := range pack.Data {
		switch m := d.Message.(type) {
		case *protocol.CombatSendData_Request:
			if m.Request != nil {
				c.processRequest(m.Request, nowMs, sink, recv)
			}
		case *protocol.CombatSendData_Push:
			if m.Push != nil {
				c.processPush(m.Push, nowMs, sink, recv)
			}
		}
	}
	// The server's state machine switches the pack made due (R-152) follow its replies.
	addNotifies(recv, c.StepFsms(nowMs))
	return &protocol.CombatSendPackResponse{ErrorCode: codeSuccess, ReceivePackNotify: recv}
}

func caseName(v any, prefix string) string {
	if v == nil {
		return "None"
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "*protocol."+prefix)
}

func addNotifies(recv *protocol.CombatReceivePackNotify, ns []*protocol.CombatNotifyData) {
	for _, n := range ns {
		recv.Data = append(recv.Data, &protocol.CombatReceiveData{Message: &protocol.CombatReceiveData_CombatNotifyData{CombatNotifyData: n}})
	}
}

func cloneOf[T proto.Message](m T) T {
	var zero T
	if any(m) == any(zero) {
		return zero
	}
	return proto.Clone(m).(T)
}

func (c *CombatService) processRequest(req *protocol.CombatRequestData, nowMs int64, sink PushSink, recv *protocol.CombatReceivePackNotify) {
	cc := &protocol.CombatCommon{}
	if req.CombatCommon != nil {
		cc = proto.Clone(req.CombatCommon).(*protocol.CombatCommon)
	}
	sender := cc.EntityId
	var out []*protocol.CombatNotifyData
	resp := &protocol.CombatResponseData{CombatCommon: cc, RequestId: req.RequestId}
	ok := protocol.ErrorCode_ErrorCode_Success
	switch m := req.Message.(type) {
	case *protocol.CombatRequestData_DamageExecuteRequest:
		resp.Message = &protocol.CombatResponseData_DamageExecuteResponse{DamageExecuteResponse: c.handleDamageExecute(m.DamageExecuteRequest, nowMs, sink, &out)}
	case *protocol.CombatRequestData_SwitchRoleRequest:
		resp.Message = &protocol.CombatResponseData_SwitchRoleResponse{SwitchRoleResponse: c.formation.HandleSwitchRole(m.SwitchRoleRequest, nowMs, &out)}
	case *protocol.CombatRequestData_OrderApplyBuffRequest:
		resp.Message = &protocol.CombatResponseData_OrderApplyBuffResponse{OrderApplyBuffResponse: c.buffs.HandleOrderApplyBuff(sender, m.OrderApplyBuffRequest, nowMs, &out)}
	case *protocol.CombatRequestData_OrderRemoveBuffByTagsRequest:
		resp.Message = &protocol.CombatResponseData_OrderRemoveBuffByTagsResponse{OrderRemoveBuffByTagsResponse: c.buffs.HandleOrderRemoveBuffByTags(sender, m.OrderRemoveBuffByTagsRequest, &out)}
	case *protocol.CombatRequestData_ChangeSummonEntityStateRequest:
		// R-165: the client only logs a failure; what the official server changes on the summon is
		// unknown, so the reply is Success and nothing else.
		resp.Message = &protocol.CombatResponseData_ChangeSummonEntityStateResponse{ChangeSummonEntityStateResponse: &protocol.ChangeSummonEntityStateResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_OrderRemoveBuffRequest:
		resp.Message = &protocol.CombatResponseData_OrderRemoveBuffResponse{OrderRemoveBuffResponse: c.buffs.HandleOrderRemoveBuff(sender, m.OrderRemoveBuffRequest, &out)}
	case *protocol.CombatRequestData_ActivateBuffRequest:
		resp.Message = &protocol.CombatResponseData_ActivateBuffResponse{ActivateBuffResponse: c.buffs.HandleActivateBuff(sender, m.ActivateBuffRequest, &out)}
	case *protocol.CombatRequestData_BuffStackCountRequest:
		resp.Message = &protocol.CombatResponseData_BuffStackCountResponse{BuffStackCountResponse: c.buffs.HandleBuffStackCount(sender, m.BuffStackCountRequest, &out)}
	case *protocol.CombatRequestData_HitRequest:
		resp.Message = &protocol.CombatResponseData_HitResponse{HitResponse: &protocol.HitResponse{ErrorCode: ok, HitInfo: cloneOf(m.HitRequest.GetHitInfo())}}
	case *protocol.CombatRequestData_FragileChangeRequest:
		resp.Message = &protocol.CombatResponseData_FragileChangeResponse{FragileChangeResponse: c.handleFragileChange(sender, m.FragileChangeRequest, nowMs, &out)}
	case *protocol.CombatRequestData_HitEndRequest:
		resp.Message = &protocol.CombatResponseData_HitEndResponse{HitEndResponse: &protocol.HitEndResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_SkillRequest:
		resp.Message = &protocol.CombatResponseData_SkillResponse{SkillResponse: &protocol.SkillResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_UseSkillRequest:
		u := m.UseSkillRequest
		if e := c.scene.GetEntity(sender); e != nil {
			e.FsmDirty = true
		}
		var skillID int64
		if u.GetUseSkillInfo() != nil {
			skillID = int64(u.UseSkillInfo.SkillId)
		}
		c.hooks.OnUseSkill(sender, skillID) // R-14
		resp.Message = &protocol.CombatResponseData_UseSkillResponse{UseSkillResponse: &protocol.UseSkillResponse{ErrorCode: ok,
			UseSkillInfo: cloneOf(u.GetUseSkillInfo()), SkillSingleId: u.GetSkillSingleId()}}
	case *protocol.CombatRequestData_EndSkillRequest:
		e := m.EndSkillRequest
		if x := c.scene.GetEntity(sender); x != nil {
			x.FsmDirty = true
		}
		resp.Message = &protocol.CombatResponseData_EndSkillResponse{EndSkillResponse: &protocol.EndSkillResponse{ErrorCode: ok,
			SkillSingleId: e.GetSkillSingleId(), UseSkillInfo: cloneOf(e.GetUseSkillInfo())}}
	case *protocol.CombatRequestData_AiHateRequest:
		c.handleAiHateList(sender, m.AiHateRequest.GetHateList(), sink)
		resp.Message = &protocol.CombatResponseData_AiHateResponse{AiHateResponse: &protocol.AiHateResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_AnimationGameplayTagRequest:
		t := m.AnimationGameplayTagRequest
		if e := c.scene.GetEntity(sender); e != nil {
			e.AdjustTag(t.GetChangedTagId(), t.GetIsAdd())
		}
		resp.Message = &protocol.CombatResponseData_AnimationGameplayTagResponse{AnimationGameplayTagResponse: &protocol.AnimationGameplayTagResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_CombineEntitiesRequest:
		resp.Message = &protocol.CombatResponseData_CombineEntitiesResponse{CombineEntitiesResponse: c.handleCombineEntities(m.CombineEntitiesRequest, &out)}
	case *protocol.CombatRequestData_DissolveCombineRelationRequest:
		resp.Message = &protocol.CombatResponseData_DissolveCombineRelationResponse{DissolveCombineRelationResponse: c.handleDissolveCombine(m.DissolveCombineRelationRequest, &out)}
	case *protocol.CombatRequestData_ChangeStateRequest:
		cs := m.ChangeStateRequest
		c.clientSwitch(sender, cs.GetFsmId(), cs.GetToState(), nowMs)
		out = append(out, &protocol.CombatNotifyData{CombatCommon: proto.Clone(cc).(*protocol.CombatCommon),
			Message: &protocol.CombatNotifyData_ChangeStateNotify{ChangeStateNotify: &protocol.ChangeStateNotify{FsmId: cs.GetFsmId(), FromState: cs.GetFromState(), ToState: cs.GetToState()}}})
		resp.Message = &protocol.CombatResponseData_ChangeStateResponse{ChangeStateResponse: &protocol.ChangeStateResponse{
			Error: &protocol.DErrorResult{ErrorCode: ok}, FsmId: cs.GetFsmId(), CurrentState: cs.GetToState()}}
	case *protocol.CombatRequestData_ChangeStateConfirmRequest:
		cr := m.ChangeStateConfirmRequest
		c.updateFsmState(sender, cr.GetFsmId(), cr.GetState())
		resp.Message = &protocol.CombatResponseData_ChangeStateConfirmResponse{ChangeStateConfirmResponse: &protocol.ChangeStateConfirmResponse{
			Error: &protocol.DErrorResult{ErrorCode: ok}, FsmId: cr.GetFsmId(), State: cr.GetState()}}
	case *protocol.CombatRequestData_FsmConditionPassRequest:
		f := m.FsmConditionPassRequest
		c.reportCondition(sender, f.GetFromState(), f.GetToState(), f.GetConditionIndex(), f.GetValue())
		resp.Message = &protocol.CombatResponseData_FsmConditionPassResponse{FsmConditionPassResponse: &protocol.FsmConditionPassResponse{
			Error: &protocol.DErrorResult{ErrorCode: ok}, FsmId: m.FsmConditionPassRequest.GetFsmId()}}
	case *protocol.CombatRequestData_FsmStateBehaviorRequest:
		b := m.FsmStateBehaviorRequest
		resp.Message = &protocol.CombatResponseData_FsmStateBehaviorResponse{FsmStateBehaviorResponse: &protocol.FsmStateBehaviorResponse{
			ErrorCode: ok, FsmId: b.GetFsmId(), State: b.GetState()}}
	case *protocol.CombatRequestData_FsmPlayMontageRequest:
		resp.Message = &protocol.CombatResponseData_FsmPlayMontageResponse{FsmPlayMontageResponse: &protocol.FsmPlayMontageResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_LogicStateInitRequest:
		l := m.LogicStateInitRequest
		id := l.GetEntityId()
		if id == 0 {
			id = sender
		}
		if e := c.scene.GetEntity(id); e != nil && l.GetInitData() != nil {
			e.LogicState = proto.Clone(l.InitData).(*protocol.LogicStateComponentPb)
		}
		resp.Message = &protocol.CombatResponseData_LogicStateInitResponse{LogicStateInitResponse: &protocol.LogicStateInitResponse{ErrorCode: ok}}
	case *protocol.CombatRequestData_SwitchLogicStateRequest:
		s := m.SwitchLogicStateRequest
		if e := c.scene.GetEntity(sender); e != nil && s.GetStates() != nil {
			e.LogicState = proto.Clone(s.States).(*protocol.LogicStateComponentPb)
		}
		resp.Message = &protocol.CombatResponseData_SwitchLogicStateResponse{SwitchLogicStateResponse: &protocol.SwitchLogicStateResponse{ErrorCode: ok}}
	default:
		common.Info("combat", "unhandled combat message: "+caseName(req.Message, "CombatRequestData_"))
		return
	}
	if code := protocol.ErrorCodeOf(resp); code != protocol.ErrorCode_ErrorCode_Success {
		common.Warn("reply", fmt.Sprintf("%s answered %s (%d)", caseName(req.Message, "CombatRequestData_"), code, int32(code)))
	}
	recv.Data = append(recv.Data, &protocol.CombatReceiveData{Message: &protocol.CombatReceiveData_CombatResponseData{CombatResponseData: resp}})
	addNotifies(recv, out)
}

func (c *CombatService) processPush(p *protocol.CombatPushData, nowMs int64, sink PushSink, recv *protocol.CombatReceivePackNotify) {
	var sender int64
	if p.CombatCommon != nil {
		sender = p.CombatCommon.EntityId
	}
	var out []*protocol.CombatNotifyData
	switch m := p.Message.(type) {
	case *protocol.CombatPushData_ApplyGameplayEffectPush:
		c.buffs.HandleApplyGameplayEffectPush(sender, m.ApplyGameplayEffectPush, nowMs, &out)
	case *protocol.CombatPushData_ApplyBuffS2CResponsePush, *protocol.CombatPushData_RemoveBuffS2CResponsePush, *protocol.CombatPushData_HitEndPush:
	case *protocol.CombatPushData_EndSkillPush:
		if e := c.scene.GetEntity(sender); e != nil {
			e.FsmDirty = true
		}
	case *protocol.CombatPushData_AiHatePush:
		c.handleAiHateList(sender, m.AiHatePush.GetHateList(), sink)
	case *protocol.CombatPushData_AnimationGameplayTagPush:
		if e := c.scene.GetEntity(sender); e != nil {
			e.AdjustTag(m.AnimationGameplayTagPush.GetChangedTagId(), m.AnimationGameplayTagPush.GetIsAdd())
		}
	case *protocol.CombatPushData_RTimeStopPush:
		c.scene.RTimeDilation = m.RTimeStopPush.GetDilation()
	case *protocol.CombatPushData_ChangeStateConfirmPush:
		c.updateFsmState(sender, m.ChangeStateConfirmPush.GetFsmId(), m.ChangeStateConfirmPush.GetState())
	case *protocol.CombatPushData_FsmConditionPassPush:
		f := m.FsmConditionPassPush
		c.reportCondition(sender, f.GetFromState(), f.GetToState(), f.GetConditionIndex(), f.GetValue())
	case *protocol.CombatPushData_FsmPlayMontagePush:
	case *protocol.CombatPushData_LogicStateInitPush:
		l := m.LogicStateInitPush
		id := l.GetEntityId()
		if id == 0 {
			id = sender
		}
		if e := c.scene.GetEntity(id); e != nil && l.GetInitData() != nil {
			e.LogicState = proto.Clone(l.InitData).(*protocol.LogicStateComponentPb)
		}
	case *protocol.CombatPushData_SwitchLogicStatePush:
		if e := c.scene.GetEntity(sender); e != nil && m.SwitchLogicStatePush.GetStates() != nil {
			e.LogicState = proto.Clone(m.SwitchLogicStatePush.States).(*protocol.LogicStateComponentPb)
		}
	default:
		common.Info("combat", "unhandled combat message: "+caseName(p.Message, "CombatPushData_"))
		return
	}
	addNotifies(recv, out)
}

func (c *CombatService) handleDamageExecute(req *protocol.DamageExecuteRequest, nowMs int64, sink PushSink, out *[]*protocol.CombatNotifyData) *protocol.DamageExecuteResponse {
	d, ok := c.tables.DamageById[float64(req.DamageId)]
	if !ok {
		return &protocol.DamageExecuteResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrConfDamageNotFound}
	}
	stub := &protocol.DamageExecuteResponse{ErrorCode: codeSuccess, Damage: 1, ElementType: d.Element}
	attacker := c.scene.GetEntity(req.AttackerEntityId)
	if attacker == nil {
		return stub
	}
	// The attacker's Concerto and liberation energy, whatever the target (specs/combat.md §5).
	var changed []Attr
	if g := ComputeConcertoGain(d, attacker.Attributes); g != 0 {
		changed = append(changed, attacker.Attributes.ApplyDelta(AttrElementEnergy, g, false)...)
	}
	changed = append(changed, attacker.Attributes.ApplyDelta(AttrEnergy, LiberationEnergyGainPerHit, false)...)
	if len(changed) > 0 {
		attacker.FsmDirty = true
		*out = append(*out, attacker.Attributes.BuildChangeNotifies(attacker.NetID, changed, nowMs)...)
	}
	target := c.scene.GetEntity(req.TargetEntityId)
	if target == nil {
		return stub
	}
	if d.CalculateType == 1 { // §5.2 healing
		before := target.Life()
		heal := ComputeHeal(d, attacker.Attributes, target.Attributes, req.SkillLevel)
		if ch := target.Attributes.ApplyDelta(AttrLife, heal, false); len(ch) > 0 {
			target.FsmDirty = true
			*out = append(*out, target.Attributes.BuildChangeNotifies(target.NetID, ch, nowMs)...)
		}
		actual := max(target.Life()-before, 0)
		change := heal
		if actual > 0 {
			change = actual
		}
		return &protocol.DamageExecuteResponse{ErrorCode: codeSuccess, Damage: heal, ChangeLife: change, ElementType: d.Element,
			AttackerEntityId: req.AttackerEntityId, TargetEntityId: req.TargetEntityId, PartId: req.PartId}
	}
	if d.CalculateType != 0 { // §5.3 neither
		return stub
	}
	before := target.Life()
	amount, crit := ComputeDamage(d, attacker.Attributes, target.Attributes, req.SkillLevel, req.ForceCritical, c.rng)
	if ch := target.Attributes.ApplyDelta(AttrLife, -amount, false); len(ch) > 0 {
		target.FsmDirty = true
		*out = append(*out, target.Attributes.BuildChangeNotifies(target.NetID, ch, nowMs)...)
	}
	killed := before > 0 && target.Life() <= 0
	if killed {
		*out = append(*out, livingNotify(target.NetID, livingDead))
		c.scene.UpdateCombatant(target.NetID, false, sink)
		if target.IsMonster() {
			c.kills.OnMonsterKilled(target, nowMs, sink)
		} else if target.IsPlayerRole() {
			c.checkPartyWipe(sink)
		}
	}
	return &protocol.DamageExecuteResponse{ErrorCode: codeSuccess, Damage: amount, ChangeLife: 0, IsCrit: crit, KilledTarget: killed,
		AttackerEntityId: req.AttackerEntityId, TargetEntityId: req.TargetEntityId, PartId: req.PartId, ElementType: d.Element}
}

// HandleLandingDamage (R-118, specs/combat.md §5.6) always answers Success: anything else opens
// an error popup on the client.
func (c *CombatService) HandleLandingDamage(req *protocol.LandingDamageRequest, nowMs int64, sink PushSink) *protocol.LandingDamageResponse {
	if e := c.scene.GetEntity(req.EntityId); e != nil && e.IsPlayerRole() {
		dmg := LandingRoleDamage(c.landingArgs, req.SpeedZ, req.TimeExceeding, e.LifeMax())
		if lost, killed := ApplyLanding(e.Attributes, dmg); lost > 0 {
			e.FsmDirty = true
			ns := e.Attributes.BuildChangeNotifies(e.NetID, []Attr{AttrLife}, nowMs)
			if killed {
				ns = append(ns, livingNotify(e.NetID, livingDead))
				c.scene.UpdateCombatant(e.NetID, false, sink)
			}
			PushCombatNotifies(sink, ns)
			if killed {
				c.checkPartyWipe(sink)
			}
		}
	}
	return &protocol.LandingDamageResponse{ErrorCode: codeSuccess}
}

func (c *CombatService) HandleRevive(req *protocol.ReviveRequest, nowMs int64, sink PushSink) *protocol.ReviveResponse {
	return c.revive.HandleRevive(req, nowMs, sink)
}

func (c *CombatService) HandleTimeCheck(req *protocol.TimeCheckRequest, nowMs int64) *protocol.TimeCheckResponse {
	return &protocol.TimeCheckResponse{Code: codeSuccess, ClientTime: req.ClientTime, ServerTime: nowMs, ServerStopTime: nowMs,
		SceneStopTime: nowMs, FlowTime: c.scene.GetFlowTimeMs(nowMs)}
}

func (c *CombatService) HandleTimeStopPush(p *protocol.TimeStopPush, nowMs int64) {
	c.scene.SetTimeDilation(p.TimeDilation, nowMs)
}

func (c *CombatService) HandleRTimeStopPush(p *protocol.RTimeStopPush) {
	c.scene.RTimeDilation = p.Dilation
}

func HandlePrefixInfo(*protocol.CombatMessagePrefixInfoRequest) *protocol.CombatMessagePrefixInfoResponse {
	return &protocol.CombatMessagePrefixInfoResponse{}
}

func HandlePostInfo(*protocol.CombatMessagePostInfoRequest) *protocol.CombatMessagePostInfoResponse {
	return &protocol.CombatMessagePostInfoResponse{}
}

func (c *CombatService) checkPartyWipe(sink PushSink) {
	any := false
	for _, r := range c.scene.PlayerRoleEntities() {
		any = true
		if r.Life() > 0 {
			return
		}
	}
	if !any {
		return
	}
	sink.Push(&protocol.PlayerDeadNotify{PlayerId: c.scene.PlayerID, DelaySeconds: 0, IsAutoRevive: true, ReviveId: 2, IsShowRevive: false})
}

func (c *CombatService) handleAiHateList(sender int64, hate []*protocol.AiHateEntity, sink PushSink) {
	var party []int64
	for _, r := range c.formation.ActiveFormation().FilledRoles() {
		if r.EntityID > 0 {
			party = append(party, r.EntityID)
		}
	}
	hates := false
	for _, h := range hate {
		if h.HatredValue >= 1 && slices.Contains(party, h.EntityId) {
			hates = true
			break
		}
	}
	if e := c.scene.GetEntity(sender); e != nil {
		e.HasHate = hates
		e.FsmDirty = true
	}
	c.scene.UpdateCombatant(sender, hates, sink)
}

func (c *CombatService) handleCombineEntities(req *protocol.CombineEntitiesRequest, out *[]*protocol.CombatNotifyData) *protocol.CombineEntitiesResponse {
	info := req.CombinePartInfo
	if info == nil {
		return &protocol.CombineEntitiesResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrLackCombinePartInfoParam}
	}
	target := c.scene.GetEntity(req.TargetEntity)
	if target == nil {
		return &protocol.CombineEntitiesResponse{ErrorCode: codeEntityMissing}
	}
	if c.scene.GetEntity(info.CombinerEntityId) == nil {
		return &protocol.CombineEntitiesResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrCombinerEntityNotExists}
	}
	if _, has := c.tables.CharacterPartConfigByModelId[target.ModelID]; target.ModelID > 0 && has && !PartAllowsCombine(c.tables, target.ModelID, info.PartId) {
		return &protocol.CombineEntitiesResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrCombineComponentNotExists}
	}
	c.scene.CombineRelations = slices.DeleteFunc(c.scene.CombineRelations, func(r *CombineRelation) bool {
		return r.TargetEntityID == req.TargetEntity && r.CombinerEntityID == info.CombinerEntityId
	})
	c.scene.CombineRelations = append(c.scene.CombineRelations, &CombineRelation{req.TargetEntity, info.CombinerEntityId, proto.Clone(info).(*protocol.CombinePartInfo)})
	*out = append(*out, protocol.Notify(req.TargetEntity, &protocol.CombatNotifyData_AddCombineEntitiesRelationNotify{AddCombineEntitiesRelationNotify: &protocol.AddCombineEntitiesRelationNotify{
		TargetEntity: req.TargetEntity, CombinePartInfo: proto.Clone(info).(*protocol.CombinePartInfo),
	}}))
	return &protocol.CombineEntitiesResponse{ErrorCode: codeSuccess}
}

func (c *CombatService) handleDissolveCombine(req *protocol.DissolveCombineRelationRequest, out *[]*protocol.CombatNotifyData) *protocol.DissolveCombineRelationResponse {
	i := slices.IndexFunc(c.scene.CombineRelations, func(r *CombineRelation) bool {
		return (r.TargetEntityID == req.EntityA && r.CombinerEntityID == req.EntityB) || (r.TargetEntityID == req.EntityB && r.CombinerEntityID == req.EntityA)
	})
	target, combiner := req.EntityA, req.EntityB
	if i >= 0 {
		m := c.scene.CombineRelations[i]
		target, combiner = m.TargetEntityID, m.CombinerEntityID
		c.scene.CombineRelations = slices.Delete(c.scene.CombineRelations, i, i+1)
	}
	if e := c.scene.GetEntity(target); e != nil {
		e.FsmDirty = true
	}
	if e := c.scene.GetEntity(combiner); e != nil {
		e.FsmDirty = true
	}
	*out = append(*out, protocol.Notify(target, &protocol.CombatNotifyData_RemoveCombineRelationNotify{RemoveCombineRelationNotify: &protocol.RemoveCombineRelationNotify{
		CombinerEntity: combiner, TargetEntity: target,
	}}))
	return &protocol.DissolveCombineRelationResponse{ErrorCode: codeSuccess}
}

// clientSwitch is a switch the client made (ChangeStateRequest): the server's runtime follows it.
func (c *CombatService) clientSwitch(entityID int64, fsmID, to int32, nowMs int64) {
	e := c.scene.GetEntity(entityID)
	if e == nil || e.Fsm == nil {
		c.updateFsmState(entityID, fsmID, to)
		return
	}
	e.Fsm.SwitchTo(to, nowMs)
	e.syncFsmComponent()
	e.FsmDirty = true
}

func (c *CombatService) reportCondition(entityID int64, from, to, index int32, value bool) {
	if e := c.scene.GetEntity(entityID); e != nil && e.Fsm != nil {
		e.Fsm.Report(from, to, index, value)
		e.FsmDirty = true
	}
}

// StartFsm gives an entity the server's side of its state machines, started at the states its
// EntityFsmComponentPb sends the client.
func (c *CombatService) StartFsm(e *CombatEntity, g *FsmGraph, nowMs int64) {
	if e == nil || g == nil || e.FsmComponent == nil {
		return
	}
	e.Fsm = NewFsmRuntime(g, e.FsmComponent.Fsms, nowMs, c.rng)
}

// StepFsms makes the server's state machine switches that hold now for every entity in the
// scene (specs/combat.md §9.7) and returns their ChangeStateNotifies.
func (c *CombatService) StepFsms(nowMs int64) []*protocol.CombatNotifyData {
	var out []*protocol.CombatNotifyData
	for id, e := range c.scene.Entities.All() {
		if e.Fsm == nil {
			continue
		}
		switches := e.Fsm.Step(e, nowMs)
		for _, s := range switches {
			out = append(out, protocol.Notify(id, &protocol.CombatNotifyData_ChangeStateNotify{ChangeStateNotify: &protocol.ChangeStateNotify{
				FsmId: s.FsmID, FromState: s.From, ToState: s.To}}))
		}
		if len(switches) > 0 {
			e.syncFsmComponent()
		}
		e.FsmDirty = false
	}
	return out
}

// TickFsms pushes the switches that fell due with time alone (CondTimer).
func (c *CombatService) TickFsms(nowMs int64, sink PushSink) {
	if n := c.StepFsms(nowMs); sink != nil && len(n) > 0 {
		PushCombatNotifies(sink, n)
	}
}

func (c *CombatService) updateFsmState(entityID int64, fsmID, state int32) {
	e := c.scene.GetEntity(entityID)
	if e == nil || e.FsmComponent == nil {
		return
	}
	for _, f := range e.FsmComponent.Fsms {
		if f.FsmId == fsmID {
			f.CurrentState = state
			e.FsmDirty = true
			return
		}
	}
}

// handleFragileChange answers the client's weak-time start and end (specs/combat.md §9.1, R-164).
// At a break the client zeroes the entity's ToughRecover and never restores it: when the weak
// time ends the server refills Tough and sends the recovery rate back, or every later hit
// staggers the monster.
func (c *CombatService) handleFragileChange(sender int64, req *protocol.FragileChangeRequest, nowMs int64, out *[]*protocol.CombatNotifyData) *protocol.FragileChangeResponse {
	id := req.GetEntityId()
	if id == 0 {
		id = sender
	}
	if e := c.scene.GetEntity(id); e != nil && e.Attributes != nil && !req.GetFlag() {
		e.Attributes.ApplyOverride(AttrTough, e.Attributes.Slots[AttrToughMax].Current)
		*out = append(*out, e.Attributes.BuildChangeNotifies(id, []Attr{AttrTough}, nowMs)...)
	}
	return &protocol.FragileChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}
