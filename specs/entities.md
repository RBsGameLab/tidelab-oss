# Entities

Entity ids, `EntityPb` and `EntityComponentPb` layout per kind, positions, state tags, role
entities, client-made entities, add/remove, persistence, and typed `ComponentsData`.

- Combat/role values inside `AttributeComponent`, `FightBuffComponent`, `PartComponent`,
  `EntityFsmComponentPb`, `SkillComponentPb` of roles, `VisionSkillComponent`, and `EquipComponent`
  are specified in [roles.md](roles.md) and [combat.md](combat.md).

## 1. `EntityPb`

Every entity the client is told about is one `EntityPb`. The kind-specific part is the component
list (§4–§7).

| Field | Value |
|---|---|
| `Id` | the net id (§2) |
| `ConfigId` | role id (roles), `LevelEntityConfig.EntityId` (level entities), `TemplateConfig.Id` (template entities), the entity's id inside the prefab (prefab entities) |
| `ConfigType` | `EntityConfigType`: `Character` roles, `Level` level rows, `Template` templates, `Prefab` prefab entities |
| `EntityType` | `EEntityType` (§3) |
| `Pos`, `InitPos` | both the same: the actor location (§3.2) |
| `Rot` | `Rotator`; Pitch, Yaw, Roll from the entity's rotation |
| `IsVisible` | true for every level entity, the player entity and the **current** role; false for the other roles, visions, summons, the vehicle and the battle entity |
| `IsActorVisible` | true for every entity |
| `PlayerId` | the player id for the player's own entities (roles, player entity, vehicle, battle entity, summons, visions, client stand-ins), 0 for level entities |
| `EntityState` | `EntityState`: `Born` for monsters spawned from level rows, `Default` for everything else |
| `Camp` | §5.4 for level entities; 0 for the player's own entities |
| `PrefabId`, `PrefabIncId` | prefab entities only (level plays, R-143h); else 0 |
| `GravityDirection` | (0, 0, −1) |
| `InitLinearVelocity` | (0, 0, 0) |
| `RoleSkinId`, `GlideWingSkinId`, `GlideBrollySkinId` | roles: from the role (roles.md); else 0 |
| `LivingStatus`, `DurabilityValue`, `IsPosAbnormal`, `SubEntityType`, `OwnerIncId`, `IsSnap` | 0 / false |
| `ComponentPbs` | §4–§7, in the order the tables list them |

## 2. Net ids

**Counter.** Net ids come from `state/player/<p>/scene/<instanceId>/entity/next`
(save-format.md §4). A scene takes ids in blocks of 256: when its block is used up (or the scene
was just loaded) it reserves the next 256 (counter += 256, file rewritten) and hands them out in
order. A scene loaded again starts a new block, so every login moves the counter by at least 256
(live save, map 8: counter `95059777256` before the captured login; the login's first id was
`95059777256`).

**Explicit ids.** When an entity is added with an id chosen by the caller, the counter is raised
to at least that id + 1. Asking for an id that is in use, or ≤ 0, is an error.

**The floor for world spawns.** Before every world refresh (spawning.md §4)
the counter is raised to at least

    floor = max((now_seconds − 1 700 000 000) × 1000, 1000)

where `now_seconds` is the game clock's Unix time **the first time this server process
refreshes this instance** (kept for the life of the process), and at least (largest net id in
the scene + 1). Why: after a server restart a reconnecting client keeps the entities it had
(network.md §8, R-121); a new process starting from small ids would hand out ids the client
still holds ("实体已经存在", interactions on the wrong entity). The time-based floor puts every
new process's world spawns above every earlier process's.

**Not for the entities made at scene join.** The vehicle, player entity, battle entity and the
role entities (§4, §6) take ids from the counter as it stands, without the floor (lab rule:
"Raise the net id floor only for world spawns; raising it for every id breaks role movement
(id 5)": with a fresh save the counter starts at 1 and the first role was entity 5; when the
floor was applied to every id, the client's `MovePackagePush` named role ids the server did not
hold, log `entity with id 5 is not movable`). On the live save the counter already sits above
an old floor, so role ids are large anyway (captured: roles 95059777260, …263, …266); what
matters is that the scene-join ids and the `FightRoleInfos` entity ids are the same numbers.

Ids are never reused within a counter's life. Saved state that names net ids
(`world_net_ids`, save-format.md §6) relies on that.

## 3. Kinds, position, state

