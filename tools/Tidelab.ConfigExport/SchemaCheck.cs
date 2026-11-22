// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

namespace Tidelab.ConfigExport;

/// <summary>Compares two readings of the client's schema (the C# IL and the JavaScript): for
/// every table both define, the same fields at the same slots, of the same kind, with the same
/// default; names equal ignoring case (C# capitalizes the first letter).</summary>
public static class SchemaCheck
{
    public static List<string> Compare(FlatSchema il, FlatSchema js)
    {
        var diffs = new List<string>();
        foreach (var (name, a) in il.Tables.OrderBy(kv => kv.Key, StringComparer.Ordinal))
        {
            if (!js.Tables.TryGetValue(name, out var b))
                continue;
            var bySlot = b.Fields.ToDictionary(f => f.Slot);
            foreach (var fa in a.Fields)
            {
                if (!bySlot.Remove(fa.Slot, out var fb))
                {
                    diffs.Add($"{name}: slot {fa.Slot} ({fa.Name}) only in C#");
                    continue;
                }
                if (!string.Equals(fa.Name, fb.Name, StringComparison.OrdinalIgnoreCase))
                    diffs.Add($"{name}: slot {fa.Slot} named {fa.Name} / {fb.Name}");
                if (fa.Kind != fb.Kind && !SameWidth(fa.Kind, fb.Kind))
                    diffs.Add($"{name}.{fb.Name}: {fa.Kind} / {fb.Kind}");
                if (fa.Vector != fb.Vector || fa.Map != fb.Map)
                    diffs.Add($"{name}.{fb.Name}: vector {fa.Vector}/{fb.Vector}, map {fa.Map}/{fb.Map}");
                if (fa.Table != fb.Table)
                    diffs.Add($"{name}.{fb.Name}: table {fa.Table} / {fb.Table}");
                if (!fa.Vector && !Equals(fa.Default, fb.Default))
                    diffs.Add($"{name}.{fb.Name}: default {fa.Default ?? "none"} / {fb.Default ?? "none"}");
            }
            foreach (var fb in bySlot.Values)
                diffs.Add($"{name}: slot {fb.Slot} ({fb.Name}) only in JS");
        }
        return diffs;
    }

    // A one-byte enum is sbyte in the JS reader (readInt8) and byte in C# (Get).
    private static bool SameWidth(Kind a, Kind b) => (a, b) is (Kind.Byte, Kind.SByte) or (Kind.SByte, Kind.Byte);
}
