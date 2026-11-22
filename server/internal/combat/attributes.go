// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package combat is a player's roles, inventory, formation and fights (R-143g): the
// attribute sets, role and monster attribute calculators, buffs, damage, kills and drops,
// revive, the inventory of items, weapons and echoes, and the formation's role entities.
// Ported from our C# Attributes/, Roles/, Inventory/, Formation/, Combat/ and R143gGame;
// one Go package because those C# namespaces use one another.
package combat

import (
	"math"
	"reflect"
	"strings"

	"tidelab/internal/data"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// Attr is an attribute (EAttributeType), as an index.
type Attr = int32

// attr looks up an attribute by its C# enum name; a wrong name is a programming error.
func attr(name string) Attr {
	v, ok := world.AttributeID(name)
	if !ok {
		panic("no attribute " + name)
	}
	return v
}

var (
	AttrNone                 = attr("None")
	AttrLv                   = attr("Lv")
	AttrLifeMax              = attr("LifeMax")
	AttrLife                 = attr("Life")
	AttrAtk                  = attr("Atk")
	AttrDef                  = attr("Def")
	AttrCrit                 = attr("Crit")
	AttrCritDamage           = attr("CritDamage")
	AttrDamageReduce         = attr("DamageReduce")
	AttrAutoAttackSpeed      = attr("AutoAttackSpeed")
	AttrCastAttackSpeed      = attr("CastAttackSpeed")
	AttrEnergy               = attr("Energy")
	AttrEnergyMax            = attr("EnergyMax")
	AttrStrength             = attr("Strength")
	AttrStrengthMax          = attr("StrengthMax")
	AttrHardness             = attr("Hardness")
	AttrHardnessMax          = attr("HardnessMax")
	AttrHardnessRecover      = attr("HardnessRecover")
	AttrRage                 = attr("Rage")
	AttrRageMax              = attr("RageMax")
	AttrTough                = attr("Tough")
	AttrToughMax             = attr("ToughMax")
	AttrToughRecover         = attr("ToughRecover")
	AttrElementEnergy        = attr("ElementEnergy")
	AttrElementEnergyMax     = attr("ElementEnergyMax")
	AttrParalysisTime        = attr("ParalysisTime")
	AttrParalysisTimeMax     = attr("ParalysisTimeMax")
	AttrParalysisTimeRecover = attr("ParalysisTimeRecover")
	AttrWeaknessBuildUp      = attr("WeaknessBuildUp")
	AttrWeaknessBuildUpMax   = attr("WeaknessBuildUpMax")
	AttrElementPropertyType  = attr("ElementPropertyType")
)

// AttrCount is the number of slots (EAttributeType.MAX, 143).
var AttrCount = int32(world.AttributeCount)

func attrsNamed(prefix string, from, to int) []Attr {
	var out []Attr
	for i := from; i <= to; i++ {
		out = append(out, attr(prefix+itoa(i)))
	}
	return out
}

func itoa(i int) string { return string(rune('0' + i)) }

var (
	maxAttr     = map[Attr]Attr{}
	recoverAttr = map[Attr]Attr{}
	negativeOK  = map[Attr]bool{}
)

func init() {
	for k, v := range map[string]string{
		"Life": "LifeMax", "Strength": "StrengthMax", "Hardness": "HardnessMax", "Rage": "RageMax", "Tough": "ToughMax",
		"Energy": "EnergyMax", "ElementEnergy": "ElementEnergyMax",
		"SpecialEnergy1": "SpecialEnergy1Max", "SpecialEnergy2": "SpecialEnergy2Max", "SpecialEnergy3": "SpecialEnergy3Max",
		"SpecialEnergy4": "SpecialEnergy4Max", "SpecialEnergy5": "SpecialEnergy5Max",
		"StatusBuildUp1": "StatusBuildUp1Max", "StatusBuildUp2": "StatusBuildUp2Max", "StatusBuildUp3": "StatusBuildUp3Max",
		"StatusBuildUp4": "StatusBuildUp4Max", "StatusBuildUp5": "StatusBuildUp5Max",
		"ParalysisTime": "ParalysisTimeMax", "WeaknessBuildUp": "WeaknessBuildUpMax",
	} {
		maxAttr[attr(k)] = attr(v)
	}
	for k, v := range map[string]string{"Hardness": "HardnessRecover", "Tough": "ToughRecover", "ParalysisTime": "ParalysisTimeRecover"} {
		recoverAttr[attr(k)] = attr(v)
	}
	for _, n := range []string{"Crit", "DamageReduce", "DamageReducePhys",
		"DamageReduceElement1", "DamageReduceElement2", "DamageReduceElement3", "DamageReduceElement4", "DamageReduceElement5", "DamageReduceElement6",
		"DamageResistancePhys", "DamageResistanceElement1", "DamageResistanceElement2", "DamageResistanceElement3",
		"DamageResistanceElement4", "DamageResistanceElement5", "DamageResistanceElement6", "ParalysisTimeRecover"} {
		negativeOK[attr(n)] = true
	}
}

// AttributeSlot is one attribute: its base, the increment on it, and the current value.
type AttributeSlot struct{ Base, Increment, Current int32 }

// AttributeSet is the 143 attributes indexed by EAttributeType, with clamping, the wire forms
// and BaseProperty copying (specs/roles.md §3, combat.md §4).
type AttributeSet struct {
	Slots []AttributeSlot
}

func NewAttributeSet() *AttributeSet { return &AttributeSet{Slots: make([]AttributeSlot, AttrCount)} }

// RoundHalfAwayFromZero rounds as C#'s Math.Round(v, MidpointRounding.AwayFromZero), cast to int.
func RoundHalfAwayFromZero(v float64) int32 { return int32(math.Round(v)) }

// MapConfigPropID maps a config PropId to an attribute (specs/roles.md §3.2); 0 when none.
func MapConfigPropID(propID int32) Attr {
	if propID <= 0 {
		return AttrNone
	}
	var mapped int32
	switch {
	case propID == 10002:
		mapped = AttrLifeMax
	case propID == 10007:
		mapped = AttrAtk
	case propID == 10010:
		mapped = AttrDef
	case propID >= 10000:
		mapped = propID - 10000
	default:
		mapped = propID
	}
	if mapped >= 1 && mapped < AttrCount {
		return mapped
	}
	return AttrNone
}

// IsStateAttribute: the 25 attributes whose WhiteValue is Current (specs/roles.md §3.3.1).
func IsStateAttribute(a Attr) bool { return world.StateAttributes[int(a)] }

func MaxAttribute(a Attr) (Attr, bool) {
	m, ok := maxAttr[a]
	return m, ok
}

func RecoverAttribute(a Attr) (Attr, bool) {
	r, ok := recoverAttr[a]
	return r, ok
}

func AllowsNegative(a Attr) bool { return negativeOK[a] }

func clamp32(v, lo, hi int32) int32 { return max(lo, min(v, hi)) }

// ClampValue clamps value as specs/combat.md §4 says.
func (s *AttributeSet) ClampValue(a Attr, value int32) int32 {
	if a == AttrAutoAttackSpeed || a == AttrCastAttackSpeed {
		return clamp32(value, 0, 20000)
	}
	if a == AttrDamageReduce {
		return clamp32(value, -10000, 10000)
	}
	if m, ok := MaxAttribute(a); ok {
		return clamp32(value, 0, max(0, s.Slots[m].Current))
	}
	if AllowsNegative(a) {
		return value
	}
	return max(0, value)
}

// ApplyDelta adds delta to an attribute (a dead entity's Life does not move; clamped; LifeMax
// keeps the Life ratio) and returns the attributes that changed (specs/combat.md §4).
func (s *AttributeSet) ApplyDelta(a Attr, delta int32, toIncrement bool) []Attr {
	var changed []Attr
	if a <= AttrNone || a >= AttrCount || delta == 0 {
		return changed
	}
	if a == AttrLife && s.Slots[AttrLife].Current <= 0 {
		return changed
	}
	slot := &s.Slots[a]
	old := slot.Current
	if toIncrement && !IsStateAttribute(a) {
		slot.Increment += delta
		slot.Current = s.ClampValue(a, slot.Base+slot.Increment)
	} else {
		sum := int64(slot.Current) + int64(delta)
		next := s.ClampValue(a, int32(max(math.MinInt32, min(sum, math.MaxInt32))))
		slot.Current = next
		if a == AttrLife {
			slot.Base = next
		}
	}
	if slot.Current != old {
		changed = append(changed, a)
	}
	if a == AttrLifeMax && old > 0 && slot.Current != old {
		changed = s.preserveLifeRatio(old, slot.Current, changed)
	}
	return changed
}

// ApplyOverride sets an attribute to value (CalculationPolicy 3).
func (s *AttributeSet) ApplyOverride(a Attr, value int32) []Attr {
	var changed []Attr
	if a <= AttrNone || a >= AttrCount {
		return changed
	}
	if a == AttrLife && s.Slots[AttrLife].Current <= 0 {
		return changed
	}
	slot := &s.Slots[a]
	old := slot.Current
	v := s.ClampValue(a, value)
	slot.Current, slot.Base, slot.Increment = v, v, 0
	if slot.Current != old {
		changed = append(changed, a)
	}
	if a == AttrLifeMax && old > 0 && slot.Current != old {
		changed = s.preserveLifeRatio(old, slot.Current, changed)
	}
	return changed
}

func (s *AttributeSet) preserveLifeRatio(oldMax, newMax int32, changed []Attr) []Attr {
	life := &s.Slots[AttrLife]
	oldLife := life.Current
	ratio := float32(oldLife) / float32(oldMax)
	next := clamp32(RoundHalfAwayFromZero(float64(float32(newMax)*ratio)), 0, max(0, newMax))
	life.Base, life.Current = next, next
	if next != oldLife {
		changed = append(changed, AttrLife)
	}
	return changed
}

// LoadFrom writes all slots from paired base and add arrays (specs/roles.md §3.2 step 3).
func (s *AttributeSet) LoadFrom(base, add []int32) {
	n := min(int(AttrCount), len(base), len(add))
	for i := 0; i < n; i++ {
		s.Slots[i] = AttributeSlot{base[i], add[i], base[i] + add[i]}
	}
	for i := n; i < int(AttrCount); i++ {
		s.Slots[i] = AttributeSlot{}
	}
}

// ToAttributeComponentPb is all attributes as an AttributeComponentPb (specs/roles.md §3.3.1).
func (s *AttributeSet) ToAttributeComponentPb(hardnessModeID, rageModeID int32) *protocol.AttributeComponentPb {
	pb := &protocol.AttributeComponentPb{HardnessModeId: hardnessModeID, RageModeId: rageModeID}
	for i := int32(1); i < AttrCount; i++ {
		sl := s.Slots[i]
		white := sl.Base
		if IsStateAttribute(i) {
			white = sl.Current
		}
		pb.WhiteGreenProps = append(pb.WhiteGreenProps, &protocol.WhiteGreenPropData{
			AttributeType: i, WhiteValue: white, GreenValue: sl.Increment, CurrentValue: sl.Current,
		})
	}
	return pb
}

// ToGameplayAttributeData is one attribute (specs/roles.md §3.3.2): BaseValue is the base for
// LifeMax, Atk and Def and the current value for the others.
func (s *AttributeSet) ToGameplayAttributeData(a Attr) *protocol.GameplayAttributeData {
	sl := s.Slots[a]
	b := sl.Current
	if a == AttrLifeMax || a == AttrAtk || a == AttrDef {
		b = sl.Base
	}
	return &protocol.GameplayAttributeData{AttributeType: a, BaseValue: b, CurrentValue: sl.Current}
}

// BuildChangeNotifies are the AttributeChangedNotify and RecoverPropChangedNotify of a changed
// batch (specs/combat.md §4).
func (s *AttributeSet) BuildChangeNotifies(entityID int64, changed []Attr, nowMs int64) []*protocol.CombatNotifyData {
	if len(changed) == 0 {
		return nil
	}
	an := &protocol.AttributeChangedNotify{}
	rn := &protocol.RecoverPropChangedNotify{CurTime: nowMs}
	for _, a := range changed {
		an.Attributes = append(an.Attributes, s.ToGameplayAttributeData(a))
		r, ok1 := RecoverAttribute(a)
		m, ok2 := MaxAttribute(a)
		if ok1 && ok2 {
			rn.Attributes = append(rn.Attributes, &protocol.RecoverAttr{
				AttrId: a, Ratio: s.Slots[r].Current, MaxValue: s.Slots[m].Current, CurrentValue: s.Slots[a].Current,
			})
		}
	}
	return []*protocol.CombatNotifyData{
		{CombatCommon: &protocol.CombatCommon{EntityId: entityID}, Message: &protocol.CombatNotifyData_AttributeChangedNotify{AttributeChangedNotify: an}},
		{CombatCommon: &protocol.CombatCommon{EntityId: entityID}, Message: &protocol.CombatNotifyData_RecoverPropChangedNotify{RecoverPropChangedNotify: rn}},
	}
}

func (s *AttributeSet) BuildAllAttributesChangedNotify(entityID int64) *protocol.CombatNotifyData {
	n := &protocol.AttributeChangedNotify{}
	for i := int32(0); i < AttrCount; i++ {
		n.Attributes = append(n.Attributes, s.ToGameplayAttributeData(i))
	}
	return &protocol.CombatNotifyData{CombatCommon: &protocol.CombatCommon{EntityId: entityID},
		Message: &protocol.CombatNotifyData_AttributeChangedNotify{AttributeChangedNotify: n}}
}

// CopyFromBaseProperty copies the named attribute columns of a BaseProperty row into dest
// (len AttrCount), the rest 0.
func CopyFromBaseProperty(p *data.BaseProperty, dest []int32) {
	clear(dest)
	copy(dest, basePropertyValues(p))
}

// basePropertyValues reads the row's columns by attribute name (the C# copies the same 128
// columns by name).
func basePropertyValues(p *data.BaseProperty) []int32 {
	out := make([]int32, AttrCount)
	for _, f := range basePropertyFields {
		out[f.attr] = f.get(p)
	}
	return out
}

type propField struct {
	attr Attr
	get  func(*data.BaseProperty) int32
}

// basePropertyColumns are the 128 BaseProperty columns copied, by name (each is also the
// attribute's name).
var basePropertyColumns = []string{
	"Lv", "LifeMax", "Life", "Sheild", "SheildDamageChange", "SheildDamageReduce", "Atk", "Crit", "CritDamage",
	"Def", "EnergyEfficiency", "CdReduse", "ElementEfficiency", "DamageChangeNormalSkill", "DamageChange",
	"DamageReduce", "DamageChangeAuto", "DamageChangeCast", "DamageChangeUltra", "DamageChangeQte",
	"DamageChangePhys", "DamageChangeElement1", "DamageChangeElement2", "DamageChangeElement3",
	"DamageChangeElement4", "DamageChangeElement5", "DamageChangeElement6", "DamageResistancePhys",
	"DamageResistanceElement1", "DamageResistanceElement2", "DamageResistanceElement3",
	"DamageResistanceElement4", "DamageResistanceElement5", "DamageResistanceElement6", "HealChange",
	"HealedChange", "DamageReducePhys", "DamageReduceElement1", "DamageReduceElement2", "DamageReduceElement3",
	"DamageReduceElement4", "DamageReduceElement5", "DamageReduceElement6", "SpecialEnergy5Max",
	"SpecialEnergy5", "EnergyMax", "Energy", "SpecialEnergy1Max", "SpecialEnergy1", "SpecialEnergy2Max",
	"SpecialEnergy2", "SpecialEnergy3Max", "SpecialEnergy3", "SpecialEnergy4Max", "SpecialEnergy4",
	"StrengthMax", "Strength", "StrengthRecover", "StrengthPunishTime", "StrengthRun", "StrengthSwim",
	"StrengthFastSwim", "ElementEnergyMax", "ElementEnergy", "HardnessMax", "Hardness", "HardnessRecover",
	"HardnessPunishTime", "HardnessChange", "HardnessReduce", "ToughMax", "Tough", "ToughRecover",
	"ToughChange", "ToughReduce", "ElementPower1", "ElementPower2", "ElementPower3", "ElementPower4",
	"ElementPower5", "ElementPower6", "SpecialDamageChange", "StrengthFastClimbCost", "ElementPropertyType",
	"WeakTime", "IgnoreDefRate", "IgnoreDamageResistancePhys", "IgnoreDamageResistanceElement1",
	"IgnoreDamageResistanceElement2", "IgnoreDamageResistanceElement3", "IgnoreDamageResistanceElement4",
	"IgnoreDamageResistanceElement5", "IgnoreDamageResistanceElement6", "SkillToughRatio", "StrengthClimbJump",
	"StrengthGliding", "Mass", "BrakingFrictionFactor", "GravityScale", "SpeedRatio", "DamageChangePhantom",
	"AutoAttackSpeed", "CastAttackSpeed", "StatusBuildUp1Max", "StatusBuildUp1", "StatusBuildUp2Max",
	"StatusBuildUp2", "StatusBuildUp3Max", "StatusBuildUp3", "StatusBuildUp4Max", "StatusBuildUp4",
	"StatusBuildUp5Max", "StatusBuildUp5", "RageMax", "Rage", "RageRecover", "RagePunishTime", "RageChange",
	"RageReduce", "ToughRecoverDelayTime", "ParalysisTimeMax", "ParalysisTime", "ParalysisTimeRecover",
	"WeaknessBuildUp", "WeaknessBuildUpMax", "WeaknessTotalBonus", "BreakWeaknessRatio", "WeaknessMastery",
}

var basePropertyFields = func() []propField {
	t := reflect.TypeFor[data.BaseProperty]()
	var out []propField
	for _, n := range basePropertyColumns {
		f, ok := t.FieldByName(n)
		if !ok {
			panic("BaseProperty has no column " + n)
		}
		idx := f.Index
		out = append(out, propField{attr(n), func(p *data.BaseProperty) int32 {
			return int32(reflect.ValueOf(p).Elem().FieldByIndex(idx).Int())
		}})
	}
	return out
}()

// ----- gameplay tags -----

const FnvPrime = 16_777_619

// HashTag is the signed tag id of a name over its UTF-16 code units, remapped through
// GameplayTagRemap when tables are given (specs/entities.md §3.4).
func HashTag(name string, t *data.DataTables) int32 {
	if t == nil {
		return world.RawTagID(name)
	}
	return world.TagID(t, name)
}

// TagMatches: held satisfies required by exact match, dot-prefix hierarchy or the
// GameplayTagParent ancestor chain.
func TagMatches(held, required string, t *data.DataTables) bool {
	if required == "" || held == required {
		return true
	}
	if len(held) > len(required) && held[len(required)] == '.' && strings.HasPrefix(held, required) {
		return true
	}
	if t != nil && len(t.GameplayTagParentById) > 0 {
		target, cur := HashTag(required, t), HashTag(held, t)
		for depth := 0; depth < 32; depth++ {
			if cur == target {
				return true
			}
			row, ok := t.GameplayTagParentById[cur]
			if !ok || row.ParentId == 0 || row.ParentId == cur {
				break
			}
			cur = row.ParentId
		}
	}
	return false
}

// SetHasTag: some tag of the set satisfies required.
func SetHasTag(active map[string]bool, required string, t *data.DataTables) bool {
	if active[required] {
		return true
	}
	for held := range active {
		if TagMatches(held, required, t) {
			return true
		}
	}
	return false
}

// PassesOngoingTags: every OngoingTagRequirements holds and no OngoingTagIgnores does.
func PassesOngoingTags(b *data.Buff, active map[string]bool, t *data.DataTables) bool {
	for _, r := range b.OngoingTagRequirements {
		if r != "" && !SetHasTag(active, r, t) {
			return false
		}
	}
	for _, i := range b.OngoingTagIgnores {
		if i != "" && SetHasTag(active, i, t) {
			return false
		}
	}
	return true
}
