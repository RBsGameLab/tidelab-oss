# R-171: prepares a new machine for tidelab. Run setup.cmd at the checkout's root (it calls this).
# Nothing is written in: it finds or asks for the lab, the 3.8 client and the state directory,
# writes tidelab.local.txt (read by tools\paths.cmd, tools\tl_paths.py and the launcher), checks
# the tools, makes a new state directory's config files from the client's cache (tools\make_cfg.py),
# makes the game data from the client when there is none (tools\setup_data.py, R-174) and builds what is missing.
# Safe to run again: it only adds what is missing and asks before replacing tidelab.local.txt.
# -Rebuild makes the game data again from the client.
param([switch]$NoBuild, [switch]$Rebuild)

$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$beside = Split-Path $repo -Parent
$problems = New-Object System.Collections.Generic.List[string]

function Say($text, $color = 'Gray') { Write-Host $text -ForegroundColor $color }
function Ask-Folder($what, $default, $mustHave) {
    $p = $default
    while ($true) {
        if ($p -and (Test-Path (Join-Path $p $mustHave))) { return (Resolve-Path $p).Path }
        if ($p) { Say "  $what not found at $p (no $mustHave there)." 'Yellow' }
        $p = Read-Host "  Folder of the $what (Enter to skip)"
        if (-not $p) { return $null }
        $p = $p.Trim('"')
    }
}
function Find-Tool($name) {
    $c = Get-Command $name -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    return $null
}

Say "tidelab setup: checkout $repo" 'Cyan'

# Existing local file: keep its values as the starting point.
$localFile = Join-Path $repo 'tidelab.local.txt'
$local = @{}
if (Test-Path $localFile) {
    foreach ($line in Get-Content $localFile) {
        $t = $line.Trim()
        if (-not $t -or $t.StartsWith('#') -or -not $t.Contains('=')) { continue }
        $k, $v = $t.Split('=', 2)
        $local[$k.Trim().ToLower()] = $v.Trim()
    }
}

# 1. Game data workspace: exported tables (gamedata\3.8), C# references (backups\csref38).
Say "`n1. Data directory (game data)" 'Cyan'
$labDefault = if ($local['lab']) { $local['lab'] } else { Join-Path $beside 'tidelab-data' }
# R-174: no data yet -> step 6 makes it from the client into $labDefault (or the folder typed).
$lab = $null
if (Test-Path (Join-Path $labDefault 'gamedata\3.8\BinData')) { $lab = (Resolve-Path $labDefault).Path }
else {
    $typed = Read-Host "  No game data at $labDefault. Folder with existing data (Enter: make it from your client there)"
    $lab = if ($typed) { $typed.Trim('"') } else { $labDefault }
}
# Data made by setup (it keeps export\rb_export.txt) is checked again on every run: setup_data
# skips what is done and finishes what a broken run left.
$needData = $Rebuild -or -not (Test-Path (Join-Path $lab 'gamedata\3.8\BinData')) -or (Test-Path (Join-Path $lab 'export\rb_export.txt'))
if ($needData) { Say "  lab: $lab (game data made in step 6)" 'Green' } else { Say "  lab: $lab" 'Green' }

# 2. The 3.8 client.
Say "`n2. 3.8 client" 'Cyan'
$gameDefault = if ($local['game']) { $local['game'] } elseif ($lab) { Join-Path $lab 'Game38' } else { $null }
$game = Ask-Folder '3.8 client (the folder holding Client)' $gameDefault 'Client\Binaries\Win64\Client-Win64-Shipping.exe'
if ($game) {
    Say "  client: $game" 'Green'
    Say '  The patch fits 3.8.0 build 9244589 only (start screen, top right). A newer download will not start.' 'Yellow'
} else { $problems.Add('No 3.8 client: copy Game38 from the old machine (the official launcher installs the newest version).') }

# 3. The state directory. R-173: no key is needed any more. Without gateway\client_public_key.der
# the game sessions run unencrypted, without cfg\key and cfg\iv the config files go out in the
# clear (both need our C# patch pak); the pak keys come from the official CDN (RB.RBKeys).
# A new state gets its config files from the user's own client (tools\make_cfg.py, after step 4).
Say "`n3. Save data" 'Cyan'
$state = if ($local['state']) { $local['state'] } else { Join-Path $beside 'tidelab-state' }
$cfgIndex = Join-Path $state 'cfg\index'
$needCfg = -not (Test-Path $cfgIndex)
if ($needCfg) { Say "  New save data at $state (the first player is 666); config files made after the tools check." 'Green' }
else { Say "  state: $state" 'Green' }
if (Test-Path (Join-Path $state 'gateway\client_public_key.der')) { Say '  Gateway key present: game sessions encrypted.' 'Gray' }

