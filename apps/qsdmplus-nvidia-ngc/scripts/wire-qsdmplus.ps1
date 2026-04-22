# Example: set env vars so docker-compose validators POST proof bundles to a local QSDM+ API.
# 1) Start the node with the same secret:  $env:QSDMPLUS_NGC_INGEST_SECRET = "Charming123"
# 2) From this directory:  .\scripts\wire-qsdmplus.ps1 -ApiPort 8080 -Secret "Charming123"
#    Optional node binding (must match node's QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID):
#    .\scripts\wire-qsdmplus.ps1 -ApiPort 8080 -Secret "..." -ProofNodeId "validator-1"
# 3)  docker compose up --build

param(
    [int]$ApiPort = 8080,
    [string]$Secret = "",
    [string]$ProofNodeId = "",
    [string]$ProofHMACSecret = ""
)

if ([string]::IsNullOrWhiteSpace($Secret)) {
    Write-Host "Usage: .\scripts\wire-qsdmplus.ps1 -ApiPort 8080 -Secret (same as node) [-ProofNodeId id] [-ProofHMACSecret s]" -ForegroundColor Yellow
    exit 1
}

$env:QSDMPLUS_NGC_INGEST_SECRET = $Secret
$env:QSDMPLUS_NGC_REPORT_URL = "http://host.docker.internal:$ApiPort/api/v1/monitoring/ngc-proof"
$env:QSDM_NGC_INGEST_SECRET = $Secret
$env:QSDM_NGC_REPORT_URL = $env:QSDMPLUS_NGC_REPORT_URL
if (![string]::IsNullOrWhiteSpace($ProofNodeId)) {
    $env:QSDMPLUS_NGC_PROOF_NODE_ID = $ProofNodeId
    $env:QSDM_NGC_PROOF_NODE_ID = $ProofNodeId
}
if (![string]::IsNullOrWhiteSpace($ProofHMACSecret)) {
    $env:QSDMPLUS_NGC_PROOF_HMAC_SECRET = $ProofHMACSecret
    $env:QSDM_NGC_PROOF_HMAC_SECRET = $ProofHMACSecret
}
Write-Host "QSDMPLUS_NGC_REPORT_URL=$($env:QSDMPLUS_NGC_REPORT_URL)" -ForegroundColor Green
Write-Host "QSDMPLUS_NGC_INGEST_SECRET=(set)" -ForegroundColor Green
if (![string]::IsNullOrWhiteSpace($ProofNodeId)) {
    Write-Host "QSDMPLUS_NGC_PROOF_NODE_ID=$ProofNodeId" -ForegroundColor Green
}
if (![string]::IsNullOrWhiteSpace($ProofHMACSecret)) {
    Write-Host "QSDMPLUS_NGC_PROOF_HMAC_SECRET=(set)" -ForegroundColor Green
}
Write-Host "Run: docker compose up --build" -ForegroundColor Cyan
Write-Host ""
Write-Host "Ops checklist (better NGC utilization):" -ForegroundColor DarkGray
Write-Host "  - Node: set QSDMPLUS_NGC_INGEST_SECRET to the same value (enables POST .../ngc-proof)." -ForegroundColor DarkGray
Write-Host "  - NVIDIA-lock: align QSDMPLUS_NGC_PROOF_NODE_ID with node QSDMPLUS_NVIDIA_LOCK_EXPECTED_NODE_ID when binding." -ForegroundColor DarkGray
Write-Host "  - If node uses ingest nonce: set QSDMPLUS_NGC_FETCH_CHALLENGE=true on sidecar; optional QSDMPLUS_NGC_CHALLENGE_JITTER_MAX_SEC for many validators." -ForegroundColor DarkGray
Write-Host "  - GPU proofs: use Dockerfile.ngc / validator-gpu so gpu_fingerprint.available=true in bundles." -ForegroundColor DarkGray
Write-Host "  - Metrics: qsdmplus_ngc_proof_ingest_* on /api/metrics/prometheus (dashboard + alerts)." -ForegroundColor DarkGray
