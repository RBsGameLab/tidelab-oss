// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"fmt"
	"math"
	"slices"

	"tidelab/internal/combat"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/quests"
	"tidelab/internal/world"
	"tidelab/protocol"
)

func okWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "FAILED"
}

func wrongMark(ok bool) string {
	if ok {
		return ""
	}
	return "  <-- WRONG"
}

// boolWord is C#'s bool.ToString().
func boolWord(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

func optBoolWord(b *bool) string {
	if b == nil {
		return ""
	}
	return boolWord(*b)
}

func optInt32Word(v *int32) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(*v)
}

func lastErr(r *Runner, def protocol.ErrorCode) protocol.ErrorCode {
	if r.LastError != nil {
		return *r.LastError
	}
	return def
}

const success = protocol.ErrorCode_ErrorCode_Success

func absf32(x float32) float32 { return float32(math.Abs(float64(x))) }

// RunStartupProbes are the Sola guide, detection, gacha, role ability and nearby monster checks
// run before the story and dungeons.
func (r *Runner) RunStartupProbes(checkRoleAndMonsters bool) {
	r.SendRequest(&protocol.AdventureManualRequest{})
	r.SendRequest(&protocol.GetDetectionTargetRequest{DetectionId: 310000010})
	r.SendRequest(&protocol.GetDetectionTargetRequest{DetectionId: 340000330})
	r.SendRequest(&protocol.GachaInfoRequest{})
	s := r.Session()
	if !checkRoleAndMonsters || s.Scene == nil {
		return
	}
	if rid, ok := r.RoleNetID(); ok {
		if re := s.Scene.Get(rid); re != nil {
			var skills []*protocol.PassiveSkillItemPb
			for _, c := range re.ToPb().ComponentPbs {
				if ps := c.GetPassiveSkillComponentPb(); ps != nil {
					skills = ps.PassiveSkillItemPbList
					break
				}
			}
			scan := slices.ContainsFunc(skills, func(sk *protocol.PassiveSkillItemPb) bool { return sk.SkillId == 210004 })
			manip := slices.ContainsFunc(skills, func(sk *protocol.PassiveSkillItemPb) bool { return sk.SkillId == 210003 })
			r.printf("robot: role abilities: %d, scan %s, manipulate %s\n", len(skills), boolWord(scan), boolWord(manip))
		}
	}
	here := r.PlayerPos()
	r.MoveTo(world.Vec3{X: here.X + 3000, Y: here.Y, Z: here.Z})
	r.MoveTo(here)
	sp, sc := s.Spawner, s.Scene
	if sp == nil || sc == nil {
		return
	}
	monsters, owned := 0, 0
	for _, w := range sp.Spawns() {
		if e := sc.Get(w.NetID); e == nil || e.EntityType != world.TypeMonster {
			continue
		}
		monsters++
		id := int64(w.Candidate.EntityID())
		if e := r.Quests(); e != nil && e.Catalog().IsQuestOwned(sc.MapID, id) && !s.World.State.IsAwake(id) {
			owned++
		}
	}
	r.printf("robot: world monsters around the player: %d, quest-owned and not woken: %d\n", monsters, owned)
	all, withAi, withPassives := 0, 0, 0
	for _, e := range sc.Entities() {
		if e.EntityType != world.TypeMonster {
			continue
		}
		all++
		pb := e.ToPb()
		if slices.ContainsFunc(pb.ComponentPbs, func(c *protocol.EntityComponentPb) bool {
			ai := c.GetMonsterAiComponentPb()
			return ai != nil && ai.CombatMessageId != 0
		}) {
			withAi++
		}
		if slices.ContainsFunc(pb.ComponentPbs, func(c *protocol.EntityComponentPb) bool {
			ps := c.GetPassiveSkillComponentPb()
			return ps != nil && len(ps.PassiveSkillItemPbList) > 0
		}) {
			withPassives++
		}
	}
	r.printf("robot: monsters: %d, with an AI context %d, with passive GA skills %d\n", all, withAi, withPassives)
}

// ----- R-61 offers (+ R-62, R-66, R-67, R-117, R-124) -----

func (r *Runner) CheckOffers() int {
	r.printf("robot: offers: %d offered, %d accepted at login\n", len(r.Offered), len(r.InProgress))
	e := r.Quests()
	if e == nil {
		r.printf("robot: offers check FAILED: no quest catalog\n")
		return 1
	}
	cat := e.Catalog()
	for _, q := range r.Offered {
		r.printf("robot:   offered %d type %d %s\n", q, cat.QuestType(q), cat.QuestName(q))
	}
	for _, q := range r.InProgress {
		r.printf("robot:   accepted %d type %d %s\n", q, cat.QuestType(q), cat.QuestName(q))
	}
	for _, q := range slices.Clone(r.Offered) {
		offer, ok := cat.Offers()[q]
		if !ok || offer.Option == nil {
			continue
		}
		opt := offer.Option
		pos, ok := r.RowPos(opt.EntityID)
		if !ok {
			continue
		}
		r.MoveTo(pos)
		net, ok := r.NetIDOf(opt.EntityID)
		if !ok {
			r.printf("robot: offer %d: NPC %d not spawned at its spot\n", q, opt.EntityID)
			continue
		}
		hadOption := slices.Contains(r.OptionNets, net)
		r.Flows = nil
		r.SendRequest(&protocol.EntityDynamicInteractRequest{EntityId: net, OptionGuid: opt.Guid})
		ended := r.EndFlows()
		accepted := slices.Contains(r.InProgress, q)
		good := hadOption && ended == 1 && accepted
		r.printf("robot: offers check %s: quest %d at NPC %d: option %s, dialogues %d, in progress %s\n", okWord(good), q, opt.EntityID, boolWord(hadOption), ended, boolWord(accepted))
		if !good || !r.checkItemQuest() || !r.checkEntrance() || !r.checkOfferedOnce() || !r.checkTreesOnce() || !r.checkLeftovers() {
			return 1
		}
		if r.checkWorldPlays() {
			return 0
		}
		return 1
	}
	r.printf("robot: offers check FAILED: no offered quest has its NPC on this map\n")
	return 1
}

func (r *Runner) checkOfferedOnce() bool {
	twice := 0
	seen := map[int32]bool{}
	for _, q := range r.Offered {
		if seen[q] {
			twice++
		}
		seen[q] = true
	}
	r.printf("robot: offered-once check %s: %d offers, %d repeated after scene changes\n", okWord(twice == 0), len(r.Offered), twice)
	return twice == 0
}

func (r *Runner) checkTreesOnce() bool {
	ok := r.TreeInfosSeen > 0 && r.TreeDupNodes == 0 && r.TreeOrphanUpdates == 0
	r.printf("robot: trees-once check %s: %d trees sent, %d nodes sent again, %d status updates without their tree\n", okWord(ok), r.TreeInfosSeen, r.TreeDupNodes, r.TreeOrphanUpdates)
	return ok
}

