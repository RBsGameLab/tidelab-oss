@echo off
rem R-171: prepares a new machine (paths, tools, gateway key, builds). See tools\setup.ps1.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0tools\setup.ps1" %*
pause
