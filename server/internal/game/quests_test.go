// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
	"tidelab/protocol"
)

func parseJSON(t *testing.T, s string) data.Value {
	t.Helper()
	v, err := data.ParseJSON([]byte(s))
	if err != nil {
		t.Fatalf("ParseJSON(%s): %v", s, err)
	}
	return v
}

func ofType[T proto.Message](c *recording) []T {
	var out []T
	for _, m := range c.msgs {
		if v, ok := m.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func (r *rig) rebuild() {
	r.tables.Reindex()
	r.data = world.NewWorldData(r.tables, map[int32][]int32{906: {4, 5}})
	r.game = nil
}

func (r *rig) enterWithQuests(cat *quests.Catalog, reconnect bool) (*PlayerSession, *recording) {
	g := r.Game()
	if cat != nil {
		g.EnableQuests(cat)
	}
	c := &recording{id: rigPlayer}
	p := g.NewSession(c)
	p.Enter(reconnect)
	return p, c
}

func qNode(id, parent int32, typ, cond string, kids ...int32) quests.NodeDef {
	return quests.NodeDef{ID: id, Parent: parent, Type: typ, Cond: cond, Kids: kids}
}

func questSessionCatalog(t *testing.T) *quests.Catalog {
	t.Helper()
	talk := &quests.FlowRef{FlowListName: "剧情_测试", FlowID: 3, StateID: 1}
	ds := quests.NewQuestDataSet()
	n5_2 := qNode(2, 1, "ChildQuest", "DoInteract")
	n5_2.Options = []quests.QuestOption{{EntityID: 40, Guid: "g40", TidContent: "t", Flow: talk}}
	ds.Quests = append(ds.Quests, quests.QuestDef{
		ID: 5, Type: 1, Name: "五", PreQuest: 1,
		Nodes: []quests.NodeDef{
			qNode(1, 0, "Sequence", "", 2, 3, 4),
			n5_2,
			qNode(3, 1, "ChildQuest", "ReachArea"),
			qNode(4, 1, "QuestSucceed", ""),
		},
	})
	n7_2 := qNode(2, 1, "ChildQuest", "Kill")
	n7_2.Targets = []int64{300}
	ds.Quests = append(ds.Quests, quests.QuestDef{
		ID: 7, Type: 1, Name: "七", PreQuest: 1,
		Nodes: []quests.NodeDef{
			qNode(1, 0, "Sequence", "", 2, 3),
			n7_2,
			qNode(3, 1, "QuestSucceed", ""),
		},
	})
	n8_2 := qNode(2, 1, "ChildQuest", "ReachArea")
	n8_2.Enter = []quests.QuestAction{{
		Name: "AddFlowInteractOption",
		Raw:  parseJSON(t, `{"EntityId":41,"Option":{"Guid":"opt41","TidContent":"聊聊","Type":{"Type":"Flow","Flow":{"FlowListName":"剧情_选项","FlowId":4,"StateId":1}}}}`),
	}}
	ds.Quests = append(ds.Quests, quests.QuestDef{
		ID: 8, Type: 1, Name: "八", PreQuest: 1,
		Nodes: []quests.NodeDef{
			qNode(1, 0, "Sequence", "", 2, 3),
			n8_2,
			qNode(3, 1, "QuestSucceed", ""),
		},
	})
	ds.Owned[quests.EntityPair{Map: 8, Entity: 300}] = struct{}{}
	ds.FlowActions["剧情_测试_3_1"] = []quests.FlowActionDef{
		quests.ParseFlowAction(parseJSON(t, `{"Name":"AwakeEntity","Params":{"EntityIds":[500,501]},"ActionId":1}`)),
		quests.ParseFlowAction(parseJSON(t, `{"Name":"DestroyEntity","Params":{"EntityIds":[502]},"ActionId":2}`)),
		quests.ParseFlowAction(parseJSON(t, `{"Name":"SetPlayerPos","Pos":[100,200,300,90],"ActionId":3}`)),
	}
	return quests.NewCatalog(ds)
}

func addQuestNPC(s *PlayerSession, cfgID int32) int64 {
	npc := s.Scene.Add(&world.Entity{ConfigID: cfgID, EntityType: world.TypeNpc, ConfigType: world.ConfigLevel})
	s.Quests().OnEntitySpawned(int64(cfgID), npc.ID)
	return npc.ID
}

// ----- QuestSessionTests -----

func TestJoinSyncsQuestsAndStartsUnlocked(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, c := r.enterWithQuests(questSessionCatalog(t), false)

	names := c.names()
	after := slices.Index(names, "AfterJoinSceneNotify")
	if after < 0 || names[after+1] != "QuestFinishListNotify" || names[after+2] != "QuestListNotify" {
		t.Fatalf("unexpected join order around AfterJoinSceneNotify: %v", names)
	}
	finishes := ofType[*protocol.QuestFinishListNotify](c)
	if len(finishes) != 1 || !slices.Contains(finishes[0].QuestId, 1) {
		t.Fatalf("QuestFinishListNotify = %v", finishes)
	}
	var started []int32
	for _, n := range ofType[*protocol.QuestStateUpdateNotify](c) {
		started = append(started, n.QuestId)
	}
	slices.Sort(started)
	if !slices.Equal(started, []int32{5, 7, 8}) {
		t.Fatalf("started quests = %v, want [5 7 8]", started)
	}
	if got := len(ofType[*protocol.BehaviorTreeInfoNotify](c)); got != 3 {
		t.Fatalf("BehaviorTreeInfoNotify count = %d, want 3", got)
	}
	if !s.Quests().IsRunning(5) {
		t.Fatalf("quest 5 not running")
	}
	if names[len(names)-1] != "UpdateFormationNotify" {
		t.Fatalf("last push = %s, want UpdateFormationNotify", names[len(names)-1])
	}
}

func TestTreesGoOncePerSessionAndAgainAfterReconnect(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	cat := questSessionCatalog(t)
	s, c := r.enterWithQuests(cat, false)
	c.msgs = nil
	s.ChangeScene(8, nil)

	lists := ofType[*protocol.QuestListNotify](c)
	if len(lists) != 1 {
		t.Fatalf("QuestListNotify count = %d, want 1", len(lists))
	}
	var ids []int32
	for _, q := range lists[0].Quests {
		ids = append(ids, q.QuestId)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int32{5, 7, 8}) {
		t.Fatalf("QuestListNotify quests = %v", ids)
	}
	if len(ofType[*protocol.BehaviorTreeInfoNotify](c)) != 0 {
		t.Fatalf("expected no tree resends on same-session scene change")
	}

	s.Quests().RestorePending = false
	again, c2 := r.enterWithQuests(cat, true)
	if again.Quests() != s.Quests() {
		t.Fatalf("reconnect created a new QuestEngine instead of reusing PlayerQuests")
	}
	if again.Quests().RestorePending {
		t.Fatalf("reconnect armed RestorePending")
	}
	var treeIDs []int32
	for _, n := range ofType[*protocol.BehaviorTreeInfoNotify](c2) {
		for _, ti := range n.TreeInfos {
			treeIDs = append(treeIDs, int32(ti.TreeIncId))
		}
	}
	slices.Sort(treeIDs)
	if !slices.Equal(treeIDs, []int32{5, 7, 8}) {
		t.Fatalf("reconnect tree ids = %v, want [5 7 8]", treeIDs)
	}
}

func TestLoginArmsRestoreFixesAndDropsFlowQueue(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	cat := questSessionCatalog(t)
	s, _ := r.enterWithQuests(cat, false)
	s.Flows().Send(&protocol.FlowStartNotify{FlowIncId: 9}, s.Sink())
	s.Flows().Send(&protocol.FlowStartNotify{FlowIncId: 10}, s.Sink())
	if s.Flows().Playing() == nil || *s.Flows().Playing() != 9 {
		t.Fatalf("playing = %v, want 9", s.Flows().Playing())
	}

	again, c := r.enterWithQuests(cat, true)
	if again.Flows().Playing() == nil || *again.Flows().Playing() != 10 {
		t.Fatalf("after reconnect playing = %v, want 10", again.Flows().Playing())
	}
	if got := last[*protocol.FlowStartNotify](t, c).FlowIncId; got != 10 {
		t.Fatalf("last FlowStartNotify = %d, want 10", got)
	}

	fresh, _ := r.enterWithQuests(cat, false)
	if fresh.Flows().Playing() != nil || !fresh.Quests().RestorePending {
		t.Fatalf("fresh login playing=%v RestorePending=%v", fresh.Flows().Playing(), fresh.Quests().RestorePending)
	}
}

func TestDynamicOptionPlaysFlowAndFlowEndMovesQueueBeforeQuest(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, c := r.enterWithQuests(questSessionCatalog(t), false)
	npc := addQuestNPC(s, 40)
	if got := last[*protocol.EntityAddDynamicInteractNotify](t, c).EntityId; got != npc {
		t.Fatalf("add notify entity = %d, want %d", got, npc)
	}

	c.msgs = nil
	resp := s.EntityDynamicInteract(&protocol.EntityDynamicInteractRequest{EntityId: npc, OptionGuid: "g40"})
	if !resp.Interacting {
		t.Fatalf("expected Interacting=true")
	}
	flows := ofType[*protocol.FlowStartNotify](c)
	if len(flows) != 1 || flows[0].FlowListName != "剧情_测试" || flows[0].GameCtx.CtxType != protocol.GameCtxType_GameCtxType_DoInteractChildQuestNode {
		t.Fatalf("unexpected FlowStartNotify: %v", flows)
	}
	flow := flows[0]
	s.RunAfterResponse()

	// Another flow waits behind it (R-89).
	s.Flows().Send(&protocol.FlowStartNotify{FlowIncId: 77, FlowListName: "后"}, s.Sink())
	if len(ofType[*protocol.FlowStartNotify](c)) != 1 {
		t.Fatalf("queued flow went out early")
	}

	c.msgs = nil
	if endResp := s.FlowEnd(&protocol.FlowEndRequest{FlowIncId: flow.FlowIncId}); endResp.Code != protocol.ErrorCode_ErrorCode_Success {
		t.Fatalf("FlowEnd code = %v", endResp.Code)
	}
	names := c.names()
	if len(names) == 0 || names[0] != "FlowStartNotify" {
		t.Fatalf("expected FlowStartNotify first on FlowEnd, got %v", names)
	}
	if got := last[*protocol.FlowStartNotify](t, c).FlowIncId; got != 77 {
		t.Fatalf("next flow = %d, want 77", got)
	}
	freed := false
	for _, n := range ofType[*protocol.EntityInteractingNotify](c) {
		if n.EntityId == npc && !n.IsInteracting {
			freed = true
		}
	}
	if !freed {
		t.Fatalf("NPC was not freed after FlowEnd")
	}
	var q5Conds []string
	for _, st := range s.Quests().ActiveSteps(false) {
		if st.Tree == 5 {
			q5Conds = append(q5Conds, st.Def.Cond)
		}
	}
	if !slices.Equal(q5Conds, []string{"ReachArea"}) {
		t.Fatalf("quest 5 active conds = %v, want [ReachArea]", q5Conds)
	}
	if slices.Contains(names, "JSPatchNotify") {
		t.Fatalf("unexpected JSPatchNotify")
	}
	s.RunAfterResponse()
}

func TestUnknownOptionDoesNotLockEntity(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, _ := r.enterWithQuests(questSessionCatalog(t), false)
	npc := addQuestNPC(s, 40)
	if s.EntityDynamicInteract(&protocol.EntityDynamicInteractRequest{EntityId: npc, OptionGuid: "other"}).Interacting {
		t.Fatalf("unknown option returned Interacting=true")
	}
}

func TestSubmitNodeEchoesAndFinishesQuest(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, _ := r.enterWithQuests(questSessionCatalog(t), false)
	npc := addQuestNPC(s, 40)
	s.EntityDynamicInteract(&protocol.EntityDynamicInteractRequest{EntityId: npc, OptionGuid: "g40"})
	s.FlowEnd(&protocol.FlowEndRequest{FlowIncId: *s.Flows().Playing()})

	bad := s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: 5, NodeId: 2})
	if bad.ErrorId != protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive {
		t.Fatalf("inactive node ErrorId = %v", bad.ErrorId)
	}
	ok := s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: 5, NodeId: 3})
	if ok.TreeOwnerId != 777 || ok.TreeIncId != 5 || ok.NodeId != 3 || ok.ErrorId != protocol.ErrorCode_ErrorCode_Success {
		t.Fatalf("SubmitNode response = %+v", ok)
	}
	s.RunAfterResponse()
	if !s.Quests().IsFinished(5) {
		t.Fatalf("quest 5 not finished")
	}
	if !strings.Contains(r.read("quests"), "F 5") {
		t.Fatalf("quests file missing F 5: %q", r.read("quests"))
	}
}

