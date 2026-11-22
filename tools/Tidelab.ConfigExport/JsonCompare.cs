// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Text.Json;
using System.Text.RegularExpressions;

namespace Tidelab.ConfigExport;

/// <summary>Compares JSON by value: objects by their keys (order aside), arrays element by
/// element, numbers by their double value, strings exactly.</summary>
public static class JsonCompare
{
    public static int Directories(string a, string b, Regex? only, int max, TextWriter output)
    {
        var namesA = Names(a);
        var namesB = Names(b);
        int same = 0, differ = 0;
        foreach (var n in namesA.Keys.Where(k => only is null || only.IsMatch(k)).Order(StringComparer.Ordinal))
        {
            if (!namesB.TryGetValue(n, out var pb))
                continue;
            var diffs = Files(namesA[n], pb, max);
            if (diffs.Count == 0) { same++; continue; }
            differ++;
            output.WriteLine($"DIFF {n}");
            foreach (var d in diffs)
                output.WriteLine("  " + d);
        }
        var onlyA = namesA.Keys.Except(namesB.Keys).Where(k => only is null || only.IsMatch(k)).Order(StringComparer.Ordinal).ToList();
        var onlyB = namesB.Keys.Except(namesA.Keys).Where(k => only is null || only.IsMatch(k)).Order(StringComparer.Ordinal).ToList();
        foreach (var n in onlyA) output.WriteLine($"ONLY-A {n}");
        foreach (var n in onlyB) output.WriteLine($"ONLY-B {n}");
        output.WriteLine($"same {same}, different {differ}, only in A {onlyA.Count}, only in B {onlyB.Count}");
        return differ == 0 ? 0 : 1;
    }

    /// <summary>Table name -> its files: Name.json, or Name_1.json, Name_2.json ... in order.</summary>
    private static Dictionary<string, List<string>> Names(string dir)
    {
        var result = new Dictionary<string, List<string>>();
        foreach (var p in Directory.GetFiles(dir, "*.json").Order(StringComparer.Ordinal))
        {
            string n = Path.GetFileNameWithoutExtension(p);
            var part = Regex.Match(n, @"^(.+)_(\d+)$");
            if (part.Success && !File.Exists(Path.Combine(dir, part.Groups[1].Value + ".json")) && File.Exists(Path.Combine(dir, part.Groups[1].Value + "_1.json")))
                n = part.Groups[1].Value;
            if (!result.TryGetValue(n, out var list))
                result[n] = list = [];
            list.Add(p);
        }
        foreach (var list in result.Values)
            list.Sort((a, b) => PartNumber(a).CompareTo(PartNumber(b)));
        return result;
    }

    private static int PartNumber(string path)
    {
        var m = Regex.Match(Path.GetFileNameWithoutExtension(path), @"_(\d+)$");
        return m.Success ? int.Parse(m.Groups[1].Value) : 0;
    }

    public static List<string> Files(List<string> a, List<string> b, int max)
    {
        var diffs = new List<string>();
        using var da = Load(a);
        using var db = Load(b);
        Values(da.RootElement, db.RootElement, "$", diffs, max);
        return diffs;
    }

    /// <summary>One file as it is; several parts as one array of all their rows.</summary>
    private static JsonDocument Load(List<string> parts)
    {
        var options = new JsonDocumentOptions { MaxDepth = 512 };
        if (parts.Count == 1)
            return JsonDocument.Parse(File.ReadAllBytes(parts[0]), options);
        var all = new MemoryStream();
        all.WriteByte((byte)'[');
        bool first = true;
        foreach (var p in parts)
        {
            using var doc = JsonDocument.Parse(File.ReadAllBytes(p), options);
            foreach (var row in doc.RootElement.EnumerateArray())
            {
                if (!first) all.WriteByte((byte)',');
                first = false;
                var raw = System.Text.Encoding.UTF8.GetBytes(row.GetRawText());
                all.Write(raw);
            }
        }
        all.WriteByte((byte)']');
        return JsonDocument.Parse(all.ToArray(), options);
    }

    public static void Values(JsonElement a, JsonElement b, string path, List<string> diffs, int max)
    {
        if (diffs.Count >= max)
            return;
        if (a.ValueKind != b.ValueKind && !(IsBool(a) && IsBool(b)))
        {
            diffs.Add($"{path}: {Show(a)} vs {Show(b)}");
            return;
        }
        switch (a.ValueKind)
        {
            case JsonValueKind.Object:
            {
                var pb = new Dictionary<string, JsonElement>();
                foreach (var p in b.EnumerateObject()) pb[p.Name] = p.Value;
                var seen = new HashSet<string>();
                foreach (var p in a.EnumerateObject())
                {
                    seen.Add(p.Name);
                    if (!pb.TryGetValue(p.Name, out var vb))
                    {
                        diffs.Add($"{path}.{p.Name}: only in A ({Show(p.Value)})");
                        if (diffs.Count >= max) return;
                        continue;
                    }
                    Values(p.Value, vb, path + "." + p.Name, diffs, max);
                }
                foreach (var (k, v) in pb)
                    if (!seen.Contains(k) && diffs.Count < max)
                        diffs.Add($"{path}.{k}: only in B ({Show(v)})");
                break;
            }
            case JsonValueKind.Array:
            {
                int na = a.GetArrayLength(), nb = b.GetArrayLength();
                if (na != nb)
                    diffs.Add($"{path}: {na} vs {nb} elements");
                int k = 0;
                using var ea = a.EnumerateArray().GetEnumerator();
                using var eb = b.EnumerateArray().GetEnumerator();
                while (ea.MoveNext() && eb.MoveNext() && diffs.Count < max)
                    Values(ea.Current, eb.Current, $"{path}[{k++}]", diffs, max);
                break;
            }
            case JsonValueKind.Number:
                if (a.GetDouble() != b.GetDouble())
                    diffs.Add($"{path}: {a.GetRawText()} vs {b.GetRawText()}");
                break;
            case JsonValueKind.String:
                if (a.GetString() != b.GetString())
                    diffs.Add($"{path}: {Show(a)} vs {Show(b)}");
                break;
            case JsonValueKind.True or JsonValueKind.False:
                if (a.GetBoolean() != b.GetBoolean())
                    diffs.Add($"{path}: {a.GetRawText()} vs {b.GetRawText()}");
                break;
        }
    }

    private static bool IsBool(JsonElement e) => e.ValueKind is JsonValueKind.True or JsonValueKind.False;

    private static string Show(JsonElement e)
    {
        string t = e.GetRawText();
        return t.Length > 80 ? t[..77] + "..." : t;
    }
}
