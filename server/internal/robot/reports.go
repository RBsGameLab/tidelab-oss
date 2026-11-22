// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type DungeonOutcome int

const (
	DungeonSuccess DungeonOutcome = iota
	DungeonFailed
	DungeonStuck
	DungeonEndedAtStart
	DungeonEnterFailed
	DungeonNoLevelPlay
)

type StoryOutcome int

const (
	StoryRunning StoryOutcome = iota
	StoryFinished
	StoryStuck
	StoryOutOfIterations
)

// DungeonReport is one dungeon's report row (robot_dungeons*.tsv).
type DungeonReport struct {
	Dungeon                           int32
	Outcome                           DungeonOutcome
	LevelPlays                        []int32
	Iterations, Monsters              int
	LevelMin, LevelMax, LevelExpected int32
	RewardOK, LeftOK                  bool
	ClockSkips                        int
	StuckAt, Skipped, Unhandled       string
	Warnings                          int
	FirstWarning                      string
}

func NewDungeonReport(dungeon int32) *DungeonReport {
	return &DungeonReport{Dungeon: dungeon, Outcome: DungeonStuck}
}

// StoryQuestReport is one quest's report row (robot_story.tsv and robot_side.tsv).
type StoryQuestReport struct {
	ID                                int32
	Name                              string
	Kind                              int32
	Outcome                           StoryOutcome
	AcceptedAt, FinishedAt            *int
	Skips                             int
	Dungeons, StuckAt, Skipped, Drove strings.Builder
}

// FixCounts are the fix processor's counters (R-82a).
type FixCounts struct{ Run, Finished, Moved, Transfers, ClientSkipped int }

// StorySummaryCounters are printed at the end of a story or side run (gate.py parses them).
type StorySummaryCounters struct {
	Warnings                                                                   int
	Unhandled                                                                  string
	HideHidden, HideEntities, HideShown, HideReleased, HideRemoved, HideActive int
	ItemsGiven, ItemsTaken                                                     int
	StatesDriven                                                               SetterStats
	OptionsAdded, OptionsRemoved, OptionsReleased, OptionsPicked               int
	OptionsStillOffered, OptionFlowsPending                                    int
	HandIns, BattleStepsDone                                                   int
	PresAudio, PresGuests, PresBubbles, PresBubblesCleared, PresTimeLocks      int
	PresWeather, PresSwitchedOff, PresSwitchedOn, PresUndoneAtEnd, PresStillOn int
	AreaStatesChanged, EntitiesMoved                                           int
	VehiclesBoarded, VehiclesLeft, VehiclesNotDone                             int
	TeleportsSent, TeleportsHeld                                               int
	Fixes                                                                      FixCounts
}

const (
	StoryHeader   = "quest\tname\ttype\toutcome\taccepted_at\tfinished_at\tskips\tdungeons\tstuck_at\tskipped\tdrove"
	DungeonHeader = "dungeon\toutcome\tlevel_plays\titerations\tmonsters\tlevel\texpected\treward\tleft\tclock_skips\tstuck_at\tskipped\tunhandled\twarnings\tfirst_warning"
)

func (o DungeonOutcome) String() string {
	switch o {
	case DungeonSuccess:
		return "success"
	case DungeonFailed:
		return "failed"
	case DungeonEndedAtStart:
		return "ended_at_start"
	case DungeonEnterFailed:
		return "enter_failed"
	case DungeonNoLevelPlay:
		return "no_level_play"
	}
	return "stuck"
}

func (o StoryOutcome) String() string {
	switch o {
	case StoryFinished:
		return "finished"
	case StoryStuck:
		return "stuck"
	case StoryOutOfIterations:
		return "out_of_iterations"
	}
	return "running"
}

