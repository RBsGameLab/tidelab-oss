# Content panel (R-160)

A page gamesv serves on this machine to view and change what the server runs: which activities
are open, which edition, and when; the pay shop's goods; the limited gacha banners. Changes are
overrides over the automatic rules; whatever the panel never touched runs as before. r160a:
activities (§2). r160b: pay shop (§4), gacha banners (§5). r160c: the Adversity Tower (§6).

## 1. Overrides (`internal/panel`)

- File `<state>/panel/activities`, one line per overridden slot:
  `<slot key> <off 0|1> <edition> <begin> <end>`. Edition 0 = the automatic edition; begin and
  end (Unix seconds) both 0 = the automatic window, else begin < end, both positive.
- File `<state>/panel/pay_goods`: `<good id> <off 0|1> <price> <limit> <update>`; price, limit,
  update `-1` = the catalogue's. Price ≥ 1, limit ≥ 0 (0 = no limit), update a
  `PayUpdateType` 0–4.
- File `<state>/panel/gacha`: `role <gacha id>` / `weapon <gacha id>`; no line = that slot closed.
- File `<state>/panel/tower_season`: `season <id>`, the 深境区 season listed; no line = newest.
- File `<state>/panel/tower_monsters`: `<floor>/<wave>/<blueprint> <prefab> <entity>`: in that
  floor's wave (from 0) every monster of that blueprint is replaced by tower prefab entity
  (prefab, entity).
- A line that does not parse is skipped with a warning at start-up; the rest load.
- A slot without a line is automatic. "Reset" deletes the line.
- Every change appends `<YYYY-MM-DD HH:MM UTC+8> <what>` to `<state>/panel/changes.log`
  (read only; there is no rollback).
- The game reads an immutable snapshot (atomic pointer); the page writes from its own goroutine.
  The panel files live under `<state>/panel`, so save copies carry them.

## 2. Activity plan (`game/activity_plan.go`)

Every place that picks a listed activity asks the plan, computed from the tables, the overrides
and the game clock.

### 2.1 Slots

One slot per `Activity.Type`; double rewards one per `DoubleRewardActivity.Type` (sub type).
Editions exclude `ExcludedActivities` (activities.md §3.1); newest id first; automatic = newest.

| Key | Type | Editions (ids that have) |
|---|---|---|
| `2` | sign-in | `ActivitySign` row, `Activity.Type` 2 |
| `20` | time-point reward | `TimePointRewardActivity` rows, `Activity.Type` 20 |
| `15` | turntable | `TurntableInfo` row, `Activity.Type` 15 |
| `27` | scratch card | `ScratchCardActivityRe` row, `Activity.Type` 27 |
| `26` | dream link | `RogueWhiteCat` row |
| `14` | boss rush | a `BossRushActivity.ActivityId` |
| `34` | 新领主挑战 | `Activity.Type` 34 |
| `7.2`, `7.3` | double rewards, sub types 2 and 3 | `DoubleRewardActivity` row of that sub type |

Every other `Activity.Type` (not excluded) is a slot with `Supported = false`: listed on the
page, never open, overrides for it refused. Opening one needs its server handler first.

With no overrides the plan is exactly the earlier automatic rules (R-37, R-52, R-79).

### 2.2 Edition, window, open

- Edition: the override's when it is one of the slot's editions (else the override is ignored
  and the slot automatic), otherwise automatic.
- Window: the override's when set (`ExplicitWindow`); otherwise R-52's: permanent
  `OpenType` → 0/0 (always open), double rewards the 7-day cycle, the rest the 21-day cycle.
- Open = not off, has an edition, and (no explicit window, or begin ≤ now < end). Closed slots
  are left off the page (the client hides out-of-window activities anyway).
- Entries carry the window in all of `Begin/EndShowTime`, `Begin/EndOpenTime`,
  `LimitBegin/EndTime`, `LimitRewardBegin/EndTime`. Dream link's `LimitedStart/EndTime` and
  新领主挑战's entrance times (`LordGymInfoResponse`, else its 28-day cycle) take the window too.
