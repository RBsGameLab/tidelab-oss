# Roles, role attributes, and role requests

Boundary with entities: [entities.md](entities.md) owns how a role entity is built, numbered, and
spawned; this spec owns the role records, login pushes, attribute calculation, role entity
initial buffs and skills, and role requests.

---

## 1. Save state and new-account roles

Role records live under `state/player/<id>/role/<roleId>` ([save-format.md](save-format.md) §5.9).
Account-level fields read or written here are `basic_info.sex` and
`basic_info.selected_main_role_id` (§5.1), `role_showlist` and `cosmetic_info` (§5.5).

### New account (`player/<id>/role/` absent)

The server creates one role record for **every** row in `RoleInfo` (161 rows in 3.8 data).
`CharacterInitInfo` (53 rows) is **not** used to choose which roles an account owns; it supplies
initial combat buffs and concomitant definitions when a role entity is created (§5).

For each `RoleInfo` row:

| Field | Initial value |
|---|---|
| `level` | `RoleInfo.MaxLevel` (90 for playable resonators; 1 for non-playable entries) |
| `exp` | `0` |
| `breakthrough` | `6` |
| `skill_branch` | `RoleInfo.DefaultSkillBranchId` |
| `role_skin_id` | `RoleInfo.SkinId` |
| `favor` | `5` |
| `create_time` | current Unix timestamp in seconds (`u32`) |
| `base_prop` | 143 `i32` entries (`0..142`), built from `BaseProperty` and `RolePropertyGrowth` (§3.1) |
| `add_prop` | empty (live bonuses are computed from equipment, skills, chains, and echoes; §3.2) |
| `skill_node_state` | every `SkillTree` row where `SkillTree.NodeGroup == RoleInfo.SkillTreeGroupId`; `node_id = Id`, `skill_id = SkillId`, `active = (Condition is empty && UnLockCondition == 0)` |
| `skills` | one `(SkillId, 1)` pair for each active node above with `SkillId != 0` (deduplicated by `SkillId`) |
| `weapon`, `star`, `resonant_chain_group_index`, other skins, `voice_language` | `0` |

### Login repair (`Entered(session, reconnect)`)

Before sending role pushes, the server checks every loaded role record:

1. **Base attribute vector length**: if `base_prop.len != 143` (`EAttributeType.MAX`), rebuild
   `base_prop` from `BaseProperty` and `RolePropertyGrowth(level, breakthrough)` (§3.1) and mark
   the role dirty for saving (§7).
2. **Guaranteed weapon**: every owned role must hold an equipped weapon. If `role.weapon == 0`
   or no weapon exists with that unique id:
   - Look for an existing unequipped weapon (`role_id == null`) whose config id equals
     `RoleInfo.InitWeaponItemId`.
   - If none exists, allocate a new unique id from `special_item/next` ([inventory.md](inventory.md) §1)
     and create a weapon with `id = RoleInfo.InitWeaponItemId`, `level = 1`, `exp = 0`,
     `breach = 0`, `reson_level = 1`, `func_value = (WeaponConf.QualityId >= 5 ? 0 : 1)`.
   - Set `weapon.role_id = roleId`, `role.weapon = uniqueId`, and save both files.

---

## 2. Role pushes at login

On `Entered(session, reconnect)` (after login repair and stray-weapon migration,
[inventory.md](inventory.md) §2), the server pushes the following role and cosmetic notifies
before scene join (overall login push order is in [enter-world.md](enter-world.md)):

### 2.1 `RoleConfigInfoNotify` (18911)

