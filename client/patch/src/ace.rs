// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Lower CPU load in the local single-player client.
//!
//! Polls marker files beside the DLL (`tidelab_ace.quiet`, `tidelab_ace.noload`,
//! `tidelab_perfsight.quiet`) and lowers CPU load from busy background threads.
//! Status is written to `tidelab_ace.status`.

use std::ffi::c_void;
use std::fmt::Write as _;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicUsize, Ordering};

use windows_sys::Win32::Foundation::{
    CloseHandle, ERROR_MOD_NOT_FOUND, FILETIME, HANDLE, INVALID_HANDLE_VALUE, LocalFree,
    SetLastError,
};
use windows_sys::Win32::System::Diagnostics::Debug::{
    CONTEXT, CONTEXT_CONTROL_AMD64, GetThreadContext,
};
use windows_sys::Win32::System::Diagnostics::ToolHelp::{
    CreateToolhelp32Snapshot, TH32CS_SNAPTHREAD, THREADENTRY32, Thread32First, Thread32Next,
};
use windows_sys::Win32::System::ProcessStatus::{
    K32EnumProcessModules, K32GetModuleBaseNameW, K32GetModuleInformation, MODULEINFO,
};
use windows_sys::Win32::System::SystemInformation::GetTickCount64;
use windows_sys::Win32::System::Threading::{
    GetCurrentProcess, GetCurrentProcessId, GetCurrentThread, GetCurrentThreadId,
    GetThreadDescription, GetThreadId, GetThreadTimes, OpenThread, ResumeThread, Sleep,
    SuspendThread, THREAD_GET_CONTEXT, THREAD_QUERY_INFORMATION, THREAD_SUSPEND_RESUME,
};

use crate::beside;
use crate::gates;
use crate::hook;
use crate::nt;

const MAX_MODULES: usize = 1024;

/// Lower CPU load.
/// Lower CPU load.
/// starting up, and freezing one of those mid-lock is the way to wedge the client. The burner
/// passes this within a second or two of its life.
const MIN_CPU_MS: u64 = 1000;

/// Lower CPU load.
/// last poll) before it is sampled at all, and before the samples can condemn it.
const HOT_RATE_PCT: u32 = 15;

/// Lower CPU load.
/// Lower CPU load.
/// Lower CPU load.
const ACE_HITS_NEEDED: u32 = 5;
const MIN_SAMPLES: u32 = 6;

/// A PerfSight thread is suspended once it has run this long: past its start-up, so it is not
/// frozen while it still sets itself up.
const PERFSIGHT_MIN_CPU_MS: u64 = 200;

/// Poll every second while something may need a verdict, else every 5 s (R-20b).
const FAST_POLL_MS: u32 = 1000;
const SLOW_POLL_MS: u32 = 5000;
/// Lower CPU load.
const WARMUP_POLLS: u32 = 60;
/// Lower CPU load.
const ACE_BUSY_PCT: u32 = 5;

const THREAD_ACCESS: u32 = THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT | THREAD_QUERY_INFORMATION;

pub const QUIET: &str = "tidelab_ace.quiet";
pub const NOLOAD: &str = "tidelab_ace.noload";
pub const PERFSIGHT_QUIET: &str = "tidelab_perfsight.quiet";
pub const STATUS: &str = "tidelab_ace.status";

// ---------------------------------------------------------------------------------------------
// Log: lines for the status file. Reached from the poller and, later, from the loader gate on
// the game's threads; held only for a copy.
// ---------------------------------------------------------------------------------------------

const LOG_MAX: usize = 24 * 1024;

struct Log {
    text: String,
    cut: u32,
}

static LOG: Mutex<Log> = Mutex::new(Log {
    text: String::new(),
    cut: 0,
});

impl Log {
    fn push(&mut self, line: &str) {
        if self.text.len() + line.len() + 2 > LOG_MAX {
            self.cut += 1;
            return;
        }
        self.text.push_str(line);
        self.text.push_str("\r\n");
    }
}

fn log(line: String) {
    if let Ok(mut l) = LOG.lock() {
        l.push(&line);
    }
}

// ---------------------------------------------------------------------------------------------
// Entry
// ---------------------------------------------------------------------------------------------

/// Called on attach, on the loader thread. Starts the poller in the game's process only.
pub fn init(module: *mut c_void) {
    if !beside::in_client() || !beside::set_dir(module as usize) {
        return;
    }
    // SAFETY: plain query.
    let pid = unsafe { GetCurrentProcessId() };
    let noload = beside::exists(NOLOAD);
    log(format!("armed in pid {pid}; noload {noload}"));
    if noload {
        // Here, on the loader thread: by the time a spawned thread ran, the client might
        // Lower CPU load.
        install_load_gates();
    }
    let module = module as usize;
    let spawned = std::thread::Builder::new()
        .name("tidelab ACE poller".into())
        .spawn(move || {
            let mut w = Watch::new(module, pid, noload);
            w.run();
        });
    if let Err(e) = spawned {
        log(format!(
            "the poller did not start ({e}); tidelab_ace.quiet will not work"
        ));
    }
}

/// A line for the status file, from the gates.
pub(crate) fn note(line: &str) {
    log(line.to_string());
}

