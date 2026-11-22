// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

func js(t testing.TB, text string) data.Value {
	t.Helper()
	v, err := data.ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func writeFile(t testing.TB, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func kinds(e *Entity) []string {
	var out []string
	for _, c := range e.Components {
		out = append(out, strings.TrimPrefix(fmt.Sprintf("%T", c.Component), "*protocol.EntityComponentPb_"))
	}
	return out
}

// ----- spawner (SpawnerTests) -----

type spawnRig struct {
	t    *testing.T
	dir  string
	rows []*data.LevelEntityConfig
}

func newSpawnRig(t *testing.T) *spawnRig { return &spawnRig{t: t, dir: t.TempDir()} }

func iv(x, y, z float32) *data.IntVector {
	return &data.IntVector{X: int32(x * 100), Y: int32(y * 100), Z: int32(z * 100)}
}

type rowOpt struct {
	y          float32
	sleep      bool
	name       string
	hidden     bool
	components string
}

// row is a row on map 8 at (x, y, 0) in game units.
func (r *spawnRig) row(id int32, blueprint string, x float32, o rowOpt) *data.LevelEntityConfig {
	comps := o.components
	if comps == "" {
		comps = "{}"
	}
	row := &data.LevelEntityConfig{
		MapId: 8, EntityId: id, BlueprintType: blueprint, Name: o.name, InSleep: o.sleep, IsHidden: o.hidden,
		Transform: []*data.IntVector{iv(x, o.y, 0), iv(0, 0, 0), iv(1, 1, 1)}, ComponentsData: js(r.t, comps),
	}
	r.rows = append(r.rows, row)
	return row
}

func (r *spawnRig) data() *WorldData {
	b := func(bp, typ, logic string) *data.BlueprintConfig {
		return &data.BlueprintConfig{BlueprintType: bp, EntityType: typ, EntityLogic: logic}
	}
	tpl := func(bp, extra string) *data.TemplateConfig {
		return &data.TemplateConfig{BlueprintType: bp, ComponentsData: js(r.t, `{"BaseInfoComponent":{}`+extra+`}`)}
	}
	t := &data.DataTables{
		InstanceDungeon: []*data.InstanceDungeon{{Id: 8, MapConfigId: 8, ExitEntities: []int32{70}}},
		BlueprintConfig: []*data.BlueprintConfig{
			b("Teleport001", "Teleporter", "Item"), b("NPC301", "Npc", "Npc"), b("Walker", "PasserbyNpc", "Npc"),
			b("Monster071", "Monster", "Monster"), b("Chair1", "Chair", "Item"), b("Hook1", "HookLockPoint", "Item"),
			b("Door1", "SceneItem", "Item"), b("Entry1", "DungeonEntry", "Item"), b("Box1", "TreasureBox", "Item"),
			b("Prop1", "Prop", "Item"), b("NPC0001", "Npc", "Npc"),
		},
		TemplateConfig: []*data.TemplateConfig{
			tpl("Teleport001", ""), tpl("NPC301", ""), tpl("Walker", ""), tpl("Monster071", ""), tpl("Chair1", ""), tpl("Hook1", ""),
			tpl("Door1", `,"InteractComponent":{"Options":[{"Type":{"Actions":[{"Name":"TeleportDungeon"}]}}]}`),
			tpl("Entry1", ""), tpl("Box1", ""), tpl("Prop1", ""), tpl("NPC0001", ""),
		},
		LevelEntityConfig: slices.Clone(r.rows),
	}
	return NewWorldData(t.Reindex(), nil)
}

func candidatesByID(d *WorldData) map[int32]*Candidate {
	out := map[int32]*Candidate{}
	for _, c := range d.CandidatesOf(8) {
		if _, ok := out[c.EntityID()]; !ok {
			out[c.EntityID()] = c
		}
	}
	return out
}

func TestThePassesClassifyTheRows(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "Teleport001", 0, rowOpt{})
	r.row(2, "NPC0001", 0, rowOpt{})
	r.row(3, "Hook1", 0, rowOpt{sleep: true})
	r.row(4, "Door1", 0, rowOpt{})
	r.row(5, "Prop1", 0, rowOpt{components: `{"InteractComponent":{"Options":[{"Type":{"Actions":[{"Name":"SetPlayerPos"}]}}]}}`})
	r.row(6, "Entry1", 0, rowOpt{name: "水星天教堂世界副本前门入口"})
	r.row(7, "Chair1", 0, rowOpt{})
	r.row(8, "Chair1", 0, rowOpt{sleep: true})
	r.row(9, "NPC301", 0, rowOpt{})
	r.row(10, "Walker", 0, rowOpt{})
	r.row(11, "Monster071", 0, rowOpt{})
	r.row(12, "Monster071", 0, rowOpt{})
	r.row(13, "Monster071", 0, rowOpt{components: `{"BaseInfoComponent":{"HeadStateViewConfig":{"HeadStateViewType":0}}}`})
	r.row(14, "Monster071", 0, rowOpt{components: `{"BaseInfoComponent":{"Category":{"EntityPlotBindingType":1}}}`})
	r.row(15, "NPC301", 0, rowOpt{name: "任务NPC"})
	r.row(16, "NPC301", 0, rowOpt{sleep: true, name: "任务NPC"})
	r.row(17, "Box1", 0, rowOpt{name: "测试宝箱"})
	r.row(18, "Prop1", 0, rowOpt{hidden: true, sleep: true})
	r.row(70, "Prop1", 0, rowOpt{sleep: true})
	c := candidatesByID(r.data())
	all := r.data().CandidatesOf(8)

	type cs struct {
		cls   SpawnClass
		sleep bool
	}
	want := map[int32]cs{1: {ClassBeacon, false}, 3: {ClassItem, true}, 8: {ClassOther, true}, 11: {ClassOther, false}}
	for id, w := range want {
		if (cs{c[id].Class, c[id].Sleeping}) != w {
			t.Fatal(id, c[id].Class, c[id].Sleeping)
		}
	}
	for id, cls := range map[int32]SpawnClass{2: ClassOther, 4: ClassItem, 5: ClassItem, 6: ClassItem, 7: ClassSeat, 9: ClassNpc, 10: ClassNpc} {
		if c[id].Class != cls {
			t.Fatal(id, c[id].Class)
		}
	}
	if !c[12].Sleeping || !c[16].Sleeping {
		t.Fatal("12/16 awake")
	}
	for _, id := range []int32{13, 14, 15, 17, 18} {
		if _, ok := c[id]; ok {
			t.Fatal(id, "taken")
		}
	}
	var ids []int32
	for i, x := range all {
		ids = append(ids, x.EntityID())
		if x.Order != i {
			t.Fatal("order", i)
		}
	}
	if !slices.Equal(ids, []int32{1, 2, 3, 4, 5, 6, 7, 70, 9, 10, 11, 12, 8, 16, 70}) {
		t.Fatal(ids)
	}
}

func TestNpcSpotKeysAreBlueprintAndWholeMetres(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "NPC301", 149.9, rowOpt{y: -50})
	if k := candidatesByID(r.data())[1].SpotKey; k != "NPC301|1|-1|0" {
		t.Fatal(k)
	}
	if Metres(14990) != 1 || Metres(15000) != 2 || Metres(-15000) != -2 {
		t.Fatal("metres")
	}
}

