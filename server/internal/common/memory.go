// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package common

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

// GCPercent is the game server's GOGC (R-147): the heap may grow by half its live size before
// a collection, not by all of it (Go's default 100). The server is idle most of the time, so
// the extra collections cost little; its peak memory drops by about a quarter.
const GCPercent = 50

// gcPercent is the GOGC to use: GCPercent unless the GOGC environment variable says otherwise.
func gcPercent(env string) (int, bool) {
	if env != "" {
		return 0, false
	}
	return GCPercent, true
}

// TuneMemory sets GCPercent unless GOGC is set, and returns what it did, for the log.
func TuneMemory() string {
	env := os.Getenv("GOGC")
	if p, ok := gcPercent(env); ok {
		debug.SetGCPercent(p)
		return fmt.Sprintf("GOGC %d", p)
	}
	return "GOGC " + env + " (from the environment)"
}

// TrimMemory collects and hands the memory no longer used back to the system (R-147): loading
// the tables and quests leaves about as much garbage as data, which Go would otherwise keep
// for minutes. Returns the live heap and what the process still holds, in MB, for the log.
func TrimMemory() (live, held uint64) {
	runtime.GC()
	debug.FreeOSMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc >> 20, (ms.Sys - ms.HeapReleased) >> 20
}
