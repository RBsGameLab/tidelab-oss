// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! In-process, module-level sampling profiler for the game client, with time slices, watchdog
//! and memory dump.
//!
//! EventPipe is switched off in the client's .NET host, so the
//! only way to look inside is from the patch itself. While `tidelab_prof.on` exists beside the
//! DLL, a thread of ours suspends the game thread (and up to three other threads) every
//! millisecond, reads RIP/RSP, copies the top of the stack, resumes it, and only then works out
//! which module the time belongs to. Nothing that can take a lock (heap, loader, function
//! tables) is called while a target is suspended.
//!
//! Reports: `tidelab_prof_<pid>_<n>.txt` and `_slices.csv` beside the DLL, rewritten every 10 s
//! and at the end; `_hot.csv` at the end.

mod dump;
mod report;
pub mod tables;

use std::ffi::c_void;
use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, AtomicU8, AtomicU32, AtomicUsize, Ordering};

use windows_sys::Win32::Foundation::{
    CloseHandle, FILETIME, HANDLE, INVALID_HANDLE_VALUE, LocalFree, SYSTEMTIME,
};
use windows_sys::Win32::System::Diagnostics::Debug::{
    CONTEXT, CONTEXT_CONTROL_AMD64, GetThreadContext,
};
use windows_sys::Win32::System::Diagnostics::ToolHelp::{
    CreateToolhelp32Snapshot, TH32CS_SNAPTHREAD, THREADENTRY32, Thread32First, Thread32Next,
};
use windows_sys::Win32::System::LibraryLoader::{
    GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS, GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
    GetModuleHandleExW,
};
use windows_sys::Win32::System::Memory::{
    MEM_COMMIT, MEM_IMAGE, MEMORY_BASIC_INFORMATION, VirtualQuery,
};
use windows_sys::Win32::System::Performance::{QueryPerformanceCounter, QueryPerformanceFrequency};
use windows_sys::Win32::System::ProcessStatus::{K32EnumProcessModules, K32GetModuleBaseNameW};
use windows_sys::Win32::System::SystemInformation::GetLocalTime;
use windows_sys::Win32::System::Threading::{
    CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, CreateWaitableTimerExW, GetCurrentProcess,
    GetCurrentProcessId, GetCurrentThread, GetCurrentThreadId, GetThreadDescription,
    GetThreadTimes, OpenThread, ResumeThread, SetWaitableTimer, Sleep, SuspendThread,
    THREAD_GET_CONTEXT, THREAD_QUERY_INFORMATION, THREAD_QUERY_LIMITED_INFORMATION,
    THREAD_SUSPEND_RESUME, TIMER_ALL_ACCESS, WaitForSingleObject,
};

use crate::beside;
use crate::nt;
use tables::*;

pub const ON: &str = "tidelab_prof.on";
pub const STATUS: &str = "tidelab_prof.status";
pub const STUCK: &str = "tidelab_prof.stuck";
pub const RANGES: &str = "tidelab_prof_ranges.txt";

const TICK_100NS: i64 = 10_000; // 1 ms
const STACK_COPY_WORDS: usize = 32 * 1024 / 8;
const REPORT_EVERY_S: i64 = 10;
/// GameThread, RenderThread, RHIThread and the busiest other named game thread.
const MAX_TARGETS: usize = 4;
/// Distinct sampled threads per session (hot-table slot 15 holds the hitch slices).
const MAX_SLOTS: usize = 15;
const HITCH_SLOT: usize = 15;
const MAX_THREADS: usize = 2048;
const MAX_SLICES: usize = 6000; // 10 minutes
const MAX_ANONS: usize = 1024;
const MAX_MOD_RANGES: usize = 8192;
const MAX_REGIONS: usize = 16;

const fn is_exec(protect: u32) -> bool {
    protect & (0x10 | 0x20 | 0x40 | 0x80) != 0
}

fn file_time(ft: FILETIME) -> u64 {
    (u64::from(ft.dwHighDateTime) << 32) | u64::from(ft.dwLowDateTime)
}

fn now() -> i64 {
    let mut t = 0i64;
    // SAFETY: out-pointer to a local.
    unsafe { QueryPerformanceCounter(&mut t) };
    t
}

/// CPU time (100 ns) of a thread, and its creation time.
fn thread_times(h: HANDLE) -> Option<(u64, u64)> {
    let z = FILETIME {
        dwLowDateTime: 0,
        dwHighDateTime: 0,
    };
    let (mut c, mut e, mut k, mut u) = (z, z, z, z);
    // SAFETY: out-pointers to locals.
    (unsafe { GetThreadTimes(h, &mut c, &mut e, &mut k, &mut u) } != 0)
        .then(|| (file_time(k) + file_time(u), file_time(c)))
}

fn self_cpu() -> u64 {
    // SAFETY: the pseudo handle of this thread.
    thread_times(unsafe { GetCurrentThread() }).map_or(0, |t| t.0)
}

fn local_time() -> SYSTEMTIME {
    // SAFETY: plain data, filled by the call.
    let mut t: SYSTEMTIME = unsafe { std::mem::zeroed() };
    unsafe { GetLocalTime(&mut t) };
    t
}

fn virtual_query(addr: usize) -> Option<MEMORY_BASIC_INFORMATION> {
    // SAFETY: plain data, filled by the call; any address may be queried.
    let mut mbi: MEMORY_BASIC_INFORMATION = unsafe { std::mem::zeroed() };
    let n = unsafe {
        VirtualQuery(
            addr as *const c_void,
            &mut mbi,
            size_of::<MEMORY_BASIC_INFORMATION>(),
        )
    };
    (n != 0).then_some(mbi)
}

