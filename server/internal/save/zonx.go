// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package save holds a player's files under state/player/<id>/ (specs/save-format.md): the
// basic info, roles, weapons, echoes, items and formations (R-143g's PlayerData), and the scene,
// world-state and explore-tool files the world owns. Files written here keep the fields tidelab
// does not know. A file that does not parse panics with a *zon.Error, as the C# server threw:
// the gateway closes that session and the server goes on.
package save

import (
	"strconv"
	"strings"

	"tidelab/internal/zon"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func kindIs(v *zon.Value, k zon.Kind) bool { return v != nil && v.Kind() == k }

// field is the struct's field, nil when v is nil or not a struct.
func field(v *zon.Value, name string) *zon.Value {
	if v == nil {
		return nil
	}
	return v.Field(name)
}

// items of a list (or the empty .{}); a value of another kind panics.
func items(v *zon.Value) []*zon.Value { return must(v.Items()) }

func isListish(v *zon.Value) bool { return kindIs(v, zon.List) || kindIs(v, zon.Struct) }

// ----- reading fields of a known kind, else the fallback (the R-143g files) -----

func zInt(v *zon.Value, name string, fallback int32) int32 {
	if f := field(v, name); kindIs(f, zon.Number) {
		return must(f.AsInt32())
	}
	return fallback
}

func zUint(v *zon.Value, name string, fallback uint32) uint32 {
	f := field(v, name)
	if !kindIs(f, zon.Number) {
		return fallback
	}
	n := must(f.AsBig())
	if n.Sign() < 0 || !n.IsUint64() || n.Uint64() > 0xFFFFFFFF {
		return fallback
	}
	return uint32(n.Uint64())
}

func zLong(v *zon.Value, name string, fallback int64) int64 {
	if f := field(v, name); kindIs(f, zon.Number) {
		return must(f.AsInt64())
	}
	return fallback
}

func zNullableInt(v *zon.Value, name string) *int32 {
	if f := field(v, name); kindIs(f, zon.Number) {
		n := must(f.AsInt32())
		return &n
	}
	return nil
}

func zBool(v *zon.Value, name string, fallback bool) bool {
	if f := field(v, name); kindIs(f, zon.Bool) {
		return must(f.AsBool())
	}
	return fallback
}

func zString(v *zon.Value, name, fallback string) string {
	if f := field(v, name); kindIs(f, zon.String) {
		return must(f.AsString())
	}
	return fallback
}

func zNullableString(v *zon.Value, name string) *string {
	if f := field(v, name); kindIs(f, zon.String) {
		s := must(f.AsString())
		return &s
	}
	return nil
}

func zIntList(v *zon.Value, name string) []int32 {
	out := []int32{}
	if f := field(v, name); isListish(f) {
		for _, it := range items(f) {
			out = append(out, must(it.AsInt32()))
		}
	}
	return out
}

// IntPair is a two-element tuple .{ a, b }.
type IntPair struct{ Key, Value int32 }

func zIntPairList(v *zon.Value, name string) []IntPair {
	out := []IntPair{}
	if f := field(v, name); isListish(f) {
		for _, it := range items(f) {
			if p := items(it); len(p) >= 2 {
				out = append(out, IntPair{must(p[0].AsInt32()), must(p[1].AsInt32())})
			}
		}
	}
	return out
}

// captureExtras are the struct's fields tidelab does not know, in file order.
func captureExtras(v *zon.Value, known map[string]bool) []zon.Field {
	var out []zon.Field
	for _, f := range must(v.Fields()) {
		if !known[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

func withExtras(extras []zon.Field, fields ...zon.Field) *zon.Value {
	return zon.NewStruct(append(fields, extras...)...)
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// ----- the world's files: a missing or null field takes its default, a wrong kind panics -----

// Get is the field when present and not null.
func Get(s *zon.Value, name string) *zon.Value {
	if !kindIs(s, zon.Struct) {
		return nil
	}
	if f := s.Field(name); f != nil && f.Kind() != zon.Null {
		return f
	}
	return nil
}

func Int(s *zon.Value, name string, fallback int32) int32 {
	if f := Get(s, name); f != nil {
		return must(f.AsInt32())
	}
	return fallback
}

func Long(s *zon.Value, name string, fallback int64) int64 {
	if f := Get(s, name); f != nil {
		return must(f.AsInt64())
	}
	return fallback
}

func Float(s *zon.Value, name string, fallback float32) float32 {
	if f := Get(s, name); f != nil {
		return float32(must(f.AsFloat()))
	}
	return fallback
}

func Bool(s *zon.Value, name string, fallback bool) bool {
	if f := Get(s, name); f != nil {
		return must(f.AsBool())
	}
	return fallback
}

func Str(s *zon.Value, name, fallback string) string {
	if f := Get(s, name); f != nil {
		return must(f.AsString())
	}
	return fallback
}

func Ints(s *zon.Value, name string) []int32 {
	out := []int32{}
	if f := Get(s, name); f != nil {
		for _, it := range items(f) {
			out = append(out, must(it.AsInt32()))
		}
	}
	return out
}

func Floats(s *zon.Value, name string) []float32 {
	out := []float32{}
	if f := Get(s, name); f != nil {
		for _, it := range items(f) {
			out = append(out, float32(must(it.AsFloat())))
		}
	}
	return out
}

func List(s *zon.Value, name string) []*zon.Value {
	if f := Get(s, name); f != nil {
		return items(f)
	}
	return nil
}

// Pairs written as two-element tuples .{ a, b }.
func Pairs(s *zon.Value, name string) []IntPair {
	var out []IntPair
	for _, p := range List(s, name) {
		it := items(p)
		out = append(out, IntPair{must(it[0].AsInt32()), must(it[1].AsInt32())})
	}
	return out
}

func N(v int64) *zon.Value { return zon.Int(v) }

func NF(v float32) *zon.Value { return zon.Float32(v) }

func IntList(v []int32) *zon.Value {
	out := make([]*zon.Value, len(v))
	for i, x := range v {
		out[i] = zon.Int(int64(x))
	}
	return zon.NewList(out...)
}

func FloatList(v []float32) *zon.Value {
	out := make([]*zon.Value, len(v))
	for i, x := range v {
		out[i] = zon.Float32(x)
	}
	return zon.NewList(out...)
}

// Merge is original (or an empty struct) with every field set.
func Merge(original *zon.Value, fields ...zon.Field) *zon.Value {
	s := zon.NewStruct()
	if kindIs(original, zon.Struct) {
		s = original
	}
	for _, f := range fields {
		s = must(s.With(f.Name, f.Value))
	}
	return s
}

// ----- text files -----

// Lines splits on CR and LF, dropping empty lines.
func Lines(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' })
}

// Words splits on spaces only, dropping empty words.
func Words(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool { return r == ' ' })
}

// ParseSigned reads an optionally signed decimal integer and nothing else.
func ParseSigned(s string, bits int) (int64, bool) {
	if s == "" || s[0] == '+' && len(s) > 1 && (s[1] == '-' || s[1] == '+') {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, bits)
	return n, err == nil
}

// ParseDigits reads plain decimal digits (no sign).
func ParseDigits(s string, bits int) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, bits)
	return n, err == nil
}
