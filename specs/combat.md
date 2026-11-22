# Combat: combat pack, monster HP, damage, kills, drops, revive, buffs, and relays

Boundary with entities/world: [entities.md](entities.md), [spawning.md](spawning.md), and
[movement.md](movement.md) own entity creation, net ids, positions, open-world spawning, and
movement updates; this spec owns monster attributes and HP, combat pack dispatch, damage, fall
damage, kills and drops, death/revive/beacon healing, buffs, and combat relays.

---

## 1. What the client decides by itself (do not invent a server combat simulation)

The server pushes an **empty** `SwitchBattleModeNotify` (24840) after scene join (both
`ServerControllerModules` #4 and `ClientControllerModules` #13 empty). In the 3.8 client,
client-side control defaults to `true` for every battle module (`Damage`, `GameplayEffect`, `Log`).

Accordingly, **the client decides all of the following locally**, and the server must **not**
simulate or gate them:

- Hit detection, bullet trajectories, hitboxes, hurtboxes, and AoE overlap.
- Movement, jumping, climbing, dodging, perfect-dodge slow-motion windows, and parries/counters
  (the client reports hits, dodges/counters, and time dilation after the fact).
- Skill animations, montages, combo branching, skill cooldowns, stamina/forte consumption, and
  most energy spending.
- Toughness / stagger reduction, hitstun, knockback, and visual/audio cues.
- Monster AI steering, pathfinding, target picking, and behavior-tree attack selection (the
  server tracks `AiHate` battle state and makes the AI state machines' Server transitions, §9.7).

**What the server does own**:
- Initial role & monster attributes and HP;
- Authoritative damage and heal calculation (`DamageExecuteRequest`, which the client sends on
  every hit/heal and applies from `DamageExecuteResponse`), plus fall damage
  (`LandingDamageRequest`);
- Entity death (`EntityLivingStatusNotify{ Dead }`), monster kill side effects (corpse removal,
  respawn timer, drops, quest/dungeon hooks), full-party wipe (`PlayerDeadNotify`), and revive
  / beacon healing;
- Buff application, stacking, instant/periodic attribute modifiers, and duration timers;
- Echoing / validating combat pack requests (`Hit`, `Skill`, `SwitchRole`, `AiHate`,
  `AnimationGameplayTag`, `CombineEntities`, `TimeCheck`, FSM).

---

## 2. Combat pack envelope and dispatch

Combat messages are batched inside:
- **`CombatMessage.CombatSendPackRequest`** (26497): ordered list of `CombatSendData`, each
  carrying either a `CombatPushData` (push, no response) or a `CombatRequestData` (request,
  tagged with `RequestId` and `CombatCommon`).
- **`CombatMessage.CombatSendPackResponse`** (21536): carries `ReceivePackNotify`
  (`CombatReceivePackNotify`), whose `Data` list holds all `CombatResponseData` and
  `CombatNotifyData` generated while processing the pack.
- **`CombatMessage.CombatReceivePackNotify`** (29973): server-initiated combat notifications
  sent outside a `CombatSendPackResponse` (e.g., buff expiry, landing damage, beacon heal,
  equipment attribute sync).

### Dispatch rules

1. Process each `CombatSendData` entry in arrival order.
2. Each handled request appends its `CombatResponseData` (echoing the request's `RequestId` and
   `CombatCommon`) plus any generated `CombatNotifyData` into the reply's `ReceivePackNotify`.
3. **Unhandled inner messages**: log (`unhandled combat message: <name>`) and ignore without
   failing the pack or fabricating a response.
4. **`CombatMessagePrefixInfoRequest` / `CombatMessagePostInfoRequest`**: placeholder handlers
   returning empty responses (default `ErrorCode = Success`).

---

## 3. Monster attributes and HP (R-126)

When R-143f spawns a non-player combat entity (`Monster`, `Animal`, `Npc`, or `Vision` from
`LevelEntityConfig` / `TemplateConfig`), its `AttributeComponent` (143 `EAttributeType` slots)
is computed as follows:

### 3.1 Property row (`BaseProperty`)
Resolve `PropertyId` in order:
1. Entity's `AttributeComponent.PropertyId` (from `ComponentsData` / template) if present, not
   disabled, and `> 0`;
2. Entity's `BaseInfoComponent.EntityPropertyId` if `> 0`;
3. Fallback `2` for `Animal`, `Monster`, `Npc`, and `Vision` (otherwise no attributes).

Copy the 128 matching columns of `BaseProperty(PropertyId)` into `base` (and `current = base`,
`increment = 0`).

### 3.2 Effective monster level
Determine `level` in order:
1. **Dungeon override (hook for R-143f/h)**: inside a dungeon instance whose
   `InstanceDungeon.EntityLevel > 0`, use `InstanceDungeon.EntityLevel`.
2. Entity's `AttributeComponent.Level` (if `>= 10`), clamped to `1..120`.
3. **Area world-level cap**: look up `Area(entity_area_id)` and walk up `Area.Father` until an
   `Area` row has `WorldMonsterLevelMax[world_level]` (`> 0`).
4. **World-level fallback table** (`world_level = 1..8`):
   `1 -> 10, 2 -> 21, 3 -> 32, 4 -> 43, 5 -> 54, 6 -> 65, 7 -> 76, 8 -> 85` (clamp
   `world_level` to `1..8`).

Set `attributes[Lv] = level`.

### 3.3 `MonsterPropertyGrowth` scaling and saturation (R-126)
Look up `MonsterPropertyGrowth` matching `(CurveId == MonsterPropGrowthId_or_0, Level == level)`
(where `MonsterPropGrowthId` comes from `AttributeComponent` config, defaulting to `0`).
If found, scale **11 attributes** using **64-bit intermediate multiplication with signed 32-bit
saturation** (R-126 — high-level bosses and hologram monsters overflow `i32` without saturation):

`scaled = clamp_i32(trunc_i64(i64(base_val) * i64(ratio) / 10000), i32.MIN, i32.MAX)`

Applied to both `base` and `current` for:

| Attribute | Growth column |
|---|---|
| `LifeMax`, `Life` | `LifeMaxRatio` |
| `Atk` | `AtkRatio` |
| `Def` | `DefRatio` |
| `HardnessMax` | `HardnessMaxRatio` |
| `Hardness` | `HardnessRatio` |
| `HardnessRecover` | `HardnessRecoverRatio` |
| `RageMax` | `RageMaxRatio` |
| `Rage` | `RageRatio` |
| `RageRecover` | `RageRecoverRatio` |
| `WeaknessBuildUpMax` | `WeaknessBuildUpMaxRatio` |

### 3.4 Initial `Life` normalization
After scaling, if `LifeMax.current > 0` and (`Life.current <= 0` or `Life.current >
LifeMax.current`), set `Life.base = LifeMax.current` and `Life.current = LifeMax.current`.

### 3.5 `PartComponentPb` (breakable and combinable monster parts)
When an entity's `ModelId` has a row in `CharacterPartConfig` ([data-tables.md](data-tables.md)
§6), build `PartComponentPb` (`EntityComponentPb` #5):
- `PartLifeInfos` (#1, `repeated PartInformation`), one per entry in `CharacterPartConfig.Parts`:
  - `PartId` (#1): `part.Index`
  - `LifeMax` (#3) and initial `LifeValue` (#2): `part.LifeRatio > 0 ? f32(entity_LifeMax) *
    part.LifeRatio : -1.0f`
  - `Activated` (#4): `part.BirthActivated`
  - `PartTag` (#5): `part.PartTagId`
- Active part tag counts (`part.ActiveTagId` summed over activated parts) are synced into the
  entity's `TagComponent`.
- A part allows `CombineEntitiesRequest` (§9.4) only when `part.CombineSocket` is non-empty.

---

## 4. Attribute mutation, clamping, and notification rules

Shared by damage, healing, fall damage, and buffs:

- **Dead-entity guard**: when mutating `Life`, if `Life.current <= 0`, do nothing (return empty
  change list). Revive and beacon healing restore dead entities by writing `Life` directly (§7).
- **Clamping**:
  - `AutoAttackSpeed`, `CastAttackSpeed`: clamp `[0, 20000]`.
  - `DamageReduce`: clamp `[-10000, 10000]`.
  - Any attribute with a paired `<Name>Max` attribute (`Life`, `Strength`, `Hardness`, `Rage`,
    `Tough`, `Energy`, `ElementEnergy`, `SpecialEnergy1..5`, `WeaknessBuildUp`, etc.): clamp
    `[0, <Name>Max.current]`.
  - Negatives allowed (no floor at `0`): `Crit`, `DamageReduce`, `DamageReducePhys`,
    `DamageReduceElement1..6`, `DamageResistancePhys`, `DamageResistanceElement1..6`,
    `ParalysisTimeRecover`.
  - All other attributes: clamp `>= 0`.
- **`LifeMax` ratio preservation**: when a buff changes `LifeMax`, preserve `ratio =
  f32(Life.current) / f32(old_LifeMax)` by setting `Life.base = Life.current =
  clamp(round_i32(f32(new_LifeMax) * ratio), 0, new_LifeMax)`.
- **Emitted combat notifications**: for each attribute mutation batch on `entity_id`, append
  two `CombatNotifyData` entries:
  1. `AttributeChangedNotify` (26859): `Attributes` (#6, `GameplayAttributeData`, encoding rule
     in [roles.md](roles.md) §3.3.2);
  2. `RecoverPropChangedNotify` (17789): `CurTime` (#6, real-clock Unix ms) and `Attributes`
     (#14, one `RecoverAttr{ AttrId #1, Ratio #2 = <Name>Recover.current, MaxValue #4 =
     <Name>Max.current, CurrentValue #5 = attr.current }` for each changed attribute in
     `{Hardness, Tough, ParalysisTime}`).

---

## 5. `DamageExecuteRequest` (18654) -> `DamageExecuteResponse` (16478)

Handled inside `CombatSendPackRequest` (§2).

1. Look up `Damage(request.DamageId)`. If missing, reply `ErrorCode` (#5) =
   `ErrConfDamageNotFound`.
2. Look up attacker (`request.AttackerEntityId`). If not in the scene, reply `Success` with
   `Damage = 1`, `ElementType = damage.Element`.
3. **Attacker energy & concerto gain** (runs on every hit/heal regardless of target lookup):
   - Concerto energy (`ElementEnergy`):
     `concerto = (damage.ElementPower.len > 0) ? trunc_i32(damage.ElementPower[0] *
     attacker.ElementEfficiency.current / 10000) : 0`.
     Add delta `concerto` to `attacker.ElementEnergy.current` and emit attribute notifies (§4).
   - Resonance Liberation energy (`Energy`):
     unconditionally adds delta **`+10_000_000`** to `attacker.Energy.current` (clamped to
     `EnergyMax`, giving instant full liberation energy on every hit) and emits attribute notifies
     (§4).
   - Mark attacker FSM dirty for attribute changes if either changed.
4. Look up target (`request.TargetEntityId`). If not in the scene, reply `Success` with
   `Damage = 1`, `ElementType = damage.Element`.

### 5.1 Helper: level-indexed array lookup
For any `Damage` array `values` and `skill_level = request.SkillLevel`:
- If `values` is empty -> `0`.
- Else index `clamp(skill_level - 1, 0, values.len - 1)`.

### 5.2 Healing (`damage.CalculateType == 1`)

`heal_bonus = max(0.0, 1.0 + f64(attacker.HealChange + target.HealedChange) / 10000.0)`.
`holder(flag)` = `attacker` if `flag == 0` else `target`.

| `damage.FormulaType` | `total` (`f64`) |
|---|---|
| `0`, `1`, or other | `(attacker[related_or_LifeMax] * (RateLv[lv] / 10000.0) + CureBaseValue[lv]) * heal_bonus` |
| `2` (heal up to threshold) | `max(0.0, holder(P2)[P1] * (P3 / 10000.0) + P4 - target.Life)` |
| `3` (single holder stat) | `holder(P2)[P1] * (P3 / 10000.0) + P4` |
| `5` (target max HP) | `target.LifeMax * (P1 / 10000.0) + P2` |
| `6` (two-stat sum) | `holder(P2)[P1] * (P3 / 10000.0) + holder(P5)[P4] * (P6 / 10000.0)` |

(`P1..P6` denote `FormulaParam1[lv]..FormulaParam6[lv]`; `related_or_LifeMax` is
`damage.RelatedProperty` if `> 0` else `LifeMax`.)

- `heal_amount = clamp_i32(ceil(total), 1, 2_000_000_000)`.
- Apply `+heal_amount` to `target.Life.current` (§4), copy `Life.base = Life.current`, emit
  attribute notifies, mark target FSM dirty.
- `actual_change = max(life_after - life_before, 0)`.
- Reply `Success` with `Damage = heal_amount`, `ChangeLife = (actual_change > 0 ? actual_change
  : heal_amount)`, `ElementType = damage.Element`, echoing `AttackerEntityId`,
  `TargetEntityId`, `PartId` (#13).

### 5.3 Non-damage / non-heal (`damage.CalculateType != 0 && != 1`, e.g. `2 = Cost`)
Reply `Success` with `Damage = 1`, `ElementType = damage.Element`, no HP change.

### 5.4 Damage (`damage.CalculateType == 0`)

All rates and bonuses are in basis points (`1/10000`). Compute in `f64`:

1. `related = damage.RelatedProperty > 0 ? damage.RelatedProperty : Atk`.
   `scaling_stat = f64(attacker[related].current)`.
2. `rate = f64(RateLv[lv]) / 10000.0`.
3. `bonus = 1.0 + f64(attacker.DamageChange + attacker[element_bonus(damage.Element)] +
   attacker[ability_bonus(damage.Type)]) / 10000.0` (R-31: weapon mod bonus is `0`):
   - `element_bonus`: `0 -> DamageChangePhys`, `1..6 -> DamageChangeElement1..6` (default Phys).
   - `ability_bonus`: `0 -> DamageChangeAuto`, `1 -> DamageChangeCast`, `2 -> DamageChangeUltra`,
     `3 -> DamageChangeQte`, `4 -> DamageChangeNormalSkill`, `5 -> DamageChangePhantom`,
     `6..12` / other -> `0` (`None`).
4. **Crit**: the 3.8 client sends `RandomSeed = 0`, so the server rolls its own PRNG
   (`rng.next_int(0, 10000)`):
   `is_crit = request.ForceCritical || roll < attacker.Crit.current`.
   `crit_mult = is_crit ? max(f64(attacker.CritDamage.current) / 10000.0, 1.0) : 1.0`.
5. **Defense**:
   `attacker_lv = f64(max(attacker.Lv.current, 1))`;
   `ignore_def = clamp(f64(attacker.IgnoreDefRate.current) / 10000.0, 0.0, 1.0)`;
   `eff_def = f64(max(target.Def.current, 0)) * (1.0 - ignore_def)`;
   `def_base = 800.0 + 8.0 * attacker_lv`;
   `def_mult = def_base / (def_base + eff_def)`.
6. **Resistance**:
   `res_raw = target[element_res(damage.Element)] - attacker[element_pen(damage.Element)]`;
   `res_mult = clamp(1.0 - f64(res_raw) / 10000.0, 0.1, 2.0)`
   (`0 -> Phys`, `1..6 -> Element1..6`).
7. **Damage reduction**:
   `reduce_raw = target.DamageReduce + target[element_reduce(damage.Element)]`;
   `reduce_mult = clamp(1.0 - f64(reduce_raw) / 10000.0, 0.0, 2.0)`.
8. **Final amount**:
   `total = scaling_stat * rate * bonus * crit_mult * def_mult * res_mult * reduce_mult`;
   `amount = clamp_i32(round_half_away_from_zero(total), 1, 2_000_000_000)`.

Apply `-amount` to `target.Life.current` (§4), set `Life.base = Life.current`, emit attribute
notifies, mark target FSM dirty.

### 5.5 Kill detection
`killed = (life_before > 0) && (target.Life.current <= 0)`. When `killed` is `true`:
1. Append `EntityLivingStatusNotify` (24917: `Id = target_net_id`, `LivingStatus = Dead`) to
   the combat pack.
2. Remove `target_net_id` from the scene's active-combatant set (updating
   `PlayerBattleStateChangeNotify` if the last active combatant left, §9.2).
3. If target is a **monster**: trigger the shared monster-killed path (§6).
4. If target is a **player role** and **every** player role entity in the scene now has
   `Life.current <= 0`: push `PlayerDeadNotify` (23671):
   `PlayerId = scene.player_id`, `DelaySeconds = 0`, `IsAutoRevive = true`, `ReviveId = 2`,
   `IsShowRevive = false` (the client plays the death animation ~3 s and sends `ReviveRequest`).

Reply `DamageExecuteResponse`: `ErrorCode = Success`, `Damage = amount`, `ChangeLife = 0` (for
damage), `IsCrit`, `KilledTarget = killed`, `AttackerEntityId`, `TargetEntityId`, `PartId`
(#13), `ElementType = damage.Element`.

### 5.6 Fall damage (`LandingDamageRequest` 27510 -> `LandingDamageResponse` 27519, R-118)
- **Always** reply `LandingDamageResponse{ ErrorCode (#10) = Success }` immediately (any non-
  `Success` code opens a client error popup, and entities the server does not track — e.g.
  client-side Vision entity 6 — simply take no damage).
- Only applies when `request.EntityId` (#8) resolves to a **player role** entity in the scene.
- **Formula and `CommonParam.json` (`landing_damage_args_role`)**: implemented in
  `server/internal/combat/calculators.go`.
- If HP lost `> 0`: update `Life` (`current` and `base`), mark FSM dirty, push
  `CombatReceivePackNotify` containing `AttributeChangedNotify` + `RecoverPropChangedNotify`
  (plus `EntityLivingStatusNotify{ Dead }` and removing from the active-combatant set if
  lethal). If lethal and all player roles are dead, push `PlayerDeadNotify` with the same
  fields as §5.5.

---

## 6. Monster kills and drops

The **shared monster-killed path** is triggered by `DamageExecuteRequest` (§5.5), and later
reused by dungeon `killall` and the robot's `killMonsters` (R-143h):

1. **Corpse removal (8 s)**: record the monster's net id as dead with `remove_at = now_ms +
   8_000`. During periodic world refresh, expired corpses are removed from the scene and pushed
   via `EntityRemoveNotify`.
2. **Open-world respawn (3 min)**: if the monster was an open-world spawn (not a transient
   dungeon/quest spawn), remove it from the active open-world spawn table and record a respawn
   timer `respawn_at = now_ms + 180_000` (3 minutes).
3. **Hooks for R-143h (quests / dungeons / level plays)**: notify quest kill counters,
   `EntityGroupComponent` triggers, prefab wave-cleared checks, and quest-owned entity sleep.
4. **Echo drop (`VisionCaptureComponent.VisionCaptureId`)**:
   - When the monster's `LevelEntityConfig` / `TemplateConfig` has `VisionCaptureId > 0`, pick
     the highest-`QualityId` `PhantomItem` with `MonsterId == VisionCaptureId` that passes
     valid-owned-echo checks ([inventory.md](inventory.md) §1).
   - Drop probability by `PhantomItem.Rarity` (1..4): `1 -> 40%`, `2 -> 60%`, `3 -> 100%`,
     `4 -> 100%` (else `100%`).
   - On drop: allocate unique id from `special_item/next`, pick a random `PhantomMainProperty`
     group and random `fetter_group_id` from `PhantomItem.FetterGroup[]`, level `0`, save
     `echo/<uniqueId>`, and push **`PhantomItemAddNotify`** (25657) directly into the player's
     bag (no ground absorbable phantom entity is spawned).
5. **Item drop (`RewardComponent.RewardId` -> `DropPackage.DropPreview`)**:
   - Look up `DropPackage(RewardId)`. Iterate `DropPreview` entries `(item_id -> value)`
     filtered to ids present in **`ItemInfo`** (R-109, R-116).
   - Slot drop probabilities by index: `[100%, 45%, 18%, 6%]` (slots beyond index 3 use `6%`).
   - Amount on drop: `value` if `value > 0`, else random `1..2`.
   - Add to `inventory_info` through the single inventory writer (R-108,
     [inventory.md](inventory.md) §2.1) and push **`NormalItemAddNotify`** (24714) /
     **`NormalItemUpdateNotify`** (27265) (filtered by R-109/R-116).

---

## 7. Death, revive at a beacon, and beacon healing

### 7.1 `ReviveRequest` (15137) -> `ReviveResponse` (24465)
- Request: `CustomParam` (#7), `UseItem` (#12) (both ignored).
- **Nearest beacon selection**: scan `LevelEntityConfig` rows on the current map whose
  `BlueprintType` starts with `"Teleport"` or equals `"branch2.0_Teleport6"` or
  `"branch2.2_14_Teleport4"` (skipping hidden/disabled rows); pick the one with minimum 3D
  Euclidean distance to the player's current position. If found, target position is
  `beacon.pos + (180, 180, 50)` cm (fallback: player's current position).
- Restore **every** player role entity in the scene:
  - Set `Life.current = LifeMax.current` and `Life.base = LifeMax.current`;
  - Update each role entity's position (and `scene.instance.players[0].location`) to the revive
    position.
- Reply `ReviveResponse{ ErrorCode (#14) = Success }`.
- Push **`PlayerReviveNotify`** (22493): `PlayerId` (#8), `Location` (#1), `Rotator` (#10),
  `ReviveType` (#11) = `0`, `NeedLoading` (#3) = `false`, and `ReviveRoleInfos` (#5) listing
  every role's `{ EntityId, CurHpAttribute }` (`GameplayAttributeData` for `Life`).
- Push **`CombatReceivePackNotify`** (29973) with, for every role entity:
  `EntityLivingStatusNotify{ Id, LivingStatus = Alive }` + `AttributeChangedNotify{ Life }` +
  `RecoverPropChangedNotify`.

### 7.2 Passive beacon healing on movement (`1500 cm` / 15 m radius)
Whenever movement updates the player's position (R-143f `movement.md`), if the player is within
`1500 cm` (squared distance `<= 1500^2`) of any beacon on the current map (same beacon predicate
as §7.1):
- For every player role entity in the scene whose `Life.current < LifeMax.current`:
  - Record `was_dead = (Life.current <= 0)`;
  - Set `Life.current = LifeMax.current` and `Life.base = LifeMax.current`;
  - Push `CombatReceivePackNotify` containing `EntityLivingStatusNotify{ Alive }` (if
    `was_dead`), `AttributeChangedNotify{ Life }`, and `RecoverPropChangedNotify`.

---

## 8. Buffs and buff timer scheduler

### 8.1 `OrderApplyBuffRequest` (21297) -> `OrderApplyBuffResponse` (15979)
Inside `CombatSendPackRequest` (§2); target entity is `CombatCommon.EntityId`:
1. Validate target entity exists (`ErrSceneEntityNotExist`), `Buff(request.Id)` exists, and if
   `Duration` (#15) is present it is finite and `<= max_i64 / 1000` seconds.
2. **`DurationPolicy == Instant`**:
   - Append `ApplyGameplayEffectNotify` (26040) with `Handle = -2`, `Id = request.Id`,
     `EntityId = target`, `InstigatorId`, `Level`, `StackCount`, `IsActive = true`.
   - Execute instant buff attribute effects (§8.3).
   - Reply `OrderApplyBuffResponse{ ErrorCode (#8) = Success }`.
3. **Non-instant (`Infinite` or `HasDuration`)**:
   - Effective duration (seconds): `request.Duration` if provided and `> 0`, else
     `Buff.DurationMagnitude[clamp(level-1)]` if `HasDuration`, else `-1.0`.
   - Look up existing buff on target `FightBuffComponent` by `BuffId`:
     - **New buff**: allocate monotonic `scene.buff_handle`, insert `FightBuffInformation`
       (clamping stack count to `1..max(StackLimitCount, 1)`), append
       `ApplyGameplayEffectNotify` (26040, `Duration` #1, `LeftDuration` #3), run immediate
       periodic tick if `Period > 0 && bExecutePeriodicEffectOnApplication`.
     - **Existing buff**: update stack count (`StackAppendCount` / `request.StackCount`, capped
       at `StackLimitCount`), refresh expiry if `StackDurationRefreshPolicy ==
       RefreshOnSuccessfulApplication`, reset period if `StackPeriodResetPolicy ==
       ResetOnSuccessfulApplication`, append `BuffStackCountNotify` (19317, `LeftDuration` #2,
       `Duration` #13) and `ActivateBuffNotify` (29719) if `IsActive` changed.
   - Register/update expiry and periodic deadlines in the buff timer scheduler (§8.4).
   - Reply `OrderApplyBuffResponse{ ErrorCode = Success }`.

**`OrderRemoveBuffByTagsRequest` -> `OrderRemoveBuffByTagsResponse`**: sent when the client has
no buff authority over the entity (the sender). Remove every buff of the entity whose
`Buff.GrantedTags` hold one of `TagIds` or a child of one (dot prefixes, then the
`GameplayTagParent` chain), only the `InstigatorId` ones when it is not 0; append
`RemoveGameplayEffectNotify` for each; reply `Success`. Entity missing:
`ErrSceneEntityNotExist` (the client then removes those buffs itself).

**`ChangeSummonEntityStateRequest` -> `ChangeSummonEntityStateResponse`**: reply `Success`; the
client only logs a failure. What the official server changes on the summon (`SummonEntityId`,
`TagId`) is unknown: nothing.

### 8.2 `ApplyGameplayEffectPush` (17445)
Inside `CombatSendPackRequest`: executes instant buff attribute effects (§8.3) for `request.Id`
on `CombatCommon.EntityId` and pushes any resulting attribute notifies in
`CombatReceivePackNotify`.

### 8.3 Buff attribute effects (`Buff.CalculationPolicy`)
Applies `Buff.GameAttributeID` (`1..142`; `0` and `143` are no-ops) using
`val = ModifierMagnitude[clamp(level-1)]`, `val2 = ModifierMagnitude2[clamp(level-1)]`:

| `CalculationPolicy[0]` | Effect on `target[GameAttributeID]` |
|---:|---|
| `0` (`AddValue`) | Delta `+val` (`Current` for state attrs, `Increment` for stats) |
| `1` (`AddRate`) | Delta `+round(base * val / 10000)` |
| `2` (`AddFromAttr`) | Delta based on referenced attribute (`val` / `val2`) |
| `3` (`OverrideValue`) | Override to `val` |
| `4`, `5`, `6`, `9`, `-1` | No-op |

Emit `AttributeChangedNotify` + `RecoverPropChangedNotify` (§4) on any change.

### 8.4 Buff timer scheduler (min-heap)
- Tracks per-entity `(handle_id, expire_at_ms, next_period_at_ms)` entries in a min-heap ticked
  on the server loop and synced before entity snapshots / role switches.
- **Periodic tick** (`Period > 0` s): executes §8.3 and advances `next_period_at_ms += period_ms`
  (respecting `PeriodicInhibitionPolicy`).
- **Expiration**:
  - If `StackExpirationRemoveNumber > 0` and `stack_count > StackExpirationRemoveNumber`:
    decrement stacks by `StackExpirationRemoveNumber`, reset expiry by full duration, and push
    `BuffStackCountNotify` (19317).
  - Otherwise remove the buff, push `RemoveGameplayEffectNotify` (18172), and apply any
    follow-up buffs in `Buff.RoutineExpirationEffects[]`.

### 8.5 Server-to-client equipment buff swaps
Weapon refinement/equip ([inventory.md](inventory.md) §5.4–§5.5) and echo equip/change
([inventory.md](inventory.md) §6.3) send inside `CombatReceivePackNotify`:
- `ApplyBuffS2cRequestNotify` (17614): `Reason` (#4) = `12` (weapon) or `7` (echo);
- `RemoveBuffS2cRequestNotify` (28392): `Reason` (#11) = `41` (weapon), `35` (echo), or `36`
  (fetter).
The client responds with `ApplyBuffS2cResponsePush` / `RemoveBuffS2cResponsePush` (handled as
no-op acknowledgements on the server).

---

## 9. Combat relays, battle state, time sync, and entity FSM

All messages below are carried inside `CombatSendPackRequest` (§2) unless marked top-level.

### 9.1 Hits and skills
- **`HitRequest` (17489) -> `HitResponse` (27996)**: echoes `HitInfo` (#13) and replies
  `ErrorCode` (#15) = `Success`.
- **`HitEndRequest` (19130) -> `HitEndResponse` (27143)**: replies `ErrorCode` (#9) = `Success`.
- **`HitEndPush` (15924)**: no-op.
- **`FragileChangeRequest` (22818) -> `FragileChangeResponse` (21843)**: the client
  sends it when a hit empties an entity's `Tough` (`Flag` (#14) = true: weak time starts, tag
  `Damage.Vulnerable`, the client sets `ToughRecover` to 0) and when the `WeakTime` timer ends
  (`Flag` = false). `EntityId` (#10), 0 = the sender. Always reply `ErrorCode` (#1) = `Success`
  (any other code makes the client drop the weak time). On `Flag` = false set the entity's
  `Tough` to `ToughMax` and append the §4 notifies for `Tough` (`AttributeChangedNotify` +
  `RecoverPropChangedNotify` with `Ratio` = `ToughRecover`): the client restores the rate only
  from that notify; without it `Tough` stays 0 and every hit staggers the entity. Expected
  behaviour: staggerable during the weak time, then Tough is full at once.
- **`SkillRequest` (28732) -> `SkillResponse` (17453)**: replies `ErrorCode` (#15) = `Success`.
- **`UseSkillRequest` (15873) -> `UseSkillResponse` (21045)**:
  - Notes skill start on the caster's entity FSM (§9.6);
  - **Quest hook (R-14, R-143h)**: reports `(entity, SkillId)` to the quest engine for
    `UseSkill` quest steps;
  - Echoes `UseSkillInfo` (#3) and `SkillSingleId` (#9) with `ErrorCode` (#7) = `Success`.
- **`EndSkillRequest` (26657) -> `EndSkillResponse` (24932)** and **`EndSkillPush` (18120)**:
  signals skill end to the caster's entity FSM (§9.6); request replies `ErrorCode` (#8) =
  `Success`, echoing `SkillSingleId` (#1) and `UseSkillInfo` (#14).

### 9.2 Battle state (`Ai.AiHateRequest` 27610 / `Ai.AiHatePush` 22440)
- `HateList` holds `Ai.AiHateEntity{ EntityId #1, HatredValue #2 }`.
- The sender entity (`CombatCommon.EntityId`) is tracked in the scene's active-combatant set
  iff at least one entry has `HatredValue >= 1` and targets an entity in the player's current
  formation; otherwise removed. Also updates the entity FSM's hate state.
- Whenever the active-combatant set transitions between empty and non-empty, push top-level
  **`PlayerBattleStateChangeNotify`** (15313: `PlayerId` #3, `InBattle` #14).
- `Ai.AiHateRequest` replies `Ai.AiHateResponse` (20048) `{ ErrorCode (#14) = Success }`.

### 9.3 Gameplay tags (`AnimationGameplayTagRequest` 20782 / `Push` 15510)
- Request/push fields in 3.8: `ChangedTagId` (`int32`) and `IsAdd` (`bool`).
- Increments (when `IsAdd == true`) or decrements (floor `0`, when `false`) the tag's reference
  count in the entity's `TagComponent` and wakes the entity FSM. Request replies
  `AnimationGameplayTagResponse` (18524) `{ ErrorCode (#1) = Success }`.
- Server-initiated tag changes emit `AnimationGameplayTagNotify` (17808: `IsAdd` #12,
  `ChangedTagId` #14).

### 9.4 Entity combining (`CombineEntitiesRequest` 28910 / `DissolveCombineRelationRequest` 20585)
- `CombineEntitiesRequest`: validates target & combiner entities exist (and `PartId` against
  `CharacterPartConfig` if present), records the combine relation, emits
  `AddCombineEntitiesRelationNotify` (24720: `TargetEntity` #3, `CombinePartInfo` #13), and
  replies `CombineEntitiesResponse` (16600) `{ ErrorCode (#12) = Success }`.
- `DissolveCombineRelationRequest`: removes relation between `EntityA` (#2) and `EntityB` (#3),
  signals the entity FSM, emits `RemoveCombineRelationNotify` (18044: `CombinerEntity` #8,
  `TargetEntity` #14), and replies `DissolveCombineRelationResponse` (20084) `{ ErrorCode (#5)
  = Success }`.

### 9.5 Time dilation and time check
- **`RTimeStopPush` (21080)** and **`TimeStopPush` (16707)**: record current dilation values
  (no response).
- **`TimeCheckRequest` (28799) -> `TimeCheckResponse` (28031)**:
  - `now_ms = real-clock Unix ms`.
  - Replies `Code` (#10) = `Success`, `ClientTime` (#15) = `request.ClientTime`,
    `ServerTime` (#1) = `now_ms`, `ServerStopTime` (#4) = `now_ms`, `SceneStopTime` (#13) =
    `now_ms`, `FlowTime` (#7) = `now_ms`.

### 9.6 `EntityFsmComponentPb` and entity FSM combat messages (scope note)
Entities whose `RoleBeHitMap` (for player roles) or `AiBase` / `AiStateMachineConfig` (for
monsters and bosses) specifies a state machine carry `EntityFsmComponentPb`
(`EntityComponentPb` #28):
- `Fsms` (#1, `repeated DFsm`): one entry per unique root FSM state in the graph's state list:
  `FsmId` (#1), `CurrentState` (#2, initial leaf state), `Flag` (#3: `Confirmed` if root has
  `IsAnimStateMachine == true`, else `Changed`), `StateElapseTime` (#6, elapsed ms, `0` at
  spawn).
- `HashCode` (#2): `StateMachineJson.Version` (keep the first `"Version"` key on duplicate
  JSON keys, [data-tables.md](data-tables.md) §7.2); `CommonHashCode` (#3): the common state
  machine's `Version`.
- `BlackBoard` (#4, `repeated DFsmBlackBoard{ Key #1, Value #2 }`); `FsmCustomBlackboardDatas`
  (#5, empty `BlackboardIntValues`).

The 3.8 client exchanges these FSM messages inside `CombatSendPackRequest`:
- `ChangeStateRequest` (20524) -> `ChangeStateResponse` (28519) + `ChangeStateNotify` (19168)
- `ChangeStateConfirmRequest` (17371) -> `ChangeStateConfirmResponse` (22660) /
  `ChangeStateConfirmPush` (24241)
- `FsmConditionPassRequest` (21831) -> `FsmConditionPassResponse` (18028) /
  `FsmConditionPassPush` (24334)
- `FsmStateBehaviorRequest` (23005) -> `FsmStateBehaviorResponse` (21048)
- `FsmPlayMontageRequest` (25815) -> `FsmPlayMontageResponse` (25141) /
  `FsmPlayMontagePush` (28981)
- `LogicStateInitRequest` (18260) -> `LogicStateInitResponse` (29984) /
  `LogicStateInitPush` (16642)
- `SwitchLogicStateRequest` (17062) -> `SwitchLogicStateResponse` (22141) /
  `SwitchLogicStatePush` (18461)

**Replies.** `LogicStateInit*`, `SwitchLogicState*`, `FsmPlayMontage*`, `FsmStateBehaviorRequest`
and `ChangeStateConfirm*` are answered with `Success` (echoing `FsmId` / `CurrentState` /
`State`, updating `LogicStateComponent` on `LogicStateInit` / `SwitchLogicState`).
`ChangeStateRequest` is answered with `Success` and echoed as `ChangeStateNotify`, and the
server's runtime (§9.7) follows the switch. `FsmConditionPass*` feeds §9.7. Not done:
`FsmBlackboardNotify`, `FsmResetNotify`, `FsmMontageDurationNotify`.

### 9.7 The server's AI state machine transitions (R-152)

An earlier version of this spec said server-driven state machines were needed only for scripted
bosses. That was wrong: every monster's sleep, idle and combat entry use transitions the server
must make, and without them wild monsters never move (R-152; 3852 Server transitions in 261 of
the 397 3.8 state machines).

**Who switches.** A transition's `TransitionPredictionType` is `Server` (0), `Autonomous` (1) or
`Simulated` (2). The client switches Autonomous (when it controls the actor, which it does for
monsters in single player) and Simulated transitions itself, and every transition out of a
conduit node (`IsConduitNode`), and tells the server with `ChangeStateRequest`. Server
transitions are the server's: the client reports each condition it judges (`IsClient` true) with
`FsmConditionPassPush { FsmId, FromState, ToState, ConditionIndex, Value }` whenever its value
changes and once when the From node becomes active (`Value` already has `Reverse` applied), and
waits for `ChangeStateNotify { FsmId, FromState, ToState }`. Animation state machines
(`IsAnimStateMachine` roots) are the client's alone.

**The graph**: nodes of `SM_Common`,
then the entity's machine (`AiBase.StateMachine`) over them; a node of the entity's with
`OverrideCommonUuid` X stands wherever X is used; a node with `ReferenceUuid` R is a reference:
R is built as a root of its own and is active while a reference to it is. Each
`StateMachines` entry (resolved through its reference) and each referenced node is a root. A
transition, listed on a parent, belongs to its From node (both ends resolved through
overrides). A node with Uuid 0 (one 3.8 machine has one) is skipped.

**Running**: the runtime starts at the `DFsm.CurrentState`s sent in
`EntityFsmComponentPb` (the parents become active without entering their first child). Switching
to a node exits its parent's current child and enters the node, which enters its first child,
recursively. A client switch (`ChangeStateRequest`) is applied the same way under `ToState`'s
parent. Entering a node clears the reported values of its transitions and starts their server
timers.

**Judging**, after every combat pack and at every heartbeat: for each active non-animation root,
in order, a node's Server transitions (none for conduit nodes), then its current child's; the
first one that holds switches, and this repeats (at most 8 passes) until none holds. Each switch
is sent as `ChangeStateNotify` with `FsmId` = the root's uuid (the node the client builds, not
the `StateMachines` entry: the client looks the root up by it) and the entity's
`EntityFsmComponentPb` current states follow. A transition is judged only when the client has
reported every `IsClient` condition reachable from its condition 0 since its From node was
entered: a missing report is unknown, not false (a reversed OR of client tags would otherwise
hold at once and switch the monster away). Conditions (`Reverse` negates the server's):

| Condition | Server's value |
|---|---|
| any with `IsClient` true | the client's last report |
| `CondAnd`, `CondOr` | over the listed indexes |
| `CondTrue` | true |
| `CondHate` | the entity has hate on a party role (§9.2) |
| `CondTimer` | `MinTime`..`MaxTime` ms (drawn when the From node was entered) have passed |
| `CondCheckState` / `CondCheckStateByName` | the target node (by uuid / name, overrides answer to both names) is active |
| `CondTag`, `CondInstStateChange` | the entity carries the tag (its spawn tags and §9.3) |
| `CondAttribute` | the attribute's current value is within `Min`..`Max` |
| `CondAttributeRate` | attribute / `Denominator` within `Min`..`Max` × 0.0001 |
| `CondBuffStack` | the stacks of `BuffId` on the entity are within `MinStack`..`MaxStack` |
| `CondPartLife`, `CondListenEvent`, `CondCheckLastState`, `CondCheckPartActivated`, `CondCheckDissolveCombine` | false (not judged) |

Not tracked: tags that buffs grant, the summoner's hate.

---

## 10. Tests

- **Monster attributes & R-126 saturation**:
  - `PropertyId` fallback chain (`AttributeComponent` -> `BaseInfoComponent` -> `2`).
  - `effectiveLevel`: explicit level `>= 10`, `Area.Father` walk to `WorldMonsterLevelMax`,
    and WL `1..8` fallback table.
  - High-level monster scaling (`base_val * ratio / 10000` overflowing `i32`) saturates cleanly
    to `i32.MAX` without throwing or wrapping (R-126).
- **Damage & heal math**:
  - `DamageExecuteRequest` damage formula verified against hand-computed values (Atk/Def/HP
    scaling, `RateLv` clamping, element & ability bonus, `ForceCritical`, `Def` & `IgnoreDefRate`,
    `Resistance - Pen` clamped `[0.1, 2.0]`, `DamageReduce` clamped `[0, 2.0]`, final clamp
    `[1, 2_000_000_000]`).
  - All 6 heal `FormulaType` cases (`0/1`, `2`, `3`, `5`, `6`) with `ceil` rounding and
    `HealChange + HealedChange`.
  - Attacker `ElementEnergy` and `Energy` updates; lethal hit emits
    `EntityLivingStatusNotify{ Dead }`, triggers monster kill, and triggers `PlayerDeadNotify`
    only when all player roles reach `Life == 0`.
- **Fall damage (R-118, matches robot `landing` check)**:
  - `SpeedZ = -4500, TimeExceeding = 0` takes half of `LifeMax` off a role;
  - Unknown entity id (e.g. `6`, client Vision) replies `Success` and changes nothing;
  - Long fall (`SpeedZ = -4000, TimeExceeding = 34069`) drops `Life` to `0` (never negative)
    and triggers `EntityLivingStatusNotify{ Dead }` (and `PlayerDeadNotify` if team wiped).
- **Monster kills & drops**:
  - Corpse queued for 8 s removal, open-world respawn timer set to 3 min;
  - Echo drop produces a valid level-0 echo and pushes `PhantomItemAddNotify`;
  - Item drop skips non-`ItemInfo` ids (R-109/R-116), persists via single writer (R-108), and
    pushes `NormalItemAddNotify`.
- **Revive & beacon heal**:
  - `ReviveRequest` finds nearest beacon, offsets `(+180, +180, +50)`, restores all roles to
    `LifeMax`, replies `Success`, pushes `PlayerReviveNotify` and `CombatReceivePackNotify`.
  - Moving within `1500 cm` of a beacon heals injured and revives dead roles; moving outside
    `1500 cm` does not.
- **Buffs & timers**:
  - `OrderApplyBuffRequest` instant vs duration vs infinite, stack cap & refresh policies,
    partial stack expiry (`StackExpirationRemoveNumber`), periodic tick, and
    `RoutineExpirationEffects` on full expiry.
- **Combat relays**:
  - `Ai.AiHateRequest` / `Push` flips `PlayerBattleStateChangeNotify` on entering and leaving
    combat;
  - `HitRequest`, `UseSkillRequest`, `AnimationGameplayTagRequest`, `CombineEntitiesRequest`,
    `TimeCheckRequest`, and unhandled inner message skipped cleanly inside a multi-message
    `CombatSendPackRequest`.
- **Acceptance**:
  - Robot checks covering `landing`, `shop` item-list filter / stray-weapon migration,
    and role/weapon/combat checks;
  - Real 3.8 client logs in, equips/switches roles and formations, fights and kills an
    open-world monster (HP drops, damage numbers appear, drops arrive), takes fall damage,
    dies and revives at a beacon.
