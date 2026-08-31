param(
    [string]$BinaryPath = "",
    [int]$ProducerApiPort = 19080,
    [int]$FollowerApiPort = 19090,
    [int]$ProducerNetworkPort = 19001,
    [int]$FollowerNetworkPort = 19002,
    [int]$ProducerDashboardPort = 19081,
    [int]$FollowerDashboardPort = 19091,
    [int]$WaitSeconds = 90,
    [switch]$NoBuild,
    [switch]$KeepState
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$sourceDir = Join-Path $repoRoot 'source'
$configPath = Join-Path $repoRoot 'qsdm.yaml'
$stageRoot = Join-Path $repoRoot 'source\.cache\two-node-staging'
$binDir = Join-Path $stageRoot 'bin'
$producerDir = Join-Path $stageRoot 'producer'
$followerDir = Join-Path $stageRoot 'follower'

function Assert-PathInside {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $rootFull = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    $pathFull = [IO.Path]::GetFullPath($Path)
    if (-not $pathFull.StartsWith($rootFull, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to operate outside staging root: $pathFull"
    }
}

function Test-PortFree {
    param([int]$Port)

    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $Port)
    try {
        $listener.Start()
        return $true
    } catch {
        return $false
    } finally {
        try { $listener.Stop() } catch {}
    }
}

function Assert-PortFree {
    param([int]$Port)

    if (-not (Test-PortFree -Port $Port)) {
        throw "Port $Port is already in use. Pass alternate -ProducerApiPort/-FollowerApiPort/-ProducerNetworkPort/-FollowerNetworkPort values."
    }
}

function Invoke-QsdmJson {
    param([Parameter(Mandatory = $true)][string]$Uri)

    Invoke-RestMethod -Uri $Uri -Method Get -TimeoutSec 5
}

function Wait-HttpReady {
    param(
        [string]$BaseUrl,
        [string]$Name,
        [int]$Seconds
    )

    $deadline = (Get-Date).AddSeconds($Seconds)
    $last = $null
    while ((Get-Date) -lt $deadline) {
        try {
            $resp = Invoke-WebRequest -Uri "$BaseUrl/health/ready" -Method Get -TimeoutSec 3 -UseBasicParsing
            if ($resp.StatusCode -eq 200) {
                return
            }
            $last = "HTTP $($resp.StatusCode)"
        } catch {
            $last = $_.Exception.Message
        }
        Start-Sleep -Milliseconds 500
    }
    throw "$Name did not become ready at $BaseUrl/health/ready within ${Seconds}s. Last error: $last"
}

function Get-ChainTip {
    param([string]$BaseUrl)

    $status = Invoke-QsdmJson -Uri "$BaseUrl/status"
    return [uint64]$status.chain_tip
}

function Wait-ChainTipAtLeast {
    param(
        [string]$BaseUrl,
        [string]$Name,
        [uint64]$MinimumTip,
        [int]$Seconds
    )

    $deadline = (Get-Date).AddSeconds($Seconds)
    $lastTip = 0
    while ((Get-Date) -lt $deadline) {
        try {
            $lastTip = Get-ChainTip -BaseUrl $BaseUrl
            if ($lastTip -ge $MinimumTip) {
                return $lastTip
            }
        } catch {
            # Keep polling; readiness and catch-up race during startup.
        }
        Start-Sleep -Seconds 1
    }
    throw "$Name tip stayed at $lastTip; expected at least $MinimumTip within ${Seconds}s."
}

function Get-BlockAtHeight {
    param(
        [string]$BaseUrl,
        [uint64]$Height
    )

    $resp = Invoke-QsdmJson -Uri "$BaseUrl/chain/blocks?from=$Height&to=$Height&limit=1"
    if (-not $resp.blocks -or $resp.blocks.Count -lt 1) {
        throw "No block returned by $BaseUrl at height $Height"
    }
    return $resp.blocks[0]
}

function Start-QsdmNode {
    param(
        [string]$Name,
        [string]$WorkingDirectory,
        [int]$ApiPort,
        [int]$NetworkPort,
        [int]$DashboardPort,
        [hashtable]$ExtraEnv
    )

    New-Item -ItemType Directory -Force -Path $WorkingDirectory | Out-Null

    $logPath = Join-Path $WorkingDirectory 'qsdm.log'
    $commonEnv = @{
        'CONFIG_FILE' = $configPath
        'DISABLE_CLI' = '1'
        'STORAGE_TYPE' = 'file'
        'SQLITE_PATH' = (Join-Path $WorkingDirectory 'qsdm.db')
        'LOG_FILE' = $logPath
        'LOG_LEVEL' = 'INFO'
        'API_PORT' = "$ApiPort"
        'QSDM_API_BIND_ADDRESS' = '127.0.0.1'
        'DASHBOARD_PORT' = "$DashboardPort"
        'QSDM_DASHBOARD_BIND_ADDRESS' = '127.0.0.1'
        'NETWORK_PORT' = "$NetworkPort"
        'QSDM_NETWORK_BIND_ADDRESS' = '127.0.0.1'
        'QSDM_NETWORK_HOST_KEY_PATH' = (Join-Path $WorkingDirectory 'qsdm_network_host.key')
        'QSDM_CONSENSUS_SIGNER_KEY_PATH' = (Join-Path $WorkingDirectory 'qsdm_consensus_signer.json')
        'QSDM_TASK_ACTION_LOG_PATH' = (Join-Path $WorkingDirectory 'qsdm_task_actions.ndjson')
        'QSDM_USER_STORE_PATH' = (Join-Path $WorkingDirectory 'qsdm-users.json')
        'QSDM_NGC_PROOF_PERSIST_PATH' = (Join-Path $WorkingDirectory 'qsdm-ngc-proofs.json')
        'QSDM_API_RATE_LIMIT_MAX' = '1000'
        'QSDM_PRODUCTION_MODE' = '0'
    }
    foreach ($k in $ExtraEnv.Keys) {
        $commonEnv[$k] = [string]$ExtraEnv[$k]
    }

    $psi = [Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $BinaryPath
    $psi.WorkingDirectory = $WorkingDirectory
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    foreach ($entry in $commonEnv.GetEnumerator()) {
        $psi.Environment[$entry.Key] = $entry.Value
    }

    $proc = [Diagnostics.Process]::Start($psi)
    Start-Sleep -Milliseconds 750
    if ($proc.HasExited) {
        throw "$Name exited during startup with code $($proc.ExitCode). Check $logPath"
    }
    return $proc
}

function Stop-QsdmNode {
    param([Diagnostics.Process]$Process)

    if ($null -eq $Process) {
        return
    }
    try {
        if (-not $Process.HasExited) {
            try { $Process.CloseMainWindow() | Out-Null } catch {}
            Start-Sleep -Milliseconds 500
        }
        if (-not $Process.HasExited) {
            try {
                $Process.Kill($true)
            } catch {
                $Process.Kill()
            }
        }
        $Process.WaitForExit(5000) | Out-Null
    } catch {
        Write-Warning "Failed to stop process $($Process.Id): $($_.Exception.Message)"
    }
}

if (-not (Test-Path $sourceDir)) {
    throw "Expected QSDM source directory at $sourceDir"
}
if (-not (Test-Path $configPath)) {
    throw "Expected QSDM config at $configPath"
}

Assert-PortFree -Port $ProducerApiPort
Assert-PortFree -Port $FollowerApiPort
Assert-PortFree -Port $ProducerNetworkPort
Assert-PortFree -Port $FollowerNetworkPort
Assert-PortFree -Port $ProducerDashboardPort
Assert-PortFree -Port $FollowerDashboardPort

if (-not $KeepState) {
    Assert-PathInside -Root (Join-Path $repoRoot 'source\.cache') -Path $stageRoot
    if (Test-Path -LiteralPath $stageRoot) {
        Remove-Item -LiteralPath $stageRoot -Recurse -Force
    }
}
New-Item -ItemType Directory -Force -Path $binDir, $producerDir, $followerDir | Out-Null

if ([string]::IsNullOrWhiteSpace($BinaryPath)) {
    $BinaryPath = Join-Path $binDir 'qsdm-two-node-staging.exe'
}
$BinaryPath = [IO.Path]::GetFullPath($BinaryPath)

if (-not $NoBuild) {
    $buildScript = Join-Path $PSScriptRoot 'go-build-no-cgo.ps1'
    & $buildScript -OutputPath $BinaryPath
    if ($LASTEXITCODE -ne 0) {
        throw "qsdm build failed with exit code $LASTEXITCODE"
    }
}
if (-not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) {
    throw "QSDM binary was not found at $BinaryPath"
}

$producerBase = "http://127.0.0.1:$ProducerApiPort/api/v1"
$followerBase = "http://127.0.0.1:$FollowerApiPort/api/v1"

$soloProducer = $null
$networkProducer = $null
$follower = $null

try {
    Write-Host "Starting disposable producer in solo seed mode..."
    $soloProducer = Start-QsdmNode -Name 'producer(seed)' -WorkingDirectory $producerDir -ApiPort $ProducerApiPort -NetworkPort $ProducerNetworkPort -DashboardPort $ProducerDashboardPort -ExtraEnv @{
        'QSDM_SOLO_VALIDATOR_MODE' = '1'
    }
    Wait-HttpReady -BaseUrl $producerBase -Name 'producer(seed)' -Seconds 30
    $seedTip = Wait-ChainTipAtLeast -BaseUrl $producerBase -Name 'producer(seed)' -MinimumTip 1 -Seconds $WaitSeconds
    $seedBlock = Get-BlockAtHeight -BaseUrl $producerBase -Height $seedTip
    $producerID = [string]$seedBlock.producer_id
    if ([string]::IsNullOrWhiteSpace($producerID)) {
        throw "Seed producer block at height $seedTip did not expose producer_id"
    }
    Write-Host "Seed producer reached tip $seedTip with producer_id $producerID"

    Stop-QsdmNode -Process $soloProducer
    $soloProducer = $null
    Start-Sleep -Seconds 1
    Assert-PortFree -Port $ProducerApiPort
    Assert-PortFree -Port $ProducerNetworkPort

    Write-Host "Restarting producer in network-producer mode from the seeded chain..."
    $networkProducer = Start-QsdmNode -Name 'producer(network)' -WorkingDirectory $producerDir -ApiPort $ProducerApiPort -NetworkPort $ProducerNetworkPort -DashboardPort $ProducerDashboardPort -ExtraEnv @{
        'QSDM_NETWORK_BLOCK_PRODUCER' = '1'
        'QSDM_AUTHORIZED_BLOCK_PRODUCERS' = $producerID
    }
    Wait-HttpReady -BaseUrl $producerBase -Name 'producer(network)' -Seconds 30
    $producerTip = Wait-ChainTipAtLeast -BaseUrl $producerBase -Name 'producer(network)' -MinimumTip ($seedTip + 1) -Seconds $WaitSeconds
    Write-Host "Network producer reached tip $producerTip"

    Write-Host "Starting follower in networked catch-up mode..."
    $follower = Start-QsdmNode -Name 'follower' -WorkingDirectory $followerDir -ApiPort $FollowerApiPort -NetworkPort $FollowerNetworkPort -DashboardPort $FollowerDashboardPort -ExtraEnv @{
        'QSDM_NETWORKED_CATCHUP_MODE' = '1'
        'QSDM_CHAIN_SYNC_URLS' = $producerBase
        'QSDM_AUTHORIZED_BLOCK_PRODUCERS' = $producerID
        'QSDM_STAGING_ALLOW_REMOTE_GENESIS' = '1'
    }
    Wait-HttpReady -BaseUrl $followerBase -Name 'follower' -Seconds 30
    $followerTip = Wait-ChainTipAtLeast -BaseUrl $followerBase -Name 'follower' -MinimumTip $producerTip -Seconds $WaitSeconds

    $producerBlock = Get-BlockAtHeight -BaseUrl $producerBase -Height $producerTip
    $followerBlock = Get-BlockAtHeight -BaseUrl $followerBase -Height $producerTip
    if ([string]$producerBlock.hash -ne [string]$followerBlock.hash) {
        throw "Follower tip mismatch at height ${producerTip}: producer=$($producerBlock.hash) follower=$($followerBlock.hash)"
    }
    if ([string]$followerBlock.producer_id -ne $producerID) {
        throw "Follower accepted block producer_id $($followerBlock.producer_id), expected $producerID"
    }

    Write-Host ""
    Write-Host "Two-node staging rehearsal passed." -ForegroundColor Green
    Write-Host "Producer tip: $producerTip"
    Write-Host "Follower tip: $followerTip"
    Write-Host "Matched block: height=$producerTip hash=$($producerBlock.hash)"
    Write-Host "Producer ID: $producerID"
    Write-Host "State: $stageRoot"
} finally {
    Stop-QsdmNode -Process $follower
    Stop-QsdmNode -Process $networkProducer
    Stop-QsdmNode -Process $soloProducer
}
