// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"slices"
	"sort"

	"tidelab/internal/common"
)

type questNodeKey struct {
	quest int32
	node  int32
}

// Catalog holds the quest engine's shared data: every quest and level play as a tree, the offers,
// the world plays, and the indexes built from them. Loaded once per process; each player's Engine
// reads it. Ported from QuestCatalog.cs.
type Catalog struct {
	Data          *QuestDataSet
	Quests        map[int32]*QuestTree
	LevelPlays    map[int32]*QuestTree
	WorldOnly     map[int32]struct{}
	WorldPlayIDs  map[int32]struct{}
	QuestDungeons map[int32]struct{}
	preChild      map[questNodeKey][]QuestDef
	killTargets   map[int64]struct{}
}

func NewCatalog(data *QuestDataSet) *Catalog {
	if data == nil {
		data = NewQuestDataSet()
	}
	c := &Catalog{
		Data:          data,
		Quests:        make(map[int32]*QuestTree, len(data.Quests)),
		LevelPlays:    make(map[int32]*QuestTree, len(data.LevelPlays)+len(data.WorldPlays)),
		WorldOnly:     map[int32]struct{}{},
		WorldPlayIDs:  make(map[int32]struct{}, len(data.WorldPlays)),
		QuestDungeons: map[int32]struct{}{},
		preChild:      map[questNodeKey][]QuestDef{},
	}
	for _, def := range data.Quests {
		for _, n := range def.Nodes {
			if n.DungeonID != 0 {
				c.QuestDungeons[n.DungeonID] = struct{}{}
			}
		}
		if tree := BuildTree(def, ChildOrderAsListed); tree != nil {
			c.Quests[def.ID] = tree
		}
		for _, cond := range def.Provide.Conds {
			if cond.Type != "PreChildQuest" {
				continue
			}
			k := questNodeKey{quest: cond.Quest, node: cond.Node}
			c.preChild[k] = append(c.preChild[k], def)
		}
	}
	for _, lp := range data.LevelPlays {
		def := QuestDef{ID: lp.ID, Name: lp.Name, Nodes: lp.Nodes}
		if tree := BuildTree(def, ChildOrderTerminalsLast); tree != nil {
			c.LevelPlays[lp.ID] = tree
		}
	}
	for _, wp := range data.WorldPlays {
		c.WorldPlayIDs[wp.ID] = struct{}{}
		if _, exists := c.LevelPlays[wp.ID]; exists {
			continue // also a dungeon's sub play: same tree
		}
		def := QuestDef{ID: wp.ID, Name: wp.Name, Nodes: wp.Nodes}
		if tree := BuildTree(def, ChildOrderTerminalsLast); tree != nil {
			c.LevelPlays[wp.ID] = tree
			c.WorldOnly[wp.ID] = struct{}{}
		}
	}
	common.Info("quest", fmt.Sprintf("%d quest trees, %d level play trees (%d world plays only)",
		len(c.Quests), len(c.LevelPlays), len(c.WorldOnly)))
	return c
}

// LoadCatalog loads assets/quest under dir into a Catalog.
func LoadCatalog(dir string) *Catalog {
	return NewCatalog(LoadDataSet(dir))
}

func (c *Catalog) LevelPlayDefs() []LevelPlayDef { return c.Data.LevelPlays }
func (c *Catalog) WorldPlays() []WorldPlay       { return c.Data.WorldPlays }
func (c *Catalog) Offers() map[int32]Offer       { return c.Data.Offers }

// Prepare initializes what a fresh player's tree variables start from (R-83): declared initial
// values, the Self variables each tree's own actions write, and the globals entity logic uses.
func (c *Catalog) Prepare(vars *BtVariables) {
	for id, list := range c.Data.QuestVars {
		for _, v := range list {
			vars.Declare(false, id, v.Name, v.Value)
		}
	}
	for id, list := range c.Data.LevelPlayVars {
		for _, v := range list {
			vars.Declare(true, id, v.Name, v.Value)
		}
	}
	for _, t := range c.Quests {
		noteTreeWriters(vars, false, t.Def)
	}
	for _, t := range c.LevelPlays {
		noteTreeWriters(vars, true, t.Def)
	}
	for _, name := range c.Data.EntityGlobals {
		vars.NoteEntityGlobal(name)
	}
}

func noteTreeWriters(vars *BtVariables, levelPlay bool, def QuestDef) {
	note := func(actions []QuestAction) {
		for _, a := range actions {
			if a.VarOp != nil && a.VarOp.Dst != nil {
				vars.NoteWriter(levelPlay, def.ID, *a.VarOp.Dst)
			}
		}
	}
	for _, n := range def.Nodes {
		note(n.Enter)
		note(n.Finish)
		note(n.Actions)
		for _, f := range n.Fix {
			note(f.Then)
		}
	}
}

func (c *Catalog) IsQuestOwned(mapID int32, entity int64) bool {
	_, ok := c.Data.Owned[EntityPair{Map: mapID, Entity: entity}]
	return ok
}