fn thread_name(h: HANDLE) -> String {
    let mut w: *mut u16 = std::ptr::null_mut();
    // SAFETY: the description is a NUL-terminated string freed with LocalFree.
    unsafe {
        if GetThreadDescription(h, &mut w) < 0 || w.is_null() {
            return String::new();
        }
        let len = (0..).take_while(|&i| *w.add(i) != 0).count();
        let s = beside::narrow(std::slice::from_raw_parts(w, len.min(64)));
        LocalFree(w.cast());
        s
    }
}

// ---------------------------------------------------------------------------------------------
// Shared with the watchdog (R-16): the sampler once stopped for good 30 s into a session while
// the game ran on. If the sampler makes no progress for 3 s, the watchdog records where it is
// stuck (stage, target thread, the sampler's own RIP) in tidelab_prof.stuck, and resumes the
// target it may have left suspended.
// ---------------------------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
enum Stage {
    Idle = 0,
    Running = 1,
    Suspended = 2,
}

impl Stage {
    fn name(v: u8) -> &'static str {
        match v {
            0 => "idle",
            1 => "running",
            _ => "suspended",
        }
    }
}

struct Shared {
    progress: AtomicU32,
    stage: AtomicU8,
    target_tid: AtomicU32,
    target_handle: AtomicUsize,
    resumed: AtomicBool,
    sampler_thread: AtomicUsize,
    watchdog_tid: AtomicU32,
    stuck: Mutex<(u32, String)>,
}

static SHARED: Shared = Shared {
    progress: AtomicU32::new(0),
    stage: AtomicU8::new(0),
    target_tid: AtomicU32::new(0),
    target_handle: AtomicUsize::new(0),
    resumed: AtomicBool::new(false),
    sampler_thread: AtomicUsize::new(0),
    watchdog_tid: AtomicU32::new(0),
    stuck: Mutex::new((0, String::new())),
};

fn set_stage(s: Stage) {
    SHARED.stage.store(s as u8, Ordering::Release);
}

fn watchdog() {
    // SAFETY: plain query.
    SHARED
        .watchdog_tid
        .store(unsafe { GetCurrentThreadId() }, Ordering::Release);
    let mut last = SHARED.progress.load(Ordering::Acquire);
    let mut still = 0u32;
    loop {
        // SAFETY: plain sleep.
        unsafe { Sleep(500) };
        let p = SHARED.progress.load(Ordering::Acquire);
        if p != last || SHARED.stage.load(Ordering::Acquire) == Stage::Idle as u8 {
            last = p;
            still = 0;
            continue;
        }
        still += 1;
        if still == 6 {
            report_stuck();
        }
    }
}

/// The module holding `addr`, and the offset into it, looked up through the loader (the
/// watchdog does this only after resuming the sampler).
fn module_of(addr: usize) -> (String, usize) {
    let mut hm: *mut c_void = std::ptr::null_mut();
    // SAFETY: FROM_ADDRESS takes the address as the name; no reference is added.
    let ok = unsafe {
        GetModuleHandleExW(
            GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
            addr as *const u16,
            &mut hm,
        )
    };
    if ok == 0 || hm.is_null() {
        return ("[unknown]".into(), addr);
    }
    let name = beside::module_path(hm as usize).map_or_else(
        || "?".into(),
        |p| beside::basename(&beside::narrow(&p)).to_string(),
    );
    (name, addr - hm as usize)
}

fn report_stuck() {
    let stage = SHARED.stage.load(Ordering::Acquire);
    let tid = SHARED.target_tid.load(Ordering::Acquire);

    // Where is the sampler thread?
    let mut rip = 0usize;
    let sampler = SHARED.sampler_thread.load(Ordering::Acquire) as HANDLE;
    if !sampler.is_null() {
        // SAFETY: our own sampler thread; CONTEXT is plain data, filled by the call.
        unsafe {
            if SuspendThread(sampler) != u32::MAX {
                let mut ctx: Box<CONTEXT> = Box::new(std::mem::zeroed());
                ctx.ContextFlags = CONTEXT_CONTROL_AMD64;
                if GetThreadContext(sampler, &mut *ctx) != 0 {
                    rip = ctx.Rip as usize;
                }
                ResumeThread(sampler);
            }
        }
    }

    // Undo our suspension of the target.
    let mut prev: i64 = -2;
    if stage == Stage::Suspended as u8 {
        let hv = SHARED.target_handle.load(Ordering::Acquire);
        if hv != 0 {
            SHARED.resumed.store(true, Ordering::Release);
            // SAFETY: the sampler's target, which it suspended.
            prev = i64::from(unsafe { ResumeThread(hv as HANDLE) } as i32);
        }
    }

    let (rip_mod, rip_off) = module_of(rip);
    // SAFETY: plain open, closed below.
    let th = unsafe {
        OpenThread(
            THREAD_QUERY_INFORMATION | THREAD_QUERY_LIMITED_INFORMATION,
            0,
            tid,
        )
    };
    let (name, start) = if th.is_null() {
        ("?".to_string(), "?".to_string())
    } else {
        let mut start_addr = 0usize;
        // SAFETY: ThreadQuerySetWin32StartAddress into a usize.
        let st = unsafe {
            nt::NtQueryInformationThread(
                th,
                nt::THREAD_QUERY_SET_WIN32_START_ADDRESS,
                (&raw mut start_addr).cast(),
                size_of::<usize>() as u32,
                std::ptr::null_mut(),
            )
        };
        let start = if st >= 0 {
            module_of(start_addr).0
        } else {
            "?".into()
        };
        let name = thread_name(th);
        // SAFETY: opened above.
        unsafe { CloseHandle(th) };
        (name, start)
    };
    let clock = local_time();
    let Ok(mut stuck) = SHARED.stuck.lock() else {
        return;
    };
    stuck.0 += 1;
    let line = format!(
        "stuck #{} at {:02}:{:02}:{:02}: stage {}; target tid {} \"{}\" start {}; sampler RIP {}+0x{:x}; resumed target: previous suspend count {}\r\n",
        stuck.0,
        clock.wHour,
        clock.wMinute,
        clock.wSecond,
        Stage::name(stage),
        tid,
        name,
        start,
        rip_mod,
        rip_off,
        prev
    );
    if stuck.1.len() + line.len() <= 4096 {
        stuck.1.push_str(&line);
    }
    beside::write(STUCK, stuck.1.as_bytes());
}

