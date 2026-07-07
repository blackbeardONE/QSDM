param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$StateDir = "",
    [string]$ApiUrl = "https://api.qsdm.tech/api/v1",
    [string]$CliPath = ""
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
if ([string]::IsNullOrWhiteSpace($StateDir)) {
    $StateDir = Join-Path $QsdmRoot "source\.cache\local-validator\run-networked"
}
if ([string]::IsNullOrWhiteSpace($CliPath)) {
    $CliPath = Join-Path $QsdmRoot "source\qsdmcli.exe"
}
$StateDir = (Resolve-Path $StateDir).Path
$CliPath = (Resolve-Path $CliPath).Path
$chainPath = Join-Path $StateDir "qsdm_chain.ndjson"
$snapshotPath = Join-Path $StateDir "qsdm_enrollment.json"
$pidPath = Join-Path $StateDir "qsdm.autostart.pid"

if (Test-Path -LiteralPath $pidPath) {
    $pidText = (Get-Content -LiteralPath $pidPath -Raw).Trim()
    if ($pidText -match '^\d+$') {
        $pidProcess = Get-Process -Id ([int]$pidText) -ErrorAction SilentlyContinue
        if ($pidProcess -and $pidProcess.ProcessName -like 'qsdm*') {
            throw "Refusing to repair enrollment state while validator PID $pidText is running."
        }
    }
}

$payloadByNode = @{}
$slashTransactions = 0
$tipHeight = 0L
foreach ($line in [System.IO.File]::ReadLines($chainPath)) {
    if ([string]::IsNullOrWhiteSpace($line)) {
        continue
    }
    if ($line.Contains('qsdm/slash/', [StringComparison]::Ordinal)) {
        $slashTransactions++
    }
    if (-not $line.Contains('qsdm/enroll/v2', [StringComparison]::Ordinal)) {
        if ($line.StartsWith('{"height":', [StringComparison]::Ordinal)) {
            $comma = $line.IndexOf(',')
            if ($comma -gt 10) {
                [long]::TryParse($line.Substring(10, $comma - 10), [ref]$tipHeight) | Out-Null
            }
        }
        continue
    }

    $block = $line | ConvertFrom-Json -Depth 12
    $tipHeight = [Math]::Max($tipHeight, [long]$block.height)
    foreach ($tx in @($block.transactions)) {
        if ([string]$tx.contract_id -ne 'qsdm/enroll/v2' -or [string]::IsNullOrWhiteSpace([string]$tx.Payload)) {
            continue
        }
        $payloadJson = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([string]$tx.Payload))
        $payload = $payloadJson | ConvertFrom-Json
        if ([string]$payload.kind -eq 'enroll') {
            $payloadByNode[[string]$payload.node_id] = [pscustomobject]@{
                Payload = $payload
                Owner = [string]$tx.Sender
                Height = [long]$block.height
            }
        }
    }
}

if ($slashTransactions -gt 0) {
    throw "The chain contains $slashTransactions slashing transaction(s). Automatic repair is refusing because seen-evidence replay must also be reconstructed."
}

$oldApi = $env:QSDM_API_URL
$oldHttpProxy = $env:HTTP_PROXY
$oldHttpsProxy = $env:HTTPS_PROXY
$oldAllProxy = $env:ALL_PROXY
$oldNoProxy = $env:NO_PROXY
try {
    $env:QSDM_API_URL = $ApiUrl
    $env:HTTP_PROXY = ""
    $env:HTTPS_PROXY = ""
    $env:ALL_PROXY = ""
    $env:NO_PROXY = "api.qsdm.tech,127.0.0.1,localhost"
    $rawRegistry = (& $CliPath enrollments --all | Out-String)
    if ($LASTEXITCODE -ne 0) {
        throw "qsdmcli enrollments failed with exit code $LASTEXITCODE"
    }
    $registry = $rawRegistry | ConvertFrom-Json
} finally {
    $env:QSDM_API_URL = $oldApi
    $env:HTTP_PROXY = $oldHttpProxy
    $env:HTTPS_PROXY = $oldHttpsProxy
    $env:ALL_PROXY = $oldAllProxy
    $env:NO_PROXY = $oldNoProxy
}

