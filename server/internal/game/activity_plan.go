// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"slices"
	"strconv"

	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// R-160: the activity plan, the automatic rules (newest edition of each type, R-52 cycles) with
// the content panel's overrides on top (specs/panel.md §2). Every place that picks a listed
// activity asks the plan.

// ActivitySlot is one row of the plan: an activity type, double rewards one per sub type.
type ActivitySlot struct {
	Key     string // "2"; double rewards "7.2"
	Type    int32
	SubType int32
	// Supported: the server fills the type's data; other slots are listed for the panel only.
	Supported bool
	// Editions are the activity ids the slot can run, newest first; Auto is the automatic one.
	Editions []int32
	Auto     int32

	Override   panel.Activity
	Overridden bool

	// Edition is the one in effect (0: none). Begin/End its window (0/0: permanent),
	// AutoBegin/AutoEnd the automatic window; ExplicitWindow when the panel set it.
	Edition            int32
	Begin, End         int64
	AutoBegin, AutoEnd int64
	ExplicitWindow     bool
	// Open: listed now (on, an edition, now inside the window).
	Open bool
}

// ActivityPlan is the plan at one moment.
type ActivityPlan struct {
	Slots []*ActivitySlot
	byKey map[string]*ActivitySlot
}

// Slot is the slot of key (nil: none).
func (p *ActivityPlan) Slot(key string) *ActivitySlot { return p.byKey[key] }

// Listed is the slot's edition when it is open (0, false: not listed).
func (p *ActivityPlan) Listed(key string) (int32, bool) {
	if s := p.byKey[key]; s != nil && s.Open {
		return s.Edition, true
	}
	return 0, false
}

// ListedSlot is the open slot that runs activity id (nil: not listed).
func (p *ActivityPlan) ListedSlot(id int32) *ActivitySlot {
	for _, s := range p.Slots {
		if s.Open && s.Edition == id {
			return s
		}
	}
	return nil
}

// Slot keys of the supported types.
const (
	SlotSign       = "2"
	SlotTimePoint  = "20"
	SlotTurntable  = "15"
	SlotScratch    = "27"
	SlotDreamLink  = "26"
	SlotBossRush   = "14"
	SlotLordGym    = "34"
	SlotDoubleMain = "7.2"
	SlotDoubleSide = "7.3"
)

// DoubleSlot is the slot key of a double reward sub type.
func DoubleSlot(subType int32) string {
	return strconv.Itoa(DoubleActivityType) + "." + strconv.Itoa(int(subType))
}

