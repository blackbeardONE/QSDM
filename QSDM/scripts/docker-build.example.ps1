# Build the QSDM image. Run: .\scripts\docker-build.example.ps1
# Optional: pass extra docker build args after the script name.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$dockerfile = Join-Path $root 'Dockerfile'
# Context last; extra args before context (e.g. --no-cache).
docker build -t qsdm:latest -f $dockerfile @args $root
