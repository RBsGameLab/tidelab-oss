#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Tests for build_cspak.py and pak_build.py: run `python -I tools/build_cspak_test.py`."""

from __future__ import annotations

import os
import struct
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_cspak  # noqa: E402
import pak_build  # noqa: E402


def fake_dll(size: int) -> bytes:
    return b"\0" * 11 + b"MZ" + bytes(i % 251 for i in range(size))


class ShippedTest(unittest.TestCase):
    def test_prefix_is_eleven_zero_bytes(self) -> None:
        self.assertEqual(build_cspak.shipped(b"MZabc"), b"\0" * 11 + b"MZabc")

    def test_refuses_a_file_that_still_has_its_prefix(self) -> None:
        with self.assertRaises(ValueError):
            build_cspak.shipped(b"\0" * 11 + b"MZ")


class PakBuildTest(unittest.TestCase):
    def setUp(self) -> None:
        self.files = [
            (build_cspak.PAK_DIR + "Aki.Launcher.dll", fake_dll(5000)),
            (build_cspak.PAK_DIR + "Aki.Game.dll", fake_dll(9000)),
        ]
        self.pak = pak_build.build(self.files, align_dll=4096)

    def test_mz_of_every_dll_is_4096_aligned(self) -> None:
        # R-55: the client maps script assemblies straight out of the pak.
        for _, content in self.files:
            at = self.pak.find(content) + content.find(b"MZ")
            self.assertEqual(at % 4096, 0)

    def test_footer_has_the_magic_and_version_12(self) -> None:
        # The footer's last 204 bytes: magic, version, index offset and size, index SHA-1, 160 zeros.
        magic, version, index_offset, index_size = struct.unpack_from("<IIqq", self.pak, len(self.pak) - 204)
        self.assertEqual((magic, version), (pak_build.MAGIC, 12))
        self.assertLess(index_offset + index_size, len(self.pak) - 204)

    def test_dll_entries_carry_custom_data_4(self) -> None:
        # Kuro's encoded entries: bitfield u32, CustomData u8, size u32, offset u32; one per file.
        index_offset = struct.unpack_from("<q", self.pak, len(self.pak) - 204 + 8)[0]
        p = index_offset + 4 + len(pak_build.MOUNT) + 1 + 12 + 4 + 16 + 20 + 4 + 16 + 20
        enc_len = struct.unpack_from("<i", self.pak, p)[0]
        self.assertEqual(enc_len, 13 * len(self.files))
        for i in range(len(self.files)):
            self.assertEqual(self.pak[p + 4 + i * 13 + 4], 4)

    def test_same_input_same_bytes(self) -> None:
        self.assertEqual(self.pak, pak_build.build(list(reversed(self.files)), align_dll=4096))


if __name__ == "__main__":
    unittest.main()
