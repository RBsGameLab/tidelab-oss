// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"sync"

	"tidelab/internal/data"
	"tidelab/protocol"
)

// ----- the server's half of the AI state machines (specs/combat.md §9.7, R-152) -----
//
// The client runs every state machine and predicts the transitions marked Autonomous (1) or
// Simulated (2), and every transition out of a conduit node. A transition marked Server (0) is
// the server's: the client reports the conditions it judges (IsClient) with
// FsmConditionPassPush, the server judges the rest, and when the transition holds the server
// switches and sends ChangeStateNotify. Without this a monster never leaves its sleep or idle
// state (client: AiStateMachineTransition.CanPrediction, AiStateMachineCondition.ReqFsmConditionPass,
// AiStateMachineGroup.HandleSwitch).

const (
	fsmPredictionServer = 0
	fsmMaxSwitchPasses  = 8
	fsmMaxCondDepth     = 16
)

type fsmCond struct {
	kind       string
	reverse    bool
	client     bool
	children   []int
	tag        int32
	minMs      int64
	maxMs      int64
	attr       int32
	denom      int32
	min, max   float64
	target     int32
	targetName string
	buff       int64
	minStack   int32
	maxStack   int32
}

type fsmTransition struct {
	from, to int32
	server   bool
	conds    []fsmCond
	client   []int // the IsClient conditions the client reports for it (reachable from 0)
}

type fsmNode struct {
	uuid        int32
	name        string
	parent      int32 // 0: a root
	children    []int32
	ref         int32 // a reference node: the root it stands for
	conduit     bool
	anyState    bool
	anim        bool
	transitions []*fsmTransition
}

// FsmGraph is one state machine group as the client builds it from an AiStateMachineConfig and
// SM_Common (AiStateMachineGroup.OnActivate, AiStateMachineBase's constructor): common nodes
// overridden by the entity's (OverrideCommonUuid), reference nodes standing for other roots, and
// each transition kept on its From node.
type FsmGraph struct {
	nodes  map[int32]*fsmNode
	roots  []int32
	byName map[string]int32
	alias  map[int32]int32 // a StateMachines entry -> the root the client builds for it
}

// Root is the root the client builds for a StateMachines entry (the FsmId it uses), or the
// entry itself.
func (g *FsmGraph) Root(entry int32) int32 {
	if r, ok := g.alias[entry]; ok {
		return r
	}
	return entry
}

