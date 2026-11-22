// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"errors"

	"tidelab/internal/save"
	"tidelab/protocol"
)

// The entities made at every scene join, before JoinSceneNotify (specs/entities.md §4): the
// motorcycle, the player entity, the motorcycle's companion and the battle entity. The
// formation's roles with their visions and concomitants follow them (combat's formation makes
// those: entities.md §6, roles.md §5).
const (
	PlayerEntityTemplate = 14750001
	CompanionTemplate    = 36700001
	CompanionSummonCfg   = 24000030
	BattleEntityTemplate = 14000169
	VehicleProperty      = 90000015
	CompanionProperty    = 90000004
	PlayerEntityProperty = 390077025
	VehicleTag           = -849916103
)

var VehicleBuffs = []int64{3000000801, 3000020024}

// ErrNoVehicle: the data has no motorcycle template.
var ErrNoVehicle = errors.New("no ExploreTools row summons a Vehicle blueprint")

// MakeJoinEntities makes them in the scene, in order; it panics with ErrNoVehicle when the data
// has no motorcycle.
func MakeJoinEntities(scene *Scene, player *save.PlayerState, d *WorldData) {
	if d.VehicleTemplateID == nil {
		panic(ErrNoVehicle)
	}
	pid := player.PlayerID()
	vehicle := scene.Add(&Entity{ConfigID: *d.VehicleTemplateID, ConfigType: ConfigTemplate, EntityType: TypeVehicle,
		Pos: FarAway, PlayerID: pid, IsVehicle: true})
	playerEntity := scene.Add(&Entity{ConfigID: PlayerEntityTemplate, ConfigType: ConfigTemplate, EntityType: TypePlayerEntity,
		Pos: FarAway, PlayerID: pid, IsVisible: true})
	companion := scene.Add(&Entity{ConfigID: CompanionTemplate, ConfigType: ConfigTemplate, EntityType: TypeMonster,
		Pos: scene.PlayerPos, PlayerID: pid})
	battle := scene.Add(&Entity{ConfigID: BattleEntityTemplate, ConfigType: ConfigTemplate, EntityType: TypeSceneEntity,
		Pos: FarAway, PlayerID: pid})
	scene.VehicleID = vehicle.ID
	scene.PlayerEntityID = playerEntity.ID
	scene.BattleEntityID = battle.ID

	var vehicleBuffs []int64
	for _, b := range VehicleBuffs {
		if _, ok := d.Tables.BuffById[float64(b)]; ok {
			vehicleBuffs = append(vehicleBuffs, b)
		}
	}
	m := player.Motor
	vehicle.Components = append(vehicle.Components,
		Attributes(d.BaseAttributesOf(VehicleProperty), nil),
		C(&protocol.EntityComponentPb_TagComponent{TagComponent: &protocol.TagComponentPb{
			GameplayTags:    []*protocol.GameplayTagData{{Id: VehicleTag, TagCount: 1}},
			InitGameplayTag: true,
		}}),
		FightBuffs(scene, vehicle.ID, vehicleBuffs),
		C(&protocol.EntityComponentPb_VehiclePb{VehiclePb: &protocol.VehiclePb{}}),
		C(&protocol.EntityComponentPb_MotorOutlookComponentPb{MotorOutlookComponentPb: &protocol.MotorOutlookEquippedPb{
			SkinEquipped:        m.EquippedSkin,
			StickerEquipped:     append([]int32(nil), m.EquippedStickers...),
			DecorationsEquipped: append([]int32(nil), m.EquippedDecorations...),
			FrameEquipped:       m.EquippedFrame,
		}}),
		C(&protocol.EntityComponentPb_MotorDaCtxComponentPb{MotorDaCtxComponentPb: &protocol.MotorDaCtxComponentPb{MotorDaCtxId: vehicle.ID}}),
		C(&protocol.EntityComponentPb_FollowEntityComponentPb{FollowEntityComponentPb: &protocol.FollowEntityComponentPb{EntityId: companion.ID}}),
	)

	followers := &protocol.PlayerFollowersComponentPb{}
	for _, t := range []protocol.EPlayerFollower{
		protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerDefault,
		protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerExploreSkill,
		protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerAuxiliary,
		protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerSpecialItem,
		protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerMotor,
		protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerMax,
	} {
		var id int64
		if t == protocol.EPlayerFollower_EPlayerFollower_EPlayerFollowerMotor {
			id = vehicle.ID
		}
		followers.ListPlayerFollowerPb = append(followers.ListPlayerFollowerPb, &protocol.PlayerFollowerPb{Type: t, EntityId: id})
	}
	playerEntity.Components = append(playerEntity.Components,
		Attributes(d.BaseAttributesOf(PlayerEntityProperty), nil),
		FightBuffs(scene, playerEntity.ID, nil),
		C(&protocol.EntityComponentPb_PlayerFollowersComponentPb{PlayerFollowersComponentPb: followers}),
	)

	var companionBuffs []int64
	if s, ok := d.Tables.SummonCfgById[CompanionSummonCfg]; ok {
		for _, b := range s.BornBuffId {
			companionBuffs = append(companionBuffs, int64(b))
		}
	}
	companion.Components = append(companion.Components,
		Attributes(d.BaseAttributesOf(CompanionProperty), nil),
		FightBuffs(scene, companion.ID, companionBuffs),
		C(&protocol.EntityComponentPb_CombineEntityInfoPb{CombineEntityInfoPb: &protocol.CombineEntityInfoPb{}}),
		C(&protocol.EntityComponentPb_EquipComponent{EquipComponent: &protocol.EquipComponentPb{}}),
		C(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: &protocol.LogicStateComponentPb{}}),
		C(&protocol.EntityComponentPb_MonsterAiComponentPb{MonsterAiComponentPb: &protocol.MonsterAiComponentPb{CombatMessageId: 1}}),
		C(&protocol.EntityComponentPb_SummonerComponent{SummonerComponent: &protocol.SummonerComponentPb{
			SummonerId: vehicle.ID, SummonCfgId: CompanionSummonCfg, PlayerId: pid,
			Type: protocol.ESummonType_ESummonType_ESummonTypeConcomitantMotorcycle,
		}}),
		C(&protocol.EntityComponentPb_PlayerFollowerSelfComponentPb{PlayerFollowerSelfComponentPb: &protocol.PlayerFollowerSelfComponentPb{
			PlayerEntityId: playerEntity.ID, SummonConfigId: CompanionSummonCfg,
		}}),
	)

	battle.Components = append(battle.Components,
		Attributes(d.BaseAttributesOf(PlayerEntityProperty), nil),
		FightBuffs(scene, battle.ID, nil),
	)
}