func TestKillCountsForQuestAndSleepsQuestOwnedMonster(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, _ := r.enterWithQuests(questSessionCatalog(t), false)

	s.Systems.Hooks(s).OnMonsterKilled(&combat.CombatEntity{NetID: 9001, ConfigID: 300})
	if !s.Quests().IsFinished(7) {
		t.Fatalf("quest 7 not finished after kill")
	}
	if s.World.State.IsAwake(300) || !s.World.State.IsGone(300, r.clock.Seconds()) {
		t.Fatalf("quest-owned monster 300 should be asleep and gone")
	}
	if !strings.Contains(r.read("world_state"), "300 0") {
		t.Fatalf("world_state missing 300 0: %q", r.read("world_state"))
	}
}

func TestRoverGenderReachesEngine(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, _ := r.enterWithQuests(questSessionCatalog(t), false)
	if s.Quests().PlayerMale == nil || !*s.Quests().PlayerMale {
		t.Fatalf("expected PlayerMale=true on join")
	}
	s.Systems.Hooks(s).OnGenderChanged(false)
	if s.Quests().PlayerMale == nil || *s.Quests().PlayerMale {
		t.Fatalf("expected PlayerMale=false after gender change")
	}
}

func TestLevelPlayStatesComeFromEngine(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, _ := r.enterWithQuests(questSessionCatalog(t), false)

	resp := s.LevelPlayStateList(&protocol.LevelPlayStateListRequest{
		InstLevelPlayStateReqs: []*protocol.InstLevelPlayStateReq{{InstId: 8, LevelPlayIds: []int32{11, 12}}},
	})
	if len(resp.InstLevelPlayStateRsps) != 1 {
		t.Fatalf("InstLevelPlayStateRsps len = %d", len(resp.InstLevelPlayStateRsps))
	}
	rsp := resp.InstLevelPlayStateRsps[0]
	if rsp.InstId != 8 || rsp.LevelPlayStateDict[11] != 0 || rsp.CompleteNumberDict[12] != 0 ||
		rsp.HideDict[11] || !rsp.IsUnlockDict[12] || len(rsp.HideGroupInfoDict) != 0 {
		t.Fatalf("unexpected LevelPlayStateList rsp: %+v", rsp)
	}
}

