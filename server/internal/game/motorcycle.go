// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/data"
	"tidelab/internal/gateway"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

// PlayerMotorcycle is the motorcycle's progression, outlook, presets and ride-sharing
// (player/<id>/motor_info; specs/systems.md §10, save-format.md §5.8). Ported from our C#
// Game/Motorcycle.cs.
type PlayerMotorcycle struct {
	files          *save.PlayerFiles
	original       *zon.Value
	loaded         bool
	passengerNetID int64

	Level, Exp, RewardedMaxLevel, TreeInUse, DailyExpGain, DailyExpLimit int32
	TechNodes                                                            []*MotorTechNode
	TaskNodes                                                            []*MotorTaskNode
	OwnedSkins, OwnedStickers, OwnedDecorations, OwnedFrames             []int32
	EquippedSkin, EquippedFrame                                          int32
	EquippedStickers, EquippedDecorations                                []int32
	Presets                                                              []*MotorPreset
	NextPresetID                                                         int32
}

type MotorTechNode struct {
	ID, TreeType, Level int32
	Unlocked            bool
	Current, Target     int32
}

func (n *MotorTechNode) ToPb() *protocol.MotorTechPb {
	return &protocol.MotorTechPb{Id: n.ID, Level: n.Level, Unlock: n.Unlocked, Current: n.Current, Target: n.Target}
}

type MotorTaskNode struct {
	ID, TreeType, Type, Current, Target, Rewarded, WaitReward, MaxReward int32
}

func (n *MotorTaskNode) ToPb() *protocol.MotorTaskPb {
	t := protocol.MotorTaskTypePb_MotorTaskTypePb_Unknown
	switch n.Type {
	case 1:
		t = protocol.MotorTaskTypePb_MotorTaskTypePb_Single
	case 2:
		t = protocol.MotorTaskTypePb_MotorTaskTypePb_Limited
	case 3:
		t = protocol.MotorTaskTypePb_MotorTaskTypePb_Cycle
	}
	return &protocol.MotorTaskPb{
		Id: n.ID, Type: t,
		Process: &protocol.MotorTaskProcessPb{Current: n.Current, Target: n.Target},
		Reward:  &protocol.MotorTaskRewardPb{Rewarded: n.Rewarded, WaitReward: n.WaitReward, MaxReward: n.MaxReward},
	}
}

type MotorPreset struct {
	ID                    int32
	Name                  string
	Skin                  int32
	Stickers, Decorations []int32
	Frame                 int32
}

func (p *MotorPreset) ToPb() *protocol.MotorOutlookPresetPlanPb {
	return &protocol.MotorOutlookPresetPlanPb{Id: p.ID, Name: p.Name, Preset: &protocol.MotorOutlookEquippedPb{
		SkinEquipped: p.Skin, StickerEquipped: slices.Clone(p.Stickers), DecorationsEquipped: slices.Clone(p.Decorations), FrameEquipped: p.Frame,
	}}
}

func NewPlayerMotorcycle(files *save.PlayerFiles) *PlayerMotorcycle {
	return &PlayerMotorcycle{files: files, Level: 1, NextPresetID: 1}
}

// Motorcycle is the player's motorcycle (loaded at first use in the session).
func (s *PlayerSession) Motorcycle() *PlayerMotorcycle {
	if s.motorcycle == nil {
		s.motorcycle = NewPlayerMotorcycle(s.Files)
	}
	return s.motorcycle
}

func (m *PlayerMotorcycle) EnsureInitialized(s *PlayerSession) {
	m.ensureLoaded(s.Data.Tables)
	m.syncToPlayerState(s.State)
}

func (m *PlayerMotorcycle) syncToPlayerState(st *save.PlayerState) {
	if st.Motor == nil {
		st.Motor = &save.MotorLooks{}
	}
	st.Motor.EquippedSkin = m.EquippedSkin
	st.Motor.EquippedFrame = m.EquippedFrame
	st.Motor.EquippedStickers = slices.Clone(m.EquippedStickers)
	st.Motor.EquippedDecorations = slices.Clone(m.EquippedDecorations)
}

func (m *PlayerMotorcycle) ensureLoaded(t *data.DataTables) {
	if m.loaded {
		return
	}
	m.loaded = true
	o := m.files.Read("motor_info")
	m.original = o
	m.Level = save.Int(o, "level", 1)
	m.Exp = save.Int(o, "exp", 0)
	m.RewardedMaxLevel = save.Int(o, "rewarded_max_level", 0)
	m.TreeInUse = save.Int(o, "tree_in_use", 0)
	m.DailyExpGain = save.Int(o, "daily_exp_gain", 0)
	m.DailyExpLimit = save.Int(o, "daily_exp_limit", 0)
	m.TechNodes = nil
	for _, n := range save.List(o, "tech_nodes") {
		m.TechNodes = append(m.TechNodes, &MotorTechNode{
			ID: save.Int(n, "id", 0), TreeType: save.Int(n, "tree_type", 0), Level: save.Int(n, "level", 0),
			Unlocked: save.Bool(n, "unlocked", false), Current: save.Int(n, "current", 0), Target: save.Int(n, "target", 0),
		})
	}
	m.TaskNodes = nil
	for _, n := range save.List(o, "task_nodes") {
		m.TaskNodes = append(m.TaskNodes, &MotorTaskNode{
			ID: save.Int(n, "id", 0), TreeType: save.Int(n, "tree_type", 0), Type: save.Int(n, "type", 0),
			Current: save.Int(n, "current", 0), Target: save.Int(n, "target", 0), Rewarded: save.Int(n, "rewarded", 0),
			WaitReward: save.Int(n, "wait_reward", 0), MaxReward: save.Int(n, "max_reward", 0),
		})
	}
	m.OwnedSkins = save.Ints(o, "owned_skins")
	m.OwnedStickers = save.Ints(o, "owned_stickers")
	m.OwnedDecorations = save.Ints(o, "owned_decorations")
	m.OwnedFrames = save.Ints(o, "owned_frames")
	m.EquippedSkin = save.Int(o, "equipped_skin", 0)
	m.EquippedFrame = save.Int(o, "equipped_frame", 0)
	m.EquippedStickers = save.Ints(o, "equipped_stickers")
	m.EquippedDecorations = save.Ints(o, "equipped_decorations")
	m.Presets = nil
	for _, p := range save.List(o, "presets") {
		m.Presets = append(m.Presets, &MotorPreset{
			ID: save.Int(p, "id", 0), Name: save.Str(p, "name", ""), Skin: save.Int(p, "skin", 0),
			Stickers: save.Ints(p, "stickers"), Decorations: save.Ints(p, "decorations"), Frame: save.Int(p, "frame", 0),
		})
	}
	m.NextPresetID = save.Int(o, "next_preset_id", 1)
	// §10.1: a default motor_info when it is empty.
	if len(m.TechNodes) == 0 && len(m.TaskNodes) == 0 && len(m.OwnedSkins) == 0 && len(m.OwnedStickers) == 0 &&
		len(m.OwnedDecorations) == 0 && len(m.OwnedFrames) == 0 {
		m.seedDefaults(t)
	}
}

