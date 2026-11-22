#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Build the client's login pak, rb38_login_100_p.pak, from the 3.8 client's own files (R-146).

It points the client at the local servers and turns off what an offline client cannot pass:

  KuroPublicConfig.ini     InternalPrefix -> four http://127.0.0.1:5500 entries, UseSDK=0,
                           CheckEntryTime=0
  RbOfficial.ini (new)     the original InternalPrefix and UrlPath lines (R-173d, RB.RBKeys)
  DefaultTpSafe.ini        Enable=False
  BaseDefine.js            USESDK "0"
  BaseConfigController.js  http:// prefixes accepted in a shipping build
  LauncherSdk.js           canUseSdk = () => false
  PakManager.js            Eih (the periodic pak sha1 check and its quit box) does nothing
  LoginDebugView.js        closing the view tolerates the missing input box 20

The JS files matter only when the client runs without its C# build
(-ForceDisableCSharpEnvironment); the C# build gets the same changes from tools/cspatch, and
logs in through R-145 (tools/rbauto/RBLogin.cs) without the debug login view.

    python -I tools/build_loginpak.py [--game <Client\\Content\\Paks>] [--pakx <pakx.dll>] [--out <pak>]

--game  the 3.8 client's paks (default the lab's Game38); only the official ones are read
--pakx  the pak exporter (default the lab's tools/pakx build)
--out   default out\\paks\\rb38_login_100_p.pak in this repository (start-game.cmd installs it)
--from  the client's own export (tidelab_start.exe -RbExport=<folder>, R-174): no pakx, no pak key

Every replacement must hit exactly once, so a client update that changed a file stops the build
instead of producing a broken pak.
"""
from __future__ import annotations

import argparse
import hashlib
import os
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pak_build  # noqa: E402
import tl_paths  # noqa: E402

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LAB = str(tl_paths.LAB)  # R-171
DEFAULT_GAME = os.path.join(str(tl_paths.GAME), r"Client\Content\Paks")
DEFAULT_PAKX = os.path.join(LAB, r"tools\pakx\bin\Debug\net10.0\pakx.dll")
LOCAL = "http://127.0.0.1:5500"
JS = "Client/Content/Aki/JavaScript/"

INI_PUBLIC = "Client/Config/Kuro/KuroPublicConfig.ini"
INI_OFFICIAL = "Client/Config/Kuro/RbOfficial.ini"  # R-173d, read by RB.RBKeys (tools/rbauto)
INI_TPSAFE = "Client/Plugins/TpSafe/Config/DefaultTpSafe.ini"
BASE_DEFINE = JS + "Launcher/BaseConfig/BaseDefine.js"
BASE_CONFIG = JS + "Launcher/BaseConfig/BaseConfigController.js"
LAUNCHER_SDK = JS + "Launcher/HotPatchKuroSdk/LauncherSdk.js"
PAK_MANAGER = JS + "Game/Manager/PakManager.js"
LOGIN_DEBUG = JS + "Game/Module/Debug/View/LoginDebugView.js"
FILES = [INI_PUBLIC, INI_TPSAFE, BASE_DEFINE, BASE_CONFIG, LAUNCHER_SDK, PAK_MANAGER, LOGIN_DEBUG]


def once(text: str, old: str, new: str, what: str) -> str:
    n = text.count(old)
    if n != 1:
        raise ValueError(f"{what}: expected 1 match, found {n}: {old[:80]!r}")
    return text.replace(old, new)


def method_span(text: str, head: str) -> tuple[int, int]:
    """Start and end (after its closing brace) of the method whose text starts with head."""
    start = text.index(head)
    depth, i = 0, start + len(head) - 1
    while True:
        c = text[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                return start, i + 1
        i += 1


def ini_public(text: str) -> str:
    # One `key=value` per line; InternalPrefix is one line of `;`-separated prefixes (the C#
    # launcher's parser throws IndexOutOfRange on a line without `=`).
    nl = "\r\n" if "\r\n" in text else "\n"
    out = []
    for line in text.split(nl):
        if line.startswith("InternalPrefix="):
            line = "InternalPrefix=" + ";".join([LOCAL] * 4)
        elif line.startswith("UseSDK="):
            line = "UseSDK=0"
        elif line.startswith("CheckEntryTime="):
            line = "CheckEntryTime=0"
        out.append(line)
    t = nl.join(out)
    if t.count(LOCAL) != 4 or "UseSDK=0" not in t or "CheckEntryTime=0" not in t:
        raise ValueError(f"{INI_PUBLIC}: InternalPrefix, UseSDK or CheckEntryTime missing")
    if not all("=" in l for l in t.splitlines() if l.strip()):
        raise ValueError(f"{INI_PUBLIC}: a line without =")
    return t


def official_ini(text: str) -> str:
    """R-173d: the client's own official config prefixes and UrlPath, which ini_public replaces,
    for RB.RBKeys (it fetches the official index.json to find the CDN of the pak keys)."""
    keep = [l.strip() for l in text.splitlines() if l.startswith(("InternalPrefix=", "UrlPath="))]
    if len(keep) != 2:
        raise ValueError(f"{INI_PUBLIC}: InternalPrefix or UrlPath missing")
    return "\r\n".join(keep) + "\r\n"


def patch(rel: str, text: str) -> str:
    """The patched text of one original file."""
    if rel == INI_PUBLIC:
        return ini_public(text)
    if rel == INI_TPSAFE:
        return once(text, "Enable=True", "Enable=False", rel)
    if rel == BASE_DEFINE:
        return once(text, 'exports.USESDK="1"', 'exports.USESDK="0"', rel)
    if rel == BASE_CONFIG:
        return once(text, 'for(const l of r)!o&&l.startsWith("http://")||l.startsWith("https://")?',
                    'for(const l of r)l.startsWith("http://")||l.startsWith("https://")?', rel)
    if rel == LAUNCHER_SDK:
        a = text.index("const canUseSdk=")
        b = text.index(";", a)
        return text[:a] + "const canUseSdk=()=>!1" + text[b:]
    if rel == PAK_MANAGER:
        a, b = method_span(text, "static Eih(){")
        return text[:a] + "static Eih(){}" + text[b:]
    if rel == LOGIN_DEBUG:
        # The 3.8 login prefab has no input box 20 (the 3.8 JS still reads it): closing the view
        # threw "Cannot read properties of undefined (reading 'GetText')" and LoginView never opened.
        return once(text, "this.GetInputText(20).GetText()", "this.GetInputText(20)?.GetText()", rel)
    raise ValueError(f"no patch for {rel}")


def export(game: str, pakx: str, out: str) -> None:
    """Exports FILES from the official paks only (our own *_p.pak would win otherwise)."""
    paks = tempfile.mkdtemp(prefix="loginpak-paks-")
    try:
        for name in os.listdir(game):
            if name.lower().startswith("rb") or not os.path.isfile(os.path.join(game, name)):
                continue
            try:
                os.link(os.path.join(game, name), os.path.join(paks, name))
            except OSError:
                shutil.copy2(os.path.join(game, name), os.path.join(paks, name))
        pattern = "^(" + "|".join(f.replace(".", r"\.") for f in FILES) + ")$"
        env = dict(os.environ, PAKX_PAKS=paks)
        subprocess.run(["dotnet", pakx, "export", pattern, out], check=True, env=env)
    finally:
        shutil.rmtree(paks, ignore_errors=True)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--game", default=DEFAULT_GAME)
    ap.add_argument("--pakx", default=DEFAULT_PAKX)
    ap.add_argument("--out", default=os.path.join(REPO, "out", "paks", "rb38_login_100_p.pak"))
    ap.add_argument("--from", dest="source", help="the client export folder (-RbExport, R-174) instead of pakx")
    args = ap.parse_args()

    work = tempfile.mkdtemp(prefix="loginpak-")
    try:
        if args.source:
            # The client export keeps each file's path under Client/ (tools/rbauto/RBExport.cs).
            def src(rel: str) -> str:
                return os.path.join(args.source, rel[len("Client/"):].replace("/", os.sep))
            for rel in FILES:
                if not os.path.isfile(src(rel)):
                    raise SystemExit(f"{rel} not in {args.source}: run the client export again")
        else:
            export(args.game, args.pakx, work)

            def src(rel: str) -> str:
                return os.path.join(work, rel.replace("/", os.sep))
        files = []
        for rel in FILES:
            with open(src(rel), "rb") as f:
                text = f.read().decode("utf-8-sig")
            files.append((rel, patch(rel, text).encode("utf-8")))
            if rel == INI_PUBLIC:
                files.append((INI_OFFICIAL, official_ini(text).encode("utf-8")))
    finally:
        shutil.rmtree(work, ignore_errors=True)
    pak = pak_build.build(files)
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "wb") as f:
        f.write(pak)
    print(f"wrote {args.out}: {len(files)} files, {len(pak)} bytes, SHA-256 {hashlib.sha256(pak).hexdigest().upper()}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
