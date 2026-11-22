#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-166 one-click audit after a play session: what the session touched that tidelab has not
finished, all systems. Reads the last client log (Client.log, decoded) and the server logs of
the same time span (gamesv.log, gamesv_prev.log, archive/gamesv-*.log; lines stamped since R-166).

    python -I tools/tl_audit.py [--client-log PATH] [--logs DIR] [--out DIR]   (audit.cmd)
    python -I tools/tl_audit.py combat-calls      refresh specs/combat-client-calls.tsv

Prints a short Chinese summary (red = severe) and writes the full English report to
out/audit/<client log end>.md. Groups matching a line of tools/tl_audit_ignore.txt (regex) are
left out. Checks (spec: specs/audit.md):
  1. combat requests the server left unanswered (`unhandled combat message: XRequest`)  [red]
  2. requests answered empty and pushes dropped (`no handler for …`), by game system
  3. replies that are not Success (`warning(reply): X answered CODE (N)`)
  4. other server warnings/errors, by scope
  5. client errors, by system; exceptions red, engine (non-C#) errors apart
  6. stuck monsters: > STUCK_HIT_S in a hit main state, or skills failing for > STUCK_SKILL_S [red]
"""

from __future__ import annotations

import argparse
import collections
import os
import re
import subprocess
import sys
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_clientlog  # noqa: E402
import tl_paths as P  # noqa: E402

STUCK_HIT_S = 10.0
STUCK_SKILL_S = 15.0
# The server log of a session may start a little before the client's first line (login) and end after.
MARGIN = timedelta(minutes=1)
IGNORE_FILE = P.REPO / "tools" / "tl_audit_ignore.txt"
COMBAT_CALLS = P.REPO / "specs" / "combat-client-calls.tsv"
SENDERS_CACHE = "audit_senders.tsv"

# Game systems by client sender (controller file stem without Controller/NetHandler/Component, or
# module folder) and by client log tag. Unlisted ones print in English.
SYSTEM_ZH = {
    "Combat": "战斗", "CombatInfo": "战斗", "CombatMessage": "战斗", "Skill": "战斗", "Buff": "战斗",
    "CharacterBuff": "战斗", "BaseBuff": "战斗", "PlayerBuff": "战斗", "SceneTeam": "编队", "Formation": "编队",
    "FormationNet": "编队", "Role": "角色", "Inventory": "背包", "Item": "背包", "Mail": "邮件", "MailBind": "邮件",
    "Friend": "好友", "Online": "联机", "Chat": "聊天", "Personal": "个人信息", "Achievement": "成就",
    "Activity": "活动", "ActivityRegress": "回归活动", "AdventureGuide": "冒险手册", "BattlePass": "战令",
    "PayShop": "商城", "Shop": "商店", "Gacha": "抽卡", "Tower": "深塔", "TowerDefense": "塔防",
    "RoleTower": "角色塔", "WheelTower": "轮转塔", "BabelTower": "巴别塔", "ShipTower": "船塔",
    "Quest": "任务", "QuestNew": "任务", "QuestMultiLine": "任务", "RecallQuest": "回忆任务", "Plot": "剧情",
    "LevelPlay": "玩法", "LevelGamePlay": "关卡玩法", "GeneralLogicTree": "行为树", "InstanceDungeon": "副本",
    "Map": "地图", "WorldMap": "地图", "MapExploreTool": "探索工具", "ExploreProgress": "探索度", "Area": "区域",
    "Teleport": "传送", "Weather": "天气", "DeadRevive": "复活", "Calabash": "声骸收集", "PhantomBattle": "声骸",
    "VisionEquipGroup": "声骸套装", "HandBook": "图鉴", "Manufacture": "合成", "Cook": "烹饪",
    "Infrastructure": "基建", "Creature": "生物", "Login": "登录", "KuroSdk": "SDK", 
    "Advice": "留言", "PhoneMsg": "短信", "Roguelike": "肉鸽", "WeeklyRogue": "周常肉鸽", "PermanentRogue": "常驻肉鸽",
    "MapRogue": "地图肉鸽", "RogueBattle": "肉鸽战斗", "SurvivorsRogue": "幸存者肉鸽", "BossRush": "Boss挑战",
    "MonsterGroup": "怪物组", "Skin": "皮肤", "FlySkin": "滑翔皮肤", "GameMode": "游戏模式", "SceneItem": "场景物件",
    "Vehicle": "载具", "Motorcycle": "摩托", "MotorcycleDiy": "摩托改装", "MotorcycleDevelop": "摩托研发",
    "CommonDbConnect": "配置表", "Resource": "资源", "UiImageSetting": "界面图片", "Audio": "音频",
    "StateMachineNew": "怪物AI", "FightState": "战斗状态", "Launcher": "启动", "Net": "网络", "World": "世界",
    "Guide": "引导", "Functional": "功能开放", "Reward": "奖励", "ResManager": "资源", "LevelLoading": "加载",
}
SYSTEM_SUFFIXES = ("Controller", "NetHandler", "Component", "Manager", "Model")

SERVER_LINE = re.compile(r"^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3}) (info|warning|error)\(([^)]*)\): (.*)$")
UNHANDLED_COMBAT = re.compile(r"^unhandled combat message: (\w+)")
NO_HANDLER = re.compile(r"^no handler for (\w+) \(msg_id: -?\d+\)(, replied empty)?")
ERROR_REPLY = re.compile(r"^(\w+) answered (\w+) \((-?\d+)\)")

CLIENT_STAMP = tl_clientlog.STAMP
CSHARP_TAG = re.compile(r"\[C#\]\[\d+\]\[\w+\]\[(\w+)\]")
ENGINE_TAG = re.compile(r"\]\s*(\w+): (?:Error|Warning):")
ENTITY = re.compile(r"\[EntityId:\d+:(\w+):(\w+?)(?:_C_\d+)?\]")
MONSTER_LINE = re.compile(r"\[(FightState|Skill)\]\[EntityId:(\d+):Monster:(\w+?)(?:_C_\d+)?\] (.*)$")
MAIN_STATE = re.compile(r"切换本地主状态成功，\[当前状态\(\d+\):([^(\]]+)")
SEVERE = re.compile(r"Exception|Callback执行异常|怪物状态出问题|等待实体超时")


def zh(system: str) -> str:
    return SYSTEM_ZH.get(system, system)


def system_of_file(path: str) -> str:
    """The game system of a client source file: its module folder, else its class name."""
    parts = re.split(r"[\\/]", path.lstrip(".\\/"))
    if len(parts) > 1:
        m = re.match(r"(?:CSharpScript|Aki)\.Game\.(?:Module\.|Manager\.|World\.|Common\.)?(\w+)", parts[0])
        if m and m.group(1) not in ("NewWorld", "AI"):
            return m.group(1)
    stem = re.sub(r"\.cs$", "", parts[-1])
    for s in SYSTEM_SUFFIXES:
        if stem.endswith(s) and len(stem) > len(s):
            return stem[: -len(s)]
    return stem


def normalize(text: str) -> str:
    """A message without numbers and entity names: lines that differ only there group."""
    text = ENTITY.sub(r"[\1]", text)
    return re.sub(r"\d+", "#", text).strip()[:200]


def numbers_of(text: str) -> list[str]:
    return re.findall(r"(?<![\w.])\d+(?![\w.])", ENTITY.sub("", text))


FIELD = re.compile(r"\[([^\[\]:：]{1,24})[:：]\s*([^\[\]]{1,60})\]")
FIELD_SKIP = {"handleId", "TimeCount", "DebugInfo", "传入语言", "查询语言"}


def sample_of(text: str) -> str:
    """What tells two lines of a group apart: its first [name: value] field with a number (not a
    handle), else its entity, else its last numbers."""
    for name, value in FIELD.findall(text):
        if name.strip() not in FIELD_SKIP and re.search(r"\d", value):
            return value.strip()
    ents = ENTITY.findall(text)
    if ents:
        return ents[0][1]
    return ",".join(numbers_of(text)[-2:])


# ---------------------------------------------------------------- client log

@dataclass
class Group:
    count: int = 0
    first: str = ""
    samples: list[str] = field(default_factory=list)  # distinct numbers/entities seen, a few

    def add(self, line: str, sample: str) -> None:
        if not self.count:
            self.first = line
        self.count += 1
        if sample and sample not in self.samples and len(self.samples) < 6:
            self.samples.append(sample)


@dataclass
class Stuck:
    entity: str
    name: str
    kind: str  # "hit" or "skill"
    start: datetime
    end: datetime
    open_end: bool  # the span ran to the monster's last line (died or the log ended)

    @property
    def seconds(self) -> float:
        return (self.end - self.start).total_seconds()


@dataclass
class ClientFacts:
    start: datetime | None = None
    end: datetime | None = None
    lines: int = 0
    game_errors: dict[tuple[str, str], Group] = field(default_factory=dict)    # (system, key)
    engine_errors: dict[tuple[str, str], Group] = field(default_factory=dict)
    stuck: list[Stuck] = field(default_factory=list)


def client_stamp(line: str) -> datetime | None:
    return tl_clientlog.stamp_of(line)


def find_stuck(events: list[tuple[datetime, str, str, str]], hit_s: float, skill_s: float) -> list[Stuck]:
    """events: (time, entity id, monster name, message) of monster FightState/Skill lines in order.
    Hit span: main state with 受击 (not a 技能 state) until a main state without it.
    Skill span: first 技能释放失败 until the next successful main state with 技能."""
    per: dict[str, dict] = {}
    spans: list[Stuck] = []
    for t, ent, name, msg in events:
        s = per.setdefault(ent, {"name": name, "hit": None, "fail": None, "last_fail": None, "seen": t})
        s["seen"] = t
        m = MAIN_STATE.search(msg)
        if m:
            state = m.group(1)
            is_skill = "技能" in state
            if "受击" in state and not is_skill:
                if s["hit"] is None:
                    s["hit"] = t
            elif s["hit"] is not None:
                spans.append(Stuck(ent, name, "hit", s["hit"], t, False))
                s["hit"] = None
            if is_skill and s["fail"] is not None:
                spans.append(Stuck(ent, name, "skill", s["fail"], t, False))
                s["fail"] = None
        elif "技能释放失败" in msg:
            if s["fail"] is None:
                s["fail"] = t
            s["last_fail"] = t
    for ent, s in per.items():
        if s["hit"] is not None:
            spans.append(Stuck(ent, s["name"], "hit", s["hit"], s["seen"], True))
        if s["fail"] is not None:
            spans.append(Stuck(ent, s["name"], "skill", s["fail"], s["last_fail"], True))
    limit = {"hit": hit_s, "skill": skill_s}
    return sorted((x for x in spans if x.seconds > limit[x.kind]), key=lambda x: x.start)


def read_client(lines: list[str], hit_s: float = STUCK_HIT_S, skill_s: float = STUCK_SKILL_S) -> ClientFacts:
    f = ClientFacts(lines=len(lines))
    now = None
    events = []
    for line in lines:
        t = client_stamp(line)
        if t:
            now = t
            if f.start is None:
                f.start = t
            f.end = t
        mm = MONSTER_LINE.search(line)
        if mm and now:
            events.append((now, mm.group(2), mm.group(3).removeprefix("BP_"), mm.group(4)))
        if line.lstrip().startswith("at ") or not tl_clientlog.ERROR.search(line) or tl_clientlog.NOISE.search(line):
            continue
        body = CLIENT_STAMP.sub("", line)
        body = re.sub(r"^\[\s*\d+\]\[[^\]]*\]", "", body)
        body = re.sub(r"^Sharphereal: \w+: ", "", body)
        body = re.sub(r"^\[\d+\]\[E\]\[\w+\]\[[\d.:]+\]\s*", "", body)  # launcher: [seq][E][Launcher][time]
        tag = CSHARP_TAG.search(line)
        if tag:
            msg = re.sub(r"^\[\w+\]\[\d+\]\s*", "", line[tag.end():])
            sample = sample_of(msg)
            key = normalize(msg)
            f.game_errors.setdefault((tag.group(1), key), Group()).add(line.rstrip(), sample)
        else:
            sample = sample_of(body)
            e = ENGINE_TAG.search(line)
            system = e.group(1) if e else ("Exception" if "Exception" in line else "other")
            if system in ("Sharphereal",):  # C# lines without the [C#] tag: launcher, exceptions
                m = re.search(r"\[E\]\[(\w+)\]", line)
                system = m.group(1) if m else "C#"
                f.game_errors.setdefault((system, normalize(body)), Group()).add(line.rstrip(), sample)
                continue
            if system == "Exception" or "Exception" in body[:80]:
                f.game_errors.setdefault(("Exception", normalize(body)), Group()).add(line.rstrip(), sample)
                continue
            f.engine_errors.setdefault((system, normalize(body)), Group()).add(line.rstrip(), sample)
    f.stuck = find_stuck(events, hit_s, skill_s)
    return f


# ---------------------------------------------------------------- server logs

@dataclass
class ServerFacts:
    files: list[str] = field(default_factory=list)
    lines: int = 0                 # stamped lines inside the window
    unstamped_files: list[str] = field(default_factory=list)
    combat_unanswered: collections.Counter = field(default_factory=collections.Counter)
    combat_dropped: collections.Counter = field(default_factory=collections.Counter)
    empty_replies: collections.Counter = field(default_factory=collections.Counter)
    dropped_pushes: collections.Counter = field(default_factory=collections.Counter)
    error_replies: collections.Counter = field(default_factory=collections.Counter)  # (request, code)
    warnings: dict[tuple[str, str], Group] = field(default_factory=dict)            # (scope, key)


def server_logs(logs: Path) -> list[Path]:
    found = [logs / "gamesv.log", logs / "gamesv_prev.log", *sorted((logs / "archive").glob("gamesv-*.log"))]
    return [p for p in found if p.is_file()]


def read_server(files: list[tuple[str, list[str]]], start: datetime | None, end: datetime | None) -> ServerFacts:
    """files: (name, lines). Only stamped lines within [start - MARGIN, end + MARGIN] count;
    a line seen in two files (gamesv_prev.log is a copy) counts once."""
    f = ServerFacts()
    lo = start - MARGIN if start else None
    hi = end + MARGIN if end else None
    seen: set[str] = set()
    for name, lines in files:
        stamped = used = 0
        for line in lines:
            m = SERVER_LINE.match(line)
            if not m:
                continue
            stamped += 1
            t = datetime.strptime(m.group(1), "%Y-%m-%d %H:%M:%S.%f")
            if (lo and t < lo) or (hi and t > hi) or line in seen:
                continue
            seen.add(line)
            used += 1
            level, scope, msg = m.group(2), m.group(3), m.group(4)
            if u := UNHANDLED_COMBAT.match(msg):
                (f.combat_unanswered if u.group(1).endswith("Request") else f.combat_dropped)[u.group(1)] += 1
            elif n := NO_HANDLER.match(msg):
                (f.empty_replies if n.group(2) else f.dropped_pushes)[n.group(1)] += 1
            elif scope == "reply" and (r := ERROR_REPLY.match(msg)):
                f.error_replies[(r.group(1), r.group(2))] += 1
            elif level != "info":
                key = normalize(re.sub(r"\s*payload: .*$", "", msg))
                f.warnings.setdefault((scope, key), Group()).add(line, sample_of(msg))
        if not stamped and lines:
            f.unstamped_files.append(name)
        if used:
            f.files.append(name)
        f.lines += used
    return f


# ---------------------------------------------------------------- client sources

def sender_systems(refresh: bool = False) -> dict[str, str]:
    """Request/push name → the game system of the client code that sends it (cached)."""
    cache = P.out_file(SENDERS_CACHE)
    if cache.exists() and not refresh:
        return dict(l.split("\t", 1) for l in cache.read_text(encoding="utf-8").splitlines() if "\t" in l)
    pat = r"Net\.Instance\??\.(?:Call|CallAsync|Send)\((?:ERequestMessageId|EPushMessageId)\.(\w+)"
    try:
        out = subprocess.run(["rg", "-o", "-N", "--no-heading", pat, "-r", "$1", "."], cwd=P.CLIENT_SRC / "Aki.Game",
                             capture_output=True, text=True, encoding="utf-8").stdout
    except OSError:
        return {}
    votes: dict[str, collections.Counter] = collections.defaultdict(collections.Counter)
    for line in out.splitlines():
        path, _, name = line.rpartition(":")
        if path:
            votes[name][system_of_file(path)] += 1
    found = {name: c.most_common(1)[0][0] for name, c in votes.items()}
    cache.write_text("".join(f"{k}\t{v}\n" for k, v in sorted(found.items())), encoding="utf-8")
    return found


def combat_calls() -> list[tuple[str, str]]:
    """(request, client file) the client sends with CombatNet.Instance.Call."""
    pat = r"CombatNet\.Instance\.Call\(ERequestMessageId\.(\w+)"
    out = subprocess.run(["rg", "-o", "-N", "--no-heading", pat, "-r", "$1", "."], cwd=P.CLIENT_SRC / "Aki.Game",
                         capture_output=True, text=True, encoding="utf-8").stdout
    calls: dict[str, str] = {}
    for line in sorted(out.splitlines()):
        path, _, name = line.rpartition(":")
        calls.setdefault(name, path.lstrip(".\\/").replace("\\", "/"))
    return sorted(calls.items())


def write_combat_calls() -> int:
    rows = combat_calls()
    head = ("# Combat requests the 3.8 client sends with CombatNet.Instance.Call and waits on (R-166).\n"
            "# Refresh: python -I tools/tl_audit.py combat-calls. Test: combat TestEveryCombatRequestTheClientWaitsOnIsAnswered.\n"
            "# request\tclient file\n")
    COMBAT_CALLS.write_text(head + "".join(f"{n}\t{p}\n" for n, p in rows), encoding="utf-8", newline="\n")
    print(f"{len(rows)} combat requests → {COMBAT_CALLS}")
    return 0


# ---------------------------------------------------------------- report

def ignore_patterns(path: Path = IGNORE_FILE) -> list[re.Pattern]:
    if not path.exists():
        return []
    return [re.compile(l.strip()) for l in path.read_text(encoding="utf-8").splitlines() if l.strip() and not l.startswith("#")]


def ignored(text: str, pats: list[re.Pattern]) -> bool:
    return any(p.search(text) for p in pats)


def by_system(counter: collections.Counter, systems: dict[str, str], default: str = "?") -> dict[str, list[tuple[str, int]]]:
    out: dict[str, list[tuple[str, int]]] = collections.defaultdict(list)
    for name, n in counter.most_common():
        out[systems.get(name, default)].append((name, n))
    return dict(sorted(out.items(), key=lambda kv: -sum(n for _, n in kv[1])))


@dataclass
class Report:
    summary: list[tuple[bool, str]]  # (red, line)
    markdown: str


def build_report(c: ClientFacts, s: ServerFacts, systems: dict[str, str], pats: list[re.Pattern], out_path: Path) -> Report:
    sm: list[tuple[bool, str]] = []
    md: list[str] = []
    span = f"{c.start:%Y-%m-%d %H:%M}–{c.end:%H:%M}" if c.start and c.end else "?"
    sm.append((False, f"tidelab 体检 {span}：客户端日志 {c.lines} 行，服务器日志 {s.lines} 行（{len(s.files)} 个文件）"))
    md += [f"# Audit {span}", "", f"- client log: {c.lines} lines, {c.start} → {c.end}",
           f"- server log lines in the span: {s.lines} from {', '.join(s.files) or 'none'}"]
    if not s.lines:
        why = f"; unstamped (before R-166): {', '.join(s.unstamped_files)}" if s.unstamped_files else ""
        sm.append((True, "没找到这局的服务器日志（R-166 之前的日志没有时间戳），服务器侧的检查是空的"))
        md.append(f"- **no server log of this session**{why}")
    md.append("")

    def keep(counter: collections.Counter) -> collections.Counter:
        return collections.Counter({k: v for k, v in counter.items() if not ignored(k, pats)})

    # 1. unanswered combat requests
    cu = keep(s.combat_unanswered)
    md += ["## 1. Combat requests left unanswered (client waits)", ""]
    md += [f"- {n} ×{k}" for n, k in cu.most_common()] or ["- none"]
    if cu:
        sm.append((True, f"战斗请求没回 {len(cu)} 种：" + "，".join(f"{n} {k}次" for n, k in cu.most_common(4))))
    # 6. stuck monsters (red, so up front in the summary)
    stuck = [x for x in c.stuck if not ignored(x.name, pats)]
    md += ["", f"## 2. Stuck monsters (hit state > {STUCK_HIT_S:g} s, skills failing > {STUCK_SKILL_S:g} s)", ""]
    for x in stuck:
        what = "in a hit state" if x.kind == "hit" else "skills failing (状态不满足)"
        tail = " (until its last line: died or log end)" if x.open_end else ""
        md.append(f"- {x.start:%H:%M:%S} {x.name} ({x.entity}): {what} {x.seconds:.0f} s{tail}")
    if not stuck:
        md.append("- none")
    else:
        # one line per stuck time: a hit span and the skill span beside it are the same freeze
        times: dict[tuple[str, int], Stuck] = {}
        for x in stuck:
            k = (x.entity, int(x.start.timestamp()) // 5)
            if k not in times or x.kind == "hit":
                times[k] = x
        monsters = len({x.entity for x in stuck})
        sm.append((True, f"怪卡住 {len(times)} 次（{monsters} 只）：" + "，".join(
            f"{x.start:%H:%M:%S} {x.name} {'受击' if x.kind == 'hit' else '放不出技能'} {x.seconds:.0f} 秒"
            for x in sorted(times.values(), key=lambda x: -x.seconds)[:3])))
    # 5a. client exceptions (red)
    game = {k: g for k, g in c.game_errors.items() if not ignored(k[1], pats) and not ignored(g.first, pats)}
    severe = {k: g for k, g in game.items() if SEVERE.search(g.first) or k[0] == "Exception"}
    if severe:
        sm.append((True, f"客户端严重报错 {len(severe)} 种 / {sum(g.count for g in severe.values())} 次：" + "；".join(
            f"{zh(sy)} {g.count}次 {short(key, 40)}" for (sy, key), g in sorted(severe.items(), key=lambda kv: -kv[1].count)[:3])))
    # 2. empty replies / dropped pushes by system
    er, dp = keep(s.empty_replies), keep(s.dropped_pushes) + keep(s.combat_dropped)
    combat_sys = {n: "Combat" for n in s.combat_dropped}
    md += ["", "## 3. Requests answered empty (not implemented), by system", ""]
    groups = by_system(er, systems)
    for system, items in groups.items():
        md.append(f"- **{system}** ({zh(system)}): " + ", ".join(f"{n} ×{k}" for n, k in items))
    if not groups:
        md.append("- none")
    else:
        sm.append((False, f"没做的请求 {len(er)} 种 / {sum(er.values())} 次，按系统：" + "；".join(
            f"{zh(sy)} {len(it)}种{sum(k for _, k in it)}次" for sy, it in list(groups.items())[:6])))
    md += ["", "## 4. Pushes the server dropped, by system", ""]
    groups = by_system(dp, {**systems, **combat_sys})
    for system, items in groups.items():
        md.append(f"- **{system}** ({zh(system)}): " + ", ".join(f"{n} ×{k}" for n, k in items))
    if not groups:
        md.append("- none")
    else:
        sm.append((False, f"服务器没处理的推送 {len(dp)} 种 / {sum(dp.values())} 次：" + "；".join(
            f"{zh(sy)} {len(it)}种{sum(k for _, k in it)}次" for sy, it in list(groups.items())[:4])))
    # 3. error replies
    ec = collections.Counter({k: v for k, v in s.error_replies.items() if not ignored(f"{k[0]} {k[1]}", pats)})
    md += ["", "## 5. Replies that are not Success", ""]
    md += [f"- {r} → {code} ×{n} ({zh(systems.get(r, '?'))})" for (r, code), n in ec.most_common()] or ["- none"]
    if ec:
        sm.append((False, f"服务器回了错误码 {len(ec)} 种：" + "，".join(f"{r}→{code.removeprefix('ErrorCode_')} {n}次" for (r, code), n in ec.most_common(3))))
    # 4. server warnings
    sw = {k: g for k, g in s.warnings.items() if not ignored(k[1], pats)}
    md += ["", "## 6. Other server warnings/errors, by scope", ""]
    for (scope, key), g in sorted(sw.items(), key=lambda kv: -kv[1].count):
        md.append(f"- [{scope}] ×{g.count}: {key}" + (f" (e.g. {', '.join(g.samples)})" if g.samples else ""))
    if not sw:
        md.append("- none")
    else:
        sm.append((False, f"服务器警告 {len(sw)} 种 / {sum(g.count for g in sw.values())} 次"))
    # 5. client errors
    md += ["", "## 7. Client errors (C#), by system; severe first", ""]
    for (system, key), g in sorted(game.items(), key=lambda kv: (kv[0] not in severe, -kv[1].count)):
        mark = "**!** " if (system, key) in severe else ""
        md.append(f"- {mark}{system} ({zh(system)}) ×{g.count}: {key}" + (f" (e.g. {', '.join(g.samples)})" if g.samples else ""))
    if not game:
        md.append("- none")
    eng = {k: g for k, g in c.engine_errors.items() if not ignored(k[1], pats)}
    md += ["", "## 8. Client engine errors (mostly noise)", ""]
    for (system, key), g in sorted(eng.items(), key=lambda kv: -kv[1].count):
        md.append(f"- {system} ×{g.count}: {key}")
    if not eng:
        md.append("- none")
    top = sorted(game.items(), key=lambda kv: -kv[1].count)[:3]
    sm.append((False, f"客户端报错 {len(game)} 种 / {sum(g.count for g in game.values())} 次，最多：" + "；".join(
        f"{zh(sy)} {g.count}次 {short(key)}" + (f"（{'/'.join(g.samples[:3])}）" if g.samples else "") for (sy, key), g in top) + f"（引擎报错 {len(eng)} 种另列，多为噪音）"))
    sm.append((False, f"明细：{out_path}"))
    md.append("")
    return Report(sm, "\n".join(md))


def short(key: str, n: int = 28) -> str:
    """A group key short enough for the summary: the message before its [fields]."""
    head = re.split(r"[，,！!。]|\s\[", key, maxsplit=1)[0]
    return head[:n]


def print_summary(lines: list[tuple[bool, str]]) -> None:
    if os.name == "nt":
        os.system("")  # turns on ANSI colours in the Windows console
    for red, text in lines:
        print(f"\x1b[31m✖ {text}\x1b[0m" if red else f"  {text}")


def audit(a: argparse.Namespace) -> int:
    lines = tl_clientlog.decode(a.client_log, P.out_file("audit_client_decoded.log"))
    if isinstance(lines, str):
        print(lines)
        return 1
    c = read_client(lines)
    files = [(p.name if p.parent == a.logs else f"archive/{p.name}", p.read_text(encoding="utf-8", errors="replace").splitlines())
             for p in server_logs(a.logs) if not c.start or datetime.fromtimestamp(p.stat().st_mtime) >= c.start - MARGIN]
    s = read_server(files, c.start, c.end)
    a.out.mkdir(parents=True, exist_ok=True)
    out_path = a.out / f"{(c.end or datetime.now()):%Y%m%d-%H%M%S}.md"
    r = build_report(c, s, sender_systems(), ignore_patterns(), out_path)
    out_path.write_text(r.markdown, encoding="utf-8")
    print_summary(r.summary)
    return 0


def main(argv: list[str]) -> int:
    P.utf8_stdout()
    if argv[:1] == ["combat-calls"]:
        return write_combat_calls()
    ap = argparse.ArgumentParser()
    ap.add_argument("--client-log", type=Path, default=P.CLIENT_LOG)
    ap.add_argument("--logs", type=Path, default=P.MAIN_WORKTREE / "out" / "logs")
    ap.add_argument("--out", type=Path, default=P.MAIN_WORKTREE / "out" / "audit")
    return audit(ap.parse_args(argv))


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
