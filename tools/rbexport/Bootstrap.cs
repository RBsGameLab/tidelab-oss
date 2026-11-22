// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-174d: copies the files tidelab needs out of the client's paks, from inside the client.
//
// Loaded by the patch DLL (client/patch/src/export.rs) once the game's Aki.Launcher is loaded;
// Run(folder) is called on the patch's own thread. The engine reads pak files through its file
// layer, so UKuroStaticLibrary.CopyFile copies a pak file to disk like any other: no key. The
// engine types are found by name (this assembly references nothing of the game). Each entry of
// Items is copied to <folder>/<path under Client/>; <folder>/rb_export.txt gets one line per entry
// and "done" at the end (or "error"). The patch DLL ends the client afterwards.
using System;
using System.Collections;
using System.IO;
using System.Linq;
using System.Reflection;

namespace RB;

public static class Bootstrap
{
    // Directory under Client/, file filter, recursive.
    public static readonly string[] Items =
    {
        "Content/Aki/ScriptAssemblies|*.dll|0",
        "Content/Aki/ConfigDB|*.db|1",
        "Content/Aki/JavaScript/Core/Define|*.js|1",
        "Content/Aki/Config/UniverseEditorConfig/Quests|*.bytes|0",
        "Content/Aki/Config/UniverseEditorConfig/LevelPlays|*.bytes|0",
        // Every config ini (R-174b: the gameplay tag list for GameplayTagParent / GameplayTagRemap).
        "Config|*.ini|1",
        "Config/Kuro|KuroPublicConfig.ini|0",
        "Plugins/TpSafe/Config|DefaultTpSafe.ini|0",
        "Content/Aki/JavaScript/Launcher/BaseConfig|BaseDefine.js|0",
        "Content/Aki/JavaScript/Launcher/BaseConfig|BaseConfigController.js|0",
        "Content/Aki/JavaScript/Launcher/HotPatchKuroSdk|LauncherSdk.js|0",
        "Content/Aki/JavaScript/Game/Manager|PakManager.js|0",
        "Content/Aki/JavaScript/Game/Module/Debug/View|LoginDebugView.js|0",
    };

    private const string ClientRoot = "../../../Client/";

    public static void Run(string folder)
    {
        folder = folder.Trim('"').Replace('\\', '/').TrimEnd('/') + "/";
        string report = folder + "rb_export.txt";
        try
        {
            Directory.CreateDirectory(folder);
            File.WriteAllText(report, "");
            Engine e = Engine.Find();
            int total = 0, failed = 0;
            // R-174b research: <folder>/extra_items.txt adds "dir under Client/|filter|recursive" lines.
            string extra = folder + "extra_items.txt";
            string[] items = File.Exists(extra) ? Items.Concat(File.ReadAllLines(extra).Where(l => l.Count(c => c == '|') == 2)).ToArray() : Items;
            // R-174 setup, second run: <folder>/skip_copy.txt skips the file copy (only the in-game tasks below run).
            if (File.Exists(folder + "skip_copy.txt")) items = Array.Empty<string>();
            foreach (string item in items)
            {
                string[] parts = item.Split('|');
                string dir = ClientRoot + parts[0];
                int copied = 0, bad = 0;
                if (!parts[1].Contains('*'))
                {
                    string one = dir + "/" + parts[1];
                    if (e.FileExists(one) && e.Copy(one, folder + parts[0] + "/" + parts[1])) copied++;
                    else bad++;
                }
                else
                {
                    foreach (string file in e.List(dir, parts[1], parts[2] == "1"))
                    {
                        string src = file.Replace('\\', '/');
                        if (!src.Contains('/')) src = dir + "/" + src;
                        if (!Matches(Path.GetFileName(src), parts[1])) continue;
                        int at = src.IndexOf("Client/", StringComparison.Ordinal);
                        string rel = at < 0 ? parts[0] + "/" + Path.GetFileName(src) : src.Substring(at + "Client/".Length);
                        if (e.Copy(src, folder + rel)) copied++;
                        else bad++;
                    }
                }
                File.AppendAllText(report, parts[0] + "/" + parts[1] + "\t" + copied + (bad > 0 ? "\tfailed " + bad : "") + "\r\n");
                total += copied;
                failed += bad;
            }
            File.AppendAllText(report, "done\t" + total + " files, " + failed + " failed\r\n");
            // R-174b research: <folder>/probe_classes.txt lists blueprint class paths to probe.
            string probe = folder + "probe_classes.txt";
            if (File.Exists(probe)) Probe.Run(folder, File.ReadAllLines(probe).Where(l => l.Trim().Length > 0).ToArray());
            // R-174b: <folder>/parts_models.txt (ModelId TAB class path) -> CharacterPartConfig.json.
            string dumpClasses = folder + "dump_classes.txt";
            if (File.Exists(dumpClasses)) Dump.Run(folder, File.ReadAllLines(dumpClasses).Where(l => l.Trim().Length > 0).ToArray());
            string tagNames = folder + "tag_names.txt";
            if (File.Exists(tagNames)) Tags.Run(folder, File.ReadAllLines(tagNames));
            string partModels = folder + "parts_models.txt";
            if (File.Exists(partModels)) Parts.Run(folder, File.ReadAllLines(partModels));
        }
        catch (Exception ex)
        {
            try { File.AppendAllText(report, "error\t" + ex + "\r\n"); } catch (Exception) { }
        }
    }

    public static bool Matches(string name, string filter) =>
        filter.StartsWith("*", StringComparison.Ordinal)
            ? name.EndsWith(filter.Substring(1), StringComparison.OrdinalIgnoreCase)
            : string.Equals(name, filter, StringComparison.OrdinalIgnoreCase);

    /// The engine's file functions (UnrealEngine.UKuroStaticLibrary, UBlueprintPathsLibrary), by name.
    private sealed class Engine
    {
        private MethodInfo copyFile = null!, getFiles = null!, getFilesRecursive = null!, fileExists = null!;

        public static Engine Find()
        {
            Assembly ue = AppDomain.CurrentDomain.GetAssemblies().FirstOrDefault(a => a.GetName().Name == "UnrealEngine")
                ?? throw new InvalidOperationException("UnrealEngine assembly not loaded");
            Type kuro = ue.GetType("UnrealEngine.UKuroStaticLibrary", true)!;
            Type paths = ue.GetType("UnrealEngine.UBlueprintPathsLibrary", true)!;
            return new Engine
            {
                copyFile = Method(kuro, "CopyFile", 2),
                getFiles = Method(kuro, "GetFiles", 2),
                getFilesRecursive = Method(kuro, "GetFilesRecursive", 4),
                fileExists = Method(paths, "FileExists", 1),
            };
        }

        private static MethodInfo Method(Type t, string name, int args) =>
            t.GetMethods(BindingFlags.Public | BindingFlags.Static).FirstOrDefault(m => m.Name == name && m.GetParameters().Length == args)
            ?? throw new MissingMethodException(t.FullName, name);

        public bool FileExists(string path) => (bool)fileExists.Invoke(null, new object[] { path })!;

        public IEnumerable List(string dir, string filter, bool recursive) =>
            (IEnumerable)(recursive
                ? getFilesRecursive.Invoke(null, new object[] { dir, filter, true, false })
                : getFiles.Invoke(null, new object[] { dir, filter }))!;

        public bool Copy(string src, string dst)
        {
            string? parent = Path.GetDirectoryName(dst);
            if (parent != null) Directory.CreateDirectory(parent);
            return (bool)copyFile.Invoke(null, new object[] { src, dst })! && File.Exists(dst);
        }
    }
}
