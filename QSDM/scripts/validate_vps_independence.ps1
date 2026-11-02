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

$referenceCoreApiBases = @(
    (Get-QsdmEndpointValue -Name "core_api_base" -EnvVar "QSDM_PUBLIC_API_BASE_URL") -split ',' |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ }
)
$referenceBootstrapPeers = @(
    (Get-QsdmEndpointValue -Name "reference_bootstrap_peer" -EnvVar "QSDM_REFERENCE_BOOTSTRAP_PEER") -split ',' |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ }
)
$referenceSshTargets = @(
    (Get-QsdmEndpointValue -Name "vps_ssh_target" -EnvVar "QSDM_RELEASE_SSH_TARGET") -split ',' |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ }
)
$referenceHosts = @(Get-QsdmReferenceEndpointHosts `
    -CoreApiBases $referenceCoreApiBases `
    -Relays @($Relay) `
    -BootstrapPeers $referenceBootstrapPeers `
    -SshTargets $referenceSshTargets)
$referenceHostDetail = if ($referenceHosts.Count -gt 0) {
    $referenceHosts -join ', '
} else {
    'none'
}

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
    Add-Check "alternate_bootstrap" $false "non-reference bootstrap peers=0; reference hosts=$referenceHostDetail"
    Add-Check "alternate_chain_sync" $false "non-reference HTTPS sources=0; reference hosts=$referenceHostDetail"
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
        $independentBootstrap = @($bootstrap | Where-Object {
            $bootstrapHost = Get-QsdmBootstrapPeerHost -Multiaddr $_
            $_ -match '/p2p/' -and (Test-QsdmNonReferenceEndpointHost -CandidateHost $bootstrapHost -ReferenceHosts $referenceHosts)
        })
        Add-Check "alternate_bootstrap" ($independentBootstrap.Count -gt 0) ("non-reference bootstrap peers={0}; reference hosts={1}" -f $independentBootstrap.Count, $referenceHostDetail)
        $sync = @([string]$mode.chainSyncUrls -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
        $independentSync = @($sync | Where-Object {
            $syncHost = Get-QsdmEndpointHost -Value $_
            $_ -match '^https://' -and (Test-QsdmNonReferenceEndpointHost -CandidateHost $syncHost -ReferenceHosts $referenceHosts)
        })
        Add-Check "alternate_chain_sync" ($independentSync.Count -gt 0) ("non-reference HTTPS sources={0}; reference hosts={1}" -f $independentSync.Count, $referenceHostDetail)
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
    reference_hosts = $referenceHosts
    checks = $checks
} | ConvertTo-Json -Depth 6

if (-not $independenceReady -and -not ($AcceptFallback -and $fallbackReady)) { exit 2 }