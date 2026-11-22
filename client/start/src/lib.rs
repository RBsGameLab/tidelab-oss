// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! `tidelab_start.exe`: starts the 3.8 client with tidelab's patch DLL in it
//! (specs/client-patch.md §1).
//!
//! Run from anywhere: the game and the DLL are found beside the launcher itself. The client is
//! created suspended, `tidelab_patch.dll` is loaded into it by a remote thread at
//! `kernel32!LoadLibraryW` (its DllMain installs the gates while the main thread has not run a
//! single instruction), then the main thread is resumed and the launcher exits without waiting.
//! The arguments (`-krqlv=SD`, the client's own resource-quality switch) are passed on as they
//! are. It reads and writes no files; the scripts copy paks and touch the marker files.

use std::ffi::{OsStr, OsString, c_void};
use std::os::windows::ffi::OsStrExt;
use std::path::{Path, PathBuf};

use windows_sys::Win32::Foundation::{CloseHandle, HANDLE, WAIT_OBJECT_0};
use windows_sys::Win32::System::Diagnostics::Debug::WriteProcessMemory;
use windows_sys::Win32::System::LibraryLoader::{GetModuleHandleW, GetProcAddress};
use windows_sys::Win32::System::Memory::{
    MEM_COMMIT, MEM_RELEASE, MEM_RESERVE, PAGE_READWRITE, VirtualAllocEx, VirtualFreeEx,
};
use windows_sys::Win32::System::Threading::{
    CREATE_SUSPENDED, CreateProcessW, CreateRemoteThread, GetExitCodeThread, INFINITE,
    PROCESS_INFORMATION, ResumeThread, STARTUPINFOW, TerminateProcess, WaitForSingleObject,
};
use windows_sys::Win32::UI::WindowsAndMessaging::{MB_ICONWARNING, MessageBoxW};

const GAME_EXE: &str = "Client-Win64-Shipping.exe";
const PATCH_DLL: &str = "tidelab_patch.dll";
const TITLE: &str = "[tidelab] launcher";
/// The exit code of a client the launcher gave up on.
const ABANDONED: u32 = 0x4845_4C49;

pub fn run() {
    let me = std::env::current_exe().unwrap_or_default();
    let paths = Paths::beside(&me);
    if !paths.game.is_file() {
        warn(
            "Game executable doesn't exist.\nMake sure you've placed the launcher in the right directory.",
        );
        std::process::exit(1);
    }
    let args: Vec<OsString> = std::env::args_os().skip(1).collect();
    if let Err(e) = launch(&paths, &args) {
        warn(&format!("tidelab launch failed: {e}"));
        std::process::exit(1);
    }
}

/// Where the launcher finds things: all beside itself.
#[derive(Debug, PartialEq, Eq)]
pub struct Paths {
    pub game: PathBuf,
    pub dll: PathBuf,
    /// The game's directory, with its trailing `\`: the client's working directory.
    pub dir: PathBuf,
}

impl Paths {
    pub fn beside(launcher: &Path) -> Paths {
        let s = launcher.as_os_str().to_string_lossy();
        let cut = s.rfind(['\\', '/']).map_or(0, |i| i + 1);
        let dir = PathBuf::from(&s[..cut]);
        Paths {
            game: dir.join(GAME_EXE),
            dll: dir.join(PATCH_DLL),
            dir,
        }
    }
}

/// The client's command line: the bare program name, then each argument after one space,
/// quoted by the Microsoft C runtime rules where needed.
pub fn command_line(args: &[OsString]) -> Vec<u16> {
    let mut out: Vec<u16> = GAME_EXE.encode_utf16().collect();
    for a in args {
        out.push(u16::from(b' '));
        quote(&a.encode_wide().collect::<Vec<_>>(), &mut out);
    }
    out
}

fn quote(arg: &[u16], out: &mut Vec<u16>) {
    let plain = !arg.is_empty()
        && arg
            .iter()
            .all(|&c| c > 0x20 && c != 0x7F && c != u16::from(b'"'));
    if plain {
        out.extend_from_slice(arg);
        return;
    }
    let bs = u16::from(b'\\');
    let q = u16::from(b'"');
    out.push(q);
    let mut slashes = 0usize;
    for &c in arg {
        if c == bs {
            slashes += 1;
            continue;
        }
        if c == q {
            // Backslashes before a quote are doubled, and the quote escaped.
            out.extend(std::iter::repeat_n(bs, slashes * 2 + 1));
        } else {
            out.extend(std::iter::repeat_n(bs, slashes));
        }
        slashes = 0;
        out.push(c);
    }
    // ...and doubled before the closing quote.
    out.extend(std::iter::repeat_n(bs, slashes * 2));
    out.push(q);
}

