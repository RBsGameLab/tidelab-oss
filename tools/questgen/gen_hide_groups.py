# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-69: quest clearings. Quests and level plays hide world entities for a scene (HideGroup
{GroupKey, HideConfig}) and show them again (ShowHidedGroup {GroupKey}); 114 of the 1098 groups
are never shown in the data: the owner (the quest or play in the key, "q_<quest>_..." or
"l_<play>_...") ending releases them.

Writes assets/quest/HideGroups.json: [{"Key": ..., "Owner": "q123" | "l123", "Ids": [[map, entity], ...],
"Force": [[map, entity], ...]}] with the entities each group hides, so the server only looks the key
up. Ids come from ranges and give way to entities a quest woke (its actors); Force ones are named
(SpecifyEntity, AppendEntities, plays) and are hidden even when awake.

HideConfig types:
  WorldMonsterAndMonsterTreasure  monsters inside the HideRangeEntities' ranges
  WorldEntityAndLevelPlay         monsters, NPCs and animals inside the ranges, and level play entities
  SpecifyEntity                   EntityIds
  SpecifyLevelPlay                the plays' LevelPlayEntityId
  SpecifyDataLayers               not supported (5 groups): hides nothing
Range kinds never hide quest-owned entities (QuestOwned.json: the quest's own cast) or
ExcludeEntities / the ExcludeLevelPlays' entities; AppendEntities / AppendLevelPlays add any
entity. IsHidePasserByNpc (the client's own passersby) is not handled.

usage: python -I tools\\questgen\\gen_hide_groups.py
"""
import collections
import json
import os
import sys

QG = os.environ.get("QUESTGEN_DIR") or "questgen"
B = os.path.join(QG, "assets", "BinData")
Q = os.path.join(QG, "assets", "quest")

MONSTERS = {"Monster", "NpcMonster"}
CREATURES = MONSTERS | {"Npc", "Npc2", "SimpleNPc", "PasserbyNpc", "ElementalNpc", "RewardNpc", "Animal", "Animal2",
                        "CombatAnimal", "CombatAnimal2", "CombatAnimal3", "CollectAnimal"}
PLAY_ENTITIES = {"LevelPlay"}


def load(name, base=B):
    with open(os.path.join(base, name), encoding="utf-8") as f:
        return json.load(f)


def walk_actions(node, out):
    if isinstance(node, dict):
        if node.get("Name") in ("HideGroup", "ShowHidedGroup") and isinstance(node.get("Params"), dict):
            out.append(node)
        for v in node.values():
            walk_actions(v, out)
    elif isinstance(node, list):
        for v in node:
            walk_actions(v, out)


def merged_shape(row, templates):
    """The row's RangeComponent.Shape over its template's (fields the row leaves null keep the template's)."""
    tmpl = ((templates.get(row["BlueprintType"]) or {}).get("ComponentsData") or {}).get("RangeComponent") or {}
    own = (row.get("ComponentsData") or {}).get("RangeComponent") or {}
    shape = dict(tmpl.get("Shape") or {})
    for k, v in (own.get("Shape") or {}).items():
        if v is not None and v != {}:
            shape[k] = v
    return shape


def num(v, default=0.0):
    return float(v) if isinstance(v, (int, float)) else default


def make_range(row, shape):
    """(map, test(location) -> bool) in the server's units: Transform / 100 (cm)."""
    pos, scale = row["Transform"][0], row["Transform"][2] if len(row["Transform"]) > 2 else {"X": 100, "Y": 100, "Z": 100}
    center = shape.get("Center") or {}
    c = [pos[a] / 100 + num(center.get(a)) for a in "XYZ"]
    s = [num(scale.get(a), 100) / 100 for a in "XYZ"]
    kind = shape.get("Type") or ("Sphere" if "Radius" in shape and shape.get("Radius") is not None else "Box")
    if kind == "Sphere":
        r = num(shape.get("Radius")) * max(s)
        return lambda p: (p[0] - c[0]) ** 2 + (p[1] - c[1]) ** 2 + (p[2] - c[2]) ** 2 <= r * r
    if kind == "Cylinder":
        r = num(shape.get("Radius")) * max(s[0], s[1])
        h = num(shape.get("Height")) * s[2]
        return lambda p: (p[0] - c[0]) ** 2 + (p[1] - c[1]) ** 2 <= r * r and abs(p[2] - c[2]) <= h
    size = shape.get("Size") or {}
    e = [num(size.get(a)) * s[i] for i, a in enumerate("XYZ")]
    # A box without a Z extent (Size {X, Y}) is a footprint: any height.
    return lambda p: abs(p[0] - c[0]) <= e[0] and abs(p[1] - c[1]) <= e[1] and (e[2] == 0 or abs(p[2] - c[2]) <= e[2])


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    entity_type = {b["BlueprintType"]: b.get("EntityType") for b in load("BlueprintConfig.json")}
    templates = {t["BlueprintType"]: t for t in load("TemplateConfig.json")}
    rows = []
    for name in sorted(os.listdir(B)):
        if name.startswith("LevelEntityConfig") and name.endswith(".json"):
            rows += load(name)
    by_id = collections.defaultdict(list)
    for r in rows:
        by_id[r["EntityId"]].append(r)
    owned = {(m, e) for m, e in load("QuestOwned.json", Q)}
    play_entity = {e["LevelPlayId"]: (e["Data"].get("LevelId"), e["Data"].get("LevelPlayEntityId")) for e in load("LevelPlayData.json")}

    configs = collections.defaultdict(list)  # key -> HideGroup params (a key can be hidden by several nodes)
    shown = set()
    for table, tag in (("QuestNodeData.json", "q"), ("LevelPlayNodeData.json", "l")):
        for e in load(table):
            acts = []
            walk_actions(e["Data"], acts)
            for a in acts:
                key = a["Params"].get("GroupKey")
                if not isinstance(key, str):
                    continue
                if a["Name"] == "ShowHidedGroup":
                    shown.add(key)
                elif a["Params"] not in configs[key]:
                    configs[key].append(a["Params"])

    def ids_of(entity, maps):
        found = [(r["MapId"], entity) for r in by_id.get(entity, [])]
        near = [p for p in found if not maps or p[0] in maps]
        return near or found

    out, stats = [], collections.Counter()
    for key in sorted(configs):
        parts = key.split("_")
        owner = parts[0] + parts[1] if len(parts) > 1 and parts[0] in ("q", "l") and parts[1].isdigit() else ""
        hidden, forced = set(), set()
        for p in configs[key]:
            hc = p.get("HideConfig") or {}
            kind = hc.get("Type")
            ranges = []
            for rid in hc.get("HideRangeEntities") or []:
                for r in by_id.get(rid, []):
                    if len(r["Transform"]) >= 1:
                        ranges.append((r["MapId"], make_range(r, merged_shape(r, templates))))
            maps = {m for m, _ in ranges}
            exclude = set()
            for x in hc.get("ExcludeEntities") or []:
                exclude.update(ids_of(x, maps))
            for lp in hc.get("ExcludeLevelPlays") or []:
                m, ent = play_entity.get(lp, (None, None))
                if ent:
                    exclude.update(ids_of(ent, maps))
            if kind in ("WorldMonsterAndMonsterTreasure", "WorldEntityAndLevelPlay"):
                wanted = MONSTERS if kind == "WorldMonsterAndMonsterTreasure" else CREATURES | PLAY_ENTITIES
                for r in rows:
                    if entity_type.get(r["BlueprintType"]) not in wanted or not r["Transform"]:
                        continue
                    pair = (r["MapId"], r["EntityId"])
                    if pair in owned or pair in exclude or r["EntityId"] in (hc.get("HideRangeEntities") or []):
                        continue
                    t = r["Transform"][0]
                    loc = (t["X"] / 100, t["Y"] / 100, t["Z"] / 100)
                    if any(m == r["MapId"] and test(loc) for m, test in ranges):
                        hidden.add(pair)
            elif kind == "SpecifyEntity":
                for x in hc.get("EntityIds") or []:
                    forced.update(ids_of(x, maps))
            elif kind == "SpecifyLevelPlay":
                for lp in hc.get("LevelPlays") or []:
                    m, ent = play_entity.get(lp, (None, None))
                    if ent:
                        forced.update(ids_of(ent, {m} if m else maps))
            else:
                stats["unsupported " + str(kind)] += 1
            for x in hc.get("AppendEntities") or []:
                forced.update(ids_of(x, maps))
            for lp in hc.get("AppendLevelPlays") or []:
                m, ent = play_entity.get(lp, (None, None))
                if ent:
                    forced.update(ids_of(ent, maps or ({m} if m else set())))
        hidden -= forced
        stats["groups"] += 1
        stats["ids"] += len(hidden) + len(forced)
        if not hidden and not forced:
            stats["empty"] += 1
        if not owner:
            stats["no owner"] += 1
        out.append({"Key": key, "Owner": owner, "Ids": sorted(hidden), "Force": sorted(forced)})
    path = os.path.join(Q, "HideGroups.json")
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        json.dump(out, f, ensure_ascii=False, separators=(",", ":"))
    sizes = sorted(len(g["Ids"]) + len(g["Force"]) for g in out)
    print(dict(stats), "never shown:", len(set(configs) - shown), "largest:", sizes[-5:], "median:", sizes[len(sizes) // 2])
    print("->", path, os.path.getsize(path), "bytes")


if __name__ == "__main__":
    main()
