// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
	"tidelab/internal/zon"
)

// VarRef is a behaviour-tree variable reference as our quest-data generator normalises it (R-4g):
// Src Self (the tree), Global (Name = keyword), Other (Ref = entity id), Constant (Num or Str),
// System. T: Int, Boolean, Float, String. Ported from Conditions.cs.
type VarRef struct {
	Src  string
	T    string
	Name string
	Ref  int64
	Num  float64
	Str  string
}

func (r VarRef) IsString() bool { return r.T == "String" }

// Sub is one condition of a CompareVar list or a CheckConditionGroup. T names the condition type;
// the other fields are set per type.
type Sub struct {
	T         string
	A         *VarRef
	B         *VarRef
	Op        string
	Entity    int64
	State     string
	Num       int32
	Entities  []int64
	Exist     bool
	Quest     int32
	Node      int32
	LevelPlay int32
	Dungeon   int32
	// CheckPlayerGender (R-76a): "男" or "女".
	Gender string
	// CheckInRange (R-76h) / CheckPlayerPosition (R-82a): inside (true) or outside (false).
	InRange bool
	// CheckPlayerPosition: 0 circle (Ext[0] radius, any height), 1 box (Ext half sizes, turned
	// Yaw degrees), 2 cylinder (Ext[0] radius, Ext[1] half height).
	Shape  byte
	Center [3]float32
	Ext    [3]float32
	Yaw    float32
	// CheckItems (R-82a): [item id, count] pairs, each compared with Op.
	Items [][]int32
}

// Check is a CompareVar list or condition group. Count is EConditionCount: 0 all, 1 any, 2..5 at
// least that many.
type Check struct {
	Count int32
	Conds []Sub
}

// VarOp is SetVar (Op "Set": Dst = A) or CalculateVar (Op Add/Sub/Mut: Dst = A op B).
type VarOp struct {
	Op  string
	Dst *VarRef
	A   *VarRef
	B   *VarRef
}

// BtValue is a variable's value: a number or a string.
type BtValue struct {
	Num      float64
	Str      string
	IsString bool
}

func BtNum(n float64) BtValue   { return BtValue{Num: n} }
func BtString(s string) BtValue { return BtValue{Str: s, IsString: true} }
func BtZero(r VarRef) BtValue {
	if r.IsString() {
		return BtString("")
	}
	return BtNum(0)
}

// ConditionEnv is what the quest engine knows about the world for the sub-conditions that ask it.
// Each answer returns ok=false when the server cannot tell (the condition then counts as met, or,
// for fix processors, as not met).
type ConditionEnv interface {
	EntityHasState(id int64, state string) (bool, bool)
	EntityExists(id int64) (bool, bool)
	// EQuestState: 0 InActive, 1 Ready, 2 Progress, 3 Finish.
	QuestState(quest int32) (int32, bool)
	ChildDone(quest, node int32) (bool, bool)
	// ELevelPlayState: 0 Close, 1 Wait, 2 Open, 3 Finish.
	LevelPlayState(levelPlay int32) (int32, bool)
	DungeonID() int32
	TreasureClaimed(id int64) (bool, bool)
	// CheckPlayerGender (R-76a): the Rover is male. ok=false: not known yet.
	Male() (bool, bool)
	// CheckInRange (R-76h): is the player inside any of these range entities?
	InRange(ids []int64) (bool, bool)
	// R-82a (fix processors): the player's position on the map the tree is about.
	PlayerPos() ([3]float32, bool)
	ItemCount(id int32) (int32, bool)
	DungeonCleared(id int32) (bool, bool)
	NodeStatus(run int64, node int32) (int32, bool)
	ChildStatus(run int64, node int32) (int32, bool)
}

// DefaultConditionEnv returns unknown (ok=false) for every query; embed it to override only what is known.
type DefaultConditionEnv struct{}

