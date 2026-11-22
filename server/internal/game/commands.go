// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// The chat, gift-mail and cmd.txt commands (specs/systems.md §12). Input holds commands
// separated by ';'; each is cut on spaces and its first word names a command or its alias.
// Ported from our C# Game/Commands.cs.

// CommandError is a command's wrong arguments (§12.1): the text goes back through the bot.
type CommandError struct{ Msg string }

func (e *CommandError) Error() string { return e.Msg }

func cmdFail(format string, a ...any) { panic(&CommandError{fmt.Sprintf(format, a...)}) }

// ChatCommand is one chat command: its name, alias, usage line, what it does, and how it runs.
type ChatCommand struct {
	Name, Alias, Usage, Description string
	Run                             func(s *PlayerSession, args []string) string
}

// PlannedCommands are the commands of §12.2 tidelab does not have yet.
var PlannedCommands = map[string]bool{}

// AllCommands are the commands, in the help order.
var AllCommands []ChatCommand

func init() {
	AllCommands = []ChatCommand{
		{"help", "", "help [command]", "lists the commands, or tells what one does", cmdHelp},
		{"give_item", "item", "item <item_id> [count=1]", "gives a normal item", cmdGiveItem},
		{"give_role", "role", "role <role_id>", "gives a role (an owned one turns into its spillover items)", cmdGiveRole},
		{"weapon_item", "weapon", "weapon <current|weapon_id> [count=4|max|clear]",
			"gives copies of a weapon, one finished copy (max), or removes the unequipped ones (clear)", cmdGiveWeapon},
		{"resonance_item", "waveband", "waveband <current|role_id> [count=6|clear]",
			"gives a role's resonance chain item, or removes it (clear)", cmdGiveWaveband},
		{"quest_cmd", "quest", "quest | quest next | quest start <quest_id>",
			"shows the running quests, completes the active steps, or starts a quest", cmdQuest},
		{"dungeon_cmd", "dungeon", "dungeon enter <id> | dungeon leave | dungeon killall", "enters a dungeon, leaves the one entered, or kills all monsters", cmdDungeon},
		{"goto", "", "goto <x> <y> <z>", "moves the player to a place (centimetres) on this map", cmdGoto},
		{"map", "m", "map <map_id>", "moves the player to another map", cmdMap},
		{"open_shop", "shop", "shop", "pushes the shop list (ShopInfoNotify)", cmdOpenShop},
		{"achievement_cmd", "achieve", "achieve <id>", "finishes an achievement", cmdAchieve},
		{"find", "", "find [chest|door] [n=1]", "teleports next to the n-th nearest unopened chest or door", cmdFind},
		{"flow", "", "flow <flow_list_name> <flow_id> <state_id>", "starts the specified flow", cmdFlowStart},
		{"flowtp", "", "flowtp <action_id> <x> <y> <z>", "teleports with a FlowAction context", cmdFlowTp},
		{"flowend", "", "flowend <inc_id> [flow_list_name flow_id state_id]", "force-ends a client flow", cmdFlowEnd},
		{"battle", "bt", "battle <true|false>", "forces the player's combat battle state on or off", cmdBattle},
		{"max_attribute", "max", "max <role_id|team> <EAttributeType>", "sets an attribute to its max value", func(s *PlayerSession, a []string) string { return cmdMutateAttribute(s, a, "max") }},
		{"empty_attribute", "empty", "empty <role_id|team> <EAttributeType>", "sets an attribute to 0", func(s *PlayerSession, a []string) string { return cmdMutateAttribute(s, a, "empty") }},
		{"set_attribute", "set", "set <role_id|team> <EAttributeType> <value>", "sets an attribute to value", func(s *PlayerSession, a []string) string { return cmdMutateAttribute(s, a, "set") }},
		{"echo", "ec", "echo <echo_id> <sonata_id> <main:a-m> [sub tier]...", "creates a max-level custom echo", cmdGiveEcho},
		{"spawn", "s", "spawn <entity_id> [freeze] [tune_break]", "spawns a LevelEntityConfig entity near the player", func(s *PlayerSession, a []string) string { return cmdSpawn(s, a, false) }},
		{"spawn_map", "sm", "spawn_map <entity_id> <source_map_id> [freeze] [tune_break]", "spawns a LevelEntityConfig entity from another map near the player", func(s *PlayerSession, a []string) string { return cmdSpawn(s, a, true) }},
		{"reset_formation", "rf", "reset_formation", "respawns all entities of the current formation", cmdResetFormation},
		{"photo", "cam", "photo [enabled=true]", "toggles 2.7 photo-event mode", cmdPhoto},
	}
}

