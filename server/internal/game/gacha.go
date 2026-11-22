// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"

	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

// PlayerGacha is R-23, R-80h, R-108, R-109, R-116: gacha banners (GachaViewInfo.Type 4 and 5
// only), pool selection, pool details, and pulls (player/<id>/gacha_info; specs/shops.md §5).
// Ported from our C# Game/Gacha.cs.
type PlayerGacha struct {
	files   *save.PlayerFiles
	Banners []*GachaBanner
	Buckets []*GachaBucket
}

type GachaBanner struct {
	ID, SelectedPoolID, DailyPulls, TotalPulls int32
}

type GachaBucket struct {
	ID, PullCount, PityFour      int32
	GuaranteeFive, GuaranteeFour bool
}

const (
	GachaCurrencyItem   = 50001
	AfterglowCoralID    = 50003
	OscillatedCoralID   = 50004
	FiveStarBaseRate    = 8000
	FourStarBaseRate    = 600
	GachaEndTimeForever = 2147483647
	GachaUnlimited      = 9999999
)

func NewPlayerGacha(files *save.PlayerFiles) *PlayerGacha {
	g := &PlayerGacha{files: files}
	g.load()
	return g
}

// zonNumber is C#'s ZonHelpers.Int: the field when it is a number, else 0.
func zonNumber(v *zon.Value, name string) int32 {
	if f := v.Field(name); f != nil && f.Kind() == zon.Number {
		n, err := f.AsInt32()
		if err != nil {
			panic(err)
		}
		return n
	}
	return 0
}

func zonFlag(v *zon.Value, name string) bool {
	if f := v.Field(name); f != nil && f.Kind() == zon.Bool {
		b, err := f.AsBool()
		if err != nil {
			panic(err)
		}
		return b
	}
	return false
}

func (g *PlayerGacha) load() {
	g.Banners, g.Buckets = nil, nil
	root := g.files.Read("gacha_info")
	if root == nil || root.Kind() != zon.Struct {
		return
	}
	for _, b := range save.List(root, "banners") {
		if b.Kind() != zon.Struct {
			continue
		}
		g.Banners = append(g.Banners, &GachaBanner{ID: zonNumber(b, "id"), SelectedPoolID: zonNumber(b, "selected_pool_id"),
			DailyPulls: zonNumber(b, "daily_pulls"), TotalPulls: zonNumber(b, "total_pulls")})
	}
	for _, b := range save.List(root, "buckets") {
		if b.Kind() != zon.Struct {
			continue
		}
		g.Buckets = append(g.Buckets, &GachaBucket{ID: zonNumber(b, "id"), PullCount: zonNumber(b, "pull_count"), PityFour: zonNumber(b, "pity_four"),
			GuaranteeFive: zonFlag(b, "guarantee_five"), GuaranteeFour: zonFlag(b, "guarantee_four")})
	}
}

func (g *PlayerGacha) Save() {
	banners := make([]*zon.Value, 0, len(g.Banners))
	for _, b := range g.Banners {
		banners = append(banners, zon.NewStruct(
			zon.F("id", zon.Int(int64(b.ID))), zon.F("selected_pool_id", zon.Int(int64(b.SelectedPoolID))),
			zon.F("daily_pulls", zon.Int(int64(b.DailyPulls))), zon.F("total_pulls", zon.Int(int64(b.TotalPulls)))))
	}
	buckets := make([]*zon.Value, 0, len(g.Buckets))
	for _, b := range g.Buckets {
		buckets = append(buckets, zon.NewStruct(
			zon.F("id", zon.Int(int64(b.ID))), zon.F("pull_count", zon.Int(int64(b.PullCount))), zon.F("pity_four", zon.Int(int64(b.PityFour))),
			zon.F("guarantee_five", zon.BoolValue(b.GuaranteeFive)), zon.F("guarantee_four", zon.BoolValue(b.GuaranteeFour))))
	}
	g.files.Write("gacha_info", zon.NewStruct(zon.F("banners", zon.NewList(banners...)), zon.F("buckets", zon.NewList(buckets...))))
}

func (g *PlayerGacha) GetOrCreateBanner(gachaID, defaultPoolID int32) (*GachaBanner, bool) {
	for _, b := range g.Banners {
		if b.ID == gachaID {
			if b.SelectedPoolID == 0 && defaultPoolID != 0 {
				b.SelectedPoolID = defaultPoolID
				return b, true
			}
			return b, false
		}
	}
	b := &GachaBanner{ID: gachaID, SelectedPoolID: defaultPoolID}
	g.Banners = append(g.Banners, b)
	return b, true
}

