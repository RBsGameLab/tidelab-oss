// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// Dungeons is R-4a: entering and leaving instance dungeons (specs/dungeons.md). The Sola guide
// or an entrance sends EnterInstRequest{InstanceId}; a quest's TeleportDungeon action can be
// offered with TeleportDungeonActionNotify and the client answers with
// TeleportDungeonActionRequest. LeaveInstRequest and the ExitDungeon action go back to where the
// player entered from. Saved in player/<id>/dungeon: one line
// "<dungeon> <back instance> <x> <y> <z>", empty when outside. Ported from our C# Game/Dungeons.cs.
type Dungeons struct {
	path      string
	offers    map[int32]int32
	nextOffer int32
	// Current is the dungeon the player is in (nil in the open world or a story instance a quest entered).
	Current *DungeonInside
}

const (
	DungeonFileName = "dungeon"
	// DungeonWorldInstance is the open world: where leaving goes when nothing better is known.
	DungeonWorldInstance = 8
	// RepeatableRun is InstanceDungeon.InstType of a repeatable run (a story instance is 1).
	RepeatableRun = 2
)

// DungeonInside is where the player came from, so leaving goes back there.
type DungeonInside struct {
	Dungeon      int32
	BackInstance int32
	BackPos      world.Vec3
}

func NewDungeons(files *save.PlayerFiles) *Dungeons {
	d := &Dungeons{path: files.PathOf(DungeonFileName), offers: map[int32]int32{}}
	if b, err := os.ReadFile(d.path); err == nil {
		d.Current = ParseDungeonInside(string(b))
	}
	return d
}

// IsEntered: the player is inside dungeonID, entered through this module.
func (d *Dungeons) IsEntered(dungeonID int32) bool {
	return d.Current != nil && d.Current.Dungeon == dungeonID
}

func FormatDungeonInside(i DungeonInside) string {
	return fmt.Sprintf("%d %d %s %s %s\n", i.Dungeon, i.BackInstance, f0(i.BackPos.X), f0(i.BackPos.Y), f0(i.BackPos.Z))
}

func ParseDungeonInside(text string) *DungeonInside {
	parts := strings.FieldsFunc(text, isNetSpace)
	if len(parts) < 5 {
		return nil
	}
	var n [5]float32
	for i := range n {
		v, err := strconv.ParseFloat(parts[i], 32)
		if err != nil {
			return nil
		}
		n[i] = float32(v)
	}
	return &DungeonInside{Dungeon: int32(n[0]), BackInstance: int32(n[1]), BackPos: world.Vec3{X: n[2], Y: n[3], Z: n[4]}}
}

func (d *Dungeons) save() {
	text := ""
	if d.Current != nil {
		text = FormatDungeonInside(*d.Current)
	}
	save.WriteAtomic(d.path, text)
}

func dungeonBornPos(d *data.InstanceDungeon) world.Vec3 {
	if len(d.BornPosition) >= 3 {
		return world.Vec3{X: float32(d.BornPosition[0]), Y: float32(d.BornPosition[1]), Z: float32(d.BornPosition[2])}
	}
	return world.Vec3{}
}

// dungeonEntityPos is R-50c: where an entity of mapID stands (a TeleportDungeon's LocationEntityId).
func dungeonEntityPos(s *PlayerSession, mapID int32, entityID int64) (world.Vec3, bool) {
	if entityID <= 0 || entityID > 1<<31-1 {
		return world.Vec3{}, false
	}
	row := s.Data.Tables.LevelEntity(mapID, int32(entityID))
	if row == nil || len(row.Transform) == 0 || row.Transform[0] == nil {
		return world.Vec3{}, false
	}
	p := row.Transform[0]
	return world.Vec3{X: float32(p.X) / 100, Y: float32(p.Y) / 100, Z: float32(p.Z) / 100}, true
}

func (s *PlayerSession) playerPosOrZero() world.Vec3 {
	if s.Scene != nil {
		return s.Scene.PlayerPos
	}
	return world.Vec3{}
}