$records = @()
foreach ($current in @($registry.records)) {
    $nodeID = [string]$current.node_id
    $source = $payloadByNode[$nodeID]
    if ([long]$current.enrolled_at_height -gt $tipHeight) {
        # The canonical registry may be ahead of this snapshot's chain. A
        # future enrollment must not influence historical reward replay.
        continue
    }
    if ($null -eq $source) {
        if ([string]$current.phase -eq 'active') {
            throw "Active canonical enrollment $nodeID has no matching enroll transaction in $chainPath."
        }
        # Early pilot enrollments were seeded before enrollment transactions
        # were journaled. They are already inactive and cannot submit proofs;
        # retain their economic/unbond state with an inert key until swept.
        $source = [pscustomobject]@{
            Payload = [pscustomobject]@{
                hmac_key = [Convert]::ToBase64String([byte[]]::new(32))
                memo = "recovered inactive legacy enrollment"
            }
            Owner = [string]$current.owner
            Height = [long]$current.enrolled_at_height
        }
    }

    $journaledEnrollment = $payloadByNode.ContainsKey($nodeID)
    $bondMode = [string]$source.Payload.bond_mode
    if ([string]::IsNullOrWhiteSpace($bondMode)) {
        $bondMode = [string]$current.bond_mode
    }
    if ([string]::IsNullOrWhiteSpace($bondMode)) {
        $bondMode = "upfront"
    }
    $initialStakeDust = [uint64]$current.stake_dust
    if ($journaledEnrollment) {
        # Canonical registry projections describe the current tip. Rebuild a
        # historical snapshot from the on-chain enrollment payload instead;
        # deferred bonds are replayed below only through $tipHeight.
        $initialStakeDust = [uint64]$source.Payload.stake_dust
    }

    $record = [ordered]@{
        node_id = $nodeID
        owner = [string]$current.owner
        gpu_uuid = [string]$current.gpu_uuid
        hmac_key = [string]$source.Payload.hmac_key
        stake_dust = $initialStakeDust
    }
    $record.bond_mode = $bondMode
    if ([uint64]$current.required_stake_dust -gt 0) {
        $record.required_stake_dust = [uint64]$current.required_stake_dust
    }
    $record.enrolled_at_height = [uint64]$current.enrolled_at_height
    if ([uint64]$current.revoked_at_height -gt 0 -and [uint64]$current.revoked_at_height -le [uint64]$tipHeight) {
        $record.revoked_at_height = [uint64]$current.revoked_at_height
    }
    if ([uint64]$current.unbond_matures_at_height -gt 0 -and [uint64]$current.revoked_at_height -le [uint64]$tipHeight) {
        $record.unbond_matures_at_height = [uint64]$current.unbond_matures_at_height
    }
    if (-not [string]::IsNullOrWhiteSpace([string]$source.Payload.memo)) {
        $record.memo = [string]$source.Payload.memo
    }
    $records += [pscustomobject]$record
}

# Enrollment state is not folded into the account StateRoot, but deferred-bond
# withholding depends on it. Replaying from the registry's current projection
# at an older chain tip therefore credits rewards that canonical consensus held
# as stake. Rebuild each journaled mining_rewards bond from its enrollment
# payload and only the reward transactions committed at or before this tip.
$deferredByOwner = @{}
foreach ($record in $records) {
    if ([string]$record.bond_mode -ne 'mining_rewards') {
        continue
    }
    $owner = [string]$record.owner
    if (-not $deferredByOwner.ContainsKey($owner)) {
        $deferredByOwner[$owner] = [Collections.Generic.List[object]]::new()
    }
    $deferredByOwner[$owner].Add($record)
}
foreach ($owner in @($deferredByOwner.Keys)) {
    $deferredByOwner[$owner] = @($deferredByOwner[$owner] | Sort-Object node_id)
}

$rewardTransactions = 0L
foreach ($line in [System.IO.File]::ReadLines($chainPath)) {
    if ([string]::IsNullOrWhiteSpace($line) -or
        -not $line.Contains('qsdm/mining-reward/v1', [StringComparison]::Ordinal)) {
        continue
    }
    $block = $line | ConvertFrom-Json -Depth 12
    $height = [uint64]$block.height
    if ($height -gt [uint64]$tipHeight) {
        break
    }
    foreach ($tx in @($block.transactions)) {
        if ([string]$tx.contract_id -ne 'qsdm/mining-reward/v1') {
            continue
        }
        $owner = [string]$tx.Recipient
        if (-not $deferredByOwner.ContainsKey($owner)) {
            continue
        }
        $rewardDustDecimal = [Math]::Round(
            ([decimal]$tx.Amount * [decimal]100000000),
            0,
            [MidpointRounding]::AwayFromZero
        )
        if ($rewardDustDecimal -le 0) {
            continue
        }
        $remainingReward = [uint64]$rewardDustDecimal
        foreach ($record in @($deferredByOwner[$owner])) {
            if ($height -lt [uint64]$record.enrolled_at_height) {
                continue
            }
            if ($null -ne $record.revoked_at_height -and
                [uint64]$record.revoked_at_height -gt 0 -and
                $height -ge [uint64]$record.revoked_at_height) {
                continue
            }
            $required = [uint64]$record.required_stake_dust
            $staked = [uint64]$record.stake_dust
            if ($required -eq 0 -or $staked -ge $required) {
                continue
            }
            $needed = $required - $staked
            $locked = [Math]::Min($needed, $remainingReward)
            $record.stake_dust = [uint64]($staked + $locked)
            $remainingReward -= $locked
            if ($remainingReward -eq 0) {
                break
            }
        }
        $rewardTransactions++
    }
}

$document = [ordered]@{ records = $records }
$json = $document | ConvertTo-Json -Depth 12
$tempPath = "$snapshotPath.repair-$PID.tmp"
$stamp = [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ")
$backupPath = "$snapshotPath.corrupt-$stamp.bak"
$utf8 = [Text.UTF8Encoding]::new($false)
[IO.File]::WriteAllText($tempPath, $json, $utf8)
$check = Get-Content -LiteralPath $tempPath -Raw | ConvertFrom-Json
if (@($check.records).Count -ne @($records).Count) {
    throw "Generated snapshot validation failed."
}
if (Test-Path -LiteralPath $snapshotPath) {
    [IO.File]::Copy($snapshotPath, $backupPath, $false)
}
[IO.File]::Copy($tempPath, $snapshotPath, $true)
[IO.File]::Copy($tempPath, "$snapshotPath.last-good", $true)
Remove-Item -LiteralPath $tempPath -Force -ErrorAction SilentlyContinue

Write-Host "Rebuilt $(@($records).Count) enrollment records at chain tip $tipHeight."
Write-Host "Replayed $rewardTransactions deferred-bond reward transactions through the local tip."
Write-Host "Snapshot: $snapshotPath"
Write-Host "Quarantined prior file: $backupPath"
