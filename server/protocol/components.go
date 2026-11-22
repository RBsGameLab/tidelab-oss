// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package protocol

// EntityComponent is one of the EntityComponentPb oneof cases (*EntityComponentPb_TagComponent
// and the rest), named so code outside this package can pass them around.
type EntityComponent = isEntityComponentPb_Component

// Component wraps one case into an EntityComponentPb.
func Component(c EntityComponent) *EntityComponentPb { return &EntityComponentPb{Component: c} }

// CombatNotify is one of the CombatNotifyData oneof cases.
type CombatNotify = isCombatNotifyData_Message

// Notify is a CombatNotifyData about an entity.
func Notify(entityID int64, m CombatNotify) *CombatNotifyData {
	return &CombatNotifyData{CombatCommon: &CombatCommon{EntityId: entityID}, Message: m}
}

// CombatRequestMessage is one of the CombatRequestData oneof cases.
type CombatRequestMessage = isCombatRequestData_Message
