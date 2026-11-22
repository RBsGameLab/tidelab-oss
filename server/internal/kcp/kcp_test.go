// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package kcp

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// link is two KCP ends over a simulated link that can drop and reorder datagrams.
type link struct {
	t        *testing.T
	rnd      *rand.Rand
	loss     float64
	maxDelay int
	inFlight []flight
	a, b     *KCP
	now      uint32
}

type flight struct {
	due  uint32
	toB  bool
	data []byte
}

func newLink(t *testing.T, seed int64, loss float64, maxDelay int, setupA, setupB func(*KCP)) *link {
	l := &link{t: t, rnd: rand.New(rand.NewSource(seed)), loss: loss, maxDelay: maxDelay}
	l.a = New(7, func(d []byte) { l.enqueue(true, d) })
	l.b = New(7, func(d []byte) { l.enqueue(false, d) })
	if setupA != nil {
		setupA(l.a)
	}
	if setupB != nil {
		setupB(l.b)
	}
	return l
}

func (l *link) enqueue(toB bool, d []byte) {
	if l.rnd.Float64() < l.loss {
		return
	}
	// A random delay reorders datagrams.
	l.inFlight = append(l.inFlight, flight{l.now + uint32(1+l.rnd.Intn(l.maxDelay)), toB, append([]byte{}, d...)})
}

// step advances one millisecond: delivers what is due, then updates both ends.
func (l *link) step() {
	l.now++
	var due, rest []flight
	for _, f := range l.inFlight {
		if f.due <= l.now {
			due = append(due, f)
		} else {
			rest = append(rest, f)
		}
	}
	l.inFlight = rest
	for _, f := range due {
		to := l.a
		if f.toB {
			to = l.b
		}
		if r := to.Input(f.data); r != 0 {
			l.t.Fatalf("input %d", r)
		}
	}
	l.a.Update(l.now)
	l.b.Update(l.now)
}

func message(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func drain(k *KCP) [][]byte {
	var got [][]byte
	for m := k.Receive(); m != nil; m = k.Receive() {
		got = append(got, m)
	}
	return got
}

func equalAll(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestMessagesLargerThanOneMtuArriveWhole(t *testing.T) {
	l := newLink(t, 1, 0, 5, nil, nil)
	sent := [][]byte{message(1, 1), message(1376, 2), message(1377, 3), message(50_000, 4), message(0, 5)}
	for _, m := range sent {
		if !l.a.Send(m) {
			t.Fatal("refused")
		}
	}
	var got [][]byte
	for i := 0; i < 5000 && len(got) < len(sent); i++ {
		l.step()
		got = append(got, drain(l.b)...)
	}
	if !equalAll(sent, got) {
		t.Fatalf("%d messages", len(got))
	}
}

func TestLossAndReorderingStillDeliverInOrder(t *testing.T) {
	l := newLink(t, 42, 0.2, 40, nil, nil)
	var sent [][]byte
	for i := 0; i < 200; i++ {
		sent = append(sent, message(1+i*37%3000, int64(i)))
		l.a.Send(sent[i])
	}
	l.b.Send(message(9000, 99))
	var got, replies [][]byte
	for i := 0; i < 200_000 && (len(got) < len(sent) || len(replies) < 1); i++ {
		l.step()
		got = append(got, drain(l.b)...)
		replies = append(replies, drain(l.a)...)
	}
	if !equalAll(sent, got) || len(replies) != 1 || !bytes.Equal(replies[0], message(9000, 99)) || l.a.IsDead() {
		t.Fatal(len(got), len(replies))
	}
}

func TestAStreamModeSenderIsReadAsOneByteStream(t *testing.T) {
	// The client: stream mode, MTU 1000, windows 256, NoDelay(1, 10, 2, 1) (specs/network.md §3).
	l := newLink(t, 3, 0.1, 20, func(c *KCP) {
		c.Stream = true
		c.SetMtu(1000)
		c.SetWindows(256, 256)
		c.SetNoDelay(1, 10, 2, true)
	}, nil)
	var want []byte
	for i := 0; i < 300; i++ {
		w := message(i*13%2500, int64(i))
		want = append(want, w...)
		l.a.Send(w)
	}
	var got []byte
	for i := 0; i < 100_000 && len(got) < len(want); i++ {
		l.step()
		for _, m := range drain(l.b) {
			if len(m) > 1000-HeaderSize {
				t.Fatal("a stream segment is larger than the sender's MTU")
			}
			got = append(got, m...)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatal(len(got), len(want))
	}
}

func TestSmallStreamWritesShareASegment(t *testing.T) {
	var out [][]byte
	k := New(5, func(d []byte) { out = append(out, append([]byte{}, d...)) })
	k.Stream = true
	k.Update(0)
	k.Update(100)
	k.Send([]byte{1, 2, 3})
	k.Send([]byte{4, 5})
	k.Flush()
	if len(out) != 1 || len(out[0]) != HeaderSize+5 || out[0][5] != 0 || binary.LittleEndian.Uint32(out[0][20:]) != 5 {
		t.Fatal(out)
	}
}

func TestSegmentHeaderIsLittleEndianWithTheConversationFirst(t *testing.T) {
	var out [][]byte
	k := New(0x01020304, func(d []byte) { out = append(out, append([]byte{}, d...)) })
	k.Update(0)
	k.Update(100)
	k.Send(make([]byte, 3000))
	k.Flush()
	d := out[0]
	if !bytes.Equal(d[:4], []byte{4, 3, 2, 1}) || d[4] != 81 || d[5] != 2 || binary.LittleEndian.Uint32(d[12:]) != 0 || binary.LittleEndian.Uint32(d[20:]) != 1376 {
		t.Fatalf("% x", d[:24])
	}
}

func TestAMessageNeedingMoreThan256SegmentsIsRefused(t *testing.T) {
	k := New(1, func([]byte) {})
	if !k.Send(make([]byte, 1376*256)) || k.Send(make([]byte, 1376*256+1)) {
		t.Fatal("limit")
	}
}

func TestALinkThatNeverAnswersGoesDead(t *testing.T) {
	// Default settings double the timeout per retransmission, so 20 tries take days: count 5.
	l := newLink(t, 5, 1.0, 1, func(k *KCP) { k.DeadLink = 5 }, nil)
	l.a.Send([]byte{1, 2, 3})
	steps := 0
	for ; steps < 60_000 && !l.a.IsDead(); steps++ {
		l.step()
	}
	if !l.a.IsDead() || steps < 200+400+800+1600 || steps > 200+400+800+1600+3200+1000 {
		t.Fatal(steps)
	}
}

func TestDatagramsOfAnotherConversationOrTooShortAreRefused(t *testing.T) {
	k := New(1, func([]byte) {})
	if k.Input(make([]byte, 10)) != -1 {
		t.Fatal("short")
	}
	other := make([]byte, HeaderSize)
	other[0], other[4] = 2, 81
	if k.Input(other) != -1 {
		t.Fatal("other conv")
	}
	trunc := make([]byte, HeaderSize)
	trunc[0], trunc[4], trunc[20] = 1, 81, 5
	if k.Input(trunc) != -2 {
		t.Fatal("truncated")
	}
}

func TestCheckSaysWhenTheNextUpdateIsDue(t *testing.T) {
	k := New(1, func([]byte) {})
	if k.Check(0) != 0 {
		t.Fatal("before update")
	}
	k.Update(0)
	if n := k.Check(1); n < 2 || n > 100 {
		t.Fatal(n)
	}
}
