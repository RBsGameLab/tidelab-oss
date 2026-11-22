// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package wire is the game connection's framing and payload cipher (specs/network.md §4–§5),
// ported from Tidelab.GameServer.Net (Frame.cs, SessionCipher.cs).
package wire

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"tidelab/protocol"
)

// FrameType is the message type byte of a frame.
type FrameType byte

const (
	Request   FrameType = 1
	Response  FrameType = 2
	Exception FrameType = 3
	Push      FrameType = 4
)

func (t FrameType) String() string {
	switch t {
	case Request:
		return "Request"
	case Response:
		return "Response"
	case Exception:
		return "Exception"
	case Push:
		return "Push"
	}
	return fmt.Sprintf("FrameType(%d)", byte(t))
}

const (
	// LengthSize is the bytes before the header: the u24 length.
	LengthSize = 3
	// MaxBody is the largest frame body the u24 length can describe.
	MaxBody = 0xFF_FFFF
)

// Frame is one application message. Payload is as the frame carries it (encrypted once a
// session key exists); RpcID is 0 for pushes.
type Frame struct {
	Type      FrameType
	SeqNo     uint32
	RpcID     uint16
	MessageID protocol.MessageID
	Payload   []byte
}

func HasRpcID(t FrameType) bool { return t == Request || t == Response }

// HeaderSize is the header bytes after the length: 13 with an rpc id, 11 without.
func HeaderSize(t FrameType) int {
	if HasRpcID(t) {
		return 13
	}
	return 11
}