func buildFsmGraph(common, local *data.Object) *FsmGraph {
	g := &FsmGraph{nodes: map[int32]*fsmNode{}, byName: map[string]int32{}, alias: map[int32]int32{}}
	nodeData := map[int32]*data.Object{}
	refs := map[int32]int32{}
	overrides := map[int32]int32{}
	var refOrder []int32
	addRef := func(u, r int32) {
		if _, seen := refs[u]; !seen {
			refOrder = append(refOrder, u)
		}
		refs[u] = r
	}
	eachNode := func(root *data.Object, f func(*data.Object, int32)) {
		if nodes, ok := member(root, "Nodes").(*data.Array); ok {
			for _, it := range nodes.Items {
				if n := objOf(it); n != nil {
					f(n, readInt(n.Get("Uuid")))
				}
			}
		}
	}
	eachNode(common, func(n *data.Object, u int32) {
		nodeData[u] = n
		if r, ok := optInt(n.Get("ReferenceUuid")); ok {
			addRef(u, r)
		}
	})
	eachNode(local, func(n *data.Object, u int32) { nodeData[u] = n })
	eachNode(local, func(n *data.Object, u int32) {
		if o, ok := optInt(n.Get("OverrideCommonUuid")); ok {
			overrides[o] = u
		}
		if r, ok := optInt(n.Get("ReferenceUuid")); ok {
			addRef(u, r)
		}
	})
	get := func(u int32) *data.Object {
		if o, ok := overrides[u]; ok {
			u = o
		}
		return nodeData[u]
	}
	resolve := func(u int32) int32 {
		if n := get(u); n != nil {
			return readInt(n.Get("Uuid"))
		}
		return 0
	}
	var build func(u, parent int32, depth int) int32
	build = func(u, parent int32, depth int) int32 {
		nd := get(u)
		if nd == nil || depth > 64 {
			return 0
		}
		uuid := readInt(nd.Get("Uuid"))
		if uuid == 0 {
			return 0 // 0 means "a root" here; one 3.8 machine has such a node (an empty override)
		}
		if _, done := g.nodes[uuid]; done {
			return uuid
		}
		n := &fsmNode{uuid: uuid, parent: parent, name: str(nd.Get("Name")),
			conduit: boolOf(nd.Get("IsConduitNode")), anyState: boolOf(nd.Get("IsAnyState")), anim: boolOf(nd.Get("IsAnimStateMachine"))}
		g.nodes[uuid] = n
		if r, ok := refs[uuid]; ok {
			n.ref = build(r, 0, depth+1)
			return uuid
		}
		if !n.anyState {
			g.byName[n.name] = uuid
			if o, ok := optInt(nd.Get("OverrideCommonUuid")); ok {
				if base := nodeData[o]; base != nil {
					g.byName[str(base.Get("Name"))] = uuid
				}
			}
		}
		if ch, ok := nd.Get("Children").(*data.Array); ok {
			for _, it := range ch.Items {
				if c := build(readInt(it), uuid, depth+1); c != 0 {
					n.children = append(n.children, c)
				}
			}
		}
		if ts, ok := nd.Get("Transitions").(*data.Array); ok {
			for _, it := range ts.Items {
				t := objOf(it)
				if t == nil {
					continue
				}
				from, to := g.nodes[resolve(readInt(t.Get("From")))], g.nodes[resolve(readInt(t.Get("To")))]
				if from == nil || to == nil {
					continue
				}
				tr := &fsmTransition{from: from.uuid, to: to.uuid, server: readInt(t.Get("TransitionPredictionType")) == fsmPredictionServer}
				if cs, ok := t.Get("Conditions").(*data.Array); ok {
					for _, c := range cs.Items {
						tr.conds = append(tr.conds, parseFsmCond(objOf(c)))
					}
				}
				if len(tr.conds) > 0 {
					tr.client = clientConds(tr.conds)
					from.transitions = append(from.transitions, tr)
				}
			}
		}
		return uuid
	}
	if sms, ok := member(local, "StateMachines").(*data.Array); ok {
		for _, it := range sms.Items {
			entry := readInt(it)
			nd := get(entry)
			if nd == nil {
				continue
			}
			start := readInt(nd.Get("Uuid"))
			if r, ok := refs[start]; ok {
				start = r
			}
			root := build(start, 0, 0)
			if root == 0 {
				continue
			}
			g.alias[entry] = root
			g.addRoot(root)
		}
	}
	for _, u := range refOrder {
		if r := resolve(refs[u]); g.nodes[r] != nil {
			g.addRoot(r)
		}
	}
	return g
}

func (g *FsmGraph) addRoot(u int32) {
	for _, r := range g.roots {
		if r == u {
			return
		}
	}
	if n := g.nodes[u]; n != nil && n.parent == 0 {
		g.roots = append(g.roots, u)
	}
}

// clientConds are the conditions the client builds for a transition (from index 0 through
// CondAnd/CondOr) that it judges and reports itself.
func clientConds(conds []fsmCond) []int {
	var out []int
	seen := map[int]bool{}
	var walk func(i, depth int)
	walk = func(i, depth int) {
		if i < 0 || i >= len(conds) || seen[i] || depth > fsmMaxCondDepth {
			return
		}
		seen[i] = true
		if conds[i].client {
			out = append(out, i)
		}
		for _, j := range conds[i].children {
			walk(j, depth+1)
		}
	}
	walk(0, 0)
	return out
}

