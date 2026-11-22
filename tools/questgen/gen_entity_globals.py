# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-83: global variables entity logic uses (LevelEntityConfig, TemplateConfig).

Entities run their own logic in the client (variables set by interactions, triggers, gear
states). A quest condition on a global variable that entity logic may set is something the server
cannot see change, so the server treats it as unknown for quests and the
condition counts as met, as it did before R-83. The same goes for every entity ("Other") variable;
those need no list.

Every "Keyword" (the name of a Global variable reference) in those tables is listed, read or
written: telling them apart would need the whole entity action schema, and a read next to a write
is the common case.

usage: python -I tools\\questgen\\gen_entity_globals.py [--bindata <QUESTGEN_DIR>\\assets\\BinData]
           [--out <QUESTGEN_DIR>\\assets\\quest\\EntityGlobals.json]
"""
import json
import os
import re
import sys

QG = os.environ.get("QUESTGEN_DIR") or "questgen"
KEYWORD = re.compile(r'"Keyword":\s*"([^"]*)"')
TABLES = ["TemplateConfig.json", "LevelEntityConfig.json", "LevelEntityConfig_1.json", "LevelEntityConfig_2.json"]


def main(argv):
    sys.stdout.reconfigure(encoding="utf-8")
    bindata = os.path.join(QG, "assets", "BinData")
    out = os.path.join(QG, "assets", "quest", "EntityGlobals.json")
    while argv:
        a = argv.pop(0)
        if a == "--bindata":
            bindata = argv.pop(0)
        elif a == "--out":
            out = argv.pop(0)
    names = set()
    for t in TABLES:
        path = os.path.join(bindata, t)
        if not os.path.exists(path):
            continue
        with open(path, encoding="utf-8") as f:
            found = set(KEYWORD.findall(f.read()))
        print(f"{t}: {len(found)} global variable names")
        names |= found
    names.discard("")
    with open(out, "w", encoding="utf-8", newline="\n") as f:
        json.dump(sorted(names), f, ensure_ascii=False, separators=(",", ":"))
        f.write("\n")
    print(f"{len(names)} names -> {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
