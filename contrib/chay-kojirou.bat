@echo off
title Kojirou
rem Chay Kojirou. Moi lan mo, neu co ban moi tren GitHub thi tu cap nhat truoc (can co mang va Go).
rem Muon bo qua cap nhat: chay  chay-kojirou.bat --no-update
cd /d "%~dp0.."
set "REPO=davit-philn/kojirou"
set "BRANCH=claude/clever-planck-ee82ul"

if /i "%~1"=="--no-update" goto run

set "NEW="
for /f %%i in ('curl -s -m 10 -H "Accept: application/vnd.github.sha" "https://api.github.com/repos/%REPO%/commits/%BRANCH%" 2^>nul') do set "NEW=%%i"
if not defined NEW goto run
rem Mot ma phien ban hop le dai dung 40 ky tu
if "%NEW:~39,1%"=="" goto run
if not "%NEW:~40,1%"=="" goto run

set "OLD="
if exist kojirou.version set /p OLD=<kojirou.version
if not exist kojirou.exe set "OLD="
if "%NEW%"=="%OLD%" goto run

echo Co ban moi (%NEW:~0,7%), dang cap nhat...
taskkill /F /IM kojirou.exe >nul 2>&1
if exist kojirou-update rmdir /s /q kojirou-update
mkdir kojirou-update
curl -sL -m 180 -o kojirou-update\src.zip "https://github.com/%REPO%/archive/%NEW%.zip"
if errorlevel 1 goto failed
tar -xf kojirou-update\src.zip -C kojirou-update
if errorlevel 1 goto failed
if exist kojirou-new.exe del kojirou-new.exe
for /d %%d in (kojirou-update\kojirou-*) do (
  pushd "%%d"
  go build -o "..\..\kojirou-new.exe" .
  popd
)
if not exist kojirou-new.exe goto failed
move /y kojirou-new.exe kojirou.exe >nul
>kojirou.version echo %NEW%
rmdir /s /q kojirou-update
echo Da cap nhat xong.
goto run

:failed
echo Cap nhat khong thanh cong (kiem tra mang hoac Go). Dung ban hien tai.
if exist kojirou-update rmdir /s /q kojirou-update
if exist kojirou-new.exe del kojirou-new.exe

:run
if not exist kojirou.exe (
  echo Khong tim thay kojirou.exe. Hay kiem tra ket noi mang va da cai Go chua.
  pause
  exit /b 1
)
taskkill /F /IM kojirou.exe >nul 2>&1
echo Dang chay Kojirou tai http://127.0.0.1:8080 - dong cua so nay de tat.
kojirou.exe serve --library "%~dp0."
pause
