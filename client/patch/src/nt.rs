// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The ntdll calls windows-sys does not declare.

use std::ffi::c_void;

use windows_sys::Win32::Foundation::HANDLE;

/// ThreadQuerySetWin32StartAddress: where the thread was started (the address CreateThread got).
pub const THREAD_QUERY_SET_WIN32_START_ADDRESS: u32 = 9;
/// ThreadBasicInformation: `ThreadBasicInformation` below.
pub const THREAD_BASIC_INFORMATION_CLASS: u32 = 0;

#[repr(C)]
#[derive(Default)]
pub struct ThreadBasicInformation {
    pub exit_status: i32,
    pub teb_base_address: usize,
    pub unique_process: usize,
    pub unique_thread: usize,
    pub affinity_mask: usize,
    pub priority: i32,
    pub base_priority: i32,
}

#[link(name = "ntdll")]
unsafe extern "system" {
    /// The next thread of `process` after `thread` (null: the first), opened with `access`.
    pub fn NtGetNextThread(
        process: HANDLE,
        thread: HANDLE,
        access: u32,
        attributes: u32,
        flags: u32,
        next: *mut HANDLE,
    ) -> i32;
    pub fn NtQueryInformationThread(
        thread: HANDLE,
        class: u32,
        info: *mut c_void,
        len: u32,
        ret_len: *mut u32,
    ) -> i32;
}