// CRC32 is zlib's CRC-32 (IEEE).
func CRC32(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

// Encode is the whole frame, length prefix and CRC included.
func (f *Frame) Encode() []byte {
	body := HeaderSize(f.Type) + len(f.Payload)
	if body > MaxBody {
		panic(fmt.Sprintf("a frame of %d bytes does not fit the u24 length", body))
	}
	b := make([]byte, LengthSize+body)
	b[0], b[1], b[2] = byte(body), byte(body>>8), byte(body>>16)
	b[3] = byte(f.Type)
	binary.LittleEndian.PutUint32(b[4:], f.SeqNo)
	at := 8
	if HasRpcID(f.Type) {
		binary.LittleEndian.PutUint16(b[at:], f.RpcID)
		at += 2
	}
	binary.LittleEndian.PutUint16(b[at:], uint16(f.MessageID))
	binary.LittleEndian.PutUint32(b[at+2:], CRC32(f.Payload))
	copy(b[at+6:], f.Payload)
	return b
}

// FrameError is a frame that cannot be read: the session that sent it is closed.
type FrameError struct{ Msg string }

func (e *FrameError) Error() string { return e.Msg }

// FrameReader cuts frames out of a byte stream. The client's KCP runs in stream mode, so one
// frame may span several KCP messages and one message may hold several frames (§3).
type FrameReader struct {
	buf        []byte
	start, end int
}

// Pending is the bytes held that do not make a whole frame yet.
func (r *FrameReader) Pending() int { return r.end - r.start }

func (r *FrameReader) Append(b []byte) {
	if r.buf == nil {
		r.buf = make([]byte, 4096)
	}
	if r.end+len(b) > len(r.buf) {
		pending := r.Pending()
		if pending+len(b) > len(r.buf) {
			grown := make([]byte, max(len(r.buf)*2, pending+len(b)))
			copy(grown, r.buf[r.start:r.end])
			r.buf = grown
		} else {
			copy(r.buf, r.buf[r.start:r.end])
		}
		r.start, r.end = 0, pending
	}
	copy(r.buf[r.end:], b)
	r.end += len(b)
}

// Next is the next whole frame, nil when more bytes are needed; an error for a bad type, a
// body shorter than its header, or a CRC mismatch.
func (r *FrameReader) Next() (*Frame, error) {
	p := r.buf[r.start:r.end]
	if len(p) < LengthSize+1 {
		return nil, nil
	}
	body := int(p[0]) | int(p[1])<<8 | int(p[2])<<16
	t := FrameType(p[3])
	switch t {
	case Request, Response, Push:
	case Exception:
		return nil, &FrameError{"exception frames are not read"}
	default:
		return nil, &FrameError{fmt.Sprintf("unknown message type %d", byte(t))}
	}
	header := HeaderSize(t)
	if body < header {
		return nil, &FrameError{fmt.Sprintf("a frame body of %d bytes is shorter than its %d-byte header", body, header)}
	}
	if len(p) < LengthSize+body {
		return nil, nil
	}
	fr := p[LengthSize : LengthSize+body]
	seq := binary.LittleEndian.Uint32(fr[1:])
	at := 5
	var rpc uint16
	if HasRpcID(t) {
		rpc = binary.LittleEndian.Uint16(fr[at:])
		at += 2
	}
	id := protocol.MessageID(binary.LittleEndian.Uint16(fr[at:]))
	crc := binary.LittleEndian.Uint32(fr[at+2:])
	payload := append([]byte{}, fr[at+6:]...)
	r.start += LengthSize + body
	if r.start == r.end {
		r.start, r.end = 0, 0
	}
	if CRC32(payload) != crc {
		return nil, &FrameError{fmt.Sprintf("CRC mismatch on message %d (seq %d)", uint16(id), seq)}
	}
	return &Frame{t, seq, rpc, id, payload}, nil
}

// SessionKeySize is the session key's length.
const SessionKeySize = 32

const block = 16

// Cipher is the payload cipher once a session key exists (§5.2): AES-256-ECB with PKCS#7
// padding, plus one byte XORed with a key byte chosen by the frame's sequence number.
type Cipher struct {
	key []byte
	aes cipher.Block
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != SessionKeySize {
		return nil, fmt.Errorf("a session key has %d bytes", SessionKeySize)
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Cipher{append([]byte{}, key...), b}, nil
}

func (c *Cipher) Key() []byte { return c.key }

func (c *Cipher) mask(b []byte, seq uint32) {
	if len(b) > 0 {
		b[seq%uint32(len(b))] ^= c.key[seq%SessionKeySize]
	}
}

func (c *Cipher) Encrypt(plain []byte, seq uint32) []byte {
	if len(plain) == 0 {
		return []byte{}
	}
	pad := block - len(plain)%block
	b := make([]byte, len(plain)+pad)
	copy(b, plain)
	c.mask(b[:len(plain)], seq)
	for i := len(plain); i < len(b); i++ {
		b[i] = byte(pad)
	}
	for at := 0; at < len(b); at += block {
		c.aes.Encrypt(b[at:at+block], b[at:at+block])
	}
	return b
}

// CipherError is a payload that does not decrypt: not whole blocks, or bad padding.
type CipherError struct{ Msg string }

func (e *CipherError) Error() string { return e.Msg }

func (c *Cipher) Decrypt(payload []byte, seq uint32) ([]byte, error) {
	if len(payload) == 0 {
		return []byte{}, nil
	}
	if len(payload)%block != 0 {
		return nil, &CipherError{fmt.Sprintf("an encrypted payload of %d bytes is not whole blocks", len(payload))}
	}
	b := make([]byte, len(payload))
	for at := 0; at < len(b); at += block {
		c.aes.Decrypt(b[at:at+block], payload[at:at+block])
	}
	pad := b[len(b)-1]
	if pad == 0 || pad > block {
		return nil, &CipherError{fmt.Sprintf("bad padding byte %d", pad)}
	}
	b = b[:len(b)-int(pad)]
	c.mask(b, seq)
	return b, nil
}

// KeyWrapper is the RSA public key session keys are sent under
// (state/gateway/client_public_key.der, a DER RSAPublicKey; §5.1). The client holds the private key.
type KeyWrapper struct {
	pub *rsa.PublicKey
}

// KeyWrapperFromDER reads exactly one DER RSAPublicKey (PKCS#1).
func KeyWrapperFromDER(der []byte) (*KeyWrapper, error) {
	pub, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, err
	}
	return &KeyWrapper{pub}, nil
}

func (w *KeyWrapper) KeyBits() int { return w.pub.N.BitLen() }

// NewKey is a new random session key, and the same key under RSA PKCS#1 v1.5 for the client.
func (w *KeyWrapper) NewKey() (key, wrapped []byte, err error) {
	key = make([]byte, SessionKeySize)
	if _, err = rand.Read(key); err != nil {
		return nil, nil, err
	}
	wrapped, err = rsa.EncryptPKCS1v15(rand.Reader, w.pub, key)
	if err != nil {
		return nil, nil, err
	}
	return key, wrapped, nil
}
