// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// LoginSlots are the 35 login-data pushes in the captured order (specs/enter-world.md §3), except
// that StorageInfoNotify goes first (R-154): BasicInfoNotify sets the client's UID, which binds
// ActivityCache to the storage container of that moment, and StorageInfoNotify then rebuilds every
// container, leaving ActivityCache on an empty one (the newcomer splash showed at every login).
// Combat makes the role, item and data-bank ones; each slot takes combat's message of that type
// when it made one, else the session's (the systems', else an empty one). A combat push of a
// type no slot names goes out before ContextIdNotify.
var LoginSlots = []string{
	"StorageInfoNotify", "BasicInfoNotify", "DressPlayerTitleUpdateNotify", "NormalItemUpdateNotify", "WeaponSkinAddNotify", "FlyEquipAddNotify",
	"RoleFlyEquipNotify", "OrnamentInfoNotify", "RoleChangeUnlockNotify", "PbGetRoleListNotify",
	"RoleFavorListNotify", "RoleMotionListNotify", "RoleConfigInfoNotify", "FuncOpenNotify", "AreaTerminalInfoNotify",
	"AdviceSettingNotify", "ControlInfoNotify", "InstDataNotify", "CalabashMsgNotify", "PhantomInteractionUnlockNotify",
	"CalabashLevelsRewardNotify", "ItemPkgOpenNotify", "BuffItemNotify", "EnergyUpdateNotify", "LevelPlayInfoNotify",
	"PlayerVarNotify", "RoguelikeCurrencyNotify", "PassiveSkillNotify", "MailInfosNotify", "SettingNotify",
	"TrackMoonIllustratedNotify", "TrackMoonTokenCoinNotify", "SilenceNpcNotify", "ContextIdNotify", "PushDataCompleteNotify",
}

type recorded struct {
	id protocol.MessageID
	m  proto.Message
}

// recorder keeps what combat pushes, with each message's id.
type recorder struct{ msgs []recorded }

func (r *recorder) Push(m proto.Message) {
	id, ok := protocol.IDOf(m)
	if !ok {
		panic("game: a login push has no message id")
	}
	r.msgs = append(r.msgs, recorded{id, m})
}

func msgName(m proto.Message) string { return string(m.ProtoReflect().Descriptor().Name()) }

// ownLoginPush is the session's message for a slot combat did not fill (nil: nothing).
func (p *PlayerSession) ownLoginPush(slot string) proto.Message {
	if m := p.Systems.OwnLoginPush(p, slot); m != nil {
		return m
	}
	switch slot {
	case "WeaponSkinAddNotify":
		return &protocol.WeaponSkinAddNotify{}
	case "FlyEquipAddNotify":
		return &protocol.FlyEquipAddNotify{}
	case "RoleFlyEquipNotify":
		return &protocol.RoleFlyEquipNotify{}
	case "OrnamentInfoNotify":
		return &protocol.OrnamentInfoNotify{}
	case "StorageInfoNotify":
		return p.ClientStorage().BuildNotify(p.Combat.Player.CosmeticInfo.ViewedOrnaments) // R-113
	case "RoleFavorListNotify":
		return &protocol.RoleFavorListNotify{}
	case "RoleMotionListNotify":
		return &protocol.RoleMotionListNotify{}
	case "FuncOpenNotify":
		n := &protocol.FuncOpenNotify{}
		for _, f := range p.Data.Tables.FunctionCondition {
			n.Func = append(n.Func, &protocol.Function{Id: f.FunctionId, Flag: 2})
		}
		return n
	case "AreaTerminalInfoNotify":
		return AreaTerminalInfoNotify(p.Data.Tables, p.Files) // R-50
	case "AdviceSettingNotify":
		return &protocol.AdviceSettingNotify{IsShow: false}
	case "ControlInfoNotify":
		return &protocol.ControlInfoNotify{}
	case "InstDataNotify":
		return &protocol.InstDataNotify{}
	case "PhantomInteractionUnlockNotify":
		return &protocol.PhantomInteractionUnlockNotify{}
	case "ItemPkgOpenNotify":
		return &protocol.ItemPkgOpenNotify{OpenPkg: []int32{0, 1, 2, 3, 4, 5, 6, 7}}
	case "BuffItemNotify":
		return &protocol.BuffItemNotify{}
	case "EnergyUpdateNotify":
		return &protocol.EnergyUpdateNotify{UpdateInfoList: []*protocol.EnergyInfo{{EnergyCount: 240, Id: 5}, {EnergyCount: 480, Id: 6}}}
	case "LevelPlayInfoNotify":
		return &protocol.LevelPlayInfoNotify{}
	case "PlayerVarNotify":
		return &protocol.PlayerVarNotify{}
	case "RoguelikeCurrencyNotify":
		return &protocol.RoguelikeCurrencyNotify{}
	case "PassiveSkillNotify":
		return &protocol.PassiveSkillNotify{}
	case "MailInfosNotify":
		return p.mailInfos() // R-102 (activities.md §5.1)
	case "SettingNotify":
		return &protocol.SettingNotify{}
	case "TrackMoonIllustratedNotify":
		return &protocol.TrackMoonIllustratedNotify{}
	case "TrackMoonTokenCoinNotify":
		return &protocol.TrackMoonTokenCoinNotify{TokenCoin: 0}
	case "SilenceNpcNotify":
		return &protocol.SilenceNpcNotify{}
	case "ContextIdNotify":
		return &protocol.ContextIdNotify{Id: 0}
	case "PushDataCompleteNotify":
		return &protocol.PushDataCompleteNotify{}
	}
	return nil
}