func (m *PlayerMotorcycle) seedDefaults(t *data.DataTables) {
	if len(t.MotorLvl) > 0 {
		top := t.MotorLvl[0]
		for _, l := range t.MotorLvl[1:] {
			if l.Level > top.Level {
				top = l
			}
		}
		m.Level, m.Exp = top.Level, top.Exp
	}
	m.TreeInUse = 0
	if len(t.MotorTechTree) > 0 {
		m.TreeInUse = t.MotorTechTree[0].Id
	}
	for _, row := range t.MotorTech {
		lv := min(max(row.InitLevel, 0), int32(len(row.TechLv)))
		m.TechNodes = append(m.TechNodes, &MotorTechNode{ID: row.Id, TreeType: row.TreeType, Level: lv, Current: lv, Unlocked: true, Target: int32(len(row.TechLv))})
	}
	for _, row := range t.MotorTask {
		reward := max(row.RewardTpCount, 1)
		m.TaskNodes = append(m.TaskNodes, &MotorTaskNode{ID: row.Id, TreeType: row.TreeType, Type: 1, Current: 1, Target: 1, Rewarded: reward, MaxReward: reward})
	}
	var defaultSkin int32
	for _, row := range t.MotorSkin {
		if row.Id == 0 {
			continue
		}
		m.OwnedSkins = append(m.OwnedSkins, row.Id)
		if row.DefaultFlag {
			defaultSkin = row.Id
		}
	}
	m.EquippedSkin = defaultSkin
	if defaultSkin == 0 && len(m.OwnedSkins) > 0 {
		m.EquippedSkin = m.OwnedSkins[0]
	}
	seen := map[int32]bool{}
	addSticker := func(ids []int32) {
		for _, id := range ids {
			if id != 0 && !seen[id] {
				seen[id] = true
				m.OwnedStickers = append(m.OwnedStickers, id)
			}
		}
	}
	for _, row := range t.MotorLoadProject {
		addSticker(row.Sticker)
	}
	for _, row := range t.MotorGeneralPreview {
		addSticker(row.Sticker)
	}
	for _, row := range t.MotorDecalIp {
		addSticker(row.IpStickerList)
	}
	for _, row := range t.MotorLinkageIp {
		addSticker(row.IpStickerList)
	}
	decs := map[int32]bool{}
	for _, row := range t.MotorDecorations {
		if row.Id != 0 && !decs[row.Id] {
			decs[row.Id] = true
			m.OwnedDecorations = append(m.OwnedDecorations, row.Id)
		}
	}
	frames := map[int32]bool{}
	var defaultFrame int32
	for _, row := range t.MotorFrame {
		if row.Id == 0 {
			continue
		}
		if !frames[row.Id] {
			frames[row.Id] = true
			m.OwnedFrames = append(m.OwnedFrames, row.Id)
		}
		if row.DefaultFlag {
			defaultFrame = row.Id
		}
	}
	m.EquippedFrame = defaultFrame
	if defaultFrame == 0 && len(m.OwnedFrames) > 0 {
		m.EquippedFrame = m.OwnedFrames[0]
	}
	if len(m.EquippedStickers) == 0 && len(t.MotorStickerPart) > 0 {
		m.EquippedStickers = make([]int32, len(t.MotorStickerPart))
	}
	if len(m.EquippedDecorations) == 0 && len(t.MotorDecorationsPart) > 0 {
		m.EquippedDecorations = make([]int32, len(t.MotorDecorationsPart))
	}
	if len(m.TechNodes) > 0 || len(m.TaskNodes) > 0 || len(m.OwnedSkins) > 0 || len(m.OwnedStickers) > 0 ||
		len(m.OwnedDecorations) > 0 || len(m.OwnedFrames) > 0 {
		m.Save()
	}
}

func zInts(v []int32) *zon.Value { return save.IntList(v) }

func zN(v int32) *zon.Value { return zon.Int(int64(v)) }

