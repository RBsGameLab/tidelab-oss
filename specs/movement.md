# Movement, teleports, scene change

Movement updates, position persistence, beacon teleports, open-world scene changes, and vehicles.

## 1. `MovePackagePush`

The client pushes `MovePackagePush { MovingEntities[], SceneOwnerId }` for every entity it moves
(its roles, its summons and visions, a vehicle). No reply. For each `MovingEntityData { EntityId,
Originator, MoveInfos[] }`, in order:

1. Find the entity by `EntityId`. Not in the scene: skip it (log a warning, `entity
   with id N is not movable (originator: O, sender: P)`; this is the line the id-floor rule of
   entities.md §2 is about).
2. **Does this move carry the player?** Yes when the entity is one of the player's role entities,
   or it is *ridden*:
   - the player's vehicle entity (the motorcycle of entities.md §4.1, the only entity the server
     marks as a vehicle) when the player sits in it (`VehicleRequest`/`EntityEnterVehicleRequest` set it, §5), or whose
     `Originator` is one of the player's role entities (riding, the client moves the
     vehicle, not the role; the player's position stood still and nothing spawned along the
     way);
   - a level vehicle a quest step seated the player in (`internal/quests/vehicles.go`).
3. Take the **last** `MoveReplaySample` of `MoveInfos`. None: skip (warning).
4. If it has `Location`:
   - if the move carries the player, the player's location in this scene = `Location`
     (X, Y, Z);
   - the entity's location = `Location`;
   - if it also has `Rotation` (a `Rotator`): the entity's rotation (and the player's, if it
     carries the player) = (Roll, Pitch, Yaw);
   - then run the movement reactions (§2) for this entity.
   A sample without `Location` changes nothing.

Captured: the first pushes after loading move the role and the vision entity, e.g.
`{EntityId: <vision>, Originator: 777, MoveInfos: [{Location: (−143200, 63380, 7610),
Rotation: {Yaw: 45}, TimeStamp: 156.66}]}`. `Originator` was the player id there, not an
entity id; the ridden check compares it with role entity ids, so it only matters for vehicles.

`SplineMoveRequest { EntityId, Pos }` (R-99, a vehicle on a spline, every 2 s): answer `Success`
first; then, if the player rides that entity (R-143h's ride state), set the entity's and the
player's location to `Pos` and run the movement reactions.

## 2. Movement reactions

After each applied move, in this order (all with the **player's** location, whichever entity
moved):

1. Mark the scene's player position for saving (§3).
2. Quest timers and scene transfers the quest engine queued (R-143h; a transfer is a scene
   change, §4.3).
3. In scenes with world spawns (spawning.md §1):
   - **Beacon heal:** if the player is within **1500** of any beacon candidate's location
     (spawning.md §2 pass 1), every role entity below full HP is set to full and dead ones are
     revived, in one `CombatReceivePackNotify` (values and messages: g, `combat.md`). Checked on
     every move, so standing at a beacon after a teleport heals.
   - Open-world level plays near the player and quests that wait for items (R-62, R-143h).
   - **World refresh** (spawning.md §4) when no refresh ran in this scene yet, or the player
     is at least **800** from where the last one ran. The first refresh of a scene also drops
     saved monsters the spawner no longer wants (with entity persistence off, entities.md §11,
     there are none; tidelab can leave this out).

The lab rule: "the player's position, and so every world refresh, comes from MovePackagePush;
a ridden vehicle moves the player too (R-65, R-99). When nothing spawns around the player, first
check that the refreshes keep coming as they move."

## 3. Saving the position (`last_scene`, `scene/<i>/instance`)

- The player's location and rotation in a scene live in `scene/<instanceId>/instance`
  (`players[0].location`, `.rotation` = [Roll, Pitch, Yaw]; save-format.md §5.10).
- After a move, the save is due **30 000 ms** later (game clock); later moves before then do not
  push it back. The instance file is written then, and at every scene join and when the session
  ends (`Released`: everything pending is written).
- `last_scene` (`instance_id`) is written when the scene changes (§4) — never by movement.
- At login the player stands in `last_scene`'s instance at that instance's saved location (§4.4).

## 4. Teleports and scene changes

### 4.1 `TeleportDataRequest`

Answer `TeleportDataResponse { ErrCode = Success, Ids = every Teleporter.Id }` (all beacons
unlocked; captured 3953 bytes).

### 4.2 `TeleportTransferRequest { Id }` (beacon travel; the robot uses it too)

1. `Teleporter` row `Id`; none: answer `ErrorCode = ErrTeleportIdNotExist`.
2. Its entity: `LevelEntityConfig` row (`Teleporter.MapId`, `Teleporter.TeleportEntityConfigId`)
   (looked up by map and id, R-80g); none: `ErrorCode = ErrTeleportCreatureIdNotExist`.
3. **Landing point (R-48):** p = `Transform[0]` / 100, yaw = `Transform[1].Z` / 100 (degrees).
   If the row's `TeleportComponent.TeleportPos` is an object {X, Y, Z, A} (missing or null
   members count 0):
   - p.X += X·cos(yaw) − Y·sin(yaw); p.Y += X·sin(yaw) + Y·cos(yaw); p.Z += Z;
   - facing = yaw + A.
   Else facing = yaw. Scale (`Transform[2]`) is ignored (dividing by it put the player inside 9
   scaled beacons before R-48).
