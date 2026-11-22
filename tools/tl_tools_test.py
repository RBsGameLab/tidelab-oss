#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Tests for the R-161 helper scripts (tools/tl_*.py): run `python -I tools/tl_tools_test.py`."""

from __future__ import annotations

import os
import re
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tl_audit  # noqa: E402
import tl_paths  # noqa: E402
import tl_clientlog  # noqa: E402
import tl_clientref  # noqa: E402
import tl_data  # noqa: E402
import tl_testserver  # noqa: E402

try:
    import tl_ship  # noqa: E402
except ImportError:
    tl_ship = None  # excluded from public export

try:
    from tl_publish_test import Publish  # noqa: E402, F401
except ImportError:
    pass  # excluded from public export


class ClientLog(unittest.TestCase):
    LINES = [
        "[2026.11.21-15.20.00:000][  1]LogInit: Display: start",
        "[2026.11.21-15.21.07:876][499][GameThread]Sharphereal: Error: [C#][6084][Error][Gacha] 抽卡贴图信息表没有配置 [itemId: 21040053]",
        "   at GachaConfig.GetGachaTextureInfo(Int32 itemId)",
        "[2026.11.21-15.21.07:887][499][GameThread]Sharphereal: Error: [C#][6086][Error][Gacha] 抽卡贴图信息表没有配置 [itemId: 21020053]",
        "[2026.11.21-15.21.08:586][542][TaskGraphThreadNP 4]LogWwiseFileHandler: Error: noise",
        "[2026.11.21-15.21.09:000][543][GameThread]Sharphereal: Error: [4548][E][Launcher] UnobservedException",
    ]

    def test_errors_group_without_numbers_and_skip_noise(self):
        rows = tl_clientlog.triage(self.LINES, None, None, 10)
        self.assertEqual([(c, first) for c, first, _ in rows], [(2, 2), (1, 6)])

    def test_minutes_keep_only_the_tail(self):
        rows = tl_clientlog.triage(self.LINES, 1, None, 10)
        self.assertEqual(sum(c for c, _, _ in rows), 3)
        self.assertEqual(tl_clientlog.triage(self.LINES[:2], 1, re.compile("start"), 10), [])
        self.assertEqual(tl_clientlog.triage(self.LINES[:2], 2, re.compile("start"), 10)[0][1], 1)


class Data(unittest.TestCase):
    ROWS = [{"Id": 1, "Type": 2, "Show": [1404, 1602]}, {"Id": 2, "Type": 3, "Show": []}, {"Id": 3, "Type": 2, "Name": "琳奈"}]

    def test_conditions(self):
        ids = lambda conds: [r["Id"] for r in tl_data.where(self.ROWS, conds)]
        self.assertEqual(ids(["Type=2"]), [1, 3])
        self.assertEqual(ids(["Type=2", "Id>1"]), [3])
        self.assertEqual(ids(["Show=1602"]), [1])
        self.assertEqual(ids(["Name~琳"]), [3])
        self.assertEqual(ids(["Type!=2"]), [2])
        with self.assertRaises(SystemExit):
            tl_data.where(self.ROWS, ["bad"])


class ClientRef(unittest.TestCase):
    def test_patterns_find_what_they_say(self):
        senders = re.compile(tl_clientref.pattern("senders", "GachaInfoRequest"))
        self.assertTrue(senders.search("GachaInfoRequest message = Aki.Protocol.GachaInfoRequest.Create();"))
        self.assertFalse(senders.search("public void TryGachaInfoRequest(Action<bool>? callback = null)"))
        reads = re.compile(tl_clientref.pattern("reads", "ShowStageImage"))
        self.assertTrue(reads.search("x = item.ShowStageImage;"))
        handlers = re.compile(tl_clientref.pattern("handlers", "TowerFloorUpdateNotify"))
        self.assertTrue(handlers.search("Net.Instance.Register(ENotifyMessageId.TowerFloorUpdateNotify, OnUpdate);"))


