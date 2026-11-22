// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// GoCodec writes and reads values of one Go type in the cache encoding (R-151): structs field by
// field in declaration order, then their fields' kinds as the table rows' are (numbers fixed-width,
// strings and JSON values shared within one Writer). It is how data that is not a config table,
// such as the quest engine's, gets a cache of its own. Nil and empty differ: a slice or map is
// its length plus one (0 = nil), a pointer or a data.Value has a presence byte, so a value reads
// back reflect.DeepEqual to what was written. Map entries are written in key order.
type GoCodec struct {
	t      reflect.Type
	node   *goNode
	layout string
}

type goNode struct {
	enc func(w *Writer, v reflect.Value)
	dec func(r *Reader, v reflect.Value)
	// min is the fewest bytes one value takes (0 for an empty struct): a count of values that
	// take bytes cannot be more than the bytes left.
	min int
}

var goCodecs sync.Map // reflect.Type -> *GoCodec

// GoCodecOf is the codec of t (built once). Types it cannot write panic: unexported fields,
// channels, functions, interfaces other than data.Value, and types that contain themselves.
func GoCodecOf(t reflect.Type) *GoCodec {
	if c, ok := goCodecs.Load(t); ok {
		return c.(*GoCodec)
	}
	var layout strings.Builder
	node := buildGoNode(t, &layout, map[reflect.Type]bool{})
	c, _ := goCodecs.LoadOrStore(t, &GoCodec{t, node, layout.String()})
	return c.(*GoCodec)
}

// Layout is the type's shape as text (type and field names, kinds); LayoutHash its FNV-1a.
func (c *GoCodec) Layout() string     { return c.layout }
func (c *GoCodec) LayoutHash() uint64 { return Fnv([]byte(c.layout), fnvOffset) }

// Write writes *p (p is a pointer to the codec's type).
func (c *GoCodec) Write(w *Writer, p any) { c.node.enc(w, c.elem(p)) }

// Read reads into *p (panics with *CorruptError on damage).
func (c *GoCodec) Read(r *Reader, p any) { c.node.dec(r, c.elem(p)) }

func (c *GoCodec) elem(p any) reflect.Value {
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Pointer || v.Type().Elem() != c.t || v.IsNil() {
		panic(fmt.Sprintf("GoCodec of %s: want a non-nil *%s, got %T", c.t, c.t, p))
	}
	return v.Elem()
}

var valueType = reflect.TypeFor[Value]()

