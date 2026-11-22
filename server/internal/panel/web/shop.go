// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"tidelab/internal/common"
	"tidelab/internal/game"
	"tidelab/internal/panel"
)

// R-160b: the pay shop and gacha pages (specs/panel.md §4, §5).

var shopNames = map[int32]string{game.PayShopGifts: "礼包中心", game.PayShopExchange: "海市兑换", game.PayShopSkins: "装扮橱窗"}

var updateNames = []string{"不刷新", "每天", "每周", "每月", "永久"}

type goodValues struct {
	Price  int32 `json:"price"`
	Limit  int32 `json:"limit"`
	Update int32 `json:"update"`
}

type goodJSON struct {
	ID         int32      `json:"id"`
	Shop       int32      `json:"shop"`
	ShopName   string     `json:"shopName"`
	Tab        int32      `json:"tab"`
	TabName    string     `json:"tabName"`
	Item       int32      `json:"item"`
	Name       string     `json:"name"`
	Money      int32      `json:"money"`
	MoneyName  string     `json:"moneyName"`
	Off        bool       `json:"off"`
	Overridden bool       `json:"overridden"`
	Now        goodValues `json:"now"`
	Auto       goodValues `json:"auto"`
}

func (p *Panel) shopList(w http.ResponseWriter, _ *http.Request) {
	t := p.Data.Tables
	over := p.Data.Panel.Goods()
	out := struct {
		Goods       []goodJSON `json:"goods"`
		UpdateNames []string   `json:"updateNames"`
		Changes     []string   `json:"changes"`
	}{Goods: []goodJSON{}, UpdateNames: updateNames, Changes: p.Data.Panel.Changes(30)}
	for _, g := range game.PayCatalogOf(t).Goods {
		j := goodJSON{ID: g.ID, Shop: g.Shop, ShopName: shopNames[g.Shop], Tab: g.Tab, TabName: game.PayTabName(t, g.Shop, g.Tab),
			Item: g.ItemID, Name: p.Names.Item[g.ItemID], Money: g.Money, MoneyName: p.Names.Item[g.Money],
			Auto: goodValues{g.Price, g.Limit, int32(g.Update)}}
		j.Now = j.Auto
		if o, ok := over[g.ID]; ok {
			c := game.ApplyGood(g, o)
			j.Off, j.Overridden, j.Now = o.Off, true, goodValues{c.Price, c.Limit, int32(c.Update)}
		}
		out.Goods = append(out.Goods, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Panel) good(w http.ResponseWriter, r *http.Request) *game.PayGood {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 32)
	g, ok := game.PayCatalogOf(p.Data.Tables).ByID[int32(id)]
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, "没有这个商品")
		return nil
	}
	return g
}

func (p *Panel) shopSet(w http.ResponseWriter, r *http.Request) {
	g := p.good(w, r)
	if g == nil {
		return
	}
	var in struct {
		Off bool `json:"off"`
		goodValues
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	o := panel.Good{Off: in.Off, Price: in.Price, Limit: in.Limit, Update: in.Update}
	// A value equal to the catalogue's stays automatic.
	if o.Price == g.Price {
		o.Price = panel.Auto
	}
	if o.Limit == g.Limit {
		o.Limit = panel.Auto
	}
	if o.Update == int32(g.Update) {
		o.Update = panel.Auto
	}
	if !panel.ValidGood(o) {
		writeError(w, http.StatusBadRequest, "价格至少 1，限购至少 0（0 是不限购）")
		return
	}
	note := p.describeGood(g, o)
	if o == (panel.Good{Price: panel.Auto, Limit: panel.Auto, Update: panel.Auto}) {
		p.goodReset(w, g)
		return
	}
	if err := p.Data.Panel.SetGood(g.ID, o, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) shopReset(w http.ResponseWriter, r *http.Request) {
	if g := p.good(w, r); g != nil {
		p.goodReset(w, g)
	}
}

func (p *Panel) goodReset(w http.ResponseWriter, g *game.PayGood) {
	note := fmt.Sprintf("商品 %d「%s」：恢复默认", g.ID, p.Names.Item[g.ItemID])
	if err := p.Data.Panel.ResetGood(g.ID, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) describeGood(g *game.PayGood, o panel.Good) string {
	s := fmt.Sprintf("商品 %d「%s」：", g.ID, p.Names.Item[g.ItemID])
	if o.Off {
		return s + "下架"
	}
	c := game.ApplyGood(g, o)
	limit := "不限购"
	if c.Limit > 0 {
		limit = fmt.Sprintf("限购 %d", c.Limit)
	}
	return s + fmt.Sprintf("上架，%d %s，%s，%s", c.Price, p.Names.Item[g.Money], limit, updateNames[c.Update])
}

// ----- gacha -----

type bannerJSON struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

type gachaSlotJSON struct {
	Slot    string       `json:"slot"`
	Open    int32        `json:"open"`
	Banners []bannerJSON `json:"banners"`
}

func (p *Panel) gachaList(w http.ResponseWriter, _ *http.Request) {
	t := p.Data.Tables
	out := struct {
		Slots   []gachaSlotJSON `json:"slots"`
		Changes []string        `json:"changes"`
	}{Changes: p.Data.Panel.Changes(30)}
	listed := game.GachaListed(p.Data)
	for _, slot := range []string{panel.GachaRole, panel.GachaWeapon} {
		j := gachaSlotJSON{Slot: slot, Banners: []bannerJSON{}}
		for _, id := range game.GachaSlotBanners(t, slot) {
			j.Banners = append(j.Banners, bannerJSON{id, p.Names.Gacha[id]})
			if slices.Contains(listed, id) {
				j.Open = id
			}
		}
		out.Slots = append(out.Slots, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func gachaSlotName(slot string) string {
	if slot == panel.GachaWeapon {
		return "限定武器池"
	}
	return "限定角色池"
}

func (p *Panel) gachaSet(w http.ResponseWriter, r *http.Request) {
	slot := r.PathValue("slot")
	banners := game.GachaSlotBanners(p.Data.Tables, slot)
	if banners == nil {
		writeError(w, http.StatusNotFound, "没有这个卡池位置")
		return
	}
	var in struct {
		ID int32 `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || !slices.Contains(banners, in.ID) {
		writeError(w, http.StatusBadRequest, "不是这个位置的卡池")
		return
	}
	note := fmt.Sprintf("%s：开启 %d「%s」", gachaSlotName(slot), in.ID, p.Names.Gacha[in.ID])
	if err := p.Data.Panel.SetGacha(slot, in.ID, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Panel) gachaReset(w http.ResponseWriter, r *http.Request) {
	slot := r.PathValue("slot")
	if game.GachaSlotBanners(p.Data.Tables, slot) == nil {
		writeError(w, http.StatusNotFound, "没有这个卡池位置")
		return
	}
	note := gachaSlotName(slot) + "：关闭（恢复默认）"
	if err := p.Data.Panel.ResetGacha(slot, note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	common.Info("panel", note)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
