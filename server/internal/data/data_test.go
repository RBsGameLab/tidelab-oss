// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package data

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"tidelab/internal/common"
)

// Row types of every field kind the generated tables use, written the way configexport gen writes them.
type Inner struct {
	X    int32
	Y    float32
	Name string
}

func (*Inner) TypeName() string { return "Inner" }
func (r *Inner) Fields(v Visitor) {
	F(v, "X", &r.X, IntC)
	F(v, "Y", &r.Y, FloatC)
	F(v, "Name", &r.Name, StringC)
}

type Sample struct {
	Id       int32
	Flag     bool
	Big      int64
	Ratio    float32
	Exact    float64
	Name     string
	Data     Value
	One      *Inner
	Ids      []int32
	Words    []string
	Many     []*Inner
	ByNumber *Map[int32, string]
	ByName   *Map[string, int64]
}

func (*Sample) TypeName() string { return "Sample" }
func (r *Sample) Fields(v Visitor) {
	F(v, "Id", &r.Id, IntC)
	F(v, "Flag", &r.Flag, BoolC)
	F(v, "Big", &r.Big, LongC)
	F(v, "Ratio", &r.Ratio, FloatC)
	F(v, "Exact", &r.Exact, DoubleC)
	F(v, "Name", &r.Name, StringC)
	F(v, "Data", &r.Data, JSONC)
	F(v, "One", &r.One, TableOf[Inner]())
	F(v, "Ids", &r.Ids, ArrayOf(IntC))
	F(v, "Words", &r.Words, ArrayOf(StringC))
	F(v, "Many", &r.Many, ArrayOf(TableOf[Inner]()))
	F(v, "ByNumber", &r.ByNumber, MapOf(IntC, StringC))
	F(v, "ByName", &r.ByName, MapOf(StringC, LongC))
}

type sampleRenamed struct{ Id, Other int32 }

func (*sampleRenamed) TypeName() string { return "SampleRenamed" }
func (r *sampleRenamed) Fields(v Visitor) {
	F(v, "Id", &r.Id, IntC)
	F(v, "Other", &r.Other, IntC)
}

type sampleRetyped struct {
	Id    int32
	Other int64
}

func (*sampleRetyped) TypeName() string { return "SampleRetyped" }
func (r *sampleRetyped) Fields(v Visitor) {
	F(v, "Id", &r.Id, IntC)
	F(v, "Other", &r.Other, LongC)
}

// As tools/Tidelab.ConfigExport writes a row: every field, in order, int64 as a string.
const rowJSON = `{
  "Id": 106800000,
  "Flag": true,
  "Big": "57158396800000001",
  "Ratio": 0.10000000149011612,
  "Exact": -0.125,
  "Name": "古今多少故事",
  "Data": {"BaseInfoComponent": {"TidName": "Monster001", "Camp": 1}, "Ids": [1, 2.5, null, true], "Again": {"TidName": "Monster001", "Camp": 1}},
  "One": {"X": 3, "Y": 1.5, "Name": "Monster001"},
  "Ids": [1, -2, 2147483647],
  "Words": ["[1,2]", ""],
  "Many": [{"X": 1, "Y": 0, "Name": ""}, null],
  "ByNumber": {"3": "three", "1": "one"},
  "ByName": {"k": "9"}
}`

func parse(t *testing.T, text string) (*Sample, error) {
	v, err := ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return RowFromJSON[Sample](v.(*Object), "Sample[0]")
}

