// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"tidelab/internal/save"
	"tidelab/protocol"
)

// Money items (specs/inventory.md §2.3): 贝币 (2), 星声 (3) and 月相 (4) are player attributes,
// not inventory items. The client reads them only from PlayerAttr (InventoryModel routes every
// EItemId to PlayerInfoModel.GetPlayerMoney: 2 → MoneyGold, 3 → MoneyDiamond, 4 → CashCoin), so
// counting, granting and taking them goes through basic_info.attributes.
const (
	MoneyShellCredit int32 = 2 // 贝币
	MoneyAstrite     int32 = 3 // 星声
	MoneyLunite      int32 = 4 // 月相
)

// MoneyAttr is the attribute holding money item id, or nil for any other item.
func MoneyAttr(a *save.BasicAttributesSave, id int32) (*int32, protocol.PlayerAttrKey) {
	switch id {
	case MoneyShellCredit:
		return &a.Coin, protocol.PlayerAttrKey_PlayerAttrKey_Coin
	case MoneyAstrite:
		return &a.RareCoin, protocol.PlayerAttrKey_PlayerAttrKey_RareCoin
	case MoneyLunite:
		return &a.CashCoin, protocol.PlayerAttrKey_PlayerAttrKey_CashCoin
	}
	return nil, 0
}

// IsMoney reports whether id is kept as a player attribute.
func IsMoney(id int32) bool { return id >= MoneyShellCredit && id <= MoneyLunite }

// SetMoney stores a money attribute (clamped to 0…MaxInt32), saves basic_info and pushes the
// attribute. Returns the stored value.
func SetMoney(player *save.PlayerData, id int32, value int64, sink PushSink) int32 {
	v, key := MoneyAttr(&player.BasicInfo.Attributes, id)
	*v = int32(min(max(value, 0), 1<<31-1))
	save.SaveBasicInfo(player)
	sink.Push(&protocol.PlayerAttrNotify{Attributes: []*protocol.PlayerAttr{{
		Key: key, ValueType: protocol.PlayerAttrType_PlayerAttrType_Int32,
		Value: &protocol.PlayerAttr_Int32Value{Int32Value: *v},
	}}})
	return *v
}
