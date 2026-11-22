// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/protocol"
)

// RoadDungeon is Lahai-Roi, whose roads are all built (specs/enter-world.md §4).
const RoadDungeon = 906

// PassiveSkillContextID is the context id a passive skill is given (R-122): the client only
// checks it is not 0.
const PassiveSkillContextID = -1

// WorldData is what the world logic looks up in the config tables, indexed once per load.
// It is shared by every session; the lazily built parts are guarded.
type WorldData struct {
	Tables *data.DataTables
	// JSONDirs are the directories the game's JSON tables are in (the BinData, then the further
	// ones), for the files read when first needed (specs/data-tables.md §5).
	JSONDirs []string
	// VehicleTemplateID is the motorcycle's template (specs/entities.md §4.1), nil when the data has none.
	VehicleTemplateID *int32
	// Panel is R-160's content panel overrides (nil: none, the automatic rules; specs/panel.md).
	Panel *panel.Overrides

	blueprintByType map[string]*data.BlueprintConfig
	templateByType  map[string]*data.TemplateConfig
	layersOfMap     map[int32][]int32
	roadsOfMap      map[int32][]int32

	mu              sync.Mutex
	jsonFiles       map[string]data.Value
	templateByID    map[int32]*data.TemplateConfig
	skillsOfActor   map[string][]int32
	repeatBoss      map[int32]bool
	floorSeconds    map[int32]int64
	doorTemplate    map[string]bool
	candidates      map[int32][]*Candidate
	questCandidates map[questKey][]*Candidate
}

type questKey struct {
	instance        int32
	entered, offers bool
}

func NewWorldData(tables *data.DataTables, roadsOfMap map[int32][]int32) *WorldData {
	d := &WorldData{
		Tables:          tables,
		blueprintByType: map[string]*data.BlueprintConfig{},
		templateByType:  map[string]*data.TemplateConfig{},
		layersOfMap:     map[int32][]int32{},
		roadsOfMap:      map[int32][]int32{},
		jsonFiles:       map[string]data.Value{},
		repeatBoss:      map[int32]bool{},
		floorSeconds:    map[int32]int64{},
		doorTemplate:    map[string]bool{},
		candidates:      map[int32][]*Candidate{},
		questCandidates: map[questKey][]*Candidate{},
	}
	for _, b := range tables.BlueprintConfig {
		if _, ok := d.blueprintByType[b.BlueprintType]; !ok {
			d.blueprintByType[b.BlueprintType] = b
		}
	}
	for _, t := range tables.TemplateConfig {
		if _, ok := d.templateByType[t.BlueprintType]; !ok {
			d.templateByType[t.BlueprintType] = t
		}
	}
	for _, l := range tables.DataLayerConfig {
		if l.InitLoad && !slices.Contains(d.layersOfMap[l.LevelId], l.Id) {
			d.layersOfMap[l.LevelId] = append(d.layersOfMap[l.LevelId], l.Id)
		}
	}
	for k, v := range roadsOfMap {
		d.roadsOfMap[k] = v
	}
	d.VehicleTemplateID = d.findVehicleTemplate()
	return d
}

func (d *WorldData) Blueprint(blueprintType string) *data.BlueprintConfig {
	return d.blueprintByType[blueprintType]
}

func (d *WorldData) Template(blueprintType string) *data.TemplateConfig {
	return d.templateByType[blueprintType]
}

// ReadJSON is a table read outside the loader, from the first of JSONDirs that has it; nil
// when none has it or it cannot be read.
func (d *WorldData) ReadJSON(file string) data.Value {
	d.mu.Lock()
	defer d.mu.Unlock()
	if v, ok := d.jsonFiles[file]; ok {
		return v
	}
	var v data.Value
	for _, dir := range d.JSONDirs {
		path := filepath.Join(dir, file)
		if !common.FileExists(path) {
			continue
		}
		b, err := os.ReadFile(path)
		if err == nil {
			v, err = data.ParseJSON(b)
		}
		if err != nil {
			common.Warn("data", "reading "+path+" failed: "+err.Error())
			v = nil
		}
		break
	}
	d.jsonFiles[file] = v
	return v
}