### 3.1 Kind of a level entity

A level entity's kind comes from its row's blueprint, never from names (R-39, R-60):

1. `BlueprintConfig` row with `BlueprintType` = the `LevelEntityConfig.BlueprintType` (first
   row of that type in table order). No row: the entity cannot be spawned.
2. `TemplateConfig` row with the same `BlueprintType` (first in table order). No row: cannot be
   spawned.
3. `BlueprintConfig.EntityLogic` gives `EntityType`, `ConfigType` and `EntityState`:

   | `EntityLogic` | `EntityType` | `EntityState` | AI entity (§5.2) |
   |---|---|---|---|
   | `Item` | `SceneItem` | `Default` | no |
   | `Animal` | `Animal` | `Default` | yes |
   | `Monster` | `Monster` | `Born` | yes |
   | `Vehicle` | `Vehicle` | `Default` | no |
   | `Npc` | `Npc` | `Default` | yes |
   | `Vision` | `Vision` | `Default` | yes |
   | `ClientOnly` | `ClientOnly` | `Default` | no |
   | `Custom` | `Custom` | `Default` | no |
   | `ServerOnly`, `SimpleCombat` | not spawned (refused) | | |

   `ConfigType` is `Level`, except prefab entities (`Prefab`) and spawner waves spawned from a
   template (`Template`).

**`BlueprintConfig.EntityType`** (a string, distinct from `EntityLogic`) is what the spawner
uses to tell NPCs, monsters, chairs, entrances and traversal items apart (spawning.md §2). It is
an open string field in ConfigDB (279 distinct values in 3.8 data). The loader keeps `EntityType`
as a string and checks at load that every name the spawner compares with (spawning.md §2) occurs in
the data at least once, so a renamed value warns loudly instead of silently matching nothing. Same
for `EntityLogic` (10 values, data-tables.md §4).

### 3.2 Position (R-105)

`LevelEntityConfig.Transform` is a list of three vectors in hundredths of a game unit:
`[0]` location, `[1]` rotation in degrees (X, Y, Z), `[2]` scale (ignored). A row with fewer than
two vectors is never spawned.

- Location = `Transform[0]` / 100 (X, Y, Z).
- Rotation = `Transform[1]` / 100: X → Roll, Y → Pitch, Z → Yaw.
- For **characters** (kind `Animal`, `Monster`, `Npc`, `Vision`) the row places the feet;
  `EntityPb.Pos` is the capsule centre: add `BlueprintConfig.HalfHeight` to Z when it is > 0.
  Other kinds keep the row's location.

Checked against the capture: NPC 125000663 (`NPC301`, `HalfHeight` 80), row Z 404658 →
`Pos.Z` 4126.58; monster 110001099 (`Monster071`, `HalfHeight` 120), row Z 694135 → 7061.35;
beacon 113002862 (`Teleport001`, item) Z 411097 → 4110.97; NPC yaw row 9860 → 98.6.

The client's own ground fix lifts characters only sometimes (not `SimpleNPC` actors, not
`NpcPerformComponent.FixedPosition` NPCs, not those ready before the world's render assets), so
without the lift they sank into floors or were pushed up onto tables (R-105).

A quest may have moved an entity (`SetEntityPos`): the spawner then uses the moved
position and yaw instead (`entity_pos` file).

### 3.3 Entity state tag (scene items)

The client reads `StateTagComponentPb` unconditionally when initializing scene items; a scene item
without it fails init (e.g. `Teleport007` 回血触发器). So every entity of kind `SceneItem`, and
every entity whose merged components have an `EntityStateComponent`, gets one:

1. State name: if `BlueprintType` contains `Teleport`, `关卡.Common.状态.激活` (beacons spawn
   active; the quest rules count on it); else the template's `EntityStateComponent.State`, else
   the row's, else `关卡.Common.状态.常态`.
2. If the player changed this entity's state before (a chest opened, a door moved), the saved
   state wins (`world_state`, `internal/save/worldstate.go`).
3. `StateTagComponentPb.StateTagId` = the tag id of that name (§3.4).
4. The same id goes into `TagComponent.GameplayTags` (count 1) **and** into
   `TagComponent.EntityCommonTags`.

### 3.4 Gameplay tag ids from names

Tag names hash to ids as follows (checked: `关卡.Common.状态.激活` → −3775711;
`关卡.Common.状态.常态` → −1152559349):

