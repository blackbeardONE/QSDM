# Verifies Docker, optionally logs in to nvcr.io with NGC_CLI_API_KEY (never printed),
# and confirms registry access to the Dockerfile.ngc base image (manifest inspect = no layer download unless -Pull).
#
# Usage (from apps/qsdmplus-nvidia-ngc):
#   .\scripts\verify-ngc-docker.ps1
#   .\scripts\verify-ngc-docker.ps1 -NgcEnvPath .\ngc.env
#   .\scripts\verify-ngc-docker.ps1 -SkipLogin
#   .\scripts\verify-ngc-docker.ps1 -Pull   # full image download (large)

param(
    [string]$NgcEnvPath = "",
    [switch]$SkipLogin,
    [switch]$Pull
)

$ErrorActionPreference = "Stop"
$Image = "nvcr.io/nvidia/pytorch:24.07-py3"

function Read-NgcKeyFromFile {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        return $null
    }
    foreach ($line in Get-Content -LiteralPath $Path -ErrorAction Stop) {
        $t = $line.Trim()
        if ($t -match '^\s*#' -or $t -eq "") { continue }
        if ($t -match '^\s*NGC_CLI_API_KEY\s*=\s*(.+)$') {
            return $Matches[1].Trim().Trim('"').Trim("'")
        }
    }
    return $null
}

Write-Host "=== QSDM+ NGC / Docker verification ===" -ForegroundColor Cyan
Write-Host "Target image: $Image" -ForegroundColor Gray

try {
    $dv = docker version --format "{{.Client.Version}}" 2>$null
    if (-not $dv) { throw "no client" }
    Write-Host "[ok] Docker client: $dv" -ForegroundColor Green
} catch {
    Write-Host "[fail] Docker CLI not working. Install Docker Desktop / Engine and ensure it is running." -ForegroundColor Red
    exit 1
}

if (-not $SkipLogin) {
    $key = $env:NGC_CLI_API_KEY
    if ([string]::IsNullOrWhiteSpace($NgcEnvPath)) {
        $NgcEnvPath = Join-Path (Split-Path $PSScriptRoot -Parent) "ngc.env"
    } else {
        $NgcEnvPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($NgcEnvPath)
    }

    if ([string]::IsNullOrWhiteSpace($key)) {
        $key = Read-NgcKeyFromFile -Path $NgcEnvPath
    }

    if ([string]::IsNullOrWhiteSpace($key) -or $key -eq "replace-me") {
        Write-Host "[skip] No NGC_CLI_API_KEY (env or $NgcEnvPath). Using existing Docker nvcr.io credentials for manifest check..." -ForegroundColor Yellow
    } else {
        Write-Host "[..] docker login nvcr.io (key not shown)..." -ForegroundColor Gray
        $key | & docker login nvcr.io -u '$oauthtoken' --password-stdin
        if ($LASTEXITCODE -ne 0) {
            Write-Host "[fail] docker login nvcr.io failed (exit $LASTEXITCODE)" -ForegroundColor Red
            exit 1
        }
        Write-Host "[ok] Logged in to nvcr.io" -ForegroundColor Green
    }
}

if ($Pull) {
    Write-Host "[..] docker pull $Image (large download)..." -ForegroundColor Gray
    docker pull $Image
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[fail] docker pull failed" -ForegroundColor Red
        exit 1
    }
    Write-Host "[ok] Image pulled" -ForegroundColor Green
} else {
    Write-Host "[..] docker manifest inspect (metadata only)..." -ForegroundColor Gray
    docker manifest inspect $Image 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[fail] Cannot read manifest for $Image. Set NGC_CLI_API_KEY or ngc.env, or use -SkipLogin after manual login." -ForegroundColor Red
        exit 1
    }
    Write-Host "[ok] Registry allows access to $Image (same tag as Dockerfile.ngc)" -ForegroundColor Green
}

Write-Host "=== Done ===" -ForegroundColor Cyan
