// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"slices"

	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// Short names of the error codes combat answers with.
const (
	codeSuccess      = protocol.ErrorCode_ErrorCode_Success
	codeParamError   = protocol.ErrorCode_ErrorCode_RequestParamError
	codeSkillParam   = protocol.ErrorCode_ErrorCode_ErrSkillInfoParamError
	codeItemsMissing = protocol.ErrorCode_ErrorCode_ErrRoleItemListNoEnough
	// codeItemNotEnough: 道具数量不足, a skill or skill-tree cost the player cannot pay (R-158).
	codeItemNotEnough = protocol.ErrorCode_ErrorCode_ErrItemNotEnough
)

// RoleService is role creation, the login repairs and pushes, live attribute refreshes,
// granting roles (R-38) and the role requests (specs/roles.md).
type RoleService struct {
	tables      *data.DataTables
	player      *save.PlayerData
	scene       *CombatSceneState
	buffBuilder *RoleBuffBuilder
	formation   *FormationService
	buffs       *BuffManager
	hooks       GameHooks
}

func NewRoleService(t *data.DataTables, p *save.PlayerData, s *CombatSceneState, bb *RoleBuffBuilder, f *FormationService, b *BuffManager, hooks GameHooks) *RoleService {
	if hooks == nil {
		hooks = NullHooks{}
	}
	return &RoleService{t, p, s, bb, f, b, hooks}
}

// rolesByID are the player's roles sorted by id.
func rolesByID(p *save.PlayerData) []*save.RoleSave {
	rs := p.Roles.Values()
	slices.SortStableFunc(rs, func(a, b *save.RoleSave) int { return int(a.RoleID) - int(b.RoleID) })
	return rs
}

// SeedNewAccountRoles makes a role of every RoleInfo row when player/<id>/role/ was missing
// (specs/roles.md §1).
func (r *RoleService) SeedNewAccountRoles(nowSec uint32) {
	if r.player.RoleDirExistedOnLoad && r.player.Roles.Len() > 0 {
		return
	}
	for _, ri := range r.tables.RoleInfo {
		if r.player.Roles.Has(ri.Id) {
			continue
		}
		role := r.CreateInitialRole(ri, nowSec)
		r.player.Roles.Set(ri.Id, role)
		save.SaveRole(r.player, role)
	}
}

func (r *RoleService) CreateInitialRole(ri *data.RoleInfo, nowSec uint32) *save.RoleSave {
	level := ri.MaxLevel
	if level <= 0 {
		level = 1
	}
	const breakthrough = 6
	role := save.NewRole(ri.Id)
	seen := map[int32]bool{}
	for _, st := range r.tables.SkillTree {
		if st.NodeGroup != ri.SkillTreeGroupId {
			continue
		}
		active := len(st.Condition) == 0 && st.UnLockCondition == 0
		role.SkillNodeState = append(role.SkillNodeState, &save.SkillNodeSave{NodeID: st.Id, Active: active, SkillID: st.SkillId})
		if active && st.SkillId != 0 && !seen[st.SkillId] {
			seen[st.SkillId] = true
			role.Skills = append(role.Skills, save.IntPair{Key: st.SkillId, Value: 1})
		}
	}
	role.Level, role.Exp, role.Breakthrough = level, 0, breakthrough
	role.SkillBranch = ri.DefaultSkillBranchId
	role.RoleSkinID = ri.SkinId
	role.Favor = 5
	role.CreateTime = nowSec
	role.BaseProp = BuildSavedBaseProp(r.tables, ri, level, breakthrough)
	return role
}

// RepairOnLogin runs the login repairs before the role pushes (specs/roles.md §1): a base_prop
// of 143, and an equipped weapon for every role.
func (r *RoleService) RepairOnLogin(nowMs int64) {
	for _, role := range r.player.Roles.Values() {
		ri, ok := r.tables.RoleInfoById[role.RoleID]
		if !ok {
			continue
		}
		if len(role.BaseProp) != int(AttrCount) {
			role.BaseProp = BuildSavedBaseProp(r.tables, ri, role.Level, role.Breakthrough)
			r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
		}
		r.EnsureRoleHasWeapon(role, ri)
	}
}

func (r *RoleService) EnsureRoleHasWeapon(role *save.RoleSave, ri *data.RoleInfo) {
	if role.Weapon > 0 && r.player.Weapons.Has(role.Weapon) {
		return
	}
	if ri.InitWeaponItemId <= 0 {
		return
	}
	var chosen *save.WeaponSave
	for _, w := range r.player.Weapons.All() {
		if w.RoleID == nil && w.ID == ri.InitWeaponItemId {
			chosen = w
			break
		}
	}
	if chosen == nil {
		uid := r.player.SpecialItemIDs.Next()
		quality := int32(1)
		if wc, ok := r.tables.WeaponConfByItemId[ri.InitWeaponItemId]; ok {
			quality = wc.QualityId
		}
		chosen = save.NewWeapon(uid)
		chosen.ID = ri.InitWeaponItemId
		if quality < 5 {
			chosen.FuncValue = 1
		}
		r.player.Weapons.Set(uid, chosen)
	}
	id := role.RoleID
	chosen.RoleID = &id
	role.Weapon = chosen.UniqueID
	save.SaveWeapon(r.player, chosen)
	save.SaveRole(r.player, role)
}

