param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputName = ""
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$SourceRoot = Join-Path $QsdmRoot "source"
$LocalRoot = Join-Path $SourceRoot ".cache\local-validator"
$CacheRoot = Join-Path $SourceRoot ".cache\go-local-sqlite"
New-Item -ItemType Directory -Force -Path $LocalRoot, $CacheRoot | Out-Null

$runningExecutablePaths = @(
    Get-Process -ErrorAction SilentlyContinue |
        Where-Object { $_.Path } |
        ForEach-Object { $_.Path.ToLowerInvariant() }
)

if ([string]::IsNullOrWhiteSpace($OutputName)) {
    $OutputName = "qsdm-local-validator-sqlite.staged-$((Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ'))-$PID.exe"
}
$OutPath = Join-Path $LocalRoot $OutputName
if ($runningExecutablePaths -contains $OutPath.ToLowerInvariant()) {
    throw "Refusing to overwrite the running validator binary: $OutPath"
}
Remove-Item -LiteralPath $OutPath -Force -ErrorAction SilentlyContinue

$mingwBin = "E:\msys64\mingw64\bin"
if (Test-Path -LiteralPath (Join-Path $mingwBin "gcc.exe")) {
    $env:PATH = "$mingwBin;$env:PATH"
    $env:CC = Join-Path $mingwBin "gcc.exe"
}

$goBin = "C:\Program Files\Go\bin"
if (Test-Path -LiteralPath (Join-Path $goBin "go.exe")) {
    $env:PATH = "$goBin;$env:PATH"
}

$env:CGO_ENABLED = "1"
Remove-Item Env:\CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CPPFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CXXFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
$env:GOCACHE = Join-Path $CacheRoot "build"
Remove-Item Env:\GOTMPDIR -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null

$gitSha = "local"
try {
    $gitSha = (& git -C $QsdmRoot rev-parse --short HEAD 2>$null).Trim()
    if ([string]::IsNullOrWhiteSpace($gitSha)) {
        $gitSha = "local"
    }
} catch {
    $gitSha = "local"
}
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-X github.com/blackbeardONE/QSDM/pkg/buildinfo.Version=local-sqlite -X github.com/blackbeardONE/QSDM/pkg/buildinfo.GitSHA=$gitSha -X github.com/blackbeardONE/QSDM/pkg/buildinfo.BuildDate=$buildDate"

Write-Host "Building SQLite-capable local validator..." -ForegroundColor Cyan
Write-Host "Output: $OutPath"
Write-Host "CGO_ENABLED=$env:CGO_ENABLED"
Write-Host "CC=$env:CC"
Write-Host "Tags: dilithium_circl"

Push-Location $SourceRoot
try {
    & go build -tags dilithium_circl -ldflags $ldflags -o $OutPath .\cmd\qsdm
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }
} finally {
    Pop-Location
}

$built = Get-Item -LiteralPath $OutPath
if ($built.Length -lt 1MB) {
    throw "Built validator is unexpectedly small: $($built.Length) bytes"
}

Write-Host "Built $OutPath" -ForegroundColor Green
