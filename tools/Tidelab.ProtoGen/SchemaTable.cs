// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Text;

namespace Tidelab.ProtoGen;

/// <summary>
/// The schema as sorted tab-separated lines, one per message id, message, field, enum and enum
/// value. Two schemas are the same protocol when their tables are equal; a diff of two tables
/// says exactly what differs. Lines:
/// <code>
/// id      &lt;EMessageId member&gt;  &lt;id&gt;
/// message &lt;full name&gt;          &lt;id or -&gt;
/// field   &lt;message&gt; &lt;number&gt; &lt;property&gt; &lt;label&gt; &lt;type&gt; [key=&lt;type&gt;] [oneof=&lt;name&gt;] [unpacked]
/// enum    &lt;full name&gt;
/// value   &lt;enum&gt; &lt;number&gt; &lt;name&gt;
/// </code>
/// </summary>
public static class SchemaTable
{
    public static List<string> Lines(Schema s)
    {
        var lines = new List<string>();
        foreach (var (name, id) in s.MessageIds)
            lines.Add($"id\t{name}\t{id}");
        foreach (var m in s.Messages)
        {
            lines.Add($"message\t{m.FullName}\t{(m.Id is int id ? id.ToString() : "-")}");
            foreach (var f in m.Fields)
            {
                var sb = new StringBuilder();
                sb.Append($"field\t{m.FullName}\t{f.Number:D5}\t{f.Name}\t{f.Label.ToString().ToLowerInvariant()}\t{f.Type}");
                if (f.Key is not null)
                    sb.Append($"\tkey={f.Key}");
                if (f.Oneof is not null)
                    sb.Append($"\toneof={f.Oneof}");
                if (f.Label == Label.Repeated && f.Type.Packable && !f.Packed)
                    sb.Append("\tunpacked");
                lines.Add(sb.ToString());
            }
        }
        foreach (var e in s.Enums)
        {
            lines.Add($"enum\t{e.FullName}");
            foreach (var (name, value) in e.Values)
                lines.Add($"value\t{e.FullName}\t{value:D11}\t{name}");
        }
        lines.Sort(StringComparer.Ordinal);
        return lines;
    }

    public static string Text(Schema s) => string.Join("\n", Lines(s)) + "\n";
}
