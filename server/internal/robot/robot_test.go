// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/data"
	"tidelab/internal/world"
)

// Ported from our C# RobotOptionsTests, RobotDataTests and RobotReportsTests.

func mustParse(t *testing.T, args ...string) *Options {
	t.Helper()
	o, err := ParseOptions(args)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestParsesStoryWithStartQuestLimitAndSkipStuck(t *testing.T) {
	o := mustParse(t, "story", "139000027", "--quests", "5", "--skip-stuck", "--epoch=1795039200", "--bindata", "bd", "--more=m1", "--more", "m2")
	if o.Mode != ModeStory || o.StoryStartQuest == nil || *o.StoryStartQuest != 139000027 || o.MaxQuests != 5 || !o.SkipStuck ||
		o.Epoch == nil || *o.Epoch != 1795039200 || o.BinData != "bd" || !slices.Equal(o.MoreBinData, []string{"m1", "m2"}) ||
		!strings.HasSuffix(o.ResolveReportPath(), "robot_story.tsv") {
		t.Fatalf("%+v", o)
	}
}

func TestParsesSideShard(t *testing.T) {
	o := mustParse(t, "side", "--shard=2/4", "--logs", "out_logs")
	if s, e := o.ShardRange(50); o.Mode != ModeSide || o.ShardIndex != 2 || o.ShardCount != 4 || s != 25 || e != 37 || o.ResolveReportPath() != filepath.Join("out_logs", "robot_side.tsv") {
		t.Fatalf("%+v", o)
	}
}

func TestRejectsBadShard(t *testing.T) {
	for _, spec := range []string{"4/4", "-1/4", "0/0", "bad"} {
		if _, err := ParseOptions([]string{"side", "--shard=" + spec}); err == nil {
			t.Fatal(spec)
		}
	}
}

func TestParsesNamedModes(t *testing.T) {
	for arg, want := range map[string]Mode{"assets": ModeAssets, "npcs": ModeNpcs, "offers": ModeOffers, "worldinst": ModeWorldInst, "pos": ModePos,
		"ride": ModeRide, "chairs": ModeChairs, "shop": ModeShop, "landing": ModeLanding, "patrol": ModePatrol, "weapons": ModeWeapons, "activity": ModeActivity} {
		if o := mustParse(t, arg); o.Mode != want {
			t.Fatal(arg)
		}
	}
}

func TestParsesDungeonModesAndReportNames(t *testing.T) {
	all := mustParse(t, "all", "--logs", "L")
	quests := mustParse(t, "quests", "--logs", "L")
	ids := mustParse(t, "3166", "2113", "1002055", "--logs", "L")
	custom := mustParse(t, "3166", "--report", "custom.tsv")
	if all.Mode != ModeDungeons || !all.DungeonsAll || all.ResolveReportPath() != filepath.Join("L", "robot_dungeons.tsv") ||
		quests.Mode != ModeDungeons || !quests.DungeonsQuests || quests.ResolveReportPath() != filepath.Join("L", "robot_quest_dungeons.tsv") ||
		ids.Mode != ModeDungeons || !slices.Equal(ids.DungeonIDs, []int32{3166, 2113, 1002055}) || ids.ResolveReportPath() != filepath.Join("L", "robot_dungeons_ids.tsv") ||
		custom.ResolveReportPath() != "custom.tsv" {
		t.Fatal("dungeon modes")
	}
}

func TestParsesCheckpointAndBooleanFlags(t *testing.T) {
	o := mustParse(t, "story", "--keep-state", "--save-state", "cp1", "--from=cp0", "--try-options", "--with-side", "--verbose", "--verify", "--player", "999")
	if !o.KeepState || *o.SaveState != "cp1" || *o.FromState != "cp0" || !o.TryOptions || !o.WithSide || !o.Verbose || !o.Verify || o.PlayerID != 999 {
		t.Fatalf("%+v", o)
	}
}

func TestRejectsUnknownFlagAndMissingValue(t *testing.T) {
	for _, args := range [][]string{{"story", "--no-such-flag"}, {"story", "--epoch"}, {"not_a_mode"}} {
		if _, err := ParseOptions(args); err == nil {
			t.Fatal(args)
		}
	}
}

func parseJSON(t *testing.T, s string) data.Value {
	t.Helper()
	v, err := data.ParseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStateSettersParsesAndMatchesByMapEntityAndState(t *testing.T) {
	ss := ParseStateSetters(parseJSON(t, `[
	  { "Map": 8, "Id": 1001, "State": "关卡.Common.状态.激活",
	    "Ways": [ { "Kind": "interact", "Id": 2001, "Option": 2 }, { "Kind": "chain", "Id": 2002, "State": "关卡.Common.状态.开启" } ] },
	  { "Map": 8, "Id": 1001, "State": "关卡.Common.状态.完成",
	    "Ways": [ { "Kind": "group", "Id": 3001, "Need": [ { "Id": 3010, "State": "关卡.Common.状态.激活" } ] } ] },
	  { "Map": 906, "Id": 1001, "State": "关卡.Common.状态.激活", "Ways": [ { "Kind": "interact", "Id": 4001, "Option": 0 } ] }
	]`))
	ways := ss.WaysFor(8, 1001, "关卡.Common.状态.激活")
	if ss.Count() != 3 || len(ways) != 2 || ways[0].Kind != "interact" || ways[0].ID != 2001 || ways[0].Option != 2 ||
		ways[1].Kind != "chain" || ways[1].ID != 2002 || ways[1].State != "关卡.Common.状态.开启" {
		t.Fatalf("%+v", ways)
	}
	g := ss.WaysFor(8, 1001, "关卡.Common.状态.完成")
	if len(g) != 1 || g[0].Kind != "group" || len(g[0].Need) != 1 || g[0].Need[0].ID != 3010 || len(ss.WaysFor(8, 9999, "关卡.Common.状态.激活")) != 0 {
		t.Fatalf("%+v", g)
	}
	if LoadStateSetters(filepath.Join(t.TempDir(), "missing.json")).Count() != 0 {
		t.Fatal("missing file")
	}
}

func TestRangeTableChecksSphereBoxAndCylinder(t *testing.T) {
	rt := ParseRangeTable(parseJSON(t, `[
	  { "Id": 10, "Map": 8, "Kind": 0, "C": [100, 200, 300], "P": [50, 0, 0] },
	  { "Id": 20, "Map": 8, "Kind": 1, "C": [1000, 0, 0], "P": [100, 50, 25] },
	  { "Id": 30, "Map": 906, "Kind": 2, "C": [0, 0, 100], "P": [40, 30, 0] }
	]`))
	v := func(x, y, z float32) world.Vec3 { return world.Vec3{X: x, Y: y, Z: z} }
	sphere, box, cyl := rt.Find(10), rt.Find(20), rt.Find(30)
	if rt.Count() != 3 || !sphere.Contains(v(130, 200, 300)) || !sphere.Contains(v(150, 200, 300)) || sphere.Contains(v(151, 200, 300)) ||
		!box.Contains(v(1090, 40, 20)) || box.Contains(v(1101, 0, 0)) || box.Contains(v(1000, 0, 30)) ||
		!cyl.Contains(v(30, 20, 125)) || cyl.Contains(v(45, 0, 100)) || cyl.Contains(v(0, 0, 135)) {
		t.Fatal("shapes")
	}
	if in, known := rt.InAny(8, []int64{10, 20}, v(100, 200, 300)); !in || !known {
		t.Fatal("in")
	}
	if in, known := rt.InAny(906, []int64{10, 20}, v(100, 200, 300)); in || !known {
		t.Fatal("other map")
	}
	if _, known := rt.InAny(8, []int64{999}, v(0, 0, 0)); known {
		t.Fatal("unknown")
	}
}

func TestStoryRowMatchesRbMainColumnsAndSanitizesControlChars(t *testing.T) {
	q := &StoryQuestReport{ID: 139000029, Name: "撞金\t止行\n阵", Kind: 1, Outcome: StoryFinished, AcceptedAt: intp(111), FinishedAt: intp(149)}
	q.Dungeons.WriteString("1501:enter 1501:success")
	q.Drove.WriteString("87:139000081=client")
	cols := strings.Split(FormatStoryRow(q), "\t")
	want := []string{"139000029", "撞金 止行 阵", "1", "finished", "111", "149", "0", "1501:enter 1501:success", "", "", "87:139000081=client"}
	if !slices.Equal(cols, want) || len(strings.Split(StoryHeader, "\t")) != 11 {
		t.Fatalf("%q", cols)
	}
	u := strings.Split(FormatStoryRow(&StoryQuestReport{ID: 1, Name: "x", Outcome: StoryStuck}), "\t")
	if u[4] != "null" || u[5] != "null" || u[3] != "stuck" {
		t.Fatal(u)
	}
}

func TestDungeonRowMatchesRbMainColumnsAndLevelPlayBraces(t *testing.T) {
	e := strings.Split(FormatDungeonRow(&DungeonReport{Dungeon: 9315, Outcome: DungeonNoLevelPlay, LevelExpected: 90, LeftOK: true}), "\t")
	if len(e) != 15 || len(strings.Split(DungeonHeader, "\t")) != 15 || e[0] != "9315" || e[1] != "no_level_play" || e[2] != "{  }" || e[5] != "0-0" || e[6] != "90" || e[7] != "false" || e[8] != "true" {
		t.Fatal(e)
	}
	ok := &DungeonReport{Dungeon: 8664, Outcome: DungeonSuccess, Iterations: 7, Monsters: 3, LevelMin: 80, LevelMax: 85, LevelExpected: 80, RewardOK: true, LeftOK: true, ClockSkips: 1,
		LevelPlays: []int32{224750000, 224750002}}
	o := strings.Split(FormatDungeonRow(ok), "\t")
	if o[2] != "{ 224750000, 224750002 }" || o[5] != "80-85" || o[7] != "true" {
		t.Fatal(o)
	}
}

func TestWriteStoryReportEmitsTsvAndGateCounterLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_story.tsv")
	q := &StoryQuestReport{ID: 5, Name: "测试", Kind: 1, Outcome: StoryFinished, AcceptedAt: intp(0), FinishedAt: intp(3)}
	c := &StorySummaryCounters{Warnings: 2, HideHidden: 10, HideEntities: 200, HideShown: 6, HideReleased: 4, HideRemoved: 15,
		ItemsGiven: 5, ItemsTaken: 3, OptionsAdded: 4, OptionsRemoved: 1, OptionsReleased: 3, OptionsPicked: 2, HandIns: 1,
		PresAudio: 9, PresGuests: 2, PresBubbles: 7, PresBubblesCleared: 1, PresTimeLocks: 3, PresWeather: 1, PresSwitchedOff: 4, PresSwitchedOn: 4, PresUndoneAtEnd: 5,
		AreaStatesChanged: 2, EntitiesMoved: 3, VehiclesBoarded: 1, VehiclesLeft: 1, TeleportsSent: 11, TeleportsHeld: 2}
	var out bytes.Buffer
	if err := WriteStoryReport([]*StoryQuestReport{q}, c, path, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 || lines[0] != StoryHeader || !strings.HasPrefix(lines[1], "5\t测试\t1\tfinished\t0\t3\t0\t") {
		t.Fatal(lines)
	}
	text := out.String()
	numLabel := regexp.MustCompile(`(\d+) ([a-z][a-z ]*[a-z])`)
	for _, label := range []string{"hide groups", "quest items", "presentation", "quest options", "hand-ins", "area states changed", "vehicles", "quest teleports"} {
		var line string
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, "robot: story: "+label+":") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatal(label)
		}
		nums := strings.SplitN(line, ":", 4)[3]
		if len(numLabel.FindAllString(nums, -1)) == 0 && label != "area states changed" && label != "hand-ins" {
			t.Fatal(line)
		}
	}
	if !strings.Contains(text, "robot: story: 1 quests finished (1 without skips), 0 not, 2 warnings; unhandled requests: none") {
		t.Fatal(text)
	}
}

func TestWriteDungeonReportWritesHeaderAndSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_dungeons.tsv")
	r1 := &DungeonReport{Dungeon: 3166, Outcome: DungeonSuccess, Iterations: 3, Monsters: 1, LevelMin: 65, LevelMax: 65, LevelExpected: 65, RewardOK: true, LeftOK: true}
	r2 := &DungeonReport{Dungeon: 9864, Outcome: DungeonStuck, Iterations: 6, StuckAt: "229000041:14:CompareVar"}
	var out bytes.Buffer
	if err := WriteDungeonReport([]*DungeonReport{r1, r2}, path, 2, 5, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 || lines[0] != DungeonHeader ||
		!strings.Contains(out.String(), "robot: 1 success, 0 failed, 1 stuck, 0 ended at start, 0 enter failed, 0 no level play, 0 with wrong monster level (5 s)") ||
		!strings.Contains(out.String(), "robot: battle steps judged: 2") {
		t.Fatal(out.String())
	}
}
