// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package save

import (
	"tidelab/internal/zon"
)

func ints(v []int32) *zon.Value { return IntList(v) }

// BasicInfoSave is state/player/<id>/basic_info (specs/save-format.md §5.1).
type BasicInfoSave struct {
	Attributes         BasicAttributesSave
	RoleShowList       []int32
	SelectedMainRoleID int32
	CurCardID          int32
	CurPlayerTitleID   int32
	LastModifyNameTime int64
	Birthday           int32
	DisplayBirthday    bool
	ExtraFields        []zon.Field
}

var basicInfoKnown = set("attributes", "role_show_list", "selected_main_role_id", "cur_card_id",
	"cur_player_title_id", "last_modify_name_time", "birthday", "display_birthday")

func NewBasicInfo() BasicInfoSave {
	return BasicInfoSave{Attributes: NewBasicAttributes(), RoleShowList: []int32{}, Birthday: 20260407, DisplayBirthday: true}
}

func BasicInfoFromZon(v *zon.Value) BasicInfoSave {
	b := BasicInfoSave{Attributes: NewBasicAttributes()}
	if a := field(v, "attributes"); kindIs(a, zon.Struct) {
		b.Attributes = BasicAttributesFromZon(a)
	}
	b.RoleShowList = zIntList(v, "role_show_list")
	b.SelectedMainRoleID = zInt(v, "selected_main_role_id", 0)
	b.CurCardID = zInt(v, "cur_card_id", 0)
	b.CurPlayerTitleID = zInt(v, "cur_player_title_id", 0)
	b.LastModifyNameTime = zLong(v, "last_modify_name_time", 0)
	b.Birthday = zInt(v, "birthday", 20260407)
	b.DisplayBirthday = zBool(v, "display_birthday", true)
	b.ExtraFields = captureExtras(v, basicInfoKnown)
	return b
}

func (b *BasicInfoSave) ToZon() *zon.Value {
	return withExtras(b.ExtraFields,
		zon.F("attributes", b.Attributes.ToZon()),
		zon.F("role_show_list", ints(b.RoleShowList)),
		zon.F("selected_main_role_id", N(int64(b.SelectedMainRoleID))),
		zon.F("cur_card_id", N(int64(b.CurCardID))),
		zon.F("cur_player_title_id", N(int64(b.CurPlayerTitleID))),
		zon.F("last_modify_name_time", N(b.LastModifyNameTime)),
		zon.F("birthday", N(int64(b.Birthday))),
		zon.F("display_birthday", zon.BoolValue(b.DisplayBirthday)),
	)
}

type BasicAttributesSave struct {
	Level               int32
	Exp                 int32
	Coin                int32
	RareCoin            int32
	HeadPhoto           int32
	HeadFrame           int32
	AreaID              int32
	Name                string
	Sign                string
	Sex                 int32
	OriginWorldLevel    int32
	CurWorldLevel       int32
	WorldLevelTimeStamp int32
	CashCoin            int32
	WorldPermission     int32
	ExtraFields         []zon.Field
}

var basicAttributesKnown = set("level", "exp", "coin", "rare_coin", "head_photo", "head_frame", "area_id",
	"name", "sign", "sex", "origin_world_level", "cur_world_level",
	"world_level_time_stamp", "cash_coin", "world_permission")

func NewBasicAttributes() BasicAttributesSave {
	return BasicAttributesSave{Level: 80, Exp: 666, Coin: 666666, RareCoin: 666666, HeadPhoto: 82001209,
		AreaID: 1, Name: "Tidelab", Sign: "Tidelab offline server", OriginWorldLevel: 8, CurWorldLevel: 8, CashCoin: 666666}
}