// FindCommand is the command named or aliased word.
func FindCommand(word string) *ChatCommand {
	for i := range AllCommands {
		c := &AllCommands[i]
		if c.Name == word || (c.Alias != "" && c.Alias == word) {
			return c
		}
	}
	return nil
}

// RunCommandLine runs every command of input; their replies (and errors) go to replies. False
// when one of them was unknown or failed. Other panics propagate (RunCommands catches them).
func RunCommandLine(s *PlayerSession, input string, replies *[]string) bool {
	ok := true
	for _, part := range strings.Split(input, ";") {
		words := splitSpaces(strings.Trim(part, " "))
		if len(words) == 0 {
			continue
		}
		cmd := FindCommand(words[0])
		if cmd == nil {
			if PlannedCommands[words[0]] {
				*replies = append(*replies, fmt.Sprintf("\"%s\" is not in tidelab yet.", words[0]))
			} else {
				*replies = append(*replies, fmt.Sprintf("unknown command \"%s\"; use \"help\" to get a list of commands.", words[0]))
			}
			ok = false
			continue
		}
		reply, failed := runOneCommand(s, cmd, words[1:])
		if failed != nil {
			*replies = append(*replies, fmt.Sprintf("%s: %s\nusage: %s", cmd.Name, failed.Msg, cmd.Usage))
			ok = false
		} else if reply != "" {
			*replies = append(*replies, reply)
		}
	}
	return ok
}

func runOneCommand(s *PlayerSession, cmd *ChatCommand, args []string) (reply string, failed *CommandError) {
	defer func() {
		if r := recover(); r != nil {
			if ce, ok := r.(*CommandError); ok {
				failed = ce
				return
			}
			panic(r)
		}
	}()
	return cmd.Run(s, args), nil
}

// ----- arguments -----

const (
	// MaxCommandCount is a guard, not a rule of the game: more copies than this is a typo.
	MaxCommandCount = 100_000
	// MaxCommandWeapons is lower: each copy is a file.
	MaxCommandWeapons = 1_000
)

func argCount(args []string, lo, hi int) {
	if len(args) < lo || len(args) > hi {
		if lo == hi {
			cmdFail("takes %d argument(s)", lo)
		}
		cmdFail("takes %d to %d arguments", lo, hi)
	}
}

func argInt(word, what string) int32 {
	v, ok := parseSigned(word, 32)
	if !ok {
		cmdFail("%s \"%s\" is not a whole number", what, word)
	}
	return int32(v)
}

func argFloat(word, what string) float32 {
	v, err := strconv.ParseFloat(strings.TrimFunc(word, isNetSpace), 32)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		cmdFail("%s \"%s\" is not a number", what, word)
	}
	return float32(v)
}

func argPositive(word, what string) int32 {
	v := argInt(word, what)
	if v <= 0 || v > MaxCommandCount {
		cmdFail("%s must be 1 to %d", what, MaxCommandCount)
	}
	return v
}

