#requires -Version 5.1
#requires -RunAsAdministrator

[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $env:ProgramData "QSDM\Validator"),
    [string]$TaskName = "QSDM-Validator",
    [string]$HealthUrl = "http://127.0.0.1:8080/api/v1/health/live",
    [ValidateRange(1, 3600)][int]$HealthTimeoutSeconds = 120
)

$ErrorActionPreference = "Stop"

function Test-ReparsePoint {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $false }
    return [bool]((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)
}

function Stop-InstalledValidator {
    param([string]$ScheduledTaskName, [string]$ExecutablePath)
    Stop-ScheduledTask -TaskName $ScheduledTaskName -ErrorAction SilentlyContinue
    $target = [IO.Path]::GetFullPath($ExecutablePath)
    Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $target } |
        ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
}

function Resolve-LoopbackHealthEndpoint {
    param([Parameter(Mandatory)][string]$Url)

    $match = [regex]::Match(
        $Url,
        '^https?://(?:127\.0\.0\.1|localhost|\[::1\]):(?<port>[0-9]{1,5})(?:/[A-Za-z0-9._~/%-]*)?\z',
        [Text.RegularExpressions.RegexOptions]::IgnoreCase
    )
    if (-not $match.Success) {
        throw "-HealthUrl must be an explicit loopback HTTP(S) URL with a port and no query or fragment."
    }

    $uri = $null
    if (-not [Uri]::TryCreate($Url, [UriKind]::Absolute, [ref]$uri)) {
        throw "Invalid -HealthUrl."
    }
    $port = [int]$match.Groups['port'].Value
    if ($port -lt 1 -or $port -gt 65535) { throw "-HealthUrl port is out of range." }

    return [pscustomobject]@{ Uri = $uri; Port = $port }
}

function Wait-ValidatorLive {
    param([Uri]$Url, [int]$Port, [int]$TimeoutSeconds, [string]$ExecutablePath)
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    $target = [IO.Path]::GetFullPath($ExecutablePath)
    while ([DateTime]::UtcNow -lt $deadline) {
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 5
            $processIds = @(
                Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
                    Where-Object { $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $target } |
                    ForEach-Object { [uint32]$_.ProcessId }
            )
            $ownsListener = if ($processIds.Count -gt 0) {
                Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue |
                    Where-Object { $processIds -contains [uint32]$_.OwningProcess } |
                    Select-Object -First 1
            }
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300 -and $ownsListener) {
                return $true
            }
        } catch {}
        Start-Sleep -Seconds 2
    }
    return $false
}