// Enter moves the player into dungeonID (queues the transfer) and remembers where they were.
// R-50c: an open-world instance is not a dungeon: entering it ends the visit and lands on the
// position entity. False when the dungeon is unknown.
func (d *Dungeons) Enter(s *PlayerSession, dungeonID int32, posEntityID int64) bool {
	dg, ok := s.Data.Tables.InstanceDungeonById[dungeonID]
	if !ok {
		return false
	}
	target, ok := dungeonEntityPos(s, dg.MapConfigId, posEntityID)
	if !ok {
		target = dungeonBornPos(dg)
	}
	if world.HasWorldSpawns(dungeonID) {
		if d.Current != nil {
			if e := s.Quests(); e != nil {
				e.StopLevelPlays()
			}
			d.Current = nil
			d.save()
		}
		common.Info("dungeon", fmt.Sprintf("to open world %d at %s,%s,%s (entity %d)", dungeonID, f0(target.X), f0(target.Y), f0(target.Z), posEntityID))
		s.QueueTransfer(dungeonID, [3]float32{target.X, target.Y, target.Z}, false)
		return true
	}
	// R-37: the clock of a boss rush level starts.
	s.Activities().BossRush.NoteEnter(dungeonID, s.Clock.Seconds())
	// Entering from inside another dungeon keeps the original way back.
	back := d.Current
	if back == nil {
		inst := s.State.LastScene
		if s.Scene != nil {
			inst = s.Scene.InstanceID
		}
		back = &DungeonInside{Dungeon: dungeonID, BackInstance: inst, BackPos: s.playerPosOrZero()}
	}
	d.Current = &DungeonInside{Dungeon: dungeonID, BackInstance: back.BackInstance, BackPos: back.BackPos}
	d.save()
	common.Info("dungeon", fmt.Sprintf("entering dungeon %d (map %d) at %s,%s,%s", dungeonID, dg.MapConfigId, f0(target.X), f0(target.Y), f0(target.Z)))
	s.QueueTransfer(dungeonID, [3]float32{target.X, target.Y, target.Z}, false)
	return true
}

// OnJoined runs after joining the dungeon's scene: a repeatable run starts fresh (its map's
// entity states and wakes go, except what a running quest waits for; R-80c), then its level
// play starts.
func (d *Dungeons) OnJoined(s *PlayerSession) {
	c := d.Current
	if c == nil || s.Scene == nil || c.Dungeon != s.Scene.InstanceID {
		return
	}
	dg, ok := s.Data.Tables.InstanceDungeonById[c.Dungeon]
	if !ok {
		return
	}
	ws := s.World.State
	e := s.Quests()
	if dg.InstType != RepeatableRun {
		common.Info("dungeon", fmt.Sprintf("story instance %d: its entity states and wakes stay (not a repeatable run)", c.Dungeon))
	} else {
		forgot := 0
		for _, i := range s.Data.Tables.LevelEntityRowsOfMap(dg.MapConfigId) {
			row := s.Data.Tables.LevelEntityConfig[i]
			if _, has := ws.Get(int64(row.EntityId)); !has || (e != nil && e.UsedByRunning(int64(row.EntityId))) {
				continue
			}
			ws.Forget(int64(row.EntityId))
			forgot++
		}
		if forgot > 0 {
			ws.Save()
			common.Info("dungeon", fmt.Sprintf("dungeon %d: reset %d entity states", c.Dungeon, forgot))
		}
	}
	var lp int32
	has := false
	if e != nil {
		lp, has = e.LevelPlayForDungeon(c.Dungeon)
	}
	if !has {
		common.Info("dungeon", fmt.Sprintf("dungeon %d: no level play on map %d", c.Dungeon, dg.MapConfigId))
		return
	}
	if !e.StartLevelPlay(lp) {
		common.Warn("dungeon", fmt.Sprintf("dungeon %d: level play %d failed to start", c.Dungeon, lp))
	}
}

// Rechallenge (重新挑战): reload the dungeon; the scene join restarts its level play and resets
// its entities. False when not inside.
func (d *Dungeons) Rechallenge(s *PlayerSession) bool {
	c := d.Current
	if c == nil {
		return false
	}
	dg, ok := s.Data.Tables.InstanceDungeonById[c.Dungeon]
	if !ok {
		return false
	}
	if e := s.Quests(); e != nil {
		e.StopLevelPlays()
	}
	common.Info("dungeon", fmt.Sprintf("dungeon %d: rechallenge", c.Dungeon))
	born := dungeonBornPos(dg)
	s.QueueTransfer(c.Dungeon, [3]float32{born.X, born.Y, born.Z}, true)
	return true
}

// Leave goes back to where the player entered from. False when not inside a dungeon entered here.
func (d *Dungeons) Leave(s *PlayerSession) bool {
	c := d.Current
	if c == nil {
		return false
	}
	d.Current = nil
	d.save()
	if e := s.Quests(); e != nil {
		e.StopLevelPlays()
	}
	common.Info("dungeon", fmt.Sprintf("leaving dungeon %d to %d at %s,%s,%s", c.Dungeon, c.BackInstance, f0(c.BackPos.X), f0(c.BackPos.Y), f0(c.BackPos.Z)))
	s.QueueTransfer(c.BackInstance, [3]float32{c.BackPos.X, c.BackPos.Y, c.BackPos.Z}, false)
	return true
}

// OnQuestTransfer: a quest moves the player to instanceID itself (a node's DungeonId). Leaving
// the entered dungeon that way ends the visit: the way back it kept is stale.
func (d *Dungeons) OnQuestTransfer(instanceID int32) {
	c := d.Current
	if c == nil || c.Dungeon == instanceID {
		return
	}
	common.Info("dungeon", fmt.Sprintf("quest moves the player out of dungeon %d: its way back is dropped", c.Dungeon))
	d.Current = nil
	d.save()
}

