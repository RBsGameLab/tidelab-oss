// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tidelab/internal/save"
	"tidelab/protocol"
)

func hideDef(key string, ids, force []EntityPair) HideGroupDef {
	return HideGroupDef{Key: key, IDs: ids, Force: force}
}

func TestHideGroupsSpecificEntities(t *testing.T) {
	h := NewHideGroups(nil, nil)
	h.Add(hideDef("q_133000002_spec_8_136801637", nil, []EntityPair{{8, 136801637}}))
	h.Add(hideDef("l_7_spec_8_136801637", nil, []EntityPair{{8, 136801637}}))
	h.Add(hideDef("q_133000002_spec_8_136801638", nil, []EntityPair{{8, 136801638}}))
	if !h.IsHidden(8, 136801637, true) {
		t.Fatal("named entity should be hidden even when awake")
	}
	if n := h.ShowSpecific(8, 136801637); n != 2 {
		t.Fatalf("ShowSpecific = %d, want 2", n)
	}
	if h.IsHidden(8, 136801637, false) {
		t.Fatal("136801637 should be shown")
	}
	if !h.IsHidden(8, 136801638, false) {
		t.Fatal("136801638 should still be hidden")
	}
	if n := h.RemoveOwner('q', 133000002); n != 1 {
		t.Fatalf("RemoveOwner = %d, want 1", n)
	}
	if h.Count() != 0 {
		t.Fatalf("Count = %d, want 0", h.Count())
	}
}

func TestHideGroupsRangedEntitiesGiveWayToAwakeOnesForcedDoNot(t *testing.T) {
	h := NewHideGroups(nil, nil)
	h.Add(hideDef("q_100_a", []EntityPair{{8, 1}, {8, 2}}, []EntityPair{{8, 3}}))
	if !h.IsHidden(8, 1, false) {
		t.Fatal("1 should be hidden when asleep")
	}
	if h.IsHidden(8, 1, true) {
		t.Fatal("1 should give way when awake")
	}
	if !h.IsHidden(8, 3, true) {
		t.Fatal("forced 3 should stay hidden when awake")
	}
	if h.IsHidden(906, 1, false) {
		t.Fatal("different map should not be hidden")
	}
	if h.IsHidden(8, 4, false) {
		t.Fatal("unlisted entity should not be hidden")
	}
}

func TestHideGroupsOverlappingGroupsShowAndOwnerRelease(t *testing.T) {
	h := NewHideGroups(nil, nil)
	if n := h.Add(hideDef("q_100_a", []EntityPair{{8, 1}, {8, 2}}, nil)); n != 2 {
		t.Fatalf("Add = %d, want 2", n)
	}
	h.Add(hideDef("q_100_b", []EntityPair{{8, 2}}, nil))
	h.Add(hideDef("l_7_c", []EntityPair{{8, 5}}, nil))
	if n := h.Add(hideDef("q_100_a", nil, nil)); n != 0 {
		t.Fatalf("duplicate Add = %d, want 0", n)
	}
	if !h.Remove("q_100_a") {
		t.Fatal("Remove q_100_a should succeed")
	}
	if h.IsHidden(8, 1, false) {
		t.Fatal("1 should no longer be hidden")
	}
	if !h.IsHidden(8, 2, false) {
		t.Fatal("2 is still in q_100_b")
	}
	if n := h.RemoveOwner('q', 100); n != 1 {
		t.Fatalf("RemoveOwner = %d, want 1", n)
	}
	if h.IsHidden(8, 2, false) {
		t.Fatal("2 should now be shown")
	}
	if !h.IsHidden(8, 5, false) {
		t.Fatal("5 in l_7_c should still be hidden")
	}
	if h.Remove("q_100_a") {
		t.Fatal("already removed")
	}
	if n := h.Retain(func(kind byte, _ int32) bool { return kind == 'q' }); n != 1 {
		t.Fatalf("Retain = %d, want 1", n)
	}
	if h.Count() != 0 {
		t.Fatalf("Count = %d, want 0", h.Count())
	}
}

func TestHideGroupsOwnerFromKey(t *testing.T) {
	if k, id, ok := HideGroupOwnerOf("q_125000121_虹镇"); !ok || k != 'q' || id != 125000121 {
		t.Fatalf("got (%c, %d, %v)", k, id, ok)
	}
	if k, id, ok := HideGroupOwnerOf("l_923800001_御剑清场"); !ok || k != 'l' || id != 923800001 {
		t.Fatalf("got (%c, %d, %v)", k, id, ok)
	}
	if _, _, ok := HideGroupOwnerOf("x_1_a"); ok {
		t.Fatal("x_1_a should have no owner")
	}
	if _, _, ok := HideGroupOwnerOf("q_abc"); ok {
		t.Fatal("q_abc should have no owner")
	}
}

