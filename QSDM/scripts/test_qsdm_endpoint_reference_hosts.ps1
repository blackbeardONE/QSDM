#requires -Version 5.1

[CmdletBinding()]
param(
    [string]$QsdmRoot = ""
)

if ([string]::IsNullOrWhiteSpace($QsdmRoot)) {
    $scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
    $QsdmRoot = (Resolve-Path (Join-Path $scriptDirectory "..")).Path
}

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

. (Join-Path $QsdmRoot "scripts\lib\qsdm-endpoints.ps1")

function Assert-Contains {
    param(
        [string[]]$Values,
        [string]$Expected,
        [string]$Message
    )

    if ($Values -notcontains $Expected) {
        throw "$Message Missing '$Expected' from '$($Values -join ', ')'."
    }
}

function Assert-True {
    param([bool]$Value, [string]$Message)

    if (-not $Value) {
        throw $Message
    }
}

$bootstrapPeer = "/dns4/bootstrap.new-vps.example/tcp/4001/p2p/12D3KooWJ8XnsdHyr7dPXWVsLzRD3mDxFCPkGA8EvBKMYe81rLPQ"
$referenceHosts = @(Get-QsdmReferenceEndpointHosts `
    -CoreApiBases @("https://api.new-vps.example/api/v1", "https://sync.new-vps.example/api/v1") `
    -Relays @("https://relay.new-vps.example") `
    -BootstrapPeers @($bootstrapPeer, "/ip4/198.51.100.23/tcp/4001/p2p/12D3KooWJNWNgx8SAdGqVEcsM9AapVk7byPp4BkJnBba2rqUFXEJ") `
    -SshTargets @("root@node.new-vps.example", "ssh://deployer@admin.new-vps.example:2222"))

foreach ($expectedHost in @(
    "api.new-vps.example",
    "sync.new-vps.example",
    "relay.new-vps.example",
    "bootstrap.new-vps.example",
    "198.51.100.23",
    "node.new-vps.example",
    "admin.new-vps.example"
)) {
    Assert-Contains -Values $referenceHosts -Expected $expectedHost `
        -Message "Configured reference endpoints must contribute their host names."
}

Assert-True -Value (-not (Test-QsdmNonReferenceEndpointHost `
    -CandidateHost "API.NEW-VPS.EXAMPLE." -ReferenceHosts $referenceHosts)) `
    -Message "A configured API host must never be treated as an independent source."
Assert-True -Value (Test-QsdmNonReferenceEndpointHost `
    -CandidateHost "peer.independent.example" -ReferenceHosts $referenceHosts) `
    -Message "A host outside the configured public reference set must remain eligible as an alternate source."

$validatorScript = Get-Content -LiteralPath (Join-Path $QsdmRoot "scripts\validate_vps_independence.ps1") -Raw
Assert-True -Value ($validatorScript -match "Get-QsdmReferenceEndpointHosts") `
    -Message "The independence verifier must use the shared reference-host helper."
Assert-True -Value (-not ($validatorScript -match "api\\.qsdm\\.tech")) `
    -Message "The independence verifier must not hard-code one public API hostname."
Assert-True -Value (-not ($validatorScript -match "QSDM_CHAIN_SYNC_URLS")) `
    -Message "Runtime chain-sync overrides must not be reclassified as public reference infrastructure."

[pscustomobject]@{
    schema = "qsdm.endpoint-reference-hosts-test.v1"
    success = $true
    reference_sources_derived = $true
    configured_hosts = $referenceHosts.Count
    configured_host_is_rejected = $true
    independent_host_is_allowed = $true
} | ConvertTo-Json -Compress