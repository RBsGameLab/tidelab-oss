// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

using System;
using System.Collections.Generic;
using dnlib.DotNet;
using dnlib.DotNet.Emit;

/// <summary>
/// Offline telemetry and background scan silencer to lower CPU load.
/// </summary>
internal static class R95TelemetryOff
{
    // keepPerfSight (R-95 on 3.8, user 2026-11-18): PerfSight stays on; R95PerfSightTap sends
    // what it collects to RBAuto instead. Only the rest (reports, scans, CrashSight) is cut.
    public static (byte[] pe, List<string> log) ApplyCore(byte[] pe, List<string> log, bool keepPerfSight)
    {
        var module = ModuleDefMD.Load(pe);

        if (!keepPerfSight) DisableCorePerfSight(module, log);

        var crashProxy = IlHelpers.RequireType(module, "CSharpScript.Typing.FCrashSightProxy");
        MakeVoidNoOps(crashProxy,
            "TestCriticalError",
            "Test",
            "SetUserId",
            "SetCustomDataCache",
            "SetCustomDataByFName",
            "SetCustomData",
            "SetBranchInfo",
            "ReportException");
        log.Add("Aki.Core FCrashSightProxy: 8 void reporting helpers no-oped");

        return (IlHelpers.WriteModule(module), log);
    }

    private static void DisableCorePerfSight(ModuleDefMD module, List<string> log)
    {
        var perfSight = IlHelpers.RequireType(module, "PerfSight");
        FlipEnableInitToFalse(IlHelpers.RequireMethod(perfSight, ".ctor"), "IsEnable");
        MakeReturnBool(IlHelpers.RequireMethod(perfSight, "Initialize"), value: true);
        MakeVoidNoOps(perfSight,
            "SetPcAppVersion",
            "SetVersionIden",
            "PostEvent",
            "MarkLevelLoad",
            "MarkLevelFin",
            "MarkLevelLoadCompleted",
            "SetUserId",
            "PostNetworkLatency");
        log.Add("Aki.Core PerfSight: IsEnable = false, Initialize() & reporting helpers no-oped");
    }

