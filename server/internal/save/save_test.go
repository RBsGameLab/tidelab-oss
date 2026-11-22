// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package save

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/zon"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWorldStateLaterLinesWinForgetsRemoveAndOnlyRealChangesAreWritten(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "player", "777", "world_state"), "1 5 0 0\n2 3 0 0\n1 7 0 1\n3 1 0 0\n3 -\n")
	files := NewPlayerFiles(dir, 777)
	s := NewWorldState(files)
	if tag, ok := s.StateOverride(1); !ok || tag != 7 {
		t.Fatal(tag)
	}
	if !s.IsAwake(1) {
		t.Fatal("1 asleep")
	}
	if _, ok := s.Get(3); ok {
		t.Fatal("3 kept")
	}
	if s.LinesOnDisk() != 5 {
		t.Fatal(s.LinesOnDisk())
	}
	s.SetState(2, 3)
	if s.PendingCount() != 0 {
		t.Fatal(s.PendingCount())
	}
	s.SetState(2, 4)
	s.Sleep(1)
	s.Save()
	if text := readFile(t, files.PathOf("world_state")); !strings.HasSuffix(text, fmt.Sprintf("2 4 0 0\n1 7 %d 0\n", int64(math.MaxInt64))) {
		t.Fatal(text)
	}
	s.Wake(1)
	if e, _ := s.Get(1); e.GoneUntil != 0 {
		t.Fatal(e)
	}
	if s.IsGone(2, 0) {
		t.Fatal("2 gone")
	}
	if s.HistoryLimit() != 64 {
		t.Fatal(s.HistoryLimit())
	}
}

func TestWorldStateSnapshotKeepsTheDictionaryOrder(t *testing.T) {
	dir := t.TempDir()
	files := NewPlayerFiles(dir, 1)
	s := NewWorldState(files)
	var text strings.Builder
	for id := 1; id <= 20; id++ {
		fmt.Fprintf(&text, "%d 1 0 0\n", id)
	}
	text.WriteString(strings.Repeat("1 1 0 0\n", 59))
	s.Apply(text.String()) // 79 lines of history for 20 entities (limit 80)
	s.Forget(3)
	s.SetState(100, 2) // takes 3's slot
	s.Save()           // 79 + 2 > 80: a snapshot
	lines := Lines(readFile(t, files.PathOf("world_state")))
	if len(lines) != 20 || lines[2] != "100 2 0 0" || s.LinesOnDisk() != 20 {
		t.Fatal(lines)
	}
}

func TestEntityMovesParseAndOptionalYaw(t *testing.T) {
	m := NewEntityMoves(nil)
	m.Parse("936800404 9681.4 -50690.0 866.3 -90.0\n114001558 704044.0 570580.0 1317.0 n\nbad line\n")
	if p, _ := m.Get(936800404); p.Yaw == nil || *p.Yaw != -90 {
		t.Fatal(p)
	}
	if p, _ := m.Get(114001558); p.Yaw != nil {
		t.Fatal(p)
	}
	if len(m.Moved()) != 2 {
		t.Fatal(m.Moved())
	}
}

func TestAreaStatesFormatAndParse(t *testing.T) {
	a := NewAreaStates(nil)
	if !a.Set(56, false) || a.Set(56, false) || !a.Set(12001, true) {
		t.Fatal("set")
	}
	if a.Format() != "56 0\n12001 1\n" {
		t.Fatal(a.Format())
	}
	b := NewAreaStates(nil)
	b.Parse("56 0\r\n12001 1\r\n")
	if !slices.Equal(b.All(), []AreaState{{56, false}, {12001, true}}) {
		t.Fatal(b.All())
	}
}

func TestMovedEntitiesFormatParseAndReset(t *testing.T) {
	m := NewEntityMoves(nil)
	yaw := float32(-90)
	m.Move(936800404, 9681.4, -50690, 866.3, &yaw)
	m.Move(114001558, 704044, 570580, 1317, nil)
	text := m.Format()
	if text != "936800404 9681.4 -50690.0 866.3 -90.0\n114001558 704044.0 570580.0 1317.0 n\n" {
		t.Fatal(text)
	}
	again := NewEntityMoves(nil)
	again.Parse(text)
	if p, _ := again.Get(936800404); *p.Yaw != -90 {
		t.Fatal(p)
	}
	if !again.Reset(114001558) || again.Reset(114001558) {
		t.Fatal("reset")
	}
	if _, ok := again.Get(114001558); ok {
		t.Fatal("still moved")
	}
}

