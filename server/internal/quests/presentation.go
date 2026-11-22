// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import "slices"

// WeatherSunny is Weather.Id 1 (DA_Weather_Sunny), what quests reset weather back to.
const WeatherSunny int32 = 1

type DisableKind int

const (
	DisableSystem DisableKind = iota
	DisableFunction
)

type Bubble struct {
	Owner  int64
	Entity int64
	Guid   string
}

type Guest struct {
	Owner int64
	ID    int32
}

// Disabled is a system or function a run switched off (re-enabled when the run ends).
type Disabled struct {
	Owner int64
	Kind  DisableKind
	ID    int32
}

type TimeLock struct {
	Owner  int64
	Hour   int64
	Minute int64
}

type Weather struct {
	Owner int64
	ID    int32
}

type PresentationStats struct {
	Audio          int
	Guests         int
	Bubbles        int
	BubblesCleared int
	TimeLocks      int
	SwitchedOff    int
	SwitchedOn     int
	UndoneAtEnd    int
	Weather        int
}

// Leftovers is what a run left turned on when it ended (the caller notifies the client).
type Leftovers struct {
	Bubbles      []Bubble
	Guests       []int32
	Disabled     []Disabled
	UnlockTime   bool
	ClearWeather bool
}

func (l Leftovers) Any() bool {
	return len(l.Bubbles)+len(l.Guests)+len(l.Disabled) > 0 || l.UnlockTime || l.ClearWeather
}

// Presentation tracks quest presentation actions owned by active runs (R-76b-f, R-82b):
// bubbles, guests, disabled systems/functions, time lock, and weather, so they are undone when
// their owning run ends. Not saved on disk. Ported from Presentation.cs.
type Presentation struct {
	bubbles        []Bubble
	guests         []Guest
	disabled       []Disabled
	Time           *TimeLock
	CurrentWeather *Weather
	Stats          PresentationStats
}

func NewPresentation() *Presentation { return &Presentation{} }

func (p *Presentation) Bubbles() []Bubble        { return p.bubbles }
func (p *Presentation) Guests() []Guest          { return p.guests }
func (p *Presentation) DisabledList() []Disabled { return p.disabled }

func swapRemoveSlice[T any](s []T, i int) []T {
	last := len(s) - 1
	s[i] = s[last]
	return s[:last]
}

func (p *Presentation) AddBubble(owner, entity int64, guid string) bool {
	for _, b := range p.bubbles {
		if b.Entity == entity && b.Guid == guid {
			return false
		}
	}
	p.bubbles = append(p.bubbles, Bubble{Owner: owner, Entity: entity, Guid: guid})
	p.Stats.Bubbles++
	return true
}

// ClearBubbles removes every bubble on entities and returns the removed ones.
func (p *Presentation) ClearBubbles(entities []int64) []Bubble {
	var gone []Bubble
	for i := 0; i < len(p.bubbles); {
		if slices.Contains(entities, p.bubbles[i].Entity) {
			gone = append(gone, p.bubbles[i])
			p.bubbles = swapRemoveSlice(p.bubbles, i)
			p.Stats.BubblesCleared++
		} else {
			i++
		}
	}
	return gone
}

func (p *Presentation) AddGuest(owner int64, id int32) bool {
	for _, g := range p.guests {
		if g.ID == id {
			return false
		}
	}
	p.guests = append(p.guests, Guest{Owner: owner, ID: id})
	p.Stats.Guests++
	return true
}

func (p *Presentation) RemoveGuest(id int32) bool {
	for i, g := range p.guests {
		if g.ID == id {
			p.guests = swapRemoveSlice(p.guests, i)
			return true
		}
	}
	return false
}

// SetEnabled implements EnableSystem / EnableFunction: an off switch is remembered for its run;
// an on switch forgets it.
func (p *Presentation) SetEnabled(owner int64, kind DisableKind, id int32, enable bool) {
	at := -1
	for i, d := range p.disabled {
		if d.Kind == kind && d.ID == id {
			at = i
			break
		}
	}
	if at >= 0 {
		if enable {
			p.disabled = swapRemoveSlice(p.disabled, at)
			p.Stats.SwitchedOn++
		}
		return
	}
	if enable {
		p.Stats.SwitchedOn++
		return
	}
	p.disabled = append(p.disabled, Disabled{Owner: owner, Kind: kind, ID: id})
	p.Stats.SwitchedOff++
}

// SetWeather tracks SetWeather: sunny is plain weather, nothing to undo.
func (p *Presentation) SetWeather(owner int64, id int32) {
	if id == WeatherSunny {
		p.CurrentWeather = nil
	} else {
		p.CurrentWeather = &Weather{Owner: owner, ID: id}
	}
	p.Stats.Weather++
}

func (p *Presentation) LockTime(owner, hour, minute int64) {
	p.Time = &TimeLock{Owner: owner, Hour: hour, Minute: minute}
	p.Stats.TimeLocks++
}

func (p *Presentation) Release(owner int64) Leftovers {
	var o Leftovers
	for i := 0; i < len(p.bubbles); {
		if p.bubbles[i].Owner == owner {
			o.Bubbles = append(o.Bubbles, p.bubbles[i])
			p.bubbles = swapRemoveSlice(p.bubbles, i)
		} else {
			i++
		}
	}
	for i := 0; i < len(p.guests); {
		if p.guests[i].Owner == owner {
			o.Guests = append(o.Guests, p.guests[i].ID)
			p.guests = swapRemoveSlice(p.guests, i)
		} else {
			i++
		}
	}
	for i := 0; i < len(p.disabled); {
		if p.disabled[i].Owner == owner {
			o.Disabled = append(o.Disabled, p.disabled[i])
			p.disabled = swapRemoveSlice(p.disabled, i)
		} else {
			i++
		}
	}
	if p.Time != nil && p.Time.Owner == owner {
		p.Time = nil
		o.UnlockTime = true
	}
	if p.CurrentWeather != nil && p.CurrentWeather.Owner == owner {
		p.CurrentWeather = nil
		o.ClearWeather = true
	}
	if o.Any() {
		undone := len(o.Bubbles) + len(o.Guests) + len(o.Disabled)
		if o.UnlockTime {
			undone++
		}
		if o.ClearWeather {
			undone++
		}
		p.Stats.UndoneAtEnd += undone
	}
	return o
}

// Count returns everything still active.
func (p *Presentation) Count() int {
	n := len(p.bubbles) + len(p.guests) + len(p.disabled)
	if p.Time != nil {
		n++
	}
	if p.CurrentWeather != nil {
		n++
	}
	return n
}
