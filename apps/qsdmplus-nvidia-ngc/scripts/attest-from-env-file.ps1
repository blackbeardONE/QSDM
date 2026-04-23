# Read an env file (KEY=VALUE, # comments ok) and run local-attest.ps1.
#
# Designed to be called from a Windows Scheduled Task:
#   powershell -NoProfile -ExecutionPolicy Bypass -File
#     "E:\Projects\QSDM+\apps\qsdmplus-nvidia-ngc\scripts\attest-from-env-file.ps1"
#     -EnvFile "E:\Projects\QSDM+\apps\qsdmplus-nvidia-ngc\ngc.local.env"
#
# ngc.local.env is gitignored (.gitignore rule "**/ngc.local.env") so your
# real QSDMPLUS_NGC_INGEST_SECRET never gets pushed. Ship a templated
# ngc.env.example in the repo (already present) and keep the real secret
# only on the machine that runs the attestation loop.

param(
    [Parameter(Mandatory = $true)]
    [string]$EnvFile,

    [string]$NodeId,
    [switch]$Quiet,
    # Optional: forwarded to local-attest.ps1. When set, the run is
    # transcripted to this file with built-in rotation (see
    # local-attest.ps1 -LogPath / -LogMaxBytes / -LogKeep).
    [string]$LogPath,
    [int]$LogMaxBytes = 10485760,
    [int]$LogKeep = 3
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $EnvFile)) {
    Write-Error "env file not found: $EnvFile"
    exit 2
}

Get-Content $EnvFile | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#")) { return }
    $eq = $line.IndexOf("=")
    if ($eq -lt 1) { return }
    $k = $line.Substring(0, $eq).Trim()
    $v = $line.Substring($eq + 1).Trim()
    if ($v.StartsWith('"') -and $v.EndsWith('"')) {
        $v = $v.Substring(1, $v.Length - 2)
    }
    Set-Item -Path ("Env:{0}" -f $k) -Value $v
}

$wrapper = Join-Path $PSScriptRoot "local-attest.ps1"
if (-not (Test-Path $wrapper)) {
    Write-Error "local-attest.ps1 not found beside this script"
    exit 2
}

# Build a hashtable and splat it — avoids PowerShell's $args automatic
# variable rules (which silently ate a leading "-" in some scheduled-task
# contexts during local testing and caused -Quiet to bind positionally
# to -Url in the downstream wrapper).
$splat = @{}
if ($NodeId)   { $splat.NodeId      = $NodeId }
if ($Quiet)    { $splat.Quiet       = $true }
if ($LogPath)  { $splat.LogPath     = $LogPath;
                 $splat.LogMaxBytes = $LogMaxBytes;
                 $splat.LogKeep     = $LogKeep }

& $wrapper @splat
exit $LASTEXITCODE
