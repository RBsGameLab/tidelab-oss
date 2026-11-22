// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/gateway"
	"tidelab/internal/quests"
	"tidelab/internal/save"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// WorldGame is the game behind the gateway: a PlayerSession per session that entered, with its
// combat context. Combat's handlers are registered first and the session's after them, so where
// both answer a request (the time checks) the scene's flow clock answers.
type WorldGame struct {
	StateDir     string
	Data         *world.WorldData
	Clock        *common.GameClock
	Shared       *combat.Shared
	QuestCatalog *quests.Catalog
	// NewSystems makes a session's systems (nil: QuestSystems when QuestCatalog != nil, else NoSystems).
	NewSystems func(p *PlayerSession) Systems
	// Rng is combat's random numbers (nil: the system's).
	Rng combat.Rng
	// SeedDefaultEchoes: a new account gets an echo of every kind (tests turn it off).
	SeedDefaultEchoes bool
	// CommandFile is the dev command file polled at the heartbeat (specs/systems.md §7.1); "": none.
	CommandFile string

	handlers gateway.MessageHandlers
	players  map[*gateway.Session]*PlayerSession
	quests   map[int32]*PlayerQuests
}

func NewWorldGame(stateDir string, d *world.WorldData, clock *common.GameClock, shared *combat.Shared) *WorldGame {
	g := &WorldGame{
		StateDir:          stateDir,
		Data:              d,
		Clock:             clock,
		Shared:            shared,
		SeedDefaultEchoes: true,
		players:           map[*gateway.Session]*PlayerSession{},
		quests:            map[int32]*PlayerQuests{},
	}
	g.registerCombat(&g.handlers)
	g.registerSession(&g.handlers)
	g.registerQuests(&g.handlers)
	g.registerDungeons(&g.handlers)
	g.registerActivities(&g.handlers)
	g.registerShops(&g.handlers)
	g.registerMail(&g.handlers)
	g.registerChat(&g.handlers)
	g.registerMotorcycle(&g.handlers)
	g.registerWorldSystems(&g.handlers)
	g.registerProfileAndCosmetics(&g.handlers)
	return g
}

// EnableQuests attaches quest data so sessions run QuestEngine and sibling quest systems.
func (g *WorldGame) EnableQuests(catalog *quests.Catalog) {
	g.QuestCatalog = catalog
}

func (g *WorldGame) Handlers() *gateway.MessageHandlers { return &g.handlers }

// Find is the session's player, nil before it entered.
func (g *WorldGame) Find(s *gateway.Session) *PlayerSession { return g.players[s] }

// sessionClient adapts a gateway session.
type sessionClient struct{ s *gateway.Session }

func (c sessionClient) PlayerID() int32                               { return c.s.PlayerID }
func (c sessionClient) Push(m proto.Message)                          { c.s.Push(m) }
func (c sessionClient) PushID(id protocol.MessageID, m proto.Message) { c.s.PushID(id, m) }

// NewSession makes a player's session (not entered yet).
func (g *WorldGame) NewSession(c Client) *PlayerSession {
	files := save.NewPlayerFiles(g.StateDir, c.PlayerID())
	p := NewPlayerSession(c, files, g.Data, g.Clock, func(hooks combat.GameHooks) *combat.PlayerContext {
		ctx := combat.NewPlayerContext(g.Shared, save.LoadPlayer(g.StateDir, c.PlayerID()), hooks, g.Rng)
		ctx.PrepareForLogin(g.Clock.RealMs(), uint32(max(0, g.Clock.Seconds())), g.SeedDefaultEchoes)
		return ctx
	})
	p.Fsm = g.Shared.Fsm
	p.CommandFile = g.CommandFile
	p.QuestState = g.questsOf(files)
	if g.NewSystems != nil {
		p.Systems = g.NewSystems(p)
	} else if p.QuestState != nil {
		p.Systems = QuestSystems{}
	}
	return p
}