func mustParse(t *testing.T, text string) *Sample {
	r, err := parse(t, text)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func encode(rows ...*Sample) []byte {
	w := NewWriter()
	w.Header(CacheHeader{LayoutHash(&Sample{}), 77, uint32(len(rows))})
	for _, r := range rows {
		WriteRow(w, r)
	}
	return w.Bytes()
}

func readRows(t *testing.T, b []byte, n int) ([]*Sample, *Reader) {
	r := NewReader(b, 0, len(b))
	if _, ok := r.Header(); !ok {
		t.Fatal("no header")
	}
	var out []*Sample
	for i := 0; i < n; i++ {
		out = append(out, ReadRow[Sample](r))
	}
	return out, r
}

func TestJSONReadsEveryFieldKind(t *testing.T) {
	r := mustParse(t, rowJSON)
	inner := r.Data.(*Object).Get("BaseInfoComponent").(*Object).Get("TidName")
	three, ok := r.ByNumber.Get(3)
	if r.Id != 106800000 || !r.Flag || r.Big != 57158396800000001 || r.Ratio != 0.1 || r.Name != "古今多少故事" ||
		inner != String("Monster001") || r.One.X != 3 || fmt.Sprint(r.Ids) != "[1 -2 2147483647]" ||
		r.Words[0] != "[1,2]" || r.Many[1] != nil || r.ByNumber.Pairs[1].Key != 1 || !ok || three != "three" || r.ByName.Pairs[0].Value != 9 {
		t.Fatalf("%+v", r)
	}
}

func TestCacheRoundTripGivesTheSameRows(t *testing.T) {
	a := mustParse(t, rowJSON)
	b := mustParse(t, strings.ReplaceAll(strings.ReplaceAll(rowJSON, "106800000", "7"), `"three"`, `"drei"`))
	data := encode(a, b, a)
	back, r := readRows(t, data, 3)
	h, _ := NewReader(data, 0, len(data)).Header()
	if h.Layout != LayoutHash(&Sample{}) || h.Source != 77 || h.Rows != 3 || !r.AtEnd() {
		t.Fatal(h)
	}
	for i, want := range []*Sample{a, b, a} {
		if d := Differences(want, back[i], "", 10); len(d) > 0 {
			t.Fatal(d)
		}
	}
	if d := strings.Join(Differences(back[1], back[0], "Sample[1]", 10), "|"); d != "Sample[1].Id: 7 vs 106800000|Sample[1].ByNumber: {2 entries} vs {2 entries}" {
		t.Fatal(d)
	}
}

func TestRepeatedStringsAndJSONDecodeToOneCopy(t *testing.T) {
	a := mustParse(t, rowJSON)
	data := encode(a, a)
	back, _ := readRows(t, data, 2)
	d := back[0].Data.(*Object)
	// Equal objects inside one value, and the same value in the next row, are one object.
	if d.Get("BaseInfoComponent") != d.Get("Again") || back[0].Data != back[1].Data {
		t.Fatal("not shared")
	}
	if unsafe.StringData(back[0].Name) != unsafe.StringData(back[1].Name) {
		t.Fatal("string not shared")
	}
	// Objects of one shape share their keys.
	k1, k2 := d.Get("BaseInfoComponent").(*Object).Keys, back[1].Data.(*Object).Get("Again").(*Object).Keys
	if &k1[0] != &k2[0] {
		t.Fatal("keys not shared")
	}
	one := encode(a)
	if !(len(data)-len(one) < len(one)-HeaderLength) {
		t.Fatal("sharing did not shrink the second row")
	}
}

func TestNumbersKeepTheirBits(t *testing.T) {
	row := mustParse(t, strings.Replace(rowJSON, "[1, 2.5, null, true]", "[-0.0, 0, 1e300, 3000000000, -7]", 1))
	back, _ := readRows(t, encode(row), 1)
	ids := back[0].Data.(*Object).Get("Ids").(*Array).Items
	if !math.Signbit(float64(ids[0].(Number))) || math.Signbit(float64(ids[1].(Number))) || ids[2] != Number(1e300) || ids[3] != Number(3000000000) || ids[4] != Number(-7) {
		t.Fatal(ids)
	}
}

func TestDamageIsAnErrorNotGarbage(t *testing.T) {
	data := encode(mustParse(t, rowJSON))
	for cut := HeaderLength; cut < len(data); cut += 7 {
		func() {
			defer func() {
				if _, ok := recover().(*CorruptError); !ok {
					t.Fatalf("cut at %d: no CorruptError", cut)
				}
			}()
			r := NewReader(data[:cut], 0, cut)
			r.Header()
			ReadRow[Sample](r)
		}()
	}
	wrong := append([]byte{}, data...)
	wrong[0] = 'X'
	if _, ok := NewReader(wrong, 0, len(wrong)).Header(); ok {
		t.Fatal("bad magic read")
	}
	old := append([]byte{}, data...)
	old[4] = CodecVersion + 1
	if _, ok := NewReader(old, 0, len(old)).Header(); ok {
		t.Fatal("other codec read")
	}
}

func TestLayoutHashFollowsTheFields(t *testing.T) {
	if LayoutHash(&sampleRenamed{}) == LayoutHash(&sampleRetyped{}) || LayoutHash(&Sample{}) == LayoutHash(&sampleRenamed{}) {
		t.Fatal("hash ignores fields")
	}
	if !strings.Contains(LayoutText(&Sample{}), "One:table(Inner{X:i32;Y:f32;Name:str;})") {
		t.Fatal(LayoutText(&Sample{}))
	}
}

func TestJSONOfTheWrongShapeIsRejected(t *testing.T) {
	var se *ShapeError
	for _, bad := range []string{strings.Replace(rowJSON, `"Id": 106800000`, `"Id": 1.5`, 1), strings.Replace(rowJSON, `"Flag": true`, `"Flag": 1`, 1)} {
		if _, err := parse(t, bad); !errors.As(err, &se) {
			t.Fatal(err)
		}
	}
	if _, err := parse(t, strings.Replace(rowJSON, `"Flag": true`, `"Flag": true, "Unknown": 3`, 1)); !errors.As(err, &se) || !strings.Contains(err.Error(), "Unknown") {
		t.Fatal(err)
	}
	// A field the JSON leaves out gets its default.
	r := mustParse(t, `{"Id": 5}`)
	if r.Name != "" || len(r.Ids) != 0 || r.One != nil {
		t.Fatal(r)
	}
}

func TestNumbersMayBeStringsBoolsAndStringsAreStrictNullIsTheDefault(t *testing.T) {
	r := mustParse(t, `{"Id": "12", "Ratio": " 1.5 ", "Exact": "-2e3", "Big": 5, "Name": null, "Ids": null, "ByNumber": {"-4": "x"}}`)
	if r.Id != 12 || r.Ratio != 1.5 || r.Exact != -2000 || r.Big != 5 || r.Name != "" || len(r.Ids) != 0 || r.ByNumber.Pairs[0].Key != -4 {
		t.Fatal(r)
	}
	for _, bad := range []string{`{"Flag": "true"}`, `{"Name": 3}`, `{"ByNumber": {"x": "y"}}`, `{"Id": "0x10"}`} {
		if _, err := parse(t, bad); err == nil {
			t.Fatal(bad)
		}
	}
	// A repeated key keeps its first value.
	if r := mustParse(t, `{"Id": 1, "Id": 2}`); r.Id != 1 {
		t.Fatal(r.Id)
	}
}

func TestFloatColumnsReadAsDoubleTakeTheShortDecimal(t *testing.T) {
	for in, want := range map[float64]float64{0.08100000023841858: 0.081, 0.081: 0.081, 1.5: 1.5} {
		if got := FloatAsDouble(float32(in)); got != want {
			t.Fatal(in, got)
		}
	}
}

func rows(n, seed int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"Id": %d, "Name": "row%d", "Data": {"Pos": {"X": %d, "Y": %d}}, "Ids": [%d, %d]}`, i, i%100, i%7, seed, i, i+1)
	}
	b.WriteByte(']')
	return b.String()
}

func TestTheStoreWritesReadsAndRefreshesTheCache(t *testing.T) {
	dir := t.TempDir()
	src := Source{JSONDir: dir, CacheDir: filepath.Join(dir, "cache"), UseCache: true}
	jsonPath := filepath.Join(dir, "Sample.json")
	os.WriteFile(jsonPath, []byte(rows(3, 0)), 0o644)
	first, a, err := LoadTable[Sample]("Sample", src)
	if err != nil || a.FromCache || !common.FileExists(filepath.Join(dir, "cache", "Sample.bin")) {
		t.Fatal(err, a)
	}
	if tmp, _ := filepath.Glob(filepath.Join(dir, "cache", "*.tmp")); len(tmp) > 0 {
		t.Fatal(tmp)
	}
	second, b, _ := LoadTable[Sample]("Sample", src)
	if !b.FromCache || len(second) != 3 || len(Differences(first[1], second[1], "", 5)) > 0 {
		t.Fatal(b)
	}
	// Changed JSON is read again: other size, or the same size with a new modification time.
	os.WriteFile(jsonPath, []byte(rows(4, 0)), 0o644)
	if r, info, _ := LoadTable[Sample]("Sample", src); info.FromCache || len(r) != 4 {
		t.Fatal(info)
	}
	os.WriteFile(jsonPath, []byte(rows(4, 1)), 0o644)
	os.Chtimes(jsonPath, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	r, info, _ := LoadTable[Sample]("Sample", src)
	if info.FromCache || r[0].Data.(*Object).Get("Pos").(*Object).Get("Y") != Number(1) {
		t.Fatal(info)
	}
	// A damaged cache falls back to the JSON and is rewritten.
	bin := filepath.Join(dir, "cache", "Sample.bin")
	bb, _ := os.ReadFile(bin)
	for i := len(bb) - 20; i < len(bb); i++ {
		bb[i] = 0xFF
	}
	os.WriteFile(bin, bb, 0o644)
	log, restore := common.CaptureLog()
	r, info, _ = LoadTable[Sample]("Sample", src)
	restore()
	if info.FromCache || len(r) != 4 || !strings.Contains(log.String(), "reading the JSON") {
		t.Fatal(info, log.String())
	}
	if r, info, _ := LoadTable[Sample]("Sample", src); !info.FromCache || len(r) != 4 {
		t.Fatal(info)
	}
	// A missing JSON file is an error even with a cache.
	os.Remove(jsonPath)
	if _, _, err := LoadTable[Sample]("Sample", src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestBigTablesAreSplitIntoSegmentsThatDecodeTheSame(t *testing.T) {
	dir := t.TempDir()
	src := Source{JSONDir: dir, CacheDir: filepath.Join(dir, "cache"), UseCache: true}
	n := SegmentRows*2 + 5
	os.WriteFile(filepath.Join(dir, "Sample.json"), []byte(rows(n, 0)), 0o644)
	fromJSON, _, _ := LoadTable[Sample]("Sample", src)
	fromCache, info, _ := LoadTable[Sample]("Sample", src)
	if !info.FromCache || len(fromCache) != n || fromCache[n-1].Id != int32(n-1) {
		t.Fatal(info)
	}
	for i := 0; i < n; i += 997 {
		if d := Differences(fromJSON[i], fromCache[i], "", 5); len(d) > 0 {
			t.Fatal(d)
		}
	}
}

func TestWithoutTheCacheNothingIsWritten(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Sample.json"), []byte(rows(2, 0)), 0o644)
	_, info, _ := LoadTable[Sample]("Sample", Source{JSONDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if _, err := os.Stat(filepath.Join(dir, "cache")); info.FromCache || !os.IsNotExist(err) {
		t.Fatal(info, err)
	}
}

// The table list is specs/data-tables.tsv's, in its order.
func TestTheTablesAreTheTsvsInItsOrder(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "specs", "data-tables.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var want []string
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if first {
			first = false
			continue
		}
		name := strings.Split(line, "\t")[0]
		if len(want) == 0 || want[len(want)-1] != name {
			want = append(want, name)
		}
	}
	if strings.Join(want, ",") != strings.Join(Names, ",") {
		t.Fatalf("%d tables in the tsv, %d in the list", len(want), len(Names))
	}
}

func TestARepeatedKeyIndexesItsLastRowAndLevelEntitiesByMapAndID(t *testing.T) {
	d := &DataTables{
		PhantomSkill:      []*PhantomSkill{{PhantomSkillId: 1, BuffEffects: []float64{1}}, {PhantomSkillId: 1, BuffEffects: []float64{2}}},
		LevelEntityConfig: []*LevelEntityConfig{{MapId: 8, EntityId: 5}, {MapId: 900, EntityId: 5}, {MapId: 8, EntityId: 6}},
	}
	d.Reindex()
	if d.PhantomSkillByPhantomSkillId[1].BuffEffects[0] != 2 {
		t.Fatal("first row kept")
	}
	if d.LevelEntity(900, 5) != d.LevelEntityConfig[1] || d.LevelEntity(900, 6) != nil || d.LevelEntityAnyMap(5) != d.LevelEntityConfig[0] || fmt.Sprint(d.LevelEntityRowsOfMap(8)) != "[0 2]" {
		t.Fatal("level entity index")
	}
}

// R-149: the sorted level entity index answers as the hash maps it replaced did: the first row
// of a (map, id), the first row of an id on any map, a map's rows in table order.
func TestTheSortedLevelEntityIndexAnswersAsMapsWould(t *testing.T) {
	rnd := rand.New(rand.NewPCG(149, 1))
	rows := make([]*LevelEntityConfig, 50000)
	for i := range rows {
		// Few maps and ids, so keys repeat; the extreme ids check the edges of the searches.
		rows[i] = &LevelEntityConfig{MapId: []int32{8, 900, -1, math.MaxInt32, 7}[rnd.IntN(5)], EntityId: rnd.Int32N(80) - 40}
		if rnd.IntN(100) == 0 {
			rows[i].EntityId = []int32{math.MinInt32, math.MaxInt32}[rnd.IntN(2)]
		}
	}
	d := (&DataTables{LevelEntityConfig: rows}).Reindex()
	byMapAndID, firstByID, ofMap := map[[2]int32]int{}, map[int32]int{}, map[int32][]int32{}
	for i, r := range rows {
		if _, ok := byMapAndID[[2]int32{r.MapId, r.EntityId}]; !ok {
			byMapAndID[[2]int32{r.MapId, r.EntityId}] = i
		}
		if _, ok := firstByID[r.EntityId]; !ok {
			firstByID[r.EntityId] = i
		}
		ofMap[r.MapId] = append(ofMap[r.MapId], int32(i))
	}
	maps := []int32{8, 900, -1, math.MaxInt32, 7, 0, 6, math.MinInt32}
	for _, m := range maps {
		if got, want := d.LevelEntityRowsOfMap(m), ofMap[m]; !slices.Equal(got, want) {
			t.Fatalf("map %d: %d rows, want %d", m, len(got), len(want))
		}
		for id := int32(-42); id <= 42; id++ {
			want, ok := byMapAndID[[2]int32{m, id}]
			if got := d.LevelEntity(m, id); (got != nil) != ok || ok && got != rows[want] {
				t.Fatalf("LevelEntity(%d, %d)", m, id)
			}
		}
	}
	for _, id := range []int32{math.MinInt32, -41, -40, 0, 39, 40, math.MaxInt32} {
		want, ok := firstByID[id]
		if got := d.LevelEntityAnyMap(id); (got != nil) != ok || ok && got != rows[want] {
			t.Fatalf("LevelEntityAnyMap(%d)", id)
		}
	}
	if (&DataTables{}).Reindex().LevelEntity(8, 1) != nil || len((&DataTables{}).LevelEntityRowsOfMap(8)) != 0 {
		t.Fatal("empty table")
	}
}

func TestAValueOutsideItsRuleStopsTheLoad(t *testing.T) {
	d := &DataTables{BlueprintConfig: []*BlueprintConfig{{Id: 1, EntityLogic: "Item"}, {Id: 2, EntityLogic: "Nope"}}, Buff: []*Buff{{Id: 9, GameAttributeID: 144}}}
	p := strings.Join(CheckValues(d), "\n")
	if !strings.Contains(p, `BlueprintConfig.EntityLogic of row 2: "Nope" is not one of Item`) || !strings.Contains(p, "Buff.GameAttributeID of row 9: 144 is not 0-143") {
		t.Fatal(p)
	}
}

// The 3.8 data, when this machine has it: the Go loader writes the C# loader's cache files.
func TestLab38LoadsAndVerifies(t *testing.T) {
	lab := common.GameDataDir()
	if lab == "" || !common.FileExists(filepath.Join(lab, "BinData", "Buff.json")) {
		t.Skip("no 3.8 data here")
	}
	src := Source{JSONDir: filepath.Join(lab, "BinData"), MoreJSONDirs: []string{filepath.Join(lab, "BinData-beta")}, CacheDir: t.TempDir(), UseCache: true}
	a, err := Load(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range b.Loads {
		if !l.FromCache {
			t.Fatal(l.Name, "not from the cache")
		}
	}
	for _, d := range Compare(a, b, 3) {
		if len(d.Differences) > 0 {
			t.Fatal(d)
		}
	}
	// R-150: the first load's rows come from the cache it built; the JSON read on its own agrees.
	src.UseCache = false
	c, err := Load(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Compare(a, c, 3) {
		if len(d.Differences) > 0 {
			t.Fatal(d)
		}
	}
	if b.LevelEntity(8, 122000892) == nil {
		t.Fatal("beacon 122000892 not on map 8")
	}
}

// R-174: the client does not ship CharacterInitInfo and SkillBranchBuff (server-only data), so
// a newcomer's BinData lacks them; they load as no rows. Any other missing table still stops.
func TestOptionalTablesLoadEmptyWhenMissing(t *testing.T) {
	src := Source{JSONDir: t.TempDir()}
	byName := map[string]slot{}
	for _, s := range slots {
		byName[s.name] = s
	}
	d := &DataTables{}
	for _, name := range []string{"CharacterInitInfo", "SkillBranchBuff"} {
		info, err := byName[name].load(d, src)
		if err != nil || info.Rows != 0 || info.Name != name {
			t.Fatalf("%s: %+v, %v; want empty, no error", name, info, err)
		}
	}
	if d.CharacterInitInfoByRoleId == nil || d.SkillBranchBuffByBranchId == nil {
		t.Fatal("indexes of the empty optional tables are nil")
	}
	if _, err := byName["RoleInfo"].load(d, src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("RoleInfo missing: %v; want not-exist error", err)
	}
}
