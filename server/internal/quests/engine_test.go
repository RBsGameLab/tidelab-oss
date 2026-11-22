// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// -----------------------------------------------------------------------------
// Test doubles

type clientRangeRecord struct {
	start int
	end   int
	ctx   protocol.GameCtxType
}

type transferRecord struct {
	instance int32
	pos      [3]float32
}

type fakeQuestHost struct {
	files         *save.PlayerFiles
	pushes        []proto.Message
	instanceID    int32
	mapID         int32
	nowMs         int64
	tables        *data.DataTables
	world         *save.WorldState
	spawned       map[int64]int64
	spawnedStates map[int64]int32
	health        map[int64]int32
	entityPos     map[int64][3]float32
	items         map[int32]int32
	drops         []int32
	serverActions [][]QuestAction
	clientRanges  []clientRangeRecord
	transfers     []transferRecord
	hidden        []int64
}

func newFakeQuestHost(t *testing.T, tables *data.DataTables) *fakeQuestHost {
	t.Helper()
	if tables == nil {
		tables = &data.DataTables{
			InstanceDungeonById:     map[int32]*data.InstanceDungeon{},
			GameplayTagRemapByRawId: map[int32]*data.GameplayTagRemap{},
		}
	}
	files := save.NewPlayerFiles(t.TempDir(), 777)
	return &fakeQuestHost{
		files:         files,
		instanceID:    8,
		mapID:         8,
		nowMs:         1_000_000,
		tables:        tables,
		world:         save.NewWorldState(files),
		spawned:       map[int64]int64{},
		spawnedStates: map[int64]int32{},
		health:        map[int64]int32{},
		entityPos:     map[int64][3]float32{},
		items:         map[int32]int32{},
	}
}

func (h *fakeQuestHost) Push(msg proto.Message)   { h.pushes = append(h.pushes, msg) }
func (h *fakeQuestHost) PlayerID() int32          { return 777 }
func (h *fakeQuestHost) InstanceID() int32        { return h.instanceID }
func (h *fakeQuestHost) MapID() int32             { return h.mapID }
func (h *fakeQuestHost) NowMs() int64             { return h.nowMs }
func (h *fakeQuestHost) NowSeconds() int64        { return h.nowMs / 1000 }
func (h *fakeQuestHost) Tables() *data.DataTables { return h.tables }
func (h *fakeQuestHost) World() *save.WorldState  { return h.world }
func (h *fakeQuestHost) Files() *save.PlayerFiles { return h.files }
func (h *fakeQuestHost) QuestsFilePath() string   { return h.files.PathOf(QuestsFileName) }
func (h *fakeQuestHost) NetIDOf(id int64) (int64, bool) {
	n, ok := h.spawned[id]
	return n, ok
}
func (h *fakeQuestHost) SpawnedState(id int64) (int32, bool) {
	s, ok := h.spawnedStates[id]
	return s, ok
}
func (h *fakeQuestHost) HealthPercent(id int64) (int32, bool) {
	p, ok := h.health[id]
	return p, ok
}
func (h *fakeQuestHost) RefreshSpawns()            {}
func (h *fakeQuestHost) ItemCount(id int32) int32  { return h.items[id] }
func (h *fakeQuestHost) GrantItem(id, count int32) { h.items[id] += count }
func (h *fakeQuestHost) TakeItem(id, count int32)  { h.items[id] -= count }
func (h *fakeQuestHost) GrantDrop(dropID, _ int32) { h.drops = append(h.drops, dropID) }
func (h *fakeQuestHost) RunActions(a []QuestAction) error {
	h.serverActions = append(h.serverActions, a)
	return nil
}
func (h *fakeQuestHost) PushClientRange(_ []QuestAction, start, end int, ctx *protocol.GameCtxPb) {
	h.clientRanges = append(h.clientRanges, clientRangeRecord{start: start, end: end, ctx: ctx.CtxType})
}
func (h *fakeQuestHost) EntityPos(id int64) ([3]float32, bool) {
	p, ok := h.entityPos[id]
	return p, ok
}
func (h *fakeQuestHost) HideEntity(id int64) {
	h.hidden = append(h.hidden, id)
	h.world.Sleep(id)
}
func (h *fakeQuestHost) QueueTransfer(inst int32, pos [3]float32, _ bool) {
	h.transfers = append(h.transfers, transferRecord{instance: inst, pos: pos})
}

