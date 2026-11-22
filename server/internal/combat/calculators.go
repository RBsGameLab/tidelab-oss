// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"slices"
	"strconv"

	"tidelab/internal/data"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// ----- monsters (specs/combat.md §3) -----

var worldLevelFallback = []int32{10, 21, 32, 43, 54, 65, 76, 85}

// readInt is a number (truncated) or a string holding an integer; else 0.
func readInt(v data.Value) int32 {
	switch x := v.(type) {
	case data.Number:
		return int32(float64(x))
	case data.String:
		if n, err := strconv.ParseInt(trimSpace(string(x)), 10, 32); err == nil {
			return int32(n)
		}
	}
	return 0
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func objOf(v data.Value) *data.Object {
	o, _ := v.(*data.Object)
	return o
}

func member(o *data.Object, key string) data.Value {
	if o == nil {
		return nil
	}
	return o.Get(key)
}

// ResolvePropertyID is the monster's BaseProperty id: its AttributeComponent's, else its
// BaseInfoComponent's EntityPropertyId, else 2 for the character logics.
func ResolvePropertyID(merged *data.Object, entityLogic string) int32 {
	if ac := objOf(member(merged, "AttributeComponent")); ac != nil {
		disabled := false
		if b, ok := ac.Get("Disabled").(data.Bool); ok {
			disabled = bool(b)
		}
		if p := readInt(ac.Get("PropertyId")); !disabled && p > 0 {
			return p
		}
	}
	if bi := objOf(member(merged, "BaseInfoComponent")); bi != nil {
		if p := readInt(bi.Get("EntityPropertyId")); p > 0 {
			return p
		}
	}
	switch entityLogic {
	case "Animal", "Monster", "Npc", "Vision":
		return 2
	}
	return 0
}

// ResolveEffectiveLevel: a dungeon's level, else AttributeComponent.Level (>= 10), else the
// first WorldMonsterLevelMax up the Area.Father chain, else the world-level table (combat.md §3.2).
func ResolveEffectiveLevel(t *data.DataTables, merged *data.Object, areaID, worldLevel int32, dungeonLevel *int32) int32 {
	if dungeonLevel != nil && *dungeonLevel > 0 {
		return *dungeonLevel
	}
	if ac := objOf(member(merged, "AttributeComponent")); ac != nil {
		if lv := readInt(ac.Get("Level")); lv >= 10 {
			return clamp32(lv, 1, 120)
		}
	}
	wl := clamp32(worldLevel, 1, 8)
	cur := areaID
	for depth := 0; depth < 32 && cur > 0; depth++ {
		area, ok := t.AreaByAreaId[cur]
		if !ok {
			break
		}
		if c, ok := area.WorldMonsterLevelMax.Get(wl); ok && c > 0 {
			return c
		}
		if area.Father == cur {
			break
		}
		cur = area.Father
	}
	return worldLevelFallback[wl-1]
}

// ScaleSaturated is base × ratio / 10000 in 64 bits, saturated to 32 (R-126).
func ScaleSaturated(base, ratio int32) int32 {
	s := int64(base) * int64(ratio) / 10000
	return int32(max(math.MinInt32, min(s, math.MaxInt32)))
}

// BuildMonsterAttributes is a monster's (NPC's, animal's, vision's) attributes (combat.md §3).
func BuildMonsterAttributes(t *data.DataTables, merged *data.Object, entityLogic string, areaID, worldLevel int32, dungeonLevel *int32) *AttributeSet {
	set := NewAttributeSet()
	pid := ResolvePropertyID(merged, entityLogic)
	bp, ok := t.BasePropertyById[pid]
	if pid <= 0 || !ok {
		return set
	}
	raw := make([]int32, AttrCount)
	CopyFromBaseProperty(bp, raw)
	for i, v := range raw {
		set.Slots[i] = AttributeSlot{v, 0, v}
	}
	level := ResolveEffectiveLevel(t, merged, areaID, worldLevel, dungeonLevel)
	set.Slots[AttrLv] = AttributeSlot{level, 0, level}
	var curve int32
	if ac := objOf(member(merged, "AttributeComponent")); ac != nil {
		curve = readInt(ac.Get("MonsterPropGrowthId"))
	}
	var growth *data.MonsterPropertyGrowth
	for _, r := range t.MonsterPropertyGrowth {
		if r.CurveId == curve && r.Level == level {
			growth = r
			break
		}
	}
	if growth != nil {
		scale := func(a Attr, ratio int32) {
			s := &set.Slots[a]
			v := ScaleSaturated(s.Base, ratio)
			s.Base, s.Current = v, v
		}
		scale(AttrLifeMax, growth.LifeMaxRatio)
		scale(AttrLife, growth.LifeMaxRatio)
		scale(AttrAtk, growth.AtkRatio)
		scale(AttrDef, growth.DefRatio)
		scale(AttrHardnessMax, growth.HardnessMaxRatio)
		scale(AttrHardness, growth.HardnessRatio)
		scale(AttrHardnessRecover, growth.HardnessRecoverRatio)
		scale(AttrRageMax, growth.RageMaxRatio)
		scale(AttrRage, growth.RageRatio)
		scale(attr("RageRecover"), growth.RageRecoverRatio)
		scale(AttrWeaknessBuildUpMax, growth.WeaknessBuildUpMaxRatio)
	}
	// The first Life (combat.md §3.4).
	lifeMax, life := set.Slots[AttrLifeMax].Current, set.Slots[AttrLife].Current
	if lifeMax > 0 && (life <= 0 || life > lifeMax) {
		set.Slots[AttrLife] = AttributeSlot{lifeMax, 0, lifeMax}
	}
	return set
}

// BuildPartComponent is a monster model's PartComponentPb from CharacterPartConfig, counting the
// active part tags into tagCounts (combat.md §3.5); nil when the model has no parts.
func BuildPartComponent(t *data.DataTables, modelID, lifeMax int32, tagCounts *omap.Map[int32, int32]) *protocol.PartComponentPb {
	cfg, ok := t.CharacterPartConfigByModelId[modelID]
	if modelID <= 0 || !ok {
		return nil
	}
	pb := &protocol.PartComponentPb{}
	for _, p := range cfg.Parts {
		if p == nil {
			continue
		}
		maxLife := float32(-1)
		if p.LifeRatio > 0 {
			maxLife = float32(lifeMax) * p.LifeRatio
		}
		pb.PartLifeInfos = append(pb.PartLifeInfos, &protocol.PartInformation{
			PartId: p.Index, LifeValue: maxLife, LifeMax: maxLife, Activated: p.BirthActivated, PartTag: p.PartTagId,
		})
		if p.BirthActivated && p.ActiveTagId != 0 && tagCounts != nil {
			tagCounts.Set(p.ActiveTagId, tagCounts.At(p.ActiveTagId)+1)
		}
	}
	return pb
}

func PartAllowsCombine(t *data.DataTables, modelID, partID int32) bool {
	cfg, ok := t.CharacterPartConfigByModelId[modelID]
	if modelID <= 0 || !ok {
		return false
	}
	for _, p := range cfg.Parts {
		if p != nil && p.Index == partID {
			return p.CombineSocket != ""
		}
	}
	return false
}

// ----- roles (specs/roles.md §3.1–§3.3) -----

// RoleAttributeSnapshot is a role's live attributes and its echo and fetter buffs.
type RoleAttributeSnapshot struct {
	BaseProps               []int32
	AddProps                []int32
	PhantomBaseProp         map[int32]int32
	PhantomAddProp          map[int32]int32
	EchoBuffIDs             []int64
	FetterBuffIDs           []int64
	AllEchoAndFetterBuffIDs []int64
}

func sortedNonZero(m map[int32]int32) []*protocol.ArrayIntInt {
	keys := make([]int32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []*protocol.ArrayIntInt
	for _, k := range keys {
		if v := m[k]; v != 0 {
			out = append(out, &protocol.ArrayIntInt{Key: k, Value: v})
		}
	}
	return out
}

func (s *RoleAttributeSnapshot) ToRolePhantomPropInfo(roleID int32) *protocol.RolePhantomPropInfo {
	return &protocol.RolePhantomPropInfo{RoleId: roleID, BaseProp: sortedNonZero(s.PhantomBaseProp), AddProp: sortedNonZero(s.PhantomAddProp)}
}

type sourceKind int

const (
	srcWeapon sourceKind = iota
	srcSkill
	srcChain
	srcEcho
	srcFetter
)

type displayMode int

const (
	displayHidden displayMode = iota
	displayBase
	displayAdd
)

type contribution struct {
	source  sourceKind
	attr    Attr
	isRatio bool
	value   int32
	ratio   float64
	display displayMode
}

// BuildSavedBaseProp is the 143-element base_prop of role/<roleId>, from BaseProperty and
// RolePropertyGrowth (specs/roles.md §3.1).
func BuildSavedBaseProp(t *data.DataTables, ri *data.RoleInfo, level, breakthrough int32) []int32 {
	props := make([]int32, AttrCount)
	if bp, ok := t.BasePropertyById[ri.PropertyId]; ok {
		CopyFromBaseProperty(bp, props)
	}
	props[AttrLv] = level
	var g *data.RolePropertyGrowth
	for _, r := range t.RolePropertyGrowth {
		if r.Level == level && r.BreachLevel == breakthrough {
			g = r
			break
		}
	}
	if g != nil {
		mul := func(v, ratio int32) int32 { return int32(float32(v) * (float32(ratio) / 10000.0)) }
		props[AttrLifeMax] = mul(props[AttrLifeMax], g.LifeMaxRatio)
		props[AttrLife] = mul(props[AttrLife], g.LifeMaxRatio)
		props[AttrAtk] = mul(props[AttrAtk], g.AtkRatio)
		props[AttrDef] = mul(props[AttrDef], g.DefRatio)
	}
	return props
}

// EffectivePhantomItem is an echo's PhantomItem, its equipped skin's when it has one.
func EffectivePhantomItem(t *data.DataTables, e *save.EchoSave) *data.PhantomItem {
	base, ok := t.PhantomItemByItemId[e.ID]
	if !ok {
		return nil
	}
	if e.SkinID != 0 {
		if skin, ok := t.PhantomItemByItemId[e.SkinID]; ok && skin.ParentMonsterId == base.MonsterId {
			return skin
		}
	}
	return base
}

func propContribution(src sourceKind, p *data.ConfigPropValue, ratioDisplay, flatDisplay displayMode) (contribution, bool) {
	if p == nil {
		return contribution{}, false
	}
	a := MapConfigPropID(p.Id)
	if a == AttrNone {
		return contribution{}, false
	}
	if p.IsRatio {
		return contribution{src, a, true, 0, p.Value, ratioDisplay}, true
	}
	return contribution{src, a, false, RoundHalfAwayFromZero(p.Value), 0, flatDisplay}, true
}

// ComputeLive is a role's live base_props and add_props, the echo display bonuses and the
// active echo and fetter buffs (specs/roles.md §3.2–§3.3).
func ComputeLive(t *data.DataTables, player *save.PlayerData, role *save.RoleSave, roleAutoBuffTags map[string]bool) *RoleAttributeSnapshot {
	baseProps := make([]int32, AttrCount)
	if len(role.BaseProp) == int(AttrCount) {
		copy(baseProps, role.BaseProp)
	} else if ri, ok := t.RoleInfoById[role.RoleID]; ok {
		baseProps = BuildSavedBaseProp(t, ri, role.Level, role.Breakthrough)
	}
	var cs []contribution

	// 1. The equipped weapon (R-31: no custom weapon mods).
	if w := player.EquippedWeapon(role); w != nil {
		if wc, ok := t.WeaponConfByItemId[w.ID]; ok {
			cs = addWeaponProp(t, cs, wc.FirstPropId, wc.FirstCurve, w.Level, w.Breach)
			cs = addWeaponProp(t, cs, wc.SecondPropId, wc.SecondCurve, w.Level, w.Breach)
			if wb, ok := t.WeaponBaseAttributesByItemId[w.ID]; ok {
				if raw, ok := wb.Values.Get(w.ResonLevel); ok {
					for _, id := range wb.Attributes {
						a := MapConfigPropID(id)
						if a == AttrNone {
							continue
						}
						if wb.IsRatio {
							cs = append(cs, contribution{srcWeapon, a, true, 0, float64(raw) / 10000.0, displayHidden})
						} else {
							cs = append(cs, contribution{srcWeapon, a, false, raw, 0, displayHidden})
						}
					}
				}
			}
		}
	}

	// 2. Active skill-tree nodes.
	for _, n := range role.SkillNodeState {
		if !n.Active {
			continue
		}
		st, ok := t.SkillTreeById[n.NodeID]
		if !ok {
			continue
		}
		for _, p := range st.Property {
			if c, ok := propContribution(srcSkill, p, displayHidden, displayHidden); ok {
				cs = append(cs, c)
			}
		}
	}

	// 3. Unlocked resonance chains (by RoleInfo.ResonantChainGroupId).
	if ri, ok := t.RoleInfoById[role.RoleID]; ok && role.ResonantChainGroupIndex > 0 && ri.ResonantChainGroupId != 0 {
		for _, ch := range t.ResonantChain {
			if ch.GroupId != ri.ResonantChainGroupId || ch.GroupIndex > role.ResonantChainGroupIndex {
				continue
			}
			for _, p := range ch.AddProp {
				if c, ok := propContribution(srcChain, p, displayHidden, displayHidden); ok {
					cs = append(cs, c)
				}
			}
		}
	}

	// 4. Equipped echoes, Sonata fetters, and the echo and fetter passive buffs.
	slots := player.EquippedEchoes(role.RoleID)
	var fetterMonsters omap.Map[int32, *omap.Map[int32, struct{}]]
	var markers, echoBuffs, fetterBuffs, all []int64
	seen := map[int64]bool{}
	active := map[string]bool{}
	for k, v := range roleAutoBuffTags {
		active[k] = v
	}
	var slot0 *data.PhantomItem
	for i, e := range slots {
		if e == nil {
			continue
		}
		for _, mp := range e.MainProp {
			cfg, ok := t.PhantomMainPropItemById[mp.ID]
			if !ok {
				continue
			}
			if a := MapConfigPropID(cfg.PropId); a != AttrNone {
				if cfg.AddType == 2 {
					cs = append(cs, contribution{srcEcho, a, true, 0, float64(mp.Value) / 10000.0, displayAdd})
				} else {
					cs = append(cs, contribution{srcEcho, a, false, mp.Value, 0, displayBase})
				}
			}
		}
		for _, sp := range e.SubProp {
			cfg, ok := t.PhantomSubPropertyById[sp.ID]
			if !ok {
				continue
			}
			if a := MapConfigPropID(cfg.PropId); a != AttrNone {
				if cfg.AddType == 2 {
					cs = append(cs, contribution{srcEcho, a, true, 0, float64(sp.Value) / 10000.0, displayAdd})
				} else {
					cs = append(cs, contribution{srcEcho, a, false, sp.Value, 0, displayBase})
				}
			}
		}
		base, ok := t.PhantomItemByItemId[e.ID]
		if !ok {
			continue
		}
		if e.FetterGroupID != 0 {
			set, ok := fetterMonsters.Get(e.FetterGroupID)
			if !ok {
				set = &omap.Map[int32, struct{}]{}
				fetterMonsters.Set(e.FetterGroupID, set)
			}
			set.Set(base.MonsterId, struct{}{})
		}
		eff := EffectivePhantomItem(t, e)
		if eff == nil {
			eff = base
		}
		if i == 0 {
			slot0 = eff
		}
		if eff.SkillId != 0 {
			marker := int64(eff.SkillId)*1000 + 1
			if b, ok := t.BuffById[float64(marker)]; ok && len(b.GrantedTags) > 0 && b.ExtraEffectID == 0 {
				markers = append(markers, marker)
				for _, tag := range b.GrantedTags {
					active[tag] = true
				}
			}
		}
	}
	// Marker buffs, then slot 0's skill buffs, then the active fetters' buffs.
	for _, id := range markers {
		if !seen[id] {
			seen[id] = true
			echoBuffs = append(echoBuffs, id)
			all = append(all, id)
		}
	}
	if slot0 != nil && slot0.SkillId != 0 {
		if ps, ok := t.PhantomSkillByPhantomSkillId[slot0.SkillId]; ok {
			for _, raw := range ps.BuffEffects {
				if id := int64(raw); id != 0 && !seen[id] {
					seen[id] = true
					echoBuffs = append(echoBuffs, id)
					all = append(all, id)
				}
			}
		}
	}
	for groupID, monsters := range fetterMonsters.All() {
		fg, ok := t.PhantomFetterGroupById[groupID]
		if !ok {
			continue
		}
		for _, pair := range fg.FetterMap.Pairs {
			if int32(monsters.Len()) < pair.Value {
				continue
			}
			f, ok := t.PhantomFetterById[pair.Key]
			if !ok {
				continue
			}
			for _, p := range f.AddProp {
				if c, ok := propContribution(srcFetter, p, displayAdd, displayBase); ok {
					cs = append(cs, c)
				}
			}
			for _, raw := range f.BuffIds {
				if id := int64(raw); id != 0 && !seen[id] {
					seen[id] = true
					fetterBuffs = append(fetterBuffs, id)
					all = append(all, id)
				}
			}
		}
	}
	for _, id := range all {
		b, ok := t.BuffById[float64(id)]
		if !ok || !PassesOngoingTags(b, active, t) {
			continue
		}
		if b.GameAttributeID < 1 || b.GameAttributeID >= AttrCount || len(b.ModifierMagnitude) == 0 {
			continue
		}
		cs = append(cs, contribution{srcEcho, b.GameAttributeID, false, b.ModifierMagnitude[0], 0, displayAdd})
	}

	// Two passes: flat weapon values into base_props, then the rest into add_props (ratios of
	// the base_props after pass 1).
	addProps := make([]int32, AttrCount)
	pBase, pAdd := map[int32]int32{}, map[int32]int32{}
	for _, c := range cs {
		if c.source != srcWeapon || c.isRatio {
			continue
		}
		baseProps[c.attr] += c.value
		if c.attr == AttrLifeMax {
			baseProps[AttrLife] += c.value
		}
	}
	for _, c := range cs {
		if c.source == srcWeapon && !c.isRatio {
			continue
		}
		delta := c.value
		if c.isRatio {
			delta = RoundHalfAwayFromZero(float64(baseProps[c.attr]) * c.ratio)
		}
		addProps[c.attr] += delta
		if c.attr == AttrLifeMax {
			addProps[AttrLife] += delta
		}
		switch c.display {
		case displayBase:
			pBase[c.attr] += delta
		case displayAdd:
			pAdd[c.attr] += delta
		}
	}
	return &RoleAttributeSnapshot{
		BaseProps: baseProps, AddProps: addProps, PhantomBaseProp: pBase, PhantomAddProp: pAdd,
		EchoBuffIDs: echoBuffs, FetterBuffIDs: fetterBuffs, AllEchoAndFetterBuffIDs: all,
	}
}

func addWeaponProp(t *data.DataTables, cs []contribution, p *data.ConfigPropValue, curve, level, breach int32) []contribution {
	if p == nil || p.Id == 0 {
		return cs
	}
	a := MapConfigPropID(p.Id)
	if a == AttrNone {
		return cs
	}
	var g *data.WeaponPropertyGrowth
	for _, r := range t.WeaponPropertyGrowth {
		if r.CurveId == curve && r.Level == level && r.BreachLevel == breach {
			g = r
			break
		}
	}
	if g == nil {
		return cs
	}
	scaled := p.Value * float64(g.CurveValue) / 10000.0
	if p.IsRatio {
		return append(cs, contribution{srcWeapon, a, true, 0, scaled, displayHidden})
	}
	return append(cs, contribution{srcWeapon, a, false, RoundHalfAwayFromZero(scaled), 0, displayHidden})
}
