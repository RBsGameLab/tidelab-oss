// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package robot

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/game"
	"tidelab/internal/gateway"
	"tidelab/internal/kcp"
	"tidelab/internal/quests"
	"tidelab/internal/wire"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// MsPerRequest is how far the virtual clock moves for each request the robot sends.
const MsPerRequest = 20

// transportTime is the in-memory KCP transport's own monotonic clock.
type transportTime struct{ ms int64 }

func (t *transportTime) Ms() int64     { return t.ms }
func (t *transportTime) UnixMs() int64 { return 1_700_000_000_000 + t.ms }

// LogCapture captures the server's warnings and errors per run and optionally echoes the log
// lines (--verbose).
type LogCapture struct {
	mu           sync.Mutex
	echo         io.Writer
	line         strings.Builder
	WarningCount int
	FirstWarning string
}

func NewLogCapture(echo io.Writer) *LogCapture { return &LogCapture{echo: echo} }

func (c *LogCapture) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.WarningCount, c.FirstWarning = 0, ""
}

func (c *LogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range string(p) {
		switch r {
		case '\n':
			c.flushLine()
		case '\r':
		default:
			c.line.WriteRune(r)
		}
	}
	return len(p), nil
}

func (c *LogCapture) Warnings() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.WarningCount, c.FirstWarning
}

func (c *LogCapture) flushLine() {
	text := c.line.String()
	c.line.Reset()
	if strings.HasPrefix(text, "warning(") || strings.HasPrefix(text, "error(") {
		if c.WarningCount == 0 {
			t := text
			if r := []rune(t); len(r) > 240 {
				t = string(r[:240])
			}
			c.FirstWarning = SanitizeField(t)
		}
		c.WarningCount++
	}
	if c.echo != nil {
		fmt.Fprintln(c.echo, text)
	}
}

// Host runs the real gateway, WorldGame and combat in memory on a private copy of a player's
// save (no network, no encryption, virtual clock).
type Host struct {
	tempStateDir string
	ownsTempDir  bool
	transport    *transportTime
	clientAddr   netip.AddrPort
	toClient     [][]byte
	reader       wire.FrameReader
	clientKcp    *kcp.KCP
	conv         uint32
	seqNo        uint32
	rpcID        uint16
	lastReceive  int64
	disposed     bool

	PlayerID       int32
	Tables         *data.DataTables
	WorldData      *world.WorldData
	Quests         *quests.Catalog
	Clock          *common.GameClock
	Game           *game.WorldGame
	Gateway        *gateway.Gateway
	GatewaySession *gateway.Session
	Session        *game.PlayerSession

	FramesReceived int64
	RequestsSent   int64
}

// HostConfig is what a host starts from.
type HostConfig struct {
	PlayerID          int32
	Epoch             int64
	Tables            *data.DataTables
	WorldData         *world.WorldData
	Quests            *quests.Catalog
	CommonParamPath   string
	OffersEnabled     bool
	RestorePending    bool
	SeedDefaultEchoes bool
	OnLoginFrame      func(*wire.Frame)
}

type quietClient struct{ id int32 }

func (q quietClient) PlayerID() int32                        { return q.id }
func (quietClient) Push(proto.Message)                       {}
func (quietClient) PushID(protocol.MessageID, proto.Message) {}

var (
	hostKeyOnce sync.Once
	hostKey     *wire.KeyWrapper
)

func robotKeys() *wire.KeyWrapper {
	hostKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		if hostKey, err = wire.KeyWrapperFromDER(x509.MarshalPKCS1PublicKey(&k.PublicKey)); err != nil {
			panic(err)
		}
	})
	return hostKey
}

