// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The sampler's pure parts: address categories, the stack scan, owner buckets, time slices and
//! the hot table. No Win32 here; all of it is unit tested.

pub const MAX_MODULES: usize = 1024;

/// Executable private or mapped memory: JIT code (Mono or V8).
pub const CAT_ANON: u16 = MAX_MODULES as u16;
pub const CAT_UNKNOWN: u16 = MAX_MODULES as u16 + 1;
/// "No caller found on the stack".
pub const CAT_NONE: u16 = MAX_MODULES as u16 + 2;
/// R-17: parts of the exe named by tidelab_prof_ranges.txt. V8's embedded builtins (JS running)...
pub const CAT_V8_JS: u16 = MAX_MODULES as u16 + 3;
/// ...and V8's C++ (runtime, GC, compiler).
pub const CAT_V8_CPP: u16 = MAX_MODULES as u16 + 4;
pub const CAT_COUNT: usize = MAX_MODULES + 5;

/// A code address that is real native code (a module or a named part of the exe), so it can be
/// "the caller".
pub fn is_native_cat(cat: u16) -> bool {
    (cat as usize) < MAX_MODULES || cat == CAT_V8_JS || cat == CAT_V8_CPP
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct Range {
    pub start: usize,
    pub end: usize,
    pub cat: u16,
    /// For JIT/anon regions: the allocation base.
    pub alloc: usize,
}

impl Range {
    pub fn new(start: usize, end: usize, cat: u16) -> Self {
        Range {
            start,
            end,
            cat,
            alloc: 0,
        }
    }
}

/// Index of the range holding `addr` in `rs` (sorted by start, not overlapping).
pub fn find_index(rs: &[Range], addr: usize) -> Option<usize> {
    let (mut lo, mut hi) = (0, rs.len());
    while lo < hi {
        let mid = (lo + hi) / 2;
        if addr < rs[mid].start {
            hi = mid;
        } else if addr >= rs[mid].end {
            lo = mid + 1;
        } else {
            return Some(mid);
        }
    }
    None
}

/// A sorted list of ranges with its overall bounds, for a quick reject.
#[derive(Debug, Default, Clone)]
pub struct RangeSet {
    pub list: Vec<Range>,
    pub lo: usize,
    pub hi: usize,
}

impl RangeSet {
    pub fn clear(&mut self) {
        self.list.clear();
        self.lo = 0;
        self.hi = 0;
    }

    /// Sorts the list and sets the bounds.
    pub fn seal(&mut self) {
        self.list.sort_by_key(|r| r.start);
        if let (Some(f), Some(l)) = (self.list.first(), self.list.last()) {
            self.lo = f.start;
            self.hi = l.end;
        } else {
            self.lo = 0;
            self.hi = 0;
        }
    }

    pub fn find(&self, addr: usize) -> Option<&Range> {
        if addr < self.lo || addr >= self.hi {
            return None;
        }
        find_index(&self.list, addr).map(|i| &self.list[i])
    }
}

/// The three lookups of a code address: named parts of the exe first, then modules, then
/// JIT/anon regions.
#[derive(Debug, Default, Clone)]
pub struct Tables {
    pub regions: RangeSet,
    pub mods: RangeSet,
    pub anons: RangeSet,
}

impl Tables {
    pub fn lookup(&self, addr: usize) -> Option<u16> {
        self.regions
            .find(addr)
            .or_else(|| self.mods.find(addr))
            .or_else(|| self.anons.find(addr))
            .map(|r| r.cat)
    }
}

/// The two bytes before a suspended thread's RIP are `syscall` (0F 05): it is inside the kernel,
/// almost always waiting.
pub fn follows_syscall(before: &[u8]) -> bool {
    before == [0x0F, 0x05]
}

/// Stack scan of one sample. Every category with a code address on the copied stack counts once
/// in `incl` (approximate inclusive time; `seen[c] == stamp` marks it). Returns the first module
/// (not JIT code) on the stack other than `leaf`: the nearest native caller.
pub fn scan_stack(
    words: &[usize],
    t: &Tables,
    leaf: u16,
    seen: &mut [u32],
    stamp: u32,
    incl: &mut [u32],
) -> u16 {
    let mut caller = CAT_NONE;
    for &w in words {
        let Some(cat) = t.lookup(w) else { continue };
        if seen[cat as usize] != stamp {
            seen[cat as usize] = stamp;
            incl[cat as usize] += 1;
        }
        if caller == CAT_NONE && cat != leaf && is_native_cat(cat) {
            caller = cat;
        }
    }
    caller
}

/// Who owns the time: the sections of the report and the columns of the slices CSV.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Bucket {
    Ue,
    Clr,
    Js,
    Gpu,
    OsRun,
    OsWait,
    Jit,
    V8Cpp,
    Other,
}

