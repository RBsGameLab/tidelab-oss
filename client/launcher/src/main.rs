// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! R-143j: tidelab's launcher. One window for the Go server, the game, the patch's switch files
//! and the auto combat buttons.

#![windows_subsystem = "windows"]

use eframe::egui;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime};
use tidelab_client_core::autocombat::{self, Mode, Status};
use tidelab_client_core::config::Config;
use tidelab_client_core::mapped;
use tidelab_client_core::markers::{self, Switch};
use tidelab_client_core::procs::{self, Proc};
use tidelab_client_core::servers::{self, Running};
use tidelab_client_core::{account, game, steps, text};

const REFRESH: Duration = Duration::from_secs(1);
/// Ports take a moment to come free after the servers are stopped.
const RESTART_PAUSE: Duration = Duration::from_secs(2);

fn main() -> eframe::Result {
    // R-171: the checkout the launcher sits in gives the paths; nothing machine-specific is built in.
    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(PathBuf::from))
        .unwrap_or_else(|| PathBuf::from("."));
    let (cfg, cfg_error) = Config::for_exe(&exe_dir);
    // R-145: the account file sits beside the launcher, like its config.
    let account_dir = exe_dir;
    let options = eframe::NativeOptions {
        viewport: egui::ViewportBuilder::default()
            .with_title("Tidelab 启动器")
            .with_inner_size([560.0, 720.0])
            .with_min_inner_size([420.0, 360.0]),
        wgpu_options: wgpu_options(),
        ..Default::default()
    };
    eframe::run_native(
        "tidelab-launcher",
        options,
        Box::new(move |cc| {
            set_fonts(&cc.egui_ctx);
            Ok(Box::new(App::new(cfg, cfg_error, account_dir)))
        }),
    )
}

/// R-147c: draw with Direct3D 12's software renderer (WARP) when there is one. The window is a
/// few panels redrawn twice a second; on the GPU, wgpu loaded the NVIDIA driver (its D3D12 and
/// Vulkan halves and shader compiler, about 290 MB of modules) and the launcher held 173 MB.
/// `TIDELAB_LAUNCHER_GPU=1` draws on the graphics card again.
fn wgpu_options() -> eframe::egui_wgpu::WgpuConfiguration {
    use eframe::egui_wgpu::{WgpuConfiguration, WgpuSetup};
    use eframe::wgpu;
    let mut options = WgpuConfiguration::default();
    if std::env::var_os("TIDELAB_LAUNCHER_GPU").is_some_and(|v| v == "1") {
        return options;
    }
    if let WgpuSetup::CreateNew(setup) = &mut options.wgpu_setup {
        setup.instance_descriptor.backends = wgpu::Backends::DX12;
        setup.native_adapter_selector = Some(Arc::new(|adapters, surface| {
            let usable = |a: &&wgpu::Adapter| surface.is_none_or(|s| a.is_surface_supported(s));
            adapters
                .iter()
                .filter(usable)
                .find(|a| a.get_info().device_type == wgpu::DeviceType::Cpu)
                .or_else(|| adapters.iter().find(usable))
                .cloned()
                .ok_or_else(|| "no Direct3D 12 adapter can draw this window".to_string())
        }));
    }
    options
}

/// egui's own fonts have no Chinese: add Microsoft YaHei as the fallback of both families.
fn set_fonts(ctx: &egui::Context) {
    // R-147c: mapped, not read: 19 MB less private memory.
    let Some(bytes) = mapped::map_for_good(std::path::Path::new(r"C:\Windows\Fonts\msyh.ttc"))
    else {
        return;
    };
    let mut fonts = egui::FontDefinitions::default();
    fonts
        .font_data
        .insert("yahei".into(), Arc::new(egui::FontData::from_static(bytes)));
    for family in [egui::FontFamily::Proportional, egui::FontFamily::Monospace] {
        fonts
            .families
            .entry(family)
            .or_default()
            .push("yahei".into());
    }
    ctx.set_fonts(fonts);
}

enum Confirm {
    Stop,
    Restart,
}

struct App {
    cfg: Config,
    procs: Vec<Proc>,
    running: Running,
    client: Option<u32>,
    last_refresh: Option<Instant>,
    pending_start: Option<Instant>,
    confirm: Option<Confirm>,
    on_top: bool,
    mode: Mode,
    auto_status: Status,
    switches: Vec<(Switch, bool)>,
    ace_status: Vec<String>,
    server_tail: Vec<String>,
    listening: bool,
    notes: Vec<String>,
    /// R-145: the account the client logs in with (`-RbAccount=`), and where it is saved.
    account: String,
    account_dir: PathBuf,
}

