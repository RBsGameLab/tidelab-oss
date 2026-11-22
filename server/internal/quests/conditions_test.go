// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"math"
	"testing"
)

func selfRef(name string) *VarRef { return &VarRef{Src: "Self", T: "Int", Name: name} }
func constRef(n float64) *VarRef  { return &VarRef{Src: "Constant", T: "Int", Num: n} }
func allCheck(subs ...Sub) Check  { return Check{Count: 0, Conds: subs} }

func TestSetVarCalculateVarAndCompareVar(t *testing.T) {
	v := NewBtVariables()
	run := LevelPlayRun(141000012)
	fails := selfRef("二阶段失败")
	if !CompareBt(v.Read(run, *fails), "Eq", BtNum(0)) {
		t.Fatal("unset self var should read as 0")
	}
	v.Apply(run, VarOp{Op: "Add", Dst: fails, A: fails, B: constRef(1)})
	v.Apply(run, VarOp{Op: "Add", Dst: fails, A: fails, B: constRef(1)})
	eq2 := allCheck(Sub{T: "CompareVar", A: fails, Op: "Eq", B: constRef(2)})
	ge4 := allCheck(Sub{T: "CompareVar", A: fails, Op: "Ge", B: constRef(4)})
	if !v.Evaluate(run, eq2, nil) {
		t.Fatal("expected fails == 2")
	}
	if v.Evaluate(run, ge4, nil) {
		t.Fatal("expected fails < 4")
	}
	v.Apply(run, VarOp{Op: "Set", Dst: fails, A: constRef(4)})
	if !v.Evaluate(run, ge4, nil) {
		t.Fatal("expected fails >= 4")
	}
	v.Apply(run, VarOp{Op: "Mut", Dst: fails, A: fails, B: constRef(0.5)})
	if !v.Evaluate(run, eq2, nil) {
		t.Fatal("expected fails == 2 after Mut 0.5")
	}
	if !CompareBt(v.Read(run+1, *fails), "Eq", BtNum(0)) {
		t.Fatal("different run should read 0")
	}
	v.DropRun(run)
	if !CompareBt(v.Read(run, *fails), "Eq", BtNum(0)) {
		t.Fatal("dropped run should reset to 0")
	}
}

func TestGroupCountsAndUnknownSubConditionsCountAsMet(t *testing.T) {
	v := NewBtVariables()
	var run int64 = 7
	a := selfRef("A")
	v.Apply(run, VarOp{Op: "Set", Dst: a, A: constRef(1)})
	yes := Sub{T: "CompareVar", A: a, Op: "Eq", B: constRef(1)}
	no := Sub{T: "CompareVar", A: a, Op: "Eq", B: constRef(2)}
	unknown := Sub{T: "CheckInRange"}
	sys := Sub{T: "CompareVar", A: &VarRef{Src: "System", T: "Int"}, Op: "Eq", B: constRef(9)}
	race := Sub{T: "CompareVar", A: &VarRef{Src: "Other", T: "Boolean", Name: "玩家1P完成比赛", Ref: 226700002}, Op: "Eq", B: constRef(1)}
	if _, ok := v.EvalSub(run, sys, nil); ok {
		t.Fatal("System var should be unknown")
	}
	if _, ok := v.EvalSub(run, race, nil); ok {
		t.Fatal("Other var in quest should be unknown")
	}
	if res, ok := v.EvalSub(run, Sub{T: "CompareVar", A: constRef(0), Op: "Eq", B: constRef(1)}, nil); !ok || res {
		t.Fatalf("0 == 1 should be (false, true), got (%v, %v)", res, ok)
	}
	if v.Evaluate(run, Check{Count: 0, Conds: []Sub{yes, no}}, nil) {
		t.Fatal("all(yes, no) should be false")
	}
	if !v.Evaluate(run, Check{Count: 1, Conds: []Sub{yes, no}}, nil) {
		t.Fatal("any(yes, no) should be true")
	}
	if v.Evaluate(run, Check{Count: 2, Conds: []Sub{yes, no}}, nil) {
		t.Fatal("2(yes, no) should be false")
	}
	if !v.Evaluate(run, Check{Count: 2, Conds: []Sub{yes, unknown}}, nil) {
		t.Fatal("2(yes, unknown) should be true")
	}
	g := &VarRef{Src: "Global", T: "Int", Name: "SuiguangFB_pass"}
	v.Apply(run, VarOp{Op: "Set", Dst: g, A: constRef(1)})
	if !CompareBt(v.Read(run+99, *g), "Eq", BtNum(1)) {
		t.Fatal("global var should be visible across runs")
	}
	flag := &VarRef{Src: "Other", T: "Boolean", Name: "dog", Ref: 36900018}
	v.Apply(run, VarOp{Op: "Set", Dst: flag, A: &VarRef{Src: "Constant", T: "Boolean", Num: 1}})
	if !CompareBt(v.Read(LevelPlayRun(1234), *flag), "Eq", BtNum(1)) {
		t.Fatal("Other var should be visible to level play")
	}
}

