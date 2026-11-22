# World spawning

The world spawner decides which `LevelEntityConfig` rows of the current scene are shown to the
client, adds them (`EntityAddNotify`, entities.md §9) as the player comes near and removes them
as the player leaves. How each entity is built: entities.md §5. Level plays, prefabs, spawner
waves, dungeons and the quest engine wake and hide rows; this spec names the questions the
spawner asks them (§9).

Units: game units (centimetres) after the row's `/100` (entities.md §3.2).

## 1. Which scenes have world spawns

A scene gets world spawns when its instance id is

- one of the open-world instances **8, 100, 900, 902, 903, 905, 906, 909, 910, 912**, or
- an instance a quest sends the player to (story instances such as 911, 907, 2516: the quest
  engine knows them), or
- the dungeon the player has entered from an entrance.

Rows are looked up by the scene's **map**: `InstanceDungeon.MapConfigId` of the instance
(dungeons share maps: 301010 uses map 301000), and a row is identified by (map, `EntityId`)
(R-80g, data-tables.md §2): never ask "does any map carry this id".

## 2. Candidates of a map

Once per (instance, entered-or-not, side quests on or off), the spawner builds the map's
**candidate list**: rows that may ever appear, each with a class and flags. Rows with
`IsHidden` true (except pass 3) or fewer than two `Transform` vectors never become candidates.
Passes, in this order (a row taken by an earlier pass is not taken again, except as noted):

**Names and sets used below**

- `BlueprintConfig.EntityType` sets (by `BlueprintType`): **NPC** = `Npc`, `SimpleNPc`,
  `PasserbyNpc` (R-39); **monster** = `Monster` (R-45); **chair** = `Chair`, `Chair2` (R-101);
  **entrance** = `DungeonEntry`, `DungeonEntryWithTrigger` (R-36); **traversal item** =
  `HookLockPoint`, `HookWithRange`, `KiteHook`, `CableWayHook`, `SuiGuangHook`, `TimeScaledHook`,
  `SequenceDrivenHook`, `ElementalResponseHook`, `Lift`, `Lift2`, `SequenceLift`, `BeamDeliver`,
  `Portal`, `NoRenderPortal`, `NoRenderPortalWithInteract`, `MoveNoRenderPortal`, `InteractGear`,
  `InteractGear2`, `GameplayInteractGear`, `StateSceneItem`, `Collect`, `CollectWithHook`,
  `CollectWithListener`, `TreasureBox`, `VisionTreasureBox`, `HookTreasureBox`. These are the
  names to check at load (entities.md §3.1).
- **door blueprint**: a `TemplateConfig` whose `InteractComponent.Options[].Type.Actions[].Name`
  includes `SetPlayerPos`, `TeleportDungeon` or `ExitDungeon` (R-35, R-77). A row also counts as
  a door when its own `InteractComponent` has such an option.
- **beacon blueprint**: `BlueprintType` starts with `Teleport`, or is `branch2.0_Teleport6` or
  `branch2.2_14_Teleport4`.
- **facility blueprint**: a beacon blueprint, or one of `SceneObj100`, `SceneObj101`,
  `Quest099`, `NPC_Shenghai1`, `NPC0001`, `NPC0002`, `NPC0003`, `NPC0004`, `NPC0005`,
  `NPC0006`, `NPC0008`, `NPC0012`, `NPC0103`, `NPC420078`, `branch2.0_NPC料理店商人乔万娜`,
  `branch2.0_NPC生命点商人薇达`, `branch2.0_TsEntity_拉古纳新贵族女4881159`,
  `branch3.0_314_NPC_zhiwuguo`, `branch3.0_152_NPC扎希拉`, `branch3.0_152_NPCIRIS`,
  `branch3.0_152_NPC阿尔塞尼`, `branch3.0_181_NPC娜娜`, `branch3.5_192_TsEntity_秋鸿`,
  `branch3.5_176_NPC鲙娘` (shops, cooking and synthesis stations; always wanted).
- **name filter** (R-25, R-77): a row whose `Name` contains any of 任务, 道馆, 梦魇, 竞技, 测试,
  演示, 剧情, 教学, 引导, 副本, 活动, 召唤, 特殊敌人, 提前开放, 插旗, 怪物对战, 王之试炼 is a quest,
  challenge or event copy that needs scripts the world does not run.
- **town NPC row**: awake (`InSleep` false), blueprint starts with `NPC` or is in the NPC set,
  and passes the name filter.
