// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The session's files: `tidelab_prof_<pid>_<n>.txt` (the report), `_slices.csv` (one row per
//! 100 ms) and, at the end, `_hot.csv` (every hot leaf and JIT stack address, for
//! hot_functions.py and compare_hot.py in the lab).

use std::ffi::{c_char, c_void};
use std::fmt::Write as _;

use windows_sys::Win32::System::LibraryLoader::{GetModuleHandleW, GetProcAddress};

use super::tables::*;
use super::{HITCH_SLOT, MAX_THREADS, STACK_COPY_WORDS, Sampler, now, self_cpu};
use crate::beside;

/// R-55: the client's .NET runtime is Mono (coreclr.dll exports the mono_* API). mono_pmip names
/// the method (or wrapper/trampoline) that holds a code address, also for methods the client
/// compiled ahead from jit_profile.dat, which its "Jit:" log lines miss. Used once, after a
/// session has stopped sampling, on the sampler thread attached to the runtime.
struct Mono {
    pmip: unsafe extern "C" fn(*mut c_void) -> *mut c_char,
    free: unsafe extern "C" fn(*mut c_void),
    detach: unsafe extern "C" fn(*mut c_void),
    attached: *mut c_void,
}

impl Mono {
    fn attach() -> Option<Mono> {
        let name: Vec<u16> = "coreclr.dll\0".encode_utf16().collect();
        // SAFETY: looks up exports by name; their signatures are Mono's embedding API.
        unsafe {
            let m = GetModuleHandleW(name.as_ptr());
            if m.is_null() {
                return None;
            }
            type Export = unsafe extern "system" fn() -> isize;
            type NoArgPtr = unsafe extern "C" fn() -> *mut c_void;
            type PtrPtr = unsafe extern "C" fn(*mut c_void) -> *mut c_void;
            type PtrVoid = unsafe extern "C" fn(*mut c_void);
            type PtrStr = unsafe extern "C" fn(*mut c_void) -> *mut c_char;
            let get = |n: &[u8]| GetProcAddress(m, n.as_ptr());
            let root_domain =
                std::mem::transmute::<Export, NoArgPtr>(get(b"mono_get_root_domain\0")?);
            let thread_attach =
                std::mem::transmute::<Export, PtrPtr>(get(b"mono_thread_attach\0")?);
            let detach = std::mem::transmute::<Export, PtrVoid>(get(b"mono_thread_detach\0")?);
            let pmip = std::mem::transmute::<Export, PtrStr>(get(b"mono_pmip\0")?);
            let free = std::mem::transmute::<Export, PtrVoid>(get(b"mono_free\0")?);
            let domain = root_domain();
            if domain.is_null() {
                return None;
            }
            let attached = thread_attach(domain);
            // Only an attached thread gets a Mono, whose drop detaches it.
            (!attached.is_null()).then_some(Mono {
                pmip,
                free,
                detach,
                attached,
            })
        }
    }

    /// The name mono_pmip gives the code at `addr` (middle of its 16-byte bucket), CSV-safe.
    fn name(&self, addr: u64) -> String {
        // SAFETY: mono_pmip returns a NUL-terminated string to be freed with mono_free, or null.
        unsafe {
            let s = (self.pmip)((addr + 8) as usize as *mut c_void);
            if s.is_null() {
                return String::new();
            }
            let text: String = std::ffi::CStr::from_ptr(s)
                .to_bytes()
                .iter()
                .take(512)
                .map(|&b| match b {
                    b'"' | b',' | b'\r' | b'\n' => ' ',
                    b => b as char,
                })
                .collect();
            (self.free)(s.cast());
            text
        }
    }
}

impl Drop for Mono {
    fn drop(&mut self) {
        // SAFETY: attached in `attach`.
        unsafe { (self.detach)(self.attached) };
    }
}

/// Sums of slice rows, for the report.
#[derive(Default)]
struct SliceSums {
    rows: u64,
    n: [u64; 3],
    wait: [u64; 3],
    bucket: [u64; BUCKET_COUNT],
    js_busy: u64,
}

impl SliceSums {
    fn add(&mut self, r: &SliceRow) {
        self.rows += 1;
        for k in 0..3 {
            self.n[k] += u64::from(r.n[k]);
            self.wait[k] += u64::from(r.wait[k]);
        }
        for (b, &c) in r.main_bucket.iter().enumerate() {
            self.bucket[b] += u64::from(c);
        }
        self.js_busy += u64::from(r.js_busy);
    }

