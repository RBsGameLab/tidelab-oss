// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

// Client storage (R-113), cosmetics and skins, the camera, explore tools, echo projector
// summons, guides, the illustrated codex, short messages, the battle pass, the profile,
// tutorials, settings and minimal UI replies (specs/systems.md §2, §5, §6, §7.2–7.4, §8.4, §9,
// §11). Ported from our C# Game/ClientStorage.cs, CosmeticsAndCamera.cs, ExploreAndSummon.cs,
// ProfileAndSocial.cs and PlayerSession.ProfileAndCosmetics.cs.

// ----- client storage (R-113, §11.1) -----

// PlayerClientStorage is the client's server-side storage, saved in player/<id>/client_storage
// as lines "<system id> <hex of encoded ClientStorageInfo>".
type PlayerClientStorage struct {
	files   *save.PlayerFiles
	order   []int32
	entries map[int32][]byte
	loaded  bool
}

func NewPlayerClientStorage(files *save.PlayerFiles) *PlayerClientStorage {
	return &PlayerClientStorage{files: files, entries: map[int32][]byte{}}
}

func (c *PlayerClientStorage) path() string { return c.files.PathOf("client_storage") }

func (c *PlayerClientStorage) ensureLoaded() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.order, c.entries = nil, map[int32][]byte{}
	if b, err := os.ReadFile(c.path()); err == nil {
		c.Parse(strings.TrimPrefix(string(b), utf8BOM))
	}
}

func (c *PlayerClientStorage) Parse(text string) {
	for _, raw := range strings.Split(text, "\n") {
		line := trimNet(raw)
		if line == "" {
			continue
		}
		parts := splitSpaces(line)
		if len(parts) < 2 {
			continue
		}
		id, ok := parseSigned(strings.TrimFunc(parts[0], isNetSpace), 32)
		if !ok || len(parts[1])%2 != 0 {
			continue
		}
		b, err := hex.DecodeString(parts[1])
		if err != nil {
			continue
		}
		c.put(int32(id), b)
	}
}

func (c *PlayerClientStorage) put(id int32, b []byte) {
	if c.entries == nil {
		c.entries = map[int32][]byte{}
	}
	if _, ok := c.entries[id]; !ok {
		c.order = append(c.order, id)
	}
	c.entries[id] = b
}

func (c *PlayerClientStorage) Serialize() string {
	var sb strings.Builder
	for _, id := range c.order {
		if b, ok := c.entries[id]; ok {
			fmt.Fprintf(&sb, "%d %s\n", id, hex.EncodeToString(b))
		}
	}
	return sb.String()
}

func (c *PlayerClientStorage) Store(infos []*protocol.ClientStorageInfo) {
	for _, info := range infos {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(info)
		if err != nil {
			panic(err)
		}
		c.put(info.SystemId, b)
	}
}

// Update is StorageInfoUpdateRequest: each info replaces the stored container of its system.
func (c *PlayerClientStorage) Update(infos []*protocol.ClientStorageInfo) {
	if len(infos) == 0 {
		return
	}
	c.ensureLoaded()
	c.Store(infos)
	save.WriteAtomic(c.path(), c.Serialize())
}

// Merge merges the stored containers into list, replacing matching SystemIds and appending new ones.
func (c *PlayerClientStorage) Merge(list []*protocol.ClientStorageInfo) []*protocol.ClientStorageInfo {
	c.ensureLoaded()
	for _, id := range c.order {
		b, ok := c.entries[id]
		if !ok {
			continue
		}
		info := &protocol.ClientStorageInfo{}
		if err := proto.Unmarshal(b, info); err != nil {
			common.Warn("client_storage", fmt.Sprintf("decoding system %d failed: %v", id, err))
			continue
		}
		found := false
		for i, x := range list {
			if x.SystemId == id {
				list[i] = info
				found = true
				break
			}
		}
		if !found {
			list = append(list, info)
		}
	}
	return list
}

// BuildNotify is StorageInfoNotify: the 6 default red-dot sets, merged with the saved entries
// (specs/systems.md §2 row 8).
func (c *PlayerClientStorage) BuildNotify(viewedOrnaments []int32) *protocol.StorageInfoNotify {
	n := &protocol.StorageInfoNotify{}
	add := func(sys protocol.EClientStorageSystemIdType, values []int32) {
		set := &protocol.ClientStorageSetData{}
		for _, v := range values {
			if v != 0 {
				set.Data = append(set.Data, v)
			}
		}
		n.Infos = append(n.Infos, &protocol.ClientStorageInfo{SystemId: int32(sys), Data: &protocol.ClientStorageInfo_SetData{SetData: set}})
	}
	add(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_RoleSkinRedDot, nil)
	add(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_FlySkinRedDot, nil)
	add(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_WeaponSkinRedDot, nil)
	add(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_CalabashSkinRedDot, nil)
	add(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_Ornament, viewedOrnaments)
	add(protocol.EClientStorageSystemIdType_EClientStorageSystemIdType_GetOrnament, nil)
	n.Infos = c.Merge(n.Infos)
	return n
}