func (r *Runner) checkLeftovers() bool {
	ids := []int64{139000336, 139000337, 140000530, 140000529, 140800148, 140800151}
	awake := 0
	for _, id := range ids {
		if r.Session().World.State.IsAwake(id) {
			awake++
			r.printf("robot:   %d still awake\n", id)
		}
	}
	r.printf("robot: leftovers check %s: %d of %d awake\n", okWord(awake == 0), awake, len(ids))
	return awake == 0
}

func (r *Runner) checkEntrance() bool {
	const entrance = 310000543
	pos, ok := r.RowPos(entrance)
	if !ok {
		r.printf("robot: entrance check FAILED: %d not on map %d\n", entrance, r.InstanceID())
		return false
	}
	r.MoveTo(pos)
	net, ok := r.NetIDOf(entrance)
	if !ok {
		r.printf("robot: entrance check FAILED: %d not spawned at its spot\n", entrance)
		return false
	}
	r.SendRequest(&protocol.EntityInteractRequest{EntityId: net, OptionIndex: 0})
	good := r.InstanceID() == 68
	r.printf("robot: entrance check %s: 进入边庭 -> instance %d\n", okWord(good), r.InstanceID())
	if good {
		r.SendRequest(&protocol.LeaveInstRequest{})
	}
	return good
}

func (r *Runner) checkItemQuest() bool {
	const q = 106800000
	here := r.PlayerPos()
	r.SendRequest(&protocol.PrivateChatRequest{Content: "item 72010002 2"})
	r.MoveTo(world.Vec3{X: here.X + 100, Y: here.Y, Z: here.Z})
	early := slices.Contains(r.InProgress, q)
	r.SendRequest(&protocol.PrivateChatRequest{Content: "item 72010002 1"})
	r.MoveTo(here)
	after := slices.Contains(r.InProgress, q)
	ok := !early && after
	r.printf("robot: item quest check %s: %d with 2 pages %s, with 3 %s\n", okWord(ok), q, boolWord(early), boolWord(after))
	return ok
}

func (r *Runner) checkWorldPlays() bool {
	sc := r.Session().Scene
	e := r.Quests()
	if sc == nil || e == nil {
		return false
	}
	plays := e.WorldPlaysOn(sc.MapID)
	r.printf("robot: world plays: %d would start on map %d\n", len(plays), sc.MapID)
	tried := 0
	for _, wp := range plays {
		if tried >= 12 {
			break
		}
		offeredBefore, acceptedBefore := len(r.Offered), len(r.InProgress)
		r.MoveTo(world.Vec3{X: wp.Pos[0], Y: wp.Pos[1], Z: wp.Pos[2]})
		if !e.IsLevelPlayRunning(wp.ID) && !e.WorldPlayDone(wp.ID) {
			r.printf("robot: world play %d %s: not started at its spot\n", wp.ID, wp.Name)
			continue
		}
		tried++
		key := quests.LevelPlayRun(wp.ID)
		round := 0
		for ; round < 20 && !e.WorldPlayDone(wp.ID); round++ {
			for _, st := range e.ActiveSteps(false) {
				if st.Run != key {
					continue
				}
				if quests.IsClientCondition(st.Def.Cond) {
					r.SendRequest(&protocol.SubmitNodeRequest{TreeOwnerId: r.PlayerID(), TreeIncId: st.Run, NodeId: st.Def.ID})
				} else if st.Def.Cond == "DoInteract" {
					for _, o := range st.Def.Options {
						if p, ok := r.RowPos(o.EntityID); ok {
							r.MoveTo(p)
						}
						if net, ok := r.NetIDOf(o.EntityID); ok {
							r.SendRequest(&protocol.EntityDynamicInteractRequest{EntityId: net, OptionGuid: o.Guid})
						}
					}
				}
			}
			r.EndFlows()
		}
		done := e.WorldPlayDone(wp.ID)
		r.printf("robot: world play %d %s: finished %s after %d rounds; quests offered +%d, accepted +%d\n", wp.ID, wp.Name, boolWord(done), round,
			len(r.Offered)-offeredBefore, len(r.InProgress)-acceptedBefore)
		if done && (len(r.Offered) > offeredBefore || len(r.InProgress) > acceptedBefore) {
			r.printf("robot: world plays check ok\n")
			return true
		}
	}
	r.printf("robot: world plays check FAILED: none of %d tried finished\n", tried)
	return false
}

// ----- R-77 worldinst -----

func (r *Runner) spawnNear(id int64, pos world.Vec3) (int64, bool) {
	for k := 0; k < 4; k++ {
		r.MoveTo(world.Vec3{X: pos.X + float32(k)*150, Y: pos.Y, Z: pos.Z})
		r.MoveTo(pos)
		if net, ok := r.NetIDOf(id); ok {
			return net, true
		}
	}
	return 0, false
}

