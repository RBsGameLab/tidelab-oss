// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package zon

import (
	"math"
	"strings"
	"testing"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestReadsAnAccountFileWithFieldsInAnyOrder(t *testing.T) {
	v := must(Parse("// written by hand\n.{\n    .sex = 1,\n    .token = \"1\",\n    .user_name = \"3838\",\n    .user_id = \"3838\",\n}"))
	if s := must(v.Field("user_id").AsString()); s != "3838" {
		t.Fatal(s)
	}
	if n := must(v.Field("sex").AsInt32()); n != 1 {
		t.Fatal(n)
	}
	if v.Field("missing") != nil {
		t.Fatal("missing field found")
	}
	var names []string
	for _, f := range must(v.Fields()) {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "sex,token,user_name,user_id" {
		t.Fatal(names)
	}
}

func TestReadsIntegers(t *testing.T) {
	for text, want := range map[string]int64{"0": 0, "-42": -42, "1_000_000": 1_000_000, "0x1F": 31, "0o17": 15, "0b101": 5, "9223372036854775807": math.MaxInt64} {
		v := must(Parse(text))
		if !v.IsInteger() || must(v.AsInt64()) != want || must(v.NumberText()) != text {
			t.Fatalf("%s", text)
		}
	}
	if n := must(must(Parse("18446744073709551615")).AsUint64()); n != math.MaxUint64 {
		t.Fatal(n)
	}
}

func TestReadsFloats(t *testing.T) {
	for text, want := range map[string]float64{"-104338.234": -104338.234, "123.01772": 123.01772, "1.5e3": 1500, "2.5E-1": 0.25, "7": 7} {
		if f := must(must(Parse(text)).AsFloat()); f != want {
			t.Fatalf("%s: %v", text, f)
		}
	}
}

func TestRefusesBadNumbersAndFiles(t *testing.T) {
	for _, text := range []string{"1__0", "_1", "1_", "1.", "0x", "12ab", "--1",
		".{ .a = 1, .a = 2 }", ".{ .a = 1 .b = 2 }", ".{ .a = 1, 2 }", ".{ 1, .a = 2 }", "\"open", "\"bad \\q escape\"", ".{ .a = 1 } extra", "maybe", ""} {
		if _, err := Parse(text); err == nil {
			t.Fatalf("%q parsed", text)
		} else if _, ok := err.(*Error); !ok {
			t.Fatalf("%q: %T", text, err)
		}
	}
}

func TestReadsStringEscapes(t *testing.T) {
	v := must(Parse(`"a\"b\\c\nd\te\rf\x41\u{4E2D}\u{1F600}文"`))
	if s := must(v.AsString()); s != "a\"b\\c\nd\te\rfA中😀文" {
		t.Fatal(s)
	}
}

func TestReadsNestedValues(t *testing.T) {
	v := must(Parse(".{\n .list = .{ 1, 2, 3, },\n .empty = .{},\n .flag = true,\n .none = null,\n .kind = .main_quest,\n .@\"odd name\" = .{ .{ .x = 1 }, .{ .x = 2 } },\n}"))
	items := must(v.Field("list").Items())
	if len(items) != 3 || must(items[2].AsInt32()) != 3 {
		t.Fatal(items)
	}
	if len(must(v.Field("empty").Items())) != 0 || len(must(v.Field("empty").Fields())) != 0 {
		t.Fatal("empty")
	}
	if !must(v.Field("flag").AsBool()) || v.Field("none").Kind() != Null || must(v.Field("kind").AsEnum()) != "main_quest" {
		t.Fatal("values")
	}
	if n := must(must(v.Field("odd name").Items())[1].Field("x").AsInt32()); n != 2 {
		t.Fatal(n)
	}
}

func TestWrongKindIsAnError(t *testing.T) {
	v := must(Parse(`.{ .a = "x" }`))
	if _, err := v.Field("a").AsInt32(); err == nil {
		t.Fatal("string as int")
	}
	if _, err := v.Items(); err == nil {
		t.Fatal("struct as list")
	}
	if _, err := must(Parse("4294967296")).AsInt32(); err == nil {
		t.Fatal("too big for i32")
	}
}

func TestWritesFourSpaceIndentOneFieldPerLine(t *testing.T) {
	v := NewStruct(
		F("user_id", Str("3838")),
		F("sex", Int(0)),
		F("list", NewList(Int(1), Int(2))),
		F("nested", NewList(NewStruct(F("x", Float64(-1.5))))),
		F("empty", NewStruct()),
		F("none", NullValue),
		F("kind", EnumValue("main")),
		F("odd name", TrueValue))
	want := ".{\n    .user_id = \"3838\",\n    .sex = 0,\n    .list = .{ 1, 2 },\n    .nested = .{\n        .{\n            .x = -1.5,\n        },\n    },\n    .empty = .{},\n    .none = null,\n    .kind = .main,\n    .@\"odd name\" = true,\n}\n"
	if got := Write(v); got != want {
		t.Fatalf("%q", got)
	}
}

func TestWritesEscapesThatReadBack(t *testing.T) {
	text := "q\"b\\n\nt\tr\rc\u0001d\u007f中😀"
	written := Write(Str(text))
	if written != "\"q\\\"b\\\\n\\nt\\tr\\rc\\x01d\\x7f中😀\"\n" {
		t.Fatalf("%q", written)
	}
	if back := must(must(Parse(written)).AsString()); back != text {
		t.Fatalf("%q", back)
	}
}

func TestRewriteKeepsUnknownFieldsAndNumberText(t *testing.T) {
	text := ".{\n    .known = 1_000,\n    .later_field = .{ 0x10, -2.50 },\n}\n"
	v := must(Parse(text))
	if Write(v) != text {
		t.Fatalf("%q", Write(v))
	}
	changed := must(must(v.With("known", Int(5))).With("added", FalseValue))
	if got := Write(changed); got != ".{\n    .known = 5,\n    .later_field = .{ 0x10, -2.50 },\n    .added = false,\n}\n" {
		t.Fatalf("%q", got)
	}
}

func TestFloatsAreWrittenAsCSharpWritesThem(t *testing.T) {
	// double.ToString("R") / float.ToString("R") as .NET 10 printed them (2026-11-20).
	for f, want := range map[float64]string{0: "0", -1.5: "-1.5", -104338.234: "-104338.234", 1234567.5: "1234567.5", 1e15: "1000000000000000", 1e17: "1E+17", 1.234e21: "1.234E+21", 123456789012345: "123456789012345", 0.0001: "0.0001", 0.00001: "1E-05", 0.1: "0.1", 1e300: "1E+300", 2.5e-7: "2.5E-07"} {
		if got := must(Float64(f).NumberText()); got != want {
			t.Fatalf("%v: %s, want %s", f, got, want)
		}
	}
	for f, want := range map[float32]string{1.5: "1.5", 123.01772: "123.01772", 1234567: "1234567", 1e7: "10000000", 1e9: "1E+09", 1e8: "100000000", 0.1: "0.1", -104338.23: "-104338.23"} {
		if got := must(Float32(f).NumberText()); got != want {
			t.Fatalf("%v: %s, want %s", f, got, want)
		}
	}
}
