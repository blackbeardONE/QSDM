param(
    [string]$TaskName = "QSDM-Local-Stack"
)

$ErrorActionPreference = "Stop"

& schtasks.exe /End /TN $TaskName 2>$null | Out-Null
& schtasks.exe /Delete /TN $TaskName /F | Out-Host
$taskExit = $LASTEXITCODE

$startup = [Environment]::GetFolderPath("Startup")
if (-not [string]::IsNullOrWhiteSpace($startup)) {
    $launcher = Join-Path $startup "$TaskName.vbs"
    Remove-Item -LiteralPath $launcher -Force -ErrorAction SilentlyContinue
}

if ($taskExit -eq 0) {
    Write-Host "Deleted scheduled task $TaskName"
} else {
    Write-Host "No scheduled task named $TaskName was deleted"
}
Write-Host "Removed Startup launcher for $TaskName if it existed"
