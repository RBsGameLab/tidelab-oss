// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! The server the game talks to: tidelab's three Go servers of `server/`, built into
//! `<tidelab>\out\gobin` and started the way `<tidelab>\start-server.cmd` starts them.

use crate::config::Config;
use crate::procs::{self, Proc};
use crate::steps::{Spawn, Step};
use std::path::PathBuf;

pub const LABEL: &str = "tidelab（Go）";

pub struct TidelabServer {
    /// The program's name under `out\gobin` (and its process name), without `.exe`.
    pub name: &'static str,
    pub port: u16,
}

pub const TIDELAB: [TidelabServer; 3] = [
    TidelabServer {
        name: "configsv",
        port: 5500,
    },
    TidelabServer {
        name: "loginsv",
        port: 10001,
    },
    TidelabServer {
        name: "gamesv",
        port: 13100,
    },
];

pub fn tidelab_exe(cfg: &Config, s: &TidelabServer) -> PathBuf {
    cfg.go_bin().join(format!("{}.exe", s.name))
}

/// `<tidelab>\out\logs\<name>.log`, as `<tidelab>\start-server.cmd` writes them.
pub fn tidelab_log(cfg: &Config, s: &TidelabServer) -> PathBuf {
    cfg.go_logs().join(format!("{}.log", s.name))
}

/// The server processes that run, as (executable, pid).
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct Running {
    pub procs: Vec<(String, u32)>,
}

impl Running {
    pub fn of(procs: &[Proc]) -> Running {
        let mut r = Running::default();
        for s in &TIDELAB {
            let exe = format!("{}.exe", s.name);
            r.procs
                .extend(procs::named(procs, &exe).map(|p| (p.name.clone(), p.pid)));
        }
        r
    }

    pub fn any(&self) -> bool {
        !self.procs.is_empty()
    }

    /// Some of the three run, not all: one stopped or crashed.
    pub fn partial(&self) -> bool {
        self.any()
            && TIDELAB.iter().any(|s| {
                let exe = format!("{}.exe", s.name);
                !self.procs.iter().any(|(n, _)| n.eq_ignore_ascii_case(&exe))
            })
    }
}

/// What starting the servers does. Fails if a build or the state copy is missing.
pub fn start_steps(cfg: &Config) -> Result<Vec<Step>, String> {
    let state = cfg.state_dir();
    if !state.join(r"gateway\client_public_key.der").exists() {
        return Err(format!(
            r"{} 里没有存档（缺 gateway\client_public_key.der）：先跑 {}\copy-state.cmd",
            state.display(),
            cfg.tidelab.display()
        ));
    }
    let data = cfg.game_data();
    let mut steps = Vec::new();
    for s in &TIDELAB {
        let exe = tidelab_exe(cfg, s);
        if !exe.exists() {
            return Err(format!(
                r"{} 不存在：先在 {}\server 里 go build -o ..\out\gobin\ ./cmd/...",
                exe.display(),
                cfg.tidelab.display()
            ));
        }
        let mut sp = Spawn::new(exe);
        sp.args = vec![
            "--listen_address".into(),
            format!("127.0.0.1:{}", s.port),
            "--state_dir".into(),
            state.display().to_string(),
        ];
        if s.name == "gamesv" {
            let more = [
                ("--bindata", data.join("BinData")),
                ("--more_bindata", data.join("BinData-beta")),
                ("--bin_cache", cfg.tidelab.join(r"out\bincache")),
                ("--quest_data", data.join(r"questgen\assets\quest")),
                ("--cmd_file", state.join("cmd.txt")),
            ];
            for (flag, path) in more {
                sp.args.push(flag.into());
                sp.args.push(path.display().to_string());
            }
        }
        sp.log = Some(tidelab_log(cfg, s));
        steps.push(Step::Spawn(sp));
    }
    Ok(steps)
}

/// Stops every server process. Returns how many were stopped.
pub fn stop(procs: &[Proc]) -> usize {
    Running::of(procs)
        .procs
        .iter()
        .filter(|(_, pid)| procs::kill(*pid))
        .count()
}

/// The log to show: the game server's.
pub fn log_path(cfg: &Config) -> PathBuf {
    tidelab_log(cfg, &TIDELAB[2])
}