func (m *PlayerMotorcycle) Save() {
	var tech, task, presets []*zon.Value
	for _, t := range m.TechNodes {
		tech = append(tech, zon.NewStruct(zon.F("id", zN(t.ID)), zon.F("tree_type", zN(t.TreeType)), zon.F("level", zN(t.Level)),
			zon.F("unlocked", zon.BoolValue(t.Unlocked)), zon.F("current", zN(t.Current)), zon.F("target", zN(t.Target))))
	}
	for _, t := range m.TaskNodes {
		task = append(task, zon.NewStruct(zon.F("id", zN(t.ID)), zon.F("tree_type", zN(t.TreeType)), zon.F("type", zN(t.Type)),
			zon.F("current", zN(t.Current)), zon.F("target", zN(t.Target)), zon.F("rewarded", zN(t.Rewarded)),
			zon.F("wait_reward", zN(t.WaitReward)), zon.F("max_reward", zN(t.MaxReward))))
	}
	for _, p := range m.Presets {
		presets = append(presets, zon.NewStruct(zon.F("id", zN(p.ID)), zon.F("name", zon.Str(p.Name)), zon.F("skin", zN(p.Skin)),
			zon.F("stickers", zInts(p.Stickers)), zon.F("decorations", zInts(p.Decorations)), zon.F("frame", zN(p.Frame))))
	}
	m.original = save.Merge(m.original,
		zon.F("level", zN(m.Level)), zon.F("exp", zN(m.Exp)), zon.F("rewarded_max_level", zN(m.RewardedMaxLevel)),
		zon.F("tree_in_use", zN(m.TreeInUse)), zon.F("daily_exp_gain", zN(m.DailyExpGain)), zon.F("daily_exp_limit", zN(m.DailyExpLimit)),
		zon.F("tech_nodes", zon.NewList(tech...)), zon.F("task_nodes", zon.NewList(task...)),
		zon.F("owned_skins", zInts(m.OwnedSkins)), zon.F("owned_stickers", zInts(m.OwnedStickers)),
		zon.F("owned_decorations", zInts(m.OwnedDecorations)), zon.F("owned_frames", zInts(m.OwnedFrames)),
		zon.F("equipped_skin", zN(m.EquippedSkin)), zon.F("equipped_frame", zN(m.EquippedFrame)),
		zon.F("equipped_stickers", zInts(m.EquippedStickers)), zon.F("equipped_decorations", zInts(m.EquippedDecorations)),
		zon.F("presets", zon.NewList(presets...)), zon.F("next_preset_id", zN(m.NextPresetID)))
	m.files.Write("motor_info", m.original)
}

func (m *PlayerMotorcycle) ToEquippedPb() *protocol.MotorOutlookEquippedPb {
	return &protocol.MotorOutlookEquippedPb{SkinEquipped: m.EquippedSkin, StickerEquipped: slices.Clone(m.EquippedStickers),
		DecorationsEquipped: slices.Clone(m.EquippedDecorations), FrameEquipped: m.EquippedFrame}
}

func (m *PlayerMotorcycle) ToPresetsPb() *protocol.MotorOutlookPlayerPresetPb {
	pb := &protocol.MotorOutlookPlayerPresetPb{}
	for _, p := range m.Presets {
		pb.Plan = append(pb.Plan, p.ToPb())
	}
	return pb
}

func (m *PlayerMotorcycle) techTreePb(treeID int32) *protocol.MotorTechOneTreePb {
	tree := &protocol.MotorTechOneTreePb{TreeId: treeID}
	for _, n := range m.TechNodes {
		if n.TreeType == treeID {
			tree.Tech = append(tree.Tech, n.ToPb())
		}
	}
	return tree
}

func (m *PlayerMotorcycle) taskTreePb(treeID int32) *protocol.MotorTaskTreePb {
	tree := &protocol.MotorTaskTreePb{TreeId: treeID, TpRewarded: 0}
	for _, n := range m.TaskNodes {
		if n.TreeType == treeID {
			tree.Tasks = append(tree.Tasks, n.ToPb())
		}
	}
	return tree
}

// FindMotorEntity is the scene's motorcycle: the player entity's motor follower, else the
// scene's vehicle, else the first vehicle entity.
func FindMotorEntity(sc *world.Scene, wd *world.WorldData) *world.Entity {
	if sc == nil {
		return nil
	}
	if pe := sc.Get(sc.PlayerEntityID); pe != nil {
		for _, c := range pe.Components {
			f := c.GetPlayerFollowersComponentPb()
			if f == nil {
				continue
			}
			for _, fl := range f.ListPlayerFollowerPb {
				if fl.Type == protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerMotor && fl.EntityId != 0 {
					if mo := sc.Get(fl.EntityId); mo != nil && (wd.VehicleTemplateID == nil || mo.ConfigID == *wd.VehicleTemplateID) {
						return mo
					}
				}
			}
		}
	}
	if sc.VehicleID != 0 {
		if v := sc.Get(sc.VehicleID); v != nil {
			return v
		}
	}
	for _, e := range sc.Entities() {
		if e.IsVehicle || (wd.VehicleTemplateID != nil && e.ConfigID == *wd.VehicleTemplateID) {
			return e
		}
	}
	return nil
}

func componentIndex(e *world.Entity, has func(*protocol.EntityComponentPb) bool) int {
	return slices.IndexFunc(e.Components, has)
}

func refreshMotorBornBuffs(s *PlayerSession) {
	motor := FindMotorEntity(s.Scene, s.Data)
	if motor == nil {
		return
	}
	var buffs []int64
	for _, b := range world.VehicleBuffs {
		if _, ok := s.Data.Tables.BuffById[float64(b)]; ok {
			buffs = append(buffs, b)
		}
	}
	if i := componentIndex(motor, func(c *protocol.EntityComponentPb) bool { return c.GetFightBuffComponent() != nil }); i >= 0 {
		motor.Components[i] = world.FightBuffs(s.Scene, motor.ID, buffs)
	}
}

// ----- §10.3 info, creation, tech and rewards -----