pub const BUCKET_COUNT: usize = 9;

impl Bucket {
    pub const ALL: [Bucket; BUCKET_COUNT] = [
        Bucket::Ue,
        Bucket::Clr,
        Bucket::Js,
        Bucket::Gpu,
        Bucket::OsRun,
        Bucket::OsWait,
        Bucket::Jit,
        Bucket::V8Cpp,
        Bucket::Other,
    ];

    /// The short name, as the CSV columns and the reports have it.
    pub fn tag(self) -> &'static str {
        match self {
            Bucket::Ue => "ue",
            Bucket::Clr => "clr",
            Bucket::Js => "js",
            Bucket::Gpu => "gpu",
            Bucket::OsRun => "os_run",
            Bucket::OsWait => "os_wait",
            Bucket::Jit => "jit",
            Bucket::V8Cpp => "v8_cpp",
            Bucket::Other => "other",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Bucket::Ue => "Client-Win64-Shipping.exe outside V8 (UE C++, Puerts glue)",
            Bucket::Clr => "C# / .NET (coreclr, managed images)",
            Bucket::Js => "JS running in V8 builtins (interpreter, ICs)",
            Bucket::V8Cpp => "V8 C++ (runtime, GC, compiler)",
            Bucket::Gpu => "GPU driver / D3D",
            Bucket::OsRun => "OS libraries, running",
            Bucket::OsWait => "OS, inside a syscall (waiting)",
            Bucket::Jit => "JIT / anonymous code (CLR or V8)",
            Bucket::Other => "other modules",
        }
    }
}

fn starts_with_lower(name: &str, prefix: &str) -> bool {
    name.len() >= prefix.len()
        && name.as_bytes()[..prefix.len()].eq_ignore_ascii_case(prefix.as_bytes())
}

fn contains_lower(name: &str, needle: &str) -> bool {
    name.to_ascii_lowercase().contains(needle)
}

/// The owner of a module, by name (and by its .NET header: `managed`).
pub fn bucket_of(name: &str, managed: bool) -> Bucket {
    if managed {
        return Bucket::Clr;
    }
    if starts_with_lower(name, "client-win64-shipping") {
        return Bucket::Ue;
    }
    // Managed images are caught by their .NET header; by name only the runtime's native parts
    // (not "microsoft.": Microsoft.Internal.WarpPal.dll is D3D's WARP).
    const CLR: [&str; 7] = [
        "coreclr",
        "clrjit",
        "clrgc",
        "hostfxr",
        "hostpolicy",
        "system.",
        "mscor",
    ];
    const JS: [&str; 4] = ["v8", "puerts", "libnode", "node.dll"];
    const GPU: [&str; 20] = [
        "nvwgf2um",
        "nvd3dum",
        "nvoglv",
        "nvldumd",
        "nvgpucomp",
        "nvapi",
        "nvppe",
        "amdxx",
        "atiumd",
        "aticfx",
        "amdihk",
        "amdenc",
        "igd",
        "igc",
        "intelocl",
        "d3d",
        "dxgi",
        "dxcore",
        "dxil",
        "dxcompiler",
    ];
    const OS: [&str; 46] = [
        "ntdll",
        "kernel32",
        "kernelbase",
        "win32u",
        "user32",
        "gdi32",
        "ucrtbase",
        "msvcp",
        "vcruntime",
        "combase",
        "rpcrt4",
        "ws2_32",
        "mswsock",
        "advapi32",
        "sechost",
        "bcrypt",
        "crypt",
        "ole32",
        "oleaut32",
        "shell32",
        "msvcrt",
        "xinput",
        "dinput",
        "hid",
        "setupapi",
        "cfgmgr32",
        "winmm",
        "mmdevapi",
        "audioses",
        "xaudio",
        "dsound",
        "dbghelp",
        "powrprof",
        "dwmapi",
        "uxtheme",
        "imm32",
        "nsi",
        "iphlpapi",
        "dnsapi",
        "wintrust",
        "msasn1",
        "secur32",
        "sspicli",
        "kernel.appcore",
        "windows.",
        "textinputframework",
    ];
    if CLR.iter().any(|p| starts_with_lower(name, p)) {
        return Bucket::Clr;
    }
    if JS.iter().any(|p| contains_lower(name, p)) {
        return Bucket::Js;
    }
    if GPU.iter().any(|p| starts_with_lower(name, p)) {
        return Bucket::Gpu;
    }
    if OS.iter().any(|p| starts_with_lower(name, p)) {
        return Bucket::OsRun;
    }
    Bucket::Other
}