// ArgBool is true/1 and false/0 (§12.1).
func ArgBool(word string) bool {
	switch word {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	cmdFail("\"%s\" is not true/false/1/0", word)
	return false
}

func aliasSuffix(c *ChatCommand) string {
	if c.Alias != "" {
		return " (" + c.Alias + ")"
	}
	return ""
}

// ----- commands -----

func cmdHelp(_ *PlayerSession, args []string) string {
	argCount(args, 0, 1)
	if len(args) == 1 {
		c := FindCommand(args[0])
		if c == nil {
			cmdFail("no command \"%s\"", args[0])
		}
		return fmt.Sprintf("%s%s: %s\nusage: %s", c.Name, aliasSuffix(c), c.Description, c.Usage)
	}
	var sb strings.Builder
	sb.WriteString("commands:")
	for i := range AllCommands {
		sb.WriteString("\n" + AllCommands[i].Name + aliasSuffix(&AllCommands[i]))
	}
	sb.WriteString("\nuse \"help <command>\" to see how one is used.")
	return sb.String()
}

// cmdGiveItem: a normal item that ItemInfo has (R-116), added, saved and pushed.
func cmdGiveItem(s *PlayerSession, args []string) string {
	argCount(args, 1, 2)
	id := argInt(args[0], "item id")
	count := int32(1)
	if len(args) > 1 {
		count = argPositive(args[1], "count")
	}
	if _, ok := s.Data.Tables.ItemInfoById[id]; !ok {
		cmdFail("no item %d in ItemInfo", id)
	}
	now := s.Actions().GrantItem(id, count, false)
	common.Info("command", fmt.Sprintf("item %d x%d (now %d)", id, count, now))
	return fmt.Sprintf("gave %d of item %d (now %d).", count, id, now)
}

// cmdGiveRole: the reward-role path (activities.md §2.3, R-38).
func cmdGiveRole(s *PlayerSession, args []string) string {
	argCount(args, 1, 1)
	id := argInt(args[0], "role id")
	if _, ok := s.Data.Tables.RoleInfoById[id]; !ok {
		cmdFail("no role %d in RoleInfo", id)
	}
	owned := s.Combat.Player.Roles.Has(id)
	s.Combat.Roles.GrantRole(id, uint32(s.Clock.Seconds()), s.Sink())
	if owned {
		common.Info("command", fmt.Sprintf("role %d (owned: spillover)", id))
		return fmt.Sprintf("role %d is owned already: gave its spillover items.", id)
	}
	common.Info("command", fmt.Sprintf("role %d", id))
	return fmt.Sprintf("gave role %d.", id)
}

// cmdGiveWeapon (R-102): copies, one finished copy, or clearing the unequipped ones.
func cmdGiveWeapon(s *PlayerSession, args []string) string {
	argCount(args, 1, 2)
	t := s.Data.Tables
	player := s.Combat.Player
	var id int32
	if args[0] == "current" {
		role, ok := player.Roles.Get(s.Formation().CurRole)
		var w *save.WeaponSave
		if ok {
			w, ok = player.Weapons.Get(role.Weapon)
		}
		if !ok {
			cmdFail("the current role has no weapon")
		}
		id = w.ID
	} else {
		id = argInt(args[0], "weapon id")
	}
	conf, ok := t.WeaponConfByItemId[id]
	if !ok {
		cmdFail("no weapon %d in WeaponConf", id)
	}
	mode := "4"
	if len(args) > 1 {
		mode = args[1]
	}
	if mode == "clear" {
		var gone []int32
		for _, w := range player.Weapons.Values() {
			if w.ID == id && w.RoleID == nil {
				gone = append(gone, w.UniqueID)
			}
		}
		for _, uid := range gone {
			player.Weapons.Delete(uid)
			save.DeleteWeapon(player, uid)
		}
		if len(gone) > 0 {
			s.Sink().Push(&protocol.WeaponItemRemoveNotify{WeaponItemIncrIdList: gone})
		}
		common.Info("command", fmt.Sprintf("weapon %d clear: %d removed", id, len(gone)))
		word := "ies"
		if len(gone) == 1 {
			word = "y"
		}
		return fmt.Sprintf("removed %d unequipped cop%s of weapon %d.", len(gone), word, id)
	}
	isMax := mode == "max"
	count := int32(1)
	if !isMax {
		count = argInt(mode, "count")
		if count <= 0 || count > MaxCommandWeapons {
			cmdFail("count must be 1 to %d, max or clear", MaxCommandWeapons)
		}
	}
	level, breach, reson := int32(1), int32(0), int32(1)
	if isMax {
		// The top WeaponBreach row of the weapon's BreachId: its level and level limit.
		var top *data.WeaponBreach
		for _, b := range t.WeaponBreach {
			if b.BreachId == conf.BreachId && (top == nil || b.Level > top.Level) {
				top = b
			}
		}
		level = 90
		if top != nil {
			breach = top.Level
			if top.LevelLimit > 0 {
				level = top.LevelLimit
			}
		}
		reson = max(conf.ResonLevelLimit, 1)
	}
	notify := &protocol.WeaponItemAddNotify{}
	for i := int32(0); i < count; i++ {
		fv := int32(1)
		if conf.QualityId >= 5 {
			fv = 0
		}
		w := &save.WeaponSave{UniqueID: player.SpecialItemIDs.Next(), ID: id, Level: level, Breach: breach, ResonLevel: reson, FuncValue: fv}
		player.Weapons.Set(w.UniqueID, w)
		save.SaveWeapon(player, w)
		notify.WeaponItemList = append(notify.WeaponItemList, &protocol.WeaponItem{
			Id: w.ID, IncrId: w.UniqueID, FuncValue: w.FuncValue, WeaponLevel: w.Level, WeaponBreach: w.Breach, WeaponResonLevel: w.ResonLevel,
		})
	}
	s.Sink().Push(notify)
	if isMax {
		common.Info("command", fmt.Sprintf("weapon %d x%d at level %d, breach %d, resonance %d", id, count, level, breach, reson))
		return fmt.Sprintf("gave weapon %d at level %d, breach %d, resonance %d.", id, level, breach, reson)
	}
	common.Info("command", fmt.Sprintf("weapon %d x%d", id, count))
	return fmt.Sprintf("gave %d of weapon %d.", count, id)
}

// WavebandOf is the item a role's resonance chain consumes: the ActivateConsume item of the
// first ResonantChain row of its ResonantChainGroupId; false if it has none.
func WavebandOf(s *PlayerSession, roleID int32) (int32, bool) {
	t := s.Data.Tables
	ri, ok := t.RoleInfoById[roleID]
	if !ok {
		return 0, false
	}
	var rows []*data.ResonantChain
	for _, c := range t.ResonantChain {
		if c.GroupId == ri.ResonantChainGroupId {
			rows = append(rows, c)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].GroupIndex < rows[j].GroupIndex })
	for _, c := range rows {
		if c.ActivateConsume != nil && len(c.ActivateConsume.Pairs) > 0 {
			return c.ActivateConsume.Pairs[0].Key, true
		}
	}
	return 0, false
}