func TestQuestVariablesAreKnownOnlyWhenTheServerWritesThem(t *testing.T) {
	v := NewBtVariables()
	var q int64 = 121000038
	lp := LevelPlayRun(121000038)
	mirror := &VarRef{Src: "Self", T: "Boolean", Name: "铜镜1首次"}
	free := &VarRef{Src: "Self", T: "Int", Name: "questVAR"}
	v.Declare(false, 121000038, mirror.Name, BtNum(1))
	v.NoteWriter(false, 121000038, *mirror)
	if val, ok := v.Lookup(q, *mirror); !ok || val.Num != 1 {
		t.Fatalf("expected 1, got %v %v", val, ok)
	}
	v.Apply(q, VarOp{Op: "Set", Dst: mirror, A: &VarRef{Src: "Constant", T: "Boolean", Num: 0}})
	if val, ok := v.Lookup(q, *mirror); !ok || val.Num != 0 {
		t.Fatalf("expected 0, got %v %v", val, ok)
	}
	if _, ok := v.Lookup(q, *free); ok {
		t.Fatal("unwritten quest Self var should be unknown")
	}
	if !v.Evaluate(q, allCheck(Sub{T: "CompareVar", A: free, Op: "Eq", B: constRef(5)}), nil) {
		t.Fatal("unknown compare should count as met")
	}
	if val, ok := v.Lookup(lp, *free); !ok || val.Num != 0 {
		t.Fatalf("level play unset Self var should be 0, got %v %v", val, ok)
	}
	v.Declare(true, 121000038, free.Name, BtNum(3))
	if val, ok := v.Lookup(lp, *free); !ok || val.Num != 3 {
		t.Fatalf("declared level play var should be 3, got %v %v", val, ok)
	}
	v.DropRun(q)
	v.SetUntracked(q, true)
	if _, ok := v.Lookup(q, *mirror); ok {
		t.Fatal("untracked quest run should hide unset Self var")
	}
	v.SetUntracked(q, false)
	if val, ok := v.Lookup(q, *mirror); !ok || val.Num != 1 {
		t.Fatalf("tracked quest run should see initial value 1, got %v %v", val, ok)
	}
	progress := VarRef{Src: "Global", T: "Int", Name: "2_8活动流程进度1"}
	v.Write(q, progress, BtNum(0))
	if val, ok := v.Lookup(q, progress); !ok || val.Num != 0 {
		t.Fatalf("expected 0, got %v %v", val, ok)
	}
	v.NoteWriter(true, 880000005, progress)
	if _, ok := v.Lookup(q, progress); ok {
		t.Fatal("global written by level play should be unknown to quests")
	}
	if val, ok := v.Lookup(lp, progress); !ok || val.Num != 0 {
		t.Fatalf("level play should still read shared global, got %v %v", val, ok)
	}
	keyUsed := VarRef{Src: "Other", T: "Boolean", Name: "keybeused", Ref: 153700051}
	v.Write(q, keyUsed, BtNum(0))
	if _, ok := v.Lookup(q, keyUsed); ok {
		t.Fatal("Other var should be unknown to quests")
	}
	if val, ok := v.Lookup(lp, keyUsed); !ok || val.Num != 0 {
		t.Fatalf("level play should read Other var, got %v %v", val, ok)
	}
	entGlobal := VarRef{Src: "Global", T: "Int", Name: "1_2_Wuwangzhe"}
	v.Write(q, entGlobal, BtNum(1))
	if val, ok := v.Lookup(q, entGlobal); !ok || val.Num != 1 {
		t.Fatalf("expected 1, got %v %v", val, ok)
	}
	v.NoteEntityGlobal("1_2_Wuwangzhe")
	if _, ok := v.Lookup(q, entGlobal); ok {
		t.Fatal("entity global should be unknown to quests")
	}
}

type fakeCondEnv struct {
	DefaultConditionEnv
	dungeonID int32
	male      *bool
	playerPos *[3]float32
}

