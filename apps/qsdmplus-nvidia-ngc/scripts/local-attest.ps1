# Post a single NGC attestation bundle to a QSDM ledger node.
#
# This is a thin PowerShell wrapper around validator_phase1.py so node
# operators on Windows can emit an attestation on demand (or from a
# scheduled task — see -LoopMinutes).
#
# Required env vars (or -Url / -Secret params):
#   QSDMPLUS_NGC_REPORT_URL     e.g. https://api.qsdm.tech/api/v1/monitoring/ngc-proof
#   QSDMPLUS_NGC_INGEST_SECRET  hex string, matches the node's QSDMPLUS_NGC_INGEST_SECRET
#
# Optional env vars:
#   QSDMPLUS_NGC_PROOF_NODE_ID  free-form label that shows up on trust pages
#                               (e.g. home-rtx3050). Never a secret.
#
# Usage:
#   .\scripts\local-attest.ps1                         # one-shot
#   .\scripts\local-attest.ps1 -LoopMinutes 12         # keep a badge green
#   .\scripts\local-attest.ps1 -NodeId home-rtx3050
#
# Exits non-zero if the POST fails or the required env is missing.

param(
    [string]$Url,
    [string]$Secret,
    [string]$NodeId,
    [int]$LoopMinutes = 0,
    [switch]$Quiet
)

$ErrorActionPreference = "Stop"

$scriptRoot = Split-Path $PSScriptRoot -Parent
$sidecar    = Join-Path $scriptRoot "validator_phase1.py"
if (-not (Test-Path $sidecar)) {
    Write-Error "validator_phase1.py not found at $sidecar"
    exit 2
}

if ($Url)    { $env:QSDMPLUS_NGC_REPORT_URL    = $Url }
if ($Secret) { $env:QSDMPLUS_NGC_INGEST_SECRET = $Secret }
if ($NodeId) { $env:QSDMPLUS_NGC_PROOF_NODE_ID = $NodeId }

if (-not $env:QSDMPLUS_NGC_REPORT_URL) {
    Write-Error "QSDMPLUS_NGC_REPORT_URL is not set. Pass -Url or set the env var."
    exit 2
}
if (-not $env:QSDMPLUS_NGC_INGEST_SECRET) {
    Write-Error "QSDMPLUS_NGC_INGEST_SECRET is not set. Pass -Secret or set the env var."
    exit 2
}

function Invoke-Attestation {
    $t0 = Get-Date
    # Inside this function we deliberately relax ErrorActionPreference so
    # PowerShell does NOT treat anything python writes to stderr as a
    # terminating error. Scheduled Task runs reach this path with the
    # preference set to Stop, which would otherwise drop the traceback.
    $prev = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $out = & python $sidecar 2>&1 | Out-String
        $ec  = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prev
    }
    $dt = (Get-Date) - $t0
    if ($ec -eq 0) {
        if (-not $Quiet) {
            Write-Host ("[{0}] attested in {1:N1}s" -f (Get-Date -Format s), $dt.TotalSeconds) -ForegroundColor Green
        }
    } else {
        Write-Host ("[{0}] attest FAILED (exit {1}) in {2:N1}s" -f (Get-Date -Format s), $ec, $dt.TotalSeconds) -ForegroundColor Red
        if ($out) { Write-Host $out }
    }
    return $ec
}

if ($LoopMinutes -le 0) {
    exit (Invoke-Attestation)
}

if (-not $Quiet) {
    Write-Host ("Attesting every {0} minute(s); Ctrl+C to stop." -f $LoopMinutes) -ForegroundColor Cyan
}
while ($true) {
    [void](Invoke-Attestation)
    Start-Sleep -Seconds ($LoopMinutes * 60)
}