// ---------------------------------------------------------------------------------------------
// Entry
// ---------------------------------------------------------------------------------------------

/// Called on attach. Starts the sampler's thread; it does nothing outside the game process.
pub fn start(module: *mut c_void) {
    let module = module as usize;
    let _ = std::thread::Builder::new()
        .name("tidelab sampler".into())
        .spawn(move || run(module));
}

fn run(module: usize) {
    if !beside::in_client() || !beside::set_dir(module) {
        return;
    }
    let mut s = Sampler::new();
    let note = format!(
        "R-15 sampler armed in pid {}; high-resolution timer: {}\r\nCreate tidelab_prof.on beside the patch DLL to sample, delete it to stop.\r\n",
        s.pid,
        s.timer != 0
    );
    beside::write(STATUS, note.as_bytes());

    // SAFETY: a handle to this thread for the watchdog, kept for the process' life.
    let me = unsafe {
        OpenThread(
            THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT | THREAD_QUERY_INFORMATION,
            0,
            s.self_tid,
        )
    };
    SHARED.sampler_thread.store(me as usize, Ordering::Release);
    let _ = std::thread::Builder::new()
        .name("tidelab sampler watchdog".into())
        .spawn(watchdog);

    loop {
        set_stage(Stage::Idle);
        while !beside::exists(ON) {
            if beside::exists(dump::ON) {
                s.dump();
            }
            // SAFETY: plain sleep.
            unsafe { Sleep(1000) };
        }
        set_stage(Stage::Running);
        s.session();
    }
}

// ---------------------------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------------------------

#[derive(Debug, Clone, Default)]
struct ThreadInfo {
    tid: u32,
    handle: usize,
    can_sample: bool,
    excluded: bool,
    alive: bool,
    created: u64,
    last_cpu: u64,
    cpu_last_s: u64,
    cpu_session: u64,
    stack_base: usize,
    start_cat: u16,
    name: String,
}

struct Slot {
    tid: u32,
    name: String,
    samples: u32,
    failures: u32,
    leaf: Vec<u32>,
    leaf_sys: Vec<u32>,
    incl: Vec<u32>,
}

impl Slot {
    fn new(tid: u32, name: &str) -> Self {
        Slot {
            tid,
            name: name.to_string(),
            samples: 0,
            failures: 0,
            leaf: vec![0; CAT_COUNT],
            leaf_sys: vec![0; CAT_COUNT],
            incl: vec![0; CAT_COUNT],
        }
    }
}

/// GameThread every tick, RenderThread and RHIThread every 2nd, the other one every 4th.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Role {
    Main,
    Render,
    Rhi,
    Other,
}

impl Role {
    fn due(self, tick: u32) -> bool {
        match self {
            Role::Main => true,
            Role::Render | Role::Rhi => tick.is_multiple_of(2),
            Role::Other => tick.is_multiple_of(4),
        }
    }
}

#[derive(Debug, Clone, Copy)]
struct Target {
    handle: usize,
    stack_base: usize,
    slot: usize,
    role: Role,
}

/// `js_stack`: a V8 builtin (JS frame) is on the copied stack, i.e. this work runs under a JS
/// call.
struct Sample {
    leaf: u16,
    sys: bool,
    leaf_payload: u64,
    js_stack: bool,
}

struct Sampler {
    pid: u32,
    self_tid: u32,
    qpc_freq: i64,
    timer: usize,
    timer_armed: bool,
    session_no: u32,
    session_start: i64,
    session_clock: SYSTEMTIME,
    self_cpu_start: u64,

    // Modules ever seen in this process; their index is their category.
    mod_names: Vec<String>,
    mod_bases: Vec<usize>,
    mod_managed: Vec<bool>,
    exe_cat: u16,
    tables: Tables,
    regions_note: String,

    threads: Vec<ThreadInfo>,
    main_tid: u32,
    slots: Vec<Slot>,
    targets: Vec<Target>,

    seen: Vec<u32>,
    stamp: u32,
    stack_buf: Vec<usize>,
    ctx: Box<CONTEXT>,
    hot: Hot,

    slices: Vec<SliceRow>,
    cur_slice: SliceRow,
    cur_leaf: Vec<u32>,
    cur_sys: Vec<u32>,
    cur_payloads: Vec<u64>,
    hitch_leaf: Vec<u32>,
    hitch_sys: Vec<u32>,
    hitch_samples: u64,
    cat_bucket: Vec<Bucket>,
    /// GameThread samples whose stack copy hit the 32 KB cap, of all.
    main_truncated: u64,
    main_total: u64,
}

