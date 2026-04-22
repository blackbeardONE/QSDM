# Start a single-node Scylla in Docker (same flags as CI), wait until staging verify passes, then remove the container.
# Requires Docker Desktop (Linux engine). From monorepo root: pwsh -File QSDM/scripts/scylla-staging-verify-with-docker.ps1
# Optional env: SCYLLA_STAGING_CONTAINER_NAME (default qsdm-scylla-local), SCYLLA_IMAGE (default scylladb/scylla:6.2).

$ErrorActionPreference = "Stop"
$ScriptDir = $PSScriptRoot
$Name = if ($env:SCYLLA_STAGING_CONTAINER_NAME) { $env:SCYLLA_STAGING_CONTAINER_NAME } else { "qsdm-scylla-local" }
$Image = if ($env:SCYLLA_IMAGE) { $env:SCYLLA_IMAGE } else { "scylladb/scylla:6.2" }

function Cleanup {
    docker rm -f $Name 2>$null | Out-Null
}

docker ps *>$null
if ($LASTEXITCODE -ne 0) {
    Write-Error "Docker is not reachable. Start Docker Desktop (Linux engine) and retry."
    exit 1
}

docker rm -f $Name 2>$null | Out-Null

Write-Host "=== Starting $Image as $Name (9042) ===" -ForegroundColor Cyan
try {
    docker run -d --name $Name -p 9042:9042 $Image --smp 1 --memory 750M --overprovisioned 1 --developer-mode 1
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    if (-not $env:SCYLLA_HOSTS) { $env:SCYLLA_HOSTS = "127.0.0.1" }
    if (-not $env:SCYLLA_KEYSPACE) { $env:SCYLLA_KEYSPACE = "qsdm" }
    $env:SCYLLA_AUTO_CREATE_KEYSPACE = if ($env:SCYLLA_AUTO_CREATE_KEYSPACE) { $env:SCYLLA_AUTO_CREATE_KEYSPACE } else { "1" }

    $ok = $false
    for ($i = 1; $i -le 90; $i++) {
        & pwsh -NoProfile -File (Join-Path $ScriptDir "scylla-staging-verify.ps1")
        if ($LASTEXITCODE -eq 0) {
            $ok = $true
            break
        }
        Write-Host "staging verify attempt $i/90 failed, sleeping 5s..."
        Start-Sleep -Seconds 5
    }

    if (-not $ok) {
        Write-Error "Scylla staging verify did not succeed within retry budget"
        exit 1
    }

    Write-Host "=== scylla-staging-verify-with-docker OK ===" -ForegroundColor Green
} finally {
    Cleanup
}
