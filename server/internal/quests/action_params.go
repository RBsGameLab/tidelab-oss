// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"math"

	"tidelab/internal/data"
)

// R-33a: reads the params of an action the server runs (QuestAction.Raw) by path, e.g.
// ActionInt(a, "SetAreaTimeConfig", "LockTime", "Hour"). Missing keys and wrong types return
// ok=false / nil: an action with odd params is skipped, not fatal. Ported from ActionParamsReader.cs.

func ActionAt(a QuestAction, path ...string) data.Value {
	return ValueAt(a.Raw, path...)
}

func ValueAt(v data.Value, path ...string) data.Value {
	for _, key := range path {
		o, ok := v.(*data.Object)
		if !ok || o == nil {
			return nil
		}
		v = o.Get(key)
	}
	return v
}

func ActionInt(a QuestAction, path ...string) (int64, bool) {
	n, ok := ActionAt(a, path...).(data.Number)
	if !ok {
		return 0, false
	}
	return int64(n), true
}

func ActionFloat(a QuestAction, path ...string) (float64, bool) {
	n, ok := ActionAt(a, path...).(data.Number)
	if !ok {
		return 0, false
	}
	return float64(n), true
}

func ActionBool(a QuestAction, path ...string) (bool, bool) {
	b, ok := ActionAt(a, path...).(data.Bool)
	if !ok {
		return false, false
	}
	return bool(b), true
}

func ActionString(a QuestAction, path ...string) (string, bool) {
	s, ok := ActionAt(a, path...).(data.String)
	if !ok {
		return "", false
	}
	return string(s), true
}

// ActionInts returns the integers of an array (other values, fractions among them, are left out).
func ActionInts(a QuestAction, path ...string) []int64 {
	arr, ok := ActionAt(a, path...).(*data.Array)
	if !ok || arr == nil {
		return nil
	}
	var out []int64
	for _, it := range arr.Items {
		if n, ok := it.(data.Number); ok && float64(n) == math.Floor(float64(n)) {
			out = append(out, int64(n))
		}
	}
	return out
}

// ActionObjects returns the values of an array, e.g. GetItem's Items or EnableLevelPlay's Configs.
func ActionObjects(a QuestAction, path ...string) []data.Value {
	arr, ok := ActionAt(a, path...).(*data.Array)
	if !ok || arr == nil {
		return nil
	}
	return arr.Items
}

// ObjectField reads a key of one element of ActionObjects.
func ObjectField(obj data.Value, key string) data.Value {
	o, ok := obj.(*data.Object)
	if !ok || o == nil {
		return nil
	}
	return o.Get(key)
}
