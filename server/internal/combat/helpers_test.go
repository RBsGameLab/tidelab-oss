// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/save"
)

// recordingSink keeps every push (RecordingPushSink).
type recordingSink struct{ msgs []proto.Message }

func (s *recordingSink) Push(m proto.Message) { s.msgs = append(s.msgs, m) }
func (s *recordingSink) clear()               { s.msgs = nil }

// ofType are the pushes of type T.
func ofType[T proto.Message](s *recordingSink) []T {
	var out []T
	for _, m := range s.msgs {
		if x, ok := m.(T); ok {
			out = append(out, x)
		}
	}
	return out
}

// recordingHooks keeps the hook calls (RecordingHooks).
type recordingHooks struct {
	usedSkills    [][2]int64
	killed        []*CombatEntity
	genderChanges []bool
	sceneSaves    int
}

func (h *recordingHooks) OnUseSkill(e, s int64)           { h.usedSkills = append(h.usedSkills, [2]int64{e, s}) }
func (h *recordingHooks) OnMonsterKilled(m *CombatEntity) { h.killed = append(h.killed, m) }
func (h *recordingHooks) OnGenderChanged(male bool)       { h.genderChanges = append(h.genderChanges, male) }
func (h *recordingHooks) OnSceneSaved()                   { h.sceneSaves++ }

type ctxOpts struct {
	tables            *data.DataTables
	nowMs             int64
	hooks             GameHooks
	rng               Rng
	seedDefaultEchoes bool
}

// createContext is CreateContext of the C# fixtures: player 777 loaded from dir and prepared
// for login (nowMs 1 700 000 000 000 unless given).
func createContext(t testing.TB, dir string, o ctxOpts) *PlayerContext {
	t.Helper()
	if o.tables == nil {
		o.tables = syntheticTables()
	}
	if o.nowMs == 0 {
		o.nowMs = 1_700_000_000_000
	}
	sh := &Shared{Tables: o.tables, Concomitants: NewConcomitantResolver(o.tables), Passives: NewPassiveSkillsIndex(o.tables),
		Fsm: NewFsmInitializer(o.tables), LandingArgs: DefaultLandingRoleArgs}
	sh.BuffBuilder = NewRoleBuffBuilder(o.tables, sh.Concomitants)
	c := NewPlayerContext(sh, save.LoadPlayer(dir, 777), o.hooks, o.rng)
	c.PrepareForLogin(o.nowMs, uint32(o.nowMs/1000), o.seedDefaultEchoes)
	return c
}

func i32p(v int32) *int32 { return &v }

func must(t testing.TB, ok bool, a ...any) {
	t.Helper()
	if !ok {
		t.Fatal(a...)
	}
}
