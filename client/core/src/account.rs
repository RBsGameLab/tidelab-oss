// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! R-145: the account the game logs in with. The launcher passes it to the client as
//! `-RbAccount=<account>`; rbauto (`tools/rbauto/RBLogin.cs`, hooked in by cspatch) reads it,
//! skips the client's debug login view and opens the official "click to enter" screen.
//!
//! It is kept in `tidelab-account.txt` beside the launcher (one line); no file means 3838, the
//! lab's 3.8 account (save 777).

use std::path::Path;

pub const FILE_NAME: &str = "tidelab-account.txt";
pub const DEFAULT: &str = "3838";
/// The client splits its command line on spaces (`LoginModel.GetServerPort` does too), and the
/// account goes into the login URL; keep it to what needs no quoting or escaping.
pub const MAX_LEN: usize = 64;

/// Why `account` cannot be passed to the client, or None when it can.
pub fn problem(account: &str) -> Option<&'static str> {
    if account.is_empty() {
        return Some("账号不能为空");
    }
    if account.chars().count() > MAX_LEN {
        return Some("账号太长（最多 64 个字符）");
    }
    if !account
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || matches!(c, '_' | '-' | '.'))
    {
        return Some("账号只能用字母、数字和 _ - .");
    }
    None
}

/// The saved account, or 3838 when there is none or it is not usable.
pub fn load(dir: &Path) -> String {
    match std::fs::read_to_string(dir.join(FILE_NAME)) {
        Ok(text) => {
            let a = text.trim();
            if problem(a).is_none() {
                a.to_string()
            } else {
                DEFAULT.to_string()
            }
        }
        Err(_) => DEFAULT.to_string(),
    }
}

pub fn save(dir: &Path, account: &str) -> std::io::Result<()> {
    if let Some(p) = problem(account) {
        return Err(std::io::Error::new(std::io::ErrorKind::InvalidInput, p));
    }
    std::fs::write(dir.join(FILE_NAME), format!("{account}\r\n"))
}

/// The client argument.
pub fn arg(account: &str) -> String {
    format!("-RbAccount={account}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_plain_accounts() {
        for a in ["3838", "test_1", "a.b-c", "X"] {
            assert_eq!(problem(a), None, "{a}");
        }
    }

    #[test]
    fn refuses_what_the_command_line_or_url_would_break() {
        for a in ["", "38 38", "a\"b", "a&b", "a=b", "账号", "a/b"] {
            assert!(problem(a).is_some(), "{a:?}");
        }
        assert!(problem(&"a".repeat(MAX_LEN)).is_none());
        assert!(problem(&"a".repeat(MAX_LEN + 1)).is_some());
    }

    #[test]
    fn saves_and_loads() {
        let dir = crate::text::test_dir("account");
        assert_eq!(load(&dir), "3838");
        save(&dir, "test_1").unwrap();
        assert_eq!(load(&dir), "test_1");
        assert!(save(&dir, "bad one").is_err());
        assert_eq!(load(&dir), "test_1");
        std::fs::write(dir.join(FILE_NAME), "bad one\n").unwrap();
        assert_eq!(load(&dir), "3838");
    }

    #[test]
    fn the_argument() {
        assert_eq!(arg("3838"), "-RbAccount=3838");
    }
}
