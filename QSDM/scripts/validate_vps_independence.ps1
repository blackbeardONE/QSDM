param(
    [string]$QsdmRoot = "",
    [string]$Relay = "",
    [string]$Slot = "",
    [switch]$AcceptFallback
)

$ErrorActionPreference = "Stop"
if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $QsdmRoot = (Resolve-Path (Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) "..")).Path
}
$QsdmRoot = (Resolve-Path $QsdmRoot).Path
. (Join-Path $QsdmRoot "scripts\lib\qsdm-endpoints.ps1")
$Relay = Resolve-QsdmEndpointValue -Value $Relay -Name "home_gateway_relay" -EnvVar "QSDM_HOME_GATEWAY_RELAY"
$Slot = Resolve-QsdmEndpointValue -Value $Slot -Name "home_gateway_slot" -EnvVar "QSDM_HOME_GATEWAY_SLOT"
$localRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$modePath = Join-Path $localRoot "validator-mode.json"
$checks = [ordered]@{}

function Add-Check {
    param([string]$Name, [bool]$Passed, [string]$Detail)
    $checks[$Name] = [ordered]@{ passed = $Passed; detail = $Detail }
}

function Test-ChecksPassed {
    param([string[]]$Names)
    foreach ($name in $Names) {
        if (-not $checks.Contains($name)) {
            return $false
        }
        if (-not [bool]$checks[$name].passed) {
            return $false
        }
    }
    return $true
}

function Test-HttpOk {
    param([string]$Url, [int]$TimeoutSeconds = 8)
    try {
        $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec $TimeoutSeconds
        return ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300)
    } catch {
        return $false
    }
}

$mode = $null
$cgnatFallback = $false
if (-not (Test-Path -LiteralPath $modePath -PathType Leaf)) {
    Add-Check "role_profile" $false "Missing validator-mode.json"
    Add-Check "public_p2p_requested" $false "publicP2P=False"
    Add-Check "cgnat_fallback_enabled" $false "cgnatFallback=False"
    Add-Check "alternate_bootstrap" $false "non-VPS bootstrap peers=0"
    Add-Check "alternate_chain_sync" $false "non-VPS HTTPS sources=0"
} else {
    try {
        $mode = Get-Content -Raw -LiteralPath $modePath | ConvertFrom-Json
        $blockProducer = $false
        if ($null -ne $mode.PSObject.Properties["blockProducer"]) {
            $blockProducer = [bool]$mode.blockProducer
        }
        $publicP2P = $false
        if ($null -ne $mode.PSObject.Properties["publicP2P"]) {
            $publicP2P = [bool]$mode.publicP2P
        }
        if ($null -ne $mode.PSObject.Properties["cgnatFallback"]) {
            $cgnatFallback = [bool]$mode.cgnatFallback
        }
        $connectivityMode = if ($null -ne $mode.PSObject.Properties["connectivityMode"] -and -not [string]::IsNullOrWhiteSpace([string]$mode.connectivityMode)) {
            [string]$mode.connectivityMode
        } elseif ($cgnatFallback) {
            "relay-fallback"
        } elseif ($publicP2P) {
            "public-p2p"
        } else {
            "local-only"
        }

        Add-Check "role_profile" ([string]$mode.mode -eq "networked" -and -not $blockProducer) ("mode={0}, blockProducer={1}" -f $mode.mode, $blockProducer)
        Add-Check "public_p2p_requested" $publicP2P ("publicP2P={0}" -f $publicP2P)
        Add-Check "cgnat_fallback_enabled" $cgnatFallback ("cgnatFallback={0}, connectivityMode={1}" -f $cgnatFallback, $connectivityMode)
        $bootstrap = @([string]$mode.bootstrapPeers -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
        $independentBootstrap = @($bootstrap | Where-Object { $_ -notmatch '(?i)api\.qsdm\.tech' -and $_ -match '/p2p/' })
        Add-Check "alternate_bootstrap" ($independentBootstrap.Count -gt 0) ("non-VPS bootstrap peers={0}" -f $independentBootstrap.Count)
        $sync = @([string]$mode.chainSyncUrls -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
        $independentSync = @($sync | Where-Object { $_ -notmatch '(?i)api\.qsdm\.tech' -and $_ -match '^https://' })
        Add-Check "alternate_chain_sync" ($independentSync.Count -gt 0) ("non-VPS HTTPS sources={0}" -f $independentSync.Count)
    } catch {
        Add-Check "role_profile" $false "Invalid validator-mode.json: $($_.Exception.Message)"
        Add-Check "public_p2p_requested" $false "Cannot read publicP2P"
        Add-Check "cgnat_fallback_enabled" $false "Cannot read cgnatFallback"
        Add-Check "alternate_bootstrap" $false "Cannot read bootstrap peers"
        Add-Check "alternate_chain_sync" $false "Cannot read chain sync URLs"
    }
}

try {
    $listeners = @(Get-NetTCPConnection -LocalPort 4001 -State Listen -ErrorAction Stop)
    $public = @($listeners | Where-Object { $_.LocalAddress -in @("0.0.0.0", "::", "*") }).Count -gt 0
    Add-Check "p2p_listener" $public ("listeners={0}" -f (($listeners | ForEach-Object { "$($_.LocalAddress):$($_.LocalPort)" }) -join ', '))
} catch {
    Add-Check "p2p_listener" $false "Could not inspect TCP 4001: $($_.Exception.Message)"
}

try {
    $status = Invoke-RestMethod -Uri "http://127.0.0.1:8080/api/v1/status" -TimeoutSec 8
    $peerCount = [int]$status.peers
    Add-Check "local_core_ready" $true ("chain_tip={0}, peers={1}, role={2}" -f $status.chain_tip, $peerCount, $status.node_role)
    Add-Check "live_peer" ($peerCount -gt 0) ("peers={0}" -f $peerCount)
} catch {
    Add-Check "local_core_ready" $false "Local API unavailable: $($_.Exception.Message)"
    Add-Check "live_peer" $false "Cannot read local peer count"
}

$relayBase = $Relay.TrimEnd('/')
$publicGatewayUrl = "$relayBase/attest/$Slot/api/v1/status"
if ($cgnatFallback) {
    Add-Check "relay_gateway_surface" (Test-HttpOk -Url $publicGatewayUrl -TimeoutSeconds 10) "url=$publicGatewayUrl"
} else {
    Add-Check "relay_gateway_surface" $false "CGNAT fallback is not enabled"
}

$independenceChecks = @(
    "role_profile",
    "public_p2p_requested",
    "alternate_bootstrap",
    "alternate_chain_sync",
    "p2p_listener",
    "local_core_ready",
    "live_peer"
)
$fallbackChecks = @(
    "role_profile",
    "cgnat_fallback_enabled",
    "local_core_ready",
    "relay_gateway_surface"
)
$independenceReady = Test-ChecksPassed -Names $independenceChecks
$fallbackReady = Test-ChecksPassed -Names $fallbackChecks
$posture = if ($independenceReady) {
    "independent"
} elseif ($fallbackReady) {
    "relay-fallback"
} else {
    "not-ready"
}

[ordered]@{
    schema = "qsdm.vps-independence-check.v2"
    checked_at_utc = [DateTime]::UtcNow.ToString("o")
    independence_ready = $independenceReady
    cgnat_fallback_ready = $fallbackReady
    operational_posture = $posture
    fallback_accepted_for_exit = [bool]$AcceptFallback
    checks = $checks
} | ConvertTo-Json -Depth 6

if (-not $independenceReady -and -not ($AcceptFallback -and $fallbackReady)) { exit 2 }