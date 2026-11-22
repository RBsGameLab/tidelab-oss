# Quests, world interaction, level plays, and system hooks

Scope: request handlers for quests, level plays, and world interaction, plus every runtime hook
connecting quests to the gateway, scene join, world spawner, movement, combat, and roles
([enter-world.md](enter-world.md), [entities.md](entities.md), [spawning.md](spawning.md),
[movement.md](movement.md), [roles.md](roles.md), [combat.md](combat.md)). Flows live in
[flows.md](flows.md); dungeons and the tower live in [dungeons.md](dungeons.md).

---

## 1. Implementation modules

| Area | Package / data |
|---|---|
| Quest engine, ownership, presentation | `server/internal/quests/engine*.go`, `presentation.go` |
| Behaviour-tree variables & conditions | `server/internal/quests/conditions.go`, `tree.go` |
| Entity actions, action params, client action list, state setters | `server/internal/game/entity_actions.go`, `server/internal/quests/action_params.go`, `client_actions.go` |
| Clearings (hide groups), entity groups, schedules, quest rides | `server/internal/quests/hide_groups.go`, `vehicles.go`, `server/internal/game/entity_groups.go`, `schedule.go` |
| Prefab groups & spawner waves | `server/internal/game/prefabs.go`, `server/internal/quests/prefabs.go` |
| Area states & moved entities | `server/internal/game/world_systems.go`, `server/internal/save/worldstate.go` |
| Range entities (`InitRangeRequest`, `EntityAccessRangeRequest`) & patrol reports | `server/internal/game/world_systems.go` |
| Generated quest/play data (cached in `QuestData.bin`) | `DungeonPlays`, `EntityGlobals`, `FlowActions`, `HideGroups`, `Prefabs`, `QuestOffers`, `QuestOwned`, `Ranges`, `StateSetters`, `TowerFloors`, `TreeOrders`, `TreeVars`, `WorldPlays` |

Save formats (`quests`, `world_state`, `hidden_groups`, `area_states`, `entity_pos`) are listed in
[save-format.md](save-format.md) §6.

---

## 2. Quest engine and action-list invariants

These rules govern how the quest and interaction logic interacts with the 3.8 client:

1. **Behaviour-tree child order**: child execution order comes from `TreeOrders.json` (`Kids`),
   never from node table order. Saved progress in `quests` indexes the node table order, so
   generators must never reorder `Nodes`.
2. **Progressive node delivery & once-per-session sync (R-117, R-124)**:
   - A running tree's node is sent to the client only once the run has reached it (the client
     constructs a missing node on its first status update and looks its NPC up on the map loaded
     at that moment).
   - Quest offers (`QuestReadyListNotify`) and running trees (`BehaviorTreeInfoNotify`) go out
     **once per session**, not on every scene switch — the client retains both across scene
     changes, and re-sending an existing tree produces duplicate-node errors.
3. **Action indices and bundles (R-60)**: never append synthetic actions to a node's Enter or
   Finish lists in generated data — the client indexes actions by its own table order. Server-only
   side effects live in separate node metadata (`Bundle`).
4. **Action list interleaving (R-99)**: a step's action list mixes client actions and server
   actions, and the client runs its half regardless of the server. Emit server-side player moves
   (`VehicleEnter`, `VehicleExitPlayer`, `SetPlayerPos`) between client action groups in list
   order.
