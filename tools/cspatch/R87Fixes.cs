// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using dnlib.DotNet;
using dnlib.DotNet.Emit;
using dnlib.DotNet.Writer;

/// R-87: client UI reliability fixes for 0-price PayShop items and stuck async UI component states.
static class R87Fixes
{
    public static (byte[] pe, List<string> log) Apply(byte[] pe, List<string>? prevLog = null)
    {
        var log = prevLog ?? new List<string>();
        using var mod = ModuleDefMD.Load(pe);
        PatchExchangePopView(mod, log);
        PatchComponentActionAsyncStates(mod, log);

        using var ms = new MemoryStream();
        var opts = new ModuleWriterOptions(mod);
        opts.MetadataOptions.Flags |= MetadataFlags.KeepOldMaxStack;
        mod.Write(ms, opts);

        var outBytes = ms.ToArray();
        using var check = ModuleDefMD.Load(outBytes);
        if (check.TablesStream.Name != "#~")
            throw new InvalidOperationException($"expected #~ tables stream, got {check.TablesStream.Name}");
        log.Add($"R-87: ExchangePopView zero-price guard + ComponentAction async exception guards (tables {check.TablesStream.Name})");
        return (outBytes, log);
    }

    /// Guard `InventoryModel.Instance.GetItemCountByConfigId(id) / goodsData.GetNowPrice()` against <= 0 price.
    static void PatchExchangePopView(ModuleDefMD mod, List<string> log)
    {
        var type = mod.Find("ExchangePopView", false)
            ?? throw new InvalidOperationException("ExchangePopView not found");
        var method = type.Methods.SingleOrDefault(m => m.Name == "SetMaxCanBuyCount" && m.MethodSig.Params.Count == 0)
            ?? throw new InvalidOperationException("ExchangePopView.SetMaxCanBuyCount not found");
        var body = method.Body ?? throw new InvalidOperationException("SetMaxCanBuyCount has no body");

        body.SimplifyMacros(method.Parameters);
        var instrs = body.Instructions;

        int divIdx = -1;
        for (int i = 1; i + 1 < instrs.Count; i++)
        {
            if (instrs[i].OpCode == OpCodes.Div
                && instrs[i - 1].OpCode == OpCodes.Callvirt
                && instrs[i - 1].Operand is IMethod callee
                && callee.Name == "GetNowPrice"
                && instrs[i + 1].IsStloc())
            {
                if (divIdx >= 0)
                    throw new InvalidOperationException("Multiple GetNowPrice/div sites in SetMaxCanBuyCount");
                divIdx = i;
            }
        }
        if (divIdx < 0)
            throw new InvalidOperationException("SetMaxCanBuyCount GetNowPrice/div pattern not found");

        var numLoc = instrs[divIdx + 1].GetLocal(body.Variables);
        var storeAnchor = instrs[divIdx + 1];
        var doDiv = Instruction.Create(OpCodes.Ldloc, numLoc);

        // Stack on entry: [itemCount, nowPrice]. Reuse `num` local briefly for nowPrice.
        instrs[divIdx].OpCode = OpCodes.Stloc;
        instrs[divIdx].Operand = numLoc;
        var tail = new[]
        {
            Instruction.Create(OpCodes.Ldloc, numLoc),
            Instruction.CreateLdcI4(0),
            Instruction.Create(OpCodes.Bgt, doDiv),
            OpCodes.Pop.ToInstruction(),
            Instruction.CreateLdcI4(int.MaxValue),
            Instruction.Create(OpCodes.Br, storeAnchor),
            doDiv,
            OpCodes.Div.ToInstruction(),
        };
        for (int k = 0; k < tail.Length; k++)
            instrs.Insert(divIdx + 1 + k, tail[k]);

        body.MaxStack = Math.Max(body.MaxStack, (ushort)3);
        body.OptimizeMacros();
        log.Add("ExchangePopView.SetMaxCanBuyCount(): guard <= 0 price with int.MaxValue");
    }

    /// Reset transient Creating/Starting/Showing/Hiding state on uncaught exceptions so IsBusy cannot stay stuck.
    static void PatchComponentActionAsyncStates(ModuleDefMD mod, List<string> log)
    {
        var compType = mod.Find("CSharpScript.Game.Ui.ComponentAction", false)
            ?? throw new InvalidOperationException("ComponentAction not found");
        var stateField = compType.Fields.SingleOrDefault(f => f.Name == "ComponentState")
            ?? throw new InvalidOperationException("ComponentAction.ComponentState not found");

        var transitions = new (string Prefix, int BusyState, int FallbackState)[]
        {
            ("<CreateAsync>d__", 1, 0),         // Creating -> Register
            ("<StartAsyncImplement>d__", 3, 2), // Starting -> Create
            ("<ShowAsyncImplement>d__", 5, 4),  // Showing -> Start
            ("<HideAsyncImplement>d__", 7, 6),  // Hiding -> Show
        };

        foreach (var (prefix, busyState, fallbackState) in transitions)
        {
            var smType = compType.NestedTypes.SingleOrDefault(t => t.Name.String.StartsWith(prefix, StringComparison.Ordinal))
                ?? throw new InvalidOperationException($"ComponentAction nested state machine {prefix}* not found");
            var thisField = smType.Fields.SingleOrDefault(f => f.Name == "<>4__this")
                ?? throw new InvalidOperationException($"{smType.Name}.<>4__this not found");
            var moveNext = smType.Methods.SingleOrDefault(m => m.Name == "MoveNext" && m.HasBody)
                ?? throw new InvalidOperationException($"{smType.Name}.MoveNext not found");
            var body = moveNext.Body;

            var eh = body.ExceptionHandlers.SingleOrDefault(h =>
                h.HandlerType == ExceptionHandlerType.Catch
                && h.CatchType?.FullName == "System.Exception")
                ?? throw new InvalidOperationException($"{smType.Name}.MoveNext catch(Exception) not found");

            body.SimplifyMacros(moveNext.Parameters);
            var start = eh.HandlerStart;
            int startIdx = body.Instructions.IndexOf(start);
            if (startIdx < 0 || !start.IsStloc() || startIdx + 1 >= body.Instructions.Count)
                throw new InvalidOperationException($"{smType.Name}.MoveNext unexpected catch start {start.OpCode}");

            var anchor = body.Instructions[startIdx + 1];
            var skipPop = OpCodes.Pop.ToInstruction();
            var guard = new[]
            {
                OpCodes.Ldarg_0.ToInstruction(),
                Instruction.Create(OpCodes.Ldfld, thisField),
                OpCodes.Dup.ToInstruction(),
                Instruction.Create(OpCodes.Ldfld, stateField),
                Instruction.CreateLdcI4(busyState),
                Instruction.Create(OpCodes.Bne_Un, skipPop),
                Instruction.CreateLdcI4(fallbackState),
                Instruction.Create(OpCodes.Stfld, stateField),
                Instruction.Create(OpCodes.Br, anchor),
                skipPop,
            };

            int insertIdx = startIdx + 1;
            for (int k = 0; k < guard.Length; k++)
                body.Instructions.Insert(insertIdx + k, guard[k]);

            body.MaxStack = Math.Max(body.MaxStack, (ushort)4);
            body.OptimizeMacros();
            log.Add($"ComponentAction/{smType.Name}.MoveNext: reset state {busyState} -> {fallbackState} on exception");
        }
    }
}