func (r *RoleService) ResolveSelectedMainRoleID() int32 {
	sex := r.player.BasicInfo.Attributes.Sex
	sel := r.player.BasicInfo.SelectedMainRoleID
	if sel != 0 && r.player.Roles.Has(sel) {
		if m, ok := r.tables.MainRoleConfigById[sel]; ok && m.Gender == sex {
			return sel
		}
	}
	for _, m := range r.tables.MainRoleConfig {
		if m.Gender == sex && r.player.Roles.Has(m.Id) {
			return m.Id
		}
	}
	for _, m := range r.tables.MainRoleConfig {
		if r.player.Roles.Has(m.Id) {
			return m.Id
		}
	}
	return sel
}

// BuildRoleInfoProto is a role with its live attributes (specs/roles.md §2.3).
func (r *RoleService) BuildRoleInfoProto(role *save.RoleSave) *protocol.RoleInfo {
	snap := ComputeLive(r.tables, r.player, role, r.buffBuilder.RoleAutoBuffTags(role.RoleID))
	p := &protocol.RoleInfo{
		RoleId: role.RoleID, Name: "", Level: role.Level, Exp: role.Exp, Breakthrough: role.Breakthrough,
		Star: role.Star, Favor: role.Favor, CurModel: role.CurModel, CreateTime: role.CreateTime,
		ResonantChainGroupIndex: role.ResonantChainGroupIndex, SkinId: role.RoleSkinID, EnableSelfBgm: role.EnableSelfBgm,
	}
	for _, s := range role.Skills {
		p.Skills = append(p.Skills, &protocol.ArrayIntInt{Key: s.Key, Value: s.Value})
	}
	for _, s := range role.Phantom {
		p.Phantom = append(p.Phantom, &protocol.ArrayIntInt{Key: s.Key, Value: s.Value})
	}
	for _, x := range role.Reson {
		p.Reson = append(p.Reson, &protocol.ResonInfo{ResonId: x.ID, IsOpen: x.Open, Increase: x.Increase})
	}
	p.Models = append(p.Models, role.Models...)
	for i := int32(0); i < AttrCount; i++ {
		p.BaseProp = append(p.BaseProp, &protocol.ArrayIntInt{Key: i, Value: snap.BaseProps[i]})
		p.AddProp = append(p.AddProp, &protocol.ArrayIntInt{Key: i, Value: snap.AddProps[i]})
	}
	for _, n := range role.SkillNodeState {
		p.SkillNodeState = append(p.SkillNodeState, &protocol.ArraySkillNode{SkillNodeId: n.NodeID, IsActive: n.Active, SkillId: n.SkillID})
	}
	return p
}

// RefreshLiveRoleAttributes recomputes a role's attributes, updates its spawned entity (Life
// reset to the new maximum, roles.md §3.2 step 3) with an AttributeChangedNotify, and pushes
// PbGetRoleListNotify.
func (r *RoleService) RefreshLiveRoleAttributes(roleID int32, sink PushSink) {
	role, ok := r.player.Roles.Get(roleID)
	if !ok {
		return
	}
	snap := ComputeLive(r.tables, r.player, role, r.buffBuilder.RoleAutoBuffTags(roleID))
	if live := r.formation.FindLiveRoleEntity(roleID); live != nil {
		live.Attributes.LoadFrom(snap.BaseProps, snap.AddProps)
		live.FsmDirty = true
		PushCombatNotifies(sink, []*protocol.CombatNotifyData{live.Attributes.BuildAllAttributesChangedNotify(live.NetID)})
	}
	sink.Push(&protocol.PbGetRoleListNotify{RoleList: []*protocol.RoleInfo{r.BuildRoleInfoProto(role)}})
}

// BuildBasicInfoNotify (specs/enter-world.md §3 row 1).
func (r *RoleService) BuildBasicInfoNotify() *protocol.BasicInfoNotify {
	bi := &r.player.BasicInfo
	a := &bi.Attributes
	n := &protocol.BasicInfoNotify{Id: r.player.PlayerID, CurCardId: bi.CurCardID, Birthday: bi.Birthday,
		DisplayBirthDay: bi.DisplayBirthday, LastModifyNameTime: bi.LastModifyNameTime, ModifyName: ""}
	addInt := func(k protocol.PlayerAttrKey, v int32) {
		n.Attributes = append(n.Attributes, &protocol.PlayerAttr{Key: k, ValueType: protocol.PlayerAttrType_PlayerAttrType_Int32, Value: &protocol.PlayerAttr_Int32Value{Int32Value: v}})
	}
	addStr := func(k protocol.PlayerAttrKey, v string) {
		n.Attributes = append(n.Attributes, &protocol.PlayerAttr{Key: k, ValueType: protocol.PlayerAttrType_PlayerAttrType_String, Value: &protocol.PlayerAttr_StringValue{StringValue: v}})
	}
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_Level, a.Level)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_Exp, a.Exp)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_Coin, a.Coin)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_RareCoin, a.RareCoin)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_HeadPhoto, a.HeadPhoto)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_HeadFrame, a.HeadFrame)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_AreaId, a.AreaID)
	addStr(protocol.PlayerAttrKey_PlayerAttrKey_Name, a.Name)
	addStr(protocol.PlayerAttrKey_PlayerAttrKey_Sign, a.Sign)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_Sex, a.Sex)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_OriginWorldLevel, a.OriginWorldLevel)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_CurWorldLevel, a.CurWorldLevel)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_WorldLevelTimeStamp, a.WorldLevelTimeStamp)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_CashCoin, a.CashCoin)
	addInt(protocol.PlayerAttrKey_PlayerAttrKey_WorldPermission, a.WorldPermission)
	for _, id := range bi.RoleShowList {
		if role, ok := r.player.Roles.Get(id); ok {
			n.RoleShowList = append(n.RoleShowList, &protocol.RoleShowEntry{RoleId: id, Level: role.Level})
		}
	}
	for _, c := range r.tables.BackgroundCard {
		n.CardUnlockList = append(n.CardUnlockList, &protocol.CardShowEntry{CardId: c.Id, IsRead: true})
	}
	return n
}

