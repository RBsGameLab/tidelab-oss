// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

const (
	// RegrowSeconds is how long plants, ores and pick-ups stay gone (wall clock).
	RegrowSeconds int64 = 24 * 60 * 60
	// MaxActionDepth caps chained StateChangeBehaviors recursion (R-34).
	MaxActionDepth = 6
	// ShellCreditID is ItemInfo id for Shell Credits.
	ShellCreditID = combat.MoneyShellCredit
	// WaveplateItemID is ItemInfo id for Waveplates (R-79).
	WaveplateItemID int32 = 5
)

// Most chest packages have an empty DropPreview; these 14xx ones of the same tier have previews.
var tierFallback = [4]int32{1401, 1402, 1403, 1404}

// EntityActions runs the server side of entity interaction actions (specs/quests.md §4).
// Ported from our C# Game/EntityActions.cs.
type EntityActions struct {
	s           *PlayerSession
	actionIncID int32

	// R-73: quest items handed over and taken back so far.
	QuestItemsGiven int
	QuestItemsTaken int
}

func NewEntityActions(s *PlayerSession) *EntityActions {
	return &EntityActions{s: s, actionIncID: 1000}
}

func (a *EntityActions) state() *save.WorldState { return a.s.World.State }
func (a *EntityActions) scene() *world.Scene     { return a.s.Scene }
func (a *EntityActions) nowSeconds() int64       { return a.s.Clock.Seconds() }

// RunOption runs the server-side actions of the option the player picked on entity netID.
func (a *EntityActions) RunOption(netID, configID int64, actions []quests.QuestAction) {
	a.runActions(&netID, configID, actions, 0)
	a.state().Save()
}

// RunDetached runs the server half of an action list not tied to an entity (quest nodes, flow actions).
func (a *EntityActions) RunDetached(actions []quests.QuestAction) {
	a.runActions(nil, 0, actions, 0)
	a.state().Save()
}

// RunAsEntity runs actions an entity executes itself (a 机关组 trigger or dynamic option): ChangeSelfEntityState means it.
func (a *EntityActions) RunAsEntity(configID int64, actions []quests.QuestAction) {
	var netPtr *int64
	if net, ok := a.NetIDOf(configID); ok {
		netPtr = &net
	}
	a.runActions(netPtr, configID, actions, 1)
	a.state().Save()
}

// PushClientActions pushes every run of consecutive client presentation actions as its own ActionNotify.
func (a *EntityActions) PushClientActions(actions []quests.QuestAction, ctx *protocol.GameCtxPb) {
	i := 0
	for i < len(actions) {
		if !quests.IsClientAction(actions[i].Name) {
			i++
			continue
		}
		end := quests.ClientRunEnd(actions, i)
		a.PushClientRange(len(actions), i, end, ctx)
		i = end + 1
	}
}

// PushClientRange sends one group of client actions: indexes start..end (inclusive) of total.
func (a *EntityActions) PushClientRange(total, start, end int, ctx *protocol.GameCtxPb) {
	a.actionIncID++
	a.s.Sink().Push(&protocol.ActionNotify{
		PlayerId:      a.s.State.PlayerID(),
		IncId:         a.actionIncID,
		GameCtx:       ctx,
		TotalCount:    int32(total),
		StartIndex:    int32(start),
		EndIndex:      int32(end),
		NeedFinishReq: false,
	})
}

