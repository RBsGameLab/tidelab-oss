// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Row is a row of a config table (or a table nested in one). The generated types implement
// it with one method that hands every field to a visitor; reading JSON, writing and reading the
// cache, the layout hash and the comparison are all visitors.
type Row interface {
	TypeName() string
	Fields(v Visitor)
}

// RowPtr is *T for a row type T.
type RowPtr[T any] interface {
	*T
	Row
}

// Visitor visits a row's fields in order, each with its JSON name.
type Visitor interface {
	Visit(name string, f Field)
}

// Field is one field of one row: a pointer to its value and its codec.
type Field interface {
	shape(ctx *layoutCtx) string
	fromJSON(j Value, path string)
	setDefault()
	isJSON() bool
	write(w *Writer)
	read(r *Reader)
	value() any
	equalTo(other Field) bool
	show() string
}

// Codec is how one kind of field value is read from JSON, written to and read from the
// cache, compared, and described for the layout hash. Errors panic (*ShapeError for JSON,
// *CorruptError for the cache); the table loader recovers them.
type Codec[T any] interface {
	Shape(ctx *layoutCtx) string
	Default() T
	FromJSON(j Value, path string) T
	FromKey(key, path string) T
	Write(w *Writer, v T)
	Read(r *Reader) T
	Equal(a, b T) bool
	Show(v T) string
}

type field[T any] struct {
	p *T
	c Codec[T]
}

// F hands one field to the visitor (the generated Fields methods call it).
func F[T any](v Visitor, name string, p *T, c Codec[T]) {
	// R-147: reading a row from the cache visits every field of every row; wrapping each in a
	// *field made millions of short-lived objects among the rows that stay, and the heap could
	// not hand those half-used spans back.
	if rv, ok := v.(readVisitor); ok {
		*p = c.Read(rv.r)
		return
	}
	v.Visit(name, &field[T]{p, c})
}

func (f *field[T]) shape(ctx *layoutCtx) string   { return f.c.Shape(ctx) }
func (f *field[T]) fromJSON(j Value, path string) { *f.p = f.c.FromJSON(j, path) }
func (f *field[T]) setDefault()                   { *f.p = f.c.Default() }
func (f *field[T]) write(w *Writer)               { f.c.Write(w, *f.p) }
func (f *field[T]) read(r *Reader)                { *f.p = f.c.Read(r) }
func (f *field[T]) value() any                    { return *f.p }
func (f *field[T]) show() string                  { return f.c.Show(*f.p) }
func (f *field[T]) isJSON() bool {
	_, ok := any(f.c).(jsonCodec)
	return ok
}
func (f *field[T]) equalTo(o Field) bool { return f.c.Equal(*f.p, *o.(*field[T]).p) }

// ShapeError is JSON that does not have the shape the row type expects.
type ShapeError struct{ Msg string }

func (e *ShapeError) Error() string { return e.Msg }

func shapeErr(format string, a ...any) { panic(&ShapeError{fmt.Sprintf(format, a...)}) }