func TestChangeEntityStateTakesConfigIDAndRemembersState(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, _ := r.enterWithQuests(questSessionCatalog(t), false)

	s.ChangeEntityState(&protocol.ChangeEntityStateRequest{EntityId: 0, StateTagId: 5})
	s.ChangeEntityState(&protocol.ChangeEntityStateRequest{EntityId: 123456, StateTagId: 99})
	s.RunAfterResponse()
	if st, ok := s.World.State.StateOverride(123456); !ok || st != 99 {
		t.Fatalf("state override(123456) = (%d, %v), want (99, true)", st, ok)
	}
	if _, ok := s.World.State.StateOverride(0); ok {
		t.Fatalf("entity 0 should not be recorded")
	}
	if !strings.Contains(r.read("world_state"), "123456 99 0 0") {
		t.Fatalf("world_state = %q", r.read("world_state"))
	}
}

func TestInteractionWithoutOptionSendsFallbackActionAfterResponse(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, c := r.enterWithQuests(questSessionCatalog(t), false)
	npc := addQuestNPC(s, 40)
	c.msgs = nil

	resp := s.EntityInteract(&protocol.EntityInteractRequest{EntityId: npc, OptionIndex: 0})
	if resp.Interacting || len(c.msgs) != 0 {
		t.Fatalf("expected Interacting=false and no pushes before RunAfterResponse")
	}
	s.RunAfterResponse()
	act := last[*protocol.ActionNotify](t, c)
	if act.IncId != 1 || act.TotalCount != 1 || act.StartIndex != 0 || act.EndIndex != 0 || act.NeedFinishReq {
		t.Fatalf("unexpected fallback ActionNotify: %+v", act)
	}
	ni := act.GameCtx.GetNormalInteract()
	if act.GameCtx.CtxType != protocol.GameCtxType_GameCtxType_NormalInteract || ni == nil || ni.EntityCtx.ConfigId != 40 || ni.EntityCtx.IncId != npc {
		t.Fatalf("unexpected NormalInteract ctx: %+v", act.GameCtx)
	}
}

func TestQuestItemsComeAndGoWithNotifies(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, c := r.enterWithQuests(questSessionCatalog(t), false)
	c.msgs = nil

	s.Actions().RunDetached([]quests.QuestAction{
		{Name: "GetItem", Raw: parseJSON(t, `{"Items":[{"ItemId":41100011,"Count":2}]}`)},
	})
	add := last[*protocol.NormalItemAddNotify](t, c)
	if len(add.NormalItemList) != 1 || add.NormalItemList[0].Id != 41100011 || add.NormalItemList[0].Count != 2 {
		t.Fatalf("NormalItemAddNotify = %+v", add)
	}
	if s.ItemCount(41100011) != 2 {
		t.Fatalf("inventory count(41100011) = %d", s.ItemCount(41100011))
	}
	s.Actions().RunDetached([]quests.QuestAction{
		{Name: "DestroyQuestItem", Raw: parseJSON(t, `{"ItemId":41100011,"IsAll":true}`)},
	})
	rem := last[*protocol.NormalItemRemoveNotify](t, c)
	if !slices.Equal(rem.NormalItemIdList, []int32{41100011}) || s.ItemCount(41100011) != 0 {
		t.Fatalf("remove = %+v, count = %d", rem, s.ItemCount(41100011))
	}
}

func TestQuestRequestsRegistered(t *testing.T) {
	r := newRig(t)
	h := r.Game().Handlers()
	for _, id := range []protocol.MessageID{
		protocol.IDSubmitNodeRequest, protocol.IDHandInItemRequest, protocol.IDSubmitAwakeAndLoadEntityNodeRequest,
		protocol.IDQuestReviewInfoRequest, protocol.IDSetFocusModeDeterCondRequest, protocol.IDQuestRedDotRequest,
		protocol.IDTraceQuestRequest, protocol.IDLevelPlayStateListRequest, protocol.IDQuestBranchRequest,
		protocol.IDLevelPlayVarRequest, protocol.IDLevelPlayStateRequest, protocol.IDActionFinishRequest,
		protocol.IDOpenSystemBoardResultRequest, protocol.IDEntityInteractRequest, protocol.IDEntityDynamicInteractRequest,
		protocol.IDChangeEntityStateRequest, protocol.IDEntitySendEventRequest, protocol.IDTargetGearHitRequest,
		protocol.IDElevatorStateChangeRequest, protocol.IDFlowEndRequest, protocol.IDFlowActionRequest,
	} {
		if !h.HasRequest(id) {
			t.Errorf("request handler %v not registered", id)
		}
	}
}

