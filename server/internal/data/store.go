// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"tidelab/internal/common"
)

// Source is where the tables come from: the JSON directory (one <Name>.json per table,
// written by tools/Tidelab.ConfigExport from the client's ConfigDB), further directories
// searched in order for tables it does not have (specs/data-tables.md §6), and the cache
// directory (<Name>.bin per table). With UseCache off the JSON is always parsed and no cache
// is written (the verification reads both ways).
type Source struct {
	JSONDir      string
	CacheDir     string
	UseCache     bool
	MoreJSONDirs []string
}

// JSONFile is the table's JSON file: the first directory that has it (the main one when none does).
func (s Source) JSONFile(name string) string {
	file := name + ".json"
	for _, dir := range append([]string{s.JSONDir}, s.MoreJSONDirs...) {
		p := filepath.Join(dir, file)
		if common.FileExists(p) {
			return p
		}
	}
	return filepath.Join(s.JSONDir, file)
}

// TableLoad is how one table was loaded.
type TableLoad struct {
	Name         string
	Rows         int
	FromCache    bool
	Time         time.Duration
	CacheProblem string
}

// dotnetEpochTicks is 1970-01-01 in .NET ticks (100 ns since 0001-01-01).
const dotnetEpochTicks = 621355968000000000

// Signature is the JSON file's name, size and modification time: new game data never reads an
// old cache. (The same hash as the C# loader, so either reads the other's cache.)
func Signature(path string) (uint64, error) {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("table file not found: %s: %w", path, fs.ErrNotExist)
		}
		return 0, err
	}
	if st.IsDir() {
		return 0, fmt.Errorf("table file not found: %s: %w", path, fs.ErrNotExist)
	}
	h := Fnv([]byte(filepath.Base(path)), fnvOffset)
	h = Fnv(binary.LittleEndian.AppendUint64(nil, uint64(st.Size())), h)
	ticks := st.ModTime().UTC().UnixNano()/100 + dotnetEpochTicks
	return Fnv(binary.LittleEndian.AppendUint64(nil, uint64(ticks)), h), nil
}

// LoadTable is the table's rows, from the cache when it is current, otherwise from the JSON
// (and then the cache is written for next time). A missing JSON file is an error even when a
// cache exists: the JSON is the source.
func LoadTable[T any, PT RowPtr[T]](name string, src Source) ([]PT, TableLoad, error) {
	start := time.Now()
	jsonPath := src.JSONFile(name)
	sig, err := Signature(jsonPath)
	if err != nil {
		return nil, TableLoad{}, err
	}
	if !src.UseCache {
		rows, err := ReadJSON[T, PT](jsonPath, name)
		if err != nil {
			return nil, TableLoad{}, err
		}
		return rows, TableLoad{name, len(rows), false, time.Since(start), ""}, nil
	}
	cache := filepath.Join(src.CacheDir, name+".bin")
	problem := ""
	rows, p := ReadCache[T, PT](cache, sig)
	if rows != nil {
		return rows, TableLoad{name, len(rows), true, time.Since(start), ""}, nil
	}
	if p != "" {
		problem = p
		common.Warn("data", fmt.Sprintf("%s: %s, reading the JSON", cache, p))
	}
	// R-150: each row read from the JSON goes into the cache's bytes at once and is dropped;
	// the rows are then read from those bytes, so the JSON's rows never all exist together and
	// the first start after new data holds what every later start holds (equal values shared,
	// one block per segment; R-147b).
	built, err := BuildCacheFromJSON[T, PT](jsonPath, name, sig)
	if err != nil {
		return nil, TableLoad{}, err
	}
	if err := WriteFileAtomic(cache, built); err != nil {
		common.Warn("data", fmt.Sprintf("%s: writing the cache failed: %v", cache, err))
	}
	rows, p = ReadCacheBytes[T, PT](built, sig)
	if rows == nil {
		return nil, TableLoad{}, fmt.Errorf("%s: the cache just built from %s does not read back: %s", cache, jsonPath, p)
	}
	return rows, TableLoad{name, len(rows), false, time.Since(start), problem}, nil
}

