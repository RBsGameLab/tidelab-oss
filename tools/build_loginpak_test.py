#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Tests for build_loginpak.py: run `python -I tools/build_loginpak_test.py`."""

from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_loginpak as lp  # noqa: E402


class IniTest(unittest.TestCase):
    def test_points_every_prefix_at_the_local_server_and_turns_the_sdk_off(self) -> None:
        text = "A=1\r\nInternalPrefix=https://x;https://y\r\nUseSDK=1\r\nCheckEntryTime=1\r\n"
        t = lp.patch(lp.INI_PUBLIC, text)
        self.assertIn("InternalPrefix=" + ";".join([lp.LOCAL] * 4) + "\r\n", t)
        self.assertIn("UseSDK=0\r\n", t)
        self.assertIn("CheckEntryTime=0\r\n", t)
        self.assertIn("A=1\r\n", t)

    def test_refuses_an_ini_without_the_keys(self) -> None:
        with self.assertRaises(ValueError):
            lp.patch(lp.INI_PUBLIC, "A=1\n")

    def test_official_ini_keeps_the_original_prefixes_and_url_path(self) -> None:
        text = "A=1\r\nInternalPrefix=https://x;https://y\r\nUrlPath=abc\r\nUseSDK=1\r\n"
        self.assertEqual(lp.official_ini(text), "InternalPrefix=https://x;https://y\r\nUrlPath=abc\r\n")
        with self.assertRaises(ValueError):
            lp.official_ini("InternalPrefix=https://x\n")

    def test_tpsafe_off(self) -> None:
        self.assertEqual(lp.patch(lp.INI_TPSAFE, "[x]\nEnable=True\n"), "[x]\nEnable=False\n")


class JsTest(unittest.TestCase):
    def test_sdk_off(self) -> None:
        self.assertEqual(lp.patch(lp.BASE_DEFINE, 'a;exports.USESDK="1";b'), 'a;exports.USESDK="0";b')
        self.assertEqual(lp.patch(lp.LAUNCHER_SDK, "x;const canUseSdk=()=>A.B&&C();y"),
                         "x;const canUseSdk=()=>!1;y")

    def test_http_prefixes_accepted(self) -> None:
        old = 'for(const l of r)!o&&l.startsWith("http://")||l.startsWith("https://")?a:b'
        self.assertEqual(lp.patch(lp.BASE_CONFIG, old),
                         'for(const l of r)l.startsWith("http://")||l.startsWith("https://")?a:b')

    def test_pak_check_does_nothing(self) -> None:
        t = "class P{static Eih(){if(a){b()}else{c()}}static Other(){}}"
        self.assertEqual(lp.patch(lp.PAK_MANAGER, t), "class P{static Eih(){}static Other(){}}")

    def test_login_view_tolerates_box_20(self) -> None:
        self.assertEqual(lp.patch(lp.LOGIN_DEBUG, "x(this.GetInputText(20).GetText())"),
                         "x(this.GetInputText(20)?.GetText())")

    def test_a_changed_client_stops_the_build(self) -> None:
        with self.assertRaises(ValueError):
            lp.patch(lp.BASE_DEFINE, 'exports.USESDK="1";exports.USESDK="1"')
        with self.assertRaises(ValueError):
            lp.patch(lp.LOGIN_DEBUG, "nothing to patch")

    def test_every_file_has_a_patch(self) -> None:
        self.assertEqual(len(lp.FILES), 7)
        for rel in lp.FILES:
            self.assertTrue(rel.startswith("Client/"), rel)


if __name__ == "__main__":
    unittest.main()
