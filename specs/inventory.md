# Inventory: normal items, weapons, echoes, presets, calabash, and projector

Save file layouts are specified in [save-format.md](save-format.md) §5.9 and §5.11 and are not
repeated here.

---

## 1. Unique ids and new-account seeding

### Shared unique-id counter (`special_item/next`)

Weapons (`weapon/<uniqueId>`), echoes (`echo/<uniqueId>`), and echo presets (`echo/presets`
`VisionGroup.incr_id`) share **one** monotonic counter: `player/<id>/special_item/next`
([save-format.md](save-format.md) §4, §5.9).

On player load, the counter is initialized to at least:

`max(next_after(weapons), next_after(echoes), next_after(preset_groups))`

where `next_after(set) = (max existing id + 1)` or `1` if empty. Every new weapon, echo, or
preset allocates `incr_id = next++` and persists `special_item/next`.

### New-account seeding

1. **Weapons (`player/<id>/weapon/` absent)**:
   Seed 1 copy of every row in `WeaponConf` (125 rows in 3.8 data):
   `level = 1`, `exp = 0`, `breach = 0`, `reson_level = 1`, `role_id = null`,
   `func_value = (WeaponConf.QualityId >= 5 ? 0 : 1)`
   (5-star weapons start unlocked `0`, <5-star start locked `1`).
   Then login role repair ensures every owned role holds an equipped `InitWeaponItemId`
   ([roles.md](roles.md) §1).

2. **Echoes (`player/<id>/echo/` empty)**:
   When default echo seeding is enabled (default `true`), seed 1 level-0 echo for
   every `(PhantomItem, fetter_group_id)` pair where:
   - `PhantomItem.PhantomType == 1`, `ParentMonsterId == 0`, `ShowInBag == true`,
     `Destructible == true`;
   - `PhantomRarity(Rarity)` and `PhantomQuality(QualityId)` exist;
   - `PhantomMainProperty` has a row matching `MainProp.RandGroupId` with at least one valid
     `PhantomMainPropItem`;
   - iterating each `fetter_group_id` in `PhantomItem.FetterGroup[]` (748 items -> 1 488 echoes
     in 3.8 data).
   Each seeded echo has `level = 0`, `exp = 0`, initial `main_prop` rolled from the first valid
   `PhantomMainProperty` group for `MainProp.RandGroupId` (using `MainProp.RandNum` entries),
   empty `sub_prop`, and `fetter_group_id`.
   Default `echo/calabash` marks all `CalabashLevel.Level` rows in `rewarded_levels`.

3. **Normal items (`inventory_info` empty)**:
   Seed count `777` for every item id collected from:
   - gacha currencies: `50001, 50002, 50005, 50006, 50007, 50008`;
   - keys of `SkillTree.Consume`, `WeaponBreach.Consume`, `RoleBreach.BreachConsume`,
     `SkillLevel.Consume`, `PhantomQuality.IdentifyCost`, `PhantomRarity.PolishCost`,
     `PhantomVicePolishConfig.Cost`;
   - ids of `WeaponExpItem`, `PhantomExpItem`, and `MotorTechTree.TpItemId`;
   skipping currency ids `1..6` and deduplicating.

---

## 2. Normal-item rules: snapshot + log (R-80e, R-108) and `ItemInfo` filter (R-109, R-116)

### 2.1 Snapshot + append log (R-80e, R-108)

`inventory_info` and `inventory_info.log` follow [save-format.md](save-format.md) §5.11:

- **Load**: read `inventory_info`, then apply every parseable `<id> <count>` line from
  `inventory_info.log` in order (`count <= 0` deletes the item; otherwise sets its count).
- **Single writer (R-108)**: every mutation to normal items goes through one persistence helper.
  A direct snapshot rewrite without clearing/updating the log would be undone by the log on
  next login.
  - Given the changed `(id, new_count)` pairs (using `new_count = 0` for removed items):
    if `current_log_lines + adding <= 4 * max(item_count, 16)`, append those lines to
    `inventory_info.log`;
    otherwise atomically replace `inventory_info` and remove `inventory_info.log`.
- **Quest hook (R-62, R-143h)**: adding or modifying a normal item bumps the player's
  inventory-change version so item-count quest conditions re-evaluate.

