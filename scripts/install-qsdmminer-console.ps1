#Requires -Version 5.1
<#
.SYNOPSIS
  One-command installer for the QSDM friendly console miner on Windows.

.DESCRIPTION
  Downloads the latest signed qsdmminer-console-windows-<arch>.exe
  from GitHub Releases, verifies its SHA-256 against the consolidated
  SHA256SUMS file, installs it to a user-writable directory (so no
  Admin prompt is required), and runs --version to confirm the binary
  is a release build.

.PARAMETER Version
  Release tag to install (e.g. "v0.1.0"). Defaults to the latest
  semver tag returned by the GitHub Releases API.

.PARAMETER InstallDir
  Directory to install the binary into. Defaults to
  $env:LOCALAPPDATA\Programs\QSDM.

.PARAMETER Repo
  Override the GitHub repo (owner/name). Defaults to blackbeardONE/QSDM.

.EXAMPLE
  # Bootstrap via iwr (equivalent to `curl | bash` on Unix):
  iwr https://raw.githubusercontent.com/blackbeardONE/QSDM/main/scripts/install-qsdmminer-console.ps1 -UseBasicParsing | iex

.EXAMPLE
  # Pin a specific release:
  & .\install-qsdmminer-console.ps1 -Version v0.1.0

.NOTES
  PowerShell 5.1+ (Windows 10 / 11 / Server 2019+ ship this in-box).
  The script never elevates — if you point -InstallDir at a path that
  requires Admin it will fail fast with a clear error instead of
  silently UAC-prompting mid-install.
#>
[CmdletBinding()]
param(
    [string] $Version    = $env:QSDM_VERSION,
    [string] $InstallDir = $env:QSDM_INSTALL_DIR,
    [string] $Repo       = $(if ($env:QSDM_REPO) { $env:QSDM_REPO } else { "blackbeardONE/QSDM" })
)

$ErrorActionPreference = "Stop"
$Binary = "qsdmminer-console"

# ---- helpers ---------------------------------------------------------------

