// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"slices"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

var DefaultRoleIDs = []int32{1211, 1108, 1506}

const (
	DefaultCurRoleID     = 1211
	FormationAttrCurTime = 1534854458
	codeFormationUnknown = protocol.ErrorCode_ErrorCode_ErrFormationUnknown
)

// FormationService is formation_info, the formation sync at login and scene join (R-114
// UpdateSceneGroupDataNotify), the role entities and their diffs, R-119 reordering and role
// switches (specs/formation.md).
type FormationService struct {
	tables       *data.DataTables
	player       *save.PlayerData
	scene        *CombatSceneState
	buffBuilder  *RoleBuffBuilder
	concomitants *ConcomitantResolver
	passives     *PassiveSkillsIndex
	fsm          *FsmInitializer
	buffs        *BuffManager
	hooks        GameHooks
}

func NewFormationService(t *data.DataTables, p *save.PlayerData, s *CombatSceneState, bb *RoleBuffBuilder, c *ConcomitantResolver,
	ps *PassiveSkillsIndex, fsm *FsmInitializer, b *BuffManager, hooks GameHooks) *FormationService {
	if hooks == nil {
		hooks = NullHooks{}
	}
	return &FormationService{t, p, s, bb, c, ps, fsm, b, hooks}
}

func (f *FormationService) ActiveFormation() *save.FormationSlotSave {
	fi := f.player.FormationInfo
	return fi.Formations[clamp32(fi.CurFormation, 0, save.FormationSlotCount-1)]
}

// EnsureDefaults keeps cur_formation in 0 … 19 and gives an empty formation 0 the default roles
// (specs/formation.md §1).
func (f *FormationService) EnsureDefaults() bool {
	fi := f.player.FormationInfo
	changed := false
	if fi.CurFormation < 0 || fi.CurFormation >= save.FormationSlotCount {
		fi.CurFormation = 0
		changed = true
	}
	f0 := fi.Formations[0]
	if !f0.HasAnyRole() {
		fi.CurFormation = 0
		f0.CurRole = DefaultCurRoleID
		empty := ""
		f0.Name = &empty
		for i, id := range DefaultRoleIDs {
			f0.Roles[i] = save.NewFormationRole(id)
		}
		changed = true
	}
	if changed {
		save.SaveFormationInfo(f.player)
	}
	return changed
}

// SpawnActiveFormation spawns the active formation's roles into the scene (the scene join).
func (f *FormationService) SpawnActiveFormation(nowMs int64) []*protocol.EntityPb {
	f.EnsureDefaults()
	active := f.ActiveFormation()
	var all []*protocol.EntityPb
	for _, slot := range active.Roles {
		if slot == nil || slot.RoleID <= 0 {
			continue
		}
		e, pb, cons := f.SpawnRoleWithConcomitants(slot.RoleID, slot.RoleID == active.CurRole, nowMs)
		slot.EntityID = e.NetID
		all = append(all, pb)
		all = append(all, cons...)
	}
	save.SaveFormationInfo(f.player)
	return all
}

