// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"math"
	"slices"
	"sync"
	"time"

	"tidelab/internal/combat"
	"tidelab/internal/data"
	"tidelab/protocol"
)

// ----- the pay shop (商城), R-159 (specs/shops.md §3) -----
//
// Five shops like the official ones: 特别推荐 (1), 礼包中心 (3), 海市兑换 (4), 装扮橱窗 (6) and
// 凝刻月相 (100). Goods and prices are not in the client; they are ours (the value table, §3.2).
// Gift goods are the client's gift items (ItemInfo ItemType 11, Parameters 2/4 = GiftPackage):
// the client draws gift and skin cards from the package contents (ShopSkinData, GiftPackageDetailsView).

const (
	PayAstrite = combat.MoneyAstrite // 星声
	PayLunite  = combat.MoneyLunite  // 月相
	PayCoralA  = 50004               // 余波珊瑚
	PayCoralO  = 50003               // 残振珊瑚
	PayCoralD  = 50009               // 幻梦珊瑚

	PayShopRecommend = 1
	PayShopGifts     = 3
	PayShopExchange  = 4
	PayShopSkins     = 6
	PayShopRecharge  = 100

	PayMaxBuy      = 9999
	AstritePerYuan = 10 // real-money goods cost 星声: ¥1 = 10 星声 (凝刻月相 60 = ¥6 = 60 星声)

	MonthCardGiftID  = 42    // PayGift 42 = item 50011 月相观测卡 (CommonParam MonthCardPayItemId)
	MonthCardItemID  = 50011 // ItemInfo 50011
	MonthCardPayID   = 15    // Pay 15 com.kurogame.aki.month
	MonthCardYuan    = 30
	MonthCardDays    = 30
	MonthCardMaxDays = 180
	MonthCardDaily   = 90 // 星声 a day
)

// payShops are the shops sent, in the client's PayShop order.
var payShops = []int32{PayShopRecommend, PayShopGifts, PayShopExchange, PayShopSkins, PayShopRecharge}

// payTexts are the names the client shows as they are (PayShopSwitchItem.UpdateView and
// PayShopRecommendSwitchItem.UpdateTitle call SetText, not a lookup), from the 3.8 zh-Hans
// lang_multi_text.db.
var payTexts = map[string]string{
	"PayShopTab_1_1_Name": "时装联合会", "PayShopTab_1_2_Name": "月相观测卡",
	"PayShopTab_3_1_Name": "特惠专区", "PayShopTab_3_2_Name": "等级礼包", "PayShopTab_3_3_Name": "常驻礼包", "PayShopTab_3_5_Name": "新旅礼包",
	"PayShopTab_4_1_Name": "余波珊瑚", "PayShopTab_4_2_Name": "残振珊瑚", "PayShopTab_4_3_Name": "聆听星声", "PayShopTab_4_4_Name": "幻梦珊瑚",
	"PayShopTab_5_1_Name": "深塔兑换所", "PayShopTab_5_2_Name": "演练商店", "PayShopTab_5_3_Name": "试炼商店", "PayShopTab_5_4_Name": "同步商店", "PayShopTab_5_5_Name": "演武商店",
	"PayShopTab_6_1_Name": "服饰橱窗", "PayShopTab_6_2_Name": "换影橱窗", "PayShopTab_6_3_Name": "载具外观", "PayShopTab_6_4_Name": "饰品橱窗",
	"PayShopTab_7_1_Name": "新旅周卡", "PayShopTab_7_2_Name": "新旅礼包", "PayShopTab_7_3_Name": "归旅礼包",
	"PrefabTextItem_3160938884_Text": "月相观测卡", "PrefabTextItem_WeekCard_Text": "周度演算卡",
	"PrefabTextItem_QianxiaoSkinRecommend_Text": "蜜桃冰", "PrefabTextItem_LinnaiSkinRecommend_Text": "薄荷糖",
	"PrefabTextItem_FeixueSkinRecommend_Text": "月沉霜",
}

func payText(key string) string {
	if s, ok := payTexts[key]; ok {
		return s
	}
	return key
}

