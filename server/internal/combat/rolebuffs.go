// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"slices"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// InitialBuffSpec is a buff a role entity spawns with.
type InitialBuffSpec struct {
	BuffID   int64
	IsActive bool
}

var (
	commonFixedBuffs = []int64{3003, 3004, 3127, 3134, 1213, 1214, 1215, 1216, 1001000000, 70000077}

	concertoExemptRoles = map[int32]bool{1211: true, 1108: true, 1210: true, 1306: true, 1608: true, 1507: true,
		1107: true, 1405: true, 1403: true, 1308: true, 1311: true}

	autoBuffExcludedSuffixPrefixes = []string{"666", "990", "777", "095", "402", "403", "302", "291", "98", "009", "020"}

	autoBuffExcludedTagSubstrings = []string{
		"Rogue", "肉鸽", "试用", "零能量", "59b4b0a5", "371b7a72",
		"角色.R2T1JiyanMd10011.技能标识.大招状态", "23d0723c", "20b04084",
		"8c658780", "9296f07f", "c79fde44", "fbcbee28", "ed839a56",
		"功能.功能制作.禁止体力恢复",
	}
)

const (
	roverMaleGenderBuff   = 291724064
	roverFemaleGenderBuff = 291724065
	concertoEnergyBuff    = 1101002011
)

// RoleBuffBuilder assembles the first FightBuffComponentPb buffs of a role entity
// (specs/roles.md §5.3). A role's own buffs are cached for the loaded tables (R-131).
type RoleBuffBuilder struct {
	tables       *data.DataTables
	concomitants *ConcomitantResolver
	mu           sync.Mutex
	ownBuffs     map[int32][]InitialBuffSpec
	autoTags     map[int32]map[string]bool
	honamiIDs    map[int64]bool
	comboIDs     map[int64]bool
}

func NewRoleBuffBuilder(t *data.DataTables, c *ConcomitantResolver) *RoleBuffBuilder {
	b := &RoleBuffBuilder{tables: t, concomitants: c, ownBuffs: map[int32][]InitialBuffSpec{},
		autoTags: map[int32]map[string]bool{}, honamiIDs: map[int64]bool{}, comboIDs: map[int64]bool{}}
	for _, r := range t.HonamiStoryEffect {
		for _, p := range r.Param {
			if len(p) >= 7 {
				if id, ok := save.ParseDigits(p, 64); ok {
					b.honamiIDs[id] = true
				}
			}
		}
	}
	for _, r := range t.ComboTeaching {
		for _, raw := range r.AddBuffID {
			if id := int64(raw); id != 0 {
				b.comboIDs[id] = true
			}
		}
	}
	return b
}

// StartsActiveAtSpawn: a buff starts active at spawn iff all 9 of its lists are empty (roles.md §5.3).
func StartsActiveAtSpawn(b *data.Buff) bool {
	return len(b.ModifierMagnitude) == 0 && len(b.BuffAction) == 0 &&
		len(b.OngoingTagRequirements) == 0 && len(b.OngoingTagIgnores) == 0 &&
		len(b.RemovalTagRequirements) == 0 && len(b.RemovalTagIgnores) == 0 &&
		len(b.ApplicationSourceTagIgnores) == 0 && len(b.ApplicationTagIgnores) == 0 &&
		len(b.ExtraEffectRequirements) == 0
}

func (b *RoleBuffBuilder) buff(id int64) (*data.Buff, bool) {
	x, ok := b.tables.BuffById[float64(id)]
	return x, ok
}

// RoleAutoBuffTags is the union of GrantedTags of a role's own buffs.
func (b *RoleBuffBuilder) RoleAutoBuffTags(roleID int32) map[string]bool {
	b.mu.Lock()
	if t, ok := b.autoTags[roleID]; ok {
		b.mu.Unlock()
		return t
	}
	b.mu.Unlock()
	tags := map[string]bool{}
	for _, s := range b.RoleOwnBuffs(roleID) {
		if buff, ok := b.buff(s.BuffID); ok {
			for _, tag := range buff.GrantedTags {
				if tag != "" {
					tags[tag] = true
				}
			}
		}
	}
	b.mu.Lock()
	b.autoTags[roleID] = tags
	b.mu.Unlock()
	return tags
}

