// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System;
using System.Collections.Generic;
using dnlib.DotNet;
using dnlib.DotNet.Emit;

/// <summary>
/// R-94 safe C# hot-path optimizations for Aki.Game.dll:
/// 1. Skip dead Shipping ControllerBase.GetPerformanceStateObject() dispatch in ControllerRegister.Tick.
/// 2. Make per-frame temporary List&lt;T&gt; allocations lazy in AiTeam, AiModelController (+empty early-out),
///    ItemMaterialManager, and CombatMessageController.
/// 3. Cache two Render CVar lookups per 128 frames in SceneCharacterInteraction and SceneCharacterTriggerEffect.
/// 4. Cache read-only WeaponProp05 FName in RenderDataManager.Tick.
/// </summary>
internal static class R94PerfFixes
{
    public static (byte[] pe, List<string> log) Apply(byte[] pe, List<string> log)
    {
        var module = ModuleDefMD.Load(pe);
        var refs = ResolveRefs(module);
        var cache = BuildCacheType(module, refs);

        ApplyControllerRegisterFix(module, log);
        ApplyLazyListFixes(module, log);
        ApplyRenderCVarFixes(module, cache, log);
        ApplyRenderWeaponNameFix(module, cache, log);

        return (IlHelpers.WriteModule(module), log);
    }

    private sealed record GameRefs(
        IMethod TimeInstance,
        IMethod TimeFrame,
        IMethod GetCVarFloat,
        IMethod GetCVarInt,
        IMethod FNameStringCtor,
        TypeSig FNameSig);

    private sealed record CacheApi(
        MethodDef GetUseCppWater,
        MethodDef GetEnableBushEffect,
        MethodDef GetWeaponProp05Name);

    private static GameRefs ResolveRefs(ModuleDefMD module)
    {
        var charShadowTick = IlHelpers.RequireMethod(module, "CharacterShadowController", "OnTick");
        var timeInstance = IlHelpers.FindCall(charShadowTick, "Time", "get_Instance");
        var timeFrame = IlHelpers.FindCall(charShadowTick, "Time", "get_Frame");

        var waterMethod = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.SceneCharacterInteraction", "Update");
        var getCVarFloat = IlHelpers.FindCall(waterMethod, "UKismetSystemLibrary", "GetConsoleVariableFloatValue");

        var bushMethod = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.SceneCharacterTriggerEffect", "IsBushInteractionEffectEnabled");
        var getCVarInt = IlHelpers.FindCall(bushMethod, "UKismetSystemLibrary", "GetConsoleVariableIntValue");

        var renderTick = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.RenderDataManager", "Tick");
        var fnameCtor = IlHelpers.FindCall(renderTick, "FName", ".ctor");
        var fnameSig = fnameCtor.DeclaringType.ToTypeSig();

        return new GameRefs(timeInstance, timeFrame, getCVarFloat, getCVarInt, fnameCtor, fnameSig);
    }

    private static CacheApi BuildCacheType(ModuleDefMD module, GameRefs r)
    {
        var cor = module.CorLibTypes;
        var cache = new TypeDefUser("RB", "R94Cache", cor.Object.TypeDefOrRef)
        {
            Attributes = TypeAttributes.Public | TypeAttributes.Abstract | TypeAttributes.Sealed | TypeAttributes.BeforeFieldInit | TypeAttributes.Class,
        };
        module.Types.Add(cache);

        FieldDef AddField(string name, TypeSig sig)
        {
            var f = new FieldDefUser(name, new FieldSig(sig), FieldAttributes.Private | FieldAttributes.Static);
            cache.Fields.Add(f);
            return f;
        }

        var waterBucket = AddField("_waterBucket", cor.Int32);
        var waterCached = AddField("_waterCached", cor.Boolean);
        var bushBucket = AddField("_bushBucket", cor.Int32);
        var bushCached = AddField("_bushCached", cor.Boolean);
        var weaponInit = AddField("_weaponInit", cor.Boolean);
        var weaponName = AddField("_weaponName", r.FNameSig);

        return new CacheApi(
            BuildCVarCacheMethod(cache, "GetUseCppWaterEffect", waterBucket, waterCached, r,
                "r.Kuro.InteractionEffect.UseCppWaterEffect", isFloat: true),
            BuildCVarCacheMethod(cache, "GetEnableBushInteractionEffect", bushBucket, bushCached, r,
                "r.Kuro.InteractionEffect.EnableBushInteractionEffect", isFloat: false),
            BuildWeaponNameMethod(cache, weaponInit, weaponName, r));
    }