func (r *Runner) CheckWorldInstances() int {
	total, good := 0, 0
	for _, d := range r.Tables().InstanceDungeon {
		if d.InstType != 1 || d.InstSubType != 12 {
			continue
		}
		total++
		if len(d.EntranceEntities) == 0 || d.EntranceEntities[0] == nil {
			r.printf("robot: worldinst %d: no entrance in the data\n", d.Id)
			continue
		}
		ent0 := d.EntranceEntities[0]
		entrance, worldMap := int64(ent0.EntranceEntityId), ent0.DungeonId
		row := r.Tables().LevelEntity(worldMap, int32(entrance))
		if row == nil || len(row.Transform) == 0 || row.Transform[0] == nil {
			r.printf("robot: worldinst %d: entrance %d not on map %d\n", d.Id, entrance, worldMap)
			continue
		}
		ep := row.Transform[0]
		epos := world.Vec3{X: float32(ep.X) / 100, Y: float32(ep.Y) / 100, Z: float32(ep.Z) / 100}
		if r.InstanceID() != worldMap {
			if r.InDungeon() {
				r.SendRequest(&protocol.LeaveInstRequest{})
			}
			if tp, ok := r.TeleporterNear(worldMap, epos); ok {
				r.SendRequest(&protocol.TeleportTransferRequest{Id: tp})
			}
		}
		if r.InstanceID() != worldMap {
			r.printf("robot: worldinst %d: could not reach map %d (on %d)\n", d.Id, worldMap, r.InstanceID())
			continue
		}
		if row.InSleep || row.IsHidden {
			what := "is hidden"
			if row.InSleep {
				what = "sleeps"
			}
			r.printf("robot: worldinst %d: entrance %d %s until a quest shows it (%s)\n", d.Id, entrance, what, row.Name)
			continue
		}
		if world.VisibilityConditions(row.ComponentsData) != nil && !world.Visible(row.ComponentsData, r.Session().World.Quests) {
			r.printf("robot: worldinst %d: entrance %d not visible yet (its quest conditions, %s)\n", d.Id, entrance, row.Name)
			continue
		}
		ec, ok := row.ComponentsData.(*data.Object)
		if !ok || ec.Get("InteractComponent") == nil {
			r.printf("robot: worldinst %d: entrance %d has no option: entered from the map (%s)\n", d.Id, entrance, row.Name)
			continue
		}
		if _, isObj := ec.Get("InteractComponent").(*data.Object); !isObj {
			r.printf("robot: worldinst %d: entrance %d has no option: entered from the map (%s)\n", d.Id, entrance, row.Name)
			continue
		}
		enet, ok := r.spawnNear(entrance, epos)
		if !ok {
			r.printf("robot: worldinst %d: entrance %d not spawned (%s)\n", d.Id, entrance, row.Name)
			continue
		}
		r.SendRequest(&protocol.EntityInteractRequest{EntityId: enet, OptionIndex: 0})
		if r.InstanceID() != d.Id {
			r.printf("robot: worldinst %d: entering FAILED (on %d)\n", d.Id, r.InstanceID())
			continue
		}
		if len(d.ExitEntities) == 0 {
			r.SendRequest(&protocol.LeaveInstRequest{})
			r.printf("robot: worldinst %d: entered; no exit in the data, left by LeaveInst -> %d\n", d.Id, r.InstanceID())
			continue
		}
		exit := int64(d.ExitEntities[0])
		xpos, ok := r.RowPos(exit)
		if !ok {
			r.printf("robot: worldinst %d: entered; exit %d not on map %d\n", d.Id, exit, r.InstanceID())
			r.SendRequest(&protocol.LeaveInstRequest{})
			continue
		}
		xnet, ok := r.spawnNear(exit, xpos)
		if !ok {
			r.printf("robot: worldinst %d: entered; exit %d NOT SPAWNED at its spot\n", d.Id, exit)
			r.SendRequest(&protocol.LeaveInstRequest{})
			continue
		}
		r.SendRequest(&protocol.EntityInteractRequest{EntityId: xnet, OptionIndex: 0})
		back := r.InstanceID()
		left := back != d.Id
		if left {
			good++
		}
		where := "STILL ON"
		if left {
			where = "back on"
		}
		r.printf("robot: worldinst %d: entered, exit %d used -> %s map %d\n", d.Id, exit, where, back)
		if !left {
			r.SendRequest(&protocol.LeaveInstRequest{})
		}
	}
	r.printf("robot: worldinst: %d world instances, %d entered and left by their exit\n", total, good)
	return 0
}

// ----- R-100 pos -----

func f1(x float32) string { return fmt.Sprintf("%.1f", x) }

func (r *Runner) CheckEntityPos() int {
	type pt struct{ X, Y float32 }
	cases := []struct {
		Dungeon, ID int32
		Want        *pt
	}{
		{8, 880700000, &pt{-32689, 100113}},
		{8, 122900116, &pt{888297, 600727}},
		{8, 131700001, &pt{44679, 83869}},
		{906, 131700001, &pt{6793, -388321}},
		{8, 1, nil},
	}
	good := 0
	for _, c := range cases {
		r.EntityPosOK = false
		r.EntityPosResult = nil
		r.SendRequest(&protocol.TraceEntityPosRequest{ConfigId: c.ID, DungeonId: c.Dungeon})
		ok := !r.EntityPosOK
		if c.Want != nil {
			ok = r.EntityPosOK && r.EntityPosResult != nil && absf32(r.EntityPosResult.X-c.Want.X) < 100 && absf32(r.EntityPosResult.Y-c.Want.Y) < 100
		}
		if ok {
			good++
		}
		at := "null"
		if p := r.EntityPosResult; p != nil {
			at = fmt.Sprintf("(%s, %s, %s)", f1(p.X), f1(p.Y), f1(p.Z))
		}
		r.printf("robot: entity position %d in dungeon %d: success %s, at %s%s\n", c.ID, c.Dungeon, boolWord(r.EntityPosOK), at, wrongMark(ok))
	}
	all := good == len(cases) && len(r.UnhandledList) == 0
	r.printf("robot: entity position check %s: %d of %d as wanted\n", okWord(all), good, len(cases))
	if all {
		return 0
	}
	return 1
}

// ----- R-99 ride -----

func (r *Runner) CheckRide() int {
	const q, node = 114000026, 138
	const vehicle = 915900222
	pier := world.Vec3{X: 654702, Y: 554345, Z: 158}
	goal := world.Vec3{X: 654986, Y: 557188, Z: 71}
	s := r.Session()
	s.World.State.Wake(vehicle)
	s.World.State.Save()
	eventsAtLogin := r.LevelEvents
	r.MoveTo(pier)
	earlyOn := r.RidesOn
	spawned, hasSpawned := r.NetIDOf(vehicle)
	r.MoveTo(world.Vec3{X: pier.X + 50, Y: pier.Y, Z: pier.Z})
	waitingOn := r.RidesOn
	earlyEvents := r.LevelEvents - eventsAtLogin
	e := r.Quests()
	if e != nil {
		e.ClockOffsetMs += 2500
	}
	r.MoveTo(pier)
	on := r.RidesOn
	events := r.LevelEvents - eventsAtLogin - earlyEvents
	named := hasSpawned && r.RideVehicle == spawned
	if e != nil {
		e.ClockOffsetMs += 2500
	}
	r.MoveTo(world.Vec3{X: pier.X + 50, Y: pier.Y, Z: pier.Z})
	again := r.RidesOn
	followed := false
	if hasSpawned {
		r.SendRequest(&protocol.SplineMoveRequest{EntityId: spawned, PathId: 1, PathRatio: 9000, Pos: goal.ToPb()})
		at := r.PlayerPos()
		followed = absf32(at.X-goal.X) < 1 && absf32(at.Y-goal.Y) < 1
	}
	r.SendRequest(&protocol.SubmitNodeRequest{TreeOwnerId: r.PlayerID(), TreeIncId: q, NodeId: node})
	done := e != nil && (e.IsNodeDone(q, node) || e.IsFinished(q))
	ok := hasSpawned && earlyOn == 0 && earlyEvents == 0 && waitingOn == 0 && on == 1 && events == 2 && named && again == 1 && followed && done && r.RidesOff == 1
	r.printf("robot: ride check %s: gondola spawned %s, before it was loaded %d boardings and %d action groups (want 0, 0), "+
		"then %d boarding naming it %s and %d action groups (want 1, true, 2), %d after another pass (want 1), "+
		"player followed the spline report %s, step done %s, left %d (want 1)\n",
		okWord(ok), boolWord(hasSpawned), earlyOn+waitingOn, earlyEvents, on, boolWord(named), events, again, boolWord(followed), boolWord(done), r.RidesOff)
	if ok {
		return 0
	}
	return 1
}

// ----- R-101 chairs -----