func trim(s string) string {
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

func wrong(path, want string, got Value) {
	shapeErr("%s: want %s, got %s", path, want, trim(JSONText(got)))
}

// number is a JSON number, or a string holding one (specs/data-tables.md §4: some tables write
// their ids as strings).
func number(j Value, path, want string) float64 {
	switch x := j.(type) {
	case Number:
		return float64(x)
	case String:
		if f, ok := parseDouble(string(x)); ok {
			return f
		}
	}
	wrong(path, want, j)
	return 0
}

// parseDouble is C#'s double.TryParse with NumberStyles.Float and the invariant culture:
// white space around, one leading sign, digits with a point and an exponent, or the words
// Infinity and NaN in any case.
func parseDouble(s string) (float64, bool) {
	t := strings.TrimSpace(s)
	body := strings.TrimLeft(t, "+-")
	if len(t)-len(body) > 1 || body == "" {
		return 0, false
	}
	if lb := strings.ToLower(body); lb == "infinity" || lb == "nan" {
		f, _ := strconv.ParseFloat(t, 64)
		return f, true
	}
	for i := 0; i < len(body); i++ {
		if c := body[i]; !(c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-') {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// parseIntKey is C#'s int.TryParse with AllowLeadingSign.
func parseIntKey(s string, bits int) (int64, bool) {
	if s == "" || s[0] == ' ' || s[len(s)-1] == ' ' {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, bits)
	return n, err == nil
}

// csharpR is a float's text as C#'s "R" format writes it (messages only).
func csharpR(f float64, bits int) string { return strconv.FormatFloat(f, 'g', -1, bits) }

type boolC struct{}

func (boolC) Shape(*layoutCtx) string { return "bool" }
func (boolC) Default() bool           { return false }
func (boolC) FromJSON(j Value, path string) bool {
	if b, ok := j.(Bool); ok {
		return bool(b)
	}
	wrong(path, "bool", j)
	return false
}
func (boolC) FromKey(k, path string) bool {
	switch k {
	case "true":
		return true
	case "false":
		return false
	}
	shapeErr("%s: key %s is not a bool", path, k)
	return false
}
func (boolC) Write(w *Writer, v bool) { w.Bool(v) }
func (boolC) Read(r *Reader) bool     { return r.Bool() }
func (boolC) Equal(a, b bool) bool    { return a == b }
func (boolC) Show(v bool) string      { return strconv.FormatBool(v) }

type intC struct{}

func (intC) Shape(*layoutCtx) string { return "i32" }
func (intC) Default() int32          { return 0 }
func (intC) FromJSON(j Value, path string) int32 {
	d := number(j, path, "i32")
	if d == math.Floor(d) && d >= math.MinInt32 && d <= math.MaxInt32 {
		return int32(d)
	}
	wrong(path, "i32", j)
	return 0
}
func (intC) FromKey(k, path string) int32 {
	n, ok := parseIntKey(k, 32)
	if !ok {
		shapeErr("%s: key %s is not an i32", path, k)
	}
	return int32(n)
}
func (intC) Write(w *Writer, v int32) { w.I32(v) }
func (intC) Read(r *Reader) int32     { return r.I32() }
func (intC) Equal(a, b int32) bool    { return a == b }
func (intC) Show(v int32) string      { return strconv.Itoa(int(v)) }

// longC is a 64-bit integer: a decimal string in the JSON (JavaScript reads it as BigInt).
type longC struct{}

func (longC) Shape(*layoutCtx) string { return "i64" }
func (longC) Default() int64          { return 0 }
func (c longC) FromJSON(j Value, path string) int64 {
	switch x := j.(type) {
	case String:
		return c.FromKey(string(x), path)
	case Number:
		f := float64(x)
		if f == math.Floor(f) && math.Abs(f) < 9007199254740992.0 {
			return int64(f)
		}
	}
	wrong(path, "i64", j)
	return 0
}
func (longC) FromKey(k, path string) int64 {
	n, ok := parseIntKey(k, 64)
	if !ok {
		shapeErr("%s: %s is not an i64", path, k)
	}
	return n
}
func (longC) Write(w *Writer, v int64) { w.I64(v) }
func (longC) Read(r *Reader) int64     { return r.I64() }
func (longC) Equal(a, b int64) bool    { return a == b }
func (longC) Show(v int64) string      { return strconv.FormatInt(v, 10) }

// floatC is a float32, which the JSON holds widened to a double (exactly).
type floatC struct{}

func (floatC) Shape(*layoutCtx) string { return "f32" }
func (floatC) Default() float32        { return 0 }
func (floatC) FromJSON(j Value, path string) float32 {
	return float32(number(j, path, "f32"))
}
func (floatC) FromKey(k, path string) float32 {
	f, ok := parseDouble(k)
	if !ok {
		shapeErr("%s: key %s is not an f32", path, k)
	}
	return float32(f)
}
func (floatC) Write(w *Writer, v float32) { w.F32(v) }
func (floatC) Read(r *Reader) float32     { return r.F32() }
func (floatC) Equal(a, b float32) bool    { return math.Float32bits(a) == math.Float32bits(b) }
func (floatC) Show(v float32) string      { return csharpR(float64(v), 64) }

// FloatAsDouble is the float computed from a float32 column read as a
// double: the shortest decimal that reads back as this float (0.081, not
// 0.08100000023841858; specs/data-tables.md §7.1).
func FloatAsDouble(v float32) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(v), 'g', -1, 32), 64)
	return f
}

type floatAsDoubleC struct{}

func (floatAsDoubleC) Shape(*layoutCtx) string { return "f32>f64" }
func (floatAsDoubleC) Default() float64        { return 0 }
func (floatAsDoubleC) FromJSON(j Value, path string) float64 {
	return FloatAsDouble(floatC{}.FromJSON(j, path))
}
func (floatAsDoubleC) FromKey(k, path string) float64 {
	return FloatAsDouble(floatC{}.FromKey(k, path))
}
func (floatAsDoubleC) Write(w *Writer, v float64) { w.F64(v) }
func (floatAsDoubleC) Read(r *Reader) float64     { return r.F64() }
func (floatAsDoubleC) Equal(a, b float64) bool    { return math.Float64bits(a) == math.Float64bits(b) }
func (floatAsDoubleC) Show(v float64) string      { return csharpR(v, 64) }

type doubleC struct{}

func (doubleC) Shape(*layoutCtx) string               { return "f64" }
func (doubleC) Default() float64                      { return 0 }
func (doubleC) FromJSON(j Value, path string) float64 { return number(j, path, "f64") }
func (doubleC) FromKey(k, path string) float64 {
	f, ok := parseDouble(k)
	if !ok {
		shapeErr("%s: key %s is not an f64", path, k)
	}
	return f
}
func (doubleC) Write(w *Writer, v float64) { w.F64(v) }
func (doubleC) Read(r *Reader) float64     { return r.F64() }
func (doubleC) Equal(a, b float64) bool    { return math.Float64bits(a) == math.Float64bits(b) }
func (doubleC) Show(v float64) string      { return csharpR(v, 64) }

type stringC struct{}

func (stringC) Shape(*layoutCtx) string { return "str" }
func (stringC) Default() string         { return "" }
func (stringC) FromJSON(j Value, path string) string {
	if s, ok := j.(String); ok {
		return string(s)
	}
	wrong(path, "string", j)
	return ""
}
func (stringC) FromKey(k, _ string) string { return k }
func (stringC) Write(w *Writer, v string)  { w.String(v) }
func (stringC) Read(r *Reader) string      { return r.String() }
func (stringC) Equal(a, b string) bool     { return a == b }
func (stringC) Show(v string) string       { return `"` + trim(v) + `"` }

// jsonCodec marks the codecs whose value is the JSON itself (null is a value, not the default).
type jsonCodec interface{ isJSONCodec() }

// jsonC is a string field whose text the exporter wrote out as JSON (or kept as a string).
type jsonC struct{}

func (jsonC) isJSONCodec()                     {}
func (jsonC) Shape(*layoutCtx) string          { return "json" }
func (jsonC) Default() Value                   { return Null{} }
func (jsonC) FromJSON(j Value, _ string) Value { return j }
func (jsonC) FromKey(k, _ string) Value        { return String(k) }
func (jsonC) Write(w *Writer, v Value)         { w.JSON(v) }
func (jsonC) Read(r *Reader) Value             { return r.JSON() }
func (jsonC) Equal(a, b Value) bool            { return JSONEqual(a, b) }
func (jsonC) Show(v Value) string              { return trim(JSONText(v)) }

// maskedJSONC is a JSON field of which only the paths the server reads are kept.
type maskedJSONC struct{ m *JSONMask }

func (maskedJSONC) isJSONCodec() {}
func (c maskedJSONC) Shape(*layoutCtx) string {
	return "json(" + strings.ReplaceAll(c.m.Text, "\n", ";") + ")"
}
func (maskedJSONC) Default() Value                     { return Null{} }
func (c maskedJSONC) FromJSON(j Value, _ string) Value { return c.m.Apply(j) }
func (maskedJSONC) FromKey(k, _ string) Value          { return String(k) }
func (maskedJSONC) Write(w *Writer, v Value)           { w.JSON(v) }
func (maskedJSONC) Read(r *Reader) Value               { return r.JSON() }
func (maskedJSONC) Equal(a, b Value) bool              { return JSONEqual(a, b) }
func (maskedJSONC) Show(v Value) string                { return trim(JSONText(v)) }

// The codecs the generated rows use.
var (
	BoolC          Codec[bool]    = boolC{}
	IntC           Codec[int32]   = intC{}
	LongC          Codec[int64]   = longC{}
	FloatC         Codec[float32] = floatC{}
	FloatAsDoubleC Codec[float64] = floatAsDoubleC{}
	DoubleC        Codec[float64] = doubleC{}
	StringC        Codec[string]  = stringC{}
	JSONC          Codec[Value]   = jsonC{}
)

// Masked keeps only the paths of m.
func Masked(m *JSONMask) Codec[Value] { return maskedJSONC{m} }

// tableC is a nested table; nil when the row does not have it.
type tableC[T any, PT RowPtr[T]] struct{}

// TableOf is the codec of a nested table of type T.
func TableOf[T any, PT RowPtr[T]]() Codec[PT] { return tableC[T, PT]{} }

func (tableC[T, PT]) Shape(ctx *layoutCtx) string {
	return "table(" + layoutText(PT(new(T)), ctx) + ")"
}
func (tableC[T, PT]) Default() PT { return nil }
func (tableC[T, PT]) FromJSON(j Value, path string) PT {
	switch x := j.(type) {
	case Null:
		return nil
	case *Object:
		return rowFromJSON[T, PT](x, path)
	}
	wrong(path, PT(new(T)).TypeName(), j)
	return nil
}
func (tableC[T, PT]) FromKey(_, path string) PT {
	shapeErr("%s: a table is not a map key", path)
	return nil
}
func (tableC[T, PT]) Write(w *Writer, v PT) {
	w.Bool(v != nil)
	if v != nil {
		WriteRow(w, v)
	}
}
func (tableC[T, PT]) Read(r *Reader) PT {
	if !r.Bool() {
		return nil
	}
	return ReadRow[T, PT](r)
}

// readMany reads len(out) nested rows into one block (R-147): an array of them, such as a
// LevelEntityConfig's Transform, is then one allocation instead of one per row. The rows are
// never changed after loading, so sharing a block is safe.
func (tableC[T, PT]) readMany(r *Reader, out []PT) {
	var block []T
	for i := range out {
		if !r.Bool() {
			continue
		}
		if block == nil {
			block = make([]T, len(out))
		}
		row := PT(&block[i])
		row.Fields(readVisitor{r})
		out[i] = row
	}
}
func (tableC[T, PT]) Equal(a, b PT) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return len(Differences(Row(a), Row(b), "", 1)) == 0
}
func (tableC[T, PT]) Show(v PT) string {
	if v == nil {
		return "null"
	}
	return v.TypeName() + "{...}"
}

