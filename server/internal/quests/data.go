// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"tidelab/internal/common"
	"tidelab/internal/data"
)

// The quest data our generators write (the lab's scripts/dev/gen_quest_data.py,
// gen_levelplay_data.py, gen_quest_offers.py, fb_vars.py, gen_entity_globals.py) into
// assets/quest/*.json, read as the quest engine needs it. Ported from QuestData.cs.

// FlowRef is a flow to play: FlowListName, FlowId, StateId.
type FlowRef struct {
	FlowListName string
	FlowID       int32
	StateID      int32
}

func (f FlowRef) IsEmpty() bool { return f.FlowListName == "" }

// ActionParams holds the typed params the generators keep for the actions the server itself runs.
type ActionParams struct {
	IsSelf       *bool
	Location     *int32
	EntityState  *string
	EntityID     *int64
	State        *string
	EntityIDs    []int64
	HasEntityIDs bool
	Flow         *FlowRef
	DungeonID    *int32
	PosEntityID  *int64
	PrefabID     *int32
	Var          *string
	IsSuccess    *bool
}

// QuestAction is one action of a node's Enter, Finish or Actions list, a fix's Then list or an
// option's Actions. Raw is the action's whole Params as the game has them (R-33a).
type QuestAction struct {
	Name   string
	VarOp  *VarOp
	Params *ActionParams
	Raw    data.Value
}

// QuestOption is a quest option the server adds to an NPC (DoInteract, HandInItems).
type QuestOption struct {
	EntityID   int64
	Guid       string
	TidContent string
	Flow       *FlowRef
	Actions    []QuestAction
}

type StateCond struct {
	EntityID int64
	State    string
}

// BattleNeed is CheckTargetBattleAttribute (R-76i): monsters whose health must be between Min and Max percent.
type BattleNeed struct {
	IDs []int64
	Min int32
	Max int32
}

// HandInNeed is HandInItems (R-76g): a group of item ids and the count to hand in.
type HandInNeed struct {
	IDs   []int32
	Count int32
}

// UseSkill is judged by skill id (R-14): the skill, and the sphere the player must stand in.
type UseSkill struct {
	ID     int64
	Center []float64
	Radius float64
}

// Slot is a ConditionSelector (R-37) branch: its child node and the check that picks it.
type Slot struct {
	Node  int32
	Check Check
}

// Fix is a FixProcessor entry (R-82a): when Check holds (unknown sub-conditions count as not met),
// run Then. When: OnActivated, OnStartAction, OnFinishAction, or OnProgress / Default.
type Fix struct {
	When   string
	Period float64
	Check  Check
	Then   []QuestAction
}

type NodeDef struct {
	ID           int32
	Parent       int32
	Type         string
	Count        int32
	Cond         string
	RewardID     int32
	DungeonID    int32
	Enter        []QuestAction
	Finish       []QuestAction
	Actions      []QuestAction
	Options      []QuestOption
	HandIn       []HandInNeed
	Battle       []BattleNeed
	BattleAny    bool
	Targets      []int64
	AwakeTargets []int64
	States       []StateCond
	Flow         *FlowRef
	Time         float64
	Var          string
	Creators     []int64
	Bundle       int32
	BundlePos    int64
	Check        *Check
	LevelPlays   []int32
	Need         int32
	Dungeon      int32
	SubType      int32
	Kids         []int32
	Skill        *UseSkill
	Slots        []Slot
	FailTime     float64
	Fix          []Fix
}

func (n NodeDef) Is(typ string) bool { return n.Type == typ }

// ProvideCond is one unlock condition (QuestData ProvideType, R-10).
type ProvideCond struct {
	Type    string
	Quest   int32
	Node    int32
	Level   int32
	Play    int32
	Dungeon int32
}

// Provide holds unlock conditions: Any=true means one condition suffices; else all.
type Provide struct {
	Any   bool
	Conds []ProvideCond
}

type QuestDef struct {
	ID        int32
	Type      int32
	Name      string
	PreQuest  int32
	RewardID  int32
	DungeonID int32
	Nodes     []NodeDef
	Provide   Provide
}

// LevelPlayDef is a dungeon level play (R-4b).
type LevelPlayDef struct {
	ID       int32
	Name     string
	LevelID  int32
	Monsters []int32
	Nodes    []NodeDef
	Sub      bool
}

