// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-174b: probe how the client's own assets give the server-only tables (CharacterPartConfig:
// each monster blueprint's DtCharacterPart DataTable). Runs on the game thread (UniTask's
// MicrotaskQueue, drained there every frame), because loading assets needs it. Writes
// <folder>/rb_probe.txt. Everything by reflection and logged, so a wrong guess shows what is there.
using System;
using System.Collections;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Text;
using System.Threading;

namespace RB;

public static class Probe
{
    public static void Run(string folder, string[] classPaths)
    {
        var log = new StringBuilder();
        var done = new ManualResetEventSlim();
        try
        {
            // Aki.Game (SCharacterPart, DataTableUtil) loads after the launcher stage.
            for (int i = 0; i < 600 && TryFind("Aki.Game", "DataTableUtil") == null; i++) Thread.Sleep(500);
            log.AppendLine("Aki.Game loaded: " + (TryFind("Aki.Game", "DataTableUtil") != null));
            Type queue = Find("UniTask", "Cysharp.Threading.Tasks.MicrotaskQueue");
            MethodInfo enqueue = queue.GetMethod("Enqueue", new[] { typeof(Action) })!;
            Action work = () =>
            {
                try { OnGameThread(log, classPaths); }
                catch (Exception e) { log.AppendLine("game thread error: " + e); }
                finally { done.Set(); }
            };
            enqueue.Invoke(null, new object[] { work });
            if (!done.Wait(TimeSpan.FromSeconds(120))) log.AppendLine("timeout: the game thread did not run the probe");
        }
        catch (Exception e)
        {
            log.AppendLine("error: " + e);
        }
        File.WriteAllText(folder + "rb_probe.txt", log.ToString());
    }

    private static void OnGameThread(StringBuilder log, string[] classPaths)
    {
        Type globals = Find("UnrealEngine", "UnrealEngine.UObjectGlobals");
        Type kuro = Find("UnrealEngine", "UnrealEngine.UKuroStaticLibrary");
        Type classPtr = Find("UnrealEngine", "UnrealEngine.UClassStackOnlyPtr");
        MethodInfo loadClass = globals.GetMethods().First(m => m.Name == "StaticLoadClass" && m.GetParameters().Length == 2);
        MethodInfo cdoOf = kuro.GetMethods().First(m => m.Name == "GetDefaultObject" && m.GetParameters().Length == 1);
        Type? util = TryFind("Aki.Game", "DataTableUtil");
        Type? partRow = TryFind("Aki.Game", "SCharacterPart");
        log.AppendLine($"DataTableUtil {util?.FullName}; SCharacterPart {partRow?.FullName}");
        foreach (string path in classPaths)
        {
            log.AppendLine("== " + path);
            nint cls = (nint)loadClass.Invoke(null, new object?[] { null, path })!;
            log.AppendLine($"class 0x{cls:x}");
            if (cls == 0) continue;
            object ptr = Activator.CreateInstance(classPtr, new object[] { cls })!;
            object? cdo = cdoOf.Invoke(null, new[] { ptr });
            log.AppendLine("cdo type " + cdo?.GetType().FullName);
            if (cdo == null) continue;
            PropertyInfo? dt = cdo.GetType().GetProperty("DtCharacterPart", BindingFlags.Public | BindingFlags.Instance);
            if (dt == null)
            {
                log.AppendLine("no DtCharacterPart; properties with Dt/Part/Entity/Buff: " + string.Join(", ",
                    cdo.GetType().GetProperties().Select(p => p.Name).Where(n => n.Contains("Dt") || n.Contains("Part") || n.Contains("Entity") || n.Contains("Buff")).Take(40)));
                continue;
            }
            object? table = dt.GetValue(cdo);
            log.AppendLine("DtCharacterPart " + (table == null ? "null" : table.GetType().FullName + " " + Call(table, "GetPathName")));
            if (table == null || util == null || partRow == null) continue;
            MethodInfo rows = util.GetMethods().First(m => m.Name == "GetDataTableAllRowFromTable" && m.IsGenericMethodDefinition).MakeGenericMethod(partRow);
            var list = (IList)rows.Invoke(null, new[] { table })!;
            log.AppendLine("rows " + list.Count);
            for (int i = 0; i < Math.Min(list.Count, 3); i++)
            {
                object row = list[i]!;
                log.AppendLine($"row {i}: " + string.Join("; ", row.GetType().GetProperties(BindingFlags.Public | BindingFlags.Instance)
                    .Where(p => p.GetIndexParameters().Length == 0)
                    .Select(p => p.Name + "=" + Show(SafeGet(p, row)))));
            }
        }
    }

    private static object? SafeGet(PropertyInfo p, object o)
    {
        try { return p.GetValue(o); } catch (Exception e) { return "!" + e.GetType().Name; }
    }

