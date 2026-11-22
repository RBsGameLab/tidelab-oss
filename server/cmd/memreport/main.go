// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// tidelab memreport (R-147): what the game server's memory is spent on. It runs gamesv's start-up
// steps (tables, world data, combat, quests, the game) without opening a port, prints the Go
// heap after each step, and writes a heap profile for `go tool pprof`.
//
//	go run ./cmd/memreport --bindata <BinData> --more_bindata <BinData-beta> --bin_cache <dir>
//	    --quest_data <quest dir> --state_dir <state> [--profile heap.pprof]
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/quests"
	"tidelab/internal/world"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

type meter struct {
	last uint64
}

// step prints the live heap after a full GC and how much the step added.
func (m *meter) step(name string) {
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	fmt.Printf("%-28s heap %5d MB (%+5d MB)  in use %5d MB  from the system %5d MB  objects %d\n",
		name, ms.HeapAlloc>>20, (int64(ms.HeapAlloc)-int64(m.last))>>20, ms.HeapInuse>>20, ms.Sys>>20, ms.HeapObjects)
	m.last = ms.HeapAlloc
}

func run(args []string) int {
	common.LogOutput = os.Stderr
	opts, err := common.ParseOptions(args, map[string]string{
		"state_dir":    "state",
		"bindata":      filepath.Join("assets", "BinData"),
		"more_bindata": "",
		"bin_cache":    filepath.Join("assets", "BinCache-tidelab"),
		"quest_data":   filepath.Join("assets", "quest"),
		"profile":      "heap.pprof",
		// Seconds to stay alive at the end, so the working set can be read from outside.
		"wait": "0",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println("memory:", common.TuneMemory())
	var m meter
	m.step("start")

	var more []string
	for _, d := range strings.Split(opts.Get("more_bindata"), ";") {
		if d = strings.TrimSpace(d); d != "" {
			more = append(more, d)
		}
	}
	dirs := append([]string{opts.Get("bindata")}, more...)
	started := time.Now()
	tables, err := data.Load(data.Source{JSONDir: opts.Get("bindata"), CacheDir: opts.Get("bin_cache"), UseCache: true, MoreJSONDirs: more}, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	m.step(fmt.Sprintf("tables (%d, %d ms)", len(tables.Loads), time.Since(started).Milliseconds()))

	var roads map[int32][]int32
	for _, d := range dirs {
		if p := filepath.Join(d, "AutoPilotCircles.json"); common.FileExists(p) {
			if roads, err = world.ReadRoads(p); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			break
		}
	}
	wd := world.NewWorldData(tables, roads)
	wd.JSONDirs = dirs
	m.step("world data")

	commonParam := ""
	for _, d := range dirs {
		if p := filepath.Join(d, "CommonParam.json"); common.FileExists(p) {
			commonParam = p
			break
		}
	}
	shared := combat.NewShared(tables, commonParam)
	m.step("combat")

	var catalog *quests.Catalog
	if common.FileExists(filepath.Join(opts.Get("quest_data"), "Quests.json")) {
		var load quests.CacheLoad
		catalog, load = quests.LoadCatalogCached(opts.Get("quest_data"), opts.Get("bin_cache"))
		from := "JSON"
		if load.FromCache {
			from = "cache"
		}
		m.step(fmt.Sprintf("quests (%d, %s, %d ms)", len(catalog.Quests), from, load.Time.Milliseconds()))
	}

	g := game.NewWorldGame(opts.Get("state_dir"), wd, common.NewGameClock(time.Now), shared)
	if catalog != nil {
		g.EnableQuests(catalog)
	}
	m.step("game")
	live, held := common.TrimMemory()
	fmt.Printf("%-28s heap %5d MB, held from the system %d MB\n", "after TrimMemory", live, held)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Printf("  heap in use %d MB, idle %d MB (released %d MB), stacks %d MB, GC metadata %d MB, other %d MB, objects %d\n",
		ms.HeapInuse>>20, ms.HeapIdle>>20, ms.HeapReleased>>20, ms.StackSys>>20, ms.GCSys>>20,
		(ms.OtherSys+ms.MSpanSys+ms.MCacheSys+ms.BuckHashSys)>>20, ms.HeapObjects)

	f, err := os.Create(opts.Get("profile"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("heap profile: %s (go tool pprof -top -sample_index=inuse_space %s)\n", opts.Get("profile"), opts.Get("profile"))
	if w := opts.Get("wait"); w != "0" {
		var s int
		fmt.Sscan(w, &s)
		fmt.Printf("waiting %d s (pid %d)\n", s, os.Getpid())
		time.Sleep(time.Duration(s) * time.Second)
	}
	// Keep everything reachable until the profile is written.
	runtime.KeepAlive(tables)
	runtime.KeepAlive(wd)
	runtime.KeepAlive(shared)
	runtime.KeepAlive(catalog)
	runtime.KeepAlive(g)
	return 0
}
