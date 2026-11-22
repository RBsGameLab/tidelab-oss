# Client launcher and helper DLL

- Target client: `Client\Binaries\Win64\Client-Win64-Shipping.exe` (3.8 build).
- Windows: 11 Pro, x86-64.

This spec covers the client helper DLL (`tidelab_patch.dll`) and its launcher (`tidelab_start.exe`),
including the CPU load limiter, sampling profiler (`client/patch/src/profiler/`), clean exit watch
(`client/patch/src/exit.rs`), and inline hook infrastructure (`client/patch/src/hook.rs`,
`client/patch/src/gates.rs`).

## 1. The launcher (`tidelab_start.exe`)

Built as `tidelab_start.exe` (x86-64, Windows). Scripts start it from the client's directory with
`-krqlv=SD` (resource-quality level flag) and optional `-RbAccount=<account>`.

Steps, in order:

1. **Find the client.** Looks for `Client-Win64-Shipping.exe` in the launcher's own directory.
2. **Find the DLL.** Looks for `tidelab_patch.dll` in the same directory.
3. **Command line.** Builds a properly quoted Windows command line (`Client-Win64-Shipping.exe`
   followed by all arguments passed to the launcher).
4. **Create suspended process.** Calls `CreateProcessW` with `CREATE_SUSPENDED` and working
   directory set to the client binary directory.
5. **Load the DLL** into the suspended process via `LoadLibraryW` before the main thread executes.
6. **Resume** the client's main thread and exit with code 0.
7. **Errors** after process creation terminate the child process and report the failure.

## 2. The DLL entry (`tidelab_patch.dll`)

On `DLL_PROCESS_ATTACH` (loader thread, main thread still suspended):

1. Check the host executable name (`Client-Win64-Shipping.exe`, case-insensitive); do nothing in
   any other process.
2. Install the inline hooks before game code runs (`gates::install`).
3. Start the clean exit watch thread (`exit::start`): watches the visible `UnrealWindow` and
   ensures the client process exits cleanly after the window closes.
4. Start the sampling profiler (`profiler::start`).
5. Start the CPU load limiter, which lowers background CPU load once activated via its marker
   files.

## 3. Inline hooks (`hook.rs`)

- **Size:** 12-byte x86-64 absolute jump (`48 B8 <addr> FF E0`) fitting within 16-byte stubs.
- **Verification:** inspects the target prologue bytes before writing (RIP-relative jump stub for
  `kernel32` forwards; syscall stub prologue for `ntdll`) and refuses unknown layouts.
- **Trampoline:** copies non-RIP-relative prologue bytes into an executable trampoline page for
  thread-safe pass-through.

## 4. Runtime control and status files beside the DLL

| File | Purpose |
|---|---|
| `tidelab_ace.quiet` | Polled marker file: enables CPU load lowering for high-CPU background threads |
| `tidelab_ace.noload` | Read at attach: optional startup load-reduction mode |
| `tidelab_perfsight.quiet` | Polled marker file: lowers CPU load from telemetry worker threads |
| `tidelab_ace.status` | Rewritten periodically with runtime worker status |
| `tidelab_prof.on`, `tidelab_prof.status`, `tidelab_prof_<pid>_<n>.txt` | Sampling profiler control and output |
| `tidelab_dump.on`, `tidelab_dump.now`, `tidelab_dump_<pid>\` | Profiler memory dump control and output |

## 5. Tests (`cargo test`)

Unit tests cover launcher command-line quoting, binary path resolution, file-filter predicates,
hook prologue validation, in-process hook installation and release, exit-watch state transitions,
process exe-name checks, and CPU load-limiter sampling/backoff rules.
