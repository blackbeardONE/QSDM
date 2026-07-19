$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$scriptPath = Join-Path $PSScriptRoot "maintain_generated_cache.ps1"
$testRoot = Join-Path ([IO.Path]::GetTempPath()) `
    ("qsdm-cache-maintenance-{0}" -f [Guid]::NewGuid().ToString("N"))
$qsdmRoot = Join-Path $testRoot "QSDM"
$releaseRoot = Join-Path (Join-Path $testRoot ".cache") "release"
$outsidePath = Join-Path $testRoot "outside-sentinel.txt"

try {
    New-Item -ItemType Directory -Force -Path $qsdmRoot, $releaseRoot | Out-Null
    [IO.File]::WriteAllText($outsidePath, "preserve")
    for ($index = 0; $index -lt 5; $index++) {
        $path = Join-Path $releaseRoot ("build-{0}" -f $index)
        New-Item -ItemType Directory -Force -Path $path | Out-Null
        [IO.File]::WriteAllBytes((Join-Path $path "artifact.bin"), [byte[]](1..32))
        $stamp = if ($index -lt 3) {
            (Get-Date).ToUniversalTime().AddDays(-20 - $index)
        } else {
            (Get-Date).ToUniversalTime().AddMinutes(-$index)
        }
        (Get-Item -LiteralPath $path).LastWriteTimeUtc = $stamp
    }

    $preview = & $scriptPath -QsdmRoot $qsdmRoot -KeepNewest 2 `
        -MaxAgeDays 7 -MinimumFreeGiB 0 -TargetFreeGiB 0 | ConvertFrom-Json
    if ($preview.applied -or $preview.removed_count -ne 3) {
        throw "Preview did not select exactly three expired builds"
    }
    if (@(Get-ChildItem -LiteralPath $releaseRoot -Directory).Count -ne 5) {
        throw "Preview changed the generated cache"
    }

    $applied = & $scriptPath -QsdmRoot $qsdmRoot -KeepNewest 2 `
        -MaxAgeDays 7 -MinimumFreeGiB 0 -TargetFreeGiB 0 -Apply |
        ConvertFrom-Json
    if (-not $applied.applied -or $applied.removed_count -ne 3) {
        throw "Applied cleanup did not remove exactly three expired builds"
    }
    $remaining = @(Get-ChildItem -LiteralPath $releaseRoot -Directory |
        Select-Object -ExpandProperty Name | Sort-Object)
    if (($remaining -join ",") -ne "build-3,build-4") {
        throw "Unexpected retained builds: $($remaining -join ',')"
    }
    if (-not (Test-Path -LiteralPath $outsidePath -PathType Leaf)) {
        throw "Cleanup touched data outside its approved cache roots"
    }

    $emptySelection = & $scriptPath -QsdmRoot $qsdmRoot -KeepNewest 2 `
        -MaxAgeDays 3650 -MinimumFreeGiB 0 -TargetFreeGiB 0 |
        ConvertFrom-Json
    if ($emptySelection.removed_count -ne 0) {
        throw "A cache with no eligible candidates produced removals"
    }
    Write-Host "Generated cache maintenance tests passed"
} finally {
    if (Test-Path -LiteralPath $testRoot) {
        $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
        $resolvedTest = [IO.Path]::GetFullPath($testRoot)
        if (-not $resolvedTest.StartsWith($resolvedTemp, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing test cleanup outside the temporary directory: $resolvedTest"
        }
        Remove-Item -LiteralPath $resolvedTest -Recurse -Force -ErrorAction SilentlyContinue
    }
}