### 2.2 Normal-item list rule and stray-weapon migration (R-109, R-116)

The 3.8 client reads `NormalItemList` sequentially and **stops parsing the rest of the list at
the first id missing from `ItemInfo`** (silent truncation of the bag).

- **Outbound filter (R-109, R-116)**: every outgoing `NormalItemResponse` (22771),
  `NormalItemAddNotify` (24714), and `NormalItemUpdateNotify` (27265) **must** filter
  `NormalItemList` to ids present in `ItemInfo`.
  - If filtering reduces a non-empty `NormalItemAddNotify` or `NormalItemUpdateNotify` to empty,
    do **not** send the notify at all.
- **Login stray-weapon migration (R-116)**: on `Entered(session, reconnect)`, **before** any
  item push, scan `inventory_info` for entries whose `id` is absent from `ItemInfo` and present
  in `WeaponConf` (for example, weapons previously dropped into normal items such as `21020053`):
  1. For each copy (`count` times), allocate a unique id from `special_item/next` and create a
     level-1 weapon file under `weapon/<uniqueId>` (saving weapon files first).
  2. Remove those ids from `inventory_info` and persist through the single inventory writer
     (§2.1).
- **Money override**: `NormalItemResponse` sets or appends items `2`, `3`, `4` with the
  attribute values (§2.3).

### 2.3 Money: 贝币 (2), 星声 (3), 月相 (4) are player attributes
- Client: counts money items 2 (`PlayerAttrKey_Coin`), 3 (`RareCoin`), and 4 (`CashCoin`) solely
  from player attributes; bag rows 2/3/4 are never shown or checked by the client.
- Server: counting, granting and taking 2/3/4 read and write `basic_info.attributes.coin` /
  `rare_coin` / `cash_coin` (`combat.MoneyAttr`, `PlayerSession.ItemCount`, `GrantItem`,
  `TakeItem`, drops, role spillover), save `basic_info` and push `PlayerAttrNotify` with the new
  value; no item notify. Bag rows 2/3/4 left from before are ignored.

### 2.4 Costs: paying for an upgrade (R-158)
- An upgrade's cost is a list of (item id, count) summed from its config maps
  (`combat.AddCosts`). `combat.CanPay`: the player holds every count (money from the attributes,
  §2.3, else the bag). Short → the request is refused with its error code and nothing is taken.
- `combat.Pay`: money through the attributes (`PlayerAttrNotify`); bag items in one batch, then
  `NormalItemUpdateNotify { NoTips = true }` with the counts left (ItemInfo ids, §2.2) and
  `NormalItemRemoveNotify` with the ids that reached 0. Pushed before the reply.
- Users: role skills and skill-tree nodes, resonant chain ([roles.md](roles.md) §6.3, §6.4,
  §6.6, §6.7); weapon level-up, breach, resonance (§5.2–§5.4); echo level-up, tuning, polish,
  reroll (§6.5). Costs of the same item in one list add up.
- `combat.Give` (refunds): money through the attributes; bag items in one batch,
  `NormalItemAddNotify { NoTips = true }` with the counts added.

---

## 3. Login pushes

On `Entered(session, reconnect)` (after stray-weapon migration §2.2), the server pushes:

