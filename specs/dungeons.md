# Dungeons, entrances, and the Adversity Tower (深塔)

Scope: request handlers for instance dungeons, dungeon entrances, rechallenge/reward claim, and
the Adversity Tower (逆境深塔), together with their scene-join hooks and client rules. Open-world
instance `EnterInstRequest` routing lives in [movement.md](movement.md) §4.3; prefab/wave spawning
and level-play entity filtering live in [quests.md](quests.md) §5.5.

---

## 1. Implementation modules

| Area | Package / data |
|---|---|
| Dungeon lifecycle, entrances, rewards, return point | `server/internal/game/dungeons.go`, `session_dungeons.go` |
| Adversity Tower (深塔) floors, seasons, teams, stars, rewards | `server/internal/game/tower.go`, `panel_tower.go` |
| Prefab groups & spawner wave tracker | `server/internal/game/prefabs.go`, `server/internal/quests/prefabs.go` |
| Generated dungeon & tower data | `DungeonPlays.json`, `TowerFloors.json`, `Prefabs.json` |

Save formats (`dungeon`, `tower`) follow [save-format.md](save-format.md) §6.

---

## 2. Client hard rules for dungeons and the tower

1. **Key on `InstanceId`, never `MapConfigId` (R-50c)**: dungeons such as 85xx and 103 sit on
   open-world maps 8 and 100 with their own level plays. Only the explicit open-world instance id
   list (`8, 100, 900, 902, 903, 905, 906, 909, 910, 912`; [movement.md](movement.md) §4.3) is
   an open-world return teleport; every other `InstanceId` enters as a dungeon/story instance.
2. **Dungeon level-play resolution (R-32, R-56, R-59)**: a dungeon's level play is the play on
   its map with the most `MonsterPreview` hits; dungeons without `MonsterPreview` (including
   无音区 `210404001–210404021` and 梦魇聚落 `210403001–210403005`, R-59/R-60) take theirs from
   `assets/quest/DungeonPlays.json`.
3. **Entered-dungeon spawning (R-4, R-127)**: inside an entered dungeon, entities its level play
   acts on spawn regardless of blueprint type and awake name filters; map monsters start sleeping
   and spawn only when woken, at `InstanceDungeon.EntityLevel`. `MonsterCreator` steps without a
   prefab variable run spawner waves.
4. **Client mirroring notifies (R-92)**: push `EnterLevelPlayNotify` (28613) before a dungeon's
   level-play tree starts and `LeaveLevelPlayNotify` (25269) when it finishes or is stopped.
5. **Run cleanup on end or abort (R-127)**: whenever a dungeon or level-play run ends (finished
   or aborted by leaving/restarting), discard its prefab groups and spawner waves immediately —
   scene net ids are reused across dungeon runs, and a stale group steals the next dungeon's
   kills.
6. **Quest exits drop return state**: if a quest transfers the player out of an entered dungeon
   (node `DungeonId`), discard the dungeon's saved return point.
7. **深塔 floor rules (R-41, R-46, R-56, R-84, R-85, R-86, R-88, R-126, R-127)**:
   - Each floor spawns its own prefab from `assets/quest/TowerFloors.json` wave by wave with
     per-entity `PropertyId` tuning at `InstanceDungeon.EntityLevel`; template-wave monsters with
     a tower prefab entry (e.g. 无冠者 `Monster104`) spawn as that tower prefab entity so the
     client grants the `爬塔` tag and arena-wide hate/sense (R-88, R-126; [quests.md](quests.md)
     §5.5).
   - `TowerConfig.RoleBuff` applies to every role on wave 0; `TowerConfig.MonsterBuff` applies to
     each spawned wave monster (R-127).
   - Entering a floor swaps the active formation to the team submitted in `TowerStartRequest`, and
     leaving the tower in **any** way restores the player's own formation during the next scene
     join (R-86).
   - Logging in while already standing inside a tower floor still initializes the floor's
     settlement path (R-84).
   - The 深境区 season listed and per-wave monster swaps can be set on the content panel
     (R-160c, [panel.md](panel.md) §6).
   - `TowerSeasonUpdateResponse.MaxUnlockDifficulty` must be `0`; a non-zero value makes the
     client re-open the 深境区&超载区 unlock popup every time the map refreshes the tower (R-46).

---

## 3. Dungeon request handlers

All message and field names are 3.8 `Aki.Protocol` names.

### 3.1 `InstEntranceDetailRequest` (19282) → `InstEntranceDetailResponse` (26537)

Sent by the Sola guide or a dungeon entrance panel to list the dungeons behind an entrance.

