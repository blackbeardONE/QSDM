@echo off
REM Build QSDM without CGO dependencies (for development/testing)
REM This builds a version without WASM, quantum crypto, or CUDA support

echo Building QSDM without CGO dependencies...
echo This version will have limited functionality but will run without external C libraries.

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
    echo.
    echo The dashboard and core functionality will still work.
) else (
    echo.
    echo Build failed. Check errors above.
    exit /b %ERRORLEVEL%
)

