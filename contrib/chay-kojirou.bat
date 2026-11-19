@echo off
title Kojirou
cd /d "%~dp0.."
taskkill /F /IM kojirou.exe >nul 2>&1
echo Dang chay Kojirou tai http://127.0.0.1:8080 - dong cua so nay de tat.
kojirou.exe serve --library "%~dp0."
pause
