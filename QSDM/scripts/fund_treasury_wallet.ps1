param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$ApiBaseUrl = "http://127.0.0.1:8080",
    [Parameter(Mandatory = $true)]
    [ValidateSet("referral", "faucet", "integration", "operations")]
    [string]$Role,
    [Parameter(Mandatory = $true)][string]$DestinationAddress,
    [Parameter(Mandatory = $true)][double]$AmountCell,
    [double]$FeeCell = 0.001,
    [string]$KeystorePath = (Join-Path $HOME ".qsdm\wallet.json"),
    [string]$PassphraseFile = "",
    [switch]$Submit
)

$ErrorActionPreference = "Stop"
if ($DestinationAddress -notmatch '^[0-9a-fA-F]{64}$') { throw "DestinationAddress must be a 64-character QSDM wallet address." }
if ($AmountCell -le 0) { throw "AmountCell must be positive." }
if ($FeeCell -lt 0) { throw "FeeCell cannot be negative." }

$api = $ApiBaseUrl.TrimEnd("/")
$qsdmCli = Join-Path $QsdmRoot "source\qsdmcli.exe"
if (-not (Test-Path -LiteralPath $qsdmCli)) { throw "Missing qsdmcli.exe at $qsdmCli." }
if (-not (Test-Path -LiteralPath $KeystorePath)) { throw "Missing source keystore: $KeystorePath" }
if ($PassphraseFile -and -not (Test-Path -LiteralPath $PassphraseFile)) { throw "Missing passphrase file: $PassphraseFile" }

$wallet = (& $qsdmCli wallet show --json --in $KeystorePath) | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or -not $wallet.address) { throw "Unable to read the source wallet." }
$sender = [string]$wallet.address
$account = Invoke-RestMethod -Uri "$api/api/v1/mining/account?address=$sender" -TimeoutSec 15
$needed = $AmountCell + $FeeCell
if ([double]$account.balance -lt $needed) { throw "Source wallet needs $needed CELL; balance is $($account.balance)." }

$cacheDir = Join-Path $QsdmRoot "source\.cache\treasury-funding"
New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null
$stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
$txId = "treasury_$Role`_$stamp`_$([guid]::NewGuid().ToString('N').Substring(0, 10))"
$unsignedPath = Join-Path $cacheDir "$txId.unsigned.json"
$signedPath = Join-Path $cacheDir "$txId.signed.json"
$envelope = [ordered]@{
    id = $txId
    sender = $sender
    recipient = $DestinationAddress.ToLowerInvariant()
    amount = $AmountCell
    fee = $FeeCell
    geotag = "qsdm-treasury-$Role"
    parent_cells = @()
    timestamp = (Get-Date).ToUniversalTime().ToString("o")
}
($envelope | ConvertTo-Json -Depth 5 -Compress) | Set-Content -LiteralPath $unsignedPath -NoNewline -Encoding UTF8

Write-Host "QSDM treasury funding preflight"
Write-Host "  Role:        $Role"
Write-Host "  From:        $sender"
Write-Host "  Destination: $DestinationAddress"
Write-Host "  Amount:      $AmountCell CELL"
Write-Host "  Fee:         $FeeCell CELL"
Write-Host "  Balance:     $($account.balance) CELL"
if (-not $Submit) {
    Write-Host "Dry run only. Re-run with -Submit to sign and submit."
    exit 0
}

$signArgs = @("wallet", "sign-tx", "--in", $KeystorePath, "--envelope-file", $unsignedPath, "--auto-nonce", "--api-url", $api)
if ($PassphraseFile) { $signArgs += @("--passphrase-file", $PassphraseFile) }
$signed = & $qsdmCli @signArgs
if ($LASTEXITCODE -ne 0) { throw "qsdmcli wallet sign-tx failed." }
$signed | Set-Content -LiteralPath $signedPath -NoNewline -Encoding UTF8
$response = Invoke-RestMethod -Method Post -Uri "$api/api/v1/wallet/submit-signed" -ContentType "application/json" -Body $signed -TimeoutSec 15

Write-Host "Funding transfer submitted"
Write-Host "  Transaction: $($response.transaction_id)"
Write-Host "  Status:      $($response.status)"
Write-Host "  Signed copy: $signedPath"
