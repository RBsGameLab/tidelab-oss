// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Reflection.Metadata;

namespace Tidelab.ProtoGen;

/// <summary>
/// Rebuilds the protocol schema from an assembly that protoc's C# generator produced. The
/// client's copy has its descriptors stripped (<c>Descriptor => null</c>), so everything comes
/// from what the generated code itself holds:
/// <list type="bullet">
/// <item>field numbers: the <c>XxxFieldNumber</c> constants, in declaration order;</item>
/// <item>labels and value types: the property types (RepeatedField, MapField, messages, enums);</item>
/// <item>scalar encodings (sint32, fixed64, ...) of singular fields: the <c>ReadXxx</c> call whose
///   result goes straight into the property setter in the parse method;</item>
/// <item>encodings and packing of repeated and map fields: the <c>FieldCodec.ForXxx(tag)</c>
///   calls in the static constructor (the tag's wire type says packed or not);</item>
/// <item>oneofs: the nested <c>XxxOneofCase</c> enums; proto3 optional: a <c>HasXxx</c> property;</item>
/// <item>enum value names: the <c>OriginalName</c> attributes; message ids: the <c>NetMessage</c>
///   attribute and the id enum.</item>
/// </list>
/// The same reader runs on the client's assembly and on ours, which is how the tests show that
/// what we generate is the client's protocol.
/// </summary>
public sealed class SchemaReader
{
    private const string Protobuf = "Google.Protobuf.";
    private readonly Assembly asm;
    private readonly string root;
    private readonly string idEnum;
    private readonly Dictionary<string, string> rel = new(StringComparer.Ordinal);
    private readonly HashSet<string> messageTypes = new(StringComparer.Ordinal);
    private readonly HashSet<string> enumTypes = new(StringComparer.Ordinal);
    public List<string> Problems { get; } = [];

    /// <param name="root">Root namespace of the protocol types ("Aki.Protocol" in the client).</param>
    /// <param name="idEnum">Name of the message id enum in the root namespace.</param>
    public SchemaReader(Assembly asm, string root, string idEnum)
    {
        this.asm = asm;
        this.root = root;
        this.idEnum = idEnum;
    }

    public static Schema Read(string dll, string root, string idEnum, out List<string> problems)
    {
        using var asm = new Assembly(dll);
        var reader = new SchemaReader(asm, root, idEnum);
        var schema = reader.Read();
        problems = reader.Problems;
        return schema;
    }

    public Schema Read()
    {
        var schema = new Schema();
        var types = asm.Types.Where(t => InRoot(asm.FullName(t))).ToList();

        // First pass: which types are messages and which are protocol enums, and their names.
        foreach (var t in types)
        {
            string full = asm.FullName(t);
            if (IsMessage(t))
                messageTypes.Add(full);
            else if (IsProtoEnum(t))
                enumTypes.Add(full);
            else
                continue;
            rel[full] = Relative(full);
        }

        foreach (var t in types.OrderBy(t => rel.GetValueOrDefault(asm.FullName(t)), StringComparer.Ordinal))
        {
            string full = asm.FullName(t);
            if (messageTypes.Contains(full))
                schema.Messages.Add(ReadMessage(t, full));
            else if (enumTypes.Contains(full))
                schema.Enums.Add(ReadEnum(t, full));
            else if (full == root + "." + idEnum)
                foreach (var (name, _, value) in asm.Fields(t))
                    if (value is int id)
                        schema.MessageIds[name] = id;
        }
        return schema;
    }

    private bool InRoot(string full) => full.StartsWith(root + ".", StringComparison.Ordinal);

    private bool IsMessage(TypeDefinitionHandle t) =>
        asm.Interfaces(t).Any(i => i == Protobuf + "IBufferMessage");

    private bool IsProtoEnum(TypeDefinitionHandle t)
    {
        if (asm.BaseType(t) != "System.Enum")
            return false;
        var values = asm.Fields(t).Where(f => f.Name != "value__").ToList();
        return values.Count > 0 && values.All(f => OriginalName(f.Handle) is not null);
    }

    private string? OriginalName(FieldDefinitionHandle f)
    {
        foreach (var a in asm.FieldAttributes(f))
        {
            var (type, arg) = asm.Attribute(a);
            if (type == Protobuf + "Reflection.OriginalNameAttribute")
                return arg as string;
        }
        return null;
    }

    /// <summary>"Aki.Protocol.Ai.Foo/Types/Bar" → "Ai.Foo.Bar".</summary>
    private string Relative(string full) =>
        full[(root.Length + 1)..].Replace("/Types/", ".", StringComparison.Ordinal).Replace('/', '.');