func parseFsmCond(o *data.Object) fsmCond {
	c := fsmCond{kind: str(member(o, "Name")), reverse: boolOf(member(o, "Reverse")), client: boolOf(member(o, "IsClient"))}
	body := objOf(member(o, c.kind))
	switch c.kind {
	case "CondAnd", "CondOr":
		if a, ok := member(body, "Conditions").(*data.Array); ok {
			for _, it := range a.Items {
				c.children = append(c.children, int(readInt(it)))
			}
		}
	case "CondTag", "CondInstStateChange":
		c.tag = readInt(member(body, "TagId"))
	case "CondTimer":
		c.minMs, c.maxMs = int64(readNum(member(body, "MinTime"))), int64(readNum(member(body, "MaxTime")))
	case "CondAttribute":
		c.attr, c.min, c.max = readInt(member(body, "AttributeId")), readNum(member(body, "Min")), readNum(member(body, "Max"))
	case "CondAttributeRate":
		c.attr, c.denom = readInt(member(body, "AttributeId")), readInt(member(body, "Denominator"))
		c.min, c.max = readNum(member(body, "Min"))*0.0001, readNum(member(body, "Max"))*0.0001
	case "CondCheckState":
		c.target = readInt(member(body, "TargetState"))
	case "CondCheckStateByName":
		c.targetName = str(member(body, "TargetStateName"))
	case "CondBuffStack":
		c.buff = int64(readNum(member(body, "BuffId")))
		c.minStack, c.maxStack = readInt(member(body, "MinStack")), readInt(member(body, "MaxStack"))
	}
	return c
}

func optInt(v data.Value) (int32, bool) {
	if n, ok := v.(data.Number); ok {
		return int32(float64(n)), true
	}
	return 0, false
}

func readNum(v data.Value) float64 {
	if n, ok := v.(data.Number); ok {
		return float64(n)
	}
	return float64(readInt(v))
}

func str(v data.Value) string {
	if s, ok := v.(data.String); ok {
		return string(s)
	}
	return ""
}

func boolOf(v data.Value) bool {
	b, ok := v.(data.Bool)
	return ok && bool(b)
}

// ----- one entity's state machines -----

type fsmCondKey struct {
	t *fsmTransition
	i int
}

// FsmRuntime is which nodes of an entity's state machines are active, as the client has them
// after the same switches (StateMachineCommon: entering a node enters its first child), with the
// client's reported condition values and the server's timers.
type FsmRuntime struct {
	g        *FsmGraph
	active   map[int32]bool
	current  map[int32]int32          // an active node -> its active child
	holders  map[int32]map[int32]bool // a referenced root -> its active reference nodes
	reported map[fsmCondKey]bool      // IsClient conditions: the client's last Value
	timers   map[fsmCondKey]int64     // server CondTimer: when it turns true
	rng      Rng
}

// FsmSwitch is a switch the server made: what ChangeStateNotify carries.
type FsmSwitch struct{ FsmID, From, To int32 }

// NewFsmRuntime starts the state machines at the given states (the DFsm CurrentStates the client
// is sent, keyed by FsmId), as the client's StartState does.
func NewFsmRuntime(g *FsmGraph, starts []*protocol.DFsm, nowMs int64, rng Rng) *FsmRuntime {
	if rng == nil {
		rng = SystemRng{}
	}
	r := &FsmRuntime{g: g, active: map[int32]bool{}, current: map[int32]int32{}, holders: map[int32]map[int32]bool{},
		reported: map[fsmCondKey]bool{}, timers: map[fsmCondKey]int64{}, rng: rng}
	for _, f := range starts {
		if g.nodes[f.CurrentState] != nil {
			r.start(f.CurrentState, nowMs)
		}
	}
	return r
}

// Active is whether a node is active.
func (r *FsmRuntime) Active(uuid int32) bool { return r.active[uuid] }

// Leaf is the active leaf under a root (the root itself when it has no active child).
func (r *FsmRuntime) Leaf(root int32) int32 {
	cur := root
	for i := 0; i < 64; i++ {
		next, ok := r.current[cur]
		if !ok {
			return cur
		}
		cur = next
	}
	return cur
}

// start is StateMachineCommon.Start: the parents are started without entering their first
// child, and the node becomes their current one.
func (r *FsmRuntime) start(u int32, nowMs int64) {
	n := r.g.nodes[u]
	if n == nil || r.active[u] {
		return
	}
	if n.parent != 0 {
		r.start(n.parent, nowMs)
		if cur, ok := r.current[n.parent]; ok && cur != u {
			r.exit(cur)
		}
		r.current[n.parent] = u
	}
	r.enter(u, false, nowMs)
}

