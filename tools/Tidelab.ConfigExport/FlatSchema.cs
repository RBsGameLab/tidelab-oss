// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System.Reflection.Metadata;
using Tidelab.ProtoGen;

namespace Tidelab.ConfigExport;

/// <summary>What a FlatBuffers field holds (or each element of a vector field).</summary>
public enum Kind { Bool, Byte, SByte, Short, Int, Long, Float, Double, String, Table, Json }

/// <summary>One field of a FlatBuffers table: its vtable slot, what it holds, whether it is a
/// vector or a map (a vector of Key/Value tables the client turns into a Dictionary), and for a
/// scalar the value an absent field has (the schema's default: long, double or bool).</summary>
public sealed record Field(string Name, int Slot, Kind Kind, string? Table, bool Vector, bool Map, object? Default = null);

public sealed record TableType(string Name, IReadOnlyList<Field> Fields);

/// <summary>A config table the client queries: the database file, the SQLite table and the
/// FlatBuffers table its BinData column holds.</summary>
public sealed record ConfigTable(string Name, string Db, string Root);

/// <summary>The FlatBuffers schema of the client's config data, read from the flatc-generated
/// code in Aki.Generated.dll (metadata and IL; the assembly is never loaded or run).</summary>
public sealed class FlatSchema
{
    public Dictionary<string, TableType> Tables { get; } = [];
    public List<ConfigTable> Configs { get; } = [];
    public List<string> Problems { get; } = [];
    /// <summary>Types made from specs/data-tables.tsv (only the fields the server reads): their rows
    /// may hold more fields, which are ignored.</summary>
    public HashSet<string> Lenient { get; } = [];
    /// <summary>Queries that are not FlatBuffers tables (the text tables), left out on purpose.</summary>
    public List<string> Skipped { get; } = [];

    public const string Namespace = "Aki.Config";

    public static FlatSchema Read(string dll)
    {
        using var asm = new Tidelab.ProtoGen.Assembly(dll);
        var schema = new FlatSchema();
        foreach (var t in asm.Types)
        {
            if (asm.IsNested(t) || !asm.FullName(t).StartsWith(Namespace + "."))
                continue;
            if (asm.Interfaces(t).Contains("Google.FlatBuffers.IFlatbufferObject"))
                schema.ReadTable(asm, t);
            else
                schema.ReadQuery(asm, t);
        }
        schema.Finish();
        return schema;
    }

    /// <summary>Checks that every root and field table was read, and keeps one config per
    /// table (several query classes, ById, All, By&lt;column&gt;, read the same table).</summary>
    public void Finish()
    {
        foreach (var c in Configs)
            if (!Tables.ContainsKey(c.Root))
                Problems.Add($"{c.Name}: root table {c.Root} not found");
        foreach (var tt in Tables.Values)
            foreach (var f in tt.Fields)
                if (f.Kind == Kind.Table && !Tables.ContainsKey(f.Table!))
                    Problems.Add($"{tt.Name}.{f.Name}: table {f.Table} not found");
        var unique = Configs
            .GroupBy(c => (c.Db.ToLowerInvariant(), c.Name.ToLowerInvariant()))
            .Select(g => g.First()).OrderBy(c => c.Name, StringComparer.Ordinal).ToList();
        Configs.Clear();
        Configs.AddRange(unique);
    }

    /// <summary>A query class: const DB and TABLE, and a call to the root table's GetRootAs.</summary>
    private void ReadQuery(Tidelab.ProtoGen.Assembly asm, TypeDefinitionHandle t)
    {
        string? db = null, table = null;
        foreach (var (name, _, value) in asm.Fields(t))
        {
            if (name == "DB" && value is string d) db = d;
            if (name == "TABLE" && value is string n) table = n;
        }
        if (db is null || table is null)
            return;
        string? root = null;
        foreach (var (_, m) in asm.Methods(t))
            foreach (var i in asm.Instructions(m))
                if (i.Member is { } member && member.StartsWith("GetRootAs") && i.Owner!.StartsWith(Namespace + "."))
                    root ??= i.Owner[(Namespace.Length + 1)..];
        if (root is null)
            Skipped.Add($"{asm.FullName(t)}: no GetRootAs (not FlatBuffers)");
        else
            Configs.Add(new ConfigTable(table, db, root));
    }