    private static string Show(object? v)
    {
        if (v == null) return "null";
        if (v is string s) return s;
        if (v is IEnumerable e and not string)
        {
            var parts = new System.Collections.Generic.List<string>();
            foreach (object? x in e) { parts.Add(x?.ToString() ?? "null"); if (parts.Count >= 8) break; }
            return "[" + string.Join(",", parts) + "]";
        }
        string? t = Call(v, "ToString");
        PropertyInfo? tagName = v.GetType().GetProperty("TagName");
        return tagName != null ? "Tag(" + tagName.GetValue(v) + ")" : t ?? v.GetType().Name;
    }

    private static string? Call(object o, string method)
    {
        try { return o.GetType().GetMethod(method, Type.EmptyTypes)?.Invoke(o, null)?.ToString(); }
        catch (Exception e) { return "!" + e.GetType().Name; }
    }

    public static Type Find(string assembly, string type) =>
        TryFind(assembly, type) ?? throw new TypeLoadException(type + " not in " + assembly);

    public static Type? TryFind(string assembly, string type)
    {
        Assembly? a = AppDomain.CurrentDomain.GetAssemblies().FirstOrDefault(x => x.GetName().Name == assembly);
        if (a == null) return null;
        return a.GetType(type) ?? a.GetTypes().FirstOrDefault(t => t.Name == type || t.FullName == type);
    }
}

/// R-174b research: every public property of each class default object listed in
/// <folder>/dump_classes.txt, with values (arrays in full up to 200 items), to <folder>/rb_dump.txt.
public static class Dump
{
    public static void Run(string folder, string[] classPaths)
    {
        var log = new StringBuilder();
        var done = new ManualResetEventSlim();
        try
        {
            for (int i = 0; i < 600 && Probe.TryFind("Aki.Game", "DataTableUtil") == null; i++) Thread.Sleep(500);
            Type queue = Probe.Find("UniTask", "Cysharp.Threading.Tasks.MicrotaskQueue");
            MethodInfo enqueue = queue.GetMethod("Enqueue", new[] { typeof(Action) })!;
            Action work = () =>
            {
                try
                {
                    Type globals = Probe.Find("UnrealEngine", "UnrealEngine.UObjectGlobals");
                    Type kuro = Probe.Find("UnrealEngine", "UnrealEngine.UKuroStaticLibrary");
                    Type classPtr = Probe.Find("UnrealEngine", "UnrealEngine.UClassStackOnlyPtr");
                    MethodInfo loadClass = globals.GetMethods().First(m => m.Name == "StaticLoadClass" && m.GetParameters().Length == 2);
                    MethodInfo cdoOf = kuro.GetMethods().First(m => m.Name == "GetDefaultObject" && m.GetParameters().Length == 1);
                    foreach (string path in classPaths)
                    {
                        log.AppendLine("== " + path);
                        nint cls = (nint)loadClass.Invoke(null, new object?[] { null, path })!;
                        if (cls == 0) { log.AppendLine("no class"); continue; }
                        object? cdo = cdoOf.Invoke(null, new[] { Activator.CreateInstance(classPtr, new object[] { cls })! });
                        if (cdo == null) { log.AppendLine("no cdo"); continue; }
                        log.AppendLine("type " + cdo.GetType().FullName);
                        foreach (PropertyInfo p in cdo.GetType().GetProperties(BindingFlags.Public | BindingFlags.Instance).Where(p => p.GetIndexParameters().Length == 0))
                        {
                            object? v;
                            try { v = p.GetValue(cdo); } catch (Exception e) { v = "!" + e.GetType().Name; }
                            log.AppendLine(p.Name + " : " + p.PropertyType.Name + " = " + Show(v));
                        }
                    }
                }
                catch (Exception e) { log.AppendLine("error: " + e); }
                finally { done.Set(); }
            };
            enqueue.Invoke(null, new object[] { work });
            if (!done.Wait(TimeSpan.FromSeconds(180))) log.AppendLine("timeout");
        }
        catch (Exception e) { log.AppendLine("error: " + e); }
        File.WriteAllText(folder + "rb_dump.txt", log.ToString());
    }

    private static string Show(object? v)
    {
        if (v == null) return "null";
        if (v is string s) return s;
        if (v is IEnumerable e)
        {
            var parts = new System.Collections.Generic.List<string>();
            foreach (object? x in e) { parts.Add(x?.ToString() ?? "null"); if (parts.Count >= 200) break; }
            return "[" + string.Join(",", parts) + "]";
        }
        string t = v.ToString() ?? "";
        MethodInfo? path = v.GetType().GetMethod("GetPathName", Type.EmptyTypes);
        if (path != null) { try { t += " " + path.Invoke(v, null); } catch (Exception) { } }
        return t.Length > 300 ? t.Substring(0, 300) : t;
    }
}
