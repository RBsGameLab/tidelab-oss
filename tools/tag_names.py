#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-174b: the tag names the server's tables use that are not in the client's tag list, for the
client export to resolve into GameplayTagRemap (tools/rbexport/Tags.cs asks the client's own
GameplayTagUtils.GetTagIdByName; some client tag names are obfuscated, the tables keep readable ones).

A candidate is a JSON string in the BinData tables whose first segment is a top-level tag of the
client's list (Config/DefaultGameplayTags.ini, Config/Tags/*.ini), has at least one dot, and is
not itself in the list (with its parents).

    python -I tools/tag_names.py --config <export>/Config --bindata <BinData> [--bindata <more>] --out <file>
"""
from __future__ import annotations

import argparse
import glob
import os
import re
import sys

STRING = re.compile(r'"((?:[^"\\]|\\.){3,200})"')


def client_tags(config: str) -> set[str]:
    tags: set[str] = set()
    for f in [os.path.join(config, "DefaultGameplayTags.ini")] + sorted(glob.glob(os.path.join(config, "Tags", "*.ini"))):
        if os.path.isfile(f):
            with open(f, encoding="utf-8-sig", errors="replace") as fh:
                tags.update(m.group(1) for m in re.finditer(r'GameplayTagList=\(Tag="([^"]+)"', fh.read()))
    every: set[str] = set()
    for t in tags:
        parts = t.split(".")
        every.update(".".join(parts[:i]) for i in range(1, len(parts) + 1))
    return every


def candidates(texts, known: set[str]) -> set[str]:
    roots = {t for t in known if "." not in t}
    out: set[str] = set()
    for text in texts:
        for m in STRING.finditer(text):
            s = m.group(1)
            if "." in s and "\\" not in s and s.split(".", 1)[0] in roots and s not in known and not s.endswith("."):
                out.add(s)
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--config", required=True)
    ap.add_argument("--bindata", action="append", required=True)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    known = client_tags(a.config)

    def texts():
        for d in a.bindata:
            for f in sorted(glob.glob(os.path.join(d, "*.json"))):
                with open(f, encoding="utf-8", errors="replace") as fh:
                    yield fh.read()
    names = sorted(candidates(texts(), known))
    with open(a.out, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(names) + "\n")
    print(f"tag_names: {len(known)} client tags, {len(names)} names to resolve -> {a.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
