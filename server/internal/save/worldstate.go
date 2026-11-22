// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package save

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"tidelab/internal/omap"
)

// Forever is the "gone until" of an entity a quest destroyed.
const Forever = math.MaxInt64

// WorldEntry is what the player changed about one open-world entity.
type WorldEntry struct {
	StateTagID int32
	GoneUntil  int64
	Awake      bool
}

// WorldState is the open-world entities the player changed (ours): opened chests and doors (a
// new state tag), collected or destroyed ones (gone until a Unix second), sleeping rows a gear
// or quest woke. Saved in world_state as lines "<config id> <state tag> <gone until> <awake 0|1>"
// or "<config id> -" to forget one; a later line wins. A save appends one line per entity
// changed since the last save and rewrites the file as a snapshot when its history passes four
// times the entities it describes (at least 64 lines).
type WorldState struct {
	entries     omap.Map[int64, WorldEntry]
	pending     []int64
	path        string
	linesOnDisk int
	// AwakeVersion counts changes since the set of awake entities was last looked at.
	AwakeVersion int64
}

func NewWorldState(files *PlayerFiles) *WorldState {
	w := &WorldState{path: files.PathOf("world_state")}
	if text, ok := readText(w.path); ok {
		w.Apply(text)
	}
	return w
}

func (w *WorldState) Count() int        { return w.entries.Len() }
func (w *WorldState) PendingCount() int { return len(w.pending) }
func (w *WorldState) LinesOnDisk() int  { return w.linesOnDisk }

func (w *WorldState) Get(id int64) (WorldEntry, bool) { return w.entries.Get(id) }

// StateOverride is the state tag a re-spawned entity starts in, if the player changed it.
func (w *WorldState) StateOverride(id int64) (int32, bool) {
	if e, ok := w.entries.Get(id); ok && e.StateTagID != 0 {
		return e.StateTagID, true
	}
	return 0, false
}

func (w *WorldState) IsGone(id, nowSeconds int64) bool {
	e, ok := w.entries.Get(id)
	return ok && e.GoneUntil > nowSeconds
}

func (w *WorldState) IsAwake(id int64) bool { return w.entries.At(id).Awake }

func (w *WorldState) AwakeIDs() []int64 {
	var out []int64
	for k, e := range w.entries.All() {
		if e.Awake {
			out = append(out, k)
		}
	}
	return out
}

func (w *WorldState) Apply(text string) {
	for _, line := range Lines(text) {
		parts := Words(line)
		if len(parts) == 0 {
			continue
		}
		id, ok := ParseSigned(parts[0], 64)
		if !ok {
			continue
		}
		w.linesOnDisk++
		if len(parts) < 2 {
			continue
		}
		if parts[1] == "-" {
			w.entries.Delete(id)
			continue
		}
		tag, ok := ParseSigned(parts[1], 32)
		if !ok {
			continue
		}
		var gone int64
		if len(parts) > 2 {
			gone, _ = ParseSigned(parts[2], 64)
		}
		awake := len(parts) > 3 && parts[3] == "1"
		w.entries.Set(id, WorldEntry{int32(tag), gone, awake})
	}
	w.AwakeVersion++
}

func (w *WorldState) mark(id int64) {
	if !slices.Contains(w.pending, id) {
		w.pending = append(w.pending, id)
	}
}

func (w *WorldState) SetState(id int64, tag int32) {
	e, ok := w.entries.Get(id)
	if ok && e.StateTagID == tag {
		return
	}
	e.StateTagID = tag
	w.entries.Set(id, e)
	w.mark(id)
}

func (w *WorldState) SetGone(id, until int64) {
	e, ok := w.entries.Get(id)
	if ok && e.GoneUntil == until {
		return
	}
	e.GoneUntil = until
	w.entries.Set(id, e)
	w.mark(id)
}

// Wake (AwakeEntity): spawned from now on, even if a quest destroyed it before.
func (w *WorldState) Wake(id int64) {
	e, ok := w.entries.Get(id)
	if ok && e.Awake && e.GoneUntil != Forever {
		return
	}
	e.Awake = true
	if e.GoneUntil == Forever {
		e.GoneUntil = 0
	}
	w.entries.Set(id, e)
	w.AwakeVersion++
	w.mark(id)
}

