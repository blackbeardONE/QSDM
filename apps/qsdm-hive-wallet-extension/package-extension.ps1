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
$utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
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
    'home.html',
    'home.css',
    'home.js',
    'qsdm-hive-icon.png'
)

function New-DeterministicArchive {
    param(
        [Parameter(Mandatory = $true)][string]$Stage,
        [Parameter(Mandatory = $true)][string]$Archive
    )
    Remove-Item -LiteralPath $Archive -Force -ErrorAction SilentlyContinue
    # Compress-Archive records checkout-specific timestamps, so identical
    # extension payloads can produce different immutable release bytes.
    # Store sorted entries with a fixed ZIP timestamp for reproducible builds.
    $archiveStream = [IO.File]::Open(
        $Archive,
        [IO.FileMode]::CreateNew,
        [IO.FileAccess]::ReadWrite,
        [IO.FileShare]::None
    )
    $zip = [IO.Compression.ZipArchive]::new(
        $archiveStream,
        [IO.Compression.ZipArchiveMode]::Create,
        $false
    )
    try {
        $fixedTimestamp = [DateTimeOffset]::new(
            2000,
            1,
            1,
            0,
            0,
            0,
            [TimeSpan]::Zero
        )
        foreach ($file in @($packageFiles | Sort-Object)) {
            $entry = $zip.CreateEntry(
                $file,
                [IO.Compression.CompressionLevel]::NoCompression
            )
            $entry.LastWriteTime = $fixedTimestamp
            $sourceStream = [IO.File]::OpenRead((Join-Path $Stage $file))
            $entryStream = $entry.Open()
            try {
                $sourceStream.CopyTo($entryStream)
            } finally {
                $entryStream.Dispose()
                $sourceStream.Dispose()
            }
        }
    } finally {
        $zip.Dispose()
        $archiveStream.Dispose()
    }
}

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
    $archives = @()

    $universalArchive = Join-Path $OutputDirectory "qsdm-hive-wallet-extension-$version.zip"
    New-DeterministicArchive -Stage $stage -Archive $universalArchive
    $archives += [pscustomobject]@{ Browser = 'Universal'; Path = $universalArchive }

    $chromiumManifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    $chromiumManifest.PSObject.Properties.Remove('browser_specific_settings')
    $chromiumManifest.background.PSObject.Properties.Remove('scripts')
    [System.IO.File]::WriteAllText(
        (Join-Path $stage 'manifest.json'),
        ($chromiumManifest | ConvertTo-Json -Depth 12),
        $utf8WithoutBom
    )
    $chromiumArchive = Join-Path $OutputDirectory "qsdm-hive-wallet-extension-$version-chromium.zip"
    New-DeterministicArchive -Stage $stage -Archive $chromiumArchive
    $archives += [pscustomobject]@{ Browser = 'Chromium'; Path = $chromiumArchive }

    $firefoxManifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    $firefoxManifest.PSObject.Properties.Remove('key')
    $firefoxManifest.background.PSObject.Properties.Remove('service_worker')
    [System.IO.File]::WriteAllText(
        (Join-Path $stage 'manifest.json'),
        ($firefoxManifest | ConvertTo-Json -Depth 12),
        $utf8WithoutBom
    )
    $firefoxArchive = Join-Path $OutputDirectory "qsdm-hive-wallet-extension-$version-firefox.zip"
    New-DeterministicArchive -Stage $stage -Archive $firefoxArchive
    $archives += [pscustomobject]@{ Browser = 'Firefox'; Path = $firefoxArchive }

    foreach ($item in $archives) {
        [pscustomobject]@{
            Browser = $item.Browser
            Path = $item.Path
            Version = $version
            Sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $item.Path).Hash.ToLowerInvariant()
        }
    }
} finally {
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
}
