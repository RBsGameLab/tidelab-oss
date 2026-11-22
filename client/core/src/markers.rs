// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The switch files beside the patch DLL in `Binaries\Win64` (R-15, R-20, R-20b). A switch is
//! on while its file exists. rbauto creates `tidelab_ace.quiet` after the first world load
//! (R-110), so the names must match `tools/rbauto` and `client/patch` (R-146: `tidelab_*`).

use std::path::Path;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Switch {
    AceQuiet,
    AceNoload,
    PerfsightQuiet,
    Profiler,
}

impl Switch {
    pub const ALL: [Switch; 4] = [
        Switch::AceQuiet,
        Switch::PerfsightQuiet,
        Switch::AceNoload,
        Switch::Profiler,
    ];

    pub fn file_name(self) -> &'static str {
        match self {
            Switch::AceQuiet => "tidelab_ace.quiet",
            Switch::AceNoload => "tidelab_ace.noload",
            Switch::PerfsightQuiet => "tidelab_perfsight.quiet",
            Switch::Profiler => "tidelab_prof.on",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Switch::AceQuiet => "降低后台 CPU 占用",
            Switch::AceNoload => "启动时降低 CPU 占用",
            Switch::PerfsightQuiet => "挂起 PerfSight",
            Switch::Profiler => "CPU 采样",
        }
    }

    pub fn tip(self) -> &'static str {
        match self {
            Switch::AceQuiet => {
                "降低后台线程 CPU 占用，随时开关。启动游戏时先关闭，进入世界后自动开启"
            }
            Switch::AceNoload => {
                "启动阶段降低 CPU 占用模式。只在游戏启动时读一次；如影响启动请关闭"
            }
            Switch::PerfsightQuiet => "R-20b：挂起 PerfSight 的上报线程，随时开关",
            Switch::Profiler => {
                "R-15：在游戏里打开开始采样，关掉后 Win64 下出 tidelab_prof_<pid>_<n>.txt"
            }
        }
    }

    /// Takes effect while the game runs (polled), or only at the next start.
    pub fn live(self) -> bool {
        !matches!(self, Switch::AceNoload)
    }
}

pub fn is_on(dir: &Path, s: Switch) -> bool {
    dir.join(s.file_name()).exists()
}

pub fn set(dir: &Path, s: Switch, on: bool) -> std::io::Result<()> {
    let p = dir.join(s.file_name());
    if on {
        if !p.exists() {
            std::fs::write(&p, b"")?;
        }
    } else if p.exists() {
        std::fs::remove_file(&p)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_switch_is_its_file() {
        let dir = crate::text::test_dir("markers");
        for s in Switch::ALL {
            assert!(!is_on(&dir, s));
            set(&dir, s, true).unwrap();
            assert!(is_on(&dir, s));
            set(&dir, s, true).unwrap(); // twice is fine
            set(&dir, s, false).unwrap();
            assert!(!is_on(&dir, s));
            set(&dir, s, false).unwrap();
        }
    }

    #[test]
    fn names_are_the_ones_rbauto_and_the_scripts_use() {
        let names: Vec<_> = Switch::ALL.iter().map(|s| s.file_name()).collect();
        assert_eq!(
            names,
            [
                "tidelab_ace.quiet",
                "tidelab_perfsight.quiet",
                "tidelab_ace.noload",
                "tidelab_prof.on"
            ]
        );
        assert!(!Switch::AceNoload.live());
        assert!(Switch::AceQuiet.live());
    }
}