// PlanActivities is the plan at now.
func PlanActivities(d *world.WorldData, now int64) *ActivityPlan {
	t := d.Tables
	p := &ActivityPlan{byKey: map[string]*ActivitySlot{}}
	activity := t.ActivityById
	add := func(key string, typ, sub int32, ids []int32) {
		var eds []int32
		for _, id := range ids {
			if !ExcludedActivities[id] && !slices.Contains(eds, id) {
				eds = append(eds, id)
			}
		}
		slices.SortFunc(eds, func(a, b int32) int { return int(b) - int(a) })
		s := &ActivitySlot{Key: key, Type: typ, SubType: sub, Supported: true, Editions: eds}
		if len(eds) > 0 {
			s.Auto = eds[0]
		}
		p.Slots = append(p.Slots, s)
		p.byKey[key] = s
	}
	var ids []int32
	pick := func(keep func(a *data.Activity) bool) []int32 {
		var out []int32
		for _, a := range t.Activity {
			if keep(a) {
				out = append(out, a.Id)
			}
		}
		return out
	}
	add(SlotSign, SignType, 0, pick(func(a *data.Activity) bool {
		_, ok := t.ActivitySignById[a.Id]
		return a.Type == SignType && ok
	}))
	tp := map[int32]bool{}
	for _, r := range t.TimePointRewardActivity {
		tp[r.ActivityId] = true
	}
	add(SlotTimePoint, TimePointRewardType, 0, pick(func(a *data.Activity) bool { return a.Type == TimePointRewardType && tp[a.Id] }))
	add(SlotTurntable, TurntableActivityType, 0, pick(func(a *data.Activity) bool {
		_, ok := t.TurntableInfoById[a.Id]
		return a.Type == TurntableActivityType && ok
	}))
	add(SlotScratch, ScratchActivityType, 0, pick(func(a *data.Activity) bool {
		_, ok := t.ScratchCardActivityReById[a.Id]
		return a.Type == ScratchActivityType && ok
	}))
	ids = nil
	for _, c := range t.RogueWhiteCat {
		ids = append(ids, c.Id)
	}
	add(SlotDreamLink, DreamLinkActivityType, 0, ids)
	ids = nil
	for _, l := range t.BossRushActivity {
		ids = append(ids, l.ActivityId)
	}
	add(SlotBossRush, BossRushActivityType, 0, ids)
	add(SlotLordGym, NewLordGymType, 0, pick(func(a *data.Activity) bool { return a.Type == NewLordGymType }))
	for _, sub := range []int32{2, 3} {
		ids = nil
		for _, r := range t.DoubleRewardActivity {
			if r.Type == sub {
				ids = append(ids, r.Id)
			}
		}
		add(DoubleSlot(sub), DoubleActivityType, sub, ids)
	}

	// The types the server fills no data for: listed for the panel only.
	other := map[int32][]int32{}
	for _, a := range t.Activity {
		if ExcludedActivities[a.Id] || supportedType(a.Type) {
			continue
		}
		other[a.Type] = append(other[a.Type], a.Id)
	}
	var types []int32
	for typ := range other {
		types = append(types, typ)
	}
	slices.Sort(types)
	for _, typ := range types {
		eds := other[typ]
		slices.SortFunc(eds, func(a, b int32) int { return int(b) - int(a) })
		s := &ActivitySlot{Key: strconv.Itoa(int(typ)), Type: typ, Editions: eds}
		p.Slots = append(p.Slots, s)
		p.byKey[s.Key] = s
	}

	for _, s := range p.Slots {
		if !s.Supported {
			continue
		}
		s.Edition = s.Auto
		if o, ok := d.Panel.Activity(s.Key); ok && (o.Edition == 0 || slices.Contains(s.Editions, o.Edition)) {
			s.Override, s.Overridden = o, true
			if o.Edition != 0 {
				s.Edition = o.Edition
			}
		}
		if s.Edition == 0 {
			continue
		}
		openType := int32(0)
		if a, ok := activity[s.Edition]; ok {
			openType = a.OpenType
		}
		s.AutoBegin, s.AutoEnd = autoWindow(s.Type, openType, now)
		s.Begin, s.End = s.AutoBegin, s.AutoEnd
		if s.Overridden && s.Override.Window() {
			s.Begin, s.End, s.ExplicitWindow = s.Override.Begin, s.Override.End, true
		}
		s.Open = !(s.Overridden && s.Override.Off) && (!s.ExplicitWindow || s.Begin <= now && now < s.End)
	}
	return p
}

func supportedType(typ int32) bool {
	switch typ {
	case SignType, TimePointRewardType, TurntableActivityType, ScratchActivityType, DreamLinkActivityType,
		BossRushActivityType, NewLordGymType, DoubleActivityType:
		return true
	}
	return false
}

// autoWindow is R-52's window: permanent activities 0/0 (the client reads it as always open),
// limited ones the cycle of their real length holding now (双倍 7 days, others 21).
func autoWindow(typ, openType int32, now int64) (int64, int64) {
	if openType == int32(protocol.ActivityOpenType_ActivityOpenType_Permanent) ||
		openType == int32(protocol.ActivityOpenType_ActivityOpenType_LimitToPermanent) {
		return 0, 0
	}
	days := ActivityDays
	if typ == DoubleActivityType {
		days = DoubleDays
	}
	return Cycle(now, days)
}

// Plan is the session's activity plan now.
func (s *PlayerSession) Plan() *ActivityPlan { return PlanActivities(s.Data, s.Clock.Seconds()) }
