// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"slices"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/protocol"
)

// R-160: the activity plan, the automatic rules with the content panel's overrides
// (specs/panel.md §2).

func panelRig(t *testing.T) (*rig, *PlayerSession, *panel.Overrides) {
	r, s, _ := enterActivityRig(t)
	o := panel.Open(r.state)
	s.Data.Panel = o
	return r, s, o
}

func setOverride(t *testing.T, o *panel.Overrides, key string, a panel.Activity) {
	t.Helper()
	if err := o.SetActivity(key, a, "test "+key); err != nil {
		t.Fatal(err)
	}
}

func pageIDs(s *PlayerSession) []int32 {
	var ids []int32
	for _, a := range s.ActivityPage().Activities {
		ids = append(ids, a.Id)
	}
	return ids
}

func pageEntry(s *PlayerSession, id int32) *protocol.ActivityData {
	for _, a := range s.ActivityPage().Activities {
		if a.Id == id {
			return a
		}
	}
	return nil
}

func TestWithoutOverridesThePlanIsTheAutomaticRules(t *testing.T) {
	_, s, _ := enterActivityRig(t)
	p := s.Plan()
	sign := p.Slot(SlotSign)
	// Newest first; the excluded 100100001 is not an edition.
	eq(t, sign.Editions, []int32{100200002, 100200001})
	if sign.Auto != 100200002 || sign.Edition != 100200002 || !sign.Open || sign.Overridden || sign.ExplicitWindow {
		t.Fatal(sign)
	}
	if sign.End-sign.Begin != ActivityDays*ScheduleDay || sign.Begin != sign.AutoBegin {
		t.Fatal(sign.Begin, sign.End)
	}
	if d := p.Slot(SlotDoubleMain); d.Edition != 100700100 || !d.Open || d.End-d.Begin != DoubleDays*ScheduleDay {
		t.Fatal(d)
	}
	if p.Slot(SlotDoubleSide).Open || p.Slot(SlotScratch).Open {
		t.Fatal("slots without editions are not open")
	}
}

func TestTypesTheServerFillsNoDataForAreListedButNeverOpen(t *testing.T) {
	r, s, _ := enterActivityRig(t)
	r.tables.Activity = append(r.tables.Activity, &data.Activity{Id: 106800001, Type: 68}, &data.Activity{Id: 106800002, Type: 68})
	r.tables.Reindex()
	slot := s.Plan().Slot("68")
	if slot == nil || slot.Supported || slot.Open || slot.Edition != 0 {
		t.Fatal(slot)
	}
	eq(t, slot.Editions, []int32{106800002, 106800001})
}

func TestThePanelPicksTheEditionOfAType(t *testing.T) {
	_, s, o := panelRig(t)
	setOverride(t, o, SlotSign, panel.Activity{Edition: 100200001})
	ids := pageIDs(s)
	if !slices.Contains(ids, 100200001) || slices.Contains(ids, 100200002) {
		t.Fatal(ids)
	}
	// A claim of the edition not listed fails.
	if c := s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 0}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrActivityLock {
		t.Fatal(c)
	}
	if c := s.Sign(&protocol.SignActivityRequest{ActivityId: 100200001, Index: 0}).ErrorCode; c != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal(c)
	}
}

func TestAnEditionThatIsNotOneOfTheTypesIsIgnored(t *testing.T) {
	_, s, o := panelRig(t)
	setOverride(t, o, SlotSign, panel.Activity{Edition: 100100001}) // excluded
	if slot := s.Plan().Slot(SlotSign); slot.Edition != 100200002 || slot.Overridden {
		t.Fatal(slot)
	}
}

