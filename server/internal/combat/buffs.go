// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package combat

import (
	"math"
	"slices"
	"strings"

	"tidelab/internal/data"
	"tidelab/protocol"
)

const (
	InstantBuffHandle  = -2
	WeaponApplyReason  = 12
	EchoApplyReason    = 7
	WeaponRemoveReason = 41
	EchoRemoveReason   = 35
	FetterRemoveReason = 36

	codeEntityMissing = protocol.ErrorCode_ErrorCode_ErrSceneEntityNotExist
)

type timerEvent struct {
	entityNetID int64
	handleID    int32
	version     int32
	isPeriodic  bool
}

// BuffManager applies and stacks buffs, runs their attribute modifiers and the equipment buff
// swaps, and keeps a min-heap of expiry and period timers (specs/combat.md §8).
type BuffManager struct {
	tables *data.DataTables
	scene  *CombatSceneState
	timers priorityQueue[timerEvent]
}

func NewBuffManager(t *data.DataTables, s *CombatSceneState) *BuffManager {
	return &BuffManager{tables: t, scene: s}
}

func (m *BuffManager) PendingTimerCount() int { return m.timers.Len() }

func (m *BuffManager) buff(id int64) (*data.Buff, bool) {
	b, ok := m.tables.BuffById[float64(id)]
	return b, ok
}

// roundMs is C#'s (long)Math.Round(seconds * 1000.0): halves to even.
func roundMs(seconds float32) int64 { return int64(math.RoundToEven(float64(seconds) * 1000.0)) }

func findBuff(e *CombatEntity, pred func(*ActiveBuffEntry) bool) *ActiveBuffEntry {
	if i := slices.IndexFunc(e.Buffs, pred); i >= 0 {
		return e.Buffs[i]
	}
	return nil
}

func removeBuff(e *CombatEntity, b *ActiveBuffEntry) {
	if i := slices.Index(e.Buffs, b); i >= 0 {
		e.Buffs = slices.Delete(e.Buffs, i, i+1)
	}
}

func removeNotifyOf(e *CombatEntity, b *ActiveBuffEntry) *protocol.CombatNotifyData {
	return protocol.Notify(e.NetID, &protocol.CombatNotifyData_RemoveGameplayEffectNotify{RemoveGameplayEffectNotify: &protocol.RemoveGameplayEffectNotify{
		Handle: b.HandleID, EntityId: e.NetID, InstigatorId: b.InstigatorID,
	}})
}

func stackNotify(e *CombatEntity, n *protocol.BuffStackCountNotify) *protocol.CombatNotifyData {
	return protocol.Notify(e.NetID, &protocol.CombatNotifyData_BuffStackCountNotify{BuffStackCountNotify: n})
}

