// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"os"
	"path/filepath"
	"testing"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/protocol"
)

// A small SM_Common shaped like the real one (R-152): a main machine whose sleep state is left
// by the server, and an entity machine that references it and overrides one common node.
const testCommonSM = `{"Version":100,"StateMachines":[],"Nodes":[
 {"Uuid":10007,"Name":"C主状态机","IsAnimStateMachine":false,"Children":[10017,10031,10012],"Transitions":[
  {"From":10017,"To":10031,"TransitionPredictionType":1,"Conditions":[{"Index":0,"Name":"CondTag","IsClient":true,"Reverse":false,"CondTag":{"TagId":7}}]},
  {"From":10031,"To":10012,"TransitionPredictionType":0,"Conditions":[
   {"Index":0,"Name":"CondAnd","Reverse":false,"CondAnd":{"Conditions":[1,2]}},
   {"Index":1,"Name":"CondHate","Reverse":false},
   {"Index":2,"Name":"CondTimer","IsClient":false,"Reverse":false,"CondTimer":{"MinTime":1000,"MaxTime":1000}}]},
  {"From":10012,"To":10031,"TransitionPredictionType":0,"Conditions":[{"Index":0,"Name":"CondHate","Reverse":true}]}]},
 {"Uuid":10017,"Name":"C无音区入战中转","IsConduitNode":true,"Children":[],"Transitions":[]},
 {"Uuid":10031,"Name":"C生态","Children":[10056],"Transitions":[]},
 {"Uuid":10056,"Name":"C通用睡觉","Children":[10103,10293,10294],"Transitions":[
  {"From":10103,"To":10293,"TransitionPredictionType":2,"Conditions":[{"Index":0,"Name":"CondHate","Reverse":false}]},
  {"From":10293,"To":10294,"TransitionPredictionType":0,"Conditions":[{"Index":0,"Name":"CondTaskFinish","IsClient":true,"Reverse":false,"CondTaskFinish":{}}]},
  {"From":10294,"To":10103,"TransitionPredictionType":0,"Conditions":[
   {"Index":0,"Name":"CondOr","Reverse":true,"CondOr":{"Conditions":[1]}},
   {"Index":1,"Name":"CondTag","IsClient":true,"Reverse":false,"CondTag":{"TagId":9}}]}]},
 {"Uuid":10103,"Name":"C通用睡觉循环","Children":[],"Transitions":[]},
 {"Uuid":10293,"Name":"C通用睡觉入战","Children":[],"Transitions":[]},
 {"Uuid":10294,"Name":"C通用睡觉入战结束","Children":[],"Transitions":[]},
 {"Uuid":10012,"Name":"C战斗","Children":[10013,10014],"Transitions":[
  {"From":10013,"To":10014,"TransitionPredictionType":0,"Conditions":[{"Index":0,"Name":"CondAttributeRate","Reverse":false,"CondAttributeRate":{"AttributeId":3,"Denominator":2,"Min":0,"Max":5000}}]}]},
 {"Uuid":10013,"Name":"C战斗分支","Children":[],"Transitions":[]},
 {"Uuid":10014,"Name":"C狂暴","Children":[],"Transitions":[]}]}`

const testLocalSM = `{"Version":300,"StateMachines":[4],"Nodes":[
 {"Uuid":4,"Name":"引用_C主状态机","ReferenceUuid":10007},
 {"Uuid":20013,"Name":"本地战斗分支","OverrideCommonUuid":10013,"Children":[],"Transitions":[]}]}`

func testGraph(t *testing.T) *FsmGraph {
	t.Helper()
	g := buildFsmGraph(objOf(mustJSON(testCommonSM)), objOf(mustJSON(testLocalSM)))
	must(t, g != nil && len(g.roots) == 1 && g.roots[0] == 10007 && g.Root(4) == 10007, g.roots)
	return g
}

func TestFsmGraphFollowsReferencesAndOverrides(t *testing.T) {
	g := testGraph(t)
	// The overridden common node is built from the entity's node, under the common parent.
	must(t, g.nodes[10013] == nil && g.nodes[20013] != nil && g.nodes[20013].parent == 10012, "override")
	must(t, g.nodes[10012].children[0] == 20013, g.nodes[10012].children)
	// Transitions sit on their From node, which the override stands in for.
	must(t, len(g.nodes[10293].transitions) == 1 && g.nodes[10293].transitions[0].server, "10293")
	must(t, len(g.nodes[20013].transitions) == 1 && g.nodes[20013].transitions[0].to == 10014, "20013")
	must(t, g.byName["C战斗分支"] == 20013 && g.byName["本地战斗分支"] == 20013, g.byName)
}