// Sleep (DestroyEntity): gone until something wakes it again.
func (w *WorldState) Sleep(id int64) {
	e, ok := w.entries.Get(id)
	if ok && !e.Awake && e.GoneUntil == Forever {
		return
	}
	e.Awake = false
	e.GoneUntil = Forever
	w.entries.Set(id, e)
	w.AwakeVersion++
	w.mark(id)
}

// Forget drops everything recorded for an entity.
func (w *WorldState) Forget(id int64) {
	if w.entries.Delete(id) {
		w.AwakeVersion++
		w.mark(id)
	}
}

func (w *WorldState) HistoryLimit() int { return 4 * max(w.entries.Len(), 16) }

func (w *WorldState) LineFor(id int64) string {
	if e, ok := w.entries.Get(id); ok {
		awake := 0
		if e.Awake {
			awake = 1
		}
		return fmt.Sprintf("%d %d %d %d\n", id, e.StateTagID, e.GoneUntil, awake)
	}
	return fmt.Sprintf("%d -\n", id)
}

// Save appends what changed (nothing changed: no write), or rewrites a snapshot when the
// history is long.
func (w *WorldState) Save() {
	if len(w.pending) == 0 {
		return
	}
	if w.linesOnDisk+len(w.pending) > w.HistoryLimit() {
		var b strings.Builder
		for _, id := range w.entries.Keys() {
			b.WriteString(w.LineFor(id))
		}
		WriteAtomic(w.path, b.String())
		w.linesOnDisk = w.entries.Len()
	} else {
		var b strings.Builder
		for _, id := range w.pending {
			b.WriteString(w.LineFor(id))
		}
		appendText(w.path, b.String())
		w.linesOnDisk += len(w.pending)
	}
	w.pending = w.pending[:0]
}

// Place is where a quest put an entity (scene units); Yaw nil: keep its own.
type Place struct {
	X, Y, Z float32
	Yaw     *float32
}

// EntityMoves are the entities a quest moved (R-82e; ours): SetEntityPos puts one at a new
// place, ResetEntityPos puts it back. Saved in entity_pos as lines
// "<config id> <x> <y> <z> <yaw|n>", one decimal.
type EntityMoves struct {
	order     []int64 // insertion order with swap-remove (the file's line order)
	places    map[int64]Place
	path      string
	MoveCount int
}

func NewEntityMoves(files *PlayerFiles) *EntityMoves {
	m := &EntityMoves{places: map[int64]Place{}}
	if files != nil {
		m.path = files.PathOf("entity_pos")
		if text, ok := readText(m.path); ok {
			m.Parse(text)
		}
	}
	return m
}

func (m *EntityMoves) Moved() []int64 { return m.order }

func (m *EntityMoves) Get(id int64) (Place, bool) {
	p, ok := m.places[id]
	return p, ok
}

// parseFloat reads a float as .NET's float.TryParse(NumberStyles.Float) does.
func parseFloat(s string) (float32, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "_xXpP") {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0, false
	}
	return float32(f), true
}

func (m *EntityMoves) Parse(text string) {
	for _, line := range Lines(text) {
		parts := Words(line)
		if len(parts) < 4 {
			continue
		}
		id, ok := ParseSigned(parts[0], 64)
		if !ok {
			continue
		}
		var p [3]float32
		for i := 0; i < 3 && ok; i++ {
			p[i], ok = parseFloat(parts[1+i])
		}
		if !ok {
			continue
		}
		var yaw *float32
		if len(parts) > 4 {
			if y, ok := parseFloat(parts[4]); ok {
				yaw = &y
			}
		}
		m.put(id, Place{p[0], p[1], p[2], yaw})
	}
}

func (m *EntityMoves) put(id int64, p Place) {
	if _, ok := m.places[id]; !ok {
		m.order = append(m.order, id)
	}
	m.places[id] = p
}

// Move (SetEntityPos).
func (m *EntityMoves) Move(id int64, x, y, z float32, yaw *float32) {
	m.put(id, Place{x, y, z, yaw})
	m.MoveCount++
}

