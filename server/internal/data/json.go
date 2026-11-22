// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// Package data is the game's config tables (R-143e, specs/data-tables.md): row types
// generated from the client's schema, read from the exported JSON or from tidelab's binary
// cache (the same files the C# Tidelab.Data writes). Ported from our C# Tidelab.Data.
package data

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Value is a JSON value that never changes after it is built, so equal subtrees can be one
// shared object (the binary cache decodes repeated ones to a single copy). Numbers are
// float64, as JavaScript wrote them. The kinds: Null, Bool, Number, String, *Array, *Object.
type Value interface{ isValue() }

type Null struct{}
type Bool bool
type Number float64
type String string

type Array struct{ Items []Value }

// Object keeps its keys in the order of the text.
type Object struct {
	Keys   []string
	Values []Value
}

func (Null) isValue()    {}
func (Bool) isValue()    {}
func (Number) isValue()  {}
func (String) isValue()  {}
func (*Array) isValue()  {}
func (*Object) isValue() {}

// Get is the value of key, nil when the object has none.
func (o *Object) Get(key string) Value {
	for i, k := range o.Keys {
		if k == key {
			return o.Values[i]
		}
	}
	return nil
}

// Kind numbers, as the cache writes them.
const (
	KindNull   byte = 0
	KindFalse  byte = 1
	KindTrue   byte = 2
	KindNumber byte = 3
	KindString byte = 4
	KindArray  byte = 5
	KindObject byte = 6
)

func kindOf(v Value) byte {
	switch x := v.(type) {
	case Bool:
		if x {
			return KindTrue
		}
		return KindFalse
	case Number:
		return KindNumber
	case String:
		return KindString
	case *Array:
		return KindArray
	case *Object:
		return KindObject
	}
	return KindNull
}

// JSONEqual is structural equality: objects by their entries in order, numbers by their bits.
func JSONEqual(a, b Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if kindOf(a) != kindOf(b) {
		return false
	}
	switch x := a.(type) {
	case String:
		return x == b.(String)
	case Number:
		return math.Float64bits(float64(x)) == math.Float64bits(float64(b.(Number)))
	case Bool:
		return x == b.(Bool)
	case *Array:
		y := b.(*Array)
		if x == y {
			return true
		}
		if len(x.Items) != len(y.Items) {
			return false
		}
		for i := range x.Items {
			if !JSONEqual(x.Items[i], y.Items[i]) {
				return false
			}
		}
		return true
	case *Object:
		y := b.(*Object)
		if x == y {
			return true
		}
		if len(x.Keys) != len(y.Keys) {
			return false
		}
		for i := range x.Keys {
			if x.Keys[i] != y.Keys[i] || !JSONEqual(x.Values[i], y.Values[i]) {
				return false
			}
		}
		return true
	}
	return true
}

// JSONText is compact JSON text, for messages.
func JSONText(v Value) string {
	var b strings.Builder
	appendJSON(&b, v)
	return b.String()
}

func quote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