func (r *RoleService) BuildLoginRoleListNotify() *protocol.PbGetRoleListNotify {
	sel := r.ResolveSelectedMainRoleID()
	n := &protocol.PbGetRoleListNotify{}
	for _, role := range rolesByID(r.player) {
		// Roles 1000 … 1999 only, and of the Rover variants only the one selected.
		if role.RoleID < 1000 || role.RoleID > 1999 {
			continue
		}
		if _, isMain := r.tables.MainRoleConfigById[role.RoleID]; isMain && role.RoleID != sel {
			continue
		}
		n.RoleList = append(n.RoleList, r.BuildRoleInfoProto(role))
	}
	return n
}

func (r *RoleService) BuildRoleConfigInfoNotify() *protocol.RoleConfigInfoNotify {
	n := &protocol.RoleConfigInfoNotify{}
	for _, role := range rolesByID(r.player) {
		n.RoleConfigs = append(n.RoleConfigs, &protocol.RoleConfigInfo{RoleId: role.RoleID, SkillBranch: role.SkillBranch})
	}
	return n
}

func (r *RoleService) BuildRoleChangeUnlockNotify() *protocol.RoleChangeUnlockNotify {
	sex := r.player.BasicInfo.Attributes.Sex
	n := &protocol.RoleChangeUnlockNotify{CanChangeSexTime: 0}
	for _, m := range r.tables.MainRoleConfig {
		if m.Gender == sex {
			n.UnlockRoleIds = append(n.UnlockRoleIds, m.Id)
		}
	}
	return n
}

func unlocked(id int32) *protocol.FavorItem {
	return &protocol.FavorItem{Id: id, Status: protocol.FavorItemStatus_FavorItemStatus_ItemUnLocked}
}

func (r *RoleService) BuildRoleFavorList() []*protocol.RoleFavor {
	words, stories, goods := map[int32][]int32{}, map[int32][]int32{}, map[int32][]int32{}
	for _, w := range r.tables.FavorWord {
		words[w.RoleId] = append(words[w.RoleId], w.Id)
	}
	for _, s := range r.tables.FavorStory {
		stories[s.RoleId] = append(stories[s.RoleId], s.Id)
	}
	for _, g := range r.tables.FavorGoods {
		goods[g.RoleId] = append(goods[g.RoleId], g.Id)
	}
	list := []*protocol.RoleFavor{}
	for _, role := range rolesByID(r.player) {
		f := &protocol.RoleFavor{RoleId: role.RoleID, Level: 5, Exp: 16800}
		for _, id := range words[role.RoleID] {
			f.WordIds = append(f.WordIds, unlocked(id))
		}
		for _, id := range stories[role.RoleID] {
			f.StoryIds = append(f.StoryIds, unlocked(id))
		}
		for _, id := range goods[role.RoleID] {
			f.GoodsIds = append(f.GoodsIds, unlocked(id))
		}
		list = append(list, f)
	}
	return list
}

func (r *RoleService) BuildRoleMotionListNotify() *protocol.RoleMotionListNotify {
	motions := map[int32][]int32{}
	for _, m := range r.tables.Motion {
		motions[m.RoleId] = append(motions[m.RoleId], m.Id)
	}
	n := &protocol.RoleMotionListNotify{}
	for _, role := range rolesByID(r.player) {
		ids := motions[role.RoleID]
		if len(ids) == 0 {
			continue
		}
		rm := &protocol.RoleMotion{RoleId: role.RoleID}
		for _, id := range ids {
			rm.MotionIds = append(rm.MotionIds, unlocked(id))
		}
		n.MotionList = append(n.MotionList, rm)
	}
	return n
}

