param(
    [string]$LauncherName = "QSDM-Local-GUI"
)

$ErrorActionPreference = "Stop"

$startup = [Environment]::GetFolderPath("Startup")
if ([string]::IsNullOrWhiteSpace($startup)) {
    throw "Could not locate the current user's Startup folder"
}

$launcher = Join-Path $startup "$LauncherName.vbs"
Remove-Item -LiteralPath $launcher -Force -ErrorAction SilentlyContinue

Write-Host "Removed GUI Startup launcher if it existed: $launcher"
