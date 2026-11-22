# tidelab

An offline single-player server, Windows launcher, client helper library, and asset/protocol code
generators for a 3.8 game client.

> **Notice:** You must supply your own legally owned 3.8 game client and its extracted data files.
> This repository contains **no** game assets, protocol definitions, compiled tables, keys, or
> other game-derived files: everything the server needs is made from your own client. This project is an independent work and is not affiliated with, endorsed by,
> or connected to the game's developer or publisher.

## Layout

| Path | Description |
|---|---|
| `specs/` | Subsystem behaviour specifications |
| `server/` | Offline servers in Go: `cmd/loginsv`, `cmd/configsv`, `cmd/gamesv`, `cmd/robot`, `cmd/tables`, and packages under `internal/` |
| `client/` | Windows Rust workspace: `core` (paths, controls, auto-combat, server/game launch), `launcher` (GUI window), `start` (`tidelab_start.exe`), `patch` (`tidelab_patch.dll`: CPU load limiter, sampling profiler, clean exit watch) |
| `tools/Tidelab.ProtoGen` | C# generator that reads your client's `Aki.Protocol.dll` and emits `.proto` files and `schema.tsv` |
| `tools/Tidelab.ConfigExport` | C# generator that exports client `ConfigDB` SQLite tables to JSON and generates `server/internal/data/tables_gen.go` |
| `tools/Tidelab.Tools.Tests` | xUnit tests for the C# generators |
| `tools/cspatch`, `tools/rbauto` | C# client assembly patcher and helper (local login routing, sessions and config without keys, pak keys from the official CDN, auto-combat modes, telemetry/perf tools) |
| `tools/build_loginpak.py`, `tools/build_cspak.py`, `tools/pak_build.py` | Builders for local client configuration and C# helper `.pak` archives |
| `tools/export_panel_names.py` | Optional exporter for localized activity/item/banner display names used by the local web panel |
| `tools/tl_*.py` | Session log decoder, post-session audit, data query, and local test-server helpers |
| `tools/make_cfg.py` | Makes a new save directory's launcher config files from your client's cached manifests |

## Prerequisites

