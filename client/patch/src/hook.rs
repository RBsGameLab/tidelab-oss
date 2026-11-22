// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Inline hooks on the first bytes of an exported function (specs/client-patch.md §4).
//!
//! The entry gets a 12-byte absolute jump to the detour, `mov rax, imm64; jmp rax`
//! (`48 B8 <addr> FF E0`); RAX is free at a function's entry in the x64 calling convention.
//! Only two entry shapes are accepted, both checked byte for byte before anything is written:
//!
//! - **jump stub**: kernel32's forwarding stubs, `FF 25 rel32` or `48 FF 25 rel32` (a jump
//!   through RIP to kernelbase) padded with `CC` up to 16 bytes, so the 12-byte patch stays
//!   inside the stub. The detour calls kernelbase's copy to pass a call on; no trampoline.
//! - **syscall stub**: ntdll's `mov r10,rcx; mov eax,N; test byte [0x7FFE0308],1` (16 bytes,
//!   none of them RIP-relative). The trampoline is those 16 bytes copied, then an absolute jump
//!   to entry + 16 (the `jne`, which reads the flags the copied `test` set). The spec's "copy 8
//!   bytes and jump to entry + 8" would land inside the 12-byte patch, so the copy is 16 bytes.
//!
//! Anything else (another Windows build, someone else's hook) is refused and nothing is written.
//!
//! The write: the first two bytes become `EB FE` (jump to itself) in one aligned 16-bit store,
//! then bytes 2..12 are written, then the first two become `48 B8` in one store. A thread that
//! reaches the entry meanwhile spins on `EB FE` for the few instructions it takes, instead of
//! running half a jump. The gates are installed from DllMain before any game thread runs, so in
//! the game this matters only for the unit tests, which hook their own (busy) process.

use std::ffi::c_void;
use std::sync::atomic::{AtomicU16, Ordering};

use windows_sys::Win32::System::Diagnostics::Debug::FlushInstructionCache;
use windows_sys::Win32::System::Memory::{
    MEM_COMMIT, MEM_RESERVE, PAGE_EXECUTE_READ, PAGE_EXECUTE_READWRITE, PAGE_PROTECTION_FLAGS,
    PAGE_READWRITE, VirtualAlloc, VirtualProtect,
};
use windows_sys::Win32::System::Threading::GetCurrentProcess;

