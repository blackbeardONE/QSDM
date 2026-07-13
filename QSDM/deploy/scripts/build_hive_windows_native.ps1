param(
    [string]$HiveSourceDir = "apps/qsdm-hive/qsdm-hive-main",
    [string]$QsdmSourceDir = "QSDM/source",
    [string]$EdgeAgentVersion = "",
    [string]$GoExe = "",
    [switch]$KeepGeneratedResource,
    [switch]$SkipCudaRuntimeSelfTest
)

$ErrorActionPreference = 'Stop'

$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$hive = (Resolve-Path (Join-Path $workspace $HiveSourceDir)).Path
$qsdm = (Resolve-Path (Join-Path $workspace $QsdmSourceDir)).Path
$native = Join-Path $hive 'native\windows\x64'
$goCache = Join-Path $workspace '.cache\go-build'
$goModCache = Join-Path $workspace '.cache\go-mod'
$officialGo = Join-Path $env:ProgramFiles 'Go\bin\go.exe'
$goOverride = if ($GoExe) { $GoExe } else { $env:QSDM_GO_EXE }
$go = if ($goOverride) {
    if (-not (Test-Path -LiteralPath $goOverride -PathType Leaf)) {
        throw "Configured Go executable was not found: $goOverride"
    }
    (Resolve-Path -LiteralPath $goOverride).Path
} elseif (Test-Path -LiteralPath $officialGo) {
    $officialGo
} else {
    (Get-Command go -ErrorAction Stop).Source
}
$requiredGoLine = Get-Content -LiteralPath (Join-Path $qsdm 'go.mod') |
    Where-Object { $_ -match '^go\s+(\d+\.\d+\.\d+)\s*$' } |
    Select-Object -First 1
if (-not $requiredGoLine) {
    throw 'QSDM go.mod does not contain a MAJOR.MINOR.PATCH go directive.'
}
$requiredGo = [version]([regex]::Match($requiredGoLine, '^go\s+(\d+\.\d+\.\d+)\s*$').Groups[1].Value)
$env:GOTOOLCHAIN = 'auto'
$env:GOMODCACHE = $goModCache
New-Item -ItemType Directory -Force -Path $goModCache | Out-Null
Push-Location $qsdm
try {
    $goVersionOutput = (& $go env GOVERSION).Trim()
    if ($LASTEXITCODE -ne 0 -or $goVersionOutput -notmatch '^go(\d+\.\d+\.\d+)$') {
        throw "Unable to select the Go toolchain required by $qsdm\go.mod using $go"
    }
    $goVersion = [version]$Matches[1]
}
finally {
    Pop-Location
}
if ($goVersion -lt $requiredGo) {
    throw "Go $requiredGo or newer is required; automatic toolchain selection returned $goVersion from $go. Set QSDM_GO_EXE to a current Go SDK."
}
Write-Host "Using $goVersionOutput through $go"
$version = (Get-Content -Raw (Join-Path $hive 'release\app\package.json') | ConvertFrom-Json).version
if ($version -notmatch '^(\d+\.\d+\.\d+)(?:-[0-9A-Za-z.-]+)?$') {
    throw 'Hive version must use SemVer MAJOR.MINOR.PATCH with an optional prerelease suffix.'
}
$binaryVersion = $Matches[1]
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
$versionResourceBuilder = Join-Path $workspace 'QSDM\scripts\build_windows_version_resource.ps1'
$cudaSolverBuilder = Join-Path $workspace 'QSDM\scripts\build_miner_cuda.ps1'
$edgeGpuBuilder = Join-Path $workspace 'QSDM\scripts\build_edge_gpu_helper.ps1'
$cudaSolver = Join-Path $native 'qsdm-miner-cuda-solver.exe'
$edgeGpuHelper = Join-Path $native 'qsdm-edge-gpu-helper.exe'