// HandleOrderApplyBuff is OrderApplyBuffRequest in a combat pack (specs/combat.md §8.1).
func (m *BuffManager) HandleOrderApplyBuff(targetID int64, req *protocol.OrderApplyBuffRequest, nowMs int64, out *[]*protocol.CombatNotifyData) *protocol.OrderApplyBuffResponse {
	target := m.scene.GetEntity(targetID)
	if target == nil {
		return &protocol.OrderApplyBuffResponse{ErrorCode: codeEntityMissing}
	}
	cfg, ok := m.buff(req.Id)
	if !ok {
		return &protocol.OrderApplyBuffResponse{ErrorCode: codeParamError}
	}
	if req.Duration != nil {
		d := float64(*req.Duration)
		if math.IsInf(d, 0) || math.IsNaN(d) || d > float64(math.MaxInt64)/1000.0 {
			return &protocol.OrderApplyBuffResponse{ErrorCode: codeParamError}
		}
	}
	level := req.Level
	if level <= 0 {
		level = 1
	}
	stacks := req.StackCount
	if stacks <= 0 {
		stacks = max(cfg.DefaultStackCount, 1)
	}

	// 0: instant.
	if cfg.DurationPolicy == 0 {
		*out = append(*out, protocol.Notify(target.NetID, &protocol.CombatNotifyData_ApplyGameplayEffectNotify{ApplyGameplayEffectNotify: &protocol.ApplyGameplayEffectNotify{
			Handle: InstantBuffHandle, Id: req.Id, EntityId: target.NetID, InstigatorId: req.InstigatorId, Level: level,
			StackCount: stacks, IsActive: true, ApplyType: req.ApplyType, ServerId: req.ServerId, ConfBuffId: req.Id,
		}}))
		m.ExecuteAttributeEffect(target, cfg, level, stacks, req.InstigatorId, nowMs, out)
		return &protocol.OrderApplyBuffResponse{ErrorCode: codeSuccess}
	}

	// 1: infinite, 2: has a duration.
	duration := resolveDurationSeconds(cfg, level, req.Duration)
	var expireAt, periodMs *int64
	if duration > 0 {
		expireAt = i64p(nowMs + roundMs(duration))
	}
	if cfg.Period > 0 {
		periodMs = i64p(max(1, roundMs(cfg.Period)))
	}
	limit := max(cfg.StackLimitCount, 1)
	existing := findBuff(target, func(b *ActiveBuffEntry) bool { return b.BuffID == req.Id })
	if existing == nil {
		e := &ActiveBuffEntry{
			HandleID: m.scene.AllocateBuffHandle(), BuffID: req.Id, Level: level, StackCount: clamp32(stacks, 1, limit),
			InstigatorID: req.InstigatorId, EntityID: target.NetID, ApplyType: req.ApplyType, Duration: duration,
			LeftDuration: duration, IsActive: true, ServerID: req.ServerId, MessageID: -1, ConfBuffID: req.Id,
			ExpireAtMs: expireAt,
		}
		if periodMs != nil {
			e.NextPeriodAtMs = i64p(nowMs + *periodMs)
		}
		target.Buffs = append(target.Buffs, e)
		*out = append(*out, protocol.Notify(target.NetID, &protocol.CombatNotifyData_ApplyGameplayEffectNotify{ApplyGameplayEffectNotify: &protocol.ApplyGameplayEffectNotify{
			Handle: e.HandleID, Id: e.BuffID, EntityId: target.NetID, InstigatorId: e.InstigatorID, Level: e.Level,
			StackCount: e.StackCount, IsActive: e.IsActive, ApplyType: e.ApplyType, ServerId: e.ServerID,
			ConfBuffId: e.ConfBuffID, Duration: f32p(duration), LeftDuration: f32p(duration),
		}}))
		if periodMs != nil && cfg.ExecutePeriodicEffectOnApplication() {
			m.ExecuteAttributeEffect(target, cfg, e.Level, e.StackCount, e.InstigatorID, nowMs, out)
		} else if periodMs == nil && cfg.GameAttributeID >= 1 && cfg.GameAttributeID < AttrCount && len(cfg.ModifierMagnitude) > 0 {
			m.ExecuteAttributeEffect(target, cfg, e.Level, e.StackCount, e.InstigatorID, nowMs, out)
		}
		m.scheduleTimers(e)
		return &protocol.OrderApplyBuffResponse{ErrorCode: codeSuccess}
	}

	add := req.StackCount
	if add <= 0 {
		add = 1
		if cfg.StackAppendCount > 0 {
			add = cfg.StackAppendCount
		}
	}
	existing.StackCount = clamp32(existing.StackCount+add, 1, limit)
	existing.Level = level
	existing.InstigatorID = req.InstigatorId
	refreshDuration := cfg.StackDurationRefreshPolicy == 0 // 0: refresh
	refreshPeriod := cfg.StackPeriodResetPolicy == 0       // 0: refresh
	if refreshDuration && duration > 0 {
		existing.Duration = duration
		existing.ExpireAtMs = i64p(nowMs + roundMs(duration))
	}
	if refreshPeriod && periodMs != nil {
		existing.NextPeriodAtMs = i64p(nowMs + *periodMs)
	}
	wasActive := existing.IsActive
	existing.IsActive = true
	left := existing.Duration
	if existing.ExpireAtMs != nil {
		left = max(0, float32(*existing.ExpireAtMs-nowMs)/1000)
	}
	existing.LeftDuration = left
	*out = append(*out, stackNotify(target, &protocol.BuffStackCountNotify{
		HandleId: existing.HandleID, NewStackCount: existing.StackCount, Duration: f32p(existing.Duration), LeftDuration: f32p(left),
		NotRefreshDuration: !refreshDuration, NotRefreshPeriod: !refreshPeriod, InstigatorId: existing.InstigatorID,
	}))
	if !wasActive {
		*out = append(*out, protocol.Notify(target.NetID, &protocol.CombatNotifyData_ActivateBuffNotify{ActivateBuffNotify: &protocol.ActivateBuffNotify{Handle: existing.HandleID, On: true}}))
	}
	m.scheduleTimers(existing)
	return &protocol.OrderApplyBuffResponse{ErrorCode: codeSuccess}
}

