// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"tidelab/internal/common"
	"tidelab/internal/data"
)

// R-174b: how far the autobuff fallback (specs/roles.md §5.3) is from CharacterInitInfo, the
// server-only table the client does not have. Real 3.8 data only (skipped without it); prints the
// per-role difference, fails nothing.
func TestRealDataAutobuffAgainstCharacterInitInfo(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData-beta", "CharacterInitInfo.json")) {
		t.Skip("no 3.8 data")
	}
	tb, err := data.Load(data.Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")},
		CacheDir: filepath.Join(t.TempDir()), UseCache: false}, 0)
	if err != nil {
		t.Fatal(err)
	}
	withTable := NewRoleBuffBuilder(tb, nil)
	stripped := *tb
	stripped.CharacterInitInfoByRoleId = map[int32]*data.CharacterInitInfo{}
	fallback := NewRoleBuffBuilder(&stripped, nil)
	same, total := 0, 0
	var lines []string
	for id := range tb.CharacterInitInfoByRoleId {
		total++
		a := map[string]bool{}
		for _, s := range withTable.RoleOwnBuffs(id) {
			a[fmt.Sprint(s)] = true
		}
		b := map[string]bool{}
		for _, s := range fallback.RoleOwnBuffs(id) {
			b[fmt.Sprint(s)] = true
		}
		var onlyTable, onlyFallback []string
		for k := range a {
			if !b[k] {
				onlyTable = append(onlyTable, k)
			}
		}
		for k := range b {
			if !a[k] {
				onlyFallback = append(onlyFallback, k)
			}
		}
		if len(onlyTable) == 0 && len(onlyFallback) == 0 {
			same++
			continue
		}
		sort.Strings(onlyTable)
		sort.Strings(onlyFallback)
		lines = append(lines, fmt.Sprintf("role %d: table %d, fallback %d; only table %v; only fallback %v", id, len(a), len(b), onlyTable, onlyFallback))
	}
	sort.Strings(lines)
	for _, l := range lines {
		t.Log(l)
	}
	t.Logf("CharacterInitInfo roles: %d, fallback identical: %d", total, same)
}
