// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Files beside the patch DLL in `Binaries\Win64`: switch files (on while the file exists),
//! status files and reports (`tidelab_ace.quiet`, `tidelab_prof.on`, ...). rbauto, the launcher
//! and start-game.cmd use the same names.

use std::ffi::c_void;
use std::sync::OnceLock;

use windows_sys::Win32::Foundation::{
    CloseHandle, GENERIC_READ, GENERIC_WRITE, INVALID_HANDLE_VALUE,
};
use windows_sys::Win32::Storage::FileSystem::{
    CREATE_ALWAYS, CreateDirectoryW, CreateFileW, DeleteFileW, FILE_ATTRIBUTE_NORMAL,
    FILE_SHARE_READ, GetFileAttributesW, INVALID_FILE_ATTRIBUTES, OPEN_EXISTING, ReadFile,
    WriteFile,
};
use windows_sys::Win32::System::LibraryLoader::GetModuleFileNameW;

/// The game's executable; the patch does nothing in any other process.
pub const CLIENT_EXE: &str = "client-win64-shipping.exe";

static DIR: OnceLock<Vec<u16>> = OnceLock::new();

/// Remembers the directory of `module` (the patch DLL), with its trailing backslash.
pub fn set_dir(module: usize) -> bool {
    let Some(path) = module_path(module) else {
        return false;
    };
    let cut = path
        .iter()
        .rposition(|&c| c == u16::from(b'\\'))
        .map_or(0, |i| i + 1);
    DIR.set(path[..cut].to_vec()).is_ok() || DIR.get().is_some()
}

/// The full path of a loaded module (`0` = the exe).
pub fn module_path(module: usize) -> Option<Vec<u16>> {
    let mut buf = vec![0u16; 1024];
    // SAFETY: the buffer is as long as we say.
    let n = unsafe { GetModuleFileNameW(module as *mut c_void, buf.as_mut_ptr(), buf.len() as u32) }
        as usize;
    if n == 0 || n >= buf.len() {
        return None;
    }
    buf.truncate(n);
    Some(buf)
}

/// True in the game's process.
pub fn in_client() -> bool {
    module_path(0).is_some_and(|p| is_client_exe(&narrow(&p)))
}

/// True when the file name of `exe_path` is the game's, in any case.
pub fn is_client_exe(exe_path: &str) -> bool {
    basename(exe_path).eq_ignore_ascii_case(CLIENT_EXE)
}

/// `<dll dir>\<name>`, NUL-terminated; None before `set_dir`.
pub fn path(name: &str) -> Option<Vec<u16>> {
    let dir = DIR.get()?;
    let mut p = dir.clone();
    p.extend(name.encode_utf16());
    p.push(0);
    Some(p)
}

pub fn exists(name: &str) -> bool {
    let Some(p) = path(name) else { return false };
    // SAFETY: NUL-terminated.
    unsafe { GetFileAttributesW(p.as_ptr()) != INVALID_FILE_ATTRIBUTES }
}

pub fn write(name: &str, data: &[u8]) {
    if let Some(p) = path(name) {
        write_path(&p, data);
    }
}

pub fn delete(name: &str) {
    if let Some(p) = path(name) {
        // SAFETY: NUL-terminated.
        unsafe { DeleteFileW(p.as_ptr()) };
    }
}

pub fn create_dir(name: &str) {
    if let Some(p) = path(name) {
        // SAFETY: NUL-terminated.
        unsafe { CreateDirectoryW(p.as_ptr(), std::ptr::null()) };
    }
}

/// At most `max` bytes of the file, or None when it cannot be opened.
pub fn read(name: &str, max: usize) -> Option<Vec<u8>> {
    let p = path(name)?;
    // SAFETY: NUL-terminated path; the handle is closed below.
    let h = unsafe {
        CreateFileW(
            p.as_ptr(),
            GENERIC_READ,
            FILE_SHARE_READ,
            std::ptr::null(),
            OPEN_EXISTING,
            FILE_ATTRIBUTE_NORMAL,
            std::ptr::null_mut(),
        )
    };
    if h == INVALID_HANDLE_VALUE {
        return None;
    }
    let mut buf = vec![0u8; max];
    let mut got = 0u32;
    // SAFETY: the buffer is `max` bytes.
    let ok = unsafe {
        ReadFile(
            h,
            buf.as_mut_ptr(),
            max as u32,
            &mut got,
            std::ptr::null_mut(),
        )
    } != 0;
    // SAFETY: opened above.
    unsafe { CloseHandle(h) };
    if !ok {
        return None;
    }
    buf.truncate(got as usize);
    Some(buf)
}

