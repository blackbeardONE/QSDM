# Run cmd/migrate CGO tests. Requires liboqs at QSDM/liboqs_install (same as CI / rebuild_liboqs.sh),
# because CGO=1 compiles pkg/crypto (dilithium) as a transitive dependency of storage.
# Clears unrelated CGO_CFLAGS (e.g. wasmer) then applies liboqs flags.
# From monorepo root: pwsh -File QSDM/scripts/go-test-migrate-cgo.ps1

$ErrorActionPreference = 'Stop'
$QsdmRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$SourceDir = Join-Path $QsdmRoot 'source'
$LibRoot = Join-Path $QsdmRoot 'liboqs_install'
$include = Join-Path $LibRoot 'include'
if (-not (Test-Path (Join-Path $include 'oqs/oqs.h'))) {
    Write-Error "liboqs headers not found under $LibRoot. Build with: bash QSDM/scripts/rebuild_liboqs.sh (from QSDM/) or copy install to liboqs_install."
    exit 1
}
$libDir = if (Test-Path (Join-Path $LibRoot 'lib64')) { Join-Path $LibRoot 'lib64' } else { Join-Path $LibRoot 'lib' }

Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:CGO_LDFLAGS -ErrorAction SilentlyContinue
$env:CGO_ENABLED = '1'
$env:QSDMPLUS_METRICS_REGISTER_STRICT = '1'
$env:CGO_CFLAGS = "-I$include"
$env:CGO_LDFLAGS = "-L$libDir -loqs"
if ($env:OS -match 'Windows') {
    $env:PATH = "$libDir;$env:PATH"
} else {
    if ($env:LD_LIBRARY_PATH) {
        $env:LD_LIBRARY_PATH = "$libDir" + [IO.Path]::PathSeparator + $env:LD_LIBRARY_PATH
    } else {
        $env:LD_LIBRARY_PATH = "$libDir"
    }
}

Push-Location $SourceDir
try {
    & go test ./cmd/migrate/... -count=1 -short -timeout 2m
    if ($LASTEXITCODE -ne 0) {
        throw "go test ./cmd/migrate/... failed (exit $LASTEXITCODE)"
    }
} finally {
    Pop-Location
}

Write-Host 'OK: go-test-migrate-cgo finished'