1. h = 0 (32-bit unsigned).
2. For each UTF-16 code unit c of the name (characters above U+FFFF as their surrogate pair):
   h = (h XOR c) × 16 777 619, modulo 2³².
3. The raw id is h read as a signed 32-bit integer.
4. If `GameplayTagRemap` has a row with `RawId` = raw id, use its `CanonicalId`.

## 4. Entities made at scene join

At every scene join (enter-world.md §4), before `JoinSceneNotify`, the server creates these, in
this order, and lists them all in `JoinSceneNotify.SceneInfo.AoiData.Entities`:

| # | Entity | `EntityType` / `ConfigType` / `ConfigId` | Pos | Components |
|---|---|---|---|---|
| 1 | vehicle (the motorcycle) | `Vehicle` / `Template` / §4.1 | (−10000, −10000, −10000) | `AttributeComponent`, `TagComponent`, `FightBuffComponent`, `VehiclePb`, `MotorOutlookComponentPb`, `MotorDaCtxComponentPb`, `FollowEntityComponentPb` |
| 2 | player entity | `PlayerEntity` / `Template` / 14750001 | (−10000, −10000, −10000) | `AttributeComponent`, `FightBuffComponent`, `PlayerFollowersComponentPb` |
| 3 | vehicle companion | `Monster` / `Template` / 36700001 | the player's location | `AttributeComponent`, `FightBuffComponent`, `CombineEntityInfoPb`, `EquipComponent`, `LogicStateComponentPb`, `MonsterAiComponentPb`, `SummonerComponent`, `PlayerFollowerSelfComponentPb` |
| 4 | battle entity | `SceneEntity` / `Template` / 14000169 | (−10000, −10000, −10000) | `AttributeComponent`, `FightBuffComponent` |
| 5… | per formation slot: role, then its visions, then its concomitants (§6) | | | |

Positions of the far-away ones are literally (−10000, −10000, −10000); rotation 0.
`IsVisible`: only the player entity (and the current role). `PlayerId`: the player.

### 4.1 Vehicle (motorcycle)

- `ConfigId`: the first `ExploreTools` row with `SummonConfigId` ≠ 0 whose `SummonCfg`
  (`SummonCfg.Id` = that id) has a `BlueprintType` whose `BlueprintConfig.EntityLogic` is
  `Vehicle`; then the first `TemplateConfig` with that `BlueprintType`. 3.8 data: 36000052. No
  such row: the scene join fails.