// SpawnRoleWithConcomitants makes a role entity and its vision and custom concomitant entities.
func (f *FormationService) SpawnRoleWithConcomitants(roleID int32, isCurrent bool, nowMs int64) (*CombatEntity, *protocol.EntityPb, []*protocol.EntityPb) {
	role, ok := f.player.Roles.Get(roleID)
	if !ok {
		role = save.NewRole(roleID)
		role.Level, role.Breakthrough = 90, 6
	}
	weapon := f.player.EquippedWeapon(role)
	snap := ComputeLive(f.tables, f.player, role, f.buffBuilder.RoleAutoBuffTags(roleID))
	netID := f.scene.AllocateNetID()
	attrs := NewAttributeSet()
	attrs.LoadFrom(snap.BaseProps, snap.AddProps)
	e := NewCombatEntity(netID)
	e.ConfigID, e.ConfigType, e.EntityType = roleID, protocol.EntityConfigType_EntityConfigType_Character, protocol.EEntityType_EEntityType_Player
	e.PlayerID, e.MapID, e.IsVisible = f.player.PlayerID, f.scene.MapConfigID, isCurrent
	e.PosX, e.PosY, e.PosZ = f.scene.PlayerX, f.scene.PlayerY, f.scene.PlayerZ
	e.Pitch, e.Yaw, e.Roll = f.scene.PlayerPitch, f.scene.PlayerYaw, f.scene.PlayerRoll
	e.Attributes = attrs
	if weapon != nil {
		e.WeaponID, e.WeaponBreachLevel = weapon.ID, weapon.Breach
	}
	e.FsmComponent = f.fsm.BuildForRole(roleID)

	// The role's first buffs (specs/roles.md §5.3).
	buffs, clientApply := f.buffBuilder.BuildForRole(f.player, role, snap)
	for _, s := range buffs {
		b := NewBuffEntry()
		b.HandleID, b.BuffID, b.InstigatorID, b.EntityID = f.scene.AllocateBuffHandle(), s.BuffID, netID, netID
		b.IsActive, b.ConfBuffID = s.IsActive, s.BuffID
		e.Buffs = append(e.Buffs, b)
	}
	e.NeedClientApplyBuffs = append(e.NeedClientApplyBuffs, clientApply...)

	// Visions and custom concomitants (specs/roles.md §5.4, entities.md §6).
	var cons []*protocol.EntityPb
	var visionModels []int32
	main, combo := f.concomitants.ResolveVisionSummons(f.player, roleID)
	var mainID, comboID int64
	if main != nil {
		e.MainEchoSummonID = main.SummonSkillID
		ve, vpb := f.spawnVision(netID, main, nowMs)
		mainID = ve.NetID
		e.VisionEntityIDs = append(e.VisionEntityIDs, ve.NetID)
		visionModels = append(visionModels, main.ModelID)
		cons = append(cons, vpb)
	}
	if combo != nil {
		e.ComboEchoSummonID = combo.SummonSkillID
		ve, vpb := f.spawnVision(netID, combo, nowMs)
		comboID = ve.NetID
		e.VisionEntityIDs = append(e.VisionEntityIDs, ve.NetID)
		visionModels = append(visionModels, combo.ModelID)
		cons = append(cons, vpb)
	}
	e.VisionSkillComponent = f.concomitants.BuildVisionSkillComponent(main, mainID, combo, comboID)
	for _, c := range f.concomitants.CustomConcomitants(roleID) {
		for range c.SpawnCount {
			ce, cpb := f.spawnCustom(netID, c, nowMs)
			e.CustomEntityIDs = append(e.CustomEntityIDs, ce.NetID)
			cons = append(cons, cpb)
		}
	}
	ids := make([]int64, len(e.Buffs))
	for i, b := range e.Buffs {
		ids[i] = b.BuffID
	}
	e.SkillComponent = f.passives.BuildForRole(roleID, ids, visionModels, e.VisionSkillComponent)
	f.scene.Entities.Set(netID, e)
	f.buffs.EnsureEntityTimersRegistered(e)
	return e, f.BuildRoleEntityPb(e, role, nowMs), cons
}

func (f *FormationService) bornBuffs(netID int64, ids []int64, nowMs int64, e *CombatEntity) *protocol.FightBuffComponentPb {
	pb := &protocol.FightBuffComponentPb{}
	for _, id := range ids {
		b := NewBuffEntry()
		b.HandleID, b.BuffID, b.InstigatorID, b.EntityID, b.ConfBuffID = f.scene.AllocateBuffHandle(), id, netID, netID, id
		e.Buffs = append(e.Buffs, b)
		pb.FightBuffInfos = append(pb.FightBuffInfos, b.ToProto(nowMs))
	}
	return pb
}

func (f *FormationService) summonEntity(templateID, modelID int32, typ protocol.EEntityType) *CombatEntity {
	bx, by, bz, yaw := f.bornTransform()
	e := NewCombatEntity(f.scene.AllocateNetID())
	e.ConfigID, e.ConfigType, e.EntityType = templateID, protocol.EntityConfigType_EntityConfigType_Template, typ
	e.PlayerID, e.ModelID, e.MapID, e.IsVisible = f.player.PlayerID, modelID, f.scene.MapConfigID, false
	e.PosX, e.PosY, e.PosZ, e.Yaw = bx, by, bz, yaw
	return e
}

func comp(c protocol.EntityComponent) *protocol.EntityComponentPb { return protocol.Component(c) }

