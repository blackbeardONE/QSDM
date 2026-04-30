# Example: set env vars so docker-compose validators POST proof bundles to a local QSDM API.
# 1) Start the node with the same secret:  $env:QSDM_NGC_INGEST_SECRET = "Charming123"
# 2) From this directory:  .\scripts\wire-qsdm.ps1 -ApiPort 8080 -Secret "Charming123"
#    Optional node binding (must match node's QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID):
#    .\scripts\wire-qsdm.ps1 -ApiPort 8080 -Secret "..." -ProofNodeId "validator-1"
# 3)  docker compose up --build

param(
    [int]$ApiPort = 8080,
    [string]$Secret = "",
    [string]$ProofNodeId = "",
    [string]$ProofHMACSecret = ""
)

if ([string]::IsNullOrWhiteSpace($Secret)) {
    Write-Host "Usage: .\scripts\wire-qsdm.ps1 -ApiPort 8080 -Secret (same as node) [-ProofNodeId id] [-ProofHMACSecret s]" -ForegroundColor Yellow
    exit 1
}

# Export under BOTH the preferred QSDM_* name and the legacy
# QSDMPLUS_* alias so docker-compose containers running mixed sidecar
# versions all see the value (the post-rebrand sidecar reads QSDM_*
# first, the deprecation-window sidecar reads QSDMPLUS_*; setting
# both is cheap and removes a foot-gun). The qsdmplus -> qsdm rebrand
# previously collapsed the QSDMPLUS_* lines into duplicates of the
# QSDM_* lines (incl. an inert self-assignment of QSDM_NGC_REPORT_URL),
# silently breaking any container still on the legacy name.
$env:QSDM_NGC_INGEST_SECRET = $Secret
$env:QSDM_NGC_REPORT_URL = "http://host.docker.internal:$ApiPort/api/v1/monitoring/ngc-proof"
$env:QSDMPLUS_NGC_INGEST_SECRET = $Secret
$env:QSDMPLUS_NGC_REPORT_URL = $env:QSDM_NGC_REPORT_URL
if (![string]::IsNullOrWhiteSpace($ProofNodeId)) {
    $env:QSDM_NGC_PROOF_NODE_ID = $ProofNodeId
    $env:QSDMPLUS_NGC_PROOF_NODE_ID = $ProofNodeId
}
if (![string]::IsNullOrWhiteSpace($ProofHMACSecret)) {
    $env:QSDM_NGC_PROOF_HMAC_SECRET = $ProofHMACSecret
    $env:QSDMPLUS_NGC_PROOF_HMAC_SECRET = $ProofHMACSecret
}
Write-Host "QSDM_NGC_REPORT_URL=$($env:QSDM_NGC_REPORT_URL)" -ForegroundColor Green
Write-Host "QSDM_NGC_INGEST_SECRET=(set)" -ForegroundColor Green
if (![string]::IsNullOrWhiteSpace($ProofNodeId)) {
    Write-Host "QSDM_NGC_PROOF_NODE_ID=$ProofNodeId" -ForegroundColor Green
}
if (![string]::IsNullOrWhiteSpace($ProofHMACSecret)) {
    Write-Host "QSDM_NGC_PROOF_HMAC_SECRET=(set)" -ForegroundColor Green
}
Write-Host "Run: docker compose up --build" -ForegroundColor Cyan
Write-Host ""
Write-Host "Ops checklist (better NGC utilization):" -ForegroundColor DarkGray
Write-Host "  - Node: set QSDM_NGC_INGEST_SECRET to the same value (enables POST .../ngc-proof)." -ForegroundColor DarkGray
Write-Host "  - NVIDIA-lock: align QSDM_NGC_PROOF_NODE_ID with node QSDM_NVIDIA_LOCK_EXPECTED_NODE_ID when binding." -ForegroundColor DarkGray
Write-Host "  - If node uses ingest nonce: set QSDM_NGC_FETCH_CHALLENGE=true on sidecar; optional QSDM_NGC_CHALLENGE_JITTER_MAX_SEC for many validators." -ForegroundColor DarkGray
Write-Host "  - GPU proofs: use Dockerfile.ngc / validator-gpu so gpu_fingerprint.available=true in bundles." -ForegroundColor DarkGray
Write-Host "  - Metrics: qsdm_ngc_proof_ingest_* on /api/metrics/prometheus (dashboard + alerts)." -ForegroundColor DarkGray