// HandleApplyGameplayEffectPush (specs/combat.md §8.2).
func (m *BuffManager) HandleApplyGameplayEffectPush(targetID int64, p *protocol.ApplyGameplayEffectPush, nowMs int64, out *[]*protocol.CombatNotifyData) {
	target := m.scene.GetEntity(targetID)
	cfg, ok := m.buff(p.Id)
	if target == nil || !ok {
		return
	}
	level, stacks := p.Level, p.StackCount
	if level <= 0 {
		level = 1
	}
	if stacks <= 0 {
		stacks = 1
	}
	m.ExecuteAttributeEffect(target, cfg, level, stacks, p.InstigatorId, nowMs, out)
}

func (m *BuffManager) HandleOrderRemoveBuff(targetID int64, req *protocol.OrderRemoveBuffRequest, out *[]*protocol.CombatNotifyData) *protocol.OrderRemoveBuffResponse {
	target := m.scene.GetEntity(targetID)
	if target == nil {
		return &protocol.OrderRemoveBuffResponse{ErrorCode: codeEntityMissing}
	}
	var matches []*ActiveBuffEntry
	for _, b := range target.Buffs {
		if b.BuffID == req.Id && (req.InstigatorId == 0 || b.InstigatorID == req.InstigatorId) {
			matches = append(matches, b)
		}
	}
	for _, b := range matches {
		if req.StackCount > 0 && b.StackCount > req.StackCount {
			b.StackCount -= req.StackCount
			*out = append(*out, stackNotify(target, &protocol.BuffStackCountNotify{HandleId: b.HandleID, NewStackCount: b.StackCount, InstigatorId: b.InstigatorID}))
		} else {
			b.TimerVersion++
			removeBuff(target, b)
			*out = append(*out, removeNotifyOf(target, b))
		}
	}
	return &protocol.OrderRemoveBuffResponse{ErrorCode: codeSuccess}
}

func (m *BuffManager) HandleActivateBuff(targetID int64, req *protocol.ActivateBuffRequest, out *[]*protocol.CombatNotifyData) *protocol.ActivateBuffResponse {
	target := m.scene.GetEntity(targetID)
	if target == nil {
		return &protocol.ActivateBuffResponse{ErrorCode: codeEntityMissing}
	}
	if b := findBuff(target, func(b *ActiveBuffEntry) bool { return b.HandleID == req.Handle }); b != nil && b.IsActive != req.On {
		b.IsActive = req.On
		*out = append(*out, protocol.Notify(target.NetID, &protocol.CombatNotifyData_ActivateBuffNotify{ActivateBuffNotify: &protocol.ActivateBuffNotify{Handle: b.HandleID, On: b.IsActive}}))
	}
	return &protocol.ActivateBuffResponse{ErrorCode: codeSuccess}
}

