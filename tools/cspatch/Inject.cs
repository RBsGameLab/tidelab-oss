// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-55: copy a helper type compiled against the game's assemblies into Aki.Game.dll and hook it.
using dnlib.DotNet;
using dnlib.DotNet.Emit;
using dnlib.DotNet.Writer;

static class Inject
{
    /// Copies RB.RBAuto from `helperPath` into the module and makes
    /// BulletCollisionSystem.InDodge(victim) = InDodge_RBOrig(victim) || RBAuto.TryAutoDodge(BulletInfo, victim).
    public static (byte[] pe, List<string> log) AutoDodge(byte[] pe, string helperPath)
    {
        var log = new List<string>();
        var module = ModuleDefMD.Load(pe);
        // The helper's references to Aki.Game must resolve to this module, so the importer
        // (TryToUseDefs) uses its definitions instead of making references back to the helper.
        var helper = ModuleDefMD.Load(File.ReadAllBytes(helperPath), new ModuleContext(new TargetResolver(module)));
        var copied = CopyType(module, helper.Find("RB.RBAuto", false) ?? throw new Exception("RB.RBAuto not in helper"));
        log.Add($"copied type {copied.FullName}: {copied.Fields.Count} fields, {copied.Methods.Count} methods");

        var bcs = module.GetTypes().Single(t => t.Name == "BulletCollisionSystem" && !t.IsNested);
        var inDodge = bcs.Methods.Single(m => m.Name == "InDodge" && m.HasBody);
        var bulletInfo = bcs.Fields.Single(f => f.Name == "BulletInfo");
        var tryAutoDodge = copied.Methods.Single(m => m.Name == "TryAutoDodge");

        // The original body moves to InDodge_RBOrig; callers keep calling InDodge (same token).
        var orig = new MethodDefUser("InDodge_RBOrig", inDodge.MethodSig.Clone(), inDodge.ImplAttributes, inDodge.Attributes);
        bcs.Methods.Add(orig);
        orig.Body = inDodge.Body;
        foreach (var ins in orig.Body.Instructions)
            if (ins.Operand is Parameter p) ins.Operand = orig.Parameters[p.Index];

        var body = new CilBody { MaxStack = 8 };
        var fallback = OpCodes.Ldarg_0.ToInstruction();
        foreach (var ins in new[]
        {
            OpCodes.Ldarg_0.ToInstruction(),
            OpCodes.Ldarg_1.ToInstruction(),
            OpCodes.Call.ToInstruction(orig),
            OpCodes.Brfalse_S.ToInstruction(fallback),
            OpCodes.Ldc_I4_1.ToInstruction(),
            OpCodes.Ret.ToInstruction(),
            fallback,
            OpCodes.Ldfld.ToInstruction(bulletInfo),
            OpCodes.Ldarg_1.ToInstruction(),
            OpCodes.Call.ToInstruction(tryAutoDodge),
            OpCodes.Ret.ToInstruction(),
        })
            body.Instructions.Add(ins);
        inDodge.Body = body;
        log.Add($"{inDodge.FullName}: calls InDodge_RBOrig, then RB.RBAuto.TryAutoDodge");

        // R-55: rb_cvars.txt (console commands) is polled from the game's own tick.
        var game = module.Find("CSharpScript.Game.Game", false) ?? throw new Exception("CSharpScript.Game.Game not found");
        var gameTick = game.Methods.Single(m => m.Name == "Tick" && m.HasBody && m.Parameters.Count == 2);
        gameTick.Body.Instructions.Insert(0, OpCodes.Call.ToInstruction(copied.Methods.Single(m => m.Name == "OnGameTick")));
        log.Add($"{gameTick.FullName}: first calls RB.RBAuto.OnGameTick");

        // R-75: combat metering. Every damage the client settles (its own and the server's reply)
        // goes through BaseDamageComponent.ExecDamageEvent(result, inputParam, requirements), so
        // RBAuto.OnDamage(this, result, inputParam, requirements) sees the numbers the player sees.
        var damageComp = module.GetTypes().Single(t => t.Name == "BaseDamageComponent" && !t.IsNested);
        var execDamage = damageComp.Methods.Single(m => m.Name == "ExecDamageEvent" && m.HasBody);
        if (execDamage.Parameters.Count != 4) throw new Exception($"ExecDamageEvent takes {execDamage.Parameters.Count} parameters, expected this + 3");
        var onDamage = copied.Methods.Single(m => m.Name == "OnDamage");
        foreach (var (at, ins) in new[]
        {
            (0, OpCodes.Ldarg_0.ToInstruction()),
            (1, OpCodes.Ldarg_1.ToInstruction()),
            (2, OpCodes.Ldarg_2.ToInstruction()),
            (3, OpCodes.Ldarg_3.ToInstruction()),
            (4, OpCodes.Call.ToInstruction(onDamage)),
        })
            execDamage.Body.Instructions.Insert(at, ins);
        // KeepOldMaxStack is on, so the body keeps this value: the call pushes 4 arguments.
        if (execDamage.Body.MaxStack < 4) execDamage.Body.MaxStack = 8;
        log.Add($"{execDamage.FullName}: first calls RB.RBAuto.OnDamage (max stack {execDamage.Body.MaxStack})");

        // R-75: the enemy's parry windows, as autocombat.js patched them in JS. Both notify
        // states run on the enemy's own animation; the helper presses 攻击 / the echo skill.
        foreach (var (notify, handler) in new[]
        {
            ("TsAnimNotifyStateCounterAttack", "OnCounterWindow"),
            ("TsAnimNotifyStateVisionCounterAttack", "OnVisionCounterWindow"),
        })
        {
            var type = module.GetTypes().Single(t => t.Name == notify && !t.IsNested);
            var begin = type.Methods.Single(m => m.Name == "K2_NotifyBegin_Implementation" && m.HasBody);
            begin.Body.Instructions.Insert(0, OpCodes.Ldarg_1.ToInstruction());
            begin.Body.Instructions.Insert(1, OpCodes.Call.ToInstruction(copied.Methods.Single(m => m.Name == handler)));
            if (begin.Body.MaxStack < 1) begin.Body.MaxStack = 8;
            log.Add($"{begin.FullName}: first calls RB.RBAuto.{handler}");
        }

        // ---- the remaining client tweaks (named after the old JS patches) ----
        MethodDef Helper(string name) => copied.Methods.Single(m => m.Name == name);
        TypeDef Type(string name) => module.GetTypes().Where(t => t.Name == name && !t.IsNested).ToList() is { Count: 1 } one
            ? one[0] : throw new Exception($"type {name}: not exactly one");

        // red_dot_remover.js: RedDotData.StateByGm starts false (State is then always false), every
        // red dot widget is hidden when it registers, and the title list shows no red dots.
        var redDot = Type("RedDotData");
        foreach (var name in new[] { "CreateStaticDefaultValue", "ResetStaticDefaultValue" })
        {
            var ins = redDot.Methods.Single(m => m.Name == name).Body.Instructions;
            var n = 0;
            for (var i = 1; i < ins.Count; i++)
                if (ins[i].OpCode == OpCodes.Stsfld && ((IField)ins[i].Operand).Name == "StateByGm" && ins[i - 1].OpCode == OpCodes.Ldc_I4_1)
                {
                    ins[i - 1].OpCode = OpCodes.Ldc_I4_0;
                    n++;
                }
            if (n != 1) throw new Exception($"RedDotData.{name}: StateByGm = true not found once");
        }
        var setUiItem = redDot.Methods.Single(m => m.Name == "SetUiItem");
        setUiItem.Body.Instructions.Insert(0, OpCodes.Ldarg_1.ToInstruction());
        setUiItem.Body.Instructions.Insert(1, OpCodes.Call.ToInstruction(Helper("HideUiItem")));
        var titleRedDot = Type("PersonalPlayerTitleData").Methods.Single(m => m.Name == "GetIsShowRedDot");
        titleRedDot.Body = new CilBody { MaxStack = 1 };
        titleRedDot.Body.Instructions.Add(OpCodes.Ldc_I4_0.ToInstruction());
        titleRedDot.Body.Instructions.Add(OpCodes.Ret.ToInstruction());
        log.Add("red dots: StateByGm false, SetUiItem hides, PersonalPlayerTitleData.GetIsShowRedDot false");

        // chat_limit.js: chat input takes 777777 characters (config chat_character).
        foreach (var name in new[] { "ChatView", "QuickChatView" })
        {
            var n = 0;
            foreach (var m in Type(name).Methods.Where(m => m.HasBody && !m.IsStatic))
            {
                var ins = m.Body.Instructions;
                for (var i = 0; i < ins.Count; i++)
                {
                    if (ins[i].OpCode != OpCodes.Stfld || ((IField)ins[i].Operand).Name != "ChatInputMaxNum") continue;
                    var field = ins[i].Operand;
                    ins.Insert(i + 1, OpCodes.Ldarg_0.ToInstruction());
                    ins.Insert(i + 2, OpCodes.Ldc_I4.ToInstruction(777777));
                    ins.Insert(i + 3, new Instruction(OpCodes.Stfld, field));
                    i += 3;
                    n++;
                }
            }
            if (n == 0) throw new Exception($"{name}: no ChatInputMaxNum store");
            log.Add($"{name}: ChatInputMaxNum forced to 777777 after {n} store(s)");
        }

        // debug_disable.js: hide the position (text 0) and time (text 4) lines after each tick.
        var panel = Type("PositionPanel");
        var afterTick = panel.Methods.Single(m => m.Name == "OnAfterTickBattleChildViewPanel" && m.HasBody);
        var positionText = panel.Fields.Single(f => f.Name == "PositionText");
        var timeText = panel.Fields.Single(f => f.Name == "TimeTextOfficial");
        BeforeEachReturn(afterTick, () => new[]
        {
            OpCodes.Ldarg_0.ToInstruction(), OpCodes.Ldfld.ToInstruction(positionText),
            OpCodes.Ldarg_0.ToInstruction(), OpCodes.Ldfld.ToInstruction(timeText),
            OpCodes.Call.ToInstruction(Helper("HideTexts")),
        });
        log.Add("PositionPanel: position and time texts hidden after each tick");

        // R-156 (uid_watermark.js): the UID line shows RBAuto.UidText() instead of the player id.
        // Each method writes the id with LguiUtil.SetLocalText(GetText(i), key, [id]); at its end
        // the helper writes the same text entry again with our text:
        //   RBAuto.SetUidText(this.GetText(i), key)
        foreach (var (typeName, methodName, index, key) in new[]
        {
            ("UidView", "OnStart", 0, "FriendMyUid"),
            ("PersonalRootView", "SetUid", 1, "UserId"),
            ("PhotoSaveMarkItem", "OnStart", 2, "FriendMyUid"),
        })
        {
            var m = Type(typeName).Methods.Single(x => x.Name == methodName && x.HasBody && !x.IsStatic);
            if (!m.Body.Instructions.Any(i => i.OpCode == OpCodes.Ldstr && (string)i.Operand == key))
                throw new Exception($"{m.FullName}: text entry {key} not found");
            var getText = m.Body.Instructions.Select(i => i.Operand).OfType<IMethod>().FirstOrDefault(x => x.Name == "GetText")
                ?? throw new Exception($"{m.FullName}: no GetText call");
            BeforeEachReturn(m, () => new[]
            {
                OpCodes.Ldarg_0.ToInstruction(), OpCodes.Ldc_I4.ToInstruction(index), OpCodes.Call.ToInstruction(getText),
                OpCodes.Ldstr.ToInstruction(key), OpCodes.Call.ToInstruction(Helper("SetUidText")),
            });
            log.Add($"{m.FullName}: UID text {key} rewritten with RB.RBAuto.UidText()");
        }

        // motorcycle.js: leave the vehicle before a teleport.
        var teleportCore = Type("TeleportCore");
        var teleports = teleportCore.Methods.Where(m => m.HasBody && (m.Name == "TeleportPlayerWithLoading" || m.Name == "TeleportPlayerNoLoading")).ToList();
        if (teleports.Count < 2) throw new Exception("TeleportCore teleport methods not found");
        foreach (var m in teleports) m.Body.Instructions.Insert(0, OpCodes.Call.ToInstruction(Helper("LeaveVehicle")));
        log.Add($"TeleportCore: {teleports.Count} teleport methods first call RB.RBAuto.LeaveVehicle");

        // R-145: log in with the account our launcher passes (-RbAccount=). With the SDK off,
        // LoginController.EnterLoginView calls EnterLoadArchive (the editor's quick login) and
        // opens LoginDebugView when it returns false. EnterLoadArchive now first asks
        // RBAuto.TryAutoLogin, which sets the login data and opens LoginView itself:
        //   if (RBAuto.TryAutoLogin()) return true; <original body>
        var enterArchive = Type("LoginController").Methods.Single(m => m.Name == "EnterLoadArchive" && m.HasBody);
        if (enterArchive.MethodSig.RetType.ElementType != ElementType.Boolean || enterArchive.Parameters.Count != 1)
            throw new Exception($"{enterArchive.FullName}: expected an instance method returning bool with no arguments");
        var archiveFirst = enterArchive.Body.Instructions[0];
        foreach (var (at, ins) in new[]
        {
            (0, OpCodes.Call.ToInstruction(Helper("TryAutoLogin"))),
            (1, OpCodes.Brfalse.ToInstruction(archiveFirst)),
            (2, OpCodes.Ldc_I4_1.ToInstruction()),
            (3, OpCodes.Ret.ToInstruction()),
        })
            enterArchive.Body.Instructions.Insert(at, ins);
        if (enterArchive.Body.MaxStack < 1) enterArchive.Body.MaxStack = 8;
        log.Add($"{enterArchive.FullName}: returns true at once when RB.RBAuto.TryAutoLogin opened LoginView");

        var options = new ModuleWriterOptions(module);
        // KeepOldMaxStack: the in-place "return at once" patches leave unreachable old code after
        // the ret, which dnlib cannot compute a max stack for. New bodies set MaxStack themselves.
        // Tokens are not preserved: PreserveAll needs pointer tables (#- stream) to add a method to
        // an existing type, and the client finds managed methods by name, not by token.
        options.MetadataOptions.Flags |= MetadataFlags.KeepOldMaxStack;
        using var stream = new MemoryStream();
        module.Write(stream, options);
        var output = stream.ToArray();

        // Nothing may point back at the helper: a ModuleRef or a reference into rbauto would fail
        // to load inside the game.
        var check = ModuleDefMD.Load(output);
        var bad = check.GetTypeRefs().Where(r => r.ResolutionScope is ModuleRef || r.ResolutionScope is AssemblyRef a && a.Name == helper.Assembly.Name)
            .Select(r => "TypeRef " + r.FullName + " scope " + r.ResolutionScope)
            .Concat(check.GetModuleRefs().Select(m => "ModuleRef " + m.Name))
            .ToList();
        if (bad.Count > 0) throw new Exception("references back to the helper:\n  " + string.Join("\n  ", bad));
        log.Add($"checked: no references back to {helper.Name}; tables stream {check.Metadata.TablesStream.Name}");
        return (output, log);
    }

