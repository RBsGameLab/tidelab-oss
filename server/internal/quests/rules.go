// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import "slices"

// Progress is quest progress as the unlock rules see it (R-10, R-61, R-62).
type Progress interface {
	QuestDone(id int32) bool
	NodeDone(quest, node int32) bool
	PlayDone(id int32) bool
	DungeonDone(id int32) bool
	HasItems(want [][]int32) bool
}

// DefaultProgress provides default false implementations for optional Progress checks.
type DefaultProgress struct{}

func (DefaultProgress) PlayDone(int32) bool     { return false }
func (DefaultProgress) DungeonDone(int32) bool  { return false }
func (DefaultProgress) HasItems([][]int32) bool { return false }

// The rules of which quests start by themselves, which are offered, and which level play a
// dungeon runs. Ported from QuestRules.cs.

const (
	// StartQuest is the story start: the prologue counts as done, 嘤鸣初相召 is next.
	StartQuest int32 = 139000027
	// MainStoryType quests (QuestData Type 1) are accepted when their unlock conditions hold.
	MainStoryType int32 = 1
	// TutorialType quests (R-47: 玩法教学).
	TutorialType int32 = 7
)

var (
	// PreFinished are prologue quests that count as done for a fresh player.
	PreFinished = []int32{139000025, 139000026}
	// ActivityGuideQuests start on their own (R-53: 139000039 opens 失序梦途).
	ActivityGuideQuests = []int32{139000039}
)

// AutoAccepted reports whether the server accepts def by itself once its unlock conditions hold:
// the main story, the activity guide quests, and tutorials with unlock conditions but no OpenDate
// (R-47b: the 追月节 opening teleported the player to a spot without ground).
func AutoAccepted(def QuestDef) bool {
	if def.Type == MainStoryType || slices.Contains(ActivityGuideQuests, def.ID) {
		return true
	}
	if def.Type != TutorialType || len(def.Provide.Conds) == 0 {
		return false
	}
	for _, c := range def.Provide.Conds {
		if c.Type == "OpenDate" {
			return false
		}
	}
	return true
}

func IsProgressCond(c ProvideCond) bool {
	return c.Type == "PreQuest" || c.Type == "PreChildQuest"
}

// CondMet evaluates PreQuest and PreChildQuest by progress. ExploreLevel (the server reports every
// region's top level), OpenDate (offline, everything is open) and untracked conditions count as met.
func CondMet(c ProvideCond, p Progress) bool {
	switch c.Type {
	case "PreQuest":
		return p.QuestDone(c.Quest)
	case "PreChildQuest":
		return p.QuestDone(c.Quest) || p.NodeDone(c.Quest, c.Node)
	default:
		return true
	}
}

// Unlocked reports whether main story quest def may start now (R-10). Only quests that follow
// another (a PreQuest or PreChildQuest condition, or the PreQuest field) start on their own.
func Unlocked(def QuestDef, p Progress) bool {
	conds := def.Provide.Conds
	if len(conds) == 0 {
		return def.PreQuest != 0 && p.QuestDone(def.PreQuest)
	}
	chained := false
	progressMet := false
	for _, c := range conds {
		met := CondMet(c, p)
		if IsProgressCond(c) {
			chained = true
			if met {
				progressMet = true
			}
		}
		if !def.Provide.Any && !met {
			return false
		}
	}
	// Any: a condition counted as met (a date, a level) must not start it alone.
	return chained && (!def.Provide.Any || progressMet)
}

// OfferUnlocked reports whether side quest def may be given now (R-61). Conditions the server does
// not track count as not met; ExploreLevel counts as met; PreLevelPlay, PreDungeon and Item are
// tracked (R-62). A System quest needs a condition; an Interact quest without any is always on offer.
func OfferUnlocked(def QuestDef, offer Offer, p Progress) bool {
	conds := def.Provide.Conds
	if len(conds) == 0 {
		return offer.Interact
	}
	anyMet := false
	for _, c := range conds {
		var met bool
		if IsProgressCond(c) {
			met = CondMet(c, p)
		} else {
			switch c.Type {
			case "PreLevelPlay":
				met = p.PlayDone(c.Play)
			case "PreDungeon":
				met = p.DungeonDone(c.Dungeon)
			case "Item":
				met = len(offer.Items) > 0 && p.HasItems(offer.Items)
			case "ExploreLevel":
				met = true
			default:
				met = false
			}
		}
		if !def.Provide.Any && !met {
			return false
		}
		if met {
			anyMet = true
		}
	}
	return anyMet
}

