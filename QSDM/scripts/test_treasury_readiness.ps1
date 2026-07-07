[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern("^[0-9a-fA-F]{64}$")]
    [string]$WalletAddress,

    [string]$LocalApiBaseUrl = "http://127.0.0.1:8080/api/v1",
    [string]$CanonicalApiBaseUrl = "https://api.qsdm.tech/api/v1",
    [string]$TreasuryConfigPath = "",
    [int]$AllowedHeightLag = 3,
    [int]$TimeoutSec = 20,
    [string]$OutputPath = ""
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$qsdmRoot = Split-Path -Parent $scriptRoot
if ([string]::IsNullOrWhiteSpace($TreasuryConfigPath)) {
    $TreasuryConfigPath = Join-Path $qsdmRoot "source\.cache\local-validator\run-networked\qsdm-treasury.json"
}

$LocalApiBaseUrl = $LocalApiBaseUrl.TrimEnd("/")
$CanonicalApiBaseUrl = $CanonicalApiBaseUrl.TrimEnd("/")
$checks = [System.Collections.Generic.List[object]]::new()
$walletStates = [System.Collections.Generic.List[object]]::new()

function Add-ReadinessCheck {
    param(
        [string]$Name,
        [bool]$Passed,
        [string]$Detail
    )
    $checks.Add([pscustomobject]@{
        name = $Name
        passed = $Passed
        detail = $Detail
    })
}

function Invoke-QsdmGet {
    param(
        [string]$BaseUrl,
        [string]$Path
    )
    Invoke-RestMethod -Method Get -Uri "$BaseUrl$Path" -TimeoutSec $TimeoutSec
}

function Get-QsdmBlockAt {
    param(
        [string]$BaseUrl,
        [uint64]$Height
    )
    $response = Invoke-QsdmGet -BaseUrl $BaseUrl -Path "/chain/blocks?from=$Height&limit=1"
    $blocks = @($response.blocks)
    if ($blocks.Count -ne 1) {
        throw "expected one block at height $Height from $BaseUrl, got $($blocks.Count)"
    }
    if ([uint64]$blocks[0].height -ne $Height) {
        throw "expected block height $Height from $BaseUrl, got $($blocks[0].height)"
    }
    if ([string]::IsNullOrWhiteSpace([string]$blocks[0].hash)) {
        throw "block $Height from $BaseUrl has no hash"
    }
    $blocks[0]
}

function Get-QsdmWalletState {
    param(
        [string]$BaseUrl,
        [string]$Address
    )
    $balance = Invoke-QsdmGet -BaseUrl $BaseUrl -Path "/wallet/balance?address=$Address"
    $nonce = Invoke-QsdmGet -BaseUrl $BaseUrl -Path "/wallet/nonce?sender=$Address"
    [pscustomobject]@{
        address = $Address.ToLowerInvariant()
        balance = [decimal]$balance.balance
        nonce = [uint64]$nonce.nonce
    }
}