func nonZero(ids []int32) []int32 {
	var out []int32
	for _, id := range ids {
		if id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// PushCosmeticAndRoleLoginNotifies are the cosmetic and role pushes of the login sequence.
func (r *RoleService) PushCosmeticAndRoleLoginNotifies(sink PushSink) {
	cos := &r.player.CosmeticInfo
	// WeaponSkinAddNotify: the phantom skins, IsLogin.
	sink.Push(&protocol.WeaponSkinAddNotify{IsLogin: true, SkinIds: nonZero(cos.PhantomSkins)})
	sink.Push(&protocol.FlyEquipAddNotify{FlySkins: nonZero(cos.FlySkins)})

	// RoleFlyEquipNotify (specs/systems.md §2.1 row 6).
	roleFly := &protocol.RoleFlyEquipNotify{}
	bySkin := map[int32]*protocol.FlyEquipData{}
	entry := func(skin int32) *protocol.FlyEquipData {
		e, ok := bySkin[skin]
		if !ok {
			e = &protocol.FlyEquipData{SkinId: skin}
			bySkin[skin] = e
			roleFly.DataList = append(roleFly.DataList, e)
		}
		return e
	}
	for _, s := range cos.FlySkins {
		if s != 0 {
			entry(s)
		}
	}
	addFly := func(skin, roleID int32) {
		if skin == 0 {
			return
		}
		e := entry(skin)
		if !slices.Contains(e.RoleIds, roleID) {
			e.RoleIds = append(e.RoleIds, roleID)
		}
	}
	for _, role := range rolesByID(r.player) {
		addFly(role.SoarSkinID, role.RoleID)
		addFly(role.ParaglidingSkinID, role.RoleID)
	}
	sink.Push(roleFly)

	// OrnamentInfoNotify.
	info := &protocol.OrnamentInfo{UnlockOrnamentIds: nonZero(cos.Ornaments), RedPointOrnamentIds: nonZero(cos.ViewedOrnaments)}
	var byRoleSkin omap.Map[int32, []int32]
	for _, role := range rolesByID(r.player) {
		for _, o := range role.Ornaments {
			if o.OrnamentID == 0 {
				continue
			}
			list := byRoleSkin.At(o.RoleSkinID)
			if !slices.Contains(list, o.OrnamentID) {
				byRoleSkin.Set(o.RoleSkinID, append(list, o.OrnamentID))
			}
		}
	}
	for skin, ids := range byRoleSkin.All() {
		info.OrnamentDressInfos = append(info.OrnamentDressInfos, &protocol.OrnamentDressInfo{RoleSkinId: skin, DressOrnamentIds: ids})
	}
	sink.Push(&protocol.OrnamentInfoNotify{OrnamentInfo: info})

	// StorageInfoNotify: the default red-dot sets (specs/roles.md §2.5, systems.md §2 row 8).
	storage := &protocol.StorageInfoNotify{}
	addSet := func(id protocol.EClientStorageSystemIdType, values []int32) {
		storage.Infos = append(storage.Infos, &protocol.ClientStorageInfo{SystemId: int32(id),
			Data: &protocol.ClientStorageInfo_SetData{SetData: &protocol.ClientStorageSetData{Data: nonZero(values)}}})
	}
	addSet(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_RoleSkinRedDot, nil)
	addSet(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_FlySkinRedDot, nil)
	addSet(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_WeaponSkinRedDot, nil)
	addSet(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_CalabashSkinRedDot, nil)
	addSet(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_Ornament, cos.ViewedOrnaments)
	addSet(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_GetOrnament, nil)
	sink.Push(storage)

	sink.Push(r.BuildRoleChangeUnlockNotify())
	sink.Push(r.BuildLoginRoleListNotify())
	sink.Push(&protocol.RoleFavorListNotify{FavorList: r.BuildRoleFavorList()})
	sink.Push(r.BuildRoleMotionListNotify())
	sink.Push(r.BuildRoleConfigInfoNotify())
}

// GrantRole grants a role, or turns a duplicate into RoleInfo.SpilloverItem (R-38, roles.md §6.13).
func (r *RoleService) GrantRole(roleID int32, nowSec uint32, sink PushSink) {
	ri, ok := r.tables.RoleInfoById[roleID]
	if !ok {
		return
	}
	if r.player.Roles.Has(roleID) {
		var updates []save.ItemCount
		add := &protocol.NormalItemAddNotify{}
		for _, p := range ri.SpilloverItem.Pairs {
			if p.Value <= 0 {
				continue
			}
			if m, _ := MoneyAttr(&r.player.BasicInfo.Attributes, p.Key); m != nil {
				SetMoney(r.player, p.Key, int64(*m)+int64(p.Value), sink)
				continue
			}
			updates = append(updates, save.ItemCount{ID: p.Key, Count: r.player.NormalInventory.CountOf(p.Key) + p.Value})
			if _, ok := r.tables.ItemInfoById[p.Key]; ok {
				add.NormalItemList = append(add.NormalItemList, &protocol.NormalItem{Id: p.Key, Count: p.Value})
			}
		}
		if len(updates) > 0 {
			r.player.NormalInventory.SetBatch(updates)
		}
		if len(add.NormalItemList) > 0 {
			sink.Push(add)
		}
		return
	}
	role := r.CreateInitialRole(ri, nowSec)
	r.player.Roles.Set(roleID, role)
	r.EnsureRoleHasWeapon(role, ri)
	save.SaveRole(r.player, role)
	sink.Push(&protocol.PbGetRoleListNotify{RoleList: []*protocol.RoleInfo{r.BuildRoleInfoProto(role)}})
}

// ----- requests (specs/roles.md §6) -----

func (r *RoleService) HandleRoleFavorList(*protocol.RoleFavorListRequest) *protocol.RoleFavorListResponse {
	return &protocol.RoleFavorListResponse{ErrCode: codeSuccess, FavorList: r.BuildRoleFavorList()}
}

func (r *RoleService) HandleRoleProp(req *protocol.RolePropRequest) *protocol.RolePropResponse {
	resp := &protocol.RolePropResponse{Error: codeSuccess}
	for _, id := range req.RoleIds {
		role, ok := r.player.Roles.Get(id)
		if !ok {
			continue
		}
		info := &protocol.RolePropInfo{RoleId: id}
		for i, v := range role.BaseProp {
			info.BaseProp = append(info.BaseProp, &protocol.ArrayIntInt{Key: int32(i), Value: v})
		}
		for i, v := range role.AddProp {
			info.AddProp = append(info.AddProp, &protocol.ArrayIntInt{Key: int32(i), Value: v})
		}
		resp.RoleProps = append(resp.RoleProps, info)
	}
	return resp
}

func skillIndex(role *save.RoleSave, skillID int32) int {
	return slices.IndexFunc(role.Skills, func(s save.IntPair) bool { return s.Key == skillID })
}

func setSkill(role *save.RoleSave, idx int, skillID, level int32) {
	if idx >= 0 {
		role.Skills[idx] = save.IntPair{Key: skillID, Value: level}
	} else {
		role.Skills = append(role.Skills, save.IntPair{Key: skillID, Value: level})
	}
}

// skillLevelCosts is what levels from+1 … to of skill cfg take: SkillLevel rows of the skill's
// SkillLevelGroupId, whose field SkillId is the level (client
// RoleSkillConfig.GetSkillLevelConfigByGroupIdAndLevel; specs/roles.md §6.3).
func (r *RoleService) skillLevelCosts(cfg *data.Skill, from, to int32) []Cost {
	var costs []Cost
	if cfg.SkillLevelGroupId == 0 || to <= from {
		return costs
	}
	for lv := from + 1; lv <= to; lv++ {
		for _, row := range r.tables.SkillLevel {
			if row.SkillLevelGroupId == cfg.SkillLevelGroupId && row.SkillId == lv {
				costs = AddCosts(costs, row.Consume, 1)
				break
			}
		}
	}
	return costs
}

// skillNodeCosts is what activating a skill-tree node takes (client
// RoleSkillConfig.GetRoleSkillTreeConsume at level 1): SkillTree.Consume when the node has no
// skill, else the skill's level-1 SkillLevel row (specs/roles.md §6.4).
func (r *RoleService) skillNodeCosts(node *data.SkillTree) []Cost {
	if node.SkillId == 0 {
		return AddCosts(nil, node.Consume, 1)
	}
	cfg, ok := r.tables.SkillById[node.SkillId]
	if !ok {
		return nil
	}
	return r.skillLevelCosts(cfg, 0, 1)
}

func (r *RoleService) HandleUpLevelSkill(req *protocol.PbUpLevelSkillRequest, nowMs int64, sink PushSink) *protocol.PbUpLevelSkillResponse {
	role, ok := r.player.Roles.Get(req.RoleId)
	if !ok {
		return &protocol.PbUpLevelSkillResponse{Code: codeParamError}
	}
	cfg, ok := r.tables.SkillById[req.SkillId]
	if !ok {
		return &protocol.PbUpLevelSkillResponse{Code: codeSkillParam}
	}
	idx := skillIndex(role, req.SkillId)
	cur := int32(1)
	if idx >= 0 {
		cur = role.Skills[idx].Value
	}
	maxLv := cfg.MaxSkillLevel
	if maxLv <= 0 {
		maxLv = 10
	}
	next := min(cur+1, maxLv)
	costs := r.skillLevelCosts(cfg, cur, next)
	if !CanPay(r.player, costs) {
		return &protocol.PbUpLevelSkillResponse{Code: codeItemNotEnough}
	}
	Pay(r.tables, r.player, costs, sink)
	setSkill(role, idx, req.SkillId, next)
	r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
	return &protocol.PbUpLevelSkillResponse{Code: codeSuccess, RoleId: role.RoleID, SkillInfo: &protocol.ArrayIntInt{Key: req.SkillId, Value: next}}
}

func (r *RoleService) skillNodeNotify(role *save.RoleSave) *protocol.RoleSkillNodeNotify {
	n := &protocol.RoleSkillNodeNotify{RoleId: role.RoleID}
	for _, s := range role.SkillNodeState {
		n.SkillNodeState = append(n.SkillNodeState, &protocol.ArraySkillNode{SkillNodeId: s.NodeID, IsActive: s.Active, SkillId: s.SkillID})
	}
	return n
}

func (r *RoleService) HandleActivateSkill(req *protocol.RoleActivateSkillRequest, nowMs int64, sink PushSink) *protocol.RoleActivateSkillResponse {
	role, ok := r.player.Roles.Get(req.RoleId)
	if !ok {
		return &protocol.RoleActivateSkillResponse{Code: codeParamError}
	}
	node, ok := r.tables.SkillTreeById[req.SkillNodeId]
	if !ok {
		return &protocol.RoleActivateSkillResponse{Code: protocol.ErrorCode_ErrorCode_ErrRolSkillNodeTypeUlock}
	}
	i := slices.IndexFunc(role.SkillNodeState, func(n *save.SkillNodeSave) bool { return n.NodeID == req.SkillNodeId })
	if i < 0 || !role.SkillNodeState[i].Active {
		costs := r.skillNodeCosts(node)
		if !CanPay(r.player, costs) {
			return &protocol.RoleActivateSkillResponse{Code: codeItemNotEnough}
		}
		Pay(r.tables, r.player, costs, sink)
	}
	if i >= 0 {
		role.SkillNodeState[i].Active = true
		role.SkillNodeState[i].SkillID = node.SkillId
	} else {
		role.SkillNodeState = append(role.SkillNodeState, &save.SkillNodeSave{NodeID: req.SkillNodeId, Active: true, SkillID: node.SkillId})
	}
	if node.SkillId != 0 && skillIndex(role, node.SkillId) < 0 {
		role.Skills = append(role.Skills, save.IntPair{Key: node.SkillId, Value: 1})
	}
	r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
	sink.Push(r.skillNodeNotify(role))
	r.RefreshLiveRoleAttributes(role.RoleID, sink)
	return &protocol.RoleActivateSkillResponse{Code: codeSuccess, RoleId: role.RoleID, SkillInfo: &protocol.ArrayIntInt{Key: node.SkillId, Value: 1}}
}

func (r *RoleService) HandleSkillLevelUpView(req *protocol.RoleSkillLevelUpViewRequest) *protocol.RoleSkillLevelUpViewResponse {
	if _, ok := r.tables.SkillById[req.SkillId]; !ok {
		return &protocol.RoleSkillLevelUpViewResponse{Code: codeSkillParam}
	}
	return &protocol.RoleSkillLevelUpViewResponse{Code: codeSuccess}
}

func (r *RoleService) HandleSkillView(req *protocol.RoleSkillViewRequest) *protocol.RoleSkillViewResponse {
	if _, ok := r.tables.SkillById[req.SkillId]; !ok {
		return &protocol.RoleSkillViewResponse{Code: codeSkillParam}
	}
	return &protocol.RoleSkillViewResponse{Code: codeSuccess, IsConditionFinish: true}
}

func (r *RoleService) HandleSkillQuickLevelUp(req *protocol.RoleSkillQuickLevelUpRequest, nowMs int64, sink PushSink) *protocol.RoleSkillQuickLevelUpResponse {
	role, ok := r.player.Roles.Get(req.RoleId)
	if !ok {
		return &protocol.RoleSkillQuickLevelUpResponse{ErrorCode: codeParamError}
	}
	cfg, ok := r.tables.SkillById[req.SkillId]
	if !ok {
		return &protocol.RoleSkillQuickLevelUpResponse{ErrorCode: codeSkillParam}
	}
	idx := skillIndex(role, req.SkillId)
	cur := int32(1)
	if idx >= 0 {
		cur = role.Skills[idx].Value
	}
	want := cur + 1
	if req.TargetLevel > cur {
		want = req.TargetLevel
	}
	maxLv := cfg.MaxSkillLevel
	if maxLv <= 0 {
		maxLv = 10
	}
	next := max(min(want, maxLv), cur)
	costs := r.skillLevelCosts(cfg, cur, next)
	if !CanPay(r.player, costs) {
		return &protocol.RoleSkillQuickLevelUpResponse{ErrorCode: codeItemNotEnough}
	}
	Pay(r.tables, r.player, costs, sink)
	setSkill(role, idx, req.SkillId, next)
	r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
	return &protocol.RoleSkillQuickLevelUpResponse{ErrorCode: codeSuccess, RoleInfo: r.BuildRoleInfoProto(role)}
}

// HandleResonantChainUnlock takes the ActivateConsume items (specs/roles.md §6.7).
func (r *RoleService) HandleResonantChainUnlock(req *protocol.ResonantChainUnlockRequest, nowMs int64, sink PushSink) *protocol.ResonantChainUnlockResponse {
	role, ok1 := r.player.Roles.Get(req.RoleId)
	ri, ok2 := r.tables.RoleInfoById[req.RoleId]
	if !ok1 || !ok2 {
		return &protocol.ResonantChainUnlockResponse{ErrCode: codeParamError}
	}
	var group []*data.ResonantChain
	for _, c := range r.tables.ResonantChain {
		if c.GroupId == ri.ResonantChainGroupId {
			group = append(group, c)
		}
	}
	if len(group) == 0 {
		return &protocol.ResonantChainUnlockResponse{ErrCode: codeParamError}
	}
	maxIdx := group[0].GroupIndex
	for _, c := range group {
		maxIdx = max(maxIdx, c.GroupIndex)
	}
	if role.ResonantChainGroupIndex >= maxIdx {
		return &protocol.ResonantChainUnlockResponse{ErrCode: codeSuccess, RoleId: role.RoleID, ResonantChainGroupIndex: role.ResonantChainGroupIndex}
	}
	nextIdx := role.ResonantChainGroupIndex + 1
	var next *data.ResonantChain
	for _, c := range group {
		if c.GroupIndex == nextIdx {
			next = c
			break
		}
	}
	if next == nil {
		return &protocol.ResonantChainUnlockResponse{ErrCode: codeParamError}
	}
	costs := AddCosts(nil, next.ActivateConsume, 1)
	if !CanPay(r.player, costs) {
		return &protocol.ResonantChainUnlockResponse{ErrCode: codeItemsMissing}
	}
	Pay(r.tables, r.player, costs, sink)
	role.ResonantChainGroupIndex = nextIdx
	r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
	if live := r.formation.FindLiveRoleEntity(role.RoleID); live != nil {
		ids := make([]int64, len(next.BuffIds))
		for i, x := range next.BuffIds {
			ids[i] = int64(x)
		}
		r.buffs.ApplyDirectPassiveBuffs(live, ids, sink)
	}
	r.RefreshLiveRoleAttributes(role.RoleID, sink)
	return &protocol.ResonantChainUnlockResponse{ErrCode: codeSuccess, RoleId: role.RoleID, ResonantChainGroupIndex: role.ResonantChainGroupIndex}
}

func (r *RoleService) HandleSkillBranchModify(req *protocol.RoleSkillBranchModifyRequest, nowMs int64, sink PushSink) *protocol.RoleSkillBranchModifyResponse {
	role, ok := r.player.Roles.Get(req.RoleId)
	if !ok {
		return &protocol.RoleSkillBranchModifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSkillBranchRoleValid}
	}
	newBranch, ok := r.tables.SkillBranchBuffByBranchId[req.SkillBranch]
	if !ok {
		return &protocol.RoleSkillBranchModifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSkillBranchNotValid}
	}
	prev, hasPrev := r.tables.SkillBranchBuffByBranchId[role.SkillBranch]
	if role.SkillBranch != 0 && !hasPrev {
		return &protocol.RoleSkillBranchModifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSkillBranchNotValid}
	}
	if live := r.formation.FindLiveRoleEntity(role.RoleID); live != nil {
		if role.SkillBranch != 0 && hasPrev {
			r.buffs.RemoveDirectPassiveBuffs(live, prev.Buffs, sink)
		}
		r.buffs.ApplyDirectPassiveBuffs(live, newBranch.Buffs, sink)
	}
	role.SkillBranch = req.SkillBranch
	r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
	sink.Push(r.formation.BuildUpdateFormationNotify())
	return &protocol.RoleSkillBranchModifyResponse{ErrorCode: codeSuccess}
}

