# publish_release.ps1 — upload a release directory to the
# qsdm.tech static webroot via scp.
#
# What this script does (and just as importantly, doesn't):
#
#   It uploads the public-facing release artifacts that
#   `release/<tag>/` already contains in their finished form,
#   plus the publish-side overrides for MANIFEST.json and
#   SHA256SUMS.txt (which are scrubbed copies that strip any
#   private components from the local manifest before it
#   reaches the public web root). It does NOT build, sign,
#   tag, or change /releases/latest.txt unless you pass
#   -BumpLatest, and it does NOT touch the systemd / Caddy /
#   validator services on the VPS.
#
#   Conservative on purpose: a publish script that surprises
#   you by editing latest.txt or restarting Caddy is not the
#   kind of script you want pre-commit on Sunday morning.
#
# Inputs:
#
#   -Tag         the release tag, e.g. "v0.0.0+689fbf7". If
#                empty (default), reads the most recent
#                MANIFEST.json from release/v*/ on disk.
#   -SshTarget   ssh destination, default root@node.qsdm.tech.
#                node.qsdm.tech is the documented DNS for the
#                BLR1 host (vps.txt §1).
#   -Webroot     server-side root, default /var/www/qsdm
#                (matches QSDM/deploy/Caddyfile's `root *`).
#   -DryRun      print every scp/ssh invocation but execute
#                none of them. Use this on the first run.
#   -BumpLatest  also write `<tag>\n` to /releases/latest.txt.
#                Off by default because pinning latest is the
#                step where a botched upload becomes
#                user-visible.
#   -SkipScrubCheck
#                bypass the safety check that forbids
#                uploading a MANIFEST.json containing private
#                components (qsdm-detect at the moment). Only
#                use if you've consciously decided to publish
#                a previously-private component.
#
# Typical operator flow:
#
#   # Build:
#   .\QSDM\scripts\build_release.ps1
#   .\QSDM\scripts\windows-installer\build_installer.ps1
#   .\QSDM\deploy\scripts\publish_release.ps1 -DryRun
#   # Review the printed commands, then drop -DryRun:
#   .\QSDM\deploy\scripts\publish_release.ps1
#   # Verify by curl, then promote:
#   .\QSDM\deploy\scripts\publish_release.ps1 -BumpLatest

[CmdletBinding()]
param(
    [string]$Tag = "",
    [string]$SshTarget = "root@node.qsdm.tech",
    [string]$Webroot = "/var/www/qsdm",
    [switch]$DryRun,
    [switch]$BumpLatest,
    [switch]$SkipScrubCheck,

    # Upload only the *minimum* set of files needed for a
    # post-installer re-publish: the new installer artifact
    # (qsdmminer-setup-<tag>.exe) plus the patched MANIFEST.json
    # and SHA256SUMS.txt. Skips re-uploading the ~14 raw binaries
    # that didn't change. Use this when the previous publish ran
    # cleanly and you're only adding the installer row + file.
    #
    # Safe default behavior remains a full upload, so a fresh
    # release (where no files are on the remote yet) still works
    # without a flag.
    [switch]$MinimalPatch
)

$ErrorActionPreference = 'Stop'

# Private component names that must never appear in a
# public manifest. Update this list when carving new
# components into Blackbeard. The check runs against
# MANIFEST.publish.json (or the live MANIFEST.json if no
# publish copy exists), and the upload aborts on any hit.
$PrivateComponents = @('qsdm-detect', 'qsdmminer-gui')

$ScriptDir   = Split-Path -Parent $MyInvocation.MyCommand.Path
$DeployDir   = Split-Path -Parent $ScriptDir
$RepoRoot    = Split-Path -Parent $DeployDir
$WorkspaceRoot = Split-Path -Parent $RepoRoot