impl Sampler {
    fn new() -> Self {
        let mut qpc_freq = 1i64;
        // SAFETY: plain queries; the timer handle lives as long as the process.
        let (pid, self_tid, timer) = unsafe {
            QueryPerformanceFrequency(&mut qpc_freq);
            (
                GetCurrentProcessId(),
                GetCurrentThreadId(),
                CreateWaitableTimerExW(
                    std::ptr::null(),
                    std::ptr::null(),
                    CREATE_WAITABLE_TIMER_HIGH_RESOLUTION,
                    TIMER_ALL_ACCESS,
                ),
            )
        };
        Sampler {
            pid,
            self_tid,
            qpc_freq: qpc_freq.max(1),
            timer: timer as usize,
            timer_armed: false,
            session_no: 0,
            session_start: 0,
            session_clock: local_time(),
            self_cpu_start: 0,
            mod_names: Vec::new(),
            mod_bases: Vec::new(),
            mod_managed: Vec::new(),
            exe_cat: CAT_UNKNOWN,
            tables: Tables::default(),
            regions_note: String::new(),
            threads: Vec::new(),
            main_tid: 0,
            slots: Vec::new(),
            targets: Vec::new(),
            seen: vec![0; CAT_COUNT],
            stamp: 0,
            stack_buf: vec![0; STACK_COPY_WORDS],
            // SAFETY: CONTEXT is plain data.
            ctx: Box::new(unsafe { std::mem::zeroed() }),
            hot: Hot::default(),
            slices: Vec::with_capacity(MAX_SLICES),
            cur_slice: SliceRow::default(),
            cur_leaf: vec![0; CAT_COUNT],
            cur_sys: vec![0; CAT_COUNT],
            cur_payloads: Vec::with_capacity(1024),
            hitch_leaf: vec![0; CAT_COUNT],
            hitch_sys: vec![0; CAT_COUNT],
            hitch_samples: 0,
            // Modules get theirs when they are listed; the special categories are fixed.
            cat_bucket: (0..CAT_COUNT as u16)
                .map(|c| match c {
                    CAT_ANON => Bucket::Jit,
                    CAT_V8_JS => Bucket::Js,
                    CAT_V8_CPP => Bucket::V8Cpp,
                    _ => Bucket::Other,
                })
                .collect(),
            main_truncated: 0,
            main_total: 0,
        }
    }

    fn session(&mut self) {
        self.session_no += 1;
        self.slots.clear();
        self.hot.clear();
        self.seen.fill(0);
        self.stamp = 0;
        self.tables.anons.clear();
        self.slices.clear();
        self.cur_slice = SliceRow::default();
        self.cur_leaf.fill(0);
        self.cur_sys.fill(0);
        self.cur_payloads.clear();
        self.hitch_leaf.fill(0);
        self.hitch_sys.fill(0);
        self.hitch_samples = 0;
        self.main_truncated = 0;
        self.main_total = 0;

        self.refresh_modules();
        self.load_regions();
        self.refresh_threads();
        for t in &mut self.threads {
            t.cpu_session = 0;
        }
        self.pick_targets();
        self.session_start = now();
        self.session_clock = local_time();
        self.self_cpu_start = self_cpu();

        let freq = self.qpc_freq;
        let mut next_second = self.session_start + freq;
        let mut next_report = self.session_start + REPORT_EVERY_S * freq;
        let slice_len = freq / 10;
        let mut next_slice = self.session_start + slice_len;
        let mut slice_index = 0u32;
        self.arm_timer();
        let mut tick = 0u32;
        loop {
            self.wait_tick();
            SHARED.progress.fetch_add(1, Ordering::Release);
            for i in 0..self.targets.len() {
                let t = self.targets[i];
                if !t.role.due(tick) {
                    continue;
                }
                if let Some(s) = self.sample_one(t) {
                    self.record_slice(t.role, &s);
                }
            }

            let t_now = now();
            while t_now >= next_slice {
                self.close_slice(slice_index);
                slice_index += 1;
                next_slice += slice_len;
            }
            if t_now >= next_second {
                next_second = t_now + freq;
                if !beside::exists(ON) {
                    break;
                }
                self.refresh_modules();
                self.refresh_threads();
                self.pick_targets();
            }
            if t_now >= next_report {
                next_report = t_now + REPORT_EVERY_S * freq;
                self.write_report(false);
            }
            tick = tick.wrapping_add(1);
        }
        self.write_report(true);
    }

    /// A periodic 1 ms timer (re-arming after each tick would add the sampling work to the period).
    fn arm_timer(&mut self) {
        if self.timer == 0 {
            return;
        }
        let due = -TICK_100NS;
        // SAFETY: our timer handle.
        self.timer_armed =
            unsafe { SetWaitableTimer(self.timer as HANDLE, &due, 1, None, std::ptr::null(), 0) }
                != 0;
    }

    fn wait_tick(&self) {
        // SAFETY: our timer handle, or a plain sleep.
        unsafe {
            if self.timer_armed {
                WaitForSingleObject(self.timer as HANDLE, 100);
            } else {
                Sleep(1);
            }
        }
    }

    // -----------------------------------------------------------------------------------------
    // Sampling
    // -----------------------------------------------------------------------------------------