// WorldPlay is an open-world play some quest waits for (R-62, PreLevelPlay).
type WorldPlay struct {
	ID      int32
	Name    string
	LevelID int32
	Nodes   []NodeDef
	Sub     bool
	Pos     [3]float32
	Radius  float32
	Open    Provide
}

// AcceptOption is the accept option of an Interact side quest (R-61).
type AcceptOption struct {
	EntityID   int64
	Guid       string
	TidContent string
	Flow       FlowRef
}

// Offer describes how a side quest is given (R-61). Items are [item, count] pairs (R-62).
type Offer struct {
	ID       int32
	Interact bool
	Option   *AcceptOption
	Items    [][]int32
}

// FlowActionDef is a server action inside a flow state (FlowActions.json).
type FlowActionDef struct {
	ActionID int32
	Pos      []float32 // x, y, z in cm, yaw (len >= 4 when present)
	Action   QuestAction
}

// TreeVar is a declared initial value of a tree variable (TreeVars.json).
type TreeVar struct {
	Name  string
	Value BtValue
}

// EntityPair is a (Map, Entity) pair used by QuestOwned.json and HideGroups.json.
type EntityPair struct {
	Map    int32
	Entity int64
}

// QuestDataSet holds the quest engine's data files, loaded once per process.
type QuestDataSet struct {
	Quests        []QuestDef
	LevelPlays    []LevelPlayDef
	WorldPlays    []WorldPlay
	Offers        map[int32]Offer
	OfferOrder    []int32
	QuestVars     map[int32][]TreeVar
	LevelPlayVars map[int32][]TreeVar
	EntityGlobals []string
	Owned         map[EntityPair]struct{}
	FlowActions   map[string][]FlowActionDef
	DungeonPlays  map[int32]int32
	Prefabs       map[int32]PrefabDef
	PrefabOrder   []int32
	TowerFloors   []FloorPrefab
	HideGroups    map[string]HideGroupDef
}

func NewQuestDataSet() *QuestDataSet {
	return &QuestDataSet{
		Offers:        map[int32]Offer{},
		QuestVars:     map[int32][]TreeVar{},
		LevelPlayVars: map[int32][]TreeVar{},
		Owned:         map[EntityPair]struct{}{},
		FlowActions:   map[string][]FlowActionDef{},
		DungeonPlays:  map[int32]int32{},
		Prefabs:       map[int32]PrefabDef{},
		HideGroups:    map[string]HideGroupDef{},
	}
}

