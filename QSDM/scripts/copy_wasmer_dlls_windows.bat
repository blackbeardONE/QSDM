@echo off
REM Copy Wasmer native DLLs to the tests directory for runtime linking

REM Define source directory where Wasmer DLLs are built
set WASMER_BUILD_DIR=wasmer-go-patched\target\release

REM Define destination directory (tests folder)
set DEST_DIR=tests

REM Copy DLL files
echo Copying Wasmer DLLs from %WASMER_BUILD_DIR% to %DEST_DIR%
copy "%WASMER_BUILD_DIR%\*.dll" "%DEST_DIR%\"

if errorlevel 1 (
  echo Failed to copy Wasmer DLLs.
  exit /b 1
)

echo Wasmer DLLs copied successfully.