- **Request**: `EntranceId` (#11, int32).
- **Behaviour**: query the dungeon module for the entrance's `InstDetailEntry` list (from
  `InstanceDungeonEntrance`), and reply `InstEntranceDetailResponse`:
  - `ErrorCode` (#1) = `Success`
  - `EntranceId` (#3) = requested `EntranceId`
  - `InstDetailEntryList` (#4) = list from the dungeon module (`LimitEndTime` #2 stays 0).

### 3.2 `EnterInstRequest` (18872) → `EnterInstResponse` (18932)

Sent from a dungeon entrance, the Sola guide, a quest's client-side `TeleportDungeon` action, or
the robot (`worldinst`, story instances, `all` dungeons).

- **Request**: `InstanceId` (#12, int32), `PosEntityId` (#13, int32), plus `ContinueLastInst`
  (#2), `RoleIds` (#5), `EntranceId` (#6), `EnterInstContext` (#7), `TowerDefencePhantomId` (#14),
  `TransitionOption` (#15) (unused).
- **Order of operations**:
  1. Tell the dungeon module to enter `(InstanceId, PosEntityId)` (handles both open-world
     instance returns [movement.md](movement.md) §4.3 and entered dungeons/story instances,
     recording return point and queueing a scene transfer).
  2. If that fails (e.g. unknown `InstanceDungeon` id), log a warning and reply
     `EnterInstResponse { ErrorCode (#2) = ErrInstanceConfigNotFound }` without changing scenes.
  3. Otherwise reply `EnterInstResponse { ErrorCode (#2) = Success }`.
  4. Immediately execute the queued scene transfer ([movement.md](movement.md) §4.4).

### 3.3 `LeaveInstRequest` (21028) → `LeaveInstResponse` (20271)

Sent from the dungeon exit button or Esc menu to return to where the player entered from.

- **Request**: `PosEntityId` (#4, int32), `Way` (#15, `LeaveInstWay`) (unused).
- **Order of operations**:
  1. Tell the quest/dungeon engine to plan the exit (`planExit`: stops active dungeon level play,
     clears prefab/wave runs, queues transfer back to the saved return point or open world).
  2. Reply `LeaveInstResponse { ErrorCode (#10) = Success }`.
  3. Execute the queued scene transfer.

### 3.4 `GetInstExchangeRewardRequest` (25904) → `GetInstExchangeRewardResponse` (23064)

Sent when pressing the claim button on the dungeon settlement/reward screen (after a reward
entity's `SettlementDungeon` action).

- **Request**: `ExchangeCount` (#12, int32).
- **Behaviour**: tell the dungeon module to claim rewards for `ExchangeCount` (grants items via
  filtered inventory path, R-108/R-109). On failure, log a warning and reply
  `GetInstExchangeRewardResponse { ErrorCode (#13) = ErrInstanceConfigNotFound }`; on success
  reply `{ ErrorCode (#13) = Success }`.

### 3.5 `SingleInstRechallengeRequest` (20682) → `SingleInstRechallengeResponse` (23802)

Sent when pressing 重新挑战 on the dungeon result screen.

- **Request**: `RechallengeInInst` (#15, bool) (unused).
- **Order of operations**:
  1. Tell the dungeon module to rechallenge the current dungeon (cleans up previous run and queues
     re-entry transfer). On failure, reply `SingleInstRechallengeResponse { ErrorCode (#6) =
     ErrInstanceConfigNotFound }` and return.
  2. Reply `SingleInstRechallengeResponse { ErrorCode (#6) = Success }`.
  3. Execute the queued scene transfer.

### 3.6 `TeleportDungeonActionRequest` (17415) → `TeleportDungeonActionResponse` (18689)

Sent when the player confirms or cancels the team screen opened by a server-driven
`TeleportDungeonActionNotify`.

- **Request**: `IncId` (#6, int32), `IsConfirm` (#9, bool), plus `ContinueLastInst` (#2),
  `HostPlayerId` (#8), `RoleIds` (#12).
- **Order of operations**:
  1. Tell the dungeon module `confirmTeleport(IncId, IsConfirm)` (queues transfer if confirmed;
     log and swallow errors).
  2. Reply `TeleportDungeonActionResponse { Code (#1) = Success }`.
  3. Execute any queued scene transfer.

---

## 4. Adversity Tower (深塔) request handlers

All timestamps read from the game clock in seconds (R-129).

### 4.1 `TowerRequest` (26406) → `TowerResponse` (21047)

- **Request**: empty.
- **Behaviour**: build `TowerInfo` via the tower module for `(player_id, now_seconds)` and reply
  `TowerResponse { TowerInfo (#2) = info }`.

### 4.2 `TowerSeasonUpdateRequest` (24072) → `TowerSeasonUpdateResponse` (21534) (R-46)

- **Request**: empty.
- **Behaviour**: build `TowerInfo` as in §4.1 and reply `TowerSeasonUpdateResponse`:
  - `TowerInfo` (#4) = built `TowerInfo`
  - `MaxUnlockDifficulty` (#13) = **`0`** (hard rule §2.7: non-zero re-triggers the unlock popup
    on every map refresh).

### 4.3 `TowerStartRequest` (19933) → `TowerStartResponse` (27533) (R-86)

Sent when clicking the challenge button on a tower floor.

- **Request**: `TowerConfigId` (#7, int32), `Formation` (#12, repeated `TowerRolePb` `{ RoleId
  (#1), LeaveSkillId (#2), SkillBranchId (#3) }`).
- **Order of operations**:
  1. Ask the tower module to resolve and start `TowerConfigId`, obtaining its `InstanceDungeon`
     id `inst`. On error, log a warning and reply `TowerStartResponse { ErrorCode (#6) =
     ErrInstanceConfigNotFound }` and return.
  2. Ask the dungeon module to enter `(inst, pos_entity_id = 0)`. On error, log a warning and
     reply `{ ErrorCode (#6) = ErrInstanceConfigNotFound }` and return.
  3. **Swap to the tower formation BEFORE scene transfer (R-86)**: pass `(TowerConfigId,
     Formation)` to the tower module's `useFormation`; if it changed the scene's formation,
     persist the scene immediately so the incoming scene join spawns the chosen team.
  4. Reply `TowerStartResponse { ErrorCode (#6) = Success }` (`NeedUpdateSeason` #12 false).
  5. Execute the queued scene transfer.

### 4.4 `TowerResetRequest` (27650) → `TowerResetResponse` (26793)

- **Request**: `TowerConfigId` (#11, int32).
- **Behaviour**: placeholder — floor records are kept; reply `TowerResetResponse { ErrorCode
  (#14) = Success, TowerConfigId (#11) = request.TowerConfigId }` (`NeedUpdateSeason` #9 false).

### 4.5 `TowerApplyFloorDataRequest` (21733) → `TowerApplyFloorDataResponse` (18513)

- **Request**: `Apply` (#3, bool).
- **Behaviour**: placeholder — reply `TowerApplyFloorDataResponse { ErrorCode (#1) = Success }`
  (`NeedUpdateSeason` #10 false).

### 4.6 `TowerRewardRequest` (27282) → `TowerRewardResponse` (16113)

Claims star-milestone rewards for a difficulty (either a batch list or a single index).

- **Request**: `RewardIndexList` (#2, repeated int32), `RewardIndex` (#6, int32), `Difficulty`
  (#8, int32).
- **Behaviour**:
  1. Build the requested index list: use `RewardIndexList` if non-empty, otherwise a 1-element
     list `[RewardIndex]`.
  2. Call the tower module to claim rewards for `(Difficulty, wanted)`, collecting claimed
     indices in `got` (grants items and persists `tower`).
  3. Reply `TowerRewardResponse`:
     - `ErrorCode` (#4) = `Success` if `got.len > 0`, else `ErrActivityLock`
     - `RewardIndexList` (#15) = `got` (`NeedUpdateSeason` #3 false).

---

## 5. Scene-join and scene-switch hooks

1. **Before spawning entities in a newly loaded scene** ([enter-world.md](enter-world.md) §4 step
   4): call the tower module's `restoreFormation` so any exit from a tower floor (leave button,
   teleport, disconnect + relog to open world) restores the player's own saved team (R-86).
2. **During `AfterJoinSceneNotify`** ([enter-world.md](enter-world.md) §4, [quests.md](quests.md)
   §5.2 step 4d): call the dungeon module's `onJoined` so an entered dungeon starts its resolved
   level play (and emits `EnterLevelPlayNotify`, R-92).

---

## 6. Tests

- **Dungeon handlers**:
  - `InstEntranceDetailRequest` echoes `EntranceId` and returns `InstDetailEntryList` from
    `InstanceDungeonEntrance`.
  - `EnterInstRequest` replies `Success` **before** emitting `LeaveSceneNotify`; unknown
    `InstanceId` replies `ErrInstanceConfigNotFound` with no scene change; dungeons on open-world
    maps (e.g. 85xx, 103) enter as dungeons rather than open-world returns (R-50c).
  - `LeaveInstRequest`, `SingleInstRechallengeRequest`, and `TeleportDungeonActionRequest` reply
    before executing the queued scene transfer.
  - `GetInstExchangeRewardRequest` replies `ErrInstanceConfigNotFound` when no reward is active
    and `Success` when claimed.
- **Tower handlers**:
  - `TowerSeasonUpdateResponse.MaxUnlockDifficulty` is always `0` (R-46).
  - `TowerStartRequest` swaps formation and saves before scene transfer; leaving the floor
    restores the original formation on the next scene join (R-86).
  - `TowerRewardRequest` falls back to `[RewardIndex]` when `RewardIndexList` is empty, returns
    `Success` + claimed indices on first claim and `ErrActivityLock` + empty list on duplicate
    claim.
  - Leaving/restarting a dungeon clears prefab groups and spawner waves so reused net ids never
    trigger stale kills (R-127).
- **Robot acceptance**:
  - Full dungeon run (`all`) plus 无音区 (`210404001–210404021`) and 梦魇聚落
    (`210403001–210403005`) on one save copy and one virtual-clock epoch (3.8 baseline: 658 cleared).
