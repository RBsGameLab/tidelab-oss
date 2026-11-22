// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-174b: GameplayTagRemap from the client itself. Some of the client's tag names are obfuscated
// (segments replaced by 8 hex digits, e.g. 关卡.黑白洞机关.白洞 -> 关卡.501c7d02.20e4b5ff), while its
// tables keep the readable names; the client turns a readable name into the obfuscated tag's id
// (GameplayTagUtils.GetTagIdByName). Input <folder>/tag_names.txt: names the server reads from its
// tables that are not in the client's tag list. For each, the client's id is asked on the game
// thread; when it differs from the name's own id (specs/entities.md 3.4) and is not 0, a row
// RawId = own id, CanonicalId = client id goes to <folder>/GameplayTagRemap.json.
using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Threading;

namespace RB;

public static class Tags
{
    public static void Run(string folder, string[] names)
    {
        var log = new StringBuilder();
        var rows = new SortedDictionary<int, int>();
        var done = new ManualResetEventSlim();
        try
        {
            for (int i = 0; i < 600 && Probe.TryFind("Aki.Game", "DataTableUtil") == null; i++) Thread.Sleep(500);
            MethodInfo? byName = null;
            foreach (Assembly a in AppDomain.CurrentDomain.GetAssemblies())
            {
                Type? t;
                try { t = a.GetTypes().FirstOrDefault(x => x.Name == "GameplayTagUtils"); }
                catch (ReflectionTypeLoadException e) { t = e.Types.FirstOrDefault(x => x?.Name == "GameplayTagUtils"); }
                byName = t?.GetMethods(BindingFlags.Public | BindingFlags.Static)
                    .FirstOrDefault(m => m.Name == "GetTagIdByName" && m.GetParameters().Length >= 1 && m.GetParameters()[0].ParameterType == typeof(string));
                if (byName != null) { log.AppendLine("GetTagIdByName in " + a.GetName().Name + ": " + byName); break; }
            }
            if (byName == null) throw new MissingMethodException("GameplayTagUtils.GetTagIdByName");
            MethodInfo method = byName;
            Type queue = Probe.Find("UniTask", "Cysharp.Threading.Tasks.MicrotaskQueue");
            MethodInfo enqueue = queue.GetMethod("Enqueue", new[] { typeof(Action) })!;
            int asked = 0, same = 0, zero = 0;
            Action work = () =>
            {
                try
                {
                    foreach (string raw in names)
                    {
                        string name = raw.Trim();
                        if (name.Length == 0) continue;
                        asked++;
                        object?[] args = method.GetParameters().Select((p, i) => i == 0 ? name : (p.HasDefaultValue ? p.DefaultValue : null)).ToArray();
                        int client = Convert.ToInt32(method.Invoke(null, args));
                        int own = Parts.TagId(name);
                        if (client == 0) zero++;
                        else if (client == own) same++;
                        else rows[own] = client;
                    }
                }
                catch (Exception e) { log.AppendLine("game thread error: " + e); }
                finally { done.Set(); }
            };
            enqueue.Invoke(null, new object[] { work });
            if (!done.Wait(TimeSpan.FromMinutes(5))) log.AppendLine("timeout");
            log.AppendLine($"asked {asked}: {rows.Count} remapped, {same} same id, {zero} unknown (0)");
        }
        catch (Exception e) { log.AppendLine("error: " + e); }
        var list = rows.Select(kv => new Dictionary<string, int> { ["RawId"] = kv.Key, ["CanonicalId"] = kv.Value }).ToList();
        File.WriteAllText(folder + "GameplayTagRemap.json", JsonSerializer.Serialize(list, new JsonSerializerOptions { WriteIndented = true }));
        File.WriteAllText(folder + "rb_tags.txt", log.ToString());
    }
}
