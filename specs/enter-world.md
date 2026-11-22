# Entering the world

Login, reconnect, and new-account enter-game push sequences, `JoinSceneNotify`, and loading
requests for the 3.8 client.

Before this spec: [network.md](network.md) (handshake to `EnterGameRequest`, empty-reply rule).
Entities: [entities.md](entities.md). Spawning: [spawning.md](spawning.md). Movement, teleports,
scene change: [movement.md](movement.md).

## 1. The three ways in

| | Login | Reconnect |
|---|---|---|
| Client sends | `ProtoKeyRequest`, `LoginRequest`, `EnterGameRequest { SingleInstanceId = 8, Mode = 1, Pos = {} }` | `ProtoKeyRequest`, `ReconnectRequest` (network.md §8) |
| Server answers | `EnterGameResponse {}` | `ReconnectResponse { Success, LastRecvSeqNo, Timestamp }` |
| Then | the enter-game sequence (§2–§4) | the same sequence |
| Client afterwards | the loading requests of §5 | no loading requests: it goes on playing (captured: `CombatSendPackRequest`, `MovePackagePush`, `FormationAttrRequest`, `TimeCheckRequest`, …) |
| First world refresh | nothing removed (spawning.md §6) | one `EntityRemoveNotify` for the previous run's world spawns |

`EnterGameRequest`'s fields are not read: the instance comes from `last_scene`, not from
`SingleInstanceId`.

A **new account** (a player id with no files) takes the same path; §7 lists what is created.

`IGame.Entered(session, reconnect)` (tidelab's gateway) is where the sequence starts;
`Released(session)` is where pending saves are written (movement.md §3).

## 2. Session start

Before any push:

- Login only: the quest engine's "restored progress" fix-up is armed for the first move (R-82a,
  R-143h); the quest offers and the flow queue start empty (R-117, R-89); the stale-id clean-up
  is set to *login* (spawning.md §6).
- Reconnect: offers start empty (they go out again); the clean-up is set to *reconnect*.
- The player's state loads. Two repairs run before anything is sent (g): every role gets a
  weapon if it has none (its `RoleInfo.InitWeaponItemId`, saved as a new weapon), and roles whose
  attribute table has the wrong length get it rebuilt.

## 3. Login data (`push_data`)

Pushed in this order, except that #8 `StorageInfoNotify` goes first, before #1: `BasicInfoNotify`
makes the client set its UID and initialize its local activity cache; if `StorageInfoNotify`
arrives afterwards it clears and rebuilds storage containers, causing the activity cache to read
an empty container and show the newcomer splash on every login.

