# Save format (`state/`)

On-disk layout and encoding for every file under `state/`. Names of files and of fields inside
them are part of the format.

## 1. The tree

```
state/
  account/<userId>                 login server account (§3)
  account/link/<safe name>         account name -> player id (§3)
  cfg/key, cfg/iv, cfg/index/...   config server (config.md)
  gateway/client_public_key.der    RSA public key (network.md §5.1)
  mailbox/<player>/*.mail          gifts, read by the game server (§7)
  player/next                      next player id (§4)
  player/<player>/...              one directory per player (§5, §6)
```

Paths use `/`; numbers in paths are decimal. A missing file means "default" unless a section
says otherwise.

## 2. Encodings

Three encodings are in use.

**ZON** (Zig Object Notation) for structured files. tidelab needs a reader and a writer for the
subset that occurs:

| Value | Written as |
|---|---|
| struct | `.{ .field = value, ... }` |
| array / list / tuple | `.{ a, b, c }`; empty `.{}` |
| integer | decimal, optional `-` (also underscores like `1_000_000` may occur in hand-edited files) |
| float | decimal with `.` or as an integer (`0`, `-104338.234`, `123.01772`); `inf`/`nan` do not occur |
| bool | `true` / `false` |
| optional | the value, or `null` |
| string | `"..."` UTF-8 with `\"`, `\\`, `\n`, `\t`, `\r`, `\x..`, `\u{...}` escapes |

Reading rules:

- Fields may be in any order. A field missing from the file takes its default (tables below).
- Unknown fields are accepted and preserved on rewrite so newer saves stay compatible.
- Comments `//` to end of line are allowed.

Writing rules: write every field of the struct (defaults included), `null` for an empty optional,
fixed-size arrays at their full length. Indentation uses 4 spaces, one field per line, short lists
on one line.

**Text lines** for logs and counters: ASCII, `\n` line ends (read `\r\n` too), fields separated
by one space.

**Raw text** for single values (`reconnect_token`).

### Writing safely

- Files written as a whole: write directly, or to `<path>.new` and rename over `<path>`
  (atomic). The log-plus-snapshot files (§6: `world_state`, `quests`, `world_net_ids`,
  `inventory_info`) rewrite their snapshot atomically; others may write directly.
- Skip a write when the bytes are unchanged (R-80c: measurable).
- Append-only logs are appended, never rewritten except by compaction.
- The server writes its in-memory state on exit; never edit `state/` while it runs.

## 3. Accounts

`account/<userId>` (written by the login server, `login.md`):

| Field | Type | Default |
|---|---|---|
| `user_id` | string | |
| `user_name` | string | |
| `token` | string | |
| `sex` | i32 | 0 |

The game server rewrites `sex` when the player picks a gender: it finds the link file whose
`player_id` is the player, then rewrites `account/<that link's file name>` with the new `sex`.

`account/link/<safe name>`: `.{ .player_id = <i32> }`. The safe name replaces each of
`< > : " / \ | ? *` and bytes 0–31 with `_` (network.md §6).

## 4. Counters

A counter file holds one decimal number (then `\n`): the next id to hand out. Reading: the first
token. Taking one id: return the number, write number + 1. Taking a block of `k` ids: return the
number, write number + k (a crash loses the rest of the block, never repeats ids). A missing
file: the first id is the counter's start value and the file is created holding the next.

| File | Start | What |
|---|---|---|
| `player/next` | 666 | player ids |
| `player/<p>/echo/next` | 1 | echo unique ids |
| `player/<p>/weapon/next` | 1 | weapon unique ids |
| `player/<p>/special_item/next` | 1 | special item unique ids |
| `player/<p>/scene/<instance>/entity/next` | 1 | saved scene entity ids (§5.10) |

## 5. Structured player files (ZON)

Each table lists the fields; the type column uses ZON terms (`i32`, `i64`, `u32`, `f32`, `bool`,
`string`, `[T]` a list, `[T; n]` a fixed array, `T?` optional). Meanings follow the client
protocol fields named in the last column where there is one.

