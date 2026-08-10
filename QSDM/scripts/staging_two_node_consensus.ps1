[CmdletBinding()]
param(
    [string]$NodeAUrl = "http://127.0.0.1:18080",
    [string]$NodeBUrl = "http://127.0.0.1:28080",
    [string]$StateRoot = (Join-Path $env:TEMP "qsdm-two-node-consensus-staging"),
    [int]$NodeAP2PPort = 14001,
    [int]$NodeBP2PPort = 24001,
    [switch]$DryRun,
    [string]$FailoverProducerId = "",
    [ValidateRange(5, 300)]
    [int]$FailoverWaitSeconds = 30
)

$ErrorActionPreference = "Stop"

function Assert-LoopbackUrl {
    param([Parameter(Mandatory)][string]$Url)

    $uri = [Uri]$Url
    if ($uri.Scheme -ne "http" -or $uri.Host -notin @("127.0.0.1", "localhost", "::1")) {
        throw "Staging URLs must use loopback HTTP, got: $Url"
    }
    if ($uri.AbsolutePath -ne "/") {
        throw "Staging URLs must not include a path: $Url"
    }
}

function Get-MetricValue {
    param(
        [Parameter(Mandatory)][string]$Text,
        [Parameter(Mandatory)][string]$Name
    )

    $pattern = "(?m)^$([regex]::Escape($Name))(?:\{[^}]*\})?\s+([-+0-9.eE]+)\s*$"
    $match = [regex]::Match($Text, $pattern)
    if (-not $match.Success) {
        throw "Required metric is absent: $Name"
    }
    return [double]::Parse($match.Groups[1].Value, [Globalization.CultureInfo]::InvariantCulture)
}

function Get-NodeSnapshot {
    param([Parameter(Mandatory)][string]$Url)

    return [ordered]@{
        status = Invoke-RestMethod -Method Get -Uri "$Url/api/v1/status" -TimeoutSec 5
        validators = Invoke-RestMethod -Method Get -Uri "$Url/api/v1/validators" -TimeoutSec 5
        blocks = Invoke-RestMethod -Method Get -Uri "$Url/api/v1/mining/blocks?limit=64" -TimeoutSec 5
        metrics = (Invoke-WebRequest -Method Get -Uri "$Url/metrics" -UseBasicParsing -TimeoutSec 5).Content
    }
}

Assert-LoopbackUrl -Url $NodeAUrl
Assert-LoopbackUrl -Url $NodeBUrl
if ($NodeAUrl -eq $NodeBUrl) {
    throw "Node API URLs must be distinct"
}
if ($NodeAP2PPort -eq $NodeBP2PPort) {
    throw "Node P2P ports must be distinct"
}

$nodeAState = Join-Path $StateRoot "node-a"
$nodeBState = Join-Path $StateRoot "node-b"
$plan = [ordered]@{
    mode = "isolated-two-node-staging"
    launches_processes = $false
    production_endpoints_allowed = $false
    node_a = [ordered]@{
        api_url = $NodeAUrl
        api_port = ([Uri]$NodeAUrl).Port
        p2p_port = $NodeAP2PPort
        state_dir = $nodeAState
    }
    node_b = [ordered]@{
        api_url = $NodeBUrl
        api_port = ([Uri]$NodeBUrl).Port
        p2p_port = $NodeBP2PPort
        state_dir = $nodeBState
    }
    required_environment = [ordered]@{
        QSDM_SOLO_VALIDATOR_MODE = "0"
        QSDM_NETWORK_BLOCK_PRODUCER = "1"
        QSDM_NETWORKED_CATCHUP_MODE = "0"
        QSDM_API_BIND_ADDRESS = "127.0.0.1"
        QSDM_NETWORK_BIND_ADDRESS = "127.0.0.1"
        QSDM_CHAIN_SYNC_URLS = ""
    }
    pass_requires = @(
        "both peer_count values are positive",
        "validator membership and epoch match",
        "qsdm_consensus_peer_vote_reactor_ready equals 1",
        "qsdm_consensus_peer_vote_commits_total is positive",
        "qsdm_consensus_synthetic_preseal_commits_total equals 0",
        "at least two producer identities appear in canonical headers",
        "the chain advances after the selected producer is stopped"
    )
}

if ($DryRun) {
    $plan | ConvertTo-Json -Depth 8
    exit 0
}

$a = Get-NodeSnapshot -Url $NodeAUrl
$b = Get-NodeSnapshot -Url $NodeBUrl
if ([int]$a.status.peer_count -lt 1 -or [int]$b.status.peer_count -lt 1) {
    throw "FAIL: both nodes must report at least one connected peer"
}

$aMembers = @($a.validators.validators | Sort-Object address | ForEach-Object {
    "$($_.address)|$($_.stake)|$($_.status)"
})
$bMembers = @($b.validators.validators | Sort-Object address | ForEach-Object {
    "$($_.address)|$($_.stake)|$($_.status)"
})
if (($aMembers -join "`n") -ne ($bMembers -join "`n") -or
    [uint64]$a.validators.epoch -ne [uint64]$b.validators.epoch) {
    throw "FAIL: nodes do not report matching validator membership and epoch"
}

$reactorA = Get-MetricValue -Text $a.metrics -Name "qsdm_consensus_peer_vote_reactor_ready"
$reactorB = Get-MetricValue -Text $b.metrics -Name "qsdm_consensus_peer_vote_reactor_ready"
if ($reactorA -ne 1 -or $reactorB -ne 1) {
    Write-Output "BLOCKED: live peer-vote reactor is not ready on both nodes; synthetic preseal cannot satisfy this harness"
    exit 3
}

foreach ($node in @($a, $b)) {
    if ((Get-MetricValue -Text $node.metrics -Name "qsdm_consensus_peer_vote_commits_total") -lt 1) {
        throw "FAIL: a node has no peer-vote commit evidence"
    }
    if ((Get-MetricValue -Text $node.metrics -Name "qsdm_consensus_synthetic_preseal_commits_total") -ne 0) {
        throw "FAIL: synthetic preseal was observed in a two-validator staging run"
    }
}

if ([uint64]$a.blocks.tip -ne [uint64]$b.blocks.tip) {
    throw "FAIL: node tips differ"
}
$producerIds = @($a.blocks.headers | ForEach-Object { [string]$_.producer_id } | Where-Object { $_ } | Sort-Object -Unique)
if ($producerIds.Count -lt 2) {
    throw "FAIL: producer identity rotation was not observed"
}
if ([string]::IsNullOrWhiteSpace($FailoverProducerId)) {
    Write-Output "BLOCKED: rotation passed; provide -FailoverProducerId after stopping that producer to verify failover"
    exit 3
}

$startTip = [uint64]$a.blocks.tip
$deadline = (Get-Date).AddSeconds($FailoverWaitSeconds)
do {
    Start-Sleep -Seconds 1
    $after = Get-NodeSnapshot -Url $NodeAUrl
    $latest = @($after.blocks.headers | Sort-Object height | Select-Object -Last 1)
    if ([uint64]$after.blocks.tip -gt $startTip -and
        [string]$latest.producer_id -ne $FailoverProducerId) {
        Write-Output "PASS: peer connection, validator agreement, peer-vote provenance, rotation, and failover verified"
        exit 0
    }
} while ((Get-Date) -lt $deadline)

throw "FAIL: chain did not advance under a different producer within $FailoverWaitSeconds seconds"