func (r *Runner) CheckChairs() int {
	spots := []struct {
		Name  string
		Map   int32
		Chair int64
		Pos   world.Vec3
	}{
		{"JZ", 8, 11000437, world.Vec3{X: -49990, Y: 107840, Z: 1620}},
		{"QQ", 8, 320750429, world.Vec3{X: 1206427, Y: 1132328, Z: 25004}},
		{"LHL", 906, 320702569, world.Vec3{X: -26399, Y: -472027, Z: 35202}},
	}
	seatBps := map[string]bool{}
	for _, bp := range r.Tables().BlueprintConfig {
		if world.ChairTypes[bp.EntityType] {
			seatBps[bp.BlueprintType] = true
		}
	}
	allOK := true
	for _, spot := range spots {
		if r.MapID() != spot.Map {
			if tp, ok := r.TeleporterNear(spot.Map, spot.Pos); ok {
				r.SendRequest(&protocol.TeleportTransferRequest{Id: tp})
			}
		}
		r.MoveTo(world.Vec3{X: spot.Pos.X + 1000, Y: spot.Pos.Y, Z: spot.Pos.Z})
		r.MoveTo(spot.Pos)
		r.MoveTo(world.Vec3{X: spot.Pos.X - 1000, Y: spot.Pos.Y, Z: spot.Pos.Z})
		r.MoveTo(spot.Pos)
		sp := r.Session().Spawner
		if r.MapID() != spot.Map || sp == nil {
			r.printf("robot: chairs %s: could not get to map %d\n", spot.Name, spot.Map)
			allOK = false
			continue
		}
		player := r.PlayerPos()
		spawnedIDs := map[int32]bool{}
		for _, w := range sp.Spawns() {
			spawnedIDs[w.Candidate.EntityID()] = true
		}
		near, missing := 0, 0
		for _, idx := range r.Tables().LevelEntityRowsOfMap(spot.Map) {
			row := r.Tables().LevelEntityConfig[idx]
			if row.InSleep || row.IsHidden || len(row.Transform) < 2 || row.Transform[0] == nil || !seatBps[row.BlueprintType] {
				continue
			}
			if !world.PassesNameFilter(row.Name, false) {
				continue
			}
			p := row.Transform[0]
			d := world.Vec3{X: float32(p.X) / 100, Y: float32(p.Y) / 100, Z: float32(p.Z) / 100}.DistanceTo(player)
			if d > 2400 {
				continue
			}
			near++
			if !spawnedIDs[row.EntityId] {
				missing++
				if missing <= 3 {
					r.printf("robot: chairs %s: %d (%s) %s m away not spawned\n", spot.Name, row.EntityId, row.Name, f0(d/100))
				}
			}
		}
		spawned, tooFar := 0, 0
		for _, w := range sp.Spawns() {
			if w.Class != world.ClassSeat {
				continue
			}
			spawned++
			if w.Candidate.Location.DistanceTo(player) > world.LeaveDistance(world.ClassSeat) {
				tooFar++
			}
		}
		r.SitOK = nil
		net, hasNet := r.NetIDOf(spot.Chair)
		if hasNet {
			r.SendRequest(&protocol.SitChairRequest{EntityId: net, IsSitDown: true})
		}
		sat := r.SitOK != nil && *r.SitOK
		r.SitOK = nil
		if hasNet {
			r.SendRequest(&protocol.SitChairRequest{EntityId: net, IsSitDown: false})
		}
		stood := r.SitOK != nil && *r.SitOK
		good := near > 0 && missing == 0 && tooFar == 0 && hasNet && sat && stood
		if !good {
			allOK = false
		}
		netText := ""
		if hasNet {
			netText = fmt.Sprint(net)
		}
		r.printf("robot: chairs %s (map %d): %d within 25 m, %d missing; %d chairs spawned, %d beyond 30 m; chair %d as %s: sit %s, stand %s%s\n",
			spot.Name, spot.Map, near, missing, spawned, tooFar, spot.Chair, netText, boolWord(sat), boolWord(stood), wrongMark(good))
	}
	if len(r.UnhandledList) > 0 {
		r.printf("robot: chairs: unhandled %s\n", r.UnhandledSummary())
		allOK = false
	}
	r.printf("robot: chair check %s\n", okWord(allOK))
	if allOK {
		return 0
	}
	return 1
}

// ----- R-104 / R-109 / R-116 shop -----

func (r *Runner) CheckShop() int {
	s := r.Session()
	inv := s.Combat.Player.NormalInventory
	const astrite, ticket, price = game.PayAstrite, 50001, 160
	good := game.TicketGoodsID(ticket) // 聆听星声: a ticket for 160 星声 (R-159)
	ok := true
	if s.ItemCount(astrite) < 5*price {
		s.Actions().GrantItem(astrite, 5*price, true)
	}
	a0, m0 := s.ItemCount(astrite), s.ItemCount(ticket)
	r.ShopOK = nil
	r.SendRequest(&protocol.PayShopBuyRequest{Id: good, Count: 5})
	bought := r.ShopOK != nil && *r.ShopOK && s.ItemCount(astrite) == a0-5*price && s.ItemCount(ticket) == m0+5
	ok = ok && bought
	r.printf("robot: shop: buy 5 x %d: %s 星声 %d -> %d, item %d -> %d%s\n", ticket, optBoolWord(r.ShopOK), a0, s.ItemCount(astrite), m0, s.ItemCount(ticket), wrongMark(bought))
	r.ShopOK = nil
	a1 := s.ItemCount(astrite)
	r.SendRequest(&protocol.PayShopBuyRequest{Id: astrite, Count: 1})
	noAstrite := r.ShopOK != nil && !*r.ShopOK && s.ItemCount(astrite) == a1
	ok = ok && noAstrite
	r.printf("robot: shop: buy 星声: %s, 星声 %d -> %d%s\n", optBoolWord(r.ShopOK), a1, s.ItemCount(astrite), wrongMark(noAstrite))
	r.ShopOK = nil
	m1 := s.ItemCount(ticket)
	n := a1/price + 1
	r.SendRequest(&protocol.PayShopBuyRequest{Id: good, Count: n})
	tooMuch := r.ShopOK != nil && !*r.ShopOK && s.ItemCount(astrite) == a1 && s.ItemCount(ticket) == m1
	ok = ok && tooMuch
	r.printf("robot: shop: buy %d with %d 星声: %s, 星声 %d, item %d -> %d%s\n", n, a1, optBoolWord(r.ShopOK), s.ItemCount(astrite), m1, s.ItemCount(ticket), wrongMark(tooMuch))
	r.BagList = nil
	r.SendRequest(&protocol.NormalItemRequest{})
	bagGood := r.BagList != nil && r.BagList[0] > 0 && r.BagList[1] == 0
	ok = ok && bagGood
	bag := ""
	if r.BagList != nil {
		bag = fmt.Sprintf("(%d, %d)", r.BagList[0], r.BagList[1])
	}
	r.printf("robot: shop: bag list %s (items, not in ItemInfo)%s\n", bag, wrongMark(bagGood))
	pushesGood := r.PushedItemsTotal > 0 && r.PushedItemsUnknown == 0
	ok = ok && pushesGood
	r.printf("robot: shop: item notifies (%d, %d) (items, not in ItemInfo)%s\n", r.PushedItemsTotal, r.PushedItemsUnknown, wrongMark(pushesGood))
	unknownKept := 0
	for id, n := range inv.Items().All() {
		_, isItem := r.Tables().ItemInfoById[id]
		_, isWeapon := r.Tables().WeaponConfByItemId[id]
		if n > 0 && !isItem && isWeapon {
			unknownKept++
		}
	}
	hasSword := false
	for _, w := range s.Combat.Player.Weapons.Values() {
		if w.ID == 21020053 {
			hasSword = true
		}
	}
	ok = ok && unknownKept == 0
	r.printf("robot: shop: weapons left among the normal items %d, weapon 21020053 owned %s%s\n", unknownKept, boolWord(hasSword), wrongMark(unknownKept == 0))
	r.printf("robot: shop check %s\n", okWord(ok))
	if ok {
		return 0
	}
	return 1
}

