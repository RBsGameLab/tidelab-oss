# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""Builds <QUESTGEN_DIR>/assets/quest/Quests.json from QuestData.json + QuestNodeData.json.

The raw node table mixes types (Condition.Type is a string or an int, action Params.State is a
string, int or dict), which a typed parser can't take. This keeps only what the server's quest
engine needs, with fixed types. The node table lost the child order:
composite nodes get Kids, their children in the real order, from TreeOrders.json (fb_trees.py reads
the client's behavior tree files). Nodes keep their table order: saved progress indexes into it.
Actions keep their original positions so LevelEventNotify Start/EndIndex match the client config.

usage: python -I tools\\questgen\\gen_quest_data.py
The directory to read from and write to is QUESTGEN_DIR (assets/BinData in, assets/quest out),
e.g. a worktree under wt/ for a request branch.
"""
import json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))
QG = os.environ.get("QUESTGEN_DIR") or "questgen"
B = os.path.join(QG, "assets", "BinData")
OUT = os.path.join(QG, "assets", "quest", "Quests.json")
ORDERS = os.path.join(QG, "assets", "quest", "TreeOrders.json")


def load_orders(kind):
    """kind: "Quests" or "LevelPlays". {tree id: {"Order": {node id: [child ids]}}}."""
    if not os.path.exists(ORDERS):
        print("warning: no TreeOrders.json, child order falls back to the table order")
        return {}
    with open(ORDERS, encoding="utf-8") as f:
        return json.load(f).get(kind, {})


def apply_order(tree_id, nodes, orders):
    """Sets Kids (real child order) on the composite nodes of one tree. Returns True if known."""
    o = orders.get(str(tree_id))
    if not o:
        return False
    for n in nodes:
        kids = o["Order"].get(str(n["Id"]))
        if kids:
            n["Kids"] = kids
    return True


def flow(f):
    if not isinstance(f, dict) or not isinstance(f.get("FlowListName"), str):
        return None
    return {"FlowListName": f["FlowListName"], "FlowId": int(f.get("FlowId") or 0), "StateId": int(f.get("StateId") or 0)}


def var(v):
    """A behavior tree variable reference (R-4g), normalized for the server's condition evaluator:
    Src Self/Global/Other/Constant/System, Name (Name or Global Keyword), Ref (Other: entity id),
    T Int/Boolean/Float/String, Num (Constant number, booleans as 0/1), Str (Constant string)."""
    if not isinstance(v, dict):
        return None
    out = {"Src": v.get("Source") or "", "T": v.get("Type") or ""}
    name = v.get("Name") if isinstance(v.get("Name"), str) else v.get("Keyword")
    if isinstance(name, str): out["Name"] = name
    if isinstance(v.get("RefId"), int): out["Ref"] = v["RefId"]
    val = v.get("Value")
    if isinstance(val, bool): out["Num"] = 1.0 if val else 0.0
    elif isinstance(val, (int, float)): out["Num"] = float(val)
    elif isinstance(val, str): out["Str"] = val
    return out


def sub_condition(c):
    """One condition of a CompareVar list or a CheckConditionGroup (R-4g)."""
    if not isinstance(c, dict):
        return None
    t = c.get("Type") or "CompareVar"
    out = {"T": t}
    if "Var1" in c:
        out["A"], out["B"] = var(c.get("Var1")), var(c.get("Var2"))
    if isinstance(c.get("Compare"), str): out["Op"] = c["Compare"]
    if isinstance(c.get("EntityId"), int): out["Entity"] = c["EntityId"]
    if isinstance(c.get("State"), str): out["State"] = c["State"]
    if isinstance(c.get("State"), int): out["Num"] = c["State"]
    ids = c.get("EntityIds")
    if isinstance(ids, list): out["Entities"] = [i for i in ids if isinstance(i, int)]
    if isinstance(c.get("IsExist"), bool): out["Exist"] = c["IsExist"]
    if isinstance(c.get("QuestId"), int): out["Quest"] = c["QuestId"]
    if isinstance(c.get("ChildQuestId"), int): out["Node"] = c["ChildQuestId"]
    if isinstance(c.get("LevelId"), int): out["LevelPlay"] = c["LevelId"]
    if isinstance(c.get("DungeonId"), int): out["Dungeon"] = c["DungeonId"]
    # R-76a: CheckPlayerGender {Gender: "男" | "女"}.
    if isinstance(c.get("Gender"), str): out["Gender"] = c["Gender"]
    # R-76h: CheckInRange {RangeEntities, InRange}.
    if t == "CheckInRange":
        out["Entities"] = [i for i in c.get("RangeEntities") or [] if isinstance(i, int)]
        if isinstance(c.get("InRange"), bool): out["InRange"] = c["InRange"]
    # R-82a: CheckPlayerPosition {Range, IsOnRange}: Shape 0 circle (Ext[0] radius, any height),
    # 1 box (Ext half sizes, Yaw degrees), 2 cylinder (Ext[0] radius, Ext[1] half height).
    if t == "CheckPlayerPosition" and isinstance(c.get("Range"), dict):
        r = c["Range"]
        p = r.get("Pos") or {}
        out["Center"] = [num(p.get(a)) for a in "XYZ"]
        if r.get("Type") == "BoxRange":
            s = r.get("Size") or {}
            out["Shape"] = 1
            out["Ext"] = [num(s.get(a)) for a in "XYZ"]
            out["Yaw"] = num((r.get("Rotator") or {}).get("Z"))
        elif r.get("Type") == "Cylinder":
            out["Shape"] = 2
            out["Ext"] = [num(r.get("Radius")), num(r.get("Height")), 0.0]
        else:
            out["Shape"] = 0
            out["Ext"] = [num(r.get("Radius")), 0.0, 0.0]
        if isinstance(c.get("IsOnRange"), bool): out["InRange"] = c["IsOnRange"]
    # R-82a: CheckNodeStatus / CheckChildQuestStatus {NodeId, Status} of the tree itself.
    if t in ("CheckNodeStatus", "CheckChildQuestStatus"):
        if isinstance(c.get("NodeId"), int): out["Node"] = c["NodeId"]
        if isinstance(c.get("Status"), int): out["Num"] = c["Status"]
    # R-82a: CheckItems {Compare, Items: [{ItemId, Count}]}.
    if t == "CheckItems":
        out["Items"] = [[i["ItemId"], i.get("Count") if isinstance(i.get("Count"), int) else 1]
                        for i in c.get("Items") or [] if isinstance(i, dict) and isinstance(i.get("ItemId"), int)]
    return out


def num(v, default=0.0):
    return float(v) if isinstance(v, (int, float)) and not isinstance(v, bool) else float(default)


def var_op(name, p):
    """SetVar (VarLeft = VarRight) and CalculateVar (Result = Var1 Op Var2) as one shape."""
    if name == "SetVar":
        return {"Op": "Set", "Dst": var(p.get("VarLeft")), "A": var(p.get("VarRight"))}
    return {"Op": p.get("Op") or "", "Dst": var(p.get("Result")), "A": var(p.get("Var1")), "B": var(p.get("Var2"))}


def load_client_events():
    """Action names the 3.7 client runs itself (LevelEventCenter.SetupEvent), first column."""
    path = os.path.join(HERE, "client_level_events.txt")
    with open(path, encoding="utf-8") as f:
        return {line.split()[0] for line in f if line.strip() and not line.startswith("#")}


CLIENT_EVENTS = load_client_events()


def action(a):
    out = {"Name": a.get("Name") if isinstance(a.get("Name"), str) else ""}
    p = a.get("Params")
    # R-33a: the client does not run these; the server has to (HideGroup, UnlockTeleportTrigger,
    # GetItem, ...). Their params went missing in the whitelist below; carry them whole.
    if isinstance(p, dict) and out["Name"] and out["Name"] not in CLIENT_EVENTS:
        out["Raw"] = p
        # R-76d: the client finds a bubble's flow by the action's guid (EntityAddBubbleNotify).
        if out["Name"] == "AddPlayBubble" and isinstance(a.get("ActionGuid"), str):
            out["Raw"] = dict(p, ActionGuid=a["ActionGuid"])
    if isinstance(p, dict) and out["Name"] in ("SetVar", "CalculateVar"):
        out["VarOp"] = var_op(out["Name"], p)
    if isinstance(p, dict):
        q = {}
        if isinstance(p.get("IsSelf"), bool): q["IsSelf"] = p["IsSelf"]
        if isinstance(p.get("Location"), int): q["Location"] = p["Location"]
        if isinstance(p.get("EntityState"), str): q["EntityState"] = p["EntityState"]
        if isinstance(p.get("EntityId"), int): q["EntityId"] = p["EntityId"]
        if isinstance(p.get("State"), str): q["State"] = p["State"]
        if isinstance(p.get("DungeonId"), int): q["DungeonId"] = p["DungeonId"]
        # CreatePrefab / DestroyPrefab (dungeon level plays, R-4c)
        if isinstance(p.get("PosEntityId"), int): q["PosEntityId"] = p["PosEntityId"]
        cfg = p.get("Config")
        if isinstance(cfg, dict) and isinstance(cfg.get("PrefabId"), int): q["PrefabId"] = cfg["PrefabId"]
        var = p.get("VarName")
        if isinstance(var, dict) and isinstance(var.get("Name"), str): q["Var"] = var["Name"]
        if isinstance(p.get("IsSuccess"), bool): q["IsSuccess"] = p["IsSuccess"]
        ids = p.get("EntityIds")
        if isinstance(ids, list): q["EntityIds"] = [i for i in ids if isinstance(i, int)]
        f = flow(p)
        if f: q["Flow"] = f
        if q: out["Params"] = q
    return out


def actions(lst):
    return [action(a) for a in lst if isinstance(a, dict)] if isinstance(lst, list) else []


def fix_entries(d):
    """R-82a: a node's FixProcessor (the client has no code for it; the server runs it).
    When: OnActivated / OnStartAction / OnFinishAction (as the node runs), OnProgress / Default
    (when progress is restored at login; with Period also every Period seconds while it runs).
    Then actions are never sent to the client (it can't look them up), so a SetPlayerPos with a
    fixed target becomes the server's ServerSetPlayerPos."""
    fp = d.get("FixProcessor") if isinstance(d.get("FixProcessor"), dict) else {}
    out = []
    for fa in fp.get("FixActions") or []:
        if not isinstance(fa, dict):
            continue
        g = fa.get("Condition") if isinstance(fa.get("Condition"), dict) else {}
        then = []
        for a in fa.get("ThenActions") or []:
            if not isinstance(a, dict):
                continue
            cfg = (a.get("Params") or {}).get("TelePortConfig") if isinstance(a.get("Params"), dict) else None
            if a.get("Name") == "SetPlayerPos" and isinstance(cfg, dict) and cfg.get("Type", 0) == 0 and isinstance(cfg.get("TargetPos"), dict):
                then.append({"Name": "ServerSetPlayerPos", "Raw": {"Pos": cfg["TargetPos"]}})
                continue
            x = action(a)
            if "Raw" not in x and isinstance(a.get("Params"), dict):
                x["Raw"] = a["Params"]
            then.append(x)
        out.append({
            "When": fa.get("Timing") if isinstance(fa.get("Timing"), str) else "Default",
            "Period": num(fa.get("CheckPeriod")),
            "Check": {"Count": g.get("Type") or 0, "Conds": [x for x in map(sub_condition, g.get("Conditions") or []) if x]},
            "Then": then,
        })
    return out


def node(d):
    c = d.get("Condition") if isinstance(d.get("Condition"), dict) else {}
    ctype = c.get("Type") if isinstance(c.get("Type"), str) else ""
    n = {
        "Id": d["Id"],
        "Parent": d.get("ParentNodeId", 0),
        "Type": d["Type"],
        "Count": d.get("Count", 0) if isinstance(d.get("Count"), int) else 0,
        "Cond": ctype,
        "RewardId": d.get("RewardId", 0) if isinstance(d.get("RewardId"), int) else 0,
        "DungeonId": d.get("DungeonId", 0) if isinstance(d.get("DungeonId"), int) else 0,
        "Enter": actions(d.get("EnterActions")),
        "Finish": actions(d.get("FinishActions")),
        "Actions": actions(d.get("Actions")),
        "Options": [],
        "Targets": [],
        "States": [],
    }
    f = flow(c.get("Flow"))
    if f: n["Flow"] = f
    fixes = fix_entries(d)
    if fixes: n["Fix"] = fixes
    if ctype == "Timer" and isinstance(c.get("Time"), (int, float)):
        n["Time"] = float(c["Time"])
    if ctype == "DoInteract":
        for o in c.get("AddOptions") or []:
            if not isinstance(o, dict) or not isinstance(o.get("EntityId"), int): continue
            opt = o.get("Option") or {}
            t = opt.get("Type") or {}
            n["Options"].append({
                "EntityId": o["EntityId"],
                "Guid": opt.get("Guid") if isinstance(opt.get("Guid"), str) else "",
                "TidContent": opt.get("TidContent") if isinstance(opt.get("TidContent"), str) else "",
                "Flow": flow(t.get("Flow")),
                "Actions": actions(t.get("Actions")),
            })
    # R-76i: CheckTargetBattleAttribute: monsters' health between Min and Max percent (a boss
    # phase). Only Monster/Health options are judged; any other kind leaves the step as it was
    # (completed at once).
    if ctype == "CheckTargetBattleAttribute":
        opts = [o for o in c.get("Option") or [] if isinstance(o, dict)]
        battle = []
        for o in opts:
            inner = o.get("Option") or {}
            if o.get("Type") != "Monster" or inner.get("Type") != "Health":
                battle = []
                break
            battle.append({"Ids": [i for i in o.get("EntityIds") or [] if isinstance(i, int)],
                           "Min": inner.get("Min") if isinstance(inner.get("Min"), int) else 0,
                           "Max": inner.get("Max") if isinstance(inner.get("Max"), int) else 100})
        if battle:
            n["Battle"] = battle
            n["BattleAny"] = c.get("Count") == 1
    # R-76g: HandInItems: an option on the NPC opens the hand-in view; the client sends
    # HandInItemRequest. HandIn lists what may be handed in: item ids and how many.
    if ctype == "HandInItems":
        o = c.get("AddOption")
        if isinstance(o, dict) and isinstance(o.get("EntityId"), int):
            opt = o.get("Option") or {}
            t = opt.get("Type") or {}
            n["Options"].append({
                "EntityId": o["EntityId"],
                "Guid": opt.get("Guid") if isinstance(opt.get("Guid"), str) else "",
                "TidContent": opt.get("TidContent") if isinstance(opt.get("TidContent"), str) else "",
                "Flow": flow(t.get("Flow")),
                "Actions": actions(t.get("Actions")),
            })
        h = c.get("HandInItems") or {}
        groups = [g for g in h.get("Items") or [] if isinstance(g, dict)]
        if isinstance(h.get("GroupConfig"), dict):
            groups.append(h["GroupConfig"])
        n["HandIn"] = [{"Ids": [i for i in g.get("ItemIds") or [] if isinstance(i, int)],
                        "Count": g.get("Count") if isinstance(g.get("Count"), int) else 1}
                       for g in groups if g.get("HandInType") == "ItemIds"]
    if ctype == "Kill":
        # Both lists often name the same monsters; the engine needs one kill per distinct
        # target (with duplicates 114000020 node 41 waited for 4 kills of 2 monsters, R-3b).
        for k in ("ExistTargets", "TargetsToAwake"):
            n["Targets"] += [i for i in c.get(k) or [] if isinstance(i, int) and i not in n["Targets"]]
        n["AwakeTargets"] = [i for i in c.get("TargetsToAwake") or [] if isinstance(i, int)]
        # R-128: no targets but a prefab variable: the monsters of the group CreatePrefab made
        # (深塔 低危区第 4 层 210000004, 割草爬塔 ...): wait for them like a MonsterCreator.
        var = c.get("PrefabVar")
        if not n["Targets"] and isinstance(var, dict) and isinstance(var.get("Name"), str):
            n["Var"] = var["Name"]
    # R-60 梦魇聚落: CrossSceneSyncKillMonster spawns an entity bundle (a PrefabConfig id) at
    # PosEntityId and waits for its monsters to die (in the open world kills elsewhere count too; a
    # dungeon run has only its own). Written as a MonsterCreator over group "Bundle<id>" that the
    # server creates itself when the step starts (Bundle, BundlePos). Not as an extra CreatePrefab
    # in Enter: the client numbers a node's actions as its own data has them (LevelEventNotify
    # indexes). Its CheckCrossSceneSyncKillMonsterFinished selector checks are unknown to the
    # server and count as met, so the selectors take their first ("Ne", not finished) slot.
    if ctype == "CrossSceneSyncKillMonster" and isinstance(c.get("EntityBundleId"), int):
        n["Cond"] = "MonsterCreator"
        n["Bundle"] = c["EntityBundleId"]
        n["BundlePos"] = c.get("PosEntityId") if isinstance(c.get("PosEntityId"), int) else 0
        n["Var"] = "Bundle%d" % n["Bundle"]
    if ctype == "MonsterCreator":
        var = c.get("PrefabVar")
        if isinstance(var, dict) and isinstance(var.get("Name"), str): n["Var"] = var["Name"]
        n["Creators"] = [i for i in c.get("MonsterCreatorEntityIds") or [] if isinstance(i, int)]
    # R-14: UseSkill steps the server judges from UseSkillRequest: the skill id and the optional
    # RangeSphere the player must stand in (same units as the scene's player location).
    if ctype == "UseSkill":
        chk = c.get("Check") if isinstance(c.get("Check"), dict) else {}
        if chk.get("Type") == "CheckBySkillId" and isinstance(chk.get("SkillId"), int):
            sk = {"Id": chk["SkillId"]}
            for pc in (c.get("PreConditions") or {}).get("Conditions") or []:
                if isinstance(pc, dict) and pc.get("Type") == "RangeSphere" and isinstance(pc.get("Center"), dict):
                    ctr = pc["Center"]
                    sk["Center"] = [float(ctr.get("X", 0)), float(ctr.get("Y", 0)), float(ctr.get("Z", 0))]
                    sk["Radius"] = float(pc.get("Radius") or 0)
            n["Skill"] = sk
    if ctype == "CheckEntityState":
        for s in c.get("Conditions") or []:
            if isinstance(s, dict) and isinstance(s.get("EntityId"), int) and isinstance(s.get("State"), str):
                n["States"].append({"EntityId": s["EntityId"], "State": s["State"]})
    # R-4g: Check = {Count: EConditionCount (0 all, 1 any, 2 two, ...), Conds: [sub_condition]}.
    if ctype == "CompareVar":
        n["Check"] = {"Count": c.get("Count") or 0, "Conds": [x for x in map(sub_condition, c.get("Conditions") or []) if x]}
    if ctype == "CheckConditionGroup":
        g = c.get("Condition") if isinstance(c.get("Condition"), dict) else {}
        n["Check"] = {"Count": g.get("Type") or 0, "Conds": [x for x in map(sub_condition, g.get("Conditions") or []) if x]}
    # R-4e: the dungeon a FinishDungeon / EnterDungeon step is about. Dungeon is an InstanceDungeon
    # Id, SubType an InstSubType (either one is set); Need is how many finishes (Count).
    if ctype in ("FinishDungeon", "EnterDungeon"):
        g = c.get("Condition") if ctype == "FinishDungeon" else c.get("EnterType")
        g = g if isinstance(g, dict) else {}
        if isinstance(g.get("DungeonId"), int): n["Dungeon"] = g["DungeonId"]
        if isinstance(g.get("DungeonSubType"), int): n["SubType"] = g["DungeonSubType"]
        if ctype == "FinishDungeon": n["Need"] = g.get("Count") if isinstance(g.get("Count"), int) and g["Count"] > 0 else 1
    if ctype == "CheckLevelPlay":
        n["LevelPlays"] = [i for i in c.get("LevelPlayIds") or [] if isinstance(i, int)]
        n["Need"] = c.get("CompleteNumber") if isinstance(c.get("CompleteNumber"), int) else len(n["LevelPlays"])
    # R-37f: a QuestFailed node's countdown (FailedCondition.Timer, e.g. 领主挑战's 300 s).
    if d["Type"] == "QuestFailed":
        fc = d.get("FailedCondition") if isinstance(d.get("FailedCondition"), dict) else {}
        t = (fc.get("Timer") or {}).get("Time") if isinstance(fc.get("Timer"), dict) else None
        if isinstance(t, (int, float)) and t > 0: n["FailTime"] = float(t)
    # R-37: a ConditionSelector's slots: the child each one runs and the check that picks it
    # (领主挑战 picks its boss by the level play's Level variable).
    if d["Type"] == "ConditionSelector":
        slots = []
        for s in d.get("Slots") or []:
            if not isinstance(s, dict) or not isinstance((s.get("Node") or {}).get("Id"), int): continue
            g = s.get("Condition") if isinstance(s.get("Condition"), dict) else {}
            slots.append({"Node": s["Node"]["Id"],
                          "Check": {"Count": g.get("Type") or 0, "Conds": [x for x in map(sub_condition, g.get("Conditions") or []) if x]}})
        if slots: n["Slots"] = slots
    return n


def provide_cond(c):
    """R-10: one unlock condition (QuestData ProvideType.Conditions), the fields the server reads."""
    out = {"Type": c.get("Type") if isinstance(c.get("Type"), str) else ""}
    if isinstance(c.get("PreQuest"), int):
        out["Quest"] = c["PreQuest"]
    pc = c.get("PreChildQuest")
    if isinstance(pc, dict):
        if isinstance(pc.get("QuestId"), int): out["Quest"] = pc["QuestId"]
        if isinstance(pc.get("ChildQuestId"), int): out["Node"] = pc["ChildQuestId"]
    if isinstance(c.get("ExploreLevel"), int):
        out["Level"] = c["ExploreLevel"]
    if isinstance(c.get("PreLevelPlay"), int):
        out["Play"] = c["PreLevelPlay"]
    dungeon = c.get("Dungeon")
    if isinstance(dungeon, dict) and isinstance(dungeon.get("DungeonId"), int):
        out["Dungeon"] = dungeon["DungeonId"]
    return out


def main():
    quests = {}
    for x in json.load(open(os.path.join(B, "QuestData.json"), encoding="utf-8")):
        d = x["Data"]
        pre = 0
        provide = d.get("ProvideType") or {}
        for c in provide.get("Conditions") or []:
            if isinstance(c, dict) and c.get("Type") == "PreQuest" and isinstance(c.get("PreQuest"), int):
                pre = c["PreQuest"]
        quests[d["Id"]] = {
            "Id": d["Id"],
            "Type": d.get("Type", 0),
            "Name": d.get("Key", ""),
            "PreQuest": pre,
            "RewardId": d.get("RewardId", 0) or 0,
            "DungeonId": d.get("DungeonId", 0) or 0,
            "Nodes": [],
        }
        # R-10: all unlock conditions; Type 1 combines them with OR (assumed; the main story has
        # only AND-style lists: PreQuest + OpenDate / ExploreLevel, 880000013's PreChildQuest).
        conds = [provide_cond(c) for c in provide.get("Conditions") or [] if isinstance(c, dict)]
        if conds:
            quests[d["Id"]]["Provide"] = {"Any": provide.get("Type") == 1, "Conds": conds}
    for x in json.load(open(os.path.join(B, "QuestNodeData.json"), encoding="utf-8")):
        q = quests.get(int(x["Key"].split("_")[0]))
        if q is not None:
            q["Nodes"].append(node(x["Data"]))

    orders = load_orders("Quests")
    known = sum(apply_order(q["Id"], q["Nodes"], orders) for q in quests.values() if q["Nodes"])
    print(known, "quests with their real child order")
    # R-13: entities a quest owns (EntityOwnerData Owner Type Quest), "map entity" pairs. The world
    # spawner shows such monsters only once woken (a quest step), not as ordinary world monsters.
    # R-25: also the entities of a level play that is not part of the standing open world: a
    # Dungeon, Quest, Challenge or LordGym play, or one something else has to switch on
    # (ActiveType != 0). Their sleeping monsters are other versions of a world boss or elite
    # (鸣钟之龟 has three rows: RebornBoss 122000237 for the world, Quest 121000938, Dungeon
    # 200900086) and appeared next to it. RebornBoss, MonsterTreasure (the ordinary world monster
    # groups), SilentArea, Riddle, SpecialElite and MonsterKill plays with ActiveType 0 stay.
    owned_play_types = {"Dungeon", "Quest", "Challenge", "LordGym"}
    owned_plays = set()
    for x in json.load(open(os.path.join(B, "LevelPlayData.json"), encoding="utf-8")):
        d = x["Data"]
        active = (d.get("LevelPlayActive") or {}).get("ActiveType", 0)
        if d.get("Type") in owned_play_types or active != 0:
            owned_plays.add(x["LevelPlayId"])
    owned = []
    by_play = 0
    for x in json.load(open(os.path.join(B, "EntityOwnerData.json"), encoding="utf-8")):
        owners = x.get("Owner") or []
        quest = any(o.get("Type") == "Quest" for o in owners)
        play = any(o.get("Type") == "LevelPlay" and o.get("LevelPlayId") in owned_plays for o in owners)
        if quest or play:
            m, e = x["Guid"].split("_", 1)
            owned.append([int(m), int(e)])
            if not quest: by_play += 1
    owned_path = os.path.join(os.path.dirname(OUT), "QuestOwned.json")
    json.dump(owned, open(owned_path, "w", encoding="utf-8"), separators=(",", ":"))
    print(len(owned), "quest-owned entities (", by_play, "of them through a level play ) ->", owned_path)
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    json.dump([q for q in quests.values() if q["Nodes"]], open(OUT, "w", encoding="utf-8"), ensure_ascii=False, separators=(",", ":"))
    print(len(quests), "quests ->", OUT, os.path.getsize(OUT) // 1024, "KB")


if __name__ == "__main__":
    main()
