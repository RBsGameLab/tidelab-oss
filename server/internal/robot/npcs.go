// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// The robot's npcs check (specs/spawning.md §8): the game runs in memory on a copy of a player's
// save, travels to nine busy spots and counts the world spawns there. Ported from our C# Npcs.

type npcSpot struct {
	Name string
	Map  int32
	Pos  world.Vec3
}

var npcSpots = []npcSpot{
	{"JZ 今州", 8, world.Vec3{X: -41886, Y: 108485, Z: 2595}},
	{"QQ", 8, world.Vec3{X: 1248420, Y: 1185500, Z: 50780}},
	{"MZ_A1", 8, world.Vec3{X: -706690, Y: -92630, Z: -22010}},
	{"LGNC", 8, world.Vec3{X: 700114, Y: 583700, Z: 1081}},
	{"LHL_XJXY2", 906, world.Vec3{X: -70690, Y: -552300, Z: 36237}},
	{"LHL_JMYY", 906, world.Vec3{X: 85939, Y: -499897, Z: 48674}},
	{"QQ wild", 8, world.Vec3{X: 1223133, Y: 1150181, Z: 29240}},
	{"900 TTSZD", 900, world.Vec3{X: -18740, Y: 73600, Z: 4440}},
	{"912 MSTL", 912, world.Vec3{X: -1930, Y: 960, Z: -7073}},
}

type quietPlayer struct{ id int32 }

func (q quietPlayer) PlayerID() int32                        { return q.id }
func (quietPlayer) Push(proto.Message)                       {}
func (quietPlayer) PushID(protocol.MessageID, proto.Message) {}

// RunNpcs runs the check and returns the exit code; list receives the TSV of every world spawn per spot.
func RunNpcs(t *data.DataTables, sourceState string, playerID int32, epoch int64, out io.Writer, list io.Writer, commonParam string) int {
	wd := world.NewWorldData(t, nil)
	copyDir, err := os.MkdirTemp("", "tidelab-robot-")
	if err != nil {
		fmt.Fprintf(out, "robot: %v\n", err)
		return 1
	}
	defer os.RemoveAll(copyDir)
	pid := strconv.Itoa(int(playerID))
	if _, err := CopyDirectory(filepath.Join(sourceState, "player", pid), filepath.Join(copyDir, "player", pid)); err != nil {
		fmt.Fprintf(out, "robot: no player directory %s\n", filepath.Join(sourceState, "player", pid))
		return 1
	}
	clock := common.NewGameClock(nil)
	clock.UseVirtual(epoch)
	g := game.NewWorldGame(copyDir, wd, clock, combat.NewShared(t, commonParam))
	s := g.NewSession(quietPlayer{playerID})
	s.Enter(false)
	s.Heartbeat()
	return checkNpcs(s, t, clock, out, list)
}

