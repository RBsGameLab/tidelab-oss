// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using dnlib.DotNet;
using dnlib.DotNet.Emit;
using dnlib.DotNet.Writer;

internal static class IlHelpers
{
    public static TypeDef RequireType(ModuleDefMD module, string fullName)
        => module.GetTypes().Single(t => t.FullName == fullName || (string.IsNullOrEmpty(t.Namespace) && t.Name == fullName));

    public static MethodDef RequireMethod(ModuleDefMD module, string typeFullName, string methodName)
        => RequireMethod(RequireType(module, typeFullName), methodName);

    public static MethodDef RequireMethod(TypeDef owner, string methodName)
        => owner.Methods.Single(m => m.Name == methodName && m.HasBody);

    public static IMethod FindCall(MethodDef method, string declTypeName, string methodName)
    {
        foreach (var ins in method.Body.Instructions)
        {
            if (ins.Operand is IMethod m && m.Name == methodName && m.DeclaringType.Name == declTypeName)
                return m;
        }
        throw new Exception($"Missing call {declTypeName}.{methodName} in {method.FullName}");
    }

    public static void InsertAfter(IList<Instruction> ins, int index, params Instruction[] seq)
    {
        for (int k = 0; k < seq.Length; k++)
            ins.Insert(index + 1 + k, seq[k]);
    }

    public static void Nop(Instruction ins)
    {
        ins.OpCode = OpCodes.Nop;
        ins.Operand = null;
    }

    public static void RequireCount(string label, int actual, int expected)
    {
        if (actual != expected)
            throw new Exception($"{label}: expected {expected}, found {actual}");
    }

    public static Local? GetStoredLocal(Instruction ins, CilBody body) => ins.OpCode.Code switch
    {
        Code.Stloc_0 => body.Variables[0],
        Code.Stloc_1 => body.Variables[1],
        Code.Stloc_2 => body.Variables[2],
        Code.Stloc_3 => body.Variables[3],
        Code.Stloc or Code.Stloc_S => ins.Operand as Local,
        _ => null,
    };

    public static Local? GetLoadedLocal(Instruction ins, CilBody body) => ins.OpCode.Code switch
    {
        Code.Ldloc_0 => body.Variables[0],
        Code.Ldloc_1 => body.Variables[1],
        Code.Ldloc_2 => body.Variables[2],
        Code.Ldloc_3 => body.Variables[3],
        Code.Ldloc or Code.Ldloc_S => ins.Operand as Local,
        _ => null,
    };

    public static bool IsStloc(Instruction ins) =>
        ins.OpCode.Code is Code.Stloc or Code.Stloc_S or Code.Stloc_0 or Code.Stloc_1 or Code.Stloc_2 or Code.Stloc_3;

    public static bool IsLdloc(Instruction ins) =>
        ins.OpCode.Code is Code.Ldloc or Code.Ldloc_S or Code.Ldloc_0 or Code.Ldloc_1 or Code.Ldloc_2 or Code.Ldloc_3;

    public static bool IsLdloca(Instruction ins) =>
        ins.OpCode.Code is Code.Ldloca or Code.Ldloca_S;

    public static int FindNextEndFinally(IList<Instruction> ins, int fromIdx)
    {
        for (int i = fromIdx; i < ins.Count; i++)
            if (ins[i].OpCode == OpCodes.Endfinally)
                return i;
        throw new Exception("Missing endfinally");
    }

    public static byte[] WriteModule(ModuleDefMD module)
    {
        var options = new ModuleWriterOptions(module);
        options.MetadataOptions.Flags |= MetadataFlags.KeepOldMaxStack;
        using var stream = new MemoryStream();
        module.Write(stream, options);
        return stream.ToArray();
    }
}
