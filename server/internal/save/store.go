// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package save

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"tidelab/internal/common"
	"tidelab/internal/omap"
	"tidelab/internal/zon"
)

// WriteAtomic writes a state file (common.WriteAtomic); a failure panics, as the C# server threw.
func WriteAtomic(path, text string) bool {
	return must(common.WriteAtomic(path, text))
}

func appendText(path, text string) {
	must(0, os.MkdirAll(filepath.Dir(path), 0o755))
	f := must(os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644))
	defer f.Close()
	must(f.WriteString(text))
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// numberedFiles are a directory's files named by a plain decimal number, in name order (as
// Windows lists them).
func numberedFiles(dir string) []struct {
	ID   int32
	Path string
} {
	var out []struct {
		ID   int32
		Path string
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n, ok := ParseDigits(e.Name(), 32); ok {
			out = append(out, struct {
				ID   int32
				Path string
			}{int32(n), filepath.Join(dir, e.Name())})
		}
	}
	return out
}

func parseFile(path string) *zon.Value { return must(zon.ParseFile(path)) }

// UniqueIDCounter is player/<id>/special_item/next, shared by weapons, echoes and echo presets
// (specs/save-format.md §4, inventory.md §1).
type UniqueIDCounter struct {
	path string
	next int32
}

func NewUniqueIDCounter(path string, initialNext int32) *UniqueIDCounter {
	return &UniqueIDCounter{path, max(1, initialNext)}
}

func (c *UniqueIDCounter) Peek() int32 { return c.next }

func LoadUniqueIDCounter(path string, minNext int32) *UniqueIDCounter {
	stored := int32(1)
	if b, err := os.ReadFile(path); err == nil {
		if w := strings.Fields(string(b)); len(w) > 0 {
			if n, ok := ParseSigned(w[0], 32); ok && n >= 1 {
				stored = int32(n)
			}
		}
	}
	return NewUniqueIDCounter(path, max(stored, minNext))
}

// Next allocates one id and writes the counter.
func (c *UniqueIDCounter) Next() int32 {
	id := c.next
	c.next++
	WriteAtomic(c.path, strconv.Itoa(int(c.next))+"\n")
	return id
}

func (c *UniqueIDCounter) EnsureAtLeast(minNext int32) {
	if minNext > c.next {
		c.next = minNext
	}
}

// NormalInventoryStore is the one writer of the normal items: inventory_info and
// inventory_info.log (R-80e, R-108, specs/save-format.md §5.11, inventory.md §2.1).
type NormalInventoryStore struct {
	snapshotPath  string
	logPath       string
	items         omap.Map[int32, int32]
	logLines      int
	changeVersion int64
}

func NewNormalInventoryStore(playerDir string) *NormalInventoryStore {
	return &NormalInventoryStore{
		snapshotPath: filepath.Join(playerDir, "inventory_info"),
		logPath:      filepath.Join(playerDir, "inventory_info.log"),
	}
}

func (s *NormalInventoryStore) Items() *omap.Map[int32, int32] { return &s.items }
func (s *NormalInventoryStore) LogLines() int                  { return s.logLines }

// ChangeVersion goes up at every change, for the quest conditions (R-62).
func (s *NormalInventoryStore) ChangeVersion() int64 { return s.changeVersion }

func (s *NormalInventoryStore) CountOf(id int32) int32 { return s.items.At(id) }

func (s *NormalInventoryStore) Load() {
	s.items.Clear()
	s.logLines = 0
	if common.FileExists(s.snapshotPath) {
		root := parseFile(s.snapshotPath)
		if list := field(root, "normal_items"); isListish(list) {
			for _, e := range items(list) {
				if e.Kind() != zon.Struct {
					continue
				}
				id := zInt(e, "id", 0)
				if n := zInt(e, "count", 1); n > 0 {
					s.items.Set(id, n)
				} else {
					s.items.Delete(id)
				}
			}
		}
	}
	if b, err := os.ReadFile(s.logPath); err == nil {
		for _, raw := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			sp := strings.IndexByte(line, ' ')
			if sp <= 0 {
				continue
			}
			id, ok := ParseSigned(line[:sp], 32)
			if !ok {
				continue
			}
			n, ok := ParseSigned(strings.TrimSpace(line[sp+1:]), 32)
			if !ok {
				continue
			}
			s.logLines++
			if n <= 0 {
				s.items.Delete(int32(id))
			} else {
				s.items.Set(int32(id), int32(n))
			}
		}
	}
}

