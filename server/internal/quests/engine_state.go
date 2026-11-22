// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
	"tidelab/protocol"
)

// QuestsFileName is the player save file for quest progress.
const QuestsFileName = "quests"

func (e *Engine) statePath() string {
	if f := e.host.Files(); f != nil {
		return f.PathOf(QuestsFileName)
	}
	return ""
}

// Load reads the player's progress. A player without a save file starts the story at StartQuest
// with PreFinished prologue quests counted as done.
func (e *Engine) Load() {
	e.finished.Clear()
	e.runList = e.runList[:0]
	clear(e.runs)
	e.ready = e.ready[:0]
	e.worldDone.Clear()
	e.dungeonsDone.Clear()
	e.Vars.Clear()
	e.actorsCleaned = false
	e.log.Reset()

	path := e.statePath()
	if path != "" && common.FileExists(path) {
		if b, err := os.ReadFile(path); err == nil {
			e.ParseState(string(b))
			e.log.Adopt(e.BuildLines())
			common.Info("quest", fmt.Sprintf("progress: %d finished, %d in progress, %d lines on disk",
				e.finished.Len(), len(e.runList), e.log.LinesOnDisk))
			return
		}
	}
	for _, id := range PreFinished {
		e.finished.Set(id, struct{}{})
	}
	e.startRun(StartQuest)
	common.Info("quest", fmt.Sprintf("new story progress: starting quest %d", StartQuest))
}

func splitSpaces(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' })
}

func parseFieldInt(f []string, i int) int32 {
	if i >= len(f) {
		return 0
	}
	v, err := strconv.ParseInt(f[i], 10, 32)
	if err != nil {
		return 0
	}
	return int32(v)
}

// ParseState reads quests-file text into progress (a later line for a quest replaces earlier ones).
func (e *Engine) ParseState(text string) {
	for _, line := range splitNonEmptyLines(text) {
		// R-83: tree variables ("V\t..." / "W\t...").
		if e.Vars.LoadLine(line) {
			e.log.LinesOnDisk++
			continue
		}
		parts := splitSpaces(line)
		if len(parts) < 2 {
			continue
		}
		id64, err := strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			continue
		}
		id := int32(id64)
		e.log.LinesOnDisk++
		switch parts[0] {
		case "F":
			e.finished.Set(id, struct{}{})
			continue
		case "L":
			e.worldDone.Set(id, struct{}{})
			continue
		case "D":
			e.dungeonsDone.Set(id, struct{}{})
			continue
		case "X":
			// R-80d: drop marker: this quest has no progress of its own any more.
			e.removeRun(int64(id))
			e.Vars.SetUntracked(int64(id), false)
			continue
		}
		run := e.startRun(id)
		if run == nil {
			continue
		}
		clear(run.Nodes)
		// R-83: "v" marks a run whose variables were kept from its start.
		tracked := false
		for _, entry := range parts[2:] {
			if entry == "v" {
				tracked = true
				continue
			}
			f := strings.Split(entry, ":")
			idx, err := strconv.ParseUint(f[0], 10, 31)
			if err != nil || int(idx) >= len(run.Nodes) {
				continue
			}
			st := &run.Nodes[idx]
			s := parseFieldInt(f, 1)
			if s >= 0 && s <= 7 {
				st.Status = protocol.NodeStatus(s)
			} else {
				st.Status = protocol.NodeStatus_NodeStatus_NotActive
			}
			c := parseFieldInt(f, 2)
			if c >= 0 && c <= 6 {
				st.Cq = protocol.ChildQuestNodeStatus(c)
			} else {
				st.Cq = protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_NotActive
			}
			st.Count = max(0, parseFieldInt(f, 3))
			st.Cursor = max(0, parseFieldInt(f, 4))
		}
		e.Vars.SetUntracked(int64(id), !tracked)
		FixCursors(run)
	}
}

