// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using Google.FlatBuffers;
using Tidelab.ConfigExport;

namespace Tidelab.Tests.Data;

public class ConfigExportTests
{
    // What JavaScript's String(x) gives for each (checked in node).
    [Theory]
    [InlineData(0.0, "0")]
    [InlineData(1.5, "1.5")]
    [InlineData(100.0, "100")]
    [InlineData(-39000.0, "-39000")]
    [InlineData(0.1f, "0.10000000149011612")]
    [InlineData(5.927801680627454e-7, "5.927801680627454e-7")]
    [InlineData(1e-7, "1e-7")]
    [InlineData(0.000001, "0.000001")]
    [InlineData(123e-20, "1.23e-18")]
    [InlineData(1e21, "1e+21")]
    [InlineData(123456789012345680000.0, "123456789012345680000")]
    [InlineData(double.NaN, "null")]
    public void NumbersAreWrittenAsJavaScriptDoes(double value, string text) =>
        Assert.Equal(text, JsonText.Number(value));

    [Fact]
    public void NegativeZeroIsZero() => Assert.Equal("0", JsonText.Number(-0.0));

    [Fact]
    public void JsonTextIsJsonStringifyWithTwoSpaces()
    {
        var o = new JObject { { "Id", 1L }, { "Name", "古\"\\\n\u0001" }, { "Empty", new JArray() }, { "Map", new JObject() },
            { "List", new JArray { 1L, 0.25, true, null } } };
        Assert.Equal("{\n  \"Id\": 1,\n  \"Name\": \"古\\\"\\\\\\n\\u0001\",\n  \"Empty\": [],\n  \"Map\": {},\n  \"List\": [\n    1,\n    0.25,\n    true,\n    null\n  ]\n}",
            JsonText.ToText(o));
    }

    [Fact]
    public void JsonParseKeepsTheFirstOfARepeatedKey()
    {
        var v = (JObject)JsonText.Parse("""{"a":1,"b":2.5,"a":"x"}""", out bool ok)!;
        Assert.True(ok);
        Assert.Equal(["a", "b"], v.Select(kv => kv.Key));
        Assert.Equal(1L, v[0].Value);
        Assert.Equal(2.5, v[1].Value);
        Assert.Null(JsonText.Parse("{\"a\":1} tail", out ok));
        Assert.False(ok);
    }

