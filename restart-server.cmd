@echo off
rem Restarts tidelab's Go server (double-click this file).
rem 1. Stops the three servers (loginsv, configsv, gamesv).
rem 2. Keeps the old game log as out\logs\gamesv_prev.log.
rem 3. Runs start-server.cmd, which rebuilds them (1-2 s) and starts them again.
rem The server is up when out\logs\gamesv.log says "is listening at" (a few seconds).
rem Restarting kicks you out of the game: log in again afterwards.
set "LOGS=%~dp0out\logs"
echo Stopping the server...
taskkill /F /IM gamesv.exe /IM loginsv.exe /IM configsv.exe >nul 2>&1
ping -n 3 127.0.0.1 >nul
if exist "%LOGS%\gamesv.log" copy /y "%LOGS%\gamesv.log" "%LOGS%\gamesv_prev.log" >nul
echo Starting the server...
call "%~dp0start-server.cmd"
echo Done. Wait until out\logs\gamesv.log shows "is listening at", then log in.
ping -n 6 127.0.0.1 >nul