func (DefaultConditionEnv) EntityHasState(int64, string) (bool, bool) { return false, false }
func (DefaultConditionEnv) EntityExists(int64) (bool, bool)           { return false, false }
func (DefaultConditionEnv) QuestState(int32) (int32, bool)            { return 0, false }
func (DefaultConditionEnv) ChildDone(int32, int32) (bool, bool)       { return false, false }
func (DefaultConditionEnv) LevelPlayState(int32) (int32, bool)        { return 0, false }
func (DefaultConditionEnv) DungeonID() int32                          { return 0 }
func (DefaultConditionEnv) TreasureClaimed(int64) (bool, bool)        { return false, false }
func (DefaultConditionEnv) Male() (bool, bool)                        { return false, false }
func (DefaultConditionEnv) InRange([]int64) (bool, bool)              { return false, false }
func (DefaultConditionEnv) PlayerPos() ([3]float32, bool)             { return [3]float32{}, false }
func (DefaultConditionEnv) ItemCount(int32) (int32, bool)             { return 0, false }
func (DefaultConditionEnv) DungeonCleared(int32) (bool, bool)         { return false, false }
func (DefaultConditionEnv) NodeStatus(int64, int32) (int32, bool)     { return 0, false }
func (DefaultConditionEnv) ChildStatus(int64, int32) (int32, bool)    { return 0, false }

// LevelPlayBit distinguishes level-play run keys from quest run keys (run >= LevelPlayBit).
const LevelPlayBit int64 = 1 << 32

// IsLevelPlayRun is true when run is a level play key.
func IsLevelPlayRun(run int64) bool { return run >= LevelPlayBit }

// LevelPlayRun encodes a level play id as a run key.
func LevelPlayRun(tree int32) int64 { return LevelPlayBit | int64(uint32(tree)) }

// BtVariables holds behaviour-tree variables (SetVar, CalculateVar) and evaluates conditions
// (CompareVar, CheckConditionGroup), R-4g and R-83. One instance per player.
// Uses omap.Map for vars so BuildLines iterates in .NET Dictionary<string, BtValue> order.
type BtVariables struct {
	vars            omap.Map[string, BtValue]
	initials        map[string]BtValue
	writers         map[string]struct{}
	untracked       map[int64]struct{}
	lpSharedWriters map[string]struct{}
}

func NewBtVariables() *BtVariables {
	return &BtVariables{
		initials:        map[string]BtValue{},
		writers:         map[string]struct{}{},
		untracked:       map[int64]struct{}{},
		lpSharedWriters: map[string]struct{}{},
	}
}

func varKeyOf(run int64, r VarRef) (string, bool) {
	switch r.Src {
	case "Self":
		return "S" + strconv.FormatInt(run, 10) + ":" + r.Name, true
	case "Global":
		return "G:" + r.Name, true
	case "Other":
		return "E" + strconv.FormatInt(r.Ref, 10) + ":" + r.Name, true
	default:
		return "", false
	}
}

// "<q|l><tree id>:<name>": a run's Self variable in the tree data.
func treeVarKey(run int64, name string) string {
	prefix := "q"
	if IsLevelPlayRun(run) {
		prefix = "l"
	}
	return prefix + strconv.FormatInt(run&(LevelPlayBit-1), 10) + ":" + name
}

// Declare records a tree's declared initial value (TreeVars.json).
func (v *BtVariables) Declare(levelPlay bool, tree int32, name string, value BtValue) {
	run := int64(tree)
	if levelPlay {
		run = LevelPlayRun(tree)
	}
	v.initials[treeVarKey(run, name)] = value
}

// NoteEntityGlobal marks a global variable entity logic uses (EntityGlobals.json): unknown to quests.
func (v *BtVariables) NoteEntityGlobal(name string) {
	v.lpSharedWriters["G:"+name] = struct{}{}
}

// NoteWriter records that some action of the tree writes dst (each SetVar / CalculateVar at load).
func (v *BtVariables) NoteWriter(levelPlay bool, tree int32, dst VarRef) {
	if levelPlay && dst.Src != "Self" {
		if key, ok := varKeyOf(0, dst); ok {
			v.lpSharedWriters[key] = struct{}{}
		}
		return
	}
	if dst.Src == "Self" {
		run := int64(tree)
		if levelPlay {
			run = LevelPlayRun(tree)
		}
		v.writers[treeVarKey(run, dst.Name)] = struct{}{}
	}
}

