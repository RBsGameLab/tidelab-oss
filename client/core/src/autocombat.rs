// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The auto combat buttons (R-30, R-75; was tools\autocombat\panel.ps1). rbauto in the game
//! reads `auto_mode.txt` in `Binaries\Win64` once a second and writes `auto_status.txt`.

use std::path::Path;
use std::time::{Duration, SystemTime};

pub const MODE_FILE: &str = "auto_mode.txt";
pub const STATUS_FILE: &str = "auto_status.txt";
/// A status file older than this means the script in the game is not running.
pub const STALE_AFTER: Duration = Duration::from_secs(5);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    Off,
    Dodge,
    Counter,
    Base,
    Rb,
}

impl Mode {
    pub const ALL: [Mode; 5] = [Mode::Off, Mode::Dodge, Mode::Counter, Mode::Base, Mode::Rb];
    pub const DEFAULT: Mode = Mode::Dodge;

    pub fn word(self) -> &'static str {
        match self {
            Mode::Off => "off",
            Mode::Dodge => "dodge",
            Mode::Counter => "counter",
            Mode::Base => "base",
            Mode::Rb => "rb",
        }
    }

    /// `team` is R-75's old name for `base`.
    pub fn parse(word: &str) -> Option<Mode> {
        match word {
            "team" => Some(Mode::Base),
            _ => Mode::ALL.into_iter().find(|m| m.word() == word),
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Mode::Off => "关闭",
            Mode::Dodge => "只闪避",
            Mode::Counter => "闪避反击",
            Mode::Base => "基准连招",
            Mode::Rb => "专用连招",
        }
    }

    pub fn tip(self) -> &'static str {
        match self {
            Mode::Off => "什么都不做",
            Mode::Dodge => "自动完美闪避，其余全由你操作",
            Mode::Counter => "完美闪避 + 闪避反击 + 弹反窗口，连招还是你自己打（R-75）",
            Mode::Base => "加上 ok-ww 给这队写的轮换，作为比较基准（R-75）",
            Mode::Rb => "加上 R-75 为莫特斐/忌炎/守岸人写的轮换",
        }
    }
}

/// The mode in `dir`; no file or an unknown word is the default.
pub fn read_mode(dir: &Path) -> Mode {
    std::fs::read_to_string(dir.join(MODE_FILE))
        .ok()
        .and_then(|t| t.split_whitespace().next().and_then(Mode::parse))
        .unwrap_or(Mode::DEFAULT)
}

pub fn write_mode(dir: &Path, mode: Mode) -> std::io::Result<()> {
    std::fs::write(dir.join(MODE_FILE), format!("{}\r\n", mode.word()))
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Status {
    /// No status file yet: it appears a few seconds into the game.
    Missing,
    /// Not rewritten for this many seconds.
    Stale(u64),
    /// Its lines, without the `time` line.
    Live(Vec<String>),
}

pub fn read_status(dir: &Path, now: SystemTime) -> Status {
    let p = dir.join(STATUS_FILE);
    let Ok(meta) = std::fs::metadata(&p) else {
        return Status::Missing;
    };
    let age = meta
        .modified()
        .ok()
        .and_then(|m| now.duration_since(m).ok())
        .unwrap_or_default();
    if age > STALE_AFTER {
        return Status::Stale(age.as_secs());
    }
    let Ok(bytes) = std::fs::read(&p) else {
        return Status::Missing;
    };
    Status::Live(
        String::from_utf8_lossy(&bytes)
            .lines()
            .filter(|l| !l.starts_with("time "))
            .map(str::to_string)
            .collect(),
    )
}

pub fn status_text(mode: Mode, status: &Status) -> String {
    let mut s = format!("当前模式：{}\n", mode.label());
    match status {
        Status::Missing => s.push_str("还没有状态：进游戏后几秒内出现"),
        Status::Stale(secs) => {
            s.push_str(&format!("游戏里的脚本没有在运行（状态 {secs} 秒没更新）"))
        }
        Status::Live(lines) => {
            s.push('\n');
            s.push_str(&lines.join("\n"));
        }
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_is_dodge_and_buttons_write_the_word() {
        let dir = crate::text::test_dir("auto_mode");
        assert_eq!(read_mode(&dir), Mode::Dodge);
        for m in [Mode::Counter, Mode::Base, Mode::Rb, Mode::Off] {
            write_mode(&dir, m).unwrap();
            assert_eq!(
                std::fs::read_to_string(dir.join(MODE_FILE)).unwrap(),
                format!("{}\r\n", m.word())
            );
            assert_eq!(read_mode(&dir), m);
        }
    }

    #[test]
    fn old_and_odd_mode_files() {
        let dir = crate::text::test_dir("auto_old");
        std::fs::write(dir.join(MODE_FILE), "team\r\n").unwrap();
        assert_eq!(read_mode(&dir), Mode::Base, "R-75 renamed team to base");
        std::fs::write(dir.join(MODE_FILE), "  counter  extra\n").unwrap();
        assert_eq!(read_mode(&dir), Mode::Counter);
        std::fs::write(dir.join(MODE_FILE), "fly\n").unwrap();
        assert_eq!(read_mode(&dir), Mode::Dodge);
        std::fs::write(dir.join(MODE_FILE), "").unwrap();
        assert_eq!(read_mode(&dir), Mode::Dodge);
    }

    #[test]
    fn status_missing_live_and_stale() {
        let dir = crate::text::test_dir("auto_status");
        assert_eq!(read_status(&dir, SystemTime::now()), Status::Missing);
        assert!(status_text(Mode::Dodge, &Status::Missing).contains("还没有状态"));

        std::fs::write(
            dir.join(STATUS_FILE),
            "version 2\r\nmode base\r\nperfect dodges 3\r\ntime x\r\n",
        )
        .unwrap();
        let live = read_status(&dir, SystemTime::now());
        assert_eq!(
            live,
            Status::Live(vec![
                "version 2".into(),
                "mode base".into(),
                "perfect dodges 3".into()
            ])
        );
        let text = status_text(Mode::Base, &live);
        assert!(text.contains("perfect dodges 3") && !text.contains("time x"));
        assert!(text.starts_with("当前模式：基准连招"));

        let later = SystemTime::now() + Duration::from_secs(30);
        let stale = read_status(&dir, later);
        assert!(matches!(stale, Status::Stale(s) if s >= 29), "{stale:?}");
        assert!(status_text(Mode::Base, &stale).contains("没有在运行"));
    }
}
