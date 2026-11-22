// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Runtime hooks installed by the helper DLL (specs/client-patch.md).
//!
//! - File hook on `kernel32!CreateFileW` with clean release on exit (`exit.rs`).
//! - Process observation hook on `ntdll!NtCreateUserProcess` (pass-through only).

use std::ffi::c_void;
use std::sync::OnceLock;
use std::sync::atomic::{AtomicU32, AtomicUsize, Ordering};

use windows_sys::Win32::Foundation::HANDLE;
use windows_sys::Win32::System::LibraryLoader::{GetModuleHandleW, GetProcAddress};
use windows_sys::Win32::System::Threading::{
    CreateEventW, GetCurrentThread, GetCurrentThreadId, INFINITE, SetEvent, SuspendThread,
    WaitForSingleObject,
};

use crate::hook;

/// `kernelbase!CreateFileW`, where kernel32's stub jumps; the pass-through.
static CREATE_FILE_PASS: AtomicUsize = AtomicUsize::new(0);
/// The trampoline that runs the original `NtCreateUserProcess`.
static CREATE_PROCESS_PASS: AtomicUsize = AtomicUsize::new(0);
/// Where the image names of started processes go.
static NOTE: OnceLock<fn(&str)> = OnceLock::new();
/// Where the gates' own lines go.
static LOG: OnceLock<fn(&str)> = OnceLock::new();
/// Lower CPU load.
pub static FROZEN: AtomicU32 = AtomicU32::new(0);
/// Lower CPU load.
static HELD: AtomicU32 = AtomicU32::new(0);
/// R-148: the manual-reset event held threads wait on; set once, by `release`. Zero when it
/// could not be made: the gate then suspends the thread for good, as before.
static RELEASE: AtomicUsize = AtomicUsize::new(0);

type CreateFileWFn =
    unsafe extern "system" fn(*const u16, u32, u32, *const c_void, u32, u32, HANDLE) -> HANDLE;
type NtCreateUserProcessFn = unsafe extern "system" fn(
    usize,
    usize,
    usize,
    usize,
    usize,
    usize,
    usize,
    usize,
    usize,
    usize,
    usize,
) -> i32;

/// Lower CPU load.
/// compared case-sensitively).
pub fn sig_freezes(name: &[u16]) -> bool {
    ends_with(name, ".sig")
        && !ends_with(name, "WindowsNoEditor.sig")
        && !ends_with(name, "WindowsNoEditor_P.sig")
}

fn ends_with(s: &[u16], suffix: &str) -> bool {
    let suffix: Vec<u16> = suffix.encode_utf16().collect();
    s.ends_with(&suffix)
}

/// What was installed, for the log.
#[derive(Debug)]
pub struct Report {
    pub lines: Vec<String>,
}

/// Lower CPU load.
/// processes started, `log` the gates' own lines. Each refusal is reported and the other gate
/// still goes in. Call once per process.
pub fn install(note: fn(&str), log: fn(&str)) -> Report {
    let _ = NOTE.set(note);
    let _ = LOG.set(log);
    let mut lines = Vec::new();
    if RELEASE.load(Ordering::SeqCst) == 0 {
        // SAFETY: an unnamed manual-reset event, not set; it lives as long as the process.
        let ev = unsafe { CreateEventW(std::ptr::null(), 1, 0, std::ptr::null()) };
        if ev.is_null() {
            lines.push(".sig gate: no release event, held threads stay held to the end".into());
        } else {
            RELEASE.store(ev as usize, Ordering::SeqCst);
        }
    }
    match install_process_gate() {
        Ok(at) => lines.push(format!(
            "process gate on ntdll!NtCreateUserProcess at 0x{at:X}"
        )),
        Err(e) => lines.push(format!("process gate not installed: {e}")),
    }
    match install_sig_gate() {
        Ok(at) => lines.push(format!(".sig gate on kernel32!CreateFileW at 0x{at:X}")),
        Err(e) => lines.push(format!(
            ".sig gate NOT installed, our paks will not mount: {e}"
        )),
    }
    Report { lines }
}

/// `GetProcAddress(GetModuleHandleW(module), name)`.
pub fn export(module: &str, name: &core::ffi::CStr) -> Option<usize> {
    let m: Vec<u16> = module.encode_utf16().chain([0]).collect();
    // SAFETY: NUL-terminated names; the modules are never unloaded.
    unsafe {
        let h = GetModuleHandleW(m.as_ptr());
        if h.is_null() {
            return None;
        }
        GetProcAddress(h, name.as_ptr().cast()).map(|f| f as usize)
    }
}

