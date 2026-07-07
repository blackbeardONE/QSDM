@echo off
set SCRIPT_DIR=%~dp0
powershell.exe -NoProfile -ExecutionPolicy Bypass -NoExit -File "%SCRIPT_DIR%install_local_stack_task_admin.ps1"
