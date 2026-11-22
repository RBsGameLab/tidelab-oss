// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

//! R-147c: a file mapped into memory for the rest of the process, read-only. The launcher's
//! Chinese font (msyh.ttc, 19 MB) read with `std::fs::read` was 19 MB of the launcher's private
//! memory; mapped, its pages belong to the file and only the ones read are in memory at all.

/// The whole file, mapped read-only and never unmapped; None when it cannot be opened or is empty.
#[cfg(windows)]
pub fn map_for_good(path: &std::path::Path) -> Option<&'static [u8]> {
    use std::os::windows::io::AsRawHandle;
    use windows_sys::Win32::Foundation::CloseHandle;
    use windows_sys::Win32::System::Memory::{
        CreateFileMappingW, FILE_MAP_READ, MapViewOfFile, PAGE_READONLY,
    };
    let file = std::fs::File::open(path).ok()?;
    let len = usize::try_from(file.metadata().ok()?.len()).ok()?;
    if len == 0 {
        return None;
    }
    // SAFETY: the handle is the open file's; the mapping keeps the file's data reachable after
    // both handles close, and the view is never unmapped, so the slice lives as long as the
    // process. Nothing writes to it: the mapping is read-only.
    unsafe {
        let mapping = CreateFileMappingW(
            file.as_raw_handle(),
            std::ptr::null(),
            PAGE_READONLY,
            0,
            0,
            std::ptr::null(),
        );
        if mapping.is_null() {
            return None;
        }
        let view = MapViewOfFile(mapping, FILE_MAP_READ, 0, 0, 0);
        CloseHandle(mapping);
        if view.Value.is_null() {
            return None;
        }
        Some(std::slice::from_raw_parts(view.Value as *const u8, len))
    }
}

/// Elsewhere: the file read into memory that is never freed.
#[cfg(not(windows))]
pub fn map_for_good(path: &std::path::Path) -> Option<&'static [u8]> {
    let bytes = std::fs::read(path).ok().filter(|b| !b.is_empty())?;
    Some(Vec::leak(bytes))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_mapped_file_reads_as_its_bytes() {
        let dir = std::env::temp_dir().join(format!("tidelab-mapped-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("data.bin");
        let bytes: Vec<u8> = (0..70_000u32).map(|i| (i * 7 % 251) as u8).collect();
        std::fs::write(&path, &bytes).unwrap();
        let mapped = map_for_good(&path).expect("mapped");
        assert_eq!(mapped, &bytes[..]);
        std::fs::write(dir.join("empty.bin"), b"").unwrap();
        assert!(map_for_good(&dir.join("empty.bin")).is_none());
        assert!(map_for_good(&dir.join("missing.bin")).is_none());
    }
}
