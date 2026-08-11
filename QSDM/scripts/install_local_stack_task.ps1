param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [string]$Relay = "https://api.qsdm.tech",
    [string]$Slot = "home-validator",
    [string]$TaskName = "QSDM-Local-Stack",
    [string]$TaskUser = "",
    [int]$IntervalSeconds = 30,
    [int]$RestartAfterFailures = 10,
    [int]$GatewayRestartAfterFailures = 3,
    [switch]$NoPublicGatewayCheck,
    [switch]$Highest,
    [switch]$RemoveStartupFallback,
    [switch]$NoStartupFallback,
    [switch]$NoRunNow
)

$ErrorActionPreference = "Stop"

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
$TaskUser = $TaskUser.Trim()
if ([string]::IsNullOrWhiteSpace($TaskUser)) {
    $TaskUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
}
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

$watchdogArgs = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$WatchdogScript`" -QsdmRoot `"$QsdmRoot`" -Relay `"$Relay`" -Slot `"$Slot`" -IntervalSeconds $IntervalSeconds -RestartAfterFailures $RestartAfterFailures -GatewayRestartAfterFailures $GatewayRestartAfterFailures"
if (-not $NoPublicGatewayCheck) {
    $watchdogArgs += " -CheckPublicGateway"
} else {
    $watchdogArgs += " -NoPublicGatewayCheck"
}
$taskRun = "powershell.exe $watchdogArgs"

Write-InstallLog "install requested task=$TaskName user=$TaskUser trigger=startup logon_type=S4U highest=$Highest root=$QsdmRoot check_public_gateway=$(-not $NoPublicGatewayCheck.IsPresent)"

try {
    $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $watchdogArgs -WorkingDirectory $QsdmRoot
    # AtLogOn tasks and Startup-folder launchers both require an interactive
    # user session. A boot trigger with S4U keeps the stack supervised after a
    # headless restart without storing the user's Windows password.
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -RestartCount 999 `
        -RestartInterval (New-TimeSpan -Minutes 1) `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -MultipleInstances IgnoreNew
    $runLevel = if ($Highest) { "Highest" } else { "Limited" }
    $principal = New-ScheduledTaskPrincipal `
        -UserId $TaskUser `
        -LogonType S4U `
        -RunLevel $runLevel

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Settings $settings `
        -Principal $principal `
        -Force `
        -ErrorAction Stop | Out-Null

    $registeredTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction Stop
    if (-not $registeredTask) {
        throw "Scheduled task registration returned without creating $TaskName"
    }
    $hasBootTrigger = @($registeredTask.Triggers | Where-Object {
        $_.CimClass.CimClassName -eq "MSFT_TaskBootTrigger"
    }).Count -gt 0
    if (-not $hasBootTrigger) {
        throw "Scheduled task $TaskName was created without an AtStartup trigger"
    }
    if ([string]$registeredTask.Principal.LogonType -ne "S4U") {
        throw "Scheduled task $TaskName was created with logon type $($registeredTask.Principal.LogonType), expected S4U"
    }

    Write-InstallLog "registered scheduled task user=$TaskUser trigger=startup logon_type=S4U run_level=$runLevel"
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
        Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop
        Write-InstallLog "started scheduled task"
    } catch {
        Write-InstallLog "scheduled task start failed: $($_.Exception.Message)"
        throw "Scheduled task $TaskName was created but could not be started"
    }
}

Write-Host "Installed scheduled task $TaskName"
Write-Host "Action: $taskRun"
Write-Host "Log: $LogPath"