func (c *PlayerClientStorage) HandleUpdate(r *protocol.StorageInfoUpdateRequest) *protocol.StorageInfoUpdateResponse {
	c.Update(r.Infos)
	return &protocol.StorageInfoUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- cosmetics and the camera (§9) -----

func IsWeaponSkinCompatible(t *data.DataTables, roleID, skinID int32) bool {
	if skinID == 0 {
		return true
	}
	ws, ok := t.WeaponSkinById[skinID]
	if !ok || !ws.IsShow || ws.HideInSkinView {
		return false
	}
	ri, ok := t.RoleInfoById[roleID]
	return ok && ws.WeaponSkinType == ri.WeaponType
}

func OrnamentBuffs(t *data.DataTables, ornamentIDs []int32) []int64 {
	seen := map[int64]bool{}
	var list []int64
	add := func(raw float64) {
		if b := int64(raw); b != 0 && !seen[b] {
			seen[b] = true
			list = append(list, b)
		}
	}
	for _, id := range ornamentIDs {
		o, ok := t.OrnamentById[id]
		if id == 0 || !ok {
			continue
		}
		for _, b := range o.OrnamentBuff {
			add(b)
		}
		for _, b := range o.OrnamentUiBuff {
			add(b)
		}
	}
	return list
}

func skinOrnaments(role *save.RoleSave, skin int32) []int32 {
	var out []int32
	for _, o := range role.Ornaments {
		if o.RoleSkinID == skin && o.OrnamentID != 0 {
			out = append(out, o.OrnamentID)
		}
	}
	return out
}

func refreshSpawnedRoleOrnaments(s *PlayerSession, role *save.RoleSave, stale []int32) {
	live := s.Combat.Formation.FindLiveRoleEntity(role.RoleID)
	if live == nil {
		return
	}
	cur := skinOrnaments(role, role.RoleSkinID)
	s.Combat.Buffs.SwapEquipmentBuffsS2C(live, OrnamentBuffs(s.Data.Tables, stale), OrnamentBuffs(s.Data.Tables, cur), combat.WeaponRemoveReason, combat.WeaponApplyReason, s.Sink())
	s.Client.Push(&protocol.EntityDressOrnamentChangeNotify{EntityId: live.NetID, OrnamentComponentPb: &protocol.OrnamentComponentPb{OrnamentIds: cur}})
}

// rolesByID are the player's roles in role id order.
func rolesByID(p *save.PlayerData) []*save.RoleSave {
	roles := p.Roles.Values()
	slices.SortStableFunc(roles, func(a, b *save.RoleSave) int { return int(a.RoleID) - int(b.RoleID) })
	return roles
}

func LoadCalabashSkinInfo(s *PlayerSession, _ *protocol.LoadCalabashSkinInfoRequest) *protocol.LoadCalabashSkinInfoResponse {
	var equip int32
	for _, r := range rolesByID(s.Combat.Player) {
		if _, main := s.Data.Tables.MainRoleConfigById[r.RoleID]; main && r.CalabashSkinID != 0 {
			equip = r.CalabashSkinID
			break
		}
	}
	resp := &protocol.LoadCalabashSkinInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, EquipSkinId: equip}
	for _, c := range s.Data.Tables.CalabashSkin {
		resp.SkinItemIds = append(resp.SkinItemIds, c.Id)
	}
	return resp
}

func CalabashSkinTakeOn(s *PlayerSession, r *protocol.CalabashSkinTakeOnRequest) *protocol.CalabashSkinTakeOnResponse {
	if r.SkinId != 0 && !slices.ContainsFunc(s.Data.Tables.CalabashSkin, func(c *data.CalabashSkin) bool { return c.Id == r.SkinId }) {
		return &protocol.CalabashSkinTakeOnResponse{ErrorCode: protocol.ErrorCode_ErrorCode_CalabashSkinUnLockErr}
	}
	for _, role := range rolesByID(s.Combat.Player) {
		if _, main := s.Data.Tables.MainRoleConfigById[role.RoleID]; !main {
			continue
		}
		if role.CalabashSkinID != r.SkinId {
			role.CalabashSkinID = r.SkinId
			save.SaveRole(s.Combat.Player, role)
		}
		if live := s.Combat.Formation.FindLiveRoleEntity(role.RoleID); live != nil {
			s.Client.Push(&protocol.EntityCalabashSkinChangeNotify{EntityId: live.NetID, CalabashSkinComponent: &protocol.CalabashSkinComponentPb{CalabashSkinId: r.SkinId}})
		}
	}
	return &protocol.CalabashSkinTakeOnResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, SkinId: r.SkinId}
}

func RoleSkin(s *PlayerSession, _ *protocol.RoleSkinRequest) *protocol.RoleSkinResponse {
	resp := &protocol.RoleSkinResponse{}
	for _, id := range s.Combat.Player.CosmeticInfo.RoleSkins {
		if id != 0 {
			resp.SkinIds = append(resp.SkinIds, id)
		}
	}
	return resp
}

func pushWeaponSkinTakeOn(s *PlayerSession, roleID, skin int32) {
	s.Client.Push(&protocol.EquipTakeOnNotify{DataList: []*protocol.RoleLoadEquipData{{RoleID: roleID, Pos: protocol.EquipPos_EquipPos_WeaponSkin, EquipIncID: skin}}})
}

func pushEquipSkinChange(s *PlayerSession, roleID, skin int32) {
	if live := s.Combat.Formation.FindLiveRoleEntity(roleID); live != nil {
		s.Client.Push(&protocol.EntityEquipSkinChangeNotify{EntityId: live.NetID, RoleWeaponSkinComponent: &protocol.RoleWeaponSkinComponentPb{WeaponSkinId: skin}})
	}
}

