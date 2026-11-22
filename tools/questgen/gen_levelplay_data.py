# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-4b/R-4c: builds <QUESTGEN_DIR>/assets/quest/LevelPlays.json and Prefabs.json for the server's
LevelPlay engine.

- LevelPlays.json: dungeon level plays (LevelPlayData Type "Dungeon") with nodes converted like
  quest nodes (gen_quest_data.node), the map (LevelId), and Monsters: MonsterInfo ids of the
  monsters its prefabs and spawner entities spawn, so the server can pick the level play matching a dungeon's
  MonsterPreview when several share a map.
- Prefabs.json: the prefabs those level plays create (PrefabConfig), entities with blueprint and
  offset.

usage: python -I tools\questgen\gen_levelplay_data.py
"""
import collections
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_quest_data import node, B, QG, load_orders, apply_order  # noqa: E402

OUT = os.path.join(QG, "assets", "quest")


def load(name):
    with open(os.path.join(B, name), encoding="utf-8") as f:
        return json.load(f)


def spawner_waves(p):
    """A prefab's SpawnMonsterComponent waves: lists of its entity ids, in order (each config
    depends on the one before it)."""
    for e in p.get("Entities") or []:
        comp = (e["EntityData"].get("ComponentsData") or {}).get("SpawnMonsterComponent")
        if comp:
            return [list(c.get("TargetsToAwake") or []) for c in comp.get("SpawnMonsterConfigs") or []]
    return []


def tower_floor_prefabs(prefabs_all):
    """R-126: the game's own prefab of each 深塔 floor. TowerConfig names a floor's monsters
    (ShowMonsters) but not its prefab; the floor plays (低危区-塔1-第1层 ...) all create an old
    season's prefab, and the official server swaps in the floor's own. Those prefabs ship with the
    client (season 43 深境区: 401700012-015, 405700000-003, 405750008-011). A floor's prefab has a
    spawner and exactly the floor's monsters, by model (a preview names the 复刷专用 boss, the
    prefab its 爬塔专用 twin). Several prefabs can match a one-boss floor: a floor of the same
    season, difficulty and area with one match anchors the rest, which follow it in id order
    (480 -> 405750009, so 482 -> 405750011). Without an anchor: the newest tuning (PropertyId)."""
    bp_type = {b["BlueprintType"]: b.get("EntityType") for b in load("BlueprintConfig.json")}
    bp_models = collections.defaultdict(set)
    for t in load("TemplateConfig.json"):
        m = ((t.get("ComponentsData") or {}).get("ModelComponent") or {}).get("ModelType") or {}
        if t.get("BlueprintType") and m.get("ModelId"):
            bp_models[t["BlueprintType"]].add(m["ModelId"])

    def models(bp):
        return frozenset(bp_models.get(bp) or [bp])

    cands = collections.defaultdict(list)  # model set -> [(newest PropertyId, prefab id)]
    for pid, p in prefabs_all.items():
        if not spawner_waves(p):
            continue
        have, prop = set(), 0
        for e in p.get("Entities") or []:
            d = e["EntityData"]
            if bp_type.get(d["BlueprintType"]) != "Monster":
                continue
            have |= models(d["BlueprintType"])
            prop = max(prop, ((d.get("ComponentsData") or {}).get("AttributeComponent") or {}).get("PropertyId", 0))
        if have:
            cands[frozenset(have)].append((prop, pid))
    monster_bp = {m["Id"]: m.get("MonsterEntityID") for m in load("MonsterInfo.json")}
    floors = load("TowerConfig.json")
    options = {}
    for f in floors:
        need = frozenset().union(*[models(monster_bp[m]) for m in f["ShowMonsters"] if monster_bp.get(m)] or [frozenset()])
        options[f["Id"]] = sorted(cands.get(need, []), reverse=True)
    prefab_models = {pid: have for have, lst in cands.items() for _, pid in lst}
    out = {}
    for f in floors:
        opts = options[f["Id"]]
        anchored = []
        for a in floors:
            if (a["Season"], a["Difficulty"], a["AreaNum"]) == (f["Season"], f["Difficulty"], f["AreaNum"]) \
                    and a["Id"] != f["Id"] and len(options[a["Id"]]) == 1:
                anchored.append(options[a["Id"]][0][1] + f["Floor"] - a["Floor"])
        if not opts:
            # No prefab with exactly the floor's monsters (a preview names a variant whose model
            # no prefab entity has: floor 148): the anchor's neighbour, if it shares a monster.
            need = frozenset().union(*[models(monster_bp[m]) for m in f["ShowMonsters"] if monster_bp.get(m)] or [frozenset()])
            for want in anchored:
                if want in prefab_models and prefab_models[want] & need:
                    out[f["Id"]] = want
                    break
            continue
        pick = opts[0][1]
        if len(opts) > 1:
            for want in anchored:
                if any(pid == want for _, pid in opts):
                    pick = want
                    break
        out[f["Id"]] = pick
    return out


def main():
    sys.stdout.reconfigure(encoding="utf-8")  # play names are Chinese; -I ignores PYTHONIOENCODING
    all_lps = {x["Data"]["Id"]: x["Data"] for x in load("LevelPlayData.json")}
    lps = {i: d for i, d in all_lps.items() if d.get("Type") == "Dungeon"}
    all_nodes = collections.defaultdict(list)
    for e in load("LevelPlayNodeData.json"):
        all_nodes[int(e["Key"].split("_")[0])].append(e["Data"])
    nodes = {lid: [node(d) for d in all_nodes[lid]] for lid in lps}
    # R-4g: sub level plays a dungeon's CheckLevelPlay waits for (Challenge, Quest, RebornBoss,
    # Riddle, ... types), transitively. Marked Sub: never picked as a dungeon's main level play.
    sub = set()
    todo = [lid for lid in lps]
    while todo:
        lid = todo.pop()
        for n in nodes.get(lid, []):
            for ref in n.get("LevelPlays") or []:
                if ref in lps or ref in sub or ref not in all_lps:
                    continue
                sub.add(ref)
                nodes[ref] = [node(d) for d in all_nodes[ref]]
                todo.append(ref)
    for ref in sub:
        lps[ref] = all_lps[ref]
    # R-37: 领主挑战 plays (LordGym.PlayId, open world), started by LordGymBeginRequest. Sub: never
    # a dungeon's main level play.
    for g in load("LordGym.json"):
        ref = g.get("PlayId")
        if ref in all_lps and ref not in lps:
            sub.add(ref)
            lps[ref] = all_lps[ref]
            nodes[ref] = [node(d) for d in all_nodes[ref]]
    prefabs_all = {p["Id"]: p for p in load("PrefabConfig.json")}
    monster_by_bp = collections.defaultdict(list)
    for m in load("MonsterInfo.json"):
        if isinstance(m.get("MonsterEntityID"), str):
            monster_by_bp[m["MonsterEntityID"]].append(m["Id"])
    # Spawner entities (MonsterCreator over MonsterCreatorEntityIds, R-4f) wake map rows.
    entity_rows = {}
    for part in ("LevelEntityConfig_1.json", "LevelEntityConfig_2.json"):
        for r in load(part):
            entity_rows[(r["MapId"], r["EntityId"])] = r

    orders = load_orders("LevelPlays")
    known = sum(apply_order(lid, nodes[lid], orders) for lid in lps if nodes.get(lid))
    print(known, "level plays with their real child order")
    used_prefabs = set()
    out = []
    for lid, lp in lps.items():
        if not nodes.get(lid):
            continue
        prefab_ids = set()
        for n in nodes[lid]:
            for a in n["Enter"] + n["Finish"] + n["Actions"]:
                pid = (a.get("Params") or {}).get("PrefabId")
                if pid:
                    prefab_ids.add(pid)
            if n.get("Bundle"):  # R-60: CrossSceneSyncKillMonster's entity bundle
                prefab_ids.add(n["Bundle"])
        monsters = set()
        for n in nodes[lid]:
            for cid in n.get("Creators") or []:
                comp = (entity_rows.get((lp.get("LevelId", 0), cid)) or {}).get("ComponentsData", {}).get("SpawnMonsterComponent") or {}
                for wave in comp.get("SpawnMonsterConfigs") or []:
                    for tid in wave.get("TargetsToAwake") or []:
                        row = entity_rows.get((lp.get("LevelId", 0), tid))
                        if row:
                            monsters.update(monster_by_bp.get(row["BlueprintType"], []))
        for pid in prefab_ids:
            for ent in (prefabs_all.get(pid) or {}).get("Entities") or []:
                for mid in monster_by_bp.get(ent["EntityData"]["BlueprintType"], []):
                    monsters.add(mid)
        used_prefabs |= prefab_ids
        out.append({"Id": lid, "Name": lp.get("Key", ""), "LevelId": lp.get("LevelId", 0),
                    "Monsters": sorted(monsters), "Nodes": nodes[lid], "Sub": lid in sub})

    # R-32: dungeons whose level play the MonsterPreview rule cannot find. The 领主复刷 dungeons
    # on map 2103 (210301001-029, 210302001-010) have no MonsterPreview; all 29 got the first
    # play, 无冠者. Each loads a gameplay sub level named after its boss in pinyin
    # (Gameplay_Mat_Boss_HaiWeiXia); the play on the same map with that boss in its Key is its play.
    boss_names = {
        "WuGuanZhe": "无冠者", "ShuoLeiZhiLin": "朔雷之鳞", "YunShanZhiLin": "云闪之鳞",
        "LiaoZhaoZhiQi": "燎照之骑", "FeiLianZhiXing": "飞廉之猩", "AiShengZhi": "哀声鸷",
        "WuChangXiongLu": "无常凶鹭", "HuiYingJunShi": "辉萤军势", "JvXieJiOu": "聚械机偶",
        "WuGuiDeMiuWu": "无归的谬误", "LuoLeiLai": "罗蕾莱", "YiGouWuZhuang": "异构武装",
        "TanXiGuLong": "叹息古龙", "RongYaoShiXiang": "荣耀狮像", "Fenlaike": "芬莱克",
        "WeiZuoDeShenWang": "伪作的神王", "HaiWeiXia": "海维夏", "LuXinJiHai": "炉芯机骸",
        "WuMingTanSuoZhe": "无铭探索者", "MingZhongZhiGui": "鸣钟之龟", "KaiErPi": "凯尔匹",
        "HeKaTe": "赫卡忒",
    }
    prefix = "Gameplay_Mat_Boss_"
    mains = [p for p in out if not p["Sub"]]
    dungeon_plays = []
    unmatched = []
    for d in load("InstanceDungeon.json"):
        gameplay = [s.rsplit("/", 1)[-1] for s in d.get("SubLevels") or [] if "/Gameplay/" in s]
        sub_level = next((g for g in gameplay if g.startswith(prefix)), None)
        if not sub_level:
            continue
        boss = sub_level[len(prefix):]
        nightmare = boss.startswith("MengYan_")
        name = boss_names.get(boss[len("MengYan_"):] if nightmare else boss)
        found = [p["Id"] for p in mains if p["LevelId"] == d["MapConfigId"] and name and name in p["Name"]
                 and ("梦魇" in p["Name"]) == nightmare]
        if len(found) == 1:
            dungeon_plays.append([d["Id"], found[0]])
        else:
            unmatched.append((d["Id"], sub_level, found))
    print(len(dungeon_plays), "dungeons matched to their level play by sub level;", len(unmatched), "not:", unmatched)

    # R-59: the 无音区 (210404001-021) and 梦魇聚落 (210403001-005) world dungeons of 3.5 all load the
    # same sub level on map 2104 and have no MonsterPreview, so every one ran the map's first play
    # (梦魇聚落-海底). The Sola guide (SilentAreaDetection.DungeonId) lists them in release order, and
    # their "3.5迁移副本" plays on map 2104 come in the same order (1号无音区：中曲台地 ... 3.7梦枢天罗;
    # 梦魇聚落-海底 ... 穂波). Checked by region name and by the monsters each play wakes.
    world = sorted({x["DungeonId"] for x in load("SilentAreaDetection.json") if str(x["DungeonId"]).startswith("2104")})
    migrated = [p for p in mains if p["LevelId"] == 2104 and "3.5迁移副本" in p["Name"]]
    for ids, plays in (([i for i in world if str(i).startswith("210403")], [p for p in migrated if "梦魇聚落" in p["Name"]]),
                       ([i for i in world if str(i).startswith("210404")], [p for p in migrated if "梦魇聚落" not in p["Name"]])):
        if len(ids) != len(plays):
            print("R-59: world dungeons and their plays differ in number:", ids, [p["Id"] for p in plays])
            continue
        for d, p in zip(ids, plays):
            dungeon_plays.append([d, p["Id"]])
            print("  world dungeon", d, "->", p["Id"], p["Name"])

    # R-62: open-world plays quests wait for (QuestData ProvideType PreLevelPlay), with the plays
    # their CheckLevelPlay steps start (Sub). The server never ran open-world plays, so quests behind
    # them were never given. A separate file: added to LevelPlays.json they could become a
    # dungeon's main play on maps the world shares with dungeons (8, 100).
    from gen_quest_data import provide_cond  # noqa: E402
    targets = set()
    for x in load("QuestData.json"):
        for c in (x["Data"].get("ProvideType") or {}).get("Conditions") or []:
            if isinstance(c, dict) and c.get("Type") == "PreLevelPlay" and c.get("PreLevelPlay") in all_lps:
                targets.add(c["PreLevelPlay"])
    world_nodes = {lid: [node(d) for d in all_nodes[lid]] for lid in targets if all_nodes.get(lid)}
    world_sub = set()
    todo = list(world_nodes)
    while todo:
        for n in world_nodes[todo.pop()]:
            for ref in n.get("LevelPlays") or []:
                if ref in world_nodes or ref not in all_lps or not all_nodes.get(ref):
                    continue
                world_sub.add(ref)
                world_nodes[ref] = [node(d) for d in all_nodes[ref]]
                todo.append(ref)
    known = sum(apply_order(lid, world_nodes[lid], orders) for lid in world_nodes)
    world = []
    for lid in sorted(world_nodes):
        lp = all_lps[lid]
        for n in world_nodes[lid]:
            for a in n["Enter"] + n["Finish"] + n["Actions"]:
                pid = (a.get("Params") or {}).get("PrefabId")
                if pid:
                    used_prefabs.add(pid)
        row = entity_rows.get((lp.get("LevelId", 0), lp.get("LevelPlayEntityId"))) or {}
        t = (row.get("Transform") or [{}])[0]
        open_conds = [provide_cond(c) for c in (lp.get("LevelPlayOpenCondition") or {}).get("Conditions") or [] if isinstance(c, dict)]
        world.append({"Id": lid, "Name": lp.get("Key", ""), "LevelId": lp.get("LevelId", 0), "Nodes": world_nodes[lid],
                      "Sub": lid in world_sub and lid not in targets,
                      # LevelEntityConfig transforms are in 1/100 cm, like the robot's rowPos reads them.
                      "Pos": [t.get("X", 0) / 100, t.get("Y", 0) / 100, t.get("Z", 0) / 100],
                      "Radius": lp.get("EnterRadius", 0) or 0, "Placed": bool(row),
                      "Open": {"Any": (lp.get("LevelPlayOpenCondition") or {}).get("Type") == 1, "Conds": open_conds}})
    print(len(world), "world plays quests wait for (", len(world_sub), "sub plays,", known, "with their real child order,",
          sum(not w["Placed"] for w in world), "without a placed entity )")
    with open(os.path.join(OUT, "WorldPlays.json"), "w", encoding="utf-8") as f:
        json.dump(world, f, ensure_ascii=False, separators=(",", ":"))

    # R-88: the 深塔 version of a boss (爬塔专用) only shows up with the tag its tower prefab gives
    # it (MonsterComponent.InitGasTag 难度AI分类.爬塔); spawned as a bare template, its state machine
    # (复刷判断) hides it. Per blueprint, keep the tower prefab entity with only that tag and the
    # newest tuning (highest PropertyId), so the server can spawn the wave as that prefab entity.
    # R-126: every tower monster, not only the tagged bosses. The game's tower and challenge
    # prefab entities carry MonsterComponent.SpecialHateAndSenseConfig 1 (副本90米仇恨) or 750001
    # (副本100米仇恨): they sense and chase the player anywhere in the arena, where a bare world
    # template stood in 待机 until the player walked up. Their PropertyId is the tower tuning
    # (7000xxxxx: about 3.6 times the world version's life). Entities with another InitGasTag
    # (activities) are left out. Preference: the 爬塔 tag, then 1 over 750001, then the newest
    # tuning (highest PropertyId).
    tower_tag = ["怪物.common.关卡.难度AI分类.爬塔"]
    arena_sense = {1: 2, 750001: 1}
    tower_best = {}
    for pid, p in prefabs_all.items():
        for e in p.get("Entities") or []:
            d = e["EntityData"]
            comps = d.get("ComponentsData") or {}
            mc = comps.get("MonsterComponent") or {}
            tags = mc.get("InitGasTag") or []
            tagged = tags == tower_tag
            sense = arena_sense.get(mc.get("SpecialHateAndSenseConfig"), 0)
            if not tagged and (tags or not sense):
                continue
            prop = (comps.get("AttributeComponent") or {}).get("PropertyId", 0)
            cur = tower_best.get(d["BlueprintType"])
            if cur is None or (tagged, sense, prop, pid) > cur[0]:
                tower_best[d["BlueprintType"]] = ((tagged, sense, prop, pid), pid, d["Id"])
    tower_entities = {(pid, eid): key[2] for key, pid, eid in tower_best.values()}
    used_prefabs |= {pid for pid, _ in tower_entities}
    floor_prefabs = tower_floor_prefabs(prefabs_all)
    used_prefabs |= set(floor_prefabs.values())

    prefabs = []
    for pid in sorted(used_prefabs):
        p = prefabs_all.get(pid)
        if not p:
            continue
        ents = []
        for e in p.get("Entities") or []:
            d = e["EntityData"]
            t = d.get("Transform") or {}
            pos, rot = t.get("Pos") or {}, t.get("Rot") or {}
            ent = {"Id": d["Id"], "BlueprintType": d["BlueprintType"], "Name": d.get("Name", ""),
                   "Pos": [pos.get("X", 0), pos.get("Y", 0), pos.get("Z", 0)],
                   "Rot": [rot.get("X", 0), rot.get("Y", 0), rot.get("Z", 0)]}
            if (pid, d["Id"]) in tower_entities:
                ent["Tower"] = True
            # R-126: the server computes a monster's attributes, so it needs the entity's tuning
            # (a tower entity's PropertyId 7000xxxxx is about 3.6 times the world version's life).
            prop = ((d.get("ComponentsData") or {}).get("AttributeComponent") or {}).get("PropertyId", 0)
            if prop:
                ent["PropertyId"] = prop
            ents.append(ent)
        entry = {"Id": pid, "Entities": ents}
        if pid in floor_prefabs.values():
            entry["Waves"] = spawner_waves(p)  # R-126: a 深塔 floor's own waves
        prefabs.append(entry)
    print(len(tower_entities), "tower prefab entities (爬塔 tag or SpecialHateAndSenseConfig 1),",
          sum(key[0] for key, _, _ in tower_best.values()), "of them tagged")

    # R-127: a floor whose prefab a dungeon play creates (低危区/高危区 210000001-012, 超载区
    # 210750000-005) runs that play: its countdown (超载区 600 s, not 300), title and arena spot.
    play_of_prefab = {}
    for lp in out:
        if lp.get("LevelId") != 90 or lp.get("Sub"):
            continue
        for n in lp["Nodes"]:
            for a in n.get("Enter") or []:
                pid = (a.get("Params") or {}).get("PrefabId") if a.get("Name") == "CreatePrefab" else None
                if pid and pid not in play_of_prefab:
                    play_of_prefab[pid] = lp["Id"]
    floors = []
    for fid, pid in sorted(floor_prefabs.items()):
        row = {"Floor": fid, "Prefab": pid}
        if pid in play_of_prefab:
            row["Play"] = play_of_prefab[pid]
        floors.append(row)
    print(sum("Play" in f for f in floors), "深塔 floors with their own level play (R-127)")
    print(len(floors), "深塔 floors with their own prefab (R-126)")
    for name, data in (("LevelPlays.json", out), ("Prefabs.json", prefabs), ("DungeonPlays.json", dungeon_plays),
                       ("TowerFloors.json", floors)):
        path = os.path.join(OUT, name)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, separators=(",", ":"))
        print(len(data), name, os.path.getsize(path) // 1024, "KB")


if __name__ == "__main__":
    main()
