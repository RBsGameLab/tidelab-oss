# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-80a: assets/quest/StateSetters.json -- for every entity state a quest or level play waits
for, the ways the game data has of producing it.

The story robot used to skip such a step whenever the state needed a mechanism (28 of its 74
skips): the entity was there but nothing put it into the state. This file says who can:

  interact  the player picks option <Option> of entity <Id> (the server runs the option's actions)
  chain     entity <Id> entering <State> sets it (its EntityStateComponent.StateChangeBehaviors)
  group     the 机关组 <Id> sets it once its members are in <Need>
  play      level play <Id> sets it (the robot does not run plays for this)
  client    the client's own mechanism does it and reports it (a trigger the player walks into,
            a 钩锁 hook, a 控物 jigsaw, a minigame): the robot sends ChangeEntityStateRequest,
            which is what the client's WorldFunctionLibrary.ChangeEntityState does.

Ways come in that order: the ones that go through server code first, `client` last.

usage:
  python -I tools\\questgen\\gen_state_setters.py            (writes the file, prints a summary)
  python -I tools\\questgen\\gen_state_setters.py --check    (prints only)
  QUESTGEN_DIR=... for a worktree.
"""
import collections
import io
import json
import os
import sys

QG = os.environ.get("QUESTGEN_DIR") or "questgen"
B = os.path.join(QG, "assets", "BinData")
OUT = os.path.join(QG, "assets", "quest", "StateSetters.json")

# A component whose actions the server runs when the player picks an option.
INTERACT = "InteractComponent"


def load(name):
    with open(os.path.join(B, name), encoding="utf-8") as f:
        return json.load(f)


def walk_actions(node, path, out):
    """(path, action) for every action-shaped dict (a Name plus Params)."""
    if isinstance(node, dict):
        if isinstance(node.get("Name"), str) and "Params" in node:
            out.append((path, node))
            return
        for k, v in node.items():
            walk_actions(v, f"{path}.{k}", out)
    elif isinstance(node, list):
        for i, v in enumerate(node):
            walk_actions(v, f"{path}[{i}]", out)


def state_set_by(action, owner):
    """The (entity, state) this action puts into a state, or None. `owner`: whose action it is."""
    name = action.get("Name")
    p = action.get("Params") or {}
    if name == "ChangeEntityState":
        state = p.get("State")
        if not state:
            return None
        ids = [p["EntityId"]] if p.get("EntityId") else []
        ids += list(p.get("EntityIds") or [])
        return [(i, state) for i in ids if i]
    if name in ("ChangeSelfEntityState", "ChangeSelfState"):
        state = p.get("EntityState") or p.get("State")
        return [(owner, state)] if state else None
    return None


def wanted_pairs():
    """(entity, state) pairs any quest or level play condition checks."""
    pairs = set()

    def walk(node):
        if isinstance(node, dict):
            if node.get("Type") in ("CheckEntityState", "CompareEntityState"):
                for c in node.get("Conditions") or []:
                    if c.get("EntityId") and c.get("State"):
                        pairs.add((c["EntityId"], c["State"]))
                if node.get("EntityId") and node.get("State"):
                    pairs.add((node["EntityId"], node["State"]))
            for v in node.values():
                walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)

    for f in ("QuestNodeData.json", "LevelPlayNodeData.json"):
        for row in load(f):
            walk(row["Data"])
    return pairs


def main():
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8")
    check = "--check" in sys.argv
    pairs = wanted_pairs()
    wanted = collections.defaultdict(set)      # entity -> {state}
    for e, s in pairs:
        wanted[e].add(s)
    print(f"{len(pairs)} (entity, state) pairs checked by quests and level plays")

    # ways[(map, entity, state)] -> list of ways
    ways = collections.defaultdict(list)
    # Which maps an entity exists on, and whether it can hold a state at all.
    has_state_comp = collections.defaultdict(set)

    for f in ("LevelPlayNodeData.json", "QuestNodeData.json"):
        kind = "play" if f.startswith("LevelPlay") else "quest"
        for row in load(f):
            acts = []
            walk_actions(row["Data"], "", acts)
            owner = int(str(row["Key"]).split("_")[0])
            for _, a in acts:
                for ent, state in state_set_by(a, 0) or []:
                    if state in wanted.get(ent, ()):
                        # The map is unknown here: recorded for every map the entity is on.
                        ways[(None, ent, state)].append({"Kind": kind, "Id": owner})

    tmpl = {t["BlueprintType"]: t for t in load("TemplateConfig.json")}
    for f in ("LevelEntityConfig_1.json", "LevelEntityConfig_2.json"):
        for e in load(f):
            eid, mapid = e.get("EntityId"), e.get("MapId")
            comps = dict((tmpl.get(e.get("BlueprintType")) or {}).get("ComponentsData") or {})
            comps.update(e.get("ComponentsData") or {})
            if "EntityStateComponent" in comps:
                has_state_comp[eid].add(mapid)
            for cname, c in comps.items():
                # An interact option: remember which option sets what.
                if cname == INTERACT:
                    for k, o in enumerate(c.get("Options") or []):
                        acts = []
                        walk_actions((o.get("Type") or {}).get("Actions") or [], "", acts)
                        for _, a in acts:
                            for ent, state in state_set_by(a, eid) or []:
                                if state in wanted.get(ent, ()):
                                    ways[(mapid, ent, state)].append(
                                        {"Kind": "interact", "Id": eid, "Option": k})
                    continue
                # A state's own behaviours: the source entering that state sets the target.
                if cname == "EntityStateComponent":
                    for b in c.get("StateChangeBehaviors") or []:
                        src_state = b.get("State")
                        acts = []
                        walk_actions(b.get("Action") or [], "", acts)
                        for _, a in acts:
                            for ent, state in state_set_by(a, eid) or []:
                                if state in wanted.get(ent, ()) and src_state and ent != eid:
                                    ways[(mapid, ent, state)].append(
                                        {"Kind": "chain", "Id": eid, "State": src_state})
                    continue
                # A 机关组: its triggers' actions, and its own FinishState / FailureState.
                if cname == "EntityGroupComponent":
                    if c.get("Disabled"):
                        continue
                    for t in c.get("StateTriggers") or []:
                        need = [{"Id": x["EntityId"], "State": x.get("State") or ""}
                                for x in ((t.get("GroupCondition") or {}).get("Conditions") or [])
                                if x.get("EntityId")]
                        acts = []
                        walk_actions(t.get("SuccessActions") or [], "", acts)
                        for _, a in acts:
                            for ent, state in state_set_by(a, eid) or []:
                                if state in wanted.get(ent, ()):
                                    ways[(mapid, ent, state)].append(
                                        {"Kind": "group", "Id": eid, "Need": need})
                    for key, state in (("FinishState", "关卡.Common.状态.完成"),
                                       ("FailureState", "关卡.Common.状态.失败")):
                        conds = ((c.get(key) or {}).get("GroupCondition") or {}).get("Conditions") or []
                        need = [{"Id": x["EntityId"], "State": x.get("State") or ""}
                                for x in conds if x.get("EntityId")]
                        if need and state in wanted.get(eid, ()):
                            ways[(mapid, eid, state)].append(
                                {"Kind": "group", "Id": eid, "Need": need})
                    continue
                # Anything else (a trigger the player walks into, a 钩锁 hook, a jigsaw
                # foundation, a sequence event, a condition listener): the client does it.
                acts = []
                walk_actions(c, cname, acts)
                for _, a in acts:
                    for ent, state in state_set_by(a, eid) or []:
                        if state in wanted.get(ent, ()):
                            ways[(mapid, ent, state)].append({"Kind": "client", "Id": ent})

    # Spread the quest / level play ways over the maps the entity is on, and give every pair a
    # last-resort `client` way (what the player's own mechanism reports).
    order = {"interact": 0, "chain": 1, "group": 2, "quest": 3, "play": 4, "client": 5}
    out = []
    for eid, states in sorted(wanted.items()):
        for state in sorted(states):
            for mapid in sorted(has_state_comp.get(eid, ())):
                got = list(ways.get((mapid, eid, state), ()))
                got += ways.get((None, eid, state), ())
                got.append({"Kind": "client", "Id": eid})
                seen, uniq = set(), []
                for w in sorted(got, key=lambda w: order[w["Kind"]]):
                    key = json.dumps(w, sort_keys=True, ensure_ascii=False)
                    if key in seen:
                        continue
                    seen.add(key)
                    uniq.append(w)
                out.append({"Map": mapid, "Id": eid, "State": state, "Ways": uniq})

    kinds = collections.Counter(w["Kind"] for e in out for w in e["Ways"])
    first = collections.Counter(e["Ways"][0]["Kind"] for e in out)
    print(f"{len(out)} (map, entity, state) entries; ways {dict(kinds)}")
    print(f"best way per entry: {dict(first)}")
    if check:
        return
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, separators=(",", ":"))
    print(f"wrote {OUT} ({os.path.getsize(OUT) / 1e6:.1f} MB)")


if __name__ == "__main__":
    main()