- **A legally owned 3.8 game client** (3.8.0 build 9244589). Nothing has to be extracted by hand:
  `setup.cmd` has the client copy out the files the server needs (see "Game data from your
  client" below).
- **Go 1.27+** (`setup.cmd` installs `protoc-gen-go` itself)
- No `protoc` to install: `setup.cmd` restores the NuGet package `Grpc.Tools`, which brings one
  (a `protoc` on `PATH` is used first)
- **About 6 GB free disk** (game data 3.2 GB, Rust build 0.8 GB)
- **.NET 10 SDK** (for `Tidelab.ProtoGen`, `Tidelab.ConfigExport`, `cspatch`, `rbauto`)
- **Rust stable** (Windows x86_64, for `client/`)
- **Python 3.11+** (for the `tools/*.py` utilities)
- **Internet access** at each game start: the launcher downloads the keys of some encrypted client
  paks from the official CDN itself, as the stock client does (see "No keys needed" below)
- The client must have been started **online once** (its resource manifests are then cached in
  `Client\Saved\Resources\3.8.0`; the local server does not serve them)

## Quick start (new machine)

1. Install the tools above. Close the game.
2. Run `setup.cmd`. It finds your client (or asks), writes `tidelab.local.txt`, creates a new save
   directory with config files made from your client's cache (`tools/make_cfg.py`), makes the game
   data from your client (`tools/setup_data.py`, about 30 minutes; the game starts twice by itself,
   accept the Windows UAC prompts), and builds the servers, the launcher and the two client paks.
   It asks for no key. If a step fails, fix it and run `setup.cmd` again: it goes on where it
   stopped (`setup.cmd -Rebuild` starts over).
3. Run `start-game.cmd`. It starts the servers, installs the paks beside the client and starts the
   game. Accept the Windows UAC prompt (the client needs administrator rights) and click to enter.
   The first account is created on the spot; the first player id is 666.

### No keys needed

The servers hold no secret of the client:

- **Game sessions**: without `<state>/gateway/client_public_key.der` the gateway answers
  `ProtoKeyResponse` type 0 and the session stays unencrypted; the C# pak switches the client's
  per-message cipher off accordingly (`specs/network.md` §5.1).
- **Config files**: without `<state>/cfg/key` and `iv` they go out as plain JSON; the C# pak reads
  plain JSON and passes anything else to the client's own decryption (`specs/config.md`).
- **Pak keys** (`pakchunk101`-`114`, `Video_*`): the C# pak adds the official CDN, which the client
  finds in the official config it fetches and decrypts itself, after the local server; the key
  files go from the official CDN to the client as they always do (`specs/config.md`).

A save directory from an older setup that has these key files keeps working with them.

### Game data from your client

`tools/setup_data.py` (run by `setup.cmd` when there is no game data) starts the client twice with
`-RbExport=<folder>`; the patch DLL then loads `tools/rbexport` (`tidelab_export.dll`) into the
client's own .NET runtime:

1. **Run 1** copies the client's own files out of its paks with the engine's file functions (script
   assemblies, `ConfigDB`, `Define`, behaviour trees, config ini files, the login pak's inputs)
   into `<data>/export`, then quits the client.
2. From those it builds the C# references (`backups/csref38`), the protocol and table code and
   `BinData` (`generate.cmd`, step 1 below), the extra tables in `BinData-beta`
   (`tools/extra_tables.py`), the quest data (`tools/questgen`) and the two client paks.
3. **Run 2** starts a throwaway server and save, lets the client log in, asks it for
   `GameplayTagRemap` and `CharacterPartConfig`, and quits it.

Each stage is skipped when its output is there. Two server tables, `CharacterInitInfo` and
`SkillBranchBuff`, are not in the client; they are optional (every role gets buffs worked out from
the `Buff` table, and there are no skill branches).

## 1. Generate protocol and data-table code

Because game-derived files (`server/protocol/proto/*.proto`, `server/protocol/schema.tsv`,
`server/protocol/*.pb.go`, `server/protocol/msgid.go`, and `server/internal/data/tables_gen.go`)
are omitted from this repository, they are generated from your own 3.8 client assets. `setup.cmd`
does this for you; by hand, from files exported as above:

```bat
generate.cmd -ProtocolDll <path\to\Aki.Protocol.dll> -Schema <path\to\Define> -ConfigDb <path\to\ConfigDB> -BinData <path\to\BinData> -LangDb <path\to\lang_multi_text.db>
```

If you already exported `BinData/` JSON files, you can omit `-ConfigDb`. If you configure
`tidelab.local.txt` (see §3), `generate.cmd` picks up the default paths under your data folder
automatically.

What `generate.cmd` (`tools/generate.ps1`) runs:
1. `Tidelab.ProtoGen` — inspects `Aki.Protocol.dll` metadata/IL and writes `server/protocol/proto/*.proto` and `server/protocol/schema.tsv`.
2. `server/tools/genproto` — invokes `protoc` + `protoc-gen-go` to compile `*.pb.go` and `msgid.go`.
3. `Tidelab.ConfigExport` — exports `ConfigDB/*.db` to `BinData/*.json` (if not already present) and generates `server/internal/data/tables_gen.go` from `specs/data-tables.tsv`.
4. `tools/export_panel_names.py` (optional) — exports `activity_names.tsv`, `item_names.tsv`, and `gacha_names.tsv` to `<BinData>/../panel/` for the local web panel (`gamesv` loads these at runtime and runs without them if absent).

## 2. Build and test

### Go servers (after step 1)

```bat
cd server
go vet ./...
go test ./...
go build -o ..\out\gobin\ .\cmd\...
```

### Windows launcher and client library (Rust)

```bat
cd client
cargo fmt --all --check
cargo clippy --all-targets --locked -- -D warnings
cargo test --locked
cargo build --release --locked
```

### Generators and Python tools

```bat
dotnet test tools\Tidelab.Tools.slnx -c Release
python -I tools\build_cspak_test.py
python -I tools\build_loginpak_test.py
python -I tools\make_cfg_test.py
python -I tools\tl_tools_test.py
```

### Client `.pak` files (required for a server without keys)

```bat
python -I tools\build_loginpak.py
python -I tools\build_cspak.py
```

## 3. Configure and run

No machine-specific paths are hardcoded. By default, scripts look for `..\tidelab-data` (extracted
game data and client under `Game38`) and `..\tidelab-state` (server save directory) beside the
repository root. Run `setup.cmd` or create a gitignored `tidelab.local.txt` at the repository root
with `key=value` overrides:

```ini
lab=D:\path\to\tidelab-data
game=D:\path\to\Game38
state=D:\path\to\tidelab-state
go=C:\path\to\go.exe
```

Root scripts:

| Script | Purpose |
|---|---|
| `generate.cmd` | Run the protocol, data-table, and optional panel-name generators against your own 3.8 client assets |
| `setup.cmd` | First-time setup: `tidelab.local.txt`, toolchain checks, a new save directory with config files from your client's cache, builds (asks only for folders it cannot find) |
| `start-server.cmd` | Build `server/` into `out\gobin` and start `configsv`, `loginsv`, and `gamesv` (logs in `out\logs\`) |
| `restart-server.cmd` | Stop and restart the three server processes |
| `start-game.cmd` | Start the server if needed, copy local paks and Rust launcher/DLL beside the client, and launch the 3.8 client |
| `open-panel.cmd` | Open the local web content panel served by `gamesv` at `http://127.0.0.1:13180/` |
| `audit.cmd` | Post-session log audit (writes report to `out\audit\`) |

You can also run `client\target\release\tidelab-launcher.exe` for a desktop GUI managing the
server, client launch, runtime controls, and auto-combat mode.

### Server CLI flags

All server binaries accept `--name value` flags:

- `loginsv`: `--listen_address` (default `127.0.0.1:10001`), `--gateway_address` (default `127.0.0.1:13100`), `--state_dir` (default `state`)
- `configsv`: `--listen_address` (default `127.0.0.1:5500`), `--state_dir` (default `state`)
- `gamesv`: `--listen_address` (default `127.0.0.1:13100`, UDP), `--state_dir`, `--bindata`, `--more_bindata` (`;`-separated), `--bin_cache`, `--quest_data`, `--panel_names`, `--cmd_file`. Key files in `<state_dir>` are optional (see "No keys needed"); `configsv` needs `<state_dir>/cfg/index`, which `setup.cmd` makes with `tools/make_cfg.py`.

## 4. Commands and troubleshooting

### Commands in the game

Your friend list has a friend **Tidelab** (id 1337): open a private chat with it and type a
command; it answers in the chat. `help` lists every command, `help <command>` explains one.
Several commands can go in one message, separated by `;`.

When the chat cannot be reached, write the command as a line into `cmd.txt` in your save directory
(`<state>\cmd.txt`): the server reads it within a few seconds while you are in the game and empties
the file.

### A quest is stuck

1. `quest` shows the running quests and the step each one waits on.
2. `quest next` completes the waiting steps. Send it again if the next step is stuck too.
3. `quest start <quest id>` starts a quest that never began.
4. Stuck in a place: `goto <x> <y> <z>` (centimetres, current map), `find chest` or `find door`
   (next to the nearest unopened one); stuck in a dungeon: `dungeon leave`, or `dungeon killall`
   when the room waits for monsters that never come.

Please report the quest id that `quest` shows and what the game showed, so the step can be fixed.

### Problems we met, and what to do

- **The game freezes on the start screen and closes after about 4 minutes** (`Client.log`:
  "Hang detected on GameThread"): `tidelab_ace.quiet` was left beside the client exe
  (`Client\Binaries\Win64`). The C# pak creates it after the first world load; `start-game.cmd`,
  the launcher and `setup.cmd` delete it before each start. If you start the client another way,
  delete it first.
- **Nothing starts, "error 740" / "the requested operation requires elevation"**:
  `tidelab_start.exe` needs administrator rights. Start the game with `start-game.cmd` or the
  launcher and accept the Windows prompt.
- **`Permission denied` reading what the client wrote**: the client runs as administrator, so files
  it writes into a folder only your user may use cannot be read back. `setup.cmd` uses folders under
  the data directory for this.
- **`setup.cmd` stopped half way** (closed window, low memory, a failed step): run it again; it goes
  on where it stopped. `setup.cmd -Rebuild` makes the game data again from the start. Close big
  programs (a browser with many tabs) first if the PC has little memory.
- **Your antivirus reports `tidelab_start.exe` or `tidelab_patch.dll`**: they start the game
  suspended and load a DLL into it, which is what some malware does too. They are built on your
  machine from the source in `client/`; read it, and allow them only if you trust it.
- **No skill branches, role buffs slightly different from the official game**: two server tables
  (`CharacterInitInfo`, `SkillBranchBuff`) are not in the client; see "Game data from your client".

## Continuous Integration

Public CI (`.github/workflows/build.yml`) runs:
- **Rust (`client/`)** on Windows: `cargo fmt --all --check`, `cargo clippy --all-targets --locked -- -D warnings`, `cargo test --locked`, and `cargo build --release --locked`.
- **Python tool tests (`tools/`)** on Linux: `build_cspak_test.py`, `build_loginpak_test.py`, `make_cfg_test.py`, and `tl_tools_test.py`.

Go server compilation and tests are **skipped in public CI** because they depend on the protocol
and data-table Go files generated in step 1 from the user's own 3.8 client. Run `generate.cmd`
followed by `cd server && go test ./...` locally to verify the Go server.

## License

Copyright (C) 2026 RBsGameLab.

Licensed under the **GNU Affero General Public License v3.0 (AGPL-3.0)**. See [LICENSE](LICENSE)
and [NOTICE](NOTICE) for details.