4. If `Teleporter.MapId` equals the current instance id: push
   `TeleportNotify { MapId, Position = p, Rotation }` — same map, no scene change. Else: scene
   change to instance `Teleporter.MapId` at location p (§4.4).
5. Answer `TeleportTransferResponse { ErrorCode = Success, MapId, PosX, PosY, PosZ }` (after the
   pushes; `Pitch`, `Yaw`, `Roll` not set).

**`TeleportNotify.Rotation`.** The client's field is a `Vector` that it reads
as `Rotator(Pitch = Rotation.Y, Yaw = Rotation.Z, Roll = Rotation.X)`. Send
`Rotation = (X = 0, Y = 0, Z = facing)` so the player faces the beacon's direction after a
same-map teleport. `Reason` is not set (0) for beacon travel; flows set it
(`TeleportNotify { Reason = FlowAction, GameCtx = FlowActionCtx{…, ActionId} }` ends a flow's
`SetPlayerPos`).

On a scene change the landing rotation is lost: the new scene puts the player at p with the
instance's `BornRotation` (§4.4). Keep (observed); the better facing is a later improvement.

`TeleportFinishRequest`: answer `ErrorCode = Success`.

### 4.3 `EnterInstRequest` to a world instance (R-50c)

`EnterInstRequest { InstanceId, PosEntityId, … }` for an open-world instance id (8, 100, 900,
902, 903, 905, 906, 909, 910, 912: spawning.md §1) is a `TeleportDungeon` back to the open world,
not a dungeon (a quest action, or the robot):

1. `InstanceDungeon` row `InstanceId`; none: `ErrorCode = ErrInstanceConfigNotFound`.
2. If the player is in an entered dungeon, its level plays stop and the dungeon is left (R-143h).
3. Target = the `LevelEntityConfig` row (`InstanceDungeon.MapConfigId`, `PosEntityId`)'s
   `Transform[0]` / 100 if `PosEntityId` ≠ 0 and the row exists, else the instance's
   `BornPosition`.
4. Answer `ErrorCode = Success`, then the scene change (§4.4) to `InstanceId` at the target —
   unless the player is already in that instance (then nothing moves).

Never key this on `MapConfigId`: dungeons 85xx and 103 sit on maps 8 and 100 with their own
level plays (the robot lost 58 of them, lab rule). Every other `EnterInstRequest` (dungeons,
story instances) and `LeaveInstRequest`: R-143h; they end in the same scene change.

### 4.4 Scene change

