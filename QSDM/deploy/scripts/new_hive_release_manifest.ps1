param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('windows', 'linux')]
    [string]$Platform,

    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$DownloadsDirectory,

    [string]$SigningDirectory = "",
    [string]$QsdmCliPath = "",
    [string]$Commit = "",
    [string]$WalletExtensionVersion = "",
    [ValidateRange(1, 120)]
    [int]$ValidDays = 90
)

$ErrorActionPreference = 'Stop'

# Keep this vocabulary compatible with already-released Hive clients. New
# artifact kinds must use one of these generic authenticated roles until a
# client that understands a new role has been deployed everywhere.
$supportedArtifactRoles = @(
    'updater-manifest',
    'installer',
    'blockmap',
    'portable-archive',
    'checksums',
    'provenance',
    'evidence'
)

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'Release signing currently requires the Windows user who owns the DPAPI-protected key.'
}

if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw 'Version must use MAJOR.MINOR.PATCH format.'
}

$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
if (-not $WalletExtensionVersion) {
    $walletExtensionManifestPath = Join-Path $workspace 'apps\qsdm-hive-wallet-extension\manifest.json'
    if (-not (Test-Path -LiteralPath $walletExtensionManifestPath -PathType Leaf)) {
        throw "Wallet extension manifest is missing: $walletExtensionManifestPath"
    }
    $WalletExtensionVersion = [string](
        Get-Content -Raw -LiteralPath $walletExtensionManifestPath | ConvertFrom-Json
    ).version
}
if ($WalletExtensionVersion -notmatch '^\d+\.\d+\.\d+$') {
    throw 'WalletExtensionVersion must use MAJOR.MINOR.PATCH format.'
}
$DownloadsDirectory = (Resolve-Path -LiteralPath $DownloadsDirectory).Path
if (-not $SigningDirectory) {
    $SigningDirectory = Join-Path $workspace '.cache\qsdm-release-signing'
}
$SigningDirectory = (Resolve-Path -LiteralPath $SigningDirectory).Path
$keystorePath = Join-Path $SigningDirectory 'release-signing-wallet.json'
$protectedPassphrasePath = Join-Path $SigningDirectory 'release-signing-passphrase.dpapi'
$publicMetadataPath = Join-Path $SigningDirectory 'release-signing-public.json'
$pinnedTrustKeyPath = Join-Path $workspace 'QSDM\deploy\release-trust\qsdm-hive-release-key.json'

foreach ($path in @(
    $keystorePath,
    $protectedPassphrasePath,
    $publicMetadataPath,
    $pinnedTrustKeyPath
)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Release-signing input is missing: $path"
    }
}

if (-not $QsdmCliPath) {
    $candidatePaths = @(
        (Join-Path $workspace 'apps\qsdm-hive\qsdm-hive-main\native\windows\x64\qsdmcli.exe'),
        (Join-Path $SigningDirectory 'qsdmcli.exe'),
        (Join-Path $workspace 'QSDM\source\.cache\local-validator\qsdmcli.exe')
    )
    $QsdmCliPath = $candidatePaths |
        Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } |
        Select-Object -First 1
}
if (-not $QsdmCliPath -or -not (Test-Path -LiteralPath $QsdmCliPath -PathType Leaf)) {
    throw 'qsdmcli was not found. Build it first or pass -QsdmCliPath.'
}
$QsdmCliPath = (Resolve-Path -LiteralPath $QsdmCliPath).Path

if (-not $Commit) {
    $Commit = (& git -C $workspace rev-parse HEAD).Trim()
}
if ($Commit -notmatch '^[0-9a-fA-F]{40}$') {
    throw 'Commit must be a full 40-character Git commit hash.'
}

