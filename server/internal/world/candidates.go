// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"fmt"
	"math"
	"strings"

	"tidelab/internal/data"
)

// SpawnClass is a candidate's class: its radii and caps (specs/spawning.md §3).
type SpawnClass int

const (
	ClassBeacon SpawnClass = iota
	ClassItem
	ClassSeat
	ClassNpc
	// ClassOther: monsters and everything else.
	ClassOther
)

// Candidate is a row of the map that may appear, with its class and flags (spawning.md §2).
type Candidate struct {
	Order         int
	Row           *data.LevelEntityConfig
	Class         SpawnClass
	Sleeping      bool
	Location      Vec3
	SpotKey       string // "" when none
	HasVisibility bool
}

func (c *Candidate) EntityID() int32 { return c.Row.EntityId }

// CandidateRules is what the quests say about a scene's candidates (specs/quests.md §5.5).
type CandidateRules struct {
	Entered           bool
	OffersEnabled     bool
	LevelPlayEntities map[int64]bool
	Owned             func(mapID int32, id int64) bool
	KillTarget        func(id int64) bool
	Referenced        map[int64]bool
}

func newSet(items ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range items {
		m[s] = true
	}
	return m
}

var (
	npcTypeList       = []string{"Npc", "SimpleNPc", "PasserbyNpc"}
	monsterTypeList   = []string{"Monster"}
	chairTypeList     = []string{"Chair", "Chair2"}
	entranceTypeList  = []string{"DungeonEntry", "DungeonEntryWithTrigger"}
	traversalTypeList = []string{
		"HookLockPoint", "HookWithRange", "KiteHook", "CableWayHook", "SuiGuangHook", "TimeScaledHook",
		"SequenceDrivenHook", "ElementalResponseHook", "Lift", "Lift2", "SequenceLift", "BeamDeliver",
		"Portal", "NoRenderPortal", "NoRenderPortalWithInteract", "MoveNoRenderPortal", "InteractGear",
		"InteractGear2", "GameplayInteractGear", "StateSceneItem", "Collect", "CollectWithHook",
		"CollectWithListener", "TreasureBox", "VisionTreasureBox", "HookTreasureBox",
	}
	NpcTypes       = newSet(npcTypeList...)
	MonsterTypes   = newSet(monsterTypeList...)
	ChairTypes     = newSet(chairTypeList...)
	EntranceTypes  = newSet(entranceTypeList...)
	TraversalTypes = newSet(traversalTypeList...)

	Facilities = newSet(
		"SceneObj100", "SceneObj101", "Quest099", "NPC_Shenghai1", "NPC0001", "NPC0002", "NPC0003", "NPC0004", "NPC0005",
		"NPC0006", "NPC0008", "NPC0012", "NPC0103", "NPC420078", "branch2.0_NPC料理店商人乔万娜",
		"branch2.0_NPC生命点商人薇达", "branch2.0_TsEntity_拉古纳新贵族女4881159",
		"branch3.0_314_NPC_zhiwuguo", "branch3.0_152_NPC扎希拉", "branch3.0_152_NPCIRIS",
		"branch3.0_152_NPC阿尔塞尼", "branch3.0_181_NPC娜娜", "branch3.5_192_TsEntity_秋鸿",
		"branch3.5_176_NPC鲙娘")

	FilteredNameParts = []string{"任务", "道馆", "梦魇", "竞技", "测试", "演示", "剧情", "教学", "引导", "副本", "活动", "召唤", "特殊敌人", "提前开放", "插旗", "怪物对战", "王之试炼"}

	DoorActions = newSet("SetPlayerPos", "TeleportDungeon", "ExitDungeon")
)

// ComparedTypes are every BlueprintConfig.EntityType the spawner compares with: each must occur
// in the data (entities.md §3.1).
func ComparedTypes() []string {
	var out []string
	for _, l := range [][]string{npcTypeList, monsterTypeList, chairTypeList, entranceTypeList, traversalTypeList} {
		out = append(out, l...)
	}
	return out
}

