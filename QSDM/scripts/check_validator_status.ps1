# Check a QSDM validator using the current v1 API routes.
#
# Examples:
#   .\check_validator_status.ps1
#   .\check_validator_status.ps1 -BaseUrl https://api.qsdm.tech

[CmdletBinding()]
param(
    [string]$BaseUrl = "http://127.0.0.1:8080",
    [ValidateRange(1, 120)]
    [int]$TimeoutSec = 10,
    [switch]$Json
)

$ErrorActionPreference = "Stop"
$BaseUrl = $BaseUrl.TrimEnd("/")
$ReadyUrl = "$BaseUrl/api/v1/health/ready"
$StatusUrl = "$BaseUrl/api/v1/status"

function Mask-NodeId([object]$Value) {
    if ($null -eq $Value) { return "-" }
    $text = [string]$Value
    if ($text.Length -le 14) { return $text }
    return "$($text.Substring(0, 8))...$($text.Substring($text.Length - 6))"
}

try {
    $ready = Invoke-WebRequest -Uri $ReadyUrl -TimeoutSec $TimeoutSec -UseBasicParsing
    if ($ready.StatusCode -ne 200) {
        throw "Readiness endpoint returned HTTP $($ready.StatusCode)"
    }

    $status = Invoke-RestMethod -Uri $StatusUrl -TimeoutSec $TimeoutSec
    $consensus = $status.consensus_auth
    $chainHeight = if ($null -ne $status.chain_height) { $status.chain_height } else { $status.chain_tip }
    $peerCount = if ($null -ne $status.connected_peers) { $status.connected_peers } else { $status.peers }
    $result = [ordered]@{
        base_url = $BaseUrl
        ready = $true
        node_id_masked = Mask-NodeId $status.node_id
        version = [string]$status.version
        node_role = [string]$status.node_role
        chain_height = $chainHeight
        connected_peers = $peerCount
        consensus_auth = $consensus
    }

    if ($Json) {
        $result | ConvertTo-Json -Depth 8
        exit 0
    }

    Write-Host "QSDM validator status" -ForegroundColor Cyan
    Write-Host "  endpoint:          $BaseUrl"
    Write-Host "  readiness:         HTTP $($ready.StatusCode)" -ForegroundColor Green
    Write-Host "  node:               $($result.node_id_masked)"
    Write-Host "  version:            $($result.version)"
    Write-Host "  role:               $($result.node_role)"
    Write-Host "  chain height:       $($result.chain_height)"
    Write-Host "  connected peers:    $($result.connected_peers)"
    if ($null -ne $consensus) {
        Write-Host "  signed supported:   $($consensus.signed_consensus_supported)"
        Write-Host "  signed required:    $($consensus.require_signed_votes)"
        Write-Host "  signed active:      $($consensus.signed_consensus_active)"
        Write-Host "  unsigned accepted:  $($consensus.unsigned_consensus_traffic_accepted)"
    } else {
        Write-Host "  consensus auth:     unavailable in this node response" -ForegroundColor Yellow
    }
    exit 0
} catch {
    Write-Error "QSDM validator check failed for ${BaseUrl}: $($_.Exception.Message)"
    exit 1
}