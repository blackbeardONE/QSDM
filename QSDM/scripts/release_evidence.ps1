# release_evidence.ps1
# ====================
# One-shot release-evidence collector. Emits a self-contained bundle
# directory the next human reviewer (operator, auditor, foundation
# member) can read end-to-end without re-running the toolchain.
#
# What's inside the bundle:
#   00_MANIFEST.txt        - sha256 + size of every file, plus
#                            the bundle's own one-line summary.
#   01_environment.txt     - host info, go version, git HEAD, dirty
#                            flag, node + npm versions.
#   02_audit_report.md     - cmd/auditreport rendered against
#                            pkg/audit/checklist.go (reviewer-facing).
#   03_go_mod_verify.txt   - `go mod verify` (cryptographic check of
#                            every module in go.sum).
#   04_govulncheck.txt     - reachable-call-graph CVE scan. The
#                            allowlist is intentionally empty.
#   05_go_vet.txt          - default + soak-tag vet sweep.
#   06_go_test_full.txt    - `go test ./... -count=1` (non-`-short`),
#                            tail captured. Pass/fail per package.
#   07_jssdk_tests.txt     - `node --test sdk/javascript/qsdm.test.js`.
#   08_npm_pack.txt        - `npm pack --dry-run` from sdk/javascript/
#                            so the auditor sees the exact tarball
#                            manifest that would land on npmjs.com.
#   09_binaries.txt        - sha256 + size + `--version` banner of
#                            every cmd/<name> binary built clean.
#   10_soak_summary.txt    - one-line summaries of the most recent
#                            mempool / pubsub soak runs that have a
#                            tail-log captured in repo root.
#
# Usage:
#   # default: emit to _tmp_release_evidence_<UTC-timestamp>/
#   pwsh QSDM/scripts/release_evidence.ps1
#
#   # custom output path:
#   pwsh QSDM/scripts/release_evidence.ps1 -OutDir D:\releases\v0.3.0
#
#   # skip the slow steps (test suite + soak smoke):
#   pwsh QSDM/scripts/release_evidence.ps1 -Quick
#
# Exit code:
#   0 - bundle written; reviewer should still flip checklist items
#   2 - a hard precondition failed (git missing, go missing, etc.)
#       and the partial bundle is incomplete.
#
# The default output prefix (`_tmp_release_evidence_*`) is matched by
# the existing `_tmp_*` rule in .gitignore from session 73, so the
# bundle stays out of commits unless the operator deliberately moves
# or renames it.

[CmdletBinding()]
param(
    [string]$OutDir = $null,
    [switch]$Quick
)

$ErrorActionPreference = 'Stop'

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$sourceDir = Join-Path $repoRoot 'QSDM/source'
$jsSdkDir = Join-Path $sourceDir 'sdk/javascript'

if (-not $OutDir) {
    $ts = (Get-Date -AsUTC).ToString('yyyyMMddTHHmmssZ')
    $OutDir = Join-Path $repoRoot "_tmp_release_evidence_$ts"
}

if (-not (Test-Path $OutDir)) {
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
}
$OutDir = (Resolve-Path $OutDir).Path
Write-Host "==> evidence bundle dir: $OutDir"

# Helper that runs a command, captures stdout+stderr to a file,
# stamps a header, and never throws on non-zero exit (the captured
# exit code is part of the evidence).
function Invoke-CaptureStep {
    param(
        [string]$StepFile,
        [string]$Title,
        [scriptblock]$Body
    )
    $path = Join-Path $OutDir $StepFile
    Write-Host "==> $StepFile  ($Title)"
    $headerLines = @(
        "# $Title",
        "# captured: $((Get-Date -AsUTC).ToString('o'))",
        "# host: $env:COMPUTERNAME / $([System.Environment]::OSVersion.VersionString)",
        ('-' * 72)
    )
    $headerLines -join "`r`n" | Set-Content -Path $path -Encoding utf8
    try {
        $output = & $Body 2>&1
        $exit = $LASTEXITCODE
        if ($null -ne $output) {
            $output | Out-String -Width 200 | Add-Content -Path $path -Encoding utf8
        }
        ('-' * 72) | Add-Content -Path $path -Encoding utf8
        "# exit_code: $exit" | Add-Content -Path $path -Encoding utf8
    } catch {
        ('-' * 72) | Add-Content -Path $path -Encoding utf8
        "# exception: $($_.Exception.Message)" | Add-Content -Path $path -Encoding utf8
    }
}