func (r *FsmRuntime) enter(u int32, sub bool, nowMs int64) {
	n := r.g.nodes[u]
	if n == nil {
		return
	}
	r.active[u] = true
	for _, t := range n.transitions {
		for i, c := range t.conds {
			k := fsmCondKey{t, i}
			delete(r.reported, k)
			delete(r.timers, k)
			if c.kind == "CondTimer" && !c.client {
				d := c.minMs
				if c.maxMs > c.minMs {
					d += int64(r.rng.Next(int32(min(c.maxMs-c.minMs+1, 1<<30))))
				}
				r.timers[k] = nowMs + d
			}
		}
	}
	if n.ref != 0 {
		if r.holders[n.ref] == nil {
			r.holders[n.ref] = map[int32]bool{}
		}
		r.holders[n.ref][u] = true
		if !r.active[n.ref] {
			r.enter(n.ref, true, nowMs)
		}
		return
	}
	if sub && len(n.children) > 0 {
		first := n.children[0]
		r.current[u] = first
		r.enter(first, true, nowMs)
	}
}

func (r *FsmRuntime) exit(u int32) {
	n := r.g.nodes[u]
	if n == nil {
		return
	}
	if c, ok := r.current[u]; ok {
		delete(r.current, u)
		r.exit(c)
	}
	if n.ref != 0 {
		delete(r.holders[n.ref], u)
		if len(r.holders[n.ref]) == 0 && r.active[n.ref] {
			r.exit(n.ref)
		}
	}
	for _, t := range n.transitions {
		for i := range t.conds {
			delete(r.reported, fsmCondKey{t, i})
			delete(r.timers, fsmCondKey{t, i})
		}
	}
	delete(r.active, u)
}

// SwitchTo is a switch to a node under its parent (the client's TrySwitch and ForceActive): the
// parent is made active if it is not, its current child exits and the node enters with its first
// children. False when nothing changed.
func (r *FsmRuntime) SwitchTo(to int32, nowMs int64) bool {
	n := r.g.nodes[to]
	if n == nil || n.parent == 0 {
		return false
	}
	p := n.parent
	if !r.active[p] {
		r.forceActive(p, nowMs)
	}
	if cur, ok := r.current[p]; ok {
		if cur == to {
			return false
		}
		r.exit(cur)
	}
	r.current[p] = to
	r.enter(to, true, nowMs)
	return true
}

func (r *FsmRuntime) forceActive(u int32, nowMs int64) {
	n := r.g.nodes[u]
	if n == nil {
		return
	}
	if n.parent == 0 {
		r.enter(u, true, nowMs)
		return
	}
	r.SwitchTo(u, nowMs)
}

// Report records a condition value the client judged (FsmConditionPassPush/Request), on every
// transition from From to To.
func (r *FsmRuntime) Report(from, to int32, index int32, value bool) {
	n := r.g.nodes[from]
	if n == nil {
		return
	}
	for _, t := range n.transitions {
		if t.to == to && int(index) >= 0 && int(index) < len(t.conds) {
			r.reported[fsmCondKey{t, int(index)}] = value
		}
	}
}

// RootOf is the root a node belongs to.
func (r *FsmRuntime) RootOf(u int32) int32 {
	for i := 0; i < 64; i++ {
		n := r.g.nodes[u]
		if n == nil || n.parent == 0 {
			return u
		}
		u = n.parent
	}
	return u
}