$publicMetadata = Get-Content -Raw -LiteralPath $publicMetadataPath | ConvertFrom-Json
$pinnedTrustKey = Get-Content -Raw -LiteralPath $pinnedTrustKeyPath | ConvertFrom-Json
if ($publicMetadata.schema -ne 'qsdm.release-trust-key.v1' -or
    $publicMetadata.algorithm -ne 'ML-DSA-87' -or
    [string]$publicMetadata.key_id -notmatch '^[0-9a-f]{64}$') {
    throw 'Release-signing public metadata is invalid.'
}
if ($pinnedTrustKey.schema -ne 'qsdm.release-trust-key.v1' -or
    $pinnedTrustKey.algorithm -ne 'ML-DSA-87' -or
    [string]$pinnedTrustKey.key_id -ne [string]$publicMetadata.key_id -or
    [string]$pinnedTrustKey.public_key -ne [string]$publicMetadata.public_key) {
    throw 'Release-signing key does not match the public trust root pinned in QSDM Hive.'
}

if ($Platform -eq 'windows') {
    $manifestName = 'qsdm-hive-release-windows.json'
    $specs = @(
        @{ Name = 'latest.yml'; Role = 'updater-manifest'; Required = $true },
        @{ Name = "qsdm-hive-$Version-win-x64.exe"; Role = 'installer'; Required = $true },
        @{ Name = "qsdm-hive-$Version-win-x64.exe.blockmap"; Role = 'blockmap'; Required = $true },
        @{ Name = 'SHA256SUMS-win.txt'; Role = 'checksums'; Required = $true },
        @{ Name = "qsdm-hive-$Version-release-provenance.json"; Role = 'provenance'; Required = $false },
        @{ Name = "qsdm-hive-$Version-windows-metadata-evidence.json"; Role = 'evidence'; Required = $false },
        @{ Name = "qsdm-hive-$Version-windows-nsis-evidence.json"; Role = 'evidence'; Required = $false },
        @{ Name = "qsdm-hive-wallet-extension-$WalletExtensionVersion.zip"; Role = 'portable-archive'; Required = $true },
        @{ Name = "qsdm-hive-wallet-extension-$WalletExtensionVersion-SHA256SUMS.txt"; Role = 'checksums'; Required = $true }
    )
} else {
    $manifestName = 'qsdm-hive-release-linux.json'
    $specs = @(
        @{ Name = 'latest-linux.yml'; Role = 'updater-manifest'; Required = $true },
        @{ Name = "qsdm-hive-$Version-linux-x86_64.AppImage"; Role = 'installer'; Required = $true },
        @{ Name = "qsdm-hive-$Version-linux-x64.tar.gz"; Role = 'portable-archive'; Required = $true },
        @{ Name = "qsdm-hive-$Version-linux-SHA256SUMS.txt"; Role = 'checksums'; Required = $true },
        @{ Name = "qsdm-hive-$Version-linux-release-provenance.json"; Role = 'provenance'; Required = $false },
        @{ Name = "qsdm-hive-$Version-linux-payload-evidence.json"; Role = 'evidence'; Required = $false }
    )
}

$unsupportedSpecs = @($specs | Where-Object {
    [string]$_.Role -notin $supportedArtifactRoles
})
if ($unsupportedSpecs.Count -gt 0) {
    $unsupportedRoles = @($unsupportedSpecs | ForEach-Object {
        [string]$_.Role
    } | Sort-Object -Unique) -join ', '
    throw "Release manifest contains roles unsupported by released Hive clients: $unsupportedRoles"
}

$updaterMetadataPath = Join-Path $DownloadsDirectory $specs[0].Name
$updaterMetadata = Get-Content -Raw -LiteralPath $updaterMetadataPath
$expectedInstaller = [string]$specs[1].Name
$versionPattern = '(?m)^version:\s*' + [Regex]::Escape($Version) + '\s*$'
$pathPattern = '(?m)^path:\s*' + [Regex]::Escape($expectedInstaller) + '\s*$'
$urlPattern = '(?m)^\s*-\s*url:\s*' + [Regex]::Escape($expectedInstaller) + '\s*$'
if ($updaterMetadata -notmatch $versionPattern -or
    ($updaterMetadata -notmatch $pathPattern -and
     $updaterMetadata -notmatch $urlPattern)) {
    throw 'Updater metadata does not match the release version and installer being signed.'
}

