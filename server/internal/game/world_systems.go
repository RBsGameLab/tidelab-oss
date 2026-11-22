// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/gateway"
	"tidelab/internal/omap"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

// Achievements, the area terminal, map marks, the world map, range access, monster patrol, the
// adventure manual and phonograph music (specs/systems.md §1, §3, §4, §7.5). Ported from our C#
// Game/Achievements.cs, AreaTerminal.cs, MapMarks.cs, WorldMap.cs, Music.cs, Patrol.cs, Ranges.cs
// and PlayerSession.WorldSystems.cs.

// ----- achievements (R-58, §1.1) -----

// PlayerAchievements are saved in player/<id>/achievements as "A <id> <finishTime> <received
// 0|1>" and "G <groupId> <finishTime> <received 0|1>".
type PlayerAchievements struct {
	path       string
	done       omap.Map[int32, AchievementState]
	groupsDone omap.Map[int32, AchievementState]
}

type AchievementState struct {
	FinishTime uint32
	Received   bool
}

func NewPlayerAchievements(files *save.PlayerFiles) *PlayerAchievements {
	a := &PlayerAchievements{path: files.PathOf("achievements")}
	if b, err := os.ReadFile(a.path); err == nil {
		a.Parse(strings.TrimPrefix(string(b), utf8BOM))
	}
	return a
}

func (a *PlayerAchievements) Parse(text string) {
	a.done.Clear()
	a.groupsDone.Clear()
	for _, raw := range strings.Split(text, "\n") {
		f := splitSpaces(trimNet(raw))
		if len(f) < 3 {
			continue
		}
		id, ok := lineInt(f, 1)
		if !ok {
			continue
		}
		t, ok := parseUnsigned(f[2], 32)
		if !ok {
			continue
		}
		st := AchievementState{FinishTime: uint32(t), Received: len(f) > 3 && f[3] == "1"}
		switch f[0] {
		case "A":
			a.done.Set(id, st)
		case "G":
			a.groupsDone.Set(id, st)
		}
	}
}

func (a *PlayerAchievements) Format() string {
	var sb strings.Builder
	for id, s := range a.done.All() {
		fmt.Fprintf(&sb, "A %d %d %s\n", id, s.FinishTime, b01(s.Received))
	}
	for id, s := range a.groupsDone.All() {
		fmt.Fprintf(&sb, "G %d %d %s\n", id, s.FinishTime, b01(s.Received))
	}
	return sb.String()
}

func (a *PlayerAchievements) save() { save.WriteAtomic(a.path, a.Format()) }

// AchievementStars are the stars an achievement counts for: its Level (1..7; 1001+ count as 1).
func AchievementStars(level int32) int32 {
	if level >= 1 && level <= 7 {
		return level
	}
	return 1
}

func (a *PlayerAchievements) Entry(id int32) *protocol.AchievementEntry {
	s := a.done.At(id)
	var cur int32
	if s.FinishTime > 0 {
		cur = 1
	}
	return &protocol.AchievementEntry{Id: id, FinishTime: s.FinishTime, IsReceive: s.Received,
		Progress: &protocol.AchievementProgress{CurProgress: cur, TotalProgress: 1}}
}

func (a *PlayerAchievements) Counts(t *data.DataTables) (stars, finished int32) {
	for id, s := range a.done.All() {
		if s.FinishTime == 0 {
			continue
		}
		finished++
		if row, ok := t.AchievementById[id]; ok {
			stars += AchievementStars(row.Level)
		}
	}
	return
}

func (a *PlayerAchievements) Info(t *data.DataTables) *protocol.AchievementInfoResponse {
	res := &protocol.AchievementInfoResponse{}
	for _, g := range t.AchievementGroup {
		if !g.Enable {
			continue
		}
		gs := a.groupsDone.At(g.Id)
		gi := &protocol.AchievementGroupInfo{AchievementGroupEntry: &protocol.AchievementGroupEntry{Id: g.Id, FinishTime: gs.FinishTime, IsReceive: gs.Received}}
		for _, row := range t.Achievement {
			if row.GroupId == g.Id {
				gi.AchievementEntryList = append(gi.AchievementEntryList, a.Entry(row.Id))
			}
		}
		res.AchievementGroupInfoList = append(res.AchievementGroupInfoList, gi)
	}
	res.StarCount, res.FinishCount = a.Counts(t)
	return res
}

func (a *PlayerAchievements) Update() *protocol.AchievementUpdateResponse {
	res := &protocol.AchievementUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, id := range a.done.Keys() {
		res.AchievementEntryList = append(res.AchievementEntryList, a.Entry(id))
	}
	return res
}

// Finish marks an achievement finished (once) and notifies the client. False for an unknown id.
func (a *PlayerAchievements) Finish(s *PlayerSession, id int32) bool {
	t := s.Data.Tables
	row, ok := t.AchievementById[id]
	if !ok {
		common.Warn("achievement", fmt.Sprintf("achievement %d: not in Achievement.json", id))
		return false
	}
	if a.done.At(id).FinishTime > 0 {
		return true
	}
	now := uint32(max(s.Clock.Seconds(), 1))
	a.done.Set(id, AchievementState{FinishTime: now})
	s.Sink().Push(&protocol.AchievementProgressNotify{AchievementEntry: a.Entry(id)})
	if a.groupFinished(t, row.GroupId) && !a.groupsDone.Has(row.GroupId) {
		a.groupsDone.Set(row.GroupId, AchievementState{FinishTime: now})
		s.Sink().Push(&protocol.AchievementGroupProgressNotify{AchievementGroupEntry: &protocol.AchievementGroupEntry{Id: row.GroupId, FinishTime: now}})
	}
	stars, finished := a.Counts(t)
	s.Sink().Push(&protocol.AchievementCountChangeNotify{StarCount: stars, FinishCount: finished})
	a.save()
	common.Info("achievement", fmt.Sprintf("achievement %d finished (%d finished, %d stars)", id, finished, stars))
	return true
}