func TestHideGroupsLogAppendsChangesAndLoadsBack(t *testing.T) {
	files := save.NewPlayerFiles(t.TempDir(), 777)
	path := files.PathOf(HideGroupsFileName)
	defs := map[string]HideGroupDef{
		"q_1_a": hideDef("q_1_a", []EntityPair{{8, 10}}, nil),
		"q_2_b": hideDef("q_2_b", nil, []EntityPair{{8, 20}}),
	}
	h := NewHideGroups(defs, files)
	if n := h.Hide("q_1_a"); n != 1 {
		t.Fatalf("Hide q_1_a = %d", n)
	}
	if n := h.Hide("q_2_b"); n != 1 {
		t.Fatalf("Hide q_2_b = %d", n)
	}
	if n := h.Hide("q_2_b"); n != 0 {
		t.Fatalf("duplicate Hide q_2_b = %d", n)
	}
	if !h.Show("q_1_a") {
		t.Fatal("Show q_1_a should succeed")
	}
	h.HideSpecific('l', 7, 8, 30)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "+q_1_a\n+q_2_b\n-q_1_a\n+l_7_spec_8_30\n"
	if string(b) != want {
		t.Fatalf("log = %q, want %q", string(b), want)
	}

	// Bare key not in defs is still activated empty so Show matches.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("q_9_unknown\n")
	_ = f.Close()

	again := NewHideGroups(defs, files)
	if !slices.Equal(again.Keys(), []string{"q_2_b", "l_7_spec_8_30", "q_9_unknown"}) {
		t.Fatalf("Keys = %v", again.Keys())
	}
	if !again.IsHidden(8, 20, true) || !again.IsHidden(8, 30, true) || again.IsHidden(8, 10, false) {
		t.Fatal("unexpected hidden states after reload")
	}
	if !again.Show("q_9_unknown") {
		t.Fatal("Show q_9_unknown should succeed")
	}
	b, _ = os.ReadFile(path)
	if !strings.HasSuffix(string(b), "q_9_unknown\n-q_9_unknown\n") {
		t.Fatalf("unexpected log suffix: %q", string(b))
	}
}

func TestHideGroupsLongLogIsRewrittenAsTheSet(t *testing.T) {
	files := save.NewPlayerFiles(t.TempDir(), 777)
	path := files.PathOf(HideGroupsFileName)
	h := NewHideGroups(nil, files)
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("q_%d_a", i)
		h.Hide(key)
		h.Show(key)
	}
	h.Hide("q_77_a")
	if h.LinesOnDisk() > 64 {
		t.Fatalf("LinesOnDisk = %d, want <= 64", h.LinesOnDisk())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitNonEmptyLines(string(b))
	if len(lines) != h.LinesOnDisk() {
		t.Fatalf("file lines = %d, LinesOnDisk = %d", len(lines), h.LinesOnDisk())
	}
	reloaded := NewHideGroups(nil, files)
	if !slices.Equal(reloaded.Keys(), []string{"q_77_a"}) {
		t.Fatalf("reloaded keys = %v", reloaded.Keys())
	}
}

type hideGroupsSystems struct {
	NoSystems
	hide  *HideGroups
	mapID int32
}

func (s *hideGroupsSystems) RetainClearings(ownerRunning func(byte, int32) bool) {
	if s.hide.Retain(ownerRunning) > 0 {
		s.hide.Save()
	}
}

func (s *hideGroupsSystems) StepActions(run RunRef, actions []QuestAction, _ *protocol.GameCtxPb, _ bool) {
	kind := byte('q')
	if run.LevelPlay {
		kind = 'l'
	}
	for _, a := range actions {
		switch a.Name {
		case "HideGroup":
			if key, ok := ActionString(a, "GroupKey"); ok {
				s.hide.Hide(key)
			}
		case "HideSpecificEntities":
			for _, id := range ActionInts(a, "EntityIds") {
				s.hide.HideSpecific(kind, run.TreeID, s.mapID, id)
			}
		}
	}
}

func (s *hideGroupsSystems) ReleaseRun(_ int64, levelPlay bool, treeID int32) {
	kind := byte('q')
	if levelPlay {
		kind = 'l'
	}
	s.hide.Release(kind, treeID)
}

func TestHideGroupsEngineLifecycle(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	hidePath := host.files.PathOf(HideGroupsFileName)
	_ = os.MkdirAll(filepath.Dir(hidePath), 0o755)
	_ = os.WriteFile(hidePath, []byte("+q_555_old\n"), 0o644)

	ds := NewQuestDataSet()
	ds.Quests = append(ds.Quests, QuestDef{
		ID: 9, Type: 1, Name: "九", PreQuest: 1,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", "", 2, 3),
			func() NodeDef {
				n := node(2, 1, "ChildQuest", "ReachArea")
				n.Enter = []QuestAction{
					rawAct(t, "HideGroup", `{"GroupKey":"q_9_镇"}`),
					rawAct(t, "HideSpecificEntities", `{"EntityIds":[70]}`),
				}
				return n
			}(),
			node(3, 1, "QuestSucceed", ""),
		},
	})
	ds.HideGroups["q_9_镇"] = hideDef("q_9_镇", []EntityPair{{8, 60}, {8, 61}}, nil)

	h := NewHideGroups(ds.HideGroups, host.files)
	sys := &hideGroupsSystems{hide: h, mapID: 8}
	engine := NewEngine(NewCatalog(ds), host, sys)
	engine.ParseState("F 1\n")
	engine.SyncOnJoin()

	if h.IsActive("q_555_old") {
		t.Fatal("stale clearing q_555_old should be released on join")
	}
	if !h.IsActive("q_9_镇") || !h.IsHidden(8, 60, false) || h.IsHidden(8, 60, true) || !h.IsHidden(8, 70, true) {
		t.Fatal("quest 9 clearings should be active")
	}
	b, _ := os.ReadFile(hidePath)
	if string(b) != "+q_555_old\n-q_555_old\n+q_9_镇\n+q_9_spec_8_70\n" {
		t.Fatalf("unexpected log: %q", string(b))
	}

	if code := engine.SubmitNode(9, 2); code != protocol.ErrorCode_ErrorCode_Success {
		t.Fatalf("SubmitNode = %v", code)
	}
	if !engine.IsFinished(9) || h.Count() != 0 {
		t.Fatalf("finished=%v count=%d", engine.IsFinished(9), h.Count())
	}
	b, _ = os.ReadFile(hidePath)
	if !strings.HasSuffix(string(b), "-q_9_镇\n-q_9_spec_8_70\n") {
		t.Fatalf("unexpected log suffix: %q", string(b))
	}
}
