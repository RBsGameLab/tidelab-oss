// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-173d: the keys of the client's encrypted paks from the official CDN, on the user's own machine.
//
// pakchunk101-114 and the Video_* paks need keys the launcher downloads each start (Aki.Launcher
// PakKeyUpdate): KeyList_<version>.json / KeyList.json, then client_key/<version>/<Random>/PakData,
// handed to the native UKuroPakKeyLibrary.LoadKeys. Both come from the CDN prefixes of the config
// index.json, which our login pak points at our config server; a server without the official
// files (a new user) has none of them. The official CDN list is inside the official index.json,
// encrypted with the client's config key, so only the client can read it.
//
// So, without anything leaving the client or going into the repository:
//   Start (first thing in RBCfg.Decrypt, the launcher's first config read, and in
//     PakKeyUpdate.Init, which runs in the same frame as the first KeyList request): reads the official config prefixes and UrlPath from
//     Config/Kuro/RbOfficial.ini (our login pak copies them from the client's own
//     KuroPublicConfig.ini) and requests <prefix>/<UrlPath>/index.json, the request the stock
//     launcher makes first;
//   Prefixes (every UrlPrefixSelector.GetAllPrefixList call in PakKeyUpdate): our prefixes, then
//     the official CdnUrl entries once that response is in (decrypted by the native Decrypt, as
//     the stock launcher does). The launcher tries the prefixes in turn and keeps the newest
//     KeyList, so a server that has the files still serves them; PakData is checked against the
//     KeyList's hash by the launcher itself.
// Nothing here reads or stores a key: KeyList and PakData go from the CDN to the launcher as before.
// Status: rb_keys.txt beside the game exe (hosts and counts only).
// Same rules as RBAuto.cs: public API only, no lambdas, no async.
using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using CSharpScript.Launcher.Download;
using Cysharp.Threading.Tasks;
using UnrealEngine;

namespace RB;

public static class RBKeys
{
    private static bool started;
    private static bool read;
    private static UniTask<IResponse> request;
    private static List<string>? official;
    private static string officialMixUri = "";

    public static void Start()
    {
        try
        {
            if (started) return;
            started = true;
            string ini = "";
            string path = UBlueprintPathsLibrary.ProjectConfigDir() + "Kuro/RbOfficial.ini";
            if (!UBlueprintPathsLibrary.FileExists(path) || !UKuroStaticLibrary.LoadFileToString(ref ini, path))
            {
                Note("no " + path + ": official CDN not used");
                read = true;
                return;
            }
            string prefix = FirstPrefix(IniValue(ini, "InternalPrefix"));
            string urlPath = IniValue(ini, "UrlPath");
            if (prefix.Length == 0 || urlPath.Length == 0)
            {
                Note("RbOfficial.ini without InternalPrefix or UrlPath");
                read = true;
                return;
            }
            string url = prefix.TrimEnd('/') + "/" + urlPath + "/index.json";
            Note("requesting official index.json from " + Host(url));
            request = UrlPrefixHttpRequest.httpRequest(url);
        }
        catch (Exception e)
        {
            read = true;
            Note("Start failed: " + e.GetType().Name + " " + e.Message);
        }
    }

    public static List<string> Prefixes(UrlPrefixSelector self, bool sorted)
    {
        List<string> list = self.GetAllPrefixList(sorted);
        try
        {
            List<string>? extra = Official();
            if (extra == null) return list;
            for (int i = 0; i < extra.Count; i++)
                if (!list.Contains(extra[i])) list.Add(extra[i]);
        }
        catch (Exception e)
        {
            Note("Prefixes failed: " + e.GetType().Name + " " + e.Message);
        }
        return list;
    }