# 4. Tools.
Say "`n4. Tools" 'Cyan'
$go = if ($local['go'] -and (Test-Path $local['go'])) { $local['go'] } else { Find-Tool 'go' }
if (-not $go) {
    $sdk = Join-Path $env:LOCALAPPDATA 'Programs\go-sdk'
    if (Test-Path $sdk) {
        $g = Get-ChildItem $sdk -Directory | Sort-Object Name -Descending | Where-Object { Test-Path (Join-Path $_.FullName 'go\bin\go.exe') } | Select-Object -First 1
        if ($g) { $go = Join-Path $g.FullName 'go\bin\go.exe' }
    }
}
$cargo = Find-Tool 'cargo'
$python = Find-Tool 'python'
$dotnet = Find-Tool 'dotnet'
foreach ($t in @(@('Go (servers)', $go, 'Go 1.27.4 or newer: https://go.dev/dl/'),
                 @('Rust cargo (launcher, patch DLL)', $cargo, 'https://rustup.rs'),
                 @('Python (pak builds, tools)', $python, 'https://www.python.org'),
                 @('.NET SDK (C# patch pak)', $dotnet, 'https://dotnet.microsoft.com/download'))) {
    if ($t[1]) { Say ("  {0}: {1}" -f $t[0], $t[1]) 'Green' } else { Say ("  {0}: missing ({1})" -f $t[0], $t[2]) 'Yellow'; $problems.Add("Install $($t[0]): $($t[2])") }
}
# R-174c: versions the build needs (server/go.mod go 1.27, tools/global.json SDK 10.0, Python 3.11
# for the tools' syntax). A tool that is too old, or Windows' "python" placeholder that only opens
# the Store, is dropped here so a later step does not fail half way.
function Test-Version($name, $have, $need, $link) {
    if ($have -and $have -ge $need) { return $true }
    $shown = if ($have) { "$have" } else { 'not runnable' }
    Say "  ${name}: version $shown, need $need or newer ($link)" 'Yellow'
    $problems.Add("Update ${name} to $need or newer: $link")
    return $false
}
if ($go) {
    $v = (& $go version 2>$null) -replace '.*go(\d+\.\d+(\.\d+)?).*', '$1'
    if (-not (Test-Version 'Go' ($v -as [version]) ([version]'1.27') 'https://go.dev/dl/')) { $go = $null }
}
if ($python) {
    $v = & $python -c "import sys; print('%d.%d' % sys.version_info[:2])" 2>$null
    if (-not (Test-Version 'Python' ($v -as [version]) ([version]'3.11') 'https://www.python.org')) { $python = $null }
}
if ($dotnet) {
    $v = & $dotnet --list-sdks 2>$null | ForEach-Object { ($_ -split ' ')[0] -as [version] } | Sort-Object -Descending | Select-Object -First 1
    if (-not (Test-Version '.NET SDK' $v ([version]'10.0.100') 'https://dotnet.microsoft.com/download')) { $dotnet = $null }
}

# The new state's config files (R-173e): made from the client's cached manifests, nothing from
# the game in the repository. The client must have started online once.
if ($needCfg) {
    if ($python -and $game) {
        & $python -I (Join-Path $repo 'tools\make_cfg.py') --game $game --state $state
        if ($LASTEXITCODE -eq 0) { Say '  Config files made from the client''s cache.' 'Green' }
        else { $problems.Add('No config files: start the 3.8 client online once (it caches its manifests), then run setup.cmd again.') }
    } else { $problems.Add('No config files: needs Python and the 3.8 client; run setup.cmd again when both are there.') }
}

