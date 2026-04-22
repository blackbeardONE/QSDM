# GPU stack: gossip + validator-cpu + validator-gpu (NGC PyTorch / Dockerfile.ngc).
# Prerequisites: Docker, NVIDIA Container Toolkit, `docker login nvcr.io` (see verify-ngc-docker.ps1).
#
# Usage (from anywhere):
#   .\scripts\run-gpu.ps1              # foreground; Ctrl+C stops
#   .\scripts\run-gpu.ps1 -Detach     # background
#   .\scripts\run-gpu.ps1 -BuildOnly  # build images only

param(
    [switch]$Detach,
    [switch]$BuildOnly
)

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

$dockerArgs = @("compose", "--profile", "gpu")
if ($BuildOnly) {
    $dockerArgs += "build"
} else {
    $dockerArgs += "up", "--build"
    if ($Detach) { $dockerArgs += "-d" }
}

Write-Host "docker $($dockerArgs -join ' ')" -ForegroundColor Cyan
& docker @dockerArgs
exit $LASTEXITCODE
