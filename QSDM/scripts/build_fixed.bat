@echo off
REM Build QSDM with proper Go environment setup (without CGO)

REM Set GOROOT to official Go installation
set "GOROOT=C:\Program Files\Go"
set "PATH=C:\Program Files\Go\bin;%PATH%"

echo Building QSDM without CGO dependencies...
echo Go version:
go version

echo.
echo Building...
set CGO_ENABLED=0
go build -o qsdm.exe ./cmd/qsdm

if %ERRORLEVEL% EQU 0 (
    echo.
    echo Build successful! Executable: qsdm.exe
    echo.
    echo Note: This build does not include:
    echo   - WASM module support
    echo   - Quantum-safe cryptography (liboqs)
    echo   - CUDA acceleration
    echo   - SQLite storage (uses file storage instead)
    echo.
    echo The dashboard and core functionality will still work.
) else (
    echo.
    echo Build failed. Check errors above.
    exit /b %ERRORLEVEL%
)

