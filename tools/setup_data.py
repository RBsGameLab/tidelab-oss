#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-174: makes the game data from the user's own 3.8 client: no lab folder, no pak keys.
setup.cmd (tools/setup.ps1) runs it when <lab>/gamedata/3.8/BinData is missing; by hand:

    python -I tools/setup_data.py --lab <data folder> --game <3.8 client> [--go <go.exe>]
                                  [--from STAGE | --rebuild] [--only STAGE]

Stages, in order; each is skipped when its output is there (a new run resumes), --from STAGE
redoes that stage and the ones after it, --rebuild redoes all:

  tools     patch DLL + launcher (cargo), tidelab_export.dll (dotnet), copied beside the client exe
  export    run 1: the client copies its own files to <lab>/export (-RbExport), then quits
  refs      <lab>/backups/csref38 from the exported ScriptAssemblies (tools/client_refs.py)
  generate  protocol + table code + BinData + panel names (tools/generate.ps1); Go servers built
  extra     BinData-beta (tools/extra_tables.py), quest data (tools/questgen), the inputs of run 2
  paks      C# patch pak, login pak (out/paks)
  ingame    run 2: throwaway servers and save, the client logs in and answers GameplayTagRemap
            and CharacterPartConfig (tools/rbexport Tags.cs, Parts.cs) -> BinData-beta

Layout under <lab>: export/, backups/csref38/, gamedata/3.8/{BinData, BinData-beta, questgen,
panel, setup}: the same as the lab's, so start-server.cmd and the launcher read it as it is.
"""
from __future__ import annotations

import argparse
import glob
import os
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass
from typing import Callable

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CLIENT_EXE = "Client-Win64-Shipping.exe"
LAUNCHER, PATCH_DLL, EXPORT_DLL = "tidelab_start.exe", "tidelab_patch.dll", "tidelab_export.dll"
PAKS = ("rb38_cs_300_p.pak", "rb38_login_100_p.pak")
SERVERS = ("configsv", "loginsv", "gamesv")
SETUP_ACCOUNT = "setup"
PROTOC_GEN_GO = "google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12"
NO_WINDOW = 0x08000000


class Fail(Exception):
    pass


@dataclass
class Paths:
    lab: str
    game: str

    @property
    def export(self) -> str: return os.path.join(self.lab, "export")
    @property
    def refs(self) -> str: return os.path.join(self.lab, "backups", "csref38")
    @property
    def data(self) -> str: return os.path.join(self.lab, "gamedata", "3.8")
    @property
    def bindata(self) -> str: return os.path.join(self.data, "BinData")
    @property
    def beta(self) -> str: return os.path.join(self.data, "BinData-beta")
    @property
    def questgen(self) -> str: return os.path.join(self.data, "questgen")
    @property
    def quest(self) -> str: return os.path.join(self.questgen, "assets", "quest")
    @property
    def inputs(self) -> str: return os.path.join(self.data, "setup")
    @property
    def win64(self) -> str: return os.path.join(self.game, "Client", "Binaries", "Win64")
    @property
    def game_paks(self) -> str: return os.path.join(self.game, "Client", "Content", "Paks")
    @property
    def configdb(self) -> str: return os.path.join(self.export, "Content", "Aki", "ConfigDB")
    @property
    def config(self) -> str: return os.path.join(self.export, "Config")
    @property
    def built_paks(self) -> str: return os.path.join(REPO, "out", "paks")
    @property
    def gobin(self) -> str: return os.path.join(REPO, "out", "gobin")


# --- the reports the client export writes -------------------------------------------------

def export_report(text: str) -> tuple[int, int] | None:
    """rb_export.txt's last line "done<TAB>N files, F failed" as (N, F); None before it."""
    for line in reversed(text.splitlines()):
        if line.startswith("done\t"):
            words = line.split("\t", 1)[1].replace(",", "").split()
            return int(words[0]), int(words[2])
    return None


