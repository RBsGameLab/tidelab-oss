// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package gateway_test

import (
	"bytes"
	"crypto/rsa"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	. "tidelab/internal/gateway"
	gt "tidelab/internal/gatewaytest"
	"tidelab/internal/kcp"
	"tidelab/internal/wire"
	"tidelab/protocol"
)

type recordingGame struct {
	h      MessageHandlers
	events []string
}

func (g *recordingGame) Handlers() *MessageHandlers { return &g.h }
func (g *recordingGame) Entered(s *Session, reconnect bool) {
	kind := "login"
	if reconnect {
		kind = "reconnect"
	}
	g.events = append(g.events, fmt.Sprintf("entered %d %s", s.PlayerID, kind))
}
func (g *recordingGame) Released(s *Session) {
	g.events = append(g.events, fmt.Sprintf("released %d", s.PlayerID))
}
func (g *recordingGame) Heartbeat(s *Session) {
	g.events = append(g.events, fmt.Sprintf("heartbeat %d", s.PlayerID))
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTheConnectReplyCarriesANewConversationIDPerConnect(t *testing.T) {
	r := gt.NewRig(t, nil)
	a, b := r.Client(50001), r.Client(50002)
	a.Connect()
	b.Connect()
	again := r.Client(50001)
	again.Connect()
	eq(t, []uint32{a.Conv, b.Conv, again.Conv}, []uint32{1, 2, 3})
	eq(t, len(r.GW.Sessions()), 3)
}

func TestOnlyThirtyFiveBytesStartingWith0xEDConnect(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	r.GW.Receive(c.Addr, append([]byte{ConnectByte}, make([]byte, 33)...))
	r.GW.Receive(c.Addr, append([]byte{0xEC}, make([]byte, 34)...))
	r.GW.Receive(c.Addr, []byte{1, 2, 3})
	eq(t, len(r.GW.Sessions()), 0)
}

func TestTheKeyResponseIsInTheClearAndTheNextFrameEncrypted(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	c.Decrypts = false
	kf := c.Response(c.Request(&protocol.ProtoKeyRequest{}, false))
	eq(t, kf.SeqNo, 1)
	var kr protocol.ProtoKeyResponse
	proto.Unmarshal(kf.Payload, &kr)
	eq(t, []int{int(kr.Type), len(kr.Key)}, []int{2, 256})
	key, _ := rsa.DecryptPKCS1v15(nil, gt.TestKey(), kr.Key)
	ci, _ := wire.NewCipher(key)
	c.SendFrame(wire.Request, 2, protocol.IDLoginRequest, ci.Encrypt(protocol.Marshal(&protocol.LoginRequest{Account: "3838"}), 2), false)
	lf := c.Response(2)
	eq(t, []any{lf.SeqNo, lf.MessageID.Name(), len(lf.Payload) % 16}, []any{2, "LoginResponse", 0})
	plain, err := ci.Decrypt(lf.Payload, lf.SeqNo)
	var lr protocol.LoginResponse
	if err != nil || proto.Unmarshal(plain, &lr) != nil || len(lr.ReconnectToken) != 64 {
		t.Fatal(err)
	}
}

func TestWithoutAClientKeyTheKeyResponseIsType0AndTheSessionStaysInTheClear(t *testing.T) {
	r := gt.NewPlainRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	kf := c.Response(c.Request(&protocol.ProtoKeyRequest{IsLogin: true}, false))
	var kr protocol.ProtoKeyResponse
	proto.Unmarshal(kf.Payload, &kr)
	eq(t, []int{int(kr.Type), len(kr.Key)}, []int{0, 0})
	// The login goes and comes back as plain protobuf.
	rpc := c.Request(&protocol.LoginRequest{Account: "3838", Token: "1"}, false)
	var lr protocol.LoginResponse
	if err := proto.Unmarshal(c.Response(rpc).Payload, &lr); err != nil || lr.Code != protocol.ErrorCode_ErrorCode_Success || len(lr.ReconnectToken) != 64 {
		t.Fatal(err, lr.Code)
	}
	if r.Session(c.Conv).IsEncrypted() {
		t.Fatal("session encrypted")
	}
	gt.Answer[protocol.EnterGameResponse](c, c.Request(&protocol.EnterGameRequest{}, false))
}

func TestLoginCreatesTheLinkOnceAndReusesIt(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	lr := c.Login("3838")
	if lr.Code != protocol.ErrorCode_ErrorCode_Success || lr.Timestamp-gt.Start.UnixMilli() > 1000 || !regexp.MustCompile("^[0-9a-f]{64}$").MatchString(lr.ReconnectToken) {
		t.Fatal(lr)
	}
	eq(t, r.Read("account/link/3838"), ".{\n    .player_id = 666,\n}\n")
	eq(t, r.Read("player/next"), "667\n")
	eq(t, r.Read("player/666/reconnect_token"), lr.ReconnectToken)
	eq(t, r.GW.Sessions()[0].PlayerID, 666)
	again := r.Client(50003)
	again.Connect()
	second := again.Login("3838")
	if second.ReconnectToken == lr.ReconnectToken || r.Read("player/666/reconnect_token") != second.ReconnectToken || r.Read("player/next") != "667\n" {
		t.Fatal("second login")
	}
	other := r.Client(50004)
	other.Connect()
	other.Login("someone else")
	eq(t, r.Read("account/link/someone else"), ".{\n    .player_id = 667,\n}\n")
	eq(t, r.Read("player/next"), "668\n")
}

func TestAnExistingCounterAndLinkAreUsed(t *testing.T) {
	r := gt.NewRig(t, nil)
	r.Write("player/next", "1000 \n")
	r.Write("account/link/old", ".{ .player_id = 5 }")
	c := r.Client(50000)
	c.Connect()
	c.Login("old")
	eq(t, r.Session(c.Conv).PlayerID, 5)
	f := r.Client(50005)
	f.Connect()
	f.Login("new")
	eq(t, r.Session(f.Conv).PlayerID, 1000)
	eq(t, r.Read("player/next"), "1001\n")
}

func TestNamesAreMadeFileSafeOrRefused(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	c.Login("a:b/c?\u0001")
	if !common.FileExists(r.File("account/link/a_b_c__")) || SafeName("a:b/c?\u0001") != "a_b_c__" {
		t.Fatal("safe name")
	}
	r2 := gt.NewRig(t, nil)
	d := r2.Client(50000)
	d.Connect()
	eq(t, d.Login("..").Code, protocol.ErrorCode_ErrorCode_GateLoginUserIdErr)
	if _, err := os.Stat(r2.File("account/link")); !os.IsNotExist(err) {
		t.Fatal("link directory made")
	}
}

func TestEnterGameAnswersEmptyAndEntersTheSession(t *testing.T) {
	g := &recordingGame{}
	r := gt.NewRig(t, g)
	c := r.Client(50000)
	c.Connect()
	c.LoginAndEnter("3838")
	s := r.GW.Sessions()[0]
	if s.Phase != InGame || r.GW.Holder(666) != s {
		t.Fatal("not in game")
	}
	eq(t, g.events, []string{"entered 666 login"})
	for _, f := range c.Received {
		if f.MessageID == protocol.IDEnterGameResponse && len(f.Payload) != 0 {
			t.Fatal("payload")
		}
	}
}

func TestHeartbeatIsAnsweredBeforeAndAfterEntering(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	c.Login("3838")
	gt.Answer[protocol.HeartbeatResponse](c, c.Request(&protocol.HeartbeatRequest{}, true))
	gt.Answer[protocol.EnterGameResponse](c, c.Request(&protocol.EnterGameRequest{}, true))
	gt.Answer[protocol.HeartbeatResponse](c, c.Request(&protocol.HeartbeatRequest{}, true))
	eq(t, r.GW.Sessions()[0].Phase, InGame)
}

func TestWhatComesBeforeEnteringClosesTheSession(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	c.Login("3838")
	c.Request(&protocol.AchievementInfoRequest{}, true)
	r.Run(200)
	if len(r.GW.Sessions()) != 0 || !strings.Contains(log.String(), "closed: AchievementInfoRequest before entering the game") {
		t.Fatal(log.String())
	}
	r = gt.NewRig(t, nil)
	c = r.Client(50000)
	c.Connect()
	c.Login("3838")
	c.Push(&protocol.LogoutNotify{})
	r.Run(200)
	eq(t, len(r.GW.Sessions()), 0)
	r = gt.NewRig(t, nil)
	c = r.Client(50000)
	c.Connect()
	c.ExchangeKey()
	c.Request(&protocol.EnterGameRequest{}, true)
	r.Run(200)
	eq(t, len(r.GW.Sessions()), 0)
}

func TestAnUnhandledRequestGetsAnEmptyPairedResponseAndALogLine(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	c.LoginAndEnter("3838")
	f := c.Response(c.Request(&protocol.AchievementInfoRequest{}, true))
	if f.MessageID != protocol.IDAchievementInfoResponse || len(f.Payload) != 0 {
		t.Fatal(f)
	}
	want := fmt.Sprintf("warning(connection): no handler for AchievementInfoRequest (msg_id: %d), replied empty\n", protocol.IDAchievementInfoRequest)
	if !strings.Contains(log.String(), want) {
		t.Fatal(log.String())
	}
	c.Push(&protocol.LogoutNotify{})
	r.Run(200)
	if !strings.Contains(log.String(), "warning(connection): no handler for LogoutNotify (msg_id: 110)\n") || len(r.GW.Sessions()) != 1 {
		t.Fatal(log.String())
	}
	c.Response(c.Request(&protocol.EnterGameRequest{Mode: 1}, true))
	if !strings.Contains(log.String(), "no handler for EnterGameRequest (msg_id: 105), replied empty, payload: 1801\n") {
		t.Fatal(log.String())
	}
}

func TestRegisteredHandlersAnswerWithTheirMessage(t *testing.T) {
	g := &recordingGame{}
	OnRequest(&g.h, func(*Session, *protocol.AchievementInfoRequest) proto.Message {
		return &protocol.AchievementInfoResponse{FinishCount: 3}
	})
	OnPush(&g.h, func(s *Session, _ *protocol.LogoutNotify) {
		g.events = append(g.events, fmt.Sprintf("push %d", s.PlayerID))
	})
	r := gt.NewRig(t, g)
	c := r.Client(50000)
	c.Connect()
	c.LoginAndEnter("3838")
	eq(t, gt.Answer[protocol.AchievementInfoResponse](c, c.Request(&protocol.AchievementInfoRequest{}, true)).FinishCount, 3)
	c.Push(&protocol.LogoutNotify{})
	r.Run(200)
	if !strings.Contains(strings.Join(g.events, ","), "push 666") {
		t.Fatal(g.events)
	}
}

func TestAFrameWithABadCrcClosesTheSession(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	b := (&wire.Frame{Type: wire.Request, SeqNo: 1, RpcID: 1, MessageID: protocol.IDProtoKeyRequest, Payload: []byte{8, 1}}).Encode()
	b[len(b)-1] ^= 0xFF
	c.SendRaw(b)
	r.Run(200)
	if len(r.GW.Sessions()) != 0 || !strings.Contains(log.String(), "CRC mismatch") {
		t.Fatal(log.String())
	}
}

func TestFramesSplitAcrossKcpMessagesAndSharingOneAreBothRead(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	key := (&wire.Frame{Type: wire.Request, SeqNo: 1, RpcID: 1, MessageID: protocol.IDProtoKeyRequest}).Encode()
	beat := (&wire.Frame{Type: wire.Request, SeqNo: 2, RpcID: 2, MessageID: protocol.IDHeartbeatRequest}).Encode()
	both := append(key, beat...)
	c.SendRaw(both[:5])
	c.SendRaw(both[5 : len(both)-3])
	r.Run(50)
	eq(t, len(c.Received), 1)
	c.SendRaw(both[len(both)-3:])
	c.Response(2)
	eq(t, len(c.Received), 2)
}

func TestADatagramFromAnotherAddressIsIgnored(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	forged := make([]byte, kcp.HeaderSize)
	binary.LittleEndian.PutUint32(forged, c.Conv)
	forged[4] = 99 // would be malformed if it were read
	r.GW.Receive(netip.MustParseAddrPort("127.0.0.1:1"), forged)
	if strings.Contains(log.String(), "malformed") || len(r.GW.Sessions()) != 1 {
		t.Fatal(log.String())
	}
}

func TestASilentSessionTimesOutAfterTenSecondsBeforeEntering(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	r := gt.NewRig(t, nil)
	r.Client(50000).Connect()
	r.Time.Elapsed += LoginIdleMs + 1
	r.GW.Tick()
	if len(r.GW.Sessions()) != 0 || !strings.Contains(log.String(), "closed: timed out") {
		t.Fatal(log.String())
	}
}

func TestReconnectWithAWrongTokenOrNoBasicInfoIsRefused(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	token := c.Login("3838").ReconnectToken
	// No basic_info yet.
	n := r.Client(50010)
	n.Connect()
	n.ExchangeKey()
	eq(t, gt.Answer[protocol.ReconnectResponse](n, n.Request(&protocol.ReconnectRequest{PlayerId: 666, ReconnectToken: token}, true)).ErrorCode,
		protocol.ErrorCode_ErrorCode_ErrReconnectGWGetGatePlayerFailed)
	r.Write("player/666/basic_info", ".{}")
	for _, tc := range []struct {
		player int32
		token  string
	}{{666, strings.Repeat("0", 64)}, {666, token[:63]}, {0, token}, {667, token}} {
		a := r.Client(uint16(50100 + int(tc.player) + len(tc.token)))
		a.Connect()
		a.ExchangeKey()
		resp := gt.Answer[protocol.ReconnectResponse](a, a.Request(&protocol.ReconnectRequest{PlayerId: tc.player, ReconnectToken: tc.token, LastSvrSeqNo: 4}, true))
		if resp.ErrorCode != protocol.ErrorCode_ErrorCode_ErrReconnectGWGetGatePlayerFailed || !resp.IsPermittedSilentLogin || resp.Timestamp == 0 || r.Session(a.Conv).Phase != Login {
			t.Fatal(tc, resp)
		}
	}
}

func TestReconnectWithTheTokenEntersAndReplacesTheOldSession(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	g := &recordingGame{}
	r := gt.NewRig(t, g)
	c := r.Client(50000)
	c.Connect()
	c.ExchangeKey()
	token := gt.Answer[protocol.LoginResponse](c, c.Request(&protocol.LoginRequest{Account: "3838"}, true)).ReconnectToken
	gt.Answer[protocol.EnterGameResponse](c, c.Request(&protocol.EnterGameRequest{}, true))
	r.Write("player/666/basic_info", ".{}")
	again := r.Client(50020)
	again.Connect()
	again.ExchangeKey()
	resp := gt.Answer[protocol.ReconnectResponse](again, again.Request(&protocol.ReconnectRequest{PlayerId: 666, ReconnectToken: token, LastSvrSeqNo: 41}, true))
	if resp.ErrorCode != protocol.ErrorCode_ErrorCode_Success || resp.LastRecvSeqNo != 41 || resp.IsPermittedSilentLogin {
		t.Fatal(resp)
	}
	s := r.GW.Sessions()
	if len(s) != 1 || s[0].Conv != again.Conv || r.GW.Holder(666) != s[0] {
		t.Fatal(s)
	}
	eq(t, g.events, []string{"entered 666 login", "released 666", "entered 666 reconnect"})
	if !strings.Contains(log.String(), fmt.Sprintf("session %d closed: replaced", c.Conv)) {
		t.Fatal(log.String())
	}
}

func TestEnteringTwiceAsOnePlayerKeepsOnlyTheNewerSession(t *testing.T) {
	g := &recordingGame{}
	r := gt.NewRig(t, g)
	a := r.Client(50030)
	a.Connect()
	a.LoginAndEnter("3838")
	b := r.Client(50031)
	b.Connect()
	b.LoginAndEnter("3838")
	if s := r.GW.Sessions(); len(s) != 1 || s[0].Conv != b.Conv {
		t.Fatal(s)
	}
	eq(t, g.events, []string{"entered 666 login", "released 666", "entered 666 login"})
}

func TestSequenceNumbersCountEveryFrameTheServerSends(t *testing.T) {
	r := gt.NewRig(t, nil)
	c := r.Client(50000)
	c.Connect()
	c.LoginAndEnter("3838")
	c.Response(c.Request(&protocol.HeartbeatRequest{}, true))
	var seqs []uint32
	for _, f := range c.Received {
		seqs = append(seqs, f.SeqNo)
	}
	eq(t, seqs, []uint32{1, 2, 3, 4})
	eq(t, r.GW.Sessions()[0].LastSeqNo(), 4)
}

func TestMessagePairsFollowTheNames(t *testing.T) {
	if r, ok := ResponseFor(protocol.IDLoginRequest); !ok || r != protocol.IDLoginResponse {
		t.Fatal(r)
	}
	if r, ok := ResponseFor(protocol.IDSecWorldSEndFlowRequest); !ok || r != protocol.IDSecWorldSEndFlowResponse {
		t.Fatal(r)
	}
	for _, id := range []protocol.MessageID{protocol.IDLoginNotify, protocol.IDLoginResponse} {
		if _, ok := ResponseFor(id); ok {
			t.Fatal(id.Name())
		}
	}
}

func TestAConnectDatagramOverRealUdpGetsItsConversationID(t *testing.T) {
	log, restore := common.CaptureLog()
	defer restore()
	keys, _ := gt.KeyWrapper()
	host, err := BindUDP(netip.MustParseAddrPort("127.0.0.1:0"))
	if err != nil {
		t.Fatal(err)
	}
	gw := New(NewPlayers(t.TempDir()), keys, SystemClock(), host.Send, &NoGame{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { host.Run(gw, stop); close(done) }()
	defer func() { close(stop); <-done; host.Close() }()
	cl, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	server := net.UDPAddrFromAddrPort(host.LocalAddr())
	cl.WriteToUDP([]byte{1, 2, 3}, server) // dropped
	connect := make([]byte, ConnectSize)
	connect[0] = ConnectByte
	cl.WriteToUDP(connect, server)
	buf := make([]byte, 2000)
	cl.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := cl.ReadFromUDP(buf)
	if err != nil || n != 5 || buf[0] != ConnectReplyByte || binary.LittleEndian.Uint32(buf[1:]) != 1 {
		t.Fatal(n, err)
	}
	// A KCP push carrying a ProtoKeyRequest frame comes back acknowledged and answered.
	k := kcp.New(1, func(d []byte) { cl.WriteToUDP(d, server) })
	k.Stream = true
	k.Update(0)
	k.Update(10)
	k.Send((&wire.Frame{Type: wire.Request, SeqNo: 1, RpcID: 1, MessageID: protocol.IDProtoKeyRequest}).Encode())
	k.Flush()
	var reader wire.FrameReader
	var answered []*wire.Frame
	for now := uint32(20); len(answered) == 0 && now < 5000; now += 10 {
		cl.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, err := cl.ReadFromUDP(buf)
		if err != nil || k.Input(buf[:n]) != 0 {
			t.Fatal(err)
		}
		k.Update(now)
		for m := k.Receive(); m != nil; m = k.Receive() {
			reader.Append(m)
		}
		if f, _ := reader.Next(); f != nil {
			answered = append(answered, f)
		}
	}
	if len(answered) != 1 || !bytes.Contains([]byte(log.String()), []byte("info(gateway): session 1 from 127.0.0.1:")) {
		t.Fatal(len(answered), log.String())
	}
}