- `RoleConfigs` (#14, `repeated RoleConfigInfo`): one `{ RoleId (#1), SkillBranch (#2) }` entry
  for every owned role.

### 2.2 `RoleChangeUnlockNotify` (20322)

- `UnlockRoleIds` (#3): all `MainRoleConfig.Id` rows whose `Gender == basic_info.sex`.
- `CanChangeSexTime` (#14): `0` (no cooldown).

### 2.3 `PbGetRoleListNotify` (23798)

`RoleList` (#5, `repeated roleInfo` — note the lowercase `r` in the 3.8 schema) contains one
entry for each owned role matching **both** filters:

1. `1000 <= roleId <= 1999` (65 rows in 3.8 `RoleInfo`; excludes trial/NPC/story-only rows
   outside that range).
2. If `roleId` is in `MainRoleConfig` (the Rover variants), include it **only** when
   `roleId == selected_main_role` (derived from `basic_info.selected_main_role_id` if owned and
   matching `basic_info.sex`, else the first owned `MainRoleConfig` matching `sex`).

Each `roleInfo` entry is populated from the role record plus its **live computed attribute
snapshot** (§3.2):

| Field | Wire # | Value |
|---|---|---|
| `RoleId` | 1 | role config id |
| `Name` | 2 | `""` |
| `Level` | 3 | `role.level` |
| `Exp` | 4 | `role.exp` |
| `Breakthrough` | 5 | `role.breakthrough` |
| `Skills` | 6 | `role.skills` (`ArrayIntInt{ Key = skill_id, Value = level }`) |
| `Phantom` | 7 | `role.phantom` (`ArrayIntInt`) |
| `Star` | 8 | `role.star` |
| `Favor` | 10 | `role.favor` |
| `Reson` | 11 | `role.reson` mapped to `ResonInfo{ ResonId, IsOpen, Increase }` |
| `CurModel` | 12 | `role.cur_model` |
| `Models` | 13 | `role.models` |
| `BaseProp` | 14 | `ArrayIntInt` list for indices `0..142` from live `base_props` (§3.2) |
| `AddProp` | 15 | `ArrayIntInt` list for indices `0..142` from live `add_props` (§3.2) |
| `CreateTime` | 17 | `role.create_time` |
| `SkillNodeState` | 19 | `role.skill_node_state` (`ArraySkillNode{ SkillNodeId, IsActive, SkillId }`) |
| `ResonantChainGroupIndex` | 20 | `role.resonant_chain_group_index` |
| `SkinId` | 21 | `role.role_skin_id` |
| `EnableSelfBgm` | 22 | `role.enable_self_bgm` |

Equipped weapons and echoes are **not** carried in `roleInfo`: the client links them from
`WeaponItemResponse` (`WeaponItem.RoleId`) and `PhantomItemResponse` (`EquipInfo`,
[inventory.md](inventory.md) §4). Per-role skill branches travel in `RoleConfigInfoNotify`
(§2.1) and voice language overrides in `PlayerVoiceLanguageResponse` (§6.11).

### 2.4 `RoleFavorListNotify` (29381) and `RoleMotionListNotify` (16046)

- `RoleFavorListNotify`: `FavorList` (#10, `repeated RoleFavor`) has one entry per owned role:
  `RoleId`, `Level = 5`, `Exp = 16800`, `WordIds` = all `FavorWord.Id` rows matching `RoleId`
  with `Status = ItemUnLocked`, `StoryIds` = all `FavorStory.Id` rows matching `RoleId` with
  `Status = ItemUnLocked`, `GoodsIds` = all `FavorGoods.Id` rows matching `RoleId` with
  `Status = ItemUnLocked`.
- `RoleMotionListNotify`: `MotionList` (#2, `repeated RoleMotion`) has one entry per owned role
  that has at least one `Motion` row: `RoleId`, `MotionIds` = all `Motion.Id` rows matching
  `RoleId` with `Status = ItemUnLocked`.

### 2.5 Cosmetic and client-storage notifies at login

Before `PbGetRoleListNotify`, the server pushes:
- `UnlockSkinDataNotify` (phantom skins from `cosmetic_info.phantom_skins`, `IsLogin = true`),
- `FlyEquipAddNotify` (21836, `FlySkins` #15 from `cosmetic_info.fly_skins`),
- `RoleFlyEquipNotify` (21114, `DataList` #1 mapping each fly skin to roles equipping it via
  `soar_skin_id` / `paragliding_skin_id`),
- `OrnamentInfoNotify` (15327, unlocked ornaments, equipped map per `RoleSkinId`, viewed red
  dots),
- `StorageInfoNotify` (17245, default empty red-dot sets for `RoleSkinRedDot`,
  `FlySkinRedDot`, `WeaponSkinRedDot`, `CalabashSkinRedDot`, `GetOrnament`, plus `Ornament`
  from `cosmetic_info.viewed_ornaments`, merged with saved client storage entries, R-113).

---

## 3. Role attributes and formulas

All attribute arrays are indexed by `EAttributeType` (`0..142`; length `143 = EAttributeType.MAX`).

### 3.1 Saved base attributes (`base_prop`)

Computed when initializing a role or repairing a mismatched `base_prop` length:

1. Zero all 143 slots.
2. Look up `BaseProperty` by `RoleInfo.PropertyId`. Copy each of the 128 named columns matching
   an `EAttributeType` enumerator into that index (the other 15 indices and `None` remain `0`).
   Set `base_prop[Lv] = role.level`.
3. Look up `RolePropertyGrowth` where `Level == role.level` and
   `BreachLevel == role.breakthrough`. If found, scale four entries with **float32** arithmetic
   and truncation toward zero:
   - `base_prop[LifeMax] = trunc_i32(f32(base_prop[LifeMax]) * (f32(LifeMaxRatio) / 10000.0f))`
   - `base_prop[Life]    = trunc_i32(f32(base_prop[Life])    * (f32(LifeMaxRatio) / 10000.0f))`
   - `base_prop[Atk]     = trunc_i32(f32(base_prop[Atk])     * (f32(AtkRatio)     / 10000.0f))`
   - `base_prop[Def]     = trunc_i32(f32(base_prop[Def])     * (f32(DefRatio)     / 10000.0f))`

Note: `RolePropertyGrowth` has no separate `LifeRatio` column; both `LifeMax` and `Life` use
`LifeMaxRatio`.

### 3.2 Live role attributes (base + equipment + skills + chains + echoes)

When building `PbGetRoleListNotify` (§2.3), building/refreshing a role entity's
`AttributeComponentPb`, or computing echo display bonuses (`RolePhantomPropInfo`), the server
computes two arrays of length 143: `base_props` and `add_props`.

#### Config `PropId` to `EAttributeType` mapping

Attribute contributions in `WeaponConf`, `WeaponBaseAttributes`, `SkillTree`, `ResonantChain`,
`PhantomMainPropItem`, `PhantomSubProperty`, and `PhantomFetter` carry a config `PropId`:

- `0` -> ignore.
- `10002` -> `LifeMax` (2)
- `10007` -> `Atk` (7)
- `10010` -> `Def` (10)
- Any other `id >= 10000` -> `id - 10000`
- Otherwise -> `id`
- Ignore if the mapped value is `None` (0), `MAX` (143), or outside `1..142`.

#### Step 1: Collect contributions

Each contribution has `(source_type, attr, is_ratio, value_i32, ratio_f64, display_mode)` where
`display_mode` (`hidden` / `base` / `add`) only affects `RolePhantomPropInfo` (§3.3).
Non-ratio floating-point values round to `i32` with **round-half-away-from-zero** (`round(f64)`).

1. **Equipped weapon** (`role.weapon` -> `weapon/<uniqueId>` -> `WeaponConf` by `weapon.id`,
   `source_type = weapon`, `display_mode = hidden`):
   - **Primary stat** (`FirstPropId`, `FirstCurve`): if `FirstPropId.Id != 0`, look up
     `WeaponPropertyGrowth` matching `(CurveId == FirstCurve, Level == weapon.level,
     BreachLevel == weapon.breach)`; if missing, skip.
     `scaled = FirstPropId.Value * f64(CurveValue) / 10000.0`.
     If `FirstPropId.IsRatio`: `is_ratio = true`, `ratio = scaled` (already a decimal ratio in
     `BinData`, **not** divided by 10000 again).
     Else: `is_ratio = false`, `value = round_i32(scaled)`.
   - **Secondary stat** (`SecondPropId`, `SecondCurve`): identical to primary stat.
     Note ([data-tables.md](data-tables.md) §7.1): `WeaponConf.SecondPropId.Value` is a float32
     column in ConfigDB read as float64; use the shortest decimal that round-trips the float32
     (e.g. `0.081`) so attribute arithmetic stays exact.
   - **Refinement base attributes** (`WeaponBaseAttributes` by `weapon.id`):
     Look up `raw = Values[weapon.reson_level]`. For each `attr_id` in `Attributes`:
     If `IsRatio`: `is_ratio = true`, `ratio = f64(raw) / 10000.0`.
     Else: `is_ratio = false`, `value = raw`.
   - **No custom weapon mods**: all weapons use game data values directly.

2. **Active skill-tree nodes** (`role.skill_node_state` where `active == true`,
   `source_type = skill`, `display_mode = hidden`):
   For each `Property` item on `SkillTree(node_id)`:
   - If `Property.IsRatio`: `is_ratio = true`, `ratio = Property.Value` (already a decimal ratio;
     apply §7.1 float64 rule).
   - Else: `is_ratio = false`, `value = round_i32(Property.Value)`.

3. **Unlocked resonance chains** (`ResonantChain` rows where
   `GroupId == RoleInfo.ResonantChainGroupId` and `GroupIndex <= role.resonant_chain_group_index`,
   `source_type = chain`, `display_mode = hidden`):
   For each `AddProp` item:
   - If `AddProp.IsRatio`: `is_ratio = true`, `ratio = AddProp.Value`.
   - Else: `is_ratio = false`, `value = round_i32(AddProp.Value)`.

4. **Equipped echoes** (role's 5 echo slots `0..4`, `source_type = echo` or `fetter`):
   - **Main & sub properties**:
     - Each `main_prop` -> `PhantomMainPropItem(prop.id)`: `is_ratio = (AddType == 2)`.
     - Each `sub_prop` -> `PhantomSubProperty(prop.id)`: `is_ratio = (AddType == 2)`.
     - If `is_ratio`: `ratio = f64(prop.value) / 10000.0`, `display_mode = add`.
     - Else: `value = prop.value`, `display_mode = base`.
   - **Active Sonata (fetter) set bonuses**:
     Group equipped echoes by `echo.fetter_group_id`, counting **distinct `PhantomItem.MonsterId`**
     per fetter group (duplicate echoes of the same monster do not count twice). For each
     `PhantomFetterGroup(fetter_group_id)` entry `(fetter_id -> required_count)` in `FetterMap`,
     if `distinct_count >= required_count`, `fetter_id` is active:
     - For each `AddProp` on `PhantomFetter(fetter_id)`:
       If `IsRatio`: `is_ratio = true`, `ratio = AddProp.Value`, `display_mode = add`.
       Else: `is_ratio = false`, `value = round_i32(AddProp.Value)`, `display_mode = base`.
       (Apply §7.1 float64 rule to `PhantomFetter.AddProp[].Value`.)
   - **Echo & fetter passive buff attribute modifiers**:
     Collect `active_tags` = union of `Buff.GrantedTags` from all active role auto-buffs (§5.4)
     plus all equipped echo marker buffs. An echo's **marker buff** uses the effective
     `PhantomItem` (if `echo.skin_id != 0` and `PhantomItem(skin_id).ParentMonsterId ==
     base_item.MonsterId`, use `PhantomItem(skin_id)`, else `base_item`): if `SkillId != 0`,
     `marker_id = SkillId * 1000 + 1` qualifies when `Buff(marker_id)` exists, has non-empty
     `GrantedTags`, and has `ExtraEffectID == 0`.
     Inspect (deduplicated by `buff_id`, in order):
     1. Each equipped echo's marker buff,
     2. Slot-0 (main) echo's `PhantomSkill(effective_item.SkillId).BuffEffects[]` (last row wins
        for duplicate `PhantomSkillId`, [data-tables.md](data-tables.md) §2),
     3. Each active `PhantomFetter.BuffIds[]`.
     For each `buff_id`, if `Buff(buff_id)` passes `OngoingTagRequirements` / `OngoingTagIgnores`
     against `active_tags` (dot-prefix hierarchy via `GameplayTagParent`), and
     `GameAttributeID` is in `1..142`, and `ModifierMagnitude` is non-empty:
     add flat contribution `(attr = GameAttributeID, is_ratio = false,
     value = ModifierMagnitude[0], display_mode = add)`.

#### Step 2: Two-pass accumulation

Start with `base_props = copy(role.base_prop)` and `add_props = zeros(143)`.

- **Pass 1 (flat weapon contributions -> `base_props`)**:
  For every contribution with `source_type == weapon && !is_ratio`:
  - `base_props[attr] += value`
  - If `attr == LifeMax`, also `base_props[Life] += value`.

- **Pass 2 (all other contributions -> `add_props`)**:
  For every contribution NOT applied in Pass 1:
  - `delta = is_ratio ? round_i32(f64(base_props[attr]) * ratio) : value`
    (ratios multiply post-Pass-1 `base_props[attr]`).
  - `add_props[attr] += delta`
  - If `attr == LifeMax`, also `add_props[Life] += delta`.

#### Step 3: Applying to a live role entity

Each attribute slot on a live entity stores `(base, increment, current)`:
`base = base_props[i]`, `increment = add_props[i]`, `current = base + increment`.

When refreshing an already-spawned role entity after an equipment, skill-node, or resonance
chain change:
1. Overwrite all 143 entity attribute slots from the recomputed `(base_props, add_props)`
   (this also resets `Life` to the recomputed `base + increment`).
2. Mark the entity's FSM dirty for attribute changes.
3. Push `CombatReceivePackNotify` containing `AttributeChangedNotify` with all 143 attributes
   (§3.3.2), and push `PbGetRoleListNotify` containing the updated `roleInfo` for that role.

### 3.3 Wire encodings for attributes (3.8 client field rules)

1. **`AttributeComponentPb`** (inside `EntityPb`, R-143f `entities.md`):
   - `HardnessModeId` (#2), `RageModeId` (#3), `WhiteGreenProps` (#4,
     `repeated WhiteGreenPropData`: `AttributeType` #1, `WhiteValue` #2, `GreenValue` #3,
     `CurrentValue` #4).
   - For the 25 **state attributes**, `WhiteValue = current`; for all other attributes,
     `WhiteValue = base`. In all cases `GreenValue = increment`, `CurrentValue = current`.
   - The 25 state attributes: `Life` (3), `Strength` (15), `Hardness` (16), `Rage` (18),
     `Tough` (22), `Energy` (25), `ElementPower1..6` (35..40), `StatusBuildUp1..5` (42..46),
     `ElementEnergy` (104), `SpecialEnergy1..5` (113..117), `ParalysisTime` (118),
     `WeaknessBuildUp` (138).

2. **`GameplayAttributeData`** (inside `AttributeChangedNotify`):
   - 3.8 wire fields: `BaseValue` (#1), `CurrentValue` (#2), `AttributeType` (#3).
   - Put `attr.base` in `BaseValue` (#1) for `LifeMax` (2), `Atk` (7), and `Def` (10),
     and `attr.current` in `BaseValue` (#1) for all other attributes; `CurrentValue` (#2)
     always receives `attr.current`.

3. **`RolePhantomPropInfo`** (in `PhantomItemResponse.PropInfo` and `RolePhantomPropUpdateNotify`):
   - `RoleId` (#1), `BaseProp` (#2), `AddProp` (#3).
   - Carries **only echo and fetter contributions** (`display_mode != hidden`), summed per
     attribute id: `display_mode == base` (non-ratio echo/fetter stats) goes to `BaseProp`;
     `display_mode == add` (ratio echo/fetter stats evaluated against post-Pass-1 `base_props`,
     plus echo/fetter buff contributions) goes to `AddProp`.

---

## 4. `Buff.GameAttributeID` and `EAttributeType` validation

- **Protocol enum**: 3.8 `EAttributeType` has **144 values, contiguous `0..143` with zero gaps**:
  `0 = None`, `1..142` = gameplay attributes, `143 = MAX`.
- **Game data (`Buff.json`)**: across all 27 980 rows in 3.8 data, `GameAttributeID` takes 99
  distinct values, all in `0..142` (`0` = no attribute modifier; `143 = MAX` never appears).
- **Loader rule**: validate `0 <= Buff.GameAttributeID <= 143` at load time (rejecting any value
  outside it as a load error); at runtime, `0 (None)` and `143 (MAX)` are ignored.

---

## 5. Role entity values (equipment, skills, and initial buffs)

When [entities.md](entities.md) constructs a role's `EntityPb`, the values owned by this spec are:

### 5.1 `AttributeComponentPb`, `EquipComponentPb`, skin components
- `AttributeComponentPb`: populated from §3.2–§3.3.
- `EquipComponentPb`: `WeaponId = weapon.id`, `WeaponBreachLevel = weapon.breach`.
- Skin components: `role_skin_id`, `paragliding_skin_id`, `soar_skin_id`, `weapon_skin_id`,
  `calabash_skin_id`, and active `ornament_ids` for `role_skin_id`.

### 5.2 `SkillComponentPb` (`EntityComponentPb` #39)
For a player role entity, populate `GaPassiveSkills` / `SkillComponentPb` from:
1. All `EntitySkillPreload.SkillId` rows whose decimal string starts with `roleId`;
2. Pipe-separated (`|`) integer skill ids from `Buff.ExtraEffectParameters[0]` of every buff
   in the role's filtered `FightBuffInformation` list (§5.3) with `ExtraEffectID == 35`;
3. Fixed skill id `100005`;
4. Vision summon skills (`EntitySkillPreload` for main and combo echo summons, plus active
   `VisionSkillComponent` skills);
5. Common role skills from `EntitySkillPreload`.
(For AI/monster passive skills, see `server/internal/combat/passives.go`.)

### 5.3 `FightBuffComponentPb` — initial buffs on a role entity

Each initial buff produces a `FightBuffInformation` with a monotonic scene `HandleId`,
`Level = 1`, `StackCount = 1`, `InstigatorId = entity_net_id`, `EntityId = entity_net_id`,
`Duration = -1.0`, `LeftDuration = -1.0`, `ApplyType = Common`, `MessageId = -1`,
`ServerId = 0`, and `IsActive` set as specified below.

Sources assembled for a role entity:

1. **Role own buffs** (cacheable per `roleId` for a loaded table set, R-131):
   A buff starts **active at spawn** iff all 9 lists on `Buff` are empty:
   `ModifierMagnitude`, `BuffAction`, `OngoingTagRequirements`, `OngoingTagIgnores`,
   `RemovalTagRequirements`, `RemovalTagIgnores`, `ApplicationSourceTagIgnores`,
   `ApplicationTagIgnores`, `ExtraEffectRequirements`.
   - If `CharacterInitInfo(roleId)` exists and `FightBuffs` is non-empty: add all `FightBuffs`
     and `AdditionalFightBuffs` present in `Buff`, with `IsActive` set by that rule.
   - Otherwise (**autobuff fallback** for the 13 playable 3.8 roles in `1000..1999` absent from
     `CharacterInitInfo`): scan `Buff` for rows where:
     - decimal `Id` starts with `roleId` and has length `> 4`;
     - `DurationPolicy == Infinite`;
     - `ApplicationSourceTagRequirements`, `ApplicationTagRequirements`, and
       `PrematureExpirationEffects` are all empty;
     - suffix `id_str[4..]` does NOT start with any of `666, 990, 777, 095, 402, 403, 302,
       291, 98, 009, 020`;
     - no `GrantedTags` entry contains any of `Rogue`, `肉鸽`, `试用`, `零能量`, `59b4b0a5`,
       `371b7a72`, `角色.R2T1JiyanMd10011.技能标识.大招状态`, `23d0723c`, `20b04084`, `8c658780`,
       `9296f07f`, `c79fde44`, `fbcbee28`, `ed839a56`, `功能.功能制作.禁止体力恢复`;
     - if `ExtraEffectID == 5`: `ExtraEffectParameters.len > 1`, and if the integer prefix
       before `#` in `ExtraEffectParameters[1]` resolves to a `Buff` row, that referenced buff
       has `CalculationPolicy.len > 0 && CalculationPolicy[0] != 0`;
     then remove any buff id appearing in:
     - `ResonantChain.BuffIds` for `GroupId == roleId`;
     - `RogueResBuff.BuffId` / `BuffIds` whose decimal string starts with `roleId`;
     - numeric `HonamiStoryEffect.Param` entries of length `>= 7`;
     - `ComboTeaching.AddBuffID`;
     - `#`-separated ids in `ExtraEffectParameters[3]` of any candidate with `ExtraEffectID == 6`;
     - `SkillBranchBuff.Buffs` for `RoleId == roleId`.
     Remaining candidates receive `IsActive` from the same 9-empty-lists rule.
2. **Common fixed buffs (10 ids, `IsActive = true`)**:
   `3003, 3004, 3127, 3134, 1213, 1214, 1215, 1216, 1001000000, 70000077`.
3. **Skill branch buffs** (`IsActive = true`): `SkillBranchBuff(role.skill_branch).Buffs[]`.
4. **Rover gender tag buff** (`IsActive = true`): if `roleId` is in `MainRoleConfig`, add
   `291724064` (`Gender == 1`, male) or `291724065` (`Gender == 0`, female).
5. **Concerto energy buff** (`IsActive = true`): add `1101002011` **unless** `roleId` is in
   `[1211, 1108, 1210, 1306, 1608, 1507, 1107, 1405, 1403, 1308, 1311]`.
6. **Equipped weapon refinement buffs** (`IsActive = true`): `WeaponReson(WeaponConf.ResonId,
   weapon.reson_level).Effect[]`.
7. **Element buffs** (`IsActive = true`, `el = RoleInfo.ElementId`):
   `1101007999 + el`, `1001001015 + el * 2`, `3080 + el`.
8. **Unlocked resonance chain buffs** (`IsActive = true`):
   matches `ResonantChain.GroupId == RoleInfo.ResonantChainGroupId` for
   `GroupIndex <= role.resonant_chain_group_index` and adds `BuffIds[]`.
9. **Equipped ornament buffs** (`IsActive = true`): `Ornament.OrnamentBuff[]` for ornaments
   equipped on `role.role_skin_id`. `Ornament.OrnamentUiBuff[]` goes into
   `FightBuffComponentPb.NeedClientApplyBuffIds` (#3) with
   `NeedClientApplyBuffIdsMessageId` (#4) = `entity_net_id`.
10. **Active echo & Sonata buffs** (`IsActive = true`): marker buffs, slot-0
    `PhantomSkill.BuffEffects[]`, and active `PhantomFetter.BuffIds[]` (deduplicated).
11. **Post-processing**: sort ascending by `BuffId`, and **exclude** any buff id that appears in
    `SummonCfg.BornBuffId` of any custom concomitant entity spawned for this role.

### 5.4 Role concomitant and vision summon templates (R-143f handoff)

R-143f (`entities.md`) builds the concomitant and vision entities attached to a role via
`ConcomitantsComponentPb`; this spec defines **which** templates and initial buffs they use:

- **Custom concomitant templates** (cacheable per `roleId`, R-131):
  1. Find the first `RoleSkin` row with `RoleSkin.RoleId == roleId` and split
     `RoleSkin.UiScenePerformanceABP` on `'/'`; take segment index `6` (0-based) as `model_name`.
  2. Scan `BlueprintConfig` for rows where `EntityType == "Monster"`, `BlueprintType` contains
     both `"Player"` and `"_"`, and `ModelConfigPreload(ModelId).ActorClassPath` contains
     `model_name` (ASCII case-insensitive).
  3. Match `SummonCfg` where `SummonCfg.BlueprintType == BlueprintConfig.BlueprintType` and the
     first `TemplateConfig` with the same `BlueprintType`.
  4. Spawn count per matched concomitant: `3` for role `1105`, `4` for role `1301`, `1` for all
     other roles. Each concomitant's initial `FightBuffComponentPb` receives
     `SummonCfg.BornBuffId[]`.
- **Vision summon entities**:
  - Main vision: slot-0 equipped echo's `PhantomItem.SkillId` looked up in `SummonCfg` ->
    first matching `TemplateConfig` by `BlueprintType`; `BornBuffId[]` populates its
    `FightBuffComponentPb`.
  - Combo vision: if `PhantomSummonGroup` matches the role's equipped echo monster set, spawns
    the second vision entity the same way.
  - Both net ids populate `ConcomitantsComponentPb.VisionEntityIds` (#1) and
    `VisionSkillComponentPb` (`VisionSkillInfos` #1, `PhantomSkillInfo` #2).

---

## 6. Role requests

Requests with no handler receive an empty reply ([network.md](network.md)). Notably, there are
**no** handlers for role level-up or breakthrough (`PbUpLevelRoleRequest`, `PbOverRoleRequest`;
all roles start at `MaxLevel` 90 and breakthrough `6`, so the client never offers them).

Costs: paid per [inventory.md](inventory.md) §2.4; short → `ErrItemNotEnough` (200099, 道具数量不足)
unless noted otherwise. Level `L` of a skill uses the `SkillLevel` row with
`SkillLevelGroupId == Skill.SkillLevelGroupId` and `SkillId == L` (that field is the level).
Node types (`ESkillTreeNodeType`): 1 `InnerPassiveSkill`, 2 `InnerSkill` level up through §6.3 /
§6.6; 3 `OuterPassiveSkill`, 4 `OuterAttribute` activate through §6.4. Unlock conditions are not
checked (the client checks them).

### 6.1 `RoleFavorListRequest` (22639) -> `RoleFavorListResponse` (28736)
- Replies `ErrCode` (#14) = `Success`, `FavorList` (#2) = same full-unlock `RoleFavor` list as
  `RoleFavorListNotify` (§2.4).

### 6.2 `RolePropRequest` (29283) -> `RolePropResponse` (16928)
- Request: `RoleIds` (#13, `repeated int32`).
- For each owned `roleId` in `RoleIds` (unknown ids skipped), builds `RolePropInfo`:
  `RoleId`, `BaseProp` = raw saved `role.base_prop`, `AddProp` = raw saved `role.add_prop`
  (`PhantomProp` and equipment bonuses are not filled here).
- Replies `Error` (#5) = `Success`, `RoleProps` (#1).

### 6.3 `PbUpLevelSkillRequest` (25868) -> `PbUpLevelSkillResponse` (26023)
- Request: `RoleId` (#2), `UseBox` (#9, ignored), `SkillId` (#12).
- Validates role exists (`RequestParamError`) and `Skill(SkillId)` exists
  (`ErrSkillInfoParamError`).
- `next_level = min(current_level + 1, Skill.MaxSkillLevel)`. Pays the `SkillLevel` cost of
  `next_level` (none when already at max), else `ErrItemNotEnough`. Updates `role.skills` and
  enqueues dirty save (§7).
- Replies `Code` (#7) = `Success`, `RoleId` (#10), `SkillInfo` (#1) = `{ Key = SkillId,
  Value = next_level }`.

### 6.4 `RoleActivateSkillRequest` (17616) -> `RoleActivateSkillResponse` (17055)
- Request: `SkillNodeId` (#1), `RoleId` (#6).
- Validates role (`RequestParamError`) and `SkillTree(SkillNodeId)` (`ErrRolSkillNodeTypeUlock`).
- A node not yet active pays its cost first, else `ErrItemNotEnough`: `SkillTree.Consume` when
  `node.SkillId == 0`, else the skill's level-1 `SkillLevel` row. An active node is not paid
  again.
- Marks `SkillNodeId` active in `role.skill_node_state` (`skill_id = node.SkillId`), enqueues
  dirty save (§7).
- Pushes `RoleSkillNodeNotify` (24848) with the role's full `SkillNodeState`, refreshes the
  live role entity attributes + pushes `PbGetRoleListNotify` (§3.2 step 3), and replies
  `Code` (#13) = `Success`, `RoleId` (#11), `SkillInfo` (#2) = `{ Key = node.SkillId, Value = 1 }`.

### 6.5 `RoleSkillLevelUpViewRequest` (28071) and `RoleSkillViewRequest` (22013)
- Both validate `Skill(SkillId)` exists (else `ErrSkillInfoParamError`).
- `RoleSkillLevelUpViewResponse` (23907): `Code` (#2) = `Success`.
- `RoleSkillViewResponse` (17396): `Code` (#12) = `Success`, `IsConditionFinish` (#3) = `true`.

### 6.6 `RoleSkillQuickLevelUpRequest` (29438) -> `RoleSkillQuickLevelUpResponse` (24928)
- Request: `SkillId` (#1), `UseBox` (#5, ignored), `TargetLevel` (#7), `RoleId` (#11).
- Validates role (`RequestParamError`) and `Skill(SkillId)` (`ErrSkillInfoParamError`).
- `requested = TargetLevel > current ? TargetLevel : current + 1`;
  `next_level = max(min(requested, Skill.MaxSkillLevel), current)`. Pays the `SkillLevel` costs
  of every level `current + 1 … next_level` together, else `ErrItemNotEnough` with nothing taken.
  Updates `role.skills`, enqueues dirty save (§7).
- Replies `ErrorCode` (#10) = `Success`, `RoleInfo` (#7) = live `roleInfo` snapshot (§2.3).

### 6.7 `ResonantChainUnlockRequest` (17339) -> `ResonantChainUnlockResponse` (21735)
- Request: `RoleId` (#1).
- Validates role and `RoleInfo(RoleId)` (`RequestParamError`), finds `max_index` of
  `ResonantChain` rows with `GroupId == RoleInfo.ResonantChainGroupId` (`0` ->
  `RequestParamError`). If already `>= max_index`, replies `Success` with current index.
- Looks up `ResonantChain` for `next_index = role.resonant_chain_group_index + 1`.
- Pays `ActivateConsume` ([inventory.md](inventory.md) §2.4; money from the attributes), else
  `ErrRoleItemListNoEnough`.
- Increments `role.resonant_chain_group_index`, enqueues dirty save (§7), applies
  `new_chain.BuffIds` to the live role entity if spawned ([combat.md](combat.md) §8), refreshes
  live role attributes + pushes `PbGetRoleListNotify` (§3.2 step 3), and replies
  `ErrCode` (#7) = `Success`, `RoleId` (#10), `ResonantChainGroupIndex` (#11).

### 6.8 `RoleSkillBranchModifyRequest` (26316) -> `RoleSkillBranchModifyResponse` (23107)
- Request: `RoleId` (#14), `SkillBranch` (#15).
- Validates role (`ErrSkillBranchRoleValid`) and `SkillBranchBuff` rows for both the previous
  and new branch (`ErrSkillBranchNotValid`).
- If the role entity is spawned in the scene, removes previous branch buffs and applies the new
  branch's `Buffs[]`.
- Updates `role.skill_branch`, enqueues dirty save (§7), pushes `UpdateFormationNotify`, and
  replies `ErrorCode` (#3) = `Success`.

### 6.9 `RoleElementChangeRequest` (29639) -> `RoleElementChangeResponse` (17531)
- Request: `ElementType` (#2).
- Finds `target_role_id` in `MainRoleConfig` matching `(Gender == basic_info.sex,
  RoleInfo.ElementId == ElementType)` (else `ErrRoleChangeElementFunc`).
- Finds `source_role_id` (current active formation's Rover, else `selected_main_role_id`).
- Transfers Rover state from `source_role_id` to `target_role_id`: `level`, `exp`,
  `breakthrough`, equipped weapon (updating `weapon.role_id`), equipped echoes (updating each
  echo's `role_id`), `role_showlist`, and every formation slot + `cur_role`; sets
  `basic_info.selected_main_role_id = target_role_id`. Saves affected files.
- If the active formation contained the Rover, resets/rebuilds live role entities in the scene
  ([formation.md](formation.md) §4), re-registers buff timers, saves scene, and pushes
  `UpdateFormationNotify`.
- Pushes `EquipTakeOnNotify` (25950, if `weapon != 0`), `RoleChangeNotify` (18718,
  `SourceRoleId` #5, live `RoleInfo` #2), `RolePhantomPropUpdateNotify` (23146) +
  `PhantomEquipInfoNotify` (29430), and replies `ErrorCode` (#3) = `Success`.

### 6.10 `RoleSexChangeRequest` (17543) -> `RoleSexChangeResponse` (23208)
- Request: `Sex` (#7: `0` = female, `1` = male). If unchanged, replies `Success` + `Sex`.
- Resolves `source_role_id` and `target_role_id` (same `ElementId`, new `Gender`, else
  `ErrRoleSexFuncNotOpen`).
- Performs the same Rover state transfer as §6.9, sets `basic_info.sex = Sex`, saves
  `basic_info`, and syncs the account's login sex record ([save-format.md](save-format.md) §3).
- **Quest hook (R-76a, R-143h)**: updates the quest engine's player gender flag (`sex == 1`).
- If active formation changed, resets live role entities, saves scene, and pushes
  `UpdateFormationNotify`.
- Pushes `RoleChangeNotify` (18718) and `RoleChangeUnlockNotify` (20322, new gender's
  `MainRoleConfig` ids), replies `RoleSexChangeResponse{ ErrorCode = Success, Sex }`, and
  pushes `LogoutNotify{ Code = ErrSexChangeLogout }` so the client reloads with the new Rover.

### 6.11 `PlayerVoiceLanguageRequest` (22437) and `PlayerRoleVoiceSetRequest` (22391)
- `PlayerVoiceLanguageResponse` (27315): `ErrorCode` (#5) = `Success`, `RoleVoices` (#12) =
  `{ RoleId, VoiceId = role.voice_language - 1 }` for every role with `voice_language > 0`.
- `PlayerRoleVoiceSetRequest`:
  - If `RoleVoices` (#6) is **empty**: resets `voice_language = 0` on every role that had one
    set, enqueuing dirty saves.
  - Otherwise: for each `{ RoleId, VoiceLanguage }`, stores `role.voice_language =
    VoiceLanguage + 1` and enqueues dirty save if changed.
  - Replies `PlayerRoleVoiceSetResponse` (22358) `{ ErrorCode = Success }`.

### 6.12 `RoleDevelopConfigRequest` (29877) and `TeamRecommendRequest`
Implemented in `server/internal/combat/roles.go` using tables `RoleDevProsList` and
`RoleDevProsProject` ([data-tables.md](data-tables.md) §6).

### 6.13 Granting a role (hook for gacha/quests, R-38)
When another subsystem grants a role already owned by the player, `RoleInfo.SpilloverItem` gives
the duplicate conversion items ([inventory.md](inventory.md) §2). When a newly granted role is
not yet owned, initialize it per §1, ensure its weapon, save it, and push `PbGetRoleListNotify`.

---

## 7. Deferred saving

Role mutations (`PbUpLevelSkillRequest`, `RoleActivateSkillRequest`, `RoleSkillQuickLevelUpRequest`,
`ResonantChainUnlockRequest`, `RoleSkillBranchModifyRequest`, `PlayerRoleVoiceSetRequest`,
base-prop repair) enqueue the modified `roleId` on a **30-second (`30 000 ms`) dirty-save
queue** timed from the first mutation since last flush. The queue flushes immediately on scene
switch and on `Released(session)`.

---

## 8. Tests

- **New-account seeding**: empty `player/<id>/` creates all 161 `RoleInfo` records, assigns
  every role an equipped weapon, and sends `PbGetRoleListNotify` containing only `1000..1999`
  roles with one Rover matching `selected_main_role_id`.
- **Attribute formulas**: unit tests verifying exact integer calculations for:
  - `BaseProperty` + `RolePropertyGrowth` float32 truncation (`LifeMax`, `Life`, `Atk`, `Def`);
  - `WeaponConf.FirstPropId` + `SecondPropId` (including §7.1 `0.081` float32 round-trip) +
    `WeaponBaseAttributes` Pass 1 flat addition into `base_props`;
  - Pass 2 ratio multiplication against post-Pass-1 `base_props` (weapon ratio, skill tree,
    resonance chain, echo main/sub, Sonata fetter `AddProp` with duplicate-monster
    deduplication, and echo/fetter buff `ModifierMagnitude[0]`) and `LifeMax` -> `Life` mirror;
  - `RolePhantomPropInfo` containing echo+fetter bonuses only (`BaseProp` vs `AddProp` split);
  - `WhiteGreenPropData` (25 state attributes vs stats) and `GameplayAttributeData` field #1/#2.
- **R-143e gap closure**: test asserting `Aki.Protocol.EAttributeType` is contiguous `0..143`
  (`None == 0`, `MAX == 143`) and that the `Buff` table loader rejects `GameAttributeID < 0` or
  `> 143` while loading all 27 980 rows of 3.8 `Buff.json`.
- **Requests**: skill upgrade, node activation (`RoleSkillNodeNotify` + attr sync), quick level
  up, resonance chain unlock (`ActivateConsume` deduction + insufficient-item error), skill
  branch switch, Rover element & sex change (weapon/echo/formation transfer + `LogoutNotify`),
  voice language set/clear-all, and R-123 `RoleDevelopConfigRequest` / `TeamRecommendRequest`.
- **Costs (R-158, `combat/costs_test.go`)**: cost maps merge; skill level-up, quick level-up
  (levels summed) and node activation pay 贝币 from the attribute and items from the bag, short →
  `ErrItemNotEnough` with nothing taken, an item reaching 0 → `NormalItemRemoveNotify`, an active
  node is not paid again; on the 3.8 data every type-1/2 skill has a 贝币 cost for levels
  2 … max.
- **Dirty save queue**: mutations flush immediately on `Released(session)` and scene change.