    fn sample_one(&mut self, t: Target) -> Option<Sample> {
        let slot_tid = self.slots[t.slot].tid;
        SHARED.target_tid.store(slot_tid, Ordering::Release);
        SHARED.target_handle.store(t.handle, Ordering::Release);
        SHARED.resumed.store(false, Ordering::Release);
        set_stage(Stage::Suspended);

        // --- target suspended: no locks, no allocation, no lookups ---
        let h = t.handle as HANDLE;
        let mut rip = 0usize;
        let mut words = 0usize;
        // SAFETY: a thread handle with suspend and context access. While it is suspended only
        // GetThreadContext and a copy of its stack (between RSP and the stack base, committed
        // memory) into a buffer allocated beforehand run here.
        unsafe {
            if SuspendThread(h) == u32::MAX {
                set_stage(Stage::Running);
                self.slots[t.slot].failures += 1;
                return None;
            }
            self.ctx.ContextFlags = CONTEXT_CONTROL_AMD64;
            if GetThreadContext(h, &mut *self.ctx) != 0 {
                rip = self.ctx.Rip as usize;
                let rsp = self.ctx.Rsp as usize;
                if t.stack_base > rsp && t.stack_base - rsp < 256 * 1024 * 1024 {
                    words = STACK_COPY_WORDS.min((t.stack_base - rsp) / 8);
                    std::ptr::copy_nonoverlapping(
                        rsp as *const u8,
                        self.stack_buf.as_mut_ptr().cast::<u8>(),
                        words * 8,
                    );
                }
            }
            if !SHARED.resumed.load(Ordering::Acquire) {
                ResumeThread(h); // the watchdog may have done it
            }
        }
        set_stage(Stage::Running);
        // --- resumed ---

        if rip == 0 {
            self.slots[t.slot].failures += 1;
            return None;
        }
        self.slots[t.slot].samples += 1;

        let mut alloc = 0usize;
        let leaf = match self.tables.lookup(rip) {
            Some(c) => c,
            None => self.classify_unlisted(rip, &mut alloc),
        };
        if leaf == CAT_ANON
            && alloc == 0
            && let Some(r) = self.tables.anons.find(rip)
        {
            alloc = r.alloc;
        }
        let slot = &mut self.slots[t.slot];
        slot.leaf[leaf as usize] += 1;
        let mut sys = false;
        if (leaf as usize) < MAX_MODULES && rip >= 2 {
            // SAFETY: RIP is inside a loaded module's code, so the two bytes before it are mapped.
            let before = unsafe { std::slice::from_raw_parts((rip - 2) as *const u8, 2) };
            sys = follows_syscall(before);
            if sys {
                slot.leaf_sys[leaf as usize] += 1;
            }
        }

        self.stamp = self.stamp.wrapping_add(1);
        if self.stamp == 0 {
            self.seen.fill(0);
            self.stamp = 1;
        }
        self.seen[leaf as usize] = self.stamp;
        slot.incl[leaf as usize] += 1;
        let caller = scan_stack(
            &self.stack_buf[..words],
            &self.tables,
            leaf,
            &mut self.seen,
            self.stamp,
            &mut slot.incl,
        );
        if t.role == Role::Main {
            self.main_total += 1;
            if words == STACK_COPY_WORDS {
                self.main_truncated += 1;
            }
        }

        let offset = (rip as u64).wrapping_sub(self.cat_offset_base(leaf) as u64);
        let leaf_payload = (u64::from(leaf) << 44) | ((offset >> 4) & ((1 << 44) - 1));
        self.hot.add(t.slot, HOT_LEAF, leaf_payload);
        self.hot.add(
            t.slot,
            HOT_PAIR,
            (u64::from(leaf) << 16) | u64::from(caller),
        );
        if leaf == CAT_ANON {
            self.hot.add(t.slot, HOT_ALLOC, (alloc >> 16) as u64);
        }
        if t.role == Role::Main {
            self.record_jit_stack(t.slot, words, leaf == self.exe_cat);
        }
        Some(Sample {
            leaf,
            sys,
            leaf_payload,
            js_stack: self.seen[CAT_V8_JS as usize] == self.stamp,
        })
    }

    /// R-55: the GameThread's stack words that point into JIT/anon code (return addresses of C#
    /// methods, also stale ones and function pointers), each address once per sample. Summed per
    /// C# method offline (hot_functions.py), they give approximate inclusive time.
    ///
    /// With `leaf_in_exe`, the first such word (nearest the stack pointer) is also counted with
    /// bit 56 set: the C# method under which this engine (exe) sample ran ("[JIT caller of exe]"
    /// in _hot.csv).
    fn record_jit_stack(&mut self, slot: usize, words: usize, leaf_in_exe: bool) {
        if self.tables.anons.list.is_empty() {
            return;
        }
        let mut seen_addrs = [0u64; 64];
        let mut n = 0;
        let mut caller_noted = !leaf_in_exe;
        for &w in &self.stack_buf[..words] {
            if self.tables.anons.find(w).is_none() {
                continue;
            }
            let b = (w as u64) >> 4;
            if !caller_noted {
                caller_noted = true;
                self.hot.add(slot, HOT_STACK, (1 << 56) | b);
            }
            if seen_addrs[..n].contains(&b) {
                continue;
            }
            if n == seen_addrs.len() {
                break;
            }
            seen_addrs[n] = b;
            n += 1;
            self.hot.add(slot, HOT_STACK, b);
        }
    }

    /// RIP outside every listed module: JIT/anonymous executable memory (remembered so later
    /// stack scans recognise it), or unknown.
    fn classify_unlisted(&mut self, addr: usize, alloc: &mut usize) -> u16 {
        let Some(mbi) = virtual_query(addr) else {
            return CAT_UNKNOWN;
        };
        if mbi.State != MEM_COMMIT || !is_exec(mbi.Protect) || mbi.Type == MEM_IMAGE {
            return CAT_UNKNOWN; // MEM_IMAGE: an image the module list does not show
        }
        *alloc = mbi.AllocationBase as usize;
        let anons = &mut self.tables.anons;
        if anons.list.len() < MAX_ANONS {
            let start = mbi.BaseAddress as usize;
            anons.list.push(Range {
                start,
                end: start + mbi.RegionSize,
                cat: CAT_ANON,
                alloc: *alloc,
            });
            anons.seal();
        }
        CAT_ANON
    }

    // -----------------------------------------------------------------------------------------
    // Time slices
    // -----------------------------------------------------------------------------------------

    fn record_slice(&mut self, role: Role, s: &Sample) {
        let r = match role {
            Role::Main => 0,
            Role::Render => 1,
            Role::Rhi => 2,
            Role::Other => return,
        };
        let c = &mut self.cur_slice;
        c.n[r] = c.n[r].saturating_add(1);
        if s.sys {
            c.wait[r] = c.wait[r].saturating_add(1);
        }
        if role != Role::Main {
            return;
        }
        let b = if s.sys {
            Bucket::OsWait
        } else {
            self.cat_bucket[s.leaf as usize]
        };
        c.main_bucket[b as usize] = c.main_bucket[b as usize].saturating_add(1);
        if !s.sys && s.js_stack {
            c.js_busy = c.js_busy.saturating_add(1);
        }
        self.cur_leaf[s.leaf as usize] += 1;
        if s.sys {
            self.cur_sys[s.leaf as usize] += 1;
        }
        if self.cur_payloads.len() < self.cur_payloads.capacity() {
            self.cur_payloads.push(s.leaf_payload);
        }
    }

