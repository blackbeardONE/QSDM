param(
    [string]$HiveSourceDir = "apps/qsdm-hive/qsdm-hive-main",
    [string]$QsdmSourceDir = "QSDM/source",
    [string]$EdgeAgentVersion = "",
    [switch]$KeepGeneratedResource
)

$ErrorActionPreference = 'Stop'

$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$hive = (Resolve-Path (Join-Path $workspace $HiveSourceDir)).Path
$qsdm = (Resolve-Path (Join-Path $workspace $QsdmSourceDir)).Path
$native = Join-Path $hive 'native\windows\x64'
$goCache = Join-Path $workspace '.cache\go-build'
$officialGo = Join-Path $env:ProgramFiles 'Go\bin\go.exe'
$go = if (Test-Path -LiteralPath $officialGo) {
    $officialGo
} else {
    (Get-Command go -ErrorAction Stop).Source
}
$goRoot = Split-Path -Parent (Split-Path -Parent $go)
$version = (Get-Content -Raw (Join-Path $hive 'release\app\package.json') | ConvertFrom-Json).version
$edgeVersionFile = Join-Path $workspace 'apps\qsdm-edge-agent\VERSION'
if (-not $EdgeAgentVersion) {
    if (-not (Test-Path -LiteralPath $edgeVersionFile)) {
        throw "QSDM edge-agent version file not found at $edgeVersionFile"
    }
    $EdgeAgentVersion = (Get-Content -Raw -LiteralPath $edgeVersionFile).Trim()
}
if ($EdgeAgentVersion -notmatch '^\d+\.\d+\.\d+$') {
    throw 'EdgeAgentVersion must use MAJOR.MINOR.PATCH format.'
}
$gitSha = (& git -C $qsdm rev-parse --short HEAD).Trim()
$buildDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$buildInfo = "-s -w -X github.com/blackbeardONE/QSDM/pkg/buildinfo.Version=hive-v$version -X github.com/blackbeardONE/QSDM/pkg/buildinfo.GitSHA=$gitSha -X github.com/blackbeardONE/QSDM/pkg/buildinfo.BuildDate=$buildDate"
$controlIcon = Join-Path $hive 'assets\icon.ico'
$controlResourceBuilder = Join-Path $workspace 'QSDM\scripts\build_edge_control_windows_resource.ps1'
$controlResource = Join-Path $qsdm 'cmd\qsdm-edge-control\rsrc_windows_amd64.syso'

New-Item -ItemType Directory -Force -Path $native | Out-Null
New-Item -ItemType Directory -Force -Path $goCache | Out-Null

& $controlResourceBuilder -Version $EdgeAgentVersion -IconPath $controlIcon -OutputPath $controlResource
Push-Location $qsdm
try {
    if (Test-Path -LiteralPath (Join-Path $goRoot 'src')) {
        $env:GOROOT = $goRoot
    }
    $env:GOCACHE = $goCache
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'

    & $go build -trimpath -tags dilithium_circl -ldflags '-s -w' -o (Join-Path $native 'qsdmcli.exe') ./cmd/qsdmcli
    if ($LASTEXITCODE -ne 0) { throw "qsdmcli build failed with exit code $LASTEXITCODE" }

    & $go build -trimpath -ldflags $buildInfo -o (Join-Path $native 'qsdmminer-console.exe') ./cmd/qsdmminer-console
    if ($LASTEXITCODE -ne 0) { throw "qsdmminer-console build failed with exit code $LASTEXITCODE" }

    & $go build -trimpath -ldflags "-s -w -X main.version=$EdgeAgentVersion" -o (Join-Path $native 'qsdm-edge-agent.exe') ./cmd/qsdm-edge-agent
    if ($LASTEXITCODE -ne 0) { throw "qsdm-edge-agent build failed with exit code $LASTEXITCODE" }

    & $go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$EdgeAgentVersion" -o (Join-Path $native 'qsdm-edge-control.exe') ./cmd/qsdm-edge-control
    if ($LASTEXITCODE -ne 0) { throw "qsdm-edge-control build failed with exit code $LASTEXITCODE" }
}
finally {
    if (-not $KeepGeneratedResource) {
        for ($attempt = 0; $attempt -lt 12 -and (Test-Path -LiteralPath $controlResource); $attempt++) {
            Remove-Item -LiteralPath $controlResource -Force -ErrorAction SilentlyContinue
            if (Test-Path -LiteralPath $controlResource) {
                Start-Sleep -Milliseconds (($attempt + 1) * 100)
            }
        }
    }
    $resourceCleanupFailed = -not $KeepGeneratedResource -and (Test-Path -LiteralPath $controlResource)
    Pop-Location
    if ($resourceCleanupFailed) {
        throw "Unable to remove generated Edge Control resource: $controlResource"
    }
}

& (Join-Path $native 'qsdmminer-console.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Packaged qsdmminer-console failed its version probe.' }

& (Join-Path $native 'qsdm-edge-agent.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Packaged qsdm-edge-agent failed its version probe.' }

& (Join-Path $native 'qsdm-edge-control.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Packaged qsdm-edge-control failed its version probe.' }

Write-Host "Hive Windows native tools are ready in $native"
