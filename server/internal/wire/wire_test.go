// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package wire

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"tidelab/protocol"
)

func TestARequestHas13HeaderBytesAfterTheLength(t *testing.T) {
	got := (&Frame{Request, 0x0A0B0C0D, 0x0102, protocol.IDLoginRequest, []byte{0xAA, 0xBB}}).Encode()
	want := []byte{15, 0, 0, 1, 0x0D, 0x0C, 0x0B, 0x0A, 0x02, 0x01, 103, 0}
	want = binary.LittleEndian.AppendUint32(want, CRC32([]byte{0xAA, 0xBB}))
	want = append(want, 0xAA, 0xBB)
	if !bytes.Equal(got, want) {
		t.Fatalf("% x", got)
	}
}

func TestAPushHas11HeaderBytesAndNoRpcID(t *testing.T) {
	got := (&Frame{Push, 5, 0, protocol.IDLoginNotify, nil}).Encode()
	if !bytes.Equal(got, []byte{11, 0, 0, 4, 5, 0, 0, 0, 109, 0, 0, 0, 0, 0}) {
		t.Fatalf("% x", got)
	}
}

func TestCrcIsZlibs(t *testing.T) {
	if CRC32([]byte("123456789")) != 0xCBF43926 || CRC32(nil) != 0 {
		t.Fatal("crc")
	}
}

