@echo off
setlocal EnableExtensions

rem Always run in script directory
cd /d "%~dp0"

echo.
echo === GoBiliTicket build ===
echo.
go version
echo.

where go >nul 2>&1
if errorlevel 1 (
  echo [X] Go not found. Please install Go first.
  echo     https://go.dev/dl/
  echo.
  pause
  exit /b 1
)

set "OUTDIR=%~dp0build"
if not exist "%OUTDIR%" mkdir "%OUTDIR%"
set "OUTGUI=%OUTDIR%\\gobiliticket.exe"

set "LOGTIDY=%OUTDIR%\\build_tidy.log"
set "LOGGUI=%OUTDIR%\\build_gui.log"
set "LOGCON=%OUTDIR%\\build_console.log"

echo [0/2] go mod tidy
echo --- go mod tidy --- > "%LOGTIDY%"
go mod tidy 1>>"%LOGTIDY%" 2>&1
if errorlevel 1 (
  echo.
  echo [X] go mod tidy failed
  echo     See log: build\\build_tidy.log
  pause
  exit /b 1
)

rem Clean old outputs
if exist "gobiticket.exe" del /q "gobiticket.exe"
if exist "%OUTGUI%" (
  del /q "%OUTGUI%" >nul 2>&1
  if exist "%OUTGUI%" (
    rem File may be locked if the app is running (Windows cannot overwrite running exe)
    echo [!] build\\gobiliticket.exe is in use, trying to stop it...
    taskkill /IM gobiliticket.exe /F >nul 2>&1
    taskkill /IM gobiliticket_new.exe /F >nul 2>&1
    timeout /t 2 /nobreak >nul 2>&1
    del /q "%OUTGUI%" >nul 2>&1
  )
)
if exist "%OUTGUI%" (
  echo.
  echo [!] Cannot overwrite build\\gobiliticket.exe (file in use)
  echo     Please close the running program, or end it in Task Manager.
  echo     You can also run: taskkill /IM gobiliticket.exe /F
  echo.
  echo [!] Will build to a new filename to avoid overwrite...
  for /f %%i in ('powershell -NoProfile -Command \"Get-Date -Format yyyyMMdd_HHmmss\"') do set "TS=%%i"
  if "%TS%"=="" set "TS=%RANDOM%"
  set "OUTGUI=%OUTDIR%\\gobiliticket_%TS%.exe"
  echo [!] Output: build\\gobiliticket_%TS%.exe
)

echo [1/2] Build GUI exe (double click)
echo --- go build (GUI) --- > "%LOGGUI%"
go build -mod=mod -ldflags="-s -w -H=windowsgui" -o "%OUTGUI%" .\\cmd 1>>"%LOGGUI%" 2>&1
if errorlevel 1 (
  echo.
  echo [X] Build failed (GUI)
  echo     See log: build\\build_gui.log
  pause
  exit /b 1
)

echo [2/2] Build console exe (for debugging)
echo --- go build (console) --- > "%LOGCON%"
go build -mod=mod -ldflags="-s -w" -o "gobiticket.exe" .\\cmd 1>>"%LOGCON%" 2>&1
if errorlevel 1 (
  echo.
  echo [X] Build failed (console)
  echo     See log: build\\build_console.log
  pause
  exit /b 1
)

echo.
echo Done.
echo   - gobiticket.exe (console)
echo   - build\\gobiliticket.exe (GUI)
echo.
echo Tips:
echo   - Double click build\\gobiliticket.exe to start web UI
echo   - Or run: gobiticket.exe web
echo.
pause
