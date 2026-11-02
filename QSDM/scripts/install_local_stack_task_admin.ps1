param(
    [string]$QsdmRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path,
    [switch]$NoPause
)

$ErrorActionPreference = "Stop"

function Test-IsAdmin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Quote-Arg {
    param([string]$Value)
    return '"' + ($Value -replace '"', '\"') + '"'
}

$QsdmRoot = (Resolve-Path $QsdmRoot).Path
. (Join-Path $QsdmRoot "scripts\lib\qsdm-endpoints.ps1")
$Relay = Get-QsdmEndpointValue -Name "home_gateway_relay" -EnvVar "QSDM_HOME_GATEWAY_RELAY"
$Slot = Get-QsdmEndpointValue -Name "home_gateway_slot" -EnvVar "QSDM_HOME_GATEWAY_SLOT"
$Installer = Join-Path $QsdmRoot "scripts\install_local_stack_task.ps1"
$LogPath = Join-Path $QsdmRoot "source\.cache\local-validator\local-stack-task-install.log"

if (-not (Test-IsAdmin)) {
    $args = "-NoProfile -ExecutionPolicy Bypass -NoExit -File $(Quote-Arg $PSCommandPath) -QsdmRoot $(Quote-Arg $QsdmRoot)"
    Start-Process -FilePath "powershell.exe" -Verb RunAs -ArgumentList $args
    Write-Host "Windows administrator prompt requested."
    exit 0
}

try {
    if (-not (Test-Path -LiteralPath $Installer)) {
        throw "Missing installer: $Installer"
    }
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $Installer `
        -QsdmRoot $QsdmRoot `
        -Relay $Relay `
        -Slot $Slot `
        -Highest `
        -AtStartup `
        -AsSystem `
        -RemoveStartupFallback `
        -NoStartupFallback

    Write-Host ""
    Write-Host "QSDM elevated Scheduled Task install finished."
    Write-Host "Log: $LogPath"
    if (-not $NoPause) {
        Write-Host "Leaving this admin window open for diagnostics."
    }
} catch {
    Write-Host ""
    Write-Host "QSDM elevated Scheduled Task install failed:" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    Write-Host "Log: $LogPath"
    if (-not $NoPause) {
        Read-Host "Press Enter to close"
    }
    exit 1
}