// cmdGiveWaveband: pushes go out with NoTips.
func cmdGiveWaveband(s *PlayerSession, args []string) string {
	argCount(args, 1, 2)
	roleID := s.Formation().CurRole
	if args[0] != "current" {
		roleID = argInt(args[0], "role id")
	}
	if _, ok := s.Data.Tables.RoleInfoById[roleID]; !ok {
		cmdFail("no role %d in RoleInfo", roleID)
	}
	item, ok := WavebandOf(s, roleID)
	if !ok {
		cmdFail("role %d has no resonance chain item", roleID)
	}
	if len(args) > 1 && args[1] == "clear" {
		had := s.ItemCount(item)
		if had > 0 {
			s.Actions().TakeItem(item, nil)
		}
		common.Info("command", fmt.Sprintf("waveband %d clear: item %d, %d removed", roleID, item, had))
		return fmt.Sprintf("removed %d of item %d (role %d's resonance chain item).", had, item, roleID)
	}
	count := int32(6)
	if len(args) > 1 {
		count = argPositive(args[1], "count")
	}
	now := s.Actions().GrantItem(item, count, true)
	common.Info("command", fmt.Sprintf("waveband %d: item %d x%d (now %d)", roleID, item, count, now))
	return fmt.Sprintf("gave %d of item %d (role %d's resonance chain item, now %d).", count, item, roleID, now)
}

// cmdQuest: the robot drives stuck steps and starts quests with it.
func cmdQuest(s *PlayerSession, args []string) string {
	e := s.Quests()
	if e == nil {
		cmdFail("the server runs without quest data")
	}
	if len(args) == 0 {
		return e.DescribeProgress()
	}
	switch args[0] {
	case "next":
		argCount(args, 1, 1)
		return fmt.Sprintf("completed %d active step(s).", e.SkipActive())
	case "start":
		argCount(args, 2, 2)
		id := argInt(args[1], "quest id")
		if !e.Accept(id) {
			return fmt.Sprintf("quest %d was not started (unknown, finished or running).", id)
		}
		e.Save()
		return fmt.Sprintf("started quest %d.", id)
	}
	cmdFail("no subcommand \"%s\"", args[0])
	return ""
}

// cmdDungeon: the transfer goes out after the reply, as for EnterInstRequest.
func cmdDungeon(s *PlayerSession, args []string) string {
	q := s.QuestState
	if q == nil {
		cmdFail("the server runs without quest data")
	}
	if len(args) == 0 {
		cmdFail("takes enter <id> or leave")
	}
	switch args[0] {
	case "enter":
		argCount(args, 2, 2)
		id := argInt(args[1], "dungeon id")
		if !q.Dungeons.Enter(s, id, 0) {
			cmdFail("no instance %d in InstanceDungeon", id)
		}
		return fmt.Sprintf("entering dungeon %d.", id)
	case "leave":
		argCount(args, 1, 1)
		if q.Dungeons.Leave(s) {
			return "leaving the dungeon."
		}
		return "not inside a dungeon entered here."
	case "killall":
		argCount(args, 1, 1)
		return killAllMonsters(s)
	}
	cmdFail("no subcommand \"%s\"", args[0])
	return ""
}

