$ErrorActionPreference = "Stop"

$harness = Join-Path $PSScriptRoot "staging_two_node_consensus.ps1"
$stateRoot = Join-Path $env:TEMP "qsdm-two-node-dry-run-$PID"
$raw = & $harness `
    -DryRun `
    -NodeAUrl "http://127.0.0.1:18080" `
    -NodeBUrl "http://127.0.0.1:28080" `
    -NodeAP2PPort 14001 `
    -NodeBP2PPort 24001 `
    -StateRoot $stateRoot | Out-String
if ($LASTEXITCODE -ne 0) {
    throw "Harness dry-run exited with $LASTEXITCODE"
}
$plan = $raw | ConvertFrom-Json

if ($plan.mode -ne "isolated-two-node-staging" -or $plan.launches_processes) {
    throw "Unexpected dry-run mode: $raw"
}
if ($plan.node_a.api_url -notmatch '^http://127\.0\.0\.1:' -or
    $plan.node_b.api_url -notmatch '^http://127\.0\.0\.1:') {
    throw "Dry-run URLs are not explicit loopback endpoints"
}
if ($plan.node_a.state_dir -eq $plan.node_b.state_dir) {
    throw "Node state directories must be isolated"
}
if ($raw -match 'api\.qsdm\.tech') {
    throw "Harness must never point at the production API"
}
if (Test-Path -LiteralPath $stateRoot) {
    throw "Dry-run must not create staging state"
}
if ($plan.required_environment.QSDM_NETWORKED_CATCHUP_MODE -ne "0") {
    throw "Active staging producers must not run in catch-up-only mode"
}

Write-Output "two-node consensus harness dry-run tests passed"
