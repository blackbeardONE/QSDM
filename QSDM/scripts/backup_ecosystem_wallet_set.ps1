[CmdletBinding()]
param(
    [string]$SourceDirectory = (Join-Path $HOME ".qsdm\ecosystem-wallets\canonical-pilot-20260707"),
    [string]$DestinationDirectory = "E:\764",
    [string]$CredentialDirectory = (Join-Path $HOME ".qsdm\ecosystem-wallet-backup-credentials"),
    [int]$Pbkdf2Iterations = 600000
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $true
Set-StrictMode -Version Latest

function New-RandomSecret {
    param([int]$ByteCount = 64)

    $bytes = [byte[]]::new($ByteCount)
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    } finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
        $rng.Dispose()
    }
}

function Set-RestrictedDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ($env:OS -ne "Windows_NT") {
        throw "This backup script currently requires Windows ACL support."
    }
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $inheritance = [Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [Security.AccessControl.InheritanceFlags]::ObjectInherit
    $identities = @(
        [Security.Principal.WindowsIdentity]::GetCurrent().User,
        [Security.Principal.SecurityIdentifier]::new("S-1-5-18"),
        [Security.Principal.SecurityIdentifier]::new("S-1-5-32-544")
    )
    foreach ($identity in $identities) {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            $identity,
            [Security.AccessControl.FileSystemRights]::FullControl,
            $inheritance,
            [Security.AccessControl.PropagationFlags]::None,
            [Security.AccessControl.AccessControlType]::Allow
        )
        [void]$acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Set-RestrictedFileAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    & icacls.exe $Path "/inheritance:r" "/grant:r" `
        "*$($currentSid):F" "*S-1-5-18:F" "*S-1-5-32-544:F" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to restrict file permissions on $Path"
    }
}

function Write-RestrictedUtf8File {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Value
    )

    [IO.File]::WriteAllText($Path, $Value, [Text.UTF8Encoding]::new($false))
    Set-RestrictedFileAcl -Path $Path
}

function New-EncryptedArchive {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$TarPath,
        [Parameter(Mandatory = $true)][string]$OpenSslPath,
        [Parameter(Mandatory = $true)][string]$SourcePath,
        [Parameter(Mandatory = $true)][string]$PasswordPath,
        [Parameter(Mandatory = $true)][int]$Iterations
    )

    & $TarPath -cf - -C $SourcePath . |
        & $OpenSslPath enc -aes-256-cbc -salt -pbkdf2 -iter $Iterations -md sha256 `
            -pass "file:$PasswordPath" -out $ArchivePath
    if (-not (Test-Path -LiteralPath $ArchivePath) -or (Get-Item -LiteralPath $ArchivePath).Length -le 32) {
        throw "Encrypted backup was not created correctly: $ArchivePath"
    }
    Set-RestrictedFileAcl -Path $ArchivePath
}

