@echo off
rem R-171: this machine's paths, nothing written in. Sets TIDELAB (this checkout), LAB (game data,
rem client, tools), STATE (the servers' save tree), GAME (the 3.8 client's root, holding Client),
rem DATA (LAB\gamedata\3.8) and GO (go.exe).
rem Defaults: LAB and STATE beside the checkout (..\tidelab-data, ..\tidelab-state),
rem GAME = LAB\Game38, GO = go.exe on PATH, else the newest %LOCALAPPDATA%\Programs\go-sdk\*.
rem tidelab.local.txt at the checkout's root overrides them: key=value lines (lab, state, game,
rem go, power), # for comments. The launcher (client\core\src\config.rs) and tools\tl_paths.py read
rem the same file. tools\setup.ps1 writes it on a new machine.
for %%I in ("%~dp0..") do set "TIDELAB=%%~fI"
for %%I in ("%TIDELAB%\..") do set "TL_PARENT=%%~fI"
if "%TL_PARENT:~-1%"=="\" set "TL_PARENT=%TL_PARENT:~0,-1%"
set "LAB="
set "STATE="
set "GAME="
set "GO="
set "POWER_SCRIPT="
if exist "%TIDELAB%\tidelab.local.txt" for /f "usebackq eol=# tokens=1,* delims==" %%A in ("%TIDELAB%\tidelab.local.txt") do (
  if /i "%%A"=="lab" set "LAB=%%B"
  if /i "%%A"=="state" set "STATE=%%B"
  if /i "%%A"=="game" set "GAME=%%B"
  if /i "%%A"=="go" set "GO=%%B"
  if /i "%%A"=="power" set "POWER_SCRIPT=%%B"
)
if not defined LAB set "LAB=%TL_PARENT%\tidelab-data"
if not defined STATE set "STATE=%TL_PARENT%\tidelab-state"
if not defined GAME set "GAME=%LAB%\Game38"
if not defined POWER_SCRIPT set "POWER_SCRIPT=%LAB%\tools\power\cpu-cap.ps1"
set "DATA=%LAB%\gamedata\3.8"
if not defined GO for /f "delims=" %%G in ('where go.exe 2^>NUL') do if not defined GO set "GO=%%G"
if not defined GO for /f "delims=" %%G in ('dir /b /ad /o-n "%LOCALAPPDATA%\Programs\go-sdk" 2^>NUL') do (
  if not defined GO if exist "%LOCALAPPDATA%\Programs\go-sdk\%%G\go\bin\go.exe" set "GO=%LOCALAPPDATA%\Programs\go-sdk\%%G\go\bin\go.exe"
)
set "TL_PARENT="
exit /b 0