impl App {
    fn new(cfg: Config, cfg_error: Option<String>, account_dir: PathBuf) -> App {
        let mut app = App {
            cfg,
            procs: Vec::new(),
            running: Running::default(),
            client: None,
            last_refresh: None,
            pending_start: None,
            confirm: None,
            on_top: false,
            mode: Mode::DEFAULT,
            auto_status: Status::Missing,
            switches: Vec::new(),
            ace_status: Vec::new(),
            server_tail: Vec::new(),
            listening: false,
            notes: Vec::new(),
            account: account::load(&account_dir),
            account_dir,
        };
        if let Some(e) = cfg_error {
            app.note(format!("配置文件读不了，用默认值：{e}"));
        }
        app.refresh();
        app
    }

    fn note(&mut self, s: impl Into<String>) {
        let now = chrono_like_now();
        let line = format!("{now}  {}", s.into());
        // R-145: the window's messages also go to tidelab-launcher.log beside the exe, so a
        // failed start can be read after the window is closed.
        let _ = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(self.account_dir.join("tidelab-launcher.log"))
            .and_then(|mut f| {
                use std::io::Write;
                writeln!(f, "{line}")
            });
        self.notes.push(line);
        let len = self.notes.len();
        if len > 200 {
            self.notes.drain(..len - 200);
        }
    }

    fn refresh(&mut self) {
        self.procs = procs::list();
        self.running = Running::of(&self.procs);
        self.client = game::client_pid(&self.procs);
        let bin = self.cfg.game_bin();
        self.mode = autocombat::read_mode(&bin);
        self.auto_status = autocombat::read_status(&bin, SystemTime::now());
        self.switches = Switch::ALL
            .iter()
            .map(|&s| (s, markers::is_on(&bin, s)))
            .collect();
        self.ace_status = text::head(&bin.join("tidelab_ace.status"), 40).unwrap_or_default();
        self.server_tail = text::tail(&servers::log_path(&self.cfg), 30).unwrap_or_default();
        self.listening = self.running.any() && servers::listening(&self.cfg);
        self.last_refresh = Some(Instant::now());
    }

    fn start_server(&mut self) {
        match servers::start_steps(&self.cfg).and_then(|s| steps::run(&s)) {
            Ok(notes) => {
                for n in notes {
                    self.note(n);
                }
                self.note(format!("{} 启动中", servers::LABEL));
            }
            Err(e) => self.note(format!("{} 没启动：{e}", servers::LABEL)),
        }
        self.refresh();
    }

    fn stop_server(&mut self) {
        let n = servers::stop(&self.procs);
        self.note(format!("停止 {}：{n} 个进程", servers::LABEL));
        self.refresh();
    }

    fn start_game(&mut self) {
        let acc = self.account.trim().to_string();
        if let Some(p) = account::problem(&acc) {
            self.note(format!("游戏没启动：{p}"));
            return;
        }
        if let Err(e) = account::save(&self.account_dir, &acc) {
            self.note(format!("账号存不了（这次照样用 {acc}）：{e}"));
        }
        match game::start_steps(&self.cfg, &acc).and_then(|s| steps::run(&s)) {
            Ok(notes) => {
                for n in notes {
                    self.note(n);
                }
                self.note(format!("游戏启动中（账号 {acc}）"));
            }
            Err(e) => self.note(format!("游戏没启动：{e}")),
        }
        self.refresh();
    }

    /// A stop kicks the player out of the game: ask first while the client runs.
    fn ask_or(&mut self, c: Confirm) {
        if self.client.is_some() {
            self.confirm = Some(c);
        } else {
            self.do_confirmed(c);
        }
    }

    fn do_confirmed(&mut self, c: Confirm) {
        match c {
            Confirm::Stop => self.stop_server(),
            Confirm::Restart => {
                self.stop_server();
                self.pending_start = Some(Instant::now() + RESTART_PAUSE);
            }
        }
    }