func (g *PlayerGacha) GetOrCreateBucket(ruleGroupID int32) (*GachaBucket, bool) {
	for _, b := range g.Buckets {
		if b.ID == ruleGroupID {
			return b, false
		}
	}
	b := &GachaBucket{ID: ruleGroupID}
	g.Buckets = append(g.Buckets, b)
	return b, true
}

// IsAllowedPoolType is R-23: only permanent role (Type 4) and permanent weapon (Type 5) pools.
func IsAllowedPoolType(viewType int32) bool { return viewType == 4 || viewType == 5 }

type poolView struct {
	pool *data.GachaPool
	view *data.GachaViewInfo
}

func (g *PlayerGacha) Info(p *PlayerSession) *protocol.GachaInfoResponse {
	t := p.Data.Tables
	resp := &protocol.GachaInfoResponse{
		ErrorCode:           protocol.ErrorCode_ErrorCode_Success,
		RecordId:            "TidelabGachaRecord_" + strconv.Itoa(int(p.State.PlayerID())),
		DailyTotalLeftTimes: GachaUnlimited,
	}
	dirty := false
	ranks := map[int32]int32{}
	for _, id := range GachaListed(p.Data) {
		ga := t.GachaById[id]
		var matching []poolView
		for _, pl := range t.GachaPool {
			if view, ok := t.GachaViewInfoById[pl.Id]; ok && pl.GachaId == ga.Id {
				matching = append(matching, poolView{pl, view})
			}
		}
		if len(matching) == 0 {
			continue
		}
		viewType, _ := GachaViewTypeOf(t, ga.Id)
		ranks[ga.Id] = gachaRank(viewType)
		defaultPool := matching[0].pool.Id
		banner, bannerChanged := g.GetOrCreateBanner(ga.Id, defaultPool)
		if !slices.ContainsFunc(matching, func(m poolView) bool { return m.pool.Id == banner.SelectedPoolID }) {
			banner.SelectedPoolID = defaultPool
			bannerChanged = true
		}
		_, bucketChanged := g.GetOrCreateBucket(ga.RuleGroupId)
		dirty = dirty || bannerChanged || bucketChanged
		info := &protocol.GachaInfo{
			Id: ga.Id, TodayTimes: banner.DailyPulls, TotalTimes: banner.TotalPulls, ItemId: GachaCurrency(viewType, ga.Id),
			GachaConsumes: []*protocol.GachaConsume{{Times: 1, Consume: 1}, {Times: 10, Consume: 10}},
			UsePoolId:     banner.SelectedPoolID, BeginTime: 0, EndTime: GachaEndTimeForever,
			DailyLimitTimes: GachaUnlimited, TotalLimitTimes: GachaUnlimited, ResourcesId: strconv.Itoa(int(ga.Id)),
		}
		for _, m := range matching {
			info.Pools = append(info.Pools, &protocol.GachaPoolInfo{
				Id: m.pool.Id, BeginTime: 0, EndTime: GachaEndTimeForever, Title: m.view.SummaryTitle, SummaryDescribe: m.view.SummaryDescribe,
				UIType: m.view.Type, ThemeColor: m.view.ThemeColor, ShowIdList: append([]int32(nil), m.view.ShowIdList...),
				UpList: append([]int32(nil), m.view.UpList...), PreviewIdList: append([]int32(nil), m.view.PreviewIdList...),
			})
		}
		resp.GachaInfos = append(resp.GachaInfos, info)
	}
	// R-160b: limited banners first, as the official page (shops.md §5.1 family rank).
	sort.SliceStable(resp.GachaInfos, func(i, j int) bool { return ranks[resp.GachaInfos[i].Id] < ranks[resp.GachaInfos[j].Id] })
	if dirty {
		g.Save()
	}
	return resp
}