- Requests of an activity that is not open fail: `SignActivityRequest`,
  `SignActivityFreeDropRequest`, `TimePointActivityRewardRequest` → `ErrActivityLock`;
  `ScratchCardRewardRequest` → `ErrActivityLock`; `RunTurntableActivityRequest` →
  `ErrTurntableActivityNotOpen`; a double reward not listed gives ×1; boss rush levels of an
  unlisted activity are not scored.

### 2.3 A new window is a new run

Per player file `activity_window`: `<activity> <begin>`, the explicit window each activity's
progress belongs to. When the activity page is built, for every open slot with an explicit window:

- recorded begin = window begin → nothing;
- recorded with another begin → progress starts over;
- not recorded → starts over, unless the window begins where the automatic cycle does (the user
  only moved the end of the running cycle: progress kept);
- then the begin is recorded.

Automatic windows never reset progress (as before R-160). Starting over forgets: sign-in and
time-point days (`activity_sign` entry), the turntable state, the scratch card rounds of that
activity, all of dream link's claims and passes, all boss rush levels and score rewards.
新领主挑战's pass records and double rewards' daily counts are kept.

## 3. The page (`internal/panel/web`)

- gamesv options `--panel_address` (default `127.0.0.1:13180`; empty: no page) and
  `--panel_names` (optional directory holding display-name TSVs; default searches `<bindata>/../panel`,
  `<bindata>`, and `--more_bindata`). The log prints `content panel at http://…/`; `open-panel.cmd`
  opens it.
- Requests from other machines → 403; a request whose `Origin` is not the page's own → 403.
- `GET /` the page (embedded `index.html`); `GET /api/activities` → `{now, slots[], changes[]}`;
  `POST /api/activities/{key}` with `{off, edition, begin, end}` → 200, 400 (edition not of the
  slot, bad window, bad JSON), 404 (no such slot or not supported);
  `POST /api/activities/{key}/reset`.
- Times are shown and entered in UTC+8. Display names (optional, loaded at run time from TSVs
  written by `tools/export_panel_names.py` from `lang_multi_text.db`; missing files leave names
  blank and show numeric ids): `activity_names.tsv` (`Activity.Title`), `item_names.tsv`
  (`ItemInfo.Name`), `gacha_names.tsv` (featured 5-stars of each limited banner).
- Takes effect when the client asks again; nothing is pushed. The client sends
  `ActivityRequest` at the first world load after login, every 600 s, at a new day, and at the
  begin/end times of the activities it holds: a change shows within 10 minutes, or at once after
  a relog. Not checked: whether the client drops an activity that a later `ActivityResponse` no
  longer lists (relog does).

## 4. Pay shop (r160b, `game/panel_shop.go`)

- Goods on sale = the catalogue (shops.md §3.3) with the overrides: an off good is left out of
  `PayShopInfo`, `PayShopUpdate` and `PayShopItemUpdate`, and `PayShopBuyRequest` for it fails
  `ErrPayShopGoodsNotExists`; price, limit and update replace the catalogue's everywhere (shown,
  charged, limited). The currency stays the catalogue's (decided).
- Bought counts are kept per good and period (shops.md §3.4): a new limit counts what was
  already bought this period; a new period type starts the count again.
- The page: `GET /api/shop` → every catalogue good with its shop and tab name, item name,
  currency, now and automatic values; `POST /api/shop/{id}` with `{off, price, limit, update}`
  (a value equal to the catalogue's is stored as -1; all automatic = reset);
  `POST /api/shop/{id}/reset`. 404 for a good not in the catalogue, 400 for bad values.
- Takes effect when the client asks again: `PayShopInfoRequest` at every world load,
  `PayShopUpdateRequest` when it switches shop pages.

## 5. Gacha banners (r160b, `game/panel_shop.go`)

- Listed (`GachaInfoRequest`): R-23's standard resonator and weapon banners (default pool view
  type 4 or 5), plus the panel's `role` banner (view type 2, 7, 9, 11) and `weapon` banner (3, 8,
  10, 12) when set to one of those; nothing else. Order by family rank (shops.md §5.1): limited
  resonator, limited weapon, then the standard ones. A banner's pools: every pool of it with a
  `GachaViewInfo` row.