func (f *FormationService) spawnVision(summoner int64, s *VisionSummonSpec, nowMs int64) (*CombatEntity, *protocol.EntityPb) {
	e := f.summonEntity(s.TemplateID, s.ModelID, protocol.EEntityType_EEntityType_Vision)
	e.SkillComponent = BuildSkillsForIDs(f.passives.SkillsForModel(s.ModelID))
	fb := f.bornBuffs(e.NetID, s.BornBuffIDs, nowMs, e)
	f.scene.Entities.Set(e.NetID, e)
	pb := baseEntityPb(e)
	pb.ComponentPbs = append(pb.ComponentPbs,
		comp(&protocol.EntityComponentPb_AttributeComponent{AttributeComponent: e.Attributes.ToAttributeComponentPb(0, 0)}),
		comp(&protocol.EntityComponentPb_FightBuffComponent{FightBuffComponent: fb}),
		comp(&protocol.EntityComponentPb_CombineEntityInfoPb{CombineEntityInfoPb: &protocol.CombineEntityInfoPb{}}),
		comp(&protocol.EntityComponentPb_EquipComponent{EquipComponent: &protocol.EquipComponentPb{}}),
		comp(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: proto.Clone(e.LogicState).(*protocol.LogicStateComponentPb)}),
		comp(&protocol.EntityComponentPb_MonsterAiComponentPb{MonsterAiComponentPb: &protocol.MonsterAiComponentPb{CombatMessageId: AiContextID}}),
		comp(&protocol.EntityComponentPb_SkillComponentPb{SkillComponentPb: e.SkillComponent}),
		comp(&protocol.EntityComponentPb_SummonerComponent{SummonerComponent: &protocol.SummonerComponentPb{
			SummonerId: summoner, SummonCfgId: s.SummonSkillID, SummonSkillId: s.SummonSkillID, PlayerId: f.player.PlayerID,
			Type: protocol.ESummonType_ESummonType_ESummonTypeConcomitantVision,
		}}),
		comp(&protocol.EntityComponentPb_VisionSkillComponent{VisionSkillComponent: &protocol.VisionSkillComponentPb{}}),
	)
	return e, pb
}

func (f *FormationService) spawnCustom(summoner int64, s ConcomitantSpec, nowMs int64) (*CombatEntity, *protocol.EntityPb) {
	e := f.summonEntity(s.TemplateID, s.ModelID, protocol.EEntityType_EEntityType_Monster)
	fb := f.bornBuffs(e.NetID, s.BornBuffIDs, nowMs, e)
	f.scene.Entities.Set(e.NetID, e)
	pb := baseEntityPb(e)
	pb.ComponentPbs = append(pb.ComponentPbs,
		comp(&protocol.EntityComponentPb_AttributeComponent{AttributeComponent: e.Attributes.ToAttributeComponentPb(0, 0)}),
		comp(&protocol.EntityComponentPb_FightBuffComponent{FightBuffComponent: fb}),
		comp(&protocol.EntityComponentPb_CombineEntityInfoPb{CombineEntityInfoPb: &protocol.CombineEntityInfoPb{}}),
		comp(&protocol.EntityComponentPb_EquipComponent{EquipComponent: &protocol.EquipComponentPb{}}),
		comp(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: proto.Clone(e.LogicState).(*protocol.LogicStateComponentPb)}),
		comp(&protocol.EntityComponentPb_MonsterAiComponentPb{MonsterAiComponentPb: &protocol.MonsterAiComponentPb{CombatMessageId: AiContextID}}),
		comp(&protocol.EntityComponentPb_SummonerComponent{SummonerComponent: &protocol.SummonerComponentPb{
			SummonerId: summoner, SummonCfgId: s.SummonCfgID, SummonSkillId: 0, PlayerId: f.player.PlayerID,
			Type: protocol.ESummonType_ESummonType_ESummonTypeConcomitantCustom,
		}}),
	)
	return e, pb
}