1. `NormalItemUpdateNotify` (27265): all normal items (filtered by §2.2), `NoTips` (#4) = `true`.
2. `CalabashMsgNotify` (29927): `CalabashCfg` (#13) for max `CalabashLevel` (`LevelUpExp`,
   `LevelUpCondition`, `CatchGain` map) and `CalabashMsg` (#14) with max `Level`, `Exp = 0`,
   all `CalabashLevel.Level` in `UnlockedLevels`, all `CalabashDevelopReward` entries unlocked
   and rewarded.
3. `PhantomInteractionUnlockNotify` (23244): `EquipedMonsterIds` (#14) from
   `echo/calabash.projector_monster_ids` (up to 8 slots) and `UnlockIllustratedPhantoms` (#15)
   built from `PhantomItem` + `cosmetic_info.phantom_skins` + `echo/calabash.projector_skins`.
4. `CalabashLevelsRewardNotify` (19440): `RewardedLevels` (#11) from
   `echo/calabash.rewarded_levels`.
5. `ItemPkgOpenNotify` (17095): `OpenPkg` (#9) = `[0, 1, 2, 3, 4, 5, 6, 7]` (all 8 bag tabs).
6. `BuffItemNotify` (19426): empty (`{}`).
7. `EnergyUpdateNotify` (24514): `UpdateInfoList` (#14) with two `EnergyInfo` entries:
   - `Id = 5` (Waveplates): current `240`
   - `Id = 6` (Crystal Waveplates): current `480`

The client fetches full item, weapon, and echo lists via `NormalItemRequest`,
`WeaponItemRequest`, and `PhantomItemRequest` (§4–§6).

---

## 4. Normal-item and lock/disuse requests

### 4.1 `NormalItemRequest` (17423) -> `NormalItemResponse` (22771)
- Replies `NormalItemList` (#8): every entry from `inventory_info` plus money 2/3/4 with the
  attribute values (§2.3), filtered to `ItemInfo` ids (§2.2). `ExpireTime` = `0`.

### 4.2 `ItemLockRequest` (26312) -> `ItemLockResponse` (22732)
- Request: `IncrId` (#4), `Oper` (#13: `1` = lock -> `func_value = 1`; else unlock -> `0`).
- Looks up `IncrId` in weapons first, then echoes (if neither exists, reply `ErrCode` (#4) =
  error).
- Updates `func_value`, saves the item (weapons via 30 s dirty queue; echoes immediately),
  pushes `ItemFuncValueUpdateNotify` (25472: `IncrId` #5, `FuncValue` #15), and replies
  `ErrCode = Success`.

### 4.3 `ItemDisuseRequest` (20767) -> `ItemDisuseResponse` (28866)
- Request: `Oper` (#1: `1` = mark discarded -> `func_value = 2`; else clear -> `0`),
  `IncrId` (#12).
- Same lookup, persistence, and `ItemFuncValueUpdateNotify` push as §4.2; replies
  `ErrCode` (#6) = `Success`.

---

## 5. Weapon requests

Costs: level-up, breach and resonance deduct materials and currency per §2.4.

### 5.1 `WeaponItemRequest` (28901) -> `WeaponItemResponse` (24074)
- Replies `WeaponItemList` (#10): one `WeaponItem` per owned weapon (`Id`, `IncrId`,
  `FuncValue`, `WeaponLevel`, `WeaponExp`, `WeaponBreach`, `WeaponResonLevel`,
  `RoleId = role_id ?? 0`).

### 5.2 `WeaponLevelUpRequest` (19242) -> `WeaponLevelUpResponse` (17061)
- Request: `IncId` (#8), `ConsumeList` (#14, `repeated WeaponConsumeItem{ IncId, Count, ItemId }`).
- Validates weapon exists (`ErrWeaponConsumeItemNotFound`) and `WeaponConf` exists
  (`ErrWeaponResonConfigNotFound`).
- `limit = WeaponBreach(BreachId, weapon.breach).LevelLimit` (fallback `90`). If
  `weapon.level >= limit`, reply `ErrWeaponLevelLimit` with current level/exp.
- Entries with `ItemId != 0 && Count > 0`:
  - `IncId > 0`: a weapon used as material. Refused when missing (`ErrWeaponConsumeItemNotFound`),
    the target (`ErrWeaponConsumeSelf`), listed twice (`ErrWeaponConsumeDuplicate`), equipped
    (`ErrWeaponEquiped`), locked (`FuncValue == 1`, `ErrWeaponLocked`) or without `WeaponConf`
    (`ErrWeaponConsumeItemIdNotFound`). Exp: `q = WeaponQualityInfo(QualityId)`;
    level 1 and exp 0 → `q.BasicExp`, else `floor(q.BasicExp + (exp to its level + exp) × 0.9)`
    (CommonParam `WeaponStrengthenExpCost` 900 ‰). 贝币: `q.Cost`.
  - else an exp item: `WeaponExpItem(ItemId)` (else `ErrWeaponConsumeItemIdNotFound`; listed twice →
    `ErrWeaponLevelUpItemDuplicate`). Exp `BasicExp × Count`, 贝币 `Cost × Count`, takes `Count`.
- `gained_exp <= 0` → `ErrWeaponLevelUpNoExp`. Not payable (items + 贝币) →
  `ErrWeaponConsumeInvalid` (200003, 消耗道具不足), nothing taken.
- Pays, deletes the material weapons (`WeaponItemRemoveNotify` with their ids).
- Add `gained_exp` to `weapon.exp` and advance `weapon.level` while `level < limit` and
  `exp >= WeaponLevel(LevelId, level).Exp` (what level L needs to reach L+1); clamp
  `weapon.exp = 0` at `limit`.
- Overflow `over = gained_exp − (exp from the current level to limit − weapon.exp)`; when > 0 it
  comes back as exp items, biggest `BasicExp` first, `floor(over / BasicExp)` each, the rest lost:
  added to the bag (`NormalItemAddNotify`, `NoTips`) and replied in `ItemMap` (#13), which the
  client shows as returned items. No overflow → empty `ItemMap`.
- Enqueue weapon dirty save (30 s queue, §9). If equipped (`weapon.role_id != null`), refresh
  live role attributes and push `PbGetRoleListNotify` ([roles.md](roles.md) §3.2 step 3).
- Reply `ErrorCode = Success`, `IncId`, `WeaponLevel`, `WeaponExp`, `ItemMap`.

### 5.3 `WeaponBreachRequest` (16833) -> `WeaponBreachResponse` (26514)
- Request: `IncId` (#2).
- Validates weapon (`ErrWeaponConsumeItemNotFound`) and `WeaponConf` (`ErrWeaponConfigNotFound`).
- `cur = WeaponBreach(BreachId, breach)`; no `cur` or no row `breach + 1` → `ErrWeaponBreachLimit`;
  `level < cur.LevelLimit` → `ErrWeaponLevelUpLevel`. Pays `cur.Consume` + `cur.GoldConsume`
  贝币, else `ErrWeaponConsumeInvalid`.
- Increments `weapon.breach += 1`, enqueues weapon dirty save.
- If equipped on a live role entity: updates `EquipComponentPb.WeaponBreachLevel`, pushes
  `EntityEquipChangeNotify` (27672: `EntityId` #11, `EquipComponent` #7), refreshes live role
  attributes + pushes `PbGetRoleListNotify`.
- Pushes `UpdateFormationNotify`, replies `ErrorCode = Success`, `IncId`, `WeaponBreach`.

### 5.4 `WeaponResonUpRequest` (17476) -> `WeaponResonUpResponse` (21310)
- Request: `IncId` (#1), `ConsumeList` (#2, `repeated int32`), `ConsumeItemList` (#6,
  `repeated WeaponConsumeItem`).
- Validates target weapon and `WeaponConf`; if `reson_level >= WeaponConf.ResonLevelLimit`,
  replies `ErrWeaponResonLevelLimit`.
- Finds the **first** unequipped, unlocked duplicate weapon (`consumed.id == weapon.id &&
  consumed.role_id == null && consumed.func_value != 1 && consumed_id != request.IncId`) from `ConsumeList` (then
  `ConsumeItemList`), removes it from memory and deletes `weapon/<consumed_id>` immediately.
  If none matched, replies `ErrWeaponResonConsumeItem`. Pays `WeaponReson(ResonId,
  reson_level).GoldConsume` 贝币 (level L → L+1 costs row L),
  else `ErrWeaponResonConsumeGold`, nothing taken. One level per request; `AlternativeConsume`
  (items instead of a duplicate) is not handled.
- Increments `weapon.reson_level += 1`, enqueues weapon dirty save, pushes
  `WeaponItemRemoveNotify` (19935) with the deleted id.
- If equipped on a live role entity: swaps passive buffs from `WeaponReson(ResonId, old_level)`
  to `WeaponReson(ResonId, new_level)` via `RemoveBuffS2cRequestNotify` (`Reason = 41`) and
  `ApplyBuffS2cRequestNotify` (`Reason = 12`) ([combat.md](combat.md) §8), and refreshes live
  role attributes + pushes `PbGetRoleListNotify`.
- Replies `ErrorCode = Success`, `IncId` (#9), `ReaonLevel` (#11 — exact 3.8 client spelling).

### 5.5 `EquipTakeOnRequest` (21385) -> `EquipTakeOnResponse` (29980)
- Request: `Data` (#8, `RoleLoadEquipData{ RoleID #1, Pos #2, EquipIncID #3 }`).
- Only `Pos == EquipPos.Weapon` (0) is handled here. Validates target role and target weapon.
- Swaps weapons atomically if the weapon was held by another role (that other role receives the
  target role's previous weapon; every role always keeps a weapon). Saves modified roles and
  weapons.
- For each affected role whose entity is spawned in the scene:
  - Updates `EquipComponentPb` (`WeaponId`, `WeaponBreachLevel`) and pushes
    `EntityEquipChangeNotify` (27672);
  - Swaps `WeaponReson.Effect[]` passive buffs (`Reason = 41` remove / `Reason = 12` apply);
  - Refreshes live role attributes + pushes `PbGetRoleListNotify`.
- Pushes `UpdateFormationNotify` and `EquipTakeOnNotify` (25950) with every changed
  `RoleLoadEquipData`, and replies `ErrorCode = Success`, `DataList` (#5).

---

## 6. Echo (Phantom) requests

Echo upgrade, tuning, polishing, and reroll handlers deduct costs per §2.4 and §6.5.

### 6.1 Echo stat formulas

- **Main prop value at `level`**:
  Look up `PhantomGrowth` matching `(GrowthId == PhantomMainPropItem.GrowthId, Level == level)`
  (default `growth = 10000` if absent).
  `value = trunc_i32(i64(StandardProperty) * i64(growth) / 10000)`.
- **Sub-stat roll pool**:
  Candidates come from `PhantomSubProperty` deduplicated by sub-prop type: for `PropId` in
  `{10002, 10007, 10010}` (HP/Atk/Def), `(PropId, AddType)` distinguishes flat vs percent;
  for all other `PropId`, `PropId` alone must be unique on an echo (no duplicate sub-stat type).
  Pick uniformly among eligible unused sub-prop types.
- **Sub-stat value roll**:
  `scaled_value = trunc_i32((SubStandardProperty * multiplier + 5000) / 10000) * 10`.
  Weighted tier selection (indices `0..max_index` inclusive, capped by `PhantomItem.QualityId`):

| Sub-stat group | Match rule | Multipliers | Weights | `max_index` by quality (3 / 4 / 5+) |
|---|---|---|---|---|
| Flat Atk | `PropId == 10007 && AddType == 1` | `[813, 1083, 1353, 1623]` | `[70, 540, 390, 30]` | `1 / 2 / 3` |
| Flat Def | `PropId == 10010 && AddType == 1` | `[813, 1083, 1353, 1623]` | `[150, 460, 330, 90]` | `1 / 2 / 3` |
| Crit Rate / Crit DMG | `PropId == 8 \|\| PropId == 9` | `[1263, 1383, 1503, 1623, 1743, 1863, 1983, 2103]` | `[70, 70, 70, 24, 24, 24, 9, 9]` | `3 / 5 / 7` |
| All other sub-stats | default | `[853, 953, 1053, 1153, 1253, 1353, 1453, 1553]` | `[70, 80, 210, 250, 180, 150, 60, 30]` | `3 / 5 / 7` |

- **Cost limit**: total cost across a role's 5 echo slots (`sum(PhantomRarity(Rarity).Cost)`)
  must not exceed `TotalCost = max(CalabashLevel.Cost)` (`12` in 3.8).

### 6.2 `PhantomItemRequest` (24954) -> `PhantomItemResponse` (24418)
- `TotalCost` (#2): max `CalabashLevel.Cost` (`12`).
- `DirectRefineWeekTimes` (#5): `0`.
- `UnlockSkins` (#6): `cosmetic_info.phantom_skins`.
- `PhantomItemList` (#9): every owned echo (`StackCount = 1`, `AddTime = 0`, all prop/skin/lock
  fields from `echo/<uniqueId>`).
- `EquipInfo` (#15): one `RolePhantomEquipInfo{ RoleId, PhantomItemIncrId[5] }` per role that
  has at least one echo equipped.
- `PropInfo` (#4): one `RolePhantomPropInfo` per role with echoes equipped (echo + fetter
  display bonuses only, [roles.md](roles.md) §3.3.3).

### 6.3 Equipping echoes: `PhantomPutOnRequest` (25608) and `PhantomAutoPutRequest` (25130)
- **`PhantomPutOnRequest`**: `Pos` (#7, `0..4`), `RoleId` (#13), `IncId` (#14, `0` = unequip).
  - Validates slot range, cost cap (`<= TotalCost`), and no duplicate `MonsterId` in another
    slot on the same role (if swapping with another role, transfers cleanly).
- **`PhantomAutoPutRequest`**: `RoleId` (#10), `PhantomItemIncrId` (#8, up to 5 ids) — replaces
  all 5 slots at once under the same cost/duplicate rules.
- **Role entity & client sync after any equip/unequip/upgrade/skin change**:
  1. Persist modified echo files under `echo/<uniqueId>`.
  2. Push `RolePhantomPropUpdateNotify` (23146) for every affected role.
  3. For each affected role currently spawned in the scene:
     - Check whether the role's **vision summon topology** changed (the pair of
       `(main_echo_summon_id, combo_echo_summon_id)` derived from slot 0 `PhantomItem.SkillId`
       and `PhantomSummonGroup`).
     - If topology changed: rebuild the role's scene entities (remove & re-add role + vision +
       concomitant entities, [formation.md](formation.md) §4).
     - Otherwise: update `VisionSkillComponent` and push `VisionSkillChangeNotify` (25196),
       sync changed echo/fetter passive buffs via `RemoveBuffS2cRequestNotify` (`Reason = 35`
       echo / `36` fetter) and `ApplyBuffS2cRequestNotify` (`Reason = 7`), and refresh live
       role attributes + push `PbGetRoleListNotify` ([roles.md](roles.md) §3.2 step 3).
  4. Reply with `ErrorCode = Success` and `EquipInfoList` for all affected roles.

### 6.4 `PhantomSkinChangeRequest` (22400) -> `PhantomSkinChangeResponse` (26665)
- Request: `SkinId` (#1, `0` = default), `ChangeDefault` (#6), `IncrId` (#14).
- Validates echo exists and `SkinId == 0` or `PhantomItem(SkinId).ParentMonsterId ==
  base_item.MonsterId`.
- Updates `echo.skin_id`, saves echo, pushes `PhantomItemUpdateNotify` (16679), refreshes live
  role entity if equipped (§6.3), and replies `ErrorCode = Success`.

### 6.5 Echo upgrade, tuning, and polishing
Costs (§2.4; CommonParam `PhantomLevelUpCoinCost` 100 ‰, `PhantomExpReturnRatio` 750 ‰,
`PhantomIdentifyReturnRatio` 300 ‰). `PhantomLevel(LevelUpGroupId, L).Exp` is what level L−1
needs to reach L (level 0 rows are 0).

- **`PhantomLevelUpRequest` (25666) -> `PhantomLevelUpResponse` (29647)**:
  - Level `>= PhantomQuality(QualityId).LevelLimit` → `ErrPhantomLvupMax`.
  - `ConsumeList` (#8) entries with `IncId > 0` are echoes used as material: missing or the target
    → `ErrPhantomLevelUpConsumeItemErr`, listed twice → `ErrPhantomLevelUpRepeatItem`, equipped →
    `ErrPhantomEquiped`, locked (`FuncValue == 1`) → `ErrPhantomLevelUpMaterialLock`. Exp
    `floor(full exp × 0.75)` (full exp = `PhantomLevel` 1 … level + exp); gives back
    `floor(IdentifyCost count × sub-stat count × 0.3)` of its quality's tuning item. Other entries:
    `PhantomExpItem(ItemId)` (else `ErrPhantomExpItemNotExist`; twice → `ErrPhantomLevelUpRepeatItem`),
    exp `Exp × Count`, takes `Count`. No exp → `ErrPhantomLvupNoItem`.
  - The new level/exp as before (clamp `exp = 0` at the limit). 贝币 =
    `floor(min(gained, exp to the limit) × 0.1)`.
    Exp past the limit comes back as `PhantomExpItem`s, biggest first.
  - `IdentifyCount` (#2) > 0: `n = min(IdentifyCount, unlocked_slots(new level) - sub_prop.len)`
    tunings, paid as `PhantomIdentifyRequest`. Returned tuning items pay the tuning first.
  - Not payable → `ErrPhantomLevelUpConsumeItemNotEnough` (200067), nothing taken. Else pays,
    deletes the material echoes (`PhantomItemRemoveNotify`), gives the returns
    (`NormalItemAddNotify`), levels, recomputes `main_prop` (§6.1), rolls the `n` sub-stats.
  - Saves echo, refreshes equipped role entity (§6.3), replies `ErrorCode = Success`,
    `UpdateInfo` (#9), `ItemMap` (#8) = the returned items (shown as returned).
  - Not known: what a fresh echo (level 0, exp 0) gives as material. The client shows its
    `PhantomExp`, which we send as 0, so it gives 0 (it gave max(exp, 1000) before R-158).
- **`PhantomIdentifyRequest` (26204) -> `PhantomIdentifyResponse` (19102)**:
  - `n` = `Count` (#10) up to `unlocked_slots - sub_prop.len`; pays `n ×` (`PhantomQuality.IdentifyCost`
    + `IdentifyCoin` 贝币), else `ErrItemNotEnough`. Rolls the sub-stats (§6.1), saves echo,
    refreshes equipped role entity (§6.3), replies `ErrorCode = Success`, `UpdateInfo` (#11).
- **`PhantomPolishRequest` (25622) -> `PhantomPolishResponse` (25461)**:
  - Changes main stat (`PhantomMainPropItemId` #10, must belong to the echo's
    `PhantomMainProperty` group) on a level-0 echo; pays `PhantomRarity(PhantomItem.Rarity).PolishCost`,
    else `ErrItemNotEnough`; recomputes `main_prop` value, saves echo, refreshes equipped role if
    any, replies `ErrorCode = Success`, `UpdateInfo` (#8).
- **`PhantomBatchPolishRequest` (17121) -> `PhantomBatchPolishResponse` (21024)**:
  - Applies the same main-stat change across every valid echo in `StackEntries` (#14), paying
    all their polish costs together (all or none, `ErrItemNotEnough`), returning the updated
    echoes in `UpdateInfos` (#15).
- **`PhantomVicePolishRequest` (21223) -> `PhantomVicePolishResponse` (25546)**:
  - Pays `PhantomVicePolishConfig(locked count).Cost` (client `GetPhantomVicePolishCostByLockCount`),
    else `ErrItemNotEnough`.
  - Rerolls unlocked sub-stats (preserving indices listed in `LockPropIndex` #8) into
    `echo.unack_sub_prop` and stores `echo.lock_prop_index`, saves echo, and replies
    `ErrorCode = Success`, `PhantomSubProp` (#6) = candidate `unack_sub_prop`.
- **`PhantomVicePolishAckRequest` (26021) -> `PhantomVicePolishAckResponse` (22940)**:
  - If `Ack` (#15) is `true`: replaces `echo.sub_prop` with `echo.unack_sub_prop`.
  - Always clears `echo.unack_sub_prop` and `echo.lock_prop_index`, saves echo, refreshes
    equipped role if any, replies `ErrorCode = Success`, `UpdateInfo` (#12).

### 6.6 Echo presets (`echo/presets`, max 20 groups)
All 6 preset handlers operate on `echo/presets` ([save-format.md](save-format.md) §5.9):
- `PhantomEquipGroupInfosRequest` (25068) -> `PhantomEquipGroupInfosResponse` (17071): returns
  all saved `PhantomEquipGroupInfo{ IncId[5], Name }`, `IsOldData = false`.
- `AddPhantomEquipGroupRequest` (18869) -> `AddPhantomEquipGroupResponse` (17340): saves
  `RoleId`'s current 5 equipped echo ids under `Name` (max 20 groups; allocates `incr_id` from
  `special_item/next`), persists `echo/presets`, returns updated group list.
- `UsePhantomEquipGroupRequest` (23641) -> `UsePhantomEquipGroupResponse` (19113): equips the
  5 echo ids from group `Index` onto `RoleId` (applying the same equip/sync rules as §6.3),
  returns `EquipInfoList`.
- `TopPhantomEquipGroupRequest` (19922) -> `TopPhantomEquipGroupResponse` (24824): moves group
  `Index` to position `0`, saves, returns updated list.
- `ChangePhantomEquipGroupNameRequest` (18907) -> `ChangePhantomEquipGroupNameResponse` (16060):
  renames group `Index`, saves, returns updated list.
- `DeletePhantomEquipGroupRequest` (28863) -> `DeletePhantomEquipGroupResponse` (16721):
  removes group `Index`, saves, returns updated list.

### 6.7 Calabash and echo projector
- **`CalabashLevelRewardRequest` (28983) -> `CalabashLevelRewardResponse` (23187)**:
  - Marks `Level` (#13) in `echo/calabash.rewarded_levels`, saves `echo/calabash`, pushes
    `CalabashLevelsRewardNotify` (19440), replies `ErrorCode = Success`.
- **`PhantomInteractionEquipRequest` (21354) -> `PhantomInteractionEquipResponse` (21997)**:
  - Stores up to 8 `EquipedMonsterIds` (#4) in `echo/calabash.projector_monster_ids`, saves
    `echo/calabash`, pushes `PhantomInteractionUnlockNotify` (23244), replies `Code` (#10) =
    `Success`.
- **`PhantomInteractionSkinChangeRequest` (21448) -> `PhantomInteractionSkinChangeResponse` (18194)**:
  - Updates `(MonsterId, SkinId)` entries in `echo/calabash.projector_skins`, saves
    `echo/calabash`, pushes `PhantomInteractionUnlockNotify` (23244), replies `Code` (#10) =
    `Success`.

---

## 7. Deferred saving

Weapon level/breach/refinement/lock changes use the **30-second dirty-save queue** (flushed on
scene switch and `Released(session)`), except weapon creation, deletion (`WeaponResonUpRequest`),
stray-weapon migration, and equip swaps (`EquipTakeOnRequest`), which save immediately.
Echoes, `echo/presets`, `echo/calabash`, and normal-item log/snapshot writes persist immediately.

---

## 8. Tests

- **Unique-id counter**: adding a weapon, an echo, and an echo preset allocates strictly
  increasing unique ids from `special_item/next` with zero collisions across reload.
- **Snapshot + log (R-80e, R-108)**:
  - Writing mutations appends `<id> <count>` lines to `inventory_info.log` and reloads the exact
    counts (including `<= 0` deletions).
  - Exceeding `4 * max(item_count, 16)` log lines compacts atomically into `inventory_info` and
    deletes `inventory_info.log`.
- **Normal-item list filter & stray-weapon migration (R-109, R-116)**:
  - A save containing weapon id `21020053` inside `inventory_info` migrates it into `weapon/`
    at login, removes it from `inventory_info`, and leaves zero non-`ItemInfo` ids in
    `NormalItemResponse`, login `NormalItemUpdateNotify`, and subsequent `NormalItemAddNotify` /
    `NormalItemUpdateNotify` (matches robot `shop` check).
  - A notify whose items are all filtered out is suppressed (not sent empty).
  - `NormalItemResponse` reflects the money attributes in items `2`, `3`, `4` (§2.3).
- **Weapons**: level-up exp & breach limit, breach (`EntityEquipChangeNotify` + attr sync),
  refinement (deletes 1 unequipped duplicate, pushes `WeaponItemRemoveNotify`, swaps weapon
  passive buffs), equip swap between two roles (both roles keep a weapon, live entity equip &
  buffs sync), lock/disuse (`ItemFuncValueUpdateNotify`).
- **Weapon costs (R-158, `combat/costs_test.go`)**: material weapons locked / equipped / the
  target refused; short 贝币 → nothing taken; exp items + a material weapon paid, the weapon
  deleted; overflow refunded as exp items in `ItemMap`; breach short / paid / below the limit /
  no next row; resonance gold short / paid; on the 3.8 data every exp item, weapon quality and
  breach step below the top has a 贝币 price.
- **Echo costs (R-158, `combat/costs_test.go`)**: polish short / paid; level-up with a material
  echo (exp 0.75, tuners back netted against the auto tuning, echo deleted) short by one 贝币 /
  paid; overflow refunded as exp items; tuning short / paid; reroll short / paid; on the 3.8 data
  every quality, rarity and locked count has a price.
- **Echoes**: main-prop level growth formula, sub-stat roll uniqueness and tier caps per
  quality (3/4/5), cost-cap rejection (`> 12`), equip/unequip triggering `VisionSkillChangeNotify`
  vs full role rebuild on summon-topology change, tune/polish/vice-polish-ack flow, preset
  CRUD (20-group cap), calabash reward, and projector equip/skin persistence.
