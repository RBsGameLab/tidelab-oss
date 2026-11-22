// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! What starting a server or the game does, as a list of steps: the list is what the tests
//! check, `run` is the only part that touches the machine.

use std::path::PathBuf;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Step {
    /// Delete the file if it is there.
    Remove(PathBuf),
    /// Create an empty file if it is not there.
    Touch(PathBuf),
    /// Copy over; failing is not fatal (the file in use keeps the old copy).
    Copy {
        from: PathBuf,
        to: PathBuf,
    },
    Spawn(Spawn),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Spawn {
    pub exe: PathBuf,
    pub args: Vec<String>,
    pub cwd: Option<PathBuf>,
    /// No console window.
    pub hidden: bool,
    /// stderr to this file, stdout to the same name plus `.out` (as the lab's scripts do).
    pub log: Option<PathBuf>,
}

impl Spawn {
    pub fn new(exe: impl Into<PathBuf>) -> Spawn {
        Spawn {
            exe: exe.into(),
            args: Vec::new(),
            cwd: None,
            hidden: true,
            log: None,
        }
    }
}

/// Runs the steps in order. Returns one line per step for the launcher's log; the first fatal
/// failure stops the list.
pub fn run(steps: &[Step]) -> Result<Vec<String>, String> {
    let mut notes = Vec::new();
    for step in steps {
        match step {
            Step::Remove(p) => {
                if p.exists() {
                    std::fs::remove_file(p).map_err(|e| format!("删不掉 {}：{e}", p.display()))?;
                    notes.push(format!("删除 {}", name(p)));
                }
            }
            Step::Touch(p) => {
                if !p.exists() {
                    std::fs::write(p, b"").map_err(|e| format!("建不了 {}：{e}", p.display()))?;
                    notes.push(format!("创建 {}", name(p)));
                }
            }
            Step::Copy { from, to } => match std::fs::copy(from, to) {
                Ok(_) => notes.push(format!("安装 {}", name(to))),
                Err(e) => notes.push(format!("没装上 {}（{e}），沿用旧的", name(to))),
            },
            Step::Spawn(s) => {
                if !s.exe.exists() {
                    return Err(format!("找不到 {}", s.exe.display()));
                }
                spawn(s).map_err(|e| format!("启动 {} 失败：{e}", s.exe.display()))?;
                notes.push(format!("启动 {}", name(&s.exe)));
            }
        }
    }
    Ok(notes)
}

fn name(p: &std::path::Path) -> String {
    p.file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| p.display().to_string())
}

/// How many old logs of one server `archive_log` keeps.
const ARCHIVED_LOGS: usize = 30;

/// Moves a log a start would overwrite to `<dir>\archive\<stem>-<modified, unix s>.log` and keeps
/// the newest `keep` of that stem (R-166: tools/tl_audit.py reads a play session's server log
/// after restarts; start-server.cmd does the same). Best effort: failing never stops a start.
fn archive_log(log: &std::path::Path, keep: usize) {
    let Ok(meta) = std::fs::metadata(log) else {
        return;
    };
    let (Some(dir), Some(stem)) = (log.parent(), log.file_stem().and_then(|s| s.to_str())) else {
        return;
    };
    if meta.len() == 0 {
        return;
    }
    let secs = meta
        .modified()
        .ok()
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map_or(0, |d| d.as_secs());
    let archive = dir.join("archive");
    if std::fs::create_dir_all(&archive).is_err() {
        return;
    }
    let to = archive.join(format!("{stem}-{secs}.log"));
    if std::fs::rename(log, &to).is_err() {
        let _ = std::fs::copy(log, &to);
    }
    let prefix = format!("{stem}-");
    let mut old: Vec<PathBuf> = std::fs::read_dir(&archive)
        .into_iter()
        .flatten()
        .flatten()
        .map(|e| e.path())
        .filter(|p| {
            p.file_name().and_then(|n| n.to_str()).is_some_and(|n| {
                n.strip_prefix(&prefix)
                    .and_then(|r| r.strip_suffix(".log"))
                    .is_some_and(|d| !d.is_empty() && d.bytes().all(|b| b.is_ascii_digit()))
            })
        })
        .collect();
    old.sort();
    for p in old.iter().take(old.len().saturating_sub(keep)) {
        let _ = std::fs::remove_file(p);
    }
}