func TestFlowWakesActorsAndSettlesAtEnd(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, c := r.enterWithQuests(questSessionCatalog(t), false)
	npc := addQuestNPC(s, 40)

	s.EntityDynamicInteract(&protocol.EntityDynamicInteractRequest{EntityId: npc, OptionGuid: "g40"})
	inc := last[*protocol.FlowStartNotify](t, c).FlowIncId
	if !s.World.State.IsAwake(500) || !s.World.State.IsAwake(501) || !s.Spawner.Marked {
		t.Fatalf("flow start did not wake actors 500/501 and mark spawner")
	}

	c.msgs = nil
	if resp := s.FlowAction(&protocol.FlowActionRequest{FlowIncId: inc, ActionId: 3}); resp.Code != protocol.ErrorCode_ErrorCode_Success {
		t.Fatalf("FlowAction code = %v", resp.Code)
	}
	tp := last[*protocol.TeleportNotify](t, c)
	if tp.Reason != protocol.TeleportReason_TeleportReason_FlowAction ||
		tp.Position.X != 100 || tp.Position.Y != 200 || tp.Position.Z != 300 || tp.Rotation.Z != 90 ||
		tp.GameCtx.CtxType != protocol.GameCtxType_GameCtxType_FlowActionCtx {
		t.Fatalf("unexpected flow TeleportNotify: %+v", tp)
	}
	fa := tp.GameCtx.GetFlowAction()
	if fa == nil || fa.FlowListName != "剧情_测试" || fa.FlowId != 3 || fa.StateId != 1 || fa.ActionId != 3 {
		t.Fatalf("unexpected FlowActionCtx: %+v", fa)
	}
	if resp := s.FlowAction(&protocol.FlowActionRequest{FlowIncId: 12345, ActionId: 1}); resp.Code != protocol.ErrorCode_ErrorCode_Success {
		t.Fatalf("unknown FlowAction code = %v", resp.Code)
	}

	s.FlowEnd(&protocol.FlowEndRequest{FlowIncId: inc})
	if s.World.State.IsAwake(502) || !s.World.State.IsGone(502, r.clock.Seconds()) || s.QuestState.FlowActions.RunningCount() != 0 {
		t.Fatalf("flow end did not settle DestroyEntity 502")
	}
}

func TestStepOptionAnnouncedPickedAndReleasedWithQuest(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	s, c := r.enterWithQuests(questSessionCatalog(t), false)
	npc := s.Scene.Add(&world.Entity{ConfigID: 41, EntityType: world.TypeNpc, ConfigType: world.ConfigLevel})
	c.msgs = nil
	s.Quests().OnEntitySpawned(41, npc.ID)

	add := last[*protocol.EntityAddDynamicInteractNotify](t, c)
	if add.EntityId != npc.ID || add.OptionGuid != "opt41" || add.Text != "聊聊" ||
		add.GameCtx.CtxType != protocol.GameCtxType_GameCtxType_ChildQuestNodeEnterAction {
		t.Fatalf("unexpected dynamic option notify: %+v", add)
	}

	resp := s.EntityDynamicInteract(&protocol.EntityDynamicInteractRequest{EntityId: npc.ID, OptionGuid: "opt41"})
	if !resp.Interacting {
		t.Fatalf("expected Interacting=true")
	}
	flow := last[*protocol.FlowStartNotify](t, c)
	di := flow.GameCtx.GetDynamicInteract()
	if flow.FlowListName != "剧情_选项" || flow.FlowId != 4 || flow.StateId != 1 ||
		flow.GameCtx.CtxType != protocol.GameCtxType_GameCtxType_DynamicInteract ||
		di == nil || di.OptionGuid != "opt41" || di.FinalOptionCtx.CtxType != protocol.GameCtxType_GameCtxType_ChildQuestNodeEnterAction {
		t.Fatalf("unexpected picked option flow: %+v", flow)
	}
	s.FlowEnd(&protocol.FlowEndRequest{FlowIncId: flow.FlowIncId})
	if s.QuestState.Options.Count() != 1 {
		t.Fatalf("option count = %d, want 1", s.QuestState.Options.Count())
	}

	c.msgs = nil
	s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: 8, NodeId: 2})
	if !s.Quests().IsFinished(8) || s.QuestState.Options.Count() != 0 {
		t.Fatalf("quest 8 did not release its option")
	}
	rem := last[*protocol.EntityRemoveDynamicInteractNotify](t, c)
	if rem.EntityId != npc.ID || rem.OptionGuid != "opt41" {
		t.Fatalf("unexpected remove option notify: %+v", rem)
	}
}

func TestWithoutQuestDataJoinSendsEmptySync(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	s, c := r.enter(false)
	if s.Quests() != nil {
		t.Fatalf("expected nil Quests without catalog")
	}
	if len(ofType[*protocol.BehaviorTreeInfoNotify](c)) != 1 || len(ofType[*protocol.QuestReadyListNotify](c)) != 1 {
		t.Fatalf("expected empty sync notifies without catalog")
	}
}

// ----- FlowQueueTests -----

func flowNotify(inc int64, name string) *protocol.FlowStartNotify {
	return &protocol.FlowStartNotify{FlowIncId: inc, FlowListName: name, FlowId: 1, StateId: 1}
}

func TestFlowQueueOrderAndLifecycle(t *testing.T) {
	q := NewFlowQueue()
	if got := q.Start(flowNotify(500003, "序幕")); got == nil || got.FlowIncId != 500003 {
		t.Fatalf("first flow = %v", got)
	}
	if q.Start(flowNotify(500004, "序幕")) != nil || q.Start(flowNotify(500005, "序幕")) != nil {
		t.Fatalf("queued flows should return nil")
	}
	if q.Ended(500005) != nil || q.Ended(42) != nil || *q.Playing() != 500003 {
		t.Fatalf("ending non-playing flow moved queue")
	}
	second := q.Ended(500003)
	if second == nil || second.FlowIncId != 500004 || second.FlowListName != "序幕" {
		t.Fatalf("second = %v", second)
	}
	if got := q.Ended(500004); got == nil || got.FlowIncId != 500005 {
		t.Fatalf("third = %v", got)
	}
	if q.Ended(500005) != nil || q.Playing() != nil {
		t.Fatalf("queue should be idle")
	}
	if q.Start(flowNotify(500006, "椿线")) == nil {
		t.Fatalf("idle queue should send immediately")
	}

	// Login reset drops queued flows.
	q.Start(flowNotify(2, "b"))
	q.LoginReset()
	if q.Playing() != nil || q.Start(flowNotify(3, "c")) == nil {
		t.Fatalf("LoginReset failed")
	}

	// Queued flow keeps its own clone.
	n := flowNotify(4, "剧情_甲")
	q.Start(n)
	n.FlowListName = "x"
	if got := q.Ended(3); got == nil || got.FlowListName != "剧情_甲" {
		t.Fatalf("queued flow was not cloned: %v", got)
	}

	// Scene switch ends the playing flow and sends the next.
	q2 := NewFlowQueue()
	sink := &recording{id: 1}
	q2.Send(flowNotify(1, "a"), sink)
	q2.Send(flowNotify(2, "b"), sink)
	q2.SceneChanged(sink)
	if q2.Playing() == nil || *q2.Playing() != 2 {
		t.Fatalf("after SceneChanged playing = %v", q2.Playing())
	}
	var ids []int64
	for _, f := range ofType[*protocol.FlowStartNotify](sink) {
		ids = append(ids, f.FlowIncId)
	}
	if !slices.Equal(ids, []int64{1, 2}) {
		t.Fatalf("SceneChanged pushes = %v, want [1 2]", ids)
	}
}

