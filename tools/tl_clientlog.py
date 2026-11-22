#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-161 client log triage: decode the client log (pitfall list §1) and print only its errors,
grouped and counted, with the first line of each group.

    python -I tools/tl_clientlog.py [--minutes N] [--grep REGEX] [--log PATH] [--top K]

--minutes: only the last N minutes of the log (by its own timestamps; default all).
--grep: instead of errors, the lines matching REGEX (counted the same way).
The decoded log is kept at out/tl/client_decoded.log for follow-up greps.
"""

from __future__ import annotations

import argparse
import collections
import os
import re
import subprocess
import sys
from datetime import datetime, timedelta
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_paths as P  # noqa: E402

STAMP = re.compile(r"^\[(\d{4})\.(\d\d)\.(\d\d)-(\d\d)\.(\d\d)\.(\d\d):(\d{3})\]")
ERROR = re.compile(r"Error:|Exception|没有配置|Callback执行异常|UnobservedException|怪物状态出问题|等待实体超时|AwakeEntity失败")
# Noise the client logs at every start: never the cause.
NOISE = re.compile(r"LogWwise|LogHLOD|JitProfile|LogShaderCompilers|LogStreaming|LogStreamlineAPI")
# Listed first whatever their count: what usually breaks a view (R-109, R-160d).
SEVERE = re.compile(r"Exception|Callback执行异常|没有配置|InvalidOperation|NullReference")


def stamp_of(line: str) -> datetime | None:
    m = STAMP.match(line)
    if not m:
        return None
    y, mo, d, h, mi, s, ms = map(int, m.groups())
    return datetime(y, mo, d, h, mi, s, ms * 1000)


def normalize(line: str) -> str:
    """The message without its time stamp, frame and numbers: lines that differ only there group."""
    msg = STAMP.sub("", line)
    msg = re.sub(r"^\[\s*\d+\]", "", msg)
    msg = re.sub(r"\d+", "#", msg)
    return msg.strip()[:160]


def triage(lines: list[str], minutes: int | None, pattern: re.Pattern | None, top: int) -> list[tuple[int, int, str]]:
    """(count, first line number, first line) per group, most frequent first."""
    start = None
    if minutes:
        last = next((t for t in map(stamp_of, reversed(lines)) if t), None)
        if last:
            start = last - timedelta(minutes=minutes)
    groups: dict[str, list] = collections.OrderedDict()
    current = None
    for i, line in enumerate(lines, 1):
        t = stamp_of(line)
        if t:
            current = t
        if start and (current is None or current < start):
            continue
        if line.lstrip().startswith("at "):
            continue  # stack frames: the line above names the error
        if pattern:
            if not pattern.search(line):
                continue
        elif not ERROR.search(line) or NOISE.search(line):
            continue
        key = normalize(line)
        if key in groups:
            groups[key][0] += 1
        else:
            groups[key] = [1, i, line.rstrip()]
    ranked = sorted(groups.values(), key=lambda g: (not SEVERE.search(g[2]), -g[0], g[1]))
    return [tuple(g) for g in ranked[:top]]


def decode(log: Path, decoded: Path) -> list[str] | str:
    """The decoded log's lines, or the decoder's error text (tl_audit.py uses it too)."""
    r = subprocess.run([sys.executable, "-I", str(P.LOG_DECODER), str(log), str(decoded)], capture_output=True, text=True)
    if r.returncode or not decoded.exists():
        return "decode failed: " + (r.stderr or r.stdout)[-500:]
    return decoded.read_text(encoding="utf-8", errors="replace").splitlines()


def main(argv: list[str]) -> int:
    P.utf8_stdout()
    ap = argparse.ArgumentParser()
    ap.add_argument("--minutes", type=int)
    ap.add_argument("--grep")
    ap.add_argument("--log", type=Path, default=P.CLIENT_LOG)
    ap.add_argument("--top", type=int, default=15)
    a = ap.parse_args(argv)
    decoded = P.out_file("client_decoded.log")
    lines = decode(a.log, decoded)
    if isinstance(lines, str):
        print(lines)
        return 1
    rows = triage(lines, a.minutes, re.compile(a.grep) if a.grep else None, a.top)
    span = next((stamp_of(l) for l in lines if stamp_of(l)), None), next((stamp_of(l) for l in reversed(lines) if stamp_of(l)), None)
    print(f"{len(lines)} lines {span[0]} → {span[1]}; {len(rows)} groups; decoded: {decoded}")
    for count, first, line in rows:
        print(f"{count:5}× L{first}: {line[:220]}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
