// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
	"tidelab/internal/save"
)

// HideGroupsFileName is the player file holding hidden-group log lines.
const HideGroupsFileName = "hidden_groups"

// HideGroupDef is one group of HideGroups.json: (map, entity) pairs. IDs give way to entities a
// quest woke; Force ones are hidden even when awake.
type HideGroupDef struct {
	Key   string
	IDs   []EntityPair
	Force []EntityPair
}

type HideGroupStats struct {
	Hidden   int
	Entities int
	Shown    int
	Released int
	Removed  int
}

type activeHideGroup struct {
	ids   []int64
	force []int64
}

// HideGroups implements R-69 quest clearings (HideGroup / ShowHidedGroup) and R-82d
// HideSpecificEntities / ShowSpecificEntities. Saved as an append-only log (R-80e) in
// hidden_groups: "+key" hidden, "-key" shown again, bare key counts as hidden.
// Ported from HideGroups.cs.
type HideGroups struct {
	defs        map[string]HideGroupDef
	path        string
	order       []string // insertion order with swap-remove
	groups      map[string]activeHideGroup
	ranged      map[int64]int
	forced      map[int64]int
	savedKeys   omap.Map[string, struct{}]
	linesOnDisk int
	Stats       HideGroupStats
}

// NewHideGroups creates a HideGroups backed by defs and player files (nil = in-memory only for tests).
func NewHideGroups(defs map[string]HideGroupDef, files *save.PlayerFiles) *HideGroups {
	path := ""
	if files != nil {
		path = files.PathOf(HideGroupsFileName)
	}
	return newHideGroupsAt(defs, path)
}

func newHideGroupsAt(defs map[string]HideGroupDef, path string) *HideGroups {
	if defs == nil {
		defs = map[string]HideGroupDef{}
	}
	h := &HideGroups{
		defs:   defs,
		path:   path,
		groups: map[string]activeHideGroup{},
		ranged: map[int64]int{},
		forced: map[int64]int{},
	}
	if path != "" && common.FileExists(path) {
		if b, err := os.ReadFile(path); err == nil {
			h.load(string(b))
		}
	}
	return h
}

func (h *HideGroups) Count() int       { return len(h.groups) }
func (h *HideGroups) LinesOnDisk() int { return h.linesOnDisk }
func (h *HideGroups) Keys() []string   { return h.order }

// PackHideKey combines a map id and 40-bit entity id into one lookup key.
func PackHideKey(mapID int32, entity int64) int64 {
	return (int64(mapID) << 40) | (entity & ((int64(1) << 40) - 1))
}

// IsHidden reports whether the entity is hidden by an active group. Entities a quest woke (awake)
// give way to range groups, while forced groups hide them anyway.
func (h *HideGroups) IsHidden(mapID int32, entity int64, awake bool) bool {
	if len(h.groups) == 0 {
		return false
	}
	k := PackHideKey(mapID, entity)
	if _, ok := h.forced[k]; ok {
		return true
	}
	if awake {
		return false
	}
	_, ok := h.ranged[k]
	return ok
}

func (h *HideGroups) IsActive(key string) bool {
	_, ok := h.groups[key]
	return ok
}

func countPairs(m map[int64]int, pairs []EntityPair) []int64 {
	keys := make([]int64, len(pairs))
	for i, p := range pairs {
		k := PackHideKey(p.Map, p.Entity)
		keys[i] = k
		m[k]++
	}
	return keys
}

func uncountPairs(m map[int64]int, keys []int64) {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if v <= 1 {
			delete(m, k)
		} else {
			m[k] = v - 1
		}
	}
}

// Add activates a group (no-op if already active). Returns how many entities it hides.
func (h *HideGroups) Add(def HideGroupDef) int {
	if _, exists := h.groups[def.Key]; exists {
		return 0
	}
	g := activeHideGroup{
		ids:   countPairs(h.ranged, def.IDs),
		force: countPairs(h.forced, def.Force),
	}
	h.groups[def.Key] = g
	h.order = append(h.order, def.Key)
	return len(g.ids) + len(g.force)
}

// Remove deactivates a group. Returns false if it was not active.
func (h *HideGroups) Remove(key string) bool {
	g, ok := h.groups[key]
	if !ok {
		return false
	}
	delete(h.groups, key)
	if i := slices.Index(h.order, key); i >= 0 {
		last := len(h.order) - 1
		h.order[i] = h.order[last]
		h.order = h.order[:last]
	}
	uncountPairs(h.ranged, g.ids)
	uncountPairs(h.forced, g.force)
	return true
}

// HideGroupOwnerOf returns the owner a key names: 'q' + quest id, or 'l' + level play id.
func HideGroupOwnerOf(key string) (byte, int32, bool) {
	parts := strings.Split(key, "_")
	if len(parts) < 2 || (parts[0] != "q" && parts[0] != "l") {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return 0, 0, false
	}
	return parts[0][0], int32(id), true
}

// Retain deactivates the groups whose owner keep refuses. Returns how many were removed.
func (h *HideGroups) Retain(keep func(kind byte, id int32) bool) int {
	removed := 0
	for i := 0; i < len(h.order); {
		key := h.order[i]
		if kind, id, ok := HideGroupOwnerOf(key); ok && !keep(kind, id) {
			h.Remove(key)
			removed++
		} else {
			i++
		}
	}
	return removed
}

// RemoveOwner deactivates every group of one owner. Returns how many were removed.
func (h *HideGroups) RemoveOwner(kind byte, id int32) int {
	return h.Retain(func(k byte, i int32) bool { return !(k == kind && i == id) })
}

