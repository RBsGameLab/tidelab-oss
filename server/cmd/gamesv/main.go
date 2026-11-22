// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// tidelab gamesv: the KCP game gateway (specs/network.md) and the world (R-144e). The quests
// and systems of R-143h come with R-144f; until then the world runs as the C# server did
// without quest data.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/gateway"
	"tidelab/internal/panel"
	"tidelab/internal/panel/web"
	"tidelab/internal/quests"
	"tidelab/internal/wire"
	"tidelab/internal/world"
)

func main() {
	common.LogClock = time.Now // R-166: tl_audit.py cuts the log to a play session by these stamps
	os.Exit(run(os.Args[1:]))
}

// firstFile is the first directory's file of that name, "" when none has it.
func firstFile(dirs []string, name string) string {
	for _, d := range dirs {
		if p := filepath.Join(d, name); common.FileExists(p) {
			return p
		}
	}
	return ""
}

func run(args []string) int {
	common.Info("init", common.Banner("gamesv"))
	common.Info("init", "memory: "+common.TuneMemory())
	opts, err := common.ParseOptions(args, map[string]string{
		"listen_address": "127.0.0.1:13100",
		"state_dir":      "state",
		// The game data: the client's tables, then further directories (';'-separated) for the
		// tables not in its ConfigDB (specs/data-tables.md §6), and tidelab's own cache.
		"bindata":      filepath.Join("assets", "BinData"),
		"more_bindata": "",
		"bin_cache":    filepath.Join("assets", "BinCache-tidelab"),
		"quest_data":   filepath.Join("assets", "quest"),
		"cmd_file":     filepath.Join("assets", "scripts", "dbg", "cmd.txt"),
		// R-160's content panel page ("": none; specs/panel.md §3).
		"panel_address": "127.0.0.1:13180",
		"panel_names":   "",
	})
	if err != nil {
		common.Error("init", err.Error())
		return 2
	}
	listen, err := opts.Endpoint("listen_address")
	if err != nil {
		common.Error("init", err.Error())
		return 2
	}
	keyPath := filepath.Join(opts.Get("state_dir"), "gateway", "client_public_key.der")
	der, err := os.ReadFile(keyPath)
	var keys *wire.KeyWrapper
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// R-173b: no key, sessions run unencrypted (ProtoKeyResponse Type 0; needs the patched client).
		err = nil
		common.Info("init", "no "+keyPath+": gateway sessions unencrypted (Type 0, patched client only)")
	case err == nil:
		keys, err = wire.KeyWrapperFromDER(der)
	}
	if err != nil {
		common.Error("init", "game server could not read "+keyPath+": "+err.Error())
		return 1
	}

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
		common.Error("init", "game server could not load the data tables: "+err.Error())
		return 1
	}
	fromCache := 0
	for _, l := range tables.Loads {
		if l.FromCache {
			fromCache++
		}
	}
	common.Info("init", fmt.Sprintf("data tables loaded in %d ms (%d tables, %d from the cache)", time.Since(started).Milliseconds(), len(tables.Loads), fromCache))
	var roads map[int32][]int32
	if f := firstFile(dirs, "AutoPilotCircles.json"); f != "" {
		if roads, err = world.ReadRoads(f); err != nil {
			common.Error("init", "game server could not load the data tables: "+err.Error())
			return 1
		}
	}
	wd := world.NewWorldData(tables, roads)
	wd.JSONDirs = dirs
	wd.Panel = panel.Open(opts.Get("state_dir"))
	if missing := wd.MissingEntityTypes(); len(missing) > 0 {
		common.Warn("init", "BlueprintConfig.EntityType has none of "+strings.Join(missing, ", ")+": the spawner matches nothing by these names")
	}
	shared := combat.NewShared(tables, firstFile(dirs, "CommonParam.json"))

	var questCatalog *quests.Catalog
	questsFile := filepath.Join(opts.Get("quest_data"), "Quests.json")
	if common.FileExists(questsFile) {
		var load quests.CacheLoad
		questCatalog, load = quests.LoadCatalogCached(opts.Get("quest_data"), opts.Get("bin_cache"))
		from := "the JSON"
		if load.FromCache {
			from = "the cache"
		}
		common.Info("init", fmt.Sprintf("quest data loaded from %s in %d ms: %d quests, %d level plays", from, load.Time.Milliseconds(), len(questCatalog.Quests), len(questCatalog.LevelPlays)))
	} else {
		common.Warn("init", "no quest data at "+questsFile+": the world runs without quests")
	}

	host, err := gateway.BindUDP(listen)
	if err != nil {
		common.Error("init", "game server could not start: "+err.Error())
		return 1
	}
	defer host.Close()
	g := game.NewWorldGame(opts.Get("state_dir"), wd, common.NewGameClock(time.Now), shared)
	g.CommandFile = opts.Get("cmd_file")
	if questCatalog != nil {
		g.EnableQuests(questCatalog)
	}
	if addr := opts.Get("panel_address"); addr != "" {
		nameDirs := []string{
			opts.Get("panel_names"),
			filepath.Join(filepath.Dir(opts.Get("bindata")), "panel"),
			opts.Get("bindata"),
		}
		nameDirs = append(nameDirs, more...)
		p := &web.Panel{Data: wd, Names: web.LoadNames(nameDirs...), Now: g.Clock.Seconds}
		if questCatalog != nil {
			p.Quests = questCatalog.Data
		}
		if err := p.Serve(addr); err != nil {
			common.Warn("init", "content panel could not start: "+err.Error())
		}
	}
	gw := gateway.New(gateway.NewPlayers(opts.Get("state_dir")), keys, gateway.SystemClock(), host.Send, g)
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; close(stop) }()
	live, held := common.TrimMemory()
	common.Info("init", fmt.Sprintf("memory after start-up: %d MB live, %d MB held from the system", live, held))
	common.Info("init", "game server is listening at "+host.LocalAddr().String())
	host.Run(gw, stop)
	gw.CloseAll("server shutting down")
	return 0
}
