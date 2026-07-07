param(
    [string]$ServiceName = "QSDMMiner",
    [string]$SourceMiner,
    [string]$RuntimeMiner
)

$ErrorActionPreference = "Stop"

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Resolve-RepoRoot {
    return (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}

if (-not $SourceMiner) {
    $SourceMiner = Join-Path $PSScriptRoot "..\source\qsdmminer-console.exe"
}
if (-not $RuntimeMiner) {
    $RuntimeMiner = Join-Path (Resolve-RepoRoot) "Blackbeard\qsdmminer.exe"
}

$SourceMiner = (Resolve-Path $SourceMiner).Path
$RuntimeDir = Split-Path $RuntimeMiner -Parent

if (-not (Test-Path $SourceMiner)) {
    throw "Source miner not found: $SourceMiner"
}
if (-not (Test-Path $RuntimeDir)) {
    throw "Runtime miner directory not found: $RuntimeDir"
}

if (-not (Test-Administrator)) {
    $pwsh = (Get-Process -Id $PID).Path
    $args = @(
        "-NoProfile",
        "-ExecutionPolicy", "Bypass",
        "-NoExit",
        "-File", $PSCommandPath,
        "-ServiceName", $ServiceName,
        "-SourceMiner", $SourceMiner,
        "-RuntimeMiner", $RuntimeMiner
    )
    Start-Process -FilePath $pwsh -ArgumentList $args -Verb RunAs
    Write-Host "Requested administrator window for QSDM miner runtime update."
    return
}

Write-Host "Updating QSDM miner runtime..."
Write-Host "Service: $ServiceName"
Write-Host "Source:  $SourceMiner"
Write-Host "Runtime: $RuntimeMiner"

$service = Get-Service -Name $ServiceName -ErrorAction Stop
if ($service.Status -ne "Stopped") {
    Write-Host "Stopping $ServiceName..."
    Stop-Service -Name $ServiceName -Force
    $service.WaitForStatus("Stopped", [TimeSpan]::FromSeconds(30))
}

if (Test-Path $RuntimeMiner) {
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $backup = Join-Path $RuntimeDir "qsdmminer.backup.$stamp.exe"
    Copy-Item $RuntimeMiner $backup -Force
    Write-Host "Backup: $backup"
}

Copy-Item $SourceMiner $RuntimeMiner -Force
Write-Host "Installed miner version:"
& $RuntimeMiner --version

Write-Host "Starting $ServiceName..."
Start-Service -Name $ServiceName
Start-Sleep -Seconds 3

$state = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'"
$state | Select-Object Name, State, ProcessId, PathName | Format-List

Write-Host "QSDM miner runtime update complete."
Read-Host "Press Enter to close"