# 1) Resolve the release dir.
$releaseRoot = Join-Path $WorkspaceRoot 'release'
if (-not (Test-Path $releaseRoot)) {
    Write-Error "release/ directory not found at $releaseRoot. Run build_release.ps1 first."
    exit 1
}
if (-not $Tag) {
    $latest = Get-ChildItem $releaseRoot -Directory -Filter "v*" |
        Where-Object { Test-Path (Join-Path $_.FullName "MANIFEST.json") } |
        ForEach-Object {
            [PSCustomObject]@{
                Path     = $_.FullName
                Manifest = (Get-Item (Join-Path $_.FullName "MANIFEST.json")).LastWriteTime
            }
        } | Sort-Object Manifest -Descending | Select-Object -First 1
    if (-not $latest) {
        Write-Error "No release/v* directories with a MANIFEST.json found."
        exit 1
    }
    $releaseDir = $latest.Path
    $Tag = Split-Path $releaseDir -Leaf
} else {
    $releaseDir = Join-Path $releaseRoot $Tag
    if (-not (Test-Path $releaseDir)) {
        Write-Error "Release dir not found: $releaseDir"
        exit 1
    }
}

# 2) Pick which manifest + sha256sums files to upload.
#    Prefer publish-side scrubbed copies if they exist; fall
#    back to the local versions otherwise. Always upload them
#    AS the public file names (MANIFEST.json /
#    SHA256SUMS.txt), not the publish-side basenames.
$pubManifest  = Join-Path $releaseDir 'MANIFEST.publish.json'
$pubSums      = Join-Path $releaseDir 'SHA256SUMS.publish.txt'
$liveManifest = Join-Path $releaseDir 'MANIFEST.json'
$liveSums     = Join-Path $releaseDir 'SHA256SUMS.txt'

$srcManifest = if (Test-Path $pubManifest) { $pubManifest } else { $liveManifest }
$srcSums     = if (Test-Path $pubSums)     { $pubSums }     else { $liveSums }

# 3) Privacy gate: refuse to upload a manifest that still
#    contains private components. This is the single check
#    that prevents a future "I forgot to scrub" mistake.
if (-not $SkipScrubCheck) {
    $manifestRaw = [System.IO.File]::ReadAllText($srcManifest)
    if ($manifestRaw.Length -gt 0 -and ([int][char]$manifestRaw[0]) -eq 0xFEFF) {
        $manifestRaw = $manifestRaw.Substring(1)
    }
    $manifest = $manifestRaw | ConvertFrom-Json
    $bad = @($manifest.components | Where-Object { $PrivateComponents -contains $_.component })
    if ($bad.Count -gt 0) {
        Write-Host "REFUSING TO UPLOAD: the source manifest still lists private components." -ForegroundColor Red
        Write-Host "  source: $srcManifest"
        Write-Host "  rows that must be stripped before publish:"
        $bad | ForEach-Object {
            Write-Host ("    {0} ({1}/{2}) -> {3}" -f $_.component, $_.os, $_.arch, $_.file)
        }
        Write-Host ""
        Write-Host "  Either:"
        Write-Host "    1. produce a MANIFEST.publish.json next to MANIFEST.json that omits these rows, or"
        Write-Host "    2. re-run with -SkipScrubCheck to override (you must be sure)."
        exit 2
    }
}

# 4) Walk the release dir and pick out the files to upload.
#    We use the destination manifest (the scrubbed publish
#    copy when present) as the source-of-truth: any file it
#    references must be uploaded; nothing else may be.
$manifestRaw = [System.IO.File]::ReadAllText($srcManifest)
if ($manifestRaw.Length -gt 0 -and ([int][char]$manifestRaw[0]) -eq 0xFEFF) {
    $manifestRaw = $manifestRaw.Substring(1)
}
$manifest = $manifestRaw | ConvertFrom-Json
$componentFiles = @($manifest.components | ForEach-Object { $_.file } | Sort-Object -Unique)

