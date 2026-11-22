// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"math"
	"os"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/quests"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// Tower is R-41: 深塔 (TowerConfig; specs/dungeons.md §2.7, §4, §5). Each difficulty shows the
// floors of the newest season that has floors of it, all open. TowerStartRequest sends the
// player into the floor's dungeon; its FinishDungeon gives the floor the stars of the
// conditions it met (R-127) and sends TowerFloorUpdateNotify and TowerEndNotify. Star rewards
// are claimed once each. R-86: the team picked for a floor runs in the player's formation slot;
// their own team comes back at the next scene join outside the tower.
// Saved in player/<id>/tower: "F <floor> <stars>", "R <difficulty> <index>",
// "P <floor> <3 roles>", "B <slot> <cur role> <3 roles>". Ported from our C# Game/Tower.cs.
type Tower struct {
	path       string
	stars      omap.Map[int32, int32]
	claimed    omap.Map[int32, struct{}] // difficulty*100 + reward index
	floorTeams omap.Map[int32, [3]int32]
	clock      *towerClock

	// Current is the floor being climbed (TowerStartRequest).
	Current *int32
	// OwnTeam is R-86: the player's own team, kept while a 深塔 team runs in its formation slot.
	OwnTeam *TowerBackup
	// Pick is the 深境区 season the content panel set (R-160c; nil or 0: the newest).
	Pick func() int32
}

func (t *Tower) pick() int32 {
	if t.Pick == nil {
		return 0
	}
	return t.Pick()
}

type towerClock struct{ startedMs, limitMs int64 }

// TowerBackup is the player's own team while a 深塔 team runs.
type TowerBackup struct {
	Slot    int32
	CurRole int32
	Roles   [3]int32
}

const (
	TowerFileName     = "tower"
	TowerStarsPerPass = 3
	TowerTeamSize     = 3
	// TowerDungeonSubType is R-56: InstanceDungeon.InstSubType of the 深塔 floor dungeons.
	TowerDungeonSubType = 17
	// TowerBossRarity is R-126: MonsterInfo.RarityId from which a monster fights alone, after the rest.
	TowerBossRarity = 3
)

// TowerDifficulties are the difficulties shown.
var TowerDifficulties = []int32{1, 2, 3, 4}

func NewTower(files *save.PlayerFiles) *Tower {
	t := &Tower{path: files.PathOf(TowerFileName)}
	if b, err := os.ReadFile(t.path); err == nil {
		t.Parse(string(b))
	}
	return t
}

func (t *Tower) StarsOf(floor int32) int32 { return t.stars.At(floor) }

func (t *Tower) IsClaimed(difficulty, index int32) bool { return t.claimed.Has(difficulty*100 + index) }

func (t *Tower) TeamOf(floor int32) ([3]int32, bool) { return t.floorTeams.Get(floor) }

func (t *Tower) Parse(text string) {
	for _, raw := range strings.Split(text, "\n") {
		f := splitSpaces(trimNet(raw))
		if len(f) == 0 {
			continue
		}
		// R-86: "B" and "P" carry a team, so a line is as long as its kind needs.
		var n [5]int32
		count := 0
		for count < len(n) && count+1 < len(f) {
			v, ok := lineInt(f, count+1)
			if !ok {
				break
			}
			n[count] = v
			count++
		}
		if count < 2 {
			continue
		}
		switch f[0] {
		case "F":
			t.stars.Set(n[0], n[1])
		case "R":
			t.claimed.Set(n[0]*100+n[1], struct{}{})
		case "P":
			t.floorTeams.Set(n[0], [3]int32{n[1], n[2], n[3]})
		case "B":
			t.OwnTeam = &TowerBackup{Slot: n[0], CurRole: n[1], Roles: [3]int32{n[2], n[3], n[4]}}
		}
	}
}

