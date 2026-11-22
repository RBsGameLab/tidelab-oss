// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// Shared is what every player's context shares: built once per data load.
type Shared struct {
	Tables       *data.DataTables
	BuffBuilder  *RoleBuffBuilder
	Concomitants *ConcomitantResolver
	Passives     *PassiveSkillsIndex
	Fsm          *FsmInitializer
	LandingArgs  []int32
}

func NewShared(t *data.DataTables, commonParamPath string) *Shared {
	c := NewConcomitantResolver(t)
	return &Shared{
		Tables: t, Concomitants: c, BuffBuilder: NewRoleBuffBuilder(t, c),
		Passives: NewPassiveSkillsIndex(t), Fsm: NewFsmInitializer(t),
		LandingArgs: LoadLandingRoleArgs(commonParamPath),
	}
}

// PlayerContext is one player's persistent state, its scene's combat state and the services
// (roles, inventory, formation, combat) over them.
type PlayerContext struct {
	Player    *save.PlayerData
	Scene     *CombatSceneState
	Roles     *RoleService
	Inventory *InventoryService
	Formation *FormationService
	Buffs     *BuffManager
	Kills     *KillAndDropService
	Revive    *ReviveService
	Combat    *CombatService
}

func NewPlayerContext(sh *Shared, player *save.PlayerData, hooks GameHooks, rng Rng) *PlayerContext {
	t := sh.Tables
	scene := NewCombatSceneState()
	scene.PlayerID = player.PlayerID
	c := &PlayerContext{Player: player, Scene: scene}
	c.Buffs = NewBuffManager(t, scene)
	c.Formation = NewFormationService(t, player, scene, sh.BuffBuilder, sh.Concomitants, sh.Passives, sh.Fsm, c.Buffs, hooks)
	c.Roles = NewRoleService(t, player, scene, sh.BuffBuilder, c.Formation, c.Buffs, hooks)
	c.Inventory = NewInventoryService(t, player, scene, sh.BuffBuilder, sh.Concomitants, c.Formation, c.Roles, c.Buffs, rng)
	c.Kills = NewKillAndDropService(t, player, scene, hooks, rng)
	c.Revive = NewReviveService(t, scene)
	c.Combat = NewCombatService(t, scene, c.Formation, c.Buffs, c.Kills, c.Revive, hooks, sh.LandingArgs, rng)
	return c
}

// PrepareForLogin seeds a new account, migrates stray weapons (R-116), repairs the roles'
// base_prop and weapons, checks the default formation and starts the flow clock.
func (c *PlayerContext) PrepareForLogin(nowMs int64, nowSec uint32, seedDefaultEchoes bool) {
	if !c.Player.BasicInfoExistedOnLoad {
		save.SaveBasicInfo(c.Player)
	}
	c.Inventory.SeedNewAccount(seedDefaultEchoes)
	c.Inventory.MigrateStrayWeaponsOnLogin()
	c.Roles.SeedNewAccountRoles(nowSec)
	c.Roles.RepairOnLogin(nowMs)
	c.Formation.EnsureDefaults()
	c.Scene.InitFlowClock(nowMs)
}

// PushLoginData sends combat's login notifies in the order of specs/enter-world.md §3.
func (c *PlayerContext) PushLoginData(sink PushSink) {
	sink.Push(c.Roles.BuildBasicInfoNotify())
	if id := c.Player.BasicInfo.CurPlayerTitleID; id > 0 {
		sink.Push(&protocol.DressPlayerTitleUpdateNotify{PlayerTitleId: id, PlayerTitleExtraParam: 1})
	}
	c.Inventory.PushLoginNormalItems(sink)
	c.Roles.PushCosmeticAndRoleLoginNotifies(sink)
	c.Inventory.PushLoginCalabashAndBagNotifies(sink)
	sink.Push(&protocol.PassiveSkillNotify{})
}

// Tick advances buff timers and the state machines' timers (R-152), removes corpses and makes the 30 s dirty saves; it returns the
// corpses removed.
func (c *PlayerContext) Tick(nowMs int64, sink PushSink) []int64 {
	c.Buffs.Tick(nowMs, sink)
	c.Combat.TickFsms(nowMs, sink)
	gone := c.Kills.ProcessExpiredCorpses(nowMs, sink)
	save.FlushDirtyIfDue(c.Player, nowMs)
	return gone
}

// FlushSaves writes the dirty roles and weapons now (a scene change, a release).
func (c *PlayerContext) FlushSaves() { save.FlushDirty(c.Player) }
