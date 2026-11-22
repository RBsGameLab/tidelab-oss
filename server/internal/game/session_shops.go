// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"google.golang.org/protobuf/proto"

	"tidelab/internal/gateway"
	"tidelab/protocol"
)

// The pay shop (paystore*.go), NPC fixed shops, manufacture and gacha requests (specs/shops.md
// §1–§5). Ported from our C# Game/PlayerSession.Shops.cs.

func (s *PlayerSession) Shops() *PlayerShops {
	if s.shops == nil {
		s.shops = NewPlayerShops(s.Files)
	}
	return s.shops
}

func (s *PlayerSession) Gacha() *PlayerGacha {
	if s.gacha == nil {
		s.gacha = NewPlayerGacha(s.Files)
	}
	return s.gacha
}

// PushShopInfo pushes ShopInfoNotify for every shop in ShopFixed (specs/shops.md §2.3).
func (s *PlayerSession) PushShopInfo() {
	s.Sink().Push(&protocol.ShopInfoNotify{VersionStr: "1", ShopList: s.Shops().BuildAllShops(s.Data.Tables)})
}

func (s *PlayerSession) ShopInfo(r *protocol.ShopInfoRequest) *protocol.ShopInfoResponse {
	v := r.VersionStr
	if v == "" {
		v = "1"
	}
	return &protocol.ShopInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, VersionStr: v, ShopList: s.Shops().BuildAllShops(s.Data.Tables)}
}

func (s *PlayerSession) ShopUpdate(r *protocol.ShopUpdateRequest) *protocol.ShopUpdateResponse {
	if info := s.Shops().BuildShop(s.Data.Tables, r.ShopId); info != nil {
		return &protocol.ShopUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Info: info}
	}
	return &protocol.ShopUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrShopIdNotExit}
}

func (s *PlayerSession) ShopBuy(r *protocol.ShopBuyRequest) *protocol.ShopBuyResponse {
	return s.Shops().Buy(s, r)
}

// reqOn registers a request the session answers (none when the player has not entered).
func reqOn[T any, PT interface {
	*T
	proto.Message
}, R proto.Message](h *gateway.MessageHandlers, g *WorldGame, run func(p *PlayerSession, r PT) R, none func(r PT) R) {
	gateway.OnRequest(h, func(ss *gateway.Session, r PT) proto.Message {
		if p := g.Find(ss); p != nil {
			return run(p, r)
		}
		return none(r)
	})
}

