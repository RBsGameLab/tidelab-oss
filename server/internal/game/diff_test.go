// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	"tidelab/internal/combat"
	"tidelab/internal/common"
	"tidelab/internal/data"
	"tidelab/internal/quests"
	"tidelab/internal/world"
	"tidelab/protocol"
)

// recClient records pushes as "<message name> <base64>" lines, as the C# dumper writes them.
type recClient struct {
	id   int32
	msgs []proto.Message
}

func (r *recClient) PlayerID() int32                              { return r.id }
func (r *recClient) Push(m proto.Message)                         { r.msgs = append(r.msgs, m) }
func (r *recClient) PushID(_ protocol.MessageID, m proto.Message) { r.msgs = append(r.msgs, m) }

// TestAgainstCSharp runs the scenario of the C# dumper (csdump.cs) on the real data and compares
// every push and every save file with what the C# server made. Set TIDELAB_DIFF to the dumper's
// output directory and TIDELAB_DATA to the game data (…/gamedata/3.8).
//
// The C# server and the dumper were removed in R-144h; both are in commit 422979f
// (testdata/csdump.cs ran against src/Tidelab.GameServer there), so a dump comes from a checkout
// of that commit. Since then Go answers two things differently on purpose (DarkCoastInfo carries
// DragonPoolConf, PhantomInteractionUnlockNotify lists only phantoms with a
// CalabashDevelopReward row), so a 3.8 dump no longer matches in full.
func TestAgainstCSharp(t *testing.T) {
	csDir, dataDir := os.Getenv("TIDELAB_DIFF"), os.Getenv("TIDELAB_DATA")
	if csDir == "" || dataDir == "" {
		t.Skip("TIDELAB_DIFF and TIDELAB_DATA not set")
	}
	runAgainstCSharp(t, csDir, dataDir, "")
}

// TestAgainstCSharpWithQuests runs csdump.cs with real quest data (TIDELAB_QUESTS, e.g.
// …/gamedata/3.8/questgen/assets/quest) and compares every push and every save file against C#.
func TestAgainstCSharpWithQuests(t *testing.T) {
	csDir, dataDir, questDir := os.Getenv("TIDELAB_DIFF_QUESTS"), os.Getenv("TIDELAB_DATA"), os.Getenv("TIDELAB_QUESTS")
	if csDir == "" || dataDir == "" || questDir == "" {
		t.Skip("TIDELAB_DIFF_QUESTS, TIDELAB_DATA, and TIDELAB_QUESTS not set")
	}
	runAgainstCSharp(t, csDir, dataDir, questDir)
}