    fn close_slice(&mut self, index: u32) {
        self.cur_slice.index = index;
        self.cur_slice.hitch = is_hitch(&self.cur_slice);
        if self.cur_slice.hitch {
            for c in 0..CAT_COUNT {
                self.hitch_leaf[c] += self.cur_leaf[c];
                self.hitch_sys[c] += self.cur_sys[c];
            }
            self.hitch_samples += u64::from(self.cur_slice.n[0]);
            for &p in &self.cur_payloads {
                self.hot.add(HITCH_SLOT, HOT_LEAF, p);
            }
        }
        if self.slices.len() < MAX_SLICES {
            self.slices.push(self.cur_slice);
        }
        self.cur_slice = SliceRow::default();
        self.cur_leaf.fill(0);
        self.cur_sys.fill(0);
        self.cur_payloads.clear();
    }

    // -----------------------------------------------------------------------------------------
    // Modules
    // -----------------------------------------------------------------------------------------

    fn intern_module(&mut self, name: &str) -> Option<u16> {
        if let Some(i) = self.mod_names.iter().position(|n| n == name) {
            return Some(i as u16);
        }
        if self.mod_names.len() == MAX_MODULES {
            return None;
        }
        self.mod_names.push(name.to_string());
        self.mod_bases.push(0);
        self.mod_managed.push(false);
        Some((self.mod_names.len() - 1) as u16)
    }

    /// The executable sections of the image at `base` (the whole image if it has none).
    fn add_image(&mut self, base: usize, cat: u16) {
        let Some(mbi) = virtual_query(base) else {
            return;
        };
        if mbi.State != MEM_COMMIT || mbi.Type != MEM_IMAGE {
            return;
        }
        // SAFETY: a committed image: its headers are mapped readable (at least the first page,
        // which the bounds below keep to).
        let rd16 =
            |off: usize| unsafe { std::ptr::read_unaligned((base + off) as *const u16) } as usize;
        let rd32 =
            |off: usize| unsafe { std::ptr::read_unaligned((base + off) as *const u32) } as usize;
        if rd16(0) != 0x5A4D {
            return;
        }
        let nt = rd32(0x3c);
        if nt > 2048 || rd32(nt) != 0x4550 {
            return;
        }
        let nsec = rd16(nt + 6);
        let opt_size = rd16(nt + 20);
        if nsec > 96 || nt + 24 + opt_size + nsec * 40 > 4096 {
            return;
        }
        let opt = nt + 24;
        if rd16(opt) == 0x20b && opt_size >= 112 + 15 * 8 {
            self.mod_managed[cat as usize] = rd32(opt + 112 + 14 * 8) != 0; // COM descriptor: a .NET image
        }
        let mods = &mut self.tables.mods.list;
        let mut added = false;
        for i in 0..nsec {
            let s = opt + opt_size + i * 40;
            let (vsize, va, ch) = (rd32(s + 8), rd32(s + 12), rd32(s + 36));
            if ch & 0x2000_0000 != 0 && vsize > 0 && mods.len() < MAX_MOD_RANGES {
                mods.push(Range::new(base + va, base + va + vsize, cat));
                added = true;
            }
        }
        if !added && mods.len() < MAX_MOD_RANGES {
            mods.push(Range::new(base, base + rd32(opt + 56), cat));
        }
    }

    fn refresh_modules(&mut self) {
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
        self.tables.mods.list.clear();
        for &hm in &handles[..n] {
            if hm.is_null() {
                continue;
            }
            let mut wname = [0u16; 260];
            // SAFETY: a module of this process; buffer as declared.
            let wl = unsafe {
                K32GetModuleBaseNameW(
                    GetCurrentProcess(),
                    hm,
                    wname.as_mut_ptr(),
                    wname.len() as u32,
                )
            } as usize;
            if wl == 0 {
                continue;
            }
            let name: String = beside::narrow(&wname[..wl]).chars().take(64).collect();
            let Some(cat) = self.intern_module(&name) else {
                continue;
            };
            self.mod_bases[cat as usize] = hm as usize;
            self.add_image(hm as usize, cat);
            self.cat_bucket[cat as usize] = bucket_of(&name, self.mod_managed[cat as usize]);
            if name.eq_ignore_ascii_case(beside::CLIENT_EXE) {
                self.exe_cat = cat;
            }
        }
        self.tables.mods.seal();
    }

    fn load_regions(&mut self) {
        self.tables.regions.clear();
        self.regions_note.clear();
        let Some(text) = beside::read(RANGES, 4096) else {
            return;
        };
        if self.exe_cat as usize >= MAX_MODULES {
            return;
        }
        self.tables.regions.list = parse_regions(
            &String::from_utf8_lossy(&text),
            self.mod_bases[self.exe_cat as usize],
            MAX_REGIONS,
        );
        self.tables.regions.seal();
        self.regions_note = format!(
            "{} exe ranges from tidelab_prof_ranges.txt",
            self.tables.regions.list.len()
        );
    }

    // -----------------------------------------------------------------------------------------
    // Threads
    // -----------------------------------------------------------------------------------------

    fn find_thread(&self, tid: u32) -> Option<usize> {
        self.threads.iter().position(|t| t.tid == tid)
    }