1. Push `LeaveSceneNotify { PlayerId, SceneId = "", TransitionOption = {} }`.
2. `last_scene.instance_id` = the new instance; write `last_scene`.
3. The old scene is closed: pending saves written (§3), its entities dropped, its world spawns,
   respawn timers and refresh position forgotten (spawning.md §7).
4. The new scene loads from `scene/<new>/instance` (created if missing):
   - no player saved there yet (first visit): location = the target if one was given, else
     `InstanceDungeon.BornPosition`; rotation = `BornRotation`;
   - a player saved and a target given: location = target, rotation = `BornRotation`;
   - a player saved, no target (login): the saved location and rotation.
5. The scene-join sequence of enter-world.md §4 runs (without the login data of §3 there):
   explore pushes, `JoinSceneNotify`, `AfterJoinSceneNotify`, … .

The same steps (4 and 5) run at login for `last_scene`'s instance.

## 5. Vehicles

- `VehicleRequest { EntityId, IsGetOn }` (id 21983): the vehicle entity by net id. Unknown: if
  getting off a quest ride answer `Success`, else `ErrorCode = ErrVehicleEntityNotExist`. Known:
  occupied = `IsGetOn`; when getting on, the vehicle's location and rotation = the current role's.
  Answer `Success`.
- `EntityEnterVehicleRequest { VehicleId, IsGetOn }`: occupied = `IsGetOn` (unknown vehicle:
  `ErrVehicleEntityNotExist`). Answer `Success`.
- `VehicleFinishRequest`: answer `Success`.
- The occupied flag only feeds the ridden test of §1. Rides a quest starts are tracked by
  `internal/quests/vehicles.go`.

## 6. Small requests while moving

| Request | Server answer |
|---|---|
| `PlayerMotionRequest { Motion }` (captured `BeLand`) | empty response |
| `SitChairRequest { EntityId, IsSitDown }` (R-101) | `ErrorCode = Success`; nothing else (a non-Success code stands the role up again) |
| `EnterAreaRequest { Id }` | `EnterAreaResponse { Id }` (echo) |
| `UpdateSceneDateRequest { Hour, Minute }` | empty response (the scene time is not changed; captured `CurrDate` 0) |
| `TimeCheckRequest { ClientTime, RDilation }` | `TimeCheckResponse { ClientTime = request's, ServerTime = real ms, ServerStopTime = same, SceneStopTime = same, FlowTime = flow time }` (enter-world.md §4) |
| `TimeStopPush { TimeDilation }`, `RTimeStopPush { Dilation }` | change the flow-time dilation (enter-world.md §4) |
| `SceneBlockSplitPlayerNeedBlockPush`, `EntitySimplyMoveInfoPackagePush`, `MotorPositionStatePush` | no handler; logged and dropped (network.md §9) |

## 7. Tests

- `MovePackagePush`: role move updates player and entity; summon move updates only the entity;
  vehicle move carries the player when occupied or when `Originator` is a role entity; unknown
  entity and empty `MoveInfos` skipped; sample without `Location` ignored; last sample wins.
- Reactions: refresh only past 800 and on the first move; beacon heal radius 1500.
- Position save due 30 s after the first move; written at session end; `last_scene` only on
  scene change.
- Landing point: the R-48 example (Transform (10000, 20000, 300), yaw 90°, TeleportPos
  {X 100, A −90} → (100, 300, 3), facing 0); scale ignored; missing teleporter and missing row
  error codes.
- Same-map teleport sends `TeleportNotify` with `Rotation.Z` = facing; other map sends
  `LeaveSceneNotify` and runs the join; response after the pushes.
- `EnterInstRequest` to 8 lands on `PosEntityId`'s row, else on `BornPosition`; same instance:
  no change; a dungeon on map 8 (e.g. an 85xx id) is not treated as the open world.
- Robot (R-143i port): the `npcs` travel (spawning.md §8) and `worldinst` (R-77) reach every map.
