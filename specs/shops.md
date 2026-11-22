# Shops, pay shop, manufacture, material replace, and gacha

NPC shops, pay shop catalogue and periods, crafting/forging/synthesis/exchange, and gacha/convene.

---

## 2. NPC / fixed shops (`ShopFixed`)

### 2.1 Save file: `player/<id>/shop_bought`

Plain ASCII text file, `\n`-terminated lines (accept `\r\n` when reading):

```
<goodsId> <count>
```

- Each line has two space-separated base-10 `i32` values: `ShopFixed.Id` and total bought count.
- Missing file means an empty map (0 bought for every goods entry). Lines that do not parse as
  two integers are ignored.
- On purchase, the entire map is rewritten as `<id> <count>\n` lines.

### 2.2 Building a `ShopInfo`

For a given `shop_id`:
- `ShopInfo.ShopId` (#1) = `shop_id`; `UpdateTime` (#2) = 0.
- `ShopInfo.ItemInfoList` (#3, repeated `ShopItemInfoNew`): iterate `ShopFixed` in table order,
  keeping rows with `ShopId == shop_id` and `Show == true`:
  - `Id` (#1) = `goods.Id`
  - `BoughtCount` (#2) = `bought[goods.Id]` (default 0)
  - `Lock` (#3) = `false`
  - `ItemId` (#4) = `goods.ItemId`, `ItemNum` (#5) = `goods.ItemNum`
  - `MoneyList` (#7, repeated `ShoppMoneyInfo`) = entries of `goods.Price` (`MoneyId` #1, `MoneyNum` #2)
  - `OriginalMoneyList` (#11, repeated `ShoppMoneyInfo`) = entries of `goods.OriginalPrice`
  - `LimitNum` (#10) = `goods.LimitNum`
  - `Label` (#12) = `goods.Label`, `SwitchText` (#13) = `goods.SwitchText`, `PurchaseText` (#14) = `goods.PurchaseText`, `SortIndex` (#16) = `goods.SortIndex`.

### 2.3 `ShopInfoNotify` (18614) before entity interaction

Called by `EntityInteractRequest` ([quests.md](quests.md) §4.1) before replying: collect every
distinct `ShopId` in `ShopFixed` in first-seen table order, build each `ShopInfo` (§2.2), and
push `ShopInfoNotify { VersionStr (#15) = "1", ShopList (#12) = shops }`.

### 2.4 `ShopInfoRequest` (29540) → `ShopInfoResponse` (28926)

- **Request**: `VersionStr` (#15, string).
- **Behaviour**: build `ShopList` for every distinct `ShopId` in `ShopFixed` (first-seen order)
  using `shop_bought`, and reply `ShopInfoResponse { ErrorCode (#13) = Success, VersionStr (#9)
  = request.VersionStr, ShopList (#8) = shops }`.

### 2.5 `ShopUpdateRequest` (17187) → `ShopUpdateResponse` (27292)

- **Request**: `ShopId` (#13, int32).
- **Behaviour**: reply `ShopUpdateResponse { ErrorCode (#1) = Success, Info (#12) =
  buildShop(request.ShopId) }`.

### 2.6 `ShopBuyRequest` (28078) → `ShopBuyResponse` (27294)

- **Request**: `Num` (#4, int32), `MoneyId` (#6, int32), `Id` (#7, int32, `ShopFixed.Id`),
  `VersionStr` (#10, string), `ShopId` (#11, int32), `InteractEntityId` (#12, int64).
- **Behaviour** (`PlayerShops.Buy`; prices are charged):
  1. `goods = ShopFixed(Id)`; missing or `goods.ShopId != request.ShopId` → `ErrShopFixeNotExist`.
     `Num <= 0` → `ErrShopIllegalBuyCount`.
  2. `already = bought[goods.Id]`; `goods.LimitNum > 0 && already + Num > LimitNum` →
     `ErrShopNumLimit` with `BoughtCount (#14) = already`.
  3. Every `Price` pair × `Num` must be held (money from the attributes, [inventory.md](inventory.md)
     §2.3), else `ErrItemNotEnough`; then taken.
  4. Grants `max(ItemNum, 1) × Num` of `goods.ItemId` (`GrantWeaponOrItem`: weapons capped at 5,
     R-116; normal items R-108/R-109), records `bought[goods.Id] += Num` and replies `Success`,
     `ShopId (#6)`, `Id (#1)`, `BoughtCount (#14)`.

---

## 3. Pay shop / 商城

Code: `server/internal/game/paystore.go` (catalogue, values, periods), `paystore_player.go`
(state, requests). Goods and prices are not in the client; they are defined in §3.2–§3.3.

### 3.1 Client facts

- Shops sent: 1 特别推荐, 3 礼包中心, 4 海市兑换, 6 装扮橱窗, 100 凝刻月相 (`PayShopInfo` from
  the `PayShop` row: `ShopTabViewType`, `DynamicTabId`, `Sort`, `Money`, `SortRule`). Not yet: 5
  积分商店, 7 旅程专享, 2xx event shops.
- The nav bar names come from `UiDynamicTab.TabName` (client lookup). Tab names
  (`PayShopTabConfigInfo.Name`) and banner titles (`PayShopRecommendConfigInfo.TabName`) are
  shown verbatim as sent: the server sends localized text (`payTexts`) rather than localization
  keys.
- Gift and skin cards are drawn from the gift item's package: `ItemInfo.Parameters` 2, else 4
  → `GiftPackage.Content`. A skin good must be a gift item holding the skin. The client marks
  owned role skins sold out locally.
- 礼包中心 (3) also lists `PayGiftShopInfo.Gifts` (ShopId 0/3/7 → shop 3, 7; any other → 3).
- `PayGiftRequest` / `PayItemRequest`: on Success the offline client skips external SDK payment;
  the server pushes `PayGiftSuccessNotify` / `PayItemSuccessNotify`. The price label is `¥` +
  `Amount` using region `Default` (`Pay` rows exist for PayId 1–8, 15).
- `PayShopRecommend.RecommendType`: 1 month card, 2 role skin (RecommendId = a good id), 3 week
  card, 4 motor (RecommendId = a PayGift id).
- Month card = PayGift 42, item 50011 (CommonParam `MonthCardPayItemId`). RemainDays: -1 none,
  0 today is the last day. `MonthCardDailyRewardNotify` triggers the client's month-card reward
  dialog (immediately, or at `WorldDone`).

### 3.2 Values (星声 per unit; 1 月相 = 1 星声)

| What | Value |
|---|---|
| 星声 3, 月相 4 | 1 |
| tickets 50001/50002/50005–50008 | 160 |
| 余波珊瑚 50004, 幻梦珊瑚 50009 / 残振珊瑚 50003 | 20 / 2 |
| 贝币 2 | 1/5000 |
| role skin 5★ / 4★ (by the gift item's quality) | 2480 / 1280 |
| weapon skin / glider / motor look (`MotorSkin`, any 89xxxxxx) / ornament | 980 / 680 / 1280 / 480 |
| head (`PlayerHeadRe`), card (`BackgroundCard`) | 0 (every one is owned) |
| other ItemInfo, by quality 1–5 | 5 / 15 / 40 / 120 / 400 |

- Gift price (月相) = half the contents' value at the nearest tier of 60, 300, 680, 1280,
  1980, 3280, 6480 (ties go down).
- Skin price (月相) = the value of the pack's dearest cosmetic.
- Real money: ¥1 = 10 星声 (`AstritePerYuan`).

### 3.3 Goods

| Shop / tab | Goods | Price | Limit |
|---|---|---|---|
| 3/1 特惠专区 | 51018–51025, 51026 | gift | 1 a week |
| 3/1 | 51017, 51014 | gift | 1 a month |
| 3/1 | month card (PayGift 42) | ¥30 = 300 星声 | 180 days |
| 3/2 等级礼包 | 52003, 52004 | gift | once |
| 3/3 时装联合会 | 52014 | gift | once |
| 3/4 常驻礼包 | 52002 (once); 51006, 51007, 51010, 51011, 51015, 51027, 51034, 51039, 51038, 51048–51051 | gift | 1 a month |
| 3/5 新旅礼包 | 51032, 51033, 52017–52019 | gift | once |
| 6/1 服饰橱窗 | 53001–53005, 53008–53011 (ids 106100001–009) | skin | once |
| 6/2 换影橱窗 | 54001 (105100101) | skin | once |
| 6/3 载具外观 | 54002–54007, 54009, 54010 (105100102–109) | skin | once |
| 6/4 饰品橱窗 | 55001–55009 (107100101–109) | skin | once |
| 4/3 聆听星声 | tickets 50001, 50002, 50005, 50007, 50008 (ids 104300003, 104300001, 104300002, 104300004, 104300005) | 160 星声 | none |
| 4/1 余波珊瑚, 4/4 幻梦珊瑚 | the 3 tickets; materials 4–5★ | 8 coral; ⌈value/20⌉ | 5 a month; 5★ 5, 4★ 20 a month |
| 4/2 残振珊瑚 | materials 2–4★ | ⌈value/2⌉ | 2★ 100, 3★ 50, 4★ 20 a month |
| 100 凝刻月相 | PayItem 1–6 (60 … 6480) | ItemCount 星声 | none |

- Gift goods use the client's PayGift id of the item (list in `payGiftGoods`); identical
  re-releases (same package) are left out. Not sold: 54008, 54011 (chat backgrounds and bubbles,
  no server system), 52005–52013 (归旅, shop 7 later).
- Materials: `ItemInfo` MainTypeId 4, ItemType 1, ShowTypes without 47 (异相).
- Exchange good id = (5 + tab) × 10^8 + item, except the 聆听星声 tickets: their ids are CommonParam
  `GachaItemIdToShopId`, because the gacha view's exchange button looks up that good id and fails
  when it is missing.
- Prices in 星声/月相 are paid from the money attributes (inventory.md §2.3).
- Gift card picture: `StageImage` and `ShowStageImage` = `IconGiftPack728/T_IconGiftPack728_<k>_UI`
  (728×352; `payGiftImages`; month card `Month`). Large cards (tab view type 3) read `StageImage`;
  small cards read `ShowStageImage`. Without it the client stretches the gift item's square
  256×256 icon, and 71 of 136 gifts share `T_IconGiftPack_dj_19`. The official picture per gift is
  not in the client data: `<k>` is the same-kind picture where an item shows one (057 浮金/溶剂,
  058 浮金珍藏), else an unused one per series.
- Week card banner (type 3) is sent hidden; other banners as configured.

### 3.4 Periods

Daily 04:00 UTC+8 (`Cycle(now, 1)`), weekly Monday 04:00 (`Cycle(now, 7)`), monthly the 1st
04:00 UTC+8. `PayShopItem.UpdateType` = the period, `UpdateTime` = its end; bought counts are
kept per period and read as 0 in a new one.

### 3.5 Save file `player/<id>/pay_shop`

Lines `b <good> <count> <periodBegin>` (periodBegin 0 for once/none) and `m <lastDay>
<claimedDay>` (month card, game days since `ScheduleEpoch`; no line = never bought).

### 3.6 Requests

| Request | Behaviour |
|---|---|
| `PayShopInfoRequest` | `Infos` = the 5 shops with their goods; `PayShopTabConfigs` = `PayShopTab` rows of those shops (Chinese names, `Money` = the tab's coral on shop 4, else the shop's); `PayShopRecommends` = all rows; `PayGiftShopInfo` = the month card |
| `PayShopUpdateRequest` | that shop (`ErrPayShopNotExists` for others) |
| `PayShopItemUpdateRequest` | the listed goods |
| `PayShopBuyRequest` | unknown good `ErrPayShopGoodsNotExists`; count outside 1–9999 `ErrPayShopIllegalBuyCount`; over the period limit or skin owned `ErrPayShopGoodsBuyLimit`; money short `ErrItemNotEnough`. Else takes the money, gives the package contents (gift items) or the item, records the count |
| `PayInfoRequest` | PayItem 1–6, `Amount` = ItemCount / 10 |
| `PayItemRequest` | takes ItemCount 星声, gives ItemCount + BonusItemCount 月相, pushes `PayItemSuccessNotify` |
| `PayGiftInfoRequest` | the month card |
| `PayGiftRequest` (42) | `ErrMonthCardDaysMax` when RemainDays + 1 + 30 > 180; takes 300 星声; card runs 30 more days (from today when none); gives today's 90 星声 (`MonthCardDailyRewardNotify`) when not given, then 300 月相 (`MonthCardContent` 1) with `MonthCardUseNotify`, then `PayGiftSuccessNotify` |
| `MonthCardRequest` | `Days` = RemainDays, `IsDailyGot` |

Giving contents: role skin → `CosmeticInfo.RoleSkins` + `RoleSkinAddNotify`; weapon skin →
`WeaponSkinAddNotify`; glider → `FlyEquipAddNotify`; ornament → `OrnamentUnlockNotify`; motor
looks, heads and cards are owned from the start; the rest through `GrantWeaponOrItem`.

### 3.7 Month card daily reward

At login (end of the login pushes) and on purchase: while RemainDays ≥ 0 and today not given,
give 90 星声 and push `MonthCardDailyRewardNotify { ItemId 3, Count 90, Days }`. Missed days
are lost.

---

## 4. Manufacture (synthesis, cooking, forging) and material replacement

Every formula counts as unlocked. All inventory changes go through the single snapshot+log
inventory writer (R-108) and emit `ItemInfo`-filtered `NormalItemUpdateNotify` /
`NormalItemAddNotify` (R-109, R-116) with `NoTips = true`, `Update` before `Add`.

### Shared strict craft helper

Given `(ingredients, product_id, count)`:
1. Verify `normalItemCount(need.ItemId) >= need.Count * count` for every ingredient; if any is
   short, return false without modifying state.
2. Consume `need.Count * count` of each ingredient, recording updated counts in `updated`.
3. Add `count` of `product_id`; record in `added` if prior count was 0, else `updated`.
4. Save inventory and push `NormalItemUpdateNotify { NoTips = true }` (if `updated` non-empty)
   followed by `NormalItemAddNotify { NoTips = true }` (if `added` non-empty).

### 4.1 Synthesis

| Request | Response | Behaviour |
|---|---|---|
| `SynthesisInfoRequest` (25534) | `SynthesisInfoResponse` (19842) | `Code (#9) = Success`, `LevelInfo (#5) = { Level = 1 }`, `SynthesisInfoList (#11)` = one `OneSynthesisInfo { Id (#1) = f.Id }` per `SynthesisFormula` row |
| `SynthesisItemRequest` (24085) `{ RoleId (#11), Count (#12), InteractEntityId (#13), Id (#14) }` | `SynthesisItemResponse` (24774) | `Count <= 0` or no `SynthesisFormula(Id)` → `ErrSynthesisConfigNotFound`. Takes `ConsumeItems × Count`, grants `Count` of `ItemId`; reply `{ Code (#14) = Success, Id (#11), RoleId, ItemInfos (#10) = [{ ItemId, ItemNum = Count }] }`. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken (R-158). |
| `BatchSynthesisItemRequest` (19088) `{ BatchItems (#14) }` | `BatchSynthesisItemResponse` (26720) | Every `BatchItem { Id (#1), Count (#2) }` needs a formula and `Count > 0` (else `ErrSynthesisConfigNotFound`); the formulas' `ConsumeItems × Count` are taken together (the request's `CostItems` are not trusted), all or none; grants each product; `Items` (#1) = product → count. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken (R-158). |
| `SynthesisLevelRewardRequest` (15075) | `SynthesisLevelRewardResponse` (21274) | Placeholder: `{ Code (#4) = Success, LevelInfo (#10) = { Level = 1 } }` |
| `SynthesisFormulaUnlockRequest` (23511) `{ Id (#4) }` | `SynthesisFormulaUnlockResponse` (23763) | Placeholder: `{ Code (#7) = Success, Id (#8) = Id }` |

### 4.2 Cooking

| Request | Response | Behaviour |
|---|---|---|
| `CookingDataRequest` (24585) | `CookingDataResponse` (26168) | `Code (#9) = Success`, `CookingInfo (#14) = { CookingLevel = 1 }`, `FoodFormulaInfos (#6)` = one `SingleFoodFormulaInfo { Id (#1) = f.Id }` per `CookFormula` row, `ProcessedFoodFormulaInfos (#1)` = one `SingleProcessedFoodFormulaInfo { Id (#1) = p.Id, LockState (#2) = false, Interations (#3) = p.InterationId }` per `CookProcessed` row |
| `CookFoodRequest` (29582) `{ InteractEntityId (#3), CookCount (#10), Id (#13), RoleId (#14) }` | `CookFoodResponse` (23883) | `CookCount <= 0` → `ErrCookingCount`; `CookFormula(Id)` (`ConsumeItems` → `FoodItemId`) or `CookProcessed(Id)` (`ConsumeItemsId` → `FinalItemId`), else `ErrCookingFormulaNotFound`. Takes the ingredients × `CookCount`, grants `CookCount`; reply `{ Code (#5) = Success, Id (#13), RoleId, ItemInfos (#1) }`. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken (R-158). |
| `CookFormulaRequest` (27218) `{ CookFormulaId (#6) }` | `CookFormulaResponse` (23024) | Placeholder: `{ Code (#4) = Success, CookFormulaId (#10) = CookFormulaId }` |

### 4.3 Forging

| Request | Response | Behaviour |
|---|---|---|
| `ForgeInfoRequest` (16907) | `ForgeInfoResponse` (18747) | `Code (#7) = Success`, `ForgeInfoList (#13)` = one `OneForgeInfo { Id (#1) = f.Id }` per `ForgeFormula` row |
| `ForgeItemRequest` (21849) `{ RoleId (#12), Count (#13), Id (#14), InteractEntityId (#15) }` | `ForgeItemResponse` (24992) | `Count <= 0` or no `ForgeFormula(Id)` → `ErrForgeConfigNotFound`. Takes `ConsumeItems × Count` (3.8 formulas include 贝币), grants `Count` of `ItemId` (`GrantWeaponOrItem`); reply `{ Code (#11) = Success, Id (#14), RoleId (#12), ItemInfos (#4) }`. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken (R-158). |
| `ForgeFormulaUnlockRequest` (23395) `{ Id (#6) }` | `ForgeFormulaUnlockResponse` (26326) | Placeholder: `{ Code (#4) = Success, Id (#12) = Id }` |

### 4.4 Material replacement and item exchange (R-108)

| Request | Response | Behaviour |
|---|---|---|
| `BatchMaterialReplaceRequest` (25690) `{ ReplaceItems (#11) }` | `BatchMaterialReplaceResponse` (21431) | Each `BatchReplaceItem { TargetReplaceItemId (#1), ConsumeItems (#3) }`: every consumed item and the target are `MaterialReplace` rows of one `GroupId` (else `ErrItemInvalidParams`); `ratio = ExchangeParam(target.ExchangeParam).Target.Item2` (else 2); output `sum(count / ratio)` (the request's `TargetNum` is not trusted), 0 → `ErrItemInvalidParams`. All entries' items are taken together, then the outputs granted. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken. |
| `MaterialReplaceRequest` (26427) `{ ConsumeItemId (#12), TargetReplaceItemId (#13), ConsumeItemNum (#15) }` | `MaterialReplaceResponse` (17042) | Same group and ratio as the batch; takes `ConsumeItemNum`, grants `ConsumeItemNum / ratio` of the target, 0 → `ErrItemInvalidParams`. Reply `{ Code (#12) = Success }`. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken. |
| `ItemExchangeRequest` (16929) `{ ItemId (#8), ExchangeTimes (#14) }` | `ItemExchangeResponse` (24645) | `ExchangeTimes <= 0` → `ErrItemExchageParamError`; the item's `ItemExchangeContent` row (3.8: one row, `Times` 1, per item: 月相 → 星声 1 : 1, 星声 160 → each pass) else `ErrItemExchageConfigNotFound`. Takes `Consume × ExchangeTimes`, grants `GainCount × ExchangeTimes`; reply `{ ErrorCode (#4) = Success, ItemId (#10), ItemCount (#13) = granted }`. Price tiers by times exchanged and the daily/total limits are not kept. All costs of one request add up and must be held (money from the attributes); short → `ErrItemNotEnough`, nothing taken (R-158). |

---

## 5. Gacha / Convene (`gacha_info`, R-23)

Save file: `player/<id>/gacha_info` ([save-format.md](save-format.md) §5.7): `banners` (`{ id,
selected_pool_id, daily_pulls, total_pulls }`) and `buckets` (`{ id, pull_count, pity_four,
guarantee_five, guarantee_four }`).

### 5.1 View types, currencies, and banner visibility (R-23)

`GachaViewInfo.Type` values:

| Type | Kind | Currency `ItemId` | `ResourcesId` | Family rank |
|---:|---|---:|---|---:|
| 1 | Novice | `50001` | `UiItem_NewPlayerGachaPool` | 30 |
| 2 | Featured Resonator | `50002` | `UiItem_RoleUpGachaPool` | 10 |
| 3 | Featured Weapon | `50005` | `UiItem_WeaponGachaPool` | 20 |
| 4 | Standard Resonator | `50001` | `UiItem_BaseGachaPool` | 50 |
| 5 | Standard Weapon | `50001` | `UiItem_BaseGachaPool` | 60 |
| 6 | Beginner's Choice | `50006` if `GachaId == 5` else `50001` | `UiItem_BaseGachaPool` | 40 |
| 7 | Anniversary Resonator | `50002` | `UiItem_RoleUpGachaPool` | 10 |
| 8 | Anniversary Weapon | `50005` | `UiItem_WeaponGachaPool` | 20 |
| 9 | New Voyage Resonator | `50002` | `UiItem_RoleUpGachaPool` | 10 |
| 10 | New Voyage Weapon | `50005` | `UiItem_WeaponGachaPool` | 20 |
| 11 | Collab Resonator | `50007` | `UiItem_RoleUpGachaPool` | 10 (sub-rank 1) |
| 12 | Collab Weapon | `50008` | `UiItem_WeaponGachaPool` | 20 (sub-rank 1) |
| other | Other | `50001` | `UiItem_BaseGachaPool` | 100 |

- **Default pool of a banner**: among `GachaPool` rows with `GachaId == gacha_id`, pick the row
  with smallest `Sort` (tie-break: smallest `Id`).
- **R-23 visibility filter**: `GachaInfoRequest` lists banners whose default pool has
  `GachaViewInfo.Type == 4` (Standard Resonator) or `5` (Standard Weapon), plus the one limited
  resonator and one limited weapon banner the content panel opened (R-160b, [panel.md](panel.md)
  §5); novice and every other banner are omitted. `GachaRequest` for a banner not listed fails
  `ErrGachaIsNotOpen`; the ticket is the banner type's `ItemId` in the table above.
- **Consumes (`GachaConsumes`)**:
  - Type 1 (Novice): `[{ Times = 10, Consume = 8 }]`
  - Type 6 with `GachaId == 5` (Beginner's Choice): `[{ Times = 1, Consume = 1 }]`
  - All others: `[{ Times = 1, Consume = 1 }, { Times = 10, Consume = 10 }]`
- **Display text fallback**: if `SummaryTitle` / `SummaryDescribe` starts with `"Text_"`, use
  the English fallback string for `Type` (e.g. `"Standard Resonator Convene"`, `"Standard Weapon
  Convene"`, `"Featured Resonator Convene"`, `"Featured Weapon Convene"`, `"Novice Convene"`,
  `"Beginner's Choice Convene"`, else `"Convene"` / `"Convene Event"`; featured resonator
  description fallback is `"Every 10 Convenes guarantees a\n4-Star or above item.\nA 5-Star
  Character is guaranteed\nwithin 80 Convenes."`).

### 5.2 `GachaInfoRequest` (25069) → `GachaInfoResponse` (22256)

- **Request**: `Language` (#1, int32; ignored).
- **Behaviour**:
  1. For each row in `Gacha` passing the R-23 filter (§5.1): ensure a `Banner` entry in
     `gacha_info` (initializing `selected_pool_id` to the default pool if 0 or invalid), build
     `GachaPoolInfo` for every `GachaPool` of that `GachaId` that has a `GachaViewInfo` row
     (`Id` #1, `BeginTime` #2 = 0, `EndTime` #3 = 0, `Title` #4, `SummaryDescribe` #5, `UIType`
     #6 = `view.Type`, `ThemeColor` #7, `ShowIdList` #8, `UpList` #9, `PreviewIdList` #10,
     `ComplianceDetail` #11 = `""`), and assemble `GachaInfo` (`Id` #1, `TodayTimes` #2,
     `TotalTimes` #3, `ItemId` #4, `GachaConsumes` #5, `UsePoolId` #6, `Pools` #7, `BeginTime`
     #8 = 0, `EndTime` #9 = 0, `DailyLimitTimes` #10 = 0, `TotalLimitTimes` #11 = 0,
     `ResourcesId` #12). Skip if `Pools` is empty.
  2. Sort `GachaInfos` ascending by `(family_rank, sub_rank, Gacha.Sort, Gacha.Id)`.
  3. Persist `gacha_info`.
  4. Reply `GachaInfoResponse { ErrorCode (#2) = Success, GachaInfos (#5), DailyTotalLeftTimes
     (#10) = -1 }`.

### 5.3 `GachaUsePoolRequest` (29160) → `GachaUsePoolResponse` (19735)

- **Request**: `PoolId` (#1, int32), `GachaId` (#9, int32).
- **Behaviour**: check that a `GachaPool` row exists with `(GachaId, Id == PoolId)`; if not,
  reply `GachaUsePoolResponse { ErrorCode (#1) = ErrGachaPoolConfigNotFound }`. Otherwise update
  `banner.selected_pool_id = PoolId`, persist `gacha_info`, and reply `{ ErrorCode (#1) =
  Success }`.

### 5.4 `GachaPoolDetailRequest` (21264) → `GachaPoolDetailResponse` (18488)

- **Request**: `PoolId` (#7, int32).
- **Behaviour**: look up `view = GachaViewInfo(PoolId)`; if absent, reply
  `GachaPoolDetailResponse { ErrorCode (#3) = ErrGachaPoolConfigNotFound }`.
- An item id matches a `(view.Type, quality)` slot iff:
  - `GachaTextureInfo(item_id)` exists, AND
  - `quality_of(item_id) == quality` (from `RoleInfo.QualityId` or `WeaponConf.QualityId`), AND
  - if `quality == 5`: for resonator/novice/beginner types (`1, 2, 4, 6, 7, 9, 11`), item is in
    `RoleInfo`; for weapon types (`3, 5, 8, 10, 12`), item is in `WeaponConf`.
- Populate `Detail` (#10, `GachaPoolDetail`) with `ErrorCode (#3) = Success`:
  - `Detail` (#1) = `displayText(view.SummaryDescribe, fallback)`
  - `FiveStarRoleList` (#2, repeated `GachaPoolDrop { ItemId (#1), IsUp (#2) }`):
    - featured resonator types (`2, 7, 9, 11`): matching 5-star ids from `ShowIdList` then `UpList` (`IsUp = true`).
    - types `1, 4, 6`: matching 5-star ids from `ShowIdList`, `UpList`, `PreviewIdList` (`IsUp = false`).
    - else empty.
  - `FiveStarWeaponList` (#3):
    - featured weapon types (`3, 8, 10, 12`): matching 5-star ids from `ShowIdList` then `UpList` (`IsUp = true`).
    - featured resonator types (`2, 7, 9, 11`): 5-star resonators from the first `GachaViewInfo`
      with `Type == 4`'s `PreviewIdList` (`IsUp = false`).
    - type `5` (standard weapon): matching 5-star ids from `ShowIdList`, `UpList`, `PreviewIdList` (`IsUp = false`).
    - else empty.
  - `FourStarRoleList` (#4): if featured type (`2, 3, 7, 8, 9, 10, 11, 12`), matching 4-star ids
    from `ShowIdList` then `UpList` (`IsUp = true`); else empty.
  - `FourStarWeaponList` (#5): matching 4-star ids from `PreviewIdList` excluding `ShowIdList`/`UpList`
    when featured (`IsUp = false`); if empty, fall back to all non-trial 4-star `RoleInfo` then
    4-star `WeaponConf` with `GachaTextureInfo` (excluding the same lists).
  - `ThreeStarWeaponList` (#6): matching 3-star ids from `PreviewIdList` (`IsUp = false`); if
    empty, fall back to all 3-star `WeaponConf` with `GachaTextureInfo`.
  - Titles/subtitles: `FiveStarTitle` (#7) = `"5-Star Convene Items"`, `FiveStarDetail` (#8) =
    `"Featured 5-Star"`, `FourStarTitle` (#9) = `"4-Star Convene Items"`, `FourStarDetail` (#10)
    = `"Featured 4-Star"`, `ThreeStarTitle` (#11) = `"3-Star Convene Items"`, `ThreeStarDetail`
    (#12) = `"Available 3-Star"`.

### 5.5 Compiled-in gacha rates

Compiled constants (`rate_denominator = 10000`, so `100` = 1%):

| Constant | Value | Notes |
|---|---:|---|
| `rate_denominator` | `10000` | denominator for all base and soft-pity rates |
| `five_star_base_rate` | `8000` | **80%** base 5-star rate (official rate is 0.8% = `80`) |
| `four_star_base_rate` | `600` | 6.0% base 4-star rate |
| `featured_five_star_win` | `1 / 2` | 50/50 when `guarantee_five == false` |
| `featured_four_star_win` | `1 / 2` | 50/50 when `guarantee_four == false` |
| Normal hard pity | `80` | guaranteed 5-star on pull 80 |
| Normal soft pity force | `79` | rate becomes `10000` at pull >= 79 |
| Normal soft pity start | `66` | phase 1: pulls 66..70, `+400` per pull |
| Normal soft pity phase 2 | `71..75` | `+800` per pull past 70 |
| Normal soft pity phase 3 | `76..78` | `+1000` per pull past 75 |
| Novice hard pity | `50` | guaranteed 5-star on pull 50 (type 1) |
| Novice soft pity | `41..` | `+600` per pull from pull 41, capped at `10000` |
| 4-star hard pity | `10` | guaranteed 4-star when `pity_four + 1 >= 10` (checked after 5-star hard pity) |

### 5.6 `GachaRequest` (19232) → `GachaResponse` (28271)

- **Request**: `GachaId` (#12, int32), `GachaTimes` (#14, int32).
- **Order of operations**:
  1. Reject unless `GachaTimes == 1 || GachaTimes == 10`: reply `GachaResponse { ErrorCode (#5)
     = ErrGachaTimesNonsupport }`.
  2. Ensure banner `GachaId` and pity bucket `rule_group = (Gacha(GachaId)?.RuleGroupId != 0 ?
     RuleGroupId : GachaId)`. Resolve selected pool's `GachaViewInfo`; if missing, reply
     `ErrGachaConfigNotFound`.
  3. Repeat `GachaTimes` times:
     - **Determine rarity** (`pull_number = bucket.pull_count + 1`):
       - If `pull_number >= hard_pity`: rarity = `5`.
       - Else if `bucket.pity_four + 1 >= 10`: rarity = `4`.
       - Else draw uniform `v` in `[0, 10000)` via CSPRNG: `< five_rate` → `5`;
         `< five_rate + 600` → `4`; else `3`.
     - **Choose reward id** (if candidate list is empty, reply `ErrGachaPoolConfigNotFound` and
       abort without saving). Every candidate list keeps only ids with a `GachaTextureInfo` row:
       the client's convene animation looks up each result id in `GachaTextureInfo` and fails with
       a black screen when absent (e.g. non-gacha 4-star weapons `21020053`, `21040053`):
       - Beginner's choice (`Type == 6 && GachaId == 5`) with non-empty `ShowIdList`: always
         `ShowIdList[0]`.
       - Featured resonator 5-star: if `bucket.guarantee_five` or 50/50 win, clear
         `guarantee_five` and pick uniformly from featured 5-star candidates (`ShowIdList` +
         `UpList`, fallback all non-trial 5-star roles with texture); else set `guarantee_five =
         true` and pick uniformly from standard 5-star resonators (fallback to featured if empty).
       - Featured banner 4-star: if `UpList` has matching 4-star candidates, apply
         `guarantee_four` / 50/50 against regular 4-star candidates (`PreviewIdList` excluding
         `ShowIdList`/`UpList`, fallback all 4-star roles + weapons with texture).
       - All other pulls: pick uniformly from matching candidates (`ShowIdList` + `UpList` for
         featured 5-star weapon; `PreviewIdList` with fallback scan for standard/3-star).
     - **Update pity**:
       - If rarity == 5: `bucket.pull_count = 0`, `bucket.pity_four = 0`.
       - Else: `bucket.pull_count += 1`, `bucket.pity_four = (rarity == 4 ? 0 : pity_four + 1)`.
       - `banner.daily_pulls += 1`, `banner.total_pulls += 1`.
     - Append `GachaResult { GachaReward (#1) = { ItemId (#1) = reward_id, ItemCount (#2) = 1 } }`.
  4. Persist `gacha_info`.
  5. Reply `GachaResponse { ErrorCode (#5) = Success, GachaResults (#10) }`.

- Tidelab deducts `GachaTimes` of the banner's ticket first (`ErrItemNotEnough` when short) and
  grants each result (`grantPullReward`).

---

## 6. Tests

- **`shop_bought` round-trip**: load missing file (empty), save multiple entries, reload; malformed
  lines ignored; CRLF accepted.
- **NPC shops**: `ShopInfoRequest` returns every distinct `ShopId` with `Show == true` rows;
  `ShopBuyRequest` enforces `ShopId` match and `LimitNum`, increments `shop_bought`, grants
  weapons vs normal items (capped at 5 for weapons), and skips non-`ItemInfo` ids (R-109).
- **Pay shop (R-159, `paystore_test.go`)**: five shops, Chinese tab names, hidden week card; gift
  price tier, contents given (not the gift item), weekly reset; exchange prices, coral and limits,
  异相 left out; skins owned → sold out and refused, bought → unlocked; Lunite tiers; month card
  (rewards, notify order, daily claim, 180 days, saved, login claim); periods at 04:00 UTC+8; on
  the 3.8 data every listed good resolves and can be given.
- **Manufacture & material replace (R-158)**: strict costs on synthesis, batch synthesis,
  cooking, forging (贝币 from the attribute); replace 3 : 1 by `ExchangeParam`, fewer than the
  ratio refused; item exchange short / paid; every mutation persists through snapshot+log (R-108) and
  pushes `NormalItemUpdateNotify` before `NormalItemAddNotify` with `NoTips = true`.
- **Gacha**: `GachaInfoRequest` returns only types 4 and 5 (R-23) sorted by family/sort/id;
  `GachaUsePoolRequest` rejects mismatched pools; `GachaRequest` rejects `GachaTimes` not in
  `{1, 10}`, respects hard pity (80 / 50), 4-star pity (10), soft-pity curve, 50/50 guarantee
  flags, and persists `gacha_info`.
