// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-173c: config files from our config server without the client's config key.
//
// The launcher (Aki.Launcher) reads every config file from the CDN through
// UKuroLauncherLibrary.Decrypt(cipher, ref plain): native AES-CBC with a key built into the
// client. A server without that key (a new user, tools that never take secrets out of the
// client) serves the JSON in the clear. cspatch copies this type into Aki.Launcher.dll and points
// every call to UKuroLauncherLibrary.Decrypt there at RBCfg.Decrypt:
//   text that starts like a JSON object or array (IsPlainJson) is returned as is;
//   anything else (Base64 cipher, the official server's form) goes to the native Decrypt.
// Same rules as RBAuto.cs: public API only, no lambdas.
using UnrealEngine;

namespace RB;

public static class RBCfg
{
    public static bool Decrypt(string InCipher, ref string OutPlain)
    {
        if (IsPlainJson(InCipher))
        {
            OutPlain = InCipher;
            return true;
        }
        return UKuroLauncherLibrary.Decrypt(InCipher, ref OutPlain);
    }

    // '{' must open an object ('"' or '}' next), '[' an array (a JSON value or ']' next):
    // Config/Kuro/RConfig.ini starts "[Section]" and is not JSON (2026-11-22 real client: taken
    // as JSON, RemoteVersionConfig failed to parse and the launcher stopped).
    public static bool IsPlainJson(string? text)
    {
        if (text == null) return false;
        int i = SkipSpace(text, 0);
        if (i >= text.Length) return false;
        char open = text[i];
        if (open != '{' && open != '[') return false;
        int j = SkipSpace(text, i + 1);
        if (j >= text.Length) return false;
        char next = text[j];
        if (open == '{') return next == '"' || next == '}';
        return next == '{' || next == '[' || next == '"' || next == ']' || next == '-'
            || (next >= '0' && next <= '9') || next == 't' || next == 'f' || next == 'n';
    }

    private static int SkipSpace(string text, int i)
    {
        while (i < text.Length)
        {
            char c = text[i];
            if (c != ' ' && c != '\t' && c != '\r' && c != '\n' && c != '\uFEFF') break;
            i++;
        }
        return i;
    }
}