// ---------------------------------------------------------------------------------------------
// Lower CPU load.
// ---------------------------------------------------------------------------------------------

static LOAD_GATES: AtomicBool = AtomicBool::new(false);
static BLOCKED_LOADS: AtomicU32 = AtomicU32::new(0);
/// kernelbase's copies, where the gates pass a call on (kernel32's are stubs that jump there).
static LOAD_W_PASS: AtomicUsize = AtomicUsize::new(0);
static LOAD_EX_W_PASS: AtomicUsize = AtomicUsize::new(0);

type LoadLibraryWFn = unsafe extern "system" fn(*const u16) -> *mut c_void;
type LoadLibraryExWFn = unsafe extern "system" fn(*const u16, HANDLE, u32) -> *mut c_void;

fn install_load_gates() {
    let found = (
        gates::export("kernel32.dll", c"LoadLibraryW"),
        gates::export("kernel32.dll", c"LoadLibraryExW"),
        gates::export("kernelbase.dll", c"LoadLibraryW"),
        gates::export("kernelbase.dll", c"LoadLibraryExW"),
    );
    let (Some(k32_w), Some(k32_ex_w), Some(kb_w), Some(kb_ex_w)) = found else {
        log("LoadLibraryW/LoadLibraryExW not found; noload is off".into());
        return;
    };
    if k32_w == kb_w || k32_ex_w == kb_ex_w {
        log("kernel32 and kernelbase share LoadLibrary on this Windows; not hooking".into());
        return;
    }
    LOAD_W_PASS.store(kb_w, Ordering::SeqCst);
    LOAD_EX_W_PASS.store(kb_ex_w, Ordering::SeqCst);
    // SAFETY: kernel32 exports; the detours have their signatures.
    if let Err(e) = unsafe {
        hook::install(
            k32_ex_w as *mut u8,
            load_library_ex_w_gate as *const () as usize,
        )
    } {
        log(format!(
            "could not hook LoadLibraryExW ({e}); noload is off"
        ));
        return;
    }
    // SAFETY: as above.
    if let Err(e) =
        unsafe { hook::install(k32_w as *mut u8, load_library_w_gate as *const () as usize) }
    {
        log(format!(
            "could not hook LoadLibraryW ({e}); only LoadLibraryExW is gated"
        ));
    }
    LOAD_GATES.store(true, Ordering::SeqCst);
    log(format!(
        "noload armed: LoadLibraryW at 0x{k32_w:X}, LoadLibraryExW at 0x{k32_ex_w:X}"
    ));
}

/// # Safety
/// `name` is null or NUL-terminated.
unsafe fn refuses(api: &str, name: *const u16) -> bool {
    if name.is_null() {
        return false;
    }
    let mut n = 0;
    // SAFETY: stops at the NUL.
    let path = unsafe {
        while *name.add(n) != 0 {
            n += 1;
        }
        std::slice::from_raw_parts(name, n)
    };
    if !is_ace_path(path) {
        return false;
    }
    BLOCKED_LOADS.fetch_add(1, Ordering::SeqCst);
    let full = String::from_utf16_lossy(path);
    log(format!("blocked {api}(\"{}\")", beside::basename(&full)));
    // SAFETY: plain call.
    unsafe { SetLastError(ERROR_MOD_NOT_FOUND) };
    true
}

unsafe extern "system" fn load_library_w_gate(name: *const u16) -> *mut c_void {
    // SAFETY: the loader's own argument.
    if unsafe { refuses("LoadLibraryW", name) } {
        return std::ptr::null_mut();
    }
    // SAFETY: kernelbase!LoadLibraryW, same signature, set before the hook went in.
    unsafe {
        let pass: LoadLibraryWFn = std::mem::transmute(LOAD_W_PASS.load(Ordering::SeqCst));
        pass(name)
    }
}

unsafe extern "system" fn load_library_ex_w_gate(
    name: *const u16,
    file: HANDLE,
    flags: u32,
) -> *mut c_void {
    // SAFETY: the loader's own argument.
    if unsafe { refuses("LoadLibraryExW", name) } {
        return std::ptr::null_mut();
    }
    // SAFETY: kernelbase!LoadLibraryExW, same signature, set before the hook went in.
    unsafe {
        let pass: LoadLibraryExWFn = std::mem::transmute(LOAD_EX_W_PASS.load(Ordering::SeqCst));
        pass(name, file, flags)
    }
}

/// Told which processes the client starts (by the process gate), so the status file
/// Lower CPU load.
pub fn note_process(image_name: &str) {
    let base = beside::basename(image_name);
    if starts_with_ignore_case(base, "ace-") {
        log(format!("the client starts {base}"));
    }
}

/// Lower CPU load.
pub fn is_ace_path(path: &[u16]) -> bool {
    let cut = path
        .iter()
        .rposition(|&c| c == u16::from(b'\\') || c == u16::from(b'/'))
        .map_or(0, |i| i + 1);
    let base = &path[cut..];
    let prefix = b"ace-";
    base.len() >= prefix.len()
        && base
            .iter()
            .zip(prefix)
            .all(|(&got, &want)| got < 0x80 && (got as u8).to_ascii_lowercase() == want)
}

