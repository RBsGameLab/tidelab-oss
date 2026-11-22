// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-173b: a gateway session without encryption, so a server needs no key matching the client's.
//
// Net (Aki.Core) gives the native KCP client a cipher per message id: Net.Initialize calls
// KcpClient.SetEnType(2 = Rsa, 111/112) for ProtoKeyRequest/Response, every other id keeps the
// native default (Aes). ProtoKeyResponse.Type only reaches Net.SetDynamicProtoKey, which hands the
// key to KcpClient.SetK(type, key) and changes no per-message cipher: with Type 0 the client still
// encrypted LoginRequest (R-173 Type 0 test, 2026-11-22). cspatch copies this type into
// Aki.Core.dll and makes Net.SetDynamicProtoKey call OnProtoKey(KcpClient, sType) first:
//   Type 0 (NoEncrypt): every other message id gets SetEnType 2, what 111/112 have, which are
//     plain on the wire both ways (our gateway reads 111 and writes 112 unencrypted). SetEnType 1
//     (the C# enum's NoEncrypt) did NOT stop the session cipher (real client, 2026-11-22 12:03);
//   Type 2 (Aes) after such a session in the same run: those ids go back to 0 (the default).
// Status: rb_net.txt beside the game exe (one line per switch).
// Same rules as RBAuto.cs: public API only, no lambdas. NetDefine is in Aki.Protocol, which
// Aki.Core references; UKuroKcpClient in UnrealEngine, which Aki.Core references too.
using System;
using System.IO;
using Aki.Protocol;
using UnrealEngine;

namespace RB;

public static class RBNet
{
    private const int ServerNoEncrypt = 0;
    private const byte ClientAes = 0;
    private const byte ClientWirePlain = 2; // as Net.Initialize gives ProtoKeyRequest/Response

    private static bool plain;

    public static void OnProtoKey(UKuroKcpClient? kcp, int serverType)
    {
        try
        {
            if (kcp == null) return;
            bool wantPlain = serverType == ServerNoEncrypt;
            if (wantPlain == plain) return;
            byte type = wantPlain ? ClientWirePlain : ClientAes;
            int n = SetAll(kcp, NetDefine.RequestMessageIds, type)
                + SetAll(kcp, NetDefine.ResponseMessageIds, type)
                + SetAll(kcp, NetDefine.PushMessageIds, type)
                + SetAll(kcp, NetDefine.NotifyMessageIds, type);
            plain = wantPlain;
            Note("server type " + serverType + ": " + n + " message ids set to " + (wantPlain ? "EnType 2 (plain, as 111/112)" : "EnType 0 (Aes)"));
        }
        catch (Exception e)
        {
            Note("OnProtoKey failed: " + e.GetType().Name + " " + e.Message);
        }
    }

    private static int SetAll(UKuroKcpClient kcp, EMessageId[] ids, byte type)
    {
        int n = 0;
        for (int i = 0; i < ids.Length; i++)
        {
            int id = (int)ids[i];
            // ProtoKeyRequest/Response keep what Net.Initialize gave them.
            if (id == (int)EMessageId.ProtoKeyRequest || id == (int)EMessageId.ProtoKeyResponse) continue;
            kcp.SetEnType(type, (short)id);
            n++;
        }
        return n;
    }

    private static void Note(string text)
    {
        try
        {
            string? exe = Environment.ProcessPath;
            string dir = exe == null ? AppContext.BaseDirectory : (Path.GetDirectoryName(exe) ?? "") + Path.DirectorySeparatorChar;
            File.AppendAllText(dir + "rb_net.txt", DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + "\t" + text + "\r\n");
        }
        catch (Exception)
        {
        }
    }
}
