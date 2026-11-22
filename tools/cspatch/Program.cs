// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-55: patch the client's C# script assemblies the way the login pak patches the JS ones, so the
// C# build (Sharphereal) can start against the offline server.
//
// Usage: cspatch <ScriptAssemblies dir with the shipped Aki.*.dll> <output dir> [rbauto.dll]
//
// With rbauto.dll (tools/rbauto, built against the game's assemblies), the type RB.RBAuto is
// copied into Aki.Game.dll and BulletCollisionSystem.InDodge calls it (auto dodge, R-55). That
// step rewrites Aki.Game.dll with dnlib, keeping every metadata token (PreserveAll).
//
// Shipped assemblies carry an 11-byte zero prefix before "MZ"; it is kept in the output.
// Patches overwrite IL bytes in place (same length), so metadata tokens and the assembly
// layout stay exactly as shipped. dnlib is used only to find where each method body is.
using dnlib.DotNet;
using dnlib.DotNet.Emit;

//
// Options (off unless given; R-94/R-95 must be measured with the R-95 PerfSight tap first):
//   --r94     R94PerfFixes (Aki.Game hot paths)
//   --r95off  R95TelemetryOff: disables offline telemetry and background scans to lower CPU load. PerfSight
//             itself stays on when rbauto.dll is given (its data goes to RBAuto, R95PerfSightTap).
var options = args.Where(a => a.StartsWith("--")).ToHashSet();
var positional = args.Where(a => !a.StartsWith("--")).ToArray();
if (positional.Length is not (2 or 3) || options.Except(new[] { "--r94", "--r95off" }).Any())
{
    Console.Error.WriteLine("usage: cspatch <ScriptAssemblies dir> <output dir> [rbauto.dll] [--r94] [--r95off]");
    return 2;
}
var inDir = positional[0];
var outDir = positional[1];
var rbauto = positional.Length == 3 ? positional[2] : null;
var withR94 = options.Contains("--r94");
var withR95Off = options.Contains("--r95off");
var perfTap = rbauto != null;
Directory.CreateDirectory(outDir);
var failed = 0;

failed += Patch("Aki.Launcher.dll", (module, edit) =>
{
    // As the login pak does to BaseConfigController.js: a shipping build accepts only https:// CDN
    // prefixes, so http://127.0.0.1:5500 was "包内无效前缀" and the launcher stopped.
    // Make the prefix checks see a non-shipping build (http:// and https:// both allowed).
    var n = 0;
    foreach (var typeName in new[] { "CSharpScript.Launcher.BaseConfig.BaseConfigController", "CSharpScript.Launcher.SoPatch.SoPatchStatic" })
        n += edit.ReplaceCalls(Find(module, typeName), "KuroApplication", "IsBuildShipping", new byte[] { 0x16, 0, 0, 0, 0 }); // ldc.i4.0 + 4 nop
    return n >= 2 ? null : $"expected 2 IsBuildShipping calls, patched {n}";
}, rbauto == null ? null : pe => Inject.PlainConfig(pe, new List<string>(), rbauto)); // R-173c: plain config JSON

failed += Patch("Aki.Game.dll", (module, edit) =>
{
    // As the login pak does to PakManager.js: our patch paks fail the client's file SHA1 check; never show its dialog.
    var t = Find(module, "CSharpScript.Game.Manager.PakManager");
    edit.Stub(Method(t, "ProcessingSha1CheckResult"), new byte[] { 0x2A }); // ret

    // Client tweaks for the offline game (the names before each colon are the old JS patches'):
    // motorcycle.js: the motorcycle is always allowed.
    edit.Stub(Method(FindByName(module, "VehicleController"), "CheckMotorAllowed"), new byte[] { 0x17, 0x2A }); // ldc.i4.1; ret
    // flight_fix.js: soaring is always allowed: return (true, null), reusing the method's own tuple constructor.
    var soar = Method(FindByName(module, "CharacterGlideComponent"), "CheckSoarAllowed");
    var tupleCtor = soar.Body.Instructions.First(i => i.OpCode == OpCodes.Newobj && i.Operand is IMethod c && c.DeclaringType.Name.StartsWith("ValueTuple"));
    var token = BitConverter.GetBytes(((IMethod)tupleCtor.Operand).MDToken.Raw);
    edit.Stub(soar, new byte[] { 0x17, 0x14, 0x73, token[0], token[1], token[2], token[3], 0x2A }); // ldc.i4.1; ldnull; newobj; ret
    // censorshipfix.js: no see-through dither when the camera gets close.
    edit.Stub(Method(FindByName(module, "CharacterDitherEffectController"), "SetDitherEffect"), new byte[] { 0x2A });
    // main_watermask_disable.js: no watermark.
    edit.Stub(Method(FindByName(module, "WaterMaskView"), "CreateWaterMask"), new byte[] { 0x2A });

    // LoginDebugView (when R-145's login does not apply): the view saves the server address with "http://" and the
    // port stripped (ParseServerIpAndPort), and GetServerIp prefers that saved "127.0.0.1" next time,
    // so the second start logged in on the wrong port (404 on /api/login). Ignore the saved value:
    // GetServerIp's `global == "-1"` test always passes and it returns the server list's address.
    var getIp = Method(FindByName(module, "LoginDebugView"), "GetServerIp");
    var eq = getIp.Body.Instructions.Single(i => i.OpCode == OpCodes.Call && i.Operand is IMethod c && c.Name == "op_Equality");
    edit.Write(getIp, eq.Offset, new byte[] { 0x26, 0x26, 0x17, 0x00, 0x00 }); // pop; pop; ldc.i4.1; nop; nop
    return null;
}, pe =>
{
    var (nextPe, log) = rbauto == null ? (pe, new List<string>()) : Inject.AutoDodge(pe, rbauto);
    if (perfTap) (nextPe, log) = R95PerfSightTap.Apply(nextPe, log);
    (nextPe, log) = R87Fixes.Apply(nextPe, log);
    if (withR94) (nextPe, log) = R94PerfFixes.Apply(nextPe, log);
    if (withR95Off) (nextPe, log) = R95TelemetryOff.ApplyGame(nextPe, log, keepPerfSight: perfTap);
    return (nextPe, log);
});