func hideSpecOf(key string) (EntityPair, bool) {
	const marker = "_spec_"
	at := strings.Index(key, marker)
	if at < 0 {
		return EntityPair{}, false
	}
	parts := strings.Split(key[at+len(marker):], "_")
	if len(parts) != 2 {
		return EntityPair{}, false
	}
	m, err1 := strconv.ParseInt(parts[0], 10, 64)
	e, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return EntityPair{}, false
	}
	return EntityPair{Map: int32(m), Entity: e}, true
}

func (h *HideGroups) activate(keys []string) int {
	hidden := 0
	for _, key := range keys {
		if pair, ok := hideSpecOf(key); ok {
			hidden += h.Add(HideGroupDef{Key: key, Force: []EntityPair{pair}})
			continue
		}
		def, ok := h.defs[key]
		if !ok {
			common.Warn("hide_groups", fmt.Sprintf("group %s is not in HideGroups.json", key))
			def = HideGroupDef{Key: key}
		}
		hidden += h.Add(def)
	}
	return hidden
}

func splitNonEmptyLines(text string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\r' || text[i] == '\n' {
			if i > start {
				out = append(out, text[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func (h *HideGroups) load(text string) {
	var keys []string
	for _, line := range splitNonEmptyLines(text) {
		h.linesOnDisk++
		if len(line) > 1 && line[0] == '-' {
			if at := slices.Index(keys, line[1:]); at >= 0 {
				last := len(keys) - 1
				keys[at] = keys[last]
				keys = keys[:last]
			}
			continue
		}
		key := line
		if len(line) > 1 && line[0] == '+' {
			key = line[1:]
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	hidden := h.activate(keys)
	for _, k := range keys {
		h.savedKeys.Set(k, struct{}{})
	}
	common.Info("hide_groups", fmt.Sprintf("%d hidden groups active (%d entities), %d lines on disk",
		len(h.groups), hidden, h.linesOnDisk))
}

// Save appends what changed since the last save (or rewrites the set when the log is long).
func (h *HideGroups) Save() {
	var changes strings.Builder
	changed := 0
	for _, key := range h.order {
		if h.savedKeys.Has(key) {
			continue
		}
		changes.WriteByte('+')
		changes.WriteString(key)
		changes.WriteByte('\n')
		changed++
	}
	for key := range h.savedKeys.All() {
		if _, active := h.groups[key]; active {
			continue
		}
		changes.WriteByte('-')
		changes.WriteString(key)
		changes.WriteByte('\n')
		changed++
	}
	if changed == 0 {
		return
	}
	if h.path != "" {
		limit := 4 * max(len(h.groups), 16)
		if h.linesOnDisk+changed > limit {
			var snap strings.Builder
			for _, k := range h.order {
				snap.WriteByte('+')
				snap.WriteString(k)
				snap.WriteByte('\n')
			}
			if _, err := common.WriteAtomic(h.path, snap.String()); err != nil {
				common.Warn("hide_groups", fmt.Sprintf("saving hidden groups failed: %v", err))
			} else {
				h.linesOnDisk = len(h.groups)
			}
		} else {
			if err := appendTextFile(h.path, changes.String()); err != nil {
				common.Warn("hide_groups", fmt.Sprintf("saving hidden groups failed: %v", err))
			} else {
				h.linesOnDisk += changed
			}
		}
	}
	h.savedKeys.Clear()
	for _, k := range h.order {
		h.savedKeys.Set(k, struct{}{})
	}
}

// Hide activates the group and saves. Returns how many entities it hides (0 if already active).
func (h *HideGroups) Hide(key string) int {
	if _, exists := h.groups[key]; exists {
		return 0
	}
	hidden := h.activate([]string{key})
	h.Stats.Hidden++
	h.Stats.Entities += hidden
	h.Save()
	common.Info("hide_groups", fmt.Sprintf("group %s hidden: %d entities", key, hidden))
	return hidden
}

// Show deactivates the group and saves; its entities spawn again on the next refresh.
func (h *HideGroups) Show(key string) bool {
	if !h.Remove(key) {
		return false
	}
	h.Stats.Shown++
	h.Save()
	common.Info("hide_groups", fmt.Sprintf("group %s shown again", key))
	return true
}

// HideSpecific hides one entity for its owner's run and saves. Returns true if this owner did not
// already hide it.
func (h *HideGroups) HideSpecific(kind byte, owner int32, mapID int32, entity int64) bool {
	key := fmt.Sprintf("%c_%d_spec_%d_%d", kind, owner, mapID, entity)
	if _, exists := h.groups[key]; exists {
		return false
	}
	h.Add(HideGroupDef{Key: key, Force: []EntityPair{{Map: mapID, Entity: entity}}})
	h.Stats.Hidden++
	h.Stats.Entities++
	h.Save()
	return true
}

// ShowSpecific shows one entity whoever hid it, and saves. Returns how many hides ended.
func (h *HideGroups) ShowSpecific(mapID int32, entity int64) int {
	removed := 0
	for i := 0; i < len(h.order); {
		if pair, ok := hideSpecOf(h.order[i]); ok && pair.Map == mapID && pair.Entity == entity {
			h.Remove(h.order[i])
			removed++
		} else {
			i++
		}
	}
	if removed > 0 {
		h.Stats.Shown += removed
		h.Save()
	}
	return removed
}

// Release releases the groups of an ended quest ('q') or level play ('l') and saves.
func (h *HideGroups) Release(kind byte, id int32) {
	n := h.RemoveOwner(kind, id)
	if n == 0 {
		return
	}
	h.Stats.Released += n
	h.Save()
	common.Info("hide_groups", fmt.Sprintf("%c%d ended: %d hidden groups released", kind, id, n))
}