- `AttributeComponent`: `BaseProperty` row 90000015.
- `TagComponent`: `GameplayTags` [{−849916103, 1}], `InitGameplayTag` true.
- `FightBuffComponent`: buffs 3000000801 and 3000020024 (each only if the `Buff` table has it),
  active, with handles as for any buff (so the bike's charged leap and vectored thrust work).
- `VehiclePb`: `Source` 0.
- `MotorOutlookComponentPb` (type `MotorOutlookEquippedPb`): `SkinEquipped`, `StickerEquipped`,
  `DecorationsEquipped`, `FrameEquipped` from `motor_info` (save-format.md §5.8).
- `MotorDaCtxComponentPb.MotorDaCtxId` = the vehicle's own net id.
- `FollowEntityComponentPb.EntityId` = the companion's net id (#3).

### 4.2 Player entity

- `AttributeComponent`: `BaseProperty` 390077025 (values: g).
- `PlayerFollowersComponentPb.ListPlayerFollowerPb`: six entries, `Type` in this order
  `EPlayerFollowerDefault`, `EPlayerFollowerExploreSkill`, `EPlayerFollowerAuxiliary`,
  `EPlayerFollowerSpecialItem`, `EPlayerFollowerMotor` (with `EntityId` = the vehicle's net id),
  `EPlayerFollowerMax`; the others' `EntityId` 0.

### 4.3 Vehicle companion

Template 36700001, `SummonCfg` 24000030, `BaseProperty` 90000004:

- `SummonerComponent`: `SummonerId` = vehicle net id, `SummonCfgId` 24000030, `SummonSkillId` 0,
  `PlayerId`, `Type` `ESummonTypeConcomitantMotorcycle`.
- `PlayerFollowerSelfComponentPb`: `PlayerEntityId` = player entity net id, `SummonConfigId`
  24000030.
- `MonsterAiComponentPb`: `CombatMessageId` 1, the rest 0. `LogicStateComponentPb` all 0.
  `EquipComponent` empty. `CombineEntityInfoPb` empty.
- `FightBuffComponent`: `SummonCfg.BornBuffId` (values: g).

### 4.4 Battle entity

Template 14000169, `BaseProperty` 390077025. `SceneInformation.SceneEntityId` carries the battle
entity's net id (or fixed 2).

## 5. Level entities (NPCs, monsters, animals, scene items)

Spawned by the world spawner (spawning.md) from one `LevelEntityConfig` row, looked up by
(map, `EntityId`) (R-80g; data-tables.md §2).

### 5.1 Components data

The row's `ComponentsData` is merged with its template's (`TemplateConfig.ComponentsData`, the
template of §3.1 step 2): for each of `BaseInfoComponent`, `AiComponent`, `AttributeComponent`,
`ModelComponent`, `MonsterComponent`, `RewardComponent`, `VisionCaptureComponent`,
`InteractComponent`, `EntityStateComponent`:

- the row has none: take the template's;
- both have one: start from the row's, and every top-level field the row's leaves out (absent
  or null) takes the template's value. Not deeper.

Other components come from the row only. A merged set lacking `BaseInfoComponent` is not
spawned.

### 5.2 Components per kind

**AI entities** (`Animal`, `Monster`, `Npc`, `Vision`), in this order:

| Component | Content |
|---|---|
| `AttributeComponent` | combat attributes from the merged `AttributeComponent`, the row's `AreaId` and the player's world level (values: g); an entity without one gets an all-zero component |
| `TagComponent` | §5.3 |
| `PartComponent` | only if `CharacterPartConfig` has a row for `ModelComponent.ModelType.ModelId` (values: g) |
| `FightBuffComponent` | `AttributeComponent.AppendBuffIds` of the merged set (values: g); else empty |
| `CombineEntityInfoPb` | empty |
| `LogicStateComponentPb` | all four states 0 |
| `MonsterAiComponentPb` | `WeaponId` = `AiComponent.WeaponId` (number or numeric string; else 0), `HatredGroupId` 0, `AiTeamInitId` **100**, `CombatMessageId` **1** (R-122) |
| `SkillComponentPb` | monsters only (R-122, §5.5) |
| `EntityFsmComponentPb` | only if the AI has a usable state machine (`AiComponent.AiId` > 0 → `AiBase.StateMachine` non-empty → `AiStateMachineConfig` row); contents: g (`combat.md`) |
| `StateTagComponentPb` | only if §3.3 applies |

Captured: monster 110001099 has all ten; the NPC 125000663 has `AttributeComponent`,
`TagComponent`, `FightBuffComponent`, `CombineEntityInfoPb`, `LogicStateComponentPb`,
`MonsterAiComponentPb` (no part, no skills, no state machine).

**Other kinds** (`SceneItem`, `Vehicle`, `ClientOnly`, `Custom`): `TagComponent` (§5.3, only when
§3.3 applies), `FightBuffComponent`, `StateTagComponentPb` (§3.3). Captured beacon: exactly
these three.

### 5.3 `TagComponent` of a level entity

1. Start empty (`InitGameplayTag` false).
2. Monsters' starting tags: each name in `MonsterComponent.InitGasTag` → its id (§3.4), count
   +1. Repeat-boss rule: if the entity's AI state machine (via `AiBase.StateMachine` →
   `AiStateMachineConfig.StateMachineJson`) has, on one node, a transition with a `CondTag` of
   tag 1639442014 (repeat boss) and another transition from the same `From` with tag
   −1276461747 (story boss), the init tag −1276461747 is replaced by 1639442014. (Captured:
   monster 110001099 carries 1639442014.)
3. The state tag of §3.3, if any.
4. `PartComponent` entries may add their active part tags (g).

### 5.4 Camp

`BaseInfoComponent.Camp` of the merged set; for monsters, if that is absent, 0 or 2 and the
template's `BaseInfoComponent.Camp` exists, the template's; if neither exists, 0. (Captured:
beacon 2, NPC 2, monster 1.)

### 5.5 Monsters' passive skills

The client drops AI buffs whose `PreMessageId` is 0, so every AI entity needs a non-zero
`MonsterAiComponentPb.CombatMessageId` (1) and monsters need their actor's passive GA skills in
`SkillComponentPb.GaPassiveSkills` (`server/internal/combat/passives.go`, entries like
`{SkillId: 80004001, ContextId: −1}`).

### 5.6 Kinds not spawned

`ServerOnly` and `SimpleCombat` blueprints; rows without a `BaseInfoComponent` after the merge;
rows whose blueprint or template is missing. The spawner retries such a row after an hour
(spawning.md §4).

## 6. Role entities and what comes with them

One role entity per filled formation slot (formation.md, g), in slot order. f says how the
entity is built; every value is g's.

| Field | Value |
|---|---|
| `EntityType` / `ConfigType` / `ConfigId` | `Player` / `Character` / role id |
| `Pos`, `Rot` | the player's location and rotation in this scene (`scene/<i>/instance`) |
| `IsVisible` | true only for the formation's current role |
| `RoleSkinId`, `GlideWingSkinId`, `GlideBrollySkinId` | g |

Components, in this order (captured, all three roles): `AttributeComponent`,
`FightBuffComponent`, `ConcomitantsComponentPb`, `EquipComponent`, `LogicStateComponentPb` (all
0), `SkillComponentPb`, `DependenceComponentPb`, `EntityFsmComponentPb` (the role's be-hit state
machine, g), `VisionSkillComponent`, `RoleWeaponSkinComponent`, `OrnamentComponentPb`,
`CalabashSkinComponentPb`.

- `DependenceComponentPb.DependenceEntityIds`: the four numbers 1, 2, 1, 2 (placeholder
  player/battle entity references accepted by the client).
- `ConcomitantsComponentPb.VisionEntityIds`: the net ids of the role's vision entities (0, 1 or
  2); `CustomEntityIds`: its concomitants' net ids; `PhantomRoleEid` 0.