def parts_report(text: str) -> tuple[int, int] | None:
    """rb_parts.txt's first line "done: L/C classes, P with parts, F failed, M models" as (F, M)."""
    first = text.splitlines()[0] if text else ""
    if not first.startswith("done: "):
        return None
    fields = {w[-1]: int(w[0]) for w in (p.split() for p in first[6:].split(", ")) if w and w[0].isdigit()}
    return fields.get("failed", -1), fields.get("models", -1)


def tags_report(text: str) -> tuple[int, int] | None:
    """rb_tags.txt's "asked A: R remapped, S same id, U unknown (0)" as (R, U)."""
    for line in text.splitlines():
        if line.startswith("asked "):
            w = line.replace(":", "").replace(",", "").split()
            return int(w[2]), int(w[7])
    return None


def read(path: str) -> str:
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return ""


# --- processes ------------------------------------------------------------------------------

def say(text: str) -> None:
    print(text, flush=True)


def run(cmd: list[str], cwd: str | None = None, env: dict[str, str] | None = None) -> None:
    say("  > " + " ".join(os.path.basename(cmd[0]) if i == 0 else c for i, c in enumerate(cmd))[:200])
    r = subprocess.run(cmd, cwd=cwd, env=env)
    if r.returncode != 0:
        raise Fail(f"{os.path.basename(cmd[0])} failed (exit {r.returncode})")


def py(script: str, *args: str) -> None:
    run([sys.executable, "-I", os.path.join(REPO, "tools", script), *args], cwd=REPO)


def running(image: str) -> bool:
    r = subprocess.run(["tasklist", "/FI", f"IMAGENAME eq {image}", "/NH", "/FO", "CSV"],
                       capture_output=True, text=True, errors="replace")
    return f'"{image.lower()}"' in r.stdout.lower()


def wait_client(minutes: float) -> None:
    """Waits for the client to start (5 minutes: there may be a Windows prompt to answer) and then
    to end by itself (the export quits it)."""
    end = time.time() + 300
    while not running(CLIENT_EXE):
        if time.time() > end:
            raise Fail("the client did not start (Windows prompt declined or not answered?)")
        time.sleep(2)
    end = time.time() + minutes * 60
    while running(CLIENT_EXE):
        if time.time() > end:
            subprocess.run(["taskkill", "/F", "/IM", CLIENT_EXE], capture_output=True)
            raise Fail(f"the client did not finish in {minutes:g} minutes (ended)")
        time.sleep(5)


def start_client(p: Paths, *args: str) -> None:
    """Through ShellExecute, as start-game.cmd and the launcher do: tidelab_start.exe's manifest
    asks for elevation (highestAvailable), which CreateProcess refuses (error 740) and
    ShellExecute shows as the Windows prompt."""
    # Lower CPU load.
    # during start-up stalls the client (hang on the game thread, fatal after 4 minutes); rbauto
    # creates tidelab_ace.quiet again after the first world load.
    try:
        os.remove(os.path.join(p.win64, "tidelab_ace.quiet"))
    except FileNotFoundError:
        pass
    open(os.path.join(p.win64, "tidelab_perfsight.quiet"), "a").close()
    say("  (answer Yes if Windows asks whether tidelab_start may make changes)")
    try:
        os.startfile(os.path.join(p.win64, LAUNCHER), "open", subprocess.list2cmdline(["-krqlv=SD", *args]), p.win64)
    except OSError as e:
        raise Fail(f"could not start the client: {e}") from e


def same_file(a: str, b: str) -> bool:
    try:
        with open(a, "rb") as fa, open(b, "rb") as fb:
            return fa.read() == fb.read()
    except OSError:
        return False


def go_env(go: str) -> dict[str, str]:
    env = dict(os.environ)
    gopath = subprocess.run([go, "env", "GOPATH"], capture_output=True, text=True).stdout.strip()
    extra = [os.path.dirname(go)] + ([os.path.join(gopath, "bin")] if gopath else [])
    env["PATH"] = os.pathsep.join(extra + [env.get("PATH", "")])
    return env


# --- stages ---------------------------------------------------------------------------------

TOOL_FILES = ((LAUNCHER, "client/target/release"), (PATCH_DLL, "client/target/release"),
              (EXPORT_DLL, "tools/rbexport/bin/setup"))