func TestFsmStartsWhereTheClientIsToldAndFollowsClientSwitches(t *testing.T) {
	g := testGraph(t)
	r := NewFsmRuntime(g, []*protocol.DFsm{{FsmId: 4, CurrentState: 10017}}, 0, nil)
	must(t, r.Active(10017) && r.Active(10007) && !r.Active(10031), "start")
	// The client leaves the conduit itself; entering 10031 enters its first children.
	must(t, r.SwitchTo(10031, 10) && r.Leaf(10007) == 10103 && !r.Active(10017), r.Leaf(10007))
	must(t, !r.SwitchTo(10031, 20), "same state")
	// A predicted switch (Simulated) is the client's: the server never makes it.
	e := NewCombatEntity(1)
	e.HasHate = true
	must(t, len(r.Step(e, 30)) == 0, "no server switch from 10103")
	must(t, r.SwitchTo(10293, 40) && r.Leaf(10007) == 10293, "client switch")
}

func TestFsmServerSwitchWaitsForTheClientsConditionReport(t *testing.T) {
	g := testGraph(t)
	r := NewFsmRuntime(g, []*protocol.DFsm{{FsmId: 4, CurrentState: 10293}}, 0, nil)
	e := NewCombatEntity(1)
	must(t, len(r.Step(e, 10)) == 0, "task not finished yet")
	r.Report(10293, 10294, 0, true)
	sw := r.Step(e, 20)
	must(t, len(sw) == 1 && sw[0] == FsmSwitch{FsmID: 10007, From: 10293, To: 10294} && r.Leaf(10007) == 10294, sw)
	// 10294 -> 10103 is "not (tag 9)": not judged before the client reports the tag.
	must(t, len(r.Step(e, 25)) == 0, "unreported is not false")
	r.Report(10294, 10103, 1, false)
	sw = r.Step(e, 26)
	must(t, len(sw) == 1 && sw[0].To == 10103, sw)
	// A report for a node that is left again counts no more.
	r.SwitchTo(10293, 30)
	must(t, len(r.Step(e, 40)) == 0, "report cleared on re-entry")
}

func TestFsmServerConditionsHateTimerAttributesBuffs(t *testing.T) {
	g := testGraph(t)
	r := NewFsmRuntime(g, []*protocol.DFsm{{FsmId: 4, CurrentState: 10103}}, 1000, nil)
	e := NewCombatEntity(1)
	e.Attributes.Slots[AttrLifeMax].Current, e.Attributes.Slots[AttrLife].Current = 100, 100
	must(t, len(r.Step(e, 5000)) == 0, "no hate")
	e.HasHate = true
	// The timer started when 10031 became active (1000) and runs 1000 ms.
	r = NewFsmRuntime(g, []*protocol.DFsm{{FsmId: 4, CurrentState: 10103}}, 1000, nil)
	must(t, len(r.Step(e, 1999)) == 0, "timer not due")
	sw := r.Step(e, 2000)
	must(t, len(sw) == 1 && sw[0].To == 10012 && r.Leaf(10007) == 20013, sw, r.Leaf(10007))
	// The common transition out of the overridden node: life at most 50%.
	must(t, len(r.Step(e, 2100)) == 0, "full life")
	e.Attributes.Slots[AttrLife].Current = 50
	sw = r.Step(e, 2200)
	must(t, len(sw) == 1 && sw[0] == FsmSwitch{FsmID: 10007, From: 20013, To: 10014}, sw)
	// Reverse: no hate takes it back to 10031, whose first child is 10056/10103.
	e.HasHate = false
	sw = r.Step(e, 2300)
	must(t, len(sw) == 1 && sw[0].To == 10031 && r.Leaf(10007) == 10103, sw)
}

func TestFsmAttributeRateAndReverse(t *testing.T) {
	r := NewFsmRuntime(testGraph(t), nil, 0, nil)
	e := NewCombatEntity(1)
	e.Attributes.Slots[AttrLifeMax].Current, e.Attributes.Slots[AttrLife].Current = 100, 80
	tr := &fsmTransition{conds: []fsmCond{{kind: "CondAttributeRate", attr: AttrLife, denom: AttrLifeMax, min: 0, max: 0.5}}}
	must(t, !r.eval(tr, 0, e, 10, 0), "80% is not in 0..50%")
	e.Attributes.Slots[AttrLife].Current = 40
	must(t, r.eval(tr, 0, e, 10, 0), "40%")
	tr.conds[0].reverse = true
	must(t, !r.eval(tr, 0, e, 10, 0), "reverse")
	// A client condition is the client's value as reported: Reverse is not applied again.
	tr.conds[0] = fsmCond{kind: "CondBuffStack", buff: 555, minStack: 2, maxStack: 9}
	e.Buffs = []*ActiveBuffEntry{{BuffID: 555, StackCount: 1}, {BuffID: 7, StackCount: 5}}
	must(t, !r.eval(tr, 0, e, 10, 0), "one stack")
	e.Buffs = append(e.Buffs, &ActiveBuffEntry{BuffID: 555, StackCount: 1})
	must(t, r.eval(tr, 0, e, 10, 0), "two stacks")
	tr.conds[0] = fsmCond{kind: "CondTag", client: true, reverse: true}
	r.reported[fsmCondKey{tr, 0}] = true
	must(t, r.eval(tr, 0, e, 10, 0), "client value")
}

