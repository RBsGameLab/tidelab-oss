// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package quests

import (
	"slices"

	"tidelab/internal/data"
)

// SpawnWave is one wave of a spawner entity's SpawnMonsterComponent (SpawnMonsterConfigs).
type SpawnWave struct {
	ID              int32
	DelayTime       float64
	PreConditionIDs []int32
	TargetsToAwake  []int64
	// CompleteType (ESpawnMonsterCompleteCondition): 0 AllKill, 1 Duration, 2 QuantityRefill.
	CompleteType int32
	Quantity     *int32
	Duration     *float64
}

// ParseSpawnWaves parses a SpawnMonsterComponent JSON object (game data) into waves.
func ParseSpawnWaves(component data.Value) []SpawnWave {
	c, ok := component.(*data.Object)
	if !ok || c == nil {
		return nil
	}
	list, ok := c.Get("SpawnMonsterConfigs").(*data.Array)
	if !ok || list == nil {
		return nil
	}
	var out []SpawnWave
	for _, it := range list.Items {
		o, ok := it.(*data.Object)
		if !ok || o == nil {
			continue
		}
		cc, _ := o.Get("CompleteCondition").(*data.Object)
		pre, _ := o.Get("PreCondition").(*data.Object)
		var preIDs []int32
		if pre != nil {
			preIDs = JSONInts(pre.Get("Ids"))
		}
		var completeType int32
		var quantity *int32
		var duration *float64
		if cc != nil {
			completeType = JSONInt(cc.Get("Type"), 0)
			if q, ok := cc.Get("Quantity").(data.Number); ok {
				qv := int32(q)
				quantity = &qv
			}
			if d, ok := cc.Get("Duration").(data.Number); ok {
				dv := float64(d)
				duration = &dv
			}
		}
		out = append(out, SpawnWave{
			ID:              JSONInt(o.Get("Id"), 0),
			DelayTime:       JSONDouble(o.Get("DelayTime"), 0),
			PreConditionIDs: preIDs,
			TargetsToAwake:  JSONLongs(o.Get("TargetsToAwake")),
			CompleteType:    completeType,
			Quantity:        quantity,
			Duration:        duration,
		})
	}
	return out
}

// SpawnerKey identifies the step a spawner job belongs to: run key and node index.
type SpawnerKey struct {
	Run  int64
	Node int
}

type waveStatus int

const (
	waveWaiting waveStatus = iota
	waveDelayed
	waveActive
	waveDone
)

type spawnerWaveState struct {
	cfg       SpawnWave
	creator   int
	status    waveStatus
	dueMs     int64
	startedMs int64
	woken     int
}

type spawnerJob struct {
	key    SpawnerKey
	waves  []*spawnerWaveState
	killed map[int64]struct{}
	done   bool
}

func (j *spawnerJob) mentions(id int64) bool {
	for _, w := range j.waves {
		if slices.Contains(w.cfg.TargetsToAwake, id) {
			return true
		}
	}
	return false
}

func (j *spawnerJob) depsDone(w *spawnerWaveState) bool {
	for _, dep := range w.cfg.PreConditionIDs {
		for _, other := range j.waves {
			if other.creator == w.creator && other.cfg.ID == dep && other.status != waveDone {
				return false
			}
		}
	}
	return true
}

func (j *spawnerJob) aliveIn(w *spawnerWaveState) int {
	n := 0
	for i := 0; i < w.woken; i++ {
		if _, dead := j.killed[w.cfg.TargetsToAwake[i]]; !dead {
			n++
		}
	}
	return n
}

func (j *spawnerJob) fill(w *spawnerWaveState, wake *[]int64) {
	targets := w.cfg.TargetsToAwake
	capAlive := len(targets)
	if w.cfg.CompleteType == 2 {
		q := 1
		if w.cfg.Quantity != nil && int(*w.cfg.Quantity) > 1 {
			q = int(*w.cfg.Quantity)
		}
		capAlive = q
	}
	for w.woken < len(targets) {
		id := targets[w.woken]
		if _, dead := j.killed[id]; dead {
			// Killed before its wave (open-world rows are out anyway): counts as done.
			w.woken++
			continue
		}
		if j.aliveIn(w) >= capAlive {
			break
		}
		*wake = append(*wake, id)
		w.woken++
	}
}

func (j *spawnerJob) waveComplete(w *spawnerWaveState, nowMs int64) bool {
	allDead := w.woken == len(w.cfg.TargetsToAwake) && j.aliveIn(w) == 0
	if w.cfg.CompleteType == 1 {
		var dur float64
		if w.cfg.Duration != nil {
			dur = *w.cfg.Duration
		}
		durMs := int64(dur * 1000)
		return (allDead && len(w.cfg.TargetsToAwake) > 0) || nowMs-w.startedMs >= durMs
	}
	return allDead
}