func IsBeaconBlueprint(bp string) bool {
	return strings.HasPrefix(bp, "Teleport") || bp == "branch2.0_Teleport6" || bp == "branch2.2_14_Teleport4"
}

func IsFacility(bp string) bool { return IsBeaconBlueprint(bp) || Facilities[bp] }

// PassesNameFilter (R-25, R-77): true when the name has none of the parts (a door ignores 副本).
func PassesNameFilter(name string, door bool) bool {
	for _, p := range FilteredNameParts {
		if (!door || p != "副本") && strings.Contains(name, p) {
			return false
		}
	}
	return true
}

// HasDoorOption: an InteractComponent has an option whose actions move the player (a door).
func HasDoorOption(components data.Value) bool {
	options, ok := Field(Field(components, "InteractComponent"), "Options").(*data.Array)
	if !ok {
		return false
	}
	for _, o := range options.Items {
		actions, ok := Field(Field(o, "Type"), "Actions").(*data.Array)
		if !ok {
			continue
		}
		for _, a := range actions.Items {
			if n, ok := Str(Field(a, "Name")); ok && DoorActions[n] {
				return true
			}
		}
	}
	return false
}

// IsScriptedActor: a scripted actor has no HP bar (HeadStateViewConfig.HeadStateViewType 0) or
// is bound to a plot sequence (Category.EntityPlotBindingType), in its merged BaseInfoComponent.
func IsScriptedActor(baseInfo *data.Object) bool {
	if baseInfo == nil {
		return false
	}
	if n, ok := Field(baseInfo.Get("HeadStateViewConfig"), "HeadStateViewType").(data.Number); ok && n == 0 {
		return true
	}
	v := Field(baseInfo.Get("Category"), "EntityPlotBindingType")
	return v != nil && !IsNull(v)
}

// Metres is a transform coordinate (hundredths of a centimetre) in whole metres, halves away from zero.
func Metres(transform int32) int64 { return int64(math.Round(float64(transform) / 10000.0)) }

