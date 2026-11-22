// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package zon reads and writes the ZON subset of specs/save-format.md §2, with // comments.
// Values keep struct fields in file order, unknown ones included, and numbers as written, so a
// file can be read, changed in one field and written back (ported from Tidelab.Common.Zon).
package zon

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Kind int

const (
	// Struct is .{ .a = 1 }. The empty .{} is a struct with no fields and also reads as an empty list.
	Struct Kind = iota
	List
	// Number keeps its text as written.
	Number
	String
	Bool
	Null
	// Enum is an enum literal .name.
	Enum
)

func (k Kind) String() string {
	return [...]string{"struct", "list", "number", "string", "bool", "null", "enum"}[k]
}

type Field struct {
	Name  string
	Value *Value
}

// Value is an immutable ZON value.
type Value struct {
	kind   Kind
	fields []Field
	items  []*Value
	text   string
	flag   bool
}

// Error is a file that does not parse, or a value of the wrong kind.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

var (
	NullValue  = &Value{kind: Null}
	TrueValue  = &Value{kind: Bool, flag: true}
	FalseValue = &Value{kind: Bool}
)

func (v *Value) Kind() Kind { return v.kind }

// NewStruct makes a struct; a name given twice panics (a programming error).
func NewStruct(fields ...Field) *Value {
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Name] {
			panic("zon: field ." + f.Name + " given twice")
		}
		seen[f.Name] = true
	}
	return &Value{kind: Struct, fields: append([]Field(nil), fields...)}
}

// F is a struct field, for NewStruct.
func F(name string, v *Value) Field { return Field{name, v} }

func NewList(items ...*Value) *Value {
	return &Value{kind: List, items: append([]*Value(nil), items...)}
}
func Str(s string) *Value { return &Value{kind: String, text: s} }
func BoolValue(b bool) *Value {
	if b {
		return TrueValue
	}
	return FalseValue
}
func EnumValue(name string) *Value { return &Value{kind: Enum, text: name} }
func Int(n int64) *Value           { return &Value{kind: Number, text: strconv.FormatInt(n, 10)} }
func Uint(n uint64) *Value         { return &Value{kind: Number, text: strconv.FormatUint(n, 10)} }

// Float64 is written as C#'s double.ToString("R") writes it, so the C# and Go servers write
// the same files: shortest round-trip digits, exponent form below 1e-4 and from 1e17 (checked against .NET 10).
func Float64(f float64) *Value {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		panic("zon: ZON files here hold no inf or nan")
	}
	return &Value{kind: Number, text: csharpFloat(strconv.FormatFloat(f, 'e', -1, 64), 17)}
}

// Float32 is an f32 as the shortest decimal that reads back as the same float (C#'s
// float.ToString("R"): exponent form below 1e-4 and from 1e9).
func Float32(f float32) *Value {
	if math.IsInf(float64(f), 0) || math.IsNaN(float64(f)) {
		panic("zon: ZON files here hold no inf or nan")
	}
	return &Value{kind: Number, text: csharpFloat(strconv.FormatFloat(float64(f), 'e', -1, 32), 9)}
}

// csharpFloat turns Go's shortest "d.ddde±XX" into .NET's round-trip text.
func csharpFloat(e string, sciFrom int) string {
	neg := strings.HasPrefix(e, "-")
	e = strings.TrimPrefix(e, "-")
	mant, expText, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)
	sign := ""
	if neg {
		sign = "-"
	}
	if digits == "0" {
		return sign + "0"
	}
	if exp >= sciFrom || exp < -4 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		es := "+"
		if exp < 0 {
			es, exp = "-", -exp
		}
		return fmt.Sprintf("%s%sE%s%02d", sign, m, es, exp)
	}
	if exp < 0 {
		return sign + "0." + strings.Repeat("0", -exp-1) + digits
	}
	if len(digits) <= exp+1 {
		return sign + digits + strings.Repeat("0", exp+1-len(digits))
	}
	return sign + digits[:exp+1] + "." + digits[exp+1:]
}

