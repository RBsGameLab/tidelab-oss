# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-61: builds <QUESTGEN_DIR>/assets/quest/QuestOffers.json, how quests outside the main story are given.

QuestData.DistributeType says how the official server hands a quest out:
  System    accepted as soon as its unlock conditions (ProvideType) hold;
  Interact  offered (QuestReadyListNotify); the player accepts it at an NPC: the server puts the
            quest's AddInteractOption on that entity (EntityAddDynamicInteractNotify, Guid from the
            client's InteractData), plays its flow and then accepts the quest.
Passive, Activity, Daily, PhoneMessage and UseItem start other ways and are left out.

Only the quest panel types the server gives out  are written. The
unlock conditions themselves are in Quests.json (Provide), written by gen_quest_data.py; this
script does not touch Quests.json.

usage: python -I tools\\questgen\\gen_quest_offers.py   (QUESTGEN_DIR as in gen_quest_data.py)
"""
import collections, json, os, sys

QG = os.environ.get("QUESTGEN_DIR") or "questgen"
B = os.path.join(QG, "assets", "BinData")
QUESTS = os.path.join(QG, "assets", "quest", "Quests.json")
OUT = os.path.join(QG, "assets", "quest", "QuestOffers.json")

# Quest panel types (QuestType.IsShowInQuestPanel) other than the main story (1, its own rules),
# daily (4) and activity (10) quests: 支线 2, 角色 3, 8 (shown as 支线), 地点 9, 16, 17.
# 引导 7 keeps its own rule in the server but its Interact quests are offered too.
OFFER_TYPES = {2, 3, 7, 8, 9, 16, 17}


def option(d):
    """The accept option: entity, Guid, text and flow. None if it is not a flow option."""
    o = d.get("AddInteractOption") or {}
    opt = o.get("Option") or {}
    flow = (opt.get("Type") or {}).get("Flow") or {}
    if not isinstance(o.get("EntityId"), int) or not opt.get("Guid") or not flow.get("FlowListName"):
        return None
    return {
        "EntityId": o["EntityId"],
        "Guid": opt["Guid"],
        "TidContent": opt.get("TidContent", ""),
        "Flow": {"FlowListName": flow["FlowListName"], "FlowId": flow.get("FlowId", 0), "StateId": flow.get("StateId", 0)},
    }


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    with open(QUESTS, encoding="utf-8") as f:
        have = {q["Id"] for q in json.load(f)}
    offers = []
    skipped = collections.Counter()
    with open(os.path.join(B, "QuestData.json"), encoding="utf-8") as f:
        rows = json.load(f)
    for x in rows:
        d = x["Data"]
        if isinstance(d, str):
            d = json.loads(d)
        kind = d.get("DistributeType")
        if d.get("Type") not in OFFER_TYPES or kind not in ("System", "Interact"):
            continue
        if d["Id"] not in have:
            skipped["no nodes"] += 1
            continue
        entry = {"Id": d["Id"], "Interact": kind == "Interact"}
        # R-62: Item unlock conditions (Quests.json keeps only the condition's type): every
        # [item, count] the player must hold.
        items = []
        for c in (d.get("ProvideType") or {}).get("Conditions") or []:
            if isinstance(c, dict) and c.get("Type") == "Item":
                items += [[i["ItemId"], i.get("Count", 1)] for i in c.get("Items") or [] if isinstance(i.get("ItemId"), int)]
        if items:
            entry["Items"] = items
        if kind == "Interact":
            opt = option(d)
            if opt is None:
                skipped["Interact without a flow option"] += 1
                continue
            entry["Option"] = opt
        offers.append(entry)
    offers.sort(key=lambda e: e["Id"])
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(offers, f, ensure_ascii=False, separators=(",", ":"))
    n_int = sum(e["Interact"] for e in offers)
    print(len(offers), "offers (", len(offers) - n_int, "System,", n_int, "Interact,", sum("Items" in e for e in offers), "with items ) ->", OUT)
    for k, v in skipped.items():
        print("skipped", v, k)


if __name__ == "__main__":
    main()