func (g *PlayerGacha) UsePool(p *PlayerSession, r *protocol.GachaUsePoolRequest) *protocol.GachaUsePoolResponse {
	t := p.Data.Tables
	ga, ok := t.GachaById[r.GachaId]
	if !ok {
		return &protocol.GachaUsePoolResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaConfigNotFound}
	}
	if pl, ok := t.GachaPoolById[r.PoolId]; !ok || pl.GachaId != r.GachaId {
		return &protocol.GachaUsePoolResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaPoolNotBelongToGacha}
	}
	banner, _ := g.GetOrCreateBanner(r.GachaId, r.PoolId)
	banner.SelectedPoolID = r.PoolId
	g.GetOrCreateBucket(ga.RuleGroupId)
	g.Save()
	return &protocol.GachaUsePoolResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func GachaPoolDetail(p *PlayerSession, r *protocol.GachaPoolDetailRequest) *protocol.GachaPoolDetailResponse {
	t := p.Data.Tables
	view, ok := t.GachaViewInfoById[r.PoolId]
	if !ok {
		return &protocol.GachaPoolDetailResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaPoolConfigNotFound}
	}
	d := &protocol.GachaPoolDetail{Detail: view.SummaryDescribe, FiveStarTitle: "5-Star", FourStarTitle: "4-Star", ThreeStarTitle: "3-Star"}
	drop := func(id int32, up bool) *protocol.GachaPoolDrop { return &protocol.GachaPoolDrop{ItemId: id, IsUp: up} }
	up := map[int32]bool{}
	for _, id := range view.UpList {
		up[id] = true
		if ri, ok := t.RoleInfoById[id]; ok {
			if ri.QualityId >= 5 {
				d.FiveStarRoleList = append(d.FiveStarRoleList, drop(id, true))
			} else if ri.QualityId == 4 {
				d.FourStarRoleList = append(d.FourStarRoleList, drop(id, true))
			}
		} else if wc, ok := t.WeaponConfByItemId[id]; ok {
			if wc.QualityId >= 5 {
				d.FiveStarWeaponList = append(d.FiveStarWeaponList, drop(id, true))
			} else if wc.QualityId == 4 {
				d.FourStarWeaponList = append(d.FourStarWeaponList, drop(id, true))
			}
		}
	}
	for _, ri := range t.RoleInfo {
		if _, main := t.MainRoleConfigById[ri.Id]; ri.RoleType != 1 || ri.IsTrial || main || up[ri.Id] {
			continue
		}
		if ri.QualityId == 5 {
			d.FiveStarRoleList = append(d.FiveStarRoleList, drop(ri.Id, false))
		} else if ri.QualityId == 4 {
			d.FourStarRoleList = append(d.FourStarRoleList, drop(ri.Id, false))
		}
	}
	for _, wc := range t.WeaponConf {
		if up[wc.ItemId] {
			continue
		}
		switch wc.QualityId {
		case 5:
			d.FiveStarWeaponList = append(d.FiveStarWeaponList, drop(wc.ItemId, false))
		case 4:
			d.FourStarWeaponList = append(d.FourStarWeaponList, drop(wc.ItemId, false))
		case 3:
			d.ThreeStarWeaponList = append(d.ThreeStarWeaponList, drop(wc.ItemId, false))
		}
	}
	return &protocol.GachaPoolDetailResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Detail: d}
}

func (g *PlayerGacha) Pull(p *PlayerSession, r *protocol.GachaRequest) *protocol.GachaResponse {
	t := p.Data.Tables
	if r.GachaTimes != 1 && r.GachaTimes != 10 {
		return &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaTimesNonsupport}
	}
	ga, ok := t.GachaById[r.GachaId]
	if !ok {
		return &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaConfigNotFound}
	}
	if !slices.Contains(GachaListed(p.Data), r.GachaId) {
		return &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaIsNotOpen}
	}
	defaultPool := r.GachaId
	for _, pl := range t.GachaPool {
		if pl.GachaId == r.GachaId {
			defaultPool = pl.Id
			break
		}
	}
	banner, _ := g.GetOrCreateBanner(r.GachaId, defaultPool)
	poolID := banner.SelectedPoolID
	if poolID == 0 {
		poolID = defaultPool
	}
	view, ok := t.GachaViewInfoById[poolID]
	if !ok {
		return &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaPoolConfigNotFound}
	}
	viewType, _ := GachaViewTypeOf(t, r.GachaId)
	currency := GachaCurrency(viewType, r.GachaId)
	if p.ItemCount(currency) < r.GachaTimes {
		return &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemNotEnough}
	}
	times := r.GachaTimes
	p.Actions().TakeItem(currency, &times)
	bucket, _ := g.GetOrCreateBucket(ga.RuleGroupId)
	up5, up4, std5, std4, std3 := gachaCandidates(t, view)
	resp := &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for i := int32(0); i < r.GachaTimes; i++ {
		bucket.PullCount++
		bucket.PityFour++
		banner.DailyPulls++
		banner.TotalPulls++
		fiveRate := int32(FiveStarBaseRate)
		if bucket.PullCount >= 80 {
			fiveRate = 10000
		} else if bucket.PullCount > 65 {
			fiveRate = min(10000, FiveStarBaseRate+(bucket.PullCount-65)*600)
		}
		fourRate := int32(FourStarBaseRate)
		if bucket.PityFour >= 10 {
			fourRate = 10000
		} else if bucket.PityFour == 9 {
			fourRate = FourStarBaseRate + 5000
		}
		roll := rand.Int32N(10000)
		var reward int32
		switch {
		case roll < fiveRate:
			bucket.PullCount = 0
			reward = pickStarTier(up5, std5, &bucket.GuaranteeFive, view.Type == 2 || view.Type == 3)
		case roll < fiveRate+fourRate:
			bucket.PityFour = 0
			reward = pickStarTier(up4, std4, &bucket.GuaranteeFour, false)
		default:
			reward = pickRandom(std3)
		}
		resp.GachaResults = append(resp.GachaResults, grantPullReward(p, reward))
	}
	g.Save()
	return resp
}

