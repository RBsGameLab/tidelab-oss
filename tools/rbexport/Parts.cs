// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-174b: CharacterPartConfig from the client's own assets. Each monster blueprint's class default
// object holds DtCharacterPart, a DataTable of SCharacterPart rows (the client reads it in
// CharacterPartComponent); probe 2026-11-22: model 280003 gives the 6 parts of the earlier copy.
// Input <folder>/parts_models.txt: "ModelId<TAB>blueprint class path" lines (setup writes them from
// ModelConfigPreload). Output <folder>/CharacterPartConfig.json in the server's shape
// (specs/data-tables.tsv): ModelId, Parts[Index, Name, LifeRatio, BirthActivated, PartTagId,
// ActiveTagId, CombineSocket]; tag ids as specs/entities.md 3.4 (of the client's tag names).
// Runs on the game thread, a few classes per frame; progress in <folder>/rb_parts.txt.
using System;
using System.Collections;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Threading;

namespace RB;

public static class Parts
{
    private const int PerFrame = 4;

    public static void Run(string folder, string[] lines)
    {
        string progress = folder + "rb_parts.txt";
        var byClass = new Dictionary<string, List<int>>();
        foreach (string line in lines)
        {
            string[] f = line.Split('\t');
            if (f.Length < 2 || !int.TryParse(f[0], out int model) || f[1].Trim().Length == 0) continue;
            if (!byClass.TryGetValue(f[1].Trim(), out var ids)) byClass[f[1].Trim()] = ids = new List<int>();
            ids.Add(model);
        }
        var result = new SortedDictionary<int, List<Dictionary<string, object>>>();
        var log = new StringBuilder();
        var done = new ManualResetEventSlim();
        try
        {
            for (int i = 0; i < 600 && Probe.TryFind("Aki.Game", "DataTableUtil") == null; i++) Thread.Sleep(500);
            var api = new Api();
            Type queue = Probe.Find("UniTask", "Cysharp.Threading.Tasks.MicrotaskQueue");
            MethodInfo enqueue = queue.GetMethod("Enqueue", new[] { typeof(Action) })!;
            var pending = new Queue<string>(byClass.Keys);
            int loaded = 0, withParts = 0, failed = 0;
            Action? step = null;
            step = () =>
            {
                try
                {
                    for (int n = 0; n < PerFrame && pending.Count > 0; n++)
                    {
                        string path = pending.Dequeue();
                        try
                        {
                            var parts = api.PartsOf(path);
                            loaded++;
                            if (parts != null && parts.Count > 0)
                            {
                                withParts++;
                                foreach (int model in byClass[path]) result[model] = parts;
                            }
                        }
                        catch (Exception e)
                        {
                            failed++;
                            log.AppendLine("failed " + path + ": " + e.GetType().Name + " " + e.Message);
                        }
                    }
                    if (loaded % 40 == 0) File.WriteAllText(progress, $"{loaded + failed}/{byClass.Count} classes, {withParts} with parts, {failed} failed\r\n" + log);
                    if (pending.Count > 0) enqueue.Invoke(null, new object[] { step! });
                    else done.Set();
                }
                catch (Exception e)
                {
                    log.AppendLine("step error: " + e);
                    done.Set();
                }
            };
            enqueue.Invoke(null, new object[] { step });
            done.Wait(TimeSpan.FromMinutes(30));
            log.Insert(0, $"done: {loaded + failed}/{byClass.Count} classes, {withParts} with parts, {failed} failed, {result.Count} models\r\n");
        }
        catch (Exception e)
        {
            log.AppendLine("error: " + e);
        }
        var rows = result.Select(kv => new Dictionary<string, object> { ["ModelId"] = kv.Key, ["Parts"] = kv.Value }).ToList();
        File.WriteAllText(folder + "CharacterPartConfig.json", JsonSerializer.Serialize(rows, new JsonSerializerOptions { WriteIndented = true, Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping }));
        File.WriteAllText(progress, log.ToString());
    }

    /// 32-bit FNV-1, basis 0, over UTF-16 code units, signed (specs/entities.md 3.4).
    public static int TagId(string name)
    {
        uint h = 0;
        foreach (char c in name) h = unchecked((h ^ c) * 16777619u);
        return unchecked((int)h);
    }

    private sealed class Api
    {
        private readonly MethodInfo loadClass, cdoOf, rowsOf;
        private readonly Type classPtr;

        public Api()
        {
            Type globals = Probe.Find("UnrealEngine", "UnrealEngine.UObjectGlobals");
            Type kuro = Probe.Find("UnrealEngine", "UnrealEngine.UKuroStaticLibrary");
            classPtr = Probe.Find("UnrealEngine", "UnrealEngine.UClassStackOnlyPtr");
            loadClass = globals.GetMethods().First(m => m.Name == "StaticLoadClass" && m.GetParameters().Length == 2);
            cdoOf = kuro.GetMethods().First(m => m.Name == "GetDefaultObject" && m.GetParameters().Length == 1);
            Type util = Probe.Find("Aki.Game", "DataTableUtil");
            Type row = Probe.Find("Aki.Game", "SCharacterPart");
            rowsOf = util.GetMethods().First(m => m.Name == "GetDataTableAllRowFromTable" && m.IsGenericMethodDefinition).MakeGenericMethod(row);
        }

        public List<Dictionary<string, object>>? PartsOf(string classPath)
        {
            nint cls = (nint)loadClass.Invoke(null, new object?[] { null, classPath })!;
            if (cls == 0) return null;
            object? cdo = cdoOf.Invoke(null, new[] { Activator.CreateInstance(classPtr, new object[] { cls })! });
            PropertyInfo? dt = cdo?.GetType().GetProperty("DtCharacterPart", BindingFlags.Public | BindingFlags.Instance);
            object? table = dt?.GetValue(cdo);
            if (table == null) return null;
            var list = (IList)rowsOf.Invoke(null, new[] { table })!;
            var parts = new List<Dictionary<string, object>>();
            for (int i = 0; i < list.Count; i++)
            {
                object r = list[i]!;
                parts.Add(new Dictionary<string, object>
                {
                    ["Index"] = i,
                    ["Name"] = Get(r, "部位名")?.ToString() ?? "",
                    ["LifeRatio"] = Convert.ToSingle(Get(r, "继承生命值比例") ?? 0f),
                    ["BirthActivated"] = Get(r, "是否出生激活") is true,
                    ["PartTagId"] = TagId(TagName(Get(r, "部位标签"))),
                    ["ActiveTagId"] = TagId(TagName(Get(r, "部位激活标签"))),
                    ["CombineSocket"] = Get(r, "合体骨骼名")?.ToString() ?? "",
                });
            }
            return parts;
        }

        private static object? Get(object o, string name) =>
            o.GetType().GetProperty(name, BindingFlags.Public | BindingFlags.Instance)?.GetValue(o);

        /// FGameplayTag.TagName (field or property, an FName) as text; "" for none.
        private static string TagName(object? tag)
        {
            if (tag == null) return "";
            Type t = tag.GetType();
            object? n = t.GetProperty("TagName")?.GetValue(tag) ?? t.GetField("TagName", BindingFlags.Public | BindingFlags.NonPublic | BindingFlags.Instance)?.GetValue(tag);
            string s = n?.ToString() ?? "";
            return s == "None" ? "" : s;
        }
    }
}
