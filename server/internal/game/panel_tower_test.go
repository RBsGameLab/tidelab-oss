// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/internal/quests"
	"tidelab/protocol"
)

// R-160c: the content panel over the 深塔 (specs/panel.md §6).

func towerQuestData() *quests.QuestDataSet {
	ds := quests.NewQuestDataSet()
	ds.Prefabs = map[int32]quests.PrefabDef{
		900: {ID: 900, Entities: []quests.PrefabEntity{
			{ID: 1, BlueprintType: "MonA", Name: "TsEntity_小怪_甲1", Pos: [3]float32{10, 0, 0}, PropertyID: 11},
			{ID: 2, BlueprintType: "MonA", Name: "TsEntity_小怪_甲2", Pos: [3]float32{20, 0, 0}, PropertyID: 11},
			{ID: 3, BlueprintType: "MonC", Name: "TsEntity_领主_丙_爬塔专用", Pos: [3]float32{30, 0, 0}, PropertyID: 33},
		}, Waves: [][]int32{{1, 2}, {3}}},
		901: {ID: 901, Entities: []quests.PrefabEntity{
			{ID: 5, BlueprintType: "MonB", Name: "TsEntity_精英_乙", Pos: [3]float32{99, 0, 0}, PropertyID: 22},
		}, Waves: [][]int32{{5}}},
	}
	ds.PrefabOrder = []int32{900, 901}
	ds.TowerFloors = []quests.FloorPrefab{{Floor: 463, Prefab: 900}, {Floor: 300, Prefab: 901}}
	return ds
}

func enterTowerPanelRig(t *testing.T) (*rig, *PlayerSession, *panel.Overrides) {
	r := setupTowerRig(t)
	tb := r.tables
	for i, bp := range []string{"MonA", "MonB", "MonC"} {
		tb.BlueprintConfig = append(tb.BlueprintConfig, &data.BlueprintConfig{Id: int32(100 + i), BlueprintType: bp, EntityLogic: "Monster", EntityType: "Monster"})
		tb.TemplateConfig = append(tb.TemplateConfig, &data.TemplateConfig{Id: int32(200 + i), BlueprintType: bp, Name: bp,
			ComponentsData: parseJSON(t, `{ "BaseInfoComponent": { "Category": { "MainType": "Monster" } } }`)})
	}
	r.rebuild()
	o := panel.Open(r.state)
	r.data.Panel = o
	s, _ := r.enterWithQuests(quests.NewCatalog(towerQuestData()), false)
	return r, s, o
}

func TestTowerMonstersAndWavesForThePanel(t *testing.T) {
	ds := towerQuestData()
	ms := TowerMonsters(ds)
	var names []string
	for _, m := range ms {
		names = append(names, m.Name)
	}
	eq(t, names, []string{"小怪·甲", "精英·乙", "领主·丙"})
	waves := TowerFloorWaves(ds, 463)
	if len(waves) != 2 || waves[0][0] != (TowerWaveGroup{"MonA", "小怪·甲", 2}) || waves[1][0].Blueprint != "MonC" {
		t.Fatal(waves)
	}
	if TowerFloorWaves(ds, 999) != nil {
		t.Fatal("no prefab, no waves")
	}
	if m, ok := TowerMonsterOf(ds, panel.TowerSwap{Prefab: 901, Entity: 5}); !ok || m.Blueprint != "MonB" || m.PropertyID != 22 {
		t.Fatal(m)
	}
	if _, ok := TowerMonsterOf(ds, panel.TowerSwap{Prefab: 901, Entity: 6}); ok {
		t.Fatal("not a candidate")
	}
}

func TestThePanelsSeasonListsItsFloorsAndTheFloorItsDungeonRuns(t *testing.T) {
	_, s, o := enterTowerPanelRig(t)
	floorIDs := func() []int32 {
		var ids []int32
		for _, d := range s.TowerSeasonUpdate().TowerInfo.TowerDifficulties {
			for _, a := range d.TowerAreas {
				for _, f := range a.TowerFloors {
					ids = append(ids, f.TowerConfigId)
				}
			}
		}
		return ids
	}
	eq(t, floorIDs(), []int32{463})
	o.SetTowerSeason(40, "t")
	if info := s.TowerSeasonUpdate().TowerInfo; info.CurrentSeason != 40 || info.DataSeason != 40 {
		t.Fatal(info.CurrentSeason)
	}
	eq(t, floorIDs(), []int32{300})
	if f := s.QuestState.Tower.FloorOfDungeon(s.Data.Tables, 20013); f.Id != 300 {
		t.Fatal(f.Id)
	}
	// A season without floors is the newest.
	o.SetTowerSeason(7, "t")
	eq(t, floorIDs(), []int32{463})
}

type spawned struct {
	prefab, configID int32
	x                float32
}

func spawnedFloorWave(t *testing.T, s *PlayerSession) []spawned {
	t.Helper()
	if s.TowerStart(&protocol.TowerStartRequest{TowerConfigId: 463}).ErrorCode != protocol.ErrorCode_ErrorCode_Success {
		t.Fatal("start")
	}
	s.RunAfterResponse()
	if s.Scene.InstanceID != 20013 {
		t.Fatal(s.Scene.InstanceID)
	}
	before := map[int64]bool{}
	for _, e := range s.Scene.Entities() {
		before[e.ID] = true
	}
	s.QuestState.Prefabs.CreateFloor(1, "floor", 900, 0)
	s.SpawnPrefabs()
	var out []spawned
	for _, e := range s.Scene.Entities() {
		if !before[e.ID] {
			out = append(out, spawned{e.PrefabID, e.ConfigID, e.Pos.X})
		}
	}
	return out
}

func TestAFloorsWaveSpawnsAsItIsWithoutASwap(t *testing.T) {
	_, s, _ := enterTowerPanelRig(t)
	got := spawnedFloorWave(t, s)
	if len(got) != 2 || got[0].prefab != 900 || got[0].configID != 1 || got[1].configID != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestASwappedKindSpawnsTheOtherMonstersTowerEntityAtTheSameSpot(t *testing.T) {
	_, s, o := enterTowerPanelRig(t)
	if err := o.SetTowerSwap(463, 0, "MonA", panel.TowerSwap{Prefab: 901, Entity: 5}, "t"); err != nil {
		t.Fatal(err)
	}
	got := spawnedFloorWave(t, s)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	for _, e := range got {
		if e.prefab != 901 || e.configID != 5 {
			t.Fatalf("%+v", e)
		}
	}
	if got[0].x == 99 || got[1].x == 99 || got[0].x == got[1].x {
		t.Fatalf("at the floor's own spots: %+v", got)
	}
}

func TestASwapToAMonsterThatIsNoCandidateIsIgnored(t *testing.T) {
	_, s, o := enterTowerPanelRig(t)
	o.SetTowerSwap(463, 0, "MonA", panel.TowerSwap{Prefab: 901, Entity: 77}, "t")
	if got := spawnedFloorWave(t, s); len(got) != 2 || got[0].prefab != 900 {
		t.Fatalf("%+v", got)
	}
}
