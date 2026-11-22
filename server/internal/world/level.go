// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package world

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"tidelab/internal/data"
	"tidelab/protocol"
)

// ----- JSON helpers -----

// Obj is v as an object, nil when it is not one.
func Obj(v data.Value) *data.Object {
	o, _ := v.(*data.Object)
	return o
}

// Field is the member of an object, nil when v is not an object or has no such member.
func Field(v data.Value, key string) data.Value {
	if o := Obj(v); o != nil {
		return o.Get(key)
	}
	return nil
}

// Str is a JSON string's text.
func Str(v data.Value) (string, bool) {
	s, ok := v.(data.String)
	return string(s), ok
}

func IsNull(v data.Value) bool {
	_, ok := v.(data.Null)
	return ok
}

// JSONInt is a whole number, or a string holding one.
func JSONInt(v data.Value) (int32, bool) {
	switch x := v.(type) {
	case data.Number:
		f := float64(x)
		if f == math.Floor(f) && f >= math.MinInt32 && f <= math.MaxInt32 {
			return int32(f), true
		}
	case data.String:
		if n, err := strconv.ParseInt(strings.TrimSpace(string(x)), 10, 32); err == nil {
			return int32(n), true
		}
	}
	return 0, false
}

// JSONIntOr is JSONInt, or fallback.
func JSONIntOr(v data.Value, fallback int32) int32 {
	if n, ok := JSONInt(v); ok {
		return n
	}
	return fallback
}

// ----- level entities -----

// LevelKind is what a level row turns into, decided by its blueprint's EntityLogic (specs/entities.md §3.1).
type LevelKind struct {
	Type  protocol.EEntityType
	State protocol.EntityState
	IsAI  bool
}

// LevelKindOf is the kind of an EntityLogic; false for the logics that are not spawned.
func LevelKindOf(logic string) (LevelKind, bool) {
	switch logic {
	case "Item":
		return LevelKind{TypeSceneItem, StateDefault, false}, true
	case "Animal":
		return LevelKind{TypeAnimal, StateDefault, true}, true
	case "Monster":
		return LevelKind{TypeMonster, StateBorn, true}, true
	case "Vehicle":
		return LevelKind{TypeVehicle, StateDefault, false}, true
	case "Npc":
		return LevelKind{TypeNpc, StateDefault, true}, true
	case "Vision":
		return LevelKind{TypeVision, StateDefault, true}, true
	case "ClientOnly":
		return LevelKind{TypeClientOnly, StateDefault, false}, true
	case "Custom":
		return LevelKind{TypeCustom, StateDefault, false}, true
	}
	return LevelKind{}, false
}

// IsCharacter: characters stand on their feet; their position is lifted by the blueprint's half height (§3.2).
func (k LevelKind) IsCharacter() bool {
	return k.Type == TypeAnimal || k.Type == TypeMonster || k.Type == TypeNpc || k.Type == TypeVision
}

// MovedPlace is where a quest moved an entity, and its yaw if one was given.
type MovedPlace struct {
	Pos Vec3
	Yaw *float32
}

// LevelContext is what the world knows about a level entity beyond its row.
type LevelContext struct {
	// WorldLevel is the player's world level.
	WorldLevel int32
	// SavedState is the state tag a player gave the entity before (world_state).
	SavedState func(id int32) (int32, bool)
	// Moved is a place a quest moved the entity to (entity_pos, R-82e).
	Moved func(id int32) (MovedPlace, bool)
}

func (c *LevelContext) savedState(id int32) (int32, bool) {
	if c == nil || c.SavedState == nil {
		return 0, false
	}
	return c.SavedState(id)
}

func (c *LevelContext) moved(id int32) (MovedPlace, bool) {
	if c == nil || c.Moved == nil {
		return MovedPlace{}, false
	}
	return c.Moved(id)
}

// The components merged with the template's (§5.1).
var MergedComponents = []string{
	"BaseInfoComponent", "AiComponent", "AttributeComponent", "ModelComponent", "MonsterComponent",
	"RewardComponent", "VisionCaptureComponent", "InteractComponent", "EntityStateComponent",
}

const (
	RepeatBossTag     int32 = 1639442014
	StoryBossTag      int32 = -1276461747
	AiTeamInitID            = 100
	AiCombatMessageID       = 1
)

// Place is the row's location and rotation: Transform / 100, X → Roll, Y → Pitch, Z → Yaw (§3.2).
func Place(row *data.LevelEntityConfig) (Vec3, Rot, bool) {
	if len(row.Transform) < 2 || row.Transform[0] == nil || row.Transform[1] == nil {
		return Vec3{}, Rot{}, false
	}
	p, r := row.Transform[0], row.Transform[1]
	return Vec3{float32(p.X) / 100, float32(p.Y) / 100, float32(p.Z) / 100},
		Rot{Pitch: float32(r.Y) / 100, Yaw: float32(r.Z) / 100, Roll: float32(r.X) / 100}, true
}