Right after the role entity, in this order:

1. **Vision entities**, one for the role's main echo and one for its combo echo if it has them
   (which echo: g): `EntityType` `Vision`, `ConfigType` `Template`, `ConfigId` = the
   `TemplateConfig` whose `BlueprintType` is the echo's `SummonCfg.BlueprintType`; Pos/Rot =
   the instance's `InstanceDungeon.BornPosition` / `BornRotation` (not the player's location;
   captured map 8: (−143200, 63380, 7610), yaw 45); components `AttributeComponent`,
   `FightBuffComponent`, `CombineEntityInfoPb`, `EquipComponent`, `LogicStateComponentPb`,
   `MonsterAiComponentPb` (`CombatMessageId` 1), `SkillComponentPb`, `SummonerComponent`
   (`SummonerId` = role net id, `Type` `ESummonTypeConcomitantVision`, `SummonSkillId` = the
   summon id), `VisionSkillComponent` (empty).
2. **Concomitants** (summons a role always has, e.g. captured 2000064 with 1311): for each
   concomitant template of the role, one copy (role 1105: 3 copies, role 1301: 4):
   `EntityType` `Monster`, `ConfigType` `Template`; Pos/Rot = born position; components
   `AttributeComponent`, `FightBuffComponent` (its `SummonCfg.BornBuffId`), `CombineEntityInfoPb`,
   `EquipComponent`, `LogicStateComponentPb`, `MonsterAiComponentPb` (`CombatMessageId` 1),
   `SummonerComponent` (`Type` `ESummonTypeConcomitantCustom`, `SummonSkillId` 0).
   Which templates are a role's concomitants is defined in [roles.md](roles.md) §5.4 (matching the
   role skin's model against `Monster` blueprints whose name contains `Player` and `_`).

Role entity ids are written back into `formation_info` (`entity_id`, save-format.md §5.3) and
sent in `ScenePlayerInformation.PlayerSceneGroupData.FightRoleInfos` (enter-world.md §4).

## 7. Entities the client creates itself (R-107)

New in 3.8: `ApplySceneClientEntityIncIdRequest { EntityConfigId[] }`. The client asks for net
ids of entities it creates by `LevelEntityConfig` id and later sends `EntityActiveRequest` for
them. For each id, in request order:

- if the scene holds an entity with that `ConfigId`, `ConfigType` `Level` or `Template`, and no
  prefab: its net id;
- else a stand-in is added (no notify): `EntityType` `SceneEntity`, `ConfigType` `Template`,
  `ConfigId` = the id, Pos (−10000, −10000, −10000), `PlayerId` the player, not visible.

Answer `ApplySceneClientEntityIncIdResponse { InstId = scene instance id, ClientEntityInfo =
[{EntityConfigId, IncId = net id, ServerRealExist = true, IsClientEntityConfig = true}] }`.
Unanswered, the 3.8 client sent `EntityActiveRequest` for ids the server never had and its
loading stopped at 75 %. (The captured login did not send this request; it happens on maps
with such entities.)

