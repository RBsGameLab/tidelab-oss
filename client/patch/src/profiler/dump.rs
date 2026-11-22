// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Memory dump (R-17): the exe's code is encrypted on disk and decrypted in memory. While
//! `tidelab_dump.on` exists (and no sampling session runs), write `tidelab_dump_<pid>\` beside the
//! DLL: `image.bin` (the exe image, unreadable pages as zeros, file offset = RVA),
//! `anon_<base>.bin` (executable memory outside any image: JIT code, up to 512 MB) and
//! `info.txt` (modules and regions). Reads go through ReadProcessMemory on our own process,
//! which fails instead of faulting when a page is unreadable or freed meanwhile. The switch
//! file is deleted when done.

use std::ffi::c_void;
use std::fmt::Write as _;

use windows_sys::Win32::System::Diagnostics::Debug::ReadProcessMemory;
use windows_sys::Win32::System::Memory::{MEM_COMMIT, MEM_IMAGE};
use windows_sys::Win32::System::Threading::GetCurrentProcess;

use super::tables::MAX_MODULES;
use super::{Sampler, is_exec, now, virtual_query};
use crate::beside;

pub const ON: &str = "tidelab_dump.on";
const ANON_LIMIT: usize = 512 * 1024 * 1024;
const CHUNK: usize = 1 << 20;
const PAGE: usize = 4096;

/// Reads our own memory; false when any of it cannot be read.
fn read_own(from: usize, buf: &mut [u8]) -> bool {
    let mut got = 0usize;
    // SAFETY: the buffer is as long as we say; ReadProcessMemory fails instead of faulting.
    let ok = unsafe {
        ReadProcessMemory(
            GetCurrentProcess(),
            from as *const c_void,
            buf.as_mut_ptr().cast(),
            buf.len(),
            &mut got,
        )
    };
    ok != 0 && got == buf.len()
}

/// Copies [from, from+len) of our own memory into the file; pages that cannot be read become
/// zeros. Returns the number of bytes actually read.
fn write_memory(f: &beside::WrittenFile, from: usize, len: usize) -> usize {
    let mut buf = vec![0u8; CHUNK];
    let mut readable = 0;
    let mut off = 0;
    while off < len {
        let chunk = CHUNK.min(len - off);
        if read_own(from + off, &mut buf[..chunk]) {
            readable += chunk;
        } else {
            for p in (0..chunk).step_by(PAGE) {
                let n = PAGE.min(chunk - p);
                if read_own(from + off + p, &mut buf[p..p + n]) {
                    readable += n;
                } else {
                    buf[p..p + n].fill(0);
                }
            }
        }
        f.append(&buf[..chunk]);
        off += chunk;
    }
    readable
}

/// SizeOfImage from the PE header at `base`, read safely; 0 if it is not one.
fn image_size(base: usize) -> usize {
    let mut hdr = vec![0u8; PAGE];
    if !read_own(base, &mut hdr) {
        return 0;
    }
    let u32_at = |o: usize| u32::from_le_bytes(hdr[o..o + 4].try_into().unwrap()) as usize;
    let nt = u32_at(0x3c);
    if nt > 2048 || u32_at(nt) != 0x4550 {
        return 0;
    }
    u32_at(nt + 24 + 56)
}

impl Sampler {
    fn dump_name(&self, file: &str) -> String {
        if file.is_empty() {
            format!("tidelab_dump_{}", self.pid)
        } else {
            format!("tidelab_dump_{}\\{}", self.pid, file)
        }
    }

    fn dump_to_file(&self, file: &str, from: usize, len: usize) -> Option<usize> {
        let path = beside::path(&self.dump_name(file))?;
        let f = beside::WrittenFile::create(&path)?;
        Some(write_memory(&f, from, len))
    }

    pub(super) fn dump(&mut self) {
        beside::create_dir(&self.dump_name(""));
        let info = self.dump_body();
        beside::write(&self.dump_name("info.txt"), info.as_bytes());
        beside::delete(ON);
    }

