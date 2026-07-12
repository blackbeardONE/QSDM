[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InstallerPath,

    [Parameter(Mandatory = $true)]
    [string]$SignedUnpackedDirectory,

    [string]$SevenZipPath,

    [string]$EvidencePath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$installer = (Resolve-Path -LiteralPath $InstallerPath).Path
$signedRoot = (Resolve-Path -LiteralPath $SignedUnpackedDirectory).Path

if (-not $SevenZipPath) {
    $bundledSevenZip = Join-Path $workspace `
        'apps\qsdm-hive\qsdm-hive-main\node_modules\7zip-bin\win\x64\7za.exe'
    $sevenZipCommand = Get-Command 7z.exe, 7za.exe -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (Test-Path -LiteralPath $bundledSevenZip -PathType Leaf) {
        $SevenZipPath = $bundledSevenZip
    } elseif ($sevenZipCommand) {
        $SevenZipPath = $sevenZipCommand.Source
    }
}
if (-not $SevenZipPath -or -not (Test-Path -LiteralPath $SevenZipPath -PathType Leaf)) {
    throw '7-Zip is required to verify the application payload embedded in the NSIS installer.'
}
$sevenZip = (Resolve-Path -LiteralPath $SevenZipPath).Path

$requiredFiles = @(
    'QSDM Hive.exe'
    'resources\edge\qsdm-edge-agent.exe'
    'resources\edge\qsdm-edge-control.exe'
    'resources\edge\qsdm-edge-gpu-helper.exe'
    'resources\native\qsdmcli.exe'
    'resources\miner\qsdmminer-console.exe'
    'resources\miner\qsdm-miner-cuda-solver.exe'
)

$cacheRoot = Join-Path $workspace '.cache'
New-Item -ItemType Directory -Force -Path $cacheRoot | Out-Null
$extractRoot = Join-Path $cacheRoot "nsis-payload-$([guid]::NewGuid().ToString('N'))"

try {
    New-Item -ItemType Directory -Force -Path $extractRoot | Out-Null
    $extractArguments = @(
        'x'
        $installer
        "-o$extractRoot"
        '-y'
    ) + $requiredFiles

    & $sevenZip @extractArguments | Out-Host
    if ($LASTEXITCODE -gt 1) {
        throw "7-Zip failed to inspect the NSIS payload with exit code $LASTEXITCODE."
    }

    $evidenceFiles = foreach ($relativePath in $requiredFiles) {
        $signedPath = Join-Path $signedRoot $relativePath
        $embeddedPath = Join-Path $extractRoot $relativePath
        if (-not (Test-Path -LiteralPath $signedPath -PathType Leaf)) {
            throw "Signed source executable is missing: $signedPath"
        }
        if (-not (Test-Path -LiteralPath $embeddedPath -PathType Leaf)) {
            throw "NSIS installer does not contain the required executable: $relativePath"
        }

        $signedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $signedPath).Hash.ToLowerInvariant()
        $embeddedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $embeddedPath).Hash.ToLowerInvariant()
        if ($signedHash -cne $embeddedHash) {
            throw "NSIS payload differs from the signed source executable: $relativePath"
        }

        [ordered]@{
            path = $relativePath.Replace('\', '/')
            sha256 = $signedHash
        }
    }
}
finally {
    if (Test-Path -LiteralPath $extractRoot) {
        Remove-Item -LiteralPath $extractRoot -Recurse -Force
    }
}

if ([string]::IsNullOrWhiteSpace($EvidencePath)) {
    $EvidencePath = Join-Path (Split-Path $installer -Parent) 'windows-nsis-payload-evidence.json'
}
$evidenceParent = Split-Path -Parent $EvidencePath
if ($evidenceParent) {
    New-Item -ItemType Directory -Force -Path $evidenceParent | Out-Null
}

$evidence = [ordered]@{
    schema = 'qsdm.windows-nsis-payload-evidence.v1'
    generated_at = (Get-Date).ToUniversalTime().ToString('o')
    installer = [ordered]@{
        name = Split-Path -Leaf $installer
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $installer).Hash.ToLowerInvariant()
    }
    files = @($evidenceFiles)
}
$evidence | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $EvidencePath -Encoding UTF8

Write-Host "Verified $($requiredFiles.Count) embedded NSIS executables against the signed Hive payload."
Write-Host "Evidence: $EvidencePath"
