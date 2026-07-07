param(
    [string]$ApiUrl = "http://127.0.0.1:8080/api/v1",
    [string]$TaskId = "qsdm-hive-local-task",
    [string]$CliPath = "",
    [string]$WalletPath = "",
    [string]$PassphraseFile = "",
    [string]$Sender = "",
    [double]$FundAmount = 10.0,
    [double]$StakeAmount = 2.5,
    [double]$RewardAmount = 1.25,
    [int]$WaitSeconds = 45,
    [switch]$SkipFund
)

$ErrorActionPreference = "Stop"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$LocalRoot = Join-Path $RepoRoot "QSDM\source\.cache\local-validator"
$SignerRoot = Join-Path $LocalRoot "hive-signer"

if ([string]::IsNullOrWhiteSpace($CliPath)) {
    $CliPath = Join-Path $RepoRoot "apps\qsdm-hive\qsdm-hive-main\release\native-smoke\qsdmcli-smoke.exe"
}
if ([string]::IsNullOrWhiteSpace($WalletPath)) {
    $WalletPath = Join-Path $SignerRoot "wallet.json"
}
if ([string]::IsNullOrWhiteSpace($PassphraseFile)) {
    $PassphraseFile = Join-Path $SignerRoot "passphrase.txt"
}

$CliPath = (Resolve-Path $CliPath).Path
$WalletPath = (Resolve-Path $WalletPath).Path
$PassphraseFile = (Resolve-Path $PassphraseFile).Path
$ApiUrl = $ApiUrl.TrimEnd("/")

$env:HTTP_PROXY = ""
$env:HTTPS_PROXY = ""
$env:ALL_PROXY = ""
$env:NO_PROXY = "127.0.0.1,localhost,api.qsdm.tech"

if ([string]::IsNullOrWhiteSpace($Sender)) {
    $walletInfo = & $CliPath wallet show --in $WalletPath --json | ConvertFrom-Json
    $Sender = $walletInfo.address
}

function New-ActionID {
    param([string]$Action)
    $stamp = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
    $suffix = -join ((48..57) + (97..102) | Get-Random -Count 12 | ForEach-Object {[char]$_})
    return "hive_${Action}_${stamp}_${suffix}"
}

function Get-Account {
    Invoke-RestMethod "$ApiUrl/mining/account?address=$Sender" -TimeoutSec 5
}

function Get-TaskState {
    Invoke-RestMethod "$ApiUrl/tasks/$TaskId/state" -TimeoutSec 5
}

function Wait-AccountNonce {
    param([uint64]$MinimumNonce)
    $deadline = (Get-Date).AddSeconds($WaitSeconds)
    do {
        $account = Get-Account
        if ([uint64]$account.nonce -ge $MinimumNonce) {
            return $account
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "Timed out waiting for signer nonce >= $MinimumNonce"
}

function Submit-Action {
    param(
        [string]$Action,
        [double]$Amount = 0,
        [hashtable]$Payload = @{}
    )

    $account = Get-Account
    $nonce = [uint64]$account.nonce
    $envelope = [ordered]@{
        id = New-ActionID $Action
        sender = $Sender
        task_id = $TaskId
        action = $Action
        nonce = $nonce
        timestamp = (Get-Date).ToUniversalTime().ToString("o")
    }
    if ($Amount -gt 0) {
        $envelope.amount = $Amount
    }
    if ($Payload.Count -gt 0) {
        $envelope.payload = ($Payload | ConvertTo-Json -Compress -Depth 8)
    }

    $unsigned = $envelope | ConvertTo-Json -Compress -Depth 8
    $oldErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    $signed = $unsigned | & $CliPath wallet sign-task-action --in $WalletPath --passphrase-file $PassphraseFile --envelope-file - 2>$null
    $signExit = $LASTEXITCODE
    $ErrorActionPreference = $oldErrorActionPreference
    if ($signExit -ne 0) {
        throw "qsdmcli wallet sign-task-action failed with exit code $signExit"
    }
    $signedEnvelope = $signed | ConvertFrom-Json
    $response = Invoke-RestMethod "$ApiUrl/tasks/actions/submit-signed" `
        -Method Post `
        -ContentType "application/json" `
        -Body ($signedEnvelope | ConvertTo-Json -Compress -Depth 8) `
        -TimeoutSec 10
    $advanced = Wait-AccountNonce ($nonce + 1)

    [pscustomobject]@{
        action = $Action
        action_id = $response.action_id
        status = $response.status
        mempool_status = $response.mempool_status
        nonce_before = $nonce
        nonce_after = [uint64]$advanced.nonce
        balance_after = [double]$advanced.balance
    }
}

$results = @()
if (-not $SkipFund) {
    $results += Submit-Action -Action "fund" -Amount $FundAmount -Payload @{
        source = "qsdm-hive-loop"
        reason = "seed reward pool for signed loop proof"
    }
}
$results += Submit-Action -Action "start" -Payload @{
    source = "qsdm-hive-loop"
    mode = "proof"
}
$results += Submit-Action -Action "stake" -Amount $StakeAmount -Payload @{
    source = "qsdm-hive-loop"
}
$slot = (Invoke-RestMethod "$ApiUrl/status" -TimeoutSec 5).chain_tip
$results += Submit-Action -Action "submit" -Payload @{
    source = "qsdm-hive-loop"
    round = 1
    slot = [uint64]$slot
    submission_value = "qsdm-hive-loop-proof-$slot"
    reward_amount = $RewardAmount
}
$results += Submit-Action -Action "claim" -Payload @{
    source = "qsdm-hive-loop"
    round = 0
}

$finalAccount = Get-Account
$finalState = Get-TaskState
[pscustomobject]@{
    api_url = $ApiUrl
    task_id = $TaskId
    sender = $Sender
    actions = $results
    final_balance = [double]$finalAccount.balance
    final_nonce = [uint64]$finalAccount.nonce
    task_state = $finalState.task
} | ConvertTo-Json -Depth 16