func (t *Tower) Format() string {
	var sb strings.Builder
	for floor, n := range t.stars.All() {
		fmt.Fprintf(&sb, "F %d %d\n", floor, n)
	}
	for k := range t.claimed.All() {
		fmt.Fprintf(&sb, "R %d %d\n", k/100, k%100)
	}
	for floor, tm := range t.floorTeams.All() {
		fmt.Fprintf(&sb, "P %d %d %d %d\n", floor, tm[0], tm[1], tm[2])
	}
	if b := t.OwnTeam; b != nil {
		fmt.Fprintf(&sb, "B %d %d %d %d %d\n", b.Slot, b.CurRole, b.Roles[0], b.Roles[1], b.Roles[2])
	}
	return sb.String()
}

func (t *Tower) save() { save.WriteAtomic(t.path, t.Format()) }

// ----- seasons and floors -----

// TowerSeasonOf is the season listed for difficulty: pick when it has floors of difficulty
// (R-160c), else the newest season with floors of difficulty.
func TowerSeasonOf(tb *data.DataTables, difficulty, pick int32) int32 {
	var best int32
	for _, f := range tb.TowerConfig {
		if f.Difficulty != difficulty {
			continue
		}
		if pick > 0 && f.Season == pick {
			return pick
		}
		if f.Season > best {
			best = f.Season
		}
	}
	return best
}

// TowerCurrentSeason is the season the tower runs: pick when it has floors, else the newest.
func TowerCurrentSeason(tb *data.DataTables, pick int32) int32 {
	if pick > 0 {
		for _, f := range tb.TowerConfig {
			if f.Season == pick {
				return pick
			}
		}
	}
	return TowerNewestSeason(tb)
}

func TowerNewestSeason(tb *data.DataTables) int32 {
	var best int32
	for _, f := range tb.TowerConfig {
		if f.Season > best {
			best = f.Season
		}
	}
	return best
}

// TowerListed: floor f is one the tower shows (its season is the one listed for its difficulty).
func TowerListed(tb *data.DataTables, f *data.TowerConfig, pick int32) bool {
	return f.Season == TowerSeasonOf(tb, f.Difficulty, pick)
}

func (t *Tower) difficultyStars(tb *data.DataTables, difficulty int32) int32 {
	var sum int32
	for _, f := range tb.TowerConfig {
		// R-46c: 超载区 (4) has no floors in the recent seasons but its own star rewards; the
		// client claims them with the 深境区 stars.
		counts := f.Difficulty == difficulty || (difficulty == 4 && f.Difficulty == 3)
		if counts && TowerListed(tb, f, t.pick()) {
			sum += t.stars.At(f.Id)
		}
	}
	return sum
}

func towerRewardsOf(tb *data.DataTables, difficulty, pick int32) []*data.IntPair {
	season := TowerSeasonOf(tb, difficulty, pick)
	var group int32
	if s, ok := tb.TowerSeasonById[season]; ok {
		group = s.RewardGroup
	}
	for _, r := range tb.TowerSeasonReward {
		if r.Difficulty == difficulty && r.RewardGroup == group {
			return r.Reward
		}
	}
	return nil
}

func (t *Tower) floorPb(f *data.TowerConfig) *protocol.TowerFloorPb {
	res := &protocol.TowerFloorPb{TowerConfigId: f.Id, Star: t.stars.At(f.Id)}
	// R-86: the team this floor was last started with, so the 深塔 team screen opens with it.
	if team, ok := t.floorTeams.Get(f.Id); ok {
		for _, role := range team {
			if role != 0 {
				res.Formation = append(res.Formation, &protocol.TowerRolePb{RoleId: role})
			}
		}
	}
	return res
}

// TowerCycle is R-52's cycle of days holding now (cycles start at 04:00 UTC+8 on Mondays from 2024-05-20).
func TowerCycle(now, days int64) (int64, int64) { return Cycle(now, days) }