// ----- R-118 landing -----

func setRoleLife(role *combat.CombatEntity, life int32) {
	role.Attributes.Slots[combat.AttrLife].Current = life
	role.Attributes.Slots[combat.AttrLife].Base = life
}

func (r *Runner) CheckLanding() int {
	var role *combat.CombatEntity
	for _, e := range r.Session().Combat.Scene.PlayerRoleEntities() {
		if e.LifeMax() > 0 {
			role = e
			break
		}
	}
	if role == nil {
		r.printf("robot: landing: no role with LifeMax in the scene\n")
		return 1
	}
	net, lifeMax := role.NetID, role.LifeMax()
	setRoleLife(role, lifeMax)
	ok := true
	r.LandingOK = nil
	r.SendRequest(&protocol.LandingDamageRequest{EntityId: net, SpeedZ: -4500, TimeExceeding: 0})
	want := lifeMax - lifeMax/2
	life := role.Life()
	hurt := r.LandingOK != nil && *r.LandingOK && life == want
	ok = ok && hurt
	r.printf("robot: landing: role %d LifeMax %d: reply %s, Life %d (want %d)%s\n", net, lifeMax, optBoolWord(r.LandingOK), life, want, wrongMark(hurt))
	r.LandingOK = nil
	r.SendRequest(&protocol.LandingDamageRequest{EntityId: 6, SpeedZ: -4000, TimeExceeding: 34069})
	vision := r.LandingOK != nil && *r.LandingOK && role.Life() == want
	ok = ok && vision
	r.printf("robot: landing: unknown entity 6: reply %s, role Life %d%s\n", optBoolWord(r.LandingOK), role.Life(), wrongMark(vision))
	r.LandingOK = nil
	r.SendRequest(&protocol.LandingDamageRequest{EntityId: net, SpeedZ: -4000, TimeExceeding: 34069})
	dead := r.LandingOK != nil && *r.LandingOK && role.Life() == 0
	ok = ok && dead
	r.printf("robot: landing: 34 s fall: reply %s, Life %d%s\n", optBoolWord(r.LandingOK), role.Life(), wrongMark(dead))
	setRoleLife(role, lifeMax)
	r.printf("robot: landing check %s\n", okWord(ok))
	if ok {
		return 0
	}
	return 1
}

// ----- R-120 patrol -----

func (r *Runner) CheckPatrol() int {
	const configID = 133002060
	if pos, ok := r.RowPos(configID); ok {
		r.MoveTo(world.Vec3{X: pos.X + 500, Y: pos.Y, Z: pos.Z})
		r.MoveTo(pos)
	}
	id, ok := r.NetIDOf(configID)
	if !ok {
		if sc := r.Session().Scene; sc != nil {
			for _, e := range sc.Entities() {
				if e.EntityType == world.TypeMonster {
					id, ok = e.ID, true
					break
				}
			}
		}
	}
	if !ok {
		r.printf("robot: patrol: no monster in the scene\n")
		return 1
	}
	r.PatrolOK, r.PatrolBad = 0, 0
	r.SendRequest(&protocol.EntityPatrolStopRequest{EntityId: id})
	r.SendRequest(&protocol.EntityPatrolStartRequest{EntityId: id, Dir: true})
	r.SendRequest(&protocol.MonsterPatrolPointReachedRequest{EntityId: id, SplineEntityConfigId: 133002058, Index: 1})
	r.SendRequest(&protocol.MonsterPatrolPointReachedRequest{EntityId: id, SplineEntityConfigId: 133002058, Index: 2})
	r.SendRequest(&protocol.EntityPatrolChangeDirRequest{EntityId: id, Dir: false})
	r.SendRequest(&protocol.EntityPatrolStopRequest{EntityId: id})
	r.SendRequest(&protocol.EntityPatrolStopRequest{EntityId: 6})
	good := r.PatrolOK == 7 && r.PatrolBad == 0 && len(r.UnhandledList) == 0
	unh := "none"
	if len(r.UnhandledList) > 0 {
		unh = r.UnhandledSummary()
	}
	r.printf("robot: patrol: replies %d Success, %d other (want 7, 0); unhandled: %s\n", r.PatrolOK, r.PatrolBad, unh)
	r.printf("robot: patrol check %s\n", okWord(good))
	if good {
		return 0
	}
	return 1
}

// ----- R-102 weapons -----

func (r *Runner) CheckWeaponGifts() int {
	ok := true
	for _, wid := range []int32{21050116, 21010026} {
		r.WeaponsAdded = nil
		r.SendRequest(&protocol.PrivateChatRequest{Content: fmt.Sprintf("weapon %d max", wid)})
		good := len(r.WeaponsAdded) == 1 && r.WeaponsAdded[0] == [4]int32{wid, 90, 6, 5}
		ok = ok && good
		r.printf("robot: weapon %d max: %d added%s\n", wid, len(r.WeaponsAdded), wrongMark(good))
	}
	r.WeaponsAdded = nil
	r.SendRequest(&protocol.PrivateChatRequest{Content: "weapon 21050036 2"})
	plain := len(r.WeaponsAdded) == 2 && r.WeaponsAdded[0][1] == 1 && r.WeaponsAdded[0][2] == 0 && r.WeaponsAdded[0][3] == 1
	ok = ok && plain
	r.printf("robot: weapon 21050036 2: %d added%s\n", len(r.WeaponsAdded), wrongMark(plain))
	r.printf("robot: weapon gift check %s\n", okWord(ok))
	if ok {
		return 0
	}
	return 1
}

// ----- R-37 / R-41 / R-43 / R-58 / R-79 / R-87 / R-91 activity -----

func signStateName(s protocol.SignState) string {
	switch s {
	case protocol.SignState_SignState_Unlock:
		return "Unlock"
	case protocol.SignState_SignState_IsReceive:
		return "IsReceive"
	}
	return "Lock"
}