func killAllMonsters(s *PlayerSession) string {
	var vehicle int64
	if s.Scene != nil {
		vehicle = s.Scene.VehicleID
	}
	var monsters []*combat.CombatEntity
	for _, e := range s.Combat.Scene.Entities.Values() {
		if e.IsMonster() && e.NetID != vehicle {
			monsters = append(monsters, e)
		}
	}
	hooks := s.Systems.Hooks(s)
	for _, m := range monsters {
		if hooks != nil {
			hooks.OnMonsterKilled(m)
		}
		s.RemoveFromScene(m.NetID)
	}
	return fmt.Sprintf("killed %d monster(s).", len(monsters))
}

func cmdGoto(s *PlayerSession, args []string) string {
	argCount(args, 3, 3)
	to := world.Vec3{X: argFloat(args[0], "x"), Y: argFloat(args[1], "y"), Z: argFloat(args[2], "z")}
	if !s.Teleport(to, nil) {
		cmdFail("not in a scene")
	}
	return fmt.Sprintf("moved to %s %s %s.", f0(to.X), f0(to.Y), f0(to.Z))
}

func cmdMap(s *PlayerSession, args []string) string {
	argCount(args, 1, 1)
	id := argInt(args[0], "map id")
	if _, ok := s.Data.Tables.InstanceDungeonById[id]; !ok {
		cmdFail("no instance %d in InstanceDungeon", id)
	}
	s.ChangeSceneAfterReply(id)
	return fmt.Sprintf("moving to map %d.", id)
}

func cmdOpenShop(s *PlayerSession, args []string) string {
	argCount(args, 0, 0)
	s.PushShopInfo()
	return "pushed shop list."
}

func cmdAchieve(s *PlayerSession, args []string) string {
	argCount(args, 1, 1)
	id := argInt(args[0], "achievement id")
	if !s.Achievements().Finish(s, id) {
		cmdFail("no achievement %d in Achievement", id)
	}
	return fmt.Sprintf("finished achievement %d.", id)
}

func cmdFind(s *PlayerSession, args []string) string {
	argCount(args, 0, 2)
	sc := s.Scene
	if sc == nil {
		cmdFail("not in a scene")
	}
	kind := "chest"
	if len(args) > 0 {
		kind = args[0]
	}
	n := int32(1)
	if len(args) > 1 {
		n = argPositive(args[1], "n")
	}
	var wantDoor bool
	switch kind {
	case "chest":
	case "door":
		wantDoor = true
	default:
		cmdFail("kind must be chest or door")
	}
	type match struct {
		row   *data.LevelEntityConfig
		pos   world.Vec3
		dist2 float64
	}
	var matches []match
	for _, c := range s.Data.CandidatesOf(sc.InstanceID) {
		typ := ""
		if bp := s.Data.Blueprint(c.Row.BlueprintType); bp != nil {
			typ = bp.EntityType
		}
		isDoor := s.Data.IsDoorTemplate(c.Row.BlueprintType) || world.HasDoorOption(c.Row.ComponentsData) || world.EntranceTypes[typ]
		if wantDoor && !isDoor || !wantDoor && !strings.Contains(c.Row.BlueprintType, "TreasureBox") {
			continue
		}
		if s.IsDead(int64(c.Row.EntityId)) {
			continue
		}
		dx := float64(c.Location.X - sc.PlayerPos.X)
		dy := float64(c.Location.Y - sc.PlayerPos.Y)
		dz := float64(c.Location.Z - sc.PlayerPos.Z)
		matches = append(matches, match{c.Row, c.Location, dx*dx + dy*dy + dz*dz})
	}
	// List<T>.Sort is not stable; the distances of distinct entities rarely tie.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].dist2 < matches[j].dist2 })
	if int(n) > len(matches) {
		cmdFail("found %d matching %s(s)", len(matches), kind)
	}
	picked := matches[n-1]
	s.Teleport(picked.pos, nil)
	return fmt.Sprintf("teleported to %s %d at %s %s %s.", kind, picked.row.EntityId, f0(picked.pos.X), f0(picked.pos.Y), f0(picked.pos.Z))
}

