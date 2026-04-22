@echo off
REM Build the validator WASM module from Go source on Windows using TinyGo for WASI target

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

echo Building validator.wasm with TinyGo...
"%TINYGO_PATH%" build -o wasm_modules\validator\validator.wasm -target wasi wasm_modules\validator\validator.go
if errorlevel 1 (
  echo Failed to build validator.wasm
  exit /b 1
)

echo Optimizing validator.wasm...
"%WASMOPT%" -O4 wasm_modules\validator\validator.wasm -o wasm_modules\validator\validator.wasm
if errorlevel 1 (
  echo Failed to optimize validator.wasm
  exit /b 1
)

echo Build complete: wasm_modules\validator\validator.wasm