    // Minified flatc JavaScript as the client ships it (the shapes JsSchema knows).
    private static readonly Dictionary<string, string> Js = new()
    {
        ["Config/Thing.js"] = """
            "use strict";Object.defineProperty(exports,"__esModule",{value:!0}),exports.Thing=void 0;const GameUtils_1=require("../../../Game/GameUtils"),Kind_1=require("./SubType/Kind"),Vec_1=require("./SubType/Vec"),DicIntString_1=require("./SubType/DicIntString");class Thing{constructor(){this.J7=null,this.z7=0}get Id(){return this.id()}get Level(){return this.level()}get bShow(){return this.bshow()}get Visible(){return this.visible()}get Ratio(){return this.ratio()}get Big(){return this.big()}get Name(){return this.name()}get Kind(){return this.kind()}get Pos(){return this.pos()}get Ids(){return GameUtils_1.GameUtils.ConvertToArray(this.idsLength(),this.ids,this)}get Points(){return GameUtils_1.GameUtils.ConvertToArray(this.pointsLength(),this.points,this)}get Names(){return GameUtils_1.GameUtils.ConvertToMap(this.namesLength(),this.namesKey,this.namesValue,this)}namesKey(t){return this.names(t)?.key()}namesValue(t){return this.names(t)?.value()}__init(t,i){return this.z7=t,this.J7=i,this}static getRootAsThing(t,i){return(i||new Thing).__init(t.readInt32(t.position())+t.position(),t)}id(){var t=this.J7.__offset(this.z7,4);return t?this.J7.readInt32(this.z7+t):0}level(){var t=this.J7.__offset(this.z7,6);return t?this.J7.readInt32(this.z7+t):-1}bshow(){var t=this.J7.__offset(this.z7,8);return!!t&&!!this.J7.readInt8(this.z7+t)}visible(){var t=this.J7.__offset(this.z7,10);return!t||!!this.J7.readInt8(this.z7+t)}ratio(){var t=this.J7.__offset(this.z7,12);return t?this.J7.readFloat32(this.z7+t):.25}big(){var t=this.J7.__offset(this.z7,14);return t?this.J7.readInt64(this.z7+t):BigInt("-7")}name(t){var i=this.J7.__offset(this.z7,16),i=i?this.J7.__string(this.z7+i,t):null;return"string"==typeof i&&GameUtils_1.GameUtils.IsOptimizeDbString&&GameUtils_1.GameUtils.InternalizedString(i),i}kind(){var t=this.J7.__offset(this.z7,18);return t?this.J7.readInt8(this.z7+t):Kind_1.Kind.Wall}pos(t){var i=this.J7.__offset(this.z7,20);return i?(t||new Vec_1.Vec).__init(this.J7.__indirect(this.z7+i),this.J7):null}GetIdsAt(t){return this.ids(t)}ids(t){var i=this.J7.__offset(this.z7,22);return i?this.J7.readInt64(this.J7.__vector(this.z7+i)+8*t):BigInt(0)}idsLength(){var t=this.J7.__offset(this.z7,22);return t?this.J7.__vector_len(this.z7+t):0}points(t,i){var s=this.J7.__offset(this.z7,24);return s?(i||new Vec_1.Vec).__init(this.J7.__indirect(this.J7.__vector(this.z7+s)+4*t),this.J7):null}pointsLength(){var t=this.J7.__offset(this.z7,24);return t?this.J7.__vector_len(this.z7+t):0}names(t,i){var s=this.J7.__offset(this.z7,26);return s?(i||new DicIntString_1.DicIntString).__init(this.J7.__indirect(this.J7.__vector(this.z7+s)+4*t),this.J7):null}namesLength(){var t=this.J7.__offset(this.z7,26);return t?this.J7.__vector_len(this.z7+t):0}}exports.Thing=Thing;
            """,
        ["Config/SubType/Vec.js"] = """
            "use strict";Object.defineProperty(exports,"__esModule",{value:!0}),exports.Vec=void 0;class Vec{constructor(){this.J7=null,this.z7=0}get X(){return this.x()}get Y(){return this.y()}__init(t,i){return this.z7=t,this.J7=i,this}x(){var t=this.J7.__offset(this.z7,4);return t?this.J7.readFloat32(this.z7+t):0}y(){var t=this.J7.__offset(this.z7,6);return t?this.J7.readFloat32(this.z7+t):0}}exports.Vec=Vec;
            """,
        ["Config/SubType/DicIntString.js"] = """
            "use strict";Object.defineProperty(exports,"__esModule",{value:!0}),exports.DicIntString=void 0;class DicIntString{constructor(){this.J7=null,this.z7=0}__init(t,i){return this.z7=t,this.J7=i,this}key(){var t=this.J7.__offset(this.z7,4);return t?this.J7.readInt32(this.z7+t):0}value(t){var i=this.J7.__offset(this.z7,6),i=i?this.J7.__string(this.z7+i,t):null;return i}}exports.DicIntString=DicIntString;
            """,
        ["Config/SubType/Kind.js"] = """
            "use strict";var Kind;Object.defineProperty(exports,"__esModule",{value:!0}),exports.Kind=void 0,function(e){e[e.Plain=0]="Plain",e[e.Wall=1]="Wall"}(Kind=exports.Kind||(exports.Kind={}));
            """,
        ["Config/ConfigDefineBase.js"] = """
            "use strict";Object.defineProperty(exports,"__esModule",{value:!0});
            """,
        ["ConfigQuery/ThingById.js"] = """
            "use strict";const Thing_1=require("../Config/Thing"),DB="db_thing.db",FILE="t.xlsx",TABLE="Thing",COMMAND="select BinData from `Thing` where Id=?";exports.configThingById={GetConfig:(o)=>{return Thing_1.Thing.getRootAsThing(new byte_buffer_1.ByteBuffer(o))}};
            """,
        ["ConfigQuery/ThingLang.js"] = """
            "use strict";const DB="lang_thing.db",TABLE="ThingLang";exports.configThingLang={};
            """,
    };