func TestNormalInventorySnapshotLogAppendsDeletesAndCompactsAtThreshold(t *testing.T) {
	playerDir := filepath.Join(t.TempDir(), "player", "777")
	store := NewNormalInventoryStore(playerDir)
	store.SetBatch([]ItemCount{{41100011, 10}, {43020001, 5}})
	store.CompactSnapshot()
	logPath := filepath.Join(playerDir, "inventory_info.log")
	if _, err := os.Stat(logPath); err == nil {
		t.Fatal("log after compact")
	}
	store.Add(41100011, 5)
	store.SetBatch([]ItemCount{{43020001, 0}})
	if store.LogLines() != 2 {
		t.Fatal(store.LogLines())
	}
	reloaded := NewNormalInventoryStore(playerDir)
	reloaded.Load()
	if reloaded.CountOf(41100011) != 15 || reloaded.CountOf(43020001) != 0 {
		t.Fatal(reloaded.CountOf(41100011))
	}
	for range 62 {
		reloaded.Add(41100011, 1)
	}
	if reloaded.LogLines() != 64 {
		t.Fatal(reloaded.LogLines())
	}
	reloaded.Add(41100011, 1)
	if _, err := os.Stat(logPath); err == nil || reloaded.LogLines() != 0 {
		t.Fatal("no compaction")
	}
	again := NewNormalInventoryStore(playerDir)
	again.Load()
	if again.CountOf(41100011) != 78 {
		t.Fatal(again.CountOf(41100011))
	}
	if text := readFile(t, filepath.Join(playerDir, "inventory_info")); text != ".{\n    .normal_items = .{\n        .{\n            .id = 41100011,\n            .count = 78,\n        },\n    },\n}\n" {
		t.Fatal(text)
	}
}

func TestDirtyQueueIsDueThirtySecondsAfterTheFirstChange(t *testing.T) {
	var q DirtySaveQueue
	q.MarkRole(1, 1000)
	q.MarkWeapon(5, 20_000)
	if q.IsDue(30_999) || !q.IsDue(31_000) {
		t.Fatal("due")
	}
	roles, weapons := q.Drain()
	if !slices.Equal(roles, []int32{1}) || !slices.Equal(weapons, []int32{5}) || q.HasPending() {
		t.Fatal(roles, weapons)
	}
}

// A role file read and written again keeps the fields tidelab does not know, after its own.
func TestRoleRoundTripKeepsUnknownFields(t *testing.T) {
	v, err := zon.Parse(".{ .level = 50, .mystery = .{ 1, 2 }, .skills = .{ .{ 1001, 3 } }, .create_time = 4000000000, .weapon = 7 }")
	if err != nil {
		t.Fatal(err)
	}
	r := RoleFromZon(1102, v)
	if r.Level != 50 || r.Weapon != 7 || r.Favor != 5 || r.CreateTime != 4000000000 || r.Skills[0] != (IntPair{1001, 3}) {
		t.Fatal(r)
	}
	out := zon.Write(r.ToZon())
	if !strings.Contains(out, ".create_time = 4000000000,") || !strings.HasSuffix(out, "    .mystery = .{ 1, 2 },\n}\n") {
		t.Fatal(out)
	}
}

func TestPlayerLoadCountsUniqueIDsPastEveryItem(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "player", "777")
	writeFile(t, filepath.Join(pd, "weapon", "12"), ".{ .id = 21010011 }")
	writeFile(t, filepath.Join(pd, "echo", "30"), ".{ .id = 6000038, .role_id = 1102, .equipped_pos = 0 }")
	writeFile(t, filepath.Join(pd, "echo", "presets"), ".{ .groups = .{ .{ .incr_id = 40, .name = \"a\", .slots = .{ 30 } } } }")
	writeFile(t, filepath.Join(pd, "special_item", "next"), "5\n")
	d := LoadPlayer(dir, 777)
	if d.SpecialItemIDs.Peek() != 41 {
		t.Fatal(d.SpecialItemIDs.Peek())
	}
	if !d.WeaponDirExistedOnLoad || d.RoleDirExistedOnLoad || !d.EchoDirHadEntriesOnLoad {
		t.Fatal("flags")
	}
	if e := d.EquippedEchoes(1102); e[0] == nil || e[0].UniqueID != 30 {
		t.Fatal(e)
	}
	if got := d.SpecialItemIDs.Next(); got != 41 || readFile(t, filepath.Join(pd, "special_item", "next")) != "42\n" {
		t.Fatal(got)
	}
}

func TestSceneInstanceKeepsUnknownFieldsOfPlayers(t *testing.T) {
	v, _ := zon.Parse(".{ .owner_id = 7, .players = .{ .{ .id = 7, .extra = true, .location = .{ 1, 2 } } } }")
	f := SceneInstanceFrom(v)
	if f.Hour != 8 || f.Players[0].GroupType != 1 || f.Players[0].Location != [3]float32{1, 2, 0} {
		t.Fatal(f.Players[0])
	}
	f.Players[0].Location[2] = 1.5
	out := zon.Write(f.ToZon(v))
	if !strings.Contains(out, ".extra = true,") || !strings.Contains(out, ".location = .{ 1, 2, 1.5 },\n            .group_type = 1,") || !strings.Contains(out, ".map_time = .{\n        .hour = 8,") {
		t.Fatal(out)
	}
}
