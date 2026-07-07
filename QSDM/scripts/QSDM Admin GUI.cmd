@echo off
set SCRIPT_DIR=%~dp0
powershell.exe -NoProfile -ExecutionPolicy Bypass -NoExit -File "%SCRIPT_DIR%start_local_gui_admin.ps1"
