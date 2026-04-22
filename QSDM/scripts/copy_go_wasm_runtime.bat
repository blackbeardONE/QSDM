@echo off
REM Copy the official Go wasm_exec.js runtime to the tests directory as go.wasm.js

set SRC="C:\Program Files\Go\lib\wasm\wasm_exec.js"
set DEST="d:\Projects\QSDM\tests\go.wasm.js"

echo Copying %SRC% to %DEST%
copy /Y %SRC% %DEST%

if %ERRORLEVEL% EQU 0 (
    echo Copy succeeded.
) else (
    echo Copy failed. Please check the source path and permissions.
)