// Step makes the server's switches that hold now, in the client's order (each root: a node's
// transitions, then its current child's), until none holds.
func (r *FsmRuntime) Step(e *CombatEntity, nowMs int64) []FsmSwitch {
	var out []FsmSwitch
	for pass := 0; pass < fsmMaxSwitchPasses; pass++ {
		changed := false
		for _, root := range r.g.roots {
			if !r.active[root] || r.g.nodes[root].anim {
				continue
			}
			if t := r.ready(root, e, nowMs, 0); t != nil && r.SwitchTo(t.to, nowMs) {
				out = append(out, FsmSwitch{FsmID: root, From: t.from, To: t.to})
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return out
}

func (r *FsmRuntime) ready(u int32, e *CombatEntity, nowMs int64, depth int) *fsmTransition {
	n := r.g.nodes[u]
	if n == nil || depth > 64 {
		return nil
	}
	if !n.conduit {
		for _, t := range n.transitions {
			if t.server && r.reportedAll(t) && r.eval(t, 0, e, nowMs, 0) {
				return t
			}
		}
	}
	if c, ok := r.current[u]; ok {
		return r.ready(c, e, nowMs, depth+1)
	}
	return nil
}

// reportedAll is whether the client has reported every condition it judges for a transition since
// its From node was entered. Until then the transition is not judged: a missing report is not
// "false" (a reversed one would hold), and the client reports each one as soon as the node is
// active on its side (AiStateMachineCondition.Tick: LastResult starts null).
func (r *FsmRuntime) reportedAll(t *fsmTransition) bool {
	for _, i := range t.client {
		if _, ok := r.reported[fsmCondKey{t, i}]; !ok {
			return false
		}
	}
	return true
}

// eval is one condition of a transition. A client condition is the client's last report (it
// already applied Reverse); the others are judged here, as the client's condition classes do.
func (r *FsmRuntime) eval(t *fsmTransition, i int, e *CombatEntity, nowMs int64, depth int) bool {
	if i < 0 || i >= len(t.conds) || depth > fsmMaxCondDepth {
		return false
	}
	c := &t.conds[i]
	k := fsmCondKey{t, i}
	if c.client {
		return r.reported[k]
	}
	var self bool
	switch c.kind {
	case "CondAnd":
		self = true
		for _, j := range c.children {
			if !r.eval(t, j, e, nowMs, depth+1) {
				self = false
				break
			}
		}
	case "CondOr":
		for _, j := range c.children {
			if r.eval(t, j, e, nowMs, depth+1) {
				self = true
				break
			}
		}
	case "CondTrue":
		self = true
	case "CondHate":
		self = e != nil && e.HasHate
	case "CondTimer":
		at, ok := r.timers[k]
		self = ok && nowMs >= at
	case "CondCheckState":
		self = r.active[c.target]
	case "CondCheckStateByName":
		u, ok := r.g.byName[c.targetName]
		self = ok && r.active[u]
	case "CondTag", "CondInstStateChange":
		self = e != nil && e.TagCounts.At(c.tag) > 0
	case "CondAttribute":
		if v, ok := attrValue(e, c.attr); ok {
			self = v >= c.min && v <= c.max
		}
	case "CondAttributeRate":
		v, ok1 := attrValue(e, c.attr)
		d, ok2 := attrValue(e, c.denom)
		if ok1 && ok2 && d != 0 {
			rate := v / d
			self = rate >= c.min && rate <= c.max
		}
	case "CondBuffStack":
		var stack int32
		if e != nil {
			for _, b := range e.Buffs {
				if b.BuffID == c.buff {
					stack += b.StackCount
				}
			}
		}
		self = stack >= c.minStack && stack <= c.maxStack
	default:
		// CondPartLife, CondListenEvent, CondCheckLastState, CondCheckPartActivated,
		// CondCheckDissolveCombine and the client-only kinds: the server does not judge them
		// (the client's own classes leave most of them false too).
		self = false
	}
	return self != c.reverse
}

func attrValue(e *CombatEntity, id int32) (float64, bool) {
	if e == nil || e.Attributes == nil || id <= 0 || id >= AttrCount {
		return 0, false
	}
	return float64(e.Attributes.Slots[id].Current), true
}

// syncFsmComponent keeps the entity's EntityFsmComponentPb at its current leaves, so that the
// entity sent again starts where it is.
func (e *CombatEntity) syncFsmComponent() {
	if e.Fsm == nil || e.FsmComponent == nil {
		return
	}
	for _, f := range e.FsmComponent.Fsms {
		root := e.Fsm.g.Root(f.FsmId)
		if e.Fsm.active[root] {
			f.CurrentState = e.Fsm.Leaf(root)
		}
	}
}

// ----- the graphs, built once per state machine -----

type fsmGraphCache struct {
	mu     sync.Mutex
	graphs map[string]*FsmGraph
}

func (c *fsmGraphCache) get(t *data.DataTables, id string) *FsmGraph {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g, ok := c.graphs[id]; ok {
		return g
	}
	var g *FsmGraph
	common, ok1 := t.AiStateMachineConfigById[CommonStateMachineID]
	cfg, ok2 := t.AiStateMachineConfigById[id]
	if ok1 && ok2 {
		g = buildFsmGraph(objOf(common.StateMachineJson), objOf(cfg.StateMachineJson))
	}
	if c.graphs == nil {
		c.graphs = map[string]*FsmGraph{}
	}
	c.graphs[id] = g
	return g
}
