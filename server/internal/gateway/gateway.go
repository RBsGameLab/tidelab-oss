// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package gateway is the game gateway (specs/network.md): connect handshakes, KCP
// conversations, frames, the key exchange and the login requests, then dispatch to the game.
// Ported from our C# Tidelab.GameServer.Gateway. Single-threaded: the caller feeds datagrams
// to Receive and calls Tick by NextTickMs; datagrams go out through the send callback.
package gateway

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/kcp"
	"tidelab/internal/wire"
	"tidelab/protocol"
)

// Game is what happens when a session enters the game or lets its player go.
type Game interface {
	// Handlers are the requests and pushes after entering (§9).
	Handlers() *MessageHandlers
	// Entered: load the player's state and send the enter-game sequence.
	Entered(s *Session, reconnect bool)
	// Released: the session lets its player go (closed or replaced): save the player's state.
	Released(s *Session)
	// Heartbeat of a session in the game, before its response goes out.
	Heartbeat(s *Session)
}

// NoGame only answers what the gateway itself answers (R-143d).
type NoGame struct{ h MessageHandlers }

func (g *NoGame) Handlers() *MessageHandlers { return &g.h }
func (*NoGame) Entered(*Session, bool)       {}
func (*NoGame) Released(*Session)            {}
func (*NoGame) Heartbeat(*Session)           {}

// Clock is the gateway's time: monotonic milliseconds and wall Unix milliseconds.
type Clock interface {
	Ms() int64
	UnixMs() int64
}

type systemClock struct{ start time.Time }

func (c systemClock) Ms() int64     { return time.Since(c.start).Milliseconds() }
func (c systemClock) UnixMs() int64 { return time.Now().UnixMilli() }

// SystemClock reads the system clocks.
func SystemClock() Clock { return systemClock{time.Now()} }

const (
	ConnectSize      = 35
	ConnectByte      = 0xED
	ConnectReplyByte = 0xEE
	// LoginIdleMs, GameIdleMs: idle time before entering the game, then after (§3).
	LoginIdleMs = 10_000
	GameIdleMs  = 5 * 60_000
)

// Gateway serves every session.
type Gateway struct {
	players  *Players
	keys     *wire.KeyWrapper
	clock    Clock
	send     func(netip.AddrPort, []byte)
	game     Game
	sessions map[uint32]*Session
	order    []uint32 // conversation ids in the order sessions were made, for stable iteration
	holders  map[int32]*Session
	lastConv uint32
}

func New(players *Players, keys *wire.KeyWrapper, clock Clock, send func(netip.AddrPort, []byte), game Game) *Gateway {
	return &Gateway{players: players, keys: keys, clock: clock, send: send, game: game,
		sessions: map[uint32]*Session{}, holders: map[int32]*Session{}}
}

// Sessions are the open sessions, oldest first.
func (g *Gateway) Sessions() []*Session {
	out := make([]*Session, 0, len(g.order))
	for _, c := range g.order {
		out = append(out, g.sessions[c])
	}
	return out
}

// Holder is the session holding a player, if any.
func (g *Gateway) Holder(playerID int32) *Session { return g.holders[playerID] }

func (g *Gateway) NowMs() int64 { return g.clock.Ms() }

// Receive handles one datagram.
func (g *Gateway) Receive(from netip.AddrPort, datagram []byte) {
	now := g.NowMs()
	if len(datagram) == ConnectSize && datagram[0] == ConnectByte {
		g.connect(from, now)
		return
	}
	if len(datagram) < kcp.HeaderSize {
		return
	}
	conv := binary.LittleEndian.Uint32(datagram)
	s, ok := g.sessions[conv]
	if !ok || s.Remote != from {
		return
	}
	s.lastReceiveMs = now
	s.kcp.Update(s.kcpTime(now))
	if s.kcp.Input(datagram) < 0 {
		common.Warn("gateway", fmt.Sprintf("session %d: malformed KCP datagram of %d bytes", conv, len(datagram)))
		return
	}
	for s.Phase != Closed {
		msg := s.kcp.Receive()
		if msg == nil {
			break
		}
		s.reader.Append(msg)
		for s.Phase != Closed {
			f, err := s.reader.Next()
			if err != nil {
				g.Close(s, err.Error())
				break
			}
			if f == nil {
				break
			}
			g.handle(s, f)
		}
	}
	if s.Phase == Closed {
		return
	}
	s.kcp.Flush()
	s.nextUpdate = s.kcp.Check(s.kcpTime(now))
}

