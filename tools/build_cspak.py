#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Build the client's C# patch pak, rb38_cs_300_p.pak (R-55, R-107, R-95 tap), from this
repository: tools/rbauto is compiled against the 3.8 script assemblies, tools/cspatch patches
the shipped Aki.Launcher.dll and Aki.Game.dll (rbauto copied into the latter), and pak_build.py
packs both under Client/Content/Aki/ScriptAssemblies/.

    python -I tools/build_cspak.py [--refs <dir>] [--out <pak>] [--check <pak>]

--refs   the 3.8 script assemblies with the 11-byte prefix removed
         (default <lab>\\backups\\csref38, tools/tl_paths.py); the shipped files are those
         bytes behind 11 zero bytes, which is what cspatch is given.
--out    default out\\paks\\rb38_cs_300_p.pak in this repository (start-game.cmd installs it).
--check  compare the result with another pak byte for byte (the lab's backups\\v38 one was
         rebuilt identically on 2026-11-20).

cspatch takes about 4 minutes and more than 2 GB of memory. R-94 and R-95 off (--r94,
--r95off) are not built: they were never measured on 3.8.
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
DEFAULT_REFS = str(tl_paths.LAB / "backups" / "csref38")  # R-171
SHIPPED_PREFIX = 11
# Aki.Core.dll since R-173b: Net.SetDynamicProtoKey calls RB.RBNet (Type 0 sessions unencrypted).
PATCHED = ("Aki.Launcher.dll", "Aki.Game.dll", "Aki.Core.dll")
INPUTS = PATCHED
PAK_DIR = "Client/Content/Aki/ScriptAssemblies/"


def shipped(stripped: bytes) -> bytes:
    """The assembly as the game ships it: zero bytes before "MZ"."""
    if not stripped.startswith(b"MZ"):
        raise ValueError("not a PE file without its prefix")
    return b"\0" * SHIPPED_PREFIX + stripped


def pak_files(patched_dir: str) -> list[tuple[str, bytes]]:
    out = []
    for name in PATCHED:
        with open(os.path.join(patched_dir, name), "rb") as f:
            out.append((PAK_DIR + name, f.read()))
    return out


def run(*cmd: str) -> None:
    print("> " + " ".join(cmd), flush=True)
    subprocess.run(cmd, check=True)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--refs", default=DEFAULT_REFS)
    ap.add_argument("--out", default=os.path.join(REPO, "out", "paks", "rb38_cs_300_p.pak"))
    ap.add_argument("--check")
    args = ap.parse_args()

    tools = os.path.join(REPO, "tools")
    rbauto_out = os.path.join(REPO, "out", "rbauto")
    cspatch_out = os.path.join(REPO, "out", "cspatch")
    run("dotnet", "build", os.path.join(tools, "rbauto"), "-c", "Release", "-o", rbauto_out, f"-p:GameRefs={args.refs}")
    run("dotnet", "build", os.path.join(tools, "cspatch"), "-c", "Release", "-o", cspatch_out)

    work = tempfile.mkdtemp(prefix="cspak-")
    try:
        src = os.path.join(work, "in")
        dst = os.path.join(work, "out")
        os.makedirs(src)
        for name in INPUTS:
            with open(os.path.join(args.refs, name), "rb") as f:
                data = shipped(f.read())
            with open(os.path.join(src, name), "wb") as f:
                f.write(data)
        run("dotnet", os.path.join(cspatch_out, "cspatch.dll"), src, dst, os.path.join(rbauto_out, "rbauto.dll"))
        pak = pak_build.build(pak_files(dst), align_dll=4096)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "wb") as f:
        f.write(pak)
    print(f"wrote {args.out}: {len(pak)} bytes, SHA-256 {hashlib.sha256(pak).hexdigest().upper()}")
    if args.check:
        with open(args.check, "rb") as f:
            other = f.read()
        if other != pak:
            print(f"DIFFERENT from {args.check} ({len(other)} bytes)")
            return 1
        print(f"identical to {args.check}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