func qualityOf(t *data.DataTables, id int32) int32 {
	if ri, ok := t.RoleInfoById[id]; ok {
		return ri.QualityId
	}
	if wc, ok := t.WeaponConfByItemId[id]; ok {
		return wc.QualityId
	}
	return 0
}

// gachaCandidates are the rewards a pull of view can give, by tier. R-160d: only items with a
// GachaTextureInfo row: the client's convene animation looks each result up there
// (GachaModel.GetGachaSequencePath) and goes black on one without (21020053, 21040053).
func gachaCandidates(t *data.DataTables, view *data.GachaViewInfo) (up5, up4, std5, std4, std3 []int32) {
	defer func() {
		up5, up4, std5, std4, std3 = showable(t, up5), showable(t, up4), showable(t, std5), showable(t, std4), showable(t, std3)
	}()
	for _, id := range view.UpList {
		switch q := qualityOf(t, id); {
		case q >= 5:
			up5 = append(up5, id)
		case q == 4:
			up4 = append(up4, id)
		}
	}
	if len(up5) == 0 && len(view.ShowIdList) > 0 {
		for _, id := range view.ShowIdList {
			if qualityOf(t, id) >= 5 {
				up5 = append(up5, id)
			}
		}
	}
	if view.Type == 3 || view.Type == 5 {
		for _, wc := range t.WeaponConf {
			switch wc.QualityId {
			case 5:
				std5 = append(std5, wc.ItemId)
			case 4:
				std4 = append(std4, wc.ItemId)
			case 3:
				std3 = append(std3, wc.ItemId)
			}
		}
		return
	}
	for _, ri := range t.RoleInfo {
		if _, main := t.MainRoleConfigById[ri.Id]; ri.RoleType != 1 || ri.IsTrial || main {
			continue
		}
		if ri.QualityId == 5 {
			std5 = append(std5, ri.Id)
		} else if ri.QualityId == 4 {
			std4 = append(std4, ri.Id)
		}
	}
	for _, wc := range t.WeaponConf {
		if wc.QualityId == 4 {
			std4 = append(std4, wc.ItemId)
		} else if wc.QualityId == 3 {
			std3 = append(std3, wc.ItemId)
		}
	}
	return
}

// showable keeps the ids the convene animation can show.
func showable(t *data.DataTables, ids []int32) []int32 {
	out := ids[:0:0]
	for _, id := range ids {
		if _, ok := t.GachaTextureInfoById[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

func pickStarTier(up, std []int32, guarantee *bool, guaranteeRateUp bool) int32 {
	if len(up) > 0 {
		if *guarantee || guaranteeRateUp || len(std) == 0 || rand.IntN(2) == 0 {
			*guarantee = false
			return pickRandom(up)
		}
		*guarantee = true
		return pickRandom(std)
	}
	return pickRandom(std)
}

func pickRandom(pool []int32) int32 {
	if len(pool) == 0 {
		return 21010013
	}
	return pool[rand.IntN(len(pool))]
}

func grantPullReward(p *PlayerSession, rewardID int32) *protocol.GachaResult {
	t := p.Data.Tables
	res := &protocol.GachaResult{GachaReward: &protocol.GachaReward{ItemId: rewardID, ItemCount: 1}}
	if ri, ok := t.RoleInfoById[rewardID]; ok {
		owned := p.Combat.Player.Roles.Has(rewardID)
		p.GrantReward(rewardID, 1)
		if owned && ri.SpilloverItem != nil {
			for _, pr := range ri.SpilloverItem.Pairs {
				if pr.Value > 0 {
					res.TransformRewards = append(res.TransformRewards, &protocol.GachaReward{ItemId: pr.Key, ItemCount: pr.Value})
				}
			}
		}
		p.Actions().GrantItem(AfterglowCoralID, 15, false)
		res.ExtraRewards = append(res.ExtraRewards, &protocol.GachaReward{ItemId: AfterglowCoralID, ItemCount: 15})
		return res
	}
	GrantWeaponOrItem(p, rewardID, 1)
	extra := int32(OscillatedCoralID)
	if wc, ok := t.WeaponConfByItemId[rewardID]; ok && wc.QualityId >= 4 {
		extra = AfterglowCoralID
	}
	p.Actions().GrantItem(extra, 15, false)
	res.ExtraRewards = append(res.ExtraRewards, &protocol.GachaReward{ItemId: extra, ItemCount: 15})
	return res
}
