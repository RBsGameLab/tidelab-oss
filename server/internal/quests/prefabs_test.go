// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"slices"
	"testing"

	"tidelab/internal/data"
)

func prefabGroupsOf(defs ...PrefabDef) *PrefabGroups {
	m := make(map[int32]PrefabDef, len(defs))
	order := make([]int32, len(defs))
	for i, d := range defs {
		m[d.ID] = d
		order[i] = d.ID
	}
	return NewPrefabGroups(m, order, nil)
}

func TestBlueprintTowerVersionIsPrefabEntityMarkedTower(t *testing.T) {
	a := PrefabDef{
		ID: 210750008,
		Entities: []PrefabEntity{
			{ID: 1750000, BlueprintType: "Gameplay101"},
			{ID: 1750002, BlueprintType: "Monster104"},
		},
	}
	b := PrefabDef{
		ID: 405850005,
		Entities: []PrefabEntity{
			{ID: 1850002, BlueprintType: "Monster104", Tower: true},
		},
	}
	if _, ok := TowerEntityIn(a, "Monster104"); ok {
		t.Fatal("unmarked entity should not be tower version")
	}
	got, ok := TowerEntityIn(b, "Monster104")
	want := TowerEntity{PrefabID: 405850005, EntityID: 1850002, PropertyID: 0}
	if !ok || got != want {
		t.Fatalf("TowerEntityIn = (%+v, %v), want (%+v, true)", got, ok, want)
	}
	pg := prefabGroupsOf(a, b)
	if got, ok = pg.TowerEntityOf("Monster104"); !ok || got != want {
		t.Fatalf("TowerEntityOf = (%+v, %v), want (%+v, true)", got, ok, want)
	}
	if _, ok = pg.TowerEntityOf("Monster071"); ok {
		t.Fatal("Monster071 has no tower prefab")
	}
}

func TestPrefabGroupsCreateSpawnKillCleared(t *testing.T) {
	p := prefabGroupsOf()
	run := LevelPlayRun(200000005)
	p.Create(run, "Monster", 610000003, 610000074)
	if _, ok := p.GroupCleared(run, "Monster"); ok {
		t.Fatal("queued but unspawned group should return ok=false")
	}
	g := p.Queue()[len(p.Queue())-1].Group
	g.Spawned = true
	g.Alive = append(g.Alive, 101, 102)
	if cleared, ok := p.GroupCleared(run, "Monster"); !ok || cleared {
		t.Fatalf("GroupCleared = (%v, %v), want (false, true)", cleared, ok)
	}
	if _, _, ok := p.OnKilled(101); ok {
		t.Fatal("one monster still alive")
	}
	r, v, ok := p.OnKilled(102)
	if !ok || r != run || v != "Monster" {
		t.Fatalf("OnKilled(102) = (%d, %q, %v), want (%d, Monster, true)", r, v, ok, run)
	}
	if cleared, ok := p.GroupCleared(run, "Monster"); !ok || !cleared {
		t.Fatalf("GroupCleared = (%v, %v), want (true, true)", cleared, ok)
	}
	p.DropRun(run)
	if _, ok := p.GroupCleared(run, "Monster"); ok {
		t.Fatal("dropped group should not exist")
	}
	if len(p.Queue()) != 0 {
		t.Fatalf("Queue should be empty, got %d", len(p.Queue()))
	}
}

func TestPrefabReusedNetIDCountsForNewestGroup(t *testing.T) {
	p := prefabGroupsOf()
	oldRun, newRun := LevelPlayRun(210000004), LevelPlayRun(210000005)
	gOld := p.Create(oldRun, "Monster1", 210000004, 131000000)
	gOld.Spawned = true
	gOld.Alive = append(gOld.Alive, 301, 302)
	gNew := p.Create(newRun, "Monster1", 210000005, 131000000)
	gNew.Spawned = true
	gNew.Alive = append(gNew.Alive, 301)

	r, _, ok := p.OnKilled(301)
	if !ok || r != newRun {
		t.Fatalf("OnKilled(301) = (%d, %v), want newRun %d", r, ok, newRun)
	}
	if len(gOld.Alive) != 2 {
		t.Fatalf("old group should stay untouched, got %v", gOld.Alive)
	}
}