    /// R-173c, Aki.Launcher: copies RB.RBCfg from `helperPath` and points every call to
    /// UKuroLauncherLibrary.Decrypt(string, ref string) at RBCfg.Decrypt, which passes plain JSON
    /// through and hands anything else to the native Decrypt (tools/rbauto/RBCfg.cs).
    public static (byte[] pe, List<string> log) PlainConfig(byte[] pe, List<string> log, string helperPath)
    {
        var module = ModuleDefMD.Load(pe);
        var helper = ModuleDefMD.Load(File.ReadAllBytes(helperPath), new ModuleContext(new TargetResolver(module)));
        var copied = CopyType(module, helper.Find("RB.RBCfg", false) ?? throw new Exception("RB.RBCfg not in helper"));
        var decrypt = copied.Methods.Single(m => m.Name == "Decrypt");
        var calls = 0;
        foreach (var type in module.GetTypes().Where(t => t != copied).ToList())
            foreach (var m in type.Methods.Where(m => m.HasBody))
                foreach (var ins in m.Body.Instructions)
                    if (ins.OpCode == OpCodes.Call && ins.Operand is IMethod target && target.Name == "Decrypt"
                        && target.DeclaringType?.FullName == "UnrealEngine.UKuroLauncherLibrary" && target.MethodSig.Params.Count == 2)
                    {
                        ins.Operand = decrypt;
                        calls++;
                    }
        // 18 call sites in the 3.8 Aki.Launcher (BaseConfig, DiffPatch, HotPatch, PreDownload,
        // PakKeyUpdate, ResourceUpdate, ResPackageInfo); fewer means the client changed.
        if (calls < 18) throw new Exception($"expected at least 18 UKuroLauncherLibrary.Decrypt calls, found {calls}");
        log.Add($"copied type {copied.FullName}; {calls} UKuroLauncherLibrary.Decrypt calls now go to RB.RBCfg.Decrypt");
        OfficialPakKeys(module, helper, log);
        return (WriteChecked(module, helper, log), log);
    }