// PayGood is one sellable PayShopItem.
type PayGood struct {
	ID, Shop, Tab, ItemID, Count int32
	Money, Price                 int32
	Limit                        int32 // 0 = no limit
	Update                       protocol.PayUpdateType
	Quality, Sort                int32
	Skin                         bool   // bought once; sold out when every cosmetic in it is owned
	Image                        string // card picture (StageImage, ShowStageImage); "" = the item icon
}

// PayCatalog is every good, built once per table set.
type PayCatalog struct {
	Goods []*PayGood
	ByID  map[int32]*PayGood
}

var payCatalogs sync.Map // *data.DataTables -> *PayCatalog

func PayCatalogOf(t *data.DataTables) *PayCatalog {
	if c, ok := payCatalogs.Load(t); ok {
		return c.(*PayCatalog)
	}
	c := buildPayCatalog(t)
	payCatalogs.Store(t, c)
	return c
}

// ----- §3.2 the value table: what one unit is worth in 星声 -----

var payTiers = []int32{60, 300, 680, 1280, 1980, 3280, 6480}

func isTicket(id int32) bool { return id >= 50001 && id <= 50008 && id != 50003 && id != 50004 }

// qualityValue is a material's worth by quality 1–5.
func qualityValue(q int32) float64 {
	switch {
	case q <= 1:
		return 5
	case q == 2:
		return 15
	case q == 3:
		return 40
	case q == 4:
		return 120
	}
	return 400
}

// itemValue is one unit's worth in 星声; giftQuality prices role skins (5★ 2480, 4★ 1280).
func itemValue(t *data.DataTables, id, giftQuality int32) float64 {
	switch {
	case id == PayAstrite || id == PayLunite:
		return 1
	case id == ShellCreditID:
		return 1.0 / 5000
	case isTicket(id):
		return 160
	case id == PayCoralA || id == PayCoralD:
		return 20
	case id == PayCoralO:
		return 2
	}
	switch cosmeticKind(t, id) {
	case cosRole:
		if giftQuality >= 5 {
			return 2480
		}
		return 1280
	case cosWeapon:
		return 980
	case cosHead, cosCard:
		return 0 // every head and card is owned
	case cosFly:
		return 680
	case cosMotor:
		return 1280
	case cosOrnament:
		return 480
	}
	if it, ok := t.ItemInfoById[id]; ok {
		return qualityValue(it.QualityId)
	}
	return 0
}

// giftPrice is half the contents' worth, at the nearest Lunite tier (ties go down).
func giftPrice(t *data.DataTables, contents []itemCount, quality int32) int32 {
	var v float64
	for _, c := range contents {
		v += itemValue(t, c.id, quality) * float64(c.count)
	}
	half := v / 2
	best := payTiers[0]
	for _, tier := range payTiers[1:] {
		if math.Abs(float64(tier)-half) < math.Abs(float64(best)-half) {
			best = tier
		}
	}
	return best
}

// GiftContents is a gift item's package (ItemInfo Parameters 2, else 4 -> GiftPackage.Content),
// in id order; nil when the item is no gift.
func GiftContents(t *data.DataTables, itemID int32) []itemCount {
	it, ok := t.ItemInfoById[itemID]
	if !ok || it.Parameters == nil {
		return nil
	}
	pkg, ok := it.Parameters.Get(2)
	if !ok {
		if pkg, ok = it.Parameters.Get(4); !ok {
			return nil
		}
	}
	gp, ok := t.GiftPackageById[pkg]
	if !ok || gp.Content == nil {
		return nil
	}
	var out []itemCount
	for _, p := range gp.Content.Pairs {
		out = append(out, itemCount{p.Key, p.Value})
	}
	slices.SortFunc(out, func(a, b itemCount) int { return int(a.id - b.id) })
	return out
}

// ----- cosmetics in gift packages -----

type cosKind int

const (
	cosNone cosKind = iota
	cosRole
	cosWeapon
	cosFly
	cosOrnament
	cosMotor
	cosHead // a 头像 (PlayerHeadRe; a role skin pack holds RoleSkin.HeadId): every head is unlocked (PlayerHead)
	cosCard // a 名片 (BackgroundCard): every card is unlocked at login (RoleService.BuildBasicInfo)
)