func newHost(tempStateDir string, owns bool, cfg HostConfig) (*Host, error) {
	h := &Host{
		tempStateDir: tempStateDir, ownsTempDir: owns, transport: &transportTime{},
		clientAddr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 40001),
		PlayerID:   cfg.PlayerID, Tables: cfg.Tables, WorldData: cfg.WorldData, Quests: cfg.Quests,
	}
	h.Clock = common.NewGameClock(nil)
	h.Clock.UseVirtual(cfg.Epoch)
	players := gateway.NewPlayers(tempStateDir)
	if err := writeAccountLink(tempStateDir, "robot", cfg.PlayerID); err != nil {
		return nil, err
	}
	shared := combat.NewShared(cfg.Tables, cfg.CommonParamPath)
	h.Game = game.NewWorldGame(tempStateDir, cfg.WorldData, h.Clock, shared)
	h.Game.Rng = combat.NewDotNetRandom(int32(cfg.Epoch))
	h.Game.SeedDefaultEchoes = cfg.SeedDefaultEchoes
	if cfg.Quests != nil {
		h.Game.EnableQuests(cfg.Quests)
	}
	h.Gateway = gateway.New(players, robotKeys(), h.transport, func(_ netip.AddrPort, b []byte) {
		h.toClient = append(h.toClient, append([]byte(nil), b...))
	}, h.Game)

	// The player's quest state exists before EnterGameRequest runs the join's sync.
	if cfg.Quests != nil {
		pre := h.Game.NewSession(quietClient{cfg.PlayerID})
		if e := pre.Quests(); e != nil {
			e.OffersEnabled = cfg.OffersEnabled
		}
	}

	// 1. Handshake (0xED -> 0xEE + conv).
	connect := make([]byte, gateway.ConnectSize)
	connect[0] = gateway.ConnectByte
	h.Gateway.Receive(h.clientAddr, connect)
	if len(h.toClient) == 0 {
		return nil, fmt.Errorf("gateway did not reply to connect handshake")
	}
	reply := h.toClient[0]
	h.toClient = h.toClient[1:]
	if len(reply) != 5 || reply[0] != gateway.ConnectReplyByte {
		return nil, fmt.Errorf("unexpected connect reply")
	}
	h.conv = binary.LittleEndian.Uint32(reply[1:])
	h.clientKcp = kcp.New(h.conv, func(d []byte) {
		h.lastReceive = h.transport.ms
		h.Gateway.Receive(h.clientAddr, append([]byte(nil), d...))
	})
	h.clientKcp.Stream = true
	h.clientKcp.SetMtu(1000)
	h.clientKcp.SetWindows(256, 256)
	h.clientKcp.SetNoDelay(1, 10, 2, true)
	h.clientKcp.Update(0)

	// 2. Login (no ProtoKeyRequest, so frames stay unencrypted in memory).
	loggedIn := false
	h.rpcID++
	if err := h.sendFrameRaw(wire.Request, h.rpcID, protocol.IDLoginRequest, protocol.Marshal(&protocol.LoginRequest{Account: "robot", Token: "1"}), func(f *wire.Frame) {
		if f.MessageID == protocol.IDLoginResponse {
			loggedIn = true
		}
	}); err != nil {
		return nil, err
	}
	if !loggedIn {
		return nil, fmt.Errorf("gateway did not answer LoginRequest")
	}
	// 3. EnterGameRequest (the virtual clock moves one step, like a request).
	h.Clock.Advance(MsPerRequest)
	h.rpcID++
	if err := h.sendFrameRaw(wire.Request, h.rpcID, protocol.IDEnterGameRequest, protocol.Marshal(&protocol.EnterGameRequest{}), cfg.OnLoginFrame); err != nil {
		return nil, err
	}
	h.GatewaySession = h.Gateway.Holder(cfg.PlayerID)
	if h.GatewaySession == nil {
		return nil, fmt.Errorf("player %d did not enter the game", cfg.PlayerID)
	}
	h.Session = h.Game.Find(h.GatewaySession)
	if h.Session == nil {
		return nil, fmt.Errorf("WorldGame has no session for player %d", cfg.PlayerID)
	}
	// Restore-time fixes run only in ride mode (not story, side or dungeons).
	if e := h.Session.Quests(); e != nil {
		e.RestorePending = cfg.RestorePending
	}
	return h, nil
}

// StartFromCopy starts a host on a fresh temporary copy of sourceStateDir's player.
func StartFromCopy(sourceStateDir string, cfg HostConfig, preparePlayerDir func(string)) (*Host, error) {
	temp, err := os.MkdirTemp("", "tidelab-robot-")
	if err != nil {
		return nil, err
	}
	pid := strconv.Itoa(int(cfg.PlayerID))
	dst := filepath.Join(temp, "player", pid)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	if sourceStateDir != "" {
		src := filepath.Join(sourceStateDir, "player", pid)
		if st, err := os.Stat(src); err == nil && st.IsDir() {
			if _, err := CopyDirectory(src, dst); err != nil {
				os.RemoveAll(temp)
				return nil, err
			}
		}
	}
	if preparePlayerDir != nil {
		preparePlayerDir(dst)
	}
	h, err := newHost(temp, true, cfg)
	if err != nil {
		os.RemoveAll(temp)
		return nil, err
	}
	return h, nil
}

// StartInDirectory starts a host directly on stateDir (tests).
func StartInDirectory(stateDir string, cfg HostConfig) (*Host, error) {
	return newHost(stateDir, false, cfg)
}

func (h *Host) StateDir() string { return h.tempStateDir }