func BasicAttributesFromZon(v *zon.Value) BasicAttributesSave {
	return BasicAttributesSave{
		Level:               zInt(v, "level", 80),
		Exp:                 zInt(v, "exp", 666),
		Coin:                zInt(v, "coin", 666666),
		RareCoin:            zInt(v, "rare_coin", 666666),
		HeadPhoto:           zInt(v, "head_photo", 82001209),
		HeadFrame:           zInt(v, "head_frame", 0),
		AreaID:              zInt(v, "area_id", 1),
		Name:                zString(v, "name", "Tidelab"),
		Sign:                zString(v, "sign", "Tidelab offline server"),
		Sex:                 zInt(v, "sex", 0),
		OriginWorldLevel:    zInt(v, "origin_world_level", 8),
		CurWorldLevel:       zInt(v, "cur_world_level", 8),
		WorldLevelTimeStamp: zInt(v, "world_level_time_stamp", 0),
		CashCoin:            zInt(v, "cash_coin", 666666),
		WorldPermission:     zInt(v, "world_permission", 0),
		ExtraFields:         captureExtras(v, basicAttributesKnown),
	}
}

func (a *BasicAttributesSave) ToZon() *zon.Value {
	return withExtras(a.ExtraFields,
		zon.F("level", N(int64(a.Level))),
		zon.F("exp", N(int64(a.Exp))),
		zon.F("coin", N(int64(a.Coin))),
		zon.F("rare_coin", N(int64(a.RareCoin))),
		zon.F("head_photo", N(int64(a.HeadPhoto))),
		zon.F("head_frame", N(int64(a.HeadFrame))),
		zon.F("area_id", N(int64(a.AreaID))),
		zon.F("name", zon.Str(a.Name)),
		zon.F("sign", zon.Str(a.Sign)),
		zon.F("sex", N(int64(a.Sex))),
		zon.F("origin_world_level", N(int64(a.OriginWorldLevel))),
		zon.F("cur_world_level", N(int64(a.CurWorldLevel))),
		zon.F("world_level_time_stamp", N(int64(a.WorldLevelTimeStamp))),
		zon.F("cash_coin", N(int64(a.CashCoin))),
		zon.F("world_permission", N(int64(a.WorldPermission))),
	)
}

// CosmeticInfoSave is state/player/<id>/cosmetic_info (specs/save-format.md §5.5).
type CosmeticInfoSave struct {
	RoleSkins       []int32
	PhantomSkins    []int32
	FlySkins        []int32
	WeaponSkins     []int32
	Ornaments       []int32
	ViewedOrnaments []int32
	ExtraFields     []zon.Field
}

var cosmeticKnown = set("role_skins", "phantom_skins", "fly_skins", "weapon_skins", "ornaments", "viewed_ornaments")

func NewCosmeticInfo() CosmeticInfoSave {
	return CosmeticInfoSave{RoleSkins: []int32{}, PhantomSkins: []int32{}, FlySkins: []int32{}, WeaponSkins: []int32{},
		Ornaments: []int32{}, ViewedOrnaments: []int32{}}
}

func CosmeticInfoFromZon(v *zon.Value) CosmeticInfoSave {
	return CosmeticInfoSave{
		RoleSkins:       zIntList(v, "role_skins"),
		PhantomSkins:    zIntList(v, "phantom_skins"),
		FlySkins:        zIntList(v, "fly_skins"),
		WeaponSkins:     zIntList(v, "weapon_skins"),
		Ornaments:       zIntList(v, "ornaments"),
		ViewedOrnaments: zIntList(v, "viewed_ornaments"),
		ExtraFields:     captureExtras(v, cosmeticKnown),
	}
}

func (c *CosmeticInfoSave) ToZon() *zon.Value {
	return withExtras(c.ExtraFields,
		zon.F("role_skins", ints(c.RoleSkins)),
		zon.F("phantom_skins", ints(c.PhantomSkins)),
		zon.F("fly_skins", ints(c.FlySkins)),
		zon.F("weapon_skins", ints(c.WeaponSkins)),
		zon.F("ornaments", ints(c.Ornaments)),
		zon.F("viewed_ornaments", ints(c.ViewedOrnaments)),
	)
}

const (
	FormationSlotCount = 20
	RolesPerFormation  = 3
)

// FormationInfoSave is state/player/<id>/formation_info (specs/save-format.md §5.3, formation.md §1).
type FormationInfoSave struct {
	CurFormation int32
	Formations   [FormationSlotCount]*FormationSlotSave
	ExtraFields  []zon.Field
}

var formationInfoKnown = set("cur_formation", "formations")