if ($TaskName -notmatch '^[A-Za-z0-9_. -]+$') { throw "Invalid -TaskName." }
if (-not [IO.Path]::IsPathRooted($InstallDir)) { throw "-InstallDir must be absolute." }
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
$InstallRoot = [IO.Path]::GetPathRoot($InstallDir)
if ($InstallDir.TrimEnd('\') -eq $InstallRoot.TrimEnd('\')) { throw "Refusing a drive-root install directory." }
if ($InstallRoot -notmatch '^[A-Za-z]:\\$') { throw "-InstallDir must be on a local drive." }
$InstallDir = $InstallDir.TrimEnd('\')
if (-not (Test-Path -LiteralPath $InstallDir -PathType Container)) { throw "Install directory is missing: $InstallDir" }
if (Test-ReparsePoint -Path $InstallDir) { throw "Refusing a reparse-point install directory." }

$StateFile = Join-Path $InstallDir "validator-install-state.json"
$TargetBinary = Join-Path $InstallDir "qsdm-validator.exe"
if (-not (Test-Path -LiteralPath $StateFile -PathType Leaf) -or (Test-ReparsePoint -Path $StateFile)) {
    throw "Install state is missing or is a reparse point: $StateFile"
}
if (-not (Test-Path -LiteralPath $TargetBinary -PathType Leaf) -or (Test-ReparsePoint -Path $TargetBinary)) {
    throw "Installed validator binary is missing or is a reparse point: $TargetBinary"
}

$state = Get-Content -Raw -LiteralPath $StateFile | ConvertFrom-Json
if (-not $PSBoundParameters.ContainsKey('TaskName') -and $state.task) { $TaskName = [string]$state.task }
if (-not $PSBoundParameters.ContainsKey('HealthUrl') -and $state.health) { $HealthUrl = [string]$state.health }
if ($TaskName -notmatch '^[A-Za-z0-9_. -]+$') { throw "Invalid scheduled task name in install state." }
$healthEndpoint = Resolve-LoopbackHealthEndpoint -Url $HealthUrl
$recordedPrevious = [string]$state.previous
if ([string]::IsNullOrWhiteSpace($recordedPrevious)) { throw "No previous validator binary is recorded." }
$BackupPath = [IO.Path]::GetFullPath($recordedPrevious)
if (-not $BackupPath.StartsWith($InstallDir + '\', [StringComparison]::OrdinalIgnoreCase)) {
    throw "Recorded backup is outside the install directory."
}
if (-not (Test-Path -LiteralPath $BackupPath -PathType Leaf) -or (Test-ReparsePoint -Path $BackupPath)) {
    throw "No valid previous validator binary is recorded."
}
$recordedBackupHash = [string]$state.previousSha256
if ($recordedBackupHash -notmatch '^[0-9a-fA-F]{64}$') { throw "No valid previous binary checksum is recorded." }
$actualBackupHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $BackupPath).Hash
if ($actualBackupHash -ne $recordedBackupHash) { throw "Previous validator binary checksum mismatch." }

$backupVersion = ((& $BackupPath --version 2>&1) | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $backupVersion -notmatch '^qsdm\s') { throw "Backup version check failed." }
$existingTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if (-not $existingTask) { throw "Scheduled task is missing: $TaskName" }
$wasTaskRunning = [bool]($existingTask.State -eq 'Running')

$failed = Join-Path $InstallDir ("qsdm-validator.failed.{0}.{1}.exe" -f [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ'), $PID)
$rollbackApplied = $false
$rollbackStarted = $false
try {
    $rollbackStarted = $true
    Stop-InstalledValidator -ScheduledTaskName $TaskName -ExecutablePath $TargetBinary
    Copy-Item -LiteralPath $TargetBinary -Destination $failed
    $rollbackApplied = $true
    Copy-Item -LiteralPath $BackupPath -Destination $TargetBinary -Force
    Start-ScheduledTask -TaskName $TaskName

    if (-not (Wait-ValidatorLive -Url $healthEndpoint.Uri -Port $healthEndpoint.Port -TimeoutSeconds $HealthTimeoutSeconds -ExecutablePath $TargetBinary)) {
        throw "Rollback binary did not become live from the restored executable within $HealthTimeoutSeconds seconds."
    }

    $rolledBackState = [ordered]@{
        status = 'installed'
        version = $backupVersion
        config = [string]$state.config
        binary = $TargetBinary
        previous = $failed
        previousSha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $failed).Hash.ToLowerInvariant()
        task = $TaskName
        health = $HealthUrl
        data = [string]$state.data
        installedAt = [DateTime]::UtcNow.ToString('o')
    }
    $stateTemp = "$StateFile.tmp.$PID"
    [IO.File]::WriteAllText($stateTemp, ($rolledBackState | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $stateTemp -Destination $StateFile -Force
    $rollbackApplied = $false
    Write-Host "Rollback complete: $backupVersion" -ForegroundColor Green
    Write-Host "Replaced build preserved at $failed"
} catch {
    $failure = $_
    if ($rollbackStarted) {
        Write-Warning "Rollback failed; restoring the validator that was active before rollback."
        try {
            if ($rollbackApplied) {
                Stop-InstalledValidator -ScheduledTaskName $TaskName -ExecutablePath $TargetBinary
                Copy-Item -LiteralPath $failed -Destination $TargetBinary -Force
            }
            if ($wasTaskRunning) { Start-ScheduledTask -TaskName $TaskName }
        } catch { Write-Warning "Could not restore the pre-rollback validator: $($_.Exception.Message)" }
    }
    throw $failure
}
