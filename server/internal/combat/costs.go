// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"tidelab/internal/data"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// Costs (R-158, specs/inventory.md §2.4): what an upgrade takes, as item id → count. Money
// (2/3/4) is paid from the player attributes, every other item from the bag.

// Cost is one item an upgrade takes.
type Cost = data.Pair[int32, int32]

// AddCosts adds m × times to costs, merging equal ids and keeping first-seen order; counts ≤ 0
// are skipped.
func AddCosts(costs []Cost, m *data.Map[int32, int32], times int32) []Cost {
	if m == nil || times <= 0 {
		return costs
	}
	for _, p := range m.Pairs {
		if p.Key <= 0 || p.Value <= 0 {
			continue
		}
		n := p.Value * times
		merged := false
		for i := range costs {
			if costs[i].Key == p.Key {
				costs[i].Value += n
				merged = true
				break
			}
		}
		if !merged {
			costs = append(costs, Cost{Key: p.Key, Value: n})
		}
	}
	return costs
}

// ItemCountOf is how many of id the player holds: money from the attributes, else the bag.
func ItemCountOf(p *save.PlayerData, id int32) int32 {
	if v, _ := MoneyAttr(&p.BasicInfo.Attributes, id); v != nil {
		return *v
	}
	return p.NormalInventory.CountOf(id)
}

// CanPay reports whether the player holds every cost; costs of the same item add up.
func CanPay(p *save.PlayerData, costs []Cost) bool {
	need := map[int32]int64{}
	for _, c := range costs {
		if c.Value > 0 {
			need[c.Key] += int64(c.Value)
		}
	}
	for id, n := range need {
		if int64(ItemCountOf(p, id)) < n {
			return false
		}
	}
	return true
}

// Pay takes costs the player can pay (check CanPay first). Bag items are written in one batch
// and pushed as NormalItemUpdateNotify (NoTips, ItemInfo ids only) for what is left and
// NormalItemRemoveNotify for what is gone; money goes through SetMoney (PlayerAttrNotify).
func Pay(t *data.DataTables, p *save.PlayerData, costs []Cost, sink PushSink) {
	var left []save.ItemCount
	var gone []int32
	for _, c := range costs {
		if c.Value <= 0 {
			continue
		}
		n := max(ItemCountOf(p, c.Key)-c.Value, 0)
		if IsMoney(c.Key) {
			SetMoney(p, c.Key, int64(n), sink)
			continue
		}
		if n == 0 {
			gone = append(gone, c.Key)
		}
		left = append(left, save.ItemCount{ID: c.Key, Count: n})
	}
	if len(left) == 0 {
		return
	}
	p.NormalInventory.SetBatch(left)
	update := &protocol.NormalItemUpdateNotify{NoTips: true}
	for _, it := range left {
		if _, ok := t.ItemInfoById[it.ID]; ok && it.Count > 0 {
			update.NormalItemList = append(update.NormalItemList, &protocol.NormalItem{Id: it.ID, Count: it.Count})
		}
	}
	if len(update.NormalItemList) > 0 {
		sink.Push(update)
	}
	if len(gone) > 0 {
		sink.Push(&protocol.NormalItemRemoveNotify{NormalItemIdList: gone})
	}
}

// Give adds items (a refund): money through SetMoney, bag items in one batch pushed as
// NormalItemAddNotify { NoTips = true } with the counts added (ItemInfo ids only).
func Give(t *data.DataTables, p *save.PlayerData, items []Cost, sink PushSink) {
	var batch []save.ItemCount
	add := &protocol.NormalItemAddNotify{NoTips: true}
	for _, it := range items {
		if it.Value <= 0 {
			continue
		}
		if IsMoney(it.Key) {
			SetMoney(p, it.Key, int64(ItemCountOf(p, it.Key))+int64(it.Value), sink)
			continue
		}
		batch = append(batch, save.ItemCount{ID: it.Key, Count: p.NormalInventory.CountOf(it.Key) + it.Value})
		if _, ok := t.ItemInfoById[it.Key]; ok {
			add.NormalItemList = append(add.NormalItemList, &protocol.NormalItem{Id: it.Key, Count: it.Value})
		}
	}
	if len(batch) > 0 {
		p.NormalInventory.SetBatch(batch)
	}
	if len(add.NormalItemList) > 0 {
		sink.Push(add)
	}
}