def done_tools(p: Paths) -> bool:
    """This checkout's builds are beside the client exe (not files some other checkout left)."""
    return all(same_file(os.path.join(REPO, src, f), os.path.join(p.win64, f)) for f, src in TOOL_FILES)


def stage_tools(p: Paths, a: argparse.Namespace) -> None:
    run([a.cargo, "build", "--release"], cwd=os.path.join(REPO, "client"))
    rbexport = os.path.join(REPO, "tools", "rbexport")
    run([a.dotnet, "build", rbexport, "-c", "Release", "-o", os.path.join(rbexport, "bin", "setup")])
    for f, src in TOOL_FILES:
        shutil.copy2(os.path.join(REPO, src, f), os.path.join(p.win64, f))


def done_export(p: Paths) -> bool:
    r = export_report(read(os.path.join(p.export, "rb_export.txt")))
    return r is not None and r[0] > 0 and r[1] == 0


def stage_export(p: Paths, a: argparse.Namespace) -> None:
    if running(CLIENT_EXE):
        raise Fail("the game is running: close it first")
    # Our paks would replace the client's own scripts and configs in the export; the launcher
    # copies them in again from out/paks at the next start.
    for name in PAKS:
        pak = os.path.join(p.game_paks, name)
        if os.path.isfile(pak):
            os.remove(pak)
            say(f"  removed {name} from the client (put back at the next game start)")
    shutil.rmtree(p.export, ignore_errors=True)
    say("  starting the client to copy its files (about 10 minutes; it quits by itself)")
    start_client(p, f"-RbExport={p.export}")
    wait_client(40)
    r = export_report(read(os.path.join(p.export, "rb_export.txt")))
    if r is None or r[1] or not r[0]:
        raise Fail(f"export incomplete ({r}): see {os.path.join(p.export, 'rb_export.txt')}")
    say(f"  exported {r[0]} files")


def done_refs(p: Paths) -> bool:
    return all(os.path.isfile(os.path.join(p.refs, f)) for f in ("Aki.Protocol.dll", "Aki.Generated.dll"))


def stage_refs(p: Paths, a: argparse.Namespace) -> None:
    py("client_refs.py", "--export", p.export, "--out", p.refs)


def done_generate(p: Paths) -> bool:
    return (os.path.isfile(os.path.join(p.bindata, "RoleInfo.json"))
            and os.path.isfile(os.path.join(REPO, "server", "protocol", "msgid.go"))
            and os.path.isfile(os.path.join(REPO, "server", "internal", "data", "tables_gen.go"))
            and all(os.path.isfile(os.path.join(p.gobin, s + ".exe")) for s in SERVERS))


def protoc_found(path: str, home: str) -> bool:
    """Where server/tools/genproto looks: protoc on PATH, else the NuGet Grpc.Tools package."""
    if shutil.which("protoc", path=path):
        return True
    return bool(glob.glob(os.path.join(home, ".nuget", "packages", "grpc.tools", "*", "tools", "windows_x64", "protoc.exe")))


def stage_generate(p: Paths, a: argparse.Namespace) -> None:
    env = go_env(a.go)
    if not shutil.which("protoc-gen-go", path=env["PATH"]):
        run([a.go, "install", PROTOC_GEN_GO], env=env)
    # R-174c: no protoc to install by hand: Grpc.Tools (a package of the generators' test project)
    # brings one; a newcomer's NuGet cache has it only after this restore.
    if not protoc_found(env["PATH"], os.path.expanduser("~")):
        run([a.dotnet, "restore", os.path.join(REPO, "tools", "Tidelab.Tools.Tests")])
    define = os.path.join(p.export, "Content", "Aki", "JavaScript", "Core", "Define")
    run(["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File",
         os.path.join(REPO, "tools", "generate.ps1"),
         "-ProtocolDll", os.path.join(p.refs, "Aki.Protocol.dll"), "-Schema", define,
         "-ConfigDb", p.configdb, "-BinData", p.bindata,
         "-LangDb", os.path.join(p.configdb, "zh-Hans", "lang_multi_text.db"), "-ForceExport"],
        cwd=REPO, env=env)
    run([a.go, "build", "-o", p.gobin + os.sep, "./cmd/..."], cwd=os.path.join(REPO, "server"), env=env)