    fn add_thread(&mut self, tid: u32) -> Option<usize> {
        if self.threads.len() == MAX_THREADS {
            return None;
        }
        let mut t = ThreadInfo {
            tid,
            start_cat: CAT_UNKNOWN,
            ..ThreadInfo::default()
        };
        // SAFETY: plain opens; the handle is closed when the thread is gone.
        let h = unsafe {
            let h = OpenThread(
                THREAD_SUSPEND_RESUME
                    | THREAD_GET_CONTEXT
                    | THREAD_QUERY_INFORMATION
                    | THREAD_QUERY_LIMITED_INFORMATION,
                0,
                tid,
            );
            t.can_sample = !h.is_null();
            if h.is_null() {
                OpenThread(THREAD_QUERY_LIMITED_INFORMATION, 0, tid)
            } else {
                h
            }
        };
        t.handle = h as usize;
        if !h.is_null() {
            if let Some((cpu, created)) = thread_times(h) {
                t.last_cpu = cpu;
                t.created = created;
            }
            if t.can_sample {
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
                if st >= 0 {
                    t.start_cat = self.tables.lookup(start_addr).unwrap_or(CAT_UNKNOWN);
                }
                let mut tbi = nt::ThreadBasicInformation::default();
                // SAFETY: ThreadBasicInformation into its struct.
                let st = unsafe {
                    nt::NtQueryInformationThread(
                        h,
                        nt::THREAD_BASIC_INFORMATION_CLASS,
                        (&raw mut tbi).cast(),
                        size_of::<nt::ThreadBasicInformation>() as u32,
                        std::ptr::null_mut(),
                    )
                };
                if st >= 0 && tbi.teb_base_address != 0 {
                    // SAFETY: a TEB of this process; NT_TIB.StackBase is its second word.
                    t.stack_base = unsafe {
                        std::ptr::read_volatile((tbi.teb_base_address as *const usize).add(1))
                    };
                }
            }
        }
        // Exclude protected background threads from sampling.
        if let Some(n) = self.mod_names.get(t.start_cat as usize) {
            let n = n.to_ascii_lowercase();
            if n.starts_with("ace") || n.contains("sguard") || n.contains("tersafe") {
                t.excluded = true;
            }
        }
        if t.start_cat == CAT_UNKNOWN && tid != self.main_tid {
            t.excluded = true; // started outside any listed module
        }
        self.threads.push(t);
        Some(self.threads.len() - 1)
    }