// Info is TowerRequest / TowerSeasonUpdateRequest.
func (t *Tower) Info(tb *data.DataTables, nowSeconds int64) *protocol.TowerInfo {
	season := TowerCurrentSeason(tb, t.pick())
	begin, end := TowerCycle(nowSeconds, towerCycleDays)
	res := &protocol.TowerInfo{
		CurrentSeason:       season,
		DataSeason:          season,
		BeginTime:           begin,
		EndTime:             end,
		MaxUnlockDifficulty: TowerDifficulties[len(TowerDifficulties)-1],
	}
	for _, d := range TowerDifficulties {
		diff := &protocol.TowerDifficultyPb{Difficulty: d}
		var floors int32
		for area := int32(1); area <= 8; area++ {
			a := &protocol.TowerAreaPb{AreaNum: area}
			for _, f := range tb.TowerConfig {
				if f.Difficulty != d || f.AreaNum != area || !TowerListed(tb, f, t.pick()) {
					continue
				}
				fp := t.floorPb(f)
				for k := int32(0); k < fp.Star; k++ {
					fp.StarIndex = append(fp.StarIndex, k)
				}
				a.TowerFloors = append(a.TowerFloors, fp)
				floors++
			}
			if len(a.TowerFloors) > 0 {
				diff.TowerAreas = append(diff.TowerAreas, a)
			}
		}
		rewards := towerRewardsOf(tb, d, t.pick())
		for i := range rewards {
			if t.claimed.Has(d*100 + int32(i)) {
				diff.RewardIndex = append(diff.RewardIndex, int32(i))
			}
		}
		diff.MaxStar = floors * TowerStarsPerPass
		res.TowerDifficulties = append(res.TowerDifficulties, diff)
	}
	return res
}

// Start is TowerStartRequest: the floor's dungeon (the caller transfers the player).
func (t *Tower) Start(tb *data.DataTables, configID int32) (int32, bool) {
	f, ok := tb.TowerConfigById[configID]
	if !ok {
		return 0, false
	}
	id := configID
	t.Current = &id
	common.Info("tower", fmt.Sprintf("tower floor %d (difficulty %d, area %d, floor %d): dungeon %d", f.Id, f.Difficulty, f.AreaNum, f.Floor, f.InstanceId))
	return f.InstanceId, true
}

// IsTowerFloorDungeon: inst is the dungeon of a 深塔 floor (of any season: seasons reuse dungeons).
func IsTowerFloorDungeon(tb *data.DataTables, inst int32) bool {
	for _, f := range tb.TowerConfig {
		if f.InstanceId == inst {
			return true
		}
	}
	return false
}

// TowerFloorOf is the floor dungeon inst runs: the floor being climbed, else the floor with that
// dungeon in season pick (R-160c), else the newest season's.
func TowerFloorOf(floors []*data.TowerConfig, climbing *int32, inst, pick int32) *data.TowerConfig {
	var best, picked *data.TowerConfig
	for _, f := range floors {
		if f.InstanceId != inst {
			continue
		}
		if climbing != nil && f.Id == *climbing {
			return f
		}
		if pick > 0 && f.Season == pick && picked == nil {
			picked = f
		}
		if best == nil || f.Season > best.Season {
			best = f
		}
	}
	if picked != nil {
		return picked
	}
	return best
}

// FloorOfDungeon is R-126, R-127: the floor the dungeon inst runs.
func (t *Tower) FloorOfDungeon(tb *data.DataTables, inst int32) *data.TowerConfig {
	return TowerFloorOf(tb.TowerConfig, t.Current, inst, t.pick())
}

// FloorPlay is R-127: the level play of the floor dungeon: the floor's own, when the game has one.
func (t *Tower) FloorPlay(tb *data.DataTables, prefabs *quests.PrefabGroups, inst int32) (int32, bool) {
	d, ok := tb.InstanceDungeonById[inst]
	if !ok || d.InstSubType != TowerDungeonSubType {
		return 0, false
	}
	f := t.FloorOfDungeon(tb, inst)
	if f == nil {
		return 0, false
	}
	return prefabs.FloorPlay(f.Id)
}

// ----- waves (R-56, R-85, R-126) -----

