# Copyright (C) 2026 RBsGameLab
# SPDX-License-Identifier: AGPL-3.0-only

"""Check server/protocol/schema.tsv against the decompiled client source, field by field.

schema.tsv comes from tools/Tidelab.ProtoGen, which reads the client's Aki.Protocol.dll on the
parse side (ReadXxx calls, codec constructors). This script is a second, independent reading of
the other side: the write code in the decompiled C# (reference/client-src-3.8), where each field
is written as

    output.WriteRawTag((byte)8);            <- tag: field number and wire type
    output.WriteInt32(ConfigId);            <- encoding

and each repeated or map field through a static codec, FieldCodec.ForXxx(<tag>u, ...).

For every field it compares the tag (number and wire type), the encoding and the label. It
also checks that each message's set of field numbers is the same on both sides.

    python -I tools/client_tags.py [<client-src-3.8>] [<schema.tsv>]

Exit 0 when everything agrees. Needs the lab; CI does not run it.
"""

import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # python -I drops the script dir
import tl_paths  # noqa: E402

DEFAULT_SRC = str(tl_paths.CLIENT_SRC)  # R-171
DEFAULT_SCHEMA = os.path.join(os.path.dirname(__file__), "..", "server", "protocol", "schema.tsv")

WIRE = {
    "int32": 0, "int64": 0, "uint32": 0, "uint64": 0, "sint32": 0, "sint64": 0,
    "bool": 0, "enum": 0, "fixed64": 1, "sfixed64": 1, "double": 1,
    "string": 2, "bytes": 2, "message": 2, "fixed32": 5, "sfixed32": 5, "float": 5,
}

CLASS = re.compile(r"^\t*public (?:sealed )?(?:partial )?class (\w+)", re.M)
NAMESPACE = re.compile(r"^namespace ([\w.]+)", re.M)
WRITE_BODY = re.compile(r"^(\t*)void IBufferMessage\.InternalWriteTo\(ref WriteContext output\)\n\1\{(.*?)\n\1\}$", re.S | re.M)
RAW_WRITE = re.compile(r"output\.WriteRawTag\(([^;]*)\);\s*output\.Write(\w+)\(")
COLLECTION_WRITE = re.compile(r"(\w+)_\.WriteTo\(ref output, (_(?:repeated|map)_\w+_codec)\)")
CODEC_FOR = re.compile(r"FieldCodec\.For(\w+?)(?:<[\w.]+>)?\((\d+)u")
CODEC_DECL = re.compile(r"static readonly [^=]*? (_(?:repeated|map)_\w+_codec) = (.*?);\n")
MAP_TAG = re.compile(r",\s*(\d+)u\)$")


def varint(raw):
    """'(byte)200, (byte)1' -> 200 | 1 << 7."""
    value, shift = 0, 0
    for b in re.findall(r"\d+", raw):
        value |= (int(b) & 0x7F) << shift
        shift += 7
    return value


def kind_of(write):
    """WriteSInt32 -> 'sint32'; WriteMessage -> 'message'."""
    return write.lower()


