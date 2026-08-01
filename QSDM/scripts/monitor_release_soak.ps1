param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$StatePath = "",
    [string]$ExpectedVersion = "",
    [string]$ExpectedRevision = "",
    [string]$ExpectedNodeId = "",
    [string]$StartedAtUtc = "",
    [int]$DurationHours = 24,
    [int]$ExpectedIntervalMinutes = 5,
    [int]$MaxConsecutiveFailures = 3,
    [int]$MaxStagnationMinutes = 30,
    [double]$MinimumSampleRatio = 0.80,
    [switch]$NoPublicGatewayCheck,
    [switch]$RequireMiner,
    [switch]$Reset
)

$ErrorActionPreference = "Stop"

function Get-ObjectValue {
    param(
        [object]$Object,
        [string]$Name,
        [object]$Default = $null
    )

    if ($null -eq $Object) {
        return $Default
    }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) {
        return $Default
    }
    return $property.Value
}

function Set-ObjectValue {
    param(
        [object]$Object,
        [string]$Name,
        [object]$Value
    )

    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) {
        $Object | Add-Member -NotePropertyName $Name -NotePropertyValue $Value
    } else {
        $property.Value = $Value
    }
}

function Convert-ToUtcDate {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        return [DateTime]::UtcNow
    }
    return [DateTimeOffset]::Parse(
        $Value,
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::RoundtripKind
    ).UtcDateTime
}

function Write-JsonAtomic {
    param(
        [string]$Path,
        [object]$Value
    )

    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $temporary = "$Path.tmp-$PID-$([Guid]::NewGuid().ToString('N'))"
    $json = $Value | ConvertTo-Json -Depth 16
    [IO.File]::WriteAllText($temporary, $json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $temporary -Destination $Path -Force
}

function Protect-Detail {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        return ""
    }
    $safe = $Value -replace '(?i)([?&](?:t|token|key|secret|password)=)[^&\s]+', '$1[redacted]'
    $safe = $safe -replace '[\r\n]+', ' '
    if ($safe.Length -gt 600) {
        return $safe.Substring(0, 600)
    }
    return $safe
}

function Test-RevisionMatch {
    param(
        [string]$Expected,
        [string]$Actual
    )

    if ([string]::IsNullOrWhiteSpace($Expected) -or [string]::IsNullOrWhiteSpace($Actual)) {
        return $false
    }
    $expectedLower = $Expected.Trim().ToLowerInvariant()
    $actualLower = $Actual.Trim().ToLowerInvariant()
    return $expectedLower.StartsWith($actualLower) -or $actualLower.StartsWith($expectedLower)
}

function Get-QueryValue {
    param(
        [Uri]$Uri,
        [string]$Name
    )

    foreach ($item in $Uri.Query.TrimStart('?').Split('&', [StringSplitOptions]::RemoveEmptyEntries)) {
        $parts = $item.Split('=', 2)
        if ([Uri]::UnescapeDataString($parts[0]) -eq $Name) {
            if ($parts.Count -eq 1) {
                return ""
            }
            return [Uri]::UnescapeDataString($parts[1])
        }
    }
    return ""
}

function Invoke-JsonEndpoint {
    param(
        [string]$Uri,
        [int]$TimeoutSeconds = 4,
        [hashtable]$Headers = @{}
    )

    return Invoke-RestMethod -Uri $Uri -Headers $Headers -TimeoutSec $TimeoutSeconds -ErrorAction Stop
}

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
if ([string]::IsNullOrWhiteSpace($StatePath)) {
    $StatePath = Join-Path $LocalRoot "release-soak.json"
}
$StatePath = [IO.Path]::GetFullPath($StatePath)
$SampleLogPath = [IO.Path]::ChangeExtension($StatePath, ".samples.jsonl")
$LockPath = "$StatePath.lock"
$ManifestPath = Join-Path $LocalRoot "validator-active.json"

if ($DurationHours -lt 1) {
    throw "DurationHours must be at least 1."
}
if ($ExpectedIntervalMinutes -lt 1) {
    throw "ExpectedIntervalMinutes must be at least 1."
}
if ($MaxConsecutiveFailures -lt 1) {
    throw "MaxConsecutiveFailures must be at least 1."
}
if ($MaxStagnationMinutes -lt 1) {
    throw "MaxStagnationMinutes must be at least 1."
}
if ($MinimumSampleRatio -le 0 -or $MinimumSampleRatio -gt 1) {
    throw "MinimumSampleRatio must be greater than 0 and no more than 1."
}

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $StatePath) | Out-Null
if ($Reset) {
    Remove-Item -LiteralPath $StatePath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $SampleLogPath -Force -ErrorAction SilentlyContinue
}

