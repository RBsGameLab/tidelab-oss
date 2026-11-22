// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package common

import (
	"runtime"
	"testing"
)

func TestGCPercentUnlessTheEnvironmentSetsIt(t *testing.T) {
	if p, ok := gcPercent(""); !ok || p != 50 {
		t.Fatalf("gcPercent(\"\") = %d, %v; want 50, true", p, ok)
	}
	if _, ok := gcPercent("200"); ok {
		t.Fatal("GOGC=200 in the environment was overridden")
	}
	if _, ok := gcPercent("off"); ok {
		t.Fatal("GOGC=off in the environment was overridden")
	}
}

func TestTrimMemoryHandsGarbageBack(t *testing.T) {
	// 64 MB of garbage, as loading leaves behind.
	for i := 0; i < 64; i++ {
		buf := make([]byte, 1<<20)
		buf[0] = byte(i)
		runtime.KeepAlive(buf)
	}
	live, held := TrimMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapIdle-ms.HeapReleased > 16<<20 {
		t.Fatalf("after TrimMemory %d MB idle heap is still held from the system", (ms.HeapIdle-ms.HeapReleased)>>20)
	}
	if live > held {
		t.Fatalf("live %d MB more than held %d MB", live, held)
	}
}