// Merge is the row's components merged with its template's: a component the row lacks is the
// template's; one both have is the row's, with each top-level field the row leaves out (absent
// or null) taken from the template. Not deeper. Other components are the row's.
func Merge(row, template data.Value) map[string]*data.Object {
	out := map[string]*data.Object{}
	if r := Obj(row); r != nil {
		for i, k := range r.Keys {
			if o := Obj(r.Values[i]); o != nil {
				out[k] = o
			}
		}
	}
	t := Obj(template)
	if t == nil {
		return out
	}
	for _, name := range MergedComponents {
		tc := Obj(t.Get(name))
		if tc == nil {
			continue
		}
		rc, ok := out[name]
		if !ok {
			out[name] = tc
			continue
		}
		keys := append([]string(nil), rc.Keys...)
		values := append([]data.Value(nil), rc.Values...)
		for i, k := range tc.Keys {
			at := -1
			for j, kk := range keys {
				if kk == k {
					at = j
					break
				}
			}
			if at < 0 {
				keys = append(keys, k)
				values = append(values, tc.Values[i])
			} else if IsNull(values[at]) {
				values[at] = tc.Values[i]
			}
		}
		out[name] = &data.Object{Keys: keys, Values: values}
	}
	return out
}

// Identity is the config identity in EntityPb when it is not the row's (a prefab entity:
// Prefab, its prefab id and group inc; a wave monster: Template).
type Identity struct {
	Type        protocol.EntityConfigType
	PrefabID    int32
	PrefabIncID int64
}

// BuildLevelEntity is the entity of a row, its id not yet given, or nil and why not when the
// row cannot be spawned (no blueprint or template, a refused logic, fewer than two transform
// vectors, no BaseInfoComponent after the merge: §5.6).
func BuildLevelEntity(d *WorldData, row *data.LevelEntityConfig, ctx *LevelContext, identity *Identity) (*Entity, string) {
	blueprint, template := d.Blueprint(row.BlueprintType), d.Template(row.BlueprintType)
	if blueprint == nil {
		return nil, "no blueprint"
	}
	if template == nil {
		return nil, "no template"
	}
	kind, ok := LevelKindOf(blueprint.EntityLogic)
	if !ok {
		return nil, "logic " + blueprint.EntityLogic
	}
	pos, rot, ok := Place(row)
	if !ok {
		return nil, "no transform"
	}
	if m, ok := ctx.moved(row.EntityId); ok {
		pos = m.Pos
		if m.Yaw != nil {
			rot.Yaw = *m.Yaw
		}
	}
	if kind.IsCharacter() && blueprint.HalfHeight > 0 {
		pos.Z += float32(blueprint.HalfHeight)
	}
	c := Merge(row.ComponentsData, template.ComponentsData)
	if _, ok := c["BaseInfoComponent"]; !ok {
		return nil, "no BaseInfoComponent"
	}
	templateObj := Obj(template.ComponentsData)
	camp := campOf(c, templateObj, kind)
	e := &Entity{
		ConfigID: row.EntityId, ConfigType: ConfigLevel, EntityType: kind.Type,
		Pos: pos, Rot: rot, IsVisible: true, State: kind.State, Camp: &camp, LevelMap: row.MapId,
	}
	if identity != nil {
		e.ConfigType, e.PrefabID, e.PrefabIncID = identity.Type, identity.PrefabID, identity.PrefabIncID
	}
	stateTag, hasTag := StateTag(d, row, kind, c, templateObj, ctx)
	tags := tagsOf(d, c, kind, stateTag, hasTag)
	if kind.IsAI {
		e.Components = append(e.Components,
			monsterAttributes(d, c["AttributeComponent"]),
			C(&protocol.EntityComponentPb_TagComponent{TagComponent: tags}),
			C(&protocol.EntityComponentPb_FightBuffComponent{FightBuffComponent: &protocol.FightBuffComponentPb{}}),
			C(&protocol.EntityComponentPb_CombineEntityInfoPb{CombineEntityInfoPb: &protocol.CombineEntityInfoPb{}}),
			C(&protocol.EntityComponentPb_LogicStateComponentPb{LogicStateComponentPb: &protocol.LogicStateComponentPb{}}),
			C(&protocol.EntityComponentPb_MonsterAiComponentPb{MonsterAiComponentPb: &protocol.MonsterAiComponentPb{
				WeaponId:        JSONIntOr(objGet(c["AiComponent"], "WeaponId"), 0),
				HatredGroupId:   0,
				AiTeamInitId:    AiTeamInitID,
				CombatMessageId: AiCombatMessageID,
			}}))
		if kind.Type == TypeMonster {
			skills := &protocol.SkillComponentPb{}
			model, ok := modelIDOf(c)
			if !ok {
				model = blueprint.ModelId
			}
			for _, id := range d.PassiveSkills(model) {
				skills.GaPassiveSkills = append(skills.GaPassiveSkills, &protocol.GaPassiveSkill{SkillId: int64(id), ContextId: PassiveSkillContextID})
			}
			e.Components = append(e.Components, C(&protocol.EntityComponentPb_SkillComponentPb{SkillComponentPb: skills}))
		}
	} else {
		if hasTag {
			e.Components = append(e.Components, C(&protocol.EntityComponentPb_TagComponent{TagComponent: tags}))
		}
		e.Components = append(e.Components, C(&protocol.EntityComponentPb_FightBuffComponent{FightBuffComponent: &protocol.FightBuffComponentPb{}}))
	}
	if hasTag {
		e.Components = append(e.Components, C(&protocol.EntityComponentPb_StateTagComponentPb{StateTagComponentPb: &protocol.StateTagComponentPb{StateTagId: stateTag}}))
	}
	return e, ""
}

