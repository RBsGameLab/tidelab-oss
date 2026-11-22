# Flows and flow actions

Scope: client-reported flow lifecycle (`FlowEndRequest`, `FlowActionRequest`), `FlowStartNotify`
and flow-action `TeleportNotify` rules, and the client rules governing cutscenes and dialogues.
Quest step triggers and entity interactions live in [quests.md](quests.md).

---

## 1. Implementation modules

| Area | Package / data |
|---|---|
| Single-active flow queue | `server/internal/game/flow_queue.go` |
| Flow actor wake/replay & server-side flow actions | `server/internal/game/flow_actions.go` |
| Quest-added NPC dialogue options | `server/internal/quests/flow_options.go` |
| Extracted flow action data | `FlowActions.json` (cached in `QuestData.bin`) |

---

## 2. Client hard rules for flows

1. **Never start server flows through `ActionNotify` (R-89)**: the 3.8 client rejects server
   flows triggered via `ActionNotify` (`PlayFlow`). Always send `FlowStartNotify` (25049)
   directly from the server, serialized through the flow queue so **at most one flow is active
   on the client at a time**.
2. **Preventing queue deadlocks (R-89)**: if a flow starts and nobody ever ends it, every later
   queued flow stalls forever (leaving a black screen or frozen cutscene). Therefore:
   - Fresh login (`EnterGameRequest`) clears the flow queue.
   - Any scene change releases a flow that was cut off by the switch so the next queued flow can
     proceed.
   - Robot runs must explicitly send `FlowEndRequest` for every `FlowStartNotify` they receive.
3. **Actor AOI asymmetry (`AwakeEntity` / `DestroyEntity`)**: the client sends
   `FlowActionRequest` (22962) **only** when the target entity is outside its AOI; otherwise it
   waits locally (`AwakeEntity`) or hides locally (`DestroyEntity`). Therefore:
   - Wake a flow's actors as soon as `FlowStartNotify` is issued (and run an immediate world
     spawn check so they appear before the client times out).
   - Replay the flow's `AwakeEntity` and `DestroyEntity` effects when `FlowEndRequest` arrives
     so world state stays consistent even when the client never sent `FlowActionRequest`.
4. **Flow `SetPlayerPos` waits for `TeleportNotify` (25575)**: when a flow executes a
   `SetPlayerPos` action, the client blocks until it receives `TeleportNotify` carrying:
   - `Reason (#7) = TeleportReason.FlowAction (10)`
   - `GameCtx (#9) = GameCtxPb { CtxType (#1) = GameCtxType.FlowActionCtx (33), FlowAction (#33)
     = FlowActionCtxPb { FlowListName (#1), FlowId (#2), StateId (#3), ActionId (#4) } }`
   - `Position (#2)` and `Rotation (#13)` (client reads `Rotation` as `X = Roll, Y = Pitch, Z =
     Yaw`; [movement.md](movement.md) §4.2).
5. **Dynamic flow options (R-74)**: options added via `AddFlowInteractOption` belong to the
   quest run that created them and disappear when it ends. They are announced with the adding
   action's `GameCtxPb`; picking one sends `EntityDynamicInteractRequest` ([quests.md](quests.md)
   §4.2) with `Interacting = true`, plays under `GameCtxType.DynamicInteract`, and unlocks the
   entity with `EntityInteractingNotify { EntityId (#13), IsInteracting (#2) = false }` when the
   flow finishes.
6. **Mixed client/server action lists (R-99)**: server player-movement actions (`VehicleEnter`,
   `VehicleExitPlayer`, `SetPlayerPos`) go out between client `ActionNotify` spans in list order
   ([quests.md](quests.md) §2).

---

## 3. Starting a flow (`FlowStartNotify`, 25049)

| Field | Wire # | Type | Notes |
|---|---|---|---|
| `HasPlotPos` | 3 | bool | optional plot anchor flag |
| `StateId` | 4 | int32 | state inside the flow |
| `PlotPos` | 5 | optional `Vector` | optional plot position |
| `FlowIncId` | 6 | int64 | unique id echoed back in `FlowActionRequest` and `FlowEndRequest` |
| `FlowListName` | 8 | string | flow list / namespace name |
| `PlotMode` | 9 | string | optional plot mode |
| `Async` | 10 | bool | async flag |
| `FlowId` | 12 | int32 | flow id inside `FlowListName` |
| `GameCtx` | 14 | `GameCtxPb` | context (present for quest/dynamic-interact flows) |
| `IsSkip` | 15 | bool | optional skip flag |