| # | Push | Content |
|---|---|---|
| 1 | `BasicInfoNotify` | `Id` = player id; `Attributes` from `basic_info` (Level, Exp, Coin, RareCoin, HeadPhoto, HeadFrame, …; save-format.md §5.1); `RoleShowList` (role id + level of `role_show_list` roles the player has); `CardUnlockList` = every `BackgroundCard` id, read; `CurCardId`, `Birthday`, `DisplayBirthDay`, `LastModifyNameTime`; `ModifyNameTime` "" |
| 2 | `DressPlayerTitleUpdateNotify` | only if `cur_player_title_id` > 0: `{ PlayerTitleId, PlayerTitleExtraParam = 1 }` |
| 3 | `NormalItemUpdateNotify` | the bag's normal items, `NoTips` true; **only `ItemInfo` ids** |
| 4 | `WeaponSkinAddNotify` | unlocked echo skins, `IsLogin` true |
| 5 | `FlyEquipAddNotify` | unlocked glider skins |
| 6 | `RoleFlyEquipNotify` | per glider skin, the roles wearing it |
| 7 | `OrnamentInfoNotify` | unlocked ornaments, ornaments per role, viewed ones |
| 8 | `StorageInfoNotify` | client storage (sent first, before #1): six default records (`SystemId` 12, 7, 6, 8 empty sets; the ornament set; one more empty), then saved entries replace them |
| 9 | `RoleChangeUnlockNotify` | main-character variants unlocked (`UnlockRoleIds`) |
| 10 | `PbGetRoleListNotify` | the role list (roles 1000–1999; of the main characters only the selected one) |
| 11 | `RoleFavorListNotify` | favour levels |
| 12 | `RoleMotionListNotify` | role motions |
| 13 | `RoleConfigInfoNotify` | `{RoleId, SkillBranch}` per role |
| 14 | `FuncOpenNotify` | every `FunctionCondition.FunctionId` with `Flag` 2 (all functions open) |
| 15 | `AreaTerminalInfoNotify` | the area terminal's region groups |
| 16 | `AdviceSettingNotify` | `IsShow` false |
| 17 | `ControlInfoNotify` | empty |
| 18 | `InstDataNotify` | empty |
| 19 | `CalabashMsgNotify` | data bank: level = the highest `CalabashLevel.Level`, every level unlocked, `CalabashDevelopReward` rows with `IsShow` as unlocked |
| 20 | `PhantomInteractionUnlockNotify` | echo projector unlocks |
| 21 | `CalabashLevelsRewardNotify` | `RewardedLevels` from the save |
| 22 | `ItemPkgOpenNotify` | `OpenPkg` 0…7 |
| 23 | `BuffItemNotify` | empty |
| 24 | `EnergyUpdateNotify` | two entries: `{EnergyCount 240, Id 5}`, `{EnergyCount 480, Id 6}` (fixed) |
| 25 | `LevelPlayInfoNotify` | empty |
| 26 | `PlayerVarNotify` | empty |
| 27 | `RoguelikeCurrencyNotify` | empty |
| 28 | `PassiveSkillNotify` | empty |
| 29 | `MailInfosNotify` | the mailbox (`activities.md` §5) |
| 30 | `SettingNotify` | empty |
| 31 | `TrackMoonIllustratedNotify` | empty |
| 32 | `TrackMoonTokenCoinNotify` | `TokenCoin` 0 |
| 33 | `SilenceNpcNotify` | empty |
| 34 | `ContextIdNotify` | `Id` 0 |
| 35 | `PushDataCompleteNotify` | empty |

The client logs and builds nothing visible from 1–33 alone; 34–35 end the data phase. Which of
1–33 the 3.8 client strictly needs to finish loading was **not tested** (R-143d saw it stop
right after `EnterGameResponse` when nothing was pushed). Send all of them, in this order, until
a real-client test shows one can go.

## 4. Scene join

Runs at login, at reconnect and at every scene change (movement.md §4.4):

1. The flow queue releases a flow the switch cut off (R-89; R-143h).
2. The previous scene, if any, is closed (movement.md §4.4 step 3).
3. The scene loads (`scene/<i>/instance`, `formation_info`, `explore_tools_info`); the player's
   place in it: movement.md §4.4 step 4. On the first scene of a new player the explore tools
   are initialised (§7).
4. The 深塔 team, if any, is swapped back for the player's own (R-86, R-143h); an empty first
   formation gets roles 1211, 1108, 1506 (current 1211) and a formation index outside 0–19
   becomes 0 (g: formation.md).
5. The scene and `formation_info`, `explore_tools_info` are saved.

Then, pushed in this order:

| # | Push | Content |
|---|---|---|
| 1 | `ExploreToolAllNotify` | `ExploreSkill` = `active_explore_skill`, `SkillList` = `unlocked_explore_skills` (save-format.md §5.4) |
| 2 | `ExploreSkillRouletteUpdateNotify` | `RouletteInfo`, four entries: {`roulette` padded/cut to 8, `explore_extra_item_id`, `active_explore_skill`}; {`function_roulette` to 8, `function_extra_item_id`, `active_function_skill`}; {} (empty); {`motorcycle_roulette` to 8, `motorcycle_extra_item_id`, `active_motorcycle_skill`} |
| 3 | `VisionExploreSkillNotify` | `ExploreSkill` 1001 (fixed) |
| 4 | `JoinSceneNotify` | §4.1 |
| 5 | `AfterJoinSceneNotify` | empty |
| 6–9 | `QuestFinishListNotify`, `QuestListNotify`, `BehaviorTreeInfoNotify`, `QuestReadyListNotify` | quest sync (R-143h; R-117: trees and offers once per session) |
| – | (pushes of the day's activity tasks and of an entered dungeon's level play, when there are any: R-143h) | |
| 10 | `SwitchBattleModeNotify` | empty |
| 11 | `SceneRoadSyncNotify` | only if the map has roads: `{ InstanceId, EnabledRoads }` = the union of `WaySplines` of the `AutoPilotCircles` rows whose `MapId` is the scene's `MapConfigId`, in first-seen order (a table read outside the loader, data-tables.md §5) |
| 12 | `InfrRoadUpdateNotify` | `RoadInfo.Roads` = every `InfrRoadBuild` row with `DungeonId` 906: `{RoadId = Id, Status = InfrStatusComplete}` (Lahai-Roi's roads all built), sent in every scene |
| 13 | `TimeCheckNotify` | `ClientTime` 0; `ServerTime`, `ServerStopTime`, `SceneStopTime` = real time in ms; `FlowTime` = the scene's flow time (§4.2), which starts at the same real time |
| 14 | `FormationAttrNotify` | fixed: `CurTime` 1534854458, `FormationAttrs` {AttrId 1, Ratio 2400, BaseMaxValue 24000, MaxValue 24000, CurrentValue 22000}, {10, 2400, 7000, 15000, 7000}, {14, 0, 10000, 10000, 10000} (g: placeholder values, `combat.md`) |
| 15 | `JSPatchNotify` × 12 | §6 |
| 16 | `UpdateFormationNotify` | the formations (g: formation.md) |
| 17 | `FlowStartNotify` | only when a scene change carried a pending flow (R-143h) |

The first `HeartbeatRequest` after this runs the first world refresh (spawning.md §4); its
`HeartbeatResponse` goes out after the adds.

### 4.1 `JoinSceneNotify`

`{ MaxEntityId = 0, TransitionOption = {}, SceneInfo }`. `SceneInfo` (`SceneInformation`):

| Field | Value |
|---|---|
| `InstanceId` | the scene's instance id |
| `OwnerId` | `instance.owner_id` (0) |
| `CurContextId` | the player id |
| `Mode` | `Single` |
| `SceneEntityId` | 2 (fixed; entities.md §4.4) |
| `TimeInfo` | `{ Hour, Minute, OwnerTimeClockTimeSpan }` from `instance.map_time` (8, 0, 0 in the save) |
| `DataLayers` | every `DataLayerConfig.Id` with `LevelId` = the scene's `MapConfigId` and `InitLoad` true (67 on map 8), no repeats, table order. Instance 906 (Lahai-Roi): then drop every layer some `InfrRoadBuild` row with `DungeonId` 906 lists in `UnloadDataLayers`, and add every layer they list in `LoadDataLayers` (the roads built). The client activates exactly these layers and unloads the rest; an empty list left out water bodies and quest layers |
| `AreaStates` | `{AreaId, State}` for every area a quest changed; the client starts the others at `Area.IsInitActived` |
| `PlayerInfos` | one `ScenePlayerInformation`, below |
| `AoiData.Entities` | every entity of entities.md §4 (vehicle, player entity, companion, battle entity, roles with their visions and concomitants), in creation order |
| others (`SceneId`, `EndTime`, `ResetPointEntityId`, `SceneTraceId`, `LastHighLevelArea`, `EnableRoads`, …) | empty / 0 |

`ScenePlayerInformation`: `PlayerId`; `PlayerName` = `basic_info` name; `Level`; `Location` and
`Rotation` (Pitch, Yaw, Roll) = the player's place in this scene; `Group` = the player's
`group_type` (1); `CurRole` = the formation's current role; `PlayerPrefix` 1;
`PlayerGEIncHandle` 0; `VehicleInfoPb` = {0, 0}; `GravityDirection` (0, 0, 0);
`PlayerSceneGroupData` = one entry `{ Group = group_type, CurRole, LivingStatus = Alive,
FixedLocationByCfg = false, FightRoleInfos = [ {RoleId, EntityId = the role entity's net id,
OnStageOutOfControl = the slot's flag} per formation slot ] }`.

Captured: map 8, player at (−111141.48, 74229.27, 6980.71), yaw 68.93, roles 1311, 1505, 1304
(current 1304), 12 entities, 40457 bytes.

### 4.2 Flow time

The scene keeps a flow clock: at join it is anchored at the real time with dilation 1. Flow
time = anchor + (game-clock ms since the anchor) × dilation. `TimeStopPush { TimeDilation }` and
`RTimeStopPush { Dilation }` re-anchor it with a new dilation (non-finite values ignored).
`TimeCheckRequest` answers `FlowTime` from it (movement.md §6). Captured: after a `TimeStopPush`
the flow time trailed real time by 312 ms.

## 5. What the client asks while loading

Requests sent by the client after the login pushes (interleaved with `CombatSendPackRequest`,
`HeartbeatRequest`, `EntityActiveRequest`, and `MovePackagePush`):

| Request | Server answer |
|---|---|
| `QuestReviewInfoRequest` | empty |
| `QuestBranchRequest` | `Success`, empty `QuestBranchInfo` |
| `AccessPathConfigRequest` | empty |
| `GuideInfoRequest` | finished guide groups from `guide_info` |
| `PhantomMusicInfoRequest` | music albums (`music_info`) |
| `InputSettingRequest` | empty |
| `PhantomFetterRecommendRequest`, `PhantomAttrRecommendRequest` (per role) | `Success` |
| `GachaInfoRequest` | banners |
| `FishingInfoRequest` | fishing data |
| `PlayerTitleRequest` | titles |
| `MotorOutlookInfoRequest`, `MotorInfoRequest`, `MotorCreateRequest` | motorcycle data from `motor_info`; `MotorCreate` `Success` |
| `WeatherSwitchInfoRequest` | empty |
| `HonamiStoryActivityInfoRequest`, `SheriffZoneInfoRequest` | `Success` |
| `PhantomEquipGroupInfosRequest` | `Success` |
| `PlayerHeadRequest`, `MonthCardRequest` | heads; month-card status |
| `RoleFavorListRequest`, `RoleIllustratedInfoRequest` (empty), `RoleDevelopConfigRequest` | role data |
| `TeamRecommendRequest` | empty |
| `IllustratedInfoRequest`, `ExchangeRewardInfoRequest` (empty), `ItemExchangeInfoRequest` (empty), `InfluenceInfoRequest` (empty), `ForgeInfoRequest`, `ValidTimeItemRequest` (empty) | system responses |
| `EnergySyncRequest` | `Success` |
| `NormalItemRequest`, `WeaponItemRequest`, `PhantomItemRequest`, `LoadEquipInfoRequest`, `RoleSkinRequest`, `LoadCalabashSkinInfoRequest` | inventory (`NormalItemResponse`: only `ItemInfo` ids) |
| `WeeklyFrameworkInfoRequest`, `TutorialInfoRequest` (empty), `AchievementInfoRequest`, `LivenessRequest`, `PhotoMemoryRequest` (empty), `GivebackInfoRequest`, `GetDetectionLabelInfoRequest` | system responses |
| `PsnSettingRequest`, `XboxSettingRequest`, `PlayerGameTokenRequest`, `PlayerPsnBlockListRequest` (empty), `PlayerXboxBlockListRequest` (empty) | `false` / "" |
| `FriendAllRequest` | friend list |
| `GetFormationDataRequest` | the formations |
| `JoinSceneNotifyConfirmRequest` | empty |
| `EnterAreaRequest { Id }` | `{ Id }` echo |
| `LoadingLevelAreaRequest` | empty |
| `UpdateSceneDateRequest { Hour, Minute }` | empty |
| `FormationAttrRequest` | `Success` |
| `PrivateChatDataRequest`, `PrivateChatOperateRequest` | chat (`PrivateChatHistoryNotify` before the reply) |
| `AdviceRequest`, `LanguageSettingUpdateRequest`, `ClientPullResourcePackageRequest` | `Success` |
| `StorageInfoUpdateRequest` | saves client storage, `Success` |
| `EntityActiveRequest { EntityId }` | entities.md §8 |
| `PlayerMotionRequest` | empty |
| `ShortMessageInfoRequest`, `PayShopInfoRequest`, `PayInfoRequest`, `LordGymInfoRequest`, `DirectTrainInfoRequest` (empty), `ButtonIsEnableRequest`, `ActivityRequest`, `BattlePassRequest`, `MailBindInfoRequest` (empty), `SlashAndTowerInfoRequest`, `TowerRequest`, `AdventureManualRequest`, `ExploreProgressRequest`, `MapTraceInfoRequest`, `LevelPlayStateListRequest` | system responses |
| `InitRangeRequest { RangeEntityId }` | range state |
| `PlayerVoxelEnvRequest { VoxelEnv }` | echo |
| `TraceEntityPosRequest { DungeonId, ConfigId }` | where a not-loaded entity is: `{Success, Pos}` or `ErrEntityNotFound` |
| `SceneLoadingFinishRequest` | empty |
| `TeleportDataRequest` | movement.md §4.1 |
| `TimeCheckRequest` | movement.md §6 |
| `MapUnlockFieldInfoRequest` | every field id unlocked |
| `ApplySceneClientEntityIncIdRequest` (maps with client entities) | entities.md §7 |

## 6. No `JSPatchNotify`

In the 3.8 C# client, `JSPatchNotify` is a no-op; tidelab sends **no** `JSPatchNotify` at scene
join, on entity add/remove, or at heartbeat. Client-side modifications live in `tools/cspatch` and
`tools/rbauto`.

## 7. New account

A player id with no files (first login of a new account name, network.md §6):

- `basic_info`, roles, items: created from defaults and `CharacterInitInfo` (roles.md).
- `last_scene` missing → instance 8 (save-format.md §5.2).
- `scene/8/instance` missing → created with no player; the scene join places the player at
  `InstanceDungeon(8).BornPosition`, rotation `BornRotation`.
- First scene of this player (no `scene/` sub-directory under `<state_dir>/player/<id>` yet):
  `explore_tools_info` is set:
  - `unlocked_explore_skills` = `PhantomSkillId` of every `ExploreTools` row whose
    `Authorization` is empty, table order;
  - `roulette` = 6002, 1001, 1007, then each `ExploreTools.PhantomSkillId` (table order) that is
    1015, 1029 or 1009, padded with 0 to 8;
  - `active_explore_skill` 1001, `active_function_skill` 0;
  - `motorcycle_roulette` = 6001, 6003, 6007, 6011, 6012, 6020, 0, 0; `active_motorcycle_skill`
    6001.
- Empty formation → 1211, 1108, 1506 (§4 step 4; these roles must exist: roles.md).

## 8. Reconnect

Same as login from §2 on (the same 35 data pushes and the same scene join), with
`ReconnectResponse` instead of `EnterGameResponse` and the reconnect clean-up at the first
refresh (spawning.md §6). The client keeps its UI and entities and sends none of §5's loading
requests.

## 9. Tests

- Order of the 35 data pushes (with `StorageInfoNotify` first) and of the scene-join pushes.
- `JoinSceneNotify`: data layers for map 8 (67 ids) and for 906 (road layers swapped); player info
  fields; `FightRoleInfos` entity ids equal the role entities' ids.
- Explore tool defaults for a new player; roulette padding to 8; fourth roulette entry.
- `TimeCheckNotify` fields; flow time with dilation 1, 0.5, 0, and a re-anchor.
- `SceneRoadSyncNotify` from `AutoPilotCircles`; omitted for a map without roads.
- No `JSPatchNotify` sent.
- Reconnect sends `ReconnectResponse` first and the same pushes.
- Loading requests of §5 receive their expected responses.
