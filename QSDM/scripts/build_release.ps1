# build_release.ps1 — produce signed-ready console miner releases.
#
# Builds qsdmminer for the supported (os, arch) tuples and writes the
# artifacts plus a SHA256SUMS.txt under release/. Run from the repo
# root or anywhere — the script resolves QSDM/source/ relative to its
# own location, so `pwsh QSDM/scripts/build_release.ps1` from any cwd
# does the right thing.
#
# The output binary is named `qsdmminer` (not `qsdmminer-console`) to
# keep the operator-facing command short. The Go module command we
# build is still cmd/qsdmminer-console — that's the canonical source
# for the console miner with the wizard, live panel, --idle-only, and
# --service mode wired in. cmd/qsdmminer (no suffix) stays in tree as
# the audit-only minimal reference miner.
#
# What the release directory looks like after a run:
#
#   release/
#     v0.0.0+a1b2c3d/
#       qsdmminer-windows-amd64.exe
#       qsdmminer-linux-amd64
#       qsdmminer-linux-arm64
#       qsdmminer-darwin-arm64
#       qsdmminer-darwin-amd64
#       SHA256SUMS.txt
#       MANIFEST.json
#
# Operators publish the directory as-is at https://qsdm.tech/releases/<tag>/
# (see deploy/landing/download.html which fetches MANIFEST.json to populate
# the platform-detected download buttons).
#
# Flags:
#   -Tag <string>     Override the version tag (default: pkg/buildinfo
#                     resolution from git: <BuildVersion>+<short SHA>).
#   -Platforms <list> Comma-separated list of "os/arch" tuples to limit
#                     the build (default: all supported tuples).
#   -SkipCli          Don't ship qsdmcli alongside qsdmminer.
#   -SkipAttester     Don't ship qsdm-attester (the home-3050 issuer).

param(
    [string]$Tag = "",
    [string]$Platforms = "",
    [switch]$SkipCli,
    [switch]$SkipAttester
)

$ErrorActionPreference = "Stop"

# Resolve repo root from the script's own location: scripts/.. = QSDM/
$ScriptDir   = Split-Path -Parent $MyInvocation.MyCommand.Definition
$QSDMRoot    = Split-Path -Parent $ScriptDir
$RepoRoot    = Split-Path -Parent $QSDMRoot
$SourceDir   = Join-Path $QSDMRoot "source"
$ReleaseRoot = Join-Path $RepoRoot "release"

if (-not (Test-Path $SourceDir)) {
    Write-Error "Cannot find Go source dir at $SourceDir"
    exit 1
}

# Prefer Go 1.25+; the qsdm module pins go 1.25.9 in go.mod.
$env:GOROOT = "C:\Program Files\Go"
if (Test-Path "$env:GOROOT\bin\go.exe") {
    $env:PATH = "$env:GOROOT\bin;$env:PATH"
}
$env:CGO_ENABLED = "0"
$goVersion = (& go version) -replace "^go version ", ""
Write-Host "Building with: $goVersion" -ForegroundColor Cyan

# Resolve the version tag. Format mirrors pkg/buildinfo:
#   v<MAJOR>.<MINOR>.<PATCH>+<SHORT_SHA>
# When -Tag was supplied, use it verbatim; otherwise derive from git.
function Resolve-Tag {
    param([string]$Override)
    if ($Override) { return $Override }

    $sha = ""
    try {
        $sha = (& git -C $RepoRoot rev-parse --short HEAD) 2>$null
        $sha = $sha.Trim()
    } catch {
        $sha = "dev"
    }
    if (-not $sha) { $sha = "dev" }
    return "v0.0.0+$sha"
}

$VersionTag = Resolve-Tag -Override $Tag
$OutDir = Join-Path $ReleaseRoot $VersionTag
New-Item -ItemType Directory -Path $OutDir -Force | Out-Null