    private static FlatSchema ReadFixture(out TempDirectory dir)
    {
        dir = new TempDirectory();
        foreach (var (path, text) in Js)
            dir.Write(path, text.Trim());
        return JsSchema.Read(dir.Path);
    }

    [Fact]
    public void JsSchemaReadsEveryAccessorShape()
    {
        var schema = ReadFixture(out var dir);
        using var _ = dir;
        Assert.Empty(schema.Problems);
        Assert.Equal(new ConfigTable("Thing", "db_thing.db", "Thing"), Assert.Single(schema.Configs));
        Assert.Contains(schema.Skipped, s => s.StartsWith("ThingLang"));
        Assert.Contains(schema.Skipped, s => s.StartsWith("ConfigDefineBase"));
        var f = schema.Tables["Thing"].Fields.ToDictionary(x => x.Name);
        Assert.Equal(["Id", "Level", "bShow", "Visible", "Ratio", "Big", "Name", "Kind", "Pos", "Ids", "Points", "Names"],
            schema.Tables["Thing"].Fields.Select(x => x.Name));
        Assert.Equal((Kind.Int, 6, (object)-1L), (f["Level"].Kind, f["Level"].Slot, f["Level"].Default!));
        Assert.Equal((Kind.Bool, (object)false), (f["bShow"].Kind, f["bShow"].Default!));
        Assert.Equal((Kind.Bool, (object)true), (f["Visible"].Kind, f["Visible"].Default!));
        Assert.Equal((Kind.Float, (object)0.25), (f["Ratio"].Kind, f["Ratio"].Default!));
        Assert.Equal((Kind.Long, (object)-7L), (f["Big"].Kind, f["Big"].Default!));
        Assert.Equal((Kind.SByte, (object)1L), (f["Kind"].Kind, f["Kind"].Default!));
        Assert.Equal((Kind.Table, "Vec", false), (f["Pos"].Kind, f["Pos"].Table, f["Pos"].Vector));
        Assert.Equal((Kind.Long, true), (f["Ids"].Kind, f["Ids"].Vector));
        Assert.Equal((Kind.Table, "Vec", true, false), (f["Points"].Kind, f["Points"].Table, f["Points"].Vector, f["Points"].Map));
        Assert.Equal((Kind.Table, "DicIntString", true, true), (f["Names"].Kind, f["Names"].Table, f["Names"].Vector, f["Names"].Map));
        Assert.Equal(["Key", "Value"], schema.Tables["DicIntString"].Fields.Select(x => x.Name));
    }

    private static byte[] BuildThing(bool full)
    {
        var b = new FlatBufferBuilder(256);
        int Vec(float x, float y)
        {
            b.StartTable(2);
            b.AddFloat(0, x, 0);
            b.AddFloat(1, y, 0);
            return b.EndTable();
        }
        int Entry(int key, string value)
        {
            var s = b.CreateString(value);
            b.StartTable(2);
            b.AddInt(0, key, 0);
            b.AddOffset(1, s.Value, 0);
            return b.EndTable();
        }
        if (!full)
        {
            b.StartTable(12);
            b.AddInt(0, 9, 0);
            b.Finish(b.EndTable());
            return b.SizedByteArray();
        }
        var name = b.CreateString("""{"Type":"Flow","Ids":[1,2.5]}""");
        var pos = Vec(1.5f, 0.1f);
        b.StartVector(8, 2, 8);
        b.AddLong(9007199254740993); // vectors are built back to front
        b.AddLong(-1);
        var ids = b.EndVector();
        int p1 = Vec(1, 2), p2 = Vec(3, 4);
        b.StartVector(4, 2, 4);
        b.AddOffset(p2);
        b.AddOffset(p1);
        var points = b.EndVector();
        int e1 = Entry(3, "[19,23]"), e2 = Entry(1, "one");
        b.StartVector(4, 2, 4);
        b.AddOffset(e2);
        b.AddOffset(e1);
        var names = b.EndVector();
        b.StartTable(12);
        b.AddInt(0, 106800000, 0);
        b.AddInt(1, 0, -1);
        b.ForceDefaults = true;
        b.AddBool(2, true, false);
        b.AddBool(3, false, true);
        b.ForceDefaults = false;
        b.AddFloat(4, 0.1f, 0.25f);
        b.AddLong(5, 57158396800000001, -7);
        b.AddOffset(6, name.Value, 0);
        b.AddSbyte(7, 0, 1);
        b.AddOffset(8, pos, 0);
        b.AddOffset(9, ids.Value, 0);
        b.AddOffset(10, points.Value, 0);
        b.AddOffset(11, names.Value, 0);
        b.Finish(b.EndTable());
        return b.SizedByteArray();
    }

