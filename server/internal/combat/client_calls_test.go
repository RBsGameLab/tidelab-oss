// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"tidelab/internal/common"
	"tidelab/protocol"
)

// R-166: every combat request the client sends with CombatNet.Instance.Call waits for its
// response (R-164: an unanswered FragileChangeRequest froze tower monsters). The list is
// specs/combat-client-calls.tsv, refreshed from the client by `tools/tl_audit.py combat-calls`.
func TestEveryCombatRequestTheClientWaitsOnIsAnswered(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "combat-client-calls.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, line := range strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, strings.SplitN(line, "\t", 2)[0])
		}
	}
	must(t, len(names) >= 14, "too few combat requests listed:", len(names))

	ctx := createContext(t, t.TempDir(), ctxOpts{nowMs: 1000})
	sink := &recordingSink{}
	mid := ctx.Scene.AllocateNetID()
	m := NewCombatEntity(mid)
	m.EntityType = protocol.EEntityType_EEntityType_Monster
	ctx.Scene.Entities.Set(mid, m)

	fields := (&protocol.CombatRequestData{}).ProtoReflect().Descriptor().Fields()
	for _, name := range names {
		var field protoreflect.FieldDescriptor
		for i := 0; i < fields.Len(); i++ {
			if f := fields.Get(i); f.Message() != nil && string(f.Message().Name()) == name {
				field = f
			}
		}
		if field == nil {
			t.Errorf("%s is not a CombatRequestData case", name)
			continue
		}
		data := &protocol.CombatRequestData{RequestId: 1, CombatCommon: &protocol.CombatCommon{EntityId: mid}}
		r := data.ProtoReflect()
		r.Set(field, r.NewField(field))
		log, restore := common.CaptureLog()
		got := ctx.Combat.HandleCombatSendPack(&protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{
			{Message: &protocol.CombatSendData_Request{Request: data}}}}, 2000, sink).ReceivePackNotify.Data
		restore()
		answered := false
		for _, d := range got {
			if d.GetCombatResponseData() != nil {
				answered = true
			}
		}
		if !answered || strings.Contains(log.String(), "unhandled combat message") {
			t.Errorf("%s: the client waits for a response the server never sends", name)
		}
	}
}
