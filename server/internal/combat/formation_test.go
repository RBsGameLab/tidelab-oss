// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"slices"
	"testing"

	"tidelab/internal/save"
	"tidelab/protocol"
)

// specs/formation.md §6 (FormationTests).

func TestEmptySaveSeedsDefaultFormationAndSceneJoinSyncNotifies(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{})
	sink := &recordingSink{}
	f0 := ctx.Player.FormationInfo.Formations[0]
	var ids []int32
	for _, r := range f0.FilledRoles() {
		ids = append(ids, r.RoleID)
	}
	must(t, ctx.Player.FormationInfo.CurFormation == 0 && f0.CurRole == 1211 && slices.Equal(ids, []int32{1211, 1108, 1506}), ids)
	ctx.Formation.SpawnActiveFormation(1000)
	ctx.Formation.PushSceneJoinSync(sink)
	must(t, len(ofType[*protocol.SwitchBattleModeNotify](sink)) == 1, "battle mode")
	a := ofType[*protocol.FormationAttrNotify](sink)[0]
	var attrIDs []int32
	for _, x := range a.FormationAttrs {
		attrIDs = append(attrIDs, x.AttrId)
	}
	must(t, a.CurTime == FormationAttrCurTime && slices.Equal(attrIDs, []int32{1, 10, 14}), a)
	u := ofType[*protocol.UpdateFormationNotify](sink)[0]
	must(t, len(u.PlayersFormations[0].Formations) == 1 && len(u.PlayersFormations[0].Formations[0].RoleInfos) == 3, u)
}

func TestUpdateFormationDiffsEntitiesRolesBeforeConcomitantsAndSendsR114GroupData(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{})
	sink := &recordingSink{}
	fs := ctx.Player.FormationInfo.Formations
	fs[0].CurRole = 1108
	ctx.Formation.SpawnActiveFormation(1000)
	kept, rb, rc := fs[0].Roles[0].EntityID, fs[0].Roles[1].EntityID, fs[0].Roles[2].EntityID
	sink.clear()
	resp := ctx.Formation.HandleUpdateFormation(&protocol.UpdateFormationRequest{Formations: []*protocol.FightFormation{
		{FormationId: 1, IsCurrent: true, RoleIds: []int32{1211, 1105}}}}, 2000, sink)
	must(t, resp.ErrorCode == protocol.ErrorCode_ErrorCode_Success && fs[0].Roles[0].EntityID == kept, resp)
	rm := ofType[*protocol.EntityRemoveNotify](sink)
	must(t, len(rm) == 1 && rm[0].IsRemove, rm)
	var removed []int64
	for _, r := range rm[0].RemoveInfos {
		removed = append(removed, r.EntityId)
	}
	must(t, slices.Contains(removed, rb) && slices.Contains(removed, rc) && !slices.Contains(removed, kept), removed)
	adds := ofType[*protocol.EntityAddNotify](sink)
	must(t, len(adds) == 2 && !adds[0].IsAdd && len(adds[0].EntityPbs) == 1 && adds[0].EntityPbs[0].ConfigId == 1105 && len(adds[1].EntityPbs) == 3, adds)
	g := ofType[*protocol.UpdateSceneGroupDataNotify](sink)
	must(t, len(g) == 1 && g[0].PlayerSceneGroupDataList[0].CurrentGroup == 1 && g[0].PlayerSceneGroupDataList[0].PlayerSceneGroupData[0].Group == 1 &&
		len(g[0].PlayerSceneGroupDataList[0].PlayerSceneGroupData[0].FightRoleInfos) == 2, g)

	sink.clear()
	ctx.Formation.HandleUpdateFormation(&protocol.UpdateFormationRequest{Formations: []*protocol.FightFormation{
		{FormationId: 1, IsCurrent: true, RoleIds: []int32{1211}}}}, 3000, sink)
	must(t, fs[0].CurRole == 1211 && len(ofType[*protocol.EntityRemoveNotify](sink)[0].RemoveInfos) == 4, fs[0].CurRole)
}