// HandleRoleElementChange (specs/roles.md §6.9).
func (r *RoleService) HandleRoleElementChange(req *protocol.RoleElementChangeRequest, nowMs int64, sink PushSink) *protocol.RoleElementChangeResponse {
	fail := &protocol.RoleElementChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrRoleChangeElementFunc}
	sex := r.player.BasicInfo.Attributes.Sex
	var target int32
	for _, m := range r.tables.MainRoleConfig {
		if ri, ok := r.tables.RoleInfoById[m.Id]; m.Gender == sex && ok && ri.ElementId == req.ElementType {
			target = m.Id
			break
		}
	}
	if target == 0 || !r.player.Roles.Has(target) {
		return fail
	}
	source := r.findCurrentRoverRoleID()
	if source == 0 || !r.player.Roles.Has(source) {
		return fail
	}
	touched := r.transferRoverState(source, target)
	save.SaveBasicInfo(r.player)
	if touched {
		r.formation.RebuildActiveFormationEntities(nowMs, sink)
		sink.Push(r.formation.BuildUpdateFormationNotify())
	}
	tr := r.player.Roles.At(target)
	if tr.Weapon > 0 {
		sink.Push(&protocol.EquipTakeOnNotify{DataList: []*protocol.RoleLoadEquipData{{RoleID: target, Pos: protocol.EquipPos_EquipPos_Weapon, EquipIncID: tr.Weapon}}})
	}
	sink.Push(&protocol.RoleChangeNotify{SourceRoleId: source, RoleInfo: r.BuildRoleInfoProto(tr)})
	snap := ComputeLive(r.tables, r.player, tr, r.buffBuilder.RoleAutoBuffTags(target))
	sink.Push(&protocol.RolePhantomPropUpdateNotify{PropInfo: []*protocol.RolePhantomPropInfo{snap.ToRolePhantomPropInfo(target)}})
	sink.Push(&protocol.PhantomEquipInfoNotify{EquipInfo: []*protocol.RolePhantomEquipInfo{r.buildRolePhantomEquipInfo(target)}})
	return &protocol.RoleElementChangeResponse{ErrorCode: codeSuccess}
}