func pushesOf[T proto.Message](h *fakeQuestHost) []T {
	var out []T
	for _, m := range h.pushes {
		if v, ok := m.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

type flowRecorder struct {
	NoSystems
	flows []*protocol.FlowStartNotify
}

func (f *flowRecorder) StartFlow(flow *protocol.FlowStartNotify) {
	f.flows = append(f.flows, flow)
}

// -----------------------------------------------------------------------------
// Rules & tree builders

type fakeProgress struct {
	done     []int32
	nodes    [][2]int32
	plays    []int32
	dungeons []int32
	held     [][2]int32
}

func (p fakeProgress) QuestDone(id int32) bool   { return slices.Contains(p.done, id) }
func (p fakeProgress) PlayDone(id int32) bool    { return slices.Contains(p.plays, id) }
func (p fakeProgress) DungeonDone(id int32) bool { return slices.Contains(p.dungeons, id) }
func (p fakeProgress) NodeDone(quest, node int32) bool {
	return slices.Contains(p.nodes, [2]int32{quest, node})
}
func (p fakeProgress) HasItems(want [][]int32) bool {
	for _, w := range want {
		var have int32
		for _, h := range p.held {
			if h[0] == w[0] {
				have = h[1]
				break
			}
		}
		if have < w[1] {
			return false
		}
	}
	return true
}

func pcond(typ string, quest, node, play, dungeon int32) ProvideCond {
	return ProvideCond{Type: typ, Quest: quest, Node: node, Play: play, Dungeon: dungeon}
}

func qdef(id, typ int32, any bool, conds ...ProvideCond) QuestDef {
	return QuestDef{ID: id, Type: typ, Provide: Provide{Any: any, Conds: conds}}
}

func node(id, parent int32, typ, cond string, kids ...int32) NodeDef {
	return NodeDef{ID: id, Parent: parent, Type: typ, Cond: cond, Kids: kids}
}

func catalogOf(quests ...QuestDef) *Catalog {
	ds := NewQuestDataSet()
	ds.Quests = append(ds.Quests, quests...)
	return NewCatalog(ds)
}

func catalogWithOffers(quests []QuestDef, offers []Offer) *Catalog {
	ds := NewQuestDataSet()
	ds.Quests = append(ds.Quests, quests...)
	for _, o := range offers {
		ds.Offers[o.ID] = o
		ds.OfferOrder = append(ds.OfferOrder, o.ID)
	}
	return NewCatalog(ds)
}

func TestUseSkillCountsInsideTheSphereOnly(t *testing.T) {
	s := UseSkill{ID: 210004, Center: []float64{-43790, 58640, 4240}, Radius: 2000}
	if !InSphere(s, [3]float32{-43491, 59210, 4327}) {
		t.Fatal("point inside sphere should pass")
	}
	if InSphere(s, [3]float32{-45817, 80281, 536}) {
		t.Fatal("point outside sphere should fail")
	}
	if !InSphere(UseSkill{ID: 1}, [3]float32{0, 0, 0}) {
		t.Fatal("no sphere means anywhere")
	}
}

func TestR10UnlockConditions(t *testing.T) {
	abu := qdef(880000013, 1, false, pcond("PreChildQuest", 140800000, 330, 0, 0), pcond("ExploreLevel", 0, 0, 0, 0))
	if Unlocked(abu, fakeProgress{}) {
		t.Fatal("abu should be locked initially")
	}
	if !Unlocked(abu, fakeProgress{nodes: [][2]int32{{140800000, 330}}}) {
		t.Fatal("abu should unlock on pre child quest node")
	}
	if !Unlocked(abu, fakeProgress{done: []int32{140800000}}) {
		t.Fatal("abu should unlock when parent quest finished")
	}
	if Unlocked(abu, fakeProgress{nodes: [][2]int32{{140800000, 331}}}) {
		t.Fatal("wrong child node should not unlock abu")
	}
	dated := qdef(880000017, 1, false, pcond("PreQuest", 880000015, 0, 0, 0), pcond("OpenDate", 0, 0, 0, 0))
	dated.PreQuest = 880000015
	if !Unlocked(dated, fakeProgress{done: []int32{880000015}}) || Unlocked(dated, fakeProgress{}) {
		t.Fatal("dated PreQuest check mismatch")
	}
	old := QuestDef{ID: 2, Type: 1, PreQuest: 1}
	if !Unlocked(old, fakeProgress{done: []int32{1}}) || Unlocked(old, fakeProgress{}) {
		t.Fatal("legacy PreQuest field mismatch")
	}
	if Unlocked(QuestDef{ID: 139000025, Type: 1}, fakeProgress{done: []int32{1, 2}}) {
		t.Fatal("quest without precondition should not auto-unlock")
	}
	if Unlocked(qdef(9, 1, false, pcond("OpenDate", 0, 0, 0, 0)), fakeProgress{}) {
		t.Fatal("OpenDate alone should not auto-unlock")
	}
	either := qdef(10, 1, true, pcond("PreQuest", 5, 0, 0, 0), pcond("PreQuest", 6, 0, 0, 0))
	if !Unlocked(either, fakeProgress{done: []int32{6}}) || Unlocked(either, fakeProgress{}) {
		t.Fatal("Any PreQuest mismatch")
	}
	eitherDated := qdef(11, 1, true, pcond("PreQuest", 5, 0, 0, 0), pcond("OpenDate", 0, 0, 0, 0))
	if Unlocked(eitherDated, fakeProgress{}) || !Unlocked(eitherDated, fakeProgress{done: []int32{5}}) {
		t.Fatal("Any PreQuest+OpenDate should not unlock on OpenDate alone")
	}
}

func TestR61SideQuestConditions(t *testing.T) {
	system := Offer{}
	npc := Offer{Interact: true}
	cat := qdef(126000019, 2, false, pcond("PreQuest", 139000030, 0, 0, 0), pcond("ExploreLevel", 0, 0, 0, 0))
	if !OfferUnlocked(cat, npc, fakeProgress{done: []int32{139000030}}) || OfferUnlocked(cat, npc, fakeProgress{}) {
		t.Fatal("cat offer unlock mismatch")
	}
	play := qdef(136000000, 2, false, pcond("PreLevelPlay", 0, 0, 136000067, 0))
	if OfferUnlocked(play, system, fakeProgress{}) || !OfferUnlocked(play, system, fakeProgress{plays: []int32{136000067}}) {
		t.Fatal("PreLevelPlay offer mismatch")
	}
	afterDungeon := qdef(122000010, 9, false, pcond("PreDungeon", 0, 0, 0, 1509))
	if OfferUnlocked(afterDungeon, system, fakeProgress{}) || !OfferUnlocked(afterDungeon, system, fakeProgress{dungeons: []int32{1509}}) {
		t.Fatal("PreDungeon offer mismatch")
	}
	book := qdef(106800000, 2, false, pcond("Item", 0, 0, 0, 0))
	pages := Offer{Items: [][]int32{{72010002, 3}}}
	if OfferUnlocked(book, pages, fakeProgress{held: [][2]int32{{72010002, 2}}}) ||
		!OfferUnlocked(book, pages, fakeProgress{held: [][2]int32{{72010002, 3}}}) ||
		OfferUnlocked(book, system, fakeProgress{}) {
		t.Fatal("Item offer mismatch")
	}
	if !OfferUnlocked(qdef(118000002, 9, false, pcond("ExploreLevel", 0, 0, 0, 0)), system, fakeProgress{}) {
		t.Fatal("ExploreLevel alone should unlock offer")
	}
	if OfferUnlocked(qdef(1, 2, false, pcond("PreQuest", 5, 0, 0, 0), pcond("OpenDate", 0, 0, 0, 0)), system, fakeProgress{done: []int32{5}}) {
		t.Fatal("OpenDate should block side quest offer")
	}
	free := QuestDef{ID: 122000004, Type: 2}
	if !OfferUnlocked(free, npc, fakeProgress{}) || OfferUnlocked(free, system, fakeProgress{}) {
		t.Fatal("conditionless offer: Interact true, System false")
	}
}

func TestBuildTreeRunsSequencesQuestSucceedLastInLevelPlays(t *testing.T) {
	def := QuestDef{
		ID: 691000003,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", ""),
			node(3, 1, "QuestSucceed", ""),
			node(4, 1, "Sequence", ""),
			node(14, 1, "ChildQuest", "LeaveDungeon"),
		},
	}
	tree := BuildTree(def, ChildOrderTerminalsLast)
	var ids []int32
	for _, k := range tree.Children[tree.Root] {
		ids = append(ids, def.Nodes[k].ID)
	}
	if !slices.Equal(ids, []int32{4, 14, 3}) {
		t.Fatalf("got %v, want [4 14 3]", ids)
	}

	boss := QuestDef{
		ID: 141000012,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", ""),
			node(4, 1, "ChildQuest", "PlayFlow"),
			node(14, 1, "ParallelSelect", ""),
			node(3, 1, "QuestSucceed", ""),
			node(76, 1, "ChildQuest", "PlayFlow"),
			node(73, 1, "ChildQuest", "LeaveDungeon"),
		},
	}
	t2 := BuildTree(boss, ChildOrderTerminalsLast)
	ids = ids[:0]
	for _, k := range t2.Children[t2.Root] {
		ids = append(ids, boss.Nodes[k].ID)
	}
	if !slices.Equal(ids, []int32{4, 14, 76, 73, 3}) {
		t.Fatalf("boss order = %v", ids)
	}

	t3 := BuildTree(def, ChildOrderAsListed)
	if def.Nodes[t3.Children[t3.Root][0]].ID != 3 {
		t.Fatal("AsListed should keep QuestSucceed first")
	}
}