def done_extra(p: Paths) -> bool:
    return (os.path.isfile(os.path.join(p.beta, "CommonParam.json"))
            and os.path.isfile(os.path.join(p.quest, "TreeOrders.json"))
            and all(os.path.isfile(os.path.join(p.inputs, f)) for f in ("tag_names.txt", "parts_models.txt")))


def stage_extra(p: Paths, a: argparse.Namespace) -> None:
    py("extra_tables.py", "--configdb", p.configdb, "--config", p.config, "--bindata", p.bindata, "--out", p.beta)
    trees = os.path.join(p.export, "Content", "Aki", "Config", "UniverseEditorConfig")
    py(os.path.join("questgen", "build.py"), "--bindata", p.bindata, "--bindata", p.beta,
       "--trees", trees, "--out", p.questgen)
    os.makedirs(p.inputs, exist_ok=True)
    py("tag_names.py", "--config", p.config, "--bindata", p.bindata, "--bindata", p.beta,
       "--out", os.path.join(p.inputs, "tag_names.txt"))
    py("parts_models.py", "--bindata", p.bindata, "--out", os.path.join(p.inputs, "parts_models.txt"))


def done_paks(p: Paths) -> bool:
    return all(os.path.isfile(os.path.join(p.built_paks, f)) for f in PAKS)


def stage_paks(p: Paths, a: argparse.Namespace) -> None:
    say("  building the C# patch pak (about 5 minutes)")
    py("build_cspak.py", "--refs", p.refs)
    py("build_loginpak.py", "--from", p.export, "--game", p.game)


IN_GAME = ("GameplayTagRemap.json", "CharacterPartConfig.json")


def done_ingame(p: Paths) -> bool:
    return all(os.path.isfile(os.path.join(p.beta, f)) for f in IN_GAME)


def run2_beta(p: Paths, into: str) -> None:
    """BinData-beta for the throwaway server: the real one with [] for the tables run 2 makes
    (the server cannot start without them; nothing reads them before the world)."""
    shutil.copytree(p.beta, into)
    for f in IN_GAME:
        if not os.path.isfile(os.path.join(into, f)):
            with open(os.path.join(into, f), "w", encoding="utf-8") as fh:
                fh.write("[]")


def stage_ingame(p: Paths, a: argparse.Namespace) -> None:
    for s in SERVERS:
        if running(s + ".exe"):
            raise Fail(f"{s} is running: stop the server first (run 2 needs its ports), then run setup again")
    if running(CLIENT_EXE):
        raise Fail("the game is running: close it first")
    # Not tempfile.mkdtemp: its folder is the owner's only, and the client runs elevated
    # (tidelab_start.exe), so what it writes there cannot be read back (Permission denied).
    tmp = os.path.join(p.inputs, "run2")
    shutil.rmtree(tmp, ignore_errors=True)
    state, beta, out, logs = (os.path.join(tmp, d) for d in ("state", "beta", "out", "logs"))
    os.makedirs(logs)
    os.makedirs(out)
    py("make_cfg.py", "--game", p.game, "--state", state)
    run2_beta(p, beta)
    for f in ("tag_names.txt", "parts_models.txt"):
        shutil.copy2(os.path.join(p.inputs, f), out)
    open(os.path.join(out, "skip_copy.txt"), "w").close()
    for name in PAKS:
        shutil.copy2(os.path.join(p.built_paks, name), os.path.join(p.game_paks, name))
    procs: list[subprocess.Popen] = []
    ok = False
    try:
        args = {"configsv": [], "loginsv": [],
                "gamesv": ["--bindata", p.bindata, "--more_bindata", beta,
                           "--bin_cache", os.path.join(REPO, "out", "bincache"), "--quest_data", p.quest]}
        for s in SERVERS:
            log = open(os.path.join(logs, s + ".log"), "w")
            procs.append(subprocess.Popen([os.path.join(p.gobin, s + ".exe"), "--state_dir", state, *args[s]],
                                          stdout=log, stderr=subprocess.STDOUT, creationflags=NO_WINDOW))
        say("  throwaway server starting (first start reads every table: a few minutes)")
        end = time.time() + 600
        while "is listening at" not in read(os.path.join(logs, "gamesv.log")):
            if time.time() > end or any(pr.poll() is not None for pr in procs):
                raise Fail(f"the throwaway server did not start: see {logs}")
            time.sleep(2)
        say("  starting the client: it logs in, answers, quits by itself")
        start_client(p, f"-RbAccount={SETUP_ACCOUNT}", f"-RbExport={out}")
        wait_client(40)
        tags = tags_report(read(os.path.join(out, "rb_tags.txt")))
        parts = parts_report(read(os.path.join(out, "rb_parts.txt")))
        # A name the client does not know (id 0) is no remap row: counted, not a failure.
        if not tags or not parts or parts[0] or not parts[1]:
            raise Fail(f"run 2 incomplete (tags {tags}, parts {parts}): see {out}")
        for f in IN_GAME:
            shutil.copy2(os.path.join(out, f), os.path.join(p.beta, f))
        say(f"  GameplayTagRemap {tags[0]} rows ({tags[1]} names unknown to the client), "
            f"CharacterPartConfig {parts[1]} models")
        ok = True
    finally:
        for pr in procs:
            pr.kill()
        if ok:
            shutil.rmtree(tmp, ignore_errors=True)
        else:
            say(f"  kept {tmp} for a look")