    fn dump_body(&mut self) -> String {
        let mut w = String::new();
        let t0 = now();
        self.refresh_modules();
        let exe_base = if (self.exe_cat as usize) < MAX_MODULES {
            self.mod_bases[self.exe_cat as usize]
        } else {
            0
        };
        let exe_size = image_size(exe_base);
        let _ = write!(w, "tidelab patch R-17 memory dump, pid {}\r\n", self.pid);
        let exe_read = self
            .dump_to_file("image.bin", exe_base, exe_size)
            .unwrap_or(0);
        let _ = write!(
            w,
            "exe base 0x{exe_base:x} size 0x{exe_size:x} readable 0x{exe_read:x} -> image.bin (file offset = RVA)\r\n"
        );

        w.push_str("\r\n== exe image regions (rva, size, protect, state) ==\r\n");
        let mut a = exe_base;
        while a < exe_base + exe_size {
            let Some(m) = virtual_query(a) else { break };
            let base = m.BaseAddress as usize;
            let _ = write!(
                w,
                "0x{:08x} 0x{:08x} protect 0x{:x} state 0x{:x}\r\n",
                base - exe_base,
                m.RegionSize,
                m.Protect,
                m.State
            );
            a = base + m.RegionSize;
        }

        w.push_str("\r\n== modules (base, size, name) ==\r\n");
        for (i, &base) in self.mod_bases.iter().enumerate() {
            let _ = write!(
                w,
                "0x{base:012x} 0x{:08x} {}\r\n",
                image_size(base),
                self.mod_names[i]
            );
        }

        w.push_str("\r\n== executable memory outside images (base, size, alloc base, protect, type, dumped) ==\r\n");
        let mut anon_total = 0usize;
        let mut a = 0x10000usize;
        while a < 0x7FFF_FFFF_0000 {
            let Some(m) = virtual_query(a) else { break };
            let base = m.BaseAddress as usize;
            a = base + m.RegionSize;
            if m.State != MEM_COMMIT || !is_exec(m.Protect) || m.Type == MEM_IMAGE {
                continue;
            }
            let mut dumped = false;
            if anon_total + m.RegionSize <= ANON_LIMIT
                && self
                    .dump_to_file(&format!("anon_{base:012x}.bin"), base, m.RegionSize)
                    .is_some()
            {
                anon_total += m.RegionSize;
                dumped = true;
            }
            let _ = write!(
                w,
                "0x{base:012x} 0x{:08x} 0x{:012x} protect 0x{:x} type 0x{:x}{}\r\n",
                m.RegionSize,
                m.AllocationBase as usize,
                m.Protect,
                m.Type,
                if dumped { " dumped" } else { "" }
            );
        }
        let _ = write!(
            w,
            "\r\nanon dumped 0x{anon_total:x} bytes; took {:.1} s\r\n",
            (now() - t0) as f64 / self.qpc_freq as f64
        );
        w
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn image_size_reads_our_own_exe_header() {
        let exe = beside::module_path(0).unwrap();
        let len = std::fs::metadata(String::from_utf16_lossy(&exe))
            .unwrap()
            .len() as usize;
        // SAFETY: the base of our own exe.
        let base = unsafe {
            windows_sys::Win32::System::LibraryLoader::GetModuleHandleW(std::ptr::null())
        } as usize;
        let size = image_size(base);
        assert!(
            size >= PAGE && size >= len / 2,
            "{size} for a {len}-byte exe"
        );
        assert_eq!(image_size(0), 0);
    }

    #[test]
    fn read_own_fails_instead_of_faulting() {
        let mut buf = [0u8; 16];
        assert!(!read_own(8, &mut buf));
        let x = [7u8; 16];
        assert!(read_own(x.as_ptr() as usize, &mut buf));
        assert_eq!(buf, x);
    }
}