/// Lower CPU load.
/// Returns how many were held. Only the exit watch calls it, once the game is exiting.
pub fn release() -> u32 {
    let held = HELD.load(Ordering::SeqCst);
    let ev = RELEASE.load(Ordering::SeqCst);
    if ev != 0 {
        // SAFETY: the event made in `install`.
        unsafe { SetEvent(ev as HANDLE) };
    }
    held
}

fn install_sig_gate() -> Result<usize, String> {
    let target = export("kernel32.dll", c"CreateFileW").ok_or("kernel32!CreateFileW not found")?;
    let pass =
        export("kernelbase.dll", c"CreateFileW").ok_or("kernelbase!CreateFileW not found")?;
    if pass == target {
        return Err("kernel32 and kernelbase share CreateFileW".into());
    }
    CREATE_FILE_PASS.store(pass, Ordering::SeqCst);
    // SAFETY: an exported function; the detour has CreateFileW's signature.
    unsafe { hook::install(target as *mut u8, create_file_gate as *const () as usize) }
        .map_err(|e| e.to_string())?;
    Ok(target)
}

fn install_process_gate() -> Result<usize, String> {
    let target =
        export("ntdll.dll", c"NtCreateUserProcess").ok_or("ntdll!NtCreateUserProcess not found")?;
    // The trampoline is stored before the jump goes in: a call can arrive right after.
    // SAFETY: an exported function, read only.
    let prepared = unsafe { hook::prepare(target as *mut u8) }.map_err(|e| e.to_string())?;
    let Some(t) = prepared.trampoline else {
        return Err("NtCreateUserProcess is not a syscall stub".into());
    };
    CREATE_PROCESS_PASS.store(t, Ordering::SeqCst);
    // SAFETY: the detour has NtCreateUserProcess' signature.
    unsafe { prepared.commit(create_process_gate as *const () as usize) }
        .map_err(|e| e.to_string())?;
    Ok(target)
}

fn note(image: &str) {
    if let Some(f) = NOTE.get() {
        f(image);
    }
}

fn log(line: &str) {
    if let Some(f) = LOG.get() {
        f(line);
    }
}

unsafe extern "system" fn create_file_gate(
    name: *const u16,
    access: u32,
    share: u32,
    security: *const c_void,
    disposition: u32,
    flags: u32,
    template: HANDLE,
) -> HANDLE {
    if !name.is_null() {
        // SAFETY: CreateFileW takes a NUL-terminated string.
        let s = unsafe { wide_str(name) };
        if sig_freezes(s) {
            FROZEN.fetch_add(1, Ordering::SeqCst);
            // SAFETY: plain query.
            let tid = unsafe { GetCurrentThreadId() };
            // Logged before the wait: the log's lock and the heap are let go by then.
            log(&format!(
                ".sig gate: thread {tid} frozen opening {}",
                String::from_utf16_lossy(s)
            ));
            let ev = RELEASE.load(Ordering::SeqCst);
            if ev == 0 {
                // SAFETY: suspends the calling thread; nothing resumes it.
                unsafe { SuspendThread(GetCurrentThread()) };
            } else {
                HELD.fetch_add(1, Ordering::SeqCst);
                // SAFETY: the event made in `install`; set only when the game exits.
                unsafe { WaitForSingleObject(ev as HANDLE, INFINITE) };
                HELD.fetch_sub(1, Ordering::SeqCst);
                log(&format!(
                    ".sig gate: thread {tid} let go (the game is exiting)"
                ));
            }
        }
    }
    // The pass-through is set before the jump is written; a hook can only run after that.
    let pass: CreateFileWFn =
        // SAFETY: kernelbase!CreateFileW, same signature.
        unsafe { std::mem::transmute(CREATE_FILE_PASS.load(Ordering::SeqCst)) };
    // SAFETY: the caller's arguments, unchanged.
    unsafe { pass(name, access, share, security, disposition, flags, template) }
}