func (a *EntityActions) runActions(netID *int64, configID int64, actions []quests.QuestAction, depth int) {
	if depth > MaxActionDepth {
		return
	}
	for _, action := range actions {
		if action.Name == "" {
			continue
		}
		p := action.Params
		switch action.Name {
		case "ChangeSelfEntityState":
			if p != nil && p.EntityState != nil {
				a.changeState(netID, configID, *p.EntityState, depth)
			}
		case "ChangeEntityState":
			if p != nil && p.EntityID != nil && p.State != nil {
				var tNet *int64
				if net, ok := a.NetIDOf(*p.EntityID); ok {
					tNet = &net
				}
				a.changeState(tNet, *p.EntityID, *p.State, depth)
			}
		case "Collect", "GetRewardByInteract":
			a.grantEntityReward(configID)
			if action.Name == "Collect" {
				a.removeEntity(netID, configID, a.nowSeconds()+RegrowSeconds)
			}
		case "DestroySelf":
			a.removeEntity(netID, configID, a.nowSeconds()+RegrowSeconds)
		case "ChangeLiftTarget":
			a.changeLiftTarget(netID, p)
		case "UnlockSystemItem":
			if typ, ok := quests.ActionString(action, "SystemOption", "Type"); ok && typ == "AchievementSystem" {
				if achID, ok := quests.ActionInt(action, "SystemOption", "Id"); ok {
					a.s.Achievements().Finish(a.s, int32(achID))
				}
			}
		case "AwakeEntity":
			if p == nil || !p.HasEntityIDs {
				break
			}
			for _, id := range p.EntityIDs {
				a.state().Wake(id)
			}
			common.Info("interact_actions", fmt.Sprintf("%d: woke %d entities %v", configID, len(p.EntityIDs), p.EntityIDs))
			a.s.MarkSpawns()
		case "FinishDungeon":
			ok := true
			if p != nil && p.IsSuccess != nil {
				ok = *p.IsSuccess
			}
			a.finishDungeon(ok)
		case "TeleportDungeon":
			if netID == nil || p == nil || p.DungeonID == nil {
				break
			}
			landing, _ := quests.ActionInt(action, "LocationEntityId")
			common.Info("interact_actions", fmt.Sprintf("%d: TeleportDungeon %d (landing on %d)", configID, *p.DungeonID, landing))
			if q := a.s.QuestState; q == nil || !q.Dungeons.Enter(a.s, *p.DungeonID, landing) {
				common.Warn("interact_actions", fmt.Sprintf("%d: entering dungeon %d failed", configID, *p.DungeonID))
			}
		case "SettlementDungeon":
			// The client opens the claim dialog itself; the result screen goes out after the claim.
		case "ExitDungeon":
			if e := a.s.Quests(); e != nil {
				e.PlanExit()
			}
		case "SetEntityVisible":
			a.setEntityVisible(configID, action)
		case "HideGroup":
			if key, ok := quests.ActionString(action, "GroupKey"); ok {
				if q := a.s.QuestState; q != nil && q.HideGroups.Hide(key) > 0 {
					a.s.RemoveHidden()
				}
			}
		case "ShowHidedGroup":
			if key, ok := quests.ActionString(action, "GroupKey"); ok {
				if q := a.s.QuestState; q != nil && q.HideGroups.Show(key) {
					a.s.MarkSpawns()
				}
			}
		case "GetItem":
			a.getItem(configID, action)
		case "DestroyQuestItem":
			a.destroyQuestItem(configID, action)
		case "DestroyEntity":
			if p == nil || !p.HasEntityIDs {
				break
			}
			for _, id := range p.EntityIDs {
				a.state().Sleep(id)
				var dNet *int64
				if net, ok := a.NetIDOf(id); ok {
					dNet = &net
				}
				a.removeEntity(dNet, id, save.Forever)
			}
		case "SetPlayerPos":
			a.setPlayerPos(configID, action)
		}
	}
}

func (a *EntityActions) changeLiftTarget(netID *int64, p *quests.ActionParams) {
	if p == nil || p.Location == nil {
		return
	}
	var id int64
	isSelf := true
	if p.IsSelf != nil {
		isSelf = *p.IsSelf
	}
	if !isSelf && p.EntityID != nil {
		net, ok := a.NetIDOf(*p.EntityID)
		if !ok {
			common.Warn("interact_actions", fmt.Sprintf("lift %d not spawned", *p.EntityID))
			return
		}
		id = net
	} else if netID != nil {
		id = *netID
	} else {
		return
	}
	common.Info("interact_actions", fmt.Sprintf("lift %d -> floor %d", id, *p.Location))
	a.s.Sink().Push(&protocol.ElevatorMoveNotify{EntityId: id, Location: *p.Location})
}

