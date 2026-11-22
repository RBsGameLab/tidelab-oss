// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"

	"tidelab/internal/common"
	"tidelab/internal/quests"
	"tidelab/protocol"
)

type runningFlow struct {
	key   string
	list  string
	flow  int32
	state int32
}

// FlowActions executes server-side actions inside flows (剧情; specs/flows.md §2 rule 3, §4, §5).
// Ported from our C# Game/FlowActions.cs.
type FlowActions struct {
	states  map[string][]quests.FlowActionDef
	running map[int64]runningFlow
}

func NewFlowActions(states map[string][]quests.FlowActionDef) *FlowActions {
	if states == nil {
		states = map[string][]quests.FlowActionDef{}
	}
	return &FlowActions{
		states:  states,
		running: map[int64]runningFlow{},
	}
}

func (f *FlowActions) RunningCount() int { return len(f.running) }

func FlowActionKey(list string, flow, state int32) string {
	return fmt.Sprintf("%s_%d_%d", list, flow, state)
}

// Started remembers which flow state an inc id plays (every FlowStartNotify the server sends),
// and wakes its actors now: for an actor already in range the client sends no FlowActionRequest,
// it just waits for the server to have spawned it. Returns true when it woke someone.
func (f *FlowActions) Started(s *PlayerSession, n *protocol.FlowStartNotify) bool {
	key := FlowActionKey(n.FlowListName, n.FlowId, n.StateId)
	f.running[n.FlowIncId] = runningFlow{key: key, list: n.FlowListName, flow: n.FlowId, state: n.StateId}
	actions, ok := f.states[key]
	if !ok {
		return false
	}
	woke := false
	for _, fa := range actions {
		if fa.Action.Name != "AwakeEntity" || fa.Action.Params == nil || !fa.Action.Params.HasEntityIDs {
			continue
		}
		for _, id := range fa.Action.Params.EntityIDs {
			s.World.State.Wake(id)
		}
		if len(fa.Action.Params.EntityIDs) > 0 {
			woke = true
		}
	}
	if woke {
		s.MarkSpawns()
		s.World.State.Save()
	}
	return woke
}

// Ended settles the flow's actors on FlowEndRequest: replays every AwakeEntity / DestroyEntity
// in order so actors the client hid or waited for locally persist across scene loads.
func (f *FlowActions) Ended(s *PlayerSession, incID int64) {
	r, ok := f.running[incID]
	if !ok {
		return
	}
	delete(f.running, incID)
	actions, ok := f.states[r.key]
	if !ok {
		return
	}
	n := 0
	for _, fa := range actions {
		if fa.Action.Name != "AwakeEntity" && fa.Action.Name != "DestroyEntity" {
			continue
		}
		s.Actions().RunDetached([]quests.QuestAction{fa.Action})
		n++
	}
	if n > 0 {
		common.Info("flow_actions", fmt.Sprintf("%s: settled %d awake/destroy actions at flow end", r.key, n))
	}
}

// Run executes the server half of flow action actionID; returns true if it was found.
func (f *FlowActions) Run(s *PlayerSession, incID int64, actionID int32) bool {
	r, ok := f.running[incID]
	if !ok {
		common.Warn("flow_actions", fmt.Sprintf("flow inc %d: unknown flow (action %d)", incID, actionID))
		return false
	}
	actions, ok := f.states[r.key]
	if !ok {
		return false
	}
	for _, fa := range actions {
		if fa.ActionID != actionID {
			continue
		}
		common.Info("flow_actions", fmt.Sprintf("%s: action %d %s", r.key, actionID, fa.Action.Name))
		if len(fa.Pos) >= 4 {
			var mapID int32
			if s.Scene != nil {
				mapID = s.Scene.InstanceID
			}
			// FlowActionSetPlayerPos waits for a TeleportNotify carrying this flow action's ctx (§2 rule 4).
			s.Sink().Push(&protocol.TeleportNotify{
				MapId:    mapID,
				Position: &protocol.Vector{X: fa.Pos[0], Y: fa.Pos[1], Z: fa.Pos[2]},
				// The client reads Rotator(Pitch = Y, Yaw = Z, Roll = X).
				Rotation: &protocol.Vector{X: 0, Y: 0, Z: fa.Pos[3]},
				Reason:   protocol.TeleportReason_TeleportReason_FlowAction,
				GameCtx: &protocol.GameCtxPb{
					CtxType: protocol.GameCtxType_GameCtxType_FlowActionCtx,
					CtxInfo: &protocol.GameCtxPb_FlowAction{
						FlowAction: &protocol.FlowActionCtxPb{
							FlowListName: r.list,
							FlowId:       r.flow,
							StateId:      r.state,
							ActionId:     actionID,
						},
					},
				},
			})
			return true
		}
		s.Actions().RunDetached([]quests.QuestAction{fa.Action})
		return true
	}
	return false
}