type arrayC[T any] struct{ c Codec[T] }

// ArrayOf is the codec of an array of c's values.
func ArrayOf[T any](c Codec[T]) Codec[[]T] {
	// R-147: the generated Fields call this for every row; one codec per element codec, not a
	// new one (an allocation) per call.
	if got, ok := arrayCodecs.Load(c); ok {
		return got.(Codec[[]T])
	}
	got, _ := arrayCodecs.LoadOrStore(c, Codec[[]T](arrayC[T]{c}))
	return got.(Codec[[]T])
}

// arrayCodecs and mapCodecs hold the codecs ArrayOf and MapOf made, by element codec.
var arrayCodecs, mapCodecs sync.Map

func (a arrayC[T]) Shape(ctx *layoutCtx) string { return "[" + a.c.Shape(ctx) + "]" }
func (arrayC[T]) Default() []T                  { return []T{} }
func (a arrayC[T]) FromJSON(j Value, path string) []T {
	arr, ok := j.(*Array)
	if !ok {
		wrong(path, "array", j)
	}
	out := make([]T, len(arr.Items))
	for i, it := range arr.Items {
		out[i] = a.c.FromJSON(it, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}
func (arrayC[T]) FromKey(_, path string) []T {
	shapeErr("%s: an array is not a map key", path)
	return nil
}
func (a arrayC[T]) Write(w *Writer, v []T) {
	w.U32(uint32(len(v)))
	for _, x := range v {
		a.c.Write(w, x)
	}
}
func (a arrayC[T]) Read(r *Reader) []T {
	out := make([]T, r.Count())
	if many, ok := any(a.c).(interface{ readMany(*Reader, []T) }); ok {
		many.readMany(r, out)
		return out
	}
	for i := range out {
		out[i] = a.c.Read(r)
	}
	return out
}
func (a arrayC[T]) Equal(x, y []T) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if !a.c.Equal(x[i], y[i]) {
			return false
		}
	}
	return true
}
func (arrayC[T]) Show(v []T) string { return fmt.Sprintf("[%d items]", len(v)) }