func cmdFlowStart(s *PlayerSession, args []string) string {
	argCount(args, 3, 3)
	list := args[0]
	flowID := argInt(args[1], "flow id")
	stateID := argInt(args[2], "state id")
	inc := s.Clock.RealMs()
	s.Flows().Send(&protocol.FlowStartNotify{FlowIncId: inc, FlowListName: list, FlowId: flowID, StateId: stateID}, s.Sink())
	return fmt.Sprintf("started flow %s %d %d (inc %d).", list, flowID, stateID, inc)
}

func cmdFlowTp(s *PlayerSession, args []string) string {
	argCount(args, 4, 4)
	actionID := argInt(args[0], "action id")
	pos := world.Vec3{X: argFloat(args[1], "x"), Y: argFloat(args[2], "y"), Z: argFloat(args[3], "z")}
	sc := s.Scene
	if sc == nil {
		cmdFail("not in a scene")
	}
	sc.PlayerPos = pos
	s.Client.Push(&protocol.TeleportNotify{
		MapId: sc.InstanceID, Position: pos.ToPb(), Rotation: &protocol.Vector{X: 0, Y: 0, Z: sc.PlayerRot.Yaw},
		Reason: protocol.TeleportReason_TeleportReason_FlowAction,
		GameCtx: &protocol.GameCtxPb{CtxType: protocol.GameCtxType_GameCtxType_FlowActionCtx,
			CtxInfo: &protocol.GameCtxPb_FlowAction{FlowAction: &protocol.FlowActionCtxPb{ActionId: actionID}}},
	})
	return fmt.Sprintf("flowtp action %d to %s %s %s.", actionID, f0(pos.X), f0(pos.Y), f0(pos.Z))
}

func cmdFlowEnd(s *PlayerSession, args []string) string {
	if len(args) != 1 && len(args) != 4 {
		cmdFail("takes <inc_id> [flow_list_name flow_id state_id]")
	}
	inc, ok := parseSigned(strings.TrimFunc(args[0], isNetSpace), 64)
	if !ok {
		cmdFail("inc id \"%s\" is not a whole number", args[0])
	}
	if len(args) == 4 {
		s.Client.Push(&protocol.FlowServerSkipNotify{FlowListName: args[1], FlowId: argInt(args[2], "flow id"), StateId: argInt(args[3], "state id")})
	}
	s.Client.Push(&protocol.FlowEndNotify{FlowIncId: inc, IsFinished: true})
	s.Flows().SendEnded(inc, s.Sink())
	return fmt.Sprintf("ended flow %d.", inc)
}

func cmdBattle(s *PlayerSession, args []string) string {
	argCount(args, 1, 1)
	on := ArgBool(args[0])
	if on {
		s.Combat.Scene.ActiveCombatants.Set(1, struct{}{})
	} else {
		s.Combat.Scene.ActiveCombatants.Clear()
	}
	s.Client.Push(&protocol.PlayerBattleStateChangeNotify{PlayerId: s.State.PlayerID(), InBattle: on})
	if on {
		return "battle state on."
	}
	return "battle state off."
}

// attributeByWord is C#'s Enum.TryParse<EAttributeType>(word, ignoreCase: true): a name in any
// case, or a number.
func attributeByWord(word string) (combat.Attr, string, bool) {
	if n, ok := parseSigned(strings.TrimFunc(word, isNetSpace), 32); ok {
		return combat.Attr(n), world.AttributeName(int32(n)), true
	}
	return world.AttributeIDFold(strings.TrimFunc(word, isNetSpace))
}