func mismatch(v *Value, wanted string) error {
	found := v.kind.String()
	if v.kind == Number || v.kind == Enum {
		found += " " + v.text
	}
	return &Error{fmt.Sprintf("expected %s, found %s", wanted, found)}
}

// Fields are the struct's fields in order; an empty list has none.
func (v *Value) Fields() ([]Field, error) {
	switch {
	case v.kind == Struct:
		return v.fields, nil
	case v.kind == List && len(v.items) == 0:
		return nil, nil
	}
	return nil, mismatch(v, "a struct")
}

// Items are the list's items; the empty struct .{} reads as an empty list.
func (v *Value) Items() ([]*Value, error) {
	switch {
	case v.kind == List:
		return v.items, nil
	case v.kind == Struct && len(v.fields) == 0:
		return nil, nil
	}
	return nil, mismatch(v, "a list")
}

// Field is the field's value, nil when the struct has no such field (or v is not a struct).
func (v *Value) Field(name string) *Value {
	if v.kind != Struct {
		return nil
	}
	for _, f := range v.fields {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}

// With is this struct with name set: replaced in place, or added at the end.
func (v *Value) With(name string, value *Value) (*Value, error) {
	fields, err := v.Fields()
	if err != nil {
		return nil, err
	}
	out := append([]Field(nil), fields...)
	for i := range out {
		if out[i].Name == name {
			out[i].Value = value
			return &Value{kind: Struct, fields: out}, nil
		}
	}
	return &Value{kind: Struct, fields: append(out, Field{name, value})}, nil
}

func (v *Value) AsString() (string, error) {
	if v.kind != String {
		return "", mismatch(v, "a string")
	}
	return v.text, nil
}

func (v *Value) AsEnum() (string, error) {
	if v.kind != Enum {
		return "", mismatch(v, "an enum literal")
	}
	return v.text, nil
}

func (v *Value) AsBool() (bool, error) {
	if v.kind != Bool {
		return false, mismatch(v, "a bool")
	}
	return v.flag, nil
}

// NumberText is the number as written in the file (underscores and prefixes included).
func (v *Value) NumberText() (string, error) {
	if v.kind != Number {
		return "", mismatch(v, "a number")
	}
	return v.text, nil
}

func (v *Value) IsInteger() bool {
	_, ok := parseInteger(v.text)
	return v.kind == Number && ok
}

// AsBig is the integer at any size.
func (v *Value) AsBig() (*big.Int, error) {
	if v.kind == Number {
		if n, ok := parseInteger(v.text); ok {
			return n, nil
		}
	}
	return nil, mismatch(v, "an integer")
}

func (v *Value) AsInt64() (int64, error) {
	n, err := v.AsBig()
	if err != nil {
		return 0, err
	}
	if !n.IsInt64() {
		return 0, mismatch(v, "a 64-bit integer")
	}
	return n.Int64(), nil
}

func (v *Value) AsUint64() (uint64, error) {
	n, err := v.AsBig()
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() {
		return 0, mismatch(v, "an unsigned 64-bit integer")
	}
	return n.Uint64(), nil
}

func (v *Value) AsInt32() (int32, error) {
	n, err := v.AsInt64()
	if err != nil || n < math.MinInt32 || n > math.MaxInt32 {
		return 0, mismatch(v, "a 32-bit integer")
	}
	return int32(n), nil
}

// AsFloat is the number as a float; an integer reads as a float too.
func (v *Value) AsFloat() (float64, error) {
	if v.kind == Number {
		if f, ok := parseFloat(v.text); ok {
			return f, nil
		}
	}
	return 0, mismatch(v, "a number")
}

func (v *Value) String() string { return Write(v) }

// ----- numbers -----

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// digits checks the digits and drops the underscores, which may only sit between digits.
func digits(text string, radix int) (string, bool) {
	if text == "" || text[0] == '_' || text[len(text)-1] == '_' || strings.Contains(text, "__") {
		return "", false
	}
	for i := 0; i < len(text); i++ {
		if c := text[i]; c != '_' {
			if d := hexDigit(c); d < 0 || d >= radix {
				return "", false
			}
		}
	}
	return strings.ReplaceAll(text, "_", ""), true
}

// maxInteger bounds integers the way C#'s Int128 does.
var maxInteger = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))