func (v *BtVariables) SetUntracked(run int64, yes bool) {
	if yes {
		v.untracked[run] = struct{}{}
	} else {
		delete(v.untracked, run)
	}
}

func (v *BtVariables) IsTracked(run int64) bool {
	_, bad := v.untracked[run]
	return !bad
}

func (v *BtVariables) unsetSelf(run int64, r VarRef) (BtValue, bool) {
	key := treeVarKey(run, r.Name)
	if !IsLevelPlayRun(run) {
		if _, unt := v.untracked[run]; unt {
			return BtValue{}, false
		}
		if _, written := v.writers[key]; !written {
			return BtValue{}, false
		}
	}
	if val, ok := v.initials[key]; ok {
		return val, true
	}
	return BtZero(r), true
}

// Lookup returns a variable's value, or ok=false when the server cannot know it.
func (v *BtVariables) Lookup(run int64, r VarRef) (BtValue, bool) {
	if r.Src == "Constant" {
		if r.IsString() {
			return BtString(r.Str), true
		}
		return BtNum(r.Num), true
	}
	key, ok := varKeyOf(run, r)
	if !ok {
		return BtValue{}, false
	}
	self := r.Src == "Self"
	if !IsLevelPlayRun(run) {
		if r.Src == "Other" {
			return BtValue{}, false
		}
		if !self {
			if _, shared := v.lpSharedWriters[key]; shared {
				return BtValue{}, false
			}
		}
	}
	if val, ok := v.vars.Get(key); ok {
		return val, true
	}
	if self {
		return v.unsetSelf(run, r)
	}
	return BtValue{}, false
}

// Read is Lookup with unknowns as 0 / "" (a counter nobody set starts at 0).
func (v *BtVariables) Read(run int64, r VarRef) BtValue {
	if val, ok := v.Lookup(run, r); ok {
		return val
	}
	return BtZero(r)
}

func (v *BtVariables) Write(run int64, r VarRef, value BtValue) {
	if key, ok := varKeyOf(run, r); ok {
		v.vars.Set(key, value)
	}
}

// Apply runs a SetVar / CalculateVar; returns true if it wrote a variable.
func (v *BtVariables) Apply(run int64, op VarOp) bool {
	if op.Dst == nil {
		return false
	}
	a := BtNum(0)
	if op.A != nil {
		a = v.Read(run, *op.A)
	}
	if op.Op == "Set" {
		v.Write(run, *op.Dst, a)
		return true
	}
	b := BtNum(0)
	if op.B != nil {
		b = v.Read(run, *op.B)
	}
	if a.IsString || b.IsString {
		return false
	}
	var result float64
	switch op.Op {
	case "Add":
		result = a.Num + b.Num
	case "Sub":
		result = a.Num - b.Num
	case "Mut":
		result = a.Num * b.Num
	default:
		common.Warn("conditions", fmt.Sprintf("CalculateVar: unknown op %s", op.Op))
		return false
	}
	v.Write(run, *op.Dst, BtNum(result))
	return true
}

func CompareBt(a BtValue, op string, b BtValue) bool {
	if a.IsString || b.IsString {
		eq := a.Str == b.Str
		if op == "Ne" {
			return !eq
		}
		return eq
	}
	switch op {
	case "Eq":
		return a.Num == b.Num
	case "Ne":
		return a.Num != b.Num
	case "Ge":
		return a.Num >= b.Num
	case "Gt":
		return a.Num > b.Num
	case "Le":
		return a.Num <= b.Num
	case "Lt":
		return a.Num < b.Num
	default:
		return a.Num == b.Num
	}
}

func cmpInt(a int32, op string, b int32) bool {
	return CompareBt(BtNum(float64(a)), op, BtNum(float64(b)))
}

