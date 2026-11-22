// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"google.golang.org/protobuf/proto"

	"tidelab/internal/gateway"
	"tidelab/protocol"
)

// fixed registers a request answered without the player (a fixed or empty reply).
func fixed[T any, PT interface {
	*T
	proto.Message
}, R proto.Message](h *gateway.MessageHandlers, fn func(r PT) R) {
	gateway.OnRequest(h, func(_ *gateway.Session, r PT) proto.Message { return fn(r) })
}

// registerProfileAndCosmetics registers client storage, explore tools and summons, guides and
// the codex, short messages, the battle pass, the profile, cosmetics and the camera, tutorials,
// settings and §11.4's fixed replies (specs/systems.md §2, §5–§7, §9, §11). Ported from our C#
// PlayerSession.ProfileAndCosmetics.cs.
func (g *WorldGame) registerProfileAndCosmetics(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	bad := protocol.ErrorCode_ErrorCode_RequestParamError
	// Client storage (§11.1, R-113)
	reqOn(h, g, func(p *PlayerSession, r *protocol.StorageInfoUpdateRequest) *protocol.StorageInfoUpdateResponse {
		return p.ClientStorage().HandleUpdate(r)
	},
		func(*protocol.StorageInfoUpdateRequest) *protocol.StorageInfoUpdateResponse {
			return &protocol.StorageInfoUpdateResponse{ErrorCode: ok}
		})

	// Explore tools & echo projector summons (§5)
	reqOn(h, g, VisionExploreSkillSet, func(*protocol.VisionExploreSkillSetRequest) *protocol.VisionExploreSkillSetResponse {
		return &protocol.VisionExploreSkillSetResponse{ErrCode: ok}
	})
	reqOn(h, g, ExploreSkillRouletteSet, func(*protocol.ExploreSkillRouletteSetRequest) *protocol.ExploreSkillRouletteSetResponse {
		return &protocol.ExploreSkillRouletteSetResponse{ErrCode: ok}
	})
	reqOn(h, g, Summon3, func(*protocol.Summon3Request) *protocol.Summon3Response {
		return &protocol.Summon3Response{ErrorCode: bad}
	})
	reqOn(h, g, RemoveSummonEntity, func(*protocol.RemoveSummonEntityRequest) *protocol.RemoveSummonEntityResponse {
		return &protocol.RemoveSummonEntityResponse{ErrCode: ok}
	})

	// Guides & illustrated codex (§6)
	reqOn(h, g, func(p *PlayerSession, r *protocol.GuideInfoRequest) *protocol.GuideInfoResponse {
		return p.Guides().HandleInfo(r)
	},
		func(*protocol.GuideInfoRequest) *protocol.GuideInfoResponse { return &protocol.GuideInfoResponse{} })
	fixed(h, func(*protocol.GuideTriggerRequest) *protocol.GuideTriggerResponse {
		return &protocol.GuideTriggerResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.GuideFinishRequest) *protocol.GuideFinishResponse {
		return p.Guides().HandleFinish(r)
	},
		func(*protocol.GuideFinishRequest) *protocol.GuideFinishResponse {
			return &protocol.GuideFinishResponse{ErrorCode: ok}
		})
	reqOn(h, g, IllustratedInfo, func(*protocol.IllustratedInfoRequest) *protocol.IllustratedInfoResponse {
		return &protocol.IllustratedInfoResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.IllustratedReadRequest) *protocol.IllustratedReadResponse {
		return &protocol.IllustratedReadResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.IllustratedUnlockRequest) *protocol.IllustratedUnlockResponse {
		return &protocol.IllustratedUnlockResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.IllustratedRedDotRequest) *protocol.IllustratedRedDotResponse {
		return &protocol.IllustratedRedDotResponse{}
	})

	// Short messages (§7.2)
	fixed(h, func(*protocol.ShortMessageInfoRequest) *protocol.ShortMessageInfoResponse { return ShortMessageInfo() })
	fixed(h, func(*protocol.ShortMessageReadRequest) *protocol.ShortMessageReadResponse {
		return &protocol.ShortMessageReadResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ShortMessageReceivedRequest) *protocol.ShortMessageReceivedResponse {
		return &protocol.ShortMessageReceivedResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ShortMessageReplyRequest) *protocol.ShortMessageReplyResponse {
		return &protocol.ShortMessageReplyResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ShortMessageShowChangeRequest) *protocol.ShortMessageShowChangeResponse {
		return &protocol.ShortMessageShowChangeResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ShortMessageUpdateRequest) *protocol.ShortMessageUpdateResponse {
		return &protocol.ShortMessageUpdateResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ShortMessageChatCharmChangeRequest) *protocol.ShortMessageChatCharmChangeResponse {
		return &protocol.ShortMessageChatCharmChangeResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ShortMessageChatThemeChangeRequest) *protocol.ShortMessageChatThemeChangeResponse {
		return &protocol.ShortMessageChatThemeChangeResponse{ErrCode: ok}
	})

	// Battle pass (§7.3)
	fixed(h, func(*protocol.BattlePassRequest) *protocol.BattlePassResponse {
		return &protocol.BattlePassResponse{ErrorCode: ok, BattlePass: &protocol.PbBattlePass{InTimeRange: false}}
	})
	fixed(h, func(*protocol.BattlePassTaskRequest) *protocol.BattlePassTaskResponse {
		return &protocol.BattlePassTaskResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.BattlePassTakeRewardRequest) *protocol.BattlePassTakeRewardResponse {
		return &protocol.BattlePassTakeRewardResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.BattlePassTakeAllRewardRequest) *protocol.BattlePassTakeAllRewardResponse {
		return &protocol.BattlePassTakeAllRewardResponse{ErrorCode: ok}
	})
	fixed(h, func(r *protocol.BattlePassTaskTakeRequest) *protocol.BattlePassTaskTakeResponse {
		return &protocol.BattlePassTaskTakeResponse{ErrorCode: ok, Ids: append([]int32(nil), r.Ids...)}
	})
	fixed(h, func(*protocol.BattlePassLevelUpRequest) *protocol.BattlePassLevelUpResponse {
		return &protocol.BattlePassLevelUpResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.BattlePassRecurringTakeRequest) *protocol.BattlePassRecurringTakeResponse {
		return &protocol.BattlePassRecurringTakeResponse{ErrorCode: ok}
	})

	// Player profile (§7.4)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.PlayerHeadRequest) *protocol.PlayerHeadResponse {
		return PlayerHead(p)
	},
		func(*protocol.PlayerHeadRequest) *protocol.PlayerHeadResponse { return &protocol.PlayerHeadResponse{} })
	reqOn(h, g, func(p *PlayerSession, _ *protocol.PlayerTitleRequest) *protocol.PlayerTitleResponse {
		return PlayerTitle(p)
	},
		func(*protocol.PlayerTitleRequest) *protocol.PlayerTitleResponse {
			return &protocol.PlayerTitleResponse{ErrorCode: ok}
		})
	reqOn(h, g, RoleShowListUpdate, func(*protocol.RoleShowListUpdateRequest) *protocol.RoleShowListUpdateResponse {
		return &protocol.RoleShowListUpdateResponse{ErrorCode: ok}
	})
	reqOn(h, g, ChangeCard, func(*protocol.ChangeCardRequest) *protocol.ChangeCardResponse {
		return &protocol.ChangeCardResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.ReadCardRequest) *protocol.ReadCardResponse {
		return &protocol.ReadCardResponse{ErrorCode: ok}
	})
	reqOn(h, g, ChangePlayerTitle, func(*protocol.ChangePlayerTitleRequest) *protocol.ChangePlayerTitleResponse {
		return &protocol.ChangePlayerTitleResponse{ErrorCode: ok}
	})
	reqOn(h, g, ModifyName, func(*protocol.ModifyNameRequest) *protocol.ModifyNameResponse {
		return &protocol.ModifyNameResponse{ErrorCode: ok}
	})
	reqOn(h, g, ChangeHeadPhoto, func(*protocol.ChangeHeadPhotoRequest) *protocol.ChangeHeadPhotoResponse {
		return &protocol.ChangeHeadPhotoResponse{ErrorCode: ok}
	})
	reqOn(h, g, ModifySignature,
		func(*protocol.ModifySignatureRequest) *protocol.ModifySignatureResponse {
			return &protocol.ModifySignatureResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ContainsDirtyWord}
		})
	reqOn(h, g, BirthdayInit, func(*protocol.BirthdayInitRequest) *protocol.BirthdayInitResponse {
		return &protocol.BirthdayInitResponse{ErrorCode: ok}
	})
	reqOn(h, g, BirthdayShowSet, func(*protocol.BirthdayShowSetRequest) *protocol.BirthdayShowSetResponse {
		return &protocol.BirthdayShowSetResponse{ErrorCode: ok}
	})

	// Cosmetics & camera (§9)
	reqOn(h, g, LoadCalabashSkinInfo, func(*protocol.LoadCalabashSkinInfoRequest) *protocol.LoadCalabashSkinInfoResponse {
		return &protocol.LoadCalabashSkinInfoResponse{ErrorCode: ok}
	})
	reqOn(h, g, CalabashSkinTakeOn, func(*protocol.CalabashSkinTakeOnRequest) *protocol.CalabashSkinTakeOnResponse {
		return &protocol.CalabashSkinTakeOnResponse{ErrorCode: ok}
	})
	reqOn(h, g, RoleSkin, func(*protocol.RoleSkinRequest) *protocol.RoleSkinResponse { return &protocol.RoleSkinResponse{} })
	reqOn(h, g, RoleSkinChange, func(*protocol.RoleSkinChangeRequest) *protocol.RoleSkinChangeResponse {
		return &protocol.RoleSkinChangeResponse{ErrorCode: bad}
	})
	reqOn(h, g, LoadEquipInfo, func(*protocol.LoadEquipInfoRequest) *protocol.LoadEquipInfoResponse {
		return &protocol.LoadEquipInfoResponse{ErrorCode: ok}
	})
	reqOn(h, g, EquipSkinTakeOn, func(*protocol.EquipSkinTakeOnRequest) *protocol.EquipSkinTakeOnResponse {
		return &protocol.EquipSkinTakeOnResponse{ErrorCode: bad}
	})
	reqOn(h, g, EquipSkinUnLoad, func(*protocol.EquipSkinUnLoadRequest) *protocol.EquipSkinUnLoadResponse {
		return &protocol.EquipSkinUnLoadResponse{ErrorCode: bad}
	})
	reqOn(h, g, FlySkinWear, func(*protocol.FlySkinWearRequest) *protocol.FlySkinWearResponse {
		return &protocol.FlySkinWearResponse{ErrorCode: bad}
	})
	reqOn(h, g, FlySkinWearAllRole, func(*protocol.FlySkinWearAllRoleRequest) *protocol.FlySkinWearAllRoleResponse {
		return &protocol.FlySkinWearAllRoleResponse{ErrorCode: bad}
	})
	reqOn(h, g, FlySkinUnLoad, func(*protocol.FlySkinUnLoadRequest) *protocol.FlySkinUnLoadResponse {
		return &protocol.FlySkinUnLoadResponse{ErrorCode: bad}
	})
	reqOn(h, g, FlySkinAllUnLoad, func(*protocol.FlySkinAllUnLoadRequest) *protocol.FlySkinAllUnLoadResponse {
		return &protocol.FlySkinAllUnLoadResponse{ErrorCode: ok}
	})
	reqOn(h, g, ChangeOrnament, func(*protocol.ChangeOrnamentRequest) *protocol.ChangeOrnamentResponse {
		return &protocol.ChangeOrnamentResponse{ErrorCode: bad}
	})
	reqOn(h, g, CameraSettingQuery, func(*protocol.CameraSettingQueryRequest) *protocol.CameraSettingQueryResponse {
		return &protocol.CameraSettingQueryResponse{ErrorCode: ok}
	})
	reqOn(h, g, CameraSettingModify, func(*protocol.CameraSettingModifyRequest) *protocol.CameraSettingModifyResponse {
		return &protocol.CameraSettingModifyResponse{ErrorCode: ok}
	})
	reqOn(h, g, SelfPortraitState, func(*protocol.SelfPortraitStateRequest) *protocol.SelfPortraitStateResponse {
		return &protocol.SelfPortraitStateResponse{ErrCode: ok}
	})

	// Tutorials & ButtonIsEnable (§11.2, §11.3)
	fixed(h, func(*protocol.TutorialInfoRequest) *protocol.TutorialInfoResponse {
		return &protocol.TutorialInfoResponse{}
	})
	reqOn(h, g, TutorialUnlock, func(r *protocol.TutorialUnlockRequest) *protocol.TutorialUnlockResponse {
		return &protocol.TutorialUnlockResponse{ErrorCode: ok, UnLockInfo: &protocol.TutorialInfo{Id: r.Id}}
	})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.TutorialReceiveRequest) *protocol.TutorialReceiveResponse {
		return TutorialReceive(p)
	},
		func(*protocol.TutorialReceiveRequest) *protocol.TutorialReceiveResponse {
			return &protocol.TutorialReceiveResponse{ErrorCode: ok, ItemMap: map[int32]int32{3: 20}}
		})
	fixed(h, ButtonIsEnable)

	// Settings, access path, direct train & §11.4's fixed or empty replies
	fixed(h, func(*protocol.InputSettingRequest) *protocol.InputSettingResponse {
		return &protocol.InputSettingResponse{}
	})
	fixed(h, func(*protocol.InputSettingUpdateRequest) *protocol.InputSettingUpdateResponse {
		return &protocol.InputSettingUpdateResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.CommonTouchUiSettingUpdateRequest) *protocol.CommonTouchUiSettingUpdateResponse {
		return &protocol.CommonTouchUiSettingUpdateResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.MobileButtonSettingUpdateRequest) *protocol.MobileButtonSettingUpdateResponse {
		return &protocol.MobileButtonSettingUpdateResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.AccessPathConfigRequest) *protocol.AccessPathConfigResponse {
		return &protocol.AccessPathConfigResponse{}
	})
	fixed(h, func(*protocol.DirectTrainInfoRequest) *protocol.DirectTrainInfoResponse {
		return &protocol.DirectTrainInfoResponse{}
	})
	fixed(h, func(*protocol.ThroughTrainRequest) *protocol.ThroughTrainResponse {
		return &protocol.ThroughTrainResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.ThroughTrainFinishViewRequest) *protocol.ThroughTrainFinishViewResponse {
		return &protocol.ThroughTrainFinishViewResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.PhantomSettingRequest) *protocol.PhantomSettingResponse {
		return &protocol.PhantomSettingResponse{}
	})
	fixed(h, func(r *protocol.PhantomSettingUpdateRequest) *protocol.PhantomSettingUpdateResponse {
		return &protocol.PhantomSettingUpdateResponse{ErrorCode: ok, Setting: r.Setting}
	})
	fixed(h, func(r *protocol.PhantomSettingBatchUpdateRequest) *protocol.PhantomSettingBatchUpdateResponse {
		return &protocol.PhantomSettingBatchUpdateResponse{ErrorCode: ok, Settings: r.Settings}
	})
	fixed(h, func(*protocol.WeekCardInfoRequest) *protocol.WeekCardInfoResponse {
		return &protocol.WeekCardInfoResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.WeekCardRewardRequest) *protocol.WeekCardRewardResponse {
		return &protocol.WeekCardRewardResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.DollSmallMapInfoRequest) *protocol.DollSmallMapInfoResponse {
		return &protocol.DollSmallMapInfoResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.MainPhantomRecommendRequest) *protocol.MainPhantomRecommendResponse {
		return &protocol.MainPhantomRecommendResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.PhantomRefiningRequest) *protocol.PhantomRefiningResponse {
		return &protocol.PhantomRefiningResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.PhantomBatchRefiningRequest) *protocol.PhantomBatchRefiningResponse {
		return &protocol.PhantomBatchRefiningResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.PhantomBatchDirectRefiningRequest) *protocol.PhantomBatchDirectRefiningResponse {
		return &protocol.PhantomBatchDirectRefiningResponse{ErrorCode: ok, DirectRefineWeekTimes: 0}
	})
	fixed(h, func(*protocol.PhBaPlanUsePlanRequest) *protocol.PhBaPlanUsePlanResponse {
		return &protocol.PhBaPlanUsePlanResponse{Code: ok, UsePlan: &protocol.PhBaOneAllSuitPlan{}, TowPlanSame: true}
	})
	fixed(h, func(*protocol.PhBaPlanFindPlanRequest) *protocol.PhBaPlanFindPlanResponse {
		return &protocol.PhBaPlanFindPlanResponse{Code: ok, Plan: &protocol.PhBaOneAllSuitPlan{}, TowPlanSame: true}
	})
	fixed(h, func(*protocol.PhBaPlanSaveUsePlanRequest) *protocol.PhBaPlanSaveUsePlanResponse {
		return &protocol.PhBaPlanSaveUsePlanResponse{Code: ok, TowPlanSame: true}
	})
	fixed(h, func(*protocol.PhBaPlanSetPlanStatusRequest) *protocol.PhBaPlanSetPlanStatusResponse {
		return &protocol.PhBaPlanSetPlanStatusResponse{Code: ok, TowPlanSame: true}
	})
	fixed(h, func(*protocol.PhBaPlanUpdatePlanRequest) *protocol.PhBaPlanUpdatePlanResponse {
		return &protocol.PhBaPlanUpdatePlanResponse{Code: ok}
	})
	fixed(h, func(*protocol.PhBaPlanSetFiveStarSwitchRequest) *protocol.PhBaPlanSetFiveStarSwitchResponse {
		return &protocol.PhBaPlanSetFiveStarSwitchResponse{Code: ok}
	})
	fixed(h, func(*protocol.PhBaPlanBatchOperRequest) *protocol.PhBaPlanBatchOperResponse {
		return &protocol.PhBaPlanBatchOperResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.PhantomBatchOperRequest) *protocol.PhantomBatchOperResponse {
		return &protocol.PhantomBatchOperResponse{ErrCode: ok}
	})
	fixed(h, func(*protocol.ExchangeRewardInfoRequest) *protocol.ExchangeRewardInfoResponse {
		return &protocol.ExchangeRewardInfoResponse{}
	})
	fixed(h, func(*protocol.ValidTimeItemRequest) *protocol.ValidTimeItemResponse {
		return &protocol.ValidTimeItemResponse{}
	})
	fixed(h, func(*protocol.RoleIllustratedInfoRequest) *protocol.RoleIllustratedInfoResponse {
		return &protocol.RoleIllustratedInfoResponse{}
	})
	fixed(h, func(*protocol.InfluenceInfoRequest) *protocol.InfluenceInfoResponse {
		return &protocol.InfluenceInfoResponse{}
	})
	fixed(h, func(*protocol.PhotoMemoryRequest) *protocol.PhotoMemoryResponse {
		return &protocol.PhotoMemoryResponse{}
	})
	fixed(h, func(*protocol.SheriffZoneInfoRequest) *protocol.SheriffZoneInfoResponse {
		return &protocol.SheriffZoneInfoResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.HonamiStoryActivityInfoRequest) *protocol.HonamiStoryActivityInfoResponse {
		return &protocol.HonamiStoryActivityInfoResponse{ErrorCode: ok}
	})
	fixed(h, func(*protocol.GivebackInfoRequest) *protocol.GivebackInfoResponse {
		return &protocol.GivebackInfoResponse{ErrorCode: ok}
	})
}
