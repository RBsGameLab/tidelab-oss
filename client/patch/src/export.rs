// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! R-174d: the client's own files out of its paks, without any key and without a C# patch.
//!
//! The C# assemblies, the tables and the behaviour trees our tools need are inside the client's
//! encrypted paks, and our C# patch pak is built from those same assemblies, so the first export
//! cannot come from the C# patch. The client's C# runtime is Mono (`coreclr.dll` beside the exe,
//! which exports the Mono embedding API). Started with `-RbExport=<folder>`, this thread waits
//! until the runtime has loaded `Aki.Launcher` (the game's managed code is running, so the engine
//! bindings are registered), loads our own assembly `tidelab_export.dll` (beside this DLL, built
//! from tools/rbexport, references nothing of the game) and calls `RB.Bootstrap.Run(folder)`, which
//! copies the files through the engine's file layer by reflection. Then the client is ended.
//! Lower CPU load.
use std::ffi::{CString, c_char, c_void};
use std::time::{Duration, Instant};

use windows_sys::Win32::System::Environment::GetCommandLineW;
use windows_sys::Win32::System::LibraryLoader::{GetModuleHandleW, GetProcAddress};
use windows_sys::Win32::System::Threading::{GetCurrentProcess, TerminateProcess};

use crate::beside;

pub const ASSEMBLY: &str = "tidelab_export.dll";
const ARG: &str = "-RbExport=";
const WAIT: Duration = Duration::from_secs(300);

/// The value of `-RbExport=` on the client's command line (up to the next space, quotes off).
pub fn folder_from(command_line: &str) -> Option<String> {
    for arg in command_line.split(' ') {
        let a = arg.trim_matches('"');
        if a.len() > ARG.len() && a[..ARG.len()].eq_ignore_ascii_case(ARG) {
            return Some(a[ARG.len()..].to_string());
        }
    }
    None
}

/// Starts the export thread when the client was started with `-RbExport=`.
pub fn start(note: fn(&str)) {
    if !beside::in_client() {
        return;
    }
    // SAFETY: the process's own command line, NUL-terminated and alive for the process.
    let line = unsafe {
        let p = GetCommandLineW();
        let mut n = 0;
        while *p.add(n) != 0 {
            n += 1;
        }
        String::from_utf16_lossy(std::slice::from_raw_parts(p, n))
    };
    let Some(folder) = folder_from(&line) else { return };
    note(&format!("export: armed, folder {folder}"));
    std::thread::spawn(move || {
        let result = run(&folder, note);
        note(&format!("export: {}", result.unwrap_or_else(|e| e)));
        // SAFETY: ends this process; the export run has nothing else to do.
        unsafe { TerminateProcess(GetCurrentProcess(), 0) };
    });
}

type Fn0 = unsafe extern "C" fn() -> *mut c_void;
type Fn1 = unsafe extern "C" fn(*mut c_void) -> *mut c_void;
type FnStr = unsafe extern "C" fn(*const c_char) -> *mut c_void;
type FnPtrStr = unsafe extern "C" fn(*mut c_void, *const c_char) -> *mut c_void;
type FnClass = unsafe extern "C" fn(*mut c_void, *const c_char, *const c_char) -> *mut c_void;
type FnMethod = unsafe extern "C" fn(*mut c_void, *const c_char, i32) -> *mut c_void;
type FnInvoke =
    unsafe extern "C" fn(*mut c_void, *mut c_void, *mut *mut c_void, *mut *mut c_void) -> *mut c_void;

fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0)).collect()
}

fn c(s: &str) -> CString {
    CString::new(s).unwrap_or_default()
}

/// A Mono export of `coreclr.dll`, as a function pointer of type `T`.
unsafe fn sym<T: Copy>(module: *mut c_void, name: &str) -> Result<T, String> {
    let n = c(name);
    // SAFETY: valid module handle and NUL-terminated name.
    let p = unsafe { GetProcAddress(module, n.as_ptr().cast()) };
    match p {
        // SAFETY: T is a function pointer type of the export's signature (the Mono API).
        Some(f) => Ok(unsafe { std::mem::transmute_copy::<_, T>(&f) }),
        None => Err(format!("coreclr.dll has no {name}")),
    }
}