// BuildCandidates is the candidate list of a map (specs/spawning.md §2): four passes over the
// map's rows, each row taken once (a sleeping row that pass 1b took comes again in pass 3), with
// the quest rules of specs/quests.md §5.5 when rules is not nil.
func BuildCandidates(d *WorldData, instanceID int32, rules *CandidateRules) []*Candidate {
	t := d.Tables
	m := d.MapOf(instanceID)
	exits := map[int32]bool{}
	if dg, ok := t.InstanceDungeonById[instanceID]; ok {
		for _, e := range dg.ExitEntities {
			exits[e] = true
		}
	}
	var rows []*data.LevelEntityConfig
	for _, i := range t.LevelEntityRowsOfMap(m) {
		rows = append(rows, t.LevelEntityConfig[i])
	}

	var result []*Candidate
	taken := map[int32]bool{}
	take := func(row *data.LevelEntityConfig, cls SpawnClass, sleeping bool, spot string) {
		loc, _, _ := Place(row)
		result = append(result, &Candidate{len(result), row, cls, sleeping, loc, spot, VisibilityConditions(row.ComponentsData) != nil})
		taken[row.EntityId] = true
	}
	typeOf := func(row *data.LevelEntityConfig) string {
		if b := d.Blueprint(row.BlueprintType); b != nil {
			return b.EntityType
		}
		return ""
	}
	placeable := func(row *data.LevelEntityConfig) bool {
		return len(row.Transform) >= 2 && row.Transform[0] != nil && row.Transform[1] != nil
	}
	townNpc := func(row *data.LevelEntityConfig) bool {
		return !row.InSleep && (strings.HasPrefix(row.BlueprintType, "NPC") || NpcTypes[typeOf(row)]) && PassesNameFilter(row.Name, false)
	}
	wantedMonster := func(row *data.LevelEntityConfig) bool {
		if row.MapId != m || row.IsHidden ||
			!(strings.HasPrefix(row.BlueprintType, "Monster") || MonsterTypes[typeOf(row)]) ||
			!PassesNameFilter(row.Name, false) {
			return false
		}
		var tc data.Value
		if tpl := d.Template(row.BlueprintType); tpl != nil {
			tc = tpl.ComponentsData
		}
		return !IsScriptedActor(Merge(row.ComponentsData, tc)["BaseInfoComponent"])
	}
	doorBlueprint := func(row *data.LevelEntityConfig) bool {
		return d.IsDoorTemplate(row.BlueprintType) || HasDoorOption(row.ComponentsData)
	}
	beaconOr := func(row *data.LevelEntityConfig, other SpawnClass) SpawnClass {
		if IsBeaconBlueprint(row.BlueprintType) {
			return ClassBeacon
		}
		return other
	}

	// Pass 1: facilities.
	for _, row := range rows {
		if row.IsHidden || !placeable(row) || row.InSleep || !IsFacility(row.BlueprintType) {
			continue
		}
		take(row, beaconOr(row, ClassOther), false, "")
	}

	// Pass 1b: scene items.
	for _, row := range rows {
		if row.IsHidden || !placeable(row) || IsFacility(row.BlueprintType) || taken[row.EntityId] {
			continue
		}
		typ := typeOf(row)
		chair := ChairTypes[typ]
		door := !row.InSleep && (EntranceTypes[typ] || (doorBlueprint(row) && !townNpc(row) && !wantedMonster(row)))
		// An entered dungeon's level play entities (its start device ...): any blueprint, no name filter.
		play := rules != nil && rules.Entered && !row.InSleep && rules.LevelPlayEntities[int64(row.EntityId)]
		item := TraversalTypes[typ] || exits[row.EntityId] || door || (chair && !row.InSleep) || play
		if !item {
			continue
		}
		if !play && !row.InSleep && !PassesNameFilter(row.Name, door) {
			continue
		}
		cls := ClassItem
		if chair {
			cls = ClassSeat
		}
		take(row, cls, row.InSleep, "")
	}

	// Pass 2: monsters and town NPCs; one monster per spot (R-25).
	type spot struct {
		bp      string
		x, y, z int32
	}
	spots := map[spot]bool{}
	for _, row := range rows {
		if row.IsHidden || !placeable(row) || IsFacility(row.BlueprintType) || taken[row.EntityId] {
			continue
		}
		if wantedMonster(row) {
			// A quest's sleeping monster (owned, or a kill target) waits for its quest and claims no spot.
			if rules != nil && row.InSleep && (rules.Owned(m, int64(row.EntityId)) || rules.KillTarget(int64(row.EntityId))) {
				take(row, ClassOther, true, "")
				continue
			}
			p := row.Transform[0]
			k := spot{row.BlueprintType, p.X, p.Y, p.Z}
			first := !spots[k]
			spots[k] = true
			// In an entered dungeon the map's monsters come when its level play or spawner wakes them.
			take(row, ClassOther, !first || (rules != nil && rules.Entered), "")
		} else if townNpc(row) {
			p := row.Transform[0]
			key := fmt.Sprintf("%s|%d|%d|%d", row.BlueprintType, Metres(p.X), Metres(p.Y), Metres(p.Z))
			take(row, ClassNpc, false, key)
		}
	}

	// Pass 3: plain sleeping rows (not checked against earlier passes).
	pass3 := map[int32]bool{}
	for _, row := range rows {
		if row.IsHidden || !placeable(row) || !row.InSleep || IsFacility(row.BlueprintType) ||
			TraversalTypes[typeOf(row)] || wantedMonster(row) || pass3[row.EntityId] {
			continue
		}
		pass3[row.EntityId] = true
		if taken[row.EntityId] && !exits[row.EntityId] {
			continue
		}
		take(row, beaconOr(row, ClassOther), true, "")
	}

	// Quest rows: any row quest data refers to (hidden ones too) that no pass took, sleeping.
	if rules != nil {
		for _, row := range rows {
			if !placeable(row) || taken[row.EntityId] || !rules.Referenced[int64(row.EntityId)] {
				continue
			}
			take(row, beaconOr(row, ClassOther), true, "")
		}
	}
	return result
}