fn starts_with_ignore_case(s: &str, prefix: &str) -> bool {
    s.len() >= prefix.len() && s.as_bytes()[..prefix.len()].eq_ignore_ascii_case(prefix.as_bytes())
}

// ---------------------------------------------------------------------------------------------
// Modules
// ---------------------------------------------------------------------------------------------

#[derive(Debug, Clone, Default)]
struct Mod {
    start: usize,
    end: usize,
    name: String,
    is_ace: bool,
    is_perfsight: bool,
}

impl Mod {
    fn new(start: usize, end: usize, name: &str) -> Self {
        Mod {
            start,
            end,
            name: name.chars().take(40).collect(),
            is_ace: starts_with_ignore_case(name, "ace-"),
            is_perfsight: name.eq_ignore_ascii_case("perfsight.dll"),
        }
    }
}

#[derive(Debug, Default)]
struct Mods {
    list: Vec<Mod>,
    exe: Option<usize>,
    this: Option<usize>,
    last_handles: Vec<usize>,
    refreshes: u32,
}

impl Mods {
    fn at(&self, addr: usize) -> Option<&Mod> {
        self.list.iter().find(|m| addr >= m.start && addr < m.end)
    }

    fn index_at(&self, addr: usize) -> Option<usize> {
        self.list
            .iter()
            .position(|m| addr >= m.start && addr < m.end)
    }

    fn ace_loaded(&self) -> bool {
        self.list.iter().any(|m| m.is_ace)
    }

    /// Resolving a module walks the loader list again for every call, so the table is rebuilt
    /// only when the list of handles changed (R-20b).
    fn refresh(&mut self, self_module: usize) {
        let mut handles = vec![std::ptr::null_mut::<c_void>(); MAX_MODULES];
        let mut needed = 0u32;
        // SAFETY: the buffer holds MAX_MODULES handles.
        let ok = unsafe {
            K32EnumProcessModules(
                GetCurrentProcess(),
                handles.as_mut_ptr(),
                size_of_val(handles.as_slice()) as u32,
                &mut needed,
            )
        };
        if ok == 0 {
            return;
        }
        let n = (needed as usize / size_of::<usize>()).min(MAX_MODULES);
        let handles: Vec<usize> = handles[..n].iter().map(|&h| h as usize).collect();
        if handles == self.last_handles {
            return;
        }
        self.last_handles = handles;
        self.refreshes += 1;
        self.list.clear();
        self.exe = None;
        self.this = None;
        for &hm in &self.last_handles {
            let mut info = MODULEINFO {
                lpBaseOfDll: std::ptr::null_mut(),
                SizeOfImage: 0,
                EntryPoint: std::ptr::null_mut(),
            };
            let mut wname = [0u16; 260];
            // SAFETY: module handles of this process; buffers as declared.
            let (ok, wl) = unsafe {
                let ok = K32GetModuleInformation(
                    GetCurrentProcess(),
                    hm as _,
                    &mut info,
                    size_of::<MODULEINFO>() as u32,
                );
                (
                    ok,
                    K32GetModuleBaseNameW(
                        GetCurrentProcess(),
                        hm as _,
                        wname.as_mut_ptr(),
                        wname.len() as u32,
                    ),
                )
            };
            if ok == 0 || wl == 0 {
                continue;
            }
            let name = beside::narrow(&wname[..wl as usize]);
            let base = info.lpBaseOfDll as usize;
            if name.eq_ignore_ascii_case(beside::CLIENT_EXE) {
                self.exe = Some(self.list.len());
            }
            if hm == self_module {
                self.this = Some(self.list.len());
            }
            self.list
                .push(Mod::new(base, base + info.SizeOfImage as usize, &name));
        }
    }
}

// ---------------------------------------------------------------------------------------------
// Threads
// ---------------------------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
enum Reason {
    #[default]
    None,
    AceStart,
    AceCode,
    Perfsight,
}

#[derive(Debug, Clone, Default)]
struct Thread {
    /// 0 = a free slot: dead threads are forgotten so the table never grows without bound.
    tid: u32,
    handle: usize,
    alive: bool,
    named: bool,
    judged: bool,
    start_in_ace: bool,
    start_in_perfsight: bool,
    start_in_exe: bool,
    has_cpu: bool,
    start_name: String,
    start_off: usize,
    last_cpu: u64,
    cpu: u64,
    rate_pct: u32,
    samples: u32,
    ace_hits: u32,
    /// Lower CPU load.
    recent: u8,
    recent_len: u32,
    rip_name: String,
    rip_off: usize,
    suspended: bool,
    reason: Reason,
    /// SuspendThread failed once for it: logged once, with the error, not every poll.
    suspend_failed: bool,
}

impl Thread {
    /// Keep the verdicts of the last 8 samples, and with them the count that condemns a thread.
    fn note_sample(&mut self, in_ace: bool) {
        self.samples += 1;
        if self.recent_len == 8 {
            if self.recent & 0x80 != 0 {
                self.ace_hits -= 1;
            }
        } else {
            self.recent_len += 1;
        }
        self.recent <<= 1;
        if in_ace {
            self.recent |= 1;
            self.ace_hits += 1;
        }
    }

    /// Lower CPU load.
    /// Lower CPU load.
    fn cleared(&self) -> bool {
        self.recent_len == 8 && self.ace_hits == 0
    }