func (a *PlayerAchievements) groupFinished(t *data.DataTables, groupID int32) bool {
	for _, row := range t.Achievement {
		if row.GroupId == groupID && a.done.At(row.Id).FinishTime == 0 {
			return false
		}
	}
	return true
}

func AchievementDropOf(t *data.DataTables, id int32, isGroup bool) int32 {
	if isGroup {
		if g, ok := t.AchievementGroupById[id]; ok {
			return g.DropId
		}
		return 0
	}
	row, ok := t.AchievementById[id]
	if !ok {
		return 0
	}
	if row.OverrideDropId > 0 {
		return row.OverrideDropId
	}
	if l, ok := t.AchievementStarLevelByLevel[row.Level]; ok {
		return l.DropId
	}
	return 0
}

func (a *PlayerAchievements) Receive(s *PlayerSession, id int32, isGroup bool, given *[]*protocol.RewardItemInfo) bool {
	m := &a.done
	if isGroup {
		m = &a.groupsDone
	}
	st, ok := m.Get(id)
	if !ok || st.FinishTime == 0 || st.Received {
		return false
	}
	st.Received = true
	m.Set(id, st)
	a.save()
	drop := AchievementDropOf(s.Data.Tables, id, isGroup)
	if drop > 0 {
		s.Actions().GrantDrop(drop, int64(id), 0, given)
	}
	what := ""
	if isGroup {
		what = " group"
		s.Sink().Push(&protocol.AchievementGroupProgressNotify{AchievementGroupEntry: &protocol.AchievementGroupEntry{Id: id, FinishTime: st.FinishTime, IsReceive: true}})
	} else {
		s.Sink().Push(&protocol.AchievementProgressNotify{AchievementEntry: a.Entry(id)})
	}
	common.Info("achievement", fmt.Sprintf("achievement%s %d: reward drop %d", what, id, drop))
	return true
}

// ----- the area terminal (R-50, §1.2) -----

// Every region group in AreaMapGroup is unlocked at login; pinned entries are saved in
// player/<id>/area_terminal_pins.
const (
	AreaTerminalFile         = "area_terminal_pins"
	AreaTerminalFallbackFile = "area_terminal"
)

func ParseAreaPins(text string) []int32 {
	list := []int32{}
	for _, tok := range strings.FieldsFunc(text, isNetSpace) {
		if id, ok := parseSigned(tok, 32); ok && !slices.Contains(list, int32(id)) {
			list = append(list, int32(id))
		}
	}
	return list
}

func LoadAreaPins(files *save.PlayerFiles) []int32 {
	path := files.PathOf(AreaTerminalFile)
	if !common.FileExists(path) {
		path = files.PathOf(AreaTerminalFallbackFile)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return []int32{}
	}
	return ParseAreaPins(strings.TrimPrefix(string(b), utf8BOM))
}

func AreaTerminalInfoNotify(t *data.DataTables, files *save.PlayerFiles) *protocol.AreaTerminalInfoNotify {
	n := &protocol.AreaTerminalInfoNotify{}
	for _, g := range t.AreaMapGroup {
		n.AreaMapGroup = append(n.AreaMapGroup, g.Id)
	}
	n.PinIds = LoadAreaPins(files)
	return n
}

func SetAreaPin(files *save.PlayerFiles, id int32, pin bool) {
	pins := LoadAreaPins(files)
	at := slices.Index(pins, id)
	if pin && at < 0 {
		pins = append(pins, id)
	} else if !pin && at >= 0 {
		pins = slices.Delete(pins, at, at+1)
	}
	var sb strings.Builder
	for _, p := range pins {
		fmt.Fprintf(&sb, "%d ", p)
	}
	save.WriteAtomic(files.PathOf(AreaTerminalFile), sb.String())
}

// ----- map marks (R-51, §1.3) -----

func mapMarkMet(s *PlayerSession, c *data.Condition) bool {
	switch c.Type {
	case "CheckLevel":
		need, ok := conditionParam(c, "Level")
		return !ok || int64(s.Combat.Player.BasicInfo.Attributes.Level) >= need
	case "NewQuestStepState", "NewQuestState":
		id, ok := conditionParam(c, "NewQuestId")
		if !ok {
			return true
		}
		e := s.Quests()
		if e != nil && e.IsFinished(int32(id)) {
			return true
		}
		node, ok := conditionParam(c, "QuestNodeId")
		return ok && e != nil && e.IsNodeDone(int32(id), int32(node))
	}
	return true
}

// MapMarkGroupMet is whether condition group groupID holds; false, false when unknown.
func MapMarkGroupMet(s *PlayerSession, groupID int32) (met, known bool) {
	t := s.Data.Tables
	g, ok := t.ConditionGroupById[groupID]
	if !ok {
		return false, false
	}
	n := 0
	for _, cid := range g.GroupId {
		if c, ok := t.ConditionById[cid]; !ok || mapMarkMet(s, c) {
			n++
		}
	}
	if g.Relation == 1 {
		return n > 0, true
	}
	return n == len(g.GroupId), true
}