// TemplateByID is a TemplateConfig row by its Id (R-56 wave monsters).
func (d *WorldData) TemplateByID(id int32) *data.TemplateConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.templateByID == nil {
		d.templateByID = map[int32]*data.TemplateConfig{}
		for _, t := range d.Tables.TemplateConfig {
			if _, ok := d.templateByID[t.Id]; !ok {
				d.templateByID[t.Id] = t
			}
		}
	}
	return d.templateByID[id]
}

// MapOf is an instance's map (InstanceDungeon.MapConfigId); the id itself when the table has no row.
func (d *WorldData) MapOf(instanceID int32) int32 {
	if r, ok := d.Tables.InstanceDungeonById[instanceID]; ok {
		return r.MapConfigId
	}
	return instanceID
}

// DataLayers are the data layers the client loads at a scene join (specs/enter-world.md §4.1):
// every InitLoad layer of the map, table order, no repeats; for Lahai-Roi with the built
// roads' layers swapped in.
func (d *WorldData) DataLayers(instanceID int32) []int32 {
	layers := slices.Clone(d.layersOfMap[d.MapOf(instanceID)])
	if layers == nil {
		layers = []int32{}
	}
	if instanceID != RoadDungeon {
		return layers
	}
	unload := map[int32]bool{}
	var load []int32
	for _, r := range d.Tables.InfrRoadBuild {
		if r.DungeonId != RoadDungeon {
			continue
		}
		for _, u := range r.UnloadDataLayers {
			unload[u] = true
		}
		load = append(load, r.LoadDataLayers...)
	}
	layers = slices.DeleteFunc(layers, func(id int32) bool { return unload[id] })
	for _, id := range load {
		if !slices.Contains(layers, id) {
			layers = append(layers, id)
		}
	}
	return layers
}

// Roads are the auto-pilot roads of a map (AutoPilotCircles), empty when it has none.
func (d *WorldData) Roads(mapID int32) []int32 { return d.roadsOfMap[mapID] }

// ReadRoads reads AutoPilotCircles.json (specs/data-tables.md §5): per MapId, the union of the
// rows' WaySplines in first-seen order. A missing file means no roads.
func ReadRoads(jsonFile string) (map[int32][]int32, error) {
	out := map[int32][]int32{}
	b, err := os.ReadFile(jsonFile)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := data.ParseJSON(b)
	if err != nil {
		return nil, err
	}
	arr, ok := v.(*data.Array)
	if !ok {
		return nil, &data.SyntaxError{Msg: jsonFile + " is not an array"}
	}
	for _, it := range arr.Items {
		row, ok := it.(*data.Object)
		if !ok {
			continue
		}
		m, ok := exactInt32(row.Get("MapId"))
		if !ok {
			continue
		}
		list := out[m]
		if list == nil {
			list = []int32{}
		}
		if splines, ok := row.Get("WaySplines").(*data.Array); ok {
			for _, s := range splines.Items {
				if id, ok := exactInt32(s); ok && !slices.Contains(list, id) {
					list = append(list, id)
				}
			}
		}
		out[m] = list
	}
	return out, nil
}

// exactInt32 is a JSON number that is a whole 32-bit integer (JsonElement.TryGetInt32).
func exactInt32(v data.Value) (int32, bool) {
	n, ok := v.(data.Number)
	if !ok || float64(n) != float64(int32(n)) {
		return 0, false
	}
	return int32(n), true
}

// AttributeCount is the number of attribute slots (EAttributeType 0 … MAX − 1).
var AttributeCount = int(protocol.EAttributeType_value["EAttributeType_MAX"])

// attributeByName maps the C# names of EAttributeType (protoc's: the prefix stripped, then
// ShoutyToPascalCase, so MAX is Max and LifeMax stays) to their values.
var attributeByName = func() map[string]int32 {
	m := map[string]int32{}
	for name, v := range protocol.EAttributeType_value {
		m[shoutyToPascal(strings.TrimPrefix(name, "EAttributeType_"))] = v
	}
	return m
}()

// AttributeID is an attribute by its C# enum name (Life, ElementPower1 ...).
func AttributeID(name string) (int32, bool) {
	v, ok := attributeByName[name]
	return v, ok
}