// Add adds delta (either sign) to the item and writes; the new count.
func (s *NormalInventoryStore) Add(id, delta int32) int32 {
	if delta == 0 {
		return s.CountOf(id)
	}
	next := max(0, s.CountOf(id)+delta)
	s.SetBatch([]ItemCount{{id, next}})
	return next
}

// Remove drops the item entirely.
func (s *NormalInventoryStore) Remove(id int32) {
	if s.items.Has(id) {
		s.SetBatch([]ItemCount{{id, 0}})
	}
}

type ItemCount struct{ ID, Count int32 }

// SetBatch sets new counts (<= 0 removes) and writes them through the snapshot and log.
func (s *NormalInventoryStore) SetBatch(updates []ItemCount) {
	var changed []ItemCount
	for _, u := range updates {
		next := max(0, u.Count)
		if next == s.items.At(u.ID) {
			continue
		}
		if next == 0 {
			s.items.Delete(u.ID)
		} else {
			s.items.Set(u.ID, next)
		}
		changed = append(changed, ItemCount{u.ID, next})
	}
	if len(changed) == 0 {
		return
	}
	s.changeVersion++
	threshold := 4 * max(s.items.Len(), 16)
	if s.logLines+len(changed) <= threshold {
		var b strings.Builder
		for _, c := range changed {
			fmt.Fprintf(&b, "%d %d\n", c.ID, c.Count)
		}
		appendText(s.logPath, b.String())
		s.logLines += len(changed)
	} else {
		s.CompactSnapshot()
	}
}

func (s *NormalInventoryStore) CompactSnapshot() {
	keys := s.items.Keys()
	slices.Sort(keys)
	entries := make([]*zon.Value, len(keys))
	for i, k := range keys {
		entries[i] = zon.NewStruct(zon.F("id", N(int64(k))), zon.F("count", N(int64(s.items.At(k)))))
	}
	WriteAtomic(s.snapshotPath, zon.Write(zon.NewStruct(zon.F("normal_items", zon.NewList(entries...)))))
	if common.FileExists(s.logPath) {
		must(0, os.Remove(s.logPath))
	}
	s.logLines = 0
}

// DirtyFlushDelayMs: role and weapon changes are saved 30 s after the first one since the last
// save, and at once on a scene change or a release (specs/roles.md §7, inventory.md §7).
const DirtyFlushDelayMs = 30_000

type DirtySaveQueue struct {
	roles      omap.Map[int32, struct{}]
	weapons    omap.Map[int32, struct{}]
	firstDirty *int64
}

func (q *DirtySaveQueue) HasPending() bool        { return q.roles.Len() > 0 || q.weapons.Len() > 0 }
func (q *DirtySaveQueue) PendingRoles() []int32   { return q.roles.Keys() }
func (q *DirtySaveQueue) PendingWeapons() []int32 { return q.weapons.Keys() }

func (q *DirtySaveQueue) mark(m *omap.Map[int32, struct{}], id int32, nowMs int64) {
	m.Set(id, struct{}{})
	if q.firstDirty == nil {
		q.firstDirty = &nowMs
	}
}

func (q *DirtySaveQueue) MarkRole(id int32, nowMs int64)   { q.mark(&q.roles, id, nowMs) }
func (q *DirtySaveQueue) MarkWeapon(id int32, nowMs int64) { q.mark(&q.weapons, id, nowMs) }
func (q *DirtySaveQueue) UnmarkWeapon(id int32)            { q.weapons.Delete(id) }

func (q *DirtySaveQueue) IsDue(nowMs int64) bool {
	return q.firstDirty != nil && q.HasPending() && nowMs-*q.firstDirty >= DirtyFlushDelayMs
}

func (q *DirtySaveQueue) Drain() (roles, weapons []int32) {
	if !q.HasPending() {
		return nil, nil
	}
	roles, weapons = q.roles.Keys(), q.weapons.Keys()
	q.roles.Clear()
	q.weapons.Clear()
	q.firstDirty = nil
	return
}