func buildGoNode(t reflect.Type, layout *strings.Builder, building map[reflect.Type]bool) *goNode {
	if building[t] {
		panic("GoCodec: " + t.String() + " contains itself")
	}
	building[t] = true
	defer delete(building, t)
	switch t.Kind() {
	case reflect.Bool:
		layout.WriteString("bool")
		return &goNode{func(w *Writer, v reflect.Value) { w.Bool(v.Bool()) },
			func(r *Reader, v reflect.Value) { v.SetBool(r.Bool()) }, 1}
	case reflect.Int8, reflect.Int16, reflect.Int32:
		layout.WriteString(t.Kind().String())
		return &goNode{func(w *Writer, v reflect.Value) { w.I32(int32(v.Int())) },
			func(r *Reader, v reflect.Value) { v.SetInt(int64(r.I32())) }, 4}
	case reflect.Int, reflect.Int64:
		layout.WriteString("int64")
		return &goNode{func(w *Writer, v reflect.Value) { w.I64(v.Int()) },
			func(r *Reader, v reflect.Value) { v.SetInt(r.I64()) }, 8}
	case reflect.Uint8:
		layout.WriteString("uint8")
		return &goNode{func(w *Writer, v reflect.Value) { w.U8(byte(v.Uint())) },
			func(r *Reader, v reflect.Value) { v.SetUint(uint64(r.U8())) }, 1}
	case reflect.Uint16, reflect.Uint32:
		layout.WriteString(t.Kind().String())
		return &goNode{func(w *Writer, v reflect.Value) { w.U32(uint32(v.Uint())) },
			func(r *Reader, v reflect.Value) { v.SetUint(uint64(r.U32())) }, 4}
	case reflect.Uint, reflect.Uint64:
		layout.WriteString("uint64")
		return &goNode{func(w *Writer, v reflect.Value) { w.U64(v.Uint()) },
			func(r *Reader, v reflect.Value) { v.SetUint(r.U64()) }, 8}
	case reflect.Float32:
		layout.WriteString("float32")
		return &goNode{func(w *Writer, v reflect.Value) { w.F32(float32(v.Float())) },
			func(r *Reader, v reflect.Value) { v.SetFloat(float64(r.F32())) }, 4}
	case reflect.Float64:
		layout.WriteString("float64")
		return &goNode{func(w *Writer, v reflect.Value) { w.F64(v.Float()) },
			func(r *Reader, v reflect.Value) { v.SetFloat(r.F64()) }, 8}
	case reflect.String:
		layout.WriteString("string")
		return &goNode{func(w *Writer, v reflect.Value) { w.String(v.String()) },
			func(r *Reader, v reflect.Value) { v.SetString(r.String()) }, 4}
	case reflect.Interface:
		if t != valueType {
			panic("GoCodec: interface " + t.String() + " (only data.Value)")
		}
		layout.WriteString("json")
		return &goNode{func(w *Writer, v reflect.Value) {
			w.Bool(!v.IsNil())
			if !v.IsNil() {
				w.JSON(v.Interface().(Value))
			}
		}, func(r *Reader, v reflect.Value) {
			if r.Bool() {
				v.Set(reflect.ValueOf(r.JSON()))
			} else {
				v.SetZero()
			}
		}, 1}
	case reflect.Pointer:
		layout.WriteString("*")
		elem := buildGoNode(t.Elem(), layout, building)
		et := t.Elem()
		return &goNode{func(w *Writer, v reflect.Value) {
			w.Bool(!v.IsNil())
			if !v.IsNil() {
				elem.enc(w, v.Elem())
			}
		}, func(r *Reader, v reflect.Value) {
			if !r.Bool() {
				v.SetZero()
				return
			}
			p := reflect.New(et)
			elem.dec(r, p.Elem())
			v.Set(p)
		}, 1}
	case reflect.Array:
		fmt.Fprintf(layout, "[%d]", t.Len())
		elem := buildGoNode(t.Elem(), layout, building)
		n := t.Len()
		return &goNode{func(w *Writer, v reflect.Value) {
			for i := 0; i < n; i++ {
				elem.enc(w, v.Index(i))
			}
		}, func(r *Reader, v reflect.Value) {
			for i := 0; i < n; i++ {
				elem.dec(r, v.Index(i))
			}
		}, n * elem.min}
	case reflect.Slice:
		layout.WriteString("[]")
		elem := buildGoNode(t.Elem(), layout, building)
		return &goNode{func(w *Writer, v reflect.Value) {
			if v.IsNil() {
				w.U32(0)
				return
			}
			w.U32(uint32(v.Len() + 1))
			for i := 0; i < v.Len(); i++ {
				elem.enc(w, v.Index(i))
			}
		}, func(r *Reader, v reflect.Value) {
			n, ok := count(r, elem.min)
			if !ok {
				v.SetZero()
				return
			}
			s := reflect.MakeSlice(t, n, n)
			for i := 0; i < n; i++ {
				elem.dec(r, s.Index(i))
			}
			v.Set(s)
		}, 4}
	case reflect.Map:
		layout.WriteString("map[")
		key := buildGoNode(t.Key(), layout, building)
		layout.WriteString("]")
		val := buildGoNode(t.Elem(), layout, building)
		kt, vt := t.Key(), t.Elem()
		return &goNode{func(w *Writer, v reflect.Value) {
			if v.IsNil() {
				w.U32(0)
				return
			}
			keys := v.MapKeys()
			slices.SortFunc(keys, compareKeys)
			w.U32(uint32(len(keys) + 1))
			for _, k := range keys {
				key.enc(w, k)
				val.enc(w, v.MapIndex(k))
			}
		}, func(r *Reader, v reflect.Value) {
			n, ok := count(r, key.min+val.min)
			if !ok {
				v.SetZero()
				return
			}
			m := reflect.MakeMapWithSize(t, n)
			k, e := reflect.New(kt).Elem(), reflect.New(vt).Elem()
			for i := 0; i < n; i++ {
				key.dec(r, k)
				val.dec(r, e)
				m.SetMapIndex(k, e)
			}
			if m.Len() != n {
				corrupt("a map key twice")
			}
			v.Set(m)
		}, 4}
	case reflect.Struct:
		layout.WriteString(t.Name())
		layout.WriteString("{")
		var fields []*goNode
		var index []int
		total := 0
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				panic("GoCodec: " + t.String() + "." + f.Name + " is not exported")
			}
			layout.WriteString(f.Name)
			layout.WriteString(":")
			n := buildGoNode(f.Type, layout, building)
			layout.WriteString(";")
			fields = append(fields, n)
			index = append(index, i)
			total += n.min
		}
		layout.WriteString("}")
		return &goNode{func(w *Writer, v reflect.Value) {
			for i, f := range fields {
				f.enc(w, v.Field(index[i]))
			}
		}, func(r *Reader, v reflect.Value) {
			for i, f := range fields {
				f.dec(r, v.Field(index[i]))
			}
		}, total}
	}
	panic("GoCodec: cannot write a " + t.String())
}

