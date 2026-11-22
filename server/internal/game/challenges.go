// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/quests"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// ----- Boss 连战 (R-37) -----

// BossRush is R-37's Boss 连战 (BossRushActivity, activity type 14). The newest activity's
// levels are all open; each is an ordinary dungeon. A successful finish scores it (kills + time
// left), keeps the best per level and sends BossRushResultNotify and BossRushLevelInfoNotify.
// Score rewards open once the sum of the best scores reaches them. Saved in boss_rush:
// "L <inst> <best> <level reward 0|1>", "S <score reward id>". Ported from our C# Game/Challenges.cs.
type BossRush struct {
	path         string
	levels       omap.Map[int32, *bossRushLevel]
	scoreClaimed omap.Map[int32, struct{}]
	enteredAt    *bossRushEntered
}

type bossRushLevel struct {
	best    int32
	claimed bool
}

type bossRushEntered struct {
	inst int32
	at   int64
}

const (
	BossRushActivityType = 14
	BossRushTimeLimit    = 600
	BossRushPerSecond    = 5
	BossRushPerBoss      = 1000
)

func NewBossRush(files *save.PlayerFiles) *BossRush {
	b := &BossRush{path: files.PathOf("boss_rush")}
	for _, f := range readLineFile(b.path) {
		id, ok := lineInt(f, 1)
		if !ok {
			continue
		}
		switch f[0] {
		case "L":
			best, _ := lineInt(f, 2)
			b.levels.Set(id, &bossRushLevel{best: best, claimed: len(f) > 3 && f[3] == "1"})
		case "S":
			b.scoreClaimed.Set(id, struct{}{})
		}
	}
	return b
}

// Reset forgets every level and score reward (R-160: a new window).
func (b *BossRush) Reset() {
	b.levels.Clear()
	b.scoreClaimed.Clear()
	b.save()
}

func (b *BossRush) save() {
	var sb strings.Builder
	for inst, l := range b.levels.All() {
		fmt.Fprintf(&sb, "L %d %d %s\n", inst, l.best, b01(l.claimed))
	}
	for id := range b.scoreClaimed.All() {
		fmt.Fprintf(&sb, "S %d\n", id)
	}
	save.WriteAtomic(b.path, sb.String())
}

// listedBossRush is the plan's boss rush (0: none listed; R-160, automatic: the newest
// ActivityId with levels).
func listedBossRush(s *PlayerSession) int32 {
	id, _ := s.Plan().Listed(SlotBossRush)
	return id
}

func bossRushLevelOf(t *data.DataTables, id, inst int32) *data.BossRushActivity {
	for _, l := range t.BossRushActivity {
		if l.ActivityId == id && l.InstId == inst {
			return l
		}
	}
	return nil
}

func (b *BossRush) Best(inst int32) int32 {
	if l, ok := b.levels.Get(inst); ok {
		return l.best
	}
	return 0
}

func (b *BossRush) totalScore() int32 {
	var sum int32
	for _, l := range b.levels.All() {
		sum += l.best
	}
	return sum
}

func (b *BossRush) levelStatus(l *data.BossRushActivity) protocol.BossRushRewardClaimStatus {
	st, ok := b.levels.Get(l.InstId)
	if ok && st.claimed {
		return protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimed
	}
	if ok && st.best > 0 && l.RewardId > 0 {
		return protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimable
	}
	return protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Incomplete
}

func (b *BossRush) scoreStatus(id, need, total int32) protocol.BossRushRewardClaimStatus {
	switch {
	case b.scoreClaimed.Has(id):
		return protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimed
	case total >= need:
		return protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimable
	}
	return protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Incomplete
}

func (b *BossRush) lists(t *data.DataTables, id int32) ([]*protocol.BossRushLevelData, []*protocol.BossRushScoreRewardData) {
	levels := []*protocol.BossRushLevelData{}
	for _, l := range t.BossRushActivity {
		if l.ActivityId == id {
			levels = append(levels, &protocol.BossRushLevelData{InstId: l.InstId, IsOpen: true, Score: b.Best(l.InstId), LevelRewardClaimStatus: b.levelStatus(l)})
		}
	}
	total := b.totalScore()
	scores := []*protocol.BossRushScoreRewardData{}
	for _, s := range t.BossRushScore {
		scores = append(scores, &protocol.BossRushScoreRewardData{RewardId: s.Id, RewardClaimStatus: b.scoreStatus(s.Id, s.Score, total)})
	}
	return levels, scores
}