// Map is a map field (the schema's vector of Key/Value tables), in the order of the data.
type Map[K comparable, V any] struct {
	Pairs  []Pair[K, V]
	once   sync.Once
	lookup map[K]V
}

type Pair[K comparable, V any] struct {
	Key   K
	Value V
}

func (m *Map[K, V]) Count() int {
	if m == nil {
		return 0
	}
	return len(m.Pairs)
}

// Get is the value for key (the last pair with it wins, as a dictionary built in order).
func (m *Map[K, V]) Get(key K) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}
	m.once.Do(func() {
		m.lookup = make(map[K]V, len(m.Pairs))
		for _, p := range m.Pairs {
			m.lookup[p.Key] = p.Value
		}
	})
	v, ok := m.lookup[key]
	return v, ok
}

type mapC[K comparable, V any] struct {
	k Codec[K]
	v Codec[V]
}

// MapOf is the codec of a map with keys k and values v.
func MapOf[K comparable, V any](k Codec[K], v Codec[V]) Codec[*Map[K, V]] {
	key := [2]any{k, v}
	if got, ok := mapCodecs.Load(key); ok {
		return got.(Codec[*Map[K, V]])
	}
	got, _ := mapCodecs.LoadOrStore(key, Codec[*Map[K, V]](mapC[K, V]{k, v}))
	return got.(Codec[*Map[K, V]])
}