// BuildRoleEntityPb is a role entity in the component order of specs/entities.md §6.
func (f *FormationService) BuildRoleEntityPb(e *CombatEntity, role *save.RoleSave, nowMs int64) *protocol.EntityPb {
	pb := baseEntityPb(e)
	pb.RoleSkinId, pb.GlideWingSkinId, pb.GlideBrollySkinId = role.RoleSkinID, role.SoarSkinID, role.ParaglidingSkinID
	fb := &protocol.FightBuffComponentPb{}
	for _, b := range e.Buffs {
		fb.FightBuffInfos = append(fb.FightBuffInfos, b.ToProto(nowMs))
	}
	if len(e.NeedClientApplyBuffs) > 0 {
		fb.NeedClientApplyBuffIds = append(fb.NeedClientApplyBuffIds, e.NeedClientApplyBuffs...)
		fb.NeedClientApplyBuffIdsMessageId = e.NetID
	}
	cons := &protocol.ConcomitantsComponentPb{PhantomRoleEid: 0,
		VisionEntityIds: slices.Clone(e.VisionEntityIDs), CustomEntityIds: slices.Clone(e.CustomEntityIDs)}
	orn := &protocol.OrnamentComponentPb{}
	for _, o := range role.Ornaments {
		if o.RoleSkinID == role.RoleSkinID {
			orn.OrnamentIds = append(orn.OrnamentIds, o.OrnamentID)
		}
	}
	skills := e.SkillComponent
	if skills == nil {
		skills = &protocol.SkillComponentPb{}
	}
	vision := e.VisionSkillComponent
	if vision == nil {
		vision = &protocol.VisionSkillComponentPb{}
	}
	pb.ComponentPbs = append(pb.ComponentPbs,
		comp(&protocol.EntityComponentPb_AttributeComponent{AttributeComponent: e.Attributes.ToAttributeComponentPb(0, 0)}),
		comp(&protocol.EntityComponentPb_FightBuffComponent{FightBuffComponent: fb}),
		comp(&protocol.EntityComponentPb_ConcomitantsComponentPb{ConcomitantsComponentPb: cons}),
		comp(&protocol.EntityComponentPb_EquipComponent{EquipComponent: &protocol.EquipComponentPb{WeaponId: e.WeaponID, WeaponBreachLevel: e.WeaponBreachLevel}}),
		comp(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: proto.Clone(e.LogicState).(*protocol.LogicStateComponentPb)}),
		comp(&protocol.EntityComponentPb_SkillComponentPb{SkillComponentPb: skills}),
		comp(&protocol.EntityComponentPb_DependenceComponentPb{DependenceComponentPb: &protocol.DependenceComponentPb{DependenceEntityIds: []int64{1, 2, 1, 2}}}),
	)
	if e.FsmComponent != nil {
		pb.ComponentPbs = append(pb.ComponentPbs, comp(&protocol.EntityComponentPb_EntityFsmComponentPb{EntityFsmComponentPb: proto.Clone(e.FsmComponent).(*protocol.EntityFsmComponentPb)}))
	}
	pb.ComponentPbs = append(pb.ComponentPbs,
		comp(&protocol.EntityComponentPb_VisionSkillComponent{VisionSkillComponent: vision}),
		comp(&protocol.EntityComponentPb_RoleWeaponSkinComponent{RoleWeaponSkinComponent: &protocol.RoleWeaponSkinComponentPb{WeaponSkinId: role.WeaponSkinID}}),
		comp(&protocol.EntityComponentPb_OrnamentComponentPb{OrnamentComponentPb: orn}),
		comp(&protocol.EntityComponentPb_CalabashSkinComponentPb{CalabashSkinComponentPb: &protocol.CalabashSkinComponentPb{CalabashSkinId: role.CalabashSkinID}}),
	)
	return pb
}

func baseEntityPb(e *CombatEntity) *protocol.EntityPb {
	return &protocol.EntityPb{
		Id: e.NetID, ConfigId: e.ConfigID, ConfigType: e.ConfigType, EntityType: int32(e.EntityType),
		Pos:                &protocol.Vector{X: e.PosX, Y: e.PosY, Z: e.PosZ},
		InitPos:            &protocol.Vector{X: e.PosX, Y: e.PosY, Z: e.PosZ},
		Rot:                &protocol.Rotator{Pitch: e.Pitch, Yaw: e.Yaw, Roll: e.Roll},
		IsVisible:          e.IsVisible,
		IsActorVisible:     true,
		PlayerId:           e.PlayerID,
		EntityState:        protocol.EntityState_EntityState_Default,
		GravityDirection:   &protocol.Vector{X: 0, Y: 0, Z: -1},
		InitLinearVelocity: &protocol.Vector{},
	}
}

func (f *FormationService) bornTransform() (x, y, z, yaw float32) {
	if inst, ok := f.tables.InstanceDungeonById[f.scene.InstanceID]; ok && len(inst.BornPosition) >= 3 {
		if len(inst.BornRotation) >= 3 {
			yaw = float32(inst.BornRotation[2])
		}
		return float32(inst.BornPosition[0]), float32(inst.BornPosition[1]), float32(inst.BornPosition[2]), yaw
	}
	return f.scene.PlayerX, f.scene.PlayerY, f.scene.PlayerZ, f.scene.PlayerYaw
}

