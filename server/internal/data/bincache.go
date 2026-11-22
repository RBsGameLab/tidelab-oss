// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The binary cache encoding (our own design; byte for byte what the C#
// Tidelab.Data writes): a header, then the rows field by field. Numbers are fixed-width
// little-endian; strings, arrays and maps are length-prefixed (u32); an optional value has a
// presence byte. A string or a JSON array or object that occurs again is written once and
// afterwards referred to by number, and decodes to one shared copy. A JSON object is its
// shape (its keys in order, written once and then referred to by number) followed by its
// values only.
const (
	// CodecVersion: bump when the encoding changes.
	CodecVersion = 4
	HeaderLength = 4 + 4 + 8 + 8 + 4
	// SegmentRows is the rows per segment. After the header: the number of segments, then each
	// segment's row count and byte length (u32, u32), then the segments. Each segment shares
	// strings and JSON values only within itself, so segments decode in parallel.
	SegmentRows = 16384
	again       = math.MaxUint32
	jsonAgain   = 7
	jsonInt     = 8
)

var magic = []byte("TLC1")

// CacheHeader is what a cache file was written from; a file whose header does not match the
// row type's layout and the JSON file's signature is never read.
type CacheHeader struct {
	Layout, Source uint64
	Rows           uint32
}

// Writer writes the cache encoding.
type Writer struct {
	buf        []byte
	strings    map[string]uint32
	contentOf  map[Value]int // arrays and objects by identity
	contentIDs map[string]int
	written    map[int]uint32
	nextJSON   uint32
	shapes     map[string]uint32
}

func NewWriter() *Writer {
	return &Writer{buf: make([]byte, 0, 1<<16), strings: map[string]uint32{}, contentOf: map[Value]int{},
		contentIDs: map[string]int{}, written: map[int]uint32{}, shapes: map[string]uint32{}}
}

func (w *Writer) Bytes() []byte { return w.buf }

func (w *Writer) Header(h CacheHeader) {
	w.buf = append(w.buf, magic...)
	w.U32(CodecVersion)
	w.U64(h.Layout)
	w.U64(h.Source)
	w.U32(h.Rows)
}

func (w *Writer) U8(v byte) { w.buf = append(w.buf, v) }
func (w *Writer) Bool(v bool) {
	if v {
		w.U8(1)
	} else {
		w.U8(0)
	}
}
func (w *Writer) I32(v int32)   { w.buf = binary.LittleEndian.AppendUint32(w.buf, uint32(v)) }
func (w *Writer) U32(v uint32)  { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }
func (w *Writer) I64(v int64)   { w.buf = binary.LittleEndian.AppendUint64(w.buf, uint64(v)) }
func (w *Writer) U64(v uint64)  { w.buf = binary.LittleEndian.AppendUint64(w.buf, v) }
func (w *Writer) F32(v float32) { w.U32(math.Float32bits(v)) }
func (w *Writer) F64(v float64) { w.U64(math.Float64bits(v)) }

func (w *Writer) String(s string) {
	if s == "" {
		w.U32(0)
		return
	}
	if id, ok := w.strings[s]; ok {
		w.U32(again)
		w.U32(id)
		return
	}
	w.strings[s] = uint32(len(w.strings))
	w.U32(uint32(len(s)))
	w.buf = append(w.buf, s...)
}

