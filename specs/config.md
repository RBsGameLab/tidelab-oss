# Config and CDN server (HTTP)

HTTP server serving the client's encrypted config JSON and raw CDN manifests from `state/cfg/`.

## Listening

- Plain HTTP/1.1 on TCP, default `127.0.0.1:5500` (`--listen_address`); `--state_dir`
  (default `state`).
- Startup log line: `config server is listening at <addr>`.
- One request per connection; only the request line is read (as `login.md`). Log each one as
  `request: <path>` (scripts grep for it).
- Not `GET`: 405, no body.
- Refuse (close without an answer) an empty path, a path starting with `//`, or one containing
  `../`.

## Serving a path

The path without its leading `/` is looked up under `state/cfg/index/`:

1. If `<path>.raw` exists, answer it unchanged: 200, `application/octet-stream`. These are
   official CDN bytes whose hashes the client checks (manifests, `PakData`, video manifests).
2. Else if `<path>` exists, answer it encrypted: 200, `application/json`, body =
   Base64 (standard alphabet, with padding) of AES-256-CBC(content), key and IV below,
   PKCS#7 padding (a whole block of `0x10` when the length is already a multiple of 16).
3. Else 404, no body, log `requested file not found: <path>`.

Key and IV: `state/cfg/key` (Base64 of 32 bytes) and `state/cfg/iv` (Base64 of 16 bytes). A
file of the wrong length is an error (no answer).

**Without key and IV** (R-173c; neither file exists): step 2 answers the file unchanged (200,
`application/json`). The stock launcher cannot read that: every config read goes through the
native `UKuroLauncherLibrary.Decrypt(cipher, ref plain)` with the client's built-in key. Our C#
patch (type `RB.RBCfg` in tools/rbauto) points every such call in Aki.Launcher at
`RBCfg.Decrypt`: text that opens a JSON object (`{` then `"` or `}`) or array (`[` then a JSON
value or `]`) is returned as is, anything else goes to the native Decrypt (a bare `[` is not
enough: `Config/Kuro/RConfig.ini` in the client's paks starts `[Section]`). Only one of the two
files present is still an error.

**Pak keys** (R-173d). `pakchunk101`–`114` and the `Video_*` paks need keys the launcher
downloads at every start (`PakKeyUpdate`): `KeyList_<version>.json` and `KeyList.json` (videos),
then `client_key/<version>/<Random>/PakData`, from each CDN prefix in turn; the newest KeyList
wins and PakData is checked against its hash. A server without these files (a new user) still
starts the client: our C# patch (type `RB.RBKeys`) adds the official CDN prefixes after ours,
taken from the official `index.json` that the client requests and decrypts itself (official
config prefixes and `UrlPath` from `Config/Kuro/RbOfficial.ini`, which tools/build_loginpak.py
copies from the client's `KuroPublicConfig.ini`). The key files go from the official CDN to the
launcher as they always did; nothing is stored or served by tidelab.

A file changed on disk is served changed at the next request (no restart).

## The data

`state/cfg/index/` is data, not code; tidelab serves the same tree. The 3.8 client uses the
folder `zXGBurT1FHY5eBpEhgbCw7n85imvi3XR`; the other folders are 3.7 and older. Requests one
3.8 session made (`logs/server.log`):

```
/<id>/index.json
/<id>/Windows/config.json
/<id>/Windows/config_sd.json
/<id>/Windows/KeyList.json
/<id>/Windows/KeyList_3.8.0.json
/<id>/Windows/VideoConfig_sd.json
/<id>/Windows/3.8.0/ManifestAggregated.txt            (.raw)
/<id>/Windows/3.8.0/ManifestLang_zh.txt               (.raw)
/<id>/Windows/client_key/3.8.0/<pak id>/PakData       (.raw)
```

and, after a change of option packs, the `ManifestOption_Pack_*.txt` manifests (.raw).

Rules for the content (from R-107; keep them when editing the files):

- Every CDN list names only our server (`index.json` `default.CdnUrl` is
  `http://127.0.0.1:5500/`): with the official CDN kept as a weight-0 fallback the 3.8 client
  downloaded 2.2 GB by itself.
- Files whose SHA1 the client checks go out as the official bytes (`.raw`).
- Option-pack manifests keep their 3.8.0 IndexSha1 and are served, or the client mounts no
  terrain and the player falls through every map.
- `index.json` also points the client at the login server for gacha pages and update notes
  (`GachaUrl`, `PackageUpdateDescUrl` on port 10001). Its `LogReport.region` still says
  `dev-reversedrooms`; that is data the client only reports, change it if wanted.

**A new state** (R-173e): `tools/make_cfg.py` (run by `setup.cmd`) writes the files a 3.8 start
asks for, from the user's own client and nothing in the repository: `index.json` with only our
URLs (the CDN list is the config server, login, gacha, notice and update pages the login server);
`Windows/config.json` and `config_<tier>.json` with `PackageVersion` 3.8.0, an empty
`ChangeList` (the launcher then reports the package's own) and `ResVersions` whose `IndexSha1` is
the SHA1 of the client's cached manifest
`Client/Saved/Resources/3.8.0/ResManifest/Manifest<Name>_3.8.0.txt` (the plain file lists
`ManifestAggregated` and the option packs, a tier file its `_<TIER>` manifests under the plain
pack names); `Windows/VideoConfig*.json` with the SHA1 of
`Client/Saved/Resources/Video/<tier>/3.8.0/VideoManifest.json`. The caches exist once the client
started online; without them `make_cfg.py` stops with that message. Checked: for this machine's
client the generated `ResVersions` and video hashes equal the live server's files.

## Tests

- `make_cfg.py`: hashes from the cached manifests, tier files, local URLs only, missing cache,
  existing files kept (`tools/make_cfg_test.py`).
- `.raw` wins over the plain file; a missing file is 404.
- Encryption: known plaintext of 0, 15, 16, 17 bytes against a reference AES-256-CBC with
  PKCS#7 (any library), Base64 out.
- Path refusals.
- Real client: reaches the server list through tidelab's login and config servers.