### 5.1 `basic_info`

| Field | Type | Default |
|---|---|---|
| `attributes` | struct below | |
| `role_show_list` | [i32] | empty |
| `selected_main_role_id` | i32 | 0 |
| `cur_card_id` | i32 | 0 |
| `cur_player_title_id` | i32 | 0 |
| `last_modify_name_time` | i64 | 0 |
| `birthday` | i32 | 20260407 |
| `display_birthday` | bool | true |

`attributes` (each field maps to `PlayerAttrKey.<PascalCase of the name>`, e.g. `rare_coin` →
`RareCoin`; strings are `ValueType String`, the rest `Int32`):

| Field | Type | Default |
|---|---|---|
| `level` | i32 | 80 |
| `exp` | i32 | 666 |
| `coin` | i32 | 666666 |
| `rare_coin` | i32 | 666666 |
| `head_photo` | i32 | 82001209 |
| `head_frame` | i32 | 0 |
| `area_id` | i32 | 1 |
| `name` | string | a default name |
| `sign` | string | a default signature |
| `sex` | i32 | 0 |
| `origin_world_level` | i32 | 8 |
| `cur_world_level` | i32 | 8 |
| `world_level_time_stamp` | i32 | 0 |
| `cash_coin` | i32 | 666666 |
| `world_permission` | i32 | 0 |

The existence of `basic_info` is what "this player exists" means at reconnect (network.md §8).
Choose tidelab's own default name and signature for new players.

### 5.2 `last_scene`

| Field | Type | Default |
|---|---|---|
| `instance_id` | i32 | 8 (the open world) |

### 5.3 `formation_info`

| Field | Type | Default |
|---|---|---|
| `cur_formation` | i32 | 0 (index into `formations`) |
| `formations` | [Formation; 20] | 20 defaults |

Formation: `cur_role` i32 = 0; `name` string? = null; `roles` [Role?; 3] = nulls.
Role: `role_id` i32; `entity_id` i64 = -1; `on_stage_without_control` bool = false.
Reordering (R-119) is behaviour, see its own spec later.

### 5.4 `explore_tools_info`

`unlocked_explore_skills` [i32]; `active_explore_skill` i32 = 1001; `roulette` [i32];
`explore_extra_item_id` i32 = 0; `active_function_skill` i32 = 0; `function_roulette` [i32];
`function_extra_item_id` i32 = 0; `active_motorcycle_skill` i32 = 0; `motorcycle_roulette`
[i32]; `motorcycle_extra_item_id` i32 = 0. Lists default empty.

### 5.5 Small lists

| File | Fields |
|---|---|
| `music_info` | `favorite_music_ids` [i32] |
| `guide_info` | `finished_groups` [i32] (group 0 counts as finished) |
| `cosmetic_info` | `role_skins`, `phantom_skins`, `fly_skins`, `weapon_skins`, `ornaments`, `viewed_ornaments`: all [i32] |
| `settings/camera` | `camera_mode` i32, `pitch` f32, `yaw` f32, `roll` f32, `pos_x` f32, `pos_y` f32, `pos_z` f32, `fov` i32, `focus_distance` i32, `blur_radius` i32, `tilt_angle` i32, `weather` i32, `atmosphere` i32, `wind_direction` i32; all 0. Maps one to one onto `CameraParamInfo` (`tilt_angle` = `CameraTiltAngle`, `pitch` = `PitchValue` ...). |

### 5.6 `gacha_info`

`banners` [Banner], `buckets` [Bucket].
Banner: `id` i32, `selected_pool_id` i32 = 0, `daily_pulls` i32 = 0, `total_pulls` i32 = 0.
Bucket: `id` i32, `pull_count` i32 = 0, `pity_four` i32 = 0, `guarantee_five` bool = false,
`guarantee_four` bool = false.