func parseInteger(text string) (*big.Int, bool) {
	neg := strings.HasPrefix(text, "-")
	body := strings.TrimPrefix(text, "-")
	radix := 10
	if len(body) > 2 && body[0] == '0' && (body[1] == 'x' || body[1] == 'o' || body[1] == 'b') {
		radix = map[byte]int{'x': 16, 'o': 8, 'b': 2}[body[1]]
		body = body[2:]
	}
	d, ok := digits(body, radix)
	if !ok {
		return nil, false
	}
	n, ok := new(big.Int).SetString(d, radix)
	if !ok || n.Cmp(maxInteger) > 0 {
		return nil, false
	}
	if neg {
		n.Neg(n)
	}
	return n, true
}

func parseFloat(text string) (float64, bool) {
	if n, ok := parseInteger(text); ok {
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	}
	neg := strings.HasPrefix(text, "-")
	body := strings.TrimPrefix(text, "-")
	mantissa, exponent, hasExp := body, "", false
	if e := strings.IndexAny(body, "eE"); e >= 0 {
		mantissa, exponent, hasExp = body[:e], body[e+1:], true
	}
	dot := strings.IndexByte(mantissa, '.')
	if dot <= 0 || dot == len(mantissa)-1 {
		return 0, false
	}
	whole, ok1 := digits(mantissa[:dot], 10)
	frac, ok2 := digits(mantissa[dot+1:], 10)
	if !ok1 || !ok2 {
		return 0, false
	}
	plain := whole + "." + frac
	if hasExp {
		sign := ""
		if strings.HasPrefix(exponent, "-") || strings.HasPrefix(exponent, "+") {
			sign, exponent = exponent[:1], exponent[1:]
		}
		ed, ok := digits(exponent, 10)
		if !ok {
			return 0, false
		}
		plain += "e" + sign + ed
	}
	f, err := strconv.ParseFloat(plain, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		f = -f
	}
	return f, true
}

// ----- reading -----

// ParseFile parses a UTF-8 ZON file.
func ParseFile(path string) (*Value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(b))
}

// Parse parses one ZON value.
func Parse(text string) (v *Value, err error) {
	p := &parser{text: text}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok {
				v, err = nil, e
				return
			}
			panic(r)
		}
	}()
	v = p.value()
	p.skipSpace()
	if !p.atEnd() {
		panic(p.fail("text after the value"))
	}
	return v, nil
}

type parser struct {
	text string
	at   int
}

func (p *parser) atEnd() bool { return p.at >= len(p.text) }

func (p *parser) peek() byte {
	if p.atEnd() {
		return 0
	}
	return p.text[p.at]
}

func (p *parser) fail(wanted string) *Error {
	line, col := 1, 1
	for i := 0; i < p.at && i < len(p.text); {
		r, n := utf8.DecodeRuneInString(p.text[i:])
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
			if r >= 0x10000 {
				col++ // C# counts UTF-16 units
			}
		}
		i += n
	}
	return &Error{fmt.Sprintf("%d:%d: expected %s", line, col, wanted)}
}

func (p *parser) expect(c byte) {
	if p.peek() != c {
		panic(p.fail("'" + string(c) + "'"))
	}
	p.at++
}

func (p *parser) skipSpace() {
	for !p.atEnd() {
		switch {
		case p.text[p.at] == ' ' || p.text[p.at] == '\t' || p.text[p.at] == '\r' || p.text[p.at] == '\n':
			p.at++
		case strings.HasPrefix(p.text[p.at:], "\uFEFF"):
			p.at += 3
		case strings.HasPrefix(p.text[p.at:], "//"):
			for !p.atEnd() && p.text[p.at] != '\n' {
				p.at++
			}
		default:
			return
		}
	}
}