// ----- EntityActionsTests -----

func TestChestTierAndFromEntityData(t *testing.T) {
	cases := []struct {
		name string
		tier int
	}{
		{"宝箱_豪华", 4}, {"珍稀宝箱", 4}, {"丰厚的箱子", 3}, {"标准宝箱", 2}, {"简易宝箱", 1},
	}
	for _, tc := range cases {
		if got := ChestTier(tc.name); got != tc.tier {
			t.Errorf("ChestTier(%q) = %d, want %d", tc.name, got, tc.tier)
		}
	}
	a := EntityActionFromData(parseJSON(t, `{"Name":"SetPlayerPos","Params":{"TelePortConfig":{"Type":0,"TargetPos":{"X":1,"Y":2,"Z":3,"A":90}}}}`))
	if typ, ok := quests.ActionInt(a, "TelePortConfig", "Type"); !ok || typ != 0 {
		t.Fatalf("Type = (%d, %v)", typ, ok)
	}
	if yaw, ok := quests.ActionFloat(a, "TelePortConfig", "TargetPos", "A"); !ok || yaw != 90 {
		t.Fatalf("A = (%v, %v)", yaw, ok)
	}
	b := EntityActionFromData(parseJSON(t, `{"Name":"ChangeEntityState","Params":{"EntityId":7,"State":"关卡.Common.状态.激活"}}`))
	if b.Params == nil || b.Params.EntityID == nil || *b.Params.EntityID != 7 || b.Params.State == nil || *b.Params.State != "关卡.Common.状态.激活" {
		t.Fatalf("ChangeEntityState params = %+v", b.Params)
	}
}

// ----- EntityGroupsTests -----

type stubGroupProbe struct {
	states map[int64]int32
	dead   map[int64]bool
}

func (p stubGroupProbe) State(id int64) (int32, bool) { v, ok := p.states[id]; return v, ok }
func (p stubGroupProbe) Dead(id int64) bool           { return p.dead[id] }
func (p stubGroupProbe) Tag(name string) (int32, bool) {
	if strings.HasSuffix(name, "完成") {
		return 7, true
	}
	return 1, true
}

func TestEntityGroupConditions(t *testing.T) {
	done := parseJSON(t, `{"Conditions":[{"EntityId":1,"State":"关卡.Common.状态.完成","IsDead":true},{"EntityId":2,"State":"关卡.Common.状态.完成","IsDead":true}]}`)
	if GroupHolds(done, stubGroupProbe{}) {
		t.Fatal("empty probe should not hold")
	}
	if !GroupHolds(done, stubGroupProbe{states: map[int64]int32{1: 7}, dead: map[int64]bool{2: true}}) {
		t.Fatal("1 done + 2 dead should hold")
	}
	if GroupHolds(done, stubGroupProbe{states: map[int64]int32{1: 7, 2: 1}}) {
		t.Fatal("2 still normal should not hold")
	}
	any := parseJSON(t, `{"Count":1,"Conditions":[{"EntityId":1,"State":"关卡.Common.状态.完成","IsDead":true},{"EntityId":2,"State":"关卡.Common.状态.完成","IsDead":true}]}`)
	if !GroupHolds(any, stubGroupProbe{states: map[int64]int32{1: 7}}) {
		t.Fatal("Count=1 with 1 done should hold")
	}
	alive := parseJSON(t, `{"Conditions":[{"EntityId":1,"State":"关卡.Common.状态.完成"}]}`)
	if GroupHolds(alive, stubGroupProbe{dead: map[int64]bool{1: true}}) {
		t.Fatal("dead without IsDead should not hold")
	}
	if GroupHolds(parseJSON(t, `{"Conditions":[]}`), stubGroupProbe{}) || GroupHolds(nil, stubGroupProbe{}) {
		t.Fatal("empty/nil conditions should never hold")
	}
}

func TestEntityGroupFinishesAndRunsTriggerInSession(t *testing.T) {
	r := newRig(t)
	r.tables.LevelEntityConfig = []*data.LevelEntityConfig{{
		MapId: 8, EntityId: 900, Name: "机关组", BlueprintType: "TeleControlGroup",
		ComponentsData: parseJSON(t, `{
			"EntityGroupComponent": {
				"FinishState": {"GroupCondition": {"Conditions": [
					{"EntityId": 901, "State": "关卡.Common.状态.完成"},
					{"EntityId": 902, "State": "关卡.Common.状态.完成", "IsDead": true}
				]}},
				"StateTriggers": [{
					"GroupCondition": {"Conditions": [{"EntityId": 901, "State": "关卡.Common.状态.完成"}]},
					"SuccessActions": [{"Name": "ChangeEntityState", "Params": {"EntityId": 903, "State": "关卡.Common.状态.激活"}}]
				}]
			}
		}`),
	}}
	r.rebuild()
	r.saveLivePlayer()
	r.save("world_state", "902 0 9999999999 0\n")
	s, _ := r.enter(false)
	done := world.TagID(r.tables, EntityGroupFinished)
	active := world.TagID(r.tables, "关卡.Common.状态.激活")

	s.ChangeEntityState(&protocol.ChangeEntityStateRequest{EntityId: 901, StateTagId: done})
	s.RunAfterResponse()
	if st, ok := s.World.State.StateOverride(900); !ok || st != done {
		t.Fatalf("group 900 state = (%d, %v), want %d", st, ok, done)
	}
	if st, ok := s.World.State.StateOverride(903); !ok || st != active {
		t.Fatalf("barrier 903 state = (%d, %v), want %d", st, ok, active)
	}

	// Trigger fires only once while its condition keeps holding.
	s.World.State.SetState(903, 0)
	s.ChangeEntityState(&protocol.ChangeEntityStateRequest{EntityId: 901, StateTagId: done})
	s.RunAfterResponse()
	if _, ok := s.World.State.StateOverride(903); ok {
		t.Fatalf("trigger fired twice while still holding")
	}
}

// ----- HideGroups, Prefabs, Presentation, Vehicles in session -----

