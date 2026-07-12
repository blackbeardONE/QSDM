[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$UnpackedDirectory,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$unpackedRoot = (Resolve-Path -LiteralPath $UnpackedDirectory).Path
$outputRoot = [IO.Path]::GetFullPath($OutputDirectory)
$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
if (-not $outputRoot.StartsWith($workspace + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'SignPath payload output must remain inside the QSDM workspace.'
}

& (Join-Path $PSScriptRoot 'verify_hive_windows_metadata.ps1') -UnpackedDirectory $unpackedRoot

$hivePackage = Join-Path $workspace 'apps\qsdm-hive\qsdm-hive-main\release\app\package.json'
$version = (Get-Content -Raw $hivePackage | ConvertFrom-Json).version
$commit = (& git -C $workspace rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-f]{40}$') {
    throw 'Could not resolve the source commit for the SignPath payload.'
}

New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$stageRoot = Join-Path $outputRoot "stage-$([guid]::NewGuid().ToString('N'))"
$stageUnpacked = Join-Path $stageRoot 'win-unpacked'
$payloadPath = Join-Path $outputRoot "qsdm-hive-$version-windows-unpacked-unsigned.zip"
$manifestPath = Join-Path $outputRoot "qsdm-hive-$version-windows-unpacked-manifest.json"

try {
    New-Item -ItemType Directory -Force -Path $stageRoot | Out-Null
    Copy-Item -LiteralPath $unpackedRoot -Destination $stageUnpacked -Recurse

    $files = Get-ChildItem -LiteralPath $stageUnpacked -Recurse -File |
        Sort-Object FullName |
        ForEach-Object {
            [ordered]@{
                path = $_.FullName.Substring($stageRoot.Length + 1).Replace('\', '/')
                size = $_.Length
                sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
            }
        }

    $manifest = [ordered]@{
        schema = 'qsdm.signpath-input.v1'
        generated_at = (Get-Date).ToUniversalTime().ToString('o')
        version = $version
        commit = $commit
        unsigned = $true
        files = @($files)
    }
    $manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath -Encoding UTF8

    Remove-Item -LiteralPath $payloadPath -Force -ErrorAction SilentlyContinue
    Compress-Archive -LiteralPath $stageUnpacked -DestinationPath $payloadPath -CompressionLevel Optimal
}
finally {
    if (Test-Path -LiteralPath $stageRoot) {
        Remove-Item -LiteralPath $stageRoot -Recurse -Force
    }
}

Write-Host "Prepared unsigned SignPath input: $payloadPath"
Write-Host "Manifest: $manifestPath"