func (r *Runner) CheckActivities() int {
	groupOK := r.checkEntityGroup()
	r.SendRequest(&protocol.ActivityRequest{})
	signOK := false
	if r.SignActivity != nil {
		day := slices.Index(r.SignStates, protocol.SignState_SignState_Unlock)
		if day >= 0 {
			r.SendRequest(&protocol.SignActivityRequest{ActivityId: *r.SignActivity, Index: int32(day)})
			first := lastErr(r, success)
			r.SendRequest(&protocol.SignActivityRequest{ActivityId: *r.SignActivity, Index: int32(day)})
			again := lastErr(r, success)
			r.SendRequest(&protocol.ActivityRequest{})
			now := protocol.SignState_SignState_Lock
			if day < len(r.SignStates) {
				now = r.SignStates[day]
			}
			signOK = first == success && again != success && now == protocol.SignState_SignState_IsReceive
			r.printf("robot: activity check %s: claim day %d: %s, again: %s, now %s\n", okWord(signOK), day+1, errName(first), errName(again), signStateName(now))
		} else {
			signOK = true
			r.printf("robot: activity check: no open sign-in day to claim (all claimed or locked), skipped\n")
		}
	} else {
		r.printf("robot: activity check FAILED: no sign-in data on the page\n")
	}
	tpOK := r.checkTimePoint()
	doubleOK := r.checkDouble()
	menuOK := r.checkMenuRequests()
	rangeOK := r.checkRangeRequests()
	achOK := r.checkAchievements()
	shopOK := r.checkPayShop()
	skillOK := r.checkSkillCost()
	weaponOK := r.checkWeaponCost()
	echoOK := r.checkEchoCost()
	exchangeOK := r.checkExchangeCost()
	r.MusicAlbums, r.MusicSongs = 0, 0
	r.SendRequest(&protocol.PhantomMusicInfoRequest{})
	musicOK := r.MusicAlbums > 0 && r.MusicSongs > 0
	r.printf("robot: music check %s: %d albums, %d songs\n", okWord(musicOK), r.MusicAlbums, r.MusicSongs)
	r.MarksShown, r.TowerMarkShown = 0, false
	r.SendRequest(&protocol.MapUnlockFieldInfoRequest{})
	questDone := r.Quests() != nil && r.Quests().IsFinished(310000041)
	marksOK := r.MarksShown > 0 && (r.TowerMarkShown || !questDone)
	r.printf("robot: map mark check %s: %d marks shown, tower mark %s, 爬塔 done %s\n", okWord(marksOK), r.MarksShown, boolWord(r.TowerMarkShown), boolWord(questDone))
	terminalOK := r.TerminalGroups > 0 && marksOK
	r.printf("robot: terminal check %s: %d region groups unlocked at login\n", okWord(terminalOK), r.TerminalGroups)
	if groupOK && signOK && tpOK && doubleOK && menuOK && rangeOK && achOK && shopOK && skillOK && weaponOK && echoOK && exchangeOK && musicOK && terminalOK {
		return 0
	}
	return 1
}

func optStateWord(v int32, ok bool) string {
	if !ok {
		return ""
	}
	return fmt.Sprint(v)
}

func (r *Runner) checkEntityGroup() bool {
	const groupMap = 8
	if r.MapID() != groupMap {
		var pos world.Vec3
		if row := r.Tables().LevelEntity(groupMap, 102001708); row != nil && len(row.Transform) > 0 && row.Transform[0] != nil {
			p := row.Transform[0]
			pos = world.Vec3{X: float32(p.X) / 100, Y: float32(p.Y) / 100, Z: float32(p.Z) / 100}
		}
		if r.InDungeon() {
			r.SendRequest(&protocol.LeaveInstRequest{})
		}
		if tp, ok := r.TeleporterNear(groupMap, pos); ok {
			r.SendRequest(&protocol.TeleportTransferRequest{Id: tp})
		}
		if r.MapID() != groupMap {
			r.printf("robot: entity group check FAILED: could not reach map %d (on map %d, scene %d)\n", groupMap, r.MapID(), r.InstanceID())
			return false
		}
	}
	closed := world.TagID(r.Tables(), "关卡.屏障生成机关.闭合")
	active := world.TagID(r.Tables(), "关卡.Common.状态.激活")
	ws := r.Session().World.State
	before, hasBefore := ws.StateOverride(102001735)
	r.SendRequest(&protocol.ChangeEntityStateRequest{EntityId: 102001708, StateTagId: closed})
	r.SendRequest(&protocol.ChangeEntityStateRequest{EntityId: 102001709, StateTagId: closed})
	early, hasEarly := ws.StateOverride(102001735)
	r.SendRequest(&protocol.ChangeEntityStateRequest{EntityId: 102001710, StateTagId: closed})
	after, hasAfter := ws.StateOverride(102001735)
	ok := (!hasEarly || early != active || (hasBefore && before == active)) && hasAfter && after == active
	r.printf("robot: entity group check %s: record 102001735 before %s, after 2 barriers %s, after 3 %s (激活 %d)\n", okWord(ok),
		optStateWord(before, hasBefore), optStateWord(early, hasEarly), optStateWord(after, hasAfter), active)
	return ok
}

func (r *Runner) checkTimePoint() bool {
	if len(r.TimePoints) == 0 {
		r.printf("robot: time point check FAILED: no 定时领奖 data on the page\n")
		return false
	}
	k := slices.IndexFunc(r.TimePoints, func(t timePointEntry) bool { return t.Can })
	if k < 0 {
		r.printf("robot: time point check: nothing open to claim\n")
		return true
	}
	id := r.TimePoints[k].ID
	r.SendRequest(&protocol.TimePointActivityRewardRequest{Id: id})
	first := lastErr(r, success)
	r.SendRequest(&protocol.TimePointActivityRewardRequest{Id: id})
	again := lastErr(r, success)
	r.SendRequest(&protocol.ActivityRequest{})
	done := k < len(r.TimePoints) && r.TimePoints[k].Done
	ok := first == success && again != success && done
	r.printf("robot: time point check %s: #%d: %s, again: %s, received %s\n", okWord(ok), id, errName(first), errName(again), boolWord(done))
	return ok
}

func (r *Runner) checkDouble() bool {
	if r.DoubleID == nil {
		r.printf("robot: double check FAILED: no 奖励翻倍 on the page\n")
		return false
	}
	row, has := r.Tables().DoubleRewardActivityById[*r.DoubleID]
	if !has {
		r.printf("robot: double check FAILED: no 奖励翻倍 on the page\n")
		return false
	}
	used := r.DoubleUsed
	ok := true
	runs := int32(0)
	for ; runs <= row.Count-r.DoubleUsed; runs++ {
		r.SettleRate = 0
		r.UpdateUsed = nil
		rep := r.RunDungeon(201010)
		doubled := used < row.Count
		want := int32(0)
		if doubled {
			want = row.UpRate
			used++
		}
		pushedOK := r.UpdateUsed == nil
		if doubled {
			pushedOK = r.UpdateUsed != nil && *r.UpdateUsed == used
		}
		runOK := rep.Outcome == DungeonSuccess && r.SettleRate == want && pushedOK
		ok = ok && runOK
		r.printf("robot: double run %d: %s, settle x%d (want x%d), pushed count %s\n", runs+1, rep.Outcome, r.SettleRate, want, optInt32Word(r.UpdateUsed))
	}
	r.SendRequest(&protocol.ActivityRequest{})
	if r.DoubleUsed != used {
		ok = false
	}
	r.printf("robot: double check %s: %d runs, page count %d (want %d)\n", okWord(ok), runs, r.DoubleUsed, used)
	return ok
}

