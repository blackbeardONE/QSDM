@echo off
REM Build WASM modules using TinyGo on Windows

set TINYGO_PATH=C:\tinygo\bin\tinygo.exe
set WASMOPT=C:\binaryen\bin\wasm-opt.exe

if not exist "%TINYGO_PATH%" (
  echo TinyGo compiler not found at %TINYGO_PATH%
  exit /b 1
)

if not exist "%WASMOPT%" (
  echo wasm-opt not found at %WASMOPT%
  exit /b 1
)

REM Example: Build validator WASM module
echo Building validator.wasm...
"%TINYGO_PATH%" build -o wasm_modules/validator/validator.wasm -target wasi wasm_modules/validator/validator.go
if errorlevel 1 (
  echo Failed to build validator.wasm
  exit /b 1
)

REM Example: Build wallet WASM module
echo Building wallet.wasm...
"%TINYGO_PATH%" build -o wasm_modules/wallet/wallet.wasm -target wasi wasm_modules/wallet/wallet.go
if errorlevel 1 (
  echo Failed to build wallet.wasm
  exit /b 1
)

echo WASM modules built successfully.
