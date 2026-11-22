// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package robot is tidelab's robot (R-143i, R-144g): data checks, world spawn checks, and the
// story, side, dungeon and targeted self-test robots, driving the real gateway and game in
// memory on a copy of a player's save. Ported from our C# Tidelab.Robot.
package robot

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// Mode is what the robot runs.
type Mode int

const (
	ModeAssets Mode = iota
	ModeNpcs
	ModeStory
	ModeSide
	ModeOffers
	ModeWorldInst
	ModePos
	ModeRide
	ModeChairs
	ModeShop
	ModeLanding
	ModePatrol
	ModeWeapons
	ModeActivity
	ModeDungeons
)

// Options is the parsed command line.
type Options struct {
	Mode          Mode
	BinData       string
	Cache         string
	MoreBinData   []string
	QuestData     string
	StateDir      *string
	PlayerID      int32
	Epoch         *int64
	LogsDir       string
	ReportPath    *string
	NpcListPath   *string
	RobotStateDir string
	CheckpointDir string
	Verify        bool
	Threads       int
	Verbose       bool

	// Story options
	StoryStartQuest *int32
	MaxQuests       int
	SkipStuck       bool
	WithSide        bool
	TryOptions      bool
	KeepState       bool
	SaveState       *string
	FromState       *string

	// Side sharding (R-130)
	ShardIndex int
	ShardCount int

	// Dungeon targets
	DungeonsAll    bool
	DungeonsQuests bool
	DungeonIDs     []int32
}

func defaultOptions() *Options {
	return &Options{
		BinData: filepath.Join("assets", "BinData"), Cache: filepath.Join("assets", "BinCache"),
		QuestData: filepath.Join("assets", "quest"), PlayerID: 777, LogsDir: filepath.Join("..", "logs"),
		RobotStateDir: "robot_state", CheckpointDir: "robot_ckpt", MaxQuests: math.MaxInt32, ShardCount: 1,
	}
}

// ResolveReportPath is where the TSV report goes for the active mode.
func (o *Options) ResolveReportPath() string {
	if o.ReportPath != nil {
		return *o.ReportPath
	}
	file := "robot_dungeons_ids.tsv"
	switch {
	case o.Mode == ModeStory:
		file = "robot_story.tsv"
	case o.Mode == ModeSide:
		file = "robot_side.tsv"
	case o.Mode == ModeDungeons && o.DungeonsAll:
		file = "robot_dungeons.tsv"
	case o.Mode == ModeDungeons && o.DungeonsQuests:
		file = "robot_quest_dungeons.tsv"
	}
	return filepath.Join(o.LogsDir, file)
}

// ShardRange is the slice [start, end) of total quests for this shard.
func (o *Options) ShardRange(total int) (int, int) {
	n := max(o.ShardCount, 1)
	k := min(max(o.ShardIndex, 0), n-1)
	return total * k / n, total * (k + 1) / n
}

// dotnetInt is .NET int.TryParse(s, NumberStyles.Integer, Invariant): blanks around, a sign, digits.
func dotnetInt(s string, bits int) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	t := s
	if t[0] == '+' || t[0] == '-' {
		t = t[1:]
	}
	if t == "" {
		return 0, false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(strings.TrimPrefix(s, "+"), 10, bits)
	return v, err == nil
}

// digitsOnly is NumberStyles.None: digits only.
func digitsOnly(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 32)
	return int(v), err == nil
}