// Tick runs KCP timers and closes idle or dead sessions.
func (g *Gateway) Tick() {
	now := g.NowMs()
	for _, s := range g.Sessions() {
		idle := int64(LoginIdleMs)
		if s.Phase == InGame {
			idle = GameIdleMs
		}
		if now-s.lastReceiveMs > idle {
			g.Close(s, "timed out")
			continue
		}
		t := s.kcpTime(now)
		if int32(t-s.nextUpdate) >= 0 {
			s.kcp.Update(t)
			s.nextUpdate = s.kcp.Check(t)
		}
		if s.kcp.IsDead() {
			g.Close(s, "dead link")
		}
	}
}

// NextTickMs is the milliseconds until Tick has something to do (at most cap).
func (g *Gateway) NextTickMs(cap int64) int64 {
	now := g.NowMs()
	wait := cap
	for _, s := range g.sessions {
		due := int64(int32(s.nextUpdate - s.kcpTime(now)))
		wait = min(wait, max(0, due))
	}
	return wait
}

// CloseAll closes every session (the server shuts down).
func (g *Gateway) CloseAll(reason string) {
	for _, s := range g.Sessions() {
		g.Close(s, reason)
	}
}

func (g *Gateway) Close(s *Session, reason string) {
	if s.Phase == Closed {
		return
	}
	s.Phase = Closed
	delete(g.sessions, s.Conv)
	for i, c := range g.order {
		if c == s.Conv {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	if s.HoldsPlayer {
		s.HoldsPlayer = false
		delete(g.holders, s.PlayerID)
		g.game.Released(s)
	}
	common.Info("gateway", fmt.Sprintf("session %d closed: %s", s.Conv, reason))
}

func (g *Gateway) connect(from netip.AddrPort, now int64) {
	for {
		g.lastConv++
		if _, used := g.sessions[g.lastConv]; g.lastConv != 0 && !used {
			break
		}
	}
	s := newSession(g, g.lastConv, from, now)
	g.sessions[s.Conv] = s
	g.order = append(g.order, s.Conv)
	s.kcp.Update(0)
	s.nextUpdate = s.kcp.Check(0)
	reply := make([]byte, 5)
	reply[0] = ConnectReplyByte
	binary.LittleEndian.PutUint32(reply[1:], s.Conv)
	g.send(from, reply)
	common.Info("gateway", fmt.Sprintf("session %d from %s", s.Conv, from))
}

// Name is a message id's name for the logs.
func Name(id protocol.MessageID) string {
	if n := id.Name(); n != "" {
		return n
	}
	return "unknown message"
}

func hexPayload(p []byte) string {
	if len(p) > 0 && len(p) <= 512 {
		return ", payload: " + hex.EncodeToString(p)
	}
	return ""
}

func (g *Gateway) handle(s *Session, f *wire.Frame) {
	if f.Type == wire.Push {
		if s.Phase != InGame {
			g.Close(s, fmt.Sprintf("push %s before entering the game", Name(f.MessageID)))
			return
		}
		payload, ok := g.decrypt(s, f)
		if !ok {
			return
		}
		push := g.game.Handlers().pushes[f.MessageID]
		if push == nil {
			common.Warn("connection", fmt.Sprintf("no handler for %s (msg_id: %d)%s", Name(f.MessageID), uint16(f.MessageID), hexPayload(payload)))
			return
		}
		g.run(s, f, func() error { return push(s, payload) })
		return
	}
	if f.Type != wire.Request {
		g.Close(s, fmt.Sprintf("unexpected %s frame", f.Type))
		return
	}
	// The client never encrypts ProtoKeyRequest (Net: SetEnType for 111).
	plain := f.Payload
	if f.MessageID != protocol.IDProtoKeyRequest {
		var ok bool
		if plain, ok = g.decrypt(s, f); !ok {
			return
		}
	}
	switch {
	case f.MessageID == protocol.IDProtoKeyRequest && s.Phase == Login:
		g.run(s, f, func() error { return g.protoKey(s, f) })
		return
	case f.MessageID == protocol.IDLoginRequest && s.Phase == Login:
		g.run(s, f, func() error {
			var req protocol.LoginRequest
			if err := unmarshal(plain, &req); err != nil {
				return err
			}
			g.login(s, f, &req)
			return nil
		})
		return
	case f.MessageID == protocol.IDEnterGameRequest && s.Phase == Login:
		g.run(s, f, func() error { g.enterGame(s, f); return nil })
		return
	case f.MessageID == protocol.IDReconnectRequest && s.Phase == Login:
		g.run(s, f, func() error {
			var req protocol.ReconnectRequest
			if err := unmarshal(plain, &req); err != nil {
				return err
			}
			g.reconnect(s, f, &req)
			return nil
		})
		return
	case f.MessageID == protocol.IDHeartbeatRequest:
		g.run(s, f, func() error {
			if s.Phase == InGame {
				g.game.Heartbeat(s)
			}
			s.answer(f, &protocol.HeartbeatResponse{})
			return nil
		})
		return
	}
	if s.Phase != InGame {
		g.Close(s, Name(f.MessageID)+" before entering the game")
		return
	}
	responseID, ok := ResponseFor(f.MessageID)
	if !ok {
		g.Close(s, fmt.Sprintf("%s (msg_id: %d) is not a request", Name(f.MessageID), uint16(f.MessageID)))
		return
	}
	handler := g.game.Handlers().requests[f.MessageID]
	if handler == nil {
		common.Warn("connection", fmt.Sprintf("no handler for %s (msg_id: %d), replied empty%s", Name(f.MessageID), uint16(f.MessageID), hexPayload(plain)))
		s.Respond(f.RpcID, responseID, nil, true)
		return
	}
	g.run(s, f, func() error {
		resp, err := handler(s, plain)
		if err != nil {
			return err
		}
		var b []byte
		if resp != nil {
			b = protocol.Marshal(resp)
			if code := protocol.ErrorCodeOf(resp); code != protocol.ErrorCode_ErrorCode_Success {
				common.Warn("reply", fmt.Sprintf("%s answered %s (%d)", Name(f.MessageID), code, int32(code)))
			}
		}
		s.Respond(f.RpcID, responseID, b, true)
		if after := g.game.Handlers().AfterResponse; after != nil {
			after(s)
		}
		return nil
	})
}

// decodeError is a message that does not decode.
type decodeError struct{ err error }

func (e *decodeError) Error() string { return e.err.Error() }

func unmarshal(b []byte, m proto.Message) error {
	if err := proto.Unmarshal(b, m); err != nil {
		return &decodeError{err}
	}
	return nil
}

// run runs a handler. A message that does not decode closes the session (§9); so does a
// handler that panics, after logging it, so one bad handler never stops the server.
func (g *Gateway) run(s *Session, f *wire.Frame, handle func() error) {
	defer func() {
		if r := recover(); r != nil {
			common.Error("gateway", fmt.Sprintf("session %d: handler of %s threw: %v\n%s", s.Conv, Name(f.MessageID), r, debug.Stack()))
			g.Close(s, "a handler threw")
		}
	}()
	if err := handle(); err != nil {
		var de *decodeError
		if errors.As(err, &de) {
			g.Close(s, fmt.Sprintf("%s does not decode: %s", Name(f.MessageID), de.err))
			return
		}
		common.Error("gateway", fmt.Sprintf("session %d: handler of %s failed: %v", s.Conv, Name(f.MessageID), err))
		g.Close(s, "a handler threw")
	}
}

func (g *Gateway) decrypt(s *Session, f *wire.Frame) ([]byte, bool) {
	b, err := s.decrypt(f)
	if err != nil {
		g.Close(s, fmt.Sprintf("%s (seq %d) does not decrypt: %s", Name(f.MessageID), f.SeqNo, err))
		return nil, false
	}
	return b, true
}

func (g *Gateway) protoKey(s *Session, f *wire.Frame) error {
	if g.keys == nil {
		// No client key (§5.1): Type 0, the session stays in the clear both ways. The patched
		// client turns its per-message ciphers off on Type 0 (R-173b); the stock client does not.
		s.Respond(f.RpcID, protocol.IDProtoKeyResponse, protocol.Marshal(&protocol.ProtoKeyResponse{Type: 0}), false)
		s.cipher = nil
		return nil
	}
	key, wrapped, err := g.keys.NewKey()
	if err != nil {
		return err
	}
	// Its response goes out in the clear; every frame after it is under the new key.
	s.Respond(f.RpcID, protocol.IDProtoKeyResponse, protocol.Marshal(&protocol.ProtoKeyResponse{Type: 2, Key: wrapped}), false)
	return s.setKey(key)
}

func (g *Gateway) login(s *Session, f *wire.Frame, req *protocol.LoginRequest) {
	if !IsLinkable(req.Account) {
		n := min(len(f.Payload), 24)
		common.Warn("gateway", fmt.Sprintf("session %d: account name %s cannot name a link file (payload %d bytes, head %x, encrypted %v)",
			s.Conv, req.Account, len(f.Payload), f.Payload[:n], s.IsEncrypted()))
		s.answer(f, &protocol.LoginResponse{Code: protocol.ErrorCode_ErrorCode_GateLoginUserIdErr, Timestamp: g.clock.UnixMs()})
		return
	}
	playerID, err := g.players.LookUpOrCreate(req.Account)
	var token string
	if err == nil {
		token, err = g.players.NewReconnectToken(playerID)
	}
	if err != nil {
		common.Error("gateway", fmt.Sprintf("session %d: login of %s failed: %v", s.Conv, req.Account, err))
		s.answer(f, &protocol.LoginResponse{Code: protocol.ErrorCode_ErrorCode_GateLoginException, Timestamp: g.clock.UnixMs()})
		return
	}
	s.PlayerID = playerID
	common.Info("gateway", fmt.Sprintf("session %d: account %s logged in as player %d", s.Conv, req.Account, playerID))
	s.answer(f, &protocol.LoginResponse{ReconnectToken: token, Timestamp: g.clock.UnixMs()})
}

func (g *Gateway) enterGame(s *Session, f *wire.Frame) {
	if s.PlayerID == 0 {
		g.Close(s, "EnterGameRequest before LoginRequest")
		return
	}
	g.hold(s)
	s.answer(f, &protocol.EnterGameResponse{})
	s.Phase = InGame
	common.Info("gateway", fmt.Sprintf("session %d: player %d entered the game", s.Conv, s.PlayerID))
	g.game.Entered(s, false)
}

func (g *Gateway) reconnect(s *Session, f *wire.Frame, req *protocol.ReconnectRequest) {
	if !g.players.MayReconnect(req.PlayerId, req.ReconnectToken) {
		common.Info("gateway", fmt.Sprintf("session %d: reconnect of player %d refused", s.Conv, req.PlayerId))
		s.answer(f, &protocol.ReconnectResponse{
			ErrorCode:              protocol.ErrorCode_ErrorCode_ErrReconnectGWGetGatePlayerFailed,
			Timestamp:              g.clock.UnixMs(),
			IsPermittedSilentLogin: true,
		})
		return
	}
	s.PlayerID = req.PlayerId
	g.hold(s)
	s.Phase = InGame
	s.answer(f, &protocol.ReconnectResponse{
		ErrorCode:     protocol.ErrorCode_ErrorCode_Success,
		LastRecvSeqNo: req.LastSvrSeqNo,
		Timestamp:     g.clock.UnixMs(),
	})
	common.Info("gateway", fmt.Sprintf("session %d: player %d reconnected", s.Conv, s.PlayerID))
	g.game.Entered(s, true)
}

// hold makes the session the player's only one: an older session is closed first (§7).
func (g *Gateway) hold(s *Session) {
	if old, ok := g.holders[s.PlayerID]; ok && old != s {
		g.Close(old, "replaced")
	}
	g.holders[s.PlayerID] = s
	s.HoldsPlayer = true
}

var pairs = func() map[protocol.MessageID]protocol.MessageID {
	out := map[protocol.MessageID]protocol.MessageID{}
	for _, id := range protocol.AllIDs() {
		if base, ok := strings.CutSuffix(id.Name(), "Request"); ok {
			if resp, ok := protocol.IDByName(base + "Response"); ok {
				out[id] = resp
			}
		}
	}
	return out
}()

// ResponseFor is the response id for a request id: XRequest is answered by XResponse. The
// client's own lists (NetDefine.RequestMessageIds, ResponseMessageIds) hold the same pairs.
func ResponseFor(request protocol.MessageID) (protocol.MessageID, bool) {
	r, ok := pairs[request]
	return r, ok
}

// RequestFunc handles a request payload; the message it returns goes out as the paired response.
type RequestFunc func(s *Session, payload []byte) (proto.Message, error)

// PushFunc handles a push payload.
type PushFunc func(s *Session, payload []byte) error

// MessageHandlers are the handlers of requests and pushes that arrive after the session
// entered the game (§9). Later stages register theirs here.
type MessageHandlers struct {
	requests map[protocol.MessageID]RequestFunc
	pushes   map[protocol.MessageID]PushFunc
	// AfterResponse runs after a handled request's response went out (pushes that must follow it).
	AfterResponse func(*Session)
}

// OnRequest handles request type T.
func OnRequest[T any, PT interface {
	*T
	proto.Message
}](h *MessageHandlers, fn func(*Session, PT) proto.Message) {
	id, ok := protocol.IDOf(PT(new(T)))
	if !ok {
		panic("gateway: the message has no id")
	}
	if _, ok := ResponseFor(id); !ok {
		panic(fmt.Sprintf("gateway: %s has no paired response", id.Name()))
	}
	if h.requests == nil {
		h.requests = map[protocol.MessageID]RequestFunc{}
	}
	h.requests[id] = func(s *Session, payload []byte) (proto.Message, error) {
		m := PT(new(T))
		if err := unmarshal(payload, m); err != nil {
			return nil, err
		}
		return fn(s, m), nil
	}
}

// OnPush handles push type T.
func OnPush[T any, PT interface {
	*T
	proto.Message
}](h *MessageHandlers, fn func(*Session, PT)) {
	id, ok := protocol.IDOf(PT(new(T)))
	if !ok {
		panic("gateway: the message has no id")
	}
	if h.pushes == nil {
		h.pushes = map[protocol.MessageID]PushFunc{}
	}
	h.pushes[id] = func(s *Session, payload []byte) error {
		m := PT(new(T))
		if err := unmarshal(payload, m); err != nil {
			return err
		}
		fn(s, m)
		return nil
	}
}

// HasRequest says whether a request id has a handler.
func (h *MessageHandlers) HasRequest(id protocol.MessageID) bool { return h.requests[id] != nil }

// HasPush says whether a push id has a handler.
func (h *MessageHandlers) HasPush(id protocol.MessageID) bool { return h.pushes[id] != nil }