- **wanted monster row** on map m: `MapId` = m, not `IsHidden`, blueprint starts with `Monster`
  or is in the monster set, passes the name filter, and is not a scripted actor. A **scripted
  actor** has `BaseInfoComponent.HeadStateViewConfig.HeadStateViewType` = 0 (no HP bar) or any
  `BaseInfoComponent.Category.EntityPlotBindingType` (bound to a plot sequence): outside their
  script they had quest stats and chased the player forever (Septimont gladiator 159850031).

**Pass 1 — facilities.** Awake rows (not `InSleep`) with a facility blueprint. Class
**beacon** if the blueprint is a beacon blueprint, else ordinary. Beacon locations are also kept
in a list for the beacon heal (movement.md §2).

**Pass 1b — scene items.** Rows without a facility blueprint (awake or not) where any of these
holds: blueprint is a
traversal item; the row is one of the instance's `InstanceDungeon.ExitEntities`; (entered
dungeon only) the row is awake and its level play acts on it (R-143h); the row is a **door**
(awake, an entrance blueprint — or a door blueprint / door options, provided it is neither a town
NPC row nor a wanted monster row); the row is an awake chair.

- Name filter: applies only to awake rows that are not level-play entities; a door ignores the
  part 副本 (world-dungeon doors keep 副本 in their names, e.g. 水星天教堂世界副本前门入口). Sleeping
  rows are kept regardless (they appear only when woken, R-77: both ends of a path, entrance
  and exit, must pass).
- Class **seat** for chairs, else **item**. Flag **sleeping** = `InSleep`.

**Pass 2 — monsters and town NPCs.** Rows without a facility blueprint that are wanted monster
rows or town NPC rows:

- In an entered dungeon, every wanted monster is **sleeping** (spawns only when woken, R-56/59).
- A sleeping wanted monster the quest engine owns, or a sleeping wanted monster that is some
  quest step's kill target: **sleeping** (spawns when woken).
- **One monster per spot:** among the other wanted monster rows, the first row (table
  order) with a given (`BlueprintType`, exact `Transform[0]`) claims the spot; later rows with
  the same blueprint at exactly the same position are versions of one monster and become
  **sleeping**.
- Class **npc** for town NPC rows. Awake NPCs get a **spot key**: their `BlueprintType` plus
  their location rounded to whole metres (round(x / 100) per axis).

**Pass 3 — sleeping rows and quest rows.** Rows that are either

- *plain sleeping*: not `IsHidden`, `InSleep`, not a facility blueprint, not a traversal item,
  not a wanted monster (not checked against earlier passes: a sleeping exit entity, taken by
  pass 1b, comes again here); or
- *quest-referenced*: any row whose `EntityId` the quest data names (R-143h), whatever its
  flags (`IsHidden` included), if no earlier pass took that `EntityId`;

all flagged **sleeping**, class beacon if beacon blueprint.

Within the list, candidates keep pass order, then table order. The spawner refers to a candidate
by its position in this list ("candidate order"). Candidates are built in memory per scene.

## 3. Classes, radii and caps

| Class | Spawns within | Leaves beyond | Alive cap | Per refresh |
|---|---|---|---|---|
| beacon | 30000 | 40000 | none | none |
| woken (a *sleeping* candidate that is not an item, once awake) | 30000 | 40000 | none | none |
| item | 8000 | 15000 | 260 | 40 |
| seat | 2500 | 3000 | 120 | 60 |
| npc (awake town NPC) | 8000 | 10000 | 60 | 20 |
| quest NPC (an awake npc candidate the quest engine woke) | 8000 | 10000 | none | none |
| monster and everything else | 8000 | 15000 | 80 | 20 |

- "Alive" counts the world spawns of that class now in the scene. A woken entity is recorded as
  a beacon (it keeps the beacon radii so far quest targets stay); quest NPCs count as npc.
- The monster cap counts every world spawn that is not beacon, item or npc.
- Distances are straight 3-D distances between the candidate's location (moved position if a
  quest moved it, entities.md §3.2) and the player's location.

## 4. A refresh

**When.** (movement.md §2)

- On a `MovePackagePush` that moves the player (or the vehicle they ride) when the player is at
  least **800** (8 m) from where the last refresh ran, or when no refresh has run in this scene
  yet.
- On `HeartbeatRequest` and after flow, quest and interaction requests, if something woke
  entities since the last refresh (the quest engine and interaction actions mark the scene;
  R-143h). The captured login refreshed first at the first `HeartbeatRequest` after
  `JoinSceneNotify` (the quest sync at join had marked the scene) and before any
  `MovePackagePush`.
- Only in scenes of §1, and only when the scene has a player.

**Steps.** With `now` the game clock (logic time, R-129):

