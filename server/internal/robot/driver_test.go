// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/wire"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// Ported from our C# RobotDriverTests: the robot drives hand-made quests in memory.

const testPlayer = 777

func savePlayerFile(t *testing.T, state, rel, text string) {
	t.Helper()
	p := filepath.Join(state, "player", fmt.Sprint(testPlayer), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildQuestWorld is the game's test world (WorldRig) with an NPC, gears, a monster template and
// three main quests: 10 DoInteract → ReachArea → Timer, 11 CheckEntityState on 5001, 12 an
// unsolvable Kill.
func buildQuestWorld(t *testing.T) (string, *data.DataTables, *world.WorldData, *quests.Catalog) {
	m := func(kv ...int32) *data.Map[int32, int32] {
		r := &data.Map[int32, int32]{}
		for i := 0; i+1 < len(kv); i += 2 {
			r.Pairs = append(r.Pairs, data.Pair[int32, int32]{Key: kv[i], Value: kv[i+1]})
		}
		return r
	}
	js := func(s string) data.Value {
		v, err := data.ParseJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tb := &data.DataTables{
		InstanceDungeon: []*data.InstanceDungeon{
			{Id: 8, MapConfigId: 8, BornPosition: []int32{-143200, 63380, 7610}, BornRotation: []int32{0, 0, 45}},
			{Id: 906, MapConfigId: 906, BornPosition: []int32{88159, -577501, 34162}, BornRotation: []int32{0, 0, 186}},
			{Id: 301010, MapConfigId: 301000},
		},
		DataLayerConfig: []*data.DataLayerConfig{{Id: 11, LevelId: 8, InitLoad: true}, {Id: 61, LevelId: 906, InitLoad: true}},
		ExploreTools: []*data.ExploreTools{
			{PhantomSkillId: 1001, Authorization: m()}, {PhantomSkillId: 1009, Authorization: m(1, 1)},
			{PhantomSkillId: 1015, Authorization: m()}, {PhantomSkillId: 6001, SummonConfigId: 3001, Authorization: m()},
		},
		SummonCfg: []*data.SummonCfg{{Id: 3001, BlueprintType: "Motor"}, {Id: world.CompanionSummonCfg, BlueprintType: "Companion", BornBuffId: []float64{5001}}},
		BlueprintConfig: []*data.BlueprintConfig{
			{Id: 1, BlueprintType: "Motor", EntityLogic: "Vehicle", EntityType: "Vehicle"},
			{Id: 2, BlueprintType: "NPC_Test", EntityLogic: "Npc", EntityType: "Npc"},
			{Id: 3, BlueprintType: "Gear_Test", EntityLogic: "Item", EntityType: "InteractGear"},
			{Id: 4, BlueprintType: "Monster_Test", EntityLogic: "Monster", EntityType: "Monster"},
			{Id: 5, BlueprintType: "ServerOnly_Test", EntityLogic: "ServerOnly", EntityType: "InteractGear"},
		},
		TemplateConfig: []*data.TemplateConfig{
			{Id: 36000052, BlueprintType: "Motor"},
			{Id: 100, BlueprintType: "NPC_Test", ComponentsData: js(`{"BaseInfoComponent":{}}`)},
			{Id: 101, BlueprintType: "Gear_Test", ComponentsData: js(`{"BaseInfoComponent":{}}`)},
			{Id: 102, BlueprintType: "Monster_Test", ComponentsData: js(`{"BaseInfoComponent":{}}`)},
			{Id: 103, BlueprintType: "ServerOnly_Test", ComponentsData: js(`{"BaseInfoComponent":{}}`)},
		},
		Buff:              []*data.Buff{{Id: 3000000801}},
		BaseProperty:      []*data.BaseProperty{{Id: world.VehicleProperty, LifeMax: 1000, Life: 1000, Atk: 7}, {Id: 100, Lv: 90, LifeMax: 10000, Life: 10000, Atk: 500, Def: 200}, {Id: 102, LifeMax: 500, Life: 500, Atk: 10}},
		Teleporter:        []*data.Teleporter{{Id: 1, MapId: 8}, {Id: 2, MapId: 906}},
		ItemInfo:          []*data.ItemInfo{{Id: 2}, {Id: 3}},
		MainRoleConfig:    []*data.MainRoleConfig{{Id: 1501, Gender: 1}, {Id: 1502, Gender: 0}},
		FunctionCondition: []*data.FunctionCondition{{FunctionId: 10001}, {FunctionId: 10002}},
		BackgroundCard:    []*data.BackgroundCard{{Id: 80060001}},
		CalabashLevel:     []*data.CalabashLevel{{Level: 1}, {Level: 5}},
		RoleInfo: []*data.RoleInfo{
			{Id: 1304, MaxLevel: 90, PropertyId: 100, ElementId: 1},
			{Id: 1505, MaxLevel: 90, PropertyId: 100, ElementId: 5},
			{Id: 1311, MaxLevel: 90, PropertyId: 100, ElementId: 3},
		},
	}
	one := func() *data.IntVector { return &data.IntVector{X: 100, Y: 100, Z: 100} }
	at := func(x, y, z int32) []*data.IntVector { return []*data.IntVector{{X: x, Y: y, Z: z}, {}, one()} }
	tb.LevelEntityConfig = []*data.LevelEntityConfig{
		{Id: 1, MapId: 8, EntityId: 40, BlueprintType: "NPC_Test", Transform: at(-11110000, 7422900, 698000), ComponentsData: js(`{}`)},
		{Id: 2, MapId: 8, EntityId: 5001, BlueprintType: "Gear_Test", Transform: at(-11105000, 7422900, 698000), ComponentsData: js(`{ "EntityStateComponent": { "State": "关卡.Common.状态.常态" } }`)},
		{Id: 3, MapId: 8, EntityId: 5002, BlueprintType: "Gear_Test", InSleep: true, Transform: at(-11100000, 7422900, 698000), ComponentsData: js(`{}`)},
		{Id: 4, MapId: 8, EntityId: 5003, BlueprintType: "Gear_Test", Transform: at(90000000, 0, 0), ComponentsData: js(`{}`)},
		{Id: 5, MapId: 8, EntityId: 5004, BlueprintType: "ServerOnly_Test", Transform: at(-11110000, 7422900, 698000), ComponentsData: js(`{}`)},
	}
	tb.Reindex()
	wd := world.NewWorldData(tb, map[int32][]int32{906: {4, 5}})

	state := t.TempDir()
	s := func(rel, text string) { savePlayerFile(t, state, rel, text) }
	s("basic_info", `.{ .attributes = .{ .level = 70, .name = "Ren", .sex = 1 }, .role_show_list = .{ 1304, 9999 } }`)
	s("last_scene", ".{ .instance_id = 8 }")
	s("formation_info", `.{ .cur_formation = 0, .formations = .{
	    .{ .cur_role = 1304, .name = null, .roles = .{
	        .{ .role_id = 1311, .entity_id = 105, .on_stage_without_control = false },
	        .{ .role_id = 1505, .entity_id = 106, .on_stage_without_control = false },
	        .{ .role_id = 1304, .entity_id = 107, .on_stage_without_control = false } } },
	} }`)
	s("scene/8/instance", ".{ .owner_id = 0, .buff_handle = 0, .map_time = .{ .hour = 8, .minute = 0, .owner_clock_time_span = 0 }, .players = .{ .{ .id = 777, .group_type = 1, .location = .{ -111141.48, 74229.27, 6980.71 }, .rotation = .{ 0, 0, 68.93 } } } }")
	s("scene/8/entity/next", "95059777256\n")
	for _, rw := range [][2]int{{1311, 11}, {1505, 12}, {1304, 13}} {
		s(fmt.Sprintf("role/%d", rw[0]), fmt.Sprintf(".{ .level = 90, .weapon = %d, .skill_branch = 3, .base_prop = .{ 0, 90, 10000, 10000 }, .add_prop = .{ 0, 0, 500, 500 } }", rw[1]))
		s(fmt.Sprintf("weapon/%d", rw[1]), fmt.Sprintf(".{ .id = 2100%d, .breach = 6, .role_id = %d }", rw[1], rw[0]))
	}
	s("inventory_info", ".{ .normal_items = .{ .{ .id = 2, .count = 100 }, .{ .id = 4, .count = 1 }, .{ .id = 3 } } }")

	n := func(id, parent int32, typ, cond string, kids ...int32) quests.NodeDef {
		return quests.NodeDef{ID: id, Parent: parent, Type: typ, Cond: cond, Kids: kids}
	}
	ds := quests.NewQuestDataSet()
	q10talk := n(2, 1, "ChildQuest", "DoInteract")
	q10talk.Options = []quests.QuestOption{{EntityID: 40, Guid: "g40", TidContent: "说话", Flow: &quests.FlowRef{FlowListName: "剧情_一", FlowID: 1, StateID: 1}}}
	q10timer := n(4, 1, "ChildQuest", "Timer")
	q10timer.Time = 15
	ds.Quests = append(ds.Quests, quests.QuestDef{ID: 10, Type: 1, Name: "主线一", PreQuest: 1,
		Nodes: []quests.NodeDef{n(1, 0, "Sequence", "", 2, 3, 4, 5), q10talk, n(3, 1, "ChildQuest", "ReachArea"), q10timer, n(5, 1, "QuestSucceed", "")}})
	q11state := n(2, 1, "ChildQuest", "CheckEntityState")
	q11state.States = []quests.StateCond{{EntityID: 5001, State: "关卡.Common.状态.激活"}}
	ds.Quests = append(ds.Quests, quests.QuestDef{ID: 11, Type: 1, Name: "主线二", PreQuest: 10, Nodes: []quests.NodeDef{n(1, 0, "Sequence", "", 2, 3), q11state, n(3, 1, "QuestSucceed", "")}})
	q12kill := n(2, 1, "ChildQuest", "Kill")
	q12kill.Targets = []int64{999999}
	ds.Quests = append(ds.Quests, quests.QuestDef{ID: 12, Type: 1, Name: "主线三", PreQuest: 11, Nodes: []quests.NodeDef{n(1, 0, "Sequence", "", 2, 3), q12kill, n(3, 1, "QuestSucceed", "")}})
	ds.HideGroups["q_10_clear"] = quests.HideGroupDef{Key: "q_10_clear", IDs: []quests.EntityPair{{Map: 8, Entity: 5001}}}
	return state, tb, wd, quests.NewCatalog(ds)
}

func startTestHost(t *testing.T, prepare func(string, *quests.Catalog)) (*Host, *quests.Catalog, []*wire.Frame) {
	state, tb, wd, cat := buildQuestWorld(t)
	var frames []*wire.Frame
	h, err := StartFromCopy(state, HostConfig{PlayerID: testPlayer, Epoch: 1_795_039_200, Tables: tb, WorldData: wd, Quests: cat,
		OnLoginFrame: func(f *wire.Frame) { frames = append(frames, f) }}, func(dir string) {
		if prepare != nil {
			prepare(dir, cat)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, cat, frames
}

func TestDrivesHandMadeStoryQuestsEndToEndInMemory(t *testing.T) {
	h, _, frames := startTestHost(t, func(dir string, cat *quests.Catalog) { PrepareFreshStory(dir, cat, 10, io.Discard) })
	setters := NewStateSetters([]SetterEntry{{Map: 8, ID: 5001, State: "关卡.Common.状态.激活", Ways: []SetterWay{{Kind: "client", ID: 5001}}}})
	var out bytes.Buffer
	r := NewRunner(h, setters, NewRangeTable(nil), nil, &out, false, false)
	for _, f := range frames {
		r.ProcessFrame(f)
	}
	reports := r.RunStory(math.MaxInt32, true)
	byID := map[int32]*StoryQuestReport{}
	for _, q := range reports {
		byID[q.ID] = q
	}
	if len(reports) != 3 {
		var sb strings.Builder
		for _, q := range reports {
			fmt.Fprintf(&sb, "%d:%s:stuck=%s:skips=%d; ", q.ID, q.Outcome, q.StuckAt.String(), q.Skips)
		}
		t.Fatalf("got %d reports: %s\n%s", len(reports), sb.String(), out.String())
	}
	q10, q11, q12 := byID[10], byID[11], byID[12]
	if q10.Outcome != StoryFinished || q10.Skips != 0 || q11.Outcome != StoryFinished || q11.Skips != 0 || !strings.Contains(q11.Drove.String(), "2:5001=client") ||
		q12.Outcome != StoryFinished || q12.Skips != 1 || !strings.Contains(q12.Skipped.String(), "2:Kill[999999=nowhere]") {
		t.Fatalf("%s", out.String())
	}
	if h.Session.Quests().ClockOffsetMs < 5000 {
		t.Fatal(h.Session.Quests().ClockOffsetMs)
	}
}

func TestTraceEntityPosAndLandingChecksWorkInMemory(t *testing.T) {
	h, _, frames := startTestHost(t, nil)
	var out bytes.Buffer
	r := NewRunner(h, nil, nil, nil, &out, false, false)
	for _, f := range frames {
		r.ProcessFrame(f)
	}
	r.EntityPosOK, r.EntityPosResult = false, nil
	if !r.SendRequest(&protocol.TraceEntityPosRequest{ConfigId: 40, DungeonId: 8}) || !r.EntityPosOK || r.EntityPosResult == nil ||
		r.EntityPosResult.X < -111200 || r.EntityPosResult.X > -111000 {
		t.Fatalf("%+v", r.EntityPosResult)
	}
	r.EntityPosOK = true
	if !r.SendRequest(&protocol.TraceEntityPosRequest{ConfigId: 999999, DungeonId: 8}) || r.EntityPosOK {
		t.Fatal("miss")
	}
	if rc := r.CheckLanding(); rc != 0 || !strings.Contains(out.String(), "robot: landing check ok") {
		t.Fatal(out.String())
	}
}

func TestSpawnDiagnosticsReportsExactReasons(t *testing.T) {
	h, _, _ := startTestHost(t, nil)
	s := h.Session
	s.ImmediateSpawnCheck()
	check := func(id int64, want string, helps bool) {
		t.Helper()
		if got := WhyNotSpawned(s, id); got != want {
			t.Fatalf("%d: %q, want %q", id, got, want)
		}
		if WaitingHelps(InspectSpawn(s, id)) != helps {
			t.Fatalf("%d: waiting helps %v", id, !helps)
		}
	}
	check(999999, "not on map", false)
	check(40, "spawned", true)
	check(5002, "asleep, nobody woke it", false)
	check(5004, "not spawnable", false)
	if !WaitingHelps(InspectSpawn(s, 5003)) || !strings.Contains(WhyNotSpawned(s, 5003), "m away") {
		t.Fatal(WhyNotSpawned(s, 5003))
	}
	s.World.State.Sleep(5003)
	check(5003, "destroyed/gone", false)
	s.QuestState.HideGroups.Add(quests.HideGroupDef{Key: "q_10_clear", Force: []quests.EntityPair{{Map: 8, Entity: 5002}}})
	s.World.State.Wake(5002)
	check(5002, "hidden by hide group", false)
}