type spawnRun struct {
	spawner *Spawner
	scene   *Scene
	events  *[]string
	world   *PlayerWorld
}

func (r *spawnRig) rig(d *WorldData, start SessionStart, worldState, netIDs string) spawnRun {
	files := save.NewPlayerFiles(r.dir, 777)
	if worldState != "" {
		writeFile(r.t, files.PathOf("world_state"), worldState)
	}
	if netIDs != "" {
		writeFile(r.t, files.PathOf("world_net_ids"), netIDs)
	}
	world := LoadPlayerWorld(files, 8)
	scene := NewScene(8, 8, files, NewFlowClock(0, 0))
	events := &[]string{}
	sp := NewSpawner(d, scene, world, start,
		func(e *Entity) { *events = append(*events, fmt.Sprintf("+%d", e.ConfigID)) },
		func(ids []int64) {
			var s []string
			for _, id := range ids {
				s = append(s, fmt.Sprint(id))
			}
			*events = append(*events, "-"+strings.Join(s, ","))
		}, nil)
	return spawnRun{sp, scene, events, world}
}

func TestARefreshSpawnsWithinTheClassRadiiInCandidateOrder(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "Teleport001", 29000, rowOpt{})
	r.row(2, "Teleport001", 31000, rowOpt{})
	r.row(3, "NPC301", 7900, rowOpt{})
	r.row(4, "NPC301", 8100, rowOpt{y: 1})
	r.row(5, "Chair1", 2400, rowOpt{y: 1})
	r.row(6, "Chair1", 2600, rowOpt{y: 2})
	r.row(7, "Monster071", 100, rowOpt{})
	run := r.rig(r.data(), StartLogin, "", "")
	run.spawner.Refresh(1000, 1_800_000_000)
	if !slices.Equal(*run.events, []string{"+1", "+5", "+3", "+7"}) {
		t.Fatal(*run.events)
	}
	for _, s := range run.spawner.Spawns() {
		if run.scene.Get(s.NetID) == nil || s.NetID < WorldFloor(1_800_000_000) {
			t.Fatal(s.NetID)
		}
	}
}

func TestNpcsAreCappedAndOnlyTheNearestPerSpotShow(t *testing.T) {
	r := newSpawnRig(t)
	for i := range 70 {
		r.row(int32(100+i), "NPC301", float32(1000+i*100), rowOpt{})
	}
	r.row(300, "NPC301", 1000, rowOpt{})
	run := r.rig(r.data(), StartLogin, "", "")
	run.spawner.Refresh(0, 1_800_000_000)
	if len(*run.events) != 20 {
		t.Fatal(len(*run.events))
	}
	for range 5 {
		run.spawner.Refresh(0, 1_800_000_000)
	}
	var ids []int32
	for _, s := range run.spawner.Spawns() {
		ids = append(ids, s.Candidate.EntityID())
	}
	slices.Sort(ids)
	want := make([]int32, 60)
	for i := range want {
		want[i] = int32(100 + i)
	}
	if !slices.Equal(ids, want) {
		t.Fatal(ids)
	}
}

func TestQuestNpcsHaveNoCapAndNeedNotBeAmongTheNearest(t *testing.T) {
	r := newSpawnRig(t)
	for i := range 70 {
		r.row(int32(100+i), "NPC301", float32(1000+i*100), rowOpt{})
	}
	r.row(400, "NPC301", 7900, rowOpt{})
	r.row(401, "NPC301", 7950, rowOpt{})
	run := r.rig(r.data(), StartLogin, "400 0 0 1\n", "")
	run.spawner.Refresh(0, 1_800_000_000)
	if len(*run.events) != 21 {
		t.Fatal(len(*run.events))
	}
	has := func(id int32) bool {
		return slices.ContainsFunc(run.spawner.Spawns(), func(s *WorldSpawn) bool { return s.Candidate.EntityID() == id })
	}
	if !has(400) {
		t.Fatal("no quest npc")
	}
	for range 5 {
		run.spawner.Refresh(0, 1_800_000_000)
	}
	if len(run.spawner.Spawns()) != 60 || !has(400) || has(401) {
		t.Fatal(len(run.spawner.Spawns()))
	}
}