// R-55 component index cache, with R-94's change (missing components cached too); 3.7 only, 3.8
// caches the index itself. With --r95off the Core CrashSight proxies (PerfSight too when there is no tap).
failed += Patch("Aki.Core.dll", (_, _) => null, pe =>
{
    var (nextPe, log) = Inject.ComponentIndexCache(pe);
    // R-173b: a ProtoKeyResponse.Type 0 session runs without encryption (no gateway key needed).
    if (rbauto != null) (nextPe, log) = Inject.PlainSession(nextPe, log, rbauto);
    if (withR95Off) (nextPe, log) = R95TelemetryOff.ApplyCore(nextPe, log, keepPerfSight: perfTap);
    return (nextPe, log);
});

return failed == 0 ? 0 : 1;

static TypeDef Find(ModuleDef module, string fullName) =>
    module.Find(fullName, isReflectionName: false) ?? throw new Exception($"type {fullName} not found");

static TypeDef FindByName(ModuleDef module, string name) =>
    module.GetTypes().Where(t => t.Name == name && !t.IsNested).ToList() is { Count: 1 } one
        ? one[0] : throw new Exception($"type {name}: not exactly one");

static MethodDef Method(TypeDef type, string name) =>
    type.Methods.Where(m => m.Name == name && m.HasBody).ToList() is { Count: 1 } one
        ? one[0] : throw new Exception($"{type.FullName}.{name}: not exactly one method with a body");

int Patch(string name, Func<ModuleDefMD, Editor, string?> apply, Func<byte[], (byte[] pe, List<string> log)>? rewrite = null)
{
    var raw = File.ReadAllBytes(Path.Combine(inDir, name));
    var prefix = Array.IndexOf(raw, (byte)'M');
    while (prefix >= 0 && !(raw[prefix + 1] == (byte)'Z')) prefix = Array.IndexOf(raw, (byte)'M', prefix + 1);
    for (var i = 0; i < prefix; i++) if (raw[i] != 0) throw new Exception($"{name}: unexpected prefix");
    var pe = raw.AsSpan(prefix).ToArray();
    var edit = new Editor(ModuleDefMD.Load(pe), pe);
    var error = apply(edit.Module, edit);
    if (error != null)
    {
        Console.Error.WriteLine($"{name}: {error}");
        return 1;
    }
    if (rewrite != null)
    {
        var (rewritten, log) = rewrite(pe);
        pe = rewritten;
        edit.Log.AddRange(log);
    }
    var output = new byte[prefix + pe.Length];
    Array.Copy(raw, output, prefix);
    Array.Copy(pe, 0, output, prefix, pe.Length);
    File.WriteAllBytes(Path.Combine(outDir, name), output);
    Console.WriteLine($"{name}: {edit.Count} patch(es), {output.Length} bytes");
    foreach (var line in edit.Log) Console.WriteLine("  " + line);
    return 0;
}

sealed class Editor(ModuleDefMD module, byte[] pe)
{
    public ModuleDefMD Module => module;
    public int Count;
    public readonly List<string> Log = new();

    long CodeOffset(MethodDef m)
    {
        var body = m.Body ?? throw new Exception($"{m.FullName} has no body");
        return (long)module.Metadata.PEImage.ToFileOffset(m.RVA) + body.HeaderSize;
    }

    public void Write(MethodDef m, uint ilOffset, byte[] bytes)
    {
        var at = CodeOffset(m) + ilOffset;
        Array.Copy(bytes, 0, pe, at, bytes.Length);
        Count++;
        Log.Add($"{m.FullName} IL_{ilOffset:X4}: {Convert.ToHexString(bytes)}");
    }

    /// Replaces a whole method body: `bytes`, then nop (0x00) up to the body's old length, so
    /// every later byte still decodes as an instruction (a lone `ret` left the rest of the old IL
    /// behind, and dnlib's rewrite of 3.8's PakManager.ProcessingSha1CheckResult choked on it).
    public void Stub(MethodDef m, byte[] bytes)
    {
        var last = m.Body.Instructions[^1];
        var size = (int)last.Offset + last.GetSize();
        if (bytes.Length > size) throw new Exception($"{m.FullName}: stub longer than the body");
        var code = new byte[size];
        Array.Copy(bytes, code, bytes.Length);
        Write(m, 0, code);
    }

    /// Replaces each `call <type>::<method>` in the type and its nested types (async state
    /// machines hold the code) with `replacement` (same length as the call: 5 bytes).
    public int ReplaceCalls(TypeDef type, string calleeType, string calleeName, byte[] replacement)
    {
        var n = 0;
        foreach (var t in new[] { type }.Concat(type.GetTypes()))
            foreach (var m in t.Methods.Where(m => m.HasBody))
                foreach (var ins in m.Body.Instructions)
                    if (ins.OpCode == OpCodes.Call && ins.Operand is IMethod callee && callee.Name == calleeName && callee.DeclaringType.Name == calleeType)
                    {
                        Write(m, ins.Offset, replacement);
                        n++;
                    }
        return n;
    }
}