    /// R-173d, Aki.Launcher: copies RB.RBKeys; PakKeyUpdate.Init(AppPathMisc) first calls
    /// RBKeys.Start (requests the official index.json), and every
    /// UrlPrefixSelector.GetAllPrefixList(bool) call in PakKeyUpdate and its async state machines
    /// goes to RBKeys.Prefixes(selector, sorted): ours, then the official CDN (tools/rbauto/RBKeys.cs).
    static void OfficialPakKeys(ModuleDefMD module, ModuleDefMD helper, List<string> log)
    {
        var copied = CopyType(module, helper.Find("RB.RBKeys", false) ?? throw new Exception("RB.RBKeys not in helper"));
        var start = copied.Methods.Single(m => m.Name == "Start");
        var prefixes = copied.Methods.Single(m => m.Name == "Prefixes");
        var pakKey = module.Find("CSharpScript.Launcher.Update.PakKeyUpdate", false) ?? throw new Exception("PakKeyUpdate not found");
        var init = pakKey.Methods.Single(m => m.Name == "Init" && m.HasBody && m.Parameters.Count == 2);
        init.Body.Instructions.Insert(0, OpCodes.Call.ToInstruction(start));
        // Init runs in the same frame as the first CheckPakKey (real client 2026-11-22 15:05:39:
        // KeyList_3.8.0.json asked before the official index.json was in, only the video keys got
        // the official CDN), so the request also starts at the launcher's first config read.
        var cfgDecrypt = (module.Find("RB.RBCfg", false) ?? throw new Exception("RB.RBCfg must be copied first")).Methods.Single(m => m.Name == "Decrypt");
        cfgDecrypt.Body.Instructions.Insert(0, OpCodes.Call.ToInstruction(start));
        var calls = 0;
        foreach (var type in new[] { pakKey }.Concat(pakKey.GetTypes()))
            foreach (var m in type.Methods.Where(m => m.HasBody))
                foreach (var ins in m.Body.Instructions)
                    if ((ins.OpCode == OpCodes.Callvirt || ins.OpCode == OpCodes.Call) && ins.Operand is IMethod target && target.Name == "GetAllPrefixList"
                        && target.DeclaringType?.FullName == "CSharpScript.Launcher.Download.UrlPrefixSelector" && target.MethodSig.Params.Count == 1)
                    {
                        ins.OpCode = OpCodes.Call;
                        ins.Operand = prefixes;
                        calls++;
                    }
        // KeyList, PakData, video KeyList, VideoPakData: 4 in the 3.8 client.
        if (calls < 4) throw new Exception($"expected at least 4 GetAllPrefixList calls in PakKeyUpdate, found {calls}");
        log.Add($"copied type {copied.FullName}; {init.FullName} and RB.RBCfg.Decrypt first call RBKeys.Start; {calls} GetAllPrefixList calls in PakKeyUpdate go to RBKeys.Prefixes");
    }

