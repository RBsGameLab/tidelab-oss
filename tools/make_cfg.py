#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-173e: a new state's config files, made from the user's own 3.8 client.

The config server (specs/config.md) answers the launcher's first requests from
<state>/cfg/index/<UrlPath>/. A real 3.8 run asks only for index.json, Windows/config.json,
Windows/config_sd.json and Windows/VideoConfig_sd.json (plus the pak key files, which the
client takes from the official CDN, RB.RBKeys). This tool writes them without any file from the
game in the repository:

  index.json            our own: every URL points at the local config and login servers
  Windows/config*.json  PackageVersion 3.8.0 and ResVersions; each manifest's IndexSha1 is the
                        SHA1 of the client's cached manifest
                        (Client/Saved/Resources/3.8.0/ResManifest/Manifest<Name>_3.8.0.txt)
  Windows/VideoConfig*.json   IndexSha1 = SHA1 of the cached video manifest
                        (Client/Saved/Resources/Video/<tier>/3.8.0/VideoManifest.json)

The caches exist once the client has started online. ChangeList is left empty (the launcher then
reports the package's own changelist, BaseConfigController.GetP4Version).

    python -I tools/make_cfg.py --game <folder holding Client> --state <state dir> [--force]
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import time

VERSION = "3.8.0"
# The 3.8 client's UrlPath and MixUri (its KuroPublicConfig.ini; specs/config.md).
URL_PATH = "zXGBurT1FHY5eBpEhgbCw7n85imvi3XR"
CONFIG_SERVER = "http://127.0.0.1:5500/"
LOGIN_SERVER = "http://127.0.0.1:10001"
BASE_PACKS = ["launcher", "resource", "en", "ja", "ko", "zh"]
TIERS = ["sd", "hd", "uhd"]


class MissingCache(Exception):
    pass


def sha1(path: str) -> str:
    with open(path, "rb") as f:
        return hashlib.sha1(f.read()).hexdigest()


def manifests(res_dir: str) -> dict[str, str]:
    """Manifest<Name>_3.8.0.txt in ResManifest -> {Name: SHA1 upper-case}."""
    out = {}
    suffix = f"_{VERSION}.txt"
    for name in sorted(os.listdir(res_dir)):
        if name.startswith("Manifest") and name.endswith(suffix):
            out[name[len("Manifest"):-len(suffix)]] = sha1(os.path.join(res_dir, name)).upper()
    return out


def tier_of(name: str) -> str:
    for t in TIERS:
        if name.endswith("_" + t.upper()):
            return t
    return ""


def version_config(found: dict[str, str], tier: str, now: int) -> dict | None:
    """config.json (tier "") or config_<tier>.json. The plain one lists ManifestAggregated and
    the option packs, a tier lists its own option-pack manifests under the plain pack names."""
    res: dict[str, dict] = {}
    for p in BASE_PACKS:
        res[p] = {"Name": p, "Version": VERSION, "IndexSha1": {}}
    packs = 0
    for name, digest in found.items():
        if name.startswith("Lang_"):
            continue
        if name == "ManifestAggregated" or name == "Aggregated":
            if tier == "":
                res["ManifestAggregated"] = {"Name": "ManifestAggregated", "Version": VERSION, "IndexSha1": {VERSION: digest}}
            continue
        if tier_of(name) != tier:
            continue
        pack = name if tier == "" else name[: -len(tier) - 1]
        res[pack] = {"Name": pack, "Version": VERSION, "IndexSha1": {VERSION: digest}}
        packs += 1
    if packs == 0:
        return None
    return {"PackageVersion": VERSION, "ChangeList": "", "CompatibleChangeLists": [], "ResVersions": res, "UpdateTime": now}


def index_json(now: int) -> dict:
    login = LOGIN_SERVER
    update = lambda page: {"MainUrl": f"{login}/force_update/{page}", "SubUrl": f"{login}/force_update/{page}"}  # noqa: E731
    entry = {
        "CdnUrl": [{"url": CONFIG_SERVER, "weight": "100"}],
        "SecondaryUrl": [],
        "PriceRatio": 1,
        "SpeedRatio": 1,
        "MixUri": URL_PATH,
        "ResUri": URL_PATH,
        "LoginServers": [{"id": hashlib.md5(b"tidelab").hexdigest(), "ip": login, "name": "tidelab"}],
        "PrivateServers": {"enable": False, "serverUrl": ""},
        "NoticeUrl": f"{login}/notice",
        "GachaUrl": {"GachaRecord": f"{login}/gacha/record", "GachaPoolDetail": f"{login}/gacha/detail"},
        "PackageUpdateUrl": update("UpdateJs.html"),
        "PackageUpdateDescUrl": update("UpdateDesc.html"),
        "ParallelPackageDescUrl": update("OptionalUpdate.json"),
        "GARUrl": login,
        "GmOpen": False,
        "RptOpen": False,
        "IosAuditFirstDownloadTip": False,
        "GrayBox": {"Items": []},
        "UdpPort": [7777],
    }
    return {"default": entry, "p1": {"IosAuditFirstDownloadTip": False}, "ModifyTime": now}


def build(game: str, now: int) -> dict[str, dict]:
    """{relative path under cfg/index: JSON value}."""
    saved = os.path.join(game, "Client", "Saved", "Resources")
    res_dir = os.path.join(saved, VERSION, "ResManifest")
    if not os.path.isdir(res_dir):
        raise MissingCache(f"no {res_dir}: start the 3.8 client online once, then run this again")
    found = manifests(res_dir)
    if "Aggregated" not in found:
        raise MissingCache(f"no ManifestAggregated_{VERSION}.txt in {res_dir}")
    found["ManifestAggregated"] = found.pop("Aggregated")
    out: dict[str, dict] = {f"{URL_PATH}/index.json": index_json(now)}
    win = f"{URL_PATH}/Windows/"
    for tier in [""] + TIERS:
        cfg = version_config(found, tier, now)
        if cfg is not None:
            out[win + ("config.json" if tier == "" else f"config_{tier}.json")] = cfg
    videos = {}
    for tier in TIERS:
        p = os.path.join(saved, "Video", tier, VERSION, "VideoManifest.json")
        if os.path.isfile(p):
            videos[tier] = {"UpdateTime": now, "IndexSha1": sha1(p)}
    plain = os.path.join(saved, "Video", VERSION, "VideoManifest.json")
    if os.path.isfile(plain):
        out[win + "VideoConfig.json"] = {"UpdateTime": now, "IndexSha1": sha1(plain)}
    elif "sd" in videos:
        out[win + "VideoConfig.json"] = videos["sd"]
    for tier, v in videos.items():
        out[win + f"VideoConfig_{tier}.json"] = v
    return out


def write(files: dict[str, dict], state: str, force: bool) -> list[str]:
    root = os.path.join(state, "cfg", "index")
    written = []
    for rel, value in files.items():
        path = os.path.join(root, rel.replace("/", os.sep))
        if os.path.exists(path) and not force:
            continue
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as f:
            f.write(json.dumps(value, ensure_ascii=False, indent=4) + "\n")
        written.append(rel)
    return written


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--game", required=True, help="the 3.8 client (the folder holding Client)")
    ap.add_argument("--state", required=True, help="the state directory")
    ap.add_argument("--force", action="store_true", help="replace existing files")
    args = ap.parse_args()
    try:
        files = build(args.game, int(time.time()))
    except MissingCache as e:
        print(f"make_cfg: {e}", file=sys.stderr)
        return 1
    written = write(files, args.state, args.force)
    print(f"make_cfg: {len(written)} of {len(files)} config files written to {os.path.join(args.state, 'cfg', 'index')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