func names4(fi *save.FormationInfoSave) []string {
	var out []string
	for _, f := range fi.Formations[:4] {
		n := ""
		if f.Name != nil {
			n = *f.Name
		}
		out = append(out, n)
	}
	return out
}

func TestReorderFormationShiftsTracksCurFormationAndNeverPushesUpdateFormationNotify(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{})
	fi := ctx.Player.FormationInfo
	for i, n := range []string{"A", "B", "C", "D"} {
		name := n
		fi.Formations[i].Name = &name
	}
	fi.CurFormation = 0
	ok := protocol.ErrorCode_ErrorCode_Success
	must(t, ctx.Formation.HandleReorderFormation(&protocol.ReorderFormationRequest{FormationId: 1, TargetId: 3}).ErrorCode == ok, "r1")
	must(t, slices.Equal(names4(fi), []string{"B", "C", "A", "D"}) && fi.CurFormation == 2, names4(fi), fi.CurFormation)
	must(t, ctx.Formation.HandleReorderFormation(&protocol.ReorderFormationRequest{FormationId: 1, TargetId: 4}).ErrorCode == ok, "r2")
	must(t, slices.Equal(names4(fi), []string{"C", "A", "D", "B"}) && fi.CurFormation == 1, names4(fi), fi.CurFormation)
	must(t, ctx.Formation.HandleReorderFormation(&protocol.ReorderFormationRequest{FormationId: 4, TargetId: 1}).ErrorCode == ok, "r3")
	must(t, slices.Equal(names4(fi), []string{"B", "C", "A", "D"}) && fi.CurFormation == 2, names4(fi), fi.CurFormation)
	unknown := protocol.ErrorCode_ErrorCode_ErrFormationUnknown
	must(t, ctx.Formation.HandleReorderFormation(&protocol.ReorderFormationRequest{FormationId: 0, TargetId: 2}).ErrorCode == unknown &&
		ctx.Formation.HandleReorderFormation(&protocol.ReorderFormationRequest{FormationId: 1, TargetId: 21}).ErrorCode == unknown, "unknown")
}

func TestRenameFormationAndSwitchRoleInsideCombatPackWork(t *testing.T) {
	dir := t.TempDir()
	ctx := createContext(t, dir, ctxOpts{})
	sink := &recordingSink{}
	ctx.Formation.SpawnActiveFormation(1000)
	r := ctx.Formation.HandleRenameFormation(&protocol.RenameFormationRequest{FormationId: 1, Name: "BossTeam"}, sink)
	saved := save.LoadPlayer(dir, 777).FormationInfo.Formations[0].Name
	must(t, r.ErrorCode == protocol.ErrorCode_ErrorCode_Success && len(ofType[*protocol.UpdateFormationNotify](sink)) == 1 && saved != nil && *saved == "BossTeam", r)
	e1211, e1108 := ctx.Formation.FindLiveRoleEntity(1211), ctx.Formation.FindLiveRoleEntity(1108)
	must(t, e1211.IsVisible && !e1108.IsVisible, "visibility")
	pack := &protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{{Message: &protocol.CombatSendData_Request{Request: &protocol.CombatRequestData{
		RequestId: 7, CombatCommon: &protocol.CombatCommon{EntityId: e1211.NetID},
		Message: &protocol.CombatRequestData_SwitchRoleRequest{SwitchRoleRequest: &protocol.SwitchRoleRequest{RoleId: 1108}}}}}}}
	resp := ctx.Combat.HandleCombatSendPack(pack, 2000, sink)
	var sw *protocol.SwitchRoleResponse
	for _, d := range resp.ReceivePackNotify.Data {
		if x := d.GetCombatResponseData().GetSwitchRoleResponse(); x != nil {
			sw = x
		}
	}
	must(t, sw != nil && sw.ErrorCode == protocol.ErrorCode_ErrorCode_Success && sw.RoleId == 1108 && !e1211.IsVisible && e1108.IsVisible, sw)
	must(t, save.LoadPlayer(dir, 777).FormationInfo.Formations[0].CurRole == 1108, "cur role not saved")
}