func (f *fakeCondEnv) EntityHasState(id int64, state string) (bool, bool) {
	return id == 1 && state == "关卡.Common.状态.激活", true
}
func (f *fakeCondEnv) EntityExists(id int64) (bool, bool) { return id == 1, true }
func (f *fakeCondEnv) QuestState(quest int32) (int32, bool) {
	if quest == 5 {
		return 3, true
	}
	return 0, true
}
func (f *fakeCondEnv) ChildDone(_, node int32) (bool, bool) { return node == 44, true }
func (f *fakeCondEnv) DungeonID() int32                     { return f.dungeonID }
func (f *fakeCondEnv) TreasureClaimed(int64) (bool, bool)   { return false, true }
func (f *fakeCondEnv) Male() (bool, bool) {
	if f.male == nil {
		return false, false
	}
	return *f.male, true
}
func (f *fakeCondEnv) PlayerPos() ([3]float32, bool) {
	if f.playerPos == nil {
		return [3]float32{}, false
	}
	return *f.playerPos, true
}
func (f *fakeCondEnv) ItemCount(id int32) (int32, bool) {
	if id == 70050066 {
		return 1, true
	}
	return 0, true
}
func (f *fakeCondEnv) DungeonCleared(id int32) (bool, bool) { return id == 1504, true }
func (f *fakeCondEnv) NodeStatus(_ int64, node int32) (int32, bool) {
	if node == 48 {
		return 4, true
	}
	return 0, false
}

func TestEnvBackedSubConditions(t *testing.T) {
	v := NewBtVariables()
	env := &fakeCondEnv{dungeonID: 1570}
	check := func(name string, sub Sub, wantRes, wantOK bool) {
		t.Helper()
		res, ok := v.EvalSub(0, sub, env)
		if res != wantRes || ok != wantOK {
			t.Fatalf("%s: got (%v, %v), want (%v, %v)", name, res, ok, wantRes, wantOK)
		}
	}
	check("CompareEntityState", Sub{T: "CompareEntityState", Entity: 1, Op: "Eq", State: "关卡.Common.状态.激活"}, true, true)
	check("CheckEntitesExist", Sub{T: "CheckEntitesExist", Entities: []int64{2}, Exist: false}, true, true)
	check("CompareQuestState", Sub{T: "CompareQuestState", Quest: 5, Op: "Eq", Num: 3}, true, true)
	check("CheckChildQuestFinished", Sub{T: "CheckChildQuestFinished", Quest: 1, Node: 43}, false, true)
	check("CheckLevelPlayState", Sub{T: "CheckLevelPlayState", LevelPlay: 9}, false, false)
	check("CompareDungeonId", Sub{T: "CompareDungeonId", Op: "Ne", Dungeon: 1570}, false, true)
	check("CheckTreasureBeenClaimed", Sub{T: "CheckTreasureBeenClaimed", Entity: 3}, false, true)
	check("CheckPlayerGender unknown", Sub{T: "CheckPlayerGender", Gender: "男"}, false, false)

	isMale := true
	env.male = &isMale
	check("CheckPlayerGender male", Sub{T: "CheckPlayerGender", Gender: "男"}, true, true)
	check("CheckPlayerGender female", Sub{T: "CheckPlayerGender", Gender: "女"}, false, true)
}

func TestFixProcessorChecks(t *testing.T) {
	v := NewBtVariables()
	box := Sub{
		T:       "CheckPlayerPosition",
		Shape:   1,
		Center:  [3]float32{-140765.86, 66128.34, 8315},
		Ext:     [3]float32{5500, 2200, 1500},
		Yaw:     30,
		InRange: false,
	}
	c, s := float32(math.Cos(math.Pi/6)), float32(math.Sin(math.Pi/6))
	alongX := [3]float32{-140765.86 + 5000*c, 66128.34 + 5000*s, 8315}
	alongY := [3]float32{-140765.86 - 5000*s, 66128.34 + 5000*c, 8315}
	if !InShape(box, alongX) {
		t.Fatal("alongX should be inside rotated box")
	}
	if InShape(box, alongY) {
		t.Fatal("alongY should be outside rotated box")
	}
	if InShape(box, [3]float32{-140765.86, 66128.34, 8315 + 2000}) {
		t.Fatal("above Z extent should be outside box")
	}
	if !InShape(Sub{Shape: 0, Ext: [3]float32{2000, 0, 0}}, [3]float32{1999, 0, 99999}) {
		t.Fatal("circle ignores Z")
	}
	if InShape(Sub{Shape: 2, Ext: [3]float32{800, 600, 0}}, [3]float32{0, 0, 700}) {
		t.Fatal("cylinder checks half-height Z")
	}

	env := &fakeCondEnv{dungeonID: 8}
	outside := allCheck(box, Sub{T: "CompareDungeonId", Op: "Eq", Dungeon: 8})
	if !v.Evaluate(0, outside, env) {
		t.Fatal("Evaluate treats unknown pos as met")
	}
	if v.EvaluateKnown(0, outside, env) {
		t.Fatal("EvaluateKnown treats unknown pos as not met")
	}
	env.playerPos = &alongY
	if !v.EvaluateKnown(0, outside, env) {
		t.Fatal("alongY is outside box (InRange=false)")
	}
	env.playerPos = &alongX
	if v.EvaluateKnown(0, outside, env) {
		t.Fatal("alongX is inside box (InRange=false)")
	}
	if !v.EvaluateKnown(0, allCheck(Sub{T: "CheckDungeonFinish", Dungeon: 1504}), env) {
		t.Fatal("dungeon 1504 should be cleared")
	}
	if v.EvaluateKnown(0, allCheck(Sub{T: "CheckDungeonFinish", Dungeon: 1511}), env) {
		t.Fatal("dungeon 1511 should not be cleared")
	}
	if v.EvaluateKnown(0, allCheck(Sub{T: "CheckItems", Op: "Lt", Items: [][]int32{{70050066, 1}}}), env) {
		t.Fatal("have 1, not < 1")
	}
	if !v.EvaluateKnown(0, allCheck(Sub{T: "CheckNodeStatus", Node: 48, Num: 4}), env) {
		t.Fatal("node 48 status 4 should match")
	}
	if v.EvaluateKnown(0, allCheck(Sub{T: "CheckNodeStatus", Node: 52, Num: 4}), env) {
		t.Fatal("unknown node 52 should be false under EvaluateKnown")
	}
	if !v.EvaluateKnown(0, Check{}, env) {
		t.Fatal("empty check should be true")
	}
}