func NewFormationInfo() *FormationInfoSave {
	f := &FormationInfoSave{}
	f.Formations = EmptyFormationSlots()
	return f
}

func EmptyFormationSlots() [FormationSlotCount]*FormationSlotSave {
	var a [FormationSlotCount]*FormationSlotSave
	for i := range a {
		a[i] = &FormationSlotSave{}
	}
	return a
}

func FormationInfoFromZon(v *zon.Value) *FormationInfoSave {
	s := NewFormationInfo()
	s.CurFormation = zInt(v, "cur_formation", 0)
	s.ExtraFields = captureExtras(v, formationInfoKnown)
	if f := field(v, "formations"); isListish(f) {
		it := items(f)
		for i := 0; i < min(len(it), FormationSlotCount); i++ {
			s.Formations[i] = FormationSlotFromZon(it[i])
		}
	}
	return s
}

func (s *FormationInfoSave) ToZon() *zon.Value {
	slots := make([]*zon.Value, FormationSlotCount)
	for i, f := range s.Formations {
		slots[i] = f.ToZon()
	}
	return withExtras(s.ExtraFields,
		zon.F("cur_formation", N(int64(s.CurFormation))),
		zon.F("formations", zon.NewList(slots...)),
	)
}

type FormationSlotSave struct {
	CurRole     int32
	Name        *string
	Roles       [RolesPerFormation]*FormationRoleSave
	ExtraFields []zon.Field
}

var formationSlotKnown = set("cur_role", "name", "roles")

func (s *FormationSlotSave) HasAnyRole() bool {
	for _, r := range s.Roles {
		if r != nil && r.RoleID > 0 {
			return true
		}
	}
	return false
}

// FilledRoles are the slots with a role, in slot order.
func (s *FormationSlotSave) FilledRoles() []*FormationRoleSave {
	var out []*FormationRoleSave
	for _, r := range s.Roles {
		if r != nil && r.RoleID > 0 {
			out = append(out, r)
		}
	}
	return out
}

func FormationSlotFromZon(v *zon.Value) *FormationSlotSave {
	s := &FormationSlotSave{
		CurRole:     zInt(v, "cur_role", 0),
		Name:        zNullableString(v, "name"),
		ExtraFields: captureExtras(v, formationSlotKnown),
	}
	if r := field(v, "roles"); isListish(r) {
		it := items(r)
		for i := 0; i < min(len(it), RolesPerFormation); i++ {
			if it[i].Kind() == zon.Struct {
				s.Roles[i] = FormationRoleFromZon(it[i])
			}
		}
	}
	return s
}

func (s *FormationSlotSave) ToZon() *zon.Value {
	name := zon.NullValue
	if s.Name != nil {
		name = zon.Str(*s.Name)
	}
	roles := make([]*zon.Value, RolesPerFormation)
	for i, r := range s.Roles {
		if r == nil {
			roles[i] = zon.NullValue
		} else {
			roles[i] = r.ToZon()
		}
	}
	return withExtras(s.ExtraFields,
		zon.F("cur_role", N(int64(s.CurRole))),
		zon.F("name", name),
		zon.F("roles", zon.NewList(roles...)),
	)
}

type FormationRoleSave struct {
	RoleID                int32
	EntityID              int64
	OnStageWithoutControl bool
	ExtraFields           []zon.Field
}

var formationRoleKnown = set("role_id", "entity_id", "on_stage_without_control")

func NewFormationRole(roleID int32) *FormationRoleSave {
	return &FormationRoleSave{RoleID: roleID, EntityID: -1}
}

func FormationRoleFromZon(v *zon.Value) *FormationRoleSave {
	return &FormationRoleSave{
		ExtraFields:           captureExtras(v, formationRoleKnown),
		RoleID:                zInt(v, "role_id", 0),
		EntityID:              zLong(v, "entity_id", -1),
		OnStageWithoutControl: zBool(v, "on_stage_without_control", false),
	}
}

func (r *FormationRoleSave) ToZon() *zon.Value {
	return withExtras(r.ExtraFields,
		zon.F("role_id", N(int64(r.RoleID))),
		zon.F("entity_id", N(r.EntityID)),
		zon.F("on_stage_without_control", zon.BoolValue(r.OnStageWithoutControl)),
	)
}

