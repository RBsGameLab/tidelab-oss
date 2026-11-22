// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package quests implements the behaviour-tree quest and level-play engine and its supporting
// subsystems (conditions, clearings, flow options, prefabs, presentation, spawner waves, quest
// vehicles). Ported from our C# src/Tidelab.GameServer/Quests.
package quests

import (
	"tidelab/internal/data"
	"tidelab/internal/world"
)

// Common gameplay tag names for entity state checks (specs/entities.md §3.4).
const (
	TagActive = world.TagActive
	TagNormal = world.TagNormal
)

// GameplayTagRawID is the raw tag id: 32-bit FNV-1 over the name's UTF-16 code units, read as signed.
func GameplayTagRawID(name string) int32 { return world.RawTagID(name) }

// GameplayTagID is the tag id the client knows: the raw id, remapped through GameplayTagRemap.
func GameplayTagID(tables *data.DataTables, name string) int32 {
	if tables == nil {
		return world.RawTagID(name)
	}
	return world.TagID(tables, name)
}
