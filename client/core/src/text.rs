// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Reading the ends of text files the game and the servers keep rewriting.

use std::io::{Read, Seek, SeekFrom};
use std::path::Path;

/// The last `max_lines` lines of `path`, from at most its last 64 KB; bytes that are not UTF-8
/// are replaced. Missing file: `None`.
pub fn tail(path: &Path, max_lines: usize) -> Option<Vec<String>> {
    let mut f = std::fs::File::open(path).ok()?;
    let len = f.metadata().ok()?.len();
    let from = len.saturating_sub(64 * 1024);
    f.seek(SeekFrom::Start(from)).ok()?;
    let mut buf = Vec::new();
    f.read_to_end(&mut buf).ok()?;
    let text = String::from_utf8_lossy(&buf);
    let mut lines: Vec<&str> = text.lines().collect();
    if from > 0 && !lines.is_empty() {
        lines.remove(0); // cut in the middle
    }
    let skip = lines.len().saturating_sub(max_lines);
    Some(lines[skip..].iter().map(|l| l.to_string()).collect())
}

/// The first `max_lines` lines of `path`. Missing file: `None`.
pub fn head(path: &Path, max_lines: usize) -> Option<Vec<String>> {
    let bytes = std::fs::read(path).ok()?;
    let text = String::from_utf8_lossy(&bytes);
    Some(
        text.lines()
            .take(max_lines)
            .map(|l| l.to_string())
            .collect(),
    )
}

/// A fresh, empty directory for one test.
#[cfg(test)]
pub fn test_dir(name: &str) -> std::path::PathBuf {
    use std::sync::atomic::{AtomicU32, Ordering};
    static N: AtomicU32 = AtomicU32::new(0);
    let dir = std::env::temp_dir().join(format!(
        "tidelab_client_{}_{}_{}",
        name,
        std::process::id(),
        N.fetch_add(1, Ordering::Relaxed)
    ));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).unwrap();
    dir
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tail_keeps_the_last_lines() {
        let dir = test_dir("tail");
        let p = dir.join("a.log");
        std::fs::write(&p, "1\r\n2\r\n3\r\n4\r\n").unwrap();
        assert_eq!(tail(&p, 2).unwrap(), vec!["3", "4"]);
        assert_eq!(tail(&p, 10).unwrap().len(), 4);
        assert!(tail(&dir.join("none"), 3).is_none());
    }

    #[test]
    fn tail_of_a_big_file_drops_the_cut_line() {
        let dir = test_dir("tail_big");
        let p = dir.join("big.log");
        let mut s = String::new();
        for i in 0..20_000 {
            s.push_str(&format!("line {i}\n"));
        }
        std::fs::write(&p, s).unwrap();
        let t = tail(&p, 100_000).unwrap();
        assert_eq!(t.last().unwrap(), "line 19999");
        assert!(t[0].starts_with("line "), "{}", t[0]);
        assert!(t.len() < 20_000);
    }

    #[test]
    fn head_keeps_the_first_lines() {
        let dir = test_dir("head");
        let p = dir.join("s.txt");
        std::fs::write(&p, "a\nb\nc\n").unwrap();
        assert_eq!(head(&p, 2).unwrap(), vec!["a", "b"]);
    }
}