// EntranceDetail is InstEntranceDetailRequest: the dungeons of an entrance, all open.
func EntranceDetail(wd *world.WorldData, entranceID int32) []*protocol.InstDetailEntry {
	e, ok := wd.Tables.InstanceDungeonEntranceById[entranceID]
	if !ok {
		return nil
	}
	out := make([]*protocol.InstDetailEntry, 0, len(e.InstanceDungeonList))
	for _, id := range e.InstanceDungeonList {
		out = append(out, &protocol.InstDetailEntry{InstId: id, Count: 0, ResetTime: 0})
	}
	return out
}

// OfferTeleport is a quest's TeleportDungeon action: ask the client (team screen); see ConfirmTeleport.
func (d *Dungeons) OfferTeleport(s *PlayerSession, dungeonID int32) {
	d.nextOffer++
	d.offers[d.nextOffer] = dungeonID
	common.Info("dungeon", fmt.Sprintf("offering dungeon %d (inc %d)", dungeonID, d.nextOffer))
	s.Sink().Push(&protocol.TeleportDungeonActionNotify{HostPlayerId: s.State.PlayerID(), IncId: d.nextOffer, DungeonId: dungeonID})
}

// ConfirmTeleport is TeleportDungeonActionRequest: the player confirmed (or cancelled) the team screen.
func (d *Dungeons) ConfirmTeleport(s *PlayerSession, incID int32, confirmed bool) {
	dungeon, ok := d.offers[incID]
	if !ok {
		common.Warn("dungeon", fmt.Sprintf("teleport dungeon inc %d: no such offer", incID))
		return
	}
	delete(d.offers, incID)
	if !confirmed {
		common.Info("dungeon", fmt.Sprintf("dungeon %d declined", dungeon))
		return
	}
	d.Enter(s, dungeon, 0)
}

// ClaimDungeonReward is GetInstExchangeRewardRequest{ExchangeCount}: the settlement screen's
// claim. InstanceDungeon.RewardId is an ExchangeReward row: RewardId {count: drop}, the key
// equal to the count, else "1" that many times. Waveplates are not deducted. False when there
// is no reward.
func ClaimDungeonReward(s *PlayerSession, exchangeCount int32) bool {
	if s.Scene == nil {
		return false
	}
	dg, ok := s.Data.Tables.InstanceDungeonById[s.Scene.InstanceID]
	if !ok {
		return false
	}
	var rewards *data.Object
	if ex, ok := exchangeReward(s.Data, dg.RewardId).(*data.Object); ok {
		rewards, _ = ex.Get("RewardId").(*data.Object)
	}
	if rewards == nil {
		common.Warn("dungeon", fmt.Sprintf("dungeon %d: exchange reward %d not found", dg.Id, dg.RewardId))
		return false
	}
	count := max(exchangeCount, 1)
	var dropID, times int32
	if exact, ok := rewards.Get(strconv.Itoa(int(count))).(data.Number); ok {
		dropID, times = int32(exact), 1
	} else if one, ok := rewards.Get("1").(data.Number); ok {
		dropID, times = int32(one), count
	} else {
		return false
	}
	// R-37: a listed double-reward activity covering the dungeon multiplies the claim.
	rate := ClaimDouble(s, dg.CustomTypes)
	common.Info("dungeon", fmt.Sprintf("dungeon %d: claiming reward %d x%d (drop %d, rate %d)", dg.Id, dg.RewardId, times, dropID, rate))
	var given []*protocol.RewardItemInfo
	for i := int32(0); i < times*rate; i++ {
		s.Actions().GrantDrop(dropID, int64(dg.Id), 1, &given)
	}
	// The result screen lists RewardItems by EDropItemType (0 = Normal).
	settle := &protocol.InstSettleNotify{IsSuccess: true, RewardItems: map[int32]*protocol.RewardItemInfoList{}}
	if rate > 1 {
		settle.Magnification = rate
		settle.From = protocol.DoubleDropFrom_DoubleDropFrom_DoubleActivity
	}
	if given == nil {
		given = []*protocol.RewardItemInfo{}
	}
	settle.RewardItems[0] = &protocol.RewardItemInfoList{ItemList: given}
	s.Sink().Push(settle)
	return true
}

func exchangeReward(wd *world.WorldData, id int32) data.Value {
	arr, ok := wd.ReadJSON("ExchangeReward.json").(*data.Array)
	if !ok {
		return nil
	}
	for _, e := range arr.Items {
		if o, ok := e.(*data.Object); ok {
			if n, ok := o.Get("Id").(data.Number); ok && int32(n) == id {
				return e
			}
		}
	}
	return nil
}