func TestARowWithVisibilityConditionsShowsWhenTheyHoldAndWinsATie(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "NPC301", 500, rowOpt{components: `{"EntityVisibleComponent":{"VisibleConditions":[{"Conditions":[{"Type":"TimePeriod","TimePeriod":"Night"}]}]}}`})
	r.row(2, "NPC301", 600, rowOpt{components: `{"EntityVisibleComponent":{"VisibleConditions":[{"Conditions":[{"Type":"PreQuest","PreQuestId":5}]}]}}`})
	r.row(3, "NPC301", 700, rowOpt{})
	run := r.rig(r.data(), StartLogin, "", "")
	run.spawner.Refresh(0, 1_800_000_000)
	if !slices.Equal(*run.events, []string{"+3"}) {
		t.Fatal(*run.events)
	}
	writeFile(t, filepath.Join(r.dir, "player", "777", "quests"), "F 5\n")
	again := r.rig(r.data(), StartLogin, "", "")
	again.spawner.Refresh(0, 1_800_000_000)
	if !slices.Equal(*again.events, []string{"+2", "+3"}) {
		t.Fatal(*again.events)
	}
}

func TestSleepingRowsSpawnOnlyWhenWokenAndThenReachFar(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "Prop1", 20000, rowOpt{sleep: true})
	r.row(2, "Prop1", 100, rowOpt{sleep: true})
	run := r.rig(r.data(), StartLogin, "1 0 0 1\n", "")
	run.spawner.Refresh(0, 1_800_000_000)
	if !slices.Equal(*run.events, []string{"+1"}) || !run.spawner.Spawns()[0].Woken || run.spawner.Spawns()[0].Class != ClassBeacon {
		t.Fatal(*run.events)
	}
}

func TestGoneEntitiesWaitAndChangedStatesSpawnInTheirState(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "Box1", 100, rowOpt{})
	r.row(2, "Box1", 200, rowOpt{})
	run := r.rig(r.data(), StartLogin, fmt.Sprintf("1 0 %d 0\n2 777 0 0\n", int64(math.MaxInt64)), "")
	run.spawner.Refresh(0, 1_800_000_000)
	if !slices.Equal(*run.events, []string{"+2"}) {
		t.Fatal(*run.events)
	}
	e := run.scene.Get(run.spawner.Spawns()[0].NetID)
	last := e.Components[len(e.Components)-1].Component.(*protocol.EntityComponentPb_StateTagComponentPb)
	if last.StateTagComponentPb.StateTagId != 777 {
		t.Fatal(last)
	}
}

func TestFarSpawnsAreRemovedAndKilledOnesComeBackAfterThreeMinutes(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "Monster071", 100, rowOpt{})
	r.row(2, "NPC301", 200, rowOpt{})
	run := r.rig(r.data(), StartLogin, "", "")
	sp := run.spawner
	sp.Refresh(0, 1_800_000_000)
	if !slices.Equal(*run.events, []string{"+1", "+2"}) {
		t.Fatal(*run.events)
	}
	netOf := func(id int32) int64 {
		n, _ := sp.NetIDOf(int64(id))
		return n
	}
	run.scene.Remove(netOf(1))
	*run.events = nil
	sp.Refresh(1000, 1_800_000_000)
	if len(*run.events) != 0 {
		t.Fatal(*run.events)
	}
	sp.Refresh(1000+RespawnMs, 1_800_000_000)
	if !slices.Equal(*run.events, []string{"+1"}) {
		t.Fatal(*run.events)
	}
	*run.events = nil
	npc := netOf(2)
	run.scene.PlayerPos = Vec3{10300, 0, 0}
	sp.Refresh(200_000, 1_800_000_000)
	if !slices.Equal(*run.events, []string{fmt.Sprintf("-%d", npc)}) || run.scene.Get(npc) != nil {
		t.Fatal(*run.events)
	}
	if len(sp.Spawns()) != 1 || sp.Spawns()[0].Candidate.EntityID() != 1 {
		t.Fatal(sp.Spawns())
	}
}

func TestRefreshesAreDueOnTheFirstMoveAndEvery8m(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "NPC301", 100, rowOpt{})
	run := r.rig(r.data(), StartLogin, "", "")
	sp := run.spawner
	if !sp.DueAfterMove(Zero3) {
		t.Fatal("first")
	}
	sp.Refresh(0, 1_800_000_000)
	if sp.DueAfterMove(Vec3{799, 0, 0}) || !sp.DueAfterMove(Vec3{800, 0, 0}) || *sp.LastRefreshAt != Zero3 {
		t.Fatal("8 m")
	}
}

func TestARowThatCannotBeBuiltIsNotSpawned(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "SceneObj100", 100, rowOpt{})
	run := r.rig(r.data(), StartLogin, "", "")
	if !slices.ContainsFunc(run.spawner.CandidateList(), func(c *Candidate) bool { return c.EntityID() == 1 }) {
		t.Fatal("no candidate")
	}
	run.spawner.Refresh(0, 1_800_000_000)
	run.spawner.Refresh(RetryMs+1, 1_800_000_000)
	if len(*run.events) != 0 {
		t.Fatal(*run.events)
	}
}