// LoadDataSet reads the files of dir (assets/quest). A missing or unreadable file is logged and
// left empty: the server runs without that part.
func LoadDataSet(dir string) *QuestDataSet {
	d := NewQuestDataSet()
	readQuestFile(dir, "Quests.json", "gen_quest_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			d.Quests = append(d.Quests, ParseQuest(it))
		}
	})
	readQuestFile(dir, "LevelPlays.json", "gen_levelplay_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			d.LevelPlays = append(d.LevelPlays, ParseLevelPlay(it))
		}
	})
	readQuestFile(dir, "WorldPlays.json", "gen_levelplay_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			d.WorldPlays = append(d.WorldPlays, ParseWorldPlay(it))
		}
	})
	readQuestFile(dir, "QuestOffers.json", "gen_quest_offers.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			o := ParseOffer(it)
			if _, exists := d.Offers[o.ID]; !exists {
				d.OfferOrder = append(d.OfferOrder, o.ID)
			}
			d.Offers[o.ID] = o
		}
	})
	readQuestFile(dir, "TreeVars.json", "fb_vars.py", func(v data.Value) {
		ParseTreeVars(v, "Quests", d.QuestVars)
		ParseTreeVars(v, "LevelPlays", d.LevelPlayVars)
	})
	readQuestFile(dir, "EntityGlobals.json", "gen_entity_globals.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			if s, ok := it.(data.String); ok {
				d.EntityGlobals = append(d.EntityGlobals, string(s))
			}
		}
	})
	readQuestFile(dir, "QuestOwned.json", "gen_quest_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			if arr, ok := it.(*data.Array); ok && len(arr.Items) >= 2 {
				d.Owned[EntityPair{Map: int32(JSONLong(arr.Items[0], 0)), Entity: JSONLong(arr.Items[1], 0)}] = struct{}{}
			}
		}
	})
	readQuestFile(dir, "FlowActions.json", "gen_flow_actions.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			if st, ok := it.(*data.Object); ok {
				rawActions := JSONArr(st.Get("Actions"))
				actions := make([]FlowActionDef, len(rawActions))
				for i, a := range rawActions {
					actions[i] = ParseFlowAction(a)
				}
				d.FlowActions[JSONStr(st.Get("Key"), "")] = actions
			}
		}
	})
	readQuestFile(dir, "DungeonPlays.json", "gen_levelplay_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			if arr, ok := it.(*data.Array); ok && len(arr.Items) >= 2 {
				d.DungeonPlays[JSONInt(arr.Items[0], 0)] = JSONInt(arr.Items[1], 0)
			}
		}
	})
	readQuestFile(dir, "Prefabs.json", "gen_levelplay_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			p, ok := it.(*data.Object)
			if !ok {
				continue
			}
			var entities []PrefabEntity
			for _, ev := range JSONArr(p.Get("Entities")) {
				e, ok := ev.(*data.Object)
				if !ok {
					continue
				}
				entities = append(entities, PrefabEntity{
					ID:            JSONInt(e.Get("Id"), 0),
					BlueprintType: JSONStr(e.Get("BlueprintType"), ""),
					Name:          JSONStr(e.Get("Name"), ""),
					Pos:           jsonVec3(e.Get("Pos")),
					Rot:           jsonVec3(e.Get("Rot")),
					Tower:         JSONBool(e.Get("Tower"), false),
					PropertyID:    JSONInt(e.Get("PropertyId"), 0),
				})
			}
			rawWaves := JSONArr(p.Get("Waves"))
			waves := make([][]int32, len(rawWaves))
			for i, w := range rawWaves {
				waves[i] = JSONInts(w)
			}
			def := PrefabDef{
				ID:       JSONInt(p.Get("Id"), 0),
				Entities: entities,
				Waves:    waves,
			}
			if _, exists := d.Prefabs[def.ID]; !exists {
				d.PrefabOrder = append(d.PrefabOrder, def.ID)
			}
			d.Prefabs[def.ID] = def
		}
	})
	readQuestFile(dir, "TowerFloors.json", "gen_levelplay_data.py", func(v data.Value) {
		for _, it := range jsonItems(v) {
			if f, ok := it.(*data.Object); ok {
				d.TowerFloors = append(d.TowerFloors, FloorPrefab{
					Floor:  JSONInt(f.Get("Floor"), 0),
					Prefab: JSONInt(f.Get("Prefab"), 0),
					Play:   JSONInt(f.Get("Play"), 0),
				})
			}
		}
	})
	readQuestFile(dir, "HideGroups.json", "gen_hide_groups.py", func(v data.Value) {
		pairs := func(list data.Value) []EntityPair {
			var out []EntityPair
			for _, it := range JSONArr(list) {
				if p, ok := it.(*data.Array); ok && len(p.Items) >= 2 {
					out = append(out, EntityPair{Map: int32(JSONLong(p.Items[0], 0)), Entity: JSONLong(p.Items[1], 0)})
				}
			}
			return out
		}
		for _, it := range jsonItems(v) {
			if g, ok := it.(*data.Object); ok {
				key := JSONStr(g.Get("Key"), "")
				d.HideGroups[key] = HideGroupDef{
					Key:   key,
					IDs:   pairs(g.Get("Ids")),
					Force: pairs(g.Get("Force")),
				}
			}
		}
	})
	common.Info("quest", fmt.Sprintf("quest data: %d quests, %d level plays, %d world plays, %d offers, %d quest-owned entities",
		len(d.Quests), len(d.LevelPlays), len(d.WorldPlays), len(d.Offers), len(d.Owned)))
	return d
}

func jsonItems(v data.Value) []data.Value {
	if arr, ok := v.(*data.Array); ok && arr != nil {
		return arr.Items
	}
	return nil
}

func readQuestFile(dir, file, generator string, use func(data.Value)) {
	path := filepath.Join(dir, file)
	v, err := ParseJSONFile(path)
	if err != nil {
		common.Error("quest", fmt.Sprintf("loading %s failed: %s (run the lab's scripts/dev/%s)", path, err, generator))
		return
	}
	use(v)
}

func ParseJSONFile(path string) (data.Value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data.ParseJSON(b)
}