    fn js_pct(&self) -> f64 {
        pct(self.js_busy, self.n[0] - self.wait[0])
    }

    fn busy(&self, k: usize) -> f64 {
        busy_pct(self.n[k], self.wait[k]).unwrap_or(0.0)
    }

    fn print(&self, w: &mut String, label: &str) {
        let _ = write!(
            w,
            "{label:<22} {:>5} slices  GameThread busy {:>5.1}%  RenderThread busy {:>5.1}%  RHIThread busy {:>5.1}%  GameThread busy time under a JS call {:.1}%",
            self.rows,
            self.busy(0),
            self.busy(1),
            self.busy(2),
            self.js_pct()
        );
        w.push_str("\r\n    GameThread time:");
        for (b, &c) in self.bucket.iter().enumerate() {
            if c != 0 {
                let _ = write!(w, "  {} {:.1}%", Bucket::ALL[b].tag(), pct(c, self.n[0]));
            }
        }
        w.push_str("\r\n");
    }
}

/// `cat << 44 | offset >> 4` back to the category and the offset.
fn split_leaf(p: u64) -> (u16, u64) {
    ((p >> 44) as u16, (p & ((1 << 44) - 1)) << 4)
}

impl Sampler {
    pub(super) fn write_report(&mut self, final_: bool) {
        self.write_session_file(".txt", self.report_body(final_).as_bytes());
        self.write_session_file("_slices.csv", self.slices_csv().as_bytes());
        if final_ {
            self.write_session_file("_hot.csv", self.hot_csv().as_bytes());
        }
    }

    /// tidelab_prof_<pid>_<session><suffix> beside the DLL.
    fn write_session_file(&self, suffix: &str, data: &[u8]) {
        beside::write(
            &format!("tidelab_prof_{}_{}{}", self.pid, self.session_no, suffix),
            data,
        );
    }

    fn clock(&self, index: u32) -> String {
        let c = &self.session_clock;
        clock_at((c.wHour, c.wMinute, c.wSecond, c.wMilliseconds), index)
    }

    fn hot_csv(&self) -> String {
        let mut w = String::with_capacity(1 << 20);
        w.push_str("slot,thread,tid,category,where,samples,name\r\n");
        let mono = Mono::attach();
        let name_of = |addr: u64| mono.as_ref().map(|m| m.name(addr)).unwrap_or_default();
        for e in self
            .hot
            .entries
            .iter()
            .filter(|e| e.key != 0 && (e.kind() == HOT_LEAF || e.kind() == HOT_STACK))
        {
            let si = e.slot();
            let p = e.payload();
            let (name, tid) = if si == HITCH_SLOT {
                ("hitch slices (GameThread)", 0)
            } else if let Some(s) = self.slots.get(si) {
                (s.name.as_str(), s.tid)
            } else {
                ("?", 0)
            };
            if e.kind() == HOT_STACK {
                let addr = (p & ((1 << 56) - 1)) << 4;
                let label = if (p >> 56) & 1 != 0 {
                    "[JIT caller of exe]"
                } else {
                    "[JIT stack]"
                };
                let _ = write!(
                    w,
                    "{si},\"{name}\",{tid},{label},0x{addr:x},{},\"{}\"\r\n",
                    e.count,
                    name_of(addr)
                );
                continue;
            }
            let (cat, off) = split_leaf(p);
            let method = if cat == CAT_ANON {
                name_of(off)
            } else {
                String::new()
            };
            let _ = write!(
                w,
                "{si},\"{name}\",{tid},{},0x{off:x},{},\"{method}\"\r\n",
                self.cat_name(cat),
                e.count
            );
        }
        w
    }

    fn slices_csv(&self) -> String {
        let c = &self.session_clock;
        let mut w = String::with_capacity(self.slices.len() * 64 + 512);
        let _ = write!(
            w,
            "# session start {:02}:{:02}:{:02}.{:03} local; one row per 100 ms; busy = not inside a syscall; main_* = GameThread time by owner (%)\r\n",
            c.wHour, c.wMinute, c.wSecond, c.wMilliseconds
        );
        w.push_str("t_s,clock,main_n,main_busy,render_busy,rhi_busy");
        for b in Bucket::ALL {
            let _ = write!(w, ",main_{}", b.tag());
        }
        w.push_str(",main_busy_js_on_stack,hitch\r\n");
        for r in &self.slices {
            let _ = write!(
                w,
                "{:.1},{},{}",
                f64::from(r.index) / 10.0,
                self.clock(r.index),
                r.n[0]
            );
            for k in 0..3 {
                match busy_pct(u64::from(r.n[k]), u64::from(r.wait[k])) {
                    Some(p) => {
                        let _ = write!(w, ",{p:.0}");
                    }
                    None => w.push(','),
                }
            }
            for &c in &r.main_bucket {
                let _ = write!(w, ",{:.0}", pct(u64::from(c), u64::from(r.n[0])));
            }
            let _ = write!(
                w,
                ",{:.0},{}\r\n",
                pct(u64::from(r.js_busy), u64::from(r.n[0] - r.wait[0])),
                u8::from(r.hitch)
            );
        }
        w
    }

