// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/data"
)

// The quest data's cache (R-151): LoadDataSet's result written in the tables' cache encoding
// (internal/data, data.GoCodec) to <cache dir>/QuestData.bin. Read back, it is the same data
// (reflect.DeepEqual) without parsing the 25 MB of JSON into a tree first, which left about
// 38 MB of holes among the quest data that the heap could not hand back (R-147b).

// questFiles are the files LoadDataSet reads: the cache is current while none of them changes.
var questFiles = []string{
	"Quests.json", "LevelPlays.json", "WorldPlays.json", "QuestOffers.json", "TreeVars.json",
	"EntityGlobals.json", "QuestOwned.json", "FlowActions.json", "DungeonPlays.json", "Prefabs.json",
	"TowerFloors.json", "HideGroups.json",
}

// QuestCacheFile is the cache's name in the cache directory.
const QuestCacheFile = "QuestData.bin"

var dataSetCodec = data.GoCodecOf(reflect.TypeFor[QuestDataSet]())

// questSignature is the files' names, sizes and modification times (data.Signature), false when
// one is missing (then LoadDataSet logs it and the data is not cached).
func questSignature(dir string) (uint64, bool) {
	h := uint64(0xcbf29ce484222325)
	for _, f := range questFiles {
		sig, err := data.Signature(filepath.Join(dir, f))
		if err != nil {
			return 0, false
		}
		h = data.Fnv(binary.LittleEndian.AppendUint64(nil, sig), h)
	}
	return h, true
}

// CacheLoad is how the quest data was loaded.
type CacheLoad struct {
	FromCache bool
	Time      time.Duration
	// Problem is why a cache that was there could not be read ("" when there was none, or it
	// was only out of date).
	Problem string
}

// LoadDataSetCached is LoadDataSet through the cache in cacheDir: read from it when it is
// current, otherwise from the JSON, and then written for the next start. cacheDir "" is no cache.
func LoadDataSetCached(dir, cacheDir string) (*QuestDataSet, CacheLoad) {
	start := time.Now()
	sig, ok := questSignature(dir)
	if cacheDir == "" || !ok {
		return LoadDataSet(dir), CacheLoad{Time: time.Since(start)}
	}
	path := filepath.Join(cacheDir, QuestCacheFile)
	d, problem := ReadDataSetCache(path, sig)
	if d != nil {
		common.Info("quest", fmt.Sprintf("quest data: %d quests, %d level plays, %d world plays, %d offers, %d quest-owned entities (from %s)",
			len(d.Quests), len(d.LevelPlays), len(d.WorldPlays), len(d.Offers), len(d.Owned), path))
		return d, CacheLoad{FromCache: true, Time: time.Since(start)}
	}
	if problem != "" {
		common.Warn("quest", fmt.Sprintf("%s: %s, reading the JSON", path, problem))
	}
	d = LoadDataSet(dir)
	b := EncodeDataSet(sig, d)
	if err := data.WriteFileAtomic(path, b); err != nil {
		common.Warn("quest", fmt.Sprintf("%s: writing the cache failed: %v", path, err))
	}
	// R-150: the data as the cache gives it, as every later start has it (the JSON's tree left
	// holes among it).
	if again, _ := readDataSetBytes(b, sig); again != nil {
		d = again
	}
	return d, CacheLoad{Time: time.Since(start), Problem: problem}
}

// ReadDataSetCache is the cached quest data, or nil (with the reason, unless there is no cache
// or it is out of date).
func ReadDataSetCache(path string, sig uint64) (d *QuestDataSet, problem string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	return readDataSetBytes(b, sig)
}

func readDataSetBytes(b []byte, sig uint64) (d *QuestDataSet, problem string) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*data.CorruptError)
			if !ok {
				panic(r)
			}
			d, problem = nil, "cache unreadable ("+e.Msg+")"
		}
	}()
	r := data.NewReader(b, 0, len(b))
	h, ok := r.Header()
	if !ok || h.Layout != dataSetCodec.LayoutHash() || h.Source != sig || h.Rows != 1 {
		return nil, ""
	}
	d = &QuestDataSet{}
	dataSetCodec.Read(r, d)
	if !r.AtEnd() {
		return nil, "cache longer than its data"
	}
	return d, ""
}

// EncodeDataSet is the cache file of d.
func EncodeDataSet(sig uint64, d *QuestDataSet) []byte {
	w := data.NewWriter()
	w.Header(data.CacheHeader{Layout: dataSetCodec.LayoutHash(), Source: sig, Rows: 1})
	dataSetCodec.Write(w, d)
	return w.Bytes()
}

// LoadCatalogCached is LoadCatalog through the quest data's cache in cacheDir.
func LoadCatalogCached(dir, cacheDir string) (*Catalog, CacheLoad) {
	d, load := LoadDataSetCached(dir, cacheDir)
	return NewCatalog(d), load
}