@unittest.skipIf(tl_ship is None, "tl_ship is private-repo only")
class Ship(unittest.TestCase):
    def test_rows_merge_by_id(self):
        base = ["| R-158 | costs | todo |", "| R-160 | panel | todo |", "| R-94 | perf | todo |"]
        ours = ["| R-158 | costs | todo |", "| R-160 | panel | a, b merged |", "| R-94 | perf | todo |"]
        theirs = ["| R-158 | costs | a–d merged |", "| R-160 | panel | todo |", "| R-161 | skills | doing |", "| R-94 | perf | todo |"]
        self.assertEqual(tl_ship.merge_rows(base, ours, theirs), [
            "| R-158 | costs | a–d merged |", "| R-160 | panel | a, b merged |", "| R-161 | skills | doing |", "| R-94 | perf | todo |"])

    def test_a_row_deleted_on_one_side_stays_deleted(self):
        base = ["| R-1 | a | x |", "| R-2 | b | x |"]
        self.assertEqual(tl_ship.merge_rows(base, ["| R-2 | b | x |"], base), ["| R-2 | b | x |"])
        self.assertEqual(tl_ship.merge_rows(base, base, ["| R-1 | a | x |"]), ["| R-1 | a | x |"])

    def test_resolve_index_only_touches_index_conflicts(self):
        base = "| R-158 | c | todo |\n| R-160 | p | todo |\n"
        text = ("top\n<<<<<<< HEAD\n| R-158 | c | todo |\n| R-160 | p | b merged |\n=======\n"
                "| R-158 | c | done |\n| R-160 | p | todo |\n>>>>>>> main\nend\n")
        self.assertEqual(tl_ship.resolve_index(text, base), "top\n| R-158 | c | done |\n| R-160 | p | b merged |\nend\n")
        prose = "<<<<<<< HEAD\nsome text\n=======\nother text\n>>>>>>> main\n"
        self.assertIsNone(tl_ship.resolve_index(prose, base))


class TestServer(unittest.TestCase):
    def test_never_the_live_ports_or_state(self):
        args = " ".join(tl_testserver.gamesv_args())
        self.assertIn("127.0.0.1:13199", args)
        self.assertIn("127.0.0.1:13181", args)
        self.assertNotIn("13100", args)
        self.assertNotIn(str(tl_paths.LIVE_STATE), args)

    def test_api_paths_survive_git_bash(self):
        for p in ("/api/gacha", "api/gacha", "C:/Program Files/Git/api/gacha"):
            self.assertEqual(tl_testserver.api_path(p), "/api/gacha")

    def test_summary_shows_the_shape(self):
        self.assertEqual(tl_testserver.summarize({"slots": [1, 2], "now": 5, "m": {"a": 1}}), "slots[2] now=5 m{1}")