    fn server_ui(&mut self, ui: &mut egui::Ui) {
        let state = if self.running.any() {
            let n = self.running.procs.len();
            let up = if self.listening {
                "已就绪"
            } else {
                "启动中"
            };
            format!("{}：运行中，{n} 个进程，{up}", servers::LABEL)
        } else {
            format!("{}：没有在运行", servers::LABEL)
        };
        ui.label(state);
        if self.running.partial() {
            ui.colored_label(
                ui.visuals().warn_fg_color,
                "三个服务没有全在跑（有一个停了或崩了），点“重启”",
            );
        }
        if self.pending_start.is_some() {
            ui.label("马上启动……");
        }
        ui.horizontal(|ui| {
            let busy = self.pending_start.is_some();
            if !self.running.any() {
                if ui.add_enabled(!busy, egui::Button::new("启动")).clicked() {
                    self.start_server();
                }
            } else {
                if ui.add_enabled(!busy, egui::Button::new("重启")).clicked() {
                    self.ask_or(Confirm::Restart);
                }
                if ui.add_enabled(!busy, egui::Button::new("停止")).clicked() {
                    self.ask_or(Confirm::Stop);
                }
            }
        });
    }

    fn game_ui(&mut self, ui: &mut egui::Ui) {
        match self.client {
            Some(pid) => ui.label(format!("客户端运行中（pid {pid}）")),
            None => ui.label("客户端没在运行"),
        };
        let problem = account::problem(self.account.trim());
        ui.horizontal(|ui| {
            ui.label("账号");
            ui.add_enabled(
                self.client.is_none(),
                egui::TextEdit::singleline(&mut self.account).desired_width(160.0),
            )
            .on_hover_text("游戏里直接用这个账号登录，只剩官服的“点击进入”；下次启动游戏生效");
            if let Some(p) = problem {
                ui.colored_label(ui.visuals().warn_fg_color, p);
            }
        });
        ui.horizontal(|ui| {
            let can = self.client.is_none() && problem.is_none();
            if ui.add_enabled(can, egui::Button::new("启动游戏")).clicked() {
                self.start_game();
            }
            if self.client.is_none() && !self.listening {
                ui.colored_label(ui.visuals().warn_fg_color, "服务端还没就绪");
            }
        });
        if !game::built(&self.cfg) {
            ui.colored_label(
                ui.visuals().warn_fg_color,
                "启动器和补丁 DLL 还没编译：在 client 里 cargo build --release",
            );
        }
    }

    fn switches_ui(&mut self, ui: &mut egui::Ui) {
        let bin = self.cfg.game_bin();
        let mut changed = None;
        ui.horizontal_wrapped(|ui| {
            for (s, on) in &self.switches {
                let mut v = *on;
                let label = if s.live() {
                    s.label().to_string()
                } else {
                    format!("{}（下次启动生效）", s.label())
                };
                if ui.checkbox(&mut v, label).on_hover_text(s.tip()).changed() {
                    changed = Some((*s, v));
                }
            }
        });
        if let Some((s, v)) = changed {
            match markers::set(&bin, s, v) {
                Ok(()) => self.note(format!("{} {}", s.label(), if v { "打开" } else { "关闭" })),
                Err(e) => self.note(format!("{} 改不了：{e}", s.file_name())),
            }
            self.refresh();
        }
    }

    fn auto_ui(&mut self, ui: &mut egui::Ui) {
        let bin = self.cfg.game_bin();
        let mut picked = None;
        ui.horizontal(|ui| {
            for m in Mode::ALL {
                let b = egui::Button::selectable(self.mode == m, m.label())
                    .min_size(egui::vec2(80.0, 32.0));
                if ui.add(b).on_hover_text(m.tip()).clicked() {
                    picked = Some(m);
                }
            }
        });
        if let Some(m) = picked {
            match autocombat::write_mode(&bin, m) {
                Ok(()) => self.note(format!("自动战斗：{}", m.label())),
                Err(e) => self.note(format!("auto_mode.txt 写不了：{e}")),
            }
            self.refresh();
        }
        ui.add(
            egui::Label::new(
                egui::RichText::new(autocombat::status_text(self.mode, &self.auto_status))
                    .monospace(),
            )
            .wrap_mode(egui::TextWrapMode::Extend),
        );
    }
}