// Waves are R-56: the monsters of the floor dungeon inst runs, as waves of TemplateConfig ids.
// R-126: commons and elites together in the first wave, then each boss alone.
func (t *Tower) Waves(wd *world.WorldData, hasTowerEntity func(string) bool, inst int32) [][]int32 {
	f := t.FloorOfDungeon(wd.Tables, inst)
	if f == nil {
		return nil
	}
	var pack, bosses []int32
	for _, m := range f.ShowMonsters {
		monster, ok := wd.Tables.MonsterInfoById[m]
		if !ok {
			common.Warn("tower", fmt.Sprintf("floor %d: monster %d not in MonsterInfo", f.Id, m))
			continue
		}
		tpl := wd.Template(monster.MonsterEntityID)
		if tpl == nil {
			common.Warn("tower", fmt.Sprintf("floor %d: no template for %s", f.Id, monster.MonsterEntityID))
			continue
		}
		id := TowerVariant(wd.Tables.TemplateConfig, tpl, hasTowerEntity)
		if id != tpl.Id {
			common.Info("tower", fmt.Sprintf("floor %d: monster %d (%s) template %d has no tower version, spawning %d", f.Id, m, monster.MonsterEntityID, tpl.Id, id))
		}
		if monster.RarityId >= TowerBossRarity {
			bosses = append(bosses, id)
		} else {
			pack = append(pack, id)
		}
	}
	waves := GroupTowerWaves(pack, bosses)
	common.Info("tower", fmt.Sprintf("floor %d (season %d, dungeon %d): %d waves", f.Id, f.Season, inst, len(waves)))
	return waves
}

func GroupTowerWaves(pack, bosses []int32) [][]int32 {
	var waves [][]int32
	if len(pack) > 0 {
		waves = append(waves, append([]int32(nil), pack...))
	}
	for _, b := range bosses {
		waves = append(waves, []int32{b})
	}
	return waves
}

func templateModel(t *data.TemplateConfig) int32 {
	c, ok := t.ComponentsData.(*data.Object)
	if !ok {
		return 0
	}
	m, ok := c.Get("ModelComponent").(*data.Object)
	if !ok {
		return 0
	}
	mt, ok := m.Get("ModelType").(*data.Object)
	if !ok {
		return 0
	}
	if n, ok := mt.Get("ModelId").(data.Number); ok {
		return int32(n)
	}
	return 0
}

// TowerVariant is R-85, R-126: the template a floor spawns for a preview monster's template. A
// preview that names a story version (任务专用) or a 复刷专用 world boss without a tower prefab
// entity spawns the same model's tower version (爬塔专用, or one with a tower prefab entity).
func TowerVariant(templates []*data.TemplateConfig, t *data.TemplateConfig, hasTowerEntity func(string) bool) int32 {
	if hasTowerEntity(t.BlueprintType) {
		return t.Id
	}
	model := templateModel(t)
	if model == 0 {
		return t.Id
	}
	var otherTower, storyTower *int32
	for _, o := range templates {
		if templateModel(o) != model || o.Id == t.Id {
			continue
		}
		climb := strings.Contains(o.Name, "爬塔专用")
		if hasTowerEntity(o.BlueprintType) {
			if climb {
				return o.Id
			}
			if otherTower == nil {
				id := o.Id
				otherTower = &id
			}
		} else if climb && storyTower == nil {
			id := o.Id
			storyTower = &id
		}
	}
	if otherTower != nil {
		return *otherTower
	}
	if strings.Contains(t.Name, "任务专用") && storyTower != nil {
		return *storyTower
	}
	return t.Id
}

// ----- formation (R-86) -----