func (a *EntityActions) finishDungeon(ok bool) {
	sc := a.scene()
	if sc == nil {
		return
	}
	common.Info("interact_actions", fmt.Sprintf("dungeon %d finished, success %v", sc.InstanceID, ok))
	a.s.Sink().Push(&protocol.InstResultNotify{Id: sc.InstanceID, Succ: ok, IsRecReward: false})
	// R-37: a Boss 连战 level is scored.
	if ok {
		a.s.Activities().BossRush.OnFinished(a.s, sc.InstanceID)
	}
	// R-41: a 深塔 floor being climbed.
	if q := a.s.QuestState; q != nil {
		nowMs := a.s.Clock.Ms()
		if e := a.s.Quests(); e != nil {
			nowMs = e.NowMs()
		}
		q.Tower.OnFinished(a.s, sc.InstanceID, ok, nowMs)
	}
	// R-53: a 失序梦途 dungeon cleared.
	a.s.Activities().DreamLink.OnFinished(a.s, sc.InstanceID, ok)
	// R-4e: quests waiting for this dungeon (FinishDungeon steps).
	if e := a.s.Quests(); e != nil {
		e.OnDungeonFinished(sc.InstanceID, ok)
	}
}

func (a *EntityActions) setEntityVisible(configID int64, action quests.QuestAction) {
	if action.Params == nil || !action.Params.HasEntityIDs {
		return
	}
	visible := true
	if v, ok := quests.ActionBool(action, "Visible"); ok {
		visible = v
	}
	for _, id := range action.Params.EntityIDs {
		if visible {
			a.state().Wake(id)
		} else {
			a.state().Sleep(id)
			var netPtr *int64
			if net, ok := a.NetIDOf(id); ok {
				netPtr = &net
			}
			a.removeEntity(netPtr, id, 0)
		}
	}
	verb := "shown"
	if !visible {
		verb = "hidden"
	}
	common.Info("interact_actions", fmt.Sprintf("%d: %d entities %s", configID, len(action.Params.EntityIDs), verb))
	a.s.MarkSpawns()
}

func (a *EntityActions) getItem(configID int64, action quests.QuestAction) {
	for _, item := range quests.ActionObjects(action, "Items") {
		id, ok := world.JSONInt(quests.ObjectField(item, "ItemId"))
		if !ok {
			continue
		}
		count := world.JSONIntOr(quests.ObjectField(item, "Count"), 1)
		if id <= 0 || count <= 0 {
			continue
		}
		total := a.GrantItem(id, count, false)
		a.QuestItemsGiven++
		common.Info("interact_actions", fmt.Sprintf("%d: GetItem %d x%d (now %d)", configID, id, count, total))
	}
}

func (a *EntityActions) destroyQuestItem(configID int64, action quests.QuestAction) {
	id, ok := quests.ActionInt(action, "ItemId")
	if !ok {
		return
	}
	all, _ := quests.ActionBool(action, "IsAll")
	var cnt *int32
	if !all {
		c := int32(1)
		if v, ok := quests.ActionInt(action, "Count"); ok {
			c = int32(v)
		}
		cnt = &c
	}
	left := a.TakeItem(int32(id), cnt)
	a.QuestItemsTaken++
	how := "some"
	if all {
		how = "all"
	}
	common.Info("interact_actions", fmt.Sprintf("%d: DestroyQuestItem %d (%s), %d left", configID, id, how, left))
}

func (a *EntityActions) setPlayerPos(configID int64, action quests.QuestAction) {
	pos, ok := a.TeleportTarget(action)
	if !ok {
		common.Warn("interact_actions", fmt.Sprintf("%d: SetPlayerPos without a usable target", configID))
		return
	}
	common.Info("interact_actions", fmt.Sprintf("%d: SetPlayerPos -> %.0f,%.0f,%.0f", configID, pos[0], pos[1], pos[2]))
	if q := a.s.QuestState; q != nil {
		q.LeaveVehicle(protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal)
	}
	var mapID int32
	if sc := a.scene(); sc != nil {
		mapID = sc.InstanceID
	}
	var rot *protocol.Vector
	if yaw, ok := quests.ActionFloat(action, "TelePortConfig", "TargetPos", "A"); ok {
		rot = &protocol.Vector{X: 0, Y: 0, Z: float32(yaw)}
	}
	a.s.Sink().Push(&protocol.TeleportNotify{
		MapId:    mapID,
		Position: &protocol.Vector{X: pos[0], Y: pos[1], Z: pos[2]},
		Rotation: rot,
	})
}