fn wait_for<T>(what: &str, mut f: impl FnMut() -> Option<T>) -> Result<T, String> {
    let start = Instant::now();
    loop {
        if let Some(v) = f() {
            return Ok(v);
        }
        if start.elapsed() > WAIT {
            return Err(format!("gave up waiting for {what}"));
        }
        std::thread::sleep(Duration::from_millis(200));
    }
}

fn run(folder: &str, note: fn(&str)) -> Result<String, String> {
    let name = wide("coreclr.dll");
    // SAFETY: plain module lookup.
    let module = wait_for("coreclr.dll", || {
        let h = unsafe { GetModuleHandleW(name.as_ptr()) };
        (!h.is_null()).then_some(h)
    })?;
    // SAFETY: the Mono embedding API's signatures.
    let (root, attach, name_new, loaded, open, image_of, class_of, method_of, string_new, invoke) = unsafe {
        (
            sym::<Fn0>(module, "mono_get_root_domain")?,
            sym::<Fn1>(module, "mono_thread_attach")?,
            sym::<FnStr>(module, "mono_assembly_name_new")?,
            sym::<Fn1>(module, "mono_assembly_loaded")?,
            sym::<FnPtrStr>(module, "mono_domain_assembly_open")?,
            sym::<Fn1>(module, "mono_assembly_get_image")?,
            sym::<FnClass>(module, "mono_class_from_name")?,
            sym::<FnMethod>(module, "mono_class_get_method_from_name")?,
            sym::<FnPtrStr>(module, "mono_string_new")?,
            sym::<FnInvoke>(module, "mono_runtime_invoke")?,
        )
    };
    // SAFETY: Mono calls with the arguments the API documents.
    let domain = wait_for("the Mono root domain", || {
        let d = unsafe { root() };
        (!d.is_null()).then_some(d)
    })?;
    unsafe { attach(domain) };
    let launcher = c("Aki.Launcher");
    let aname = unsafe { name_new(launcher.as_ptr()) };
    wait_for("Aki.Launcher to load", || {
        let a = unsafe { loaded(aname) };
        (!a.is_null()).then_some(a)
    })?;
    note("export: Aki.Launcher loaded; loading tidelab_export.dll");
    let path = beside::path(ASSEMBLY).ok_or("no patch directory")?;
    let path = c(&String::from_utf16_lossy(&path[..path.len() - 1]));
    let assembly = unsafe { open(domain, path.as_ptr()) };
    if assembly.is_null() {
        return Err(format!("could not load {ASSEMBLY}"));
    }
    let (ns, cls, meth) = (c("RB"), c("Bootstrap"), c("Run"));
    let class = unsafe { class_of(image_of(assembly), ns.as_ptr(), cls.as_ptr()) };
    if class.is_null() {
        return Err("no class RB.Bootstrap".into());
    }
    let method = unsafe { method_of(class, meth.as_ptr(), 1) };
    if method.is_null() {
        return Err("no method RB.Bootstrap.Run(string)".into());
    }
    let arg = c(folder);
    let mut args = [unsafe { string_new(domain, arg.as_ptr()) }];
    let mut exc: *mut c_void = std::ptr::null_mut();
    unsafe { invoke(method, std::ptr::null_mut(), args.as_mut_ptr(), &mut exc) };
    if exc.is_null() {
        Ok("RB.Bootstrap.Run returned".into())
    } else {
        Err("RB.Bootstrap.Run threw (see rb_export.txt)".into())
    }
}

#[cfg(test)]
mod tests {
    use super::folder_from;

    #[test]
    fn the_folder_comes_from_the_command_line() {
        assert_eq!(folder_from("x.exe -krqlv=SD -RbExport=C:\\out"), Some("C:\\out".into()));
        assert_eq!(folder_from("x.exe \"-rbexport=D:\\a\""), Some("D:\\a".into()));
        assert_eq!(folder_from("x.exe -RbExport= -RbAccount=1"), None);
        assert_eq!(folder_from("x.exe -RbAccount=3838"), None);
    }
}