// RoleSave is state/player/<id>/role/<roleId> (specs/save-format.md §5.9, roles.md §1).
type RoleSave struct {
	RoleID                  int32
	Level                   int32
	Exp                     int32
	Breakthrough            int32
	Skills                  []IntPair // skill id, level
	SkillBranch             int32
	Phantom                 []IntPair
	Weapon                  int32
	Star                    int32
	Favor                   int32
	Reson                   []ResonSave
	CurModel                int32
	Models                  []int32
	BaseProp                []int32
	AddProp                 []int32
	CreateTime              uint32
	SkillNodeState          []*SkillNodeSave
	ResonantChainGroupIndex int32
	RoleSkinID              int32
	ParaglidingSkinID       int32
	SoarSkinID              int32
	WeaponSkinID            int32
	Ornaments               []OrnamentEquipSave
	CalabashSkinID          int32
	EnableSelfBgm           bool
	VoiceLanguage           int32
	ExtraFields             []zon.Field
}

var roleKnown = set("level", "exp", "breakthrough", "skills", "skill_branch", "phantom", "weapon",
	"star", "favor", "reson", "cur_model", "models", "base_prop", "add_prop",
	"create_time", "skill_node_state", "resonant_chain_group_index", "role_skin_id",
	"paragliding_skin_id", "soar_skin_id", "weapon_skin_id", "ornaments",
	"calabash_skin_id", "enable_self_bgm", "voice_language")

// NewRole is a role with the defaults of a missing file.
func NewRole(roleID int32) *RoleSave {
	return &RoleSave{RoleID: roleID, Level: 1, Favor: 5, Skills: []IntPair{}, Phantom: []IntPair{}, Reson: []ResonSave{},
		Models: []int32{}, BaseProp: []int32{}, AddProp: []int32{}, SkillNodeState: []*SkillNodeSave{}, Ornaments: []OrnamentEquipSave{}}
}

type ResonSave struct {
	ID       int32
	Open     bool
	Increase int32
}

type SkillNodeSave struct {
	NodeID  int32
	Active  bool
	SkillID int32
}

type OrnamentEquipSave struct{ RoleSkinID, OrnamentID int32 }

func RoleFromZon(roleID int32, v *zon.Value) *RoleSave {
	r := &RoleSave{
		RoleID:                  roleID,
		Level:                   zInt(v, "level", 1),
		Exp:                     zInt(v, "exp", 0),
		Breakthrough:            zInt(v, "breakthrough", 0),
		Skills:                  zIntPairList(v, "skills"),
		SkillBranch:             zInt(v, "skill_branch", 0),
		Phantom:                 zIntPairList(v, "phantom"),
		Weapon:                  zInt(v, "weapon", 0),
		Star:                    zInt(v, "star", 0),
		Favor:                   zInt(v, "favor", 5),
		CurModel:                zInt(v, "cur_model", 0),
		Models:                  zIntList(v, "models"),
		BaseProp:                zIntList(v, "base_prop"),
		AddProp:                 zIntList(v, "add_prop"),
		CreateTime:              zUint(v, "create_time", 0),
		ResonantChainGroupIndex: zInt(v, "resonant_chain_group_index", 0),
		RoleSkinID:              zInt(v, "role_skin_id", 0),
		ParaglidingSkinID:       zInt(v, "paragliding_skin_id", 0),
		SoarSkinID:              zInt(v, "soar_skin_id", 0),
		WeaponSkinID:            zInt(v, "weapon_skin_id", 0),
		CalabashSkinID:          zInt(v, "calabash_skin_id", 0),
		EnableSelfBgm:           zBool(v, "enable_self_bgm", false),
		VoiceLanguage:           zInt(v, "voice_language", 0),
		ExtraFields:             captureExtras(v, roleKnown),
		Reson:                   []ResonSave{},
		SkillNodeState:          []*SkillNodeSave{},
		Ornaments:               []OrnamentEquipSave{},
	}
	if rs := field(v, "reson"); isListish(rs) {
		for _, it := range items(rs) {
			r.Reson = append(r.Reson, ResonSave{zInt(it, "id", 0), zBool(it, "open", false), zInt(it, "increase", 0)})
		}
	}
	if sn := field(v, "skill_node_state"); isListish(sn) {
		for _, it := range items(sn) {
			r.SkillNodeState = append(r.SkillNodeState, &SkillNodeSave{zInt(it, "node_id", 0), zBool(it, "active", false), zInt(it, "skill_id", 0)})
		}
	}
	if o := field(v, "ornaments"); isListish(o) {
		for _, it := range items(o) {
			r.Ornaments = append(r.Ornaments, OrnamentEquipSave{zInt(it, "role_skin_id", 0), zInt(it, "ornament_id", 0)})
		}
	}
	return r
}

