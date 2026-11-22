#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Tests for tools/setup_data.py and tools/parts_models.py (R-174): python -I tools/setup_data_test.py"""
from __future__ import annotations

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import parts_models  # noqa: E402
import setup_data as sd  # noqa: E402


class Reports(unittest.TestCase):
    def test_export_report_reads_the_done_line(self):
        self.assertEqual(sd.export_report("Config/*.ini\t120\r\ndone\t14735 files, 0 failed\r\n"), (14735, 0))
        self.assertEqual(sd.export_report("done\t10 files, 2 failed"), (10, 2))
        self.assertIsNone(sd.export_report("Config/*.ini\t120\r\n"))

    def test_parts_report(self):
        self.assertEqual(sd.parts_report("done: 771/771 classes, 85 with parts, 0 failed, 122 models\r\nx"), (0, 122))
        self.assertIsNone(sd.parts_report("loading 10/771"))
        self.assertIsNone(sd.parts_report(""))

    def test_tags_report(self):
        text = "GetTagIdByName in Aki.Core: Int32 GetTagIdByName(System.String)\r\nasked 94: 77 remapped, 17 same id, 0 unknown (0)\r\n"
        self.assertEqual(sd.tags_report(text), (77, 0))
        self.assertIsNone(sd.tags_report("error: x"))


class Plan(unittest.TestCase):
    def setUp(self):
        self.done = {s.name: True for s in sd.STAGES}
        self.saved = [(s, s.done) for s in sd.STAGES]
        for s in sd.STAGES:
            s.done = (lambda n: lambda p: self.done[n])(s.name)

    def tearDown(self):
        for s, d in self.saved:
            s.done = d

    def runs(self, start=None, only=None):
        return [s.name for s, go in sd.plan(None, start, only) if go]

    def test_all_done_runs_nothing(self):
        self.assertEqual(self.runs(), [])

    def test_a_missing_output_redoes_that_stage_and_the_later_ones(self):
        self.done["refs"] = False
        self.assertEqual(self.runs(), ["refs", "generate", "extra", "paks", "ingame"])

    def test_from_and_only(self):
        self.assertEqual(self.runs(start="paks"), ["paks", "ingame"])
        self.assertEqual(self.runs(only="extra"), ["extra"])
        with self.assertRaises(sd.Fail):
            self.runs(only="nope")


class Files(unittest.TestCase):
    def test_parts_models_skips_items_roles_vehicles_weapons_and_empty(self):
        rows = [{"Id": 1, "ActorClassPath": "/Game/Aki/Character/Monster/A/BP_A.BP_A_C"},
                {"Id": 2, "ActorClassPath": "/Game/Aki/Character/Role/FemaleM/Y/BP_Y.BP_Y_C"},
                {"Id": 3, "ActorClassPath": "/Game/Aki/Character/Item/I.I_C"},
                {"Id": 4, "ActorClassPath": ""},
                {"Id": 5},
                {"Id": 6, "ActorClassPath": "/Game/Aki/GamePlay/G.G_C"}]
        self.assertEqual(parts_models.models(rows), [(1, rows[0]["ActorClassPath"]), (6, rows[5]["ActorClassPath"])])

    def test_run2_beta_adds_empty_tables_and_keeps_real_ones(self):
        with tempfile.TemporaryDirectory() as d:
            p = sd.Paths(d, d)
            os.makedirs(p.beta)
            with open(os.path.join(p.beta, "GameplayTagRemap.json"), "w") as f:
                f.write('[{"RawId":1}]')
            into = os.path.join(d, "tmp")
            sd.run2_beta(p, into)
            with open(os.path.join(into, "CharacterPartConfig.json")) as f:
                self.assertEqual(f.read(), "[]")
            with open(os.path.join(into, "GameplayTagRemap.json")) as f:
                self.assertEqual(f.read(), '[{"RawId":1}]')


class Protoc(unittest.TestCase):
    def test_found_in_the_nuget_cache_or_not(self):
        with tempfile.TemporaryDirectory() as home, tempfile.TemporaryDirectory() as empty:
            self.assertFalse(sd.protoc_found(empty, home))
            d = os.path.join(home, ".nuget", "packages", "grpc.tools", "2.85.0", "tools", "windows_x64")
            os.makedirs(d)
            open(os.path.join(d, "protoc.exe"), "w").close()
            self.assertTrue(sd.protoc_found(empty, home))


if __name__ == "__main__":
    unittest.main()