// RemoveRoleWithConcomitants takes a role entity and its visions and concomitants out of the scene.
func (f *FormationService) RemoveRoleWithConcomitants(netID int64) []int64 {
	var removed []int64
	e, ok := f.scene.Entities.Get(netID)
	if netID <= 0 || !ok {
		return removed
	}
	f.scene.Entities.Delete(netID)
	removed = append(removed, netID)
	for _, id := range e.VisionEntityIDs {
		if f.scene.Entities.Delete(id) {
			removed = append(removed, id)
		}
	}
	for _, id := range e.CustomEntityIDs {
		if f.scene.Entities.Delete(id) {
			removed = append(removed, id)
		}
	}
	return removed
}

// BuildUpdateFormationNotify (specs/formation.md §2.1).
func (f *FormationService) BuildUpdateFormationNotify() *protocol.UpdateFormationNotify {
	pf := &protocol.PlayerFightFormations{PlayerId: f.player.PlayerID}
	fi := f.player.FormationInfo
	for i, slot := range fi.Formations {
		if !slot.HasAnyRole() {
			continue
		}
		name := ""
		if slot.Name != nil {
			name = *slot.Name
		}
		info := &protocol.FightFormationNotifyInfo{FormationId: int32(i + 1), CurRole: slot.CurRole, IsCurrent: int32(i) == fi.CurFormation, Name: name}
		for _, rs := range slot.FilledRoles() {
			role, ok := f.player.Roles.Get(rs.RoleID)
			if !ok {
				continue
			}
			w := f.player.EquippedWeapon(role)
			if w == nil {
				continue
			}
			var maxHp, curHp int32
			if live := f.scene.GetEntity(rs.EntityID); rs.EntityID > 0 && live != nil {
				maxHp, curHp = live.LifeMax(), live.Life()
			} else {
				snap := ComputeLive(f.tables, f.player, role, f.buffBuilder.RoleAutoBuffTags(role.RoleID))
				maxHp = snap.BaseProps[AttrLifeMax] + snap.AddProps[AttrLifeMax]
				curHp = snap.BaseProps[AttrLife] + snap.AddProps[AttrLife]
			}
			ri := &protocol.FormationRoleInfo{
				RoleId: role.RoleID, MaxHp: maxHp, CurHp: curHp, Level: role.Level, RoleSkinId: role.RoleSkinID,
				// Always with SkillBranchId (specs/formation.md §2.1).
				SkillBranchId: role.SkillBranch, WeaponId: w.ID, WeaponBreachLevel: w.Breach, WeaponSkinId: role.WeaponSkinID,
			}
			for _, o := range role.Ornaments {
				ri.DressList = append(ri.DressList, o.OrnamentID)
			}
			info.RoleInfos = append(info.RoleInfos, ri)
		}
		pf.Formations = append(pf.Formations, info)
	}
	return &protocol.UpdateFormationNotify{PlayersFormations: []*protocol.PlayerFightFormations{pf}}
}

// BuildFormationAttrNotify is the fixed FormationAttrNotify (specs/formation.md §2.2).
func BuildFormationAttrNotify() *protocol.FormationAttrNotify {
	return &protocol.FormationAttrNotify{CurTime: FormationAttrCurTime, FormationAttrs: []*protocol.FormationAttr{
		{AttrId: 1, Ratio: 2400, BaseMaxValue: 24000, MaxValue: 24000, CurrentValue: 22000},
		{AttrId: 10, Ratio: 2400, BaseMaxValue: 7000, MaxValue: 15000, CurrentValue: 7000},
		{AttrId: 14, Ratio: 0, BaseMaxValue: 10000, MaxValue: 10000, CurrentValue: 10000},
	}}
}