func SanitizeField(s string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

func FormatLevelPlays(plays []int32) string {
	if len(plays) == 0 {
		return "{  }"
	}
	parts := make([]string, len(plays))
	for i, p := range plays {
		parts[i] = strconv.Itoa(int(p))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func optInt(p *int) string {
	if p == nil {
		return "null"
	}
	return strconv.Itoa(*p)
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func FormatStoryRow(q *StoryQuestReport) string {
	return fmt.Sprintf("%d\t%s\t%d\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s", q.ID, SanitizeField(q.Name), q.Kind, q.Outcome,
		optInt(q.AcceptedAt), optInt(q.FinishedAt), q.Skips, SanitizeField(q.Dungeons.String()), SanitizeField(q.StuckAt.String()),
		SanitizeField(q.Skipped.String()), SanitizeField(q.Drove.String()))
}

func FormatDungeonRow(r *DungeonReport) string {
	return fmt.Sprintf("%d\t%s\t%s\t%d\t%d\t%d-%d\t%d\t%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s", r.Dungeon, r.Outcome, FormatLevelPlays(r.LevelPlays),
		r.Iterations, r.Monsters, r.LevelMin, r.LevelMax, r.LevelExpected, boolText(r.RewardOK), boolText(r.LeftOK), r.ClockSkips,
		SanitizeField(r.StuckAt), SanitizeField(r.Skipped), SanitizeField(r.Unhandled), r.Warnings, SanitizeField(r.FirstWarning))
}

func BuildStoryTsv(list []*StoryQuestReport) string {
	var sb strings.Builder
	sb.WriteString(StoryHeader + "\n")
	for _, q := range list {
		sb.WriteString(FormatStoryRow(q) + "\n")
	}
	return sb.String()
}

func BuildDungeonTsv(list []*DungeonReport) string {
	var sb strings.Builder
	sb.WriteString(DungeonHeader + "\n")
	for _, r := range list {
		sb.WriteString(FormatDungeonRow(r) + "\n")
	}
	return sb.String()
}

func WriteStoryReport(list []*StoryQuestReport, c *StorySummaryCounters, path string, out io.Writer) error {
	finished, clean := 0, 0
	for _, q := range list {
		if q.Outcome == StoryFinished {
			finished++
			if q.Skips == 0 {
				clean++
			}
		} else {
			fmt.Fprintf(out, "robot: quest %d %s %s at %s\n", q.ID, q.Name, q.Outcome, q.StuckAt.String())
		}
		if q.Skips > 0 {
			fmt.Fprintf(out, "robot: quest %d %s: %d skips: %s\n", q.ID, q.Name, q.Skips, q.Skipped.String())
		}
	}
	unhandled := c.Unhandled
	if unhandled == "" {
		unhandled = "none"
	}
	fmt.Fprintf(out, "robot: story: %d quests finished (%d without skips), %d not, %d warnings; unhandled requests: %s\n", finished, clean, len(list)-finished, c.Warnings, unhandled)
	fmt.Fprintf(out, "robot: story: hide groups: %d hidden (%d entities), %d shown, %d released at quest end, %d spawned removed, %d still active\n",
		c.HideHidden, c.HideEntities, c.HideShown, c.HideReleased, c.HideRemoved, c.HideActive)
	fmt.Fprintf(out, "robot: story: quest items: %d given, %d taken back\n", c.ItemsGiven, c.ItemsTaken)
	ss := c.StatesDriven
	fmt.Fprintf(out, "robot: story: states driven: %d by an interact option, %d by a device's state, %d by a 机关组, %d reported like the client, %d with no way in the data\n",
		ss.Interact, ss.Chain, ss.Group, ss.Client, ss.None)
	fmt.Fprintf(out, "robot: story: quest options: %d added, %d removed, %d released at run end, %d picked, %d still offered, %d dialogues not ended\n",
		c.OptionsAdded, c.OptionsRemoved, c.OptionsReleased, c.OptionsPicked, c.OptionsStillOffered, c.OptionFlowsPending)
	fmt.Fprintf(out, "robot: story: hand-ins: %d, battle steps judged: %d\n", c.HandIns, c.BattleStepsDone)
	fmt.Fprintf(out, "robot: story: presentation: %d audio, %d guests, %d bubbles (%d cleared), %d time locks, %d weathers, %d switched off / %d on, %d undone at run end, %d still on\n",
		c.PresAudio, c.PresGuests, c.PresBubbles, c.PresBubblesCleared, c.PresTimeLocks, c.PresWeather, c.PresSwitchedOff, c.PresSwitchedOn, c.PresUndoneAtEnd, c.PresStillOn)
	fmt.Fprintf(out, "robot: story: area states changed: %d, entities moved: %d\n", c.AreaStatesChanged, c.EntitiesMoved)
	fmt.Fprintf(out, "robot: story: vehicles: %d boarded, %d left, %d not done\n", c.VehiclesBoarded, c.VehiclesLeft, c.VehiclesNotDone)
	fmt.Fprintf(out, "robot: story: quest teleports: %d sent, %d held back\n", c.TeleportsSent, c.TeleportsHeld)
	fx := c.Fixes
	fmt.Fprintf(out, "robot: story: fixes: %d run, %d steps finished, %d player moves, %d instance transfers, %d client actions not sent\n",
		fx.Run, fx.Finished, fx.Moved, fx.Transfers, fx.ClientSkipped)
	if err := WriteUTF8(path, BuildStoryTsv(list)); err != nil {
		return err
	}
	fmt.Fprintf(out, "robot: report written to %s\n", path)
	return nil
}

func WriteDungeonReport(list []*DungeonReport, path string, battleStepsDone int, elapsedSeconds int64, out io.Writer) error {
	counts := map[DungeonOutcome]int{}
	levelBad := 0
	for _, r := range list {
		counts[r.Outcome]++
		if r.LevelExpected > 0 && r.Monsters > 0 && (r.LevelMin != r.LevelExpected || r.LevelMax != r.LevelExpected) {
			levelBad++
		}
	}
	fmt.Fprintf(out, "robot: %d success, %d failed, %d stuck, %d ended at start, %d enter failed, %d no level play, %d with wrong monster level (%d s)\n",
		counts[DungeonSuccess], counts[DungeonFailed], counts[DungeonStuck], counts[DungeonEndedAtStart], counts[DungeonEnterFailed], counts[DungeonNoLevelPlay], levelBad, elapsedSeconds)
	if err := WriteUTF8(path, BuildDungeonTsv(list)); err != nil {
		return err
	}
	fmt.Fprintf(out, "robot: report written to %s\n", path)
	fmt.Fprintf(out, "robot: battle steps judged: %d\n", battleStepsDone)
	return nil
}

func WriteUTF8(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