    private void ReadTable(Tidelab.ProtoGen.Assembly asm, TypeDefinitionHandle t)
    {
        string name = asm.Name(t);
        var methods = asm.Methods(t).ToList();
        var fields = new List<Field>();
        foreach (var (prop, type, getter) in asm.Getters(t))
        {
            if (prop == "ByteBuffer")
                continue;
            var il = asm.Instructions(getter);
            int? slot = Slot(il);
            if (slot is null)
            {
                Problems.Add($"{name}.{prop}: no vtable slot");
                continue;
            }
            bool isLength = il.Any(i => i.Member == "__vector_len");
            if (!isLength)
            {
                var (kind, table) = Classify(il, type);
                if (kind is null)
                    Problems.Add($"{name}.{prop}: unknown accessor ({type})");
                else
                    fields.Add(new Field(prop, slot.Value, kind.Value, table, false, false, Default(il, kind.Value)));
                continue;
            }
            // XxxLength: a vector field Xxx, whose element accessor is Xxx(int j).
            string vec = prop[..^"Length".Length];
            var element = methods.Where(m => m.Name == vec).Select(m => (m.Handle, Sig: asm.Signature(m.Handle)))
                .FirstOrDefault(m => !m.Sig.Static && m.Sig.Parameters.Length == 1 && m.Sig.Parameters[0].Name == "System.Int32");
            if (element.Handle.IsNil)
            {
                Problems.Add($"{name}.{vec}: vector without element accessor");
                continue;
            }
            var (ek, et) = Classify(asm.Instructions(element.Handle), element.Sig.Return);
            if (ek is null)
            {
                Problems.Add($"{name}.{vec}: unknown element accessor ({element.Sig.Return})");
                continue;
            }
            // A Dictionary-returning Xxx() marks a map: a vector of tables with Key and Value.
            bool map = methods.Where(m => m.Name == vec).Select(m => asm.Signature(m.Handle))
                .Any(s => !s.Static && s.Parameters.Length == 0 && s.Return.Name == "System.Collections.Generic.Dictionary`2");
            fields.Add(new Field(vec, slot.Value, ek.Value, et, true, map));
        }
        fields.Sort((a, b) => a.Slot.CompareTo(b.Slot));
        Tables[name] = new TableType(name, fields);
    }

    /// <summary>The constant passed to Table.__offset.</summary>
    private static int? Slot(List<Instr> il)
    {
        for (int k = 1; k < il.Count; k++)
            if (il[k].Member == "__offset" && il[k - 1].Op == ILOpCode.Ldc_i4)
                return (int)il[k - 1].Int;
        return null;
    }

    /// <summary>The schema default of a scalar: the constant the getter returns when the field is
    /// absent (a constant directly followed by ret). Float literals keep their decimal form
    /// (0.1f is 0.1, as the schema writes it), not the widened double.</summary>
    private static object? Default(List<Instr> il, Kind kind)
    {
        if (kind is Kind.String or Kind.Table)
            return null;
        for (int k = 0; k + 1 < il.Count; k++)
        {
            var i = il[k];
            var next = il[k + 1].Op;
            // ldc.i4 then conv.i8 (a long default) or conv.r4/r8 (a whole float default).
            if (next is ILOpCode.Conv_i8 or ILOpCode.Conv_r4 or ILOpCode.Conv_r8 && k + 2 < il.Count && il[k + 2].Op == ILOpCode.Ret)
                next = ILOpCode.Ret;
            // (long)(num != 0 ? GetDouble(..) : 0.0), the "Long54" fields: the branches meet at conv.i8.
            if (next is ILOpCode.Br or ILOpCode.Br_s)
                next = ILOpCode.Ret;
            if (next != ILOpCode.Ret)
                continue;
            object? value = i.Op switch
            {
                ILOpCode.Ldc_i4 or ILOpCode.Ldc_i8 => i.Int,
                ILOpCode.Ldc_r4 => double.Parse(((float)i.Real).ToString("R", System.Globalization.CultureInfo.InvariantCulture), System.Globalization.CultureInfo.InvariantCulture),
                ILOpCode.Ldc_r8 => i.Real,
                _ => null,
            };
            if (value is null)
                continue;
            if (kind == Kind.Bool)
                return i.Int != 0;
            return value is double d ? JsonText.Integral(d) : value;
        }
        return null;
    }

    private static (Kind?, string?) Classify(List<Instr> il, TypeSig type)
    {
        foreach (var i in il)
        {
            switch (i.Member)
            {
                case "__string": return (Kind.String, null);
                case "__indirect":
                    // Nullable<T>: T is the table.
                    var inner = type.Args.Length == 1 ? type.Args[0] : type;
                    return (Kind.Table, inner.ShortName);
                case "GetInt": return (Kind.Int, null);
                case "GetLong": return (Kind.Long, null);
                case "GetFloat": return (Kind.Float, null);
                case "GetDouble": return (Kind.Double, null);
                case "GetSbyte": return (Kind.SByte, null);
                case "GetShort": return (Kind.Short, null);
                case "Get": return (type.Name == "System.Boolean" ? Kind.Bool : Kind.Byte, null);
            }
        }
        return (null, null);
    }
}