# 01 - environment fingerprint.
Invoke-CaptureStep '01_environment.txt' 'Build environment fingerprint' {
    "OS: $([System.Environment]::OSVersion.VersionString)"
    "Arch: $([System.Environment]::Is64BitOperatingSystem ? 'x64' : 'x86')"
    "Host: $env:COMPUTERNAME"
    "PowerShell: $($PSVersionTable.PSVersion)"
    "Captured (UTC): $((Get-Date -AsUTC).ToString('o'))"
    ''
    '--- go ---'
    # Capture the LOCAL bootstrap toolchain AND the version Go will
    # auto-fetch and actually use to build the project (per the `go`
    # directive in QSDM/source/go.mod). On Go 1.21+ these differ when
    # the bootstrap toolchain is older than the directive; the binaries
    # in 09_binaries.txt are built with the in-module version.
    'local bootstrap toolchain:'
    & go version
    'in-module toolchain (the one that builds the binaries):'
    Push-Location $sourceDir
    try {
        & go version
    } finally {
        Pop-Location
    }
    ''
    '--- git ---'
    Push-Location $repoRoot
    try {
        "HEAD: $(git rev-parse HEAD)"
        "Branch: $(git rev-parse --abbrev-ref HEAD)"
        "Origin: $(git remote get-url origin 2>$null)"
        "Working tree dirty? (file count):"
        (git status --porcelain | Measure-Object -Line).Lines
    } finally {
        Pop-Location
    }
    ''
    '--- node ---'
    & node --version
    & npm --version
}

# 02 - audit checklist as the project sees it.
Invoke-CaptureStep '02_audit_report.md' 'cmd/auditreport markdown render' {
    Push-Location $sourceDir
    try {
        $env:CGO_ENABLED = '0'
        & go run ./cmd/auditreport -format markdown -gate=$false -notes=$true
    } finally {
        Pop-Location
    }
}

# 03 - go mod cryptographic verify.
Invoke-CaptureStep '03_go_mod_verify.txt' 'go mod verify (cryptographic)' {
    Push-Location $sourceDir
    try {
        $env:CGO_ENABLED = '0'
        & go mod verify
    } finally {
        Pop-Location
    }
}

# 04 - govulncheck reachable scan.
if ($Quick) {
    "# skipped (-Quick)" | Set-Content -Path (Join-Path $OutDir '04_govulncheck.txt') -Encoding utf8
} else {
    Invoke-CaptureStep '04_govulncheck.txt' 'govulncheck ./... (reachable findings)' {
        Push-Location $sourceDir
        try {
            $env:CGO_ENABLED = '0'
            & go run golang.org/x/vuln/cmd/govulncheck@latest ./...
        } finally {
            Pop-Location
        }
    }
}

# 05 - go vet default + soak tag.
Invoke-CaptureStep '05_go_vet.txt' 'go vet ./... (default + soak)' {
    Push-Location $sourceDir
    try {
        $env:CGO_ENABLED = '0'
        '--- go vet ./... ---'
        & go vet ./...
        $rcDefault = $LASTEXITCODE
        ''
        '--- go vet -tags soak ./tests/... ---'
        & go vet -tags soak ./tests/...
        $rcSoak = $LASTEXITCODE
        ''
        "# vet default exit: $rcDefault"
        "# vet soak    exit: $rcSoak"
    } finally {
        Pop-Location
    }
}

# 06 - full non-short test suite (the long step).
if ($Quick) {
    "# skipped (-Quick)" | Set-Content -Path (Join-Path $OutDir '06_go_test_full.txt') -Encoding utf8
} else {
    Invoke-CaptureStep '06_go_test_full.txt' 'go test ./... -count=1 (non-short)' {
        Push-Location $sourceDir
        try {
            $env:CGO_ENABLED = '0'
            $env:QSDM_METRICS_REGISTER_STRICT = '1'
            & go test ./... -count=1 -timeout 900s
        } finally {
            Pop-Location
        }
    }
}

# 07 - JS SDK tests.
Invoke-CaptureStep '07_jssdk_tests.txt' 'node --test qsdm.test.js' {
    Push-Location $jsSdkDir
    try {
        & node --test qsdm.test.js
    } finally {
        Pop-Location
    }
}

# 08 - npm pack dry-run (auditor sees exact published tarball).
Invoke-CaptureStep '08_npm_pack.txt' 'npm pack --dry-run' {
    Push-Location $jsSdkDir
    try {
        & npm pack --dry-run
    } finally {
        Pop-Location
    }
}