// BuildUpdateSceneGroupDataNotify is 3.8's UpdateSceneGroupDataNotify with Group = 1 and
// CurrentGroup = 1, not the GroupFormation name trap (R-114, specs/formation.md §3).
func (f *FormationService) BuildUpdateSceneGroupDataNotify() *protocol.UpdateSceneGroupDataNotify {
	active := f.ActiveFormation()
	g := &protocol.PlayerSceneGroupData{Group: 1, CurRole: active.CurRole, LivingStatus: protocol.LivingStatus_LivingStatus_Alive, FixedLocationByCfg: false}
	for _, rs := range active.FilledRoles() {
		g.FightRoleInfos = append(g.FightRoleInfos, &protocol.SceneRoleInformation{RoleId: rs.RoleID, EntityId: rs.EntityID, OnStageOutOfControl: rs.OnStageWithoutControl})
	}
	return &protocol.UpdateSceneGroupDataNotify{PlayerSceneGroupDataList: []*protocol.PlayerSceneGroupDataList{{
		PlayerId: f.player.PlayerID, CurrentGroup: 1, PlayerSceneGroupData: []*protocol.PlayerSceneGroupData{g},
	}}}
}

func (f *FormationService) PushSceneJoinSync(sink PushSink) {
	sink.Push(&protocol.SwitchBattleModeNotify{})
	sink.Push(BuildFormationAttrNotify())
	sink.Push(f.BuildUpdateFormationNotify())
}

// FindLiveRoleEntity is the spawned entity of a role of the active formation.
func (f *FormationService) FindLiveRoleEntity(roleID int32) *CombatEntity {
	for _, s := range f.ActiveFormation().FilledRoles() {
		if s.RoleID == roleID && s.EntityID > 0 {
			return f.scene.GetEntity(s.EntityID)
		}
	}
	return nil
}

func removeNotify(ids []int64) *protocol.EntityRemoveNotify {
	n := &protocol.EntityRemoveNotify{IsRemove: true}
	for _, id := range ids {
		n.RemoveInfos = append(n.RemoveInfos, &protocol.EntityRemoveInfo{EntityId: id})
	}
	return n
}

// RebuildLiveRoleEntity resets one role of the active formation (a Rover switch, an echo summon
// change; specs/formation.md §4.2).
func (f *FormationService) RebuildLiveRoleEntity(roleID int32, nowMs int64, sink PushSink) {
	active := f.ActiveFormation()
	var target *save.FormationRoleSave
	for _, s := range active.FilledRoles() {
		if s.RoleID == roleID {
			target = s
			break
		}
	}
	if target == nil {
		return
	}
	sink.Push(removeNotify(f.RemoveRoleWithConcomitants(target.EntityID)))
	e, pb, cons := f.SpawnRoleWithConcomitants(roleID, roleID == active.CurRole, nowMs)
	target.EntityID = e.NetID
	sink.Push(&protocol.EntityAddNotify{IsAdd: false, EntityPbs: []*protocol.EntityPb{pb}})
	if len(cons) > 0 {
		sink.Push(&protocol.EntityAddNotify{IsAdd: false, EntityPbs: cons})
	}
	sink.Push(f.BuildUpdateSceneGroupDataNotify())
	save.SaveFormationInfo(f.player)
	f.hooks.OnSceneSaved()
}

// RebuildActiveFormationEntities rebuilds every role entity of the active formation (roles.md §6.9–§6.10).
func (f *FormationService) RebuildActiveFormationEntities(nowMs int64, sink PushSink) {
	active := f.ActiveFormation()
	var removed []int64
	for _, s := range active.FilledRoles() {
		if s.EntityID > 0 {
			removed = append(removed, f.RemoveRoleWithConcomitants(s.EntityID)...)
		}
		s.EntityID = -1
	}
	sink.Push(removeNotify(removed))
	roles, cons := &protocol.EntityAddNotify{IsAdd: false}, &protocol.EntityAddNotify{IsAdd: false}
	for _, s := range active.FilledRoles() {
		e, pb, c := f.SpawnRoleWithConcomitants(s.RoleID, s.RoleID == active.CurRole, nowMs)
		s.EntityID = e.NetID
		roles.EntityPbs = append(roles.EntityPbs, pb)
		cons.EntityPbs = append(cons.EntityPbs, c...)
	}
	if len(roles.EntityPbs) > 0 {
		sink.Push(roles)
	}
	if len(cons.EntityPbs) > 0 {
		sink.Push(cons)
	}
	sink.Push(f.BuildUpdateSceneGroupDataNotify())
	save.SaveFormationInfo(f.player)
	f.hooks.OnSceneSaved()
}