func (r *Runner) checkAchievements() bool {
	r.SendRequest(&protocol.AchievementInfoRequest{})
	listed, finishedBefore := r.AchEntries, r.AchFinished
	wasFinished, wasReceived := r.AchTargetTime > 0, r.AchTargetReceived
	r.AchNotifies = 0
	r.SendRequest(&protocol.AchievementFinishRequest{Id: AchievementChecked})
	finishErr := lastErr(r, success)
	notified := r.AchNotifies
	s := r.Session()
	before := s.ItemCount(3) // 星声 is a player attribute (R-159)
	r.SendRequest(&protocol.AchievementReceiveRequest{Id: AchievementChecked})
	first := lastErr(r, success)
	after := s.ItemCount(3)
	r.SendRequest(&protocol.AchievementReceiveRequest{Id: AchievementChecked})
	again := lastErr(r, success)
	r.SendRequest(&protocol.AchievementInfoRequest{})
	claimOK := first == success && after == before+5
	if wasReceived {
		claimOK = first != success
	}
	plusOne := int32(1)
	if wasFinished {
		plusOne = 0
	}
	ok := listed > 1000 && finishErr == success && (wasFinished || notified == 1) && r.AchTargetTime > 0 && r.AchTargetReceived &&
		claimOK && again != success && r.AchFinished == finishedBefore+plusOne
	r.printf("robot: achievement check %s: %d listed, finish %d: %s (%d notifies), claim: %s (星声 %d -> %d), again: %s, finished %d -> %d, stars %d\n",
		okWord(ok), listed, AchievementChecked, errName(finishErr), notified, errName(first), before, after, errName(again), finishedBefore, r.AchFinished, r.AchStars)
	return ok
}

func (r *Runner) checkPayShop() bool {
	r.PayShopItems = 0
	r.SendRequest(&protocol.PayShopInfoRequest{Version: "1"})
	s := r.Session()
	const item = 50001
	if s.ItemCount(game.PayAstrite) < 5*160 {
		s.Actions().GrantItem(game.PayAstrite, 5*160, true)
	}
	before := s.ItemCount(item)
	r.SendRequest(&protocol.PayShopBuyRequest{Id: game.TicketGoodsID(item), Count: 5, Version: "1"})
	bought := lastErr(r, success)
	after := s.ItemCount(item)
	r.SendRequest(&protocol.PayShopBuyRequest{Id: 70010001, Count: 1, Version: "1"})
	questItem := lastErr(r, success)
	ok := r.PayShopItems > 100 && bought == success && after == before+5 && questItem != success
	r.printf("robot: shop check %s: %d items, buy %d x5: %s (%d -> %d), quest item: %s\n", okWord(ok), r.PayShopItems, item, errName(bought), before, after, errName(questItem))
	return ok
}

// checkSkillCost (R-158): one skill level-up of an owned role takes the SkillLevel cost of the
// next level (贝币 from the attribute, materials from the bag); skipped when no role has a
// skill below max with a cost row.
func (r *Runner) checkSkillCost() bool {
	s, tb := r.Session(), r.Tables()
	p := s.Combat.Player
	for _, role := range p.Roles.Values() {
		for _, n := range tb.SkillTree {
			cfg, ok := tb.SkillById[n.SkillId]
			if n.NodeType != 2 || !ok || cfg.SkillLevelGroupId == 0 {
				continue
			}
			cur := int32(0)
			for _, sk := range role.Skills {
				if sk.Key == n.SkillId {
					cur = sk.Value
				}
			}
			if cur < 1 || cur >= cfg.MaxSkillLevel {
				continue
			}
			var costs []combat.Cost
			for _, row := range tb.SkillLevel {
				if row.SkillLevelGroupId == cfg.SkillLevelGroupId && row.SkillId == cur+1 {
					costs = combat.AddCosts(nil, row.Consume, 1)
				}
			}
			if len(costs) == 0 {
				continue
			}
			before := map[int32]int32{}
			for _, c := range costs {
				if s.ItemCount(c.Key) < c.Value {
					s.Actions().GrantItem(c.Key, c.Value, true)
				}
				before[c.Key] = s.ItemCount(c.Key)
			}
			r.SendRequest(&protocol.PbUpLevelSkillRequest{RoleId: role.RoleID, SkillId: n.SkillId})
			code := lastErr(r, success)
			paid := true
			for _, c := range costs {
				paid = paid && s.ItemCount(c.Key) == before[c.Key]-c.Value
			}
			ok = code == success && paid
			r.printf("robot: skill cost check %s: role %d skill %d -> %d: %s, %d costs paid %s\n", okWord(ok), role.RoleID, n.SkillId, cur+1, errName(code), len(costs), boolWord(paid))
			return ok
		}
	}
	r.printf("robot: skill cost check: no skill to level up, skipped\n")
	return true
}

// checkWeaponCost (R-158): one weapon exp item on an owned weapon below its level limit takes
// the item and WeaponExpItem.Cost 贝币; skipped when every weapon is at its limit.
func (r *Runner) checkWeaponCost() bool {
	s, tb := r.Session(), r.Tables()
	p := s.Combat.Player
	var exp *data.WeaponExpItem
	for _, e := range tb.WeaponExpItem {
		if exp == nil || e.BasicExp < exp.BasicExp {
			exp = e
		}
	}
	if exp == nil {
		r.printf("robot: weapon cost check: no exp item, skipped\n")
		return true
	}
	for _, w := range p.Weapons.Values() {
		wc, ok := tb.WeaponConfByItemId[w.ID]
		if !ok {
			continue
		}
		limit := int32(0)
		for _, b := range tb.WeaponBreach {
			if b.BreachId == wc.BreachId && b.Level == w.Breach {
				limit = b.LevelLimit
			}
		}
		if w.Level >= limit {
			continue
		}
		if s.ItemCount(exp.Id) < 1 {
			s.Actions().GrantItem(exp.Id, 1, true)
		}
		if s.ItemCount(combat.MoneyShellCredit) < exp.Cost {
			s.Actions().GrantItem(combat.MoneyShellCredit, exp.Cost, true)
		}
		items, coin := s.ItemCount(exp.Id), s.ItemCount(combat.MoneyShellCredit)
		r.SendRequest(&protocol.WeaponLevelUpRequest{IncId: w.UniqueID, ConsumeList: []*protocol.WeaponConsumeItem{{ItemId: exp.Id, Count: 1}}})
		code := lastErr(r, success)
		paid := s.ItemCount(exp.Id) == items-1 && s.ItemCount(combat.MoneyShellCredit) == coin-exp.Cost
		ok = code == success && paid
		r.printf("robot: weapon cost check %s: weapon %d (level %d): %s, item %d and %d 贝币 paid %s\n", okWord(ok), w.ID, w.Level, errName(code), exp.Id, exp.Cost, boolWord(paid))
		return ok
	}
	r.printf("robot: weapon cost check: every weapon at its level limit, skipped\n")
	return true
}

