#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-161 test server: a throwaway gamesv on spare ports and a temporary state, built from this
checkout. Never touches the live server (13100) or the live state directory.

    python -I tools/tl_testserver.py start [--keep-state]   build, start, wait until listening
    python -I tools/tl_testserver.py get api/tower          GET on the panel; JSON to out/tl/, prints size + keys
    python -I tools/tl_testserver.py post api/x '{"a":1}'   POST on the panel; prints status + body (short)
    python -I tools/tl_testserver.py shot '#tower' [name]   headless Chrome screenshot → out/tl/<name>.png
    python -I tools/tl_testserver.py log [pattern]          last matching server log lines (default warnings)
    python -I tools/tl_testserver.py stop
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_paths as P  # noqa: E402

GAME_PORT = 13199
PANEL_PORT = 13181
ROOT = P.OUT / "testserver"
STATE = ROOT / "state"
BIN = ROOT / "bin"
LOG = ROOT / "gamesv.log"
PID = ROOT / "pid"


def gamesv_args(state: Path = STATE) -> list[str]:
    """The command line, as start-server.cmd but on the spare ports and the temp state."""
    return [
        str(BIN / "gamesv.exe"),
        "--listen_address", f"127.0.0.1:{GAME_PORT}",
        "--panel_address", f"127.0.0.1:{PANEL_PORT}",
        "--state_dir", str(state),
        "--bindata", str(P.BINDATA),
        "--more_bindata", str(P.MORE_BINDATA),
        "--bin_cache", str(P.MAIN_WORKTREE / "out" / "bincache"),  # warm: the live server's
        "--quest_data", str(P.QUEST_DATA),
        "--cmd_file", str(state / "cmd.txt"),
    ]


def wait_ready(log: Path, timeout: float = 180) -> str:
    """'ready', or the first fatal line, or 'timeout'."""
    end = time.time() + timeout
    while time.time() < end:
        if log.exists():
            text = log.read_text(encoding="utf-8", errors="replace")
            if "is listening at" in text:
                return "ready"
            m = re.search(r"^(error\(.*|.*could not.*|panic:.*)$", text, re.M)
            if m:
                return m.group(1)
        time.sleep(0.5)
    return "timeout"


def stop() -> None:
    if PID.exists():
        pid = PID.read_text().strip()
        subprocess.run(["taskkill", "/F", "/PID", pid], capture_output=True)
        PID.unlink()
        print(f"stopped {pid}")
    else:
        print("not running")


def start(keep_state: bool) -> int:
    stop()
    ROOT.mkdir(parents=True, exist_ok=True)
    if not keep_state and STATE.exists():
        shutil.rmtree(STATE)
    (STATE / "gateway").mkdir(parents=True, exist_ok=True)
    key = P.LIVE_STATE / "gateway" / "client_public_key.der"
    shutil.copy(key, STATE / "gateway" / key.name)
    b = subprocess.run([P.go_exe(), "build", "-o", str(BIN) + "/", "./cmd/gamesv"], cwd=P.REPO / "server",
                       env=P.go_env(), capture_output=True, text=True)
    if b.returncode:
        print("build failed:\n" + (b.stdout + b.stderr)[-2000:])
        return 1
    log = open(LOG, "w", encoding="utf-8")
    proc = subprocess.Popen(gamesv_args(), stdout=log, stderr=subprocess.STDOUT,
                            creationflags=getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0))
    PID.write_text(str(proc.pid))
    state = wait_ready(LOG)
    print(f"{state}: panel http://127.0.0.1:{PANEL_PORT}/  game udp {GAME_PORT}  pid {proc.pid}  log {LOG}")
    return 0 if state == "ready" else 1


def summarize(value) -> str:
    """Keys and list lengths, one level down: enough to see the shape without the data."""
    if isinstance(value, dict):
        parts = []
        for k, v in value.items():
            if isinstance(v, list):
                parts.append(f"{k}[{len(v)}]")
            elif isinstance(v, dict):
                parts.append(f"{k}{{{len(v)}}}")
            else:
                s = json.dumps(v, ensure_ascii=False)
                parts.append(f"{k}={s[:40]}")
        return " ".join(parts)
    if isinstance(value, list):
        return f"list[{len(value)}]"
    return json.dumps(value, ensure_ascii=False)[:200]


def api_path(path: str) -> str:
    """'/api/x' or 'api/x'; undoes Git Bash turning '/api/x' into 'C:/Program Files/Git/api/x'."""
    path = path.replace("\\", "/")
    if "/Git/" in path:
        path = path.split("/Git/", 1)[1]
    return "/" + path.lstrip("/")


def request(method: str, path: str, body: str | None = None) -> tuple[int, str]:
    path = api_path(path)
    req = urllib.request.Request(f"http://127.0.0.1:{PANEL_PORT}{path}", method=method,
                                 data=body.encode() if body is not None else None,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, r.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8")


def main(argv: list[str]) -> int:
    P.utf8_stdout()
    if not argv:
        print(__doc__)
        return 2
    cmd, rest = argv[0], argv[1:]
    if cmd == "start":
        return start("--keep-state" in rest)
    if cmd == "stop":
        stop()
        return 0
    if cmd == "get":
        status, text = request("GET", rest[0])
        name = re.sub(r"[^A-Za-z0-9]+", "_", rest[0]).strip("_") or "root"
        out = P.out_file(f"get_{name}.json")
        out.write_text(text, encoding="utf-8")
        try:
            shape = summarize(json.loads(text))
        except ValueError:
            shape = text[:200]
        print(f"{status} {len(text)} bytes → {out}\n{shape}")
        return 0 if status == 200 else 1
    if cmd == "post":
        status, text = request("POST", rest[0], rest[1] if len(rest) > 1 else "{}")
        print(status, text[:300])
        return 0 if status == 200 else 1
    if cmd == "shot":
        name = rest[1] if len(rest) > 1 else "panel"
        out = P.out_file(f"{name}.png")
        subprocess.run([str(P.CHROME), "--headless=new", "--disable-gpu", f"--user-data-dir={ROOT / 'chrome'}",
                        "--window-size=1280,1500", "--virtual-time-budget=4000", f"--screenshot={out}",
                        f"http://127.0.0.1:{PANEL_PORT}/{rest[0] if rest else ''}"], capture_output=True, timeout=90)
        print(out if out.exists() else "no screenshot")
        return 0 if out.exists() else 1
    if cmd == "log":
        pat = re.compile(rest[0] if rest else r"warning|error|panic")
        lines = [l for l in LOG.read_text(encoding="utf-8", errors="replace").splitlines() if pat.search(l)]
        print(f"{len(lines)} lines match")
        print("\n".join(l[:200] for l in lines[-15:]))
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
