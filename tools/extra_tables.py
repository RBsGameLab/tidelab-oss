#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-174b: tables the server reads that the client's schema (Core/Define) does not describe,
decoded from the client's own ConfigDB (the export, R-174) instead of copied from elsewhere.

  RoleDevProsList     db_RoleDev.db roledevproslist      (FlatBuffers rows, slots below)
  RoleDevProsProject  db_RoleDev.db roledevprosproject   (FlatBuffers rows, slots below)
  CommonParam         db_common_param.db commonparam     (KeyName -> raw value bytes)
  WeaponBaseAttributes  BinData WeaponConf + the zh-Hans text db (with --bindata, --lang): a weapon
                      whose description starts with a flat stat ("攻击提升{0}" ...) gets that stat at
                      each resonance level; the values are the description's first parameters
                      ("4%" -> 400 with IsRatio). Checked 2026-11-22 against the earlier copy (61):
                      values 61/61 equal; 60 weapons equal, 21010074 not made (its line starts with a
                      condition, 造成普攻或重击伤害时), 4 newer weapons added (21010076, 21020106,
                      21020107, 21050116).
  GameplayTagParent   Config/DefaultGameplayTags.ini + Config/Tags/*.ini (with --config): every tag
                      and its parents, Id = the tag's id (specs/entities.md 3.4), ParentId = its
                      parent's; checked: 13289 of the 13295 rows of the earlier copy equal (the
                      client's list is newer). GameplayTagRemap cannot be made: it maps readable
                      names to the client's obfuscated ones (segments replaced by a keyed hash).

The FlatBuffers slot order is the field order of specs/data-tables.tsv for these tables (and of
the protocol's RoleDevPropsConfig / RoleDevPropsProjectConfig, which the server builds from them);
checked on 2026-11-22: RoleDevProsProject 1411 decodes to the same row as the earlier copy.
CommonParam values carry no type: 1 byte is a bool, 4 bytes an int (the client reads some of
those as fixed-point x10000 floats), u32 count + count ints a list, u32 length + UTF-8 a string.
The server reads only landing_damage_args_role (a list) and falls back without the file.

    python -I tools/extra_tables.py --configdb <export>/Content/Aki/ConfigDB --out <BinData dir>
"""
from __future__ import annotations

import argparse
import json
import os
import sqlite3
import struct
import sys

# (field name, kind): i32, str, i32list, pairlist (vector of tables with Item1, Item2)
ROLE_DEV_PROS_LIST = [("Id", "i32"), ("TypeId", "i32"), ("RoleProspectTimeId", "i32"), ("GachaId", "i32"),
                      ("SpecialGachaId", "pairlist"), ("SortId", "i32")]
ROLE_DEV_PROS_PROJECT = [("Id", "i32"), ("ElementId", "i32"), ("RoleName", "str"), ("RoleExperience", "i32"),
                         ("RoleGoalLevel", "i32"), ("WeaponGoalLevel", "i32"), ("WeaponExperience", "i32"),
                         ("RoleItemGroup", "i32list"), ("WeaponBreachItemGroup", "i32list"), ("WeaponType", "i32"),
                         ("SkillItemGroup", "i32list"), ("PrefectSkillLevel", "i32list"), ("RoleHeadIcon", "str"),
                         ("RoleHeadIconSmall", "str"), ("FormationRoleCard", "str")]
DEFAULTS = {"i32": 0, "str": "", "i32list": [], "pairlist": []}


class Table:
    """A FlatBuffers table at `pos` of buffer `b`."""

    def __init__(self, b: bytes, pos: int):
        self.b, self.pos = b, pos
        vt = pos - struct.unpack_from("<i", b, pos)[0]
        vlen = struct.unpack_from("<H", b, vt)[0]
        self.slots = [struct.unpack_from("<H", b, vt + 4 + 2 * i)[0] for i in range((vlen - 4) // 2)]

    def field(self, slot: int) -> int | None:
        if slot >= len(self.slots) or not self.slots[slot]:
            return None
        return self.pos + self.slots[slot]

    def i32(self, slot: int) -> int:
        p = self.field(slot)
        return 0 if p is None else struct.unpack_from("<i", self.b, p)[0]

    def _target(self, slot: int) -> int | None:
        p = self.field(slot)
        return None if p is None else p + struct.unpack_from("<I", self.b, p)[0]

    def str(self, slot: int) -> str:
        t = self._target(slot)
        if t is None:
            return ""
        n = struct.unpack_from("<I", self.b, t)[0]
        return self.b[t + 4:t + 4 + n].decode("utf-8")

    def i32list(self, slot: int) -> list[int]:
        t = self._target(slot)
        if t is None:
            return []
        n = struct.unpack_from("<I", self.b, t)[0]
        return list(struct.unpack_from(f"<{n}i", self.b, t + 4))

    def pairlist(self, slot: int) -> list[dict]:
        t = self._target(slot)
        if t is None:
            return []
        n = struct.unpack_from("<I", self.b, t)[0]
        out = []
        for i in range(n):
            e = t + 4 + 4 * i
            sub = Table(self.b, e + struct.unpack_from("<I", self.b, e)[0])
            out.append({"Item1": sub.i32(0), "Item2": sub.i32(1)})
        return out


def decode_row(blob: bytes, schema: list[tuple[str, str]]) -> dict:
    t = Table(blob, struct.unpack_from("<I", blob, 0)[0])
    return {name: getattr(t, kind)(slot) for slot, (name, kind) in enumerate(schema)}


def common_value(b: bytes):
    if len(b) == 1:
        return b[0] != 0
    if len(b) >= 4:
        n = struct.unpack_from("<I", b, 0)[0]
        if len(b) == 4:
            return struct.unpack_from("<i", b, 0)[0]
        if len(b) == 4 + 4 * n:
            return list(struct.unpack_from(f"<{n}i", b, 4))
        if len(b) == 4 + n:
            try:
                return b[4:].decode("utf-8")
            except UnicodeDecodeError:
                pass
    return b.hex()


def tag_id(name: str) -> int:
    """32-bit FNV-1 with basis 0 over the UTF-16 code units, signed (specs/entities.md 3.4)."""
    h = 0
    data = name.encode("utf-16-le")
    for i in range(0, len(data), 2):
        h = ((h ^ (data[i] | data[i + 1] << 8)) * 16777619) & 0xFFFFFFFF
    return h - (1 << 32) if h >= 1 << 31 else h


def gameplay_tag_parents(config: str) -> list[dict]:
    import glob
    import re
    files = [os.path.join(config, "DefaultGameplayTags.ini")] + sorted(glob.glob(os.path.join(config, "Tags", "*.ini")))
    tags = set()
    for f in files:
        if os.path.isfile(f):
            with open(f, encoding="utf-8-sig", errors="replace") as fh:
                tags.update(m.group(1) for m in re.finditer(r'GameplayTagList=\(Tag="([^"]+)"', fh.read()))
    every = set()
    for t in tags:
        parts = t.split(".")
        every.update(".".join(parts[:i]) for i in range(1, len(parts) + 1))
    return sorted(({"Id": tag_id(t), "ParentId": tag_id(t.rsplit(".", 1)[0])} for t in every if "." in t), key=lambda r: r["Id"])


# The stat phrase before {0} at the start of a weapon's description -> attribute ids (the ids
# combat.MapConfigPropID reads; from the earlier copy, one phrase each).
WEAPON_STAT = {
    "攻击提升": [7], "生命提升": [2], "防御提升": [10], "暴击提升": [8], "共鸣效率提升": [11],
    "全属性伤害加成提升": [22, 23, 24, 25, 26, 27], "普攻和重击伤害加成提升": [17, 18],
    "共鸣技能伤害加成提升": [14], "共鸣解放伤害加成提升": [19],
}


def weapon_base_attributes(bindata: str, lang_db: str) -> list[dict]:
    with open(os.path.join(bindata, "WeaponConf.json"), encoding="utf-8") as f:
        weapons = json.load(f)
    c = sqlite3.connect(f"file:{lang_db}?mode=ro", uri=True)
    try:
        def text(key: str) -> str:
            r = c.execute("select Content from MultiText where Id = ?", (key,)).fetchone()
            return r[0] if r else ""
        out = []
        for w in weapons:
            desc = text(w.get("Desc") or "")
            at = desc.find("{0}")
            attrs = WEAPON_STAT.get(desc[:at]) if at > 0 else None
            params = w.get("DescParams") or []
            first = params[0].get("ArrayString") if params else None
            if not attrs or not first:
                continue
            ratio = all(v.strip().endswith("%") for v in first)
            values = {str(i + 1): round(float(v.strip().rstrip("%")) * (100 if ratio else 1)) for i, v in enumerate(first)}
            out.append({"ItemId": w["ItemId"], "IsRatio": ratio, "Attributes": attrs, "Values": values})
        return sorted(out, key=lambda r: r["ItemId"])
    finally:
        c.close()


def rows(db: str, table: str):
    c = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        return c.execute(f"select * from '{table}'").fetchall()
    finally:
        c.close()


def build(configdb: str, config: str | None = None, bindata: str | None = None) -> dict[str, object]:
    role_dev = os.path.join(configdb, "db_RoleDev.db")
    common = os.path.join(configdb, "db_common_param.db")
    out: dict[str, object] = {
        "RoleDevProsList": [decode_row(r[1], ROLE_DEV_PROS_LIST) for r in rows(role_dev, "roledevproslist")],
        "RoleDevProsProject": [decode_row(r[1], ROLE_DEV_PROS_PROJECT) for r in rows(role_dev, "roledevprosproject")],
        "CommonParam": {k: common_value(v) for k, v in rows(common, "commonparam")},
    }
    if config:
        out["GameplayTagParent"] = gameplay_tag_parents(config)
    lang = os.path.join(configdb, "zh-Hans", "lang_multi_text.db")
    if bindata and os.path.isfile(lang):
        out["WeaponBaseAttributes"] = weapon_base_attributes(bindata, lang)
    for name in ("RoleDevProsList", "RoleDevProsProject"):
        out[name] = sorted(out[name], key=lambda r: r["Id"])  # type: ignore[arg-type,index]
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--configdb", required=True)
    ap.add_argument("--config", help="<export>/Config, for GameplayTagParent")
    ap.add_argument("--bindata", help="the exported BinData (WeaponConf), for WeaponBaseAttributes")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    tables = build(a.configdb, a.config, a.bindata)
    os.makedirs(a.out, exist_ok=True)
    for name, value in tables.items():
        with open(os.path.join(a.out, name + ".json"), "w", encoding="utf-8", newline="\n") as f:
            json.dump(value, f, ensure_ascii=False, indent=1)
    print("extra_tables: " + ", ".join(f"{n} {len(v)}" for n, v in tables.items()))  # type: ignore[arg-type]
    return 0


if __name__ == "__main__":
    sys.exit(main())
