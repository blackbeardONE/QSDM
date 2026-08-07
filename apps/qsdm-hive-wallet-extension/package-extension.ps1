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

    # Chrome Web Store assigns the published extension ID and rejects a
    # manifest that carries the development-only key used by Load unpacked.
    $storeManifestPath = Join-Path $stage 'manifest.json'
    $storeManifest = Get-Content -Raw -LiteralPath $storeManifestPath | ConvertFrom-Json
    $storeManifest.PSObject.Properties.Remove('key')
    foreach ($contentScript in $storeManifest.content_scripts) {
        $contentScript.matches = @(
            $contentScript.matches | Where-Object {
                $_ -notin @('http://localhost/*', 'http://127.0.0.1/*')
            }
        )
    }
    $storeManifestJson = $storeManifest | ConvertTo-Json -Depth 20
    $utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText(
        $storeManifestPath,
        "$storeManifestJson$([Environment]::NewLine)",
        $utf8WithoutBom
    )

    $packagedManifest = Get-Content -Raw -LiteralPath $storeManifestPath | ConvertFrom-Json
    if ($null -ne $packagedManifest.PSObject.Properties['key']) {
        throw 'Chrome Web Store package must not contain manifest.key.'
    }
    $broadHostPatterns = @('<all_urls>', '*://*/*', 'http://*/*', 'https://*/*')
    $packagedHostPatterns = @(
        @($packagedManifest.host_permissions) +
        @($packagedManifest.optional_host_permissions) +
        @($packagedManifest.content_scripts | ForEach-Object { @($_.matches) })
    )
    $unexpectedBroadHosts = @(
        $packagedHostPatterns | Where-Object { $_ -in $broadHostPatterns }
    )
    if ($unexpectedBroadHosts.Count -gt 0) {
        throw "Chrome Web Store package contains broad host access: $($unexpectedBroadHosts -join ', ')"
    }
    $developmentOnlyHosts = @('http://localhost/*', 'http://127.0.0.1/*')
    $unexpectedDevelopmentHosts = @(
        $packagedHostPatterns | Where-Object { $_ -in $developmentOnlyHosts }
    )
    if ($unexpectedDevelopmentHosts.Count -gt 0) {
        throw "Chrome Web Store package contains development hosts: $($unexpectedDevelopmentHosts -join ', ')"
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
