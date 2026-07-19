[CmdletBinding()]
param(
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $PSScriptRoot 'dist'
}
$manifestPath = Join-Path $PSScriptRoot 'manifest.json'
$manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
$version = [string]$manifest.version
if ($version -notmatch '^\d+\.\d+\.\d+$') {
    throw "Extension manifest has an invalid version: $version"
}

$packageFiles = @(
    'manifest.json',
    'background.js',
    'content.js',
    'provider.js',
    'popup.html',
    'popup.css',
    'popup.js',
    'qsdm-hive-icon.png'
)

$stage = Join-Path ([System.IO.Path]::GetTempPath()) "qsdm-hive-wallet-extension-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $stage | Out-Null
try {
    foreach ($file in $packageFiles) {
        $source = Join-Path $PSScriptRoot $file
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "Extension package input is missing: $source"
        }
        Copy-Item -LiteralPath $source -Destination (Join-Path $stage $file)
    }

    New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
    $archive = Join-Path $OutputDirectory "qsdm-hive-wallet-extension-$version.zip"
    Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $archive -CompressionLevel Optimal
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
    [pscustomobject]@{
        Path = $archive
        Version = $version
        Sha256 = $hash
    }
} finally {
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
}