func TestPrefabWavesSpawnOneAfterAnotherClearedAfterLast(t *testing.T) {
	p := prefabGroupsOf()
	run := LevelPlayRun(210000001)
	g := p.CreateWaves(run, "Monster1", 210000001, 131000000, [][]int32{{667000000, 667000001}, {660000000}})
	p.TakeQueue()
	g.Spawned = true
	g.Alive = append(g.Alive, 201, 203)
	if _, _, ok := p.OnKilled(201); ok || len(p.Queue()) != 0 {
		t.Fatal("one monster of wave 1 still alive")
	}
	if _, _, ok := p.OnKilled(203); ok {
		t.Fatal("wave 1 dead should queue wave 2, not clear")
	}
	if _, ok := p.GroupCleared(run, "Monster1"); ok {
		t.Fatal("wave 2 not spawned yet")
	}
	if len(p.Queue()) != 1 || p.Queue()[0].PosEntity != 131000000 {
		t.Fatalf("unexpected queue: %+v", p.Queue())
	}
	if !slices.Equal(g.Waves[g.Wave], []int32{660000000}) {
		t.Fatalf("unexpected wave 2: %v", g.Waves[g.Wave])
	}
	p.TakeQueue()
	g.Spawned = true
	g.Alive = append(g.Alive, 202)
	if cleared, ok := p.GroupCleared(run, "Monster1"); !ok || cleared {
		t.Fatalf("wave 2 alive: got (%v, %v)", cleared, ok)
	}
	if r, _, ok := p.OnKilled(202); !ok || r != run {
		t.Fatalf("final wave kill = (%d, %v), want %d", r, ok, run)
	}
	if cleared, ok := p.GroupCleared(run, "Monster1"); !ok || !cleared {
		t.Fatalf("expected cleared, got (%v, %v)", cleared, ok)
	}
}

func TestPrefabFloorPlayAndFloorPrefab(t *testing.T) {
	defs := map[int32]PrefabDef{
		900: {ID: 900, Entities: []PrefabEntity{{ID: 1}}, Waves: [][]int32{{1}}},
		901: {ID: 901, Entities: []PrefabEntity{{ID: 2}}},
	}
	floors := []FloorPrefab{
		{Floor: 1, Prefab: 900, Play: 5001},
		{Floor: 2, Prefab: 901, Play: 0},
	}
	pg := NewPrefabGroups(defs, nil, floors)
	if lp, ok := pg.FloorPlay(1); !ok || lp != 5001 {
		t.Fatalf("FloorPlay(1) = (%d, %v)", lp, ok)
	}
	if _, ok := pg.FloorPlay(2); ok {
		t.Fatal("FloorPlay(2) should be false")
	}
	if pf, ok := pg.FloorPrefabOf(1); !ok || pf != 900 {
		t.Fatalf("FloorPrefabOf(1) = (%d, %v)", pf, ok)
	}
	if _, ok := pg.FloorPrefabOf(2); ok {
		t.Fatal("FloorPrefabOf(2) has no waves")
	}
	g := pg.CreateFloor(LevelPlayRun(5001), "Floor", 900, 10)
	if g == nil || !g.EntityWaves || len(g.Waves) != 1 {
		t.Fatalf("CreateFloor = %+v", g)
	}
	if e, ok := PrefabEntityIn(defs[900], 1); !ok || e.ID != 1 {
		t.Fatalf("PrefabEntityIn = (%+v, %v)", e, ok)
	}
}

// -----------------------------------------------------------------------------
// Spawner waves (R-4f)

func TestSpawnerWavesFollowPreconditionsDoneWhenAllDead(t *testing.T) {
	s := NewSpawnerJobs()
	waves := []SpawnWave{
		{ID: 0, TargetsToAwake: []int64{11, 12}},
		{ID: 1, PreConditionIDs: []int32{0}, TargetsToAwake: []int64{21}},
	}
	var wake []int64
	key := SpawnerKey{Run: 7, Node: 3}
	if s.Start(key, [][]SpawnWave{waves}, 0, &wake) {
		t.Fatal("should not be finished immediately")
	}
	if !slices.Equal(wake, []int64{11, 12}) {
		t.Fatalf("wake = %v, want [11 12]", wake)
	}
	wake = wake[:0]
	if _, ok := s.OnKilled(11, 10, &wake); ok || len(wake) != 0 {
		t.Fatal("killing 11 alone should not advance wave")
	}
	if _, ok := s.OnKilled(99, 10, &wake); ok {
		t.Fatal("unrelated kill should do nothing")
	}
	if _, ok := s.OnKilled(12, 20, &wake); ok {
		t.Fatal("wave 1 should start, not finish job")
	}
	if !slices.Equal(wake, []int64{21}) {
		t.Fatalf("wake = %v, want [21]", wake)
	}
	if k, ok := s.OnKilled(21, 30, &wake); !ok || k != key {
		t.Fatalf("OnKilled(21) = (%+v, %v), want %+v", k, ok, key)
	}
	if s.Has(key) {
		t.Fatal("finished job should be dropped")
	}
}