func (c mapC[K, V]) Shape(ctx *layoutCtx) string {
	return "map(" + c.k.Shape(ctx) + "," + c.v.Shape(ctx) + ")"
}
func (mapC[K, V]) Default() *Map[K, V] { return &Map[K, V]{} }
func (c mapC[K, V]) FromJSON(j Value, path string) *Map[K, V] {
	o, ok := j.(*Object)
	if !ok {
		wrong(path, "object", j)
	}
	m := &Map[K, V]{Pairs: make([]Pair[K, V], len(o.Keys))}
	for i, k := range o.Keys {
		m.Pairs[i] = Pair[K, V]{c.k.FromKey(k, path), c.v.FromJSON(o.Values[i], path+"."+k)}
	}
	return m
}
func (mapC[K, V]) FromKey(_, path string) *Map[K, V] {
	shapeErr("%s: a map is not a map key", path)
	return nil
}
func (c mapC[K, V]) Write(w *Writer, m *Map[K, V]) {
	w.U32(uint32(m.Count()))
	for _, p := range m.Pairs {
		c.k.Write(w, p.Key)
		c.v.Write(w, p.Value)
	}
}
func (c mapC[K, V]) Read(r *Reader) *Map[K, V] {
	m := &Map[K, V]{Pairs: make([]Pair[K, V], r.Count())}
	for i := range m.Pairs {
		k := c.k.Read(r)
		m.Pairs[i] = Pair[K, V]{k, c.v.Read(r)}
	}
	return m
}
func (c mapC[K, V]) Equal(a, b *Map[K, V]) bool {
	if a.Count() != b.Count() {
		return false
	}
	for i := range a.Pairs {
		if !c.k.Equal(a.Pairs[i].Key, b.Pairs[i].Key) || !c.v.Equal(a.Pairs[i].Value, b.Pairs[i].Value) {
			return false
		}
	}
	return true
}
func (mapC[K, V]) Show(m *Map[K, V]) string { return fmt.Sprintf("{%d entries}", m.Count()) }

// ----- visitors -----

type layoutCtx struct{ building map[string]bool }

type layoutVisitor struct {
	ctx *layoutCtx
	out *strings.Builder
}

func (v layoutVisitor) Visit(name string, f Field) {
	v.out.WriteString(name)
	v.out.WriteByte(':')
	v.out.WriteString(f.shape(v.ctx))
	v.out.WriteByte(';')
}

var layouts sync.Map // type name -> layout text

func layoutText(r Row, ctx *layoutCtx) string {
	name := r.TypeName()
	if t, ok := layouts.Load(name); ok {
		return t.(string)
	}
	if ctx.building[name] {
		return name + "(recursive)" // a table that contains itself
	}
	ctx.building[name] = true
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	r.Fields(layoutVisitor{ctx, &b})
	b.WriteByte('}')
	delete(ctx.building, name)
	text := b.String()
	layouts.Store(name, text)
	return text
}

// LayoutText is the layout of a row type as text (field names and the shapes of their
// values). Nested tables are included by their own layout.
func LayoutText(r Row) string { return layoutText(r, &layoutCtx{building: map[string]bool{}}) }