func (m *BuffManager) HandleBuffStackCount(targetID int64, req *protocol.BuffStackCountRequest, out *[]*protocol.CombatNotifyData) *protocol.BuffStackCountResponse {
	target := m.scene.GetEntity(targetID)
	if target == nil {
		return &protocol.BuffStackCountResponse{ErrorCode: codeEntityMissing}
	}
	if b := findBuff(target, func(b *ActiveBuffEntry) bool { return b.HandleID == req.HandleId }); b != nil {
		if req.NewStackCount <= 0 {
			b.TimerVersion++
			removeBuff(target, b)
			*out = append(*out, removeNotifyOf(target, b))
		} else {
			b.StackCount = req.NewStackCount
			*out = append(*out, stackNotify(target, &protocol.BuffStackCountNotify{HandleId: b.HandleID, NewStackCount: b.StackCount, InstigatorId: b.InstigatorID}))
		}
	}
	return &protocol.BuffStackCountResponse{ErrorCode: codeSuccess}
}

// ExecuteAttributeEffect runs the buff's CalculationPolicy on the target (specs/combat.md §8.3).
func (m *BuffManager) ExecuteAttributeEffect(target *CombatEntity, cfg *data.Buff, level, stackCount int32, instigatorID, nowMs int64, out *[]*protocol.CombatNotifyData) {
	if cfg.GameAttributeID < 1 || cfg.GameAttributeID >= AttrCount || len(cfg.CalculationPolicy) == 0 {
		return
	}
	a := cfg.GameAttributeID
	cp := cfg.CalculationPolicy
	at := func(i int, fallback int32) int32 {
		if len(cp) > i {
			return cp[i]
		}
		return fallback
	}
	val, val2 := levelValue(cfg.ModifierMagnitude, level), levelValue(cfg.ModifierMagnitude2, level)
	stacks := max(stackCount, 1)
	toIncrement := !IsStateAttribute(a)
	var changed []Attr
	switch cp[0] {
	case 0: // AddValue
		changed = target.Attributes.ApplyDelta(a, val*stacks, toIncrement)
	case 1: // AddRate
		base := target.Attributes.Slots[a].Base
		delta := RoundHalfAwayFromZero(float64(base) * float64(val) * float64(stacks) / 10000.0)
		changed = target.Attributes.ApplyDelta(a, delta, toIncrement)
	case 2: // AddFromAttr
		ref, captureSource, calcType := at(1, 0), at(2, 0), at(3, 1)
		src := target
		if captureSource == 1 {
			if e := m.scene.GetEntity(instigatorID); e != nil {
				src = e
			}
		}
		adjusted := float64(readCapturedAttr(src.Attributes, ref, calcType))
		if len(cp) > 5 && cp[5] != 0 {
			adjusted -= float64(cp[5])
			if adjusted <= 0 {
				return
			}
		}
		if len(cp) > 6 && cp[6] != 0 {
			adjusted /= float64(cp[6])
			if len(cp) > 8 && cp[8] == 1 {
				adjusted = math.Floor(adjusted)
			}
		}
		raw := adjusted*float64(val)/10000.0 + float64(val2)
		if len(cp) > 7 && cp[7] != 0 && raw > float64(cp[7]) {
			raw = float64(cp[7])
		}
		changed = target.Attributes.ApplyDelta(a, RoundHalfAwayFromZero(raw*float64(stacks)), toIncrement)
	case 3: // OverrideValue
		changed = target.Attributes.ApplyOverride(a, val)
	default:
		// 4, 5, 6, 9, -1: no server-side action.
		return
	}
	if len(changed) > 0 {
		target.FsmDirty = true
		*out = append(*out, target.Attributes.BuildChangeNotifies(target.NetID, changed, nowMs)...)
	}
}

// EnsureEntityTimersRegistered schedules the timers of the entity's timed buffs.
func (m *BuffManager) EnsureEntityTimersRegistered(e *CombatEntity) {
	for _, b := range e.Buffs {
		if b.ExpireAtMs != nil || b.NextPeriodAtMs != nil {
			m.scheduleTimers(b)
		}
	}
}

