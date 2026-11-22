// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! tidelab's helper DLL for the 3.8 client (specs/client-patch.md). `tidelab_start.exe`
//! (client/start) starts the client suspended and loads this DLL; DllMain installs the
//! hooks (`gates`, on top of `hook`), starts the sampling profiler (`profiler`), the clean
//! exit watch (`exit`), and the CPU load limiter.

pub mod ace;
pub mod beside;
pub mod exit;
pub mod export;
pub mod gates;
pub mod hook;
mod nt;
pub mod profiler;

use std::ffi::c_void;

use windows_sys::Win32::System::SystemServices::DLL_PROCESS_ATTACH;

/// On process attach, on the loader thread while the main thread is suspended:
/// installs hooks, starts the exit watch, sampling profiler, and CPU load limiter.
#[unsafe(no_mangle)]
pub extern "system" fn DllMain(module: *mut c_void, reason: u32, _reserved: *mut c_void) -> i32 {
    if reason == DLL_PROCESS_ATTACH {
        if beside::in_client() {
            for line in gates::install(ace::note_process, ace::note).lines {
                ace::note(&line);
            }
            // R-148: lets the gate's held thread go when the game exits.
            exit::start(ace::note);
        }
        profiler::start(module);
        ace::init(module);
        // R-174d: -RbExport=<folder> copies the client's files out of its paks, then ends it.
        export::start(ace::note);
    }
    1
}