// StateAttributes are the 25 attributes whose white value is the current one (roles.md §3.3),
// by the client's enum names.
var StateAttributes = func() map[int]bool {
	m := map[int]bool{}
	for _, n := range []string{"Life", "Strength", "Hardness", "Rage", "Tough", "Energy",
		"ElementPower1", "ElementPower2", "ElementPower3", "ElementPower4", "ElementPower5", "ElementPower6",
		"StatusBuildUp1", "StatusBuildUp2", "StatusBuildUp3", "StatusBuildUp4", "StatusBuildUp5",
		"ElementEnergy", "SpecialEnergy1", "SpecialEnergy2", "SpecialEnergy3", "SpecialEnergy4", "SpecialEnergy5",
		"ParalysisTime", "WeaknessBuildUp"} {
		v, ok := AttributeID(n)
		if !ok {
			panic("no attribute " + n)
		}
		m[int(v)] = true
	}
	return m
}()

// Attributes is an AttributeComponent from a base and an added array (roles.md §3.3 encoding).
func Attributes(base, added []int32) *protocol.EntityComponentPb {
	c := &protocol.AttributeComponentPb{}
	for i := 1; i < AttributeCount; i++ {
		var b, a int32
		if i < len(base) {
			b = base[i]
		}
		if i < len(added) {
			a = added[i]
		}
		current := b + a
		white := b
		if StateAttributes[i] {
			white = current
		}
		c.WhiteGreenProps = append(c.WhiteGreenProps, &protocol.WhiteGreenPropData{
			AttributeType: int32(i), WhiteValue: white, GreenValue: a, CurrentValue: current,
		})
	}
	return C(&protocol.EntityComponentPb_AttributeComponent{AttributeComponent: c})
}

// FightBuffs are buffs as they stand at spawn: active, infinite (roles.md §5.3 form).
func FightBuffs(scene *Scene, entityID int64, buffIDs []int64) *protocol.EntityComponentPb {
	c := &protocol.FightBuffComponentPb{}
	for _, id := range buffIDs {
		h := scene.NextBuffHandle
		scene.NextBuffHandle++
		c.FightBuffInfos = append(c.FightBuffInfos, &protocol.FightBuffInformation{
			HandleId: h, BuffId: id, Level: 1, StackCount: 1,
			InstigatorId: entityID, EntityId: entityID, ApplyType: protocol.ApplyGEType_ApplyGEType_Common,
			Duration: -1, LeftDuration: -1, IsActive: true, MessageId: -1,
		})
	}
	return C(&protocol.EntityComponentPb_FightBuffComponent{FightBuffComponent: c})
}