// MapMarkInfoNotify is the custom marks plus every system mark whose server-judged show condition holds.
func MapMarkInfoNotify(s *PlayerSession) *protocol.MapMarkInfoNotify {
	n := &protocol.MapMarkInfoNotify{}
	for _, m := range s.WorldMap().CustomMarks {
		n.InfoList = append(n.InfoList, m.ToPb())
	}
	for _, m := range s.Data.Tables.MapMark {
		if m.ShowCondition == 0 {
			continue
		}
		if met, known := MapMarkGroupMet(s, m.ShowCondition); !known || !met {
			continue
		}
		n.ShowMarkIds = append(n.ShowMarkIds, &protocol.MapMarkShowInfo{MarkId: m.MarkId, ShowFlag: 1})
		n.UnlockMarkIds = append(n.UnlockMarkIds, m.MarkId)
	}
	return n
}

// ----- the world map (§4, save-format.md §5.7) -----

const InitialCustomMarkID = 1_000_000

// PlayerWorldMap is the custom marks (player/<id>/map_info), fog unlock, explore progress,
// infrastructure and minimap queries.
type PlayerWorldMap struct {
	files            *save.PlayerFiles
	CustomMarks      []*CustomMark
	TrackedMarkIDs   []int32
	NextCustomMarkID int32
}

// CustomMark keeps the save format's field names: IsTrace holds the wire MapId and MapID the wire
// EntityConfigId (specs/systems.md §4.1).
type CustomMark struct {
	ID                                 int32
	X, Y, Z                            float32
	IsTrace, MarkType, MapID, ConfigID int32
	Locked                             bool
	State                              int32
}

func (m *CustomMark) ToPb() *protocol.MarkPointInfo {
	return &protocol.MarkPointInfo{MarkId: m.ID, PosX: m.X, PosY: m.Y, PosZ: m.Z, ConfigId: m.ConfigID, MarkType: m.MarkType,
		MapId: m.IsTrace, EntityConfigId: m.MapID, IsDisable: m.Locked, State: protocol.MarkState(m.State)}
}

func NewPlayerWorldMap(files *save.PlayerFiles) *PlayerWorldMap {
	w := &PlayerWorldMap{files: files, NextCustomMarkID: InitialCustomMarkID}
	root := files.Read("map_info")
	if root == nil || root.Kind() != zon.Struct {
		return w
	}
	for _, it := range save.List(root, "custom_marks") {
		if it.Kind() != zon.Struct {
			continue
		}
		w.CustomMarks = append(w.CustomMarks, &CustomMark{
			ID: zonNumber(it, "id"), X: save.Float(it, "x", 0), Y: save.Float(it, "y", 0), Z: save.Float(it, "z", 0),
			IsTrace: zonNumber(it, "is_trace"), MarkType: zonNumber(it, "mark_type"), MapID: zonNumber(it, "map_id"),
			ConfigID: zonNumber(it, "config_id"), Locked: zonFlag(it, "locked"), State: zonNumber(it, "state"),
		})
	}
	w.TrackedMarkIDs = zonNumbers(root, "tracked_mark_ids")
	if f := root.Field("next_custom_mark_id"); f != nil && f.Kind() == zon.Number {
		w.NextCustomMarkID = zonNumber(root, "next_custom_mark_id")
	}
	return w
}

// zonNumbers is C#'s ZonHelpers.IntList: a list (or struct) field's numbers.
func zonNumbers(v *zon.Value, name string) []int32 {
	out := []int32{}
	f := v.Field(name)
	if f == nil || (f.Kind() != zon.List && f.Kind() != zon.Struct) {
		return out
	}
	items, err := f.Items()
	if err != nil {
		panic(err)
	}
	for _, it := range items {
		n, err := it.AsInt32()
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

func zonInts(v []int32) *zon.Value {
	items := make([]*zon.Value, len(v))
	for i, x := range v {
		items[i] = zon.Int(int64(x))
	}
	return zon.NewList(items...)
}

func (w *PlayerWorldMap) Save() {
	marks := make([]*zon.Value, 0, len(w.CustomMarks))
	for _, m := range w.CustomMarks {
		marks = append(marks, zon.NewStruct(zon.F("id", zN(m.ID)), zon.F("x", zon.Float32(m.X)), zon.F("y", zon.Float32(m.Y)), zon.F("z", zon.Float32(m.Z)),
			zon.F("is_trace", zN(m.IsTrace)), zon.F("mark_type", zN(m.MarkType)), zon.F("map_id", zN(m.MapID)), zon.F("config_id", zN(m.ConfigID)),
			zon.F("locked", zon.BoolValue(m.Locked)), zon.F("state", zN(m.State))))
	}
	w.files.Write("map_info", zon.NewStruct(zon.F("custom_marks", zon.NewList(marks...)),
		zon.F("tracked_mark_ids", zonInts(w.TrackedMarkIDs)), zon.F("next_custom_mark_id", zN(w.NextCustomMarkID))))
}

func removeInt(list []int32, v int32) ([]int32, bool) {
	if i := slices.Index(list, v); i >= 0 {
		return slices.Delete(list, i, i+1), true
	}
	return list, false
}

func (w *PlayerWorldMap) Trace(s *PlayerSession, r *protocol.MapTraceRequest) *protocol.MapTraceResponse {
	if r.MarkId != 0 && !slices.Contains(w.TrackedMarkIDs, r.MarkId) {
		w.TrackedMarkIDs = append(w.TrackedMarkIDs, r.MarkId)
		w.Save()
	}
	s.Sink().Push(&protocol.MapTraceNotify{MarkId: r.MarkId})
	return &protocol.MapTraceResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MarkId: r.MarkId}
}

func (w *PlayerWorldMap) CancelTrace(s *PlayerSession, r *protocol.MapCancelTraceRequest) *protocol.MapCancelTraceResponse {
	var removed bool
	if w.TrackedMarkIDs, removed = removeInt(w.TrackedMarkIDs, r.MarkId); removed {
		w.Save()
	}
	s.Sink().Push(&protocol.MapCancelTraceNotify{MarkId: r.MarkId})
	return &protocol.MapCancelTraceResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MarkId: r.MarkId}
}