fn wide(s: &OsStr) -> Vec<u16> {
    s.encode_wide().chain([0]).collect()
}

/// Creates the client suspended, loads the DLL into it, resumes it. Returns the client's pid.
pub fn launch(paths: &Paths, args: &[OsString]) -> Result<u32, String> {
    let app = wide(paths.game.as_os_str());
    let mut cmd = command_line(args);
    cmd.push(0);
    let mut dir = wide(paths.dir.as_os_str());
    if dir.len() == 1 {
        dir = wide(OsStr::new("."));
    }
    let si = STARTUPINFOW {
        cb: size_of::<STARTUPINFOW>() as u32,
        // SAFETY: plain data, zero is valid.
        ..unsafe { std::mem::zeroed() }
    };
    // SAFETY: plain data, filled in by CreateProcessW.
    let mut pi: PROCESS_INFORMATION = unsafe { std::mem::zeroed() };
    // SAFETY: NUL-terminated strings; the command line buffer is writable.
    let ok = unsafe {
        CreateProcessW(
            app.as_ptr(),
            cmd.as_mut_ptr(),
            std::ptr::null(),
            std::ptr::null(),
            0,
            CREATE_SUSPENDED,
            std::ptr::null(),
            dir.as_ptr(),
            &si,
            &mut pi,
        )
    };
    if ok == 0 {
        return Err(format!("CreateProcess ({})", last_error()));
    }
    let result = inject(pi.hProcess, &wide(paths.dll.as_os_str())).and_then(|()| {
        // SAFETY: the main thread, created suspended.
        if unsafe { ResumeThread(pi.hThread) } == u32::MAX {
            Err(format!("ResumeThread ({})", last_error()))
        } else {
            Ok(pi.dwProcessId)
        }
    });
    // SAFETY: handles from CreateProcessW.
    unsafe {
        if result.is_err() {
            TerminateProcess(pi.hProcess, ABANDONED);
        }
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
    }
    result
}

/// `LoadLibraryW(dll)` on a remote thread of `process`, waited for.
fn inject(process: HANDLE, dll: &[u16]) -> Result<(), String> {
    let bytes = std::mem::size_of_val(dll);
    let k32: Vec<u16> = "kernel32.dll".encode_utf16().chain([0]).collect();
    // SAFETY: kernel32 is loaded at the same address in every process of one boot.
    let load = unsafe {
        GetProcAddress(
            GetModuleHandleW(k32.as_ptr()),
            c"LoadLibraryW".as_ptr().cast(),
        )
    }
    .ok_or("LoadLibraryW not found")?;
    // SAFETY: memory in the child, freed below.
    let mem = unsafe {
        VirtualAllocEx(
            process,
            std::ptr::null(),
            bytes,
            MEM_COMMIT | MEM_RESERVE,
            PAGE_READWRITE,
        )
    };
    if mem.is_null() {
        return Err(format!("VirtualAllocEx ({})", last_error()));
    }
    let r = (|| {
        // SAFETY: `bytes` bytes allocated above.
        if unsafe {
            WriteProcessMemory(
                process,
                mem,
                dll.as_ptr().cast(),
                bytes,
                std::ptr::null_mut(),
            )
        } == 0
        {
            return Err(format!("WriteProcessMemory ({})", last_error()));
        }
        // SAFETY: LoadLibraryW takes one pointer argument, as a thread routine does.
        let start: unsafe extern "system" fn(*mut c_void) -> u32 =
            unsafe { std::mem::transmute(load) };
        // SAFETY: a thread in the child at LoadLibraryW(mem).
        let th = unsafe {
            CreateRemoteThread(
                process,
                std::ptr::null(),
                0,
                Some(start),
                mem,
                0,
                std::ptr::null_mut(),
            )
        };
        if th.is_null() {
            return Err(format!("CreateRemoteThread ({})", last_error()));
        }
        let mut code = 0u32;
        // SAFETY: the thread created above, closed here.
        let waited = unsafe {
            let w = WaitForSingleObject(th, INFINITE);
            GetExitCodeThread(th, &mut code);
            CloseHandle(th);
            w
        };
        if waited != WAIT_OBJECT_0 {
            return Err(format!("waiting for the loader thread ({})", last_error()));
        }
        // The low 32 bits of the module handle; 0 is a failed load.
        if code == 0 {
            return Err("load failed".into());
        }
        Ok(())
    })();
    // SAFETY: allocated above.
    unsafe { VirtualFreeEx(process, mem, 0, MEM_RELEASE) };
    r
}