    fn slices_report(&self, w: &mut String) {
        let c = &self.session_clock;
        let rows = &self.slices;
        let _ = write!(
            w,
            "\r\n== Time slices (100 ms; session start {:02}:{:02}:{:02}.{:03}); hitch = GameThread busy >= {HITCH_BUSY_PCT}% of a slice with >= {HITCH_MIN_SAMPLES} samples ==\r\n",
            c.wHour, c.wMinute, c.wSecond, c.wMilliseconds
        );
        let (mut normal, mut hitch) = (SliceSums::default(), SliceSums::default());
        for r in rows {
            if r.hitch { hitch.add(r) } else { normal.add(r) }
        }
        normal.print(w, "normal slices");
        hitch.print(w, "hitch slices");

        w.push_str("\r\n-- Hitch episodes (consecutive hitch slices), in time order --\r\n");
        let mut from = 0;
        let mut shown = 0;
        while let Some(e) = next_episode(rows, from) {
            from = e.first + e.len;
            if shown == 60 {
                break;
            }
            shown += 1;
            let mut s = SliceSums::default();
            for r in &rows[e.first..e.first + e.len] {
                s.add(r);
            }
            let _ = write!(
                w,
                "{}  t={:.1}s  {} ms  GameThread busy {:.0}%  Render busy {:.0}%  RHI busy {:.0}%   ",
                self.clock(rows[e.first].index),
                f64::from(rows[e.first].index) / 10.0,
                e.len * 100,
                s.busy(0),
                s.busy(1),
                s.busy(2)
            );
            for (b, &c) in s.bucket.iter().enumerate() {
                if pct(c, s.n[0]) >= 2.0 {
                    let _ = write!(w, " {} {:.0}%", Bucket::ALL[b].tag(), pct(c, s.n[0]));
                }
            }
            let _ = write!(w, "  | under JS {:.0}%\r\n", s.js_pct());
        }
        if shown == 0 {
            w.push_str("(none)\r\n");
        }
        let _ = write!(
            w,
            "GameThread stack copies cut at 32 KB: {:.1}% (a JS frame deeper than that is missed)\r\n",
            pct(self.main_truncated, self.main_total)
        );

        if self.hitch_samples == 0 {
            return;
        }
        w.push_str("\r\n-- GameThread leaf modules during hitch slices --\r\n");
        for (cat, count) in top_cats(&self.hitch_leaf).into_iter().take(20) {
            let _ = write!(
                w,
                "{:>6.1}%  {:<40} wait {:.1}%\r\n",
                pct(u64::from(count), self.hitch_samples),
                self.cat_name(cat),
                pct(u64::from(self.hitch_sys[cat as usize]), self.hitch_samples)
            );
        }
        w.push_str("-- GameThread hot leaf addresses during hitch slices --\r\n");
        for e in self.hot.top(HITCH_SLOT, HOT_LEAF).into_iter().take(30) {
            self.hot_leaf_line(w, e, self.hitch_samples);
        }
    }

    fn hot_leaf_line(&self, w: &mut String, e: HotEntry, total: u64) {
        let (cat, off) = split_leaf(e.payload());
        let sep = if is_native_cat(cat) { "+" } else { " " };
        let _ = write!(
            w,
            "{:>6.2}%  {}{sep}0x{off:x}\r\n",
            pct(u64::from(e.count), total),
            self.cat_name(cat)
        );
    }