func cosmeticKind(t *data.DataTables, id int32) cosKind {
	if _, ok := t.BackgroundCardById[id]; ok {
		return cosCard
	}
	if _, ok := t.PlayerHeadReById[id]; ok {
		return cosHead
	}
	if _, ok := t.RoleSkinById[id]; ok {
		return cosRole
	}
	if _, ok := t.WeaponSkinById[id]; ok {
		return cosWeapon
	}
	if _, ok := t.FlySkinConfigById[id]; ok {
		return cosFly
	}
	if _, ok := t.OrnamentById[id]; ok {
		return cosOrnament
	}
	if _, ok := t.MotorSkinById[id]; ok {
		return cosMotor
	}
	if id/1000000 == 89 { // motor frames, stickers, decorations (8910…, 8940…)
		return cosMotor
	}
	return cosNone
}

// ----- §3.3 the goods -----

// payGift is one gift-centre good: the gift item, its tab and limit (R-159 decision 5, list in
// specs/shops.md §3.3). Ids are the client's PayGift ids of those items.
type payGift struct {
	id, item, tab int32
	update        protocol.PayUpdateType
}

const (
	payWeekly  = protocol.PayUpdateType_PayUpdateType_Weekly
	payMonthly = protocol.PayUpdateType_PayUpdateType_Monthly
	payForever = protocol.PayUpdateType_PayUpdateType_Forever
	payNone    = protocol.PayUpdateType_PayUpdateType_None
)

var payGiftGoods = []payGift{
	// 特惠专区: small weekly and monthly packs
	{103103001, 51018, 1, payWeekly}, {103103010, 51019, 1, payWeekly}, {103103011, 51020, 1, payWeekly},
	{103103012, 51021, 1, payWeekly}, {103103013, 51022, 1, payWeekly}, {103103014, 51023, 1, payWeekly},
	{103103015, 51024, 1, payWeekly}, {103103016, 51025, 1, payWeekly}, {103103035, 51026, 1, payWeekly},
	{103103007, 51017, 1, payMonthly}, {103103002, 51014, 1, payMonthly},
	// 等级礼包
	{103102003, 52003, 2, payForever}, {103102004, 52004, 2, payForever},
	// 时装联合会
	// (54008 甜咖的金印 and 54011 心月琳琅集 hold chat backgrounds and bubbles, which the server
	// has no system for: not sold.)
	{108100101, 52014, 3, payForever},
	// 常驻礼包
	{103102002, 52002, 4, payForever},
	{103101015, 51006, 4, payMonthly}, {103101016, 51007, 4, payMonthly}, {103103003, 51010, 4, payMonthly},
	{103103004, 51011, 4, payMonthly}, {103103008, 51015, 4, payMonthly}, {103103036, 51027, 4, payMonthly},
	{103103069, 51034, 4, payMonthly}, {103103071, 51039, 4, payMonthly}, {103103072, 51038, 4, payMonthly},
	{103103085, 51048, 4, payMonthly}, {103103086, 51049, 4, payMonthly}, {103103087, 51050, 4, payMonthly},
	{103103088, 51051, 4, payMonthly},
	// 新旅礼包
	{104100101, 51032, 5, payForever}, {104100102, 51033, 5, payForever}, {104100113, 52017, 5, payForever},
	{104100114, 52018, 5, payForever}, {104100115, 52019, 5, payForever},
}

// GiftImage is the client path of a 728-wide gift card picture (Common/Image/IconGiftPack728).
// 礼包中心 draws its big cards from StageImage (PayPackageShopView → PayShopBigItem), small cards
// from ShowStageImage (PayShopItemBase.RefreshTexture); without one the gift item's square icon
// is stretched. The pictures are not in any table: the official server sends them.
func GiftImage(key string) string {
	if key == "" {
		return ""
	}
	n := "T_IconGiftPack728_" + key + "_UI"
	return "/Game/Aki/UI/UIResources/Common/Image/IconGiftPack728/" + n + "." + n
}