    public static (byte[] pe, List<string> log) ApplyGame(byte[] pe, List<string> log, bool keepPerfSight)
    {
        var module = ModuleDefMD.Load(pe);

        // 1. PerfSightController
        if (!keepPerfSight)
        {
            var perfCtrl = IlHelpers.RequireType(module, "PerfSightController");
            FlipEnableInitToFalse(IlHelpers.RequireMethod(perfCtrl, ".cctor"), "IsEnable");
            MakeReturnBool(IlHelpers.RequireMethod(perfCtrl, "OnInit"), value: true);
            MakeReturnBool(IlHelpers.RequireMethod(perfCtrl, "OnClear"), value: true);
            MakeVoidNoOps(perfCtrl, "OnTick");
            log.Add("PerfSightController: IsEnable = false, OnInit/OnClear/OnTick disabled");
        }

        // 2. CharacterController.TestMessage & CharacterShadowController.TrySendMessage
        SilenceCharacterControllerScan(module, log);
        SilenceCharacterShadowControllerScan(module, log);

        // 3. Telemetry and log reports (lower CPU load)
        var logCtrl = IlHelpers.RequireType(module, "LogReportController");
        MakeReturnBool(IlHelpers.RequireMethod(logCtrl, "OnInit"), value: false);
        MakeReturnBool(IlHelpers.RequireMethod(logCtrl, "OnClear"), value: true);
        MakeVoidNoOps(logCtrl, "LogReport", "UnitLogReport", "CheckLogReport");

        MakeVoidNoOps(IlHelpers.RequireType(module, "LogReportModel"), "RecordOperateTime");
        MakeVoidNoOps(IlHelpers.RequireType(module, "RealThinkingAnalyticsReporter"), "Init", "Report");
        MakeVoidNoOps(IlHelpers.RequireType(module, "RealKRAnalyticsReporter"), "Init", "Report");
        MakeVoidNoOps(IlHelpers.RequireType(module, "KuroSdkReport"), "Init", "Report");
        MakeVoidNoOps(IlHelpers.RequireType(module, "AntiCheatController"), "OnAddEvents", "OnRemoveEvents");

        var gamepadLog = IlHelpers.RequireType(module, "GamepadLogReportController");
        MakeVoidNoOps(gamepadLog, "OnAddEvents");
        MakeReturnBool(IlHelpers.RequireMethod(gamepadLog, "OnClear"), value: true);

        MakeVoidNoOps(IlHelpers.RequireType(module, "UiInteractLogReport"),
            "ReportSpaceKeyInteract",
            "RecordRouletteOpen",
            "RecordRouletteClose",
            "RecordChatOpen",
            "RecordChatClose");
        log.Add("Telemetry & LogReport: LogReportController, ThinkingAnalytics, RealKRAnalytics, KuroSdkReport, AntiCheatController, GamepadLog, UiInteractLog disabled");

        // 4. Background SDK and crash-collection hooks (lower CPU load)
        MakeVoidNoOps(IlHelpers.RequireType(module, "ThirdPartySdkManager"),
            "Init",
            "SetUserInfo",
            "SetUserInfoForTpSafe",
            "InitDataTransferTimerForTpSafe",
            "SendAceData",
            "AceAntiDataNotify",
            "Logout",
            "Clear");

        var aceCtrl = IlHelpers.RequireType(module, "AceAntiCheatController");
        MakeReturnBool(IlHelpers.RequireMethod(aceCtrl, "OnInit"), value: true);
        MakeReturnBool(IlHelpers.RequireMethod(aceCtrl, "OnClear"), value: true);
        MakeVoidNoOps(aceCtrl, "OnTick");

        var crashCtrl = IlHelpers.RequireType(module, "CrashCollectionController");
        MakeReturnBool(IlHelpers.RequireMethod(crashCtrl, "OnInit"), value: true);
        MakeReturnBool(IlHelpers.RequireMethod(crashCtrl, "OnClear"), value: true);
        MakeVoidNoOps(crashCtrl, "OnAddEvents", "OnRemoveEvents", "RecordHttpInfo");
        log.Add("ThirdPartySdkManager / AceAntiCheatController / CrashCollectionController: C# hooks & notifications disabled");

        return (IlHelpers.WriteModule(module), log);
    }

    private static void FlipEnableInitToFalse(MethodDef method, string fieldName)
    {
        var ins = method.Body.Instructions;
        int flipped = 0;
        for (int i = 1; i < ins.Count; i++)
        {
            if (ins[i - 1].OpCode == OpCodes.Ldc_I4_1 &&
                (ins[i].OpCode == OpCodes.Stfld || ins[i].OpCode == OpCodes.Stsfld) &&
                ins[i].Operand is IField f && f.Name == fieldName)
            {
                ins[i - 1].OpCode = OpCodes.Ldc_I4_0;
                flipped++;
            }
        }
        IlHelpers.RequireCount($"{method.FullName} {fieldName}=true->false", flipped, 1);
    }

