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
    [switch]$Quiet,
    # When -LogPath is set, the whole run (incl. python stdout/stderr)
    # is captured with Start-Transcript into that file. The file is
    # rotated before opening if it is already >= $LogMaxBytes (default
    # 10 MiB), keeping up to $LogKeep archives as .1, .2, ... This is
    # what Scheduled Task invocations should use — without rotation,
    # a long-running refresh loop (every 10 min => ~144 runs/day) grew
    # the log into the hundreds of MB over a week.
    [string]$LogPath,
    [int]$LogMaxBytes = 10485760,
    [int]$LogKeep = 3
)

$ErrorActionPreference = "Stop"

function Rotate-LogIfNeeded {
    param([string]$Path, [int]$MaxBytes, [int]$Keep)
    if (-not $Path) { return }
    if (-not (Test-Path $Path)) { return }
    $size = (Get-Item $Path).Length
    if ($size -lt $MaxBytes) { return }
    # Shift ring: .(Keep-1) -> drop, older -> .+1, current -> .1
    for ($i = $Keep - 1; $i -ge 1; $i--) {
        $src = "$Path.$i"
        $dst = "$Path.$($i + 1)"
        if (Test-Path $src) {
            if ($i + 1 -gt $Keep) {
                Remove-Item -LiteralPath $src -Force -ErrorAction SilentlyContinue
            } else {
                Move-Item -LiteralPath $src -Destination $dst -Force -ErrorAction SilentlyContinue
            }
        }
    }
    Move-Item -LiteralPath $Path -Destination "$Path.1" -Force -ErrorAction SilentlyContinue
}

$transcriptStarted = $false
if ($LogPath) {
    $logDir = Split-Path -Path $LogPath -Parent
    if ($logDir -and -not (Test-Path $logDir)) {
        New-Item -ItemType Directory -Path $logDir -Force | Out-Null
    }
    Rotate-LogIfNeeded -Path $LogPath -MaxBytes $LogMaxBytes -Keep $LogKeep
    try {
        Start-Transcript -Path $LogPath -Append -IncludeInvocationHeader | Out-Null
        $transcriptStarted = $true
    } catch {
        Write-Host "warn: could not start transcript at ${LogPath}: $_" -ForegroundColor Yellow
    }
}

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

try {
    if ($LoopMinutes -le 0) {
        $rc = Invoke-Attestation
        if ($transcriptStarted) { try { Stop-Transcript | Out-Null } catch {} }
        exit $rc
    }

    if (-not $Quiet) {
        Write-Host ("Attesting every {0} minute(s); Ctrl+C to stop." -f $LoopMinutes) -ForegroundColor Cyan
    }
    while ($true) {
        [void](Invoke-Attestation)
        # Re-check the log size between iterations so a long-running
        # loop eventually rotates even without a process restart. We
        # stop the current transcript, rotate, and reopen — this is
        # the cheapest way to get "line count" style rolling in PS 5.1.
        if ($transcriptStarted -and $LogPath) {
            try { Stop-Transcript | Out-Null } catch {}
            Rotate-LogIfNeeded -Path $LogPath -MaxBytes $LogMaxBytes -Keep $LogKeep
            try {
                Start-Transcript -Path $LogPath -Append -IncludeInvocationHeader | Out-Null
            } catch {
                Write-Host "warn: could not reopen transcript at ${LogPath}: $_" -ForegroundColor Yellow
                $transcriptStarted = $false
            }
        }
        Start-Sleep -Seconds ($LoopMinutes * 60)
    }
} finally {
    if ($transcriptStarted) { try { Stop-Transcript | Out-Null } catch {} }
}