// LayoutHash is the FNV-1a of LayoutText: a type that gains, loses or retypes a field gets a
// new hash, so its old cache is never read.
func LayoutHash(r Row) uint64 { return Fnv([]byte(LayoutText(r)), fnvOffset) }

const fnvOffset = 0xcbf29ce484222325

// Fnv is FNV-1a, 64 bits, continuing from h.
func Fnv(b []byte, h uint64) uint64 {
	for _, c := range b {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	return h
}

type fromJSONVisitor struct {
	o    *Object
	path string
	used int
	next int
}

func (v *fromJSONVisitor) Visit(name string, f Field) {
	// The JSON has the type's fields in the type's order, with others between them: look
	// ahead from the last match first.
	var j Value
	for k := v.next; k < len(v.o.Keys); k++ {
		if v.o.Keys[k] == name {
			j = v.o.Values[k]
			v.next = k + 1
			break
		}
	}
	if j == nil {
		j = v.o.Get(name)
	}
	if j == nil {
		f.setDefault()
		return
	}
	v.used++
	// null is the field's default (accepted in optional fields).
	if _, isNull := j.(Null); isNull && !f.isJSON() {
		f.setDefault()
		return
	}
	f.fromJSON(j, v.path+"."+name)
}

// IgnoresUnknown is implemented by row types whose JSON may have fields they do not: the types
// made from specs/data-tables.tsv list only what the server reads.
type IgnoresUnknown interface{ IgnoresUnknownFields() bool }

func rowFromJSON[T any, PT RowPtr[T]](o *Object, path string) PT {
	row := PT(new(T))
	v := &fromJSONVisitor{o: o, path: path}
	row.Fields(v)
	ignores := false
	if i, ok := any(row).(IgnoresUnknown); ok {
		ignores = i.IgnoresUnknownFields()
	}
	if v.used != len(o.Keys) && !ignores {
		names := FieldNames(row)
		var extra []string
		for _, k := range o.Keys {
			if !names[k] && len(extra) < 5 {
				extra = append(extra, k)
			}
		}
		shapeErr("%s: %d field(s) the row type %s does not have (%s)", path, len(o.Keys)-v.used, row.TypeName(), strings.Join(extra, ", "))
	}
	return row
}

// RowFromJSON reads a row from a JSON object; a *ShapeError when the JSON does not fit.
func RowFromJSON[T any, PT RowPtr[T]](o *Object, path string) (row PT, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*ShapeError); ok {
				err = e
				return
			}
			panic(r)
		}
	}()
	return rowFromJSON[T, PT](o, path), nil
}

type nameVisitor map[string]bool

func (v nameVisitor) Visit(name string, _ Field) { v[name] = true }

// FieldNames are the row type's field names.
func FieldNames(r Row) map[string]bool {
	v := nameVisitor{}
	r.Fields(v)
	return v
}

type writeVisitor struct{ w *Writer }

func (v writeVisitor) Visit(_ string, f Field) { f.write(v.w) }

// WriteRow writes a row's fields to the cache.
func WriteRow(w *Writer, r Row) { r.Fields(writeVisitor{w}) }

type readVisitor struct{ r *Reader }

func (v readVisitor) Visit(_ string, f Field) { f.read(v.r) }

// ReadRow reads a row from the cache (panics with *CorruptError on damage).
func ReadRow[T any, PT RowPtr[T]](r *Reader) PT {
	row := PT(new(T))
	row.Fields(readVisitor{r})
	return row
}

type namedField struct {
	name string
	f    Field
}

type collectVisitor struct{ fields *[]namedField }

func (v collectVisitor) Visit(name string, f Field) {
	*v.fields = append(*v.fields, namedField{name, f})
}

// Differences are the fields in which two rows differ, at most max, as "path.Field: a vs b";
// nested tables are compared field by field.
func Differences(a, b Row, path string, max int) []string {
	var fa, fb []namedField
	a.Fields(collectVisitor{&fa})
	b.Fields(collectVisitor{&fb})
	var diffs []string
	for i := 0; i < len(fa) && len(diffs) < max; i++ {
		if !fa[i].f.equalTo(fb[i].f) {
			diffs = append(diffs, fmt.Sprintf("%s.%s: %s vs %s", path, fa[i].name, fa[i].f.show(), fb[i].f.show()))
		}
	}
	return diffs
}