func (g *WorldGame) registerShops(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	// Pay shop (§3, R-159)
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayShopInfoRequest) *protocol.PayShopInfoResponse {
		return p.PayShopInfo(r)
	},
		func(*protocol.PayShopInfoRequest) *protocol.PayShopInfoResponse {
			return &protocol.PayShopInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayShopUpdateRequest) *protocol.PayShopUpdateResponse {
		return p.PayShopUpdate(r)
	},
		func(*protocol.PayShopUpdateRequest) *protocol.PayShopUpdateResponse {
			return &protocol.PayShopUpdateResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayShopBuyRequest) *protocol.PayShopBuyResponse {
		return p.PayShopBuy(r)
	},
		func(*protocol.PayShopBuyRequest) *protocol.PayShopBuyResponse {
			return &protocol.PayShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayShopItemUpdateRequest) *protocol.PayShopItemUpdateResponse {
		return p.PayShopItemUpdate(r)
	},
		func(*protocol.PayShopItemUpdateRequest) *protocol.PayShopItemUpdateResponse {
			return &protocol.PayShopItemUpdateResponse{ErrorCode: ok}
		})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PayShopDirectBuyRequest) proto.Message {
		return &protocol.PayShopDirectBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists}
	})
	// 凝刻月相 and the month card (R-159, §3.7–§3.8)
	notExists := protocol.ErrorCode_ErrorCode_ErrPayShopGoodsNotExists
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayInfoRequest) *protocol.PayInfoResponse { return p.PayInfo(r) },
		func(*protocol.PayInfoRequest) *protocol.PayInfoResponse {
			return &protocol.PayInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayItemRequest) *protocol.PayItemResponse { return p.PayItem(r) },
		func(*protocol.PayItemRequest) *protocol.PayItemResponse {
			return &protocol.PayItemResponse{ErrorCode: notExists}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayGiftInfoRequest) *protocol.PayGiftInfoResponse {
		return p.PayGiftInfo(r)
	},
		func(*protocol.PayGiftInfoRequest) *protocol.PayGiftInfoResponse {
			return &protocol.PayGiftInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PayGiftRequest) *protocol.PayGiftResponse { return p.PayGift(r) },
		func(*protocol.PayGiftRequest) *protocol.PayGiftResponse {
			return &protocol.PayGiftResponse{ErrorCode: notExists}
		})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.MonthCardRequest) *protocol.MonthCardResponse { return p.MonthCard() },
		func(*protocol.MonthCardRequest) *protocol.MonthCardResponse {
			return &protocol.MonthCardResponse{ErrorCode: ok, Days: -1}
		})
	// Fixed / NPC shops (§2–§3)
	reqOn(h, g, func(p *PlayerSession, r *protocol.ShopInfoRequest) *protocol.ShopInfoResponse { return p.ShopInfo(r) },
		func(*protocol.ShopInfoRequest) *protocol.ShopInfoResponse {
			return &protocol.ShopInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.ShopUpdateRequest) *protocol.ShopUpdateResponse {
		return p.ShopUpdate(r)
	},
		func(*protocol.ShopUpdateRequest) *protocol.ShopUpdateResponse {
			return &protocol.ShopUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrShopIdNotExit}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.ShopBuyRequest) *protocol.ShopBuyResponse { return p.ShopBuy(r) },
		func(*protocol.ShopBuyRequest) *protocol.ShopBuyResponse {
			return &protocol.ShopBuyResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrShopFixeNotExist}
		})
	// Manufacture: synthesis (§4.1)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.SynthesisInfoRequest) *protocol.SynthesisInfoResponse {
		return SynthesisInfo(p)
	},
		func(*protocol.SynthesisInfoRequest) *protocol.SynthesisInfoResponse {
			return &protocol.SynthesisInfoResponse{Code: ok}
		})
	reqOn(h, g, SynthesisItem, func(*protocol.SynthesisItemRequest) *protocol.SynthesisItemResponse {
		return &protocol.SynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrSynthesisConfigNotFound}
	})
	reqOn(h, g, BatchSynthesisItem, func(*protocol.BatchSynthesisItemRequest) *protocol.BatchSynthesisItemResponse {
		return &protocol.BatchSynthesisItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrSynthesisConfigNotFound}
	})
	reqOn(h, g, SynthesisLevelReward, func(*protocol.SynthesisLevelRewardRequest) *protocol.SynthesisLevelRewardResponse {
		return &protocol.SynthesisLevelRewardResponse{Code: ok}
	})
	reqOn(h, g, SynthesisFormulaUnlock, func(*protocol.SynthesisFormulaUnlockRequest) *protocol.SynthesisFormulaUnlockResponse {
		return &protocol.SynthesisFormulaUnlockResponse{Code: ok}
	})
	// Manufacture: cooking (§4.2)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.CookingDataRequest) *protocol.CookingDataResponse {
		return CookingData(p)
	},
		func(*protocol.CookingDataRequest) *protocol.CookingDataResponse {
			return &protocol.CookingDataResponse{Code: ok}
		})
	reqOn(h, g, CookFood, func(*protocol.CookFoodRequest) *protocol.CookFoodResponse {
		return &protocol.CookFoodResponse{Code: protocol.ErrorCode_ErrorCode_ErrCookingFormulaNotFound}
	})
	reqOn(h, g, CookFormulaUnlock, func(*protocol.CookFormulaRequest) *protocol.CookFormulaResponse {
		return &protocol.CookFormulaResponse{Code: ok}
	})
	// Manufacture: forging (§4.3)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.ForgeInfoRequest) *protocol.ForgeInfoResponse { return ForgeInfo(p) },
		func(*protocol.ForgeInfoRequest) *protocol.ForgeInfoResponse {
			return &protocol.ForgeInfoResponse{Code: ok}
		})
	reqOn(h, g, ForgeItem, func(*protocol.ForgeItemRequest) *protocol.ForgeItemResponse {
		return &protocol.ForgeItemResponse{Code: protocol.ErrorCode_ErrorCode_ErrForgeConfigNotFound}
	})
	reqOn(h, g, ForgeFormulaUnlock, func(*protocol.ForgeFormulaUnlockRequest) *protocol.ForgeFormulaUnlockResponse {
		return &protocol.ForgeFormulaUnlockResponse{Code: ok}
	})
	// Material replacement & item exchange (§4.4, §4.5)
	reqOn(h, g, BatchMaterialReplace, func(*protocol.BatchMaterialReplaceRequest) *protocol.BatchMaterialReplaceResponse {
		return &protocol.BatchMaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemInvalidParams}
	})
	reqOn(h, g, MaterialReplace, func(*protocol.MaterialReplaceRequest) *protocol.MaterialReplaceResponse {
		return &protocol.MaterialReplaceResponse{Code: protocol.ErrorCode_ErrorCode_ErrItemInvalidParams}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.ItemExchangeInfoRequest) proto.Message {
		return &protocol.ItemExchangeInfoResponse{}
	})
	reqOn(h, g, ItemExchange, func(*protocol.ItemExchangeRequest) *protocol.ItemExchangeResponse {
		return &protocol.ItemExchangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrItemExchageParamError}
	})
	// Gacha (§5)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.GachaInfoRequest) *protocol.GachaInfoResponse {
		return p.Gacha().Info(p)
	},
		func(*protocol.GachaInfoRequest) *protocol.GachaInfoResponse {
			return &protocol.GachaInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.GachaUsePoolRequest) *protocol.GachaUsePoolResponse {
		return p.Gacha().UsePool(p, r)
	},
		func(*protocol.GachaUsePoolRequest) *protocol.GachaUsePoolResponse {
			return &protocol.GachaUsePoolResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaConfigNotFound}
		})
	reqOn(h, g, GachaPoolDetail, func(*protocol.GachaPoolDetailRequest) *protocol.GachaPoolDetailResponse {
		return &protocol.GachaPoolDetailResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaPoolConfigNotFound}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.GachaRequest) *protocol.GachaResponse { return p.Gacha().Pull(p, r) },
		func(*protocol.GachaRequest) *protocol.GachaResponse {
			return &protocol.GachaResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrGachaConfigNotFound}
		})
}
