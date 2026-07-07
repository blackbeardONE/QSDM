param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("referral", "faucet", "integration", "operations")]
    [string]$Role,
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$OutputDirectory = (Join-Path $HOME ".qsdm\treasury"),
    [Parameter(Mandatory = $true)]
    [string]$PassphraseFile
)

$ErrorActionPreference = "Stop"

$qsdmCli = Join-Path $QsdmRoot "source\qsdmcli.exe"
if (-not (Test-Path -LiteralPath $qsdmCli)) {
    throw "Missing qsdmcli.exe at $qsdmCli. Build qsdmcli before creating treasury wallets."
}
if (-not (Test-Path -LiteralPath $PassphraseFile)) {
    throw "Passphrase file does not exist: $PassphraseFile"
}
if ([string]::IsNullOrWhiteSpace((Get-Content -LiteralPath $PassphraseFile -Raw))) {
    throw "Passphrase file is empty."
}

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$walletPath = Join-Path $OutputDirectory "$Role-wallet.json"
$tokenPath = Join-Path $OutputDirectory "$Role-signer.token"
if (Test-Path -LiteralPath $walletPath) {
    throw "Refusing to overwrite existing treasury wallet: $walletPath"
}
if (Test-Path -LiteralPath $tokenPath) {
    throw "Refusing to overwrite existing signer token: $tokenPath"
}

$address = (& $qsdmCli wallet new --out $walletPath --passphrase-file $PassphraseFile).Trim()
if ($LASTEXITCODE -ne 0 -or $address -notmatch '^[0-9a-fA-F]{64}$') {
    throw "qsdmcli did not create a valid treasury wallet."
}

$tokenBytes = [byte[]]::new(64)
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
try {
    $rng.GetBytes($tokenBytes)
} finally {
    $rng.Dispose()
}
$token = [Convert]::ToHexString($tokenBytes).ToLowerInvariant()
Set-Content -LiteralPath $tokenPath -Value $token -NoNewline -Encoding UTF8

if ($IsWindows -or $env:OS -eq "Windows_NT") {
    & icacls.exe $OutputDirectory /inheritance:r /grant:r "$env:USERNAME`:(OI)(CI)F" | Out-Null
}

Write-Host "Created QSDM $Role hot wallet"
Write-Host "  Address:    $address"
Write-Host "  Keystore:   $walletPath"
Write-Host "  Token file: $tokenPath"
Write-Host ""
Write-Host "Keep the keystore, passphrase, and token out of source control. Fund this"
Write-Host "wallet only with the approved short-period operating budget."