// questsOf returns the player's quest state, kept for the life of the server (specs/quests.md §5.1).
func (g *WorldGame) questsOf(files *save.PlayerFiles) *PlayerQuests {
	if g.QuestCatalog == nil {
		return nil
	}
	q, ok := g.quests[files.PlayerID]
	if !ok {
		q = NewPlayerQuests(g.QuestCatalog, files)
		g.quests[files.PlayerID] = q
	}
	return q
}

func (g *WorldGame) Entered(s *gateway.Session, reconnect bool) {
	p := g.NewSession(sessionClient{s})
	g.players[s] = p
	defer func() {
		if r := recover(); r != nil {
			common.Error("scene", fmt.Sprintf("player %d could not enter the world: %v", s.PlayerID, r))
			panic(r)
		}
	}()
	p.Enter(reconnect)
}

func (g *WorldGame) Heartbeat(s *gateway.Session) {
	if p := g.Find(s); p != nil {
		p.Heartbeat()
	}
}

func (g *WorldGame) Released(s *gateway.Session) {
	p, ok := g.players[s]
	if !ok {
		return
	}
	delete(g.players, s)
	p.Release()
}

// ----- handlers -----

// on answers with run's message when the session's player entered, else with none.
func on[T proto.Message](g *WorldGame, s *gateway.Session, run func(*PlayerSession) T, none T) proto.Message {
	if p := g.Find(s); p != nil {
		return run(p)
	}
	return none
}

func (g *WorldGame) ctx(s *gateway.Session) *combat.PlayerContext {
	p := g.Find(s)
	if p == nil || p.Combat == nil {
		panic(fmt.Errorf("no combat context for player %d", s.PlayerID))
	}
	return p.Combat
}

func (g *WorldGame) nowMs() int64 { return g.Clock.RealMs() }

// req registers a request handled by the player's combat context.
func req[T any, PT interface {
	*T
	proto.Message
}, R proto.Message](h *gateway.MessageHandlers, g *WorldGame, fn func(c *combat.PlayerContext, r PT, sink combat.PushSink) R) {
	gateway.OnRequest(h, func(s *gateway.Session, r PT) proto.Message { return fn(g.ctx(s), r, s) })
}