    fn refresh_threads(&mut self) {
        // SAFETY: closed below.
        let snap = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0) };
        if snap == INVALID_HANDLE_VALUE {
            return;
        }
        for t in &mut self.threads {
            t.alive = false;
        }
        // SAFETY: plain data.
        let mut e: THREADENTRY32 = unsafe { std::mem::zeroed() };
        e.dwSize = size_of::<THREADENTRY32>() as u32;
        // SAFETY: `e` is sized.
        let mut ok = unsafe { Thread32First(snap, &mut e) };
        while ok != 0 {
            if e.th32OwnerProcessID == self.pid {
                let i = self
                    .find_thread(e.th32ThreadID)
                    .or_else(|| self.add_thread(e.th32ThreadID));
                if let Some(i) = i {
                    let t = &mut self.threads[i];
                    t.alive = true;
                    if t.handle != 0 {
                        if let Some((cpu, _)) = thread_times(t.handle as HANDLE) {
                            t.cpu_last_s = cpu.wrapping_sub(t.last_cpu);
                            t.cpu_session += t.cpu_last_s;
                            t.last_cpu = cpu;
                        }
                        if t.name.is_empty() {
                            t.name = thread_name(t.handle as HANDLE);
                        }
                    }
                }
            }
            // SAFETY: as above.
            ok = unsafe { Thread32Next(snap, &mut e) };
        }
        // SAFETY: opened above.
        unsafe { CloseHandle(snap) };

        self.threads.retain(|t| {
            if !t.alive && t.handle != 0 {
                // SAFETY: a handle we opened.
                unsafe { CloseHandle(t.handle as HANDLE) };
            }
            t.alive
        });

        if self.main_tid == 0 {
            // The process's first thread is UE's GameThread.
            let best = self
                .threads
                .iter_mut()
                .filter(|t| t.handle != 0 && t.created != 0)
                .min_by_key(|t| t.created);
            if let Some(b) = best {
                self.main_tid = b.tid;
                b.excluded = false;
            }
        }
    }

    fn slot_for(&mut self, ti: usize) -> Option<usize> {
        let t = &self.threads[ti];
        if let Some(i) = self.slots.iter().position(|s| s.tid == t.tid) {
            if !t.name.is_empty() {
                self.slots[i].name = t.name.clone();
            }
            return Some(i);
        }
        if self.slots.len() == MAX_SLOTS {
            return None;
        }
        self.slots.push(Slot::new(t.tid, &t.name));
        Some(self.slots.len() - 1)
    }

    fn sampleable(&self, t: &ThreadInfo) -> bool {
        t.can_sample
            && !t.excluded
            && t.tid != self.self_tid
            && t.tid != SHARED.watchdog_tid.load(Ordering::Acquire)
            && t.stack_base != 0
    }

    /// Extra targets come only from the game's own named threads (UE names them; started in the
    /// exe). R-16: an unnamed thread picked by CPU use once left the sampler stuck for good.
    fn safe_extra(&self, t: &ThreadInfo) -> bool {
        !t.name.is_empty() && t.start_cat == self.exe_cat
    }

    /// False when there is no room (targets or per-session slots full).
    fn add_target(&mut self, ti: usize, role: Role) -> bool {
        if self.targets.len() == MAX_TARGETS {
            return false;
        }
        let Some(slot) = self.slot_for(ti) else {
            return false;
        };
        let t = &self.threads[ti];
        self.targets.push(Target {
            handle: t.handle,
            stack_base: t.stack_base,
            slot,
            role,
        });
        true
    }

    fn targeted(&self, tid: u32) -> bool {
        self.targets.iter().any(|tg| self.slots[tg.slot].tid == tid)
    }

    fn add_named(&mut self, prefix: &str, role: Role) {
        let found = self.threads.iter().position(|t| {
            self.sampleable(t) && !self.targeted(t.tid) && t.name.starts_with(prefix)
        });
        if let Some(i) = found {
            self.add_target(i, role);
        }
    }

    fn pick_targets(&mut self) {
        self.targets.clear();
        if let Some(m) = self.find_thread(self.main_tid)
            && self.sampleable(&self.threads[m])
        {
            self.add_target(m, Role::Main);
        }
        self.add_named("RenderThread", Role::Render);
        self.add_named("RHIThread", Role::Rhi);
        while self.targets.len() < MAX_TARGETS {
            let best = self
                .threads
                .iter()
                .enumerate()
                .filter(|(_, t)| {
                    self.sampleable(t)
                        && self.safe_extra(t)
                        && t.cpu_last_s != 0
                        && !self.targeted(t.tid)
                })
                .max_by_key(|(_, t)| t.cpu_last_s)
                .map(|(i, _)| i);
            // R-16: once the session's slots are full, add_target adds nothing; without this
            // break the loop never ended and the sampler hung 30-70 s into a session.
            let Some(i) = best else { break };
            if !self.add_target(i, Role::Other) {
                break;
            }
        }
    }

    // -----------------------------------------------------------------------------------------
    // Names
    // -----------------------------------------------------------------------------------------

    fn cat_name(&self, cat: u16) -> &str {
        if let Some(n) = self.mod_names.get(cat as usize) {
            return n;
        }
        match cat {
            CAT_ANON => "[JIT/anon code]",
            CAT_NONE => "[no caller found]",
            CAT_V8_JS => "[exe: V8 builtins (JS running)]",
            CAT_V8_CPP => "[exe: V8 C++]",
            _ => "[unknown]",
        }
    }

    fn cat_bucket_of(&self, cat: u16) -> Bucket {
        match cat {
            CAT_ANON => Bucket::Jit,
            CAT_V8_JS => Bucket::Js,
            CAT_V8_CPP => Bucket::V8Cpp,
            c if c as usize >= self.mod_names.len() => Bucket::Other,
            c => bucket_of(self.cat_name(c), self.mod_managed[c as usize]),
        }
    }

    /// Offset printed after a code category: RVA inside the exe for the named exe parts.
    fn cat_offset_base(&self, cat: u16) -> usize {
        if let Some(&b) = self.mod_bases.get(cat as usize) {
            return b;
        }
        if cat == CAT_V8_JS || cat == CAT_V8_CPP {
            return self
                .mod_bases
                .get(self.exe_cat as usize)
                .copied()
                .unwrap_or(0);
        }
        0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn thread(tid: u32, name: &str, start_cat: u16, cpu_last_s: u64) -> ThreadInfo {
        ThreadInfo {
            tid,
            handle: 0x1234,
            can_sample: true,
            stack_base: 0x10000,
            start_cat,
            cpu_last_s,
            name: name.into(),
            ..ThreadInfo::default()
        }
    }

    #[test]
    fn pick_targets_ends_when_the_sessions_slots_are_full() {
        let mut s = Sampler::new();
        s.exe_cat = 0;
        s.main_tid = 1;
        // Every slot already holds some other thread; a busy named game thread wants one.
        for i in 0..MAX_SLOTS {
            s.slots.push(Slot::new(1000 + i as u32, ""));
        }
        s.threads.push(thread(77, "Pool", 0, 500));
        s.pick_targets(); // used to loop forever
        assert!(s.targets.is_empty());

        // With a free slot it is picked.
        s.slots.clear();
        s.pick_targets();
        assert_eq!(s.targets.len(), 1);
        assert_eq!(s.targets[0].role, Role::Other);
    }

    #[test]
    fn pick_targets_takes_the_game_render_and_rhi_threads_first() {
        let mut s = Sampler::new();
        s.exe_cat = 0;
        s.main_tid = 10;
        s.threads = vec![
            thread(10, "GameThread", 0, 1),
            thread(11, "RHIThread", 0, 1),
            thread(12, "RenderThread 1", 0, 1),
            thread(13, "Busy", 0, 900),
            thread(14, "Busier but not the game's", 3, 999),
            thread(15, "", 0, 999),
        ];
        s.pick_targets();
        let roles: Vec<(u32, Role)> = s
            .targets
            .iter()
            .map(|t| (s.slots[t.slot].tid, t.role))
            .collect();
        assert_eq!(
            roles,
            vec![
                (10, Role::Main),
                (12, Role::Render),
                (11, Role::Rhi),
                (13, Role::Other)
            ]
        );
    }

    #[test]
    fn roles_are_due_every_1_2_and_4_ticks() {
        let due = |r: Role| (0..8).filter(|&t| r.due(t)).count();
        assert_eq!(
            (
                due(Role::Main),
                due(Role::Render),
                due(Role::Rhi),
                due(Role::Other)
            ),
            (8, 4, 4, 2)
        );
    }

    #[test]
    fn a_slice_of_busy_game_thread_samples_is_a_hitch_and_feeds_the_hitch_slot() {
        let mut s = Sampler::new();
        for _ in 0..30 {
            s.record_slice(
                Role::Main,
                &Sample {
                    leaf: 0,
                    sys: false,
                    leaf_payload: 42,
                    js_stack: true,
                },
            );
        }
        s.record_slice(
            Role::Render,
            &Sample {
                leaf: 0,
                sys: true,
                leaf_payload: 1,
                js_stack: false,
            },
        );
        s.close_slice(0);
        let row = s.slices[0];
        assert!(row.hitch);
        assert_eq!((row.n, row.wait, row.js_busy), ([30, 1, 0], [0, 1, 0], 30));
        assert_eq!(s.hitch_samples, 30);
        assert_eq!(s.hot.top(HITCH_SLOT, HOT_LEAF)[0].count, 30);
        assert!(s.cur_payloads.is_empty());
    }

    #[test]
    fn cat_names_cover_modules_and_the_special_categories() {
        let mut s = Sampler::new();
        assert_eq!(s.intern_module("ntdll.dll"), Some(0));
        assert_eq!(s.intern_module("ntdll.dll"), Some(0));
        assert_eq!(s.cat_name(0), "ntdll.dll");
        assert_eq!(s.cat_name(CAT_ANON), "[JIT/anon code]");
        assert_eq!(s.cat_name(5), "[unknown]");
        assert_eq!(s.cat_bucket_of(0), Bucket::OsRun);
        assert_eq!(s.cat_bucket_of(CAT_V8_JS), Bucket::Js);
    }
}
