// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/common"
	"tidelab/internal/kcp"
	"tidelab/internal/wire"
	"tidelab/internal/zon"
	"tidelab/protocol"
)

type Phase int

const (
	// Login: only the login requests are accepted (§6).
	Login Phase = iota
	// InGame: requests go to their handlers.
	InGame
	Closed
)

// MaxFailedSends is the sends in a row that may fail before the session is closed.
const MaxFailedSends = 3

// Session is one client connection: its KCP conversation, the frame stream, the session key
// and the server's sequence counter (§2–§5).
type Session struct {
	g      *Gateway
	Conv   uint32
	Remote netip.AddrPort
	Phase  Phase
	// PlayerID is set once LoginRequest or ReconnectRequest named one; 0 before.
	PlayerID int32
	// HoldsPlayer: this session entered the game as its player.
	HoldsPlayer bool

	kcp           *kcp.KCP
	reader        wire.FrameReader
	cipher        *wire.Cipher
	seqNo         uint32
	failedSends   int
	startMs       int64
	lastReceiveMs int64
	nextUpdate    uint32
}

func newSession(g *Gateway, conv uint32, remote netip.AddrPort, startMs int64) *Session {
	s := &Session{g: g, Conv: conv, Remote: remote, startMs: startMs, lastReceiveMs: startMs}
	s.kcp = kcp.New(conv, func(d []byte) { g.send(remote, append([]byte{}, d...)) })
	return s
}

// LastSeqNo is the sequence number of the last frame the server sent; 0 before the first.
func (s *Session) LastSeqNo() uint32 { return s.seqNo }
func (s *Session) IsEncrypted() bool { return s.cipher != nil }

// kcpTime is milliseconds since the session started.
func (s *Session) kcpTime(nowMs int64) uint32 { return uint32(nowMs - s.startMs) }

func (s *Session) setKey(key []byte) error {
	c, err := wire.NewCipher(key)
	if err != nil {
		return err
	}
	s.cipher = c
	return nil
}

func (s *Session) decrypt(f *wire.Frame) ([]byte, error) {
	if s.cipher == nil {
		return f.Payload, nil
	}
	return s.cipher.Decrypt(f.Payload, f.SeqNo)
}

// Push sends a push; panics if m has no message id (a programming error).
func (s *Session) Push(m proto.Message) {
	id, ok := protocol.IDOf(m)
	if !ok {
		panic(fmt.Sprintf("gateway: %s has no message id", m.ProtoReflect().Descriptor().Name()))
	}
	s.send(wire.Push, 0, id, protocol.Marshal(m), true)
}

// PushID sends m under the given id.
func (s *Session) PushID(id protocol.MessageID, m proto.Message) {
	s.send(wire.Push, 0, id, protocol.Marshal(m), true)
}

func (s *Session) answer(req *wire.Frame, m proto.Message) {
	id, _ := protocol.IDOf(m)
	s.Respond(req.RpcID, id, protocol.Marshal(m), true)
}

// Respond sends a response frame.
func (s *Session) Respond(rpcID uint16, responseID protocol.MessageID, plain []byte, encrypt bool) {
	s.send(wire.Response, rpcID, responseID, plain, encrypt)
}

func (s *Session) send(t wire.FrameType, rpcID uint16, id protocol.MessageID, plain []byte, encrypt bool) {
	if s.Phase == Closed {
		return
	}
	s.seqNo++
	payload := plain
	if payload == nil {
		payload = []byte{}
	}
	if encrypt && s.cipher != nil {
		payload = s.cipher.Encrypt(plain, s.seqNo)
	}
	frame := (&wire.Frame{Type: t, SeqNo: s.seqNo, RpcID: rpcID, MessageID: id, Payload: payload}).Encode()
	if s.kcp.Send(frame) {
		s.failedSends = 0
		return
	}
	s.failedSends++
	common.Error("gateway", fmt.Sprintf("session %d: %s of %d bytes does not fit in %d KCP segments", s.Conv, Name(id), len(frame), kcp.MaxFragments))
	if s.failedSends >= MaxFailedSends {
		s.g.Close(s, "sends failed")
	}
}

// ----- players -----

const (
	// FirstPlayerID is the first player id when player/next does not exist (R-170: 666, was 777).
	FirstPlayerID = 666
	TokenLength   = 64
)

// Players is who a login is: account links, the player id counter and reconnect tokens under
// state/ (§6, §8; save-format.md §3, §4, §5.12).
type Players struct {
	mu       sync.Mutex
	StateDir string
}

func NewPlayers(stateDir string) *Players { return &Players{StateDir: stateDir} }