func TestFixCursorsPointsRunningSequenceAtItsActiveChild(t *testing.T) {
	def := QuestDef{
		ID: 139000029,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", "", 261, 262, 117, 3),
			node(117, 1, "ParallelSelect", "", 202),
			node(202, 117, "ChildQuest", "ReachArea"),
			node(261, 1, "ChildQuest", "ScheduleTime"),
			node(262, 1, "ChildQuest", "ReachArea"),
			node(3, 1, "QuestSucceed", ""),
		},
	}
	tree := BuildTree(def, ChildOrderAsListed)
	var kids []int32
	for _, k := range tree.Children[tree.Root] {
		kids = append(kids, tree.Node(k).ID)
	}
	if !slices.Equal(kids, []int32{261, 262, 117, 3}) {
		t.Fatalf("root kids = %v", kids)
	}
	run := newRun(tree, 139000029, false)
	run.Nodes[0] = NodeState{Status: protocol.NodeStatus_NodeStatus_Activated, Cursor: 0}
	run.Nodes[1] = NodeState{Status: protocol.NodeStatus_NodeStatus_Activated}
	run.Nodes[2] = NodeState{Status: protocol.NodeStatus_NodeStatus_Activated, Cq: protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Progress}
	FixCursors(run)
	if run.Nodes[0].Cursor != 2 {
		t.Fatalf("cursor = %d, want 2", run.Nodes[0].Cursor)
	}
}

func TestLevelPlayChoiceTakesMostMonsterPreviewMatches(t *testing.T) {
	defs := []LevelPlayDef{
		{ID: 1, LevelID: 302000, Monsters: []int32{320000260, 320000270}},
		{ID: 2, LevelID: 302000, Monsters: []int32{320000260, 310000120, 310000130}},
		{ID: 3, LevelID: 999, Monsters: []int32{320000260}},
	}
	if id, ok := BestLevelPlay(defs, 302000, []int32{320000260, 310000120, 310000130}); !ok || id != 2 {
		t.Fatalf("got (%d, %v), want 2", id, ok)
	}
	if id, ok := BestLevelPlay(defs, 302000, []int32{320000260, 320000270}); !ok || id != 1 {
		t.Fatalf("got (%d, %v), want 1", id, ok)
	}
	if id, ok := BestLevelPlay(defs, 302000, nil); !ok || id != 1 {
		t.Fatalf("got (%d, %v), want 1", id, ok)
	}
	if _, ok := BestLevelPlay(defs, 1, nil); ok {
		t.Fatal("map 1 should have no level play")
	}
}

func TestR4eDungeonStepsMatchByIdOrSubType(t *testing.T) {
	sub := func(id int32) (int32, bool) {
		switch id {
		case 8:
			return 13, true
		case 1505:
			return 2, true
		case 4043:
			return 5, true
		default:
			return 0, false
		}
	}
	if !DungeonMatches(NodeDef{Dungeon: 1505}, 1505, sub) {
		t.Fatal("exact dungeon match failed")
	}
	if DungeonMatches(NodeDef{Dungeon: 1505}, 8, sub) {
		t.Fatal("mismatched dungeon should be false")
	}
	if !DungeonMatches(NodeDef{SubType: 2}, 1505, sub) {
		t.Fatal("subtype match failed")
	}
	if DungeonMatches(NodeDef{SubType: 2}, 4043, sub) {
		t.Fatal("wrong subtype should be false")
	}
	if DungeonMatches(NodeDef{}, 1505, sub) {
		t.Fatal("empty node should not match")
	}
}

