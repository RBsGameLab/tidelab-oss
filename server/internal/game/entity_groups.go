// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"slices"
	"sync"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
)

const (
	// EntityGroupFinished and EntityGroupFailed are the state tags a group takes on FinishState / FailureState (R-80a).
	EntityGroupFinished = "关卡.Common.状态.完成"
	EntityGroupFailed   = "关卡.Common.状态.失败"
)

type entityGroupKey struct {
	group int64
	slot  int64
}

type entityGroupIndex struct {
	byMember map[int64][]*data.LevelEntityConfig
}

var (
	groupIndexMu     sync.Mutex
	groupIndexTables *data.DataTables
	groupIndexCached *entityGroupIndex
)

func getEntityGroupIndex(tables *data.DataTables) *entityGroupIndex {
	groupIndexMu.Lock()
	defer groupIndexMu.Unlock()
	if groupIndexTables == tables && groupIndexCached != nil {
		return groupIndexCached
	}
	idx := buildEntityGroupIndex(tables)
	groupIndexTables = tables
	groupIndexCached = idx
	return idx
}

// EntityGroups executes 机关组 (EntityGroupComponent StateTriggers, FinishState, FailureState; R-43, R-80a).
// Ported from our C# Game/EntityGroups.cs.
type EntityGroups struct {
	holding map[entityGroupKey]struct{}
}

func NewEntityGroups() *EntityGroups {
	return &EntityGroups{holding: map[entityGroupKey]struct{}{}}
}

// GroupOf returns a row's EntityGroupComponent when it is an enabled 机关组.
func GroupOf(row *data.LevelEntityConfig) *data.Object {
	c, ok := row.ComponentsData.(*data.Object)
	if !ok || c == nil {
		return nil
	}
	eg, ok := c.Get("EntityGroupComponent").(*data.Object)
	if !ok || eg == nil {
		return nil
	}
	if b, ok := eg.Get("Disabled").(data.Bool); ok && bool(b) {
		return nil
	}
	return eg
}

func buildEntityGroupIndex(tables *data.DataTables) *entityGroupIndex {
	idx := &entityGroupIndex{byMember: map[int64][]*data.LevelEntityConfig{}}
	groups := 0
	for _, row := range tables.LevelEntityConfig {
		eg := GroupOf(row)
		if eg == nil {
			continue
		}
		groups++
		if triggers, ok := eg.Get("StateTriggers").(*data.Array); ok && triggers != nil {
			for _, t := range triggers.Items {
				if to, ok := t.(*data.Object); ok && to != nil {
					indexGroupCondition(idx, row, to.Get("GroupCondition"))
				}
			}
		}
		if f, ok := eg.Get("FinishState").(*data.Object); ok && f != nil {
			indexGroupCondition(idx, row, f.Get("GroupCondition"))
		}
		if fail, ok := eg.Get("FailureState").(*data.Object); ok && fail != nil {
			indexGroupCondition(idx, row, fail.Get("GroupCondition"))
		}
	}
	common.Info("entity_groups", fmt.Sprintf("%d 机关组 with triggers, %d member entities", groups, len(idx.byMember)))
	return idx
}

func indexGroupCondition(idx *entityGroupIndex, row *data.LevelEntityConfig, group data.Value) {
	g, ok := group.(*data.Object)
	if !ok || g == nil {
		return
	}
	conds, ok := g.Get("Conditions").(*data.Array)
	if !ok || conds == nil {
		return
	}
	for _, c := range conds.Items {
		co, ok := c.(*data.Object)
		if !ok || co == nil {
			continue
		}
		idn, ok := co.Get("EntityId").(data.Number)
		if !ok {
			continue
		}
		id := int64(idn)
		rows := idx.byMember[id]
		if !slices.Contains(rows, row) {
			idx.byMember[id] = append(rows, row)
		}
	}
}

// GroupProbe supplies member state, death status, and tag lookup to GroupHolds.
type GroupProbe interface {
	State(id int64) (int32, bool)
	Dead(id int64) bool
	Tag(name string) (int32, bool)
}

