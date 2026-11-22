@echo off
title Tidelab 3.8 Offline Launcher
rem Starts the 3.8 client against tidelab's Go server, logged in with ACCOUNT (3838) on the state
rem directory. Starts the server first (start-server.cmd) when none runs.
rem Paths: tools\paths.cmd: LAB, GAME (the client, default LAB\Game38), STATE.
set "LOG=%~dp0out\logs\gamesv.log"
call "%~dp0tools\paths.cmd"
set "WIN64=%GAME%\Client\Binaries\Win64"
if not exist "%WIN64%" echo No 3.8 client at %GAME%: set game=^<path^> in tidelab.local.txt. & pause & exit /b 1
rem R-145: the account the client logs in with; the client skips its debug login view and shows
rem only the official "click to enter" screen (tools\rbauto\RBLogin.cs). The window launcher
rem keeps its own in tidelab-account.txt.
set "ACCOUNT=3838"

tasklist /FI "IMAGENAME eq gamesv.exe" /FO CSV 2>NUL | %SystemRoot%\System32\find.exe /I "gamesv.exe" >NUL
if errorlevel 1 goto start_server
echo Server already running.
goto launch

:start_server
echo Starting the server...
if exist "%LOG%" del "%LOG%"
call "%~dp0start-server.cmd"
if errorlevel 1 (echo The server did not start. & pause & exit /b 1)

:wait_server
timeout /t 1 /nobreak >NUL
powershell -NoProfile -Command "if ((Select-String -Path '%LOG%' -Pattern 'is listening at' -ErrorAction SilentlyContinue).Count -ge 1) { exit 0 } else { exit 1 }"
if errorlevel 1 goto wait_server
echo Server is up.

:launch
rem tidelab's launcher and patch DLL (client\start, client\patch), copied beside the client at
rem each start.
set "PATCH=%~dp0client\target\release"
if not exist "%PATCH%\tidelab_start.exe" goto not_built
if not exist "%PATCH%\tidelab_patch.dll" goto not_built
echo Launching 3.8 client...
cd /d "%WIN64%"
rem Lower CPU load: tidelab_ace.quiet is created after the first world load.
if exist "tidelab_ace.quiet" del "tidelab_ace.quiet"
if not exist "tidelab_perfsight.quiet" type nul > "tidelab_perfsight.quiet"
rem Optional: cap the CPU at 3000 MHz (E and P cores) while the game runs; previous power plan returns on exit.
if exist "%POWER_SCRIPT%" start "" /min powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%POWER_SCRIPT%" -UntilGameExits -MaxMHz 3000 -MaxMHzP 3000
rem The two patch paks, installed at each start: the C# patch (tools\build_cspak.py) and the
rem login pak (tools\build_loginpak.py), both built into out\paks.
for %%P in (rb38_cs_300_p.pak rb38_login_100_p.pak) do (
  if exist "%~dp0out\paks\%%P" (
    copy /y "%~dp0out\paks\%%P" "%GAME%\Client\Content\Paks\%%P" >NUL
    if errorlevel 1 echo Could not install %%P, is the 3.8 client still running? The old one stays.
  ) else (
    echo %%P is not built in out\paks, the installed one stays.
  )
)
copy /y "%PATCH%\tidelab_start.exe" "%WIN64%\" >NUL
copy /y "%PATCH%\tidelab_patch.dll" "%WIN64%\" >NUL
if errorlevel 1 echo Could not install tidelab_patch.dll, is the 3.8 client still running? The old one stays.
start "" "%WIN64%\tidelab_start.exe" -krqlv=SD -RbAccount=%ACCOUNT%
exit /b 0

:not_built
echo tidelab's launcher and patch DLL are not built: run cargo build --release in client.
pause
exit /b 1
