@echo off
rem Runs apply.ps1 from this folder with the same arguments.
rem Usage: apply.cmd -Coddy C:\coddy-src -Check
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0apply.ps1" %*
exit /b %ERRORLEVEL%
