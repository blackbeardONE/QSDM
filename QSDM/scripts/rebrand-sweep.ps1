# rebrand-sweep.ps1 — replace qsdmplus / QSDM+ variants with qsdm / QSDM across
# source, config, docs, scripts, and website files. Skips build artifacts and
# binaries. Safe to run idempotently.
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
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

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