func isDigit(c byte) bool         { return c >= '0' && c <= '9' }
func isLetterOrDigit(c byte) bool { return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'z') }

func (p *parser) value() *Value {
	p.skipSpace()
	if p.atEnd() {
		panic(p.fail("a value"))
	}
	c := p.text[p.at]
	switch {
	case c == '.':
		p.at++
		if p.peek() == '{' {
			p.at++
			return p.aggregate()
		}
		return EnumValue(p.identifier())
	case c == '"':
		return Str(p.stringLiteral())
	case c == '-' || isDigit(c):
		return p.number()
	}
	switch w := p.word(); w {
	case "true":
		return TrueValue
	case "false":
		return FalseValue
	case "null":
		return NullValue
	case "":
		panic(p.fail("a value"))
	default:
		panic(p.fail("a value, not '" + w + "'"))
	}
}

// aggregate reads after .{: a struct if the first item is .name =, else a list.
func (p *parser) aggregate() *Value {
	p.skipSpace()
	if p.peek() == '}' {
		p.at++
		return &Value{kind: Struct}
	}
	if p.startsField() {
		var fields []Field
		names := map[string]bool{}
		for {
			p.skipSpace()
			if !p.startsField() {
				panic(p.fail("a field .name = value"))
			}
			p.at++
			name := p.identifier()
			p.skipSpace()
			p.expect('=')
			if names[name] {
				panic(p.fail("field ." + name + " only once"))
			}
			names[name] = true
			fields = append(fields, Field{name, p.value()})
			if !p.separator() {
				break
			}
		}
		return &Value{kind: Struct, fields: fields}
	}
	var items []*Value
	for {
		items = append(items, p.value())
		if !p.separator() {
			break
		}
	}
	return &Value{kind: List, items: items}
}

// separator reads a comma (then true, unless the closing brace follows) or the closing brace.
func (p *parser) separator() bool {
	p.skipSpace()
	if p.peek() == ',' {
		p.at++
		p.skipSpace()
		if p.peek() != '}' {
			return true
		}
	}
	p.expect('}')
	return false
}

func (p *parser) startsField() (ok bool) {
	if p.peek() != '.' {
		return false
	}
	save := p.at
	defer func() {
		if r := recover(); r != nil {
			if _, isErr := r.(*Error); !isErr {
				panic(r)
			}
			ok = false
		}
		p.at = save
	}()
	p.at++
	if p.peek() == '{' {
		return false
	}
	p.identifier()
	p.skipSpace()
	return p.peek() == '='
}

func (p *parser) identifier() string {
	if p.peek() == '@' {
		p.at++
		if p.peek() != '"' {
			panic(p.fail("a quoted name after @"))
		}
		return p.stringLiteral()
	}
	w := p.word()
	if w == "" || isDigit(w[0]) {
		panic(p.fail("a name"))
	}
	return w
}

func (p *parser) word() string {
	start := p.at
	for !p.atEnd() && (isLetterOrDigit(p.text[p.at]) || p.text[p.at] == '_') {
		p.at++
	}
	return p.text[start:p.at]
}

func (p *parser) isHex(start int) bool {
	body := start
	if p.text[start] == '-' {
		body++
	}
	return body+1 < len(p.text) && p.text[body] == '0' && p.text[body+1] == 'x'
}

func (p *parser) number() *Value {
	start := p.at
	if p.peek() == '-' {
		p.at++
	}
	for !p.atEnd() {
		c := p.text[p.at]
		expSign := (c == '-' || c == '+') && p.at > start && (p.text[p.at-1] == 'e' || p.text[p.at-1] == 'E') && !p.isHex(start)
		if isLetterOrDigit(c) || c == '_' || c == '.' || expSign {
			p.at++
		} else {
			break
		}
	}
	lit := p.text[start:p.at]
	if _, ok := parseInteger(lit); !ok {
		if _, ok := parseFloat(lit); !ok {
			p.at = start
			panic(p.fail("a number, not '" + lit + "'"))
		}
	}
	return &Value{kind: Number, text: lit}
}

