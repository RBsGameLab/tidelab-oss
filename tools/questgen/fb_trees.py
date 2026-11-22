# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""Reads the client's behavior tree files (FlatBuffers) for the real child order.

The flattened BinData tables (QuestNodeData, LevelPlayNodeData) keep only ParentNodeId; the
shipping client loads each tree from Content/Aki/Config/UniverseEditorConfig/{Quests,LevelPlays}/
<id>.bytes (QuestNewModel.LoadQuestFromFb), exported with FModel ("Export Folder's Packages Raw
Data"). Schema: reference/client-src Aki.TdInterface/UniverseEditor/Interface/Fbs/Quest.

Slots (field index; vtable entry 4 + 2*slot):
  IQuest.Tree 41, ILevelPlay.Tree 42 -> IBehaviorTree { Vars 0, Root 1 }
    (3.8 added a field before Tree in both: 42, 43. Set FB_TREE_SLOTS=42,43 for 3.8 files, R-107.)
  every node: Type 0 (EBtNode), Id 1 (int), Disabled 5 (bool)
  IStartBtNode / IAlwaysTrue / IAlwaysFalse / IRepeater: ChildType 6, Child 7
  ISequence / ISelect / IParallelSelect: ChildrenType 38, Children 39
  IConditionSelector: Slots 39 -> slot { NodeType 1, Node 2 }

usage:
  python -I tools\\questgen\\fb_trees.py <export dir> [tree id ...]   print trees
  python -I tools\\questgen\\fb_trees.py <export dir> --out <QUESTGEN_DIR>\\assets\\quest\\TreeOrders.json
      every tree's child order, read by gen_quest_data.py and gen_levelplay_data.py (Kids)
"""
import json
import os
import struct
import sys

# IQuest.Tree and ILevelPlay.Tree slots: 41, 42 up to 3.7; 42, 43 in 3.8 (R-107).
TREE_SLOTS = dict(zip(("Quests", "LevelPlays"), map(int, (os.environ.get("FB_TREE_SLOTS") or "41,42").split(","))))

# TBtNode union types (TBtNode.cs).
T_NAMES = {1: "Action", 2: "ActionWithResult", 3: "AlwaysFalse", 4: "AlwaysTrue", 5: "ChildQuest",
           6: "Condition", 7: "ConditionSelector", 8: "ParallelSelect", 9: "QuestFailed",
           10: "QuestSucceed", 11: "Repeater", 12: "Select", 13: "Sequence", 14: "Start"}
ONE_CHILD = {"Start", "AlwaysTrue", "AlwaysFalse", "Repeater"}
MANY = {"Sequence", "Select", "ParallelSelect"}


class Buf:
    def __init__(self, data):
        self.b = data

    def u8(self, p): return self.b[p]
    def u16(self, p): return struct.unpack_from("<H", self.b, p)[0]
    def i32(self, p): return struct.unpack_from("<i", self.b, p)[0]
    def u32(self, p): return struct.unpack_from("<I", self.b, p)[0]

    def field(self, table, slot):
        """Absolute position of a field, or None if absent (default value)."""
        vt = table - self.i32(table)
        vt_len = self.u16(vt)
        entry = 4 + 2 * slot
        if entry >= vt_len:
            return None
        off = self.u16(vt + entry)
        return table + off if off else None

    def deref(self, pos):
        return pos + self.u32(pos)

    def table(self, table, slot):
        p = self.field(table, slot)
        return self.deref(p) if p is not None else None

    def vector(self, table, slot):
        p = self.field(table, slot)
        if p is None:
            return None, 0
        v = self.deref(p)
        return v + 4, self.u32(v)

    def int_field(self, table, slot, default=0):
        p = self.field(table, slot)
        return self.i32(p) if p is not None else default

    def bool_field(self, table, slot):
        p = self.field(table, slot)
        return bool(self.u8(p)) if p is not None else False


def read_tree(path, tree_slot):
    """Returns (nodes in depth-first order, child order per parent id, disabled ids)."""
    with open(path, "rb") as f:
        b = Buf(f.read())
    root_table = b.deref(0)
    tree = b.table(root_table, tree_slot)
    if tree is None:
        return None
    start = b.table(tree, 1)
    if start is None:
        return None
    nodes, order, disabled = [], {}, []

    def visit(node, kind, parent):
        nid = b.int_field(node, 1)
        nodes.append((nid, kind, parent))
        if b.bool_field(node, 5):
            disabled.append(nid)
        kids = []
        if kind in ONE_CHILD:
            ctype = b.field(node, 6)
            child = b.table(node, 7)
            if child is not None and ctype is not None:
                kids.append((child, T_NAMES.get(b.u8(ctype), "?")))
        elif kind in MANY:
            tpos, tn = b.vector(node, 38)
            cpos, cn = b.vector(node, 39)
            for i in range(min(tn, cn)):
                kids.append((b.deref(cpos + 4 * i), T_NAMES.get(b.u8(tpos + i), "?")))
        elif kind == "ConditionSelector":
            spos, sn = b.vector(node, 39)
            for i in range(sn):
                slot = b.deref(spos + 4 * i)
                ntype = b.field(slot, 1)
                child = b.table(slot, 2)
                if child is not None and ntype is not None:
                    kids.append((child, T_NAMES.get(b.u8(ntype), "?")))
        child_ids = []
        for child, ckind in kids:
            child_ids.append(visit(child, ckind, nid))
        if child_ids:
            order[nid] = child_ids
        return nid

    visit(start, "Start", 0)
    return nodes, order, disabled


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    base = sys.argv[1]
    args = sys.argv[2:]
    dirs = tuple(TREE_SLOTS.items())
    if args and args[0] == "--out":
        out = {"Quests": {}, "LevelPlays": {}}
        for sub, slot in dirs:
            d = os.path.join(base, sub)
            for name in os.listdir(d):
                if not name.endswith(".bytes"):
                    continue
                r = read_tree(os.path.join(d, name), slot)
                if r is None:
                    continue
                nodes, order, disabled = r
                entry = {"Order": {str(k): v for k, v in order.items()}}
                if disabled:
                    entry["Disabled"] = disabled
                out[sub][name[:-6]] = entry
        with open(args[1], "w", encoding="utf-8") as f:
            json.dump(out, f, ensure_ascii=False, separators=(",", ":"))
        print({k: len(v) for k, v in out.items()}, "->", args[1])
        return
    for tid in args:
        for sub, slot in dirs:
            p = os.path.join(base, sub, tid + ".bytes")
            if os.path.exists(p):
                nodes, order, disabled = read_tree(p, slot)
                print(f"{sub}/{tid}: {len(nodes)} nodes, disabled {disabled}")
                for parent, kids in order.items():
                    print(f"  {parent} -> {kids}")


if __name__ == "__main__":
    main()