5. **`ActionNotify` (27631) bounds and whitelist**: `EndIndex` (#10) is **inclusive**. Send only
   actions registered by the client (`internal/quests/client_actions.go`).
6. **Step params vs entity params**: quest step action parameters live in `Raw`; `Params` holds
   only the subset copied for entity actions.
7. **Finish-action re-entry guard (R-42)**: a node's Finish actions run before the node is marked
   done and can satisfy its own condition again; guard against re-entry.
8. **`ParallelSelect` count 0 (R-80h)**: `Count == 0` means **all** children must complete.
9. **Entity-state checks**: read the saved entity state first; if unset, fall back to the spawned
   entity's default state (e.g. resonance beacons spawn in the active state `激活`).
10. **Condition visibility boundary (R-83)**:
    - Conditions judged during normal progression may wait only on state the server observes
      changing; entity variables and globals written only by client-side entity logic or an
      unrun level play evaluate as unknown (treated as **met**).
    - A quest's own variable is judged only if a quest action writes it and the run started under
      tracked variable recording.
    - Progress fix processors (`Fix` nodes, R-82a) evaluate only **known** conditions (unknown
      evaluates as **unmet**), because they teleport the player; no periodic pull-back may land
      outside its own valid range.
11. **Entity waking, sleeping, and clearings (R-25, R-67, R-69)**:
    - Every wake/sleep mutation to world state must be persisted immediately.
    - Finished quests put their woken actors back to sleep; sleeping rows still claim their
      spawn spot when building open-world candidates.
    - Quest clearings (`HideGroup` / `ShowHidedGroup`, R-69) are tracked by owner prefix
      (`q_<quest>_`, `l_<play>_`) and released when their owner finishes.
12. **Non-story quest distribution (R-62)**: driven by `QuestData.DistributeType` via
    `QuestOffers.json`. `System` quests auto-accept once conditions hold; `Interact` quests are
    offered and accepted through their NPC's dynamic option. Untracked conditions
    (`SystemState`, `OpenDate`) count as unmet; `PreLevelPlay`, `PreDungeon`, and `Item` are
    tracked.
13. **Open-world level plays & client mirroring (R-62, R-92)**:
    - Open-world level plays run only when an active or offered quest waits on them
      (`WorldPlays.json`), starting when the player enters `EnterRadius` and persisting
      completion (`L` lines in `quests`; cleared dungeons persist as `D` lines).
    - Every level play the client should mirror sends `EnterLevelPlayNotify` (28613) before its
      tree runs and `LeaveLevelPlayNotify` (25269) when it ends.
14. **Dungeon exit via quest**: when a quest moves the player out of an entered dungeon
    (`DungeonId`), drop the dungeon's return state.
15. **Dynamic quest options (R-74)**: options added by `AddFlowInteractOption` belong to the run
    that added them and are removed when that run ends. They are announced with the adding
    action's `GameCtxPb`; picking one plays under `GameCtxType.DynamicInteract` referencing that
    context.

---

## 3. Quest and level-play request handlers

All message and field names below are 3.8 `Aki.Protocol` names.

### 3.1 `SubmitNodeRequest` (21759) → `SubmitNodeResponse` (21283)

Sent when a client-evaluated child quest node finishes (reach area, timer, photo, etc.).

- **Request**: `TreeOwnerId` (#3, int32), `NodeId` (#4, int32), `TreeIncId` (#12, int64).
- **Order of operations**:
  1. Tell the quest engine to submit `(TreeIncId, NodeId)`. Obtain `ErrorCode` (on internal
     error, log a warning and fall back to `ErrorCode.Success`).
  2. Reply `SubmitNodeResponse`:
     - `TreeIncId` (#4) = request's `TreeIncId`
     - `ErrorId` (#7) = returned `ErrorCode`
     - `NodeId` (#9) = request's `NodeId`
     - `TreeOwnerId` (#15) = request's `TreeOwnerId`
  3. If the quest engine queued a scene transfer (e.g. `TeleportDungeon`), execute it now
     ([movement.md](movement.md) §4.4).
  4. Run the spawner's immediate spawn check (§5.5) if submitting the node woke entities or
     queued a flow/prefab.

### 3.2 `HandInItemRequest` (21324) → `HandInItemResponse` (21737) (R-76g)

Sent when the player submits items at a `HandInItems` step UI.

- **Request**: `HandInItem` (#2, repeated `PbHandInItem`), `TreeOwnerId` (#8, int32),
  `NodeId` (#13, int32), `TreeIncId` (#15, int64).
- **Order of operations**:
  1. Tell the quest engine to process hand-in for `(TreeIncId, NodeId, HandInItem)`. Obtain
     `ErrorCode` (on internal error, log warning and fall back to `ErrorCode.Success`).
  2. Reply `HandInItemResponse { ErrorId (#3) = code }`.
  3. Apply any pending quest scene transfer.
  4. Run the spawner's immediate spawn check (§5.5).

### 3.3 `SubmitAwakeAndLoadEntityNodeRequest` (17608) → `SubmitAwakeAndLoadEntityNodeResponse` (16025)

Sent when the client reports that entities required by a node have been awoken/loaded.

- **Request**: `TreeOwnerId` (#2, int32), `NodeId` (#9, int32), `TreeIncId` (#13, int64).
- **Behaviour**:
  1. Tell the quest engine to submit `(TreeIncId, NodeId)` (same submission path as §3.1; on
     error fall back to `ErrorCode.Success`).
  2. Reply `SubmitAwakeAndLoadEntityNodeResponse { ErrorCode (#14) = code }`.
  3. (Does not run pending-transfer or immediate-spawn after this request.)

### 3.4 `QuestReviewInfoRequest` (23616) → `QuestReviewInfoResponse` (25121)

- **Request**: empty.
- **Behaviour**: reply empty `QuestReviewInfoResponse {}` (placeholder; `ErrorCode` #3 = 0 =
  `Success`).

### 3.5 `SetFocusModeDeterCondRequest` (23749) → `SetFocusModeDeterCondResponse` (15892)

- **Request**: `DeterCond` (#4, bool).
- **Behaviour**: reply `SetFocusModeDeterCondResponse { ErrorCode (#12) = Success }`.

### 3.6 `QuestRedDotRequest` (28280) → `QuestRedDotResponse` (29563) (R-87)

Sent when opening the quest menu or reading a quest entry.

- **Request**: `Operate` (#11, int32), `QuestId` (#14, int32).
- **Behaviour**: reply `QuestRedDotResponse { ErrorId (#6) = Success }`.
- **Why explicit**: an unhandled empty reply left `ErrorId` unset and stalled the client's async UI
  stack after repeated menu openings, preventing any further view (including Esc) from opening.

### 3.7 `TraceQuestRequest` (26774) → `TraceQuestResponse` (15680) (R-87)

Sent when pinning or unpinning a quest on the map/HUD.

- **Request**: `Operate` (#5, int32), `QuestId` (#10, int32), `TraceType` (#13, int32).
- **Behaviour**: reply `TraceQuestResponse { ErrorId (#14) = Success }`. Tracking state is not
  persisted on the server; the client tracks it for the session.

### 3.8 `LevelPlayStateListRequest` (17858) → `LevelPlayStateListResponse` (24664) (R-87)

Sent by the world map and Sola guide to query the states of level plays across one or more
instances.

- **Request**: `InstLevelPlayStateReqs` (#4, repeated `InstLevelPlayStateReq`), each carrying
  `InstId` (#1, int32) and `LevelPlayIds` (#2, repeated int32).
- **Behaviour**: reply `LevelPlayStateListResponse { ErrorCode (#14) = Success,
  InstLevelPlayStateRsps (#2) }`. For each requested `InstLevelPlayStateReq`, emit one
  `InstLevelPlayStateRsp`:
  - `InstId` (#1) = requested `InstId`.
  - For each `lp` in `LevelPlayIds`:
    - `LevelPlayStateDict` (#2): `lp -> state` queried from the quest engine (`0 = Close`,
      `1 = Wait`, `2 = Open`, `3 = Finish`).
    - `CompleteNumberDict` (#3): `lp -> 0` (partial completion counts are not tracked).
    - `HideDict` (#4): `lp -> false`.
    - `IsUnlockDict` (#6): `lp -> true`.
  - `HideGroupInfoDict` (#5) and `GainRewardInfoDict` (#7) stay empty.

### 3.9 `QuestBranchRequest` (19642) → `QuestBranchResponse` (20238)

- **Request**: empty.
- **Behaviour**: reply `QuestBranchResponse { ErrorCode (#6) = Success, QuestBranchInfo (#9) =
  {} }` (explicit non-null empty sub-message).

### 3.10 Level-play & action small replies

These requests come from client level-play queries and `ActionNotify` acknowledgements and are
answered with fixed success replies:

| Request | Response | Fields set |
|---|---|---|
| `LevelPlayVarRequest` (17974) `{ InstId (#3), LevelPlayId (#12) }` | `LevelPlayVarResponse` (21256) | `ErrorCode (#1) = Success` (`Vars` #14 empty) |
| `LevelPlayStateRequest` (16155) `{ ArenaId (#3), InstId (#11) }` | `LevelPlayStateResponse` (29313) | `ErrorCode (#11) = Success` (`LevelPlayStateDict` #5 empty) |
| `ActionFinishRequest` (27577) `{ ErrorMsg (#1), PlayerId (#3), IncId (#5), StartIndex (#14) }` | `ActionFinishResponse` (26069) | `Code (#3) = Success` |
| `OpenSystemBoardResultRequest` (24936) `{ IncId (#3), PlayerId (#5), Result (#6) }` | `OpenSystemBoardResultResponse` (23490) | `Code (#6) = Success` |

---

## 4. World interaction and client-reported entity state handlers

Lab hard rule: the client reports what its own local actions did via
`ChangeEntityStateRequest`, `EntitySendEventRequest`, `TargetGearHitRequest`, and
`ElevatorStateChangeRequest`. Each updates server state that quests and entity groups check.

### 4.1 `EntityInteractRequest` (23858) → `EntityInteractResponse` (29979)

Sent when the player selects a normal (config-defined) interaction option on an entity.

- **Request**: `OptionIndex` (#6, int32, 0-based), `EntityId` (#7, int64, scene net id),
  `VisionEntityId` (#14, int64).
- **Config id lookup**: look up `EntityId` in the scene's spawned entities to obtain its
  `config_id`; if not found, clamp `EntityId` to `[0, Int32.MaxValue]` as a fallback `config_id`.
- **Option lookup**: if `OptionIndex >= 0`, look up the `LevelEntityConfig` row for
  `(scene.map_id, config_id)` and merge its `TemplateConfig` ([entities.md](entities.md) §5.1).
  Read `ComponentsData.InteractComponent.Options[OptionIndex].Type` (if present and in range).
- **Order of operations**:
  1. Push `ShopInfoNotify` for every shop ([shops.md](shops.md) §2.1) so shop NPCs have up-to-date
     inventory before the interaction opens their UI.
  2. Reply `EntityInteractResponse { ErrorCode (#2) = Success, Interacting (#8) = false }`.
  3. If an option was found:
     - Run the server half of the option's actions (state changes, rewards, removals, lifts,
       entrances, doors, etc.) for `(EntityId, config_id, option)`.
     - Ensure any pending quest/door transfer (e.g. `ExitDungeon`, `TeleportDungeon`,
       `SetPlayerPos`) is executed after the rest of the handler finishes.
  4. **Flow option** (`option.Type == "Flow"` and `option.Flow` present):
     - Increment a monotonically increasing flow instance counter (starts at `100`, so the first
       issued id is `101`; shared with other normal-interact flow starts).
     - Record the started flow's actors/actions for
       `(player_id, flow_inc_id, FlowListName, FlowId, StateId)` — this wakes its actors.
     - Enqueue and start `FlowStartNotify` via the flow queue ([flows.md](flows.md) §1).
     - Run the spawner's immediate spawn check (§5.5) so awoken flow actors appear right away.
     - Apply any pending transfer and return (do **not** send `ActionNotify`).
  5. **Non-flow option**: construct `GameCtxPb`:
     - `CtxType (#1) = GameCtxType.NormalInteract (2)`
     - `NormalInteract (#4) = NormalInteractCtxPb { EntityCtx (#1) = { ConfigId = config_id,
       IncId = EntityId }, OptionIndex (#2) = OptionIndex }`
     - If `option` has an `Actions` array, push its client-registered action spans via
       `ActionNotify` (27631).
     - Otherwise (no option or no `Actions`), push a single fallback `ActionNotify` (27631):
       `{ PlayerId (#6) = player_id, IncId (#12) = 1, GameCtx (#1) = game_ctx, TotalCount (#2) = 1,
       StartIndex (#8) = 0, EndIndex (#10) = 0, NeedFinishReq (#11) = false }`.
     - Apply any pending transfer.

### 4.2 `EntityDynamicInteractRequest` (15821) → `EntityDynamicInteractResponse` (29039) (R-74)

Sent when the player selects an option dynamically added to an entity by a quest or offer
(`DoInteract` child quests, `Interact` quest offers, `AddFlowInteractOption`).

- **Request**: `OptionGuid` (#8, string), `EntityId` (#12, int64, scene net id).
- **Config id lookup**: look up `EntityId` in the scene to obtain `config_id`; if absent, use
  `EntityId` directly.
- **Order of operations**:
  1. Notify the quest engine of the dynamic interaction `(config_id, OptionGuid)`. It returns
     whether the option started a flow (`flow`), ran another action, or was unhandled.
  2. Reply `EntityDynamicInteractResponse`:
     - `ErrorCode (#4) = Success`
     - `Interacting (#8) = true` **if and only if** the quest engine started a flow for this
       option; otherwise `false`.
     - **Hard rule**: `Interacting = true` locks the entity's interact prompt on the client until
       the server later pushes `EntityInteractingNotify { EntityId (#13), IsInteracting (#2) =
       false }` when that flow ends ([flows.md](flows.md) §2).
  3. Run the spawner's immediate spawn check (§5.5).
  4. Apply any pending quest transfer (e.g. a dynamic option's `TeleportDungeon` / `ExitDungeon`).

### 4.3 `ChangeEntityStateRequest` (18733) → `ChangeEntityStateResponse` (21818)

The client ran a local `ChangeEntityState` action (area trigger, client-side option, minigame)
and reports the resulting state tag.

- **Request**: `StateTagId` (#10, int32), `EntityId` (#15, int32 — **this is the `LevelEntityConfig`
  config id (`PbDataId`), NOT a scene net id**).
- **Order of operations**:
  1. Reply `ChangeEntityStateResponse { ErrorCode (#7) = Success }`.
  2. If `EntityId != 0` and `StateTagId != 0`: resolve the entity's current scene net id (if
     spawned) by config id, and deliver `(net_id, EntityId, StateTagId)` to the client-state
     handler, which persists the new state, updates entity groups, and notifies the quest engine.

### 4.4 `EntitySendEventRequest` (22433) → `EntitySendEventResponse` (21355)

The client reports an entity self-event (e.g. a minigame's `FinishSendSelfEvent`).

- **Request**: `EventKey` (#5, string), `EntityId` (#13, int64, scene net id).
- **Order of operations**:
  1. Reply `EntitySendEventResponse { ErrorCode (#4) = Success }`.
  2. If `EntityId` exists in the scene with `config_id`, run the entity's `ConditionListener`
     server actions for `(EntityId, config_id, EventKey)`.

### 4.5 `TargetGearHitRequest` (20478) → `TargetGearHitResponse` (15616)

A player bullet or attack hit a target gear (trap, switch, target).

- **Request**: `BulletId` (#6, int32), `EntityId` (#7, int64, scene net id), `BulletTypes` (#8,
  repeated int32), `HitParts` (#9, repeated string).
- **Order of operations**:
  1. Reply `TargetGearHitResponse { ErrorCode (#1) = Success }`.
  2. If `EntityId` exists in the scene with `config_id`, run the target-gear hit state transition
     for `(EntityId, config_id)`.

### 4.6 `ElevatorStateChangeRequest` (22336) → `ElevatorStateChangeResponse` (19002)

A lift started moving or reached a stop after `ElevatorMoveNotify`.

- **Request**: `State` (#4, `ElevatorState`: `End` 0 stopped, `Forward` 1 going up, `Reverse` 2
  going down), `EntityId` (#9, int64, scene net id), `TagState` (#11, int32): the **target floor**
  (1, 2, …), not a tag. The client sends `Forward`/`Reverse` when the lift starts moving and
  `End` when it arrives.
- **Order of operations**:
  1. Reply `ElevatorStateChangeResponse { ErrorCode (#14) = Success }`.
  2. If `EntityId` exists in the scene with `config_id`: `Forward` → state `关卡.电梯.上升`,
     `Reverse` → `关卡.电梯.下降`, delivered as `(EntityId, config_id, TagID(state))` to the
     client-state handler (same path as §4.3: the elevator's `StateChangeBehaviors` run, quests
     waiting on the state advance, e.g. 139000027 node 154 on 109002461 `关卡.电梯.上升`).
     `End` changes nothing.

### 4.7 `SplineMoveRequest` (26613) → `SplineMoveResponse` (26766)

Already outlined in [movement.md](movement.md) §1: reply `SplineMoveResponse { ErrorCode (#3) =
Success }` first; if `Pos` (#1) is present and the quest-ride tracker reports that the player is
currently riding `EntityId` (#14), update both the entity's and the player's scene position to
`Pos` and run movement reactions (§5.4).

Likewise, in `VehicleRequest` (21983, [movement.md](movement.md) §5), when `EntityId` is not a
motorcycle entity, `IsGetOn == false`, and the quest-ride module reports the client left a quest
vehicle of that net id, reply `ErrorCode.Success` instead of `ErrVehicleEntityNotExist`.

---

## 5. Runtime hooks into R-143f and R-143g

This section closes every R-143h placeholder left by the earlier specs.

### 5.1 Session start ([enter-world.md](enter-world.md) §2)

- **On `EnterGameRequest` (fresh login)**:
  1. Arm the quest engine's "restored progress" flag so active steps' `Fix` processors run on
     the first move/tick (R-82a).
  2. Reset per-session quest tracking (clear delivered offers and delivered tree flags so the
     fresh client receives them once, R-117, R-124).
  3. Clear the flow queue (no flow from a previous session will ever send `FlowEndRequest`,
     R-89).
- **On `ReconnectRequest` (valid token)**:
  1. Reset per-session quest tracking (R-117; harmless if the client kept them). Do **not** arm
     `restore_pending` and do **not** clear the flow queue.

### 5.2 Scene join ([enter-world.md](enter-world.md) §4)

In exact order during every scene join (login, reconnect, or scene change):

1. Before loading the new scene's entities: if leaving a深塔 floor, restore the player's own
   saved formation before validating the formation (R-86; [dungeons.md](dungeons.md) §3).
2. At the start of `JoinSceneNotify` construction: release any flow cut off by the scene switch
   from the flow queue (R-89; [flows.md](flows.md) §1).
3. Inside `JoinSceneNotify.SceneInfo`: populate `AreaStates` (#17, repeated `SceneAreaState`) from
   the player's saved `area_states` file (`{ AreaId, State }`, R-82c); areas not listed start at
   `Area.IsInitActived` on the client.
4. Immediately after pushing `AfterJoinSceneNotify` (enter-world.md §4 rows 6–9):
   a. Sync player gender to the quest engine (`male = (basic_info.sex == 1)`, R-76a).
   b. Run quest sync on join: pushes `QuestFinishListNotify` (17785), `QuestListNotify` (18320),
      `BehaviorTreeInfoNotify` (16786, once per session for running trees, R-124), and
      `QuestReadyListNotify` (29359, once per session for offers, R-117).
   c. Refresh daily activity tasks (R-79; [activities.md](activities.md) §1.5), which may grant
      daily login rewards and push updates.
   d. Notify the dungeon module that the scene was joined so an entered dungeon starts its level
      play ([dungeons.md](dungeons.md) §2).
5. At the very end of scene join (after `UpdateFormationNotify`, enter-world.md §4 row 17): if
   the scene change carried a `pending_flow`, start it through the flow queue as
   `FlowStartNotify` ([flows.md](flows.md) §1).

### 5.3 Heartbeat (`HeartbeatRequest`, [network.md](network.md) §9)

After replying `HeartbeatResponse {}` and running any queued dev commands ([systems.md](systems.md)
§11), when the player is in a scene:

1. Advance quest timers (`quest.tick`) so time-based steps and woken actors advance even while
   standing still in cutscenes or menus.
2. Execute any pending quest scene transfer.
3. Run the spawner's immediate spawn check (§5.5).
4. Announce any newly dropped `.mail` files ([activities.md](activities.md) §5).

### 5.4 Movement reactions ([movement.md](movement.md) §2)

After every applied player position update (from `MovePackagePush` or `SplineMoveRequest`):

1. Mark player position dirty for 30 s deferred save.
2. Advance quest timers (`quest.tick`) and apply any pending quest scene transfer.
3. If the scene has world spawns:
   - Run beacon healing/revive within 1500 units ([combat.md](combat.md) §7).
   - Notify the quest engine of the player's new position (`onPlayerMoved`): starts open-world
     level plays whose `EnterRadius` contains the player, and checks quests waiting on items
     (R-62).
   - Run a world refresh if none has run in this scene yet or the player moved at least 800
     units since the last refresh.

### 5.5 World spawner integration ([spawning.md](spawning.md) §9)

This closes every question in [spawning.md](spawning.md) §9 and specifies prefab and dungeon-level
spawning logic:

| Spawner question / action | R-143h behaviour |
|---|---|
| Scenes with world spawns | Open-world instances (spawning.md §1), **or** any instance known to the quest engine as a quest dungeon, **or** the currently entered dungeon (`dungeon.isEntered(instance_id)`). |
| Candidate cache key | `(instance_id, entered_dungeon_bool, quest_offers_enabled_bool)`. |
| Entered-dungeon level-play entities | When `entered` is true, resolve the dungeon's level play ([dungeons.md](dungeons.md) §2) and collect every entity id that play acts on. Awake rows in that set are included in Pass 1b **regardless** of blueprint type and bypass the awake name filter (e.g. dungeon start devices like `TsEntity_副本_挑战交互`). |
| Entered-dungeon map monsters | When `entered` is true, every wanted monster row in Pass 2 starts `sleeping = true` (spawns only when the dungeon's level play or spawner wakes it). |
| Quest-owned & kill-target monsters | In Pass 2, an `InSleep` wanted monster row owned by a quest (`QuestOwned.json`) or targeted by a quest kill step starts `sleeping = true` and does not consume an open-world duplicate spot claim. |
| Referenced quest rows | In Pass 3, any row (even `IsHidden`) whose `EntityId` is referenced by quest data (with current `offers_enabled` setting) and not already taken by an earlier pass is added as `sleeping = true`. |
| Quest clearings (`HideGroup`) | Checked during both removal of already-spawned entities and candidate filtering: `hide_groups.isHidden(map_id, config_id, is_awake)` (R-69). |
| Moved entities (`SetEntityPos`) | Before distance checks during refresh, override a candidate's `location` (and `rotation = (0, 0, yaw)` if yaw was set) from `entity_moves` (R-82e). |
| Woken dungeon monster level | When spawning a woken monster row inside an entered dungeon with `InstanceDungeon.EntityLevel > 0`, override its `AttributeComponent.Level` to `EntityLevel`. A row counts as a monster if the spawner wave module mentions its `config_id`, **or** its `BlueprintType` contains `"Monster"`, **or** `BlueprintConfig.EntityType == "Monster"` (R-127, e.g. tower anti-deaggro entities). |
| Entity spawned callback | Immediately after adding a world-spawned entity (`EntityAddNotify`), notify the quest engine `(config_id, net_id)` so any active dynamic interact options for `config_id` are announced on that `net_id`. |
| Immediate spawn check | Triggered after `SubmitNodeRequest`, `HandInItemRequest`, `EntityInteractRequest` (flow), `EntityDynamicInteractRequest`, `FlowEndRequest`, `FlowActionRequest`, and `HeartbeatRequest`. Runs a world refresh immediately (updating the last world-refresh position to the current player position) whenever **any** of these is true: (a) a flow start/action marked a pending spawn (and clear that flag), (b) the last world-refresh position holds the forced-refresh sentinel (`X == 1e9`), or (c) the prefab spawn queue is non-empty. |
| Stale prefab cleanup on first refresh | On the first refresh of a scene, any persisted entity with `config_type == Prefab` is removed (level plays restart and spawn fresh prefabs). |
| `world_net_ids` tracking | When writing `player/<id>/world_net_ids`, include net ids of both open-world spawns and all currently alive prefab-group entities so a reconnect removes them too. |

#### Prefab and tower wave spawning (runs near the start of every world refresh)

Before iterating open-world candidates, drain every queued prefab spawn job (created by level-play
`CreatePrefab` actions and tower wave advances; data from `assets/quest/Prefabs.json`):

1. Mark the group `spawned = true` and look up its prefab entry in `Prefabs.json` (skip if
   missing).
2. Base position = `LevelEntityConfig(scene.map_id, job.pos_entity).Transform[0] / 100` if that
   row exists, else the player's current location (or `(0, 0, 0)`).
3. Collect all monster entries in the prefab: an entry is a monster iff its `BlueprintType`'s
   `BlueprintConfig.EntityType == "Monster"` (never check blueprint name prefix — R-60
   梦魇聚落 uses `branch2.7_430_Monster_...`).
4. Let `is_wave = (group.waves.len > 0)`. If `is_wave`, look up the `TowerConfig` row for
   `scene.instance_id` (R-127). On wave 0 (`group.wave == 0`), enqueue `TowerConfig.RoleBuff`
   (each `is_active = true, stack_count = 1`) onto every spawned player role entity in the scene.
5. Spawn count = `group.waves[group.wave].len` if `is_wave`, else `spots.len`. For each index `k`
   (skipping if `spots` is empty):
   - Start from spot `e = spots[k % spots.len]`, `property_id = 0`.
   - **Entity-wave mode** (`is_wave && group.entity_waves`, R-126 tower floor prefab): replace `e`
     with the prefab entry matching `wave[k]` and set `property_id = e.PropertyId`.
   - **Template-wave mode** (`is_wave && !group.entity_waves`, R-56/R-88/R-126): look up
     `TemplateConfig(wave[k])`. Check whether `prefab.towerEntity(tpl.BlueprintType)` provides a
     tower-specific prefab entity override (`t`, carrying `prefab_id`, `entity_id`, `property_id`,
     required so bosses receive the `爬塔` tag and arena-wide hate/sense — R-88, R-126). Shift
     `e.Pos[0] += 200 * floor(k / spots.len)`. Set `e.Id = t.entity_id` (or `tpl.Id`),
     `e.BlueprintType = tpl.BlueprintType`, `e.Name = tpl.BlueprintType`, and if `t` is present
     set `property_id = t.property_id`.
   - Location = `base + e.Pos`, rotation = `e.Rot`.
   - Construct synthetic entity attributes: if `property_id > 0`, use `AttributeComponent` with
     that `PropertyId` (R-126); if `InstanceDungeon.EntityLevel > 0`, override its `Level` to
     `EntityLevel` (so dungeon monsters never fall back to world level 85).
   - Config identity in `EntityPb`:
     - If tower override `t` applied: `ConfigType = Prefab`, `PrefabId = t.prefab_id`,
       `PrefabIncId = group.inc`.
     - Else if template-wave mode: `ConfigType = Template`.
     - Else (plain prefab or entity-wave): `ConfigType = Prefab`, `PrefabId = group.prefab_id`,
       `PrefabIncId = group.inc`.
   - Spawn the entity (`EntityAddNotify`), register its `net_id` in the prefab group, and if a
     tower `TowerConfig` matched, enqueue `TowerConfig.MonsterBuff` (`is_active = true,
     stack_count = 1`) onto the spawned monster (R-127).
6. After draining the job, persist updated `world_net_ids`.

### 5.6 Combat and role hooks ([combat.md](combat.md) §6, [roles.md](roles.md) §6.6)

- **On monster kill** (`monster_killed` event for `(net_id, config_id)`):
  1. Notify the quest engine of the kill (`quest.onKill(config_id)`).
  2. Notify entity groups (`entity_groups.onEntityState(config_id)`) so groups waiting on
     `IsDead` trigger (R-80a).
  3. Notify prefab groups (`prefab.onKilled(net_id)`); if a group/wave cleared, notify the quest
     engine (`quest.onGroupCleared(run, var_name)` — R-4c, R-127).
  4. If the killed `config_id` on `scene.map_id` is quest-owned (`QuestOwned.json`), put it back
     to sleep and save `world_state` immediately so it does not respawn every 3 minutes after its
     quest step (R-13).
- **On `UseSkillRequest`** (inside `CombatSendPackRequest`, [combat.md](combat.md) §9):
  When `CombatCommon.EntityId != 0` and `UseSkillInfo.SkillId > 0`, notify the quest engine
  (`quest.onUseSkill(skill_id)`) for server-judged `UseSkill` steps (R-14).
- **On `RoleSexChangeRequest`** ([roles.md](roles.md) §6.6):
  Update the quest engine's gender flag (`male = (target_gender == 1)`) for `CheckPlayerGender`
  branches (R-76a).

---

## 6. Tests

- **Request handlers**:
  - `SubmitNodeRequest` echoes `TreeOwnerId`, `TreeIncId`, `NodeId`, returns `ErrorId`, then runs
    pending transfer and immediate spawn check.
  - `HandInItemRequest` forwards `HandInItem`, returns `ErrorId`, then runs pending transfer and
    immediate spawn check.
  - `QuestRedDotRequest` and `TraceQuestRequest` explicitly set `ErrorId = Success` (R-87).
  - `QuestBranchRequest` returns non-null `QuestBranchInfo`.
  - `LevelPlayStateListRequest` returns per-instance maps matching quest engine play states
    (`Close`/`Wait`/`Open`/`Finish`), `CompleteNumber = 0`, `Hide = false`, `IsUnlock = true`.
  - `LevelPlayVarRequest`, `LevelPlayStateRequest`, `ActionFinishRequest`,
    `OpenSystemBoardResultRequest` return `Success`.
- **World interaction**:
  - `EntityInteractRequest`: pushes `ShopInfoNotify` before `EntityInteractResponse{Interacting =
    false}`; Flow option starts queued `FlowStartNotify` and wakes actors without sending
    `ActionNotify`; non-Flow option with actions sends client-registered `ActionNotify` spans
    with inclusive `EndIndex`; non-Flow option without actions sends the 1-count fallback
    `ActionNotify`.
  - `EntityDynamicInteractRequest`: returns `Interacting = true` iff a flow started; triggers
    immediate spawn and pending transfer.
  - `ChangeEntityStateRequest` treats `EntityId` as config id (`PbDataId`), ignores 0, updates
    saved state and quest/group checks.
  - `EntitySendEventRequest`, `TargetGearHitRequest`, `ElevatorStateChangeRequest` reply
    `Success` and dispatch to entity state/listener runners.
- **Hooks**:
  - Login arms `restore_pending`, resets per-session quest tracking and flow queue; reconnect
    resets offers without arming `restore_pending` or resetting flow queue.
  - Scene join sends `AreaStates`, syncs gender, pushes quest sync (offers and running trees once
    per session, R-117/R-124), refreshes daily activity tasks, starts entered dungeon play, and
    emits `pending_flow` last.
  - Prefab wave spawning respects `EntityType == Monster` (R-60), tower entity override (R-88,
    R-126), `EntityLevel` override (R-127), and tower `RoleBuff` / `MonsterBuff` (R-127).
  - Monster kill triggers quest kill, entity group `IsDead`, prefab group clear, and puts
    quest-owned monsters back to sleep (R-13).
- **Robot acceptance**:
  On a 3.8 save copy and fixed virtual-clock epoch, the robot passes:
  - `story` (3.8 baseline: 99/99 main-story runs),
  - `side` (3.8 baseline: 502 side quests),
  - `all` dungeons (3.8 baseline: 658 cleared),
  - plus targeted checks `offers`, `worldinst`, `ride`, `pos`.