    fn report_body(&self, final_: bool) -> String {
        let elapsed_s = (now() - self.session_start) as f64 / self.qpc_freq as f64;
        let wall_100ns = (elapsed_s.max(0.001) * 1e7) as u64;
        let mut w = String::with_capacity(64 * 1024);

        let _ = write!(
            w,
            "tidelab patch R-15 module sampler  pid {}  session {}  {}\r\n",
            self.pid,
            self.session_no,
            if final_ { "FINAL" } else { "(in progress)" }
        );
        let _ = write!(
            w,
            "elapsed {elapsed_s:.1} s; tick 1 ms; stack copy {} KB; sampler thread CPU {:.1}% of one core; hot table {} used, {} dropped\r\n",
            STACK_COPY_WORDS * 8 / 1024,
            pct(self_cpu().wrapping_sub(self.self_cpu_start), wall_100ns),
            self.hot.used,
            self.hot.dropped
        );
        let _ = write!(
            w,
            "modules {} ({} code ranges); JIT/anon regions seen {}; main thread {}; {}\r\n",
            self.mod_names.len(),
            self.tables.mods.list.len(),
            self.tables.anons.list.len(),
            self.main_tid,
            if self.regions_note.is_empty() {
                "no tidelab_prof_ranges.txt: V8 counted as exe"
            } else {
                &self.regions_note
            }
        );
        self.slices_report(&mut w);

        // Threads.
        w.push_str("\r\n== Threads by CPU, whole session (% of one core) ==\r\n");
        let mut idx: Vec<usize> = (0..self.threads.len().min(MAX_THREADS)).collect();
        idx.sort_by(|&a, &b| {
            self.threads[b]
                .cpu_session
                .cmp(&self.threads[a].cpu_session)
        });
        let total_cpu: u64 = self.threads.iter().map(|t| t.cpu_session).sum();
        let _ = write!(
            w,
            "all {} threads: {:.1}% of one core\r\n",
            self.threads.len(),
            pct(total_cpu, wall_100ns)
        );
        for &i in idx.iter().take(40) {
            let t = &self.threads[i];
            let _ = write!(
                w,
                "{:>6.1}%  tid {:>6}  {:<32}  start {}{}{}{}{}\r\n",
                pct(t.cpu_session, wall_100ns),
                t.tid,
                t.name,
                self.cat_name(t.start_cat),
                if t.tid == self.main_tid {
                    "  [main]"
                } else {
                    ""
                },
                if t.excluded {
                    "  [not sampled: excluded]"
                } else {
                    ""
                },
                if !t.can_sample { "  [no access]" } else { "" },
                if t.stack_base == 0 && t.can_sample {
                    "  [no stack]"
                } else {
                    ""
                }
            );
        }

        for (si, s) in self.slots.iter().enumerate() {
            if s.samples == 0 {
                continue;
            }
            let total = u64::from(s.samples);
            let _ = write!(
                w,
                "\r\n== Thread {} \"{}\"{}: {} samples ({:.0}/s), {} failed ==\r\n",
                s.tid,
                s.name,
                if s.tid == self.main_tid {
                    " [main/GameThread]"
                } else {
                    ""
                },
                s.samples,
                f64::from(s.samples) / elapsed_s.max(0.001),
                s.failures
            );

            let mut buckets = [0u64; BUCKET_COUNT];
            for (cat, &c) in s.leaf.iter().enumerate() {
                if c == 0 {
                    continue;
                }
                let sys = s.leaf_sys[cat];
                buckets[Bucket::OsWait as usize] += u64::from(sys);
                buckets[self.cat_bucket_of(cat as u16) as usize] += u64::from(c - sys);
            }
            w.push_str("-- Where the thread is (leaf), by owner --\r\n");
            for (b, &c) in buckets.iter().enumerate() {
                if c != 0 {
                    let _ = write!(w, "{:>6.1}%  {}\r\n", pct(c, total), Bucket::ALL[b].label());
                }
            }

            w.push_str("-- Leaf module (self time); 'wait' = inside a syscall --\r\n");
            for (cat, count) in top_cats(&s.leaf).into_iter().take(25) {
                let _ = write!(
                    w,
                    "{:>6.1}%  {:<40} wait {:.1}%\r\n",
                    pct(u64::from(count), total),
                    self.cat_name(cat),
                    pct(u64::from(s.leaf_sys[cat as usize]), total)
                );
            }

            w.push_str("-- On the stack anywhere (approximate inclusive time; stack scan, overcounts) --\r\n");
            for (cat, count) in top_cats(&s.incl).into_iter().take(25) {
                let _ = write!(
                    w,
                    "{:>6.1}%  {}\r\n",
                    pct(u64::from(count), total),
                    self.cat_name(cat)
                );
            }

            w.push_str("-- Leaf <- nearest other module on the stack (who called it) --\r\n");
            for e in self.hot.top(si, HOT_PAIR).into_iter().take(25) {
                let p = e.payload();
                let _ = write!(
                    w,
                    "{:>6.1}%  {} <- {}\r\n",
                    pct(u64::from(e.count), total),
                    self.cat_name((p >> 16) as u16),
                    self.cat_name((p & 0xFFFF) as u16)
                );
            }

            w.push_str("-- Hot leaf addresses (16-byte buckets; module+RVA) --\r\n");
            for e in self.hot.top(si, HOT_LEAF).into_iter().take(30) {
                self.hot_leaf_line(&mut w, e, total);
            }

            let allocs = self.hot.top(si, HOT_ALLOC);
            if !allocs.is_empty() {
                w.push_str("-- JIT/anon samples by allocation base --\r\n");
                for e in allocs.into_iter().take(10) {
                    let _ = write!(
                        w,
                        "{:>6.2}%  0x{:x}\r\n",
                        pct(u64::from(e.count), total),
                        e.payload() << 16
                    );
                }
            }
        }

        let _ = write!(
            w,
            "\r\n== Modules ever seen this process ({}; managed = has a .NET header) ==\r\n",
            self.mod_names.len()
        );
        for (i, base) in self.mod_bases.iter().enumerate() {
            let cat = i as u16;
            let _ = write!(
                w,
                "0x{base:012x}  {:<44} {}{}\r\n",
                self.cat_name(cat),
                self.cat_bucket_of(cat).tag(),
                if self.mod_managed[i] { "  managed" } else { "" }
            );
        }
        w
    }
}