func TestALoginForgetsOldIdsAndAReconnectRemovesThemFromTheClient(t *testing.T) {
	r := newSpawnRig(t)
	r.row(1, "NPC301", 100, rowOpt{})
	login := r.rig(r.data(), StartLogin, "", "5 6 7 ")
	login.spawner.Refresh(0, 1_800_000_000)
	if !slices.Equal(*login.events, []string{"+1"}) || !slices.Equal(login.world.NetIds.Alive(), []int64{login.spawner.Spawns()[0].NetID}) {
		t.Fatal(*login.events)
	}
	path := filepath.Join(r.dir, "player", "777", "world_net_ids")
	if !strings.Contains(readFile(t, path), "-5 ") {
		t.Fatal(readFile(t, path))
	}
	os.Remove(path)
	re := r.rig(r.data(), StartReconnect, "", "5 +6 -7 ")
	re.spawner.Refresh(0, 1_800_000_000)
	if !slices.Equal(*re.events, []string{"-5,6", "+1"}) {
		t.Fatal(*re.events)
	}
	*re.events = nil
	re.spawner.Refresh(0, 1_800_000_000)
	if len(*re.events) != 0 {
		t.Fatal(*re.events)
	}
}

func TestWorldNetIdsParseAppendAndCompact(t *testing.T) {
	var alive omap.Map[int64, struct{}]
	if n := ParseWorldNetIds("100 101 102\n+103 -101 -102 +102 junk", &alive); n != 8 {
		t.Fatal(n)
	}
	got := alive.Keys()
	slices.Sort(got)
	if !slices.Equal(got, []int64{100, 102, 103}) {
		t.Fatal(got)
	}
	files := save.NewPlayerFiles(t.TempDir(), 777)
	path := files.PathOf("world_net_ids")
	ids := NewWorldNetIds(files)
	ids.Save([]int64{1, 2})
	if readFile(t, path) != "+1 +2 " {
		t.Fatal(readFile(t, path))
	}
	ids.Save([]int64{2, 3})
	ids.Save([]int64{2, 3})
	if readFile(t, path) != "+1 +2 +3 -1 " {
		t.Fatal(readFile(t, path))
	}
	for i := range 130 {
		ids.Save([]int64{int64(10 + i)})
	}
	if n := len(strings.Fields(readFile(t, path))); n > 256 {
		t.Fatal(n)
	}
	if !slices.Equal(NewWorldNetIds(files).Alive(), []int64{139}) {
		t.Fatal(NewWorldNetIds(files).Alive())
	}
}

func TestVisibilityClockRangesWrapAndUnknownTypesShow(t *testing.T) {
	if !InHours(9, 17) || InHours(17, 1) || !InHours(6, 0) || !InHours(3, 3) {
		t.Fatal("hours")
	}
	none := save.QuestProgressOf()
	h := func(s string) bool { return Holds(Obj(js(t, s)), none) }
	cases := map[string]bool{
		`{"Type":"TimePeriod","TimePeriod":"DayTime"}`:              true,
		`{"Type":"TimePeriod","TimePeriod":"Night"}`:                false,
		`{"Type":"TimePeriod","Compare":"Ne","TimePeriod":"Night"}`: true,
		`{"Type":"Weather","WeatherId":5}`:                          false,
		`{"Type":"CompareVar"}`:                                     true,
		`{"Type":"PreQuest","PreQuestId":140000004}`:                false,
		`{"Type":"PreQuest","Compare":"Ne","PreQuestId":121000031}`: true,
	}
	for s, want := range cases {
		if h(s) != want {
			t.Fatal(s)
		}
	}
	if !Visible(js(t, `{"EntityVisibleComponent":{"Disabled":true,"VisibleConditions":[{"Conditions":[{"Type":"Weather","WeatherId":5}]}]}}`), none) {
		t.Fatal("disabled")
	}
}

func TestEveryComparedEntityTypeMustOccurInTheData(t *testing.T) {
	missing := newSpawnRig(t).data().MissingEntityTypes()
	if !slices.Contains(missing, "SimpleNPc") || slices.Contains(missing, "Chair") {
		t.Fatal(missing)
	}
}

// ----- level entities (LevelEntityTests) -----

