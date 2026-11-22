// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/world"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

// ----- the rig (WorldRig): a hand-made world, a state directory -----

const (
	rigPlayer     = 777
	motorTemplate = 36000052
)

type rig struct {
	t      *testing.T
	state  string
	tables *data.DataTables
	data   *world.WorldData
	clock  *common.GameClock
	game   *WorldGame
}

func newRig(t *testing.T) *rig {
	tb := &data.DataTables{
		InstanceDungeon: []*data.InstanceDungeon{
			{Id: 8, MapConfigId: 8, BornPosition: []int32{-143200, 63380, 7610}, BornRotation: []int32{0, 0, 45}},
			{Id: 906, MapConfigId: 906, BornPosition: []int32{88159, -577501, 34162}, BornRotation: []int32{0, 0, 186}},
			{Id: 301010, MapConfigId: 301000},
		},
		DataLayerConfig: []*data.DataLayerConfig{
			{Id: 11, LevelId: 8, InitLoad: true}, {Id: 12, LevelId: 8}, {Id: 13, LevelId: 8, InitLoad: true},
			{Id: 11, LevelId: 8, InitLoad: true}, {Id: 61, LevelId: 906, InitLoad: true}, {Id: 62, LevelId: 906, InitLoad: true},
		},
		InfrRoadBuild: []*data.InfrRoadBuild{
			{Id: 1, DungeonId: 906, UnloadDataLayers: []int32{61}, LoadDataLayers: []int32{70}},
			{Id: 2, DungeonId: 906, LoadDataLayers: []int32{71, 70}},
			{Id: 3, DungeonId: 8, UnloadDataLayers: []int32{11}, LoadDataLayers: []int32{99}},
		},
		ExploreTools: []*data.ExploreTools{
			{PhantomSkillId: 1001, Authorization: &data.Map[int32, int32]{}},
			{PhantomSkillId: 1009, Authorization: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 1, Value: 1}}}},
			{PhantomSkillId: 1015, Authorization: &data.Map[int32, int32]{}},
			{PhantomSkillId: 6001, SummonConfigId: 3001, Authorization: &data.Map[int32, int32]{}},
		},
		SummonCfg: []*data.SummonCfg{
			{Id: 3001, BlueprintType: "Motor"},
			{Id: world.CompanionSummonCfg, BlueprintType: "Companion", BornBuffId: []float64{5001}},
		},
		BlueprintConfig:   []*data.BlueprintConfig{{Id: 1, BlueprintType: "Motor", EntityLogic: "Vehicle", EntityType: "Vehicle"}},
		TemplateConfig:    []*data.TemplateConfig{{Id: motorTemplate, BlueprintType: "Motor"}},
		Buff:              []*data.Buff{{Id: 3000000801}},
		BaseProperty:      []*data.BaseProperty{{Id: world.VehicleProperty, LifeMax: 1000, Life: 1000, Atk: 7}},
		Teleporter:        []*data.Teleporter{{Id: 1, MapId: 8}, {Id: 2, MapId: 906}},
		ItemInfo:          []*data.ItemInfo{{Id: 2}, {Id: 3}, {Id: 41100011}},
		MainRoleConfig:    []*data.MainRoleConfig{{Id: 1501, Gender: 1}, {Id: 1502, Gender: 0}},
		FunctionCondition: []*data.FunctionCondition{{FunctionId: 10001}, {FunctionId: 10002}},
		BackgroundCard:    []*data.BackgroundCard{{Id: 80060001}},
		CalabashLevel:     []*data.CalabashLevel{{Level: 1}, {Level: 5}},
	}
	r := &rig{t: t, state: t.TempDir(), tables: tb.Reindex(), clock: common.NewGameClock(nil)}
	r.data = world.NewWorldData(r.tables, map[int32][]int32{906: {4, 5}})
	r.clock.UseVirtual(1_800_000_000)
	return r
}

func (r *rig) Game() *WorldGame {
	if r.game == nil {
		r.game = NewWorldGame(r.state, r.data, r.clock, combat.NewShared(r.tables, ""))
		r.game.SeedDefaultEchoes = false
	}
	return r.game
}

func (r *rig) playerFile(rel string) string {
	return filepath.Join(r.state, "player", fmt.Sprint(rigPlayer), filepath.FromSlash(rel))
}