func (w *PlayerWorldMap) TraceInfo() *protocol.MapTraceInfoResponse {
	return &protocol.MapTraceInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MarkIdList: slices.Clone(w.TrackedMarkIDs)}
}

func (w *PlayerWorldMap) AddMark(r *protocol.MapMarkRequest) *protocol.MapMarkResponse {
	req := r.MarkPointRequestInfo
	if req == nil {
		req = &protocol.MarkPointRequestInfo{}
	}
	id := w.NextCustomMarkID
	w.NextCustomMarkID++
	m := &CustomMark{ID: id, X: req.PosX, Y: req.PosY, Z: req.PosZ, ConfigID: req.ConfigId, MarkType: req.MarkType, IsTrace: req.MapId, MapID: req.EntityConfigId}
	w.CustomMarks = append(w.CustomMarks, m)
	if req.IsTrace && !slices.Contains(w.TrackedMarkIDs, id) {
		w.TrackedMarkIDs = append(w.TrackedMarkIDs, id)
	}
	w.Save()
	return &protocol.MapMarkResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, Info: m.ToPb()}
}

func (w *PlayerWorldMap) RemoveMarks(r *protocol.MapRemoveMarkRequest) *protocol.MapRemoveMarkResponse {
	changed := false
	for _, id := range r.MarkIds {
		n := len(w.CustomMarks)
		w.CustomMarks = slices.DeleteFunc(w.CustomMarks, func(m *CustomMark) bool { return m.ID == id })
		changed = changed || len(w.CustomMarks) != n
		var removed bool
		w.TrackedMarkIDs, removed = removeInt(w.TrackedMarkIDs, id)
		changed = changed || removed
	}
	if changed {
		w.Save()
	}
	return &protocol.MapRemoveMarkResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, MarkIds: slices.Clone(r.MarkIds)}
}

func UnlockFieldInfo(s *PlayerSession) *protocol.MapUnlockFieldInfoResponse {
	t := s.Data.Tables
	n := &protocol.MultiMapInfoNotify{}
	for _, m := range t.MultiMap {
		n.UnlockMultiMapId = append(n.UnlockMultiMapId, m.Id)
	}
	for _, b := range t.MapBlockInfo {
		n.UnlockBlockId = append(n.UnlockBlockId, b.BlockId)
	}
	s.Sink().Push(n)
	s.Sink().Push(MapMarkInfoNotify(s))
	resp := &protocol.MapUnlockFieldInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, f := range t.MapFog {
		resp.FieldId = append(resp.FieldId, f.Fog)
	}
	return resp
}

func LastBigSceneMiniMapInfo(s *PlayerSession) *protocol.LastBigSceneMiniMapInfoResponse {
	var pos world.Vec3
	if q := s.QuestState; q != nil && q.Dungeons.Current != nil {
		pos = q.Dungeons.Current.BackPos
	} else if s.Scene != nil {
		pos = s.Scene.PlayerPos
	}
	return &protocol.LastBigSceneMiniMapInfoResponse{Code: protocol.ErrorCode_ErrorCode_Success, PosX: pos.X, PosY: pos.Y, PosZ: pos.Z, PosA: 0}
}

func ExploreProgress(s *PlayerSession, r *protocol.ExploreProgressRequest) *protocol.ExploreProgressResponse {
	t := s.Data.Tables
	resp := &protocol.ExploreProgressResponse{}
	for _, area := range r.AreaIds {
		info := &protocol.AreaExploreInfo{AreaId: area, ExplorePercent: 100}
		// The items are the area's ExploreProgress rows; ExploreScore's keys are percent steps
		// (20…100), not ids (R-165: the client looked up 60/80/100 and logged an error each).
		for _, p := range t.ExploreProgress {
			if p.Area == area {
				info.ExploreProgress = append(info.ExploreProgress, &protocol.OneExploreItem{ExploreProgressId: p.Id, ExplorePercent: 100, CurCount: 1, TotalCount: 1, UnLock: true})
			}
		}
		resp.AreaProgress = append(resp.AreaProgress, info)
	}
	return resp
}

func ExploreProgressReward(s *PlayerSession) *protocol.ExploreProgressRewardResponse {
	resp := &protocol.ExploreProgressRewardResponse{}
	for _, x := range s.Data.Tables.ExploreProgressReward {
		resp.RewardIds = append(resp.RewardIds, x.Id)
	}
	return resp
}

func CountryExploreScoreInfo(s *PlayerSession, r *protocol.CountryExploreScoreInfoRequest) *protocol.CountryExploreScoreInfoResponse {
	t := s.Data.Tables
	resp := &protocol.CountryExploreScoreInfoResponse{CountryExploreScore: 10000}
	for _, area := range t.Area {
		if area.CountryId != r.CountryId {
			continue
		}
		take := &protocol.ExploreProgressRewardTake{AreaId: area.AreaId}
		for _, rw := range t.ExploreProgressReward {
			if rw.Area == area.AreaId {
				take.ExploreProgress = append(take.ExploreProgress, rw.StepId)
			}
		}
		if len(take.ExploreProgress) > 0 {
			resp.ExploreProgressRewardTakes = append(resp.ExploreProgressRewardTakes, take)
		}
	}
	return resp
}