// UnlocksSomething reports whether an accepted or offered quest waits for child quest node of quest.
func (c *Catalog) UnlocksSomething(quest, node int32) bool {
	list := c.preChild[questNodeKey{quest: quest, node: node}]
	for _, d := range list {
		if AutoAccepted(d) {
			return true
		}
		if _, offered := c.Data.Offers[d.ID]; offered {
			return true
		}
	}
	return false
}

// IsKillTarget reports whether any quest's Kill step targets configID.
func (c *Catalog) IsKillTarget(configID int64) bool {
	if c.killTargets == nil {
		c.killTargets = map[int64]struct{}{}
		for _, t := range c.Quests {
			for _, n := range t.Def.Nodes {
				if n.Cond != "Kill" {
					continue
				}
				for _, id := range n.Targets {
					c.killTargets[id] = struct{}{}
				}
			}
		}
	}
	_, ok := c.killTargets[configID]
	return ok
}

// PreQuestChain returns the main story quests before quest (its PreQuest chain), nearest first.
func (c *Catalog) PreQuestChain(quest int32) []int32 {
	var chain []int32
	id := quest
	for {
		tree, ok := c.Quests[id]
		if !ok {
			break
		}
		// R-10: a quest that follows a child quest continues the chain through it.
		pre := tree.Def.PreQuest
		if pre == 0 {
			for _, cond := range tree.Def.Provide.Conds {
				if cond.Type == "PreChildQuest" {
					pre = cond.Quest
					break
				}
			}
		}
		if pre == 0 || slices.Contains(chain, pre) {
			break
		}
		chain = append(chain, pre)
		id = pre
	}
	return chain
}

// QuestIDsOfTypes returns the ids of every quest of these types, ascending (R-42).
func (c *Catalog) QuestIDsOfTypes(types ...int32) []int32 {
	var out []int32
	for _, t := range c.Quests {
		if slices.Contains(types, t.Def.Type) {
			out = append(out, t.Def.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// QuestDungeonIDs returns every dungeon a quest refers to, ascending (R-4e).
func (c *Catalog) QuestDungeonIDs() []int32 {
	seen := map[int32]struct{}{}
	add := func(id int32) {
		if id != 0 {
			seen[id] = struct{}{}
		}
	}
	scanActions := func(actions []QuestAction) {
		for _, a := range actions {
			if a.Name == "TeleportDungeon" && a.Params != nil && a.Params.DungeonID != nil {
				add(*a.Params.DungeonID)
			}
		}
	}
	for _, t := range c.Quests {
		for _, n := range t.Def.Nodes {
			add(n.Dungeon)
			add(n.DungeonID)
			scanActions(n.Enter)
			scanActions(n.Finish)
			scanActions(n.Actions)
			for _, o := range n.Options {
				scanActions(o.Actions)
			}
		}
	}
	out := make([]int32, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ReferencedEntities returns every entity a quest or level play step uses (R-33b: DoInteract
// options, CheckEntityState entities, Kill targets, SetEntityVisible). World plays count only
// when offersEnabled is true.
func (c *Catalog) ReferencedEntities(offersEnabled bool) map[int64]bool {
	set := map[int64]bool{}
	for _, t := range c.Quests {
		addReferencedEntities(t.Def.Nodes, set)
	}
	for id, t := range c.LevelPlays {
		if _, wo := c.WorldOnly[id]; offersEnabled || !wo {
			addReferencedEntities(t.Def.Nodes, set)
		}
	}
	return set
}

func addReferencedEntities(nodes []NodeDef, set map[int64]bool) {
	scan := func(actions []QuestAction) {
		for _, a := range actions {
			if a.Name == "SetEntityVisible" && a.Params != nil && a.Params.HasEntityIDs {
				for _, id := range a.Params.EntityIDs {
					set[id] = true
				}
			}
		}
	}
	for _, n := range nodes {
		for _, s := range n.States {
			set[s.EntityID] = true
		}
		for _, o := range n.Options {
			set[o.EntityID] = true
		}
		for _, id := range n.Targets {
			set[id] = true
		}
		scan(n.Enter)
		scan(n.Finish)
		scan(n.Actions)
	}
}

// LevelPlayEntities returns entities level play lp acts on: CheckEntityState targets and DoInteract options.
func (c *Catalog) LevelPlayEntities(lp int32) map[int64]bool {
	set := map[int64]bool{}
	if t, ok := c.LevelPlays[lp]; ok {
		for _, n := range t.Def.Nodes {
			for _, s := range n.States {
				set[s.EntityID] = true
			}
			for _, o := range n.Options {
				set[o.EntityID] = true
			}
		}
	}
	return set
}

// LevelPlayFor returns the dungeon level play for a map by MonsterPreview (see BestLevelPlay).
func (c *Catalog) LevelPlayFor(mapID int32, monsterPreview []int32) (int32, bool) {
	return BestLevelPlay(c.Data.LevelPlays, mapID, monsterPreview)
}

func (c *Catalog) QuestName(quest int32) string {
	if t, ok := c.Quests[quest]; ok {
		return t.Def.Name
	}
	return ""
}

func (c *Catalog) QuestType(quest int32) int32 {
	if t, ok := c.Quests[quest]; ok {
		return t.Def.Type
	}
	return 0
}
