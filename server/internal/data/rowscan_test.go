// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"
)

// The old way: the whole file parsed at once, then each row converted.
func readWhole(t *testing.T, text string) []*Sample {
	t.Helper()
	v, err := ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var out []*Sample
	for i, it := range v.(*Array).Items {
		r, err := RowFromJSON[Sample](it.(*Object), fmt.Sprintf("Sample[%d]", i))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// R-150: a table read a row at a time from a stream gives the rows the whole parse gave, however
// the reads split the text: escaped quotes and brackets inside strings, nested values, numbers
// and words at the end of a read, white space everywhere.
func TestATableReadAsAStreamGivesTheRowsOfTheWholeText(t *testing.T) {
	odd := `{"Id": 9, "Name": "a \"]}[{\" b \\", "Data": {"x": [[1, [2]], {"y": "}"}], "z": -0.5e3, "w": [true, false, null]}, "Words": ["[1,2]", "{\"q\":1}"]}`
	text := " \n[ " + rowJSON + " ,\n" + odd + "," + strings.TrimPrefix(rows(40, 3), "[")
	want := readWhole(t, "["+rowJSON+","+odd+","+strings.TrimPrefix(rows(40, 3), "["))
	for _, r := range []struct {
		name string
		wrap func(io.Reader) io.Reader
	}{
		{"whole", func(s io.Reader) io.Reader { return s }},
		{"one byte at a time", iotest.OneByteReader},
		{"half reads", iotest.HalfReader},
	} {
		got, err := collectRows(r.wrap(strings.NewReader(text)))
		if err != nil || len(got) != len(want) {
			t.Fatal(r.name, err, len(got))
		}
		for i := range want {
			if d := Differences(want[i], got[i], fmt.Sprintf("Sample[%d]", i), 3); len(d) > 0 {
				t.Fatal(r.name, d)
			}
		}
	}
	if got, err := collectRows(strings.NewReader(" [ ] ")); err != nil || got == nil || len(got) != 0 {
		t.Fatal("empty table", got, err)
	}
}

// A row longer than the scanner's buffer grows it.
func TestARowLongerThanTheBufferIsRead(t *testing.T) {
	long := strings.Repeat("长", rowScanChunk)
	got, err := collectRows(strings.NewReader(`[{"Id": 1, "Name": "` + long + `"}, {"Id": 2}]`))
	if err != nil || len(got) != 2 || got[0].Name != long || got[1].Id != 2 {
		t.Fatal(err)
	}
}

// Broken text is a *SyntaxError naming the file and the byte; a row that is not an object is a
// *ShapeError, as before.
func TestBrokenTableTextIsAnErrorWithItsPlace(t *testing.T) {
	var syn *SyntaxError
	for text, at := range map[string]string{
		`[{"Id": 1}`:            "unexpected end at byte 10",
		`[{"Id": 1} {"Id": 2}]`: "expected , or ] at byte 11",
		`[{"Id": 1}, {"Id": 1]`: "expected , or } at byte 20",
		`[{"Id": "abc`:          "unexpected end at byte",
		`[{"Id": 1},`:           "unexpected end at byte 11",
		`[{"Id": 1}, {"Id" 1}]`: "expected : at byte 18",
		`[{"Id": tru}]`:         "bad literal at byte 8",
	} {
		_, err := collectRows(iotest.OneByteReader(strings.NewReader(text)))
		if !errors.As(err, &syn) || !strings.Contains(err.Error(), "x.json") || !strings.Contains(err.Error(), at) {
			t.Fatalf("%s: %v", text, err)
		}
	}
	var shape *ShapeError
	if _, err := collectRows(strings.NewReader(`[{"Id": 1}, 5]`)); !errors.As(err, &shape) || !strings.Contains(err.Error(), "Sample[1]: not an object") {
		t.Fatal(err)
	}
	if _, err := collectRows(strings.NewReader(`{"Id": 1}`)); !errors.As(err, &shape) {
		t.Fatal(err)
	}
	if _, err := collectRows(iotest.ErrReader(errors.New("disk gone"))); err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatal(err)
	}
}

// The cache built a row at a time from the JSON is, byte for byte, the cache of the rows
// ReadJSON gives; its rows read back equal them.
func TestTheCacheBuiltWhileReadingIsTheCacheOfTheRows(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/Sample.json"
	text := "[" + rowJSON + "," + strings.TrimPrefix(rows(SegmentRows+10, 2), "[")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	built, err := BuildCacheFromJSON[Sample](path, "Sample", 5)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ReadJSON[Sample](path, "Sample")
	if err != nil {
		t.Fatal(err)
	}
	WriteCache(dir+"/Sample.bin", 5, rows)
	if written, _ := os.ReadFile(dir + "/Sample.bin"); !bytes.Equal(written, built) {
		t.Fatal("the two caches differ")
	}
	back, problem := ReadCacheBytes[Sample](built, 5)
	if len(back) != len(rows) {
		t.Fatal(problem)
	}
	for i := range rows {
		if d := Differences(rows[i], back[i], "", 3); len(d) > 0 {
			t.Fatal(i, d)
		}
	}
}

func collectRows(r io.Reader) ([]*Sample, error) {
	rows := []*Sample{}
	_, err := readJSONRows[Sample](r, "x.json", "Sample", func(s *Sample) { rows = append(rows, s) })
	return rows, err
}
