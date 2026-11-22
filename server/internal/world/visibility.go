// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"tidelab/internal/data"
	"tidelab/internal/save"
)

// A row's EntityVisibleComponent conditions, judged by the server (ours). Groups are OR,
// conditions within a group AND. The server keeps no time of day or weather: clock, day/night
// and weather conditions are judged for a clear day at 10:00. Condition types it does not track
// show the entity.
const (
	GameHour  = 10
	WeatherID = 1
)

// VisibilityConditions is the row's visibility component, when it has conditions that count.
func VisibilityConditions(components data.Value) *data.Object {
	v := Obj(Field(components, "EntityVisibleComponent"))
	if v == nil {
		return nil
	}
	if b, ok := v.Get("Disabled").(data.Bool); ok && bool(b) {
		return nil
	}
	if arr, ok := v.Get("VisibleConditions").(*data.Array); !ok || len(arr.Items) == 0 {
		return nil
	}
	return v
}

func Visible(components data.Value, quests save.QuestView) bool {
	v := VisibilityConditions(components)
	if v == nil {
		return true
	}
	for _, g := range v.Get("VisibleConditions").(*data.Array).Items {
		if groupHolds(g, quests) {
			return true
		}
	}
	return false
}

func groupHolds(group data.Value, quests save.QuestView) bool {
	conditions, ok := Field(group, "Conditions").(*data.Array)
	if !ok {
		return true
	}
	for _, c := range conditions.Items {
		if o := Obj(c); o != nil && !Holds(o, quests) {
			return false
		}
	}
	return true
}

func Holds(c *data.Object, quests save.QuestView) bool {
	typ, ok := Str(c.Get("Type"))
	if !ok {
		return true
	}
	var value bool
	switch typ {
	case "PreQuest":
		q, ok := JSONInt(c.Get("PreQuestId"))
		if !ok {
			return true
		}
		value = quests.IsFinished(q)
	case "PreChildQuest":
		p := Obj(c.Get("PreChildQuest"))
		if p == nil {
			return true
		}
		value = quests.IsChildDone(JSONIntOr(p.Get("QuestId"), 0), JSONIntOr(p.Get("ChildQuestId"), 0))
	case "Clock":
		start, ok1 := JSONInt(c.Get("Start"))
		end, ok2 := JSONInt(c.Get("End"))
		if !ok1 || !ok2 {
			return true
		}
		value = InHours(start, end)
	case "TimePeriod":
		p, ok := Str(c.Get("TimePeriod"))
		if !ok {
			return true
		}
		value = p == "DayTime"
	case "Weather":
		w, ok := JSONInt(c.Get("WeatherId"))
		if !ok {
			return true
		}
		value = w == WeatherID
	default:
		return true
	}
	if cmp, ok := Str(c.Get("Compare")); ok && cmp == "Ne" {
		return !value
	}
	return value
}

// InHours is [start, end) in game hours, wrapping past midnight; an empty range is always.
func InHours(start, end int32) bool {
	switch {
	case start == end:
		return true
	case start < end:
		return GameHour >= start && GameHour < end
	}
	return GameHour >= start || GameHour < end
}