// ReadJSON reads a table's JSON file: an array of row objects. R-150: the file is read as a
// stream and parsed a row at a time; only one row's JSON value exists at once.
func ReadJSON[T any, PT RowPtr[T]](path, name string) (rows []PT, err error) {
	rows = []PT{}
	_, err = eachJSONRow(path, name, func(r PT) { rows = append(rows, r) })
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// BuildCacheFromJSON is the cache file of a table's JSON file, each row written as it is read.
func BuildCacheFromJSON[T any, PT RowPtr[T]](path, name string, sig uint64) ([]byte, error) {
	b := cacheBuilder{}
	n, err := eachJSONRow(path, name, func(r PT) { b.add(r) })
	if err != nil {
		return nil, err
	}
	return b.bytes(CacheHeader{LayoutHash(PT(new(T))), sig, uint32(n)}), nil
}

// eachJSONRow hands each row of a table's JSON file to use, in order, and is their count.
func eachJSONRow[T any, PT RowPtr[T]](path, name string, use func(PT)) (count int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return readJSONRows[T, PT](f, path, name, use)
}

func readJSONRows[T any, PT RowPtr[T]](f io.Reader, path, name string, use func(PT)) (count int, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *ShapeError:
				err = e
			case *SyntaxError:
				err = fmt.Errorf("%s: %w", path, e)
			case error:
				err = fmt.Errorf("%s: %w", path, e) // reading the file failed
			default:
				panic(r)
			}
		}
	}()
	s := newRowScanner(f)
	if c, ok := s.peek(); !ok || c != '[' {
		return 0, &ShapeError{path + ": not a JSON array"}
	}
	s.skip()
	if c, ok := s.peek(); ok && c == ']' {
		return 0, nil
	}
	p := &jsonParser{}
	for {
		if _, ok := s.peek(); !ok {
			s.fail("unexpected end", s.offset())
		}
		p.base = s.offset()
		p.b, p.at = s.value(), 0
		v := p.value()
		if p.at != len(p.b) {
			p.fail("unexpected character")
		}
		o, ok := v.(*Object)
		if !ok {
			return count, &ShapeError{fmt.Sprintf("%s[%d]: not an object", name, count)}
		}
		use(rowFromJSON[T, PT](o, fmt.Sprintf("%s[%d]", name, count)))
		count++
		c, ok := s.peek()
		if !ok {
			s.fail("unexpected end", s.offset())
		}
		s.skip()
		if c == ',' {
			continue
		}
		if c == ']' {
			return count, nil
		}
		s.fail("expected , or ]", s.offset()-1)
	}
}

// cacheBuilder writes rows into the cache's segments as they come (SegmentRows a segment, each
// with its own Writer, so each segment shares strings and JSON values only within itself). A
// full segment is encoded on its own goroutine, at most buildersAtOnce at a time, while the
// next one fills.
type cacheBuilder struct {
	batch  []Row
	parts  [][]byte
	counts []int
	wg     sync.WaitGroup
	slots  chan struct{}
}

const buildersAtOnce = 4

func (b *cacheBuilder) add(r Row) {
	b.batch = append(b.batch, r)
	if len(b.batch) == SegmentRows {
		b.endSegment()
	}
}

func (b *cacheBuilder) endSegment() {
	if len(b.batch) == 0 {
		return
	}
	if b.slots == nil {
		b.slots = make(chan struct{}, buildersAtOnce)
	}
	seg, batch := len(b.parts), b.batch
	b.parts = append(b.parts, nil)
	b.counts = append(b.counts, len(batch))
	b.batch = nil
	b.slots <- struct{}{}
	b.wg.Add(1)
	go func() {
		defer func() { <-b.slots; b.wg.Done() }()
		w := NewWriter()
		for _, r := range batch {
			WriteRow(w, r)
		}
		b.parts[seg] = w.Bytes()
	}()
}