@dataclass
class Stage:
    name: str
    done: Callable[[Paths], bool]
    run: Callable[[Paths, argparse.Namespace], None]


STAGES = [
    Stage("tools", done_tools, stage_tools),
    Stage("export", done_export, stage_export),
    Stage("refs", done_refs, stage_refs),
    Stage("generate", done_generate, stage_generate),
    Stage("extra", done_extra, stage_extra),
    Stage("paks", done_paks, stage_paks),
    Stage("ingame", done_ingame, stage_ingame),
]


def plan(p: Paths, start: str | None, only: str | None) -> list[tuple[Stage, bool]]:
    """Each stage and whether it runs: --only runs one; from --from on all run; before it, and
    without --from, a stage runs when its output is missing, and then so do the ones after it."""
    names = [s.name for s in STAGES]
    for n in (start, only):
        if n and n not in names:
            raise Fail(f"no stage {n}: {', '.join(names)}")
    out, forced = [], False
    for s in STAGES:
        if only:
            out.append((s, s.name == only))
            continue
        forced = forced or s.name == start or not s.done(p)
        out.append((s, forced))
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--lab", required=True)
    ap.add_argument("--game", required=True)
    ap.add_argument("--go", default=shutil.which("go"))
    ap.add_argument("--cargo", default=shutil.which("cargo"))
    ap.add_argument("--dotnet", default=shutil.which("dotnet"))
    g = ap.add_mutually_exclusive_group()
    g.add_argument("--from", dest="start")
    g.add_argument("--rebuild", action="store_true")
    g.add_argument("--only")
    a = ap.parse_args()
    if a.rebuild:
        a.start = STAGES[0].name
    p = Paths(os.path.abspath(a.lab), os.path.abspath(a.game))
    try:
        for tool in ("go", "cargo", "dotnet"):
            if not getattr(a, tool):
                raise Fail(f"{tool} not found")
        if not os.path.isfile(os.path.join(p.win64, CLIENT_EXE)):
            raise Fail(f"no 3.8 client at {p.game}")
        for s, go in plan(p, a.start, a.only):
            if not go:
                say(f"[{s.name}] already done")
                continue
            say(f"[{s.name}]")
            started = time.time()
            s.run(p, a)
            say(f"[{s.name}] done in {time.time() - started:.0f} s")
    except (Fail, OSError) as e:
        say(f"setup_data: {e}")
        return 1
    say("setup_data: game data ready in " + p.data)
    return 0


if __name__ == "__main__":
    sys.exit(main())
