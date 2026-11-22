// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/data"
	"tidelab/internal/gatewaytest"
	"tidelab/internal/save"
	"tidelab/protocol"
)

// The C# CombatTests.EndToEndGatewayWithR143gGameHandlesLoginRequestsAndFlushesDirtySaves, ported:
// a client logs in over KCP, with the key exchange and encryption, enters the world, sends the
// role and formation requests, and the saves are flushed when the gateway closes the session.
// The game is the whole WorldGame here, since the combat services are part of it in Go.
//
// Every wait is on a condition and on the test clock (gatewaytest.Rig.WaitFor), with a bound:
// no sleeping, and the same steps on every run.

const e2eWaitMs = 5000

func TestEndToEndGatewayWithTheGameHandlesLoginRequestsAndFlushesDirtySaves(t *testing.T) {
	r := newRig(t)
	// The skill the request raises: the rig's tables have none.
	r.tables.Skill = append(r.tables.Skill, &data.Skill{Id: 1001, MaxSkillLevel: 10})
	r.tables.Reindex()
	r.saveLivePlayer() // player 777 with roles 1304 (current), 1505, 1311
	// New accounts start at gateway.FirstPlayerID (666 since R-170): point the counter at 777.
	if err := os.WriteFile(filepath.Join(r.state, "player", "next"), []byte("777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rig := gatewaytest.NewRigAt(t, r.state, r.Game())

	c := rig.Client(50000)
	c.Connect()
	c.ExchangeKey()

	// Login, then entering the game: the login data and the scene join arrive as encrypted KCP pushes.
	login := c.Request(&protocol.LoginRequest{Account: "r143g_e2e", Token: "1"}, true)
	rig.WaitFor(e2eWaitMs, "the login response", func() bool { return c.ResponseTo(login) != nil })
	if f := c.ResponseTo(login); f.MessageID != protocol.IDLoginResponse {
		t.Fatalf("answered with %s", f.MessageID.Name())
	}
	enter := c.Request(&protocol.EnterGameRequest{}, true)
	rig.WaitFor(e2eWaitMs, "the enter game response and the end of the scene join", func() bool {
		return c.ResponseTo(enter) != nil && c.Has(protocol.IDUpdateFormationNotify)
	})
	for _, id := range []protocol.MessageID{
		protocol.IDBasicInfoNotify, protocol.IDPbGetRoleListNotify, protocol.IDFormationAttrNotify,
		protocol.IDUpdateFormationNotify, protocol.IDSwitchBattleModeNotify,
	} {
		if !c.Has(id) {
			t.Errorf("no %s after entering the game", id.Name())
		}
	}

	// Requests and responses through the gateway.
	type pending struct {
		rpc  uint16
		want protocol.MessageID
	}
	var wait []pending
	ask := func(rpc uint16, want protocol.MessageID) { wait = append(wait, pending{rpc, want}) }
	ask(c.Request(&protocol.NormalItemRequest{}, true), protocol.IDNormalItemResponse)
	ask(c.Request(&protocol.WeaponItemRequest{}, true), protocol.IDWeaponItemResponse)
	ask(c.Request(&protocol.GetFormationDataRequest{}, true), protocol.IDGetFormationDataResponse)
	ask(c.Request(&protocol.RoleDevelopConfigRequest{}, true), protocol.IDRoleDevelopConfigResponse)
	up := c.Request(&protocol.PbUpLevelSkillRequest{RoleId: 1304, SkillId: 1001}, true)
	ask(up, protocol.IDPbUpLevelSkillResponse)
	ask(c.Request(&protocol.ReorderFormationRequest{FormationId: 1, TargetId: 2}, true), protocol.IDReorderFormationResponse)
	rig.WaitFor(e2eWaitMs, "the six responses", func() bool {
		for _, p := range wait {
			if c.ResponseTo(p.rpc) == nil {
				return false
			}
		}
		return true
	})
	for _, p := range wait {
		if got := c.ResponseTo(p.rpc).MessageID; got != p.want {
			t.Errorf("rpc %d answered with %s, want %s", p.rpc, got.Name(), p.want.Name())
		}
	}
	// The reply to the skill request carries the new level, not only the message id.
	var upResp protocol.PbUpLevelSkillResponse
	if err := proto.Unmarshal(c.ResponseTo(up).Payload, &upResp); err != nil {
		t.Fatal(err)
	}
	if upResp.Code != protocol.ErrorCode_ErrorCode_Success || upResp.SkillInfo == nil || upResp.SkillInfo.Key != 1001 || upResp.SkillInfo.Value != 2 {
		t.Fatalf("skill reply %v", &upResp)
	}

	// Closing the session releases the player, which flushes the dirty role save at once.
	rig.GW.CloseAll("test done")
	saved := save.LoadPlayer(r.state, rigPlayer)
	role, ok := saved.Roles.Get(1304)
	if !ok {
		t.Fatal("role 1304 not saved")
	}
	level := int32(0)
	for _, s := range role.Skills {
		if s.Key == 1001 {
			level = s.Value
		}
	}
	if level != 2 {
		t.Errorf("saved skill 1001 level %d, want 2", level)
	}
	if saved.FormationInfo.CurFormation != 1 {
		t.Errorf("saved cur formation %d, want 1", saved.FormationInfo.CurFormation)
	}
}
