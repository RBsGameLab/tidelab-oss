@echo off
rem Audit after a play session (double-click this file, R-166): what the session touched that
rem tidelab has not finished. Reads the last Client.log and the server logs of the same time span
rem (out\logs, out\logs\archive), prints a short summary (red = severe) and writes the full
rem report to out\audit\. Noise to hide: one regex per line in tools\tl_audit_ignore.txt.
chcp 65001 >nul
python -I "%~dp0tools\tl_audit.py" %*
pause