func (m *PlayerMotorcycle) HandleInfo(s *PlayerSession, _ *protocol.MotorInfoRequest) *protocol.MotorInfoResponse {
	m.EnsureInitialized(s)
	motor := &protocol.MotorPb{MotorLevel: m.Level, MotorExp: m.Exp, MotorRewardedLvMax: m.RewardedMaxLevel, TreeInUse: m.TreeInUse,
		MotorExpLimitGainDaily: m.DailyExpGain, MotorExpMonsterDropDailyLimit: m.DailyExpLimit}
	var trees []int32
	for _, n := range m.TechNodes {
		if !slices.Contains(trees, n.TreeType) {
			trees = append(trees, n.TreeType)
		}
	}
	for _, id := range trees {
		motor.UnlockedTree = append(motor.UnlockedTree, m.techTreePb(id))
	}
	trees = nil
	for _, n := range m.TaskNodes {
		if !slices.Contains(trees, n.TreeType) {
			trees = append(trees, n.TreeType)
		}
	}
	for _, id := range trees {
		motor.TaskTrees = append(motor.TaskTrees, m.taskTreePb(id))
	}
	return &protocol.MotorInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Motor: motor}
}

func idTimes(ids []int32) []*protocol.MotorOutlookIdTimePairPb {
	var out []*protocol.MotorOutlookIdTimePairPb
	for _, id := range ids {
		out = append(out, &protocol.MotorOutlookIdTimePairPb{Id: id, OpenTime: 0})
	}
	return out
}

func (m *PlayerMotorcycle) HandleOutlookInfo(s *PlayerSession, _ *protocol.MotorOutlookInfoRequest) *protocol.MotorOutlookInfoResponse {
	m.EnsureInitialized(s)
	s.Client.Push(&protocol.MotorOutlookRegionInfoNotify{MotorOutlookRegion: &protocol.MotorOutlookRegionPb{
		MotorSticker: idTimes(m.OwnedStickers), MotorDecoration: idTimes(m.OwnedDecorations), MotorFrame: idTimes(m.OwnedFrames),
	}})
	return &protocol.MotorOutlookInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MotorOutlook: &protocol.MotorOutlookPb{
		MotorOutlookOwned: &protocol.MotorOutlookOwnedPb{SkinOwned: slices.Clone(m.OwnedSkins), StickerOwned: slices.Clone(m.OwnedStickers),
			DecorationsOwned: slices.Clone(m.OwnedDecorations), FrameOwned: slices.Clone(m.OwnedFrames)},
		MotorOutlookEquipped: m.ToEquippedPb(),
		MotorOutlookPreset:   m.ToPresetsPb(),
	}}
}

func (m *PlayerMotorcycle) HandleTechLevelUp(s *PlayerSession, r *protocol.MotorTechLevelUpRequest) *protocol.MotorTechLevelUpResponse {
	m.EnsureInitialized(s)
	var node *MotorTechNode
	for _, n := range m.TechNodes {
		if n.ID == r.TechId {
			node = n
			break
		}
	}
	if node == nil {
		return &protocol.MotorTechLevelUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorTechCfgNotFound}
	}
	if node.Level < node.Target {
		node.Level++
		node.Current = node.Level
		node.Unlocked = true
		m.Save()
	}
	refreshMotorBornBuffs(s)
	tree := m.techTreePb(node.TreeType)
	s.Client.Push(&protocol.MotorLockedTechUpdateNotify{TreeId: node.TreeType, Tech: tree.Tech})
	return &protocol.MotorTechLevelUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Tree: tree}
}

func (m *PlayerMotorcycle) HandleTechTreeSwitch(s *PlayerSession, r *protocol.MotorTechTreeSwitchRequest) *protocol.MotorTechTreeSwitchResponse {
	m.EnsureInitialized(s)
	m.TreeInUse = r.TreeId
	m.Save()
	refreshMotorBornBuffs(s)
	return &protocol.MotorTechTreeSwitchResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, TreeInUse: m.TreeInUse}
}

func (m *PlayerMotorcycle) HandleLevelOneKeyReward(s *PlayerSession, _ *protocol.MotorLevelOneKeyRewardRequest) *protocol.MotorLevelOneKeyRewardResponse {
	m.EnsureInitialized(s)
	m.RewardedMaxLevel = m.Level
	m.Save()
	return &protocol.MotorLevelOneKeyRewardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MotorRewardedLvMax: m.RewardedMaxLevel}
}

