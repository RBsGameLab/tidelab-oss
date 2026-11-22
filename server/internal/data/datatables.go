// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"tidelab/internal/common"
)

// OptionalTables load as no rows when their file is missing (R-174): server-only data the
// client does not ship, so a BinData exported from the client lacks them. Without
// CharacterInitInfo every role gets the autobuff fallback (specs/roles.md §5.3); without
// SkillBranchBuff there are no skill branches.
var OptionalTables = map[string]bool{"CharacterInitInfo": true, "SkillBranchBuff": true}

// slot is one table of the list: how to load, compare, describe and reindex it.
type slot struct {
	name    string
	load    func(d *DataTables, src Source) (TableLoad, error)
	compare func(a, b *DataTables, max int) []string
	layout  func() string
	reindex func(d *DataTables)
}

func tableSlot[T any, PT RowPtr[T]](name string, get func(*DataTables) []PT, set func(*DataTables, []PT)) slot {
	return slot{
		name: name,
		load: func(d *DataTables, src Source) (TableLoad, error) {
			rows, info, err := LoadTable[T, PT](name, src)
			if errors.Is(err, fs.ErrNotExist) && OptionalTables[name] {
				common.Warn("data", name+".json not found: optional table, loaded empty")
				set(d, nil)
				return TableLoad{Name: name}, nil
			}
			if err == nil {
				set(d, rows)
			}
			return info, err
		},
		compare: func(a, b *DataTables, max int) []string {
			ra, rb := get(a), get(b)
			var diffs []string
			if len(ra) != len(rb) {
				diffs = append(diffs, fmt.Sprintf("%s: %d vs %d rows", name, len(ra), len(rb)))
			}
			for i := 0; i < min(len(ra), len(rb)) && len(diffs) < max; i++ {
				diffs = append(diffs, Differences(Row(ra[i]), Row(rb[i]), fmt.Sprintf("%s[%d]", name, i), max-len(diffs))...)
			}
			return diffs
		},
		layout:  func() string { return LayoutText(PT(new(T))) },
		reindex: func(d *DataTables) { set(d, get(d)) },
	}
}

// index is an index by key in which a key that occurs twice keeps its last row (matching the
// rule; in the 3.8 data only PhantomSkill repeats keys).
func index[K comparable, T any](rows []*T, key func(*T) K) map[K]*T {
	m := make(map[K]*T, len(rows))
	for _, r := range rows {
		m[key(r)] = r
	}
	return m
}

// Load loads every table in parallel, each from its cache or its JSON, the biggest first so
// one long table does not start last; then checks the value rules.
func Load(src Source, workers int) (*DataTables, error) {
	start := time.Now()
	d := &DataTables{}
	loads := make([]TableLoad, len(slots))
	errs := make([]error, len(slots))
	order := make([]int, len(slots))
	sizes := make([]int64, len(slots))
	for i, s := range slots {
		order[i] = i
		if st, err := os.Stat(src.JSONFile(s.name)); err == nil {
			sizes[i] = st.Size()
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return sizes[order[a]] > sizes[order[b]] })
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < parallelism(workers); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				loads[i], errs[i] = slots[i].load(d, src)
			}
		}()
	}
	for _, i := range order {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if problems := CheckValues(d); len(problems) > 0 {
		return nil, &ShapeError{strings.Join(problems, "\n")}
	}
	d.Loads = loads
	d.LoadTime = time.Since(start)
	return d, nil
}

var entityLogics = []string{"Item", "Animal", "Monster", "Vehicle", "Npc", "Vision", "ClientOnly", "ServerOnly", "Custom", "SimpleCombat"}

// ValueRules are the value rules of specs/data-tables.md §4 and specs/roles.md §4 that a
// field's type does not hold by itself, by "Table.Field". A value outside its rule stops the
// load. Buff.GameAttributeID is checked against the contiguous 0..143 of EAttributeType.
var ValueRules = map[string]string{
	"BlueprintConfig.EntityLogic":     "one of " + strings.Join(entityLogics, ", "),
	"Buff.DurationPolicy":             "0-2",
	"Buff.FormationPolicy":            "0-6",
	"Buff.PeriodicInhibitionPolicy":   "0-2",
	"Buff.GameAttributeID":            "0-143",
	"Buff.StackingType":               "0-2",
	"Buff.StackDurationRefreshPolicy": "0-1",
	"Buff.StackPeriodResetPolicy":     "0-1",
	"Buff.ExtraEffectReqSetting":      "0-1",
}

// CheckValues is the rows that break ValueRules, at most three per field.
func CheckValues(t *DataTables) []string {
	var problems []string
	check := func(n int, field string, key func(int) any, ok func(int) bool, show func(int) string) {
		bad := 0
		for i := 0; i < n; i++ {
			if !ok(i) {
				if bad < 3 {
					problems = append(problems, fmt.Sprintf("%s of row %v: %s is not %s", field, key(i), show(i), ValueRules[field]))
				}
				bad++
			}
		}
	}
	isLogic := func(s string) bool {
		for _, l := range entityLogics {
			if l == s {
				return true
			}
		}
		return false
	}
	check(len(t.BlueprintConfig), "BlueprintConfig.EntityLogic", func(i int) any { return t.BlueprintConfig[i].Id },
		func(i int) bool { return isLogic(t.BlueprintConfig[i].EntityLogic) }, func(i int) string { return `"` + t.BlueprintConfig[i].EntityLogic + `"` })
	rng := func(field string, get func(*Buff) int32, max int32) {
		check(len(t.Buff), field, func(i int) any { return fmtNumber(float64(t.Buff[i].Id)) },
			func(i int) bool { v := get(t.Buff[i]); return v >= 0 && v <= max }, func(i int) string { return fmt.Sprint(get(t.Buff[i])) })
	}
	rng("Buff.DurationPolicy", func(b *Buff) int32 { return b.DurationPolicy }, 2)
	rng("Buff.FormationPolicy", func(b *Buff) int32 { return b.FormationPolicy }, 6)
	rng("Buff.PeriodicInhibitionPolicy", func(b *Buff) int32 { return b.PeriodicInhibitionPolicy }, 2)
	rng("Buff.GameAttributeID", func(b *Buff) int32 { return b.GameAttributeID }, 143)
	rng("Buff.StackingType", func(b *Buff) int32 { return b.StackingType }, 2)
	rng("Buff.StackDurationRefreshPolicy", func(b *Buff) int32 { return b.StackDurationRefreshPolicy }, 1)
	rng("Buff.StackPeriodResetPolicy", func(b *Buff) int32 { return b.StackPeriodResetPolicy }, 1)
	rng("Buff.ExtraEffectReqSetting", func(b *Buff) int32 { return b.ExtraEffectReqSetting }, 1)
	return problems
}