fn warn(text: &str) {
    let t: Vec<u16> = text.encode_utf16().chain([0]).collect();
    let title: Vec<u16> = TITLE.encode_utf16().chain([0]).collect();
    // SAFETY: NUL-terminated strings.
    unsafe {
        MessageBoxW(
            std::ptr::null_mut(),
            t.as_ptr(),
            title.as_ptr(),
            MB_ICONWARNING,
        )
    };
}

fn last_error() -> u32 {
    // SAFETY: plain query.
    unsafe { windows_sys::Win32::Foundation::GetLastError() }
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows_sys::Win32::Foundation::LocalFree;
    use windows_sys::Win32::System::Threading::{
        GetExitCodeProcess, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, PROCESS_SYNCHRONIZE,
    };
    use windows_sys::Win32::UI::Shell::CommandLineToArgvW;

    fn argv(cmd: &[u16]) -> Vec<String> {
        let mut c = cmd.to_vec();
        c.push(0);
        let mut n = 0;
        // SAFETY: NUL-terminated; the array is freed below.
        unsafe {
            let a = CommandLineToArgvW(c.as_ptr(), &mut n);
            assert!(!a.is_null());
            let out = (0..n as usize)
                .map(|i| {
                    let p = *a.add(i);
                    let len = (0..).take_while(|&k| *p.add(k) != 0).count();
                    String::from_utf16_lossy(std::slice::from_raw_parts(p, len))
                })
                .collect();
            LocalFree(a.cast());
            out
        }
    }

    #[test]
    fn command_line_passes_every_argument_through() {
        let cases: &[&[&str]] = &[
            &[],
            &["-krqlv=SD"],
            &[
                "",
                "a b",
                "tab\there",
                "say \"hi\"",
                r"C:\dir\",
                r"C:\a b\",
                r#"x\"y"#,
                r#"\\\""#,
                "plain",
            ],
            &[r"a\\b", "end\\", "\"", "\\\\"],
        ];
        for &args in cases {
            let os: Vec<OsString> = args.iter().map(OsString::from).collect();
            let cmd = command_line(&os);
            let got = argv(&cmd);
            assert_eq!(got[0], GAME_EXE);
            assert_eq!(&got[1..], args, "{}", String::from_utf16_lossy(&cmd));
        }
        assert_eq!(
            String::from_utf16_lossy(&command_line(&[OsString::from("-krqlv=SD")])),
            "Client-Win64-Shipping.exe -krqlv=SD"
        );
    }

    #[test]
    fn game_and_dll_beside_the_launcher() {
        let p = Paths::beside(Path::new(r"C:\g\Client\Binaries\Win64\tidelab_start.exe"));
        assert_eq!(
            p.game,
            PathBuf::from(r"C:\g\Client\Binaries\Win64\Client-Win64-Shipping.exe")
        );
        assert_eq!(
            p.dll,
            PathBuf::from(r"C:\g\Client\Binaries\Win64\tidelab_patch.dll")
        );
        assert_eq!(p.dir.as_os_str(), r"C:\g\Client\Binaries\Win64\");
        let p = Paths::beside(Path::new("C:/g/tidelab_start.exe"));
        assert_eq!(p.dir.as_os_str(), "C:/g/");
    }

    fn system32() -> PathBuf {
        PathBuf::from(std::env::var("SystemRoot").unwrap()).join("System32")
    }

    fn wait_exit(pid: u32) -> u32 {
        // SAFETY: a process we started; closed below.
        unsafe {
            let h = OpenProcess(
                PROCESS_SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                0,
                pid,
            );
            assert!(!h.is_null());
            WaitForSingleObject(h, 10_000);
            let mut code = 0;
            GetExitCodeProcess(h, &mut code);
            CloseHandle(h);
            code
        }
    }

    /// The whole launch on a harmless program: cmd.exe, any directory, a system DLL injected.
    #[test]
    fn launch_injects_and_resumes() {
        let dir = system32();
        let mut d = dir.as_os_str().to_os_string();
        d.push("\\");
        let paths = Paths {
            game: dir.join("cmd.exe"),
            dll: dir.join("version.dll"),
            dir: PathBuf::from(d),
        };
        let pid = launch(&paths, &["/c".into(), "exit 5".into()]).unwrap();
        assert_eq!(wait_exit(pid), 5);
    }

    #[test]
    fn a_failed_load_ends_the_client() {
        let dir = system32();
        let paths = Paths {
            game: dir.join("cmd.exe"),
            dll: dir.join("no_such_tidelab.dll"),
            dir: dir.clone(),
        };
        assert_eq!(
            launch(&paths, &["/c".into(), "exit 5".into()]),
            Err("load failed".into())
        );
    }
}
