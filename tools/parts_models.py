#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-174: the models whose blueprint may carry a part table, for the client export to read into
CharacterPartConfig (tools/rbexport/Parts.cs loads each class and reads its DtCharacterPart).

A model is a ModelConfigPreload row with an ActorClassPath, except items, roles, vehicles and
weapons (no parts). One line per model: Id TAB ActorClassPath.

    python -I tools/parts_models.py --bindata <BinData> --out <file>
"""
from __future__ import annotations

import argparse
import json
import os
import sys

SKIP = tuple(f"/Game/Aki/Character/{k}/" for k in ("Item", "Role", "Vehicle", "Weapon"))


def models(rows: list[dict]) -> list[tuple[int, str]]:
    out = []
    for r in rows:
        path = r.get("ActorClassPath") or ""
        if path and not path.startswith(SKIP):
            out.append((int(r["Id"]), path))
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bindata", required=True)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    with open(os.path.join(a.bindata, "ModelConfigPreload.json"), encoding="utf-8") as f:
        found = models(json.load(f))
    with open(a.out, "w", encoding="utf-8", newline="\n") as f:
        f.writelines(f"{i}\t{p}\n" for i, p in found)
    print(f"parts_models: {len(found)} models, {len({p for _, p in found})} classes")
    return 0


if __name__ == "__main__":
    sys.exit(main())