$resourceSpecs = @(
    @{
        Path = Join-Path $qsdm 'cmd\qsdmcli\rsrc_windows_amd64.syso'
        FileVersion = $binaryVersion
        Description = 'QSDM Command Line Interface'
        InternalName = 'qsdmcli'
        OriginalFilename = 'qsdmcli.exe'
    },
    @{
        Path = Join-Path $qsdm 'cmd\qsdmminer-console\rsrc_windows_amd64.syso'
        FileVersion = $binaryVersion
        Description = 'QSDM Console Miner'
        InternalName = 'qsdmminer-console'
        OriginalFilename = 'qsdmminer-console.exe'
    },
    @{
        Path = Join-Path $qsdm 'cmd\qsdm-edge-agent\rsrc_windows_amd64.syso'
        FileVersion = $EdgeAgentVersion
        Description = 'QSDM Edge Agent'
        InternalName = 'qsdm-edge-agent'
        OriginalFilename = 'qsdm-edge-agent.exe'
    },
    @{
        Path = Join-Path $qsdm 'cmd\qsdm-edge-control\rsrc_windows_amd64.syso'
        FileVersion = $EdgeAgentVersion
        Description = 'QSDM Edge Control'
        InternalName = 'qsdm-edge-control'
        OriginalFilename = 'qsdm-edge-control.exe'
    }
)

New-Item -ItemType Directory -Force -Path $native | Out-Null
New-Item -ItemType Directory -Force -Path $goCache | Out-Null

foreach ($resource in $resourceSpecs) {
    & $versionResourceBuilder `
        -ProductVersion $binaryVersion `
        -FileVersion $resource.FileVersion `
        -ProductName 'QSDM Hive' `
        -FileDescription $resource.Description `
        -InternalName $resource.InternalName `
        -OriginalFilename $resource.OriginalFilename `
        -IconPath $controlIcon `
        -OutputPath $resource.Path
}
Push-Location $qsdm
try {
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
    $resourceCleanupFailed = @()
    if (-not $KeepGeneratedResource) {
        foreach ($resource in $resourceSpecs) {
            for ($attempt = 0; $attempt -lt 30 -and (Test-Path -LiteralPath $resource.Path); $attempt++) {
                Remove-Item -LiteralPath $resource.Path -Force -ErrorAction SilentlyContinue
                if (Test-Path -LiteralPath $resource.Path) {
                    Start-Sleep -Milliseconds ([Math]::Min(($attempt + 1) * 100, 1000))
                }
            }
            if (Test-Path -LiteralPath $resource.Path) {
                $resourceCleanupFailed += $resource.Path
            }
        }
    }
    Pop-Location
    if ($resourceCleanupFailed.Count -gt 0) {
        Write-Warning "Windows retained generated version resources after cleanup retries. They are ignored by git and will be overwritten by the next build: $($resourceCleanupFailed -join ', ')"
    }
}

$cudaArguments = @{
    Version = $binaryVersion
    SkipRuntimeSelfTest = [bool]$SkipCudaRuntimeSelfTest
}
& $cudaSolverBuilder @cudaArguments
if ($LASTEXITCODE -ne 0) { throw 'QSDM CUDA miner solver build failed.' }

& $edgeGpuBuilder @cudaArguments
if ($LASTEXITCODE -ne 0) { throw 'QSDM Edge GPU Helper build failed.' }

& (Join-Path $native 'qsdmminer-console.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Packaged qsdmminer-console failed its version probe.' }

if (-not $SkipCudaRuntimeSelfTest) {
    & $cudaSolver --self-test
    if ($LASTEXITCODE -ne 0) { throw 'Packaged QSDM CUDA miner solver failed its self-test.' }

    $gpuResult = & $edgeGpuHelper --seed ('00' * 32) --units 1024 --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or -not $gpuResult.gpu_name) {
        throw 'Packaged QSDM Edge GPU Helper failed its runtime self-test.'
    }
}

& (Join-Path $native 'qsdm-edge-agent.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Packaged qsdm-edge-agent failed its version probe.' }

& (Join-Path $native 'qsdm-edge-control.exe') --version
if ($LASTEXITCODE -ne 0) { throw 'Packaged qsdm-edge-control failed its version probe.' }

Write-Host "Hive Windows native tools are ready in $native"