func levelData(t testing.TB) *WorldData {
	tables := &data.DataTables{
		BlueprintConfig: []*data.BlueprintConfig{
			{BlueprintType: "NPC301", EntityType: "Npc", EntityLogic: "Npc", ModelId: 410230, HalfHeight: 80},
			{BlueprintType: "Monster071", EntityType: "Monster", EntityLogic: "Monster", ModelId: 390008, HalfHeight: 120},
			{BlueprintType: "Teleport001", EntityType: "Teleporter", EntityLogic: "Item", ModelId: 700009},
			{BlueprintType: "Chest", EntityType: "TreasureBox", EntityLogic: "Item", HalfHeight: 50},
			{BlueprintType: "Server", EntityType: "X", EntityLogic: "ServerOnly"},
			{BlueprintType: "NoTemplate", EntityType: "X", EntityLogic: "Item"},
		},
		TemplateConfig: []*data.TemplateConfig{
			{Id: 109700031, BlueprintType: "NPC301", ComponentsData: js(t, `{"BaseInfoComponent":{"Category":{"MainType":"Npc"},"Camp":2},"AiComponent":{"AiId":0},"ModelComponent":{"HalfHeight":80,"ModelType":{"Type":"ModelId","ModelId":410230}}}`)},
			{Id: 650000014, BlueprintType: "Monster071", ComponentsData: js(t, `{"BaseInfoComponent":{"Category":{"MonsterMatchType":2,"MainType":"Monster"},"Camp":1},
				"AiComponent":{"AiId":800041,"WeaponId":"77"},
				"AttributeComponent":{"PropertyId":390000008,"Level":1,"RageModeId":20080004,"HardnessModeId":10080004},
				"ModelComponent":{"HalfHeight":120,"ModelType":{"Type":"ModelId","ModelId":390008}},
				"MonsterComponent":{"InitGasTag":["怪物.boss"]}}`)},
			{Id: 1, BlueprintType: "Teleport001", ComponentsData: js(t, `{"BaseInfoComponent":{"Camp":2}}`)},
			{Id: 2, BlueprintType: "Chest", ComponentsData: js(t, `{"BaseInfoComponent":{},"EntityStateComponent":{"State":"关卡.Common.状态.关闭"}}`)},
			{Id: 3, BlueprintType: "Server", ComponentsData: js(t, `{"BaseInfoComponent":{}}`)},
		},
		BaseProperty:       []*data.BaseProperty{{Id: 390000008, LifeMax: 5000, Life: 5000}},
		ModelConfigPreload: []*data.ModelConfigPreload{{Id: 390008, ActorClassPath: "/Game/Boss.Boss_C"}},
		EntitySkillPreload: []*data.EntitySkillPreload{
			{Id: 1, SkillId: 200001, ActorBlueprint: "/Game/Boss.Boss_C"},
			{Id: 2, SkillId: 77012005, ActorBlueprint: "/Game/Boss.Boss_C"},
			{Id: 3, SkillId: 77012005, ActorBlueprint: "/Game/Boss.Boss_C"},
			{Id: 4, SkillId: 9, ActorBlueprint: ""},
		},
		AiBase: []*data.AiBase{{Id: 800041, StateMachine: "SM_Boss"}},
		AiStateMachineConfig: []*data.AiStateMachineConfig{{Id: "SM_Boss", StateMachineJson: js(t, fmt.Sprintf(
			`{"Nodes":[{"Uuid":36,"Transitions":[
			{"From":38,"To":51,"Conditions":[{"Name":"CondTag","CondTag":{"TagId":%d}}]},
			{"From":38,"To":52,"Conditions":[{"Name":"CondTag","CondTag":{"TagId":%d}}]}]}]}`, RepeatBossTag, StoryBossTag))}},
	}
	return NewWorldData(tables.Reindex(), nil)
}

func lrow(t testing.TB, id int32, bp string, loc, rot [3]int32, comps string) *data.LevelEntityConfig {
	if comps == "" {
		comps = "{}"
	}
	return &data.LevelEntityConfig{MapId: 8, EntityId: id, BlueprintType: bp,
		Transform:      []*data.IntVector{{X: loc[0], Y: loc[1], Z: loc[2]}, {X: rot[0], Y: rot[1], Z: rot[2]}, {X: 100, Y: 100, Z: 100}},
		ComponentsData: js(t, comps)}
}

func build(t *testing.T, d *WorldData, row *data.LevelEntityConfig, ctx *LevelContext) *Entity {
	t.Helper()
	e, why := BuildLevelEntity(d, row, ctx, nil)
	if e == nil {
		t.Fatal("refused: " + why)
	}
	return e
}

func near(a, b, eps float32) bool { return math.Abs(float64(a-b)) <= float64(eps) }

func TestTheLogicGivesTheKind(t *testing.T) {
	cases := map[string]LevelKind{
		"Item": {TypeSceneItem, StateDefault, false}, "Animal": {TypeAnimal, StateDefault, true},
		"Monster": {TypeMonster, StateBorn, true}, "Vehicle": {TypeVehicle, StateDefault, false},
		"Npc": {TypeNpc, StateDefault, true}, "Vision": {TypeVision, StateDefault, true},
		"ClientOnly": {TypeClientOnly, StateDefault, false}, "Custom": {TypeCustom, StateDefault, false},
	}
	for logic, want := range cases {
		if k, ok := LevelKindOf(logic); !ok || k != want {
			t.Fatal(logic)
		}
	}
	for _, logic := range []string{"ServerOnly", "SimpleCombat", "Unknown"} {
		if _, ok := LevelKindOf(logic); ok {
			t.Fatal(logic)
		}
	}
}

func TestCharactersAreLiftedByHalfHeightAsCaptured(t *testing.T) {
	d := levelData(t)
	npc := build(t, d, lrow(t, 125000663, "NPC301", [3]int32{-8555039, 7130178, 404658}, [3]int32{0, 0, 9860}, ""), nil)
	if !near(npc.Pos.Z, 4126.58, 0.01) || !near(npc.Pos.X, -85550.39, 0.01) || !near(npc.Rot.Yaw, 98.6, 0.001) {
		t.Fatal(npc.Pos, npc.Rot)
	}
	monster := build(t, d, lrow(t, 110001099, "Monster071", [3]int32{-11027876, 7326324, 694135}, [3]int32{}, ""), nil)
	if !near(monster.Pos.Z, 7061.35, 0.01) {
		t.Fatal(monster.Pos)
	}
	beacon := build(t, d, lrow(t, 113002862, "Teleport001", [3]int32{-10010922, 9338225, 411097}, [3]int32{}, ""), nil)
	if !near(beacon.Pos.Z, 4110.97, 0.01) {
		t.Fatal(beacon.Pos)
	}
	chest := build(t, d, lrow(t, 5, "Chest", [3]int32{0, 0, 1000}, [3]int32{}, ""), nil)
	if chest.Pos.Z != 10 {
		t.Fatal(chest.Pos)
	}
}

func TestRotationMapsXToRollYToPitchZToYaw(t *testing.T) {
	_, rot, _ := Place(lrow(t, 1, "NPC301", [3]int32{}, [3]int32{100, 200, 300}, ""))
	if rot != (Rot{Pitch: 2, Yaw: 3, Roll: 1}) {
		t.Fatal(rot)
	}
	if _, _, ok := Place(&data.LevelEntityConfig{Transform: []*data.IntVector{{X: 1, Y: 2, Z: 3}}}); ok {
		t.Fatal("one vector placed")
	}
}