func cmdMutateAttribute(s *PlayerSession, args []string, mode string) string {
	expected := 2
	if mode == "set" {
		expected = 3
	}
	argCount(args, expected, expected)
	var targets []*combat.CombatEntity
	if args[0] == "team" {
		for _, slot := range s.Formation().FilledRoles() {
			if live := s.Combat.Formation.FindLiveRoleEntity(slot.RoleID); live != nil {
				targets = append(targets, live)
			}
		}
	} else if live := s.Combat.Formation.FindLiveRoleEntity(argInt(args[0], "role id")); live != nil {
		targets = append(targets, live)
	}
	if len(targets) == 0 {
		cmdFail("no spawned role entity for \"%s\"", args[0])
	}
	a, name, ok := attributeByWord(args[1])
	if !ok || a <= combat.AttrNone || a >= combat.AttrCount {
		cmdFail("unknown attribute \"%s\"", args[1])
	}
	var explicit int32
	if mode == "set" {
		explicit = argInt(args[2], "value")
	}
	var notifies []*protocol.CombatNotifyData
	for _, live := range targets {
		var val int32
		switch mode {
		case "empty":
		case "set":
			val = explicit
		default:
			val = 10_000_000
			if m, ok := combat.MaxAttribute(a); ok {
				val = live.Attributes.Slots[m].Current
			}
		}
		slot := &live.Attributes.Slots[a]
		slot.Current = val
		slot.Base = val
		live.FsmDirty = true
		notifies = append(notifies, live.Attributes.BuildChangeNotifies(live.NetID, []combat.Attr{a}, s.Clock.RealMs())...)
	}
	combat.PushCombatNotifies(s.Sink(), notifies)
	return fmt.Sprintf("updated %s on %d role(s).", name, len(targets))
}

func echoStatCode(code string) (propID, addType int32) {
	switch strings.ToLower(code) {
	case "a":
		return 8, 1 // Crit Rate
	case "b":
		return 9, 1 // Crit DMG
	case "c":
		return 10007, 2 // ATK%
	case "d":
		return 10002, 2 // HP%
	case "e":
		return 10010, 2 // DEF%
	case "f":
		return 23, 1 // Healing Bonus
	case "g":
		return 15, 1 // Glacio DMG
	case "h":
		return 16, 1 // Fusion DMG
	case "i":
		return 17, 1 // Electro DMG
	case "j":
		return 18, 1 // Aero DMG
	case "k":
		return 19, 1 // Spectro DMG
	case "l":
		return 20, 1 // Havoc DMG
	case "m":
		return 11, 1 // Energy Regen
	}
	cmdFail("unknown stat code \"%s\" (use a-m)", code)
	return 0, 0
}

func cmdGiveEcho(s *PlayerSession, args []string) string {
	if len(args) < 3 || (len(args)-3)%2 != 0 || len(args) > 13 {
		cmdFail("takes <echo_id> <sonata_id> <main:a-m> [sub:a-m tier]...")
	}
	t := s.Data.Tables
	player := s.Combat.Player
	echoID := argInt(args[0], "echo id")
	sonataID := argInt(args[1], "sonata id")
	itemCfg, ok := t.PhantomItemByItemId[echoID]
	if !ok {
		cmdFail("no echo %d in PhantomItem", echoID)
	}
	mainProp, mainAdd := echoStatCode(args[2])
	maxLevel := int32(25)
	if q, ok := t.PhantomQualityByQuality[itemCfg.QualityId]; ok && q.LevelLimit > 0 {
		maxLevel = q.LevelLimit
	}
	mainProps := []save.EchoPropSave{}
	var mainCandidate *data.PhantomMainPropItem
	for _, m := range t.PhantomMainPropItem {
		if m.PropId == mainProp && (m.AddType == mainAdd || mainAdd == 1) {
			mainCandidate = m
			break
		}
	}
	if mainCandidate != nil {
		mainProps = append(mainProps, save.EchoPropSave{ID: mainCandidate.Id, Value: combat.ComputeMainPropValue(t, mainCandidate, maxLevel)})
	} else if itemCfg.MainProp != nil {
		if groups := combat.FindValidMainPropGroups(t, itemCfg.MainProp.RandGroupId); len(groups) > 0 && groups[0] != nil {
			mainProps = append(mainProps, combat.BuildMainProps(t, groups[0], maxLevel)...)
		}
	}
	subProps := []save.EchoPropSave{}
	for i := 3; i < len(args); i += 2 {
		subProp, subAdd := echoStatCode(args[i])
		tier := max(1, argInt(args[i+1], "tier"))
		var subCfg *data.PhantomSubProperty
		for _, sp := range t.PhantomSubProperty {
			if sp.PropId == subProp && (sp.AddType == subAdd || subAdd == 1) {
				subCfg = sp
				break
			}
		}
		if subCfg == nil {
			continue
		}
		mults, _, maxIdx := combat.GetTierTable(subCfg.PropId, subCfg.AddType, itemCfg.QualityId)
		idx := min(max(int(tier-1), 0), maxIdx)
		val := int32((int64(subCfg.SubStandardProperty)*int64(mults[idx])+5000)/10000) * 10
		subProps = append(subProps, save.EchoPropSave{ID: subCfg.Id, Value: val})
	}
	uid := player.SpecialItemIDs.Next()
	echo := &save.EchoSave{UniqueID: uid, ID: echoID, Level: maxLevel, FetterGroupID: sonataID, MainProp: mainProps, SubProp: subProps}
	player.Echoes.Set(uid, echo)
	save.SaveEcho(player, echo)
	s.Sink().Push(&protocol.PhantomItemAddNotify{PhantomItemList: []*protocol.PhantomItem{combat.ToProtoEcho(echo)}})
	return fmt.Sprintf("created echo %d (uid %d, level %d, sonata %d).", echoID, uid, maxLevel, sonataID)
}

