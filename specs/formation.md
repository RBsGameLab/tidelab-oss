# Formation and role switching

Boundary with entities: this spec says **when** formation changes add or remove role entities and
what formation/team messages carry; [entities.md](entities.md) owns **how** each role entity and
its concomitant entities are constructed, numbered, and serialized.

---

## 1. Save state (`formation_info`) and defaults

Stored at `state/player/<id>/formation_info` ([save-format.md](save-format.md) §5.3):

- **20 formation slots** (`0..19` on disk; 1-based `FormationId` `1..20` on the wire).
- Each slot has `cur_role` (role config id), `name` (string), and up to **3 role entries**
  `(role_id, entity_id, on_stage_without_control)`. On disk, `entity_id` is `-1` when not
  spawned in the current scene.
- `cur_formation` is the 0-based active slot (`0..19`).

### New account / empty active formation default

When formation `0` has no roles at login, the server seeds:
- `cur_formation = 0`
- formation `0`: `roles = [1211, 1108, 1506]`, `cur_role = 1211`, `name = ""`.

Note: on scene join, R-143f spawns role entities (and their vision/custom concomitants) for
every role in the active formation (`formations[cur_formation]`) and binds each slot's
`entity_id` to the spawned role's net id, setting the visibility marker on the entity matching
`cur_role` only.

---

## 2. Login and scene-join formation sync

After joining a scene (R-143f `enter-world.md` carries the initial `FightRoleInfos` in
`JoinSceneNotify`), the server pushes two formation notifies:

### 2.1 `UpdateFormationNotify` (20593)