func checkNpcs(s *game.PlayerSession, t *data.DataTables, clock *common.GameClock, out, list io.Writer) int {
	npcTypes, monsterTypes := map[string]bool{}, map[string]bool{}
	for _, bp := range t.BlueprintConfig {
		if world.NpcTypes[bp.EntityType] {
			npcTypes[bp.BlueprintType] = true
		} else if bp.EntityType == "Monster" {
			monsterTypes[bp.BlueprintType] = true
		}
	}
	tps := npcTeleporters(t)
	if list != nil {
		fmt.Fprintln(list, "spot\tmap\tkind\tconfig_id\tx\ty\tz")
	}
	ok := true
	for _, spot := range npcSpots {
		if s.Scene.MapID != spot.Map {
			if tp, found := nearestTeleporter(tps, spot); found {
				s.TeleportTransfer(&protocol.TeleportTransferRequest{Id: tp})
				clock.Advance(MsPerRequest)
			}
		}
		for k := 0; k < 4; k++ {
			dx := float32(1000)
			if k%2 != 0 {
				dx = -1000
			}
			npcMoveTo(s, clock, world.Vec3{X: spot.Pos.X + dx, Y: spot.Pos.Y, Z: spot.Pos.Z})
		}
		npcMoveTo(s, clock, spot.Pos)
		sp := s.Spawner
		if s.Scene.MapID != spot.Map || sp == nil {
			fmt.Fprintf(out, "robot: npcs %s: could not get to map %d\n", spot.Name, spot.Map)
			ok = false
			continue
		}
		var npcs []*world.WorldSpawn
		monsters := 0
		for _, w := range sp.Spawns() {
			if npcTypes[w.Candidate.Row.BlueprintType] {
				npcs = append(npcs, w)
			}
			if monsterTypes[w.Candidate.Row.BlueprintType] {
				monsters++
			}
		}
		copies := 0
		for i := 0; i < len(npcs); i++ {
			for j := i + 1; j < len(npcs); j++ {
				a, b := npcs[i].Candidate, npcs[j].Candidate
				if a.Row.Name != b.Row.Name || !hasVisibleComponent(a) || !hasVisibleComponent(b) || a.Location.DistanceTo(b.Location) > 300 {
					continue
				}
				copies++
				if copies <= 3 {
					fmt.Fprintf(out, "robot: npcs %s: copies %s (%d, %d)\n", spot.Name, a.Row.Name, a.EntityID(), b.EntityID())
				}
			}
		}
		if len(npcs)+monsters == 0 || copies > 0 {
			ok = false
		}
		fmt.Fprintf(out, "robot: npcs %s (map %d): %d spawned, %d monsters (%d world spawns in all), %d same-name copies within 3 m\n",
			spot.Name, spot.Map, len(npcs), monsters, len(sp.Spawns()), copies)
		if list != nil {
			kind := func(w *world.WorldSpawn) string {
				switch {
				case npcTypes[w.Candidate.Row.BlueprintType]:
					return "npc"
				case monsterTypes[w.Candidate.Row.BlueprintType]:
					return "monster"
				}
				return "other"
			}
			spawns := append([]*world.WorldSpawn(nil), sp.Spawns()...)
			sort.SliceStable(spawns, func(i, j int) bool {
				ki, kj := kind(spawns[i]), kind(spawns[j])
				if ki != kj {
					return ki < kj
				}
				return spawns[i].Candidate.EntityID() < spawns[j].Candidate.EntityID()
			})
			for _, w := range spawns {
				p := w.Candidate.Location
				if m, ok := s.World.Moves.Get(int64(w.Candidate.EntityID())); ok {
					p = world.Vec3{X: m.X, Y: m.Y, Z: m.Z}
				}
				fmt.Fprintf(list, "%s\t%d\t%s\t%d\t%.2f\t%.2f\t%.2f\n", spot.Name, spot.Map, kind(w), w.Candidate.EntityID(), p.X, p.Y, p.Z)
			}
		}
	}
	fmt.Fprintf(out, "robot: npc check %s\n", okWord(ok))
	s.Release()
	if ok {
		return 0
	}
	return 1
}

func hasVisibleComponent(c *world.Candidate) bool {
	o, ok := c.Row.ComponentsData.(*data.Object)
	if !ok {
		return false
	}
	_, ok = o.Get("EntityVisibleComponent").(*data.Object)
	return ok
}

func npcMoveTo(s *game.PlayerSession, clock *common.GameClock, pos world.Vec3) {
	role, ok := s.Scene.RoleEntities.Get(s.Formation().CurRole)
	if !ok {
		return
	}
	s.Move(&protocol.MovePackagePush{MovingEntities: []*protocol.MovingEntityData{{
		EntityId: role, Originator: role,
		MoveInfos: []*protocol.MoveReplaySample{{Location: pos.ToPb(), Rotation: &protocol.Rotator{}}},
	}}})
	clock.Advance(MsPerRequest)
}

func npcTeleporters(t *data.DataTables) []teleporterAt {
	var out []teleporterAt
	for _, tp := range t.Teleporter {
		row := t.LevelEntity(tp.MapId, tp.TeleportEntityConfigId)
		if row == nil || len(row.Transform) < 3 || row.Transform[0] == nil || row.Transform[2] == nil {
			continue
		}
		p, sc := row.Transform[0], row.Transform[2]
		if sc.X == 0 || sc.Y == 0 || sc.Z == 0 {
			continue
		}
		out = append(out, teleporterAt{tp.Id, row.MapId, world.Vec3{X: float32(p.X) / float32(sc.X), Y: float32(p.Y) / float32(sc.Y), Z: float32(p.Z) / float32(sc.Z)}})
	}
	return out
}

func nearestTeleporter(tps []teleporterAt, spot npcSpot) (int32, bool) {
	var best int32
	found := false
	bestD := float32(math.MaxFloat32)
	for _, tp := range tps {
		if tp.Map != spot.Map {
			continue
		}
		if d := tp.Pos.DistanceTo(spot.Pos); d < bestD {
			bestD, best, found = d, tp.ID, true
		}
	}
	return best, found
}
