# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-160: the content panel's names, zh-Hans, from the 3.8 client data.

Writes "<id>\t<name>" TSV files read by gamesv at run time (specs/panel.md §3):
activity_names.tsv (Activity.Title), item_names.tsv (ItemInfo.Name, the pay shop's goods),
gacha_names.tsv (each limited banner's featured 5-star names: RoleInfo.Name / WeaponConf.WeaponName).
Run again when the client moves to a new version:

    python -I tools/export_panel_names.py [--bindata DIR] [--lang DB] [--out DIR]
"""

import argparse
import json
import sqlite3
import os
import sys
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_paths  # noqa: E402

LAB = tl_paths.LAB  # R-171
BINDATA = LAB / "gamedata" / "3.8" / "BinData"
LANG = LAB / "reference" / "lang38" / "Client" / "Content" / "Aki" / "ConfigDB" / "zh-Hans" / "lang_multi_text.db"


def clean(text):
    return " ".join(text.replace("\t", " ").split())


def write(out_dir, name, pairs):
    out_dir.mkdir(parents=True, exist_ok=True)
    path = out_dir / name
    lines = [f"{i}\t{clean(n)}\n" for i, n in sorted(pairs.items()) if n]
    path.write_text("".join(lines), encoding="utf-8", newline="\n")
    print(f"{len(lines)} of {len(pairs)} names -> {path}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bindata", type=Path, default=BINDATA)
    ap.add_argument("--lang", type=Path, default=LANG)
    ap.add_argument("--out", type=Path, default=None, help="output directory (default: <bindata>/../panel)")
    args = ap.parse_args()
    out_dir = args.out if args.out is not None else args.bindata.parent / "panel"

    def load(name):
        return json.loads((args.bindata / name).read_text(encoding="utf-8"))

    db = sqlite3.connect(f"file:{args.lang}?mode=ro", uri=True)

    def text(key):
        hit = db.execute("select Content from MultiText where Id = ?", (key or "",)).fetchone()
        return hit[0] if hit and hit[0] else ""

    write(out_dir, "activity_names.tsv", {r["Id"]: text(r.get("Title") or r.get("Name")) for r in load("Activity.json")})
    write(out_dir, "item_names.tsv", {r["Id"]: text(r.get("Name")) for r in load("ItemInfo.json")})
    roles = {r["Id"]: text(r["Name"]) for r in load("RoleInfo.json")}
    weapons = {r["ItemId"]: text(r["WeaponName"]) for r in load("WeaponConf.json")}
    views = {v["Id"]: v for v in load("GachaViewInfo.json")}
    gacha = {}
    for pool in sorted(load("GachaPool.json"), key=lambda p: (p["Sort"], p["Id"])):
        v = views.get(pool["Id"])
        if not v or v["Type"] in (1, 4, 5, 6) or pool["GachaId"] in gacha:
            continue
        gacha[pool["GachaId"]] = "、".join(roles.get(i) or weapons.get(i) or str(i) for i in v["ShowIdList"])
    write(out_dir, "gacha_names.tsv", gacha)


if __name__ == "__main__":
    main()
