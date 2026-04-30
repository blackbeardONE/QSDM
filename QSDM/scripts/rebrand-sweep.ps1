# rebrand-sweep.ps1 — replace qsdmplus / QSDM+ variants with qsdm / QSDM across
# source, config, docs, scripts, and website files.
#
# !! DANGER — DO NOT RE-RUN BLINDLY !!
# This script executed the one-time qsdmplus -> qsdm migration in commit
# db9b590. The migration is COMPLETE and committed; running it again
# is unsafe because some files now legitimately contain
# QSDMPLUS_<name> tokens as the LEGACY half of a (preferred, legacy)
# deprecation pair (e.g. _env_preferred("QSDM_X", "QSDMPLUS_X") in
# apps/qsdm-nvidia-ngc/validator_phase1.py, the parallel exports in
# apps/qsdm-nvidia-ngc/scripts/wire-qsdm.{sh,ps1}, and the dual-name
# documentation in vps.txt). A blind re-run would re-collapse those
# pairs and silently kill the legacy fallback for every operator who
# hasn't yet rotated their secrets to the new env-var name.
#
# History: an audit on 2026-04-30 (commit b0b2f77 + this commit)
# discovered that the original rebrand had collapsed twelve
# _env_preferred() call sites in validator_phase1.py, the grep regex
# in install_ngc_sidecar_vps.py, four duplicated exports in
# wire-qsdm.{sh,ps1}, the local-attest preflight, the docker-compose
# comment, and three vps.txt documentation lines, by exactly this
# mechanism. The fixes restored the QSDMPLUS_<name> halves; this
# guard exists so they survive a future operator running this script
# under the impression that "it's idempotent".
#
# To run it anyway (e.g. you're handling a NEW migration that needs
# the same shape), pass -IAcceptThatThisRecollapsesLegacyFallbacks
# explicitly. The flag name is deliberately long, ugly, and
# self-explaining — there is no reasonable use case where you should
# add it without first reading the audit notes in CHANGELOG.md.
#
# Replacement rules (applied in this order; case-sensitive):
#   QSDMPLUS_ -> QSDM_
#   QSDMPlus  -> QSDM
#   QSDMplus  -> QSDM
#   qsdmplus  -> qsdm
#   QSDM+     -> QSDM
#   qsdm+     -> qsdm

[CmdletBinding()]
param(
    [string]$Root = '',
    [switch]$DryRun,
    # Required to actually mutate files. Without it the script halts
    # before reading anything.
    [switch]$IAcceptThatThisRecollapsesLegacyFallbacks
)

$ErrorActionPreference = 'Stop'

if (-not $IAcceptThatThisRecollapsesLegacyFallbacks -and -not $DryRun) {
    Write-Host ""                                                              -ForegroundColor Red
    Write-Host "rebrand-sweep.ps1: refusing to run without an explicit"          -ForegroundColor Red
    Write-Host "  -IAcceptThatThisRecollapsesLegacyFallbacks flag."              -ForegroundColor Red
    Write-Host ""                                                              -ForegroundColor Red
    Write-Host "  The qsdmplus -> qsdm rebrand executed in commit db9b590"       -ForegroundColor Yellow
    Write-Host "  and the cleanup pass in commits b0b2f77 + the audit follow-"    -ForegroundColor Yellow
    Write-Host "  up. Some files now legitimately contain QSDMPLUS_<name>"        -ForegroundColor Yellow
    Write-Host "  tokens as the LEGACY half of (preferred, legacy) pairs."        -ForegroundColor Yellow
    Write-Host "  A blind re-run RE-COLLAPSES those pairs and breaks the"         -ForegroundColor Yellow
    Write-Host "  deprecation-window fallback for every operator still on"        -ForegroundColor Yellow
    Write-Host "  the legacy env-var names."                                      -ForegroundColor Yellow
    Write-Host ""                                                              -ForegroundColor Red
    Write-Host "  If you understand and still want to proceed, re-run with:"      -ForegroundColor Yellow
    Write-Host "    .\rebrand-sweep.ps1 -IAcceptThatThisRecollapsesLegacyFallbacks" -ForegroundColor Yellow
    Write-Host ""                                                              -ForegroundColor Red
    Write-Host "  Or use -DryRun to preview without mutating files."              -ForegroundColor Yellow
    Write-Host ""
    exit 2
}