// WorldPlayOpen checks a world play's open conditions: PreQuest / PreChildQuest by progress, ExploreLevel met.
func WorldPlayOpen(wp WorldPlay, p Progress) bool {
	conds := wp.Open.Conds
	if len(conds) == 0 {
		return true
	}
	anyMet := false
	for _, c := range conds {
		var met bool
		if IsProgressCond(c) {
			met = CondMet(c, p)
		} else {
			met = c.Type == "ExploreLevel"
		}
		if !wp.Open.Any && !met {
			return false
		}
		if met {
			anyMet = true
		}
	}
	return anyMet
}

// InSphere reports whether pos is inside the skill step's sphere (R-14; no sphere: anywhere).
func InSphere(skill UseSkill, pos [3]float32) bool {
	if len(skill.Center) < 3 || skill.Radius <= 0 {
		return true
	}
	dx := float64(pos[0]) - skill.Center[0]
	dy := float64(pos[1]) - skill.Center[1]
	dz := float64(pos[2]) - skill.Center[2]
	return dx*dx+dy*dy+dz*dz <= skill.Radius*skill.Radius
}

// BestLevelPlay picks the dungeon level play for mapID: the one whose monsters match most of the
// dungeon's monsterPreview, else the first on the map. Sub plays are never a dungeon's own.
func BestLevelPlay(defs []LevelPlayDef, mapID int32, monsterPreview []int32) (int32, bool) {
	var best int32
	found := false
	bestHits := 0
	for _, lp := range defs {
		if lp.LevelID != mapID || lp.Sub {
			continue
		}
		hits := 0
		for _, m := range monsterPreview {
			if slices.Contains(lp.Monsters, m) {
				hits++
			}
		}
		if !found || hits > bestHits {
			best = lp.ID
			found = true
			bestHits = hits
		}
	}
	return best, found
}

// ClientConditions are child quest conditions the client checks itself and reports with SubmitNodeRequest.
var ClientConditions = map[string]struct{}{
	"ReachArea":                       {},
	"ReadPhoneMessage":                {},
	"TakePhoto":                       {},
	"TakePhoto2":                      {},
	"UseSkill":                        {},
	"ShowUi":                          {},
	"Guide":                           {},
	"WaitUntilLevelSequenceReachMark": {},
	"CheckUiGame":                     {},
	"CheckClientConditionGroup":       {},
	"UseItem":                         {},
	"ReadMail":                        {},
	"ReceiveTelecom":                  {},
	"FinishArcadeGameplay":            {},
	"AwakeAndLoadEntity":              {},
	"DetectCombatState":               {},
	"DetectCombatState2":              {},
	"AtomicProcess":                   {},
	"CheckPlayerInput":                {},
}

func IsClientCondition(cond string) bool {
	_, ok := ClientConditions[cond]
	return ok
}

// HasOptions reports whether def's Options are NPC options the server adds (DoInteract, R-76g HandInItems).
func HasOptions(def NodeDef) bool {
	return def.Cond == "DoInteract" || def.Cond == "HandInItems"
}

func IsDungeonStep(def NodeDef) bool {
	return def.Cond == "FinishDungeon" || def.Cond == "EnterDungeon" || def.Cond == "LeaveDungeon"
}

func IsVarConditionStep(def NodeDef) bool {
	return def.Cond == "CompareVar" || def.Cond == "CheckConditionGroup"
}

func IsConditionStep(def NodeDef) bool {
	return IsVarConditionStep(def) || def.Cond == "CheckLevelPlay"
}

// DungeonMatches checks whether the step's dungeon is instanceID (R-4e): by InstanceDungeon Id or InstSubType.
func DungeonMatches(def NodeDef, instanceID int32, subTypeOf func(int32) (int32, bool)) bool {
	if def.Dungeon != 0 {
		return def.Dungeon == instanceID
	}
	if def.SubType == 0 || subTypeOf == nil {
		return false
	}
	st, ok := subTypeOf(instanceID)
	return ok && st == def.SubType
}
