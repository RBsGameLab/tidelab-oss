// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package gatewaytest is the gateway's test bench, shared by the gateway's own tests and by the
// tests of games that run behind it (R-144g): a gateway on a state directory, a simulated
// network on a clock the test moves by hand, and clients that behave as the 3.8 client's Net
// does (stream-mode KCP, MTU 1000, NoDelay(1, 10, 2, 1)).
//
// It is imported only by tests. It imports the gateway, so the gateway's own tests are an
// external test package (gateway_test), which keeps the import graph acyclic.
package gatewaytest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/gateway"
	"tidelab/internal/kcp"
	"tidelab/internal/wire"
	"tidelab/protocol"
)

// ManualTime is a clock the test moves by hand: Elapsed milliseconds after Start.
type ManualTime struct{ Elapsed int64 }

// Start is the wall-clock time of the test clock's zero.
var Start = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

func (m *ManualTime) Ms() int64     { return m.Elapsed }
func (m *ManualTime) UnixMs() int64 { return Start.UnixMilli() + m.Elapsed }

var (
	rsaOnce sync.Once
	rsaKey  *rsa.PrivateKey
)

// TestKey is the RSA key the test gateway hands out to clients.
func TestKey() *rsa.PrivateKey {
	rsaOnce.Do(func() { rsaKey, _ = rsa.GenerateKey(rand.Reader, 2048) })
	return rsaKey
}

// KeyWrapper wraps TestKey's public half as the gateway's key wrapper.
func KeyWrapper() (*wire.KeyWrapper, error) {
	return wire.KeyWrapperFromDER(x509.MarshalPKCS1PublicKey(&TestKey().PublicKey))
}

// Rig is a gateway, its state directory and the network between it and the clients.
type Rig struct {
	T       *testing.T
	State   string
	Time    *ManualTime
	Players *gateway.Players
	GW      *gateway.Gateway
	Clients []*Client

	toClients []datagram
}

type datagram struct {
	to netip.AddrPort
	b  []byte
}

// NewRig is a gateway on a new empty state directory. A nil game is gateway.NoGame.
func NewRig(t *testing.T, game gateway.Game) *Rig { return NewRigAt(t, t.TempDir(), game) }

// NewRigAt is a gateway on an existing state directory, for a game that works on the same one.
func NewRigAt(t *testing.T, state string, game gateway.Game) *Rig {
	keys, err := KeyWrapper()
	if err != nil {
		t.Fatal(err)
	}
	return newRig(t, state, keys, game)
}

// NewPlainRig is a gateway without a client key: sessions stay unencrypted (Type 0, R-173b).
func NewPlainRig(t *testing.T, game gateway.Game) *Rig { return newRig(t, t.TempDir(), nil, game) }

func newRig(t *testing.T, state string, keys *wire.KeyWrapper, game gateway.Game) *Rig {
	if game == nil {
		game = &gateway.NoGame{}
	}
	r := &Rig{T: t, State: state, Time: &ManualTime{}}
	r.Players = gateway.NewPlayers(r.State)
	r.GW = gateway.New(r.Players, keys, r.Time, func(to netip.AddrPort, b []byte) { r.toClients = append(r.toClients, datagram{to, b}) }, game)
	return r
}

// Client is a client at 127.0.0.1:port.
func (r *Rig) Client(port uint16) *Client {
	c := &Client{R: r, Addr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port), Decrypts: true}
	r.Clients = append(r.Clients, c)
	return c
}

func (r *Rig) File(rel string) string { return filepath.Join(r.State, filepath.FromSlash(rel)) }

func (r *Rig) Read(rel string) string {
	b, err := os.ReadFile(r.File(rel))
	if err != nil {
		r.T.Fatal(err)
	}
	return string(b)
}

func (r *Rig) Write(rel, text string) {
	os.MkdirAll(filepath.Dir(r.File(rel)), 0o755)
	if err := os.WriteFile(r.File(rel), []byte(text), 0o644); err != nil {
		r.T.Fatal(err)
	}
}