func appendJSON(b *strings.Builder, v Value) {
	switch x := v.(type) {
	case String:
		b.WriteString(quote(string(x)))
	case Number:
		b.WriteString(strconv.FormatFloat(float64(x), 'g', -1, 64))
	case Bool:
		b.WriteString(strconv.FormatBool(bool(x)))
	case *Array:
		b.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			appendJSON(b, it)
		}
		b.WriteByte(']')
	case *Object:
		b.WriteByte('{')
		for i, k := range x.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(quote(k))
			b.WriteByte(':')
			appendJSON(b, x.Values[i])
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// SyntaxError is text that is not JSON.
type SyntaxError struct{ Msg string }

func (e *SyntaxError) Error() string { return e.Msg }

const maxDepth = 512

// jsonParser reads JSON (RFC 8259, as System.Text.Json's reader does) from bytes.
//
// R-147: a parse shares what repeats (Values never change once built): one String value per
// short text, one Number per number, one key list per set of keys. Arrays and objects are
// built on two stacks and copied once at their final size. The quest data (25 MB of JSON)
// otherwise left about 2 million short-lived objects among the ones it keeps.
type jsonParser struct {
	b     []byte
	at    int
	depth int
	// base is the file offset of b[0], for messages (R-150: a table is parsed a row at a time).
	base int64

	texts  map[string]string
	strs   map[string]Value
	nums   map[uint64]Value
	shapes map[string][]string
	vstack []Value
	kstack []string
	idbuf  []byte
}

// maxShared is the longest text a parse shares; longer ones are seldom repeated.
const maxShared = 128

func (p *jsonParser) text(s string) string {
	if len(s) > maxShared {
		return s
	}
	if got, ok := p.texts[s]; ok {
		return got
	}
	if p.texts == nil {
		p.texts = map[string]string{}
	}
	p.texts[s] = s
	return s
}

func (p *jsonParser) stringValue(s string) Value {
	if len(s) > maxShared {
		return String(s)
	}
	if got, ok := p.strs[s]; ok {
		return got
	}
	if p.strs == nil {
		p.strs = map[string]Value{}
	}
	v := Value(String(p.text(s)))
	p.strs[s] = v
	return v
}

func (p *jsonParser) numberValue(f float64) Value {
	bits := math.Float64bits(f)
	if got, ok := p.nums[bits]; ok {
		return got
	}
	if p.nums == nil {
		p.nums = map[uint64]Value{}
	}
	v := Value(Number(f))
	p.nums[bits] = v
	return v
}

// keyList is keys as a list shared by every object with the same keys in the same order.
func (p *jsonParser) keyList(keys []string) []string {
	p.idbuf = p.idbuf[:0]
	for _, k := range keys {
		p.idbuf = append(p.idbuf, k...)
		p.idbuf = append(p.idbuf, 0)
	}
	if got, ok := p.shapes[string(p.idbuf)]; ok { // no allocation for the lookup
		return got
	}
	if p.shapes == nil {
		p.shapes = map[string][]string{}
	}
	list := append([]string(nil), keys...)
	p.shapes[string(p.idbuf)] = list
	return list
}

func (p *jsonParser) fail(what string) {
	panic(&SyntaxError{fmt.Sprintf("JSON: %s at byte %d", what, p.base+int64(p.at))})
}

func (p *jsonParser) space() {
	for p.at < len(p.b) {
		switch p.b[p.at] {
		case ' ', '\t', '\r', '\n':
			p.at++
		default:
			return
		}
	}
}

// ParseJSON parses one JSON value.
func ParseJSON(text []byte) (v Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*SyntaxError); ok {
				err = e
				return
			}
			panic(r)
		}
	}()
	p := &jsonParser{b: text}
	v = p.value()
	p.space()
	if p.at != len(p.b) {
		p.fail("text after the value")
	}
	return v, nil
}

func (p *jsonParser) value() Value {
	p.space()
	if p.at >= len(p.b) {
		p.fail("unexpected end")
	}
	switch c := p.b[p.at]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		return p.stringValue(p.str())
	case c == 't':
		p.word("true")
		return Bool(true)
	case c == 'f':
		p.word("false")
		return Bool(false)
	case c == 'n':
		p.word("null")
		return Null{}
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	p.fail("unexpected character")
	return nil
}

func (p *jsonParser) word(w string) {
	if !strings.HasPrefix(string(p.b[p.at:min(len(p.b), p.at+len(w))]), w) {
		p.fail("bad literal")
	}
	p.at += len(w)
}

func (p *jsonParser) enter() {
	p.depth++
	if p.depth > maxDepth {
		p.fail("too deep")
	}
}

func (p *jsonParser) array() Value {
	p.enter()
	p.at++
	p.space()
	if p.at < len(p.b) && p.b[p.at] == ']' {
		p.at++
		p.depth--
		return &Array{Items: []Value{}}
	}
	base := len(p.vstack)
	for {
		v := p.value()
		p.vstack = append(p.vstack, v)
		p.space()
		if p.at >= len(p.b) {
			p.fail("unexpected end")
		}
		if p.b[p.at] == ',' {
			p.at++
			continue
		}
		if p.b[p.at] == ']' {
			p.at++
			p.depth--
			items := append([]Value(nil), p.vstack[base:]...)
			clear(p.vstack[base:])
			p.vstack = p.vstack[:base]
			return &Array{Items: items}
		}
		p.fail("expected , or ]")
	}
}