// JSON helpers matching QuestJson in QuestData.cs: wrong types read as defaults.

func JSONLong(v data.Value, def int64) int64 {
	if n, ok := v.(data.Number); ok {
		return int64(n)
	}
	return def
}

func JSONInt(v data.Value, def int32) int32 {
	if n, ok := v.(data.Number); ok {
		return int32(n)
	}
	return def
}

func JSONDouble(v data.Value, def float64) float64 {
	if n, ok := v.(data.Number); ok {
		return float64(n)
	}
	return def
}

func JSONStr(v data.Value, def string) string {
	if s, ok := v.(data.String); ok {
		return string(s)
	}
	return def
}

func JSONBool(v data.Value, def bool) bool {
	if b, ok := v.(data.Bool); ok {
		return bool(b)
	}
	return def
}

func JSONArr(v data.Value) []data.Value {
	if arr, ok := v.(*data.Array); ok && arr != nil {
		return arr.Items
	}
	return nil
}

func JSONLongs(v data.Value) []int64 {
	items := JSONArr(v)
	if len(items) == 0 {
		return nil
	}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		if n, ok := it.(data.Number); ok {
			out = append(out, int64(n))
		}
	}
	return out
}

func JSONInts(v data.Value) []int32 {
	items := JSONArr(v)
	if len(items) == 0 {
		return nil
	}
	out := make([]int32, 0, len(items))
	for _, it := range items {
		if n, ok := it.(data.Number); ok {
			out = append(out, int32(n))
		}
	}
	return out
}

func JSONFloats(v data.Value) []float32 {
	items := JSONArr(v)
	if len(items) == 0 {
		return nil
	}
	out := make([]float32, 0, len(items))
	for _, it := range items {
		if n, ok := it.(data.Number); ok {
			out = append(out, float32(n))
		}
	}
	return out
}

func jsonVec3(v data.Value) [3]float32 {
	f := JSONFloats(v)
	var out [3]float32
	copy(out[:], f)
	return out
}

func optLong(v data.Value) *int64 {
	if n, ok := v.(data.Number); ok {
		x := int64(n)
		return &x
	}
	return nil
}

func optInt(v data.Value) *int32 {
	if n, ok := v.(data.Number); ok {
		x := int32(n)
		return &x
	}
	return nil
}

func optBool(v data.Value) *bool {
	if b, ok := v.(data.Bool); ok {
		x := bool(b)
		return &x
	}
	return nil
}

func optStr(v data.Value) *string {
	if s, ok := v.(data.String); ok {
		x := string(s)
		return &x
	}
	return nil
}

func ParseFlowRef(v data.Value) *FlowRef {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return nil
	}
	return &FlowRef{
		FlowListName: JSONStr(o.Get("FlowListName"), ""),
		FlowID:       JSONInt(o.Get("FlowId"), 0),
		StateID:      JSONInt(o.Get("StateId"), 0),
	}
}

func ParseVarRef(v data.Value) *VarRef {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return nil
	}
	return &VarRef{
		Src:  JSONStr(o.Get("Src"), ""),
		T:    JSONStr(o.Get("T"), ""),
		Name: JSONStr(o.Get("Name"), ""),
		Ref:  JSONLong(o.Get("Ref"), 0),
		Num:  JSONDouble(o.Get("Num"), 0),
		Str:  JSONStr(o.Get("Str"), ""),
	}
}

func ParseVarOp(v data.Value) *VarOp {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return nil
	}
	return &VarOp{
		Op:  JSONStr(o.Get("Op"), ""),
		Dst: ParseVarRef(o.Get("Dst")),
		A:   ParseVarRef(o.Get("A")),
		B:   ParseVarRef(o.Get("B")),
	}
}

