// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Globalization;
using System.Text.RegularExpressions;

namespace Tidelab.ConfigExport;

/// <summary>Reads the same FlatBuffers schema from the client's JavaScript config definitions
/// (Aki/JavaScript/Core/Define: Config/*.js, Config/SubType/*.js, ConfigQuery/*.js). The JS keeps
/// the schema's own field names in its getters (bPlayLoadingCameraAnimation, where flatc's C#
/// capitalizes) and defines tables the C# code no longer queries. The code is flatc output put
/// through the game's minifier, so a small set of regular shapes covers all of it; anything
/// else is reported as a problem rather than guessed.</summary>
public static partial class JsSchema
{
    public static FlatSchema Read(string defineDir)
    {
        var schema = new FlatSchema();
        var enums = new Dictionary<string, Dictionary<string, long>>();
        var files = new List<(string Path, string Text)>();
        foreach (var sub in new[] { "Config", Path.Combine("Config", "SubType") })
        {
            string dir = Path.Combine(defineDir, sub);
            if (!Directory.Exists(dir))
                continue;
            foreach (var path in Directory.GetFiles(dir, "*.js").Order(StringComparer.Ordinal))
            {
                string text = File.ReadAllText(path);
                if (EnumBody().Match(text) is { Success: true } e)
                {
                    var values = new Dictionary<string, long>();
                    foreach (Match v in EnumValue().Matches(e.Groups[2].Value))
                        values[v.Groups[1].Value] = long.Parse(v.Groups[2].Value, CultureInfo.InvariantCulture);
                    enums[e.Groups[1].Value] = values;
                }
                else
                    files.Add((path, text));
            }
        }
        foreach (var (path, text) in files)
            ReadTable(schema, enums, Path.GetFileNameWithoutExtension(path), text);
        string queries = Path.Combine(defineDir, "ConfigQuery");
        if (Directory.Exists(queries))
            foreach (var path in Directory.GetFiles(queries, "*.js").Order(StringComparer.Ordinal))
                ReadQuery(schema, path);
        schema.Finish();
        return schema;
    }

    private static void ReadQuery(FlatSchema schema, string path)
    {
        string text = File.ReadAllText(path);
        var db = Const(text, "DB");
        var table = Const(text, "TABLE");
        if (db is null || table is null)
            return;
        var root = RootCall().Match(text);
        if (!root.Success)
        {
            // Text tables (MultiText and the *Lang queries) hold strings, not FlatBuffers.
            schema.Skipped.Add($"{Path.GetFileNameWithoutExtension(path)}: no getRootAs (not FlatBuffers)");
            return;
        }
        schema.Configs.Add(new ConfigTable(table, db, root.Groups[1].Value));
    }

    private static string? Const(string text, string name)
    {
        var m = Regex.Match(text, $@"[,\s]{name}=""([^""]*)""");
        return m.Success ? m.Groups[1].Value : null;
    }

    private static void ReadTable(FlatSchema schema, Dictionary<string, Dictionary<string, long>> enums, string name, string text)
    {
        var cls = ClassName().Match(text);
        if (!cls.Success || cls.Groups[1].Value != name)
        {
            // Helpers next to the tables (ConfigDefineBase, ConfigMap) read no FlatBuffers.
            if (text.Contains("__offset("))
                schema.Problems.Add($"{name}.js: no class {name}");
            else
                schema.Skipped.Add($"{name}.js: not a table");
            return;
        }
        var methods = new Dictionary<string, (int Slot, string Body)>();
        foreach (Match m in Method().Matches(text))
            methods[m.Groups[1].Value] = (int.Parse(m.Groups[2].Value, CultureInfo.InvariantCulture), m.Groups[3].Value);
        var fields = new List<Field>();
        var getters = Getter().Matches(text).Select(g => (g.Groups[1].Value, g.Groups[2].Value)).ToList();
        // Map entries (DicXxx) have no getters, only key() and value().
        if (getters.Count == 0 && methods.ContainsKey("key") && methods.ContainsKey("value"))
            getters = [("Key", "this.key()"), ("Value", "this.value()")];
        foreach (var (prop, body) in getters)
        {
            Field? f = null;
            if (PlainGetter().Match(body) is { Success: true } p)
                f = Scalar(schema, enums, name, prop, p.Groups[1].Value, methods);
            else if (ArrayGetter().Match(body) is { Success: true } a)
                f = Vector(schema, name, prop, a.Groups[1].Value, methods, false);
            else if (MapGetter().Match(body) is { Success: true } mp)
                f = Vector(schema, name, prop, mp.Groups[1].Value, methods, true);
            else
                schema.Problems.Add($"{name}.{prop}: unknown getter {body}");
            if (f is not null)
                fields.Add(f);
        }
        schema.Tables[name] = new TableType(name, fields);
    }