    private static List<string>? Official()
    {
        if (read) return official;
        if (!started) return null;
        if (request.Status == UniTaskStatus.Pending)
        {
            Note("official index.json not answered yet");
            return null;
        }
        read = true;
        if (request.Status != UniTaskStatus.Succeeded)
        {
            Note("official index.json request " + request.Status);
            return null;
        }
        IResponse response = request.GetAwaiter().GetResult();
        if (response == null || response.Code != 200 || response.Result == null)
        {
            Note("official index.json: HTTP " + (response == null ? -1 : response.Code));
            return null;
        }
        string plain = "";
        if (!UKuroLauncherLibrary.Decrypt(response.Result, ref plain) || plain == null)
        {
            Note("official index.json: Decrypt failed (" + response.Result.Length + " chars)");
            return null;
        }
        official = CdnUrls(plain);
        string hosts = "";
        for (int i = 0; i < official.Count; i++) hosts += (i == 0 ? "" : ", ") + Host(official[i]);
        Note("official CDN: " + official.Count + " prefixes (" + hosts + "), MixUri " + officialMixUri);
        return official;
    }

    // Every entry of index.json ("default", "p1", ...): CdnUrl[].url with a weight above 0,
    // highest weight first; MixUri of the first entry that has one (logged only).
    private static List<string> CdnUrls(string json)
    {
        List<string> urls = new List<string>();
        List<int> weights = new List<int>();
        using (JsonDocument doc = JsonDocument.Parse(json))
        {
            foreach (JsonProperty entry in doc.RootElement.EnumerateObject())
            {
                if (entry.Value.ValueKind != JsonValueKind.Object) continue;
                JsonElement mix;
                if (officialMixUri.Length == 0 && entry.Value.TryGetProperty("MixUri", out mix) && mix.ValueKind == JsonValueKind.String)
                    officialMixUri = mix.GetString() ?? "";
                JsonElement cdn;
                if (!entry.Value.TryGetProperty("CdnUrl", out cdn) || cdn.ValueKind != JsonValueKind.Array) continue;
                foreach (JsonElement item in cdn.EnumerateArray())
                {
                    JsonElement u;
                    JsonElement w;
                    if (item.ValueKind != JsonValueKind.Object || !item.TryGetProperty("url", out u) || u.ValueKind != JsonValueKind.String) continue;
                    string url = u.GetString() ?? "";
                    int weight = 1;
                    if (item.TryGetProperty("weight", out w))
                    {
                        if (w.ValueKind == JsonValueKind.String) int.TryParse(w.GetString(), out weight);
                        else if (w.ValueKind == JsonValueKind.Number) w.TryGetInt32(out weight);
                    }
                    if (url.Length == 0 || weight <= 0 || urls.Contains(url)) continue;
                    int at = 0;
                    while (at < weights.Count && weights[at] >= weight) at++;
                    urls.Insert(at, url);
                    weights.Insert(at, weight);
                }
            }
        }
        return urls;
    }

    public static string IniValue(string ini, string key)
    {
        string[] lines = ini.Split('\n');
        for (int i = 0; i < lines.Length; i++)
        {
            string line = lines[i].Trim();
            if (line.StartsWith(key + "=", StringComparison.Ordinal)) return line.Substring(key.Length + 1).Trim();
        }
        return "";
    }

    public static string FirstPrefix(string list)
    {
        string[] parts = list.Split(';');
        for (int i = 0; i < parts.Length; i++)
            if (parts[i].Trim().Length > 0) return parts[i].Trim();
        return "";
    }

    private static string Host(string url)
    {
        int s = url.IndexOf("://", StringComparison.Ordinal);
        int start = s < 0 ? 0 : s + 3;
        int end = url.IndexOf('/', start);
        return end < 0 ? url.Substring(start) : url.Substring(start, end - start);
    }

    private static void Note(string text)
    {
        try
        {
            string? exe = Environment.ProcessPath;
            string dir = exe == null ? AppContext.BaseDirectory : (Path.GetDirectoryName(exe) ?? "") + Path.DirectorySeparatorChar;
            File.AppendAllText(dir + "rb_keys.txt", DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + "\t" + text + "\r\n");
        }
        catch (Exception)
        {
        }
    }
}