    static byte[] WriteChecked(ModuleDefMD module, ModuleDefMD helper, List<string> log)
    {
        var options = new ModuleWriterOptions(module);
        options.MetadataOptions.Flags |= MetadataFlags.KeepOldMaxStack;
        using var stream = new MemoryStream();
        module.Write(stream, options);
        var output = stream.ToArray();
        var check = ModuleDefMD.Load(output);
        var bad = check.GetTypeRefs().Where(r => r.ResolutionScope is ModuleRef || r.ResolutionScope is AssemblyRef a && a.Name == helper.Assembly.Name)
            .Select(r => "TypeRef " + r.FullName + " scope " + r.ResolutionScope)
            .Concat(check.GetModuleRefs().Select(m => "ModuleRef " + m.Name))
            .ToList();
        if (bad.Count > 0) throw new Exception("references back to the helper:\n  " + string.Join("\n  ", bad));
        log.Add($"checked: no references back to {helper.Name}; assembly refs {string.Join(", ", check.GetAssemblyRefs().Select(a => a.Name.String))}");
        return output;
    }

    /// R-173b, Aki.Core: copies RB.RBNet from `helperPath` and makes Net.SetDynamicProtoKey(sType, key)
    /// first call RBNet.OnProtoKey(this.KcpClient, (int)sType), which switches every message id's
    /// cipher to NoEncrypt when the server answered ProtoKeyResponse.Type 0 (tools/rbauto/RBNet.cs).
    public static (byte[] pe, List<string> log) PlainSession(byte[] pe, List<string> log, string helperPath)
    {
        var module = ModuleDefMD.Load(pe);
        var helper = ModuleDefMD.Load(File.ReadAllBytes(helperPath), new ModuleContext(new TargetResolver(module)));
        var copied = CopyType(module, helper.Find("RB.RBNet", false) ?? throw new Exception("RB.RBNet not in helper"));
        log.Add($"copied type {copied.FullName}: {copied.Fields.Count} fields, {copied.Methods.Count} methods");

        var net = module.Find("Net", false) ?? throw new Exception("Net not found");
        var setKey = net.Methods.Single(m => m.Name == "SetDynamicProtoKey" && m.HasBody && m.Parameters.Count == 3);
        var kcpField = net.Fields.Single(f => f.Name == "KcpClient");
        var onProtoKey = copied.Methods.Single(m => m.Name == "OnProtoKey");
        var at = 0;
        foreach (var ins in new[]
        {
            OpCodes.Ldarg_0.ToInstruction(),
            OpCodes.Ldfld.ToInstruction(kcpField),
            OpCodes.Ldarg_1.ToInstruction(),
            OpCodes.Call.ToInstruction(onProtoKey),
        })
            setKey.Body.Instructions.Insert(at++, ins);
        if (setKey.Body.MaxStack < 2) setKey.Body.MaxStack = 8;
        log.Add($"{setKey.FullName}: first calls RB.RBNet.OnProtoKey(KcpClient, sType)");

        var options = new ModuleWriterOptions(module);
        options.MetadataOptions.Flags |= MetadataFlags.KeepOldMaxStack;
        using var stream = new MemoryStream();
        module.Write(stream, options);
        var output = stream.ToArray();

        var check = ModuleDefMD.Load(output);
        var bad = check.GetTypeRefs().Where(r => r.ResolutionScope is ModuleRef || r.ResolutionScope is AssemblyRef a && a.Name == helper.Assembly.Name)
            .Select(r => "TypeRef " + r.FullName + " scope " + r.ResolutionScope)
            .Concat(check.GetModuleRefs().Select(m => "ModuleRef " + m.Name))
            .ToList();
        if (bad.Count > 0) throw new Exception("references back to the helper:\n  " + string.Join("\n  ", bad));
        log.Add($"checked: no references back to {helper.Name}; assembly refs {string.Join(", ", check.GetAssemblyRefs().Select(a => a.Name.String))}");
        return (output, log);
    }

