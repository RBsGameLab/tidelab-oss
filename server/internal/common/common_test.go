// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package common

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var defaults = map[string]string{"listen_address": "127.0.0.1:10001", "state_dir": "state"}

func TestDefaultsApplyAndBothFormsSet(t *testing.T) {
	o, err := ParseOptions([]string{"--state_dir", "C:/x", "--listen_address=0.0.0.0:80"}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if o.Get("state_dir") != "C:/x" {
		t.Fatal(o.Get("state_dir"))
	}
	if ep, err := o.Endpoint("listen_address"); err != nil || ep.String() != "0.0.0.0:80" {
		t.Fatal(ep, err)
	}
	o, _ = ParseOptions(nil, defaults)
	if ep, err := o.Endpoint("listen_address"); err != nil || ep.String() != "127.0.0.1:10001" {
		t.Fatal(ep, err)
	}
}

func TestRefusesBadArguments(t *testing.T) {
	for _, args := range [][]string{{"--unknown", "x"}, {"--state_dir"}, {"stray"}, {"--"}} {
		if _, err := ParseOptions(args, defaults); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestRefusesAnAddressWithoutANumericHostAndPort(t *testing.T) {
	for _, text := range []string{"127.0.0.1", "localhost:80", "1.2.3.4:99999"} {
		o, _ := ParseOptions([]string{"--listen_address", text}, defaults)
		if _, err := o.Endpoint("listen_address"); err == nil {
			t.Fatalf("%s accepted", text)
		}
	}
}

func TestWriteAtomicWritesOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b")
	if wrote, err := WriteAtomic(path, "x"); !wrote || err != nil {
		t.Fatal(wrote, err)
	}
	if wrote, _ := WriteAtomic(path, "x"); wrote {
		t.Fatal("rewrote the same bytes")
	}
	if wrote, _ := WriteAtomic(path, "y"); !wrote {
		t.Fatal("did not write")
	}
	if b, _ := os.ReadFile(path); string(b) != "y" {
		t.Fatal(string(b))
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new left behind")
	}
}

func TestVirtualClockMovesOnlyWhenAdvanced(t *testing.T) {
	c := NewGameClock(nil)
	c.UseVirtual(1_795_039_200)
	if c.Seconds() != 1_795_039_200 || c.Ms() != 1_000_000_000 {
		t.Fatal(c.Seconds(), c.Ms())
	}
	c.Advance(1500)
	c.Advance(-5)
	if c.Seconds() != 1_795_039_201 || c.RealMs() != 1_795_039_201_500 {
		t.Fatal(c.Seconds(), c.RealMs())
	}
}

// R-166: gamesv stamps its lines so tl_audit.py can cut the log to a play session; without a
// clock (tests, robots) the shape stays "level(scope): message".
func TestLogLinesCarryTheClockOnlyWhenSet(t *testing.T) {
	log, restore := CaptureLog()
	defer restore()
	Info("x", "plain")
	LogClock = func() time.Time { return time.Date(2026, 11, 21, 16, 48, 22, 322_000_000, time.Local) }
	Warn("y", "stamped")
	LogClock = nil
	want := "info(x): plain\n2026-11-21 16:48:22.322 warning(y): stamped\n"
	if log.String() != want {
		t.Fatalf("got %q", log.String())
	}
}