// Tick runs the timers due by nowMs (specs/combat.md §8.4) and pushes what they made.
func (m *BuffManager) Tick(nowMs int64, sink PushSink) []*protocol.CombatNotifyData {
	n := m.TickNotifies(nowMs)
	if sink != nil && len(n) > 0 {
		PushCombatNotifies(sink, n)
	}
	return n
}

// TickNotifies runs the timers due by nowMs and returns what they made.
func (m *BuffManager) TickNotifies(nowMs int64) []*protocol.CombatNotifyData {
	var out []*protocol.CombatNotifyData
	for {
		ev, due, ok := m.timers.Peek()
		if !ok || due > nowMs {
			break
		}
		m.timers.Dequeue()
		e := m.scene.GetEntity(ev.entityNetID)
		if e == nil {
			continue
		}
		b := findBuff(e, func(b *ActiveBuffEntry) bool { return b.HandleID == ev.handleID })
		if b == nil || b.TimerVersion != ev.version {
			continue
		}
		cfg, _ := m.buff(b.BuffID)
		if ev.isPeriodic {
			if b.NextPeriodAtMs == nil || *b.NextPeriodAtMs > nowMs || cfg == nil || cfg.Period <= 0 {
				continue
			}
			nextAt := *b.NextPeriodAtMs
			period := max(1, roundMs(cfg.Period))
			if b.IsActive || cfg.PeriodicInhibitionPolicy == 2 { // 2: ResetAndExecute
				m.ExecuteAttributeEffect(e, cfg, b.Level, b.StackCount, b.InstigatorID, nowMs, &out)
			}
			next := nextAt + period
			if b.ExpireAtMs == nil || next <= *b.ExpireAtMs {
				b.NextPeriodAtMs = &next
				m.timers.Enqueue(timerEvent{e.NetID, b.HandleID, b.TimerVersion, true}, next)
			} else {
				b.NextPeriodAtMs = nil
			}
			continue
		}
		if b.ExpireAtMs == nil || *b.ExpireAtMs > nowMs {
			continue
		}
		var removeStacks int32
		if cfg != nil {
			removeStacks = cfg.StackExpirationRemoveNumber
		}
		if removeStacks > 0 && b.StackCount > removeStacks {
			b.StackCount -= removeStacks
			b.ExpireAtMs = i64p(nowMs + max(1, roundMs(b.Duration)))
			b.LeftDuration = b.Duration
			out = append(out, stackNotify(e, &protocol.BuffStackCountNotify{HandleId: b.HandleID, NewStackCount: b.StackCount,
				Duration: f32p(b.Duration), LeftDuration: f32p(b.Duration), InstigatorId: b.InstigatorID}))
			m.scheduleTimers(b)
			continue
		}
		b.TimerVersion++
		removeBuff(e, b)
		out = append(out, removeNotifyOf(e, b))
		if cfg != nil {
			for _, raw := range cfg.RoutineExpirationEffects {
				if id := int64(raw); id != 0 {
					m.HandleOrderApplyBuff(e.NetID, &protocol.OrderApplyBuffRequest{Id: id, InstigatorId: b.InstigatorID, Level: b.Level,
						StackCount: 1, ApplyType: protocol.ApplyGEType_ApplyGEType_Common}, nowMs, &out)
				}
			}
		}
	}
	return out
}

func directEntry(m *BuffManager, e *CombatEntity, id int64) *ActiveBuffEntry {
	b := NewBuffEntry()
	b.HandleID, b.BuffID, b.InstigatorID, b.EntityID, b.ConfBuffID = m.scene.AllocateBuffHandle(), id, e.NetID, e.NetID, id
	return b
}