func (p *jsonParser) object() Value {
	p.enter()
	p.at++
	p.space()
	if p.at < len(p.b) && p.b[p.at] == '}' {
		p.at++
		p.depth--
		return &Object{Keys: []string{}, Values: []Value{}}
	}
	kbase, vbase := len(p.kstack), len(p.vstack)
	for {
		p.space()
		if p.at >= len(p.b) || p.b[p.at] != '"' {
			p.fail("expected a key")
		}
		key := p.text(p.str())
		p.space()
		if p.at >= len(p.b) || p.b[p.at] != ':' {
			p.fail("expected :")
		}
		p.at++
		val := p.value()
		// A key given twice keeps its first value, matching the exported data format
		// (AiStateMachineConfig's "Version", specs/data-tables.md §7.2).
		dup := false
		for _, k := range p.kstack[kbase:] {
			if k == key {
				dup = true
				break
			}
		}
		if !dup {
			p.kstack = append(p.kstack, key)
			p.vstack = append(p.vstack, val)
		}
		p.space()
		if p.at >= len(p.b) {
			p.fail("unexpected end")
		}
		if p.b[p.at] == ',' {
			p.at++
			continue
		}
		if p.b[p.at] == '}' {
			p.at++
			p.depth--
			o := &Object{Keys: p.keyList(p.kstack[kbase:]), Values: append([]Value(nil), p.vstack[vbase:]...)}
			clear(p.kstack[kbase:])
			clear(p.vstack[vbase:])
			p.kstack, p.vstack = p.kstack[:kbase], p.vstack[:vbase]
			return o
		}
		p.fail("expected , or }")
	}
}

func (p *jsonParser) number() Value {
	start := p.at
	if p.b[p.at] == '-' {
		p.at++
	}
	digits := func() int {
		n := 0
		for p.at < len(p.b) && p.b[p.at] >= '0' && p.b[p.at] <= '9' {
			p.at++
			n++
		}
		return n
	}
	if p.at < len(p.b) && p.b[p.at] == '0' {
		p.at++
	} else if digits() == 0 {
		p.fail("bad number")
	}
	if p.at < len(p.b) && p.b[p.at] == '.' {
		p.at++
		if digits() == 0 {
			p.fail("bad number")
		}
	}
	if p.at < len(p.b) && (p.b[p.at] == 'e' || p.b[p.at] == 'E') {
		p.at++
		if p.at < len(p.b) && (p.b[p.at] == '+' || p.b[p.at] == '-') {
			p.at++
		}
		if digits() == 0 {
			p.fail("bad number")
		}
	}
	f, err := strconv.ParseFloat(string(p.b[start:p.at]), 64)
	if err != nil {
		// Out of range: System.Text.Json's GetDouble gives ±infinity, ParseFloat says so too.
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			p.fail("bad number")
		}
	}
	return p.numberValue(f)
}

func hexVal(c byte) int {
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

func (p *jsonParser) str() string {
	p.at++ // the opening quote
	start := p.at
	// Fast path: no escapes.
	for p.at < len(p.b) {
		c := p.b[p.at]
		if c == '"' {
			raw := p.b[start:p.at]
			p.at++
			if got, ok := p.texts[string(raw)]; ok { // seen before: valid, no allocation
				return got
			}
			s := string(raw)
			if !utf8.ValidString(s) {
				p.fail("invalid UTF-8")
			}
			return p.text(s)
		}
		if c == '\\' || c < 0x20 {
			break
		}
		p.at++
	}
	buf := append([]byte{}, p.b[start:p.at]...)
	for {
		if p.at >= len(p.b) {
			p.fail("unterminated string")
		}
		c := p.b[p.at]
		switch {
		case c == '"':
			p.at++
			if !utf8.Valid(buf) {
				p.fail("invalid UTF-8")
			}
			return string(buf)
		case c < 0x20:
			p.fail("control character in a string")
		case c == '\\':
			p.at++
			if p.at >= len(p.b) {
				p.fail("unterminated escape")
			}
			e := p.b[p.at]
			p.at++
			switch e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				r := p.hex4()
				if utf16.IsSurrogate(r) {
					if p.at+1 < len(p.b) && p.b[p.at] == '\\' && p.b[p.at+1] == 'u' {
						p.at += 2
						r = utf16.DecodeRune(r, p.hex4())
					} else {
						r = utf8.RuneError
					}
				}
				buf = utf8.AppendRune(buf, r)
			default:
				p.fail("bad escape")
			}
		default:
			buf = append(buf, c)
			p.at++
		}
	}
}