/// Offsets in RTL_USER_PROCESS_PARAMETERS (x64).
const PARAMS_FLAGS: usize = 0x08;
const PARAMS_IMAGE_PATH: usize = 0x60;
/// RTL_USER_PROC_PARAMS_NORMALIZED: the string buffers are pointers, not offsets.
const PARAMS_NORMALIZED: u32 = 1;

#[allow(clippy::too_many_arguments)]
unsafe extern "system" fn create_process_gate(
    process: usize,
    thread: usize,
    process_access: usize,
    thread_access: usize,
    process_attributes: usize,
    thread_attributes: usize,
    process_flags: usize,
    thread_flags: usize,
    parameters: usize,
    create_info: usize,
    attributes: usize,
) -> i32 {
    // SAFETY: the parameters argument is null or the caller's RTL_USER_PROCESS_PARAMETERS.
    match unsafe { image_path(parameters) } {
        Some(path) => note(&String::from_utf16_lossy(&path)),
        None => log("process gate: a process is started without parameters"),
    }
    let pass: NtCreateUserProcessFn =
        // SAFETY: the trampoline runs the original stub.
        unsafe { std::mem::transmute(CREATE_PROCESS_PASS.load(Ordering::SeqCst)) };
    // SAFETY: the caller's arguments, unchanged.
    unsafe {
        pass(
            process,
            thread,
            process_access,
            thread_access,
            process_attributes,
            thread_attributes,
            process_flags,
            thread_flags,
            parameters,
            create_info,
            attributes,
        )
    }
}

/// `ImagePathName` of an RTL_USER_PROCESS_PARAMETERS.
///
/// # Safety
/// `params` is null or points to a valid RTL_USER_PROCESS_PARAMETERS.
pub unsafe fn image_path(params: usize) -> Option<Vec<u16>> {
    if params == 0 {
        return None;
    }
    // SAFETY: fields of the structure.
    unsafe {
        let flags = *((params + PARAMS_FLAGS) as *const u32);
        let len = *((params + PARAMS_IMAGE_PATH) as *const u16) as usize / 2;
        let mut buf = *((params + PARAMS_IMAGE_PATH + 8) as *const usize);
        if buf == 0 || len == 0 {
            return Some(Vec::new());
        }
        if flags & PARAMS_NORMALIZED == 0 {
            buf += params;
        }
        Some(std::slice::from_raw_parts(buf as *const u16, len).to_vec())
    }
}