/// R-17: named parts of the exe from tidelab_prof_ranges.txt. One per line,
/// `v8_js <start rva hex> <end rva hex>` or `v8_cpp ...`; `#` starts a comment; ranges must not
/// overlap. RVAs are for one game build: redo them after an update. At most `max` ranges.
pub fn parse_regions(text: &str, exe_base: usize, max: usize) -> Vec<Range> {
    let hex = |s: &str| {
        usize::from_str_radix(s.trim_start_matches("0x").trim_start_matches("0X"), 16).ok()
    };
    let mut out = Vec::new();
    for line in text.lines() {
        let line = line.trim_end_matches('\r');
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let mut f = line.split([' ', '\t']).filter(|s| !s.is_empty());
        let (Some(name), Some(a), Some(b)) =
            (f.next(), f.next().and_then(hex), f.next().and_then(hex))
        else {
            continue;
        };
        let cat = match name {
            "v8_js" => CAT_V8_JS,
            "v8_cpp" => CAT_V8_CPP,
            _ => continue,
        };
        if b <= a || out.len() == max {
            continue;
        }
        out.push(Range::new(exe_base + a, exe_base + b, cat));
    }
    out.sort_by_key(|r| r.start);
    out
}

// ---------------------------------------------------------------------------------------------
// Time slices (R-16): 100 ms each; which threads were busy, and where GameThread's time went
// ---------------------------------------------------------------------------------------------

pub const HITCH_BUSY_PCT: u32 = 90;
pub const HITCH_MIN_SAMPLES: u32 = 20;

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct SliceRow {
    pub index: u32,
    /// Samples: main, render, rhi.
    pub n: [u16; 3],
    /// Of which inside a syscall.
    pub wait: [u16; 3],
    pub main_bucket: [u16; BUCKET_COUNT],
    pub hitch: bool,
    /// GameThread busy samples with a JS frame on the stack.
    pub js_busy: u16,
}