// HandleRoleSexChange (specs/roles.md §6.10, the R-76a hook, LogoutNotify).
func (r *RoleService) HandleRoleSexChange(req *protocol.RoleSexChangeRequest, nowMs int64, sink PushSink) *protocol.RoleSexChangeResponse {
	if req.Sex == r.player.BasicInfo.Attributes.Sex {
		return &protocol.RoleSexChangeResponse{ErrorCode: codeSuccess, Sex: req.Sex}
	}
	fail := &protocol.RoleSexChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrRoleSexFuncNotOpen}
	source := r.findCurrentRoverRoleID()
	sri, ok := r.tables.RoleInfoById[source]
	if source == 0 || !ok {
		return fail
	}
	var target int32
	for _, m := range r.tables.MainRoleConfig {
		if ri, ok := r.tables.RoleInfoById[m.Id]; m.Gender == req.Sex && ok && ri.ElementId == sri.ElementId {
			target = m.Id
			break
		}
	}
	if target == 0 || !r.player.Roles.Has(target) {
		return fail
	}
	touched := r.transferRoverState(source, target)
	r.player.BasicInfo.Attributes.Sex = req.Sex
	save.SaveBasicInfo(r.player)
	save.SyncAccountSex(r.player, req.Sex)
	r.hooks.OnGenderChanged(req.Sex == 1)
	if touched {
		r.formation.RebuildActiveFormationEntities(nowMs, sink)
		sink.Push(r.formation.BuildUpdateFormationNotify())
	}
	sink.Push(&protocol.RoleChangeNotify{SourceRoleId: source, RoleInfo: r.BuildRoleInfoProto(r.player.Roles.At(target))})
	sink.Push(r.BuildRoleChangeUnlockNotify())
	sink.Push(&protocol.LogoutNotify{Code: protocol.ErrorCode_ErrorCode_ErrSexChangeLogout})
	return &protocol.RoleSexChangeResponse{ErrorCode: codeSuccess, Sex: req.Sex}
}

