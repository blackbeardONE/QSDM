# Verify tooling for building QSDM (run from repo: QSDM/scripts or QSDM+ root).
# QSDM requires the standard Go toolchain (1.20+). TinyGo is optional for WASM targets only.

$ErrorActionPreference = "Continue"
Write-Host "=== QSDM toolchain check ===" -ForegroundColor Cyan

# Prefer a full Go SDK if MSYS/MinGW ships a trimmed `go` that requires GOROOT.
$officialGoRoot = "C:\Program Files\Go"
if (-not $env:GOROOT -and (Test-Path (Join-Path $officialGoRoot "bin\go.exe"))) {
    $env:GOROOT = $officialGoRoot
    $env:PATH = "$(Join-Path $officialGoRoot 'bin');$env:PATH"
    Write-Host "[INFO] Using full SDK at $officialGoRoot (prepended to PATH for this session)." -ForegroundColor DarkYellow
}

$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    Write-Host "[FAIL] go.exe not on PATH." -ForegroundColor Red
    Write-Host "       Install Go from https://go.dev/dl/ and use it to build QSDM/source." -ForegroundColor Yellow
    Write-Host "       TinyGo (e.g. C:\tinygo) is not a full GOROOT replacement for this module." -ForegroundColor Yellow
    exit 1
}

& go version
Write-Host "[OK] Go executable: $($goCmd.Source)" -ForegroundColor Green

$srcRoot = Resolve-Path (Join-Path $PSScriptRoot "..\source") -ErrorAction SilentlyContinue
if (-not $srcRoot -or -not (Test-Path (Join-Path $srcRoot.Path "go.mod"))) {
    Write-Host "[WARN] ../source/go.mod not found from scripts dir." -ForegroundColor Yellow
} else {
    Push-Location $srcRoot.Path
    Write-Host "`n--- go env (GOROOT / GOTOOLCHAIN) ---" -ForegroundColor Cyan
    go env GOROOT GOTOOLCHAIN
    Write-Host "`n--- compile check (CGO_ENABLED=0) ---" -ForegroundColor Cyan
    $outExe = Join-Path $env:TEMP "qsdm-toolcheck.exe"
    $env:CGO_ENABLED = "0"
    go build -o $outExe ./cmd/qsdmplus 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[WARN] go build ./cmd/qsdmplus failed." -ForegroundColor Yellow
    } else {
        Write-Host "[OK] go build ./cmd/qsdmplus" -ForegroundColor Green
        Remove-Item $outExe -ErrorAction SilentlyContinue
    }
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

if (Test-Path "C:\tinygo") {
    Write-Host "`n[INFO] C:\tinygo present — fine for TinyGo workflows; keep official Go on PATH for QSDM." -ForegroundColor DarkYellow
}

Write-Host "`nDone." -ForegroundColor Cyan
