@echo off
rem Обёртка для update.ps1: двойной щелчок или "update.cmd -Check".
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0update.ps1" %*