func TestQuestClearingsLastUntilItEnds(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")
	r.save(quests.HideGroupsFileName, "+q_555_old\n")

	ds := quests.NewQuestDataSet()
	n2 := qNode(2, 1, "ChildQuest", "ReachArea")
	n2.Enter = []quests.QuestAction{
		{Name: "HideGroup", Raw: parseJSON(t, `{"GroupKey":"q_9_镇"}`)},
		{Name: "HideSpecificEntities", Raw: parseJSON(t, `{"EntityIds":[70]}`)},
	}
	ds.Quests = append(ds.Quests, quests.QuestDef{
		ID: 9, Type: 1, Name: "九", PreQuest: 1,
		Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2, 3), n2, qNode(3, 1, "QuestSucceed", "")},
	})
	ds.HideGroups["q_9_镇"] = quests.HideGroupDef{Key: "q_9_镇", IDs: []quests.EntityPair{{Map: 8, Entity: 60}, {Map: 8, Entity: 61}}}
	s, _ := r.enterWithQuests(quests.NewCatalog(ds), false)
	h := s.QuestState.HideGroups

	if h.IsActive("q_555_old") || !h.IsActive("q_9_镇") || !h.IsHidden(8, 60, false) || h.IsHidden(8, 60, true) || !h.IsHidden(8, 70, true) {
		t.Fatalf("unexpected clearing state")
	}
	wantDisk := "+q_555_old\n-q_555_old\n+q_9_镇\n+q_9_spec_8_70\n"
	if got := r.read(quests.HideGroupsFileName); got != wantDisk {
		t.Fatalf("hidden_groups = %q, want %q", got, wantDisk)
	}

	s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: 9, NodeId: 2})
	if !s.Quests().IsFinished(9) || h.Count() != 0 {
		t.Fatalf("quest 9 finish did not release clearings")
	}
	if got := r.read(quests.HideGroupsFileName); !strings.HasSuffix(got, "-q_9_镇\n-q_9_spec_8_70\n") {
		t.Fatalf("hidden_groups after finish = %q", got)
	}
}

func TestPrefabSpawnsAtNextRefreshAndDeathEndsMonsterCreator(t *testing.T) {
	r := newRig(t)
	r.tables.BlueprintConfig = append(r.tables.BlueprintConfig, &data.BlueprintConfig{BlueprintType: "Monster001", EntityType: "Monster", EntityLogic: "Monster", HalfHeight: 100})
	r.tables.TemplateConfig = append(r.tables.TemplateConfig, &data.TemplateConfig{Id: 7, BlueprintType: "Monster001", ComponentsData: parseJSON(t, `{"BaseInfoComponent":{"Camp":1},"AttributeComponent":{"PropertyId":90000015,"Level":10}}`)})
	r.tables.LevelEntityConfig = []*data.LevelEntityConfig{{
		MapId: 8, EntityId: 5000, BlueprintType: "Motor",
		Transform: []*data.IntVector{{X: -11100000, Y: 7400000, Z: 700000}, {}},
	}}
	r.rebuild()
	r.saveLivePlayer()
	r.save("quests", "F 1\n")

	ds := quests.NewQuestDataSet()
	prefabID := int32(3000)
	varName := "M"
	posEnt := int64(5000)
	n2 := qNode(2, 1, "ChildQuest", "MonsterCreator")
	n2.Var = "M"
	n2.Enter = []quests.QuestAction{{Name: "CreatePrefab", Params: &quests.ActionParams{PrefabID: &prefabID, Var: &varName, PosEntityID: &posEnt}}}
	ds.Quests = append(ds.Quests, quests.QuestDef{
		ID: 13, Type: 1, Name: "十三", PreQuest: 1,
		Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2, 3), n2, qNode(3, 1, "QuestSucceed", "")},
	})
	ds.Prefabs[3000] = quests.PrefabDef{
		ID: 3000,
		Entities: []quests.PrefabEntity{
			{ID: 1, BlueprintType: "Monster001", Pos: [3]float32{10, 20, 0}, Rot: [3]float32{0, 0, 90}},
			{ID: 2, BlueprintType: "Motor"},
		},
	}
	s, c := r.enterWithQuests(quests.NewCatalog(ds), false)
	q := s.QuestState
	if len(q.Prefabs.Queue()) != 1 {
		t.Fatalf("expected 1 queued prefab, got %d", len(q.Prefabs.Queue()))
	}

	s.Heartbeat()
	if len(q.Prefabs.Queue()) != 0 || len(q.Prefabs.Groups()) != 1 || len(q.Prefabs.Groups()[0].Alive) != 1 {
		t.Fatalf("prefab did not spawn cleanly: queue=%d groups=%d", len(q.Prefabs.Queue()), len(q.Prefabs.Groups()))
	}
	g := q.Prefabs.Groups()[0]
	net := g.Alive[0]
	var pb *protocol.EntityPb
	for _, n := range ofType[*protocol.EntityAddNotify](c) {
		for _, e := range n.EntityPbs {
			if e.Id == net {
				pb = e
			}
		}
	}
	if pb == nil || pb.ConfigType != world.ConfigPrefab || pb.ConfigId != 1 || pb.PrefabId != 3000 || pb.PrefabIncId != g.Inc ||
		pb.Pos.X != -110990 || pb.Pos.Y != 74020 {
		t.Fatalf("unexpected spawned prefab EntityPb: %+v", pb)
	}
	ce := s.Combat.Scene.GetEntity(net)
	if ce == nil || ce.IsOpenWorldSpawn {
		t.Fatalf("prefab monster combat entity = %+v", ce)
	}
	if !strings.Contains(r.read("world_net_ids"), fmt.Sprint(net)) {
		t.Fatalf("world_net_ids missing prefab net id %d", net)
	}

	s.Systems.Hooks(s).OnMonsterKilled(&combat.CombatEntity{NetID: net, ConfigID: 1})
	if !s.Quests().IsFinished(13) {
		t.Fatalf("quest 13 not finished after prefab monster killed")
	}
}

