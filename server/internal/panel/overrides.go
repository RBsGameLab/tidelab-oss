// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package panel is R-160's content panel store: the overrides the user set on the panel page
// over the server's automatic rules (specs/panel.md). The game reads a snapshot; the page
// writes through Set/Reset from its own goroutine.
package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tidelab/internal/common"
	"tidelab/internal/save"
)

// Activity is one activity slot's override. The zero value is "automatic"; Edition 0 keeps the
// automatic edition, Begin and End 0 keep the automatic window (both set, or neither).
type Activity struct {
	Off     bool
	Edition int32
	Begin   int64
	End     int64
}

// Window reports whether the override sets the window.
func (a Activity) Window() bool { return a.Begin != 0 || a.End != 0 }

// Auto is the value of a pay good field the panel leaves automatic.
const Auto = -1

// Good is one pay shop good's override (r160b). Price, Limit and Update are Auto (-1) when
// the panel leaves them; Limit 0 is "no limit".
type Good struct {
	Off    bool
	Price  int32
	Limit  int32
	Update int32
}

// Gacha slots (r160b): the limited resonator and weapon banners. No override: closed.
const (
	GachaRole   = "role"
	GachaWeapon = "weapon"
)

// TowerSwap is a 深塔 swap (r160c): the monster that replaces one kind in a floor's wave, as its
// tower prefab entity.
type TowerSwap struct {
	Prefab int32
	Entity int32
}

// TowerSwapKey is the key of a swap: "<floor>/<wave>/<blueprint>" (wave from 0).
func TowerSwapKey(floor int32, wave int, blueprint string) string {
	return fmt.Sprintf("%d/%d/%s", floor, wave, blueprint)
}

// Overrides are the saved overrides, in <state>/panel/. A nil *Overrides has none.
type Overrides struct {
	dir   string
	mu    sync.Mutex // writers
	acts  table[Activity]
	goods table[Good]
	gacha table[int32]
	// season: key "season", the 深境区 season listed (r160c); swaps: TowerSwapKey → TowerSwap.
	season table[int32]
	swaps  table[TowerSwap]
	// Now is the clock of the change log (nil: time.Now).
	Now func() time.Time
}

// table is one overrides file: "<key> <fields…>" lines.
type table[T any] struct {
	file   string
	parse  func(f []string) (T, bool)
	format func(T) string
	m      atomic.Pointer[map[string]T]
}

func (t *table[T]) load(dir string) {
	m := map[string]T{}
	if b, err := os.ReadFile(filepath.Join(dir, t.file)); err == nil {
		for n, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			v, ok := t.parse(f[1:])
			if !ok {
				common.Warn("panel", fmt.Sprintf("%s line %d ignored: %q", t.file, n+1, strings.TrimSpace(line)))
				continue
			}
			m[f[0]] = v
		}
	}
	t.m.Store(&m)
}

func (t *table[T]) get(key string) (T, bool) {
	v, ok := (*t.m.Load())[key]
	return v, ok
}

const changesFile = "changes.log"

// Open reads <stateDir>/panel/; a missing file has no overrides, a bad line is skipped with a
// warning.
func Open(stateDir string) *Overrides {
	o := &Overrides{dir: filepath.Join(stateDir, "panel")}
	o.acts = table[Activity]{file: "activities", parse: parseActivity, format: func(a Activity) string {
		return fmt.Sprintf("%s %d %d %d", b01(a.Off), a.Edition, a.Begin, a.End)
	}}
	o.goods = table[Good]{file: "pay_goods", parse: parseGood, format: func(g Good) string {
		return fmt.Sprintf("%s %d %d %d", b01(g.Off), g.Price, g.Limit, g.Update)
	}}
	o.gacha = table[int32]{file: "gacha", parse: parseGacha, format: func(id int32) string { return strconv.Itoa(int(id)) }}
	o.season = table[int32]{file: "tower_season", parse: parseGacha, format: func(id int32) string { return strconv.Itoa(int(id)) }}
	o.swaps = table[TowerSwap]{file: "tower_monsters", parse: parseSwap, format: func(s TowerSwap) string {
		return fmt.Sprintf("%d %d", s.Prefab, s.Entity)
	}}
	o.acts.load(o.dir)
	o.goods.load(o.dir)
	o.gacha.load(o.dir)
	o.season.load(o.dir)
	o.swaps.load(o.dir)
	return o
}

