param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$Relay = "https://api.qsdm.tech",
    [string]$Slot = "home-validator",
    [string]$TaskName = "QSDM-Local-Stack",
    [int]$IntervalSeconds = 30,
    [int]$RestartAfterFailures = 10,
    [switch]$Highest,
    [switch]$RemoveStartupFallback,
    [switch]$NoStartupFallback,
    [switch]$NoRunNow
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$WatchdogScript = Join-Path $QsdmRoot "scripts\watch_local_stack.ps1"
$LocalRoot = Join-Path $QsdmRoot "source\.cache\local-validator"
$LogPath = Join-Path $LocalRoot "local-stack-task-install.log"
New-Item -ItemType Directory -Force -Path $LocalRoot | Out-Null

function Write-InstallLog {
    param([string]$Message)
    $stamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssK"
    Add-Content -LiteralPath $LogPath -Value "$stamp $Message"
}

if (-not (Test-Path -LiteralPath $WatchdogScript)) {
    throw "Missing watchdog script: $WatchdogScript"
}

$watchdogArgs = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$WatchdogScript`" -QsdmRoot `"$QsdmRoot`" -Relay `"$Relay`" -Slot `"$Slot`" -IntervalSeconds $IntervalSeconds -RestartAfterFailures $RestartAfterFailures"
$taskRun = "powershell.exe $watchdogArgs"

Write-InstallLog "install requested task=$TaskName highest=$Highest root=$QsdmRoot"

try {
    $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $watchdogArgs -WorkingDirectory $QsdmRoot
    $trigger = New-ScheduledTaskTrigger -AtLogOn
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -MultipleInstances IgnoreNew
    $runLevel = if ($Highest) { "Highest" } else { "Limited" }
    $principal = New-ScheduledTaskPrincipal `
        -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) `
        -LogonType Interactive `
        -RunLevel $runLevel

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Settings $settings `
        -Principal $principal `
        -Force | Out-Null

    Write-InstallLog "registered scheduled task run_level=$runLevel"
} catch {
    Write-InstallLog "scheduled task registration failed: $($_.Exception.Message)"
    if ($NoStartupFallback) {
        throw "Failed to create scheduled task $TaskName`: $($_.Exception.Message)"
    }
    $startup = [Environment]::GetFolderPath("Startup")
    if ([string]::IsNullOrWhiteSpace($startup)) {
        throw "Failed to create scheduled task $TaskName and could not locate the Startup folder"
    }
    New-Item -ItemType Directory -Force -Path $startup | Out-Null
    $launcher = Join-Path $startup "$TaskName.vbs"
    $vbsCommand = $taskRun.Replace('"', '""')
    Set-Content -LiteralPath $launcher -Encoding ASCII -Value @"
Set shell = CreateObject("WScript.Shell")
shell.Run "$vbsCommand", 0, False
"@
    Write-Host "Scheduled task creation was denied; installed Startup launcher instead: $launcher"
    if (-not $NoRunNow) {
        Start-Process -FilePath "wscript.exe" -ArgumentList "`"$launcher`"" -WindowStyle Hidden
    }
    exit 0
}

if ($RemoveStartupFallback) {
    $startup = [Environment]::GetFolderPath("Startup")
    if (-not [string]::IsNullOrWhiteSpace($startup)) {
        $launcher = Join-Path $startup "$TaskName.vbs"
        Remove-Item -LiteralPath $launcher -Force -ErrorAction SilentlyContinue
        Write-InstallLog "removed startup fallback launcher=$launcher"
    }
}

if (-not $NoRunNow) {
    try {
        Start-ScheduledTask -TaskName $TaskName
        Write-InstallLog "started scheduled task"
    } catch {
        Write-InstallLog "scheduled task start failed: $($_.Exception.Message)"
        throw "Scheduled task $TaskName was created but could not be started"
    }
}

Write-Host "Installed scheduled task $TaskName"
Write-Host "Action: $taskRun"
Write-Host "Log: $LogPath"
