# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""Builds <QUESTGEN_DIR>/assets/quest/FlowActions.json: the server-side actions inside flows (剧情).

The client runs a flow itself but asks the server (FlowActionRequest{FlowIncId, ActionId}) to do
actions it can't, e.g. AwakeEntity, and waits for the result (an NPC that must appear). FlowState.json
is ~100 MB, so this keeps only states with such actions, keyed "<FlowListName>_<FlowId>_<StateId>".

usage: python -I tools\\questgen\\gen_flow_actions.py   (QUESTGEN_DIR as in gen_quest_data.py)
"""
import json, os, sys

QG = os.environ.get("QUESTGEN_DIR") or "questgen"
B = os.path.join(QG, "assets", "BinData")
OUT = os.path.join(QG, "assets", "quest", "FlowActions.json")
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_quest_data import action  # noqa: E402

# Flow actions the server executes; the rest are the client's.
server_actions = {"AwakeEntity", "DestroyEntity", "ChangeEntityState", "SetPlayerPos"}

# SetPlayerPos: TelePortConfig Type 0 = TargetPos, Type 1 = first of EntityIds (its placement).
entity_pos = {}
for f in ("LevelEntityConfig_1.json", "LevelEntityConfig_2.json"):
    for e in json.load(open(os.path.join(B, f), encoding="utf-8")):
        t = e.get("Transform") or []
        if t and e["EntityId"] not in entity_pos:
            r = t[1] if len(t) > 1 and isinstance(t[1], dict) else {}
            entity_pos[e["EntityId"]] = [t[0]["X"] / 100, t[0]["Y"] / 100, t[0]["Z"] / 100, r.get("Z", 0)]


def player_pos(params):
    tp = params.get("TelePortConfig") if isinstance(params, dict) else None
    if not isinstance(tp, dict):
        return None
    if isinstance(tp.get("TargetPos"), dict):
        p = tp["TargetPos"]
        return [p.get("X", 0), p.get("Y", 0), p.get("Z", 0), p.get("A", 0)]
    for i in tp.get("EntityIds") or []:
        if i in entity_pos:
            return entity_pos[i]
    return None


def walk(node, out):
    if isinstance(node, dict):
        name = node.get("Name")
        if isinstance(name, str) and isinstance(node.get("ActionId"), int) and name in server_actions:
            a = action(node)
            a["ActionId"] = node["ActionId"]
            if name == "SetPlayerPos":
                pos = player_pos(node.get("Params"))
                if pos is None:
                    return
                a["Pos"] = pos
            out.append(a)
        for v in node.values():
            walk(v, out)
    elif isinstance(node, list):
        for v in node:
            walk(v, out)


states = []
for s in json.load(open(os.path.join(B, "FlowState.json"), encoding="utf-8")):
    acts = []
    walk(s.get("Actions"), acts)
    if acts:
        states.append({"Key": s["StateKey"], "Actions": acts})
json.dump(states, open(OUT, "w", encoding="utf-8"), ensure_ascii=False, separators=(",", ":"))
print(len(states), "flow states with server actions ->", OUT, os.path.getsize(OUT) // 1024, "KB")
