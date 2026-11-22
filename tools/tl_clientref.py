#!/usr/bin/env python3
# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only
"""R-161 client source lookup (reference\\client-src-3.8, read only): who sends a request, who
handles a reply or notify, who reads a field, where a class or method is. Prints file:line and
the line, capped; full hits in out/tl/clientref.txt.

    python -I tools/tl_clientref.py senders GachaInfoRequest     where it is created and sent
    python -I tools/tl_clientref.py handlers TowerFloorUpdateNotify   Net.Register / response casts
    python -I tools/tl_clientref.py reads ShowStageImage         uses of a member (.Name / Name =)
    python -I tools/tl_clientref.py def GetGachaTextureInfo      class/method/property definitions
    python -I tools/tl_clientref.py grep 'REGEX'                 anything (rg syntax)

--all also searches the protocol and other assemblies (default: Aki.Game only). --max N lines.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_paths as P  # noqa: E402


def pattern(cmd: str, name: str) -> str:
    n = re.escape(name)
    return {
        "senders": rf"{n}\.Create\(\)|new {n}\(|ERequestMessageId\.{n}\b",
        "handlers": rf"ENotifyMessageId\.{n}\b|\({n}\)\s*msg|as {n}\b|ERequestMessageId\.{n.replace('Response', 'Request')}\b",
        "reads": rf"\.{n}\b|\b{n}\s*=[^=]",
        "def": rf"(class|struct|enum|interface)\s+{n}\b|\s{n}\s*\([^;]*\)\s*$|\s{n}\s*(=>|\{{)",
    }.get(cmd, name)


def rg(regex: str, everything: bool) -> list[str]:
    root = P.CLIENT_SRC if everything else P.CLIENT_SRC / "Aki.Game"
    # "--" before the pattern: file names in this tree start with '-' and break bare greps.
    r = subprocess.run(["rg", "-n", "--no-heading", "--glob", "*.cs", "-e", regex, "--", str(root)],
                       capture_output=True, text=True, encoding="utf-8", errors="replace")
    prefix = str(P.CLIENT_SRC) + "\\"
    return [l.replace(prefix, "") for l in r.stdout.splitlines()]


def main(argv: list[str]) -> int:
    P.utf8_stdout()
    everything = "--all" in argv
    argv = [a for a in argv if a != "--all"]
    cap = 25
    if "--max" in argv:
        i = argv.index("--max")
        cap = int(argv[i + 1])
        argv = argv[:i] + argv[i + 2:]
    if len(argv) != 2:
        print(__doc__)
        return 2
    hits = rg(pattern(argv[0], argv[1]), everything)
    out = P.out_file("clientref.txt")
    out.write_text("\n".join(hits), encoding="utf-8")
    files = sorted({h.split(":", 1)[0] for h in hits})
    print(f"{len(hits)} hits in {len(files)} files → {out}")
    for h in hits[:cap]:
        f, ln, text = (h.split(":", 2) + ["", ""])[:3]
        print(f"{f}:{ln}: {text.strip()[:180]}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