func cmdSpawn(s *PlayerSession, args []string, withMap bool) string {
	if withMap {
		argCount(args, 2, 4)
	} else {
		argCount(args, 1, 3)
	}
	sc := s.Scene
	if sc == nil {
		cmdFail("not in a scene")
	}
	entityID := argInt(args[0], "entity id")
	mapID := sc.MapID
	flagStart := 1
	if withMap {
		mapID = argInt(args[1], "map id")
		flagStart = 2
	}
	freeze := len(args) > flagStart && ArgBool(args[flagStart])
	tuneBreak := len(args) > flagStart+1 && ArgBool(args[flagStart+1])
	row := s.Data.Tables.LevelEntity(mapID, entityID)
	if row == nil {
		cmdFail("no entity %d on map %d", entityID, mapID)
	}
	bp := s.Data.Blueprint(row.BlueprintType)
	var lift float32
	if bp != nil {
		if kind, ok := world.LevelKindOf(bp.EntityLogic); ok && kind.IsCharacter() && bp.HalfHeight > 0 {
			lift = float32(bp.HalfHeight)
		}
	}
	pos := world.Vec3{X: sc.PlayerPos.X, Y: sc.PlayerPos.Y, Z: sc.PlayerPos.Z + lift}
	ctx := &world.LevelContext{WorldLevel: s.World.WorldLevel, SavedState: func(id int32) (int32, bool) { return s.World.State.StateOverride(int64(id)) }}
	entity, _ := world.BuildLevelEntity(s.Data, row, ctx, nil)
	if entity == nil {
		entity = &world.Entity{ConfigID: row.EntityId, ConfigType: world.ConfigLevel, EntityType: world.TypeMonster, Pos: pos}
	}
	entity.Pos = pos
	sc.Add(entity)
	s.RegisterLevelEntity(entity, row, false)
	if c := s.Combat.Scene.GetEntity(entity.ID); c != nil {
		if freeze {
			s.Combat.Buffs.ApplyDirectPassiveBuffs(c, []int64{10010001}, s.Sink())
		}
		if tuneBreak {
			s.Combat.Buffs.ApplyDirectPassiveBuffs(c, []int64{3161, 3162, 3163, 3164, 9000000000}, s.Sink())
		}
	}
	s.Client.Push(&protocol.EntityAddNotify{EntityPbs: []*protocol.EntityPb{entity.ToPb()}})
	return fmt.Sprintf("spawned entity %d (net %d).", entityID, entity.ID)
}

func cmdResetFormation(s *PlayerSession, args []string) string {
	argCount(args, 0, 0)
	s.Combat.Formation.RebuildActiveFormationEntities(s.Clock.RealMs(), s.Sink())
	return "respawned current formation."
}

func cmdPhoto(s *PlayerSession, args []string) string {
	argCount(args, 0, 1)
	enabled := len(args) == 0 || ArgBool(args[0])
	if !enabled {
		s.Client.Push(&protocol.BehaviorTreeDeleteNotify{TreeIncIds: []int64{69}})
		return "disabled photo mode."
	}
	level := &protocol.PhotoFightLevelPb{LevelId: 100016, InstId: 8, IsOpen: true}
	for _, r := range s.Formation().FilledRoles() {
		level.Roles = append(level.Roles, r.RoleID)
	}
	s.Client.Push(&protocol.PhotoFightLevelInfoNotify{PhotoFightLevelInfos: []*protocol.PhotoFightLevelPb{level}})
	s.Client.Push(&protocol.InstPlayDataNotify{Id: 314800019})
	return "enabled photo mode."
}