func ParseSub(v data.Value) Sub {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	rawItems := JSONArr(o.Get("Items"))
	var items [][]int32
	if len(rawItems) > 0 {
		items = make([][]int32, len(rawItems))
		for i, it := range rawItems {
			items[i] = JSONInts(it)
		}
	}
	return Sub{
		T:         JSONStr(o.Get("T"), ""),
		A:         ParseVarRef(o.Get("A")),
		B:         ParseVarRef(o.Get("B")),
		Op:        JSONStr(o.Get("Op"), "Eq"),
		Entity:    JSONLong(o.Get("Entity"), 0),
		State:     JSONStr(o.Get("State"), ""),
		Num:       JSONInt(o.Get("Num"), 0),
		Entities:  JSONLongs(o.Get("Entities")),
		Exist:     JSONBool(o.Get("Exist"), true),
		Quest:     JSONInt(o.Get("Quest"), 0),
		Node:      JSONInt(o.Get("Node"), 0),
		LevelPlay: JSONInt(o.Get("LevelPlay"), 0),
		Dungeon:   JSONInt(o.Get("Dungeon"), 0),
		Gender:    JSONStr(o.Get("Gender"), ""),
		InRange:   JSONBool(o.Get("InRange"), true),
		Shape:     byte(JSONInt(o.Get("Shape"), 0)),
		Center:    jsonVec3(o.Get("Center")),
		Ext:       jsonVec3(o.Get("Ext")),
		Yaw:       float32(JSONDouble(o.Get("Yaw"), 0)),
		Items:     items,
	}
}

func ParseCheck(v data.Value) *Check {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return nil
	}
	rawConds := JSONArr(o.Get("Conds"))
	conds := make([]Sub, len(rawConds))
	for i, c := range rawConds {
		conds[i] = ParseSub(c)
	}
	return &Check{Count: JSONInt(o.Get("Count"), 0), Conds: conds}
}

func ParseAction(v data.Value) QuestAction {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	var p *ActionParams
	if po, ok := o.Get("Params").(*data.Object); ok && po != nil {
		_, hasIDs := po.Get("EntityIds").(*data.Array)
		p = &ActionParams{
			IsSelf:       optBool(po.Get("IsSelf")),
			Location:     optInt(po.Get("Location")),
			EntityState:  optStr(po.Get("EntityState")),
			EntityID:     optLong(po.Get("EntityId")),
			State:        optStr(po.Get("State")),
			EntityIDs:    JSONLongs(po.Get("EntityIds")),
			HasEntityIDs: hasIDs,
			Flow:         ParseFlowRef(po.Get("Flow")),
			DungeonID:    optInt(po.Get("DungeonId")),
			PosEntityID:  optLong(po.Get("PosEntityId")),
			PrefabID:     optInt(po.Get("PrefabId")),
			Var:          optStr(po.Get("Var")),
			IsSuccess:    optBool(po.Get("IsSuccess")),
		}
	}
	return QuestAction{
		Name:   JSONStr(o.Get("Name"), ""),
		VarOp:  ParseVarOp(o.Get("VarOp")),
		Params: p,
		Raw:    o.Get("Raw"),
	}
}

func ParseActions(v data.Value) []QuestAction {
	items := JSONArr(v)
	if len(items) == 0 {
		return nil
	}
	out := make([]QuestAction, len(items))
	for i, it := range items {
		out[i] = ParseAction(it)
	}
	return out
}

func ParseFlowAction(v data.Value) FlowActionDef {
	o, _ := v.(*data.Object)
	var pos []float32
	if o != nil {
		if arr, ok := o.Get("Pos").(*data.Array); ok && len(arr.Items) >= 4 {
			pos = JSONFloats(arr)
		}
	}
	var id int32
	if o != nil {
		id = JSONInt(o.Get("ActionId"), 0)
	}
	return FlowActionDef{ActionID: id, Pos: pos, Action: ParseAction(v)}
}

func ParseOption(v data.Value) QuestOption {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	return QuestOption{
		EntityID:   JSONLong(o.Get("EntityId"), 0),
		Guid:       JSONStr(o.Get("Guid"), ""),
		TidContent: JSONStr(o.Get("TidContent"), ""),
		Flow:       ParseFlowRef(o.Get("Flow")),
		Actions:    ParseActions(o.Get("Actions")),
	}
}