// Data is the activity page's data.
func (b *BossRush) Data(t *data.DataTables, id int32) *protocol.BossRushActivityData {
	levels, scores := b.lists(t, id)
	return &protocol.BossRushActivityData{BossRushLevelInfo: levels, BossRushScoreScoreRewardInfo: scores, UnlockedBuffIndices: []int32{0, 1, 2, 3}}
}

func (b *BossRush) pushLevelInfo(s *PlayerSession) {
	id := listedBossRush(s)
	levels, scores := b.lists(s.Data.Tables, id)
	s.Sink().Push(&protocol.BossRushLevelInfoNotify{
		ActivityId:                   id,
		BossRushLevelInfo:            levels,
		BossRushScoreScoreRewardInfo: scores,
		UnlockedBuffIndices:          []int32{0, 1, 2, 3},
	})
}

// NoteEnter: entering a dungeon: the clock of a boss rush level starts.
func (b *BossRush) NoteEnter(inst int32, nowSeconds int64) {
	b.enteredAt = &bossRushEntered{inst, nowSeconds}
}

// BossRushScore is the kill score, the time score and the seconds left.
func BossRushScore(bossCount int32, elapsedSeconds int64) (kill, timeScore, left int32) {
	l := min(max(BossRushTimeLimit-elapsedSeconds, 0), BossRushTimeLimit)
	return max(bossCount, 1) * BossRushPerBoss, int32(l) * BossRushPerSecond, int32(l)
}

// OnFinished: a dungeon finished successfully: scored when it is a level of the listed activity.
func (b *BossRush) OnFinished(s *PlayerSession, inst int32) {
	l := bossRushLevelOf(s.Data.Tables, listedBossRush(s), inst)
	if l == nil {
		return
	}
	now := s.Clock.Seconds()
	started := now
	if e := b.enteredAt; e != nil && e.inst == inst {
		started = e.at
	}
	kill, ts, left := BossRushScore(l.BossCount, now-started)
	total := kill + ts
	st, ok := b.levels.Get(inst)
	if !ok {
		st = &bossRushLevel{}
		b.levels.Set(inst, st)
	}
	if total > st.best {
		st.best = total
	}
	b.save()
	common.Info("boss_rush", fmt.Sprintf("boss rush %d: %d + %d = %d (best %d)", inst, kill, ts, total, st.best))
	s.Sink().Push(&protocol.BossRushResultNotify{
		ActivityId: l.ActivityId, InstId: inst, KilMonsterScore: kill, TimerScore: ts, TechnicalScore: 0, HighScore: st.best, LeftTime: left,
	})
	b.pushLevelInfo(s)
}

// Claim is BossRushClaimRewardRequest: a level reward (RewardId = the level's InstId) or a
// score reward (a BossRushScore Id).
func (b *BossRush) Claim(s *PlayerSession, rewardID int32, kind protocol.BossRushRewardType) protocol.ErrorCode {
	t := s.Data.Tables
	lock := protocol.ErrorCode_ErrorCode_ErrActivityLock
	if kind == protocol.BossRushRewardType_BossRushRewardType_Score {
		sc, ok := t.BossRushScoreById[rewardID]
		if !ok || b.scoreStatus(sc.Id, sc.Score, b.totalScore()) != protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimable {
			return lock
		}
		if sc.RewardId > 0 {
			s.Actions().GrantDrop(sc.RewardId, 0, 0, nil)
		}
		b.scoreClaimed.Set(sc.Id, struct{}{})
	} else {
		l := bossRushLevelOf(t, listedBossRush(s), rewardID)
		if l == nil {
			return lock
		}
		st, ok := b.levels.Get(l.InstId)
		if !ok || b.levelStatus(l) != protocol.BossRushRewardClaimStatus_BossRushRewardClaimStatus_Claimable {
			return lock
		}
		s.Actions().GrantDrop(l.RewardId, 0, 0, nil)
		st.claimed = true
	}
	b.save()
	name := "Level"
	if kind == protocol.BossRushRewardType_BossRushRewardType_Score {
		name = "Score"
	}
	common.Info("boss_rush", fmt.Sprintf("boss rush reward %d (%s) claimed", rewardID, name))
	b.pushLevelInfo(s)
	return protocol.ErrorCode_ErrorCode_Success
}