1. Raise the net id floor (entities.md §2).
2. The first refresh of a session runs the reconnect clean-up (§6).
3. Prefabs level plays queued are spawned (R-143h).
4. **Forget and remove.** For every current world spawn, in order:
   - its entity is no longer in the scene (killed and removed): forget it, and it may spawn
     again only after `now + 180 000 ms` (3 min);
   - its row has visibility conditions and they now fail (entities.md §10): remove it (quest
     progress moved on: this copy of the NPC is no longer the one to show);
   - a quest clearing hides it now: remove it (R-69);
   - it is farther than its class's leave distance and not taking part in a fight (the server
     tracks which entities hate the formation; combat.md): remove it.
   Removal = `EntityRemoveNotify` for that one entity (entities.md §9), entity dropped from the
   scene.
5. **Corpses.** Killed monsters wait 8 000 ms for their death animation (combat.md), then are
   removed the same way.
6. **Nearest NPCs (R-39, R-50b).** From the candidates near the player (step 7's set): awake npc
   candidates within 8000 whose visibility conditions pass. Sort by distance, ties: one with
   visibility conditions first. Walk the sorted list, skipping a candidate whose spot key was
   already taken, and keep at most 60. Only these may spawn as ordinary npcs this refresh
   (towns have up to 571 NPC rows within 80 m; without this the cap filled in table order;
   two rows of one blueprint at one place are versions of one person and only one shows).
7. **Spawn.** Walk the candidates in candidate order; for each, the first rule that applies
   skips it. (Spatial indexing uses the 3 × 3 block of 100 m grid cells around the player —
   cell = floor(X / 10000), floor(Y / 10000) — plus beacons, awake rows and moved rows; that set
   holds every candidate rule 1 lets through.)
   1. farther than its spawn distance (beacon or woken: 30000; seat: 2500; else 8000);
   2. already spawned;
   3. an awake npc candidate (not a quest NPC) not among the nearest 60;
   4. caps of §3 (woken: no cap, but skip unless awake);
   5. picked or opened and not regrown (world state, R-143h);
   6. hidden by a quest clearing;
   7. sleeping and not awake;
   8. not woken and its row has `EntityVisibleComponent` whose conditions fail;
   9. its respawn time has not come (then the timer is cleared once passed);
   10. no blueprint or template config (entities.md §3.1), or the spawn is refused
       (entities.md §5.6): retry after `now + 3 600 000 ms` (1 h).
   Otherwise spawn it at its location and rotation (entities.md §5), record it with its class,
   and tell the quest engine the entity now has a net id (it may announce interact options,
   R-74; R-143h).
   - Dungeon level: in an entered dungeon, a woken row that is a monster (named by a spawner
     wave, or `Monster` in its blueprint name, or monster `EntityType`) and whose
     `AttributeComponent` has no `Level` is spawned with `Level` = `InstanceDungeon.EntityLevel`
     (D3, R-127; values: g).
8. If anything was spawned or removed, save the world spawn ids (§5).

The order of the spawned entities on the wire is candidate order (captured login: 6 beacons,
1 item, 2 monsters, woken NPCs, … — 58 adds in all at the first refresh, each one
`EntityAddNotify`).

A scene change makes a new scene: no world spawns, respawn timers or last-refresh position
carry over (the client clears its world at `JoinSceneNotify`).

## 5. `world_net_ids` (our format)

`state/player/<p>/world_net_ids` lists the net ids of the world spawns (and level-play monsters,
R-143h) alive in the client, so a later session can remove them (§6). It is a text of
whitespace-separated words, applied in order:

- `+<id>`: alive; `-<id>`: gone; `<id>` (no sign): alive (a snapshot word).

Saving after a refresh: words `+id ` for every alive id not in the file's current alive set,
`-id ` for every id in that set no longer alive, appended (one space after each word, no
newline). When the file's word count plus the new words would exceed 4 × max(alive count, 64),
it is rewritten instead (atomically) as the snapshot `<id> <id> … ` of the alive ids.
(save-format.md §6 calls these "`+id` lines"; they are words, as here.)

## 6. Login and reconnect (R-121)

The first refresh of a session reads `world_net_ids` (the ids a previous run left alive):

- **After a login** (`EnterGameRequest`): the client starts empty, so nothing is removed; the
  ids are only forgotten (the next save writes `-id` for each, or a snapshot). Removing ids the
  client never had logs a client entity-removal error once per id.
- **After a reconnect** (`ReconnectRequest`): the client kept its entities; one
  `EntityRemoveNotify { IsRemove = true, RemoveInfos = [every listed id this scene does not hold
  under that id] }` goes out before the refresh spawns anything.

## 7. Everything is rebuilt per scene join

At each scene join the spawner starts empty (entities.md §11). A teleport within the map is not
a scene join: the next refresh after `TeleportNotify` (when the client's first move arrives at
the new place) removes what is now far and spawns what is near (movement.md §4).

## 8. The robot's `npcs` check

The robot (`server/internal/robot/npcs.go`) runs in memory on a copy of a save. `npcs`:

1. For each of nine spots: travel there (`TeleportTransferRequest` to a teleporter near the spot
   if on another map), move ±1000 on X four times and then onto the spot (so refreshes run).

   | Name | Map | Position |
   |---|---|---|
   | JZ 今州 | 8 | (−41886, 108485, 2595) |
   | QQ | 8 | (1248420, 1185500, 50780) |
   | MZ_A1 | 8 | (−706690, −92630, −22010) |
   | LGNC | 8 | (700114, 583700, 1081) |
   | LHL_XJXY2 | 906 | (−70690, −552300, 36237) |
   | LHL_JMYY | 906 | (85939, −499897, 48674) |
   | QQ wild | 8 | (1223133, 1150181, 29240) |
   | 900 TTSZD | 900 | (−18740, 73600, 4440) |
   | 912 MSTL | 912 | (−1930, 960, −7073) |

2. Count the world spawns whose row's blueprint is in the NPC set (by `BlueprintConfig.EntityType`,
   not by class), and those in the monster set; count same-name NPC pairs within 300 of each other
   when both rows have an `EntityVisibleComponent` ("copies": a day and a night version shown
   together).
