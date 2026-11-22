// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package save

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/zon"
)

// PlayerFiles is one player's directory, state/player/<id>/ (specs/save-format.md §5).
type PlayerFiles struct {
	PlayerID int32
	StateDir string
	Dir      string
}

func NewPlayerFiles(stateDir string, playerID int32) *PlayerFiles {
	return &PlayerFiles{playerID, stateDir, filepath.Join(stateDir, "player", strconv.Itoa(int(playerID)))}
}

func (f *PlayerFiles) PathOf(relative string) string {
	return filepath.Join(f.Dir, filepath.FromSlash(relative))
}

func (f *PlayerFiles) ScenePath(instanceID int32, relative string) string {
	return f.PathOf("scene/" + strconv.Itoa(int(instanceID)) + "/" + relative)
}

// Read is the file's ZON value, nil when it does not exist.
func (f *PlayerFiles) Read(relative string) *zon.Value {
	p := f.PathOf(relative)
	if !common.FileExists(p) {
		return nil
	}
	return parseFile(p)
}

// Write writes the file (atomically; nothing when the bytes are the same).
func (f *PlayerFiles) Write(relative string, v *zon.Value) { writeZon(f.PathOf(relative), v) }

// NumberedFiles are the numeric file names of a directory, sorted (specs/save-format.md §5.9).
func (f *PlayerFiles) NumberedFiles(relativeDir string) []int32 {
	var ids []int32
	for _, e := range numberedFiles(f.PathOf(relativeDir)) {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	return ids
}

// PlayerState is the saved state the world owns, loaded at Entered (specs/enter-world.md §2):
// last_scene, explore_tools_info and the motorcycle's looks.
type PlayerState struct {
	Files           *PlayerFiles
	LastScene       int32
	ExploreTools    *ExploreToolsInfo
	Motor           *MotorLooks
	FirstScene      bool // no scene/ directory before: a new player's first scene (enter-world.md §7)
	exploreOriginal *zon.Value
}

func (s *PlayerState) PlayerID() int32 { return s.Files.PlayerID }

func LoadPlayerState(files *PlayerFiles) *PlayerState {
	s := &PlayerState{
		Files:      files,
		LastScene:  Int(files.Read("last_scene"), "instance_id", 8),
		FirstScene: !dirExists(files.PathOf("scene")),
	}
	s.exploreOriginal = files.Read("explore_tools_info")
	s.ExploreTools = ExploreToolsFrom(s.exploreOriginal)
	s.Motor = MotorLooksFrom(files.Read("motor_info"))
	return s
}

func (s *PlayerState) SaveLastScene() {
	s.Files.Write("last_scene", Merge(s.Files.Read("last_scene"), zon.F("instance_id", N(int64(s.LastScene)))))
}

func (s *PlayerState) SaveExploreTools() {
	s.exploreOriginal = s.ExploreTools.ToZon(s.exploreOriginal)
	s.Files.Write("explore_tools_info", s.exploreOriginal)
}

// SceneSaved: the first scene is over once a scene of this player has been saved.
func (s *PlayerState) SceneSaved() { s.FirstScene = false }

// ExploreToolsInfo is explore_tools_info (specs/save-format.md §5.4).
type ExploreToolsInfo struct {
	UnlockedExploreSkills []int32
	ActiveExploreSkill    int32
	Roulette              []int32
	ExploreExtraItemID    int32
	ActiveFunctionSkill   int32
	FunctionRoulette      []int32
	FunctionExtraItemID   int32
	ActiveMotorcycleSkill int32
	MotorcycleRoulette    []int32
	MotorcycleExtraItemID int32
}

func ExploreToolsFrom(f *zon.Value) *ExploreToolsInfo {
	return &ExploreToolsInfo{
		UnlockedExploreSkills: Ints(f, "unlocked_explore_skills"),
		ActiveExploreSkill:    Int(f, "active_explore_skill", 1001),
		Roulette:              Ints(f, "roulette"),
		ExploreExtraItemID:    Int(f, "explore_extra_item_id", 0),
		ActiveFunctionSkill:   Int(f, "active_function_skill", 0),
		FunctionRoulette:      Ints(f, "function_roulette"),
		FunctionExtraItemID:   Int(f, "function_extra_item_id", 0),
		ActiveMotorcycleSkill: Int(f, "active_motorcycle_skill", 0),
		MotorcycleRoulette:    Ints(f, "motorcycle_roulette"),
		MotorcycleExtraItemID: Int(f, "motorcycle_extra_item_id", 0),
	}
}

func (t *ExploreToolsInfo) ToZon(original *zon.Value) *zon.Value {
	return Merge(original,
		zon.F("unlocked_explore_skills", IntList(t.UnlockedExploreSkills)),
		zon.F("active_explore_skill", N(int64(t.ActiveExploreSkill))),
		zon.F("roulette", IntList(t.Roulette)),
		zon.F("explore_extra_item_id", N(int64(t.ExploreExtraItemID))),
		zon.F("active_function_skill", N(int64(t.ActiveFunctionSkill))),
		zon.F("function_roulette", IntList(t.FunctionRoulette)),
		zon.F("function_extra_item_id", N(int64(t.FunctionExtraItemID))),
		zon.F("active_motorcycle_skill", N(int64(t.ActiveMotorcycleSkill))),
		zon.F("motorcycle_roulette", IntList(t.MotorcycleRoulette)),
		zon.F("motorcycle_extra_item_id", N(int64(t.MotorcycleExtraItemID))))
}

// ScenePlayer is a player's place in a scene: players[] of scene/<i>/instance.
type ScenePlayer struct {
	ID        int32
	GroupType int32
	Location  [3]float32 // X, Y, Z in game units
	Rotation  [3]float32 // roll, pitch, yaw in degrees (that order on disk)
}

// SceneInstanceFile is scene/<instanceId>/instance (specs/save-format.md §5.10).
type SceneInstanceFile struct {
	OwnerID            int32
	BuffHandle         int32
	Hour, Minute       int32
	OwnerClockTimeSpan int64
	Players            []*ScenePlayer
}

func pad3(v []float32) (out [3]float32) {
	copy(out[:], v)
	return
}

func SceneInstanceFrom(f *zon.Value) *SceneInstanceFile {
	t := Get(f, "map_time")
	file := &SceneInstanceFile{
		OwnerID:            Int(f, "owner_id", 0),
		BuffHandle:         Int(f, "buff_handle", 0),
		Hour:               Int(t, "hour", 8),
		Minute:             Int(t, "minute", 0),
		OwnerClockTimeSpan: Long(t, "owner_clock_time_span", 0),
	}
	for _, p := range List(f, "players") {
		file.Players = append(file.Players, &ScenePlayer{
			ID:        Int(p, "id", 0),
			GroupType: Int(p, "group_type", 1),
			Location:  pad3(Floats(p, "location")),
			Rotation:  pad3(Floats(p, "rotation")),
		})
	}
	return file
}

func (s *SceneInstanceFile) ToZon(original *zon.Value) *zon.Value {
	old := List(original, "players")
	players := make([]*zon.Value, len(s.Players))
	for i, p := range s.Players {
		var o *zon.Value
		if i < len(old) {
			o = old[i]
		}
		players[i] = Merge(o, zon.F("id", N(int64(p.ID))), zon.F("group_type", N(int64(p.GroupType))),
			zon.F("location", FloatList(p.Location[:])), zon.F("rotation", FloatList(p.Rotation[:])))
	}
	return Merge(original,
		zon.F("owner_id", N(int64(s.OwnerID))),
		zon.F("buff_handle", N(int64(s.BuffHandle))),
		zon.F("map_time", Merge(Get(original, "map_time"),
			zon.F("hour", N(int64(s.Hour))), zon.F("minute", N(int64(s.Minute))),
			zon.F("owner_clock_time_span", N(s.OwnerClockTimeSpan)))),
		zon.F("players", zon.NewList(players...)))
}

// MotorLooks are the motorcycle's looks from motor_info (specs/save-format.md §5.8).
type MotorLooks struct {
	EquippedSkin, EquippedFrame           int32
	EquippedStickers, EquippedDecorations []int32
}

func MotorLooksFrom(f *zon.Value) *MotorLooks {
	return &MotorLooks{
		EquippedSkin:        Int(f, "equipped_skin", 0),
		EquippedFrame:       Int(f, "equipped_frame", 0),
		EquippedStickers:    Ints(f, "equipped_stickers"),
		EquippedDecorations: Ints(f, "equipped_decorations"),
	}
}

func readText(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(string(b), string([]byte{0xEF, 0xBB, 0xBF})), true
}