func objGet(o *data.Object, key string) data.Value {
	if o == nil {
		return nil
	}
	return o.Get(key)
}

// StateTag is the state tag of §3.3, false when the entity carries none. Scene items and
// entities whose merged components have an EntityStateComponent carry one: a state the player
// gave it before; else a beacon's active state; else the template's state, the row's, or the
// normal state.
func StateTag(d *WorldData, row *data.LevelEntityConfig, kind LevelKind, merged map[string]*data.Object, template *data.Object, ctx *LevelContext) (int32, bool) {
	if _, ok := merged["EntityStateComponent"]; kind.Type != TypeSceneItem && !ok {
		return 0, false
	}
	if saved, ok := ctx.savedState(row.EntityId); ok {
		return saved, true
	}
	return TagID(d.Tables, stateName(row, template)), true
}

func stateName(row *data.LevelEntityConfig, template *data.Object) string {
	if strings.Contains(row.BlueprintType, "Teleport") {
		return TagActive
	}
	if s, ok := Str(Field(objGet(template, "EntityStateComponent"), "State")); ok {
		return s
	}
	if s, ok := Str(Field(Field(row.ComponentsData, "EntityStateComponent"), "State")); ok {
		return s
	}
	return TagNormal
}

func campOf(c map[string]*data.Object, template *data.Object, kind LevelKind) int32 {
	camp, has := JSONInt(objGet(c["BaseInfoComponent"], "Camp"))
	if kind.Type == TypeMonster && (!has || camp == 0 || camp == 2) {
		if fromTemplate, ok := JSONInt(Field(objGet(template, "BaseInfoComponent"), "Camp")); ok {
			return fromTemplate
		}
	}
	return camp
}

func tagsOf(d *WorldData, c map[string]*data.Object, kind LevelKind, stateTag int32, hasTag bool) *protocol.TagComponentPb {
	tags := &protocol.TagComponentPb{InitGameplayTag: false}
	add := func(id int32) {
		for _, g := range tags.GameplayTags {
			if g.Id == id {
				g.TagCount++
				return
			}
		}
		tags.GameplayTags = append(tags.GameplayTags, &protocol.GameplayTagData{Id: id, TagCount: 1})
	}
	if kind.Type == TypeMonster {
		if init, ok := objGet(c["MonsterComponent"], "InitGasTag").(*data.Array); ok {
			repeat := d.IsRepeatBoss(JSONIntOr(objGet(c["AiComponent"], "AiId"), 0))
			for _, n := range init.Items {
				name, ok := Str(n)
				if !ok {
					continue
				}
				id := TagID(d.Tables, name)
				if repeat && id == StoryBossTag {
					id = RepeatBossTag
				}
				add(id)
			}
		}
	}
	if hasTag {
		add(stateTag)
		tags.EntityCommonTags = append(tags.EntityCommonTags, stateTag)
	}
	return tags
}

func monsterAttributes(d *WorldData, attribute *data.Object) *protocol.EntityComponentPb {
	if attribute == nil {
		return Attributes(nil, nil)
	}
	values := d.BaseAttributesOf(JSONIntOr(attribute.Get("PropertyId"), 0))
	if level, ok := JSONInt(attribute.Get("Level")); ok && len(values) > 1 {
		values[1] = level
	}
	c := Attributes(values, nil)
	a := c.Component.(*protocol.EntityComponentPb_AttributeComponent).AttributeComponent
	a.HardnessModeId = JSONIntOr(attribute.Get("HardnessModeId"), 0)
	a.RageModeId = JSONIntOr(attribute.Get("RageModeId"), 0)
	return c
}

func modelIDOf(c map[string]*data.Object) (int32, bool) {
	return JSONInt(Field(objGet(c["ModelComponent"], "ModelType"), "ModelId"))
}

// RefusedError is a row that cannot be spawned.
func RefusedError(row *data.LevelEntityConfig, why string) error {
	return fmt.Errorf("row %d (%s) cannot be spawned: %s", row.EntityId, row.BlueprintType, why)
}