func TestCombatPackDrivesTheServerSwitch(t *testing.T) {
	ctx := createContext(t, t.TempDir(), ctxOpts{nowMs: 1000})
	mid := ctx.Scene.AllocateNetID()
	m := NewCombatEntity(mid)
	m.EntityType = protocol.EEntityType_EEntityType_Monster
	m.FsmComponent = &protocol.EntityFsmComponentPb{Fsms: []*protocol.DFsm{{FsmId: 4, CurrentState: 10103}}}
	ctx.Combat.StartFsm(m, testGraph(t), 1000)
	ctx.Scene.Entities.Set(mid, m)
	push := func(p *protocol.FsmConditionPassPush) *protocol.CombatSendData {
		return &protocol.CombatSendData{Message: &protocol.CombatSendData_Push{Push: &protocol.CombatPushData{
			CombatCommon: &protocol.CombatCommon{EntityId: mid}, Message: &protocol.CombatPushData_FsmConditionPassPush{FsmConditionPassPush: p}}}}
	}
	resp := ctx.Combat.HandleCombatSendPack(&protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{
		req(1, mid, &protocol.CombatRequestData_ChangeStateRequest{ChangeStateRequest: &protocol.ChangeStateRequest{FsmId: 10007, FromState: 10103, ToState: 10293}}),
		push(&protocol.FsmConditionPassPush{FsmId: 10007, FromState: 10293, ToState: 10294, ConditionIndex: 0, Value: true}),
	}}, 2000, &recordingSink{})
	var got []*protocol.ChangeStateNotify
	for _, d := range resp.ReceivePackNotify.Data {
		if n := d.GetCombatNotifyData(); n != nil && n.GetChangeStateNotify() != nil && n.CombatCommon.GetEntityId() == mid {
			got = append(got, n.GetChangeStateNotify())
		}
	}
	// The echo of the client's own switch, then the server's.
	must(t, len(got) == 2 && got[1].FsmId == 10007 && got[1].FromState == 10293 && got[1].ToState == 10294, got)
	must(t, m.FsmComponent.Fsms[0].CurrentState == 10294, m.FsmComponent.Fsms[0].CurrentState)

	// A timer alone: the tick pushes the switch.
	m.HasHate = true
	sink := &recordingSink{}
	m.FsmComponent.Fsms[0].CurrentState = 10103
	ctx.Combat.StartFsm(m, testGraph(t), 3000)
	ctx.Combat.TickFsms(3500, sink)
	must(t, len(ofType[*protocol.CombatReceivePackNotify](sink)) == 0, "not due")
	ctx.Combat.TickFsms(4000, sink)
	packs := ofType[*protocol.CombatReceivePackNotify](sink)
	must(t, len(packs) == 1 && packs[0].Data[0].GetCombatNotifyData().GetChangeStateNotify().GetToState() == 10012, packs)
}

// Every state machine of the 3.8 data builds, and the sleeping wild monster of the user's log
// (SM_ME1SiXiaoMd00201, R-152) gets out of 睡觉入战 once the client reports its task finished.
func TestFsmRealData(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "AiStateMachineConfig.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(os.TempDir(), "tidelab-go-r144e-cache"), UseCache: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	init := NewFsmInitializer(tb)
	server := 0
	for _, cfg := range tb.AiStateMachineConfig {
		g := init.graphs.get(tb, cfg.Id)
		must(t, g != nil, cfg.Id)
		for _, n := range g.nodes {
			for _, tr := range n.transitions {
				must(t, g.nodes[tr.from] == n && g.nodes[tr.to] != nil, cfg.Id, tr.from, tr.to)
				if tr.server {
					server++
				}
			}
		}
	}
	must(t, server > 3000, server)

	g := init.graphs.get(tb, "SM_ME1SiXiaoMd00201")
	pb := init.BuildForStateMachine("SM_ME1SiXiaoMd00201")
	must(t, g != nil && pb != nil, "SiXiao")
	r := NewFsmRuntime(g, pb.Fsms, 0, nil)
	// What the client logged (2026-11-20 23:25): its own switches into sleep, then into 入战.
	for _, to := range []int32{10031, 10039, 10056, 10103, 10293} {
		r.SwitchTo(to, 10)
	}
	e := NewCombatEntity(1)
	e.HasHate = true
	must(t, r.Leaf(10007) == 10293, r.Leaf(10007))
	// Nothing the client has not reported yet is judged (10031 -> 10008 has a reversed client
	// OR that would hold if a missing report counted as false).
	must(t, len(r.Step(e, 20)) == 0, r.Leaf(10007))
	r.Report(10293, 10294, 0, true)
	sw := r.Step(e, 30)
	must(t, len(sw) >= 1 && sw[0] == FsmSwitch{FsmID: 10007, From: 10293, To: 10294}, sw)
}