    private (string Ns, string Name, string? Outer) Split(string full)
    {
        var t = full;
        int nest = t.IndexOf('/');
        string top = nest < 0 ? t : t[..nest];
        string ns = top[..top.LastIndexOf('.')];
        ns = ns.Length > root.Length ? ns[(root.Length + 1)..] : "";
        int lastNest = t.LastIndexOf("/Types/", StringComparison.Ordinal);
        string? outer = lastNest < 0 ? null : Relative(t[..lastNest]);
        string name = Relative(full);
        name = name[(name.LastIndexOf('.') + 1)..];
        return (ns, name, outer);
    }

    private EnumType ReadEnum(TypeDefinitionHandle t, string full)
    {
        var (ns, name, outer) = Split(full);
        var e = new EnumType { FullName = rel[full], Namespace = ns, Name = name, Outer = outer };
        foreach (var (_, handle, value) in asm.Fields(t))
        {
            string? original = OriginalName(handle);
            if (original is null || value is not int v)
                continue;
            // Our .proto files prefix every value with its enum's name (see ProtoWriter); the
            // client's names never start with it. Drop it so both sides read the same.
            if (original.StartsWith(name + "_", StringComparison.Ordinal))
                original = original[(name.Length + 1)..];
            e.Values.Add((original, v));
        }
        return e;
    }

    private Message ReadMessage(TypeDefinitionHandle t, string full)
    {
        var (ns, name, outer) = Split(full);
        var msg = new Message { FullName = rel[full], Namespace = ns, Name = name, Outer = outer };

        foreach (var a in asm.TypeAttributes(t))
        {
            var (type, arg) = asm.Attribute(a);
            if (type.EndsWith(".NetMessageAttribute", StringComparison.Ordinal) && arg is int id)
                msg.Id = id;
        }

        // Oneof membership: nested enum XxxOneofCase { None = 0, Member = number, ... }.
        var oneofOf = new Dictionary<int, string>();
        foreach (var n in asm.Nested(t))
        {
            string nn = asm.Name(n);
            if (!nn.EndsWith("OneofCase", StringComparison.Ordinal) || asm.BaseType(n) != "System.Enum")
                continue;
            string oneof = nn[..^"OneofCase".Length];
            msg.Oneofs.Add(oneof);
            foreach (var (_, _, value) in asm.Fields(n))
                if (value is int number && number != 0)
                    oneofOf[number] = oneof;
        }

        var props = asm.Properties(t).ToDictionary(p => p.Name, p => p.Type, StringComparer.Ordinal);
        var singular = SingularEncodings(t);
        var codecs = Codecs(t);

        foreach (var (fieldName, _, value) in asm.Fields(t))
        {
            if (!fieldName.EndsWith("FieldNumber", StringComparison.Ordinal) || value is not int number)
                continue;
            string prop = fieldName[..^"FieldNumber".Length];
            if (!props.TryGetValue(prop, out var type))
            {
                Problems.Add($"{msg.FullName}: no property {prop} for field {number}");
                continue;
            }
            msg.Fields.Add(ReadField(msg, prop, number, type, oneofOf.GetValueOrDefault(number),
                props.ContainsKey("Has" + prop), singular, codecs));
        }
        return msg;
    }

    private Field ReadField(Message msg, string prop, int number, TypeSig type, string? oneof, bool hasProperty,
        Dictionary<string, string> singular, Dictionary<int, List<(string Codec, long Tag)>> codecs)
    {
        string where = $"{msg.FullName}.{prop}";
        if (type.Name == Protobuf + "Collections.RepeatedField`1")
        {
            var f = new Field { Name = prop, Number = number, Label = Label.Repeated, Type = new(Kind.Int32) };
            if (codecs.TryGetValue(number, out var c) && c.Count == 1)
            {
                f.Type = FromCodec(c[0].Codec, type.Args[0], where);
                f.Packed = f.Type.Packable && (c[0].Tag & 7) == 2;
            }
            else
                Problems.Add($"{where}: no codec for repeated field {number}");
            return f;
        }
        if (type.Name == Protobuf + "Collections.MapField`2")
        {
            var f = new Field { Name = prop, Number = number, Label = Label.Map, Type = new(Kind.Int32) };
            if (codecs.TryGetValue(number, out var c) && c.Count == 2)
            {
                f.Key = FromCodec(c[0].Codec, type.Args[0], where);
                f.Type = FromCodec(c[1].Codec, type.Args[1], where);
            }
            else
                Problems.Add($"{where}: no codec for map field {number}");
            return f;
        }

        var label = hasProperty && oneof is null ? Label.Optional : Label.Singular;
        ValueType vt;
        if (messageTypes.Contains(type.Name))
            vt = new(Kind.Message, rel[type.Name]);
        else if (enumTypes.Contains(type.Name))
            vt = new(Kind.Enum, rel[type.Name]);
        else
        {
            singular.TryGetValue(prop, out var read);
            vt = FromScalar(type.Name, read, where);
        }
        return new Field { Name = prop, Number = number, Label = label, Type = vt, Oneof = oneof };
    }