func (p *PlayerSession) pushLoginData() {
	g := &recorder{}
	p.Combat.PushLoginData(g)
	slots := map[string]bool{}
	for _, s := range LoginSlots {
		slots[s] = true
	}
	for _, slot := range LoginSlots {
		if slot == "ContextIdNotify" {
			for _, r := range g.msgs {
				if name := msgName(r.m); !slots[name] {
					common.Warn("scene", "login push "+name+" has no slot in the captured order; sent before ContextIdNotify")
					p.Client.PushID(r.id, r.m)
				}
			}
		}
		filled := false
		for _, r := range g.msgs {
			if msgName(r.m) == slot {
				if storage, ok := r.m.(*protocol.StorageInfoNotify); ok {
					storage.Infos = p.ClientStorage().Merge(storage.Infos)
				}
				p.Client.PushID(r.id, r.m)
				filled = true
			}
		}
		if filled {
			continue
		}
		if own := p.ownLoginPush(slot); own != nil {
			if id, ok := protocol.IDByName(slot); ok {
				p.Client.PushID(id, own)
			}
		}
	}
	p.ClaimMonthCard() // R-159: the daily reward; the client opens its popup at WorldDone
}

// pushSceneJoin sends the pushes of a scene join in the captured order (specs/enter-world.md §4).
func (p *PlayerSession) pushSceneJoin(s *world.Scene) {
	c := p.Client
	t := p.State.ExploreTools
	c.Push(&protocol.ExploreToolAllNotify{ExploreSkill: t.ActiveExploreSkill, SkillList: append([]int32(nil), t.UnlockedExploreSkills...)})
	c.Push(&protocol.ExploreSkillRouletteUpdateNotify{RouletteInfo: []*protocol.ExploreSkillRoulette{
		{SkillIds: Pad(t.Roulette), ExtraItemId: t.ExploreExtraItemID, ExploreSkill: t.ActiveExploreSkill},
		{SkillIds: Pad(t.FunctionRoulette), ExtraItemId: t.FunctionExtraItemID, ExploreSkill: t.ActiveFunctionSkill},
		{},
		{SkillIds: Pad(t.MotorcycleRoulette), ExtraItemId: t.MotorcycleExtraItemID, ExploreSkill: t.ActiveMotorcycleSkill},
	}})
	c.Push(&protocol.VisionExploreSkillNotify{ExploreSkill: 1001})
	c.Push(p.JoinSceneNotify(s))
	c.Push(&protocol.AfterJoinSceneNotify{})
	p.Systems.SyncOnJoin(p)
	c.Push(&protocol.SwitchBattleModeNotify{})
	if roads := p.Data.Roads(s.MapID); len(roads) > 0 {
		c.Push(&protocol.SceneRoadSyncNotify{InstanceId: s.InstanceID, EnabledRoads: append([]int32(nil), roads...)})
	}
	built := &protocol.InfrRoadPb{}
	for _, r := range p.Data.Tables.InfrRoadBuild {
		if r.DungeonId == world.RoadDungeon {
			built.Roads = append(built.Roads, &protocol.InfrOneRoad{RoadId: r.Id, Status: protocol.InfrStatusPb_InfrStatusPb_InfrStatusComplete})
		}
	}
	c.Push(&protocol.InfrRoadUpdateNotify{RoadInfo: built})
	real := p.Clock.RealMs()
	c.Push(&protocol.TimeCheckNotify{ClientTime: 0, ServerTime: real, ServerStopTime: real, SceneStopTime: real, FlowTime: s.Flow.Now(p.Clock.Ms())})
	c.Push(combat.BuildFormationAttrNotify())
	c.Push(p.Combat.Formation.BuildUpdateFormationNotify())
}