func TestQuestPresentationKeptAcrossJoinsAndUndoneAtEnd(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("quests", "F 1\n")

	ds := quests.NewQuestDataSet()
	n2 := qNode(2, 1, "ChildQuest", "ReachArea")
	n2.Enter = []quests.QuestAction{
		{Name: "AddGuestCharacter", Raw: parseJSON(t, `{"GuestCharacterId":1003}`)},
		{Name: "SetWeather", Raw: parseJSON(t, `{"WeatherId":5}`)},
		{Name: "EnableSystem", Raw: parseJSON(t, `{"SystemType":10015,"IsEnable":false}`)},
		{Name: "SetAreaTimeState", Raw: parseJSON(t, `{"SetAreaTimeConfig":{"Type":0,"LockTime":{"Hour":8,"Minutes":30}}}`)},
		{Name: "SetAreaState", Raw: parseJSON(t, `{"AreaId":56,"State":1}`)},
		{Name: "SetEntityPos", Raw: parseJSON(t, `{"EntityId":4242,"Pos":{"X":10,"Y":20,"Z":30},"Rot":{"Z":90}}`)},
		{Name: "EnableFunction", Raw: parseJSON(t, `{"Type":"TeleportDungeon","DisplayMode":{"Type":"DisableAll"},"DungeonList":[68]}`)},
	}
	ds.Quests = append(ds.Quests, quests.QuestDef{
		ID: 11, Type: 1, Name: "十一", PreQuest: 1,
		Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2, 3), n2, qNode(3, 1, "QuestSucceed", "")},
	})
	s, c := r.enterWithQuests(quests.NewCatalog(ds), false)

	if last[*protocol.AddGuestNotify](t, c).GuestId != 1003 ||
		last[*protocol.WeatherNotify](t, c).WeatherId != 5 ||
		last[*protocol.UpdateEnableSystemNotify](t, c).FuncInfo[0].FuncId != 10015 ||
		last[*protocol.UpdateEnableSystemNotify](t, c).FuncInfo[0].IsOpen ||
		!last[*protocol.TimeLockInfoNotify](t, c).IsLock ||
		last[*protocol.TimeLockInfoNotify](t, c).Hour != 8 ||
		last[*protocol.TimeLockInfoNotify](t, c).Minute != 30 ||
		last[*protocol.SceneAreaStateNotify](t, c).AreaState.AreaId != 56 ||
		!last[*protocol.SceneAreaStateNotify](t, c).AreaState.State {
		t.Fatalf("unexpected initial presentation notifies")
	}
	fn := last[*protocol.UpdateEnableFunctionNotify](t, c).FuncInfo[0]
	if fn.Type != protocol.EnableFunctionType_EnableFunctionType_TeleportDungeon || fn.IsEnable || !slices.Equal(fn.GetFBParamList().ParamIntList, []int32{68}) {
		t.Fatalf("unexpected EnableFunctionNotify: %+v", fn)
	}
	if r.read("area_states") != "56 1\n" || r.read("entity_pos") != "4242 10.0 20.0 30.0 90.0\n" {
		t.Fatalf("area_states=%q entity_pos=%q", r.read("area_states"), r.read("entity_pos"))
	}

	c.msgs = nil
	s.ChangeScene(8, nil)
	areaStates := last[*protocol.JoinSceneNotify](t, c).SceneInfo.AreaStates
	if len(areaStates) != 1 || areaStates[0].AreaId != 56 || !areaStates[0].State {
		t.Fatalf("JoinSceneNotify AreaStates = %v", areaStates)
	}
	if len(ofType[*protocol.AddGuestNotify](c)) != 1 || !last[*protocol.WeatherNotify](t, c).IsClient ||
		len(ofType[*protocol.UpdateEnableSystemNotify](c))+len(ofType[*protocol.UpdateEnableFunctionNotify](c)) != 2 {
		t.Fatalf("presentation not re-sent on scene join")
	}

	c.msgs = nil
	s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: 11, NodeId: 2})
	if !s.Quests().IsFinished(11) ||
		last[*protocol.RemoveGuestNotify](t, c).GuestId != 1003 ||
		last[*protocol.WeatherNotify](t, c).WeatherId != quests.WeatherSunny ||
		!last[*protocol.UpdateEnableSystemNotify](t, c).FuncInfo[0].IsOpen ||
		!last[*protocol.UpdateEnableFunctionNotify](t, c).FuncInfo[0].IsEnable ||
		last[*protocol.TimeLockInfoNotify](t, c).IsLock ||
		s.QuestState.Presentation.Count() != 0 {
		t.Fatalf("presentation not undone on quest end")
	}
	if all := s.World.Areas.All(); len(all) != 1 || all[0].AreaID != 56 || !all[0].On {
		t.Fatalf("area states should persist after quest end: %+v", all)
	}
}

func TestVehiclesInSession(t *testing.T) {
	makeCat := func() *quests.Catalog {
		ds := quests.NewQuestDataSet()
		n2 := qNode(2, 1, "ChildQuest", "ReachArea")
		n2.Enter = []quests.QuestAction{{Name: "VehicleEnter", Raw: parseJSON(t, `{"EnteringTarget":{"Type":"Player","TargetVehicle":600},"Seat":1}`)}}
		n3 := qNode(3, 1, "ChildQuest", "ReachArea")
		n3.Enter = []quests.QuestAction{{Name: "VehicleExitPlayer", Raw: parseJSON(t, `{"ExitType":{"Type":"Launch"}}`)}}
		ds.Quests = append(ds.Quests, quests.QuestDef{
			ID: 12, Type: 1, Name: "十二", PreQuest: 1,
			Nodes: []quests.NodeDef{qNode(1, 0, "Sequence", "", 2, 3, 4), n2, n3, qNode(4, 1, "QuestSucceed", "")},
		})
		return quests.NewCatalog(ds)
	}

	t.Run("board spline and exit", func(t *testing.T) {
		r := newRig(t)
		r.saveLivePlayer()
		r.save("quests", "F 1\n")
		s, c := r.enterWithQuests(makeCat(), false)
		v := s.QuestState.Vehicles
		if !v.RidesConfig(600) || len(ofType[*protocol.VehicleUpdateNotify](c)) != 0 {
			t.Fatalf("expected pending ride on 600 with no notify yet")
		}
		gondola := s.Scene.Add(&world.Entity{ConfigID: 600, EntityType: world.TypeSceneEntity, ConfigType: world.ConfigLevel})
		s.Quests().OnEntitySpawned(600, gondola.ID)
		board := last[*protocol.VehicleUpdateNotify](t, c)
		if board.EntityId != gondola.ID || board.SeatId != 1 || board.PlayerId != 777 {
			t.Fatalf("unexpected board notify: %+v", board)
		}

		if resp := s.SplineMove(&protocol.SplineMoveRequest{EntityId: gondola.ID, Pos: &protocol.Vector{X: 1, Y: 2, Z: 3}}); resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success {
			t.Fatalf("SplineMove code = %v", resp.ErrorCode)
		}
		s.RunAfterResponse()
		want := world.Vec3{X: 1, Y: 2, Z: 3}
		if s.Scene.PlayerPos != want || gondola.Pos != want {
			t.Fatalf("player=%v gondola=%v, want %v", s.Scene.PlayerPos, gondola.Pos, want)
		}

		s.SubmitNode(&protocol.SubmitNodeRequest{TreeOwnerId: 777, TreeIncId: 12, NodeId: 2})
		off := last[*protocol.VehicleUpdateNotify](t, c)
		if off.EntityId != gondola.ID || off.SeatId != -1 || off.ExitType != protocol.ExitVehicleType_ExitVehicleType_ExitVehicleTypeLaunch || v.Current != nil {
			t.Fatalf("unexpected exit notify: %+v current=%v", off, v.Current)
		}
	})

	t.Run("ride replayed after login", func(t *testing.T) {
		r := newRig(t)
		r.saveLivePlayer()
		r.save("quests", "F 1\nP 12 0:2:0:0:0 1:2:3:0:0\n")
		s, c := r.enterWithQuests(makeCat(), false)
		if !s.Quests().IsRunning(12) {
			t.Fatal("quest 12 not running")
		}
		gondola := s.Scene.Add(&world.Entity{ConfigID: 600, EntityType: world.TypeSceneEntity, ConfigType: world.ConfigLevel})
		role := s.Scene.RoleEntities.Values()[0]
		step := func() {
			s.Move(&protocol.MovePackagePush{MovingEntities: []*protocol.MovingEntityData{{
				EntityId:  role,
				MoveInfos: []*protocol.MoveReplaySample{{Location: &protocol.Vector{X: -111000, Y: 74000, Z: 6980}}},
			}}})
		}
		c.msgs = nil
		step()
		step()
		if len(ofType[*protocol.VehicleUpdateNotify](c)) != 0 {
			t.Fatal("ride replayed before 2s delay")
		}
		r.clock.Advance(2500)
		s.Heartbeat()
		board := last[*protocol.VehicleUpdateNotify](t, c)
		if board.EntityId != gondola.ID || board.SeatId != 1 {
			t.Fatalf("replayed board = %+v", board)
		}
		c.msgs = nil
		r.clock.Advance(2500)
		s.Heartbeat()
		if len(ofType[*protocol.VehicleUpdateNotify](c)) != 0 {
			t.Fatal("ride replayed more than once")
		}
	})

	t.Run("client leaving quest vehicle answered success", func(t *testing.T) {
		r := newRig(t)
		r.saveLivePlayer()
		r.save("quests", "F 1\n")
		s, _ := r.enterWithQuests(makeCat(), false)
		gondola := s.Scene.Add(&world.Entity{ConfigID: 600, EntityType: world.TypeSceneEntity, ConfigType: world.ConfigLevel})
		s.Quests().OnEntitySpawned(600, gondola.ID)
		if resp := s.Vehicle(&protocol.VehicleRequest{EntityId: 4000, IsGetOn: false}); resp.ErrorCode != protocol.ErrorCode_ErrorCode_ErrVehicleEntityNotExist {
			t.Fatalf("wrong entity code = %v", resp.ErrorCode)
		}
		if resp := s.Vehicle(&protocol.VehicleRequest{EntityId: gondola.ID, IsGetOn: false}); resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success || s.QuestState.Vehicles.Current != nil {
			t.Fatalf("leaving quest vehicle failed: resp=%+v current=%v", resp, s.QuestState.Vehicles.Current)
		}
	})
}