// ApplyDirectPassiveBuffs adds infinite passive buffs to a live entity (chains, branches).
func (m *BuffManager) ApplyDirectPassiveBuffs(e *CombatEntity, ids []int64, sink PushSink) {
	var out []*protocol.CombatNotifyData
	for _, id := range ids {
		if id == 0 || findBuff(e, func(b *ActiveBuffEntry) bool { return b.BuffID == id }) != nil {
			continue
		}
		b := directEntry(m, e, id)
		e.Buffs = append(e.Buffs, b)
		out = append(out, protocol.Notify(e.NetID, &protocol.CombatNotifyData_ApplyGameplayEffectNotify{ApplyGameplayEffectNotify: &protocol.ApplyGameplayEffectNotify{
			Handle: b.HandleID, Id: id, EntityId: e.NetID, InstigatorId: e.NetID, Level: 1, StackCount: 1, IsActive: true,
			Duration: f32p(-1), LeftDuration: f32p(-1), ConfBuffId: id,
		}}))
	}
	if sink != nil && len(out) > 0 {
		PushCombatNotifies(sink, out)
	}
}

func (m *BuffManager) RemoveDirectPassiveBuffs(e *CombatEntity, ids []int64, sink PushSink) {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	if len(set) == 0 {
		return
	}
	var out []*protocol.CombatNotifyData
	for i := len(e.Buffs) - 1; i >= 0; i-- {
		b := e.Buffs[i]
		if !set[b.BuffID] {
			continue
		}
		b.TimerVersion++
		e.Buffs = slices.Delete(e.Buffs, i, i+1)
		out = append(out, removeNotifyOf(e, b))
	}
	if sink != nil && len(out) > 0 {
		PushCombatNotifies(sink, out)
	}
}