// checkEchoCost (R-158): the smallest echo exp item on an owned level-0 echo takes the item and
// exp / 10 贝币; skipped when no echo is at level 0.
func (r *Runner) checkEchoCost() bool {
	s, tb := r.Session(), r.Tables()
	var exp *data.PhantomExpItem
	for _, e := range tb.PhantomExpItem {
		if exp == nil || e.Exp < exp.Exp {
			exp = e
		}
	}
	if exp == nil {
		r.printf("robot: echo cost check: no exp item, skipped\n")
		return true
	}
	for _, e := range s.Combat.Player.Echoes.Values() {
		if e.Level != 0 || e.RoleID != nil {
			continue
		}
		gold := exp.Exp / 10
		if s.ItemCount(exp.ItemId) < 1 {
			s.Actions().GrantItem(exp.ItemId, 1, true)
		}
		if s.ItemCount(combat.MoneyShellCredit) < gold {
			s.Actions().GrantItem(combat.MoneyShellCredit, gold, true)
		}
		items, coin := s.ItemCount(exp.ItemId), s.ItemCount(combat.MoneyShellCredit)
		r.SendRequest(&protocol.PhantomLevelUpRequest{IncId: e.UniqueID, ConsumeList: []*protocol.PhantomConsumeItem{{ItemId: exp.ItemId, Count: 1}}})
		code := lastErr(r, success)
		paid := s.ItemCount(exp.ItemId) == items-1 && s.ItemCount(combat.MoneyShellCredit) == coin-gold
		ok := code == success && paid && e.Level > 0
		r.printf("robot: echo cost check %s: echo %d -> level %d: %s, item %d and %d 贝币 paid %s\n", okWord(ok), e.ID, e.Level, errName(code), exp.ItemId, gold, boolWord(paid))
		return ok
	}
	r.printf("robot: echo cost check: no unequipped level-0 echo, skipped\n")
	return true
}

// checkExchangeCost (R-158): one 月相 → 星声 exchange takes 1 月相 and gives 1 星声.
func (r *Runner) checkExchangeCost() bool {
	s := r.Session()
	if s.ItemCount(combat.MoneyLunite) < 1 {
		s.Actions().GrantItem(combat.MoneyLunite, 1, true)
	}
	lunite, astrite := s.ItemCount(combat.MoneyLunite), s.ItemCount(combat.MoneyAstrite)
	r.SendRequest(&protocol.ItemExchangeRequest{ItemId: combat.MoneyAstrite, ExchangeTimes: 1})
	code := lastErr(r, success)
	ok := code == success && s.ItemCount(combat.MoneyLunite) == lunite-1 && s.ItemCount(combat.MoneyAstrite) == astrite+1
	r.printf("robot: exchange cost check %s: 月相 %d -> %d, 星声 %d -> %d: %s\n", okWord(ok), lunite, s.ItemCount(combat.MoneyLunite), astrite, s.ItemCount(combat.MoneyAstrite), errName(code))
	return ok
}

func (r *Runner) checkMenuRequests() bool {
	r.UnhandledList, r.unhandledSet = nil, map[string]bool{}
	const questID = 310000041
	lock := protocol.ErrorCode_ErrorCode_ErrActivityLock
	r.SendRequest(&protocol.QuestRedDotRequest{QuestId: questID, Operate: 1})
	redDot := lastErr(r, lock)
	r.SendRequest(&protocol.TraceQuestRequest{TraceType: 1, QuestId: questID, Operate: 1})
	trace := lastErr(r, lock)
	var lpIDs []int32
	if e := r.Quests(); e != nil {
		for i, l := range e.Catalog().LevelPlayDefs() {
			if i >= 8 {
				break
			}
			lpIDs = append(lpIDs, l.ID)
		}
	}
	req := &protocol.LevelPlayStateListRequest{InstLevelPlayStateReqs: []*protocol.InstLevelPlayStateReq{{InstId: r.InstanceID(), LevelPlayIds: lpIDs}}}
	r.LevelPlayStates = 0
	r.SendRequest(req)
	list := lastErr(r, lock)
	states := r.LevelPlayStates
	r.MinimapPos = nil
	r.SendRequest(&protocol.LastBigSceneMiniMapInfoRequest{})
	minimap := lastErr(r, lock)
	pos := r.MinimapPos
	ok := len(r.UnhandledList) == 0 && redDot == success && trace == success && list == success && states == len(lpIDs) && minimap == success && pos != nil
	unh := "none"
	if len(r.UnhandledList) > 0 {
		unh = r.UnhandledSummary()
	}
	r.printf("robot: menu request check %s: red dot %s, trace %s, level play list %s (%d/%d states), minimap %s; unhandled: %s\n",
		okWord(ok), errName(redDot), errName(trace), errName(list), states, len(lpIDs), errName(minimap), unh)
	return ok
}

func rangeText(v *rangeReplyInfo) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("RangeReplyInfo { Id = %d, Entities = %d, Player = %s }", v.ID, v.Entities, boolWord(v.Player))
}

func (r *Runner) checkRangeRequests() bool {
	r.UnhandledList, r.unhandledSet = nil, map[string]bool{}
	const rng = 94748818081
	inside := []int64{94748818082, 94748818083}
	r.RangeReply = nil
	r.SendRequest(&protocol.InitRangeRequest{RangeEntityId: rng, IsPlayerInRange: true, InRangeEntityIds: inside})
	init := r.RangeReply
	initOK := r.LastError != nil && *r.LastError == success && init != nil && init.ID == rng && init.Entities == 2 && init.Player
	r.RangeReply = nil
	r.SendRequest(&protocol.PlayerAccessRangeRequest{RangeEntityId: rng, AccessRangeType: protocol.AccessRangeType_AccessRangeType_RangeEnter})
	player := r.RangeReply
	playerOK := r.LastError != nil && *r.LastError == success && player != nil && player.ID == rng && player.Player
	r.RangeReply = nil
	r.SendRequest(&protocol.EntityAccessRangeRequest{RangeEntityId: rng, AccessRangeType: protocol.AccessRangeType_AccessRangeType_RangeLeave, AccessEntityIds: inside})
	entity := r.RangeReply
	entityOK := r.LastError != nil && *r.LastError == success && entity != nil && entity.ID == rng && entity.Entities == 2
	ok := len(r.UnhandledList) == 0 && initOK && playerOK && entityOK
	unh := "none"
	if len(r.UnhandledList) > 0 {
		unh = r.UnhandledSummary()
	}
	r.printf("robot: range request check %s: init %s, player enter %s, entities leave %s; unhandled: %s\n", okWord(ok), rangeText(init), rangeText(player), rangeText(entity), unh)
	return ok
}