func runAgainstCSharp(t *testing.T, csDir, dataDir, questDir string) {
	bindata, more := filepath.Join(dataDir, "BinData"), filepath.Join(dataDir, "BinData-beta")
	tables, err := data.Load(data.Source{JSONDir: bindata, CacheDir: filepath.Join(t.TempDir(), "cache"), MoreJSONDirs: []string{more}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{bindata, more}
	roads, err := world.ReadRoads(filepath.Join(bindata, "AutoPilotCircles.json"))
	if err != nil {
		t.Fatal(err)
	}
	wd := world.NewWorldData(tables, roads)
	wd.JSONDirs = dirs
	commonPath := ""
	for _, d := range dirs {
		if p := filepath.Join(d, "CommonParam.json"); fileExists(p) {
			commonPath = p
			break
		}
	}
	var questCat *quests.Catalog
	if questDir != "" {
		questCat = quests.LoadCatalog(questDir)
	}
	state := filepath.Join(t.TempDir(), "state")
	clock := newVirtualClock(1_800_000_000)
	g := NewWorldGame(state, wd, clock, combat.NewShared(tables, commonPath))
	g.Rng = combat.NewDotNetRandom(7)
	if questCat != nil {
		g.EnableQuests(questCat)
	}
	c := &recClient{id: 777}
	p := g.NewSession(c)
	p.Enter(false)
	clock.Advance(1000)
	p.Heartbeat()
	clock.Advance(1000)
	var role int64
	for _, r := range p.Formation().FilledRoles() {
		if r.RoleID == p.Formation().CurRole {
			role = r.EntityID
		}
	}
	at := p.Scene.PlayerPos
	p.Move(&protocol.MovePackagePush{MovingEntities: []*protocol.MovingEntityData{{EntityId: role,
		MoveInfos: []*protocol.MoveReplaySample{{Location: &protocol.Vector{X: at.X + 2000, Y: at.Y, Z: at.Z}}}}}})
	clock.Advance(1000)
	p.Heartbeat()
	clock.Advance(40_000)
	p.Heartbeat()

	// Combat: go to the nearest awake monster candidate, then hit the first monster until it dies.
	c2 := p.Combat
	now := clock.RealMs
	note := func(s string) { c.msgs = append(c.msgs, noteMsg(s)) }
	from := p.Scene.PlayerPos
	var target *world.Candidate
	for _, x := range wd.CandidatesOf(8) {
		if b := wd.Blueprint(x.Row.BlueprintType); x.Sleeping || b == nil || b.EntityLogic != "Monster" {
			continue
		}
		if target == nil || x.Location.DistanceTo(from) < target.Location.DistanceTo(from) {
			target = x
		}
	}
	note(fmt.Sprintf("goto %d", target.EntityID()))
	p.Move(&protocol.MovePackagePush{MovingEntities: []*protocol.MovingEntityData{{EntityId: role,
		MoveInfos: []*protocol.MoveReplaySample{{Location: &protocol.Vector{X: target.Location.X + 300, Y: target.Location.Y, Z: target.Location.Z}}}}}})
	clock.Advance(1000)
	p.Heartbeat()
	var dmg *data.Damage
	for _, d := range tables.Damage {
		if d.CalculateType == 0 && len(d.RateLv) > 0 && (dmg == nil || d.Id < dmg.Id) {
			dmg = d
		}
	}
	var monster *combat.CombatEntity
	for _, e := range c2.Scene.Entities.All() {
		if e.IsMonster() && e.Life() > 0 && (monster == nil || e.NetID < monster.NetID) {
			monster = e
		}
	}
	mid := ""
	if monster != nil {
		mid = fmt.Sprint(monster.NetID)
	}
	note(fmt.Sprintf("monster %s damage %d", mid, int64(dmg.Id)))
	for i := 0; i < 300 && monster != nil && monster.Life() > 0; i++ {
		pack := &protocol.CombatSendPackRequest{Data: []*protocol.CombatSendData{{Message: &protocol.CombatSendData_Request{Request: &protocol.CombatRequestData{
			RequestId: int32(i + 1), CombatCommon: &protocol.CombatCommon{EntityId: role},
			Message: &protocol.CombatRequestData_DamageExecuteRequest{DamageExecuteRequest: &protocol.DamageExecuteRequest{
				DamageId: int64(dmg.Id), AttackerEntityId: role, TargetEntityId: monster.NetID, SkillLevel: 10}}}}}}}
		c.Push(c2.Combat.HandleCombatSendPack(pack, now(), c))
		clock.Advance(100)
	}
	life := ""
	if monster != nil {
		life = fmt.Sprint(monster.Life())
	}
	note("life " + life)
	clock.Advance(9000)
	p.Heartbeat()

	// Formation, echoes, weapons, a fall, revive.
	c.Push(c2.Formation.HandleUpdateFormation(&protocol.UpdateFormationRequest{Formations: []*protocol.FightFormation{
		{FormationId: 1, IsCurrent: true, CurRole: 1506, RoleIds: []int32{1506, 1211, 1102}}}}, now(), c))
	echo := slices.Min(c2.Player.Echoes.Keys())
	c.Push(c2.Inventory.HandlePhantomPutOn(&protocol.PhantomPutOnRequest{RoleId: 1211, Pos: 0, IncId: echo}, now(), c))
	w := c2.Player.Roles.At(1211).Weapon
	minExp := tables.WeaponExpItem[0].Id
	for _, x := range tables.WeaponExpItem {
		minExp = min(minExp, x.Id)
	}
	c.Push(c2.Inventory.HandleWeaponLevelUp(&protocol.WeaponLevelUpRequest{IncId: w, ConsumeList: []*protocol.WeaponConsumeItem{{ItemId: minExp, Count: 5}}}, now(), c))
	c.Push(c2.Combat.HandleLandingDamage(&protocol.LandingDamageRequest{EntityId: p.Formation().FilledRoles()[0].EntityID, SpeedZ: -5000, TimeExceeding: 900}, now(), c))
	c.Push(c2.Combat.HandleRevive(&protocol.ReviveRequest{}, now(), c))
	clock.Advance(31_000)
	p.Heartbeat()
	if questCat != nil {
		for f := 0; f < 5; f++ {
			playing := p.Flows().Playing()
			if playing == nil {
				break
			}
			inc := *playing
			note(fmt.Sprintf("flowend %d", inc))
			c.Push(p.FlowEnd(&protocol.FlowEndRequest{FlowIncId: inc}))
			p.RunAfterResponse()
			clock.Advance(1000)
			p.Heartbeat()
		}
		steps := p.Quests().ActiveSteps(false)
		if len(steps) > 0 {
			st := steps[0]
			note(fmt.Sprintf("submit %d %d", st.Run, st.Def.ID))
			c.Push(p.SubmitNode(&protocol.SubmitNodeRequest{TreeIncId: st.Run, NodeId: st.Def.ID}))
			p.RunAfterResponse()
			clock.Advance(1000)
			p.Heartbeat()
		}
		for f := 0; f < 5; f++ {
			playing := p.Flows().Playing()
			if playing == nil {
				break
			}
			inc := *playing
			note(fmt.Sprintf("flowend2 %d", inc))
			c.Push(p.FlowEnd(&protocol.FlowEndRequest{FlowIncId: inc}))
			p.RunAfterResponse()
			clock.Advance(1000)
			p.Heartbeat()
		}
		if offer, ok := questCat.Data.Offers[11000031]; ok && offer.Option != nil {
			opt := offer.Option
			note(fmt.Sprintf("offer %d %d", offer.ID, opt.EntityID))
			c.Push(p.EntityDynamicInteract(&protocol.EntityDynamicInteractRequest{EntityId: opt.EntityID, OptionGuid: opt.Guid}))
			p.RunAfterResponse()
			for f := 0; f < 5; f++ {
				playing := p.Flows().Playing()
				if playing == nil {
					break
				}
				inc := *playing
				note(fmt.Sprintf("offerflow %d", inc))
				c.Push(p.FlowEnd(&protocol.FlowEndRequest{FlowIncId: inc}))
				p.RunAfterResponse()
				clock.Advance(1000)
				p.Heartbeat()
			}
		}
		instReq := &protocol.InstLevelPlayStateReq{InstId: 8}
		for i, wp := range questCat.WorldPlays() {
			if i >= 5 {
				break
			}
			instReq.LevelPlayIds = append(instReq.LevelPlayIds, wp.ID)
		}
		c.Push(p.LevelPlayStateList(&protocol.LevelPlayStateListRequest{
			InstLevelPlayStateReqs: []*protocol.InstLevelPlayStateReq{instReq},
		}))
		p.Release()
		note("reconnect")
		p2 := g.NewSession(c)
		p2.Enter(true)
		clock.Advance(1000)
		p2.Heartbeat()
		p2.Release()
	} else {
		p.Release()
	}

	// The pushes, one by one.
	b, err := os.ReadFile(filepath.Join(csDir, "pushes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n")), "\n")
	csLines := lines
	differ := 0
	n := max(len(csLines), len(c.msgs))
	for i := 0; i < n; i++ {
		var cs, gm proto.Message
		var csName, goName string
		if i < len(csLines) && strings.HasPrefix(csLines[i], "note ") {
			got := ""
			if i < len(c.msgs) {
				if nt, ok := c.msgs[i].(*noteText); ok {
					got = "note " + nt.s
				} else {
					got = msgName(c.msgs[i])
				}
			}
			if csLines[i] != got {
				t.Fatalf("push %d: C# %q, Go %q", i, csLines[i], got)
			}
			continue
		}
		if i < len(csLines) {
			name, b64, _ := strings.Cut(csLines[i], " ")
			csName = name
			id, ok := protocol.IDByName(name)
			if !ok {
				t.Fatalf("push %d: unknown message %s", i, name)
			}
			raw, _ := base64.StdEncoding.DecodeString(b64)
			cs = protocol.New(id)
			if err := proto.Unmarshal(raw, cs); err != nil {
				t.Fatal(err)
			}
		}
		if i < len(c.msgs) {
			gm = c.msgs[i]
			if nt, ok := gm.(*noteText); ok {
				goName = "note " + nt.s
			} else {
				goName = msgName(gm)
			}
		}
		if csName != goName {
			t.Errorf("push %d: C# %s, Go %s", i, csName, goName)
			differ++
			continue
		}
		if !proto.Equal(cs, gm) {
			differ++
			if differ <= 12 {
				t.Errorf("push %d %s differs: %s", i, csName, firstDiff(prototext.Format(cs), prototext.Format(gm)))
			}
		}
	}
	t.Logf("%d pushes from C# (%d raw), %d from Go, %d differ", len(csLines), len(lines), len(c.msgs), differ)

	// The save files, byte for byte.
	csState := filepath.Join(csDir, "state", "player", "777")
	goState := filepath.Join(state, "player", "777")
	files := map[string]bool{}
	for _, root := range []string{csState, goState} {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(root, path)
				files[filepath.ToSlash(rel)] = true
			}
			return nil
		})
	}
	same := 0
	for f := range files {
		a, errA := os.ReadFile(filepath.Join(csState, f))
		b, errB := os.ReadFile(filepath.Join(goState, f))
		switch {
		case errA != nil:
			t.Errorf("save %s: only Go wrote it", f)
		case errB != nil:
			t.Errorf("save %s: only C# wrote it", f)
		case !bytes.Equal(a, b):
			t.Errorf("save %s differs:\nC#: %s\nGo: %s", f, clip(string(a), 600), clip(string(b), 600))
		default:
			same++
		}
	}
	t.Logf("%d save files, %d the same", len(files), same)
}

// firstDiff is the first line where two texts differ, with the lines before it.
func firstDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < max(len(la), len(lb)); i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			ctx := strings.Join(la[max(0, i-12):i], "\n")
			return fmt.Sprintf("line %d\n%s\nC#: %s\nGo: %s", i, ctx, x, y)
		}
	}
	return "(same text)"
}

// noteText is a note line of the scenario, kept among the pushes.
type noteText struct {
	proto.Message
	s string
}

func noteMsg(s string) proto.Message { return &noteText{s: s} }

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + fmt.Sprintf("… (%d more)", len(s)-n)
	}
	return s
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func newVirtualClock(epoch int64) *common.GameClock {
	c := common.NewGameClock(nil)
	c.UseVirtual(epoch)
	return c
}