// UseFormation is R-86: TowerStartRequest.Formation, the team picked in the 深塔 team screen,
// goes into the current formation slot; the player's own team is kept until RestoreFormation.
// Only owned roles, no repeats. True when it changed the formation (the caller saves it).
func (t *Tower) UseFormation(player *save.PlayerData, floorID int32, wanted []*protocol.TowerRolePb) bool {
	var roles [TowerTeamSize]int32
	count := 0
	for _, w := range wanted {
		if count >= TowerTeamSize {
			break
		}
		if !player.Roles.Has(w.RoleId) {
			common.Warn("tower", fmt.Sprintf("floor %d: formation role %d is not owned", floorID, w.RoleId))
			continue
		}
		dup := false
		for i := 0; i < count; i++ {
			if roles[i] == w.RoleId {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		roles[count] = w.RoleId
		count++
	}
	if count == 0 {
		return false
	}
	info := player.FormationInfo
	slot := info.CurFormation
	if slot < 0 || int(slot) >= len(info.Formations) {
		return false
	}
	cur := info.Formations[slot]
	t.floorTeams.Set(floorID, roles)
	// A second floor from inside the tower must not back up the team of the floor before it.
	if t.OwnTeam == nil {
		b := &TowerBackup{Slot: slot, CurRole: cur.CurRole}
		for i, r := range cur.Roles {
			if r != nil {
				b.Roles[i] = r.RoleID
			}
		}
		t.OwnTeam = b
	}
	var slots [save.RolesPerFormation]*save.FormationRoleSave
	for i := 0; i < count; i++ {
		entity := int64(-1)
		for _, r := range cur.Roles {
			if r != nil && r.RoleID == roles[i] {
				entity = r.EntityID
				break
			}
		}
		slots[i] = &save.FormationRoleSave{RoleID: roles[i], EntityID: entity}
	}
	cur.Roles = slots
	cur.CurRole = roles[0]
	t.save()
	common.Info("tower", fmt.Sprintf("floor %d: formation slot %d is now [%s] (the player's own team: [%s])", floorID, slot, joinInts(roles[:count]), joinInts(t.OwnTeam.Roles[:])))
	return true
}

// RestoreFormation is R-86: a scene that is not a 深塔 floor: the player's own team goes back into
// its slot. True when it changed the formation.
func (t *Tower) RestoreFormation(player *save.PlayerData, tb *data.DataTables, instanceID int32) bool {
	b := t.OwnTeam
	if b == nil || IsTowerFloorDungeon(tb, instanceID) {
		return false
	}
	info := player.FormationInfo
	if b.Slot < 0 || int(b.Slot) >= len(info.Formations) {
		return false
	}
	var slots [save.RolesPerFormation]*save.FormationRoleSave
	for i := 0; i < TowerTeamSize; i++ {
		if b.Roles[i] != 0 {
			slots[i] = &save.FormationRoleSave{RoleID: b.Roles[i], EntityID: -1}
		}
	}
	cur := info.Formations[b.Slot]
	cur.Roles = slots
	cur.CurRole = b.CurRole
	info.CurFormation = b.Slot
	t.OwnTeam = nil
	t.save()
	common.Info("tower", fmt.Sprintf("out of the tower (instance %d): formation slot %d is the player's own team [%s] again", instanceID, b.Slot, joinInts(b.Roles[:])))
	return true
}

func joinInts(v []int32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = itoa(x)
	}
	return strings.Join(parts, ", ")
}

// ----- the fight and its result (R-84, R-127) -----

// StartClock: a 深塔 floor's fight began; it has limitSeconds (its play's QuestFailed FailTime).
func (t *Tower) StartClock(nowMs int64, limitSeconds float64) {
	t.clock = &towerClock{startedMs: nowMs, limitMs: int64(limitSeconds * 1000)}
	common.Info("tower", fmt.Sprintf("floor countdown: %s s", f0d(limitSeconds)))
}

// OnFinished: a dungeon finished: the floor being climbed is passed or failed. R-84: settled
// however the player got in (logged in inside a floor: the floor the waves came from).
func (t *Tower) OnFinished(s *PlayerSession, inst int32, ok bool, nowMs int64) {
	tb := s.Data.Tables
	var floorID int32
	if t.Current != nil {
		floorID = *t.Current
	} else if f := TowerFloorOf(tb.TowerConfig, nil, inst, t.pick()); f != nil {
		floorID = f.Id
	} else {
		return
	}
	f, has := tb.TowerConfigById[floorID]
	if !has || f.InstanceId != inst {
		return
	}
	t.Current = nil
	var remaining *int64
	if c := t.clock; c != nil {
		r := int64(math.Floor(float64(c.startedMs+c.limitMs-nowMs) / 1000.0))
		remaining = &r
	}
	t.clock = nil
	var earned []int32
	if ok {
		earned = EarnedStars(tb, f, remaining)
		if int32(len(earned)) > t.stars.At(floorID) {
			t.stars.Set(floorID, int32(len(earned)))
		}
		t.save()
	}
	fp := t.floorPb(f)
	// The result screen shows this run's stars; the floor keeps its best.
	fp.StarIndex = append(fp.StarIndex, earned...)
	s.Sink().Push(&protocol.TowerFloorUpdateNotify{TowerFloors: []*protocol.TowerFloorPb{fp}})
	s.Sink().Push(&protocol.TowerEndNotify{Success: ok, CurrentFloorData: fp})
	left := int64(-1)
	if remaining != nil {
		left = *remaining
	}
	result := "failed"
	if ok {
		result = "passed"
	}
	common.Info("tower", fmt.Sprintf("tower floor %d: %s, %d s left, stars [%s] (best %d)", floorID, result, left, joinInts(earned), fp.Star))
}

// OnTimeUp: the floor's countdown ran out: failed, the result screen, no stars.
func (t *Tower) OnTimeUp(s *PlayerSession, nowMs int64) {
	if s.Scene == nil || !IsTowerFloorDungeon(s.Data.Tables, s.Scene.InstanceID) {
		return
	}
	inst := s.Scene.InstanceID
	common.Info("tower", fmt.Sprintf("floor in dungeon %d: time is up", inst))
	s.Sink().Push(&protocol.InstResultNotify{Id: inst, Succ: false, IsRecReward: false})
	t.OnFinished(s, inst, false, nowMs)
}

// EarnedStars is R-127: indexes of the floor's star conditions this clear meets. Without a
// countdown the time conditions count as met; "nobody died" counts as met.
func EarnedStars(tb *data.DataTables, f *data.TowerConfig, remainingSeconds *int64) []int32 {
	if len(f.TargetConfig) == 0 {
		return []int32{0, 1, 2}
	}
	var earned []int32
	for k, id := range f.TargetConfig {
		if target, ok := tb.TowerTargetById[id]; ok && TowerTargetMet(target, remainingSeconds) {
			earned = append(earned, int32(k))
		}
	}
	return earned
}

func TowerTargetMet(t *data.TowerTarget, remainingSeconds *int64) bool {
	var param int64
	if len(t.Params) > 0 {
		param = int64(t.Params[0])
	}
	if t.TargetType == 1 {
		return remainingSeconds == nil || *remainingSeconds >= param
	}
	return true
}

// ClaimRewards is TowerRewardRequest: star rewards of a difficulty, once each, when its stars
// reach them. Returns the indexes claimed.
func (t *Tower) ClaimRewards(s *PlayerSession, difficulty int32, indexes []int32) []int32 {
	tb := s.Data.Tables
	rewards := towerRewardsOf(tb, difficulty, t.pick())
	have := t.difficultyStars(tb, difficulty)
	got := []int32{}
	for _, i := range indexes {
		if i < 0 || int(i) >= len(rewards) || rewards[i] == nil {
			continue
		}
		r := rewards[i]
		key := difficulty*100 + i
		if t.claimed.Has(key) || have < r.Item1 {
			continue
		}
		if r.Item2 > 0 {
			s.Actions().GrantDrop(r.Item2, 0, 0, nil)
		}
		t.claimed.Set(key, struct{}{})
		got = append(got, i)
	}
	t.save()
	common.Info("tower", fmt.Sprintf("tower difficulty %d: %d stars, rewards [%s] claimed", difficulty, have, joinInts(got)))
	return got
}
