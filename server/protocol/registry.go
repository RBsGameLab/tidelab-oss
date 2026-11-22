// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package protocol is the client's protocol (Aki.Protocol 3.8): the messages protoc-gen-go
// wrote from proto/*.proto (beside this file), and the message ids (msgid.go). Regenerate both with
// tools/genproto; nothing here but this file is written by hand.
package protocol

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

var (
	typesOnce sync.Once
	types     map[MessageID]protoreflect.MessageType
	idsByName map[string]MessageID
)

// byShortName indexes every message of this package by its name without the proto package
// (the names are unique across aki.protocol and its sub-packages).
func byShortName() map[string]protoreflect.MessageType {
	all := map[string]protoreflect.MessageType{}
	protoregistry.GlobalTypes.RangeMessages(func(t protoreflect.MessageType) bool {
		d := t.Descriptor()
		if d.ParentFile() != nil && isOurs(d.ParentFile().Path()) && d.Parent() == d.ParentFile() {
			all[string(d.Name())] = t
		}
		return true
	})
	return all
}

func isOurs(path string) bool {
	return len(path) >= 12 && path[:12] == "aki_protocol"
}

func load() {
	short := byShortName()
	types = make(map[MessageID]protoreflect.MessageType, len(messageNames))
	idsByName = make(map[string]MessageID, len(messageNames))
	for id, name := range messageNames {
		idsByName[name] = id
		if t, ok := short[name]; ok {
			types[id] = t
		}
	}
}

// New returns an empty message for id, or nil if the client has no message with that id.
func New(id MessageID) proto.Message {
	typesOnce.Do(load)
	t, ok := types[id]
	if !ok {
		return nil
	}
	return t.New().Interface()
}

// Name is the message's short name for id ("" if unknown), as the logs print it.
func (id MessageID) Name() string {
	return messageNames[id]
}

// IDOf is the id of m's message, false if it has none (a message only used inside others).
func IDOf(m proto.Message) (MessageID, bool) {
	typesOnce.Do(load)
	id, ok := idsByName[string(m.ProtoReflect().Descriptor().Name())]
	return id, ok
}

// IDByName is the id of the message with this short name.
func IDByName(name string) (MessageID, bool) {
	typesOnce.Do(load)
	id, ok := idsByName[name]
	return id, ok
}

// AllIDs is every message id the client has.
func AllIDs() []MessageID {
	ids := make([]MessageID, 0, len(messageNames))
	for id := range messageNames {
		ids = append(ids, id)
	}
	return ids
}

// Marshal encodes m as the servers send it: map entries sorted by key, so the same message
// is always the same bytes. Fields go out in field-number order (C# writes them in declaration
// order; the client reads either).
func Marshal(m proto.Message) []byte {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		panic(err) // only invalid UTF-8 in a string field gets here: a programming error
	}
	return b
}
