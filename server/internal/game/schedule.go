// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package game

import (
	"math"
	"os"
	"strconv"
	"strings"
)

// Schedule is R-52's real-length time windows: back-to-back cycles from 2024-05-20 04:00 UTC+8,
// a Monday. Ported from our C# Game/Activities.cs (Schedule).
const (
	ScheduleEpoch  int64 = 1716148800
	ScheduleDay    int64 = 24 * 60 * 60
	ActivityDays   int64 = 21
	DoubleDays     int64 = 7
	TowerDays      int64 = 28
	LordGymDays    int64 = 28
	MailDays       int64 = 30
	towerCycleDays int64 = 28
)

// Cycle is the cycle of days days that holds now: [begin, end).
func Cycle(now, days int64) (begin, end int64) {
	l := days * ScheduleDay
	k := int64(math.Floor(float64(now-ScheduleEpoch) / float64(l)))
	begin = ScheduleEpoch + k*l
	return begin, begin + l
}

// Today is the days since the Unix epoch (UTC).
func Today(nowSeconds int64) int64 {
	return int64(math.Floor(float64(nowSeconds) / float64(ScheduleDay)))
}

// readLineFile reads a small per-player state file of lines: each line's fields split on
// spaces (empty lines skipped). A missing file has none. (C# LineFile.Read.)
func readLineFile(path string) [][]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out [][]string
	for _, raw := range strings.Split(string(b), "\n") {
		f := splitSpaces(trimNet(raw))
		if len(f) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// trimNet is .NET's string.Trim(): white space at both ends.
func trimNet(s string) string {
	return strings.TrimFunc(s, isNetSpace)
}

// splitSpaces is .NET's Split(' ', RemoveEmptyEntries): only the space character separates.
func splitSpaces(s string) []string {
	var out []string
	for _, p := range strings.Split(s, " ") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isNetSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// lineInt is .NET int.TryParse(f[i], AllowLeadingSign, Invariant): an optional sign and digits.
func lineInt(f []string, i int) (int32, bool) {
	if i >= len(f) {
		return 0, false
	}
	v, ok := parseSigned(f[i], 32)
	return int32(v), ok
}

// lineLong is lineInt for long.
func lineLong(f []string, i int) (int64, bool) {
	if i >= len(f) {
		return 0, false
	}
	return parseSigned(f[i], 64)
}

func parseSigned(s string, bits int) (int64, bool) {
	if s == "" {
		return 0, false
	}
	digits := s
	if s[0] == '+' || s[0] == '-' {
		digits = s[1:]
	}
	if !allDigits(digits) {
		return 0, false
	}
	if s[0] == '+' {
		s = s[1:]
	}
	v, err := strconv.ParseInt(s, 10, bits)
	return v, err == nil
}

// parseUnsigned is .NET uint/ulong.TryParse(s, NumberStyles.None, Invariant): digits only.
func parseUnsigned(s string, bits int) (uint64, bool) {
	if !allDigits(s) {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, bits)
	return v, err == nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// f0 is .NET's ToString("F0") of a float: rounded half away from zero, no decimals.
func f0(x float32) string { return f0d(float64(x)) }

// f0d is f0 of a double.
func f0d(x float64) string { return strconv.FormatFloat(math.Round(x), 'f', 0, 64) }

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func itoa[T ~int32 | ~int64 | ~int | ~uint32 | ~uint64](v T) string {
	switch x := any(v).(type) {
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	}
	return strconv.FormatInt(int64(v), 10)
}
