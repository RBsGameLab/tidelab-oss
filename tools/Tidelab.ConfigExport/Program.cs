// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Text;
using System.Text.RegularExpressions;
using Microsoft.Data.Sqlite;
using Tidelab.ConfigExport;

// <schema> is the client's JavaScript config definitions (the Aki/JavaScript/Core/Define
// directory: field names as the schema has them, every table) or its Aki.Generated.dll (flatc's
// C#, read as metadata and IL).
//
// configexport export <schema> <ConfigDB dir> <out dir> [--only <regex>] [--plain]
//   Writes one <name>.json per table of the databases that the schema describes (see
//   Exporter.Plan); --plain adds the others, with their plain columns only.
// configexport schema <schema>
//   Prints every config table the client queries, its database and root table, and anything
//   the reader could not understand.
// configexport checkschema <Aki.Generated.dll> <Define dir>
//   Compares the two readings of the schema table by table (slots, kinds, defaults, names).
// configexport gen <schema> <BinData dir> <specs/data-tables.tsv> <out .cs|.go> [--go]
//   Writes the row types for the tables of the tsv (Go when <out> ends in .go or --go is passed,
//   otherwise C#): from the client's schema where it has the table, otherwise from the tsv's fields.
// configexport compare <dir A> <dir B> [--only <regex>] [--max <n>]
//   Compares the JSON files of two directories by value (numbers by value, keys in any order)
//   and lists the files that differ, with the first differences in each. Name_1.json,
//   Name_2.json ... are read as one Name.json.
if (args.Length >= 3 && args[0] == "checkschema")
{
    var il = FlatSchema.Read(args[1]);
    var js = JsSchema.Read(args[2]);
    var diffs = SchemaCheck.Compare(il, js);
    foreach (var d in diffs)
        Console.WriteLine(d);
    int both = il.Tables.Keys.Count(js.Tables.ContainsKey);
    Console.Error.WriteLine($"{both} tables in both, {il.Tables.Count - both} only in C#, {js.Tables.Count - both} only in JS; {diffs.Count} differences");
    return diffs.Count == 0 ? 0 : 1;
}
if (args.Length >= 2 && args[0] == "schema")
{
    var s = ReadSchema(args[1]);
    foreach (var c in s.Configs)
        Console.WriteLine($"{c.Name}\t{c.Db}\t{c.Root}");
    foreach (var p in s.Problems)
        Console.Error.WriteLine("problem: " + p);
    Console.Error.WriteLine($"{s.Configs.Count} config tables, {s.Tables.Count} FlatBuffers tables, {s.Skipped.Count} skipped (text tables, helpers), {s.Problems.Count} problems");
    return s.Problems.Count == 0 ? 0 : 1;
}
if (args.Length >= 4 && args[0] == "export")
{
    var only = Option(args, "--only") is { } o ? new Regex(o) : null;
    var schema = ReadSchema(args[1]);
    foreach (var p in schema.Problems)
        Console.Error.WriteLine("problem: " + p);
    var exporter = new Exporter(schema, new ExportRules(), args[2]);
    // Tables with no FlatBuffers schema have only their key column to give; they are left out
    // unless asked for (their lower-case names would also shadow the real tables of the same
    // name, specs/data-tables.md §1, §6).
    var plan = exporter.Plan(Console.Error).Where(t => t.Root is not null || args.Contains("--plain")).ToList();
    Directory.CreateDirectory(args[3]);
    int files = 0;
    long rows = 0;
    int failed = 0;
    foreach (var t in plan)
    {
        if (only is not null && !only.IsMatch(t.Name))
            continue;
        try
        {
            rows += exporter.Export(t, args[3]);
            files++;
        }
        catch (Exception e) when (e is ArgumentOutOfRangeException or InvalidDataException or KeyNotFoundException)
        {
            // Data the schema does not describe (a database from another client version).
            File.Delete(Path.Combine(args[3], t.Name + ".json"));
            Console.Error.WriteLine($"failed: {t.Name} ({t.Db}): {e.Message}");
            failed++;
        }
    }
    Console.WriteLine($"{files} tables, {rows} rows written to {args[3]}" + (failed > 0 ? $"; {failed} failed" : ""));
    return schema.Problems.Count == 0 && failed == 0 ? 0 : 1;
}
if (args.Length >= 5 && args[0] == "gen")
{
    var schema = ReadSchema(args[1]);
    var tsv = TsvSchema.Read(args[3]);
    var gen = new RowGen(schema, args[2]);
    bool go = args[4].EndsWith(".go", StringComparison.OrdinalIgnoreCase) || args.Contains("--go");
    string text = go ? gen.GenerateGo(tsv) : gen.Generate(tsv);
    foreach (var p in schema.Problems.Concat(gen.Problems))
        Console.Error.WriteLine("problem: " + p);
    if (schema.Problems.Count + gen.Problems.Count > 0)
        return 1;
    File.WriteAllText(args[4], text, new UTF8Encoding(false));
    Console.WriteLine($"{tsv.Tables.Count} tables written to {args[4]}");
    return 0;
}
if (args.Length >= 3 && args[0] == "compare")
{
    var only = Option(args, "--only") is { } o ? new Regex(o) : null;
    int max = Option(args, "--max") is { } m ? int.Parse(m) : 5;
    return JsonCompare.Directories(args[1], args[2], only, max, Console.Out);
}
Console.Error.WriteLine("usage: configexport export <schema> <ConfigDB dir> <out dir> [--only <regex>] [--plain]\n" +
    "       configexport schema <schema>\n" +
    "       configexport checkschema <Aki.Generated.dll> <Define dir>\n" +
    "       configexport gen <schema> <BinData dir> <specs/data-tables.tsv> <out .cs|.go> [--go]\n" +
    "       configexport compare <dir A> <dir B> [--only <regex>] [--max <n>]");