    fn h(&self) -> HANDLE {
        self.handle as HANDLE
    }
}

#[derive(Debug, Default)]
struct Threads {
    slots: Vec<Thread>,
}

impl Threads {
    fn find(&mut self, tid: u32) -> Option<usize> {
        self.slots.iter().position(|t| t.tid == tid)
    }

    /// Takes over `handle` (closed when the thread is forgotten). Reuses a free slot first.
    fn add(&mut self, tid: u32, handle: usize) -> usize {
        let t = Thread {
            tid,
            handle,
            ..Thread::default()
        };
        if let Some(i) = self.slots.iter().position(|s| s.tid == 0) {
            self.slots[i] = t;
            i
        } else {
            self.slots.push(t);
            self.slots.len() - 1
        }
    }

    /// Threads that were not seen in this poll have exited: close their handles, free the slots.
    fn forget_dead(&mut self) {
        for t in &mut self.slots {
            if t.tid == 0 || t.alive {
                continue;
            }
            if t.handle != 0 {
                // SAFETY: a handle we opened.
                unsafe { CloseHandle(t.h()) };
            }
            *t = Thread::default();
        }
    }

    /// Is anything left that a poll a second from now could act on?
    fn need_fast_poll(&self, quiet: bool, perf_quiet: bool) -> bool {
        self.slots.iter().any(|t| {
            t.tid != 0
                && t.alive
                && !t.suspended
                && ((t.start_in_ace && quiet && t.rate_pct >= ACE_BUSY_PCT)
                    || (t.start_in_perfsight && perf_quiet && t.rate_pct > 0)
                    || (t.samples > 0 && t.rate_pct >= HOT_RATE_PCT && !t.cleared()))
        })
    }
}

/// Percent of one core: `delta` is CPU time in 100 ns units over `elapsed_ms` of wall time.
fn rate_pct(delta: u64, elapsed_ms: u64) -> u32 {
    (delta / (elapsed_ms.max(1) * 100)).min(1000) as u32
}

fn file_time(ft: FILETIME) -> u64 {
    (u64::from(ft.dwHighDateTime) << 32) | u64::from(ft.dwLowDateTime)
}

/// Kernel plus user time of a thread, in 100 ns units.
pub(crate) fn thread_cpu(h: HANDLE) -> u64 {
    let z = FILETIME {
        dwLowDateTime: 0,
        dwHighDateTime: 0,
    };
    let (mut c, mut e, mut k, mut u) = (z, z, z, z);
    // SAFETY: out-pointers to locals.
    if unsafe { GetThreadTimes(h, &mut c, &mut e, &mut k, &mut u) } == 0 {
        return 0;
    }
    file_time(k) + file_time(u)
}

// ---------------------------------------------------------------------------------------------
// The poller
// ---------------------------------------------------------------------------------------------

struct Watch {
    self_module: usize,
    pid: u32,
    self_tid: u32,
    noload: bool,
    quiet: bool,
    perf_quiet: bool,
    polls: u32,
    fast: bool,
    toolhelp_fallbacks: u32,
    mods: Mods,
    threads: Threads,
    ctx: Box<CONTEXT>,
    poller_cpu: u64,
    poller_window_cpu: u64,
    poller_window_tick: u64,
    /// Hundredths of a percent of one core over the last full minute.
    poller_last_minute_pct100: u64,
    poller_minutes: u32,
}

impl Watch {
    fn new(self_module: usize, pid: u32, noload: bool) -> Self {
        Watch {
            self_module,
            pid,
            self_tid: 0,
            noload,
            quiet: false,
            perf_quiet: false,
            polls: 0,
            fast: true,
            toolhelp_fallbacks: 0,
            mods: Mods::default(),
            threads: Threads::default(),
            // SAFETY: CONTEXT is plain data.
            ctx: Box::new(unsafe { std::mem::zeroed() }),
            poller_cpu: 0,
            poller_window_cpu: 0,
            poller_window_tick: 0,
            poller_last_minute_pct100: 0,
            poller_minutes: 0,
        }
    }

    fn run(&mut self) {
        // SAFETY: plain queries.
        self.self_tid = unsafe { GetCurrentThreadId() };
        let mut last_tick = unsafe { GetTickCount64() };
        loop {
            self.mods.refresh(self.self_module);
            let (was_quiet, was_perf_quiet) = (self.quiet, self.perf_quiet);
            self.quiet = beside::exists(QUIET);
            self.perf_quiet = beside::exists(PERFSIGHT_QUIET);
            // SAFETY: plain query.
            let now = unsafe { GetTickCount64() };
            self.poll(now.wrapping_sub(last_tick).max(1));
            last_tick = now;
            self.polls += 1;
            self.fast = self.polls < WARMUP_POLLS
                || was_quiet != self.quiet
                || was_perf_quiet != self.perf_quiet
                || self.threads.need_fast_poll(self.quiet, self.perf_quiet);
            self.note_poller_cpu(now);
            beside::write(STATUS, self.status().as_bytes());
            // SAFETY: plain sleep.
            unsafe {
                Sleep(if self.fast {
                    FAST_POLL_MS
                } else {
                    SLOW_POLL_MS
                })
            };
        }
    }