// parseInteger reads an integer as .NET's NumberStyles.Integer: spaces around, a sign.
func parseInteger(s string) (int64, bool) {
	n, err := strconv.ParseInt(trimSpace(s), 10, 64)
	return n, err == nil
}

// RoleOwnBuffs is source 1, cached per role: CharacterInitInfo, or the autobuff fallback for
// 3.8 roles missing from CharacterInitInfo (specs/roles.md §5.3).
func (b *RoleBuffBuilder) RoleOwnBuffs(roleID int32) []InitialBuffSpec {
	b.mu.Lock()
	if c, ok := b.ownBuffs[roleID]; ok {
		b.mu.Unlock()
		return c
	}
	b.mu.Unlock()
	t := b.tables
	var list []InitialBuffSpec
	seen := map[int64]bool{}
	if init, ok := t.CharacterInitInfoByRoleId[roleID]; ok && len(init.FightBuffs) > 0 {
		for _, ids := range [][]int64{init.FightBuffs, init.AdditionalFightBuffs} {
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				if buff, ok := b.buff(id); ok {
					list = append(list, InitialBuffSpec{id, StartsActiveAtSpawn(buff)})
				}
			}
		}
	} else {
		prefix := strconv.Itoa(int(roleID))
		var candidates []*data.Buff
	buffs:
		for _, buff := range t.Buff {
			idStr := strconv.FormatInt(int64(buff.Id), 10)
			if len(idStr) <= 4 || !strings.HasPrefix(idStr, prefix) {
				continue
			}
			if buff.DurationPolicy != 1 { // 1: infinite
				continue
			}
			if len(buff.ApplicationSourceTagRequirements) > 0 || len(buff.ApplicationTagRequirements) > 0 || len(buff.PrematureExpirationEffects) > 0 {
				continue
			}
			suffix := idStr[4:]
			for _, p := range autoBuffExcludedSuffixPrefixes {
				if strings.HasPrefix(suffix, p) {
					continue buffs
				}
			}
			for _, tag := range buff.GrantedTags {
				for _, sub := range autoBuffExcludedTagSubstrings {
					if strings.Contains(tag, sub) {
						continue buffs
					}
				}
			}
			if buff.ExtraEffectID == 5 {
				if len(buff.ExtraEffectParameters) <= 1 {
					continue
				}
				p1 := buff.ExtraEffectParameters[1]
				if h := strings.IndexByte(p1, '#'); h >= 0 {
					p1 = p1[:h]
				}
				if ref, ok := parseInteger(p1); ok {
					if rb, ok := b.buff(ref); ok && (len(rb.CalculationPolicy) == 0 || rb.CalculationPolicy[0] == 0) {
						continue
					}
				}
			}
			candidates = append(candidates, buff)
		}

		excluded := map[int64]bool{}
		for id := range b.honamiIDs {
			excluded[id] = true
		}
		for id := range b.comboIDs {
			excluded[id] = true
		}
		for _, ch := range t.ResonantChain {
			if ch.GroupId == roleID {
				for _, raw := range ch.BuffIds {
					excluded[int64(raw)] = true
				}
			}
		}
		for _, r := range t.RogueResCharacterBuff {
			if r.BuffId != 0 && strings.HasPrefix(strconv.FormatInt(int64(r.BuffId), 10), prefix) {
				excluded[int64(r.BuffId)] = true
			}
			for _, raw := range r.BuffIds {
				if id := int64(raw); id != 0 && strings.HasPrefix(strconv.FormatInt(id, 10), prefix) {
					excluded[id] = true
				}
			}
		}
		for _, s := range t.SkillBranchBuff {
			if s.RoleId == roleID {
				for _, id := range s.Buffs {
					excluded[id] = true
				}
			}
		}
		for _, c := range candidates {
			if c.ExtraEffectID == 6 && len(c.ExtraEffectParameters) > 3 {
				for _, part := range strings.Split(c.ExtraEffectParameters[3], "#") {
					if part = trimSpace(part); part != "" {
						if id, ok := parseInteger(part); ok {
							excluded[id] = true
						}
					}
				}
			}
		}
		for _, c := range candidates {
			id := int64(c.Id)
			if !excluded[id] && !seen[id] {
				seen[id] = true
				list = append(list, InitialBuffSpec{id, StartsActiveAtSpawn(c)})
			}
		}
	}
	b.mu.Lock()
	b.ownBuffs[roleID] = list
	b.mu.Unlock()
	return list
}

