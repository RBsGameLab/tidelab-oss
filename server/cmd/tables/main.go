// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// tables loads the config tables (R-144d) and reports how: from the JSON or the cache, how
// long, and with --verify whether the cache and the JSON give the same rows (R-29's check).
// --same_bytes <dir> compares every cache file with another cache directory byte for byte
// (the C# loader's, to show both write the same files).
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
)

func main() {
	opts, err := common.ParseOptions(os.Args[1:], map[string]string{
		"bindata": filepath.Join("assets", "BinData"), "more_bindata": "", "bin_cache": filepath.Join("assets", "BinCache-tidelab"),
		"verify": "false", "same_bytes": "",
	})
	if err != nil {
		common.Error("init", err.Error())
		os.Exit(2)
	}
	var more []string
	for _, m := range strings.Split(opts.Get("more_bindata"), ";") {
		if m = strings.TrimSpace(m); m != "" {
			more = append(more, m)
		}
	}
	src := data.Source{JSONDir: opts.Get("bindata"), CacheDir: opts.Get("bin_cache"), UseCache: true, MoreJSONDirs: more}
	t, err := data.Load(src, 0)
	if err != nil {
		common.Error("data", err.Error())
		os.Exit(1)
	}
	cached, rows := 0, 0
	for _, l := range t.Loads {
		rows += l.Rows
		if l.FromCache {
			cached++
		}
	}
	fmt.Printf("data tables loaded in %d ms (%d tables, %d from the cache, %d rows)\n", t.LoadTime.Milliseconds(), len(t.Loads), cached, rows)
	failed := false
	if opts.Get("verify") == "true" {
		src.UseCache = false
		fresh, err := data.Load(src, 0)
		if err != nil {
			common.Error("data", err.Error())
			os.Exit(1)
		}
		differ := 0
		for _, d := range data.Compare(t, fresh, 5) {
			if len(d.Differences) > 0 {
				differ++
				fmt.Println(strings.Join(d.Differences, "\n"))
			}
		}
		fmt.Printf("cache check: %d tables differ\n", differ)
		failed = failed || differ > 0
	}
	if other := opts.Get("same_bytes"); other != "" {
		same, differ, missing := 0, 0, 0
		for _, name := range data.Names {
			a, errA := os.ReadFile(filepath.Join(src.CacheDir, name+".bin"))
			b, errB := os.ReadFile(filepath.Join(other, name+".bin"))
			switch {
			case errA != nil || errB != nil:
				missing++
				fmt.Printf("%s: missing (%v %v)\n", name, errA, errB)
			case bytes.Equal(a, b):
				same++
			default:
				differ++
				fmt.Printf("%s: %d vs %d bytes, differ\n", name, len(a), len(b))
			}
		}
		fmt.Printf("same bytes as %s: %d tables, %d differ, %d missing\n", other, same, differ, missing)
		failed = failed || differ > 0 || missing > 0
	}
	if failed {
		os.Exit(1)
	}
}