# 5. tidelab.local.txt: only what differs from the defaults.
Say "`n5. tidelab.local.txt" 'Cyan'
$lines = @('# R-171: this machine''s paths (setup.cmd). Keys: lab, state, game, go.')
if ($lab -and $lab -ne (Join-Path $beside 'tidelab-data')) { $lines += "lab=$lab" }
if ($game -and $lab -and $game -ne (Join-Path $lab 'Game38')) { $lines += "game=$game" }
if ($state -ne (Join-Path $beside 'tidelab-state')) { $lines += "state=$state" }
if ($go -and -not (Find-Tool 'go')) { $lines += "go=$go" }
$write = $true
if ((Test-Path $localFile) -and ((Get-Content $localFile -Raw).Trim() -ne ($lines -join "`r`n").Trim())) {
    $write = (Read-Host "  Replace the existing tidelab.local.txt? (y/N)") -eq 'y'
}
if ($write) { Set-Content -Path $localFile -Value $lines -Encoding ASCII; Say "  Wrote $localFile" 'Green' }

# 6. Game data from the user's own client (R-174): about 30 minutes, the client starts twice.
if ($needData -and -not $NoBuild) {
    Say "`n6. Game data (from your 3.8 client; about 30 minutes, the game starts twice by itself)" 'Cyan'
    # R-174c: a newcomer run took 3.2 GB of data, 0.8 GB of Rust build and 0.3 GB more.
    $drive = [System.IO.Path]::GetPathRoot([System.IO.Path]::GetFullPath($lab))
    $freeGB = [math]::Floor((New-Object System.IO.DriveInfo $drive).AvailableFreeSpace / 1GB)
    if ($freeGB -lt 6) {
        Say "  Only $freeGB GB free on $drive; the game data needs about 6 GB." 'Yellow'
        $problems.Add("Free at least 6 GB on $drive (now $freeGB GB), then run setup.cmd again.")
    } elseif ($python -and $go -and $cargo -and $dotnet -and $game) {
        $dataArgs = @('-I', (Join-Path $repo 'tools\setup_data.py'), '--lab', $lab, '--game', $game, '--go', $go, '--cargo', $cargo, '--dotnet', $dotnet)
        if ($Rebuild) { $dataArgs += '--rebuild' }
        & $python @dataArgs
        if ($LASTEXITCODE -eq 0) { Say '  Game data ready.' 'Green' }
        else { $problems.Add('Game data not finished (see above); fix it and run setup.cmd again: it goes on where it stopped.') }
    } else { $problems.Add('No game data: needs Go, Rust, Python, .NET and the 3.8 client.') }
}

# 7. Builds.
if (-not $NoBuild) {
    Say "`n7. Build" 'Cyan'
    if ($go) {
        Push-Location (Join-Path $repo 'server')
        & $go build -o ..\out\gobin\ ./cmd/...
        if ($LASTEXITCODE -eq 0) { Say '  Go servers built (out\gobin).' 'Green' } else { $problems.Add('go build failed.') }
        Pop-Location
    }
    if ($cargo) {
        Push-Location (Join-Path $repo 'client')
        & $cargo build --release
        if ($LASTEXITCODE -eq 0) { Say '  Launcher and patch DLL built (client\target\release).' 'Green' } else { $problems.Add('cargo build failed.') }
        Pop-Location
    }
    $paks = Join-Path $repo 'out\paks'
    $cs = Join-Path $paks 'rb38_cs_300_p.pak'
    $login = Join-Path $paks 'rb38_login_100_p.pak'
    if ((Test-Path $cs) -and (Test-Path $login)) { Say '  Patch paks already in out\paks.' 'Green' }
    elseif ($python -and $dotnet -and $lab -and (Test-Path (Join-Path $lab 'backups\csref38'))) {
        Say '  Building the patch paks (about 5 minutes)...'
        & $python -I (Join-Path $repo 'tools\build_cspak.py')
        if ($LASTEXITCODE -ne 0) { $problems.Add('build_cspak.py failed.') }
        & $python -I (Join-Path $repo 'tools\build_loginpak.py')
        if ($LASTEXITCODE -ne 0) { $problems.Add('build_loginpak.py failed.') }
    } else { $problems.Add('Patch paks not built: copy out\paks from the old machine, or install Python and .NET and have the lab''s backups\csref38.') }
}

Say ''
if ($problems.Count -eq 0) { Say 'Ready. Start the game with start-game.cmd (or the launcher in client\target\release).' 'Green' }
else {
    Say 'Still to do:' 'Yellow'
    foreach ($p in $problems) { Say "  - $p" 'Yellow' }
}