func ParseNode(v data.Value) NodeDef {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	rawOpts := JSONArr(o.Get("Options"))
	var opts []QuestOption
	if len(rawOpts) > 0 {
		opts = make([]QuestOption, len(rawOpts))
		for i, x := range rawOpts {
			opts[i] = ParseOption(x)
		}
	}
	var handIn []HandInNeed
	for _, x := range JSONArr(o.Get("HandIn")) {
		if ho, ok := x.(*data.Object); ok {
			handIn = append(handIn, HandInNeed{IDs: JSONInts(ho.Get("Ids")), Count: JSONInt(ho.Get("Count"), 1)})
		}
	}
	var battle []BattleNeed
	for _, x := range JSONArr(o.Get("Battle")) {
		if bo, ok := x.(*data.Object); ok {
			battle = append(battle, BattleNeed{IDs: JSONLongs(bo.Get("Ids")), Min: JSONInt(bo.Get("Min"), 0), Max: JSONInt(bo.Get("Max"), 100)})
		}
	}
	var states []StateCond
	for _, x := range JSONArr(o.Get("States")) {
		if so, ok := x.(*data.Object); ok {
			states = append(states, StateCond{EntityID: JSONLong(so.Get("EntityId"), 0), State: JSONStr(so.Get("State"), "")})
		}
	}
	var skill *UseSkill
	if so, ok := o.Get("Skill").(*data.Object); ok && so != nil {
		var center []float64
		if arr, ok := so.Get("Center").(*data.Array); ok {
			center = make([]float64, len(arr.Items))
			for i, it := range arr.Items {
				center[i] = JSONDouble(it, 0)
			}
		}
		skill = &UseSkill{
			ID:     JSONLong(so.Get("Id"), 0),
			Center: center,
			Radius: JSONDouble(so.Get("Radius"), 0),
		}
	}
	var slots []Slot
	for _, x := range JSONArr(o.Get("Slots")) {
		if so, ok := x.(*data.Object); ok {
			c := Check{}
			if pc := ParseCheck(so.Get("Check")); pc != nil {
				c = *pc
			}
			slots = append(slots, Slot{Node: JSONInt(so.Get("Node"), 0), Check: c})
		}
	}
	var fixes []Fix
	for _, x := range JSONArr(o.Get("Fix")) {
		if fo, ok := x.(*data.Object); ok {
			c := Check{}
			if pc := ParseCheck(fo.Get("Check")); pc != nil {
				c = *pc
			}
			fixes = append(fixes, Fix{
				When:   JSONStr(fo.Get("When"), ""),
				Period: JSONDouble(fo.Get("Period"), 0),
				Check:  c,
				Then:   ParseActions(fo.Get("Then")),
			})
		}
	}
	return NodeDef{
		ID:           JSONInt(o.Get("Id"), 0),
		Parent:       JSONInt(o.Get("Parent"), 0),
		Type:         JSONStr(o.Get("Type"), ""),
		Count:        JSONInt(o.Get("Count"), 0),
		Cond:         JSONStr(o.Get("Cond"), ""),
		RewardID:     JSONInt(o.Get("RewardId"), 0),
		DungeonID:    JSONInt(o.Get("DungeonId"), 0),
		Enter:        ParseActions(o.Get("Enter")),
		Finish:       ParseActions(o.Get("Finish")),
		Actions:      ParseActions(o.Get("Actions")),
		Options:      opts,
		HandIn:       handIn,
		Battle:       battle,
		BattleAny:    JSONBool(o.Get("BattleAny"), false),
		Targets:      JSONLongs(o.Get("Targets")),
		AwakeTargets: JSONLongs(o.Get("AwakeTargets")),
		States:       states,
		Flow:         ParseFlowRef(o.Get("Flow")),
		Time:         JSONDouble(o.Get("Time"), 0),
		Var:          JSONStr(o.Get("Var"), ""),
		Creators:     JSONLongs(o.Get("Creators")),
		Bundle:       JSONInt(o.Get("Bundle"), 0),
		BundlePos:    JSONLong(o.Get("BundlePos"), 0),
		Check:        ParseCheck(o.Get("Check")),
		LevelPlays:   JSONInts(o.Get("LevelPlays")),
		Need:         JSONInt(o.Get("Need"), 0),
		Dungeon:      JSONInt(o.Get("Dungeon"), 0),
		SubType:      JSONInt(o.Get("SubType"), 0),
		Kids:         JSONInts(o.Get("Kids")),
		Skill:        skill,
		Slots:        slots,
		FailTime:     JSONDouble(o.Get("FailTime"), 0),
		Fix:          fixes,
	}
}

func ParseNodes(v data.Value) []NodeDef {
	items := JSONArr(v)
	if len(items) == 0 {
		return nil
	}
	out := make([]NodeDef, len(items))
	for i, it := range items {
		out[i] = ParseNode(it)
	}
	return out
}