// AttributeIDFold is AttributeID ignoring case (C#'s Enum.TryParse with ignoreCase), with the
// C# name.
func AttributeIDFold(name string) (int32, string, bool) {
	for n, v := range attributeByName {
		if strings.EqualFold(n, name) {
			return v, n, true
		}
	}
	return 0, "", false
}

// AttributeName is an attribute's C# enum name; a value it has no name for prints as a number
// (C#'s Enum.ToString).
func AttributeName(id int32) string {
	best := ""
	for n, v := range attributeByName {
		if v == id && (best == "" || n < best) {
			best = n
		}
	}
	if best == "" {
		return strconv.Itoa(int(id))
	}
	return best
}

// shoutyToPascal is protoc's C# enum value naming.
func shoutyToPascal(in string) string {
	var b strings.Builder
	prev := byte('_')
	alnum := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	for i := 0; i < len(in); i++ {
		c := in[i]
		if !alnum(c) {
			prev = c
			continue
		}
		switch {
		case !alnum(prev), prev >= '0' && prev <= '9':
			b.WriteString(strings.ToUpper(string(c)))
		case prev >= 'a' && prev <= 'z':
			b.WriteByte(c)
		default:
			b.WriteString(strings.ToLower(string(c)))
		}
		prev = c
	}
	return b.String()
}

// baseAttributeIndex is, for each BaseProperty field in order, its attribute index or −1.
var baseAttributeIndex = func() []int {
	t := reflect.TypeFor[data.BaseProperty]()
	idx := make([]int, t.NumField())
	for i := range idx {
		idx[i] = -1
		f := t.Field(i)
		if f.Name == "Id" || f.Type.Kind() != reflect.Int32 {
			continue
		}
		if a, ok := AttributeID(f.Name); ok && a > 0 && int(a) < AttributeCount {
			idx[i] = int(a)
		}
	}
	return idx
}()

// BaseAttributesOf is a BaseProperty row as an attribute array indexed by EAttributeType.
func (d *WorldData) BaseAttributesOf(propertyID int32) []int32 {
	out := make([]int32, AttributeCount)
	row, ok := d.Tables.BasePropertyById[propertyID]
	if !ok {
		return out
	}
	v := reflect.ValueOf(row).Elem()
	for i, to := range baseAttributeIndex {
		if to >= 0 {
			out[to] = int32(v.Field(i).Int())
		}
	}
	return out
}

// PassiveSkills are the skills EntitySkillPreload lists for the actor of a model
// (ModelConfigPreload.ActorClassPath), each once, rows without an actor or skill left out (R-122).
func (d *WorldData) PassiveSkills(modelID int32) []int32 {
	if modelID == 0 {
		return nil
	}
	model, ok := d.Tables.ModelConfigPreloadById[modelID]
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.skillsOfActor == nil {
		idx := map[string][]int32{}
		for _, r := range d.Tables.EntitySkillPreload {
			if r.ActorBlueprint == "" || r.SkillId == 0 {
				continue
			}
			if !slices.Contains(idx[r.ActorBlueprint], r.SkillId) {
				idx[r.ActorBlueprint] = append(idx[r.ActorBlueprint], r.SkillId)
			}
		}
		d.skillsOfActor = idx
	}
	return d.skillsOfActor[model.ActorClassPath]
}

