// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package common holds what every tidelab program shares: options, log lines, the product
// banner and safe writes under state/ (ported from src/Tidelab.Common).
package common

import (
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ProductName = "tidelab"
	// ClientVersion is the game client version the servers speak to (R-111: 3.8).
	ClientVersion = "3.8.0"
)

// Banner is the line a program prints when it starts.
func Banner(program string) string {
	return fmt.Sprintf("%s %s for client %s", ProductName, program, ClientVersion)
}

// Options are --name value or --name=value pairs against declared defaults; an unknown
// option, or one without a value, is an error, so a typo never silently runs with a default.
type Options struct {
	values map[string]string
}

// ParseOptions parses args against the options and defaults given.
func ParseOptions(args []string, defaults map[string]string) (*Options, error) {
	values := make(map[string]string, len(defaults))
	for k, v := range defaults {
		values[k] = v
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") || len(arg) == 2 {
			return nil, fmt.Errorf("unexpected argument: %s", arg)
		}
		var name, value string
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			name, value = arg[2:eq], arg[eq+1:]
		} else {
			name = arg[2:]
			if i+1 >= len(args) {
				return nil, fmt.Errorf("option --%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if _, ok := defaults[name]; !ok {
			return nil, fmt.Errorf("unknown option: --%s", name)
		}
		values[name] = value
	}
	return &Options{values}, nil
}

// Get is the option's value (its default when not given).
func (o *Options) Get(name string) string {
	return o.values[name]
}

// Endpoint is the option as host:port with a numeric IPv4 or IPv6 host.
func (o *Options) Endpoint(name string) (netip.AddrPort, error) {
	text := o.values[name]
	ap, err := netip.ParseAddrPort(text)
	if err != nil || (ap.Port() == 0 && !strings.HasSuffix(text, ":0")) {
		return netip.AddrPort{}, fmt.Errorf("--%s is not an address with a port: %s", name, text)
	}
	return ap, nil
}

var (
	logMu sync.Mutex
	// LogOutput is where log lines go; tests swap it. Standard error by default.
	LogOutput io.Writer = os.Stderr
	// LogClock, when set, stamps every line with its local time (gamesv: tools/tl_audit.py
	// matches the server log to the client log's time span, R-166). Tests leave it nil.
	LogClock func() time.Time
)

// LogStampLayout is the time stamp LogClock puts in front of a line.
const LogStampLayout = "2006-01-02 15:04:05.000"

// Log lines are "level(scope): message", one per line, after "<LogStampLayout> " when LogClock
// is set. The start scripts and the robots read them (for example "is listening at"), so the
// shape is part of the interface.
func logLine(level, scope, message string) {
	logMu.Lock()
	defer logMu.Unlock()
	if LogClock != nil {
		fmt.Fprintf(LogOutput, "%s %s(%s): %s\n", LogClock().Format(LogStampLayout), level, scope, message)
		return
	}
	fmt.Fprintf(LogOutput, "%s(%s): %s\n", level, scope, message)
}

func Info(scope, message string)  { logLine("info", scope, message) }
func Warn(scope, message string)  { logLine("warning", scope, message) }
func Error(scope, message string) { logLine("error", scope, message) }

// CaptureLog sends log lines to a buffer until the returned function is called (tests).
func CaptureLog() (*SyncBuffer, func()) {
	logMu.Lock()
	defer logMu.Unlock()
	old := LogOutput
	b := &SyncBuffer{}
	LogOutput = b
	return b, func() {
		logMu.Lock()
		LogOutput = old
		logMu.Unlock()
	}
}

// SyncBuffer is a bytes.Buffer safe for the server goroutines that log into it.
type SyncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *SyncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *SyncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// WriteAtomic writes text to <path>.new and renames it over path, so a reader sees the old file
// or the new one, never half of it (specs/save-format.md §2). It does nothing when the file
// already holds these bytes, creates the directory, and says whether it wrote.
func WriteAtomic(path, text string) (bool, error) {
	data := []byte(text)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, err
	}
	return true, nil
}

// FileExists is true for a regular file (not a directory), as C#'s File.Exists.
func FileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// GameDataDir locates the optional local 3.8 gamedata directory for integration tests:
// TIDELAB_GAMEDATA env var, then tidelab.local.txt (gamedata= or lab= -> <lab>/gamedata/3.8)
// in an ancestor checkout, then <checkout>/../tidelab-data/gamedata/3.8.
func GameDataDir() string {
	if v := strings.TrimSpace(os.Getenv("TIDELAB_GAMEDATA")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TIDELAB_LAB")); v != "" {
		return filepath.Join(v, "gamedata", "3.8")
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for {
		if raw, err := os.ReadFile(filepath.Join(dir, "tidelab.local.txt")); err == nil {
			var gamedata, lab string
			for _, line := range strings.Split(strings.TrimPrefix(string(raw), "\xef\xbb\xbf"), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "gamedata":
					gamedata = strings.TrimSpace(v)
				case "lab":
					lab = strings.TrimSpace(v)
				}
			}
			if gamedata != "" {
				return gamedata
			}
			if lab != "" {
				return filepath.Join(lab, "gamedata", "3.8")
			}
		}
		if FileExists(filepath.Join(dir, "start-server.cmd")) {
			return filepath.Join(filepath.Dir(dir), "tidelab-data", "gamedata", "3.8")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
