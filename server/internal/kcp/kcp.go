// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package kcp is one KCP conversation: an ARQ protocol over datagrams (the protocol of
// skywind3000's ikcp, MIT licence; specs/network.md §3). Ported from our C#
// Tidelab.GameServer.Net.Kcp. Not safe for concurrent use; times are milliseconds on a clock
// the caller owns, and only their differences matter (they may wrap).
package kcp

import (
	"container/list"
	"encoding/binary"
	"math"
)

const (
	HeaderSize    = 24
	cmdPush       = 81
	cmdAck        = 82
	cmdWindowAsk  = 83
	cmdWindowTell = 84
	askSend       = 1
	askTell       = 2
	rtoNoDelay    = 30
	rtoMin        = 100
	rtoDefault    = 200
	rtoMax        = 60000
	threshInit    = 2
	threshMin     = 2
	probeInit     = 7000
	probeLimit    = 120000
	fastAckLimit  = 5
	// MaxFragments is the most fragments one message may have: the fragment index is one byte.
	MaxFragments = 256
)

type segment struct {
	conv     uint32
	cmd      byte
	frg      byte
	wnd      uint16
	ts       uint32
	sn       uint32
	una      uint32
	resendTs uint32
	rto      int
	fastAck  int
	xmit     int
	data     []byte
}

type ack struct{ sn, ts uint32 }

// KCP is one conversation.
type KCP struct {
	conv   uint32
	output func([]byte)

	sendQueue, sendBuffer, recvQueue, recvBuffer *list.List
	acks                                         []ack
	buffer                                       []byte

	sndUna, sndNxt, rcvNxt uint32
	ssthresh               int
	rxRttVal, rxSrtt       int
	rxRto, rxMinRto        int
	sndWnd, rcvWnd, rmtWnd int
	cwnd, incr             int
	probe                  int
	current, tsFlush       uint32
	tsProbe                uint32
	interval, probeWait    int
	updated                bool
	noDelay, fastResend    int
	fastLimit              int
	noCwnd                 bool

	mtu int
	// Stream mode: messages are not kept apart, small sends merge into one segment.
	Stream bool
	// DeadLink: a segment is given up after this many transmissions; the link is then dead.
	DeadLink int
	dead     bool
}

// New makes a conversation; output sends one datagram (the slice is only valid during the call).
func New(conv uint32, output func([]byte)) *KCP {
	k := &KCP{
		conv: conv, output: output,
		sendQueue: list.New(), sendBuffer: list.New(), recvQueue: list.New(), recvBuffer: list.New(),
		ssthresh: threshInit, rxRto: rtoDefault, rxMinRto: rtoMin,
		sndWnd: 32, rcvWnd: 256, rmtWnd: 256, interval: 100, fastLimit: fastAckLimit,
		DeadLink: 20,
	}
	k.SetMtu(1400)
	return k
}

func (k *KCP) Conv() uint32 { return k.conv }
func (k *KCP) Mtu() int     { return k.mtu }

// Mss is the largest payload of one segment.
func (k *KCP) Mss() int { return k.mtu - HeaderSize }

// IsDead: a segment reached DeadLink transmissions.
func (k *KCP) IsDead() bool { return k.dead }

// WaitingToSend is the segments waiting to be sent or acknowledged.
func (k *KCP) WaitingToSend() int { return k.sendQueue.Len() + k.sendBuffer.Len() }

func (k *KCP) SendWindow() int    { return k.sndWnd }
func (k *KCP) ReceiveWindow() int { return k.rcvWnd }

func (k *KCP) SetMtu(mtu int) {
	if mtu < 50 {
		panic("kcp: an MTU below 50 leaves no room for data")
	}
	k.mtu = mtu
	k.buffer = make([]byte, (mtu+HeaderSize)*3)
}

func (k *KCP) SetWindows(send, receive int) {
	if send > 0 {
		k.sndWnd = send
	}
	if receive > 0 {
		k.rcvWnd = max(receive, MaxFragments/2)
	}
}

// SetNoDelay: noDelay 0 normal, 1 no delay (minimum RTO 30 ms, gentler backoff), 2 gentler
// still; interval is the flush interval in ms (10–5000); resend after this many later
// segments were acknowledged (0 off); noCwnd ignores the congestion window.
func (k *KCP) SetNoDelay(noDelay, interval, resend int, noCwnd bool) {
	k.noDelay = noDelay
	if noDelay != 0 {
		k.rxMinRto = rtoNoDelay
	} else {
		k.rxMinRto = rtoMin
	}
	k.interval = min(max(interval, 10), 5000)
	k.fastResend = max(0, resend)
	k.noCwnd = noCwnd
}

