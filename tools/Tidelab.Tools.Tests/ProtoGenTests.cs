// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using Tidelab.ProtoGen;
using Tidelab.Tests.Fixture;

namespace Tidelab.Tests.Protocol;

/// <summary>The generator's reader, on code protoc generated from a .proto we wrote by hand
/// (fixture.proto): every shape the client's protocol uses reads back as written.</summary>
public class ProtoGenTests
{
    private static Schema ReadFixture()
    {
        var schema = SchemaReader.Read(typeof(Everything).Assembly.Location, "Tidelab.Tests.Fixture", "Ids", out var problems);
        Assert.Empty(problems);
        return schema;
    }

    [Fact]
    public void ReaderRecoversEveryFieldShapeOfTheFixture()
    {
        string[] expected =
        [
            "id\tPing\t7",
            "message\tEverything\t-",
            "message\tEverything.Inner\t-",
            "message\tLeaf\t-",
            "message\tPing\t7",
            "field\tEverything\t00001\tA32\tsingular\tint32",
            "field\tEverything\t00002\tA64\tsingular\tint64",
            "field\tEverything\t00003\tU32\tsingular\tuint32",
            "field\tEverything\t00004\tU64\tsingular\tuint64",
            "field\tEverything\t00005\tS32\tsingular\tsint32",
            "field\tEverything\t00006\tS64\tsingular\tsint64",
            "field\tEverything\t00007\tF32\tsingular\tfixed32",
            "field\tEverything\t00008\tF64\tsingular\tfixed64",
            "field\tEverything\t00009\tSf32\tsingular\tsfixed32",
            "field\tEverything\t00010\tSf64\tsingular\tsfixed64",
            "field\tEverything\t00011\tFl\tsingular\tfloat",
            "field\tEverything\t00012\tDb\tsingular\tdouble",
            "field\tEverything\t00013\tFlag\tsingular\tbool",
            "field\tEverything\t00014\tText\tsingular\tstring",
            "field\tEverything\t00015\tBlob\tsingular\tbytes",
            "field\tEverything\t00016\tColor\tsingular\tColor",
            "field\tEverything\t00017\tLeaf\tsingular\tLeaf",
            "field\tEverything\t00018\tMaybe\toptional\tint32",
            "field\tEverything\t00019\tMaybeColor\toptional\tColor",
            "field\tEverything\t00020\tPackedInts\trepeated\tint32",
            "field\tEverything\t00021\tPackedSInts\trepeated\tsint32",
            "field\tEverything\t00022\tLooseInts\trepeated\tint64\tunpacked",
            "field\tEverything\t00023\tTexts\trepeated\tstring",
            "field\tEverything\t00024\tLeaves\trepeated\tLeaf",
            "field\tEverything\t00025\tColors\trepeated\tColor",
            "field\tEverything\t00026\tCounts\tmap\tint32\tkey=int32",
            "field\tEverything\t00027\tLeafByName\tmap\tLeaf\tkey=string",
            "field\tEverything\t00028\tColorById\tmap\tColor\tkey=int64",
            "field\tEverything\t00030\tPickedLeaf\tsingular\tLeaf\toneof=Choice",
            "field\tEverything\t00031\tPickedNumber\tsingular\tsint32\toneof=Choice",
            "field\tEverything\t00032\tPickedText\tsingular\tstring\toneof=Choice",
            "field\tEverything\t00300\tInner\tsingular\tEverything.Inner",
            "field\tEverything\t00301\tMood\tsingular\tEverything.Inner.Mood",
            "field\tEverything.Inner\t00001\tTick\tsingular\tfixed32",
            "field\tLeaf\t00001\tValue\tsingular\tint32",
            "field\tPing\t00001\tSeq\tsingular\tint32",
            "enum\tColor",
            "value\tColor\t00000000000\tRed",
            "value\tColor\t00000000001\tGreen",
            "value\tColor\t00000000005\tBlue",
            "enum\tEverything.Inner.Mood",
            "value\tEverything.Inner.Mood\t00000000000\tCalm",
            "value\tEverything.Inner.Mood\t00000000002\tAngry",
        ];
        Array.Sort(expected, StringComparer.Ordinal);
        Assert.Equal(expected, SchemaTable.Lines(ReadFixture()));
    }

    [Fact]
    public void WriterReproducesTheFixtureProto()
    {
        var file = Assert.Single(new ProtoWriter(ReadFixture()).Write());
        string text = file.Value;
        Assert.Contains("  repeated int64 looseInts = 22 [packed = false];\n", text);
        Assert.Contains("  repeated sint32 packedSInts = 21;\n", text);
        Assert.Contains("  optional Color maybeColor = 19;\n", text);
        Assert.Contains("  map<string, Leaf> leafByName = 27;\n", text);
        Assert.Contains("  oneof choice {\n    Leaf pickedLeaf = 30;\n    sint32 pickedNumber = 31;\n    string pickedText = 32;\n  }\n", text);
        Assert.Contains("  Everything.Inner.Mood mood = 301;\n", text);
        Assert.Contains("  message Inner {\n    fixed32 tick = 1;\n\n    enum Mood {\n      Mood_Calm = 0;\n      Mood_Angry = 2;\n    }\n  }\n", text);
        Assert.Contains("enum Color {\n  Color_Red = 0;\n  Color_Green = 1;\n  Color_Blue = 5;\n}\n", text);
    }

