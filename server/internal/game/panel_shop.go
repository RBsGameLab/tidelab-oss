// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"slices"
	"sort"

	"tidelab/internal/data"
	"tidelab/internal/panel"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// R-160b: the content panel over the pay shop and the gacha banners (specs/panel.md §4, §5).

// ----- pay shop -----

// PayGoods are the goods on sale: the catalogue (§3.3 of shops.md) with the panel's overrides:
// a good turned off is left out; price, limit and period replace the catalogue's.
type PayGoods struct {
	Goods []*PayGood
	ByID  map[int32]*PayGood
}

// PayGoodsOf are the goods on sale now.
func PayGoodsOf(d *world.WorldData) *PayGoods {
	cat := PayCatalogOf(d.Tables)
	over := d.Panel.Goods()
	if len(over) == 0 {
		return &PayGoods{Goods: cat.Goods, ByID: cat.ByID}
	}
	out := &PayGoods{ByID: map[int32]*PayGood{}}
	for _, g := range cat.Goods {
		if o, ok := over[g.ID]; ok {
			if o.Off {
				continue
			}
			g = ApplyGood(g, o)
		}
		out.Goods = append(out.Goods, g)
		out.ByID[g.ID] = g
	}
	return out
}

// ApplyGood is a good with an override's price, limit and period.
func ApplyGood(g *PayGood, o panel.Good) *PayGood {
	c := *g
	if o.Price != panel.Auto {
		c.Price = o.Price
	}
	if o.Limit != panel.Auto {
		c.Limit = o.Limit
	}
	if o.Update != panel.Auto {
		c.Update = protocol.PayUpdateType(o.Update)
	}
	return &c
}

func (p *PlayerSession) payGoods() *PayGoods { return PayGoodsOf(p.Data) }

// ----- gacha banners -----

// Gacha slot view types: the limited resonator and weapon banners the panel can open.
var (
	gachaRoleTypes   = []int32{2, 7, 9, 11}
	gachaWeaponTypes = []int32{3, 8, 10, 12}
)

// GachaViewTypeOf is the view type of a banner: its default pool's (smallest Sort, then Id).
func GachaViewTypeOf(t *data.DataTables, gachaID int32) (int32, bool) {
	var best *data.GachaPool
	for _, pl := range t.GachaPool {
		if pl.GachaId != gachaID {
			continue
		}
		if _, ok := t.GachaViewInfoById[pl.Id]; !ok {
			continue
		}
		if best == nil || pl.Sort < best.Sort || pl.Sort == best.Sort && pl.Id < best.Id {
			best = pl
		}
	}
	if best == nil {
		return 0, false
	}
	return t.GachaViewInfoById[best.Id].Type, true
}

// GachaSlotBanners are the banners a gacha slot can open, newest (largest id) first.
func GachaSlotBanners(t *data.DataTables, slot string) []int32 {
	types := gachaRoleTypes
	if slot == panel.GachaWeapon {
		types = gachaWeaponTypes
	} else if slot != panel.GachaRole {
		return nil
	}
	var out []int32
	for _, g := range t.Gacha {
		if typ, ok := GachaViewTypeOf(t, g.Id); ok && slices.Contains(types, typ) {
			out = append(out, g.Id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// GachaListed are the banners GachaInfoRequest lists: R-23's standard resonator and weapon
// banners, then the panel's limited resonator and weapon banners when it opened them.
func GachaListed(d *world.WorldData) []int32 {
	t := d.Tables
	var out []int32
	for _, g := range t.Gacha {
		if typ, ok := GachaViewTypeOf(t, g.Id); ok && IsAllowedPoolType(typ) {
			out = append(out, g.Id)
		}
	}
	for _, slot := range []string{panel.GachaRole, panel.GachaWeapon} {
		if id, ok := d.Panel.Gacha(slot); ok && slices.Contains(GachaSlotBanners(t, slot), id) {
			out = append(out, id)
		}
	}
	return out
}

// GachaCurrency is the ticket a banner of view type pulls with (shops.md §5.1).
func GachaCurrency(viewType, gachaID int32) int32 {
	switch viewType {
	case 2, 7, 9:
		return 50002
	case 3, 8, 10:
		return 50005
	case 6:
		if gachaID == 5 {
			return 50006
		}
	case 11:
		return 50007
	case 12:
		return 50008
	}
	return GachaCurrencyItem
}

// gachaRank orders the banners on the page (shops.md §5.1 family rank).
func gachaRank(viewType int32) int32 {
	switch viewType {
	case 2, 7, 9, 11:
		return 10
	case 3, 8, 10, 12:
		return 20
	case 1:
		return 30
	case 6:
		return 40
	case 4:
		return 50
	case 5:
		return 60
	}
	return 100
}

// PayTabName is a pay shop tab's name as the client shows it (shops.md §3.1).
func PayTabName(t *data.DataTables, shop, tab int32) string {
	for _, row := range t.PayShopTab {
		if row.ShopId == shop && row.TabId == tab {
			return payText(row.Name)
		}
	}
	return ""
}
