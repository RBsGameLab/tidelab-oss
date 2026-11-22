// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! Where things are. No machine paths are written in (R-171): the defaults follow the tidelab
//! checkout the launcher was built in (the lab and the state directory beside it),
//! `tidelab.local.txt` at the checkout's root overrides them for this machine (the scripts read
//! the same file), and `tidelab-launcher.toml` beside the launcher can override any field.

use serde::Deserialize;
use std::path::{Path, PathBuf};

pub const FILE_NAME: &str = "tidelab-launcher.toml";
/// R-171: `key=value` lines (`lab`, `state`, `game`, `go`; `#` comments) shared with
/// `tools\paths.cmd` and `tools\tl_paths.py`.
pub const LOCAL_FILE: &str = "tidelab.local.txt";
/// The data folder name and the state folder's, beside the checkout by default.
pub const LAB_NAME: &str = "tidelab-data";
pub const STATE_NAME: &str = "tidelab-state";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Config {
    /// The game-data workspace (game data, client, optional tools): default `<checkout>\..\tidelab-data`.
    pub lab: PathBuf,
    /// The tidelab checkout whose Go servers run: `<tidelab>\out\gobin\*.exe`, with the
    /// binary cache in `<tidelab>\out\bincache`.
    pub tidelab: PathBuf,
    /// The 3.8 client's `Binaries\Win64` (the patch's switch files and auto combat files are here).
    /// Default: `<lab>\Game38\Client\Binaries\Win64`.
    pub game_bin: Option<PathBuf>,
    /// The tidelab servers' state directory. Default: `<checkout>\..\tidelab-state`.
    pub state_dir: Option<PathBuf>,
    /// The exported game data: `BinData`, `BinData-beta`, `questgen\assets\quest`.
    /// Default: `<lab>\gamedata\3.8`.
    pub game_data: Option<PathBuf>,
    /// Optional CPU power-cap script (`power=` in `tidelab.local.txt`).
    pub power_script: Option<PathBuf>,
}

/// `tidelab-launcher.toml`: any field, the rest kept.
#[derive(Debug, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
struct Overrides {
    lab: Option<PathBuf>,
    tidelab: Option<PathBuf>,
    game_bin: Option<PathBuf>,
    state_dir: Option<PathBuf>,
    game_data: Option<PathBuf>,
    power_script: Option<PathBuf>,
}

impl Default for Config {
    /// Relative to the working directory; the launcher uses [`Config::for_exe`].
    fn default() -> Self {
        Config::for_repo(Path::new("."))
    }
}

fn beside(repo: &Path, name: &str) -> PathBuf {
    match repo.parent() {
        Some(p) if !p.as_os_str().is_empty() => p.join(name),
        _ => repo.join("..").join(name),
    }
}

impl Config {
    /// The defaults for a checkout at `repo`: the lab and the state directory beside it.
    pub fn for_repo(repo: &Path) -> Config {
        Config {
            lab: beside(repo, LAB_NAME),
            tidelab: repo.to_path_buf(),
            game_bin: None,
            state_dir: None,
            game_data: None,
            power_script: None,
        }
    }

    /// The checkout holding `dir` (the launcher sits in `<checkout>\client\target\release`):
    /// the nearest ancestor with `start-server.cmd`.
    pub fn find_repo(dir: &Path) -> Option<PathBuf> {
        dir.ancestors()
            .find(|d| d.join("start-server.cmd").is_file())
            .map(Path::to_path_buf)
    }