$artifacts = @()
foreach ($spec in $specs) {
    $artifactPath = Join-Path $DownloadsDirectory $spec.Name
    if (-not (Test-Path -LiteralPath $artifactPath -PathType Leaf)) {
        if ($spec.Required) {
            throw "Required release artifact is missing: $artifactPath"
        }
        continue
    }
    $file = Get-Item -LiteralPath $artifactPath
    $artifacts += [ordered]@{
        name = $spec.Name
        platform = $Platform
        role = $spec.Role
        size = [long]$file.Length
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $artifactPath).Hash.ToLowerInvariant()
    }
}
$artifacts = @($artifacts | Sort-Object { $_.name })

$issuedAt = (Get-Date).ToUniversalTime()
$manifest = [ordered]@{
    schema = 'qsdm.release-manifest.v1'
    product = 'qsdm-hive'
    channel = 'stable'
    platform = $Platform
    version = $Version
    commit = $Commit.ToLowerInvariant()
    issued_at = $issuedAt.ToString('o')
    expires_at = $issuedAt.AddDays($ValidDays).ToString('o')
    key_id = ([string]$publicMetadata.key_id).ToLowerInvariant()
    artifacts = $artifacts
}

$manifestPath = Join-Path $DownloadsDirectory $manifestName
$payloadPath = Join-Path $SigningDirectory ('.manifest-' + [Guid]::NewGuid().ToString('N') + '.tmp')
$manifestJson = ($manifest | ConvertTo-Json -Depth 8) + [Environment]::NewLine
[IO.File]::WriteAllText($payloadPath, $manifestJson, [Text.UTF8Encoding]::new($false))

$temporaryPassphrasePath = Join-Path $SigningDirectory ('.passphrase-' + [Guid]::NewGuid().ToString('N') + '.tmp')
try {
    $protectedPassphrase = (
        Get-Content -Raw -LiteralPath $protectedPassphrasePath
    ).Trim()
    $securePassphrase = ConvertTo-SecureString $protectedPassphrase
    $credential = [Management.Automation.PSCredential]::new('qsdm-release', $securePassphrase)
    $plainPassphrase = $credential.GetNetworkCredential().Password
    [IO.File]::WriteAllText(
        $temporaryPassphrasePath,
        $plainPassphrase,
        [Text.UTF8Encoding]::new($false)
    )

    $signature = (& $QsdmCliPath wallet sign `
        --in $keystorePath `
        --passphrase-file $temporaryPassphrasePath `
        --message-file $payloadPath).Trim()
    if ($LASTEXITCODE -ne 0 -or $signature -notmatch '^[0-9a-f]{9254}$') {
        throw 'qsdmcli did not produce a valid ML-DSA-87 release signature.'
    }
    & $QsdmCliPath wallet verify `
        --public-key ([string]$publicMetadata.public_key) `
        --message-file $payloadPath `
        --signature $signature | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw 'Generated release signature failed its verification check.'
    }

    $envelope = [ordered]@{
        schema = 'qsdm.signed-release.v1'
        algorithm = 'ML-DSA-87'
        key_id = ([string]$publicMetadata.key_id).ToLowerInvariant()
        manifest_base64 = [Convert]::ToBase64String(
            [Text.UTF8Encoding]::new($false).GetBytes($manifestJson)
        )
        signature = $signature
    }
    [IO.File]::WriteAllText(
        $manifestPath,
        (($envelope | ConvertTo-Json -Depth 4) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false)
    )
}
finally {
    if (Test-Path -LiteralPath $temporaryPassphrasePath) {
        Remove-Item -LiteralPath $temporaryPassphrasePath -Force
    }
    if (Test-Path -LiteralPath $payloadPath) {
        Remove-Item -LiteralPath $payloadPath -Force
    }
    $plainPassphrase = $null
    $protectedPassphrase = $null
    $credential = $null
    $securePassphrase = $null
}

Write-Host "Created $manifestPath"