func pairList(p []IntPair) *zon.Value {
	out := make([]*zon.Value, len(p))
	for i, x := range p {
		out[i] = zon.NewList(N(int64(x.Key)), N(int64(x.Value)))
	}
	return zon.NewList(out...)
}

func (r *RoleSave) ToZon() *zon.Value {
	reson := make([]*zon.Value, len(r.Reson))
	for i, x := range r.Reson {
		reson[i] = zon.NewStruct(zon.F("id", N(int64(x.ID))), zon.F("open", zon.BoolValue(x.Open)), zon.F("increase", N(int64(x.Increase))))
	}
	nodes := make([]*zon.Value, len(r.SkillNodeState))
	for i, n := range r.SkillNodeState {
		nodes[i] = zon.NewStruct(zon.F("node_id", N(int64(n.NodeID))), zon.F("active", zon.BoolValue(n.Active)), zon.F("skill_id", N(int64(n.SkillID))))
	}
	orn := make([]*zon.Value, len(r.Ornaments))
	for i, o := range r.Ornaments {
		orn[i] = zon.NewStruct(zon.F("role_skin_id", N(int64(o.RoleSkinID))), zon.F("ornament_id", N(int64(o.OrnamentID))))
	}
	return withExtras(r.ExtraFields,
		zon.F("level", N(int64(r.Level))),
		zon.F("exp", N(int64(r.Exp))),
		zon.F("breakthrough", N(int64(r.Breakthrough))),
		zon.F("skills", pairList(r.Skills)),
		zon.F("skill_branch", N(int64(r.SkillBranch))),
		zon.F("phantom", pairList(r.Phantom)),
		zon.F("weapon", N(int64(r.Weapon))),
		zon.F("star", N(int64(r.Star))),
		zon.F("favor", N(int64(r.Favor))),
		zon.F("reson", zon.NewList(reson...)),
		zon.F("cur_model", N(int64(r.CurModel))),
		zon.F("models", ints(r.Models)),
		zon.F("base_prop", ints(r.BaseProp)),
		zon.F("add_prop", ints(r.AddProp)),
		zon.F("create_time", zon.Uint(uint64(r.CreateTime))),
		zon.F("skill_node_state", zon.NewList(nodes...)),
		zon.F("resonant_chain_group_index", N(int64(r.ResonantChainGroupIndex))),
		zon.F("role_skin_id", N(int64(r.RoleSkinID))),
		zon.F("paragliding_skin_id", N(int64(r.ParaglidingSkinID))),
		zon.F("soar_skin_id", N(int64(r.SoarSkinID))),
		zon.F("weapon_skin_id", N(int64(r.WeaponSkinID))),
		zon.F("ornaments", zon.NewList(orn...)),
		zon.F("calabash_skin_id", N(int64(r.CalabashSkinID))),
		zon.F("enable_self_bgm", zon.BoolValue(r.EnableSelfBgm)),
		zon.F("voice_language", N(int64(r.VoiceLanguage))),
	)
}

func nullableInt(p *int32) *zon.Value {
	if p == nil {
		return zon.NullValue
	}
	return N(int64(*p))
}

// WeaponSave is state/player/<id>/weapon/<uniqueId> (specs/save-format.md §5.9).
type WeaponSave struct {
	UniqueID    int32
	ID          int32
	FuncValue   int32
	Level       int32
	Exp         int32
	Breach      int32
	ResonLevel  int32
	RoleID      *int32
	ExtraFields []zon.Field
}