func (r *RoleService) HandlePlayerVoiceLanguage(*protocol.PlayerVoiceLanguageRequest) *protocol.PlayerVoiceLanguageResponse {
	resp := &protocol.PlayerVoiceLanguageResponse{ErrorCode: codeSuccess}
	for _, role := range rolesByID(r.player) {
		if role.VoiceLanguage > 0 {
			resp.RoleVoices = append(resp.RoleVoices, &protocol.RoleVoice{RoleId: role.RoleID, VoiceId: role.VoiceLanguage - 1})
		}
	}
	return resp
}

func (r *RoleService) HandlePlayerRoleVoiceSet(req *protocol.PlayerRoleVoiceSetRequest, nowMs int64) *protocol.PlayerRoleVoiceSetResponse {
	if len(req.RoleVoices) == 0 {
		for _, role := range r.player.Roles.Values() {
			if role.VoiceLanguage != 0 {
				role.VoiceLanguage = 0
				r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
			}
		}
	} else {
		for _, s := range req.RoleVoices {
			role, ok := r.player.Roles.Get(s.RoleId)
			if !ok {
				continue
			}
			if stored := s.VoiceLanguage + 1; role.VoiceLanguage != stored {
				role.VoiceLanguage = stored
				r.player.DirtySaves.MarkRole(role.RoleID, nowMs)
			}
		}
	}
	return &protocol.PlayerRoleVoiceSetResponse{ErrorCode: codeSuccess}
}