func (j *spawnerJob) advance(nowMs int64, wake *[]int64) bool {
	changed := true
	for changed {
		changed = false
		for _, w := range j.waves {
			switch w.status {
			case waveWaiting:
				if j.depsDone(w) {
					w.status = waveDelayed
					w.dueMs = nowMs + int64(w.cfg.DelayTime*1000)
					changed = true
				}
			case waveDelayed:
				if nowMs >= w.dueMs {
					w.status = waveActive
					w.startedMs = nowMs
					j.fill(w, wake)
					changed = true
				}
			case waveActive:
				j.fill(w, wake)
				if j.waveComplete(w, nowMs) {
					w.status = waveDone
					changed = true
				}
			}
		}
	}
	for _, w := range j.waves {
		if w.status != waveDone || j.aliveIn(w) != 0 {
			return false
		}
	}
	j.done = true
	return true
}

func (j *spawnerJob) nextDue() (int64, bool) {
	var best int64
	found := false
	for _, w := range j.waves {
		var due int64
		hasDue := false
		switch w.status {
		case waveDelayed:
			due = w.dueMs
			hasDue = true
		case waveActive:
			if w.cfg.CompleteType == 1 {
				var dur float64
				if w.cfg.Duration != nil {
					dur = *w.cfg.Duration
				}
				due = w.startedMs + int64(dur*1000)
				hasDue = true
			}
		}
		if hasDue && (!found || due < best) {
			best = due
			found = true
		}
	}
	return best, found
}

// SpawnerJobs runs MonsterCreator steps that name spawner entities (MonsterCreatorEntityIds, R-4f).
// Pure state machine: returns monster ids to wake and steps that finished. Ported from SpawnerJobs.cs.
type SpawnerJobs struct {
	jobs []*spawnerJob
}

func NewSpawnerJobs() *SpawnerJobs { return &SpawnerJobs{} }

func (s *SpawnerJobs) Active() bool { return len(s.jobs) > 0 }

// NextDue returns the earliest DelayTime or Duration deadline of a running wave.
func (s *SpawnerJobs) NextDue() (int64, bool) {
	var best int64
	found := false
	for _, j := range s.jobs {
		if d, ok := j.nextDue(); ok && (!found || d < best) {
			best = d
			found = true
		}
	}
	return best, found
}

// Mentions reports whether some running step counts configID.
func (s *SpawnerJobs) Mentions(configID int64) bool {
	for _, j := range s.jobs {
		if !j.done && j.mentions(configID) {
			return true
		}
	}
	return false
}

func (s *SpawnerJobs) Has(key SpawnerKey) bool {
	for _, j := range s.jobs {
		if j.key == key {
			return true
		}
	}
	return false
}

// Start begins a step's spawners (one wave slice per spawner entity). Appends ids to wake;
// returns true if the step is already done.
func (s *SpawnerJobs) Start(key SpawnerKey, creators [][]SpawnWave, nowMs int64, wake *[]int64) bool {
	s.Drop(key)
	var waves []*spawnerWaveState
	for ci, c := range creators {
		for _, cfg := range c {
			waves = append(waves, &spawnerWaveState{cfg: cfg, creator: ci})
		}
	}
	job := &spawnerJob{key: key, waves: waves, killed: map[int64]struct{}{}}
	s.jobs = append(s.jobs, job)
	finished := job.advance(nowMs, wake)
	if finished {
		s.Drop(key)
	}
	return finished
}

// OnKilled records a monster death. Appends newly woken ids to wake; returns (key, true) if that
// job finished.
func (s *SpawnerJobs) OnKilled(configID int64, nowMs int64, wake *[]int64) (SpawnerKey, bool) {
	for _, job := range s.jobs {
		if job.done || !job.mentions(configID) {
			continue
		}
		job.killed[configID] = struct{}{}
		if job.advance(nowMs, wake) {
			key := job.key
			s.Drop(key)
			return key, true
		}
		return SpawnerKey{}, false
	}
	return SpawnerKey{}, false
}

// Tick advances DelayTime and Duration waves, appending ids to wake and completed steps to finished.
func (s *SpawnerJobs) Tick(nowMs int64, wake *[]int64, finished *[]SpawnerKey) {
	start := len(*finished)
	for _, job := range s.jobs {
		if !job.done && job.advance(nowMs, wake) {
			*finished = append(*finished, job.key)
		}
	}
	for _, k := range (*finished)[start:] {
		s.Drop(k)
	}
}

func (s *SpawnerJobs) Drop(key SpawnerKey) {
	s.removeWhere(func(j *spawnerJob) bool { return j.key == key })
}

func (s *SpawnerJobs) DropRun(run int64) {
	s.removeWhere(func(j *spawnerJob) bool { return j.key.Run == run })
}

func (s *SpawnerJobs) removeWhere(match func(*spawnerJob) bool) {
	for i := 0; i < len(s.jobs); {
		if match(s.jobs[i]) {
			last := len(s.jobs) - 1
			s.jobs[i] = s.jobs[last]
			s.jobs = s.jobs[:last]
		} else {
			i++
		}
	}
}
