#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-161 game data peek: the client's tables (BinData, then BinData-beta) and the quest data,
without writing a one-off script. Full results go to out/tl/data.json; the console gets a summary.

    python -I tools/tl_data.py fields Activity                 field names, row count
    python -I tools/tl_data.py row Activity 100200001          rows whose Id (or --key) is the value
    python -I tools/tl_data.py where GachaViewInfo Type=2 [Sort>3 …]   rows matching all conditions
    python -I tools/tl_data.py count TowerConfig Season        rows per value of a field
    python -I tools/tl_data.py text Activity_100200001_Title   zh-Hans text of a key (3.8 lang db)

Tables: a name (Activity → Activity.json) or a quest file (quest:TowerFloors). Conditions:
field=value, field!=value, field>n, field<n, field~substring; a list field matches when any
element does. --limit N (default 5) rows printed.
"""

from __future__ import annotations

import collections
import json
import os
import re
import sqlite3
import sys
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_paths as P  # noqa: E402

LANG = P.LAB / "reference" / "lang38" / "Client" / "Content" / "Aki" / "ConfigDB" / "zh-Hans" / "lang_multi_text.db"


def load(table: str) -> list[dict]:
    if table.startswith("quest:"):
        path = P.QUEST_DATA / (table[6:] + ".json")
    else:
        path = next((d / (table + ".json") for d in (P.BINDATA, P.MORE_BINDATA) if (d / (table + ".json")).exists()), None)
    if path is None or not path.exists():
        raise SystemExit(f"no table {table}")
    v = json.loads(path.read_text(encoding="utf-8"))
    if isinstance(v, dict):
        v = next((x for x in v.values() if isinstance(x, list)), list(v.values()))
    return v


COND = re.compile(r"^([A-Za-z0-9_.]+)(!=|=|>|<|~)(.*)$")


def field(row: dict, name: str):
    v = row
    for part in name.split("."):
        v = v.get(part) if isinstance(v, dict) else None
    return v


def match_one(v, op: str, want: str) -> bool:
    if isinstance(v, list):
        return any(match_one(x, op, want) for x in v) if op != "!=" else all(match_one(x, op, want) for x in v)
    s = "" if v is None else (json.dumps(v, ensure_ascii=False) if isinstance(v, (dict, bool)) else str(v))
    if op == "=":
        return s == want
    if op == "!=":
        return s != want
    if op == "~":
        return want in s
    try:
        return float(s) > float(want) if op == ">" else float(s) < float(want)
    except ValueError:
        return False


def where(rows: list[dict], conds: list[str]) -> list[dict]:
    parsed = []
    for c in conds:
        m = COND.match(c)
        if not m:
            raise SystemExit(f"bad condition {c!r}")
        parsed.append(m.groups())
    return [r for r in rows if all(match_one(field(r, f), op, want) for f, op, want in parsed)]


def short(row: dict, width: int = 300) -> str:
    return json.dumps(row, ensure_ascii=False)[:width]


def main(argv: list[str]) -> int:
    P.utf8_stdout()
    limit = 5
    if "--limit" in argv:
        i = argv.index("--limit")
        limit = int(argv[i + 1])
        argv = argv[:i] + argv[i + 2:]
    key = "Id"
    if "--key" in argv:
        i = argv.index("--key")
        key = argv[i + 1]
        argv = argv[:i] + argv[i + 2:]
    if len(argv) < 2:
        print(__doc__)
        return 2
    cmd, table, rest = argv[0], argv[1], argv[2:]
    if cmd == "text":
        db = sqlite3.connect(f"file:{LANG}?mode=ro", uri=True)
        for k in [table] + rest:
            hit = db.execute("select Content from MultiText where Id = ?", (k,)).fetchone()
            print(f"{k}: {hit[0] if hit else '(none)'}")
        return 0
    rows = load(table)
    out = P.out_file("data.json")
    if cmd == "fields":
        names = collections.Counter(k for r in rows for k in r)
        print(f"{table}: {len(rows)} rows; fields: " + ", ".join(sorted(names)))
        print("first: " + short(rows[0]) if rows else "")
        return 0
    if cmd == "row":
        hits = [r for r in rows if str(r.get(key)) in rest]
    elif cmd == "where":
        hits = where(rows, rest)
    elif cmd == "count":
        c = collections.Counter(json.dumps(field(r, rest[0]), ensure_ascii=False) for r in rows)
        print(f"{table}.{rest[0]}: {len(c)} values over {len(rows)} rows")
        for v, n in sorted(c.items(), key=lambda x: (-x[1], x[0]))[:30]:
            print(f"{n:6}  {v[:80]}")
        return 0
    else:
        print(__doc__)
        return 2
    out.write_text(json.dumps(hits, ensure_ascii=False, indent=1), encoding="utf-8")
    print(f"{len(hits)} of {len(rows)} rows → {out}")
    for r in hits[:limit]:
        print(short(r))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