// JoinSceneNotify (specs/enter-world.md §4.1).
func (p *PlayerSession) JoinSceneNotify(s *world.Scene) *protocol.JoinSceneNotify {
	f := p.Formation()
	basic := &p.Combat.Player.BasicInfo.Attributes
	group := &protocol.PlayerSceneGroupData{Group: s.GroupType, CurRole: f.CurRole, LivingStatus: protocol.LivingStatus_LivingStatus_Alive}
	for _, slot := range f.FilledRoles() {
		group.FightRoleInfos = append(group.FightRoleInfos, &protocol.SceneRoleInformation{RoleId: slot.RoleID, EntityId: slot.EntityID, OnStageOutOfControl: slot.OnStageWithoutControl})
	}
	info := &protocol.SceneInformation{
		InstanceId: s.InstanceID, OwnerId: s.Instance.OwnerID, CurContextId: int64(p.State.PlayerID()),
		Mode: protocol.SceneMode_SceneMode_Single, SceneEntityId: 2,
		TimeInfo:   &protocol.SceneTimeInfo{Hour: s.Instance.Hour, Minute: s.Instance.Minute, OwnerTimeClockTimeSpan: s.Instance.OwnerClockTimeSpan},
		DataLayers: p.Data.DataLayers(s.InstanceID),
		PlayerInfos: []*protocol.ScenePlayerInformation{{
			PlayerId: p.State.PlayerID(), PlayerName: basic.Name, Level: basic.Level,
			Location: s.PlayerPos.ToPb(), Rotation: s.PlayerRot.ToPb(), Group: s.GroupType, CurRole: f.CurRole,
			PlayerPrefix: 1, PlayerGEIncHandle: 0, VehicleInfoPb: &protocol.VehicleInfoPb{}, GravityDirection: &protocol.Vector{},
			PlayerSceneGroupData: []*protocol.PlayerSceneGroupData{group},
		}},
		AoiData: &protocol.PlayerSceneAoiData{},
	}
	for _, e := range s.Entities() {
		info.AoiData.Entities = append(info.AoiData.Entities, e.ToPb())
	}
	// R-82c: the areas quests switched; the others start at Area.IsInitActived on the client.
	for _, a := range p.World.Areas.All() {
		info.AreaStates = append(info.AreaStates, &protocol.SceneAreaState{AreaId: a.AreaID, State: a.On})
	}
	return &protocol.JoinSceneNotify{MaxEntityId: 0, TransitionOption: &protocol.TransitionOptionPb{}, SceneInfo: info}
}