return 2;

static FlatSchema ReadSchema(string source) =>
    source.EndsWith(".dll", StringComparison.OrdinalIgnoreCase) ? FlatSchema.Read(source) : JsSchema.Read(source);

static string? Option(string[] args, string name)
{
    int i = Array.IndexOf(args, name);
    return i >= 0 && i + 1 < args.Length ? args[i + 1] : null;
}

namespace Tidelab.ConfigExport
{
    /// <summary>One table to export: the JSON file's name, the database and SQLite table it comes
    /// from, and the FlatBuffers table its BinData holds (null: none known, the plain columns are
    /// written instead).</summary>
    public sealed record ExportTable(string Name, string Db, string SqlTable, string? Root);

    /// <summary>Reads config tables out of their SQLite databases and writes them as JSON.</summary>
    public sealed class Exporter(FlatSchema schema, ExportRules rules, string dbDir)
    {
        private readonly FlatDecoder decoder = new(schema, rules);

        /// <summary>What to export. Every table the client queries, from the database its query
        /// names; then every other SQLite table, under the name of the schema table it matches
        /// (ignoring case), or under its own name with only its plain columns when none does. A
        /// table that is in several databases and that no query names comes from the database
        /// with the most rows (db_activity.db keeps older copies of some activity tables).</summary>
        public List<ExportTable> Plan(TextWriter log)
        {
            var found = new Dictionary<string, List<(string Db, string Table, long Rows)>>(StringComparer.OrdinalIgnoreCase);
            foreach (var path in Directory.GetFiles(dbDir, "*.db").Order(StringComparer.Ordinal))
            {
                using var conn = Open(path);
                using var cmd = conn.CreateCommand();
                cmd.CommandText = "select name from sqlite_master where type='table' order by name";
                var names = new List<string>();
                using (var r = cmd.ExecuteReader())
                    while (r.Read())
                        names.Add(r.GetString(0));
                foreach (var n in names)
                {
                    using var count = conn.CreateCommand();
                    count.CommandText = $"select count(*) from `{n}`";
                    found.TryAdd(n, []);
                    found[n].Add((Path.GetFileName(path), n, (long)count.ExecuteScalar()!));
                }
            }
            var plan = new List<ExportTable>();
            var done = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            foreach (var c in schema.Configs)
            {
                var hit = found.TryGetValue(c.Name, out var list)
                    ? list.FirstOrDefault(x => string.Equals(x.Db, c.Db, StringComparison.OrdinalIgnoreCase))
                    : default;
                if (hit.Db is null)
                {
                    log.WriteLine($"queried but not in the databases: {c.Name} ({c.Db})");
                    continue;
                }
                if (done.Add(c.Name))
                    plan.Add(new ExportTable(c.Name, hit.Db, hit.Table, c.Root));
            }
            var byLower = schema.Tables.Keys.ToDictionary(k => k, StringComparer.OrdinalIgnoreCase);
            foreach (var (name, list) in found.OrderBy(kv => kv.Key, StringComparer.Ordinal))
            {
                string? root = byLower.TryGetValue(name, out var r) ? r : null;
                string file = root ?? name;
                if (done.Contains(file))
                    continue;
                var pick = list.OrderByDescending(x => x.Rows).ThenBy(x => x.Db, StringComparer.Ordinal).First();
                if (list.Count > 1)
                    log.WriteLine($"{file}: in {string.Join(", ", list.Select(x => $"{x.Db} ({x.Rows})"))}; using {pick.Db}");
                done.Add(file);
                plan.Add(new ExportTable(file, pick.Db, pick.Table, root));
            }
            return plan;
        }

        private static SqliteConnection Open(string path)
        {
            var conn = new SqliteConnection(new SqliteConnectionStringBuilder { DataSource = path, Mode = SqliteOpenMode.ReadOnly }.ToString());
            conn.Open();
            return conn;
        }

        /// <summary>Writes the table's rows, in the database's order; returns how many.</summary>
        public int Export(ExportTable t, string outDir)
        {
            using var conn = Open(Path.Combine(dbDir, t.Db));
            using var cmd = conn.CreateCommand();
            cmd.CommandText = $"select * from `{t.SqlTable}`";
            using var reader = cmd.ExecuteReader();
            int bin = -1;
            for (int k = 0; k < reader.FieldCount; k++)
                if (reader.GetName(k) == "BinData") bin = k;
            using var w = new StreamWriter(Path.Combine(outDir, t.Name + ".json"), false, new UTF8Encoding(false)) { NewLine = "\n" };
            int rows = 0;
            w.Write('[');
            while (reader.Read())
            {
                JObject row;
                if (t.Root is not null && bin >= 0)
                    row = decoder.DecodeRoot((byte[])reader[bin], t.Root);
                else
                {
                    row = [];
                    for (int k = 0; k < reader.FieldCount; k++)
                        if (k != bin)
                            row.Add(reader.GetName(k), Column(reader.GetValue(k)));
                }
                w.Write(rows == 0 ? "\n  " : ",\n  ");
                JsonText.Write(w, row, 1);
                rows++;
            }
            w.Write(rows == 0 ? "]" : "\n]");
            return rows;
        }

        private static object? Column(object v) => v switch
        {
            DBNull => null,
            long l => l,
            double d => JsonText.Integral(d),
            string s => s,
            byte[] b => Convert.ToBase64String(b),
            _ => v.ToString(),
        };
    }
}
