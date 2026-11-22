# Systems, UI, cosmetics, motorcycle and commands

Specification for the world systems, UI panels, cosmetics, camera, motorcycle and
chat/dev-command subsystems:

- Core subsystems owned by this spec (§1)
- Login data pushes owned by these systems (§2)
- Adventure manual, Sola guide, liveness and weekly framework (§3)
- World map, custom marks, explore progress, infrastructure and range access (§4)
- Explore tools and echo projector summons (§5)
- Guides and illustrated codex (§6)
- Heartbeat hooks, short messages, battle pass, profile and music (§7)
- Private chat, friends, settings, access path and direct train (§8)
- Cosmetics / skins and camera / self-portrait (§9)
- Motorcycle progression, outlook, presets and ride-sharing (§10)
- Fixed and minimal UI replies, client storage and tutorials (§11)
- Chat, gift-mail and dev-channel (`cmd.txt`) commands (§12)

Related specs: `enter-world.md` (§3 login pushes, §5 loading requests, §6 no `JSPatchNotify`),
`save-format.md` (§5.4–§5.9 player files), `data-tables.md` (§5 tables read outside the main
loader), `entities.md`, `roles.md`, `formation.md`, `inventory.md`, `combat.md`,
`quests.md`, `flows.md`, `dungeons.md`, `shops.md`, `activities.md`.

All message, field and enum names below are the 3.8 client's protocol names.

---

## 1. Subsystems covered here

| Subsystem | Owns |
|---|---|
| Achievements | Achievement progress, stars, rewards and `achievements` save file; `AchievementInfoRequest` (22315), `AchievementFinishRequest` (27912), `AchievementReceiveRequest` (16062), `MulAchievementReceiveRequest` (28143), `AchievementUpdateRequest` (28975) |
| Client storage | Client key-value storage per `EClientStorageSystemIdType`, `client_storage` save file and login merge (R-113) |
| Area terminal | 区域终端 region groups, pins, `AreaTerminalInfoNotify` and `area_terminal` save file (R-50) |
| Map marks | Server-judged `MapMark` show conditions and `MapMarkInfoNotify` (R-51) |

