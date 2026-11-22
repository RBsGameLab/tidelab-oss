@echo off
rem Generates the protocol and data-table Go code from your own 3.8 client assets
rem (ProtoGen -> genproto -> ConfigExport -> optional panel display names).
rem Pass any tools\generate.ps1 flags through, e.g. generate.cmd -ProtocolDll ... -Schema ... -BinData ...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0tools\generate.ps1" %*
exit /b %errorlevel%
