// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import "tidelab/internal/combat"

// ItemCount is how many of a normal item the player has; money (贝币, 星声, 月相) comes from
// the player attributes (combat.ItemCountOf, specs/inventory.md §2.3).
func (p *PlayerSession) ItemCount(id int32) int32 {
	return combat.ItemCountOf(p.Combat.Player, id)
}