    /// What the poller itself costs, for the status file: CPU over the last full minute.
    fn note_poller_cpu(&mut self, now: u64) {
        // SAFETY: the pseudo handle of this thread.
        self.poller_cpu = thread_cpu(unsafe { GetCurrentThread() });
        if self.poller_window_tick == 0 {
            self.poller_window_tick = now;
            self.poller_window_cpu = self.poller_cpu;
            return;
        }
        let span = now.wrapping_sub(self.poller_window_tick);
        if span < 60_000 {
            return;
        }
        // 100 ns units -> hundredths of a percent: cpu * 100 * 100 / (span_ms * 10_000)
        self.poller_last_minute_pct100 =
            self.poller_cpu.wrapping_sub(self.poller_window_cpu) / span;
        self.poller_window_tick = now;
        self.poller_window_cpu = self.poller_cpu;
        self.poller_minutes += 1;
    }

    /// Walks this process' threads with NtGetNextThread, which hands out a handle per thread
    /// without a system-wide snapshot (R-20b). Falls back to Toolhelp if it yields nothing.
    fn poll(&mut self, elapsed_ms: u64) {
        for t in &mut self.threads.slots {
            t.alive = false;
        }
        let mut seen = 0u32;
        let mut cur: HANDLE = std::ptr::null_mut();
        let mut cur_kept = false;
        loop {
            let mut next: HANDLE = std::ptr::null_mut();
            // SAFETY: walks this process' threads; `cur` is the previous handle or null.
            let status = unsafe {
                nt::NtGetNextThread(GetCurrentProcess(), cur, THREAD_ACCESS, 0, 0, &mut next)
            };
            if !cur.is_null() && !cur_kept {
                // SAFETY: a handle NtGetNextThread gave us and nobody kept.
                unsafe { CloseHandle(cur) };
            }
            if status < 0 || next.is_null() {
                break;
            }
            cur = next;
            cur_kept = false;
            seen += 1;
            // SAFETY: a valid thread handle.
            let tid = unsafe { GetThreadId(next) };
            if tid == 0 || tid == self.self_tid {
                continue;
            }
            let i = match self.threads.find(tid) {
                Some(i) => i,
                None => {
                    cur_kept = true;
                    self.threads.add(tid, next as usize)
                }
            };
            self.threads.slots[i].alive = true;
            self.visit(i, elapsed_ms);
        }
        if seen == 0 {
            self.poll_toolhelp(elapsed_ms);
        }
        self.threads.forget_dead();
    }

