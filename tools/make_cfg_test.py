#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""Tests for make_cfg.py: run `python -I tools/make_cfg_test.py`."""
from __future__ import annotations

import hashlib
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import make_cfg as mc  # noqa: E402


def put(root: str, rel: str, data: bytes) -> str:
    p = os.path.join(root, rel.replace("/", os.sep))
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "wb") as f:
        f.write(data)
    return hashlib.sha1(data).hexdigest()


class MakeCfgTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.game = os.path.join(self.tmp.name, "Game38")
        res = "Client/Saved/Resources/"
        man = res + "3.8.0/ResManifest/"
        self.agg = put(self.game, man + "ManifestAggregated_3.8.0.txt", b"agg").upper()
        self.pack = put(self.game, man + "ManifestOption_Pack_QQ_3.8.0.txt", b"qq").upper()
        self.pack_sd = put(self.game, man + "ManifestOption_Pack_QQ_SD_3.8.0.txt", b"qq-sd").upper()
        put(self.game, man + "ManifestOption_Pack_QQ_3.7.0.txt", b"old")
        self.video_sd = put(self.game, res + "Video/sd/3.8.0/VideoManifest.json", b"{}")

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_hashes_come_from_the_cached_manifests(self) -> None:
        files = mc.build(self.game, 123)
        w = mc.URL_PATH + "/Windows/"
        cfg = files[w + "config.json"]
        self.assertEqual(cfg["PackageVersion"], "3.8.0")
        self.assertEqual(cfg["ChangeList"], "")
        self.assertEqual(cfg["ResVersions"]["ManifestAggregated"]["IndexSha1"], {"3.8.0": self.agg})
        self.assertEqual(cfg["ResVersions"]["Option_Pack_QQ"]["IndexSha1"], {"3.8.0": self.pack})
        self.assertEqual(cfg["ResVersions"]["zh"], {"Name": "zh", "Version": "3.8.0", "IndexSha1": {}})
        sd = files[w + "config_sd.json"]
        self.assertNotIn("ManifestAggregated", sd["ResVersions"])
        self.assertEqual(sd["ResVersions"]["Option_Pack_QQ"]["IndexSha1"], {"3.8.0": self.pack_sd})
        self.assertEqual(sd["ResVersions"]["zh"]["IndexSha1"], {})
        self.assertNotIn(w + "config_hd.json", files)
        self.assertEqual(files[w + "VideoConfig_sd.json"], {"UpdateTime": 123, "IndexSha1": self.video_sd})
        self.assertEqual(files[w + "VideoConfig.json"]["IndexSha1"], self.video_sd)

    def test_index_points_only_at_local_servers(self) -> None:
        text = json.dumps(mc.build(self.game, 1)[mc.URL_PATH + "/index.json"])
        hosts = {u.split("/")[2] for u in text.replace('"', " ").split() if u.startswith("http")}
        self.assertEqual(hosts, {"127.0.0.1:5500", "127.0.0.1:10001"})

    def test_no_cache_is_an_error(self) -> None:
        with self.assertRaises(mc.MissingCache):
            mc.build(os.path.join(self.tmp.name, "none"), 1)

    def test_existing_files_are_kept_unless_forced(self) -> None:
        state = os.path.join(self.tmp.name, "state")
        files = mc.build(self.game, 1)
        self.assertEqual(len(mc.write(files, state, False)), len(files))
        self.assertEqual(mc.write(files, state, False), [])
        self.assertEqual(len(mc.write(files, state, True)), len(files))


if __name__ == "__main__":
    unittest.main()