function Test-WalletParity {
    param([string]$Address)
    try {
        $localState = Get-QsdmWalletState -BaseUrl $LocalApiBaseUrl -Address $Address
        $canonicalState = Get-QsdmWalletState -BaseUrl $CanonicalApiBaseUrl -Address $Address
        $matches = ($localState.balance -eq $canonicalState.balance) -and ($localState.nonce -eq $canonicalState.nonce)
        $walletStates.Add([pscustomobject]@{
            address = $Address.ToLowerInvariant()
            localBalance = $localState.balance
            canonicalBalance = $canonicalState.balance
            localNonce = $localState.nonce
            canonicalNonce = $canonicalState.nonce
            matches = $matches
        })
        Add-ReadinessCheck `
            -Name "wallet:$($Address.ToLowerInvariant())" `
            -Passed $matches `
            -Detail "local balance=$($localState.balance) nonce=$($localState.nonce); canonical balance=$($canonicalState.balance) nonce=$($canonicalState.nonce)"
    } catch {
        Add-ReadinessCheck -Name "wallet:$($Address.ToLowerInvariant())" -Passed $false -Detail $_.Exception.Message
    }
}

$localStatus = $null
$canonicalStatus = $null
try {
    $localStatus = Invoke-QsdmGet -BaseUrl $LocalApiBaseUrl -Path "/status"
    Add-ReadinessCheck -Name "local-status" -Passed $true -Detail "version=$($localStatus.version) tip=$($localStatus.chain_tip) peers=$($localStatus.peers)"
} catch {
    Add-ReadinessCheck -Name "local-status" -Passed $false -Detail $_.Exception.Message
}
try {
    $canonicalStatus = Invoke-QsdmGet -BaseUrl $CanonicalApiBaseUrl -Path "/status"
    Add-ReadinessCheck -Name "canonical-status" -Passed $true -Detail "version=$($canonicalStatus.version) tip=$($canonicalStatus.chain_tip) peers=$($canonicalStatus.peers)"
} catch {
    Add-ReadinessCheck -Name "canonical-status" -Passed $false -Detail $_.Exception.Message
}

if ($null -ne $localStatus) {
    $peerCount = [int]$localStatus.peers
    Add-ReadinessCheck -Name "network-peers" -Passed ($peerCount -ge 1) -Detail "local peers=$peerCount; required >=1"
}

if ($null -ne $localStatus -and $null -ne $canonicalStatus) {
    $localTip = [uint64]$localStatus.chain_tip
    $canonicalTip = [uint64]$canonicalStatus.chain_tip
    $localNotAhead = $localTip -le $canonicalTip
    $lag = if ($localNotAhead) { [uint64]($canonicalTip - $localTip) } else { [uint64]($localTip - $canonicalTip) }
    $heightReady = $localNotAhead -and ($lag -le [uint64]$AllowedHeightLag)
    Add-ReadinessCheck -Name "chain-height" -Passed $heightReady -Detail "local=$localTip canonical=$canonicalTip lag=$lag allowed=$AllowedHeightLag"

    $sampleHeights = [System.Collections.Generic.HashSet[uint64]]::new()
    [void]$sampleHeights.Add(0)
    [void]$sampleHeights.Add([Math]::Min($localTip, $canonicalTip))
    $commonTip = [Math]::Min($localTip, $canonicalTip)
    if ($commonTip -gt 64) {
        [void]$sampleHeights.Add($commonTip - 64)
    }
    foreach ($height in ($sampleHeights | Sort-Object)) {
        try {
            $localBlock = Get-QsdmBlockAt -BaseUrl $LocalApiBaseUrl -Height $height
            $canonicalBlock = Get-QsdmBlockAt -BaseUrl $CanonicalApiBaseUrl -Height $height
            $hashMatches = [string]$localBlock.hash -eq [string]$canonicalBlock.hash
            Add-ReadinessCheck `
                -Name "block-hash:$height" `
                -Passed $hashMatches `
                -Detail "local=$($localBlock.hash) canonical=$($canonicalBlock.hash)"
        } catch {
            Add-ReadinessCheck -Name "block-hash:$height" -Passed $false -Detail $_.Exception.Message
        }
    }
}

$treasury = $null
if (-not (Test-Path -LiteralPath $TreasuryConfigPath)) {
    Add-ReadinessCheck -Name "treasury-config" -Passed $false -Detail "missing: $TreasuryConfigPath"
} else {
    try {
        $treasury = Get-Content -Raw -LiteralPath $TreasuryConfigPath | ConvertFrom-Json
        Add-ReadinessCheck -Name "treasury-config" -Passed $true -Detail "loaded: $TreasuryConfigPath"
    } catch {
        Add-ReadinessCheck -Name "treasury-config" -Passed $false -Detail $_.Exception.Message
    }
}