// parseSwap reads "<prefab> <entity>".
func parseSwap(f []string) (TowerSwap, bool) {
	if len(f) != 2 {
		return TowerSwap{}, false
	}
	n, ok := ints(f, 32)
	if !ok || n[0] <= 0 || n[1] <= 0 {
		return TowerSwap{}, false
	}
	return TowerSwap{int32(n[0]), int32(n[1])}, true
}

// TowerSeason is the 深境区 season the panel set (false: the newest).
func (o *Overrides) TowerSeason() (int32, bool) {
	if o == nil {
		return 0, false
	}
	return o.season.get("season")
}

func (o *Overrides) SetTowerSeason(season int32, note string) error {
	if season <= 0 {
		return fmt.Errorf("bad season %d", season)
	}
	return change(o, &o.season, func(m map[string]int32) { m["season"] = season }, note)
}

func (o *Overrides) ResetTowerSeason(note string) error {
	return change(o, &o.season, func(m map[string]int32) { delete(m, "season") }, note)
}

// TowerSwap is the swap of a kind of monster in a floor's wave (false: none).
func (o *Overrides) TowerSwap(floor int32, wave int, blueprint string) (TowerSwap, bool) {
	if o == nil {
		return TowerSwap{}, false
	}
	return o.swaps.get(TowerSwapKey(floor, wave, blueprint))
}

func (o *Overrides) SetTowerSwap(floor int32, wave int, blueprint string, s TowerSwap, note string) error {
	if floor <= 0 || wave < 0 || !validKey(blueprint) || strings.Contains(blueprint, "/") || s.Prefab <= 0 || s.Entity <= 0 {
		return fmt.Errorf("bad swap %d/%d/%q %+v", floor, wave, blueprint, s)
	}
	return change(o, &o.swaps, func(m map[string]TowerSwap) { m[TowerSwapKey(floor, wave, blueprint)] = s }, note)
}

