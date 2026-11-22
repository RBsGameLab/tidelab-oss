// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-95 (3.8 direction, user 2026-11-18): keep PerfSight's collection points but send what they
// collect to RB.RBAuto (tools/rbauto/RBPerf.cs) instead of Tencent's SDK, so the game's own
// frame, level, battle and GC marks become our local performance counters.
//
// In Aki.Game (after Inject.AutoDodge copied RB.RBAuto in), every
//   call     FKuroPerfSightHelper::X(args)   ->  call RB.RBAuto::PerfHelper_X(args)
//   callvirt PerfSight::X(args)              ->  call RB.RBAuto::PerfSight_X(PerfSight self, args)
// for which RBAuto has a method of that name with the same parameter types. The stack is the
// same before and after (the instance becomes the first argument), so each swap only changes the
// instruction's operand. Calls with no RBAuto method stay as they were and are listed in the log.
using dnlib.DotNet;
using dnlib.DotNet.Emit;

static class R95PerfSightTap
{
    public static (byte[] pe, List<string> log) Apply(byte[] pe, List<string> log)
    {
        var module = ModuleDefMD.Load(pe);
        var rb = module.Find("RB.RBAuto", false) ?? throw new Exception("R95PerfSightTap: RB.RBAuto not in Aki.Game (build with rbauto.dll)");
        var helpers = rb.Methods.Where(m => m.IsStatic && (m.Name.StartsWith("PerfHelper_") || m.Name.StartsWith("PerfSight_")))
            .ToDictionary(m => m.Name.String);

        var swapped = new SortedDictionary<string, int>();
        var left = new SortedDictionary<string, int>();
        var tags = new SortedSet<string>();
        foreach (var type in module.GetTypes())
        {
            if (type == rb) continue;
            foreach (var m in type.Methods.Where(m => m.HasBody))
                for (var at = 0; at < m.Body.Instructions.Count; at++)
                {
                    var ins = m.Body.Instructions[at];
                    if (ins.OpCode != OpCodes.Call && ins.OpCode != OpCodes.Callvirt) continue;
                    if (ins.Operand is not IMethod callee || callee.DeclaringType == null) continue;
                    var owner = callee.DeclaringType.FullName;
                    string prefix;
                    if (owner == "CSharpScript.Typing.FKuroPerfSightHelper" && !callee.MethodSig.HasThis) prefix = "PerfHelper_";
                    else if (owner == "PerfSight" && callee.MethodSig.HasThis && callee.Name != "get_Instance") prefix = "PerfSight_";
                    else continue;
                    var name = prefix + callee.Name;
                    if (!helpers.TryGetValue(name, out var helper) || !SameParams(callee, helper, prefix == "PerfSight_"))
                    {
                        left[owner + "." + callee.Name] = left.GetValueOrDefault(owner + "." + callee.Name) + 1;
                        continue;
                    }
                    if (callee.MethodSig.RetType.FullName != helper.MethodSig.RetType.FullName)
                        throw new Exception($"{name}: returns {helper.MethodSig.RetType.FullName}, the game's {callee.MethodSig.RetType.FullName}");
                    ins.OpCode = OpCodes.Call;
                    ins.Operand = helper;
                    swapped[name] = swapped.GetValueOrDefault(name) + 1;
                    if (name == "PerfHelper_BeginExtTag")
                        tags.Add(at > 0 && m.Body.Instructions[at - 1].Operand is string tag ? tag : "(computed in " + type.Name + ")");
                }
        }

        // PostFrame must reach us (PerfSightController.OnTick) or rb_perf.tsv stays empty.
        foreach (var must in new[] { "PerfHelper_PostFrame", "PerfSight_MarkLevelLoad", "PerfHelper_BeginExtTag" })
            if (!swapped.ContainsKey(must)) throw new Exception($"R95PerfSightTap: no call redirected to {must}");
        log.Add("R-95 PerfSight tap: " + string.Join(", ", swapped.Select(kv => $"{kv.Key} x{kv.Value}")));
        log.Add("R-95 PerfSight tap, tags begun: " + string.Join(", ", tags));
        if (left.Count > 0) log.Add("R-95 PerfSight tap, left to PerfSight: " + string.Join(", ", left.Select(kv => $"{kv.Key} x{kv.Value}")));
        return (IlHelpers.WriteModule(module), log);
    }

    static bool SameParams(IMethod callee, MethodDef helper, bool instance)
    {
        var want = new List<string>();
        if (instance) want.Add(callee.DeclaringType.FullName);
        want.AddRange(callee.MethodSig.Params.Select(p => p.FullName));
        var have = helper.MethodSig.Params.Select(p => p.FullName).ToList();
        return want.SequenceEqual(have);
    }
}
