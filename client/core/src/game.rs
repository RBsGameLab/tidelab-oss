// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Starting the 3.8 client the way `start-game.cmd` does, server aside: through tidelab's own
//! launcher and patch DLL (client/start, client/patch), copied from `client\target\release`
//! beside the client at each start.

use crate::config::Config;
use crate::markers::Switch;
use crate::procs::{self, Proc};
use crate::steps::{Spawn, Step};
use std::path::PathBuf;

pub const CLIENT_EXE: &str = "Client-Win64-Shipping.exe";
pub const LAUNCHER: &str = "tidelab_start.exe";
pub const PATCH_DLL: &str = "tidelab_patch.dll";
/// R-157: the CPU cap while the game runs, both core classes (R-54 used 3500, R-157 first 3300).
pub const CPU_CAP_MHZ: u32 = 3000;

/// Whether tidelab's launcher and patch DLL are built (`cargo build --release` in `client`).
pub fn built(cfg: &Config) -> bool {
    cfg.patch_built().join(LAUNCHER).exists() && cfg.patch_built().join(PATCH_DLL).exists()
}

pub fn client_pid(procs: &[Proc]) -> Option<u32> {
    procs::named(procs, CLIENT_EXE).next().map(|p| p.pid)
}

/// `account` goes to the client as `-RbAccount=` (R-145): the login skips the debug view.
/// Fails when the launcher and patch DLL are not built.
pub fn start_steps(cfg: &Config, account: &str) -> Result<Vec<Step>, String> {
    if !built(cfg) {
        return Err(format!(
            r"{} 里没有 {LAUNCHER} 和 {PATCH_DLL}：先在 client 里 cargo build --release",
            cfg.patch_built().display()
        ));
    }
    let bin = cfg.game_bin();
    let mut power = Spawn::new(PathBuf::from(
        r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
    ));
    power.args = vec![
        "-NoProfile".into(),
        "-ExecutionPolicy".into(),
        "Bypass".into(),
        "-WindowStyle".into(),
        "Hidden".into(),
        "-File".into(),
        cfg.power_script().display().to_string(),
        "-UntilGameExits".into(),
        // R-157: both core classes (the script's own default is 3500).
        "-MaxMHz".into(),
        CPU_CAP_MHZ.to_string(),
        "-MaxMHzP".into(),
        CPU_CAP_MHZ.to_string(),
    ];
    let mut game = Spawn::new(bin.join(LAUNCHER));
    game.args = vec!["-krqlv=SD".into(), crate::account::arg(account)];
    game.cwd = Some(bin.clone());
    game.hidden = false;
    let mut steps = vec![
        // Lower CPU load: enabled by rbauto after the first world load.
        Step::Remove(bin.join(Switch::AceQuiet.file_name())),
        Step::Touch(bin.join(Switch::PerfsightQuiet.file_name())),
    ];
    if cfg.power_script().exists() {
        steps.push(Step::Spawn(power)); // R-54
    }
    steps.push(Step::Copy {
        from: cfg.cs_pak(),
        to: cfg.game_paks().join("rb38_cs_300_p.pak"),
    }); // R-107
    if cfg.login_pak_built().exists() {
        steps.push(Step::Copy {
            from: cfg.login_pak_built(),
            to: cfg.game_paks().join("rb38_login_100_p.pak"),
        }); // R-146
    }
    for f in [LAUNCHER, PATCH_DLL] {
        steps.push(Step::Copy {
            from: cfg.patch_built().join(f),
            to: bin.join(f),
        });
    }
    steps.push(Step::Spawn(game));
    Ok(steps)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn built_cfg(name: &str) -> Config {
        let dir = crate::text::test_dir(name);
        // Both roots in the test dir: a test must never write outside its own temp dir (the power
        // script test once emptied the real power script at every cargo test).
        let cfg = Config {
            lab: dir.join("lab"),
            tidelab: dir,
            ..Config::default()
        };
        std::fs::create_dir_all(cfg.patch_built()).unwrap();
        std::fs::write(cfg.patch_built().join(LAUNCHER), b"exe").unwrap();
        std::fs::write(cfg.patch_built().join(PATCH_DLL), b"dll").unwrap();
        cfg
    }

    #[test]
    fn caps_the_cpu_at_3000_mhz() {
        let cfg = built_cfg("cpucap");
        let script = cfg.power_script();
        std::fs::create_dir_all(script.parent().unwrap()).unwrap();
        std::fs::write(&script, b"").unwrap();
        let steps = start_steps(&cfg, "3838").unwrap();
        let power = steps
            .iter()
            .find_map(|s| match s {
                Step::Spawn(p) if p.args.iter().any(|a| a == "-UntilGameExits") => Some(p),
                _ => None,
            })
            .unwrap();
        let args = power.args.join(" ");
        assert!(
            args.ends_with("-UntilGameExits -MaxMHz 3000 -MaxMHzP 3000"),
            "{args}"
        );
    }

    #[test]
    fn starts_like_start_game_cmd() {
        let cfg = built_cfg("startgame");
        let steps = start_steps(&cfg, "3838").unwrap();
        let bin = cfg.game_bin();
        assert_eq!(steps[0], Step::Remove(bin.join("tidelab_ace.quiet")));
        assert_eq!(steps[1], Step::Touch(bin.join("tidelab_perfsight.quiet")));
        let copy = steps
            .iter()
            .find(|s| matches!(s, Step::Copy { .. }))
            .unwrap();
        assert_eq!(
            copy,
            &Step::Copy {
                from: cfg.cs_pak(),
                to: cfg
                    .lab
                    .join(r"Game38\Client\Content\Paks\rb38_cs_300_p.pak"),
            }
        );
        for f in [LAUNCHER, PATCH_DLL] {
            assert!(steps.contains(&Step::Copy {
                from: cfg.patch_built().join(f),
                to: bin.join(f),
            }));
        }
        // The login pak only when it is built.
        let login = |s: &Step| matches!(s, Step::Copy { from, .. } if from.ends_with("rb38_login_100_p.pak"));
        assert!(!steps.iter().any(login));
        std::fs::create_dir_all(cfg.login_pak_built().parent().unwrap()).unwrap();
        std::fs::write(cfg.login_pak_built(), b"pak").unwrap();
        let steps = start_steps(&cfg, "3838").unwrap();
        assert!(
            steps.contains(&Step::Copy {
                from: cfg.login_pak_built(),
                to: cfg
                    .lab
                    .join(r"Game38\Client\Content\Paks\rb38_login_100_p.pak"),
            })
        );
        let Step::Spawn(game) = steps.last().unwrap() else {
            panic!()
        };
        assert_eq!(game.exe, bin.join("tidelab_start.exe"));
        assert_eq!(game.args, ["-krqlv=SD", "-RbAccount=3838"]);
        assert_eq!(game.cwd.as_ref(), Some(&bin));
    }

    #[test]
    fn refuses_to_start_without_our_launcher_and_dll() {
        let dir = crate::text::test_dir("patchbuilt");
        let cfg = Config {
            tidelab: dir.clone(),
            ..Config::default()
        };
        assert!(!built(&cfg));
        assert!(
            start_steps(&cfg, "3838")
                .unwrap_err()
                .contains("cargo build")
        );
        std::fs::create_dir_all(cfg.patch_built()).unwrap();
        std::fs::write(cfg.patch_built().join(LAUNCHER), b"exe").unwrap();
        assert!(!built(&cfg));
        std::fs::write(cfg.patch_built().join(PATCH_DLL), b"dll").unwrap();
        assert!(built(&cfg));
    }

    #[test]
    fn finds_the_client() {
        let procs = vec![Proc {
            pid: 9,
            name: "Client-Win64-Shipping.exe".into(),
        }];
        assert_eq!(client_pid(&procs), Some(9));
        assert_eq!(client_pid(&[]), None);
    }
}