func InfrInfo(s *PlayerSession) *protocol.InfrInfoResponse {
	road := &protocol.InfrRoadPb{}
	for _, r := range s.Data.Tables.InfrRoadBuild {
		if r.DungeonId == world.RoadDungeon {
			road.Roads = append(road.Roads, &protocol.InfrOneRoad{RoadId: r.Id, Status: protocol.InfrStatusPb_InfrStatusPb_InfrStatusComplete})
		}
	}
	return &protocol.InfrInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, InfrInfo: &protocol.InfrPb{RoadInfo: road}}
}

func InfrV2Info(s *PlayerSession) *protocol.InfrV2InfoResponse {
	tree := &protocol.InfrV2TreePb{}
	for _, t := range s.Data.Tables.InfrV2TreeBuild {
		tree.Trees = append(tree.Trees, &protocol.InfrV2OneTree{TreeId: t.Id, Status: protocol.InfrV2StatusPb_InfrV2StatusPb_InfrV2StatusComplete})
	}
	return &protocol.InfrV2InfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, InfrInfo: &protocol.InfrV2Pb{
		FireInfo: &protocol.InfrV2FirePb{FireLevel: 1, FireStatus: protocol.InfrV2StatusPb_InfrV2StatusPb_InfrV2StatusComplete}, TreeInfo: tree,
	}}
}

// DarkCoastInfo (systems.md §4.5.3): the world map asks for dragon pool 3 when it opens, and the
// 3.8 client reads DragonPoolConf without a null check (MingSuModel.RefreshDragonPoolDropItems).
func DarkCoastInfo(r *protocol.DarkCoastInfoRequest) *protocol.DarkCoastInfoResponse {
	return &protocol.DarkCoastInfoResponse{
		ErrorCode:      protocol.ErrorCode_ErrorCode_Success,
		DragonPoolConf: &protocol.DragonPoolConf{DragonPoolId: r.DragonPoolId},
	}
}

// ----- phonograph music (R-52, §7.5; player/<id>/music_info) -----

type PlayerMusic struct {
	files            *save.PlayerFiles
	FavoriteMusicIDs []int32
}

func NewPlayerMusic(files *save.PlayerFiles) *PlayerMusic {
	m := &PlayerMusic{files: files, FavoriteMusicIDs: []int32{}}
	if root := files.Read("music_info"); root != nil && root.Kind() == zon.Struct {
		m.FavoriteMusicIDs = zonNumbers(root, "favorite_music_ids")
	}
	return m
}

func (m *PlayerMusic) Save() {
	m.files.Write("music_info", zon.NewStruct(zon.F("favorite_music_ids", zonInts(m.FavoriteMusicIDs))))
}

func (m *PlayerMusic) Info(t *data.DataTables) *protocol.PhantomMusicInfoResponse {
	resp := &protocol.PhantomMusicInfoResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, CurMusicId: 0, CollectMusicIds: slices.Clone(m.FavoriteMusicIDs)}
	byAlbum := map[int32]*protocol.PhonographAlbumTimeInfo{}
	for _, row := range t.PhonographMusic {
		for _, id := range row.Album {
			album, ok := byAlbum[id]
			if !ok {
				album = &protocol.PhonographAlbumTimeInfo{AlbumId: id}
				byAlbum[id] = album
				resp.AlbumTimeInfos = append(resp.AlbumTimeInfos, album)
			}
			if !slices.Contains(album.MusicIds, row.Id) {
				album.MusicIds = append(album.MusicIds, row.Id)
			}
		}
	}
	return resp
}