func (m *PlayerMotorcycle) HandleTaskOneKeyReward(s *PlayerSession, r *protocol.MotorTaskOneKeyRewardRequest) *protocol.MotorTaskOneKeyRewardResponse {
	m.EnsureInitialized(s)
	matched := false
	var lastTree int32
	for _, id := range r.TaskIds {
		var node *MotorTaskNode
		for _, n := range m.TaskNodes {
			if n.ID == id {
				node = n
				break
			}
		}
		if node == nil {
			continue
		}
		lastTree = node.TreeType
		matched = true
		if node.WaitReward > 0 {
			node.Rewarded += node.WaitReward
			node.WaitReward = 0
		}
	}
	if matched {
		m.Save()
		s.Client.Push(&protocol.MotorTaskUpdateNotify{Task: m.taskTreePb(lastTree).Tasks})
	}
	return &protocol.MotorTaskOneKeyRewardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- §10.4 skins, outlook and presets -----

func normalizeOutlook(t *data.DataTables, prevFrame int32, prevStickers, prevDecorations []int32,
	reqFrame int32, reqStickers, reqDecorations []int32) (protocol.ErrorCode, int32, []int32, []int32) {
	frame := reqFrame
	if frame == 0 {
		frame = prevFrame
	}
	if frame != 0 && !slices.ContainsFunc(t.MotorFrame, func(f *data.MotorFrame) bool { return f.Id == frame }) {
		return protocol.ErrorCode_ErrorCode_MotorFrameCfgNotFound, 0, nil, nil
	}
	stickerSlots := len(t.MotorStickerPart)
	stickers := make([]int32, stickerSlots)
	copy(stickers, prevStickers)
	for i, id := range reqStickers {
		if id == 0 {
			if i < stickerSlots {
				stickers[i] = 0
			}
			continue
		}
		idx := slices.IndexFunc(t.MotorSticker, func(x *data.MotorSticker) bool { return x.Id == id })
		if idx < 0 {
			return protocol.ErrorCode_ErrorCode_MotorStickerCfgNotFound, 0, nil, nil
		}
		cfg := t.MotorSticker[idx]
		if cfg.PartId < 1 || int(cfg.PartId) > stickerSlots {
			return protocol.ErrorCode_ErrorCode_MotorStickerPartIdError, 0, nil, nil
		}
		stickers[cfg.PartId-1] = id
	}
	decSlots := len(t.MotorDecorationsPart)
	decs := make([]int32, decSlots)
	copy(decs, prevDecorations)
	for i, id := range reqDecorations {
		if id == 0 {
			if i < decSlots {
				decs[i] = 0
			}
			continue
		}
		idx := slices.IndexFunc(t.MotorDecorations, func(x *data.MotorDecorations) bool { return x.Id == id })
		if idx < 0 {
			return protocol.ErrorCode_ErrorCode_MotorDecorationsCfgNotFound, 0, nil, nil
		}
		cfg := t.MotorDecorations[idx]
		if cfg.PartId < 1 || int(cfg.PartId) > decSlots {
			return protocol.ErrorCode_ErrorCode_MotorDecorationsPartIdError, 0, nil, nil
		}
		decs[cfg.PartId-1] = id
	}
	return protocol.ErrorCode_ErrorCode_Success, frame, stickers, decs
}

func (m *PlayerMotorcycle) applyOutlookToEntity(s *PlayerSession, frameChanged bool) {
	m.syncToPlayerState(s.State)
	eq := m.ToEquippedPb()
	s.Client.Push(&protocol.MotorOutlookEquippedChangeNotify{MotorOutlookEquipped: eq})
	sc := s.Scene
	motor := FindMotorEntity(sc, s.Data)
	if motor == nil {
		return
	}
	outlook := world.C(&protocol.EntityComponentPb_MotorOutlookComponentPb{MotorOutlookComponentPb: proto.Clone(eq).(*protocol.MotorOutlookEquippedPb)})
	if i := componentIndex(motor, func(c *protocol.EntityComponentPb) bool { return c.GetMotorOutlookComponentPb() != nil }); i >= 0 {
		motor.Components[i] = outlook
	} else {
		motor.Components = append(motor.Components, outlook)
	}
	if !frameChanged {
		s.Client.Push(&protocol.EntityMotorOutlookChangeNotify{EntityId: motor.ID, MotorOutlookEquipped: proto.Clone(eq).(*protocol.MotorOutlookEquippedPb)})
		return
	}
	var companions []*world.Entity
	for _, e := range sc.Entities() {
		if slices.ContainsFunc(e.Components, func(c *protocol.EntityComponentPb) bool {
			sm := c.GetSummonerComponent()
			return sm != nil && sm.SummonerId == motor.ID
		}) {
			companions = append(companions, e)
		}
	}
	rm := &protocol.EntityRemoveNotify{IsRemove: true, RemoveInfos: []*protocol.EntityRemoveInfo{{EntityId: motor.ID}}}
	for _, c := range companions {
		rm.RemoveInfos = append(rm.RemoveInfos, &protocol.EntityRemoveInfo{EntityId: c.ID})
	}
	s.Client.Push(rm)
	add := &protocol.EntityAddNotify{IsAdd: false, EntityPbs: []*protocol.EntityPb{motor.ToPb()}}
	for _, c := range companions {
		add.EntityPbs = append(add.EntityPbs, c.ToPb())
	}
	s.Client.Push(add)
}

func (m *PlayerMotorcycle) HandleUseSkin(s *PlayerSession, r *protocol.MotorUseSkinRequest) *protocol.MotorUseSkinResponse {
	m.EnsureInitialized(s)
	idx := slices.IndexFunc(s.Data.Tables.MotorSkin, func(x *data.MotorSkin) bool { return x.Id == r.SkinId })
	if idx < 0 {
		return &protocol.MotorUseSkinResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookNotOwned}
	}
	skin := s.Data.Tables.MotorSkin[idx]
	code, frame, stickers, decs := normalizeOutlook(s.Data.Tables, m.EquippedFrame, nil, nil, skin.BindFrame, skin.BindSticker, skin.BindDecorations)
	if code != protocol.ErrorCode_ErrorCode_Success {
		return &protocol.MotorUseSkinResponse{ErrorCode: code}
	}
	changed := frame != m.EquippedFrame
	m.EquippedSkin, m.EquippedFrame, m.EquippedStickers, m.EquippedDecorations = skin.Id, frame, stickers, decs
	m.Save()
	m.applyOutlookToEntity(s, changed)
	return &protocol.MotorUseSkinResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (m *PlayerMotorcycle) HandleChangeOutlook(s *PlayerSession, r *protocol.MotorChangeOutlookRequest) *protocol.MotorChangeOutlookResponse {
	m.EnsureInitialized(s)
	code, frame, stickers, decs := normalizeOutlook(s.Data.Tables, m.EquippedFrame, m.EquippedStickers, m.EquippedDecorations,
		r.FrameEquipped, r.StickerEquipped, r.DecorationsEquipped)
	if code != protocol.ErrorCode_ErrorCode_Success {
		return &protocol.MotorChangeOutlookResponse{ErrorCode: code}
	}
	changed := frame != m.EquippedFrame
	m.EquippedFrame, m.EquippedStickers, m.EquippedDecorations = frame, stickers, decs
	m.Save()
	m.applyOutlookToEntity(s, changed)
	return &protocol.MotorChangeOutlookResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (m *PlayerMotorcycle) skinOr(skin int32) int32 {
	if skin == 0 {
		return m.EquippedSkin
	}
	return skin
}

func (m *PlayerMotorcycle) HandleCreatePreset(s *PlayerSession, r *protocol.MotorOutlookCreatePresetRequest) *protocol.MotorOutlookCreatePresetResponse {
	m.EnsureInitialized(s)
	p := r.Preset
	if p == nil {
		return &protocol.MotorOutlookCreatePresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetInputInvalid}
	}
	m.Presets = append(m.Presets, &MotorPreset{ID: m.NextPresetID, Name: r.Name, Skin: m.skinOr(p.SkinEquipped),
		Stickers: slices.Clone(p.StickerEquipped), Decorations: slices.Clone(p.DecorationsEquipped), Frame: p.FrameEquipped})
	m.NextPresetID++
	m.Save()
	return &protocol.MotorOutlookCreatePresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MotorOutlookPreset: m.ToPresetsPb()}
}

