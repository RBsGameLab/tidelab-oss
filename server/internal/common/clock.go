// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package common

import "time"

// GameClock is the clock game logic reads: timers, spawns, respawns, saves that are due
// (R-129). The server reads the system clocks; the robot switches to a virtual clock it moves
// itself (UseVirtual, Advance), so the same build on the same save runs the same way every time.
// Time measured only for statistics keeps the system clock.
type GameClock struct {
	origin  time.Time
	now     func() time.Time
	virtual bool
	vMs     int64
	vEpochS int64
}

// NewGameClock reads now (time.Now when nil).
func NewGameClock(now func() time.Time) *GameClock {
	if now == nil {
		now = time.Now
	}
	return &GameClock{origin: now(), now: now}
}

func (c *GameClock) IsVirtual() bool { return c.virtual }

// Ms is monotonic milliseconds.
func (c *GameClock) Ms() int64 {
	if c.virtual {
		return c.vMs
	}
	return c.now().Sub(c.origin).Milliseconds()
}

// Seconds is wall-clock Unix seconds.
func (c *GameClock) Seconds() int64 {
	if c.virtual {
		return c.vEpochS + c.vMs/1000
	}
	return c.now().Unix()
}

// RealMs is wall-clock Unix milliseconds.
func (c *GameClock) RealMs() int64 {
	if c.virtual {
		return c.vEpochS*1000 + c.vMs
	}
	return c.now().UnixMilli()
}

// UseVirtual: from now on the logic sees a clock that starts at epochSeconds (wall seconds) and
// moves only by Advance. The monotonic part starts at a large value so that "now minus a
// duration" stays positive.
func (c *GameClock) UseVirtual(epochSeconds int64) {
	c.virtual = true
	c.vMs = 1_000_000_000
	c.vEpochS = epochSeconds - c.vMs/1000
}

// Advance moves the virtual clock (no effect on the system clock).
func (c *GameClock) Advance(deltaMs int64) {
	if c.virtual && deltaMs > 0 {
		c.vMs += deltaMs
	}
}