// TeleportTarget resolves where SetPlayerPos sends the player (Type 0 TargetPos, 1 EntityIds[0], 2 GravityAndPosEntityId).
func (a *EntityActions) TeleportTarget(action quests.QuestAction) ([3]float32, bool) {
	typ, _ := quests.ActionInt(action, "TelePortConfig", "Type")
	if typ == 0 {
		tp, ok := quests.ActionAt(action, "TelePortConfig", "TargetPos").(*data.Object)
		if !ok || tp == nil {
			return [3]float32{}, false
		}
		num := func(k string) float32 {
			if n, ok := tp.Get(k).(data.Number); ok {
				return float32(n)
			}
			return 0
		}
		return [3]float32{num("X"), num("Y"), num("Z")}, true
	}
	var id int64
	var hasID bool
	switch typ {
	case 1:
		if ids := quests.ActionInts(action, "TelePortConfig", "EntityIds"); len(ids) > 0 {
			id, hasID = ids[0], true
		}
	case 2:
		id, hasID = quests.ActionInt(action, "TelePortConfig", "GravityAndPosEntityId")
	}
	if !hasID {
		return [3]float32{}, false
	}
	return a.EntityPos(id)
}

// EntityPos returns the row position of configID on this scene's map: Transform / 100.
func (a *EntityActions) EntityPos(configID int64) ([3]float32, bool) {
	sc := a.scene()
	if sc == nil || configID < math.MinInt32 || configID > math.MaxInt32 {
		return [3]float32{}, false
	}
	row := a.s.Data.Tables.LevelEntity(sc.MapID, int32(configID))
	if row == nil || len(row.Transform) == 0 || row.Transform[0] == nil {
		return [3]float32{}, false
	}
	p := row.Transform[0]
	return [3]float32{float32(p.X) / 100, float32(p.Y) / 100, float32(p.Z) / 100}, true
}

// NetIDOf returns the net id of a spawned entity with this config id, if any.
func (a *EntityActions) NetIDOf(configID int64) (int64, bool) {
	if a.s.Spawner != nil {
		if net, ok := a.s.Spawner.NetIDOf(configID); ok {
			return net, true
		}
	}
	sc := a.scene()
	if sc == nil {
		return 0, false
	}
	for _, e := range sc.Entities() {
		if int64(e.ConfigID) == configID && e.EntityType != protocol.EEntityType_EEntityType_Player {
			return e.ID, true
		}
	}
	return 0, false
}

// SpawnedState returns the state tag the spawned entity has now.
func (a *EntityActions) SpawnedState(configID int64) (int32, bool) {
	net, ok := a.NetIDOf(configID)
	if !ok {
		return 0, false
	}
	return a.currentTag(&net)
}

func (a *EntityActions) currentTag(netID *int64) (int32, bool) {
	if netID == nil || a.scene() == nil {
		return 0, false
	}
	e := a.scene().Get(*netID)
	if e == nil {
		return 0, false
	}
	sc, ok := world.ComponentOf[*protocol.EntityComponentPb_StateTagComponentPb](e)
	if !ok || sc.StateTagComponentPb == nil {
		return 0, false
	}
	return sc.StateTagComponentPb.StateTagId, true
}

func (a *EntityActions) components(configID int64) map[string]*data.Object {
	sc := a.scene()
	if sc == nil || configID < math.MinInt32 || configID > math.MaxInt32 {
		return nil
	}
	row := a.s.Data.Tables.LevelEntity(sc.MapID, int32(configID))
	if row == nil {
		return nil
	}
	var tpl data.Value
	if t := a.s.Data.Template(row.BlueprintType); t != nil {
		tpl = t.ComponentsData
	}
	return world.Merge(row.ComponentsData, tpl)
}

// EntityActionFromData parses an entity action of the game data with both typed Params and Raw params (R-58).
func EntityActionFromData(v data.Value) quests.QuestAction {
	act := quests.ParseAction(v)
	if o, ok := v.(*data.Object); ok && o != nil {
		act.Raw = o.Get("Params")
	} else {
		act.Raw = nil
	}
	return act
}

// EntityActionsFromList parses a JSON array of entity actions.
func EntityActionsFromList(list data.Value) []quests.QuestAction {
	arr, ok := list.(*data.Array)
	if !ok || arr == nil || len(arr.Items) == 0 {
		return nil
	}
	out := make([]quests.QuestAction, len(arr.Items))
	for i, item := range arr.Items {
		out[i] = EntityActionFromData(item)
	}
	return out
}

