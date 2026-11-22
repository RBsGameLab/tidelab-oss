// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package panel

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// R-160: the panel's saved overrides (specs/panel.md §1).

func TestOverridesAreSavedAndReadBack(t *testing.T) {
	dir := t.TempDir()
	o := Open(dir)
	if _, ok := o.Activity("2"); ok {
		t.Fatal("a new state has no overrides")
	}
	if err := o.SetActivity("2", Activity{Edition: 100200001, Begin: 100, End: 200}, "a"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetActivity("7.2", Activity{Off: true}, "b"); err != nil {
		t.Fatal(err)
	}
	o2 := Open(dir)
	if a, ok := o2.Activity("2"); !ok || a != (Activity{Edition: 100200001, Begin: 100, End: 200}) {
		t.Fatal(a)
	}
	if a, ok := o2.Activity("7.2"); !ok || !a.Off {
		t.Fatal(a)
	}
	if err := o2.ResetActivity("2", "c"); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(dir).Activity("2"); ok {
		t.Fatal("reset is saved")
	}
}

func TestBadLinesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "panel"), 0o755)
	os.WriteFile(filepath.Join(dir, "panel", "activities"), []byte("2 0 100200001 0 0\n20 2 0 0 0\n15 0 x 0 0\n27 0 0 200 100\n34 0 0 100\n26 0 0 0 0\n"), 0o644)
	o := Open(dir)
	keys := []string{}
	for k := range o.Activities() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	eq(t, keys, []string{"2", "26"})
}

func TestBadOverridesAreRefused(t *testing.T) {
	o := Open(t.TempDir())
	for _, a := range []Activity{{Begin: 200, End: 100}, {Begin: 100}, {Edition: -1}} {
		if o.SetActivity("2", a, "") == nil {
			t.Fatal(a)
		}
	}
	if o.SetActivity("a b", Activity{}, "") == nil || o.SetActivity("", Activity{}, "") == nil {
		t.Fatal("bad key")
	}
}

func TestTheChangeLogIsReadNewestFirstInUTC8(t *testing.T) {
	o := Open(t.TempDir())
	at := time.Date(2026, 11, 21, 6, 30, 0, 0, time.UTC)
	o.Now = func() time.Time { return at }
	o.SetActivity("2", Activity{Off: true}, "first")
	at = at.Add(time.Minute)
	o.ResetActivity("2", "second\nline")
	eq(t, o.Changes(5), []string{"2026-11-21 14:31 second line", "2026-11-21 14:30 first"})
	eq(t, o.Changes(1), []string{"2026-11-21 14:31 second line"})
}

func TestNoOverridesIsNil(t *testing.T) {
	var o *Overrides
	if _, ok := o.Activity("2"); ok || o.Activities() != nil || o.Changes(3) != nil {
		t.Fatal("nil has none")
	}
}

func eq[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGoodAndGachaOverridesAreSavedAndReadBack(t *testing.T) {
	dir := t.TempDir()
	o := Open(dir)
	if err := o.SetGood(400050001, Good{Price: 100, Limit: Auto, Update: 2}, "a"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetGacha(GachaRole, 100088, "b"); err != nil {
		t.Fatal(err)
	}
	o2 := Open(dir)
	if g, ok := o2.Good(400050001); !ok || g != (Good{Price: 100, Limit: Auto, Update: 2}) || len(o2.Goods()) != 1 {
		t.Fatal(g)
	}
	if id, ok := o2.Gacha(GachaRole); !ok || id != 100088 {
		t.Fatal(id)
	}
	if _, ok := o2.Gacha(GachaWeapon); ok {
		t.Fatal("weapon slot closed")
	}
	o2.ResetGood(400050001, "c")
	o2.ResetGacha(GachaRole, "d")
	if _, ok := Open(dir).Good(400050001); ok {
		t.Fatal("reset good")
	}
	if _, ok := Open(dir).Gacha(GachaRole); ok {
		t.Fatal("reset gacha")
	}
}

func TestBadGoodAndGachaOverridesAreRefused(t *testing.T) {
	o := Open(t.TempDir())
	for _, g := range []Good{{Price: 0, Limit: Auto, Update: Auto}, {Price: Auto, Limit: -2, Update: Auto}, {Price: Auto, Limit: Auto, Update: 5}} {
		if o.SetGood(1, g, "") == nil {
			t.Fatal(g)
		}
	}
	if o.SetGood(0, Good{Price: Auto, Limit: Auto, Update: Auto}, "") == nil || o.SetGacha("other", 1, "") == nil || o.SetGacha(GachaRole, 0, "") == nil {
		t.Fatal("bad key")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "panel"), 0o755)
	os.WriteFile(filepath.Join(dir, "panel", "pay_goods"), []byte("1 0 -1 -1 -1\n2 0 0 -1 -1\n3 x 1 1 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "panel", "gacha"), []byte("role 100001\nweapon x\n"), 0o644)
	o = Open(dir)
	if len(o.Goods()) != 1 {
		t.Fatal(o.Goods())
	}
	if _, ok := o.Gacha(GachaWeapon); ok {
		t.Fatal("bad line read")
	}
}

func TestTowerSeasonAndSwapsAreSavedAndReadBack(t *testing.T) {
	dir := t.TempDir()
	o := Open(dir)
	if _, ok := o.TowerSeason(); ok {
		t.Fatal("no season yet")
	}
	if err := o.SetTowerSeason(40, "a"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetTowerSwap(471, 1, "Monster_Branch2.2_007", TowerSwap{401700012, 1700009}, "b"); err != nil {
		t.Fatal(err)
	}
	o2 := Open(dir)
	if s, ok := o2.TowerSeason(); !ok || s != 40 {
		t.Fatal(s)
	}
	if s, ok := o2.TowerSwap(471, 1, "Monster_Branch2.2_007"); !ok || s != (TowerSwap{401700012, 1700009}) {
		t.Fatal(s)
	}
	if _, ok := o2.TowerSwap(471, 0, "Monster_Branch2.2_007"); ok {
		t.Fatal("other wave")
	}
	o2.ResetTowerSeason("c")
	o2.ResetTowerSwap(471, 1, "Monster_Branch2.2_007", "d")
	o3 := Open(dir)
	if _, ok := o3.TowerSeason(); ok {
		t.Fatal("season reset")
	}
	if _, ok := o3.TowerSwap(471, 1, "Monster_Branch2.2_007"); ok {
		t.Fatal("swap reset")
	}
	for _, bad := range []error{o.SetTowerSeason(0, ""), o.SetTowerSwap(0, 0, "M", TowerSwap{1, 1}, ""), o.SetTowerSwap(1, -1, "M", TowerSwap{1, 1}, ""),
		o.SetTowerSwap(1, 0, "a/b", TowerSwap{1, 1}, ""), o.SetTowerSwap(1, 0, "M", TowerSwap{0, 1}, "")} {
		if bad == nil {
			t.Fatal("bad tower override accepted")
		}
	}
}