func RoleSkinChange(s *PlayerSession, r *protocol.RoleSkinChangeRequest) *protocol.RoleSkinChangeResponse {
	player := s.Combat.Player
	t := s.Data.Tables
	role, ok := player.Roles.Get(r.RoleId)
	if !ok {
		return &protocol.RoleSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	var skin int32
	if r.SkinId == 0 {
		ri, ok := t.RoleInfoById[r.RoleId]
		if !ok {
			return &protocol.RoleSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrRoleSkinConfig}
		}
		skin = ri.SkinId
	} else {
		rs, ok := t.RoleSkinById[r.SkinId]
		if !slices.Contains(player.CosmeticInfo.RoleSkins, r.SkinId) || !ok || rs.RoleId != r.RoleId {
			return &protocol.RoleSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrRoleSkinNotMatch}
		}
		skin = r.SkinId
	}
	stale := skinOrnaments(role, role.RoleSkinID)
	role.RoleSkinID = skin
	if cfg, ok := t.RoleSkinById[skin]; r.SignatureWeapon && ok {
		suit := cfg.SuitWeaponSkinId
		if (suit == 0 || slices.Contains(player.CosmeticInfo.WeaponSkins, suit)) && IsWeaponSkinCompatible(t, r.RoleId, suit) {
			role.WeaponSkinID = suit
			pushEquipSkinChange(s, r.RoleId, suit)
			pushWeaponSkinTakeOn(s, r.RoleId, suit)
		}
	}
	s.Combat.Formation.RebuildLiveRoleEntity(r.RoleId, s.Clock.RealMs(), s.Sink())
	save.SaveRole(player, role)
	s.Client.Push(s.Combat.Formation.BuildUpdateFormationNotify())
	refreshSpawnedRoleOrnaments(s, role, stale)
	return &protocol.RoleSkinChangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func LoadEquipInfo(s *PlayerSession, _ *protocol.LoadEquipInfoRequest) *protocol.LoadEquipInfoResponse {
	resp := &protocol.LoadEquipInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	seen := map[[2]int32]bool{}
	for _, role := range rolesByID(s.Combat.Player) {
		key := [2]int32{role.RoleID, role.WeaponSkinID}
		if role.WeaponSkinID != 0 && IsWeaponSkinCompatible(s.Data.Tables, role.RoleID, role.WeaponSkinID) && !seen[key] {
			seen[key] = true
			resp.EquipList = append(resp.EquipList, &protocol.RoleSkinEquipData{RoleID: role.RoleID, SkinItemId: role.WeaponSkinID})
		}
	}
	return resp
}

func EquipSkinTakeOn(s *PlayerSession, r *protocol.EquipSkinTakeOnRequest) *protocol.EquipSkinTakeOnResponse {
	bad := &protocol.EquipSkinTakeOnResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	d := r.Data
	if d == nil {
		return bad
	}
	player := s.Combat.Player
	role, ok := player.Roles.Get(d.RoleID)
	if !ok || (d.SkinItemId != 0 && !slices.Contains(player.CosmeticInfo.WeaponSkins, d.SkinItemId)) || !IsWeaponSkinCompatible(s.Data.Tables, d.RoleID, d.SkinItemId) {
		return bad
	}
	role.WeaponSkinID = d.SkinItemId
	save.SaveRole(player, role)
	s.Client.Push(s.Combat.Formation.BuildUpdateFormationNotify())
	pushEquipSkinChange(s, d.RoleID, d.SkinItemId)
	pushWeaponSkinTakeOn(s, d.RoleID, d.SkinItemId)
	return &protocol.EquipSkinTakeOnResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, DataList: []*protocol.RoleSkinEquipData{{RoleID: d.RoleID, SkinItemId: d.SkinItemId}}}
}

