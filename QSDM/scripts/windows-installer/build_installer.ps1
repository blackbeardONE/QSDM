#requires -Version 5.1

<#
.SYNOPSIS
  Build the Windows installer for QSDM Miner.

.DESCRIPTION
  Wraps Inno Setup 6 with the right inputs for the consumer
  release pipeline:

    1. Locate the most recent release directory under release/.
       Reads MANIFEST.json to get the version tag and to enforce
       that the windows/amd64 row matches the on-disk binary.
    2. Stage qsdmminer + qsdmcli + qsdm-attester + LICENSE +
       README under a temporary directory, alongside a
       README.txt produced from the live MANIFEST so the
       installed copy carries its own provenance fingerprint.
    3. Substitute the tokens in qsdmminer.iss.template, write
       the result to a per-build .iss file under release/<tag>/.
    4. Invoke iscc.exe and emit qsdmminer-setup-<tag>.exe into
       release/<tag>/.

  The script intentionally does NOT install Inno Setup for you:
  silent-installing dev tooling is too aggressive a side effect
  for a build script. If iscc.exe is missing, the script tells
  you exactly which winget/curl one-liner to run and exits 1.

.PARAMETER Tag
  Override the release tag. Default: pick the most recent
  release/v* directory by name (sorted descending).

.PARAMETER OutputDir
  Where to drop the installer. Default: same directory as the
  source binaries (release/<tag>/).

.PARAMETER Iscc
  Path to ISCC.exe. Default: try
  "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" then
  "C:\Program Files\Inno Setup 6\ISCC.exe".
#>