// MusicUnlock answers the client's unlock of the phonograph items in its bag (§7.5). Every song is
// already unlocked (Info lists them all), so no song is new: echoing the item ids as AddMusicIds
// made the client show "获得新曲目" at every ride (R-155,
// MotorcycleMusicPlayerController.CheckAndUnlockMusic).
func MusicUnlock(*protocol.PhantomMusicUnlockRequest) *protocol.PhantomMusicUnlockResponse {
	return &protocol.PhantomMusicUnlockResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (m *PlayerMusic) UpdateCollect(r *protocol.PhantomMusicCollectUpdateRequest) *protocol.PhantomMusicCollectUpdateResponse {
	m.FavoriteMusicIDs = append([]int32{}, r.CollectMusicIds...)
	m.Save()
	return &protocol.PhantomMusicCollectUpdateResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

// ----- monster patrols (R-120, §1.5) -----

const PatrolPruneAt = 64

type PatrolState struct {
	Active, Forward bool
	Spline, Point   int32
	PointsReached   uint32
}

// Patrols are the patrols of the scene's monsters, by net id (in the C# server's insertion order).
type Patrols struct{ m omap.Map[int64, PatrolState] }

func (p *Patrols) Count() int { return p.m.Len() }

func (p *Patrols) Get(netID int64) (PatrolState, bool) { return p.m.Get(netID) }

func (p *Patrols) entry(netID int64) PatrolState {
	if s, ok := p.m.Get(netID); ok {
		return s
	}
	return PatrolState{Forward: true, Point: -1}
}

func (p *Patrols) Start(netID int64, forward bool) {
	s := p.entry(netID)
	s.Active, s.Forward = true, forward
	p.m.Set(netID, s)
}

func (p *Patrols) Stop(netID int64) {
	s := p.entry(netID)
	s.Active = false
	p.m.Set(netID, s)
}

func (p *Patrols) ChangeDir(netID int64, forward bool) {
	s := p.entry(netID)
	s.Forward = forward
	p.m.Set(netID, s)
}

func (p *Patrols) PointReached(netID int64, spline, index int32) {
	s := p.entry(netID)
	s.Active, s.Spline, s.Point = true, spline, index
	s.PointsReached++
	p.m.Set(netID, s)
}

func (p *Patrols) Prune(alive func(int64) bool) {
	if p.m.Len() < PatrolPruneAt {
		return
	}
	for _, id := range p.m.Keys() {
		if !alive(id) {
			p.m.Delete(id)
		}
	}
}

func (p *Patrols) IsMonster(s *PlayerSession, netID int64, what string) bool {
	sc := s.Scene
	if sc == nil {
		return false
	}
	p.Prune(func(id int64) bool { return sc.Get(id) != nil })
	e := sc.Get(netID)
	if e == nil {
		return false
	}
	if e.EntityType != world.TypeMonster {
		common.Info("patrol", fmt.Sprintf("%s for entity %d, a %s, not a monster", what, netID, entityTypeName(e.EntityType)))
		return false
	}
	return true
}

// entityTypeName is the C# name of an EEntityType (protoc's: the prefix stripped).
func entityTypeName(t protocol.EEntityType) string {
	return strings.TrimPrefix(t.String(), "EEntityType_")
}

// ----- ranges (R-91, §1.4) -----

func InitRange(r *protocol.InitRangeRequest) *protocol.InitRangeResponse {
	typ := protocol.AccessRangeType_AccessRangeType_RangeInitOut
	if r.IsPlayerInRange {
		typ = protocol.AccessRangeType_AccessRangeType_RangeInit
	}
	resp := &protocol.InitRangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, RangeEntityId: r.RangeEntityId,
		PlayerAccessRangeResult: &protocol.AccessRangeResultMessage{AccessRangeType: typ}}
	for _, id := range r.InRangeEntityIds {
		resp.EntitiesAccessRangeResult = append(resp.EntitiesAccessRangeResult, &protocol.AccessRangeResultMessage{AccessEntityId: id, AccessRangeType: protocol.AccessRangeType_AccessRangeType_RangeInit})
	}
	return resp
}

func EntityAccessRange(r *protocol.EntityAccessRangeRequest) *protocol.EntityAccessRangeResponse {
	resp := &protocol.EntityAccessRangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, RangeEntityId: r.RangeEntityId}
	for _, id := range r.AccessEntityIds {
		resp.EntitiesAccessRangeResult = append(resp.EntitiesAccessRangeResult, &protocol.AccessRangeResultMessage{AccessEntityId: id, AccessRangeType: r.AccessRangeType})
	}
	return resp
}

func PlayerAccessRange(r *protocol.PlayerAccessRangeRequest) *protocol.PlayerAccessRangeResponse {
	return &protocol.PlayerAccessRangeResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, RangeEntityId: r.RangeEntityId,
		AccessRangeResult: &protocol.AccessRangeResultMessage{AccessRangeType: r.AccessRangeType}}
}

// ----- in the session -----

func (s *PlayerSession) Achievements() *PlayerAchievements {
	if s.achievements == nil {
		s.achievements = NewPlayerAchievements(s.Files)
	}
	return s.achievements
}

func (s *PlayerSession) WorldMap() *PlayerWorldMap {
	if s.worldMap == nil {
		s.worldMap = NewPlayerWorldMap(s.Files)
	}
	return s.worldMap
}

func (s *PlayerSession) Music() *PlayerMusic {
	if s.music == nil {
		s.music = NewPlayerMusic(s.Files)
	}
	return s.music
}

func (s *PlayerSession) AchievementFinish(r *protocol.AchievementFinishRequest) *protocol.AchievementFinishResponse {
	if s.Achievements().Finish(s, r.Id) {
		return &protocol.AchievementFinishResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	}
	return &protocol.AchievementFinishResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrAchievementNotClinet}
}

func (s *PlayerSession) AchievementReceive(r *protocol.AchievementReceiveRequest) *protocol.AchievementReceiveResponse {
	var given []*protocol.RewardItemInfo
	if !s.Achievements().Receive(s, r.Id, r.IsGroupId, &given) {
		return &protocol.AchievementReceiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrTriggerConditionNotMet}
	}
	resp := &protocol.AchievementReceiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success, ItemMap: map[int32]int32{}}
	for _, g := range given {
		resp.ItemMap[g.ItemId] += g.Count
	}
	return resp
}

func (s *PlayerSession) MulAchievementReceive(r *protocol.MulAchievementReceiveRequest) *protocol.MulAchievementReceiveResponse {
	resp := &protocol.MulAchievementReceiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
	for _, id := range r.AchievementIds {
		if s.Achievements().Receive(s, id, false, nil) {
			resp.AchievementIds = append(resp.AchievementIds, id)
		}
	}
	for _, id := range r.AchievementGroupIds {
		if s.Achievements().Receive(s, id, true, nil) {
			resp.AchievementGroupIds = append(resp.AchievementGroupIds, id)
		}
	}
	return resp
}