func ParseProvide(v data.Value) Provide {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return Provide{}
	}
	var conds []ProvideCond
	for _, x := range JSONArr(o.Get("Conds")) {
		if co, ok := x.(*data.Object); ok {
			conds = append(conds, ProvideCond{
				Type:    JSONStr(co.Get("Type"), ""),
				Quest:   JSONInt(co.Get("Quest"), 0),
				Node:    JSONInt(co.Get("Node"), 0),
				Level:   JSONInt(co.Get("Level"), 0),
				Play:    JSONInt(co.Get("Play"), 0),
				Dungeon: JSONInt(co.Get("Dungeon"), 0),
			})
		}
	}
	return Provide{Any: JSONBool(o.Get("Any"), false), Conds: conds}
}

func ParseQuest(v data.Value) QuestDef {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	return QuestDef{
		ID:        JSONInt(o.Get("Id"), 0),
		Type:      JSONInt(o.Get("Type"), 0),
		Name:      JSONStr(o.Get("Name"), ""),
		PreQuest:  JSONInt(o.Get("PreQuest"), 0),
		RewardID:  JSONInt(o.Get("RewardId"), 0),
		DungeonID: JSONInt(o.Get("DungeonId"), 0),
		Nodes:     ParseNodes(o.Get("Nodes")),
		Provide:   ParseProvide(o.Get("Provide")),
	}
}

func ParseLevelPlay(v data.Value) LevelPlayDef {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	return LevelPlayDef{
		ID:       JSONInt(o.Get("Id"), 0),
		Name:     JSONStr(o.Get("Name"), ""),
		LevelID:  JSONInt(o.Get("LevelId"), 0),
		Monsters: JSONInts(o.Get("Monsters")),
		Nodes:    ParseNodes(o.Get("Nodes")),
		Sub:      JSONBool(o.Get("Sub"), false),
	}
}

func ParseWorldPlay(v data.Value) WorldPlay {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	return WorldPlay{
		ID:      JSONInt(o.Get("Id"), 0),
		Name:    JSONStr(o.Get("Name"), ""),
		LevelID: JSONInt(o.Get("LevelId"), 0),
		Nodes:   ParseNodes(o.Get("Nodes")),
		Sub:     JSONBool(o.Get("Sub"), false),
		Pos:     jsonVec3(o.Get("Pos")),
		Radius:  float32(JSONDouble(o.Get("Radius"), 0)),
		Open:    ParseProvide(o.Get("Open")),
	}
}

func ParseOffer(v data.Value) Offer {
	o, _ := v.(*data.Object)
	if o == nil {
		o = &data.Object{}
	}
	var opt *AcceptOption
	if a, ok := o.Get("Option").(*data.Object); ok && a != nil {
		flow := FlowRef{}
		if f := ParseFlowRef(a.Get("Flow")); f != nil {
			flow = *f
		}
		opt = &AcceptOption{
			EntityID:   JSONLong(a.Get("EntityId"), 0),
			Guid:       JSONStr(a.Get("Guid"), ""),
			TidContent: JSONStr(a.Get("TidContent"), ""),
			Flow:       flow,
		}
	}
	rawItems := JSONArr(o.Get("Items"))
	var items [][]int32
	if len(rawItems) > 0 {
		items = make([][]int32, len(rawItems))
		for i, it := range rawItems {
			items[i] = JSONInts(it)
		}
	}
	return Offer{
		ID:       JSONInt(o.Get("Id"), 0),
		Interact: JSONBool(o.Get("Interact"), false),
		Option:   opt,
		Items:    items,
	}
}

func ParseTreeVars(v data.Value, group string, into map[int32][]TreeVar) {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return
	}
	g, ok := o.Get(group).(*data.Object)
	if !ok || g == nil {
		return
	}
	for i, key := range g.Keys {
		id, err := strconv.ParseInt(key, 10, 32)
		if err != nil {
			continue
		}
		var list []TreeVar
		for _, it := range JSONArr(g.Values[i]) {
			to, ok := it.(*data.Object)
			if !ok {
				continue
			}
			val := BtNum(JSONDouble(to.Get("Num"), 0))
			if s, ok := to.Get("Str").(data.String); ok {
				val = BtString(string(s))
			}
			list = append(list, TreeVar{Name: JSONStr(to.Get("Name"), ""), Value: val})
		}
		into[int32(id)] = list
	}
}
