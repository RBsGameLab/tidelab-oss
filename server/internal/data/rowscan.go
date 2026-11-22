// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"fmt"
	"io"
)

// rowScanner hands out the elements of a JSON array read from a stream, one at a time, as the
// bytes of that element (R-150): a table's file is never all in memory at once. It only finds
// where each element ends (brackets and strings); the parser reads the element and reports what
// is wrong inside it.
type rowScanner struct {
	r        io.Reader
	buf      []byte
	at, end  int   // the unread bytes are buf[at:end]
	consumed int64 // file offset of buf[0]
	eof      bool
	err      error
}

const rowScanChunk = 1 << 20

func newRowScanner(r io.Reader) *rowScanner {
	return &rowScanner{r: r, buf: make([]byte, rowScanChunk)}
}

// offset is the file offset of the next unread byte.
func (s *rowScanner) offset() int64 { return s.consumed + int64(s.at) }

func (s *rowScanner) fail(what string, at int64) {
	panic(&SyntaxError{fmt.Sprintf("JSON: %s at byte %d", what, at)})
}

// fill reads more bytes after buf[end], moving the unread ones to the front first and growing
// the buffer when they fill it; false at the end of the stream.
func (s *rowScanner) fill() bool {
	if s.eof {
		return false
	}
	if s.at > 0 {
		n := copy(s.buf, s.buf[s.at:s.end])
		s.consumed += int64(s.at)
		s.at, s.end = 0, n
	}
	if s.end == len(s.buf) {
		s.buf = append(s.buf, make([]byte, len(s.buf))...)
	}
	for {
		n, err := s.r.Read(s.buf[s.end:])
		s.end += n
		if err == io.EOF {
			s.eof = true
			return n > 0
		}
		if err != nil {
			s.eof, s.err = true, err
			panic(err)
		}
		if n > 0 {
			return true
		}
	}
}

// peek skips white space and is the next byte, false at the end of the stream.
func (s *rowScanner) peek() (byte, bool) {
	for {
		for s.at < s.end {
			switch c := s.buf[s.at]; c {
			case ' ', '\t', '\r', '\n':
				s.at++
			default:
				return c, true
			}
		}
		if !s.fill() {
			return 0, false
		}
	}
}

// skip takes the byte peek returned.
func (s *rowScanner) skip() { s.at++ }

// value is the next value's bytes (after peek): valid until the next call.
func (s *rowScanner) value() []byte {
	start := s.offset()
	k := 0 // offset from s.at; fill moves s.at, not k
	byteAt := func() (byte, bool) {
		for s.at+k >= s.end {
			if !s.fill() {
				return 0, false
			}
		}
		return s.buf[s.at+k], true
	}
	c, _ := byteAt()
	depth, inString := 0, false
	switch {
	case c == '{' || c == '[':
	case c == '"':
		inString = true
		k++
	default:
		// A number or a word: up to the next delimiter.
		for {
			c, ok := byteAt()
			if !ok || c == ',' || c == ']' || c == '}' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			k++
		}
		v := s.buf[s.at : s.at+k]
		s.at += k
		return v
	}
	for {
		c, ok := byteAt()
		if !ok {
			s.fail("unexpected end", start+int64(k))
		}
		k++
		if inString {
			switch c {
			case '\\':
				k++ // the escaped byte cannot end the string
			case '"':
				inString = false
			}
			if !inString && depth == 0 {
				break
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if k > s.end-s.at {
		s.fail("unexpected end", start+int64(s.end-s.at))
	}
	v := s.buf[s.at : s.at+k]
	s.at += k
	return v
}
