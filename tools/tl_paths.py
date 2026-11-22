# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""Shared paths of the helper scripts (tools/*.py). No machine paths are written in (R-171): the
same rules as tools/paths.cmd and the launcher (client/core/src/config.rs). The lab and the state
directory default to folders beside the main checkout; tidelab.local.txt at a checkout's root
(key=value: lab, state, game, go; # comments) overrides them; TIDELAB_LAB still overrides lab."""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def _main_worktree() -> Path:
    """The checkout on branch main: the live server's, holding tidelab.local.txt (else this one)."""
    try:
        out = subprocess.run(["git", "worktree", "list", "--porcelain"], cwd=REPO, capture_output=True,
                             text=True, timeout=10).stdout
        tree = None
        for line in out.splitlines():
            if line.startswith("worktree "):
                tree = Path(line[len("worktree "):].strip())
            elif line.strip() == "branch refs/heads/main" and tree is not None:
                return tree
    except (OSError, subprocess.SubprocessError):
        pass
    return REPO


def read_local(path: Path) -> dict[str, str]:
    """tidelab.local.txt: key=value lines, keys lower-cased; missing file → {}."""
    values: dict[str, str] = {}
    try:
        text = path.read_text(encoding="utf-8-sig")
    except OSError:
        return values
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip().lower()] = value.strip()
    return values


MAIN_WORKTREE = _main_worktree()
LOCAL = read_local(REPO / "tidelab.local.txt") or read_local(MAIN_WORKTREE / "tidelab.local.txt")
_BESIDE = MAIN_WORKTREE.parent


def _go_bin() -> Path:
    if LOCAL.get("go"):
        return Path(LOCAL["go"]).parent
    found = shutil.which("go")
    if found:
        return Path(found).parent
    sdk = Path(os.environ.get("LOCALAPPDATA", "")) / "Programs" / "go-sdk"
    for d in sorted(sdk.glob("go*"), reverse=True):
        if (d / "go" / "bin" / "go.exe").exists():
            return d / "go" / "bin"
    return Path()


def _chrome() -> Path:
    for base in (os.environ.get("PROGRAMFILES"), os.environ.get("PROGRAMFILES(X86)"), os.environ.get("LOCALAPPDATA")):
        if base and (Path(base) / "Google" / "Chrome" / "Application" / "chrome.exe").exists():
            return Path(base) / "Google" / "Chrome" / "Application" / "chrome.exe"
    return Path(shutil.which("chrome") or "chrome.exe")


LAB = Path(LOCAL.get("lab") or os.environ.get("TIDELAB_LAB") or _BESIDE / "tidelab-data")
GAME = Path(LOCAL.get("game") or LAB / "Game38")
DATA = LAB / "gamedata" / "3.8"
BINDATA = DATA / "BinData"
MORE_BINDATA = DATA / "BinData-beta"
QUEST_DATA = DATA / "questgen" / "assets" / "quest"
CLIENT_SRC = LAB / "reference" / "client-src-3.8"
CLIENT_LOG = GAME / "Client" / "Saved" / "Logs" / "Client.log"
LOG_DECODER = LAB / "scripts" / "dev" / "decode_client_log.py"
LIVE_STATE = Path(LOCAL.get("state") or _BESIDE / "tidelab-state")
GO_BIN = _go_bin()
CHROME = _chrome()
# Scratch output (big results go here; scripts print only summaries).
OUT = REPO / "out" / "tl"

def utf8_stdout() -> None:
    """python -I ignores PYTHONIOENCODING: Chinese output needs this on a cp1252 console."""
    for s in (sys.stdout, sys.stderr):
        try:
            s.reconfigure(encoding="utf-8")
        except AttributeError:
            pass


def out_file(name: str) -> Path:
    OUT.mkdir(parents=True, exist_ok=True)
    return OUT / name


def go_exe() -> str:
    """Full path: Windows looks the program up on the parent's PATH, not the child env's."""
    return str(GO_BIN / "go.exe")


def go_env() -> dict:
    env = dict(os.environ)
    env["PATH"] = str(GO_BIN) + os.pathsep + env.get("PATH", "")
    return env