# In -MinimalPatch mode, narrow the component-binary list down
# to just the installer artifact (qsdmminer-installer rows).
# The manifest + sums uploads happen unconditionally below.
if ($MinimalPatch) {
    $installerFiles = @($manifest.components |
        Where-Object { $_.component -eq 'qsdmminer-installer' } |
        ForEach-Object { $_.file } | Sort-Object -Unique)
    if ($installerFiles.Count -eq 0) {
        Write-Host "REFUSING -MinimalPatch: no qsdmminer-installer row in the publish manifest." -ForegroundColor Red
        Write-Host "  Run build_installer.ps1 first, then re-run this script."
        exit 7
    }
    $componentFiles = $installerFiles
    Write-Host "(minimal-patch mode: uploading only the installer + manifest + sums)" -ForegroundColor DarkGray
}

# Final upload list: every component binary listed in the
# scrubbed manifest, plus the manifest itself and the
# scrubbed checksums file. We deliberately don't upload the
# raw .iss source or the build logs.
$uploadPlan = @()
foreach ($f in $componentFiles) {
    $local = Join-Path $releaseDir $f
    if (-not (Test-Path $local)) {
        Write-Error "manifest references missing file: $local"
        exit 3
    }
    $uploadPlan += [PSCustomObject]@{
        Local  = $local
        Remote = "$Webroot/releases/$Tag/$f"
    }
}
# MANIFEST.json: from publish-side copy, but uploaded as the
# canonical name on the server.
$uploadPlan += [PSCustomObject]@{
    Local  = $srcManifest
    Remote = "$Webroot/releases/$Tag/MANIFEST.json"
}
$uploadPlan += [PSCustomObject]@{
    Local  = $srcSums
    Remote = "$Webroot/releases/$Tag/SHA256SUMS.txt"
}

# Resolve ssh.exe / scp.exe up-front. On Windows hosts the Optional
# Feature OpenSSH client lives in C:\Windows\System32\OpenSSH and
# isn't always on the system PATH (this varies by build). Fall back
# to the Git-for-Windows bundle if the system one is missing. Bake
# the absolute path into the invoked command line so cmd /c doesn't
# need PATH cooperation.
function Resolve-SshTool {
    param([string]$Name)  # "ssh" or "scp"
    $envExt = if ($IsWindows -or ([Environment]::OSVersion.Platform -eq 'Win32NT')) { ".exe" } else { "" }
    $fromPath = Get-Command "$Name$envExt" -ErrorAction SilentlyContinue
    if ($fromPath) { return $fromPath.Source }
    $candidates = @(
        "$env:WINDIR\System32\OpenSSH\$Name$envExt",
        "$env:ProgramFiles\OpenSSH\$Name$envExt",
        "$env:ProgramFiles\Git\usr\bin\$Name$envExt",
        "$env:USERPROFILE\AppData\Local\Programs\Git\usr\bin\$Name$envExt"
    )
    foreach ($c in $candidates) {
        if ($c -and (Test-Path $c)) { return $c }
    }
    return $null
}
$SshExe = Resolve-SshTool 'ssh'
$ScpExe = Resolve-SshTool 'scp'
if (-not $SshExe -or -not $ScpExe) {
    Write-Host "REFUSING TO PROCEED: ssh.exe / scp.exe not found on this host." -ForegroundColor Red
    Write-Host "  Install Windows OpenSSH Client (Settings -> Apps -> Optional features)" -ForegroundColor Red
    Write-Host "  or Git for Windows (which bundles OpenSSH), then re-run."          -ForegroundColor Red
    exit 8
}

# 5) Print the plan.
Write-Host "===== publish_release plan =====" -ForegroundColor Cyan
Write-Host "  tag:         $Tag"
Write-Host "  release dir: $releaseDir"
Write-Host "  ssh target:  $SshTarget"
Write-Host "  webroot:     $Webroot"
Write-Host "  manifest:    $srcManifest"
Write-Host "  sha256sums:  $srcSums"
Write-Host "  ssh:         $SshExe"
Write-Host "  scp:         $ScpExe"
Write-Host "  bump-latest: $BumpLatest"
Write-Host "  dry-run:     $DryRun"
Write-Host ""
Write-Host "===== uploads ($($uploadPlan.Count) file(s)) =====" -ForegroundColor Cyan
foreach ($u in $uploadPlan) {
    $sz = (Get-Item $u.Local).Length
    Write-Host ("  {0,12} bytes  {1}" -f $sz, $u.Remote)
}
Write-Host ""

