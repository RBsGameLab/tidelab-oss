// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

namespace Tidelab.ConfigExport;

/// <summary>specs/data-tables.tsv: the tables the server loads, their key columns and the fields it
/// reads (path, JSON shape, presence). It is the table list of Tidelab.Data, and, for the tables
/// the client's schema does not describe (specs/data-tables.md §6), the source of their row
/// types: fields from the paths, kinds from the shapes.</summary>
public sealed class TsvSchema
{
    public sealed record Line(string Table, string File, string Key, string Path, string Shape, string Presence);

    public List<Line> Lines { get; } = [];
    /// <summary>Tables in the order of the file, with their key column.</summary>
    public List<(string Table, string Key)> Tables { get; } = [];

    public static TsvSchema Read(string path)
    {
        var tsv = new TsvSchema();
        foreach (var raw in File.ReadAllLines(path))
        {
            if (raw.StartsWith('#') || raw.StartsWith("table\t") || raw.Trim().Length == 0)
                continue;
            var c = raw.Split('\t');
            if (c.Length < 6)
                throw new InvalidDataException($"{path}: not 6 columns: {raw}");
            var line = new Line(c[0], c[1], c[2], c[3], c[4], c[5]);
            tsv.Lines.Add(line);
            if (tsv.Tables.Count == 0 || tsv.Tables[^1].Table != line.Table)
                tsv.Tables.Add((line.Table, line.Key));
        }
        return tsv;
    }

    /// <summary>Row types for a table from its lines: nested objects become tables named
    /// Table_Field (Table_Field_Sub ...), lists vectors, maps Key/Value entry tables.</summary>
    public void AddTypes(string table, FlatSchema schema)
    {
        var lines = Lines.Where(l => l.Table == table).ToList();
        BuildType(table, "", lines, schema);
    }

    private void BuildType(string typeName, string prefix, List<Line> lines, FlatSchema schema)
    {
        var fields = new List<Field>();
        int slot = 4;
        foreach (var l in lines)
        {
            if (!l.Path.StartsWith(prefix) || l.Path.Length == prefix.Length)
                continue;
            string name = l.Path[prefix.Length..];
            if (name.IndexOfAny(['.', '[', '{']) >= 0)
                continue; // a part of a field, handled with the field
            fields.Add(MakeField(typeName, name, l.Path, l.Shape, lines, schema, slot));
            slot += 2;
        }
        schema.Tables[typeName] = new TableType(typeName, fields);
        schema.Lenient.Add(typeName);
    }

    private Field MakeField(string owner, string name, string path, string shape, List<Line> lines, FlatSchema schema, int slot)
    {
        if (shape == "list" || shape.StartsWith("array(") || shape.StartsWith("tuple("))
        {
            var elem = lines.FirstOrDefault(l => l.Path == path + "[]")
                ?? throw new InvalidDataException($"{path}: a list without an element line");
            var inner = MakeField(owner, name, path + "[]", elem.Shape, lines, schema, slot);
            if (inner.Vector)
                throw new InvalidDataException($"{path}: list of lists is not supported");
            return inner with { Vector = true };
        }
        if (shape.StartsWith("map("))
        {
            var value = lines.FirstOrDefault(l => l.Path == path + "{}")
                ?? throw new InvalidDataException($"{path}: a map without a value line");
            string entry = $"{owner}_{name}Entry";
            var keyKind = shape.StartsWith("map(string") ? Kind.String : Kind.Int;
            var v = MakeField(entry, "Value", path + "{}", value.Shape, lines, schema, 6);
            schema.Tables[entry] = new TableType(entry, [new Field("Key", 4, keyKind, null, false, false), v with { Name = "Value" }]);
            schema.Lenient.Add(entry);
            return new Field(name, slot, Kind.Table, entry, true, true);
        }
        if (shape == "object")
        {
            string sub = $"{owner}_{name}";
            BuildType(sub, path + ".", lines, schema);
            return new Field(name, slot, Kind.Table, sub, false, false);
        }
        var kind = shape switch
        {
            "int64" => Kind.Long,
            "float32" => Kind.Float,
            "float64" => Kind.Double,
            "bool" => Kind.Bool,
            "string" => Kind.String,
            "any" => Kind.Json,
            _ when shape.StartsWith("int") || shape.StartsWith("uint") || shape.StartsWith("enum(") || shape.StartsWith("protocol-enum(") => Kind.Int,
            _ => throw new InvalidDataException($"{path}: unknown shape {shape}"),
        };
        return new Field(name, slot, kind, null, false, false);
    }
}