func (m *PlayerMotorcycle) HandleEditPreset(s *PlayerSession, r *protocol.MotorOutlookEditPresetRequest) *protocol.MotorOutlookEditPresetResponse {
	m.EnsureInitialized(s)
	plan := r.PresetPlan
	if plan == nil || plan.Preset == nil {
		return &protocol.MotorOutlookEditPresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetInputInvalid}
	}
	idx := slices.IndexFunc(m.Presets, func(x *MotorPreset) bool { return x.ID == plan.Id })
	if idx < 0 {
		return &protocol.MotorOutlookEditPresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetNotExist}
	}
	e, p := m.Presets[idx], plan.Preset
	e.Name, e.Skin, e.Stickers, e.Decorations, e.Frame = plan.Name, m.skinOr(p.SkinEquipped), slices.Clone(p.StickerEquipped), slices.Clone(p.DecorationsEquipped), p.FrameEquipped
	m.Save()
	return &protocol.MotorOutlookEditPresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MotorOutlookPreset: m.ToPresetsPb()}
}

func (m *PlayerMotorcycle) HandleDeletePreset(s *PlayerSession, r *protocol.MotorOutlookDeletePresetRequest) *protocol.MotorOutlookDeletePresetResponse {
	m.EnsureInitialized(s)
	n := len(m.Presets)
	m.Presets = slices.DeleteFunc(m.Presets, func(x *MotorPreset) bool { return x.ID == r.Id })
	if len(m.Presets) == n {
		return &protocol.MotorOutlookDeletePresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetNotExist}
	}
	m.Save()
	return &protocol.MotorOutlookDeletePresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MotorOutlookPreset: m.ToPresetsPb()}
}

// ----- §10.5 ride-sharing -----