// PlayerData is the persistent state of one player that R-143g owns, in memory.
type PlayerData struct {
	PlayerID  int32
	StateDir  string
	PlayerDir string

	BasicInfo       BasicInfoSave
	CosmeticInfo    CosmeticInfoSave
	FormationInfo   *FormationInfoSave
	Roles           omap.Map[int32, *RoleSave]
	Weapons         omap.Map[int32, *WeaponSave]
	Echoes          omap.Map[int32, *EchoSave]
	EchoPresets     *EchoPresetsSave
	Calabash        *CalabashSave
	NormalInventory *NormalInventoryStore
	SpecialItemIDs  *UniqueIDCounter
	DirtySaves      DirtySaveQueue

	RoleDirExistedOnLoad    bool
	WeaponDirExistedOnLoad  bool
	EchoDirHadEntriesOnLoad bool
	BasicInfoExistedOnLoad  bool
}

func NewPlayerData(stateDir string, playerID int32) *PlayerData {
	dir := filepath.Join(stateDir, "player", strconv.Itoa(int(playerID)))
	return &PlayerData{
		PlayerID:        playerID,
		StateDir:        stateDir,
		PlayerDir:       dir,
		BasicInfo:       NewBasicInfo(),
		CosmeticInfo:    NewCosmeticInfo(),
		FormationInfo:   NewFormationInfo(),
		EchoPresets:     &EchoPresetsSave{},
		Calabash:        NewCalabash(),
		NormalInventory: NewNormalInventoryStore(dir),
		SpecialItemIDs:  NewUniqueIDCounter(filepath.Join(dir, "special_item", "next"), 1),
	}
}

// EquippedEchoes are the role's 5 echo slots (nil: empty).
func (p *PlayerData) EquippedEchoes(roleID int32) [5]*EchoSave {
	var slots [5]*EchoSave
	for _, e := range p.Echoes.All() {
		if e.RoleID != nil && *e.RoleID == roleID && e.EquippedPos >= 0 && e.EquippedPos < 5 {
			slots[e.EquippedPos] = e
		}
	}
	return slots
}

// EquippedWeapon is the role's weapon, nil when it has none.
func (p *PlayerData) EquippedWeapon(r *RoleSave) *WeaponSave {
	if r.Weapon <= 0 {
		return nil
	}
	return p.Weapons.At(r.Weapon)
}

// LoadPlayer reads R-143g's files of the player (specs/save-format.md).
func LoadPlayer(stateDir string, playerID int32) *PlayerData {
	d := NewPlayerData(stateDir, playerID)
	dir := d.PlayerDir
	if p := filepath.Join(dir, "basic_info"); common.FileExists(p) {
		d.BasicInfoExistedOnLoad = true
		d.BasicInfo = BasicInfoFromZon(parseFile(p))
	}
	if p := filepath.Join(dir, "cosmetic_info"); common.FileExists(p) {
		d.CosmeticInfo = CosmeticInfoFromZon(parseFile(p))
	}
	if p := filepath.Join(dir, "formation_info"); common.FileExists(p) {
		d.FormationInfo = FormationInfoFromZon(parseFile(p))
	}
	if rd := filepath.Join(dir, "role"); dirExists(rd) {
		d.RoleDirExistedOnLoad = true
		for _, f := range numberedFiles(rd) {
			d.Roles.Set(f.ID, RoleFromZon(f.ID, parseFile(f.Path)))
		}
	}
	var maxWeapon, maxEcho int32
	if wd := filepath.Join(dir, "weapon"); dirExists(wd) {
		d.WeaponDirExistedOnLoad = true
		for _, f := range numberedFiles(wd) {
			d.Weapons.Set(f.ID, WeaponFromZon(f.ID, parseFile(f.Path)))
			maxWeapon = max(maxWeapon, f.ID)
		}
	}
	if ed := filepath.Join(dir, "echo"); dirExists(ed) {
		for _, f := range numberedFiles(ed) {
			d.Echoes.Set(f.ID, EchoFromZon(f.ID, parseFile(f.Path)))
			maxEcho = max(maxEcho, f.ID)
		}
		d.EchoDirHadEntriesOnLoad = d.Echoes.Len() > 0
		if p := filepath.Join(ed, "presets"); common.FileExists(p) {
			d.EchoPresets = EchoPresetsFromZon(parseFile(p))
		}
		if p := filepath.Join(ed, "calabash"); common.FileExists(p) {
			d.Calabash = CalabashFromZon(parseFile(p))
		}
	}
	d.NormalInventory.Load()
	var maxPreset int32
	for _, g := range d.EchoPresets.Groups {
		maxPreset = max(maxPreset, g.IncrID)
	}
	minNext := max(1, maxWeapon+1, maxEcho+1, maxPreset+1)
	d.SpecialItemIDs = LoadUniqueIDCounter(filepath.Join(dir, "special_item", "next"), minNext)
	return d
}