// Run runs the network for ms milliseconds of the test clock, one millisecond at a time.
func (r *Rig) Run(ms int) {
	for i := 0; i < ms; i++ {
		r.step()
	}
}

func (r *Rig) step() {
	r.deliver()
	r.GW.Tick()
	for _, c := range r.Clients {
		if c.kcp != nil {
			c.kcp.Update(uint32(r.Time.Elapsed))
		}
	}
	r.deliver()
	r.Time.Elapsed++
}

// WaitFor runs the network, one millisecond of the test clock at a time, until cond holds, and
// fails the test if it does not within maxMs. It waits on a condition and on the test clock
// only: no sleeping, so it takes the same steps on every machine.
func (r *Rig) WaitFor(maxMs int, what string, cond func() bool) {
	r.T.Helper()
	for i := 0; i < maxMs; i++ {
		if cond() {
			return
		}
		r.step()
	}
	if !cond() {
		r.T.Fatalf("still waiting for %s after %d ms of the test clock", what, maxMs)
	}
}

func (r *Rig) deliver() {
	for len(r.toClients) > 0 {
		d := r.toClients[0]
		r.toClients = r.toClients[1:]
		// A newer client on the same address stands for the same program reconnecting.
		for i := len(r.Clients) - 1; i >= 0; i-- {
			if r.Clients[i].Addr == d.to {
				r.Clients[i].fromServer(d.b)
				break
			}
		}
	}
}

// Session is the gateway's session with the conversation id.
func (r *Rig) Session(conv uint32) *gateway.Session {
	for _, s := range r.GW.Sessions() {
		if s.Conv == conv {
			return s
		}
	}
	r.T.Fatalf("no session %d", conv)
	return nil
}

// Client is one client of a Rig, with its KCP, its frame reader and its cipher.
type Client struct {
	R        *Rig
	Addr     netip.AddrPort
	Conv     uint32
	Received []*wire.Frame
	// Decrypts says whether Received holds decrypted payloads once the key is known.
	Decrypts bool
	Cipher   *wire.Cipher

	kcp    *kcp.KCP
	reader wire.FrameReader
	seqNo  uint32
	rpcID  uint16
}

// Connect sends the connect datagram and takes the conversation id out of the reply.
func (c *Client) Connect() {
	d := make([]byte, gateway.ConnectSize)
	d[0] = gateway.ConnectByte
	c.R.GW.Receive(c.Addr, d)
	c.R.Run(1)
}

func (c *Client) fromServer(b []byte) {
	if c.kcp == nil {
		if len(b) != 5 || b[0] != gateway.ConnectReplyByte {
			c.R.T.Fatalf("connect reply % x", b)
		}
		c.Conv = binary.LittleEndian.Uint32(b[1:])
		c.kcp = kcp.New(c.Conv, func(d []byte) { c.R.GW.Receive(c.Addr, append([]byte{}, d...)) })
		c.kcp.Stream = true
		c.kcp.SetMtu(1000)
		c.kcp.SetWindows(256, 256)
		c.kcp.SetNoDelay(1, 10, 2, true)
		return
	}
	if c.kcp.Input(b) != 0 {
		c.R.T.Fatal("bad datagram from the server")
	}
	for m := c.kcp.Receive(); m != nil; m = c.kcp.Receive() {
		c.reader.Append(m)
		for {
			f, err := c.reader.Next()
			if err != nil {
				c.R.T.Fatal(err)
			}
			if f == nil {
				break
			}
			if c.Decrypts && c.Cipher != nil && f.MessageID != protocol.IDProtoKeyResponse {
				if f.Payload, err = c.Cipher.Decrypt(f.Payload, f.SeqNo); err != nil {
					c.R.T.Fatal(err)
				}
			}
			c.Received = append(c.Received, f)
		}
	}
}