// Reset (ResetEntityPos): back to its configured place. True if it had been moved.
func (m *EntityMoves) Reset(id int64) bool {
	if _, ok := m.places[id]; !ok {
		return false
	}
	delete(m.places, id)
	i := slices.Index(m.order, id)
	m.order[i] = m.order[len(m.order)-1]
	m.order = m.order[:len(m.order)-1]
	return true
}

// F1 formats a float with one decimal, as C#'s ToString("F1").
func F1(f float32) string { return strconv.FormatFloat(float64(f), 'f', 1, 32) }

func (m *EntityMoves) Format() string {
	var b strings.Builder
	for _, id := range m.order {
		p := m.places[id]
		fmt.Fprintf(&b, "%d %s %s %s ", id, F1(p.X), F1(p.Y), F1(p.Z))
		if p.Yaw != nil {
			b.WriteString(F1(*p.Yaw))
		} else {
			b.WriteString("n")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (m *EntityMoves) Save() {
	if m.path != "" {
		WriteAtomic(m.path, m.Format())
	}
}

// AreaState is one area's state as a quest set it.
type AreaState struct {
	AreaID int32
	On     bool
}

// AreaStates are the area states a quest or level play set (R-82c; ours): SetAreaState. They go
// out in SceneInformation.AreaStates at every scene join. Saved in area_states as lines
// "<area id> <0|1>".
type AreaStates struct {
	order   []int32
	states  map[int32]bool
	path    string
	Changes int
}

func NewAreaStates(files *PlayerFiles) *AreaStates {
	a := &AreaStates{states: map[int32]bool{}}
	if files != nil {
		a.path = files.PathOf("area_states")
		if text, ok := readText(a.path); ok {
			a.Parse(text)
		}
	}
	return a
}

func (a *AreaStates) All() []AreaState {
	out := make([]AreaState, len(a.order))
	for i, id := range a.order {
		out[i] = AreaState{id, a.states[id]}
	}
	return out
}

func (a *AreaStates) Parse(text string) {
	for _, line := range Lines(text) {
		parts := Words(line)
		if len(parts) < 2 {
			continue
		}
		id, ok := ParseSigned(parts[0], 32)
		if !ok {
			continue
		}
		if _, ok := a.states[int32(id)]; !ok {
			a.order = append(a.order, int32(id))
		}
		a.states[int32(id)] = parts[1] == "1"
	}
}

// Set records a state; true if it changed.
func (a *AreaStates) Set(areaID int32, on bool) bool {
	old, ok := a.states[areaID]
	if ok && old == on {
		return false
	}
	if !ok {
		a.order = append(a.order, areaID)
	}
	a.states[areaID] = on
	a.Changes++
	return true
}

func (a *AreaStates) Format() string {
	var b strings.Builder
	for _, id := range a.order {
		on := 0
		if a.states[id] {
			on = 1
		}
		fmt.Fprintf(&b, "%d %d\n", id, on)
	}
	return b.String()
}

func (a *AreaStates) Save() {
	if a.path != "" {
		WriteAtomic(a.path, a.Format())
	}
}

// QuestView is what the spawner's visibility conditions ask about quests.
type QuestView interface {
	IsFinished(quest int32) bool
	IsChildDone(quest, child int32) bool
}

// QuestProgress is the quests the player finished, from the "F <quest>" lines of quests (our
// format, specs/save-format.md §6), for the visibility conditions when no quest engine runs. A
// child quest counts as done only when its quest is finished.
type QuestProgress struct{ finished map[int32]bool }

func NewQuestProgress(files *PlayerFiles) *QuestProgress {
	q := &QuestProgress{finished: map[int32]bool{}}
	b, err := os.ReadFile(files.PathOf("quests"))
	if err != nil {
		return q
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		parts := Words(line)
		if len(parts) >= 2 && parts[0] == "F" {
			if n, ok := ParseDigits(parts[1], 32); ok {
				q.finished[int32(n)] = true
			}
		}
	}
	return q
}

func QuestProgressOf(finished ...int32) *QuestProgress {
	q := &QuestProgress{finished: map[int32]bool{}}
	for _, f := range finished {
		q.finished[f] = true
	}
	return q
}

func (q *QuestProgress) IsFinished(quest int32) bool     { return q.finished[quest] }
func (q *QuestProgress) IsChildDone(quest, _ int32) bool { return q.finished[quest] }
