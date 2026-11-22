# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-76h: the ranges CheckInRange conditions test ("is the player inside range entity X").

Writes assets/quest/Ranges.json: [{"Id": entity, "Map": map, "Kind": 0 sphere | 1 box | 2 cylinder,
"C": [x, y, z], "P": [a, b, c]}] in the server's units (Transform / 100, cm):
  sphere   P = [radius, 0, 0]
  box      P = [half x, half y, half z] (half z 0: any height)
  cylinder P = [radius, half height, 0]
Shapes are the entity's RangeComponent over its template's (gen_hide_groups.merged_shape).

usage: python -I tools\\questgen\\gen_ranges.py
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_hide_groups import B, Q, load, merged_shape, num  # noqa: E402


def wanted_ids():
    ids = set()

    def walk(n):
        if isinstance(n, dict):
            if n.get("Type") == "CheckInRange":
                ids.update(i for i in n.get("RangeEntities") or [] if isinstance(i, int))
            for v in n.values():
                walk(v)
        elif isinstance(n, list):
            for v in n:
                walk(v)

    for name in ("QuestNodeData.json", "LevelPlayNodeData.json", "QuestData.json", "LevelPlayData.json"):
        walk(load(name))
    return ids


def shape_of(row, shape):
    pos = row["Transform"][0]
    scale = row["Transform"][2] if len(row["Transform"]) > 2 else {"X": 100, "Y": 100, "Z": 100}
    center = shape.get("Center") or {}
    c = [pos[a] / 100 + num(center.get(a)) for a in "XYZ"]
    s = [num(scale.get(a), 100) / 100 for a in "XYZ"]
    kind = shape.get("Type") or ("Sphere" if shape.get("Radius") is not None else "Box")
    if kind == "Sphere":
        return 0, c, [num(shape.get("Radius")) * max(s), 0, 0]
    if kind == "Cylinder":
        return 2, c, [num(shape.get("Radius")) * max(s[0], s[1]), num(shape.get("Height")) * s[2], 0]
    size = shape.get("Size") or {}
    return 1, c, [num(size.get(a)) * s[i] for i, a in enumerate("XYZ")]


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    ids = wanted_ids()
    templates = {t["BlueprintType"]: t for t in load("TemplateConfig.json")}
    out, found = [], set()
    for name in sorted(os.listdir(B)):
        if not (name.startswith("LevelEntityConfig") and name.endswith(".json")):
            continue
        for row in load(name):
            if row["EntityId"] not in ids or not row.get("Transform"):
                continue
            shape = merged_shape(row, templates)
            if not shape:
                continue
            kind, c, p = shape_of(row, shape)
            out.append({"Id": row["EntityId"], "Map": row["MapId"], "Kind": kind, "C": c, "P": p})
            found.add(row["EntityId"])
    path = os.path.join(Q, "Ranges.json")
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        json.dump(out, f, separators=(",", ":"))
    print(f"{len(ids)} range entities in CheckInRange, {len(found)} with a shape, {len(out)} rows -> {path}")


if __name__ == "__main__":
    main()