/// Writes `data` to a NUL-terminated path, replacing the file; others may read it meanwhile.
pub fn write_path(path: &[u16], data: &[u8]) -> Option<WrittenFile> {
    let f = WrittenFile::create(path)?;
    f.append(data);
    Some(f)
}

/// A file being written; closed when dropped.
pub struct WrittenFile(*mut c_void);

impl WrittenFile {
    pub fn create(path: &[u16]) -> Option<Self> {
        debug_assert_eq!(path.last(), Some(&0));
        // SAFETY: NUL-terminated path.
        let h = unsafe {
            CreateFileW(
                path.as_ptr(),
                GENERIC_WRITE,
                FILE_SHARE_READ,
                std::ptr::null(),
                CREATE_ALWAYS,
                FILE_ATTRIBUTE_NORMAL,
                std::ptr::null_mut(),
            )
        };
        (h != INVALID_HANDLE_VALUE).then_some(Self(h))
    }

    pub fn append(&self, data: &[u8]) {
        for chunk in data.chunks(1 << 30) {
            // SAFETY: the chunk is valid for its length.
            unsafe {
                WriteFile(
                    self.0,
                    chunk.as_ptr(),
                    chunk.len() as u32,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                )
            };
        }
    }
}

impl Drop for WrittenFile {
    fn drop(&mut self) {
        // SAFETY: opened in `create`.
        unsafe { CloseHandle(self.0) };
    }
}

/// UTF-16 to ASCII; anything else becomes '?'.
pub fn narrow(w: &[u16]) -> String {
    w.iter()
        .map(|&c| if c < 0x80 { c as u8 as char } else { '?' })
        .collect()
}

/// The text after the last `\` or `/`.
pub fn basename(name: &str) -> &str {
    name.rsplit(['\\', '/']).next().unwrap_or(name)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn narrow_keeps_ascii_only() {
        let w: Vec<u16> = "Aki.dll é".encode_utf16().collect();
        assert_eq!(narrow(&w), "Aki.dll ?");
    }

    #[test]
    fn basename_keeps_the_file_name() {
        assert_eq!(
            basename(r"C:\g\AntiCheatExpert\ACE-Setup64.exe"),
            "ACE-Setup64.exe"
        );
        assert_eq!(basename("a/b.exe"), "b.exe");
        assert_eq!(basename("a.exe"), "a.exe");
    }

    #[test]
    fn only_the_client_exe_arms() {
        assert!(is_client_exe(
            r"C:\g\Client\Binaries\Win64\Client-Win64-Shipping.exe"
        ));
        assert!(is_client_exe("CLIENT-WIN64-SHIPPING.EXE"));
        assert!(is_client_exe("client-win64-shipping.exe"));
        assert!(!is_client_exe(r"C:\g\XClient-Win64-Shipping.exe"));
        assert!(!is_client_exe(r"C:\g\Client-Win64-Shipping.exe\cmd.exe"));
        assert!(!is_client_exe("Client-Win64-ShippingBase.dll"));
        assert!(!is_client_exe("cargo.exe"));
    }

    #[test]
    fn files_beside_the_test_binary() {
        // The test exe stands in for the DLL: its own directory.
        assert!(set_dir(0));
        let name = format!("tidelab_patch_test_{}.tmp", std::process::id());
        assert!(!exists(&name));
        write(&name, b"one");
        assert!(exists(&name));
        assert_eq!(read(&name, 2).as_deref(), Some(&b"on"[..]));
        delete(&name);
        assert!(!exists(&name));
        assert!(!in_client());
    }
}
