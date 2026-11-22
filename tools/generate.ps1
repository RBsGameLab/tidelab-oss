# Generates the Go protocol package (server/protocol) and data-table row types
# (server/internal/data/tables_gen.go) from the user's own extracted 3.8 client files,
# and optionally exports panel display names (<BinData>/../panel/*.tsv).
#
#   generate.cmd [-ProtocolDll PATH] [-Schema PATH] [-ConfigDb DIR] [-BinData DIR] [-LangDb PATH] [-PanelOut DIR] [-ForceExport] [-SkipPanelNames]
param(
    [string]$ProtocolDll = '',
    [string]$Schema = '',
    [string]$ConfigDb = '',
    [string]$BinData = '',
    [string]$LangDb = '',
    [string]$PanelOut = '',
    [switch]$ForceExport,
    [switch]$SkipPanelNames
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$beside = Split-Path $repo -Parent

function Say($text, $color = 'Gray') { Write-Host $text -ForegroundColor $color }
function Find-Tool($name) {
    $c = Get-Command $name -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    return $null
}

# Read tidelab.local.txt (same keys as tools\paths.cmd and tools\tl_paths.py).
$local = @{}
$localFile = Join-Path $repo 'tidelab.local.txt'
if (Test-Path $localFile) {
    foreach ($line in Get-Content $localFile) {
        $t = $line.Trim()
        if (-not $t -or $t.StartsWith('#') -or -not $t.Contains('=')) { continue }
        $k, $v = $t.Split('=', 2)
        $local[$k.Trim().ToLower()] = $v.Trim()
    }
}

$lab = if ($local['lab']) { $local['lab'] } elseif ($env:TIDELAB_LAB) { $env:TIDELAB_LAB } else { Join-Path $beside 'tidelab-data' }
$data = if ($local['gamedata']) { $local['gamedata'] } elseif ($env:TIDELAB_GAMEDATA) { $env:TIDELAB_GAMEDATA } else { Join-Path $lab 'gamedata\3.8' }

if (-not $ProtocolDll) {
    $candidates = @(
        (Join-Path $lab 'backups\csref38\Aki.Protocol.dll'),
        (Join-Path $data 'Aki.Protocol.dll')
    )
    $ProtocolDll = ($candidates | Where-Object { Test-Path $_ } | Select-Object -First 1)
    if (-not $ProtocolDll) { $ProtocolDll = $candidates[0] }
}
if (-not $Schema) {
    $candidates = @(
        (Join-Path $data 'Define'),
        (Join-Path $lab 'backups\csref38\Aki.Generated.dll'),
        (Join-Path $data 'Aki.Generated.dll')
    )
    $Schema = ($candidates | Where-Object { Test-Path $_ } | Select-Object -First 1)
    if (-not $Schema) { $Schema = $candidates[0] }
}
if (-not $ConfigDb) { $ConfigDb = Join-Path $data 'ConfigDB' }
if (-not $BinData) { $BinData = Join-Path $data 'BinData' }
if (-not $LangDb) {
    $candidates = @(
        (Join-Path $lab 'reference\lang38\Client\Content\Aki\ConfigDB\zh-Hans\lang_multi_text.db'),
        (Join-Path $data 'lang_multi_text.db')
    )
    $LangDb = ($candidates | Where-Object { Test-Path $_ } | Select-Object -First 1)
    if (-not $LangDb) { $LangDb = $candidates[0] }
}
if (-not $PanelOut) { $PanelOut = Join-Path (Split-Path $BinData -Parent) 'panel' }

$dotnet = Find-Tool 'dotnet'
$go = if ($local['go'] -and (Test-Path $local['go'])) { $local['go'] } else { Find-Tool 'go' }
if (-not $go) {
    $sdk = Join-Path $env:LOCALAPPDATA 'Programs\go-sdk'
    if (Test-Path $sdk) {
        $g = Get-ChildItem $sdk -Directory | Sort-Object Name -Descending | Where-Object { Test-Path (Join-Path $_.FullName 'go\bin\go.exe') } | Select-Object -First 1
        if ($g) { $go = Join-Path $g.FullName 'go\bin\go.exe' }
    }
}
$python = Find-Tool 'python'

if (-not $dotnet) { throw 'dotnet SDK not found on PATH (.NET 10 SDK required).' }
if (-not $go) { throw 'go not found: put go.exe on PATH or set go=<path> in tidelab.local.txt.' }
if (-not (Test-Path $ProtocolDll)) { throw "Aki.Protocol.dll not found at $ProtocolDll (pass -ProtocolDll or set lab= in tidelab.local.txt)." }
if (-not (Test-Path $Schema)) { throw "Config schema (Define dir or Aki.Generated.dll) not found at $Schema (pass -Schema or set lab= in tidelab.local.txt)." }

$protoOut = Join-Path $repo 'server\protocol'
$tablesOut = Join-Path $repo 'server\internal\data\tables_gen.go'
$tsvPath = Join-Path $repo 'specs\data-tables.tsv'

Say '1/4 ProtoGen (Aki.Protocol.dll -> server/protocol/proto + schema.tsv)' 'Cyan'
& $dotnet run --project (Join-Path $repo 'tools\Tidelab.ProtoGen') -c Release -- $ProtocolDll $protoOut
if ($LASTEXITCODE -ne 0) { throw "Tidelab.ProtoGen failed (exit $LASTEXITCODE)" }

Say '2/4 genproto (proto/*.proto + schema.tsv -> *.pb.go + msgid.go)' 'Cyan'
Push-Location (Join-Path $repo 'server')
try {
    & $go run ./tools/genproto
    if ($LASTEXITCODE -ne 0) { throw "genproto failed (exit $LASTEXITCODE). Ensure protoc-gen-go is installed: go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12" }
} finally {
    Pop-Location
}

if ($ForceExport -or -not (Test-Path (Join-Path $BinData 'RoleInfo.json'))) {
    if (Test-Path $ConfigDb) {
        Say "3a/4 ConfigExport export ($ConfigDb -> $BinData)" 'Cyan'
        & $dotnet run --project (Join-Path $repo 'tools\Tidelab.ConfigExport') -c Release -- export $Schema $ConfigDb $BinData
        if ($LASTEXITCODE -ne 0) { throw "ConfigExport export failed (exit $LASTEXITCODE)" }
    } else {
        throw "BinData not found at $BinData and ConfigDB not found at $ConfigDb (pass -BinData or -ConfigDb)."
    }
}

Say '3/4 ConfigExport gen (tables_gen.go)' 'Cyan'
& $dotnet run --project (Join-Path $repo 'tools\Tidelab.ConfigExport') -c Release -- gen $Schema $BinData $tsvPath $tablesOut
if ($LASTEXITCODE -ne 0) { throw "ConfigExport gen failed (exit $LASTEXITCODE)" }

if (-not $SkipPanelNames) {
    if ($python -and (Test-Path $LangDb)) {
        Say "4/4 Panel display names (-> $PanelOut)" 'Cyan'
        & $python -I (Join-Path $repo 'tools\export_panel_names.py') --bindata $BinData --lang $LangDb --out $PanelOut
        if ($LASTEXITCODE -ne 0) { throw "export_panel_names.py failed (exit $LASTEXITCODE)" }
    } else {
        Say "4/4 Panel display names skipped (optional: lang_multi_text.db not found at $LangDb or python missing)." 'Yellow'
    }
}

Say 'Done. Next: cd server && go test ./...' 'Green'