func (r *rig) save(rel, text string) {
	p := r.playerFile(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read(rel string) string {
	b, err := os.ReadFile(r.playerFile(rel))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

// saveLivePlayer is a saved player standing on map 8 with roles 1304 (current), 1505 and 1311.
func (r *rig) saveLivePlayer() {
	r.save("basic_info", `.{ .attributes = .{ .level = 70, .name = "Ren", .sex = 1, .coin = 150, .rare_coin = 0, .cash_coin = 1 }, .role_show_list = .{ 1304, 9999 } }`)
	r.save("last_scene", ".{ .instance_id = 8 }")
	r.save("formation_info", `.{ .cur_formation = 0, .formations = .{
	    .{ .cur_role = 1304, .name = null, .roles = .{
	        .{ .role_id = 1311, .entity_id = 5, .on_stage_without_control = false },
	        .{ .role_id = 1505, .entity_id = 6, .on_stage_without_control = false },
	        .{ .role_id = 1304, .entity_id = 7, .on_stage_without_control = false } } },
	}, .future_field = 3 }`)
	r.save("scene/8/instance", ".{ .owner_id = 0, .buff_handle = 0, .map_time = .{ .hour = 8, .minute = 0, .owner_clock_time_span = 0 }, .players = .{ .{ .id = 777, .group_type = 1, .location = .{ -111141.48, 74229.27, 6980.71 }, .rotation = .{ 0, 0, 68.93 } } } }")
	r.save("scene/8/entity/next", "95059777256\n")
	for _, rw := range [][2]int{{1311, 11}, {1505, 12}, {1304, 13}} {
		r.save(fmt.Sprintf("role/%d", rw[0]), fmt.Sprintf(".{ .level = 90, .weapon = %d, .skill_branch = 3, .base_prop = .{ 0, 90, 10000, 10000 }, .add_prop = .{ 0, 0, 500, 500 } }", rw[1]))
		r.save(fmt.Sprintf("weapon/%d", rw[1]), fmt.Sprintf(".{ .id = 2100%d, .breach = 6, .role_id = %d }", rw[1], rw[0]))
	}
	r.save("inventory_info", ".{ .normal_items = .{ .{ .id = 2, .count = 100 }, .{ .id = 4, .count = 1 }, .{ .id = 3 } } }")
	r.save("inventory_info.log", "2 150\n3 0\n")
}

// recording is a client that keeps every push.
type recording struct {
	id   int32
	msgs []proto.Message
}

func (c *recording) PlayerID() int32                              { return c.id }
func (c *recording) Push(m proto.Message)                         { c.msgs = append(c.msgs, m) }
func (c *recording) PushID(_ protocol.MessageID, m proto.Message) { c.msgs = append(c.msgs, m) }

func (c *recording) names() []string {
	var out []string
	for _, m := range c.msgs {
		out = append(out, msgName(m))
	}
	return out
}

// last is the last push of type T.
func last[T proto.Message](t *testing.T, c *recording) T {
	t.Helper()
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if m, ok := c.msgs[i].(T); ok {
			return m
		}
	}
	var zero T
	t.Fatalf("no %T pushed", zero)
	return zero
}

func (r *rig) enter(reconnect bool) (*PlayerSession, *recording) {
	c := &recording{id: rigPlayer}
	p := r.Game().NewSession(c)
	p.Enter(reconnect)
	return p, c
}

// ----- EnterWorldTests -----

// loginData is specs/enter-world.md §3 without DressPlayerTitleUpdateNotify (no title).
var loginData = []string{
	"StorageInfoNotify", "BasicInfoNotify", "NormalItemUpdateNotify", "WeaponSkinAddNotify", "FlyEquipAddNotify",
	"RoleFlyEquipNotify", "OrnamentInfoNotify", "RoleChangeUnlockNotify", "PbGetRoleListNotify",
	"RoleFavorListNotify", "RoleMotionListNotify", "RoleConfigInfoNotify", "FuncOpenNotify", "AreaTerminalInfoNotify",
	"AdviceSettingNotify", "ControlInfoNotify", "InstDataNotify", "CalabashMsgNotify", "PhantomInteractionUnlockNotify",
	"CalabashLevelsRewardNotify", "ItemPkgOpenNotify", "BuffItemNotify", "EnergyUpdateNotify", "LevelPlayInfoNotify",
	"PlayerVarNotify", "RoguelikeCurrencyNotify", "PassiveSkillNotify", "MailInfosNotify", "SettingNotify",
	"TrackMoonIllustratedNotify", "TrackMoonTokenCoinNotify", "SilenceNpcNotify", "ContextIdNotify", "PushDataCompleteNotify",
}

// sceneJoin is specs/enter-world.md §4 on a map without roads.
var sceneJoin = []string{
	"ExploreToolAllNotify", "ExploreSkillRouletteUpdateNotify", "VisionExploreSkillNotify", "JoinSceneNotify",
	"AfterJoinSceneNotify", "QuestFinishListNotify", "QuestListNotify", "BehaviorTreeInfoNotify", "QuestReadyListNotify",
	"SwitchBattleModeNotify", "InfrRoadUpdateNotify", "TimeCheckNotify", "FormationAttrNotify", "UpdateFormationNotify",
}

func eq[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestLoginPushesTheDataThenTheSceneJoinInTheCapturedOrder(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	_, c := r.enter(false)
	eq(t, c.names(), append(slices.Clone(loginData), sceneJoin...))
}

func TestAReconnectSendsTheSamePushes(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	p, c := r.enter(true)
	if !p.Reconnected {
		t.Fatal("not reconnected")
	}
	eq(t, c.names(), append(slices.Clone(loginData), sceneJoin...))
}

func TestATitleAddsItsPushAfterTheBasicInfo(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("basic_info", ".{ .cur_player_title_id = 42 }")
	_, c := r.enter(false)
	eq(t, c.names()[:4], []string{"StorageInfoNotify", "BasicInfoNotify", "DressPlayerTitleUpdateNotify", "NormalItemUpdateNotify"})
	if n := last[*protocol.DressPlayerTitleUpdateNotify](t, c); n.PlayerTitleId != 42 || n.PlayerTitleExtraParam != 1 {
		t.Fatal(n)
	}
}

func TestLoginDataCarriesTheSavedPlayer(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	_, c := r.enter(false)
	b := last[*protocol.BasicInfoNotify](t, c)
	attr := func(k protocol.PlayerAttrKey) *protocol.PlayerAttr {
		for _, a := range b.Attributes {
			if a.Key == k {
				return a
			}
		}
		t.Fatal("no attribute", k)
		return nil
	}
	if b.Id != rigPlayer || attr(protocol.PlayerAttrKey_PlayerAttrKey_Name).GetStringValue() != "Ren" ||
		attr(protocol.PlayerAttrKey_PlayerAttrKey_Level).GetInt32Value() != 70 ||
		attr(protocol.PlayerAttrKey_PlayerAttrKey_Coin).GetInt32Value() != 150 || len(b.Attributes) != 15 {
		t.Fatal(b)
	}
	if len(b.RoleShowList) != 1 || b.RoleShowList[0].RoleId != 1304 || b.RoleShowList[0].Level != 90 || b.CardUnlockList[0].CardId != 80060001 {
		t.Fatal(b.RoleShowList)
	}
	items := last[*protocol.NormalItemUpdateNotify](t, c)
	if len(items.NormalItemList) != 1 || items.NormalItemList[0].Id != 2 || items.NormalItemList[0].Count != 150 || !items.NoTips {
		t.Fatal(items)
	}
	eq(t, last[*protocol.RoleChangeUnlockNotify](t, c).UnlockRoleIds, []int32{1501})
	roles := last[*protocol.PbGetRoleListNotify](t, c).RoleList
	var ids []int32
	for _, x := range roles {
		ids = append(ids, x.RoleId)
	}
	eq(t, ids, []int32{1304, 1311, 1505})
	if len(roles[0].BaseProp) != int(combat.AttrCount) {
		t.Fatal(len(roles[0].BaseProp))
	}
	var branches []int32
	for _, x := range last[*protocol.RoleConfigInfoNotify](t, c).RoleConfigs {
		branches = append(branches, x.SkillBranch)
	}
	eq(t, branches, []int32{3, 3, 3})
	eq(t, last[*protocol.ItemPkgOpenNotify](t, c).OpenPkg, []int32{0, 1, 2, 3, 4, 5, 6, 7})
	if last[*protocol.CalabashMsgNotify](t, c).CalabashMsg.Level != 5 {
		t.Fatal("calabash")
	}
}

func kindsOf(e *protocol.EntityPb) []string {
	var out []string
	for _, c := range e.ComponentPbs {
		out = append(out, strings.TrimPrefix(fmt.Sprintf("%T", c.Component), "*protocol.EntityComponentPb_"))
	}
	return out
}

func TestTheJoinMakesTheFourPlayerEntitiesThenTheRoles(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	p, c := r.enter(false)
	info := last[*protocol.JoinSceneNotify](t, c).SceneInfo
	es := info.AoiData.Entities
	var types, cfgs []int32
	var ids []int64
	var vis []bool
	for _, e := range es {
		types, cfgs, ids, vis = append(types, e.EntityType), append(cfgs, e.ConfigId), append(ids, e.Id), append(vis, e.IsVisible)
		if e.PlayerId != rigPlayer {
			t.Fatal(e.PlayerId)
		}
	}
	eq(t, types, []int32{10, 11, 2, 12, 0, 0, 0})
	eq(t, cfgs, []int32{motorTemplate, world.PlayerEntityTemplate, world.CompanionTemplate, world.BattleEntityTemplate, 1311, 1505, 1304})
	for i, id := range ids {
		if id != 95059777256+int64(i) {
			t.Fatal(ids)
		}
	}
	if strings.TrimSpace(r.read("scene/8/entity/next")) != "95059777512" {
		t.Fatal(r.read("scene/8/entity/next"))
	}
	eq(t, vis, []bool{false, true, false, false, false, false, true})
	me := info.PlayerInfos[0]
	if me.CurRole != 1304 || me.Location.X != -111141.48 || me.Rotation.Yaw != 68.93 || me.PlayerName != "Ren" {
		t.Fatal(me)
	}
	if info.SceneEntityId != 2 || info.CurContextId != rigPlayer || info.TimeInfo.Hour != 8 {
		t.Fatal(info.SceneEntityId)
	}
	eq(t, info.DataLayers, []int32{11, 13})
	saved, err := zon.ParseFile(r.playerFile("formation_info"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(zon.Write(saved), fmt.Sprintf(".entity_id = %d,", ids[6])) || !strings.Contains(zon.Write(saved), ".future_field = 3,") {
		t.Fatal(zon.Write(saved))
	}
	if id, _ := p.Scene.RoleEntities.Get(1304); id != ids[6] {
		t.Fatal(id)
	}
}

func TestEntityComponentsFollowTheCapturedLists(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	_, c := r.enter(false)
	e := last[*protocol.JoinSceneNotify](t, c).SceneInfo.AoiData.Entities
	eq(t, kindsOf(e[0]), []string{"AttributeComponent", "TagComponent", "FightBuffComponent", "VehiclePb", "MotorOutlookComponentPb", "MotorDaCtxComponentPb", "FollowEntityComponentPb"})
	eq(t, kindsOf(e[1]), []string{"AttributeComponent", "FightBuffComponent", "PlayerFollowersComponentPb"})
	eq(t, kindsOf(e[2]), []string{"AttributeComponent", "FightBuffComponent", "CombineEntityInfoPb", "EquipComponent", "LogicStateComponentPb", "MonsterAiComponentPb", "SummonerComponent", "PlayerFollowerSelfComponentPb"})
	eq(t, kindsOf(e[3]), []string{"AttributeComponent", "FightBuffComponent"})
	eq(t, kindsOf(e[4]), []string{"AttributeComponent", "FightBuffComponent", "ConcomitantsComponentPb", "EquipComponent", "LogicStateComponentPb", "SkillComponentPb", "DependenceComponentPb", "VisionSkillComponent", "RoleWeaponSkinComponent", "OrnamentComponentPb", "CalabashSkinComponentPb"})
	if e[0].ComponentPbs[5].GetMotorDaCtxComponentPb().MotorDaCtxId != e[0].Id || e[0].ComponentPbs[6].GetFollowEntityComponentPb().EntityId != e[2].Id {
		t.Fatal("vehicle links")
	}
	if e[4].ComponentPbs[3].GetEquipComponent().WeaponId != 210011 {
		t.Fatal(e[4].ComponentPbs[3])
	}
	eq(t, e[4].ComponentPbs[6].GetDependenceComponentPb().DependenceEntityIds, []int64{1, 2, 1, 2})
	f := last[*protocol.UpdateFormationNotify](t, c).PlayersFormations[0].Formations[0]
	if f.FormationId != 1 || !f.IsCurrent || f.CurRole != 1304 || len(f.RoleInfos) != 3 {
		t.Fatal(f)
	}
}

func TestANewPlayerStandsAtTheBornPointWithDefaultTeamAndExploreTools(t *testing.T) {
	r := newRig(t)
	p, c := r.enter(false)
	if _, err := os.Stat(r.playerFile("basic_info")); err != nil {
		t.Fatal(err)
	}
	info := last[*protocol.JoinSceneNotify](t, c).SceneInfo
	me := info.PlayerInfos[0]
	if info.InstanceId != 8 || me.Location.X != -143200 || me.Location.Y != 63380 || me.Location.Z != 7610 || me.Rotation.Yaw != 45 || me.CurRole != 1211 {
		t.Fatal(me)
	}
	var roles []int32
	for _, x := range me.PlayerSceneGroupData[0].FightRoleInfos {
		roles = append(roles, x.RoleId)
	}
	eq(t, roles, []int32{1211, 1108, 1506})
	if info.AoiData.Entities[0].Id != 1 || strings.TrimSpace(r.read("scene/8/entity/next")) != "257" {
		t.Fatal(info.AoiData.Entities[0].Id)
	}
	tools := p.State.ExploreTools
	eq(t, tools.UnlockedExploreSkills, []int32{1001, 1015, 6001})
	eq(t, tools.Roulette, []int32{6002, 1001, 1007, 1009, 1015, 0, 0, 0})
	eq(t, tools.MotorcycleRoulette, []int32{6001, 6003, 6007, 6011, 6012, 6020, 0, 0})
	ro := last[*protocol.ExploreSkillRouletteUpdateNotify](t, c).RouletteInfo
	if len(ro) != 4 || len(ro[2].SkillIds) != 0 || len(ro[1].SkillIds) != 8 || ro[3].ExploreSkill != 6001 {
		t.Fatal(ro)
	}
	r.save("explore_tools_info", ".{ .unlocked_explore_skills = .{ 1 }, .roulette = .{ 1 } }")
	again, _ := r.enter(false)
	eq(t, again.State.ExploreTools.UnlockedExploreSkills, []int32{1})
}

func TestASecondJoinStartsANewBlockOfIds(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	first, _ := r.enter(false)
	es := first.Scene.Entities()
	lastFirst := es[len(es)-1].ID
	second, _ := r.enter(false)
	if id := second.Scene.Entities()[0].ID; id != 95059777512 || id <= lastFirst {
		t.Fatal(id)
	}
}

func TestLahaiRoiSwapsTheRoadLayersAndSyncsItsRoads(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	r.save("last_scene", ".{ .instance_id = 906 }")
	_, c := r.enter(false)
	eq(t, last[*protocol.JoinSceneNotify](t, c).SceneInfo.DataLayers, []int32{62, 70, 71})
	roads := last[*protocol.SceneRoadSyncNotify](t, c)
	if roads.InstanceId != 906 {
		t.Fatal(roads)
	}
	eq(t, roads.EnabledRoads, []int32{4, 5})
	var built []int32
	for _, x := range last[*protocol.InfrRoadUpdateNotify](t, c).RoadInfo.Roads {
		built = append(built, x.RoadId)
	}
	eq(t, built, []int32{1, 2})
	if len(c.names())-len(loginData) != len(sceneJoin)+1 {
		t.Fatal(len(c.names()))
	}
}

func TestTimeCheckNotifyCarriesRealTimeAndTheFlowTime(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	_, c := r.enter(false)
	n := last[*protocol.TimeCheckNotify](t, c)
	if n.ServerTime != r.clock.RealMs() || n.ServerStopTime != n.ServerTime || n.SceneStopTime != n.ServerTime || n.FlowTime != n.ServerTime || n.ClientTime != 0 {
		t.Fatal(n)
	}
}

func TestReleaseWritesThePositionAndOnlyDueMovesAreSavedBefore(t *testing.T) {
	r := newRig(t)
	r.saveLivePlayer()
	p, _ := r.enter(false)
	p.Scene.PlayerPos = world.Vec3{X: 1, Y: 2, Z: 3}
	p.Scene.MarkMoved(r.clock.Ms())
	r.clock.Advance(29_999)
	p.Tick()
	if !strings.Contains(r.read("scene/8/instance"), "-111141.48") {
		t.Fatal(r.read("scene/8/instance"))
	}
	p.Release()
	if text := r.read("scene/8/instance"); !strings.Contains(text, ".location = .{ 1, 2, 3 },") || !strings.Contains(text, ".rotation = .{ 0, 0, 68.93 },") {
		t.Fatal(text)
	}
}

func TestJoinFailsWithoutAMotorcycleInTheData(t *testing.T) {
	r := newRig(t)
	r.tables.ExploreTools = nil
	r.data = world.NewWorldData(r.tables.Reindex(), nil)
	defer func() {
		if rec := recover(); rec != world.ErrNoVehicle {
			t.Fatal(rec)
		}
	}()
	r.enter(false)
}
