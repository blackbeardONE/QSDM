param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [ValidateRange(1, 100)]
    [int]$KeepNewest = 4,
    [ValidateRange(1, 3650)]
    [int]$MaxAgeDays = 30,
    [ValidateRange(0, 1024)]
    [double]$MinimumFreeGiB = 5,
    [ValidateRange(0, 1024)]
    [double]$TargetFreeGiB = 8,
    [switch]$Apply
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

if ($TargetFreeGiB -lt $MinimumFreeGiB) {
    throw "TargetFreeGiB must be greater than or equal to MinimumFreeGiB"
}

$resolvedQsdmRoot = (Resolve-Path -LiteralPath $QsdmRoot).Path
$workspaceRoot = [IO.Path]::GetFullPath((Join-Path $resolvedQsdmRoot ".."))
$generatedCacheRoot = [IO.Path]::GetFullPath((Join-Path $workspaceRoot ".cache"))
$allowedRoots = @(
    (Join-Path $generatedCacheRoot "release"),
    (Join-Path $generatedCacheRoot "releases")
)
$comparison = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
    [StringComparison]::OrdinalIgnoreCase
} else {
    [StringComparison]::Ordinal
}
$pathComparer = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
    [StringComparer]::OrdinalIgnoreCase
} else {
    [StringComparer]::Ordinal
}

function Test-StrictDescendant {
    param(
        [string]$Root,
        [string]$Candidate
    )
    $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    ) + [IO.Path]::DirectorySeparatorChar
    $candidatePath = [IO.Path]::GetFullPath($Candidate)
    return $candidatePath.StartsWith($rootPath, $comparison)
}

function Get-TreeBytes {
    param([IO.FileSystemInfo]$Item)
    if (-not $Item.PSIsContainer) {
        return [uint64]$Item.Length
    }
    $sum = (Get-ChildItem -LiteralPath $Item.FullName -File -Recurse -Force `
        -ErrorAction SilentlyContinue | Measure-Object Length -Sum).Sum
    if ($null -eq $sum) {
        return [uint64]0
    }
    return [uint64]$sum
}

function Test-ContainsReparsePoint {
    param([IO.FileSystemInfo]$Item)
    if (-not $Item.PSIsContainer) {
        return $false
    }
    $nested = Get-ChildItem -LiteralPath $Item.FullName -Recurse -Force `
        -ErrorAction Stop |
        Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint } |
        Select-Object -First 1
    return $null -ne $nested
}

function Get-AvailableBytes {
    param([string]$Path)
    $root = [IO.Path]::GetPathRoot([IO.Path]::GetFullPath($Path))
    return [uint64]([IO.DriveInfo]::new($root).AvailableFreeSpace)
}

if (-not (Test-StrictDescendant -Root $workspaceRoot -Candidate $generatedCacheRoot)) {
    throw "Generated cache root escaped the workspace: $generatedCacheRoot"
}

$minimumBytes = [uint64]($MinimumFreeGiB * 1GB)
$targetBytes = [uint64]($TargetFreeGiB * 1GB)
$initialFree = Get-AvailableBytes -Path $workspaceRoot
$cutoff = (Get-Date).ToUniversalTime().AddDays(-$MaxAgeDays)
$candidates = [Collections.Generic.List[object]]::new()
$skippedReparseCount = 0

foreach ($root in $allowedRoots) {
    if (-not (Test-Path -LiteralPath $root -PathType Container)) {
        continue
    }
    $resolvedRoot = (Resolve-Path -LiteralPath $root).Path
    if (-not (Test-StrictDescendant -Root $generatedCacheRoot -Candidate $resolvedRoot)) {
        throw "Refusing unexpected cache root outside .cache: $resolvedRoot"
    }
    $items = @(Get-ChildItem -LiteralPath $resolvedRoot -Force |
        Where-Object { -not ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } |
        Sort-Object LastWriteTimeUtc -Descending)
    for ($index = $KeepNewest; $index -lt $items.Count; $index++) {
        $item = $items[$index]
        if (-not (Test-StrictDescendant -Root $resolvedRoot -Candidate $item.FullName)) {
            throw "Refusing cache candidate outside its approved root: $($item.FullName)"
        }
        if (Test-ContainsReparsePoint -Item $item) {
            $skippedReparseCount++
            continue
        }
        $candidates.Add([pscustomobject]@{
            Item = $item
            Root = $resolvedRoot
            Bytes = Get-TreeBytes -Item $item
            Expired = $item.LastWriteTimeUtc -le $cutoff
        })
    }
}

$selected = [Collections.Generic.List[object]]::new()
$selectedPaths = [Collections.Generic.HashSet[string]]::new($pathComparer)
foreach ($candidate in @($candidates | Where-Object Expired |
    Sort-Object { $_.Item.LastWriteTimeUtc })) {
    $selected.Add($candidate)
    [void]$selectedPaths.Add($candidate.Item.FullName)
}

$selectedBytes = [uint64]0
foreach ($candidate in $selected) {
    $selectedBytes += [uint64]$candidate.Bytes
}
$estimatedFree = $initialFree + $selectedBytes
if ($initialFree -lt $minimumBytes -and $estimatedFree -lt $targetBytes) {
    foreach ($candidate in @($candidates | Sort-Object { $_.Item.LastWriteTimeUtc })) {
        if ($selectedPaths.Contains($candidate.Item.FullName)) {
            continue
        }
        $selected.Add($candidate)
        [void]$selectedPaths.Add($candidate.Item.FullName)
        $estimatedFree += [uint64]$candidate.Bytes
        if ($estimatedFree -ge $targetBytes) {
            break
        }
    }
}

$removedCount = 0
$removedBytes = [uint64]0
foreach ($candidate in $selected) {
    $target = [IO.Path]::GetFullPath($candidate.Item.FullName)
    if (-not (Test-StrictDescendant -Root $candidate.Root -Candidate $target)) {
        throw "Refusing to remove cache path outside its approved root: $target"
    }
    if ($candidate.Item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
        continue
    }
    if ($Apply) {
        Remove-Item -LiteralPath $target -Recurse -Force -ErrorAction Stop
        if (Test-Path -LiteralPath $target) {
            throw "Generated cache path still exists after removal: $target"
        }
    }
    $removedCount++
    $removedBytes += [uint64]$candidate.Bytes
}

$finalFree = if ($Apply) {
    Get-AvailableBytes -Path $workspaceRoot
} else {
    $initialFree + $removedBytes
}

[ordered]@{
    schema = "qsdm.generated-cache-maintenance.v1"
    applied = $Apply.IsPresent
    workspace = $workspaceRoot
    initial_free_bytes = $initialFree
    final_free_bytes = $finalFree
    minimum_free_bytes = $minimumBytes
    target_free_bytes = $targetBytes
    removed_count = $removedCount
    removed_bytes = $removedBytes
    skipped_reparse_count = $skippedReparseCount
    disk_pressure = $initialFree -lt $minimumBytes
    reserve_satisfied = $finalFree -ge $minimumBytes
} | ConvertTo-Json -Compress