// registerCombat registers combat's requests and pushes (roles, inventory, formation, combat).
func (g *WorldGame) registerCombat(h *gateway.MessageHandlers) {
	now := g.nowMs
	t := g.Shared.Tables
	type ctx = combat.PlayerContext
	type sink = combat.PushSink

	// Roles (specs/roles.md §6).
	req(h, g, func(c *ctx, r *protocol.RoleFavorListRequest, _ sink) proto.Message {
		return c.Roles.HandleRoleFavorList(r)
	})
	req(h, g, func(c *ctx, r *protocol.RolePropRequest, _ sink) proto.Message { return c.Roles.HandleRoleProp(r) })
	req(h, g, func(c *ctx, r *protocol.PbUpLevelSkillRequest, s sink) proto.Message {
		return c.Roles.HandleUpLevelSkill(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.RoleActivateSkillRequest, s sink) proto.Message {
		return c.Roles.HandleActivateSkill(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.RoleSkillLevelUpViewRequest, _ sink) proto.Message {
		return c.Roles.HandleSkillLevelUpView(r)
	})
	req(h, g, func(c *ctx, r *protocol.RoleSkillViewRequest, _ sink) proto.Message {
		return c.Roles.HandleSkillView(r)
	})
	req(h, g, func(c *ctx, r *protocol.RoleSkillQuickLevelUpRequest, s sink) proto.Message {
		return c.Roles.HandleSkillQuickLevelUp(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.ResonantChainUnlockRequest, s sink) proto.Message {
		return c.Roles.HandleResonantChainUnlock(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.RoleSkillBranchModifyRequest, s sink) proto.Message {
		return c.Roles.HandleSkillBranchModify(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.RoleElementChangeRequest, s sink) proto.Message {
		return c.Roles.HandleRoleElementChange(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.RoleSexChangeRequest, s sink) proto.Message {
		return c.Roles.HandleRoleSexChange(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PlayerVoiceLanguageRequest, _ sink) proto.Message {
		return c.Roles.HandlePlayerVoiceLanguage(r)
	})
	req(h, g, func(c *ctx, r *protocol.PlayerRoleVoiceSetRequest, _ sink) proto.Message {
		return c.Roles.HandlePlayerRoleVoiceSet(r, now())
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.RoleDevelopConfigRequest) proto.Message {
		return combat.HandleRoleDevelopConfig(t, r)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.TeamRecommendRequest) proto.Message {
		return combat.HandleTeamRecommend(r)
	})

	// Inventory: items, lock and disuse, weapons, echoes, presets, calabash, projector (specs/inventory.md).
	req(h, g, func(c *ctx, r *protocol.NormalItemRequest, _ sink) proto.Message {
		return c.Inventory.HandleNormalItem(r)
	})
	req(h, g, func(c *ctx, r *protocol.ItemLockRequest, s sink) proto.Message {
		return c.Inventory.HandleItemLock(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.ItemDisuseRequest, s sink) proto.Message {
		return c.Inventory.HandleItemDisuse(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.WeaponItemRequest, _ sink) proto.Message {
		return c.Inventory.HandleWeaponItem(r)
	})
	req(h, g, func(c *ctx, r *protocol.WeaponLevelUpRequest, s sink) proto.Message {
		return c.Inventory.HandleWeaponLevelUp(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.WeaponBreachRequest, s sink) proto.Message {
		return c.Inventory.HandleWeaponBreach(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.WeaponResonUpRequest, s sink) proto.Message {
		return c.Inventory.HandleWeaponResonUp(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.EquipTakeOnRequest, s sink) proto.Message {
		return c.Inventory.HandleEquipTakeOn(r, s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomItemRequest, _ sink) proto.Message {
		return c.Inventory.HandlePhantomItem(r)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomPutOnRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomPutOn(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomAutoPutRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomAutoPut(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomSkinChangeRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomSkinChange(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomLevelUpRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomLevelUp(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomIdentifyRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomIdentify(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomPolishRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomPolish(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomBatchPolishRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomBatchPolish(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomVicePolishRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomVicePolish(r, s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomVicePolishAckRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomVicePolishAck(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomEquipGroupInfosRequest, _ sink) proto.Message {
		return c.Inventory.HandlePhantomEquipGroupInfos(r)
	})
	req(h, g, func(c *ctx, r *protocol.AddPhantomEquipGroupRequest, _ sink) proto.Message {
		return c.Inventory.HandleAddPhantomEquipGroup(r)
	})
	req(h, g, func(c *ctx, r *protocol.UsePhantomEquipGroupRequest, s sink) proto.Message {
		return c.Inventory.HandleUsePhantomEquipGroup(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.TopPhantomEquipGroupRequest, _ sink) proto.Message {
		return c.Inventory.HandleTopPhantomEquipGroup(r)
	})
	req(h, g, func(c *ctx, r *protocol.ChangePhantomEquipGroupNameRequest, _ sink) proto.Message {
		return c.Inventory.HandleChangePhantomEquipGroupName(r)
	})
	req(h, g, func(c *ctx, r *protocol.DeletePhantomEquipGroupRequest, _ sink) proto.Message {
		return c.Inventory.HandleDeletePhantomEquipGroup(r)
	})
	req(h, g, func(c *ctx, r *protocol.CalabashLevelRewardRequest, s sink) proto.Message {
		return c.Inventory.HandleCalabashLevelReward(r, s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomInteractionEquipRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomInteractionEquip(r, s)
	})
	req(h, g, func(c *ctx, r *protocol.PhantomInteractionSkinChangeRequest, s sink) proto.Message {
		return c.Inventory.HandlePhantomInteractionSkinChange(r, s)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.EnergySyncRequest) proto.Message {
		return combat.HandleEnergySync(r)
	})
	req(h, g, func(c *ctx, r *protocol.LoadEquipInfoRequest, _ sink) proto.Message {
		return c.Inventory.HandleLoadEquipInfo(r)
	})
	req(h, g, func(c *ctx, r *protocol.RoleSkinRequest, _ sink) proto.Message { return c.Inventory.HandleRoleSkin(r) })
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.LoadCalabashSkinInfoRequest) proto.Message {
		return combat.HandleLoadCalabashSkinInfo(r)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.PhantomFetterRecommendRequest) proto.Message {
		return combat.HandlePhantomFetterRecommend(r)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.PhantomAttrRecommendRequest) proto.Message {
		return combat.HandlePhantomAttrRecommend(r)
	})

	// Formation (specs/formation.md §5).
	req(h, g, func(c *ctx, r *protocol.GetFormationDataRequest, _ sink) proto.Message {
		return c.Formation.HandleGetFormationData(r)
	})
	req(h, g, func(c *ctx, r *protocol.UpdateFormationRequest, s sink) proto.Message {
		return c.Formation.HandleUpdateFormation(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.ReorderFormationRequest, _ sink) proto.Message {
		return c.Formation.HandleReorderFormation(r)
	})
	req(h, g, func(c *ctx, r *protocol.RenameFormationRequest, s sink) proto.Message {
		return c.Formation.HandleRenameFormation(r, s)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.FormationAttrRequest) proto.Message {
		return combat.HandleFormationAttr(r)
	})

	// Combat (specs/combat.md).
	req(h, g, func(c *ctx, r *protocol.CombatSendPackRequest, s sink) proto.Message {
		return c.Combat.HandleCombatSendPack(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.LandingDamageRequest, s sink) proto.Message {
		return c.Combat.HandleLandingDamage(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.ReviveRequest, s sink) proto.Message {
		return c.Combat.HandleRevive(r, now(), s)
	})
	req(h, g, func(c *ctx, r *protocol.TimeCheckRequest, _ sink) proto.Message {
		return c.Combat.HandleTimeCheck(r, now())
	})
	gateway.OnPush(h, func(s *gateway.Session, p *protocol.TimeStopPush) { g.ctx(s).Combat.HandleTimeStopPush(p, now()) })
	gateway.OnPush(h, func(s *gateway.Session, p *protocol.RTimeStopPush) { g.ctx(s).Combat.HandleRTimeStopPush(p) })
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.CombatMessagePrefixInfoRequest) proto.Message {
		return combat.HandlePrefixInfo(r)
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.CombatMessagePostInfoRequest) proto.Message {
		return combat.HandlePostInfo(r)
	})
}

// registerSession registers the session's request handlers.
func (g *WorldGame) registerSession(h *gateway.MessageHandlers) {
	ok := protocol.ErrorCode_ErrorCode_Success
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.EntityActiveRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.EntityActiveResponse { return p.EntityActive(r) },
			&protocol.EntityActiveResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrEntityNotFound})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.ApplySceneClientEntityIncIdRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.ApplySceneClientEntityIncIdResponse { return p.ApplyClientEntityIDs(r) },
			&protocol.ApplySceneClientEntityIncIdResponse{})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.TraceEntityPosRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.TraceEntityPosResponse { return p.TraceEntityPos(r) },
			&protocol.TraceEntityPosResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrEntityNotFound})
	})
	gateway.OnRequest(h, func(s *gateway.Session, _ *protocol.TeleportDataRequest) proto.Message {
		return on(g, s, (*PlayerSession).TeleportData, &protocol.TeleportDataResponse{})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.TimeCheckRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.TimeCheckResponse { return p.TimeCheck(r) }, &protocol.TimeCheckResponse{})
	})
	gateway.OnPush(h, func(s *gateway.Session, m *protocol.TimeStopPush) {
		if p := g.Find(s); p != nil {
			p.SetFlowDilation(float64(m.TimeDilation))
		}
	})
	gateway.OnPush(h, func(s *gateway.Session, m *protocol.RTimeStopPush) {
		if p := g.Find(s); p != nil {
			p.SetFlowDilation(float64(m.Dilation))
		}
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.EnterAreaRequest) proto.Message {
		return &protocol.EnterAreaResponse{Id: r.Id}
	})
	gateway.OnRequest(h, func(_ *gateway.Session, r *protocol.PlayerVoxelEnvRequest) proto.Message {
		return &protocol.PlayerVoxelEnvResponse{VoxelEnv: r.VoxelEnv}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.JoinSceneNotifyConfirmRequest) proto.Message {
		return &protocol.JoinSceneNotifyConfirmResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.LoadingLevelAreaRequest) proto.Message {
		return &protocol.LoadingLevelAreaResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.UpdateSceneDateRequest) proto.Message {
		return &protocol.UpdateSceneDateResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.SceneLoadingFinishRequest) proto.Message {
		return &protocol.SceneLoadingFinishResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PlayerMotionRequest) proto.Message {
		return &protocol.PlayerMotionResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.WeatherSwitchInfoRequest) proto.Message {
		return &protocol.WeatherSwitchInfoResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PsnSettingRequest) proto.Message {
		return &protocol.PsnSettingResponse{MatchPsnUser: false}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.XboxSettingRequest) proto.Message {
		return &protocol.XboxSettingResponse{MatchXboxUser: false}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PlayerGameTokenRequest) proto.Message {
		return &protocol.PlayerGameTokenResponse{GameToken: ""}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PlayerPsnBlockListRequest) proto.Message {
		return &protocol.PlayerPsnBlockListResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.PlayerXboxBlockListRequest) proto.Message {
		return &protocol.PlayerXboxBlockListResponse{}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.AdviceRequest) proto.Message {
		return &protocol.AdviceResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.LanguageSettingUpdateRequest) proto.Message {
		return &protocol.LanguageSettingUpdateResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.ClientPullResourcePackageRequest) proto.Message {
		return &protocol.ClientPullResourcePackageResponse{ErrorId: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.TeleportFinishRequest) proto.Message {
		return &protocol.TeleportFinishResponse{ErrorCode: ok}
	})
	gateway.OnPush(h, func(s *gateway.Session, m *protocol.MovePackagePush) {
		if p := g.Find(s); p != nil {
			p.Move(m)
		}
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.TeleportTransferRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.TeleportTransferResponse { return p.TeleportTransfer(r) },
			&protocol.TeleportTransferResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrTeleportIdNotExist})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.EnterInstRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.EnterInstResponse { return p.EnterInst(r) },
			&protocol.EnterInstResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrInstanceConfigNotFound})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.VehicleRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.VehicleResponse { return p.Vehicle(r) },
			&protocol.VehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrVehicleEntityNotExist})
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.EntityEnterVehicleRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.EntityEnterVehicleResponse { return p.EnterVehicle(r) },
			&protocol.EntityEnterVehicleResponse{ErrorCode: protocol.ErrorCode_ErrorCode_ErrVehicleEntityNotExist})
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.VehicleFinishRequest) proto.Message {
		return &protocol.VehicleFinishResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(*gateway.Session, *protocol.SitChairRequest) proto.Message {
		return &protocol.SitChairResponse{ErrorCode: ok}
	})
	gateway.OnRequest(h, func(s *gateway.Session, r *protocol.SplineMoveRequest) proto.Message {
		return on(g, s, func(p *PlayerSession) *protocol.SplineMoveResponse { return p.SplineMove(r) },
			&protocol.SplineMoveResponse{ErrorCode: ok})
	})
	h.AfterResponse = func(s *gateway.Session) {
		if p := g.Find(s); p != nil {
			p.RunAfterResponse()
		}
	}
}