// InShape reports whether p is inside a CheckPlayerPosition shape (R-82a).
func InShape(sub Sub, p [3]float32) bool {
	dx := p[0] - sub.Center[0]
	dy := p[1] - sub.Center[1]
	dz := p[2] - sub.Center[2]
	switch sub.Shape {
	case 0:
		return dx*dx+dy*dy <= sub.Ext[0]*sub.Ext[0]
	case 2:
		return dx*dx+dy*dy <= sub.Ext[0]*sub.Ext[0] && float32(math.Abs(float64(dz))) <= sub.Ext[1]
	default:
		yaw := float64(sub.Yaw) * math.Pi / 180.0
		c, s := float32(math.Cos(yaw)), float32(math.Sin(yaw))
		lx := dx*c + dy*s
		ly := -dx*s + dy*c
		return float32(math.Abs(float64(lx))) <= sub.Ext[0] &&
			float32(math.Abs(float64(ly))) <= sub.Ext[1] &&
			(sub.Ext[2] == 0 || float32(math.Abs(float64(dz))) <= sub.Ext[2])
	}
}

// EvalSub evaluates one sub-condition: (result, known). When known is false the server cannot judge it.
func (v *BtVariables) EvalSub(run int64, sub Sub, env ConditionEnv) (bool, bool) {
	if sub.T == "CompareVar" {
		if sub.A == nil || sub.B == nil {
			return false, false
		}
		a, okA := v.Lookup(run, *sub.A)
		if !okA {
			return false, false
		}
		b, okB := v.Lookup(run, *sub.B)
		if !okB {
			return false, false
		}
		return CompareBt(a, sub.Op, b), true
	}
	if env == nil {
		return false, false
	}
	switch sub.T {
	case "CompareEntityState":
		has, ok := env.EntityHasState(sub.Entity, sub.State)
		if !ok {
			return false, false
		}
		if sub.Op == "Ne" {
			return !has, true
		}
		return has, true
	case "CheckEntitesExist":
		for _, id := range sub.Entities {
			exists, ok := env.EntityExists(id)
			if !ok {
				return false, false
			}
			if exists != sub.Exist {
				return false, true
			}
		}
		return true, true
	case "CompareQuestState":
		qs, ok := env.QuestState(sub.Quest)
		if !ok {
			return false, false
		}
		return cmpInt(qs, sub.Op, sub.Num), true
	case "CheckChildQuestFinished":
		return env.ChildDone(sub.Quest, sub.Node)
	case "CheckLevelPlayState":
		ls, ok := env.LevelPlayState(sub.LevelPlay)
		if !ok {
			return false, false
		}
		return cmpInt(ls, sub.Op, sub.Num), true
	case "CompareDungeonId":
		return cmpInt(env.DungeonID(), sub.Op, sub.Dungeon), true
	case "CheckTreasureBeenClaimed":
		return env.TreasureClaimed(sub.Entity)
	case "CheckInRange":
		inside, ok := env.InRange(sub.Entities)
		if !ok {
			return false, false
		}
		return inside == sub.InRange, true
	case "CheckPlayerGender":
		male, ok := env.Male()
		if !ok {
			return false, false
		}
		switch sub.Gender {
		case "男":
			return male, true
		case "女":
			return !male, true
		default:
			return false, false
		}
	case "CheckPlayerPosition":
		pos, ok := env.PlayerPos()
		if !ok {
			return false, false
		}
		return InShape(sub, pos) == sub.InRange, true
	case "CheckItems":
		if len(sub.Items) == 0 {
			return false, false
		}
		for _, it := range sub.Items {
			if len(it) < 2 {
				return false, false
			}
			have, ok := env.ItemCount(it[0])
			if !ok {
				return false, false
			}
			if !cmpInt(have, sub.Op, it[1]) {
				return false, true
			}
		}
		return true, true
	case "CheckDungeonFinish":
		return env.DungeonCleared(sub.Dungeon)
	case "CheckNodeStatus":
		ns, ok := env.NodeStatus(run, sub.Node)
		if !ok {
			return false, false
		}
		return ns == sub.Num, true
	case "CheckChildQuestStatus":
		cs, ok := env.ChildStatus(run, sub.Node)
		if !ok {
			return false, false
		}
		return cs == sub.Num, true
	default:
		return false, false
	}
}

