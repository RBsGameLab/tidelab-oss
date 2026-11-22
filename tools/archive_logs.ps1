# R-166: before start-server.cmd overwrites the servers' logs, move each non-empty one to
# <logs>\archive\<name>-<modified, unix seconds>.log and keep the newest 30 per server, so
# tools/tl_audit.py still finds a play session's server log after restarts. The launcher does
# the same (client/core/src/steps.rs archive_log). Best effort: never fails the start.
param([Parameter(Mandatory = $true)][string]$Logs, [int]$Keep = 30)
$ErrorActionPreference = 'SilentlyContinue'
$archive = Join-Path $Logs 'archive'
foreach ($name in 'gamesv', 'loginsv', 'configsv') {
    $log = Join-Path $Logs "$name.log"
    $item = Get-Item -LiteralPath $log
    if (-not $item -or $item.Length -eq 0) { continue }
    New-Item -ItemType Directory -Force -Path $archive | Out-Null
    $secs = [DateTimeOffset]::new($item.LastWriteTimeUtc).ToUnixTimeSeconds()
    $to = Join-Path $archive "$name-$secs.log"
    Move-Item -LiteralPath $log -Destination $to -Force
    if (Test-Path -LiteralPath $log) { Copy-Item -LiteralPath $log -Destination $to -Force }
    Get-ChildItem -LiteralPath $archive -File | Where-Object { $_.Name -match "^$name-\d+\.log$" } |
        Sort-Object Name -Descending | Select-Object -Skip $Keep | Remove-Item -Force
}
exit 0