def read_client(src):
    """{message full name: {number: (label, kind, wire, packed, key kind)}}"""
    out = {}
    for d in sorted(os.listdir(src)):
        path = os.path.join(src, d)
        if not (d == "Aki.Protocol" or d.startswith("Aki.Protocol.")) or not os.path.isdir(path):
            continue
        for name in sorted(os.listdir(path)):
            if not name.endswith(".cs"):
                continue
            with open(os.path.join(path, name), encoding="utf-8-sig") as f:
                text = f.read()
            ns = NAMESPACE.search(text)
            if not ns or "InternalWriteTo" not in text:
                continue
            rel = ns.group(1)[len("Aki.Protocol."):] if ns.group(1) != "Aki.Protocol" else ""
            codecs = dict(CODEC_DECL.findall(text))
            for body in WRITE_BODY.finditer(text):
                classes = [m for m in CLASS.finditer(text) if m.start() < body.start()]
                owner = ".".join(m.group(1) for m in classes[-1:])
                full = f"{rel}.{owner}" if rel else owner
                fields = out.setdefault(full, {})
                for raw, write in RAW_WRITE.findall(body.group(2)):
                    tag = varint(raw)
                    fields[tag >> 3] = ("singular", kind_of(write), tag & 7, None, None)
                for _, codec in COLLECTION_WRITE.findall(body.group(2)):
                    decl = codecs.get(codec, "")
                    fors = CODEC_FOR.findall(decl)
                    if codec.startswith("_repeated_") and len(fors) == 1:
                        kind, tag = fors[0][0].lower(), int(fors[0][1])
                        packed = (tag & 7) == 2 and kind not in ("string", "bytes", "message")
                        fields[tag >> 3] = ("repeated", kind, tag & 7, packed, None)
                    elif codec.startswith("_map_") and len(fors) == 2 and MAP_TAG.search(decl):
                        tag = int(MAP_TAG.search(decl).group(1))
                        fields[tag >> 3] = ("map", fors[1][0].lower(), tag & 7, None, fors[0][0].lower())
                    else:
                        fields[-1 - len(fields)] = ("unreadable", codec, -1, None, None)
    return out


def read_schema(path):
    enums, messages = set(), {}
    rows = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            cols = line.rstrip("\n").split("\t")
            if cols[0] == "enum":
                enums.add(cols[1])
            elif cols[0] == "message":
                messages[cols[1]] = {}
            elif cols[0] == "field":
                rows.append(cols)
    for cols in rows:
        msg, number, label, typ = cols[1], int(cols[2]), cols[4], cols[5]
        extra = dict(c.split("=", 1) if "=" in c else (c, True) for c in cols[6:])
        kind = "enum" if typ in enums else "message" if typ in messages else typ
        key = extra.get("key")
        messages[msg][number] = (label, kind, "unpacked" not in extra, key)
    return messages


def expected(label, kind, packed, key):
    """(label as the write code shows it, kind, wire type, packed, key kind)."""
    if label == "map":
        return ("map", kind, 2, None, key)
    if label == "repeated":
        is_packed = packed and kind not in ("string", "bytes", "message")
        return ("repeated", kind, 2 if is_packed else WIRE[kind], is_packed, None)
    return ("singular", kind, WIRE[kind], None, None)


def compare(client, schema):
    problems = []
    for msg in sorted(set(client) | set(schema)):
        if msg not in schema:
            problems.append(f"{msg}: in the client, not in schema.tsv")
            continue
        if msg not in client:
            if schema[msg]:
                problems.append(f"{msg}: has fields in schema.tsv, no write code in the client")
            continue
        c, s = client[msg], schema[msg]
        for number in sorted(set(c) | set(s)):
            if number not in s:
                problems.append(f"{msg} #{number}: client writes {c[number]}, not in schema.tsv")
            elif number not in c:
                problems.append(f"{msg} #{number}: in schema.tsv {s[number]}, client never writes it")
            else:
                label, kind, packed, key = s[number]
                want = expected("singular" if label == "optional" else label, kind, packed, key)
                if c[number] != want:
                    problems.append(f"{msg} #{number}: client {c[number]}, schema.tsv {want}")
    return problems


def main(argv):
    src = argv[1] if len(argv) > 1 else DEFAULT_SRC
    schema_path = argv[2] if len(argv) > 2 else DEFAULT_SCHEMA
    client = read_client(src)
    schema = read_schema(schema_path)
    problems = compare(client, schema)
    fields = sum(len(v) for v in client.values())
    print(f"client: {len(client)} messages with write code, {fields} fields; schema.tsv: {len(schema)} messages")
    for p in problems[:50]:
        print("problem:", p)
    if len(problems) > 50:
        print(f"... {len(problems) - 50} more")
    print("agree" if not problems else f"{len(problems)} differences")
    return 0 if not problems else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