var weaponKnown = set("id", "func_value", "level", "exp", "breach", "reson_level", "role_id")

func NewWeapon(uniqueID int32) *WeaponSave {
	return &WeaponSave{UniqueID: uniqueID, Level: 1, ResonLevel: 1}
}

func WeaponFromZon(uniqueID int32, v *zon.Value) *WeaponSave {
	return &WeaponSave{
		UniqueID:    uniqueID,
		ID:          zInt(v, "id", 0),
		FuncValue:   zInt(v, "func_value", 0),
		Level:       zInt(v, "level", 1),
		Exp:         zInt(v, "exp", 0),
		Breach:      zInt(v, "breach", 0),
		ResonLevel:  zInt(v, "reson_level", 1),
		RoleID:      zNullableInt(v, "role_id"),
		ExtraFields: captureExtras(v, weaponKnown),
	}
}

func (w *WeaponSave) ToZon() *zon.Value {
	return withExtras(w.ExtraFields,
		zon.F("id", N(int64(w.ID))),
		zon.F("func_value", N(int64(w.FuncValue))),
		zon.F("level", N(int64(w.Level))),
		zon.F("exp", N(int64(w.Exp))),
		zon.F("breach", N(int64(w.Breach))),
		zon.F("reson_level", N(int64(w.ResonLevel))),
		zon.F("role_id", nullableInt(w.RoleID)),
	)
}

// EchoSave is state/player/<id>/echo/<uniqueId> (specs/save-format.md §5.9).
type EchoSave struct {
	UniqueID      int32
	ID            int32
	FuncValue     int32
	Level         int32
	Exp           int32
	MainProp      []EchoPropSave
	SubProp       []EchoPropSave
	FetterGroupID int32
	SkinID        int32
	RoleID        *int32
	EquippedPos   int32
	UnackSubProp  []EchoPropSave
	LockPropIndex []int32
	ExtraFields   []zon.Field
}

type EchoPropSave struct{ ID, Value int32 }

var echoKnown = set("id", "func_value", "level", "exp", "main_prop", "sub_prop",
	"fetter_group_id", "skin_id", "role_id", "equipped_pos",
	"unack_sub_prop", "lock_prop_index")

func NewEcho(uniqueID int32) *EchoSave {
	return &EchoSave{UniqueID: uniqueID, EquippedPos: -1, MainProp: []EchoPropSave{}, SubProp: []EchoPropSave{},
		UnackSubProp: []EchoPropSave{}, LockPropIndex: []int32{}}
}

func readProps(v *zon.Value, name string) []EchoPropSave {
	out := []EchoPropSave{}
	if p := field(v, name); isListish(p) {
		for _, it := range items(p) {
			out = append(out, EchoPropSave{zInt(it, "id", 0), zInt(it, "value", 0)})
		}
	}
	return out
}

func writeProps(p []EchoPropSave) *zon.Value {
	out := make([]*zon.Value, len(p))
	for i, x := range p {
		out[i] = zon.NewStruct(zon.F("id", N(int64(x.ID))), zon.F("value", N(int64(x.Value))))
	}
	return zon.NewList(out...)
}

func EchoFromZon(uniqueID int32, v *zon.Value) *EchoSave {
	return &EchoSave{
		UniqueID:      uniqueID,
		ID:            zInt(v, "id", 0),
		FuncValue:     zInt(v, "func_value", 0),
		Level:         zInt(v, "level", 0),
		Exp:           zInt(v, "exp", 0),
		MainProp:      readProps(v, "main_prop"),
		SubProp:       readProps(v, "sub_prop"),
		FetterGroupID: zInt(v, "fetter_group_id", 0),
		SkinID:        zInt(v, "skin_id", 0),
		RoleID:        zNullableInt(v, "role_id"),
		EquippedPos:   zInt(v, "equipped_pos", -1),
		UnackSubProp:  readProps(v, "unack_sub_prop"),
		LockPropIndex: zIntList(v, "lock_prop_index"),
		ExtraFields:   captureExtras(v, echoKnown),
	}
}