    [Fact]
    public void ZeroValueGoesFirstAndAliasesAreAllowed()
    {
        var schema = new Schema();
        var e = new EnumType { FullName = "E", Namespace = "", Name = "E" };
        e.Values.AddRange([("One", 1), ("Zero", 0), ("Uno", 1)]);
        schema.Enums.Add(e);
        var writer = new ProtoWriter(schema);
        string text = Assert.Single(writer.Write()).Value;
        Assert.Empty(writer.Problems);
        Assert.Contains("enum E {\n  option allow_alias = true;\n  E_Zero = 0;\n  E_One = 1;\n  E_Uno = 1;\n}\n", text);
    }

    [Fact]
    public void EnumWithoutZeroIsAProblem()
    {
        var schema = new Schema();
        var e = new EnumType { FullName = "E", Namespace = "", Name = "E" };
        e.Values.Add(("One", 1));
        schema.Enums.Add(e);
        var writer = new ProtoWriter(schema);
        writer.Write();
        Assert.Contains("enum E has no zero value", writer.Problems);
    }

    [Fact]
    public void NamespacesThatUseEachOtherAreSplitIntoLayers()
    {
        // Root.A uses Sub.B, Sub.B uses Root.C: root and sub may not import each other, so
        // A goes to a second root file above Sub's.
        var schema = new Schema();
        Message M(string ns, string name, string? uses = null)
        {
            var m = new Message { FullName = ns.Length == 0 ? name : $"{ns}.{name}", Namespace = ns, Name = name };
            if (uses is not null)
                m.Fields.Add(new Field { Name = "X", Number = 1, Label = Label.Singular, Type = new(Kind.Message, uses) });
            schema.Messages.Add(m);
            return m;
        }
        M("", "A", "Sub.B");
        M("Sub", "B", "C");
        M("", "C");
        var writer = new ProtoWriter(schema);
        var files = writer.Write();
        Assert.Empty(writer.Problems);
        Assert.Equal(["aki_protocol.1.proto", "aki_protocol.proto", "aki_protocol_sub.proto"], files.Keys);
        Assert.Contains("message C {", files["aki_protocol.proto"]);
        Assert.Contains("import \"aki_protocol.proto\";", files["aki_protocol_sub.proto"]);
        Assert.Contains("  .aki.protocol.C x = 1;", files["aki_protocol_sub.proto"]);
        Assert.Contains("import \"aki_protocol_sub.proto\";", files["aki_protocol.1.proto"]);
        Assert.Contains("  .aki.protocol.sub.B x = 1;", files["aki_protocol.1.proto"]);
    }

    [Fact]
    public void LoopAcrossNamespacesIsAProblem()
    {
        var schema = new Schema();
        void M(string ns, string name, string uses)
        {
            var m = new Message { FullName = ns.Length == 0 ? name : $"{ns}.{name}", Namespace = ns, Name = name };
            m.Fields.Add(new Field { Name = "X", Number = 1, Label = Label.Singular, Type = new(Kind.Message, uses) });
            schema.Messages.Add(m);
        }
        M("", "A", "Sub.B");
        M("Sub", "B", "A");
        var writer = new ProtoWriter(schema);
        writer.Write();
        Assert.Contains("types in different namespaces use each other: A, Sub.B", writer.Problems);
    }

    [Fact]
    public void NestedTypeOfTheSameNameShadowsTheTopLevelOne()
    {
        // Inside Outer, "Leaf" would mean Outer.Leaf, so the top-level Leaf is written in full.
        var schema = new Schema();
        schema.Messages.Add(new Message { FullName = "Leaf", Namespace = "", Name = "Leaf" });
        schema.Messages.Add(new Message { FullName = "Outer.Leaf", Namespace = "", Name = "Leaf", Outer = "Outer" });
        var outer = new Message { FullName = "Outer", Namespace = "", Name = "Outer" };
        outer.Fields.Add(new Field { Name = "Top", Number = 1, Label = Label.Singular, Type = new(Kind.Message, "Leaf") });
        outer.Fields.Add(new Field { Name = "Mine", Number = 2, Label = Label.Singular, Type = new(Kind.Message, "Outer.Leaf") });
        schema.Messages.Add(outer);
        string text = Assert.Single(new ProtoWriter(schema).Write()).Value;
        Assert.Contains("  .aki.protocol.Leaf top = 1;\n", text);
        Assert.Contains("  Outer.Leaf mine = 2;\n", text);
    }
}