### 5.7 `map_info`

`custom_marks` [CustomMark]; `tracked_mark_ids` [i32]; `next_custom_mark_id` i32 = 1000000.
CustomMark: `id` i32, `x` f32, `y` f32, `z` f32, `is_trace` i32, `mark_type` i32, `map_id` i32,
`config_id` i32, `locked` bool, `state` i32 (all required).

### 5.8 `motor_info`

| Field | Type | Default |
|---|---|---|
| `level` | i32 | 1 |
| `exp`, `rewarded_max_level`, `tree_in_use`, `daily_exp_gain`, `daily_exp_limit` | i32 | 0 |
| `tech_nodes` | [TechNode] | empty |
| `task_nodes` | [TaskNode] | empty |
| `owned_skins`, `owned_stickers`, `owned_decorations`, `owned_frames` | [i32] | empty |
| `equipped_skin`, `equipped_frame` | i32 | 0 |
| `equipped_stickers`, `equipped_decorations` | [i32] | empty |
| `presets` | [Preset] | empty |
| `next_preset_id` | i32 | 1 |

TechNode: `id`, `tree_type`, `level` i32, `unlocked` bool, `current`, `target` i32.
TaskNode: `id`, `tree_type`, `type`, `current`, `target`, `rewarded`, `wait_reward`,
`max_reward`: all i32. Preset: `id` i32, `name` string, `skin` i32, `stickers` [i32],
`decorations` [i32], `frame` i32. All required.

### 5.9 Items with unique ids

One file per item, named by its unique id, plus the directory's `next` counter (§4). Loading
reads every numeric file name in the directory.