/// # Safety
/// `p` is NUL-terminated.
unsafe fn wide_str<'a>(p: *const u16) -> &'a [u16] {
    let mut n = 0;
    // SAFETY: stops at the NUL.
    unsafe {
        while *p.add(n) != 0 {
            n += 1;
        }
        std::slice::from_raw_parts(p, n)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Mutex, Once};

    use windows_sys::Win32::Foundation::{CloseHandle, GENERIC_READ, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::Storage::FileSystem::{
        FILE_ATTRIBUTE_NORMAL, FILE_SHARE_READ, GetFileSize, OPEN_EXISTING,
    };
    use windows_sys::Win32::System::Threading::Sleep;

    fn w(s: &str) -> Vec<u16> {
        s.encode_utf16().collect()
    }

    #[test]
    fn sig_rule() {
        assert!(sig_freezes(&w(r"X\rb38_cs_300_p.sig")));
        assert!(sig_freezes(&w(
            r"..\..\..\Client\Content\Paks\rb38_login_100_p.sig"
        )));
        assert!(!sig_freezes(&w("pakchunk0-WindowsNoEditor.sig")));
        assert!(!sig_freezes(&w("pakchunk9-WindowsNoEditor_P.sig")));
        assert!(!sig_freezes(&w("a.pak")));
        assert!(!sig_freezes(&w("a.sig.txt")));
        assert!(!sig_freezes(&w("A.SIG")));
        assert!(!sig_freezes(&w("")));
    }

    static SEEN: Mutex<Vec<String>> = Mutex::new(Vec::new());
    static INSTALL: Once = Once::new();

    fn record(line: &str) {
        SEEN.lock().unwrap().push(line.to_string());
    }

    /// Both gates in this test process, once.
    fn installed() {
        INSTALL.call_once(|| {
            let r = install(record, record);
            assert!(r.lines.iter().all(|l| !l.contains("not")), "{r:?}");
        });
    }

    fn kernel32_create_file() -> CreateFileWFn {
        // SAFETY: kernel32!CreateFileW.
        unsafe { std::mem::transmute(export("kernel32.dll", c"CreateFileW").unwrap()) }
    }

    #[test]
    fn sig_gate_passes_files_and_freezes_sig_opens() {
        installed();
        let create = kernel32_create_file();
        // An ordinary file opens and its handle works.
        let me = std::env::current_exe().unwrap();
        let path: Vec<u16> = me.as_os_str().encode_wide_nul();
        // SAFETY: NUL-terminated path; closed below.
        let h = unsafe {
            create(
                path.as_ptr(),
                GENERIC_READ,
                FILE_SHARE_READ,
                std::ptr::null(),
                OPEN_EXISTING,
                FILE_ATTRIBUTE_NORMAL,
                std::ptr::null_mut(),
            )
        };
        assert_ne!(h, INVALID_HANDLE_VALUE);
        // SAFETY: an open file.
        assert!(unsafe { GetFileSize(h, std::ptr::null_mut()) } > 0);
        // SAFETY: opened above.
        unsafe { CloseHandle(h) };

        // Lower CPU load.
        let open_sig = move || {
            let mut name = w(r"X:\nowhere\rb38_cs_300_p.sig");
            name.push(0);
            // SAFETY: NUL-terminated path.
            unsafe {
                create(
                    name.as_ptr(),
                    GENERIC_READ,
                    FILE_SHARE_READ,
                    std::ptr::null(),
                    OPEN_EXISTING,
                    FILE_ATTRIBUTE_NORMAL,
                    std::ptr::null_mut(),
                ) as usize
            }
        };
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || tx.send(open_sig()).unwrap());
        let mut held = false;
        for _ in 0..500 {
            if HELD.load(Ordering::SeqCst) >= 1 {
                held = true;
                break;
            }
            // SAFETY: plain sleep.
            unsafe { Sleep(10) };
        }
        assert!(held, "the helper thread was not held");
        assert!(
            rx.recv_timeout(std::time::Duration::from_millis(300))
                .is_err(),
            "the held thread went on before the release"
        );
        // R-148: the exit watch's release lets it go on to the pass-through (no such file).
        assert!(release() >= 1);
        let h = rx
            .recv_timeout(std::time::Duration::from_secs(5))
            .expect("the released thread did not go on");
        assert_eq!(h, INVALID_HANDLE_VALUE as usize);
        // Lower CPU load.
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || tx.send(open_sig()).unwrap());
        assert_eq!(
            rx.recv_timeout(std::time::Duration::from_secs(5)).unwrap(),
            INVALID_HANDLE_VALUE as usize
        );
        assert!(FROZEN.load(Ordering::SeqCst) >= 1);
        assert!(
            SEEN.lock()
                .unwrap()
                .iter()
                .any(|l| l.contains("frozen opening X:\\nowhere\\rb38_cs_300_p.sig"))
        );
        assert!(
            SEEN.lock()
                .unwrap()
                .iter()
                .any(|l| l.contains("let go (the game is exiting)"))
        );
    }

    #[test]
    fn process_gate_passes_concurrent_starts_and_stays() {
        installed();
        let run = || {
            std::thread::spawn(|| {
                std::process::Command::new("cmd")
                    .args(["/c", "exit 7"])
                    .status()
                    .unwrap()
                    .code()
            })
        };
        let (a, b) = (run(), run());
        assert_eq!(a.join().unwrap(), Some(7));
        assert_eq!(b.join().unwrap(), Some(7));
        let cmd = SEEN
            .lock()
            .unwrap()
            .iter()
            .filter(|l| l.to_ascii_lowercase().ends_with("\\cmd.exe"))
            .count();
        assert!(cmd >= 2, "{:?}", SEEN.lock().unwrap());
        let at = export("ntdll.dll", c"NtCreateUserProcess").unwrap();
        // SAFETY: 12 readable bytes of ntdll.
        let entry = unsafe { std::slice::from_raw_parts(at as *const u8, 12) };
        assert_eq!(
            entry,
            hook::jump_bytes(create_process_gate as *const () as usize)
        );
    }

    trait WideNul {
        fn encode_wide_nul(&self) -> Vec<u16>;
    }
    impl WideNul for std::ffi::OsStr {
        fn encode_wide_nul(&self) -> Vec<u16> {
            use std::os::windows::ffi::OsStrExt;
            self.encode_wide().chain([0]).collect()
        }
    }
}