// bytes is the whole cache file: the header, the segment table, the segments.
func (b *cacheBuilder) bytes(h CacheHeader) []byte {
	b.endSegment()
	b.wg.Wait()
	head := NewWriter()
	head.Header(h)
	head.U32(uint32(len(b.parts)))
	size := 0
	for i, p := range b.parts {
		head.I32(int32(b.counts[i]))
		head.I32(int32(len(p)))
		size += len(p)
	}
	out := make([]byte, 0, len(head.Bytes())+size)
	out = append(out, head.Bytes()...)
	for _, p := range b.parts {
		out = append(out, p...)
	}
	return out
}

// WriteCache writes the cache of rows (through WriteFileAtomic). Failing to write is only a
// warning: the next start reads the JSON again.
func WriteCache[T any, PT RowPtr[T]](path string, sig uint64, rows []PT) {
	b := cacheBuilder{}
	for _, r := range rows {
		b.add(r)
	}
	if err := WriteFileAtomic(path, b.bytes(CacheHeader{LayoutHash(PT(new(T))), sig, uint32(len(rows))})); err != nil {
		common.Warn("data", fmt.Sprintf("%s: writing the cache failed: %v", path, err))
	}
}

// ReadCache is the cached rows, or nil (with the reason, unless there simply is no cache or it
// is out of date) when they cannot be used.
func ReadCache[T any, PT RowPtr[T]](path string, sig uint64) (rows []PT, problem string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	return ReadCacheBytes[T, PT](data, sig)
}

// ReadCacheBytes is ReadCache of a cache file's bytes.
func ReadCacheBytes[T any, PT RowPtr[T]](data []byte, sig uint64) (rows []PT, problem string) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*CorruptError); ok {
				rows, problem = nil, "cache unreadable ("+e.Msg+")"
				return
			}
			panic(r)
		}
	}()
	r := NewReader(data, 0, len(data))
	h, ok := r.Header()
	if !ok || h.Layout != LayoutHash(PT(new(T))) || h.Source != sig {
		return nil, "" // another codec, row type or JSON file: out of date, not damaged
	}
	rows = make([]PT, h.Rows)
	segments := r.Count()
	type seg struct{ row, n, from, to int }
	starts := make([]seg, segments)
	at, row := r.Position()+segments*8, 0
	for s := 0; s < segments; s++ {
		n, size := int(r.I32()), int(r.I32())
		if n < 0 || size < 0 || row+n > len(rows) || at+size > len(data) {
			corrupt("bad segment table")
		}
		starts[s] = seg{row, n, at, at + size}
		row += n
		at += size
	}
	if row != len(rows) || at != len(data) {
		return nil, "segments do not add up to the rows and the file"
	}
	var wg sync.WaitGroup
	errs := make([]*CorruptError, segments)
	for s := range starts {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					e, ok := r.(*CorruptError)
					if !ok {
						panic(r)
					}
					errs[s] = e
				}
			}()
			sg := starts[s]
			sr := NewReader(data, sg.from, sg.to)
			// R-147: a segment's rows are one block, not one allocation each.
			block := make([]T, sg.n)
			for i := 0; i < sg.n; i++ {
				row := PT(&block[i])
				row.Fields(readVisitor{sr})
				rows[sg.row+i] = row
			}
			if !sr.AtEnd() {
				corrupt("segment longer than its rows")
			}
		}(s)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, "cache unreadable (" + e.Msg + ")"
		}
	}
	return rows, ""
}

// WriteFileAtomic writes parts to path through a file of this process, then renames it: a
// crash never leaves half a file, and two processes never write into each other's file.
func WriteFileAtomic(path string, parts ...[]byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	write := func() error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		for _, p := range parts {
			if err == nil {
				_, err = f.Write(p)
			}
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	err := write()
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// parallelism is how many tables load at once.
func parallelism(n int) int {
	if n > 0 {
		return n
	}
	return runtime.NumCPU()
}