/// Whether the game server's log says it listens.
pub fn listening(cfg: &Config) -> bool {
    let Some(lines) = crate::text::tail(&log_path(cfg), 2000) else {
        return false;
    };
    lines.iter().any(|l| l.contains("is listening at"))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn p(pid: u32, name: &str) -> Proc {
        Proc {
            pid,
            name: name.into(),
        }
    }

    #[test]
    fn which_servers_run() {
        let none = Running::of(&[p(1, "explorer.exe")]);
        assert!(!none.any() && !none.partial());
        let two = Running::of(&[p(1, "gamesv.exe"), p(2, "loginsv.exe")]);
        assert_eq!(two.procs.len(), 2);
        assert!(two.any() && two.partial());
        let all = Running::of(&[
            p(1, "gamesv.exe"),
            p(2, "loginsv.exe"),
            p(3, "configsv.exe"),
        ]);
        assert!(all.any() && !all.partial());
    }

    #[test]
    fn starts_like_start_server_cmd() {
        let dir = crate::text::test_dir("servers");
        let cfg = Config {
            lab: dir.join("lab"),
            tidelab: dir.join("tl"),
            state_dir: Some(dir.join("state")),
            ..Config::default()
        };
        assert!(start_steps(&cfg).unwrap_err().contains("copy-state.cmd"));
        let key = dir.join(r"state\gateway\client_public_key.der");
        std::fs::create_dir_all(key.parent().unwrap()).unwrap();
        std::fs::write(&key, b"").unwrap();
        assert!(start_steps(&cfg).unwrap_err().contains("go build"));
        for s in &TIDELAB {
            let exe = tidelab_exe(&cfg, s);
            std::fs::create_dir_all(exe.parent().unwrap()).unwrap();
            std::fs::write(&exe, b"").unwrap();
        }
        let steps = start_steps(&cfg).unwrap();
        let spawns: Vec<_> = steps
            .iter()
            .map(|s| match s {
                Step::Spawn(sp) => sp,
                _ => panic!(),
            })
            .collect();
        let state = dir.join("state").display().to_string();
        let config = spawns[0];
        assert!(config.exe.ends_with(r"tl\out\gobin\configsv.exe"));
        assert_eq!(
            config.args,
            [
                "--listen_address",
                "127.0.0.1:5500",
                "--state_dir",
                state.as_str()
            ]
        );
        assert_eq!(
            config.log.as_deref(),
            Some(dir.join(r"tl\out\logs\configsv.log").as_path())
        );
        assert!(spawns[1].exe.ends_with(r"tl\out\gobin\loginsv.exe"));
        assert_eq!(spawns[1].args[1], "127.0.0.1:10001");
        let game = spawns[2];
        assert!(game.exe.ends_with(r"tl\out\gobin\gamesv.exe"));
        let data = dir.join(r"lab\gamedata\3.8");
        let want: Vec<String> = vec![
            "--listen_address".into(),
            "127.0.0.1:13100".into(),
            "--state_dir".into(),
            state.clone(),
            "--bindata".into(),
            data.join("BinData").display().to_string(),
            "--more_bindata".into(),
            data.join("BinData-beta").display().to_string(),
            "--bin_cache".into(),
            dir.join(r"tl\out\bincache").display().to_string(),
            "--quest_data".into(),
            data.join(r"questgen\assets\quest").display().to_string(),
            "--cmd_file".into(),
            dir.join(r"state\cmd.txt").display().to_string(),
        ];
        assert_eq!(game.args, want);
        assert_eq!(log_path(&cfg), dir.join(r"tl\out\logs\gamesv.log"));
    }

    #[test]
    fn listening_reads_the_game_servers_log() {
        let dir = crate::text::test_dir("listening");
        let cfg = Config {
            lab: dir.clone(),
            tidelab: dir.join("tl"),
            ..Config::default()
        };
        std::fs::create_dir_all(cfg.go_logs()).unwrap();
        assert!(!listening(&cfg));
        std::fs::write(cfg.go_logs().join("gamesv.log"), "loading tables\n").unwrap();
        assert!(!listening(&cfg));
        std::fs::write(
            cfg.go_logs().join("gamesv.log"),
            "game server is listening at 127.0.0.1:13100\n",
        )
        .unwrap();
        assert!(listening(&cfg));
    }
}