/// Bytes the patch overwrites.
pub const PATCH_LEN: usize = 12;
/// Bytes read and checked at the entry.
pub const HEAD_LEN: usize = 16;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Shape {
    JumpStub,
    SyscallStub,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Refused {
    /// The entry has neither accepted shape: its first 16 bytes.
    Shape([u8; HEAD_LEN]),
    /// VirtualProtect or VirtualAlloc failed: GetLastError.
    Memory(u32),
}

impl std::fmt::Display for Refused {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Refused::Shape(b) => {
                f.write_str("unknown entry bytes")?;
                for x in b {
                    write!(f, " {x:02X}")?;
                }
                Ok(())
            }
            Refused::Memory(e) => write!(f, "memory protection failed (error {e})"),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Installed {
    pub shape: Shape,
    /// For a syscall stub: the address that runs the original function.
    pub trampoline: Option<usize>,
}

const SYSCALL_TAIL: [u8; 8] = [0xF6, 0x04, 0x25, 0x08, 0x03, 0xFE, 0x7F, 0x01];

/// Which accepted shape the first 16 bytes of an entry have, if any.
pub fn shape(b: &[u8; HEAD_LEN]) -> Option<Shape> {
    let jump = if b[..2] == [0xFF, 0x25] {
        6
    } else if b[..3] == [0x48, 0xFF, 0x25] {
        7
    } else {
        0
    };
    if jump != 0 && b[jump..].iter().all(|&x| x == 0xCC) {
        return Some(Shape::JumpStub);
    }
    if b[..4] == [0x4C, 0x8B, 0xD1, 0xB8] && b[8..] == SYSCALL_TAIL {
        return Some(Shape::SyscallStub);
    }
    None
}

/// The 12 bytes that jump to `to`.
pub fn jump_bytes(to: usize) -> [u8; PATCH_LEN] {
    let mut j = [0u8; PATCH_LEN];
    j[..2].copy_from_slice(&[0x48, 0xB8]);
    j[2..10].copy_from_slice(&(to as u64).to_le_bytes());
    j[10..].copy_from_slice(&[0xFF, 0xE0]);
    j
}

/// Puts a jump to `detour` over the entry at `target`, after checking its shape.
///
/// # Safety
/// As for [`prepare`] and [`Prepared::commit`].
pub unsafe fn install(target: *mut u8, detour: usize) -> Result<Installed, Refused> {
    // SAFETY: passed on to the caller.
    unsafe { prepare(target)?.commit(detour) }
}

/// A checked entry, with its trampoline built, before anything is written to it.
#[derive(Debug)]
pub struct Prepared {
    target: *mut u8,
    pub shape: Shape,
    pub trampoline: Option<usize>,
}

/// Checks the entry at `target` and builds its trampoline; writes nothing to it.
///
/// # Safety
/// `target` must point to at least 16 readable bytes of code that nothing else is patching.
pub unsafe fn prepare(target: *mut u8) -> Result<Prepared, Refused> {
    let mut head = [0u8; HEAD_LEN];
    // SAFETY: the caller promises 16 readable bytes.
    unsafe { std::ptr::copy_nonoverlapping(target, head.as_mut_ptr(), HEAD_LEN) };
    let shape = shape(&head).ok_or(Refused::Shape(head))?;
    let trampoline = match shape {
        Shape::JumpStub => None,
        Shape::SyscallStub => Some(trampoline(&head, target as usize + HEAD_LEN)?),
    };
    Ok(Prepared {
        target,
        shape,
        trampoline,
    })
}

impl Prepared {
    /// Writes the jump to `detour`.
    ///
    /// # Safety
    /// `detour` must be a function with the target's signature and calling convention.
    pub unsafe fn commit(self, detour: usize) -> Result<Installed, Refused> {
        // SAFETY: the entry was checked by `prepare` and is 16 bytes long.
        unsafe { write_jump(self.target, detour)? };
        Ok(Installed {
            shape: self.shape,
            trampoline: self.trampoline,
        })
    }
}

/// Executable memory: `head`, then `jmp [rip+0]` to `back`.
fn trampoline(head: &[u8; HEAD_LEN], back: usize) -> Result<usize, Refused> {
    let mut code = Vec::with_capacity(HEAD_LEN + 14);
    code.extend_from_slice(head);
    code.extend_from_slice(&[0xFF, 0x25, 0, 0, 0, 0]);
    code.extend_from_slice(&(back as u64).to_le_bytes());
    // SAFETY: a fresh allocation, written before it is made executable.
    unsafe {
        let mem = VirtualAlloc(
            std::ptr::null(),
            code.len(),
            MEM_COMMIT | MEM_RESERVE,
            PAGE_READWRITE,
        ) as *mut u8;
        if mem.is_null() {
            return Err(Refused::Memory(last_error()));
        }
        std::ptr::copy_nonoverlapping(code.as_ptr(), mem, code.len());
        let mut old: PAGE_PROTECTION_FLAGS = 0;
        if VirtualProtect(
            mem as *const c_void,
            code.len(),
            PAGE_EXECUTE_READ,
            &mut old,
        ) == 0
        {
            return Err(Refused::Memory(last_error()));
        }
        FlushInstructionCache(GetCurrentProcess(), mem as *const c_void, code.len());
        Ok(mem as usize)
    }
}

/// # Safety
/// `target` points to at least 12 bytes of code that may be overwritten.
unsafe fn write_jump(target: *mut u8, detour: usize) -> Result<(), Refused> {
    let jump = jump_bytes(detour);
    let mut old: PAGE_PROTECTION_FLAGS = 0;
    // SAFETY: as promised by the caller; the protection is restored below.
    unsafe {
        if VirtualProtect(
            target as *const c_void,
            PATCH_LEN,
            PAGE_EXECUTE_READWRITE,
            &mut old,
        ) == 0
        {
            return Err(Refused::Memory(last_error()));
        }
        store_head(target, [0xEB, 0xFE]);
        std::ptr::copy_nonoverlapping(jump.as_ptr().add(2), target.add(2), PATCH_LEN - 2);
        store_head(target, [jump[0], jump[1]]);
        let mut ignored: PAGE_PROTECTION_FLAGS = 0;
        VirtualProtect(target as *const c_void, PATCH_LEN, old, &mut ignored);
        FlushInstructionCache(GetCurrentProcess(), target as *const c_void, PATCH_LEN);
    }
    Ok(())
}

/// The first two bytes in one store when the entry is 2-aligned (exports are 16-aligned).
unsafe fn store_head(target: *mut u8, b: [u8; 2]) {
    if (target as usize).is_multiple_of(2) {
        // SAFETY: aligned and writable.
        unsafe { (*(target as *const AtomicU16)).store(u16::from_le_bytes(b), Ordering::SeqCst) };
    } else {
        // SAFETY: writable.
        unsafe { std::ptr::copy_nonoverlapping(b.as_ptr(), target, 2) };
    }
}

fn last_error() -> u32 {
    // SAFETY: plain query.
    unsafe { windows_sys::Win32::Foundation::GetLastError() }
}

#[cfg(test)]
mod tests {
    use super::*;

    const CREATE_FILE_W: [u8; 16] = [
        0xFF, 0x25, 0xE2, 0x3C, 0x03, 0x00, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC,
        0xCC,
    ];
    const LOAD_LIBRARY_W: [u8; 16] = [
        0x48, 0xFF, 0x25, 0x21, 0xBD, 0x04, 0x00, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC,
        0xCC,
    ];
    const NT_CREATE_USER_PROCESS: [u8; 16] = [
        0x4C, 0x8B, 0xD1, 0xB8, 0xD1, 0x00, 0x00, 0x00, 0xF6, 0x04, 0x25, 0x08, 0x03, 0xFE, 0x7F,
        0x01,
    ];
    const KERNELBASE_CREATE_FILE_W: [u8; 16] = [
        0x48, 0x8B, 0xC4, 0x48, 0x89, 0x58, 0x08, 0x00, 0, 0, 0, 0, 0, 0, 0, 0,
    ];

    #[test]
    fn accepts_the_shapes_of_the_spec_table() {
        assert_eq!(shape(&CREATE_FILE_W), Some(Shape::JumpStub));
        assert_eq!(shape(&LOAD_LIBRARY_W), Some(Shape::JumpStub));
        assert_eq!(shape(&NT_CREATE_USER_PROCESS), Some(Shape::SyscallStub));
    }

    #[test]
    fn refuses_other_prologues() {
        assert_eq!(shape(&KERNELBASE_CREATE_FILE_W), None);
        // A jump stub whose padding is short: the patch would reach the next export.
        let mut short = CREATE_FILE_W;
        short[12] = 0x48;
        assert_eq!(shape(&short), None);
        // Already hooked by someone: starts with our own jump.
        let mut hooked = [0xCCu8; 16];
        hooked[..12].copy_from_slice(&jump_bytes(0x1234));
        assert_eq!(shape(&hooked), None);
        // A syscall stub of another shape.
        let mut other = NT_CREATE_USER_PROCESS;
        other[9] = 0x05;
        assert_eq!(shape(&other), None);
    }

    #[test]
    fn refused_install_writes_nothing() {
        let mut buf = KERNELBASE_CREATE_FILE_W;
        // SAFETY: 16 bytes of our own memory; refused before any write.
        let r = unsafe { install(buf.as_mut_ptr(), 0x1234) };
        assert_eq!(r, Err(Refused::Shape(KERNELBASE_CREATE_FILE_W)));
        assert_eq!(buf, KERNELBASE_CREATE_FILE_W);
    }

    #[test]
    fn install_writes_the_jump_and_builds_the_trampoline() {
        let mut buf = NT_CREATE_USER_PROCESS;
        // SAFETY: 16 bytes of our own memory, never executed.
        let r = unsafe { install(buf.as_mut_ptr(), 0x1122_3344_5566_7788) }.unwrap();
        assert_eq!(r.shape, Shape::SyscallStub);
        assert_eq!(buf[..12], jump_bytes(0x1122_3344_5566_7788));
        assert_eq!(buf[12..], NT_CREATE_USER_PROCESS[12..]);
        let t = r.trampoline.unwrap() as *const u8;
        // SAFETY: the trampoline is 30 readable bytes.
        let code = unsafe { std::slice::from_raw_parts(t, 30) };
        assert_eq!(code[..16], NT_CREATE_USER_PROCESS);
        assert_eq!(code[16..22], [0xFF, 0x25, 0, 0, 0, 0]);
        assert_eq!(code[22..], (buf.as_ptr() as u64 + 16).to_le_bytes());
    }
}
