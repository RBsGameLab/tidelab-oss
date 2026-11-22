// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package omap is a map that iterates in the order .NET's Dictionary<K,V> does: insertion
// order, except that a removed entry's slot goes on a free list and the next insertion takes
// the slot freed last. The C# server wrote save files and sent lists in that order, so the Go
// server keeps it wherever the order reaches a file or the client.
package omap

import "iter"

type entry[K comparable, V any] struct {
	key  K
	val  V
	used bool
}

// Map is the ordered map. The zero value is empty and ready.
type Map[K comparable, V any] struct {
	entries []entry[K, V]
	index   map[K]int
	free    []int
}

// New is an empty map.
func New[K comparable, V any]() *Map[K, V] { return &Map[K, V]{} }

func (m *Map[K, V]) Len() int { return len(m.index) }

func (m *Map[K, V]) Get(k K) (V, bool) {
	if i, ok := m.index[k]; ok {
		return m.entries[i].val, true
	}
	var zero V
	return zero, false
}

// At is the value at k, or the zero value (C#'s GetValueOrDefault).
func (m *Map[K, V]) At(k K) V {
	v, _ := m.Get(k)
	return v
}

func (m *Map[K, V]) Has(k K) bool {
	_, ok := m.index[k]
	return ok
}

// Set replaces the value in place, or adds the key (into the slot freed last, if any).
func (m *Map[K, V]) Set(k K, v V) {
	if i, ok := m.index[k]; ok {
		m.entries[i].val = v
		return
	}
	if m.index == nil {
		m.index = map[K]int{}
	}
	if n := len(m.free); n > 0 {
		i := m.free[n-1]
		m.free = m.free[:n-1]
		m.entries[i] = entry[K, V]{k, v, true}
		m.index[k] = i
		return
	}
	m.index[k] = len(m.entries)
	m.entries = append(m.entries, entry[K, V]{k, v, true})
}

// Delete removes the key; true if it was there.
func (m *Map[K, V]) Delete(k K) bool {
	i, ok := m.index[k]
	if !ok {
		return false
	}
	delete(m.index, k)
	m.entries[i] = entry[K, V]{}
	m.free = append(m.free, i)
	return true
}

// Take removes the key and returns its value.
func (m *Map[K, V]) Take(k K) (V, bool) {
	v, ok := m.Get(k)
	if ok {
		m.Delete(k)
	}
	return v, ok
}

func (m *Map[K, V]) Clear() {
	m.entries = nil
	m.index = nil
	m.free = nil
}

// All is every key and value in the Dictionary's order.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for i := 0; i < len(m.entries); i++ {
			if e := m.entries[i]; e.used && !yield(e.key, e.val) {
				return
			}
		}
	}
}

func (m *Map[K, V]) Keys() []K {
	out := make([]K, 0, m.Len())
	for k := range m.All() {
		out = append(out, k)
	}
	return out
}

func (m *Map[K, V]) Values() []V {
	out := make([]V, 0, m.Len())
	for _, v := range m.All() {
		out = append(out, v)
	}
	return out
}
