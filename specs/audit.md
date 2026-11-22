# Audit after a play session (R-166)

`audit.cmd` → `tools/tl_audit.py`: after one real play session, list everything the session
touched that tidelab has not finished, all systems. Logs only, nothing made up; a robot that
plays for the audit is a later request.

## 1. Inputs

- Client: the last `Client.log` (`<game>\Client\Saved\Logs`, one per game start), decoded to
  `out/tl/audit_client_decoded.log`. Its first and last time stamps are the session.
- Server: `out\logs\gamesv.log`, `gamesv_prev.log` and `archive\gamesv-*.log` of the main
  worktree. Only lines stamped inside the session ± 1 minute count; a line found in two files
  counts once. Logs written before R-166 have no stamps: the report says the server side is empty.
- gamesv stamps every line `YYYY-MM-DD HH:MM:SS.mmm level(scope): message` (local time;
  `common.LogClock`, set only in `cmd/gamesv`; tests and robots keep `level(scope): message`).
- Before a start overwrites a server log, `start-server.cmd` (`tools/archive_logs.ps1`) and the
  launcher (`steps.rs` `archive_log`) move it to `out\logs\archive\<name>-<modified unix s>.log`
  and keep the newest 30 per server.

## 2. Checks

| # | What | Source | Severe |
|---|---|---|---|
| 1 | Combat requests left unanswered | `info(combat): unhandled combat message: XRequest` | red |
| 2 | Stuck monsters | client `[FightState]`/`[Skill]` lines of `Monster` entities, below | red |
| 3 | Client exceptions, callback exceptions, `怪物状态出问题`, `等待实体超时` | client errors | red |
| 4 | Requests answered empty (handler missing), by game system | `warning(connection): no handler for X (msg_id: N), replied empty` | |
| 5 | Pushes dropped, by game system | `no handler for X (msg_id: N)` and `unhandled combat message: XPush` | |
| 6 | Replies that are not Success | `warning(reply): X answered CODE (N)`: gateway for top-level replies, `fight.go` for combat responses (`protocol.ErrorCodeOf`: first top-level ErrorCode field, or the one of the message set in a oneof) | |
| 7 | Other server warnings/errors, by scope | `warning(...)`/`error(...)` | |
| 8 | Client C# errors by system (`[C#][n][Error][Tag]`), engine errors (`LogRHI` …) apart | client errors (`tl_clientlog.ERROR`) | |

- Stuck monster, per entity: a hit span runs from a successful main state whose name has `受击`
  (and not `技能`) to the next main state without it; a skill span from the first
  `技能释放失败` to the next successful main state with `技能`. A span still open at the
  monster's last line ends there (died or the log ended). Reported: hit > 10 s, skill > 15 s
  (R-164's tower boss: 42 s). The summary shows a freeze once (hit and skill span together).
- Game system of a request/push: grouped by the client module that sends it; Chinese names in
  `SYSTEM_ZH`; cached in `out/tl/audit_senders.tsv`.
- Groups: numbers become `#` and entity names `[kind]`; a few distinct values (first
  `[name: value]` field with a digit, not a handle) are kept as examples.

## 3. Output

- Console: a short Chinese summary, severe lines red; the last line names the report.
- `out\audit\<client log end>.md`: every group in English with counts and examples.
- Noise: `tools/tl_audit_ignore.txt`, one regex per line, matched against a group's text or a
  request/monster name; the user picks the lines after reading a report.

## 4. Combat requests the client waits on

`specs/combat-client-calls.tsv`: the 14 combat requests the client sends and waits on for a
response, refreshed by `python -I tools/tl_audit.py combat-calls`. Go test
`TestEveryCombatRequestTheClientWaitsOnIsAnswered` sends each one and fails when no response
comes back.