#[cfg(test)]
mod tests {
    use super::super::{Role, Sample, Slot};
    use super::*;

    #[test]
    fn split_leaf_undoes_the_payload() {
        let p = (7u64 << 44) | (0x1234_5678 >> 4);
        assert_eq!(split_leaf(p), (7, 0x1234_5670));
    }

    #[test]
    fn the_slices_csv_has_one_column_per_bucket_and_a_row_per_slice() {
        let mut s = Sampler::new();
        s.session_clock.wHour = 12;
        for _ in 0..25 {
            s.record_slice(
                Role::Main,
                &Sample {
                    leaf: CAT_ANON,
                    sys: false,
                    leaf_payload: 0,
                    js_stack: false,
                },
            );
        }
        s.close_slice(0);
        s.close_slice(1);
        let csv = s.slices_csv();
        let lines: Vec<&str> = csv.split("\r\n").collect();
        assert_eq!(
            lines[1],
            "t_s,clock,main_n,main_busy,render_busy,rhi_busy,main_ue,main_clr,main_js,main_gpu,main_os_run,main_os_wait,main_jit,main_v8_cpp,main_other,main_busy_js_on_stack,hitch"
        );
        assert!(lines[2].starts_with("0.0,12:"), "{}", lines[2]);
        assert!(
            lines[2].ends_with(",25,100,,,0,0,0,0,0,0,100,0,0,0,1"),
            "{}",
            lines[2]
        );
        assert!(
            lines[3].starts_with("0.1,") && lines[3].ends_with(",0,,,,0,0,0,0,0,0,0,0,0,0,0"),
            "{}",
            lines[3]
        );
    }

    #[test]
    fn the_report_lists_the_sampled_threads_and_the_hot_leaves() {
        let mut s = Sampler::new();
        s.intern_module("Client-Win64-Shipping.exe");
        s.mod_bases[0] = 0x1_4000_0000;
        s.exe_cat = 0;
        s.slots.push(Slot::new(42, "GameThread"));
        s.main_tid = 42;
        s.slots[0].samples = 4;
        s.slots[0].leaf[0] = 4;
        s.hot.add(0, HOT_LEAF, 0x1230 >> 4);
        let r = s.report_body(true);
        assert!(
            r.starts_with("tidelab patch R-15 module sampler  pid "),
            "{r}"
        );
        assert!(
            r.contains("== Thread 42 \"GameThread\" [main/GameThread]: 4 samples"),
            "{r}"
        );
        assert!(
            r.contains("100.0%  Client-Win64-Shipping.exe outside V8"),
            "{r}"
        );
        assert!(r.contains("Client-Win64-Shipping.exe+0x1230"), "{r}");
        assert!(
            r.contains("0x000140000000  Client-Win64-Shipping.exe"),
            "{r}"
        );
        let hot = s.hot_csv();
        assert!(
            hot.contains("0,\"GameThread\",42,Client-Win64-Shipping.exe,0x1230,1,\"\""),
            "{hot}"
        );
    }
}
