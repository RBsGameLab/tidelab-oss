// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/gateway"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// The dungeon requests (specs/dungeons.md §3): leave, rechallenge, the reward claim, entrances,
// the team screen, and 深塔 (§4). Ported from our C# Game/PlayerSession.Dungeons.cs.

// guard runs fn and logs a panic as a warning (C#'s catch of everything but out-of-memory).
func guard(scope, what string, fn func()) (failed bool) {
	defer func() {
		if r := recover(); r != nil {
			common.Warn(scope, fmt.Sprintf("%s failed: %v", what, r))
			failed = true
		}
	}()
	fn()
	return false
}

func (s *PlayerSession) applyTransferAfter() { s.After(func() { s.ApplyPendingTransfer() }) }

// LeaveInst is LeaveInstRequest (§3.3): plan the exit, reply, then the transfer.
func (s *PlayerSession) LeaveInst(*protocol.LeaveInstRequest) *protocol.LeaveInstResponse {
	guard("dungeon", "LeaveInstRequest", func() {
		if e := s.Quests(); e != nil {
			e.PlanExit()
		}
	})
	s.applyTransferAfter()
	return &protocol.LeaveInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// InstEntranceDetail is InstEntranceDetailRequest (§3.1): the dungeons of an entrance, all open.
func (s *PlayerSession) InstEntranceDetail(r *protocol.InstEntranceDetailRequest) *protocol.InstEntranceDetailResponse {
	return &protocol.InstEntranceDetailResponse{
		ErrorCode:           protocol.ErrorCode_ErrorCode_Success,
		EntranceId:          r.EntranceId,
		InstDetailEntryList: EntranceDetail(s.Data, r.EntranceId),
	}
}

// GetInstExchangeReward is GetInstExchangeRewardRequest (§3.4): the settlement screen's claim.
func (s *PlayerSession) GetInstExchangeReward(r *protocol.GetInstExchangeRewardRequest) *protocol.GetInstExchangeRewardResponse {
	ok := false
	if guard("dungeon", "claiming the dungeon reward", func() { ok = ClaimDungeonReward(s, r.ExchangeCount) }) {
		ok = false
	}
	code := protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound
	if ok {
		code = protocol.ErrorCode_ErrorCode_Success
	}
	return &protocol.GetInstExchangeRewardResponse{ErrorCode: code}
}

// SingleInstRechallenge is SingleInstRechallengeRequest (§3.5): reload the dungeon after the reply.
func (s *PlayerSession) SingleInstRechallenge(*protocol.SingleInstRechallengeRequest) *protocol.SingleInstRechallengeResponse {
	if q := s.QuestState; q == nil || !q.Dungeons.Rechallenge(s) {
		return &protocol.SingleInstRechallengeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound}
	}
	s.applyTransferAfter()
	return &protocol.SingleInstRechallengeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// TeleportDungeonAction is TeleportDungeonActionRequest (§3.6): the team screen confirmed or cancelled.
func (s *PlayerSession) TeleportDungeonAction(r *protocol.TeleportDungeonActionRequest) *protocol.TeleportDungeonActionResponse {
	guard("dungeon", "TeleportDungeonActionRequest", func() {
		if q := s.QuestState; q != nil {
			q.Dungeons.ConfirmTeleport(s, r.IncId, r.IsConfirm)
		}
	})
	s.applyTransferAfter()
	return &protocol.TeleportDungeonActionResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

// enterInstWithQuests is EnterInstRequest with quest data (specs/dungeons.md §3.2): the dungeon
// module enters the dungeon or returns to the open world (R-50c); the reply, then the transfer.
func (s *PlayerSession) enterInstWithQuests(q *PlayerQuests, r *protocol.EnterInstRequest) *protocol.EnterInstResponse {
	if !q.Dungeons.Enter(s, r.InstanceId, int64(r.PosEntityId)) {
		common.Warn("dungeon", fmt.Sprintf("EnterInstRequest to instance %d: unknown instance", r.InstanceId))
		return &protocol.EnterInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound}
	}
	s.applyTransferAfter()
	return &protocol.EnterInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- 深塔 (specs/dungeons.md §4) -----

func (s *PlayerSession) towerInfo() *protocol.TowerInfo {
	if q := s.QuestState; q != nil {
		return q.Tower.Info(s.Data.Tables, s.Clock.Seconds())
	}
	return &protocol.TowerInfo{}
}

func (s *PlayerSession) TowerInfoRequest() *protocol.TowerResponse {
	return &protocol.TowerResponse{TowerInfo: s.towerInfo()}
}

// TowerSeasonUpdate is R-46: MaxUnlockDifficulty 0, or the client re-opens the unlock popup at
// every map refresh.
func (s *PlayerSession) TowerSeasonUpdate() *protocol.TowerSeasonUpdateResponse {
	return &protocol.TowerSeasonUpdateResponse{TowerInfo: s.towerInfo(), MaxUnlockDifficulty: 0}
}

// TowerStart is TowerStartRequest (§4.3): the floor's dungeon, the picked team in the formation
// slot before the transfer (R-86), the reply, then the transfer.
func (s *PlayerSession) TowerStart(r *protocol.TowerStartRequest) *protocol.TowerStartResponse {
	notFound := &protocol.TowerStartResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound}
	q := s.QuestState
	var inst int32
	ok := false
	if q != nil {
		inst, ok = q.Tower.Start(s.Data.Tables, r.TowerConfigId)
	}
	if !ok {
		common.Warn("tower", fmt.Sprintf("TowerStartRequest %d: unknown floor", r.TowerConfigId))
		return notFound
	}
	if !q.Dungeons.Enter(s, inst, 0) {
		common.Warn("tower", fmt.Sprintf("TowerStartRequest %d: entering dungeon %d failed", r.TowerConfigId, inst))
		return notFound
	}
	if q.Tower.UseFormation(s.Combat.Player, r.TowerConfigId, r.Formation) {
		save.SaveFormationInfo(s.Combat.Player)
	}
	s.applyTransferAfter()
	return &protocol.TowerStartResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// TowerReward is TowerRewardRequest (§4.6): the list, else the one index.
func (s *PlayerSession) TowerReward(r *protocol.TowerRewardRequest) *protocol.TowerRewardResponse {
	wanted := r.RewardIndexList
	if len(wanted) == 0 {
		wanted = []int32{r.RewardIndex}
	}
	got := []int32{}
	if q := s.QuestState; q != nil {
		got = q.Tower.ClaimRewards(s, r.Difficulty, wanted)
	}
	code := protocol.ErrorCode_ErrorCode_ErrActivityLock
	if len(got) > 0 {
		code = protocol.ErrorCode_ErrorCode_Success
	}
	return &protocol.TowerRewardResponse{ErrorCode: code, RewardIndexList: got}
}

// registerDungeons registers the dungeon and 深塔 requests.
func (g *WorldGame) registerDungeons(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	notFound := protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.LeaveInstRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.LeaveInstResponse { return p.LeaveInst(r) }, &protocol.LeaveInstResponse{ErrorCode: ok})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.InstEntranceDetailRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.InstEntranceDetailResponse { return p.InstEntranceDetail(r) },
			&protocol.InstEntranceDetailResponse{ErrorCode: ok, EntranceId: r.EntranceId})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.GetInstExchangeRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.GetInstExchangeRewardResponse { return p.GetInstExchangeReward(r) },
			&protocol.GetInstExchangeRewardResponse{ErrorCode: notFound})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.SingleInstRechallengeRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.SingleInstRechallengeResponse { return p.SingleInstRechallenge(r) },
			&protocol.SingleInstRechallengeResponse{ErrorCode: notFound})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.TeleportDungeonActionRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.TeleportDungeonActionResponse { return p.TeleportDungeonAction(r) },
			&protocol.TeleportDungeonActionResponse{Code: ok})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, _ *protocol.TowerRequest) proto.Message {
		return on(g, ss, (*PlayerSession).TowerInfoRequest, &protocol.TowerResponse{TowerInfo: &protocol.TowerInfo{}})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, _ *protocol.TowerSeasonUpdateRequest) proto.Message {
		return on(g, ss, (*PlayerSession).TowerSeasonUpdate, &protocol.TowerSeasonUpdateResponse{TowerInfo: &protocol.TowerInfo{}})
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.TowerStartRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.TowerStartResponse { return p.TowerStart(r) },
			&protocol.TowerStartResponse{ErrorCode: notFound})
	})
	// Placeholders: floor records are kept.
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.TowerResetRequest) proto.Message {
		return &protocol.TowerResetResponse{ErrorCode: ok, TowerConfigId: r.TowerConfigId}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.TowerApplyFloorDataRequest) proto.Message {
		return &protocol.TowerApplyFloorDataResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(ss *gateway.Session, r *protocol.TowerRewardRequest) proto.Message {
		return on(g, ss, func(p *PlayerSession) *protocol.TowerRewardResponse { return p.TowerReward(r) },
			&protocol.TowerRewardResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrActivityLock})
	})
}
