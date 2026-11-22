# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""R-83: the initial values of behavior tree variables, from the client's tree files.

A quest or level play declares its variables in IBehaviorTree.Vars (slot 0, a vector of
IVarDefine). Schema (reference: SphinX2k6/wuwa_c-script
Aki.TdInterface/UniverseEditor/Interface/Fbs/Var/IVarDefine.cs):
  IVarDefine { Name 0 (string), Type 1 (EVar), ValueType 2 (TVarValue), Value 3 (table),
               Access 4 (EVarAccess), IsIgnoreOnRollBack 5, IsClient 6, ... }
  EVar: Boolean 0, Int 1, String 2, Float 3, Entity 4, Quest 5, QuestState 6, Transform 7, Prefab 8
  TVarValue: BooleanValue 1, IntValue 2, FloatValue 3, StringValue 4 (each { Value 0 }), ...
  EVarAccess: Private 0, Public 1, Global 2

The server reads a variable nobody has set as its declared value. Only
Boolean/Int/Float/String variables whose value is not 0 / false / "" are written: everything else
starts at zero anyway. 3.7: 509 variables declared in 246 quests, 48 of them non-zero (32
quests); 669 non-zero in 376 level plays. None is IsClient.

usage:
  python -I tools\\questgen\\fb_vars.py <export dir> [tree id ...]      print the trees' variables
  python -I tools\\questgen\\fb_vars.py <export dir> --out <QUESTGEN_DIR>\\assets\\quest\\TreeVars.json
The export dir is FModel's .../Content/Aki/Config/UniverseEditorConfig (Quests/, LevelPlays/).
"""
import json
import os
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import fb_trees  # noqa: E402

EVAR = ["Boolean", "Int", "String", "Float", "Entity", "Quest", "QuestState", "Transform", "Prefab"]
ACCESS = ["Private", "Public", "Global"]
TREE_SLOT = fb_trees.TREE_SLOTS  # FB_TREE_SLOTS=42,43 for 3.8 (R-107)


def string_at(b, table, slot):
    p = b.field(table, slot)
    if p is None:
        return None
    s = b.deref(p)
    n = b.u32(s)
    return b.b[s + 4:s + 4 + n].decode("utf-8")


def read_vars(path, tree_slot):
    """The tree's declared variables: [{Name, T, V, Access, IsClient}]."""
    with open(path, "rb") as f:
        b = fb_trees.Buf(f.read())
    tree = b.table(b.deref(0), tree_slot)
    if tree is None:
        return []
    pos, n = b.vector(tree, 0)
    out = []
    for i in range(n):
        v = b.deref(pos + 4 * i)
        p = b.field(v, 1)
        typ = EVAR[b.u8(p)] if p is not None else "Boolean"
        p = b.field(v, 2)
        vt = b.u8(p) if p is not None else 0
        val = None
        vtab = b.table(v, 3)
        if vtab is not None:
            if vt == 1:
                val = b.bool_field(vtab, 0)
            elif vt == 2:
                val = b.int_field(vtab, 0)
            elif vt == 3:
                p = b.field(vtab, 0)
                val = struct.unpack_from("<f", b.b, p)[0] if p is not None else 0.0
            elif vt == 4:
                val = string_at(b, vtab, 0) or ""
        p = b.field(v, 4)
        acc = ACCESS[b.u8(p)] if p is not None else "Private"
        out.append({"Name": string_at(b, v, 0) or "", "T": typ, "V": val, "Access": acc,
                    "IsClient": b.bool_field(v, 6)})
    return out


def initial(v):
    """The entry the server reads ({Name, Num} or {Name, Str}), or None if it starts at zero."""
    t, val = v["T"], v["V"]
    if t == "String":
        return {"Name": v["Name"], "Str": val} if val else None
    if t in ("Boolean", "Int", "Float") and val not in (None, False, 0, 0.0):
        return {"Name": v["Name"], "Num": float(1 if val is True else val)}
    return None


def main(argv):
    sys.stdout.reconfigure(encoding="utf-8")
    if not argv:
        print(__doc__)
        return 2
    root = argv[0]
    out_path = None
    ids = set()
    rest = argv[1:]
    while rest:
        a = rest.pop(0)
        if a == "--out":
            out_path = rest.pop(0)
        else:
            ids.add(int(a))
    result = {}
    counts = {}
    for kind, slot in TREE_SLOT.items():
        d = os.path.join(root, kind)
        trees = {}
        nvars = 0
        for fn in sorted(os.listdir(d)):
            if not fn.endswith(".bytes"):
                continue
            tid = int(fn[:-6])
            vs = read_vars(os.path.join(d, fn), slot)
            if tid in ids:
                print(kind, tid, json.dumps(vs, ensure_ascii=False))
            init = [e for e in (initial(v) for v in vs) if e]
            if init:
                trees[str(tid)] = init
                nvars += len(init)
        result[kind] = trees
        counts[kind] = (len(trees), nvars)
    for kind, (t, n) in counts.items():
        print(f"{kind}: {n} non-zero initial values in {t} trees")
    if out_path:
        with open(out_path, "w", encoding="utf-8", newline="\n") as f:
            json.dump(result, f, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
            f.write("\n")
        print("wrote", out_path)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