func TestTheBeaconCarriesTheActiveStateTagInBothTagListsAsCaptured(t *testing.T) {
	b := build(t, levelData(t), lrow(t, 113002862, "Teleport001", [3]int32{}, [3]int32{}, ""), nil)
	if b.EntityType != TypeSceneItem || !slices.Equal(kinds(b), []string{"TagComponent", "FightBuffComponent", "StateTagComponentPb"}) {
		t.Fatal(kinds(b))
	}
	if b.Components[2].GetStateTagComponentPb().StateTagId != -3775711 {
		t.Fatal(b.Components[2])
	}
	tags := b.Components[0].GetTagComponent()
	if len(tags.GameplayTags) != 1 || tags.GameplayTags[0].Id != -3775711 || tags.GameplayTags[0].TagCount != 1 ||
		!slices.Equal(tags.EntityCommonTags, []int32{-3775711}) {
		t.Fatal(tags)
	}
	if *b.Camp != 2 || b.ConfigType != ConfigLevel || !b.IsVisible {
		t.Fatal(b)
	}
}

func TestTheStateIsTheTemplatesElseTheRowsElseNormalAndASavedOneWins(t *testing.T) {
	d := levelData(t)
	last := func(e *Entity) int32 { return e.Components[len(e.Components)-1].GetStateTagComponentPb().StateTagId }
	chest := build(t, d, lrow(t, 5, "Chest", [3]int32{}, [3]int32{}, ""), nil)
	if last(chest) != RawTagID("关卡.Common.状态.关闭") {
		t.Fatal(last(chest))
	}
	saved := build(t, d, lrow(t, 5, "Chest", [3]int32{}, [3]int32{}, ""), &LevelContext{SavedState: func(id int32) (int32, bool) {
		return RawTagID("关卡.Common.状态.开启"), id == 5
	}})
	if last(saved) != RawTagID("关卡.Common.状态.开启") {
		t.Fatal(last(saved))
	}
	beacon := build(t, d, lrow(t, 9, "Teleport001", [3]int32{}, [3]int32{}, ""), &LevelContext{SavedState: func(int32) (int32, bool) {
		return RawTagID(TagNormal), true
	}})
	if last(beacon) != RawTagID(TagNormal) {
		t.Fatal(last(beacon))
	}
}

func TestAnNpcHasTheSixCapturedComponentsAndNoStateTag(t *testing.T) {
	npc := build(t, levelData(t), lrow(t, 125000663, "NPC301", [3]int32{}, [3]int32{}, `{"BaseInfoComponent":{"TidName":"x"}}`), nil)
	if !slices.Equal(kinds(npc), []string{"AttributeComponent", "TagComponent", "FightBuffComponent", "CombineEntityInfoPb", "LogicStateComponentPb", "MonsterAiComponentPb"}) {
		t.Fatal(kinds(npc))
	}
	ai := npc.Components[5].GetMonsterAiComponentPb()
	if npc.State != StateDefault || *npc.Camp != 2 || ai.WeaponId != 0 || ai.AiTeamInitId != 100 || ai.CombatMessageId != 1 {
		t.Fatal(ai)
	}
	if len(npc.Components[0].GetAttributeComponent().WhiteGreenProps) != AttributeCount-1 {
		t.Fatal(len(npc.Components[0].GetAttributeComponent().WhiteGreenProps))
	}
}

func TestAMonsterIsBornWithItsPassiveSkillsAndTheRepeatBossTag(t *testing.T) {
	d := levelData(t)
	m := build(t, d, lrow(t, 110001099, "Monster071", [3]int32{}, [3]int32{}, `{"BaseInfoComponent":{"Category":{"TraceMatchType":1}},"MonsterComponent":{"InitGasTag":["怪物.剧情boss"]}}`), nil)
	if m.State != StateBorn || !slices.Equal(kinds(m), []string{"AttributeComponent", "TagComponent", "FightBuffComponent", "CombineEntityInfoPb", "LogicStateComponentPb", "MonsterAiComponentPb", "SkillComponentPb"}) {
		t.Fatal(kinds(m))
	}
	var skills [][2]int64
	for _, s := range m.Components[6].GetSkillComponentPb().GaPassiveSkills {
		skills = append(skills, [2]int64{s.SkillId, s.ContextId})
	}
	if !slices.Equal(skills, [][2]int64{{200001, -1}, {77012005, -1}}) {
		t.Fatal(skills)
	}
	if m.Components[5].GetMonsterAiComponentPb().WeaponId != 77 {
		t.Fatal("weapon")
	}
	a := m.Components[0].GetAttributeComponent()
	if a.HardnessModeId != 10080004 || a.RageModeId != 20080004 {
		t.Fatal(a.HardnessModeId)
	}
	for _, p := range a.WhiteGreenProps {
		if p.AttributeType == 2 && p.CurrentValue != 5000 || p.AttributeType == 1 && p.CurrentValue != 1 {
			t.Fatal(p)
		}
	}
	if *m.Camp != 1 {
		t.Fatal(*m.Camp)
	}
	if g := m.Components[1].GetTagComponent().GameplayTags; len(g) != 1 || g[0].Id != RawTagID("怪物.剧情boss") {
		t.Fatal(g)
	}
	if !d.IsRepeatBoss(800041) || d.IsRepeatBoss(1) {
		t.Fatal("repeat boss")
	}
}

