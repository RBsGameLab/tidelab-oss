#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-174: the 3.8 script assemblies our C# tools compile against, from the client's own export.

The client's C# assemblies are inside its paks (Client/Content/Aki/ScriptAssemblies). The client
export (tidelab_start.exe -RbExport=<folder>, client/patch/src/export.rs) copies them through the
engine: each file is the whole PE image. tools/build_cspak.py and generate.cmd read them from
<lab>/backups/csref38 in the form the lab's earlier pak extraction gave, which build_cspak turns
back into what it packs (11 zero bytes, then these bytes): the image without its last 11 bytes.
Checked 2026-11-22: for all 78 assemblies, export[:-11] == the lab's csref38 file, and the C# pak
built from them is byte for byte the one built before.

    python -I tools/client_refs.py --export <export folder> --out <lab>/backups/csref38
"""
from __future__ import annotations

import argparse
import os
import sys

SCRIPT_DIR = os.path.join("Content", "Aki", "ScriptAssemblies")
TAIL = 11


def to_ref(image: bytes) -> bytes | None:
    """The csref38 form of an exported assembly, or None when it is not a PE image."""
    if not image.startswith(b"MZ") or len(image) <= TAIL:
        return None
    return image[:-TAIL]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--export", required=True, help="the client export folder")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    src = os.path.join(a.export, SCRIPT_DIR)
    if not os.path.isdir(src):
        print(f"client_refs: no {src}: run the client export first", file=sys.stderr)
        return 1
    os.makedirs(a.out, exist_ok=True)
    n = skipped = 0
    for name in sorted(os.listdir(src)):
        if not name.lower().endswith(".dll"):
            continue
        with open(os.path.join(src, name), "rb") as f:
            ref = to_ref(f.read())
        if ref is None:
            skipped += 1
            continue
        with open(os.path.join(a.out, name), "wb") as f:
            f.write(ref)
        n += 1
    print(f"client_refs: {n} assemblies written to {a.out}" + (f", {skipped} skipped (not a PE image)" if skipped else ""))
    return 0 if n else 1


if __name__ == "__main__":
    sys.exit(main())