fn lines_ui(ui: &mut egui::Ui, id: &str, lines: &[String], empty: &str) {
    egui::ScrollArea::both()
        .id_salt(id)
        .max_height(220.0)
        .stick_to_bottom(true)
        .show(ui, |ui| {
            if lines.is_empty() {
                ui.weak(empty);
            }
            for l in lines {
                ui.add(
                    egui::Label::new(egui::RichText::new(l).monospace().size(12.0))
                        .wrap_mode(egui::TextWrapMode::Extend),
                );
            }
        });
}

/// `HH:MM:SS` local time without a date crate: good enough for the launcher's own log.
fn chrono_like_now() -> String {
    #[cfg(windows)]
    {
        #[repr(C)]
        struct SystemTimeW {
            year: u16,
            month: u16,
            dow: u16,
            day: u16,
            hour: u16,
            minute: u16,
            second: u16,
            ms: u16,
        }
        unsafe extern "system" {
            fn GetLocalTime(t: *mut SystemTimeW);
        }
        let mut t = SystemTimeW {
            year: 0,
            month: 0,
            dow: 0,
            day: 0,
            hour: 0,
            minute: 0,
            second: 0,
            ms: 0,
        };
        // SAFETY: fills the struct it is given.
        unsafe { GetLocalTime(&mut t) };
        format!("{:02}:{:02}:{:02}", t.hour, t.minute, t.second)
    }
    #[cfg(not(windows))]
    {
        String::new()
    }
}

impl eframe::App for App {
    fn ui(&mut self, root: &mut egui::Ui, _frame: &mut eframe::Frame) {
        let ctx = root.ctx().clone();
        if let Some(at) = self.pending_start
            && Instant::now() >= at
        {
            self.pending_start = None;
            self.start_server();
        }
        if self.last_refresh.is_none_or(|t| t.elapsed() >= REFRESH) {
            self.refresh();
        }
        ctx.request_repaint_after(Duration::from_millis(500));

        egui::Panel::bottom("notes")
            .resizable(true)
            .default_size(110.0)
            .show(root, |ui| {
                lines_ui(ui, "notes", &self.notes, "（启动器自己的记录）");
            });

        egui::CentralPanel::default().show(root, |ui| {
            egui::ScrollArea::vertical().show(ui, |ui| {
                ui.horizontal(|ui| {
                    ui.heading("Tidelab 启动器");
                    ui.with_layout(egui::Layout::right_to_left(egui::Align::Center), |ui| {
                        if ui.checkbox(&mut self.on_top, "置顶").changed() {
                            let level = if self.on_top {
                                egui::WindowLevel::AlwaysOnTop
                            } else {
                                egui::WindowLevel::Normal
                            };
                            ctx.send_viewport_cmd(egui::ViewportCommand::WindowLevel(level));
                        }
                    });
                });
                ui.separator();
                ui.strong("服务端");
                self.server_ui(ui);
                ui.separator();
                ui.strong("游戏");
                self.game_ui(ui);
                ui.separator();
                ui.strong("补丁开关");
                self.switches_ui(ui);
                ui.separator();
                ui.strong("自动战斗");
                self.auto_ui(ui);
                ui.separator();
                egui::CollapsingHeader::new("服务器日志")
                    .default_open(false)
                    .show(ui, |ui| {
                        lines_ui(ui, "server_log", &self.server_tail, "没有日志");
                    });
                egui::CollapsingHeader::new("CPU 降载状态（tidelab_ace.status）")
                    .default_open(false)
                    .show(ui, |ui| {
                        lines_ui(ui, "ace_status", &self.ace_status, "没有状态文件");
                    });
            });
        });

        if let Some(c) = &self.confirm {
            let text = match c {
                Confirm::Stop => format!("停止 {}？", servers::LABEL),
                Confirm::Restart => format!("重启 {}？", servers::LABEL),
            };
            let mut answer = None;
            egui::Window::new("确认")
                .collapsible(false)
                .resizable(false)
                .anchor(egui::Align2::CENTER_CENTER, [0.0, 0.0])
                .show(&ctx, |ui| {
                    ui.label(text);
                    ui.colored_label(ui.visuals().warn_fg_color, "游戏正在运行，这会把你踢下线。");
                    ui.horizontal(|ui| {
                        if ui.button("确定").clicked() {
                            answer = Some(true);
                        }
                        if ui.button("取消").clicked() {
                            answer = Some(false);
                        }
                    });
                });
            if let Some(yes) = answer {
                let c = self.confirm.take().unwrap();
                if yes {
                    self.do_confirmed(c);
                }
            }
        }
    }
}