# 6) Execute.
#
# Direct invocation via the PowerShell call operator (`&`), passing
# args as a typed array. This avoids `cmd /c` and its first/last-
# quote stripping, which makes quoted paths with spaces fragile.
function Invoke-Step {
    param(
        [string]$Description,
        [string]$Exe,
        [string[]]$ArgsList
    )
    Write-Host ">>> $Description" -ForegroundColor Yellow
    $rendered = ($ArgsList | ForEach-Object { if ($_ -match '\s') { '"' + $_ + '"' } else { $_ } }) -join ' '
    Write-Host "    $Exe $rendered"
    if ($DryRun) {
        Write-Host "    (dry-run; not executed)" -ForegroundColor DarkGray
        return 0
    }
    $output = & $Exe @ArgsList 2>&1
    $exitCode = $LASTEXITCODE
    if ($output) {
        $output | ForEach-Object { Write-Host $_ }
    }
    return [int]$exitCode
}

# Ensure remote dir exists before any scp. Multi-tag deploys
# create the per-tag directory on demand.
$rc = Invoke-Step "mkdir on remote" $SshExe @($SshTarget, "mkdir -p $Webroot/releases/$Tag")
if ($rc -ne 0) {
    Write-Host "remote mkdir failed (exit=$rc); aborting." -ForegroundColor Red
    exit 4
}

# scp each file. We use -p to preserve mtime so the
# Last-Modified header on Caddy stays sensible.
foreach ($u in $uploadPlan) {
    $rc = Invoke-Step ("upload " + (Split-Path $u.Local -Leaf)) $ScpExe @("-p", $u.Local, "$($SshTarget):$($u.Remote)")
    if ($rc -ne 0) {
        Write-Host "scp failed (exit=$rc) for $($u.Local); aborting." -ForegroundColor Red
        exit 5
    }
}

# Optional: bump latest.txt to point at this tag.
if ($BumpLatest) {
    $rc = Invoke-Step "bump latest.txt" $SshExe @($SshTarget, "echo $Tag > $Webroot/releases/latest.txt && cat $Webroot/releases/latest.txt")
    if ($rc -ne 0) {
        Write-Host "bump latest.txt failed (exit=$rc)." -ForegroundColor Red
        exit 6
    }
}

# 7) Post-deploy verification — only when actually executed.
if (-not $DryRun) {
    Write-Host ""
    Write-Host "===== post-deploy curl checks =====" -ForegroundColor Cyan
    $checks = @(
        "https://qsdm.tech/releases/$Tag/MANIFEST.json",
        "https://qsdm.tech/releases/$Tag/SHA256SUMS.txt"
    )
    if ($BumpLatest) { $checks += "https://qsdm.tech/releases/latest.txt" }
    foreach ($c in $checks) {
        try {
            $r = Invoke-WebRequest -Uri $c -UseBasicParsing -Method Head -TimeoutSec 15
            $cl = $r.Headers.'Content-Length'
            $ct = $r.Headers.'Content-Type'
            Write-Host ("  [{0}] {1}  ct={2}  len={3}" -f $r.StatusCode, $c, $ct, $cl)
        } catch {
            Write-Host ("  [ERR] {0}  {1}" -f $c, $_.Exception.Message) -ForegroundColor Red
        }
    }
    # And one HEAD on the new installer specifically.
    $instUrl = "https://qsdm.tech/releases/$Tag/qsdmminer-setup-$Tag.exe"
    try {
        $r = Invoke-WebRequest -Uri $instUrl -UseBasicParsing -Method Head -TimeoutSec 15
        Write-Host ("  [{0}] {1}  len={2}" -f $r.StatusCode, $instUrl, $r.Headers.'Content-Length')
    } catch {
        Write-Host ("  [skip] {0}  {1}" -f $instUrl, $_.Exception.Message) -ForegroundColor DarkGray
    }
}

Write-Host ""
Write-Host "publish_release: done." -ForegroundColor Green