// OnEntityEvent handles EntitySendEventRequest: runs ConditionListener actions listening for eventKey.
func (a *EntityActions) OnEntityEvent(netID, configID int64, eventKey string) {
	comp := a.components(configID)["ConditionListenerComponent"]
	if comp == nil {
		common.Info("interact_actions", fmt.Sprintf("entity %d: event %s, nothing listens", configID, eventKey))
		return
	}
	defer a.state().Save()
	listeners, ok := comp.Get("Listeners").(*data.Array)
	if !ok || listeners == nil {
		return
	}
	for _, item := range listeners.Items {
		l, ok := item.(*data.Object)
		if !ok || l == nil || !listensTo(l, eventKey) {
			continue
		}
		actions, ok := l.Get("Actions").(*data.Array)
		if !ok || actions == nil {
			continue
		}
		common.Info("interact_actions", fmt.Sprintf("entity %d: event %s", configID, eventKey))
		a.runActions(&netID, configID, EntityActionsFromList(actions), 0)
	}
}

func listensTo(listener *data.Object, key string) bool {
	cond, ok := listener.Get("Condition").(*data.Object)
	if !ok || cond == nil {
		return false
	}
	list, ok := cond.Get("Conditions").(*data.Array)
	if !ok || list == nil {
		return false
	}
	for _, c := range list.Items {
		o, ok := c.(*data.Object)
		if !ok || o == nil {
			continue
		}
		if k, ok := o.Get("EventKey").(data.String); ok && string(k) == key {
			return true
		}
	}
	return false
}

// OnTargetGearHit handles TargetGearHitRequest (ChangeNextAndLockTargetState / ChangeNextState).
func (a *EntityActions) OnTargetGearHit(netID, configID int64) {
	gear := a.components(configID)["TargetGearComponent"]
	if gear == nil {
		return
	}
	defer a.state().Save()
	hit, _ := gear.Get("HitLogicType").(*data.Object)
	kind := "ChangeNextState"
	if hit != nil {
		if t, ok := hit.Get("Type").(data.String); ok {
			kind = string(t)
		}
	}
	if kind == "ChangeNextAndLockTargetState" {
		if hit == nil {
			return
		}
		target, ok := hit.Get("TargetState").(data.String)
		if !ok {
			return
		}
		common.Info("interact_actions", fmt.Sprintf("gear %d: hit, -> %s", configID, string(target)))
		a.changeState(&netID, configID, string(target), 0)
		return
	}
	if kind != "ChangeNextState" {
		common.Info("interact_actions", fmt.Sprintf("gear %d: hit logic %s not handled", configID, kind))
		return
	}
	cycle, ok := gear.Get("CycleStates").(*data.Array)
	if !ok || cycle == nil || len(cycle.Items) == 0 {
		return
	}
	var states []string
	for _, item := range cycle.Items {
		if s, ok := item.(data.String); ok {
			states = append(states, string(s))
		}
	}
	if len(states) == 0 {
		return
	}
	var current int32
	if st, ok := a.state().StateOverride(configID); ok {
		current = st
	} else if ct, ok := a.currentTag(&netID); ok {
		current = ct
	}
	next := 0
	for i, st := range states {
		if world.TagID(a.s.Data.Tables, st) == current {
			next = (i + 1) % len(states)
		}
	}
	common.Info("interact_actions", fmt.Sprintf("gear %d: hit, -> %s", configID, states[next]))
	a.changeState(&netID, configID, states[next], 0)
}

// OnClientState handles ElevatorStateChangeRequest and ChangeEntityStateRequest.
func (a *EntityActions) OnClientState(netID *int64, configID int64, tag int32) {
	defer a.state().Save()
	if esc := a.components(configID)["EntityStateComponent"]; esc != nil {
		if behaviours, ok := esc.Get("StateChangeBehaviors").(*data.Array); ok && behaviours != nil {
			for _, item := range behaviours.Items {
				o, ok := item.(*data.Object)
				if !ok || o == nil {
					continue
				}
				if name, ok := o.Get("State").(data.String); ok && world.TagID(a.s.Data.Tables, string(name)) == tag {
					a.changeState(netID, configID, string(name), 0)
					return
				}
			}
		}
	}
	common.Info("interact_actions", fmt.Sprintf("entity %d: client state %d", configID, tag))
	a.state().SetState(configID, tag)
	a.stateChanged(configID)
}