// payGiftImages picks each gift's card picture by its theme (specs/shops.md §3.3). Where the
// client has a picture for a gift of the same kind (ItemInfo icon T_IconGiftPack_<k> with a 728
// twin: 057 浮金/溶剂, 058 浮金珍藏), that one; else an unused one, distinct per series.
var payGiftImages = map[int32]string{
	51018: "060", 51019: "063", 51020: "064", 51021: "066", 51022: "070", 51023: "074", 51024: "075",
	51025: "076", 51026: "077", 51017: "078", 51014: "057",
	52003: "079", 52004: "080", 52014: "081", 52002: "082",
	51006: "057", 51007: "083", 51010: "058", 51011: "084", 51015: "058", 51027: "085", 51034: "086",
	51039: "087", 51038: "088", 51048: "089", 51049: "090", 51050: "092", 51051: "090",
	51032: "093", 51033: "058", 52017: "094", 52018: "057", 52019: "086",
}

// MonthCardImage is the month card's gift card picture.
var MonthCardImage = GiftImage("Month")

// paySkinGoods are the 装扮橱窗 goods: role skins (tab 1; ids 1061000xx so that 特别推荐's
// banners, PayShopRecommend.RecommendId, find them), glider (2), motor (3), ornaments (4).
var paySkinGoods = []struct{ id, item, tab int32 }{
	{106100001, 53001, 1}, {106100002, 53002, 1}, {106100003, 53003, 1}, {106100004, 53004, 1},
	{106100005, 53005, 1}, {106100006, 53008, 1}, {106100007, 53009, 1}, {106100008, 53010, 1},
	{106100009, 53011, 1},
	{105100101, 54001, 2},
	{105100102, 54002, 3}, {105100103, 54003, 3}, {105100104, 54004, 3}, {105100105, 54005, 3},
	{105100106, 54006, 3}, {105100107, 54007, 3}, {105100108, 54009, 3}, {105100109, 54010, 3},
	{107100101, 55001, 4}, {107100102, 55002, 4}, {107100103, 55003, 4}, {107100104, 55004, 4},
	{107100105, 55005, 4}, {107100106, 55006, 4}, {107100107, 55007, 4}, {107100108, 55008, 4},
	{107100109, 55009, 4},
}

// skinPrice is a skin pack's price in 月相 by its main cosmetic.
func skinPrice(t *data.DataTables, contents []itemCount, quality int32) int32 {
	best := int32(0)
	for _, c := range contents {
		v := int32(itemValue(t, c.id, quality))
		if cosmeticKind(t, c.id) != cosNone && v > best {
			best = v
		}
	}
	return best
}

// ExchangeGoodsID is a 海市兑换 good's id: (5 + tab) * 10^8 + item.
func ExchangeGoodsID(tab, item int32) int32 { return (5+tab)*100000000 + item }

// ticketGoods are the 聆听星声 goods (tab 3) that sell a gacha ticket for 星声, under the ids
// of CommonParam GachaItemIdToShopId (3.8): the gacha view's exchange button
// (GachaMainView.OpenExChangeTokenView) opens the good of that id, and the client's
// ExchangePopView throws when it is missing (R-168).
var ticketGoods = []struct{ item, id int32 }{
	{50001, 104300003}, {50002, 104300001}, {50005, 104300002}, {50007, 104300004}, {50008, 104300005},
}

// TicketGoodsID is the 聆听星声 good that sells ticket item for 星声 (0 if none).
func TicketGoodsID(item int32) int32 {
	for _, g := range ticketGoods {
		if g.item == item {
			return g.id
		}
	}
	return 0
}

// Monthly limits of exchange materials by quality (decision 7).
var materialMonthlyLimit = map[int32]int32{1: 200, 2: 100, 3: 50, 4: 20, 5: 5}

// isExchangeMaterial: stackable resources (MainTypeId 4, ItemType 1), not 异相 (ShowTypes 47).
func isExchangeMaterial(it *data.ItemInfo) bool {
	return it.MainTypeId == 4 && it.ItemType == 1 && !slices.Contains(it.ShowTypes, 47)
}