func fmtNumber(f float64) string { return csharpR(f, 64) }

// TableDifferences is one table compared with another load of it.
type TableDifferences struct {
	Table       string
	Differences []string
}

// Compare compares every table with another load of it, row by row and field by field: the
// differences, at most perTable per table (R-29's --verify).
func Compare(a, b *DataTables, perTable int) []TableDifferences {
	out := make([]TableDifferences, len(slots))
	for i, s := range slots {
		out[i] = TableDifferences{s.name, s.compare(a, b, perTable)}
	}
	return out
}

// Layouts are each table's layout text, by table name.
func Layouts() map[string]string {
	m := make(map[string]string, len(slots))
	for _, s := range slots {
		m[s.name] = s.layout()
	}
	return m
}

// Reindex builds every index again from the row slices (for tables filled by hand, as the tests do).
func (d *DataTables) Reindex() *DataTables {
	for _, s := range slots {
		s.reindex(d)
	}
	return d
}

// levelEntityIndex: LevelEntityConfig's EntityId is unique only within a map (17 288 ids are
// on several). R-149: sorted arrays of row positions searched by halving, not hash maps: about
// 2.6 MB for the 217 022 rows of 3.8 instead of 12 MB.
type levelEntityIndex struct {
	// levelEntityByMapAndID: every row's position, by (MapId, EntityId, position).
	levelEntityByMapAndID []int32
	// levelEntityFirstByID: the first row's position of each EntityId, by EntityId.
	levelEntityFirstByID []int32
	// levelEntityByMap: every row's position, by (MapId, position): a map's rows are one run.
	levelEntityByMap []int32
}

func (d *DataTables) indexLevelEntityConfig() {
	rows := d.LevelEntityConfig
	all := func() []int32 {
		p := make([]int32, len(rows))
		for i := range p {
			p[i] = int32(i)
		}
		return p
	}
	byMapAndID := all()
	slices.SortFunc(byMapAndID, func(a, b int32) int {
		ra, rb := rows[a], rows[b]
		return cmp.Or(cmp.Compare(ra.MapId, rb.MapId), cmp.Compare(ra.EntityId, rb.EntityId), cmp.Compare(a, b))
	})
	byID := all()
	slices.SortFunc(byID, func(a, b int32) int {
		return cmp.Or(cmp.Compare(rows[a].EntityId, rows[b].EntityId), cmp.Compare(a, b))
	})
	firsts := byID[:0]
	for i, p := range byID {
		if i == 0 || rows[p].EntityId != rows[firsts[len(firsts)-1]].EntityId {
			firsts = append(firsts, p)
		}
	}
	byMap := all()
	slices.SortStableFunc(byMap, func(a, b int32) int { return cmp.Compare(rows[a].MapId, rows[b].MapId) })
	d.levelEntityByMapAndID, d.levelEntityFirstByID, d.levelEntityByMap = byMapAndID, slices.Clone(firsts), byMap
}

// LevelEntity is the entity's row on that map: the first row with this id and map.
func (d *DataTables) LevelEntity(mapID, id int32) *LevelEntityConfig {
	rows, idx := d.LevelEntityConfig, d.levelEntityByMapAndID
	i, found := slices.BinarySearchFunc(idx, [2]int32{mapID, id}, func(p int32, k [2]int32) int {
		return cmp.Or(cmp.Compare(rows[p].MapId, k[0]), cmp.Compare(rows[p].EntityId, k[1]))
	})
	if !found {
		return nil
	}
	return rows[idx[i]]
}

// LevelEntityAnyMap is the first row with this id, whatever its map. Asking whether an id is on
// a map through this is the R-80g bug: use LevelEntity.
func (d *DataTables) LevelEntityAnyMap(id int32) *LevelEntityConfig {
	rows, idx := d.LevelEntityConfig, d.levelEntityFirstByID
	i, found := slices.BinarySearchFunc(idx, id, func(p, k int32) int { return cmp.Compare(rows[p].EntityId, k) })
	if !found {
		return nil
	}
	return rows[idx[i]]
}

// LevelEntityRowsOfMap are the positions in LevelEntityConfig of the rows on a map, in table
// order. The slice is the index's own: do not change it.
func (d *DataTables) LevelEntityRowsOfMap(mapID int32) []int32 {
	rows, idx := d.LevelEntityConfig, d.levelEntityByMap
	from := sort.Search(len(idx), func(i int) bool { return rows[idx[i]].MapId >= mapID })
	to := from + sort.Search(len(idx)-from, func(i int) bool { return rows[idx[from+i]].MapId > mapID })
	return idx[from:to:to]
}