/// GameThread computed (did not wait in the kernel) for at least 90% of a slice with enough
/// samples: a frame there took at least ~100 ms of game-thread work.
pub fn is_hitch(row: &SliceRow) -> bool {
    let n = u32::from(row.n[0]);
    n >= HITCH_MIN_SAMPLES && (n - u32::from(row.wait[0])) * 100 >= n * HITCH_BUSY_PCT
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Episode {
    pub first: usize,
    pub len: usize,
}

/// The next run of consecutive hitch slices at or after `from`.
pub fn next_episode(rows: &[SliceRow], from: usize) -> Option<Episode> {
    let first = from + rows.get(from..)?.iter().position(|r| r.hitch)?;
    let len = rows[first..].iter().take_while(|r| r.hitch).count();
    Some(Episode { first, len })
}

/// Local wall-clock time (HH:MM:SS.d) of slice `index`, for a session that started at
/// `h:m:s.ms`.
pub fn clock_at(start: (u16, u16, u16, u16), index: u32) -> String {
    let (h, m, s, ms) = start;
    let start_ds =
        ((u64::from(h) * 60 + u64::from(m)) * 60 + u64::from(s)) * 10 + u64::from(ms) / 100;
    let t = (start_ds + u64::from(index)) % (24 * 3600 * 10);
    format!(
        "{:02}:{:02}:{:02}.{}",
        t / 36000,
        (t / 600) % 60,
        (t / 10) % 60,
        t % 10
    )
}

pub fn pct(a: u64, b: u64) -> f64 {
    if b == 0 {
        0.0
    } else {
        a as f64 * 100.0 / b as f64
    }
}

/// Busy (not inside a syscall) share of a slice's samples, or None without samples.
pub fn busy_pct(n: u64, wait: u64) -> Option<f64> {
    (n != 0).then(|| pct(n - wait, n))
}

// ---------------------------------------------------------------------------------------------
// The hot table: counts per (slot, kind, payload), open addressing, never resized.
// ---------------------------------------------------------------------------------------------

/// R-55: room for the JIT stack entries besides the leaves.
pub const HOT_BITS: u32 = 18;
/// Payload `cat << 44 | (offset >> 4)`.
pub const HOT_LEAF: u64 = 0;
/// Payload `leaf << 16 | caller`.
pub const HOT_PAIR: u64 = 1;
/// Payload: an anon region's AllocationBase >> 16.
pub const HOT_ALLOC: u64 = 2;
/// Payload: address >> 4 of a stack word pointing into JIT/anon code, once per sample; bit 56
/// set for the one nearest the stack pointer under an exe leaf.
pub const HOT_STACK: u64 = 3;
pub const PAYLOAD_MASK: u64 = (1 << 57) - 1;

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct HotEntry {
    pub key: u64,
    pub count: u32,
}

impl HotEntry {
    pub fn slot(&self) -> usize {
        ((self.key >> 59) & 0xF) as usize
    }

    pub fn kind(&self) -> u64 {
        (self.key >> 57) & 0x3
    }

    pub fn payload(&self) -> u64 {
        self.key & PAYLOAD_MASK
    }
}

pub struct Hot {
    pub entries: Vec<HotEntry>,
    pub used: usize,
    pub dropped: u32,
}

impl Default for Hot {
    fn default() -> Self {
        Hot {
            entries: vec![HotEntry::default(); 1 << HOT_BITS],
            used: 0,
            dropped: 0,
        }
    }
}

impl Hot {
    pub fn clear(&mut self) {
        self.entries.fill(HotEntry::default());
        self.used = 0;
        self.dropped = 0;
    }

    /// Counts one more; beyond three quarters full new keys are dropped (and counted).
    pub fn add(&mut self, slot: usize, kind: u64, payload: u64) {
        let key = (1 << 63) | ((slot as u64) << 59) | (kind << 57) | (payload & PAYLOAD_MASK);
        let len = self.entries.len();
        let mask = len - 1;
        let mut i = (key.wrapping_mul(0x9E37_79B9_7F4A_7C15) >> (64 - HOT_BITS)) as usize;
        loop {
            let e = &mut self.entries[i];
            if e.key == key {
                e.count += 1;
                return;
            }
            if e.key == 0 {
                if self.used * 4 >= len * 3 {
                    self.dropped += 1;
                    return;
                }
                *e = HotEntry { key, count: 1 };
                self.used += 1;
                return;
            }
            i = (i + 1) & mask;
        }
    }

    /// The entries of one slot and kind, most counted first.
    pub fn top(&self, slot: usize, kind: u64) -> Vec<HotEntry> {
        let mut v: Vec<HotEntry> = self
            .entries
            .iter()
            .filter(|e| e.key != 0 && e.slot() == slot && e.kind() == kind)
            .copied()
            .collect();
        v.sort_by_key(|e| std::cmp::Reverse(e.count));
        v
    }
}

/// Non-zero counts, most first, as (category, count).
pub fn top_cats(counts: &[u32]) -> Vec<(u16, u32)> {
    let mut v: Vec<(u16, u32)> = counts
        .iter()
        .enumerate()
        .filter(|(_, c)| **c != 0)
        .map(|(i, c)| (i as u16, *c))
        .collect();
    v.sort_by_key(|e| std::cmp::Reverse(e.1));
    v
}

#[cfg(test)]
mod tests {
    use super::*;

    fn set(list: &[Range]) -> RangeSet {
        let mut s = RangeSet {
            list: list.to_vec(),
            ..RangeSet::default()
        };
        s.seal();
        s
    }

    #[test]
    fn find_index_picks_the_containing_range_and_rejects_gaps() {
        let rs = [
            Range::new(0x1000, 0x2000, 1),
            Range::new(0x3000, 0x3100, 2),
            Range::new(0x8000, 0x9000, 3),
        ];
        assert_eq!(find_index(&rs, 0x1000), Some(0));
        assert_eq!(find_index(&rs, 0x1fff), Some(0));
        assert_eq!(find_index(&rs, 0x2000), None);
        assert_eq!(find_index(&rs, 0x30ff), Some(1));
        assert_eq!(find_index(&rs, 0x8abc), Some(2));
        assert_eq!(find_index(&rs, 0xfff), None);
        assert_eq!(find_index(&rs, 0x9000), None);
        assert_eq!(find_index(&[], 0x1234), None);
    }

    #[test]
    fn follows_syscall_matches_0f_05_only() {
        assert!(follows_syscall(&[0x0F, 0x05]));
        assert!(!follows_syscall(&[0xC3, 0x05]));
        assert!(!follows_syscall(&[0x0F, 0x0B]));
    }

    #[test]
    fn scan_stack_counts_each_module_once_and_finds_the_nearest_other_caller() {
        let t = Tables {
            mods: set(&[
                Range::new(0x7000_0000, 0x7000_1000, 0), // ntdll
                Range::new(0x7100_0000, 0x7200_0000, 1), // game exe
                Range::new(0x7300_0000, 0x7300_8000, 2), // coreclr
            ]),
            anons: set(&[Range::new(0x2_0000_0000, 0x2_0010_0000, CAT_ANON)]),
            ..Tables::default()
        };
        let mut seen = vec![0u32; CAT_COUNT];
        let mut incl = vec![0u32; CAT_COUNT];
        // Leaf in ntdll (cat 0): stack holds another ntdll address, data, JIT code, coreclr, the exe twice.
        seen[0] = 7;
        incl[0] = 1;
        let words = [
            0x7000_0010,
            42,
            0x2_0000_5000,
            0x7300_0100,
            0x7150_0000,
            0x7150_0004,
            0x1234_5678,
        ];
        let caller = scan_stack(&words, &t, 0, &mut seen, 7, &mut incl);
        assert_eq!(caller, 2); // coreclr: first module other than the leaf (JIT code is skipped)
        assert_eq!(
            (incl[0], incl[1], incl[2], incl[CAT_ANON as usize]),
            (1, 1, 1, 1)
        );
        // Nothing on the stack: no caller.
        assert_eq!(
            scan_stack(&[1, 2, 3], &t, 0, &mut seen, 8, &mut incl),
            CAT_NONE
        );
    }

    #[test]
    fn scan_stack_a_v8_builtin_frame_marks_the_sample_as_under_js() {
        let t = Tables {
            regions: set(&[Range::new(0x6000, 0x7000, CAT_V8_JS)]),
            mods: set(&[Range::new(0x1000, 0x9000, 0)]),
            ..Tables::default()
        };
        let mut seen = vec![0u32; CAT_COUNT];
        let mut incl = vec![0u32; CAT_COUNT];
        // Leaf in UE code (cat 0); deeper on the stack a return address into a builtin.
        scan_stack(&[0x2000, 99, 0x6100], &t, 0, &mut seen, 1, &mut incl);
        assert_eq!(seen[CAT_V8_JS as usize], 1);
        scan_stack(&[0x2000, 99], &t, 0, &mut seen, 2, &mut incl);
        assert_ne!(seen[CAT_V8_JS as usize], 2);
    }

    #[test]
    fn bucket_of_groups_modules_by_owner() {
        assert_eq!(bucket_of("Client-Win64-Shipping.exe", false), Bucket::Ue);
        assert_eq!(bucket_of("coreclr.dll", false), Bucket::Clr);
        assert_eq!(bucket_of("System.Private.CoreLib.dll", false), Bucket::Clr);
        assert_eq!(bucket_of("Aki.Game.dll", true), Bucket::Clr);
        assert_eq!(bucket_of("v8.dll", false), Bucket::Js);
        assert_eq!(bucket_of("libpuerts.dll", false), Bucket::Js);
        assert_eq!(bucket_of("nvwgf2umx.dll", false), Bucket::Gpu);
        assert_eq!(bucket_of("D3D12Core.dll", false), Bucket::Gpu);
        assert_eq!(bucket_of("ntdll.dll", false), Bucket::OsRun);
        assert_eq!(bucket_of("KERNELBASE.dll", false), Bucket::OsRun);
        assert_eq!(bucket_of("tidelab_patch.dll", false), Bucket::Other);
        assert_eq!(
            bucket_of("Microsoft.Internal.WarpPal.dll", false),
            Bucket::Other
        );
    }

    #[test]
    fn is_hitch_needs_enough_samples_and_90_percent_busy() {
        let row = |n: u16, wait: u16| SliceRow {
            n: [n, 0, 0],
            wait: [wait, 0, 0],
            ..SliceRow::default()
        };
        assert!(is_hitch(&row(68, 0)));
        assert!(is_hitch(&row(70, 7))); // exactly 90%
        assert!(!is_hitch(&row(70, 8)));
        assert!(!is_hitch(&row(68, 34))); // normal frame: half waiting
        assert!(!is_hitch(&row(10, 0))); // too few samples
        assert!(!is_hitch(&SliceRow::default()));
    }

    #[test]
    fn next_episode_groups_consecutive_hitch_slices() {
        let mut rows = [SliceRow::default(); 8];
        for i in [1, 2, 3, 6] {
            rows[i].hitch = true;
        }
        let a = next_episode(&rows, 0).unwrap();
        assert_eq!(a, Episode { first: 1, len: 3 });
        let b = next_episode(&rows, a.first + a.len).unwrap();
        assert_eq!(b, Episode { first: 6, len: 1 });
        assert_eq!(next_episode(&rows, b.first + b.len), None);
        assert_eq!(next_episode(&rows, 99), None);
    }

    #[test]
    fn clock_at_adds_slices_to_the_session_start() {
        let start = (23, 59, 59, 950);
        assert_eq!(clock_at(start, 0), "23:59:59.9");
        assert_eq!(clock_at(start, 1), "00:00:00.0");
        assert_eq!(clock_at(start, 125), "00:00:12.4");
    }

    #[test]
    fn parse_regions_reads_named_exe_ranges_and_skips_junk() {
        let text = "# 3.7.2\r\nv8_cpp 0x5f48000 0x632c3d0\r\nv8_js 632c3d0 64e928c\r\nv8_cpp 0x64e928c 0x6d2c000\nbogus 1 2\nv8_js 10 5\n";
        let out = parse_regions(text, 0x1_4000_0000, 4);
        assert_eq!(out.len(), 3);
        assert_eq!(out[0].start, 0x1_45f4_8000);
        assert_eq!(out[1].cat, CAT_V8_JS);
        assert_eq!(out[1].end, 0x1_464e_928c);
        let t = Tables {
            regions: set(&out),
            ..Tables::default()
        };
        assert_eq!(t.lookup(0x1_4632_df90), Some(CAT_V8_JS)); // an R-16 hitch hot spot
        assert_eq!(t.lookup(0x1_462c_5970), Some(CAT_V8_CPP)); // V8 GC slot update
        assert_eq!(t.lookup(0x1_433f_c9a0), None); // UE object array lookup
        assert_eq!(parse_regions(text, 0, 1).len(), 1);
    }

    #[test]
    fn hot_table_keeps_slot_and_kind_apart() {
        let mut hot = Hot::default();
        hot.add(0, HOT_LEAF, 5);
        hot.add(0, HOT_LEAF, 5);
        hot.add(1, HOT_LEAF, 5);
        hot.add(0, HOT_PAIR, 5);
        assert_eq!(hot.top(0, HOT_LEAF)[0].count, 2);
        assert_eq!(hot.top(0, HOT_LEAF)[0].payload(), 5);
        assert_eq!(hot.top(1, HOT_LEAF).len(), 1);
        assert_eq!(hot.top(0, HOT_PAIR).len(), 1);
        assert_eq!(hot.used, 3);
    }

    #[test]
    fn hot_table_drops_new_keys_beyond_three_quarters() {
        let mut hot = Hot {
            entries: vec![HotEntry::default(); 1 << HOT_BITS],
            used: (1 << HOT_BITS) * 3 / 4,
            dropped: 0,
        };
        hot.add(0, HOT_LEAF, 1);
        assert_eq!(hot.dropped, 1);
    }

    #[test]
    fn top_cats_sorts_non_zero_counts() {
        assert_eq!(top_cats(&[0, 3, 0, 7, 1]), vec![(3, 7), (1, 3), (4, 1)]);
    }
}