func buildPayCatalog(t *data.DataTables) *PayCatalog {
	c := &PayCatalog{ByID: map[int32]*PayGood{}}
	add := func(g *PayGood) {
		if _, dup := c.ByID[g.ID]; dup || g.Price <= 0 {
			return
		}
		c.Goods = append(c.Goods, g)
		c.ByID[g.ID] = g
	}
	quality := func(id int32) int32 {
		if it, ok := t.ItemInfoById[id]; ok {
			return it.QualityId
		}
		return 0
	}
	// 礼包中心 (月相).
	for i, g := range payGiftGoods {
		contents := GiftContents(t, g.item)
		if contents == nil {
			continue
		}
		add(&PayGood{ID: g.id, Shop: PayShopGifts, Tab: g.tab, ItemID: g.item, Count: 1, Money: PayLunite,
			Price: giftPrice(t, contents, quality(g.item)), Limit: 1, Update: g.update, Quality: quality(g.item), Sort: int32(i + 1),
			Image: GiftImage(payGiftImages[g.item])})
	}
	// 装扮橱窗 (月相).
	for i, g := range paySkinGoods {
		contents := GiftContents(t, g.item)
		if contents == nil {
			continue
		}
		add(&PayGood{ID: g.id, Shop: PayShopSkins, Tab: g.tab, ItemID: g.item, Count: 1, Money: PayLunite,
			Price: skinPrice(t, contents, quality(g.item)), Limit: 1, Update: payForever, Quality: quality(g.item), Sort: int32(i + 1), Skin: true})
	}
	// 海市兑换: 聆听星声 (tab 3) sells tickets for 星声; the coral tabs sell tickets and materials.
	for _, g := range ticketGoods {
		if _, ok := t.ItemInfoById[g.item]; ok {
			add(&PayGood{ID: g.id, Shop: PayShopExchange, Tab: 3, ItemID: g.item, Count: 1, Money: PayAstrite,
				Price: 160, Update: payNone, Quality: quality(g.item), Sort: g.item})
		}
	}
	tickets := []int32{50001, 50002, 50005}
	coral := []struct {
		tab, money  int32
		perUnit     float64
		minQ, maxQ  int32
		withTickets bool
	}{{1, PayCoralA, 20, 4, 5, true}, {2, PayCoralO, 2, 2, 4, false}, {4, PayCoralD, 20, 4, 5, true}}
	for _, cr := range coral {
		if cr.withTickets {
			for _, id := range tickets {
				if _, ok := t.ItemInfoById[id]; ok {
					add(&PayGood{ID: ExchangeGoodsID(cr.tab, id), Shop: PayShopExchange, Tab: cr.tab, ItemID: id, Count: 1, Money: cr.money,
						Price: 8, Limit: 5, Update: payMonthly, Quality: quality(id), Sort: id})
				}
			}
		}
		for _, it := range t.ItemInfo {
			if !isExchangeMaterial(it) || it.QualityId < cr.minQ || it.QualityId > cr.maxQ {
				continue
			}
			price := int32(math.Ceil(qualityValue(it.QualityId) / cr.perUnit))
			add(&PayGood{ID: ExchangeGoodsID(cr.tab, it.Id), Shop: PayShopExchange, Tab: cr.tab, ItemID: it.Id, Count: 1, Money: cr.money,
				Price: price, Limit: materialMonthlyLimit[it.QualityId], Update: payMonthly, Quality: it.QualityId, Sort: it.Id})
		}
	}
	return c
}

// ----- §3.4 reset periods: daily 04:00 (UTC+8), weekly Monday 04:00, monthly the 1st 04:00 -----

var payZone = time.FixedZone("UTC+8", 8*60*60)

// PayPeriod is the [begin, end) period of an update type at now; Forever and None never end.
func PayPeriod(u protocol.PayUpdateType, now int64) (begin, end int64) {
	switch u {
	case protocol.PayUpdateType_PayUpdateType_Daily:
		return Cycle(now, 1)
	case protocol.PayUpdateType_PayUpdateType_Weekly:
		return Cycle(now, 7)
	case protocol.PayUpdateType_PayUpdateType_Monthly:
		local := time.Unix(now, 0).In(payZone).Add(-4 * time.Hour)
		b := time.Date(local.Year(), local.Month(), 1, 4, 0, 0, 0, payZone)
		return b.Unix(), b.AddDate(0, 1, 0).Unix()
	}
	return 0, 0
}

// PayDay is the game day of now (days since the schedule epoch, a day starts 04:00 UTC+8).
func PayDay(now int64) int64 {
	return int64(math.Floor(float64(now-ScheduleEpoch) / float64(ScheduleDay)))
}