$addresses = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
[void]$addresses.Add($WalletAddress)
if ($null -ne $treasury) {
    $referralAddress = [string]$treasury.referral.expectedAddress
    $faucetAddress = [string]$treasury.faucet.expectedAddress
    foreach ($entry in @(
        [pscustomobject]@{ name = "referral"; config = $treasury.referral; address = $referralAddress },
        [pscustomobject]@{ name = "faucet"; config = $treasury.faucet; address = $faucetAddress }
    )) {
        $validAddress = $entry.address -match "^[0-9a-fA-F]{64}$"
        Add-ReadinessCheck -Name "$($entry.name)-address" -Passed $validAddress -Detail "address=$($entry.address)"
        if ($validAddress) {
            [void]$addresses.Add($entry.address)
        }

        $signerUrl = ([string]$entry.config.signerUrl).TrimEnd("/")
        try {
            $health = Invoke-RestMethod -Method Get -Uri "$signerUrl/healthz" -TimeoutSec $TimeoutSec
            $signerMatches = `
                ([string]$health.status -eq "ok") -and `
                ([string]$health.role -eq $entry.name) -and `
                ([string]$health.address -eq $entry.address)
            Add-ReadinessCheck `
                -Name "$($entry.name)-signer" `
                -Passed $signerMatches `
                -Detail "status=$($health.status) role=$($health.role) address=$($health.address)"
        } catch {
            Add-ReadinessCheck -Name "$($entry.name)-signer" -Passed $false -Detail $_.Exception.Message
        }
    }

    $claimsLocked = -not [bool]$treasury.referral.claimsEnabled
    $faucetLocked = -not [bool]$treasury.faucet.enabled
    Add-ReadinessCheck -Name "referral-payout-lock" -Passed $claimsLocked -Detail "claimsEnabled=$($treasury.referral.claimsEnabled)"
    Add-ReadinessCheck -Name "faucet-payout-lock" -Passed $faucetLocked -Detail "enabled=$($treasury.faucet.enabled)"
    Add-ReadinessCheck -Name "treasury-role-separation" -Passed ($referralAddress -ne $faucetAddress) -Detail "referral=$referralAddress faucet=$faucetAddress"
}

foreach ($address in $addresses) {
    Test-WalletParity -Address $address
}

$failedChecks = @($checks | Where-Object { -not $_.passed })
$ready = $failedChecks.Count -eq 0
$report = [ordered]@{
    ready = $ready
    checkedAtUtc = [DateTime]::UtcNow.ToString("o")
    localApi = $LocalApiBaseUrl
    canonicalApi = $CanonicalApiBaseUrl
    allowedHeightLag = $AllowedHeightLag
    local = if ($null -ne $localStatus) {
        [ordered]@{ version = $localStatus.version; tip = $localStatus.chain_tip; peers = $localStatus.peers; nodeId = $localStatus.node_id }
    } else { $null }
    canonical = if ($null -ne $canonicalStatus) {
        [ordered]@{ version = $canonicalStatus.version; tip = $canonicalStatus.chain_tip; peers = $canonicalStatus.peers; nodeId = $canonicalStatus.node_id }
    } else { $null }
    wallets = @($walletStates)
    checks = @($checks)
}

$reportJson = $report | ConvertTo-Json -Depth 10
if (-not [string]::IsNullOrWhiteSpace($OutputPath)) {
    $resolvedOutputPath = [System.IO.Path]::GetFullPath($OutputPath)
    $outputDirectory = Split-Path -Parent $resolvedOutputPath
    if (-not [string]::IsNullOrWhiteSpace($outputDirectory)) {
        New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
    }
    Set-Content -LiteralPath $resolvedOutputPath -Value $reportJson -Encoding UTF8
}

foreach ($check in $checks) {
    $label = if ($check.passed) { "PASS" } else { "FAIL" }
    $color = if ($check.passed) { "Green" } else { "Red" }
    Write-Host ("[{0}] {1}: {2}" -f $label, $check.name, $check.detail) -ForegroundColor $color
}
Write-Host ""
if ($ready) {
    Write-Host "TREASURY READINESS: READY" -ForegroundColor Green
    exit 0
}
Write-Host "TREASURY READINESS: BLOCKED ($($failedChecks.Count) failed checks)" -ForegroundColor Red
exit 2