func checkNeeded(check Check) int {
	n := len(check.Conds)
	switch check.Count {
	case 0:
		return n
	case 1:
		return min(1, n)
	default:
		return min(int(check.Count), n)
	}
}

// Evaluate checks a CompareVar list or condition group; unknown sub-conditions count as met.
func (v *BtVariables) Evaluate(run int64, check Check, env ConditionEnv) bool {
	met := 0
	for _, s := range check.Conds {
		res, ok := v.EvalSub(run, s, env)
		if !ok || res {
			met++
		}
	}
	return met >= checkNeeded(check)
}

// EvaluateKnown is like Evaluate, but unknown counts as not met (fix processors act only on what holds).
func (v *BtVariables) EvaluateKnown(run int64, check Check, env ConditionEnv) bool {
	met := 0
	for _, s := range check.Conds {
		res, ok := v.EvalSub(run, s, env)
		if ok && res {
			met++
		}
	}
	return met >= checkNeeded(check)
}

// R-83 saving: quest, global and entity variables go in the quests file as
// "V\t<key>\t<n|s>\t<value>" lines keyed "V\t<key>"; "W\t<key>" forgets one. Level-play
// variables live as long as their dungeon run and are not saved.

func isSavedVarKey(key string) bool {
	if len(key) < 2 || key[0] != 'S' {
		return true
	}
	colon := strings.IndexByte(key, ':')
	if colon < 0 {
		return false
	}
	run, err := strconv.ParseInt(key[1:colon], 10, 64)
	return err == nil && !IsLevelPlayRun(run)
}

var tabNewlineReplacer = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ")

func formatBtDouble(n float64) string {
	switch {
	case math.IsNaN(n):
		return "NaN"
	case math.IsInf(n, 1):
		return "Infinity"
	case math.IsInf(n, -1):
		return "-Infinity"
	}
	s, _ := zon.Float64(n).NumberText()
	return s
}

// BuildLines returns the saved variables as keyed lines ("V\t<key>") in .NET Dictionary order,
// formatted with C#'s double.ToString("R", CultureInfo.InvariantCulture).
func (v *BtVariables) BuildLines() []KeyedLine {
	var out []KeyedLine
	for key, val := range v.vars.All() {
		if !isSavedVarKey(key) {
			continue
		}
		k := "V\t" + key
		if val.IsString {
			out = append(out, KeyedLine{Key: k, Line: k + "\ts\t" + tabNewlineReplacer.Replace(val.Str)})
		} else {
			out = append(out, KeyedLine{Key: k, Line: k + "\tn\t" + formatBtDouble(val.Num)})
		}
	}
	return out
}

// DropVarLine returns the drop marker for a variable line key ("W\t<key>").
func DropVarLine(lineKey string) (string, bool) {
	if strings.HasPrefix(lineKey, "V\t") {
		return "W\t" + lineKey[2:], true
	}
	return "", false
}

// LoadLine applies one saved line ("V…" or "W…"); returns false if it is not a variable line.
func (v *BtVariables) LoadLine(line string) bool {
	f := strings.SplitN(line, "\t", 4)
	if f[0] == "W" {
		if len(f) > 1 {
			v.vars.Delete(f[1])
		}
		return true
	}
	if f[0] != "V" {
		return false
	}
	if len(f) < 3 {
		return true
	}
	text := ""
	if len(f) > 3 {
		text = f[3]
	}
	if f[2] == "s" {
		v.vars.Set(f[1], BtString(text))
	} else if n, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
		v.vars.Set(f[1], BtNum(n))
	}
	return true
}

// Clear forgets every variable (another player's progress is loaded).
func (v *BtVariables) Clear() {
	v.vars.Clear()
	clear(v.untracked)
}

// DropRun forgets a run's own variables (a level play ending with its dungeon).
func (v *BtVariables) DropRun(run int64) {
	prefix := "S" + strconv.FormatInt(run, 10) + ":"
	var drop []string
	for k := range v.vars.All() {
		if strings.HasPrefix(k, prefix) {
			drop = append(drop, k)
		}
	}
	for _, k := range drop {
		v.vars.Delete(k)
	}
	delete(v.untracked, run)
}
