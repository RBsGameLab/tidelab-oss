// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/data"
)

func writeQuestFiles(t *testing.T, dir string, quests string) {
	t.Helper()
	for _, f := range questFiles {
		body := "[]"
		if f == "TreeVars.json" {
			body = `{"Quests":{},"LevelPlays":{}}`
		}
		if f == "Quests.json" {
			body = quests
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// R-151: the first start reads the JSON and writes the cache, the next reads the cache and gets
// the same data; a changed file reads the JSON again; a damaged cache is a warning, not a crash.
func TestTheQuestDataComesFromItsCacheUntilAFileChanges(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	writeQuestFiles(t, dir, `[{"Id":7,"Name":"a","Nodes":[{"Id":1,"Type":"ChildQuest","Enter":[{"Name":"SetEntityState","Params":{"EntityState":"Open"},"Raw":{"x":[1,2]}}]}]}]`)
	first, load := LoadDataSetCached(dir, cache)
	if load.FromCache || len(first.Quests) != 1 {
		t.Fatal("first load", load, len(first.Quests))
	}
	again, load := LoadDataSetCached(dir, cache)
	if !load.FromCache {
		t.Fatal("second load not from the cache")
	}
	if d := data.GoDifferences(LoadDataSet(dir), again, 5); len(d) > 0 {
		t.Fatal(d)
	}
	if d := data.GoDifferences(LoadDataSet(dir), first, 5); len(d) > 0 {
		t.Fatal(d)
	}

	writeQuestFiles(t, dir, `[{"Id":8,"Name":"b"}]`)
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(dir, "Quests.json"), later, later)
	changed, load := LoadDataSetCached(dir, cache)
	if load.FromCache || changed.Quests[0].ID != 8 {
		t.Fatal("a changed file still read the old cache")
	}

	path := filepath.Join(cache, QuestCacheFile)
	b, _ := os.ReadFile(path)
	os.WriteFile(path, b[:len(b)-3], 0o644)
	damaged, load := LoadDataSetCached(dir, cache)
	if load.FromCache || load.Problem == "" || damaged.Quests[0].ID != 8 {
		t.Fatal("a damaged cache", load)
	}
	if _, load := LoadDataSetCached(dir, cache); !load.FromCache {
		t.Fatal("the damaged cache was not written again")
	}
	if _, load := LoadDataSetCached(dir, ""); load.FromCache {
		t.Fatal("no cache directory, no cache")
	}
}

// The lab's 3.8 quest data, when this machine has it: read from the cache it is the data the
// JSON gives, field by field.
func TestLab38QuestDataFromTheCacheIsTheJSONs(t *testing.T) {
	g := common.GameDataDir()
	dir := ""
	if g != "" {
		dir = filepath.Join(g, "questgen", "assets", "quest")
	}
	if dir == "" || !common.FileExists(filepath.Join(dir, "Quests.json")) {
		t.Skip("no 3.8 quest data here")
	}
	cache := t.TempDir()
	first, _ := LoadDataSetCached(dir, cache)
	fromCache, load := LoadDataSetCached(dir, cache)
	if !load.FromCache {
		t.Fatal("not from the cache")
	}
	fromJSON := LoadDataSet(dir)
	for _, got := range []*QuestDataSet{first, fromCache} {
		if d := data.GoDifferences(fromJSON, got, 5); len(d) > 0 {
			t.Fatal(d)
		}
	}
	if len(fromCache.Quests) < 1000 {
		t.Fatal(len(fromCache.Quests), "quests")
	}
}