func TestTheMergeTakesMissingComponentsAndNullTopLevelFieldsButNotDeeper(t *testing.T) {
	merged := Merge(
		js(t, `{"BaseInfoComponent":{"Camp":null,"Category":{"A":1}},"NpcPerformComponent":{"X":1}}`),
		js(t, `{"BaseInfoComponent":{"Camp":2,"Category":{"A":0,"B":2},"TidName":"t"},"AiComponent":{"AiId":5},"TriggerComponent":{"Y":1}}`))
	info := merged["BaseInfoComponent"]
	if JSONIntOr(info.Get("Camp"), 0) != 2 || Field(info.Get("Category"), "B") != nil {
		t.Fatal(info)
	}
	if s, _ := Str(info.Get("TidName")); s != "t" {
		t.Fatal(s)
	}
	if JSONIntOr(merged["AiComponent"].Get("AiId"), 0) != 5 {
		t.Fatal("ai")
	}
	if _, ok := merged["NpcPerformComponent"]; !ok {
		t.Fatal("npc perform")
	}
	if _, ok := merged["TriggerComponent"]; ok {
		t.Fatal("trigger")
	}
}

func TestRowsThatCannotBeSpawnedSayWhy(t *testing.T) {
	d := levelData(t)
	for bp, want := range map[string]string{"Server": "logic ServerOnly", "NoTemplate": "no template", "Nothing": "no blueprint"} {
		if e, why := BuildLevelEntity(d, lrow(t, 1, bp, [3]int32{}, [3]int32{}, ""), &LevelContext{}, nil); e != nil || why != want {
			t.Fatal(bp, why)
		}
	}
	if e, _ := BuildLevelEntity(d, lrow(t, 1, "Monster071", [3]int32{}, [3]int32{}, ""), &LevelContext{}, nil); e == nil {
		t.Fatal("monster refused")
	}
}

func TestAMovedEntityStandsWhereTheQuestPutIt(t *testing.T) {
	yaw := float32(45)
	npc := build(t, levelData(t), lrow(t, 7, "NPC301", [3]int32{}, [3]int32{0, 0, 9000}, ""), &LevelContext{Moved: func(id int32) (MovedPlace, bool) {
		return MovedPlace{Vec3{10, 20, 30}, &yaw}, id == 7
	}})
	if npc.Pos != (Vec3{10, 20, 110}) || npc.Rot.Yaw != 45 {
		t.Fatal(npc.Pos, npc.Rot)
	}
}

func TestWeaponIdsMayBeNumbersOrNumericStrings(t *testing.T) {
	if n, ok := JSONInt(js(t, "3")); !ok || n != 3 {
		t.Fatal(n)
	}
	if n, ok := JSONInt(js(t, `"3"`)); !ok || n != 3 {
		t.Fatal(n)
	}
	if _, ok := JSONInt(js(t, `"x"`)); ok {
		t.Fatal("x")
	}
	if _, ok := JSONInt(js(t, "1.5")); ok {
		t.Fatal("1.5")
	}
}

// ----- parts (WorldPartsTests) -----

func TestNetIdsComeInBlocksOf256FromTheCounterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "next")
	ids := NewNetIds(path)
	if ids.Take() != 1 || strings.TrimSpace(readFile(t, path)) != "257" {
		t.Fatal(readFile(t, path))
	}
	for i := int64(2); i <= 256; i++ {
		if ids.Take() != i {
			t.Fatal(i)
		}
	}
	if ids.Take() != 257 || strings.TrimSpace(readFile(t, path)) != "513" {
		t.Fatal(readFile(t, path))
	}
	if NewNetIds(path).Take() != 513 {
		t.Fatal("again")
	}
}

func TestRaisingTheCounterSkipsAheadAndNeverGoesBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "next")
	ids := NewNetIds(path)
	ids.Take()
	ids.RaiseTo(100)
	if ids.Take() != 100 {
		t.Fatal("100")
	}
	ids.RaiseTo(50)
	if ids.Take() != 101 {
		t.Fatal("101")
	}
	ids.RaiseTo(10_000)
	if ids.Take() != 10_000 || strings.TrimSpace(readFile(t, path)) != "10256" {
		t.Fatal(readFile(t, path))
	}
}

func TestTheWorldFloorIsMillisecondsSinceItsEpochAndAtLeast1000(t *testing.T) {
	if WorldFloor(0) != 1000 || WorldFloor(1_700_000_000) != 1000 || WorldFloor(1_763_000_000) != 63_000_000_000 {
		t.Fatal("floor")
	}
}

func TestAnExplicitIDRaisesTheCounterAndATakenIDIsRefused(t *testing.T) {
	scene := NewScene(8, 8, save.NewPlayerFiles(t.TempDir(), 777), NewFlowClock(0, 0))
	e := scene.AddAs(&Entity{}, scene.NetIds.Peek()+10)
	if scene.Add(&Entity{}).ID != e.ID+1 {
		t.Fatal("raise")
	}
	for _, id := range []int64{e.ID, 0} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("accepted", id)
				}
			}()
			scene.AddAs(&Entity{}, id)
		}()
	}
}

func TestFlowTimeRunsAtItsDilation(t *testing.T) {
	for dilation, after := range map[float64]int64{1: 1000, 0.5: 500, 0: 0} {
		f := NewFlowClock(5_000_000, 100)
		f.SetDilation(dilation, 100)
		if f.Now(1100) != 5_000_000+after {
			t.Fatal(dilation)
		}
	}
}