func (p *parser) hexNumber(min, max int) int {
	value, n := 0, 0
	for n < max && !p.atEnd() && hexDigit(p.text[p.at]) >= 0 {
		value = value*16 + hexDigit(p.text[p.at])
		p.at++
		n++
	}
	if n < min {
		panic(p.fail("hex digits"))
	}
	return value
}

func (p *parser) stringLiteral() string {
	p.expect('"')
	var b []byte
	for {
		if p.atEnd() || p.text[p.at] == '\n' {
			panic(p.fail("the closing quote"))
		}
		c := p.text[p.at]
		p.at++
		if c == '"' {
			break
		}
		if c != '\\' {
			b = append(b, c)
			continue
		}
		if p.atEnd() {
			panic(p.fail("an escape"))
		}
		e := p.text[p.at]
		p.at++
		switch e {
		case 'n':
			b = append(b, '\n')
		case 't':
			b = append(b, '\t')
		case 'r':
			b = append(b, '\r')
		case '"', '\'', '\\':
			b = append(b, e)
		case 'x':
			b = append(b, byte(p.hexNumber(2, 2)))
		case 'u':
			p.expect('{')
			code := p.hexNumber(1, 6)
			p.expect('}')
			if !utf8.ValidRune(rune(code)) {
				panic(p.fail("a Unicode scalar value"))
			}
			b = utf8.AppendRune(b, rune(code))
		default:
			panic(p.fail("a known escape, not \\" + string(e)))
		}
	}
	// C# decodes the bytes as UTF-8, replacing what is not.
	return strings.ToValidUTF8(string(b), "�")
}

// ----- writing -----

// Write is v's text with 4-space indentation and one field per line, ending in a line feed; a
// list of plain values stays on one line. Any ZON reader can read it.
func Write(v *Value) string {
	var b strings.Builder
	appendValue(&b, v, 0)
	b.WriteByte('\n')
	return b.String()
}

func indent(b *strings.Builder, depth int) { b.WriteString(strings.Repeat(" ", depth*4)) }

func isPlain(v *Value) bool {
	return (v.kind != Struct && v.kind != List) || (v.kind == Struct && len(v.fields) == 0)
}

func appendValue(b *strings.Builder, v *Value, depth int) {
	switch v.kind {
	case Struct:
		if len(v.fields) == 0 {
			b.WriteString(".{}")
			return
		}
		b.WriteString(".{\n")
		for _, f := range v.fields {
			indent(b, depth+1)
			b.WriteByte('.')
			appendName(b, f.Name)
			b.WriteString(" = ")
			appendValue(b, f.Value, depth+1)
			b.WriteString(",\n")
		}
		indent(b, depth)
		b.WriteByte('}')
	case List:
		if len(v.items) == 0 {
			b.WriteString(".{}")
			return
		}
		plain := true
		for _, it := range v.items {
			plain = plain && isPlain(it)
		}
		if plain {
			b.WriteString(".{ ")
			for i, it := range v.items {
				if i > 0 {
					b.WriteString(", ")
				}
				appendValue(b, it, depth)
			}
			b.WriteString(" }")
			return
		}
		b.WriteString(".{\n")
		for _, it := range v.items {
			indent(b, depth+1)
			appendValue(b, it, depth+1)
			b.WriteString(",\n")
		}
		indent(b, depth)
		b.WriteByte('}')
	case Number:
		b.WriteString(v.text)
	case String:
		appendString(b, v.text)
	case Bool:
		if v.flag {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Null:
		b.WriteString("null")
	case Enum:
		b.WriteByte('.')
		appendName(b, v.text)
	}
}

func appendName(b *strings.Builder, name string) {
	plain := name != "" && !isDigit(name[0])
	for i := 0; plain && i < len(name); i++ {
		plain = isLetterOrDigit(name[i]) || name[i] == '_'
	}
	if plain {
		b.WriteString(name)
		return
	}
	b.WriteByte('@')
	appendString(b, name)
}

func appendString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