    /// Applies `tidelab.local.txt`: `lab`, `state` and `game` (the client's root, holding
    /// `Client`); other keys are the scripts'.
    pub fn apply_local(&mut self, text: &str) {
        for line in text.lines() {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') {
                continue;
            }
            let Some((key, value)) = line.split_once('=') else {
                continue;
            };
            let value = PathBuf::from(value.trim());
            match key.trim().to_ascii_lowercase().as_str() {
                "lab" => self.lab = value,
                "state" => self.state_dir = Some(value),
                "game" => self.game_bin = Some(value.join(r"Client\Binaries\Win64")),
                "power" => self.power_script = Some(value),
                _ => {}
            }
        }
    }

    /// The launcher's configuration: the checkout it was built in (or `exe_dir` itself), its
    /// `tidelab.local.txt`, then `tidelab-launcher.toml` beside the launcher.
    pub fn for_exe(exe_dir: &Path) -> (Config, Option<String>) {
        let repo = Config::find_repo(exe_dir).unwrap_or_else(|| exe_dir.to_path_buf());
        let mut base = Config::for_repo(&repo);
        if let Ok(text) = std::fs::read_to_string(repo.join(LOCAL_FILE)) {
            base.apply_local(&text);
        }
        Config::load(base, &exe_dir.join(FILE_NAME))
    }

    /// `base` with the fields `path` sets. A missing file is `base`; a broken one is `base` plus
    /// the error, which the launcher shows.
    pub fn load(base: Config, path: &Path) -> (Config, Option<String>) {
        match std::fs::read_to_string(path) {
            Err(_) => (base, None),
            Ok(text) => match toml::from_str::<Overrides>(&text) {
                Ok(o) => {
                    let mut c = base;
                    if let Some(v) = o.lab {
                        c.lab = v;
                    }
                    if let Some(v) = o.tidelab {
                        c.tidelab = v;
                    }
                    c.game_bin = o.game_bin.or(c.game_bin);
                    c.state_dir = o.state_dir.or(c.state_dir);
                    c.game_data = o.game_data.or(c.game_data);
                    c.power_script = o.power_script.or(c.power_script);
                    (c, None)
                }
                Err(e) => (base, Some(format!("{}: {}", path.display(), e))),
            },
        }
    }

    pub fn game_bin(&self) -> PathBuf {
        self.game_bin
            .clone()
            .unwrap_or_else(|| self.lab.join(r"Game38\Client\Binaries\Win64"))
    }

    /// `Client\Content\Paks`, two levels above `Binaries\Win64`.
    pub fn game_paks(&self) -> PathBuf {
        let bin = self.game_bin();
        match bin.parent().and_then(Path::parent) {
            Some(client) => client.join(r"Content\Paks"),
            None => bin.join(r"..\..\Content\Paks"),
        }
    }

    pub fn state_dir(&self) -> PathBuf {
        self.state_dir
            .clone()
            .unwrap_or_else(|| beside(&self.tidelab, STATE_NAME))
    }

    pub fn game_data(&self) -> PathBuf {
        self.game_data
            .clone()
            .unwrap_or_else(|| self.lab.join(r"gamedata\3.8"))
    }

    /// Where `go build -o ..\out\gobin\ ./cmd/...` (run in `server`) puts the servers.
    pub fn go_bin(&self) -> PathBuf {
        self.tidelab.join(r"out\gobin")
    }

    /// The Go servers' logs, as `<tidelab>\start-server.cmd` writes them.
    pub fn go_logs(&self) -> PathBuf {
        self.tidelab.join(r"out\logs")
    }

    /// The C# patch pak installed at each start (R-107): the one `tools\build_cspak.py` wrote
    /// into `<tidelab>\out\paks` when there is one, else the lab's copy (the same bytes).
    pub fn cs_pak(&self) -> PathBuf {
        let built = self.cs_pak_built();
        if built.exists() {
            built
        } else {
            self.cs_pak_lab()
        }
    }

    pub fn cs_pak_built(&self) -> PathBuf {
        self.tidelab.join(r"out\paks\rb38_cs_300_p.pak")
    }

    /// The login pak (R-146), as `tools\build_loginpak.py` writes it; installed at each start
    /// when it is built.
    pub fn login_pak_built(&self) -> PathBuf {
        self.tidelab.join(r"out\paks\rb38_login_100_p.pak")
    }

    pub fn cs_pak_lab(&self) -> PathBuf {
        self.lab.join(r"backups\v38\rb38_cs_300_p.pak")
    }

    /// Where `cargo build --release` puts tidelab's launcher and patch DLL (R-144h).
    pub fn patch_built(&self) -> PathBuf {
        self.tidelab.join("client").join("target").join("release")
    }

    /// Optional helper script that caps the CPU while the game runs.
    pub fn power_script(&self) -> PathBuf {
        self.power_script
            .clone()
            .unwrap_or_else(|| self.lab.join(r"tools\power\cpu-cap.ps1"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults_follow_the_checkout() {
        let c = Config::for_repo(Path::new(r"D:\work\tidelab"));
        assert_eq!(c.lab, PathBuf::from(r"D:\work\tidelab-data"));
        assert_eq!(
            c.game_bin(),
            PathBuf::from(r"D:\work\tidelab-data\Game38\Client\Binaries\Win64")
        );
        assert_eq!(
            c.game_paks(),
            PathBuf::from(r"D:\work\tidelab-data\Game38\Client\Content\Paks")
        );
        assert_eq!(c.state_dir(), PathBuf::from(r"D:\work\tidelab-state"));
        assert_eq!(
            c.game_data(),
            PathBuf::from(r"D:\work\tidelab-data\gamedata\3.8")
        );
        assert_eq!(
            c.patch_built(),
            PathBuf::from(r"D:\work\tidelab\client\target\release")
        );
        assert_eq!(c.go_bin(), PathBuf::from(r"D:\work\tidelab\out\gobin"));
        assert_eq!(c.go_logs(), PathBuf::from(r"D:\work\tidelab\out\logs"));
        assert_eq!(
            c.cs_pak_built(),
            PathBuf::from(r"D:\work\tidelab\out\paks\rb38_cs_300_p.pak")
        );
        assert_eq!(
            c.cs_pak_lab(),
            PathBuf::from(r"D:\work\tidelab-data\backups\v38\rb38_cs_300_p.pak")
        );
    }

    #[test]
    fn the_local_file_moves_lab_state_and_game() {
        let mut c = Config::for_repo(Path::new(r"D:\work\tidelab"));
        c.apply_local("# this machine\nlab = E:\\lab\nstate=F:\\saves\ngame=G:\\WW\ngo=H:\\go\\bin\\go.exe\nnot a line\n");
        assert_eq!(c.lab, PathBuf::from(r"E:\lab"));
        assert_eq!(c.state_dir(), PathBuf::from(r"F:\saves"));
        assert_eq!(c.game_bin(), PathBuf::from(r"G:\WW\Client\Binaries\Win64"));
        assert_eq!(c.game_paks(), PathBuf::from(r"G:\WW\Client\Content\Paks"));
        assert_eq!(c.game_data(), PathBuf::from(r"E:\lab\gamedata\3.8"));
        assert_eq!(c.tidelab, PathBuf::from(r"D:\work\tidelab"));
    }

    #[test]
    fn the_launcher_finds_its_checkout_and_reads_both_files() {
        let dir = crate::text::test_dir("forexe");
        let repo = dir.join("tl");
        let exe_dir = repo.join(r"client\target\release");
        std::fs::create_dir_all(&exe_dir).unwrap();
        std::fs::write(repo.join("start-server.cmd"), b"").unwrap();
        std::fs::write(repo.join(LOCAL_FILE), "state=Z:\\s\n").unwrap();
        std::fs::write(exe_dir.join(FILE_NAME), "game_data = 'Y:\\data'").unwrap();
        let (c, err) = Config::for_exe(&exe_dir);
        assert!(err.is_none());
        assert_eq!(c.tidelab, repo);
        assert_eq!(c.lab, dir.join(LAB_NAME));
        assert_eq!(c.state_dir(), PathBuf::from(r"Z:\s"));
        assert_eq!(c.game_data(), PathBuf::from(r"Y:\data"));
    }

    #[test]
    fn the_built_cs_pak_wins_over_the_labs() {
        let dir = crate::text::test_dir("cspak");
        let c = Config {
            tidelab: dir.clone(),
            ..Config::default()
        };
        assert_eq!(c.cs_pak(), c.cs_pak_lab());
        std::fs::create_dir_all(dir.join(r"out\paks")).unwrap();
        std::fs::write(c.cs_pak_built(), b"pak").unwrap();
        assert_eq!(c.cs_pak(), c.cs_pak_built());
    }

    #[test]
    fn a_missing_toml_is_the_base_and_a_broken_one_says_why() {
        let dir = crate::text::test_dir("config");
        let base = Config::for_repo(Path::new(r"D:\work\tidelab"));
        let (c, err) = Config::load(base.clone(), &dir.join("none.toml"));
        assert_eq!(c, base);
        assert!(err.is_none());

        let bad = dir.join("bad.toml");
        std::fs::write(&bad, "labb = 1").unwrap();
        let (c, err) = Config::load(base.clone(), &bad);
        assert_eq!(c, base);
        assert!(err.unwrap().contains("labb"));

        let some = dir.join("some.toml");
        std::fs::write(
            &some,
            "lab = 'D:\\lab'\ngame_bin = 'E:\\g\\Client\\Binaries\\Win64'",
        )
        .unwrap();
        let (c, _) = Config::load(base, &some);
        assert_eq!(c.lab, PathBuf::from(r"D:\lab"));
        assert_eq!(c.tidelab, PathBuf::from(r"D:\work\tidelab"));
        assert_eq!(c.game_paks(), PathBuf::from(r"E:\g\Client\Content\Paks"));
        assert_eq!(c.state_dir(), PathBuf::from(r"D:\work\tidelab-state"));
    }
}