    [Fact]
    public void DecoderWritesRowsAsTheBinDataJsonHasThem()
    {
        var schema = ReadFixture(out var dir);
        using var _ = dir;
        var decoder = new FlatDecoder(schema, new ExportRules());
        Assert.Equal("""
            {
              "Id": 106800000,
              "Level": 0,
              "bShow": true,
              "Visible": false,
              "Ratio": 0.10000000149011612,
              "Big": "57158396800000001",
              "Name": {
                "Type": "Flow",
                "Ids": [
                  1,
                  2.5
                ]
              },
              "Kind": 0,
              "Pos": {
                "X": 1.5,
                "Y": 0.10000000149011612
              },
              "Ids": [
                "-1",
                "9007199254740993"
              ],
              "Points": [
                {
                  "X": 1,
                  "Y": 2
                },
                {
                  "X": 3,
                  "Y": 4
                }
              ],
              "Names": {
                "3": "[19,23]",
                "1": "one"
              }
            }
            """.Replace("\r\n", "\n"), JsonText.ToText(decoder.DecodeRoot(BuildThing(full: true), "Thing")));
        // Absent fields: the schema's defaults, "" for strings, null for tables, [] and {}.
        Assert.Equal("""{"Id":9,"Level":-1,"bShow":false,"Visible":true,"Ratio":0.25,"Big":"-7","Name":"","Kind":1,"Pos":null,"Ids":[],"Points":[],"Names":{}}""",
            JsonText.ToText(decoder.DecodeRoot(BuildThing(full: false), "Thing")).Replace("\n", "").Replace(" ", ""));
    }

    [Fact]
    public void SchemaCheckFindsADifferentSlot()
    {
        var a = ReadFixture(out var d1);
        var b = ReadFixture(out var d2);
        using var _1 = d1;
        using var _2 = d2;
        Assert.Empty(SchemaCheck.Compare(a, b));
        var fields = b.Tables["Vec"].Fields.ToList();
        fields[1] = fields[1] with { Slot = 8, Default = 1L };
        b.Tables["Vec"] = new TableType("Vec", fields);
        Assert.Equal(["Vec: slot 6 (Y) only in C#", "Vec: slot 8 (Y) only in JS"], SchemaCheck.Compare(a, b));
    }