// Send queues a message; false when it needs more than MaxFragments segments.
func (k *KCP) Send(data []byte) bool {
	mss := k.Mss()
	if k.Stream {
		if e := k.sendQueue.Back(); e != nil {
			last := e.Value.(*segment)
			if len(last.data) < mss {
				extend := min(len(data), mss-len(last.data))
				merged := make([]byte, len(last.data)+extend)
				copy(merged, last.data)
				copy(merged[len(last.data):], data[:extend])
				last.data = merged
				last.frg = 0
				data = data[extend:]
				if len(data) == 0 {
					return true
				}
			}
		}
	}
	count := 1
	if len(data) > mss {
		count = (len(data) + mss - 1) / mss
	}
	if count > MaxFragments {
		return false
	}
	for i := 0; i < count; i++ {
		size := min(len(data), mss)
		s := &segment{data: append([]byte{}, data[:size]...)}
		if !k.Stream {
			s.frg = byte(count - i - 1)
		}
		k.sendQueue.PushBack(s)
		data = data[size:]
	}
	return true
}

// PeekSize is the size of the next whole message, -1 when none is complete.
func (k *KCP) PeekSize() int {
	e := k.recvQueue.Front()
	if e == nil {
		return -1
	}
	first := e.Value.(*segment)
	if first.frg == 0 {
		return len(first.data)
	}
	if k.recvQueue.Len() < int(first.frg)+1 {
		return -1
	}
	n := 0
	for ; e != nil; e = e.Next() {
		s := e.Value.(*segment)
		n += len(s.data)
		if s.frg == 0 {
			break
		}
	}
	return n
}

// Receive is the next whole message, nil when none is complete.
func (k *KCP) Receive() []byte {
	size := k.PeekSize()
	if size < 0 {
		return nil
	}
	recover := k.recvQueue.Len() >= k.rcvWnd
	msg := make([]byte, 0, size)
	for e := k.recvQueue.Front(); e != nil; e = k.recvQueue.Front() {
		s := e.Value.(*segment)
		k.recvQueue.Remove(e)
		msg = append(msg, s.data...)
		if s.frg == 0 {
			break
		}
	}
	k.moveReceived()
	if recover && k.recvQueue.Len() < k.rcvWnd {
		k.probe |= askTell
	}
	return msg
}

func diff(later, earlier uint32) int { return int(int32(later - earlier)) }

// Input feeds one datagram: 0, or negative when it is malformed or belongs to another
// conversation (segments read before the fault still count).
func (k *KCP) Input(data []byte) int {
	if len(data) < HeaderSize {
		return -1
	}
	prevUna := k.sndUna
	haveAck := false
	var maxAck uint32
	for len(data) >= HeaderSize {
		conv := binary.LittleEndian.Uint32(data)
		if conv != k.conv {
			return -1
		}
		cmd, frg := data[4], data[5]
		wnd := binary.LittleEndian.Uint16(data[6:])
		ts := binary.LittleEndian.Uint32(data[8:])
		sn := binary.LittleEndian.Uint32(data[12:])
		una := binary.LittleEndian.Uint32(data[16:])
		length := binary.LittleEndian.Uint32(data[20:])
		data = data[HeaderSize:]
		if length > uint32(len(data)) {
			return -2
		}
		if cmd != cmdPush && cmd != cmdAck && cmd != cmdWindowAsk && cmd != cmdWindowTell {
			return -3
		}
		k.rmtWnd = int(wnd)
		k.parseUna(una)
		k.shrinkBuffer()
		switch cmd {
		case cmdAck:
			if diff(k.current, ts) >= 0 {
				k.updateRtt(diff(k.current, ts))
			}
			k.parseAck(sn)
			k.shrinkBuffer()
			if !haveAck || diff(sn, maxAck) > 0 {
				maxAck = sn
			}
			haveAck = true
		case cmdPush:
			if diff(sn, k.rcvNxt+uint32(k.rcvWnd)) < 0 {
				k.acks = append(k.acks, ack{sn, ts})
				if diff(sn, k.rcvNxt) >= 0 {
					k.parseData(&segment{conv: conv, cmd: cmd, frg: frg, wnd: wnd, ts: ts, sn: sn, una: una,
						data: append([]byte{}, data[:length]...)})
				}
			}
		case cmdWindowAsk:
			k.probe |= askTell
		}
		data = data[length:]
	}
	if haveAck {
		k.parseFastAck(maxAck)
	}
	if diff(k.sndUna, prevUna) > 0 && k.cwnd < k.rmtWnd {
		mss := k.Mss()
		if k.cwnd < k.ssthresh {
			k.cwnd++
			k.incr += mss
		} else {
			if k.incr < mss {
				k.incr = mss
			}
			k.incr += mss*mss/k.incr + mss/16
			if (k.cwnd+1)*mss <= k.incr {
				k.cwnd = (k.incr + mss - 1) / mss
			}
		}
		if k.cwnd > k.rmtWnd {
			k.cwnd = k.rmtWnd
			k.incr = k.rmtWnd * mss
		}
	}
	return 0
}