// BuildForRole assembles the 11 sources of a role entity's first buffs (specs/roles.md §5.3):
// the buffs sorted by id and the ornament UI buffs the client applies.
func (b *RoleBuffBuilder) BuildForRole(player *save.PlayerData, role *save.RoleSave, snap *RoleAttributeSnapshot) (buffs []InitialBuffSpec, needClientApply []int64) {
	t := b.tables
	m := map[int64]bool{}
	add := func(id int64, active bool) {
		if id == 0 {
			return
		}
		if cur, ok := m[id]; !ok || active {
			m[id] = cur || active
		}
	}
	// 1. The role's own buffs.
	for _, s := range b.RoleOwnBuffs(role.RoleID) {
		add(s.BuffID, s.IsActive)
	}
	// 2. The common fixed buffs.
	for _, id := range commonFixedBuffs {
		add(id, true)
	}
	// 3. Skill branch buffs.
	if role.SkillBranch != 0 {
		if br, ok := t.SkillBranchBuffByBranchId[role.SkillBranch]; ok {
			for _, id := range br.Buffs {
				add(id, true)
			}
		}
	}
	// 4. The Rover's gender tag buff.
	if mr, ok := t.MainRoleConfigById[role.RoleID]; ok {
		if mr.Gender == 1 {
			add(roverMaleGenderBuff, true)
		} else {
			add(roverFemaleGenderBuff, true)
		}
	}
	// 5. The Concerto energy buff, but for the exempt roles.
	if !concertoExemptRoles[role.RoleID] {
		add(concertoEnergyBuff, true)
	}
	// 6. The equipped weapon's refinement buffs.
	if w := player.EquippedWeapon(role); w != nil {
		if wc, ok := t.WeaponConfByItemId[w.ID]; ok {
			for _, wr := range t.WeaponReson {
				if wr.ResonId == wc.ResonId && wr.Level == w.ResonLevel {
					for _, raw := range wr.Effect {
						add(int64(raw), true)
					}
					break
				}
			}
		}
	}
	// 7. Element buffs, 8. unlocked resonance chain buffs.
	if ri, ok := t.RoleInfoById[role.RoleID]; ok {
		if el := int64(ri.ElementId); el > 0 {
			add(1101007999+el, true)
			add(1001001015+el*2, true)
			add(3080+el, true)
		}
		if role.ResonantChainGroupIndex > 0 {
			group := ri.ResonantChainGroupId
			if group == 0 {
				group = role.RoleID
			}
			for _, ch := range t.ResonantChain {
				if ch.GroupId == group && ch.GroupIndex <= role.ResonantChainGroupIndex {
					for _, raw := range ch.BuffIds {
						add(int64(raw), true)
					}
				}
			}
		}
	}
	// 9. The equipped ornaments' buffs and UI buffs.
	for _, o := range role.Ornaments {
		if o.RoleSkinID != role.RoleSkinID {
			continue
		}
		cfg, ok := t.OrnamentById[o.OrnamentID]
		if !ok {
			continue
		}
		for _, raw := range cfg.OrnamentBuff {
			add(int64(raw), true)
		}
		for _, raw := range cfg.OrnamentUiBuff {
			if id := int64(raw); id != 0 && !slices.Contains(needClientApply, id) {
				needClientApply = append(needClientApply, id)
			}
		}
	}
	// 10. The active echo and Sonata buffs.
	for _, id := range snap.AllEchoAndFetterBuffIDs {
		add(id, true)
	}
	// 11. Without the custom concomitants' born buffs, sorted by id.
	born := map[int64]bool{}
	for _, c := range b.concomitants.CustomConcomitants(role.RoleID) {
		for _, id := range c.BornBuffIDs {
			born[id] = true
		}
	}
	for id, active := range m {
		if !born[id] {
			buffs = append(buffs, InitialBuffSpec{id, active})
		}
	}
	slices.SortFunc(buffs, func(a, b InitialBuffSpec) int {
		switch {
		case a.BuffID < b.BuffID:
			return -1
		case a.BuffID > b.BuffID:
			return 1
		}
		return 0
	})
	if needClientApply == nil {
		needClientApply = []int64{}
	}
	return buffs, needClientApply
}