try {
    $lockStream = [IO.File]::Open($LockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
} catch [IO.IOException] {
    Write-Host "Release soak audit is already running; this invocation was skipped."
    exit 0
}

try {
    $now = [DateTime]::UtcNow
    $checks = [ordered]@{}
    $hardFailures = New-Object System.Collections.Generic.List[string]

    function Set-Check {
        param(
            [string]$Name,
            [bool]$Ok,
            [bool]$Critical,
            [string]$Detail,
            [bool]$Hard = $false
        )

        $script:checks[$Name] = [ordered]@{
            ok       = $Ok
            critical = $Critical
            hard     = $Hard
            detail   = Protect-Detail $Detail
        }
        if (-not $Ok -and $Hard -and -not $script:hardFailures.Contains($Name)) {
            $script:hardFailures.Add($Name)
        }
    }

    $manifest = $null
    try {
        $manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
        Set-Check "active_manifest" $true $true "Loaded validator-active.json"
    } catch {
        Set-Check "active_manifest" $false $true $_.Exception.Message $true
    }

    if ([string]::IsNullOrWhiteSpace($ExpectedVersion) -and $null -ne $manifest) {
        $ExpectedVersion = [string](Get-ObjectValue $manifest "version" "")
    }
    if ([string]::IsNullOrWhiteSpace($ExpectedRevision) -and $null -ne $manifest) {
        $ExpectedRevision = [string](Get-ObjectValue $manifest "revision" "")
    }

    $state = $null
    if (Test-Path -LiteralPath $StatePath) {
        try {
            $state = Get-Content -LiteralPath $StatePath -Raw | ConvertFrom-Json
        } catch {
            throw "Could not read release soak state $StatePath`: $($_.Exception.Message)"
        }
    }

    if ($null -eq $state) {
        $started = Convert-ToUtcDate $StartedAtUtc
        $state = [pscustomobject][ordered]@{
            schema                       = "qsdm.release-soak.v1"
            state                        = "running"
            started_at_utc               = $started.ToString("o")
            deadline_at_utc              = $started.AddHours($DurationHours).ToString("o")
            finalized_at_utc             = ""
            expected_version             = $ExpectedVersion
            expected_revision            = $ExpectedRevision
            expected_node_id             = $ExpectedNodeId
            release_transaction_id       = [string](Get-ObjectValue $manifest "transaction_id" "")
            duration_hours                = $DurationHours
            expected_interval_minutes     = $ExpectedIntervalMinutes
            max_consecutive_failures      = $MaxConsecutiveFailures
            max_stagnation_minutes        = $MaxStagnationMinutes
            minimum_sample_ratio          = $MinimumSampleRatio
            baseline_chain_tip            = $null
            last_chain_tip                = $null
            last_chain_advance_at_utc      = ""
            sample_count                  = 0
            successful_sample_count       = 0
            consecutive_failure_count     = 0
            max_consecutive_failures_seen = 0
            hard_failures                 = @()
            failure_events                = @()
            last_sample                   = $null
        }
    }

    $defaults = [ordered]@{
        schema                       = "qsdm.release-soak.v1"
        state                        = "running"
        finalized_at_utc             = ""
        expected_version             = $ExpectedVersion
        expected_revision            = $ExpectedRevision
        expected_node_id             = $ExpectedNodeId
        release_transaction_id       = [string](Get-ObjectValue $manifest "transaction_id" "")
        duration_hours                = $DurationHours
        expected_interval_minutes     = $ExpectedIntervalMinutes
        max_consecutive_failures      = $MaxConsecutiveFailures
        max_stagnation_minutes        = $MaxStagnationMinutes
        minimum_sample_ratio          = $MinimumSampleRatio
        baseline_chain_tip            = $null
        last_chain_tip                = $null
        last_chain_advance_at_utc      = ""
        sample_count                  = 0
        successful_sample_count       = 0
        consecutive_failure_count     = 0
        max_consecutive_failures_seen = 0
        hard_failures                 = @()
        failure_events                = @()
        last_sample                   = $null
    }
    foreach ($entry in $defaults.GetEnumerator()) {
        if ($null -eq $state.PSObject.Properties[$entry.Key]) {
            Set-ObjectValue $state $entry.Key $entry.Value
        }
    }

    if ([string]::IsNullOrWhiteSpace([string](Get-ObjectValue $state "expected_version" ""))) {
        Set-ObjectValue $state "expected_version" $ExpectedVersion
    }
    if ([string]::IsNullOrWhiteSpace([string](Get-ObjectValue $state "expected_revision" ""))) {
        Set-ObjectValue $state "expected_revision" $ExpectedRevision
    }
    if ([string]::IsNullOrWhiteSpace([string](Get-ObjectValue $state "expected_node_id" "")) -and -not [string]::IsNullOrWhiteSpace($ExpectedNodeId)) {
        Set-ObjectValue $state "expected_node_id" $ExpectedNodeId
    }

    $deadline = Convert-ToUtcDate ([string](Get-ObjectValue $state "deadline_at_utc" ""))
    $existingState = [string](Get-ObjectValue $state "state" "running")
    $finalizedAt = [string](Get-ObjectValue $state "finalized_at_utc" "")
    if (($existingState -eq "passed" -or $existingState -eq "failed") -and -not [string]::IsNullOrWhiteSpace($finalizedAt)) {
        Write-Host "Release soak already $existingState. State: $StatePath"
        exit $(if ($existingState -eq "passed") { 0 } else { 1 })
    }

    $binaryPath = ""
    if ($null -ne $manifest) {
        $manifestVersion = [string](Get-ObjectValue $manifest "version" "")
        $manifestRevision = [string](Get-ObjectValue $manifest "revision" "")
        $versionOk = $manifestVersion -eq [string](Get-ObjectValue $state "expected_version" "")
        $revisionOk = Test-RevisionMatch ([string](Get-ObjectValue $state "expected_revision" "")) $manifestRevision
        Set-Check "release_identity" ($versionOk -and $revisionOk) $true "version=$manifestVersion revision=$manifestRevision" $true

        try {
            $binaryName = [string](Get-ObjectValue $manifest "binary" "")
            $binaryPath = [IO.Path]::GetFullPath((Join-Path $LocalRoot $binaryName))
            $localPrefix = [IO.Path]::GetFullPath($LocalRoot).TrimEnd('\') + '\'
            if (-not $binaryPath.StartsWith($localPrefix, [StringComparison]::OrdinalIgnoreCase)) {
                throw "Active binary resolves outside the local validator directory."
            }
            if (-not (Test-Path -LiteralPath $binaryPath -PathType Leaf)) {
                throw "Active validator binary is missing."
            }
            $actualHash = (Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
            $expectedHash = ([string](Get-ObjectValue $manifest "sha256" "")).ToLowerInvariant()
            Set-Check "binary_integrity" ($actualHash -eq $expectedHash) $true "sha256=$actualHash" $true
        } catch {
            Set-Check "binary_integrity" $false $true $_.Exception.Message $true
        }
    } else {
        Set-Check "release_identity" $false $true "Active manifest is unavailable." $true
        Set-Check "binary_integrity" $false $true "Active manifest is unavailable." $true
    }

    if (-not [string]::IsNullOrWhiteSpace($binaryPath)) {
        try {
            $processName = [IO.Path]::GetFileNameWithoutExtension($binaryPath)
            $matching = @()
            foreach ($process in @(Get-Process -Name $processName -ErrorAction SilentlyContinue)) {
                try {
                    if ([IO.Path]::GetFullPath($process.Path) -eq $binaryPath) {
                        $matching += $process
                    }
                } catch {
                    # A process that cannot expose its path is not accepted as the release process.
                }
            }
            Set-Check "validator_process" ($matching.Count -eq 1) $true "matching_processes=$($matching.Count)"
        } catch {
            Set-Check "validator_process" $false $true $_.Exception.Message
        }
    } else {
        Set-Check "validator_process" $false $true "Active binary path is unavailable."
    }

    try {
        $ready = Invoke-JsonEndpoint "http://127.0.0.1:8080/api/v1/health/ready"
        Set-Check "core_ready" ([string](Get-ObjectValue $ready "status" "") -eq "ready") $true "status=$([string](Get-ObjectValue $ready 'status' 'unknown'))"
    } catch {
        Set-Check "core_ready" $false $true $_.Exception.Message
    }

    $status = $null
    try {
        $status = Invoke-JsonEndpoint "http://127.0.0.1:8080/api/v1/status"
        Set-Check "core_status" $true $true "Local status endpoint responded."
    } catch {
        Set-Check "core_status" $false $true $_.Exception.Message
    }

    $currentChainTip = $null
    if ($null -ne $status) {
        $statusVersion = [string](Get-ObjectValue $status "version" "")
        $statusRevision = [string](Get-ObjectValue $status "git_sha" "")
        $statusReleaseOk = $statusVersion -eq [string](Get-ObjectValue $state "expected_version" "")
        $statusRevisionOk = Test-RevisionMatch ([string](Get-ObjectValue $state "expected_revision" "")) $statusRevision
        Set-Check "runtime_release_identity" ($statusReleaseOk -and $statusRevisionOk) $true "version=$statusVersion revision=$statusRevision" $true

        $nodeId = [string](Get-ObjectValue $status "node_id" "")
        $stateNodeId = [string](Get-ObjectValue $state "expected_node_id" "")
        if ([string]::IsNullOrWhiteSpace($stateNodeId) -and -not [string]::IsNullOrWhiteSpace($nodeId)) {
            Set-ObjectValue $state "expected_node_id" $nodeId
            $stateNodeId = $nodeId
        }
        Set-Check "node_identity" (-not [string]::IsNullOrWhiteSpace($nodeId) -and $nodeId -eq $stateNodeId) $true "Node identity is unchanged." $true

        $currentChainTip = [long](Get-ObjectValue $status "chain_tip" 0)
        $lastChainTipValue = Get-ObjectValue $state "last_chain_tip" $null
        if ($null -eq $lastChainTipValue) {
            Set-ObjectValue $state "baseline_chain_tip" $currentChainTip
            Set-ObjectValue $state "last_chain_tip" $currentChainTip
            Set-ObjectValue $state "last_chain_advance_at_utc" $now.ToString("o")
            Set-Check "chain_regression" $true $true "chain_tip=$currentChainTip" $true
            Set-Check "chain_progress" $true $true "Baseline chain tip recorded."
        } else {
            $lastChainTip = [long]$lastChainTipValue
            $regressed = $currentChainTip -lt $lastChainTip
            Set-Check "chain_regression" (-not $regressed) $true "previous=$lastChainTip current=$currentChainTip" $true
            if ($currentChainTip -gt $lastChainTip) {
                Set-ObjectValue $state "last_chain_tip" $currentChainTip
                Set-ObjectValue $state "last_chain_advance_at_utc" $now.ToString("o")
                Set-Check "chain_progress" $true $true "advanced_from=$lastChainTip current=$currentChainTip"
            } else {
                $lastAdvanceText = [string](Get-ObjectValue $state "last_chain_advance_at_utc" "")
                if ([string]::IsNullOrWhiteSpace($lastAdvanceText)) {
                    $lastAdvanceText = [string](Get-ObjectValue $state "started_at_utc" $now.ToString("o"))
                }
                $lastAdvance = Convert-ToUtcDate $lastAdvanceText
                $stagnationMinutes = [Math]::Round(($now - $lastAdvance).TotalMinutes, 1)
                $progressOk = $stagnationMinutes -le [int](Get-ObjectValue $state "max_stagnation_minutes" $MaxStagnationMinutes)
                Set-Check "chain_progress" $progressOk $true "unchanged_minutes=$stagnationMinutes chain_tip=$currentChainTip"
            }
        }

        $peers = [int](Get-ObjectValue $status "peers" 0)
        Set-Check "network_peers" ($peers -ge 1) $true "peers=$peers"
        Set-Check "task_actions" ([bool](Get-ObjectValue $status "task_actions_ready" $false)) $true "task_actions_ready=$([bool](Get-ObjectValue $status 'task_actions_ready' $false))"
    } else {
        Set-Check "runtime_release_identity" $false $true "Core status is unavailable."
        Set-Check "node_identity" $false $true "Core status is unavailable."
        Set-Check "chain_regression" $false $true "Core status is unavailable."
        Set-Check "chain_progress" $false $true "Core status is unavailable."
        Set-Check "network_peers" $false $true "Core status is unavailable."
        Set-Check "task_actions" $false $true "Core status is unavailable."
    }

    try {
        $watchdogState = Get-Content -LiteralPath (Join-Path $LocalRoot "watchdog.process.json") -Raw | ConvertFrom-Json
        $watchdogPid = [int](Get-ObjectValue $watchdogState "pid" 0)
        $watchdogProcess = Get-Process -Id $watchdogPid -ErrorAction Stop
        Set-Check "watchdog_process" ($null -ne $watchdogProcess -and $watchdogPid -gt 0) $true "pid=$watchdogPid"
    } catch {
        Set-Check "watchdog_process" $false $true $_.Exception.Message
    }

    foreach ($signer in @(
        @{ Name = "referral_signer"; Uri = "http://127.0.0.1:8897/healthz"; Role = "referral" },
        @{ Name = "faucet_signer"; Uri = "http://127.0.0.1:8898/healthz"; Role = "faucet" }
    )) {
        try {
            $signerStatus = Invoke-JsonEndpoint $signer.Uri
            $signerOk = [string](Get-ObjectValue $signerStatus "status" "") -eq "ok" -and [string](Get-ObjectValue $signerStatus "role" "") -eq $signer.Role
            Set-Check $signer.Name $signerOk $true "status=$([string](Get-ObjectValue $signerStatus 'status' 'unknown')) role=$([string](Get-ObjectValue $signerStatus 'role' 'unknown'))"
        } catch {
            Set-Check $signer.Name $false $true $_.Exception.Message
        }
    }

    $snapshot = $null
    try {
        $guiUrl = (Get-Content -LiteralPath (Join-Path $LocalRoot "local-gui-persist.url") -Raw).Trim()
        $guiUri = [Uri]$guiUrl
        if ($guiUri.Host -ne "127.0.0.1" -and $guiUri.Host -ne "localhost") {
            throw "Local GUI URL is not loopback-only."
        }
        $guiToken = Get-QueryValue $guiUri "t"
        if ([string]::IsNullOrWhiteSpace($guiToken)) {
            throw "Local GUI URL has no access token."
        }
        $snapshotUri = $guiUri.GetLeftPart([UriPartial]::Authority) + "/api/snapshot"
        $snapshot = Invoke-JsonEndpoint $snapshotUri 5 @{ "X-QSDM-Token" = $guiToken }
        Set-Check "admin_gui" ([bool](Get-ObjectValue (Get-ObjectValue $snapshot "admin" $null) "elevated" $false)) $true "Local GUI is reachable and elevated."
        $gateway = Get-ObjectValue $snapshot "gateway" $null
        Set-Check "home_gateway" ([bool](Get-ObjectValue $gateway "running" $false)) $true "running=$([bool](Get-ObjectValue $gateway 'running' $false))"
        if (-not $NoPublicGatewayCheck) {
            $publicCode = [int](Get-ObjectValue $gateway "public_code" 0)
            Set-Check "public_gateway" ([bool](Get-ObjectValue $gateway "public_ok" $false)) $true "http_status=$publicCode"
        }
    } catch {
        Set-Check "admin_gui" $false $true $_.Exception.Message
        Set-Check "home_gateway" $false $true $_.Exception.Message
        if (-not $NoPublicGatewayCheck) {
            Set-Check "public_gateway" $false $true $_.Exception.Message
        }
    }

    $minerRunning = $false
    if ($null -ne $snapshot) {
        $miner = Get-ObjectValue $snapshot "miner" $null
        $minerProcesses = @(Get-ObjectValue $miner "processes" @())
        $service = Get-ObjectValue $miner "service" $null
        $minerRunning = $minerProcesses.Count -gt 0 -or [string](Get-ObjectValue $service "state" "") -eq "RUNNING"
    }
    Set-Check "miner_process" $minerRunning ([bool]$RequireMiner) "running=$minerRunning"

    $spoolerRunning = $null -ne (Get-Process -Name "spoolsv" -ErrorAction SilentlyContinue | Select-Object -First 1)
    Set-Check "print_spooler" $spoolerRunning $false "running=$spoolerRunning"

    $criticalFailures = @()
    foreach ($entry in $checks.GetEnumerator()) {
        if ([bool]$entry.Value.critical -and -not [bool]$entry.Value.ok) {
            $criticalFailures += $entry.Key
        }
    }
    $sampleOk = $criticalFailures.Count -eq 0
    $sampleCount = [int](Get-ObjectValue $state "sample_count" 0) + 1
    $successCount = [int](Get-ObjectValue $state "successful_sample_count" 0)
    $failureStreak = [int](Get-ObjectValue $state "consecutive_failure_count" 0)
    if ($sampleOk) {
        $successCount++
        $failureStreak = 0
    } else {
        $failureStreak++
    }
    $maxFailureStreak = [Math]::Max([int](Get-ObjectValue $state "max_consecutive_failures_seen" 0), $failureStreak)

    Set-ObjectValue $state "sample_count" $sampleCount
    Set-ObjectValue $state "successful_sample_count" $successCount
    Set-ObjectValue $state "consecutive_failure_count" $failureStreak
    Set-ObjectValue $state "max_consecutive_failures_seen" $maxFailureStreak

    $allHardFailures = @((Get-ObjectValue $state "hard_failures" @()))
    foreach ($name in $hardFailures) {
        if ($allHardFailures -notcontains $name) {
            $allHardFailures += $name
        }
    }
    Set-ObjectValue $state "hard_failures" $allHardFailures

    $sample = [pscustomobject][ordered]@{
        sampled_at_utc   = $now.ToString("o")
        ok               = $sampleOk
        chain_tip        = $currentChainTip
        critical_failures = $criticalFailures
        checks           = [pscustomobject]$checks
    }
    Set-ObjectValue $state "last_sample" $sample

    if (-not $sampleOk) {
        $events = @((Get-ObjectValue $state "failure_events" @()))
        $events += [pscustomobject][ordered]@{
            at_utc = $now.ToString("o")
            checks = $criticalFailures
        }
        if ($events.Count -gt 100) {
            $events = @($events | Select-Object -Last 100)
        }
        Set-ObjectValue $state "failure_events" $events
    }

    $failureLimit = [int](Get-ObjectValue $state "max_consecutive_failures" $MaxConsecutiveFailures)
    if ($allHardFailures.Count -gt 0 -or $failureStreak -ge $failureLimit) {
        Set-ObjectValue $state "state" "failed"
        Set-ObjectValue $state "finalized_at_utc" $now.ToString("o")
    } elseif ($now -ge $deadline) {
        $duration = [int](Get-ObjectValue $state "duration_hours" $DurationHours)
        $interval = [int](Get-ObjectValue $state "expected_interval_minutes" $ExpectedIntervalMinutes)
        $expectedSamples = [Math]::Max(1, [Math]::Floor(($duration * 60) / $interval))
        $minimumSamples = [Math]::Ceiling($expectedSamples * [double](Get-ObjectValue $state "minimum_sample_ratio" $MinimumSampleRatio))
        $baselineTipValue = Get-ObjectValue $state "baseline_chain_tip" $null
        $lastTipValue = Get-ObjectValue $state "last_chain_tip" $null
        $chainAdvanced = $null -ne $baselineTipValue -and $null -ne $lastTipValue -and [long]$lastTipValue -gt [long]$baselineTipValue
        if ($successCount -ge $minimumSamples -and $chainAdvanced -and $sampleOk) {
            Set-ObjectValue $state "state" "passed"
        } else {
            Set-ObjectValue $state "state" "failed"
            $events = @((Get-ObjectValue $state "failure_events" @()))
            $events += [pscustomobject][ordered]@{
                at_utc = $now.ToString("o")
                checks = @("final_sample_coverage_or_chain_progress")
            }
            Set-ObjectValue $state "failure_events" $events
        }
        Set-ObjectValue $state "finalized_at_utc" $now.ToString("o")
    }

    Write-JsonAtomic $StatePath $state
    $sampleLine = $sample | ConvertTo-Json -Depth 12 -Compress
    [IO.File]::AppendAllText($SampleLogPath, $sampleLine + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))

    $resultState = [string](Get-ObjectValue $state "state" "running")
    Write-Host "Release soak sample $sampleCount`: state=$resultState ok=$sampleOk chain_tip=$currentChainTip"
    Write-Host "State: $StatePath"
    if ($resultState -eq "failed") {
        exit 1
    }
} finally {
    if ($null -ne $lockStream) {
        $lockStream.Dispose()
    }
}
