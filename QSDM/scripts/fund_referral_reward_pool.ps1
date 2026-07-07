param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$ApiBaseUrl = "http://127.0.0.1:8080",
    [Parameter(Mandatory = $true)]
    [double]$AmountCell,
    [double]$FeeCell = 0.001,
    [string]$PoolAddress = "",
    [string]$KeystorePath = (Join-Path $HOME ".qsdm\wallet.json"),
    [string]$PassphraseFile = "",
    [switch]$Submit
)

$ErrorActionPreference = "Stop"

function Invoke-QsdmApi {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$Path,
        [string]$Body = ""
    )

    $uri = "$($script:Api.TrimEnd('/'))$Path"
    if ($Body) {
        return Invoke-RestMethod -Method $Method -Uri $uri -ContentType "application/json" -Body $Body -TimeoutSec 15
    }
    return Invoke-RestMethod -Method $Method -Uri $uri -TimeoutSec 15
}

if ($AmountCell -le 0) {
    throw "AmountCell must be positive."
}
if ($FeeCell -lt 0) {
    throw "FeeCell cannot be negative."
}

$script:Api = $ApiBaseUrl.TrimEnd("/")
$qsdmCli = Join-Path $QsdmRoot "source\qsdmcli.exe"
if (-not (Test-Path -LiteralPath $qsdmCli)) {
    throw "Missing qsdmcli.exe at $qsdmCli. Build qsdmcli before funding the referral pool."
}
if (-not (Test-Path -LiteralPath $KeystorePath)) {
    throw "Missing keystore at $KeystorePath."
}
if ($PassphraseFile -and -not (Test-Path -LiteralPath $PassphraseFile)) {
    throw "Missing passphrase file at $PassphraseFile."
}

$poolStatus = Invoke-QsdmApi -Method "GET" -Path "/api/v1/referrals/reward-pool"
if (-not $PoolAddress) {
    $PoolAddress = [string]$poolStatus.pool_address
}
if (-not $PoolAddress) {
    throw "Referral reward pool address is empty."
}
if ($PoolAddress -notmatch '^[0-9a-fA-F]{64}$') {
    throw "Referral treasury address must be a 64-character QSDM wallet address. Legacy named pools cannot be funded in production."
}
if ([string]$poolStatus.funding_method -ne "isolated-signer-signed-transfer") {
    throw "Core is not reporting the production treasury-signer funding path. Refusing to fund."
}

$showArgs = @("wallet", "show", "--json", "--in", $KeystorePath)
$walletRaw = & $qsdmCli @showArgs
if ($LASTEXITCODE -ne 0) {
    throw "qsdmcli wallet show failed."
}
$wallet = $walletRaw | ConvertFrom-Json
$sender = [string]$wallet.address
if (-not $sender) {
    throw "qsdmcli wallet show did not return an address."
}

$account = Invoke-QsdmApi -Method "GET" -Path "/api/v1/mining/account?address=$sender"
$balance = [double]$account.balance
$needed = $AmountCell + $FeeCell
if ($balance -lt $needed) {
    throw "Funding wallet balance is too low. Balance=$balance CELL, needed=$needed CELL."
}

$cacheDir = Join-Path $QsdmRoot "source\.cache\referral-funding"
New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null

$timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
$random = [guid]::NewGuid().ToString("N").Substring(0, 12)
$txId = "refpool_$timestamp`_$random"
$envelope = [ordered]@{
    id = $txId
    sender = $sender
    recipient = $PoolAddress
    amount = $AmountCell
    fee = $FeeCell
    geotag = "qsdm-referral-pool"
    parent_cells = @()
    timestamp = (Get-Date).ToUniversalTime().ToString("o")
}

$unsignedPath = Join-Path $cacheDir "$txId.unsigned.json"
$signedPath = Join-Path $cacheDir "$txId.signed.json"
($envelope | ConvertTo-Json -Depth 5 -Compress) | Set-Content -LiteralPath $unsignedPath -NoNewline -Encoding UTF8

Write-Host "QSDM referral pool funding preflight"
Write-Host "  API:         $script:Api"
Write-Host "  From:        $sender"
Write-Host "  To pool:     $PoolAddress"
Write-Host "  Amount:      $AmountCell CELL"
Write-Host "  Fee:         $FeeCell CELL"
Write-Host "  Balance:     $balance CELL"
Write-Host "  Pool before: $($poolStatus.balance) CELL"
Write-Host "  Unsigned:    $unsignedPath"

if (-not $Submit) {
    Write-Host ""
    Write-Host "Dry run only. Re-run with -Submit to sign and submit this funding transfer."
    exit 0
}

$signArgs = @(
    "wallet", "sign-tx",
    "--in", $KeystorePath,
    "--envelope-file", $unsignedPath,
    "--auto-nonce",
    "--api-url", $script:Api
)
if ($PassphraseFile) {
    $signArgs += @("--passphrase-file", $PassphraseFile)
}

$signedRaw = & $qsdmCli @signArgs
if ($LASTEXITCODE -ne 0) {
    throw "qsdmcli wallet sign-tx failed."
}
$signedRaw | Set-Content -LiteralPath $signedPath -NoNewline -Encoding UTF8

$submitResponse = Invoke-QsdmApi -Method "POST" -Path "/api/v1/wallet/submit-signed" -Body $signedRaw
$poolAfter = Invoke-QsdmApi -Method "GET" -Path "/api/v1/referrals/reward-pool"

Write-Host ""
Write-Host "Funding transfer submitted."
Write-Host "  Tx ID:       $($submitResponse.transaction_id)"
Write-Host "  Status:      $($submitResponse.status)"
Write-Host "  Broadcast:   $($submitResponse.broadcast)"
Write-Host "  Signed:      $signedPath"
Write-Host "  Pool after:  $($poolAfter.balance) CELL"
