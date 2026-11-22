// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// ActivityWindows are R-160's runs: the begin of the panel-set window each activity's progress
// belongs to. Saved in activity_window: "<activity> <begin>".
type ActivityWindows struct {
	path  string
	begin omap.Map[int32, int64]
}

func NewActivityWindows(files *save.PlayerFiles) *ActivityWindows {
	w := &ActivityWindows{path: files.PathOf("activity_window")}
	for _, f := range readLineFile(w.path) {
		id, ok1 := lineInt(f, 0)
		begin, ok2 := lineLong(f, 1)
		if ok1 && ok2 {
			w.begin.Set(id, begin)
		}
	}
	return w
}

func (w *ActivityWindows) set(id int32, begin int64) {
	w.begin.Set(id, begin)
	var sb strings.Builder
	for k, v := range w.begin.All() {
		fmt.Fprintf(&sb, "%d %d\n", k, v)
	}
	save.WriteAtomic(w.path, sb.String())
}

// SyncActivityWindows is "a new window is a new run" (specs/panel.md §2.3): an open activity in
// a window the panel set starts over when that window is not the one its progress belongs to.
// The first panel window of an activity keeps its progress when it begins where the automatic
// cycle does (the user only moved the end).
func (s *PlayerSession) SyncActivityWindows(p *ActivityPlan) {
	w := s.Activities().Windows
	for _, slot := range p.Slots {
		if !slot.Open || !slot.ExplicitWindow {
			continue
		}
		begin, seen := w.begin.Get(slot.Edition)
		if seen && begin == slot.Begin {
			continue
		}
		if seen || slot.Begin != slot.AutoBegin {
			s.resetActivity(slot)
		}
		w.set(slot.Edition, slot.Begin)
	}
}

// resetActivity forgets the progress of a slot's edition. 新领主挑战's pass records and the
// double rewards' daily counts are not runs and stay.
func (s *PlayerSession) resetActivity(slot *ActivitySlot) {
	a := s.Activities()
	id := slot.Edition
	switch slot.Type {
	case SignType, TimePointRewardType:
		a.Signs.Reset(id)
	case TurntableActivityType:
		a.Turntable.Reset(id)
	case ScratchActivityType:
		a.Scratch.Reset(ScratchRounds(s.Data.Tables, id))
	case DreamLinkActivityType:
		a.DreamLink.Reset()
	case BossRushActivityType:
		a.BossRush.Reset()
	default:
		return
	}
	common.Info("activity", fmt.Sprintf("activity %d: new window from %d, progress starts over", id, slot.Begin))
}

// activityListed: activity id is open in the plan now (claims of an activity not listed fail).
func (s *PlayerSession) activityListed(id int32) bool { return s.Plan().ListedSlot(id) != nil }

// lordGymWindow is the window of 新领主挑战's entrances: the panel's, else R-52's 28-day cycle.
func (s *PlayerSession) lordGymWindow() (int64, int64) {
	if slot := s.Plan().Slot(SlotLordGym); slot != nil && slot.ExplicitWindow {
		return slot.Begin, slot.End
	}
	return Cycle(s.Clock.Seconds(), LordGymDays)
}

var errActivityLock = protocol.ErrorCode_ErrorCode_ErrActivityLock
