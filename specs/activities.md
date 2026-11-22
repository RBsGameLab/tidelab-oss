# Activities, Boss Rush, Lord Gym, Dream Link, Fishing, and Mail

Activity scheduling, sign-in, double rewards, tasks, scratch card, turntable, Boss Rush, Lord Gym,
Dream Link, fishing, and the file-backed mailbox.

---

## 1. Implementation modules

| Area | Package |
|---|---|
| Sign-in, double-reward, daily activity tasks, Boss Rush, Lord Gym, Scratch Card, Turntable, Fishing | `server/internal/game/activities.go`, `challenges.go`, `session_activities.go` |
| Dream Link / 失序梦途 (all 7 request handlers) | `server/internal/game/dream_link.go` |
| Mailbox `.mail` file format, UTF-8 handling, `cmd:` parser | `server/internal/game/mailbox.go` |

Save files (`activity_sign`, `activity_task`, `boss_rush`, `lord_gym`, `dream_link`,
`scratch_card`, `turntable`, `mailbox/<player>/*.mail`) follow [save-format.md](save-format.md)
§6–§7.

---

## 2. Client rules

1. **Populate every sub-message the client reads**: even when proto marks a field optional,
   missing sub-messages crash client parsing (e.g. `OneTimePointRewardActivityData.Task` must
   always be populated or the client reports `"活动数据异常"`).
2. **Never send `0` for table-looked-up ids**: any id the client resolves from config tables must
   be a valid table id (e.g. fishing ship, cabin, and handbook ids in `FishingInfoResponse`).
3. **Role rewards (R-38)**: when an activity reward's `item_id` exists in `RoleInfo`:
   - If the player does not own the role yet: create the default role record ([roles.md](roles.md)
     §1), save it, ensure it has its `InitWeaponItemId` equipped, and push
     `PbGetRoleListNotify` (23798) containing the newly granted role.
   - If the player already owns the role: grant `RoleInfo.SpilloverItem` (`item -> qty * count`)
     via the normal-item grant path (resonance chain waveband material).
   - Non-role items go through the normal-item grant path (single inventory writer `save-format.md` §5.11 R-108 + `ItemInfo`
     filter R-109).
4. **Mail UTF-8 and claim-before-command (R-55, R-102)**: the 3.8 C# protobuf decoder rejects
   invalid UTF-8 bytes in `MailInfosNotify` (trim BOM cleanly without splitting multibyte
   characters). On `MailGetAttachmentRequest`, persist the `claimed` state **before** running
   `cmd:` lines so a failing command never causes double-claiming.

---

## 3. Activity page and activity requests

All timestamps read from the game clock in seconds (R-129). Activity type constants from the
game's `Activity.Type` column: `Sign = 2`, `TimePointReward = 20`, `NewLordGym = 34` (others
owned by their exported logic modules).

### 3.1 `ActivityRequest` (16076) → `ActivityResponse` (15999)