- `PlayersFormations` (#5, single `PlayerFightFormations` for `scene.player_id`):
  - `PlayerId` (#1): player id
  - `Formations` (#2, `repeated FightFormationNotifyInfo`): one entry for each of the 20 slots
    that has **at least one role** (empty slots are omitted):
    - `FormationId` (#1): 1-based index (`1..20`)
    - `CurRole` (#2): `formation.cur_role`
    - `IsCurrent` (#4): `index == cur_formation`
    - `Name` (#5): `formation.name`
    - `RoleInfos` (#3, `repeated FormationRoleInfo`): for each role slot whose role and weapon
      exist:
      - `RoleId` (#1): role config id
      - `MaxHp` (#2), `CurHp` (#3): from the live entity's `LifeMax.current` / `Life.current`
        when `slot.entity_id != -1` and the entity exists in the scene; otherwise from the
        role's live attribute snapshot (`base_props + add_props`, [roles.md](roles.md) §3.2)
      - `Level` (#4): `role.level`
      - `RoleSkinId` (#5): `role.role_skin_id`
      - `SkillBranchId` (#6): `role.skill_branch` (sent for both live and non-spawned roles).
      - `WeaponId` (#7): `weapon.id`
      - `WeaponBreachLevel` (#8): `weapon.breach`
      - `WeaponSkinId` (#9): `role.weapon_skin_id`
      - `DressList` (#10): all `ornament_id` values from `role.ornaments`.

### 2.2 `FormationAttrNotify` (16879)

Sent once after scene join with stamina / flight / vehicle energy caps:
- `CurTime` (#5): `1534854458` (fixed constant)
- `FormationAttrs` (#11, `repeated FormationAttr`):

| `AttrId` (#1) | Client `EFormationAttributeId` | `Ratio` (#2) | `BaseMaxValue` (#3) | `MaxValue` (#4) | `CurrentValue` (#5) |
|---:|---|---:|---:|---:|---:|
| `1` | `Strength` (stamina) | `2400` | `24000` | `24000` | `22000` |
| `10` | `SoarStrength` (flight) | `2400` | `7000` | `15000` | `7000` |
| `14` | `MotorcycleStrength` | `0` | `10000` | `10000` | `10000` |

---

## 3. The 3.8 formation-update struct pitfall (R-114)

Wire message id **22332** is **`UpdateSceneGroupDataNotify`** in 3.8 `Aki.Protocol`. Its
structure is:

- `UpdateSceneGroupDataNotify` (22332):
  - `#12 PlayerSceneGroupDataList` (`repeated PlayerSceneGroupDataList`):
    - `#1 PlayerId` (`int32`)
    - `#2 PlayerSceneGroupData` (`repeated PlayerSceneGroupData`):
      - `#1 Group` (`int32`, `1` for normal player party)
      - `#2 FightRoleInfos` (`repeated SceneRoleInformation`):
        - `#1 RoleId` (`int32`)
        - `#2 EntityId` (`int64`, role entity net id)
        - `#3 OnStageOutOfControl` (`bool`, `false`)
      - `#3 CurRole` (`int32`)
      - `#4 LivingStatus` (`LivingStatus`, `Alive` = `0`)
      - `#5 FixedLocationByCfg` (`bool`, `false`)
    - `#3 CurrentGroup` (`int32`, `1`)

**Pitfall (R-114)**: 3.8 `Aki.Protocol` also has an unrelated pinball message named
`GroupFormation` (`#1 LevelGroup`, `#2 RoleIds`, **no field #3**). Matching by message name
instead of wire structure caused `CurrentGroup` (#3) to be dropped, leaving the client with
group `0` and **no active role** as soon as the player switched teams. Always use
`UpdateSceneGroupDataNotify` -> `PlayerSceneGroupDataList` -> `PlayerSceneGroupData` ->
`SceneRoleInformation` with `Group = 1` and `CurrentGroup = 1`.

---

## 4. Role entities on formation change and rebuild

### 4.1 Incremental diff on `UpdateFormationRequest` (§5.2)

When an `UpdateFormationRequest` entry has `IsCurrent == true`:

1. Compare `old_formation` (the previous active slot) against `new_formation` (the newly
   selected active slot):
   - Roles present in both keep their existing `entity_id`.
   - Roles only in `old_formation` are **removed**: remove each role entity AND all of its
     concomitant entities (`ConcomitantsComponentPb.VisionEntityIds` and `CustomEntityIds`)
     from the scene, then push **`EntityRemoveNotify`** (`IsRemove = true`) with all removed
     entity ids (pushed even if the list is empty).
   - Roles only in `new_formation` are **added**: create each role entity (and its vision and
     custom concomitant entities via R-143f) and write the new role net id into the slot's
     `entity_id`.
2. Push **two** `EntityAddNotify` messages (`IsAdd` #9 = `false`):
   - First: all newly added **role** `EntityPb`s;
   - Second: all newly added **concomitant** (`vision` + `custom`) `EntityPb`s.
   (The client needs the role entity registered before its concomitants reference it as
   summoner.)
3. Choose `current_formation.cur_role`:
   - If `old_formation.cur_role` is still in `new_formation`, keep it.
   - Otherwise, find `old_slot` = the slot index (`0..2`) that held `old_formation.cur_role`
     (default `0`), scan downward from `old_slot` to `0` for the first non-empty slot in
     `new_formation`, and fall back to slot `0`.
4. Push **`UpdateSceneGroupDataNotify`** (22332, §3) with the new active formation's
   `SceneRoleInformation` list and `CurRole`.
5. Register/sync buff timers ([combat.md](combat.md) §8), save the scene (`formation_info` +
   scene instance), push **`UpdateFormationNotify`** (20593, §2.1), and send the reply.

### 4.2 Full role-entity reset (triggered by Rover switch or echo summon-topology change)

When `RoleElementChangeRequest`, `RoleSexChangeRequest` ([roles.md](roles.md) §6.9–§6.10), or
an echo change that alters a live role's main/combo vision summon ids
([inventory.md](inventory.md) §6.3) touches a role in the active formation:
- Remove the affected role entity (and its vision + custom concomitants) via
  `EntityRemoveNotify{ IsRemove = true }`;
- Recreate the role entity (and its concomitants) with updated data;
- Push `EntityAddNotify{ IsAdd = false }` (role first, then concomitants);
- Push `UpdateSceneGroupDataNotify` (22332, §3) so the client binds the new entity id.

---

## 5. Formation requests

### 5.1 `GetFormationDataRequest` (20343) -> `GetFormationDataResponse` (18668)
- Replies `Formations` (#8): all 20 `FightFormation` entries (`FormationId = 1..20`, `CurRole`,
  `RoleIds`, `IsCurrent`).

### 5.2 `UpdateFormationRequest` (23144) -> `UpdateFormationResponse` (24546)
- Request: `Formations` (#11, `repeated FightFormation`).
- For each `FightFormation` (`idx = FormationId - 1`):
  - If `IsCurrent == true`: set `cur_formation = idx`, update slots (`<= 3`), and run the
    entity diff, `EntityRemoveNotify`, two `EntityAddNotify`, `cur_role` selection, and
    `UpdateSceneGroupDataNotify` (§4.1).
  - If `IsCurrent == false`: replace `formations[idx]` with `CurRole` and `RoleIds` (each with
    `entity_id = -1`).
  - Preserve `formations[idx].name` when `FightFormation.Name` is empty.
- Re-register/sync buff timers, save scene + `formation_info`, push `UpdateFormationNotify`,
  and reply empty `UpdateFormationResponse{}` (`ErrorCode = Success`).

### 5.3 `ReorderFormationRequest` (27566) -> `ReorderFormationResponse` (28600)
- Request: `FormationId` (#2, 1-based source `1..20`), `TargetId` (#12, 1-based destination
  `1..20`).
- Validate `1 <= FormationId <= 20` and `1 <= TargetId <= 20`; out of range replies
  `ErrorCode` (#13) = `ErrFormationUnknown`.
- Operation is **remove-and-insert** (not swap): remove slot `src = FormationId - 1`, shift
  intervening slots by one, insert at `dst = TargetId - 1`.
- **`cur_formation` tracks the active team**:
  - if `cur_formation == src` -> `cur_formation = dst`;
  - else if `src < dst && cur_formation > src && cur_formation <= dst` -> `cur_formation -= 1`;
  - else if `dst < src && cur_formation >= dst && cur_formation < src` -> `cur_formation += 1`.
- Save scene + `formation_info`.
- **Critical rule — do NOT push `UpdateFormationNotify`**: on `Success`, the 3.8 client reorders
  its own formation list locally; pushing `UpdateFormationNotify` before the response causes the
  client to reorder the already-reordered list a second time. Reply `ErrorCode = Success` only.

### 5.4 `RenameFormationRequest` (26314) -> `RenameFormationResponse` (18743)
- Request: `Name` (#1), `FormationId` (#8, 1-based `1..20`).
- Updates `formations[FormationId - 1].name`, saves scene + `formation_info`, pushes
  `UpdateFormationNotify`, and replies `ErrorCode` (#5) = `Success`.

### 5.5 `FormationAttrRequest` (19501) -> `FormationAttrResponse` (28542)
- Ignores the payload and replies `ErrorCode` (#10) = `Success` (the client manages stamina
  consumption and regeneration locally).

### 5.6 `SwitchRoleRequest` (27457) -> `SwitchRoleResponse` (16820)
- Carried inside **`CombatMessage.CombatSendPackRequest`** ([combat.md](combat.md) §2).
- Request: `RoleId` (#3), `Transform` (#7), `SwitchType` (#8), `OnStageWithoutControl` (#14).
- Updates `formations[cur_formation].cur_role = request.RoleId`.
- Clears visibility on the previous `cur_role` entity and sets visibility on the new `RoleId`
  entity.
- Ensures buff timers are registered and syncs left durations ([combat.md](combat.md) §8), saves
  scene + `formation_info`, and replies inside `CombatSendPackResponse` with
  `SwitchRoleResponse{ ErrorCode (#1) = Success, RoleId (#3) = request.RoleId }`.

### 5.7 Hook for later stages (R-86, R-143h)
Tower / dungeon overrides may temporarily swap `formation_info` and restore it when leaving the
instance (R-86); R-143g does not implement dungeons or tower, only normal formation storage and
requests.

---

## 6. Tests

- **Defaults & scene join**: empty save seeds formation 0 = `[1211, 1108, 1506]`, `cur_role =
  1211`; scene join sends `UpdateFormationNotify` and `FormationAttrNotify` (`AttrId` 1, 10,
  14).
- **`UpdateFormationRequest` entity diff & R-114 wire format**:
  - Switching active formation from `[A, B, C]` (`cur_role = B`) to `[A, D]` removes `B` and
    `C` (plus their vision & custom concomitants) via `EntityRemoveNotify`, keeps `A`'s entity
    id untouched, adds `D` via two `EntityAddNotify` messages (role first, concomitants
    second), picks `cur_role = A` (downward scan from old slot 1), and pushes
    `UpdateSceneGroupDataNotify` (22332) with `CurrentGroup = 1` and `Group = 1`.
- **`ReorderFormationRequest` (R-119)**:
  - Moving formation 1 to 3 shifts `[1, 2, 3, 4]` -> `[2, 3, 1, 4]` (not swap) and updates
    `cur_formation` to follow the active team in all three cases (`cur == src`, `src < cur <=
    dst`, `dst <= cur < src`).
  - Verify **no** `UpdateFormationNotify` is sent on `ReorderFormationRequest`.
  - Out-of-range id (0 or 21) returns `ErrFormationUnknown`.
- **`RenameFormationRequest`**: persists name across reload and pushes `UpdateFormationNotify`.
- **`SwitchRoleRequest`** inside `CombatSendPackRequest`: updates `cur_role`, toggles entity
  visibility, saves, and replies `{ ErrorCode = Success, RoleId }`.