func TestR33aRawParamsReadByPath(t *testing.T) {
	v, err := data.ParseJSON([]byte(`[
		{"Name":"UnlockTeleportTrigger","Raw":{"TeleportId":300201}},
		{"Name":"SetAreaTimeState","Raw":{"SetAreaTimeConfig":{"Type":0,"AreaIds":[33,"x",34],"LockTime":{"Hour":8,"Minutes":30}}}},
		{"Name":"GetItem","Raw":{"Items":[{"ItemId":70010029,"Count":1}]}},
		{"Name":"HideGroup","Raw":{"GroupKey":"q_125000121_虹镇","IsHidePasserByNpc":true}},
		{"Name":"PlayFlow","Params":{"EntityId":5}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	actions := ParseActions(v)
	if n, ok := ActionInt(actions[0], "TeleportId"); !ok || n != 300201 {
		t.Fatalf("TeleportId = (%d, %v)", n, ok)
	}
	if n, ok := ActionInt(actions[1], "SetAreaTimeConfig", "LockTime", "Hour"); !ok || n != 8 {
		t.Fatalf("Hour = (%d, %v)", n, ok)
	}
	if !slices.Equal(ActionInts(actions[1], "SetAreaTimeConfig", "AreaIds"), []int64{33, 34}) {
		t.Fatalf("AreaIds = %v", ActionInts(actions[1], "SetAreaTimeConfig", "AreaIds"))
	}
	items := ActionObjects(actions[2], "Items")
	if len(items) != 1 || JSONLong(ObjectField(items[0], "ItemId"), 0) != 70010029 {
		t.Fatalf("Items = %v", items)
	}
	if s, ok := ActionString(actions[3], "GroupKey"); !ok || s != "q_125000121_虹镇" {
		t.Fatalf("GroupKey = (%q, %v)", s, ok)
	}
	if b, ok := ActionBool(actions[3], "IsHidePasserByNpc"); !ok || !b {
		t.Fatalf("IsHidePasserByNpc = (%v, %v)", b, ok)
	}
	if _, ok := ActionInt(actions[0], "Nope"); ok {
		t.Fatal("missing key should return false")
	}
	if _, ok := ActionInt(actions[3], "GroupKey"); ok {
		t.Fatal("string key should not parse as int")
	}
	if _, ok := ActionInt(actions[4], "EntityId"); ok {
		t.Fatal("PlayFlow Raw is nil")
	}
	if actions[4].Params == nil || actions[4].Params.EntityID == nil || *actions[4].Params.EntityID != 5 {
		t.Fatal("typed Params.EntityID should be 5")
	}
	if len(ActionObjects(actions[4], "Items")) != 0 {
		t.Fatal("nil Raw should return empty objects")
	}
}

// -----------------------------------------------------------------------------
// Engine tests

func twoQuests() []QuestDef {
	talk := &FlowRef{FlowListName: "剧情_测试", FlowID: 3, StateID: 1}
	n2 := node(2, 1, "ChildQuest", "DoInteract")
	n2.Options = []QuestOption{{EntityID: 40, Guid: "g40", TidContent: "t", Flow: talk}}
	return []QuestDef{
		{
			ID: 5, Type: 1, Name: "五", RewardID: 900,
			Nodes: []NodeDef{
				node(1, 0, "Sequence", "", 2, 3, 4),
				n2,
				node(3, 1, "ChildQuest", "ReachArea"),
				node(4, 1, "QuestSucceed", ""),
			},
		},
		{
			ID: 6, Type: 1, Name: "六", PreQuest: 5,
			Nodes: []NodeDef{
				node(1, 0, "Sequence", "", 2),
				node(2, 1, "ChildQuest", "ReachArea"),
			},
		},
	}
}

func TestStoryQuestRunsFromTalkToFinishAndNextStarts(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	host.spawned[40] = 1040
	flows := &flowRecorder{}
	engine := NewEngine(catalogOf(twoQuests()...), host, flows)
	engine.ParseState("F 1\n")
	if !engine.Accept(5) {
		t.Fatal("Accept(5) failed")
	}

	var hasProgress bool
	for _, n := range pushesOf[*protocol.QuestStateUpdateNotify](host) {
		if n.QuestId == 5 && n.State == protocol.QuestState_QuestState_Progress {
			hasProgress = true
		}
	}
	if !hasProgress {
		t.Fatal("expected QuestStateUpdateNotify Progress for 5")
	}
	if len(pushesOf[*protocol.BehaviorTreeInfoNotify](host)) != 1 {
		t.Fatal("expected 1 BehaviorTreeInfoNotify")
	}
	adds := pushesOf[*protocol.EntityAddDynamicInteractNotify](host)
	if len(adds) != 1 || adds[0].EntityId != 1040 || adds[0].OptionGuid != "g40" || adds[0].GameCtx.CtxType != protocol.GameCtxType_GameCtxType_DoInteractChildQuestNode {
		t.Fatalf("unexpected interact notify: %+v", adds)
	}
	if !host.world.IsAwake(40) {
		t.Fatal("NPC 40 should be awake")
	}

	if got := engine.OnDynamicInteract(40, "g40"); got != DynamicInteractFlow {
		t.Fatalf("OnDynamicInteract = %v, want Flow", got)
	}
	if len(flows.flows) != 1 || flows.flows[0].FlowIncId != 500001 || flows.flows[0].FlowListName != "剧情_测试" {
		t.Fatalf("unexpected flows: %+v", flows.flows)
	}
	engine.OnFlowEnd(flows.flows[0].FlowIncId)
	if len(pushesOf[*protocol.EntityRemoveDynamicInteractNotify](host)) != 1 {
		t.Fatal("expected option removal after flow end")
	}
	steps := engine.ActiveSteps(false)
	if len(steps) != 1 || steps[0].Def.Cond != "ReachArea" {
		t.Fatalf("active steps = %+v", steps)
	}

	if code := engine.SubmitNode(5, 2); code != protocol.ErrorCode_ErrorCode_ErrTreeNodeNotActive {
		t.Fatalf("SubmitNode(5, 2) = %v", code)
	}
	if code := engine.SubmitNode(5, 3); code != protocol.ErrorCode_ErrorCode_Success {
		t.Fatalf("SubmitNode(5, 3) = %v", code)
	}
	if !engine.IsFinished(5) || !slices.Equal(host.drops, []int32{900}) || !engine.IsRunning(6) {
		t.Fatalf("fin5=%v drops=%v run6=%v", engine.IsFinished(5), host.drops, engine.IsRunning(6))
	}
	var hasFinish, hasDelete bool
	for _, n := range pushesOf[*protocol.QuestStateUpdateNotify](host) {
		if n.QuestId == 5 && n.State == protocol.QuestState_QuestState_Finish {
			hasFinish = true
		}
	}
	for _, n := range pushesOf[*protocol.BehaviorTreeDeleteNotify](host) {
		if slices.Contains(n.TreeIncIds, 5) {
			hasDelete = true
		}
	}
	if !hasFinish || !hasDelete {
		t.Fatalf("hasFinish=%v hasDelete=%v", hasFinish, hasDelete)
	}

	b, err := os.ReadFile(host.QuestsFilePath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "F 5\n") || !strings.Contains(text, "P 6 0:2:0:0:0 1:2:3:0:0 v\n") {
		t.Fatalf("unexpected saved quests log:\n%s", text)
	}
	again := NewEngine(catalogOf(twoQuests()...), host, nil)
	again.Load()
	if !again.IsFinished(5) || len(again.ActiveSteps(false)) != 1 || again.ActiveSteps(false)[0].Def.Cond != "ReachArea" {
		t.Fatal("reload mismatch")
	}
}

func TestNewPlayerStartsStoryAtChosenQuest(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	start := QuestDef{
		ID: StartQuest, Type: 1,
		Nodes: []NodeDef{node(1, 0, "Sequence", "", 2), node(2, 1, "ChildQuest", "ReachArea")},
	}
	engine := NewEngine(catalogOf(start), host, nil)
	engine.Load()
	for _, q := range PreFinished {
		if !engine.IsFinished(q) {
			t.Fatalf("PreFinished %d should be done", q)
		}
	}
	if !engine.IsRunning(StartQuest) {
		t.Fatal("StartQuest should be running")
	}
	if len(engine.ActiveSteps(false)) != 0 {
		t.Fatal("root should not be active before SyncOnJoin")
	}
	engine.SyncOnJoin()
	if len(engine.ActiveSteps(false)) != 1 {
		t.Fatal("expected 1 active step after SyncOnJoin")
	}
	finLists := pushesOf[*protocol.QuestFinishListNotify](host)
	if len(finLists) != 1 || !slices.Equal(finLists[0].QuestId, PreFinished) {
		t.Fatalf("QuestFinishListNotify = %+v", finLists)
	}
	// R-124: second join in same session does not re-send tree; NewSession does.
	trees := len(pushesOf[*protocol.BehaviorTreeInfoNotify](host))
	engine.SyncOnJoin()
	if len(pushesOf[*protocol.BehaviorTreeInfoNotify](host)) != trees {
		t.Fatal("same session SyncOnJoin should not resend tree")
	}
	engine.NewSession()
	engine.SyncOnJoin()
	if len(pushesOf[*protocol.BehaviorTreeInfoNotify](host)) != trees+1 {
		t.Fatal("NewSession SyncOnJoin should resend tree")
	}
}

func TestR117TreeNodesGoToClientOnceReached(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	n93 := node(93, 1, "Sequence", "", 94)
	n93.DungeonID = 2510
	def := QuestDef{
		ID: 114000027, DungeonID: 8,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", "", 33, 72, 93),
			node(33, 1, "ChildQuest", "DoInteract"),
			node(72, 1, "ChildQuest", "ReachArea"),
			n93,
			node(94, 93, "ChildQuest", "DoInteract"),
		},
	}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.ParseState("P 114000027 0:2:0:0:0 1:4:4:0:0 2:0:3:0:0\n")
	info := engine.TreeInfoOf(engine.Runs()[0])
	var keys []int32
	for k := range info.Nodes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if !slices.Equal(keys, []int32{1, 33, 72}) {
		t.Fatalf("reached nodes = %v, want [1 33 72]", keys)
	}
	if info.Nodes[33].GetChildQuestNodeInfo().Status != protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_Finished {
		t.Fatal("node 33 cq status mismatch")
	}
	if info.Nodes[1].GetChildQuestNodeInfo() != nil {
		t.Fatal("composite node should not have ChildQuestNodeInfo")
	}
}

func TestR80dLaterLineReplacesQuestAndXDropsIt(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	def := QuestDef{ID: 7, Nodes: []NodeDef{node(1, 0, "Sequence", ""), node(2, 1, "Condition", ""), node(3, 1, "Condition", "")}}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.ParseState("P 7 0:2:0:0:0 1:2:2:3:0\nF 9\n")
	if engine.Runs()[0].Nodes[1].Status != protocol.NodeStatus_NodeStatus_Activated || engine.Runs()[0].Nodes[1].Count != 3 {
		t.Fatal("initial state mismatch")
	}
	engine.ParseState("P 7 0:2:0:0:1\n")
	if engine.Runs()[0].Nodes[0].Cursor != 1 || engine.Runs()[0].Nodes[1].Status != protocol.NodeStatus_NodeStatus_NotActive || engine.Runs()[0].Nodes[1].Count != 0 {
		t.Fatal("later P line did not replace earlier state")
	}
	if !engine.IsFinished(9) || engine.LinesOnDisk() != 3 {
		t.Fatalf("fin9=%v lines=%d", engine.IsFinished(9), engine.LinesOnDisk())
	}
	engine.ParseState("X 7\n")
	if len(engine.Runs()) != 0 || !engine.IsFinished(9) {
		t.Fatal("X 7 should drop run 7 while keeping F 9")
	}
}

func TestR83QuestVariablesAreSavedAndOldRunsKeepThemUnknown(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	look := &VarRef{Src: "Self", T: "Int", Name: "调查"}
	writer := func(id int32) QuestDef {
		n := node(1, 0, "Sequence", "")
		n.Enter = []QuestAction{{Name: "SetVar", VarOp: &VarOp{Op: "Set", Dst: look, A: constRef(1)}}}
		return QuestDef{ID: id, Nodes: []NodeDef{n}}
	}
	engine := NewEngine(catalogOf(writer(7), writer(8)), host, nil)
	engine.ParseState("P 7 0:2:0:0:0 v\nV\tS7:调查\tn\t2\nV\tG:SuiguangFB_pass\tn\t1\nV\tS7:名字\ts\tCan't Pass\n")
	if !engine.Vars.IsTracked(7) {
		t.Fatal("run 7 should be tracked")
	}
	if val, ok := engine.Vars.Lookup(7, *look); !ok || val.Num != 2 {
		t.Fatalf("S7:调查 = (%+v, %v)", val, ok)
	}
	if val, ok := engine.Vars.Lookup(99, VarRef{Src: "Global", T: "Int", Name: "SuiguangFB_pass"}); !ok || val.Num != 1 {
		t.Fatalf("global = (%+v, %v)", val, ok)
	}
	if val, ok := engine.Vars.Lookup(7, VarRef{Src: "Self", T: "String", Name: "名字"}); !ok || val.Str != "Can't Pass" {
		t.Fatalf("str = (%+v, %v)", val, ok)
	}
	lines := map[string]string{}
	for _, kl := range engine.BuildLines() {
		lines[kl.Key] = kl.Line
	}
	if lines["P 7"] != "P 7 0:2:0:0:0 v" || lines["V\tS7:调查"] != "V\tS7:调查\tn\t2" || lines["V\tS7:名字"] != "V\tS7:名字\ts\tCan't Pass" {
		t.Fatalf("unexpected BuildLines: %v", lines)
	}
	engine.ParseState("W\tS7:调查\n")
	if val, ok := engine.Vars.Lookup(7, *look); !ok || val.Num != 0 {
		t.Fatalf("after W drop = (%+v, %v)", val, ok)
	}
	engine.ParseState("P 8 0:2:0:0:0\n")
	if engine.Vars.IsTracked(8) {
		t.Fatal("run 8 without v should be untracked")
	}
	if _, ok := engine.Vars.Lookup(8, *look); ok {
		t.Fatal("untracked run 8 var should be unknown")
	}
	engine.Vars.Write(LevelPlayRun(5), *look, BtNum(3))
	for _, kl := range engine.BuildLines() {
		if kl.Key == "V\tS4294967301:调查" {
			t.Fatal("level play variable must not be saved")
		}
	}
}

func TestR96SavesAppendWhatChangedAndCompact(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	def := QuestDef{ID: 7, Nodes: []NodeDef{node(1, 0, "Sequence", "")}}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.ParseState("P 7\nF 11\nL 33\nD 22\nV\tG:PassFlag\tn\t1\n")
	engine.Save()
	firstB, err := os.ReadFile(host.QuestsFilePath())
	if err != nil {
		t.Fatal(err)
	}
	first := string(firstB)
	for _, want := range []string{"F 11\n", "L 33\n", "D 22\n", "P 7\n", "V\tG:PassFlag\tn\t1\n"} {
		if !strings.Contains(first, want) {
			t.Fatalf("missing %q in %q", want, first)
		}
	}
	engine.Save()
	againB, _ := os.ReadFile(host.QuestsFilePath())
	if string(againB) != first {
		t.Fatal("unchanged Save should write nothing")
	}
	engine.ParseState("X 7\nW\tG:PassFlag\n")
	engine.Save()
	droppedB, _ := os.ReadFile(host.QuestsFilePath())
	if !strings.HasSuffix(string(droppedB), "X 7\nW\tG:PassFlag\n") {
		t.Fatalf("unexpected drop log: %q", string(droppedB))
	}
	counter := VarRef{Src: "Global", T: "Int", Name: "Count"}
	for i := 0; i < 70; i++ {
		engine.Vars.Write(0, counter, BtNum(float64(i)))
		engine.Save()
	}
	compactedB, _ := os.ReadFile(host.QuestsFilePath())
	compacted := string(compactedB)
	if strings.Contains(compacted, "X 7") {
		t.Fatal("compacted file should drop old X 7")
	}
	if engine.LinesOnDisk() >= 64 || engine.LinesOnDisk() != strings.Count(compacted, "\n") {
		t.Fatalf("LinesOnDisk=%d newlines=%d", engine.LinesOnDisk(), strings.Count(compacted, "\n"))
	}
	reread := NewEngine(catalogOf(def), host, nil)
	reread.Load()
	if !reread.IsFinished(11) || !reread.WorldPlayDone(33) || !reread.DungeonCleared(22) || len(reread.Runs()) != 0 {
		t.Fatal("reread state mismatch")
	}
	if val, ok := reread.Vars.Lookup(0, counter); !ok || val.Num != 69 {
		t.Fatalf("counter = (%+v, %v), want 69", val, ok)
	}
	reread.Save()
	afterB, _ := os.ReadFile(host.QuestsFilePath())
	if string(afterB) != compacted {
		t.Fatal("re-saving after Load should not change file")
	}
}

func TestParallelSelectStopsOtherBranchesAndTimerEnds(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	host.spawned[50] = 1050
	n2 := node(2, 1, "ParallelSelect", "", 3, 4)
	n2.Count = 1
	n3 := node(3, 2, "ChildQuest", "Timer")
	n3.Time = 5
	n4 := node(4, 2, "ChildQuest", "DoInteract")
	n4.Options = []QuestOption{{EntityID: 50, Guid: "g50"}}
	def := QuestDef{
		ID: 20, Type: 2,
		Nodes: []NodeDef{node(1, 0, "Sequence", "", 2, 9), n2, n3, n4, node(9, 1, "QuestSucceed", "")},
	}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.Accept(20)
	if len(engine.ActiveSteps(false)) != 2 {
		t.Fatalf("expected 2 active steps, got %d", len(engine.ActiveSteps(false)))
	}
	engine.Tick()
	if len(engine.ActiveSteps(false)) != 2 {
		t.Fatal("timer not yet due")
	}
	host.nowMs += 5000
	engine.Tick()
	if !engine.IsFinished(20) {
		t.Fatal("quest 20 should finish when timer ends")
	}
	rems := pushesOf[*protocol.EntityRemoveDynamicInteractNotify](host)
	if len(rems) != 1 || rems[0].EntityId != 1050 {
		t.Fatalf("expected losing option removal, got %+v", rems)
	}
	var resetChild bool
	for _, n := range pushesOf[*protocol.UpdateChildQuestNodeStatusNotify](host) {
		if n.NodeId == 4 && n.Status == protocol.ChildQuestNodeStatus_ChildQuestNodeStatus_CQNS_NotActive {
			resetChild = true
		}
	}
	if !resetChild {
		t.Fatal("expected losing child quest status reset to NotActive")
	}
}

func TestKillStepsCountTargetsAndUnimplementedStepsComplete(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	n2 := node(2, 1, "ChildQuest", "Kill")
	n2.Targets = []int64{601, 602}
	def := QuestDef{
		ID: 21, Type: 2,
		Nodes: []NodeDef{
			node(1, 0, "Sequence", "", 2, 3, 4),
			n2,
			node(3, 1, "ChildQuest", "ScheduleTime"),
			node(4, 1, "ChildQuest", "ReachArea"),
		},
	}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.Accept(21)
	if !host.world.IsAwake(601) || !host.world.IsAwake(602) {
		t.Fatal("kill targets should be woken")
	}
	engine.OnKill(601)
	if steps := engine.ActiveSteps(false); len(steps) != 1 || steps[0].Def.Cond != "Kill" {
		t.Fatalf("expected Kill still active, got %+v", steps)
	}
	engine.OnKill(602)
	if steps := engine.ActiveSteps(false); len(steps) != 1 || steps[0].Def.Cond != "ReachArea" {
		t.Fatalf("expected ScheduleTime auto-completed and ReachArea active, got %+v", steps)
	}
}

func TestConditionStepsWaitForVariablesQuestSets(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	flag := selfRef("旗")
	n1 := node(1, 0, "ParallelSelect", "", 2, 3)
	n1.Count = 2
	n2 := node(2, 1, "ChildQuest", "CompareVar")
	n2.Check = &Check{Count: 0, Conds: []Sub{{T: "CompareVar", A: flag, Op: "Eq", B: constRef(1)}}}
	n3 := node(3, 1, "ChildQuest", "ReachArea")
	n3.Finish = []QuestAction{{Name: "SetVar", VarOp: &VarOp{Op: "Set", Dst: flag, A: constRef(1)}}}
	def := QuestDef{ID: 22, Type: 2, Nodes: []NodeDef{n1, n2, n3}}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.Accept(22)
	if len(engine.ActiveSteps(false)) != 2 {
		t.Fatal("both branches should be active")
	}
	engine.SubmitNode(22, 3)
	if !engine.IsFinished(22) {
		t.Fatal("setting flag should complete CompareVar and finish quest 22")
	}
}

func TestR61InteractQuestOfferedAndAcceptedAfterDialogue(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	host.spawned[70] = 1070
	flows := &flowRecorder{}
	side := qdef(30, 2, false, pcond("PreQuest", 5, 0, 0, 0))
	side.Nodes = []NodeDef{node(1, 0, "Sequence", "", 2), node(2, 1, "ChildQuest", "ReachArea")}
	offer := Offer{
		ID: 30, Interact: true,
		Option: &AcceptOption{EntityID: 70, Guid: "acc", TidContent: "接受", Flow: FlowRef{FlowListName: "剧情_接取", FlowID: 1, StateID: 1}},
	}
	engine := NewEngine(catalogWithOffers([]QuestDef{side}, []Offer{offer}), host, flows)
	engine.AcceptUnlocked()
	if len(pushesOf[*protocol.QuestReadyListNotify](host)) != 0 {
		t.Fatal("should not offer before PreQuest 5")
	}
	engine.ParseState("F 5\n")
	engine.AcceptUnlocked()
	ready := pushesOf[*protocol.QuestReadyListNotify](host)
	if len(ready) != 1 || !slices.Equal(ready[0].QuestId, []int32{30}) {
		t.Fatalf("QuestReadyListNotify = %+v", ready)
	}
	adds := pushesOf[*protocol.EntityAddDynamicInteractNotify](host)
	if len(adds) != 1 || adds[0].GameCtx.CtxType != protocol.GameCtxType_GameCtxType_QuestAcceptAction {
		t.Fatalf("accept interact = %+v", adds)
	}
	if got := engine.OnDynamicInteract(70, "acc"); got != DynamicInteractFlow {
		t.Fatalf("OnDynamicInteract = %v, want Flow", got)
	}
	if engine.IsRunning(30) {
		t.Fatal("quest 30 should wait for accept flow end")
	}
	engine.OnFlowEnd(flows.flows[0].FlowIncId)
	if !engine.IsRunning(30) {
		t.Fatal("quest 30 should run after accept flow end")
	}

	// Offers disabled: nothing offered.
	host2 := newFakeQuestHost(t, nil)
	quiet := NewEngine(catalogWithOffers([]QuestDef{side}, []Offer{offer}), host2, nil)
	quiet.OffersEnabled = false
	quiet.ParseState("F 5\n")
	quiet.AcceptUnlocked()
	if len(pushesOf[*protocol.QuestReadyListNotify](host2)) != 0 {
		t.Fatal("OffersEnabled=false should suppress offers")
	}
}

func TestR82aFixFinishesStepWhoseEntityIsGone(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	n2 := node(2, 1, "ChildQuest", "ReachArea")
	n2.Fix = []Fix{{
		When:  "OnProgress",
		Check: allCheck(Sub{T: "CheckEntitesExist", Entities: []int64{109005323}, Exist: false}),
		Then:  []QuestAction{{Name: "FinishCondition"}},
	}}
	def := QuestDef{ID: 23, Type: 2, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2), n2}}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.ParseState("P 23 0:2:0:0:0 1:2:3:0:0 v\n")
	engine.RestorePending = true
	engine.OnPlayerMoved([3]float32{0, 0, 0})
	if !engine.IsFinished(23) || engine.FixStats.Finished != 1 {
		t.Fatalf("fin23=%v FixStats=%+v", engine.IsFinished(23), engine.FixStats)
	}
}

// -----------------------------------------------------------------------------
// Additional unit tests for Go port coverage

func TestHandInItemsGrantsMissingTakesItemsAndCompletes(t *testing.T) {
	host := newFakeQuestHost(t, nil)
	host.spawned[90] = 1090
	host.items[5001] = 1
	n2 := node(2, 1, "ChildQuest", "HandInItems")
	n2.Options = []QuestOption{{EntityID: 90, Guid: "hi"}}
	n2.HandIn = []HandInNeed{{IDs: []int32{5001}, Count: 3}}
	def := QuestDef{ID: 40, Type: 2, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2), n2}}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.Accept(40)
	if host.items[5001] != 3 {
		t.Fatalf("missing hand-in items should be granted to 3, got %d", host.items[5001])
	}
	if got := engine.OnDynamicInteract(90, "hi"); got != DynamicInteractDone {
		t.Fatalf("HandInItems interact = %v, want Done", got)
	}
	code := engine.HandIn(40, 2, []*protocol.PbHandInItem{
		{HandItemInfo: []*protocol.PbHandInItemInfo{{ItemId: 5001, Num: 3}}},
	})
	if code != protocol.ErrorCode_ErrorCode_Success || !engine.IsFinished(40) || host.items[5001] != 0 || engine.HandIns != 1 {
		t.Fatalf("code=%v fin=%v items=%d handIns=%d", code, engine.IsFinished(40), host.items[5001], engine.HandIns)
	}
}

func TestOnUseSkillAndCheckEntityStateAndTags(t *testing.T) {
	tables := &data.DataTables{
		InstanceDungeonById: map[int32]*data.InstanceDungeon{},
		GameplayTagRemapByRawId: map[int32]*data.GameplayTagRemap{
			GameplayTagRawID("关卡.Common.状态.激活"): {RawId: GameplayTagRawID("关卡.Common.状态.激活"), CanonicalId: 999001},
		},
	}
	if GameplayTagID(tables, "关卡.Common.状态.激活") != 999001 {
		t.Fatal("GameplayTagID should apply remap table")
	}
	if GameplayTagID(nil, "关卡.Common.状态.激活") != GameplayTagRawID("关卡.Common.状态.激活") {
		t.Fatal("GameplayTagID without tables should return raw FNV-1 hash")
	}

	host := newFakeQuestHost(t, tables)
	n2 := node(2, 1, "ChildQuest", "UseSkill")
	n2.Skill = &UseSkill{ID: 777, Center: []float64{100, 100, 0}, Radius: 50}
	n3 := node(3, 1, "ChildQuest", "CheckEntityState")
	n3.States = []StateCond{{EntityID: 55, State: "关卡.Common.状态.激活"}}
	def := QuestDef{ID: 41, Type: 2, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2, 3), n2, n3}}
	engine := NewEngine(catalogOf(def), host, nil)
	engine.Accept(41)

	engine.OnUseSkill(777, [3]float32{500, 500, 0}) // outside sphere
	if engine.ActiveSteps(false)[0].Def.Cond != "UseSkill" {
		t.Fatal("UseSkill outside sphere should not complete")
	}
	engine.OnUseSkill(777, [3]float32{110, 100, 0}) // inside sphere
	if engine.ActiveSteps(false)[0].Def.Cond != "CheckEntityState" || !host.world.IsAwake(55) {
		t.Fatal("expected CheckEntityState active and entity 55 woken")
	}
	host.world.SetState(55, 999001)
	engine.OnEntityState(55)
	if !engine.IsFinished(41) {
		t.Fatal("quest 41 should finish once entity 55 enters remapped state")
	}
}

func TestLevelPlaySubTreeAndDungeonStepsAndWorldPlay(t *testing.T) {
	tables := &data.DataTables{
		InstanceDungeonById: map[int32]*data.InstanceDungeon{
			8:    {Id: 8, MapConfigId: 8, InstType: 1},
			1505: {Id: 1505, MapConfigId: 302000, InstType: 2, InstSubType: 2},
		},
		GameplayTagRemapByRawId: map[int32]*data.GameplayTagRemap{},
	}
	host := newFakeQuestHost(t, tables)

	ds := NewQuestDataSet()
	// Main level play 100 waits on sub level play 101.
	lpMainNode := node(2, 1, "ChildQuest", "CheckLevelPlay")
	lpMainNode.LevelPlays = []int32{101}
	lpMainNode.Need = 1
	ds.LevelPlays = []LevelPlayDef{
		{ID: 100, LevelID: 302000, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2, 3), lpMainNode, node(3, 1, "QuestSucceed", "")}},
		{ID: 101, LevelID: 302000, Sub: true, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2, 3), node(2, 1, "ChildQuest", "ReachArea"), node(3, 1, "QuestSucceed", "")}},
	}
	// World play 200 starts when player walks within radius 500 of (1000, 0, 0).
	ds.WorldPlays = []WorldPlay{
		{ID: 200, LevelID: 8, Pos: [3]float32{1000, 0, 0}, Radius: 500, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2), node(2, 1, "ChildQuest", "ReachArea")}},
	}
	// Quest 50 waits on EnterDungeon -> FinishDungeon -> LeaveDungeon for instance 1505.
	nEnter := node(2, 1, "ChildQuest", "EnterDungeon")
	nEnter.Dungeon = 1505
	nFin := node(3, 1, "ChildQuest", "FinishDungeon")
	nFin.Dungeon = 1505
	nFin.Need = 1
	nLeave := node(4, 1, "ChildQuest", "LeaveDungeon")
	ds.Quests = []QuestDef{
		{ID: 50, Type: 1, Nodes: []NodeDef{node(1, 0, "Sequence", "", 2, 3, 4), nEnter, nFin, nLeave}},
	}

	engine := NewEngine(NewCatalog(ds), host, nil)
	engine.Accept(50)
	if engine.ActiveSteps(false)[0].Def.Cond != "EnterDungeon" {
		t.Fatal("expected EnterDungeon waiting")
	}

	// Walk near world play 200 -> starts automatically.
	engine.OnPlayerMoved([3]float32{1100, 0, 0})
	if !engine.IsLevelPlayRunning(200) {
		t.Fatal("world play 200 should start on proximity")
	}
	engine.SubmitNode(LevelPlayRun(200), 2)
	if !engine.WorldPlayDone(200) {
		t.Fatal("world play 200 should be recorded done")
	}

	// Enter dungeon 1505 -> EnterDungeon completes, FinishDungeon activates.
	host.instanceID = 1505
	host.mapID = 302000
	engine.RecheckConditions()
	if engine.ActiveSteps(false)[0].Def.Cond != "FinishDungeon" {
		t.Fatalf("expected FinishDungeon, got %+v", engine.ActiveSteps(false))
	}
	// Start main level play 100 -> automatically starts sub level play 101.
	if !engine.StartLevelPlay(100, LevelPlayVar{Name: "Level", Value: 3}) {
		t.Fatal("StartLevelPlay(100) failed")
	}
	if !engine.IsLevelPlayRunning(101) {
		t.Fatal("sub level play 101 should have started")
	}
	engine.SubmitNode(LevelPlayRun(101), 2)
	if !engine.IsLevelPlayFinished(101) || !engine.IsLevelPlayFinished(100) {
		t.Fatalf("lp101=%v lp100=%v", engine.IsLevelPlayFinished(101), engine.IsLevelPlayFinished(100))
	}

	// Finish dungeon 1505 -> FinishDungeon completes, LeaveDungeon waits until leaving.
	engine.OnDungeonFinished(1505, true)
	if !engine.DungeonCleared(1505) || engine.ActiveSteps(false)[0].Def.Cond != "LeaveDungeon" {
		t.Fatalf("cleared=%v active=%+v", engine.DungeonCleared(1505), engine.ActiveSteps(false))
	}
	host.instanceID = 8
	host.mapID = 8
	engine.StopLevelPlays()
	engine.RecheckConditions()
	if !engine.IsFinished(50) {
		t.Fatal("quest 50 should finish after leaving dungeon")
	}
}

func TestSkipActiveDescribeProgressAndHideActorsAndClientRunEnd(t *testing.T) {
	tables := &data.DataTables{
		InstanceDungeonById:     map[int32]*data.InstanceDungeon{},
		GameplayTagRemapByRawId: map[int32]*data.GameplayTagRemap{},
		BlueprintConfig:         []*data.BlueprintConfig{{BlueprintType: "Npc01", EntityType: "Npc"}},
		LevelEntityConfig:       []*data.LevelEntityConfig{{MapId: 8, EntityId: 400, BlueprintType: "Npc01", InSleep: true}},
	}
	host := newFakeQuestHost(t, tables)
	n2 := node(2, 1, "ChildQuest", "DoInteract")
	n2.Options = []QuestOption{{EntityID: 400, Guid: "g400"}}
	def := QuestDef{
		ID: 60, Type: 2, Name: "测试",
		Nodes: []NodeDef{
			node(1, 0, "ParallelSelect", "", 2, 3),
			n2,
			node(3, 1, "AlwaysFalse", "", 4),
			node(4, 3, "ChildQuest", "ReachArea"),
		},
	}
	def.Nodes[0].Count = 1
	engine := NewEngine(catalogOf(def), host, nil)
	engine.Accept(60)
	desc := engine.DescribeProgress()
	if !strings.Contains(desc, "60 测试:") || !strings.Contains(desc, "[2 DoInteract]") {
		t.Fatalf("unexpected DescribeProgress:\n%s", desc)
	}
	if !host.world.IsAwake(400) {
		t.Fatal("actor 400 should be awake")
	}
	// SkipActive skips node 2, leaves AlwaysFalse branch node 4 alone, and finishes quest 60, putting 400 back to sleep.
	if n := engine.SkipActive(); n != 1 || !engine.IsFinished(60) {
		t.Fatalf("SkipActive=%d fin60=%v", n, engine.IsFinished(60))
	}
	if host.world.IsAwake(400) || !slices.Contains(host.hidden, 400) {
		t.Fatalf("actor 400 should be hidden on finish; awake=%v hidden=%v", host.world.IsAwake(400), host.hidden)
	}

	if ClientActionCount() != 193 {
		t.Fatalf("ClientActionCount = %d, want 193", ClientActionCount())
	}
	acts := []QuestAction{{Name: "Wait"}, {Name: "FadeInScreen"}, {Name: "SetPlayerPos"}, {Name: "FadeOutScreen"}}
	if end := ClientRunEnd(acts, 0); end != 1 {
		t.Fatalf("ClientRunEnd = %d, want 1", end)
	}
}

func TestQuestLogDropOrderAndFinishedListOrderMatchCSharp(t *testing.T) {
	// QuestLog.Save appends dropped keys in .NET Dictionary order (insertion + LIFO slot reuse).
	log := NewQuestLog()
	path := filepath.Join(t.TempDir(), QuestsFileName)
	log.Save(path, []KeyedLine{
		{Key: "P 30", Line: "P 30 0:2:0:0:0"},
		{Key: "V\tG:z", Line: "V\tG:z\tn\t1"},
		{Key: "V\tG:a", Line: "V\tG:a\tn\t2"},
	})
	// Drop "V\tG:z" (slot 1) and add "P 10" (reuses slot 1).
	log.Save(path, []KeyedLine{
		{Key: "P 30", Line: "P 30 0:2:0:0:0"},
		{Key: "V\tG:a", Line: "V\tG:a\tn\t2"},
		{Key: "P 10", Line: "P 10 0:2:0:0:0"},
	})
	// Drop everything: .NET Dictionary slots are [0:"P 30", 1:"P 10", 2:"V\tG:a"].
	log.Save(path, nil)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := "X 30\nX 10\nW\tG:a\n"
	if !strings.HasSuffix(string(b), wantSuffix) {
		t.Fatalf("log = %q, want suffix %q", string(b), wantSuffix)
	}

	// QuestFinishListNotify preserves .NET HashSet<int> insertion order while BuildLines sorts ascending.
	host := newFakeQuestHost(t, nil)
	engine := NewEngine(catalogOf(), host, nil)
	engine.ParseState("F 20\nF 10\nF 30\n")
	engine.SyncOnJoin()
	fin := pushesOf[*protocol.QuestFinishListNotify](host)
	if len(fin) != 1 || !slices.Equal(fin[0].QuestId, []int32{20, 10, 30}) {
		t.Fatalf("QuestFinishListNotify = %v, want [20 10 30]", fin[0].QuestId)
	}
	var buildF []string
	for _, kl := range engine.BuildLines() {
		buildF = append(buildF, kl.Line)
	}
	if !slices.Equal(buildF, []string{"F 10", "F 20", "F 30"}) {
		t.Fatalf("BuildLines = %v, want sorted [F 10 F 20 F 30]", buildF)
	}
}

// -----------------------------------------------------------------------------
// Generated 3.8 quest data smoke test (skipped when not on this machine, e.g. CI)

func TestTheGeneratedQuestDataLoadsIntoTrees(t *testing.T) {
	g := common.GameDataDir()
	dir := ""
	if g != "" {
		dir = filepath.Join(g, "questgen", "assets", "quest")
	}
	if dir == "" || !common.FileExists(filepath.Join(dir, "Quests.json")) {
		t.Skip("3.8 quest data not present on this machine")
	}
	cat := LoadCatalog(dir)
	if len(cat.Data.Quests) != 1930 || len(cat.Quests) != 1930 {
		t.Fatalf("quests data=%d trees=%d, want 1930/1930", len(cat.Data.Quests), len(cat.Quests))
	}
	if len(cat.Data.LevelPlays) != 1152 {
		t.Fatalf("level plays = %d, want 1152", len(cat.Data.LevelPlays))
	}
	if len(cat.Data.WorldPlays) != 185 {
		t.Fatalf("world plays = %d, want 185", len(cat.Data.WorldPlays))
	}
	if len(cat.Offers()) != 444 {
		t.Fatalf("offers = %d, want 444", len(cat.Offers()))
	}
	if len(cat.Data.Owned) != 46454 {
		t.Fatalf("owned = %d, want 46454", len(cat.Data.Owned))
	}
	if len(cat.Data.EntityGlobals) != 361 {
		t.Fatalf("entity globals = %d, want 361", len(cat.Data.EntityGlobals))
	}

	start := cat.Quests[StartQuest]
	if start == nil || start.Node(start.Root).Type != "Sequence" || len(start.Children[start.Root]) == 0 {
		t.Fatal("StartQuest tree mismatch")
	}
	q29 := cat.Quests[139000029]
	if q29 == nil || len(q29.Children[q29.Root]) < 2 ||
		q29.Node(q29.Children[q29.Root][0]).ID != 261 ||
		q29.Node(q29.Children[q29.Root][1]).ID != 262 {
		t.Fatal("139000029 root child order (TreeOrders.json) mismatch")
	}

	var hasSetVar, hasCheck, hasFix bool
	for _, tree := range cat.Quests {
		for _, n := range tree.Def.Nodes {
			for _, a := range slices.Concat(n.Enter, n.Finish, n.Actions) {
				if a.Name == "SetVar" && a.VarOp != nil && a.VarOp.Dst != nil {
					hasSetVar = true
				}
			}
			if n.Check != nil && len(n.Check.Conds) > 0 {
				hasCheck = true
			}
			if len(n.Fix) > 0 {
				hasFix = true
			}
		}
	}
	if !hasSetVar || !hasCheck || !hasFix {
		t.Fatalf("parsed flags: setVar=%v check=%v fix=%v", hasSetVar, hasCheck, hasFix)
	}

	host := newFakeQuestHost(t, nil)
	engine := NewEngine(cat, host, nil)
	engine.Load()
	engine.SyncOnJoin()
	if len(engine.ActiveSteps(false)) == 0 {
		t.Fatal("fresh player on 3.8 data should have at least one active step")
	}
}
