// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

// The Buff columns named bXxx in the game's data are unexported in Go; these read them.

func (b *Buff) ClearStackOnOverflow() bool         { return b.bClearStackOnOverflow }
func (b *Buff) DenyOverflowApplication() bool      { return b.bDenyOverflowApplication }
func (b *Buff) DurationAffectedByBulletTime() bool { return b.bDurationAffectedByBulletTime }
func (b *Buff) ExecutePeriodicEffectOnApplication() bool {
	return b.bExecutePeriodicEffectOnApplication
}
func (b *Buff) OnlyLocalAdd() bool { return b.bOnlyLocalAdd }
func (b *Buff) RequireModifierSuccessToTriggerCues() bool {
	return b.bRequireModifierSuccessToTriggerCues
}
func (b *Buff) SuppressStackingCues() bool { return b.bSuppressStackingCues }

// SetExecutePeriodicEffectOnApplication is for tests that build rows by hand.
func (b *Buff) SetExecutePeriodicEffectOnApplication(v bool) {
	b.bExecutePeriodicEffectOnApplication = v
}