fn spawn(s: &Spawn) -> std::io::Result<()> {
    use std::process::{Command, Stdio};
    let mut c = Command::new(&s.exe);
    c.args(&s.args).stdin(Stdio::null());
    if let Some(cwd) = &s.cwd {
        c.current_dir(cwd);
    }
    match &s.log {
        Some(log) => {
            if let Some(dir) = log.parent() {
                std::fs::create_dir_all(dir)?;
            }
            archive_log(log, ARCHIVED_LOGS);
            let mut out = log.clone().into_os_string();
            out.push(".out");
            c.stderr(std::fs::File::create(log)?);
            c.stdout(std::fs::File::create(PathBuf::from(out))?);
        }
        None => {
            c.stdout(Stdio::null()).stderr(Stdio::null());
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        const DETACHED_PROCESS: u32 = 0x0000_0008;
        c.creation_flags(if s.hidden {
            CREATE_NO_WINDOW
        } else {
            DETACHED_PROCESS
        });
    }
    match c.spawn() {
        Ok(_) => Ok(()),
        // ERROR_ELEVATION_REQUIRED: the program's manifest asks for elevation (tidelab_start.exe,
        // highestAvailable) and CreateProcess never shows UAC. ShellExecute does, as `start` in
        // start-game.cmd does. Its output cannot be redirected, which a visible program does not need.
        #[cfg(windows)]
        Err(e) if e.raw_os_error() == Some(740) && s.log.is_none() => shell_execute(s),
        Err(e) => Err(e),
    }
}

/// The command line for `args`, quoted the way the MSVC runtime splits it (what Rust's own
/// Command does): only arguments with spaces, tabs or quotes, or empty ones, are quoted.
pub fn command_line(args: &[String]) -> String {
    let mut out = String::new();
    for (i, a) in args.iter().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        if !a.is_empty() && !a.contains([' ', '\t', '"']) {
            out.push_str(a);
            continue;
        }
        out.push('"');
        let mut backslashes = 0;
        for c in a.chars() {
            match c {
                '\\' => backslashes += 1,
                '"' => {
                    out.extend(std::iter::repeat_n('\\', backslashes * 2 + 1));
                    out.push('"');
                    backslashes = 0;
                }
                _ => {
                    out.extend(std::iter::repeat_n('\\', backslashes));
                    out.push(c);
                    backslashes = 0;
                }
            }
        }
        out.extend(std::iter::repeat_n('\\', backslashes * 2));
        out.push('"');
    }
    out
}