// Update advances the clock; flushes when the interval has passed.
func (k *KCP) Update(current uint32) {
	k.current = current
	if !k.updated {
		k.updated = true
		k.tsFlush = current
	}
	slap := diff(current, k.tsFlush)
	if slap >= 10000 || slap < -10000 {
		k.tsFlush = current
		slap = 0
	}
	if slap >= 0 {
		k.tsFlush += uint32(k.interval)
		if diff(current, k.tsFlush) >= 0 {
			k.tsFlush = current + uint32(k.interval)
		}
		k.Flush()
	}
}

// Check is when Update should next be called.
func (k *KCP) Check(current uint32) uint32 {
	if !k.updated {
		return current
	}
	tsFlush := k.tsFlush
	if d := diff(current, tsFlush); d >= 10000 || d < -10000 {
		tsFlush = current
	}
	if diff(current, tsFlush) >= 0 {
		return current
	}
	untilFlush := diff(tsFlush, current)
	untilResend := math.MaxInt32
	for e := k.sendBuffer.Front(); e != nil; e = e.Next() {
		d := diff(e.Value.(*segment).resendTs, current)
		if d <= 0 {
			return current
		}
		untilResend = min(untilResend, d)
	}
	return current + uint32(min(untilResend, untilFlush, k.interval))
}

// Flush sends acknowledgements, window probes and whatever the windows allow.
func (k *KCP) Flush() {
	if !k.updated {
		return
	}
	current := k.current
	at := 0
	window := uint16(k.windowUnused())
	for _, a := range k.acks {
		at = k.reserve(at, HeaderSize)
		at = k.encode(at, cmdAck, 0, window, a.ts, a.sn, k.rcvNxt, 0)
	}
	k.acks = k.acks[:0]
	if k.rmtWnd == 0 {
		if k.probeWait == 0 {
			k.probeWait = probeInit
			k.tsProbe = current + uint32(k.probeWait)
		} else if diff(current, k.tsProbe) >= 0 {
			k.probeWait = max(k.probeWait, probeInit)
			k.probeWait = min(k.probeWait+k.probeWait/2, probeLimit)
			k.tsProbe = current + uint32(k.probeWait)
			k.probe |= askSend
		}
	} else {
		k.tsProbe, k.probeWait = 0, 0
	}
	if k.probe&askSend != 0 {
		at = k.reserve(at, HeaderSize)
		at = k.encode(at, cmdWindowAsk, 0, window, 0, 0, k.rcvNxt, 0)
	}
	if k.probe&askTell != 0 {
		at = k.reserve(at, HeaderSize)
		at = k.encode(at, cmdWindowTell, 0, window, 0, 0, k.rcvNxt, 0)
	}
	k.probe = 0
	cwnd := min(k.sndWnd, k.rmtWnd)
	if !k.noCwnd {
		cwnd = min(k.cwnd, cwnd)
	}
	for diff(k.sndNxt, k.sndUna+uint32(cwnd)) < 0 {
		e := k.sendQueue.Front()
		if e == nil {
			break
		}
		k.sendQueue.Remove(e)
		s := e.Value.(*segment)
		s.conv, s.cmd, s.wnd, s.ts = k.conv, cmdPush, window, current
		s.sn = k.sndNxt
		k.sndNxt++
		s.una, s.resendTs, s.rto, s.fastAck, s.xmit = k.rcvNxt, current, k.rxRto, 0, 0
		k.sendBuffer.PushBack(s)
	}
	resent := math.MaxInt32
	if k.fastResend > 0 {
		resent = k.fastResend
	}
	rtoMinExtra := 0
	if k.noDelay == 0 {
		rtoMinExtra = k.rxRto >> 3
	}
	lost, change := false, false
	for e := k.sendBuffer.Front(); e != nil; e = e.Next() {
		s := e.Value.(*segment)
		send := false
		switch {
		case s.xmit == 0:
			send = true
			s.xmit++
			s.rto = k.rxRto
			s.resendTs = current + uint32(s.rto) + uint32(rtoMinExtra)
		case diff(current, s.resendTs) >= 0:
			send = true
			s.xmit++
			if k.noDelay == 0 {
				s.rto += max(s.rto, k.rxRto)
			} else if k.noDelay < 2 {
				s.rto += s.rto / 2
			} else {
				s.rto += k.rxRto / 2
			}
			s.resendTs = current + uint32(s.rto)
			lost = true
		case s.fastAck >= resent && (s.xmit <= k.fastLimit || k.fastLimit <= 0):
			send = true
			s.xmit++
			s.fastAck = 0
			s.resendTs = current + uint32(s.rto)
			change = true
		}
		if !send {
			continue
		}
		s.ts, s.wnd, s.una = current, window, k.rcvNxt
		at = k.reserve(at, HeaderSize+len(s.data))
		at = k.encode(at, cmdPush, s.frg, window, s.ts, s.sn, s.una, len(s.data))
		copy(k.buffer[at:], s.data)
		at += len(s.data)
		if s.xmit >= k.DeadLink {
			k.dead = true
		}
	}
	if at > 0 {
		k.output(k.buffer[:at])
	}
	mss := k.Mss()
	if change {
		inflight := int(k.sndNxt - k.sndUna)
		k.ssthresh = max(inflight/2, threshMin)
		k.cwnd = k.ssthresh + resent
		k.incr = k.cwnd * mss
	}
	if lost {
		k.ssthresh = max(cwnd/2, threshMin)
		k.cwnd = 1
		k.incr = mss
	}
	if k.cwnd < 1 {
		k.cwnd = 1
		k.incr = mss
	}
}