// ----- concomitants and echo summons (specs/roles.md §5.4) -----

type ConcomitantSpec struct {
	SummonCfgID   int32
	TemplateID    int32
	BlueprintType string
	ModelID       int32
	SpawnCount    int32
	BornBuffIDs   []int64
}

type VisionSummonSpec struct {
	SummonSkillID int32
	TemplateID    int32
	BlueprintType string
	ModelID       int32
	Level         int32
	Quality       int32
	BornBuffIDs   []int64
}

// ConcomitantResolver finds a role's custom concomitants and its main and combo echo summons,
// cached per role (R-131).
type ConcomitantResolver struct {
	tables          *data.DataTables
	mu              sync.Mutex
	custom          map[int32][]ConcomitantSpec
	templateByBp    map[string]*data.TemplateConfig
	blueprintByType map[string]*data.BlueprintConfig
	summonByBp      map[string]*data.SummonCfg
}

func NewConcomitantResolver(t *data.DataTables) *ConcomitantResolver {
	c := &ConcomitantResolver{tables: t, custom: map[int32][]ConcomitantSpec{}, templateByBp: map[string]*data.TemplateConfig{},
		blueprintByType: map[string]*data.BlueprintConfig{}, summonByBp: map[string]*data.SummonCfg{}}
	for _, r := range t.TemplateConfig {
		if _, ok := c.templateByBp[r.BlueprintType]; r.BlueprintType != "" && !ok {
			c.templateByBp[r.BlueprintType] = r
		}
	}
	for _, r := range t.BlueprintConfig {
		if _, ok := c.blueprintByType[r.BlueprintType]; r.BlueprintType != "" && !ok {
			c.blueprintByType[r.BlueprintType] = r
		}
	}
	for _, r := range t.SummonCfg {
		if _, ok := c.summonByBp[r.BlueprintType]; r.BlueprintType != "" && !ok {
			c.summonByBp[r.BlueprintType] = r
		}
	}
	return c
}

func SpawnCountForRole(roleID int32) int32 {
	switch roleID {
	case 1105:
		return 3
	case 1301:
		return 4
	}
	return 1
}