// ParseOptions parses the command line; an error says what was wrong.
func ParseOptions(args []string) (*Options, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no mode given")
	}
	o := defaultOptions()
	var mode *Mode
	setMode := func(m Mode) { mode = &m }
	takeValue := func(i int, arg string) (string, int, bool) {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			return arg[eq+1:], i, true
		}
		if i+1 < len(args) {
			return args[i+1], i + 1, true
		}
		return "", i, false
	}
	str := func(p *string, i *int, arg, flag string) error {
		v, next, ok := takeValue(*i, arg)
		if !ok {
			return fmt.Errorf("missing value for %s", flag)
		}
		*p, *i = v, next
		return nil
	}
	optStr := func(p **string, i *int, arg, flag string) error {
		var v string
		if err := str(&v, i, arg, flag); err != nil {
			return err
		}
		*p = &v
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			flag := arg
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				flag = arg[:eq]
			}
			var err error
			switch flag {
			case "--verify":
				o.Verify = true
			case "--verbose":
				o.Verbose = true
			case "--skip-stuck":
				o.SkipStuck = true
			case "--with-side":
				o.WithSide = true
			case "--try-options":
				o.TryOptions = true
			case "--keep-state":
				o.KeepState = true
			case "--bindata":
				err = str(&o.BinData, &i, arg, flag)
			case "--cache":
				err = str(&o.Cache, &i, arg, flag)
			case "--more":
				var m string
				if err = str(&m, &i, arg, flag); err == nil {
					for _, part := range strings.Split(m, ";") {
						if part = strings.TrimSpace(part); part != "" {
							o.MoreBinData = append(o.MoreBinData, part)
						}
					}
				}
			case "--quest-data", "--quest_data":
				err = str(&o.QuestData, &i, arg, flag)
			case "--state", "--state_dir":
				err = optStr(&o.StateDir, &i, arg, flag)
			case "--logs":
				err = str(&o.LogsDir, &i, arg, flag)
			case "--report":
				err = optStr(&o.ReportPath, &i, arg, flag)
			case "--npclist":
				err = optStr(&o.NpcListPath, &i, arg, flag)
			case "--robot-dir":
				err = str(&o.RobotStateDir, &i, arg, flag)
			case "--ckpt-dir":
				err = str(&o.CheckpointDir, &i, arg, flag)
			case "--save-state":
				err = optStr(&o.SaveState, &i, arg, flag)
			case "--from":
				err = optStr(&o.FromState, &i, arg, flag)
			case "--player", "--epoch", "--threads", "--quests":
				v, next, ok := takeValue(i, arg)
				bits := 32
				if flag == "--epoch" {
					bits = 64
				}
				n, good := dotnetInt(v, bits)
				if !ok || !good || (flag == "--quests" && n < 0) {
					return nil, fmt.Errorf("bad value for %s", flag)
				}
				i = next
				switch flag {
				case "--player":
					o.PlayerID = int32(n)
				case "--epoch":
					o.Epoch = &n
				case "--threads":
					o.Threads = int(n)
				case "--quests":
					o.MaxQuests = int(n)
				}
			case "--shard":
				v, next, ok := takeValue(i, arg)
				if !ok {
					return nil, fmt.Errorf("missing value for --shard")
				}
				i = next
				slash := strings.IndexByte(v, '/')
				var k, n int
				good := slash > 0
				if good {
					var ok1, ok2 bool
					k, ok1 = digitsOnly(v[:slash])
					n, ok2 = digitsOnly(v[slash+1:])
					good = ok1 && ok2 && n > 0 && k < n
				}
				if !good {
					return nil, fmt.Errorf("bad --shard value %s (want k/n with 0 <= k < n)", v)
				}
				o.ShardIndex, o.ShardCount = k, n
			default:
				return nil, fmt.Errorf("unknown argument %s", arg)
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		switch arg {
		case "assets":
			setMode(ModeAssets)
		case "npcs":
			setMode(ModeNpcs)
		case "story":
			setMode(ModeStory)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				if q, ok := dotnetInt(args[i+1], 32); ok {
					qq := int32(q)
					o.StoryStartQuest = &qq
					i++
				}
			}
		case "side":
			setMode(ModeSide)
		case "offers":
			setMode(ModeOffers)
		case "worldinst":
			setMode(ModeWorldInst)
		case "pos":
			setMode(ModePos)
		case "ride":
			setMode(ModeRide)
		case "chairs":
			setMode(ModeChairs)
		case "shop":
			setMode(ModeShop)
		case "landing":
			setMode(ModeLanding)
		case "patrol":
			setMode(ModePatrol)
		case "weapons":
			setMode(ModeWeapons)
		case "activity":
			setMode(ModeActivity)
		case "all":
			if mode == nil {
				setMode(ModeDungeons)
			}
			o.DungeonsAll = true
		case "quests":
			if mode == nil {
				setMode(ModeDungeons)
			}
			o.DungeonsQuests = true
		default:
			if d, ok := dotnetInt(arg, 32); ok && (mode == nil || *mode == ModeDungeons) {
				setMode(ModeDungeons)
				o.DungeonIDs = append(o.DungeonIDs, int32(d))
				continue
			}
			return nil, fmt.Errorf("unknown argument %s", arg)
		}
	}
	if mode == nil {
		return nil, fmt.Errorf("no mode or dungeon ids given")
	}
	if *mode == ModeDungeons && !o.DungeonsAll && !o.DungeonsQuests && len(o.DungeonIDs) == 0 {
		return nil, fmt.Errorf("no dungeon ids given")
	}
	o.Mode = *mode
	return o, nil
}

// PrintUsage prints the usage lines.
func PrintUsage(w io.Writer) {
	for _, l := range []string{
		"usage: robot <mode> [options]",
		"  modes:",
		"    assets [--verify] [--threads N]",
		"    npcs [--npclist <file>]",
		"    story [<quest id>] [--quests N] [--skip-stuck] [--with-side] [--try-options]",
		"          [--save-state <name>] [--from <name>] [--keep-state]",
		"    side [--shard=k/n]",
		"    offers | worldinst | pos | ride | chairs | shop | landing | patrol | weapons | activity",
		"    <dungeon id>... | all | quests",
		"  common options:",
		"    --state <dir> --player <id> --epoch <s> --bindata <dir> [--more <dir>]...",
		"    --cache <dir> --quest-data <dir> --logs <dir> [--report <file>] [--verbose]",
	} {
		fmt.Fprintln(w, l)
	}
}