func writeZon(path string, v *zon.Value) { WriteAtomic(path, zon.Write(v)) }

func idName(id int32) string { return strconv.Itoa(int(id)) }

func SaveBasicInfo(d *PlayerData) {
	writeZon(filepath.Join(d.PlayerDir, "basic_info"), d.BasicInfo.ToZon())
}
func SaveCosmeticInfo(d *PlayerData) {
	writeZon(filepath.Join(d.PlayerDir, "cosmetic_info"), d.CosmeticInfo.ToZon())
}
func SaveFormationInfo(d *PlayerData) {
	writeZon(filepath.Join(d.PlayerDir, "formation_info"), d.FormationInfo.ToZon())
}
func SaveRole(d *PlayerData, r *RoleSave) {
	writeZon(filepath.Join(d.PlayerDir, "role", idName(r.RoleID)), r.ToZon())
}
func SaveWeapon(d *PlayerData, w *WeaponSave) {
	writeZon(filepath.Join(d.PlayerDir, "weapon", idName(w.UniqueID)), w.ToZon())
}

func DeleteWeapon(d *PlayerData, uniqueID int32) {
	d.DirtySaves.UnmarkWeapon(uniqueID)
	p := filepath.Join(d.PlayerDir, "weapon", idName(uniqueID))
	if common.FileExists(p) {
		must(0, os.Remove(p))
	}
}

func SaveEcho(d *PlayerData, e *EchoSave) {
	writeZon(filepath.Join(d.PlayerDir, "echo", idName(e.UniqueID)), e.ToZon())
}

// DeleteEcho removes an echo's file (the caller drops it from d.Echoes).
func DeleteEcho(d *PlayerData, uniqueID int32) {
	p := filepath.Join(d.PlayerDir, "echo", idName(uniqueID))
	if common.FileExists(p) {
		must(0, os.Remove(p))
	}
}
func SaveEchoPresets(d *PlayerData) {
	writeZon(filepath.Join(d.PlayerDir, "echo", "presets"), d.EchoPresets.ToZon())
}
func SaveCalabash(d *PlayerData) {
	writeZon(filepath.Join(d.PlayerDir, "echo", "calabash"), d.Calabash.ToZon())
}

func FlushDirty(d *PlayerData) {
	roles, weapons := d.DirtySaves.Drain()
	for _, id := range roles {
		if r, ok := d.Roles.Get(id); ok {
			SaveRole(d, r)
		}
	}
	for _, id := range weapons {
		if w, ok := d.Weapons.Get(id); ok {
			SaveWeapon(d, w)
		}
	}
}

func FlushDirtyIfDue(d *PlayerData, nowMs int64) {
	if d.DirtySaves.IsDue(nowMs) {
		FlushDirty(d)
	}
}

// SyncAccountSex writes account/<link>.sex when the player switches the Rover's sex
// (specs/save-format.md §3, roles.md §6.10).
func SyncAccountSex(d *PlayerData, sex int32) {
	linkDir := filepath.Join(d.StateDir, "account", "link")
	entries, err := os.ReadDir(linkDir)
	if err != nil {
		return
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		linkFile := filepath.Join(linkDir, e.Name())
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.Warn("save", fmt.Sprintf("could not sync account sex for %s: %v", linkFile, r))
				}
			}()
			link := parseFile(linkFile)
			if zInt(link, "player_id", 0) != d.PlayerID {
				return
			}
			account := filepath.Join(d.StateDir, "account", e.Name())
			if !common.FileExists(account) {
				return
			}
			updated := must(parseFile(account).With("sex", N(int64(sex))))
			writeZon(account, updated)
		}()
	}
}