function Test-EncryptedArchive {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$TarPath,
        [Parameter(Mandatory = $true)][string]$OpenSslPath,
        [Parameter(Mandatory = $true)][string]$PasswordPath,
        [Parameter(Mandatory = $true)][int]$Iterations,
        [Parameter(Mandatory = $true)][string[]]$RequiredRoles
    )

    $entries = @(
        & $OpenSslPath enc -d -aes-256-cbc -pbkdf2 -iter $Iterations -md sha256 `
            -pass "file:$PasswordPath" -in $ArchivePath |
            & $TarPath -tf -
    )
    foreach ($role in $RequiredRoles) {
        $walletEntry = "./$role/wallet.json"
        $passphraseEntry = "./$role/wallet.passphrase"
        if ($entries -notcontains $walletEntry -or $entries -notcontains $passphraseEntry) {
            throw "Backup verification failed for $role in $ArchivePath"
        }
    }
    if ($entries -notcontains "./ecosystem-wallets.public.pending.json" -or
        $entries -notcontains "./ecosystem-wallets.private.inventory.json") {
        throw "Backup inventory files are missing from $ArchivePath"
    }
}

$source = (Resolve-Path -LiteralPath $SourceDirectory).Path
$publicRegistryPath = Join-Path $source "ecosystem-wallets.public.pending.json"
$privateInventoryPath = Join-Path $source "ecosystem-wallets.private.inventory.json"
if (-not (Test-Path -LiteralPath $publicRegistryPath) -or
    -not (Test-Path -LiteralPath $privateInventoryPath)) {
    throw "The source directory is not a complete QSDM ecosystem wallet set: $source"
}

$registry = Get-Content -LiteralPath $publicRegistryPath -Raw | ConvertFrom-Json
$roles = @($registry.wallets | ForEach-Object { [string]$_.role })
$addresses = @($registry.wallets | ForEach-Object { [string]$_.address })
if ($roles.Count -ne 7 -or @($roles | Sort-Object -Unique).Count -ne 7) {
    throw "Expected seven unique ecosystem wallet roles."
}
if ($addresses.Count -ne 7 -or @($addresses | Sort-Object -Unique).Count -ne 7 -or
    @($addresses | Where-Object { $_ -notmatch '^[0-9a-f]{64}$' }).Count -ne 0) {
    throw "The public registry does not contain seven unique valid QSDM addresses."
}
foreach ($role in $roles) {
    if (-not (Test-Path -LiteralPath (Join-Path $source "$role\wallet.json")) -or
        -not (Test-Path -LiteralPath (Join-Path $source "$role\wallet.passphrase"))) {
        throw "Source wallet files are missing for $role"
    }
}

$tar = (Get-Command tar.exe -ErrorAction Stop).Source
$openSsl = (Get-Command openssl.exe -ErrorAction Stop).Source
$destination = [IO.Path]::GetFullPath($DestinationDirectory)
$credentialRoot = [IO.Path]::GetFullPath($CredentialDirectory)
if (Test-Path -LiteralPath $destination) {
    if (@(Get-ChildItem -LiteralPath $destination -Force).Count -gt 0) {
        throw "Refusing to overwrite non-empty backup directory: $destination"
    }
} else {
    [void](New-Item -ItemType Directory -Path $destination)
}
Set-RestrictedDirectoryAcl -Path $destination

if (-not (Test-Path -LiteralPath $credentialRoot)) {
    [void](New-Item -ItemType Directory -Path $credentialRoot)
}
Set-RestrictedDirectoryAcl -Path $credentialRoot
$credentialPath = Join-Path $credentialRoot "canonical-pilot-20260707-backup.passphrase"
if (Test-Path -LiteralPath $credentialPath) {
    throw "Refusing to overwrite existing backup credential: $credentialPath"
}

$archivePassword = New-RandomSecret
try {
    Write-RestrictedUtf8File -Path $credentialPath -Value $archivePassword
} finally {
    $archivePassword = $null
}

$archiveA = Join-Path $destination "QSDM-Ecosystem-Wallets-Backup-A.tar.aes256"
$archiveB = Join-Path $destination "QSDM-Ecosystem-Wallets-Backup-B.tar.aes256"
New-EncryptedArchive -ArchivePath $archiveA -TarPath $tar -OpenSslPath $openSsl `
    -SourcePath $source -PasswordPath $credentialPath -Iterations $Pbkdf2Iterations
New-EncryptedArchive -ArchivePath $archiveB -TarPath $tar -OpenSslPath $openSsl `
    -SourcePath $source -PasswordPath $credentialPath -Iterations $Pbkdf2Iterations
Test-EncryptedArchive -ArchivePath $archiveA -TarPath $tar -OpenSslPath $openSsl `
    -PasswordPath $credentialPath -Iterations $Pbkdf2Iterations -RequiredRoles $roles
Test-EncryptedArchive -ArchivePath $archiveB -TarPath $tar -OpenSslPath $openSsl `
    -PasswordPath $credentialPath -Iterations $Pbkdf2Iterations -RequiredRoles $roles

$publicCopy = Join-Path $destination "ecosystem-wallets.public.pending.json"
Copy-Item -LiteralPath $publicRegistryPath -Destination $publicCopy
Set-RestrictedFileAcl -Path $publicCopy

$walletLines = $registry.wallets | ForEach-Object {
    "$($_.role): $($_.address)`r`n  $($_.purpose)"
}
$informationPath = Join-Path $destination "WALLET-INFORMATION.txt"
Write-RestrictedUtf8File -Path $informationPath -Value @"
QSDM ECOSYSTEM WALLET INFORMATION
Status: PENDING CUSTODY, FUNDING, READINESS, AND ACTIVATION

$($walletLines -join "`r`n`r`n")

FUNDING FLOW
- Wallet creation does not mint CELL. Every wallet currently starts at zero.
- Existing canonical CELL may be transferred to Operations Treasury through a
  normal signed transaction.
- Operations Treasury may fund only approved bounded budgets for referral,
  onboarding, Sky Fang, and the separate CPU/GPU/RAM task pools.
- Protocol mining emission pays miners directly and does not use these wallets.
- Mother Hive workload revenue must enter consensus-controlled escrow before a
  70% contributor-owner / 15% Mother Hive / 15% ecosystem split.
- Do not use faucet credits, development prefunds, solo-ledger balances, or
  direct state edits to fund production programs.

The encrypted archives contain the keystores and their matching wallet
passphrases. The archive credential is intentionally stored outside E:\764 at:
$credentialPath

Move one encrypted archive and a separately stored copy of its credential to
different offline media before any wallet receives CELL.
"@

$restorePath = Join-Path $destination "README-RESTORE.txt"
Write-RestrictedUtf8File -Path $restorePath -Value @"
QSDM ECOSYSTEM WALLET BACKUP RESTORE

Encryption: OpenSSL AES-256-CBC, PBKDF2-HMAC-SHA-256, $Pbkdf2Iterations iterations.
Archive credential: kept separately at $credentialPath

Test an archive without extracting:
  openssl enc -d -aes-256-cbc -pbkdf2 -iter $Pbkdf2Iterations -md sha256 -pass file:<credential-file> -in <archive> | tar -tf -

Restore into a new empty, offline, access-controlled directory:
  openssl enc -d -aes-256-cbc -pbkdf2 -iter $Pbkdf2Iterations -md sha256 -pass file:<credential-file> -in <archive> | tar -xf - -C <empty-directory>

After restoring, run qsdmcli wallet inspect against every wallet.json and its
matching wallet.passphrase before transferring funds or configuring a signer.
Never extract these archives on a shared or internet-facing server.
"@

$verification = [ordered]@{
    schema_version = 1
    created_at = (Get-Date).ToUniversalTime().ToString("o")
    source_registry_sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $publicRegistryPath).Hash.ToLowerInvariant()
    encryption = [ordered]@{
        cipher = "aes-256-cbc"
        kdf = "pbkdf2-hmac-sha256"
        iterations = $Pbkdf2Iterations
        archive_credential_stored_separately = $true
    }
    archives = @(
        [ordered]@{
            file = [IO.Path]::GetFileName($archiveA)
            sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $archiveA).Hash.ToLowerInvariant()
            decrypt_and_list_test = "passed"
        },
        [ordered]@{
            file = [IO.Path]::GetFileName($archiveB)
            sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $archiveB).Hash.ToLowerInvariant()
            decrypt_and_list_test = "passed"
        }
    )
    wallet_count = 7
    unique_address_count = 7
}
$verificationPath = Join-Path $destination "BACKUP-VERIFICATION.json"
Write-RestrictedUtf8File -Path $verificationPath -Value ($verification | ConvertTo-Json -Depth 6)

$destinationFiles = @(Get-ChildItem -LiteralPath $destination -File)
foreach ($file in $destinationFiles) {
    Set-RestrictedFileAcl -Path $file.FullName
}

Write-Host "Created QSDM ecosystem backup kit: $destination"
Write-Host "  Encrypted backup A: $archiveA"
Write-Host "  Encrypted backup B: $archiveB"
Write-Host "  Wallet information: $informationPath"
Write-Host "  Verification record: $verificationPath"
Write-Host "  Separate archive credential: $credentialPath"
Write-Host "Both encrypted archives passed decrypt-and-list verification."
