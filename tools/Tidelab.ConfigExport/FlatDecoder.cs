// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Buffers.Binary;
using System.Text;

namespace Tidelab.ConfigExport;

/// <summary>How a decoded row becomes JSON where the FlatBuffers data leaves room for choice.</summary>
public sealed class ExportRules
{
    /// <summary>A string field whose text is a JSON object or array is written as that value
    /// (the client parses these, e.g. FlowState.Actions, AreaFunction.Data). Strings that are
    /// vector elements or map values stay text (BossRushBuff.DescriptionParam).</summary>
    public bool ParseJsonStrings { get; init; } = true;

    /// <summary>64-bit integers are written as decimal strings (JavaScript reads them as BigInt,
    /// which JSON cannot hold).</summary>
    public bool LongsAsStrings { get; init; } = true;
}

/// <summary>Decodes a FlatBuffers buffer into JSON values by the schema read from the client.
/// Absent fields get their FlatBuffers defaults: 0, false, "", [] and {}; an absent table null.</summary>
public sealed class FlatDecoder(FlatSchema schema, ExportRules rules)
{
    public JObject DecodeRoot(byte[] buf, string root)
    {
        int pos = I32(buf, 0);
        return DecodeTable(buf, pos, schema.Tables[root]);
    }

    private object Long(long v) => rules.LongsAsStrings ? v.ToString(System.Globalization.CultureInfo.InvariantCulture) : v;

    private JObject DecodeTable(byte[] buf, int pos, TableType type, bool parseJson = true)
    {
        var o = new JObject();
        int vt = pos - I32(buf, pos);
        int vtSize = U16(buf, vt);
        foreach (var f in type.Fields)
        {
            int off = f.Slot < vtSize ? U16(buf, vt + f.Slot) : 0;
            o.Add(f.Name, off == 0 ? Absent(f) : Present(buf, pos + off, f, parseJson && rules.ParseJsonStrings));
        }
        return o;
    }

    private object? Absent(Field f)
    {
        if (f.Map) return new JObject();
        if (f.Vector) return new JArray();
        if (f.Default is { } d)
            return f.Kind == Kind.Long && d is long l ? Long(l) : d;
        return f.Kind switch
        {
            Kind.Bool => false,
            Kind.String => "",
            Kind.Table => null,
            _ => 0L,
        };
    }

    private object? Present(byte[] buf, int at, Field f, bool parseJson)
    {
        if (!f.Vector)
            return Scalar(buf, at, f.Kind, f.Table, parseJson);
        int vec = at + I32(buf, at);
        int n = I32(buf, vec);
        int size = Size(f.Kind);
        if (f.Map)
        {
            var map = new JObject();
            var entry = schema.Tables[f.Table!];
            for (int j = 0; j < n; j++)
            {
                var kv = DecodeTable(buf, vec + 4 + j * 4 + I32(buf, vec + 4 + j * 4), entry, parseJson: false);
                var key = kv.Find(e => e.Key == "Key").Value;
                var value = kv.Find(e => e.Key == "Value").Value;
                if (entry.Fields.All(e => e.Name != "Key") || entry.Fields.All(e => e.Name != "Value"))
                    throw new InvalidDataException($"{f.Name}: map entry {f.Table} has no Key/Value");
                map.Set(KeyText(key), value);
            }
            return map;
        }
        var a = new JArray();
        for (int j = 0; j < n; j++)
            a.Add(Scalar(buf, vec + 4 + j * size, f.Kind, f.Table, parseJson && f.Kind == Kind.Table));
        return a;
    }

    private static string KeyText(object? key) => key switch
    {
        string s => s,
        long l => l.ToString(System.Globalization.CultureInfo.InvariantCulture),
        bool b => b ? "true" : "false",
        double d => JsonText.Number(d),
        _ => "null",
    };

    private static int Size(Kind k) => k switch
    {
        Kind.Bool or Kind.Byte or Kind.SByte => 1,
        Kind.Short => 2,
        Kind.Long or Kind.Double => 8,
        _ => 4,
    };

    private object? Scalar(byte[] buf, int at, Kind kind, string? table, bool parseJson)
    {
        switch (kind)
        {
            case Kind.Bool: return buf[at] != 0;
            case Kind.Byte: return (long)buf[at];
            case Kind.SByte: return (long)(sbyte)buf[at];
            case Kind.Short: return (long)BinaryPrimitives.ReadInt16LittleEndian(buf.AsSpan(at));
            case Kind.Int: return (long)I32(buf, at);
            case Kind.Long: return Long(BinaryPrimitives.ReadInt64LittleEndian(buf.AsSpan(at)));
            case Kind.Float: return JsonText.Integral(BinaryPrimitives.ReadSingleLittleEndian(buf.AsSpan(at)));
            case Kind.Double: return JsonText.Integral(BinaryPrimitives.ReadDoubleLittleEndian(buf.AsSpan(at)));
            case Kind.String:
            {
                int s = at + I32(buf, at);
                string text = Encoding.UTF8.GetString(buf, s + 4, I32(buf, s));
                if (parseJson && text.Length > 0 && (text[0] == '{' || text[0] == '['))
                {
                    var parsed = JsonText.Parse(text, out bool ok);
                    if (ok) return parsed;
                }
                return text;
            }
            case Kind.Table:
                return DecodeTable(buf, at + I32(buf, at), schema.Tables[table!], parseJson);
            default:
                throw new InvalidDataException($"kind {kind}");
        }
    }

    private static int I32(byte[] b, int at) => BinaryPrimitives.ReadInt32LittleEndian(b.AsSpan(at));
    private static int U16(byte[] b, int at) => BinaryPrimitives.ReadUInt16LittleEndian(b.AsSpan(at));
}