func TestNewPlayerStartsStoryOnGeneratedLabData(t *testing.T) {
	dir := os.Getenv("TIDELAB_QUESTS")
	if dir == "" {
		if g := common.GameDataDir(); g != "" {
			dir = filepath.Join(g, "questgen", "assets", "quest")
		}
	}
	if dir == "" || !fileExists(filepath.Join(dir, "Quests.json")) {
		t.Skip("3.8 quest data not present")
	}
	cat := quests.LoadCatalog(dir)
	if len(cat.Data.FlowActions) == 0 || len(cat.Data.HideGroups) == 0 || len(cat.Data.Prefabs) == 0 || len(cat.Data.TowerFloors) == 0 {
		t.Fatalf("expected populated FlowActions/HideGroups/Prefabs/TowerFloors")
	}
	r := newRig(t)
	r.saveLivePlayer()
	s, c := r.enterWithQuests(cat, false)
	if !s.Quests().IsRunning(quests.StartQuest) || len(s.Quests().ActiveSteps(false)) == 0 {
		t.Fatalf("start quest %d not running", quests.StartQuest)
	}
	foundTree := false
	for _, n := range ofType[*protocol.BehaviorTreeInfoNotify](c) {
		for _, ti := range n.TreeInfos {
			if ti.TreeIncId == int64(quests.StartQuest) {
				foundTree = true
			}
		}
	}
	if !foundTree {
		t.Fatalf("start quest tree not pushed")
	}
	if !slices.Contains(ofType[*protocol.QuestFinishListNotify](c)[0].QuestId, quests.PreFinished[0]) {
		t.Fatalf("pre-finished quest missing")
	}
	if !fileExists(r.playerFile("quests")) {
		t.Fatalf("quests file not written")
	}
	s.Heartbeat()
	s.Move(&protocol.MovePackagePush{MovingEntities: []*protocol.MovingEntityData{{
		EntityId:  s.Scene.RoleEntities.Values()[0],
		MoveInfos: []*protocol.MoveReplaySample{{Location: &protocol.Vector{X: -111000, Y: 74000, Z: 6980}}},
	}}})
	if s.Quests().RestorePending {
		t.Fatalf("RestorePending still true after first move")
	}
}

// The elevator request carries the floor in tagState and the motion in state; the motion is
// the entity state quests wait on (139000027 node 154) and the one its behaviours run on (R-169).
func TestElevatorMotionSetsTheElevatorState(t *testing.T) {
	r := newRig(t)
	r.tables.LevelEntityConfig = []*data.LevelEntityConfig{{
		MapId: 8, EntityId: 950, Name: "TsEntity_玩法_电梯", BlueprintType: "Gameplay106",
		ComponentsData: parseJSON(t, `{
			"EntityStateComponent": {"StateChangeBehaviors": [
				{"State": "关卡.电梯.上升", "Action": [{"Name": "ChangeEntityState", "Params": {"Type": "Directly", "EntityId": 951, "State": "关卡.Common.状态.激活"}}]},
				{"State": "关卡.电梯.下降", "Action": []}
			]}
		}`),
	}}
	r.rebuild()
	r.saveLivePlayer()
	s, _ := r.enter(false)
	lift := s.Scene.Add(&world.Entity{ConfigID: 950, EntityType: world.TypeSceneEntity, ConfigType: world.ConfigLevel})
	up, down := world.TagID(r.tables, "关卡.电梯.上升"), world.TagID(r.tables, "关卡.电梯.下降")
	active := world.TagID(r.tables, "关卡.Common.状态.激活")

	s.ElevatorStateChange(&protocol.ElevatorStateChangeRequest{EntityId: lift.ID, TagState: 2, State: protocol.ElevatorState_ElevatorState_End})
	s.RunAfterResponse()
	if _, ok := s.World.State.StateOverride(950); ok {
		t.Fatal("End (stopped) must not change the state; the floor is not a tag")
	}
	s.ElevatorStateChange(&protocol.ElevatorStateChangeRequest{EntityId: lift.ID, TagState: 2, State: protocol.ElevatorState_ElevatorState_Forward})
	s.RunAfterResponse()
	if st, ok := s.World.State.StateOverride(950); !ok || st != up {
		t.Fatalf("after Forward: state (%d, %v), want 上升 %d", st, ok, up)
	}
	if st, ok := s.World.State.StateOverride(951); !ok || st != active {
		t.Fatalf("上升 behaviour: 951 state (%d, %v), want %d", st, ok, active)
	}
	s.ElevatorStateChange(&protocol.ElevatorStateChangeRequest{EntityId: lift.ID, TagState: 1, State: protocol.ElevatorState_ElevatorState_Reverse})
	s.RunAfterResponse()
	if st, _ := s.World.State.StateOverride(950); st != down {
		t.Fatalf("after Reverse: state %d, want 下降 %d", st, down)
	}
}
