# Build cmd/qsdm with CGO off (same CGO cleanup as go-test-short-no-cgo.ps1).
param(
    [string]$OutputPath = "qsdm.exe"
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$sourceDir = Join-Path $repoRoot 'source'
if (-not (Test-Path $sourceDir)) {
    throw "Expected module root at $sourceDir"
}

$goExe = $null
$candidates = @(
    "${env:ProgramFiles}\Go\bin\go.exe",
    "${env:ProgramFiles(x86)}\Go\bin\go.exe"
)
foreach ($c in $candidates) {
    if (Test-Path $c) { $goExe = $c; break }
}
if (-not $goExe) {
    $cmdGo = Get-Command go -ErrorAction SilentlyContinue
    if ($cmdGo) { $goExe = $cmdGo.Source }
}
if (-not $goExe) { throw 'go.exe not found; install Go or add it to PATH.' }

$gorootCandidate = Split-Path (Split-Path $goExe -Parent) -Parent
if (Test-Path (Join-Path $gorootCandidate 'src\internal')) {
    $env:GOROOT = $gorootCandidate
}

$env:CGO_ENABLED = '0'
Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:CGO_LDFLAGS -ErrorAction SilentlyContinue

Push-Location $sourceDir
try {
    & $goExe build -o $OutputPath ./cmd/qsdm
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
