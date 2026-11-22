// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"testing"

	"tidelab/internal/data"
	"tidelab/protocol"
)

func TestFlowOptionsParseFlowAndActions(t *testing.T) {
	src := &protocol.GameCtxPb{CtxType: protocol.GameCtxType_GameCtxType_ChildQuestNodeEnterAction}
	flowRaw, err := data.ParseJSON([]byte(`{
		"EntityId": 8801,
		"InteractToken": "tok-1",
		"Option": {
			"Guid": "g1",
			"TidContent": "Talk",
			"Type": {
				"Type": "Flow",
				"Flow": {"FlowListName": "剧情_选项", "FlowId": 4, "StateId": 2}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := ParseFlowOption(100, src, flowRaw)
	if !ok {
		t.Fatal("ParseFlowOption(Flow) failed")
	}
	if e.Owner != 100 || e.Entity != 8801 || e.Guid != "g1" || e.Text != "Talk" || e.Token != "tok-1" ||
		e.Flow == nil || e.Flow.FlowListName != "剧情_选项" || e.Flow.FlowID != 4 || e.Flow.StateID != 2 {
		t.Fatalf("unexpected Flow entry: %+v", e)
	}

	actRaw, err := data.ParseJSON([]byte(`{
		"EntityId": 8802,
		"Option": {
			"Guid": "g2",
			"TidContent": "Board",
			"Type": {
				"Type": "Actions",
				"Actions": [{"Name": "TeleportDungeon", "Params": {"DungeonId": 1505}}]
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	ea, ok := ParseFlowOption(100, src, actRaw)
	if !ok || len(ea.Actions) != 1 || ea.Actions[0].Name != "TeleportDungeon" {
		t.Fatalf("ParseFlowOption(Actions) = (%+v, %v)", ea, ok)
	}
	// Raw of Actions entries comes from the item's Params (R-33a).
	if id, ok := ActionInt(ea.Actions[0], "DungeonId"); !ok || id != 1505 {
		t.Fatalf("ActionInt(DungeonId) = (%d, %v), want 1505", id, ok)
	}
}

func TestFlowOptionsAddReplaceFindRemoveAndRemoveOwner(t *testing.T) {
	fo := NewFlowOptions()
	src := &protocol.GameCtxPb{CtxType: protocol.GameCtxType_GameCtxType_ChildQuestNodeEnterAction}
	fo.Add(FlowOptionEntry{Owner: 1, Source: src, Entity: 50, Guid: "a", Text: "v1", Flow: &FlowRef{FlowListName: "F", FlowID: 1}, Token: "t1"})
	fo.Add(FlowOptionEntry{Owner: 1, Source: src, Entity: 50, Guid: "a", Text: "v2", Flow: &FlowRef{FlowListName: "F", FlowID: 1}, Token: "t1"})
	if fo.Count() != 1 || fo.Stats.Added != 1 {
		t.Fatalf("replace should keep count=1 added=1, got count=%d added=%d", fo.Count(), fo.Stats.Added)
	}
	if got, ok := fo.Find(50, ""); !ok || got.Text != "v2" {
		t.Fatalf("Find(50, '') = (%+v, %v)", got, ok)
	}
	fo.Add(FlowOptionEntry{Owner: 1, Source: src, Entity: 50, Guid: "b", Token: "t2"})
	fo.Add(FlowOptionEntry{Owner: 2, Source: src, Entity: 50, Guid: "c", Token: "t3"})

	gone := fo.Remove(50, &FlowRef{FlowListName: "F", FlowID: 1}, "")
	if len(gone) != 1 || gone[0].Guid != "a" || fo.Stats.Removed != 1 {
		t.Fatalf("Remove by flow = %+v", gone)
	}
	gone = fo.Remove(50, nil, "t2")
	if len(gone) != 1 || gone[0].Guid != "b" {
		t.Fatalf("Remove by token = %+v", gone)
	}
	gone = fo.RemoveOwner(2)
	if len(gone) != 1 || gone[0].Guid != "c" || fo.Count() != 0 || fo.Stats.Released != 1 {
		t.Fatalf("RemoveOwner = %+v, count=%d stats=%+v", gone, fo.Count(), fo.Stats)
	}
}

func TestFlowOptionsRemoveArgsAndPickedCtx(t *testing.T) {
	raw, err := data.ParseJSON([]byte(`{"EntityId": 50, "Flow": {"FlowListName": "L", "FlowId": 9, "StateId": 1}, "InteractToken": "tk"}`))
	if err != nil {
		t.Fatal(err)
	}
	ent, flow, tok, ok := RemoveFlowOptionArgs(raw)
	if !ok || ent != 50 || flow == nil || flow.FlowListName != "L" || flow.FlowID != 9 || tok != "tk" {
		t.Fatalf("RemoveFlowOptionArgs = (%d, %+v, %q, %v)", ent, flow, tok, ok)
	}
	src := &protocol.GameCtxPb{CtxType: protocol.GameCtxType_GameCtxType_ActionGroupNodeAction}
	ctx := PickedFlowOptionCtx(FlowOptionEntry{Entity: 50, Guid: "g", Source: src}, 5050)
	di := ctx.GetDynamicInteract()
	if ctx.CtxType != protocol.GameCtxType_GameCtxType_DynamicInteract || di == nil ||
		di.EntityCtx.ConfigId != 50 || di.EntityCtx.IncId != 5050 || di.OptionGuid != "g" || di.FinalOptionCtx != src {
		t.Fatalf("unexpected PickedFlowOptionCtx: %+v", ctx)
	}
}