func TestQuantityRefillKeepsQuantityOutAndRefillsOnKills(t *testing.T) {
	s := NewSpawnerJobs()
	q := int32(3)
	waves := []SpawnWave{{ID: 0, TargetsToAwake: []int64{1, 2, 3, 4}, CompleteType: 2, Quantity: &q}}
	var wake []int64
	if s.Start(SpawnerKey{Run: 1, Node: 0}, [][]SpawnWave{waves}, 0, &wake) {
		t.Fatal("should not be finished")
	}
	if !slices.Equal(wake, []int64{1, 2, 3}) {
		t.Fatalf("wake = %v, want [1 2 3]", wake)
	}
	wake = wake[:0]
	if _, ok := s.OnKilled(2, 1, &wake); ok {
		t.Fatal("should not be finished")
	}
	if !slices.Equal(wake, []int64{4}) {
		t.Fatalf("wake = %v, want [4]", wake)
	}
	s.OnKilled(1, 2, &wake)
	s.OnKilled(3, 3, &wake)
	if _, ok := s.OnKilled(4, 4, &wake); !ok {
		t.Fatal("expected finished on kill 4")
	}
}

func TestDelayTimeAndDurationWavesRunOnTick(t *testing.T) {
	s := NewSpawnerJobs()
	dur := 5.0
	waves := []SpawnWave{
		{ID: 0, CompleteType: 1, Duration: &dur},
		{ID: 1, DelayTime: 2, PreConditionIDs: []int32{0}, TargetsToAwake: []int64{8}},
	}
	var wake []int64
	var finished []SpawnerKey
	s.Start(SpawnerKey{Run: 2, Node: 1}, [][]SpawnWave{waves}, 0, &wake)
	if len(wake) != 0 {
		t.Fatalf("unexpected initial wake: %v", wake)
	}
	s.Tick(4_000, &wake, &finished)
	if len(wake) != 0 {
		t.Fatal("should not wake at 4s")
	}
	if d, ok := s.NextDue(); !ok || d != 5_000 {
		t.Fatalf("NextDue = (%d, %v), want 5000", d, ok)
	}
	s.Tick(5_000, &wake, &finished)
	if len(wake) != 0 {
		t.Fatal("wave 1 delayed until 7s")
	}
	if d, ok := s.NextDue(); !ok || d != 7_000 {
		t.Fatalf("NextDue = (%d, %v), want 7000", d, ok)
	}
	s.Tick(7_000, &wake, &finished)
	if !slices.Equal(wake, []int64{8}) {
		t.Fatalf("wake = %v, want [8]", wake)
	}
	if _, ok := s.OnKilled(8, 8_000, &wake); !ok {
		t.Fatal("kill 8 should finish job")
	}
}

func TestMonstersKilledBeforeTheirWaveAreNotWokenAgainAndEmptySpawnerIsDone(t *testing.T) {
	s := NewSpawnerJobs()
	waves := []SpawnWave{
		{ID: 0, TargetsToAwake: []int64{5}},
		{ID: 1, PreConditionIDs: []int32{0}, TargetsToAwake: []int64{6, 7}},
	}
	var wake []int64
	s.Start(SpawnerKey{Run: 3, Node: 2}, [][]SpawnWave{waves}, 0, &wake)
	s.OnKilled(6, 1, &wake)
	wake = wake[:0]
	s.OnKilled(5, 2, &wake)
	if !slices.Equal(wake, []int64{7}) {
		t.Fatalf("wake = %v, want [7]", wake)
	}
	if _, ok := s.OnKilled(7, 3, &wake); !ok {
		t.Fatal("kill 7 should finish job")
	}
	if !s.Start(SpawnerKey{Run: 4, Node: 0}, [][]SpawnWave{{}}, 0, &wake) {
		t.Fatal("empty spawner should finish immediately")
	}
}

func TestSpawnMonsterComponentParses(t *testing.T) {
	v, err := data.ParseJSON([]byte(`{
		"SpawnMonsterConfigs": [{
			"Id": 1,
			"DelayTime": 1.5,
			"PreCondition": {"Type": "DependOnPreceding", "Ids": [0]},
			"TargetsToAwake": [41, 42],
			"CompleteCondition": {"Type": 2, "Quantity": 1, "Duration": 3}
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	waves := ParseSpawnWaves(v)
	if len(waves) != 1 {
		t.Fatalf("len(waves) = %d, want 1", len(waves))
	}
	w := waves[0]
	if w.ID != 1 || w.DelayTime != 1.5 || w.CompleteType != 2 || w.Quantity == nil || *w.Quantity != 1 || w.Duration == nil || *w.Duration != 3.0 {
		t.Fatalf("unexpected wave: %+v", w)
	}
	if !slices.Equal(w.PreConditionIDs, []int32{0}) || !slices.Equal(w.TargetsToAwake, []int64{41, 42}) {
		t.Fatalf("unexpected ids: pre=%v targets=%v", w.PreConditionIDs, w.TargetsToAwake)
	}
}