class Audit(unittest.TestCase):
    E = "[GameThread]Sharphereal: Display: [C#][1][Info][CombatInfo][WCL][1] [{kind}][EntityId:95232813001:Monster:BP_MB1FuludelisiMd00401_C_2147401961] {msg}"

    def line(self, t: str, kind: str, msg: str) -> str:
        return f"[2026.11.21-{t}][1]" + self.E.format(kind=kind, msg=msg)

    def boss(self) -> list[str]:
        hit = "切换本地主状态成功，[当前状态(22):普通受击(2|0)]"
        skill = "切换本地主状态成功，[当前状态(164):普通技能(1|0)]"
        fail = "技能释放失败，状态不满足 [技能Id: 80025012][技能名: 诸武精通·风]"
        return [
            "[2026.11.21-16.43.44:416][  0]LogInit: Display: start",
            self.line("16.48.22:322", "FightState", hit),
            self.line("16.48.22:417", "Skill", fail),
            self.line("16.48.40:000", "FightState", hit.replace("22", "40")),
            self.line("16.49.04:227", "FightState", skill),       # the R-164 freeze: 42 s
            self.line("16.49.10:000", "FightState", hit),
            self.line("16.49.13:000", "FightState", skill),       # a normal 3 s stagger
            "[2026.11.21-16.49.20:000][5][GameThread]Sharphereal: Error: [C#][9][Error][CommonDbConnect][XXJ][802] 配置表中没有该数据，请确认该问题，或修改为合理的查询！ [Id: 60][handleId: 342]",
            "[2026.11.21-16.49.21:000][5][GameThread]Sharphereal: Error: [C#][9][Error][CommonDbConnect][XXJ][803] 配置表中没有该数据，请确认该问题，或修改为合理的查询！ [Id: 80][handleId: 342]",
            "[2026.11.21-16.49.22:000][5][RenderThread 1]LogRHI: Error: Failed to find prepared GraphicsPipelineState",
            "[2026.11.21-16.49.23:000][5][GameThread]Sharphereal: Error: [3916][E][Launcher][16.49.23:001] UnobservedException",
            "System.NullReferenceException: Object reference not set to an instance of an object",
            "   at Foo.Bar()",
            "[2026.11.21-16.54.08:383][9]LogExit: Display: bye",
        ]

    def test_stuck_monster_spans_over_the_limits_only(self):
        c = tl_audit.read_client(self.boss())
        self.assertEqual([(x.kind, round(x.seconds)) for x in c.stuck], [("hit", 42), ("skill", 42)])
        self.assertEqual(c.stuck[0].name, "MB1FuludelisiMd00401")
        self.assertFalse(c.stuck[0].open_end)
        self.assertEqual(c.stuck[0].start.strftime("%H:%M:%S"), "16:48:22")

    def test_a_hit_state_until_the_last_line_is_open(self):
        lines = self.boss()[:4]  # never leaves 受击
        c = tl_audit.read_client(lines, hit_s=10)
        self.assertEqual([(x.kind, x.open_end) for x in c.stuck], [("hit", True)])

    def test_client_errors_group_by_system_and_keep_engine_apart(self):
        c = tl_audit.read_client(self.boss())
        db = c.game_errors[("CommonDbConnect", "配置表中没有该数据，请确认该问题，或修改为合理的查询！ [Id: #][handleId: #]")]
        self.assertEqual((db.count, db.samples), (2, ["60", "80"]))
        self.assertIn(("Launcher", "UnobservedException"), c.game_errors)
        self.assertTrue(any(k[0] == "Exception" for k in c.game_errors))
        self.assertEqual([k[0] for k in c.engine_errors], ["LogRHI"])
        self.assertEqual((c.start.strftime("%H:%M"), c.end.strftime("%H:%M")), ("16:43", "16:54"))

    def test_server_lines_in_the_window_counted_once(self):
        lines = [
            "2026-11-21 16:40:00.000 warning(connection): no handler for OldRequest (msg_id: 1), replied empty",  # before
            "2026-11-21 16:45:00.000 info(combat): unhandled combat message: FragileChangeRequest",
            "2026-11-21 16:45:00.001 info(combat): unhandled combat message: ActivateBuffPush",
            "2026-11-21 16:45:01.000 warning(connection): no handler for ShopInfoRequest (msg_id: 2), replied empty payload: 0a",
            "2026-11-21 16:45:02.000 warning(connection): no handler for SomePush (msg_id: 3)",
            "2026-11-21 16:45:03.000 warning(reply): TowerRequest answered ErrorCode_ErrTowerNotOpen (123)",
            "2026-11-21 16:45:04.000 warning(interact_actions): 1001: drop package 77 not found",
            "2026-11-21 16:45:05.000 info(quest): fine",
        ]
        from datetime import datetime
        s = tl_audit.read_server([("gamesv_prev.log", lines), ("gamesv.log", lines), ("old.log", ["info(init): x"])],
                                 datetime(2026, 11, 21, 16, 43, 44), datetime(2026, 11, 21, 16, 54, 8))
        self.assertEqual(s.combat_unanswered, {"FragileChangeRequest": 1})
        self.assertEqual(s.combat_dropped, {"ActivateBuffPush": 1})
        self.assertEqual(s.empty_replies, {"ShopInfoRequest": 1})
        self.assertEqual(s.dropped_pushes, {"SomePush": 1})
        self.assertEqual(s.error_replies, {("TowerRequest", "ErrorCode_ErrTowerNotOpen"): 1})
        self.assertEqual([(k, g.count, g.samples) for k, g in s.warnings.items()],
                         [(("interact_actions", "#: drop package # not found"), 1, ["1001,77"])])
        self.assertEqual((s.lines, s.files, s.unstamped_files), (7, ["gamesv_prev.log"], ["old.log"]))

    def test_report_marks_severe_red_and_drops_ignored_groups(self):
        import re as _re
        from datetime import datetime
        from pathlib import Path
        c = tl_audit.read_client(self.boss())
        s = tl_audit.read_server([("gamesv.log", [
            "2026-11-21 16:45:00.000 info(combat): unhandled combat message: FragileChangeRequest",
            "2026-11-21 16:45:01.000 warning(connection): no handler for ShopInfoRequest (msg_id: 2), replied empty",
        ])], c.start, c.end)
        r = tl_audit.build_report(c, s, {"ShopInfoRequest": "Shop"}, [_re.compile("LogRHI")], Path("x.md"))
        red = [t for is_red, t in r.summary if is_red]
        self.assertTrue(red[0].startswith("战斗请求没回 1 种：FragileChangeRequest 1次"), red)
        self.assertTrue(red[1].startswith("怪卡住 1 次（1 只）：16:48:22 MB1FuludelisiMd00401 受击 42 秒"), red)
        self.assertTrue(any("商店 1种1次" in t for _, t in r.summary), r.summary)
        self.assertNotIn("LogRHI", r.markdown)
        self.assertIn("ShopInfoRequest ×1", r.markdown)

    def test_system_of_a_client_file(self):
        self.assertEqual(tl_audit.system_of_file(r".\PayShopController.cs"), "PayShop")
        self.assertEqual(tl_audit.system_of_file(r".\CSharpScript.Game.Module.Activity\X.cs"), "Activity")
        self.assertEqual(tl_audit.system_of_file(r".\CSharpScript.Game.NewWorld.SceneItem\FooComponent.cs"), "Foo")
        self.assertEqual(tl_audit.zh("Gacha"), "抽卡")


if __name__ == "__main__":
    unittest.main()