# `go build` resolves the module from the working directory; from
# the repo root QSDM/source/go.mod isn't visible. Pin the cwd to
# the module root before any go invocation.
Push-Location $SourceDir
try {

# Each row in $Targets is a tuple: (GOOS, GOARCH, ext, displayName).
# `ext` is either ".exe" (Windows) or empty (Unix). displayName is
# what gets baked into the file name AND keyed in MANIFEST.json so
# the download page can surface human-friendly platform labels.
$Targets = @(
    @{ os = "windows"; arch = "amd64"; ext = ".exe"; name = "windows-amd64"; label = "Windows 10/11 x64" },
    @{ os = "linux";   arch = "amd64"; ext = "";     name = "linux-amd64";   label = "Linux x86_64" },
    @{ os = "linux";   arch = "arm64"; ext = "";     name = "linux-arm64";   label = "Linux ARM64 (Pi 5, Ampere Altra)" },
    @{ os = "darwin";  arch = "arm64"; ext = "";     name = "darwin-arm64";  label = "macOS Apple Silicon (M1/M2/M3/M4)" },
    @{ os = "darwin";  arch = "amd64"; ext = "";     name = "darwin-amd64";  label = "macOS Intel" }
)

# When the operator restricted the platform set via -Platforms,
# filter the target list. Format: "windows/amd64,linux/amd64".
if ($Platforms) {
    $allowed = $Platforms -split "," | ForEach-Object { $_.Trim().ToLower() }
    $Targets = $Targets | Where-Object {
        $allowed -contains "$($_.os)/$($_.arch)"
    }
    if (-not $Targets) {
        Write-Error "No targets match -Platforms $Platforms"
        exit 1
    }
}

# Each entry of $Components describes what we build per platform.
# By default we ship all three: the console miner, the cli wallet
# tool, and the home attester. -SkipCli and -SkipAttester let an
# operator publish a slimmer release without rebuilding the world.
#
# DO NOT add private components (currently: qsdm-detect, qsdmminer-gui)
# to this list. Those binaries are part of the operator-private kit
# at <workspace>/Blackbeard/ and must not leak into a public release.
# The $PrivateComponents check below is an extra guard rail, but the
# right place to enforce this is the comment you're reading: think
# twice before extending this list.
$Components = @(
    @{ pkg = "./cmd/qsdmminer-console"; name = "qsdmminer";    skip = $false }
)
if (-not $SkipCli) {
    $Components += @{ pkg = "./cmd/qsdmcli"; name = "qsdmcli"; skip = $false }
}
if (-not $SkipAttester) {
    $Components += @{ pkg = "./cmd/qsdm-attester"; name = "qsdm-attester"; skip = $false }
}

# Trip-wire: assert the build matrix doesn't try to ship a private
# component into release/. Mirrors $PrivateComponents in
# QSDM/deploy/scripts/publish_release.ps1 — keep the two lists in
# sync. Build-time failure is preferable to relying on the publish
# gate alone: this catches the regression at the developer's
# workstation, before any binary ever touches release/<tag>/.
$PrivateComponents = @('qsdm-detect', 'qsdmminer-gui')
$leak = @($Components | Where-Object { $PrivateComponents -contains $_.name })
if ($leak.Count -gt 0) {
    Write-Host "REFUSING TO BUILD: a private component is in the build matrix." -ForegroundColor Red
    foreach ($c in $leak) {
        Write-Host ("  {0} -> pkg {1}" -f $c.name, $c.pkg) -ForegroundColor Red
    }
    Write-Host "  These binaries belong in <workspace>/Blackbeard/ and ship via" -ForegroundColor Red
    Write-Host "  Blackbeard/build-kit.ps1, not via the public release pipeline." -ForegroundColor Red
    Write-Host "  Either remove them from `$Components above, or update the" -ForegroundColor Red
    Write-Host "  matching `$PrivateComponents lists in BOTH build_release.ps1 and" -ForegroundColor Red
    Write-Host "  publish_release.ps1 if a previously-private component is being" -ForegroundColor Red
    Write-Host "  promoted to public." -ForegroundColor Red
    exit 7
}

# Build matrix.
$Manifest = [ordered]@{
    version    = $VersionTag
    builtAt    = (Get-Date).ToUniversalTime().ToString("o")
    goVersion  = $goVersion
    components = @()
}

foreach ($t in $Targets) {
    Write-Host ""
    Write-Host "=== $($t.os)/$($t.arch) ===" -ForegroundColor Yellow

    $env:GOOS   = $t.os
    $env:GOARCH = $t.arch

    foreach ($c in $Components) {
        $artifactName = "$($c.name)-$($t.name)$($t.ext)"
        $artifactPath = Join-Path $OutDir $artifactName

        # ldflags:
        #   -s -w strips the symbol table + DWARF for smaller
        #     binaries. Stack traces still work because Go embeds
        #     runtime tables separately, only debug symbols go away.
        #   -X github.com/.../buildinfo.{Version,GitSHA,BuildDate}=
        #     injects the release tag + short SHA + UTC timestamp
        #     into pkg/buildinfo so `qsdmminer --version` and the
        #     updater both have a precise identifier to compare
        #     against /releases/latest.txt.
        $injSha = $VersionTag.Split('+')[1]
        if (-not $injSha) { $injSha = "unknown" }
        $injDate = (Get-Date).ToUniversalTime().ToString("o")
        $ldflags = "-s -w" `
            + " -X github.com/blackbeardONE/QSDM/pkg/buildinfo.Version=$VersionTag" `
            + " -X github.com/blackbeardONE/QSDM/pkg/buildinfo.GitSHA=$injSha" `
            + " -X github.com/blackbeardONE/QSDM/pkg/buildinfo.BuildDate=$injDate"

        Write-Host "  building $artifactName ..." -NoNewline
        & go build -trimpath -ldflags $ldflags -o $artifactPath $c.pkg
        if ($LASTEXITCODE -ne 0) {
            Write-Host " FAIL" -ForegroundColor Red
            exit $LASTEXITCODE
        }
        $size = (Get-Item $artifactPath).Length
        $sizeMB = [math]::Round($size / 1MB, 2)
        Write-Host " ok ($sizeMB MiB)" -ForegroundColor Green

        $Manifest.components += [ordered]@{
            component = $c.name
            os        = $t.os
            arch      = $t.arch
            file      = $artifactName
            label     = $t.label
            sizeBytes = $size
        }
    }
}

# Reset GOOS/GOARCH so subsequent shell sessions don't inherit
# stale cross-compile state.
$env:GOOS   = ""
$env:GOARCH = ""

# Write SHA256SUMS.txt in the canonical `<sha2>  <filename>` format
# so downstream consumers can validate with `sha256sum -c`.
#
# Using the .NET SHA256 API directly avoids a Get-FileHash dependency:
# Get-FileHash is documented as PS 4.0+ but is missing on some
# stripped Windows Server SKUs and on constrained-language hosts.
# Cryptography.SHA256 has been there since .NET 4.0.
Write-Host ""
Write-Host "Computing SHA256 checksums..." -ForegroundColor Cyan
function Get-Sha256 {
    param([string]$Path)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $stream = [System.IO.File]::OpenRead($Path)
        try {
            $bytes = $sha.ComputeHash($stream)
        } finally {
            $stream.Close()
        }
    } finally {
        $sha.Dispose()
    }
    return ([System.BitConverter]::ToString($bytes) -replace "-", "").ToLower()
}

$sumsPath = Join-Path $OutDir "SHA256SUMS.txt"
$sumLines = @()
Get-ChildItem -Path $OutDir -File |
    Where-Object { $_.Name -ne "SHA256SUMS.txt" -and $_.Name -ne "MANIFEST.json" } |
    Sort-Object Name |
    ForEach-Object {
        $hash = Get-Sha256 -Path $_.FullName
        $sumLines += "$hash  $($_.Name)"
    }
$sumLines -join "`n" | Set-Content -Path $sumsPath -Encoding ASCII

# Stamp the manifest with checksums so the download page can render
# them inline (and to make sha256 verification a pure JSON read on
# the client side, no shell required).
foreach ($c in $Manifest.components) {
    $p = Join-Path $OutDir $c.file
    $c.sha256 = Get-Sha256 -Path $p
}

$manifestPath = Join-Path $OutDir "MANIFEST.json"
# UTF-8 *without* BOM. Set-Content -Encoding UTF8 in PS 5.1 prepends
# the EF BB BF BOM, which Go's encoding/json rejects with
# "invalid character 'ï' looking for beginning of value". Write
# directly via .NET to get UTF-8-no-BOM that every consumer
# (browsers via fetch(), the auto-updater's json.Unmarshal, sha256
# tools) parses identically.
$manifestJson = $Manifest | ConvertTo-Json -Depth 6
[System.IO.File]::WriteAllText($manifestPath, $manifestJson, (New-Object System.Text.UTF8Encoding $false))

Write-Host ""
Write-Host "=== Release built at $OutDir ===" -ForegroundColor Green
Get-ChildItem -Path $OutDir | Select-Object Name, @{N="Size(MiB)";E={ [math]::Round($_.Length/1MB,2) }} | Format-Table -AutoSize
Write-Host ""
Write-Host "Next steps:" -ForegroundColor Cyan
Write-Host "  1. (optional) Code-sign the .exe and notarize the macOS bins."
Write-Host "  2. Upload the directory to https://qsdm.tech/releases/$VersionTag/"
Write-Host "  3. Bump deploy/landing/download.html (or its config) to point at $VersionTag."

} finally {
    Pop-Location
}