if ([string]::IsNullOrEmpty($Root)) {
    if ($PSScriptRoot) {
        $Root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
    } else {
        $Root = (Get-Location).Path
    }
}

$IncludeExts = @(
    '.go','.rs','.js','.ts','.jsx','.tsx','.py','.sh','.ps1','.cmd','.bat',
    '.yaml','.yml','.toml','.json','.env','.example','.md','.html','.css',
    '.c','.cu','.h','.service','.cfg','.conf','.ini','.mod','.sum','.txt'
)

$IncludeFixedNames = @('Dockerfile','Makefile','Caddyfile','.gitignore')

$ExcludePatterns = @(
    '\\wasm_module\\target\\',
    '\\target\\debug\\',
    '\\target\\release\\',
    '\\\.git\\',
    '\\node_modules\\',
    '\\vendor\\',
    '\\bin\\',
    '\\dist\\',
    'rebrand-sweep\.ps1$',
    'test\.log$',
    '\.d$'
)

function Should-Process([System.IO.FileInfo]$f) {
    $full = $f.FullName
    foreach ($p in $ExcludePatterns) {
        if ($full -match $p) { return $false }
    }
    $ext = $f.Extension.ToLowerInvariant()
    if ($IncludeExts -contains $ext) { return $true }
    if ($IncludeFixedNames -contains $f.Name) { return $true }
    if ($f.Name -match '^Dockerfile(\..+)?$') { return $true }
    return $false
}

function Rebrand-Content([string]$s) {
    $out = $s
    # Order matters: start with the longest all-caps variant so we hit
    # QSDMPLUS before QSDM, and underscored form before plain.
    $out = $out -creplace 'QSDMPLUS_', 'QSDM_'
    $out = $out -creplace 'QSDMPLUS',  'QSDM'
    $out = $out -creplace 'QSDMPlus',  'QSDM'
    $out = $out -creplace 'QSDMplus',  'QSDM'
    $out = $out -creplace 'qsdmplus',  'qsdm'
    $out = $out -creplace 'QSDM\+',    'QSDM'
    $out = $out -creplace 'qsdm\+',    'qsdm'
    return $out
}

Write-Host "Scanning $Root ..."
$files = Get-ChildItem -LiteralPath $Root -Recurse -File | Where-Object { Should-Process $_ }
Write-Host "Candidates: $($files.Count)"

$changedCount = 0
$totalHits    = 0
$changedFiles = New-Object System.Collections.Generic.List[string]

foreach ($f in $files) {
    try {
        $orig = [System.IO.File]::ReadAllText($f.FullName)
    } catch {
        Write-Warning "read failed: $($f.FullName): $_"
        continue
    }
    if ([string]::IsNullOrEmpty($orig)) { continue }

    $hits = ([regex]::Matches($orig, 'qsdmplus|QSDMPLUS|QSDMPlus|QSDMplus|QSDM\+|qsdm\+')).Count
    if ($hits -eq 0) { continue }

    $new = Rebrand-Content $orig
    if ($new -eq $orig) { continue }

    if (-not $DryRun) {
        # Preserve original encoding by writing without BOM
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($f.FullName, $new, $utf8NoBom)
    }
    $changedCount++
    $totalHits += $hits
    $changedFiles.Add("[$hits] $($f.FullName)")
}

$changedFiles | ForEach-Object { Write-Host $_ }
Write-Host '---'
if ($DryRun) {
    Write-Host "DRY RUN: would change $changedCount files, $totalHits occurrences."
} else {
    Write-Host "Changed $changedCount files, $totalHits occurrences."
}