func (k *KCP) reserve(at, need int) int {
	if at+need <= k.mtu {
		return at
	}
	if at > 0 {
		k.output(k.buffer[:at])
	}
	return 0
}

func (k *KCP) encode(at int, cmd, frg byte, wnd uint16, ts, sn, una uint32, length int) int {
	b := k.buffer[at : at+HeaderSize]
	binary.LittleEndian.PutUint32(b, k.conv)
	b[4], b[5] = cmd, frg
	binary.LittleEndian.PutUint16(b[6:], wnd)
	binary.LittleEndian.PutUint32(b[8:], ts)
	binary.LittleEndian.PutUint32(b[12:], sn)
	binary.LittleEndian.PutUint32(b[16:], una)
	binary.LittleEndian.PutUint32(b[20:], uint32(length))
	return at + HeaderSize
}

func (k *KCP) windowUnused() int { return max(0, min(k.rcvWnd-k.recvQueue.Len(), math.MaxUint16)) }

func (k *KCP) updateRtt(rtt int) {
	if k.rxSrtt == 0 {
		k.rxSrtt = rtt
		k.rxRttVal = rtt / 2
	} else {
		delta := rtt - k.rxSrtt
		if delta < 0 {
			delta = -delta
		}
		k.rxRttVal = (3*k.rxRttVal + delta) / 4
		k.rxSrtt = max(1, (7*k.rxSrtt+rtt)/8)
	}
	rto := k.rxSrtt + max(k.interval, 4*k.rxRttVal)
	k.rxRto = min(max(rto, k.rxMinRto), rtoMax)
}

func (k *KCP) shrinkBuffer() {
	if e := k.sendBuffer.Front(); e != nil {
		k.sndUna = e.Value.(*segment).sn
	} else {
		k.sndUna = k.sndNxt
	}
}

func (k *KCP) parseAck(sn uint32) {
	if diff(sn, k.sndUna) < 0 || diff(sn, k.sndNxt) >= 0 {
		return
	}
	for e := k.sendBuffer.Front(); e != nil; e = e.Next() {
		s := e.Value.(*segment)
		if s.sn == sn {
			k.sendBuffer.Remove(e)
			break
		}
		if diff(sn, s.sn) < 0 {
			break
		}
	}
}

func (k *KCP) parseUna(una uint32) {
	for e := k.sendBuffer.Front(); e != nil && diff(una, e.Value.(*segment).sn) > 0; e = k.sendBuffer.Front() {
		k.sendBuffer.Remove(e)
	}
}

func (k *KCP) parseFastAck(sn uint32) {
	if diff(sn, k.sndUna) < 0 || diff(sn, k.sndNxt) >= 0 {
		return
	}
	for e := k.sendBuffer.Front(); e != nil; e = e.Next() {
		s := e.Value.(*segment)
		if diff(sn, s.sn) < 0 {
			break
		}
		if sn != s.sn {
			s.fastAck++
		}
	}
}

func (k *KCP) parseData(s *segment) {
	sn := s.sn
	if diff(sn, k.rcvNxt+uint32(k.rcvWnd)) >= 0 || diff(sn, k.rcvNxt) < 0 {
		return
	}
	e := k.recvBuffer.Back()
	for ; e != nil; e = e.Prev() {
		o := e.Value.(*segment)
		if o.sn == sn {
			return
		}
		if diff(sn, o.sn) > 0 {
			break
		}
	}
	if e == nil {
		k.recvBuffer.PushFront(s)
	} else {
		k.recvBuffer.InsertAfter(s, e)
	}
	k.moveReceived()
}

func (k *KCP) moveReceived() {
	for e := k.recvBuffer.Front(); e != nil && e.Value.(*segment).sn == k.rcvNxt && k.recvQueue.Len() < k.rcvWnd; e = k.recvBuffer.Front() {
		k.recvBuffer.Remove(e)
		k.recvQueue.PushBack(e.Value)
		k.rcvNxt++
	}
}