// HandleGetFormationData (specs/formation.md §5.1).
func (f *FormationService) HandleGetFormationData(*protocol.GetFormationDataRequest) *protocol.GetFormationDataResponse {
	resp := &protocol.GetFormationDataResponse{ErrorCode: codeSuccess}
	fi := f.player.FormationInfo
	for i, slot := range fi.Formations {
		name := ""
		if slot.Name != nil {
			name = *slot.Name
		}
		ff := &protocol.FightFormation{FormationId: int32(i + 1), CurRole: slot.CurRole, IsCurrent: int32(i) == fi.CurFormation, Name: name}
		for _, r := range slot.FilledRoles() {
			ff.RoleIds = append(ff.RoleIds, r.RoleID)
		}
		resp.Formations = append(resp.Formations, ff)
	}
	return resp
}

// HandleUpdateFormation diffs the role entities (specs/formation.md §4.1, §5.2).
func (f *FormationService) HandleUpdateFormation(req *protocol.UpdateFormationRequest, nowMs int64, sink PushSink) *protocol.UpdateFormationResponse {
	fi := f.player.FormationInfo
	for _, ff := range req.Formations {
		idx := ff.FormationId - 1
		if idx < 0 || idx >= save.FormationSlotCount {
			continue
		}
		var newIDs []int32
		for _, id := range ff.RoleIds {
			if id > 0 && len(newIDs) < save.RolesPerFormation {
				newIDs = append(newIDs, id)
			}
		}
		if !ff.IsCurrent {
			t := fi.Formations[idx]
			if ff.Name != "" {
				name := ff.Name
				t.Name = &name
			}
			switch {
			case ff.CurRole != 0:
				t.CurRole = ff.CurRole
			case len(newIDs) > 0:
				t.CurRole = newIDs[0]
			default:
				t.CurRole = 0
			}
			for s := 0; s < save.RolesPerFormation; s++ {
				if s < len(newIDs) {
					t.Roles[s] = save.NewFormationRole(newIDs[s])
				} else {
					t.Roles[s] = nil
				}
			}
			continue
		}
		old := f.ActiveFormation()
		oldCur := old.CurRole
		oldSlot := 0
		for s, r := range old.Roles {
			if r != nil && r.RoleID == oldCur {
				oldSlot = s
				break
			}
		}
		var oldEntity omap.Map[int32, int64]
		for _, r := range old.FilledRoles() {
			if r.EntityID > 0 {
				oldEntity.Set(r.RoleID, r.EntityID)
			}
		}
		// 1. Remove the roles only in the old formation, with their concomitants.
		var removed []int64
		for rid, net := range oldEntity.All() {
			if !slices.Contains(newIDs, rid) {
				removed = append(removed, f.RemoveRoleWithConcomitants(net)...)
			}
		}
		if idx != fi.CurFormation {
			for _, r := range old.FilledRoles() {
				r.EntityID = -1
			}
		}
		// EntityRemoveNotify even when empty (specs/formation.md §4.1).
		sink.Push(removeNotify(removed))
		fi.CurFormation = idx
		t := fi.Formations[idx]
		if ff.Name != "" {
			name := ff.Name
			t.Name = &name
		}
		// 3. The current role, chosen before the spawns so IsVisible is right.
		var cur int32
		if oldCur > 0 && slices.Contains(newIDs, oldCur) {
			cur = oldCur
		} else {
			for s := min(oldSlot, len(newIDs)-1); s >= 0; s-- {
				if newIDs[s] > 0 {
					cur = newIDs[s]
					break
				}
			}
			if cur == 0 && len(newIDs) > 0 {
				cur = newIDs[0]
			}
		}
		t.CurRole = cur
		roles, cons := &protocol.EntityAddNotify{IsAdd: false}, &protocol.EntityAddNotify{IsAdd: false}
		for s := 0; s < save.RolesPerFormation; s++ {
			if s >= len(newIDs) {
				t.Roles[s] = nil
				continue
			}
			rid := newIDs[s]
			if kept, ok := oldEntity.Get(rid); ok && f.scene.GetEntity(kept) != nil {
				f.scene.GetEntity(kept).IsVisible = rid == cur
				r := save.NewFormationRole(rid)
				r.EntityID = kept
				t.Roles[s] = r
				continue
			}
			e, pb, c := f.SpawnRoleWithConcomitants(rid, rid == cur, nowMs)
			r := save.NewFormationRole(rid)
			r.EntityID = e.NetID
			t.Roles[s] = r
			roles.EntityPbs = append(roles.EntityPbs, pb)
			cons.EntityPbs = append(cons.EntityPbs, c...)
		}
		// 2. Two EntityAddNotify: the roles, then the concomitants.
		if len(roles.EntityPbs) > 0 {
			sink.Push(roles)
		}
		if len(cons.EntityPbs) > 0 {
			sink.Push(cons)
		}
		// 4. UpdateSceneGroupDataNotify (R-114).
		sink.Push(f.BuildUpdateSceneGroupDataNotify())
	}
	// 5. The buff timers, the save, UpdateFormationNotify, an empty success.
	for _, e := range f.scene.PlayerRoleEntities() {
		f.buffs.EnsureEntityTimersRegistered(e)
	}
	save.SaveFormationInfo(f.player)
	f.hooks.OnSceneSaved()
	sink.Push(f.BuildUpdateFormationNotify())
	return &protocol.UpdateFormationResponse{ErrorCode: codeSuccess}
}

