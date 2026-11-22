#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-174a: builds the quest engine's data (assets/quest/*.json, gamesv --quest_data) from the 3.8
tables and the client's behaviour trees. The generators beside this file are ours (moved from the
lab's scripts/dev in R-174); they read <QUESTGEN_DIR>/assets/BinData and write
<QUESTGEN_DIR>/assets/quest.

    python -I tools/questgen/build.py --bindata <BinData> [--bindata <more>] --out <dir>
        (--trees <dir> | --tree-json <dir>)

--bindata    exported tables (the first directory that has a file wins)
--trees      the client's behaviour tree files: <dir>/Quests/<id>.bytes, <dir>/LevelPlays/<id>.bytes
             (Content/Aki/Config/UniverseEditorConfig in the paks; tools/client export, R-174);
             fb_trees.py and fb_vars.py read them into TreeOrders.json and TreeVars.json
--tree-json  a directory that already holds TreeOrders.json and TreeVars.json
--out        the staging directory; the result is <out>/assets/quest
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
GENERATORS = ["gen_quest_data", "gen_levelplay_data", "gen_quest_offers", "gen_entity_globals",
              "gen_flow_actions", "gen_hide_groups", "gen_ranges", "gen_state_setters"]
# 3.8 added a field before Tree in IQuest and ILevelPlay (fb_trees.py).
TREE_SLOTS_38 = "42,43"


def link(src: str, dst: str) -> None:
    if os.path.exists(dst):
        return
    try:
        os.link(src, dst)
    except OSError:
        shutil.copy2(src, dst)


def stage_bindata(dirs: list[str], bin_dir: str) -> int:
    os.makedirs(bin_dir, exist_ok=True)
    n = 0
    for d in dirs:
        for name in sorted(os.listdir(d)):
            if name.endswith(".json") and os.path.isfile(os.path.join(d, name)):
                if not os.path.exists(os.path.join(bin_dir, name)):
                    link(os.path.join(d, name), os.path.join(bin_dir, name))
                    n += 1
    # The generators read LevelEntityConfig in two parts (_1 all rows, _2 empty); some also glob
    # LevelEntityConfig*.json, so the unsplit table must not stay (ranges would be listed twice).
    whole = os.path.join(bin_dir, "LevelEntityConfig.json")
    part1 = os.path.join(bin_dir, "LevelEntityConfig_1.json")
    if os.path.exists(whole):
        if not os.path.exists(part1):
            link(whole, part1)
        os.remove(whole)
    with open(os.path.join(bin_dir, "LevelEntityConfig_2.json"), "w", encoding="ascii") as f:
        f.write("[]")
    return n


def run(script: str, args: list[str], env: dict[str, str]) -> None:
    r = subprocess.run([sys.executable, "-I", os.path.join(HERE, script + ".py"), *args], cwd=HERE, env=env)
    if r.returncode != 0:
        raise SystemExit(f"{script} failed ({r.returncode})")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--bindata", action="append", required=True)
    ap.add_argument("--out", required=True)
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--trees")
    g.add_argument("--tree-json")
    a = ap.parse_args()

    out = os.path.abspath(a.out)
    bin_dir = os.path.join(out, "assets", "BinData")
    quest = os.path.join(out, "assets", "quest")
    os.makedirs(quest, exist_ok=True)
    n = stage_bindata(a.bindata, bin_dir)
    env = dict(os.environ, QUESTGEN_DIR=out, FB_TREE_SLOTS=TREE_SLOTS_38, PYTHONIOENCODING="utf-8")
    if a.trees:
        run("fb_trees", [a.trees, "--out", os.path.join(quest, "TreeOrders.json")], env)
        run("fb_vars", [a.trees, "--out", os.path.join(quest, "TreeVars.json")], env)
    else:
        for f in ("TreeOrders.json", "TreeVars.json"):
            shutil.copy2(os.path.join(a.tree_json, f), os.path.join(quest, f))
    for s in GENERATORS:
        print(f"== {s}", flush=True)
        run(s, [], env)
    print(f"questgen: {n} tables staged, quest data in {quest} ({len(os.listdir(quest))} files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