func TestSavedLinesRoundTripAndLevelPlayVariablesAreNotSaved(t *testing.T) {
	v := NewBtVariables()
	v.Write(5, *selfRef("n"), BtNum(2.5))
	v.Write(5, VarRef{Src: "Global", T: "String", Name: "g"}, BtString("周常肉鸽 第一期\tx"))
	v.Write(LevelPlayRun(9), *selfRef("lp"), BtNum(1))

	lines := map[string]string{}
	for _, kl := range v.BuildLines() {
		lines[kl.Key] = kl.Line
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 saved var lines, got %d: %v", len(lines), lines)
	}
	if lines["V\tS5:n"] != "V\tS5:n\tn\t2.5" {
		t.Fatalf("unexpected num line: %q", lines["V\tS5:n"])
	}
	if lines["V\tG:g"] != "V\tG:g\ts\t周常肉鸽 第一期 x" {
		t.Fatalf("unexpected str line: %q", lines["V\tG:g"])
	}

	w := NewBtVariables()
	for _, line := range lines {
		if !w.LoadLine(line) {
			t.Fatalf("LoadLine returned false for %q", line)
		}
	}
	if w.Read(5, *selfRef("n")).Num != 2.5 {
		t.Fatalf("expected 2.5, got %v", w.Read(5, *selfRef("n")))
	}
	if w.Read(5, VarRef{Src: "Global", T: "String", Name: "g"}).Str != "周常肉鸽 第一期 x" {
		t.Fatalf("unexpected round-trip str: %q", w.Read(5, VarRef{Src: "Global", T: "String", Name: "g"}).Str)
	}
	drop, ok := DropVarLine("V\tS5:n")
	if !ok || !w.LoadLine(drop) {
		t.Fatalf("DropVarLine failed: %q %v", drop, ok)
	}
	if w.Read(5, *selfRef("n")).Num != 0 {
		t.Fatal("dropped var should read 0")
	}
	if w.LoadLine("F 100") {
		t.Fatal("F 100 is not a variable line")
	}
}

func TestBtVariablesOrderAndDotNetRoundTripDoubleFormat(t *testing.T) {
	v := NewBtVariables()
	// Insertion order + LIFO free-slot reuse (matches .NET Dictionary<string, BtValue>).
	v.Write(1, *selfRef("z"), BtNum(1e6))
	v.Write(1, *selfRef("a"), BtNum(1e15))
	v.Write(1, *selfRef("m"), BtNum(1e17))
	v.Write(1, *selfRef("b"), BtNum(1e-5))
	// Drop "a" (slot 1), then insert "c" (reuses slot 1).
	v.LoadLine("W\tS1:a")
	v.Write(1, *selfRef("c"), BtNum(0.0001))

	got := v.BuildLines()
	want := []string{
		"V\tS1:z\tn\t1000000",
		"V\tS1:c\tn\t0.0001",
		"V\tS1:m\tn\t1E+17",
		"V\tS1:b\tn\t1E-05",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Line != want[i] {
			t.Fatalf("line %d = %q, want %q (all: %v)", i, got[i].Line, want[i], got)
		}
	}
}