    private static MethodDef BuildCVarCacheMethod(
        TypeDefUser owner,
        string methodName,
        FieldDef bucketField,
        FieldDef cachedField,
        GameRefs r,
        string cvarName,
        bool isFloat)
    {
        var cor = owner.Module.CorLibTypes;
        var m = NewStaticMethod(owner, methodName, MethodSig.CreateStatic(cor.Boolean));
        var body = m.Body;
        body.MaxStack = 4;
        var bucketLocal = new Local(cor.Int32, "bucket");
        body.Variables.Add(bucketLocal);

        var done = OpCodes.Ldsfld.ToInstruction(cachedField);
        Emit(body,
            OpCodes.Call.ToInstruction(r.TimeInstance),
            OpCodes.Callvirt.ToInstruction(r.TimeFrame),
            OpCodes.Ldc_I4_7.ToInstruction(),
            OpCodes.Shr.ToInstruction(),
            OpCodes.Ldc_I4_1.ToInstruction(),
            OpCodes.Add.ToInstruction(),
            OpCodes.Stloc.ToInstruction(bucketLocal),
            OpCodes.Ldloc.ToInstruction(bucketLocal),
            OpCodes.Ldsfld.ToInstruction(bucketField),
            OpCodes.Beq_S.ToInstruction(done),
            OpCodes.Ldloc.ToInstruction(bucketLocal),
            OpCodes.Stsfld.ToInstruction(bucketField),
            OpCodes.Ldstr.ToInstruction(cvarName),
            OpCodes.Call.ToInstruction(isFloat ? r.GetCVarFloat : r.GetCVarInt),
            isFloat ? OpCodes.Ldc_R4.ToInstruction(0f) : OpCodes.Ldc_I4_0.ToInstruction(),
            OpCodes.Cgt.ToInstruction(),
            OpCodes.Stsfld.ToInstruction(cachedField),
            done,
            OpCodes.Ret.ToInstruction());
        return m;
    }

    private static MethodDef BuildWeaponNameMethod(TypeDefUser owner, FieldDef initField, FieldDef nameField, GameRefs r)
    {
        var m = NewStaticMethod(owner, "GetWeaponProp05Name", MethodSig.CreateStatic(r.FNameSig));
        var body = m.Body;
        body.MaxStack = 3;
        var done = OpCodes.Ldsfld.ToInstruction(nameField);
        Emit(body,
            OpCodes.Ldsfld.ToInstruction(initField),
            OpCodes.Brtrue_S.ToInstruction(done),
            OpCodes.Ldsflda.ToInstruction(nameField),
            OpCodes.Ldstr.ToInstruction("WeaponProp05"),
            OpCodes.Call.ToInstruction(r.FNameStringCtor),
            OpCodes.Ldc_I4_1.ToInstruction(),
            OpCodes.Stsfld.ToInstruction(initField),
            done,
            OpCodes.Ret.ToInstruction());
        return m;
    }

    private static void ApplyControllerRegisterFix(ModuleDefMD module, List<string> log)
    {
        var tick = IlHelpers.RequireMethod(module, "ControllerRegister", "Tick");
        var ins = tick.Body.Instructions;
        int patched = 0;
        for (int i = 0; i + 3 < ins.Count; i++)
        {
            if (ins[i].OpCode == OpCodes.Ldloc_1 &&
                ins[i + 1].OpCode == OpCodes.Callvirt &&
                ins[i + 1].Operand is IMethod m && m.Name == "GetPerformanceStateObject" &&
                ins[i + 2].OpCode == OpCodes.Pop)
            {
                IlHelpers.Nop(ins[i]);
                IlHelpers.Nop(ins[i + 1]);
                IlHelpers.Nop(ins[i + 2]);
                patched++;
            }
        }
        IlHelpers.RequireCount("ControllerRegister.Tick GetPerformanceStateObject", patched, 1);
        log.Add($"{tick.FullName}: removed dead GetPerformanceStateObject() dispatch");
    }