func (a *EntityActions) stateChanged(configID int64) {
	if e := a.s.Quests(); e != nil {
		e.OnEntityState(configID)
	}
	a.s.Groups().OnEntityState(a.s, configID)
}

func (a *EntityActions) changeState(netID *int64, configID int64, stateName string, depth int) {
	if depth > MaxActionDepth {
		common.Warn("interact_actions", fmt.Sprintf("entity %d: state chain deeper than %d, stopped at %s", configID, MaxActionDepth, stateName))
		return
	}
	newTag := world.TagID(a.s.Data.Tables, stateName)
	var oldTag int32
	if netID != nil {
		id := *netID
		if sc := a.scene(); sc != nil {
			if entity := sc.Get(id); entity != nil {
				oldTag = setEntityTag(entity, newTag)
			}
		}
		if oldTag == newTag {
			a.state().SetState(configID, newTag)
			a.stateChanged(configID)
			return
		}
		tags := &protocol.EntityCommonTagNotify{Id: id}
		if oldTag != 0 {
			tags.Tags = append(tags.Tags, &protocol.CommonTagData{TagId: oldTag, IsAdd: false})
		}
		tags.Tags = append(tags.Tags, &protocol.CommonTagData{TagId: newTag, IsAdd: true})
		a.s.Sink().Push(tags)
		a.s.Sink().Push(&protocol.EntityStateReadyNotify{EntityId: id, TagId: newTag, Ready: true})
	}
	common.Info("interact_actions", fmt.Sprintf("entity %d: state %d -> %s", configID, oldTag, stateName))
	a.state().SetState(configID, newTag)
	a.stateChanged(configID)

	comps := a.components(configID)
	if comps == nil {
		return
	}
	// Treasure chests (RewardType 1) pay out when the player opens them, once.
	if reward := comps["RewardComponent"]; reward != nil {
		disabled, _ := reward.Get("Disabled").(data.Bool)
		chest := world.JSONIntOr(reward.Get("RewardType"), 0) == 1 && !bool(disabled)
		st, hasSt := a.state().Get(configID)
		already := hasSt && st.GoneUntil == save.Forever
		if chest && !already && depth == 0 {
			a.grantEntityReward(configID)
			a.state().SetGone(configID, save.Forever)
		}
	}
	esc := comps["EntityStateComponent"]
	if esc == nil {
		return
	}
	behaviours, ok := esc.Get("StateChangeBehaviors").(*data.Array)
	if !ok || behaviours == nil {
		return
	}
	for i, item := range behaviours.Items {
		b, ok := item.(*data.Object)
		if !ok || b == nil {
			continue
		}
		st, ok := b.Get("State").(data.String)
		if !ok || string(st) != stateName {
			continue
		}
		if list, ok := b.Get("Action").(*data.Array); ok && list != nil {
			actions := EntityActionsFromList(list)
			if netID != nil {
				a.PushClientActions(actions, &protocol.GameCtxPb{
					CtxType: protocol.GameCtxType_GameCtxType_EntityStateChangeAction,
					CtxInfo: &protocol.GameCtxPb_StateChangeAction{
						StateChangeAction: &protocol.StateChangeActionCtxPb{
							EntityCtx:  &protocol.EntityCtxPb{ConfigId: int32(configID), IncId: *netID},
							StateIndex: int32(i),
						},
					},
				})
			}
			a.runActions(netID, configID, actions, depth+1)
		}
		if dcs, ok := b.Get("DelayChangeState").(*data.Object); ok && dcs != nil {
			if next, ok := dcs.Get("NewState").(data.String); ok {
				a.changeState(netID, configID, string(next), depth+1)
			}
		}
	}
}

