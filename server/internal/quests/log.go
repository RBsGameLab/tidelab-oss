// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
)

// KeyedLine is a single keyed line in the append-only quests log.
type KeyedLine struct {
	Key  string
	Line string
}

// QuestLog implements the R-80d append-only quests log (specs/save-format.md §6).
// A save appends the lines that changed, "X <quest>" for a run that is gone and "W\t<key>" for a
// variable that is gone; it rewrites the file as a snapshot only when the history passes four
// times the lines it describes. Ported from QuestLog.cs.
//
// Lines are keyed: "F 11", "L 33", "D 22" are their own key; a run's key is "P <quest>" and a
// variable's is "V\t<key>". Uses omap.Map so dropped-key order matches .NET Dictionary<string,string>.
type QuestLog struct {
	saved       omap.Map[string, string]
	LinesOnDisk int
}

func NewQuestLog() *QuestLog { return &QuestLog{} }

// Adopt records that the file on disk now holds exactly these lines (after a load).
func (l *QuestLog) Adopt(lines []KeyedLine) {
	l.saved.Clear()
	for _, kl := range lines {
		l.saved.Set(kl.Key, kl.Line)
	}
}

func (l *QuestLog) Reset() {
	l.saved.Clear()
	l.LinesOnDisk = 0
}

func dropLogLine(key string) (string, bool) {
	if strings.HasPrefix(key, "P ") {
		return "X " + key[2:], true
	}
	return DropVarLine(key)
}

// Save writes what changed since the last save to path; current is every line the progress is now,
// in snapshot order. Returns how many lines changed.
func (l *QuestLog) Save(path string, current []KeyedLine) int {
	var text strings.Builder
	changed := 0
	keys := make(map[string]struct{}, len(current))
	for _, kl := range current {
		keys[kl.Key] = struct{}{}
		if old, ok := l.saved.Get(kl.Key); ok && old == kl.Line {
			continue
		}
		text.WriteString(kl.Line)
		text.WriteByte('\n')
		changed++
	}
	var gone []string
	for k := range l.saved.All() {
		if _, ok := keys[k]; !ok {
			gone = append(gone, k)
		}
	}
	for _, k := range gone {
		if drop, ok := dropLogLine(k); ok {
			text.WriteString(drop)
			text.WriteByte('\n')
			changed++
		}
	}
	if changed == 0 {
		return 0
	}
	for _, k := range gone {
		l.saved.Delete(k)
	}
	for _, kl := range current {
		l.saved.Set(kl.Key, kl.Line)
	}

	limit := 4 * max(len(current), 16)
	if l.LinesOnDisk+changed > limit {
		// R-80d: atomic, so a crash while compacting cannot leave half a snapshot.
		var snapshot strings.Builder
		for _, kl := range current {
			snapshot.WriteString(kl.Line)
			snapshot.WriteByte('\n')
		}
		if _, err := common.WriteAtomic(path, snapshot.String()); err != nil {
			common.Warn("quest", fmt.Sprintf("saving quest progress failed: %v", err))
			return changed
		}
		l.LinesOnDisk = len(current)
	} else {
		if err := appendTextFile(path, text.String()); err != nil {
			common.Warn("quest", fmt.Sprintf("saving quest progress failed: %v", err))
			return changed
		}
		l.LinesOnDisk += changed
	}
	return changed
}

func appendTextFile(path, text string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(text)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