func distinctNonZero(ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		if id != 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// SwapEquipmentBuffsS2C sends the equipment buff removals (Reason 41 for weapons) and
// applications (Reason 12) in a CombatReceivePackNotify (specs/inventory.md §5.4–§5.5, combat.md §8.5).
func (m *BuffManager) SwapEquipmentBuffsS2C(e *CombatEntity, oldIDs, newIDs []int64, removeReason, applyReason int32, sink PushSink) {
	oldSet, newSet := distinctNonZero(oldIDs), distinctNonZero(newIDs)
	var out []*protocol.CombatNotifyData
	for _, id := range oldSet {
		if slices.Contains(newSet, id) {
			continue
		}
		var handle int32
		if b := findBuff(e, func(b *ActiveBuffEntry) bool { return b.BuffID == id }); b != nil {
			handle = b.HandleID
			b.TimerVersion++
			removeBuff(e, b)
		}
		out = append(out, protocol.Notify(e.NetID, &protocol.CombatNotifyData_RemoveBuffS2CRequestNotify{RemoveBuffS2CRequestNotify: &protocol.RemoveBuffS2CRequestNotify{
			Handle: handle, StackCount: 1, Reason: removeReason, InstigatorId: e.NetID,
		}}))
	}
	for _, id := range newSet {
		if slices.Contains(oldSet, id) {
			continue
		}
		if findBuff(e, func(b *ActiveBuffEntry) bool { return b.BuffID == id }) == nil {
			e.Buffs = append(e.Buffs, directEntry(m, e, id))
		}
		out = append(out, protocol.Notify(e.NetID, &protocol.CombatNotifyData_ApplyBuffS2CRequestNotify{ApplyBuffS2CRequestNotify: &protocol.ApplyBuffS2CRequestNotify{
			Id: id, InstigatorId: e.NetID, Level: 1, StackCount: 1, Reason: applyReason, ApplyType: protocol.ApplyGEType_ApplyGEType_Common,
		}}))
	}
	if len(out) > 0 {
		PushCombatNotifies(sink, out)
	}
}

// PushCombatNotifies sends notifies in one CombatReceivePackNotify.
func PushCombatNotifies(sink PushSink, notifies []*protocol.CombatNotifyData) {
	if len(notifies) == 0 {
		return
	}
	pack := &protocol.CombatReceivePackNotify{}
	for _, n := range notifies {
		pack.Data = append(pack.Data, &protocol.CombatReceiveData{Message: &protocol.CombatReceiveData_CombatNotifyData{CombatNotifyData: n}})
	}
	sink.Push(pack)
}

func (m *BuffManager) scheduleTimers(b *ActiveBuffEntry) {
	b.TimerVersion++
	if b.ExpireAtMs != nil {
		m.timers.Enqueue(timerEvent{b.EntityID, b.HandleID, b.TimerVersion, false}, *b.ExpireAtMs)
	}
	if b.NextPeriodAtMs != nil {
		m.timers.Enqueue(timerEvent{b.EntityID, b.HandleID, b.TimerVersion, true}, *b.NextPeriodAtMs)
	}
}

func resolveDurationSeconds(cfg *data.Buff, level int32, override *float32) float32 {
	if override != nil && *override > 0 {
		return *override
	}
	if cfg.DurationPolicy == 2 && len(cfg.DurationMagnitude) > 0 {
		return cfg.DurationMagnitude[clamp32(level-1, 0, int32(len(cfg.DurationMagnitude)-1))]
	}
	return -1
}

func levelValue(values []int32, level int32) int32 {
	if len(values) == 0 {
		return 0
	}
	return values[clamp32(level-1, 0, int32(len(values)-1))]
}

func readCapturedAttr(a *AttributeSet, id, calcType int32) int32 {
	if id < 1 || id >= AttrCount {
		return 0
	}
	s := a.Slots[id]
	switch calcType {
	case 0:
		return s.Base
	case 2:
		return s.Increment
	}
	return s.Current
}

// HandleOrderRemoveBuffByTags is OrderRemoveBuffByTagsRequest in a combat pack (specs/combat.md
// §8.1, R-165): the client asks for it when it has no buff authority over the entity. Every buff
// whose GrantedTags hold one of TagIds, or a child of one, is removed (InstigatorId 0: any).
// A missing entity answers ErrSceneEntityNotExist, which makes the client remove them itself.
func (m *BuffManager) HandleOrderRemoveBuffByTags(targetID int64, req *protocol.OrderRemoveBuffByTagsRequest, out *[]*protocol.CombatNotifyData) *protocol.OrderRemoveBuffByTagsResponse {
	target := m.scene.GetEntity(targetID)
	if target == nil {
		return &protocol.OrderRemoveBuffByTagsResponse{ErrorCode: codeEntityMissing}
	}
	var matches []*ActiveBuffEntry
	for _, b := range target.Buffs {
		if req.InstigatorId != 0 && b.InstigatorID != req.InstigatorId {
			continue
		}
		conf, ok := m.tables.BuffById[float64(b.BuffID)]
		if !ok {
			continue
		}
		if slices.ContainsFunc(conf.GrantedTags, func(tag string) bool { return m.tagUnderAny(tag, req.TagIds) }) {
			matches = append(matches, b)
		}
	}
	for _, b := range matches {
		b.TimerVersion++
		removeBuff(target, b)
		*out = append(*out, removeNotifyOf(target, b))
	}
	return &protocol.OrderRemoveBuffByTagsResponse{ErrorCode: codeSuccess}
}

// tagUnderAny: the tag named name is one of ids or below one of them (its dot prefixes, then the
// GameplayTagParent chain).
func (m *BuffManager) tagUnderAny(name string, ids []int32) bool {
	hit := func(id int32) bool { return slices.Contains(ids, id) }
	for n := name; n != ""; {
		if hit(HashTag(n, m.tables)) {
			return true
		}
		i := strings.LastIndexByte(n, '.')
		if i < 0 {
			break
		}
		n = n[:i]
	}
	cur := HashTag(name, m.tables)
	for depth := 0; m.tables != nil && depth < 32; depth++ {
		row, ok := m.tables.GameplayTagParentById[cur]
		if !ok || row.ParentId == 0 || row.ParentId == cur {
			break
		}
		cur = row.ParentId
		if hit(cur) {
			return true
		}
	}
	return false
}