func (p *jsonParser) hex4() rune {
	if p.at+4 > len(p.b) {
		p.fail("bad \\u escape")
	}
	r := 0
	for i := 0; i < 4; i++ {
		h := hexVal(p.b[p.at+i])
		if h < 0 {
			p.fail("bad \\u escape")
		}
		r = r*16 + h
	}
	p.at += 4
	return rune(r)
}

// ----- masks -----

// JSONMask is which parts of a JSON value are kept: the paths the server reads in it
// (specs/data-tables.tsv); the rest is dropped when the value is read. Written as text, one
// path a line relative to the value: .A.B a member of an object, [] each element of an array,
// {} each value of an object used as a map, * at the end: that part is kept whole.
type JSONMask struct {
	all      bool
	members  map[string]*JSONMask
	elements *JSONMask
	values   *JSONMask
	// Text is the mask's text, normalised (the layout hash takes it).
	Text string
}

// ParseMask parses a mask's text.
func ParseMask(text string) *JSONMask {
	seen := map[string]bool{}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !seen[l] {
			seen[l] = true
			lines = append(lines, l)
		}
	}
	sortStrings(lines)
	root := &JSONMask{Text: strings.Join(lines, "\n")}
	for _, l := range lines {
		root.add(l)
	}
	return root
}

func sortStrings(s []string) {
	// Ordinal order, as C#'s StringComparer.Ordinal (UTF-16 code units; the masks are ASCII).
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (m *JSONMask) add(path string) {
	node := m
	i := 0
	for i < len(path) && !node.all {
		switch {
		case path[i] == '*':
			node.all = true
			node.members, node.elements, node.values = nil, nil, nil
			return
		case strings.HasPrefix(path[i:], "[]"):
			if node.elements == nil {
				node.elements = &JSONMask{}
			}
			node = node.elements
			i += 2
		case strings.HasPrefix(path[i:], "{}"):
			if node.values == nil {
				node.values = &JSONMask{}
			}
			node = node.values
			i += 2
		case path[i] == '.':
			end := strings.IndexAny(path[i+1:], ".[{*")
			if end < 0 {
				end = len(path)
			} else {
				end += i + 1
			}
			name := path[i+1 : end]
			if node.members == nil {
				node.members = map[string]*JSONMask{}
			}
			child := node.members[name]
			if child == nil {
				child = &JSONMask{}
				node.members[name] = child
			}
			node = child
			i = end
		default:
			panic("bad JSON mask path " + path)
		}
	}
}

// Apply is the value with only the masked parts (the same object when nothing is dropped).
func (m *JSONMask) Apply(v Value) Value {
	if m.all {
		return v
	}
	switch x := v.(type) {
	case *Object:
		if m.values != nil {
			vs := make([]Value, len(x.Values))
			for i, it := range x.Values {
				vs[i] = m.values.Apply(it)
			}
			return &Object{Keys: x.Keys, Values: vs}
		}
		if m.members != nil {
			o := &Object{Keys: []string{}, Values: []Value{}}
			for i, k := range x.Keys {
				if child, ok := m.members[k]; ok {
					o.Keys = append(o.Keys, k)
					o.Values = append(o.Values, child.Apply(x.Values[i]))
				}
			}
			return o
		}
	case *Array:
		if m.elements != nil {
			items := make([]Value, len(x.Items))
			for i, it := range x.Items {
				items[i] = m.elements.Apply(it)
			}
			return &Array{Items: items}
		}
	}
	return v
}