    private static string RepoFile(params string[] parts)
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
            if (File.Exists(Path.Combine(dir.FullName, "specs", "data-tables.tsv")))
                return Path.Combine([dir.FullName, .. parts]);
        throw new FileNotFoundException("specs/data-tables.tsv (the repository root) not found above " + AppContext.BaseDirectory);
    }

    private static string FirstDifference(string want, string got)
    {
        var w = want.Split('\n');
        var g = got.Split('\n');
        for (int i = 0; i < Math.Max(w.Length, g.Length); i++)
        {
            string wl = i < w.Length ? w[i] : "<EOF>";
            string gl = i < g.Length ? g[i] : "<EOF>";
            if (wl != gl)
                return $"tables_gen.go differs at line {i + 1}:\n  want: {wl}\n  got : {gl}";
        }
        return $"tables_gen.go length differs: want {want.Length}, got {got.Length}";
    }

    [Fact]
    public void RowGenWritesGoRowTypesMasksAndDataTables()
    {
        var schema = ReadFixture(out var defineDir);
        using var _1 = defineDir;
        using var binDir = new TempDirectory();
        binDir.Write("Thing.json", """[{"Id":1,"bShow":true,"Ratio":0.1,"Big":"7","Name":{"Type":"Flow","Ids":[1,2]},"Pos":{"X":1,"Y":2},"Ids":["1"],"Points":[{"X":1,"Y":2}],"Names":{"3":"a"}}]""");
        binDir.Write("tables.tsv", string.Join('\n',
        [
            "table\tfile\tkey\tpath\tshape\tpresence",
            "Thing\tThing.json\tId\tId\tint32\trequired",
            "Thing\tThing.json\tId\tbShow\tbool\tdefault",
            "Thing\tThing.json\tId\tRatio\tfloat64\tdefault",
            "Thing\tThing.json\tId\tBig\tint64\tdefault",
            "Thing\tThing.json\tId\tName\tany\tdefault",
            "Thing\tThing.json\tId\tName.Type\tstring\tdefault",
            "Thing\tThing.json\tId\tName.Ids[1]\tint32\telement",
            "Thing\tThing.json\tId\tPos.X\tfloat32\tdefault",
            "Thing\tThing.json\tId\tIds[]\tint64\telement",
            "Thing\tThing.json\tId\tPoints[].Y\tfloat32\tdefault",
            "Thing\tThing.json\tId\tNames{}\tstring\telement",
            "Extra\tExtra.json\tKey\tKey\tstring\trequired",
            "Extra\tExtra.json\tKey\tFields\tint32\tdefault",
            "Extra\tExtra.json\tKey\ttype\tstring\tdefault",
            "Extra\tExtra.json\tKey\tRaw\tany\tdefault",
            "Extra\tExtra.json\tKey\tBuckets\tmap(int32->list)\tdefault",
            "Extra\tExtra.json\tKey\tBuckets{}\tlist\telement",
            "Extra\tExtra.json\tKey\tBuckets{}[]\tint64\telement",
            "LevelEntityConfig\tLevelEntityConfig.json\tEntityId\tMapId\tint32\trequired",
            "LevelEntityConfig\tLevelEntityConfig.json\tEntityId\tEntityId\tint32\trequired",
            "CharacterInitInfo_ConcomitantBuffsEntry\tLong.json\tKey\tKey\tint32\trequired",
        ]));
        var tsv = TsvSchema.Read(Path.Combine(binDir.Path, "tables.tsv"));
        var gen = new RowGen(schema, binDir.Path);
        string cs = gen.Generate(tsv);
        string go = gen.GenerateGo(tsv);
        Assert.Empty(gen.Problems);
        Assert.NotEmpty(cs);
        Assert.DoesNotContain("\r", go);
        Assert.Contains("// Code generated by tools/Tidelab.ConfigExport (configexport gen). DO NOT EDIT.\n\npackage data\n\nimport \"time\"\n\n", go);
        // Long type name (>32 chars) wraps TypeName() onto 3 lines and does not pad IgnoresUnknownFields().
        Assert.Contains("""
            func (*CharacterInitInfo_ConcomitantBuffsEntry) TypeName() string {
            	return "CharacterInitInfo_ConcomitantBuffsEntry"
            }
            func (*CharacterInitInfo_ConcomitantBuffsEntry) IgnoresUnknownFields() bool { return true }
            """.Replace("\r\n", "\n"), go);
        // Reserved method name (Fields) and Go keyword (type) get a trailing underscore.
        Assert.Contains("""
            type Extra struct {
            	Key     string
            	Fields_ int32
            	type_   string
            	Raw     Value
            	Buckets *Map[int32, []int64]
            }

            func (*Extra) TypeName() string           { return "Extra" }
            func (*Extra) IgnoresUnknownFields() bool { return true }
            func (r *Extra) Fields(v Visitor) {
            	F(v, "Key", &r.Key, StringC)
            	F(v, "Fields", &r.Fields_, IntC)
            	F(v, "type", &r.type_, StringC)
            	F(v, "Raw", &r.Raw, JSONC)
            	F(v, "Buckets", &r.Buckets, MapOf(IntC, ArrayOf(LongC)))
            }
            """.Replace("\r\n", "\n"), go);
        // Widened float64, masked JSON, vectors, nested tables, maps, and mask literal.
        Assert.Contains("\tF(v, \"Ratio\", &r.Ratio, FloatAsDoubleC)\n", go);
        Assert.Contains("\tF(v, \"Name\", &r.Name, Masked(Thing_Name_Mask))\n", go);
        Assert.Contains("\tF(v, \"Pos\", &r.Pos, TableOf[Vec]())\n", go);
        Assert.Contains("\tF(v, \"Points\", &r.Points, ArrayOf(TableOf[Vec]()))\n", go);
        Assert.Contains("\tF(v, \"Names\", &r.Names, MapOf(IntC, StringC))\n", go);
        Assert.Contains("var Thing_Name_Mask = ParseMask(`\n        .Ids[]\n        .Type\n        `)\n\n", go);
        // ByHand LevelEntityConfig omits the map index and calls indexLevelEntityConfig().
        Assert.DoesNotContain("LevelEntityConfigByEntityId", go);
        Assert.Contains("\ttableSlot[LevelEntityConfig](\"LevelEntityConfig\", func(d *DataTables) []*LevelEntityConfig { return d.LevelEntityConfig }, func(d *DataTables, rows []*LevelEntityConfig) { d.LevelEntityConfig = rows; d.indexLevelEntityConfig() }),\n", go);
    }

    [Fact]
    public void Lab38GoRowTypesMatchCommittedTablesGen()
    {
        // Optional local 3.8 data check (skipped when absent or tables_gen.go has not been generated).
        string? data = Environment.GetEnvironmentVariable("TIDELAB_GAMEDATA");
        string repo = Path.GetFullPath(Path.Combine(RepoFile("specs"), ".."));
        if (string.IsNullOrWhiteSpace(data))
        {
            string localFile = Path.Combine(repo, "tidelab.local.txt");
            if (File.Exists(localFile))
            {
                foreach (var raw in File.ReadAllLines(localFile))
                {
                    var line = raw.Trim();
                    if (line.Length == 0 || line.StartsWith('#') || !line.Contains('=')) continue;
                    var parts = line.Split('=', 2);
                    var key = parts[0].Trim().ToLowerInvariant();
                    var val = parts[1].Trim();
                    if (key == "gamedata") data = val;
                    else if (key == "lab" && string.IsNullOrWhiteSpace(data)) data = Path.Combine(val, "gamedata", "3.8");
                }
            }
        }
        if (string.IsNullOrWhiteSpace(data))
            data = Path.GetFullPath(Path.Combine(repo, "..", "tidelab-data", "gamedata", "3.8"));
        string defineDir = Path.Combine(data, "Define");
        string binDataDir = Path.Combine(data, "BinData");
        string committedPath = RepoFile("server", "internal", "data", "tables_gen.go");
        if (!Directory.Exists(defineDir) || !Directory.Exists(binDataDir) || !File.Exists(committedPath))
            return;
        var schema = JsSchema.Read(defineDir);
        Assert.Empty(schema.Problems);
        var tsv = TsvSchema.Read(RepoFile("specs", "data-tables.tsv"));
        var gen = new RowGen(schema, binDataDir);
        string generated = gen.GenerateGo(tsv);
        Assert.Empty(gen.Problems);
        string committed = File.ReadAllText(committedPath).Replace("\r\n", "\n");
        Assert.True(generated == committed, FirstDifference(committed, generated));
    }
}