// count reads a length-plus-one: false for nil, else the length (damage when more values of at
// least min bytes than there are bytes left).
func count(r *Reader, min int) (int, bool) {
	n := r.U32()
	if n == 0 {
		return 0, false
	}
	n--
	if min > 0 && uint64(n)*uint64(min) > uint64(r.end-r.pos) {
		corrupt("count past the end")
	}
	if min == 0 && n > 1<<24 {
		corrupt("count too large")
	}
	return int(n), true
}

// compareKeys orders map keys: numbers, strings and bools by value, structs and arrays field by field.
func compareKeys(a, b reflect.Value) int {
	switch a.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(a.Int(), b.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cmp.Compare(a.Uint(), b.Uint())
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(a.Float(), b.Float())
	case reflect.String:
		return cmp.Compare(a.String(), b.String())
	case reflect.Bool:
		return cmp.Compare(boolInt(a.Bool()), boolInt(b.Bool()))
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if c := compareKeys(a.Field(i), b.Field(i)); c != 0 {
				return c
			}
		}
		return 0
	case reflect.Array:
		for i := 0; i < a.Len(); i++ {
			if c := compareKeys(a.Index(i), b.Index(i)); c != 0 {
				return c
			}
		}
		return 0
	}
	panic("GoCodec: cannot order map keys of kind " + a.Kind().String())
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GoDifferences are the places where two values of one type differ, at most max, as
// "path: a vs b" (what reflect.DeepEqual would call unequal, said where).
func GoDifferences(a, b any, max int) []string {
	var out []string
	goDiff(reflect.ValueOf(a), reflect.ValueOf(b), "", &out, max)
	return out
}

func goDiff(a, b reflect.Value, path string, out *[]string, max int) {
	if len(*out) >= max {
		return
	}
	say := func(format string, args ...any) { *out = append(*out, path+": "+fmt.Sprintf(format, args...)) }
	if a.IsValid() != b.IsValid() {
		say("one is missing")
		return
	}
	if !a.IsValid() {
		return
	}
	if a.Type() != b.Type() {
		say("%s vs %s", a.Type(), b.Type())
		return
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				say("nil %v vs nil %v", a.IsNil(), b.IsNil())
			}
			return
		}
		if a.Kind() == reflect.Interface && a.Type() == valueType {
			if !JSONEqual(a.Interface().(Value), b.Interface().(Value)) || !reflect.DeepEqual(a.Interface(), b.Interface()) {
				say("%s vs %s", trim(JSONText(a.Interface().(Value))), trim(JSONText(b.Interface().(Value))))
			}
			return
		}
		goDiff(a.Elem(), b.Elem(), path, out, max)
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			goDiff(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name, out, max)
		}
	case reflect.Slice, reflect.Array:
		if a.Kind() == reflect.Slice && a.IsNil() != b.IsNil() {
			say("nil %v vs nil %v", a.IsNil(), b.IsNil())
			return
		}
		if a.Len() != b.Len() {
			say("%d vs %d items", a.Len(), b.Len())
			return
		}
		for i := 0; i < a.Len(); i++ {
			goDiff(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), out, max)
		}
	case reflect.Map:
		if a.IsNil() != b.IsNil() {
			say("nil %v vs nil %v", a.IsNil(), b.IsNil())
			return
		}
		if a.Len() != b.Len() {
			say("%d vs %d entries", a.Len(), b.Len())
			return
		}
		keys := a.MapKeys()
		slices.SortFunc(keys, compareKeys)
		for _, k := range keys {
			bv := b.MapIndex(k)
			if !bv.IsValid() {
				say("key %v only in the first", k)
				continue
			}
			goDiff(a.MapIndex(k), bv, fmt.Sprintf("%s[%v]", path, k), out, max)
		}
	case reflect.Float32, reflect.Float64:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			say("%v vs %v", a.Float(), b.Float())
		}
	default:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			say("%v vs %v", a.Interface(), b.Interface())
		}
	}
}