param(
    [string]$Tag = "",
    [string]$OutputDir = "",
    [string]$Iscc = ""
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent (Split-Path -Parent $ScriptDir)
# RepoRoot is .../QSDM. The release/ directory sits one level
# higher in the workspace (.../release at the workspace root).
$WorkspaceRoot = Split-Path -Parent $RepoRoot

function Find-LatestReleaseDir {
    # "Latest" here means "the most recently rebuilt directory",
    # which is what the operator almost always wants. We sort by
    # LastWriteTime descending rather than by name because the
    # name embeds a git SHA whose lexical order has nothing to
    # do with chronology — '5cf5b69' sorts before 'ce21940' even
    # though it's the newer commit. Falling back to name-sort
    # would silently install yesterday's release with today's
    # binaries, which is exactly the integrity drift the
    # auto-updater is meant to prevent.
    $releaseRoot = Join-Path $WorkspaceRoot "release"
    if (-not (Test-Path $releaseRoot)) {
        Write-Error "release/ directory not found under $WorkspaceRoot. Run QSDM/scripts/build_release.ps1 first."
        exit 1
    }
    # Sort by MANIFEST.json's mtime, not the directory's mtime.
    # The directory's mtime gets bumped every time we drop an
    # installer .exe into it, which would silently make a
    # one-version-old release "newer" than the freshly built one.
    # MANIFEST.json is only written once per build_release.ps1
    # invocation, so its mtime is the canonical "this is when
    # this release was minted" timestamp.
    $dirs = Get-ChildItem $releaseRoot -Directory -Filter "v*" |
        Where-Object { Test-Path (Join-Path $_.FullName "MANIFEST.json") } |
        ForEach-Object {
            [PSCustomObject]@{
                Path     = $_.FullName
                Manifest = (Get-Item (Join-Path $_.FullName "MANIFEST.json")).LastWriteTime
            }
        } |
        Sort-Object Manifest -Descending
    if (-not $dirs) {
        Write-Error "No release/v* directories with a MANIFEST.json found. Run QSDM/scripts/build_release.ps1 first."
        exit 1
    }
    return $dirs[0].Path
}

function Resolve-Iscc {
    param([string]$Override)
    if ($Override -and (Test-Path $Override)) { return $Override }
    # Default install paths in priority order: per-machine (admin
    # install) wins over per-user (no-UAC install). The release/
    # install_inno.ps1 helper installs per-user, so the third
    # candidate is the common one on dev workstations that bootstrap
    # without admin.
    $candidates = @(
        "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "C:\Program Files\Inno Setup 6\ISCC.exe",
        (Join-Path $env:LOCALAPPDATA 'Programs\Inno Setup 6\ISCC.exe')
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { return $c }
    }
    Write-Host "ERROR: Inno Setup 6 not found." -ForegroundColor Red
    Write-Host "Install one of:"
    Write-Host "  winget install --id JRSoftware.InnoSetup --silent"
    Write-Host "  https://jrsoftware.org/isdl.php  (innosetup-6.x.x.exe)"
    Write-Host "Then re-run this script (no need to pass -Iscc; the default path is detected)."
    exit 1
}

function Get-ManifestVersion {
    param([string]$ReleaseDir)
    $manifestPath = Join-Path $ReleaseDir "MANIFEST.json"
    $raw = [System.IO.File]::ReadAllText($manifestPath)
    # build_release.ps1 writes MANIFEST.json BOM-free, but be
    # defensive against an older-toolchain release host that did
    # emit one — the Go updater is BOM-tolerant for the same reason.
    if ($raw.Length -gt 0 -and ([int][char]$raw[0]) -eq 0xFEFF) {
        $raw = $raw.Substring(1)
    }
    return ($raw | ConvertFrom-Json)
}

function ConvertTo-NumericVersion {
    param([string]$Tag)
    # Tag form is "v0.0.0+<sha>"; AppVersion in [Setup] takes the
    # decorative form (matches what the user sees on qsdm.tech),
    # but VersionInfoVersion needs strict 0.0.0.0 numeric digits
    # for Windows Add/Remove Programs to read it. Strip the leading
    # 'v' and the +<sha> suffix; if the prefix isn't numeric, fall
    # back to 0.0.0 so the build never blocks on a weird tag.
    $stripped = $Tag.TrimStart('v').Split('+')[0]
    if ($stripped -match '^\d+\.\d+\.\d+$') { return $stripped }
    return "0.0.0"
}

# 1) Resolve inputs.
$releaseDir = if ($Tag) { Join-Path (Join-Path $WorkspaceRoot "release") $Tag } else { Find-LatestReleaseDir }
if (-not (Test-Path $releaseDir)) {
    Write-Error "Release directory not found: $releaseDir"
    exit 1
}
$manifest = Get-ManifestVersion -ReleaseDir $releaseDir
$resolvedTag = $manifest.version
$numericVersion = ConvertTo-NumericVersion -Tag $resolvedTag

if (-not $OutputDir) { $OutputDir = $releaseDir }
$iscc = Resolve-Iscc -Override $Iscc

Write-Host "=== QSDM Miner Windows installer build ===" -ForegroundColor Cyan
Write-Host "  release dir:   $releaseDir"
Write-Host "  manifest tag:  $resolvedTag"
Write-Host "  numeric ver:   $numericVersion"
Write-Host "  output dir:    $OutputDir"
Write-Host "  iscc:          $iscc"
Write-Host ""

# 2) Sanity-check that the windows/amd64 binary exists and matches
#    the manifest sha256. If it doesn't, the consumer would receive
#    a binary whose sha was never published — exactly the integrity
#    gap our auto-updater is built to avoid. Fail loudly here.
$windowsRow = $manifest.components | Where-Object { $_.component -eq 'qsdmminer' -and $_.os -eq 'windows' -and $_.arch -eq 'amd64' } | Select-Object -First 1
if (-not $windowsRow) {
    Write-Error "MANIFEST.json has no qsdmminer/windows/amd64 row"
    exit 1
}
$winBinPath = Join-Path $releaseDir $windowsRow.file
if (-not (Test-Path $winBinPath)) {
    Write-Error "Missing binary: $winBinPath"
    exit 1
}
$sha = [System.Security.Cryptography.SHA256]::Create()
$stream = [System.IO.File]::OpenRead($winBinPath)
$hashBytes = $sha.ComputeHash($stream)
$stream.Close()
$sha.Dispose()
$actualSha = ([System.BitConverter]::ToString($hashBytes) -replace '-', '').ToLower()
if ($actualSha -ne $windowsRow.sha256) {
    Write-Error "qsdmminer-windows-amd64.exe sha256 mismatch:`n  on-disk:  $actualSha`n  manifest: $($windowsRow.sha256)"
    exit 1
}
Write-Host "  windows binary: sha256 ok"
Write-Host ""

# 3) Stage files for the installer (LICENSE + README + binaries).
$stagingDir = Join-Path $env:TEMP "qsdmminer-installer-staging-$([System.IO.Path]::GetRandomFileName())"
$null = New-Item -ItemType Directory -Path $stagingDir -Force
try {
    Copy-Item $winBinPath (Join-Path $stagingDir $windowsRow.file)
    foreach ($name in @('qsdmcli-windows-amd64.exe', 'qsdm-attester-windows-amd64.exe')) {
        $src = Join-Path $releaseDir $name
        if (Test-Path $src) {
            Copy-Item $src (Join-Path $stagingDir $name)
        }
    }
    # LICENSE: prefer the repo top-level. Fall back to a one-liner
    # so Inno Setup's [Setup] LicenseFile line still finds something.
    $repoLicense = Join-Path $RepoRoot "LICENSE"
    if (Test-Path $repoLicense) {
        Copy-Item $repoLicense (Join-Path $stagingDir "LICENSE")
    } else {
        Set-Content -Path (Join-Path $stagingDir "LICENSE") `
            -Value "QSDM Miner license — see https://qsdm.tech/" `
            -Encoding ASCII
    }
    # README.txt: a per-install provenance file. Operators can
    # `cat "C:\Program Files\QSDM Miner\README.txt"` later to
    # confirm exactly what build is running.
    $readme = @"
QSDM Miner - $resolvedTag
Built: $($manifest.builtAt)
Go:    $($manifest.goVersion)

This binary auto-updates from https://qsdm.tech/releases every
24h when registered as a service. Verify integrity manually:

  certutil -hashfile "C:\Program Files\QSDM Miner\qsdmminer.exe" SHA256

Expected qsdmminer.exe sha256:
  $($windowsRow.sha256)

Service control:
  Get-Service QSDMMiner
  Start-Service QSDMMiner
  Stop-Service QSDMMiner

Logs:
  Get-Content -Tail 200 -Wait "C:\Program Files\QSDM Miner\Logs\qsdmminer.log"

Configuration:
  C:\Users\<you>\.qsdm\miner.toml

Run interactively (one-off) instead of as a service:
  & "C:\Program Files\QSDM Miner\qsdmminer.exe" --setup
"@
    Set-Content -Path (Join-Path $stagingDir "README.txt") -Value $readme -Encoding UTF8

    # 4) Render the .iss template.
    $templatePath = Join-Path $ScriptDir "qsdmminer.iss.template"
    $template = [System.IO.File]::ReadAllText($templatePath)
    $issPath = Join-Path $releaseDir "qsdmminer-installer.iss"
    $outputBasename = "qsdmminer-setup-$resolvedTag"
    $rendered = $template `
        -replace '\{%VERSION%\}', $resolvedTag `
        -replace '\{%VERSION_NUM%\}', $numericVersion `
        -replace '\{%STAGING_DIR%\}', $stagingDir.Replace('\', '\\') `
        -replace '\{%OUTPUT_DIR%\}', $OutputDir.Replace('\', '\\') `
        -replace '\{%OUTPUT_BASENAME%\}', $outputBasename
    [System.IO.File]::WriteAllText($issPath, $rendered, (New-Object System.Text.UTF8Encoding $false))
    Write-Host "  rendered:    $issPath"

    # 5) Invoke iscc.exe.
    Write-Host "  compiling installer..." -NoNewline
    $isccLog = Join-Path $env:TEMP "qsdmminer-iscc-$([System.IO.Path]::GetRandomFileName()).log"
    $proc = Start-Process -FilePath $iscc `
        -ArgumentList "/Qp", "`"$issPath`"" `
        -NoNewWindow -PassThru -Wait `
        -RedirectStandardOutput $isccLog `
        -RedirectStandardError "$isccLog.err"
    if ($proc.ExitCode -ne 0) {
        Write-Host " FAILED" -ForegroundColor Red
        Write-Host ""
        Write-Host "iscc.exe exit code: $($proc.ExitCode)"
        Write-Host "iscc stdout:"
        Get-Content $isccLog | ForEach-Object { Write-Host "  $_" }
        Write-Host "iscc stderr:"
        Get-Content "$isccLog.err" | ForEach-Object { Write-Host "  $_" -ForegroundColor Red }
        exit 1
    }
    Write-Host " ok"

    $finalInstaller = Join-Path $OutputDir "$outputBasename.exe"
    if (Test-Path $finalInstaller) {
        $size = (Get-Item $finalInstaller).Length
        $mib = [Math]::Round($size / 1048576, 2)
        Write-Host ""
        Write-Host "=== Installer built ===" -ForegroundColor Green
        Write-Host "  $finalInstaller  ($mib MiB)"

        # Patch the per-release MANIFEST.json so download.html and
        # the auto-updater both know about the installer as a
        # first-class artifact. The component is named
        # "qsdmminer-installer" so JS that filters by component=
        # qsdmminer (the auto-updater path) ignores it — only the
        # download page treats it specially.
        Write-Host ""
        Write-Host "  patching MANIFEST.json with installer row..."
        $shaInst = [System.Security.Cryptography.SHA256]::Create()
        $stream = [System.IO.File]::OpenRead($finalInstaller)
        $hashBytes = $shaInst.ComputeHash($stream)
        $stream.Close()
        $shaInst.Dispose()
        $instSha = ([System.BitConverter]::ToString($hashBytes) -replace '-', '').ToLower()

        $manifestPath = Join-Path $releaseDir "MANIFEST.json"
        $manifestRaw = [System.IO.File]::ReadAllText($manifestPath)
        if ($manifestRaw.Length -gt 0 -and ([int][char]$manifestRaw[0]) -eq 0xFEFF) {
            $manifestRaw = $manifestRaw.Substring(1)
        }
        $manifestObj = $manifestRaw | ConvertFrom-Json

        # Drop any pre-existing installer row (rebuild idempotency).
        $components = @($manifestObj.components | Where-Object { $_.component -ne 'qsdmminer-installer' })
        $components += [PSCustomObject]@{
            component = 'qsdmminer-installer'
            os        = 'windows'
            arch      = 'amd64'
            file      = "$outputBasename.exe"
            label     = 'Windows 10/11 x64 (Installer)'
            sizeBytes = $size
            sha256    = $instSha
        }
        $manifestObj.components = $components
        $patchedJson = $manifestObj | ConvertTo-Json -Depth 6
        [System.IO.File]::WriteAllText($manifestPath, $patchedJson, (New-Object System.Text.UTF8Encoding $false))

        # Also append the installer to SHA256SUMS.txt so
        # operators verifying via `sha256sum -c SHA256SUMS.txt`
        # see one consistent gate. Existing rows (from
        # build_release.ps1) are kept; we only add ours.
        $sumsPath = Join-Path $releaseDir "SHA256SUMS.txt"
        if (Test-Path $sumsPath) {
            $sumsRaw = (Get-Content $sumsPath -Raw)
            $instLine = "$instSha  $outputBasename.exe"
            if ($sumsRaw -notmatch [regex]::Escape($outputBasename)) {
                $sumsRaw = $sumsRaw.TrimEnd("`r","`n") + "`n" + $instLine + "`n"
                Set-Content -Path $sumsPath -Value $sumsRaw -Encoding ASCII -NoNewline
            }
        }

        Write-Host "  manifest:    $manifestPath"
        Write-Host "  sha256sums:  $sumsPath"
        Write-Host "  sha256:      $instSha"
    } else {
        Write-Error "iscc.exe reported success but installer not at expected path: $finalInstaller"
        exit 1
    }
}
finally {
    Remove-Item $stagingDir -Recurse -Force -ErrorAction SilentlyContinue
}