func (r *RoleService) findCurrentRoverRoleID() int32 {
	for _, s := range r.formation.ActiveFormation().FilledRoles() {
		if _, ok := r.tables.MainRoleConfigById[s.RoleID]; ok {
			return s.RoleID
		}
	}
	return r.ResolveSelectedMainRoleID()
}

func (r *RoleService) transferRoverState(source, target int32) bool {
	if source == target {
		return false
	}
	src, dst := r.player.Roles.At(source), r.player.Roles.At(target)
	dst.Level, dst.Exp, dst.Breakthrough = src.Level, src.Exp, src.Breakthrough

	// Swap the equipped weapons.
	srcW, dstW := src.Weapon, dst.Weapon
	if sw, ok := r.player.Weapons.Get(srcW); srcW > 0 && ok {
		t := target
		sw.RoleID = &t
		dst.Weapon = srcW
		save.SaveWeapon(r.player, sw)
		if dw, ok := r.player.Weapons.Get(dstW); dstW > 0 && dstW != srcW && ok {
			s := source
			dw.RoleID = &s
			src.Weapon = dstW
			save.SaveWeapon(r.player, dw)
		} else {
			src.Weapon = 0
		}
	}
	// Move the source's echoes to the target (the target's own come off first).
	for _, e := range r.player.Echoes.Values() {
		if e.RoleID != nil && *e.RoleID == target {
			e.RoleID = nil
			e.EquippedPos = -1
			save.SaveEcho(r.player, e)
		}
	}
	for _, e := range r.player.Echoes.Values() {
		if e.RoleID != nil && *e.RoleID == source {
			t := target
			e.RoleID = &t
			save.SaveEcho(r.player, e)
		}
	}
	for i, id := range r.player.BasicInfo.RoleShowList {
		if id == source {
			r.player.BasicInfo.RoleShowList[i] = target
		}
	}
	touched := false
	fi := r.player.FormationInfo
	for f := 0; f < save.FormationSlotCount; f++ {
		form := fi.Formations[f]
		changed := false
		if form.CurRole == source {
			form.CurRole = target
			changed = true
		}
		for _, s := range form.Roles {
			if s != nil && s.RoleID == source {
				s.RoleID = target
				changed = true
			}
		}
		if changed && int32(f) == fi.CurFormation {
			touched = true
		}
	}
	r.player.BasicInfo.SelectedMainRoleID = target
	save.SaveRole(r.player, src)
	save.SaveRole(r.player, dst)
	save.SaveFormationInfo(r.player)
	return touched
}

func (r *RoleService) buildRolePhantomEquipInfo(roleID int32) *protocol.RolePhantomEquipInfo {
	info := &protocol.RolePhantomEquipInfo{RoleId: roleID}
	for _, e := range r.player.EquippedEchoes(roleID) {
		var id int32
		if e != nil {
			id = e.UniqueID
		}
		info.PhantomItemIncrId = append(info.PhantomItemIncrId, id)
	}
	return info
}