    fn poll_toolhelp(&mut self, elapsed_ms: u64) {
        self.toolhelp_fallbacks += 1;
        // SAFETY: closed below.
        let snap = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0) };
        if snap == INVALID_HANDLE_VALUE {
            return;
        }
        // SAFETY: plain data.
        let mut e: THREADENTRY32 = unsafe { std::mem::zeroed() };
        e.dwSize = size_of::<THREADENTRY32>() as u32;
        // SAFETY: `e` is sized.
        let mut ok = unsafe { Thread32First(snap, &mut e) };
        while ok != 0 {
            let tid = e.th32ThreadID;
            if e.th32OwnerProcessID == self.pid && tid != self.self_tid {
                let i = match self.threads.find(tid) {
                    Some(i) => i,
                    // SAFETY: plain open; the handle is kept in the table.
                    None => self
                        .threads
                        .add(tid, unsafe { OpenThread(THREAD_ACCESS, 0, tid) } as usize),
                };
                self.threads.slots[i].alive = true;
                self.visit(i, elapsed_ms);
            }
            // SAFETY: as above.
            ok = unsafe { Thread32Next(snap, &mut e) };
        }
        // SAFETY: opened above.
        unsafe { CloseHandle(snap) };
    }

    fn judge(&mut self, i: usize) {
        let h = self.threads.slots[i].h();
        let mut start_addr = 0usize;
        // SAFETY: ThreadQuerySetWin32StartAddress into a usize.
        let st = unsafe {
            nt::NtQueryInformationThread(
                h,
                nt::THREAD_QUERY_SET_WIN32_START_ADDRESS,
                (&raw mut start_addr).cast(),
                size_of::<usize>() as u32,
                std::ptr::null_mut(),
            )
        };
        if st < 0 {
            return;
        }
        let module = self.mods.index_at(start_addr);
        let t = &mut self.threads.slots[i];
        t.judged = true;
        if let Some(mi) = module {
            let m = &self.mods.list[mi];
            t.start_in_ace = m.is_ace;
            t.start_in_perfsight = m.is_perfsight;
            t.start_in_exe = Some(mi) == self.mods.exe || Some(mi) == self.mods.this;
            t.start_name = m.name.clone();
            t.start_off = start_addr - m.start;
        }
        let mut w: *mut u16 = std::ptr::null_mut();
        // SAFETY: the description is freed with LocalFree.
        if unsafe { GetThreadDescription(h, &mut w) } >= 0 && !w.is_null() {
            // SAFETY: a NUL-terminated string from the system.
            t.named = unsafe { *w } != 0;
            unsafe { LocalFree(w.cast()) };
        }
    }

    /// Where is this thread executing right now? Suspend, take the instruction pointer, resume,
    /// and only then look the address up: while a thread is suspended nothing that takes a lock
    /// may be called, or the client deadlocks if the thread held that lock (R-15).
    fn sample_rip(&mut self, i: usize) {
        let h = self.threads.slots[i].h();
        // SAFETY: a thread handle with suspend and context access. Between suspend and resume
        // only GetThreadContext runs, into a buffer allocated beforehand.
        let rip = unsafe {
            if SuspendThread(h) == u32::MAX {
                return;
            }
            self.ctx.ContextFlags = CONTEXT_CONTROL_AMD64;
            let got = GetThreadContext(h, &mut *self.ctx) != 0;
            let rip = self.ctx.Rip as usize;
            ResumeThread(h);
            if !got {
                return;
            }
            rip
        };
        let t = &mut self.threads.slots[i];
        let in_ace = match self.mods.at(rip) {
            Some(m) => {
                t.rip_name = m.name.clone();
                t.rip_off = rip - m.start;
                m.is_ace
            }
            None => {
                t.rip_name = "(no module)".into();
                t.rip_off = rip;
                false
            }
        };
        t.note_sample(in_ace);
    }

    fn suspend(&mut self, i: usize, reason: Reason) {
        let t = &mut self.threads.slots[i];
        // SAFETY: a thread handle with suspend access.
        if unsafe { SuspendThread(t.h()) } == u32::MAX {
            if !t.suspend_failed {
                t.suspend_failed = true;
                // SAFETY: plain query.
                let err = unsafe { windows_sys::Win32::Foundation::GetLastError() };
                log(format!(
                    "could not suspend tid {} (error {err}); tried again every poll, logged once",
                    t.tid
                ));
            }
            return;
        }
        t.suspended = true;
        t.reason = reason;
        log(match reason {
            Reason::AceCode => format!(
                "suspended tid {}: started in {}+0x{:X} but runs in ACE ({} of its last {} samples, last {}+0x{:X}), {}% of a core, {} ms of CPU",
                t.tid,
                t.start_name,
                t.start_off,
                t.ace_hits,
                t.recent_len,
                t.rip_name,
                t.rip_off,
                t.rate_pct,
                t.cpu / 10_000
            ),
            _ => format!(
                "suspended tid {}: started in {}+0x{:X}, {} ms of CPU",
                t.tid,
                t.start_name,
                t.start_off,
                t.cpu / 10_000
            ),
        });
    }

    fn visit(&mut self, i: usize, elapsed_ms: u64) {
        if self.threads.slots[i].handle == 0 {
            return;
        }
        if !self.threads.slots[i].judged {
            self.judge(i);
        }
        let (quiet, perf_quiet) = (self.quiet, self.perf_quiet);
        let t = &mut self.threads.slots[i];
        if t.suspended {
            let keep = if t.reason == Reason::Perfsight {
                perf_quiet
            } else {
                quiet
            };
            if !keep {
                // SAFETY: a thread we suspended.
                unsafe { ResumeThread(t.h()) };
                t.suspended = false;
                t.reason = Reason::None;
                log(format!(
                    "resumed tid {} ({}+0x{:X})",
                    t.tid, t.start_name, t.start_off
                ));
            }
            return;
        }

        let cpu = thread_cpu(t.h());
        t.rate_pct = if t.has_cpu {
            rate_pct(cpu.wrapping_sub(t.last_cpu), elapsed_ms)
        } else {
            0
        };
        t.has_cpu = true;
        t.last_cpu = cpu;
        t.cpu = cpu;

        if t.start_in_ace {
            if quiet && cpu / 10_000 >= MIN_CPU_MS {
                self.suspend(i, Reason::AceStart);
            }
            return;
        }
        if t.start_in_perfsight {
            if perf_quiet && cpu / 10_000 >= PERFSIGHT_MIN_CPU_MS {
                self.suspend(i, Reason::Perfsight);
            }
            return;
        }
        // Lower CPU load.
        // in this DLL, so the game's own threads are never suspended by this rule.
        if t.start_in_exe || t.named || t.rate_pct < HOT_RATE_PCT {
            return;
        }
        self.sample_rip(i);
        let t = &self.threads.slots[i];
        if quiet && t.samples >= MIN_SAMPLES && t.ace_hits >= ACE_HITS_NEEDED {
            self.suspend(i, Reason::AceCode);
        }
    }

    // -----------------------------------------------------------------------------------------
    // Status file
    // -----------------------------------------------------------------------------------------

    fn status(&self) -> String {
        let mut w = String::with_capacity(16 * 1024);
        let on = |b: bool| if b { "ON" } else { "off" };
        let _ = write!(
            w,
            "tidelab patch R-20 ACE control  pid {}  poll {}  next in {} ms  module list rebuilt {}x  toolhelp fallbacks {}\r\n",
            self.pid,
            self.polls,
            if self.fast {
                FAST_POLL_MS
            } else {
                SLOW_POLL_MS
            },
            self.mods.refreshes,
            self.toolhelp_fallbacks
        );
        let _ = write!(
            w,
            "poller CPU: {} ms in total; last full minute {}.{:02}% of a core ({} minutes measured)\r\n",
            self.poller_cpu / 10_000,
            self.poller_last_minute_pct100 / 100,
            self.poller_last_minute_pct100 % 100,
            self.poller_minutes
        );
        let _ = write!(
            w,
            "switches beside the patch DLL: tidelab_ace.quiet {} (polled), tidelab_perfsight.quiet {} (polled), tidelab_ace.noload {} (read at startup)\r\n",
            on(self.quiet),
            on(self.perf_quiet),
            if !self.noload {
                "off"
            } else if LOAD_GATES.load(Ordering::SeqCst) {
                "ON"
            } else {
                "ON but not hooked"
            }
        );
        if self.noload {
            let _ = write!(
                w,
                "ACE DLL loads blocked: {}\r\n",
                BLOCKED_LOADS.load(Ordering::SeqCst)
            );
        }
        let _ = write!(
            w,
            ".sig gate: {} threads frozen\r\n",
            gates::FROZEN.load(Ordering::SeqCst)
        );
        if !self.mods.ace_loaded() {
            w.push_str("no ACE module is loaded in this process.\r\n");
        } else {
            for m in self.mods.list.iter().filter(|m| m.is_ace) {
                let _ = write!(
                    w,
                    "{}: base 0x{:X}, {} MB\r\n",
                    m.name,
                    m.start,
                    (m.end - m.start) / (1024 * 1024)
                );
            }
        }

        let live = || self.threads.slots.iter().filter(|t| t.tid != 0);
        let _ = write!(
            w,
            "\r\nsuspended: {}\r\n",
            live().filter(|t| t.suspended).count()
        );
        for t in live().filter(|t| t.suspended) {
            let why = match t.reason {
                Reason::AceCode => "(its code was running in ACE)",
                Reason::Perfsight => "(PerfSight)",
                _ => "(it started in ACE)",
            };
            let _ = write!(
                w,
                "  tid {}  started {}+0x{:X}  {} ms of CPU  {}\r\n",
                t.tid,
                t.start_name,
                t.start_off,
                t.cpu / 10_000,
                why
            );
        }

        let _ = write!(
            w,
            "\r\nPerfSight threads still running (the bar to suspend one is {PERFSIGHT_MIN_CPU_MS} ms of CPU):\r\n"
        );
        for t in live().filter(|t| t.alive && !t.suspended && t.start_in_perfsight) {
            let _ = write!(
                w,
                "  tid {}  {}+0x{:X}  {} ms of CPU  {}% of a core\r\n",
                t.tid,
                t.start_name,
                t.start_off,
                t.cpu / 10_000,
                t.rate_pct
            );
        }

        let _ = write!(
            w,
            "\r\nACE threads still running (the bar to suspend one is {MIN_CPU_MS} ms of CPU):\r\n"
        );
        for t in live().filter(|t| t.alive && !t.suspended && t.start_in_ace) {
            let _ = write!(
                w,
                "  tid {}  {}+0x{:X}  {} ms of CPU  {}% of a core\r\n",
                t.tid,
                t.start_name,
                t.start_off,
                t.cpu / 10_000,
                t.rate_pct
            );
        }

        let _ = write!(
            w,
            "\r\nbusy threads outside the exe, sampled ({HOT_RATE_PCT}% of a core is the bar; {ACE_HITS_NEEDED} of the last 8 samples inside ACE condemns one):\r\n"
        );
        for t in live().filter(|t| t.alive && !t.suspended && !t.start_in_ace && t.samples > 0) {
            let _ = write!(
                w,
                "  tid {}  started {}+0x{:X}  {}% of a core  {} ms of CPU  in ACE {} of the last {}  last at {}+0x{:X}{}\r\n",
                t.tid,
                t.start_name,
                t.start_off,
                t.rate_pct,
                t.cpu / 10_000,
                t.ace_hits,
                t.recent_len,
                t.rip_name,
                t.rip_off,
                if t.cleared() { "  (cleared)" } else { "" }
            );
        }

        w.push_str("\r\nlog\r\n");
        if let Ok(l) = LOG.lock() {
            w.push_str(&l.text);
            if l.cut > 0 {
                let _ = write!(w, "({} lines did not fit)\r\n", l.cut);
            }
        }
        w
    }
}

