// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/protocol"
)

// FlowQueue plays one server flow (剧情) at a time (R-89, specs/flows.md §2).
// Ported from our C# Game/FlowQueue.cs.
//
// The client queues or interrupts flows itself: a node's Finish list with two PlayFlow actions
// and the next node's PlayFlow in one frame threw the prologue flow away, and a skipped cutscene
// of the last queued one left the screen black. So every FlowStartNotify goes through here: it is
// sent when no server flow is playing, else queued, and the next one goes out at the playing
// one's FlowEndRequest.
//
// A login drops the queue (Reset): the client plays no flow of the old session and never ends it,
// so waiting for its FlowEndRequest would hold every later flow back.
type FlowQueue struct {
	playing *int64
	waiting []*protocol.FlowStartNotify
}

func NewFlowQueue() *FlowQueue { return &FlowQueue{} }

// Playing is the FlowIncId of the server flow the client plays now, or nil when idle.
func (q *FlowQueue) Playing() *int64 { return q.playing }

// Waiting are the flows waiting behind the playing one, oldest first.
func (q *FlowQueue) Waiting() []*protocol.FlowStartNotify { return q.waiting }

// Start returns the notify to send now, or nil when it waits behind the playing flow (kept as a clone).
func (q *FlowQueue) Start(n *protocol.FlowStartNotify) *protocol.FlowStartNotify {
	if q.playing == nil {
		inc := n.FlowIncId
		q.playing = &inc
		return n
	}
	q.waiting = append(q.waiting, proto.Clone(n).(*protocol.FlowStartNotify))
	return nil
}

// Ended handles FlowEndRequest for inc: returns the next queued flow to send now if inc was the
// playing one. A queued flow cannot have ended (the client never saw it); a flow the client
// started itself is not ours.
func (q *FlowQueue) Ended(inc int64) *protocol.FlowStartNotify {
	if q.playing == nil || *q.playing != inc {
		return nil
	}
	q.playing = nil
	if len(q.waiting) == 0 {
		return nil
	}
	next := q.waiting[0]
	q.waiting = q.waiting[1:]
	nextInc := next.FlowIncId
	q.playing = &nextInc
	return next
}

func (q *FlowQueue) Reset() {
	q.waiting = q.waiting[:0]
	q.playing = nil
}

// Send sends n now, or queues it behind the server flows already playing or waiting.
func (q *FlowQueue) Send(n *protocol.FlowStartNotify, sink combat.PushSink) {
	var before int64
	if q.playing != nil {
		before = *q.playing
	}
	if now := q.Start(n); now != nil {
		sink.Push(now)
		return
	}
	common.Info("flow_queue", fmt.Sprintf("flow %d (%s %d state %d) waits for flow %d: %d queued",
		n.FlowIncId, n.FlowListName, n.FlowId, n.StateId, before, len(q.waiting)))
}

// SendEnded handles FlowEndRequest: sends the next queued flow if inc was the playing one.
func (q *FlowQueue) SendEnded(inc int64, sink combat.PushSink) {
	next := q.Ended(inc)
	if next == nil {
		return
	}
	common.Info("flow_queue", fmt.Sprintf("flow %d ended: starting queued flow %d (%s %d state %d), %d still queued",
		inc, next.FlowIncId, next.FlowListName, next.FlowId, next.StateId, len(q.waiting)))
	sink.Push(next)
}

// SceneChanged handles a scene switch: a flow cut off by it may never send its FlowEndRequest,
// so the playing flow counts as ended and the next queued one goes out.
func (q *FlowQueue) SceneChanged(sink combat.PushSink) {
	if q.playing == nil {
		return
	}
	inc := *q.playing
	common.Info("flow_queue", fmt.Sprintf("scene switch: flow %d counts as ended, %d queued", inc, len(q.waiting)))
	q.SendEnded(inc, sink)
}

// LoginReset drops the old session's flows on login.
func (q *FlowQueue) LoginReset() {
	if q.playing != nil || len(q.waiting) > 0 {
		var p string
		if q.playing != nil {
			p = fmt.Sprint(*q.playing)
		}
		common.Info("flow_queue", fmt.Sprintf("login: dropping flow %s and %d queued", p, len(q.waiting)))
	}
	q.Reset()
}