// FixCursors points each running Sequence's cursor at the child that is actually active (R-4h).
func FixCursors(run *Run) {
	for i := range run.Nodes {
		if !run.Tree.Node(i).Is("Sequence") || run.Nodes[i].Status != protocol.NodeStatus_NodeStatus_Activated {
			continue
		}
		kids := run.Tree.Children[i]
		for k, kid := range kids {
			if run.Nodes[kid].Status != protocol.NodeStatus_NodeStatus_Activated {
				continue
			}
			if run.Nodes[i].Cursor != int32(k) {
				common.Info("quest", fmt.Sprintf("quest %d node %d: cursor %d -> %d (child %d)",
					run.ID(), run.Tree.Node(i).ID, run.Nodes[i].Cursor, k, run.Tree.Node(kid).ID))
				run.Nodes[i].Cursor = int32(k)
			}
			break
		}
	}
}

func (e *Engine) runLine(run *Run) string {
	var sb strings.Builder
	sb.WriteString("P ")
	sb.WriteString(strconv.FormatInt(int64(run.ID()), 10))
	for i, st := range run.Nodes {
		if st.IsDefault() {
			continue
		}
		sb.WriteByte(' ')
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte(':')
		sb.WriteString(strconv.Itoa(int(st.Status)))
		sb.WriteByte(':')
		sb.WriteString(strconv.Itoa(int(st.Cq)))
		sb.WriteByte(':')
		sb.WriteString(strconv.FormatInt(int64(st.Count), 10))
		sb.WriteByte(':')
		sb.WriteString(strconv.FormatInt(int64(st.Cursor), 10))
	}
	if e.Vars.IsTracked(run.Key) {
		sb.WriteString(" v")
	}
	return sb.String()
}

func sortedInt32Keys(m *omap.Map[int32, struct{}]) []int32 {
	out := m.Keys()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// BuildLines returns the progress as keyed lines in snapshot order: F, L, D, P, V.
func (e *Engine) BuildLines() []KeyedLine {
	var lines []KeyedLine
	sets := []struct {
		kind byte
		set  *omap.Map[int32, struct{}]
	}{
		{'F', &e.finished},
		{'L', &e.worldDone},
		{'D', &e.dungeonsDone},
	}
	for _, s := range sets {
		for _, id := range sortedInt32Keys(s.set) {
			line := fmt.Sprintf("%c %d", s.kind, id)
			lines = append(lines, KeyedLine{Key: line, Line: line})
		}
	}
	for _, run := range e.runList {
		if !run.LevelPlay {
			key := fmt.Sprintf("P %d", run.ID())
			lines = append(lines, KeyedLine{Key: key, Line: e.runLine(run)})
		}
	}
	lines = append(lines, e.Vars.BuildLines()...)
	return lines
}

// Save writes what changed to the quests file.
func (e *Engine) Save() {
	path := e.statePath()
	if path == "" {
		return
	}
	e.log.Save(path, e.BuildLines())
}

func (e *Engine) LinesOnDisk() int { return e.log.LinesOnDisk }

func (e *Engine) startRun(quest int32) *Run {
	if run, ok := e.runs[int64(quest)]; ok {
		return run
	}
	tree, ok := e.cat.Quests[quest]
	if !ok {
		return nil
	}
	run := newRun(tree, int64(quest), false)
	e.addRun(run)
	return run
}

func (e *Engine) addRun(run *Run) {
	e.runs[run.Key] = run
	e.runList = append(e.runList, run)
}

// removeRun uses swap-remove to match C#/Zig run map ordering.
func (e *Engine) removeRun(key int64) {
	run, ok := e.runs[key]
	if !ok {
		return
	}
	delete(e.runs, key)
	if i := slices.Index(e.runList, run); i >= 0 {
		last := len(e.runList) - 1
		e.runList[i] = e.runList[last]
		e.runList = e.runList[:last]
	}
}

// NewSession resets session-scoped delivery flags on login or reconnect (R-117).
func (e *Engine) NewSession() {
	e.ready = e.ready[:0]
	for _, run := range e.runList {
		run.ClientHasTree = false
	}
}