func TestANewDilationReAnchorsTheFlowTimeAndNonFiniteIsIgnored(t *testing.T) {
	f := NewFlowClock(1000, 0)
	f.SetDilation(0.5, 1000)
	if f.Now(1000) != 2000 || f.Now(2000) != 2500 {
		t.Fatal(f.Now(2000))
	}
	f.SetDilation(math.NaN(), 2000)
	if f.Dilation() != 0.5 {
		t.Fatal(f.Dilation())
	}
	f.SetDilation(2, 2000)
	if f.Now(3000) != 4500 {
		t.Fatal(f.Now(3000))
	}
}

func TestTagIdsAreTheHashOfTheNameRemappedThroughTheTable(t *testing.T) {
	if RawTagID(TagActive) != -3775711 || RawTagID(TagNormal) != -1152559349 {
		t.Fatal(RawTagID(TagActive))
	}
	tables := (&data.DataTables{GameplayTagRemap: []*data.GameplayTagRemap{{RawId: -3775711, CanonicalId: 12345}}}).Reindex()
	if TagID(tables, TagActive) != 12345 || TagID(tables, TagNormal) != -1152559349 {
		t.Fatal("remap")
	}
}

func TestRoadsAreTheUnionOfTheMapsWaySplinesInFirstSeenOrder(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "AutoPilotCircles.json")
	writeFile(t, file, `[{"MapId":8,"WaySplines":[3,1]},{"MapId":9,"WaySplines":[7]},{"MapId":8,"WaySplines":[1,2]},{"Id":5}]`)
	roads, err := ReadRoads(file)
	if err != nil || !slices.Equal(roads[8], []int32{3, 1, 2}) || !slices.Equal(roads[9], []int32{7}) {
		t.Fatal(roads, err)
	}
	if r, err := ReadRoads(filepath.Join(dir, "missing.json")); err != nil || len(r) != 0 {
		t.Fatal(r)
	}
}

func TestDataLayersSwapTheBuiltRoadsInLahaiRoi(t *testing.T) {
	tables := &data.DataTables{
		InstanceDungeon: []*data.InstanceDungeon{{Id: 8, MapConfigId: 8}, {Id: 906, MapConfigId: 906}},
		DataLayerConfig: []*data.DataLayerConfig{
			{Id: 11, LevelId: 8, InitLoad: true}, {Id: 12, LevelId: 8}, {Id: 13, LevelId: 8, InitLoad: true},
			{Id: 11, LevelId: 8, InitLoad: true}, {Id: 61, LevelId: 906, InitLoad: true}, {Id: 62, LevelId: 906, InitLoad: true},
		},
		InfrRoadBuild: []*data.InfrRoadBuild{
			{Id: 1, DungeonId: 906, UnloadDataLayers: []int32{61}, LoadDataLayers: []int32{70}},
			{Id: 2, DungeonId: 906, LoadDataLayers: []int32{71, 70}},
			{Id: 3, DungeonId: 8, UnloadDataLayers: []int32{11}, LoadDataLayers: []int32{99}},
		},
	}
	d := NewWorldData(tables.Reindex(), nil)
	if !slices.Equal(d.DataLayers(8), []int32{11, 13}) || !slices.Equal(d.DataLayers(906), []int32{62, 70, 71}) {
		t.Fatal(d.DataLayers(8), d.DataLayers(906))
	}
}

func TestJoinEntitiesMakeTheFourInOrder(t *testing.T) {
	tables := &data.DataTables{
		ExploreTools:    []*data.ExploreTools{{PhantomSkillId: 6001, SummonConfigId: 3001}},
		SummonCfg:       []*data.SummonCfg{{Id: 3001, BlueprintType: "Motor"}, {Id: CompanionSummonCfg, BlueprintType: "Companion", BornBuffId: []float64{5001}}},
		BlueprintConfig: []*data.BlueprintConfig{{Id: 1, BlueprintType: "Motor", EntityLogic: "Vehicle", EntityType: "Vehicle"}},
		TemplateConfig:  []*data.TemplateConfig{{Id: 36000052, BlueprintType: "Motor"}},
		Buff:            []*data.Buff{{Id: 3000000801}},
		BaseProperty:    []*data.BaseProperty{{Id: VehicleProperty, LifeMax: 1000, Life: 1000, Atk: 7}},
	}
	d := NewWorldData(tables.Reindex(), nil)
	files := save.NewPlayerFiles(t.TempDir(), 777)
	scene := NewScene(8, 8, files, NewFlowClock(0, 0))
	MakeJoinEntities(scene, save.LoadPlayerState(files), d)
	es := scene.Entities()
	if len(es) != 4 || es[0].ConfigID != 36000052 || es[1].ConfigID != PlayerEntityTemplate || es[2].ConfigID != CompanionTemplate || es[3].ConfigID != BattleEntityTemplate {
		t.Fatal(es)
	}
	buffs := es[0].Components[2].GetFightBuffComponent().FightBuffInfos
	if len(buffs) != 1 || buffs[0].BuffId != 3000000801 || buffs[0].HandleId != 1 {
		t.Fatal(buffs)
	}
	cb := es[2].Components[1].GetFightBuffComponent().FightBuffInfos
	if len(cb) != 1 || cb[0].BuffId != 5001 || cb[0].HandleId != 2 {
		t.Fatal(cb)
	}
	for _, p := range es[0].Components[0].GetAttributeComponent().WhiteGreenProps {
		if p.AttributeType == 2 && p.CurrentValue != 1000 {
			t.Fatal(p)
		}
	}
	if ScenePbIDs := []int64{scene.VehicleID, scene.PlayerEntityID, scene.BattleEntityID}; ScenePbIDs[0] != es[0].ID || ScenePbIDs[2] != es[3].ID {
		t.Fatal(ScenePbIDs)
	}
}