// HandleReorderFormation (R-119, specs/formation.md §5.3): remove and insert, not swap; keeps
// cur_formation on its formation; pushes no UpdateFormationNotify.
func (f *FormationService) HandleReorderFormation(req *protocol.ReorderFormationRequest) *protocol.ReorderFormationResponse {
	if req.FormationId < 1 || req.FormationId > save.FormationSlotCount || req.TargetId < 1 || req.TargetId > save.FormationSlotCount {
		return &protocol.ReorderFormationResponse{ErrorCode: codeFormationUnknown}
	}
	src, dst := req.FormationId-1, req.TargetId-1
	if src != dst {
		fi := f.player.FormationInfo
		list := slices.Clone(fi.Formations[:])
		moved := list[src]
		list = slices.Delete(list, int(src), int(src)+1)
		list = slices.Insert(list, int(dst), moved)
		copy(fi.Formations[:], list)
		cur := fi.CurFormation
		switch {
		case cur == src:
			fi.CurFormation = dst
		case src < dst && cur > src && cur <= dst:
			fi.CurFormation = cur - 1
		case dst < src && cur >= dst && cur < src:
			fi.CurFormation = cur + 1
		}
		save.SaveFormationInfo(f.player)
		f.hooks.OnSceneSaved()
	}
	return &protocol.ReorderFormationResponse{ErrorCode: codeSuccess}
}

// HandleRenameFormation (specs/formation.md §5.4).
func (f *FormationService) HandleRenameFormation(req *protocol.RenameFormationRequest, sink PushSink) *protocol.RenameFormationResponse {
	idx := req.FormationId - 1
	if idx < 0 || idx >= save.FormationSlotCount {
		return &protocol.RenameFormationResponse{ErrorCode: codeFormationUnknown}
	}
	name := req.Name
	f.player.FormationInfo.Formations[idx].Name = &name
	save.SaveFormationInfo(f.player)
	f.hooks.OnSceneSaved()
	sink.Push(f.BuildUpdateFormationNotify())
	return &protocol.RenameFormationResponse{ErrorCode: codeSuccess}
}

// HandleFormationAttr is a placeholder (specs/formation.md §5.5).
func HandleFormationAttr(*protocol.FormationAttrRequest) *protocol.FormationAttrResponse {
	return &protocol.FormationAttrResponse{ErrorCode: codeSuccess}
}

// HandleSwitchRole is SwitchRoleRequest inside CombatSendPackRequest (specs/formation.md §5.6).
func (f *FormationService) HandleSwitchRole(req *protocol.SwitchRoleRequest, nowMs int64, out *[]*protocol.CombatNotifyData) *protocol.SwitchRoleResponse {
	active := f.ActiveFormation()
	prev := active.CurRole
	active.CurRole = req.RoleId
	for _, s := range active.FilledRoles() {
		e := f.scene.GetEntity(s.EntityID)
		if s.EntityID <= 0 || e == nil {
			continue
		}
		if s.RoleID == prev && prev != req.RoleId {
			e.IsVisible = false
		}
		if s.RoleID == req.RoleId {
			e.IsVisible = true
		}
		f.buffs.EnsureEntityTimersRegistered(e)
	}
	*out = append(*out, f.buffs.TickNotifies(nowMs)...)
	save.SaveFormationInfo(f.player)
	f.hooks.OnSceneSaved()
	return &protocol.SwitchRoleResponse{ErrorCode: codeSuccess, RoleId: req.RoleId}
}