Flows enter the queue from three sources:
1. **Normal entity interact** with a `Flow` option ([quests.md](quests.md) §4.1): allocates
   `FlowIncId` from a counter starting at `100` (first issued `101`), registers flow actions to
   wake actors, enqueues `FlowStartNotify { FlowIncId, FlowListName, FlowId, StateId }`, and runs
   an immediate spawn check.
2. **Quest engine** (`PlayFlow` child quest, `DoInteract` / `AddFlowInteractOption` dynamic
   interact, or action-list `PlayFlow`): enqueued through the flow queue with its `GameCtx`.
3. **Scene change with `pending_flow`** ([enter-world.md](enter-world.md) §4 row 17): after
   `AfterJoinSceneNotify` and `UpdateFormationNotify` finish in the new scene, start the carried
   `pending_flow` through the flow queue as `FlowStartNotify { FlowIncId, FlowListName, FlowId,
   StateId }`.

---

## 4. `FlowEndRequest` (26672) → `FlowEndResponse` (29872)

Sent by the client when a flow finishes, is skipped, or exits on error.

- **Request**: `OptionInfos` (#3, `map<int32, FlowOptionInfoList>`), `FlowIncId` (#9, int64),
  `IsSkip` (#12, bool), `ClientError` (#14, bool).
- **Exact order of operations**:
  1. **Release the flow queue first (R-89)**: tell the flow queue that `FlowIncId` has ended. Any
     flow that was *already* waiting in the queue is pushed immediately as `FlowStartNotify`,
     **before** step 3 runs — so any new flow triggered by completing this quest step queues
     *behind* previously waiting flows rather than cutting ahead of them.
  2. **Finalize flow actions**: tell the flow-actions module that `FlowIncId` ended so it replays
     remaining `AwakeEntity` / `DestroyEntity` effects into world state and drops tracking for
     `FlowIncId`.
  3. **Advance quests**: tell the quest engine `FlowIncId` ended (`PlayFlow` / `DoInteract`
     completion, `EntityInteractingNotify { IsInteracting = false }` unlock, follow-up steps,
     possible pending scene transfer). Log and swallow errors rather than dropping the reply.
  4. **No `JSPatchNotify`**: in the 3.8 client `JSPatchNotify` is a no-op
     ([enter-world.md](enter-world.md) §6); tidelab sends no `JSPatchNotify`.
  5. **Reply**: send empty `FlowEndResponse {}` (`Code` #8 = 0 = `Success`).
  6. **Immediate spawn check**: run the immediate world spawn refresh ([quests.md](quests.md) §5.5) so actors
     woken by step 1, 2, or 3 appear immediately.
  7. **Pending scene transfer**: if step 3 queued a scene transfer (e.g. `TeleportDungeon`),
     execute it after step 6.

---

## 5. `FlowActionRequest` (22962) → `FlowActionResponse` (29226)

Sent mid-flow when the client needs the server to execute an action inside the currently playing
flow (e.g. `AwakeEntity` for an entity outside AOI, `DestroyEntity`, `SetPlayerPos`).

- **Request**: `ActionId` (#11, int32), `FlowIncId` (#14, int64).
- **Order of operations**:
  1. Execute `(FlowIncId, ActionId)` via the flow-actions module (may wake/hide entities, push
     `TeleportNotify { Reason = FlowAction, GameCtx = FlowActionCtx }`, etc.). On error, log a
     warning and continue.
  2. Reply `FlowActionResponse { Code (#4) = Success }`.
  3. Run the spawner's immediate spawn check ([quests.md](quests.md) §5.5) so awoken actors
     spawn before the client gives up waiting.

---

## 6. Tests

- **Queue ordering on `FlowEndRequest` (R-89)**: when flow A is active and flow B is queued, and
  ending A causes a quest step to enqueue flow C, B is pushed as `FlowStartNotify` before C, and
  C waits until B ends.
- **`FlowEndRequest` side effects**: replays flow actions, notifies quest engine, unlocks
  `Interacting`, sends **no** `JSPatchNotify`, replies `FlowEndResponse { Code = Success }`,
  then triggers immediate spawn and deferred scene transfer.
- **`FlowActionRequest`**: runs the tracked action, replies `FlowActionResponse { Code = Success
  }`, and runs immediate spawn check; unknown `(FlowIncId, ActionId)` still replies `Success`.
- **`SetPlayerPos` inside a flow**: emits `TeleportNotify` with `Reason = FlowAction`,
  `GameCtx.CtxType = FlowActionCtx`, and matching `FlowListName`/`FlowId`/`StateId`/`ActionId`.
- **Scene change & login**: login resets the flow queue; scene switch releases a cut-off active
  flow and emits a carried `pending_flow` after `UpdateFormationNotify`.