#[cfg(windows)]
fn shell_execute(s: &Spawn) -> std::io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::UI::Shell::{SEE_MASK_NOASYNC, SHELLEXECUTEINFOW, ShellExecuteExW};
    use windows_sys::Win32::UI::WindowsAndMessaging::{SW_HIDE, SW_SHOWNORMAL};
    fn wide(s: &std::ffi::OsStr) -> Vec<u16> {
        s.encode_wide().chain(std::iter::once(0)).collect()
    }
    let verb = wide("open".as_ref());
    let file = wide(s.exe.as_os_str());
    let params = wide(command_line(&s.args).as_ref());
    let dir = s.cwd.as_ref().map(|d| wide(d.as_os_str()));
    // SAFETY: SHELLEXECUTEINFOW is plain data; all-zero is its documented empty value.
    let mut info: SHELLEXECUTEINFOW = unsafe { std::mem::zeroed() };
    info.cbSize = std::mem::size_of::<SHELLEXECUTEINFOW>() as u32;
    info.fMask = SEE_MASK_NOASYNC;
    info.lpVerb = verb.as_ptr();
    info.lpFile = file.as_ptr();
    info.lpParameters = params.as_ptr();
    info.lpDirectory = dir.as_ref().map_or(std::ptr::null(), |d| d.as_ptr());
    info.nShow = if s.hidden { SW_HIDE } else { SW_SHOWNORMAL };
    // SAFETY: every pointer in `info` points into a buffer that outlives the call.
    if unsafe { ShellExecuteExW(&mut info) } == 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quotes_like_the_msvc_runtime() {
        let a = |v: &[&str]| command_line(&v.iter().map(|s| s.to_string()).collect::<Vec<_>>());
        assert_eq!(
            a(&["-krqlv=SD", "-RbAccount=3838"]),
            "-krqlv=SD -RbAccount=3838"
        );
        assert_eq!(a(&["a b", ""]), "\"a b\" \"\"");
        assert_eq!(a(&["say \"hi\""]), r#""say \"hi\"""#);
        assert_eq!(a(&[r"C:\dir with space\"]), r#""C:\dir with space\\""#);
        assert_eq!(a(&[r"C:\plain\"]), r"C:\plain\");
    }

    #[test]
    fn files_steps() {
        let dir = crate::text::test_dir("steps");
        let a = dir.join("a.quiet");
        let src = dir.join("src.pak");
        std::fs::write(&src, b"pak").unwrap();
        std::fs::write(&a, b"").unwrap();
        let notes = run(&[
            Step::Remove(a.clone()),
            Step::Remove(dir.join("none")),
            Step::Touch(dir.join("b.quiet")),
            Step::Copy {
                from: src.clone(),
                to: dir.join("dst.pak"),
            },
            Step::Copy {
                from: dir.join("missing.pak"),
                to: dir.join("dst2.pak"),
            },
        ])
        .unwrap();
        assert!(!a.exists());
        assert!(dir.join("b.quiet").exists());
        assert_eq!(std::fs::read(dir.join("dst.pak")).unwrap(), b"pak");
        assert_eq!(notes.len(), 4, "{notes:?}");
        assert!(notes[3].contains("沿用旧的"));
    }

    #[test]
    fn a_log_a_start_overwrites_is_archived_and_old_ones_pruned() {
        let dir = crate::text::test_dir("archive_log");
        let log = dir.join("gamesv.log");
        let archive = dir.join("archive");
        std::fs::create_dir_all(&archive).unwrap();
        for old in [
            "gamesv-1000000001.log",
            "gamesv-1000000002.log",
            "loginsv-1.log",
        ] {
            std::fs::write(archive.join(old), b"old").unwrap();
        }
        std::fs::write(&log, b"play session").unwrap();
        archive_log(&log, 2);
        assert!(!log.exists());
        let mut names: Vec<String> = std::fs::read_dir(&archive)
            .unwrap()
            .map(|e| e.unwrap().file_name().into_string().unwrap())
            .collect();
        names.sort();
        assert_eq!(names.len(), 3, "{names:?}");
        assert_eq!(names[0], "gamesv-1000000002.log");
        assert!(names[1].starts_with("gamesv-") && names[1] != names[0]);
        assert_eq!(
            std::fs::read(archive.join(&names[1])).unwrap(),
            b"play session"
        );
        assert_eq!(names[2], "loginsv-1.log");
        std::fs::write(&log, b"").unwrap();
        archive_log(&log, 2); // an empty log is not worth keeping
        assert!(log.exists());
        archive_log(&dir.join("none.log"), 2);
    }

    #[test]
    fn a_missing_program_stops_the_list() {
        let dir = crate::text::test_dir("steps_spawn");
        let err = run(&[
            Step::Spawn(Spawn::new(dir.join("none.exe"))),
            Step::Touch(dir.join("after")),
        ])
        .unwrap_err();
        assert!(err.contains("none.exe"));
        assert!(!dir.join("after").exists());
    }
}
