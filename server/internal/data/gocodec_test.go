// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

type goKey struct {
	Map    int32
	Entity int64
}

type goInner struct {
	Name string
	Pos  [3]float32
	Flag *bool
}

type goSample struct {
	B       bool
	I8      int8
	I32     int32
	I64     int64
	U8      byte
	U32     uint32
	F32     float32
	F64     float64
	S       string
	Raw     Value
	NoRaw   Value
	Ptr     *goInner
	NilPtr  *goInner
	Items   []goInner
	Empty   []int32
	Nil     []int32
	Nested  [][]int32
	ByID    map[int32][]string
	ByKey   map[goKey]struct{}
	ByName  map[string]goInner
	NilMap  map[string]int32
	EmptyMp map[string]int32
}

func sampleValue() goSample {
	yes := true
	raw, err := ParseJSON([]byte(`{"a":[1,2.5,-0,"x",null,true],"b":{"a":[1,2.5,-0,"x",null,true]}}`))
	if err != nil {
		panic(err)
	}
	return goSample{
		B: true, I8: -5, I32: math.MinInt32, I64: math.MaxInt64, U8: 200, U32: math.MaxUint32,
		F32: float32(math.Copysign(0, -1)), F64: 0.1, S: "中文 text", Raw: raw,
		Ptr:     &goInner{"p", [3]float32{1, 2, 3}, &yes},
		Items:   []goInner{{Name: "a"}, {Name: "a", Flag: &yes}},
		Empty:   []int32{},
		Nested:  [][]int32{nil, {}, {1, 2}},
		ByID:    map[int32][]string{3: {"x"}, -1: nil, 2: {}},
		ByKey:   map[goKey]struct{}{{8, 5}: {}, {8, 4}: {}, {7, 9}: {}},
		ByName:  map[string]goInner{"z": {Name: "z"}, "a": {}},
		EmptyMp: map[string]int32{},
	}
}

func roundTrip(t *testing.T, v goSample) (goSample, []byte) {
	t.Helper()
	c := GoCodecOf(reflect.TypeFor[goSample]())
	w := NewWriter()
	c.Write(w, &v)
	var got goSample
	r := NewReader(w.Bytes(), 0, len(w.Bytes()))
	c.Read(r, &got)
	if !r.AtEnd() {
		t.Fatal("bytes left over")
	}
	return got, w.Bytes()
}

// R-151: a value reads back as it was written, nil and empty kept apart, -0 and NaN-free floats
// bit for bit, and JSON values as JSON.
func TestAGoValueReadsBackDeepEqual(t *testing.T) {
	v := sampleValue()
	got, _ := roundTrip(t, v)
	if !reflect.DeepEqual(got, v) {
		t.Fatalf("differs: %v", GoDifferences(got, v, 5))
	}
	if got.Empty == nil || got.Nil != nil || got.Nested[0] != nil || got.Nested[1] == nil || got.ByID[-1] != nil || got.ByID[2] == nil {
		t.Fatal("nil and empty slices mixed up")
	}
	if got.NilMap != nil || got.EmptyMp == nil || got.NoRaw != nil || got.NilPtr != nil {
		t.Fatal("nil and empty maps, values or pointers mixed up")
	}
	if !math.Signbit(float64(got.F32)) {
		t.Fatal("-0 lost its sign")
	}
}

// Map entries are written in key order, so the same value always writes the same bytes.
func TestAGoMapWritesTheSameBytesEveryTime(t *testing.T) {
	_, first := roundTrip(t, sampleValue())
	for i := 0; i < 20; i++ {
		if _, again := roundTrip(t, sampleValue()); !bytes.Equal(first, again) {
			t.Fatal("map order leaked into the bytes")
		}
	}
}

// A changed type has another layout (the cache of the old one is not read); damage is a
// *CorruptError, never a wrong value.
func TestAGoTypeChangeAndDamageAreCaught(t *testing.T) {
	type a struct{ X, Y int32 }
	type b struct{ X, Z int32 }
	type c struct {
		X int32
		Y int64
	}
	ha, hb, hc := GoCodecOf(reflect.TypeFor[a]()).LayoutHash(), GoCodecOf(reflect.TypeFor[b]()).LayoutHash(), GoCodecOf(reflect.TypeFor[c]()).LayoutHash()
	if ha == hb || ha == hc {
		t.Fatal("layout does not see field names or kinds")
	}
	_, full := roundTrip(t, sampleValue())
	codec := GoCodecOf(reflect.TypeFor[goSample]())
	for cut := 0; cut < len(full); cut += 7 {
		func() {
			defer func() {
				r := recover()
				var ce *CorruptError
				if err, ok := r.(error); !ok || !errors.As(err, &ce) {
					t.Fatalf("cut at %d: %v", cut, r)
				}
			}()
			var got goSample
			codec.Read(NewReader(full[:cut], 0, cut), &got)
		}()
	}
}

func TestAGoTypeItCannotWritePanicsAtOnce(t *testing.T) {
	type hidden struct{ x int32 }
	type other struct{ F any }
	for _, typ := range []reflect.Type{reflect.TypeFor[hidden](), reflect.TypeFor[other](), reflect.TypeFor[chan int]()} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.HasPrefix(r.(string), "GoCodec") {
					t.Fatalf("%s: %v", typ, r)
				}
			}()
			GoCodecOf(typ)
		}()
	}
}

func TestGoDifferencesSaysWhere(t *testing.T) {
	a, b := sampleValue(), sampleValue()
	b.Items[1].Name = "b"
	b.Nested[1] = nil
	d := GoDifferences(a, b, 5)
	if len(d) != 2 || !strings.HasPrefix(d[0], ".Items[1].Name") || !strings.HasPrefix(d[1], ".Nested[1]") {
		t.Fatal(d)
	}
}
