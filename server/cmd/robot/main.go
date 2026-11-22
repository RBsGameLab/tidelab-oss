// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// robot is tidelab's robot (R-143i, R-144g): data checks, world spawn checks, and the story,
// side, dungeon and targeted self-test robots, driving the real gateway and game in memory on a
// copy of a player's save. Same command line and reports as the C# Tidelab.Robot it replaces
// (scripts/dev/gate.py runs it).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/robot"
	"tidelab/internal/wire"
	"tidelab/internal/world"
)

func main() { os.Exit(run(os.Args[1:])) }

func firstFile(dirs []string, name string) string {
	for _, d := range dirs {
		if p := filepath.Join(d, name); common.FileExists(p) {
			return p
		}
	}
	return ""
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func run(args []string) int {
	out := os.Stdout
	fmt.Fprintln(out, common.Banner("robot"))
	opt, err := robot.ParseOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "robot: %v\n", err)
		robot.PrintUsage(os.Stderr)
		return 2
	}
	var echo *os.File
	if opt.Verbose {
		echo = os.Stderr
	}
	logs := robot.NewLogCapture(nil)
	if echo != nil {
		logs = robot.NewLogCapture(echo)
	}
	common.LogOutput = logs

	fmt.Fprintln(out, "robot: loading assets...")
	src := data.Source{JSONDir: opt.BinData, CacheDir: opt.Cache, UseCache: true, MoreJSONDirs: opt.MoreBinData}
	tables, err := data.Load(src, opt.Threads)
	if err != nil {
		fmt.Fprintf(os.Stderr, "robot: %v\n", err)
		return 1
	}
	if opt.Mode == robot.ModeAssets {
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		fromCache, rows := 0, 0
		for _, l := range tables.Loads {
			rows += l.Rows
			if l.FromCache {
				fromCache++
			}
		}
		fmt.Fprintf(out, "robot: assets loaded in %d ms (%d tables, %d from the cache, %d rows); memory now %d MB managed, %d MB from the system\n",
			tables.LoadTime.Milliseconds(), len(tables.Loads), fromCache, rows, ms.HeapAlloc>>20, ms.Sys>>20)
		loads := append([]data.TableLoad(nil), tables.Loads...)
		sort.SliceStable(loads, func(i, j int) bool { return loads[i].Time > loads[j].Time })
		for i, l := range loads {
			if i >= 5 {
				break
			}
			from := "JSON"
			if l.FromCache {
				from = "cache"
			}
			fmt.Fprintf(out, "robot:   %s: %d rows in %d ms (%s)\n", l.Name, l.Rows, l.Time.Milliseconds(), from)
		}
		if !opt.Verify {
			return 0
		}
		src.UseCache = false
		fresh, err := data.Load(src, opt.Threads)
		if err != nil {
			fmt.Fprintf(os.Stderr, "robot: %v\n", err)
			return 1
		}
		bad := 0
		for _, d := range data.Compare(tables, fresh, 5) {
			if len(d.Differences) == 0 {
				continue
			}
			bad++
			fmt.Fprintf(out, "robot: %s differs:\n", d.Table)
			for _, line := range d.Differences {
				fmt.Fprintln(out, "robot:   "+line)
			}
		}
		state := "ok"
		if bad > 0 {
			state = "FAILED"
		}
		fmt.Fprintf(out, "robot: cache check %s: %d tables differ from the JSON\n", state, bad)
		// R-151: the quest data's cache against its JSON.
		if common.FileExists(filepath.Join(opt.QuestData, "Quests.json")) {
			cached, load := quests.LoadDataSetCached(opt.QuestData, opt.Cache)
			from := "the JSON (cache written)"
			if load.FromCache {
				from = "the cache"
			}
			diffs := data.GoDifferences(cached, quests.LoadDataSet(opt.QuestData), 10)
			for _, line := range diffs {
				fmt.Fprintln(out, "robot:   quest data"+line)
			}
			qstate := "ok"
			if len(diffs) > 0 {
				qstate, bad = "FAILED", bad+1
			}
			fmt.Fprintf(out, "robot: quest data check %s: %d quests from %s in %d ms, %d differences from the JSON\n",
				qstate, len(cached.Quests), from, load.Time.Milliseconds(), len(diffs))
		}
		if bad == 0 {
			return 0
		}
		return 1
	}
	fmt.Fprintf(out, "robot: assets loaded in %d ms\n", tables.LoadTime.Milliseconds())

	stateDir := "state"
	if opt.StateDir != nil {
		stateDir = *opt.StateDir
	}
	var epoch int64
	switch {
	case opt.Epoch != nil:
		epoch = *opt.Epoch
	case opt.Mode == robot.ModeNpcs:
		epoch = 1_800_000_000
	default:
		epoch = time.Now().Unix()
	}
	dirs := append([]string{opt.BinData}, opt.MoreBinData...)
	commonParam := firstFile(dirs, "CommonParam.json")

	if opt.Mode == robot.ModeNpcs {
		if opt.StateDir == nil && !dirExists(stateDir) {
			fmt.Fprintln(os.Stderr, "robot: npcs needs --state <dir>")
			return 2
		}
		var list *os.File
		if opt.NpcListPath != nil {
			if list, err = os.Create(*opt.NpcListPath); err != nil {
				fmt.Fprintf(os.Stderr, "robot: %v\n", err)
				return 1
			}
			defer list.Close()
		}
		if list != nil {
			return robot.RunNpcs(tables, stateDir, opt.PlayerID, epoch, out, list, commonParam)
		}
		return robot.RunNpcs(tables, stateDir, opt.PlayerID, epoch, out, nil, commonParam)
	}

	var roads map[int32][]int32
	if f := firstFile(dirs, "AutoPilotCircles.json"); f != "" {
		if roads, err = world.ReadRoads(f); err != nil {
			fmt.Fprintf(os.Stderr, "robot: %v\n", err)
			return 1
		}
	}
	wd := world.NewWorldData(tables, roads)
	wd.JSONDirs = dirs

	var cat *quests.Catalog
	if questsJSON := filepath.Join(opt.QuestData, "Quests.json"); common.FileExists(questsJSON) {
		cat, _ = quests.LoadCatalogCached(opt.QuestData, opt.Cache)
	} else {
		fmt.Fprintf(os.Stderr, "robot: warning: no quest data at %s\n", questsJSON)
	}
	setters := robot.LoadStateSetters(filepath.Join(opt.QuestData, "StateSetters.json"))
	ranges := robot.LoadRangeTable(filepath.Join(opt.QuestData, "Ranges.json"))
	fmt.Fprintf(out, "robot: virtual clock from epoch %d\n", epoch)

	var dungeonIDs []int32
	if opt.Mode == robot.ModeDungeons {
		dungeonIDs = robot.BuildDungeonList(tables, cat, opt)
		if len(dungeonIDs) == 0 {
			fmt.Fprintln(os.Stderr, "robot: no valid dungeon ids to run")
			return 1
		}
	}
	sourceState := stateDir
	switch {
	case opt.FromState != nil:
		sourceState = filepath.Join(opt.CheckpointDir, *opt.FromState)
	case opt.KeepState:
		sourceState = opt.RobotStateDir
	}
	if !dirExists(filepath.Join(sourceState, "player", strconv.Itoa(int(opt.PlayerID)))) {
		fmt.Fprintf(os.Stderr, "robot: no player directory under %s\n", sourceState)
		return 2
	}
	fromCheckpoint := opt.KeepState || opt.FromState != nil
	storyStart := quests.StartQuest
	if opt.StoryStartQuest != nil {
		storyStart = *opt.StoryStartQuest
	}
	var loginFrames []*wire.Frame
	loginStart := time.Now()
	cfg := robot.HostConfig{
		PlayerID: opt.PlayerID, Epoch: epoch, Tables: tables, WorldData: wd, Quests: cat, CommonParamPath: commonParam,
		OffersEnabled: opt.Mode == robot.ModeOffers || opt.WithSide, RestorePending: opt.Mode == robot.ModeRide,
		OnLoginFrame: func(f *wire.Frame) { loginFrames = append(loginFrames, f) },
	}
	host, err := robot.StartFromCopy(sourceState, cfg, func(playerDir string) {
		if fromCheckpoint {
			fmt.Fprintf(out, "robot: keeping the state in %s\n", sourceState)
			return
		}
		switch {
		case opt.Mode == robot.ModeStory && cat != nil:
			robot.PrepareFreshStory(playerDir, cat, storyStart, out)
		case opt.Mode == robot.ModeSide && cat != nil:
			robot.PrepareFreshSide(playerDir, cat, opt, out)
		case opt.Mode == robot.ModeActivity:
			robot.PrepareFreshActivityTasks(playerDir)
		case opt.Mode == robot.ModeRide:
			robot.PrepareFreshRide(playerDir)
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "robot: %v\n", err)
		return 1
	}
	defer host.Close()
	fmt.Fprintf(out, "robot: login (enter_game) took %d ms\n", time.Since(loginStart).Milliseconds())

	r := robot.NewRunner(host, setters, ranges, logs, out, opt.Verbose, opt.TryOptions)
	for _, f := range loginFrames {
		r.ProcessFrame(f)
	}
	if host.Session.Scene == nil {
		fmt.Fprintln(out, "robot: no scene after enter_game")
		return 1
	}
	fmt.Fprintf(out, "robot: in scene %d, running %d dungeons\n", r.InstanceID(), len(dungeonIDs))

	exit := 2
	func() {
		defer func() {
			if opt.KeepState {
				host.SaveCheckpoint(opt.RobotStateDir)
			}
		}()
		switch opt.Mode {
		case robot.ModeSide:
			r.RunStartupProbes(false)
			list := r.RunSide(robot.SideTypes, 3000, opt)
			fmt.Fprintf(out, "robot: side: travelled to another map %d times\n", r.Travels)
			robot.WriteStoryReport(list, r.BuildStoryCounters(), opt.ResolveReportPath(), out)
			exit = 0
		case robot.ModeOffers:
			exit = r.CheckOffers()
		case robot.ModeWorldInst:
			exit = r.CheckWorldInstances()
		case robot.ModeRide:
			exit = r.CheckRide()
		case robot.ModePos:
			exit = r.CheckEntityPos()
		case robot.ModeChairs:
			exit = r.CheckChairs()
		case robot.ModeWeapons:
			exit = r.CheckWeaponGifts()
		case robot.ModeShop:
			exit = r.CheckShop()
		case robot.ModeLanding:
			exit = r.CheckLanding()
		case robot.ModePatrol:
			exit = r.CheckPatrol()
		case robot.ModeActivity:
			exit = r.CheckActivities()
		case robot.ModeStory:
			r.RunStartupProbes(true)
			list := r.RunStory(opt.MaxQuests, opt.SkipStuck)
			ph := r.Phase
			fmt.Fprintf(out, "robot: story: travelled to another map %d times\n", r.Travels)
			fmt.Fprintf(out, "robot: story: %d requests sent: server handlers %d ms, %d pushed frames\n", ph.Sends, ph.DispatchMs, host.FramesReceived)
			fmt.Fprintf(out, "robot: story: %d rounds: steps %d ms, actions %d ms, flows+kills %d ms, moving %d ms (%d ms per round)\n",
				ph.Rounds, ph.StepsMs, ph.ActMs, ph.FlowsMs, ph.MoveMs, (ph.StepsMs+ph.ActMs+ph.FlowsMs+ph.MoveMs)/max(ph.Rounds, 1))
			if opt.SaveState != nil {
				target := filepath.Join(opt.CheckpointDir, *opt.SaveState)
				files, _ := host.SaveCheckpoint(target)
				fmt.Fprintf(out, "robot: checkpoint %s: %d files\n", target, files)
			}
			robot.WriteStoryReport(list, r.BuildStoryCounters(), opt.ResolveReportPath(), out)
			exit = 0
		case robot.ModeDungeons:
			r.RunStartupProbes(true)
			exit = r.RunDungeonsMode(dungeonIDs, opt.ResolveReportPath())
		}
	}()
	return exit
}
