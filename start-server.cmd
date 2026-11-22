@echo off
title Tidelab server
rem Starts tidelab's Go server, the live server since R-144h (2026-11-20).
rem Builds server\ of this checkout into out\gobin (1-2 s when little changed), then starts
rem configsv (5500), loginsv (10001) and gamesv (13100/UDP) on the state directory STATE.
rem Paths: tools\paths.cmd (defaults beside this checkout, tidelab.local.txt overrides).
rem Logs: out\logs\configsv.log, loginsv.log, gamesv.log, build.log. It is up when gamesv.log
rem says "is listening at".
call "%~dp0tools\paths.cmd"
set "BIN=%TIDELAB%\out\gobin"
set "LOGS=%TIDELAB%\out\logs"
if not defined GO echo Go not found: put go.exe on PATH, or go=^<path^> in tidelab.local.txt. & exit /b 1
if not exist "%DATA%\BinData" echo No game data at %DATA%: set lab=^<path^> in tidelab.local.txt. & exit /b 1
rem R-173: no gateway key is needed (unencrypted sessions with our C# pak); the config files are.
if not exist "%STATE%\cfg\index" echo No config files at %STATE%\cfg\index: run setup.cmd first. && exit /b 1
if not exist "%LOGS%" mkdir "%LOGS%"
taskkill /F /IM loginsv.exe /IM configsv.exe /IM gamesv.exe >NUL 2>&1
rem R-166: keep the old logs in out\logs\archive (tools\tl_audit.py reads them).
powershell -NoProfile -ExecutionPolicy Bypass -File "%TIDELAB%\tools\archive_logs.ps1" -Logs "%LOGS%" >NUL 2>&1
echo Building the Go servers...
pushd "%TIDELAB%\server"
"%GO%" build -o "%BIN%/" ./cmd/configsv ./cmd/loginsv ./cmd/gamesv > "%LOGS%\build.log" 2>&1
set "RC=%errorlevel%"
popd
if not "%RC%"=="0" type "%LOGS%\build.log" & echo Build FAILED: see %LOGS%\build.log. & exit /b 1
start "tidelab configsv" /min cmd /c ""%BIN%\configsv.exe" --state_dir "%STATE%" > "%LOGS%\configsv.log" 2>&1"
start "tidelab loginsv" /min cmd /c ""%BIN%\loginsv.exe" --state_dir "%STATE%" > "%LOGS%\loginsv.log" 2>&1"
start "tidelab gamesv" /min cmd /c ""%BIN%\gamesv.exe" --state_dir "%STATE%" --bindata "%DATA%\BinData" --more_bindata "%DATA%\BinData-beta" --bin_cache "%TIDELAB%\out\bincache" --quest_data "%DATA%\questgen\assets\quest" --cmd_file "%STATE%\cmd.txt" > "%LOGS%\gamesv.log" 2>&1"
echo Started. It is up when %LOGS%\gamesv.log says "is listening at".
