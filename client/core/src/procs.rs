// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The processes on this machine, by executable name.

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Proc {
    pub pid: u32,
    /// The executable's file name, e.g. `Tidelab.GameServer.exe`.
    pub name: String,
}

/// Every process, from one Toolhelp snapshot. Empty if the snapshot fails.
#[cfg(windows)]
pub fn list() -> Vec<Proc> {
    use windows_sys::Win32::Foundation::{CloseHandle, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::System::Diagnostics::ToolHelp::{
        CreateToolhelp32Snapshot, PROCESSENTRY32W, Process32FirstW, Process32NextW,
        TH32CS_SNAPPROCESS,
    };
    let mut out = Vec::new();
    // SAFETY: plain Win32 calls on a zeroed, sized entry; the handle is closed below.
    unsafe {
        let snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
        if snap == INVALID_HANDLE_VALUE {
            return out;
        }
        let mut e: PROCESSENTRY32W = std::mem::zeroed();
        e.dwSize = std::mem::size_of::<PROCESSENTRY32W>() as u32;
        let mut ok = Process32FirstW(snap, &mut e);
        while ok != 0 {
            let len = e
                .szExeFile
                .iter()
                .position(|&c| c == 0)
                .unwrap_or(e.szExeFile.len());
            out.push(Proc {
                pid: e.th32ProcessID,
                name: String::from_utf16_lossy(&e.szExeFile[..len]),
            });
            ok = Process32NextW(snap, &mut e);
        }
        CloseHandle(snap);
    }
    out
}

#[cfg(not(windows))]
pub fn list() -> Vec<Proc> {
    Vec::new()
}

/// Ends the process at once, as `taskkill /F` does.
#[cfg(windows)]
pub fn kill(pid: u32) -> bool {
    use windows_sys::Win32::Foundation::CloseHandle;
    use windows_sys::Win32::System::Threading::{OpenProcess, PROCESS_TERMINATE, TerminateProcess};
    // SAFETY: plain Win32 calls; the handle is checked and closed.
    unsafe {
        let h = OpenProcess(PROCESS_TERMINATE, 0, pid);
        if h.is_null() {
            return false;
        }
        let ok = TerminateProcess(h, 1) != 0;
        CloseHandle(h);
        ok
    }
}

#[cfg(not(windows))]
pub fn kill(_pid: u32) -> bool {
    false
}

/// The processes whose executable is `name` (case does not matter).
pub fn named<'a>(procs: &'a [Proc], name: &str) -> impl Iterator<Item = &'a Proc> {
    procs
        .iter()
        .filter(move |p| p.name.eq_ignore_ascii_case(name))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    #[cfg(windows)]
    fn the_list_has_this_process() {
        let me = std::process::id();
        let all = list();
        let p = all.iter().find(|p| p.pid == me).expect("this process");
        assert!(p.name.to_ascii_lowercase().ends_with(".exe"), "{}", p.name);
    }

    #[test]
    fn named_ignores_case() {
        let procs = vec![
            Proc {
                pid: 1,
                name: "Client-Win64-Shipping.exe".into(),
            },
            Proc {
                pid: 2,
                name: "other.exe".into(),
            },
        ];
        assert_eq!(
            named(&procs, "client-win64-shipping.exe")
                .map(|p| p.pid)
                .collect::<Vec<_>>(),
            [1]
        );
    }
}