// IsRepeatBoss: whether an AI's state machine (AiBase.StateMachine → AiStateMachineConfig) has
// a node with a transition on the repeat-boss tag and another from the same state on the
// story-boss tag (specs/entities.md §5.3).
func (d *WorldData) IsRepeatBoss(aiID int32) bool {
	if aiID <= 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if known, ok := d.repeatBoss[aiID]; ok {
		return known
	}
	result := false
	var machine *data.AiStateMachineConfig
	for _, a := range d.Tables.AiBase {
		if a.Id == aiID {
			for _, m := range d.Tables.AiStateMachineConfig {
				if m.Id == a.StateMachine {
					machine = m
					break
				}
			}
			break
		}
	}
	if machine != nil {
		if nodes, ok := Field(machine.StateMachineJson, "Nodes").(*data.Array); ok {
			for _, n := range nodes.Items {
				node, ok := n.(*data.Object)
				if !ok {
					continue
				}
				transitions, ok := node.Get("Transitions").(*data.Array)
				if !ok {
					continue
				}
				repeatFrom, storyFrom := map[float64]bool{}, map[float64]bool{}
				for _, t := range transitions.Items {
					tr, ok := t.(*data.Object)
					if !ok {
						continue
					}
					from, ok1 := tr.Get("From").(data.Number)
					conditions, ok2 := tr.Get("Conditions").(*data.Array)
					if !ok1 || !ok2 {
						continue
					}
					for _, c := range conditions.Items {
						co, ok := c.(*data.Object)
						if !ok {
							continue
						}
						tag, ok := JSONInt(Field(co.Get("CondTag"), "TagId"))
						if ok && tag == RepeatBossTag {
							repeatFrom[float64(from)] = true
						} else if ok && tag == StoryBossTag {
							storyFrom[float64(from)] = true
						}
					}
				}
				for f := range repeatFrom {
					if storyFrom[f] {
						result = true
					}
				}
				if result {
					break
				}
			}
		}
	}
	d.repeatBoss[aiID] = result
	return result
}

// FloorSeconds is the game clock's Unix seconds when this process first refreshed the
// instance, kept for the life of the process: the base of the world-spawn id floor.
func (d *WorldData) FloorSeconds(instanceID int32, nowSeconds int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.floorSeconds[instanceID]
	if !ok {
		s = nowSeconds
		d.floorSeconds[instanceID] = s
	}
	return s
}

// IsDoorTemplate: the blueprint's template has an interact option that moves the player (spawning.md §2).
func (d *WorldData) IsDoorTemplate(blueprintType string) bool {
	d.mu.Lock()
	door, ok := d.doorTemplate[blueprintType]
	d.mu.Unlock()
	if !ok {
		var comps data.Value
		if t := d.Template(blueprintType); t != nil {
			comps = t.ComponentsData
		}
		door = HasDoorOption(comps)
		d.mu.Lock()
		d.doorTemplate[blueprintType] = door
		d.mu.Unlock()
	}
	return door
}

// CandidatesOf is the candidate list of an instance, built once (specs/spawning.md §2).
func (d *WorldData) CandidatesOf(instanceID int32) []*Candidate {
	d.mu.Lock()
	list, ok := d.candidates[instanceID]
	d.mu.Unlock()
	if !ok {
		list = BuildCandidates(d, instanceID, nil)
		d.mu.Lock()
		d.candidates[instanceID] = list
		d.mu.Unlock()
	}
	return list
}

// QuestCandidatesOf is the candidates with the quest rules, cached by (instance, entered
// dungeon, offers on) (specs/quests.md §5.5).
func (d *WorldData) QuestCandidatesOf(instanceID int32, entered, offersEnabled bool, rules func() *CandidateRules) []*Candidate {
	key := questKey{instanceID, entered, offersEnabled}
	d.mu.Lock()
	list, ok := d.questCandidates[key]
	d.mu.Unlock()
	if !ok {
		list = BuildCandidates(d, instanceID, rules())
		d.mu.Lock()
		d.questCandidates[key] = list
		d.mu.Unlock()
	}
	return list
}

// MissingEntityTypes are the names the spawner compares BlueprintConfig.EntityType with that
// occur in no row (entities.md §3.1).
func (d *WorldData) MissingEntityTypes() []string {
	present := map[string]bool{}
	for _, b := range d.Tables.BlueprintConfig {
		present[b.EntityType] = true
	}
	var out []string
	for _, n := range ComparedTypes() {
		if !present[n] && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func (d *WorldData) findVehicleTemplate() *int32 {
	for _, tool := range d.Tables.ExploreTools {
		if tool.SummonConfigId == 0 {
			continue
		}
		summon, ok := d.Tables.SummonCfgById[tool.SummonConfigId]
		if !ok {
			continue
		}
		if b := d.Blueprint(summon.BlueprintType); b == nil || b.EntityLogic != "Vehicle" {
			continue
		}
		if t := d.Template(summon.BlueprintType); t != nil {
			id := t.Id
			return &id
		}
	}
	return nil
}
