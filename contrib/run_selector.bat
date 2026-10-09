@echo off
rem Run the steps one after another: build, preview the chapter list, ask, download.
rem
rem Usage: contrib\run_selector.bat <series-url> [source.json] [mobi^|cbz] [out-dir]
rem Env:   LANG_CODE (default: en)
setlocal

set "URL=%~1"
if "%URL%"=="" (
  echo Usage: %~nx0 ^<series-url^> [source.json] [mobi^|cbz] [out-dir]
  exit /b 1
)
set "CONFIG=%~2"
if "%CONFIG%"=="" set "CONFIG=source.json"
set "FORMAT=%~3"
if "%FORMAT%"=="" set "FORMAT=cbz"
set "OUT=%~4"
if "%OUT%"=="" set "OUT=out"
if "%LANG_CODE%"=="" set "LANG_CODE=en"
set "ROOT=%~dp0.."

echo [1/3] Build
go build -C "%ROOT%" -o "%ROOT%\kojirou.exe" . || exit /b 1

echo [2/3] Preview chapters (nothing is downloaded)
"%ROOT%\kojirou.exe" "%URL%" -l %LANG_CODE% --source-config "%CONFIG%" --dry-run || exit /b 1

set /p ANSWER=Continue with the download? [y/N] 
if /i not "%ANSWER%"=="y" (
  echo Stopped.
  exit /b 0
)

echo [3/3] Download to %OUT% as %FORMAT%
"%ROOT%\kojirou.exe" "%URL%" -l %LANG_CODE% --source-config "%CONFIG%" --format %FORMAT% --out "%OUT%" || exit /b 1
echo Done.