    /// Aki.Core: ComponentIndexGetter.IndexOf&lt;T&gt;() ran GetByType(typeof(T).Name, true) on every
    /// Entity.GetComponent&lt;T&gt;(): a Mono icall that makes a new string (RuntimeType.GetName), then
    /// a Dictionary&lt;string,int&gt; lookup (string hash and compare), for an index that never changes
    /// for a type. Now the index sits in RB.ComponentIndexCache&lt;T&gt;.IndexPlusOne (0 = not known
    /// yet; -1 results are not cached, as before). Profile tidelab_prof_5752_1: string, dictionary
    /// and ComponentIndexGetter self time about 0.7% of the GameThread, plus the icall and garbage.
    public static (byte[] pe, List<string> log) ComponentIndexCache(byte[] pe)
    {
        var log = new List<string>();
        var module = ModuleDefMD.Load(pe);
        // 3.8 does this itself: ComponentIndexGetter<T>.Index is a generic static, looked up once per type (R-107).
        if (module.GetTypes().Any(t => t.Name == "ComponentIndexGetter`1" && !t.IsNested))
            return (pe, new List<string> { "ComponentIndexGetter<T>.Index already caches each type's index (3.8): not patched" });
        var getter = module.GetTypes().Single(t => t.Name == "ComponentIndexGetter" && !t.IsNested);
        var indexOf = getter.Methods.Single(m => m.Name == "IndexOf" && m.GenericParameters.Count == 1);
        var getByType = getter.Methods.Single(m => m.Name == "GetByType" && m.Parameters.Count == 2);
        var compIndexField = getter.Fields.Single(f => f.Name == "ComponentIndex");
        var hookConstType = module.GetTypes().Single(t => t.Name == "EntityComponentConstant" && !t.IsNested);
        var hookField = hookConstType.Fields.Single(f => f.Name == "ComponentTypeToIndexHook");

        var old = indexOf.Body.Instructions;
        if (old.Count != 6 || old[0].OpCode != OpCodes.Ldtoken || old[4].OpCode != OpCodes.Call || ((IMethod)old[4].Operand).Name != "GetByType")
            throw new Exception("IndexOf<T> is not `return GetByType(typeof(T).Name, true)` any more");

        var int32 = module.CorLibTypes.Int32;
        var cache = new TypeDefUser("RB", "ComponentIndexCache`1", module.CorLibTypes.Object.TypeDefOrRef)
        {
            Attributes = TypeAttributes.Public | TypeAttributes.Abstract | TypeAttributes.Sealed | TypeAttributes.BeforeFieldInit | TypeAttributes.Class,
        };
        cache.GenericParameters.Add(new GenericParamUser(0, GenericParamAttributes.NonVariant, "T"));
        // Encoded as `index + 2` (0 = uncached, 1 = missing component -1, >=2 = valid index) so misses are also cached once Hook is set.
        cache.Fields.Add(new FieldDefUser("IndexPlusTwo", new FieldSig(int32), FieldAttributes.Public | FieldAttributes.Static));
        module.Types.Add(cache);
        var field = new MemberRefUser(module, "IndexPlusTwo", new FieldSig(int32),
            new TypeSpecUser(new GenericInstSig(new ClassSig(cache), new GenericMVar(0, indexOf))));

        var body = new CilBody { InitLocals = true, MaxStack = 4 };
        var index = body.Variables.Add(new Local(int32, "index"));
        var slow = OpCodes.Pop.ToInstruction();
        var done = OpCodes.Ldloc.ToInstruction(index);
        foreach (var ins in new[]
        {
            OpCodes.Ldsfld.ToInstruction(field),
            OpCodes.Dup.ToInstruction(),
            OpCodes.Brfalse_S.ToInstruction(slow),
            OpCodes.Ldc_I4_2.ToInstruction(),
            OpCodes.Sub.ToInstruction(),
            OpCodes.Ret.ToInstruction(),
            slow,
            new Instruction(OpCodes.Ldtoken, old[0].Operand),
            new Instruction(OpCodes.Call, old[1].Operand),
            new Instruction(OpCodes.Callvirt, old[2].Operand),
            OpCodes.Ldc_I4_1.ToInstruction(),
            new Instruction(OpCodes.Call, old[4].Operand),
            OpCodes.Stloc.ToInstruction(index),
            OpCodes.Ldsfld.ToInstruction(hookField),
            OpCodes.Brfalse_S.ToInstruction(done),
            OpCodes.Ldloc.ToInstruction(index),
            OpCodes.Ldc_I4_2.ToInstruction(),
            OpCodes.Add.ToInstruction(),
            OpCodes.Stsfld.ToInstruction(field),
            done,
            OpCodes.Ret.ToInstruction(),
        })
            body.Instructions.Add(ins);
        indexOf.Body = body;
        log.Add($"{indexOf.FullName}: cached hits and -1 misses in RB.ComponentIndexCache`1<T>.IndexPlusTwo");

        // Also cache Entity.GetComponent(Type) / AddComponent lookups (`useCache: false`) and `-1` misses once Hook is active.
        var gbtOld = getByType.Body.Instructions;
        var hookInvoke = gbtOld.Select(i => i.Operand).OfType<IMethod>().First(m => m.Name == "Invoke" && m.DeclaringType.Name.StartsWith("Func"));
        var dictTryGet = gbtOld.Select(i => i.Operand).OfType<IMethod>().Single(m => m.Name == "TryGetValue");
        var dictCtor = gbtOld.Select(i => i.Operand).OfType<IMethod>().Single(m => m.Name == ".ctor" && m.DeclaringType.Name.StartsWith("Dictionary"));
        var dictSetItem = gbtOld.Select(i => i.Operand).OfType<IMethod>().Single(m => m.Name == "set_Item");

        var gbtBody = new CilBody { InitLocals = true, MaxStack = 4 };
        var gbtIndex = gbtBody.Variables.Add(new Local(int32, "index"));
        var gbtHook = gbtBody.Variables.Add(new Local(hookField.FieldType, "hook"));
        var gbtDict = gbtBody.Variables.Add(new Local(compIndexField.FieldType, "dict"));
        var haveDict = OpCodes.Stloc.ToInstruction(gbtDict);
        var miss = OpCodes.Ldloc.ToInstruction(gbtHook);
        var retMinusOne = OpCodes.Ldc_I4_M1.ToInstruction();
        foreach (var ins in new[]
        {
            OpCodes.Ldsfld.ToInstruction(hookField),
            OpCodes.Stloc.ToInstruction(gbtHook),
            OpCodes.Ldloc.ToInstruction(gbtHook),
            OpCodes.Brfalse_S.ToInstruction(retMinusOne),
            OpCodes.Ldarg_0.ToInstruction(),
            OpCodes.Brfalse_S.ToInstruction(retMinusOne),
            OpCodes.Ldsfld.ToInstruction(compIndexField),
            OpCodes.Dup.ToInstruction(),
            OpCodes.Brtrue_S.ToInstruction(haveDict),
            OpCodes.Pop.ToInstruction(),
            OpCodes.Newobj.ToInstruction(dictCtor),
            OpCodes.Dup.ToInstruction(),
            OpCodes.Stsfld.ToInstruction(compIndexField),
            haveDict,
            OpCodes.Ldloc.ToInstruction(gbtDict),
            OpCodes.Ldarg_0.ToInstruction(),
            OpCodes.Ldloca.ToInstruction(gbtIndex),
            OpCodes.Callvirt.ToInstruction(dictTryGet),
            OpCodes.Brfalse_S.ToInstruction(miss),
            OpCodes.Ldloc.ToInstruction(gbtIndex),
            OpCodes.Ret.ToInstruction(),
            miss,
            OpCodes.Ldarg_0.ToInstruction(),
            OpCodes.Callvirt.ToInstruction(hookInvoke),
            OpCodes.Stloc.ToInstruction(gbtIndex),
            OpCodes.Ldloc.ToInstruction(gbtDict),
            OpCodes.Ldarg_0.ToInstruction(),
            OpCodes.Ldloc.ToInstruction(gbtIndex),
            OpCodes.Callvirt.ToInstruction(dictSetItem),
            OpCodes.Ldloc.ToInstruction(gbtIndex),
            OpCodes.Ret.ToInstruction(),
            retMinusOne,
            OpCodes.Ret.ToInstruction(),
        })
            gbtBody.Instructions.Add(ins);
        getByType.Body = gbtBody;
        log.Add($"{getByType.FullName}: unified dictionary cache including useCache=false and -1 misses");

        var options = new ModuleWriterOptions(module);
        options.MetadataOptions.Flags |= MetadataFlags.KeepOldMaxStack;
        using var stream = new MemoryStream();
        module.Write(stream, options);
        return (stream.ToArray(), log);
    }

