param(
    [string]$StorageDirectory = "",
    [string]$QsdmCliPath = ""
)

$ErrorActionPreference = 'Stop'

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'Release-key initialization currently requires Windows DPAPI.'
}

$workspace = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
if (-not $StorageDirectory) {
    $StorageDirectory = Join-Path $workspace '.cache\qsdm-release-signing'
}
$StorageDirectory = [IO.Path]::GetFullPath($StorageDirectory)
$keystorePath = Join-Path $StorageDirectory 'release-signing-wallet.json'
$protectedPassphrasePath = Join-Path $StorageDirectory 'release-signing-passphrase.dpapi'
$publicMetadataPath = Join-Path $StorageDirectory 'release-signing-public.json'
$temporaryPassphrasePath = Join-Path $StorageDirectory ('.passphrase-' + [Guid]::NewGuid().ToString('N') + '.tmp')

foreach ($path in @($keystorePath, $protectedPassphrasePath, $publicMetadataPath)) {
    if (Test-Path -LiteralPath $path) {
        throw "Refusing to replace existing release-signing material: $path"
    }
}

if (-not $QsdmCliPath) {
    $candidatePaths = @(
        (Join-Path $workspace 'apps\qsdm-hive\qsdm-hive-main\native\windows\x64\qsdmcli.exe'),
        (Join-Path $workspace '.cache\qsdm-release-signing\qsdmcli.exe'),
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

New-Item -ItemType Directory -Force -Path $StorageDirectory | Out-Null
$currentAccount = [Security.Principal.WindowsIdentity]::GetCurrent().Name
& icacls.exe $StorageDirectory `
    '/inheritance:r' `
    '/grant:r' `
    "${currentAccount}:(OI)(CI)F" `
    'BUILTIN\Administrators:(OI)(CI)F' `
    'NT AUTHORITY\SYSTEM:(OI)(CI)F' | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'Could not restrict the release-signing directory ACL.'
}
$randomBytes = New-Object byte[] 48
[Security.Cryptography.RandomNumberGenerator]::Fill($randomBytes)
$passphrase = [Convert]::ToBase64String($randomBytes)

try {
    [IO.File]::WriteAllText(
        $temporaryPassphrasePath,
        $passphrase,
        [Text.UTF8Encoding]::new($false)
    )

    & $QsdmCliPath wallet new `
        --out $keystorePath `
        --passphrase-file $temporaryPassphrasePath | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "qsdmcli wallet new failed with exit code $LASTEXITCODE"
    }

    $protectedPassphrase = ConvertTo-SecureString $passphrase -AsPlainText -Force |
        ConvertFrom-SecureString
    [IO.File]::WriteAllText(
        $protectedPassphrasePath,
        $protectedPassphrase + [Environment]::NewLine,
        [Text.UTF8Encoding]::new($false)
    )

    $wallet = (& $QsdmCliPath wallet show --in $keystorePath --json | ConvertFrom-Json)
    if ($LASTEXITCODE -ne 0 -or -not $wallet.public_key) {
        throw 'Could not read the release-signing public key.'
    }
    $publicKeyBytes = [Convert]::FromHexString([string]$wallet.public_key)
    $keyId = [Convert]::ToHexString(
        [Security.Cryptography.SHA256]::HashData($publicKeyBytes)
    ).ToLowerInvariant()
    $metadata = [ordered]@{
        schema = 'qsdm.release-trust-key.v1'
        key_id = $keyId
        algorithm = 'ML-DSA-87'
        public_key = ([string]$wallet.public_key).ToLowerInvariant()
        address = [string]$wallet.address
        created_at = (Get-Date).ToUniversalTime().ToString('o')
        custody = 'Encrypted QSDM keystore; passphrase protected by Windows DPAPI.'
    }
    [IO.File]::WriteAllText(
        $publicMetadataPath,
        (($metadata | ConvertTo-Json -Depth 4) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false)
    )
}
finally {
    if (Test-Path -LiteralPath $temporaryPassphrasePath) {
        Remove-Item -LiteralPath $temporaryPassphrasePath -Force
    }
    [Array]::Clear($randomBytes, 0, $randomBytes.Length)
    $passphrase = $null
}

Write-Host "Created encrypted QSDM release key in $StorageDirectory"
Write-Host "Public metadata: $publicMetadataPath"
Write-Host 'Move the directory to encrypted offline storage before production signing.'
