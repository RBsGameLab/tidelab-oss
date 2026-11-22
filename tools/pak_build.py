# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""Build an uncompressed, unencrypted patch pak in the format the 3.7 client reads (pak version
12: UE4 v11 layout with Kuro's encoded entries [flags u32][u8][uncompressed size u32][offset u32]).

    python -I scripts/dev/pak_build.py <out.pak> <root dir>
        packs every file under <root dir>; pak paths are relative to it, e.g.
        <root>/Client/Content/Aki/JavaScript/Game/Foo.js -> Client/Content/Aki/JavaScript/Game/Foo.js
"""
import hashlib
import os
import struct
import sys

MAGIC = 0x5A6F12E1
VERSION = 12
MOUNT = "../../../"
FNV_OFFSET = 0xCBF29CE484222325
FNV_PRIME = 0x100000001B3


def fstring(s: str) -> bytes:
    b = s.encode("ascii") + b"\0"
    return struct.pack("<i", len(b)) + b


def fnv64_path(path: str, seed: int = 0) -> int:
    h = (FNV_OFFSET + seed) & 0xFFFFFFFFFFFFFFFF
    for byte in path.lower().encode("utf-16-le"):
        h = ((h ^ byte) * FNV_PRIME) & 0xFFFFFFFFFFFFFFFF
    return h


ENTRY_HEADER = 53  # the in-data entry header written below


def build(files, data_order=None, align_dll=None):
    """files: list of (pak path 'Client/...', bytes), any order. The index lists files sorted by
    path; the data area follows `data_order` (paths) if given, else the same sorted order (the
    reader does not care). Returns the pak bytes.

    align_dll: R-55. The client maps script assemblies (.dll) straight out of the pak; shipped
    ones start with zero bytes so that their "MZ" lands 64-byte aligned in the pak file, and an
    unaligned one fails to load ("mono_image_open_from_data need copy", "Create image failed").
    With align_dll=N each .dll's "MZ" is placed at a multiple of N (gaps are zero padding), and
    its entry is marked like the shipped ones (pakchunk1/18, read with `pakx entries`): flag
    byte 0x10 in the in-data header and CustomData 4 in the index entry. Without the marks the
    client copies the data instead of mapping it and the image still fails."""
    files = sorted(files, key=lambda f: f[0])
    data = bytearray()
    offsets = {}
    header_flags = {}
    custom = {}
    for path in (data_order or [f[0] for f in files]):
        content = dict(files)[path]
        header_flags[path], custom[path] = 0, 0
        if align_dll and path.lower().endswith(".dll"):
            mz = content.find(b"MZ")
            data += b"\0" * (-(len(data) + ENTRY_HEADER + mz) % align_dll)
            header_flags[path], custom[path] = 0x10, 4
        offsets[path] = len(data)
        # In-data entry header: offset 0, size, uncompressed size, method, sha1, flags, block size.
        data += struct.pack("<qqqI", 0, len(content), len(content), 0) + hashlib.sha1(content).digest() + bytes([header_flags[path]]) + struct.pack("<I", 0)
        data += content
    encoded = bytearray()
    enc_offsets = []
    for path, content in files:
        enc_offsets.append(len(encoded))
        # Kuro's encoded entry: bitfield u32, CustomData u8, size u32, offset u32.
        encoded += struct.pack("<IBII", 0xE0000000, custom[path], len(content), offsets[path])

    # Path hash index: count, (fnv64, encoded offset)*, then a zero (no pruned directories).
    phi = struct.pack("<i", len(files))
    for (path, _), eo in zip(files, enc_offsets):
        phi += struct.pack("<Qi", fnv64_path(path), eo)
    phi += struct.pack("<i", 0)

    # Full directory index: every directory (with all its parents), each with its files.
    dirs = {"/": []}
    for (path, _), eo in zip(files, enc_offsets):
        parts = path.split("/")
        for i in range(1, len(parts)):
            dirs.setdefault("/".join(parts[:i]) + "/", [])
        dirs["/".join(parts[:-1]) + "/"].append((parts[-1], eo))
    fdi = struct.pack("<i", len(dirs))
    for d in sorted(dirs):
        fdi += fstring(d) + struct.pack("<i", len(dirs[d]))
        for name, eo in dirs[d]:
            fdi += fstring(name) + struct.pack("<i", eo)

    index_offset = len(data)
    phi_offset = index_offset  # filled below once the primary index size is known
    def primary(phi_off, fdi_off):
        return (fstring(MOUNT) + struct.pack("<iQ", len(files), 0)
                + struct.pack("<iqq", 1, phi_off, len(phi)) + hashlib.sha1(phi).digest()
                + struct.pack("<iqq", 1, fdi_off, len(fdi)) + hashlib.sha1(fdi).digest()
                + struct.pack("<i", len(encoded)) + bytes(encoded) + struct.pack("<i", 0))
    size = len(primary(0, 0))
    phi_offset = index_offset + size
    fdi_offset = phi_offset + len(phi)
    idx = primary(phi_offset, fdi_offset)
    footer = (b"\0" * 16 + b"\0" + struct.pack("<IIqq", MAGIC, VERSION, index_offset, len(idx))
              + hashlib.sha1(idx).digest() + b"\0" * 160)
    return bytes(data) + idx + phi + fdi + footer


def collect(root):
    out = []
    for dirpath, _, names in os.walk(root):
        for n in names:
            full = os.path.join(dirpath, n)
            rel = os.path.relpath(full, root).replace(os.sep, "/")
            out.append((rel, open(full, "rb").read()))
    return out


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    files = collect(sys.argv[2])
    pak = build(files, align_dll=4096)
    open(sys.argv[1], "wb").write(pak)
    print(f"wrote {sys.argv[1]}: {len(files)} files, {len(pak)} bytes")
    for path, content in files:
        if path.lower().endswith(".dll"):
            at = pak.find(content[:4096]) + content.find(b"MZ")
            print(f"  {path}: MZ at {at} ({'aligned' if at % 4096 == 0 else 'NOT aligned'})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