- `GachaInfo.ItemId` and the pull's ticket by the banner's view type (shops.md §5.1): 2/7/9 →
  50002, 3/8/10 → 50005, 11 → 50007, 12 → 50008, 6 with id 5 → 50006, else 50001.
- `GachaRequest` for a banner not listed → `ErrGachaIsNotOpen` (before: any banner id pulled).
- Pity: per `Gacha.RuleGroupId` as before (the data puts every limited resonator banner in group
  1, every limited weapon banner in 2), so switching banners keeps it, as the official game.
- The page: `GET /api/gacha` → the two slots with their banners (newest id first, featured
  5-star names from `gacha_names.tsv`) and the open one; `POST /api/gacha/{slot}` `{id}`;
  `POST /api/gacha/{slot}/reset` closes it.
- Takes effect at once: the client sends `GachaInfoRequest` every time it opens the gacha view.

## 6. Adversity Tower (r160c, `game/panel_tower.go`, `game/tower.go`, `game/prefabs.go`)

### 6.1 Season

- Seasons 1–43 hold only 深境区 (difficulty 3); 稳定区/实验区/超载区 (1, 2, 4) are season 0 only.
- `TowerSeasonOf(difficulty, pick)`: the panel's season when it has floors of the difficulty,
  else the newest with floors of it. So the pick changes 深境区 only. `TowerInfo.CurrentSeason`
  and `DataSeason` = the pick when it has floors, else the newest.
- Everything listed follows it: floors shown, `MaxStar`, star sums, the star rewards (the
  season's `RewardGroup`), and the floor a dungeon runs when not climbing (`TowerFloorOf`: the
  climbed floor, else the floor with that dungeon in the pick, else the newest season's).
- Stars are kept per floor id (each season's floors have their own ids), so a season keeps its
  stars across switches. Claimed star rewards are kept per difficulty and index (not per season,
  as before R-160): a claimed index stays claimed in another season.
- Takes effect at the next world load: the client sends `TowerRequest` at every `WorldDone`.

### 6.2 Monster swaps

- Candidates: every wave entity of every floor's own prefab (`TowerFloors.json` → `Prefabs.json`
  waves), one per blueprint (the first in floor order): 119 on 3.8 data. Names from the entity's
  designer name (`TsEntity_精英_磐石护卫2` → `精英·磐石护卫`).
- When a floor's own prefab spawns a wave (`spawnPrefab`, R-126 path), an entity whose blueprint
  has a swap for (floor, wave) spawns as the candidate's tower entity instead: identity
  `ConfigPrefab` (candidate prefab, candidate entity id) as R-88's tower variants, the
  candidate's `PropertyId`, at the original entity's position and rotation, the floor
  dungeon's `EntityLevel`, the floor's `MonsterBuff`. Kill counting and wave order unchanged.
- A saved swap whose target is no longer a candidate is ignored. Floors without their own prefab
  (the `ShowMonsters` fallback) have no swaps.
- Takes effect the next time the wave spawns.
- The page: `GET /api/tower` → seasons (newest first, floor counts), the listed one, the listed
  floors (深境区 first) with waves as kinds and counts and their swaps, the candidates;
  `POST /api/tower/season` `{season}`, `POST /api/tower/season/reset`;
  `POST /api/tower/swap` `{floor, wave, blueprint, to}` (`to` "" or the same blueprint = reset).

## 6. Tests

`internal/panel/overrides_test.go`, `internal/panel/web/web_test.go`,
`internal/game/activity_plan_test.go`, `internal/game/panel_shop_test.go`,
`internal/game/panel_tower_test.go`.