function Info($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Ok($msg)   { Write-Host "[ok] $msg" -ForegroundColor Green }
function Die($msg)  { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

function Get-SHA256 {
    param([string] $Path)
    (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# ---- platform detection ----------------------------------------------------

# PROCESSOR_ARCHITECTURE reports the process arch; for an installer
# we want the OS arch, which on an x64 PowerShell 5 process running
# under ARM64 Windows would differ. [Environment]::Is64BitOperatingSystem
# plus PROCESSOR_ARCHITEW6432 is the canonical way.
$osArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($osArch) {
    "AMD64" { $arch = "amd64" }
    "ARM64" {
        # The release matrix in release-container.yml currently ships
        # windows/amd64 only. ARM64 Windows can run amd64 binaries via
        # the built-in emulator, so we fall back with a note rather
        # than failing outright.
        Write-Host "note: windows/arm64 not in release matrix yet; falling back to windows/amd64 (runs under x64 emulation)" -ForegroundColor Yellow
        $arch = "amd64"
    }
    default { Die "unsupported arch: $osArch. Known supported: AMD64." }
}
$os = "windows"
Info "platform: ${os}/${arch}"

# ---- release selection -----------------------------------------------------

if ([string]::IsNullOrEmpty($Version)) {
    Info "resolving latest release tag via GitHub API..."
    try {
        $resp = Invoke-RestMethod -Uri "https://api.github.com/repos/${Repo}/releases/latest" `
                                  -Headers @{ "Accept" = "application/vnd.github+json" } `
                                  -UseBasicParsing
    } catch {
        Die "could not reach GitHub releases API: $($_.Exception.Message). Set -Version vX.Y.Z to pin a specific release."
    }
    $Version = $resp.tag_name
    if ([string]::IsNullOrEmpty($Version)) { Die "could not parse tag_name from GitHub API response." }
}

Info "installing $Binary $Version"

$base      = "https://github.com/${Repo}/releases/download/${Version}"
$asset     = "${Binary}-${os}-${arch}.exe"
$assetUrl  = "${base}/${asset}"
$sumsUrl   = "${base}/SHA256SUMS"

# ---- download + verify -----------------------------------------------------

$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "qsdm-install-$([Guid]::NewGuid().ToString('N'))")
try {
    $tmpBin  = Join-Path $tmp.FullName $asset
    $tmpSums = Join-Path $tmp.FullName "SHA256SUMS"

    Info "downloading $assetUrl"
    try {
        Invoke-WebRequest -Uri $assetUrl -OutFile $tmpBin -UseBasicParsing
    } catch {
        Die "binary not found at $assetUrl. Check the release page: https://github.com/${Repo}/releases/tag/${Version}"
    }

    Info "downloading $sumsUrl"
    try {
        Invoke-WebRequest -Uri $sumsUrl -OutFile $tmpSums -UseBasicParsing
    } catch {
        Die "SHA256SUMS not found at $sumsUrl. Refusing to install an unverified binary."
    }

    # Find the expected hash for this asset. SHA256SUMS lines look like
    # "<hash>  <asset>" or "<hash>  ./<asset>".
    $expected = $null
    foreach ($line in Get-Content $tmpSums) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -lt 2) { continue }
        $hash = $parts[0].Trim()
        $name = $parts[1].Trim().TrimStart('.', '/', '\')
        if ($name -eq $asset) {
            $expected = $hash.ToLowerInvariant()
            break
        }
    }
    if ([string]::IsNullOrEmpty($expected)) {
        Die "asset $asset not listed in SHA256SUMS. Release may be incomplete."
    }

    $actual = Get-SHA256 $tmpBin
    if ($expected -ne $actual) {
        Die "sha256 mismatch for ${asset}: expected $expected, got $actual. Refusing to install."
    }
    Ok "sha256 verified: $expected"

    # ---- install --------------------------------------------------------

    if ([string]::IsNullOrEmpty($InstallDir)) {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\QSDM"
    }
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }
    $dest = Join-Path $InstallDir "${Binary}.exe"

    Info "installing to $dest"
    try {
        Copy-Item -Path $tmpBin -Destination $dest -Force
    } catch {
        Die "could not write to ${dest}: $($_.Exception.Message). Re-run with -InstallDir pointing at a user-writable path."
    }
    Ok "installed $Binary to $dest"

    # ---- post-install sanity check -------------------------------------

    Info "running $dest --version"
    $verOutput = & $dest --version 2>&1
    if ($LASTEXITCODE -ne 0) { Die "installed binary failed to execute --version" }
    Write-Host "    $verOutput"

    if ($verOutput -match '(^|\s)dev(\s|$)' -or $verOutput -match 'unknown') {
        Die "installed binary reports dev/unknown metadata -- expected a release build. Aborting."
    }
    Ok "binary identifies as a release build"

    # ---- PATH hint ------------------------------------------------------

    $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($userPath -notlike "*$InstallDir*") {
        Write-Host ""
        Write-Host "note: $InstallDir is not on your user PATH. To add it permanently run:" -ForegroundColor Yellow
        Write-Host "    [Environment]::SetEnvironmentVariable('PATH', (`$userPath + ';$InstallDir'), 'User')" -ForegroundColor Yellow
        Write-Host "or just prefix invocations with the full path: $dest"
    }

    Write-Host ""
    Write-Host "Next steps:"
    Write-Host "  1. Run the setup wizard (creates %USERPROFILE%\.qsdm\miner.toml):"
    Write-Host "       $dest --setup"
    Write-Host "  2. Start mining:"
    Write-Host "       $dest"
    Write-Host "  3. Quickstart:"
    Write-Host "       https://github.com/${Repo}/blob/main/QSDM/docs/docs/MINER_QUICKSTART.md"
    Write-Host ""
} finally {
    Remove-Item -Path $tmp.FullName -Recurse -Force -ErrorAction SilentlyContinue
}