**`role/<roleId>`** (named by the role's config id, no counter):

| Field | Type | Default |
|---|---|---|
| `level` | i32 | 1 |
| `exp`, `breakthrough`, `skill_branch`, `weapon`, `star` | i32 | 0 |
| `skills` | [(i32, i32)] (skill id, level) | empty |
| `phantom` | [(i32, i32)] | empty |
| `favor` | i32 | 5 |
| `reson` | [Reson] | empty |
| `cur_model` | i32 | 0 |
| `models` | [i32] | empty |
| `base_prop` | [i32], indexed by the client's `EAttributeType` | empty |
| `add_prop` | [i32] | empty |
| `create_time` | u32 | 0 |
| `skill_node_state` | [SkillNode] | empty |
| `resonant_chain_group_index`, `role_skin_id`, `paragliding_skin_id`, `soar_skin_id`, `weapon_skin_id`, `calabash_skin_id` | i32 | 0 |
| `ornaments` | [OrnamentEquip] | empty |
| `enable_self_bgm` | bool | false |
| `voice_language` | i32 | 0 |

`(a, b)` pairs are written as two-element tuples `.{ 1005501, 1 }`. `weapon` is a weapon
unique id. Reson: `id` i32, `open` bool, `increase` i32. SkillNode: `node_id` i32, `active`
bool, `skill_id` i32. OrnamentEquip: `role_skin_id` i32, `ornament_id` i32.

**`weapon/<uniqueId>`**: `id` i32 (config id, required), `func_value` i32 = 0, `level` i32 = 1,
`exp` i32 = 0, `breach` i32 = 0, `reson_level` i32 = 1, `role_id` i32? = null (equipped by).

**`echo/<uniqueId>`**: `id` i32 (config id), `func_value` i32 = 0, `level` i32 = 0, `exp` i32 = 0,
`main_prop` [Prop], `sub_prop` [Prop], `fetter_group_id` i32 = 0, `skin_id` i32 = 0,
`role_id` i32? = null, `equipped_pos` i32 = -1, `unack_sub_prop` [Prop], `lock_prop_index`
[i32]. Prop: `id` i32, `value` i32.

**`echo/presets`**: `groups` [VisionGroup]. VisionGroup: `incr_id` i32 = 0, `name` string = "",
`slots` [i32; 5] = zeros (echo unique ids).

**`echo/calabash`**: `rewarded_levels` [i32], `projector_monster_ids` [i32], `projector_skins`
[ProjectorSkin]. ProjectorSkin: `monster_id` i32, `skin_id` i32.

**`special_item/`**: only the counter is in use in the live save.

### 5.10 `scene/<instanceId>/`

`instance`: `owner_id` i32 = 0, `buff_handle` i32 = 0, `map_time` MapTime, `players` [Player].
MapTime: `hour` i32 = 8, `minute` i32 = 0, `owner_clock_time_span` i64 = 0.
Player: `id` i32, `group_type` i32 = 1, `location` [f32; 3], `rotation` [f32; 3]
(location X, Y, Z in game units; rotation `Roll`, `Pitch`, `Yaw` in that order, degrees, as
the scene join sends them; the live save has `.{ 0, 0, 123.0 }`).

`entity/`: entity files per saved entity id under `entity/<id>/` plus `entity/next`. In the live
save only the counters exist; the entity component files are written only for entities that
are saved (none today). Specify them with the entity work (R-143f) if they are kept.

### 5.11 `inventory_info` and `inventory_info.log` (R-80e, R-108)

`inventory_info`: `.{ .normal_items = .{ .{ .id = <i32>, .count = <i32> }, ... } }` (`count`
default 1).

`inventory_info.log`: text lines `<id> <count>`, applied in order on top of the snapshot after
loading: a line sets the item's count; count ≤ 0 removes the item; a new id is added. Lines
that do not parse are skipped. When the log has more than four times as many lines as the
snapshot has items, the snapshot is rewritten (atomically, with the log applied) and the log
emptied. One writer keeps the two in step: a snapshot written any other way is undone by the log
at the next login.

### 5.12 `reconnect_token`

64 lower-case hex digits, no newline (network.md §6).

## 6. Line-oriented player files

| File | Package | Holds |
|---|---|---|
| `quests` | `internal/quests` | log of quest progress: `F <quest>` finished, `P <quest> <node>:<status>:...` running trees, `D <dungeon>` cleared dungeons, `L <play>` finished world plays; node indexes follow the node table order |
| `world_state` | `internal/save` | entity states, woken/asleep rows: `<entity> <...>` lines, snapshot plus appended changes |
| `world_net_ids` | `internal/world` | net ids of world spawns; `+id` lines appended |
| `hidden_groups` | `internal/quests` | quest clearings: `+key` / `-key` lines |
| `area_states` | `internal/game` | area states |
| `entity_pos` | `internal/game` | moved entities |
| `dungeon` | `internal/game` | dungeon progress |
| `tower` | `internal/game` | 深塔: `F <floor> <stars>`, `P <floor> <roles...>` |
| `shop_bought` | `internal/game` | `<goods> <count>` lines ([shops.md](shops.md) §2.1) |
| `client_storage` | `internal/game` | client key-value storage: `<key> <hex bytes>` lines |
| `achievements` | `internal/game` | achievements |
| `activity_sign`, `activity_task` | `internal/game` | activities |
| `boss_rush`, `lord_gym`, `dream_link`, `scratch_card`, `turntable`, `area_terminal_pins` | `internal/game` | those systems |
| `activity_window` | `internal/game` | `<activity> <begin>`, the panel window each activity's progress belongs to ([panel.md](panel.md) §2.3) |

## 6a. Content panel

`panel/activities`, `panel/pay_goods`, `panel/gacha`, `panel/tower_season`,
`panel/tower_monsters` (the overrides) and `panel/changes.log`
(append-only change log), at the state root: [panel.md](panel.md) §1.

## 7. Mailbox

`mailbox/<player>/<name>.mail`: gifts dropped in, picked up without a restart (`internal/game/mailbox.go`,
[activities.md](activities.md) §5).

## 8. Checks

- Round trip of every file type above: read, write, read gives equal values.
- Default values apply when a field is missing.