- **Request**: empty.
- **Order of operations**:
  1. If the player is in a scene, refresh daily activity tasks (`activity_task.refresh`, R-79)
     before building the page so earned daily tokens/tasks are up to date.
  2. Filter `Activity` rows by excluding 85 retired/unsupported activity ids:
     - `100`
     - `100100001..100100020` (20 old sign-in events)
     - `100300001`, `100400001`
     - `100700001..100700036` (36 old double-drop events)
     - `100770001`, `100770002`, `100800001`, `100800002`
     - `100805001`, `100805002`, `100805003` (older Boss Rush editions; `100805004` is kept)
     - `101100001`, `101700001`, `101700002`, `102300013`, `102300020`
     - `102400001`, `102400002`, `102400003`, `102900001`, `102900002`
     - `103000001`, `103000002`, `103200001`, `103300001`, `103300002`, `103300003`
     - `103500001`, `103500002`, `103500003`, `104000001`, `104000002`
     - `104400001`, `105100001`, `105300002`, `105800001`, `107100001`
     - `107200001`, `107500002`, `108600001`
  3. R-160: the remaining rows are grouped into the activity plan's slots ([panel.md](panel.md)
     §2): one edition per type (automatic: the newest that has the type's data), double rewards
     one per sub type 2 and 3, each in its window. Run §2.3 of panel.md (a new window starts
     the progress over).
  4. Build `Activities` (#6, repeated `ActivityData`) from every open slot, newest id first:
     - **Double reward type** (7): `DoubleInstActivityReward (#13)` with today's used count.
     - **新领主挑战** (`34`): no payload (its UI reads `LordGymInfoResponse`).
     - **All other types**: the type's payload below; a slot whose payload is null is left out.
     - Every entry carries the slot's window (panel.md §2.2; R-52 cycles when automatic).
  5. Reply `ActivityResponse { ErrorCode (#10) = Success, Activities (#6) }`.

#### Per-type `ActivityData` `Data` oneof payload (3.8 field names & numbers)

| `Activity.Type` | `ActivityData` oneof field | How it is built |
|---|---|---|
| Dream Link (`dream_link.activity_type`) | `RogueWhiteCatData` (#32) | Look up `RogueWhiteCat(Id)`; read normal-item count of `EnergyId`; call `dream_link.data(..., energy, now)`. |
| Sign-in (`2`) | `SignActivity` (#11) | Look up `ActivitySign(Id)`; `SignStateList` (#1) from `activity_sign.states`, `RewardFree` (#2) = `entry.free_taken`. |
| Boss Rush (`boss_rush.activity_type`) | `BossRushActivityData` (#22) | Only if `Id == boss_rush.listedActivity()`; built by `boss_rush.data`. |
| Turntable (`turntable.activity_type`) | `TurntableActivityData` (#21) | Only if `Id == turntable.listedActivity()` and `TurntableInfo(Id)` exists and current round has awards: `IsAllFinish` (#1) = `s.all_done`, `CurRound` (#2) = `s.round`, `RewardedItem` (#3) = claimed award ids, `TurntableTasks` (#4) = daily task list when `TurntableType == daily_coins`. |
| Scratch Card (`scratch_card.activity_type`) | `ScratchCardActivityInfo` (#34) | Only if `Id == scratch_card.listedActivity()`; built by shared scratch-card builder (§3.4). |
| Time-Point Reward (`20`) | `TimePointRewardActivityData` (#24) | Collect up to 32 `TimePointRewardActivity` rows for `Id` sorted ascending by `Id`. Progress uses `activity_sign` (day `k` opens `k` days after `first_day`). Each `OneTimePointRewardActivityData`: `Id` (#1) = `row.Id`, `RewardTime` (#2) = `(first_day + k) * 86400`, `Rewarded` (#3) = `(state == IsReceive)`, `CanGetReward` (#4) = `(state == Unlock)`, `Task` (#5, mandatory R-37g) = `{ Id = row.Id, Current = (state == Lock ? 0 : 1), Target = 1 }`. |

### 3.2 Time-point reward requests (R-37)

- **`TimePointActivityRewardRequest` (25635) → `TimePointActivityRewardResponse` (18317)**:
  - Request: `Id` (#2, int32, `TimePointRewardActivity.Id`).
  - Look up row and its sorted 0-based index `k` within its `ActivityId` (missing →
    `ErrSignRewardCfg`). Verify `activity_sign.canClaim(entry, today, k, n)` (false →
    `ErrActivityLock`). Grant every `(Item1, Item2)` in `row.Rewards` via §2.3 (handles roles,
    R-38), mark index `k` claimed in `activity_sign`, reply `{ ErrorCode (#15) = Success }`.
- **`TimePointActivityBatchRewardRequest` (28173) → `TimePointActivityBatchRewardResponse` (15156)**:
  - Request: `Ids` (#5, repeated int32), `ActivityId` (#6, int32).
  - Run the single-claim logic above for each id in `Ids`. Reply `ErrorCode (#11) = Success` if
    at least one succeeded, otherwise the **first** error code encountered (or `Success` if `Ids`
    was empty).

### 3.3 Sign-in requests (R-37)

- **`SignActivityRequest` (29384) → `SignActivityResponse` (18799)**:
  - Request: `ActivityId` (#7, int32), `Index` (#10, int32, 0-based).
  - Look up `ActivitySign(ActivityId)` and validate `Index >= 0` (else `ErrSignRewardCfg`).
    Verify `activity_sign.canClaim(entry, today, Index, SignRewards.len)` (else `ErrActivityLock`).
  - Grant `SignRewards[Index]` (`ItemId`, `Count`) via §2.3 (handles roles, R-38) if both `> 0`,
    recording `ItemId -> Count` in `ItemMap` (#10).
  - Mark `Index` claimed in `activity_sign`.
  - Reply `SignActivityResponse { ErrorCode (#2) = Success, ItemMap (#10) }`.
- **`SignActivityFreeDropRequest` (27704) → `SignActivityFreeDropResponse` (15457)**:
  - Request: `ActivityId` (#10, int32).
  - Look up `ActivitySign(ActivityId)` (else `ErrSignRewardCfg`). If `entry.free_taken` or
    `FreeDropId <= 0`, reply `ErrActivityLock`.
  - Grant drop package `FreeDropId` via `interact_actions.grantDrop(..., 0, 1)`, mark free gift
    taken, reply `{ ErrorCode (#12) = Success }`.

### 3.4 Scratch card requests (R-79)

Shared `ScratchCardActivityInfo` builder for `activity_id`:
- Fetch rounds via `scratch_card.rounds(activity_id)` (empty → null).
- For each round: fetch `roundRewards`, compute `cells` and deterministic cell layout
  `layout(player_id, RoundId, cells)`, read `opened` bitmask; emit `ScratchCardRoundInfo`:
  - `RoundId` (#1) = `row.RoundId`
  - `UnlockTime` (#2) = `0` (only the prior round gates a card)
  - `RewardIds` (#3, `map<int32, ScratchCardRewardData>`) = each opened cell `cell -> { ItemId
    (#1), Num (#2) }`
  - `LeftRewardItem` (#4, `map<int32, int32>`) = remaining unopened rewards from
    `scratch_card.leftRewards`
- Populate `ScratchTimeInfos` (#2) from `activity_task.scratchDefs` for today's waveplate spend
  and claimed flags.

Handlers:
- **`ScratchCardActivityInfoRequest` (27153) → `ScratchCardActivityInfoResponse` (24195)**:
  - Request: `ActivityId` (#6, int32; if `0`, default to `scratch_card.listedActivity()`).
  - Run `activity_task.refresh`, build `ScratchCardActivityInfo`; if null reply `{ Error (#5) =
    ScratchCardNoActivityConfig }`, else `{ Error (#5) = Success, Info (#7) }`.
- **`ScratchCardRewardRequest` (23653) → `ScratchCardRewardResponse` (15806)**:
  - Request: `Index` (#4, int32, 0-based cell), `RoundId` (#5, int32).
  - Validation order:
    1. `ScratchCardRoundRe(RoundId)` exists (else `ScratchCardNoRoundConfig`).
    2. `ScratchCardActivityRe(round.ActivityId)` exists (else `ScratchCardNoActivityConfig`).
    3. `0 <= Index < cells` (else `ScratchCardIllegalIndex`).
    4. If `round.PreRoundId != 0`, prior round is completely opened (else `ScratchCardRoundNoUnlock`).
    5. Cell `Index` is not yet opened (else `ScratchCardIndexRewarded`).
    6. Player has at least `1` of `act.ItemId` (else `ScratchCardNoTime`).
  - Take `1` of `act.ItemId`, resolve the cell's `(Item1, Item2)` reward from deterministic
    `layout`, grant item if both `> 0`, set bit `Index` in `scratch_card` persistence.
  - Reply `ScratchCardRewardResponse`:
    - `Error` (#12) = `Success`
    - `OpenType` (#7) = `scratch_card.open_center`
    - `OpenIndex` (#10) = `{ Index -> { ItemId (#1), Num (#2) } }`
    - `ItemMap` (#2) = `{ Item1 -> Item2 }`
    - `LeftRewardItem` (#13) = updated `leftRewards`.

### 3.5 Turntable request (R-79)

- **`RunTurntableActivityRequest` (15007) → `RunTurntableActivityResponse` (15518)**:
  - Request: `ActivityId` (#1, int32; if `0`, default to `turntable.listedActivity()`).
  - Validation order:
    1. `TurntableInfo(id)` exists (else `ErrTurntableConfigNotFound`).
    2. `id == turntable.listedActivity()` (else `ErrTurntableActivityNotOpen`).
    3. Run `activity_task.refresh` so daily task coins are granted before checking balance.
    4. Load turntable state; if `all_done` → `ErrTurntableActivityIsFinish`.
    5. Load current round's awards (empty → `ErrTurntableActivityRoundConfigNotFound`); compute
       unclaimed picks (empty → `ErrTurntableActivityIsFinish`).
    6. Let `cost = max(info.CostItemCount, 1)`. Check normal-item count of `info.CostItemId >=
       cost` (else `ErrItemNotEnough`).
  - Deduct `cost` of `info.CostItemId`, pick uniformly from `picks` via CSPRNG, grant every
    entry in `award.RewardItem` via §2.3 (handles roles, R-38), update and save turntable state
    via `turntable.afterSpin` / `turntable.nextRound`.
  - Reply `RunTurntableActivityResponse`:
    - `ErrorCode` (#4) = `Success`
    - `RewardId` (#13) = `award.Id`
    - `IsFinishCurRound` (#9) = `award.IsSpecial`
    - `ItemMap` (#14) = granted `item_id -> count` map.

---

## 4. Boss Rush, Lord Gym, and Fishing

### 4.1 Boss Rush

| Request | Response | Behaviour |
|---|---|---|
| `BossRushChooseBuffRequest` (29389) | `BossRushChooseBuffResponse` (19084) | Placeholder: `{ ErrorCode (#12) = Success }` (`BuffIds` #6 empty) |
| `BossRushBuffChooseResultRequest` (23159) `{ Index (#5) }` | `BossRushBuffChooseResultResponse` (24781) | Placeholder: `{ ErrorCode (#4) = Success }` |
| `BossRushSettlementRequest` (19575) | `BossRushSettlementResponse` (25837) | Placeholder (scoring happens at `FinishDungeon`): `{ ErrorCode (#15) = Success }` |
| `BossRushClaimRewardRequest` (17500) `{ RewardType (#3), ActivityId (#13), RewardId (#14) }` | `BossRushClaimRewardResponse` (23469) | Call `boss_rush.claim(RewardId, RewardType ?? Level)` (default `Level = 0`) and reply `{ ErrorCode (#4) = code }` |
| `BossRushBatchClaimRewardRequest` (27431) `{ ActivityId (#6), TaskRewardIds (#9) }` | `BossRushBatchClaimRewardResponse` (26097) | Placeholder (unimplemented): `{ ErrorCode (#12) = ErrActivityLock }` |
| `BossRushLevelScoreRewardRequest` (24337) `{ BossRushActivityId (#4), Index (#10) }` | `BossRushLevelScoreRewardResponse` (17469) | Placeholder (empty reward list): `{ Error (#7) = ErrActivityLock }` |

### 4.2 Lord Gym (领主挑战, R-37)

| Request | Response | Behaviour |
|---|---|---|
| `LordGymInfoRequest` (24178) | `LordGymInfoResponse` (24508) | Reply built directly by exported `lord_gym.info(player_id, now_seconds)` (`EntranceInfos` #5, `ReadLoadGymIds` #11, `LordGymGroupInfos` #12, `UnlockLoadGymIds` #13, `LordGymPassRecords` #15) |
| `LordGymBeginRequest` (16902) `{ LoadGymId (#1) }` | `LordGymBeginResponse` (25090) | Call `lord_gym.begin(LoadGymId)` (starts the difficulty's level play); on error reply `{ ErrorCode (#3) = ErrLordGymConfigNotFound }`, else `Success` |
| `LordGymReadRequest` (22064) `{ LoadGymId (#10) }` | `LordGymReadResponse` (21070) | Call `lord_gym.markRead(player_id, LoadGymId)` and reply `{ ErrorCode (#14) = Success }` |

### 4.3 Fishing (R-115)

- **`FishingInfoRequest` (18438) → `FishingInfoResponse` (22673)**:
  - Request: empty.
  - Reply `FishingInfoResponse { FishingInfo (#6) }` populated with valid `ShipInfo`, cabin, and
    handbook config ids so client config lookup never fails on `0`.

---

## 5. Mailbox

Storage and `.mail` parsing follow [save-format.md](save-format.md) §7, with state bitmask flags
`state_read` (1) and `state_claimed` (2).

### 5.1 Login push and live delivery

- **At login** ([enter-world.md](enter-world.md) §3 row 29): list all mails for `player_id`;
  for each mail where `!mail.announced`, write initial state `0` with `now_seconds`; push
  `MailInfosNotify` (20632) `{ MailInfos (#7) }`.
- **On every `HeartbeatRequest`** ([quests.md](quests.md) §5.3): list mails for `player_id`; for
  each mail with `!mail.announced`, write state `0` with `now_seconds` and push `MailAddNotify`
  (21273) `{ NewMail (#3) = toPb(mail) }` (`Reason` #7 = 0). This delivers newly dropped `.mail`
  files without a server restart.

### 5.2 Mail request handlers

| Request | Response | Behaviour |
|---|---|---|
| `MailBindInfoRequest` (19713) | `MailBindInfoResponse` (24222) | Reply empty `{}` |
| `MailReadRequest` (20227) `{ Id (#6) }` | `MailReadResponse` (23666) | Load mail `Id` (missing → `{ Id (#5) = Id, ErrorCode (#14) = RequestParamError }`); write state `mail.state \| state_read` at `now_seconds`; reply `{ Id (#5), State (#13) = mail.state, ReadTime (#15) = now_seconds, ExpiryTime (#3) = Mailbox.expiryTime(mail) }` (`ErrorCode` #14 = `Success`) |
| `MailGetAttachmentRequest` (15409) `{ MailIds (#1) }` | `MailGetAttachmentResponse` (26747) | For each `id` in `MailIds`: load mail (skip if missing or already `state_claimed`). **First** persist `mail.state \| state_read \| state_claimed` at `now_seconds`. Then dispatch each `cmd:` line in `mail.cmds` via the command dispatcher ([systems.md](systems.md) §11), logging and continuing on command errors. Record `mail.id -> mail.state` in `SuccessIdMap` (#5). Reply `{ SuccessIdMap (#5) }` (`ErrorCode` #3 = `Success`) |
| `MailDeleteRequest` (23960) `{ MailIds (#12) }` | `MailDeleteResponse` (22089) | For each `id` in `MailIds`: remove via `Mailbox.remove`; if removed, append `id` to `SuccessIds` (#11). Reply `{ SuccessIds (#11) }` (`ErrorCode` #2 = `Success`) |

---

## 6. Tests

- **Activity page**: excluded ids are omitted; newest activity with data wins per type; double
  reward emits one per listed subtype; `NewLordGym` (34) is emitted with empty `Data`; every
  `OneTimePointRewardActivityData` has a non-null `Task` (R-37g); `ActivityRequest` refreshes
  daily tasks first.
- **Role rewards (R-38)**: claiming a sign-in / time-point / turntable reward whose id is in
  `RoleInfo` adds an unowned role (+ default weapon + `PbGetRoleListNotify`) and converts an
  owned role into `SpilloverItem * count`.
- **Scratch card & turntable**: all error codes in validation order (missing config, locked prior
  round, duplicate cell, insufficient ticket/coin); valid scratch deducts 1 ticket, grants cell
  item, persists bitmask; valid spin deducts `max(CostItemCount, 1)`, sets `IsFinishCurRound =
  award.IsSpecial`, advances round.
- **Boss Rush, Lord Gym, Fishing**: `BossRushClaimRewardRequest` defaults missing `RewardType` to
  `Level`; `LordGymBeginRequest` returns `ErrLordGymConfigNotFound` on unknown id;
  `FishingInfoResponse.FishingInfo` has non-zero ship/cabin/handbook config ids (R-115).
- **Mail**: login announces unannounced mails in `MailInfosNotify`; new `.mail` file dropped
  later triggers `MailAddNotify` on next heartbeat; `MailGetAttachmentRequest` persists claimed
  state before running `cmd:` lines so a failing command cannot be re-claimed (R-102); UTF-8
  Chinese subjects/bodies round-trip cleanly (R-55).
- **Robot check**: `activity` mode passes on a copy of the 3.8 save (R-79).
