// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"slices"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/protocol"
)

const (
	// PassiveContextID is the context id of passive GA skills; the client only checks it is not 0.
	PassiveContextID = -1
	// AiContextID is MonsterAiComponentPb.CombatMessageId.
	AiContextID = 1
	// FixedRoleCommonSkillID is a role's fixed common skill.
	FixedRoleCommonSkillID = 100005
)

// PassiveSkillsIndex is the passive GA skills of AI entities (R-122) and the role
// SkillComponentPb (specs/roles.md §5.2).
type PassiveSkillsIndex struct {
	tables     *data.DataTables
	byActor    map[string][]int32
	commonRole []int32
}

func NewPassiveSkillsIndex(t *data.DataTables) *PassiveSkillsIndex {
	p := &PassiveSkillsIndex{tables: t, byActor: map[string][]int32{}}
	seenCommon := map[int32]bool{}
	for _, r := range t.EntitySkillPreload {
		if r.SkillId == 0 {
			continue
		}
		if r.IsCommon && !seenCommon[r.SkillId] {
			seenCommon[r.SkillId] = true
			p.commonRole = append(p.commonRole, r.SkillId)
		}
		if r.ActorBlueprint == "" {
			continue
		}
		if !slices.Contains(p.byActor[r.ActorBlueprint], r.SkillId) {
			p.byActor[r.ActorBlueprint] = append(p.byActor[r.ActorBlueprint], r.SkillId)
		}
	}
	return p
}

func (p *PassiveSkillsIndex) SkillsForActor(path string) []int32 { return p.byActor[path] }

func (p *PassiveSkillsIndex) SkillsForModel(modelID int32) []int32 {
	if modelID == 0 {
		return nil
	}
	m, ok := p.tables.ModelConfigPreloadById[modelID]
	if !ok {
		return nil
	}
	return p.SkillsForActor(m.ActorClassPath)
}

type skillAdder struct {
	pb   *protocol.SkillComponentPb
	seen map[int64]bool
}

func (a *skillAdder) add(id int64) {
	if id != 0 && !a.seen[id] {
		a.seen[id] = true
		a.pb.GaPassiveSkills = append(a.pb.GaPassiveSkills, &protocol.GaPassiveSkill{SkillId: id, ContextId: PassiveContextID})
	}
}

func newSkillAdder() *skillAdder {
	return &skillAdder{&protocol.SkillComponentPb{}, map[int64]bool{}}
}

func BuildSkillsForIDs(ids []int32) *protocol.SkillComponentPb {
	a := newSkillAdder()
	for _, id := range ids {
		a.add(int64(id))
	}
	return a.pb
}

// BuildForRole is a player role entity's SkillComponentPb (specs/roles.md §5.2).
func (p *PassiveSkillsIndex) BuildForRole(roleID int32, roleBuffIDs []int64, visionModelIDs []int32, vision *protocol.VisionSkillComponentPb) *protocol.SkillComponentPb {
	a := newSkillAdder()
	// 1. Every EntitySkillPreload.SkillId whose decimal text starts with the role id.
	prefix := strconv.Itoa(int(roleID))
	for _, r := range p.tables.EntitySkillPreload {
		if r.SkillId != 0 && strings.HasPrefix(strconv.Itoa(int(r.SkillId)), prefix) {
			a.add(int64(r.SkillId))
		}
	}
	// 2. The '|'-separated skill ids of ExtraEffectParameters[0] of the buffs with ExtraEffectID 35.
	for _, id := range roleBuffIDs {
		b, ok := p.tables.BuffById[float64(id)]
		if !ok || b.ExtraEffectID != 35 || len(b.ExtraEffectParameters) == 0 {
			continue
		}
		for _, tok := range strings.Split(b.ExtraEffectParameters[0], "|") {
			if tok = trimSpace(tok); tok != "" {
				if sid, ok := parseInteger(tok); ok {
					a.add(sid)
				}
			}
		}
	}
	// 3. The fixed skill.
	a.add(FixedRoleCommonSkillID)
	// 4. The echo summons' skills and the vision skills.
	for _, m := range visionModelIDs {
		for _, sid := range p.SkillsForModel(m) {
			a.add(int64(sid))
		}
	}
	if vision != nil {
		for _, vs := range vision.VisionSkillInfos {
			a.add(int64(vs.SkillId))
		}
		if vision.PhantomSkillInfo != nil {
			a.add(int64(vision.PhantomSkillInfo.SkillId))
		}
	}
	// 5. The common role skills.
	for _, sid := range p.commonRole {
		a.add(int64(sid))
	}
	return a.pb
}

// ----- role develop (R-123, specs/roles.md §6.12) -----

const RoleDevelopVersion = "tidelab-3.8-1"

func BuildRoleDevelopConfigs(list []*data.RoleDevProsList, projects []*data.RoleDevProsProject) *protocol.RoleDevelopConfigs {
	c := &protocol.RoleDevelopConfigs{Version: RoleDevelopVersion}
	for _, r := range list {
		item := &protocol.RoleDevPropsConfig{Id: r.Id, TypeId: r.TypeId, GachaId: r.GachaId, SortId: r.SortId}
		for _, p := range r.SpecialGachaId {
			if p != nil {
				item.SpecialGachaPair = append(item.SpecialGachaPair, &protocol.SpecialGachaPair{TypeId: p.Item1, GachaId: p.Item2})
			}
		}
		c.DevPropsList = append(c.DevPropsList, item)
	}
	for _, r := range projects {
		c.DevPropsProjectList = append(c.DevPropsProjectList, &protocol.RoleDevPropsProjectConfig{
			Id: r.Id, ElementId: r.ElementId, RoleName: r.RoleName, RoleExperience: r.RoleExperience,
			RoleGoalLevel: r.RoleGoalLevel, WeaponGoalLevel: r.WeaponGoalLevel, WeaponExperience: r.WeaponExperience,
			WeaponType: r.WeaponType, RoleHeadIcon: r.RoleHeadIcon, RoleHeadIconSmall: r.RoleHeadIconSmall,
			FormationRoleCard:     r.FormationRoleCard,
			RoleItemGroup:         slices.Clone(r.RoleItemGroup),
			WeaponBreachItemGroup: slices.Clone(r.WeaponBreachItemGroup),
			SkillItemGroup:        slices.Clone(r.SkillItemGroup),
			PrefectSkillLevel:     slices.Clone(r.PrefectSkillLevel),
		})
	}
	return c
}

func HandleRoleDevelopConfig(t *data.DataTables, r *protocol.RoleDevelopConfigRequest) *protocol.RoleDevelopConfigResponse {
	version := ""
	if r.Version != nil {
		version = *r.Version
	}
	c := BuildRoleDevelopConfigs(t.RoleDevProsList, t.RoleDevProsProject)
	common.Info("role_develop", "configs: "+strconv.Itoa(len(c.DevPropsList))+" hot list rows, "+strconv.Itoa(len(c.DevPropsProjectList))+
		" plans (client had version \""+version+"\")")
	return &protocol.RoleDevelopConfigResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Configs: c}
}

func HandleTeamRecommend(*protocol.TeamRecommendRequest) *protocol.TeamRecommendResponse {
	return &protocol.TeamRecommendResponse{}
}