    /// Runs `make()`'s instructions before every `ret` of a void method. The ret instruction object
    /// itself becomes the first new instruction, so branches that targeted it still land there.
    static void BeforeEachReturn(MethodDef m, Func<Instruction[]> make)
    {
        var ins = m.Body.Instructions;
        foreach (var ret in ins.Where(i => i.OpCode == OpCodes.Ret).ToList())
        {
            var seq = make();
            ret.OpCode = seq[0].OpCode;
            ret.Operand = seq[0].Operand;
            var at = ins.IndexOf(ret);
            for (var k = 1; k < seq.Length; k++) ins.Insert(at + k, seq[k]);
            ins.Insert(at + seq.Length, OpCodes.Ret.ToInstruction());
        }
    }

    sealed class TargetResolver(ModuleDef target) : IAssemblyResolver
    {
        public AssemblyDef? Resolve(IAssembly assembly, ModuleDef sourceModule) =>
            assembly.Name == target.Assembly.Name ? target.Assembly : null;
    }

    /// A type the helper references in Aki.Game becomes the target's own TypeDef (member
    /// references then hang off that TypeDef and are found by name and signature at run time).
    sealed class TargetMapper(ModuleDef target) : ImportMapper
    {
        public override ITypeDefOrRef? Map(ITypeDefOrRef source)
        {
            if (source is TypeRef tr && tr.DefinitionAssembly?.Name == target.Assembly.Name)
                return target.Find(tr) ?? throw new Exception($"{tr.FullName} not found in {target.Name}");
            return null;
        }
    }