# 09 - build every cmd binary, capture sha256 + version banner.
Invoke-CaptureStep '09_binaries.txt' 'cmd/* clean builds + sha256 + --version' {
    Push-Location $sourceDir
    try {
        $env:CGO_ENABLED = '0'
        $binTmp = Join-Path $OutDir '_binaries_workdir'
        New-Item -ItemType Directory -Path $binTmp -Force | Out-Null
        $cmdRoot = Join-Path $sourceDir 'cmd'
        $cmds = Get-ChildItem -Path $cmdRoot -Directory | ForEach-Object { $_.Name }
        foreach ($cmd in $cmds) {
            $outBin = Join-Path $binTmp "$cmd.exe"
            $ldflags = '-s -w -X github.com/blackbeardONE/QSDM/pkg/buildinfo.Version=evidence-bundle -X github.com/blackbeardONE/QSDM/pkg/buildinfo.GitSHA=' + (git rev-parse --short HEAD)
            $buildOut = & go build -trimpath -ldflags="$ldflags" -o $outBin "./cmd/$cmd" 2>&1
            $rc = $LASTEXITCODE
            if ($rc -ne 0) {
                "== $cmd =="
                "  build: FAILED ($rc)"
                $buildOut | Out-String -Width 200
                continue
            }
            $hash = (Get-FileHash $outBin -Algorithm SHA256).Hash.ToLower()
            $size = (Get-Item $outBin).Length
            $banner = ''
            try {
                $banner = (& $outBin '--version' 2>&1 | Select-Object -First 1) -as [string]
            } catch {
                $banner = '(no --version)'
            }
            if (-not $banner) { $banner = '(empty --version)' }
            "== $cmd =="
            "  sha256: $hash"
            "  size:   $size bytes"
            "  banner: $banner"
        }
        Remove-Item $binTmp -Recurse -Force -ErrorAction SilentlyContinue
    } finally {
        Pop-Location
    }
}

# 10 - soak summaries (best-effort: pick up any prior tail log).
Invoke-CaptureStep '10_soak_summary.txt' 'most recent soak summaries (best-effort)' {
    $logs = Get-ChildItem -Path $repoRoot -Filter '_tmp_soak_*' -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending
    if ($logs.Count -eq 0) {
        'no _tmp_soak_*.log files found in repo root'
        'run mempool soak (10 min):'
        '  cd QSDM/source && QSDM_SOAK_DURATION=10m go test -tags soak ./tests/ -run TestSoak_Mempool -v'
        'run pubsub soak (10 min, 4 hosts):'
        '  cd QSDM/source && QSDM_SOAK_DURATION=10m QSDM_SOAK_HOSTS=4 go test -tags soak ./tests/ -run TestSoak_PubsubMultiHostFanout -v'
        return
    }
    foreach ($log in $logs) {
        "== $($log.Name) ($(($log.Length/1KB).ToString('F1')) KB, last write $($log.LastWriteTime.ToString('o'))) =="
        $tail = Get-Content $log.FullName -Tail 20
        $tail | ForEach-Object { "  $_" }
        ''
    }
}

# 00 - master manifest LAST, after every other artefact exists.
$manifestPath = Join-Path $OutDir '00_MANIFEST.txt'
$artefacts = Get-ChildItem -Path $OutDir -File | Where-Object { $_.Name -ne '00_MANIFEST.txt' } | Sort-Object Name
Push-Location $sourceDir
try {
    $inModuleGo = (& go version 2>&1) -join ' '
} finally {
    Pop-Location
}
$manifestLines = @(
    '# QSDM release-evidence bundle',
    "# generated: $((Get-Date -AsUTC).ToString('o'))",
    "# git HEAD:  $(git -C $repoRoot rev-parse HEAD)",
    "# host:      $env:COMPUTERNAME",
    "# go:        $inModuleGo  (in-module version; matches go.mod directive)",
    "# tool:      QSDM/scripts/release_evidence.ps1",
    ('-' * 72),
    'SHA256                                                            SIZE  FILE'
)
foreach ($a in $artefacts) {
    $h = (Get-FileHash $a.FullName -Algorithm SHA256).Hash.ToLower()
    $manifestLines += ('{0}  {1,9}  {2}' -f $h, $a.Length, $a.Name)
}
$manifestLines += ('-' * 72)
$manifestLines += '# How to review this bundle:'
$manifestLines += '#  1. 02_audit_report.md  - the 81-item security checklist. Flip each'
$manifestLines += '#                            critical/high item to passed/failed/waived'
$manifestLines += '#                            via cmd/auditreport -input <reviewed.json>.'
$manifestLines += '#  2. 03_go_mod_verify    - must end "all modules verified".'
$manifestLines += '#  3. 04_govulncheck      - must report zero reachable findings.'
$manifestLines += '#  4. 06_go_test_full     - last lines must show ok / no FAIL.'
$manifestLines += '#  5. 09_binaries         - every cmd should report go1.25.10+ banner.'
$manifestLines += '#  6. 10_soak_summary     - mempool + pubsub soaks PASS at >= 10 min.'
$manifestLines -join "`r`n" | Set-Content -Path $manifestPath -Encoding utf8

Write-Host ''
Write-Host '==> bundle complete'
Write-Host "    $OutDir"
Get-ChildItem $OutDir | Format-Table Name,@{Name='KB';Expression={[math]::Round($_.Length/1KB,1)}}