    private static Field? Scalar(FlatSchema schema, Dictionary<string, Dictionary<string, long>> enums,
        string table, string prop, string method, Dictionary<string, (int Slot, string Body)> methods)
    {
        if (!methods.TryGetValue(method, out var m))
        {
            schema.Problems.Add($"{table}.{prop}: no method {method}");
            return null;
        }
        string b = m.Body;
        if (b.Contains("__vector("))
        {
            schema.Problems.Add($"{table}.{prop}: vector read by a plain getter");
            return null;
        }
        if (b.Contains("__string("))
            return new Field(prop, m.Slot, Kind.String, null, false, false);
        if (NewTable().Match(b) is { Success: true } t && b.Contains("__indirect("))
            return new Field(prop, m.Slot, Kind.Table, t.Groups[1].Value, false, false);
        if (b.Contains("return!!") && b.Contains("readInt8("))
            return new Field(prop, m.Slot, Kind.Bool, null, false, false, false);
        if (b.Contains("return!") && b.Contains("||!!") && b.Contains("readInt8("))
            return new Field(prop, m.Slot, Kind.Bool, null, false, false, true);
        var read = ReadCall().Match(b);
        if (!read.Success)
        {
            schema.Problems.Add($"{table}.{prop}: unknown accessor {b}");
            return null;
        }
        Kind kind = KindOf(read.Groups[1].Value);
        object? def = Default(DefaultLiteral().Match(b) is { Success: true } d ? d.Groups[1].Value : null, enums, out bool ok);
        if (!ok)
            schema.Problems.Add($"{table}.{prop}: unknown default in {b}");
        return new Field(prop, m.Slot, kind, null, false, false, def);
    }

    private static Field? Vector(FlatSchema schema, string table, string prop, string method,
        Dictionary<string, (int Slot, string Body)> methods, bool map)
    {
        if (!methods.TryGetValue(method, out var m) || !m.Body.Contains("__vector("))
        {
            schema.Problems.Add($"{table}.{prop}: no vector accessor {method}");
            return null;
        }
        string b = m.Body;
        if (b.Contains("__string("))
            return new Field(prop, m.Slot, Kind.String, null, true, map);
        if (NewTable().Match(b) is { Success: true } t && b.Contains("__indirect("))
            return new Field(prop, m.Slot, Kind.Table, t.Groups[1].Value, true, map);
        if (b.Contains("!!") && b.Contains("readInt8("))
            return new Field(prop, m.Slot, Kind.Bool, null, true, map);
        var read = ReadCall().Match(b);
        if (!read.Success)
        {
            schema.Problems.Add($"{table}.{prop}: unknown element accessor {b}");
            return null;
        }
        return new Field(prop, m.Slot, KindOf(read.Groups[1].Value), null, true, map);
    }

    private static Kind KindOf(string read) => read switch
    {
        "readInt8" => Kind.SByte,
        "readUint8" => Kind.Byte,
        "readInt16" => Kind.Short,
        "readInt32" => Kind.Int,
        "readInt64" => Kind.Long,
        "readFloat32" => Kind.Float,
        "readFloat64" => Kind.Double,
        _ => throw new InvalidDataException(read),
    };

    /// <summary>A default as the minified JS writes it: 0, -1, .25, 1e3, BigInt(0), BigInt("-1"),
    /// or an enum member (EncircleHexType_1.EncircleHexType.Plain).</summary>
    private static object? Default(string? literal, Dictionary<string, Dictionary<string, long>> enums, out bool ok)
    {
        ok = true;
        if (literal is null)
        {
            ok = false;
            return null;
        }
        var big = Regex.Match(literal, @"^BigInt\(""?(-?\d+)""?\)$");
        if (big.Success)
            return long.Parse(big.Groups[1].Value, CultureInfo.InvariantCulture);
        var member = Regex.Match(literal, @"^\w+_1\.(\w+)\.(\w+)$");
        if (member.Success)
        {
            if (enums.TryGetValue(member.Groups[1].Value, out var values) && values.TryGetValue(member.Groups[2].Value, out long v))
                return v;
            ok = false;
            return null;
        }
        if (double.TryParse(literal, NumberStyles.Float, CultureInfo.InvariantCulture, out double d))
            return JsonText.Integral(d);
        ok = false;
        return null;
    }

    [GeneratedRegex(@"class (\w+)\{")]
    private static partial Regex ClassName();

    // name(args){var t=this.J7.__offset(this.z7,SLOT)...}: an accessor; bodies hold no braces.
    [GeneratedRegex(@"(?<![\w.])(\w+)\([\w,]*\)\{(?:var|let|const) \w+=this\.\w+\.__offset\(this\.\w+,(\d+)\)([^{}]*)\}")]
    private static partial Regex Method();

    [GeneratedRegex(@"get (\w+)\(\)\{return ([^{}]*)\}")]
    private static partial Regex Getter();

    [GeneratedRegex(@"^this\.(\w+)\(\)$")]
    private static partial Regex PlainGetter();

    [GeneratedRegex(@"^\w+_1\.GameUtils\.ConvertToArray\(this\.(\w+)Length\(\),this\.\1,this\)$")]
    private static partial Regex ArrayGetter();

    [GeneratedRegex(@"^\w+_1\.GameUtils\.ConvertToMap\(this\.(\w+)Length\(\),this\.\1Key,this\.\1Value,this\)$")]
    private static partial Regex MapGetter();

    [GeneratedRegex(@"new (?:\w+_1\.)?(\w+)\)\.__init")]
    private static partial Regex NewTable();

    [GeneratedRegex(@"\.(read(?:U?int(?:8|16|32|64)|Int(?:8|16|32|64)|Float32|Float64))\(")]
    private static partial Regex ReadCall();

    [GeneratedRegex(@"\):([^;:?]+)$")]
    private static partial Regex DefaultLiteral();

    [GeneratedRegex(@"getRootAs(\w+)\(")]
    private static partial Regex RootCall();

    [GeneratedRegex(@"var (\w+);Object\.defineProperty\(exports,""__esModule"",\{value:!0\}\),exports\.\1=void 0,function\(\w\)\{(.*?)\}\(")]
    private static partial Regex EnumBody();

    [GeneratedRegex(@"\[\w\.(\w+)=(-?\d+)\]")]
    private static partial Regex EnumValue();
}