func (o *Overrides) ResetTowerSwap(floor int32, wave int, blueprint, note string) error {
	return change(o, &o.swaps, func(m map[string]TowerSwap) { delete(m, TowerSwapKey(floor, wave, blueprint)) }, note)
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func ints(f []string, bits int) ([]int64, bool) {
	out := make([]int64, len(f))
	for i, s := range f {
		v, err := strconv.ParseInt(s, 10, bits)
		if err != nil {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// parseActivity reads "<off 0|1> <edition> <begin> <end>".
func parseActivity(f []string) (Activity, bool) {
	if len(f) != 4 || f[0] != "0" && f[0] != "1" {
		return Activity{}, false
	}
	n, ok := ints(f[1:], 64)
	if !ok || n[0] < 0 || n[0] > 1<<31-1 {
		return Activity{}, false
	}
	a := Activity{Off: f[0] == "1", Edition: int32(n[0]), Begin: n[1], End: n[2]}
	return a, ValidWindow(a)
}

// parseGood reads "<off 0|1> <price> <limit> <update>".
func parseGood(f []string) (Good, bool) {
	if len(f) != 4 || f[0] != "0" && f[0] != "1" {
		return Good{}, false
	}
	n, ok := ints(f[1:], 32)
	if !ok {
		return Good{}, false
	}
	g := Good{Off: f[0] == "1", Price: int32(n[0]), Limit: int32(n[1]), Update: int32(n[2])}
	return g, ValidGood(g)
}

// parseGacha reads "<gacha id>".
func parseGacha(f []string) (int32, bool) {
	if len(f) != 1 {
		return 0, false
	}
	n, ok := ints(f, 32)
	if !ok || n[0] <= 0 {
		return 0, false
	}
	return int32(n[0]), true
}

// ValidWindow: no window, or Begin < End, both positive.
func ValidWindow(a Activity) bool {
	return !a.Window() || a.Begin > 0 && a.End > a.Begin
}

// ValidGood: a price of at least 1, a limit of at least 0, a known period, each or Auto.
func ValidGood(g Good) bool {
	return (g.Price == Auto || g.Price >= 1) && (g.Limit == Auto || g.Limit >= 0) && (g.Update == Auto || g.Update >= 0 && g.Update <= 4)
}

func validKey(key string) bool { return key != "" && !strings.ContainsAny(key, " \t\r\n") }

// Activity is the override of a slot (false: automatic).
func (o *Overrides) Activity(key string) (Activity, bool) {
	if o == nil {
		return Activity{}, false
	}
	return o.acts.get(key)
}

// Activities are all activity overrides.
func (o *Overrides) Activities() map[string]Activity {
	if o == nil {
		return nil
	}
	return *o.acts.m.Load()
}

// SetActivity saves a slot's override and logs note to the change log.
func (o *Overrides) SetActivity(key string, a Activity, note string) error {
	if !validKey(key) || !ValidWindow(a) || a.Edition < 0 {
		return fmt.Errorf("bad override %q %+v", key, a)
	}
	return change(o, &o.acts, func(m map[string]Activity) { m[key] = a }, note)
}

// ResetActivity drops a slot's override (back to automatic).
func (o *Overrides) ResetActivity(key, note string) error {
	return change(o, &o.acts, func(m map[string]Activity) { delete(m, key) }, note)
}

// Good is the override of a pay good (false: automatic).
func (o *Overrides) Good(id int32) (Good, bool) {
	if o == nil {
		return Good{}, false
	}
	return o.goods.get(strconv.Itoa(int(id)))
}

// Goods are all pay good overrides by good id.
func (o *Overrides) Goods() map[int32]Good {
	if o == nil {
		return nil
	}
	out := map[int32]Good{}
	for k, v := range *o.goods.m.Load() {
		if id, err := strconv.Atoi(k); err == nil {
			out[int32(id)] = v
		}
	}
	return out
}

func (o *Overrides) SetGood(id int32, g Good, note string) error {
	if id <= 0 || !ValidGood(g) {
		return fmt.Errorf("bad good override %d %+v", id, g)
	}
	return change(o, &o.goods, func(m map[string]Good) { m[strconv.Itoa(int(id))] = g }, note)
}

func (o *Overrides) ResetGood(id int32, note string) error {
	return change(o, &o.goods, func(m map[string]Good) { delete(m, strconv.Itoa(int(id))) }, note)
}

// Gacha is the banner open in a gacha slot (false: the slot is closed).
func (o *Overrides) Gacha(slot string) (int32, bool) {
	if o == nil {
		return 0, false
	}
	return o.gacha.get(slot)
}

func (o *Overrides) SetGacha(slot string, gachaID int32, note string) error {
	if slot != GachaRole && slot != GachaWeapon || gachaID <= 0 {
		return fmt.Errorf("bad gacha override %q %d", slot, gachaID)
	}
	return change(o, &o.gacha, func(m map[string]int32) { m[slot] = gachaID }, note)
}

func (o *Overrides) ResetGacha(slot, note string) error {
	return change(o, &o.gacha, func(m map[string]int32) { delete(m, slot) }, note)
}

func change[T any](o *Overrides, t *table[T], edit func(map[string]T), note string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := map[string]T{}
	for k, v := range *t.m.Load() {
		m[k] = v
	}
	edit(m)
	if err := os.MkdirAll(o.dir, 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k + " " + t.format(m[k]) + "\n")
	}
	path := filepath.Join(o.dir, t.file)
	if !save.WriteAtomic(path, sb.String()) {
		return fmt.Errorf("could not write %s", path)
	}
	t.m.Store(&m)
	o.logChange(note)
	return nil
}

// UTC8 is the game's time zone: the panel shows and takes times in it.
var UTC8 = time.FixedZone("UTC+8", 8*60*60)

func (o *Overrides) logChange(note string) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	line := now().In(UTC8).Format("2006-01-02 15:04") + " " + strings.ReplaceAll(note, "\n", " ") + "\n"
	f, err := os.OpenFile(filepath.Join(o.dir, changesFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		common.Warn("panel", "change log: "+err.Error())
		return
	}
	defer f.Close()
	f.WriteString(line)
}

// Changes are the change log's last n lines, newest first.
func (o *Overrides) Changes(n int) []string {
	if o == nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(o.dir, changesFile))
	if err != nil {
		return nil
	}
	var out []string
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if lines[i] != "" {
			out = append(out, lines[i])
		}
	}
	return out
}