// Request sends a request frame and returns its rpc id.
func (c *Client) Request(m proto.Message, encrypt bool) uint16 {
	c.rpcID++
	id, _ := protocol.IDOf(m)
	c.SendFrame(wire.Request, c.rpcID, id, protocol.Marshal(m), encrypt)
	return c.rpcID
}

// Push sends a push frame.
func (c *Client) Push(m proto.Message) {
	id, _ := protocol.IDOf(m)
	c.SendFrame(wire.Push, 0, id, protocol.Marshal(m), true)
}

func (c *Client) SendFrame(t wire.FrameType, rpc uint16, id protocol.MessageID, plain []byte, encrypt bool) {
	c.seqNo++
	payload := plain
	if encrypt && c.Cipher != nil {
		payload = c.Cipher.Encrypt(plain, c.seqNo)
	}
	c.SendRaw((&wire.Frame{Type: t, SeqNo: c.seqNo, RpcID: rpc, MessageID: id, Payload: payload}).Encode())
}

func (c *Client) SendRaw(b []byte) {
	if !c.kcp.Send(b) {
		c.R.T.Fatal("send refused")
	}
	c.kcp.Flush()
}

// Has says whether a frame with the message id has arrived.
func (c *Client) Has(id protocol.MessageID) bool {
	for _, f := range c.Received {
		if f.MessageID == id {
			return true
		}
	}
	return false
}

// ResponseTo is the response frame to the rpc id, or nil if none has arrived.
func (c *Client) ResponseTo(rpc uint16) *wire.Frame {
	for _, f := range c.Received {
		if f.Type == wire.Response && f.RpcID == rpc {
			return f
		}
	}
	return nil
}

// Response runs the network for 200 ms and returns the one response to the rpc id.
func (c *Client) Response(rpc uint16) *wire.Frame {
	c.R.Run(200)
	var found *wire.Frame
	for _, f := range c.Received {
		if f.Type == wire.Response && f.RpcID == rpc {
			if found != nil {
				c.R.T.Fatal("two responses")
			}
			found = f
		}
	}
	if found == nil {
		c.R.T.Fatalf("no response to rpc %d", rpc)
	}
	return found
}

// Answer is the response to the rpc id, decoded as T, which must be the message that answers it.
func Answer[T any, PT interface {
	*T
	proto.Message
}](c *Client, rpc uint16) PT {
	f := c.Response(rpc)
	m := PT(new(T))
	if id, _ := protocol.IDOf(m); id != f.MessageID {
		c.R.T.Fatalf("got %s", f.MessageID.Name())
	}
	if err := proto.Unmarshal(f.Payload, m); err != nil {
		c.R.T.Fatal(err)
	}
	return m
}

// ExchangeKey does the ProtoKey exchange and sets up the cipher.
func (c *Client) ExchangeKey() *protocol.ProtoKeyResponse {
	resp := Answer[protocol.ProtoKeyResponse](c, c.Request(&protocol.ProtoKeyRequest{IsLogin: true}, false))
	if resp.Type == 0 {
		c.Cipher = nil
		return resp
	}
	key, err := rsa.DecryptPKCS1v15(nil, TestKey(), resp.Key)
	if err != nil {
		c.R.T.Fatal(err)
	}
	c.Cipher, _ = wire.NewCipher(key)
	return resp
}

// Login exchanges the key and logs in as the account.
func (c *Client) Login(account string) *protocol.LoginResponse {
	c.ExchangeKey()
	return Answer[protocol.LoginResponse](c, c.Request(&protocol.LoginRequest{Account: account, Token: "1"}, true))
}

// LoginAndEnter logs in and enters the game, failing the test if the login is refused.
func (c *Client) LoginAndEnter(account string) {
	if code := c.Login(account).Code; code != protocol.ErrorCode_ErrorCode_Success {
		c.R.T.Fatal(code)
	}
	Answer[protocol.EnterGameResponse](c, c.Request(&protocol.EnterGameRequest{}, true))
}

// String names the client for failure messages.
func (c *Client) String() string { return fmt.Sprintf("client %s conv %d", c.Addr, c.Conv) }
