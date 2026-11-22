// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"slices"
	"testing"
)

// The numbers .NET 10's new Random(seed) gives (run once with dotnet).
func TestDotNetRandomMatchesDotNet(t *testing.T) {
	r := NewDotNetRandom(42)
	var got []int32
	for range 6 {
		got = append(got, r.Next(100))
	}
	if !slices.Equal(got, []int32{66, 14, 12, 52, 16, 26}) || r.NextRange(1, 3) != 2 || r.Next(10000) != 5129 {
		t.Fatal(got)
	}
	if NewDotNetRandom(7).Next(5) != 1 || NewDotNetRandom(-3).Next(1000) != 293 || NewDotNetRandom(math.MinInt32).Next(1000) != 726 {
		t.Fatal("seeds")
	}
}

// .NET's PriorityQueue does not keep insertion order among equal priorities.
func TestPriorityQueueOrderAsDotNet(t *testing.T) {
	var q priorityQueue[int]
	for i, p := range []int64{5, 1, 5, 3, 1, 5, 2, 1} {
		q.Enqueue(i, p)
	}
	var got []int
	for q.Len() > 0 {
		got = append(got, q.Dequeue())
	}
	if !slices.Equal(got, pqWant) {
		t.Fatal(got)
	}
}

// What .NET 10 dequeued for the same enqueues (run once with dotnet).
var pqWant = []int{1, 7, 4, 6, 3, 0, 2, 5}