    private static void ApplyLazyListFixes(ModuleDefMD module, List<string> log)
    {
        var aiTeam = IlHelpers.RequireMethod(module, "AiTeam", "ReorganizeGroup");
        MakeListLocalLazy(aiTeam, localIndex: 0, patternName: "List<EntityHandle>", skipMode: LazySkipMode.BeforeForeachToRet, log);

        var shareHatred = IlHelpers.RequireMethod(module, "AiModelController", "ShareHatredTarget");
        PrependHatredCountEarlyOut(shareHatred, log);
        MakeListLocalLazy(shareHatred, localIndex: 5, patternName: "List<AiController>", skipMode: LazySkipMode.BeforeForeachNextOuter, log);

        var itemMatTick = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.ItemMaterialManager", "Tick");
        MakeListLocalLazy(itemMatTick, localIndex: 1, patternName: "List<Int32>", skipMode: LazySkipMode.BeforeForLoopEnd, log);

        var combatFlush = IlHelpers.RequireMethod(module, "CSharpScript.Game.Module.CombatMessage.CombatMessageController", "FlushMessagePack");
        MakeListLocalLazy(combatFlush, localIndex: 0, patternName: "List<String>", skipMode: LazySkipMode.None, log);
    }

    private static void PrependHatredCountEarlyOut(MethodDef method, List<string> log)
    {
        var body = method.Body;
        body.SimplifyBranches();
        var ins = body.Instructions;
        if (ins.Count < 4 || ins[0].OpCode != OpCodes.Ldarg_0 ||
            ins[1].OpCode != OpCodes.Call || ((IMethod)ins[1].Operand).Name != "get_Model" ||
            ins[2].OpCode != OpCodes.Ldfld || ((IField)ins[2].Operand).Name != "HatredGroups" ||
            ins[^1].OpCode != OpCodes.Ret)
            throw new Exception($"{method.FullName}: unexpected preamble");

        var groupsField = (IField)ins[2].Operand;
        var dictType = groupsField.FieldSig.Type.ToTypeDefOrRef();
        var getCount = new MemberRefUser(method.Module, "get_Count", MethodSig.CreateInstance(method.Module.CorLibTypes.Int32), dictType);

        var retIns = ins[^1];
        var oldFirst = OpCodes.Ldarg_0.ToInstruction();
        IlHelpers.InsertAfter(ins, 0,
            OpCodes.Call.ToInstruction(ins[1].Operand as IMethod),
            OpCodes.Ldfld.ToInstruction(groupsField),
            OpCodes.Callvirt.ToInstruction(getCount),
            OpCodes.Brfalse.ToInstruction(retIns),
            oldFirst);
        body.MaxStack = Math.Max(body.MaxStack, (ushort)4);
        body.OptimizeBranches();
        log.Add($"{method.FullName}: added HatredGroups.Count == 0 early return");
    }

    private enum LazySkipMode
    {
        None,
        BeforeForeachToRet,
        BeforeForeachNextOuter,
        BeforeForLoopEnd,
    }