// GroupHolds reports whether a group condition holds (R-80a): each condition checks either
// IsDead or State == Tag(name); Count (default: all) must hold; an empty list never holds.
func GroupHolds(group data.Value, probe GroupProbe) bool {
	g, ok := group.(*data.Object)
	if !ok || g == nil {
		return false
	}
	conds, ok := g.Get("Conditions").(*data.Array)
	if !ok || conds == nil || len(conds.Items) == 0 {
		return false
	}
	n := 0
	for _, item := range conds.Items {
		c, ok := item.(*data.Object)
		if !ok || c == nil {
			continue
		}
		idn, ok := c.Get("EntityId").(data.Number)
		if !ok {
			continue
		}
		id := int64(idn)
		if dead, ok := c.Get("IsDead").(data.Bool); ok && bool(dead) && probe.Dead(id) {
			n++
			continue
		}
		name, ok := c.Get("State").(data.String)
		if !ok {
			continue
		}
		want, ok := probe.Tag(string(name))
		if !ok {
			continue
		}
		have, ok := probe.State(id)
		if !ok {
			continue
		}
		if have == want {
			n++
		}
	}
	need := len(conds.Items)
	if count, ok := g.Get("Count").(data.Number); ok {
		need = int(count)
	}
	if need < 1 {
		need = 1
	}
	return n >= need
}

type sessionGroupProbe struct{ s *PlayerSession }

func (p sessionGroupProbe) State(id int64) (int32, bool) {
	if st, ok := p.s.World.State.StateOverride(id); ok {
		return st, true
	}
	return p.s.Actions().SpawnedState(id)
}

func (p sessionGroupProbe) Dead(id int64) bool { return p.s.IsDead(id) }

func (p sessionGroupProbe) Tag(name string) (int32, bool) {
	return world.TagID(p.s.Data.Tables, name), true
}

// OnEntityState evaluates triggers of this map's groups naming configID after its state changed or it died.
func (g *EntityGroups) OnEntityState(s *PlayerSession, configID int64) {
	if s.Scene == nil {
		return
	}
	idx := getEntityGroupIndex(s.Data.Tables)
	rows, ok := idx.byMember[configID]
	if !ok {
		return
	}
	probe := sessionGroupProbe{s: s}
	// A copy: actions below can re-enter OnEntityState.
	for _, row := range slices.Clone(rows) {
		if row.MapId != s.Scene.MapID {
			continue
		}
		eg := GroupOf(row)
		if eg == nil {
			continue
		}
		if f, ok := eg.Get("FinishState").(*data.Object); ok && f != nil {
			g.outcome(s, probe, row, f.Get("GroupCondition"), -1, EntityGroupFinished)
		}
		if fail, ok := eg.Get("FailureState").(*data.Object); ok && fail != nil {
			g.outcome(s, probe, row, fail.Get("GroupCondition"), -2, EntityGroupFailed)
		}
		var triggers []data.Value
		if arr, ok := eg.Get("StateTriggers").(*data.Array); ok && arr != nil {
			triggers = arr.Items
		}
		for i, item := range triggers {
			t, ok := item.(*data.Object)
			if !ok || t == nil {
				continue
			}
			key := entityGroupKey{group: int64(row.EntityId), slot: int64(i)}
			now := GroupHolds(t.Get("GroupCondition"), probe)
			_, was := g.holding[key]
			if !now {
				delete(g.holding, key)
				continue
			}
			if was {
				continue
			}
			// Mark first: its own actions change member states and re-enter here.
			g.holding[key] = struct{}{}
			list, ok := t.Get("SuccessActions").(*data.Array)
			if !ok || list == nil {
				continue
			}
			actions := EntityActionsFromList(list)
			common.Info("entity_groups", fmt.Sprintf("机关组 %d (%s) trigger %d: condition holds, %d actions",
				row.EntityId, row.Name, i, len(actions)))
			s.Actions().RunAsEntity(int64(row.EntityId), actions)
		}
	}
}

func (g *EntityGroups) outcome(s *PlayerSession, probe GroupProbe, row *data.LevelEntityConfig, group data.Value, slot int64, state string) {
	key := entityGroupKey{group: int64(row.EntityId), slot: slot}
	if !GroupHolds(group, probe) {
		delete(g.holding, key)
		return
	}
	if _, existed := g.holding[key]; existed {
		return
	}
	g.holding[key] = struct{}{}
	common.Info("entity_groups", fmt.Sprintf("机关组 %d (%s): %s", row.EntityId, row.Name, state))
	st := state
	s.Actions().RunAsEntity(int64(row.EntityId), []quests.QuestAction{
		{Name: "ChangeSelfEntityState", Params: &quests.ActionParams{EntityState: &st}},
	})
}
