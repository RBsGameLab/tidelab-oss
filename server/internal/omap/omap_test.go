// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package omap

import (
	"slices"
	"testing"
)

func TestInsertionOrder(t *testing.T) {
	var m Map[int, string]
	for _, k := range []int{5, 1, 9} {
		m.Set(k, "x")
	}
	if got := m.Keys(); !slices.Equal(got, []int{5, 1, 9}) {
		t.Fatal(got)
	}
	m.Set(1, "y") // a replacement keeps its place
	if got := m.Keys(); !slices.Equal(got, []int{5, 1, 9}) || m.At(1) != "y" {
		t.Fatal(got)
	}
}

// .NET's Dictionary puts a new key into the slot freed last.
func TestFreedSlotsReusedLastFirst(t *testing.T) {
	var m Map[int, int]
	for k := range 5 {
		m.Set(k, k)
	}
	m.Delete(1)
	m.Delete(3)
	m.Set(10, 0)
	m.Set(11, 0)
	m.Set(12, 0)
	if got := m.Keys(); !slices.Equal(got, []int{0, 11, 2, 10, 4, 12}) {
		t.Fatal(got)
	}
	if m.Len() != 6 || m.Delete(99) {
		t.Fatal(m.Len())
	}
}

func TestEmptiedMapStillReusesSlots(t *testing.T) {
	var m Map[int, int]
	m.Set(1, 0)
	m.Set(2, 0)
	m.Delete(1)
	m.Delete(2)
	m.Set(3, 0)
	m.Set(4, 0)
	m.Set(5, 0)
	if got := m.Keys(); !slices.Equal(got, []int{4, 3, 5}) {
		t.Fatal(got)
	}
	m.Clear()
	m.Set(6, 0)
	m.Set(7, 0)
	if got := m.Keys(); !slices.Equal(got, []int{6, 7}) {
		t.Fatal(got)
	}
}