    private static void MakeListLocalLazy(MethodDef method, int localIndex, string patternName, LazySkipMode skipMode, List<string> log)
    {
        var body = method.Body;
        body.SimplifyBranches();
        var ins = body.Instructions;
        var targetLocal = body.Variables[localIndex];
        IMethod? listCtor = null;

        int replacedAllocs = 0;
        for (int i = 0; i + 1 < ins.Count; i++)
        {
            if (ins[i].OpCode == OpCodes.Newobj &&
                ins[i].Operand is IMethod ctor && ctor.Name == ".ctor" && ctor.DeclaringType.Name.StartsWith("List`1") &&
                IlHelpers.GetStoredLocal(ins[i + 1], body) == targetLocal)
            {
                listCtor = ctor;
                ins[i].OpCode = OpCodes.Ldnull;
                ins[i].Operand = null;
                replacedAllocs++;
            }
        }
        IlHelpers.RequireCount($"{method.FullName} newobj {patternName}", replacedAllocs, 1);

        int lazyAdds = 0;
        for (int i = 0; i + 1 < ins.Count; i++)
        {
            if (ins[i].OpCode == OpCodes.Callvirt &&
                ins[i].Operand is IMethod call && call.Name == "Add" && call.DeclaringType.Name.StartsWith("List`1"))
            {
                int loadIdx = -1;
                for (int k = i - 1; k >= Math.Max(0, i - 6); k--)
                {
                    if (IlHelpers.GetLoadedLocal(ins[k], body) == targetLocal)
                    {
                        loadIdx = k;
                        break;
                    }
                }
                if (loadIdx < 0)
                    throw new Exception($"{method.FullName}: could not find ldloc[{localIndex}] before Add");

                var skipInit = OpCodes.Ldloc.ToInstruction(targetLocal);
                IlHelpers.InsertAfter(ins, loadIdx,
                    OpCodes.Brtrue.ToInstruction(skipInit),
                    OpCodes.Newobj.ToInstruction(listCtor!),
                    OpCodes.Stloc.ToInstruction(targetLocal),
                    skipInit);
                lazyAdds++;
                i += 4;
            }
        }
        IlHelpers.RequireCount($"{method.FullName} Add {patternName}", lazyAdds, 1);

        if (skipMode == LazySkipMode.BeforeForeachToRet)
        {
            GuardGetEnumerator(ins, body, targetLocal, _ => ins[^1], method.FullName);
        }
        else if (skipMode == LazySkipMode.BeforeForeachNextOuter)
        {
            GuardGetEnumerator(ins, body, targetLocal, getEnumIdx =>
            {
                int ef = IlHelpers.FindNextEndFinally(ins, getEnumIdx);
                return ins[ef + 1];
            }, method.FullName);
        }
        else if (skipMode == LazySkipMode.BeforeForLoopEnd)
        {
            int patchedFor = 0;
            for (int i = 0; i + 2 < ins.Count; i++)
            {
                if (ins[i].OpCode == OpCodes.Ldc_I4_0 &&
                    IlHelpers.IsStloc(ins[i + 1]) &&
                    ins[i + 2].OpCode == OpCodes.Br &&
                    ins[i + 2].Operand is Instruction head)
                {
                    int headIdx = ins.IndexOf(head);
                    bool usesTarget = false;
                    for (int k = headIdx; k < Math.Min(ins.Count, headIdx + 4); k++)
                        if (IlHelpers.GetLoadedLocal(ins[k], body) == targetLocal)
                            usesTarget = true;
                    if (!usesTarget) continue;

                    Instruction? afterLoop = null;
                    for (int k = headIdx; k < Math.Min(ins.Count - 1, headIdx + 6); k++)
                    {
                        if (ins[k].OpCode == OpCodes.Blt)
                        {
                            afterLoop = ins[k + 1];
                            break;
                        }
                    }
                    if (afterLoop == null)
                        throw new Exception($"{method.FullName}: could not find blt loop exit");

                    ins[i].OpCode = OpCodes.Ldloc;
                    ins[i].Operand = targetLocal;
                    IlHelpers.InsertAfter(ins, i,
                        OpCodes.Brfalse.ToInstruction(afterLoop),
                        OpCodes.Ldc_I4_0.ToInstruction());
                    patchedFor++;
                    break;
                }
            }
            IlHelpers.RequireCount($"{method.FullName} for-loop guard", patchedFor, 1);
        }

        body.MaxStack = Math.Max(body.MaxStack, (ushort)8);
        body.OptimizeMacros();
        log.Add($"{method.FullName}: made {patternName} allocation lazy");
    }

    private static void GuardGetEnumerator(
        IList<Instruction> ins,
        CilBody body,
        Local targetLocal,
        Func<int, Instruction> getSkipTarget,
        string methodName)
    {
        int guarded = 0;
        for (int i = 1; i < ins.Count; i++)
        {
            if (ins[i].OpCode == OpCodes.Callvirt &&
                ins[i].Operand is IMethod call && call.Name == "GetEnumerator" && call.DeclaringType.Name.StartsWith("List`1") &&
                IlHelpers.GetLoadedLocal(ins[i - 1], body) == targetLocal)
            {
                var skipTarget = getSkipTarget(i);
                IlHelpers.InsertAfter(ins, i - 1,
                    OpCodes.Brfalse.ToInstruction(skipTarget),
                    OpCodes.Ldloc.ToInstruction(targetLocal));
                guarded++;
                break;
            }
        }
        IlHelpers.RequireCount($"{methodName} GetEnumerator guard", guarded, 1);
    }