func EquipSkinUnLoad(s *PlayerSession, r *protocol.EquipSkinUnLoadRequest) *protocol.EquipSkinUnLoadResponse {
	player := s.Combat.Player
	role, ok := player.Roles.Get(r.RoleID)
	if !ok {
		return &protocol.EquipSkinUnLoadResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	role.WeaponSkinID = 0
	save.SaveRole(player, role)
	s.Client.Push(s.Combat.Formation.BuildUpdateFormationNotify())
	pushEquipSkinChange(s, r.RoleID, 0)
	s.Client.Push(&protocol.WeaponSkinUnLoadNotify{RoleId: r.RoleID, SkinId: 0})
	return &protocol.EquipSkinUnLoadResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func wearFlySkin(role *save.RoleSave, skin *data.FlySkinConfig, id int32) {
	if skin.SkinType == 0 {
		role.SoarSkinID = id
	} else {
		role.ParaglidingSkinID = id
	}
}

func pushFlyEquip(s *PlayerSession, roleID int32, skin *data.FlySkinConfig) {
	if live := s.Combat.Formation.FindLiveRoleEntity(roleID); live != nil {
		s.Client.Push(&protocol.EntityFlyEquipChangeNotify{SkinChanges: []*protocol.EntityFlySkin{{
			EntityId: live.NetID, SkinInfos: []*protocol.FlySkinInfo{{SkinId: skin.Id, SkinType: skin.SkinType}},
		}}})
	}
}

func FlySkinWear(s *PlayerSession, r *protocol.FlySkinWearRequest) *protocol.FlySkinWearResponse {
	player := s.Combat.Player
	if !slices.Contains(player.CosmeticInfo.FlySkins, r.SkinId) {
		return &protocol.FlySkinWearResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	skin, ok := s.Data.Tables.FlySkinConfigById[r.SkinId]
	if !ok {
		return &protocol.FlySkinWearResponse{ErrorCode: protocol.ErrorCode_ErrorCode_FlySkinItemNoConfig}
	}
	role, ok := player.Roles.Get(r.RoleId)
	if !ok {
		return &protocol.FlySkinWearResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	wearFlySkin(role, skin, skin.Id)
	save.SaveRole(player, role)
	pushFlyEquip(s, r.RoleId, skin)
	return &protocol.FlySkinWearResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func FlySkinWearAllRole(s *PlayerSession, r *protocol.FlySkinWearAllRoleRequest) *protocol.FlySkinWearAllRoleResponse {
	player := s.Combat.Player
	if !slices.Contains(player.CosmeticInfo.FlySkins, r.SkinId) {
		return &protocol.FlySkinWearAllRoleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	skin, ok := s.Data.Tables.FlySkinConfigById[r.SkinId]
	if !ok {
		return &protocol.FlySkinWearAllRoleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_FlySkinItemNoConfig}
	}
	resp := &protocol.FlySkinWearAllRoleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, role := range rolesByID(player) {
		wearFlySkin(role, skin, skin.Id)
		save.SaveRole(player, role)
		pushFlyEquip(s, role.RoleID, skin)
		resp.SkinChanges = append(resp.SkinChanges, &protocol.RoleFlySkinChange{RoleId: role.RoleID, SkinId: skin.Id})
	}
	return resp
}

func FlySkinUnLoad(s *PlayerSession, r *protocol.FlySkinUnLoadRequest) *protocol.FlySkinUnLoadResponse {
	role, ok := s.Combat.Player.Roles.Get(r.RoleId)
	if !ok {
		return &protocol.FlySkinUnLoadResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	if skin, ok := s.Data.Tables.FlySkinConfigById[r.SkinId]; ok {
		wearFlySkin(role, skin, 0)
		save.SaveRole(s.Combat.Player, role)
	}
	return &protocol.FlySkinUnLoadResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func FlySkinAllUnLoad(s *PlayerSession, r *protocol.FlySkinAllUnLoadRequest) *protocol.FlySkinAllUnLoadResponse {
	resp := &protocol.FlySkinAllUnLoadResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, role := range rolesByID(s.Combat.Player) {
		if r.SkinType == 0 {
			role.SoarSkinID = 0
		} else {
			role.ParaglidingSkinID = 0
		}
		save.SaveRole(s.Combat.Player, role)
		resp.UnLoadSkinRoles = append(resp.UnLoadSkinRoles, role.RoleID)
	}
	return resp
}

func ChangeOrnament(s *PlayerSession, r *protocol.ChangeOrnamentRequest) *protocol.ChangeOrnamentResponse {
	t := s.Data.Tables
	player := s.Combat.Player
	var roleID int32
	if rs, ok := t.RoleSkinById[r.RoleSkinId]; ok {
		roleID = rs.RoleId
	}
	if roleID == 0 {
		for _, ri := range t.RoleInfo {
			if ri.SkinId == r.RoleSkinId {
				roleID = ri.Id
				break
			}
		}
	}
	if roleID == 0 {
		return &protocol.ChangeOrnamentResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	cfg, ok := t.OrnamentById[r.OrnamentId]
	if r.OrnamentId == 0 || !slices.Contains(player.CosmeticInfo.Ornaments, r.OrnamentId) || !ok || !slices.Contains(cfg.RoleSkinIds, r.RoleSkinId) {
		return &protocol.ChangeOrnamentResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrOrnamentConfig}
	}
	role, ok := player.Roles.Get(roleID)
	if !ok {
		return &protocol.ChangeOrnamentResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	stale := skinOrnaments(role, r.RoleSkinId)
	changed := false
	if r.IsDress {
		if !slices.ContainsFunc(role.Ornaments, func(o save.OrnamentEquipSave) bool {
			return o.RoleSkinID == r.RoleSkinId && o.OrnamentID == r.OrnamentId
		}) {
			if cfg.OrGroupId != 0 {
				role.Ornaments = slices.DeleteFunc(role.Ornaments, func(o save.OrnamentEquipSave) bool {
					other, ok := t.OrnamentById[o.OrnamentID]
					return o.RoleSkinID == r.RoleSkinId && ok && other.OrGroupId == cfg.OrGroupId
				})
			}
			role.Ornaments = append(role.Ornaments, save.OrnamentEquipSave{RoleSkinID: r.RoleSkinId, OrnamentID: r.OrnamentId})
			changed = true
		}
	} else {
		n := len(role.Ornaments)
		role.Ornaments = slices.DeleteFunc(role.Ornaments, func(o save.OrnamentEquipSave) bool {
			return o.RoleSkinID == r.RoleSkinId && o.OrnamentID == r.OrnamentId
		})
		changed = len(role.Ornaments) != n
	}
	if changed {
		save.SaveRole(player, role)
		s.Client.Push(s.Combat.Formation.BuildUpdateFormationNotify())
		refreshSpawnedRoleOrnaments(s, role, stale)
	}
	update := &protocol.OrnamentDressInfoUpdateNotify{}
	var skins []int32
	bySkin := map[int32][]int32{}
	for _, owned := range rolesByID(player) {
		for _, o := range owned.Ornaments {
			if o.OrnamentID == 0 {
				continue
			}
			list, ok := bySkin[o.RoleSkinID]
			if !ok {
				skins = append(skins, o.RoleSkinID)
			}
			if !slices.Contains(list, o.OrnamentID) {
				bySkin[o.RoleSkinID] = append(list, o.OrnamentID)
			}
		}
	}
	for _, skin := range skins {
		update.OrnamentDressInfos = append(update.OrnamentDressInfos, &protocol.OrnamentDressInfo{RoleSkinId: skin, DressOrnamentIds: bySkin[skin]})
	}
	s.Client.Push(update)
	return &protocol.ChangeOrnamentResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ReadCameraParam reads player/<id>/settings/camera.
func ReadCameraParam(files *save.PlayerFiles) *protocol.CameraParamInfo {
	z := files.Read("settings/camera")
	return &protocol.CameraParamInfo{
		CameraMode: save.Int(z, "camera_mode", 0), PitchValue: save.Float(z, "pitch", 0), YawValue: save.Float(z, "yaw", 0),
		RollValue: save.Float(z, "roll", 0), PosX: save.Float(z, "pos_x", 0), PosY: save.Float(z, "pos_y", 0), PosZ: save.Float(z, "pos_z", 0),
		Fov: save.Int(z, "fov", 0), FocusDistance: save.Int(z, "focus_distance", 0), BlurRadius: save.Int(z, "blur_radius", 0),
		CameraTiltAngle: save.Int(z, "tilt_angle", 0), Weather: save.Int(z, "weather", 0), Atmosphere: save.Int(z, "atmosphere", 0),
		WindDirection: save.Int(z, "wind_direction", 0),
	}
}

func WriteCameraParam(files *save.PlayerFiles, p *protocol.CameraParamInfo) {
	files.Write("settings/camera", zon.NewStruct(
		zon.F("camera_mode", zN(p.CameraMode)), zon.F("pitch", zon.Float32(p.PitchValue)), zon.F("yaw", zon.Float32(p.YawValue)),
		zon.F("roll", zon.Float32(p.RollValue)), zon.F("pos_x", zon.Float32(p.PosX)), zon.F("pos_y", zon.Float32(p.PosY)),
		zon.F("pos_z", zon.Float32(p.PosZ)), zon.F("fov", zN(p.Fov)), zon.F("focus_distance", zN(p.FocusDistance)),
		zon.F("blur_radius", zN(p.BlurRadius)), zon.F("tilt_angle", zN(p.CameraTiltAngle)), zon.F("weather", zN(p.Weather)),
		zon.F("atmosphere", zN(p.Atmosphere)), zon.F("wind_direction", zN(p.WindDirection))))
}

func CameraSettingQuery(s *PlayerSession, _ *protocol.CameraSettingQueryRequest) *protocol.CameraSettingQueryResponse {
	return &protocol.CameraSettingQueryResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, CameraSetting: &protocol.CameraSettingInfo{CameraParam: ReadCameraParam(s.Files)}}
}

func CameraSettingModify(s *PlayerSession, r *protocol.CameraSettingModifyRequest) *protocol.CameraSettingModifyResponse {
	if r.CameraSetting != nil && r.CameraSetting.CameraParam != nil {
		WriteCameraParam(s.Files, r.CameraSetting.CameraParam)
	}
	return &protocol.CameraSettingModifyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func SelfPortraitState(s *PlayerSession, _ *protocol.SelfPortraitStateRequest) *protocol.SelfPortraitStateResponse {
	f := s.Formation()
	var cur int64
	for _, r := range f.FilledRoles() {
		if r.RoleID == f.CurRole {
			cur = r.EntityID
			break
		}
	}
	var pos world.Vec3
	var rot world.Rot
	if s.Scene != nil {
		pos, rot = s.Scene.PlayerPos, s.Scene.PlayerRot
	}
	s.Client.Push(&protocol.SwitchRoleNotify{PlayerId: s.State.PlayerID(), DownEntityId: cur, UpEntityId: cur, Transform: &protocol.Transform{Pos: pos.ToPb(), Rot: rot.ToPb()}})
	return &protocol.SelfPortraitStateResponse{ErrCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- explore tools and echo projector summons (§5) -----

func VisionExploreSkillSet(s *PlayerSession, r *protocol.VisionExploreSkillSetRequest) *protocol.VisionExploreSkillSetResponse {
	old := s.State.ExploreTools.ActiveExploreSkill
	s.State.ExploreTools.ActiveExploreSkill = r.SkillId
	s.State.SaveExploreTools()
	for _, e := range s.Combat.Scene.Entities.Values() {
		vs := e.VisionSkillComponent
		if vs == nil {
			continue
		}
		changed := false
		for _, info := range vs.VisionSkillInfos {
			if info.SkillId == old {
				info.SkillId = r.SkillId
				changed = true
			}
		}
		if psi := vs.PhantomSkillInfo; psi != nil && psi.SkillId == old {
			psi.SkillId = r.SkillId
			changed = true
		}
		if !changed {
			continue
		}
		n := &protocol.VisionSkillChangeNotify{EntityId: e.NetID}
		if vs.PhantomSkillInfo != nil {
			n.PhantomSkillInfo = proto.Clone(vs.PhantomSkillInfo).(*protocol.VisionSkillInformation)
		}
		for _, v := range vs.VisionSkillInfos {
			n.VisionSkillInfos = append(n.VisionSkillInfos, proto.Clone(v).(*protocol.VisionSkillInformation))
		}
		s.Client.Push(n)
	}
	return &protocol.VisionExploreSkillSetResponse{ErrCode: protocol.ErrorCode_ErrorCode_Success, SkillId: r.SkillId}
}

func (s *PlayerSession) roulettes() []*protocol.ExploreSkillRoulette {
	t := s.State.ExploreTools
	return []*protocol.ExploreSkillRoulette{
		{SkillIds: Pad(t.Roulette), ExtraItemId: t.ExploreExtraItemID, ExploreSkill: t.ActiveExploreSkill},
		{SkillIds: Pad(t.FunctionRoulette), ExtraItemId: t.FunctionExtraItemID, ExploreSkill: t.ActiveFunctionSkill},
		{},
		{SkillIds: Pad(t.MotorcycleRoulette), ExtraItemId: t.MotorcycleExtraItemID, ExploreSkill: t.ActiveMotorcycleSkill},
	}
}

func ExploreSkillRouletteSet(s *PlayerSession, r *protocol.ExploreSkillRouletteSetRequest) *protocol.ExploreSkillRouletteSetResponse {
	t := s.State.ExploreTools
	sel := r.SkillRoulette
	if sel == nil && len(r.SkillRoulettes) > 0 {
		sel = r.SkillRoulettes[0]
	}
	if sel != nil {
		padded := Pad(sel.SkillIds)
		switch r.RouletteType {
		case protocol.RouletteType_RouletteType_Explore:
			t.Roulette, t.ExploreExtraItemID, t.ActiveExploreSkill = padded, sel.ExtraItemId, sel.ExploreSkill
		case protocol.RouletteType_RouletteType_Function:
			t.FunctionRoulette, t.FunctionExtraItemID, t.ActiveFunctionSkill = padded, sel.ExtraItemId, sel.ExploreSkill
		case protocol.RouletteType_RouletteType_Motorcycle:
			t.MotorcycleRoulette, t.MotorcycleExtraItemID, t.ActiveMotorcycleSkill = padded, sel.ExtraItemId, sel.ExploreSkill
		}
		s.State.SaveExploreTools()
	}
	s.Client.Push(&protocol.ExploreSkillRouletteUpdateNotify{RouletteInfo: s.roulettes()})
	return &protocol.ExploreSkillRouletteSetResponse{ErrCode: protocol.ErrorCode_ErrorCode_Success, RouletteType: r.RouletteType, SkillRoulette: sel, SkillRoulettes: s.roulettes()}
}

func Summon3(s *PlayerSession, r *protocol.Summon3Request) *protocol.Summon3Response {
	info := r.SummonInfo
	if info == nil {
		return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	sc := s.Scene
	if info.SummonEntityId <= 0 || sc == nil {
		return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSummonAddEntityFail}
	}
	if sc.Get(info.SummonEntityId) != nil || s.Combat.Scene.Entities.Has(info.SummonEntityId) {
		return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSummonEntityIdAlreadyExist}
	}
	monster := info.SummonConfigId
	t := s.Data.Tables
	unlocked := slices.ContainsFunc(t.CalabashDevelopReward, func(d *data.CalabashDevelopReward) bool { return d.MonsterId == monster && d.IsShow && d.AllowVision }) &&
		slices.ContainsFunc(t.PhantomItem, func(p *data.PhantomItem) bool { return p.ParentMonsterId == 0 && p.MonsterId == monster })
	if !unlocked {
		return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomInteractionNotUnlock}
	}
	var skin int32
	for i := len(s.Combat.Player.Calabash.ProjectorSkins) - 1; i >= 0; i-- {
		if ps := s.Combat.Player.Calabash.ProjectorSkins[i]; ps.MonsterID == monster {
			skin = ps.SkinID
			break
		}
	}
	var item *data.PhantomItem
	if si, ok := t.PhantomItemByItemId[skin]; skin != 0 && ok && si.ParentMonsterId == monster {
		item = si
	} else if i := slices.IndexFunc(t.PhantomItem, func(p *data.PhantomItem) bool { return p.ParentMonsterId == 0 && p.MonsterId == monster }); i >= 0 {
		item = t.PhantomItem[i]
	}
	if item == nil {
		return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPhantomInteractionConfigNotFind}
	}
	summonID := item.MeshId
	if summonID == 0 {
		summonID = item.SkillId
	}
	cfg, ok := t.SummonCfgById[summonID]
	var tpl *data.TemplateConfig
	if ok && cfg.BlueprintType != "" {
		tpl = s.Data.Template(cfg.BlueprintType)
	}
	if summonID == 0 || tpl == nil {
		return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_ErrSummonCfgNotFound}
	}
	var model int32
	if bp := s.Data.Blueprint(cfg.BlueprintType); bp != nil {
		model = bp.ModelId
	}
	skills := &protocol.SkillComponentPb{}
	for _, id := range s.Data.PassiveSkills(model) {
		skills.GaPassiveSkills = append(skills.GaPassiveSkills, &protocol.GaPassiveSkill{SkillId: int64(id), ContextId: world.PassiveSkillContextID})
	}
	var pos world.Vec3
	var rot world.Rot
	if info.Pos != nil {
		pos = world.Vec3From(info.Pos)
	}
	if info.Rot != nil {
		rot = world.RotFrom(info.Rot)
	}
	var born []int64
	for _, b := range cfg.BornBuffId {
		if x := int64(b); x != 0 {
			born = append(born, x)
		}
	}
	e := sc.AddAs(&world.Entity{ConfigID: tpl.Id, ConfigType: world.ConfigTemplate, EntityType: world.TypeVision, Pos: pos, Rot: rot,
		IsVisible: info.IsVisible, PlayerID: s.State.PlayerID()}, info.SummonEntityId)
	e.Components = append(e.Components,
		world.Attributes(nil, nil),
		world.FightBuffs(sc, e.ID, born),
		world.C(&protocol.EntityComponentPb_CombineEntityInfoPb{CombineEntityInfoPb: &protocol.CombineEntityInfoPb{}}),
		world.C(&protocol.EntityComponentPb_EquipComponent{EquipComponent: &protocol.EquipComponentPb{}}),
		world.C(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: &protocol.LogicStateComponentPb{}}),
		world.C(&protocol.EntityComponentPb_MonsterAiComponentPb{MonsterAiComponentPb: &protocol.MonsterAiComponentPb{CombatMessageId: combat.AiContextID}}),
		world.C(&protocol.EntityComponentPb_SkillComponentPb{SkillComponentPb: skills}),
		world.C(&protocol.EntityComponentPb_SummonerComponent{SummonerComponent: &protocol.SummonerComponentPb{
			SummonerId: r.SummonerEntityId, SummonCfgId: cfg.Id, SummonSkillId: summonID, PlayerId: s.State.PlayerID(),
			Type: protocol.ESummonType_ESummonType_ESummonTypeConcomitantVision,
		}}),
		world.C(&protocol.EntityComponentPb_VisionSkillComponent{VisionSkillComponent: &protocol.VisionSkillComponentPb{}}),
	)
	s.Client.Push(&protocol.EntityAddNotify{EntityPbs: []*protocol.EntityPb{e.ToPb()}})
	return &protocol.Summon3Response{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func RemoveSummonEntity(s *PlayerSession, r *protocol.RemoveSummonEntityRequest) *protocol.RemoveSummonEntityResponse {
	if sc := s.Scene; sc != nil {
		var removed []*protocol.EntityRemoveInfo
		for _, id := range r.RemoveEntityIds {
			e := sc.Get(id)
			if e == nil || !slices.ContainsFunc(e.Components, func(c *protocol.EntityComponentPb) bool {
				sm := c.GetSummonerComponent()
				return sm != nil && sm.SummonerId == r.SummonerId
			}) {
				continue
			}
			sc.Remove(id)
			removed = append(removed, &protocol.EntityRemoveInfo{EntityId: id})
		}
		if len(removed) > 0 {
			s.Client.Push(&protocol.EntityRemoveNotify{IsRemove: true, RemoveInfos: removed})
		}
	}
	return &protocol.RemoveSummonEntityResponse{ErrCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- guides (§6; player/<id>/guide_info) -----

var DefaultFinishedGuideGroups = []int32{10116, 10193, 10194, 10120, 10175, 10270, 10285}

type PlayerGuides struct {
	files    *save.PlayerFiles
	finished []int32
	loaded   bool
}

func NewPlayerGuides(files *save.PlayerFiles) *PlayerGuides { return &PlayerGuides{files: files} }

func (g *PlayerGuides) ensureLoaded() {
	if g.loaded {
		return
	}
	g.loaded = true
	g.finished = nil
	if z := g.files.Read("guide_info"); z != nil {
		g.finished = save.Ints(z, "finished_groups")
	}
}

func (g *PlayerGuides) HandleInfo(_ *protocol.GuideInfoRequest) *protocol.GuideInfoResponse {
	g.ensureLoaded()
	resp := &protocol.GuideInfoResponse{}
	for _, id := range DefaultFinishedGuideGroups {
		if !slices.Contains(g.finished, id) {
			resp.GuideGroupFinishList = append(resp.GuideGroupFinishList, id)
		}
	}
	resp.GuideGroupFinishList = append(resp.GuideGroupFinishList, g.finished...)
	return resp
}

func (g *PlayerGuides) HandleFinish(r *protocol.GuideFinishRequest) *protocol.GuideFinishResponse {
	g.ensureLoaded()
	if r.GroupId != 0 && !slices.Contains(g.finished, r.GroupId) {
		g.finished = append(g.finished, r.GroupId)
		g.files.Write("guide_info", zon.NewStruct(zon.F("finished_groups", zonInts(g.finished))))
	}
	return &protocol.GuideFinishResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- profile and social (§6.4, §7.2–7.4, §11.2, §11.3) -----

func IllustratedInfo(s *PlayerSession, r *protocol.IllustratedInfoRequest) *protocol.IllustratedInfoResponse {
	resp := &protocol.IllustratedInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, typ := range r.TypeList {
		cls := &protocol.IllustratedClass{Type: typ}
		if typ == protocol.IllustratedType_IllustratedType_VocalCorpse {
			for _, row := range s.Data.Tables.CalabashDevelopReward {
				if row.IsShow {
					cls.IllustratedEntryList = append(cls.IllustratedEntryList, &protocol.IllustratedEntry{Id: row.MonsterId, CreateTime: 1, Num: int32(len(row.DevelopCondition)), IsRead: true})
				}
			}
		}
		resp.IllustratedClassList = append(resp.IllustratedClassList, cls)
	}
	return resp
}

func ShortMessageInfo() *protocol.ShortMessageInfoResponse {
	return &protocol.ShortMessageInfoResponse{
		ErrCode:  protocol.ErrorCode_ErrorCode_Success,
		BubbleId: 80810001, BubbleIds: []int32{80810001, 80810002, 80810003, 80810004, 80810005},
		ChatBgId: 80820001, ChatBgIds: []int32{80820001, 80820002, 80820004, 80820005},
		ChatThemeId: 80891001, ChatThemeIds: []int32{80891001, 80891002},
		ChatCharmId: 80892001, ChatCharmIds: []int32{80892001, 80892002},
	}
}

func PlayerHead(s *PlayerSession) *protocol.PlayerHeadResponse {
	resp := &protocol.PlayerHeadResponse{}
	for _, h := range s.Data.Tables.PlayerHeadRe {
		resp.PlayerHeadIds = append(resp.PlayerHeadIds, h.Id)
	}
	return resp
}

func PlayerTitle(s *PlayerSession) *protocol.PlayerTitleResponse {
	resp := &protocol.PlayerTitleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, t := range s.Data.Tables.PlayerTitle {
		resp.PlayerTitleInfo = append(resp.PlayerTitleInfo, &protocol.PlayerTitleInfo{PlayerTitleId: t.Id, IsUnlock: true, ExtraInfo: 1})
	}
	return resp
}

func RoleShowListUpdate(s *PlayerSession, r *protocol.RoleShowListUpdateRequest) *protocol.RoleShowListUpdateResponse {
	player := s.Combat.Player
	next := []int32{}
	for _, id := range r.RoleList {
		if len(next) >= 3 {
			break
		}
		if player.Roles.Has(id) && !slices.Contains(next, id) {
			next = append(next, id)
		}
	}
	player.BasicInfo.RoleShowList = next
	save.SaveBasicInfo(player)
	return &protocol.RoleShowListUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func ChangeCard(s *PlayerSession, r *protocol.ChangeCardRequest) *protocol.ChangeCardResponse {
	if slices.ContainsFunc(s.Data.Tables.BackgroundCard, func(c *data.BackgroundCard) bool { return c.Id == r.CardId }) {
		s.Combat.Player.BasicInfo.CurCardID = r.CardId
		save.SaveBasicInfo(s.Combat.Player)
	}
	return &protocol.ChangeCardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func ChangePlayerTitle(s *PlayerSession, r *protocol.ChangePlayerTitleRequest) *protocol.ChangePlayerTitleResponse {
	if r.PlayerTitleId == 0 || slices.ContainsFunc(s.Data.Tables.PlayerTitle, func(t *data.PlayerTitle) bool { return t.Id == r.PlayerTitleId }) {
		s.Combat.Player.BasicInfo.CurPlayerTitleID = r.PlayerTitleId
		save.SaveBasicInfo(s.Combat.Player)
		extra := int32(1)
		if r.PlayerTitleId == 0 {
			extra = 0
		}
		s.Client.Push(&protocol.DressPlayerTitleUpdateNotify{PlayerTitleId: r.PlayerTitleId, PlayerTitleExtraParam: extra})
	}
	return &protocol.ChangePlayerTitleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func ModifyName(s *PlayerSession, r *protocol.ModifyNameRequest) *protocol.ModifyNameResponse {
	last := max(0, s.Clock.Seconds()-72*3600+5)
	bi := &s.Combat.Player.BasicInfo
	bi.Attributes.Name = r.Name
	bi.LastModifyNameTime = last
	save.SaveBasicInfo(s.Combat.Player)
	s.Client.Push(&protocol.NameModifyNotify{Name: r.Name, LastModifyNameTime: last})
	return &protocol.ModifyNameResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Name: r.Name, LastModifyNameTime: last, ModifyName: ""}
}

func ChangeHeadPhoto(s *PlayerSession, r *protocol.ChangeHeadPhotoRequest) *protocol.ChangeHeadPhotoResponse {
	bi := &s.Combat.Player.BasicInfo
	if slices.ContainsFunc(s.Data.Tables.PlayerHeadRe, func(h *data.PlayerHeadRe) bool { return h.Id == r.HeadPhotoId }) {
		bi.Attributes.HeadPhoto = r.HeadPhotoId
		save.SaveBasicInfo(s.Combat.Player)
	}
	return &protocol.ChangeHeadPhotoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, HeadPhotoId: bi.Attributes.HeadPhoto}
}

// ModifySignature saves the signature (R-153; specs/systems.md §7 item 9). The client checks
// length itself and, on Success, shows the text it sent (PersonalController).
func ModifySignature(s *PlayerSession, r *protocol.ModifySignatureRequest) *protocol.ModifySignatureResponse {
	bi := &s.Combat.Player.BasicInfo
	bi.Attributes.Sign = r.Signature
	save.SaveBasicInfo(s.Combat.Player)
	return &protocol.ModifySignatureResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Signature: bi.Attributes.Sign}
}

func BirthdayInit(s *PlayerSession, r *protocol.BirthdayInitRequest) *protocol.BirthdayInitResponse {
	if r.Birthday > 0 {
		bi := &s.Combat.Player.BasicInfo
		bi.Birthday = r.Birthday
		bi.DisplayBirthday = true
		save.SaveBasicInfo(s.Combat.Player)
		s.Client.Push(&protocol.BirthDayInfoNotify{BirthDayReset: true, RecentRewardTime: r.Birthday / 10000})
	}
	return &protocol.BirthdayInitResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func BirthdayShowSet(s *PlayerSession, r *protocol.BirthdayShowSetRequest) *protocol.BirthdayShowSetResponse {
	s.Combat.Player.BasicInfo.DisplayBirthday = r.DisPlay
	save.SaveBasicInfo(s.Combat.Player)
	return &protocol.BirthdayShowSetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func TutorialUnlock(s *PlayerSession, r *protocol.TutorialUnlockRequest) *protocol.TutorialUnlockResponse {
	return &protocol.TutorialUnlockResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, UnLockInfo: &protocol.TutorialInfo{Id: r.Id, CreateTime: uint32(max(0, s.Clock.Seconds()))}}
}

func TutorialReceive(s *PlayerSession) *protocol.TutorialReceiveResponse {
	s.Actions().GrantItem(3, 20, false)
	return &protocol.TutorialReceiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ItemMap: map[int32]int32{3: 20}}
}

func ButtonIsEnable(r *protocol.ButtonIsEnableRequest) *protocol.ButtonIsEnableResponse {
	resp := &protocol.ButtonIsEnableResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Type: r.Type, Enable: true}
	for _, t := range r.Types_ {
		resp.Result = append(resp.Result, &protocol.ButtonEnableResult{Type: t, Enable: true})
	}
	return resp
}

// ----- in the session -----

func (s *PlayerSession) ClientStorage() *PlayerClientStorage {
	if s.clientStorage == nil {
		s.clientStorage = NewPlayerClientStorage(s.Files)
	}
	return s.clientStorage
}

func (s *PlayerSession) Guides() *PlayerGuides {
	if s.guides == nil {
		s.guides = NewPlayerGuides(s.Files)
	}
	return s.guides
}