func (w *Writer) JSON(v Value) {
	switch x := v.(type) {
	case String:
		w.U8(KindString)
		w.String(string(x))
		return
	case Number:
		f := float64(x)
		if f == math.Floor(f) && f >= math.MinInt32 && f <= math.MaxInt32 && !(f == 0 && math.Signbit(f)) {
			w.U8(jsonInt)
			w.I32(int32(f))
		} else {
			w.U8(KindNumber)
			w.F64(f)
		}
		return
	case *Array, *Object:
	default:
		w.U8(kindOf(v))
		return
	}
	content := w.contentID(v)
	if n, ok := w.written[content]; ok {
		w.U8(jsonAgain)
		w.U32(n)
		return
	}
	// Numbered before its parts, as the reader numbers it.
	w.written[content] = w.nextJSON
	w.nextJSON++
	w.U8(kindOf(v))
	if a, ok := v.(*Array); ok {
		w.U32(uint32(len(a.Items)))
		for _, it := range a.Items {
			w.JSON(it)
		}
		return
	}
	o := v.(*Object)
	w.shape(o.Keys)
	for _, it := range o.Values {
		w.JSON(it)
	}
}

// shape is an object's keys: the number of a shape written before, or again's marker, a new
// shape (count and keys) that takes the next number.
func (w *Writer) shape(keys []string) {
	key := strings.Join(keys, "\x00")
	if id, ok := w.shapes[key]; ok {
		w.U32(id)
		return
	}
	w.shapes[key] = uint32(len(w.shapes))
	w.U32(again)
	w.U32(uint32(len(keys)))
	for _, k := range keys {
		w.String(k)
	}
}

