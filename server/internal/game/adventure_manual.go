// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"strconv"
	"strings"

	"tidelab/internal/data"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// The adventure manual and the Sola guide's detection (AdventureManualRequest,
// GetDetectionTargetRequest, LivenessRequest, SlashAndTowerInfoRequest,
// WeeklyFrameworkInfoRequest; specs/systems.md §3). Ported from our C# Game/AdventureManual.cs.

const MaxDetectionTargetsPerMap = 64

var manualChapters = []int32{1, 2, 3, 4, 5, 6, 7, 8, 9}

func BuildManualData() *protocol.AdventureManualData {
	return &protocol.AdventureManualData{
		NowChapter:      9,
		ReceivedChapter: 9,
		UnLockChapters:  append([]int32(nil), manualChapters...),
		RewardChapters:  append([]int32(nil), manualChapters...),
	}
}

func AdventureManualInfo(s *PlayerSession) *protocol.AdventureManualResponse {
	unlocks := &protocol.DetectionUnlock{
		DungeonDetectionIds:    readDetectionIDs(s.Data, "DungeonDetection.json"),
		MonsterDetectionIds:    readDetectionIDs(s.Data, "MonsterDetection.json"),
		SilentAreaDetectionIds: readDetectionIDs(s.Data, "SilentAreaDetection.json"),
	}
	return &protocol.AdventureManualResponse{
		Code:                protocol.ErrorCode_ErrorCode_Success,
		DetectionUnlocks:    unlocks,
		AdventureManualData: BuildManualData(),
	}
}

func AdventureManualDataResponse() *protocol.AdventureManualDataResponse {
	return &protocol.AdventureManualDataResponse{Code: protocol.ErrorCode_ErrorCode_Success, AdventureManualData: BuildManualData()}
}

func GetDetectionTarget(s *PlayerSession, r *protocol.GetDetectionTargetRequest) *protocol.GetDetectionTargetResponse {
	resp := &protocol.GetDetectionTargetResponse{Code: protocol.ErrorCode_ErrorCode_Success, Type: r.Type, DetectionId: r.DetectionId}
	if r.Type != protocol.DetectionType_DetectionType_NormalMonster {
		return resp
	}
	arr, ok := s.Data.ReadJSON("MonsterDetection.json").(*data.Array)
	if !ok {
		return resp
	}
	var rowObj *data.Object
	for _, item := range arr.Items {
		if o, ok := item.(*data.Object); ok {
			if id, ok := world.JSONInt(o.Get("Id")); ok && id == r.DetectionId {
				rowObj = o
				break
			}
		}
	}
	if rowObj == nil {
		return resp
	}
	bps := map[string]bool{}
	if bp, ok := rowObj.Get("BlueprintType").(data.String); ok && len(bp) > 0 {
		bps[string(bp)] = true
	}
	if list, ok := rowObj.Get("BlueprintTypeList").(*data.Array); ok {
		for _, e := range list.Items {
			if sv, ok := e.(data.String); ok && len(sv) > 0 {
				bps[string(sv)] = true
			}
		}
	}
	var entityConfigID int64
	switch v := rowObj.Get("EntityConfigId").(type) {
	case data.Number:
		entityConfigID = int64(v)
	case data.String:
		if n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64); err == nil {
			entityConfigID = n
		}
	}
	if len(bps) == 0 && entityConfigID <= 0 {
		return resp
	}
	byMap := map[int32]*protocol.DetectionTargetData{}
	var mapOrder []*protocol.DetectionTargetData
	for _, row := range s.Data.Tables.LevelEntityConfig {
		matches := int64(row.EntityId) == entityConfigID
		if len(bps) > 0 {
			matches = !row.InSleep && bps[row.BlueprintType]
		}
		if !matches {
			continue
		}
		md, ok := byMap[row.MapId]
		if !ok {
			md = &protocol.DetectionTargetData{MapId: row.MapId}
			byMap[row.MapId] = md
			mapOrder = append(mapOrder, md)
		}
		if len(md.TargetIds) < MaxDetectionTargetsPerMap {
			md.TargetIds = append(md.TargetIds, int32(row.EntityId))
		}
	}
	resp.DetectionTargetDatas = mapOrder
	return resp
}

func GetDetectionLabelInfo() *protocol.GetDetectionLabelInfoResponse {
	return &protocol.GetDetectionLabelInfoResponse{UnlockLabelInfo: &protocol.UnlockDetectionLabelInfo{
		UnlockedGuideIds:         []int32{1, 2, 3, 4, 5},
		UnlockedDetectionTextIds: []int32{1, 2, 3, 4, 5},
	}}
}

func Liveness() *protocol.LivenessResponse {
	return &protocol.LivenessResponse{LivenessInfo: &protocol.LivenessInfo{LivenessCount: 100, RewardedLiveness: []int32{1, 2, 3, 4, 5}}}
}

func SlashAndTowerInfo() *protocol.SlashAndTowerInfoResponse {
	return &protocol.SlashAndTowerInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ExistEndlessRecord: false, UpdateSeason: false, SeasonEndTime: 0}
}

func WeeklyFrameworkInfo(s *PlayerSession) *protocol.WeeklyFrameworkInfoResponse {
	begin, end := Cycle(s.Clock.Seconds(), TowerDays)
	return &protocol.WeeklyFrameworkInfoResponse{
		ErrorCode: protocol.ErrorCode_ErrorCode_Success,
		FrameworkInfo: &protocol.WeeklyFrameworkInfo{
			ConfigId: 20010, BeginTime: begin, EndTime: end,
			WorldLevel: s.Combat.Player.BasicInfo.Attributes.CurWorldLevel,
		},
	}
}

func readDetectionIDs(wd *world.WorldData, file string) []int32 {
	var ids []int32
	arr, ok := wd.ReadJSON(file).(*data.Array)
	if !ok {
		return ids
	}
	for _, item := range arr.Items {
		if o, ok := item.(*data.Object); ok {
			if id, ok := world.JSONInt(o.Get("Id")); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