## 8. `EntityActiveRequest`

The client sends it once per entity it activates (captured: dozens during loading). Answer:

`EntityActiveResponse { IsVisible, Pos, Rot, ComponentPbs }` built exactly like the entity's
`EntityPb` (§1), after bringing its buff timers up to date. `ErrorCode` default (Success).
An unknown net id replies `ErrorCode = ErrEntityNotFound` and keeps the session open.

## 9. Adding and removing

**Add.** `EntityAddNotify { EntityPbs = [the one entity] }`, one notify per entity, at the moment
the server adds it (`IsAdd` unset/false). No `JSPatchNotify` is sent (in the 3.8 C# client it is
a no-op, enter-world.md §6).

**Remove.** `EntityRemoveNotify { IsRemove = true, RemoveInfos = [{EntityId}] }`; one per
entity when the spawner drops one, several in one notify for the reconnect clean-up
(spawning.md §6). `EntityRemoveInfo.Type` not set. No `JSPatchNotify` is sent.

Remove from the client only what this connection was told about (spawning.md §6).

## 10. Visibility and state the server judges

- `EntityVisibleComponent` conditions are judged by the server (`internal/world/visibility.go`).
  Used by the spawner (spawning.md §3, §4).
- Woken/asleep rows, picked/opened entities and saved state tags live in
  `internal/save/worldstate.go` (file `world_state`, save-format.md §6). Every wake or sleep is
  followed by a save.
- Quest clearings (`HideGroup`): `internal/quests/hide_groups.go`.
- Requests the client sends about its own actions on entities (`ChangeEntityStateRequest`,
  `EntitySendEventRequest`, `TargetGearHitRequest`, `ElevatorStateChangeRequest`,
  `EntityInteractRequest`…) change state the story checks (`internal/game/entity_actions.go` and
  `internal/quests`).

## 11. What is saved under `scene/<instanceId>/`

- `instance` (save-format.md §5.10): rewritten at scene join, after a scene change, and by the
  movement save (movement.md §3).
- `entity/next`: the counter (§2).
- No per-entity files are written; every entity is rebuilt at each join and by the spawner.

## 12. Typed components for `ComponentsData`

The server reads 16 components of `LevelEntityConfig.ComponentsData` and
`TemplateConfig.ComponentsData` — `BaseInfoComponent`, `AiComponent`, `AttributeComponent`,
`ModelComponent`, `MonsterComponent`, `EntityStateComponent`, `RewardComponent`,
`VisionCaptureComponent`, `InteractComponent`, `EntityVisibleComponent`,
`SpawnMonsterComponent`, `ConditionListenerComponent`, `TargetGearComponent`,
`TriggerComponent`, `EntityGroupComponent`, `TeleportComponent` — and within them only the paths
`specs/data-tables.tsv` lists for those two tables (476 paths each). Components no path names are
ignored at load; open objects (`TeleportComponent.TeleportPos`, action `Params`) stay raw JSON.

## 13. Tests (R-143f)

- Net ids: blocks of 256 from the counter file; explicit id raises the counter; floor formula
  and that it is fixed per process and instance; scene-join entities take no floor; ids never
  repeat across two scene loads.
- `EntityLogic` → kind/state/AI table, including the two refused logics.
- Position: feet + `HalfHeight` for the four character kinds only; rotation mapping; the three
  captured rows of §3.2 give the captured `Pos`.
- Tag hash: the two names of §3.4; remap through `GameplayTagRemap`.
- State tag: beacon → 激活; saved override wins; tag in both tag lists.
- Component merge: absent component taken; null top-level field filled; nested field not.
- Camp rule for monsters and others.
- Component lists per kind equal the captured ones (§4, §5.2, §6), in order.
- `EntityAddNotify`/`EntityRemoveNotify` shapes; no `JSPatchNotify`.
- `ApplySceneClientEntityIncIdRequest`: existing entity keeps its id, unknown id gets a stand-in.
- `BlueprintConfig.EntityType`: every name tidelab compares with occurs in the data.
- Real client (acceptance with spawning.md): entities appear, nothing sinks into the floor, no
  `Callback执行异常` naming `EntityAddNotify`/`JoinSceneNotify` in the client log
  (`client_log_audit.py`).