func setEntityTag(entity *world.Entity, newTag int32) int32 {
	var oldTag int32
	if sc, ok := world.ComponentOf[*protocol.EntityComponentPb_StateTagComponentPb](entity); ok && sc.StateTagComponentPb != nil {
		oldTag = sc.StateTagComponentPb.StateTagId
		sc.StateTagComponentPb.StateTagId = newTag
	}
	if tc, ok := world.ComponentOf[*protocol.EntityComponentPb_TagComponent](entity); ok && tc.TagComponent != nil {
		tags := tc.TagComponent
		if oldTag != 0 {
			for i, g := range tags.GameplayTags {
				if g.Id == oldTag {
					g.TagCount--
					if g.TagCount <= 0 {
						tags.GameplayTags = slices.Delete(tags.GameplayTags, i, i+1)
					}
					break
				}
			}
			if at := slices.Index(tags.EntityCommonTags, oldTag); at >= 0 {
				tags.EntityCommonTags[at] = newTag
			}
		}
		found := false
		for _, g := range tags.GameplayTags {
			if g.Id == newTag {
				g.TagCount++
				found = true
				break
			}
		}
		if !found {
			tags.GameplayTags = append(tags.GameplayTags, &protocol.GameplayTagData{Id: newTag, TagCount: 1})
		}
		if !slices.Contains(tags.EntityCommonTags, newTag) {
			tags.EntityCommonTags = append(tags.EntityCommonTags, newTag)
		}
	}
	return oldTag
}

// Respawn removes a spawned world entity so the next refresh spawns it again (R-82e).
func (a *EntityActions) Respawn(configID int64) {
	if a.s.Spawner == nil {
		return
	}
	net, ok := a.s.Spawner.Drop(configID)
	if !ok {
		return
	}
	a.s.RemoveFromScene(net)
	a.s.MarkSpawns()
}

// HideEntity puts an entity back to sleep and removes it if spawned (R-67).
func (a *EntityActions) HideEntity(configID int64) {
	common.Info("interact_actions", fmt.Sprintf("%d: back to sleep", configID))
	a.state().Sleep(configID)
	var netPtr *int64
	if net, ok := a.NetIDOf(configID); ok {
		netPtr = &net
	}
	a.removeEntity(netPtr, configID, save.Forever)
}

func (a *EntityActions) removeEntity(netID *int64, configID int64, until int64) {
	a.state().SetGone(configID, until)
	var dropped *int64
	if a.s.Spawner != nil {
		if d, ok := a.s.Spawner.Drop(configID); ok {
			dropped = &d
		}
	}
	// R-99: the player comes off a vehicle before it goes.
	if q := a.s.QuestState; q != nil && q.Vehicles.RidesConfig(configID) {
		q.LeaveVehicle(protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeNormal)
	}
	var id int64
	switch {
	case netID != nil:
		id = *netID
	case dropped != nil:
		id = *dropped
	default:
		return
	}
	if a.scene() == nil || a.scene().Get(id) == nil {
		return
	}
	a.s.RemoveFromScene(id)
	common.Info("interact_actions", fmt.Sprintf("entity %d (net %d) removed until %d", configID, id, until))
}

func (a *EntityActions) grantEntityReward(configID int64) {
	reward := a.components(configID)["RewardComponent"]
	if reward == nil {
		return
	}
	if disabled, ok := reward.Get("Disabled").(data.Bool); ok && bool(disabled) {
		return
	}
	dropID, ok := world.JSONInt(reward.Get("RewardId"))
	if !ok || dropID <= 0 {
		return
	}
	tier := 1
	if world.JSONIntOr(reward.Get("RewardType"), 0) == 1 && a.scene() != nil {
		if row := a.s.Data.Tables.LevelEntity(a.scene().MapID, int32(configID)); row != nil {
			tier = ChestTier(row.Name)
		}
	}
	a.GrantDrop(dropID, configID, tier, nil)
}

// ChestTier maps a chest's name to tier 1 (简易) .. 4 (豪华).
func ChestTier(name string) int {
	for _, pair := range []struct {
		word string
		tier int
	}{
		{"豪华", 4}, {"珍稀", 4}, {"丰厚", 3}, {"标准", 2},
	} {
		if strings.Contains(name, pair.word) {
			return pair.tier
		}
	}
	return 1
}

