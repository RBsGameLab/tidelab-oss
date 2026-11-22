// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"

	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
)

// NetIDBlock: ids are handed out from blocks of 256 (specs/entities.md §2).
const NetIDBlock = 256

// NetIds is a scene's net id counter, scene/<instance>/entity/next (specs/entities.md §2,
// save-format.md §4). A block is reserved in the file (counter += 256) before its first id goes
// out, so a crash never repeats an id. A counter made afresh (a scene loaded again) starts a
// new block.
type NetIds struct {
	Path string
	next int64
	end  int64
	file *int64
}

func NewNetIds(path string) *NetIds { return &NetIds{Path: path} }

// Peek is the next id Take would hand out, reserving a block if needed.
func (n *NetIds) Peek() int64 {
	n.reserve()
	return n.next
}

func (n *NetIds) Take() int64 {
	n.reserve()
	id := n.next
	n.next++
	return id
}

// RaiseTo: every id handed out from now on is at least min.
func (n *NetIds) RaiseTo(min int64) {
	if n.next >= min {
		return
	}
	if min < n.end {
		n.next = min
		return
	}
	n.next, n.end = min, min
	f := max(n.fileValue(), min)
	n.file = &f
}

func (n *NetIds) reserve() {
	if n.next < n.end {
		return
	}
	start := max(n.fileValue(), n.next)
	n.next = start
	n.end = start + NetIDBlock
	end := n.end
	n.file = &end
	save.WriteAtomic(n.Path, strconv.FormatInt(n.end, 10)+"\n")
}

func (n *NetIds) fileValue() int64 {
	if n.file != nil {
		return *n.file
	}
	value := int64(1)
	if b, err := os.ReadFile(n.Path); err == nil {
		first := ""
		if w := strings.Fields(strings.TrimPrefix(string(b), "\xef\xbb\xbf")); len(w) > 0 {
			first = w[0]
		}
		v, ok := save.ParseSigned(first, 64)
		if !ok {
			panic(fmt.Errorf("%s does not start with a number: %s", n.Path, first))
		}
		value = v
	}
	n.file = &value
	return value
}

// WorldFloor is the floor for world spawns (specs/entities.md §2): ids of a new process start
// above every earlier process's, so a reconnecting client never meets an id it still holds.
func WorldFloor(nowSeconds int64) int64 { return max((nowSeconds-1_700_000_000)*1000, 1000) }

// SessionStart is how the session started, for the first refresh's clean-up (R-121, spawning.md §6).
type SessionStart int

const (
	StartLogin SessionStart = iota
	StartReconnect
)

// WorldNetIds is world_net_ids (our format, specs/spawning.md §5): whitespace-separated words
// "+id" alive, "-id" gone, "id" alive (a snapshot word), applied in order. A save appends the
// changes, or rewrites a snapshot when the words would pass 4 × max(alive, 64).
type WorldNetIds struct {
	path  string
	alive omap.Map[int64, struct{}]
	words int
}

func NewWorldNetIds(files *save.PlayerFiles) *WorldNetIds {
	w := &WorldNetIds{path: files.PathOf("world_net_ids")}
	if b, err := os.ReadFile(w.path); err == nil {
		w.words = ParseWorldNetIds(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), &w.alive)
	}
	return w
}

// Alive are the ids the file names as alive.
func (w *WorldNetIds) Alive() []int64 { return w.alive.Keys() }
func (w *WorldNetIds) Words() int     { return w.words }

// ParseWorldNetIds applies the words of a world_net_ids text; it returns how many words it held.
func ParseWorldNetIds(text string, alive *omap.Map[int64, struct{}]) int {
	words := 0
	for _, w := range strings.Fields(text) {
		words++
		if len(w) > 1 && w[0] == '-' {
			if gone, ok := save.ParseDigits(w[1:], 64); ok {
				alive.Delete(gone)
			}
			continue
		}
		digits := w
		if len(w) > 1 && w[0] == '+' {
			digits = w[1:]
		}
		if id, ok := save.ParseDigits(digits, 64); ok {
			alive.Set(id, struct{}{})
		}
	}
	return words
}

// OnClient: the ids the client still holds and the scene does not (R-121), only after a reconnect.
func OnClient(start SessionStart, id int64, sceneHolds func(int64) bool) bool {
	return start == StartReconnect && !sceneHolds(id)
}

// Save records now as the alive set; nothing is written when nothing changed.
func (w *WorldNetIds) Save(now []int64) {
	var nowSet omap.Map[int64, struct{}]
	for _, id := range now {
		nowSet.Set(id, struct{}{})
	}
	var text strings.Builder
	added := 0
	for _, id := range now {
		if !w.alive.Has(id) {
			text.WriteString("+" + strconv.FormatInt(id, 10) + " ")
			added++
		}
	}
	for _, id := range w.alive.Keys() {
		if !nowSet.Has(id) {
			text.WriteString("-" + strconv.FormatInt(id, 10) + " ")
			added++
		}
	}
	if added == 0 {
		return
	}
	if w.words+added > 4*max(nowSet.Len(), 64) {
		var snap strings.Builder
		for _, id := range now {
			snap.WriteString(strconv.FormatInt(id, 10) + " ")
		}
		save.WriteAtomic(w.path, snap.String())
		w.words = nowSet.Len()
	} else {
		appendFile(w.path, text.String())
		w.words += added
	}
	w.alive.Clear()
	for id := range nowSet.All() {
		w.alive.Set(id, struct{}{})
	}
}

func appendFile(path, text string) {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		panic(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		panic(err)
	}
}

func dirOf(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return "."
	}
	return path[:i]
}

// FlowClock is a scene's flow time (specs/enter-world.md §4.2): anchored at the real time at
// the scene join with dilation 1; flow time = anchor + (game-clock ms since the anchor) ×
// dilation. A time-stop push re-anchors it with a new dilation.
type FlowClock struct {
	anchorFlowMs  int64
	anchorClockMs int64
	dilation      float64
}

func NewFlowClock(realMs, clockMs int64) *FlowClock { return &FlowClock{realMs, clockMs, 1} }

func (f *FlowClock) Dilation() float64 { return f.dilation }

func (f *FlowClock) Now(clockMs int64) int64 {
	return f.anchorFlowMs + int64(math.Floor(float64(clockMs-f.anchorClockMs)*f.dilation))
}

// SetDilation sets a new dilation from now on; non-finite values are ignored.
func (f *FlowClock) SetDilation(dilation float64, clockMs int64) {
	if math.IsInf(dilation, 0) || math.IsNaN(dilation) {
		return
	}
	f.anchorFlowMs = f.Now(clockMs)
	f.anchorClockMs = clockMs
	f.dilation = dilation
}

// Gameplay tag names (specs/entities.md §3.4).
const (
	TagActive = "关卡.Common.状态.激活"
	TagNormal = "关卡.Common.状态.常态"
)

// RawTagID is 32-bit FNV over the name's UTF-16 code units, read as signed.
func RawTagID(name string) int32 {
	var h uint32
	for _, c := range utf16.Encode([]rune(name)) {
		h = (h ^ uint32(c)) * 16_777_619
	}
	return int32(h)
}

// TagID is the id the client knows: the raw id, remapped through GameplayTagRemap.
func TagID(t *data.DataTables, name string) int32 {
	raw := RawTagID(name)
	if row, ok := t.GameplayTagRemapByRawId[raw]; ok {
		return row.CanonicalId
	}
	return raw
}