// SafeName is the account name as a file name: each of < > : " / \ | ? * and every character
// below 32 becomes _.
func SafeName(account string) string {
	var b strings.Builder
	for _, r := range account {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IsLinkable says whether SafeName of the account can be a file in account/link/.
func IsLinkable(account string) bool {
	s := SafeName(account)
	return s != "" && s != "." && s != ".."
}

func (p *Players) LinkPath(account string) string {
	return filepath.Join(p.StateDir, "account", "link", SafeName(account))
}

func (p *Players) PlayerDir(id int32) string {
	return filepath.Join(p.StateDir, "player", strconv.Itoa(int(id)))
}

// LookUpOrCreate is the player the account links to, linking it to a new player id first if
// it has none.
func (p *Players) LookUpOrCreate(account string) (int32, error) {
	if !IsLinkable(account) {
		return 0, fmt.Errorf("the account name %s cannot name a link file", account)
	}
	path := p.LinkPath(account)
	p.mu.Lock()
	defer p.mu.Unlock()
	if common.FileExists(path) {
		link, err := zon.ParseFile(path)
		if err != nil {
			return 0, err
		}
		f := link.Field("player_id")
		if f == nil {
			return 0, &zon.Error{Msg: "field .player_id is missing"}
		}
		return f.AsInt32()
	}
	id, err := p.takePlayerID()
	if err != nil {
		return 0, err
	}
	if _, err := common.WriteAtomic(path, zon.Write(zon.NewStruct(zon.F("player_id", zon.Int(int64(id)))))); err != nil {
		return 0, err
	}
	common.Info("login", fmt.Sprintf("account %s linked to new player %d", account, id))
	return id, nil
}

// NewReconnectToken is a new reconnect token for the player, replacing the old one on disk.
func (p *Players) NewReconnectToken(id int32) (string, error) {
	b := make([]byte, TokenLength/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	_, err := common.WriteAtomic(filepath.Join(p.PlayerDir(id), "reconnect_token"), token)
	return token, err
}

// MayReconnect: the player exists (its basic_info) and the token is the one last handed out.
func (p *Players) MayReconnect(id int32, token string) bool {
	if id <= 0 || len(token) != TokenLength {
		return false
	}
	dir := p.PlayerDir(id)
	tokenPath := filepath.Join(dir, "reconnect_token")
	if !common.FileExists(filepath.Join(dir, "basic_info")) || !common.FileExists(tokenPath) {
		return false
	}
	saved, err := os.ReadFile(tokenPath)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(saved, []byte(token)) == 1
}

func (p *Players) takePlayerID() (int32, error) {
	path := filepath.Join(p.StateDir, "player", "next")
	next := int64(FirstPlayerID)
	text, err := os.ReadFile(path)
	switch {
	case err == nil:
		fields := strings.Fields(string(text))
		first := ""
		if len(fields) > 0 {
			first = fields[0]
		}
		n, perr := strconv.ParseInt(first, 10, 32)
		if perr != nil {
			return 0, fmt.Errorf("%s does not start with a number: %s", path, first)
		}
		next = n
	case !errors.Is(err, fs.ErrNotExist):
		return 0, err
	}
	if _, err := common.WriteAtomic(path, strconv.FormatInt(next+1, 10)+"\n"); err != nil {
		return 0, err
	}
	return int32(next), nil
}

// ----- the UDP socket -----

const (
	// MaxDatagram is the largest datagram read; a longer one is dropped.
	MaxDatagram = 1500
	// DatagramsPerTurn is the datagrams read in one turn before the timers run again.
	DatagramsPerTurn = 64
)

// UDPHost is the gateway's socket (§1): one socket for every session, and the loop that
// alternates between reading datagrams and running the gateway's timers.
type UDPHost struct {
	conn *net.UDPConn
}

// BindUDP binds addr. (Go's runtime already turns off Windows' SIO_UDP_CONNRESET, so an ICMP
// "port unreachable" does not make the next read fail.)
func BindUDP(addr netip.AddrPort) (*UDPHost, error) {
	c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(addr))
	if err != nil {
		return nil, err
	}
	return &UDPHost{c}, nil
}

func (h *UDPHost) LocalAddr() netip.AddrPort { return h.conn.LocalAddr().(*net.UDPAddr).AddrPort() }

func (h *UDPHost) Close() error { return h.conn.Close() }

func (h *UDPHost) Send(to netip.AddrPort, datagram []byte) {
	if _, err := h.conn.WriteToUDPAddrPort(datagram, to); err != nil {
		common.Warn("gateway", fmt.Sprintf("send to %s failed: %v", to, err))
	}
}

// Run runs the gateway until stop is closed.
func (h *UDPHost) Run(g *Gateway, stop <-chan struct{}) {
	buf := make([]byte, MaxDatagram+1)
	for {
		select {
		case <-stop:
			return
		default:
		}
		wait := time.Duration(g.NextTickMs(50)) * time.Millisecond
		h.conn.SetReadDeadline(time.Now().Add(wait))
		for i := 0; i < DatagramsPerTurn; i++ {
			n, from, err := h.conn.ReadFromUDPAddrPort(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break
				}
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue // a reset or an oversized datagram
			}
			if n <= MaxDatagram {
				g.Receive(netip.AddrPortFrom(from.Addr().Unmap(), from.Port()), buf[:n])
			}
			// Read what else is already there, without waiting long.
			h.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
		}
		g.Tick()
	}
}