func (e *EchoSave) ToZon() *zon.Value {
	return withExtras(e.ExtraFields,
		zon.F("id", N(int64(e.ID))),
		zon.F("func_value", N(int64(e.FuncValue))),
		zon.F("level", N(int64(e.Level))),
		zon.F("exp", N(int64(e.Exp))),
		zon.F("main_prop", writeProps(e.MainProp)),
		zon.F("sub_prop", writeProps(e.SubProp)),
		zon.F("fetter_group_id", N(int64(e.FetterGroupID))),
		zon.F("skin_id", N(int64(e.SkinID))),
		zon.F("role_id", nullableInt(e.RoleID)),
		zon.F("equipped_pos", N(int64(e.EquippedPos))),
		zon.F("unack_sub_prop", writeProps(e.UnackSubProp)),
		zon.F("lock_prop_index", ints(e.LockPropIndex)),
	)
}

const (
	EchoPresetMaxGroups = 20
	EchoSlotsPerGroup   = 5
)

// EchoPresetsSave is state/player/<id>/echo/presets (specs/save-format.md §5.9, inventory.md §6.6).
type EchoPresetsSave struct {
	Groups []*VisionGroupSave
}

type VisionGroupSave struct {
	IncrID int32
	Name   string
	Slots  [EchoSlotsPerGroup]int32
}

func EchoPresetsFromZon(v *zon.Value) *EchoPresetsSave {
	s := &EchoPresetsSave{}
	if g := field(v, "groups"); isListish(g) {
		for _, it := range items(g) {
			grp := &VisionGroupSave{IncrID: zInt(it, "incr_id", 0), Name: zString(it, "name", "")}
			if sl := field(it, "slots"); isListish(sl) {
				arr := items(sl)
				for i := 0; i < min(len(arr), EchoSlotsPerGroup); i++ {
					grp.Slots[i] = must(arr[i].AsInt32())
				}
			}
			s.Groups = append(s.Groups, grp)
		}
	}
	return s
}

func (s *EchoPresetsSave) ToZon() *zon.Value {
	groups := make([]*zon.Value, len(s.Groups))
	for i, g := range s.Groups {
		groups[i] = zon.NewStruct(
			zon.F("incr_id", N(int64(g.IncrID))),
			zon.F("name", zon.Str(g.Name)),
			zon.F("slots", ints(g.Slots[:])))
	}
	return zon.NewStruct(zon.F("groups", zon.NewList(groups...)))
}

// CalabashSave is state/player/<id>/echo/calabash (specs/save-format.md §5.9, inventory.md §6.7).
type CalabashSave struct {
	RewardedLevels      []int32
	ProjectorMonsterIDs []int32
	ProjectorSkins      []ProjectorSkinSave
}

type ProjectorSkinSave struct{ MonsterID, SkinID int32 }

func NewCalabash() *CalabashSave {
	return &CalabashSave{RewardedLevels: []int32{}, ProjectorMonsterIDs: []int32{}, ProjectorSkins: []ProjectorSkinSave{}}
}

func CalabashFromZon(v *zon.Value) *CalabashSave {
	s := &CalabashSave{
		RewardedLevels:      zIntList(v, "rewarded_levels"),
		ProjectorMonsterIDs: zIntList(v, "projector_monster_ids"),
		ProjectorSkins:      []ProjectorSkinSave{},
	}
	if ps := field(v, "projector_skins"); isListish(ps) {
		for _, it := range items(ps) {
			s.ProjectorSkins = append(s.ProjectorSkins, ProjectorSkinSave{zInt(it, "monster_id", 0), zInt(it, "skin_id", 0)})
		}
	}
	return s
}

func (s *CalabashSave) ToZon() *zon.Value {
	skins := make([]*zon.Value, len(s.ProjectorSkins))
	for i, p := range s.ProjectorSkins {
		skins[i] = zon.NewStruct(zon.F("monster_id", N(int64(p.MonsterID))), zon.F("skin_id", N(int64(p.SkinID))))
	}
	return zon.NewStruct(
		zon.F("rewarded_levels", ints(s.RewardedLevels)),
		zon.F("projector_monster_ids", ints(s.ProjectorMonsterIDs)),
		zon.F("projector_skins", zon.NewList(skins...)))
}