3.8 achievement wire fields:
`AchievementInfoResponse` fields `FinishCount` (#2), `StarCount` (#10),
`AchievementGroupInfoList` (#12); `AchievementUpdateRequest` (28975) ->
`AchievementUpdateResponse` (22115) `{ ErrorCode, AchievementEntryList }`.

---

## 2. Login data pushes

`enter-world.md` §3 fixes the exact order of the 35 pushes after `EnterGameResponse` /
`ReconnectResponse`. The pushes owned by these systems (plus the cosmetic defaults that feed rows 4–7)
have the following contents:

| Row in `enter-world.md` §3 | Push | Content |
|---|---|---|
| 8 | `StorageInfoNotify` (17245) | Start with 6 default `ClientStorageInfo` entries, all using `SetData`: `RoleSkinRedDot` (12, empty), `FlySkinRedDot` (7, empty), `WeaponSkinRedDot` (6, empty), `CalabashSkinRedDot` (8, empty), `Ornament` (40, `cosmetic_info.viewed_ornaments`), `GetOrnament` (41, empty). Then merge saved records from client storage (R-113): any saved entry with the same `SystemId` replaces the default; any other saved `SystemId` is appended. |
| 14 | `FuncOpenNotify` (19093) | `Func` = one `Function { Id = FunctionId, Flag = 2 }` for every row of `FunctionCondition` (all functions open). |
| 15 | `AreaTerminalInfoNotify` | Built by the area terminal subsystem (R-50). |
| 16 | `AdviceSettingNotify` (22745) | `{ IsShow = false }`. |
| 25 | `LevelPlayInfoNotify` | Empty. |
| 26 | `PlayerVarNotify` | Empty. |
| 27 | `RoguelikeCurrencyNotify` | Empty. |
| 29 | `MailInfosNotify` | Mailbox entries built by the mailbox subsystem (`activities.md` §6, R-102). |
| 31 | `TrackMoonIllustratedNotify` (28408) | Empty. |
| 32 | `TrackMoonTokenCoinNotify` (19882) | `{ TokenCoin = 0 }`. |
| 33 | `SilenceNpcNotify` | Empty. |

### 2.1 Cosmetic defaults for login rows 4–7 and new accounts

When `player/<id>/cosmetic_info` (`save-format.md` §5.5) is empty (all six lists empty),
populate defaults from game data:

- `fly_skins`: every `FlySkinConfig.Id`.
- `role_skins`: every `RoleSkin.Id`.
- `weapon_skins`: every `WeaponSkin.Id` with `HideInSkinView == false`.
- `ornaments`: every `Ornament.Id` with `HideInUi == false`.
- `viewed_ornaments`: every `Ornament.Id` with `HideInUi == false`, plus every id in its
  `ItemAccess` list.
- `phantom_skins`: all ids added to `fly_skins`, `role_skins`, `weapon_skins`, `ornaments` and
  `Ornament.ItemAccess`, plus every `PhantomItem.ItemId` whose `ParentMonsterId != 0`.

On every login, ensure every `PhantomItem.ItemId` with `ParentMonsterId != 0` is present in
`cosmetic_info.phantom_skins` (appending missing ids and saving if changed).

Login rows 4–7 send:

- Row 4 `WeaponSkinAddNotify` (17173): `{ SkinIds = non-zero ids from cosmetic_info.phantom_skins, IsLogin = true }`.
- Row 5 `FlyEquipAddNotify` (21836): `{ FlySkins = non-zero ids from cosmetic_info.fly_skins }`.
- Row 6 `RoleFlyEquipNotify` (21114): `DataList` (`repeated FlyEquipData { SkinId, RoleIds }`) seeded with `{ SkinId, RoleIds = [] }` for every non-zero `cosmetic_info.fly_skins` entry, then for every owned role appending `role_id` to the entry matching its `soar_skin_id` (if non-zero) and to the entry matching its `paragliding_skin_id` (if non-zero), creating a new `FlyEquipData` entry if not already in the list.
- Row 7 `OrnamentInfoNotify` (15327): `OrnamentInfo { UnlockOrnamentIds = non-zero ornaments, OrnamentDressInfos = role ornament map grouped by RoleSkinId (§9.7), RedPointOrnamentIds = non-zero viewed_ornaments }`.

---

## 3. Adventure manual, Sola guide, liveness and weekly framework

### 3.1 `AdventureManualRequest` (17641) -> `AdventureManualResponse` (19686)

The client's Sola guide only displays entries listed in `DetectionUnlocks`; leaving
`DetectionUnlocks` empty hides 凝素领域, weekly bosses and 模拟领域.

Read the `Id` column once from three JSON tables outside the main loader (`data-tables.md` §5):
- `DungeonDetection.json` -> `DungeonDetectionIds`
- `MonsterDetection.json` -> `MonsterDetectionIds`
- `SilentAreaDetection.json` -> `SilentAreaDetectionIds`

Respond with:
- `Code = Success`
- `AdventureManualData = { NowChapter = 1, UnLockChapters = [1, 2, 3, 4, 5, 6, 7, 8, 9] }`
- `DetectionUnlocks = { DungeonDetectionIds, MonsterDetectionIds, SilentAreaDetectionIds }`

### 3.2 `AdventureManualDataRequest` (25198) -> `AdventureManualDataResponse` (24214)

Respond with `Code = Success` and `AdventureManualData = { NowChapter = 1, UnLockChapters = [1..9] }`.

### 3.3 `GetDetectionTargetRequest` (20959) -> `GetDetectionTargetResponse` (24853)

Request carries `{ DetectionId, Type }` (`DetectionType`, defaulting to `NormalMonster` if
absent).

- Only `DetectionType.NormalMonster` is populated; `Dungeon`, `SilentArea` and `Material`
  receive an empty `DetectionTargetDatas`.
- For `NormalMonster`, look up `DetectionId` in `MonsterDetection.json` (`Id`, `BlueprintType`,
  `BlueprintTypeList`, `EntityConfigId`):
  - If `BlueprintType` is non-empty or `BlueprintTypeList` is non-empty, scan `LevelEntityConfig`
    for rows where `BlueprintType` equals `BlueprintType` or any element of `BlueprintTypeList`
    **and** `InSleep == false`.
  - Otherwise, scan `LevelEntityConfig` for rows where `EntityId == EntityConfigId`.
  - Group matched `EntityId`s by `MapId` into `DetectionTargetDatas`
    (`DetectionTargetData { MapId, TargetIds }`), capping `TargetIds` at **64** entries per map.
- Respond `{ Code = Success, DetectionId, Type, DetectionTargetDatas }`.

### 3.4 `GetDetectionLabelInfoRequest` (22348) -> `GetDetectionLabelInfoResponse` (21286)

Respond with `UnlockLabelInfo`:
- `UnlockedGuideIds = [0, 1, 2, 3, 4, 5]`
- `UnlockedDetectionTextIds = [4, 5, 6, 7, 18, 19, 21, 22, 28, 29, 30, 61, 62, 63, 64]`

### 3.5 `LivenessRequest` (19066) -> `LivenessResponse` (26035)

Respond `{ LivenessInfo = { LivenessCount = 100 } }`.

### 3.6 `SlashAndTowerInfoRequest` (21662) -> `SlashAndTowerInfoResponse` (29704)

Respond `{ ErrorCode = Success }`.

### 3.7 `WeeklyFrameworkInfoRequest` (29511) -> `WeeklyFrameworkInfoResponse` (22499)

Let `now_ms` be current real wall-clock Unix time in milliseconds. Respond with:
- `FrameworkInfo = { ConfigId = 1, BeginTime = now_ms, EndTime = now_ms + 604800000, ScoreTasks = [1000, 2000, 3000, 4000, 5000, 6000], WorldLevel = 1 }`.
(Note: `ErrorCode` defaults to `Success`.)

---

## 4. World map, custom marks, explore progress, infrastructure and range

State is saved in `player/<id>/map_info` (`save-format.md` §5.7): `custom_marks`,
`tracked_mark_ids`, `next_custom_mark_id` (starts at `1_000_000`).

### 4.1 Custom mark wire fields vs `map_info` save compatibility

In the 3.8 protocol:
- `MarkPointRequestInfo`: `#1 PosX`, `#2 PosY`, `#3 PosZ`, `#4 ConfigId`, `#5 MarkType`,
  `#6 IsTrace (bool)`, `#7 MapId (int32)`, `#8 EntityConfigId (int32)`.
- `MarkPointInfo`: `#1 PosX`, `#2 PosY`, `#3 PosZ`, `#4 ConfigId`, `#5 MarkId`,
  `#6 MapId (int32)`, `#7 MarkType`, `#8 EntityConfigId (int32)`, `#9 IsDisable (bool)`,
  `#10 State (MarkState: MarkNormal=0, MarkDisable=1, MarkComplete=2)`.

**Save-file compatibility:** the ZON save format stores wire `#7` (`MapId`) in the ZON
field named `is_trace` and wire `#8` (`EntityConfigId`) in the ZON field named `map_id`, and
serializes `is_trace` back into `MarkPointInfo.#6` (`MapId`) and `map_id` back into
`MarkPointInfo.#8` (`EntityConfigId`). Keep that exact ZON-to-wire mapping when reading/writing
`player/<id>/map_info` so existing saves load their custom marks on the right map and entity:

| `MarkPointRequestInfo` wire field | `map_info` ZON field (`CustomMark`) | `MarkPointInfo` wire field |
|---|---|---|
| `#1..#3 PosX/Y/Z` | `x`, `y`, `z` | `#1..#3 PosX/Y/Z` |
| `#4 ConfigId` | `config_id` | `#4 ConfigId` |
| `#5 MarkType` | `mark_type` | `#7 MarkType` |
| `#7 MapId` | `is_trace` | `#6 MapId` |
| `#8 EntityConfigId` | `map_id` | `#8 EntityConfigId` |
| *(assigned `next_custom_mark_id++`)* | `id` | `#5 MarkId` |
| *(default `false`)* | `locked` | `#9 IsDisable` |
| *(default `0`)* | `state` | `#10 State` (`1 -> MarkDisable`, `2 -> MarkComplete`, else `MarkNormal`) |

### 4.2 Custom marks and tracking

1. **`MapTraceRequest` (24772) -> `MapTraceResponse` (24396)**
   Add `MarkId` to `tracked_mark_ids` (if not already present), save `map_info`, respond
   `{ ErrorCode = Success, MarkId }`.
2. **`MapCancelTraceRequest` (17205) -> `MapCancelTraceResponse` (26282)**
   Remove `MarkId` from `tracked_mark_ids` (if present), save `map_info`, respond
   `{ ErrorCode = Success, MarkId }`.
3. **`MapTraceInfoRequest` (20733) -> `MapTraceInfoResponse` (20456)**
   If `custom_marks` is non-empty, push `MapMarkInfoNotify { InfoList = all custom_marks }`
   before replying. Respond `{ ErrorCode = Success, MarkIdList = tracked_mark_ids }`.
4. **`MapMarkRequest` (16871) -> `MapMarkResponse` (18621)**
   If `MarkPointRequestInfo` is absent, respond `{ ErrorCode = RequestParamError }`.
   Otherwise create a `CustomMark` with `id = next_custom_mark_id++`, append to `custom_marks`,
   save `map_info`, push `AddMarkInfoNotify` (19623) `{ Info = mark }`, and respond
   `{ ErrorCode = Success, Info = mark }`.
5. **`MapRemoveMarkRequest` (16284) -> `MapRemoveMarkResponse` (29212)**
   Delete any `custom_marks` whose `id` is in `MarkIds`, also remove those ids from
   `tracked_mark_ids`, save `map_info`, push `MapMarkInfoNotify { InfoList = remaining custom_marks }`,
   and respond `{ ErrorCode = Success, MarkIds = request.MarkIds }`.

### 4.3 Map fog, blocks, marks and minimap

1. **`MapUnlockFieldInfoRequest` (15442) -> `MapUnlockFieldInfoResponse` (19902)**
   - Push `MultiMapInfoNotify` (20261) with `UnlockMultiMapId` = every `MultiMap.Id` and
     `UnlockBlockId` = every `MapBlockInfo.BlockId`.
   - Push `MapMarkInfoNotify` built by the map-marks subsystem using the player's current level
     (R-51).
   - Respond `{ ErrorCode = Success, FieldId = every MapFog.Fog }`.
2. **`LastBigSceneMiniMapInfoRequest` (28705) -> `LastBigSceneMiniMapInfoResponse` (20403)** (R-87)
   Determine open-world position `[x, y, z]`:
   - If the player has an active dungeon return record (`dungeons.md`), use its
     saved return position (`back_pos`).
   - Else use the player's current scene position (or `(0, 0, 0)` if none).
   Respond `{ Code = Success, PosX = x, PosY = y, PosZ = z, PosA = 0 }`.
3. **`AreaTerminalPinOperationRequest` (21848) -> `AreaTerminalPinOperationResponse` (27615)** (R-50)
   Update the area-terminal state to pin (`OpType == true`) or unpin (`OpType == false`)
   entry `Id`, save, and respond `{ Code = Success }`.

### 4.4 Explore progress and country scores

Helper `maxExploreLevels(country_filter)`: scan `ExploreReward`; for each row matching
`country_filter` (or all rows if null), compute `max(ExploreLevel)` per `Country`, returning
`repeated CountryExploreLevel { CountryId, ExploreLevel }`.

1. **`ExploreProgressRequest` (19630) -> `ExploreProgressResponse` (29853)**
   - Push `ExploreLevelNotify` (24649) `{ CountryExploreLevel = maxExploreLevels(null) }`.
   - Push `ExploreProgressRewardIdsNotify` (25441) `{ RewardIds = every ExploreProgressReward.Id }`.
   - Respond `{ AreaProgress = [ { AreaId = id, ExplorePercent = 100, ExploreProgress = [ { ExploreProgressId = row.Id,
     ExplorePercent = 100, CurCount = 1, TotalCount = 1, UnLock = true } for row in ExploreProgress where row.Area == id ] }
     for id in request.AreaIds ] }`. Never `ExploreScore`'s keys: those are percent steps (20…100), and the client
     logs a config error for each unknown id (R-165).
2. **`ExploreProgressRewardRequest` (29451) -> `ExploreProgressRewardResponse` (28094)**
   Echo `{ RewardIds = request.RewardIds }`.
3. **`CountryExploreScoreInfoRequest` (16184) -> `CountryExploreScoreInfoResponse` (24983)**
   - Scan `ExploreScore`: for each row whose `Area` exists in `Area` table with
     `Area.CountryId == request.CountryId`, collect all keys of its `Score` map as
     `ExploreProgressRewardTake { AreaId = row.Area, ExploreProgress = keys }` (skipping rows
     with empty maps) and sum all map values into `total_score`.
   - Push `ExploreLevelNotify` `{ CountryExploreLevel = maxExploreLevels(request.CountryId) }`.
   - Respond `{ CountryExploreScore = total_score, ExploreProgressRewardTakes }`.
4. **`MulExploreScoreRewardRequest` (20889) -> `MulExploreScoreRewardResponse` (27486)**
   Respond `{ ErrorCode = Success }`.

### 4.5 Infrastructure, Dark Coast, range access and track report

1. **`InfrInfoRequest` (21330) -> `InfrInfoResponse` (29855)**
   Build `Roads` = every `InfrRoadBuild` row with `DungeonId == 906` as
   `InfrOneRoad { RoadId = Id, Status = InfrStatusComplete (2), CompleteTime = 0, TotalGiftCount = 0, LastGiftTime = 0 }`.
   Respond `{ ErrorCode = Success, InfrInfo = { RoadInfo = { Roads } } }`.
2. **`InfrV2InfoRequest` (20516) -> `InfrV2InfoResponse` (26149)**
   Build `Trees` = every `InfrV2TreeBuild` row as
   `InfrV2OneTree { TreeId = Id, Status = InfrV2StatusComplete (2), CompleteTime = 0 }`.
   Respond `{ ErrorCode = Success, InfrInfo = { FireInfo = { FireExp = 0, FireLevel = 1, FireLevelReachTime = 0, FireStatus = InfrV2StatusComplete (2) }, TreeInfo = { Trees, ManualTraceTree = 0 } } }`.
3. **`DarkCoastInfoRequest` (20643) -> `DarkCoastInfoResponse` (20081)**
   Respond `{ ErrorCode = Success, LevelGains = 0, DragonPoolConf = { DragonPoolId = request.DragonPoolId, DropIds = [], DropItems = [] }, GuardIds = [], TreasureBoxDone = [] }`.
4. **`PlayerAccessRangeRequest` (20505) -> `PlayerAccessRangeResponse` (29671)**
   Respond `{ ErrorCode = Success, RangeEntityId = request.RangeEntityId, AccessRangeResult = { AccessEntityId = request.RangeEntityId, AccessRangeType = request.AccessRangeType, AccessRangeResults = {} } }`.
5. **`SimpleTrackReportRequest` (15365) -> `SimpleTrackReportResponse` (23444)**
   Respond `{ ErrorCode = Success, SimpleTrackReportMsgs = [] }`.

---

## 5. Explore tools and echo projector summons

`EnterAreaRequest` (22737) -> `EnterAreaResponse` (17162) is specified in `enter-world.md` §5
(echoes `{ Id = request.Id }`). Explore tools defaults for new accounts are in `enter-world.md`
§7 and `save-format.md` §5.4.

### 5.1 `VisionExploreSkillSetRequest` (16974) -> `VisionExploreSkillSetResponse` (18378)

1. Save `old_skill = scene.explore_tools_info.active_explore_skill`, then set
   `active_explore_skill = request.SkillId`.
2. For every entity in the current scene that has a `VisionSkillComponent`:
   - For each entry in its `vision_skills`, if `entry.SkillId == old_skill`, replace it with
     `request.SkillId`.
   - Synchronize buff remaining durations if needed, persist the entity if entity saves are active,
     and push `VisionSkillChangeNotify` (25196) `{ EntityId = entity.net_id, VisionSkillInfos }`.
3. Respond `{ SkillId = request.SkillId }` (`ErrCode = Success`).

### 5.2 `ExploreSkillRouletteSetRequest` (15609) -> `ExploreSkillRouletteSetResponse` (27182)

- `roulette_type = request.RouletteType` (default `Explore`).
- `selected = request.SkillRoulette`, or `request.SkillRoulettes[0]` if `SkillRoulette` is absent
  and `SkillRoulettes` is non-empty, else null.
- If `selected` is present, pad or truncate `selected.SkillIds` to **8** slots (filling missing
  slots with `0`) and store into `scene.explore_tools_info` according to `roulette_type`:
  - `Explore (0)`: `roulette`, `explore_extra_item_id`, `active_explore_skill`
  - `Function (1)`: `function_roulette`, `function_extra_item_id`, `active_function_skill`
  - `Motorcycle (3)`: `motorcycle_roulette`, `motorcycle_extra_item_id`, `active_motorcycle_skill`
  - `TrapDefense (2)`: ignored
  Then save the scene's `explore_tools_info`.
- Build the 4-element `RouletteInfo` array matching `enter-world.md` §4 row 2 (Explore, Function,
  empty `ExploreSkillRoulette {}`, Motorcycle — each padded/cut to 8 slots).
- Push `ExploreSkillRouletteUpdateNotify` (25728) `{ RouletteInfo }`.
- Respond `{ ErrCode = Success, RouletteType = roulette_type, SkillRoulette = selected, SkillRoulettes = RouletteInfo }`.

### 5.3 `Summon3Request` (29752) -> `Summon3Response` (25946)

Spawns an echo projector vision entity at the client-chosen net id and transform:

1. Validate `request.SummonInfo`:
   - Absent -> `RequestParamError`.
   - `SummonEntityId <= 0` -> `ErrSummonAddEntityFail`.
   - `SummonEntityId` already exists in the scene -> `ErrSummonEntityIdAlreadyExist`.
   - `SummonConfigId` (monster id) not unlocked as a projector monster ->
     `ErrPhantomInteractionNotUnlock`. A monster id is unlocked when `CalabashDevelopReward` has
     a row with `MonsterId == monster_id`, `IsShow == true`, `AllowVision == true`, and at least
     one base `PhantomItem` matches `monster_id`.
   - Look up the displayed `PhantomItem` for `SummonConfigId`: if `echo/calabash`
     `projector_skins` has a non-zero `skin_id` for `SummonConfigId` whose `PhantomItem` exists
     and has `ParentMonsterId == SummonConfigId`, use that skin item; otherwise use the base
     `PhantomItem`. If neither exists -> `ErrPhantomInteractionConfigNotFind`.
   - `summon_id = item.MeshId != 0 ? item.MeshId : item.SkillId`. If `0`, or `SummonCfg` has no
     row for `summon_id` with non-empty `BlueprintType`, or `TemplateConfig` has no matching
     blueprint -> `ErrSummonCfgNotFound`.
2. Spawn the entity with the requested `net_id = info.SummonEntityId`:
   - `Config`: `camp = 0`, `state = default`, `entity_type = vision`, `config_type = template`,
     `config_id = template.Id`.
   - `PlayerId = player_id`, `Position = info.Pos` (default `0,0,0`), `Rotation = info.Rot`
     (`Roll, Pitch, Yaw`, default `0,0,0`), `ActorVisible`.
   - `Summoner`: `player_id`, `summon_cfg_id = summon_cfg.Id`, `summon_skill_id = summon_id`,
     `summon_type = ESummonTypeConcomitantVision (1)`, `summoner_id = request.SummonerEntityId`.
   - Default `Attribute`, `LogicState`, `MonsterAi` (`combat_message_id = 1`), `Equip`,
     `CharacterAttach`, `VisionSkill`, passive GA skill entries for `summon_id` (R-122), and
     born buffs from `summon_cfg.BornBuffId`.
3. Push `EntityAddNotify` (20302) containing the built `EntityPb`.
4. Respond `{ ErrorCode = Success }`.

### 5.4 `RemoveSummonEntityRequest` (17165) -> `RemoveSummonEntityResponse` (28927)

For each `entity_id` in `request.RemoveEntityIds`:
- If the entity exists in the scene and has a `Summoner` component with
  `summoner_id == request.SummonerId`, remove it from the scene and append
  `EntityRemoveInfo { EntityId = entity_id }`.

If at least one entity was removed, push `EntityRemoveNotify` (17927)
`{ IsRemove = true, RemoveInfos }`. Respond `{ ErrCode = Success }`.

---

## 6. Guides and illustrated codex

State is saved in `player/<id>/guide_info` (`save-format.md` §5.5): `finished_groups` `[i32]`.
Group `0` always counts as finished.

### 6.1 `GuideInfoRequest` (15732) -> `GuideInfoResponse` (26611)

By default `autocomplete_guides = false`. Build `GuideGroupFinishList` as:
- First, each id in the default finished set
  `[10116, 10193, 10194, 10120, 10175, 10270, 10285]` that is not already in
  `guide_info.finished_groups`.
- Then all ids in `guide_info.finished_groups`.

(If a server option enables `autocomplete_guides`, return every `GuideGroup.Id` instead.)

### 6.2 `GuideTriggerRequest` (27818) -> `GuideTriggerResponse` (25835)

Respond `{}` (`ErrorCode = Success`).

### 6.3 `GuideFinishRequest` (22758) -> `GuideFinishResponse` (21373)

If `request.GroupId != 0` and not already in `guide_info.finished_groups`, append it and save
`player/<id>/guide_info`. Respond `{}` (`ErrorCode = Success`).

### 6.4 `IllustratedInfoRequest` (15484) -> `IllustratedInfoResponse` (28603)

For each `illustrated_type` in `request.TypeList`, emit an `IllustratedClass`:
- `Type = illustrated_type`
- If `illustrated_type == IllustratedType.VocalCorpse (1)`, populate `IllustratedEntryList` with
  one entry per `CalabashDevelopReward` row where `IsShow == true`:
  `{ Id = MonsterId, CreateTime = 1, Num = DevelopCondition.Length, IsRead = true }`.
- For all other `IllustratedType` values, `IllustratedEntryList` is empty.

Respond `{ ErrorCode = Success, IllustratedClassList }`.

---

## 7. Heartbeat hooks, short messages, battle pass, profile and music

### 7.1 `HeartbeatRequest` (1650) -> `HeartbeatResponse` (1651)

Respond `{}` immediately, then run the per-heartbeat hooks in this order:
1. Dev command file poll (`assets/scripts/dbg/cmd.txt`, §12.1).
2. If in a scene:
   - Quest engine tick (`quests.md` §5.1).
   - Pending quest transfer (`quests.md` §5.1).
   - Pending world spawn refresh (`spawning.md` §4, `quests.md` §5.2).
3. Mailbox new-file poll (`activities.md` §6, R-102).

As documented in `enter-world.md` §6, the 3.8 C# client ignores `JSPatchNotify`, so tidelab does
**not** send `JSPatchNotify` on heartbeat.

### 7.2 `ShortMessageInfoRequest` (19425) -> `ShortMessageInfoResponse` (17966)

飛讯 (short messages). **Must not return `ChatBgId = 0`**: the client short-message panel looks up
`ChatBg` id `0`, throws and hangs the UI.

Respond with fixed defaults (from `CommonParam`) and all owned cosmetic ids:
- `ErrCode = Success`
- `BubbleId = 80810001`, `BubbleIds = [80810001, 80810002, 80810003, 80810004, 80810005]`
- `ChatBgId = 80820001`, `ChatBgIds = [80820001, 80820002, 80820004, 80820005]`
- `ChatThemeId = 80891001`, `ChatThemeIds = [80891001, 80891002]`
- `ChatCharmId = 80892001`, `ChatCharmIds = [80892001, 80892002]`

### 7.3 `BattlePassRequest` (22649) -> `BattlePassResponse` (26557)

Respond `{ ErrorCode = Success, BattlePass = { InTimeRange = false } }`.

### 7.4 Profile requests (11 requests)

All persistent changes save `player/<id>/basic_info` (`save-format.md` §5.1).

1. **`PlayerHeadRequest` (18199) -> `PlayerHeadResponse` (22413)**
   Respond `{ PlayerHeadIds = every PlayerHeadRe.Id }`.
2. **`PlayerTitleRequest` (25420) -> `PlayerTitleResponse` (23404)**
   Respond `{ ErrorCode = Success, PlayerTitleInfo = [ { PlayerTitleId = Id, IsUnlock = true, ExtraInfo = 1 } for every PlayerTitle row ] }`.
3. **`RoleShowListUpdateRequest` (27889) -> `RoleShowListUpdateResponse` (23280)**
   Keep up to 3 distinct `RoleList` ids that exist in the player's owned roles (preserving order),
   store in `basic_info.role_show_list`, save, and respond `{ ErrorCode = Success }`.
4. **`ChangeCardRequest` (23459) -> `ChangeCardResponse` (29040)**
   If `CardId` exists in `BackgroundCard`, update `basic_info.cur_card_id` and save. Respond
   `{ ErrorCode = Success }`.
5. **`ReadCardRequest` (15318) -> `ReadCardResponse` (19779)**
   Respond `{ ErrorCode = Success }`.
6. **`ChangePlayerTitleRequest` (28211) -> `ChangePlayerTitleResponse` (23826)**
   If `PlayerTitleId == 0` or `PlayerTitleId` exists in `PlayerTitle`:
   - Set `basic_info.cur_player_title_id = PlayerTitleId` and save.
   - Push `DressPlayerTitleUpdateNotify` (19561)
     `{ PlayerTitleId, PlayerTitleExtraParam = (PlayerTitleId == 0 ? 0 : 1) }`.
   Respond `{ ErrorCode = Success }`.
7. **`ModifyNameRequest` (18421) -> `ModifyNameResponse` (16293)**
   - Compute `last_modify_name_time = max(0, now_seconds - 72 * 3600 + 5)` (reduces the client's
     72-hour rename cooldown to 5 seconds for testing).
   - Update `basic_info.attributes.name = request.Name` and
     `basic_info.last_modify_name_time = last_modify_name_time`; save.
   - Push `NameModifyNotify` (26559) `{ Name, LastModifyNameTime }`.
   - Respond `{ ErrorCode = Success, Name, LastModifyNameTime, ModifyName = "" }`.
8. **`ChangeHeadPhotoRequest` (26439) -> `ChangeHeadPhotoResponse` (28817)**
   If `HeadPhotoId` exists in `PlayerHeadRe`, update `basic_info.attributes.head_photo` and save.
   Respond `{ ErrorCode = Success, HeadPhotoId = basic_info.attributes.head_photo }`.
9. **`ModifySignatureRequest` (16561) -> `ModifySignatureResponse` (18895)**
   Set `basic_info.attributes.sign = request.Signature`; save. Respond
   `{ ErrorCode = Success, Signature = basic_info.attributes.sign }`. No length or word check: the
   client limits the input itself and, on Success, shows the text it sent (R-153).
10. **`BirthdayInitRequest` (21136) -> `BirthdayInitResponse` (29356)**
    If `request.Birthday > 0`:
    - Set `basic_info.birthday = request.Birthday`, `basic_info.display_birthday = true`; save.
    - Push `BirthDayInfoNotify` (20525)
      `{ BirthDayReset = true, RecentRewardTime = birthday / 10000 }` (integer truncation).
    Respond `{ ErrorCode = Success }`.
11. **`BirthdayShowSetRequest` (27980) -> `BirthdayShowSetResponse` (29672)**
    Set `basic_info.display_birthday = request.DisPlay`, save, and respond `{ ErrorCode = Success }`.

### 7.5 Music player (R-49, R-52)

State is saved in `player/<id>/music_info` (`save-format.md` §5.5): `favorite_music_ids` `[i32]`.

1. **`PhantomMusicInfoRequest` (16180) -> `PhantomMusicInfoResponse` (16334)**
   - Iterate `PhonographMusic` in table order. For each `album_id` in `row.Album`, find or append
     a `PhonographAlbumTimeInfo { AlbumId = album_id, BeginTime = 0, EndTime = 0 }` (0/0 marks
     the album as permanent, not time-limited, R-52) and append `row.Id` to its `MusicIds`.
   - `CurMusicId` = first row's `Id` (or `0` if table is empty).
   - `CollectMusicIds` = `music_info.favorite_music_ids`.
   - Respond `{ ErrorCode = Success, CurMusicId, AlbumTimeInfos, CollectMusicIds }`.
2. **`PhantomMusicUnlockRequest` (25144) -> `PhantomMusicUnlockResponse` (23502)**
   Every song is already unlocked; respond `{ ErrorCode = Success }` with no `AddMusicIds`. The
   client sends the phonograph items in its bag (`Items` are item ids, not songs) at every ride and
   shows "获得新曲目" for whatever `AddMusicIds` holds, so echoing `Items` back showed it at every
   ride (R-155).
3. **`PhantomMusicCollectUpdateRequest` (18730) -> `PhantomMusicCollectUpdateResponse` (18049)**
   Replace `music_info.favorite_music_ids` with `request.CollectMusicIds`, save `music_info`, and
   respond `{ ErrorCode = Success }`.

---

## 8. Private chat, friends, settings, access path and direct train

### 8.1 Built-in command bot (`PlayerId = 1337`)

A fixed virtual friend serves as the in-game command console:

- `PlayerId = 1337`
- `Name = "Tidelab"` (see `save-format.md` §5.1)
- `Level = 90`, `OriginWorldLevel = 8`, `CurWorldLevel = 8`
- `HeadId = 82001412`, `HeadFrameId = 0`, `Signature = "here to help!"`
- `IsOnline = true`, `IsCanLobbyOnline = false`, `MatchPsnUser = true`
- All other fields empty / `0` / `false`.

Starter greeting message: `"use \"help\" to get a list of commands."`

Whenever a command produces a reply string `text`, push:
`PrivateMessageNotify` (21956)
`{ ChatContent = { SenderUID = 1337, ChatContentType = Text (0), Content = text, OfflineMsg = false, UTCTime = 0 } }`.

### 8.2 Private chat (4 requests)

1. **`PrivateChatDataRequest` (17748) -> `PrivateChatDataResponse` (27414)**
   Push `PrivateChatHistoryNotify` (16299) with one `PrivateChatHistoryContentProto`:
   `{ TargetUID = 1337, Chats = [ { SenderUID = 1337, ChatContentType = Text, Content = starter_greeting, OfflineMsg = false, UTCTime = 0 } ], HistoryIsEnd = false, TotalNums = 1 }`.
   Then respond `{ LoadSucc = true }`.
2. **`PrivateChatHistoryRequest` (20338) -> `PrivateChatHistoryResponse` (24958)**
   If `request.TargetUID == 1337`, push `PrivateMessageNotify` with the starter greeting.
   Respond `{}`.
3. **`PrivateChatRequest` (18832) -> `PrivateChatResponse` (21677)**
   Dispatch `request.Content` to the command runner (§12); if it returns an error, enqueue/push
   the error message as a bot `PrivateMessageNotify`.
   Respond `{ ErrorCode = Success, TargetUID = request.TargetUID, FilterMsg = request.Content }`.
4. **`PrivateChatOperateRequest` (16521) -> `PrivateChatOperateResponse` (26788)**
   Respond `{ ErrorCode = Success }`.

### 8.3 Friends (2 requests)

1. **`FriendAllRequest` (29479) -> `FriendAllResponse` (20502)**
   Respond `{ ErrorCode = Success, FriendInfoList = [ { Info = bot_details, Remark = "" } ] }`.
2. **`PlayerBasicInfoGetRequest` (23383) -> `PlayerBasicInfoGetResponse` (19022)**
   - If `request.Id == 1337`, respond `{ ErrorCode = Success, Info = bot_details }`.
   - For any other id, respond `{ ErrorCode = RequestParamError }` (never leave the client RPC waiting).

### 8.4 Settings, access path and direct train (5 requests)

| Request | Response |
|---|---|
| `InputSettingRequest` (28228) | `InputSettingResponse` (20083) `{}` |
| `InputSettingUpdateRequest` (20344) | `InputSettingUpdateResponse` (20916) `{ ErrorCode = Success }` |
| `LanguageSettingUpdateRequest` (22740) | `LanguageSettingUpdateResponse` (23816) `{ ErrorCode = Success }` |
| `AccessPathConfigRequest` (28506) | `AccessPathConfigResponse` (22626) `{}` |
| `DirectTrainInfoRequest` (29272) | `DirectTrainInfoResponse` (19087) `{}` |

---

## 9. Cosmetics / skins and camera

Cosmetic ownership lives in `player/<id>/cosmetic_info` (§2.1); equipped ids live on each
`role/<roleId>` (`role_skin_id`, `weapon_skin_id`, `soar_skin_id`, `paragliding_skin_id`,
`calabash_skin_id`, `ornaments`; `save-format.md` §5.9).

### 9.1 Calabash skins (2 requests)

Calabash skin applies across all main-character (Rover) roles (`roles.md`).

1. **`LoadCalabashSkinInfoRequest` (20817) -> `LoadCalabashSkinInfoResponse` (18646)**
   - `SkinItemIds` = every `CalabashSkin.Id`.
   - `EquipSkinId` = the first non-zero `calabash_skin_id` among owned main-character roles (or `0`).
   - Respond `{ ErrorCode = Success, SkinItemIds, EquipSkinId }`.
2. **`CalabashSkinTakeOnRequest` (27489) -> `CalabashSkinTakeOnResponse` (28468)**
   - If `SkinId != 0` and `SkinId` is not in `CalabashSkin`, respond
     `{ ErrorCode = CalabashSkinUnLockErr }`.
   - Set `calabash_skin_id = SkinId` on every owned main-character role that differs and save
     those roles.
   - For every spawned scene entity whose `config_id` is a main-character role, update its
     calabash skin component and push `EntityCalabashSkinChangeNotify` (25793)
     `{ EntityId, CalabashSkinComponent = { CalabashSkinId = SkinId } }`.
   - Respond `{ ErrorCode = Success, SkinId }`.

### 9.2 Role skins (2 requests)

1. **`RoleSkinRequest` (26601) -> `RoleSkinResponse` (15433)**
   Respond `{ SkinIds = non-zero ids from cosmetic_info.role_skins }`.
2. **`RoleSkinChangeRequest` (26398) -> `RoleSkinChangeResponse` (27229)**
   - Look up owned role `request.RoleId`; missing -> `RequestParamError`.
   - Resolve target `skin_id`:
     - If `request.SkinId == 0`, use `RoleInfo(RoleId).SkinId`; missing -> `ErrRoleSkinConfig`.
     - Else verify `request.SkinId` is in `cosmetic_info.role_skins` and `RoleSkin(SkinId)` exists
       with `RoleSkin.RoleId == request.RoleId`; otherwise -> `ErrRoleSkinNotMatch`.
   - Capture `stale_ornament_ids` currently equipped for the role's previous `role_skin_id`.
   - Set `role.role_skin_id = skin_id`.
   - If `request.SignatureWeapon == true`, look up `RoleSkin(skin_id)`; if `SuitWeaponSkinId` is
     owned and weapon-skin-compatible with `RoleId` (§9.3), also set
     `role.weapon_skin_id = SuitWeaponSkinId`, push `EntityEquipSkinChangeNotify` (if spawned)
     and push `EquipTakeOnNotify` (25950) `{ DataList = [ { RoleID = RoleId, Pos = WeaponSkin (1), EquipIncID = weapon_skin_id } ] }`.
   - Respawn the role entity (`roles.md` / `formation.md` role reset for `[RoleId]`), save the
     role, broadcast formation update, and refresh the spawned role entity's ornaments and
     ornament buffs (§9.5).
   - Respond `{ ErrorCode = Success }`.

### 9.3 Weapon skins (3 requests)

Weapon skin compatibility: `skin_id == 0`, OR `WeaponSkin(skin_id)` exists with `IsShow == true`,
`HideInSkinView == false`, and `WeaponSkinType == RoleInfo(role_id).WeaponType`.

1. **`LoadEquipInfoRequest` (28824) -> `LoadEquipInfoResponse` (29155)**
   Build `EquipList` (`repeated RoleSkinEquipData { RoleID, SkinItemId }`) containing each owned
   role whose `weapon_skin_id != 0` and is compatible with that role (deduplicated).
   Respond `{ ErrorCode = Success, EquipList }`.
2. **`EquipSkinTakeOnRequest` (16426) -> `EquipSkinTakeOnResponse` (26442)**
   - Validate `request.Data` present, `Data.RoleID` owned, `Data.SkinItemId` owned (or `0`) and
     compatible with `Data.RoleID`; else -> `RequestParamError`.
   - Set `role.weapon_skin_id = Data.SkinItemId`, save role, broadcast formation update.
   - If the role entity is spawned in the scene, update its weapon skin component and push
     `EntityEquipSkinChangeNotify` (17965)
     `{ EntityId, RoleWeaponSkinComponent = { WeaponSkinId = Data.SkinItemId } }`.
   - Push `EquipTakeOnNotify` (25950)
     `{ DataList = [ { RoleID, Pos = WeaponSkin (1), EquipIncID = Data.SkinItemId } ] }`.
   - Respond `{ ErrorCode = Success, DataList = [ { RoleID, SkinItemId } ] }`.
3. **`EquipSkinUnLoadRequest` (25017) -> `EquipSkinUnLoadResponse` (15525)**
   - Validate `request.RoleID` owned; else -> `RequestParamError`.
   - Set `role.weapon_skin_id = 0`, save role, broadcast formation update.
   - If spawned, push `EntityEquipSkinChangeNotify` with `WeaponSkinId = 0`.
   - Push `WeaponSkinUnLoadNotify` (17893) `{ RoleId = request.RoleID, SkinId = 0 }`.
   - Respond `{ ErrorCode = Success }`.

### 9.4 Fly / glider skins (2 requests)

In `FlySkinConfig`, `SkinType == 0` is a soar (wing) skin (`role.soar_skin_id`); any other value
is a paraglider skin (`role.paragliding_skin_id`).

1. **`FlySkinWearRequest` (22343) -> `FlySkinWearResponse` (25179)**
   - Verify `SkinId` is owned (else `RequestParamError`), exists in `FlySkinConfig` (else
     `FlySkinItemNoConfig`), and `RoleId` is owned (else `RequestParamError`).
   - Update `soar_skin_id` or `paragliding_skin_id`, save role.
   - If the role entity is spawned, update its base-skin component and push
     `EntityFlyEquipChangeNotify` (16196)
     `{ SkinChanges = [ { EntityId, SkinInfos = [ { SkinId = skin.Id, SkinType = skin.SkinType } ] } ] }`.
   - Respond `{ ErrorCode = Success }`.
2. **`FlySkinWearAllRoleRequest` (17655) -> `FlySkinWearAllRoleResponse` (15632)**
   - Same ownership and `FlySkinConfig` checks (`RequestParamError` / `FlySkinItemNoConfig`).
   - Apply to every owned role, save each, and push `EntityFlyEquipChangeNotify` for each spawned
     role entity.
   - Respond `{ ErrorCode = Success, SkinChanges = [ { RoleId, SkinId } for every owned role ] }`.

### 9.5 Ornaments (1 request)

`ChangeOrnamentRequest` (16754) -> `ChangeOrnamentResponse` (23664):
- Find `role_id` owning `request.RoleSkinId` (from `RoleSkin.RoleId`, falling back to matching
  `RoleInfo.SkinId`); missing -> `RequestParamError`.
- Verify `OrnamentId != 0`, `OrnamentId` is in `cosmetic_info.ornaments`, and
  `Ornament(OrnamentId).RoleSkinIds` contains `RoleSkinId`; else -> `ErrOrnamentConfig`.
- Verify `role_id` is owned; else -> `RequestParamError`.
- Capture `stale_ornament_ids` equipped on `(role, RoleSkinId)`.
- If `IsDress == true`: if not already dressed, remove any currently dressed ornament on the same
  `RoleSkinId` that shares a non-zero `Ornament.OrGroupId`, then add `(RoleSkinId, OrnamentId)`.
- If `IsDress == false`: remove `(RoleSkinId, OrnamentId)` if present.
- If the role's ornaments changed:
  - Save the role and broadcast formation update.
  - Refresh the spawned role entity (if present): replace its `OrnamentComponent` ids with all
    ornaments dressed for `role.role_skin_id`; compute buffs from `Ornament.OrnamentBuff` +
    `Ornament.OrnamentUiBuff` (deduplicated, non-zero); remove stale ornament buffs
    (`RemoveGameplayEffectNotify`), apply new ornament buffs (`ApplyGameplayEffectNotify` +
    buff timers + immediate periodic tick if applicable), replace `born_buff_ids` on the entity's
    `FightBuffComponent`; push `EntityDressOrnamentChangeNotify` (24410)
    `{ EntityId, OrnamentComponentPb = { OrnamentIds } }`, and push `CombatReceivePackNotify`
    (29973) if any buff notifications were built.
- Respond `{ ErrorCode = Success }`, then push `OrnamentDressInfoUpdateNotify` (29379)
  `{ OrnamentDressInfos }` (every role's non-zero ornaments grouped by `RoleSkinId`).

### 9.6 Camera and self-portrait (3 requests)

Camera settings are saved in `player/<id>/settings/camera` (`save-format.md` §5.5), mapping 1:1
onto `CameraParamInfo` (`CameraMode`, `PitchValue`, `YawValue`, `RollValue`, `PosX`, `PosY`,
`PosZ`, `Fov`, `FocusDistance`, `BlurRadius`, `CameraTiltAngle`, `Weather`, `Atmosphere`,
`WindDirection`).

1. **`CameraSettingQueryRequest` (26920) -> `CameraSettingQueryResponse` (25637)**
   Respond `{ ErrorCode = Success, CameraSetting = { CameraParam = saved_camera_param } }`.
2. **`CameraSettingModifyRequest` (28953) -> `CameraSettingModifyResponse` (26725)**
   If `CameraSetting.CameraParam` is present, overwrite saved settings and write
   `settings/camera`. Respond `{ ErrorCode = Success }`.
3. **`SelfPortraitStateRequest` (17018) -> `SelfPortraitStateResponse` (28546)**
   Find the current formation's active role entity id (`cur_entity_id`), then push
   `SwitchRoleNotify` (26835)
   `{ PlayerId = scene.player_id, DownEntityId = cur_entity_id, UpEntityId = cur_entity_id, Transform = { Pos = player_location, Rot = player_rotation } }`,
   and respond `{ ErrCode = Success }`.

---

## 10. Motorcycle progression, outlook, presets and ride-sharing

State is saved in `player/<id>/motor_info` (`save-format.md` §5.8).

### 10.1 Default `motor_info` initialization

When `motor_info` is empty (all of `tech_nodes`, `task_nodes`, `owned_skins`, `owned_stickers`,
`owned_decorations`, `owned_frames` empty), initialize from game data:
- `level` and `exp`: taken from the `MotorLvl` row with the highest `Level`.
- `tree_in_use`: first `MotorTechTree.Id` (or `0`).
- `tech_nodes`: every `MotorTech` row with `level = current = min(max(InitLevel, 0), TechLv.Length)`,
  `unlocked = true`, `target = TechLv.Length`.
- `task_nodes`: every `MotorTask` row with `type = 1` (`Single`), `current = 1`, `target = 1`,
  `rewarded = max(RewardTpCount, 1)`, `wait_reward = 0`, `max_reward = max(RewardTpCount, 1)`.
- `owned_skins`: every non-zero `MotorSkin.Id`; `equipped_skin` = last row with `DefaultFlag == true`
  (fallback: first owned skin).
- `owned_stickers`: unique non-zero sticker ids collected in order from `MotorLoadProject.Sticker`,
  `MotorGeneralPreview.Sticker`, `MotorDecalIp.IpStickerList`, `MotorLinkageIp.IpStickerList`.
- `owned_decorations`: unique non-zero `MotorDecorations.Id`.
- `owned_frames`: unique non-zero `MotorFrame.Id`; `equipped_frame` = last row with
  `DefaultFlag == true` (fallback: first owned frame).
- `equipped_stickers`: zero-filled array of length `MotorStickerPart` row count.
- `equipped_decorations`: zero-filled array of length `MotorDecorationsPart` row count.

### 10.2 Motorcycle entity lookup and forced buffs

The player's motorcycle entity in the scene is found by matching the player scene entity's
`FollowerComponent` entry of type `EPlayerFollowerMotor (4)` (and verifying its `config_id`
matches the motorcycle template id from `TemplateConfig` / `MotorConfig`), falling back to any
scene entity with that `config_id`.

Born buffs and tags applied on motorcycle creation (and reconciled on tech changes):
- Forced born buff ids: `[3000000801, 3000020024]` (if present in `Buff` table; keeps Charged
  Leap and Vectored Thrust active).
- Forced gameplay tag id: `-849916103` (count `1`).

### 10.3 Info, creation, tech and rewards (7 requests)

1. **`MotorInfoRequest` (17813) -> `MotorInfoResponse` (22936)**
   Group `tech_nodes` by `tree_type` into `UnlockedTree` (`MotorTechOneTreePb`) and `task_nodes`
   by `tree_type` into `TaskTrees` (`MotorTaskTreePb`, `TpRewarded = 0`, `Type`: `1=Single`,
   `2=Limited`, `3=Cycle`, else `Unknown`). Respond `{ ErrorCode = Success, Motor }`.
2. **`MotorOutlookInfoRequest` (16618) -> `MotorOutlookInfoResponse` (18366)**
   - Push `MotorOutlookRegionInfoNotify` (17258) `{ MotorOutlookRegion = { MotorSticker, MotorDecoration, MotorFrame } }`
     where each list contains `{ Id, OpenTime = 0 }` for every owned sticker / decoration / frame.
   - Respond `{ ErrorCode = Success, MotorOutlook = { MotorOutlookOwned, MotorOutlookEquipped, MotorOutlookPreset } }`.
3. **`MotorCreateRequest` (29157) -> `MotorCreateResponse` (16761)**
   Respond `{ ErrorCode = Success }`.
4. **`MotorTechLevelUpRequest` (27727) -> `MotorTechLevelUpResponse` (15127)**
   - Find `tech_nodes` entry with `id == request.TechId`; missing -> `MotorTechCfgNotFound`.
   - If `level < target`, increment `level`, set `current = level`, `unlocked = true`, save
     `motor_info`.
   - Refresh the motorcycle entity's born buffs if spawned.
   - Build `tree = MotorTechOneTreePb` for `node.tree_type`.
   - Push `MotorLockedTechUpdateNotify` (23811) `{ TreeId = node.tree_type, Tech = tree.Tech }`.
   - Respond `{ ErrorCode = Success, Tree = tree }`.
5. **`MotorTechTreeSwitchRequest` (25166) -> `MotorTechTreeSwitchResponse` (17554)**
   Set `motor_info.tree_in_use = request.TreeId`, save, refresh motorcycle entity born buffs if
   spawned, and respond `{ ErrorCode = Success, TreeInUse }`.
6. **`MotorLevelOneKeyRewardRequest` (15900) -> `MotorLevelOneKeyRewardResponse` (17604)**
   Set `motor_info.rewarded_max_level = motor_info.level`, save, and respond
   `{ ErrorCode = Success, MotorRewardedLvMax }`.
7. **`MotorTaskOneKeyRewardRequest` (27567) -> `MotorTaskOneKeyRewardResponse` (26986)**
   For each `task_id` in `request.TaskIds`, find the matching `task_nodes` entry; if
   `wait_reward > 0`, add `wait_reward` to `rewarded`, set `wait_reward = 0`, and remember its
   `tree_type`. If at least one task matched, save `motor_info` and push `MotorTaskUpdateNotify`
   (22342) with all tasks of the last matched `tree_type`. Respond `{ ErrorCode = Success }`.

### 10.4 Skins, outlook and presets (5 requests)

Outlook normalization rules:
- **Frame**: `frame = (requested == 0 ? previous : requested)`. If `frame != 0` and not in
  `MotorFrame`, fail with `MotorFrameCfgNotFound`.
- **Stickers** (slot count = `MotorStickerPart` row count): initialize slots from `previous` (or
  zeros when applying a full skin). For each `(i, id)` in input: if `id == 0`, clear slot `i` (if
  in bounds); else look up `MotorSticker(id)` (missing -> `MotorStickerCfgNotFound`), check
  `PartId` in `1..slot_count` (out of range -> `MotorStickerPartIdError`), and set
  `slots[PartId - 1] = id`.
- **Decorations**: identical using `MotorDecorationsPart`, `MotorDecorations`
  (`MotorDecorationsCfgNotFound`, `MotorDecorationsPartIdError`).

Applying outlook to the spawned motorcycle entity (after saving `motor_info`):
1. Always push `MotorOutlookEquippedChangeNotify` (15683) `{ MotorOutlookEquipped }`.
2. Update the motorcycle entity's `MotorOutlookComponent`:
   - If `equipped_frame` changed, refresh the entity on the client by pushing
     `EntityRemoveNotify { IsRemove = true }` (including any custom concomitant entities) followed
     by `EntityAddNotify { IsAdd = false }` for the motorcycle (and its concomitants).
   - If `equipped_frame` did not change, push `EntityMotorOutlookChangeNotify` (29444)
     `{ EntityId = motor.net_id, MotorOutlookEquipped }`.

Requests:
1. **`MotorUseSkinRequest` (19760) -> `MotorUseSkinResponse` (22488)**
   Look up `MotorSkin(request.SkinId)` (missing -> `MotorOutlookNotOwned`). Resolve frame from
   `skin.BindFrame` (falling back to current frame), normalize stickers from `skin.BindSticker`
   (starting from zeros) and decorations from `skin.BindDecorations` (starting from zeros). Update
   `equipped_skin/frame/stickers/decorations`, save, apply to entity as above, respond
   `{ ErrorCode = Success }`.
2. **`MotorChangeOutlookRequest` (27172) -> `MotorChangeOutlookResponse` (26904)**
   Resolve frame from `request.FrameEquipped`, normalize stickers starting from current
   `equipped_stickers`, normalize decorations starting from current `equipped_decorations`. Update
   `motor_info`, save, apply to entity as above, respond `{ ErrorCode = Success }`.
3. **`MotorOutlookCreatePresetRequest` (19553) -> `MotorOutlookCreatePresetResponse` (22153)**
   If `request.Preset` is absent -> `MotorOutlookPresetInputInvalid`. Create preset with
   `id = next_preset_id++`, `name = request.Name`, `skin = (Preset.SkinEquipped == 0 ? equipped_skin : Preset.SkinEquipped)`,
   and copies of `StickerEquipped`, `DecorationsEquipped`, `FrameEquipped`. Append to `presets`,
   save, respond `{ ErrorCode = Success, MotorOutlookPreset = all presets }`.
4. **`MotorOutlookEditPresetRequest` (26832) -> `MotorOutlookEditPresetResponse` (23910)**
   If `PresetPlan` or `PresetPlan.Preset` is absent -> `MotorOutlookPresetInputInvalid`. Find
   preset matching `PresetPlan.Id` (missing -> `MotorOutlookPresetNotExist`), overwrite its name
   and outlook fields (same `SkinEquipped == 0` fallback), save, respond
   `{ ErrorCode = Success, MotorOutlookPreset = all presets }`.
5. **`MotorOutlookDeletePresetRequest` (27629) -> `MotorOutlookDeletePresetResponse` (16772)**
   Find preset matching `request.Id` (missing -> `MotorOutlookPresetNotExist`), remove it, save,
   respond `{ ErrorCode = Success, MotorOutlookPreset = remaining presets }`.

### 10.5 Ride-sharing (3 requests)

Passenger seat index is fixed at `1`.

1. **`VehicleEnterSharingRequest` (25279) -> `VehicleEnterSharingResponse` (25137)**
   Push `VehicleShareNotify` (17830)
   `{ PlayerId = player_id, RideMode = request.RideMode, IsEnter = request.IsEnter, Reason = ClientRequest (0) }`,
   then respond `{ ErrorCode = Success }`.
2. **`VehicleChangePassengerRequest` (22200) -> `VehicleChangePassengerResponse` (21531)**
   - Require `request.RoleId > 0` and owned -> else `RequestParamError`.
   - Find the motorcycle entity and player scene entity -> missing: `InternalError`.
   - Ensure the motorcycle companion entity exists (referenced by the motorcycle's `FollowEntity`
     component): if not yet spawned, create it with `SummonCfg(24000030)`, `TemplateConfig(36700001)`,
     `BaseProperty(90000004)`, `entity_type = monster`, `config_type = template`,
     `Summoner = { summon_cfg_id = 24000030, summon_type = ESummonTypeConcomitantMotorcycle (5), summoner_id = motor.net_id }`,
     `FollowShooter = { player_entity_id = player_scene_entity.net_id, summon_config_id = 24000030 }`,
     and `SummonCfg.BornBuffId` born buffs; link it on the motorcycle's `FollowEntity` component
     and push `EntityAddNotify`.
   - If an existing NPC passenger entity is spawned, push `VehicleUpdateEntityNotify` (16415)
     `{ EntityId = old.net_id, VehicleId = motor.net_id, SeatId = 1, IsGetOn = false, ExitType = ExitVehicleTypeSeatStandUp (3) }`
     and remove it (`EntityRemoveNotify { IsRemove = true }`, never removing a formation entity).
   - Resolve the passenger `TemplateConfig` for `request.RoleId`: scan `TemplateConfig` for the
     **last** entry whose `BlueprintType` has `BlueprintConfig.EntityLogic == Npc`, contains
     `"Passenger"`, and matches `RoleId` either by suffix `"_<RoleId>"`, substring `"_<RoleId>_"`,
     or case-insensitive ASCII containment of the path segment immediately before `"R2T1..."` in
     `RoleInfo(RoleId).UiScenePerformanceABP` (length ≥ 3). None found -> `RequestParamError`.
   - Spawn the passenger NPC entity (`entity_type = npc`, `config_type = template`,
     `config_id = template.Id`, `camp = BaseInfo.Camp ?? 0`, properties from
     `BaseInfo.EntityPropertyId ?? 2`, `NpcDriveVehicle = { vehicle_entity_id = motor.net_id, seat = 1 }`)
     at the player's current position and rotation, and push `EntityAddNotify`.
   - Push `VehiclePassengerUpdateNotify` (20875)
     `{ PlayerId = player_id, RoleId = request.RoleId, SeatId = 1, EntityId = passenger.net_id }`.
   - Push `VehicleUpdateEntityNotify` (16415)
     `{ EntityId = passenger.net_id, VehicleId = motor.net_id, SeatId = 1, IsGetOn = true, ExitType = ExitVehicleTypeSeatStandUp (3) }`.
   - Respond `{ ErrorCode = Success }`.
3. **`VehicleRemovePassengerRequest` (26143) -> `VehicleRemovePassengerResponse` (20287)**
   If an NPC passenger entity is spawned:
   - If the motorcycle entity exists, push `VehicleUpdateEntityNotify` with `IsGetOn = false`,
     `SeatId = 1`, `ExitType = ExitVehicleTypeSeatStandUp (3)`.
   - Push `VehiclePassengerUpdateNotify` `{ PlayerId = player_id, RoleId = 0, SeatId = 1, EntityId = passenger.net_id }`.
   - Remove the passenger entity and push `EntityRemoveNotify { IsRemove = true }`.
   Respond `{ ErrorCode = Success }`.

---

## 11. Fixed / minimal UI replies, client storage and tutorials

### 11.1 `StorageInfoUpdateRequest` (22023) -> `StorageInfoUpdateResponse` (24640) (R-113)

Pass `request.Infos` to the client storage store so per-system UI flags (e.g. newcomer
activity splash seen marks) persist across logins and are merged back into `StorageInfoNotify`
(§2). Respond `{ ErrorCode = Success }`.

### 11.2 Tutorials (3 requests — non-empty replies required)

1. **`TutorialInfoRequest` (15006) -> `TutorialInfoResponse` (22904)**: respond `{}`.
2. **`TutorialUnlockRequest` (21852) -> `TutorialUnlockResponse` (26280)**:
   The client dereferences `UnLockInfo` unconditionally on `Success`; omitting it throws on the
   client and stalls the tutorial popup.
   Respond `{ ErrorCode = Success, UnLockInfo = { Id = request.Id, CreateTime = max(0, now_seconds) } }`.
3. **`TutorialReceiveRequest` (27270) -> `TutorialReceiveResponse` (18170)**:
   The client reads the first entry of `ItemMap` on `Success`; an empty map looks up item `NaN`,
   breaks the tutorial popup and leaves a paused HUD flow hanging. Tutorial drop packages are
   empty in the client data, so grant **20** of item **3** (星声) via normal inventory grant
   (`inventory.md`) and respond `{ ErrorCode = Success, ItemMap = { 3: 20 } }` (or
   `{ ErrorCode = ErrItemNotEnough }` if granting fails).

### 11.3 `ButtonIsEnableRequest` (18232) -> `ButtonIsEnableResponse` (25924)

Respond `{ ErrorCode = Success, Enable = true }`.

### 11.4 Fixed / empty replies

All other requests from `enter-world.md` §5 and UI sub-panels not given stateful logic above:

| Request | Response |
|---|---|
| `DollSmallMapInfoRequest` (20965) | `{ ErrorCode = Success }` |
| `MainPhantomRecommendRequest` (15154), `PhantomAttrRecommendRequest` (18178), `PhantomFetterRecommendRequest` (29643) | `{ ErrorCode = Success }` |
| `PhantomRefiningRequest` (27747), `PhantomBatchRefiningRequest` (27203) | `{ ErrorCode = Success }` |
| `PhantomBatchDirectRefiningRequest` (29456) | `{ ErrorCode = Success, DirectRefineWeekTimes = 0 }` |
| `PhBaPlanUsePlanRequest` (27699) | `{ Code = Success, UsePlan = { SuitPlanList = [] }, TowPlanSame = true }` |
| `PhBaPlanFindPlanRequest` (29810) | `{ Code = Success, Plan = { SuitPlanList = [] }, TowPlanSame = true }` |
| `PhBaPlanSaveUsePlanRequest` (15491), `PhBaPlanSetPlanStatusRequest` (24251) | `{ Code = Success, TowPlanSame = true }` |
| `PhBaPlanUpdatePlanRequest` (23537), `PhBaPlanSetFiveStarSwitchRequest` (17981) | `{ Code = Success }` |
| `PhBaPlanBatchOperRequest` (26395), `PhantomBatchOperRequest` (15779) | `{ ErrCode = Success }` |
| `PhantomSettingRequest` (24025) | `{}` |
| `PhantomSettingUpdateRequest` (27251), `PhantomSettingBatchUpdateRequest` (21039) | `{ ErrorCode = Success }` |
| `ExchangeRewardInfoRequest` (25606), `ItemExchangeInfoRequest` (16256), `ValidTimeItemRequest` (17241), `RoleIllustratedInfoRequest` (23991), `InfluenceInfoRequest` (29194), `PhotoMemoryRequest` (25225) | `{}` |
| `MonthCardRequest` (22235), `PayInfoRequest` (23421), `SheriffZoneInfoRequest` (20973), `HonamiStoryActivityInfoRequest` (20790), `GivebackInfoRequest` (15189), `AdviceRequest` (23265), `EnergySyncRequest` (17150) | `{ ErrorCode = Success }` (note: `EnergySyncResponse` field is `ErrCode = Success`) |
| `ClientPullResourcePackageRequest` (28300) | `{ ErrorId = Success }` |
| `PlayerGameTokenRequest` (18839), `PsnSettingRequest` (29119), `PlayerPsnBlockListRequest` (16257), `XboxSettingRequest` (17199), `PlayerXboxBlockListRequest` (17705), `WeatherSwitchInfoRequest` (27924) | `{}` (kept from R-143f) |

(`LevelPlayVarRequest`, `LevelPlayStateRequest`, `ActionFinishRequest`, `OpenSystemBoardResultRequest`
are covered in `quests.md` §3.4; `FormationAttrRequest` is in `formation.md` §5.5;
`CombatMessagePrefixInfoRequest` / `CombatMessagePostInfoRequest` are empty `{}` replies.)

---

## 12. Chat, gift-mail and dev-channel commands

Commands are triggered from three places:
1. **In-game private chat** (`PrivateChatRequest` to bot `1337`, §8.2).
2. **Gift mails** (mailbox `cmd:` field when claiming a mail, `activities.md` §6, R-102).
3. **Dev command file** (`assets/scripts/dbg/cmd.txt`, polled on `HeartbeatRequest`, §7.1): if the
   file exists and contains non-whitespace text, clear it to `""`, split on `\r`/`\n`, trim
   spaces/tabs, skip empty lines, and dispatch each line.

### 12.1 Dispatcher syntax

- Input may contain multiple commands separated by `;`.
- Each command is trimmed of ASCII spaces and tokenized on spaces; the first token matches a
  command's name or alias.
- Boolean arguments accept `true`/`1` and `false`/`0`.
- Wrong argument counts or invalid argument syntax return an error string pushed via the command
  bot (`PrivateMessageNotify`).

### 12.2 Command reference

Commands required for gift mails (`activities.md` §6) and R-143h testing / `cmd.txt` workflows:

| Command (alias) | Usage | Behaviour |
|---|---|---|
| `help` | `help [command]` | Without args: list all commands and aliases. With a command name/alias: show its description and usage. |
| `give_item` (`item`) | `item <item_id> [count=1]` | Grant positive `count` of normal item `item_id` (must exist in `ItemInfo`, R-116), save inventory, and push `NormalItemAddNotify` (if count was `0`) or `NormalItemUpdateNotify`. |
| `give_role` (`role`) | `role <role_id>` | Grant role `role_id` via the reward-role path (`activities.md` §2.3, R-38): new role + starting weapon, or if already owned, add `RoleInfo.SpilloverItem` to inventory and save. |
| `open_shop` (`shop`) | `shop [shop_id=1]` | Push all shops (`shops.md` §1) and an `ActionNotify` (`NormalInteract`) for the target NPC/craft entity (`1`/`107` -> `109002511`, `2`/`108` -> `109002505`, `3`/`101` -> `109002503`, `4`/`104` -> `109002500`, `5`/`103` -> `109002508`, `6` Forging -> `109700015`, `7` Synthesis -> `109700016`, default `109002503`). |
| `weapon_item` (`weapon`) | `weapon <current\|weapon_id> [count=4\|max\|clear]` | Grant weapon copies (`count`, default `4`), clear unequipped copies (`clear`), or grant one finished copy at top `WeaponBreach` level/breach and `ResonLevelLimit` (`max`, R-102). Persists and pushes `WeaponItemAddNotify`. |
| `resonance_item` (`waveband`) | `waveband <current\|role_id> [count=6\|clear]` | Grant `count` (default `6`) of the role's resonance chain waveband item (or clear with `clear`), persist inventory, and push `NormalItemAddNotify`/`NormalItemUpdateNotify` (`NoTips = true`). |
| `quest_cmd` (`quest`) | `quest` / `quest next` / `quest start <quest_id>` | Describe running quests and active steps, complete active steps (`next`), or accept/start `quest_id` (`quests.md`). |
| `dungeon_cmd` (`dungeon`) | `dungeon enter <id>` / `dungeon leave` / `dungeon killall` | Enter dungeon `id` (with `applyTransfer`), plan dungeon exit (`leave`), or enqueue monster-kill events for all alive prefab-group and active-spawner monsters in the scene (`killall`, `dungeons.md`). |
| `achievement_cmd` (`achieve`) | `achieve <achievement_id>` | Complete `achievement_id` via the achievement subsystem. |
| `find` | `find [chest\|door] [n=1]` | Teleport next to the `n`-th nearest unopened chest or door/switch on the current map. |
| `goto` | `goto <x> <y> <z>` | Teleport the player to `(x, y, z)` (centimetres) on the current map (`movement.md` §4). |
| `flow` | `flow <flow_list_name> <flow_id> <state_id>` | Start the specified flow and switch to its map if needed (`flows.md`). |
| `flowtp` | `flowtp <action_id> <x> <y> <z>` | Teleport with a `FlowAction` teleport context to unstick a flow waiting on `SetPlayerPos` (`flows.md` §3). |
| `flowend` | `flowend <inc_id> [flow_list_name flow_id state_id]` | Force-end a hung client flow by pushing `FlowServerSkipNotify` and `FlowEndNotify` (`flows.md`). |
| `map` (`m`) | `map <map_id>` | Switch scene to `map_id` (`movement.md` §4.4). |
| `battle` (`bt`) | `battle <true\|false>` | Force the player's combat battle state on or off and push `CombatReceivePackNotify`. |
| `max_attribute` (`max`), `empty_attribute` (`empty`), `set_attribute` (`set`) | `max\|empty\|set <role_id\|team> <EAttributeType> [value]` | Set the named attribute on the matching spawned role entity (or all current formation members for `team`) to its related max attribute (fallback `10000000`), `0`, or `value`; push `CombatReceivePackNotify` (`AttributeChangedNotify`) and wake entity FSM. |
| `echo` (`ec`) | `echo <echo_id> <sonata_id> <main:a-m> [sub tier]...` | Create a max-level custom echo with specified `FetterGroup`, main stat (`a`–`m`: CR, CD, ATK%, HP%, DEF%, Heal, Glacio, Fusion, Electro, Aero, Spectro, Havoc, Energy) and up to 5 `(sub:a-m, tier)` sub-stats; save and push `PhantomItemAddNotify`. |
| `spawn` (`s`), `spawn_map` (`sm`) | `spawn <entity_id> [freeze] [tune_break]` / `spawn_map <entity_id> <source_map_id> [freeze] [tune_break]` | Debug-spawn a `LevelEntityConfig` entity near the player (using R-105 capsule-centre Z lift `feet.z + BlueprintConfig.HalfHeight` for characters), optionally applying frozen (`10010001`) or tune-broken (`3161..3164, 9000000000`) buffs. |
| `reset_formation` (`rf`) | `reset_formation` | Respawn all entities of the current formation (`formation.md`). |
| `photo` (`cam`) | `photo [enabled=true]` | Toggle 2.7 photo-event mode: when enabled, push `PhotoFightLevelInfoNotify` (18787) (`LevelId = 100016, InstId = 8, GroupId = 1, IsUnlocked = true`, current team roles), `InstPlayDataNotify` (18768) (`Id = 314800019`), `BehaviorTreeInfoNotify` (`TreeIncId = 69, BtType = BtTypeInst`), `UpdateNodeStatusNotify` (`NodeId = 13, Activate`), `UpdateChildQuestNodeStatusNotify` (`Enter`, `EnterAction`), `ActionNotify` (27631) (`ChildQuestNodeEnterAction`), and `UpdateChildQuestNodeStatusNotify` (`Progress`); when disabled, push `BehaviorTreeDeleteNotify { TreeIncIds = [69] }`. |

**Deliberate omission:** `JSPatchNotify` is a no-op on the 3.8 C# client (`enter-world.md` §6),
so tidelab does not implement a `js_patches` command family.

---

## 13. Tests

- **Login pushes**: `StorageInfoNotify` merges saved `client_storage` entries over the 6 defaults
  (preserving `Ornament` viewed list when untouched, replacing matching `SystemId` when saved);
  `FuncOpenNotify` emits `Flag = 2` for every `FunctionCondition` row; `cosmetic_info` default
  seeding populates all 6 lists and filters `HideInSkinView` / `HideInUi`.
- **Adventure & Sola guide**: `AdventureManualResponse` populates `DetectionUnlocks` from all
  three JSON tables; `GetDetectionTargetResponse` resolves `NormalMonster` by both
  `BlueprintType`/`BlueprintTypeList` (skipping `InSleep`) and `EntityConfigId`, caps at 64 per
  map, and returns empty for `Dungeon`/`SilentArea`/`Material`.
- **Custom marks compatibility**: `MapMarkRequest` -> `map_info` ZON -> `MapTraceInfoRequest`
  round-trip preserves wire `MapId` (in ZON `is_trace`) and `EntityConfigId` (in ZON `map_id`),
  increments `next_custom_mark_id` from `1_000_000`, and `MapRemoveMarkRequest` removes the mark
  from both `custom_marks` and `tracked_mark_ids`.
- **LastBigSceneMiniMapInfoRequest**: returns `back_pos` inside a dungeon and current player
  position in the open world.
- **Explore tools & projector**: `ExploreSkillRouletteSetRequest` pads/truncates to 8 slots and
  pushes `ExploreSkillRouletteUpdateNotify`; `Summon3Request` validates unlock/id uniqueness and
  spawns at the requested `SummonEntityId`; `RemoveSummonEntityRequest` only removes entities
  owned by `SummonerId`.
- **Profile & music**: `ModifyNameRequest` sets `LastModifyNameTime` to 5 s remaining cooldown
  and pushes `NameModifyNotify`; `PhantomMusicInfoRequest` groups every `PhonographMusic` row by
  each album with `BeginTime = 0, EndTime = 0`; `ShortMessageInfoResponse` returns non-zero
  `BubbleId` / `ChatBgId` / `ChatThemeId` / `ChatCharmId`.
- **Tutorials**: `TutorialUnlockResponse` always carries non-null `UnLockInfo` with
  `CreateTime >= 0`; `TutorialReceiveResponse` grants 20 of item `3` and returns
  `ItemMap = { 3: 20 }`.
- **Cosmetics & camera**: `RoleSkinChangeRequest` resets the role entity and refreshes ornament
  buffs; `ChangeOrnamentRequest` evicts same-`OrGroupId` ornaments on the same `RoleSkinId` and
  pushes `EntityDressOrnamentChangeNotify` + `OrnamentDressInfoUpdateNotify`;
  `CameraSettingModifyRequest` round-trips through `settings/camera` to
  `CameraSettingQueryRequest`.
- **Motorcycle**: `MotorUseSkinRequest` and `MotorChangeOutlookRequest` push
  `EntityRemoveNotify` + `EntityAddNotify` when `equipped_frame` changes and
  `EntityMotorOutlookChangeNotify` when only skin/stickers/decorations change;
  `VehicleChangePassengerRequest` creates the companion entity once, spawns the matched NPC
  passenger on seat `1`, and pushes `VehiclePassengerUpdateNotify` + `VehicleUpdateEntityNotify`;
  preset create/edit/delete persist in `motor_info`.
- **Commands**: `cmd.txt` is read, emptied and executed on heartbeat; `weapon <id> max` grants a
  max-level/breach/resonance weapon; `waveband <role_id>` grants the role's waveband item;
  `echo` creates a custom echo with requested sonata, main stat and sub-stats.