func TestATypeTurnedOffLeavesThePageAndItsClaimsFail(t *testing.T) {
	_, s, o := panelRig(t)
	setOverride(t, o, SlotSign, panel.Activity{Off: true})
	setOverride(t, o, SlotTimePoint, panel.Activity{Off: true})
	eq(t, pageIDs(s), []int32{103400001, 101500009, 100700100})
	if c := s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 0}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrActivityLock {
		t.Fatal(c)
	}
	if c := s.SignFreeDrop(&protocol.SignActivityFreeDropRequest{ActivityId: 100200002}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrActivityLock {
		t.Fatal(c)
	}
	if c := s.TimePointReward(&protocol.TimePointActivityRewardRequest{Id: 1}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrActivityLock {
		t.Fatal(c)
	}
	if c := s.RunTurntable(&protocol.RunTurntableActivityRequest{ActivityId: 101500009}).ErrorCode; c == protocol.ErrorCode_ErrorCode_ErrTurntableActivityNotOpen {
		t.Fatal("the turntable is still on")
	}
	setOverride(t, o, SlotTurntable, panel.Activity{Off: true})
	if c := s.RunTurntable(&protocol.RunTurntableActivityRequest{ActivityId: 101500009}).ErrorCode; c != protocol.ErrorCode_ErrorCode_ErrTurntableActivityNotOpen {
		t.Fatal(c)
	}
	if err := o.ResetActivity(SlotSign, "reset"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(pageIDs(s), 100200002) {
		t.Fatal("reset to automatic lists it again")
	}
}

func TestThePanelsWindowIsSentAndOutsideItTheActivityIsNotListed(t *testing.T) {
	r, s, o := panelRig(t)
	now := s.Clock.Seconds()
	setOverride(t, o, SlotSign, panel.Activity{Begin: now - 3600, End: now + 3600})
	e := pageEntry(s, 100200002)
	if e == nil || e.BeginOpenTime != now-3600 || e.EndOpenTime != now+3600 || e.BeginShowTime != now-3600 || e.LimitEndTime != now+3600 {
		t.Fatal(e)
	}
	setOverride(t, o, SlotSign, panel.Activity{Begin: now + 60, End: now + 3600})
	if pageEntry(s, 100200002) != nil {
		t.Fatal("not begun yet")
	}
	r.clock.Advance(120 * 1000)
	if pageEntry(s, 100200002) == nil {
		t.Fatal("begun")
	}
	r.clock.Advance(3600 * 1000)
	if pageEntry(s, 100200002) != nil {
		t.Fatal("ended")
	}
}

func signState(s *PlayerSession) protocol.SignState {
	return pageEntry(s, 100200002).GetSignActivity().SignStateList[0]
}

func TestANewWindowIsANewRunAndTheSameWindowKeepsItsProgress(t *testing.T) {
	_, s, o := panelRig(t)
	now := s.Clock.Seconds()
	if c := s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 0}).ErrorCode; c != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal(c)
	}
	auto := s.Plan().Slot(SlotSign)
	// Moving only the end of the running cycle keeps the progress.
	setOverride(t, o, SlotSign, panel.Activity{Begin: auto.AutoBegin, End: auto.AutoEnd + 7*ScheduleDay})
	if st := signState(s); st != protocol.SignState_SignState_IsReceive {
		t.Fatal(st)
	}
	// Another begin: a new run.
	setOverride(t, o, SlotSign, panel.Activity{Begin: now - 60, End: now + 3600})
	if st := signState(s); st != protocol.SignState_SignState_Unlock {
		t.Fatal(st)
	}
	s.Sign(&protocol.SignActivityRequest{ActivityId: 100200002, Index: 0})
	// Another edition and back with the same window: the progress is still there.
	setOverride(t, o, SlotSign, panel.Activity{Edition: 100200001, Begin: now - 60, End: now + 3600})
	pageIDs(s)
	setOverride(t, o, SlotSign, panel.Activity{Begin: now - 60, End: now + 3600})
	if st := signState(s); st != protocol.SignState_SignState_IsReceive {
		t.Fatal(st)
	}
	// Back to automatic and then the same window again: still the same run.
	o.ResetActivity(SlotSign, "reset")
	pageIDs(s)
	setOverride(t, o, SlotSign, panel.Activity{Begin: now - 60, End: now + 3600})
	if st := signState(s); st != protocol.SignState_SignState_IsReceive {
		t.Fatal(st)
	}
}

func TestANewWindowStartsTheTurntableOver(t *testing.T) {
	_, s, o := panelRig(t)
	now := s.Clock.Seconds()
	s.Activities().Turntable.SetState(101500009, TurntableState{Round: 2, AllDone: true})
	setOverride(t, o, SlotTurntable, panel.Activity{Begin: now - 60, End: now + 3600})
	pageIDs(s)
	if st := s.Activities().Turntable.StateOf(s.Data.Tables, 101500009); st.AllDone || st.Round == 2 {
		t.Fatal(st)
	}
}

func TestDoubleRewardsFollowThePanel(t *testing.T) {
	r, s, o := panelRig(t)
	r.tables.Activity = append(r.tables.Activity, &data.Activity{Id: 100700099, Type: DoubleActivityType})
	r.tables.DoubleRewardActivity = append(r.tables.DoubleRewardActivity, &data.DoubleRewardActivity{Id: 100700099, Type: 2, Count: 3, UpRate: 3})
	r.tables.Reindex()
	if got := ClaimDouble(s, []int32{1}); got != 2 {
		t.Fatal(got)
	}
	setOverride(t, o, SlotDoubleMain, panel.Activity{Edition: 100700099})
	if got := ClaimDouble(s, []int32{1}); got != 3 {
		t.Fatal(got)
	}
	setOverride(t, o, SlotDoubleMain, panel.Activity{Off: true})
	if got := ClaimDouble(s, []int32{1}); got != 1 {
		t.Fatal(got)
	}
}

func TestLordGymEntrancesTakeThePanelsWindow(t *testing.T) {
	r, s, o := panelRig(t)
	r.tables.LordGymEntrance = []*data.LordGymEntrance{{Id: 5, LordGymList: []int32{1001}}}
	r.tables.Reindex()
	now := s.Clock.Seconds()
	b, e := s.lordGymWindow()
	if cb, ce := Cycle(now, LordGymDays); b != cb || e != ce {
		t.Fatal(b, e)
	}
	setOverride(t, o, SlotLordGym, panel.Activity{Begin: now - 10, End: now + 10})
	b, e = s.lordGymWindow()
	info := s.Activities().LordGyms.InfoIn(s.Data.Tables, b, e)
	if info.EntranceInfos[0].EffectBeginTime != now-10 || info.EntranceInfos[0].EffectEndTime != now+10 {
		t.Fatal(info.EntranceInfos)
	}
}