func (s *PlayerSession) PatrolStart(r *protocol.EntityPatrolStartRequest) *protocol.EntityPatrolStartResponse {
	if s.Patrols.IsMonster(s, r.EntityId, "patrol start") {
		s.Patrols.Start(r.EntityId, r.Dir)
	}
	return &protocol.EntityPatrolStartResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

func (s *PlayerSession) PatrolStop(r *protocol.EntityPatrolStopRequest) *protocol.EntityPatrolStopResponse {
	if s.Patrols.IsMonster(s, r.EntityId, "patrol stop") {
		s.Patrols.Stop(r.EntityId)
	}
	return &protocol.EntityPatrolStopResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

func (s *PlayerSession) PatrolChangeDir(r *protocol.EntityPatrolChangeDirRequest) *protocol.EntityPatrolChangeDirResponse {
	if s.Patrols.IsMonster(s, r.EntityId, "patrol turn") {
		s.Patrols.ChangeDir(r.EntityId, r.Dir)
	}
	return &protocol.EntityPatrolChangeDirResponse{Code: protocol.ErrorCode_ErrorCode_Success}
}

func (s *PlayerSession) PatrolPointReached(r *protocol.MonsterPatrolPointReachedRequest) *protocol.MonsterPatrolPointReachedResponse {
	if s.Patrols.IsMonster(s, r.EntityId, "patrol point") {
		s.Patrols.PointReached(r.EntityId, r.SplineEntityConfigId, r.Index)
	}
	return &protocol.MonsterPatrolPointReachedResponse{ErrorCode: protocol.ErrorCode_ErrorCode_Success}
}

func (g *WorldGame) registerWorldSystems(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	// Achievements (§1.1, R-58)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.AchievementInfoRequest) *protocol.AchievementInfoResponse {
		return p.Achievements().Info(p.Data.Tables)
	},
		func(*protocol.AchievementInfoRequest) *protocol.AchievementInfoResponse {
			return &protocol.AchievementInfoResponse{}
		})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.AchievementUpdateRequest) *protocol.AchievementUpdateResponse {
		return p.Achievements().Update()
	},
		func(*protocol.AchievementUpdateRequest) *protocol.AchievementUpdateResponse {
			return &protocol.AchievementUpdateResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.AchievementFinishRequest) *protocol.AchievementFinishResponse {
		return p.AchievementFinish(r)
	},
		func(*protocol.AchievementFinishRequest) *protocol.AchievementFinishResponse {
			return &protocol.AchievementFinishResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrAchievementNotClinet}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.AchievementReceiveRequest) *protocol.AchievementReceiveResponse {
		return p.AchievementReceive(r)
	},
		func(*protocol.AchievementReceiveRequest) *protocol.AchievementReceiveResponse {
			return &protocol.AchievementReceiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrTriggerConditionNotMet}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MulAchievementReceiveRequest) *protocol.MulAchievementReceiveResponse {
		return p.MulAchievementReceive(r)
	},
		func(*protocol.MulAchievementReceiveRequest) *protocol.MulAchievementReceiveResponse {
			return &protocol.MulAchievementReceiveResponse{ErrorCode: ok}
		})
	// Area terminal (§1.2, R-50)
	reqOn(h, g, func(p *PlayerSession, r *protocol.AreaTerminalPinOperationRequest) *protocol.AreaTerminalPinOperationResponse {
		SetAreaPin(p.Files, r.Id, r.OpType)
		return &protocol.AreaTerminalPinOperationResponse{Code: ok}
	}, func(*protocol.AreaTerminalPinOperationRequest) *protocol.AreaTerminalPinOperationResponse {
		return &protocol.AreaTerminalPinOperationResponse{Code: ok}
	})
	// World map & custom marks (§1.3, §4)
	reqOn(h, g, func(p *PlayerSession, r *protocol.MapTraceRequest) *protocol.MapTraceResponse {
		return p.WorldMap().Trace(p, r)
	},
		func(*protocol.MapTraceRequest) *protocol.MapTraceResponse {
			return &protocol.MapTraceResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MapCancelTraceRequest) *protocol.MapCancelTraceResponse {
		return p.WorldMap().CancelTrace(p, r)
	},
		func(*protocol.MapCancelTraceRequest) *protocol.MapCancelTraceResponse {
			return &protocol.MapCancelTraceResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.MapTraceInfoRequest) *protocol.MapTraceInfoResponse {
		return p.WorldMap().TraceInfo()
	},
		func(*protocol.MapTraceInfoRequest) *protocol.MapTraceInfoResponse {
			return &protocol.MapTraceInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MapMarkRequest) *protocol.MapMarkResponse {
		return p.WorldMap().AddMark(r)
	},
		func(*protocol.MapMarkRequest) *protocol.MapMarkResponse {
			return &protocol.MapMarkResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MapRemoveMarkRequest) *protocol.MapRemoveMarkResponse {
		return p.WorldMap().RemoveMarks(r)
	},
		func(*protocol.MapRemoveMarkRequest) *protocol.MapRemoveMarkResponse {
			return &protocol.MapRemoveMarkResponse{ErrorCode: ok}
		})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.MapMarkInfoUpdateRequest) proto.Message {
		return &protocol.MapMarkInfoUpdateResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.MapUnlockFieldInfoRequest) *protocol.MapUnlockFieldInfoResponse {
		return UnlockFieldInfo(p)
	},
		func(*protocol.MapUnlockFieldInfoRequest) *protocol.MapUnlockFieldInfoResponse {
			return &protocol.MapUnlockFieldInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.LastBigSceneMiniMapInfoRequest) *protocol.LastBigSceneMiniMapInfoResponse {
		return LastBigSceneMiniMapInfo(p)
	}, func(*protocol.LastBigSceneMiniMapInfoRequest) *protocol.LastBigSceneMiniMapInfoResponse {
		return &protocol.LastBigSceneMiniMapInfoResponse{Code: ok}
	})
	reqOn(h, g, ExploreProgress, func(*protocol.ExploreProgressRequest) *protocol.ExploreProgressResponse {
		return &protocol.ExploreProgressResponse{}
	})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.ExploreProgressRewardRequest) *protocol.ExploreProgressRewardResponse {
		return ExploreProgressReward(p)
	},
		func(*protocol.ExploreProgressRewardRequest) *protocol.ExploreProgressRewardResponse {
			return &protocol.ExploreProgressRewardResponse{}
		})
	reqOn(h, g, CountryExploreScoreInfo, func(*protocol.CountryExploreScoreInfoRequest) *protocol.CountryExploreScoreInfoResponse {
		return &protocol.CountryExploreScoreInfoResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.MulExploreScoreRewardRequest) proto.Message {
		return &protocol.MulExploreScoreRewardResponse{ErrorCode: ok}
	})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.InfrInfoRequest) *protocol.InfrInfoResponse { return InfrInfo(p) },
		func(*protocol.InfrInfoRequest) *protocol.InfrInfoResponse {
			return &protocol.InfrInfoResponse{ErrorCode: ok}
		})
	reqOn(h, g, func(p *PlayerSession, _ *protocol.InfrV2InfoRequest) *protocol.InfrV2InfoResponse {
		return InfrV2Info(p)
	},
		func(*protocol.InfrV2InfoRequest) *protocol.InfrV2InfoResponse {
			return &protocol.InfrV2InfoResponse{ErrorCode: ok}
		})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.DarkCoastInfoRequest) proto.Message {
		return DarkCoastInfo(r)
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.SimpleTrackReportRequest) proto.Message {
		return &protocol.SimpleTrackReportResponse{ErrorCode: ok}
	})
	// Ranges (§1.4, R-91)
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.InitRangeRequest) proto.Message { return InitRange(r) })
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.EntityAccessRangeRequest) proto.Message {
		return EntityAccessRange(r)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.PlayerAccessRangeRequest) proto.Message {
		return PlayerAccessRange(r)
	})
	// Patrols (§1.5, R-120)
	reqOn(h, g, func(p *PlayerSession, r *protocol.EntityPatrolStartRequest) *protocol.EntityPatrolStartResponse {
		return p.PatrolStart(r)
	},
		func(*protocol.EntityPatrolStartRequest) *protocol.EntityPatrolStartResponse {
			return &protocol.EntityPatrolStartResponse{Code: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.EntityPatrolStopRequest) *protocol.EntityPatrolStopResponse {
		return p.PatrolStop(r)
	},
		func(*protocol.EntityPatrolStopRequest) *protocol.EntityPatrolStopResponse {
			return &protocol.EntityPatrolStopResponse{Code: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.EntityPatrolChangeDirRequest) *protocol.EntityPatrolChangeDirResponse {
		return p.PatrolChangeDir(r)
	},
		func(*protocol.EntityPatrolChangeDirRequest) *protocol.EntityPatrolChangeDirResponse {
			return &protocol.EntityPatrolChangeDirResponse{Code: ok}
		})
	reqOn(h, g, func(p *PlayerSession, r *protocol.MonsterPatrolPointReachedRequest) *protocol.MonsterPatrolPointReachedResponse {
		return p.PatrolPointReached(r)
	}, func(*protocol.MonsterPatrolPointReachedRequest) *protocol.MonsterPatrolPointReachedResponse {
		return &protocol.MonsterPatrolPointReachedResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.EntityPatrolAddTagRequest) proto.Message {
		return &protocol.EntityPatrolAddTagResponse{ErrorCode: ok}
	})
	// Adventure manual & Sola guide detection (§3, R-48)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.AdventureManualRequest) *protocol.AdventureManualResponse {
		return AdventureManualInfo(p)
	},
		func(*protocol.AdventureManualRequest) *protocol.AdventureManualResponse {
			return &protocol.AdventureManualResponse{Code: ok}
		})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.AdventureManualDataRequest) proto.Message {
		return AdventureManualDataResponse()
	})
	reqOn(h, g, GetDetectionTarget, func(*protocol.GetDetectionTargetRequest) *protocol.GetDetectionTargetResponse {
		return &protocol.GetDetectionTargetResponse{Code: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.GetDetectionLabelInfoRequest) proto.Message {
		return GetDetectionLabelInfo()
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.LivenessRequest) proto.Message { return Liveness() })
	gateway.OnRequest(h, func(*gateway.Session, *protocol.SlashAndTowerInfoRequest) proto.Message { return SlashAndTowerInfo() })
	reqOn(h, g, func(p *PlayerSession, _ *protocol.WeeklyFrameworkInfoRequest) *protocol.WeeklyFrameworkInfoResponse {
		return WeeklyFrameworkInfo(p)
	},
		func(*protocol.WeeklyFrameworkInfoRequest) *protocol.WeeklyFrameworkInfoResponse {
			return &protocol.WeeklyFrameworkInfoResponse{ErrorCode: ok}
		})
	// Phonograph music (§7.5, R-52)
	reqOn(h, g, func(p *PlayerSession, _ *protocol.PhantomMusicInfoRequest) *protocol.PhantomMusicInfoResponse {
		return p.Music().Info(p.Data.Tables)
	},
		func(*protocol.PhantomMusicInfoRequest) *protocol.PhantomMusicInfoResponse {
			return &protocol.PhantomMusicInfoResponse{ErrorCode: ok}
		})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.PhantomMusicUnlockRequest) proto.Message {
		return MusicUnlock(r)
	})
	reqOn(h, g, func(p *PlayerSession, r *protocol.PhantomMusicCollectUpdateRequest) *protocol.PhantomMusicCollectUpdateResponse {
		return p.Music().UpdateCollect(r)
	}, func(*protocol.PhantomMusicCollectUpdateRequest) *protocol.PhantomMusicCollectUpdateResponse {
		return &protocol.PhantomMusicCollectUpdateResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PhantomMusicSaveRequest) proto.Message {
		return &protocol.PhantomMusicSaveResponse{ErrCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PhantomMusicSwitchRequest) proto.Message {
		return &protocol.PhantomMusicSwitchResponse{ErrorCode: ok}
	})
}
