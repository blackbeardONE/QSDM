param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$StatePath = "",
    [string]$TaskName = "QSDM-Release-Soak",
    [string]$ExpectedVersion = "",
    [string]$ExpectedRevision = "",
    [string]$ExpectedNodeId = "",
    [string]$StartedAtUtc = "",
    [int]$DurationHours = 24,
    [int]$IntervalMinutes = 5,
    [int]$MaxConsecutiveFailures = 3,
    [int]$MaxStagnationMinutes = 30,
    [double]$MinimumSampleRatio = 0.80,
    [switch]$NoPublicGatewayCheck,
    [switch]$RequireMiner,
    [switch]$Reset,
    [switch]$NoRunNow
)

$ErrorActionPreference = "Stop"

function Quote-TaskArgument {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$MonitorScript = Join-Path $QsdmRoot "scripts\monitor_release_soak.ps1"
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
if ([string]::IsNullOrWhiteSpace($StatePath)) {
    $StatePath = Join-Path $LocalRoot "release-soak.json"
}
$StatePath = [IO.Path]::GetFullPath($StatePath)
$InstallLog = Join-Path $LocalRoot "release-soak-task-install.log"
New-Item -ItemType Directory -Force -Path $LocalRoot | Out-Null

if (-not (Test-Path -LiteralPath $MonitorScript -PathType Leaf)) {
    throw "Missing release soak monitor: $MonitorScript"
}
if ($IntervalMinutes -lt 1) {
    throw "IntervalMinutes must be at least 1."
}

function Write-InstallLog {
    param([string]$Message)
    $stamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
    Add-Content -LiteralPath $InstallLog -Value "$stamp $Message"
}

$monitorParameters = @{
    QsdmRoot                  = $QsdmRoot
    StatePath                 = $StatePath
    ExpectedVersion           = $ExpectedVersion
    ExpectedRevision          = $ExpectedRevision
    ExpectedNodeId            = $ExpectedNodeId
    StartedAtUtc              = $StartedAtUtc
    DurationHours             = $DurationHours
    ExpectedIntervalMinutes   = $IntervalMinutes
    MaxConsecutiveFailures    = $MaxConsecutiveFailures
    MaxStagnationMinutes      = $MaxStagnationMinutes
    MinimumSampleRatio        = $MinimumSampleRatio
    NoPublicGatewayCheck      = $NoPublicGatewayCheck
    RequireMiner              = $RequireMiner
    Reset                     = $Reset
}

Write-InstallLog "install requested task=$TaskName root=$QsdmRoot interval_minutes=$IntervalMinutes duration_hours=$DurationHours"
& $MonitorScript @monitorParameters
if (-not $?) {
    throw "The initial release soak sample failed. Review $StatePath before scheduling more samples."
}

$arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File $(Quote-TaskArgument $MonitorScript)"
$arguments += " -QsdmRoot $(Quote-TaskArgument $QsdmRoot)"
$arguments += " -StatePath $(Quote-TaskArgument $StatePath)"
$arguments += " -ExpectedVersion $(Quote-TaskArgument $ExpectedVersion)"
$arguments += " -ExpectedRevision $(Quote-TaskArgument $ExpectedRevision)"
$arguments += " -ExpectedNodeId $(Quote-TaskArgument $ExpectedNodeId)"
$arguments += " -DurationHours $DurationHours"
$arguments += " -ExpectedIntervalMinutes $IntervalMinutes"
$arguments += " -MaxConsecutiveFailures $MaxConsecutiveFailures"
$arguments += " -MaxStagnationMinutes $MaxStagnationMinutes"
$arguments += " -MinimumSampleRatio $MinimumSampleRatio"
if ($NoPublicGatewayCheck) {
    $arguments += " -NoPublicGatewayCheck"
}
if ($RequireMiner) {
    $arguments += " -RequireMiner"
}

try {
    $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $arguments -WorkingDirectory $QsdmRoot
    $trigger = New-ScheduledTaskTrigger `
        -Once `
        -At (Get-Date).AddMinutes(1) `
        -RepetitionInterval (New-TimeSpan -Minutes $IntervalMinutes) `
        -RepetitionDuration (New-TimeSpan -Hours ($DurationHours + 1))
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -ExecutionTimeLimit (New-TimeSpan -Minutes 2) `
        -MultipleInstances IgnoreNew
    $principal = New-ScheduledTaskPrincipal `
        -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) `
        -LogonType Interactive `
        -RunLevel Limited

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Settings $settings `
        -Principal $principal `
        -Force `
        -ErrorAction Stop | Out-Null
    Write-InstallLog "registered scheduled task"
} catch {
    Write-InstallLog "registration failed: $($_.Exception.Message)"
    throw "Could not register scheduled task $TaskName`: $($_.Exception.Message)"
}

if (-not $NoRunNow) {
    Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop
    Write-InstallLog "started scheduled task"
}

$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction Stop
$taskInfo = Get-ScheduledTaskInfo -TaskName $TaskName -ErrorAction Stop
Write-Host "Installed release soak task $TaskName"
Write-Host "Task state: $($task.State)"
Write-Host "Next run: $($taskInfo.NextRunTime)"
Write-Host "State: $StatePath"
Write-Host "Log: $InstallLog"