// contentID is a number per distinct content (hash-consing, bottom up).
func (w *Writer) contentID(v Value) int {
	_, composite := v.(*Array)
	if _, isObj := v.(*Object); isObj {
		composite = true
	}
	if composite {
		if id, ok := w.contentOf[v]; ok {
			return id
		}
	}
	var b strings.Builder
	switch x := v.(type) {
	case String:
		b.WriteByte('s')
		b.WriteString(string(x))
	case Number:
		b.WriteByte('n')
		b.WriteString(strconv.FormatInt(int64(math.Float64bits(float64(x))), 10))
	case *Array:
		b.WriteByte('a')
		for _, it := range x.Items {
			b.WriteByte(',')
			b.WriteString(strconv.Itoa(w.contentID(it)))
		}
	case *Object:
		b.WriteByte('o')
		for i, k := range x.Keys {
			b.WriteByte(',')
			b.WriteString(strings.ReplaceAll(strings.ReplaceAll(k, `\`, `\\`), ",", `\,`))
			b.WriteByte(',')
			b.WriteString(strconv.Itoa(w.contentID(x.Values[i])))
		}
	default:
		b.WriteByte('0' + kindOf(v))
	}
	k := b.String()
	id, ok := w.contentIDs[k]
	if !ok {
		id = len(w.contentIDs)
		w.contentIDs[k] = id
	}
	if composite {
		w.contentOf[v] = id
	}
	return id
}

// CorruptError is a cache that cannot be read: damage is an error, never garbage.
type CorruptError struct{ Msg string }

func (e *CorruptError) Error() string { return e.Msg }

func corrupt(msg string) { panic(&CorruptError{msg}) }

// Reader reads what Writer wrote.
type Reader struct {
	data        []byte
	pos, end    int
	strings     []string
	json        []Value
	shapes      [][]string
	jsonStrings []Value
}

// NewReader reads data[start:end] (one segment).
func NewReader(data []byte, start, end int) *Reader {
	return &Reader{data: data, pos: start, end: end}
}

func (r *Reader) AtEnd() bool   { return r.pos == r.end }
func (r *Reader) Position() int { return r.pos }

func (r *Reader) take(n int) []byte {
	if n < 0 || n > r.end-r.pos {
		corrupt("cache truncated")
	}
	s := r.data[r.pos : r.pos+n]
	r.pos += n
	return s
}

// Header is the header, false when this is not a cache file of this codec version.
func (r *Reader) Header() (CacheHeader, bool) {
	if r.end-r.pos < HeaderLength || string(r.take(4)) != string(magic) {
		return CacheHeader{}, false
	}
	if r.U32() != CodecVersion {
		return CacheHeader{}, false
	}
	return CacheHeader{r.U64(), r.U64(), r.U32()}, true
}

func (r *Reader) U8() byte { return r.take(1)[0] }
func (r *Reader) Bool() bool {
	switch r.U8() {
	case 0:
		return false
	case 1:
		return true
	}
	corrupt("bad bool")
	return false
}
func (r *Reader) I32() int32   { return int32(binary.LittleEndian.Uint32(r.take(4))) }
func (r *Reader) U32() uint32  { return binary.LittleEndian.Uint32(r.take(4)) }
func (r *Reader) I64() int64   { return int64(binary.LittleEndian.Uint64(r.take(8))) }
func (r *Reader) U64() uint64  { return binary.LittleEndian.Uint64(r.take(8)) }
func (r *Reader) F32() float32 { return math.Float32frombits(r.U32()) }
func (r *Reader) F64() float64 { return math.Float64frombits(r.U64()) }

// Count is a count of items that each take at least one byte: more than the bytes left is damage.
func (r *Reader) Count() int {
	n := r.U32()
	if uint64(n) > uint64(r.end-r.pos) {
		corrupt("count past the end")
	}
	return int(n)
}

func (r *Reader) String() string {
	id := r.stringID()
	if id < 0 {
		return ""
	}
	return r.strings[id]
}

// stringID is the number of the next string (-1 for the empty string), reading it if new.
func (r *Reader) stringID() int {
	n := r.U32()
	if n == 0 {
		return -1
	}
	if n == again {
		id := r.U32()
		if uint64(id) >= uint64(len(r.strings)) {
			corrupt("bad string number")
		}
		return int(id)
	}
	if uint64(n) > uint64(r.end-r.pos) {
		corrupt("cache truncated")
	}
	b := r.take(int(n))
	if !utf8.Valid(b) {
		corrupt("bad UTF-8")
	}
	r.strings = append(r.strings, string(b))
	return len(r.strings) - 1
}

func (r *Reader) jsonString() Value {
	id := r.stringID()
	if id < 0 {
		return String("")
	}
	for len(r.jsonStrings) <= id {
		r.jsonStrings = append(r.jsonStrings, nil)
	}
	if r.jsonStrings[id] == nil {
		r.jsonStrings[id] = String(r.strings[id])
	}
	return r.jsonStrings[id]
}

func (r *Reader) JSON() Value {
	tag := r.U8()
	switch tag {
	case KindNull:
		return Null{}
	case KindFalse:
		return Bool(false)
	case KindTrue:
		return Bool(true)
	case KindNumber:
		return Number(r.F64())
	case jsonInt:
		return Number(r.I32())
	case KindString:
		return r.jsonString()
	case jsonAgain:
		id := r.U32()
		if uint64(id) >= uint64(len(r.json)) || r.json[id] == nil {
			corrupt("bad JSON number")
		}
		return r.json[id]
	case KindArray:
		slot := len(r.json)
		r.json = append(r.json, nil)
		a := &Array{Items: make([]Value, r.Count())}
		for i := range a.Items {
			a.Items[i] = r.JSON()
		}
		r.json[slot] = a
		return a
	case KindObject:
		slot := len(r.json)
		r.json = append(r.json, nil)
		keys := r.shape()
		o := &Object{Keys: keys, Values: make([]Value, len(keys))}
		for i := range o.Values {
			o.Values[i] = r.JSON()
		}
		r.json[slot] = o
		return o
	}
	corrupt("bad JSON tag " + strconv.Itoa(int(tag)))
	return nil
}

// shape is an object's keys; objects of one shape share the slice (nothing writes to it).
func (r *Reader) shape() []string {
	id := r.U32()
	if id != again {
		if uint64(id) >= uint64(len(r.shapes)) {
			corrupt("bad shape number")
		}
		return r.shapes[id]
	}
	keys := make([]string, r.Count())
	for i := range keys {
		keys[i] = r.String()
	}
	r.shapes = append(r.shapes, keys)
	return keys
}
