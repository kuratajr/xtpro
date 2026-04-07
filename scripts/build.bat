@echo off
setlocal
echo ========================================================
echo       xtpro Multi-Platform Build Tool
echo ========================================================

REM Switch to project root directory regardless of where script is run
pushd "%~dp0.."

REM Create bin directory
if not exist "bin" mkdir bin

REM Ensure Icon exists in bin for Linux/Mac usage
echo [0/5] Preparing resources...
if exist "scripts\icon.png" copy "scripts\icon.png" "bin\icon.png" >nul
if exist "scripts\icon.png" copy "scripts\icon.png" "bin\xtpro.png" >nul

REM --------------------------------------------------------
REM 1. Windows Build (AMD64)
REM --------------------------------------------------------
echo [1/5] Building for Windows (amd64)...
REM Optional: embed icon resources if .ico files exist
if exist "src\backend\cmd\client\xtpro.ico" (
  %USERPROFILE%\go\bin\rsrc.exe -ico src\backend\cmd\client\xtpro.ico -o src\backend\cmd\client\rsrc_windows.syso
) else (
  echo [WARN] Missing client icon: src\backend\cmd\client\xtpro.ico (skipping rsrc)
)
if exist "src\backend\cmd\server\xtpro.ico" (
  %USERPROFILE%\go\bin\rsrc.exe -ico src\backend\cmd\server\xtpro.ico -o src\backend\cmd\server\rsrc_windows.syso
) else (
  echo [WARN] Missing server icon: src\backend\cmd\server\xtpro.ico (skipping rsrc)
)
cd src\backend
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o ..\..\bin\xtpro-server.exe .\cmd\server
go build -ldflags="-s -w" -o ..\..\bin\xtpro.exe .\cmd\client
cd ..\..

REM --------------------------------------------------------
REM 2. Linux Build (AMD64) + Desktop Entry
REM --------------------------------------------------------
echo [2/5] Building for Linux (amd64)...
cd src\backend
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o ..\..\bin\xtpro-linux-server .\cmd\server
go build -ldflags="-s -w" -o ..\..\bin\xtpro-linux-client .\cmd\client
cd ..\..

REM Generate .desktop file for Linux
echo    - Generating Linux Desktop Entry...
(
echo [Desktop Entry]
echo Version=1.0
echo Type=Application
echo Name=xtpro Client
echo Comment=Secure Tunnel Client
echo Exec=./xtpro-linux-client
echo Icon=./icon.png
echo Terminal=true
echo Categories=Network;Utility;
) > bin\xtpro-linux.desktop

REM --------------------------------------------------------
REM 3. macOS Build
REM --------------------------------------------------------
echo [3/5] Building for macOS...
cd src\backend
set GOOS=darwin
set GOARCH=amd64
go build -ldflags="-s -w" -o ..\..\bin\xtpro-mac-intel .\cmd\client
set GOARCH=arm64
go build -ldflags="-s -w" -o ..\..\bin\xtpro-mac-m1 .\cmd\client
cd ..\..

REM --------------------------------------------------------
REM 4. Android (Termux)
REM --------------------------------------------------------
echo [4/5] Building for Android...
cd src\backend
set GOOS=android
set GOARCH=arm64
go build -ldflags="-s -w" -o ..\..\bin\xtpro-android .\cmd\client
cd ..\..

REM --------------------------------------------------------
REM 5. Packaging Server
REM --------------------------------------------------------
echo [5/5] Packaging Server (server.tar.gz)...
REM Create a temporary directory for packaging to keep root clean
if not exist "bin\server_package" mkdir "bin\server_package"

REM Copy binaries
copy "bin\xtpro-server.exe" "bin\server_package\" >nul
copy "bin\xtpro-linux-server" "bin\server_package\" >nul

REM Copy frontend folder for Dashboard
echo    - Copying frontend assets...
xcopy "frontend" "bin\server_package\frontend\" /E /I /Q /Y >nul

REM Compress using tar (available on Windows 10+)
echo    - Compressing...
cd bin
tar -czf server.tar.gz -C server_package .
cd ..

REM Cleanup temp folder
rd /s /q "bin\server_package"

REM Restore original directory
popd

REM --------------------------------------------------------
echo.
echo ========================================================
echo ✅ Build Complete!
echo ========================================================
echo Windows:  bin\xtpro.exe
echo Linux:    bin\xtpro-linux-client
echo macOS:    bin\xtpro-mac-m1 / intel
echo Android:  bin\xtpro-android
echo.
echo 📦 SERVER PACKAGE: bin\server.tar.gz
echo    (Contains: xtpro-server.exe, xtpro-linux-server, frontend/)
echo.
