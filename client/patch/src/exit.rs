// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Clean exit watch: releases held worker threads once the game window closes and
//! ensures the client process terminates cleanly. Status goes to `tidelab_ace.status`.

use std::sync::atomic::Ordering;
use std::time::{Duration, Instant};

use windows_sys::Win32::Foundation::{HWND, LPARAM};
use windows_sys::Win32::System::Threading::{
    GetCurrentProcess, GetCurrentProcessId, TerminateProcess,
};
use windows_sys::Win32::UI::WindowsAndMessaging::{
    EnumWindows, GetClassNameW, GetWindowThreadProcessId, IsWindowVisible,
};

use crate::gates;

pub const ARM_MS: u64 = 30_000;
pub const GONE_MS: u64 = 3_000;
pub const KILL_MS: u64 = 15_000;
const POLL: Duration = Duration::from_millis(500);
const WINDOW_CLASS: &str = "UnrealWindow";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Action {
    None,
    /// The window stayed long enough: the watch is armed.
    Armed,
    /// The game is exiting: let the held threads go.
    Release,
    /// Still alive after the release: end the process.
    Terminate,
}

/// The watch, without the clock or the window lookup, so the rules can be tested.
#[derive(Debug, Default)]
pub struct Watch {
    seen_since: Option<u64>,
    armed: bool,
    gone_since: Option<u64>,
    released_at: Option<u64>,
    terminated: bool,
}

impl Watch {
    /// Lower CPU load.
    pub fn step(&mut self, now_ms: u64, window: bool, held: bool) -> Action {
        if let Some(at) = self.released_at {
            if !self.terminated && held && now_ms.saturating_sub(at) >= KILL_MS {
                self.terminated = true;
                return Action::Terminate;
            }
            return Action::None;
        }
        if window {
            self.gone_since = None;
            let since = *self.seen_since.get_or_insert(now_ms);
            if !self.armed && now_ms.saturating_sub(since) >= ARM_MS {
                self.armed = true;
                return Action::Armed;
            }
            return Action::None;
        }
        self.seen_since = None;
        if !self.armed {
            return Action::None;
        }
        let gone = *self.gone_since.get_or_insert(now_ms);
        if now_ms.saturating_sub(gone) >= GONE_MS {
            self.released_at = Some(now_ms);
            return Action::Release;
        }
        Action::None
    }
}

/// Starts the watch thread (in the client only; DllMain decides).
pub fn start(note: fn(&str)) {
    let spawned = std::thread::Builder::new()
        .name("tidelab exit watch".into())
        .spawn(move || run(note));
    if let Err(e) = spawned {
        note(&format!("exit watch: not started ({e})"));
    }
}

fn run(note: fn(&str)) {
    let start = Instant::now();
    let mut watch = Watch::default();
    loop {
        std::thread::sleep(POLL);
        let now = start.elapsed().as_millis() as u64;
        let held = gates::FROZEN.load(Ordering::SeqCst) > 0;
        match watch.step(now, game_window(), held) {
            Action::None => {}
            Action::Armed => note("exit watch: armed (the game window has been up 30 s)"),
            Action::Release => {
                let n = gates::release();
                note(&format!(
                    "exit watch: the game window is gone, the game is exiting: {n} held .sig thread(s) let go"
                ));
            }
            Action::Terminate => {
                note("exit watch: the process is still alive 15 s after the release: ending it");
                // SAFETY: ends this process; the engine has shut its modules down by now.
                unsafe { TerminateProcess(GetCurrentProcess(), 0) };
            }
        }
    }
}

/// Whether this process has a visible top-level window of the engine's class.
pub fn game_window() -> bool {
    let mut found = false;
    // SAFETY: the callback only reads window properties; `found` outlives the call.
    unsafe { EnumWindows(Some(each_window), &mut found as *mut bool as LPARAM) };
    found
}

unsafe extern "system" fn each_window(hwnd: HWND, data: LPARAM) -> i32 {
    let mut pid = 0u32;
    // SAFETY: plain queries on a window handle EnumWindows gave us.
    unsafe { GetWindowThreadProcessId(hwnd, &mut pid) };
    // SAFETY: plain query.
    if pid != unsafe { GetCurrentProcessId() } || unsafe { IsWindowVisible(hwnd) } == 0 {
        return 1;
    }
    let mut class = [0u16; 64];
    // SAFETY: the buffer and its length.
    let n = unsafe { GetClassNameW(hwnd, class.as_mut_ptr(), class.len() as i32) };
    if n > 0 && String::from_utf16_lossy(&class[..n as usize]) == WINDOW_CLASS {
        // SAFETY: `data` is the `&mut bool` game_window passed.
        unsafe { *(data as *mut bool) = true };
        return 0;
    }
    1
}

#[cfg(test)]
mod tests {
    use super::*;

    fn run_steps(
        watch: &mut Watch,
        from: u64,
        to: u64,
        window: bool,
        held: bool,
    ) -> Vec<(u64, Action)> {
        let mut out = Vec::new();
        let mut t = from;
        while t <= to {
            let a = watch.step(t, window, held);
            if a != Action::None {
                out.push((t, a));
            }
            t += 500;
        }
        out
    }

    #[test]
    fn exit_after_a_session_releases_then_terminates() {
        let mut w = Watch::default();
        assert_eq!(
            run_steps(&mut w, 0, 60_000, true, true),
            [(30_000, Action::Armed)]
        );
        // The window closes at 60.5 s: release 3 s later, terminate 15 s after that.
        assert_eq!(
            run_steps(&mut w, 60_500, 100_000, false, true),
            [(63_500, Action::Release), (78_500, Action::Terminate)]
        );
        assert_eq!(w.step(200_000, false, true), Action::None);
    }

    #[test]
    fn start_up_windows_do_not_arm() {
        // A splash window for 10 s, nothing for 20 s: never armed, nothing happens.
        let mut w = Watch::default();
        assert!(run_steps(&mut w, 0, 10_000, true, true).is_empty());
        assert!(run_steps(&mut w, 10_500, 30_000, false, true).is_empty());
        // The window has to stay 30 s without a break.
        assert!(run_steps(&mut w, 30_500, 60_000, true, true).is_empty());
        assert_eq!(w.step(60_500, true, true), Action::Armed);
    }

    #[test]
    fn a_short_gap_is_not_an_exit() {
        let mut w = Watch::default();
        run_steps(&mut w, 0, 40_000, true, true);
        // Gone for 2.5 s (a mode switch), then back.
        assert!(run_steps(&mut w, 40_500, 43_000, false, true).is_empty());
        assert!(run_steps(&mut w, 43_500, 60_000, true, true).is_empty());
        assert_eq!(
            run_steps(&mut w, 60_500, 64_000, false, true),
            [(63_500, Action::Release)]
        );
    }

    #[test]
    fn nothing_is_terminated_when_the_gate_held_no_thread() {
        let mut w = Watch::default();
        run_steps(&mut w, 0, 40_000, true, false);
        assert_eq!(
            run_steps(&mut w, 40_500, 100_000, false, false),
            [(43_500, Action::Release)]
        );
    }

    #[test]
    fn this_test_process_has_no_game_window() {
        assert!(!game_window());
    }
}