// ----- 领主挑战 (R-37) -----

// LordGyms is R-37's 领主挑战 (LordGym). Every difficulty is unlocked. LordGymBeginRequest
// starts the difficulty's level play with Self Level = Difficulty; when the play succeeds the
// player gets RewardId and a pass record (LordGymLevelPlayResultNotify). Saved in lord_gym:
// "<gym> <pass time> <read 0|1>".
type LordGyms struct {
	path    string
	records omap.Map[int32, *lordGymRecord]
	current *lordGymRun
}

type lordGymRecord struct {
	passTime int32
	read     bool
}

type lordGymRun struct {
	gym, play int32
	started   int64
}

func NewLordGyms(files *save.PlayerFiles) *LordGyms {
	g := &LordGyms{path: files.PathOf("lord_gym")}
	for _, f := range readLineFile(g.path) {
		if id, ok := lineInt(f, 0); ok {
			p, _ := lineInt(f, 1)
			g.records.Set(id, &lordGymRecord{passTime: p, read: len(f) > 2 && f[2] == "1"})
		}
	}
	return g
}

func (g *LordGyms) save() {
	var sb strings.Builder
	for id, r := range g.records.All() {
		fmt.Fprintf(&sb, "%d %d %s\n", id, r.passTime, b01(r.read))
	}
	save.WriteAtomic(g.path, sb.String())
}

// Info is LordGymInfoResponse: everything unlocked, the entrances' 28-day cycle (R-52), the
// read and passed gyms, the highest difficulty passed per entrance.
func (g *LordGyms) Info(t *data.DataTables, now int64) *protocol.LordGymInfoResponse {
	begin, end := Cycle(now, LordGymDays)
	return g.InfoIn(t, begin, end)
}

// InfoIn is Info with the entrances open in [begin, end) (R-160: the panel's window).
func (g *LordGyms) InfoIn(t *data.DataTables, begin, end int64) *protocol.LordGymInfoResponse {
	res := &protocol.LordGymInfoResponse{}
	for _, gym := range t.LordGym {
		if gym.IsDebug {
			continue
		}
		res.UnlockLoadGymIds = append(res.UnlockLoadGymIds, gym.Id)
		r, ok := g.records.Get(gym.Id)
		if !ok {
			continue
		}
		if r.read {
			res.ReadLoadGymIds = append(res.ReadLoadGymIds, gym.Id)
		}
		if r.passTime > 0 {
			res.LordGymPassRecords = append(res.LordGymPassRecords, &protocol.LordGymPassRecord{LoadGymId: gym.Id, PassTime: r.passTime})
		}
	}
	for _, e := range t.LordGymEntrance {
		if e.IsDebug {
			continue
		}
		res.EntranceInfos = append(res.EntranceInfos, &protocol.LordGymEntranceInfo{Id: e.Id, EffectBeginTime: begin, EffectEndTime: end})
		res.LordGymGroupInfos = append(res.LordGymGroupInfos, &protocol.LordGymGroupInfo{GroupId: e.Id, PassDiff: g.passedDifficulty(t, e.LordGymList)})
	}
	return res
}

func (g *LordGyms) passedDifficulty(t *data.DataTables, gyms []int32) int32 {
	var best int32
	for _, id := range gyms {
		r, ok := g.records.Get(id)
		row, known := t.LordGymById[id]
		if ok && r.passTime > 0 && known && row.Difficulty > best {
			best = row.Difficulty
		}
	}
	return best
}

func (g *LordGyms) MarkRead(gymID int32) {
	r, ok := g.records.Get(gymID)
	if !ok {
		r = &lordGymRecord{}
		g.records.Set(gymID, r)
	}
	r.read = true
	g.save()
}