// SendRequest sends a request through the gateway; false when no handler is registered.
func (h *Host) SendRequest(m proto.Message, onFrame func(*wire.Frame)) bool {
	h.Clock.Advance(MsPerRequest)
	h.RequestsSent++
	id, _ := protocol.IDOf(m)
	if !h.Game.Handlers().HasRequest(id) {
		return false
	}
	h.rpcID++
	if err := h.sendFrameRaw(wire.Request, h.rpcID, id, protocol.Marshal(m), onFrame); err != nil {
		panic(err)
	}
	return true
}

// SendPush sends a push through the gateway; false when no handler is registered.
func (h *Host) SendPush(m proto.Message, onFrame func(*wire.Frame)) bool {
	h.Clock.Advance(MsPerRequest)
	h.RequestsSent++
	id, _ := protocol.IDOf(m)
	if !h.Game.Handlers().HasPush(id) {
		return false
	}
	if err := h.sendFrameRaw(wire.Push, 0, id, protocol.Marshal(m), onFrame); err != nil {
		panic(err)
	}
	return true
}

// DrainServerPushes flushes frames pushed by direct server calls (the kill path, SkipActive) out of KCP.
func (h *Host) DrainServerPushes(onFrame func(*wire.Frame)) {
	if h.GatewaySession.Phase == gateway.Closed {
		return
	}
	h.keepGatewayAlive()
	h.transport.ms += 100
	h.Gateway.Tick()
	h.pump(onFrame)
}

// SaveCheckpoint writes what is pending and copies the player's state to targetRoot.
func (h *Host) SaveCheckpoint(targetRoot string) (int, error) {
	h.Session.Release()
	pid := strconv.Itoa(int(h.PlayerID))
	src := filepath.Join(h.tempStateDir, "player", pid)
	dst := filepath.Join(targetRoot, "player", pid)
	os.RemoveAll(dst)
	return CopyDirectory(src, dst)
}

func (h *Host) sendFrameRaw(t wire.FrameType, rpc uint16, id protocol.MessageID, payload []byte, onFrame func(*wire.Frame)) error {
	h.seqNo++
	encoded := (&wire.Frame{Type: t, SeqNo: h.seqNo, RpcID: rpc, MessageID: id, Payload: payload}).Encode()
	h.transport.ms++
	h.clientKcp.Update(uint32(h.transport.ms))
	if !h.clientKcp.Send(encoded) {
		return fmt.Errorf("frame %s (%d bytes) exceeds KCP max fragments", id.Name(), len(encoded))
	}
	h.clientKcp.Flush()
	h.pump(onFrame)
	return nil
}

func (h *Host) keepGatewayAlive() {
	if h.transport.ms-h.lastReceive < gateway.GameIdleMs/2 {
		return
	}
	// A 24-byte KCP CmdWindowAsk (83) refreshes the gateway's idle timer with no game effect.
	ask := make([]byte, kcp.HeaderSize)
	binary.LittleEndian.PutUint32(ask, h.conv)
	ask[4] = 83
	binary.LittleEndian.PutUint16(ask[6:], 256)
	h.lastReceive = h.transport.ms
	h.Gateway.Receive(h.clientAddr, ask)
}

func (h *Host) pump(onFrame func(*wire.Frame)) {
	rounds := 0
	for len(h.toClient) > 0 {
		rounds++
		if rounds >= 10_000 {
			break
		}
		for len(h.toClient) > 0 {
			d := h.toClient[0]
			h.toClient = h.toClient[1:]
			h.clientKcp.Input(d)
			for m := h.clientKcp.Receive(); m != nil; m = h.clientKcp.Receive() {
				h.reader.Append(m)
				for {
					f, err := h.reader.Next()
					if err != nil {
						panic(err)
					}
					if f == nil {
						break
					}
					h.FramesReceived++
					if onFrame != nil {
						onFrame(f)
					}
				}
			}
		}
		// ACKs go back, so the gateway flushes what still waits behind its windows.
		h.transport.ms++
		h.clientKcp.Update(uint32(h.transport.ms))
		h.clientKcp.Flush()
	}
}

func writeAccountLink(stateDir, account string, playerID int32) error {
	dir := filepath.Join(stateDir, "account", "link")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, account), []byte(fmt.Sprintf(".{ .player_id = %d }\n", playerID)), 0o644)
}

// CopyDirectory copies a directory tree; it returns how many files it copied.
func CopyDirectory(from, to string) (int, error) {
	if st, err := os.Stat(from); err != nil || !st.IsDir() {
		return 0, fmt.Errorf("no directory %s", from)
	}
	copied := 0
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

// Close closes the session and removes the temporary state.
func (h *Host) Close() {
	if h.disposed {
		return
	}
	h.disposed = true
	h.Gateway.CloseAll("robot finished")
	if h.ownsTempDir {
		os.RemoveAll(h.tempStateDir)
	}
}
