// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import "testing"

func TestPresentationWeatherSetByRunIsPutBackWhenItEnds(t *testing.T) {
	p := NewPresentation()
	p.SetWeather(1, 5) // rain
	if p.Release(2).ClearWeather {
		t.Fatal("releasing unrelated owner should not clear weather")
	}
	if !p.Release(1).ClearWeather {
		t.Fatal("releasing owner 1 should clear weather")
	}
	p.SetWeather(1, 5)
	p.SetWeather(1, WeatherSunny) // quest restored sunny itself
	if p.Release(1).Any() {
		t.Fatal("sunny weather leaves nothing to undo")
	}
}

func TestPresentationRunsUndoWhatTheyTurnedOn(t *testing.T) {
	p := NewPresentation()
	if !p.AddBubble(1, 10, "g") {
		t.Fatal("first AddBubble should succeed")
	}
	if p.AddBubble(1, 10, "g") {
		t.Fatal("duplicate AddBubble should return false")
	}
	p.AddBubble(2, 11, "h")
	if !p.AddGuest(1, 1003) {
		t.Fatal("AddGuest 1003 should succeed")
	}
	if p.AddGuest(2, 1003) {
		t.Fatal("duplicate AddGuest 1003 should return false")
	}
	p.SetEnabled(1, DisableSystem, 10015, false)
	p.SetEnabled(1, DisableFunction, 1, false)
	p.SetEnabled(1, DisableFunction, 1, true) // re-enabled by the quest itself
	p.LockTime(1, 8, 30)
	if gone := p.ClearBubbles([]int64{11}); len(gone) != 1 {
		t.Fatalf("ClearBubbles = %d, want 1", len(gone))
	}

	left := p.Release(1)
	if len(left.Bubbles) != 1 || len(left.Guests) != 1 || len(left.Disabled) != 1 || left.Disabled[0].Kind != DisableSystem || !left.UnlockTime {
		t.Fatalf("unexpected leftovers: %+v", left)
	}
	if p.Count() != 0 {
		t.Fatalf("Count = %d, want 0", p.Count())
	}
	if p.Stats.UndoneAtEnd != 4 {
		t.Fatalf("UndoneAtEnd = %d, want 4", p.Stats.UndoneAtEnd)
	}
}