3. Print per spot
   `robot: npcs <name> (map <m>): <n> spawned, <k> monsters (<all> world spawns in all), <c> same-name copies within 3 m`,
   and finally `robot: npc check ok` (exit 0) when every spot reached its map, had NPCs or
   monsters, and no copies; else `FAILED` (exit 1).

**Reference run** (3.8 data, progressed save):

    JZ 今州 (map 8): 83 spawned, 0 monsters (130 world spawns in all), 0 copies
    QQ (map 8): 63 spawned, 0 monsters (64)
    MZ_A1 (map 8): 50 spawned, 0 monsters (66)
    LGNC (map 8): 34 spawned, 1 monsters (82)
    LHL_XJXY2 (map 906): 61 spawned, 0 monsters (93)
    LHL_JMYY (map 906): 60 spawned, 0 monsters (61)
    QQ wild (map 8): 1 spawned, 30 monsters (55)
    900 TTSZD (map 900): 70 spawned, 7 monsters (107)
    912 MSTL (map 912): 60 spawned, 0 monsters (73)
    npc check ok

(83 at 今州 is more than the cap of 60: quest NPCs the story woke have no cap.) Counts move with
save quest progress, so A/B comparisons must run on copies of the same save.

**Positions.** The robot can also print, per spot, every world spawn as
`npclist <spot> <map> <npc|monster|other> <config id> <x> <y> <z>` (location as recorded by the
spawner, before the `HalfHeight` lift), sorted for diffing.

## 9. Questions the spawner asks other systems

| Question | Owner |
|---|---|
| is the scene a quest instance / an entered dungeon; the entered dungeon's level play entities | `internal/quests`, `internal/game/dungeons.go` |
| does the quest data name this row; does a quest own this sleeping monster; is it a kill target | `internal/quests` |
| is this row awake (woken), picked or opened, its saved state | `internal/save/worldstate.go` (entities.md §10) |
| does a clearing hide it | `internal/quests/hide_groups.go` |
| was it moved | `internal/game/world_systems.go` |
| do its visibility conditions pass | `internal/world/visibility.go` |
| a spawned entity got a net id (options to announce) | `internal/quests` |
| prefab spawns queued by level plays; spawner waves | `internal/game/prefabs.go`, `internal/quests/prefabs.go` |
| is this entity in a fight with the formation | `internal/combat` (`combat.md`) |

## 10. Tests

- Candidate passes on a hand-made map: a facility, a beacon, each traversal type, a door by
  template and by row options, an entrance, an exit entity, a chair (awake and sleeping), a town
  NPC, a monster, two monster rows at one spot (second sleeps), a scripted actor (both rules), a
  name-filtered row (awake: dropped; sleeping: kept; door with 副本: kept).
- Radii and caps of §3, including woken and quest NPCs without caps.
- Nearest-60 with spot keys and the visibility-conditions tie rule.
- Refresh triggers: 8 m rule, first move, heartbeat after a wake.
- Removal reasons in order; respawn after 3 min; unsupported retry after 1 h; corpse 8 s.
- `world_net_ids`: parse `+`, `-`, bare words; append; compaction threshold.
- Login vs reconnect clean-up (§6).
- Robot `npcs` passes on all 9 spots with 0 same-name copies.
