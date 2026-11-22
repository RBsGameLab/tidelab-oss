# Data tables (`BinData`)

Config tables exported from the 3.8 client's ConfigDB (`tools/Tidelab.ConfigExport`) and loaded by
the game server. The list of loaded tables and fields is in [data-tables.tsv](data-tables.tsv).

## 1. Where the tables are

- One primary directory (`--bindata`), plus optional extra directories (`--more_bindata`,
  `;`-separated), with one JSON file per table: `<Table>.json`. A table may instead be split into
  `<Table>_1.json`, `<Table>_2.json`, ... (read in number order, rows appended). When
  `<Table>.json` exists the parts are not looked at.
- A file is a JSON array; each element is one row, a JSON object.
- File names match the table names in §3 exactly, case included.

## 2. Loading and binary cache

- At start the game server loads the 157 tables of §3, in the order of `data-tables.tsv`,
  before it opens its port; if any fails it logs an error and exits with code 1.
- Each table is indexed by its key column (column `key` of the tsv). A key that occurs twice
  keeps the **last** row in the index; every row stays in the table for code that scans it.
  In the 3.8 data that happens only in `PhantomSkill` (key `PhantomSkillId`, 9 keys with two
  rows, for example 292001).
- `LevelEntityConfig` is different: `EntityId` is not unique (17 288 ids have more than one row,
  each on a different map; 217 022 rows). Lookup is by (map, id): the first row whose `MapId`
  matches. Never ask "is this id on that map" by id alone.
- Anything wrong in a table stops the server: a missing file, a field the tsv marks
  `required` absent from a row, a value of the wrong shape (§4), an enum name it does not know.
  Fields the tsv does not list are ignored.
- Each table is cached in `<bin_cache>/<Table>.bin` (`internal/data/bincache.go`), keyed by the
  row type's layout hash and the JSON file's name, size, and modification time.
- The index of `LevelEntityConfig` is three sorted arrays of row positions, by (map, id, position),
  by (id, position) keeping the first of each id, and by (map, position).
- The quest data: the twelve files `LoadDataSet` reads from the quest directory are cached in the
  same encoding as one value, `<cache dir>/QuestData.bin`: the quest engine's Go structures field
  by field (`data.GoCodec`; a slice or map is its length plus one, 0 for nil; a pointer or a JSON
  value has a presence byte; map entries in key order). The header's layout is the hash of those
  structures' names, fields and kinds; its source is the twelve files' signatures (name, size,
  modification time). Out of date, missing or damaged, the JSON is read and the cache written
  again; a missing quest file means no cache.
- Building the cache: a table's JSON file is read as a stream, one row at a time, and each row
  goes into the cache's bytes as it is read (segments of 16 384 rows, encoded on their own
  goroutines); the rows the loader returns are then read from those bytes, even when the file could
  not be written. The file is byte for byte what writing the rows read from the JSON gives. The
  quest data likewise returns what its new cache reads back.

## 3. The tables and their fields

[data-tables.tsv](data-tables.tsv), one line per field path:

| Column | Meaning |
|---|---|
| `table` | table name |
| `file` | its JSON file (or the stem of its parts) |
| `key` | the key column (§2) |
| `path` | the field: `A.B` a field of an object, `A[]` each element of a list, `A{}` each value of an object used as a map, `A[1]` one position of a fixed array |
| `shape` | what the JSON value must be (§4) |
| `presence` | `required`: every row (or every object at that level) has it; `default`: may be absent (uses 0 / `""` / `false` / empty / the schema default); `optional`: may be absent or `null`; `element`: a list element or map value |

Size: 157 tables, 2509 field paths. Most tables are flat (a few to a few dozen columns). The
big ones:

- `LevelEntityConfig` (490 paths) and `TemplateConfig` (480): `ComponentsData` is an object
  with one member per component; the server reads 16 of them and ignores the rest:
  `BaseInfoComponent`, `AiComponent`, `AttributeComponent`, `ModelComponent`,
  `MonsterComponent`, `EntityStateComponent`, `RewardComponent`, `VisionCaptureComponent`,
  `InteractComponent`, `EntityVisibleComponent`, `SpawnMonsterComponent`,
  `ConditionListenerComponent`, `TargetGearComponent`, `TriggerComponent`,
  `EntityGroupComponent`, `TeleportComponent`. Action lists inside components keep each
  action's `Params` as a free JSON value (`any`).
- `AiStateMachineConfig` (331): `StateMachineJson` is a whole state-machine tree.
- `BaseProperty` (129): one column per attribute.
- `Buff` (96), `Damage` (71), `InstanceDungeon` (45), `RoleInfo` (41).

## 4. Value shapes

How the loader reads a JSON value for each shape:

| Shape | Accepts |
|---|---|
| `int8` ... `int64`, `uint8` ... `uint32` | a JSON number with an integral value, or a string holding one (`"21040014"`); outside the range is an error. `CharacterInitInfo` and `SkillBranchBuff` write all their ids as strings. |
| `uint1` | 0 or 1 (`Buff.ExtraEffectReqSetting`) |
| `float32`, `float64` | a JSON number, or a string holding one |
| `bool` | `true` / `false` only |
| `string` | a JSON string only |
| `enum(N)` | two kinds. `BlueprintConfig.EntityType` (282 names: the client's entity type names; 3.8 added `SceneText`) and `BlueprintConfig.EntityLogic` (`Item`, `Animal`, `Monster`, `Vehicle`, `Npc`, `Vision`, `ClientOnly`, `ServerOnly`, `Custom`, `SimpleCombat`) take the name as a string; an unknown name stops the load. The six `Buff` enums (`DurationPolicy` 0-2, `FormationPolicy` 0-6, `PeriodicInhibitionPolicy` 0-2, `StackingType` 0-2, `StackDurationRefreshPolicy` 0-1, `StackPeriodResetPolicy` 0-1) are numbers in the data; a number outside that range stops the load |
| `protocol-enum(X)` | the number of a value of the client's protocol enum `X` (`Buff.GameAttributeID` = `EAttributeType`) |
| `list` | a JSON array |
| `array(N)` | a JSON array of exactly N elements |
| `tuple(N)` | a JSON array of N elements, each with its own shape (`[i]` paths) |
| `map(int32->...)` | a JSON object whose keys are decimal integers (`{"1": 31000001}`) |
| `object` | a JSON object |
| `any` | any JSON value, kept as it is and read by the code that needs it |

`null` is accepted only for `optional` fields.

## 5. Tables read outside the loader

Some JSON files are read when first needed, not at start. A missing or unreadable file makes
the feature fall back (as stated), never stops the server.

| File | Read | Fields | Missing |
|---|---|---|---|
| `ExchangeReward.json` | claiming a dungeon reward: the row whose `Id` is the dungeon's `RewardId` | `Id`; `RewardId`, an object from claim count (decimal key) to drop package id: the key equal to the count claimed, else key `"1"` given that many times | no reward |
| `AutoPilotCircles.json` | the auto-pilot roads of a map | `MapId`, `WaySplines` (ints); the road ids of all rows of the map, each once, in table order | error to the request |
| `CommonParam.json` | landing (fall) damage | one JSON object, parameter name -> value (not an array); `landing_damage_args_role`, an array of 4 integers | 3.8's values built in |
| `MonsterDetection.json` | Sola guide monster detection | `Id`; `BlueprintType` (string), `BlueprintTypeList` (strings), `EntityConfigId` (int64), each may be absent | no targets |
| `DungeonDetection.json`, `MonsterDetection.json`, `SilentAreaDetection.json` | the guide entries sent as unlocked | `Id` of every row | empty list |

The handlers that use these are ours (the export has them); their behaviour belongs to the
stage that ports the feature.

## 6. Tables outside the client's ConfigDB

Nine files loaded via `--more_bindata` are absent from the client's ConfigDB or carry rows whose
schema is not described in the client's generated definitions:

| Table | Notes | In the 3.8 client |
|---|---|---|
| `CharacterInitInfo` | 53 rows; key `RoleId`; ids as strings (§4); `ConcomitantBuffs` maps role id to buff ids | not in ConfigDB |
| `CharacterPartConfig` | key `ModelId` | not in ConfigDB |
| `GameplayTagParent` | key `Id` | not in ConfigDB |
| `GameplayTagRemap` | key `RawId` | not in ConfigDB |
| `SkillBranchBuff` | key `BranchId`; buff ids as strings | not in ConfigDB |
| `WeaponBaseAttributes` | key `ItemId` | not in ConfigDB |
| `RoleDevProsList` | key `Id` | ConfigDB table `roledevproslist`, no schema definition |
| `RoleDevProsProject` | key `Id` | ConfigDB table `roledevprosproject`, no schema definition |
| `CommonParam` | object mapping parameter names to values | ConfigDB table `commonparam`, no schema definition |

`CharacterInitInfo` and `SkillBranchBuff` are server-only data (R-174: not in the client's
databases, assets or executable). They are optional: a missing file loads as no rows, with a
warning. Then every role gets the autobuff fallback (roles.md §5.3) and no skill branch is
valid (`RoleSkillBranchModifyRequest` answers `ErrSkillBranchNotValid`). Every other missing
table stops the load.

## 7. Data quirks handled by the loader

1. **Float precision.** Three float32 columns are read as float64 (`PhantomFetter.AddProp[].Value`,
   `SkillTree.Property[].Value`, `WeaponConf.SecondPropId.Value`). Use the shortest decimal that
   round-trips the float32 (e.g. `0.081`, not `0.08100000023841858`) so attribute arithmetic stays
   exact.
2. **Duplicate keys in embedded JSON.** Three `AiStateMachineConfig` rows (e.g. `BP_SM_InstanceBase`)
   contain `"Version"` twice in `StateMachineJson` (`{"Version":1633437528,"Version":0,...}`). Keep
   the first value so `EntityFsmComponentPb.HashCode` matches.
3. **Numeric vs string values in free JSON (`any`).** Some `Params.State` / `Conditions[].State`
   entries appear as numbers (`0`) or numeric strings (`"0"`). Accept either wherever a string
   state or id is read from `Params`.

## 8. Tests

- `robot assets --verify`: every table of §3 loaded from its cache equals the same table read from
  its JSON, field by field; quest data loaded from `QuestData.bin` equals its JSON.
- Table list matches `data-tables.tsv`, and every `required` path is checked.
- Shapes of §4: an integer field given as a numeric string; a float given as a string; an unknown
  enum name rejected; a `map(int->...)` with decimal keys; `null` in an optional field.
- `PhantomSkill` 292001 resolves to its last row; `LevelEntityConfig` lookups by (map, id) return
  the row on that map for an id present on several maps (e.g. 131700001 on maps 8 and 906).
- The three float64 fields of §7.1 compute with round-tripped decimals (`WeaponConf.SecondPropId.Value`
  of row 1: `0.081`).
