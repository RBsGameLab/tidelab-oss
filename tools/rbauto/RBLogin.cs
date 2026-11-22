// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

// R-145: log in with the account our launcher passes, without the client's debug login view.
//
// With the SDK off (the login pak), LoginController.EnterLoginView opens LoginDebugView: a page
// of server, map, GM account and quick-login boxes the official client never shows. cspatch makes
// LoginController.EnterLoadArchive (the editor's "reload archive" quick login, called just before
// that view would open) call TryAutoLogin first. When the command line carries
// -RbAccount=<account>, this sets what LoginDebugView's login button sets (LoginDebugView.
// SetLoginData: server, account, sex) and opens LoginView, the official "click to enter" screen.
// Its button then runs LoginController.GetHttp in the non-SDK branch, as after the debug view.
//
//   -RbAccount=<account>   required; without it the debug view opens as before
//   -RbServer=<url>        optional; default the first server of the config server's list
//                          (what the debug view's server box shows first, http://127.0.0.1:10001)
//
// Status: rb_login.txt beside the other rbauto files (one line per decision).
// Same rules as RBAuto.cs: public game API only, no lambdas.
using System;
using CSharpScript.Game.Common;
using CSharpScript.Game.Ui;
using UnrealEngine;

namespace RB;

public static partial class RBAuto
{
    public static bool TryAutoLogin()
    {
        try
        {
            string commandLine = UKismetSystemLibrary.GetCommandLine() ?? "";
            string? account = CommandLineValue(commandLine, "-RbAccount=");
            if (account == null)
            {
                LoginNote("no -RbAccount: the debug login view opens");
                return false;
            }
            string? server = CommandLineValue(commandLine, "-RbServer=");
            string serverName = "tidelab";
            if (server == null)
            {
                var servers = LoginServerModel.Instance.GetLoginServersByClientRegion();
                if (servers == null || servers.Count == 0 || string.IsNullOrEmpty(servers[0].ip))
                {
                    LoginNote("account " + account + ": the config server listed no login server: the debug login view opens");
                    return false;
                }
                server = servers[0].ip;
                serverName = servers[0].name ?? serverName;
            }

            LoginModel model = LoginModel.Instance;
            model.InitConfig();
            model.SelectedAccountLoginMode = LoginDefine.EAccountLoginMode.NewAccount;
            model.SetServerName(serverName);
            model.SetServerIp(server, 1);
            model.SetAccount(account);
            // The debug view's sex toggle starts from this key (true = girl) and saves it back.
            bool girl = LocalStorage.GetGlobal(ELocalStorageGlobalKey.LoginSex, true);
            model.SetPlayerSex(girl ? LoginDefine.ELoginSex.Girl : LoginDefine.ELoginSex.Boy);
            UiManager.Instance.OpenView(EUiViewName.LoginView);
            LoginNote("account " + account + ", server " + server + " (" + serverName + "): LoginView");
            return true;
        }
        catch (Exception e)
        {
            LoginNote("failed, the debug login view opens: " + e.GetType().Name + ": " + e.Message);
            return false;
        }
    }

    /// The value of a `-Name=value` argument; arguments are separated by spaces (the client's own
    /// LoginModel.GetServerPort splits its command line the same way). Null when absent or empty.
    public static string? CommandLineValue(string commandLine, string prefix)
    {
        foreach (string arg in commandLine.Split(' '))
        {
            string a = arg.Trim('"');
            if (a.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
            {
                string value = a.Substring(prefix.Length);
                return value.Length == 0 ? null : value;
            }
        }
        return null;
    }

    private static void LoginNote(string text)
    {
        Append("rb_login.txt", DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + "\t" + text + "\r\n");
    }
}