func bornBuffs(s *data.SummonCfg) []int64 {
	out := []int64{}
	for _, x := range s.BornBuffId {
		if id := int64(x); id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// CustomConcomitants are a role's custom concomitant templates (specs/roles.md §5.4).
func (c *ConcomitantResolver) CustomConcomitants(roleID int32) []ConcomitantSpec {
	c.mu.Lock()
	if v, ok := c.custom[roleID]; ok {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()
	t := c.tables
	var list []ConcomitantSpec
	var firstSkin *data.RoleSkin
	for _, s := range t.RoleSkin {
		if s.RoleId == roleID {
			firstSkin = s
			break
		}
	}
	if firstSkin != nil && firstSkin.UiScenePerformanceABP != "" {
		parts := strings.Split(firstSkin.UiScenePerformanceABP, "/")
		if len(parts) > 6 && parts[6] != "" {
			modelName := strings.ToUpper(parts[6])
			seen := map[string]bool{}
			count := SpawnCountForRole(roleID)
			for _, bp := range t.BlueprintConfig {
				if bp.EntityType != "Monster" || !strings.Contains(bp.BlueprintType, "Player") || !strings.Contains(bp.BlueprintType, "_") {
					continue
				}
				if seen[bp.BlueprintType] {
					continue
				}
				seen[bp.BlueprintType] = true
				model, ok := t.ModelConfigPreloadById[bp.ModelId]
				if !ok || !strings.Contains(strings.ToUpper(model.ActorClassPath), modelName) {
					continue
				}
				summon, ok := c.summonByBp[bp.BlueprintType]
				if !ok {
					continue
				}
				tpl, ok := c.templateByBp[bp.BlueprintType]
				if !ok {
					continue
				}
				list = append(list, ConcomitantSpec{summon.Id, tpl.Id, bp.BlueprintType, bp.ModelId, count, bornBuffs(summon)})
			}
		}
	}
	c.mu.Lock()
	c.custom[roleID] = list
	c.mu.Unlock()
	return list
}

// ResolveVisionSummons are the main (slot 0) and the combo (PhantomSummonGroup) echo summons of
// a role's equipped echoes (specs/roles.md §5.4).
func (c *ConcomitantResolver) ResolveVisionSummons(player *save.PlayerData, roleID int32) (main, combo *VisionSummonSpec) {
	slots := player.EquippedEchoes(roleID)
	if slots[0] != nil {
		main = c.resolveSingle(slots[0])
	}
	byMonster := map[int32]*save.EchoSave{}
	for _, e := range slots {
		if e == nil {
			continue
		}
		if item, ok := c.tables.PhantomItemByItemId[e.ID]; ok {
			if _, dup := byMonster[item.MonsterId]; !dup {
				byMonster[item.MonsterId] = e
			}
		}
	}
	for _, g := range c.tables.PhantomSummonGroup {
		if len(g.GroupMonsterIds) == 0 {
			continue
		}
		all := true
		for _, mid := range g.GroupMonsterIds {
			if byMonster[mid] == nil {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		for _, mid := range g.GroupMonsterIds {
			if cand := c.resolveSingle(byMonster[mid]); cand != nil && (main == nil || cand.SummonSkillID != main.SummonSkillID) {
				combo = cand
				break
			}
		}
		if combo != nil {
			break
		}
	}
	return main, combo
}

func (c *ConcomitantResolver) VisionTopology(player *save.PlayerData, roleID int32) (mainID, comboID int32) {
	main, combo := c.ResolveVisionSummons(player, roleID)
	if main != nil {
		mainID = main.SummonSkillID
	}
	if combo != nil {
		comboID = combo.SummonSkillID
	}
	return
}

func (c *ConcomitantResolver) BuildVisionSkillComponent(main *VisionSummonSpec, mainEntity int64, combo *VisionSummonSpec, comboEntity int64) *protocol.VisionSkillComponentPb {
	pb := &protocol.VisionSkillComponentPb{}
	if main != nil {
		info := &protocol.VisionSkillInformation{SkillId: main.SummonSkillID, Level: main.Level, Quality: main.Quality, VisionEntityId: mainEntity, Index: 0}
		pb.VisionSkillInfos = append(pb.VisionSkillInfos, info)
		pb.PhantomSkillInfo = proto.Clone(info).(*protocol.VisionSkillInformation)
	}
	if combo != nil {
		pb.VisionSkillInfos = append(pb.VisionSkillInfos, &protocol.VisionSkillInformation{
			SkillId: combo.SummonSkillID, Level: combo.Level, Quality: combo.Quality, VisionEntityId: comboEntity, Index: 1,
		})
	}
	return pb
}

func (c *ConcomitantResolver) resolveSingle(e *save.EchoSave) *VisionSummonSpec {
	eff := EffectivePhantomItem(c.tables, e)
	if eff == nil || eff.SkillId == 0 {
		return nil
	}
	summon, ok := c.tables.SummonCfgById[eff.SkillId]
	if !ok {
		return nil
	}
	tpl, ok := c.templateByBp[summon.BlueprintType]
	if !ok {
		return nil
	}
	var model int32
	if bp, ok := c.blueprintByType[summon.BlueprintType]; ok {
		model = bp.ModelId
	}
	return &VisionSummonSpec{eff.SkillId, tpl.Id, summon.BlueprintType, model, max(1, e.Level), eff.QualityId, bornBuffs(summon)}
}