func next(t *testing.T, r *FrameReader) *Frame {
	t.Helper()
	f, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFramesRoundTripInBothKinds(t *testing.T) {
	var r FrameReader
	sent := []*Frame{
		{Request, 1, 7, protocol.IDProtoKeyRequest, []byte{1, 2, 3}},
		{Response, 2, 7, protocol.IDProtoKeyResponse, make([]byte, 300)},
		{Push, 3, 0, protocol.IDLoginNotify, []byte{}},
	}
	for _, f := range sent {
		r.Append(f.Encode())
	}
	for _, f := range sent {
		g := next(t, &r)
		if g.Type != f.Type || g.SeqNo != f.SeqNo || g.RpcID != f.RpcID || g.MessageID != f.MessageID || !bytes.Equal(g.Payload, f.Payload) {
			t.Fatalf("%+v", g)
		}
	}
	if next(t, &r) != nil || r.Pending() != 0 {
		t.Fatal("left over")
	}
}

func TestAFrameSplitAcrossChunksWaitsForItsLastByte(t *testing.T) {
	b := append((&Frame{Request, 9, 1, protocol.IDLoginRequest, make([]byte, 5000)}).Encode(),
		(&Frame{Push, 10, 0, protocol.IDLogoutNotify, []byte{9}}).Encode()...)
	var r FrameReader
	var got []*Frame
	for at := 0; at < len(b); at += 7 {
		r.Append(b[at:min(at+7, len(b))])
		for f := next(t, &r); f != nil; f = next(t, &r) {
			got = append(got, f)
		}
	}
	if len(got) != 2 || got[0].SeqNo != 9 || got[1].SeqNo != 10 || len(got[0].Payload) != 5000 {
		t.Fatal(got)
	}
}

func TestBadFramesAreErrors(t *testing.T) {
	b := (&Frame{Request, 1, 1, protocol.IDLoginRequest, []byte{1, 2, 3}}).Encode()
	b[len(b)-1] ^= 1
	var r FrameReader
	r.Append(b)
	var fe *FrameError
	if _, err := r.Next(); !errors.As(err, &fe) || !strings.Contains(err.Error(), "CRC") {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{{2, 0, 0, 1, 0}, {0, 0, 0, 9}, {0, 0, 0, 3}} {
		var r FrameReader
		r.Append(bad)
		if _, err := r.Next(); !errors.As(err, &fe) {
			t.Fatalf("% x: %v", bad, err)
		}
	}
}

var key = func() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}()

func TestLengthsRoundTripWithPkcs7(t *testing.T) {
	c, _ := NewCipher(key)
	for length, enc := range map[int]int{0: 0, 1: 16, 15: 16, 16: 32, 17: 32, 33: 48} {
		plain := make([]byte, length)
		for i := range plain {
			plain[i] = byte(i * 7)
		}
		for _, seq := range []uint32{0, 1, 15, 16, 17, 31, 32, 33, 0xFFFFFFFF} {
			s := c.Encrypt(plain, seq)
			back, err := c.Decrypt(s, seq)
			if len(s) != enc || err != nil || !bytes.Equal(back, plain) {
				t.Fatalf("%d/%d: %d %v", length, seq, len(s), err)
			}
		}
	}
}

func TestTheFirstBlockIsAes256EcbOfTheMaskedBytes(t *testing.T) {
	// FIPS-197 C.3: AES-256 with key 00..1f encrypts 00112233..ff to 8ea2b7ca...
	aesPlain, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	c, _ := NewCipher(key)
	plain := append([]byte{}, aesPlain...)
	plain[5] ^= 5 // seq 37: byte 37 % 16 = 5 is XORed with key[37 % 32] = 5
	s := c.Encrypt(plain, 37)
	if hex.EncodeToString(s[:16]) != "8ea2b7ca516745bfeafc49904b496089" || len(s) != 32 {
		t.Fatal(hex.EncodeToString(s))
	}
}

func ecbDecryptPkcs7(t *testing.T, b []byte) []byte {
	blk, _ := aes.NewCipher(key)
	out := make([]byte, len(b))
	for at := 0; at < len(b); at += 16 {
		blk.Decrypt(out[at:at+16], b[at:at+16])
	}
	return out[:len(out)-int(out[len(out)-1])]
}

func TestTheMaskedByteIsSeqModLengthWithKeySeqMod32(t *testing.T) {
	c, _ := NewCipher(key)
	for _, tc := range []struct {
		seq                     uint32
		length, index, keyIndex int
	}{{10, 5, 0, 10}, {9, 5, 4, 9}, {11, 5, 1, 11}, {40, 7, 5, 8}} {
		got := ecbDecryptPkcs7(t, c.Encrypt(make([]byte, tc.length), tc.seq))
		want := make([]byte, tc.length)
		want[tc.index] = key[tc.keyIndex]
		if !bytes.Equal(got, want) {
			t.Fatalf("%+v: % x", tc, got)
		}
	}
}

func TestBadPaddingOrPartialBlocksAreErrors(t *testing.T) {
	c, _ := NewCipher(key)
	var ce *CipherError
	if _, err := c.Decrypt(make([]byte, 15), 1); !errors.As(err, &ce) {
		t.Fatal(err)
	}
	blk, _ := aes.NewCipher(key)
	zeroPad := make([]byte, 16)
	blk.Encrypt(zeroPad, zeroPad)
	if _, err := c.Decrypt(zeroPad, 1); !errors.As(err, &ce) {
		t.Fatal(err)
	}
}

func TestTheWrappedSessionKeyOpensWithThePrivateKey(t *testing.T) {
	pair, _ := rsa.GenerateKey(rand.Reader, 2048)
	w, err := KeyWrapperFromDER(x509.MarshalPKCS1PublicKey(&pair.PublicKey))
	if err != nil || w.KeyBits() != 2048 {
		t.Fatal(err)
	}
	k, wrapped, _ := w.NewKey()
	if len(k) != 32 || len(wrapped) != 256 {
		t.Fatal(len(k), len(wrapped))
	}
	if back, err := rsa.DecryptPKCS1v15(nil, pair, wrapped); err != nil || !bytes.Equal(back, k) {
		t.Fatal(err)
	}
	other, _, _ := w.NewKey()
	if bytes.Equal(other, k) {
		t.Fatal("same key twice")
	}
	der := x509.MarshalPKCS1PublicKey(&pair.PublicKey)
	if _, err := KeyWrapperFromDER(append(der, 0)); err == nil {
		t.Fatal("trailing byte accepted")
	}
	spki, _ := x509.MarshalPKIXPublicKey(&pair.PublicKey)
	if _, err := KeyWrapperFromDER(spki); err == nil {
		t.Fatal("SubjectPublicKeyInfo accepted")
	}
}