// GrantDrop awards a drop package's preview list into the bag (R-109 filters to ItemInfo).
func (a *EntityActions) GrantDrop(dropID int32, configID int64, tier int, given *[]*protocol.RewardItemInfo) {
	t := a.s.Data.Tables
	pkg, ok := t.DropPackageById[dropID]
	if !ok {
		common.Warn("interact_actions", fmt.Sprintf("%d: drop package %d not found", configID, dropID))
		return
	}
	if pkg.DropPreview == nil || pkg.DropPreview.Count() == 0 {
		if tier <= 0 {
			return
		}
		clamped := min(max(tier, 1), 4)
		pkg, ok = t.DropPackageById[tierFallback[clamped-1]]
		if !ok || pkg.DropPreview == nil {
			return
		}
	}
	inv := a.s.Combat.Player.NormalInventory
	var added, updated, counts []save.ItemCount
	for _, pair := range pkg.DropPreview.Pairs {
		itemID, preview := pair.Key, pair.Value
		if itemID <= 0 {
			continue
		}
		if _, known := t.ItemInfoById[itemID]; !known {
			common.Warn("interact_actions", fmt.Sprintf("%d: drop %d item %d is not a normal item, not given", configID, dropID, itemID))
			continue
		}
		tt := int32(max(tier, 1))
		amount := preview
		if amount <= 0 {
			if itemID == ShellCreditID {
				amount = 1000 * tt
			} else {
				amount = tt + 1
			}
		}
		if given != nil {
			*given = append(*given, &protocol.RewardItemInfo{ItemId: itemID, Count: amount})
		}
		if combat.IsMoney(itemID) {
			a.GrantItem(itemID, amount, true)
			continue
		}
		old := inv.CountOf(itemID)
		ic := save.ItemCount{ID: itemID, Count: old + amount}
		counts = append(counts, ic)
		if old == 0 {
			added = append(added, ic)
		} else {
			updated = append(updated, ic)
		}
	}
	if len(counts) == 0 {
		return
	}
	inv.SetBatch(counts)
	if len(added) > 0 {
		a.s.Combat.Inventory.PushFilteredNormalItemAdd(added, 0, false, a.s.Sink())
	}
	if len(updated) > 0 {
		a.s.Combat.Inventory.PushFilteredNormalItemUpdate(updated, false, a.s.Sink())
	}
	common.Info("interact_actions", fmt.Sprintf("%d: drop %d gave %d item kinds", configID, dropID, len(counts)))
}

// GrantItem adds amount of one normal item, saves and notifies the client. Returns the new count.
func (a *EntityActions) GrantItem(itemID, amount int32, noTips bool) int32 {
	if combat.IsMoney(itemID) {
		return combat.SetMoney(a.s.Combat.Player, itemID, int64(a.s.ItemCount(itemID))+int64(amount), a.s.Sink())
	}
	inv := a.s.Combat.Player.NormalInventory
	old := inv.CountOf(itemID)
	next := old + amount
	batch := []save.ItemCount{{ID: itemID, Count: next}}
	inv.SetBatch(batch)
	if old == 0 {
		a.s.Combat.Inventory.PushFilteredNormalItemAdd(batch, 0, noTips, a.s.Sink())
	} else {
		a.s.Combat.Inventory.PushFilteredNormalItemUpdate(batch, noTips, a.s.Sink())
	}
	return next
}

// TakeItem takes count of one normal item (nil: all of them), saves and notifies the client.
// Returns how many are left.
func (a *EntityActions) TakeItem(itemID int32, count *int32) int32 {
	inv := a.s.Combat.Player.NormalInventory
	have := a.s.ItemCount(itemID)
	if have <= 0 {
		return 0
	}
	take := have
	if count != nil && *count < take {
		take = *count
	}
	if take <= 0 {
		return have
	}
	if itemID == WaveplateItemID {
		a.s.Activities().Tasks.AddWaveplate(Today(a.s.Clock.Seconds()), take)
	}
	left := have - take
	if combat.IsMoney(itemID) {
		return combat.SetMoney(a.s.Combat.Player, itemID, int64(left), a.s.Sink())
	}
	batch := []save.ItemCount{{ID: itemID, Count: left}}
	inv.SetBatch(batch)
	if left == 0 {
		a.s.Sink().Push(&protocol.NormalItemRemoveNotify{NormalItemIdList: []int32{itemID}})
	} else {
		a.s.Combat.Inventory.PushFilteredNormalItemUpdate(batch, false, a.s.Sink())
	}
	return left
}
