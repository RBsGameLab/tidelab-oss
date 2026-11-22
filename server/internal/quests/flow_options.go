// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"tidelab/internal/data"
	"tidelab/protocol"
)

// FlowOptionEntry is one interaction option added to an entity by AddFlowInteractOption (R-74).
type FlowOptionEntry struct {
	// Owner is the run (quest id or level play key) that added it.
	Owner   int64
	Source  *protocol.GameCtxPb
	Entity  int64
	Guid    string
	Text    string
	Flow    *FlowRef
	Actions []QuestAction
	// Token may be used by RemoveFlowInteractOption instead of Flow.
	Token string
}

type FlowOptionStats struct {
	Added    int
	Removed  int
	Released int
	Picked   int
}

// FlowOptions tracks dynamic interaction options added by quest steps (AddFlowInteractOption)
// and removed by RemoveFlowInteractOption or run end (R-74). Ported from FlowOptions.cs.
type FlowOptions struct {
	entries []FlowOptionEntry
	Stats   FlowOptionStats
}

func NewFlowOptions() *FlowOptions { return &FlowOptions{} }

func (f *FlowOptions) All() []FlowOptionEntry { return f.entries }
func (f *FlowOptions) Count() int             { return len(f.entries) }

func flowOfOption(v data.Value) *FlowRef {
	o, ok := v.(*data.Object)
	if !ok || o == nil {
		return nil
	}
	name, ok := o.Get("FlowListName").(data.String)
	if !ok || len(name) == 0 {
		return nil
	}
	return &FlowRef{
		FlowListName: string(name),
		FlowID:       JSONInt(o.Get("FlowId"), 0),
		StateID:      JSONInt(o.Get("StateId"), 0),
	}
}

// ParseFlowOption parses AddFlowInteractOption's params (an action's Raw) as a FlowOptionEntry.
func ParseFlowOption(owner int64, source *protocol.GameCtxPb, raw data.Value) (FlowOptionEntry, bool) {
	p, ok := raw.(*data.Object)
	if !ok || p == nil {
		return FlowOptionEntry{}, false
	}
	entity, ok := p.Get("EntityId").(data.Number)
	if !ok {
		return FlowOptionEntry{}, false
	}
	option, ok := p.Get("Option").(*data.Object)
	if !ok || option == nil {
		return FlowOptionEntry{}, false
	}
	guid, ok := option.Get("Guid").(data.String)
	if !ok {
		return FlowOptionEntry{}, false
	}
	kind, ok := option.Get("Type").(*data.Object)
	if !ok || kind == nil {
		return FlowOptionEntry{}, false
	}
	e := FlowOptionEntry{
		Owner:  owner,
		Source: source,
		Entity: int64(entity),
		Guid:   string(guid),
		Text:   JSONStr(option.Get("TidContent"), ""),
		Token:  JSONStr(p.Get("InteractToken"), ""),
	}
	switch JSONStr(kind.Get("Type"), "") {
	case "Flow":
		flow := flowOfOption(kind.Get("Flow"))
		if flow == nil {
			return FlowOptionEntry{}, false
		}
		e.Flow = flow
		return e, true
	case "Actions":
		list, ok := kind.Get("Actions").(*data.Array)
		if !ok || list == nil {
			return FlowOptionEntry{}, false
		}
		// Server actions read their params from Raw (R-33a), as in the generated quest data.
		actions := make([]QuestAction, len(list.Items))
		for i, item := range list.Items {
			a := ParseAction(item)
			if obj, ok := item.(*data.Object); ok && obj != nil {
				a.Raw = obj.Get("Params")
			} else {
				a.Raw = nil
			}
			actions[i] = a
		}
		e.Actions = actions
		return e, true
	default:
		return FlowOptionEntry{}, false
	}
}

// Add inserts an option; one with the same entity and guid is replaced.
func (f *FlowOptions) Add(e FlowOptionEntry) {
	for i := range f.entries {
		if f.entries[i].Entity == e.Entity && f.entries[i].Guid == e.Guid {
			f.entries[i] = e
			return
		}
	}
	f.entries = append(f.entries, e)
	f.Stats.Added++
}

func (f *FlowOptions) Find(entity int64, guid string) (FlowOptionEntry, bool) {
	for _, e := range f.entries {
		if e.Entity == entity && (guid == "" || e.Guid == guid) {
			return e, true
		}
	}
	return FlowOptionEntry{}, false
}

// Remove implements RemoveFlowInteractOption: removes entity's options matching flow or token.
func (f *FlowOptions) Remove(entity int64, flow *FlowRef, token string) []FlowOptionEntry {
	var gone []FlowOptionEntry
	for i := 0; i < len(f.entries); {
		e := f.entries[i]
		sameFlow := flow != nil && e.Flow != nil && e.Flow.FlowListName == flow.FlowListName && e.Flow.FlowID == flow.FlowID
		sameToken := token != "" && e.Token == token
		if e.Entity == entity && (sameFlow || sameToken) {
			gone = append(gone, e)
			f.swapRemove(i)
			f.Stats.Removed++
		} else {
			i++
		}
	}
	return gone
}

// RemoveOwner drops every option owned by owner when its run ends.
func (f *FlowOptions) RemoveOwner(owner int64) []FlowOptionEntry {
	var gone []FlowOptionEntry
	for i := 0; i < len(f.entries); {
		if f.entries[i].Owner == owner {
			gone = append(gone, f.entries[i])
			f.swapRemove(i)
			f.Stats.Released++
		} else {
			i++
		}
	}
	return gone
}

func (f *FlowOptions) swapRemove(i int) {
	last := len(f.entries) - 1
	f.entries[i] = f.entries[last]
	f.entries = f.entries[:last]
}

// RemoveFlowOptionArgs extracts entity, flow and token from RemoveFlowInteractOption's Raw params.
func RemoveFlowOptionArgs(raw data.Value) (entity int64, flow *FlowRef, token string, ok bool) {
	p, ok := raw.(*data.Object)
	if !ok || p == nil {
		return 0, nil, "", false
	}
	ent, ok := p.Get("EntityId").(data.Number)
	if !ok {
		return 0, nil, "", false
	}
	return int64(ent), flowOfOption(p.Get("Flow")), JSONStr(p.Get("InteractToken"), ""), true
}

// PickedFlowOptionCtx builds the DynamicInteract GameCtxPb a picked option's flow and actions run under.
func PickedFlowOptionCtx(e FlowOptionEntry, netID int64) *protocol.GameCtxPb {
	return &protocol.GameCtxPb{
		CtxType: protocol.GameCtxType_GameCtxType_DynamicInteract,
		CtxInfo: &protocol.GameCtxPb_DynamicInteract{
			DynamicInteract: &protocol.DynamicInteractCtxPb{
				EntityCtx:      &protocol.EntityCtxPb{ConfigId: int32(e.Entity), IncId: netID},
				OptionGuid:     e.Guid,
				FinalOptionCtx: e.Source,
			},
		},
	}
}
