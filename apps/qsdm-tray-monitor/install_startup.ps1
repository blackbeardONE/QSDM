param(
    [string]$ExePath = (Join-Path $PSScriptRoot "dist\qsdm-tray-monitor.exe"),
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..\QSDM")).Path
)

$ErrorActionPreference = "Stop"

$ExePath = (Resolve-Path $ExePath).Path
$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$startup = [Environment]::GetFolderPath("Startup")
if ([string]::IsNullOrWhiteSpace($startup)) {
    throw "Could not locate the current user's Startup folder"
}

$launcher = Join-Path $startup "QSDM-Tray-Monitor.vbs"
$command = "`"$ExePath`" --root `"$QsdmRoot`""
$vbsCommand = $command.Replace('"', '""')
Set-Content -LiteralPath $launcher -Encoding ASCII -Value @"
Set shell = CreateObject("WScript.Shell")
shell.Run "$vbsCommand", 0, False
"@

Write-Host "Installed QSDM Tray Monitor Startup launcher: $launcher"
