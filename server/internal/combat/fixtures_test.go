// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Code generated from our C# R143gTestFixtures.BuildSyntheticTables by a one-off tool (R-144e). DO NOT EDIT.

package combat

import "tidelab/internal/data"

// syntheticTables is the C# tests' synthetic DataTables covering the R-143g rules.
func syntheticTables() *data.DataTables {
	t := &data.DataTables{}
	t.RoleInfo = []*data.RoleInfo{
		&data.RoleInfo{Id: 1211, PropertyId: 100, ElementId: 2, SkinId: 81001211, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 41100011, Value: 10}}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1211, DefaultSkillBranchId: 121101, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1108, PropertyId: 100, ElementId: 1, SkinId: 81001108, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 11, ResonantChainGroupId: 1108, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1506, PropertyId: 100, ElementId: 5, SkinId: 81001506, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 12, ResonantChainGroupId: 1506, InitWeaponItemId: 21020074},
		&data.RoleInfo{Id: 1501, PropertyId: 100, ElementId: 5, SkinId: 81001501, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1502, PropertyId: 100, ElementId: 5, SkinId: 81001502, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1604, PropertyId: 100, ElementId: 6, SkinId: 81001604, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1604, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1605, PropertyId: 100, ElementId: 6, SkinId: 81001605, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1604, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1105, PropertyId: 100, ElementId: 1, SkinId: 81001105, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1105, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 1301, PropertyId: 100, ElementId: 3, SkinId: 81001301, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 90, SkillTreeGroupId: 10, ResonantChainGroupId: 1301, InitWeaponItemId: 21010011},
		&data.RoleInfo{Id: 9001, PropertyId: 100, ElementId: 1, SpilloverItem: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, MaxLevel: 1, InitWeaponItemId: 21010011},
	}
	t.MainRoleConfig = []*data.MainRoleConfig{
		&data.MainRoleConfig{Id: 1501, Gender: 1},
		&data.MainRoleConfig{Id: 1502},
		&data.MainRoleConfig{Id: 1604, Gender: 1},
		&data.MainRoleConfig{Id: 1605},
	}
	t.BaseProperty = []*data.BaseProperty{
		&data.BaseProperty{Id: 100, Lv: 1, LifeMax: 1000, Life: 1000, Atk: 100, Crit: 500, CritDamage: 15000, Def: 100, EnergyMax: 12500, StrengthMax: 10000, Strength: 10000, HardnessMax: 1000, Hardness: 1000, HardnessRecover: 50, RageMax: 1000, RageRecover: 10, ToughMax: 100, Tough: 100, ToughRecover: 5, AutoAttackSpeed: 10000, CastAttackSpeed: 10000, ElementEnergyMax: 10000, ElementEfficiency: 10000},
		&data.BaseProperty{Id: 2, Lv: 1, LifeMax: 2000, Life: 2000, Atk: 50, Def: 200, DamageResistancePhys: 1000, DamageResistanceElement1: 2000, HardnessMax: 500, Hardness: 500, HardnessRecover: 20, RageMax: 100, RageRecover: 5, WeaknessBuildUpMax: 100},
		&data.BaseProperty{Id: 999, Lv: 1, LifeMax: 500000, Life: 500000, Atk: 1000, Def: 500},
	}
	t.RolePropertyGrowth = []*data.RolePropertyGrowth{
		&data.RolePropertyGrowth{Id: 1, Level: 90, BreachLevel: 6, LifeMaxRatio: 103333, AtkRatio: 41250, DefRatio: 125800},
	}
	t.MonsterPropertyGrowth = []*data.MonsterPropertyGrowth{
		&data.MonsterPropertyGrowth{Id: 1, Level: 50, LifeMaxRatio: 25000, AtkRatio: 15000, DefRatio: 20000, HardnessMaxRatio: 10000, HardnessRatio: 10000, HardnessRecoverRatio: 10000, RageMaxRatio: 10000, RageRatio: 10000, RageRecoverRatio: 10000, WeaknessBuildUpMaxRatio: 10000},
		&data.MonsterPropertyGrowth{Id: 2, CurveId: 9, Level: 100, LifeMaxRatio: 100000000, AtkRatio: 10000, DefRatio: 10000},
	}
	t.Buff = []*data.Buff{
		&data.Buff{Id: 200001001, DurationPolicy: 1, GrantedTags: []string{"Echo.Active.Main"}},
		&data.Buff{Id: 80010, DurationPolicy: 1, GameAttributeID: 7, CalculationPolicy: []int32{0}, ModifierMagnitude: []int32{150}, OngoingTagRequirements: []string{"Echo.Active"}},
		&data.Buff{Id: 80001, DurationPolicy: 1, GameAttributeID: 10, CalculationPolicy: []int32{0}, ModifierMagnitude: []int32{200}},
		&data.Buff{Id: 500001, GameAttributeID: 3, CalculationPolicy: []int32{0}, ModifierMagnitude: []int32{300}},
		&data.Buff{Id: 500002, DurationPolicy: 2, DurationMagnitude: []float32{2}, DefaultStackCount: 1, StackAppendCount: 1, StackLimitCount: 3, StackExpirationRemoveNumber: 1, RoutineExpirationEffects: []float64{500001}},
		&data.Buff{Id: 500003, DurationPolicy: 2, DurationMagnitude: []float32{3}, Period: 1, GameAttributeID: 3, CalculationPolicy: []int32{0}, ModifierMagnitude: []int32{-100}},
		&data.Buff{Id: 500004, GameAttributeID: 7, CalculationPolicy: []int32{3}, ModifierMagnitude: []int32{777}},
		&data.Buff{Id: 500005, GameAttributeID: 7, CalculationPolicy: []int32{1}, ModifierMagnitude: []int32{2000}},
		&data.Buff{Id: 500006, GameAttributeID: 7, CalculationPolicy: []int32{2, 2, 0, 1, 0}, ModifierMagnitude: []int32{5000}, ModifierMagnitude2: []int32{10}},
		&data.Buff{Id: 12110001, DurationPolicy: 1, ExtraEffectID: 35, ExtraEffectParameters: []string{"901|902"}},
		&data.Buff{Id: 91105, DurationPolicy: 1},
		&data.Buff{Id: 1108001, DurationPolicy: 1},
		&data.Buff{Id: 11086661, DurationPolicy: 1},
		&data.Buff{Id: 1108002, DurationPolicy: 1, GrantedTags: []string{"Rogue.Test"}},
		&data.Buff{Id: 50001, DurationPolicy: 1},
		&data.Buff{Id: 50002, DurationPolicy: 1},
		&data.Buff{Id: 50003, DurationPolicy: 1},
		&data.Buff{Id: 60001, DurationPolicy: 1},
		&data.Buff{Id: 60002, DurationPolicy: 1},
		&data.Buff{Id: 60003, DurationPolicy: 1},
		&data.Buff{Id: 70001, DurationPolicy: 1},
		&data.Buff{Id: 70002, DurationPolicy: 1},
	}
	t.SummonCfg = []*data.SummonCfg{
		&data.SummonCfg{Id: 200001, BlueprintType: "VisionBP_1", BornBuffId: []float64{90001}},
		&data.SummonCfg{Id: 200002, BlueprintType: "VisionBP_2", BornBuffId: []float64{90002}},
		&data.SummonCfg{Id: 9901, BlueprintType: "Player1105_1", BornBuffId: []float64{91105}},
		&data.SummonCfg{Id: 9902, BlueprintType: "Player1301_1", BornBuffId: []float64{91301}},
	}
	t.ModelConfigPreload = []*data.ModelConfigPreload{
		&data.ModelConfigPreload{Id: 701, ActorClassPath: "/Game/Vision1"},
		&data.ModelConfigPreload{Id: 71105, ActorClassPath: "/Game/Role/Zhezhi/BP_Zhezhi_Summon"},
		&data.ModelConfigPreload{Id: 71301, ActorClassPath: "/Game/Role/Calcharo/BP_Calcharo_Summon"},
	}
	t.CharacterPartConfig = []*data.CharacterPartConfig{
		&data.CharacterPartConfig{ModelId: 8001, Parts: []*data.CharacterPartConfig_Parts{{Index: 1, Name: "Horn", LifeRatio: 0.25, BirthActivated: true, PartTagId: 111, ActiveTagId: 222, CombineSocket: "Socket_Horn"}, {Index: 2, Name: "Tail", PartTagId: 112, ActiveTagId: 223}}},
	}
	t.BlueprintConfig = []*data.BlueprintConfig{
		&data.BlueprintConfig{Id: 1, BlueprintType: "VisionBP_1", EntityType: "Vision", EntityLogic: "Vision", ModelId: 701},
		&data.BlueprintConfig{Id: 2, BlueprintType: "VisionBP_2", EntityType: "Vision", EntityLogic: "Vision", ModelId: 702},
		&data.BlueprintConfig{Id: 3, BlueprintType: "Player1105_1", EntityType: "Monster", EntityLogic: "Monster", ModelId: 71105},
		&data.BlueprintConfig{Id: 4, BlueprintType: "Player1301_1", EntityType: "Monster", EntityLogic: "Monster", ModelId: 71301},
	}
	t.RoleSkin = []*data.RoleSkin{
		&data.RoleSkin{Id: 81001105, RoleId: 1105, UiScenePerformanceABP: "/Game/Aki/Character/Role/FemaleM/Zhezhi/R2T1Zhezhi/ABP"},
		&data.RoleSkin{Id: 81001301, RoleId: 1301, UiScenePerformanceABP: "/Game/Aki/Character/Role/MaleXL/Calcharo/R2T1Calcharo/ABP"},
	}
	t.TemplateConfig = []*data.TemplateConfig{
		&data.TemplateConfig{Id: 30001, BlueprintType: "VisionBP_1", ComponentsData: mustJSON("null")},
		&data.TemplateConfig{Id: 30002, BlueprintType: "VisionBP_2", ComponentsData: mustJSON("null")},
		&data.TemplateConfig{Id: 31105, BlueprintType: "Player1105_1", ComponentsData: mustJSON("null")},
		&data.TemplateConfig{Id: 31301, BlueprintType: "Player1301_1", ComponentsData: mustJSON("null")},
	}
	t.WeaponConf = []*data.WeaponConf{
		&data.WeaponConf{ItemId: 21010011, QualityId: 5, ResonLevelLimit: 5, FirstPropId: &data.ConfigPropValue{Id: 10007, Value: 47}, FirstCurve: 1, SecondPropId: &data.ConfigPropValue{Id: 10007, Value: 0.081, IsRatio: true}, SecondCurve: 2, ResonId: 101, LevelId: 1, BreachId: 1},
		&data.WeaponConf{ItemId: 21020074, QualityId: 4, ResonLevelLimit: 5, FirstPropId: &data.ConfigPropValue{Id: 10007, Value: 37}, FirstCurve: 1, SecondPropId: &data.ConfigPropValue{Id: 8, Value: 0.081, IsRatio: true}, SecondCurve: 2, ResonId: 102, LevelId: 1, BreachId: 1},
		&data.WeaponConf{ItemId: 21020053, QualityId: 4, ResonLevelLimit: 5, FirstPropId: &data.ConfigPropValue{Id: 10007, Value: 30}, FirstCurve: 1, ResonId: 102, LevelId: 1, BreachId: 1},
	}
	t.WeaponBaseAttributes = []*data.WeaponBaseAttributes{
		&data.WeaponBaseAttributes{ItemId: 21010011, Attributes: []int32{2}, Values: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 1, Value: 500}, {Key: 2, Value: 700}, {Key: 5, Value: 1000}}}},
		&data.WeaponBaseAttributes{ItemId: 21020074, IsRatio: true, Attributes: []int32{7}, Values: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 1, Value: 1200}, {Key: 2, Value: 1500}}}},
	}
	t.WeaponPropertyGrowth = []*data.WeaponPropertyGrowth{
		&data.WeaponPropertyGrowth{Id: 1, CurveId: 1, Level: 1, CurveValue: 10000},
		&data.WeaponPropertyGrowth{Id: 2, CurveId: 2, Level: 1, CurveValue: 10000},
		&data.WeaponPropertyGrowth{Id: 3, CurveId: 1, Level: 90, BreachLevel: 6, CurveValue: 125000},
		&data.WeaponPropertyGrowth{Id: 4, CurveId: 2, Level: 90, BreachLevel: 6, CurveValue: 45000},
	}
	t.WeaponReson = []*data.WeaponReson{
		&data.WeaponReson{Id: 1, ResonId: 101, Level: 1, Effect: []float64{50001}},
		&data.WeaponReson{Id: 2, ResonId: 101, Level: 2, Effect: []float64{50002}},
		&data.WeaponReson{Id: 3, ResonId: 102, Level: 1, Effect: []float64{50003}},
	}
	t.WeaponExpItem = []*data.WeaponExpItem{
		&data.WeaponExpItem{Id: 43020001, BasicExp: 150},
	}
	t.WeaponLevel = []*data.WeaponLevel{
		&data.WeaponLevel{Id: 1, LevelId: 1, Level: 1, Exp: 100},
		&data.WeaponLevel{Id: 2, LevelId: 1, Level: 2, Exp: 200},
		&data.WeaponLevel{Id: 3, LevelId: 1, Level: 3, Exp: 300},
	}
	t.WeaponBreach = []*data.WeaponBreach{
		&data.WeaponBreach{Id: 1, BreachId: 1, LevelLimit: 20, Consume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 41100011, Value: 5}}}},
		&data.WeaponBreach{Id: 2, BreachId: 1, Level: 1, LevelLimit: 40, Consume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 41100011, Value: 10}}}},
	}
	t.LevelEntityConfig = []*data.LevelEntityConfig{
		&data.LevelEntityConfig{Id: 1, MapId: 8, EntityId: 1001, BlueprintType: "Teleport001", Transform: []*data.IntVector{{X: 100000, Y: 200000, Z: 50000}}, ComponentsData: mustJSON("null")},
		&data.LevelEntityConfig{Id: 2, MapId: 8, EntityId: 1002, BlueprintType: "branch2.0_Teleport6", Transform: []*data.IntVector{{X: 1000000, Y: 200000, Z: 50000}}, ComponentsData: mustJSON("null")},
		&data.LevelEntityConfig{Id: 3, MapId: 8, EntityId: 1003, BlueprintType: "Teleport002", IsHidden: true, Transform: []*data.IntVector{{}}, ComponentsData: mustJSON("null")},
	}
	t.Damage = []*data.Damage{
		&data.Damage{Id: 1001, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, RateLv: []int32{10000, 15000}, ElementPower: []int32{500}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 1002, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, Element: 1, Type: 2, RelatedProperty: 10, RateLv: []int32{20000}, ElementPower: []int32{1000}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 2000, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, CalculateType: 1, CureBaseValue: []int32{250}, RateLv: []int32{1000}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 2002, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, CalculateType: 1, FormulaType: 2, FormulaParam1: []int32{2}, FormulaParam2: []int32{1}, FormulaParam3: []int32{8000}, FormulaParam4: []int32{100}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 2003, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, CalculateType: 1, FormulaType: 3, FormulaParam1: []int32{7}, FormulaParam2: []int32{0}, FormulaParam3: []int32{5000}, FormulaParam4: []int32{50}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 2005, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, CalculateType: 1, FormulaType: 5, FormulaParam1: []int32{2500}, FormulaParam2: []int32{100}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 2006, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, CalculateType: 1, FormulaType: 6, FormulaParam1: []int32{7}, FormulaParam2: []int32{0}, FormulaParam3: []int32{2000}, FormulaParam4: []int32{2}, FormulaParam5: []int32{1}, FormulaParam6: []int32{1000}, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Damage{Id: 3000, ConstVariables: &data.Map[string, string]{Pairs: []data.Pair[string, string]{}}, CalculateType: 2, Element: 3, WeaknessRatio: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
	}
	t.Area = []*data.Area{
		&data.Area{AreaId: 1, WorldMonsterLevelMax: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 4, Value: 48}, {Key: 8, Value: 88}}}, EnterAreaTags: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, LeaveAreaTags: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Area{AreaId: 2, Father: 1, WorldMonsterLevelMax: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, EnterAreaTags: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, LeaveAreaTags: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.Area{AreaId: 3, WorldMonsterLevelMax: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, EnterAreaTags: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, LeaveAreaTags: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
	}
	t.Skill = []*data.Skill{
		&data.Skill{Id: 1001, MaxSkillLevel: 10},
		&data.Skill{Id: 1002, MaxSkillLevel: 5},
	}
	t.SkillTree = []*data.SkillTree{
		&data.SkillTree{Id: 101, NodeGroup: 10, SkillId: 1001, Consume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.SkillTree{Id: 102, NodeGroup: 10, Condition: []int32{101}, Property: []*data.ConfigPropValue{{Id: 10007, Value: 0.04, IsRatio: true}}, Consume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}, UnLockCondition: 1},
		&data.SkillTree{Id: 103, NodeGroup: 11, SkillId: 1002, Consume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
	}
	t.ResonantChain = []*data.ResonantChain{
		&data.ResonantChain{Id: 1, GroupId: 1211, GroupIndex: 1, BuffIds: []float64{60001}, AddProp: []*data.ConfigPropValue{{Id: 8, Value: 500}}, ActivateConsume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 41100011, Value: 2}}}},
		&data.ResonantChain{Id: 2, GroupId: 1211, GroupIndex: 2, BuffIds: []float64{60002}, AddProp: []*data.ConfigPropValue{{Id: 10007, Value: 0.1, IsRatio: true}}, ActivateConsume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 41100011, Value: 5}}}},
		&data.ResonantChain{Id: 3, GroupId: 1, GroupIndex: 1, BuffIds: []float64{60003}, ActivateConsume: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 41100011, Value: 1}}}},
	}
	t.FavorWord = []*data.FavorWord{
		&data.FavorWord{Id: 1, RoleId: 1211},
	}
	t.FavorStory = []*data.FavorStory{
		&data.FavorStory{Id: 10, RoleId: 1211},
	}
	t.FavorGoods = []*data.FavorGoods{
		&data.FavorGoods{Id: 20, RoleId: 1211},
	}
	t.Motion = []*data.Motion{
		&data.Motion{Id: 30, RoleId: 1211},
	}
	t.CharacterInitInfo = []*data.CharacterInitInfo{
		&data.CharacterInitInfo{RoleId: 1211, FightBuffs: []int64{12110001}, ConcomitantBuffs: &data.Map[int32, []int64]{Pairs: []data.Pair[int32, []int64]{}}},
	}
	t.SkillBranchBuff = []*data.SkillBranchBuff{
		&data.SkillBranchBuff{BranchId: 121101, RoleId: 1211, Buffs: []int64{70001}},
		&data.SkillBranchBuff{BranchId: 121102, RoleId: 1211, Buffs: []int64{70002}},
	}
	t.BackgroundCard = []*data.BackgroundCard{
		&data.BackgroundCard{Id: 5001},
	}
	t.AiStateMachineConfig = []*data.AiStateMachineConfig{
		&data.AiStateMachineConfig{Id: "SM_Common", StateMachineJson: mustJSON("{\"Version\":100,\"StateMachines\":[10],\"Nodes\":[{\"Uuid\":10,\"IsAnimStateMachine\":true,\"Children\":[11]},{\"Uuid\":11,\"IsAnimStateMachine\":false,\"Children\":[]}]}")},
		&data.AiStateMachineConfig{Id: "SM_RoleBeHit", StateMachineJson: mustJSON("{\"Version\":200,\"StateMachines\":[1],\"Nodes\":[{\"Uuid\":1,\"IsAnimStateMachine\":true,\"ReferenceUuid\":10,\"Children\":[]}]}")},
	}
	t.PhantomItem = []*data.PhantomItem{
		&data.PhantomItem{ItemId: 600001, MonsterId: 390001, MainProp: &data.PhantomPropRandom{RandGroupId: 501, RandNum: 1}, LevelUpGroupId: 1, SkillId: 200001, Rarity: 3, QualityId: 5, ShowInBag: true, Destructible: true, FetterGroup: []int32{1}, PhantomType: 1},
		&data.PhantomItem{ItemId: 600002, MonsterId: 390002, MainProp: &data.PhantomPropRandom{RandGroupId: 501, RandNum: 1}, LevelUpGroupId: 1, SkillId: 200002, Rarity: 3, QualityId: 5, ShowInBag: true, Destructible: true, FetterGroup: []int32{1}, PhantomType: 1},
		&data.PhantomItem{ItemId: 600003, MonsterId: 390003, MainProp: &data.PhantomPropRandom{RandGroupId: 501, RandNum: 1}, LevelUpGroupId: 1, SkillId: 200003, Rarity: 3, QualityId: 5, ShowInBag: true, Destructible: true, FetterGroup: []int32{1}, PhantomType: 1},
		&data.PhantomItem{ItemId: 600004, MonsterId: 390004, MainProp: &data.PhantomPropRandom{RandGroupId: 501, RandNum: 1}, LevelUpGroupId: 1, SkillId: 200004, Rarity: 1, QualityId: 5, ShowInBag: true, Destructible: true, FetterGroup: []int32{1}, PhantomType: 1},
		&data.PhantomItem{ItemId: 600091, MonsterId: 390091, ParentMonsterId: 390001, SkillId: 200001, Rarity: 3, QualityId: 5, PhantomType: 2},
	}
	t.PhantomSkill = []*data.PhantomSkill{
		&data.PhantomSkill{Id: 1, PhantomSkillId: 200001, BuffEffects: []float64{80010}},
		&data.PhantomSkill{Id: 2, PhantomSkillId: 200002, BuffEffects: []float64{80011}},
	}
	t.PhantomFetter = []*data.PhantomFetter{
		&data.PhantomFetter{Id: 101, BuffIds: []float64{80001}, AddProp: []*data.ConfigPropValue{{Id: 22, Value: 1000}}},
	}
	t.PhantomFetterGroup = []*data.PhantomFetterGroup{
		&data.PhantomFetterGroup{Id: 1, FetterMap: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 101, Value: 2}}}},
	}
	t.PhantomMainProperty = []*data.PhantomMainProperty{
		&data.PhantomMainProperty{Id: 1, RandGroupId: 501, PropGroup: []int32{5001, 50001}},
		&data.PhantomMainProperty{Id: 2, RandGroupId: 501, PropGroup: []int32{5002, 50001}},
	}
	t.PhantomMainPropItem = []*data.PhantomMainPropItem{
		&data.PhantomMainPropItem{Id: 5001, PropId: 10007, AddType: 2, StandardProperty: 600, GrowthId: 1},
		&data.PhantomMainPropItem{Id: 5002, PropId: 8, AddType: 1, StandardProperty: 440, GrowthId: 1},
		&data.PhantomMainPropItem{Id: 50001, PropId: 10007, AddType: 1, StandardProperty: 30, GrowthId: 1},
	}
	t.PhantomSubProperty = []*data.PhantomSubProperty{
		&data.PhantomSubProperty{Id: 1, PropId: 10002, AddType: 1, SubStandardProperty: 375},
		&data.PhantomSubProperty{Id: 2, PropId: 10002, AddType: 2, SubStandardProperty: 750},
		&data.PhantomSubProperty{Id: 3, PropId: 10007, AddType: 1, SubStandardProperty: 37},
		&data.PhantomSubProperty{Id: 4, PropId: 10007, AddType: 2, SubStandardProperty: 750},
		&data.PhantomSubProperty{Id: 5, PropId: 10010, AddType: 1, SubStandardProperty: 43},
		&data.PhantomSubProperty{Id: 6, PropId: 8, AddType: 1, SubStandardProperty: 500},
		&data.PhantomSubProperty{Id: 7, PropId: 9, AddType: 1, SubStandardProperty: 1000},
	}
	t.PhantomRarity = []*data.PhantomRarity{
		&data.PhantomRarity{Rare: 1, Cost: 1, PolishCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.PhantomRarity{Rare: 2, Cost: 3, PolishCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.PhantomRarity{Rare: 3, Cost: 4, PolishCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.PhantomRarity{Rare: 4, Cost: 4, PolishCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
	}
	t.PhantomQuality = []*data.PhantomQuality{
		&data.PhantomQuality{Quality: 3, LevelLimit: 15, SlotUnlockLevel: []int32{5, 10, 15}, IdentifyCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.PhantomQuality{Quality: 4, LevelLimit: 20, SlotUnlockLevel: []int32{5, 10, 15, 20}, IdentifyCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.PhantomQuality{Quality: 5, LevelLimit: 25, SlotUnlockLevel: []int32{5, 10, 15, 20, 25}, IdentifyCost: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
	}
	t.PhantomGrowth = []*data.PhantomGrowth{
		&data.PhantomGrowth{Id: 1, GrowthId: 1, Value: 10000},
		&data.PhantomGrowth{Id: 2, GrowthId: 1, Level: 5, Value: 20000},
		&data.PhantomGrowth{Id: 3, GrowthId: 1, Level: 25, Value: 50000},
	}
	t.PhantomLevel = []*data.PhantomLevel{
		&data.PhantomLevel{Id: 1, GroupId: 1, Exp: 0},
		&data.PhantomLevel{Id: 2, GroupId: 1, Level: 1, Exp: 100},
		&data.PhantomLevel{Id: 3, GroupId: 1, Level: 2, Exp: 100},
		&data.PhantomLevel{Id: 4, GroupId: 1, Level: 3, Exp: 100},
		&data.PhantomLevel{Id: 5, GroupId: 1, Level: 4, Exp: 100},
		&data.PhantomLevel{Id: 6, GroupId: 1, Level: 5, Exp: 100},
		&data.PhantomLevel{Id: 7, GroupId: 1, Level: 6, Exp: 100},
		&data.PhantomLevel{Id: 8, GroupId: 1, Level: 7, Exp: 100},
		&data.PhantomLevel{Id: 9, GroupId: 1, Level: 8, Exp: 100},
		&data.PhantomLevel{Id: 10, GroupId: 1, Level: 9, Exp: 100},
		&data.PhantomLevel{Id: 11, GroupId: 1, Level: 10, Exp: 100},
	}
	t.PhantomExpItem = []*data.PhantomExpItem{
		&data.PhantomExpItem{ItemId: 43010001, Exp: 500},
	}
	t.CalabashLevel = []*data.CalabashLevel{
		&data.CalabashLevel{Level: 1, LevelUpExp: 30, Cost: 12, QualityDropWeight: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
		&data.CalabashLevel{Level: 2, LevelUpExp: 50, Cost: 12, QualityDropWeight: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{}}},
	}
	t.DropPackage = []*data.DropPackage{
		&data.DropPackage{Id: 500, DropPreview: &data.Map[int32, int32]{Pairs: []data.Pair[int32, int32]{{Key: 21020053, Value: 1}, {Key: 41100011, Value: 3}}}},
	}
	t.ItemInfo = []*data.ItemInfo{
		&data.ItemInfo{Id: 2, QualityId: 3},
		&data.ItemInfo{Id: 41100011, ItemType: 1, QualityId: 3},
		&data.ItemInfo{Id: 43020001, ItemType: 1, QualityId: 3},
		&data.ItemInfo{Id: 43010001, ItemType: 1, QualityId: 3},
		&data.ItemInfo{Id: 50001, ItemType: 1, QualityId: 5},
	}
	t.RoleDevProsList = []*data.RoleDevProsList{
		&data.RoleDevProsList{Id: 1301, GachaId: 100039, SpecialGachaId: []*data.RoleDevProsList_SpecialGachaId{{Item1: 4, Item2: 1}}},
	}
	t.RoleDevProsProject = []*data.RoleDevProsProject{
		&data.RoleDevProsProject{Id: 1411, ElementId: 4, RoleName: "RoleInfo_1411_Name", RoleItemGroup: []int32{141101}, PrefectSkillLevel: []int32{10, 10}},
	}
	t.Buff[5].SetExecutePeriodicEffectOnApplication(true)
	return t.Reindex()
}

func mustJSON(text string) data.Value {
	v, err := data.ParseJSON([]byte(text))
	if err != nil {
		panic(err)
	}
	return v
}