// Begin is LordGymBeginRequest: (re)starts the difficulty's level play. False when the gym is unknown.
func (g *LordGyms) Begin(s *PlayerSession, gymID int32) bool {
	gym, ok := s.Data.Tables.LordGymById[gymID]
	e := s.Quests()
	if !ok || gym.IsDebug || gym.PlayId == 0 || e == nil {
		return false
	}
	e.StopLevelPlay(gym.PlayId)
	g.current = &lordGymRun{gym: gymID, play: gym.PlayId, started: s.Clock.Seconds()}
	common.Info("lord_gym", fmt.Sprintf("lord gym %d (difficulty %d): level play %d", gymID, gym.Difficulty, gym.PlayId))
	e.StartLevelPlay(gym.PlayId, quests.LevelPlayVar{Name: "Level", Value: float64(gym.Difficulty)})
	return true
}

// IsCurrentPlay: levelPlay is the 领主挑战 being fought (its failure countdown counts, R-37f).
func (g *LordGyms) IsCurrentPlay(levelPlay int32) bool {
	return g.current != nil && g.current.play == levelPlay
}

func (g *LordGyms) OnLevelPlayFailed(s *PlayerSession, levelPlay int32) {
	c := g.current
	if c == nil || c.play != levelPlay {
		return
	}
	g.current = nil
	common.Info("lord_gym", fmt.Sprintf("lord gym %d failed", c.gym))
	s.Sink().Push(&protocol.LordGymLevelPlayResultNotify{IsSuccess: false, FailLoadGymId: c.gym})
}

func (g *LordGyms) OnLevelPlayFinished(s *PlayerSession, levelPlay int32) {
	c := g.current
	if c == nil {
		return
	}
	gym, ok := s.Data.Tables.LordGymById[c.gym]
	if !ok || gym.PlayId != levelPlay {
		return
	}
	g.current = nil
	took := int32(min(max(s.Clock.Seconds()-c.started, 1), 1<<31-1))
	r, has := g.records.Get(c.gym)
	if !has {
		r = &lordGymRecord{}
		g.records.Set(c.gym, r)
	}
	newRecord := r.passTime == 0 || took < r.passTime
	if newRecord {
		r.passTime = took
	}
	g.save()
	var given []*protocol.RewardItemInfo
	if gym.RewardId > 0 {
		s.Actions().GrantDrop(gym.RewardId, int64(c.gym), 0, &given)
	}
	common.Info("lord_gym", fmt.Sprintf("lord gym %d passed in %d s (record %s), %d rewards", c.gym, took, boolWord(newRecord), len(given)))
	n := &protocol.LordGymLevelPlayResultNotify{
		IsSuccess:         true,
		IsNewRecord:       newRecord,
		LordGymPassRecord: &protocol.LordGymPassRecord{LoadGymId: c.gym, PassTime: r.passTime},
		PassTime:          took,
	}
	for _, i := range given {
		n.ItemDatas = append(n.ItemDatas, &protocol.ItemData{ItemId: i.ItemId, ItemIncId: i.IncrId, Count: i.Count})
	}
	s.Sink().Push(n)
}

// boolWord is C#'s bool.ToString().
func boolWord(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// ----- fishing (R-115) -----

const (
	// FishingStartCabinShape is the first cabin of the tech tree (the smallest of 700001..700004).
	FishingStartCabinShape = 700001
	// FishingStartSkinID is FishingShipSkin 1, the default boat.
	FishingStartSkinID = 1
)

// FishingInfo is R-115: the fishing activity's data. The 3.8 client reads ShipInfo, its
// CabinInfo and IllustratedInfo without null checks, and looks CabinShape up as a
// FishingGridItemShape id: never 0. The server keeps no fishing progress: a new player's boat.
func FishingInfo() *protocol.FishingInfo {
	return &protocol.FishingInfo{
		ShipInfo:        &protocol.FishingShipInfo{SkinId: FishingStartSkinID, CabinInfo: &protocol.CabinInfo{CabinShape: FishingStartCabinShape}},
		IllustratedInfo: &protocol.FishingIllustratedInfo{},
	}
}