    static TypeDef CopyType(ModuleDef target, TypeDef source)
    {
        var importer = new Importer(target, ImporterOptions.TryToUseDefs, default, new TargetMapper(target));
        var type = new TypeDefUser(source.Namespace, source.Name, importer.Import(source.BaseType)) { Attributes = source.Attributes };
        target.Types.Add(type);

        var fields = new Dictionary<FieldDef, FieldDef>();
        foreach (var f in source.Fields)
        {
            var nf = new FieldDefUser(f.Name, importer.Import(f.FieldSig), f.Attributes);
            if (f.HasConstant) nf.Constant = new ConstantUser(f.Constant.Value, f.Constant.Type);
            type.Fields.Add(nf);
            fields[f] = nf;
        }
        var methods = new Dictionary<MethodDef, MethodDef>();
        foreach (var m in source.Methods)
        {
            var nm = new MethodDefUser(m.Name, importer.Import(m.MethodSig), m.ImplAttributes, m.Attributes);
            type.Methods.Add(nm);
            methods[m] = nm;
        }
        foreach (var (m, nm) in methods)
        {
            if (!m.HasBody) continue;
            var body = new CilBody { InitLocals = m.Body.InitLocals, MaxStack = m.Body.MaxStack };
            var locals = new Dictionary<Local, Local>();
            foreach (var l in m.Body.Variables) locals[l] = body.Variables.Add(new Local(importer.Import(l.Type), l.Name));
            var map = new Dictionary<Instruction, Instruction>();
            foreach (var ins in m.Body.Instructions)
            {
                var copy = new Instruction(ins.OpCode, ins.Operand);
                map[ins] = copy;
                body.Instructions.Add(copy);
            }
            foreach (var ins in body.Instructions)
            {
                ins.Operand = ins.Operand switch
                {
                    Instruction t => map[t],
                    Instruction[] ts => ts.Select(t => map[t]).ToArray(),
                    Local l => locals[l],
                    Parameter p => nm.Parameters[p.Index],
                    FieldDef f when fields.ContainsKey(f) => fields[f],
                    MethodDef md when methods.ContainsKey(md) => methods[md],
                    IField f => importer.Import(f),
                    IMethod mm => importer.Import(mm),
                    ITypeDefOrRef t => importer.Import(t),
                    var other => other,
                };
            }
            foreach (var eh in m.Body.ExceptionHandlers)
            {
                body.ExceptionHandlers.Add(new ExceptionHandler(eh.HandlerType)
                {
                    TryStart = map[eh.TryStart],
                    TryEnd = eh.TryEnd == null ? null : map[eh.TryEnd],
                    HandlerStart = map[eh.HandlerStart],
                    HandlerEnd = eh.HandlerEnd == null ? null : map[eh.HandlerEnd],
                    FilterStart = eh.FilterStart == null ? null : map[eh.FilterStart],
                    CatchType = eh.CatchType == null ? null : importer.Import(eh.CatchType),
                });
            }
            nm.Body = body;
        }
        return type;
    }
}