    private static void ApplyRenderCVarFixes(ModuleDefMD module, CacheApi cache, List<string> log)
    {
        var interaction = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.SceneCharacterInteraction", "Update");
        var ins = interaction.Body.Instructions;
        int replacedWater = 0;
        for (int i = 0; i + 3 < ins.Count; i++)
        {
            if (ins[i].OpCode == OpCodes.Ldstr && ins[i].Operand is string s && s == "r.Kuro.InteractionEffect.UseCppWaterEffect" &&
                ins[i + 1].OpCode == OpCodes.Call &&
                ins[i + 2].OpCode == OpCodes.Ldc_R4 &&
                ins[i + 3].OpCode == OpCodes.Cgt)
            {
                ins[i].OpCode = OpCodes.Call;
                ins[i].Operand = cache.GetUseCppWater;
                IlHelpers.Nop(ins[i + 1]);
                IlHelpers.Nop(ins[i + 2]);
                IlHelpers.Nop(ins[i + 3]);
                replacedWater++;
            }
        }
        IlHelpers.RequireCount("SceneCharacterInteraction UseCppWaterEffect", replacedWater, 1);
        log.Add($"{interaction.FullName}: cached UseCppWaterEffect per 128 frames");

        var bush = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.SceneCharacterTriggerEffect", "IsBushInteractionEffectEnabled");
        var body = new CilBody { InitLocals = false, MaxStack = 2 };
        Emit(body, OpCodes.Call.ToInstruction(cache.GetEnableBushEffect), OpCodes.Ret.ToInstruction());
        bush.Body = body;
        log.Add($"{bush.FullName}: cached EnableBushInteractionEffect per 128 frames");
    }

    private static void ApplyRenderWeaponNameFix(ModuleDefMD module, CacheApi cache, List<string> log)
    {
        var tick = IlHelpers.RequireMethod(module, "CSharpScript.Game.Render.RenderDataManager", "Tick");
        var tIns = tick.Body.Instructions;
        int fnameReplaced = 0;
        for (int i = 0; i + 4 < tIns.Count; i++)
        {
            if (IlHelpers.IsLdloca(tIns[i]) &&
                tIns[i + 1].OpCode == OpCodes.Ldstr && tIns[i + 1].Operand is string s && s == "WeaponProp05" &&
                tIns[i + 2].OpCode == OpCodes.Call && tIns[i + 2].Operand is IMethod m && m.DeclaringType.Name == "FName" && m.Name == ".ctor" &&
                IlHelpers.IsLdloc(tIns[i + 4]))
            {
                IlHelpers.Nop(tIns[i]);
                IlHelpers.Nop(tIns[i + 1]);
                IlHelpers.Nop(tIns[i + 2]);
                tIns[i + 4].OpCode = OpCodes.Call;
                tIns[i + 4].Operand = cache.GetWeaponProp05Name;
                fnameReplaced++;
            }
        }
        IlHelpers.RequireCount("RenderDataManager.Tick WeaponProp05 FName", fnameReplaced, 1);
        log.Add($"{tick.FullName}: cached WeaponProp05 FName (MPC writes left untouched)");
    }

    private static MethodDef NewStaticMethod(TypeDefUser owner, string name, MethodSig sig)
    {
        var m = new MethodDefUser(name, sig, MethodImplAttributes.IL | MethodImplAttributes.Managed,
            MethodAttributes.Public | MethodAttributes.Static | MethodAttributes.HideBySig);
        m.Body = new CilBody { InitLocals = true };
        owner.Methods.Add(m);
        return m;
    }

    private static void Emit(CilBody body, params Instruction[] instructions)
    {
        foreach (var ins in instructions)
            body.Instructions.Add(ins);
    }
}