// ---------------------------------------------------------------------------------------------
// Tests (the pure parts)
// ---------------------------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    fn u16s(s: &str) -> Vec<u16> {
        s.encode_utf16().collect()
    }

    #[test]
    fn is_ace_path_matches_the_ace_dlls_by_file_name_whatever_the_case_or_the_path() {
        assert!(is_ace_path(&u16s(
            r"C:\g\Binaries\Win64\AntiCheatExpert\ACE-Base64.dll"
        )));
        assert!(is_ace_path(&u16s("ace-base64.dll")));
        assert!(is_ace_path(&u16s(r"..\AntiCheatExpert/ACE-Core64.dll")));
        assert!(!is_ace_path(&u16s(
            r"C:\g\Binaries\Win64\tidelab_patch.dll"
        )));
        assert!(!is_ace_path(&u16s(r"C:\AntiCheatExpert\other.dll")));
        assert!(!is_ace_path(&u16s("ace")));
        assert!(!is_ace_path(&u16s("")));
    }

    #[test]
    fn mod_at_finds_the_module_an_address_belongs_to_and_marks_the_ace_ones() {
        let mut mods = Mods::default();
        mods.list
            .push(Mod::new(0x1000, 0x2000, "Client-Win64-Shipping.exe"));
        mods.list.push(Mod::new(0x5000, 0x9000, "ACE-Base64.dll"));
        mods.list.push(Mod::new(0xA000, 0xB000, "PerfSight.dll"));
        assert!(!mods.at(0x1500).unwrap().is_ace);
        assert!(mods.at(0x5000).unwrap().is_ace);
        assert!(mods.at(0x8fff).unwrap().is_ace);
        assert!(mods.at(0xA100).unwrap().is_perfsight);
        assert!(mods.at(0x9000).is_none());
        assert!(mods.at(0).is_none());
        assert!(mods.ace_loaded());
        mods.list.clear();
        assert!(!mods.ace_loaded());
    }

    #[test]
    fn the_sample_history_keeps_the_last_8_verdicts() {
        let mut t = Thread::default();
        for _ in 0..8 {
            t.note_sample(true);
        }
        assert_eq!((t.ace_hits, t.recent_len), (8, 8));
        // Lower CPU load.
        for _ in 0..8 {
            t.note_sample(false);
        }
        assert_eq!((t.ace_hits, t.samples), (0, 16));
        // Lower CPU load.
        let mut g = Thread::default();
        for _ in 0..3 {
            g.note_sample(true);
            g.note_sample(false);
            g.note_sample(false);
        }
        assert!(g.ace_hits < ACE_HITS_NEEDED);
    }

    #[test]
    fn r20b_rate_pct_divides_by_the_real_interval() {
        assert_eq!(rate_pct(10_000_000, 1000), 100); // 1 s of CPU in 1 s
        assert_eq!(rate_pct(10_000_000, 5000), 20); // 1 s of CPU in 5 s
        assert_eq!(rate_pct(u64::MAX, 1000), 1000); // capped
        assert_eq!(rate_pct(0, 0), 0);
    }

    #[test]
    fn r20b_dead_threads_free_their_slots_and_new_threads_reuse_them() {
        let mut ts = Threads::default();
        let a = ts.add(11, 0);
        let b = ts.add(22, 0);
        ts.slots[a].alive = false;
        ts.slots[b].alive = true;
        ts.forget_dead();
        assert!(ts.find(11).is_none());
        assert!(ts.find(22).is_some());
        assert_eq!(ts.add(33, 0), 0); // the freed slot
        assert_eq!(ts.slots.len(), 2);
    }

    #[test]
    fn r20b_the_poller_slows_down_once_nothing_is_left_to_act_on() {
        let mut ts = Threads::default();
        // Lower CPU load.
        let idle = ts.add(1, 0);
        ts.slots[idle] = Thread {
            tid: 1,
            alive: true,
            start_in_ace: true,
            rate_pct: 1,
            ..Thread::default()
        };
        let frozen = ts.add(2, 0);
        ts.slots[frozen] = Thread {
            tid: 2,
            alive: true,
            start_in_ace: true,
            suspended: true,
            rate_pct: 90,
            ..Thread::default()
        };
        assert!(!ts.need_fast_poll(true, true));

        // Lower CPU load.
        ts.slots[idle].rate_pct = 50;
        assert!(ts.need_fast_poll(true, true));
        ts.slots[idle].rate_pct = 0;

        // A busy thread under watch: fast until its 8 samples clear it.
        let shell = ts.add(3, 0);
        ts.slots[shell] = Thread {
            tid: 3,
            alive: true,
            rate_pct: 25,
            ..Thread::default()
        };
        ts.slots[shell].note_sample(false);
        assert!(ts.need_fast_poll(true, true));
        for _ in 0..7 {
            ts.slots[shell].note_sample(false);
        }
        assert!(ts.slots[shell].cleared());
        assert!(!ts.need_fast_poll(true, true));
        // Lower CPU load.
        ts.slots[shell].note_sample(true);
        assert!(ts.need_fast_poll(true, true));
        ts.slots[shell].alive = false;

        // PerfSight before its freeze keeps it fast only while that switch is on.
        let perf = ts.add(4, 0);
        ts.slots[perf] = Thread {
            tid: 4,
            alive: true,
            start_in_perfsight: true,
            rate_pct: 2,
            ..Thread::default()
        };
        assert!(ts.need_fast_poll(true, true));
        assert!(!ts.need_fast_poll(true, false));
    }

    #[test]
    fn the_log_keeps_its_lines_and_counts_what_did_not_fit() {
        let mut l = Log {
            text: String::new(),
            cut: 0,
        };
        l.push("one 1");
        l.push("two");
        assert_eq!(l.text, "one 1\r\ntwo\r\n");
        l.push(&"x".repeat(LOG_MAX));
        assert_eq!(l.cut, 1);
        assert_eq!(l.text, "one 1\r\ntwo\r\n");
    }

    #[test]
    fn note_process_logs_only_ace_images() {
        let before = LOG.lock().unwrap().text.len();
        note_process(r"C:\g\AntiCheatExpert\ACE-Setup64.exe");
        note_process(r"C:\Windows\System32\cmd.exe");
        let l = LOG.lock().unwrap();
        assert_eq!(&l.text[before..], "the client starts ACE-Setup64.exe\r\n");
    }
}
