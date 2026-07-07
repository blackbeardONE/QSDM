[CmdletBinding()]
param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$WalletSetDirectory = (Join-Path $HOME ".qsdm\ecosystem-wallets\canonical-pilot-20260707"),
    [string[]]$RuntimeConfigPaths = @(),
    [switch]$Apply
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function New-RandomToken {
    $bytes = [byte[]]::new(64)
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    } finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
        $rng.Dispose()
    }
}

function Set-SecretFilePermissions {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ($env:OS -eq "Windows_NT") {
        $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
        & icacls.exe $Path "/inheritance:r" "/grant:r" `
            "*$($sid):F" "*S-1-5-18:F" "*S-1-5-32-544:F" | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to restrict signer token permissions: $Path"
        }
        return
    }

    & chmod 600 $Path
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to restrict signer token permissions: $Path"
    }
}

function Get-VerifiedWallet {
    param(
        [Parameter(Mandatory = $true)][string]$Role,
        [Parameter(Mandatory = $true)][string]$ExpectedAddress,
        [Parameter(Mandatory = $true)][string]$CliPath,
        [Parameter(Mandatory = $true)][string]$WalletRoot
    )

    $roleDirectory = Join-Path $WalletRoot $Role
    $keystorePath = Join-Path $roleDirectory "wallet.json"
    $passphrasePath = Join-Path $roleDirectory "wallet.passphrase"
    if (-not (Test-Path -LiteralPath $keystorePath) -or
        -not (Test-Path -LiteralPath $passphrasePath)) {
        throw "Wallet files are missing for role $Role"
    }

    $inspectOutput = @(& $CliPath wallet inspect --in $keystorePath --passphrase-file $passphrasePath 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "Keystore integrity verification failed for $Role`: $($inspectOutput -join ' ')"
    }
    $wallet = (& $CliPath wallet show --json --in $keystorePath) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or -not $wallet.address) {
        throw "Unable to read keystore address for $Role"
    }
    $actualAddress = ([string]$wallet.address).ToLowerInvariant()
    if ($actualAddress -ne $ExpectedAddress.ToLowerInvariant()) {
        throw "$Role keystore address $actualAddress does not match registry address $ExpectedAddress"
    }

    $tokenPath = Join-Path $roleDirectory "signer.token"
    return [pscustomobject]@{
        Role = $Role
        Address = $actualAddress
        KeystorePath = $keystorePath
        PassphrasePath = $passphrasePath
        TokenPath = $tokenPath
        TokenExists = Test-Path -LiteralPath $tokenPath
    }
}

$walletRoot = (Resolve-Path -LiteralPath $WalletSetDirectory).Path
$registryPath = Join-Path $walletRoot "ecosystem-wallets.public.pending.json"
if (-not (Test-Path -LiteralPath $registryPath)) {
    throw "Missing ecosystem wallet registry: $registryPath"
}
$registry = Get-Content -LiteralPath $registryPath -Raw | ConvertFrom-Json

$qsdmCliCandidates = @(
    (Join-Path $QsdmRoot "source\qsdmcli.exe"),
    (Join-Path $QsdmRoot "source\qsdmcli")
)
$qsdmCli = $qsdmCliCandidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
if (-not $qsdmCli) {
    throw "Missing qsdmcli under $QsdmRoot\source"
}

$referralRecord = $registry.wallets | Where-Object { $_.role -eq "referral-payout" } | Select-Object -First 1
$onboardingRecord = $registry.wallets | Where-Object { $_.role -eq "onboarding-payout" } | Select-Object -First 1
if (-not $referralRecord -or -not $onboardingRecord) {
    throw "Registry must contain referral-payout and onboarding-payout roles"
}

$referral = Get-VerifiedWallet -Role "referral-payout" `
    -ExpectedAddress ([string]$referralRecord.address) -CliPath $qsdmCli -WalletRoot $walletRoot
$onboarding = Get-VerifiedWallet -Role "onboarding-payout" `
    -ExpectedAddress ([string]$onboardingRecord.address) -CliPath $qsdmCli -WalletRoot $walletRoot
if ($referral.Address -eq $onboarding.Address) {
    throw "Referral and onboarding wallets must be separate"
}

if ($RuntimeConfigPaths.Count -eq 0) {
    $localRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
    $RuntimeConfigPaths = @(
        (Join-Path $localRoot "run-networked\qsdm-treasury.json"),
        (Join-Path $localRoot "run-v2\qsdm-treasury.json")
    )
}

Write-Host "QSDM ecosystem treasury signer configuration"
Write-Host "  Registry:   $registryPath"
Write-Host "  Referral:   $($referral.Address)"
Write-Host "  Onboarding: $($onboarding.Address)"
Write-Host "  Claims:     locked"
Write-Host "  Faucet:     locked"
if (-not $Apply) {
    Write-Host "Dry run only. Re-run with -Apply to create signer tokens and update runtime configuration."
    exit 0
}

foreach ($wallet in @($referral, $onboarding)) {
    if (-not $wallet.TokenExists) {
        [IO.File]::WriteAllText($wallet.TokenPath, (New-RandomToken), [Text.UTF8Encoding]::new($false))
        Set-SecretFilePermissions -Path $wallet.TokenPath
    }
}

$runtimeConfig = [ordered]@{
    referral = [ordered]@{
        enabled = $true
        claimsEnabled = $false
        autoStart = $true
        signerUrl = "http://127.0.0.1:8897"
        keystorePath = $referral.KeystorePath
        passphraseFile = $referral.PassphrasePath
        signerTokenFile = $referral.TokenPath
        expectedAddress = $referral.Address
        rewardCell = 5
        maxPayout = 5
        minimumReserve = 25
        feeCell = 0.001
    }
    faucet = [ordered]@{
        enabled = $false
        autoStart = $true
        signerUrl = "http://127.0.0.1:8898"
        keystorePath = $onboarding.KeystorePath
        passphraseFile = $onboarding.PassphrasePath
        signerTokenFile = $onboarding.TokenPath
        expectedAddress = $onboarding.Address
        targetBalance = 1
        maxGrant = 1
        maxPayout = 1
        minimumReserve = 10
        feeCell = 0.001
    }
}
$json = $runtimeConfig | ConvertTo-Json -Depth 6
$stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
foreach ($path in $RuntimeConfigPaths) {
    $directory = Split-Path -Parent $path
    [void](New-Item -ItemType Directory -Force -Path $directory)
    if (Test-Path -LiteralPath $path) {
        Copy-Item -LiteralPath $path -Destination "$path.bak-$stamp"
    }
    [IO.File]::WriteAllText($path, $json, [Text.UTF8Encoding]::new($false))
    Write-Host "  Updated: $path"
}

Write-Host "Signer identities are configured. No CELL was transferred and payout locks remain enabled."