func HandleVehicleEnterSharing(s *PlayerSession, r *protocol.VehicleEnterSharingRequest) *protocol.VehicleEnterSharingResponse {
	s.Client.Push(&protocol.VehicleShareNotify{PlayerId: s.State.PlayerID(), RideMode: r.RideMode, IsEnter: r.IsEnter,
		Reason: protocol.VehicleShareReason_VehicleShareReason_ClientRequest})
	return &protocol.VehicleEnterSharingResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ResolvePassengerTemplate is the Passenger NPC template of a role: the last whose blueprint
// ends in _<role>, holds _<role>_ or the role's token from its UiScenePerformanceABP path.
func ResolvePassengerTemplate(wd *world.WorldData, roleID int32) *data.TemplateConfig {
	token := ""
	if ri, ok := wd.Tables.RoleInfoById[roleID]; ok && ri.UiScenePerformanceABP != "" {
		var segs []string
		for _, p := range strings.Split(ri.UiScenePerformanceABP, "/") {
			if p != "" {
				segs = append(segs, p)
			}
		}
		for i := 1; i < len(segs); i++ {
			if len(segs[i]) >= 4 && strings.EqualFold(segs[i][:4], "R2T1") && len(segs[i-1]) >= 3 {
				token = segs[i-1]
				break
			}
		}
	}
	suffix := "_" + strconv.Itoa(int(roleID))
	infix := suffix + "_"
	var matched *data.TemplateConfig
	for _, t := range wd.Tables.TemplateConfig {
		if t.BlueprintType == "" || !strings.Contains(t.BlueprintType, "Passenger") {
			continue
		}
		if bp := wd.Blueprint(t.BlueprintType); bp == nil || bp.EntityLogic != "Npc" {
			continue
		}
		if strings.HasSuffix(t.BlueprintType, suffix) || strings.Contains(t.BlueprintType, infix) ||
			(token != "" && strings.Contains(strings.ToLower(t.BlueprintType), strings.ToLower(token))) {
			matched = t
		}
	}
	return matched
}

func (m *PlayerMotorcycle) findSpawnedPassenger(sc *world.Scene) *world.Entity {
	roles := sc.RoleEntities.Values()
	if m.passengerNetID != 0 {
		if p := sc.Get(m.passengerNetID); p != nil && !slices.Contains(roles, p.ID) {
			return p
		}
	}
	for _, e := range sc.Entities() {
		if !slices.Contains(roles, e.ID) && slices.ContainsFunc(e.Components, func(c *protocol.EntityComponentPb) bool { return c.GetVehiclePassengerComponentPb() != nil }) {
			return e
		}
	}
	return nil
}

func (m *PlayerMotorcycle) HandleVehicleChangePassenger(s *PlayerSession, r *protocol.VehicleChangePassengerRequest) *protocol.VehicleChangePassengerResponse {
	if r.RoleId <= 0 || !s.Combat.Player.Roles.Has(r.RoleId) {
		return &protocol.VehicleChangePassengerResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	sc := s.Scene
	motor := FindMotorEntity(sc, s.Data)
	var pe *world.Entity
	if sc != nil {
		pe = sc.Get(sc.PlayerEntityID)
	}
	if sc == nil || motor == nil || pe == nil {
		return &protocol.VehicleChangePassengerResponse{ErrorCode: protocol.ErrorCode_ErrorCode_InternalError}
	}
	pid := s.State.PlayerID()
	// The motorcycle's companion entity exists.
	var follow *protocol.FollowEntityComponentPb
	for _, c := range motor.Components {
		if f := c.GetFollowEntityComponentPb(); f != nil {
			follow = f
			break
		}
	}
	if follow == nil || follow.EntityId == 0 || sc.Get(follow.EntityId) == nil {
		companion := sc.Add(&world.Entity{ConfigID: world.CompanionTemplate, ConfigType: world.ConfigTemplate, EntityType: world.TypeMonster, Pos: sc.PlayerPos, PlayerID: pid})
		var buffs []int64
		if sm, ok := s.Data.Tables.SummonCfgById[world.CompanionSummonCfg]; ok {
			for _, b := range sm.BornBuffId {
				buffs = append(buffs, int64(b))
			}
		}
		companion.Components = append(companion.Components,
			world.Attributes(s.Data.BaseAttributesOf(world.CompanionProperty), nil),
			world.FightBuffs(sc, companion.ID, buffs),
			world.C(&protocol.EntityComponentPb_CombineEntityInfoPb{CombineEntityInfoPb: &protocol.CombineEntityInfoPb{}}),
			world.C(&protocol.EntityComponentPb_EquipComponent{EquipComponent: &protocol.EquipComponentPb{}}),
			world.C(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: &protocol.LogicStateComponentPb{}}),
			world.C(&protocol.EntityComponentPb_MonsterAiComponentPb{MonsterAiComponentPb: &protocol.MonsterAiComponentPb{CombatMessageId: combat.AiContextID}}),
			world.C(&protocol.EntityComponentPb_SummonerComponent{SummonerComponent: &protocol.SummonerComponentPb{
				SummonerId: motor.ID, SummonCfgId: world.CompanionSummonCfg, PlayerId: pid,
				Type: protocol.ESummonType_ESummonType_ESummonTypeConcomitantMotorcycle,
			}}),
			world.C(&protocol.EntityComponentPb_PlayerFollowerSelfComponentPb{PlayerFollowerSelfComponentPb: &protocol.PlayerFollowerSelfComponentPb{
				PlayerEntityId: pe.ID, SummonConfigId: world.CompanionSummonCfg,
			}}),
		)
		if follow != nil {
			follow.EntityId = companion.ID
		} else {
			motor.Components = append(motor.Components, world.C(&protocol.EntityComponentPb_FollowEntityComponentPb{FollowEntityComponentPb: &protocol.FollowEntityComponentPb{EntityId: companion.ID}}))
		}
		s.Client.Push(&protocol.EntityAddNotify{EntityPbs: []*protocol.EntityPb{companion.ToPb()}})
	}
	standUp := protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeSeatStandUp
	// The passenger who sat there gets off.
	if old := m.findSpawnedPassenger(sc); old != nil {
		s.Client.Push(&protocol.VehicleUpdateEntityNotify{EntityId: old.ID, VehicleId: motor.ID, SeatId: 1, IsGetOn: false, ExitType: standUp})
		sc.Remove(old.ID)
		m.passengerNetID = 0
		s.Client.Push(&protocol.EntityRemoveNotify{IsRemove: true, RemoveInfos: []*protocol.EntityRemoveInfo{{EntityId: old.ID}}})
	}
	tpl := ResolvePassengerTemplate(s.Data, r.RoleId)
	if tpl == nil {
		return &protocol.VehicleChangePassengerResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	}
	propID := int32(2)
	if o, ok := tpl.ComponentsData.(*data.Object); ok {
		if bi, ok := o.Get("BaseInfo").(*data.Object); ok {
			if v, ok := jsonIntOf(bi.Get("EntityPropertyId")); ok {
				propID = v
			}
		}
	}
	passenger := sc.Add(&world.Entity{ConfigID: tpl.Id, ConfigType: world.ConfigTemplate, EntityType: world.TypeNpc,
		Pos: sc.PlayerPos, Rot: sc.PlayerRot, PlayerID: pid, IsVisible: true})
	passenger.Components = append(passenger.Components,
		world.Attributes(s.Data.BaseAttributesOf(propID), nil),
		world.C(&protocol.EntityComponentPb_VehiclePassengerComponentPb{VehiclePassengerComponentPb: &protocol.VehiclePassengerComponentPb{VehicleId: motor.ID, SeatId: 1}}),
	)
	m.passengerNetID = passenger.ID
	s.Client.Push(&protocol.EntityAddNotify{EntityPbs: []*protocol.EntityPb{passenger.ToPb()}})
	s.Client.Push(&protocol.VehiclePassengerUpdateNotify{PlayerId: pid, RoleId: r.RoleId, SeatId: 1, EntityId: passenger.ID})
	s.Client.Push(&protocol.VehicleUpdateEntityNotify{EntityId: passenger.ID, VehicleId: motor.ID, SeatId: 1, IsGetOn: true, ExitType: standUp})
	return &protocol.VehicleChangePassengerResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (m *PlayerMotorcycle) HandleVehicleRemovePassenger(s *PlayerSession, _ *protocol.VehicleRemovePassengerRequest) *protocol.VehicleRemovePassengerResponse {
	if sc := s.Scene; sc != nil {
		if p := m.findSpawnedPassenger(sc); p != nil {
			if motor := FindMotorEntity(sc, s.Data); motor != nil {
				s.Client.Push(&protocol.VehicleUpdateEntityNotify{EntityId: p.ID, VehicleId: motor.ID, SeatId: 1, IsGetOn: false,
					ExitType: protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeSeatStandUp})
			}
			s.Client.Push(&protocol.VehiclePassengerUpdateNotify{PlayerId: s.State.PlayerID(), RoleId: 0, SeatId: 1, EntityId: p.ID})
			sc.Remove(p.ID)
			m.passengerNetID = 0
			s.Client.Push(&protocol.EntityRemoveNotify{IsRemove: true, RemoveInfos: []*protocol.EntityRemoveInfo{{EntityId: p.ID}}})
		}
	}
	return &protocol.VehicleRemovePassengerResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// registerMotorcycle registers the motorcycle and ride-sharing requests (§10).
func (g *WorldGame) registerMotorcycle(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorInfoRequest) *protocol.MotorInfoResponse {
		return p.Motorcycle().HandleInfo(p, r)
	},
		func(*protocol.MotorInfoRequest) *protocol.MotorInfoResponse {
			return &protocol.MotorInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorOutlookInfoRequest) *protocol.MotorOutlookInfoResponse {
		return p.Motorcycle().HandleOutlookInfo(p, r)
	}, func(*protocol.MotorOutlookInfoRequest) *protocol.MotorOutlookInfoResponse {
		return &protocol.MotorOutlookInfoResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.MotorCreateRequest) proto.Message {
		return &protocol.MotorCreateResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorTechLevelUpRequest) *protocol.MotorTechLevelUpResponse {
		return p.Motorcycle().HandleTechLevelUp(p, r)
	}, func(*protocol.MotorTechLevelUpRequest) *protocol.MotorTechLevelUpResponse {
		return &protocol.MotorTechLevelUpResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorTechCfgNotFound}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorTechTreeSwitchRequest) *protocol.MotorTechTreeSwitchResponse {
		return p.Motorcycle().HandleTechTreeSwitch(p, r)
	}, func(*protocol.MotorTechTreeSwitchRequest) *protocol.MotorTechTreeSwitchResponse {
		return &protocol.MotorTechTreeSwitchResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorLevelOneKeyRewardRequest) *protocol.MotorLevelOneKeyRewardResponse {
		return p.Motorcycle().HandleLevelOneKeyReward(p, r)
	}, func(*protocol.MotorLevelOneKeyRewardRequest) *protocol.MotorLevelOneKeyRewardResponse {
		return &protocol.MotorLevelOneKeyRewardResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorTaskOneKeyRewardRequest) *protocol.MotorTaskOneKeyRewardResponse {
		return p.Motorcycle().HandleTaskOneKeyReward(p, r)
	}, func(*protocol.MotorTaskOneKeyRewardRequest) *protocol.MotorTaskOneKeyRewardResponse {
		return &protocol.MotorTaskOneKeyRewardResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorUseSkinRequest) *protocol.MotorUseSkinResponse {
		return p.Motorcycle().HandleUseSkin(p, r)
	},
		func(*protocol.MotorUseSkinRequest) *protocol.MotorUseSkinResponse {
			return &protocol.MotorUseSkinResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookNotOwned}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorChangeOutlookRequest) *protocol.MotorChangeOutlookResponse {
		return p.Motorcycle().HandleChangeOutlook(p, r)
	}, func(*protocol.MotorChangeOutlookRequest) *protocol.MotorChangeOutlookResponse {
		return &protocol.MotorChangeOutlookResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorOutlookCreatePresetRequest) *protocol.MotorOutlookCreatePresetResponse {
		return p.Motorcycle().HandleCreatePreset(p, r)
	}, func(*protocol.MotorOutlookCreatePresetRequest) *protocol.MotorOutlookCreatePresetResponse {
		return &protocol.MotorOutlookCreatePresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetInputInvalid}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorOutlookEditPresetRequest) *protocol.MotorOutlookEditPresetResponse {
		return p.Motorcycle().HandleEditPreset(p, r)
	}, func(*protocol.MotorOutlookEditPresetRequest) *protocol.MotorOutlookEditPresetResponse {
		return &protocol.MotorOutlookEditPresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetInputInvalid}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MotorOutlookDeletePresetRequest) *protocol.MotorOutlookDeletePresetResponse {
		return p.Motorcycle().HandleDeletePreset(p, r)
	}, func(*protocol.MotorOutlookDeletePresetRequest) *protocol.MotorOutlookDeletePresetResponse {
		return &protocol.MotorOutlookDeletePresetResponse{ErrorCode: protocol.ErrorCode_ErrorCode_MotorOutlookPresetNotExist}
	})
	reqOn(h, g, HandleVehicleEnterSharing, func(*protocol.VehicleEnterSharingRequest) *protocol.VehicleEnterSharingResponse {
		return &protocol.VehicleEnterSharingResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.VehicleChangePassengerRequest) *protocol.VehicleChangePassengerResponse {
		return p.Motorcycle().HandleVehicleChangePassenger(p, r)
	}, func(*protocol.VehicleChangePassengerRequest) *protocol.VehicleChangePassengerResponse {
		return &protocol.VehicleChangePassengerResponse{ErrorCode: protocol.ErrorCode_ErrorCode_RequestParamError}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.VehicleRemovePassengerRequest) *protocol.VehicleRemovePassengerResponse {
		return p.Motorcycle().HandleVehicleRemovePassenger(p, r)
	}, func(*protocol.VehicleRemovePassengerRequest) *protocol.VehicleRemovePassengerResponse {
		return &protocol.VehicleRemovePassengerResponse{ErrorCode: ok}
	})
}