    private static void SilenceCharacterControllerScan(ModuleDefMD module, List<string> log)
    {
        var cc = IlHelpers.RequireType(module, "CharacterController");
        MakeReturnBool(IlHelpers.RequireMethod(cc, "OnInit"), value: true);

        var onTick = IlHelpers.RequireMethod(cc, "OnTick");
        onTick.Body.SimplifyBranches();
        var ins = onTick.Body.Instructions;
        int cutIdx = -1;
        for (int i = 0; i + 4 < ins.Count; i++)
        {
            if (ins[i].OpCode == OpCodes.Call && ins[i].Operand is IMethod m1 && m1.Name == "get_Instance" &&
                ins[i + 1].OpCode == OpCodes.Callvirt && ins[i + 1].Operand is IMethod m2 && m2.Name == "IsFinishLogin" &&
                ins[i + 4].OpCode == OpCodes.Call && ins[i + 4].Operand is IMethod m3 && m3.Name == "TestMessage")
            {
                // Preserve ins[i] in place as the branch target of `brtrue` after IsQueueEmpty(), turn it into `ret`.
                ins[i].OpCode = OpCodes.Ret;
                ins[i].Operand = null;
                cutIdx = i + 1;
                break;
            }
        }
        if (cutIdx < 0)
            throw new Exception($"{onTick.FullName}: unexpected TestMessage tail");
        while (ins.Count > cutIdx)
            ins.RemoveAt(ins.Count - 1);
        onTick.Body.OptimizeBranches();
        log.Add($"{onTick.FullName}: removed Net.IsFinishLogin -> TestMessage() scan");
    }

    private static void SilenceCharacterShadowControllerScan(ModuleDefMD module, List<string> log)
    {
        var cs = IlHelpers.RequireType(module, "CharacterShadowController");

        var onInit = IlHelpers.RequireMethod(cs, "OnInit");
        var initIns = onInit.Body.Instructions;
        int clearedInit = 0;
        for (int i = 0; i + 3 < initIns.Count; i++)
        {
            if (initIns[i].OpCode == OpCodes.Ldarg_0 &&
                initIns[i + 1].OpCode == OpCodes.Call && initIns[i + 1].Operand is IMethod m && m.Name == "get_Now" &&
                initIns[i + 2].OpCode == OpCodes.Newobj &&
                initIns[i + 3].OpCode == OpCodes.Stfld && initIns[i + 3].Operand is IField f && f.Name == "LastDate")
            {
                for (int k = 0; k < 4; k++)
                    IlHelpers.Nop(initIns[i + k]);
                clearedInit++;
            }
        }
        IlHelpers.RequireCount($"{onInit.FullName} LastDate=DateTime.Now", clearedInit, 1);

        var onTick = IlHelpers.RequireMethod(cs, "OnTick");
        onTick.Body.SimplifyBranches();
        var tickIns = onTick.Body.Instructions;
        if (tickIns.Count < 6 ||
            tickIns[0].OpCode != OpCodes.Call ||
            tickIns[1].OpCode != OpCodes.Callvirt || ((IMethod)tickIns[1].Operand).Name != "IsFinishLogin" ||
            tickIns[2].OpCode != OpCodes.Brfalse ||
            tickIns[4].OpCode != OpCodes.Call || ((IMethod)tickIns[4].Operand).Name != "TrySendMessage")
        {
            throw new Exception($"{onTick.FullName}: unexpected TrySendMessage preamble");
        }
        for (int k = 0; k < 6; k++)
            IlHelpers.Nop(tickIns[k]);
        onTick.Body.OptimizeBranches();
        log.Add($"{onTick.FullName}: removed DateTime.Now init and TrySendMessage() scan");
    }

    private static void MakeReturnBool(MethodDef method, bool value)
    {
        var body = new CilBody { InitLocals = false, MaxStack = 1 };
        body.Instructions.Add((value ? OpCodes.Ldc_I4_1 : OpCodes.Ldc_I4_0).ToInstruction());
        body.Instructions.Add(OpCodes.Ret.ToInstruction());
        method.Body = body;
    }

    private static void MakeVoidNoOps(TypeDef type, params string[] methodNames)
    {
        foreach (var name in methodNames)
        {
            var m = IlHelpers.RequireMethod(type, name);
            if (m.ReturnType.ElementType != ElementType.Void)
                throw new Exception($"{m.FullName} is not void");
            var body = new CilBody { InitLocals = false, MaxStack = 1 };
            body.Instructions.Add(OpCodes.Ret.ToInstruction());
            m.Body = body;
        }
    }
}