    /// <summary>Property name → the ReadXxx method whose result the parse code stores in it.</summary>
    private Dictionary<string, string> SingularEncodings(TypeDefinitionHandle t)
    {
        string self = asm.FullName(t);
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var (_, m) in asm.Methods(t))
        {
            var il = asm.Instructions(m);
            for (int i = 0; i + 1 < il.Count; i++)
            {
                var read = il[i];
                var store = il[i + 1];
                if (read.Op is not (ILOpCode.Call or ILOpCode.Callvirt) || read.Member is null
                    || !read.Member.StartsWith("Read", StringComparison.Ordinal)
                    || read.Owner is not (Protobuf + "ParseContext" or Protobuf + "CodedInputStream"))
                    continue;
                if (store.Op is ILOpCode.Call or ILOpCode.Callvirt && store.Owner == self
                    && store.Member!.StartsWith("set_", StringComparison.Ordinal))
                    result[store.Member[4..]] = read.Member[4..];
            }
        }
        return result;
    }

    /// <summary>
    /// Field number → the FieldCodec.ForXxx calls (with their tags) that build the static codec
    /// of a repeated field (one call) or a map field (key, value). Read from the static
    /// constructor: the first integer constant after the previous codec call is the tag, and a
    /// map's own tag is the first constant before <c>new Codec(key, value, tag)</c>.
    /// </summary>
    private Dictionary<int, List<(string Codec, long Tag)>> Codecs(TypeDefinitionHandle t)
    {
        var result = new Dictionary<int, List<(string, long)>>();
        foreach (var (name, m) in asm.Methods(t))
        {
            if (name != ".cctor")
                continue;
            var calls = new List<(string, long)>();
            long? firstConst = null;
            foreach (var ins in asm.Instructions(m))
            {
                if (ins.Op == ILOpCode.Ldc_i4)
                {
                    firstConst ??= ins.Int;
                    continue;
                }
                if (ins.Op == ILOpCode.Call && ins.Owner == Protobuf + "FieldCodec" && ins.Member!.StartsWith("For", StringComparison.Ordinal))
                {
                    calls.Add((ins.Member[3..], firstConst ?? -1));
                    firstConst = null;
                    continue;
                }
                if (ins.Op == ILOpCode.Newobj && ins.Owner == Protobuf + "Collections.MapField`2/Codec")
                {
                    if (calls.Count == 2 && firstConst is long mapTag)
                        result[(int)(mapTag >> 3)] = [.. calls];
                    calls.Clear();
                    firstConst = null;
                    continue;
                }
                // A store ends a codec, except the compiler's lambda caches ("<>9__..."), which
                // ForEnum's converters store between the tag and the call.
                if (ins.Op == ILOpCode.Stsfld && !ins.Member!.StartsWith("<>", StringComparison.Ordinal))
                {
                    if (ins.Member.StartsWith("_repeated_", StringComparison.Ordinal) && calls.Count == 1)
                        result[(int)(calls[0].Item2 >> 3)] = [.. calls];
                    calls.Clear();
                    firstConst = null;
                }
            }
        }
        return result;
    }

    private ValueType FromCodec(string codec, TypeSig element, string where)
    {
        switch (codec)
        {
            case "Message":
                return messageTypes.Contains(element.Name) ? new(Kind.Message, rel[element.Name]) : Unknown(where, element.Name);
            case "Enum":
                return enumTypes.Contains(element.Name) ? new(Kind.Enum, rel[element.Name]) : Unknown(where, element.Name);
        }
        return Enum.TryParse<Kind>(codec, out var k) && k is not (Kind.Enum or Kind.Message)
            ? new(k)
            : Unknown(where, "codec " + codec);
    }

    private ValueType FromScalar(string clr, string? read, string where)
    {
        // ReadXxx names are the proto kind names (ReadSInt32, ReadFixed64, ...).
        if (read is not null && Enum.TryParse<Kind>(read, out var k) && k is not (Kind.Enum or Kind.Message))
            return new(k);
        return clr switch
        {
            "System.Int32" => new(Kind.Int32),
            "System.Int64" => new(Kind.Int64),
            "System.UInt32" => new(Kind.UInt32),
            "System.UInt64" => new(Kind.UInt64),
            "System.Single" => new(Kind.Float),
            "System.Double" => new(Kind.Double),
            "System.Boolean" => new(Kind.Bool),
            "System.String" => new(Kind.String),
            Protobuf + "ByteString" => new(Kind.Bytes),
            _ => Unknown(where, clr),
        };
    }

    private ValueType Unknown(string where, string what)
    {
        Problems.Add($"{where}: unknown type {what}");
        return new(Kind.Bytes);
    }
}
