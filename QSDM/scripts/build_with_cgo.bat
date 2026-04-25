@echo off
REM Build QSDM with CGO enabled (full features)
REM This requires liboqs and other C dependencies to be installed

echo Building QSDM with CGO enabled (full features)...
echo This build includes:
echo   - WASM module support
echo   - Quantum-safe cryptography (liboqs)
echo   - CUDA acceleration (if available)
echo   - SQLite storage
echo.

REM Set Go environment
set "GOROOT=C:\Program Files\Go"
set "PATH=C:\Program Files\Go\bin;%PATH%"
set CGO_ENABLED=1

REM Check for liboqs
if exist "C:\liboqs\include" (
    echo Found liboqs installation
    set "CGO_CFLAGS=-IC:\liboqs\include"
    set "CGO_LDFLAGS=-LC:\liboqs\lib -loqs"
) else (
    echo WARNING: liboqs not found at C:\liboqs\
    echo The build may fail. Install liboqs or adjust paths.
    echo.
    echo You can set custom paths:
    echo   set CGO_CFLAGS=-I^<path-to-liboqs^>/include
    echo   set CGO_LDFLAGS=-L^<path-to-liboqs^>/lib -loqs
    echo.
)

REM Check for CUDA (optional)
if exist "C:\CUDA\include" (
    echo Found CUDA installation
    if defined CGO_CFLAGS (
        set "CGO_CFLAGS=%CGO_CFLAGS% -IC:\CUDA\include"
    ) else (
        set "CGO_CFLAGS=-IC:\CUDA\include"
    )
    if defined CGO_LDFLAGS (
        set "CGO_LDFLAGS=%CGO_LDFLAGS% -LC:\CUDA\lib\x64 -lcudart"
    ) else (
        set "CGO_LDFLAGS=-LC:\CUDA\lib\x64 -lcudart"
    )
) else (
    echo CUDA not found (optional, 3D mesh acceleration will be unavailable)
)

echo.
echo CGO Environment:
echo   CGO_ENABLED=%CGO_ENABLED%
echo   CGO_CFLAGS=%CGO_CFLAGS%
echo   CGO_LDFLAGS=%CGO_LDFLAGS%
echo.

REM Verify Go version
echo Go version:
go version
if %ERRORLEVEL% NEQ 0 (
    echo ERROR: Go is not working properly
    exit /b 1
)

echo.
echo Building...
go build -o qsdm.exe ./cmd/qsdm

if %ERRORLEVEL% EQU 0 (
    echo.
    echo Build successful! Executable: qsdm.exe
    echo.
    echo All features enabled:
    echo   - Quantum-safe consensus (Proof-of-Entanglement)
    echo   - WASM modules (wallet/validator)
    echo   - SQLite storage
    echo   - Full cryptographic verification
) else (
    echo.
    echo Build failed. Common issues:
    echo   1. liboqs not installed or not in PATH
    echo   2. C compiler (gcc/MSVC) not available
    echo   3. Missing CGO_CFLAGS or CGO_LDFLAGS
    echo.
    echo See INSTALL_OQS.md for installation instructions
    exit /b %ERRORLEVEL%
)

